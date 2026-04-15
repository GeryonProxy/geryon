package config

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

// Watcher monitors configuration files for changes.
type Watcher struct {
	path     string
	interval time.Duration
	log      *logger.Logger

	mu       sync.RWMutex
	lastHash []byte
	onChange func(*Config)
	stopCh   chan struct{}
	running  bool
}

// NewWatcher creates a new configuration watcher.
func NewWatcher(path string, interval time.Duration, log *logger.Logger) *Watcher {
	if interval == 0 {
		interval = 5 * time.Second
	}

	return &Watcher{
		path:     filepath.Clean(path),
		interval: interval,
		log:      log,
		stopCh:   make(chan struct{}),
	}
}

// OnChange sets the callback function for configuration changes.
func (w *Watcher) OnChange(fn func(*Config)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onChange = fn
}

// Start starts watching the configuration file.
func (w *Watcher) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running {
		return fmt.Errorf("watcher already running")
	}

	// Get initial file hash
	hash, err := w.computeHash()
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	w.lastHash = hash
	w.running = true

	go w.watch()

	w.log.Info("Config watcher started", "path", w.path, "interval", w.interval)
	return nil
}

// Stop stops watching the configuration file.
func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.running {
		return
	}

	close(w.stopCh)
	w.running = false

	w.log.Info("Config watcher stopped")
}

// watch is the main watch loop.
func (w *Watcher) watch() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			if err := w.check(); err != nil {
				w.log.Error("Config watch error", "error", err)
			}
		}
	}
}

// check checks if the configuration file has changed.
func (w *Watcher) check() error {
	hash, err := w.computeHash()
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	w.mu.RLock()
	lastHash := w.lastHash
	onChange := w.onChange
	w.mu.RUnlock()

	// Check if file content has changed
	if string(hash) == string(lastHash) {
		return nil
	}

	w.log.Info("Configuration file changed, reloading")

	// Reload configuration
	cfg, err := Load(w.path)
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	// Validate new configuration
	if err := Validate(cfg); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Update last seen
	w.mu.Lock()
	w.lastHash = hash
	w.mu.Unlock()

	// Trigger callback
	if onChange != nil {
		onChange(cfg)
	}

	return nil
}

// computeHash returns the SHA-256 hash of the config file content.
func (w *Watcher) computeHash() ([]byte, error) {
	data, err := os.ReadFile(w.path)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(data)
	return h[:], nil
}

// IsRunning returns true if the watcher is running.
func (w *Watcher) IsRunning() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.running
}

// ReloadManager manages configuration reloads with graceful transitions.
type ReloadManager struct {
	mu      sync.RWMutex
	current *Config
	applyFn func(*Config) error
	log     *logger.Logger
}

// NewReloadManager creates a new reload manager.
func NewReloadManager(initial *Config, log *logger.Logger) *ReloadManager {
	return &ReloadManager{
		current: initial,
		log:     log,
	}
}

// OnApply sets the function to apply new configuration.
func (r *ReloadManager) OnApply(fn func(*Config) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applyFn = fn
}

// Get returns the current configuration.
func (r *ReloadManager) Get() *Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

