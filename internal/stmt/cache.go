package stmt

import (
	"container/list"
	"fmt"
	"sync"
	"sync/atomic"
)

// Statement represents a prepared statement.
type Statement struct {
	Name       string
	SQL        string
	ParamTypes []int32
	NumParams  int
}

// Cache stores prepared statement metadata.
type Cache struct {
	mu          sync.RWMutex
	statements  map[string]*Statement // name -> Statement
	sqlToName   map[string]string     // normalized SQL -> statement name
	lruList     *list.List
	maxSize     int
	currentSize int
}

// NewCache creates a new statement cache.
func NewCache(maxSize int) *Cache {
	return &Cache{
		statements: make(map[string]*Statement),
		sqlToName:  make(map[string]string),
		lruList:    list.New(),
		maxSize:    maxSize,
	}
}

// Get retrieves a statement by name.
func (c *Cache) Get(name string) *Statement {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stmt, exists := c.statements[name]
	if !exists {
		return nil
	}

	return stmt
}

// GetBySQL retrieves a statement by SQL (if already prepared).
func (c *Cache) GetBySQL(sql string) *Statement {
	c.mu.RLock()
	defer c.mu.RUnlock()

	name, exists := c.sqlToName[sql]
	if !exists {
		return nil
	}

	return c.statements[name]
}

// Put adds a statement to the cache.
func (c *Cache) Put(stmt *Statement) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if we need to evict
	for c.currentSize >= c.maxSize && c.lruList.Len() > 0 {
		c.evictLRU()
	}

	c.statements[stmt.Name] = stmt
	c.sqlToName[stmt.SQL] = stmt.Name
	c.lruList.PushFront(stmt.Name)
	c.currentSize++

	return nil
}

// Remove removes a statement from the cache.
func (c *Cache) Remove(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if stmt, exists := c.statements[name]; exists {
		delete(c.statements, name)
		delete(c.sqlToName, stmt.SQL)
		c.currentSize--
	}
}

// evictLRU removes the least recently used statement.
func (c *Cache) evictLRU() {
	elem := c.lruList.Back()
	if elem == nil {
		return
	}

	name := elem.Value.(string)
	c.lruList.Remove(elem)

	if stmt, exists := c.statements[name]; exists {
		delete(c.statements, name)
		delete(c.sqlToName, stmt.SQL)
		c.currentSize--
	}
}

// Size returns the number of cached statements.
func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentSize
}

// Clear removes all statements from the cache.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.statements = make(map[string]*Statement)
	c.sqlToName = make(map[string]string)
	c.lruList.Init()
	c.currentSize = 0
}

// ConnTracker tracks prepared statements per server connection.
type ConnTracker struct {
	mu         sync.RWMutex
	statements map[string]bool // statement name -> prepared on this connection
	connID     uint64
}

// NewConnTracker creates a new connection tracker.
func NewConnTracker(connID uint64) *ConnTracker {
	return &ConnTracker{
		statements: make(map[string]bool),
		connID:     connID,
	}
}

// IsPrepared returns true if the statement is prepared on this connection.
func (t *ConnTracker) IsPrepared(name string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.statements[name]
}

// MarkPrepared marks a statement as prepared on this connection.
func (t *ConnTracker) MarkPrepared(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.statements[name] = true
}

// UnmarkPrepared removes a statement from this connection.
func (t *ConnTracker) UnmarkPrepared(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.statements, name)
}

// ListPrepared returns all prepared statement names on this connection.
func (t *ConnTracker) ListPrepared() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	names := make([]string, 0, len(t.statements))
	for name := range t.statements {
		names = append(names, name)
	}
	return names
}

// Clear removes all prepared statements.
func (t *ConnTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.statements = make(map[string]bool)
}

// Manager manages prepared statements across the system.
type Manager struct {
	mu          sync.RWMutex
	globalCache *Cache
	trackers    map[uint64]*ConnTracker // connID -> tracker
	stmtIDGen   atomic.Uint64
}

// NewManager creates a new prepared statement manager.
func NewManager(cacheSize int) *Manager {
	return &Manager{
		globalCache: NewCache(cacheSize),
		trackers:    make(map[uint64]*ConnTracker),
	}
}

// GetCache returns the global statement cache.
func (m *Manager) GetCache() *Cache {
	return m.globalCache
}

