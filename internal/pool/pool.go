package pool

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

// PoolMode defines the connection pooling strategy.
type PoolMode uint8

const (
	ModeSession PoolMode = iota
	ModeTransaction
	ModeStatement
)

// String returns the string representation of the pool mode.
func (m PoolMode) String() string {
	switch m {
	case ModeSession:
		return "session"
	case ModeTransaction:
		return "transaction"
	case ModeStatement:
		return "statement"
	default:
		return "unknown"
	}
}

// ParsePoolMode parses a pool mode string.
func ParsePoolMode(s string) (PoolMode, error) {
	switch s {
	case "session":
		return ModeSession, nil
	case "transaction":
		return ModeTransaction, nil
	case "statement":
		return ModeStatement, nil
	default:
		return ModeTransaction, fmt.Errorf("invalid pool mode: %s", s)
	}
}

// Backend represents a backend database server.
type Backend struct {
	Host     string
	Port     int
	Role     string // primary or replica
	Weight   int
	Database string
	Healthy  atomic.Bool
}

// Address returns the backend address.
func (b *Backend) Address() string {
	return fmt.Sprintf("%s:%d", b.Host, b.Port)
}

// ServerConn represents a single connection to a backend server.
type ServerConn struct {
	id            uint64
	conn          net.Conn
	backend       *Backend
	codec         common.Codec
	createdAt     time.Time
	lastUsedAt    atomic.Value // time.Time
	txnActive     atomic.Bool
	mu            sync.Mutex
	preparedStmts map[string]bool // stmt name -> exists
	paramStatus   map[string]string
	capabilities  uint32 // MySQL capability flags
	inUse         atomic.Bool
	resetPending  atomic.Bool
}

// ID returns the server connection ID.
func (s *ServerConn) ID() uint64 {
	return s.id
}

// Conn returns the underlying network connection.
func (s *ServerConn) Conn() net.Conn {
	return s.conn
}

// Backend returns the backend this connection is connected to.
func (s *ServerConn) Backend() *Backend {
	return s.backend
}

// IsInUse returns true if the connection is currently in use.
func (s *ServerConn) IsInUse() bool {
	return s.inUse.Load()
}

// MarkInUse marks the connection as in use.
func (s *ServerConn) MarkInUse() {
	s.inUse.Store(true)
}

// MarkIdle marks the connection as idle.
func (s *ServerConn) MarkIdle() {
	s.inUse.Store(false)
	s.lastUsedAt.Store(time.Now())
}

// Close closes the server connection.
func (s *ServerConn) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

// HasPreparedStatement returns true if the statement is prepared on this connection.
func (s *ServerConn) HasPreparedStatement(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.preparedStmts[name]
}

// AddPreparedStatement adds a prepared statement to this connection.
func (s *ServerConn) AddPreparedStatement(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.preparedStmts[name] = true
}

// RemovePreparedStatement removes a prepared statement from this connection.
func (s *ServerConn) RemovePreparedStatement(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.preparedStmts, name)
}

var (
	connIDCounter atomic.Uint64
)

// serverConnPool manages a pool of server connections.
type serverConnPool struct {
	mu       sync.Mutex
	idle     []*ServerConn
	active   map[uint64]*ServerConn
	maxSize  int
	minSize  int
	byBackend map[string][]*ServerConn
}

// newServerConnPool creates a new server connection pool.
func newServerConnPool(minSize, maxSize int) *serverConnPool {
	return &serverConnPool{
		idle:      make([]*ServerConn, 0, maxSize),
		active:    make(map[uint64]*ServerConn),
		maxSize:   maxSize,
		minSize:   minSize,
		byBackend: make(map[string][]*ServerConn),
	}
}

// acquire gets an available connection from the pool.
func (p *serverConnPool) acquire() *ServerConn {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.idle) > 0 {
		// Get the most recently used connection (LIFO for cache efficiency)
		conn := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		p.active[conn.id] = conn
		conn.MarkInUse()
		return conn
	}

	return nil
}

// release returns a connection to the idle pool.
func (p *serverConnPool) release(conn *ServerConn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.active, conn.id)

	// Only add to idle pool if we're below max and connection is healthy
	if len(p.idle) < p.maxSize {
		conn.MarkIdle()
		p.idle = append(p.idle, conn)
	} else {
		// Pool is full, close the connection
		conn.Close()
	}
}

// addActive adds a newly created connection to the active pool.
func (p *serverConnPool) addActive(conn *ServerConn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active[conn.id] = conn
	conn.MarkInUse()
}

