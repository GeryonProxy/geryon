package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/cluster"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

func TestDashboard_PeriodicCleanup(t *testing.T) {
	rl := newDashboardRateLimiter(10, 5)
	rl.mu.Lock()
	rl.cleanupTTL = 50 * time.Millisecond
	rl.mu.Unlock()

	rl.GetLimiter("10.0.0.1")
	rl.GetLimiter("10.0.0.2")

	rl.mu.Lock()
	initialCount := len(rl.limiters)
	rl.mu.Unlock()
	if initialCount != 2 {
		t.Fatalf("expected 2 limiters, got %d", initialCount)
	}

	rl.mu.Lock()
	rl.lastSeen["10.0.0.1"] = time.Now().Add(-1 * time.Hour)
	rl.lastSeen["10.0.0.2"] = time.Now().Add(-1 * time.Hour)
	rl.mu.Unlock()

	time.Sleep(150 * time.Millisecond)

	rl.mu.Lock()
	afterCount := len(rl.limiters)
	rl.mu.Unlock()
	if afterCount != 0 {
		t.Errorf("expected 0 limiters after cleanup, got %d", afterCount)
	}
}

func TestDashboard_WithAuth_WrongToken(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: "secret"}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	req, _ := http.NewRequest("GET", "http://"+cfg.Listen+"/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer wrongtoken")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", resp.StatusCode)
	}
}

func TestDashboard_WithAuth_InvalidFormat(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: "secret"}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	req, _ := http.NewRequest("GET", "http://"+cfg.Listen+"/api/v1/health", nil)
	req.Header.Set("Authorization", "Basic secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", resp.StatusCode)
	}
}

