package proxy

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
	"github.com/GeryonProxy/geryon/internal/protocol/postgresql"
)

// Test Listener Stop_NotStarted
func TestListener_Stop_NotStarted_ReturnsNil(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	// Stop when not started should return nil
	if err := listener.Stop(); err != nil {
		t.Errorf("Stop() when not started should return nil, got %v", err)
	}
}

// Test parseMemoryString with GB
func TestParseMemoryString_GB(t *testing.T) {
	result := parseMemoryString("1GB")
	if result != 1024*1024*1024 {
		t.Errorf("parseMemoryString(\"1GB\") = %d, want %d", result, 1024*1024*1024)
	}
}

// Test parseMemoryString with KB
func TestParseMemoryString_KB(t *testing.T) {
	result := parseMemoryString("1024KB")
	if result != 1024*1024 {
		t.Errorf("parseMemoryString(\"1024KB\") = %d, want %d", result, 1024*1024)
	}
}

// Test parseMemoryString lowercase
func TestParseMemoryString_Lowercase(t *testing.T) {
	result := parseMemoryString("64mb")
	if result != 64*1024*1024 {
		t.Errorf("parseMemoryString(\"64mb\") = %d, want %d", result, 64*1024*1024)
	}
}

// Test ProxySession Close idempotent
func TestProxySession_Close_Idempotent(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	session, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, auth.NewAuthLimiter(), nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// Multiple closes should not panic
	session.Close()
	session.Close()
	session.Close()
}

// Test isSelectQuery with only SELECT keyword
func TestIsSelectQuery_OnlySelect(t *testing.T) {
	if !isSelectQuery("SELECT") {
		t.Error("isSelectQuery(\"SELECT\") should return true")
	}
}

// Test isModificationQuery with CALL (stored procedure)
func TestIsModificationQuery_Call(t *testing.T) {
	// CALL is not in our list
	if isModificationQuery("CALL my_procedure()") {
		t.Log("CALL recognized as modification")
	}
}

// Test extractTablesFromQuery with JOIN
func TestExtractTablesFromQuery_JoinClause(t *testing.T) {
	tables := extractTablesFromQuery("SELECT * FROM users JOIN orders ON users.id=orders.user_id")
	if len(tables) == 0 || tables[0] != "users" {
		t.Errorf("extractTablesFromQuery failed: got %v", tables)
	}
}

// Test Listener with cache config
func TestListener_CacheConfig(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Cache: config.CacheConfig{
			Enabled:    true,
			MaxMemory:  "64MB",
			DefaultTTL: "5m",
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	if listener.cacheStore == nil {
		t.Error("cacheStore should be initialized when cache is enabled")
	}
}

// Test connection deadline setting
func TestConnection_Deadline(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	// Test setting read deadline - should not panic
	if err := server.SetReadDeadline(time.Now().Add(10 * time.Minute)); err != nil {
		t.Logf("SetReadDeadline error: %v", err)
	}
	if err := client.SetWriteDeadline(time.Now().Add(5 * time.Minute)); err != nil {
		t.Logf("SetWriteDeadline error: %v", err)
	}
}

// Test min helper function
func TestMin_Int(t *testing.T) {
	a, b := 5, 10
	if min(a, b) != 5 {
		t.Error("min(5, 10) should return 5")
	}
	if min(b, a) != 5 {
		t.Error("min(10, 5) should return 5")
	}
}

// Test Listener Start when already active
func TestListener_Start_WhenActive(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	// First start
	if err := listener.Start(); err != nil {
		t.Fatalf("First Start failed: %v", err)
	}
	defer listener.Stop()

	// Second start should fail
	if err := listener.Start(); err == nil {
		t.Error("Second Start should fail when already active")
	}
}

// Test session counter increment
func TestSessionIDCounter_Increment(t *testing.T) {
	before := sessionIDCounter.Load()

	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server1, client1 := net.Pipe()
	defer server1.Close()
	defer client1.Close()

	NewProxySession(server1, p, codec, nil, cfg, nil, nil, nil, nil, auth.NewAuthLimiter(), nil, nil, log)

	after := sessionIDCounter.Load()
	if after <= before {
		t.Error("sessionIDCounter should increment after creating session")
	}
}

// Test Listener with cache enabled
func TestListener_CacheEnabled(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test-cache",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Cache: config.CacheConfig{
			Enabled:    true,
			MaxMemory:  "100MB",
			DefaultTTL: "5m",
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test-cache")
	codec := postgresql.NewCodec()

	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	if listener.cacheStore == nil {
		t.Error("Cache store should be initialized when enabled")
	}
	if listener.cacheRules == nil {
		t.Error("Cache rules should be initialized when enabled")
	}
}

// Test Listener with read/write splitting router
func TestListener_ReadWriteSplitting(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test-routing",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
				{Host: "127.0.0.1", Port: 5433, Role: "replica"},
			},
		},
		Routing: config.RoutingConfig{
			ReadWriteSplit: true,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test-routing")
	codec := postgresql.NewCodec()

	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	if listener.router == nil {
		t.Error("Router should be initialized when read/write splitting is enabled")
	}
}

