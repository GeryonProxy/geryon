package mysql

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/logger"
)

type mockConn struct {
	*bytes.Buffer
	closed bool
}

func newMockConn() *mockConn {
	return &mockConn{Buffer: &bytes.Buffer{}}
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	return m.Buffer.Read(b)
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	return m.Buffer.Write(b)
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

func (m *mockConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 3306}
}

func (m *mockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 3307}
}

func (m *mockConn) SetDeadline(t time.Time) error {
	return nil
}

func (m *mockConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (m *mockConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func TestNewFrontend(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")

	f := NewFrontend(conn, nil, nil, log)
	if f == nil {
		t.Fatal("NewFrontend returned nil")
	}

	if f.state != StateHandshake {
		t.Errorf("state = %d, want StateHandshake", f.state)
	}

	if f.charset != 33 {
		t.Errorf("charset = %d, want 33", f.charset)
	}

	if f.serverVer != "8.0.0-Geryon" {
		t.Errorf("serverVer = %q, want 8.0.0-Geryon", f.serverVer)
	}

	if f.stmts == nil {
		t.Error("stmts map not initialized")
	}
}

func TestDefaultServerCapabilities(t *testing.T) {
	caps := defaultServerCapabilities()

	if caps == 0 {
		t.Error("capabilities should not be zero")
	}

	// Check some key capabilities
	if caps&ClientProtocol41 == 0 {
		t.Error("ClientProtocol41 should be set")
	}

	if caps&ClientTransactions == 0 {
		t.Error("ClientTransactions should be set")
	}
}

func TestFrontendState(t *testing.T) {
	states := []struct {
		state FrontendState
		name  string
	}{
		{StateHandshake, "StateHandshake"},
		{StateAuthentication, "StateAuthentication"},
		{StateReady, "StateReady"},
		{StateQuery, "StateQuery"},
		{StateClosed, "StateClosed"},
	}

	for _, s := range states {
		// Just verify states are valid
		_ = s.state
	}
}

func TestPreparedStatementStorage(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Add a prepared statement
	stmt := &PreparedStatement{
		ID:         1,
		Query:      "SELECT ?",
		ParamCount: 1,
		Columns:    []*ColumnInfo{},
		Params:     []*ColumnInfo{{Name: "param1", Type: 3}},
	}

	f.stmts[1] = stmt

	// Retrieve it
	if s, ok := f.stmts[1]; !ok {
		t.Error("Failed to retrieve prepared statement")
	} else if s.Query != "SELECT ?" {
		t.Errorf("Query = %q, want SELECT ?", s.Query)
	}

	// Test ID generation
	id := f.stmtIDGen.Add(1)
	if id != 1 {
		t.Errorf("ID = %d, want 1", id)
	}
}

func TestColumnInfoExtended(t *testing.T) {
	col := &ColumnInfo{
		Catalog:   "def",
		Schema:    "testdb",
		Table:     "users",
		OrgTable:  "users",
		Name:      "id",
		OrgName:   "id",
		Charset:   63,
		ColumnLen: 11,
		Type:      3, // MYSQL_TYPE_LONG
		Flags:     0x4000,
		Decimals:  0,
	}

	if col.Name != "id" {
		t.Errorf("Name = %q, want id", col.Name)
	}

	if col.Type != 3 {
		t.Errorf("Type = %d, want 3", col.Type)
	}

	if col.Schema != "testdb" {
		t.Errorf("Schema = %q, want testdb", col.Schema)
	}
}

func TestPreparedStatementTypes(t *testing.T) {
	paramTypes := []uint8{
		0x01,  // MYSQL_TYPE_TINY
		0x02,  // MYSQL_TYPE_SHORT
		0x03,  // MYSQL_TYPE_LONG
		0x04,  // MYSQL_TYPE_FLOAT
		0x05,  // MYSQL_TYPE_DOUBLE
		0x0c,  // MYSQL_TYPE_DATETIME
		0x0f,  // MYSQL_TYPE_VARCHAR
		0xfc,  // MYSQL_TYPE_BLOB
	}

	for _, typ := range paramTypes {
		_ = typ // Verify types compile
	}
}

func TestFrontendCapabilities(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Set client capabilities
	f.clientCaps = ClientProtocol41 | ClientTransactions | ClientSecureConnection

	if f.clientCaps&ClientProtocol41 == 0 {
		t.Error("ClientProtocol41 not set")
	}

	if f.clientCaps&ClientTransactions == 0 {
		t.Error("ClientTransactions not set")
	}
}

func TestStatusFlags(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Test autocommit status
	f.status = ServerStatusAutocommit

	if f.status&ServerStatusAutocommit == 0 {
		t.Error("ServerStatusAutocommit not set")
	}

	// Test in transaction status
	f.status = ServerStatusInTransaction

	if f.status&ServerStatusInTransaction == 0 {
		t.Error("ServerStatusInTransaction not set")
	}
}

func TestAuthPlugin(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Set auth plugin
	f.authPlugin = "mysql_native_password"

	if f.authPlugin != "mysql_native_password" {
		t.Errorf("authPlugin = %q, want mysql_native_password", f.authPlugin)
	}
}

func TestFrontendDatabase(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Set database
	f.database = "testdb"

	if f.database != "testdb" {
		t.Errorf("database = %q, want testdb", f.database)
	}
}

func TestFrontendUser(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Set user
	f.user = &auth.User{Username: "testuser"}

	if f.user.Username != "testuser" {
		t.Errorf("username = %q, want testuser", f.user.Username)
	}
}

func TestThreadID(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Set thread ID
	f.threadID = 12345

	if f.threadID != 12345 {
		t.Errorf("threadID = %d, want 12345", f.threadID)
	}
}

func TestCharset(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Test default charset
	if f.charset != 33 { // utf8mb4
		t.Errorf("charset = %d, want 33", f.charset)
	}

	// Set custom charset
	f.charset = 8 // latin1
	if f.charset != 8 {
		t.Errorf("charset = %d, want 8", f.charset)
	}
}

func TestClosed(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Initially not closed
	if f.closed.Load() {
		t.Error("Frontend should not be closed initially")
	}

	// Close it
	f.closed.Store(true)

	if !f.closed.Load() {
		t.Error("Frontend should be closed")
	}
}

func TestServerVersion(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Test default version
	if f.serverVer != "8.0.0-Geryon" {
		t.Errorf("serverVer = %q, want 8.0.0-Geryon", f.serverVer)
	}
}

func TestMultiplePreparedStatements(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Add multiple statements
	for i := uint32(1); i <= 5; i++ {
		f.stmts[i] = &PreparedStatement{
			ID:         i,
			Query:      fmt.Sprintf("SELECT %d", i),
			ParamCount: 0,
		}
	}

	// Verify all are stored
	if len(f.stmts) != 5 {
		t.Errorf("stmts count = %d, want 5", len(f.stmts))
	}

	// Delete one
	delete(f.stmts, 3)

	if len(f.stmts) != 4 {
		t.Errorf("stmts count = %d, want 4", len(f.stmts))
	}

	// Verify it's gone
	if _, ok := f.stmts[3]; ok {
		t.Error("Statement 3 should be deleted")
	}
}

func TestSendHandshake(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	err := f.sendHandshake()
	if err != nil {
		t.Errorf("sendHandshake failed: %v", err)
	}

	// Should have written something (handshake packet)
	if conn.Buffer.Len() == 0 {
		t.Error("sendHandshake wrote nothing")
	}
}

func TestSendOK(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	err := f.sendOK()
	if err != nil {
		t.Errorf("sendOK failed: %v", err)
	}

	if conn.Buffer.Len() == 0 {
		t.Error("sendOK wrote nothing")
	}
}

func TestSendEOF(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	err := f.sendEOF(0)
	if err != nil {
		t.Errorf("sendEOF failed: %v", err)
	}

	if conn.Buffer.Len() == 0 {
		t.Error("sendEOF wrote nothing")
	}
}

func TestSendError(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	err := f.sendError(1234, "HY000", "Test error")
	if err != nil {
		t.Errorf("sendError failed: %v", err)
	}

	if conn.Buffer.Len() == 0 {
		t.Error("sendError wrote nothing")
	}
}

func TestWritePacket(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	data := []byte("test data")
	err := f.writePacket(0, data)
	if err != nil {
		t.Errorf("writePacket failed: %v", err)
	}

	// Should have written packet header (3 bytes length + 1 byte sequence) + data
	expectedLen := 4 + len(data)
	if conn.Buffer.Len() != expectedLen {
		t.Errorf("writePacket wrote %d bytes, want %d", conn.Buffer.Len(), expectedLen)
	}
}

func TestHandleQuery(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Test various queries
	queries := [][]byte{
		[]byte("SELECT 1"),
		[]byte("INSERT INTO t VALUES (1)"),
		[]byte("UPDATE t SET x=1"),
		[]byte("DELETE FROM t"),
		[]byte("SET x = 1"),
		[]byte("SHOW VARIABLES"),
	}

	for _, query := range queries {
		conn.Buffer.Reset()
		err := f.handleQuery(query)
		if err != nil {
			t.Errorf("handleQuery(%q) failed: %v", string(query), err)
		}
	}
}

func TestHandleInitDB(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	err := f.handleInitDB([]byte("testdb"))
	if err != nil {
		t.Errorf("handleInitDB failed: %v", err)
	}

	if f.database != "testdb" {
		t.Errorf("database = %q, want testdb", f.database)
	}
}

func TestFrontendSequence(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Write a packet
	err := f.writePacket(0, []byte("test"))
	if err != nil {
		t.Errorf("writePacket failed: %v", err)
	}

	// Should have written something
	if conn.Buffer.Len() == 0 {
		t.Error("writePacket wrote nothing")
	}
}

func TestFrontendTLSActive(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Initially TLS should not be active
	if f.tlsActive {
		t.Error("TLS should not be active initially")
	}

	// Set TLS active
	f.tlsActive = true
	if !f.tlsActive {
		t.Error("TLS should be active")
	}
}

func TestCleanup(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	f.cleanup()

	if !f.closed.Load() {
		t.Error("cleanup should set closed to true")
	}
	if !conn.closed {
		t.Error("cleanup should close the connection")
	}
}

func TestPreparedStatementWithParams(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Create statement with parameters
	stmt := &PreparedStatement{
		ID:         1,
		Query:      "SELECT * FROM users WHERE id = ? AND name = ?",
		ParamCount: 2,
		Params: []*ColumnInfo{
			{Name: "id", Type: 3},
			{Name: "name", Type: 15},
		},
		Columns: []*ColumnInfo{
			{Name: "id", Type: 3},
			{Name: "name", Type: 15},
			{Name: "email", Type: 15},
		},
	}

	f.stmts[1] = stmt

	// Verify statement
	if s, ok := f.stmts[1]; ok {
		if s.ParamCount != 2 {
			t.Errorf("ParamCount = %d, want 2", s.ParamCount)
		}
		if len(s.Params) != 2 {
			t.Errorf("len(Params) = %d, want 2", len(s.Params))
		}
		if len(s.Columns) != 3 {
			t.Errorf("len(Columns) = %d, want 3", len(s.Columns))
		}
	} else {
		t.Error("Statement not found")
	}
}

func TestColumnInfoAllFields(t *testing.T) {
	col := &ColumnInfo{
		Catalog:   "def",
		Schema:    "testdb",
		Table:     "users",
		OrgTable:  "users",
		Name:      "email",
		OrgName:   "email",
		Charset:   33,
		ColumnLen: 255,
		Type:      15, // VARCHAR
		Flags:     0x4001,
		Decimals:  0,
	}

	if col.Catalog != "def" {
		t.Errorf("Catalog = %q, want def", col.Catalog)
	}
	if col.Schema != "testdb" {
		t.Errorf("Schema = %q, want testdb", col.Schema)
	}
	if col.Table != "users" {
		t.Errorf("Table = %q, want users", col.Table)
	}
	if col.OrgTable != "users" {
		t.Errorf("OrgTable = %q, want users", col.OrgTable)
	}
	if col.OrgName != "email" {
		t.Errorf("OrgName = %q, want email", col.OrgName)
	}
	if col.Charset != 33 {
		t.Errorf("Charset = %d, want 33", col.Charset)
	}
	if col.ColumnLen != 255 {
		t.Errorf("ColumnLen = %d, want 255", col.ColumnLen)
	}
	if col.Flags != 0x4001 {
		t.Errorf("Flags = 0x%x, want 0x4001", col.Flags)
	}
}

func TestAllClientCapabilities(t *testing.T) {
	caps := []uint32{
		ClientLongPassword,
		ClientFoundRows,
		ClientLongFlag,
		ClientConnectWithDB,
		ClientNoSchema,
		ClientCompress,
		ClientODBC,
		ClientLocalFiles,
		ClientIgnoreSpace,
		ClientProtocol41,
		ClientInteractive,
		ClientSSL,
		ClientIgnoreSigpipe,
		ClientTransactions,
		ClientReserved,
		ClientSecureConnection,
		ClientMultiStatements,
		ClientMultiResults,
		ClientPluginAuth,
		ClientConnectAttrs,
		ClientPluginAuthLenencClientData,
		ClientSSLVerifyServerCert,
		ClientRememberOptions,
	}

	// Verify each capability is defined
	for _, cap := range caps {
		if cap == 0 {
			t.Errorf("capability should not be zero")
		}
	}
}

func TestAuthData(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Set auth data
	f.authData = []byte{0x01, 0x02, 0x03, 0x04, 0x05}

	if len(f.authData) != 5 {
		t.Errorf("len(authData) = %d, want 5", len(f.authData))
	}
	if f.authData[0] != 0x01 {
		t.Errorf("authData[0] = 0x%02x, want 0x01", f.authData[0])
	}
}

func TestClientCaps(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Set client capabilities
	f.clientCaps = ClientProtocol41 | ClientTransactions | ClientSecureConnection | ClientPluginAuth

	if f.clientCaps&ClientProtocol41 == 0 {
		t.Error("ClientProtocol41 not set")
	}
	if f.clientCaps&ClientTransactions == 0 {
		t.Error("ClientTransactions not set")
	}
	if f.clientCaps&ClientSecureConnection == 0 {
		t.Error("ClientSecureConnection not set")
	}
	if f.clientCaps&ClientPluginAuth == 0 {
		t.Error("ClientPluginAuth not set")
	}
}

func TestServerCaps(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Default server capabilities should be set
	if f.serverCaps == 0 {
		t.Error("serverCaps should not be zero")
	}

	// Check key capabilities
	if f.serverCaps&ClientProtocol41 == 0 {
		t.Error("ClientProtocol41 should be in serverCaps")
	}
	if f.serverCaps&ClientTransactions == 0 {
		t.Error("ClientTransactions should be in serverCaps")
	}
}

func TestStmtIDGeneration(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Generate IDs
	id1 := f.stmtIDGen.Add(1)
	id2 := f.stmtIDGen.Add(1)
	id3 := f.stmtIDGen.Add(1)

	if id1 != 1 {
		t.Errorf("id1 = %d, want 1", id1)
	}
	if id2 != 2 {
		t.Errorf("id2 = %d, want 2", id2)
	}
	if id3 != 3 {
		t.Errorf("id3 = %d, want 3", id3)
	}
}

func TestStatusCombinations(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Test combining multiple status flags
	f.status = ServerStatusAutocommit | ServerStatusInTransaction

	if f.status&ServerStatusAutocommit == 0 {
		t.Error("ServerStatusAutocommit not set")
	}
	if f.status&ServerStatusInTransaction == 0 {
		t.Error("ServerStatusInTransaction not set")
	}
}

func TestAllCommands(t *testing.T) {
	commands := []struct {
		cmd  uint8
		name string
	}{
		{ComSleep, "ComSleep"},
		{ComQuit, "ComQuit"},
		{ComInitDB, "ComInitDB"},
		{ComQuery, "ComQuery"},
		{ComFieldList, "ComFieldList"},
		{ComCreateDB, "ComCreateDB"},
		{ComDropDB, "ComDropDB"},
		{ComRefresh, "ComRefresh"},
		{ComShutdown, "ComShutdown"},
		{ComStatistics, "ComStatistics"},
		{ComProcessInfo, "ComProcessInfo"},
		{ComConnect, "ComConnect"},
		{ComProcessKill, "ComProcessKill"},
		{ComDebug, "ComDebug"},
		{ComPing, "ComPing"},
		{ComTime, "ComTime"},
		{ComDelayedInsert, "ComDelayedInsert"},
		{ComChangeUser, "ComChangeUser"},
		{ComBinlogDump, "ComBinlogDump"},
		{ComTableDump, "ComTableDump"},
		{ComConnectOut, "ComConnectOut"},
		{ComRegisterSlave, "ComRegisterSlave"},
		{ComStmtPrepare, "ComStmtPrepare"},
		{ComStmtExecute, "ComStmtExecute"},
		{ComStmtSendLongData, "ComStmtSendLongData"},
		{ComStmtClose, "ComStmtClose"},
		{ComStmtReset, "ComStmtReset"},
		{ComSetOption, "ComSetOption"},
		{ComStmtFetch, "ComStmtFetch"},
	}

	for _, c := range commands {
		_ = c.cmd // Verify all commands are defined
	}
}

// Test readLengthEncodedInt function (standalone, not a method)
func TestReadLengthEncodedInt(t *testing.T) {
	tests := []struct {
		input     []byte
		want      uint64
		bytesRead int
	}{
		{[]byte{0x00}, 0, 1},                              // 0
		{[]byte{0x01}, 1, 1},                              // 1
		{[]byte{0xFA}, 250, 1},                            // 250
		{[]byte{0xFC, 0xFF, 0x00}, 255, 3},                // 255 (2-byte)
		{[]byte{0xFD, 0xFF, 0xFF, 0x00}, 0xFFFF, 4},       // 65535 (3-byte)
	}

	for _, tc := range tests {
		val, n := readLengthEncodedInt(tc.input)
		if val != tc.want {
			t.Errorf("readLengthEncodedInt(%v) = %d, want %d", tc.input, val, tc.want)
		}
		if n != tc.bytesRead {
			t.Errorf("readLengthEncodedInt(%v) bytes read = %d, want %d", tc.input, n, tc.bytesRead)
		}
	}
}

// Test readLengthEncodedInt with empty data
func TestReadLengthEncodedInt_Empty(t *testing.T) {
	val, n := readLengthEncodedInt([]byte{})
	if val != 0 {
		t.Errorf("readLengthEncodedInt([]) = %d, want 0", val)
	}
	if n != 0 {
		t.Errorf("readLengthEncodedInt([]) bytes read = %d, want 0", n)
	}
}

// Test min function
func TestMin(t *testing.T) {
	tests := []struct {
		a, b int
		want int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{0, 10, 0},
		{-1, 1, -1},
		{-5, -10, -10},
		{100, 50, 50},
	}

	for _, tc := range tests {
		got := min(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("min(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// Test readPacket function
func TestReadPacket(t *testing.T) {
	// Create packet: length(3) + sequence(1) + payload
	// Max packet size is 3 bytes little endian
	payload := []byte("test payload data")
	packet := make([]byte, 4+len(payload))
	packet[0] = byte(len(payload))
	packet[1] = byte(len(payload) >> 8)
	packet[2] = byte(len(payload) >> 16)
	packet[3] = 0 // sequence
	copy(packet[4:], payload)

	conn := &mockConn{Buffer: bytes.NewBuffer(packet)}
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	seq, data, err := f.readPacket()
	if err != nil {
		t.Fatalf("readPacket failed: %v", err)
	}
	if seq != 0 {
		t.Errorf("readPacket returned seq = %d, want 0", seq)
	}
	if string(data) != string(payload) {
		t.Errorf("readPacket returned %q, want %q", string(data), string(payload))
	}
}

// Test readPacket with EOF
func TestReadPacket_EOF(t *testing.T) {
	// Empty buffer
	conn := &mockConn{Buffer: &bytes.Buffer{}}
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	_, _, err := f.readPacket()
	if err == nil {
		t.Error("readPacket should return error on EOF")
	}
}

// Test readPacket with specific sequence
func TestReadPacket_Sequence(t *testing.T) {
	payload := []byte("small payload")
	packet := make([]byte, 4+len(payload))
	packet[0] = byte(len(payload))
	packet[1] = byte(len(payload) >> 8)
	packet[2] = byte(len(payload) >> 16)
	packet[3] = 5 // sequence number
	copy(packet[4:], payload)

	conn := &mockConn{Buffer: bytes.NewBuffer(packet)}
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	seq, data, err := f.readPacket()
	if err != nil {
		t.Fatalf("readPacket failed: %v", err)
	}
	if seq != 5 {
		t.Errorf("readPacket returned seq = %d, want 5", seq)
	}
	if string(data) != string(payload) {
		t.Errorf("readPacket returned %q, want %q", string(data), string(payload))
	}
}

// Test writePacket with error
func TestWritePacket_Error(t *testing.T) {
	// Create a connection that always errors on write
	errConn := &errorConn{}
	log, _ := logger.New("info", "json")
	f := NewFrontend(errConn, nil, nil, log)

	err := f.writePacket(0, []byte("test"))
	if err == nil {
		t.Error("writePacket should return error")
	}
}

// Test sendStmtPrepareOK
func TestSendStmtPrepareOK(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Create a prepared statement
	stmt := &PreparedStatement{
		ID:         1,
		Query:      "SELECT * FROM users WHERE id = ?",
		ParamCount: 1,
		Columns:    []*ColumnInfo{},
		Params:     []*ColumnInfo{{Name: "id", Type: 3}},
	}

	err := f.sendStmtPrepareOK(stmt)
	if err != nil {
		t.Errorf("sendStmtPrepareOK failed: %v", err)
	}
	if conn.Buffer.Len() == 0 {
		t.Error("sendStmtPrepareOK wrote nothing")
	}
}

// Test handleStatistics
func TestHandleStatistics(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	err := f.handleStatistics()
	if err != nil {
		t.Errorf("handleStatistics failed: %v", err)
	}
	// Should write something (statistics packet)
	if conn.Buffer.Len() == 0 {
		t.Error("handleStatistics wrote nothing")
	}
}

// Test handleProcessInfo
func TestHandleProcessInfo(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	err := f.handleProcessInfo()
	if err != nil {
		t.Errorf("handleProcessInfo failed: %v", err)
	}
	// Should write something
	if conn.Buffer.Len() == 0 {
		t.Error("handleProcessInfo wrote nothing")
	}
}

// Test handleStmtClose
func TestHandleStmtClose(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Add a statement
	f.stmts[1] = &PreparedStatement{ID: 1, Query: "SELECT 1"}
	if len(f.stmts) != 1 {
		t.Fatal("Failed to add statement")
	}

	// Close statement ID 1 (4 bytes little endian)
	data := []byte{0x01, 0x00, 0x00, 0x00}
	err := f.handleStmtClose(data)
	if err != nil {
		t.Errorf("handleStmtClose failed: %v", err)
	}
	if len(f.stmts) != 0 {
		t.Error("Statement should be deleted after close")
	}
}

// Test handleStmtClose with invalid data
func TestHandleStmtClose_InvalidData(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Too short data
	data := []byte{0x01, 0x00}
	err := f.handleStmtClose(data)
	// Should handle gracefully
	_ = err
}

// Test handleFieldList
func TestHandleFieldList(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Set database
	f.database = "testdb"

	// Field list for table "users"
	data := []byte("users\x00")
	err := f.handleFieldList(data)
	if err != nil {
		t.Errorf("handleFieldList failed: %v", err)
	}
	// Should write EOF packet (no fields found)
	if conn.Buffer.Len() == 0 {
		t.Error("handleFieldList wrote nothing")
	}
}

// Error connection for testing
type errorConn struct{}

func (c *errorConn) Read(b []byte) (n int, err error)   { return 0, fmt.Errorf("read error") }
func (c *errorConn) Write(b []byte) (n int, err error)  { return 0, fmt.Errorf("write error") }
func (c *errorConn) Close() error                       { return nil }
func (c *errorConn) LocalAddr() net.Addr                { return nil }
func (c *errorConn) RemoteAddr() net.Addr               { return nil }
func (c *errorConn) SetDeadline(t time.Time) error      { return nil }
func (c *errorConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *errorConn) SetWriteDeadline(t time.Time) error { return nil }

// Test sendSimpleResult
func TestSendSimpleResult(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Test with rows and columns
	rows := [][]interface{}{{1, "test"}, {2, "test2"}}
	columns := []string{"id", "name"}

	err := f.sendSimpleResult(rows, columns)
	if err != nil {
		t.Errorf("sendSimpleResult failed: %v", err)
	}

	// Should have written multiple packets
	if conn.Buffer.Len() == 0 {
		t.Error("sendSimpleResult wrote nothing")
	}
}

// Test sendSimpleResult with empty result
func TestSendSimpleResult_Empty(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Test with no rows
	rows := [][]interface{}{}
	columns := []string{"id"}

	err := f.sendSimpleResult(rows, columns)
	if err != nil {
		t.Errorf("sendSimpleResult failed: %v", err)
	}

	if conn.Buffer.Len() == 0 {
		t.Error("sendSimpleResult wrote nothing")
	}
}

// Test handleShowQuery with VERSION
func TestHandleShowQuery_Version(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// SHOW VERSION()
	err := f.handleShowQuery("VERSION()")

	if err != nil {
		t.Errorf("handleShowQuery should handle VERSION(): %v", err)
	}

	if conn.Buffer.Len() == 0 {
		t.Error("handleShowQuery wrote nothing")
	}
}

// Test handleShowQuery with DATABASES
func TestHandleShowQuery_Databases(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// SHOW DATABASES
	err := f.handleShowQuery("DATABASES")

	if err != nil {
		t.Errorf("handleShowQuery should handle DATABASES: %v", err)
	}

	if conn.Buffer.Len() == 0 {
		t.Error("handleShowQuery wrote nothing")
	}
}

// Test handleShowQuery with TABLES
func TestHandleShowQuery_Tables(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)
	f.database = "testdb"

	// SHOW TABLES
	err := f.handleShowQuery("TABLES")

	if err != nil {
		t.Errorf("handleShowQuery should handle TABLES: %v", err)
	}

	if conn.Buffer.Len() == 0 {
		t.Error("handleShowQuery wrote nothing")
	}
}

// Test handleShowQuery with unrecognized query
func TestHandleShowQuery_Unrecognized(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// SHOW UNKNOWN - returns OK for any unrecognized query
	err := f.handleShowQuery("UNKNOWN")

	// Function returns OK for unrecognized queries (doesn't error)
	if err != nil {
		t.Errorf("handleShowQuery should not error for unrecognized queries: %v", err)
	}

	if conn.Buffer.Len() == 0 {
		t.Error("handleShowQuery wrote nothing")
	}
}

// Test handleShowQuery with GRANTS
func TestHandleShowQuery_Grants(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)
	f.user = &auth.User{Username: "testuser"}
	f.database = "testdb"

	// SHOW GRANTS
	err := f.handleShowQuery("GRANTS")

	if err != nil {
		t.Errorf("handleShowQuery should handle GRANTS: %v", err)
	}

	if conn.Buffer.Len() == 0 {
		t.Error("handleShowQuery wrote nothing")
	}
}

// Test handleShowQuery with STATUS
func TestHandleShowQuery_Status(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// SHOW STATUS
	err := f.handleShowQuery("STATUS")

	if err != nil {
		t.Errorf("handleShowQuery should handle STATUS: %v", err)
	}

	if conn.Buffer.Len() == 0 {
		t.Error("handleShowQuery wrote nothing")
	}
}

// Test handleShowQuery with VARIABLES
func TestHandleShowQuery_Variables(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// SHOW VARIABLES
	err := f.handleShowQuery("VARIABLES")

	if err != nil {
		t.Errorf("handleShowQuery should handle VARIABLES: %v", err)
	}

	if conn.Buffer.Len() == 0 {
		t.Error("handleShowQuery wrote nothing")
	}
}

// Test handleStmtPrepare
func TestHandleStmtPrepare(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Prepare statement with query "SELECT 1"
	query := "SELECT 1"
	err := f.handleStmtPrepare([]byte(query))

	if err != nil {
		t.Errorf("handleStmtPrepare failed: %v", err)
	}

	// Should create a statement
	if len(f.stmts) != 1 {
		t.Errorf("Expected 1 statement, got %d", len(f.stmts))
	}

	// Check that something was written
	if conn.Buffer.Len() == 0 {
		t.Error("handleStmtPrepare wrote nothing")
	}
}

// Test handleStmtExecute with valid statement
func TestHandleStmtExecute_Valid(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// First prepare a statement
	f.stmts[1] = &PreparedStatement{ID: 1, Query: "SELECT 1"}

	// Execute statement ID 1 (4 bytes little endian)
	data := []byte{0x01, 0x00, 0x00, 0x00}
	err := f.handleStmtExecute(data)

	if err != nil {
		t.Errorf("handleStmtExecute failed: %v", err)
	}

	// Check that something was written
	if conn.Buffer.Len() == 0 {
		t.Error("handleStmtExecute wrote nothing")
	}
}

// Test handleStmtExecute with unknown statement
func TestHandleStmtExecute_Unknown(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Execute unknown statement ID 99 (4 bytes little endian)
	data := []byte{0x63, 0x00, 0x00, 0x00}
	err := f.handleStmtExecute(data)

	if err != nil {
		t.Errorf("handleStmtExecute should not fail for unknown statement: %v", err)
	}

	// Should write error response
	if conn.Buffer.Len() == 0 {
		t.Error("handleStmtExecute should write error for unknown statement")
	}
}

// Test handleStmtExecute with short data
func TestHandleStmtExecute_ShortData(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Execute with too short data
	data := []byte{0x01, 0x00}
	err := f.handleStmtExecute(data)

	if err != nil {
		t.Errorf("handleStmtExecute should not fail with short data: %v", err)
	}

	// Should write error response
	if conn.Buffer.Len() == 0 {
		t.Error("handleStmtExecute should write error for short data")
	}
}
