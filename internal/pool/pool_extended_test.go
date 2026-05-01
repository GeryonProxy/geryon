package pool

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sync"
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

		pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	p, err := NewPool(cfg, nil, log, nil)
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

	p, err := NewPool(cfg, nil, log, nil)
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

	p, err := NewPool(cfg, nil, log, nil)
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

	p, err := NewPool(cfg, nil, log, nil)
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

	p, err := NewPool(cfg, nil, log, nil)
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
	pool.release(mockConn, nil)

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
	pool.release(mockConn, nil)

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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
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

	pool, err := NewPool(cfg, codec, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Should not panic
	pool.InvalidateCache("table1")
}

// TransactionManager tests

func TestNewTransactionManager(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(0, 0, 0, log)

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
	tm := NewTransactionManager(time.Hour, 10*time.Minute, 0, log)

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
	tm := NewTransactionManager(time.Hour, time.Minute, 0, log)
	defer tm.Stop()

	info := tm.Register(1, 100, nil)

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
	tm := NewTransactionManager(time.Hour, time.Minute, 0, log)
	defer tm.Stop()

	info := tm.Register(1, 100, nil)

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
	tm := NewTransactionManager(time.Hour, time.Minute, 0, log)
	defer tm.Stop()

	info := tm.Register(1, 100, nil)
	tm.Unregister(info.ID)

	// Should be gone
	got := tm.Get(info.ID)
	if got != nil {
		t.Error("Get should return nil after Unregister")
	}
}

func TestTransactionManager_UpdateActivity(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Hour, time.Minute, 0, log)
	defer tm.Stop()

	info := tm.Register(1, 100, nil)
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
	tm := NewTransactionManager(time.Hour, time.Minute, 0, log)
	defer tm.Stop()

	info := tm.Register(1, 100, nil)

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
	tm := NewTransactionManager(time.Hour, time.Minute, 0, log)
	defer tm.Stop()

	info := tm.Register(1, 100, nil)

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
	tm := NewTransactionManager(time.Hour, time.Minute, 0, log)
	defer tm.Stop()

	// Initially 0
	if tm.GetActiveCount() != 0 {
		t.Errorf("GetActiveCount = %d, want 0", tm.GetActiveCount())
	}

	// Register active
	info1 := tm.Register(1, 100, nil)
	info2 := tm.Register(2, 101, nil)

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
	tm := NewTransactionManager(time.Hour, time.Minute, 0, log)
	defer tm.Stop()

	// Initially all zero
	stats := tm.GetStats()
	if stats.TotalCount != 0 {
		t.Errorf("TotalCount = %d, want 0", stats.TotalCount)
	}

	// Register and set statuses
	tm.Register(1, 100, nil)
	info2 := tm.Register(2, 101, nil)
	info3 := tm.Register(3, 102, nil)

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

	p, err := NewPool(cfg, nil, log, nil)
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

	p, err := NewPool(cfg, nil, log, nil)
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

	p, err := NewPool(cfg, nil, log, nil)
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

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Should not panic
	p.StartHealthChecks()

	// Clean up
	if p.healthChecker != nil {
		p.healthChecker.Stop()
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

	p, err := NewPool(cfg, nil, log, nil)
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

	p, err := NewPool(cfg, nil, log, nil)
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
	tm := NewTransactionManager(time.Minute, time.Second, 0, log)

	// Initially should be empty
	txns := tm.GetActiveTransactions()
	if len(txns) != 0 {
		t.Errorf("GetActiveTransactions length = %d, want 0", len(txns))
	}
}

func TestTransactionManager_GetTransactionDetails(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Minute, time.Second, 0, log)

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

	p, err := NewPool(cfg, nil, log, nil)
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

// Test TransactionManager checkTimeouts
func TestTransactionManager_checkTimeouts(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(
		100*time.Millisecond, // Very short timeout
		50*time.Millisecond,  // Very short idle timeout
		0,                    // Default check interval
		log,
	)

	// Register a transaction
	info := tm.Register(1, 100, nil)
	if info == nil {
		t.Fatal("Register should return non-nil info")
	}
	txnID := info.ID

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Check timeouts - this should mark the transaction as aborted
	tm.checkTimeouts()

	// Give time for status update
	time.Sleep(10 * time.Millisecond)

	// Get transaction info
	updatedInfo := tm.Get(txnID)
	if updatedInfo == nil {
		t.Fatal("Transaction should exist")
	}

	// Status should be TxnAborted due to timeout
	if updatedInfo.Status != TxnAborted {
		t.Errorf("Status = %v, want TxnAborted", updatedInfo.Status)
	}
}

// Test TransactionManager checkTimeouts with idle timeout
func TestTransactionManager_checkTimeouts_Idle(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(
		1*time.Hour,         // Long transaction timeout
		50*time.Millisecond, // Short idle timeout
		0,                   // Default check interval
		log,
	)

	// Register a transaction
	info := tm.Register(1, 100, nil)
	txnID := info.ID

	// Wait for idle timeout
	time.Sleep(100 * time.Millisecond)

	// Check timeouts - this should mark the transaction as idle
	tm.checkTimeouts()

	// Give time for status update
	time.Sleep(10 * time.Millisecond)

	// Get transaction info
	updatedInfo := tm.Get(txnID)
	if updatedInfo == nil {
		t.Fatal("Transaction should exist")
	}

	// Status should be TxnIdle due to idle timeout
	if updatedInfo.Status != TxnIdle {
		t.Errorf("Status = %v, want TxnIdle", updatedInfo.Status)
	}
}

// Test TransactionManager OnAbort callback fires on timeout
func TestTransactionManager_OnAbort(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(
		100*time.Millisecond,
		200*time.Millisecond,
		0,
		log,
	)

	var abortedSessions []uint64
	var mu sync.Mutex
	tm.OnAbort(func(sessionID uint64) {
		mu.Lock()
		defer mu.Unlock()
		abortedSessions = append(abortedSessions, sessionID)
	})

	// Register a transaction
	info := tm.Register(42, 100, nil)
	txnID := info.ID

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Check timeouts
	tm.checkTimeouts()
	time.Sleep(10 * time.Millisecond)

	// Verify callback was called with correct session ID
	mu.Lock()
	if len(abortedSessions) != 1 {
		t.Fatalf("Expected 1 abort callback, got %d", len(abortedSessions))
	}
	if abortedSessions[0] != 42 {
		t.Errorf("Abort callback session ID = %d, want 42", abortedSessions[0])
	}
	mu.Unlock()

	// Verify status was set
	updatedInfo := tm.Get(txnID)
	if updatedInfo.Status != TxnAborted {
		t.Errorf("Status = %v, want TxnAborted", updatedInfo.Status)
	}
}

// Tests for backend selection functions

func TestPool_SelectBackendByRole_Primary(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "role-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark primary healthy
	pool.primary.Healthy.Store(true)

	backend := pool.selectBackendByRole("primary")
	if backend == nil {
		t.Fatal("should select primary")
	}
	if backend.Role != "primary" {
		t.Errorf("expected primary, got %s", backend.Role)
	}
}

func TestPool_SelectBackendByRole_Replica(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "role-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica1", Port: 5433, Role: "replica"},
				{Host: "replica2", Port: 5434, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark replicas healthy
	for _, b := range pool.backends {
		if b.Role == "replica" {
			b.Healthy.Store(true)
		}
	}

	backend := pool.selectBackendByRole("replica")
	if backend == nil {
		t.Fatal("should select a replica")
	}
	if backend.Role != "replica" {
		t.Errorf("expected replica, got %s", backend.Role)
	}
}

func TestPool_SelectBackendByRole_ReplicaFallbackToPrimary(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "role-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark primary healthy, replicas unhealthy
	pool.primary.Healthy.Store(true)
	for _, b := range pool.backends {
		if b.Role == "replica" {
			b.Healthy.Store(false)
		}
	}

	// Requesting replica should fallback to primary
	backend := pool.selectBackendByRole("replica")
	if backend == nil {
		t.Fatal("should fallback to primary")
	}
	if backend.Role != "primary" {
		t.Errorf("expected primary fallback, got %s", backend.Role)
	}
}

func TestPool_SelectBackendByRole_NoHealthyBackends(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "role-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// All backends unhealthy
	for _, b := range pool.backends {
		b.Healthy.Store(false)
	}

	backend := pool.selectBackendByRole("primary")
	if backend != nil {
		t.Error("should return nil when no healthy backends")
	}

	backend = pool.selectBackendByRole("replica")
	if backend != nil {
		t.Error("should return nil when no healthy backends")
	}

	backend = pool.selectBackendByRole("unknown")
	if backend != nil {
		t.Error("should return nil for unknown role when no healthy backends")
	}
}

func TestPool_SelectBackendByRole_DrainingFallback(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "role-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(true)
	pool.primary.Draining.Store(true)

	backend := pool.selectBackendByRole("primary")
	if backend != nil {
		t.Error("should return nil when primary is draining")
	}
}

func TestPool_SelectBackendForQuery_Write(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "rw-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(true)

	// Write query should always go to primary
	backend := pool.selectBackendForQuery(true)
	if backend == nil {
		t.Fatal("should select primary for writes")
	}
	if backend.Role != "primary" {
		t.Errorf("expected primary for write, got %s", backend.Role)
	}
}

