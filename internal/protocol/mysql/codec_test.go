package mysql

import (
	"bytes"
	"testing"

	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

func TestCodec_Protocol(t *testing.T) {
	c := NewCodec()
	if c.Protocol() != common.ProtocolMySQL {
		t.Errorf("Protocol = %v, want ProtocolMySQL", c.Protocol())
	}
}

func TestCodec_ReadMessage(t *testing.T) {
	c := NewCodec()

	// COM_QUERY packet: 3 bytes length + 1 byte seq + payload
	// Length = 9 (0x09), payload is ComQuery + "SELECT 1"
	buf := bytes.NewBuffer([]byte{0x09, 0x00, 0x00, 0x00, ComQuery, 'S', 'E', 'L', 'E', 'C', 'T', ' ', '1'})
	msg, err := c.ReadMessage(buf)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if msg.Type != ComQuery {
		t.Errorf("Type = 0x%02x, want 0x03", msg.Type)
	}
	if msg.Length != 9 {
		t.Errorf("Length = %d, want 9", msg.Length)
	}
}

func TestCodec_ReadMessage_TooLarge(t *testing.T) {
	c := NewCodec()

	// Packet > 16MB
	buf := bytes.NewBuffer([]byte{0xff, 0xff, 0xff, 0})
	_, err := c.ReadMessage(buf)
	if err == nil {
		t.Error("Should fail for too large packet")
	}
}

func TestCodec_ReadMessage_EOF(t *testing.T) {
	c := NewCodec()
	_, err := c.ReadMessage(bytes.NewBuffer([]byte{}))
	if err == nil {
		t.Error("Should fail on empty buffer")
	}
}

func TestCodec_WriteMessage(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{
		Raw: []byte{0x05, 0, 0, 0, ComPing},
	}
	var buf bytes.Buffer
	if err := c.WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}
}

func TestCodec_IsTerminate(t *testing.T) {
	c := NewCodec()
	if !c.IsTerminate(&common.Message{Type: ComQuit}) {
		t.Error("ComQuit should be terminate")
	}
	if c.IsTerminate(&common.Message{Type: ComQuery}) {
		t.Error("ComQuery should not be terminate")
	}
}

func TestCodec_IsQuery(t *testing.T) {
	c := NewCodec()
	if !c.IsQuery(&common.Message{Type: ComQuery}) {
		t.Error("ComQuery should be query")
	}
}

func TestCodec_IsTransactionBegin(t *testing.T) {
	c := NewCodec()
	cases := []struct {
		payload []byte
		want    bool
	}{
		{[]byte{ComQuery, 'B', 'E', 'G', 'I', 'N'}, true},
		{[]byte{ComQuery, 'S', 'T', 'A', 'R', 'T', ' ', 'T', 'R', 'A', 'N', 'S', 'A', 'C', 'T', 'I', 'O', 'N'}, true},
		{[]byte{ComQuery, 'S', 'E', 'L', 'E', 'C', 'T', 1}, false},
	}
	for _, tc := range cases {
		msg := &common.Message{Type: ComQuery, Payload: tc.payload}
		if got := c.IsTransactionBegin(msg); got != tc.want {
			t.Errorf("IsTransactionBegin(%q) = %v, want %v", tc.payload, got, tc.want)
		}
	}
}

func TestCodec_IsTransactionEnd(t *testing.T) {
	c := NewCodec()
	cases := []struct {
		payload []byte
		want    bool
	}{
		{[]byte{ComQuery, 'C', 'O', 'M', 'M', 'I', 'T'}, true},
		{[]byte{ComQuery, 'R', 'O', 'L', 'L', 'B', 'A', 'C', 'K'}, true},
		{[]byte{ComQuery, 'S', 'E', 'L', 'E', 'C', 'T', 1}, false},
	}
	for _, tc := range cases {
		msg := &common.Message{Type: ComQuery, Payload: tc.payload}
		if got := c.IsTransactionEnd(msg); got != tc.want {
			t.Errorf("IsTransactionEnd(%q) = %v, want %v", tc.payload, got, tc.want)
		}
	}
}

func TestCodec_IsPrepare(t *testing.T) {
	c := NewCodec()
	if !c.IsPrepare(&common.Message{Type: ComStmtPrepare}) {
		t.Error("ComStmtPrepare should be prepare")
	}
}

func TestCodec_IsExecute(t *testing.T) {
	c := NewCodec()
	if !c.IsExecute(&common.Message{Type: ComStmtExecute}) {
		t.Error("ComStmtExecute should be execute")
	}
}

