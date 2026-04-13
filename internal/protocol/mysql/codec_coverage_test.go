package mysql

import (
	"testing"

	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

func TestIsTransactionBegin_Basic(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: ComQuery, Payload: []byte{ComQuery, 'B', 'E', 'G', 'I', 'N'}}
	if !c.IsTransactionBegin(msg) {
		t.Error("BEGIN should be transaction begin")
	}
}

func TestIsTransactionBegin_StartTransaction(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: ComQuery, Payload: []byte{ComQuery, 'S', 'T', 'A', 'R', 'T', ' ', 'T', 'R', 'A', 'N', 'S', 'A', 'C', 'T', 'I', 'O', 'N'}}
	if !c.IsTransactionBegin(msg) {
		t.Error("START TRANSACTION should be transaction begin")
	}
}

func TestIsTransactionBegin_Lowercase(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: ComQuery, Payload: []byte{ComQuery, 'b', 'e', 'g', 'i', 'n'}}
	if !c.IsTransactionBegin(msg) {
		t.Error("lowercase begin should be transaction begin")
	}
}

func TestIsTransactionEnd_Commit(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: ComQuery, Payload: []byte{ComQuery, 'C', 'O', 'M', 'M', 'I', 'T'}}
	if !c.IsTransactionEnd(msg) {
		t.Error("COMMIT should be transaction end")
	}
}

func TestIsTransactionEnd_Rollback(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: ComQuery, Payload: []byte{ComQuery, 'R', 'O', 'L', 'L', 'B', 'A', 'C', 'K'}}
	if !c.IsTransactionEnd(msg) {
		t.Error("ROLLBACK should be transaction end")
	}
}

func TestIsTransactionBegin_NotQuery(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: ComQuit, Payload: []byte{ComQuit}}
	if c.IsTransactionBegin(msg) {
		t.Error("COM_QUIT should not be transaction begin")
	}
}

func TestIsTransactionEnd_NotQuery(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: ComQuit, Payload: []byte{ComQuit}}
	if c.IsTransactionEnd(msg) {
		t.Error("COM_QUIT should not be transaction end")
	}
}

func TestExtractQuery_EmptyPayload(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: ComQuery, Payload: []byte{ComQuery}}
	q := c.extractQuery(msg)
	if q != "" {
		t.Errorf("Expected empty string, got %q", q)
	}
}

func TestExtractQuery_ShortPayload(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: ComQuery, Payload: []byte{}}
	q := c.extractQuery(msg)
	if q != "" {
		t.Errorf("Expected empty string, got %q", q)
	}
}

func TestExtractPrepareQuery_ShortPayload(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: ComStmtPrepare, Payload: []byte{}}
	q := c.extractPrepareQuery(msg)
	if q != "" {
		t.Errorf("Expected empty string, got %q", q)
	}
}

func TestExtractQuery_NotQueryType(t *testing.T) {
	c := NewCodec()
	msg := &common.Message{Type: ComQuit, Payload: []byte{ComQuit}}
	_, err := c.ExtractQuery(msg)
	if err == nil {
		t.Error("Should return error for non-query message type")
	}
}

func TestParseHandshakeResponse_PluginAuthLenenc(t *testing.T) {
	// Build a handshake response with ClientPluginAuthLenencClientData flag
	data := make([]byte, 64)

	// Capability flags with ClientPluginAuthLenencClientData
	caps := uint32(ClientPluginAuthLenencClientData | ClientProtocol41)
	putUint32(data[0:4], caps)
	putUint32(data[4:8], 16777216) // MaxPacketSize
	data[8] = 0x21                  // CharacterSet
	// Reserved 23 bytes (9-31) already zero
	// Username at pos 32
	copy(data[32:], "root")
	data[36] = 0 // null terminate username
	// Auth response: length-encoded string at pos 37
	data[37] = 4 // length = 4
	copy(data[38:], "auth")
	// Database: no ClientConnectWithDB, so skip
	// Auth plugin name: no ClientPluginAuth, so skip

	resp, err := ParseHandshakeResponse(data[:42])
	if err != nil {
		t.Fatalf("ParseHandshakeResponse failed: %v", err)
	}
	if resp.Username != "root" {
		t.Errorf("Username = %q, want root", resp.Username)
	}
	if string(resp.AuthResponse) != "auth" {
		t.Errorf("AuthResponse = %q, want auth", string(resp.AuthResponse))
	}
}

func TestParseHandshakeResponse_SecureConnection(t *testing.T) {
	data := make([]byte, 64)

	// Capability flags with ClientSecureConnection (no PluginAuthLenencClientData)
	caps := uint32(ClientSecureConnection | ClientProtocol41)
	putUint32(data[0:4], caps)
	putUint32(data[4:8], 16777216) // MaxPacketSize
	data[8] = 0x21
	copy(data[32:], "user")
	data[36] = 0 // null terminate username
	// Auth response: 1 byte length + data
	data[37] = 3
	copy(data[38:], "abc")

	resp, err := ParseHandshakeResponse(data[:41])
	if err != nil {
		t.Fatalf("ParseHandshakeResponse failed: %v", err)
	}
	if resp.Username != "user" {
		t.Errorf("Username = %q, want user", resp.Username)
	}
	if string(resp.AuthResponse) != "abc" {
		t.Errorf("AuthResponse = %q, want abc", string(resp.AuthResponse))
	}
}

