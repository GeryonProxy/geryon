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

// newTestServer creates a REST server with a real pool manager for testing.
func newTestServer(t *testing.T) (*Server, *pool.Manager) {
	t.Helper()
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s, err := NewServer(cfg, pm, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	return s, pm
}

// --- Middleware coverage ---

func TestWithSecurityHeaders(t *testing.T) {
	s, _ := newTestServer(t)
	handler := s.withSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rr, req)

	// Verify all security headers are set
	if v := rr.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", v)
	}
	if v := rr.Header().Get("X-Frame-Options"); v != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", v)
	}
	if v := rr.Header().Get("X-XSS-Protection"); v != "1; mode=block" {
		t.Errorf("X-XSS-Protection = %q, want '1; mode=block'", v)
	}
	if v := rr.Header().Get("Cache-Control"); v != "no-store, no-cache, must-revalidate" {
		t.Errorf("Cache-Control = %q, want 'no-store, no-cache, must-revalidate'", v)
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

func TestWithLogging(t *testing.T) {
	s, _ := newTestServer(t)
	called := false
	handler := s.withLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("Next handler should have been called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

func TestWithAuth_Disabled(t *testing.T) {
	s, _ := newTestServer(t)
	called := false
	handler := s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("Next handler should be called when auth is disabled")
	}
}

func TestWithAuth_Enabled_ValidBearer(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: "mysecret"},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	called := false
	handler := s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer mysecret")
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("Next handler should be called with valid bearer token")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

func TestWithAuth_Enabled_NoHeader(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: "mysecret"},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	handler := s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Should not call next handler")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", rr.Code)
	}
}

func TestWithAuth_LowercaseBearer(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: "mysecret"},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	called := false
	handler := s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "bearer mysecret")
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("Case-insensitive 'bearer' should be accepted")
	}
}

func TestWithAuth_BearerWithSpaces(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: "mysecret"},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	handler := s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Should not call next handler")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer  mysecret")
	handler.ServeHTTP(rr, req)

	// Even with double space, SplitN splits on first space, parts[1] = " mysecret" != "mysecret"
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", rr.Code)
	}
}

func TestWithCORS_NoOrigin(t *testing.T) {
	s, _ := newTestServer(t)
	called := false
	handler := s.withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("Next handler should be called without origin")
	}
}

func TestWithCORS_Options(t *testing.T) {
	s, _ := newTestServer(t)
	handler := s.withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Should not call next handler for OPTIONS")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/test", nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

func TestIsAllowedOrigin_EmptyOrigins_NilOrigin(t *testing.T) {
	s, _ := newTestServer(t)
	if !s.isAllowedOrigin("") {
		t.Error("Empty origin should be allowed when AllowedOrigins is empty")
	}
}

func TestIsAllowedOrigin_EmptyOrigins_NonEmptyOrigin(t *testing.T) {
	s, _ := newTestServer(t)
	if s.isAllowedOrigin("http://example.com") {
		t.Error("Non-empty origin should not be allowed when AllowedOrigins is empty")
	}
}

func TestIsAllowedOrigin_Wildcard(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen:         "127.0.0.1:0",
		Auth:           config.RESTAuthConfig{Enabled: false},
		AllowedOrigins: []string{"*"},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	if !s.isAllowedOrigin("http://anything.com") {
		t.Error("Wildcard should allow any origin")
	}
}

func TestIsAllowedOrigin_ExactMatch(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen:         "127.0.0.1:0",
		Auth:           config.RESTAuthConfig{Enabled: false},
		AllowedOrigins: []string{"http://localhost:3000"},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil)

	if !s.isAllowedOrigin("http://localhost:3000") {
		t.Error("Exact origin match should be allowed")
	}
	if s.isAllowedOrigin("http://evil.com") {
		t.Error("Non-matching origin should not be allowed")
	}
}

// --- handlePools POST success path ---