func TestCodec_IsClose(t *testing.T) {
	c := NewCodec()
	if !c.IsClose(&common.Message{Type: ComStmtClose}) {
		t.Error("ComStmtClose should be close")
	}
}

func TestCodec_IsSync(t *testing.T) {
	c := NewCodec()
	if !c.IsSync(&common.Message{Type: ComStmtReset}) {
		t.Error("ComStmtReset should be sync")
	}
}

func TestCodec_IsBind(t *testing.T) {
	c := NewCodec()
	if !c.IsBind(&common.Message{Type: ComStmtSendLongData}) {
		t.Error("ComStmtSendLongData should be bind")
	}
}

func TestCodec_ExtractQuery(t *testing.T) {
	c := NewCodec()

	// COM_QUERY
	msg := &common.Message{Type: ComQuery, Payload: []byte{ComQuery, 'S', 'E', 'L', 'E', 'C', 'T', ' ', '1'}}
	q, err := c.ExtractQuery(msg)
	if err != nil {
		t.Fatalf("ExtractQuery failed: %v", err)
	}
	if q != "SELECT 1" {
		t.Errorf("Query = %q, want %q", q, "SELECT 1")
	}

	// COM_STMT_PREPARE
	msg2 := &common.Message{Type: ComStmtPrepare, Payload: []byte{ComStmtPrepare, 'S', 'E', 'L', 'E', 'C', 'T', ' ', '?'}}
	q, err = c.ExtractQuery(msg2)
	if err != nil {
		t.Fatalf("ExtractQuery Prepare failed: %v", err)
	}
	if q != "SELECT ?" {
		t.Errorf("Query = %q, want %q", q, "SELECT ?")
	}

	// Invalid type
	_, err = c.ExtractQuery(&common.Message{Type: ComPing})
	if err == nil {
		t.Error("Should error for non-query type")
	}
}

func TestCodec_GenerateResetSequence(t *testing.T) {
	c := NewCodec()
	seq := c.GenerateResetSequence()
	if len(seq) != 1 {
		t.Fatalf("Reset sequence has %d messages, want 1", len(seq))
	}
}

func TestCreateHandshakeV10(t *testing.T) {
	authData := make([]byte, 20)
	for i := range authData {
		authData[i] = byte(i)
	}
	data := CreateHandshakeV10("8.0.0", 1, authData, ClientProtocol41)
	if len(data) < 32 {
		t.Errorf("Handshake too short: %d bytes", len(data))
	}
	if data[0] != 10 {
		t.Errorf("Protocol version = %d, want 10", data[0])
	}
}

func TestParseHandshakeResponse(t *testing.T) {
	// Minimal handshake response (38 bytes)
	// Capability flags: ClientProtocol41 | ClientSecureConnection = 512 | 32768 = 33280
	data := make([]byte, 38)
	data[0] = 0x00 // 33280 in LE
	data[1] = 0x82
	data[2] = 0x00
	data[3] = 0x00
	// Max packet size (4 bytes)
	data[4] = 0
	data[5] = 0
	data[6] = 0
	data[7] = 0
	// Character set
	data[8] = 255
	// Reserved (23 bytes, already zeroed)
	// Username at offset 32 (null-terminated)
	data[32] = 't'
	data[33] = 'e'
	data[34] = 's'
	data[35] = 't'
	data[36] = 0 // null terminator
	// Auth response length (0 bytes)
	data[37] = 0

	resp, err := ParseHandshakeResponse(data)
	if err != nil {
		t.Fatalf("ParseHandshakeResponse failed: %v", err)
	}
	if resp.Username != "test" {
		t.Errorf("Username = %q, want %q", resp.Username, "test")
	}
}

func TestCreateOKPacket(t *testing.T) {
	data := CreateOKPacket(1, 0, ServerStatusAutocommit)
	if len(data) == 0 {
		t.Error("OK packet should not be empty")
	}
	if data[0] != 0x00 {
		t.Error("OK packet should start with 0x00")
	}
}

func TestCreateERRPacket(t *testing.T) {
	data := CreateERRPacket(1045, "28000", "Access denied")
	if len(data) == 0 {
		t.Error("ERR packet should not be empty")
	}
	if data[0] != 0xff {
		t.Error("ERR packet should start with 0xff")
	}
}

