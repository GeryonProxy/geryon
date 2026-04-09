package postgresql

import (
	"bytes"
	"testing"

	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

func TestCodec_Protocol(t *testing.T) {
	c := NewCodec()
	if c.Protocol() != common.ProtocolPostgreSQL {
		t.Errorf("Protocol = %v, want ProtocolPostgreSQL", c.Protocol())
	}
}

func TestCodec_ReadMessage(t *testing.T) {
	c := NewCodec()

	// Simple Q (Query) message
	buf := bytes.NewBuffer([]byte{'Q', 0, 0, 0, 10, 'S', 'E', 'L', 'E', 'C', 'T', 1, 0})
	msg, err := c.ReadMessage(buf)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if msg.Type != 'Q' {
		t.Errorf("Type = %c, want Q", msg.Type)
	}
	if msg.Length != 10 {
		t.Errorf("Length = %d, want 10", msg.Length)
	}
}

func TestCodec_ReadMessage_InvalidLength(t *testing.T) {
	c := NewCodec()

	// Length < 4 should fail
	buf := bytes.NewBuffer([]byte{'Q', 0, 0, 0, 2})
	_, err := c.ReadMessage(buf)
	if err == nil {
		t.Error("Should fail for invalid length")
	}
}

func TestCodec_ReadMessage_TooLarge(t *testing.T) {
	c := NewCodec()

	// Message > 16MB
	buf := bytes.NewBuffer([]byte{'Q', 1, 0, 0, 0})
	_, err := c.ReadMessage(buf)
	if err == nil {
		t.Error("Should fail for too large message")
	}
}

func TestCodec_ReadMessage_EOF(t *testing.T) {
	c := NewCodec()
	buf := bytes.NewBuffer([]byte{})
	_, err := c.ReadMessage(buf)
	if err == nil {
		t.Error("Should fail on empty buffer")
	}
}

func TestCodec_WriteMessage(t *testing.T) {
	c := NewCodec()

	msg := &common.Message{
		Type:   'Q',
		Length: 10,
		Raw:    []byte{'Q', 0, 0, 0, 10, 'S', 'E', 'L', 'E', 'C', 'T', 1, 0},
	}

	var buf bytes.Buffer
	if err := c.WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("WriteMessage wrote nothing")
	}
}

func TestCodec_IsStartup(t *testing.T) {
	c := NewCodec()

	startup := &common.Message{Raw: []byte{0x00, 0x03, 0x00, 0x00, 'u', 's', 'e', 'r', 0}}
	if !c.IsStartup(startup) {
		t.Error("Should detect startup message")
	}

	nonStartup := &common.Message{Raw: []byte{'Q', 0, 0, 0, 10}}
	if c.IsStartup(nonStartup) {
		t.Error("Should not detect non-startup as startup")
	}

	// Too short
	short := &common.Message{Raw: []byte{'Q'}}
	if c.IsStartup(short) {
		t.Error("Short message should not be startup")
	}
}

func TestCodec_IsTerminate(t *testing.T) {
	c := NewCodec()
	if !c.IsTerminate(&common.Message{Type: 'X'}) {
		t.Error("X message should be terminate")
	}
	if c.IsTerminate(&common.Message{Type: 'Q'}) {
		t.Error("Q message should not be terminate")
	}
}

func TestCodec_IsQuery(t *testing.T) {
	c := NewCodec()
	if !c.IsQuery(&common.Message{Type: 'Q'}) {
		t.Error("Q message should be query")
	}
	if c.IsQuery(&common.Message{Type: 'P'}) {
		t.Error("P message should not be query")
	}
}

func TestCodec_IsTransactionBegin(t *testing.T) {
	c := NewCodec()
	cases := []struct {
		payload []byte
		want    bool
	}{
		{[]byte("BEGIN"), true},
		{[]byte("BEGIN WORK"), true},
		{[]byte("START TRANSACTION"), true},
		{[]byte("SELECT 1"), false},
	}
	for _, tc := range cases {
		msg := &common.Message{Type: 'Q', Payload: tc.payload}
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
		{[]byte("COMMIT"), true},
		{[]byte("ROLLBACK"), true},
		{[]byte("END"), true},
		{[]byte("SELECT 1"), false},
	}
	for _, tc := range cases {
		msg := &common.Message{Type: 'Q', Payload: tc.payload}
		if got := c.IsTransactionEnd(msg); got != tc.want {
			t.Errorf("IsTransactionEnd(%q) = %v, want %v", tc.payload, got, tc.want)
		}
	}
}

func TestCodec_IsPrepare(t *testing.T) {
	c := NewCodec()
	if !c.IsPrepare(&common.Message{Type: 'P'}) {
		t.Error("P should be prepare")
	}
}

func TestCodec_IsExecute(t *testing.T) {
	c := NewCodec()
	if !c.IsExecute(&common.Message{Type: 'E'}) {
		t.Error("E should be execute")
	}
}

func TestCodec_IsBind(t *testing.T) {
	c := NewCodec()
	if !c.IsBind(&common.Message{Type: 'B'}) {
		t.Error("B should be bind")
	}
}

func TestCodec_IsClose(t *testing.T) {
	c := NewCodec()
	if !c.IsClose(&common.Message{Type: 'C'}) {
		t.Error("C should be close")
	}
}

func TestCodec_IsSync(t *testing.T) {
	c := NewCodec()
	if !c.IsSync(&common.Message{Type: 'S'}) {
		t.Error("S should be sync")
	}
}