func TestDashboard_HandleStats_WithPools(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)

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
	s := NewServer(cfg, pm, log, nil, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	req, _ := http.NewRequest("GET", "http://"+cfg.Listen+"/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /stats failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestDashboard_HandleBackends_WithPools(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "backend-pool",
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
	s := NewServer(cfg, pm, log, nil, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	req, _ := http.NewRequest("GET", "http://"+cfg.Listen+"/api/v1/backends", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /backends failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestDashboard_HandlePools_WithPools(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "test-pool-d",
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
	s := NewServer(cfg, pm, log, nil, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	req, _ := http.NewRequest("GET", "http://"+cfg.Listen+"/api/v1/pools", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /pools failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestDashboard_HandleQueries_WithPools(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "query-pool",
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
	s := NewServer(cfg, pm, log, nil, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	req, _ := http.NewRequest("GET", "http://"+cfg.Listen+"/api/v1/queries", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /queries failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestDashboard_HandleConfigReload_Error(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, func() error {
		return fmt.Errorf("reload failed")
	}, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	req, _ := http.NewRequest("POST", "http://"+cfg.Listen+"/api/v1/config", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	defer resp.Body.Close()

	// Dashboard returns 200 with error status in body
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["status"] != "error" {
		t.Errorf("status = %v, want error", data["status"])
	}
}

func TestDashboard_Stop_NilServer(t *testing.T) {
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: false, Listen: "127.0.0.1:0", Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	err = s.Stop()
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestDashboard_HandleConnections_WithPoolsData(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "conn-pool",
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
	s := NewServer(cfg, pm, log, nil, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	req, _ := http.NewRequest("GET", "http://"+cfg.Listen+"/api/v1/connections", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /connections failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
}

func TestDashboard_ConfigReload_NoReloadFn(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	req, _ := http.NewRequest("POST", "http://"+cfg.Listen+"/api/v1/config", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

// --- GetLimiter maxSize eviction ---

func TestDashboard_GetLimiter_MaxSizeEviction(t *testing.T) {
	rl := newDashboardRateLimiter(5, 15)
	rl.maxSize = 3

	// Fill to max
	rl.GetLimiter("10.0.0.1")
	time.Sleep(1 * time.Millisecond)
	rl.GetLimiter("10.0.0.2")
	time.Sleep(1 * time.Millisecond)
	rl.GetLimiter("10.0.0.3")

	if len(rl.limiters) != 3 {
		t.Fatalf("Expected 3 limiters, got %d", len(rl.limiters))
	}

	// Adding a 4th should evict the oldest
	rl.GetLimiter("10.0.0.4")

	rl.mu.Lock()
	count := len(rl.limiters)
	rl.mu.Unlock()
	if count != 3 {
		t.Errorf("Expected 3 limiters after eviction, got %d", count)
	}

	// 10.0.0.1 should have been evicted (oldest)
	rl.mu.Lock()
	_, exists := rl.limiters["10.0.0.1"]
	rl.mu.Unlock()
	if exists {
		t.Error("10.0.0.1 should have been evicted as oldest")
	}

	// 10.0.0.4 should exist
	rl.mu.Lock()
	_, exists = rl.limiters["10.0.0.4"]
	rl.mu.Unlock()
	if !exists {
		t.Error("10.0.0.4 should exist")
	}
}

// --- handleConnections with active transactions ---

func TestDashboard_HandleConnections_ActiveTxns(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "txn-pool",
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

	// Register a transaction to make ActiveTransactions > 0
	p := pm.GetPool("txn-pool")
	if p != nil && p.TransactionManager() != nil {
		p.TransactionManager().Register(1, 1, nil)
	}

	s := NewServer(cfg, pm, log, nil, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	req, _ := http.NewRequest("GET", "http://"+cfg.Listen+"/api/v1/connections", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /connections failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	connections, _ := data["connections"].([]interface{})
	if len(connections) < 2 {
		t.Errorf("Expected at least 2 connection entries (pool + txn), got %d", len(connections))
	}

	if p != nil && p.TransactionManager() != nil {
		p.TransactionManager().Stop()
	}
}

// --- handleStats with cache hit rate ---

func TestDashboard_HandleStats_WithCacheHitRate(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "cache-stats-pool",
		Body: "postgresql",
		Mode: "transaction",
		Cache: config.CacheConfig{
			Enabled: true,
		},
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

	// Manually set some query count to have non-zero stats
	p := pm.GetPool("cache-stats-pool")
	if p != nil {
		p.IncrementQueryCount()
		p.IncrementQueryCount()
		p.IncrementQueryCount()
	}

	s := NewServer(cfg, pm, log, nil, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	req, _ := http.NewRequest("GET", "http://"+cfg.Listen+"/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /stats failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	totalQueries, ok := data["total_queries"].(float64)
	if !ok {
		t.Fatal("total_queries should be a number")
	}
	if totalQueries < 3 {
		t.Errorf("total_queries = %v, want >= 3", totalQueries)
	}
}

// --- handleIndex with non-root path ---

func TestDashboard_HandleIndex_NonRootPath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &Config{Enabled: true, Listen: "127.0.0.1:0", Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	req := httptest.NewRequest("GET", "/some/random/path", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404 for non-root path", rr.Code)
	}
}

// --- withRateLimit with non-hostport RemoteAddr ---

func TestDashboard_WithRateLimit_NonHostPort(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &Config{Enabled: true, Listen: "127.0.0.1:0", Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	// Create a handler that records whether it was called
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := s.withRateLimit(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.RemoteAddr = "no-colon-or-port"
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if !called {
		t.Error("Handler should have been called even with non-hostport RemoteAddr")
	}
}

// --- ConfigReload with nil reloadFn via httptest ---

func TestDashboard_ConfigReload_NilReloadFn_Direct(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &Config{Enabled: true, Listen: "127.0.0.1:0", Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/config", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()

	s.handleConfigReload(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["status"] != "reloaded" {
		t.Errorf("status = %v, want reloaded", data["status"])
	}
}

func TestDashboard_HandleUsers_Get(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()

	s.handleUsers(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if users, ok := data["users"]; !ok {
		t.Error("response missing users field")
	} else if users == nil {
		t.Log("users is nil (no userDB), which is valid")
	}
}

func TestDashboard_HandleUsers_Post(t *testing.T) {
	// This test verifies the method routing works
	// userDB is nil so POST returns 500 (user database not available)
	// The actual user creation is tested in integration with auth package
	t.Skip("requires userDB which is nil in this test context")
}

func TestDashboard_HandleUsers_MethodNotAllowed(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	req := httptest.NewRequest("DELETE", "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()

	s.handleUsers(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", rr.Code)
	}
}

func TestDashboard_HandleCluster_NoCluster(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/cluster", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()

	s.handleCluster(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["status"] != "disabled" {
		t.Errorf("status = %v, want disabled", data["status"])
	}
}

func TestSanitizeErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"simple error", fmt.Errorf("connection refused"), "connection refused"},
		{"nil error", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeErr(tt.err)
			if tt.err != nil && got == "" {
				t.Errorf("sanitizeErr returned empty string for non-nil error")
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		def      time.Duration
		expected time.Duration
	}{
		{"", 5 * time.Second, 5 * time.Second},
		{"invalid", 5 * time.Second, 5 * time.Second},
		{"300ms", 5 * time.Second, 300 * time.Millisecond},
		{"1h30m", 5 * time.Second, 1*time.Hour + 30*time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseDuration(tt.input, tt.def)
			if got != tt.expected {
				t.Errorf("parseDuration(%q, %v) = %v, want %v", tt.input, tt.def, got, tt.expected)
			}
		})
	}
}

func TestDashboard_HandleConfigFile_Get(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/config/file", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()

	s.handleConfigFile(rr, req)

	if rr.Code != http.StatusOK && rr.Code != http.StatusNotFound && rr.Code != http.StatusNotImplemented {
		t.Errorf("Status = %d, want 200, 404, or 501", rr.Code)
	}
}

func TestDashboard_HandleConfigValidate(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	body := `pools: []`
	req := httptest.NewRequest("POST", "/api/v1/config/validate", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleConfigValidate(rr, req)

	if rr.Code != http.StatusOK && rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want 200 or 400", rr.Code)
	}
}

func TestDashboard_HandleTransactions(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/transactions", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()

	s.handleTransactions(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

func TestDashboard_HandleUserDetail(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/users/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()

	s.handleUserDetail(rr, req)

	if rr.Code != http.StatusOK && rr.Code != http.StatusNotFound && rr.Code != http.StatusNotImplemented {
		t.Errorf("Status = %d, want 200, 404, or 501", rr.Code)
	}
}

func TestDashboard_HandlePoolDetail(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	pm.CreatePool(&config.PoolConfig{Name: "test-pool", Mode: "transaction", Body: "postgresql"})
	s := NewServer(cfg, pm, log, nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/pools/test-pool", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

func TestDashboard_HandlePoolDetail_NotFound(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/pools/nonexistent-pool", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", rr.Code)
	}
}

func TestDashboard_HandlePoolDetail_BadPath(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/pools/", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", rr.Code)
	}
}

func TestDashboard_SetCluster(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	mockCluster := &mockDashboardCluster{}
	s.SetCluster(mockCluster)

	s.mu.RLock()
	if s.cluster == nil {
		t.Error("cluster was not set")
	}
	s.mu.RUnlock()
}

func TestDashboard_SetConfigPath(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	s.SetConfigPath("/etc/geryon.yaml")

	s.mu.RLock()
	if s.configPath != "/etc/geryon.yaml" {
		t.Errorf("configPath = %q, want /etc/geryon.yaml", s.configPath)
	}
	s.mu.RUnlock()
}

func TestDashboard_SetRefreshRouterFn(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	called := false
	fn := func() { called = true }
	s.SetRefreshRouterFn(fn)

	s.mu.RLock()
	if s.refreshRouterFn == nil {
		t.Error("refreshRouterFn was not set")
	}
	s.mu.RUnlock()

	s.refreshRouterFn()
	if !called {
		t.Error("refreshRouterFn was not called")
	}
}

func TestDashboard_HandlePoolDetail_BackendsGet(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "backends-pool",
		Body: "postgresql",
		Mode: "transaction",
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
	s := NewServer(cfg, pm, log, nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/pools/backends-pool/backends", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

func TestDashboard_HandlePoolDetail_BackendsPost_MissingFields(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	pm.CreatePool(&config.PoolConfig{Name: "test-pool", Mode: "transaction", Body: "postgresql"})
	s := NewServer(cfg, pm, log, nil, nil)

	body := `{"host": "", "port": 0}`
	req := httptest.NewRequest("POST", "/api/v1/pools/test-pool/backends", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", rr.Code)
	}
}

func TestDashboard_HandleListUsers(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: testToken}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()

	s.handleListUsers(rr, req)

	if rr.Code != http.StatusOK && rr.Code != http.StatusNotImplemented && rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 200, 404, or 501", rr.Code)
	}
}

// mockDashboardCluster implements dashboardCluster interface for testing
type mockDashboardCluster struct{}

func (m *mockDashboardCluster) StateString() string       { return "leader" }
func (m *mockDashboardCluster) GetNodeCount() int         { return 3 }
func (m *mockDashboardCluster) GetTerm() uint64           { return 10 }
func (m *mockDashboardCluster) GetNodes() []*cluster.Node { return nil }
func (m *mockDashboardCluster) GetLeader() string         { return "node1" }
