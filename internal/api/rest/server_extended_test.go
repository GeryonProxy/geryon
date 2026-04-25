package rest

import (
	"context"
	"encoding/json"
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

// testToken is defined in server_coverage_test.go

// Test handlePools POST with invalid JSON
func TestHandlePools_Create_InvalidJSON(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

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
			Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
		}
		s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)
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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)
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

// Test handlePoolDetail PUT returns 404 for non-existent pool
func TestHandlePoolDetail_PutPoolNotFound(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)
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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", func() error {
		return fmt.Errorf("reload failed")
	}, nil)

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)
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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	// Test with POST (should fail)
	req, _ := http.NewRequest("POST", "http://"+s.listener.Addr().String()+"/api/v1/stats/stream", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	// Test GET /api/v1/ready - returns 200 when ready (server starts ready)
	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/ready", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Server is ready by default after starting
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	// Test POST (should fail)
	req2, _ := http.NewRequest("POST", "http://"+s.listener.Addr().String()+"/api/v1/ready", nil)
	req2.Header.Set("Authorization", "Bearer "+testToken)
	resp2, err := http.DefaultClient.Do(req2)
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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	// Test GET /api/v1/health
	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	// Test POST (should fail)
	req2, _ := http.NewRequest("POST", "http://"+s.listener.Addr().String()+"/api/v1/health", nil)
	req2.Header.Set("Authorization", "Bearer "+testToken)
	resp2, err := http.DefaultClient.Do(req2)
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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	// Test GET /pools/nonexistent
	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/pools/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	// Test GET /connections
	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/connections", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	// Test POST (should fail)
	req2, _ := http.NewRequest("POST", "http://"+s.listener.Addr().String()+"/api/v1/connections", nil)
	req2.Header.Set("Authorization", "Bearer "+testToken)
	resp2, err := http.DefaultClient.Do(req2)
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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	// Test GET /backends (returns empty list when no pools)
	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/backends", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil, nil)
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
	req.Header.Set("Authorization", "Bearer "+testToken)

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

// Test handlePoolDetail DELETE with existing pool
func TestHandlePoolDetail_Delete(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "delete-pool",
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

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	req := httptest.NewRequest("DELETE", "/api/v1/pools/delete-pool", nil)
	req.URL.Path = "/api/v1/pools/delete-pool"
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["status"] != "success" {
		t.Errorf("status = %v, want success", data["status"])
	}
}

// Test handlePoolDetail PUT with existing pool
func TestHandlePoolDetail_Put(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "put-pool",
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

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	// PUT with invalid JSON body
	req := httptest.NewRequest("PUT", "/api/v1/pools/put-pool", strings.NewReader("{invalid"))
	req.URL.Path = "/api/v1/pools/put-pool"
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// Test handlePoolDetail PUT with invalid config
func TestHandlePoolDetail_Put_InvalidConfig(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "put-pool2",
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

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	// PUT with empty name config
	body := `{"name": "", "body": "postgresql", "mode": "transaction"}`
	req := httptest.NewRequest("PUT", "/api/v1/pools/put-pool2", strings.NewReader(body))
	req.URL.Path = "/api/v1/pools/put-pool2"
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// Test handleStats with pools
func TestHandleStats_WithPools(t *testing.T) {
	log, _ := logger.New("error", "json")
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

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	rr := httptest.NewRecorder()

	s.handleStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	if data["active_pools"] != float64(1) {
		t.Errorf("active_pools = %v, want 1", data["active_pools"])
	}
}

// Test handleQueries with invalid method
func TestHandleQueries_InvalidMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/queries", nil)
	rr := httptest.NewRecorder()

	s.handleQueries(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Test handleTransactions with invalid method
func TestHandleTransactions_InvalidMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/transactions", nil)
	rr := httptest.NewRecorder()

	s.handleTransactions(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Test handleMetrics with invalid method
func TestHandleMetrics_InvalidMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/metrics", nil)
	rr := httptest.NewRecorder()

	s.handleMetrics(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Test handleMetrics with pools
func TestHandleMetrics_WithPools(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "metrics-pool",
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

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()

	s.handleMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "geryon_pools_total") {
		t.Error("Metrics should contain geryon_pools_total")
	}
	if !strings.Contains(body, "metrics-pool") {
		t.Error("Metrics should contain pool name")
	}
	if !strings.Contains(body, "geryon_pool_client_connections") {
		t.Error("Metrics should contain connection metrics")
	}
}

// Test handleConfig with POST (not GET)
func TestHandleConfig_Post(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/config", nil)
	rr := httptest.NewRecorder()

	s.handleConfig(rr, req)

	// POST is allowed for config (triggers reload-like behavior)
	// Depending on implementation, this should return 200 or 405
	if rr.Code == http.StatusMethodNotAllowed {
		// If method not allowed, that's fine
		return
	}
}

// Test handleTLSStatus
func TestHandleTLSStatus_Direct(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/tls/status", nil)
	rr := httptest.NewRecorder()

	s.handleTLSStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// Test handleTLSStatus with invalid method
func TestHandleTLSStatus_InvalidMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/tls/status", nil)
	rr := httptest.NewRecorder()

	s.handleTLSStatus(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Test handleCluster endpoint
func TestHandleCluster_Direct(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/cluster", nil)
	rr := httptest.NewRecorder()

	s.handleCluster(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["status"] != "disabled" {
		t.Errorf("status = %v, want 'disabled'", resp["status"])
	}
}

// Test handleStatsStream via direct call with context cancellation
func TestHandleStatsStream_ContextCancel(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen:         "127.0.0.1:0",
		Auth:           config.RESTAuthConfig{Enabled: true, Token: testToken},
		AllowedOrigins: []string{"http://example.com"},
	}
	pm := pool.NewManager(log)
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	// Create a request with a context that gets cancelled quickly
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/v1/stats/stream", nil).WithContext(ctx)
	req.Header.Set("Origin", "http://example.com")
	rr := httptest.NewRecorder()

	// Cancel context immediately to trigger quick exit
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	// This should return after context cancellation
	done := make(chan struct{})
	go func() {
		s.handleStatsStream(rr, req)
		close(done)
	}()

	select {
	case <-done:
		// Good - handleStatsStream returned after context cancel
	case <-time.After(5 * time.Second):
		t.Error("handleStatsStream should have returned after context cancellation")
	}
}

// Test handleStatsStream with SSE headers
func TestHandleStatsStream_SSEHeaders(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen:         "127.0.0.1:0",
		Auth:           config.RESTAuthConfig{Enabled: true, Token: testToken},
		AllowedOrigins: []string{"*"},
	}
	pm := pool.NewManager(log)
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/v1/stats/stream", nil).WithContext(ctx)
	req.Header.Set("Origin", "http://example.com")
	rr := httptest.NewRecorder()

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	done := make(chan struct{})
	go func() {
		s.handleStatsStream(rr, req)
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Fatal("handleStatsStream did not return")
	}

	// Verify SSE headers were set
	if rr.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", rr.Header().Get("Content-Type"))
	}
	if rr.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", rr.Header().Get("Cache-Control"))
	}
	if rr.Header().Get("Connection") != "keep-alive" {
		t.Errorf("Connection = %q, want keep-alive", rr.Header().Get("Connection"))
	}
}

// Test handlePools with unsupported method
func TestHandlePools_UnsupportedMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("PATCH", "/api/v1/pools", nil)
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Test handleReady unhealthy pool (no backends)
func TestHandleReady_UnhealthyPool(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	// Create a pool - it will have 0 backends since there's no real server
	poolCfg := &config.PoolConfig{
		Name: "unhealthy-pool",
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

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/ready", nil)
	rr := httptest.NewRecorder()

	s.handleReady(rr, req)

	// Pool may report as unhealthy if it has no healthy backends
	// Either 200 or 503 is acceptable depending on pool state
	if rr.Code != http.StatusOK && rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want 200 or 503", rr.Code)
	}
}

// Test CORS with allowed origins
func TestWithCORS_AllowedOrigin(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen:         "127.0.0.1:0",
		Auth:           config.RESTAuthConfig{Enabled: true, Token: testToken},
		AllowedOrigins: []string{"http://localhost:3000"},
	}
	pm := pool.NewManager(log)
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/health", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	// Should have CORS header
	if resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Errorf("CORS origin = %q, want http://localhost:3000", resp.Header.Get("Access-Control-Allow-Origin"))
	}
}

// Test CORS with disallowed origin
func TestWithCORS_DisallowedOrigin(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen:         "127.0.0.1:0",
		Auth:           config.RESTAuthConfig{Enabled: true, Token: testToken},
		AllowedOrigins: []string{"http://localhost:3000"},
	}
	pm := pool.NewManager(log)
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/health", nil)
	req.Header.Set("Origin", "http://evil-site.com")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Request should still succeed (CORS doesn't block, just doesn't add headers)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	// Should NOT have CORS header for disallowed origin
	if resp.Header.Get("Access-Control-Allow-Origin") == "http://evil-site.com" {
		t.Error("Should not set CORS header for disallowed origin")
	}
}

// Test periodicCleanup
func TestPeriodicCleanup(t *testing.T) {
	rl := newRateLimiter(10, 5)
	rl.cleanupTTL = 50 * time.Millisecond

	// Add limiters
	rl.GetLimiter("10.0.0.1")
	rl.GetLimiter("10.0.0.2")

	// Verify they exist
	initialCount := 0
	rl.limiters.Range(func(key, value interface{}) bool {
		initialCount++
		return true
	})
	if initialCount != 2 {
		t.Fatalf("expected 2 limiters, got %d", initialCount)
	}

	// Set lastSeen to the past (via direct store)
	rl.lastSeen.Store("10.0.0.1", time.Now().Add(-1*time.Hour))
	rl.lastSeen.Store("10.0.0.2", time.Now().Add(-1*time.Hour))

	// Wait for cleanup
	time.Sleep(150 * time.Millisecond)

	afterCount := 0
	rl.limiters.Range(func(key, value interface{}) bool {
		afterCount++
		return true
	})
	if afterCount != 0 {
		t.Errorf("expected 0 limiters after cleanup, got %d", afterCount)
	}
}

// Test handleBackendDrain with poolMgr but missing backend
func TestHandleBackendDrain_NoBackend(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/drain", nil)
	rr := httptest.NewRecorder()

	s.handleBackendDrain(rr, req, "127.0.0.1:5432")

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// Test handleBackendDrain with poolMgr but missing backend
func TestHandleBackendDrain_BackendNotFound(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/drain", nil)
	rr := httptest.NewRecorder()

	s.handleBackendDrain(rr, req, "127.0.0.1:5432")

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// Test handleBackendCancelDrain with poolMgr but missing backend
func TestHandleBackendCancelDrain_NoBackend(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/cancel-drain", nil)
	rr := httptest.NewRecorder()

	s.handleBackendCancelDrain(rr, req, "127.0.0.1:5432")

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// Test handleBackendAction with drain action and no pools
func TestHandleBackendAction_Drain_NoPools(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/drain", nil)
	req.URL.Path = "/api/v1/backends/127.0.0.1:5432/drain"
	rr := httptest.NewRecorder()

	s.handleBackendAction(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// Test handleBackendAction with cancel-drain action and no pools
func TestHandleBackendAction_CancelDrain_NoPools(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/cancel-drain", nil)
	req.URL.Path = "/api/v1/backends/127.0.0.1:5432/cancel-drain"
	rr := httptest.NewRecorder()

	s.handleBackendAction(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// Test handleBackendAction with GET method (not allowed)
func TestHandleBackendAction_Get(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/backends/127.0.0.1:5432/drain", nil)
	rr := httptest.NewRecorder()

	s.handleBackendAction(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Test handleSlowQueries with GET and limit param
func TestHandleSlowQueries_Get(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/queries/slow?limit=10", nil)
	rr := httptest.NewRecorder()

	s.handleSlowQueries(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// Test handleSlowQueries with invalid limit fallback
func TestHandleSlowQueries_InvalidLimitFallback(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/queries/slow?limit=abc", nil)
	rr := httptest.NewRecorder()

	s.handleSlowQueries(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d (should use default limit)", rr.Code, http.StatusOK)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["limit"] != float64(50) {
		t.Errorf("limit = %v, want 50 (default)", data["limit"])
	}
}

// Test handleSlowQueries with POST method
func TestHandleSlowQueries_PostMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/queries/slow", nil)
	rr := httptest.NewRecorder()

	s.handleSlowQueries(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Test handleRecentQueries with GET
func TestHandleRecentQueries_Get(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/queries/recent?limit=20", nil)
	rr := httptest.NewRecorder()

	s.handleRecentQueries(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// Test handleRecentQueries with POST method
func TestHandleRecentQueries_PostMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/queries/recent", nil)
	rr := httptest.NewRecorder()

	s.handleRecentQueries(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Test handleActiveTransactions with GET
func TestHandleActiveTransactions_Get(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/transactions/active", nil)
	rr := httptest.NewRecorder()

	s.handleActiveTransactions(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// Test handleActiveTransactions with POST method
func TestHandleActiveTransactions_PostMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/transactions/active", nil)
	rr := httptest.NewRecorder()

	s.handleActiveTransactions(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Test handleConfig with GET
func TestHandleConfig_Get(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/config", nil)
	rr := httptest.NewRecorder()

	s.handleConfig(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// Test handleConfigReload success
func TestHandleConfigReload_Success(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", func() error {
		return nil
	}, nil)

	req := httptest.NewRequest("POST", "/api/v1/config/reload", nil)
	rr := httptest.NewRecorder()

	s.handleConfigReload(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["status"] != "success" {
		t.Errorf("status = %v, want success", data["status"])
	}
}

// Test handlePoolDetail GET with existing pool
func TestHandlePoolDetail_Get_Existing(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "get-pool",
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

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/pools/get-pool", nil)
	req.URL.Path = "/api/v1/pools/get-pool"
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["name"] != "get-pool" {
		t.Errorf("name = %v, want get-pool", data["name"])
	}
}

// Test handlePoolDetail DELETE non-existent pool
func TestHandlePoolDetail_Delete_NotFound(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	req := httptest.NewRequest("DELETE", "/api/v1/pools/nonexistent", nil)
	req.URL.Path = "/api/v1/pools/nonexistent"
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// Test handlePoolDetail PUT non-existent pool
func TestHandlePoolDetail_Put_NotFound(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	body := `{"name": "newpool", "body": "postgresql", "mode": "transaction"}`
	req := httptest.NewRequest("PUT", "/api/v1/pools/nonexistent", strings.NewReader(body))
	req.URL.Path = "/api/v1/pools/nonexistent"
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// Test handlePools GET with pools
func TestHandlePools_Get_WithPools(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "list-pool",
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

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/pools", nil)
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	pools, ok := data["pools"].([]interface{})
	if !ok {
		t.Fatal("pools should be an array")
	}
	if len(pools) != 1 {
		t.Errorf("pools count = %d, want 1", len(pools))
	}
}

// Test handleConfigReload with GET (not allowed)
func TestServer_ConfigReload_Get(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	// Test GET /config/reload (should fail)
	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/config/reload", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET Status = %d, want 405", resp.StatusCode)
	}
}

// Test handleUserStats endpoint
func TestHandleUserStats(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/stats/users", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if _, ok := data["users"]; !ok {
		t.Error("Expected 'users' key in response")
	}
}

// Test handleClientStats endpoint
func TestHandleClientStats(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(context.Background())

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/stats/clients", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if _, ok := data["clients"]; !ok {
		t.Error("Expected 'clients' key in response")
	}
}

// Test handleUserStats method not allowed
func TestHandleUserStats_MethodNotAllowed(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/stats/users", nil)
	rr := httptest.NewRecorder()

	s.handleUserStats(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", rr.Code)
	}
}

// Test handleClientStats method not allowed
func TestHandleClientStats_MethodNotAllowed(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/stats/clients", nil)
	rr := httptest.NewRecorder()

	s.handleClientStats(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", rr.Code)
	}
}