// remove removes a connection from the pool.
func (p *serverConnPool) remove(conn *ServerConn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.active, conn.id)

	// Remove from idle list if present
	for i, c := range p.idle {
		if c.id == conn.id {
			p.idle = append(p.idle[:i], p.idle[i+1:]...)
			break
		}
	}
}

// size returns the total number of connections (idle + active).
func (p *serverConnPool) size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle) + len(p.active)
}

// idleCount returns the number of idle connections.
func (p *serverConnPool) idleCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle)
}

// activeCount returns the number of active connections.
func (p *serverConnPool) activeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.active)
}

// closeAll closes all connections in the pool.
func (p *serverConnPool) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conn := range p.idle {
		conn.Close()
	}
	for _, conn := range p.active {
		conn.Close()
	}

	p.idle = p.idle[:0]
	for k := range p.active {
		delete(p.active, k)
	}
}

// WaitQueue manages clients waiting for a server connection.
type WaitQueue struct {
	mu      sync.Mutex
	waiters []chan *ServerConn
	metrics waitQueueMetrics
}

type waitQueueMetrics struct {
	totalWaits   atomic.Uint64
	totalTimeouts atomic.Uint64
}

// NewWaitQueue creates a new wait queue.
func NewWaitQueue() *WaitQueue {
	return &WaitQueue{
		waiters: make([]chan *ServerConn, 0),
	}
}

