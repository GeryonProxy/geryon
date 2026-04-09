package mssql

import (
	"bytes"
	"testing"

	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

func TestCodec_Protocol(t *testing.T) {
	c := NewCodec()
	if c.Protocol() != common.ProtocolMSSQL {
		t.Errorf("Protocol = %v, want ProtocolMSSQL", c.Protocol())
	}
}

func TestCodec_ReadMessage(t *testing.T) {
	c := NewCodec()

	// Valid TDS packet: 8 byte header + payload
	// Header: type=0x01(SQLBatch), status=0x01, length=11 (big-endian at bytes 2-3)
	header := []byte{0x01, 0x01, 0x00, 0x0b, 0x00, 0x00, 0x00, 0x00}
	payload := []byte{0x00, 'S', 'Q'} // TokenTypeSQLText + "SQ"
	data := append(header, payload...)

	msg, err := c.ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if msg.Type != 0x01 {
		t.Errorf("Type = 0x%02x, want 0x01", msg.Type)
	}
	if msg.Length != 11 {
		t.Errorf("Length = %d, want 11", msg.Length)
	}
	if msg.Direction != common.Frontend {
		t.Error("Direction should be Frontend")
	}
}

func TestCodec_ReadMessage_TooSmall(t *testing.T) {
	c := NewCodec()
	_, err := c.ReadMessage(bytes.NewReader([]byte{0x01, 0x00}))
	if err == nil {
		t.Error("Should fail for header too small")
	}
}

func TestCodec_ReadMessage_InvalidLength(t *testing.T) {
	c := NewCodec()
	// Length < 8
	header := []byte{0x01, 0x01, 0x00, 0x00, 0x00, 0x05, 0x00, 0x00}
	_, err := c.ReadMessage(bytes.NewReader(header))
	if err == nil {
		t.Error("Should fail for length < 8")
	}
}

func TestCodec_ReadMessage_TooLarge(t *testing.T) {
	c := NewCodec()
	// Length > 16MB
	header := []byte{0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}
	_, err := c.ReadMessage(bytes.NewReader(header))
	if err == nil {
		t.Error("Should fail for too large payload")
	}
}

func TestCodec_ReadMessage_PayloadReadError(t *testing.T) {
	c := NewCodec()
	// Header claims 12 bytes but only provides 8
	header := []byte{0x01, 0x01, 0x00, 0x00, 0x00, 0x0c, 0x00, 0x00}
	_, err := c.ReadMessage(bytes.NewReader(header))
	if err == nil {
		t.Error("Should fail when payload cannot be fully read")
	}
}

func TestCodec_WriteMessage(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{
		Raw: []byte{0x01, 0x01, 0x00, 0x00, 0x00, 0x08, 0x00, 0x00},
	}
	var buf bytes.Buffer
	if err := c.WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}
	if buf.Len() != 8 {
		t.Errorf("Wrote %d bytes, want 8", buf.Len())
	}
}

func TestCodec_IsStartup(t *testing.T) {
	c := NewCodec()
	if !c.IsStartup(&common.Message{Type: PacketTypePreLogin}) {
		t.Error("PreLogin should be startup")
	}
	if c.IsStartup(&common.Message{Type: PacketTypeSQLBatch}) {
		t.Error("SQLBatch should not be startup")
	}
}

func TestCodec_IsTerminate(t *testing.T) {
	c := NewCodec()
	if !c.IsTerminate(&common.Message{Type: PacketTypeAttention}) {
		t.Error("Attention should be terminate")
	}
	if c.IsTerminate(&common.Message{Type: PacketTypeSQLBatch}) {
		t.Error("SQLBatch should not be terminate")
	}
}

func TestCodec_IsQuery(t *testing.T) {
	c := NewCodec()
	if !c.IsQuery(&common.Message{Type: PacketTypeSQLBatch}) {
		t.Error("SQLBatch should be query")
	}
	if c.IsQuery(&common.Message{Type: PacketTypeRPC}) {
		t.Error("RPC should not be query")
	}
}