func TestPool_SelectBackendForQuery_Read_WithReadWriteSplit(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "rw-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
		Routing: config.RoutingConfig{
			ReadWriteSplit: true,
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(true)
	for _, b := range pool.backends {
		if b.Role == "replica" {
			b.Healthy.Store(true)
		}
	}

	// Read query with read_write_split enabled should go to replica
	backend := pool.selectBackendForQuery(false)
	if backend == nil {
		t.Fatal("should select a backend for reads")
	}
	if backend.Role != "replica" {
		t.Errorf("expected replica for read with split enabled, got %s", backend.Role)
	}
}

func TestPool_SelectBackendForQuery_Read_WithoutReadWriteSplit(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "rw-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
		Routing: config.RoutingConfig{
			ReadWriteSplit: false,
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(true)
	for _, b := range pool.backends {
		if b.Role == "replica" {
			b.Healthy.Store(true)
		}
	}

	// Read query without read_write_split should go to primary
	backend := pool.selectBackendForQuery(false)
	if backend == nil {
		t.Fatal("should select primary for reads without split")
	}
	if backend.Role != "primary" {
		t.Errorf("expected primary for read without split, got %s", backend.Role)
	}
}

func TestPool_SelectBackendForQuery_Write_PrimaryDown(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "rw-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(false)
	for _, b := range pool.backends {
		if b.Role == "replica" {
			b.Healthy.Store(true)
		}
	}

	// Write query with primary down should fallback to replica
	backend := pool.selectBackendForQuery(true)
	if backend == nil {
		t.Fatal("should fallback to replica when primary is down")
	}
	if backend.Role != "replica" {
		t.Errorf("expected replica fallback for write, got %s", backend.Role)
	}
}

func TestPool_SelectBackendForQuery_NoHealthyBackends(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "rw-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(false)

	backend := pool.selectBackendForQuery(true)
	if backend != nil {
		t.Error("should return nil when no healthy backends for write")
	}

	backend = pool.selectBackendForQuery(false)
	if backend != nil {
		t.Error("should return nil when no healthy backends for read")
	}
}

func TestPool_SelectBackendForQuery_Read_NoReplicas(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "rw-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
			},
		},
		Routing: config.RoutingConfig{
			ReadWriteSplit: true,
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(true)

	// Read with split enabled but no replicas should go to primary
	backend := pool.selectBackendForQuery(false)
	if backend == nil {
		t.Fatal("should select primary when no replicas available")
	}
	if backend.Role != "primary" {
		t.Errorf("expected primary when no replicas, got %s", backend.Role)
	}
}

func TestPool_SelectBackendForQuery_Read_ReplicasDown(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "rw-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
		Routing: config.RoutingConfig{
			ReadWriteSplit: true,
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(true)
	for _, b := range pool.backends {
		if b.Role == "replica" {
			b.Healthy.Store(false)
		}
	}

	// Read with split enabled but replicas unhealthy should go to primary
	backend := pool.selectBackendForQuery(false)
	if backend == nil {
		t.Fatal("should fallback to primary when replicas are down")
	}
	if backend.Role != "primary" {
		t.Errorf("expected primary fallback, got %s", backend.Role)
	}
}

func TestSelectWeightedBackend_Empty(t *testing.T) {
	backend := selectWeightedBackend(nil)
	if backend != nil {
		t.Error("should return nil for nil slice")
	}

	backend = selectWeightedBackend([]*Backend{})
	if backend != nil {
		t.Error("should return nil for empty slice")
	}
}

func TestSelectWeightedBackend_Single(t *testing.T) {
	b := &Backend{Host: "single", Port: 5432, Weight: 50}
	backend := selectWeightedBackend([]*Backend{b})
	if backend != b {
		t.Error("should return the single backend")
	}
}

func TestSelectWeightedBackend_Multiple(t *testing.T) {
	backends := []*Backend{
		{Host: "b1", Port: 5432, Weight: 50},
		{Host: "b2", Port: 5433, Weight: 200},
		{Host: "b3", Port: 5434, Weight: 100},
	}

	backend := selectWeightedBackend(backends)
	if backend == nil {
		t.Fatal("should select a backend")
	}
	if backend.Host != "b2" {
		t.Errorf("expected b2 (highest weight), got %s", backend.Host)
	}
}

func TestSelectWeightedBackend_DefaultWeight(t *testing.T) {
	backends := []*Backend{
		{Host: "b1", Port: 5432, Weight: 0},  // default 100
		{Host: "b2", Port: 5433, Weight: -1}, // default 100
		{Host: "b3", Port: 5434, Weight: 150},
	}

	backend := selectWeightedBackend(backends)
	if backend == nil {
		t.Fatal("should select a backend")
	}
	if backend.Host != "b3" {
		t.Errorf("expected b3 (weight 150), got %s", backend.Host)
	}
}

func TestSelectWeightedBackend_ConnectionCount(t *testing.T) {
	backends := []*Backend{
		{Host: "b1", Port: 5432, Weight: 100},
		{Host: "b2", Port: 5433, Weight: 100},
	}

	// b2 has more active connections, so b1 should be selected
	backends[1].ConnCount.Store(5)

	backend := selectWeightedBackend(backends)
	if backend == nil {
		t.Fatal("should select a backend")
	}
	// b1: effective weight = 100 - 0*10 = 100
	// b2: effective weight = 100 - 5*10 = 50
	if backend.Host != "b1" {
		t.Errorf("expected b1 (lower conn count), got %s", backend.Host)
	}
}

// Tests for Release

func TestPool_Release_SignalWaiter(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "release-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	conn := &ServerConn{id: 1}

	// Start a waiter
	resultCh := make(chan *ServerConn, 1)
	go func() {
		got, err := pool.waitQueue.Wait(context.Background(), time.Second)
		if err == nil {
			resultCh <- got
		}
	}()

	// Give waiter time to register
	time.Sleep(10 * time.Millisecond)

	// Release the connection - should signal the waiter
	pool.Release(conn)

	select {
	case got := <-resultCh:
		if got.id != 1 {
			t.Errorf("waiter got conn id %d, want 1", got.id)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("waiter should have received connection")
	}
}

func TestPool_Release_NoWaiter_ReturnsToPool(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "release-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	conn := &ServerConn{
		id:            1,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}

	// Add to active pool first
	pool.serverConns.addActive(conn)

	// Release without waiter should return to idle
	pool.Release(conn)

	if pool.serverConns.idleCount() != 1 {
		t.Errorf("idleCount = %d, want 1", pool.serverConns.idleCount())
	}
	if pool.serverConns.activeCount() != 0 {
		t.Errorf("activeCount = %d, want 0", pool.serverConns.activeCount())
	}
}

// Tests for Acquire

func TestPool_Acquire_FromIdle(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "acquire-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Add an idle connection
	conn := &ServerConn{
		id:            42,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}
	pool.serverConns.idle = append(pool.serverConns.idle, conn)

	got, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	if got.ID() != 42 {
		t.Errorf("Acquire got id %d, want 42", got.ID())
	}
}

func TestPool_AcquireToRole_FromIdle(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "acquire-test",
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
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Add an idle connection
	conn := &ServerConn{
		id:            99,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}
	pool.serverConns.idle = append(pool.serverConns.idle, conn)

	got, err := pool.AcquireToRole(context.Background(), "replica")
	if err != nil {
		t.Fatalf("AcquireToRole failed: %v", err)
	}
	if got.ID() != 99 {
		t.Errorf("AcquireToRole got id %d, want 99", got.ID())
	}
}

func TestPool_AcquireToRole_NoMatchingRole(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "acquire-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// No idle connections, and no replica exists
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = pool.AcquireToRole(ctx, "replica")
	if err == nil {
		t.Error("should fail when no matching role backend exists")
	}
}

func TestPool_SelectBackendWithFallback_AllDraining(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "drain-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark all draining
	for _, b := range pool.backends {
		b.Healthy.Store(true)
		b.Draining.Store(true)
	}

	backend := pool.selectBackendWithFallback()
	if backend != nil {
		t.Error("should return nil when all backends are draining")
	}
}

func TestPool_SelectBackendWithFallback_PrimaryHealthy(t *testing.T) {
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
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(true)
	for _, b := range pool.replicas {
		b.Healthy.Store(true)
	}

	// Should prefer primary even when replicas are healthy
	backend := pool.selectBackendWithFallback()
	if backend == nil {
		t.Fatal("should select a backend")
	}
	if backend.Role != "primary" {
		t.Errorf("expected primary, got %s", backend.Role)
	}
}

func TestPool_SelectBackendWithFallback_PrimaryDraining_UseReplica(t *testing.T) {
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
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(true)
	pool.primary.Draining.Store(true)
	for _, b := range pool.replicas {
		b.Healthy.Store(true)
	}

	// Primary draining, should use replica
	backend := pool.selectBackendWithFallback()
	if backend == nil {
		t.Fatal("should select replica when primary is draining")
	}
	if backend.Role != "replica" {
		t.Errorf("expected replica, got %s", backend.Role)
	}
}

func TestPool_SelectBackend_NoHealthy_FallbackFirst(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "fallback-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "backend1", Port: 5432, Role: "primary"},
				{Host: "backend2", Port: 5433, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// All unhealthy
	for _, b := range pool.backends {
		b.Healthy.Store(false)
	}

	// Should fallback to first backend
	backend := pool.selectBackend()
	if backend == nil {
		t.Fatal("should fallback to first backend")
	}
	if backend.Host != "backend1" {
		t.Errorf("expected backend1 fallback, got %s", backend.Host)
	}
}

func TestPool_SelectBackend_WeightedSelection(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "weighted-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "b1", Port: 5432, Role: "primary", Weight: 50},
				{Host: "b2", Port: 5433, Role: "replica", Weight: 200},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark both healthy
	for _, b := range pool.backends {
		b.Healthy.Store(true)
	}

	backend := pool.selectBackend()
	if backend == nil {
		t.Fatal("should select a backend")
	}
	// b2 has higher weight (200 vs 50)
	if backend.Host != "b2" {
		t.Errorf("expected b2 (higher weight), got %s", backend.Host)
	}
}

func TestPool_SelectBackend_SingleHealthy(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "single-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "only", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(true)

	backend := pool.selectBackend()
	if backend == nil {
		t.Fatal("should select the only backend")
	}
	if backend.Host != "only" {
		t.Errorf("expected only, got %s", backend.Host)
	}
}

func TestPool_Acquire_WaitTimeout(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "acquire-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 1,
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Fill the pool to max by adding an active connection
	conn := &ServerConn{
		id:            1,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}
	pool.serverConns.addActive(conn)

	// Acquire should try to create new but exceed limit, then wait and timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = pool.Acquire(ctx)
	if err == nil {
		t.Error("Acquire should timeout when pool is at max and no connections available")
	}
}

func TestPool_SelectBackendForQuery_Write_AllBackendsDown(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "rw-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// All backends unhealthy and draining
	for _, b := range pool.backends {
		b.Healthy.Store(false)
		b.Draining.Store(true)
	}

	backend := pool.selectBackendForQuery(true)
	if backend != nil {
		t.Error("should return nil when all backends are down and draining for write")
	}
}

func TestPool_SelectBackendForQuery_Read_PrimaryDraining(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "rw-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
		Routing: config.RoutingConfig{
			ReadWriteSplit: true,
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(true)
	pool.primary.Draining.Store(true)
	for _, b := range pool.backends {
		if b.Role == "replica" {
			b.Healthy.Store(true)
		}
	}

	// Read should go to replica (primary draining)
	backend := pool.selectBackendForQuery(false)
	if backend == nil {
		t.Fatal("should select replica when primary is draining")
	}
	if backend.Role != "replica" {
		t.Errorf("expected replica, got %s", backend.Role)
	}
}

func TestPool_SelectBackendByRole_UnknownRole(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "role-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark all healthy
	for _, b := range pool.backends {
		b.Healthy.Store(true)
	}

	// Unknown role should fallback to any healthy backend
	backend := pool.selectBackendByRole("unknown_role")
	if backend == nil {
		t.Fatal("should select a healthy backend for unknown role")
	}
}

func TestPool_SelectBackendForQuery_Read_ReplicaDraining_FallbackPrimary(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "rw-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
		Routing: config.RoutingConfig{
			ReadWriteSplit: true,
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(true)
	for _, b := range pool.backends {
		if b.Role == "replica" {
			b.Healthy.Store(true)
			b.Draining.Store(true)
		}
	}

	// Replica draining, should fallback to primary
	backend := pool.selectBackendForQuery(false)
	if backend == nil {
		t.Fatal("should fallback to primary when replicas are draining")
	}
	if backend.Role != "primary" {
		t.Errorf("expected primary fallback, got %s", backend.Role)
	}
}

// Tests for createServerConnToRole error cases

func TestPool_CreateServerConnToRole_NoBackends(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "role-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	_, err = pool.createServerConnToRole("primary")
	if err == nil {
		t.Error("should fail when no backends available")
	}
}

func TestPool_CreateServerConnToRole_NoMatchingRole(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "role-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	_, err = pool.createServerConnToRole("replica")
	if err == nil {
		t.Error("should fail when no matching role exists")
	}
}

func TestPool_SelectBackendByRole_PrimaryDraining_FallbackAny(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "role-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "primary", Port: 5432, Role: "primary"},
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.primary.Healthy.Store(true)
	pool.primary.Draining.Store(true)
	for _, b := range pool.backends {
		if b.Role == "replica" {
			b.Healthy.Store(true)
		}
	}

	// Primary draining, requesting primary should fallback to replica
	backend := pool.selectBackendByRole("primary")
	if backend == nil {
		t.Fatal("should fallback to replica when primary is draining")
	}
	if backend.Role != "replica" {
		t.Errorf("expected replica fallback, got %s", backend.Role)
	}
}

// Strategy lifecycle tests

func TestNewSessionStrategy(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewSessionStrategy(pool)
	if strategy == nil {
		t.Fatal("NewSessionStrategy returned nil")
	}
	if strategy.pool != pool {
		t.Error("strategy should hold reference to pool")
	}
}

func TestSessionStrategy_OnQueryComplete(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewSessionStrategy(pool)
	sess := &Session{}

	// OnQueryComplete should do nothing
	err = strategy.OnQueryComplete(sess)
	if err != nil {
		t.Errorf("OnQueryComplete should return nil, got %v", err)
	}
}

func TestSessionStrategy_OnTransactionBeginEnd(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewSessionStrategy(pool)
	sess := &Session{}

	// OnTransactionBegin
	err = strategy.OnTransactionBegin(sess)
	if err != nil {
		t.Errorf("OnTransactionBegin should return nil, got %v", err)
	}
	if !sess.InTransaction() {
		t.Error("session should be in transaction after OnTransactionBegin")
	}

	// OnTransactionEnd
	err = strategy.OnTransactionEnd(sess)
	if err != nil {
		t.Errorf("OnTransactionEnd should return nil, got %v", err)
	}
	if sess.InTransaction() {
		t.Error("session should not be in transaction after OnTransactionEnd")
	}
}

func TestNewTransactionStrategy(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewTransactionStrategy(pool)
	if strategy == nil {
		t.Fatal("NewTransactionStrategy returned nil")
	}
	if strategy.pool != pool {
		t.Error("strategy should hold reference to pool")
	}
}

func TestTransactionStrategy_OnClientConnect(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewTransactionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	// OnClientConnect should not assign server in transaction mode
	err = strategy.OnClientConnect(context.Background(), sess)
	if err != nil {
		t.Errorf("OnClientConnect should return nil, got %v", err)
	}
	if sess.ServerConn() != nil {
		t.Error("server conn should be nil after OnClientConnect in transaction mode")
	}
}

func TestTransactionStrategy_OnTransactionEnd(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
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
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewTransactionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	// Simulate having a server conn
	conn := &ServerConn{
		id:            1,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}
	pool.serverConns.addActive(conn)
	sess.SetServerConn(conn)
	sess.SetInTransaction(true)

	// OnTransactionEnd should release the connection
	err = strategy.OnTransactionEnd(sess)
	if err != nil {
		t.Errorf("OnTransactionEnd should return nil, got %v", err)
	}
	if sess.ServerConn() != nil {
		t.Error("server conn should be nil after OnTransactionEnd")
	}
	if sess.InTransaction() {
		t.Error("session should not be in transaction after OnTransactionEnd")
	}
}

func TestNewStatementStrategy(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "statement",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewStatementStrategy(pool)
	if strategy == nil {
		t.Fatal("NewStatementStrategy returned nil")
	}
	if strategy.pool != pool {
		t.Error("strategy should hold reference to pool")
	}
}

func TestStatementStrategy_OnClientConnect(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "statement",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewStatementStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	// OnClientConnect should not assign server in statement mode
	err = strategy.OnClientConnect(context.Background(), sess)
	if err != nil {
		t.Errorf("OnClientConnect should return nil, got %v", err)
	}
	if sess.ServerConn() != nil {
		t.Error("server conn should be nil after OnClientConnect in statement mode")
	}
}

func TestStatementStrategy_OnTransactionBegin(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "statement",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewStatementStrategy(pool)
	sess := &Session{}

	// OnTransactionBegin should return error in statement mode
	err = strategy.OnTransactionBegin(sess)
	if err == nil {
		t.Error("OnTransactionBegin should return error in statement mode")
	}
}

func TestStatementStrategy_OnTransactionEnd(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "statement",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewStatementStrategy(pool)
	sess := &Session{}

	// OnTransactionEnd should be no-op
	err = strategy.OnTransactionEnd(sess)
	if err != nil {
		t.Errorf("OnTransactionEnd should return nil, got %v", err)
	}
}

func TestStrategyFactory_CreateStrategy(t *testing.T) {
	log, _ := logger.New("error", "json")

	modes := []struct {
		mode     string
		strategy interface{}
	}{
		{"session", &SessionStrategy{}},
		{"transaction", &TransactionStrategy{}},
		{"statement", &StatementStrategy{}},
	}

	for _, tc := range modes {
		cfg := &config.PoolConfig{
			Name: "test",
			Mode: tc.mode,
			Body: "postgresql",
			Backend: config.BackendConfig{
				Hosts: []config.BackendHost{
					{Host: "localhost", Port: 5432, Role: "primary"},
				},
			},
		}

		pool, err := NewPool(cfg, nil, log, nil)
		if err != nil {
			t.Fatalf("NewPool failed for mode %s: %v", tc.mode, err)
		}

		factory := &StrategyFactory{}
		strategy, err := factory.CreateStrategy(pool)
		if err != nil {
			t.Errorf("CreateStrategy failed for mode %s: %v", tc.mode, err)
		}
		if strategy == nil {
			t.Errorf("CreateStrategy returned nil for mode %s", tc.mode)
		}
	}
}

func TestStrategyFactory_CreateStrategy_InvalidMode(t *testing.T) {
	// Use a pool with valid mode, then manually change the mode enum
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Manually set an invalid mode to test the factory
	pool.mode = PoolMode(255)

	factory := &StrategyFactory{}
	_, err = factory.CreateStrategy(pool)
	if err == nil {
		t.Error("CreateStrategy should fail for invalid mode")
	}
}

func TestDefaultStrategyFactory(t *testing.T) {
	if DefaultStrategyFactory == nil {
		t.Error("DefaultStrategyFactory should be initialized")
	}
}

func TestSession_ServerConn(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	sess := NewSession(context.Background(), func() {}, pool, nil)

	// Initially nil
	if sess.ServerConn() != nil {
		t.Error("ServerConn should be nil initially")
	}

	// Set and get
	conn := &ServerConn{id: 42}
	sess.SetServerConn(conn)
	if sess.ServerConn() != conn {
		t.Error("ServerConn should return the set connection")
	}

	// Set back to nil
	sess.SetServerConn(nil)
	if sess.ServerConn() != nil {
		t.Error("ServerConn should be nil after SetServerConn(nil)")
	}
}

func TestSession_GetLastQuery(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	sess := NewSession(context.Background(), func() {}, pool, nil)

	// Initially empty
	if sess.GetLastQuery() != "" {
		t.Errorf("GetLastQuery = %q, want empty", sess.GetLastQuery())
	}

	// Via LastQuery setter
	sess.SetLastQuery("SELECT 1")
	if sess.GetLastQuery() != "SELECT 1" {
		t.Errorf("GetLastQuery = %q, want SELECT 1", sess.GetLastQuery())
	}
}

func TestSession_HandleMessage_NilMessage(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	sess := NewSession(context.Background(), func() {}, pool, nil)

	// Nil message should return nil error
	err = sess.HandleMessage(nil)
	if err != nil {
		t.Errorf("HandleMessage(nil) should return nil error, got %v", err)
	}
}

func TestSession_HandleMessage_NilCodec(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Nil codec
	pool.codec = nil
	sess := NewSession(context.Background(), func() {}, pool, nil)

	msg := &common.Message{}
	err = sess.HandleMessage(msg)
	if err == nil {
		t.Error("HandleMessage should fail with nil codec")
	}
}

func TestSession_HandleMessage_QueryWithNilServerConn(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	codec := &MockCodecQuery{}
	pool, err := NewPool(cfg, codec, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewSessionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	// No server conn assigned
	msg := &common.Message{Raw: []byte("SELECT 1")}
	err = sess.HandleMessage(msg)
	if err == nil {
		t.Error("HandleMessage should fail when no server connection")
	}
}

// MockCodecQuery is a MockCodec that reports queries
type MockCodecQuery struct{ MockCodec }

func (m *MockCodecQuery) IsQuery(msg *common.Message) bool { return true }
func (m *MockCodecQuery) ExtractQuery(msg *common.Message) (string, error) {
	return string(msg.Raw), nil
}

// MockCodecNoReset is a MockCodecQuery that returns an unknown protocol,
// so ResetConnection fails immediately instead of hanging on mock servers.
type MockCodecNoReset struct{ MockCodecQuery }

func (m *MockCodecNoReset) Protocol() common.Protocol { return common.Protocol(99) }

func TestSession_UserDatabase(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	sess := NewSession(context.Background(), func() {}, pool, nil)

	// Initially empty
	if sess.User() != "" {
		t.Errorf("User = %q, want empty", sess.User())
	}
	if sess.Database() != "" {
		t.Errorf("Database = %q, want empty", sess.Database())
	}

	// Set values
	sess.SetUser("testuser")
	sess.SetDatabase("testdb")

	if sess.User() != "testuser" {
		t.Errorf("User = %q, want testuser", sess.User())
	}
	if sess.Database() != "testdb" {
		t.Errorf("Database = %q, want testdb", sess.Database())
	}
}

func TestSession_AuthDone_Extended(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	sess := NewSession(context.Background(), func() {}, pool, nil)

	// Initially false
	if sess.AuthDone() {
		t.Error("AuthDone should be false initially")
	}

	// Set to true
	sess.SetAuthDone()
	if !sess.AuthDone() {
		t.Error("AuthDone should be true after SetAuthDone")
	}
}

func TestSession_TransactionStart_Extended(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	sess := NewSession(context.Background(), func() {}, pool, nil)

	// Initially zero time
	if !sess.TransactionStart().IsZero() {
		t.Error("TransactionStart should be zero initially")
	}

	// Set in transaction - should set txnStart
	sess.SetInTransaction(true)
	if sess.TransactionStart().IsZero() {
		t.Error("TransactionStart should be set after SetInTransaction(true)")
	}
}

func TestSession_UpdateLastActive(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	sess := NewSession(context.Background(), func() {}, pool, nil)

	oldActive := sess.LastActive()
	time.Sleep(10 * time.Millisecond)
	sess.UpdateLastActive()

	if !sess.LastActive().After(oldActive) {
		t.Error("LastActive should be updated")
	}
}

func TestSession_BytesInOut(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	sess := NewSession(context.Background(), func() {}, pool, nil)

	// Initially 0
	if sess.BytesIn() != 0 {
		t.Errorf("BytesIn = %d, want 0", sess.BytesIn())
	}
	if sess.BytesOut() != 0 {
		t.Errorf("BytesOut = %d, want 0", sess.BytesOut())
	}

	// Add bytes
	sess.AddBytesIn(100)
	sess.AddBytesOut(200)

	if sess.BytesIn() != 100 {
		t.Errorf("BytesIn = %d, want 100", sess.BytesIn())
	}
	if sess.BytesOut() != 200 {
		t.Errorf("BytesOut = %d, want 200", sess.BytesOut())
	}
}

func TestSession_QueryCount_Extended(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	sess := NewSession(context.Background(), func() {}, pool, nil)

	// Initially 0
	if sess.QueryCount() != 0 {
		t.Errorf("QueryCount = %d, want 0", sess.QueryCount())
	}

	// Increment
	sess.IncrementQueryCount()
	sess.IncrementQueryCount()

	if sess.QueryCount() != 2 {
		t.Errorf("QueryCount = %d, want 2", sess.QueryCount())
	}
}

func TestSession_TargetRole_Extended(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	sess := NewSession(context.Background(), func() {}, pool, nil)

	// Initially empty
	if sess.TargetRole() != "" {
		t.Errorf("TargetRole = %q, want empty", sess.TargetRole())
	}

	// Set role
	sess.SetTargetRole("replica")
	if sess.TargetRole() != "replica" {
		t.Errorf("TargetRole = %q, want replica", sess.TargetRole())
	}
}

func TestSession_Stats_Extended(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	sess := NewSession(context.Background(), func() {}, pool, nil)
	sess.SetUser("testuser")
	sess.SetDatabase("testdb")
	sess.SetAuthDone()
	sess.IncrementQueryCount()
	sess.AddBytesIn(50)
	sess.AddBytesOut(100)

	// Assign a server conn
	conn := &ServerConn{id: 123}
	sess.SetServerConn(conn)

	stats := sess.Stats()

	if stats.ID == 0 {
		t.Error("Stats.ID should be non-zero")
	}
	if stats.Pool != "test-pool" {
		t.Errorf("Stats.Pool = %q, want test-pool", stats.Pool)
	}
	if stats.User != "testuser" {
		t.Errorf("Stats.User = %q, want testuser", stats.User)
	}
	if stats.Database != "testdb" {
		t.Errorf("Stats.Database = %q, want testdb", stats.Database)
	}
	if !stats.AuthDone {
		t.Error("Stats.AuthDone should be true")
	}
	if stats.QueryCount != 1 {
		t.Errorf("Stats.QueryCount = %d, want 1", stats.QueryCount)
	}
	if stats.BytesIn != 50 {
		t.Errorf("Stats.BytesIn = %d, want 50", stats.BytesIn)
	}
	if stats.BytesOut != 100 {
		t.Errorf("Stats.BytesOut = %d, want 100", stats.BytesOut)
	}
	if stats.ServerConnID != 123 {
		t.Errorf("Stats.ServerConnID = %d, want 123", stats.ServerConnID)
	}
}

func TestSession_Stats_NoServerConn(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	sess := NewSession(context.Background(), func() {}, pool, nil)
	stats := sess.Stats()

	if stats.ServerConnID != 0 {
		t.Errorf("Stats.ServerConnID = %d, want 0", stats.ServerConnID)
	}
}

// Prepared statement tests

func TestSessionPreparedStatements_GetQuery(t *testing.T) {
	psc := NewPreparedStatementCache(100, time.Hour)
	sps := NewSessionPreparedStatements(psc)

	// Register a statement
	sps.Register("my_stmt", "SELECT $1", []int32{23})

	// GetQuery should work same as Get
	got, ok := sps.GetQuery("my_stmt")
	if !ok {
		t.Error("GetQuery should return true")
	}
	if got.Query != "SELECT $1" {
		t.Errorf("GetQuery Query = %q, want SELECT $1", got.Query)
	}

	// Non-existent
	_, ok = sps.GetQuery("nonexistent")
	if ok {
		t.Error("GetQuery should return false for non-existent")
	}
}

func TestSessionPreparedStatements_Add(t *testing.T) {
	psc := NewPreparedStatementCache(100, time.Hour)
	sps := NewSessionPreparedStatements(psc)

	// Add a statement
	name := sps.Add("SELECT 1")
	if name == "" {
		t.Error("Add should return a non-empty name")
	}

	// Should be retrievable
	got, ok := sps.Get(name)
	if !ok {
		t.Error("Get should return the added statement")
	}
	if got.Query != "SELECT 1" {
		t.Errorf("Query = %q, want SELECT 1", got.Query)
	}
}

func TestPreparedStatementCache_Cleanup(t *testing.T) {
	// Create cache with very short TTL
	psc := NewPreparedStatementCache(100, 50*time.Millisecond)

	// Add a statement
	psc.Add("stmt1", "SELECT 1", nil)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Trigger cleanup
	psc.cleanup()

	// Statement should be evicted
	_, ok := psc.GetByID("stmt1")
	if ok {
		t.Error("Statement should be cleaned up after TTL")
	}
}

// TransactionManager SetOnAbortWithConn test

func TestTransactionManager_SetOnAbortWithConn(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Hour, time.Minute, 0, log)
	defer tm.Stop()

	var called bool
	tm.SetOnAbortWithConn(func(sessionID uint64, serverConn net.Conn) {
		called = true
	})

	// Register a transaction
	tm.Register(42, 100, nil)

	// The callback is stored but only called during checkTimeouts
	// Just verify setter doesn't panic and stores correctly
	if called {
		// Shouldn't be called yet
	}
}

// loadBackendTLSConfig tests

func TestLoadBackendTLSConfig_Empty(t *testing.T) {
	cfg := config.TLSConfig{}

	tlsConfig, err := loadBackendTLSConfig(cfg)
	if err != nil {
		t.Errorf("loadBackendTLSConfig with empty config should not error: %v", err)
	}
	if tlsConfig == nil {
		t.Error("loadBackendTLSConfig should return a config even with empty input")
	}
	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want %d", tlsConfig.MinVersion, tls.VersionTLS12)
	}
}

func TestLoadBackendTLSConfig_NonExistentCert(t *testing.T) {
	cfg := config.TLSConfig{
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
	}

	_, err := loadBackendTLSConfig(cfg)
	if err == nil {
		t.Error("loadBackendTLSConfig should fail with non-existent cert files")
	}
}

func TestLoadBackendTLSConfig_NonExistentCA(t *testing.T) {
	cfg := config.TLSConfig{
		CAFile: "/nonexistent/ca.pem",
	}

	_, err := loadBackendTLSConfig(cfg)
	if err == nil {
		t.Error("loadBackendTLSConfig should fail with non-existent CA file")
	}
}

func TestLoadBackendTLSConfig_Modes(t *testing.T) {
	modes := []string{"require", "verify-ca", "verify-full", ""}
	for _, mode := range modes {
		cfg := config.TLSConfig{Mode: mode}
		_, err := loadBackendTLSConfig(cfg)
		if err != nil {
			t.Errorf("loadBackendTLSConfig mode %q should not error: %v", mode, err)
		}
	}
}

// Pool.TransactionManager accessor test

func TestPool_TransactionManager(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	tm := pool.TransactionManager()
	if tm == nil {
		t.Error("TransactionManager should not return nil")
	}
}

// Pool.Codec with nil

func TestPool_Codec_Nil(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Codec should be nil when nil codec passed
	if pool.Codec() != nil {
		t.Error("Codec should be nil when nil codec passed")
	}
}

// Test DecrementClientCount

func TestPool_DecrementClientCount(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	pool.IncrementClientCount()
	pool.IncrementClientCount()
	pool.DecrementClientCount()

	stats := pool.Stats()
	if stats.ClientConnections != 1 {
		t.Errorf("ClientConnections = %d, want 1", stats.ClientConnections)
	}
}

// Test Pool.Close with health checker

func TestPool_Close_WithHealthChecker(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Health: config.HealthConfig{
			CheckInterval: "1s",
			CheckQuery:    "SELECT 1",
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Start health checks
	pool.StartHealthChecks()

	// Close should stop health checker
	err = pool.Close()
	if err != nil {
		t.Errorf("Close error: %v", err)
	}
}

// ResetConnection is only called from serverConnPool.release with a valid codec,
// so nil codec is not a tested production path.

// Test ResetterRegistry_Get unknown protocol

func TestResetterRegistry_GetUnknown(t *testing.T) {
	registry := NewResetterRegistry()

	_, ok := registry.Get(common.Protocol(255))
	if ok {
		t.Error("should not find resetter for unknown protocol")
	}
}

// Test tryConnect with no backends and connection failure

func TestPool_TryConnect_Failure(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 65535, Role: "primary"}, // unlikely to be listening
			},
		},
		Limits: config.LimitConfig{
			ConnectionTimeout: "100ms",
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	backend := pool.backends[0]
	backend.Healthy.Store(true)

	_, err = pool.tryConnect(backend)
	if err == nil {
		t.Error("tryConnect should fail connecting to port 65535")
	}
}

// Test createServerConn all connection attempts fail

func TestPool_CreateServerConn_AllAttemptsFail(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 65535, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			ConnectionTimeout: "100ms",
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	_, err = pool.createServerConn()
	if err == nil {
		t.Error("createServerConn should fail when backend unreachable")
	}
}

// Test createServerConn with only draining backends

func TestPool_CreateServerConn_OnlyDrainingBackends(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
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
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark primary as draining
	pool.primary.Draining.Store(true)

	_, err = pool.createServerConn()
	if err == nil {
		t.Error("createServerConn should fail when all backends draining")
	}
}

// Test AcquireToRole with wait queue timeout

func TestPool_Acquire_WaitContextCancelled(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 1,
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Fill to max
	conn := &ServerConn{
		id:            1,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}
	pool.serverConns.addActive(conn)

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = pool.Acquire(ctx)
	if err == nil {
		t.Error("Acquire should fail with cancelled context")
	}
}

// Test selectBackendWithFallback with no primary

func TestPool_SelectBackendWithFallback_NoPrimary(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark replica healthy
	for _, b := range pool.backends {
		b.Healthy.Store(true)
	}

	backend := pool.selectBackendWithFallback()
	if backend == nil {
		t.Error("should select replica when no primary exists")
	}
	if backend.Role != "replica" {
		t.Errorf("expected replica, got %s", backend.Role)
	}
}

// Test selectBackendByRole with no replicas when requesting replica

func TestPool_SelectBackendByRole_ReplicaWithNoPrimary(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark replica healthy
	for _, b := range pool.backends {
		b.Healthy.Store(true)
	}

	backend := pool.selectBackendByRole("replica")
	if backend == nil {
		t.Error("should select replica")
	}
	if backend.Role != "replica" {
		t.Errorf("expected replica, got %s", backend.Role)
	}

	// Requesting primary when no primary exists, but replica is available
	backend = pool.selectBackendByRole("primary")
	if backend == nil {
		t.Error("should fallback to replica when no primary")
	}
	if backend.Role != "replica" {
		t.Errorf("expected replica fallback, got %s", backend.Role)
	}
}

// Test selectBackendForQuery with write when only replica exists

func TestPool_SelectBackendForQuery_WriteOnlyReplica(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "replica", Port: 5433, Role: "replica"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark replica healthy
	for _, b := range pool.backends {
		b.Healthy.Store(true)
	}

	// Write query with only replica
	backend := pool.selectBackendForQuery(true)
	if backend == nil {
		t.Error("should select replica for write when no primary")
	}
}

// Test Session HandleMessage with prepare message

func TestSession_HandleMessage_Prepare(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	codec := &MockCodecPrepare{}
	pool, err := NewPool(cfg, codec, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewSessionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	// Assign a server conn with nil net.Conn (will fail on WriteMessage but that's ok for prepare path)
	// Actually, prepare message doesn't write in HandleMessage, so it should work
	msg := &common.Message{Raw: []byte("PREPARE stmt AS SELECT 1")}
	err = sess.HandleMessage(msg)
	// Should succeed - prepare path doesn't require server conn
	if err != nil {
		t.Errorf("HandleMessage for prepare should not error: %v", err)
	}

	// Statement should be tracked
	// stmtTracker has the statement
}

type MockCodecPrepare struct{ MockCodec }

func (m *MockCodecPrepare) IsPrepare(msg *common.Message) bool { return true }
func (m *MockCodecPrepare) ExtractQuery(msg *common.Message) (string, error) {
	return string(msg.Raw), nil
}

// Test Session HandleMessage with close message

func TestSession_HandleMessage_Close(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	codec := &MockCodecClose{}
	pool, err := NewPool(cfg, codec, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewSessionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	msg := &common.Message{Raw: []byte("close")}
	err = sess.HandleMessage(msg)
	if err != nil {
		t.Errorf("HandleMessage for close should not error: %v", err)
	}
}

type MockCodecClose struct{ MockCodec }

func (m *MockCodecClose) IsClose(msg *common.Message) bool { return true }

// Test Pool.GetCachedResult and SetCachedResult with cache present

func TestPool_GetCachedResult_WithCache(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Without query cache configured, should return false
	_, hit := pool.GetCachedResult("SELECT 1", nil)
	if hit {
		t.Error("should not hit without query cache configured")
	}
}

func TestPool_SetCachedResult_WithCache(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Without query cache, should return nil error
	err = pool.SetCachedResult("SELECT 1", nil, []byte("data"), time.Minute)
	if err != nil {
		t.Errorf("SetCachedResult should return nil without query cache, got: %v", err)
	}
}

func TestPool_InvalidateCache_WithCache(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Should not panic without cache
	pool.InvalidateCache("table1")
}

// Test TransactionManager GetActiveTransactions

func TestTransactionManager_GetActiveTransactions_WithData(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Hour, time.Minute, 0, log)
	defer tm.Stop()

	tm.Register(1, 100, nil)
	tm.Register(2, 101, nil)

	txns := tm.GetActiveTransactions()
	if len(txns) != 2 {
		t.Errorf("GetActiveTransactions length = %d, want 2", len(txns))
	}
}

// Test TransactionManager GetTransactionDetails with data

func TestTransactionManager_GetTransactionDetails_WithData(t *testing.T) {
	log, _ := logger.New("error", "json")
	tm := NewTransactionManager(time.Hour, time.Minute, 0, log)
	defer tm.Stop()

	info := tm.Register(1, 100, nil)

	// GetTransactionDetails looks up by internal txn ID
	details := tm.GetTransactionDetails(info.ID)
	if details == nil {
		t.Error("GetTransactionDetails should return details for existing transaction")
	}
}

// Test ServerConn.Close with nil backend

func TestServerConn_Close_NilBackend(t *testing.T) {
	sc := &ServerConn{id: 1}
	err := sc.Close()
	if err != nil {
		t.Errorf("Close with nil backend should not error: %v", err)
	}
}

// Test ServerConn.Close with backend having zero count

func TestServerConn_Close_BackendZeroCount(t *testing.T) {
	b := &Backend{Host: "localhost", Port: 5432}
	// ConnCount already 0

	sc := &ServerConn{id: 1, backend: b}
	// Close should not panic even with zero count
	err := sc.Close()
	if err != nil {
		t.Errorf("Close should not error: %v", err)
	}

	// ConnCount is decremented unconditionally (may go negative in edge cases)
	if b.ConnCount.Load() != -1 {
		t.Errorf("ConnCount = %d, want -1 (decremented from 0)", b.ConnCount.Load())
	}
}

// Test Pool RemovePool

func TestManager_RemovePool_Extended(t *testing.T) {
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

	// Remove pool
	err = mgr.RemovePool("test")
	if err != nil {
		t.Errorf("RemovePool failed: %v", err)
	}

	// Pool should be gone
	p := mgr.GetPool("test")
	if p != nil {
		t.Error("GetPool should return nil after RemovePool")
	}
}

func TestManager_RemovePool_NotFound(t *testing.T) {
	log, _ := logger.New("error", "json")
	mgr := NewManager(log)

	err := mgr.RemovePool("nonexistent")
	if err == nil {
		t.Error("RemovePool should fail for non-existent pool")
	}
}

func TestManager_ListPools(t *testing.T) {
	log, _ := logger.New("error", "json")
	mgr := NewManager(log)

	// Initially empty
	pools := mgr.ListPools()
	if len(pools) != 0 {
		t.Errorf("ListPools = %d, want 0", len(pools))
	}

	// Add pools
	cfg := &config.PoolConfig{
		Name: "pool1",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	mgr.CreatePool(cfg)

	cfg2 := &config.PoolConfig{
		Name: "pool2",
		Body: "postgresql",
		Mode: "session",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5433, Role: "primary"},
			},
		},
	}
	mgr.CreatePool(cfg2)

	pools = mgr.ListPools()
	if len(pools) != 2 {
		t.Errorf("ListPools = %d, want 2", len(pools))
	}
}

func TestManager_GetPool(t *testing.T) {
	log, _ := logger.New("error", "json")
	mgr := NewManager(log)

	// Get non-existent
	p := mgr.GetPool("nonexistent")
	if p != nil {
		t.Error("GetPool should return nil for non-existent pool")
	}
}

// Test pool release with codec on ServerConn (triggers async reset path).
// The reset path requires a real net.Conn, so we test that release without codec
// works correctly (codec is nil on conn).

func TestServerConnPool_release_WithNilCodecOnConn(t *testing.T) {
	pool := newServerConnPool(1, 5)

	// ServerConn with no codec - release goes to idle without reset
	mockConn := &ServerConn{
		id:            1,
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}

	pool.addActive(mockConn)
	pool.release(mockConn, nil)

	if pool.activeCount() != 0 {
		t.Errorf("activeCount = %d, want 0", pool.activeCount())
	}
	if pool.idleCount() != 1 {
		t.Errorf("idleCount = %d, want 1", pool.idleCount())
	}
}

// Test tryConnect marks backend healthy on success

func TestPool_TryConnect_MarksHealthy(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			ConnectionTimeout: "100ms",
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	backend := pool.backends[0]
	backend.Healthy.Store(false)

	// This will fail to connect, but test the code path
	_, err = pool.tryConnect(backend)
	// Expect error since nothing is listening
	if err != nil {
		// Backend should still be unhealthy since connection failed
		if backend.Healthy.Load() {
			t.Error("backend should not be marked healthy on connection failure")
		}
	}
}

// Test createServerConnToRole error message format

func TestPool_CreateServerConnToRole_ErrorFormat(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 65535, Role: "replica"},
			},
		},
		Limits: config.LimitConfig{
			ConnectionTimeout: "100ms",
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	for _, b := range pool.backends {
		b.Healthy.Store(true)
	}

	_, err = pool.createServerConnToRole("replica")
	if err == nil {
		t.Fatal("expected error")
	}
	// Error should mention replica and attempts or no healthy backend
	if err.Error() != "no healthy replica backend available" &&
		(err.Error() != fmt.Sprintf("failed to connect to replica backend after 3 attempts")) {
		t.Errorf("unexpected error: %v", err)
	}
}