func TestParseHandshakeResponse_NullTerminated(t *testing.T) {
	data := make([]byte, 64)

	// No special auth flags - null-terminated auth response
	caps := uint32(ClientProtocol41)
	putUint32(data[0:4], caps)
	putUint32(data[4:8], 16777216)
	data[8] = 0x21
	copy(data[32:], "test")
	data[36] = 0 // null terminate username
	copy(data[37:], "pass")
	data[41] = 0 // null terminate auth

	resp, err := ParseHandshakeResponse(data[:42])
	if err != nil {
		t.Fatalf("ParseHandshakeResponse failed: %v", err)
	}
	if resp.Username != "test" {
		t.Errorf("Username = %q, want test", resp.Username)
	}
	if string(resp.AuthResponse) != "pass" {
		t.Errorf("AuthResponse = %q, want pass", string(resp.AuthResponse))
	}
}

func TestParseHandshakeResponse_WithDB(t *testing.T) {
	data := make([]byte, 80)

	caps := uint32(ClientSecureConnection | ClientConnectWithDB | ClientPluginAuth | ClientProtocol41)
	putUint32(data[0:4], caps)
	putUint32(data[4:8], 16777216)
	data[8] = 0x21
	copy(data[32:], "root")
	data[36] = 0
	// Auth: 1 byte length + data
	data[37] = 3
	copy(data[38:], "pwd")
	// Database name
	copy(data[41:], "mydb")
	data[45] = 0 // null terminate
	// Auth plugin name
	copy(data[46:], "mysql_native_password")
	data[67] = 0

	resp, err := ParseHandshakeResponse(data[:68])
	if err != nil {
		t.Fatalf("ParseHandshakeResponse failed: %v", err)
	}
	if resp.Database != "mydb" {
		t.Errorf("Database = %q, want mydb", resp.Database)
	}
	if resp.AuthPluginName != "mysql_native_password" {
		t.Errorf("AuthPluginName = %q, want mysql_native_password", resp.AuthPluginName)
	}
}

func TestParseHandshakeResponse_TooShort(t *testing.T) {
	data := make([]byte, 10)
	_, err := ParseHandshakeResponse(data)
	if err == nil {
		t.Error("Should fail for too short data")
	}
}

func TestReadLengthEncodedInt_Empty(t *testing.T) {
	val, isNull, n := readLengthEncodedInt([]byte{})
	if val != 0 || isNull || n != 0 {
		t.Errorf("Expected (0, false, 0), got (%d, %v, %d)", val, isNull, n)
	}
}

func TestReadLengthEncodedInt_Null(t *testing.T) {
	val, isNull, n := readLengthEncodedInt([]byte{0xfb})
	if val != 0 || !isNull || n != 1 {
		t.Errorf("Expected (0, true, 1), got (%d, %v, %d)", val, isNull, n)
	}
}

func TestReadLengthEncodedInt_TwoByte(t *testing.T) {
	data := []byte{0xfc, 0x39, 0x05} // 0x0539 = 1337
	val, isNull, n := readLengthEncodedInt(data)
	if val != 1337 || isNull || n != 3 {
		t.Errorf("Expected (1337, false, 3), got (%d, %v, %d)", val, isNull, n)
	}
}

func TestReadLengthEncodedInt_ThreeByte(t *testing.T) {
	data := []byte{0xfd, 0x01, 0x00, 0x01} // 1 | 0<<8 | 1<<16 = 65537
	val, isNull, n := readLengthEncodedInt(data)
	if val != 65537 || isNull || n != 4 {
		t.Errorf("Expected (65537, false, 4), got (%d, %v, %d)", val, isNull, n)
	}
}

func TestReadLengthEncodedInt_EightByte(t *testing.T) {
	data := []byte{0xfe, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}
	val, isNull, n := readLengthEncodedInt(data)
	if val != (1<<32)+1 || isNull || n != 9 {
		t.Errorf("Expected (%d, false, 9), got (%d, %v, %d)", uint64(1<<32)+1, val, isNull, n)
	}
}

func TestReadLengthEncodedInt_Small(t *testing.T) {
	val, isNull, n := readLengthEncodedInt([]byte{0x42})
	if val != 0x42 || isNull || n != 1 {
		t.Errorf("Expected (0x42, false, 1), got (%d, %v, %d)", val, isNull, n)
	}
}

func TestAppendLengthEncodedInt_TwoByte(t *testing.T) {
	buf := appendLengthEncodedInt([]byte{}, 256)
	if len(buf) != 3 || buf[0] != 0xfc {
		t.Errorf("Expected 3 bytes starting with 0xfc, got %v", buf)
	}
}

func TestAppendLengthEncodedInt_ThreeByte(t *testing.T) {
	buf := appendLengthEncodedInt([]byte{}, 1<<16)
	if len(buf) != 4 || buf[0] != 0xfd {
		t.Errorf("Expected 4 bytes starting with 0xfd, got %v", buf)
	}
}

func TestAppendLengthEncodedInt_EightByte(t *testing.T) {
	buf := appendLengthEncodedInt([]byte{}, 1<<24)
	if len(buf) != 9 || buf[0] != 0xfe {
		t.Errorf("Expected 9 bytes starting with 0xfe, got %v", buf)
	}
}

// Helper to put uint32 in little-endian
func putUint32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}
