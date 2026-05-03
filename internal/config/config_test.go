package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig returned nil")
	}
	if cfg.Global.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.Global.LogLevel)
	}
	if cfg.Global.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want json", cfg.Global.LogFormat)
	}
	if cfg.Pools == nil {
		t.Error("Pools should not be nil")
	}
	if len(cfg.Pools) != 0 {
		t.Errorf("Pools should be empty, got %d", len(cfg.Pools))
	}
	if cfg.Cluster.Enabled {
		t.Error("Cluster should be disabled by default")
	}
	if cfg.Auth.Mode != "passthrough" {
		t.Errorf("Auth.Mode = %q, want passthrough", cfg.Auth.Mode)
	}
}

func TestValidate_AdminAuthRequired(t *testing.T) {
	cfg := DefaultConfig()
	// DefaultConfig has admin auth enabled but no token
	err := Validate(cfg)
	if err == nil {
		t.Error("Should fail when admin auth enabled but no token")
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("Error = %q, should mention auth", err.Error())
	}
}

func TestValidate_DisableAdminAuth(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Admin.REST.Auth.Enabled = false
	cfg.Admin.GRPC.Auth.Enabled = false
	cfg.Admin.MCP.Auth.Enabled = false
	cfg.Admin.Dashboard.Auth.Enabled = false
	cfg.Admin.Dashboard.Enabled = false

	err := Validate(cfg)
	if err != nil {
		t.Errorf("Validate failed: %v", err)
	}
}

func TestValidate_PoolValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Admin.REST.Auth.Enabled = false
	cfg.Admin.GRPC.Auth.Enabled = false
	cfg.Admin.MCP.Auth.Enabled = false
	cfg.Admin.Dashboard.Auth.Enabled = false
	cfg.Admin.Dashboard.Enabled = false

	// Empty pool name
	cfg.Pools = append(cfg.Pools, PoolConfig{Name: "", Body: "postgresql", Mode: "session"})
	err := Validate(cfg)
	if err == nil {
		t.Error("Should fail for empty pool name")
	}

	// Invalid pool body
	cfg.Pools[0] = PoolConfig{Name: "test", Body: "oracle", Mode: "session"}
	err = Validate(cfg)
	if err == nil {
		t.Error("Should fail for invalid pool body")
	}

	// Invalid pool mode
	cfg.Pools[0] = PoolConfig{Name: "test", Body: "postgresql", Mode: "streaming"}
	err = Validate(cfg)
	if err == nil {
		t.Error("Should fail for invalid pool mode")
	}

	// Valid pool
	cfg.Pools[0] = PoolConfig{
		Name: "test", Body: "postgresql", Mode: "session",
		Limits: LimitConfig{
			MaxClientConnections: 100,
			MaxServerConnections: 50,
			MinServerConnections: 5,
		},
		Listen: ListenConfig{Host: "127.0.0.1", Port: 5432},
	}
	err = Validate(cfg)
	if err != nil {
		t.Errorf("Validate failed for valid pool: %v", err)
	}
}

func TestValidate_DuplicatePoolNames(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Admin.REST.Auth.Enabled = false
	cfg.Admin.GRPC.Auth.Enabled = false
	cfg.Admin.MCP.Auth.Enabled = false
	cfg.Admin.Dashboard.Auth.Enabled = false
	cfg.Admin.Dashboard.Enabled = false

	cfg.Pools = []PoolConfig{
		{Name: "pool1", Body: "postgresql", Mode: "session"},
		{Name: "pool1", Body: "mysql", Mode: "transaction"},
	}
	err := Validate(cfg)
	if err == nil {
		t.Error("Should fail for duplicate pool names")
	}
}

func TestValidate_NegativeLimits(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Admin.REST.Auth.Enabled = false
	cfg.Admin.GRPC.Auth.Enabled = false
	cfg.Admin.MCP.Auth.Enabled = false
	cfg.Admin.Dashboard.Auth.Enabled = false
	cfg.Admin.Dashboard.Enabled = false

	cfg.Pools = []PoolConfig{{
		Name: "test", Body: "postgresql", Mode: "session",
		Limits: LimitConfig{MaxClientConnections: -1},
	}}
	err := Validate(cfg)
	if err == nil {
		t.Error("Should fail for negative MaxClientConnections")
	}
}

