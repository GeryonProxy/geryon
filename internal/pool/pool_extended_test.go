package pool

import (
	"context"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

func TestBackend_Draining(t *testing.T) {
	b := &Backend{
		Host: "localhost",
		Port: 5432,
	}

	// Initially not draining
	if b.Draining.Load() {
		t.Error("expected backend to not be draining initially")
	}

	// Set draining
	b.Draining.Store(true)
	if !b.Draining.Load() {
		t.Error("expected backend to be draining after Store(true)")
	}

	// Set not draining
	b.Draining.Store(false)
	if b.Draining.Load() {
		t.Error("expected backend to not be draining after Store(false)")
	}
}

func TestBackend_ConnCount(t *testing.T) {
	b := &Backend{
		Host: "localhost",
		Port: 5432,
	}

	// Initially 0
	if b.ConnCount.Load() != 0 {
		t.Errorf("expected ConnCount 0, got %d", b.ConnCount.Load())
	}

	// Increment
	b.ConnCount.Add(1)
	if b.ConnCount.Load() != 1 {
		t.Errorf("expected ConnCount 1, got %d", b.ConnCount.Load())
	}

	// Decrement
	b.ConnCount.Add(-1)
	if b.ConnCount.Load() != 0 {
		t.Errorf("expected ConnCount 0, got %d", b.ConnCount.Load())
	}
}

func TestBackend_LastCheck(t *testing.T) {
	b := &Backend{
		Host: "localhost",
		Port: 5432,
	}

	// Initially zero time
	if !b.LastCheck.IsZero() {
		t.Error("expected LastCheck to be zero initially")
	}
}

func TestNewPool_WithReplicaWeights(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Database: "testdb",
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary", Weight: 1},
				{Host: "replica1", Port: 5432, Role: "replica", Weight: 3},
				{Host: "replica2", Port: 5432, Role: "replica", Weight: 2},
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

	// Check that weights were set
	stats := pool.Stats()
	if stats.BackendCount != 3 {
		t.Errorf("expected 3 backends, got %d", stats.BackendCount)
	}
}

func TestNewPool_NoHosts(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Database: "testdb",
			Hosts:    []config.BackendHost{},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	stats := pool.Stats()
	if stats.BackendCount != 0 {
		t.Errorf("expected 0 backends, got %d", stats.BackendCount)
	}
}

func TestPool_Mode(t *testing.T) {
	tests := []struct {
		modeStr  string
		expected PoolMode
	}{
		{"session", ModeSession},
		{"transaction", ModeTransaction},
		{"statement", ModeStatement},
	}

	for _, tt := range tests {
		cfg := &config.PoolConfig{
			Name: "test-pool",
			Mode: tt.modeStr,
			Body: "postgresql",
			Backend: config.BackendConfig{
				Database: "testdb",
				Hosts: []config.BackendHost{
					{Host: "localhost", Port: 5432, Role: "primary"},
				},
			},
		}

		log, _ := logger.New("error", "json")
		codec := &MockCodec{}

		pool, err := NewPool(cfg, codec, log)
		if err != nil {
			t.Fatalf("NewPool failed for mode %s: %v", tt.modeStr, err)
		}

		if pool.Mode() != tt.expected {
			t.Errorf("mode = %v, want %v for input %s", pool.Mode(), tt.expected, tt.modeStr)
		}
	}
}

func TestServerConn_ID(t *testing.T) {
	sc := &ServerConn{
		id: 12345,
	}
	if sc.ID() != 12345 {
		t.Errorf("ID() = %d, want 12345", sc.ID())
	}
}

func TestServerConn_TxnActive(t *testing.T) {
	sc := &ServerConn{}

	// Initially false
	if sc.txnActive.Load() {
		t.Error("expected txnActive to be false initially")
	}

	// Set to true
	sc.txnActive.Store(true)
	if !sc.txnActive.Load() {
		t.Error("expected txnActive to be true after Store(true)")
	}
}

func TestServerConn_InUse(t *testing.T) {
	sc := &ServerConn{}

	// Initially false
	if sc.inUse.Load() {
		t.Error("expected inUse to be false initially")
	}

	// Set to true
	sc.inUse.Store(true)
	if !sc.inUse.Load() {
		t.Error("expected inUse to be true after Store(true)")
	}
}

func TestServerConn_ResetPending(t *testing.T) {
	sc := &ServerConn{}

	// Initially false
	if sc.resetPending.Load() {
		t.Error("expected resetPending to be false initially")
	}

	// Set to true
	sc.resetPending.Store(true)
	if !sc.resetPending.Load() {
		t.Error("expected resetPending to be true after Store(true)")
	}
}

func TestPool_GetBackends(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5432, Role: "replica"},
			},
		},
		Limits: config.LimitConfig{
			MinServerConnections: 0,
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Get backends
	backends := pool.GetBackends()
	if len(backends) != 2 {
		t.Errorf("len(backends) = %d, want 2", len(backends))
	}

	// Check backend properties
	for _, b := range backends {
		if b.Role != "primary" && b.Role != "replica" {
			t.Errorf("unexpected role: %s", b.Role)
		}
	}
}

func TestPool_GetPrimary(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5432, Role: "replica"},
			},
		},
		Limits: config.LimitConfig{
			MinServerConnections: 0,
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark primary as healthy
	for _, b := range pool.GetBackends() {
		if b.Role == "primary" {
			b.Healthy.Store(true)
		}
	}

	// Get primary
	primary := pool.GetPrimary()
	if primary == nil {
		t.Fatal("expected to get primary backend")
	}
	if primary.Role != "primary" {
		t.Errorf("expected primary role, got %s", primary.Role)
	}
}

func TestPool_GetReplicas(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica1", Port: 5432, Role: "replica"},
				{Host: "replica2", Port: 5432, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Get replicas
	replicas := pool.GetReplicas()
	if len(replicas) != 2 {
		t.Errorf("expected 2 replicas, got %d", len(replicas))
	}

	for _, r := range replicas {
		if r.Role != "replica" {
			t.Errorf("expected replica role, got %s", r.Role)
		}
	}
}

func TestPool_DrainBackend(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Drain the backend
	_, err = pool.DrainBackend("localhost:5432")
	if err != nil {
		t.Errorf("DrainBackend failed: %v", err)
	}

	// Check if draining
	if !pool.IsDraining("localhost:5432") {
		t.Error("expected backend to be draining")
	}

	// Cancel drain
	err = pool.CancelDrain("localhost:5432")
	if err != nil {
		t.Errorf("CancelDrain failed: %v", err)
	}

	// Check not draining
	if pool.IsDraining("localhost:5432") {
		t.Error("expected backend to not be draining")
	}
}

func TestPool_DrainBackend_NotFound(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Drain non-existent backend
	_, err = pool.DrainBackend("nonexistent:5432")
	if err == nil {
		t.Error("expected error for non-existent backend")
	}
}

func TestPool_CancelDrain_NotFound(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Cancel drain for non-existent backend
	err = pool.CancelDrain("nonexistent:5432")
	if err == nil {
		t.Error("expected error for non-existent backend")
	}
}

func TestPool_GetDrainingBackends(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5432, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Initially no draining backends
	draining := pool.GetDrainingBackends()
	if len(draining) != 0 {
		t.Errorf("expected 0 draining backends, got %d", len(draining))
	}

	// Drain one backend
	pool.DrainBackend("primary:5432")

	// Now should have 1 draining backend
	draining = pool.GetDrainingBackends()
	if len(draining) != 1 {
		t.Errorf("expected 1 draining backend, got %d", len(draining))
	}
}

func TestPool_IsDraining_NotFound(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Check non-existent backend
	if pool.IsDraining("nonexistent:5432") {
		t.Error("expected false for non-existent backend")
	}
}

func TestPool_Stats(t *testing.T) {
	cfg := &config.PoolConfig{
		Name:     "test-pool",
		Mode:     "transaction",
		Body:     "postgresql",
		Backend: config.BackendConfig{
			Database: "testdb",
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5432, Role: "replica"},
			},
		},
		Limits: config.LimitConfig{
			MinServerConnections: 5,
			MaxServerConnections: 100,
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	stats := pool.Stats()
	if stats.Name != "test-pool" {
		t.Errorf("Stats.Name = %q, want test-pool", stats.Name)
	}
	if stats.Mode != "transaction" {
		t.Errorf("Stats.Mode = %q, want transaction", stats.Mode)
	}
	if stats.BackendCount != 2 {
		t.Errorf("Stats.BackendCount = %d, want 2", stats.BackendCount)
	}
}

func TestPool_PreparedStatementCache(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Get prepared statement cache
	psc := pool.PreparedStatementCache()
	if psc == nil {
		t.Error("PreparedStatementCache should not be nil")
	}
}

func TestPool_QueryCache(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Get query cache - may be nil if caching is not configured
	qc := pool.QueryCache()
	// Query cache is optional, so nil is acceptable
	_ = qc
}

func TestPool_Name(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "my-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	if pool.Name() != "my-pool" {
		t.Errorf("Name() = %q, want my-pool", pool.Name())
	}
}

