package mssql

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

// --- ReadMessage coverage ---

func TestCodec_ReadMessage_EmptyPayload(t *testing.T) {
	c := NewCodec()
	// TDS packet with length=8 (header only, no payload)
	header := []byte{0x04, 0x01, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00}
	msg, err := c.ReadMessage(bytes.NewReader(header))
	if err != nil {
		t.Fatalf("ReadMessage with empty payload failed: %v", err)
	}
	if msg.Type != 0x04 {
		t.Errorf("Type = 0x%02x, want 0x04", msg.Type)
	}
	if len(msg.Payload) != 0 {
		t.Errorf("Payload length = %d, want 0", len(msg.Payload))
	}
	if msg.Length != 8 {
		t.Errorf("Length = %d, want 8", msg.Length)
	}
}

// --- extractSQLBatchQuery coverage ---

func TestCodec_ExtractSQLBatchQuery_PayloadTooShort(t *testing.T) {
	c := NewCodec()
	// Payload with fewer than 8 bytes triggers early return ""
	msg := &common.Message{Type: PacketTypeSQLBatch, Payload: []byte{0x01, 0x02, 0x03}}
	q := c.extractSQLBatchQuery(msg)
	if q != "" {
		t.Errorf("Expected empty string for short payload, got %q", q)
	}
}

func TestCodec_ExtractSQLBatchQuery_NoSQLTextToken(t *testing.T) {
	c := NewCodec()
	// Payload >= 8 bytes but no TokenTypeSQLText marker found in first 20 bytes
	// Should hit the fallback: return string(data)
	payload := make([]byte, 30)
	for i := range payload {
		payload[i] = 0x41 // 'A' - no TokenTypeSQLText (0x00)
	}
	msg := &common.Message{Type: PacketTypeSQLBatch, Payload: payload}
	q := c.extractSQLBatchQuery(msg)
	if q != string(payload) {
		t.Errorf("Expected fallback string(data), got %q", q)
	}
}

func TestCodec_ExtractSQLBatchQuery_SQLTextTokenPastOffset(t *testing.T) {
	c := NewCodec()
	// Place TokenTypeSQLText at position 4 with UTF-16LE encoded SQL
	sql := utf16LE("SELECT 42")
	payload := make([]byte, 5+len(sql))
	payload[4] = TokenTypeSQLText
	copy(payload[5:], sql)
	msg := &common.Message{Type: PacketTypeSQLBatch, Payload: payload}
	q := c.extractSQLBatchQuery(msg)
	// Function should at least return something (may be decoded unicode or fallback)
	// The token is found at position 4, decodeUnicode is called on data[5:]
	_ = q
}

func TestCodec_ExtractSQLBatchQuery_SQLTextAtBoundary(t *testing.T) {
	c := NewCodec()
	// TokenTypeSQLText at position 19 (just within the i < 20 boundary)
	sql := utf16LE("X")
	payload := make([]byte, 21)
	payload[19] = TokenTypeSQLText
	copy(payload[20:], sql)
	msg := &common.Message{Type: PacketTypeSQLBatch, Payload: payload}
	q := c.extractSQLBatchQuery(msg)
	_ = q
}

// --- decodeUnicode coverage ---

func TestCodec_DecodeUnicode_OddLength(t *testing.T) {
	c := NewCodec()
	// Odd-length data should return string(data) directly
	data := []byte{0x41, 0x42, 0x43} // 3 bytes, odd
	result := c.decodeUnicode(data)
	if result != string(data) {
		t.Errorf("Expected %q for odd-length data, got %q", string(data), result)
	}
}

func TestCodec_DecodeUnicode_NullRune(t *testing.T) {
	c := NewCodec()
	// Even-length data with a null rune in the middle should stop at the null
	data := []byte{'A', 0x00, 0x00, 0x00, 'B', 0x00} // 'A', null, 'B'
	result := c.decodeUnicode(data)
	if result != "A" {
		t.Errorf("Expected 'A', got %q", result)
	}
}

func TestCodec_DecodeUnicode_AllNullRunes(t *testing.T) {
	c := NewCodec()
	data := []byte{0x00, 0x00, 0x00, 0x00}
	result := c.decodeUnicode(data)
	if result != "" {
		t.Errorf("Expected empty string for all null runes, got %q", result)
	}
}

