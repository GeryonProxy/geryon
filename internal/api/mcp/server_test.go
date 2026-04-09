package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
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

func TestNewServer(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s := NewServer(cfg, nil, log, nil)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if s.sseLimit != 50 {
		t.Errorf("sseLimit = %d, want 50", s.sseLimit)
	}
}

func TestServer_StartStop(t *testing.T) {
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminMCPConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s := NewServer(cfg, nil, log, nil)

	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.Stop(ctx)
}

func TestServer_Initialize(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminMCPConfig{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	resp, err := http.Post("http://"+cfg.Listen+"/mcp/v1/initialize", "application/json", strings.NewReader(`{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test"}}`))
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	if data["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %q, want 2024-11-05", data["protocolVersion"])
	}
}

func TestServer_ToolsList(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminMCPConfig{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	resp, err := http.Get("http://" + cfg.Listen + "/mcp/v1/tools/list")
	if err != nil {
		t.Fatalf("ToolsList failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
	tools, ok := data["tools"].([]interface{})
	if !ok {
		t.Fatal("tools key should be an array")
	}
	if len(tools) == 0 {
		t.Error("tools array should not be empty")
	}
}

func TestServer_ResourcesList(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminMCPConfig{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	resp, err := http.Get("http://" + cfg.Listen + "/mcp/v1/resources/list")
	if err != nil {
		t.Fatalf("ResourcesList failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_SSE(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminMCPConfig{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	resp, err := http.Get("http://" + cfg.Listen + "/mcp/v1/sse")
	if err != nil {
		t.Fatalf("SSE failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", resp.Header.Get("Content-Type"))
	}

	// Close response and stop with valid context
	resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.Stop(ctx)
}

func TestServer_Auth_RejectsWithoutToken(t *testing.T) {
	addr := bindRandomPort(t)
	log, _ := logger.New("debug", "json")
	cfg := &config.AdminMCPConfig{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: true, Token: "mcp-secret"},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// Without auth
	resp, err := http.Get("http://" + cfg.Listen + "/mcp/v1/tools/list")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", resp.StatusCode)
	}

	// With auth
	req, _ := http.NewRequest("GET", "http://"+cfg.Listen+"/mcp/v1/tools/list", nil)
	req.Header.Set("Authorization", "Bearer mcp-secret")
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
	cfg := &config.AdminMCPConfig{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	resp, err := http.Get("http://" + cfg.Listen + "/mcp/v1/tools/list")
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
	cfg := &config.AdminMCPConfig{
		Listen: addr,
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	pm := pool.NewManager(log)
	s := NewServer(cfg, pm, log, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); s.Stop(ctx) }()

	// GET to initialize (requires POST)
	resp, err := http.Get("http://" + cfg.Listen + "/mcp/v1/initialize")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", resp.StatusCode)
	}
}

func TestMCPRateLimiter(t *testing.T) {
	rl := newMCPRateLimiter()
	if rl == nil {
		t.Fatal("newMCPRateLimiter returned nil")
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

func TestInitializeRequest(t *testing.T) {
	req := InitializeRequest{
		ProtocolVersion: "2024-11-05",
		Capabilities:    map[string]interface{}{},
		ClientInfo:      map[string]interface{}{"name": "test"},
	}
	if req.ProtocolVersion != "2024-11-05" {
		t.Errorf("ProtocolVersion = %q", req.ProtocolVersion)
	}
}

func TestInitializeResponse(t *testing.T) {
	resp := InitializeResponse{
		ProtocolVersion: "2024-11-05",
		ServerInfo:      map[string]interface{}{"name": "geryon-mcp"},
	}
	if resp.ProtocolVersion != "2024-11-05" {
		t.Errorf("ProtocolVersion = %q", resp.ProtocolVersion)
	}
}

func TestTool(t *testing.T) {
	tool := Tool{
		Name:        "geryon_pool_list",
		Description: "List all connection pools",
		InputSchema: map[string]interface{}{"type": "object"},
	}
	if tool.Name != "geryon_pool_list" {
		t.Errorf("Name = %q", tool.Name)
	}
}

func TestResource(t *testing.T) {
	res := Resource{
		URI:         "geryon://config",
		Name:        "Geryon Configuration",
		Description: "Current Geryon configuration",
		MIMEType:    "application/json",
	}
	if res.URI != "geryon://config" {
		t.Errorf("URI = %q", res.URI)
	}
}