func TestValidate_Cluster(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Admin.REST.Auth.Enabled = false
	cfg.Admin.GRPC.Auth.Enabled = false
	cfg.Admin.MCP.Auth.Enabled = false
	cfg.Admin.Dashboard.Auth.Enabled = false
	cfg.Admin.Dashboard.Enabled = false

	cfg.Cluster.Enabled = true
	cfg.Cluster.NodeID = ""
	err := Validate(cfg)
	if err == nil {
		t.Error("Should fail when cluster enabled but NodeID empty")
	}

	cfg.Cluster.NodeID = "node-1"
	cfg.Cluster.Raft.Listen = ""
	err = Validate(cfg)
	if err == nil {
		t.Error("Should fail when cluster enabled but Raft.Listen empty")
	}
}

func TestValidate_PortConflict(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Admin.REST.Auth.Enabled = false
	cfg.Admin.GRPC.Auth.Enabled = false
	cfg.Admin.MCP.Auth.Enabled = false
	cfg.Admin.Dashboard.Auth.Enabled = false
	cfg.Admin.Dashboard.Enabled = false

	cfg.Pools = []PoolConfig{
		{Name: "p1", Body: "postgresql", Mode: "session", Listen: ListenConfig{Port: 5432}},
		{Name: "p2", Body: "postgresql", Mode: "session", Listen: ListenConfig{Port: 5432}},
	}
	err := Validate(cfg)
	if err == nil {
		t.Error("Should fail for port conflict")
	}
}

func TestExpandEnvVars(t *testing.T) {
	os.Setenv("GERYON_HOST", "localhost")
	os.Setenv("GERYON_PORT", "5432")
	os.Setenv("OTHER_VAR", "should_not_expand")
	defer func() {
		os.Unsetenv("GERYON_HOST")
		os.Unsetenv("GERYON_PORT")
		os.Unsetenv("OTHER_VAR")
	}()

	result := expandEnvVars("host=${GERYON_HOST}, port=${GERYON_PORT}")
	if result != "host=localhost, port=5432" {
		t.Errorf("expandEnvVars = %q, want %q", result, "host=localhost, port=5432")
	}

	// Non-GERYON_ prefix should not expand
	result2 := expandEnvVars("${OTHER_VAR}")
	if result2 != "${OTHER_VAR}" {
		t.Errorf("Non-GERYON var should not expand, got %q", result2)
	}

	// Default value
	result3 := expandEnvVars("${GERYON_MISSING:-fallback}")
	if result3 != "fallback" {
		t.Errorf("Default value = %q, want fallback", result3)
	}
}

func TestCompareConfigs(t *testing.T) {
	old := DefaultConfig()
	new := DefaultConfig()

	// No changes
	changes := CompareConfigs(old, new)
	if len(changes) != 0 {
		t.Errorf("Expected no changes, got %d", len(changes))
	}

	// Log level change
	new.Global.LogLevel = "debug"
	changes = CompareConfigs(old, new)
	if len(changes) != 1 {
		t.Errorf("Expected 1 change, got %d", len(changes))
	}
}

func TestCompareConfigs_PoolChanges(t *testing.T) {
	old := &Config{Pools: []PoolConfig{{Name: "p1", Mode: "session"}}}
	new := &Config{Pools: []PoolConfig{{Name: "p1", Mode: "session"}, {Name: "p2", Mode: "transaction"}}}

	changes := CompareConfigs(old, new)
	if len(changes) == 0 {
		t.Error("Should detect pool addition")
	}
}

func TestIsSafeReload(t *testing.T) {
	old := &Config{Pools: []PoolConfig{
		{Name: "p1", Body: "postgresql", Mode: "session", Listen: ListenConfig{Port: 5432}},
	}}
	new := &Config{Pools: []PoolConfig{
		{Name: "p1", Body: "postgresql", Mode: "session", Listen: ListenConfig{Port: 5433}},
	}}

	safe, reasons := IsSafeReload(old, new)
	if safe {
		t.Error("Port change should be unsafe")
	}
	if len(reasons) == 0 {
		t.Error("Should have unsafe reasons")
	}

	// Body change
	new2 := &Config{Pools: []PoolConfig{
		{Name: "p1", Body: "mysql", Mode: "session", Listen: ListenConfig{Port: 5432}},
	}}
	safe2, _ := IsSafeReload(old, new2)
	if safe2 {
		t.Error("Body change should be unsafe")
	}
}

