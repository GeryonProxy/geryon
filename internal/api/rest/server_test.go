package rest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

// testToken is defined in server_coverage_test.go

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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, err := NewServer(cfg, nil, nil, log, "", nil, nil)
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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, err := NewServer(cfg, nil, nil, log, "", nil, nil)
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
	defer s.Stop(nil)

	// Make HTTP request
	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	resp, err := http.DefaultClient.Do(req)
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
	defer s.Stop(nil)

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/ready", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
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
	defer s.Stop(nil)

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/pools", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
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
	defer s.Stop(nil)

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/stats", nil)
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

func TestServer_BackendsEndpoint(t *testing.T) {
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
	defer s.Stop(nil)

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/backends", nil)
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

func TestServer_TransactionsEndpoint(t *testing.T) {
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
	defer s.Stop(nil)

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/transactions", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
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
	defer s.Stop(nil)

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
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
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	pm := pool.NewManager(log)
	reloaded := false
	s, err := NewServer(cfg, pm, nil, log, "", func() error {
		reloaded = true
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop(nil)

	req, _ := http.NewRequest("POST", "http://"+s.listener.Addr().String()+"/api/v1/config/reload", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	resp, err := http.DefaultClient.Do(req)
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
	defer s.Stop(nil)

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/config", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
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
	defer s.Stop(nil)

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/tls/status", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
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
	defer s.Stop(nil)

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/connections", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
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
	defer s.Stop(nil)

	req, _ := http.NewRequest("GET", "http://"+s.listener.Addr().String()+"/api/v1/queries", nil)
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

func TestServer_AuthEnabled_RejectsWithoutToken(t *testing.T) {
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
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Authenticated GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_isAllowedOrigin(t *testing.T) {
	log, _ := logger.New("debug", "json")

	tests := []struct {
		name     string
		origins  []string
		origin   string
		expected bool
	}{
		{"empty allowed, empty origin", []string{}, "", true},
		{"empty allowed, some origin", []string{}, "http://example.com", false},
		{"wildcard", []string{"*"}, "http://example.com", true},
		{"exact match", []string{"http://example.com"}, "http://example.com", true},
		{"no match", []string{"http://example.com"}, "http://other.com", false},
		{"multiple origins match", []string{"http://a.com", "http://b.com"}, "http://b.com", true},
		{"multiple origins no match", []string{"http://a.com", "http://b.com"}, "http://c.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.AdminRESTConfig{
				Listen:         "127.0.0.1:0",
				Auth:           config.RESTAuthConfig{Enabled: false},
				AllowedOrigins: tt.origins,
			}
			s, err := NewServer(cfg, nil, nil, log, "", nil, nil)
			if err != nil {
				t.Fatalf("NewServer failed: %v", err)
			}

			got := s.isAllowedOrigin(tt.origin)
			if got != tt.expected {
				t.Errorf("isAllowedOrigin(%q) = %v, want %v", tt.origin, got, tt.expected)
			}
		})
	}
}

func TestServer_Start_AlreadyStarted(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, err := NewServer(cfg, nil, nil, log, "", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// First start
	if err := s.Start(); err != nil {
		t.Fatalf("First Start failed: %v", err)
	}
	defer s.Stop(nil)

	// Second start should fail
	if err := s.Start(); err == nil {
		t.Error("Second Start should have failed")
	}
}

func TestServer_Stop_NotStarted(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, err := NewServer(cfg, nil, nil, log, "", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Stop when not started should not error
	if err := s.Stop(nil); err != nil {
		t.Errorf("Stop when not started failed: %v", err)
	}
}

func TestValidatePoolName_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"valid_pool_123", true},
		{"Pool-Name-With-Dashes", true},
		{"a", true},  // minimum length
		{"ab", true}, // 2 chars
		{"_underscore_start", true},
		{"", false}, // empty
		{"pool with spaces", false},
		{"pool/invalid", false},
		{"pool;drop", false},
		{"pool<invalid>", false},
		{"pool&invalid", false},
		{"pool|invalid", false},
		{"pool'invalid", false},
		{`pool"invalid`, false},
		{"pool\x00invalid", false}, // null character
	}

	for _, tc := range tests {
		got := validatePoolName(tc.name)
		if got != tc.want {
			t.Errorf("validatePoolName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWriteJSON_EdgeCases(t *testing.T) {
	t.Run("nil data", func(t *testing.T) {
		rr := httptest.NewRecorder()
		writeJSON(rr, http.StatusOK, nil)

		if rr.Code != http.StatusOK {
			t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
		}
	})

	t.Run("empty map", func(t *testing.T) {
		rr := httptest.NewRecorder()
		writeJSON(rr, http.StatusOK, map[string]string{})

		if rr.Code != http.StatusOK {
			t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
		}
	})
}

func TestWriteError_EdgeCases(t *testing.T) {
	t.Run("empty message", func(t *testing.T) {
		rr := httptest.NewRecorder()
		writeError(rr, http.StatusBadRequest, "")

		if rr.Code != http.StatusBadRequest {
			t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
		}

		var data map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
			t.Fatalf("JSON decode failed: %v", err)
		}
		if data["error"] != "" {
			t.Errorf("error = %q, want empty", data["error"])
		}
	})

	t.Run("special characters", func(t *testing.T) {
		rr := httptest.NewRecorder()
		writeError(rr, http.StatusInternalServerError, "error with special chars: <>&'")

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("Status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestSanitizeErr_EdgeCases(t *testing.T) {
	t.Run("exactly 200 chars", func(t *testing.T) {
		msg := strings.Repeat("x", 200)
		got := sanitizeErr(fmt.Errorf("%s", msg))
		if len(got) != 200 {
			t.Errorf("Length = %d, want 200", len(got))
		}
	})

	t.Run("empty error", func(t *testing.T) {
		got := sanitizeErr(fmt.Errorf(""))
		if got != "" {
			t.Errorf("msg = %q, want empty", got)
		}
	})

	t.Run("error with newlines", func(t *testing.T) {
		got := sanitizeErr(fmt.Errorf("line1\nline2\nline3"))
		if got != "line1\nline2\nline3" {
			t.Errorf("msg = %q, want unchanged", got)
		}
	})
}

// Test rateLimiter GetLimiter with eviction
func TestRateLimiter_GetLimiter_Eviction(t *testing.T) {
	rl := newRateLimiter(2, 5)
	rl.maxSize.Store(2) // Set max size to 2 for testing eviction

	// Get limiters for 3 different IPs (should trigger eviction)
	l1 := rl.GetLimiter("10.0.0.1")
	l2 := rl.GetLimiter("10.0.0.2")
	l3 := rl.GetLimiter("10.0.0.3")

	// All should be non-nil
	if l1 == nil {
		t.Error("l1 should not be nil")
	}
	if l2 == nil {
		t.Error("l2 should not be nil")
	}
	if l3 == nil {
		t.Error("l3 should not be nil")
	}

	// Should have 2 limiters (one evicted)
	count := 0
	rl.limiters.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	if count != 2 {
		t.Errorf("len(limiters) = %d, want 2", count)
	}
}

// Test rateLimiter GetLimiter returns same limiter for same IP
func TestRateLimiter_GetLimiter_SameIP(t *testing.T) {
	rl := newRateLimiter(10, 5)

	l1 := rl.GetLimiter("10.0.0.1")
	l2 := rl.GetLimiter("10.0.0.1")

	if l1 != l2 {
		t.Error("Same IP should return same limiter")
	}
}

// Test handlePoolDetail
func TestHandlePoolDetail_NotFound(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	// Create a pool manager with no pools
	pm := pool.NewManager(log)
	s.poolMgr = pm

	// Test with non-existent pool - need to set up request path
	req := httptest.NewRequest("GET", "/api/v1/pools/nonexistent", nil)
	req.URL.Path = "/api/v1/pools/nonexistent"
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// Test handleSlowQueries
func TestHandleSlowQueries(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/queries/slow", nil)
	rr := httptest.NewRecorder()

	s.handleSlowQueries(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	if _, ok := data["slow_queries"]; !ok {
		t.Error("response should contain slow_queries")
	}
}

// Test handleRecentQueries
func TestHandleRecentQueries(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/queries/recent", nil)
	rr := httptest.NewRecorder()

	s.handleRecentQueries(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	if _, ok := data["recent_queries"]; !ok {
		t.Error("response should contain recent_queries")
	}
}

// Test handleActiveTransactions
func TestHandleActiveTransactions(t *testing.T) {
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

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	if _, ok := data["active_transactions"]; !ok {
		t.Error("response should contain active_transactions")
	}
}

// Test handleBackendDrain
func TestHandleBackendDrain_NotFound(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	// Create a pool manager with no pools
	pm := pool.NewManager(log)
	s.poolMgr = pm

	// Test with non-existent backend
	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/drain", nil)
	rr := httptest.NewRecorder()
	s.handleBackendDrain(rr, req, "127.0.0.1:5432")

	// Should return not found status
	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// Test handleBackendCancelDrain
func TestHandleBackendCancelDrain_NotFound(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	// Create a pool manager with no pools
	pm := pool.NewManager(log)
	s.poolMgr = pm

	// Test with non-existent backend
	req := httptest.NewRequest("POST", "/api/v1/backends/127.0.0.1:5432/cancel-drain", nil)
	rr := httptest.NewRecorder()
	s.handleBackendCancelDrain(rr, req, "127.0.0.1:5432")

	// Should return not found status
	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// Test handleBackendAction
func TestHandleBackendAction_InvalidMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	// Test with PUT (not POST)
	req := httptest.NewRequest("PUT", "/api/v1/backends/action", nil)
	rr := httptest.NewRecorder()

	s.handleBackendAction(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Test handleConnections
func TestHandleConnections_InvalidMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	// Test with POST (not GET)
	req := httptest.NewRequest("POST", "/api/v1/connections", nil)
	rr := httptest.NewRecorder()

	s.handleConnections(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Test handleConfig
func TestHandleConfig_InvalidMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	// Test with DELETE (not GET or POST)
	req := httptest.NewRequest("DELETE", "/api/v1/config", nil)
	rr := httptest.NewRecorder()

	s.handleConfig(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleSlowQueries_Limit(t *testing.T) {
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

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	if data["limit"] != float64(10) {
		t.Errorf("limit = %v, want 10", data["limit"])
	}
}

func TestHandleSlowQueries_InvalidLimit(t *testing.T) {
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
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestHandleSlowQueries_InvalidMethod(t *testing.T) {
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

func TestHandleRecentQueries_Limit(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/queries/recent?limit=25", nil)
	rr := httptest.NewRecorder()

	s.handleRecentQueries(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	if _, ok := data["recent_queries"]; !ok {
		t.Error("response should contain recent_queries")
	}
}

func TestHandleRecentQueries_InvalidMethod(t *testing.T) {
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

func TestHandleActiveTransactions_InvalidMethod(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/transactions", nil)
	rr := httptest.NewRecorder()

	s.handleActiveTransactions(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleActiveTransactions_Response(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: true, Token: testToken},
	}
	s, _ := NewServer(cfg, nil, nil, log, "", nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/transactions", nil)
	rr := httptest.NewRecorder()

	s.handleActiveTransactions(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	if _, ok := data["active_transactions"]; !ok {
		t.Error("response should contain active_transactions")
	}
	if _, ok := data["count"]; !ok {
		t.Error("response should contain count")
	}
}

func TestHandlePoolDetail_WithPool(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "detail-pool",
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

	req := httptest.NewRequest("GET", "/api/v1/pools/detail-pool", nil)
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	if data["name"] != "detail-pool" {
		t.Errorf("name = %v, want detail-pool", data["name"])
	}
}

func TestHandlePoolDetail_NotFound_InvalidName(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "exists",
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

	req := httptest.NewRequest("GET", "/api/v1/pools/nonexistent", nil)
	rr := httptest.NewRecorder()

	s.handlePoolDetail(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}