// GetOrCreateTracker returns or creates a connection tracker.
func (m *Manager) GetOrCreateTracker(connID uint64) *ConnTracker {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tracker, exists := m.trackers[connID]; exists {
		return tracker
	}

	tracker := NewConnTracker(connID)
	m.trackers[connID] = tracker
	return tracker
}

// RemoveTracker removes a connection tracker.
func (m *Manager) RemoveTracker(connID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.trackers, connID)
}

// GenerateName generates a unique statement name.
func (m *Manager) GenerateName(prefix string) string {
	id := m.stmtIDGen.Add(1)
	return fmt.Sprintf("%s_%d", prefix, id)
}

// IsPreparedOnConn checks if a statement is prepared on a specific connection.
func (m *Manager) IsPreparedOnConn(connID uint64, stmtName string) bool {
	tracker := m.GetOrCreateTracker(connID)
	return tracker.IsPrepared(stmtName)
}

// MarkPreparedOnConn marks a statement as prepared on a connection.
func (m *Manager) MarkPreparedOnConn(connID uint64, stmtName string) {
	tracker := m.GetOrCreateTracker(connID)
	tracker.MarkPrepared(stmtName)
}

// GetMissingStatements returns statements that need to be prepared on a connection.
func (m *Manager) GetMissingStatements(connID uint64, stmtNames []string) []string {
	tracker := m.GetOrCreateTracker(connID)

	missing := make([]string, 0)
	for _, name := range stmtNames {
		if !tracker.IsPrepared(name) {
			missing = append(missing, name)
		}
	}

	return missing
}

// Remapper handles client statement ID to server statement ID mapping.
type Remapper struct {
	mu             sync.RWMutex
	clientToServer map[string]uint32 // client name -> server ID
	serverToClient map[uint32]string // server ID -> client name
}

// NewRemapper creates a new statement ID remapper.
func NewRemapper() *Remapper {
	return &Remapper{
		clientToServer: make(map[string]uint32),
		serverToClient: make(map[uint32]string),
	}
}

// Map maps a client statement name to a server statement ID.
func (r *Remapper) Map(clientName string, serverID uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.clientToServer[clientName] = serverID
	r.serverToClient[serverID] = clientName
}

// GetServerID returns the server ID for a client statement name.
func (r *Remapper) GetServerID(clientName string) (uint32, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	id, exists := r.clientToServer[clientName]
	return id, exists
}

// GetClientName returns the client name for a server statement ID.
func (r *Remapper) GetClientName(serverID uint32) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	name, exists := r.serverToClient[serverID]
	return name, exists
}

// Remove removes a mapping.
func (r *Remapper) Remove(clientName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if serverID, exists := r.clientToServer[clientName]; exists {
		delete(r.clientToServer, clientName)
		delete(r.serverToClient, serverID)
	}
}

// Clear removes all mappings.
func (r *Remapper) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.clientToServer = make(map[string]uint32)
	r.serverToClient = make(map[uint32]string)
}

// TransparentRepreparer handles transparent re-preparation of statements.
type TransparentRepreparer struct {
	manager  *Manager
	cache    *Cache
	remapper *Remapper
}

// NewTransparentRepreparer creates a new transparent re-preparer.
func NewTransparentRepreparer(manager *Manager) *TransparentRepreparer {
	return &TransparentRepreparer{
		manager:  manager,
		cache:    manager.GetCache(),
		remapper: NewRemapper(),
	}
}

// PrepareIfNeeded prepares a statement on a connection if not already prepared.
func (t *TransparentRepreparer) PrepareIfNeeded(connID uint64, stmtName string) (*Statement, bool, error) {
	// Check if already prepared on this connection
	if t.manager.IsPreparedOnConn(connID, stmtName) {
		return nil, false, nil
	}

	// Get statement from cache
	stmt := t.cache.Get(stmtName)
	if stmt == nil {
		return nil, false, fmt.Errorf("statement %s not found in cache", stmtName)
	}

	// Mark as prepared on this connection
	t.manager.MarkPreparedOnConn(connID, stmtName)

	return stmt, true, nil
}

// ExecuteOnAny prepares statement on assigned server if needed, then executes.
func (t *TransparentRepreparer) ExecuteOnAny(connID uint64, stmtName string, executeFn func() error) error {
	// Ensure prepared
	_, needed, err := t.PrepareIfNeeded(connID, stmtName)
	if err != nil {
		return err
	}

	if needed {
		// Statement was just prepared (or needs to be prepared)
		// The executeFn should handle the actual prepare + execute
	}

	return executeFn()
}
