package dashboard

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

// bindRandomPort returns a free TCP port address.
func bindRandomPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to bind random port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestConfig(t *testing.T) {
	cfg := &Config{
		Enabled: true,
		Listen:  "127.0.0.1:8082",
		Path:    "/",
		Auth:    config.RESTAuthConfig{Enabled: false},
	}
	if !cfg.Enabled {
		t.Error("Should be enabled")
	}
	if cfg.Listen != "127.0.0.1:8082" {
		t.Errorf("Listen = %q, want 127.0.0.1:8082", cfg.Listen)
	}
}

func TestNewServer(t *testing.T) {
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: "127.0.0.1:0", Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestServer_Disabled(t *testing.T) {
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: false, Listen: "127.0.0.1:0", Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	// Start should succeed but do nothing
	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
}

func TestServer_StartStop(t *testing.T) {
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: "127.0.0.1:0", Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	err = s.Stop()
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestDashboard_HealthEndpoint(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	resp, err := http.Get("http://" + cfg.Listen + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["status"] != "healthy" {
		t.Errorf("status = %q, want healthy", data["status"])
	}
}

func TestDashboard_StatsEndpoint(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	resp, err := http.Get("http://" + cfg.Listen + "/api/v1/stats")
	if err != nil {
		t.Fatalf("GET /stats failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestDashboard_PoolsEndpoint(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	resp, err := http.Get("http://" + cfg.Listen + "/api/v1/pools")
	if err != nil {
		t.Fatalf("GET /pools failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestDashboard_BackendsEndpoint(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	resp, err := http.Get("http://" + cfg.Listen + "/api/v1/backends")
	if err != nil {
		t.Fatalf("GET /backends failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestDashboard_ConnectionsEndpoint(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	resp, err := http.Get("http://" + cfg.Listen + "/api/v1/connections")
	if err != nil {
		t.Fatalf("GET /connections failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestDashboard_QueriesEndpoint(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	resp, err := http.Get("http://" + cfg.Listen + "/api/v1/queries")
	if err != nil {
		t.Fatalf("GET /queries failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestDashboard_ConfigEndpoint(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	resp, err := http.Get("http://" + cfg.Listen + "/api/v1/config")
	if err != nil {
		t.Fatalf("GET /config failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["dashboard"] != true {
		t.Errorf("dashboard = %v, want true", data["dashboard"])
	}
}

func TestDashboard_ConfigReload(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	reloaded := false
	s := NewServer(cfg, pm, log, func() error {
		reloaded = true
		return nil
	})

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	resp, err := http.Post("http://"+cfg.Listen+"/api/v1/config", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
	if !reloaded {
		t.Error("Reload function should have been called")
	}
}

func TestDashboard_Auth_RejectsWithoutToken(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: true, Token: "dashboard-secret"}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	// Without auth token
	resp, err := http.Get("http://" + cfg.Listen + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", resp.StatusCode)
	}

	// With correct auth token
	req, _ := http.NewRequest("GET", "http://"+cfg.Listen+"/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer dashboard-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Authenticated GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestDashboard_SecurityHeaders(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	resp, err := http.Get("http://" + cfg.Listen + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("Missing X-Content-Type-Options header")
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("Missing X-Frame-Options header")
	}
}

func TestWriteJSON(t *testing.T) {
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: "127.0.0.1:0", Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	rr := httptest.NewRecorder()
	s.writeJSON(rr, map[string]string{"key": "value"})

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["key"] != "value" {
		t.Errorf("body[key] = %q, want value", data["key"])
	}
}

func TestDashboardRateLimiter(t *testing.T) {
	rl := newDashboardRateLimiter(5, 15)
	if rl == nil {
		t.Fatal("newDashboardRateLimiter returned nil")
	}

	l1 := rl.GetLimiter("10.0.0.1")
	if l1 == nil {
		t.Error("GetLimiter returned nil")
	}

	// Same IP returns same limiter
	l2 := rl.GetLimiter("10.0.0.1")
	if l1 != l2 {
		t.Error("Same IP should return same limiter")
	}

	// Different IP returns different limiter
	l3 := rl.GetLimiter("10.0.0.2")
	if l1 == l3 {
		t.Error("Different IP should return different limiter")
	}
}

// TestHandleConnections tests the handleConnections endpoint
func TestHandleConnections(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &Config{
		Enabled: true,
		Listen:  "127.0.0.1:0",
		Auth:    config.RESTAuthConfig{Enabled: false},
	}

	t.Run("no_pools", func(t *testing.T) {
		pm := pool.NewManager(log)
		s := NewServer(cfg, pm, log, nil)

		req := httptest.NewRequest("GET", "/api/connections", nil)
		rr := httptest.NewRecorder()

		s.handleConnections(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
			t.Fatalf("JSON decode failed: %v", err)
		}

		// connections can be nil or an array
		connections, _ := result["connections"].([]interface{})
		if connections == nil {
			connections = []interface{}{}
		}
		if len(connections) != 0 {
			t.Errorf("connections length = %d, want 0", len(connections))
		}
	})

	t.Run("with_pools", func(t *testing.T) {
		pm := pool.NewManager(log)

		// Create a pool
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

		s := NewServer(cfg, pm, log, nil)

		req := httptest.NewRequest("GET", "/api/connections", nil)
		rr := httptest.NewRecorder()

		s.handleConnections(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
			t.Fatalf("JSON decode failed: %v", err)
		}

		connections, ok := result["connections"].([]interface{})
		if !ok {
			t.Fatal("connections should be an array")
		}
		if len(connections) == 0 {
			t.Error("connections should not be empty")
		}

		// Check first connection has expected fields
		if len(connections) > 0 {
			first, ok := connections[0].(map[string]interface{})
			if !ok {
				t.Fatal("first connection should be an object")
			}
			if _, ok := first["pool_name"]; !ok {
				t.Error("first connection should have pool_name")
			}
		}
	})
}

func TestDashboard_HandleIndex(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "text")
	cfg := &Config{Enabled: true, Listen: addr, Auth: config.RESTAuthConfig{Enabled: false}}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	t.Run("root path returns HTML", func(t *testing.T) {
		resp, err := http.Get("http://" + cfg.Listen + "/")
		if err != nil {
			t.Fatalf("GET / failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}

		contentType := resp.Header.Get("Content-Type")
		if contentType != "text/html" {
			t.Errorf("Content-Type = %q, want text/html", contentType)
		}
	})

	t.Run("index.html returns HTML", func(t *testing.T) {
		resp, err := http.Get("http://" + cfg.Listen + "/index.html")
		if err != nil {
			t.Fatalf("GET /index.html failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("unknown path returns 404", func(t *testing.T) {
		resp, err := http.Get("http://" + cfg.Listen + "/nonexistent")
		if err != nil {
			t.Fatalf("GET /nonexistent failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Status = %d, want 404", resp.StatusCode)
		}
	})
}
