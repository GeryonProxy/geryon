package postgresql

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

// Extended tests for protocol functions

func TestParsePasswordMessage_Empty(t *testing.T) {
	data := []byte("\x00")
	msg, err := ParsePasswordMessage(data)
	if err != nil {
		t.Fatalf("ParsePasswordMessage failed: %v", err)
	}
	if msg.Password != "" {
		t.Errorf("Password = %q, want empty", msg.Password)
	}
}

func TestParsePasswordMessage_Long(t *testing.T) {
	// Create a long password without null bytes
	longPass := strings.Repeat("a", 1000)
	data := append([]byte(longPass), 0)
	msg, err := ParsePasswordMessage(data)
	if err != nil {
		t.Fatalf("ParsePasswordMessage failed: %v", err)
	}
	if msg.Password != longPass {
		t.Errorf("Password length = %d, want %d", len(msg.Password), len(longPass))
	}
}

func TestParseQueryMessage_Empty(t *testing.T) {
	data := []byte("\x00")
	msg, err := ParseQueryMessage(data)
	if err != nil {
		t.Fatalf("ParseQueryMessage failed: %v", err)
	}
	if msg.Query != "" {
		t.Errorf("Query = %q, want empty", msg.Query)
	}
}

func TestParseQueryMessage_Long(t *testing.T) {
	// Create a long query without null bytes
	longQuery := "SELECT " + strings.Repeat("a", 1000) + " FROM t"
	data := append([]byte(longQuery), 0)
	msg, err := ParseQueryMessage(data)
	if err != nil {
		t.Fatalf("ParseQueryMessage failed: %v", err)
	}
	if msg.Query != longQuery {
		t.Errorf("Query length = %d, want %d", len(msg.Query), len(longQuery))
	}
}

func TestParseParseMessage_EmptyName(t *testing.T) {
	// Empty name with query
	buf := []byte("\x00SELECT 1\x00\x00\x00")
	msg, err := ParseParseMessage(buf)
	if err != nil {
		t.Fatalf("ParseParseMessage failed: %v", err)
	}
	if msg.Name != "" {
		t.Errorf("Name = %q, want empty", msg.Name)
	}
	if msg.Query != "SELECT 1" {
		t.Errorf("Query = %q, want SELECT 1", msg.Query)
	}
}