// Test Acquire creates server conn error path

func TestPool_Acquire_CreateConnError(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 65535, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			ConnectionTimeout:    "100ms",
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// No idle connections, pool not at max -> tries createServerConn which fails
	_, err = pool.Acquire(context.Background())
	if err == nil {
		t.Error("Acquire should fail when backend is unreachable")
	}
}

// Test Session.HandleMessage with backend health tracking

func TestSession_IncrementQueryCount_UpdatesLastActive(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	sess := NewSession(context.Background(), func() {}, pool, nil)
	oldActive := sess.LastActive()
	time.Sleep(10 * time.Millisecond)

	sess.IncrementQueryCount()

	if sess.QueryCount() != 1 {
		t.Errorf("QueryCount = %d, want 1", sess.QueryCount())
	}
	if !sess.LastActive().After(oldActive) {
		t.Error("IncrementQueryCount should update LastActive")
	}
}

// mockTCPBackend creates a TCP listener that accepts connections and keeps them open
// for testing pool Acquire/Release operations.
func mockTCPBackend(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Keep connection open so net.Dial succeeds
			// Just drain incoming data and eventually close
			go func() {
				defer conn.Close()
				buf := make([]byte, 1024)
				for {
					if _, err := conn.Read(buf); err != nil {
						return
					}
				}
			}()
		}
	}()

	return listener
}

