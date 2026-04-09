package pool

import (
	"sync"
	"sync/atomic"
	"time"
)

// ConnectionInfo holds information about an active connection.
type ConnectionInfo struct {
	ID           uint64
	ClientAddr   string
	BackendAddr  string
	Username     string
	Database     string
	Pool         string
	StartTime    time.Time
	LastActivity time.Time
	QueryCount   int64
	Active       bool
}

// ConnectionTracker tracks all active connections.
type ConnectionTracker struct {
	mu          sync.RWMutex
	connections map[uint64]*ConnectionInfo
	nextID      atomic.Uint64
}

// NewConnectionTracker creates a new connection tracker.
func NewConnectionTracker() *ConnectionTracker {
	return &ConnectionTracker{
		connections: make(map[uint64]*ConnectionInfo),
	}
}

// Register registers a new connection.
func (ct *ConnectionTracker) Register(clientAddr, username, database, pool string) *ConnectionInfo {
	info := &ConnectionInfo{
		ID:           ct.nextID.Add(1),
		ClientAddr:   clientAddr,
		Username:     username,
		Database:     database,
		Pool:         pool,
		StartTime:    time.Now(),
		LastActivity: time.Now(),
		Active:       true,
	}

	ct.mu.Lock()
	ct.connections[info.ID] = info
	ct.mu.Unlock()

	return info
}

// Unregister removes a connection.
func (ct *ConnectionTracker) Unregister(id uint64) {
	ct.mu.Lock()
	delete(ct.connections, id)
	ct.mu.Unlock()
}

// UpdateActivity updates the last activity time for a connection.
func (ct *ConnectionTracker) UpdateActivity(id uint64) {
	ct.mu.RLock()
	info, exists := ct.connections[id]
	ct.mu.RUnlock()

	if exists {
		info.LastActivity = time.Now()
	}
}

// SetBackend sets the backend address for a connection.
func (ct *ConnectionTracker) SetBackend(id uint64, backendAddr string) {
	ct.mu.RLock()
	info, exists := ct.connections[id]
	ct.mu.RUnlock()

	if exists {
		info.BackendAddr = backendAddr
	}
}

// IncrementQueryCount increments the query counter for a connection.
func (ct *ConnectionTracker) IncrementQueryCount(id uint64) {
	ct.mu.RLock()
	info, exists := ct.connections[id]
	ct.mu.RUnlock()

	if exists {
		info.QueryCount++
		info.LastActivity = time.Now()
	}
}

// Get returns connection info by ID.
func (ct *ConnectionTracker) Get(id uint64) *ConnectionInfo {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	return ct.connections[id]
}

// List returns all active connections.
func (ct *ConnectionTracker) List() []*ConnectionInfo {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	list := make([]*ConnectionInfo, 0, len(ct.connections))
	for _, info := range ct.connections {
		list = append(list, info)
	}
	return list
}

// ListByPool returns connections for a specific pool.
func (ct *ConnectionTracker) ListByPool(pool string) []*ConnectionInfo {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	list := make([]*ConnectionInfo, 0)
	for _, info := range ct.connections {
		if info.Pool == pool {
			list = append(list, info)
		}
	}
	return list
}

// Count returns the total number of connections.
func (ct *ConnectionTracker) Count() int {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	return len(ct.connections)
}

// CountByPool returns the number of connections for a specific pool.
func (ct *ConnectionTracker) CountByPool(pool string) int {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	count := 0
	for _, info := range ct.connections {
		if info.Pool == pool {
			count++
		}
	}
	return count
}

// CleanupIdle removes connections that have been idle for longer than the specified duration.
func (ct *ConnectionTracker) CleanupIdle(maxIdle time.Duration) []uint64 {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	removed := make([]uint64, 0)
	cutoff := time.Now().Add(-maxIdle)

	for id, info := range ct.connections {
		if info.LastActivity.Before(cutoff) {
			delete(ct.connections, id)
			removed = append(removed, id)
		}
	}

	return removed
}

// ConnectionStats holds connection statistics.
type ConnectionStats struct {
	TotalConnections int
	ByPool           map[string]int
}

// Stats returns connection statistics.
func (ct *ConnectionTracker) Stats() ConnectionStats {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	stats := ConnectionStats{
		TotalConnections: len(ct.connections),
		ByPool:           make(map[string]int),
	}

	for _, info := range ct.connections {
		stats.ByPool[info.Pool]++
	}

	return stats
}