func TestWatcher(t *testing.T) {
	log, _ := logger.New("info", "json")
	w := NewWatcher("nonexistent.yaml", 100*time.Millisecond, log)
	if w == nil {
		t.Fatal("NewWatcher returned nil")
	}
	if w.IsRunning() {
		t.Error("Watcher should not be running yet")
	}
}

func TestWatcher_StartStop(t *testing.T) {
	log, _ := logger.New("info", "json")

	// Create a temp file
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(cfgPath, []byte("# test config"), 0644)

	w := NewWatcher(cfgPath, 100*time.Millisecond, log)
	err := w.Start()
	if err != nil {
		t.Fatalf("Watcher.Start failed: %v", err)
	}
	if !w.IsRunning() {
		t.Error("Watcher should be running")
	}

	w.Stop()
	if w.IsRunning() {
		t.Error("Watcher should be stopped")
	}

	// Double stop should be safe
	w.Stop()
}

func TestWatcher_OnChange(t *testing.T) {
	log, _ := logger.New("info", "json")
	w := NewWatcher("test.yaml", time.Second, log)
	called := false
	w.OnChange(func(cfg *Config) {
		called = true
	})
	// We can't easily trigger a change without modifying a file,
	// but we can verify the callback is set without panic
	if called {
		t.Error("Callback should not be called yet")
	}
}

func TestReloadManager(t *testing.T) {
	log, _ := logger.New("info", "json")
	cfg := DefaultConfig()
	cfg.Admin.REST.Auth.Enabled = false
	cfg.Admin.GRPC.Auth.Enabled = false
	cfg.Admin.MCP.Auth.Enabled = false
	cfg.Admin.Dashboard.Auth.Enabled = false
	cfg.Admin.Dashboard.Enabled = false

	m := NewReloadManager(cfg, log)
	if m == nil {
		t.Fatal("NewReloadManager returned nil")
	}

	// Get should return the initial config
	current := m.Get()
	if current == nil {
		t.Error("Get returned nil")
	}
}

func TestReloadManager_Apply(t *testing.T) {
	log, _ := logger.New("info", "json")
	cfg := DefaultConfig()
	cfg.Admin.REST.Auth.Enabled = false
	cfg.Admin.GRPC.Auth.Enabled = false
	cfg.Admin.MCP.Auth.Enabled = false
	cfg.Admin.Dashboard.Auth.Enabled = false
	cfg.Admin.Dashboard.Enabled = false

	m := NewReloadManager(cfg, log)
	var applied *Config
	m.OnApply(func(c *Config) error {
		applied = c
		return nil
	})

	newCfg := DefaultConfig()
	newCfg.Admin.REST.Auth.Enabled = false
	newCfg.Admin.GRPC.Auth.Enabled = false
	newCfg.Admin.MCP.Auth.Enabled = false
	newCfg.Admin.Dashboard.Auth.Enabled = false
	newCfg.Admin.Dashboard.Enabled = false
	newCfg.Global.LogLevel = "debug"

	err := m.Apply(newCfg)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if applied == nil {
		t.Error("Apply callback should have been called")
	}
	if m.Get().Global.LogLevel != "debug" {
		t.Error("Config should be updated after Apply")
	}
}

