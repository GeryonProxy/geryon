package postgresql

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestParsePasswordMessage(t *testing.T) {
	data := []byte("secret\x00")
	msg, err := ParsePasswordMessage(data)
	if err != nil {
		t.Fatalf("ParsePasswordMessage failed: %v", err)
	}
	if msg.Password != "secret" {
		t.Errorf("Password = %q, want secret", msg.Password)
	}
}

func TestParseQueryMessage(t *testing.T) {
	data := []byte("SELECT 1\x00")
	msg, err := ParseQueryMessage(data)
	if err != nil {
		t.Fatalf("ParseQueryMessage failed: %v", err)
	}
	if msg.Query != "SELECT 1" {
		t.Errorf("Query = %q, want SELECT 1", msg.Query)
	}
}

func TestParseParseMessage(t *testing.T) {
	data := []byte("stmt\x00SELECT * FROM t\x00\x00\x00\x00\x00\x00")
	msg, err := ParseParseMessage(data)
	if err != nil {
		t.Fatalf("ParseParseMessage failed: %v", err)
	}
	if msg.Name != "stmt" {
		t.Errorf("Name = %q, want stmt", msg.Name)
	}
	if msg.Query != "SELECT * FROM t" {
		t.Errorf("Query = %q, want SELECT * FROM t", msg.Query)
	}
	if len(msg.ParameterTypes) != 0 {
		t.Errorf("ParameterTypes = %d, want 0", len(msg.ParameterTypes))
	}
}

func TestParseParseMessage_WithParams(t *testing.T) {
	// name\0 query\0 numParamTypes(2) type1(4) type2(4)
	buf := make([]byte, 0, 30)
	buf = append(buf, []byte("s\x00")...)
	buf = append(buf, []byte("SELECT ?\x00")...)
	buf = binary.BigEndian.AppendUint16(buf, 1)          // 1 param type
	buf = binary.BigEndian.AppendUint32(buf, 23)         // int4
	msg, err := ParseParseMessage(buf)
	if err != nil {
		t.Fatalf("ParseParseMessage failed: %v", err)
	}
	if msg.Name != "s" {
		t.Errorf("Name = %q, want s", msg.Name)
	}
	if len(msg.ParameterTypes) != 1 {
		t.Fatalf("ParameterTypes count = %d, want 1", len(msg.ParameterTypes))
	}
}

func TestParseBindMessage(t *testing.T) {
	// portal\0 stmt\0 numFormats(2) format(2) numParams(2) paramLen(4) paramData(N) numResults(2)
	buf := make([]byte, 0)
	buf = append(buf, []byte("portal\x00")...)
	buf = append(buf, []byte("stmt\x00")...)
	buf = binary.BigEndian.AppendUint16(buf, 0) // 0 format codes
	buf = binary.BigEndian.AppendUint16(buf, 0) // 0 parameters
	buf = binary.BigEndian.AppendUint16(buf, 0) // 0 result formats
	msg, err := ParseBindMessage(buf)
	if err != nil {
		t.Fatalf("ParseBindMessage failed: %v", err)
	}
	if msg.PortalName != "portal" {
		t.Errorf("PortalName = %q, want portal", msg.PortalName)
	}
	if msg.StatementName != "stmt" {
		t.Errorf("StatementName = %q, want stmt", msg.StatementName)
	}
	if len(msg.ParameterFormats) != 0 {
		t.Errorf("ParameterFormats = %d, want 0", len(msg.ParameterFormats))
	}
}

func TestParseExecuteMessage(t *testing.T) {
	data := []byte("portal\x00\x00\x00\x00\x00")
	msg, err := ParseExecuteMessage(data)
	if err != nil {
		t.Fatalf("ParseExecuteMessage failed: %v", err)
	}
	if msg.PortalName != "portal" {
		t.Errorf("PortalName = %q, want portal", msg.PortalName)
	}
	if msg.MaxRows != 0 {
		t.Errorf("MaxRows = %d, want 0", msg.MaxRows)
	}
}

