package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

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

// TestToolPoolList tests the toolPoolList function with pools
func TestToolPoolList(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}

	t.Run("no_pools", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)
		result := s.toolPoolList()
		if !contains(result, "No pools configured") {
			t.Errorf("result should contain 'No pools configured', got: %s", result)
		}
	})

	t.Run("with_pools", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)

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

		result := s.toolPoolList()
		if !contains(result, "Connection Pools:") {
			t.Errorf("result should contain 'Connection Pools:', got: %s", result)
		}
		if !contains(result, "test-pool") {
			t.Errorf("result should contain pool name, got: %s", result)
		}
		if !contains(result, "Mode:") {
			t.Errorf("result should contain 'Mode:', got: %s", result)
		}
		if !contains(result, "Clients:") {
			t.Errorf("result should contain 'Clients:', got: %s", result)
		}
		if !contains(result, "Backends:") {
			t.Errorf("result should contain 'Backends:', got: %s", result)
		}
	})
}

// TestResourcePool tests the resourcePool function
func TestResourcePool(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}

	t.Run("pool_not_found", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)
		result := s.resourcePool("nonexistent")
		if !contains(result, "not found") {
			t.Errorf("result should contain 'not found', got: %s", result)
		}
		// Verify it's valid JSON with error key
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("result should be valid JSON, got error: %v, result: %s", err, result)
		}
		if parsed["error"] == nil {
			t.Error("JSON should contain an 'error' key")
		}
	})

	t.Run("existing_pool_with_backends", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)

		poolCfg := &config.PoolConfig{
			Name: "my-pool",
			Body: "postgresql",
			Mode: "session",
			Listen: config.ListenConfig{
				Host: "127.0.0.1",
				Port: 0,
			},
			Backend: config.BackendConfig{
				Hosts: []config.BackendHost{
					{Host: "127.0.0.1", Port: 5432, Role: "primary"},
					{Host: "127.0.0.2", Port: 5433, Role: "replica"},
				},
			},
		}
		pm.CreatePool(poolCfg)

		result := s.resourcePool("my-pool")
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("result should be valid JSON, got error: %v, result: %s", err, result)
		}

		if parsed["name"] != "my-pool" {
			t.Errorf("name = %v, want my-pool", parsed["name"])
		}
		if parsed["mode"] != "session" {
			t.Errorf("mode = %v, want session", parsed["mode"])
		}
		backends, ok := parsed["backends"].([]interface{})
		if !ok {
			t.Fatal("backends should be an array")
		}
		if len(backends) != 2 {
			t.Errorf("backends length = %d, want 2", len(backends))
		}
		// Check backend details
		b0 := backends[0].(map[string]interface{})
		if b0["address"] != "127.0.0.1:5432" {
			t.Errorf("backend[0] address = %v, want 127.0.0.1:5432", b0["address"])
		}
		if b0["role"] != "primary" {
			t.Errorf("backend[0] role = %v, want primary", b0["role"])
		}
	})
}

// TestResourceStatsOverview tests the resourceStatsOverview function
func TestResourceStatsOverview(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}

	t.Run("no_pools", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)
		result := s.resourceStatsOverview()
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("result should be valid JSON, got error: %v", err)
		}
		if parsed["active_pools"].(float64) != 0 {
			t.Errorf("active_pools = %v, want 0", parsed["active_pools"])
		}
		if parsed["total_clients"].(float64) != 0 {
			t.Errorf("total_clients = %v, want 0", parsed["total_clients"])
		}
	})

	t.Run("with_pools", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)

		poolCfg := &config.PoolConfig{
			Name: "stats-pool",
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

		result := s.resourceStatsOverview()
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("result should be valid JSON, got error: %v", err)
		}
		if parsed["active_pools"].(float64) != 1 {
			t.Errorf("active_pools = %v, want 1", parsed["active_pools"])
		}
		if _, ok := parsed["timestamp"]; !ok {
			t.Error("result should contain timestamp")
		}
		if _, ok := parsed["total_queries"]; !ok {
			t.Error("result should contain total_queries")
		}
		if _, ok := parsed["total_transactions"]; !ok {
			t.Error("result should contain total_transactions")
		}
	})
}

