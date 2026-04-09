package config

import (
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
		Global: GlobalConfig{LogLevel: "info", LogFormat: "json" },
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