func TestMD5Password(t *testing.T) {
	salt := []byte{1, 2, 3, 4}
	hash := MD5Password("testuser", "secret", salt)
	if len(hash) != 35 {
		t.Errorf("Hash length = %d, want 35", len(hash))
	}
	if hash[:3] != "md5" {
		t.Errorf("Hash should start with md5, got %q", hash[:3])
	}
}

func TestVerifyMD5Password(t *testing.T) {
	salt := []byte{1, 2, 3, 4}
	hash := MD5Password("testuser", "secret", salt)
	if !VerifyMD5Password("testuser", "secret", hash, salt) {
		t.Error("Password verification should succeed")
	}
	if VerifyMD5Password("testuser", "wrong", hash, salt) {
		t.Error("Wrong password should fail")
	}
}

func TestGenerateSASLSCRAMSHA256(t *testing.T) {
	clientFirst, serverFirst, salt, iter := GenerateSASLSCRAMSHA256()
	// This is a simplified stub implementation
	if clientFirst != "" {
		t.Errorf("clientFirst should be empty (stub impl), got %q", clientFirst)
	}
	if serverFirst != "" {
		t.Errorf("serverFirst should be empty (stub impl), got %q", serverFirst)
	}
	if len(salt) != 16 {
		t.Errorf("salt length = %d, want 16", len(salt))
	}
	if iter != 4096 {
		t.Errorf("iteration = %d, want 4096", iter)
	}
}

func TestMessageConstants(t *testing.T) {
	if MsgQuery != 'Q' {
		t.Errorf("MsgQuery = %c, want Q", MsgQuery)
	}
	if MsgBind != 'B' {
		t.Errorf("MsgBind = %c, want B", MsgBind)
	}
	if MsgParse != 'P' {
		t.Errorf("MsgParse = %c, want P", MsgParse)
	}
	if MsgExecute != 'E' {
		t.Errorf("MsgExecute = %c, want E", MsgExecute)
	}
	if MsgSync != 'S' {
		t.Errorf("MsgSync = %c, want S", MsgSync)
	}
	if MsgTerminate != 'X' {
		t.Errorf("MsgTerminate = %c, want X", MsgTerminate)
	}
	if MsgReadyForQuery != 'Z' {
		t.Errorf("MsgReadyForQuery = %c, want Z", MsgReadyForQuery)
	}
	if MsgClose != 'C' {
		t.Errorf("MsgClose = %c, want C", MsgClose)
	}
}

func TestAuthConstants(t *testing.T) {
	if AuthOK != 0 {
		t.Errorf("AuthOK = %d, want 0", AuthOK)
	}
	if AuthCleartextPassword != 3 {
		t.Errorf("AuthCleartextPassword = %d, want 3", AuthCleartextPassword)
	}
	if AuthMD5Password != 5 {
		t.Errorf("AuthMD5Password = %d, want 5", AuthMD5Password)
	}
	if AuthSASL != 10 {
		t.Errorf("AuthSASL = %d, want 10", AuthSASL)
	}
}

func TestTxStatusConstants(t *testing.T) {
	if TxIdle != 'I' {
		t.Errorf("TxIdle = %c, want I", TxIdle)
	}
	if TxActive != 'T' {
		t.Errorf("TxActive = %c, want T", TxActive)
	}
	if TxError != 'E' {
		t.Errorf("TxError = %c, want E", TxError)
	}
}

func TestConnection_WriteMessage(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)
	err := c.WriteMessage('T', []byte("hello"))
	if err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}
	if buf.Len() != 10 { // 1 type + 4 length + 5 data
		t.Errorf("Wrote %d bytes, want 10", buf.Len())
	}
	if buf.Bytes()[0] != 'T' {
		t.Errorf("First byte = %c, want T", buf.Bytes()[0])
	}
	// Length = 4 + 5 = 9
	length := int32(binary.BigEndian.Uint32(buf.Bytes()[1:5]))
	if length != 9 {
		t.Errorf("Length = %d, want 9", length)
	}
}