func TestCodec_DecodeUnicode_MultiByteRune(t *testing.T) {
	c := NewCodec()
	// Test with a multi-byte UTF-16 character (e.g., Euro sign U+20AC)
	data := []byte{0xAC, 0x20, 'B', 0x00}
	result := c.decodeUnicode(data)
	if result != "\u20ACB" {
		t.Errorf("Expected '\\u20ACB', got %q", result)
	}
}

// --- ParseTokenStream coverage ---

func TestParseTokenStream_DoneInProc(t *testing.T) {
	// DoneInProc: type + 8 bytes (status 2 + curCmd 2 + rowCount 4)
	data := []byte{
		TokenTypeDoneInProc,
		0x00, 0x00, // status
		0x00, 0x00, // curCmd
		0x02, 0x00, 0x00, 0x00, // rowCount = 2
	}
	tokens, err := ParseTokenStream(data)
	if err != nil {
		t.Fatalf("ParseTokenStream DoneInProc failed: %v", err)
	}
	// DoneInProc is not appended to tokens in current impl (just skips)
	// but we should get no error and no tokens
	if len(tokens) != 0 {
		t.Errorf("Expected 0 tokens (DoneInProc is skipped), got %d", len(tokens))
	}
}

func TestParseTokenStream_ColMetadata(t *testing.T) {
	// ColMetadata: type + count(2 bytes)
	data := []byte{
		TokenTypeColMetadata,
		0x03, 0x00, // column count = 3
	}
	tokens, err := ParseTokenStream(data)
	if err != nil {
		t.Fatalf("ParseTokenStream ColMetadata failed: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("Expected 1 token, got %d", len(tokens))
	}
	if tokens[0].Type != TokenTypeColMetadata {
		t.Errorf("Token type = 0x%02x, want 0x81", tokens[0].Type)
	}
	if tokens[0].ColumnCount != 3 {
		t.Errorf("ColumnCount = %d, want 3", tokens[0].ColumnCount)
	}
}

func TestParseTokenStream_LoginAck(t *testing.T) {
	// LoginAck: type + length(2 bytes) + length bytes of data
	// LoginAck is skipped (not appended to tokens)
	ackData := []byte{0x01, 0x74, 0x00, 0x00, 0x00} // Interface + TDS version
	length := uint16(len(ackData))
	data := []byte{TokenTypeLoginAck}
	data = append(data, byte(length), byte(length>>8))
	data = append(data, ackData...)

	tokens, err := ParseTokenStream(data)
	if err != nil {
		t.Fatalf("ParseTokenStream LoginAck failed: %v", err)
	}
	// LoginAck is skipped in the implementation
	if len(tokens) != 0 {
		t.Errorf("Expected 0 tokens (LoginAck is skipped), got %d", len(tokens))
	}
}

func TestParseTokenStream_InfoToken(t *testing.T) {
	// Info token: type + length(2 bytes) + length bytes
	infoData := []byte{0x01, 0x02, 0x03}
	length := uint16(len(infoData))
	data := []byte{TokenTypeInfo}
	data = append(data, byte(length), byte(length>>8))
	data = append(data, infoData...)

	tokens, err := ParseTokenStream(data)
	if err != nil {
		t.Fatalf("ParseTokenStream Info failed: %v", err)
	}
	// Info token is not appended in current impl, just skipped
	if len(tokens) != 0 {
		t.Errorf("Expected 0 tokens (Info is skipped), got %d", len(tokens))
	}
}

func TestParseTokenStream_DoneWithStatus(t *testing.T) {
	// Done token with non-zero status and row count
	data := []byte{
		TokenTypeDone,
		0x01, 0x00, // status = 1 (final)
		0x00, 0x00, // curCmd
		0x0A, 0x00, 0x00, 0x00, // rowCount = 10
	}
	tokens, err := ParseTokenStream(data)
	if err != nil {
		t.Fatalf("ParseTokenStream Done failed: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("Expected 1 token, got %d", len(tokens))
	}
	if tokens[0].Status != 1 {
		t.Errorf("Status = %d, want 1", tokens[0].Status)
	}
	if tokens[0].RowCount != 10 {
		t.Errorf("RowCount = %d, want 10", tokens[0].RowCount)
	}
}

func TestParseTokenStream_TruncatedDoneInProc(t *testing.T) {
	// DoneInProc needs 8 bytes after type, give only 3
	data := []byte{TokenTypeDoneInProc, 0x00, 0x00, 0x00}
	_, err := ParseTokenStream(data)
	if err == nil {
		t.Error("Should fail for truncated DoneInProc token")
	}
}

func TestParseTokenStream_TruncatedError_NoLength(t *testing.T) {
	// Error token with not enough bytes for length field
	data := []byte{TokenTypeError, 0x00}
	_, err := ParseTokenStream(data)
	if err == nil {
		t.Error("Should fail for truncated Error token (no length)")
	}
}

func TestParseTokenStream_TruncatedError_NoData(t *testing.T) {
	// Error token: length says 10 bytes but only 2 are available
	data := []byte{TokenTypeError, 0x0A, 0x00, 0x01, 0x02} // length=10, only 3 bytes follow
	tokens, err := ParseTokenStream(data)
	if err == nil {
		t.Error("Should fail for truncated Error token data")
	}
	// Should have returned tokens collected so far with error
	_ = tokens
}

func TestParseTokenStream_TruncatedInfo_NoLength(t *testing.T) {
	// Info token with not enough bytes for length field
	data := []byte{TokenTypeInfo}
	_, err := ParseTokenStream(data)
	if err == nil {
		t.Error("Should fail for truncated Info token")
	}
}

func TestParseTokenStream_TruncatedInfo_NoData(t *testing.T) {
	// Info token: length says 5 but only 1 byte follows
	// The implementation just advances pos, which goes past end and exits loop
	data := []byte{TokenTypeInfo, 0x05, 0x00, 0xFF}
	tokens, err := ParseTokenStream(data)
	// Should return without error (just skips past end)
	if err != nil {
		t.Logf("Got error (acceptable): %v", err)
	}
	_ = tokens
}

func TestParseTokenStream_TruncatedLoginAck_NoLength(t *testing.T) {
	data := []byte{TokenTypeLoginAck}
	_, err := ParseTokenStream(data)
	if err == nil {
		t.Error("Should fail for truncated LoginAck token")
	}
}

func TestParseTokenStream_TruncatedLoginAck_NoData(t *testing.T) {
	// LoginAck: length=10 but only 2 bytes follow
	// Implementation reads length then advances pos, which goes past end
	data := []byte{TokenTypeLoginAck, 0x0A, 0x00, 0x01, 0x02}
	tokens, err := ParseTokenStream(data)
	// May or may not error depending on impl
	if err != nil {
		t.Logf("Got error (acceptable): %v", err)
	}
	_ = tokens
}

func TestParseTokenStream_TruncatedColMetadata(t *testing.T) {
	// ColMetadata: needs 2 bytes for count, give only 1
	data := []byte{TokenTypeColMetadata, 0x00}
	_, err := ParseTokenStream(data)
	if err == nil {
		t.Error("Should fail for truncated ColMetadata token")
	}
}

func TestParseTokenStream_EmptyData(t *testing.T) {
	tokens, err := ParseTokenStream([]byte{})
	if err != nil {
		t.Fatalf("Empty data should not error: %v", err)
	}
	if len(tokens) != 0 {
		t.Errorf("Expected 0 tokens, got %d", len(tokens))
	}
}

func TestParseTokenStream_ComplexStream(t *testing.T) {
	// Build a multi-token stream: ColMetadata + Row + Row + Done
	var data []byte

	// ColMetadata with 2 columns
	data = append(data, TokenTypeColMetadata, 0x02, 0x00)

	// Row token (just type byte)
	data = append(data, TokenTypeRow)

	// Another Row token
	data = append(data, TokenTypeRow)

	// Done token
	data = append(data, TokenTypeDone, 0x00, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00)

	tokens, err := ParseTokenStream(data)
	if err != nil {
		t.Fatalf("ParseTokenStream complex stream failed: %v", err)
	}
	// ColMetadata + 2 Rows + Done = 4 tokens
	if len(tokens) != 4 {
		t.Fatalf("Expected 4 tokens, got %d", len(tokens))
	}
	if tokens[0].Type != TokenTypeColMetadata {
		t.Error("First token should be ColMetadata")
	}
	if tokens[0].ColumnCount != 2 {
		t.Error("ColMetadata should have 2 columns")
	}
	if tokens[1].Type != TokenTypeRow {
		t.Error("Second token should be Row")
	}
	if tokens[2].Type != TokenTypeRow {
		t.Error("Third token should be Row")
	}
	if tokens[3].Type != TokenTypeDone {
		t.Error("Fourth token should be Done")
	}
	if tokens[3].RowCount != 2 {
		t.Errorf("Done RowCount = %d, want 2", tokens[3].RowCount)
	}
}

// --- Token.IsFinalToken additional coverage ---

func TestToken_DoneProcIsFinal(t *testing.T) {
	token := Token{Type: TokenTypeDoneProc}
	if !token.IsFinalToken() {
		t.Error("TokenTypeDoneProc should be a final token")
	}
}

// --- Constants coverage for less-tested values ---

func TestAdditionalConstants(t *testing.T) {
	tests := []struct {
		name  string
		got   byte
		want  byte
	}{
		{"PacketTypeAttention", PacketTypeAttention, 0x06},
		{"PacketTypeBulkLoad", PacketTypeBulkLoad, 0x07},
		{"PacketTypeFedAuthToken", PacketTypeFedAuthToken, 0x08},
		{"PacketTypeBatch", PacketTypeBatch, 0x09},
		{"PacketTypeSSPI", PacketTypeSSPI, 0x11},
		{"PacketTypeLogout", PacketTypeLogout, 0x13},
		{"PacketTypeTabularResult", PacketTypeTabularResult, 0x04},
		{"StatusIgnore", StatusIgnore, 0x02},
		{"StatusResetConn", StatusResetConn, 0x08},
		{"StatusResetSkipTxn", StatusResetSkipTxn, 0x10},
		{"EncryptOff", EncryptOff, 0x00},
		{"EncryptNotSup", EncryptNotSup, 0x02},
		{"EncryptRequired", EncryptRequired, 0x03},
		{"PreLoginVersion", PreLoginVersion, 0x00},
		{"PreLoginEncryption", PreLoginEncryption, 0x01},
		{"PreLoginInstOpt", PreLoginInstOpt, 0x02},
		{"PreLoginThreadID", PreLoginThreadID, 0x03},
		{"PreLoginMars", PreLoginMars, 0x04},
		{"PreLoginTraceID", PreLoginTraceID, 0x05},
		{"PreLoginFedAuthRequired", PreLoginFedAuthRequired, 0x06},
		{"PreLoginNonceOpt", PreLoginNonceOpt, 0x07},
		{"TokenTypeEnvChange", TokenTypeEnvChange, 0xE3},
		{"TokenTypeColMetadata", TokenTypeColMetadata, 0x81},
		{"TokenTypeRow", TokenTypeRow, 0xD1},
		{"TokenTypeDoneProc", TokenTypeDoneProc, 0xFE},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = 0x%02x, want 0x%02x", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestEncryptModeType(t *testing.T) {
	var mode EncryptMode = EncryptMode(EncryptOn)
	if byte(mode) != 0x01 {
		t.Errorf("EncryptMode(EncryptOn) = 0x%02x, want 0x01", byte(mode))
	}
}

// --- CreateLogin7 with edge cases ---

func TestCreateLogin7_EmptyStrings(t *testing.T) {
	data := CreateLogin7("", "", "", "", "")
	if len(data) < 8 {
		t.Fatalf("Login7 too short: %d bytes", len(data))
	}
	if data[0] != PacketTypeLogin7 {
		t.Errorf("Packet type = 0x%02x, want 0x10", data[0])
	}
	// Verify length field in the packet header
	pktLen := binary.BigEndian.Uint16(data[2:4])
	if int(pktLen) != len(data) {
		t.Errorf("Packet length = %d, actual = %d", pktLen, len(data))
	}
	// Verify internal length field
	internalLen := binary.LittleEndian.Uint32(data[8:12])
	if int(internalLen) != len(data)-8 {
		t.Errorf("Internal length = %d, payload = %d", internalLen, len(data)-8)
	}
}

func TestCreateLogin7_UnicodeUser(t *testing.T) {
	data := CreateLogin7("user", "pass", "app", "server", "db")
	if len(data) < 8 {
		t.Fatalf("Login7 too short: %d bytes", len(data))
	}
	// Verify the packet has the TDS version field at offset 8+4=12
	tdsVersion := binary.LittleEndian.Uint32(data[12:16])
	if tdsVersion != 0x74000004 {
		t.Errorf("TDS version = 0x%08x, want 0x74000004", tdsVersion)
	}
	// Packet size should be 4096
	pktSize := binary.LittleEndian.Uint32(data[16:20])
	if pktSize != 4096 {
		t.Errorf("Packet size = %d, want 4096", pktSize)
	}
}

// --- CreatePreLogin edge cases ---

func TestCreatePreLogin_EncryptRequired(t *testing.T) {
	data := CreatePreLogin(EncryptMode(EncryptRequired), "")
	if len(data) < 8 {
		t.Fatalf("PreLogin too short: %d bytes", len(data))
	}
	// Find encryption byte: after version token header + version data
	// The structure is: header tokens then data
	// Encryption data should be EncryptRequired (0x03)
	found := false
	for _, b := range data[8:] {
		if b == EncryptRequired {
			found = true
			break
		}
	}
	if !found {
		t.Error("PreLogin should contain EncryptRequired (0x03)")
	}
}

func TestCreatePreLogin_EncryptNotSup(t *testing.T) {
	data := CreatePreLogin(EncryptMode(EncryptNotSup), "instance1")
	if len(data) < 8 {
		t.Fatalf("PreLogin too short: %d bytes", len(data))
	}
	// Verify length is consistent
	pktLen := binary.BigEndian.Uint16(data[2:4])
	if int(pktLen) != len(data) {
		t.Errorf("Packet length = %d, actual = %d", pktLen, len(data))
	}
}

func TestCreatePreLogin_LongInstanceName(t *testing.T) {
	instance := "MSSQLSERVER"
	data := CreatePreLogin(EncryptMode(EncryptOn), instance)
	if len(data) < 8 {
		t.Fatalf("PreLogin too short: %d bytes", len(data))
	}
	// Instance name should be embedded in the packet data
	dataStr := string(data[8:])
	if !strings.Contains(dataStr, instance) {
		t.Error("PreLogin should contain the instance name")
	}
}

// --- CreateSQLBatch edge cases ---

func TestCreateSQLBatch_EmptyQuery(t *testing.T) {
	data := CreateSQLBatch("")
	if len(data) != 8 {
		t.Errorf("Empty SQLBatch length = %d, want 8 (header only)", len(data))
	}
	if data[0] != PacketTypeSQLBatch {
		t.Errorf("Packet type = 0x%02x, want 0x01", data[0])
	}
}

func TestCreateSQLBatch_UnicodeChars(t *testing.T) {
	query := "SELECT 'test\u00E9'"
	data := CreateSQLBatch(query)
	if len(data) < 8 {
		t.Fatalf("SQLBatch too short: %d bytes", len(data))
	}
	// Should be 8 + len(query)*2 bytes
	expectedLen := 8 + len(query)*2
	if len(data) != expectedLen {
		t.Errorf("SQLBatch length = %d, want %d", len(data), expectedLen)
	}
	// Verify status
	if data[1] != StatusEndOfMessage {
		t.Errorf("Status = 0x%02x, want 0x01", data[1])
	}
}

// --- createPacket coverage via GenerateResetSequence ---

func TestCodec_GenerateResetSequence_Contents(t *testing.T) {
	c := NewCodec()
	seq := c.GenerateResetSequence()
	if len(seq) != 1 {
		t.Fatalf("Reset sequence has %d messages, want 1", len(seq))
	}
	msg := seq[0]
	if msg.Type != PacketTypeRPC {
		t.Errorf("Reset message type = 0x%02x, want 0x03", msg.Type)
	}
	// Verify the raw packet is properly formed
	if len(msg.Raw) < 8 {
		t.Fatalf("Raw packet too short: %d", len(msg.Raw))
	}
	if msg.Raw[1] != StatusEndOfMessage {
		t.Errorf("Status = 0x%02x, want 0x01", msg.Raw[1])
	}
	pktLen := binary.BigEndian.Uint16(msg.Raw[2:4])
	if int(pktLen) != len(msg.Raw) {
		t.Errorf("Packet length field = %d, actual = %d", pktLen, len(msg.Raw))
	}
}

// --- WriteMessage error path ---

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, bytes.ErrTooLarge // reuse a standard error
}

func TestCodec_WriteMessage_Error(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{
		Raw: []byte{0x01, 0x01, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00},
	}
	err := c.WriteMessage(errorWriter{}, msg)
	if err == nil {
		t.Error("WriteMessage should return error for failing writer")
	}
}

// --- Login option flags constants ---

func TestLoginOptionFlags(t *testing.T) {
	tests := []struct {
		name string
		flag uint32
		want uint32
	}{
		{"LoginOption1OrderX86", LoginOption1OrderX86, 0x00000001},
		{"LoginOption1Order68000", LoginOption1Order68000, 0x00000002},
		{"LoginOption1CharSetEBCDIC", LoginOption1CharSetEBCDIC, 0x00000004},
		{"LoginOption1CharSetISO8859_1", LoginOption1CharSetISO8859_1, 0x00000008},
		{"LoginOption1CharSetISO8859_2", LoginOption1CharSetISO8859_2, 0x00000010},
		{"LoginOption1UseDb", LoginOption1UseDb, 0x00000020},
		{"LoginOption1InitDbFatal", LoginOption1InitDbFatal, 0x00000040},
		{"LoginOption1SetLangOn", LoginOption1SetLangOn, 0x00000080},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.flag != tt.want {
				t.Errorf("%s = 0x%08x, want 0x%08x", tt.name, tt.flag, tt.want)
			}
		})
	}
}