func TestScramblePassword(t *testing.T) {
	scramble := []byte("1234567812345678")
	result := scramblePassword("secret", scramble)
	if len(result) != 20 {
		t.Errorf("Scramble length = %d, want 20", len(result))
	}
	// Empty password
	if scramblePassword("", scramble) != nil {
		t.Error("Empty password should return nil")
	}
}

func TestScrambleCachingSHA2Password(t *testing.T) {
	scramble := make([]byte, 20)
	result := scrambleCachingSHA2Password("secret", scramble)
	if len(result) != 32 {
		t.Errorf("SHA2 scramble length = %d, want 32", len(result))
	}
}

func TestReadLengthEncodedInt(t *testing.T) {
	cases := []struct {
		data    []byte
		want    uint64
		wantNull bool
		wantN   int
	}{
		{[]byte{0xfb}, 0, true, 1},       // NULL
		{[]byte{0x05}, 5, false, 1},      // 1-byte
		{[]byte{0xfc, 0x80, 0x00}, 128, false, 3}, // 2-byte
		{[]byte{0xfd, 0x00, 0x00, 0x01}, 65536, false, 4}, // 3-byte
	}
	for _, tc := range cases {
		val, isNull, n := readLengthEncodedInt(tc.data)
		if val != tc.want || isNull != tc.wantNull || n != tc.wantN {
			t.Errorf("readLengthEncodedInt(%v) = (%d, %v, %d), want (%d, %v, %d)",
				tc.data, val, isNull, n, tc.want, tc.wantNull, tc.wantN)
		}
	}
}

func TestAppendLengthEncodedInt(t *testing.T) {
	buf := appendLengthEncodedInt(nil, 42)
	if len(buf) != 1 || buf[0] != 42 {
		t.Errorf("appendLengthEncodedInt(42) = %v, want [42]", buf)
	}

	buf = appendLengthEncodedInt(nil, 300)
	if len(buf) != 3 || buf[0] != 0xfc {
		t.Errorf("appendLengthEncodedInt(300) = %v, want 3 bytes starting with 0xfc", buf)
	}
}

// TestCodec_IsStartup tests the IsStartup function
func TestCodec_IsStartup(t *testing.T) {
	c := NewCodec()

	tests := []struct {
		name string
		msg  *common.Message
		want bool
	}{
		{
			name: "ssl_request_pattern",
			msg:  &common.Message{Payload: []byte{0x20, 0x00, 0x00, 0x00}},
			want: true,
		},
		{
			name: "too_short",
			msg:  &common.Message{Payload: []byte{0x20, 0x00}},
			want: false,
		},
		{
			name: "not_ssl_request",
			msg:  &common.Message{Payload: []byte{0x00, 0x00, 0x00, 0x00}},
			want: false,
		},
		{
			name: "empty_payload",
			msg:  &common.Message{Payload: []byte{}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.IsStartup(tt.msg)
			if got != tt.want {
				t.Errorf("IsStartup() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCreateAuthSwitchRequest tests the CreateAuthSwitchRequest function
func TestCreateAuthSwitchRequest(t *testing.T) {
	tests := []struct {
		name       string
		pluginName string
		authData   []byte
		wantLen    int
		wantFirst  byte
	}{
		{
			name:       "mysql_native_password",
			pluginName: "mysql_native_password",
			authData:   []byte{0x01, 0x02, 0x03, 0x04},
			wantLen:    len("mysql_native_password") + 1 + 4 + 1, // plugin + null + authData + status byte
			wantFirst:  0xfe,
		},
		{
			name:       "caching_sha2_password",
			pluginName: "caching_sha2_password",
			authData:   []byte("random_auth_data"),
			wantLen:    len("caching_sha2_password") + 1 + 16 + 1,
			wantFirst:  0xfe,
		},
		{
			name:       "empty_auth_data",
			pluginName: "test_plugin",
			authData:   []byte{},
			wantLen:    len("test_plugin") + 1 + 0 + 1,
			wantFirst:  0xfe,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CreateAuthSwitchRequest(tt.pluginName, tt.authData)
			if result[0] != tt.wantFirst {
				t.Errorf("first byte = 0x%02x, want 0x%02x", result[0], tt.wantFirst)
			}
			if len(result) != tt.wantLen {
				t.Errorf("length = %d, want %d", len(result), tt.wantLen)
			}
			// Verify plugin name is in result
			if !bytes.Contains(result, []byte(tt.pluginName)) {
				t.Error("result should contain plugin name")
			}
		})
	}
}
