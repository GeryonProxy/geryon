package pool

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

// MockCodec implements a minimal codec for testing
type MockCodec struct{}

func (m *MockCodec) Protocol() common.Protocol { return common.ProtocolPostgreSQL }
func (m *MockCodec) ReadMessage(r io.Reader) (*common.Message, error)  { return nil, nil }
func (m *MockCodec) WriteMessage(w io.Writer, msg *common.Message) error { return nil }
func (m *MockCodec) IsQuery(msg *common.Message) bool { return false }
func (m *MockCodec) IsPrepare(msg *common.Message) bool { return false }
func (m *MockCodec) IsExecute(msg *common.Message) bool { return false }
func (m *MockCodec) IsClose(msg *common.Message) bool { return false }
func (m *MockCodec) IsBind(msg *common.Message) bool { return false }
func (m *MockCodec) IsSync(msg *common.Message) bool { return false }
func (m *MockCodec) IsStartup(msg *common.Message) bool { return false }
func (m *MockCodec) IsTerminate(msg *common.Message) bool { return false }
func (m *MockCodec) IsTransactionBegin(msg *common.Message) bool { return false }
func (m *MockCodec) IsTransactionEnd(msg *common.Message) bool { return false }
func (m *MockCodec) ExtractQuery(msg *common.Message) (string, error) { return "", nil }