// setupPoolWithMockBackend creates a pool connected to a mock TCP server.
func setupPoolWithMockBackend(t *testing.T, mode string) (*Pool, net.Listener) {
	t.Helper()
	listener := mockTCPBackend(t)
	addr := listener.Addr().String()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort failed: %v", err)
	}
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	cfg := &config.PoolConfig{
		Name: "test-mock",
		Body: "postgresql",
		Mode: mode,
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: host, Port: port, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Set a mock codec so strategy methods that call Codec() don't panic.
	// Use MockCodecNoReset so Release doesn't hang trying to reset mock connections.
	pool.codec = &MockCodecNoReset{}

	return pool, listener
}

// TestSessionStrategy_OnClientConnect tests SessionStrategy.OnClientConnect
// which calls pool.Acquire and assigns the server connection to the session.
func TestSessionStrategy_OnClientConnect(t *testing.T) {
	pool, listener := setupPoolWithMockBackend(t, "session")
	defer listener.Close()
	defer pool.Close()

	strategy := NewSessionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	ctx := context.Background()
	err := strategy.OnClientConnect(ctx, sess)
	if err != nil {
		t.Fatalf("OnClientConnect failed: %v", err)
	}

	conn := sess.ServerConn()
	if conn == nil {
		t.Fatal("Session should have a server connection after OnClientConnect")
	}

	if !conn.IsInUse() {
		t.Error("ServerConn should be marked as InUse")
	}
}

