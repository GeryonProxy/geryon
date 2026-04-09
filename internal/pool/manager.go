package pool

import (
	"fmt"
	"sync"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/protocol/common"
	"github.com/GeryonProxy/geryon/internal/protocol/mssql"
	"github.com/GeryonProxy/geryon/internal/protocol/mysql"
	"github.com/GeryonProxy/geryon/internal/protocol/postgresql"
)

// Manager manages all connection pools.
type Manager struct {
	mu     sync.RWMutex
	pools  map[string]*Pool
	logger *logger.Logger
}

// NewManager creates a new pool manager.
func NewManager(log *logger.Logger) *Manager {
	return &Manager{
		pools:  make(map[string]*Pool),
		logger: log,
	}
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

	pool, err := NewPool(cfg, codec, m.logger)
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