func TestCodec_ExtractQuery(t *testing.T) {
	c := NewCodec()

	// Simple query
	msg := &common.Message{Type: 'Q', Payload: []byte("SELECT 1\x00")}
	q, err := c.ExtractQuery(msg)
	if err != nil {
		t.Fatalf("ExtractQuery failed: %v", err)
	}
	if q != "SELECT 1" {
		t.Errorf("Query = %q, want %q", q, "SELECT 1")
	}

	// Parse message
	parseMsg := &common.Message{Type: 'P', Payload: []byte("stmt\x00SELECT * FROM t\x00\x00\x00")}
	q, err = c.ExtractQuery(parseMsg)
	if err != nil {
		t.Fatalf("ExtractQuery Parse failed: %v", err)
	}
	if q != "SELECT * FROM t" {
		t.Errorf("Query = %q, want %q", q, "SELECT * FROM t")
	}

	// Invalid message type
	_, err = c.ExtractQuery(&common.Message{Type: 'X'})
	if err == nil {
		t.Error("Should error for non-query message type")
	}
}

func TestCodec_ExtractStatementName(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: 'P', Payload: []byte("my_stmt\x00SELECT 1\x00\x00\x00")}
	name, err := c.ExtractStatementName(msg)
	if err != nil {
		t.Fatalf("ExtractStatementName failed: %v", err)
	}
	if name != "my_stmt" {
		t.Errorf("Name = %q, want %q", name, "my_stmt")
	}
}

func TestCodec_ExtractPortalName(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: 'B', Payload: []byte("my_portal\x00my_stmt\x00")}
	name, err := c.ExtractPortalName(msg)
	if err != nil {
		t.Fatalf("ExtractPortalName failed: %v", err)
	}
	if name != "my_portal" {
		t.Errorf("Name = %q, want %q", name, "my_portal")
	}
}

func TestCodec_GenerateResetSequence(t *testing.T) {
	c := NewCodec()
	seq := c.GenerateResetSequence()
	if len(seq) != 1 {
		t.Fatalf("Reset sequence has %d messages, want 1", len(seq))
	}
	if seq[0].Type != 'Q' {
		t.Error("Reset should be a Query message")
	}
}

func TestCodec_CreateSSLRequest(t *testing.T) {
	c := NewCodec()
	data := c.CreateSSLRequest()
	if len(data) != 8 {
		t.Errorf("SSLRequest length = %d, want 8", len(data))
	}
}

func TestCodec_CreateStartupMessage(t *testing.T) {
	c := NewCodec()
	data := c.CreateStartupMessage("testuser", "testdb")
	if len(data) < 16 {
		t.Errorf("StartupMessage too short: %d bytes", len(data))
	}
}

func TestCodec_CreatePasswordMessage(t *testing.T) {
	c := NewCodec()
	data := c.CreatePasswordMessage("secret")
	if len(data) < 6 {
		t.Errorf("PasswordMessage too short: %d bytes", len(data))
	}
	if data[0] != 'p' {
		t.Errorf("PasswordMessage type = %c, want p", data[0])
	}
}

func TestCodec_IsSSLRequest(t *testing.T) {
	c := NewCodec()
	data := make([]byte, 8)
	// SSLRequest code: 80877103
	data[4] = 0x04
	data[5] = 0xd2
	data[6] = 0x16
	data[7] = 0x2f

	if !c.IsSSLRequest(data) {
		t.Error("Should detect SSL request")
	}
	if c.IsSSLRequest([]byte{0, 0, 0, 0}) {
		t.Error("Short data should not be SSL request")
	}
}

func TestCodec_IsGSSENCRequest(t *testing.T) {
	c := NewCodec()
	data := make([]byte, 8)
	// GSSENCRequest code: 80877104
	data[4] = 0x04
	data[5] = 0xd2
	data[6] = 0x16
	data[7] = 0x30

	if !c.IsGSSENCRequest(data) {
		t.Error("Should detect GSS ENC request")
	}
}

func TestReadSSLResponse(t *testing.T) {
	c := NewCodec()
	buf := bytes.NewBuffer([]byte{'S'})
	b, err := c.ReadSSLResponse(buf)
	if err != nil {
		t.Fatalf("ReadSSLResponse failed: %v", err)
	}
	if b != 'S' {
		t.Errorf("Response = %c, want S", b)
	}
}

func TestMD5PasswordHash(t *testing.T) {
	salt := [4]byte{1, 2, 3, 4}
	hash := MD5PasswordHash("testuser", "secret", salt)
	if len(hash) != 35 {
		t.Errorf("MD5 hash length = %d, want 35", len(hash))
	}
	if hash[:3] != "md5" {
		t.Errorf("MD5 hash should start with md5, got %q", hash[:3])
	}
}

func TestSHA256PasswordHash(t *testing.T) {
	hash := SHA256PasswordHash("secret", []byte("salt"), 4096)
	if len(hash) != 32 {
		t.Errorf("SHA256 hash length = %d, want 32", len(hash))
	}
}

func TestCreateErrorResponse(t *testing.T) {
	data := CreateErrorResponse("28000", "Auth failed")
	if len(data) == 0 {
		t.Error("ErrorResponse should not be empty")
	}
	if data[0] != 'E' {
		t.Error("ErrorResponse should start with E")
	}
}

func TestCreateReadyForQuery(t *testing.T) {
	data := CreateReadyForQuery('I')
	if len(data) != 6 {
		t.Errorf("ReadyForQuery length = %d, want 6", len(data))
	}
	if data[0] != 'Z' {
		t.Error("ReadyForQuery should start with Z")
	}
}

func TestCreateParameterStatus(t *testing.T) {
	data := CreateParameterStatus("client_encoding", "UTF8")
	if len(data) == 0 {
		t.Error("ParameterStatus should not be empty")
	}
	if data[0] != 'S' {
		t.Error("ParameterStatus should start with S")
	}
}