// Reload reloads the configuration from a file.
func (r *ReloadManager) Reload(path string) error {
	cfg, err := Load(path)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := Validate(cfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	return r.Apply(cfg)
}

// Apply applies a new configuration.
func (r *ReloadManager) Apply(cfg *Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.applyFn != nil {
		if err := r.applyFn(cfg); err != nil {
			return fmt.Errorf("failed to apply config: %w", err)
		}
	}

	r.current = cfg
	r.log.Info("Configuration reloaded successfully")

	return nil
}

// HotReload performs a hot reload of configuration.
func HotReload(ctx context.Context, path string, current *Config, applyFn func(*Config) error, log *logger.Logger) (*Config, error) {
	log.Info("Starting hot reload", "path", path)

	// Load new configuration
	newCfg, err := Load(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Validate
	if err := Validate(newCfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Compare with current config
	changes := CompareConfigs(current, newCfg)
	if len(changes) == 0 {
		log.Info("No configuration changes detected")
		return current, nil
	}

	log.Info("Configuration changes detected", "changes", len(changes))
	for _, change := range changes {
		log.Info("  - " + change)
	}

	// Apply new configuration
	if err := applyFn(newCfg); err != nil {
		return nil, fmt.Errorf("failed to apply config: %w", err)
	}

	log.Info("Hot reload completed successfully")
	return newCfg, nil
}

// CompareConfigs compares two configurations and returns the differences.
func CompareConfigs(old, new *Config) []string {
	var changes []string

	// Compare global settings
	if old.Global.LogLevel != new.Global.LogLevel {
		changes = append(changes, fmt.Sprintf("global.log_level: %s -> %s", old.Global.LogLevel, new.Global.LogLevel))
	}

	if old.Global.LogFormat != new.Global.LogFormat {
		changes = append(changes, fmt.Sprintf("global.log_format: %s -> %s", old.Global.LogFormat, new.Global.LogFormat))
	}

	// Compare pools
	oldPools := make(map[string]PoolConfig)
	for _, p := range old.Pools {
		oldPools[p.Name] = p
	}

	newPools := make(map[string]PoolConfig)
	for _, p := range new.Pools {
		newPools[p.Name] = p
	}

	// Check for added/removed pools
	for name := range newPools {
		if _, exists := oldPools[name]; !exists {
			changes = append(changes, fmt.Sprintf("pool.%s: added", name))
		}
	}

	for name := range oldPools {
		if _, exists := newPools[name]; !exists {
			changes = append(changes, fmt.Sprintf("pool.%s: removed", name))
		}
	}

	// Check for modified pools
	for name, newPool := range newPools {
		if oldPool, exists := oldPools[name]; exists {
			if oldPool.Mode != newPool.Mode {
				changes = append(changes, fmt.Sprintf("pool.%s.mode: %s -> %s", name, oldPool.Mode, newPool.Mode))
			}
			if oldPool.Limits.MaxClientConnections != newPool.Limits.MaxClientConnections {
				changes = append(changes, fmt.Sprintf("pool.%s.limits.max_client_connections: %d -> %d",
					name, oldPool.Limits.MaxClientConnections, newPool.Limits.MaxClientConnections))
			}
			if oldPool.Limits.MaxServerConnections != newPool.Limits.MaxServerConnections {
				changes = append(changes, fmt.Sprintf("pool.%s.limits.max_server_connections: %d -> %d",
					name, oldPool.Limits.MaxServerConnections, newPool.Limits.MaxServerConnections))
			}
		}
	}

	// Compare admin settings
	if old.Admin.REST.Listen != new.Admin.REST.Listen {
		changes = append(changes, fmt.Sprintf("admin.rest.listen: %s -> %s", old.Admin.REST.Listen, new.Admin.REST.Listen))
	}

	return changes
}

// IsSafeReload checks if a configuration change can be safely reloaded.
func IsSafeReload(old, new *Config) (bool, []string) {
	var unsafe []string

	// Check for unsafe changes
	for _, newPool := range new.Pools {
		for _, oldPool := range old.Pools {
			if oldPool.Name == newPool.Name {
				// Changing port requires restart
				if oldPool.Listen.Port != newPool.Listen.Port {
					unsafe = append(unsafe, fmt.Sprintf("pool.%s: port change (%d -> %d) requires restart",
						newPool.Name, oldPool.Listen.Port, newPool.Listen.Port))
				}

				// Changing body type requires restart
				if oldPool.Body != newPool.Body {
					unsafe = append(unsafe, fmt.Sprintf("pool.%s: body change (%s -> %s) requires restart",
						newPool.Name, oldPool.Body, newPool.Body))
				}
			}
		}
	}

	// Check admin port changes
	if old.Admin.REST.Listen != new.Admin.REST.Listen {
		unsafe = append(unsafe, fmt.Sprintf("admin.rest.listen: port change requires restart"))
	}

	if old.Admin.MCP.Listen != new.Admin.MCP.Listen {
		unsafe = append(unsafe, fmt.Sprintf("admin.mcp.listen: port change requires restart"))
	}

	if old.Admin.GRPC.Listen != new.Admin.GRPC.Listen {
		unsafe = append(unsafe, fmt.Sprintf("admin.grpc.listen: port change requires restart"))
	}

	return len(unsafe) == 0, unsafe
}

// BackupConfig creates a backup of the current configuration.
func BackupConfig(cfg *Config, backupDir string) error {
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("geryon-backup-%s.yaml", timestamp))

	// Generate config content (simplified)
	content := GenerateConfigContent(cfg)

	if err := os.WriteFile(backupPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write backup: %w", err)
	}

	return nil
}

// GenerateConfigContent generates YAML content from a Config.
func GenerateConfigContent(cfg *Config) string {
	// This is a simplified version
	content := "# Geryon Configuration\n\n"
	content += fmt.Sprintf("global:\n  log_level: %s\n  log_format: %s\n\n", cfg.Global.LogLevel, cfg.Global.LogFormat)

	content += "pools:\n"
	for _, pool := range cfg.Pools {
		content += fmt.Sprintf("  - name: %s\n    body: %s\n    mode: %s\n", pool.Name, pool.Body, pool.Mode)
	}

	return content
}