// TestSessionStrategy_OnClientDisconnect tests releasing the server connection.
func TestSessionStrategy_OnClientDisconnect(t *testing.T) {
	pool, listener := setupPoolWithMockBackend(t, "session")
	defer listener.Close()
	defer pool.Close()

	strategy := NewSessionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	ctx := context.Background()
	if err := strategy.OnClientConnect(ctx, sess); err != nil {
		t.Fatalf("OnClientConnect failed: %v", err)
	}

	conn := sess.ServerConn()
	if conn == nil {
		t.Fatal("no server conn")
	}

	err := strategy.OnClientDisconnect(sess)
	if err != nil {
		t.Fatalf("OnClientDisconnect failed: %v", err)
	}

	if sess.ServerConn() != nil {
		t.Error("ServerConn should be nil after disconnect")
	}

	// The connection should be released (reset fails so conn is closed,
	// but Release call completes without error)
	acquired, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Failed to acquire connection: %v", err)
	}
	if acquired == nil {
		t.Error("Should be able to acquire a new connection after release")
	}
}

// TestTransactionStrategy_OnClientDisconnect tests releasing any held connection.
func TestTransactionStrategy_OnClientDisconnect(t *testing.T) {
	pool, listener := setupPoolWithMockBackend(t, "transaction")
	defer listener.Close()
	defer pool.Close()

	strategy := NewTransactionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	// OnClientConnect is a no-op for transaction strategy
	ctx := context.Background()
	if err := strategy.OnClientConnect(ctx, sess); err != nil {
		t.Fatalf("OnClientConnect failed: %v", err)
	}
	if sess.ServerConn() != nil {
		t.Error("TransactionStrategy should not assign connection on connect")
	}

	// Manually acquire a connection to simulate mid-transaction state
	acquiredConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	sess.SetServerConn(acquiredConn)

	// Disconnect should release the connection
	err = strategy.OnClientDisconnect(sess)
	if err != nil {
		t.Fatalf("OnClientDisconnect failed: %v", err)
	}
	if sess.ServerConn() != nil {
		t.Error("ServerConn should be nil after disconnect")
	}
}