func TestReloadManager_Reload(t *testing.T) {
	log, _ := logger.New("info", "json")
	cfg := DefaultConfig()
	cfg.Admin.REST.Auth.Enabled = false
	cfg.Admin.GRPC.Auth.Enabled = false
	cfg.Admin.MCP.Auth.Enabled = false
	cfg.Admin.Dashboard.Auth.Enabled = false
	cfg.Admin.Dashboard.Enabled = false

	m := NewReloadManager(cfg, log)
	m.OnApply(func(c *Config) error {
		// Disable admin auth on reload to pass validation
		c.Admin.REST.Auth.Enabled = false
		c.Admin.GRPC.Auth.Enabled = false
		c.Admin.MCP.Auth.Enabled = false
		c.Admin.Dashboard.Auth.Enabled = false
		c.Admin.Dashboard.Enabled = false
		return nil
	})

	// The Load function uses DefaultConfig which has admin auth enabled.
	// Our OnApply callback can't intercept before Validate, so we test
	// that Reload fails with the expected auth error.
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	content := `pools:
  - name: testpool
    body: postgresql
    mode: session
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	// Reload will fail validation because loaded config has auth enabled
	err := m.Reload(cfgPath)
	if err == nil {
		t.Error("Reload should fail validation (auth enabled without token)")
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("Expected auth error, got: %v", err)
	}
}

func TestBackupConfig(t *testing.T) {
	cfg := &Config{
		Global: GlobalConfig{LogLevel: "info", LogFormat: "json"},
		Pools:  []PoolConfig{{Name: "test", Body: "postgresql", Mode: "session"}},
	}

	tmpDir := t.TempDir()
	err := BackupConfig(cfg, tmpDir)
	if err != nil {
		t.Fatalf("BackupConfig failed: %v", err)
	}

	// Check a backup file was created
	entries, _ := os.ReadDir(tmpDir)
	if len(entries) == 0 {
		t.Error("No backup file created")
	}
}

func TestValidate_AdminListenEmpty(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Admin.REST.Listen = ""
	err := Validate(cfg)
	if err == nil {
		t.Error("Should fail for empty admin listen address")
	}
}

func TestValidate_AuthTokenRequired(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Admin.REST.Auth.Enabled = true
	cfg.Admin.REST.Auth.Token = ""
	err := Validate(cfg)
	if err == nil {
		t.Error("Should fail when REST auth enabled but no token")
	}
}

func TestValidate_AllAdminAuthTokensRequired(t *testing.T) {
	// Test that all admin auth configurations require tokens when enabled
	tests := []struct {
		name        string
		authEnabled func(cfg *Config)
		wantErr     bool
		errContains string
	}{
		{
			name: "GRPC auth enabled no token",
			authEnabled: func(cfg *Config) {
				cfg.Admin.REST.Auth.Enabled = false
				cfg.Admin.Dashboard.Auth.Enabled = false
				cfg.Admin.MCP.Auth.Enabled = false
				cfg.Admin.GRPC.Auth.Enabled = true
				cfg.Admin.GRPC.Auth.Token = ""
			},
			wantErr:     true,
			errContains: "gRPC auth is enabled but no auth token",
		},
		{
			name: "MCP auth enabled no token",
			authEnabled: func(cfg *Config) {
				cfg.Admin.REST.Auth.Enabled = false
				cfg.Admin.GRPC.Auth.Enabled = false
				cfg.Admin.Dashboard.Auth.Enabled = false
				cfg.Admin.MCP.Auth.Enabled = true
				cfg.Admin.MCP.Auth.Token = ""
			},
			wantErr:     true,
			errContains: "MCP auth is enabled but no auth token",
		},
		{
			name: "Dashboard auth enabled no token",
			authEnabled: func(cfg *Config) {
				cfg.Admin.REST.Auth.Enabled = false
				cfg.Admin.GRPC.Auth.Enabled = false
				cfg.Admin.MCP.Auth.Enabled = false
				cfg.Admin.Dashboard.Auth.Enabled = true
				cfg.Admin.Dashboard.Auth.Token = ""
			},
			wantErr:     true,
			errContains: "Dashboard auth is enabled but no auth token",
		},
		{
			name: "REST auth enabled no token",
			authEnabled: func(cfg *Config) {
				cfg.Admin.GRPC.Auth.Enabled = false
				cfg.Admin.MCP.Auth.Enabled = false
				cfg.Admin.Dashboard.Auth.Enabled = false
				cfg.Admin.REST.Auth.Enabled = true
				cfg.Admin.REST.Auth.Token = ""
			},
			wantErr:     true,
			errContains: "REST auth is enabled but no auth token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.authEnabled(cfg)
			err := Validate(cfg)
			if tt.wantErr {
				if err == nil {
					t.Error("Validate() = nil, want error")
				} else if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("Validate() error = %q, want containing %q", err.Error(), tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("Validate() = %v, want nil", err)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestDefaultConfig_AdminDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Admin.REST.Listen != "127.0.0.1:8080" {
		t.Errorf("REST listen = %q, want 127.0.0.1:8080", cfg.Admin.REST.Listen)
	}
	if cfg.Admin.MCP.Transport != "sse" {
		t.Errorf("MCP transport = %q, want sse", cfg.Admin.MCP.Transport)
	}
	if cfg.Admin.Dashboard.Enabled {
		t.Error("Dashboard should be disabled by default")
	}
}

func TestHotReload_NoChanges(t *testing.T) {
	// HotReload requires a valid config file — test with two identical DefaultConfigs
	// via CompareConfigs (the core logic of HotReload)
	current := DefaultConfig()
	changes := CompareConfigs(current, current)
	if len(changes) != 0 {
		t.Errorf("CompareConfigs with identical configs should return 0 changes, got %d: %v", len(changes), changes)
	}
}

func TestHotReload_InvalidFile(t *testing.T) {
	log, _ := logger.New("error", "json")
	_, err := HotReload(context.Background(), "/nonexistent/config.yaml", &Config{}, func(cfg *Config) error {
		return nil
	}, log)
	if err == nil {
		t.Error("HotReload should fail for nonexistent file")
	}
}

func TestCompareConfigs_DetectChanges(t *testing.T) {
	old := &Config{
		Global: GlobalConfig{
			LogLevel:  "info",
			LogFormat: "text",
		},
		Admin: AdminConfig{
			REST: AdminRESTConfig{
				Listen: "127.0.0.1:8080",
			},
		},
		Pools: []PoolConfig{
			{Name: "pool1", Mode: "transaction"},
		},
	}

	new := &Config{
		Global: GlobalConfig{
			LogLevel:  "debug",
			LogFormat: "json",
		},
		Admin: AdminConfig{
			REST: AdminRESTConfig{
				Listen: "127.0.0.1:9090",
			},
		},
		Pools: []PoolConfig{
			{Name: "pool1", Mode: "session"},
			{Name: "pool2", Mode: "transaction"},
		},
	}

	changes := CompareConfigs(old, new)
	if len(changes) == 0 {
		t.Fatal("CompareConfigs should detect changes")
	}

	// Check specific changes
	foundLogLevel := false
	foundNewPool := false
	for _, ch := range changes {
		if ch != "" && len(ch) > 0 {
			if ch == "global.log_level: info -> debug" {
				foundLogLevel = true
			}
			if ch == "pool.pool2: added" {
				foundNewPool = true
			}
		}
	}
	if !foundLogLevel {
		t.Error("Should detect log_level change")
	}
	if !foundNewPool {
		t.Error("Should detect new pool")
	}
}

func TestCompareConfigs_PoolRemoved(t *testing.T) {
	old := &Config{
		Pools: []PoolConfig{
			{Name: "pool1"},
			{Name: "pool2"},
		},
	}
	new := &Config{
		Pools: []PoolConfig{
			{Name: "pool1"},
		},
	}

	changes := CompareConfigs(old, new)
	foundRemove := false
	for _, ch := range changes {
		if ch == "pool.pool2: removed" {
			foundRemove = true
		}
	}
	if !foundRemove {
		t.Error("Should detect pool removal")
	}
}

func TestIsSafeReload_Basic(t *testing.T) {
	old := &Config{
		Pools: []PoolConfig{
			{Name: "pool1", Mode: "transaction"},
		},
	}
	new := &Config{
		Pools: []PoolConfig{
			{Name: "pool1", Mode: "transaction", Limits: LimitConfig{MaxClientConnections: 100}},
		},
	}

	safe, unsafe := IsSafeReload(old, new)
	if !safe {
		t.Error("Should be safe reload")
	}
	if len(unsafe) > 0 {
		t.Errorf("Should have no unsafe changes: %v", unsafe)
	}
}

// Test unescapeValue function
func TestUnescapeValue(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`hello`, "hello"},
		{`hello\nworld`, "hello\nworld"},
		{`hello\tworld`, "hello\tworld"},
		{`hello\rworld`, "hello\rworld"},
		{`hello\\world`, "hello\\world"},
		{`hello\"world`, "hello\"world"},
		{`hello\'world`, "hello'world"},
		{`hello\0world`, "hello\x00world"},
		{`\x41`, "A"},         // hex escape
		{`\xGG`, `\xxGG`},     // invalid hex - outputs \x prefix chars
		{`\x4`, `\xx4`},       // truncated hex
		{`\u0041`, "A"},       // unicode escape
		{`\uGGGG`, `\uuGGGG`}, // invalid unicode
		{`\u004`, `\uu004`},   // truncated unicode
		{`\z`, `\z`},          // unknown escape
		{`\`, `\`},            // trailing backslash
		{"", ""},              // empty
	}

	for _, tt := range tests {
		got := unescapeValue(tt.input)
		if got != tt.expected {
			t.Errorf("unescapeValue(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// Test HotReload with a valid config file that has changes
func TestHotReload_ValidFileWithChanges(t *testing.T) {
	log, _ := logger.New("error", "json")

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	content := `global:
  log_level: info
  log_format: json
admin:
  rest:
    listen: "127.0.0.1:8080"
    auth:
      enabled: false
  grpc:
    listen: "127.0.0.1:9090"
    auth:
      enabled: false
  mcp:
    listen: "127.0.0.1:8081"
    auth:
      enabled: false
  dashboard:
    enabled: false
    auth:
      enabled: false
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	current := DefaultConfig()
	current.Admin.REST.Auth.Enabled = false
	current.Admin.GRPC.Auth.Enabled = false
	current.Admin.MCP.Auth.Enabled = false
	current.Admin.Dashboard.Auth.Enabled = false
	current.Admin.Dashboard.Enabled = false

	newCfg, err := HotReload(context.Background(), cfgPath, current, func(cfg *Config) error {
		return nil
	}, log)
	if err != nil {
		t.Fatalf("HotReload failed: %v", err)
	}
	if newCfg == nil {
		t.Fatal("HotReload returned nil config")
	}
}