// Wait blocks until a server connection is available or timeout.
func (wq *WaitQueue) Wait(ctx context.Context, timeout time.Duration) (*ServerConn, error) {
	ch := make(chan *ServerConn, 1)

	wq.mu.Lock()
	wq.waiters = append(wq.waiters, ch)
	wq.mu.Unlock()

	defer func() {
		wq.mu.Lock()
		// Remove this waiter from the queue
		for i, w := range wq.waiters {
			if w == ch {
				wq.waiters = append(wq.waiters[:i], wq.waiters[i+1:]...)
				break
			}
		}
		wq.mu.Unlock()
	}()

	select {
	case conn := <-ch:
		return conn, nil
	case <-time.After(timeout):
		wq.metrics.totalTimeouts.Add(1)
		return nil, fmt.Errorf("connection timeout")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Signal gives a connection to the longest-waiting client.
func (wq *WaitQueue) Signal(conn *ServerConn) bool {
	wq.mu.Lock()
	defer wq.mu.Unlock()

	for len(wq.waiters) > 0 {
		// Get the first waiter (FIFO)
		ch := wq.waiters[0]
		wq.waiters = wq.waiters[1:]

		select {
		case ch <- conn:
			wq.metrics.totalWaits.Add(1)
			return true
		default:
			// Waiter already timed out or cancelled, try next
			continue
		}
	}

	return false
}

// Pool manages a set of backend connections for a single listen endpoint.
type Pool struct {
	mu          sync.RWMutex
	name        string
	config      *config.PoolConfig
	mode        PoolMode
	codec       common.Codec
	backends    []*Backend
	primary     *Backend
	replicas    []*Backend
	serverConns *serverConnPool
	waitQueue   *WaitQueue
	clientCount atomic.Int64
	queryCount  atomic.Int64
	txnCount    atomic.Int64
	ctx         context.Context
	cancel      context.CancelFunc
	closeCh     chan struct{}
}

// PoolStats contains pool statistics.
type PoolStats struct {
	Name               string        `json:"name"`
	Mode               string        `json:"mode"`
	ClientConnections  int64         `json:"client_connections"`
	ServerConnections  int           `json:"server_connections"`
	IdleConnections    int           `json:"idle_connections"`
	ActiveConnections  int           `json:"active_connections"`
	WaitingClients     int           `json:"waiting_clients"`
	TotalQueries       int64         `json:"total_queries"`
	TotalTransactions  int64         `json:"total_transactions"`
	BackendCount       int           `json:"backend_count"`
}

// NewPool creates a new connection pool.
func NewPool(cfg *config.PoolConfig, codec common.Codec) (*Pool, error) {
	mode, err := ParsePoolMode(cfg.Mode)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	pool := &Pool{
		name:        cfg.Name,
		config:      cfg,
		mode:        mode,
		codec:       codec,
		backends:    make([]*Backend, 0),
		replicas:    make([]*Backend, 0),
		serverConns: newServerConnPool(cfg.Limits.MinServerConnections, cfg.Limits.MaxServerConnections),
		waitQueue:   NewWaitQueue(),
		ctx:         ctx,
		cancel:      cancel,
		closeCh:     make(chan struct{}),
	}

	// Initialize backends from config
	for _, host := range cfg.Backend.Hosts {
		backend := &Backend{
			Host:     host.Host,
			Port:     host.Port,
			Role:     host.Role,
			Weight:   host.Weight,
			Database: cfg.Backend.Database,
		}
		backend.Healthy.Store(true)
		pool.backends = append(pool.backends, backend)

		if host.Role == "primary" {
			pool.primary = backend
		} else {
			pool.replicas = append(pool.replicas, backend)
		}
	}

	return pool, nil
}

// Name returns the pool name.
func (p *Pool) Name() string {
	return p.name
}

// Mode returns the pool mode.
func (p *Pool) Mode() PoolMode {
	return p.mode
}

// Codec returns the pool's codec.
func (p *Pool) Codec() common.Codec {
	return p.codec
}

// Acquire gets a server connection from the pool.
func (p *Pool) Acquire(ctx context.Context) (*ServerConn, error) {
	// Try to get from pool immediately
	if conn := p.serverConns.acquire(); conn != nil {
		return conn, nil
	}

	// Pool is empty, check if we can create a new connection
	if p.serverConns.size() < p.config.Limits.MaxServerConnections {
		conn, err := p.createServerConn()
		if err != nil {
			return nil, err
		}
		p.serverConns.addActive(conn)
		return conn, nil
	}

	// Pool is at max capacity, wait for a connection
	if conn := p.serverConns.acquire(); conn != nil {
		return conn, nil
	}

	// Wait for a connection to become available
	conn, err := p.waitQueue.Wait(ctx, 5*time.Second) // TODO: configurable timeout
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// Release returns a server connection to the pool.
func (p *Pool) Release(conn *ServerConn) {
	// Signal any waiting clients first
	if p.waitQueue.Signal(conn) {
		return
	}

	// No waiters, return to pool
	p.serverConns.release(conn)
}

// createServerConn creates a new server connection.
func (p *Pool) createServerConn() (*ServerConn, error) {
	if len(p.backends) == 0 {
		return nil, fmt.Errorf("no backends available")
	}

	// Select a backend (primary for now)
	backend := p.selectBackend()

	// Connect to backend
	addr := backend.Address()
	netConn, err := net.DialTimeout("tcp", addr, 5*time.Second) // TODO: configurable
	if err != nil {
		return nil, fmt.Errorf("failed to connect to backend %s: %w", addr, err)
	}

	// TODO: Perform backend authentication

	conn := &ServerConn{
		id:            connIDCounter.Add(1),
		conn:          netConn,
		backend:       backend,
		codec:         p.codec,
		createdAt:     time.Now(),
		preparedStmts: make(map[string]bool),
		paramStatus:   make(map[string]string),
	}
	conn.lastUsedAt.Store(time.Now())

	return conn, nil
}

// selectBackend selects a backend server.
func (p *Pool) selectBackend() *Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// For now, select the first healthy backend
	// TODO: Implement weighted selection, replica routing
	for _, b := range p.backends {
		if b.Healthy.Load() {
			return b
		}
	}

	// Fallback to first backend even if unhealthy
	if len(p.backends) > 0 {
		return p.backends[0]
	}

	return nil
}

// IncrementClientCount increments the client connection counter.
func (p *Pool) IncrementClientCount() {
	p.clientCount.Add(1)
}

// DecrementClientCount decrements the client connection counter.
func (p *Pool) DecrementClientCount() {
	p.clientCount.Add(-1)
}

// IncrementQueryCount increments the query counter.
func (p *Pool) IncrementQueryCount() {
	p.queryCount.Add(1)
}

// IncrementTxnCount increments the transaction counter.
func (p *Pool) IncrementTxnCount() {
	p.txnCount.Add(1)
}

// Stats returns pool statistics.
func (p *Pool) Stats() PoolStats {
	return PoolStats{
		Name:              p.name,
		Mode:              p.mode.String(),
		ClientConnections: p.clientCount.Load(),
		ServerConnections: p.serverConns.size(),
		IdleConnections:   p.serverConns.idleCount(),
		ActiveConnections: p.serverConns.activeCount(),
		WaitingClients:    len(p.waitQueue.waiters),
		TotalQueries:      p.queryCount.Load(),
		TotalTransactions: p.txnCount.Load(),
		BackendCount:      len(p.backends),
	}
}

// Close closes the pool and all its connections.
func (p *Pool) Close() error {
	p.cancel()
	close(p.closeCh)
	p.serverConns.closeAll()
	return nil
}
