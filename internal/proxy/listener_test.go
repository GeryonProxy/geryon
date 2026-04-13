package proxy

import (
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

func TestParseMemoryString(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"", 64 * 1024 * 1024},
		{"64MB", 64 * 1024 * 1024},
		{"1GB", 1024 * 1024 * 1024},
		{"512KB", 512 * 1024},
		{"0MB", 64 * 1024 * 1024},
		{"2GB", 2 * 1024 * 1024 * 1024},
	}
	for _, tc := range cases {
		if got := parseMemoryString(tc.input); got != tc.want {
			t.Errorf("parseMemoryString(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestNewRelay(t *testing.T) {
	r := NewRelay()
	if r == nil {
		t.Fatal("NewRelay returned nil")
	}
}

func TestIsSelectQuery(t *testing.T) {
	cases := []struct {
		query string
		want  bool
	}{
		{"SELECT * FROM users", true},
		{"  select 1", true},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"INSERT INTO users VALUES (1)", false},
		{"UPDATE users SET x=1", false},
		{"DELETE FROM users", false},
		{"DROP TABLE users", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isSelectQuery(tc.query); got != tc.want {
			t.Errorf("isSelectQuery(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
}

func TestIsModificationQuery(t *testing.T) {
	cases := []struct {
		query string
		want  bool
	}{
		{"INSERT INTO t VALUES (1)", true},
		{"UPDATE t SET x=1", true},
		{"DELETE FROM t", true},
		{"TRUNCATE TABLE t", true},
		{"DROP TABLE t", true},
		{"ALTER TABLE t ADD x INT", true},
		{"CREATE TABLE t (x INT)", true},
		{"REPLACE INTO t VALUES (1)", true},
		{"SELECT * FROM t", false},
		{"  select 1", false},
	}
	for _, tc := range cases {
		if got := isModificationQuery(tc.query); got != tc.want {
			t.Errorf("isModificationQuery(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
}

func TestExtractTablesFromQuery(t *testing.T) {
	cases := []struct {
		query string
		want  []string
	}{
		{"SELECT * FROM users WHERE id=1", []string{"users"}},
		{"SELECT * FROM orders", []string{"orders"}},
		{"SELECT 1", nil},
	}
	for _, tc := range cases {
		got := extractTablesFromQuery(tc.query)
		if len(got) != len(tc.want) {
			t.Errorf("extractTablesFromQuery(%q) = %v, want %v", tc.query, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("extractTablesFromQuery(%q)[%d] = %q, want %q", tc.query, i, got[i], tc.want[i])
			}
		}
	}
}

func TestMaxMySQLPayload(t *testing.T) {
	// maxMySQLPayload should be 16MB
	expected := int(16 << 20)
	if maxMySQLPayload != expected {
		t.Errorf("maxMySQLPayload = %d, want %d", maxMySQLPayload, expected)
	}
}

func TestSessionIDCounter(t *testing.T) {
	// Get current counter value
	initial := sessionIDCounter.Load()

	// Each new ID should increment the counter
	id1 := sessionIDCounter.Add(1)
	id2 := sessionIDCounter.Add(1)

	if id1 != initial+1 {
		t.Errorf("first ID = %d, want %d", id1, initial+1)
	}
	if id2 != initial+2 {
		t.Errorf("second ID = %d, want %d", id2, initial+2)
	}
}

func TestCreateMySQLHandshake(t *testing.T) {
	scramble := make([]byte, 20)
	for i := range scramble {
		scramble[i] = byte(i)
	}

	handshake := createMySQLHandshake(12345, scramble)

	if len(handshake) == 0 {
		t.Error("createMySQLHandshake returned empty handshake")
	}

	// Check protocol version (first byte should be 10)
	if handshake[0] != 10 {
		t.Errorf("protocol version = %d, want 10", handshake[0])
	}
}

func TestExtractMySQLScramble(t *testing.T) {
	// Create valid handshake data
	buf := []byte{
		10, // Protocol version
	}
	// Server version
	buf = append(buf, []byte("5.7.42")...)
	buf = append(buf, 0) // null terminator
	// Connection ID
	buf = append(buf, 0x01, 0x00, 0x00, 0x00)
	// Auth data part 1 (8 bytes)
	buf = append(buf, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08)
	// Filler
	buf = append(buf, 0x00)
	// Capability flags lower, charset, status
	buf = append(buf, 0x00, 0x00, 0x21, 0x00, 0x00)
	// Capability flags upper
	buf = append(buf, 0x00, 0x00)
	// Auth data length
	buf = append(buf, 21)
	// Reserved (10 bytes)
	buf = append(buf, make([]byte, 10)...)
	// Auth data part 2 (12 bytes)
	buf = append(buf, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14)

	scramble, err := extractMySQLScramble(buf)
	if err != nil {
		t.Errorf("extractMySQLScramble failed: %v", err)
	}
	if len(scramble) != 20 {
		t.Errorf("scramble length = %d, want 20", len(scramble))
	}
}

func TestExtractMySQLScrambleErrors(t *testing.T) {
	// Test with too short data
	_, err := extractMySQLScramble([]byte{0x00})
	if err == nil {
		t.Error("extractMySQLScramble should fail with short data")
	}

	// Test with wrong protocol version
	_, err = extractMySQLScramble([]byte{0x09, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	if err == nil {
		t.Error("extractMySQLScramble should fail with wrong protocol version")
	}
}

func TestParseMySQLHandshakeResponse(t *testing.T) {
	// Create valid response data (at least 32 bytes)
	buf := make([]byte, 50)
	// Capability flags
	buf[0] = 0xa6
	buf[1] = 0x85
	buf[2] = 0x00
	buf[3] = 0x00
	// Max packet size
	buf[4] = 0x00
	buf[5] = 0x00
	buf[6] = 0x00
	buf[7] = 0x01
	// Character set
	buf[8] = 0x21
	// Reserved (23 bytes)
	for i := 9; i < 32; i++ {
		buf[i] = 0x00
	}
	// Username "test"
	buf[32] = 't'
	buf[33] = 'e'
	buf[34] = 's'
	buf[35] = 't'
	buf[36] = 0x00

	username, database, err := parseMySQLHandshakeResponse(buf)
	if err != nil {
		t.Errorf("parseMySQLHandshakeResponse failed: %v", err)
	}
	if username != "test" {
		t.Errorf("username = %q, want test", username)
	}
	// Database should be empty since there's no auth response and database after it
	_ = database
}

func TestParseMySQLHandshakeResponseShortData(t *testing.T) {
	// Test with too short data
	_, _, err := parseMySQLHandshakeResponse([]byte{0x00, 0x00, 0x00, 0x00})
	if err == nil {
		t.Error("parseMySQLHandshakeResponse should fail with short data")
	}
}

func TestRelayStruct(t *testing.T) {
	r := &Relay{}
	if r == nil {
		t.Error("Failed to create Relay")
	}
}

func TestExtractLogin7CredentialsShortData(t *testing.T) {
	// Test with short data (less than 36 bytes)
	ps := &ProxySession{}
	ps.extractLogin7Credentials([]byte{0x00, 0x00, 0x00})

	// Should not panic and should leave username/database empty
	if ps.username != "" {
		t.Error("username should be empty with short data")
	}
	if ps.database != "" {
		t.Error("database should be empty with short data")
	}
}

func TestNewListener(t *testing.T) {
	log, _ := logger.New("error", "json")

	cfg := &config.PoolConfig{
		Name: "test-pool",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Body: "postgresql",
		Mode: "transaction",
	}

	// Create a minimal pool
	poolCfg := &config.PoolConfig{
		Name: "test-pool",
		Body: "postgresql",
		Mode: "transaction",
	}
	p, _ := pool.NewPool(poolCfg, nil, log)

	userDB := auth.NewUserDatabase()

	l, err := NewListener(p, cfg, nil, userDB, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}
	if l == nil {
		t.Fatal("NewListener returned nil")
	}

	// Cleanup
	l.Stop()
}

func TestListenerAddress(t *testing.T) {
	log, _ := logger.New("error", "json")

	cfg := &config.PoolConfig{
		Name: "test-pool",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 5432,
		},
		Body: "postgresql",
	}

	l := &Listener{
		config:  cfg,
		address: "127.0.0.1:5432",
		log:     log,
	}

	if addr := l.Address(); addr != "127.0.0.1:5432" {
		t.Errorf("Address() = %q, want %q", addr, "127.0.0.1:5432")
	}
}

func TestListenerIsActive(t *testing.T) {
	log, _ := logger.New("error", "json")
	l := &Listener{
		log: log,
	}

	// Should be inactive initially
	if l.IsActive() {
		t.Error("IsActive() should be false initially")
	}

	// Set active
	l.active.Store(true)
	if !l.IsActive() {
		t.Error("IsActive() should be true after setting")
	}
}

func TestListenerPool(t *testing.T) {
	log, _ := logger.New("error", "json")
	poolCfg := &config.PoolConfig{
		Name: "test-pool",
		Body: "postgresql",
		Mode: "transaction",
	}
	p, _ := pool.NewPool(poolCfg, nil, log)

	l := &Listener{
		pool: p,
		log:  log,
	}

	if got := l.Pool(); got != p {
		t.Error("Pool() returned wrong pool")
	}
}

func TestListenerQueryLogger(t *testing.T) {
	log, _ := logger.New("error", "json")
	l := &Listener{
		log: log,
	}

	// Should return nil initially
	if ql := l.QueryLogger(); ql != nil {
		t.Error("QueryLogger() should be nil initially")
	}
}

func TestListenerTransactionManager(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := pool.NewTransactionManager(30*time.Minute, 5*time.Minute, 0, log)

	l := &Listener{
		transactionMgr: tm,
		log:            log,
	}

	if got := l.TransactionManager(); got != tm {
		t.Error("TransactionManager() returned wrong manager")
	}
}

func TestListenerSessionCount(t *testing.T) {
	log, _ := logger.New("error", "json")
	l := &Listener{
		sessions: make(map[uint64]*ProxySession),
		log:      log,
	}

	// Should be 0 initially
	if count := l.SessionCount(); count != 0 {
		t.Errorf("SessionCount() = %d, want 0", count)
	}
}