// Test HotReload with no changes (same config loaded from file)
func TestHotReload_SameConfig(t *testing.T) {
	log, _ := logger.New("error", "json")

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	content := `global:
  log_level: info
  log_format: json
admin:
  rest:
    listen: "127.0.0.1:8080"
    auth:
      enabled: false
  grpc:
    listen: "127.0.0.1:9090"
    auth:
      enabled: false
  mcp:
    listen: "127.0.0.1:8081"
    auth:
      enabled: false
  dashboard:
    enabled: false
    auth:
      enabled: false
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	// Load current from same file
	current, _ := Load(cfgPath)

	newCfg, err := HotReload(context.Background(), cfgPath, current, func(cfg *Config) error {
		return nil
	}, log)
	if err != nil {
		t.Fatalf("HotReload failed: %v", err)
	}
	// Should return the same config object
	if newCfg != current {
		t.Error("HotReload should return current when no changes detected")
	}
}

// Test HotReload with apply function error
func TestHotReload_ApplyError(t *testing.T) {
	log, _ := logger.New("error", "json")

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	content := `global:
  log_level: debug
  log_format: json
admin:
  rest:
    listen: "127.0.0.1:8080"
    auth:
      enabled: false
  grpc:
    listen: "127.0.0.1:9090"
    auth:
      enabled: false
  mcp:
    listen: "127.0.0.1:8081"
    auth:
      enabled: false
  dashboard:
    enabled: false
    auth:
      enabled: false
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	current := DefaultConfig()
	current.Admin.REST.Auth.Enabled = false
	current.Admin.GRPC.Auth.Enabled = false
	current.Admin.MCP.Auth.Enabled = false
	current.Admin.Dashboard.Auth.Enabled = false
	current.Admin.Dashboard.Enabled = false

	_, err := HotReload(context.Background(), cfgPath, current, func(cfg *Config) error {
		return fmt.Errorf("apply failed")
	}, log)
	if err == nil {
		t.Fatal("HotReload should fail when applyFn returns error")
	}
	if !strings.Contains(err.Error(), "apply") {
		t.Errorf("Error should mention apply: %v", err)
	}
}

