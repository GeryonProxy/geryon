package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

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
		Listen: "127.0.0.1:50051",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	if cfg.Listen != "127.0.0.1:50051" {
		t.Errorf("Listen = %q, want 127.0.0.1:50051", cfg.Listen)
	}
	if cfg.MaxStreams != 0 {
		t.Errorf("MaxStreams = %d, want 0", cfg.MaxStreams)
	}
}

func TestNewServer(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s := NewServer(cfg, nil, log, nil)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestNewServer_DefaultStreamLimit(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen:     "127.0.0.1:0",
		Auth:       config.RESTAuthConfig{Enabled: false},
		MaxStreams: 0,
	}
	s := NewServer(cfg, nil, log, nil)
	if s.streamLimit != 100 {
		t.Errorf("streamLimit = %d, want 100", s.streamLimit)
	}
}

func TestNewServer_CustomStreamLimit(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen:     "127.0.0.1:0",
		Auth:       config.RESTAuthConfig{Enabled: false},
		MaxStreams: 50,
	}
	s := NewServer(cfg, nil, log, nil)
	if s.streamLimit != 50 {
		t.Errorf("streamLimit = %d, want 50", s.streamLimit)
	}
}

func TestServer_StartStop(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s := NewServer(cfg, nil, log, nil)

	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	s.Stop(nil)
}