func TestPoolMode_String(t *testing.T) {
	tests := []struct {
		mode PoolMode
		want string
	}{
		{ModeSession, "session"},
		{ModeTransaction, "transaction"},
		{ModeStatement, "statement"},
		{99, "unknown"}, // Invalid mode
	}

	for _, tt := range tests {
		got := tt.mode.String()
		if got != tt.want {
			t.Errorf("PoolMode(%d).String() = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestBackend_Address(t *testing.T) {
	b := &Backend{
		Host: "localhost",
		Port: 5432,
	}

	if b.Address() != "localhost:5432" {
		t.Errorf("Address() = %q, want localhost:5432", b.Address())
	}

	b2 := &Backend{
		Host: "192.168.1.1",
		Port: 3306,
	}

	if b2.Address() != "192.168.1.1:3306" {
		t.Errorf("Address() = %q, want 192.168.1.1:3306", b2.Address())
	}
}

func TestServerConn_PreparedStatements(t *testing.T) {
	sc := &ServerConn{
		id:            1,
		preparedStmts: make(map[string]bool),
	}

	// Initially should not have statement
	if sc.HasPreparedStatement("stmt1") {
		t.Error("should not have stmt1 initially")
	}

	// Add statement
	sc.AddPreparedStatement("stmt1")
	if !sc.HasPreparedStatement("stmt1") {
		t.Error("should have stmt1 after adding")
	}

	// Add another
	sc.AddPreparedStatement("stmt2")
	if !sc.HasPreparedStatement("stmt2") {
		t.Error("should have stmt2 after adding")
	}

	// Remove statement
	sc.RemovePreparedStatement("stmt1")
	if sc.HasPreparedStatement("stmt1") {
		t.Error("should not have stmt1 after removing")
	}

	// stmt2 should still exist
	if !sc.HasPreparedStatement("stmt2") {
		t.Error("should still have stmt2")
	}
}

func TestServerConn_Backend(t *testing.T) {
	b := &Backend{
		Host: "localhost",
		Port: 5432,
	}

	sc := &ServerConn{
		id:      1,
		backend: b,
	}

	if sc.Backend() != b {
		t.Error("Backend() should return the set backend")
	}

	// Test nil backend
	sc2 := &ServerConn{
		id: 2,
	}

	if sc2.Backend() != nil {
		t.Error("Backend() should return nil when not set")
	}
}

func TestServerConn_Conn(t *testing.T) {
	sc := &ServerConn{
		id: 1,
	}

	// Initially nil
	if sc.Conn() != nil {
		t.Error("Conn() should return nil initially")
	}
}

func TestServerConn_IsInUse(t *testing.T) {
	sc := &ServerConn{
		id: 1,
	}

	// Initially not in use
	if sc.IsInUse() {
		t.Error("IsInUse() should be false initially")
	}

	// Mark in use
	sc.MarkInUse()
	if !sc.IsInUse() {
		t.Error("IsInUse() should be true after MarkInUse()")
	}

	// Mark idle
	sc.MarkIdle()
	if sc.IsInUse() {
		t.Error("IsInUse() should be false after MarkIdle()")
	}
}

func TestServerConn_Close(t *testing.T) {
	b := &Backend{
		Host: "localhost",
		Port: 5432,
	}
	b.ConnCount.Store(5)

	sc := &ServerConn{
		id:      1,
		backend: b,
	}

	err := sc.Close()
	if err != nil {
		t.Errorf("Close error: %v", err)
	}

	if b.ConnCount.Load() != 4 {
		t.Errorf("ConnCount = %d, want 4", b.ConnCount.Load())
	}

	sc2 := &ServerConn{
		id: 2,
	}
	err = sc2.Close()
	if err != nil {
		t.Errorf("Close with nil backend error: %v", err)
	}
}

func TestServerConnPool_closeAll(t *testing.T) {
	pool := newServerConnPool(1, 5)

	conn1 := &ServerConn{id: 1, preparedStmts: make(map[string]bool), paramStatus: make(map[string]string)}
	conn2 := &ServerConn{id: 2, preparedStmts: make(map[string]bool), paramStatus: make(map[string]string)}
	conn3 := &ServerConn{id: 3, preparedStmts: make(map[string]bool), paramStatus: make(map[string]string)}

	pool.addActive(conn1)
	pool.idle = append(pool.idle, conn2)
	pool.idle = append(pool.idle, conn3)

	if pool.size() != 3 {
		t.Errorf("size before close = %d, want 3", pool.size())
	}

	pool.closeAll()

	if pool.size() != 0 {
		t.Errorf("size after close = %d, want 0", pool.size())
	}
}

func TestWaitQueue_Wait_ContextCancellation(t *testing.T) {
	wq := NewWaitQueue(100)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := wq.Wait(ctx, 5*time.Second)
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestPool_DrainBackend_AlreadyDraining(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.DrainBackend("localhost:5432")

	_, err = pool.DrainBackend("localhost:5432")
	if err == nil {
		t.Error("expected error when draining already draining backend")
	}
}

func TestPool_CancelDrain_NotDraining(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	err = pool.CancelDrain("localhost:5432")
	if err == nil {
		t.Error("expected error when cancelling drain on non-draining backend")
	}
}

func TestPool_IncrementTxnCount(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.IncrementTxnCount()
	pool.IncrementTxnCount()

	stats := pool.Stats()
	if stats.TotalTransactions != 2 {
		t.Errorf("TotalTransactions = %d, want 2", stats.TotalTransactions)
	}
}

func TestPool_selectBackend_NoBackends(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	backend := pool.selectBackend()
	if backend != nil {
		t.Error("expected nil when no backends")
	}
}

func TestPool_createServerConn_NoBackends(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{},
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	_, err = pool.createServerConn()
	if err == nil {
		t.Error("expected error when no backends available")
	}
}

func TestNewWaitQueue_DefaultSize(t *testing.T) {
	wq := NewWaitQueue(0)
	if wq.maxSize != 1000 {
		t.Errorf("maxSize = %d, want 1000 (default)", wq.maxSize)
	}

	wq = NewWaitQueue(-1)
	if wq.maxSize != 1000 {
		t.Errorf("maxSize = %d, want 1000 (default)", wq.maxSize)
	}
}

// Test Backend struct fields
func TestBackend_Role(t *testing.T) {
	b := &Backend{
		Host:   "127.0.0.1",
		Port:   5432,
		Role:   "primary",
		Weight: 10,
	}
	if b.Role != "primary" {
		t.Errorf("Role = %q, want primary", b.Role)
	}
}

func TestBackend_Weight(t *testing.T) {
	b := &Backend{
		Host:   "127.0.0.1",
		Port:   5432,
		Role:   "replica",
		Weight: 5,
	}
	if b.Weight != 5 {
		t.Errorf("Weight = %d, want 5", b.Weight)
	}
}

func TestBackend_Database(t *testing.T) {
	b := &Backend{
		Host:     "127.0.0.1",
		Port:     5432,
		Role:     "primary",
		Database: "testdb",
	}
	if b.Database != "testdb" {
		t.Errorf("Database = %q, want testdb", b.Database)
	}
}

func TestBackend_Healthy(t *testing.T) {
	b := &Backend{
		Host: "127.0.0.1",
		Port: 5432,
	}

	// Initially false
	if b.Healthy.Load() {
		t.Error("Healthy should be false initially")
	}

	// Set to true
	b.Healthy.Store(true)
	if !b.Healthy.Load() {
		t.Error("Healthy should be true after Store(true)")
	}
}

func TestServerConn_LastUsedAt(t *testing.T) {
	sc := &ServerConn{
		id: 1,
	}

	// Set last used time
	now := time.Now()
	sc.lastUsedAt.Store(now)

	lastUsed := sc.lastUsedAt.Load().(time.Time)
	if !lastUsed.Equal(now) {
		t.Error("lastUsedAt should be the stored time")
	}
}

func TestServerConn_Capabilities(t *testing.T) {
	sc := &ServerConn{
		id:           1,
		capabilities: 0x12345678,
	}

	if sc.capabilities != 0x12345678 {
		t.Errorf("capabilities = 0x%x, want 0x12345678", sc.capabilities)
	}
}

func TestPool_ClientCount(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
			MaxClientConnections: 100,
		},
	}

	p, err := NewPool(cfg, nil, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Use Stats() to get client connections
	stats := p.Stats()
	if stats.ClientConnections != 0 {
		t.Errorf("ClientConnections = %d, want 0", stats.ClientConnections)
	}
}

func TestPool_ServerConnCount(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
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

	p, err := NewPool(cfg, nil, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Use Stats() to get server connections
	stats := p.Stats()
	if stats.ServerConnections != 0 {
		t.Errorf("ServerConnections = %d, want 0", stats.ServerConnections)
	}
}

func TestPool_QueryCount(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
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

	p, err := NewPool(cfg, nil, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Initially 0
	stats := p.Stats()
	if stats.TotalQueries != 0 {
		t.Errorf("TotalQueries = %d, want 0", stats.TotalQueries)
	}

	// Increment
	p.IncrementQueryCount()
	stats = p.Stats()
	if stats.TotalQueries != 1 {
		t.Errorf("TotalQueries = %d, want 1", stats.TotalQueries)
	}
}

func TestPool_TxnCount(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
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

	p, err := NewPool(cfg, nil, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Initially 0
	stats := p.Stats()
	if stats.TotalTransactions != 0 {
		t.Errorf("TotalTransactions = %d, want 0", stats.TotalTransactions)
	}
}

func TestPool_Limits(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 100,
			MinServerConnections: 10,
			MaxClientConnections: 200,
		},
	}

	p, err := NewPool(cfg, nil, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	if p.config.Limits.MaxServerConnections != 100 {
		t.Errorf("MaxServerConnections = %d, want 100", p.config.Limits.MaxServerConnections)
	}
	if p.config.Limits.MinServerConnections != 10 {
		t.Errorf("MinServerConnections = %d, want 10", p.config.Limits.MinServerConnections)
	}
}

func TestParsePoolMode_Empty(t *testing.T) {
	mode, err := ParsePoolMode("")
	if err == nil {
		t.Error("ParsePoolMode(\"\") should return error")
	}
	if mode != ModeTransaction {
		t.Errorf("Mode = %v, want ModeTransaction", mode)
	}
}

func TestPoolMode_String_Unknown(t *testing.T) {
	mode := PoolMode(255)
	if mode.String() != "unknown" {
		t.Errorf("String = %q, want unknown", mode.String())
	}
}

func TestWaitQueue_MaxSize(t *testing.T) {
	wq := NewWaitQueue(5)

	if wq.maxSize != 5 {
		t.Errorf("maxSize = %d, want 5", wq.maxSize)
	}
}

func TestServerConn_CreatedAt(t *testing.T) {
	now := time.Now()
	sc := &ServerConn{
		id:        1,
		createdAt: now,
	}

	if !sc.createdAt.Equal(now) {
		t.Error("createdAt should be the set time")
	}
}

func TestBackend_LastCheck_Set(t *testing.T) {
	b := &Backend{
		Host: "127.0.0.1",
		Port: 5432,
	}

	now := time.Now()
	b.LastCheck = now

	if !b.LastCheck.Equal(now) {
		t.Error("LastCheck should be the set time")
	}
}

func TestHealthStatus_String(t *testing.T) {
	tests := []struct {
		status HealthStatus
		want   string
	}{
		{HealthUnknown, "unknown"},
		{HealthHealthy, "healthy"},
		{HealthUnhealthy, "unhealthy"},
		{HealthDegraded, "degraded"},
		{HealthStatus(255), "unknown"},
	}

	for _, tc := range tests {
		got := tc.status.String()
		if got != tc.want {
			t.Errorf("HealthStatus(%d).String() = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// PreparedStatementCache tests

func TestNewPreparedStatement(t *testing.T) {
	ps := NewPreparedStatement("stmt1", "SELECT $1", []int32{23})

	if ps.ID != "stmt1" {
		t.Errorf("ID = %q, want stmt1", ps.ID)
	}
	if ps.Query != "SELECT $1" {
		t.Errorf("Query = %q, want SELECT $1", ps.Query)
	}
	if len(ps.ParamTypes) != 1 || ps.ParamTypes[0] != 23 {
		t.Errorf("ParamTypes = %v, want [23]", ps.ParamTypes)
	}
	if ps.Hash == "" {
		t.Error("Hash should not be empty")
	}
	if ps.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestPreparedStatement_UpdateLastUsed(t *testing.T) {
	ps := NewPreparedStatement("stmt1", "SELECT 1", nil)

	// Get initial values
	initialUseCount := ps.UseCount.Load()
	initialLastUsed := ps.LastUsed.Load().(time.Time)

	// Wait a tiny bit to ensure time changes
	time.Sleep(10 * time.Millisecond)

	ps.UpdateLastUsed()

	if ps.UseCount.Load() != initialUseCount+1 {
		t.Errorf("UseCount = %d, want %d", ps.UseCount.Load(), initialUseCount+1)
	}

	newLastUsed := ps.LastUsed.Load().(time.Time)
	if !newLastUsed.After(initialLastUsed) {
		t.Error("LastUsed should be updated to a later time")
	}
}

func TestNewPreparedStatementCache_Defaults(t *testing.T) {
	psc := NewPreparedStatementCache(0, 0)

	if psc.maxSize != 1000 {
		t.Errorf("maxSize = %d, want 1000", psc.maxSize)
	}
	if psc.ttl != 30*time.Minute {
		t.Errorf("ttl = %v, want 30m", psc.ttl)
	}
}

func TestPreparedStatementCache_AddAndGet(t *testing.T) {
	psc := NewPreparedStatementCache(100, time.Hour)

	// Add a statement
	stmt := psc.Add("stmt1", "SELECT $1", []int32{23})
	if stmt == nil {
		t.Fatal("Add returned nil")
	}
	if stmt.ID != "stmt1" {
		t.Errorf("ID = %q, want stmt1", stmt.ID)
	}

	// Get by query
	got, ok := psc.Get("SELECT $1")
	if !ok {
		t.Error("Get should return true for existing statement")
	}
	if got.ID != "stmt1" {
		t.Errorf("Get ID = %q, want stmt1", got.ID)
	}

	// Get non-existent
	_, ok = psc.Get("SELECT $2")
	if ok {
		t.Error("Get should return false for non-existent statement")
	}
}

func TestPreparedStatementCache_GetByID(t *testing.T) {
	psc := NewPreparedStatementCache(100, time.Hour)

	// Add statement
	psc.Add("stmt1", "SELECT 1", nil)

	// Get by ID
	stmt, ok := psc.GetByID("stmt1")
	if !ok {
		t.Error("GetByID should return true")
	}
	if stmt.ID != "stmt1" {
		t.Errorf("ID = %q, want stmt1", stmt.ID)
	}

	// Get non-existent ID
	_, ok = psc.GetByID("nonexistent")
	if ok {
		t.Error("GetByID should return false for non-existent ID")
	}
}

func TestPreparedStatementCache_Remove(t *testing.T) {
	psc := NewPreparedStatementCache(100, time.Hour)

	// Add and remove
	psc.Add("stmt1", "SELECT 1", nil)
	psc.Remove("stmt1")

	// Should not exist anymore
	_, ok := psc.GetByID("stmt1")
	if ok {
		t.Error("GetByID should return false after Remove")
	}

	// Remove non-existent should not panic
	psc.Remove("nonexistent")
}

func TestPreparedStatementCache_AddDuplicate(t *testing.T) {
	psc := NewPreparedStatementCache(100, time.Hour)

	// Add same query twice with different IDs
	stmt1 := psc.Add("stmt1", "SELECT 1", nil)
	stmt2 := psc.Add("stmt2", "SELECT 1", nil)

	// Should return existing statement with updated ID
	if stmt1.ID != "stmt2" {
		t.Errorf("ID should be updated to stmt2, got %q", stmt1.ID)
	}
	if stmt1 != stmt2 {
		t.Error("Should return same statement object")
	}
}

func TestPreparedStatementCache_Eviction(t *testing.T) {
	psc := NewPreparedStatementCache(2, time.Hour)

	// Add 3 statements (max is 2)
	psc.Add("stmt1", "SELECT 1", nil)
	time.Sleep(10 * time.Millisecond)
	psc.Add("stmt2", "SELECT 2", nil)
	time.Sleep(10 * time.Millisecond)
	psc.Add("stmt3", "SELECT 3", nil)

	// First one should be evicted (LRU)
	_, ok := psc.GetByID("stmt1")
	if ok {
		t.Error("stmt1 should be evicted (LRU)")
	}

	// stmt2 and stmt3 should exist
	_, ok = psc.GetByID("stmt2")
	if !ok {
		t.Error("stmt2 should exist")
	}
	_, ok = psc.GetByID("stmt3")
	if !ok {
		t.Error("stmt3 should exist")
	}
}

func TestPreparedStatementCache_Stats(t *testing.T) {
	psc := NewPreparedStatementCache(100, time.Hour)

	// Initial stats
	stats := psc.Stats()
	if stats.Size != 0 {
		t.Errorf("Size = %d, want 0", stats.Size)
	}
	if stats.MaxSize != 100 {
		t.Errorf("MaxSize = %d, want 100", stats.MaxSize)
	}

	// Add statements
	psc.Add("stmt1", "SELECT 1", nil)
	psc.Add("stmt2", "SELECT 2", nil)

	// Get some
	psc.Get("SELECT 1")
	psc.Get("SELECT 1")
	psc.Get("nonexistent")

	stats = psc.Stats()
	if stats.Size != 2 {
		t.Errorf("Size = %d, want 2", stats.Size)
	}
	if stats.Hits != 2 {
		t.Errorf("Hits = %d, want 2", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Misses = %d, want 1", stats.Misses)
	}
	if stats.Added != 2 {
		t.Errorf("Added = %d, want 2", stats.Added)
	}
}

func TestPreparedStatementCache_GetByHash(t *testing.T) {
	psc := NewPreparedStatementCache(100, time.Hour)

	// Add statement
	stmt := psc.Add("stmt1", "SELECT 1", nil)

	// Get by hash
	got, ok := psc.GetByHash(stmt.Hash)
	if !ok {
		t.Error("GetByHash should return true")
	}
	if got.ID != "stmt1" {
		t.Errorf("ID = %q, want stmt1", got.ID)
	}

	// Non-existent hash
	_, ok = psc.GetByHash("nonexistent")
	if ok {
		t.Error("GetByHash should return false for non-existent")
	}
}

func TestGenerateStmtID(t *testing.T) {
	id1 := GenerateStmtID()
	id2 := GenerateStmtID()

	if id1 == id2 {
		t.Error("GenerateStmtID should return unique IDs")
	}
	if id1 == "" {
		t.Error("GenerateStmtID should not return empty string")
	}
}

// SessionPreparedStatements tests

func TestNewSessionPreparedStatements(t *testing.T) {
	psc := NewPreparedStatementCache(100, time.Hour)
	sps := NewSessionPreparedStatements(psc)

	if sps.cache != psc {
		t.Error("cache should be the same")
	}
	if sps.statements == nil {
		t.Error("statements map should be initialized")
	}
	if sps.serverIDs == nil {
		t.Error("serverIDs map should be initialized")
	}
}

func TestSessionPreparedStatements_Register(t *testing.T) {
	psc := NewPreparedStatementCache(100, time.Hour)
	sps := NewSessionPreparedStatements(psc)

	// Register a statement
	stmt := sps.Register("my_stmt", "SELECT $1", []int32{23})
	if stmt == nil {
		t.Fatal("Register returned nil")
	}

	// Get it back
	got, ok := sps.Get("my_stmt")
	if !ok {
		t.Error("Get should return true")
	}
	if got.ID != stmt.ID {
		t.Errorf("ID mismatch")
	}
}

func TestSessionPreparedStatements_ServerName(t *testing.T) {
	psc := NewPreparedStatementCache(100, time.Hour)
	sps := NewSessionPreparedStatements(psc)

	// Set server name
	sps.SetServerName("my_stmt", "server_stmt_1")

	// Get server name
	name, ok := sps.GetServerName("my_stmt")
	if !ok {
		t.Error("GetServerName should return true")
	}
	if name != "server_stmt_1" {
		t.Errorf("name = %q, want server_stmt_1", name)
	}

	// Get non-existent
	_, ok = sps.GetServerName("nonexistent")
	if ok {
		t.Error("GetServerName should return false for non-existent")
	}
}

func TestSessionPreparedStatements_Close(t *testing.T) {
	psc := NewPreparedStatementCache(100, time.Hour)
	sps := NewSessionPreparedStatements(psc)

	// Register and set server name
	sps.Register("stmt1", "SELECT 1", nil)
	sps.SetServerName("stmt1", "s1")

	// Close
	sps.Close()

	// Maps should be cleared
	_, ok := sps.Get("stmt1")
	if ok {
		t.Error("Get should return false after Close")
	}
}

// serverConnPool tests

func TestServerConnPool_acquire(t *testing.T) {
	pool := newServerConnPool(1, 5)

	// Empty pool returns nil
	conn := pool.acquire()
	if conn != nil {
		t.Error("acquire from empty pool should return nil")
	}

	// Add idle connection
	mockConn := &ServerConn{
		id:            1,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}
	pool.idle = append(pool.idle, mockConn)

	// Acquire should get it
	conn = pool.acquire()
	if conn == nil {
		t.Fatal("acquire should return connection")
	}
	if conn.id != 1 {
		t.Errorf("id = %d, want 1", conn.id)
	}
	if !conn.IsInUse() {
		t.Error("connection should be marked in use")
	}
	if pool.activeCount() != 1 {
		t.Errorf("activeCount = %d, want 1", pool.activeCount())
	}
	if pool.idleCount() != 0 {
		t.Errorf("idleCount = %d, want 0", pool.idleCount())
	}
}

func TestServerConnPool_release(t *testing.T) {
	pool := newServerConnPool(1, 5)

	mockConn := &ServerConn{
		id:            1,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}

	// Add to active
	pool.addActive(mockConn)

	// Release
	pool.release(mockConn)

	if pool.activeCount() != 0 {
		t.Errorf("activeCount = %d, want 0", pool.activeCount())
	}
	// Note: release with nil codec adds to idle without async reset
	if pool.idleCount() != 1 {
		t.Errorf("idleCount = %d, want 1", pool.idleCount())
	}
}

func TestServerConnPool_addActive(t *testing.T) {
	pool := newServerConnPool(1, 5)

	mockConn := &ServerConn{
		id:            1,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}

	pool.addActive(mockConn)

	if pool.activeCount() != 1 {
		t.Errorf("activeCount = %d, want 1", pool.activeCount())
	}
	if !mockConn.IsInUse() {
		t.Error("connection should be in use")
	}
}

func TestServerConnPool_remove(t *testing.T) {
	pool := newServerConnPool(1, 5)

	mockConn := &ServerConn{
		id:            1,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}

	// Add to active and idle
	pool.addActive(mockConn)
	pool.release(mockConn)

	// Remove from active (should also remove from idle)
	pool.remove(mockConn)

	if pool.size() != 0 {
		t.Errorf("size = %d, want 0", pool.size())
	}
}

func TestServerConnPool_size(t *testing.T) {
	pool := newServerConnPool(1, 5)

	if pool.size() != 0 {
		t.Errorf("size = %d, want 0", pool.size())
	}

	conn1 := &ServerConn{id: 1, preparedStmts: make(map[string]bool), paramStatus: make(map[string]string)}
	conn2 := &ServerConn{id: 2, preparedStmts: make(map[string]bool), paramStatus: make(map[string]string)}

	pool.addActive(conn1)
	pool.idle = append(pool.idle, conn2)

	if pool.size() != 2 {
		t.Errorf("size = %d, want 2", pool.size())
	}
}

func TestServerConnPool_idleCount(t *testing.T) {
	pool := newServerConnPool(1, 5)

	if pool.idleCount() != 0 {
		t.Errorf("idleCount = %d, want 0", pool.idleCount())
	}

	conn := &ServerConn{id: 1, preparedStmts: make(map[string]bool), paramStatus: make(map[string]string)}
	pool.idle = append(pool.idle, conn)

	if pool.idleCount() != 1 {
		t.Errorf("idleCount = %d, want 1", pool.idleCount())
	}
}

func TestServerConnPool_activeCount(t *testing.T) {
	pool := newServerConnPool(1, 5)

	if pool.activeCount() != 0 {
		t.Errorf("activeCount = %d, want 0", pool.activeCount())
	}

	conn := &ServerConn{id: 1, preparedStmts: make(map[string]bool), paramStatus: make(map[string]string)}
	pool.addActive(conn)

	if pool.activeCount() != 1 {
		t.Errorf("activeCount = %d, want 1", pool.activeCount())
	}
}

// WaitQueue tests

func TestWaitQueue_Wait_Timeout(t *testing.T) {
	wq := NewWaitQueue(100)

	ctx := context.Background()
	_, err := wq.Wait(ctx, 10*time.Millisecond)

	if err == nil {
		t.Error("Wait should return error on timeout")
	}
}

func TestWaitQueue_Wait_QueueFull(t *testing.T) {
	wq := NewWaitQueue(1)

	// First waiter
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	go func() {
		wq.Wait(ctx1, 5*time.Second)
	}()

	// Give it time to register
	time.Sleep(10 * time.Millisecond)

	// Second waiter should fail (queue full)
	ctx2 := context.Background()
	_, err := wq.Wait(ctx2, 10*time.Millisecond)

	if err == nil || err.Error() != "connection queue full (max 1)" {
		t.Errorf("expected queue full error, got: %v", err)
	}
}

func TestWaitQueue_Signal_NoWaiters(t *testing.T) {
	wq := NewWaitQueue(100)

	conn := &ServerConn{id: 1}
	signaled := wq.Signal(conn)

	if signaled {
		t.Error("Signal should return false when no waiters")
	}
}

func TestWaitQueue_Signal_WithWaiter(t *testing.T) {
	wq := NewWaitQueue(100)

	conn := &ServerConn{id: 1}

	// Start waiter
	ctx := context.Background()
	resultCh := make(chan *ServerConn, 1)
	errCh := make(chan error, 1)

	go func() {
		got, err := wq.Wait(ctx, 5*time.Second)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- got
	}()

	// Give waiter time to register
	time.Sleep(10 * time.Millisecond)

	// Signal
	signaled := wq.Signal(conn)
	if !signaled {
		t.Error("Signal should return true when waiter exists")
	}

	// Check result
	select {
		case got := <-resultCh:
			if got.id != 1 {
				t.Errorf("got id = %d, want 1", got.id)
			}
		case err := <-errCh:
			t.Fatalf("Wait error: %v", err)
		case <-time.After(100 * time.Millisecond):
			t.Error("timeout waiting for result")
	}
}

// Resetter tests

func TestNewResetterRegistry(t *testing.T) {
	registry := NewResetterRegistry()

	if registry.resetters == nil {
		t.Fatal("resetters map should be initialized")
	}

	// Check all protocols are registered
	protocols := []common.Protocol{
		common.ProtocolPostgreSQL,
		common.ProtocolMySQL,
		common.ProtocolMSSQL,
	}

	for _, p := range protocols {
		resetter, ok := registry.Get(p)
		if !ok {
			t.Errorf("protocol %v should be registered", p)
			continue
		}
		if resetter.Protocol() != p {
			t.Errorf("resetter protocol = %v, want %v", resetter.Protocol(), p)
		}
	}
}

func TestPostgreSQLResetter_Protocol(t *testing.T) {
	resetter := &PostgreSQLResetter{}
	if resetter.Protocol() != common.ProtocolPostgreSQL {
		t.Errorf("Protocol = %v, want PostgreSQL", resetter.Protocol())
	}
}

func TestMySQLResetter_Protocol(t *testing.T) {
	resetter := &MySQLResetter{}
	if resetter.Protocol() != common.ProtocolMySQL {
		t.Errorf("Protocol = %v, want MySQL", resetter.Protocol())
	}
}

func TestMSSQLResetter_Protocol(t *testing.T) {
	resetter := &MSSQLResetter{}
	if resetter.Protocol() != common.ProtocolMSSQL {
		t.Errorf("Protocol = %v, want MSSQL", resetter.Protocol())
	}
}

// SmartResetter tests

func TestDefaultSmartResetOptions(t *testing.T) {
	opts := DefaultSmartResetOptions()

	if !opts.TrackSessionVars {
		t.Error("TrackSessionVars should be true")
	}
	if !opts.TrackTempTables {
		t.Error("TrackTempTables should be true")
	}
	if !opts.TrackPreparedStmts {
		t.Error("TrackPreparedStmts should be true")
	}
	if !opts.MinimizeRoundTrips {
		t.Error("MinimizeRoundTrips should be true")
	}
}

func TestNewSmartResetter(t *testing.T) {
	opts := DefaultSmartResetOptions()
	sr := NewSmartResetter(opts)

	if sr.options != opts {
		t.Error("options should match")
	}
	if sr.state.SessionVarsModified == nil {
		t.Error("SessionVarsModified should be initialized")
	}
	if sr.state.PreparedStmts == nil {
		t.Error("PreparedStmts should be initialized")
	}
}

func TestSmartResetter_MarkSessionVarModified(t *testing.T) {
	opts := DefaultSmartResetOptions()
	sr := NewSmartResetter(opts)

	sr.MarkSessionVarModified("search_path", "public")

	if sr.state.SessionVarsModified["search_path"] != "public" {
		t.Error("SessionVarsModified should contain search_path")
	}
}

func TestSmartResetter_MarkTempTableCreated(t *testing.T) {
	opts := DefaultSmartResetOptions()
	sr := NewSmartResetter(opts)

	sr.MarkTempTableCreated("temp_table_1")

	if len(sr.state.TempTablesCreated) != 1 {
		t.Errorf("TempTablesCreated length = %d, want 1", len(sr.state.TempTablesCreated))
	}
}

func TestSmartResetter_MarkPreparedStmt(t *testing.T) {
	opts := DefaultSmartResetOptions()
	sr := NewSmartResetter(opts)

	sr.MarkPreparedStmt("stmt1")

	if !sr.state.PreparedStmts["stmt1"] {
		t.Error("PreparedStmts should contain stmt1")
	}
}

func TestSmartResetter_NeedsReset(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*SmartResetter)
		expected bool
	}{
		{
			name:     "empty state",
			setup:    func(sr *SmartResetter) {},
			expected: false,
		},
		{
			name: "in transaction",
			setup: func(sr *SmartResetter) {
				sr.state.InTransaction = true
			},
			expected: true,
		},
		{
			name: "modified session vars",
			setup: func(sr *SmartResetter) {
				sr.state.SessionVarsModified["x"] = "y"
			},
			expected: true,
		},
		{
			name: "created temp tables",
			setup: func(sr *SmartResetter) {
				sr.state.TempTablesCreated = []string{"t1"}
			},
			expected: true,
		},
		{
			name: "prepared stmts without minimize",
			setup: func(sr *SmartResetter) {
				sr.state.PreparedStmts["s1"] = true
				sr.options.MinimizeRoundTrips = false
			},
			expected: true,
		},
		{
			name: "prepared stmts with minimize",
			setup: func(sr *SmartResetter) {
				sr.state.PreparedStmts["s1"] = true
				sr.options.MinimizeRoundTrips = true
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultSmartResetOptions()
			sr := NewSmartResetter(opts)
			tc.setup(sr)

			got := sr.NeedsReset()
			if got != tc.expected {
				t.Errorf("NeedsReset() = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestSmartResetter_ResetState(t *testing.T) {
	opts := DefaultSmartResetOptions()
	sr := NewSmartResetter(opts)

	// Modify state
	sr.state.InTransaction = true
	sr.state.SessionVarsModified["x"] = "y"
	sr.state.TempTablesCreated = []string{"t1"}
	sr.state.PreparedStmts["s1"] = true

	// Reset
	sr.ResetState()

	if sr.state.InTransaction {
		t.Error("InTransaction should be false")
	}
	if len(sr.state.SessionVarsModified) != 0 {
		t.Error("SessionVarsModified should be empty")
	}
	if len(sr.state.TempTablesCreated) != 0 {
		t.Error("TempTablesCreated should be empty")
	}
	if len(sr.state.PreparedStmts) != 0 {
		t.Error("PreparedStmts should be empty")
	}
}

func TestSmartResetter_GetResetSQL(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*SmartResetter)
		expected []string
	}{
		{
			name:     "no reset needed",
			setup:    func(sr *SmartResetter) {},
			expected: nil,
		},
		{
			name: "reset session vars",
			setup: func(sr *SmartResetter) {
				sr.state.SessionVarsModified["x"] = "y"
			},
			expected: []string{"RESET ALL"},
		},
		{
			name: "drop temp tables",
			setup: func(sr *SmartResetter) {
				sr.state.TempTablesCreated = []string{"t1", "t2"}
			},
			expected: []string{
				"DROP TABLE IF EXISTS t1",
				"DROP TABLE IF EXISTS t2",
			},
		},
		{
			name: "deallocate stmts without minimize",
			setup: func(sr *SmartResetter) {
				sr.state.PreparedStmts["s1"] = true
				sr.options.MinimizeRoundTrips = false
			},
			expected: []string{"DEALLOCATE ALL"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultSmartResetOptions()
			sr := NewSmartResetter(opts)
			tc.setup(sr)

			got := sr.GetResetSQL()
			if len(got) != len(tc.expected) {
				t.Errorf("GetResetSQL() = %v, want %v", got, tc.expected)
				return
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("GetResetSQL()[%d] = %q, want %q", i, got[i], tc.expected[i])
				}
			}
		})
	}
}

// Additional Pool tests

func TestPool_TryIncrementClientCount_Race(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "race-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Concurrent increments
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			pool.TryIncrementClientCount(5)
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// Some should have succeeded, some failed
	stats := pool.Stats()
	if stats.ClientConnections > 5 {
		t.Errorf("ClientConnections = %d, should not exceed 5", stats.ClientConnections)
	}
}

func TestPool_SelectBackendWithFallback(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "fallback-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark primary as healthy
	pool.primary.Healthy.Store(true)

	// Should select primary
	backend := pool.selectBackendWithFallback()
	if backend == nil {
		t.Fatal("should select a backend")
	}
	if backend.Role != "primary" {
		t.Errorf("should select primary, got %s", backend.Role)
	}

	// Mark primary unhealthy
	pool.primary.Healthy.Store(false)

	// Mark replica healthy
	for _, b := range pool.backends {
		if b.Role == "replica" {
			b.Healthy.Store(true)
		}
	}

	// Should select replica
	backend = pool.selectBackendWithFallback()
	if backend == nil {
		t.Fatal("should select a backend")
	}
	if backend.Role != "replica" {
		t.Errorf("should select replica, got %s", backend.Role)
	}

	// Mark all unhealthy and draining
	for _, b := range pool.backends {
		b.Healthy.Store(false)
		b.Draining.Store(true)
	}

	// Should return nil
	backend = pool.selectBackendWithFallback()
	if backend != nil {
		t.Error("should return nil when all draining")
	}
}

func TestPool_SelectBackend(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "select-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "backend1", Port: 5432, Role: "primary"},
				{Host: "backend2", Port: 5433, Role: "replica"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark first healthy
	pool.backends[0].Healthy.Store(true)

	// Should select first healthy
	backend := pool.selectBackend()
	if backend == nil {
		t.Fatal("should select a backend")
	}
	if backend.Host != "backend1" {
		t.Errorf("should select backend1, got %s", backend.Host)
	}

	// Mark all unhealthy
	for _, b := range pool.backends {
		b.Healthy.Store(false)
	}

	// Should fallback to first
	backend = pool.selectBackend()
	if backend == nil {
		t.Fatal("should fallback to first backend")
	}
	if backend.Host != "backend1" {
		t.Errorf("should fallback to backend1, got %s", backend.Host)
	}
}

func TestPool_GetCachedResult_NoCache(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "no-cache-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Without cache, should return false
	result, hit := pool.GetCachedResult("SELECT 1", nil)
	if hit {
		t.Error("should not hit without cache")
	}
	if result != nil {
		t.Error("result should be nil")
	}
}

func TestPool_SetCachedResult_NoCache(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "no-cache-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Without cache, should return nil
	err = pool.SetCachedResult("SELECT 1", nil, []byte("data"), time.Minute)
	if err != nil {
		t.Errorf("should return nil without cache, got: %v", err)
	}
}

func TestPool_InvalidateCache_NoCache(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "no-cache-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	codec := &MockCodec{}

	pool, err := NewPool(cfg, codec, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Should not panic
	pool.InvalidateCache("table1")
}

// TransactionManager tests

func TestNewTransactionManager(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(0, 0, log)

	if tm == nil {
		t.Fatal("NewTransactionManager returned nil")
	}
	if tm.timeout != 30*time.Minute {
		t.Errorf("timeout = %v, want 30m", tm.timeout)
	}
	if tm.idleTimeout != 5*time.Minute {
		t.Errorf("idleTimeout = %v, want 5m", tm.idleTimeout)
	}

	tm.Stop()
}

func TestNewTransactionManager_CustomTimeouts(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Hour, 10*time.Minute, log)

	if tm.timeout != time.Hour {
		t.Errorf("timeout = %v, want 1h", tm.timeout)
	}
	if tm.idleTimeout != 10*time.Minute {
		t.Errorf("idleTimeout = %v, want 10m", tm.idleTimeout)
	}

	tm.Stop()
}

func TestTransactionManager_Register(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Hour, time.Minute, log)
	defer tm.Stop()

	info := tm.Register(1, 100)

	if info == nil {
		t.Fatal("Register returned nil")
	}
	if info.SessionID != 1 {
		t.Errorf("SessionID = %d, want 1", info.SessionID)
	}
	if info.ServerConnID != 100 {
		t.Errorf("ServerConnID = %d, want 100", info.ServerConnID)
	}
	if info.Status != TxnActive {
		t.Errorf("Status = %v, want TxnActive", info.Status)
	}
}

func TestTransactionManager_Get(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Hour, time.Minute, log)
	defer tm.Stop()

	info := tm.Register(1, 100)

	// Get existing
	got := tm.Get(info.ID)
	if got == nil {
		t.Fatal("Get returned nil for existing transaction")
	}
	if got.ID != info.ID {
		t.Errorf("ID = %d, want %d", got.ID, info.ID)
	}

	// Get non-existent
	got = tm.Get(99999)
	if got != nil {
		t.Error("Get should return nil for non-existent transaction")
	}
}

func TestTransactionManager_Unregister(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Hour, time.Minute, log)
	defer tm.Stop()

	info := tm.Register(1, 100)
	tm.Unregister(info.ID)

	// Should be gone
	got := tm.Get(info.ID)
	if got != nil {
		t.Error("Get should return nil after Unregister")
	}
}

func TestTransactionManager_UpdateActivity(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Hour, time.Minute, log)
	defer tm.Stop()

	info := tm.Register(1, 100)
	oldActivity := info.LastActivity

	time.Sleep(10 * time.Millisecond)
	tm.UpdateActivity(info.ID)

	if !info.LastActivity.After(oldActivity) {
		t.Error("LastActivity should be updated")
	}

	// Update non-existent should not panic
	tm.UpdateActivity(99999)
}

func TestTransactionManager_IncrementQueryCount(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Hour, time.Minute, log)
	defer tm.Stop()

	info := tm.Register(1, 100)

	tm.IncrementQueryCount(info.ID)
	tm.IncrementQueryCount(info.ID)

	if info.QueryCount.Load() != 2 {
		t.Errorf("QueryCount = %d, want 2", info.QueryCount.Load())
	}

	// Increment non-existent should not panic
	tm.IncrementQueryCount(99999)
}

func TestTransactionManager_SetStatus(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Hour, time.Minute, log)
	defer tm.Stop()

	info := tm.Register(1, 100)

	tests := []TransactionStatus{TxnIdle, TxnAborted, TxnCommitted}
	for _, status := range tests {
		tm.SetStatus(info.ID, status)
		if info.Status != status {
			t.Errorf("Status = %v, want %v", info.Status, status)
		}
	}

	// Set status for non-existent should not panic
	tm.SetStatus(99999, TxnAborted)
}

func TestTransactionManager_GetActiveCount(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Hour, time.Minute, log)
	defer tm.Stop()

	// Initially 0
	if tm.GetActiveCount() != 0 {
		t.Errorf("GetActiveCount = %d, want 0", tm.GetActiveCount())
	}

	// Register active
	info1 := tm.Register(1, 100)
	info2 := tm.Register(2, 101)

	if tm.GetActiveCount() != 2 {
		t.Errorf("GetActiveCount = %d, want 2", tm.GetActiveCount())
	}

	// Set one to idle
	tm.SetStatus(info1.ID, TxnIdle)
	if tm.GetActiveCount() != 1 {
		t.Errorf("GetActiveCount = %d, want 1", tm.GetActiveCount())
	}

	// Set one to committed
	tm.SetStatus(info2.ID, TxnCommitted)
	if tm.GetActiveCount() != 0 {
		t.Errorf("GetActiveCount = %d, want 0", tm.GetActiveCount())
	}
}

func TestTransactionManager_GetStats(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Hour, time.Minute, log)
	defer tm.Stop()

	// Initially all zero
	stats := tm.GetStats()
	if stats.TotalCount != 0 {
		t.Errorf("TotalCount = %d, want 0", stats.TotalCount)
	}

	// Register and set statuses
	tm.Register(1, 100)
	info2 := tm.Register(2, 101)
	info3 := tm.Register(3, 102)

	tm.SetStatus(info2.ID, TxnAborted)
	tm.SetStatus(info3.ID, TxnCommitted)

	stats = tm.GetStats()
	if stats.TotalCount != 3 {
		t.Errorf("TotalCount = %d, want 3", stats.TotalCount)
	}
	if stats.ActiveCount != 1 {
		t.Errorf("ActiveCount = %d, want 1", stats.ActiveCount)
	}
	if stats.AbortedCount != 1 {
		t.Errorf("AbortedCount = %d, want 1", stats.AbortedCount)
	}
	if stats.CommittedCount != 1 {
		t.Errorf("CommittedCount = %d, want 1", stats.CommittedCount)
	}
}

// DeadlockDetector tests

func TestNewDeadlockDetector(t *testing.T) {
	log, _ := logger.New("error", "json")
	dd := NewDeadlockDetector(0, log)

	if dd == nil {
		t.Fatal("NewDeadlockDetector returned nil")
	}
	if dd.timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", dd.timeout)
	}
}

func TestNewDeadlockDetector_CustomTimeout(t *testing.T) {
	log, _ := logger.New("error", "json")
	dd := NewDeadlockDetector(time.Minute, log)

	if dd.timeout != time.Minute {
		t.Errorf("timeout = %v, want 1m", dd.timeout)
	}
}

func TestDeadlockDetector_AddWait(t *testing.T) {
	log, _ := logger.New("error", "json")
	dd := NewDeadlockDetector(time.Minute, log)

	// Add wait
	dd.AddWait(1, 2)

	sessions := dd.GetWaitingSessions(1)
	if len(sessions) != 1 || sessions[0] != 2 {
		t.Errorf("GetWaitingSessions = %v, want [2]", sessions)
	}
}

func TestDeadlockDetector_AddWait_Multiple(t *testing.T) {
	log, _ := logger.New("error", "json")
	dd := NewDeadlockDetector(time.Minute, log)

	// Session 1 waits for 2 and 3
	dd.AddWait(1, 2)
	dd.AddWait(1, 3)

	sessions := dd.GetWaitingSessions(1)
	if len(sessions) != 2 {
		t.Errorf("GetWaitingSessions length = %d, want 2", len(sessions))
	}
}

func TestDeadlockDetector_RemoveWait(t *testing.T) {
	log, _ := logger.New("error", "json")
	dd := NewDeadlockDetector(time.Minute, log)

	dd.AddWait(1, 2)
	dd.AddWait(1, 3)

	dd.RemoveWait(1, 2)

	sessions := dd.GetWaitingSessions(1)
	if len(sessions) != 1 || sessions[0] != 3 {
		t.Errorf("GetWaitingSessions = %v, want [3]", sessions)
	}

	// Remove last
	dd.RemoveWait(1, 3)
	sessions = dd.GetWaitingSessions(1)
	if len(sessions) != 0 {
		t.Errorf("GetWaitingSessions length = %d, want 0", len(sessions))
	}
}

func TestDeadlockDetector_ClearSession(t *testing.T) {
	log, _ := logger.New("error", "json")
	dd := NewDeadlockDetector(time.Minute, log)

	// Create wait graph: 1 waits for 2, 3 waits for 1
	dd.AddWait(1, 2)
	dd.AddWait(3, 1)

	// Clear session 1
	dd.ClearSession(1)

	// Session 1 should have no waits
	sessions := dd.GetWaitingSessions(1)
	if len(sessions) != 0 {
		t.Errorf("GetWaitingSessions for 1 length = %d, want 0", len(sessions))
	}

	// Session 3 should no longer wait for 1
	sessions = dd.GetWaitingSessions(3)
	if len(sessions) != 0 {
		t.Errorf("GetWaitingSessions for 3 length = %d, want 0", len(sessions))
	}
}

func TestDeadlockDetector_DetectCycle_NoCycle(t *testing.T) {
	log, _ := logger.New("error", "json")
	dd := NewDeadlockDetector(time.Minute, log)

	// Linear wait: 1 -> 2 -> 3 (no cycle)
	dd.AddWait(1, 2)
	dd.AddWait(2, 3)

	// Should not detect cycle
	if dd.detectCycle() {
		t.Error("should not detect cycle in linear graph")
	}
}

func TestDeadlockDetector_DetectCycle_SimpleCycle(t *testing.T) {
	log, _ := logger.New("error", "json")
	dd := NewDeadlockDetector(time.Minute, log)

	// Cycle: 1 -> 2 -> 1
	dd.AddWait(1, 2)
	dd.AddWait(2, 1)

	// Should detect cycle
	if !dd.detectCycle() {
		t.Error("should detect cycle")
	}
}

func TestDeadlockDetector_DetectCycle_LongCycle(t *testing.T) {
	log, _ := logger.New("error", "json")
	dd := NewDeadlockDetector(time.Minute, log)

	// Long cycle: 1 -> 2 -> 3 -> 4 -> 1
	dd.AddWait(1, 2)
	dd.AddWait(2, 3)
	dd.AddWait(3, 4)
	dd.AddWait(4, 1)

	// Should detect cycle
	if !dd.detectCycle() {
		t.Error("should detect long cycle")
	}
}

func TestDeadlockDetector_GetWaitingSessions_NotFound(t *testing.T) {
	log, _ := logger.New("error", "json")
	dd := NewDeadlockDetector(time.Minute, log)

	sessions := dd.GetWaitingSessions(999)
	if len(sessions) != 0 {
		t.Errorf("GetWaitingSessions length = %d, want 0", len(sessions))
	}
}

// TransactionStatus tests

func TestTransactionStatus_Values(t *testing.T) {
	// Ensure values are as expected
	if TxnActive != 0 {
		t.Errorf("TxnActive = %d, want 0", TxnActive)
	}
	if TxnIdle != 1 {
		t.Errorf("TxnIdle = %d, want 1", TxnIdle)
	}
	if TxnAborted != 2 {
		t.Errorf("TxnAborted = %d, want 2", TxnAborted)
	}
	if TxnCommitted != 3 {
		t.Errorf("TxnCommitted = %d, want 3", TxnCommitted)
	}
}

// ContextWithTransactionTimeout tests

func TestContextWithTransactionTimeout_ZeroTimeout(t *testing.T) {
	ctx := context.Background()
	newCtx, cancel := ContextWithTransactionTimeout(ctx, 0)

	if newCtx != ctx {
		t.Error("should return parent context when timeout is 0")
	}
	cancel()
}

func TestContextWithTransactionTimeout_WithTimeout(t *testing.T) {
	ctx := context.Background()
	newCtx, cancel := ContextWithTransactionTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	if newCtx == ctx {
		t.Error("should return new context with timeout")
	}

	// Wait for timeout
	time.Sleep(200 * time.Millisecond)

	if newCtx.Err() != context.DeadlineExceeded {
		t.Errorf("context error = %v, want DeadlineExceeded", newCtx.Err())
	}
}

// Tests for uncovered functions

func TestNewBackend(t *testing.T) {
	b := NewBackend("localhost", 5432, "primary", 100)
	if b == nil {
		t.Fatal("NewBackend returned nil")
	}
	if b.Host != "localhost" {
		t.Errorf("Host = %q, want localhost", b.Host)
	}
	if b.Port != 5432 {
		t.Errorf("Port = %d, want 5432", b.Port)
	}
	if b.Role != "primary" {
		t.Errorf("Role = %q, want primary", b.Role)
	}
	if b.Weight != 100 {
		t.Errorf("Weight = %d, want 100", b.Weight)
	}
}

func TestManager_UpdatePoolConfig(t *testing.T) {
	log, _ := logger.New("error", "json")
	mgr := NewManager(log)

	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 5432,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	err := mgr.CreatePool(cfg)
	if err != nil {
		t.Fatalf("CreatePool failed: %v", err)
	}

	// Update config with safe changes
	newCfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 5432,
		},
		Limits: config.LimitConfig{
			MaxClientConnections: 5000,
			MaxServerConnections: 200,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	err = mgr.UpdatePoolConfig("test", newCfg)
	if err != nil {
		t.Errorf("UpdatePoolConfig failed: %v", err)
	}
}

func TestManager_UpdatePoolConfig_NotFound(t *testing.T) {
	log, _ := logger.New("error", "json")
	mgr := NewManager(log)

	cfg := &config.PoolConfig{
		Name: "nonexistent",
		Body: "postgresql",
		Mode: "transaction",
	}

	err := mgr.UpdatePoolConfig("nonexistent", cfg)
	if err == nil {
		t.Error("UpdatePoolConfig should fail for non-existent pool")
	}
}

func TestPool_UpdateConfig(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 5432,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Valid config update (safe fields only)
	newCfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 5432,
		},
		Limits: config.LimitConfig{
			MaxClientConnections: 2000,
			MaxServerConnections: 100,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	err = p.UpdateConfig(newCfg)
	if err != nil {
		t.Errorf("UpdateConfig failed: %v", err)
	}

	if p.config.Limits.MaxClientConnections != 2000 {
		t.Errorf("MaxClientConnections = %d, want 2000", p.config.Limits.MaxClientConnections)
	}
}

func TestPool_UpdateConfig_UnsafeChange(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 5432,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Try to change body (unsafe)
	newCfg := &config.PoolConfig{
		Name: "test",
		Body: "mysql", // Changed
		Mode: "transaction",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 5432,
		},
	}

	err = p.UpdateConfig(newCfg)
	if err == nil {
		t.Error("UpdateConfig should fail for unsafe changes")
	}
}

func TestPool_UpdateBackends(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Database: "testdb",
			Hosts: []config.BackendHost{
				{Host: "old", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Update backends
	newCfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Database: "testdb",
			Hosts: []config.BackendHost{
				{Host: "new", Port: 5433, Role: "primary"},
			},
		},
	}

	p.updateBackends(newCfg)

	if len(p.backends) != 1 {
		t.Errorf("backends length = %d, want 1", len(p.backends))
	}
}

func TestPool_StartHealthChecks(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Health: config.HealthConfig{
			CheckInterval: "100ms",
			CheckQuery:    "SELECT 1",
			MaxFailures:   3,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Should not panic
	p.StartHealthChecks(100 * time.Millisecond)

	// Clean up
	if p.healthTicker != nil {
		p.healthTicker.Stop()
	}
}

func TestPool_CancelDrain(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Cancel drain for non-draining backend should not panic
	p.CancelDrain("localhost:5432")
}

func TestPool_IsDraining(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Should return false for non-draining backend
	if p.IsDraining("localhost:5432") {
		t.Error("IsDraining should return false for non-draining backend")
	}
}

func TestTransactionManager_GetActiveTransactions(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Minute, time.Second, log)

	// Initially should be empty
	txns := tm.GetActiveTransactions()
	if len(txns) != 0 {
		t.Errorf("GetActiveTransactions length = %d, want 0", len(txns))
	}
}

func TestTransactionManager_GetTransactionDetails(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Minute, time.Second, log)

	// Should return nil for non-existent transaction
	info := tm.GetTransactionDetails(999)
	if info != nil {
		t.Errorf("GetTransactionDetails should return nil for non-existent, got %v", info)
	}
}

func TestTransactionStatus_String(t *testing.T) {
	tests := []struct {
		status   TransactionStatus
		expected string
	}{
		{TxnActive, "active"},
		{TxnIdle, "idle"},
		{TxnAborted, "aborted"},
		{TxnCommitted, "committed"},
		{TransactionStatus(99), "unknown"},
	}

	for _, tt := range tests {
		result := tt.status.String()
		if result != tt.expected {
			t.Errorf("TransactionStatus(%d).String() = %q, want %q", tt.status, result, tt.expected)
		}
	}
}

func TestPool_updateBackendLists(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Clear and re-add backends
	p.backends = []*Backend{
		{Host: "primary", Port: 5432, Role: "primary"},
		{Host: "replica1", Port: 5433, Role: "replica"},
	}

	p.updateBackendLists()

	if p.primary == nil {
		t.Error("primary should not be nil")
	}
	if len(p.replicas) != 1 {
		t.Errorf("replicas length = %d, want 1", len(p.replicas))
	}
}

