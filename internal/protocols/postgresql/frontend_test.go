package postgresql

import (
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

func TestFrontendStateConstants(t *testing.T) {
	if StateStartup != 0 {
		t.Errorf("StateStartup = %d, want 0", StateStartup)
	}
	if StateSSLHandshake != 1 {
		t.Errorf("StateSSLHandshake = %d, want 1", StateSSLHandshake)
	}
	if StateAuthentication != 2 {
		t.Errorf("StateAuthentication = %d, want 2", StateAuthentication)
	}
	if StateIdle != 3 {
		t.Errorf("StateIdle = %d, want 3", StateIdle)
	}
	if StateActive != 4 {
		t.Errorf("StateActive = %d, want 4", StateActive)
	}
	if StateCopy != 5 {
		t.Errorf("StateCopy = %d, want 5", StateCopy)
	}
	if StateClosed != 6 {
		t.Errorf("StateClosed = %d, want 6", StateClosed)
	}
}

func TestNewFrontend(t *testing.T) {
	log, _ := logger.New("error", "json")
	conn := &testConn{}

	frontend := NewFrontend(conn, nil, nil, log)
	if frontend == nil {
		t.Fatal("NewFrontend returned nil")
	}

	if frontend.state != StateStartup {
		t.Errorf("Initial state = %d, want %d", frontend.state, StateStartup)
	}
}

func TestPreparedStatement(t *testing.T) {
	stmt := &PreparedStatement{
		Name:           "my_stmt",
		Query:          "SELECT $1",
		ParameterTypes: []int32{23},
	}
	if stmt.Name != "my_stmt" {
		t.Errorf("Name = %q, want my_stmt", stmt.Name)
	}
}

func TestPortal(t *testing.T) {
	portal := &Portal{
		Name: "my_portal",
		Statement: &PreparedStatement{
			Name: "my_stmt",
		},
		Parameters: [][]byte{[]byte("value")},
	}
	if portal.Name != "my_portal" {
		t.Errorf("Name = %q, want my_portal", portal.Name)
	}
}

func TestFieldDescription(t *testing.T) {
	field := FieldDescription{
		Name:         "test",
		TableOID:     1234,
		ColumnNumber: 1,
		TypeOID:      23,
		TypeSize:     4,
		TypeModifier: -1,
		Format:       0,
	}
	if field.Name != "test" {
		t.Errorf("Name = %q, want test", field.Name)
	}
}

func TestMessageTypeByteConstants(t *testing.T) {
	// Frontend messages
	if MsgBind != 'B' {
		t.Errorf("MsgBind = %c, want B", MsgBind)
	}
	if MsgClose != 'C' {
		t.Errorf("MsgClose = %c, want C", MsgClose)
	}
	if MsgDescribe != 'D' {
		t.Errorf("MsgDescribe = %c, want D", MsgDescribe)
	}
	if MsgExecute != 'E' {
		t.Errorf("MsgExecute = %c, want E", MsgExecute)
	}
	if MsgFunctionCall != 'F' {
		t.Errorf("MsgFunctionCall = %c, want F", MsgFunctionCall)
	}
	if MsgParse != 'P' {
		t.Errorf("MsgParse = %c, want P", MsgParse)
	}
	if MsgPasswordMessage != 'p' {
		t.Errorf("MsgPasswordMessage = %c, want p", MsgPasswordMessage)
	}
	if MsgQuery != 'Q' {
		t.Errorf("MsgQuery = %c, want Q", MsgQuery)
	}
	if MsgSync != 'S' {
		t.Errorf("MsgSync = %c, want S", MsgSync)
	}
	if MsgTerminate != 'X' {
		t.Errorf("MsgTerminate = %c, want X", MsgTerminate)
	}
	if MsgCopyData != 'd' {
		t.Errorf("MsgCopyData = %c, want d", MsgCopyData)
	}
	if MsgCopyDone != 'c' {
		t.Errorf("MsgCopyDone = %c, want c", MsgCopyDone)
	}
	if MsgCopyFail != 'f' {
		t.Errorf("MsgCopyFail = %c, want f", MsgCopyFail)
	}

	// Backend messages
	if MsgAuthentication != 'R' {
		t.Errorf("MsgAuthentication = %c, want R", MsgAuthentication)
	}
	if MsgBackendKeyData != 'K' {
		t.Errorf("MsgBackendKeyData = %c, want K", MsgBackendKeyData)
	}
	if MsgBindComplete != '2' {
		t.Errorf("MsgBindComplete = %c, want 2", MsgBindComplete)
	}
	if MsgCloseComplete != '3' {
		t.Errorf("MsgCloseComplete = %c, want 3", MsgCloseComplete)
	}
	if MsgCommandComplete != 'C' {
		t.Errorf("MsgCommandComplete = %c, want C", MsgCommandComplete)
	}
	if MsgCopyInResponse != 'G' {
		t.Errorf("MsgCopyInResponse = %c, want G", MsgCopyInResponse)
	}
	if MsgCopyOutResponse != 'H' {
		t.Errorf("MsgCopyOutResponse = %c, want H", MsgCopyOutResponse)
	}
	if MsgCopyBothResponse != 'W' {
		t.Errorf("MsgCopyBothResponse = %c, want W", MsgCopyBothResponse)
	}
	if MsgDataRow != 'D' {
		t.Errorf("MsgDataRow = %c, want D", MsgDataRow)
	}
	if MsgEmptyQueryResponse != 'I' {
		t.Errorf("MsgEmptyQueryResponse = %c, want I", MsgEmptyQueryResponse)
	}
	if MsgErrorResponse != 'E' {
		t.Errorf("MsgErrorResponse = %c, want E", MsgErrorResponse)
	}
	if MsgFunctionCallResponse != 'V' {
		t.Errorf("MsgFunctionCallResponse = %c, want V", MsgFunctionCallResponse)
	}
	if MsgNoData != 'n' {
		t.Errorf("MsgNoData = %c, want n", MsgNoData)
	}
	if MsgNoticeResponse != 'N' {
		t.Errorf("MsgNoticeResponse = %c, want N", MsgNoticeResponse)
	}
	if MsgNotificationResponse != 'A' {
		t.Errorf("MsgNotificationResponse = %c, want A", MsgNotificationResponse)
	}
	if MsgParameterDescription != 't' {
		t.Errorf("MsgParameterDescription = %c, want t", MsgParameterDescription)
	}
	if MsgParameterStatus != 'S' {
		t.Errorf("MsgParameterStatus = %c, want S", MsgParameterStatus)
	}
	if MsgParseComplete != '1' {
		t.Errorf("MsgParseComplete = %c, want 1", MsgParseComplete)
	}
	if MsgPortalSuspended != 's' {
		t.Errorf("MsgPortalSuspended = %c, want s", MsgPortalSuspended)
	}
	if MsgReadyForQuery != 'Z' {
		t.Errorf("MsgReadyForQuery = %c, want Z", MsgReadyForQuery)
	}
	if MsgRowDescription != 'T' {
		t.Errorf("MsgRowDescription = %c, want T", MsgRowDescription)
	}
}

func TestConnection_SetDeadline(t *testing.T) {
	conn := &testConn{}
	c := NewConnection(conn)
	err := c.SetDeadline(time.Now().Add(time.Second))
	// Should not panic
	_ = err
}

func TestFrontend_closed(t *testing.T) {
	log, _ := logger.New("error", "json")
	conn := &testConn{}

	frontend := NewFrontend(conn, nil, nil, log)
	if frontend == nil {
		t.Fatal("NewFrontend returned nil")
	}

	// Initially not closed
	if frontend.closed.Load() {
		t.Error("Frontend should not be closed initially")
	}

	// Mark as closed
	frontend.closed.Store(true)
	if !frontend.closed.Load() {
		t.Error("Frontend should be closed after Store(true)")
	}
}

func TestFrontend_processID(t *testing.T) {
	log, _ := logger.New("error", "json")
	conn := &testConn{}

	frontend := NewFrontend(conn, nil, nil, log)
	if frontend == nil {
		t.Fatal("NewFrontend returned nil")
	}

	// Set process ID
	frontend.processID = 12345
	if frontend.processID != 12345 {
		t.Errorf("processID = %d, want 12345", frontend.processID)
	}
}

func TestFrontend_secretKey(t *testing.T) {
	log, _ := logger.New("error", "json")
	conn := &testConn{}

	frontend := NewFrontend(conn, nil, nil, log)
	if frontend == nil {
		t.Fatal("NewFrontend returned nil")
	}

	// Set secret key
	frontend.secretKey = 67890
	if frontend.secretKey != 67890 {
		t.Errorf("secretKey = %d, want 67890", frontend.secretKey)
	}
}

func TestFrontend_database(t *testing.T) {
	log, _ := logger.New("error", "json")
	conn := &testConn{}

	frontend := NewFrontend(conn, nil, nil, log)
	if frontend == nil {
		t.Fatal("NewFrontend returned nil")
	}

	// Set database
	frontend.database = "testdb"
	if frontend.database != "testdb" {
		t.Errorf("database = %q, want testdb", frontend.database)
	}
}

func TestBindMessage(t *testing.T) {
	msg := &BindMessage{
		PortalName:       "my_portal",
		StatementName:    "my_stmt",
		ParameterFormats: []int16{0},
		Parameters:       [][]byte{[]byte("value")},
		ResultFormats:    []int16{0},
	}
	if msg.PortalName != "my_portal" {
		t.Errorf("PortalName = %q, want my_portal", msg.PortalName)
	}
	if msg.StatementName != "my_stmt" {
		t.Errorf("StatementName = %q, want my_stmt", msg.StatementName)
	}
}

func TestParseMessage(t *testing.T) {
	msg := &ParseMessage{
		Name:           "my_stmt",
		Query:          "SELECT $1",
		ParameterTypes: []int32{23},
	}
	if msg.Name != "my_stmt" {
		t.Errorf("Name = %q, want my_stmt", msg.Name)
	}
	if len(msg.ParameterTypes) != 1 {
		t.Errorf("ParameterTypes count = %d, want 1", len(msg.ParameterTypes))
	}
}

func TestExecuteMessage(t *testing.T) {
	msg := &ExecuteMessage{
		PortalName: "my_portal",
		MaxRows:    100,
	}
	if msg.PortalName != "my_portal" {
		t.Errorf("PortalName = %q, want my_portal", msg.PortalName)
	}
	if msg.MaxRows != 100 {
		t.Errorf("MaxRows = %d, want 100", msg.MaxRows)
	}
}

func TestPasswordMessage(t *testing.T) {
	msg := &PasswordMessage{
		Password: "secret123",
	}
	if msg.Password != "secret123" {
		t.Errorf("Password = %q, want secret123", msg.Password)
	}
}

func TestQueryMessage(t *testing.T) {
	msg := &QueryMessage{
		Query: "SELECT * FROM users",
	}
	if msg.Query != "SELECT * FROM users" {
		t.Errorf("Query = %q, want SELECT * FROM users", msg.Query)
	}
}

func TestStartupMessage(t *testing.T) {
	msg := &StartupMessage{
		ProtocolVersion: 196608,
		Parameters: map[string]string{
			"user":     "testuser",
			"database": "testdb",
		},
	}
	if msg.ProtocolVersion != 196608 {
		t.Errorf("ProtocolVersion = %d, want 196608", msg.ProtocolVersion)
	}
	if msg.Parameters["user"] != "testuser" {
		t.Errorf("user = %q, want testuser", msg.Parameters["user"])
	}
}

func TestCommandCompleteMessage(t *testing.T) {
	cc := &CommandCompleteMessage{
		Tag: "SELECT 5",
	}
	if cc.Tag != "SELECT 5" {
		t.Errorf("Tag = %q, want SELECT 5", cc.Tag)
	}
}

func TestErrorResponseMessage(t *testing.T) {
	err := &ErrorResponseMessage{
		Severity: "ERROR",
		SQLState: "42601",
		Message:  "syntax error",
	}
	if err.Severity != "ERROR" {
		t.Errorf("Severity = %q, want ERROR", err.Severity)
	}
	if err.SQLState != "42601" {
		t.Errorf("SQLState = %q, want 42601", err.SQLState)
	}
}

func TestDataRowMessage(t *testing.T) {
	row := &DataRowMessage{
		Values: [][]byte{
			[]byte("1"),
			[]byte("hello"),
		},
	}
	if len(row.Values) != 2 {
		t.Errorf("Values count = %d, want 2", len(row.Values))
	}
}

func TestRowDescriptionMessage(t *testing.T) {
	rowDesc := &RowDescriptionMessage{
		Fields: []FieldDescription{
			{
				Name:     "id",
				TypeOID:  23,
				TypeSize: 4,
			},
		},
	}
	if len(rowDesc.Fields) != 1 {
		t.Errorf("Fields count = %d, want 1", len(rowDesc.Fields))
	}
}

func TestParameterStatusMessage(t *testing.T) {
	ps := &ParameterStatusMessage{
		Name:  "server_version",
		Value: "14.0",
	}
	if ps.Name != "server_version" {
		t.Errorf("Name = %q, want server_version", ps.Name)
	}
	if ps.Value != "14.0" {
		t.Errorf("Value = %q, want 14.0", ps.Value)
	}
}

func TestReadyForQueryMessage(t *testing.T) {
	rfq := &ReadyForQueryMessage{
		Status: 'I',
	}
	if rfq.Status != 'I' {
		t.Errorf("Status = %c, want I", rfq.Status)
	}
}

func TestAuthenticationMessage(t *testing.T) {
	auth := &AuthenticationMessage{
		Type: AuthOK,
		Salt: []byte{1, 2, 3, 4},
		Data: []byte("auth data"),
	}
	if auth.Type != AuthOK {
		t.Errorf("Type = %d, want %d", auth.Type, AuthOK)
	}
}

func TestMessage(t *testing.T) {
	msg := &Message{
		Type: 'Q',
		Data: []byte("SELECT 1"),
	}
	if msg.Type != 'Q' {
		t.Errorf("Type = %c, want Q", msg.Type)
	}
}

func TestHeader(t *testing.T) {
	header := &Header{
		Type:   'R',
		Length: 10,
	}
	if header.Type != 'R' {
		t.Errorf("Type = %c, want R", header.Type)
	}
	if header.Length != 10 {
		t.Errorf("Length = %d, want 10", header.Length)
	}
}

// Additional tests for Frontend struct and methods