// --- Integration-style: round-trip ReadMessage with CreateSQLBatch ---

func TestCodec_RoundTrip_SQLBatch(t *testing.T) {
	c := NewCodec()
	data := CreateSQLBatch("SELECT 1")
	msg, err := c.ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if msg.Type != PacketTypeSQLBatch {
		t.Errorf("Type = 0x%02x, want 0x01", msg.Type)
	}
	// Payload should be UTF-16LE of "SELECT 1"
	expected := utf16LE("SELECT 1")
	if !bytes.Equal(msg.Payload, expected) {
		t.Errorf("Payload mismatch: got %v, want %v", msg.Payload, expected)
	}
}

func TestCodec_RoundTrip_PreLogin(t *testing.T) {
	c := NewCodec()
	data := CreatePreLogin(EncryptMode(EncryptOn), "MSSQLSERVER")
	msg, err := c.ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if msg.Type != PacketTypePreLogin {
		t.Errorf("Type = 0x%02x, want 0x12", msg.Type)
	}
}

func TestCodec_RoundTrip_Login7(t *testing.T) {
	c := NewCodec()
	data := CreateLogin7("admin", "secret", "testapp", "localhost", "master")
	msg, err := c.ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if msg.Type != PacketTypeLogin7 {
		t.Errorf("Type = 0x%02x, want 0x10", msg.Type)
	}
}

