package mcp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

// Helper function
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// TestToolPoolStats tests the toolPoolStats function
func TestToolPoolStats(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	t.Run("pool_not_found", func(t *testing.T) {
		result := s.toolPoolStats("nonexistent")
		if result == "" {
			t.Error("expected non-empty result for non-existent pool")
		}
		if !contains(result, "not found") {
			t.Errorf("result should contain 'not found', got: %s", result)
		}
	})

	t.Run("existing_pool", func(t *testing.T) {
		// Create a pool first
		poolCfg := &config.PoolConfig{
			Name: "test-pool",
			Body: "postgresql",
			Mode: "transaction",
			Listen: config.ListenConfig{
				Host: "127.0.0.1",
				Port: 0,
			},
			Backend: config.BackendConfig{
				Hosts: []config.BackendHost{
					{Host: "127.0.0.1", Port: 5432, Role: "primary"},
				},
			},
		}
		pm.CreatePool(poolCfg)

		result := s.toolPoolStats("test-pool")
		if result == "" {
			t.Error("expected non-empty result")
		}
		if !contains(result, "test-pool") {
			t.Errorf("result should contain pool name, got: %s", result)
		}
		if !contains(result, "Mode:") {
			t.Errorf("result should contain Mode, got: %s", result)
		}
	})
}

// TestToolConnectionList tests the toolConnectionList function
func TestToolConnectionList(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}

	t.Run("no_pools", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)
		result := s.toolConnectionList()
		if !contains(result, "No pools") {
			t.Errorf("result should contain 'No pools', got: %s", result)
		}
	})

	t.Run("with_pools", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)

		// Create pools
		poolCfg := &config.PoolConfig{
			Name: "test-pool",
			Body: "postgresql",
			Mode: "transaction",
			Listen: config.ListenConfig{
				Host: "127.0.0.1",
				Port: 0,
			},
			Backend: config.BackendConfig{
				Hosts: []config.BackendHost{
					{Host: "127.0.0.1", Port: 5432, Role: "primary"},
				},
			},
		}
		pm.CreatePool(poolCfg)

		result := s.toolConnectionList()
		if !contains(result, "Active Connections") {
			t.Errorf("result should contain 'Active Connections', got: %s", result)
		}
		if !contains(result, "test-pool") {
			t.Errorf("result should contain pool name, got: %s", result)
		}
	})
}

// TestToolBackendList tests the toolBackendList function
func TestToolBackendList(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}

	t.Run("no_pools", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)
		result := s.toolBackendList()
		if !contains(result, "No pools") {
			t.Errorf("result should contain 'No pools', got: %s", result)
		}
	})

	t.Run("with_backends", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)

		// Create pool with backend
		poolCfg := &config.PoolConfig{
			Name: "test-pool",
			Body: "postgresql",
			Mode: "transaction",
			Listen: config.ListenConfig{
				Host: "127.0.0.1",
				Port: 0,
			},
			Backend: config.BackendConfig{
				Hosts: []config.BackendHost{
					{Host: "127.0.0.1", Port: 5432, Role: "primary"},
				},
			},
		}
		pm.CreatePool(poolCfg)

		result := s.toolBackendList()
		if !contains(result, "Backends:") {
			t.Errorf("result should contain 'Backends:', got: %s", result)
		}
		if !contains(result, "test-pool") {
			t.Errorf("result should contain pool name, got: %s", result)
		}
		if !contains(result, "Legend:") {
			t.Errorf("result should contain Legend, got: %s", result)
		}
	})
}