// Test IsSafeReload with admin port changes
func TestIsSafeReload_AdminPortChange(t *testing.T) {
	old := &Config{
		Admin: AdminConfig{
			REST: AdminRESTConfig{Listen: "127.0.0.1:8080"},
			MCP:  AdminMCPConfig{Listen: "127.0.0.1:8081"},
			GRPC: AdminGRPCConfig{Listen: "127.0.0.1:9090"},
		},
	}
	new := &Config{
		Admin: AdminConfig{
			REST: AdminRESTConfig{Listen: "127.0.0.1:9090"},
			MCP:  AdminMCPConfig{Listen: "127.0.0.1:9091"},
			GRPC: AdminGRPCConfig{Listen: "127.0.0.1:10090"},
		},
	}

	safe, unsafe := IsSafeReload(old, new)
	if safe {
		t.Error("Should be unsafe due to admin port changes")
	}
	if len(unsafe) < 3 {
		t.Errorf("Expected at least 3 unsafe reasons, got %d: %v", len(unsafe), unsafe)
	}
}

// Test CompareConfigs with server connection limit changes
func TestCompareConfigs_ServerLimitChanges(t *testing.T) {
	old := &Config{
		Pools: []PoolConfig{
			{Name: "pool1", Mode: "transaction", Limits: LimitConfig{MaxServerConnections: 10}},
		},
	}
	new := &Config{
		Pools: []PoolConfig{
			{Name: "pool1", Mode: "transaction", Limits: LimitConfig{MaxServerConnections: 20}},
		},
	}

	changes := CompareConfigs(old, new)
	found := false
	for _, ch := range changes {
		if strings.Contains(ch, "max_server_connections") {
			found = true
		}
	}
	if !found {
		t.Error("Should detect max_server_connections change")
	}
}