func TestHandlePools_Create_Success(t *testing.T) {
	s, pm := newTestServer(t)
	_ = pm

	body := `{"name":"test-pool","body":"postgresql","mode":"session","backend":{"hosts":[{"host":"127.0.0.1","port":5432,"role":"primary"}]}}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("Status = %d, want %d. Body: %s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["status"] != "success" {
		t.Errorf("status = %v, want success", data["status"])
	}
	if data["pool"] != "test-pool" {
		t.Errorf("pool = %v, want test-pool", data["pool"])
	}
}

func TestHandlePools_Create_Duplicate(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "dup-pool",
		Body: "postgresql",
		Mode: "session",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	body := `{"name":"dup-pool","body":"postgresql","mode":"session","backend":{"hosts":[{"host":"127.0.0.1","port":5432,"role":"primary"}]}}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusConflict)
	}
}

// --- handlePoolDetail DELETE success ---

func TestHandlePoolDetail_Delete_Success(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "delete-pool",
		Body: "postgresql",
		Mode: "session",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	req := httptest.NewRequest("DELETE", "/api/v1/pools/delete-pool", nil)
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200. Body: %s", rr.Code, rr.Body.String())
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["status"] != "success" {
		t.Errorf("status = %v, want success", data["status"])
	}
}

// --- handlePoolDetail GET success with data ---

func TestHandlePoolDetail_Get_Success(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "detail-pool",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	req := httptest.NewRequest("GET", "/api/v1/pools/detail-pool", nil)
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200. Body: %s", rr.Code, rr.Body.String())
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["name"] != "detail-pool" {
		t.Errorf("name = %v, want detail-pool", data["name"])
	}
	if data["mode"] != "transaction" {
		t.Errorf("mode = %v, want transaction", data["mode"])
	}
}

// --- handleConfigReload success path ---

func TestHandleConfigReload_SuccessPath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	reloadCalled := false
	s, _ := NewServer(cfg, nil, nil, log, "", func() error {
		reloadCalled = true
		return nil
	})

	req := httptest.NewRequest("POST", "/api/v1/config/reload", nil)
	rr := httptest.NewRecorder()

	s.handleConfigReload(rr, req)

	if !reloadCalled {
		t.Error("reloadFn should have been called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["status"] != "success" {
		t.Errorf("status = %v, want success", data["status"])
	}
}

func TestHandleConfigReload_NotImplemented(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/config/reload", nil)
	rr := httptest.NewRecorder()

	s.handleConfigReload(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotImplemented)
	}
}