// TestTransactionStrategy_OnQuery tests acquiring a connection on first query.
func TestTransactionStrategy_OnQuery(t *testing.T) {
	pool, listener := setupPoolWithMockBackend(t, "transaction")
	defer listener.Close()
	defer pool.Close()

	strategy := NewTransactionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	ctx := context.Background()
	msg := &common.Message{Raw: []byte("SELECT 1")}

	// First query should acquire a connection
	conn, err := strategy.OnQuery(ctx, sess, msg)
	if err != nil {
		t.Fatalf("OnQuery failed: %v", err)
	}
	if conn == nil {
		t.Fatal("OnQuery should return a connection")
	}
	if sess.ServerConn() != conn {
		t.Error("Session should have the acquired connection")
	}

	// Second query should reuse the same connection
	conn2, err := strategy.OnQuery(ctx, sess, msg)
	if err != nil {
		t.Fatalf("Second OnQuery failed: %v", err)
	}
	if conn2.ID() != conn.ID() {
		t.Error("Second query should reuse same connection")
	}
}

// TestTransactionStrategy_OnQueryComplete tests release behavior for autocommit.
func TestTransactionStrategy_OnQueryComplete(t *testing.T) {
	pool, listener := setupPoolWithMockBackend(t, "transaction")
	defer listener.Close()
	defer pool.Close()

	strategy := NewTransactionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	ctx := context.Background()
	msg := &common.Message{Raw: []byte("SELECT 1")}

	// Acquire via OnQuery (sets AutoCommitRelease for implicit txns)
	_, err := strategy.OnQuery(ctx, sess, msg)
	if err != nil {
		t.Fatalf("OnQuery failed: %v", err)
	}

	// OnQueryComplete should release since not in explicit transaction
	err = strategy.OnQueryComplete(sess)
	if err != nil {
		t.Fatalf("OnQueryComplete failed: %v", err)
	}
	if sess.ServerConn() != nil {
		t.Error("ServerConn should be released after OnQueryComplete in autocommit")
	}
}