func TestParseParseMessage_NoParams(t *testing.T) {
	// name\0 query\0 with no param types
	buf := []byte("stmt\x00SELECT * FROM t\x00\x00\x00")
	msg, err := ParseParseMessage(buf)
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

func TestParseParseMessage_MultipleParams(t *testing.T) {
	// name\0 query\0 numParamTypes(2) type1 type2
	buf := make([]byte, 0, 50)
	buf = append(buf, []byte("s\x00")...)
	buf = append(buf, []byte("SELECT ?, ?\x00")...)
	buf = binary.BigEndian.AppendUint16(buf, 2)   // 2 param types
	buf = binary.BigEndian.AppendUint32(buf, 23)  // int4
	buf = binary.BigEndian.AppendUint32(buf, 25)  // text
	msg, err := ParseParseMessage(buf)
	if err != nil {
		t.Fatalf("ParseParseMessage failed: %v", err)
	}
	if len(msg.ParameterTypes) != 2 {
		t.Errorf("ParameterTypes count = %d, want 2", len(msg.ParameterTypes))
	}
	if msg.ParameterTypes[0] != 23 {
		t.Errorf("ParameterTypes[0] = %d, want 23", msg.ParameterTypes[0])
	}
	if msg.ParameterTypes[1] != 25 {
		t.Errorf("ParameterTypes[1] = %d, want 25", msg.ParameterTypes[1])
	}
}

func TestParseBindMessage_WithParams(t *testing.T) {
	// portal\0 stmt\0 numFormats(1) format(1=text) numParams(1) paramLen(4) paramData numResults(0)
	buf := make([]byte, 0)
	buf = append(buf, []byte("portal\x00")...)
	buf = append(buf, []byte("stmt\x00")...)
	buf = binary.BigEndian.AppendUint16(buf, 1)    // 1 format code
	buf = binary.BigEndian.AppendUint16(buf, 0)    // text format
	buf = binary.BigEndian.AppendUint16(buf, 1)    // 1 parameter
	buf = binary.BigEndian.AppendUint32(buf, 5)    // param length
	buf = append(buf, []byte("hello")...)          // param data
	buf = binary.BigEndian.AppendUint16(buf, 0)    // 0 result formats

	msg, err := ParseBindMessage(buf)
	if err != nil {
		t.Fatalf("ParseBindMessage failed: %v", err)
	}
	if msg.PortalName != "portal" {
		t.Errorf("PortalName = %q, want portal", msg.PortalName)
	}
	if len(msg.Parameters) != 1 {
		t.Fatalf("Parameters count = %d, want 1", len(msg.Parameters))
	}
	if string(msg.Parameters[0]) != "hello" {
		t.Errorf("Parameters[0] = %q, want hello", string(msg.Parameters[0]))
	}
}

func TestParseBindMessage_BinaryParam(t *testing.T) {
	// portal\0 stmt\0 numFormats(1) format(0=binary) numParams(1) paramLen(4) paramData numResults(0)
	buf := make([]byte, 0)
	buf = append(buf, []byte("p\x00s\x00")...)
	buf = binary.BigEndian.AppendUint16(buf, 1)    // 1 format code
	buf = binary.BigEndian.AppendUint16(buf, 1)    // binary format
	buf = binary.BigEndian.AppendUint16(buf, 1)    // 1 parameter
	buf = binary.BigEndian.AppendUint32(buf, 4)    // param length
	buf = append(buf, []byte{0, 0, 0, 42}...)      // binary int32
	buf = binary.BigEndian.AppendUint16(buf, 0)    // 0 result formats

	msg, err := ParseBindMessage(buf)
	if err != nil {
		t.Fatalf("ParseBindMessage failed: %v", err)
	}
	if len(msg.ParameterFormats) != 1 || msg.ParameterFormats[0] != 1 {
		t.Errorf("ParameterFormats[0] = %d, want 1 (binary)", msg.ParameterFormats[0])
	}
}

func TestParseExecuteMessage_WithLimit(t *testing.T) {
	// portal\0 max_rows(4)
	buf := make([]byte, 0)
	buf = append(buf, []byte("portal\x00")...)
	buf = binary.BigEndian.AppendUint32(buf, 100) // max 100 rows

	msg, err := ParseExecuteMessage(buf)
	if err != nil {
		t.Fatalf("ParseExecuteMessage failed: %v", err)
	}
	if msg.PortalName != "portal" {
		t.Errorf("PortalName = %q, want portal", msg.PortalName)
	}
	if msg.MaxRows != 100 {
		t.Errorf("MaxRows = %d, want 100", msg.MaxRows)
	}
}

func TestParseExecuteMessage_ZeroLimit(t *testing.T) {
	// portal\0 max_rows(0) = unlimited
	buf := make([]byte, 0)
	buf = append(buf, []byte("p\x00")...)
	buf = binary.BigEndian.AppendUint32(buf, 0) // 0 = no limit

	msg, err := ParseExecuteMessage(buf)
	if err != nil {
		t.Fatalf("ParseExecuteMessage failed: %v", err)
	}
	if msg.MaxRows != 0 {
		t.Errorf("MaxRows = %d, want 0", msg.MaxRows)
	}
}

func TestMD5Password_DifferentUsers(t *testing.T) {
	salt := []byte{1, 2, 3, 4}

	tests := []struct {
		user     string
		password string
	}{
		{"postgres", "secret"},
		{"testuser", "password123"},
		{"user_with_underscore", "p@ssw0rd!"},
		{"", "nopassword"},
		{"user", ""},
	}

	for _, tc := range tests {
		hash := MD5Password(tc.user, tc.password, salt)
		if len(hash) != 35 {
			t.Errorf("Hash length for %q = %d, want 35", tc.user, len(hash))
		}
		if hash[:3] != "md5" {
			t.Errorf("Hash for %q should start with md5, got %q", tc.user, hash[:3])
		}

		// Verify the hash
		if !VerifyMD5Password(tc.user, tc.password, hash, salt) {
			t.Errorf("Password verification for %q should succeed", tc.user)
		}
	}
}

func TestMD5Password_DifferentSalts(t *testing.T) {
	salts := [][]byte{
		{0, 0, 0, 0},
		{255, 255, 255, 255},
		{1, 2, 3, 4},
		{0x12, 0x34, 0x56, 0x78},
	}

	for _, salt := range salts {
		hash := MD5Password("user", "pass", salt)
		if len(hash) != 35 {
			t.Errorf("Hash length with salt %v = %d, want 35", salt, len(hash))
		}

		// Verify with correct salt
		if !VerifyMD5Password("user", "pass", hash, salt) {
			t.Errorf("Password verification with salt %v should succeed", salt)
		}

		// Verify with wrong salt should fail
		wrongSalt := []byte{salt[0] + 1, salt[1], salt[2], salt[3]}
		if VerifyMD5Password("user", "pass", hash, wrongSalt) {
			t.Errorf("Password verification with wrong salt %v should fail", wrongSalt)
		}
	}
}

func TestVerifyMD5Password_WrongPassword(t *testing.T) {
	salt := []byte{1, 2, 3, 4}
	hash := MD5Password("user", "correct", salt)

	// Wrong password
	if VerifyMD5Password("user", "wrong", hash, salt) {
		t.Error("Wrong password should fail verification")
	}

	// Wrong user
	if VerifyMD5Password("wronguser", "correct", hash, salt) {
		t.Error("Wrong user should fail verification")
	}
}

func TestGenerateSASLSCRAMSHA256_MultipleCalls(t *testing.T) {
	// Multiple calls - this is a stub implementation that returns same values
	for i := 0; i < 10; i++ {
		clientFirst, serverFirst, salt, iter := GenerateSASLSCRAMSHA256()
		if iter != 4096 {
			t.Errorf("iteration = %d, want 4096", iter)
		}
		if len(salt) != 16 {
			t.Errorf("salt length = %d, want 16", len(salt))
		}
		// Stub implementation returns empty strings
		if clientFirst != "" {
			t.Errorf("clientFirst should be empty (stub), got %q", clientFirst)
		}
		if serverFirst != "" {
			t.Errorf("serverFirst should be empty (stub), got %q", serverFirst)
		}
	}
}

// Test all message type constants
func TestAllMessageConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant byte
		expected byte
	}{
		{"MsgBind", MsgBind, 'B'},
		{"MsgClose", MsgClose, 'C'},
		{"MsgDescribe", MsgDescribe, 'D'},
		{"MsgExecute", MsgExecute, 'E'},
		{"MsgFunctionCall", MsgFunctionCall, 'F'},
		{"MsgParse", MsgParse, 'P'},
		{"MsgPasswordMessage", MsgPasswordMessage, 'p'},
		{"MsgQuery", MsgQuery, 'Q'},
		{"MsgSync", MsgSync, 'S'},
		{"MsgTerminate", MsgTerminate, 'X'},
		{"MsgCopyData", MsgCopyData, 'd'},
		{"MsgCopyDone", MsgCopyDone, 'c'},
		{"MsgCopyFail", MsgCopyFail, 'f'},
		{"MsgAuthentication", MsgAuthentication, 'R'},
		{"MsgBackendKeyData", MsgBackendKeyData, 'K'},
		{"MsgBindComplete", MsgBindComplete, '2'},
		{"MsgCloseComplete", MsgCloseComplete, '3'},
		{"MsgCommandComplete", MsgCommandComplete, 'C'},
		{"MsgCopyInResponse", MsgCopyInResponse, 'G'},
		{"MsgCopyOutResponse", MsgCopyOutResponse, 'H'},
		{"MsgCopyBothResponse", MsgCopyBothResponse, 'W'},
		{"MsgDataRow", MsgDataRow, 'D'},
		{"MsgEmptyQueryResponse", MsgEmptyQueryResponse, 'I'},
		{"MsgErrorResponse", MsgErrorResponse, 'E'},
		{"MsgFunctionCallResponse", MsgFunctionCallResponse, 'V'},
		{"MsgNoData", MsgNoData, 'n'},
		{"MsgNoticeResponse", MsgNoticeResponse, 'N'},
		{"MsgNotificationResponse", MsgNotificationResponse, 'A'},
		{"MsgParameterDescription", MsgParameterDescription, 't'},
		{"MsgParameterStatus", MsgParameterStatus, 'S'},
		{"MsgParseComplete", MsgParseComplete, '1'},
		{"MsgPortalSuspended", MsgPortalSuspended, 's'},
		{"MsgReadyForQuery", MsgReadyForQuery, 'Z'},
		{"MsgRowDescription", MsgRowDescription, 'T'},
	}

	for _, tc := range tests {
		if tc.constant != tc.expected {
			t.Errorf("%s = %c, want %c", tc.name, tc.constant, tc.expected)
		}
	}
}