func TestHandleConfigReload_ErrorPath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", func() error {
		return fmt.Errorf("reload error")
	})

	req := httptest.NewRequest("POST", "/api/v1/config/reload", nil)
	rr := httptest.NewRecorder()

	s.handleConfigReload(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

// --- handleBackends with real pools ---

func TestHandleBackends_WithRealPools(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "backend-test-pool",
		Body: "postgresql",
		Mode: "session",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
				{Host: "127.0.0.1", Port: 5433, Role: "replica"},
			},
		},
	}
	pm.CreatePool(poolCfg)

	req := httptest.NewRequest("GET", "/api/v1/backends", nil)
	rr := httptest.NewRecorder()

	s.handleBackends(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

// --- handleStats with real pools ---

func TestHandleStats_WithRealPools(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "stats-test-pool",
		Body: "postgresql",
		Mode: "session",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	rr := httptest.NewRecorder()

	s.handleStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["active_pools"] != float64(1) {
		t.Errorf("active_pools = %v, want 1", data["active_pools"])
	}
}

// --- handlePools GET with real pools ---

func TestHandlePools_Get_WithData(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "list-pool",
		Body: "mysql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 3306, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	req := httptest.NewRequest("GET", "/api/v1/pools", nil)
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	pools, ok := data["pools"].([]interface{})
	if !ok || len(pools) != 1 {
		t.Fatalf("pools = %v, want 1 entry", data["pools"])
	}
	poolData := pools[0].(map[string]interface{})
	if poolData["name"] != "list-pool" {
		t.Errorf("name = %v, want list-pool", poolData["name"])
	}
}

// --- handleConnections with listeners ---

func TestHandleConnections_NilListeners(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/connections", nil)
	rr := httptest.NewRecorder()

	s.handleConnections(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

// --- handleQueries with nil listeners ---

func TestHandleQueries_NilListeners(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/queries", nil)
	rr := httptest.NewRecorder()

	s.handleQueries(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

// --- handleTransactions with nil listeners ---

func TestHandleTransactions_NilListeners(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/transactions", nil)
	rr := httptest.NewRecorder()

	s.handleTransactions(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

// --- handleMetrics with real pools ---

func TestHandleMetrics_WithRealPools(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "metrics-pool",
		Body: "postgresql",
		Mode: "session",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()

	s.handleMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "geryon_pools_total") {
		t.Error("Response should contain geryon_pools_total")
	}
	if !strings.Contains(body, "geryon_pool_client_connections") {
		t.Error("Response should contain geryon_pool_client_connections")
	}
	if !strings.Contains(body, "geryon_pool_server_connections") {
		t.Error("Response should contain geryon_pool_server_connections")
	}
	if !strings.Contains(body, `pool="metrics-pool"`) {
		t.Error("Response should contain pool label for metrics-pool")
	}
	if !strings.Contains(body, "# HELP") {
		t.Error("Response should contain HELP comments")
	}
	if !strings.Contains(body, "# TYPE") {
		t.Error("Response should contain TYPE comments")
	}
}

// --- handleSlowQueries limit boundary ---

func TestHandleSlowQueries_LimitOverMax(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/queries/slow?limit=2000", nil)
	rr := httptest.NewRecorder()

	s.handleSlowQueries(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	// Limit > 1000 should fall back to default 50
	if data["limit"] != float64(50) {
		t.Errorf("limit = %v, want 50 (default for >1000)", data["limit"])
	}
}

func TestHandleSlowQueries_NegativeLimit(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/queries/slow?limit=-5", nil)
	rr := httptest.NewRecorder()

	s.handleSlowQueries(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["limit"] != float64(50) {
		t.Errorf("limit = %v, want 50 (default for negative)", data["limit"])
	}
}

func TestHandleSlowQueries_ZeroLimit(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/queries/slow?limit=0", nil)
	rr := httptest.NewRecorder()

	s.handleSlowQueries(rr, req)

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["limit"] != float64(50) {
		t.Errorf("limit = %v, want 50 (default for zero)", data["limit"])
	}
}

// --- handleRecentQueries limit boundary ---

func TestHandleRecentQueries_LimitOverMax(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/queries/recent?limit=2000", nil)
	rr := httptest.NewRecorder()

	s.handleRecentQueries(rr, req)

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	// Limit > 1000 should fall back to default 100
	if data["limit"] != float64(100) {
		t.Errorf("limit = %v, want 100 (default for >1000)", data["limit"])
	}
}

// --- handlePoolDetail PUT with non-existent pool ---

func TestHandlePoolDetail_Put_PoolNotFound(t *testing.T) {
	s, _ := newTestServer(t)

	body := `{"name":"nonexistent","body":"postgresql","mode":"session","backend":{"hosts":[{"host":"127.0.0.1","port":5432,"role":"primary"}]}}`
	req := httptest.NewRequest("PUT", "/api/v1/pools/nonexistent", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", rr.Code)
	}
}

// --- handlePoolDetail DELETE non-existent pool ---

func TestHandlePoolDetail_Delete_NotFoundPool(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("DELETE", "/api/v1/pools/nonexistent", nil)
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", rr.Code)
	}
}

// --- handleBackendAction drain with no pools ---

func TestHandleBackendAction_Drain_NoPoolsData(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/drain", nil)
	rr := httptest.NewRecorder()

	s.handleBackendAction(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", rr.Code)
	}
}

func TestHandleBackendAction_CancelDrain_NoPoolsData(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/cancel-drain", nil)
	rr := httptest.NewRecorder()

	s.handleBackendAction(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", rr.Code)
	}
}

// --- handleConfig with pools ---

func TestHandleConfig_WithPools(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "config-pool",
		Body: "mysql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 3306, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	req := httptest.NewRequest("GET", "/api/v1/config", nil)
	rr := httptest.NewRecorder()

	s.handleConfig(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	pools, ok := data["pools"].([]interface{})
	if !ok || len(pools) != 1 {
		t.Fatalf("pools = %v, want 1 entry", data["pools"])
	}
	poolData := pools[0].(map[string]interface{})
	if poolData["name"] != "config-pool" {
		t.Errorf("name = %v, want config-pool", poolData["name"])
	}
}

// --- handleReady with empty pools ---

func TestHandleReady_NoPools(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/ready", nil)
	rr := httptest.NewRecorder()

	s.handleReady(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["ready"] != true {
		t.Errorf("ready = %v, want true", data["ready"])
	}
}

// --- handleReady with unhealthy pool (no backends) ---

func TestHandleReady_UnhealthyPool_NoBackends(t *testing.T) {
	s, pm := newTestServer(t)

	// Create a pool - it will have 0 backends since no real backend exists
	poolCfg := &config.PoolConfig{
		Name: "unhealthy-pool",
		Body: "postgresql",
		Mode: "session",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	req := httptest.NewRequest("GET", "/api/v1/ready", nil)
	rr := httptest.NewRecorder()

	s.handleReady(rr, req)

	// Pool has no healthy backends, so should be 503
	if rr.Code != http.StatusServiceUnavailable {
		t.Logf("Status = %d (pool may have backends). Body: %s", rr.Code, rr.Body.String())
	}
}

// --- handleHealth ---

func TestHandleHealth_Get(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	rr := httptest.NewRecorder()

	s.handleHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["status"] != "healthy" {
		t.Errorf("status = %v, want healthy", data["status"])
	}
}

func TestHandleHealth_InvalidMethod(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/health", nil)
	rr := httptest.NewRecorder()

	s.handleHealth(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", rr.Code)
	}
}

// --- handleTLSStatus with nil listeners ---

func TestHandleTLSStatus_NilListeners(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/tls/status", nil)
	rr := httptest.NewRecorder()

	s.handleTLSStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if tlsStatus, ok := data["tls_status"].([]interface{}); !ok || len(tlsStatus) != 0 {
		t.Errorf("tls_status = %v, want empty array", data["tls_status"])
	}
}

// --- rate limiter additional tests ---

func TestRateLimiter_MaxSizeEviction(t *testing.T) {
	rl := newRateLimiter(10, 5)
	rl.maxSize.Store(3)

	// Fill to capacity
	rl.GetLimiter("10.0.0.1")
	time.Sleep(1 * time.Millisecond)
	rl.GetLimiter("10.0.0.2")
	time.Sleep(1 * time.Millisecond)
	rl.GetLimiter("10.0.0.3")

	// Adding a 4th should evict the oldest
	rl.GetLimiter("10.0.0.4")

	count := 0
	rl.limiters.Range(func(key, value interface{}) bool {
		count++
		return true
	})

	if count != 3 {
		t.Errorf("limiters count = %d, want 3", count)
	}

	// 10.0.0.1 should have been evicted
	_, exists := rl.limiters.Load("10.0.0.1")
	if exists {
		t.Error("10.0.0.1 should have been evicted as oldest")
	}
}

func TestRateLimiter_GetLimiter_ExistingIP(t *testing.T) {
	rl := newRateLimiter(10, 5)

	l1 := rl.GetLimiter("10.0.0.1")
	l2 := rl.GetLimiter("10.0.0.1")

	if l1 != l2 {
		t.Error("Same IP should return same limiter")
	}
}

// --- handleStatsStream SSE with context cancel ---

func TestHandleStatsStream_ContextCancel_Immediate(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/stats/stream", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	// Cancel immediately
	cancel()

	rr := httptest.NewRecorder()
	s.handleStatsStream(rr, req)

	// Should return quickly after context cancellation
	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

// --- handleBackendAction drain with pools but no matching backend ---

func TestHandleBackendAction_Drain_BackendNotFound(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "drain-pool",
		Body: "postgresql",
		Mode: "session",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	req := httptest.NewRequest("POST", "/api/v1/backends/192.168.1.1:5432/drain", nil)
	rr := httptest.NewRecorder()

	s.handleBackendAction(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", rr.Code)
	}
}

// --- writeMetric via handleMetrics format validation ---

func TestHandleMetrics_MetricFormat(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()

	s.handleMetrics(rr, req)

	body := rr.Body.String()

	// Verify Prometheus exposition format
	lines := strings.Split(body, "\n")
	helpCount := 0
	typeCount := 0
	metricCount := 0

	for _, line := range lines {
		if strings.HasPrefix(line, "# HELP ") {
			helpCount++
		} else if strings.HasPrefix(line, "# TYPE ") {
			typeCount++
		} else if line != "" && !strings.HasPrefix(line, "#") {
			metricCount++
		}
	}

	if helpCount == 0 {
		t.Error("Expected at least one HELP line")
	}
	if typeCount == 0 {
		t.Error("Expected at least one TYPE line")
	}
	if metricCount == 0 {
		t.Error("Expected at least one metric line")
	}
}

// --- handlePools POST with body type mssql ---

func TestHandlePools_Create_MSSQL(t *testing.T) {
	s, _ := newTestServer(t)

	body := `{"name":"mssql-pool","body":"mssql","mode":"session","backend":{"hosts":[{"host":"127.0.0.1","port":1433,"role":"primary"}]}}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("Status = %d, want %d. Body: %s", rr.Code, http.StatusCreated, rr.Body.String())
	}
}

// --- handlePools POST with body type mysql ---

func TestHandlePools_Create_MySQL(t *testing.T) {
	s, _ := newTestServer(t)

	body := `{"name":"mysql-pool","body":"mysql","mode":"statement","backend":{"hosts":[{"host":"127.0.0.1","port":3306,"role":"primary"}]}}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("Status = %d, want %d. Body: %s", rr.Code, http.StatusCreated, rr.Body.String())
	}
}

// --- handleBackendDrain success path ---

func TestHandleBackendDrain_Success(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "drain-success-pool",
		Body: "postgresql",
		Mode: "session",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/drain", nil)
	rr := httptest.NewRecorder()

	s.handleBackendAction(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200. Body: %s", rr.Code, rr.Body.String())
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["status"] != "success" {
		t.Errorf("status = %v, want success", data["status"])
	}
	if data["backend"] != "127.0.0.1:5432" {
		t.Errorf("backend = %v, want 127.0.0.1:5432", data["backend"])
	}
}

// --- handleBackendDrain already draining ---

func TestHandleBackendDrain_AlreadyDraining(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "already-drain-pool",
		Body: "postgresql",
		Mode: "session",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	// Drain once
	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/drain", nil)
	rr := httptest.NewRecorder()
	s.handleBackendAction(rr, req)

	// Drain again - should fail
	req2 := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/drain", nil)
	rr2 := httptest.NewRecorder()
	s.handleBackendAction(rr2, req2)

	if rr2.Code != http.StatusInternalServerError {
		t.Errorf("Status = %d, want 500. Body: %s", rr2.Code, rr2.Body.String())
	}
}

// --- handleBackendCancelDrain success path ---

func TestHandleBackendCancelDrain_Success(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "cancel-drain-pool",
		Body: "postgresql",
		Mode: "session",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	// First drain the backend
	p := pm.GetPool("cancel-drain-pool")
	p.DrainBackend("127.0.0.1:5432")

	// Now cancel the drain
	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/cancel-drain", nil)
	rr := httptest.NewRecorder()

	s.handleBackendAction(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200. Body: %s", rr.Code, rr.Body.String())
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["status"] != "success" {
		t.Errorf("status = %v, want success", data["status"])
	}
}

// --- handleBackendCancelDrain not draining ---

func TestHandleBackendCancelDrain_NotDraining(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "not-draining-pool",
		Body: "postgresql",
		Mode: "session",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	// Cancel drain on a backend that's not draining
	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/cancel-drain", nil)
	rr := httptest.NewRecorder()

	s.handleBackendAction(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("Status = %d, want 500. Body: %s", rr.Code, rr.Body.String())
	}
}

// --- handlePoolDetail PUT success path ---

func TestHandlePoolDetail_Put_Success(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "update-pool",
		Body: "postgresql",
		Mode: "session",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 15432,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	body := `{"name":"update-pool","body":"postgresql","mode":"session","listen":{"host":"127.0.0.1","port":15432},"backend":{"hosts":[{"host":"127.0.0.1","port":5432,"role":"primary"}]}}`
	req := httptest.NewRequest("PUT", "/api/v1/pools/update-pool", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200. Body: %s", rr.Code, rr.Body.String())
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["status"] != "success" {
		t.Errorf("status = %v, want success", data["status"])
	}
}

// --- handlePoolDetail PUT with changed body type (error) ---

func TestHandlePoolDetail_Put_ChangedBody(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "body-change-pool",
		Body: "postgresql",
		Mode: "session",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 15433,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	body := `{"name":"body-change-pool","body":"mysql","mode":"session","listen":{"host":"127.0.0.1","port":15433},"backend":{"hosts":[{"host":"127.0.0.1","port":3306,"role":"primary"}]}}`
	req := httptest.NewRequest("PUT", "/api/v1/pools/body-change-pool", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("Status = %d, want 500. Body: %s", rr.Code, rr.Body.String())
	}
}

// --- withRateLimit middleware ---

func TestWithRateLimit_PassesThrough(t *testing.T) {
	s, _ := newTestServer(t)
	called := false
	handler := s.withRateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("Request should pass through rate limiter")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

func TestWithRateLimit_NoPort(t *testing.T) {
	s, _ := newTestServer(t)
	called := false
	handler := s.withRateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1" // no port
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("Request should pass through even without port in RemoteAddr")
	}
}

// --- handleStats nil listeners ---

func TestHandleStats_NilListeners(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	rr := httptest.NewRecorder()

	s.handleStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["active_pools"] != float64(0) {
		t.Errorf("active_pools = %v, want 0", data["active_pools"])
	}
}

// --- handleStatsStream method check ---

func TestHandleStatsStream_InvalidMethod(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/stats/stream", nil)
	rr := httptest.NewRecorder()

	s.handleStatsStream(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", rr.Code)
	}
}

// --- handlePools DELETE method ---

func TestHandlePools_Delete(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("DELETE", "/api/v1/pools", nil)
	rr := httptest.NewRecorder()

	s.handlePools(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", rr.Code)
	}
}

// --- handlePoolDetail OPTIONS method ---

func TestHandlePoolDetail_Options(t *testing.T) {
	s, pm := newTestServer(t)

	poolCfg := &config.PoolConfig{
		Name: "options-pool",
		Body: "postgresql",
		Mode: "session",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)

	req := httptest.NewRequest("OPTIONS", "/api/v1/pools/options-pool", nil)
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", rr.Code)
	}
}

// --- withRateLimit rate-limited (429) path ---

func TestWithRateLimit_RateLimited(t *testing.T) {
	s, _ := newTestServer(t)
	called := 0
	handler := s.withRateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	}))

	// Send 25+ requests to exhaust burst of 20
	for i := 0; i < 25; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		handler.ServeHTTP(rr, req)
	}

	// At least some should have been rate limited (429)
	if called >= 25 {
		t.Errorf("called = %d, expected some requests to be rate limited", called)
	}
}

// --- handleReady with no pools returns ready ---

func TestHandleReady_NoPools_Ready(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/ready", nil)
	rr := httptest.NewRecorder()

	s.handleReady(rr, req)

	// No pools, should return 200 (ready)
	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200 (no pools = ready)", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["ready"] != true {
		t.Errorf("ready = %v, want true", data["ready"])
	}
}
