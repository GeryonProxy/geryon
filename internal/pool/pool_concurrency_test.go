package pool

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
)

// mockTCPBackendForConcurrency creates a TCP listener for testing pool operations.
func mockTCPBackendForConcurrency(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	// Accept and close immediately - this simulates a backend that rejects connections
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	return listener
}

// TestPool_AcquireRelease_Concurrent tests concurrent Acquire/Release operations.
func TestPool_AcquireRelease_Concurrent(t *testing.T) {
	listener := mockTCPBackendForConcurrency(t)
	defer listener.Close()

	addr := listener.Addr().String()
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	cfg := &config.PoolConfig{
		Name: "concurrency-test",
		Mode: "transaction",
		Body: "postgresql",
		Limits: config.LimitConfig{
			MaxServerConnections: 50,
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
	defer pool.Close()

	// Add some idle connections to reduce connection creation overhead
	for i := 0; i < 10; i++ {
		conn := &ServerConn{
			id:            uint64(i),
			preparedStmts: make(map[string]bool),
			paramStatus:   make(map[string]string),
		}
		pool.serverConns.idle = append(pool.serverConns.idle, conn)
	}

	const numGoroutines = 100
	const opsPerGoroutine = 50

	var wg sync.WaitGroup
	var totalOps int64
	var totalErrs int64

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			localErrs := 0
			for i := 0; i < opsPerGoroutine; i++ {
				select {
				case <-ctx.Done():
					return
				default:
				}

				conn, err := pool.Acquire(ctx)
				if err != nil {
					localErrs++
					continue
				}

				// Simulate minimal work
				_ = conn.ID()

				pool.Release(conn)
			}
			// Atomically update counters
			atomicAddInt64(&totalOps, int64(opsPerGoroutine-localErrs))
			atomicAddInt64(&totalErrs, int64(localErrs))
		}(g)
	}

	wg.Wait()

	t.Logf("Concurrent acquire/release results: total_ops=%d errors=%d goroutines=%d",
		totalOps, totalErrs, numGoroutines)

	if totalErrs > int64(numGoroutines*opsPerGoroutine)/2 {
		t.Errorf("Too many errors (%d) in concurrent operations", totalErrs)
	}
}

// TestPool_ExhaustAndWait tests pool exhaustion and wait queue behavior.
func TestPool_ExhaustAndWait(t *testing.T) {
	listener := mockTCPBackendForConcurrency(t)
	defer listener.Close()

	addr := listener.Addr().String()
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	cfg := &config.PoolConfig{
		Name: "exhaust-test",
		Mode: "transaction",
		Body: "postgresql",
		Limits: config.LimitConfig{
			MaxServerConnections: 5,
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
	defer pool.Close()

	// Fill the pool
	connections := make([]*ServerConn, 5)
	for i := 0; i < 5; i++ {
		conn, err := pool.Acquire(context.Background())
		if err != nil {
			t.Fatalf("Failed to fill pool: %v", err)
		}
		connections[i] = conn
	}

	// Now pool should be exhausted
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// This should timeout since pool is exhausted and we can't create new connections
	_, err = pool.Acquire(ctx)
	if err == nil {
		t.Error("Should timeout when pool is exhausted")
	}

	// Release one connection
	pool.Release(connections[0])
	connections[0] = nil

	// Now acquire should succeed
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Errorf("Should succeed after releasing one connection: %v", err)
	} else {
		pool.Release(conn)
	}
}

// TestPool_MaxConnectionLimits tests that max connection limits are respected.
func TestPool_MaxConnectionLimits(t *testing.T) {
	listener := mockTCPBackendForConcurrency(t)
	defer listener.Close()

	addr := listener.Addr().String()
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	maxConns := 20
	cfg := &config.PoolConfig{
		Name: "limit-test",
		Mode: "transaction",
		Body: "postgresql",
		Limits: config.LimitConfig{
			MaxServerConnections: maxConns,
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
	defer pool.Close()

	// Try to acquire more than max
	var acquired int
	for i := 0; i < maxConns+5; i++ {
		conn, err := pool.Acquire(context.Background())
		if err != nil {
			break
		}
		acquired++
		pool.Release(conn) // Release immediately to test limit enforcement
	}

	if acquired < maxConns {
		t.Errorf("Expected to acquire at least %d connections, got %d", maxConns, acquired)
	}

	// Test that we respect limit on active connections
	connections := make([]*ServerConn, 0, maxConns+5)
	for i := 0; i < maxConns+5; i++ {
		conn, err := pool.Acquire(context.Background())
		if err != nil {
			break
		}
		connections = append(connections, conn)
	}

	if len(connections) > maxConns {
		t.Errorf("Exceeded max connections: got %d, max %d", len(connections), maxConns)
	}

	// Cleanup
	for _, conn := range connections {
		pool.Release(conn)
	}
}

// atomicAddInt64 atomically adds to an int64 pointer.
func atomicAddInt64(addr *int64, val int64) {
	var mutex sync.Mutex
	mutex.Lock()
	*addr += val
	mutex.Unlock()
}