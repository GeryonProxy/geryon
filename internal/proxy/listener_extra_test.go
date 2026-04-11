package proxy

import (
	"net"
	"testing"
	"time"

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

	session, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, nil, log)
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

	NewProxySession(server1, p, codec, nil, cfg, nil, nil, nil, nil, nil, log)

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