// Test all auth constants
func TestAllAuthConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant int
		expected int
	}{
		{"AuthOK", AuthOK, 0},
		{"AuthKerberosV5", AuthKerberosV5, 2},
		{"AuthCleartextPassword", AuthCleartextPassword, 3},
		{"AuthMD5Password", AuthMD5Password, 5},
		{"AuthSCMCredential", AuthSCMCredential, 6},
		{"AuthGSS", AuthGSS, 7},
		{"AuthGSSContinue", AuthGSSContinue, 8},
		{"AuthSSPI", AuthSSPI, 9},
		{"AuthSASL", AuthSASL, 10},
		{"AuthSASLContinue", AuthSASLContinue, 11},
		{"AuthSASLFinal", AuthSASLFinal, 12},
	}

	for _, tc := range tests {
		if tc.constant != tc.expected {
			t.Errorf("%s = %d, want %d", tc.name, tc.constant, tc.expected)
		}
	}
}

// Connection struct field accessors
func TestConnection_Conn(t *testing.T) {
	tc := &testConn{}
	c := NewConnection(tc)

	if c.conn != tc {
		t.Error("conn should be the testConn")
	}
}

// Test message struct creation
func TestMessage_Structs(t *testing.T) {
	msg := Message{Type: 'Q', Data: []byte("SELECT 1")}
	if msg.Type != 'Q' {
		t.Errorf("Type = %c, want Q", msg.Type)
	}
	if string(msg.Data) != "SELECT 1" {
		t.Errorf("Data = %q, want SELECT 1", string(msg.Data))
	}
}