func TestCodec_IsTransactionBegin(t *testing.T) {
	c := NewCodec()
	cases := []struct {
		payload []byte
		want    bool
	}{
		{makeSQLBatch("BEGIN"), true},
		{makeSQLBatch("BEGIN TRANSACTION"), true},
		{makeSQLBatch("BEGIN TRAN"), true},
		{makeSQLBatch("SELECT 1"), false},
	}
	for _, tc := range cases {
		msg := &common.Message{Type: PacketTypeSQLBatch, Payload: tc.payload}
		if got := c.IsTransactionBegin(msg); got != tc.want {
			t.Errorf("IsTransactionBegin(%q) = %v, want %v", tc.payload, got, tc.want)
		}
	}
	if c.IsTransactionBegin(&common.Message{Type: PacketTypeRPC}) {
		t.Error("Non-SQLBatch should not be transaction begin")
	}
}

func TestCodec_IsTransactionEnd(t *testing.T) {
	c := NewCodec()
	cases := []struct {
		payload []byte
		want    bool
	}{
		{makeSQLBatch("COMMIT"), true},
		{makeSQLBatch("ROLLBACK"), true},
		{makeSQLBatch("COMMIT TRANSACTION"), true},
		{makeSQLBatch("ROLLBACK TRAN"), true},
		{makeSQLBatch("SELECT 1"), false},
	}
	for _, tc := range cases {
		msg := &common.Message{Type: PacketTypeSQLBatch, Payload: tc.payload}
		if got := c.IsTransactionEnd(msg); got != tc.want {
			t.Errorf("IsTransactionEnd(%q) = %v, want %v", tc.payload, got, tc.want)
		}
	}
	if c.IsTransactionEnd(&common.Message{Type: PacketTypeRPC}) {
		t.Error("Non-SQLBatch should not be transaction end")
	}
}

func TestCodec_IsPrepare(t *testing.T) {
	c := NewCodec()
	if !c.IsPrepare(makeRPCMessage("sp_prepare")) {
		t.Error("sp_prepare RPC should be prepare")
	}
	if c.IsPrepare(makeRPCMessage("sp_execute")) {
		t.Error("sp_execute should not be prepare")
	}
	if c.IsPrepare(&common.Message{Type: PacketTypeSQLBatch}) {
		t.Error("Non-RPC should not be prepare")
	}
}

func TestCodec_IsBind(t *testing.T) {
	c := NewCodec()
	if c.IsBind(&common.Message{Type: PacketTypeSQLBatch}) {
		t.Error("TDS has no bind message")
	}
}

func TestCodec_IsExecute(t *testing.T) {
	c := NewCodec()
	if !c.IsExecute(makeRPCMessage("sp_execute")) {
		t.Error("sp_execute RPC should be execute")
	}
	if c.IsExecute(makeRPCMessage("sp_prepare")) {
		t.Error("sp_prepare should not be execute")
	}
	if c.IsExecute(&common.Message{Type: PacketTypeSQLBatch}) {
		t.Error("Non-RPC should not be execute")
	}
}

func TestCodec_IsClose(t *testing.T) {
	c := NewCodec()
	if !c.IsClose(makeRPCMessage("sp_unprepare")) {
		t.Error("sp_unprepare RPC should be close")
	}
	if c.IsClose(makeRPCMessage("sp_execute")) {
		t.Error("sp_execute should not be close")
	}
	if c.IsClose(&common.Message{Type: PacketTypeSQLBatch}) {
		t.Error("Non-RPC should not be close")
	}
}

func TestCodec_IsSync(t *testing.T) {
	c := NewCodec()
	if c.IsSync(&common.Message{Type: PacketTypeSQLBatch}) {
		t.Error("TDS has no sync message")
	}
}