func TestParsePoolMode(t *testing.T) {
	tests := []struct {
		input    string
		expected PoolMode
		wantErr  bool
	}{
		{"session", ModeSession, false},
		{"transaction", ModeTransaction, false},
		{"statement", ModeStatement, false},
		{"invalid", ModeTransaction, true},
		{"", ModeTransaction, true},
	}

	for _, tt := range tests {
		got, err := ParsePoolMode(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParsePoolMode(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParsePoolMode(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.expected {
			t.Errorf("ParsePoolMode(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestPoolModeString(t *testing.T) {
	tests := []struct {
		mode     PoolMode
		expected string
	}{
		{ModeSession, "session"},
		{ModeTransaction, "transaction"},
		{ModeStatement, "statement"},
		{PoolMode(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.mode.String()
		if got != tt.expected {
			t.Errorf("PoolMode(%d).String() = %q, want %q", tt.mode, got, tt.expected)
		}
	}
}

func TestBackendAddress(t *testing.T) {
	b := &Backend{
		Host: "localhost",
		Port: 5432,
	}
	expected := "localhost:5432"
	if got := b.Address(); got != expected {
		t.Errorf("Address() = %q, want %q", got, expected)
	}
}

func TestBackendHealth(t *testing.T) {
	b := &Backend{
		Host: "localhost",
		Port: 5432,
	}

	// Initially unhealthy (zero value)
	if b.Healthy.Load() {
		t.Error("expected backend to be unhealthy initially")
	}

	// Set healthy
	b.Healthy.Store(true)
	if !b.Healthy.Load() {
		t.Error("expected backend to be healthy after Store(true)")
	}

	// Set unhealthy
	b.Healthy.Store(false)
	if b.Healthy.Load() {
		t.Error("expected backend to be unhealthy after Store(false)")
	}
}

func TestNewPool(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Database: "testdb",
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
				{Host: "replica1", Port: 5432, Role: "replica"},
			},
		},
		Limits: config.LimitConfig{
			MinServerConnections: 1,
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	if pool.Name() != "test-pool" {
		t.Errorf("expected pool name 'test-pool', got %q", pool.Name())
	}

	if pool.Mode() != ModeTransaction {
		t.Errorf("expected mode transaction, got %v", pool.Mode())
	}

	// Check backends were initialized
	stats := pool.Stats()
	if stats.BackendCount != 2 {
		t.Errorf("expected 2 backends, got %d", stats.BackendCount)
	}
}

func TestNewPoolInvalidMode(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "invalid-mode",
		Body: "postgresql",
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	_, err := NewPool(cfg, codec, log)
	if err == nil {
		t.Error("expected error for invalid pool mode")
	}
}

func TestPoolStats(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Database: "testdb",
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MinServerConnections: 1,
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Test incrementing counters
	pool.IncrementClientCount()
	pool.IncrementClientCount()
	pool.IncrementQueryCount()
	pool.IncrementQueryCount()
	pool.IncrementQueryCount()
	pool.IncrementTxnCount()

	stats := pool.Stats()

	if stats.Name != "test-pool" {
		t.Errorf("expected name 'test-pool', got %q", stats.Name)
	}
	if stats.Mode != "session" {
		t.Errorf("expected mode 'session', got %q", stats.Mode)
	}
	if stats.ClientConnections != 2 {
		t.Errorf("expected 2 client connections, got %d", stats.ClientConnections)
	}
	if stats.TotalQueries != 3 {
		t.Errorf("expected 3 total queries, got %d", stats.TotalQueries)
	}
	if stats.TotalTransactions != 1 {
		t.Errorf("expected 1 total transaction, got %d", stats.TotalTransactions)
	}
	if stats.BackendCount != 1 {
		t.Errorf("expected 1 backend, got %d", stats.BackendCount)
	}

	// Test decrementing client count
	pool.DecrementClientCount()
	stats = pool.Stats()
	if stats.ClientConnections != 1 {
		t.Errorf("expected 1 client connection after decrement, got %d", stats.ClientConnections)
	}
}

func TestServerConnInUse(t *testing.T) {
	conn := &ServerConn{
		id:            1,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}

	if conn.IsInUse() {
		t.Error("expected connection to not be in use initially")
	}

	conn.MarkInUse()
	if !conn.IsInUse() {
		t.Error("expected connection to be in use after MarkInUse")
	}

	conn.MarkIdle()
	if conn.IsInUse() {
		t.Error("expected connection to not be in use after MarkIdle")
	}
}

func TestServerConnPreparedStatements(t *testing.T) {
	conn := &ServerConn{
		id:            1,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}

	// Test adding prepared statement
	conn.AddPreparedStatement("stmt1")
	if !conn.HasPreparedStatement("stmt1") {
		t.Error("expected stmt1 to exist after AddPreparedStatement")
	}

	// Test non-existent statement
	if conn.HasPreparedStatement("stmt2") {
		t.Error("expected stmt2 to not exist")
	}

	// Test removing prepared statement
	conn.RemovePreparedStatement("stmt1")
	if conn.HasPreparedStatement("stmt1") {
		t.Error("expected stmt1 to be removed")
	}
}

func TestWaitQueue(t *testing.T) {
	wq := NewWaitQueue(100)

	// Test basic signal without waiters
	mockConn := &ServerConn{id: 1}
	signaled := wq.Signal(mockConn)
	if signaled {
		t.Error("expected Signal to return false when no waiters")
	}

	// Test wait with timeout (no connection available)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := wq.Wait(ctx, 50*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error from Wait")
	}
}

func TestSelectBackendWithFallback(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Database: "testdb",
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica1", Port: 5432, Role: "replica"},
				{Host: "replica2", Port: 5432, Role: "replica"},
			},
		},
		Limits: config.LimitConfig{
			MinServerConnections: 1,
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Test primary selection (primary is healthy)
	backend := pool.selectBackendWithFallback()
	if backend == nil {
		t.Fatal("expected backend, got nil")
	}
	if backend.Host != "primary" {
		t.Errorf("expected primary backend, got %s", backend.Host)
	}

	// Mark primary as unhealthy
	pool.primary.Healthy.Store(false)

	// Should fall back to replica
	backend = pool.selectBackendWithFallback()
	if backend == nil {
		t.Fatal("expected backend, got nil")
	}
	if backend.Role != "replica" {
		t.Errorf("expected replica backend, got %s", backend.Role)
	}

	// Mark all as unhealthy
	for _, b := range pool.backends {
		b.Healthy.Store(false)
	}

	// Should return nil when no healthy backends
	backend = pool.selectBackendWithFallback()
	if backend != nil {
		t.Error("expected nil when no healthy backends")
	}
}

func TestPoolClose(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Database: "testdb",
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MinServerConnections: 1,
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	err = pool.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Check context is cancelled
	select {
	case <-pool.ctx.Done():
		// Good, context was cancelled
	default:
		t.Error("expected context to be cancelled after Close")
	}
}

func TestServerConnPoolAcquireRelease(t *testing.T) {
	pool := newServerConnPool(1, 5)

	// Test empty pool
	conn := pool.acquire()
	if conn != nil {
		t.Error("expected nil from empty pool")
	}

	// Create a mock connection
	mockConn := &ServerConn{
		id:            1,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}

	// Add to active
	pool.addActive(mockConn)

	if pool.activeCount() != 1 {
		t.Errorf("expected 1 active connection, got %d", pool.activeCount())
	}

	// Release it
	pool.release(mockConn)

	if pool.activeCount() != 0 {
		t.Errorf("expected 0 active connections after release, got %d", pool.activeCount())
	}
	if pool.idleCount() != 1 {
		t.Errorf("expected 1 idle connection, got %d", pool.idleCount())
	}

	// Re-acquire
	conn = pool.acquire()
	if conn == nil {
		t.Error("expected connection from pool")
	}
	if conn.id != 1 {
		t.Errorf("expected connection id 1, got %d", conn.id)
	}
}

func TestServerConnPoolSize(t *testing.T) {
	pool := newServerConnPool(1, 5)

	// Create mock connections
	conn1 := &ServerConn{id: 1, preparedStmts: make(map[string]bool), paramStatus: make(map[string]string)}
	conn2 := &ServerConn{id: 2, preparedStmts: make(map[string]bool), paramStatus: make(map[string]string)}

	pool.addActive(conn1)
	pool.addActive(conn2)

	if pool.size() != 2 {
		t.Errorf("expected pool size 2, got %d", pool.size())
	}

	pool.release(conn1)

	if pool.size() != 2 {
		t.Errorf("expected pool size 2 after release, got %d", pool.size())
	}
	if pool.activeCount() != 1 {
		t.Errorf("expected 1 active, got %d", pool.activeCount())
	}
	if pool.idleCount() != 1 {
		t.Errorf("expected 1 idle, got %d", pool.idleCount())
	}
}

func BenchmarkPoolModeString(b *testing.B) {
	for b.Loop() {
		ModeTransaction.String()
	}
}

func BenchmarkBackendAddress(b *testing.B) {
	backend := &Backend{Host: "localhost", Port: 5432}
	for b.Loop() {
		backend.Address()
	}
}