func TestHeader_Struct(t *testing.T) {
	h := Header{Type: 'R', Length: 8}
	if h.Type != 'R' {
		t.Errorf("Type = %c, want R", h.Type)
	}
	if h.Length != 8 {
		t.Errorf("Length = %d, want 8", h.Length)
	}
}

func TestStartupMessage_Struct(t *testing.T) {
	m := StartupMessage{
		ProtocolVersion: 196608,
		Parameters: map[string]string{
			"user":     "postgres",
			"database": "testdb",
		},
	}
	if m.ProtocolVersion != 196608 {
		t.Errorf("ProtocolVersion = %d, want 196608", m.ProtocolVersion)
	}
	if m.Parameters["user"] != "postgres" {
		t.Errorf("Parameters[user] = %q, want postgres", m.Parameters["user"])
	}
}

func TestPasswordMessage_Struct(t *testing.T) {
	m := PasswordMessage{Password: "secret"}
	if m.Password != "secret" {
		t.Errorf("Password = %q, want secret", m.Password)
	}
}

func TestQueryMessage_Struct(t *testing.T) {
	m := QueryMessage{Query: "SELECT 1"}
	if m.Query != "SELECT 1" {
		t.Errorf("Query = %q, want SELECT 1", m.Query)
	}
}

func TestParseMessage_Struct(t *testing.T) {
	m := ParseMessage{
		Name:           "stmt1",
		Query:          "SELECT $1",
		ParameterTypes: []int32{23},
	}
	if m.Name != "stmt1" {
		t.Errorf("Name = %q, want stmt1", m.Name)
	}
	if len(m.ParameterTypes) != 1 {
		t.Errorf("ParameterTypes len = %d, want 1", len(m.ParameterTypes))
	}
}

