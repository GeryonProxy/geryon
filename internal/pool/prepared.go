package pool

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// PreparedStatement represents a cached prepared statement.
type PreparedStatement struct {
	ID          string
	Query       string
	Hash        string
	CreatedAt   time.Time
	LastUsed    atomic.Value // time.Time
	UseCount    atomic.Int64
	ParamTypes  []int32
	ResultTypes []int32
}

// NewPreparedStatement creates a new prepared statement entry.
func NewPreparedStatement(id, query string, paramTypes []int32) *PreparedStatement {
	ps := &PreparedStatement{
		ID:         id,
		Query:      query,
		Hash:       hashQuery(query),
		CreatedAt:  time.Now(),
		ParamTypes: paramTypes,
	}
	ps.LastUsed.Store(time.Now())
	return ps
}

// UpdateLastUsed updates the last used timestamp.
func (ps *PreparedStatement) UpdateLastUsed() {
	ps.LastUsed.Store(time.Now())
	ps.UseCount.Add(1)
}

// PreparedStatementCache manages prepared statement caching across connections.
type PreparedStatementCache struct {
	mu         sync.RWMutex
	statements map[string]*PreparedStatement // hash -> statement
	byID       map[string]string             // id -> hash mapping
	maxSize    int
	ttl        time.Duration
	hitCount   atomic.Int64
	missCount  atomic.Int64
	addCount   atomic.Int64
	evictCount atomic.Int64
}

// NewPreparedStatementCache creates a new prepared statement cache.
func NewPreparedStatementCache(maxSize int, ttl time.Duration) *PreparedStatementCache {
	if maxSize <= 0 {
		maxSize = 1000
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}

	psc := &PreparedStatementCache{
		statements: make(map[string]*PreparedStatement),
		byID:       make(map[string]string),
		maxSize:    maxSize,
		ttl:        ttl,
	}

	// Start cleanup goroutine
	go psc.cleanupLoop()

	return psc
}

// Get retrieves a prepared statement by query hash.
func (psc *PreparedStatementCache) Get(query string) (*PreparedStatement, bool) {
	hash := hashQuery(query)

	psc.mu.RLock()
	stmt, exists := psc.statements[hash]
	psc.mu.RUnlock()

	if exists {
		stmt.UpdateLastUsed()
		psc.hitCount.Add(1)
		return stmt, true
	}

	psc.missCount.Add(1)
	return nil, false
}

// GetByID retrieves a prepared statement by its ID.
func (psc *PreparedStatementCache) GetByID(id string) (*PreparedStatement, bool) {
	psc.mu.RLock()
	hash, exists := psc.byID[id]
	if !exists {
		psc.mu.RUnlock()
		return nil, false
	}
	stmt, exists := psc.statements[hash]
	psc.mu.RUnlock()

	if exists {
		stmt.UpdateLastUsed()
		return stmt, true
	}
	return nil, false
}

// Add adds a prepared statement to the cache.
func (psc *PreparedStatementCache) Add(id, query string, paramTypes []int32) *PreparedStatement {
	hash := hashQuery(query)

	psc.mu.Lock()
	defer psc.mu.Unlock()

	// Check if already exists
	if stmt, exists := psc.statements[hash]; exists {
		// Update ID mapping
		delete(psc.byID, stmt.ID)
		psc.byID[id] = hash
		stmt.ID = id
		stmt.UpdateLastUsed()
		return stmt
	}

	// Evict oldest if at capacity
	if len(psc.statements) >= psc.maxSize {
		psc.evictOldest()
	}

	// Add new statement
	stmt := NewPreparedStatement(id, query, paramTypes)
	psc.statements[hash] = stmt
	psc.byID[id] = hash
	psc.addCount.Add(1)

	return stmt
}

// Remove removes a prepared statement from the cache.
func (psc *PreparedStatementCache) Remove(id string) {
	psc.mu.Lock()
	defer psc.mu.Unlock()

	hash, exists := psc.byID[id]
	if !exists {
		return
	}

	delete(psc.statements, hash)
	delete(psc.byID, id)
}

// evictOldest removes the least recently used statement.
func (psc *PreparedStatementCache) evictOldest() {
	var oldestHash string
	var oldestTime time.Time
	first := true

	for hash, stmt := range psc.statements {
		lastUsed := stmt.LastUsed.Load().(time.Time)
		if first || lastUsed.Before(oldestTime) {
			oldestHash = hash
			oldestTime = lastUsed
			first = false
		}
	}

	if oldestHash != "" {
		if stmt, exists := psc.statements[oldestHash]; exists {
			delete(psc.byID, stmt.ID)
		}
		delete(psc.statements, oldestHash)
		psc.evictCount.Add(1)
	}
}

