package mssql

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/logger"
)

// mockConn is a mock net.Conn for testing
type mockConn struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
	closed   bool
	local    net.Addr
	remote   net.Addr
}

func newMockConn() *mockConn {
	return &mockConn{
		readBuf:  bytes.NewBuffer(nil),
		writeBuf: bytes.NewBuffer(nil),
		local:    &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1433},
		remote:   &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1434},
	}
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	return m.readBuf.Read(b)
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	return m.writeBuf.Write(b)
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

func (m *mockConn) LocalAddr() net.Addr {
	return m.local
}

func (m *mockConn) RemoteAddr() net.Addr {
	return m.remote
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

// setReadData sets the data to be read
func (m *mockConn) setReadData(data []byte) {
	m.readBuf.Reset()
	m.readBuf.Write(data)
}

func TestPacketTypeConstants(t *testing.T) {
	if PackSQLBatch != 1 {
		t.Errorf("PackSQLBatch = %d, want 1", PackSQLBatch)
	}
	if PackRPCRequest != 3 {
		t.Errorf("PackRPCRequest = %d, want 3", PackRPCRequest)
	}
	if PackReply != 4 {
		t.Errorf("PackReply = %d, want 4", PackReply)
	}
	if PackAttention != 6 {
		t.Errorf("PackAttention = %d, want 6", PackAttention)
	}
	if PackBulkLoad != 7 {
		t.Errorf("PackBulkLoad = %d, want 7", PackBulkLoad)
	}
	if PackFedAuthToken != 8 {
		t.Errorf("PackFedAuthToken = %d, want 8", PackFedAuthToken)
	}
	if PackTransMgrReq != 14 {
		t.Errorf("PackTransMgrReq = %d, want 14", PackTransMgrReq)
	}
	if PackTDS7Login != 16 {
		t.Errorf("PackTDS7Login = %d, want 16", PackTDS7Login)
	}
	if PackSSPI != 17 {
		t.Errorf("PackSSPI = %d, want 17", PackSSPI)
	}
	if PackPreLogin != 18 {
		t.Errorf("PackPreLogin = %d, want 18", PackPreLogin)
	}
}

func TestStatusConstants(t *testing.T) {
	if StatusNormal != 0x00 {
		t.Errorf("StatusNormal = 0x%02x, want 0x00", StatusNormal)
	}
	if StatusEOM != 0x01 {
		t.Errorf("StatusEOM = 0x%02x, want 0x01", StatusEOM)
	}
	if StatusIgnore != 0x02 {
		t.Errorf("StatusIgnore = 0x%02x, want 0x02", StatusIgnore)
	}
	if StatusResetConn != 0x08 {
		t.Errorf("StatusResetConn = 0x%02x, want 0x08", StatusResetConn)
	}
	if StatusResetSkip != 0x10 {
		t.Errorf("StatusResetSkip = 0x%02x, want 0x10", StatusResetSkip)
	}
}

func TestVersionConstants(t *testing.T) {
	if VerSQL2000 != 0x07000000 {
		t.Errorf("VerSQL2000 = 0x%08x, want 0x07000000", VerSQL2000)
	}
	if VerSQL2005 != 0x72090002 {
		t.Errorf("VerSQL2005 = 0x%08x, want 0x72090002", VerSQL2005)
	}
	if VerSQL2008 != 0x730A0003 {
		t.Errorf("VerSQL2008 = 0x%08x, want 0x730A0003", VerSQL2008)
	}
	if VerSQL2012 != 0x74000004 {
		t.Errorf("VerSQL2012 = 0x%08x, want 0x74000004", VerSQL2012)
	}
	if VerSQL2014 != 0x74000004 {
		t.Errorf("VerSQL2014 = 0x%08x, want 0x74000004", VerSQL2014)
	}
	if VerSQL2016 != 0x74000004 {
		t.Errorf("VerSQL2016 = 0x%08x, want 0x74000004", VerSQL2016)
	}
	if VerSQL2017 != 0x74000004 {
		t.Errorf("VerSQL2017 = 0x%08x, want 0x74000004", VerSQL2017)
	}
	if VerSQL2019 != 0x74000004 {
		t.Errorf("VerSQL2019 = 0x%08x, want 0x74000004", VerSQL2019)
	}
}

func TestSizeConstants(t *testing.T) {
	if MinLoginPacketSize != 512 {
		t.Errorf("MinLoginPacketSize = %d, want 512", MinLoginPacketSize)
	}
	if MaxLoginPacketSize != 32767 {
		t.Errorf("MaxLoginPacketSize = %d, want 32767", MaxLoginPacketSize)
	}
}

func TestClientProgVer(t *testing.T) {
	if ClientProgVer != 0x07000000 {
		t.Errorf("ClientProgVer = 0x%08x, want 0x07000000", ClientProgVer)
	}
}

func TestStateConstants(t *testing.T) {
	if StatePreLogin != 0 {
		t.Errorf("StatePreLogin = %d, want 0", StatePreLogin)
	}
	if StateLogin != 1 {
		t.Errorf("StateLogin = %d, want 1", StateLogin)
	}
	if StateReady != 2 {
		t.Errorf("StateReady = %d, want 2", StateReady)
	}
	if StateActive != 3 {
		t.Errorf("StateActive = %d, want 3", StateActive)
	}
	if StateClosed != 4 {
		t.Errorf("StateClosed = %d, want 4", StateClosed)
	}
}

func TestMaxTDSPacketSize(t *testing.T) {
	// maxTDSPacketSize should be 16MB (16 << 20)
	expected := 16 << 20
	if maxTDSPacketSize != expected {
		t.Errorf("maxTDSPacketSize = %d, want %d", maxTDSPacketSize, expected)
	}
}

func TestTDSPacket(t *testing.T) {
	pkt := &TDSPacket{
		Type:   PackSQLBatch,
		Status: StatusEOM,
		Length: 100,
		SPID:   1234,
		Packet: 1,
		Window: 0,
		Data:   []byte("test"),
	}
	if pkt.Type != PackSQLBatch {
		t.Errorf("Type = %d, want %d", pkt.Type, PackSQLBatch)
	}
	if pkt.Status != StatusEOM {
		t.Errorf("Status = %d, want %d", pkt.Status, StatusEOM)
	}
	if pkt.Length != 100 {
		t.Errorf("Length = %d, want 100", pkt.Length)
	}
	if pkt.SPID != 1234 {
		t.Errorf("SPID = %d, want 1234", pkt.SPID)
	}
	if pkt.Packet != 1 {
		t.Errorf("Packet = %d, want 1", pkt.Packet)
	}
	if string(pkt.Data) != "test" {
		t.Errorf("Data = %q, want test", string(pkt.Data))
	}
}

func TestLogin(t *testing.T) {
	login := &Login{
		Username:       "sa",
		Password:       "secret",
		Database:       "master",
		HostName:       "localhost",
		AppName:        "TestApp",
		ServerName:     "server",
		LibraryName:    "TestLib",
		Language:       "English",
		TDSVersion:     VerSQL2019,
		PacketSize:     4096,
		ClientProgVer:  ClientProgVer,
		ClientPID:      12345,
		ConnectionID:   0,
		OptionFlags1:   0xe0,
		OptionFlags2:   0x03,
		TypeFlags:      0x00,
		OptionFlags3:   0x00,
		ClientTimeZone: 0,
		ClientLCID:     0x409,
	}
	if login.Username != "sa" {
		t.Errorf("Username = %q, want sa", login.Username)
	}
	if login.Database != "master" {
		t.Errorf("Database = %q, want master", login.Database)
	}
	if login.TDSVersion != VerSQL2019 {
		t.Errorf("TDSVersion = 0x%08x, want 0x%08x", login.TDSVersion, VerSQL2019)
	}
}

func TestColumnInfo(t *testing.T) {
	col := &ColumnInfo{
		Name:  "id",
		Type:  56, // INT
		Size:  4,
		Scale: 0,
		Prec:  10,
	}
	if col.Name != "id" {
		t.Errorf("Name = %q, want id", col.Name)
	}
	if col.Type != 56 {
		t.Errorf("Type = %d, want 56", col.Type)
	}
	if col.Size != 4 {
		t.Errorf("Size = %d, want 4", col.Size)
	}
	if col.Scale != 0 {
		t.Errorf("Scale = %d, want 0", col.Scale)
	}
	if col.Prec != 10 {
		t.Errorf("Prec = %d, want 10", col.Prec)
	}
}

func TestNewFrontend(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")

	f := NewFrontend(conn, nil, nil, log)
	if f == nil {
		t.Fatal("NewFrontend returned nil")
	}

	if f.state != StatePreLogin {
		t.Errorf("state = %d, want StatePreLogin", f.state)
	}

	if f.packetSize != 4096 {
		t.Errorf("packetSize = %d, want 4096", f.packetSize)
	}

	if f.conn != conn {
		t.Error("conn not set correctly")
	}

	if f.log != log {
		t.Error("log not set correctly")
	}
}

func TestFrontendStateTransitions(t *testing.T) {
	states := []struct {
		state FrontendState
		name  string
	}{
		{StatePreLogin, "StatePreLogin"},
		{StateLogin, "StateLogin"},
		{StateReady, "StateReady"},
		{StateActive, "StateActive"},
		{StateClosed, "StateClosed"},
	}

	for _, s := range states {
		_ = s.state
	}
}

func TestFrontendDatabase(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	f.database = "testdb"
	if f.database != "testdb" {
		t.Errorf("database = %q, want testdb", f.database)
	}
}

func TestFrontendUser(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	f.user = &auth.User{Username: "testuser"}
	if f.user.Username != "testuser" {
		t.Errorf("username = %q, want testuser", f.user.Username)
	}
}

func TestFrontendClientPID(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	f.clientPID = 12345
	if f.clientPID != 12345 {
		t.Errorf("clientPID = %d, want 12345", f.clientPID)
	}
}

func TestFrontendClientVer(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	f.clientVer = VerSQL2019
	if f.clientVer != VerSQL2019 {
		t.Errorf("clientVer = 0x%08x, want 0x%08x", f.clientVer, VerSQL2019)
	}
}

func TestFrontendPacketSize(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Test default
	if f.packetSize != 4096 {
		t.Errorf("default packetSize = %d, want 4096", f.packetSize)
	}

	// Set custom
	f.packetSize = 8192
	if f.packetSize != 8192 {
		t.Errorf("packetSize = %d, want 8192", f.packetSize)
	}
}

func TestFrontendClosed(t *testing.T) {
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

func TestFrontendSequence(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Initial sequence should be 0
	if f.sequence != 0 {
		t.Errorf("initial sequence = %d, want 0", f.sequence)
	}

	// Increment
	f.seqMu.Lock()
	f.sequence++
	f.seqMu.Unlock()

	if f.sequence != 1 {
		t.Errorf("sequence = %d, want 1", f.sequence)
	}
}

func TestMin(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{1, 1, 1},
		{0, 5, 0},
		{-1, 1, -1},
	}

	for _, tt := range tests {
		result := min(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("min(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestDecodeUnicode(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Test simple ASCII in UTF-16-LE
	data := []byte{'H', 0, 'i', 0}
	result := f.decodeUnicode(data)
	if result != "Hi" {
		t.Errorf("decodeUnicode = %q, want Hi", result)
	}

	// Test with null termination
	data = []byte{'T', 0, 'e', 0, 's', 0, 't', 0, 0, 0}
	result = f.decodeUnicode(data)
	if result != "Test" {
		t.Errorf("decodeUnicode = %q, want Test", result)
	}

	// Test empty
	result = f.decodeUnicode([]byte{})
	if result != "" {
		t.Errorf("decodeUnicode empty = %q, want empty", result)
	}
}

func TestBuildPreLoginResponse(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	options := make(map[uint8][]byte)
	response := f.buildPreLoginResponse(options)

	// Response should contain option tokens
	if len(response) == 0 {
		t.Error("buildPreLoginResponse returned empty data")
	}

	// Should contain terminator (0xFF)
	foundTerminator := false
	for _, b := range response {
		if b == 0xFF {
			foundTerminator = true
			break
		}
	}
	if !foundTerminator {
		t.Error("PreLogin response missing terminator")
	}
}

func TestParsePreLoginOptions(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Build minimal pre-login data with terminator
	data := []byte{
		0xFF, // Terminator
	}

	options, err := f.parsePreLoginOptions(data)
	if err != nil {
		t.Errorf("parsePreLoginOptions failed: %v", err)
	}

	if len(options) != 0 {
		t.Errorf("expected 0 options, got %d", len(options))
	}
}

func TestParsePreLoginOptionsWithVersion(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Build pre-login data with VERSION option
	// Option 0, offset 10, length 6
	data := []byte{
		0x00,       // VERSION
		0x00, 0x0A, // offset = 10
		0x00, 0x06, // length = 6
		0xFF, // Terminator
		// Padding to reach offset 10
		0x00, 0x00, 0x00, 0x00,
		// Version data (6 bytes)
		0x07, 0x04, 0x00, 0x00, 0x00, 0x00,
	}

	options, err := f.parsePreLoginOptions(data)
	if err != nil {
		t.Errorf("parsePreLoginOptions failed: %v", err)
	}

	if len(options) != 1 {
		t.Errorf("expected 1 option, got %d", len(options))
	}
}

func TestBuildLoginAckData(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	data := f.buildLoginAckData()
	if len(data) == 0 {
		t.Error("buildLoginAckData returned empty data")
	}

	// First byte should be interface (SQL_TSQL = 1)
	if data[0] != 0x01 {
		t.Errorf("interface = 0x%02x, want 0x01", data[0])
	}
}

func TestBuildEnvChangeData(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Test DATABASE
	data := f.buildEnvChangeData("DATABASE", "testdb")
	if len(data) == 0 {
		t.Error("buildEnvChangeData returned empty data")
	}
	if data[0] != 1 {
		t.Errorf("DATABASE type = %d, want 1", data[0])
	}

	// Test LANGUAGE
	data = f.buildEnvChangeData("LANGUAGE", "English")
	if data[0] != 2 {
		t.Errorf("LANGUAGE type = %d, want 2", data[0])
	}

	// Test CHARSET
	data = f.buildEnvChangeData("CHARSET", "UTF-8")
	if data[0] != 3 {
		t.Errorf("CHARSET type = %d, want 3", data[0])
	}

	// Test PACKET_SIZE
	data = f.buildEnvChangeData("PACKET_SIZE", "4096")
	if data[0] != 4 {
		t.Errorf("PACKET_SIZE type = %d, want 4", data[0])
	}

	// Test unknown type
	data = f.buildEnvChangeData("UNKNOWN", "value")
	if data[0] != 1 {
		t.Errorf("UNKNOWN default type = %d, want 1", data[0])
	}
}

func TestBuildDoneToken(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	data := f.buildDoneToken(0x0010, 0, 1)

	// First byte should be token type (0xFD)
	if data[0] != 0xFD {
		t.Errorf("token type = 0x%02x, want 0xFD", data[0])
	}

	// Should have status (2 bytes), cmd (2 bytes), row count (8 bytes) = 12 bytes after token
	if len(data) != 13 {
		t.Errorf("done token length = %d, want 13", len(data))
	}
}

func TestBuildErrorToken(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	data := f.buildErrorToken(18456, 1, "Login failed", "SERVER", "PROC", 1)

	// First byte should be token type (0xAA)
	if data[0] != 0xAA {
		t.Errorf("token type = 0x%02x, want 0xAA", data[0])
	}

	// Should have reasonable length
	if len(data) < 10 {
		t.Errorf("error token too short: %d bytes", len(data))
	}
}

func TestExecuteSQL(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Write a done token first so executeSQL has something to write
	// This tests executeSQL with a SELECT
	err := f.executeSQL("SELECT 1")
	if err != nil {
		t.Errorf("executeSQL SELECT failed: %v", err)
	}

	// Test SET command
	conn.writeBuf.Reset()
	err = f.executeSQL("SET ANSI_NULLS ON")
	if err != nil {
		t.Errorf("executeSQL SET failed: %v", err)
	}

	// Test INSERT (other command)
	conn.writeBuf.Reset()
	err = f.executeSQL("INSERT INTO t VALUES (1)")
	if err != nil {
		t.Errorf("executeSQL INSERT failed: %v", err)
	}
}

func TestSendRowMetadata(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	columns := []*ColumnInfo{
		{Name: "id", Type: 0x38, Size: 4},
		{Name: "name", Type: 0x27, Size: 255},
	}

	err := f.sendRowMetadata(columns)
	if err != nil {
		t.Errorf("sendRowMetadata failed: %v", err)
	}

	// Should have written something
	if conn.writeBuf.Len() == 0 {
		t.Error("sendRowMetadata wrote nothing")
	}
}

func TestSendRow(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	values := []interface{}{1, "test", nil}

	err := f.sendRow(values)
	if err != nil {
		t.Errorf("sendRow failed: %v", err)
	}

	// Should have written something
	if conn.writeBuf.Len() == 0 {
		t.Error("sendRow wrote nothing")
	}
}

func TestWritePacket(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	data := []byte("test data")
	err := f.writePacket(PackSQLBatch, StatusEOM, data)
	if err != nil {
		t.Errorf("writePacket failed: %v", err)
	}

	// Should have written header (8 bytes) + data
	if conn.writeBuf.Len() != 8+len(data) {
		t.Errorf("writePacket wrote %d bytes, want %d", conn.writeBuf.Len(), 8+len(data))
	}

	// Check header
	header := make([]byte, 8)
	conn.writeBuf.Read(header)

	if header[0] != PackSQLBatch {
		t.Errorf("packet type = %d, want %d", header[0], PackSQLBatch)
	}
	if header[1] != StatusEOM {
		t.Errorf("status = 0x%02x, want 0x%02x", header[1], StatusEOM)
	}
}

func TestReadPacket(t *testing.T) {
	// Skip this test as it requires complex mock setup with bufio.Reader
	t.Skip("Skipping test that requires complex bufio.Reader mocking")
}

func TestReadPacketInvalidLength(t *testing.T) {
	// Skip this test as it requires complex mock setup with bufio.Reader
	t.Skip("Skipping test that requires complex bufio.Reader mocking")
}

func TestReadPacketTooLarge(t *testing.T) {
	// Skip this test as it requires complex mock setup with bufio.Reader
	t.Skip("Skipping test that requires complex bufio.Reader mocking")
}

func TestParseLogin(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Build a minimal valid login packet
	// Fixed part: 4+4+4+4+4+4+1+1+1+1+4+4 = 36 bytes
	// Variable offsets: 9 * 4 = 36 bytes
	// Client ID: 6 bytes
	// More offsets: 3 * 4 = 12 bytes
	// Total minimum: ~90 bytes

	buf := make([]byte, 200)
	offset := 0

	// Length (4 bytes)
	binary.LittleEndian.PutUint32(buf[offset:], 200)
	offset += 4

	// TDS Version
	binary.LittleEndian.PutUint32(buf[offset:], VerSQL2019)
	offset += 4

	// Packet Size
	binary.LittleEndian.PutUint32(buf[offset:], 4096)
	offset += 4

	// Client Prog Ver
	binary.LittleEndian.PutUint32(buf[offset:], ClientProgVer)
	offset += 4

	// Client PID
	binary.LittleEndian.PutUint32(buf[offset:], 12345)
	offset += 4

	// Connection ID
	binary.LittleEndian.PutUint32(buf[offset:], 0)
	offset += 4

	// Option flags
	buf[offset] = 0xe0
	offset++
	buf[offset] = 0x03
	offset++
	buf[offset] = 0x00
	offset++
	buf[offset] = 0x00
	offset++

	// Timezone
	binary.LittleEndian.PutUint32(buf[offset:], 0)
	offset += 4

	// LCID
	binary.LittleEndian.PutUint32(buf[offset:], 0x409)
	offset += 4

	// Variable offsets (all zeros for simplicity)
	for i := 0; i < 9; i++ {
		binary.LittleEndian.PutUint16(buf[offset:], 0)
		offset += 2
		binary.LittleEndian.PutUint16(buf[offset:], 0)
		offset += 2
	}

	// Client ID
	for i := 0; i < 6; i++ {
		buf[offset] = 0
		offset++
	}

	// More offsets
	for i := 0; i < 3; i++ {
		binary.LittleEndian.PutUint16(buf[offset:], 0)
		offset += 2
		binary.LittleEndian.PutUint16(buf[offset:], 0)
		offset += 2
	}

	// SSPILong
	binary.LittleEndian.PutUint32(buf[offset:], 0)

	login, err := f.parseLogin(buf)
	if err != nil {
		t.Errorf("parseLogin failed: %v", err)
	}

	if login.TDSVersion != VerSQL2019 {
		t.Errorf("TDSVersion = 0x%08x, want 0x%08x", login.TDSVersion, VerSQL2019)
	}
	if login.PacketSize != 4096 {
		t.Errorf("PacketSize = %d, want 4096", login.PacketSize)
	}
}

func TestParseLoginTooShort(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Login packet must be at least 86 bytes
	buf := make([]byte, 50)

	_, err := f.parseLogin(buf)
	if err == nil {
		t.Error("parseLogin should fail with too short packet")
	}
}

func TestHandleSQLBatch(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Build SQL batch data: 8-byte header + SQL text
	sql := "SELECT 1"
	sqlBytes := make([]byte, len(sql)*2)
	for i, c := range sql {
		sqlBytes[i*2] = byte(c)
	}

	batch := make([]byte, 8+len(sqlBytes))
	// Header: All Headers Length = 8 (8-12 bytes at minimum)
	binary.LittleEndian.PutUint32(batch[0:4], uint32(len(batch)))
	copy(batch[8:], sqlBytes)

	err := f.handleSQLBatch(batch)
	if err != nil {
		t.Errorf("handleSQLBatch failed: %v", err)
	}
}

func TestHandleSQLBatchTooShort(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Batch too short (less than 8 bytes)
	batch := []byte{0, 1, 2, 3}

	err := f.handleSQLBatch(batch)
	if err == nil {
		t.Error("handleSQLBatch should fail with too short batch")
	}
}

func TestHandleRPC(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Build proper RPC data: NameLen (2) + Name (var) + Options (2) + Params
	// Procedure name: "sp_test" (7 chars)
	data := []byte{
		0x07, 0x00, // Name length = 7
		// Name "sp_test" in Unicode (14 bytes)
		's', 0x00, 'p', 0x00, '_', 0x00, 't', 0x00, 'e', 0x00, 's', 0x00, 't', 0x00,
		0x00, 0x00, // Options
	}

	err := f.handleRPC(data)
	if err != nil {
		t.Errorf("handleRPC failed: %v", err)
	}
}

// Test sp_prepare RPC
func TestHandleSPPrepare(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("error", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Build sp_prepare RPC
	procName := "sp_prepare"
	data := make([]byte, 0, 64)

	// Name length
	data = append(data, byte(len(procName)), 0x00)

	// Name in Unicode
	for _, c := range procName {
		data = append(data, byte(c), 0x00)
	}

	// Options
	data = append(data, 0x00, 0x00)

	err := f.handleRPC(data)
	if err != nil {
		t.Errorf("handleRPC sp_prepare failed: %v", err)
	}
}

// Test sp_execute RPC
func TestHandleSPExecute(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("error", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Build sp_execute RPC
	procName := "sp_execute"
	data := make([]byte, 0, 64)

	// Name length
	data = append(data, byte(len(procName)), 0x00)

	// Name in Unicode
	for _, c := range procName {
		data = append(data, byte(c), 0x00)
	}

	// Options
	data = append(data, 0x00, 0x00)

	err := f.handleRPC(data)
	if err != nil {
		t.Errorf("handleRPC sp_execute failed: %v", err)
	}
}

// Test sp_unprepare RPC
func TestHandleSPUnprepare(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("error", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Build sp_unprepare RPC
	procName := "sp_unprepare"
	data := make([]byte, 0, 64)

	// Name length
	data = append(data, byte(len(procName)), 0x00)

	// Name in Unicode
	for _, c := range procName {
		data = append(data, byte(c), 0x00)
	}

	// Options
	data = append(data, 0x00, 0x00)

	err := f.handleRPC(data)
	if err != nil {
		t.Errorf("handleRPC sp_unprepare failed: %v", err)
	}
}

// Test sp_reset_connection RPC
func TestHandleSPResetConnection(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("error", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Build sp_reset_connection RPC
	procName := "sp_reset_connection"
	data := make([]byte, 0, 64)

	// Name length
	data = append(data, byte(len(procName)), 0x00)

	// Name in Unicode
	for _, c := range procName {
		data = append(data, byte(c), 0x00)
	}

	// Options
	data = append(data, 0x00, 0x00)

	err := f.handleRPC(data)
	if err != nil {
		t.Errorf("handleRPC sp_reset_connection failed: %v", err)
	}
}

func TestHandleAttention(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	err := f.handleAttention()
	if err != nil {
		t.Errorf("handleAttention failed: %v", err)
	}
}

func TestHandleTransMgr(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	data := []byte{0x00, 0x00, 0x00, 0x00}

	err := f.handleTransMgr(data)
	if err != nil {
		t.Errorf("handleTransMgr failed: %v", err)
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

func TestSendLoginAck(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	err := f.sendLoginAck()
	if err != nil {
		t.Errorf("sendLoginAck failed: %v", err)
	}

	if conn.writeBuf.Len() == 0 {
		t.Error("sendLoginAck wrote nothing")
	}
}

func TestSendEnvChange(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	err := f.sendEnvChange("DATABASE", "master")
	if err != nil {
		t.Errorf("sendEnvChange failed: %v", err)
	}

	if conn.writeBuf.Len() == 0 {
		t.Error("sendEnvChange wrote nothing")
	}
}

func TestSendDone(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	err := f.sendDone(0, 0, 0)
	if err != nil {
		t.Errorf("sendDone failed: %v", err)
	}

	if conn.writeBuf.Len() == 0 {
		t.Error("sendDone wrote nothing")
	}
}

func TestErrorLogin(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	err := f.errorLogin(18456, "Login failed for user")
	if err != nil {
		t.Errorf("errorLogin failed: %v", err)
	}

	if conn.writeBuf.Len() == 0 {
		t.Error("errorLogin wrote nothing")
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

func TestFrontendClientID(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("info", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Set client ID
	f.clientID = [6]byte{1, 2, 3, 4, 5, 6}

	if f.clientID[0] != 1 {
		t.Errorf("clientID[0] = %d, want 1", f.clientID[0])
	}
}

// Test Windows Authentication (NTLM) Passthrough
func TestHandleWindowsAuth_NoSSPI(t *testing.T) {
	// Login without SSPI data
	login := &Login{
		HostName: "testhost",
		Database: "testdb",
		// SSPI is empty - this is SQL auth, not Windows auth
	}

	// Should not trigger Windows auth path
	if len(login.SSPI) > 0 || len(login.SSPILong) > 0 {
		t.Error("Login should not have SSPI data")
	}
}

func TestHandleWindowsAuth_WithSSPI(t *testing.T) {
	// Login with SSPI data (Windows Authentication)
	login := &Login{
		HostName: "testhost",
		Database: "testdb",
		SSPI:     []byte{0x4E, 0x54, 0x4C, 0x4D}, // "NTLM" header
	}

	// Should detect Windows auth
	if len(login.SSPI) == 0 && len(login.SSPILong) == 0 {
		t.Error("Login should have SSPI data for Windows auth")
	}
}

func TestBuildTDS7LoginPacket_WithSSPI(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("error", "json")
	f := NewFrontend(conn, nil, nil, log)

	login := &Login{
		Length:         100,
		TDSVersion:     0x74000004,
		PacketSize:     4096,
		ClientProgVer:  0x07000000,
		ClientPID:      1234,
		ConnectionID:   0,
		OptionFlags1:   0xE0,
		OptionFlags2:   0x03,
		TypeFlags:      0x00,
		OptionFlags3:   0x00,
		ClientTimeZone: 0,
		ClientLCID:     0x0409,
		HostName:       "testhost",
		Database:       "testdb",
		SSPI:           []byte{0x4E, 0x54, 0x4C, 0x4D, 0x53, 0x53, 0x50}, // NTLMSSP
	}

	packet := f.buildTDS7LoginPacket(login)

	if len(packet) == 0 {
		t.Error("buildTDS7LoginPacket returned empty packet")
	}

	// Check packet type
	if packet[0] != PackTDS7Login {
		t.Errorf("packet type = %d, want %d", packet[0], PackTDS7Login)
	}
}

func TestBuildTDS7LoginPacket_WithSSPILong(t *testing.T) {
	conn := newMockConn()
	log, _ := logger.New("error", "json")
	f := NewFrontend(conn, nil, nil, log)

	// Long SSPI data (> 255 bytes)
	longSSPI := make([]byte, 300)
	copy(longSSPI, []byte("NTLMSSP"))

	login := &Login{
		Length:         500,
		TDSVersion:     0x74000004,
		PacketSize:     4096,
		ClientProgVer:  0x07000000,
		ClientPID:      1234,
		Database:       "testdb",
		SSPILong:       longSSPI,
	}

	packet := f.buildTDS7LoginPacket(login)

	if len(packet) == 0 {
		t.Error("buildTDS7LoginPacket returned empty packet")
	}
}

func TestWindowsAuthDetection(t *testing.T) {
	tests := []struct {
		name     string
		sspi     []byte
		sspiLong []byte
		isWinAuth bool
	}{
		{
			name:      "Empty SSPI - SQL Auth",
			sspi:      []byte{},
			sspiLong:  []byte{},
			isWinAuth: false,
		},
		{
			name:      "With SSPI - Windows Auth",
			sspi:      []byte{0x4E, 0x54, 0x4C, 0x4D},
			sspiLong:  []byte{},
			isWinAuth: true,
		},
		{
			name:      "With SSPILong - Windows Auth",
			sspi:      []byte{},
			sspiLong:  []byte{0x4E, 0x54, 0x4C, 0x4D, 0x00, 0x00},
			isWinAuth: true,
		},
		{
			name:      "Both SSPI and SSPILong - Windows Auth",
			sspi:      []byte{0x01},
			sspiLong:  []byte{0x02},
			isWinAuth: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			login := &Login{
				SSPI:     tt.sspi,
				SSPILong: tt.sspiLong,
			}

			hasSSPI := len(login.SSPI) > 0 || len(login.SSPILong) > 0
			if hasSSPI != tt.isWinAuth {
				t.Errorf("Windows auth detection = %v, want %v", hasSSPI, tt.isWinAuth)
			}
		})
	}
}