// TestToolBackendDrain tests the toolBackendDrain function
func TestToolBackendDrain(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	t.Run("invalid_address_format", func(t *testing.T) {
		result := s.toolBackendDrain("invalid")
		if !contains(result, "invalid") {
			t.Errorf("result should indicate invalid format, got: %s", result)
		}
	})

	t.Run("pool_not_found", func(t *testing.T) {
		result := s.toolBackendDrain("127.0.0.1:5432")
		if !contains(result, "not found") {
			t.Errorf("result should indicate backend not found, got: %s", result)
		}
	})

	t.Run("backend_not_found", func(t *testing.T) {
		// Create pool without the specific backend
		poolCfg := &config.PoolConfig{
			Name: "test-pool",
			Body: "postgresql",
			Mode: "transaction",
			Listen: config.ListenConfig{
				Host: "127.0.0.1",
				Port: 0,
			},
			Backend: config.BackendConfig{
				Hosts: []config.BackendHost{
					{Host: "127.0.0.1", Port: 5432, Role: "primary"},
				},
			},
		}
		pm.CreatePool(poolCfg)

		result := s.toolBackendDrain("192.168.1.1:5432")
		if !contains(result, "not found") {
			t.Errorf("result should indicate backend not found, got: %s", result)
		}
	})
}

// TestToolCacheStats tests the toolCacheStats function
func TestToolCacheStats(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}

	t.Run("no_pools", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)
		result := s.toolCacheStats()
		// Function returns formatted stats even with 0 pools
		if !contains(result, "Cache Statistics") {
			t.Errorf("result should contain 'Cache Statistics', got: %s", result)
		}
		if !contains(result, "Pools with Cache: 0") {
			t.Errorf("result should show 0 pools, got: %s", result)
		}
	})

	t.Run("with_pools", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)

		// Create pool
		poolCfg := &config.PoolConfig{
			Name: "test-pool",
			Body: "postgresql",
			Mode: "transaction",
			Listen: config.ListenConfig{
				Host: "127.0.0.1",
				Port: 0,
			},
			Backend: config.BackendConfig{
				Hosts: []config.BackendHost{
					{Host: "127.0.0.1", Port: 5432, Role: "primary"},
				},
			},
		}
		pm.CreatePool(poolCfg)

		result := s.toolCacheStats()
		if !contains(result, "Cache Statistics") {
			t.Errorf("result should contain 'Cache Statistics', got: %s", result)
		}
	})
}

// TestToolConfigReload tests the toolConfigReload function
func TestToolConfigReload(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}

	t.Run("no_reload_fn", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)
		result := s.toolConfigReload()
		if !contains(result, "not configured") {
			t.Errorf("result should indicate reload not configured, got: %s", result)
		}
	})

	t.Run("reload_success", func(t *testing.T) {
		pm := pool.NewManager(log)
		reloadFn := func() error { return nil }
		s := NewServer(cfg, pm, log, reloadFn)
		result := s.toolConfigReload()
		if !contains(result, "successful") {
			t.Errorf("result should indicate success, got: %s", result)
		}
	})

	t.Run("reload_error", func(t *testing.T) {
		pm := pool.NewManager(log)
		reloadFn := func() error { return fmt.Errorf("reload failed") }
		s := NewServer(cfg, pm, log, reloadFn)
		result := s.toolConfigReload()
		if !contains(result, "failed") {
			t.Errorf("result should indicate failure, got: %s", result)
		}
	})
}

// TestToolQueryStats tests the toolQueryStats function
func TestToolQueryStats(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}

	t.Run("no_pools", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)
		result := s.toolQueryStats()
		// Function returns formatted stats even with 0 pools
		if !contains(result, "Query Statistics") {
			t.Errorf("result should contain 'Query Statistics', got: %s", result)
		}
		if !contains(result, "Total Queries: 0") {
			t.Errorf("result should show 0 queries, got: %s", result)
		}
	})

	t.Run("with_pools", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)

		// Create pool
		poolCfg := &config.PoolConfig{
			Name: "test-pool",
			Body: "postgresql",
			Mode: "transaction",
			Listen: config.ListenConfig{
				Host: "127.0.0.1",
				Port: 0,
			},
			Backend: config.BackendConfig{
				Hosts: []config.BackendHost{
					{Host: "127.0.0.1", Port: 5432, Role: "primary"},
				},
			},
		}
		pm.CreatePool(poolCfg)

		result := s.toolQueryStats()
		if !contains(result, "Query Statistics") {
			t.Errorf("result should contain 'Query Statistics', got: %s", result)
		}
	})
}
