package grpc

import (
	"context"
	"net"
	"net/http"
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