// Test Listener without read/write splitting
func TestListener_NoReadWriteSplitting(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test-no-routing",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test-no-routing")
	codec := postgresql.NewCodec()

	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	if listener.router != nil {
		t.Error("Router should be nil when read/write splitting is disabled")
	}
}

// Test TransactionManager integration
func TestListener_TransactionManagerIntegration(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test-txn",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test-txn")
	codec := postgresql.NewCodec()

	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	if listener.TransactionManager() == nil {
		t.Error("TransactionManager should be initialized")
	}
}

// Test isSelectQuery with various inputs
func TestIsSelectQuery_Various(t *testing.T) {
	tests := []struct {
		query    string
		expected bool
	}{
		{"SELECT * FROM users", true},
		{"select 1", true},
		{"  SELECT count(*)", true},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"INSERT INTO users", false},
		{"UPDATE users SET", false},
		{"DELETE FROM users", false},
		{"DROP TABLE users", false},
		{"", false},
	}

	for _, tt := range tests {
		result := isSelectQuery(tt.query)
		if result != tt.expected {
			t.Errorf("isSelectQuery(%q) = %v, want %v", tt.query, result, tt.expected)
		}
	}
}

// Test isModificationQuery with various inputs
func TestIsModificationQuery_Various(t *testing.T) {
	tests := []struct {
		query    string
		expected bool
	}{
		{"INSERT INTO t VALUES (1)", true},
		{"UPDATE t SET x=1", true},
		{"DELETE FROM t", true},
		{"TRUNCATE TABLE t", true},
		{"DROP TABLE t", true},
		{"ALTER TABLE t ADD x INT", true},
		{"CREATE TABLE t (x INT)", true},
		{"SELECT * FROM t", false},
		{"", false},
	}

	for _, tt := range tests {
		result := isModificationQuery(tt.query)
		if result != tt.expected {
			t.Errorf("isModificationQuery(%q) = %v, want %v", tt.query, result, tt.expected)
		}
	}
}

// Test ProxySession stmtRepreparer initialization
func TestProxySession_StmtRepreparer(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "statement",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	session, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, auth.NewAuthLimiter(), nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	if session.stmtRepreparer == nil {
		t.Error("stmtRepreparer should be initialized for all sessions")
		}
	}

// Test Listener with configurable transaction timeouts
func TestListener_TransactionTimeoutsConfig(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test-txn-timeouts",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Transaction: config.TransactionConfig{
			Timeout:       "10m",
			IdleTimeout:   "2m",
			CheckInterval: "15s",
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test-txn-timeouts")
	codec := postgresql.NewCodec()

	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	if listener.transactionMgr == nil {
		t.Fatal("TransactionManager should be initialized")
	}

	// Verify it's functional (can register/unregister)
	info := listener.transactionMgr.Register(1, 100, nil)
	if info == nil {
		t.Error("Register should return non-nil")
	}
	listener.transactionMgr.Unregister(info.ID)
}

// Test parseTxnDuration with various inputs
func TestParseTxnDuration(t *testing.T) {
	tests := []struct {
		input      string
		defaultVal time.Duration
		expected   time.Duration
	}{
		{"", 30 * time.Minute, 30 * time.Minute},
		{"10m", 30 * time.Minute, 10 * time.Minute},
		{"1h", 30 * time.Minute, time.Hour},
		{"30s", 30 * time.Minute, 30 * time.Second},
		{"invalid", 5 * time.Minute, 5 * time.Minute},
	}

	for _, tt := range tests {
		result := parseTxnDuration(tt.input, tt.defaultVal)
		if result != tt.expected {
			t.Errorf("parseTxnDuration(%q, %v) = %v, want %v", tt.input, tt.defaultVal, result, tt.expected)
		}
	}
}

// --- Additional coverage tests ---

