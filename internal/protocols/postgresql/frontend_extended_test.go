package postgresql

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/GeryonProxy/geryon/internal/logger"
)

// Test helper functions from frontend.go - min

func TestMin_Extended(t *testing.T) {
	tests := []struct {
		a, b     int
		expected int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{0, 10, 0},
		{-1, 1, -1},
		{-5, -10, -10},
	}

	for _, tc := range tests {
		got := min(tc.a, tc.b)
		if got != tc.expected {
			t.Errorf("min(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.expected)
		}
	}
}

func TestToUpper(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "HELLO"},
		{"HELLO", "HELLO"},
		{"Hello", "HELLO"},
		{"hElLo", "HELLO"},
		{"123", "123"},
		{"abc123xyz", "ABC123XYZ"},
		{"", ""},
		{"ABC!@#", "ABC!@#"},
		{"mixedCASE123", "MIXEDCASE123"},
	}

	for _, tc := range tests {
		got := toUpper(tc.input)
		if got != tc.expected {
			t.Errorf("toUpper(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestHasPrefix(t *testing.T) {
	tests := []struct {
		s      string
		prefix string
		want   bool
	}{
		{"hello world", "hello", true},
		{"hello world", "world", false},
		{"hello", "hello world", false},
		{"", "hello", false},
		{"hello", "", true},
		{"", "", true},
		{"prefix", "pre", true},
		{"PREFIX", "pre", false}, // Case sensitive
	}

	for _, tc := range tests {
		got := hasPrefix(tc.s, tc.prefix)
		if got != tc.want {
			t.Errorf("hasPrefix(%q, %q) = %v, want %v", tc.s, tc.prefix, got, tc.want)
		}
	}
}

// Test FrontendState constants
func TestFrontendState_Constants(t *testing.T) {
	// Ensure values are as expected
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

// Test PreparedStatement struct (using correct field names)
func TestPreparedStatement_Struct(t *testing.T) {
	ps := &PreparedStatement{
		Name:           "stmt1",
		Query:          "SELECT $1",
		ParameterTypes: []int32{23},
	}

	if ps.Name != "stmt1" {
		t.Errorf("Name = %q, want stmt1", ps.Name)
	}
	if ps.Query != "SELECT $1" {
		t.Errorf("Query = %q, want SELECT $1", ps.Query)
	}
	if len(ps.ParameterTypes) != 1 || ps.ParameterTypes[0] != 23 {
		t.Errorf("ParameterTypes = %v, want [23]", ps.ParameterTypes)
	}
}

// Test Portal struct (using correct field names)
func TestPortal_Struct(t *testing.T) {
	portal := &Portal{
		Name:             "portal1",
		Statement:        &PreparedStatement{Name: "stmt1"},
		ParameterFormats: []int16{0},
		Parameters:       [][]byte{[]byte("value")},
		ResultFormats:    []int16{0},
	}

	if portal.Name != "portal1" {
		t.Errorf("Name = %q, want portal1", portal.Name)
	}
	if portal.Statement == nil || portal.Statement.Name != "stmt1" {
		t.Error("Statement should be set correctly")
	}
	if len(portal.ParameterFormats) != 1 {
		t.Errorf("ParameterFormats len = %d, want 1", len(portal.ParameterFormats))
	}
	if len(portal.Parameters) != 1 {
		t.Errorf("Parameters len = %d, want 1", len(portal.Parameters))
	}
	if len(portal.ResultFormats) != 1 {
		t.Errorf("ResultFormats len = %d, want 1", len(portal.ResultFormats))
	}
}

// Test getTxStatus function
func TestFrontend_getTxStatus(t *testing.T) {
	f := &Frontend{
		state: StateIdle,
	}

	// When state is StateIdle, should return TxIdle
	if status := f.getTxStatus(); status != TxIdle {
		t.Errorf("getTxStatus() with StateIdle = %c, want %c", status, TxIdle)
	}

	// When state is StateActive, should return TxActive
	f.state = StateActive
	if status := f.getTxStatus(); status != TxActive {
		t.Errorf("getTxStatus() with StateActive = %c, want %c", status, TxActive)
	}

	// When state is StateAuthentication, should return TxIdle (not active)
	f.state = StateAuthentication
	if status := f.getTxStatus(); status != TxIdle {
		t.Errorf("getTxStatus() with StateAuthentication = %c, want %c", status, TxIdle)
	}
}

// Test cleanup function
func TestFrontend_cleanup(t *testing.T) {
	log, _ := logger.New("error", "json")

	// Create a pipe connection for conn
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	f := &Frontend{
		state:     StateIdle,
		closed:    atomic.Bool{},
		backendMu: sync.Mutex{},
		conn:      server,
		log:       log,
	}

	// Test cleanup without backend connection
	f.cleanup()

	if f.state != StateClosed {
		t.Errorf("state after cleanup = %d, want StateClosed", f.state)
	}
	if !f.closed.Load() {
		t.Error("closed should be true after cleanup")
	}
}

// Test cleanup with backend connection
func TestFrontend_cleanup_WithBackend(t *testing.T) {
	log, _ := logger.New("error", "json")

	// Create pipes to simulate connections
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	backendServer, backendClient := net.Pipe()
	defer backendServer.Close()
	defer backendClient.Close()

	f := &Frontend{
		state:        StateIdle,
		closed:       atomic.Bool{},
		backendMu:    sync.Mutex{},
		conn:         client,
		backendConn:  backendClient,
		log:          log,
	}

	// Test cleanup with backend connection
	f.cleanup()

	if f.state != StateClosed {
		t.Errorf("state after cleanup = %d, want StateClosed", f.state)
	}
	if !f.closed.Load() {
		t.Error("closed should be true after cleanup")
	}
}

// Test NewFrontend from extended
func TestNewFrontend_Extended(t *testing.T) {
	log, _ := logger.New("error", "json")

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	f := NewFrontend(client, nil, nil, log)

	if f == nil {
		t.Fatal("NewFrontend returned nil")
	}
	if f.conn != client {
		t.Error("conn should be set")
	}
	if f.state != StateStartup {
		t.Errorf("state = %d, want StateStartup", f.state)
	}
	if f.preparedStmts == nil {
		t.Error("preparedStmts should be initialized")
	}
	if f.portals == nil {
		t.Error("portals should be initialized")
	}
}

// Test handleSSLRequest
func TestFrontend_handleSSLRequest(t *testing.T) {
	log, _ := logger.New("error", "json")

	server, client := net.Pipe()
	defer server.Close()

	f := &Frontend{
		conn: client,
		log:  log,
	}

	// Run in goroutine since handleSSLRequest writes to connection
	go func() {
		f.handleSSLRequest()
	}()

	// Read response
	buf := make([]byte, 1)
	server.Read(buf)

	if buf[0] != 'N' {
		t.Errorf("SSL response = %q, want 'N'", buf[0])
	}

	client.Close()
}

// Test getTxStatus
func TestFrontend_getTxStatus_AllStates(t *testing.T) {
	log, _ := logger.New("error", "json")

	tests := []struct {
		state FrontendState
		want  byte
	}{
		{StateStartup, TxIdle},
		{StateSSLHandshake, TxIdle},
		{StateAuthentication, TxIdle},
		{StateIdle, TxIdle},
		{StateActive, TxActive},
		{StateCopy, TxIdle},
		{StateClosed, TxIdle},
	}

	for _, tc := range tests {
		f := &Frontend{
			state: tc.state,
			log:   log,
		}
		got := f.getTxStatus()
		if got != tc.want {
			t.Errorf("getTxStatus() with state %d = %c, want %c", tc.state, got, tc.want)
		}
	}
}

// Test handleSync
func TestFrontend_handleSync(t *testing.T) {
	log, _ := logger.New("error", "json")

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	f := &Frontend{
		state:  StateIdle,
		pgConn: NewConnection(client),
		log:    log,
	}

	// Run in goroutine
	go func() {
		f.handleSync()
	}()

	// Read response
	buf := make([]byte, 6)
	server.Read(buf)

	// Should get 'Z' message (ReadyForQuery)
	if buf[0] != 'Z' {
		t.Errorf("response type = %q, want 'Z'", buf[0])
	}
}

// Test handleClose with statement
func TestFrontend_handleClose_Statement(t *testing.T) {
	log, _ := logger.New("error", "json")

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	f := &Frontend{
		state:         StateIdle,
		pgConn:        NewConnection(client),
		log:           log,
		preparedStmts: make(map[string]*PreparedStatement),
	}

	// Add a prepared statement
	f.preparedStmts["stmt1"] = &PreparedStatement{Name: "stmt1"}

	// Close the statement
	data := []byte{'S', 's', 't', 'm', 't', '1', 0}

	// Run in goroutine
	go func() {
		f.handleClose(data)
	}()

	// Read response
	buf := make([]byte, 6)
	server.Read(buf)

	// Should get '3' message (CloseComplete)
	if buf[0] != '3' {
		t.Errorf("response type = %q, want '3'", buf[0])
	}

	// Verify statement was deleted
	if _, ok := f.preparedStmts["stmt1"]; ok {
		t.Error("stmt1 should be deleted")
	}
}

// Test handleClose with portal
func TestFrontend_handleClose_Portal(t *testing.T) {
	log, _ := logger.New("error", "json")

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	f := &Frontend{
		state:   StateIdle,
		pgConn:  NewConnection(client),
		log:     log,
		portals: make(map[string]*Portal),
	}

	// Add a portal
	f.portals["portal1"] = &Portal{Name: "portal1"}

	// Close the portal
	data := []byte{'P', 'p', 'o', 'r', 't', 'a', 'l', '1', 0}

	// Run in goroutine
	go func() {
		f.handleClose(data)
	}()

	// Read response
	buf := make([]byte, 6)
	server.Read(buf)

	// Should get '3' message (CloseComplete)
	if buf[0] != '3' {
		t.Errorf("response type = %q, want '3'", buf[0])
	}

	// Verify portal was deleted
	if _, ok := f.portals["portal1"]; ok {
		t.Error("portal1 should be deleted")
	}
}

// Test handleClose with short data
func TestFrontend_handleClose_ShortData(t *testing.T) {
	log, _ := logger.New("error", "json")

	f := &Frontend{
		state:  StateIdle,
		log:    log,
	}

	// Close with short data should return nil
	err := f.handleClose([]byte{0})
	if err != nil {
		t.Errorf("handleClose with short data error = %v", err)
	}
}

// Test handleDescribe with statement
func TestFrontend_handleDescribe_Statement(t *testing.T) {
	log, _ := logger.New("error", "json")

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	f := &Frontend{
		state:         StateIdle,
		pgConn:        NewConnection(client),
		log:           log,
		preparedStmts: make(map[string]*PreparedStatement),
	}

	// Add a prepared statement
	f.preparedStmts["stmt1"] = &PreparedStatement{Name: "stmt1"}

	// Describe the statement
	data := []byte{'S', 's', 't', 'm', 't', '1', 0}

	// Run in goroutine
	go func() {
		f.handleDescribe(data)
	}()

	// Read response
	buf := make([]byte, 6)
	server.Read(buf)

	// Should get 'n' message (NoData) or 't' message (ParameterDescription)
	if buf[0] != 'n' && buf[0] != 't' {
		t.Errorf("response type = %q, want 'n' or 't'", buf[0])
	}
}

// Test handleDescribe with non-existent statement
func TestFrontend_handleDescribe_NonExistentStatement(t *testing.T) {
	log, _ := logger.New("error", "json")

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	f := &Frontend{
		state:         StateIdle,
		pgConn:        NewConnection(client),
		log:           log,
		preparedStmts: make(map[string]*PreparedStatement),
	}

	// Describe a non-existent statement
	data := []byte{'S', 'n', 'o', 'n', 'e', 'x', 'i', 's', 't', 0}

	// Run in goroutine
	go func() {
		f.handleDescribe(data)
	}()

	// Read response
	buf := make([]byte, 100)
	n, _ := server.Read(buf)

	// Should get 'E' message (ErrorResponse)
	if n > 0 && buf[0] != 'E' {
		t.Errorf("response type = %q, want 'E'", buf[0])
	}
}

// Test handleParse
func TestFrontend_handleParse(t *testing.T) {
	log, _ := logger.New("error", "json")

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	f := &Frontend{
		state:         StateIdle,
		pgConn:        NewConnection(client),
		log:           log,
		preparedStmts: make(map[string]*PreparedStatement),
	}

	// Create parse message data
	data := []byte("stmt1\x00SELECT 1\x00\x00\x00")

	// Run in goroutine
	go func() {
		f.handleParse(data)
	}()

	// Read response
	buf := make([]byte, 6)
	server.Read(buf)

	// Should get '1' message (ParseComplete)
	if buf[0] != '1' {
		t.Errorf("response type = %q, want '1'", buf[0])
	}

	// Verify statement was stored
	if _, ok := f.preparedStmts["stmt1"]; !ok {
		t.Error("stmt1 should be stored")
	}
}

// Test handleBind
func TestFrontend_handleBind(t *testing.T) {
	log, _ := logger.New("error", "json")

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	f := &Frontend{
		state:         StateIdle,
		pgConn:        NewConnection(client),
		log:           log,
		preparedStmts: make(map[string]*PreparedStatement),
		portals:       make(map[string]*Portal),
	}

	// Add a prepared statement
	f.preparedStmts["stmt1"] = &PreparedStatement{Name: "stmt1", Query: "SELECT 1"}

	// Create bind message data with proper format
	// portal\0 stmt\0 numFormats(0) numParams(0) numResultFormats(0)
	buf := make([]byte, 0)
	buf = append(buf, []byte("portal1\x00")...)
	buf = append(buf, []byte("stmt1\x00")...)
	buf = append(buf, 0x00, 0x00) // 0 formats
	buf = append(buf, 0x00, 0x00) // 0 parameters
	buf = append(buf, 0x00, 0x00) // 0 result formats

	// Run in goroutine
	go func() {
		f.handleBind(buf)
	}()

	// Read response
	buf2 := make([]byte, 6)
	server.Read(buf2)

	// Should get '2' message (BindComplete)
	if buf2[0] != '2' {
		t.Errorf("response type = %q, want '2'", buf2[0])
	}

	// Verify portal was stored
	if _, ok := f.portals["portal1"]; !ok {
		t.Error("portal1 should be stored")
	}
}

// Test handleBind with non-existent statement
func TestFrontend_handleBind_NonExistentStatement(t *testing.T) {
	log, _ := logger.New("error", "json")

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	f := &Frontend{
		state:         StateIdle,
		pgConn:        NewConnection(client),
		log:           log,
		preparedStmts: make(map[string]*PreparedStatement),
		portals:       make(map[string]*Portal),
	}

	// Create bind message data for non-existent statement with proper format
	buf := make([]byte, 0)
	buf = append(buf, []byte("portal1\x00")...)
	buf = append(buf, []byte("nonexistent\x00")...)
	buf = append(buf, 0x00, 0x00) // 0 formats
	buf = append(buf, 0x00, 0x00) // 0 parameters
	buf = append(buf, 0x00, 0x00) // 0 result formats

	// Run in goroutine
	go func() {
		f.handleBind(buf)
	}()

	// Read response
	buf2 := make([]byte, 100)
	n, _ := server.Read(buf2)

	// Should get 'E' message (ErrorResponse)
	if n > 0 && buf2[0] != 'E' {
		t.Errorf("response type = %q, want 'E'", buf2[0])
	}
}

// Test handleExecute with non-existent portal
func TestFrontend_handleExecute_NonExistentPortal(t *testing.T) {
	log, _ := logger.New("error", "json")

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	f := &Frontend{
		state:   StateIdle,
		pgConn:  NewConnection(client),
		log:     log,
		portals: make(map[string]*Portal),
	}

	// Create execute message data for non-existent portal with proper format
	buf := make([]byte, 0)
	buf = append(buf, []byte("nonexistent\x00")...)
	buf = append(buf, 0x00, 0x00, 0x00, 0x00) // max rows = 0

	// Run in goroutine
	go func() {
		f.handleExecute(buf)
	}()

	// Read response
	buf2 := make([]byte, 100)
	n, _ := server.Read(buf2)

	// Should get 'E' message (ErrorResponse)
	if n > 0 && buf2[0] != 'E' {
		t.Errorf("response type = %q, want 'E'", buf2[0])
	}
}