func TestCodec_ExtractQuery(t *testing.T) {
	c := NewCodec()

	// SQLBatch with TokenTypeSQLText
	sqlBatch := &common.Message{Type: PacketTypeSQLBatch, Payload: makeSQLBatch("SELECT 1")}
	q, err := c.ExtractQuery(sqlBatch)
	if err != nil {
		t.Fatalf("ExtractQuery SQLBatch failed: %v", err)
	}
	if q != "SELECT 1" {
		t.Errorf("Query = %q, want %q", q, "SELECT 1")
	}

	// RPC
	rpc := makeRPCMessage("sp_execute")
	q, err = c.ExtractQuery(rpc)
	if err != nil {
		t.Fatalf("ExtractQuery RPC failed: %v", err)
	}
	if q != "sp_execute" {
		t.Errorf("RPC Query = %q, want %q", q, "sp_execute")
	}

	// Unsupported type
	_, err = c.ExtractQuery(&common.Message{Type: PacketTypeAttention})
	if err == nil {
		t.Error("Should error for unsupported message type")
	}
}

func TestCodec_GenerateResetSequence(t *testing.T) {
	c := NewCodec()
	seq := c.GenerateResetSequence()
	if len(seq) != 1 {
		t.Fatalf("Reset sequence has %d messages, want 1", len(seq))
	}
	if seq[0].Type != PacketTypeRPC {
		t.Errorf("Reset message type = 0x%02x, want 0x03", seq[0].Type)
	}
}

func TestCreatePreLogin(t *testing.T) {
	data := CreatePreLogin(EncryptMode(EncryptOn), "mydb")
	if len(data) < 8 {
		t.Fatalf("PreLogin too short: %d bytes", len(data))
	}
	// Check header
	if data[0] != PacketTypePreLogin {
		t.Errorf("Packet type = 0x%02x, want 0x12", data[0])
	}
}

func TestCreatePreLogin_NoInstance(t *testing.T) {
	data := CreatePreLogin(EncryptMode(EncryptOff), "")
	if len(data) < 8 {
		t.Fatalf("PreLogin too short: %d bytes", len(data))
	}
}

func TestCreateLogin7(t *testing.T) {
	data := CreateLogin7("user", "pass", "myapp", "server", "mydb")
	if len(data) < 8 {
		t.Fatalf("Login7 too short: %d bytes", len(data))
	}
	if data[0] != PacketTypeLogin7 {
		t.Errorf("Packet type = 0x%02x, want 0x10", data[0])
	}
}

func TestCreateSQLBatch(t *testing.T) {
	data := CreateSQLBatch("SELECT 1")
	if len(data) < 8 {
		t.Fatalf("SQLBatch too short: %d bytes", len(data))
	}
	if data[0] != PacketTypeSQLBatch {
		t.Errorf("Packet type = 0x%02x, want 0x01", data[0])
	}
	// Payload should contain UTF-16LE encoded "SELECT 1"
	payload := data[8:]
	if len(payload)%2 != 0 {
		t.Error("UTF-16LE payload should have even length")
	}
	// Check first char 'S'
	if payload[0] != 'S' || payload[1] != 0 {
		t.Errorf("First char encoding wrong: %02x %02x", payload[0], payload[1])
	}
}

