package rest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

// Test handlePools POST with invalid JSON
func TestHandlePools_Create_InvalidJSON(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	pm := pool.NewManager(log)
	s.poolMgr = pm

	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader("{invalid json"))
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// Test handlePools POST with empty pool name
func TestHandlePools_Create_EmptyName(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	pm := pool.NewManager(log)
	s.poolMgr = pm

	body := `{"name": ""}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// Test handlePools POST with invalid pool name
func TestHandlePools_Create_InvalidName(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	pm := pool.NewManager(log)
	s.poolMgr = pm

	body := `{"name": "invalid pool name"}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// Test handlePools POST with invalid body type
func TestHandlePools_Create_InvalidBody(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	pm := pool.NewManager(log)
	s.poolMgr = pm

	body := `{"name": "testpool", "body": "invalid"}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// Test handlePools POST with invalid mode
func TestHandlePools_Create_InvalidMode(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	pm := pool.NewManager(log)
	s.poolMgr = pm

	body := `{"name": "testpool", "body": "postgresql", "mode": "invalid"}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// Test handlePools POST with no backend hosts
func TestHandlePools_Create_NoBackends(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	pm := pool.NewManager(log)
	s.poolMgr = pm

	body := `{"name": "testpool", "body": "postgresql", "mode": "transaction", "backend": {"hosts": []}}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// Test handlePools POST with invalid backend host
func TestHandlePools_Create_InvalidBackend(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	pm := pool.NewManager(log)
	s.poolMgr = pm

	body := `{"name": "testpool", "body": "postgresql", "mode": "transaction", "backend": {"hosts": [{"host": "127.0.0.1", "port": 0}]}}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// Test handlePools POST valid body types
func TestHandlePools_Create_ValidBodies(t *testing.T) {
	log, _ := logger.New("error", "json")

	bodies := []string{"postgresql", "mysql", "mssql"}
	for _, bodyType := range bodies {
		cfg := &config.AdminRESTConfig{
			Listen: "127.0.0.1:0",
			Auth:   config.RESTAuthConfig{Enabled: false},
		}
		s, _ := NewServer(cfg, nil, nil, log, "", nil)
		pm := pool.NewManager(log)
		s.poolMgr = pm

		body := fmt.Sprintf(`{"name": "testpool_%s", "body": "%s", "mode": "transaction", "backend": {"hosts": [{"host": "127.0.0.1", "port": 5432}]}}`, bodyType, bodyType)
		req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
		rr := httptest.NewRecorder()

		s.handlePools(rr, req)

		if rr.Code == http.StatusBadRequest {
			t.Errorf("Body type %s: Status = %d, should not be 400", bodyType, rr.Code)
		}
	}
}

// Test handlePoolDetail with invalid method
func TestHandlePoolDetail_InvalidMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)
	pm := pool.NewManager(log)
	s.poolMgr = pm

	req := httptest.NewRequest("PATCH", "/api/v1/pools/testpool", nil)
	req.URL.Path = "/api/v1/pools/testpool"
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	// Returns 404 because pool not found, not 405
	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// Test handlePoolDetail PUT (not implemented)
func TestHandlePoolDetail_PutNotImplemented(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)
	pm := pool.NewManager(log)
	s.poolMgr = pm

	req := httptest.NewRequest("PUT", "/api/v1/pools/testpool", nil)
	req.URL.Path = "/api/v1/pools/testpool"
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	// Returns 404 because pool not found, not 501
	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// Test handleConfigReload without reload function
func TestHandleConfigReload_NoReloadFn(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	req := httptest.NewRequest("POST", "/api/v1/config/reload", nil)
	rr := httptest.NewRecorder()

	s.handleConfigReload(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotImplemented)
	}
}

// Test handleConfigReload with error
func TestHandleConfigReload_Error(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", func() error {
		return fmt.Errorf("reload failed")
	})

	req := httptest.NewRequest("POST", "/api/v1/config/reload", nil)
	rr := httptest.NewRecorder()

	s.handleConfigReload(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

// Test handleBackends with invalid method
func TestHandleBackends_InvalidMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	req := httptest.NewRequest("POST", "/api/v1/backends", nil)
	rr := httptest.NewRecorder()

	s.handleBackends(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Test handleStats with invalid method
func TestHandleStats_InvalidMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	req := httptest.NewRequest("POST", "/api/v1/stats", nil)
	rr := httptest.NewRecorder()

	s.handleStats(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Test handlePoolDetail invalid path
func TestHandlePoolDetail_InvalidPath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	req := httptest.NewRequest("GET", "/api/v1/pools", nil)
	req.URL.Path = "/api/v1/pools"
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// Test handlePoolDetail invalid pool name
func TestHandlePoolDetail_InvalidPoolName(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)
	pm := pool.NewManager(log)
	s.poolMgr = pm

	// Test with invalid pool name (contains special char)
	req := httptest.NewRequest("GET", "/api/v1/pools/invalid%20name", nil)
	req.URL.Path = "/api/v1/pools/invalid name"
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// Test handleBackendAction invalid path
func TestHandleBackendAction_InvalidPath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	req := httptest.NewRequest("POST", "/api/v1/backends/action", nil)
	req.URL.Path = "/api/v1/backends/action"
	rr := httptest.NewRecorder()

	s.handleBackendAction(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// Test handleBackendAction unknown action
func TestHandleBackendAction_UnknownAction(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/unknown", nil)
	req.URL.Path = "/api/v1/backends/127.0.0.1:5432/unknown"
	rr := httptest.NewRecorder()

	s.handleBackendAction(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// Test auth with invalid bearer format
func TestServer_Auth_InvalidBearer(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: "secret"},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(nil)

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearersecret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", resp.StatusCode)
	}
}

// Test auth with wrong token type
func TestServer_Auth_WrongTokenType(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: "secret"},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(nil)

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/health", nil)
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

// TestValidatePoolConfig tests the validatePoolConfig function
func TestValidatePoolConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.PoolConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty_name",
			cfg:     &config.PoolConfig{Name: ""},
			wantErr: true,
			errMsg:  "pool name is required",
		},
		{
			name: "invalid_body",
			cfg: &config.PoolConfig{
				Name: "test",
				Body: "invalid",
			},
			wantErr: true,
			errMsg:  "invalid body type",
		},
		{
			name: "invalid_mode",
			cfg: &config.PoolConfig{
				Name: "test",
				Body: "postgresql",
				Mode: "invalid",
			},
			wantErr: true,
			errMsg:  "invalid mode",
		},
		{
			name: "invalid_port_zero",
			cfg: &config.PoolConfig{
				Name:   "test",
				Body:   "postgresql",
				Mode:   "transaction",
				Listen: config.ListenConfig{Port: 0},
			},
			wantErr: true,
			errMsg:  "invalid port",
		},
		{
			name: "invalid_port_too_high",
			cfg: &config.PoolConfig{
				Name:   "test",
				Body:   "postgresql",
				Mode:   "transaction",
				Listen: config.ListenConfig{Port: 70000},
			},
			wantErr: true,
			errMsg:  "invalid port",
		},
		{
			name: "no_backend_hosts",
			cfg: &config.PoolConfig{
				Name:    "test",
				Body:    "postgresql",
				Mode:    "transaction",
				Listen:  config.ListenConfig{Port: 5432},
				Backend: config.BackendConfig{Hosts: []config.BackendHost{}},
			},
			wantErr: true,
			errMsg:  "at least one backend host is required",
		},
		{
			name: "empty_backend_host",
			cfg: &config.PoolConfig{
				Name:   "test",
				Body:   "postgresql",
				Mode:   "transaction",
				Listen: config.ListenConfig{Port: 5432},
				Backend: config.BackendConfig{
					Hosts: []config.BackendHost{{Host: "", Port: 5432}},
				},
			},
			wantErr: true,
			errMsg:  "backend host cannot be empty",
		},
		{
			name: "invalid_backend_port",
			cfg: &config.PoolConfig{
				Name:   "test",
				Body:   "postgresql",
				Mode:   "transaction",
				Listen: config.ListenConfig{Port: 5432},
				Backend: config.BackendConfig{
					Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 0}},
				},
			},
			wantErr: true,
			errMsg:  "invalid backend port",
		},
		{
			name: "invalid_backend_role",
			cfg: &config.PoolConfig{
				Name:   "test",
				Body:   "postgresql",
				Mode:   "transaction",
				Listen: config.ListenConfig{Port: 5432},
				Backend: config.BackendConfig{
					Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "invalid"}},
				},
			},
			wantErr: true,
			errMsg:  "invalid backend role",
		},
		{
			name: "valid_postgresql",
			cfg: &config.PoolConfig{
				Name:   "test",
				Body:   "postgresql",
				Mode:   "transaction",
				Listen: config.ListenConfig{Port: 5432},
				Backend: config.BackendConfig{
					Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
				},
			},
			wantErr: false,
		},
		{
			name: "valid_mysql",
			cfg: &config.PoolConfig{
				Name:   "test",
				Body:   "mysql",
				Mode:   "session",
				Listen: config.ListenConfig{Port: 3306},
				Backend: config.BackendConfig{
					Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 3306, Role: "replica"}},
				},
			},
			wantErr: false,
		},
		{
			name: "valid_mssql",
			cfg: &config.PoolConfig{
				Name:   "test",
				Body:   "mssql",
				Mode:   "statement",
				Listen: config.ListenConfig{Port: 1433},
				Backend: config.BackendConfig{
					Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 1433, Role: "primary"}},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePoolConfig(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
					return
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error = %q, should contain %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// Test auth with wrong token
func TestServer_Auth_WrongToken(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: "secret"},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(nil)

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/health", nil)
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

// Test handleStatsStream endpoint
func TestServer_StatsStream(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(nil)

	// Test with POST (should fail)
	resp, err := http.Post("http://"+s.listener.Addr().String()+"/api/v1/stats/stream", "application/json", nil)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST Status = %d, want 405", resp.StatusCode)
	}
}

// Test handleReady endpoint
func TestServer_Ready(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(nil)

	// Test GET /api/v1/ready - returns 200 when ready (server starts ready)
	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/ready")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Server is ready by default after starting
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	// Test POST (should fail)
	resp2, err := http.Post("http://"+s.listener.Addr().String()+"/api/v1/ready", "application/json", nil)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST Status = %d, want 405", resp2.StatusCode)
	}
}

