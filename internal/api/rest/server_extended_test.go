package rest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