func TestBindMessage_Struct(t *testing.T) {
	m := BindMessage{
		PortalName:       "portal1",
		StatementName:    "stmt1",
		ParameterFormats: []int16{0, 1},
		Parameters:       [][]byte{[]byte("a"), []byte("b")},
		ResultFormats:    []int16{0},
	}
	if m.PortalName != "portal1" {
		t.Errorf("PortalName = %q, want portal1", m.PortalName)
	}
	if len(m.Parameters) != 2 {
		t.Errorf("Parameters len = %d, want 2", len(m.Parameters))
	}
}

func TestExecuteMessage_Struct(t *testing.T) {
	m := ExecuteMessage{PortalName: "portal1", MaxRows: 10}
	if m.PortalName != "portal1" {
		t.Errorf("PortalName = %q, want portal1", m.PortalName)
	}
	if m.MaxRows != 10 {
		t.Errorf("MaxRows = %d, want 10", m.MaxRows)
	}
}

func TestCommandCompleteMessage_Struct(t *testing.T) {
	m := CommandCompleteMessage{Tag: "SELECT 1"}
	if m.Tag != "SELECT 1" {
		t.Errorf("Tag = %q, want SELECT 1", m.Tag)
	}
}

func TestErrorResponseMessage_Struct(t *testing.T) {
	m := ErrorResponseMessage{
		Severity: "ERROR",
		SQLState: "42601",
		Message:  "syntax error",
		Detail:   "Detail info",
		Hint:     "Hint info",
	}
	if m.Severity != "ERROR" {
		t.Errorf("Severity = %q, want ERROR", m.Severity)
	}
	if m.SQLState != "42601" {
		t.Errorf("SQLState = %q, want 42601", m.SQLState)
	}
}

// Additional connection tests
func TestConnection_WriteMessage_Large(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)

	largeData := make([]byte, 10000)
	err := c.WriteMessage('D', largeData)
	if err != nil {
		t.Fatalf("WriteMessage with large data failed: %v", err)
	}

	// Check header
	if buf.Bytes()[0] != 'D' {
		t.Errorf("First byte = %c, want D", buf.Bytes()[0])
	}
	// Check length includes 4 bytes for length itself
	length := int32(binary.BigEndian.Uint32(buf.Bytes()[1:5]))
	if length != int32(len(largeData)+4) {
		t.Errorf("Length = %d, want %d", length, len(largeData)+4)
	}
}

func TestConnection_SendReadyForQuery_AllStatuses(t *testing.T) {
	statuses := []byte{TxIdle, TxActive, TxError}

	for _, status := range statuses {
		var buf bytes.Buffer
		conn := &testConn{writeBuf: &buf}
		c := NewConnection(conn)

		err := c.SendReadyForQuery(status)
		if err != nil {
			t.Fatalf("SendReadyForQuery(%c) failed: %v", status, err)
		}
		if buf.Len() != 6 {
			t.Errorf("ReadyForQuery length = %d, want 6", buf.Len())
		}
		if buf.Bytes()[5] != status {
			t.Errorf("Status = %c, want %c", buf.Bytes()[5], status)
		}
	}
}

func TestConnection_SendRowDescription(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)

	fields := []FieldDescription{
		{Name: "id", TypeOID: 23, TypeSize: 4},
		{Name: "name", TypeOID: 25, TypeSize: -1},
	}
	err := c.SendRowDescription(fields)
	if err != nil {
		t.Fatalf("SendRowDescription failed: %v", err)
	}
	if buf.Bytes()[0] != 'T' {
		t.Errorf("First byte = %c, want T", buf.Bytes()[0])
	}
}

