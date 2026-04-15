package benchmarks

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

// mockBackendListener creates a mock TCP server for benchmarking.
func mockBackendListener(t testing.TB) net.Listener {
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
			// Echo back a simple response
			buf := make([]byte, 1024)
			conn.Read(buf)
			conn.Write([]byte("OK"))
			conn.Close()
		}
	}()
	return listener
}

// BenchmarkPoolAcquireRelease benchmarks concurrent acquire/release operations.
// This simulates the core pooling operation under load.
func BenchmarkPoolAcquireRelease(b *testing.B) {
	listener := mockBackendListener(b)
	defer listener.Close()

	host, portStr, _ := net.SplitHostPort(listener.Addr().String())
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	cfg := &config.PoolConfig{
		Name: "benchmark-pool",
		Mode: "session",
		Body: "postgresql",
		Limits: config.LimitConfig{
			MaxServerConnections: 100,
		},
		Backend: config.BackendConfig{
			Database: "testdb",
			Hosts: []config.BackendHost{
				{Host: host, Port: port, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	p, err := pool.NewPool(cfg, nil, log)
	if err != nil {
		b.Fatalf("NewPool failed: %v", err)
	}
	defer p.Close()

	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			conn, err := p.Acquire(ctx)
			if err != nil {
				b.Fatalf("Acquire failed: %v", err)
			}
			p.Release(conn)
		}
	})
}

// BenchmarkPoolAcquireReleaseWithLatency benchmarks acquire/release with simulated network latency.
func BenchmarkPoolAcquireReleaseWithLatency(b *testing.B) {
	listener := mockBackendListener(b)
	defer listener.Close()

	host, portStr, _ := net.SplitHostPort(listener.Addr().String())
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	cfg := &config.PoolConfig{
		Name: "benchmark-pool-latency",
		Mode: "session",
		Body: "postgresql",
		Limits: config.LimitConfig{
			MaxServerConnections: 100,
		},
		Backend: config.BackendConfig{
			Database: "testdb",
			Hosts: []config.BackendHost{
				{Host: host, Port: port, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	p, err := pool.NewPool(cfg, nil, log)
	if err != nil {
		b.Fatalf("NewPool failed: %v", err)
	}
	defer p.Close()

	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			conn, err := p.Acquire(ctx)
			if err != nil {
				b.Fatalf("Acquire failed: %v", err)
			}
			// Simulate 1ms latency
			time.Sleep(time.Millisecond)
			p.Release(conn)
		}
	})
}

// BenchmarkPoolConnectionReuse measures how well connections are reused.
func BenchmarkPoolConnectionReuse(b *testing.B) {
	listener := mockBackendListener(b)
	defer listener.Close()

	host, portStr, _ := net.SplitHostPort(listener.Addr().String())
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	cfg := &config.PoolConfig{
		Name: "benchmark-pool-reuse",
		Mode: "session",
		Body: "postgresql",
		Limits: config.LimitConfig{
			MinServerConnections: 10,
			MaxServerConnections: 50,
		},
		Backend: config.BackendConfig{
			Database: "testdb",
			Hosts: []config.BackendHost{
				{Host: host, Port: port, Role: "primary"},
			},
		},
	}

	log, _ := logger.New("error", "json")
	p, err := pool.NewPool(cfg, nil, log)
	if err != nil {
		b.Fatalf("NewPool failed: %v", err)
	}
	defer p.Close()

	ctx := context.Background()

	// Pre-fill the pool
	conns := make([]*pool.ServerConn, 0, 10)
	for i := 0; i < 10; i++ {
		conn, err := p.Acquire(ctx)
		if err != nil {
			b.Fatalf("Pre-fill acquire failed: %v", err)
		}
		conns = append(conns, conn)
	}
	for _, conn := range conns {
		p.Release(conn)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			conn, err := p.Acquire(ctx)
			if err != nil {
				b.Fatalf("Acquire failed: %v", err)
			}
			p.Release(conn)
		}
	})
}