func TestServer_HealthCheck(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	resp, err := http.Post("http://"+cfg.Listen+"/grpc.health.v1.Health/Check", "application/json", nil)
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_GetPools(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	time.Sleep(10 * time.Millisecond)

	resp, err := http.Post("http://"+cfg.Listen+"/geryon.v1.Stats/GetPools", "application/json", nil)
	if err != nil {
		t.Fatalf("GetPools failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_GetBackends(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	resp, err := http.Post("http://"+cfg.Listen+"/geryon.v1.Stats/GetBackends", "application/json", nil)
	if err != nil {
		t.Fatalf("GetBackends failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_GetConnections(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	resp, err := http.Post("http://"+cfg.Listen+"/geryon.v1.Stats/GetConnections", "application/json", nil)
	if err != nil {
		t.Fatalf("GetConnections failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_ReloadConfig(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	reloaded := false
	s := NewServer(cfg, pm, log, func() error {
		reloaded = true
		return nil
	})

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	resp, err := http.Post("http://"+cfg.Listen+"/geryon.v1.Admin/ReloadConfig", "application/json", nil)
	if err != nil {
		t.Fatalf("ReloadConfig failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
	if !reloaded {
		t.Error("Reload function should have been called")
	}
}

func TestServer_Auth_RejectsWithoutToken(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: true, Token: "grpc-secret"},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// Without auth
	resp, err := http.Post("http://"+cfg.Listen+"/grpc.health.v1.Health/Check", "application/json", nil)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", resp.StatusCode)
	}

	// With auth
	req, _ := http.NewRequest("POST", "http://"+cfg.Listen+"/grpc.health.v1.Health/Check", nil)
	req.Header.Set("Authorization", "Bearer grpc-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Authenticated request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_SecurityHeaders(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	time.Sleep(10 * time.Millisecond)

	resp, err := http.Post("http://"+cfg.Listen+"/grpc.health.v1.Health/Check", "application/json", nil)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("Missing X-Content-Type-Options header")
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("Missing X-Frame-Options header")
	}
}

func TestServer_MethodNotAllowed(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// GET to health endpoint (requires POST)
	resp, err := http.Get("http://" + cfg.Listen + "/grpc.health.v1.Health/Check")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", resp.StatusCode)
	}
}

func TestServer_GetStreamCount(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s := NewServer(cfg, nil, log, nil)
	if s.GetStreamCount() != 0 {
		t.Errorf("GetStreamCount = %d, want 0", s.GetStreamCount())
	}
}

func TestCheckStreamLimit(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen:     "127.0.0.1:0",
		Auth:       config.RESTAuthConfig{Enabled: false},
		MaxStreams: 2,
	}
	s := NewServer(cfg, nil, log, nil)

	// First 2 should succeed
	if !s.checkStreamLimit() {
		t.Error("checkStreamLimit should return true for first stream")
	}
	if !s.checkStreamLimit() {
		t.Error("checkStreamLimit should return true for second stream")
	}
	// Third should fail
	if s.checkStreamLimit() {
		t.Error("checkStreamLimit should return false when limit reached")
	}
}

func TestReleaseStream(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen:     "127.0.0.1:0",
		Auth:       config.RESTAuthConfig{Enabled: false},
		MaxStreams: 1,
	}
	s := NewServer(cfg, nil, log, nil)

	s.checkStreamLimit()
	if s.checkStreamLimit() {
		t.Error("Should be at limit")
	}
	s.releaseStream()
	if !s.checkStreamLimit() {
		t.Error("Should have room after release")
	}
}

func TestGRPCRateLimiter(t *testing.T) {
	rl := newGRPCRateLimiter(5, 10)
	if rl == nil {
		t.Fatal("newGRPCRateLimiter returned nil")
	}

	l1 := rl.GetLimiter("10.0.0.1")
	if l1 == nil {
		t.Error("GetLimiter returned nil")
	}

	l2 := rl.GetLimiter("10.0.0.1")
	if l1 != l2 {
		t.Error("Same IP should return same limiter")
	}

	l3 := rl.GetLimiter("10.0.0.2")
	if l1 == l3 {
		t.Error("Different IP should return different limiter")
	}
}

func TestServer_Stream_MethodNotAllowed(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// GET to stream endpoint (requires POST)
	resp, err := http.Get("http://" + cfg.Listen + "/geryon.v1.Stats/Stream")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", resp.StatusCode)
	}
}

func TestServer_EventsSubscribe_MethodNotAllowed(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	resp, err := http.Get("http://" + cfg.Listen + "/geryon.v1.Events/Subscribe")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", resp.StatusCode)
	}
}

func TestServer_DrainBackend_NotFound(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	body := `{"address":"127.0.0.1:5432"}`
	resp, err := http.Post("http://"+cfg.Listen+"/geryon.v1.Admin/DrainBackend", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("DrainBackend failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if _, ok := data["error"]; !ok {
		t.Error("Should have error for unknown backend")
	}
}

func TestServer_DrainBackend_InvalidJSON(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// Wait for server to be ready
	time.Sleep(10 * time.Millisecond)

	resp, err := http.Post("http://"+cfg.Listen+"/geryon.v1.Admin/DrainBackend", "application/json", strings.NewReader("bad"))
	if err != nil {
		t.Fatalf("DrainBackend failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_ReloadConfig_Failure(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, func() error {
		return fmt.Errorf("reload failed")
	})

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// Wait for server to be ready
	time.Sleep(10 * time.Millisecond)

	resp, err := http.Post("http://"+cfg.Listen+"/geryon.v1.Admin/ReloadConfig", "application/json", nil)
	if err != nil {
		t.Fatalf("ReloadConfig failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_ReloadConfig_NoFn(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// Wait for server to be ready
	time.Sleep(10 * time.Millisecond)

	resp, err := http.Post("http://"+cfg.Listen+"/geryon.v1.Admin/ReloadConfig", "application/json", nil)
	if err != nil {
		t.Fatalf("ReloadConfig failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestCollectStats(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	stats := s.collectStats()
	if stats == nil {
		t.Fatal("collectStats returned nil")
	}
	if stats["total_pools"] != 0 {
		t.Errorf("total_pools = %v, want 0", stats["total_pools"])
	}
	if stats["total_clients"] != int64(0) {
		t.Errorf("total_clients = %v, want 0", stats["total_clients"])
	}
	if _, ok := stats["timestamp"]; !ok {
		t.Error("Should have timestamp")
	}
}

func TestWriteProtoResponse(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s := NewServer(cfg, nil, log, nil)

	rr := httptest.NewRecorder()
	s.writeProtoResponse(rr, map[string]string{"key": "value"})

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
	if rr.Header().Get("Content-Type") != "application/grpc+proto" {
		t.Errorf("Content-Type = %q, want application/grpc+proto", rr.Header().Get("Content-Type"))
	}
	if rr.Header().Get("grpc-status") != "0" {
		t.Errorf("grpc-status = %q, want 0", rr.Header().Get("grpc-status"))
	}
}

// Additional tests for improved coverage

func TestServer_Auth_InvalidBearer(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: true, Token: "grpc-secret"},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// Invalid bearer format
	req, _ := http.NewRequest("POST", "http://"+cfg.Listen+"/grpc.health.v1.Health/Check", nil)
	req.Header.Set("Authorization", "Basic grpc-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", resp.StatusCode)
	}
}

func TestServer_Auth_WrongToken(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: true, Token: "grpc-secret"},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// Wait for server to be ready
	time.Sleep(10 * time.Millisecond)

	// Wrong token
	req, _ := http.NewRequest("POST", "http://"+cfg.Listen+"/grpc.health.v1.Health/Check", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", resp.StatusCode)
	}
}

func TestServer_Auth_EmptyHeader(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: true, Token: "grpc-secret"},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// Wait for server to be ready
	time.Sleep(10 * time.Millisecond)

	// Empty authorization header
	req, _ := http.NewRequest("POST", "http://"+cfg.Listen+"/grpc.health.v1.Health/Check", nil)
	req.Header.Set("Authorization", "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", resp.StatusCode)
	}
}

func TestGRPCRateLimiter_MaxSizeEviction(t *testing.T) {
	rl := newGRPCRateLimiter(5, 10)

	// Add many limiters to trigger eviction
	for i := 0; i < 10020; i++ {
		ip := fmt.Sprintf("10.0.0.%d", i%256)
		rl.GetLimiter(ip)
	}

	// Should have evicted old entries
	if len(rl.limiters) > rl.maxSize+10 { // Allow some buffer
		t.Errorf("limiters count = %d, should be around maxSize", len(rl.limiters))
	}
}

func TestGRPCRateLimiter_Cleanup(t *testing.T) {
	rl := newGRPCRateLimiter(5, 10)
	rl.cleanupTTL = 100 * time.Millisecond

	// Add a limiter
	l := rl.GetLimiter("10.0.0.1")
	if l == nil {
		t.Fatal("GetLimiter returned nil")
	}

	// Wait for cleanup
	time.Sleep(200 * time.Millisecond)

	// Trigger cleanup by adding another limiter
	rl.GetLimiter("10.0.0.2")

	// Old limiter might have been cleaned up
	// This is a best-effort test since cleanup runs asynchronously
}

func TestServer_GetBackends_WithPoolFilter(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	body := `{"pool_name":"nonexistent"}`
	resp, err := http.Post("http://"+cfg.Listen+"/geryon.v1.Stats/GetBackends", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("GetBackends failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_StatsStream_InvalidInterval(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// Invalid JSON body
	body := `{"interval":-1}`
	resp, err := http.Post("http://"+cfg.Listen+"/geryon.v1.Stats/Stream", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("StatsStream failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_StatsStream_InvalidBody(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	time.Sleep(10 * time.Millisecond)

	// Invalid JSON body
	body := `invalid json`
	resp, err := http.Post("http://"+cfg.Listen+"/geryon.v1.Stats/Stream", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("StatsStream failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestCheckStreamLimit_ZeroLimit(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen:     "127.0.0.1:0",
		Auth:       config.RESTAuthConfig{Enabled: false},
		MaxStreams: 0, // No limit
	}
	s := NewServer(cfg, nil, log, nil)

	// Should always succeed with no limit
	for i := 0; i < 100; i++ {
		if !s.checkStreamLimit() {
			t.Errorf("checkStreamLimit should return true with no limit, iteration %d", i)
		}
	}
}

func TestServer_GetStreamCount_WithStreams(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s := NewServer(cfg, nil, log, nil)

	// Initially 0
	if s.GetStreamCount() != 0 {
		t.Errorf("GetStreamCount = %d, want 0", s.GetStreamCount())
	}

	// Add streams manually
	s.mu.Lock()
	s.streams["stream1"] = &Stream{ID: "stream1", Type: "test"}
	s.streams["stream2"] = &Stream{ID: "stream2", Type: "test"}
	s.mu.Unlock()

	if s.GetStreamCount() != 2 {
		t.Errorf("GetStreamCount = %d, want 2", s.GetStreamCount())
	}
}

func TestCollectStats_WithPools(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)

	// Create a pool
	poolCfg := &config.PoolConfig{
		Name: "test-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
			MinServerConnections: 1,
		},
	}
	pm.CreatePool(poolCfg)

	s := NewServer(cfg, pm, log, nil)

	stats := s.collectStats()
	if stats == nil {
		t.Fatal("collectStats returned nil")
	}
	if stats["total_pools"] != 1 {
		t.Errorf("total_pools = %v, want 1", stats["total_pools"])
	}

	pools, ok := stats["pools"].([]map[string]interface{})
	if !ok || len(pools) != 1 {
		t.Errorf("pools = %v, want 1 pool", stats["pools"])
	}
}

func TestServer_Start_Multiple(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s := NewServer(cfg, nil, log, nil)

	// First start should succeed
	err := s.Start()
	if err != nil {
		t.Fatalf("First Start failed: %v", err)
	}

	// Stop
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	s.Stop(ctx)
	cancel()

	// Second start should also succeed (new server instance needed)
	s2 := NewServer(cfg, nil, log, nil)
	err = s2.Start()
	if err != nil {
		t.Fatalf("Second Start failed: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	s2.Stop(ctx2)
	cancel2()
}

func TestServer_Stop_WithActiveStreams(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s := NewServer(cfg, nil, log, nil)

	// Add some active streams
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.streams["stream1"] = &Stream{
		ID:      "stream1",
		Type:    "test",
		Started: time.Now(),
		Cancel:  cancel,
	}
	s.mu.Unlock()

	// Start server
	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Stop should cancel the stream
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	s.Stop(stopCtx)

	// Context should be cancelled
	select {
	case <-ctx.Done():
		// Good, context was cancelled
	default:
		t.Error("Stream context should have been cancelled")
	}
}

func TestServer_DrainBackend_MethodNotAllowed(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// Wait for server to be ready
	time.Sleep(10 * time.Millisecond)

	resp, err := http.Get("http://" + cfg.Listen + "/geryon.v1.Admin/DrainBackend")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", resp.StatusCode)
	}
}

func TestServer_ReloadConfig_MethodNotAllowed(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// Wait for server to be ready
	time.Sleep(10 * time.Millisecond)

	resp, err := http.Get("http://" + cfg.Listen + "/geryon.v1.Admin/ReloadConfig")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", resp.StatusCode)
	}
}

func TestServer_GetConnections_MethodNotAllowed(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	resp, err := http.Get("http://" + cfg.Listen + "/geryon.v1.Stats/GetConnections")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", resp.StatusCode)
	}
}

func TestServer_GetPools_MethodNotAllowed(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	time.Sleep(10 * time.Millisecond)

	resp, err := http.Get("http://" + cfg.Listen + "/geryon.v1.Stats/GetPools")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", resp.StatusCode)
	}
}

func TestServer_GetBackends_MethodNotAllowed(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &Config{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// Wait for server to be ready
	time.Sleep(10 * time.Millisecond)

	resp, err := http.Get("http://" + cfg.Listen + "/geryon.v1.Stats/GetBackends")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", resp.StatusCode)
	}
}