func TestParseTokenStream(t *testing.T) {
	// Done token: 1 type + 2 status + 2 curCmd + 4 rowCount = 9 bytes
	data := []byte{
		TokenTypeDone, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	tokens, err := ParseTokenStream(data)
	if err != nil {
		t.Fatalf("ParseTokenStream failed: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("Token count = %d, want 1", len(tokens))
	}
	if tokens[0].Type != TokenTypeDone {
		t.Errorf("Token type = 0x%02x, want 0xFD", tokens[0].Type)
	}
}

func TestParseTokenStream_Multiple(t *testing.T) {
	// Row: just type byte, Done: 9 bytes
	data := []byte{
		TokenTypeRow,                                    // 1 byte
		TokenTypeDone, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // 9 bytes
	}
	tokens, err := ParseTokenStream(data)
	if err != nil {
		t.Fatalf("ParseTokenStream failed: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("Token count = %d, want 2", len(tokens))
	}
}

func TestParseTokenStream_Error(t *testing.T) {
	// Error: 1 type + 2 length + length bytes
	data := []byte{
		TokenTypeError, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	tokens, err := ParseTokenStream(data)
	if err != nil {
		t.Fatalf("ParseTokenStream failed: %v", err)
	}
	if len(tokens) != 1 || !tokens[0].IsError() {
		t.Error("Should have one error token")
	}
}

func TestParseTokenStream_UnknownType(t *testing.T) {
	data := []byte{0x99}
	_, err := ParseTokenStream(data)
	if err == nil {
		t.Error("Should fail for unknown token type")
	}
}

func TestParseTokenStream_Truncated(t *testing.T) {
	// Done token needs 7 bytes, only give 3
	data := []byte{TokenTypeDone, 0x00, 0x00}
	_, err := ParseTokenStream(data)
	if err == nil {
		t.Error("Should fail for truncated data")
	}
}

func TestToken_IsFinalToken(t *testing.T) {
	if !(Token{Type: TokenTypeDone}).IsFinalToken() {
		t.Error("TokenTypeDone should be final")
	}
	if !(Token{Type: TokenTypeDoneInProc}).IsFinalToken() {
		t.Error("TokenTypeDoneInProc should be final")
	}
	if !(Token{Type: TokenTypeDoneProc}).IsFinalToken() {
		t.Error("TokenTypeDoneProc should be final")
	}
	if (Token{Type: TokenTypeSQLText}).IsFinalToken() {
		t.Error("TokenTypeSQLText should not be final")
	}
}

func TestToken_IsError(t *testing.T) {
	if !(Token{Type: TokenTypeError}).IsError() {
		t.Error("TokenTypeError should be error")
	}
	if (Token{Type: TokenTypeInfo}).IsError() {
		t.Error("TokenTypeInfo should not be error")
	}
}

func TestConstants(t *testing.T) {
	if PacketTypeSQLBatch != 0x01 {
		t.Errorf("PacketTypeSQLBatch = 0x%02x, want 0x01", PacketTypeSQLBatch)
	}
	if PacketTypeRPC != 0x03 {
		t.Errorf("PacketTypeRPC = 0x%02x, want 0x03", PacketTypeRPC)
	}
	if PacketTypePreLogin != 0x12 {
		t.Errorf("PacketTypePreLogin = 0x%02x, want 0x12", PacketTypePreLogin)
	}
	if PacketTypeLogin7 != 0x10 {
		t.Errorf("PacketTypeLogin7 = 0x%02x, want 0x10", PacketTypeLogin7)
	}
	if StatusEndOfMessage != 0x01 {
		t.Errorf("StatusEndOfMessage = 0x%02x, want 0x01", StatusEndOfMessage)
	}
	if EncryptOn != 0x01 {
		t.Errorf("EncryptOn = 0x%02x, want 0x01", EncryptOn)
	}
	if MaxPayloadLen != 1<<24 {
		t.Errorf("MaxPayloadLen = %d, want %d", MaxPayloadLen, 1<<24)
	}
}

// Helpers

func utf16LE(s string) []byte {
	var buf []byte
	for _, c := range s {
		buf = append(buf, byte(c), 0)
	}
	return buf
}

func makeSQLBatch(sql string) []byte {
	// TokenTypeSQLText (0x00) + UTF-16LE encoded SQL
	return append([]byte{TokenTypeSQLText}, utf16LE(sql)...)
}

func makeRPCMessage(proc string) *common.Message {
	return &common.Message{
		Type:    PacketTypeRPC,
		Payload: []byte(proc),
	}
}
