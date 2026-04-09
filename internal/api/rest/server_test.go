package rest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

func TestWriteJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusOK, map[string]string{"key": "value"})

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}
	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var data map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if data["key"] != "value" {
		t.Errorf("body[key] = %q, want value", data["key"])
	}
}

func TestWriteError(t *testing.T) {
	rr := httptest.NewRecorder()
	writeError(rr, http.StatusBadRequest, "bad request")

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}

	var data map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if data["error"] != "bad request" {
		t.Errorf("error = %q, want bad request", data["error"])
	}
}

func TestSanitizeErr(t *testing.T) {
	// Short error
	msg := sanitizeErr(fmt.Errorf("short error"))
	if msg != "short error" {
		t.Errorf("msg = %q, want short error", msg)
	}

	// Long error (>200 chars)
	long := ""
	for i := 0; i < 300; i++ {
		long += "x"
	}
	msg = sanitizeErr(fmt.Errorf("%s", long))
	if len(msg) != 200 {
		t.Errorf("Truncated length = %d, want 200", len(msg))
	}
}

func TestValidatePoolName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"my_pool", true},
		{"pool-1", true},
		{"a", true},
		{"", false},
		{"pool with spaces", false},
		{"pool/invalid", false},
		{"pool;drop", false},
	}
	for _, tc := range cases {
		if got := validatePoolName(tc.name); got != tc.want {
			t.Errorf("validatePoolName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNewServer(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, err := NewServer(cfg, nil, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	if s == nil {
		t.Fatal("Server is nil")
	}
}

func TestServer_StartStop(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, err := NewServer(cfg, nil, nil, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	err = s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(nil)

	// Address should be set (listening on :0)
	addr := s.listener.Addr().String()
	if addr == "" {
		t.Error("Listener address should not be empty")
	}
}

func TestServer_HealthEndpoint(t *testing.T) {
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

	// Make HTTP request
	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/health")
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

func TestServer_ReadyEndpoint(t *testing.T) {
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

	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/ready")
	if err != nil {
		t.Fatalf("GET /ready failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["ready"] != true {
		t.Errorf("ready = %v, want true", data["ready"])
	}
}

func TestServer_PoolsEndpoint(t *testing.T) {
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

	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/pools")
	if err != nil {
		t.Fatalf("GET /pools failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	pools, ok := data["pools"].([]interface{})
	if !ok {
		t.Fatal("pools key should be an array")
	}
	if len(pools) != 0 {
		t.Errorf("Pools count = %d, want 0 (no pools created)", len(pools))
	}
}

func TestServer_StatsEndpoint(t *testing.T) {
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

	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/stats")
	if err != nil {
		t.Fatalf("GET /stats failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_BackendsEndpoint(t *testing.T) {
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

	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/backends")
	if err != nil {
		t.Fatalf("GET /backends failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_TransactionsEndpoint(t *testing.T) {
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

	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/transactions")
	if err != nil {
		t.Fatalf("GET /transactions failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_MetricsEndpoint(t *testing.T) {
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

	resp, err := http.Get("http://" + s.listener.Addr().String() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_ConfigReloadEndpoint(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	reloaded := false
	s, err := NewServer(cfg, pm, nil, log, "", func() error {
		reloaded = true
		return nil
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(nil)

	resp, err := http.Post("http://"+s.listener.Addr().String()+"/api/v1/config/reload", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /config/reload failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	if !reloaded {
		t.Error("Reload function should have been called")
	}
}

func TestServer_ConfigEndpoint(t *testing.T) {
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

	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/config")
	if err != nil {
		t.Fatalf("GET /config failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_TLSStatusEndpoint(t *testing.T) {
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

	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/tls/status")
	if err != nil {
		t.Fatalf("GET /tls/status failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_ConnectionsEndpoint(t *testing.T) {
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

	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/connections")
	if err != nil {
		t.Fatalf("GET /connections failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_QueriesEndpoint(t *testing.T) {
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

	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/queries")
	if err != nil {
		t.Fatalf("GET /queries failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_AuthEnabled_RejectsWithoutToken(t *testing.T) {
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

	// Without auth token
	resp, err := http.Get("http://" + s.listener.Addr().String() + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", resp.StatusCode)
	}

	// With correct auth token
	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Authenticated GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}