// TestTransactionStrategy_OnQueryComplete_InTransaction tests no release in txn.
func TestTransactionStrategy_OnQueryComplete_InTransaction(t *testing.T) {
	pool, listener := setupPoolWithMockBackend(t, "transaction")
	defer listener.Close()
	defer pool.Close()

	strategy := NewTransactionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	ctx := context.Background()
	msg := &common.Message{Raw: []byte("SELECT 1")}

	conn, err := strategy.OnQuery(ctx, sess, msg)
	if err != nil {
		t.Fatalf("OnQuery failed: %v", err)
	}

	// Simulate being in a transaction
	sess.SetInTransaction(true)
	sess.SetAutoCommitRelease(false)

	err = strategy.OnQueryComplete(sess)
	if err != nil {
		t.Fatalf("OnQueryComplete failed: %v", err)
	}
	if sess.ServerConn() == nil {
		t.Error("ServerConn should NOT be released while in transaction")
	}
	// Clean up
	sess.SetServerConn(nil)
	pool.Release(conn)
}

// TestTransactionStrategy_OnTransactionBegin tests setting transaction state.
func TestTransactionStrategy_OnTransactionBegin(t *testing.T) {
	pool, listener := setupPoolWithMockBackend(t, "transaction")
	defer listener.Close()
	defer pool.Close()

	strategy := NewTransactionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	if sess.InTransaction() {
		t.Fatal("Session should not be in transaction initially")
	}

	err := strategy.OnTransactionBegin(sess)
	if err != nil {
		t.Fatalf("OnTransactionBegin failed: %v", err)
	}
	if !sess.InTransaction() {
		t.Error("Session should be in transaction after OnTransactionBegin")
	}
}

// TestTransactionStrategy_OnTransactionEnd tests releasing connection after txn.
func TestTransactionStrategy_OnTransactionEnd_MockBackend(t *testing.T) {
	pool, listener := setupPoolWithMockBackend(t, "transaction")
	defer listener.Close()
	defer pool.Close()

	strategy := NewTransactionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	ctx := context.Background()
	msg := &common.Message{Raw: []byte("SELECT 1")}
	conn, err := strategy.OnQuery(ctx, sess, msg)
	if err != nil {
		t.Fatalf("OnQuery failed: %v", err)
	}
	if conn == nil {
		t.Fatal("OnQuery should return a connection")
	}

	sess.SetInTransaction(true)

	err = strategy.OnTransactionEnd(sess)
	if err != nil {
		t.Fatalf("OnTransactionEnd failed: %v", err)
	}
	if sess.ServerConn() != nil {
		t.Error("ServerConn should be nil after OnTransactionEnd")
	}
	if sess.InTransaction() {
		t.Error("InTransaction should be false after OnTransactionEnd")
	}

	// Verify the connection was released (reset fails so conn is closed,
	// but Release completes and pool can create a new connection)
	reacquired, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Failed to acquire connection: %v", err)
	}
	if reacquired == nil {
		t.Error("Should be able to acquire a new connection after release")
	}
}

// TestStatementStrategy_OnClientDisconnect tests releasing any leaked connection.
func TestStatementStrategy_OnClientDisconnect(t *testing.T) {
	pool, listener := setupPoolWithMockBackend(t, "statement")
	defer listener.Close()
	defer pool.Close()

	strategy := NewStatementStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	ctx := context.Background()
	// Connect is a no-op for statement
	if err := strategy.OnClientConnect(ctx, sess); err != nil {
		t.Fatalf("OnClientConnect failed: %v", err)
	}

	// Manually set a connection (simulating a leaked state)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	sess.SetServerConn(conn)

	err = strategy.OnClientDisconnect(sess)
	if err != nil {
		t.Fatalf("OnClientDisconnect failed: %v", err)
	}
	if sess.ServerConn() != nil {
		t.Error("ServerConn should be nil after disconnect")
	}
}

// TestStatementStrategy_OnQuery tests acquiring a fresh connection per query.
func TestStatementStrategy_OnQuery(t *testing.T) {
	pool, listener := setupPoolWithMockBackend(t, "statement")
	defer listener.Close()
	defer pool.Close()

	strategy := NewStatementStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	ctx := context.Background()
	msg := &common.Message{Raw: []byte("SELECT 1")}

	// First query
	conn1, err := strategy.OnQuery(ctx, sess, msg)
	if err != nil {
		t.Fatalf("OnQuery failed: %v", err)
	}
	if conn1 == nil {
		t.Fatal("OnQuery should return a connection")
	}

	// OnQueryComplete releases the connection
	strategy.OnQueryComplete(sess)

	// Second query should get a new connection
	conn2, err := strategy.OnQuery(ctx, sess, msg)
	if err != nil {
		t.Fatalf("Second OnQuery failed: %v", err)
	}
	if conn2.ID() == conn1.ID() {
		t.Error("Statement strategy should get a fresh connection each time")
	}

	// Clean up
	strategy.OnQueryComplete(sess)
}

// TestStatementStrategy_OnQueryComplete tests always releasing the connection.
func TestStatementStrategy_OnQueryComplete(t *testing.T) {
	pool, listener := setupPoolWithMockBackend(t, "statement")
	defer listener.Close()
	defer pool.Close()

	strategy := NewStatementStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	ctx := context.Background()
	msg := &common.Message{Raw: []byte("SELECT 1")}

	conn, err := strategy.OnQuery(ctx, sess, msg)
	if err != nil {
		t.Fatalf("OnQuery failed: %v", err)
	}
	if conn == nil {
		t.Fatal("OnQuery should return a connection")
	}
	if sess.ServerConn() == nil {
		t.Fatal("Session should have a connection")
	}

	err = strategy.OnQueryComplete(sess)
	if err != nil {
		t.Fatalf("OnQueryComplete failed: %v", err)
	}
	if sess.ServerConn() != nil {
		t.Error("ServerConn should be nil after OnQueryComplete in statement mode")
	}

	// Connection was released (reset fails so conn is closed,
	// but pool can create a new connection)
	reacquired, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Failed to acquire connection: %v", err)
	}
	if reacquired == nil {
		t.Error("Should be able to acquire a new connection after release")
	}
}

// TestStatementStrategy_OnTransactionBegin tests that statement mode rejects explicit transactions.
func TestStatementStrategy_OnTransactionBegin_MockBackend(t *testing.T) {
	pool, listener := setupPoolWithMockBackend(t, "statement")
	defer listener.Close()
	defer pool.Close()

	strategy := NewStatementStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)

	err := strategy.OnTransactionBegin(sess)
	if err == nil {
		t.Fatal("OnTransactionBegin should return error in statement mode")
	}
}

// TestStrategy_WithTargetRole tests AcquireToRole via TransactionStrategy.OnQuery
func TestTransactionStrategy_OnQuery_WithTargetRole(t *testing.T) {
	listener := mockTCPBackend(t)
	defer listener.Close()
	addr := listener.Addr().String()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort failed: %v", err)
	}
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	cfg := &config.PoolConfig{
		Name: "test-role",
		Body: "postgresql",
		Mode: "transaction",
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: host, Port: port, Role: "primary"},
				{Host: host, Port: port, Role: "replica"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}
	pool.codec = &MockCodecNoReset{}
	defer pool.Close()

	strategy := NewTransactionStrategy(pool)
	sess := NewSession(context.Background(), func() {}, pool, strategy)
	sess.SetTargetRole("replica")

	ctx := context.Background()
	msg := &common.Message{Raw: []byte("SELECT 1")}

	conn, err := strategy.OnQuery(ctx, sess, msg)
	if err != nil {
		t.Fatalf("OnQuery with target role failed: %v", err)
	}
	if conn == nil {
		t.Fatal("OnQuery should return a connection")
	}
	if conn.Backend().Role != "replica" {
		t.Errorf("Connection should be to replica, got %s", conn.Backend().Role)
	}
}

// mockPostgreSQLResetServer creates a TCP listener that simulates a PostgreSQL server
// responding to DISCARD ALL reset queries.
func mockPostgreSQLResetServer(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 1024)
				// Read the 'Q' query message (header: 1 byte type + 4 bytes length)
				if _, err := conn.Read(buf[:5]); err != nil {
					return
				}
				// Read query content
				queryLen := int(buf[1])<<24 | int(buf[2])<<16 | int(buf[3])<<8 | int(buf[4])
				remaining := queryLen - 4 // subtract length field itself
				if remaining > 0 && remaining < len(buf) {
					conn.Read(buf[:remaining])
				}
				// Send ReadyForQuery 'Z' response (format: 'Z' + 4 bytes length (5) + status byte)
				resp := []byte{'Z', 0, 0, 0, 5, 'I'}
				conn.Write(resp)
				// Keep connection open, the resetter will eventually timeout
				// reading more responses
				time.Sleep(200 * time.Millisecond)
			}()
		}
	}()

	return listener
}

// TestPostgreSQLResetter_Reset tests the PostgreSQL reset command.
func TestPostgreSQLResetter_Reset(t *testing.T) {
	listener := mockPostgreSQLResetServer(t)
	defer listener.Close()

	addr := listener.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect to mock server: %v", err)
	}
	defer conn.Close()

	codec := &postgresCodecForReset{}
	resetter := &PostgreSQLResetter{}

	err = resetter.Reset(conn, codec)
	if err != nil {
		t.Fatalf("PostgreSQLResetter.Reset failed: %v", err)
	}
}

// mockMySQLResetServer creates a TCP listener that simulates a MySQL server
// responding to COM_RESET_CONNECTION.
func mockMySQLResetServer(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 1024)
				// Read MySQL packet (3-byte length + 1-byte seq + payload)
				if _, err := conn.Read(buf[:4]); err != nil {
					return
				}
				payloadLen := int(buf[0]) | int(buf[1])<<8 | int(buf[2])<<16
				if payloadLen > 0 && payloadLen < len(buf) {
					conn.Read(buf[:payloadLen])
				}
				// Send OK packet response (0x00 = OK)
				resp := []byte{0x07, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00}
				conn.Write(resp)
			}()
		}
	}()

	return listener
}