// Test handleStartup with unsupported body type
func TestProxySession_HandleStartup_UnsupportedBody(t *testing.T) {
	log, _ := logger.New("error", "json")
	// Create pool with valid body type first
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	// Override body type to unsupported after pool creation
	unsupportedCfg := *cfg
	unsupportedCfg.Body = "oracle"

	session, err := NewProxySession(server, p, codec, nil, &unsupportedCfg, nil, nil, nil, nil, auth.NewAuthLimiter(), nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	err = session.handleStartup(context.Background())
	if err == nil {
		t.Error("handleStartup should fail for unsupported body type")
	}
	if !strings.Contains(err.Error(), "unsupported body type") {
		t.Errorf("Error = %q, should mention unsupported body type", err.Error())
	}
	session.Close()
}

// Test sendRollbackToBackend with no server connection
func TestProxySession_SendRollbackToBackend_NoServerConn(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	session, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, auth.NewAuthLimiter(), nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// sendRollbackToBackend should not panic with no server connection
	session.sendRollbackToBackend()
	session.Close()
}

// Test recordAuthFailure with limiter
func TestProxySession_RecordAuthFailure_WithLimiter(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	limiter := auth.NewAuthLimiter()

	session, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, limiter, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// Record multiple failures
	for i := 0; i < 5; i++ {
		session.recordAuthFailure()
	}

	session.Close()
}

// Test recordAuthSuccess with limiter
func TestProxySession_RecordAuthSuccess_WithLimiter(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	limiter := auth.NewAuthLimiter()

	session, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, limiter, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// Record success should not panic
	session.recordAuthSuccess()
	session.Close()
}

// Test extractLogin7Credentials with too-short data
func TestProxySession_ExtractLogin7_TooShort(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "mssql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 1433, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	session, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, auth.NewAuthLimiter(), nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// Too short data - should not panic
	session.extractLogin7Credentials([]byte{1, 2, 3})
	if session.username != "" {
		t.Error("Username should be empty for short data")
	}

	session.Close()
}

// Test extractLogin7Credentials with valid-ish data containing username
func TestProxySession_ExtractLogin7_ValidUsername(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "mssql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 1433, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	session, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, auth.NewAuthLimiter(), nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// Build a Login7 packet with a username "sa"
	// Minimum: 36 bytes header + username data
	data := make([]byte, 100)
	// Username offset at byte 28 (little-endian uint16) = 40
	binary.LittleEndian.PutUint16(data[28:30], 40)
	// Username length in chars at byte 30 = 2 ("sa")
	binary.LittleEndian.PutUint16(data[30:32], 2)
	// Write "sa" as UTF-16LE at offset 40
	data[40] = 's'
	data[41] = 0
	data[42] = 'a'
	data[43] = 0

	session.extractLogin7Credentials(data)
	if session.username != "sa" {
		t.Errorf("username = %q, want sa", session.username)
	}

	session.Close()
}

// Test authenticateWithCertificate non-TLS connection
func TestProxySession_AuthenticateWithCertificate_NonTLS(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	session, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, auth.NewAuthLimiter(), nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// Non-TLS connection should return nil (not error)
	err = session.authenticateWithCertificate()
	if err != nil {
		t.Errorf("authenticateWithCertificate on non-TLS should return nil, got %v", err)
	}

	session.Close()
}

// Test reprepareStatement with empty statement name
func TestProxySession_ReprepareStatement_EmptyStmtName(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "statement",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	session, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, auth.NewAuthLimiter(), nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// Empty name should return early without panic
	session.reprepareStatement(codec, server, "")
	session.Close()
}

// Test Listener acceptLoop context cancellation
func TestListener_AcceptLoop_ContextCancel(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test-accept",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test-accept")
	codec := postgresql.NewCodec()

	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	if err := listener.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Stop will cancel context which stops acceptLoop
	if err := listener.Stop(); err != nil {
		t.Errorf("Stop failed: %v", err)
	}

	// Verify stopped
	if listener.IsActive() {
		t.Error("Listener should not be active after Stop")
	}
}

// Test ProxySession query counting
func TestProxySession_QueryCounting(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	session, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, auth.NewAuthLimiter(), nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	if session.QueryCount() != 0 {
		t.Error("Initial query count should be 0")
	}

	// Simulate query count increment
	session.queryCount.Add(1)
	if session.QueryCount() != 1 {
		t.Errorf("QueryCount = %d, want 1", session.QueryCount())
	}

	session.Close()
}

// Test SetDeadline with zero timeout
func TestSetDeadline_ZeroTimeout(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	// Zero timeout should not set deadline
	SetDeadline(server, 0)
	// Should not panic
}

// Test ProxySession Handle with context cancellation
func TestProxySession_Handle_ContextCancel(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	session, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, auth.NewAuthLimiter(), nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Handle blocks on handleStartup which reads from pipe
	// Cancel context after a short delay then close client to unblock
	done := make(chan struct{})
	go func() {
		session.Handle(ctx)
		close(done)
	}()

	// Cancel context and close the pipe to unblock the read
	time.Sleep(50 * time.Millisecond)
	cancel()
	client.Close()

	select {
	case <-done:
		// Good - Handle returned after context cancellation + pipe close
	case <-time.After(3 * time.Second):
		t.Error("Handle should return after context cancellation")
	}

	session.Close()
}