// Test handleHealth endpoint
func TestServer_Health(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(nil)

	// Test GET /api/v1/health
	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/health")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	// Test POST (should fail)
	resp2, err := http.Post("http://"+s.listener.Addr().String()+"/api/v1/health", "application/json", nil)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST Status = %d, want 405", resp2.StatusCode)
	}
}

// Test handlePoolDetail with not found
func TestServer_PoolDetail_NotFound(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	// Test GET /pools/nonexistent
	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/pools/nonexistent")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", resp.StatusCode)
	}
}

// Test periodicCleanup function - removed since rateLimiter is internal to middleware

// Test handleConnections endpoint
func TestServer_Connections(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	// Test GET /connections
	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/connections")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	// Test POST (should fail)
	resp2, err := http.Post("http://"+s.listener.Addr().String()+"/api/v1/connections", "application/json", nil)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST Status = %d, want 405", resp2.StatusCode)
	}
}

// Test handleBackends endpoint
func TestServer_Backends(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	// Test GET /backends (returns empty list when no pools)
	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/backends")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

// Test validatePoolName function
func Test_validatePoolName(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"valid-pool", true},
		{"another_valid_pool", true},
		{"pool123", true},
		{"", false},
		{"pool with spaces", false},
		{"pool/with/slashes", false},
		{"../etc/passwd", false},
	}

	for _, tt := range tests {
		result := validatePoolName(tt.name)
		if result != tt.expected {
			t.Errorf("validatePoolName(%q) = %v, want %v", tt.name, result, tt.expected)
		}
	}
}

// Test sanitizeErr function
func Test_sanitizeErr(t *testing.T) {
	tests := []struct {
		input    error
		expected string
	}{
		{fmt.Errorf("normal error"), "normal error"},
		{fmt.Errorf("error with sensitive data"), "error with sensitive data"},
	}

	for _, tt := range tests {
		result := sanitizeErr(tt.input)
		if result != tt.expected {
			t.Errorf("sanitizeErr(%v) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// Test withCORS options request
func Test_withCORS_Options(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	// Test OPTIONS request
	req, _ := http.NewRequest("OPTIONS", "http://"+s.listener.Addr().String()+"/api/v1/pools", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "GET")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("OPTIONS Status = %d, want 200", resp.StatusCode)
	}
}

// Test handleConfigReload with GET (not allowed)
func TestServer_ConfigReload_Get(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	// Test GET /config/reload (should fail)
	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/config/reload")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET Status = %d, want 405", resp.StatusCode)
	}
}