func TestConnection_WriteRaw(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)
	err := c.WriteRaw([]byte{1, 2, 3})
	if err != nil {
		t.Fatalf("WriteRaw failed: %v", err)
	}
	if buf.Len() != 3 {
		t.Errorf("Wrote %d bytes, want 3", buf.Len())
	}
}

func TestConnection_SendReadyForQuery(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)
	err := c.SendReadyForQuery('I')
	if err != nil {
		t.Fatalf("SendReadyForQuery failed: %v", err)
	}
	if buf.Len() != 6 {
		t.Errorf("ReadyForQuery = %d bytes, want 6", buf.Len())
	}
	if buf.Bytes()[5] != 'I' {
		t.Errorf("Status = %c, want I", buf.Bytes()[5])
	}
}

func TestConnection_SendErrorResponse(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)
	err := c.SendErrorResponse("ERROR", "42601", "syntax error")
	if err != nil {
		t.Fatalf("SendErrorResponse failed: %v", err)
	}
	if buf.Bytes()[0] != 'E' {
		t.Errorf("First byte = %c, want E", buf.Bytes()[0])
	}
}

func TestConnection_SendCommandComplete(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)
	err := c.SendCommandComplete("SELECT 1")
	if err != nil {
		t.Fatalf("SendCommandComplete failed: %v", err)
	}
	if buf.Bytes()[0] != 'C' {
		t.Errorf("First byte = %c, want C", buf.Bytes()[0])
	}
}

func TestConnection_SendDataRow(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)
	err := c.SendDataRow([][]byte{[]byte("hello"), []byte("world")})
	if err != nil {
		t.Fatalf("SendDataRow failed: %v", err)
	}
	if buf.Bytes()[0] != 'D' {
		t.Errorf("First byte = %c, want D", buf.Bytes()[0])
	}
}

func TestConnection_SendParseComplete(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)
	err := c.SendParseComplete()
	if err != nil {
		t.Fatalf("SendParseComplete failed: %v", err)
	}
	if buf.Bytes()[0] != '1' {
		t.Errorf("First byte = %c, want 1", buf.Bytes()[0])
	}
}

func TestConnection_SendBindComplete(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)
	err := c.SendBindComplete()
	if err != nil {
		t.Fatalf("SendBindComplete failed: %v", err)
	}
	if buf.Bytes()[0] != '2' {
		t.Errorf("First byte = %c, want 2", buf.Bytes()[0])
	}
}

func TestConnection_SendNoData(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)
	err := c.SendNoData()
	if err != nil {
		t.Fatalf("SendNoData failed: %v", err)
	}
	if buf.Bytes()[0] != 'n' {
		t.Errorf("First byte = %c, want n", buf.Bytes()[0])
	}
}

func TestConnection_SendEmptyQueryResponse(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)
	err := c.SendEmptyQueryResponse()
	if err != nil {
		t.Fatalf("SendEmptyQueryResponse failed: %v", err)
	}
	if buf.Bytes()[0] != 'I' {
		t.Errorf("First byte = %c, want I", buf.Bytes()[0])
	}
}

// testConn is a mock net.Conn
type testConn struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
	closed   bool
}

func (c *testConn) Read(b []byte) (n int, err error) {
	if c.readBuf != nil {
		return c.readBuf.Read(b)
	}
	return 0, nil
}

func (c *testConn) Write(b []byte) (n int, err error) {
	if c.writeBuf != nil {
		return c.writeBuf.Write(b)
	}
	return len(b), nil
}

func (c *testConn) Close() error {
	c.closed = true
	return nil
}

func (c *testConn) LocalAddr() net.Addr              { return nil }
func (c *testConn) RemoteAddr() net.Addr             { return nil }
func (c *testConn) SetDeadline(t time.Time) error    { return nil }
func (c *testConn) SetReadDeadline(t time.Time) error { return nil }
func (c *testConn) SetWriteDeadline(t time.Time) error { return nil }
