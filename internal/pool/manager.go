package pool

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/protocol/common"
	"github.com/GeryonProxy/geryon/internal/protocol/mssql"
	"github.com/GeryonProxy/geryon/internal/protocol/mysql"
	"github.com/GeryonProxy/geryon/internal/protocol/postgresql"
)

// Estimated bytes per server connection (buffer + overhead)
const connMemoryEstimate = 32 * 1024 // 32KB per connection

// Manager manages all connection pools.
type Manager struct {
	mu                sync.RWMutex
	pools             map[string]*Pool
	logger            *logger.Logger
	globalMemoryLimit atomic.Int64
	globalMemoryUsed  atomic.Int64
}

// NewManager creates a new pool manager.
func NewManager(log *logger.Logger) *Manager {
	return &Manager{
		pools:  make(map[string]*Pool),
		logger: log,
	}
}

// SetGlobalMaxMemory sets the global memory limit for all server connections.
// The limit is specified as a string like "4GB", "100MB", etc.
func (m *Manager) SetGlobalMaxMemory(mem string) {
	if mem == "" {
		m.globalMemoryLimit.Store(0)
		m.logger.Info("Global memory limit disabled")
		return
	}
	bytes, err := parseMemory(mem)
	if err != nil {
		m.logger.Warn("Invalid global.max_memory, ignoring", "value", mem, "error", err)
		return
	}
	m.globalMemoryLimit.Store(bytes)
	maxConns := bytes / connMemoryEstimate
	m.logger.Info("Global memory limit set", "bytes", bytes, "max_connections_approx", maxConns)
}

// TryAlloc attempts to allocate memory for a new server connection.
// Returns true if allocation succeeded, false if the global memory limit would be exceeded.
func (m *Manager) TryAlloc() bool {
	limit := m.globalMemoryLimit.Load()
	if limit == 0 {
		return true // No limit set
	}
	for {
		used := m.globalMemoryUsed.Load()
		if used+connMemoryEstimate > limit {
			return false
		}
		if m.globalMemoryUsed.CompareAndSwap(used, used+connMemoryEstimate) {
			return true
		}
	}
}

// Free releases memory when a server connection is closed.
func (m *Manager) Free() {
	for {
		used := m.globalMemoryUsed.Load()
		if used < connMemoryEstimate {
			return // Already at zero or negative (shouldn't happen)
		}
		if m.globalMemoryUsed.CompareAndSwap(used, used-connMemoryEstimate) {
			return
		}
	}
}

// GlobalMemoryUsage returns the current global memory usage in bytes.
func (m *Manager) GlobalMemoryUsage() int64 {
	return m.globalMemoryUsed.Load()
}

// GlobalMemoryLimit returns the global memory limit in bytes (0 if disabled).
func (m *Manager) GlobalMemoryLimit() int64 {
	return m.globalMemoryLimit.Load()
}

// CreatePool creates a new pool from configuration.
func (m *Manager) CreatePool(cfg *config.PoolConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.pools[cfg.Name]; exists {
		return fmt.Errorf("pool %s already exists", cfg.Name)
	}

	// Select codec based on body type
	var codec common.Codec
	switch cfg.Body {
	case "postgresql":
		codec = postgresql.NewCodec()
	case "mysql":
		codec = mysql.NewCodec()
	case "mssql":
		codec = mssql.NewCodec()
	default:
		return fmt.Errorf("unknown body type: %s", cfg.Body)
	}

	pool, err := NewPool(cfg, codec, m.logger, m)
	if err != nil {
		return fmt.Errorf("failed to create pool %s: %w", cfg.Name, err)
	}

	m.pools[cfg.Name] = pool
	m.logger.Info("Created pool", "name", cfg.Name, "mode", cfg.Mode, "body", cfg.Body)

	return nil
}

// GetPool returns a pool by name.
func (m *Manager) GetPool(name string) *Pool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pools[name]
}

// ListPools returns all pools.
func (m *Manager) ListPools() []*Pool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pools := make([]*Pool, 0, len(m.pools))
	for _, p := range m.pools {
		pools = append(pools, p)
	}
	return pools
}

// RemovePool removes a pool.
func (m *Manager) RemovePool(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, exists := m.pools[name]
	if !exists {
		return fmt.Errorf("pool %s not found", name)
	}

	if err := pool.Close(); err != nil {
		return fmt.Errorf("failed to close pool %s: %w", name, err)
	}

	delete(m.pools, name)
	m.logger.Info("Removed pool", "name", name)

	return nil
}

// UpdatePoolConfig updates pool configuration dynamically.
// Only safe-to-change fields can be updated without restart.
func (m *Manager) UpdatePoolConfig(name string, cfg *config.PoolConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, exists := m.pools[name]
	if !exists {
		return fmt.Errorf("pool %s not found", name)
	}

	// Update the pool configuration
	if err := pool.UpdateConfig(cfg); err != nil {
		return fmt.Errorf("failed to update pool %s: %w", name, err)
	}

	m.logger.Info("Updated pool configuration", "name", name)
	return nil
}

// Close closes all pools.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, pool := range m.pools {
		if err := pool.Close(); err != nil {
			m.logger.Error("Failed to close pool", "name", name, "error", err)
		}
	}

	m.pools = make(map[string]*Pool)

	return nil
}

// parseMemory parses a memory string like "4GB", "100MB", "1TB" into bytes.
func parseMemory(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	// Split numeric value and unit
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("invalid memory value: %s", s)
	}

	valueStr := s[:i]
	unit := strings.TrimSpace(s[i:])

	value, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory value: %s", s)
	}

	// Apply unit multiplier
	switch strings.ToUpper(unit) {
	case "", "B":
		return value, nil
	case "KB", "K":
		return value * 1024, nil
	case "MB", "M":
		return value * 1024 * 1024, nil
	case "GB", "G":
		return value * 1024 * 1024 * 1024, nil
	case "TB", "T":
		return value * 1024 * 1024 * 1024 * 1024, nil
	default:
		return 0, fmt.Errorf("unknown memory unit: %s", unit)
	}
}