// cleanupLoop periodically removes expired statements.
func (psc *PreparedStatementCache) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		psc.cleanup()
	}
}

// cleanup removes expired prepared statements.
func (psc *PreparedStatementCache) cleanup() {
	now := time.Now()
	psc.mu.Lock()
	defer psc.mu.Unlock()

	for hash, stmt := range psc.statements {
		lastUsed := stmt.LastUsed.Load().(time.Time)
		if now.Sub(lastUsed) > psc.ttl {
			delete(psc.byID, stmt.ID)
			delete(psc.statements, hash)
			psc.evictCount.Add(1)
		}
	}
}

// Stats returns cache statistics.
func (psc *PreparedStatementCache) Stats() PreparedStatementCacheStats {
	psc.mu.RLock()
	size := len(psc.statements)
	psc.mu.RUnlock()

	hits := psc.hitCount.Load()
	misses := psc.missCount.Load()
	total := hits + misses
	hitRate := float64(0)
	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100
	}

	return PreparedStatementCacheStats{
		Size:       size,
		MaxSize:    psc.maxSize,
		Hits:       hits,
		Misses:     misses,
		HitRate:    hitRate,
		Added:      psc.addCount.Load(),
		Evicted:    psc.evictCount.Load(),
	}
}

// PreparedStatementCacheStats contains cache statistics.
type PreparedStatementCacheStats struct {
	Size    int     `json:"size"`
	MaxSize int     `json:"max_size"`
	Hits    int64   `json:"hits"`
	Misses  int64   `json:"misses"`
	HitRate float64 `json:"hit_rate"`
	Added   int64   `json:"added"`
	Evicted int64   `json:"evicted"`
}

// SessionPreparedStatements tracks prepared statements for a session.
type SessionPreparedStatements struct {
	mu         sync.RWMutex
	cache      *PreparedStatementCache
	statements map[string]string // statement name -> query hash
	serverIDs  map[string]string // our name -> server-assigned name
}

// NewSessionPreparedStatements creates a new session prepared statement tracker.
func NewSessionPreparedStatements(cache *PreparedStatementCache) *SessionPreparedStatements {
	return &SessionPreparedStatements{
		cache:      cache,
		statements: make(map[string]string),
		serverIDs:  make(map[string]string),
	}
}

// Register registers a prepared statement for this session.
func (sps *SessionPreparedStatements) Register(name, query string, paramTypes []int32) *PreparedStatement {
	sps.mu.Lock()
	defer sps.mu.Unlock()

	// Add to cache
	stmt := sps.cache.Add(name, query, paramTypes)
	sps.statements[name] = stmt.Hash

	return stmt
}

// Get retrieves a prepared statement by name.
func (sps *SessionPreparedStatements) Get(name string) (*PreparedStatement, bool) {
	sps.mu.RLock()
	hash, exists := sps.statements[name]
	sps.mu.RUnlock()

	if !exists {
		return nil, false
	}

	return sps.cache.GetByHash(hash)
}

// GetServerName returns the server-assigned name for a statement.
func (sps *SessionPreparedStatements) GetServerName(name string) (string, bool) {
	sps.mu.RLock()
	defer sps.mu.RUnlock()

	serverName, exists := sps.serverIDs[name]
	return serverName, exists
}

// SetServerName sets the server-assigned name for a statement.
func (sps *SessionPreparedStatements) SetServerName(name, serverName string) {
	sps.mu.Lock()
	defer sps.mu.Unlock()

	sps.serverIDs[name] = serverName
}

// Add adds a prepared statement with auto-generated name (simplified tracking).
func (sps *SessionPreparedStatements) Add(query string) string {
	name := GenerateStmtID()
	sps.Register(name, query, nil)
	return name
}

// Close removes all session-specific mappings.
func (sps *SessionPreparedStatements) Close() {
	sps.mu.Lock()
	defer sps.mu.Unlock()

	sps.statements = make(map[string]string)
	sps.serverIDs = make(map[string]string)
}

// GetByHash retrieves a statement by hash (internal helper).
func (psc *PreparedStatementCache) GetByHash(hash string) (*PreparedStatement, bool) {
	psc.mu.RLock()
	defer psc.mu.RUnlock()

	stmt, exists := psc.statements[hash]
	return stmt, exists
}

// hashQuery creates a hash of the query string.
func hashQuery(query string) string {
	h := sha256.Sum256([]byte(query))
	return hex.EncodeToString(h[:8]) // Use first 8 bytes for shorter hash
}

// GenerateStmtID generates a unique statement ID.
var stmtIDCounter atomic.Uint64

func GenerateStmtID() string {
	return fmt.Sprintf("geryon_%d", stmtIDCounter.Add(1))
}