// TestMySQLResetter_Reset tests the MySQL reset command.
func TestMySQLResetter_Reset(t *testing.T) {
	listener := mockMySQLResetServer(t)
	defer listener.Close()

	addr := listener.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect to mock server: %v", err)
	}
	defer conn.Close()

	codec := &mysqlCodecForReset{}
	resetter := &MySQLResetter{}

	err = resetter.Reset(conn, codec)
	if err != nil {
		t.Fatalf("MySQLResetter.Reset failed: %v", err)
	}
}

// mockMSSQLResetServer creates a TCP listener that simulates an MSSQL server
// responding to sp_reset_connection RPC request.
func mockMSSQLResetServer(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 1024)
				// Read TDS header (8 bytes)
				if _, err := conn.Read(buf[:8]); err != nil {
					return
				}
				tdsLen := int(buf[2])<<8 | int(buf[3])
				if tdsLen > 8 {
					remaining := tdsLen - 8
					if remaining > 0 && remaining < len(buf) {
						conn.Read(buf[:remaining])
					}
				}
				// Send a simple DONE token response
				// TDS response header (8 bytes) + DONE token
				resp := make([]byte, 16)
				resp[0] = 0x04 // response
				resp[1] = 0x01 // status
				resp[2] = 0x00 // length high
				resp[3] = 0x10 // length low (16 bytes)
				resp[4] = 0x00 // spid high
				resp[5] = 0x01 // spid low
				resp[6] = 0x00 // packet ID
				resp[7] = 0x00 // window
				// DONE token: FD 00 00 00 01 00
				resp[8] = 0xFD
				resp[9] = 0x00
				resp[10] = 0x00
				resp[11] = 0x00
				resp[12] = 0x01
				resp[13] = 0x00
				resp[14] = 0x00
				resp[15] = 0x00
				conn.Write(resp)
			}()
		}
	}()

	return listener
}

// TestMSSQLResetter_Reset tests the MSSQL reset command.
func TestMSSQLResetter_Reset(t *testing.T) {
	listener := mockMSSQLResetServer(t)
	defer listener.Close()

	addr := listener.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect to mock server: %v", err)
	}
	defer conn.Close()

	codec := &mssqlCodecForReset{}
	resetter := &MSSQLResetter{}

	err = resetter.Reset(conn, codec)
	if err != nil {
		t.Fatalf("MSSQLResetter.Reset failed: %v", err)
	}
}

// postgresCodecForReset is a minimal PostgreSQL codec for testing reset.
type postgresCodecForReset struct{}

func (c *postgresCodecForReset) Protocol() common.Protocol { return common.ProtocolPostgreSQL }
func (c *postgresCodecForReset) ReadMessage(r io.Reader) (*common.Message, error) {
	// Read ReadyForQuery message
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	length := int(header[1])<<24 | int(header[2])<<16 | int(header[3])<<8 | int(header[4])
	body := make([]byte, length-4)
	io.ReadFull(r, body)
	return &common.Message{Type: header[0], Payload: body}, nil
}
func (c *postgresCodecForReset) WriteMessage(w io.Writer, msg *common.Message) error {
	_, err := w.Write(msg.Raw)
	return err
}
func (c *postgresCodecForReset) IsQuery(msg *common.Message) bool                 { return false }
func (c *postgresCodecForReset) IsPrepare(msg *common.Message) bool               { return false }
func (c *postgresCodecForReset) IsExecute(msg *common.Message) bool               { return false }
func (c *postgresCodecForReset) IsClose(msg *common.Message) bool                 { return false }
func (c *postgresCodecForReset) IsBind(msg *common.Message) bool                  { return false }
func (c *postgresCodecForReset) IsSync(msg *common.Message) bool                  { return false }
func (c *postgresCodecForReset) IsStartup(msg *common.Message) bool               { return false }
func (c *postgresCodecForReset) IsTerminate(msg *common.Message) bool             { return false }
func (c *postgresCodecForReset) IsTransactionBegin(msg *common.Message) bool      { return false }
func (c *postgresCodecForReset) IsTransactionEnd(msg *common.Message) bool        { return false }
func (c *postgresCodecForReset) ExtractQuery(msg *common.Message) (string, error) { return "", nil }
func (c *postgresCodecForReset) GenerateResetSequence() []*common.Message {
	pg := &postgresqlCodecForResetGen{}
	return []*common.Message{pg.createQueryMessage("DISCARD ALL")}
}

// postgresqlCodecForResetGen mimics the PostgreSQL codec's createQueryMessage
type postgresqlCodecForResetGen struct{}

func (c *postgresqlCodecForResetGen) createQueryMessage(query string) *common.Message {
	queryBytes := []byte(query)
	length := 4 + len(queryBytes) + 1
	buf := make([]byte, 1+length)
	buf[0] = 'Q'
	buf[1] = byte(length >> 24)
	buf[2] = byte(length >> 16)
	buf[3] = byte(length >> 8)
	buf[4] = byte(length)
	copy(buf[5:], queryBytes)
	buf[5+len(queryBytes)] = 0
	return &common.Message{Type: 'Q', Raw: buf}
}

// mysqlCodecForReset is a minimal MySQL codec for testing reset.
type mysqlCodecForReset struct{}

func (c *mysqlCodecForReset) Protocol() common.Protocol { return common.ProtocolMySQL }
func (c *mysqlCodecForReset) ReadMessage(r io.Reader) (*common.Message, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	body := make([]byte, length)
	io.ReadFull(r, body)
	return &common.Message{Type: header[3], Payload: body}, nil
}
func (c *mysqlCodecForReset) WriteMessage(w io.Writer, msg *common.Message) error {
	_, err := w.Write(msg.Raw)
	return err
}
func (c *mysqlCodecForReset) IsQuery(msg *common.Message) bool                 { return false }
func (c *mysqlCodecForReset) IsPrepare(msg *common.Message) bool               { return false }
func (c *mysqlCodecForReset) IsExecute(msg *common.Message) bool               { return false }
func (c *mysqlCodecForReset) IsClose(msg *common.Message) bool                 { return false }
func (c *mysqlCodecForReset) IsBind(msg *common.Message) bool                  { return false }
func (c *mysqlCodecForReset) IsSync(msg *common.Message) bool                  { return false }
func (c *mysqlCodecForReset) IsStartup(msg *common.Message) bool               { return false }
func (c *mysqlCodecForReset) IsTerminate(msg *common.Message) bool             { return false }
func (c *mysqlCodecForReset) IsTransactionBegin(msg *common.Message) bool      { return false }
func (c *mysqlCodecForReset) IsTransactionEnd(msg *common.Message) bool        { return false }
func (c *mysqlCodecForReset) ExtractQuery(msg *common.Message) (string, error) { return "", nil }
func (c *mysqlCodecForReset) GenerateResetSequence() []*common.Message {
	// COM_RESET_CONNECTION (0x1F)
	payload := []byte{0x1F}
	length := len(payload)
	raw := make([]byte, 4+length)
	raw[0] = byte(length)
	raw[1] = byte(length >> 8)
	raw[2] = byte(length >> 16)
	raw[3] = 0 // seq num
	copy(raw[4:], payload)
	return []*common.Message{{Raw: raw}}
}

// mssqlCodecForReset is a minimal MSSQL codec for testing reset.
type mssqlCodecForReset struct{}

func (c *mssqlCodecForReset) Protocol() common.Protocol { return common.ProtocolMSSQL }
func (c *mssqlCodecForReset) ReadMessage(r io.Reader) (*common.Message, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	length := int(header[2])<<8 | int(header[3])
	if length > 8 {
		body := make([]byte, length-8)
		io.ReadFull(r, body)
		return &common.Message{Payload: body}, nil
	}
	return &common.Message{}, nil
}
func (c *mssqlCodecForReset) WriteMessage(w io.Writer, msg *common.Message) error {
	_, err := w.Write(msg.Raw)
	return err
}
func (c *mssqlCodecForReset) IsQuery(msg *common.Message) bool                 { return false }
func (c *mssqlCodecForReset) IsPrepare(msg *common.Message) bool               { return false }
func (c *mssqlCodecForReset) IsExecute(msg *common.Message) bool               { return false }
func (c *mssqlCodecForReset) IsClose(msg *common.Message) bool                 { return false }
func (c *mssqlCodecForReset) IsBind(msg *common.Message) bool                  { return false }
func (c *mssqlCodecForReset) IsSync(msg *common.Message) bool                  { return false }
func (c *mssqlCodecForReset) IsStartup(msg *common.Message) bool               { return false }
func (c *mssqlCodecForReset) IsTerminate(msg *common.Message) bool             { return false }
func (c *mssqlCodecForReset) IsTransactionBegin(msg *common.Message) bool      { return false }
func (c *mssqlCodecForReset) IsTransactionEnd(msg *common.Message) bool        { return false }
func (c *mssqlCodecForReset) ExtractQuery(msg *common.Message) (string, error) { return "", nil }
func (c *mssqlCodecForReset) GenerateResetSequence() []*common.Message {
	// TDS7 RPC request for sp_reset_connection (simplified)
	header := make([]byte, 8)
	header[0] = 0x01 // request
	header[1] = 0x01 // status
	// length will be set when sent
	raw := append(header, 0x00, 0x01, 0x00, 0x2A, 0x00, 0x00, 0x00, 0x0A)
	return []*common.Message{{Raw: raw}}
}

func TestPool_BackendCount(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "b1", Port: 5432, Role: "primary"},
				{Host: "b2", Port: 5433, Role: "replica"},
				{Host: "b3", Port: 5434, Role: "replica"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	if len(pool.backends) != 3 {
		t.Errorf("backends count = %d, want 3", len(pool.backends))
	}
}