// TestToolBackendDrain_Success tests the successful drain path of toolBackendDrain
func TestToolBackendDrain_Success(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	poolCfg := &config.PoolConfig{
		Name: "drain-pool",
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

	result := s.toolBackendDrain("127.0.0.1:5432")
	if !contains(result, "Draining initiated") {
		t.Errorf("result should indicate draining started, got: %s", result)
	}
	if !contains(result, "127.0.0.1:5432") {
		t.Errorf("result should contain address, got: %s", result)
	}
	if !contains(result, "Active connections:") {
		t.Errorf("result should contain active connections info, got: %s", result)
	}
}

// TestToolBackendDrain_AlreadyDraining tests drain when backend is already draining
func TestToolBackendDrain_AlreadyDraining(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	poolCfg := &config.PoolConfig{
		Name: "drain-pool2",
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

	// First drain should succeed
	result1 := s.toolBackendDrain("127.0.0.1:5432")
	if !contains(result1, "Draining initiated") {
		t.Fatalf("first drain should succeed, got: %s", result1)
	}

	// Second drain should fail with already draining
	result2 := s.toolBackendDrain("127.0.0.1:5432")
	if !contains(result2, "Error") {
		t.Errorf("second drain should report error, got: %s", result2)
	}
	if !contains(result2, "already draining") {
		t.Errorf("result should say already draining, got: %s", result2)
	}
}

// TestPeriodicCleanup tests the periodicCleanup function of mcpRateLimiter
func TestPeriodicCleanup(t *testing.T) {
	rl := newMCPRateLimiter()
	// Override cleanupTTL to be very short for testing
	rl.mu.Lock()
	rl.cleanupTTL = 50 * time.Millisecond
	rl.mu.Unlock()

	// Add limiters
	rl.GetLimiter("10.0.0.1")
	rl.GetLimiter("10.0.0.2")

	// Verify they exist
	rl.mu.Lock()
	initialCount := len(rl.limiters)
	rl.mu.Unlock()
	if initialCount != 2 {
		t.Fatalf("expected 2 limiters, got %d", initialCount)
	}

	// Manually set lastSeen to the past so they get cleaned up
	rl.mu.Lock()
	rl.lastSeen["10.0.0.1"] = time.Now().Add(-1 * time.Hour)
	rl.lastSeen["10.0.0.2"] = time.Now().Add(-1 * time.Hour)
	rl.mu.Unlock()

	// Manually trigger cleanup (don't rely on ticker which has 5min interval)
	rl.doCleanup()

	// Old entries should be cleaned up
	rl.mu.Lock()
	afterCount := len(rl.limiters)
	rl.mu.Unlock()
	if afterCount != 0 {
		t.Errorf("expected 0 limiters after cleanup, got %d", afterCount)
	}
}

// TestHandleToolsCall_AllTools tests handleToolsCall via HTTP for all tool branches
func TestHandleToolsCall_AllTools(t *testing.T) {
	log, _ := logger.New("error", "json")

	setup := func(t *testing.T) (*Server, string) {
		t.Helper()
		addr := bindRandomPort(t)
		cfg := &config.AdminMCPConfig{
			Listen: addr,
			Auth:   config.RESTAuthConfig{Enabled: false},
		}
		pm := pool.NewManager(log)

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

		reloadFn := func() error { return nil }
		s := NewServer(cfg, pm, log, reloadFn)
		if err := s.Start(); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
		return s, addr
	}

	tools := []struct {
		name string
		body string
	}{
		{"geryon_pool_list", `{"name":"geryon_pool_list","arguments":{}}`},
		{"geryon_pool_stats", `{"name":"geryon_pool_stats","arguments":{"name":"test-pool"}}`},
		{"geryon_connection_list", `{"name":"geryon_connection_list","arguments":{}}`},
		{"geryon_backend_list", `{"name":"geryon_backend_list","arguments":{}}`},
		{"geryon_cache_stats", `{"name":"geryon_cache_stats","arguments":{}}`},
		{"geryon_config_reload", `{"name":"geryon_config_reload","arguments":{}}`},
		{"geryon_query_stats", `{"name":"geryon_query_stats","arguments":{}}`},
	}

	for _, tt := range tools {
		t.Run(tt.name, func(t *testing.T) {
			s, addr := setup(t)
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				s.Stop(ctx)
			}()

			resp, err := http.Post("http://"+addr+"/mcp/v1/tools/call", "application/json", strings.NewReader(tt.body))
			if err != nil {
				t.Fatalf("ToolCall failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("Status = %d, want 200", resp.StatusCode)
			}

			var data map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
				t.Fatalf("JSON decode failed: %v", err)
			}

			content, ok := data["content"].([]interface{})
			if !ok || len(content) == 0 {
				t.Fatal("content array should not be empty")
			}
			c := content[0].(map[string]interface{})
			text, _ := c["text"].(string)
			if text == "" {
				t.Error("tool result text should not be empty")
			}

			if isError, ok := data["isError"].(bool); ok && isError {
				t.Errorf("isError should be false for %s, got true", tt.name)
			}
		})
	}
}

// TestHandleToolsCall_BackendDrain tests the backend_drain tool via HTTP
func TestHandleToolsCall_BackendDrain(t *testing.T) {
	log, _ := logger.New("error", "json")
	addr := bindRandomPort(t)
	cfg := &config.AdminMCPConfig{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "drain-test",
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

	s := NewServer(cfg, pm, log, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.Stop(ctx)
	}()
	time.Sleep(10 * time.Millisecond)

	body := `{"name":"geryon_backend_drain","arguments":{"address":"127.0.0.1:5432"}}`
	resp, err := http.Post("http://"+addr+"/mcp/v1/tools/call", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("ToolCall failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	content, _ := data["content"].([]interface{})
	c := content[0].(map[string]interface{})
	text := c["text"].(string)
	if !contains(text, "Draining initiated") {
		t.Errorf("expected draining to be initiated, got: %s", text)
	}
}

// TestResourcePoolViaHTTP tests reading a pool-specific resource via HTTP
func TestResourcePoolViaHTTP(t *testing.T) {
	log, _ := logger.New("error", "json")
	addr := bindRandomPort(t)
	cfg := &config.AdminMCPConfig{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "res-pool",
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

	s := NewServer(cfg, pm, log, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.Stop(ctx)
	}()
	time.Sleep(10 * time.Millisecond)

	// Read the pool-specific resource
	body := `{"uri":"geryon://pools/res-pool"}`
	resp, err := http.Post("http://"+addr+"/mcp/v1/resources/read", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("ResourcesRead failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	contents, _ := data["contents"].([]interface{})
	if len(contents) == 0 {
		t.Fatal("contents should not be empty")
	}
	rc := contents[0].(map[string]interface{})
	text := rc["text"].(string)

	// Verify it's valid JSON with pool details
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("resource text should be valid JSON, got error: %v", err)
	}
	if parsed["name"] != "res-pool" {
		t.Errorf("name = %v, want res-pool", parsed["name"])
	}
}