// Test CompareConfigs with admin listen change
func TestCompareConfigs_AdminListenChange(t *testing.T) {
	old := &Config{
		Admin: AdminConfig{
			REST: AdminRESTConfig{Listen: "127.0.0.1:8080"},
		},
	}
	new := &Config{
		Admin: AdminConfig{
			REST: AdminRESTConfig{Listen: "127.0.0.1:9090"},
		},
	}

	changes := CompareConfigs(old, new)
	found := false
	for _, ch := range changes {
		if strings.Contains(ch, "admin.rest.listen") {
			found = true
		}
	}
	if !found {
		t.Error("Should detect admin REST listen change")
	}
}

// Test ReloadManager with concurrent Get
func TestReloadManager_ConcurrentGet(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := DefaultConfig()
	cfg.Admin.REST.Auth.Enabled = false
	cfg.Admin.GRPC.Auth.Enabled = false
	cfg.Admin.MCP.Auth.Enabled = false
	cfg.Admin.Dashboard.Auth.Enabled = false
	cfg.Admin.Dashboard.Enabled = false

	m := NewReloadManager(cfg, log)

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			c := m.Get()
			if c == nil {
				t.Error("Get returned nil")
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

// Test Watcher with context cancellation
func TestWatcher_ContextCancel(t *testing.T) {
	log, _ := logger.New("error", "json")
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(cfgPath, []byte("# test"), 0644)

	w := NewWatcher(cfgPath, 100*time.Millisecond, log)
	err := w.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Let it run briefly
	time.Sleep(150 * time.Millisecond)

	w.Stop()
	if w.IsRunning() {
		t.Error("Watcher should be stopped")
	}
}

// --- NewWatcher zero interval ---

func TestNewWatcher_ZeroInterval(t *testing.T) {
	log, _ := logger.New("error", "json")
	w := NewWatcher("test.yaml", 0, log)
	if w.interval != 5*time.Second {
		t.Errorf("interval = %v, want 5s when zero passed", w.interval)
	}
}

// --- Watcher check with valid change and onChange callback ---

func TestWatcher_CheckWithChange(t *testing.T) {
	log, _ := logger.New("error", "json")
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	// Write initial config
	initial := []byte(`
global:
  log_level: info
admin:
  rest:
    auth:
      enabled: false
  grpc:
    auth:
      enabled: false
  mcp:
    auth:
      enabled: false
  dashboard:
    auth:
      enabled: false
pools:
  - name: test-pool
    listen:
      host: 127.0.0.1
      port: 5432
    body: postgresql
    mode: transaction
`)
	os.WriteFile(cfgPath, initial, 0644)

	w := NewWatcher(cfgPath, 100*time.Millisecond, log)

	// First check initializes hash
	if err := w.check(); err != nil {
		t.Fatalf("first check failed: %v", err)
	}

	// Set onChange callback
	onChangeCalled := false
	w.OnChange(func(cfg *Config) {
		onChangeCalled = true
	})

	// Modify config
	updated := []byte(`
global:
  log_level: debug
admin:
  rest:
    auth:
      enabled: false
  grpc:
    auth:
      enabled: false
  mcp:
    auth:
      enabled: false
  dashboard:
    auth:
      enabled: false
pools:
  - name: test-pool
    listen:
      host: 127.0.0.1
      port: 5432
    body: postgresql
    mode: transaction
`)
	os.WriteFile(cfgPath, updated, 0644)

	if err := w.check(); err != nil {
		t.Fatalf("check with change failed: %v", err)
	}

	if !onChangeCalled {
		t.Error("onChange callback should have been called")
	}
}

// --- Watcher computeHash error path ---

func TestWatcher_ComputeHash_Nonexistent(t *testing.T) {
	log, _ := logger.New("error", "json")
	tmpDir := t.TempDir()
	nonexistentPath := filepath.Join(tmpDir, "nonexistent", "dir", "config.yaml")
	w := NewWatcher(nonexistentPath, time.Second, log)

	_, err := w.computeHash()
	if err == nil {
		t.Error("computeHash should fail for nonexistent file")
	}
}

// --- Watcher already running ---

func TestWatcher_AlreadyRunning(t *testing.T) {
	log, _ := logger.New("error", "json")
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(cfgPath, []byte("# test"), 0644)

	w := NewWatcher(cfgPath, 100*time.Millisecond, log)
	if err := w.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	if err := w.Start(); err == nil {
		t.Error("Second Start should fail")
	}
}

// --- parseKeyValue with list item prefix ---

func TestParseYAML_ListItemWithKeyValue(t *testing.T) {
	content := `
global:
  log_level: info
admin:
  rest:
    auth:
      enabled: false
  grpc:
    auth:
      enabled: false
  mcp:
    auth:
      enabled: false
  dashboard:
    auth:
      enabled: false
pools:
  - name: pool1
    body: postgresql
    mode: session
    limits:
      max_client_connections: 100
      max_server_connections: 50
`
	cfg, err := parseYAML(content)
	if err != nil {
		t.Fatalf("parseYAML failed: %v", err)
	}
	if len(cfg.Pools) != 1 {
		t.Fatalf("expected 1 pool, got %d", len(cfg.Pools))
	}
	if cfg.Pools[0].Name != "pool1" {
		t.Errorf("Pool.Name = %q, want pool1", cfg.Pools[0].Name)
	}
	if cfg.Pools[0].Limits.MaxClientConnections != 100 {
		t.Errorf("MaxClientConnections = %d, want 100", cfg.Pools[0].Limits.MaxClientConnections)
	}
}

// --- parseKeyValue section depth growth ---

func TestParseYAML_DeepNesting(t *testing.T) {
	content := `
global:
  log_level: info
admin:
  rest:
    listen: "127.0.0.1:8080"
    auth:
      enabled: false
  grpc:
    listen: "127.0.0.1:9090"
    auth:
      enabled: false
  mcp:
    listen: "127.0.0.1:8081"
    auth:
      enabled: false
  dashboard:
    enabled: false
    auth:
      enabled: false
cluster:
  enabled: true
  node_id: "node-1"
  raft:
    listen: "0.0.0.0:7000"
    election_timeout: "1s"
  gossip:
    listen: "0.0.0.0:7001"
auth:
  mode: passthrough
pools:
  - name: deep-pool
    body: postgresql
    mode: transaction
    backend:
      hosts:
        - host: localhost
          port: 5432
          role: primary
`
	cfg, err := parseYAML(content)
	if err != nil {
		t.Fatalf("parseYAML failed: %v", err)
	}
	if cfg.Cluster.NodeID != "node-1" {
		t.Errorf("Cluster.NodeID = %q, want node-1", cfg.Cluster.NodeID)
	}
}

// --- ReloadManager Apply without callback ---

func TestReloadManager_ApplyNoCallback(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := DefaultConfig()
	cfg.Admin.REST.Auth.Enabled = false
	cfg.Admin.GRPC.Auth.Enabled = false
	cfg.Admin.MCP.Auth.Enabled = false
	cfg.Admin.Dashboard.Auth.Enabled = false
	cfg.Admin.Dashboard.Enabled = false

	m := NewReloadManager(cfg, log)
	// Don't set OnApply

	newCfg := DefaultConfig()
	newCfg.Global.LogLevel = "debug"

	err := m.Apply(newCfg)
	if err != nil {
		t.Fatalf("Apply without callback should succeed: %v", err)
	}
	if m.Get().Global.LogLevel != "debug" {
		t.Error("Config should be updated even without callback")
	}
}

// --- BackupConfig creates backup ---

func TestBackupConfig_VerifyContent(t *testing.T) {
	cfg := &Config{
		Global: GlobalConfig{LogLevel: "debug", LogFormat: "text"},
		Pools:  []PoolConfig{{Name: "testpool", Body: "mysql", Mode: "session"}},
	}

	tmpDir := t.TempDir()
	err := BackupConfig(cfg, tmpDir)
	if err != nil {
		t.Fatalf("BackupConfig failed: %v", err)
	}

	entries, _ := os.ReadDir(tmpDir)
	if len(entries) != 1 {
		t.Fatalf("Expected 1 backup file, got %d", len(entries))
	}

	data, _ := os.ReadFile(filepath.Join(tmpDir, entries[0].Name()))
	content := string(data)
	if !strings.Contains(content, "debug") {
		t.Error("Backup should contain log level")
	}
	if !strings.Contains(content, "testpool") {
		t.Error("Backup should contain pool name")
	}
}