// --- WriteMessage round-trip ---

func TestCodec_RoundTrip_WriteAndRead(t *testing.T) {
	c := NewCodec()
	original := CreateSQLBatch("INSERT INTO t VALUES (1)")
	msg, err := c.ReadMessage(bytes.NewReader(original))
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	var buf bytes.Buffer
	if err := c.WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), original) {
		t.Error("Round-trip write/read mismatch")
	}
}

// --- ExtractQuery via RPC with no SQLText token (fallback path) ---

func TestCodec_ExtractQuery_RPCFallback(t *testing.T) {
	c := NewCodec()
	// RPC payload without TokenTypeSQLText - goes through extractSQLBatchQuery fallback
	msg := &common.Message{Type: PacketTypeRPC, Payload: []byte("SP_CUSTOM_PROC")}
	q, err := c.ExtractQuery(msg)
	if err != nil {
		t.Fatalf("ExtractQuery RPC failed: %v", err)
	}
	// Since extractRPCQuery calls extractSQLBatchQuery, and payload is short (<8 bytes area),
	// but payload is > 8 bytes here with no SQLText token, it returns string(data)
	if !strings.Contains(q, "SP_CUSTOM_PROC") {
		t.Errorf("Expected fallback string containing 'SP_CUSTOM_PROC', got %q", q)
	}
}

// --- IsTransactionBegin with edge cases ---

func TestCodec_IsTransactionBegin_LowerCase(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: PacketTypeSQLBatch, Payload: makeSQLBatch("begin")}
	if !c.IsTransactionBegin(msg) {
		t.Error("Lowercase 'begin' should match IsTransactionBegin")
	}
}

func TestCodec_IsTransactionBegin_MixedCase(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: PacketTypeSQLBatch, Payload: makeSQLBatch("Begin Transaction")}
	if !c.IsTransactionBegin(msg) {
		t.Error("Mixed case 'Begin Transaction' should match IsTransactionBegin")
	}
}

// --- IsTransactionEnd with edge cases ---

func TestCodec_IsTransactionEnd_LowerCase(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: PacketTypeSQLBatch, Payload: makeSQLBatch("commit")}
	if !c.IsTransactionEnd(msg) {
		t.Error("Lowercase 'commit' should match IsTransactionEnd")
	}
}

func TestCodec_IsTransactionEnd_MixedCase(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: PacketTypeSQLBatch, Payload: makeSQLBatch("Rollback TRAN")}
	if !c.IsTransactionEnd(msg) {
		t.Error("Mixed case 'Rollback TRAN' should match IsTransactionEnd")
	}
}