func TestConnection_SendErrorResponse_Fields(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)

	err := c.SendErrorResponse("FATAL", "28P01", "authentication failed")
	if err != nil {
		t.Fatalf("SendErrorResponse failed: %v", err)
	}

	data := buf.Bytes()
	if data[0] != 'E' {
		t.Errorf("First byte = %c, want E", data[0])
	}

	// Check that message contains the error details
	if !bytes.Contains(data, []byte("FATAL")) {
		t.Error("Message should contain severity FATAL")
	}
	if !bytes.Contains(data, []byte("28P01")) {
		t.Error("Message should contain SQL state 28P01")
	}
	if !bytes.Contains(data, []byte("authentication failed")) {
		t.Error("Message should contain error message")
	}
}

func TestConnection_SendCommandComplete_Tags(t *testing.T) {
	tags := []string{
		"SELECT 1",
		"INSERT 0 1",
		"UPDATE 1",
		"DELETE 1",
		"CREATE TABLE",
		"DROP TABLE",
		"",
	}

	for _, tag := range tags {
		var buf bytes.Buffer
		conn := &testConn{writeBuf: &buf}
		c := NewConnection(conn)

		err := c.SendCommandComplete(tag)
		if err != nil {
			t.Fatalf("SendCommandComplete(%q) failed: %v", tag, err)
		}
		if buf.Bytes()[0] != 'C' {
			t.Errorf("First byte for %q = %c, want C", tag, buf.Bytes()[0])
		}
	}
}

func TestConnection_SendDataRow_Various(t *testing.T) {
	tests := [][][]byte{
		{},                              // Empty row
		{[]byte("hello")},               // One column
		{[]byte("a"), []byte("b")},      // Two columns
		{[]byte{}, []byte("c")},         // Empty value
		{[]byte("123"), []byte("456"), []byte("789")}, // Three columns
	}

	for _, columns := range tests {
		var buf bytes.Buffer
		conn := &testConn{writeBuf: &buf}
		c := NewConnection(conn)

		err := c.SendDataRow(columns)
		if err != nil {
			t.Fatalf("SendDataRow failed: %v", err)
		}
		if buf.Bytes()[0] != 'D' {
			t.Errorf("First byte = %c, want D", buf.Bytes()[0])
		}
	}
}

func TestConnection_SendParameterStatus(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)

	err := c.SendParameterStatus("server_version", "14.0")
	if err != nil {
		t.Fatalf("SendParameterStatus failed: %v", err)
	}
	if buf.Bytes()[0] != 'S' {
		t.Errorf("First byte = %c, want S", buf.Bytes()[0])
	}
}

func TestConnection_SendBackendKeyData(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)

	err := c.SendBackendKeyData(12345, 67890)
	if err != nil {
		t.Fatalf("SendBackendKeyData failed: %v", err)
	}
	if buf.Bytes()[0] != 'K' {
		t.Errorf("First byte = %c, want K", buf.Bytes()[0])
	}
}

func TestConnection_SendCloseComplete(t *testing.T) {
	var buf bytes.Buffer
	conn := &testConn{writeBuf: &buf}
	c := NewConnection(conn)

	err := c.SendCloseComplete()
	if err != nil {
		t.Fatalf("SendCloseComplete failed: %v", err)
	}
	if buf.Bytes()[0] != '3' {
		t.Errorf("First byte = %c, want 3", buf.Bytes()[0])
	}
}

// Connection deadline tests
func TestConnection_Deadline(t *testing.T) {
	conn := &testConn{}
	c := NewConnection(conn)

	err := c.SetDeadline(time.Now().Add(time.Minute))
	if err != nil {
		t.Errorf("SetDeadline error = %v", err)
	}
}

// Test Connection Close function
func TestConnection_Close(t *testing.T) {
	conn := &testConn{}
	c := NewConnection(conn)

	err := c.Close()
	if err != nil {
		t.Errorf("Close error = %v", err)
	}
	if !conn.closed {
		t.Error("Connection should be marked as closed")
	}
}
