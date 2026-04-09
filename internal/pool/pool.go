package pool

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
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
	Host       string
	Port       int
	Role       string // primary or replica
	Weight     int
	Database   string
	Healthy    atomic.Bool
	LastCheck  time.Time // Exported for API access
	Draining   atomic.Bool // true if backend is being drained
	drainStart time.Time
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
	mu            sync.RWMutex
	name          string
	config        *config.PoolConfig
	mode          PoolMode
	codec         common.Codec
	backends      []*Backend
	primary       *Backend
	replicas      []*Backend
	serverConns   *serverConnPool
	waitQueue     *WaitQueue
	clientCount   atomic.Int64
	queryCount    atomic.Int64
	txnCount      atomic.Int64
	stmtCache     *PreparedStatementCache
	log           *logger.Logger
	tlsConfig     *tls.Config
	ctx           context.Context
	cancel        context.CancelFunc
	closeCh       chan struct{}
	healthTicker  *time.Ticker
}

// PoolStats contains pool statistics.
type PoolStats struct {
	Name                  string        `json:"name"`
	Mode                  string        `json:"mode"`
	ClientConnections     int64         `json:"client_connections"`
	ServerConnections     int           `json:"server_connections"`
	IdleConnections       int           `json:"idle_connections"`
	ActiveConnections     int           `json:"active_connections"`
	WaitingClients        int           `json:"waiting_clients"`
	TotalQueries          int64         `json:"total_queries"`
	TotalTransactions     int64         `json:"total_transactions"`
	BackendCount          int           `json:"backend_count"`
	PreparedStmtCacheSize int           `json:"prepared_stmt_cache_size"`
	PreparedStmtHitRate   float64       `json:"prepared_stmt_hit_rate"`
}

// NewPool creates a new connection pool.
func NewPool(cfg *config.PoolConfig, codec common.Codec, log *logger.Logger) (*Pool, error) {
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
		log:         log,
		ctx:         ctx,
		cancel:      cancel,
		closeCh:     make(chan struct{}),
	}

	// Initialize prepared statement cache (default: 1000 statements, 30min TTL)
	pool.stmtCache = NewPreparedStatementCache(1000, 30*time.Minute)

	// Load backend TLS config if enabled
	if cfg.Backend.TLS.Mode != "disable" && cfg.Backend.TLS.Mode != "" {
		pool.tlsConfig, err = loadBackendTLSConfig(cfg.Backend.TLS)
		if err != nil {
			return nil, fmt.Errorf("failed to load backend TLS config: %w", err)
		}
		log.Info("Backend TLS enabled", "pool", cfg.Name, "mode", cfg.Backend.TLS.Mode)
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

// createServerConn creates a new server connection with retry and failover.
func (p *Pool) createServerConn() (*ServerConn, error) {
	if len(p.backends) == 0 {
		return nil, fmt.Errorf("no backends available")
	}

	// Try backends with retry logic
	maxRetries := 3
	baseDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Select a backend
		backend := p.selectBackendWithFallback()
		if backend == nil {
			return nil, fmt.Errorf("no healthy backends available")
		}

		// Try to connect
		conn, err := p.tryConnect(backend)
		if err == nil {
			return conn, nil
		}

		// Mark backend as unhealthy temporarily
		backend.Healthy.Store(false)
		p.log.Warn("Backend connection failed, marking unhealthy",
			"backend", backend.Address(),
			"error", err,
			"attempt", attempt+1,
		)

		// Wait before retry with exponential backoff
		if attempt < maxRetries-1 {
			delay := baseDelay * time.Duration(1<<attempt)
			if delay > 2*time.Second {
				delay = 2 * time.Second
			}
			time.Sleep(delay)
		}
	}

	return nil, fmt.Errorf("failed to connect to any backend after %d attempts", maxRetries)
}

// loadBackendTLSConfig loads TLS configuration for backend connections.
func loadBackendTLSConfig(cfg config.TLSConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Configure server certificate verification
	switch cfg.Mode {
	case "require":
		tlsConfig.InsecureSkipVerify = true
	case "verify-ca", "verify-full":
		tlsConfig.InsecureSkipVerify = false
	}

	// Load client certificate for mutual TLS
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// Load CA for server certificate verification
	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}

	return tlsConfig, nil
}

// tryConnect attempts to connect to a specific backend.
func (p *Pool) tryConnect(backend *Backend) (*ServerConn, error) {
	addr := backend.Address()

	// Connect with timeout
	var netConn net.Conn
	var err error

	if p.tlsConfig != nil {
		// TLS connection
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		netConn, err = tls.DialWithDialer(dialer, "tcp", addr, p.tlsConfig)
	} else {
		// Plain TCP connection
		netConn, err = net.DialTimeout("tcp", addr, 5*time.Second)
	}

	if err != nil {
		return nil, err
	}

	// Set TCP keepalive (only for non-TLS or if we can access underlying conn)
	if tcpConn, ok := netConn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(3 * time.Minute)
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

	// Mark backend as healthy on successful connection
	backend.Healthy.Store(true)
	backend.LastCheck = time.Now()

	return conn, nil
}

// selectBackendWithFallback selects a healthy backend, preferring primary.
func (p *Pool) selectBackendWithFallback() *Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Try primary first if healthy and not draining
	if p.primary != nil && p.primary.Healthy.Load() && !p.primary.Draining.Load() {
		return p.primary
	}

	// Try replicas with round-robin
	if len(p.replicas) > 0 {
		// Simple round-robin: find first healthy replica that is not draining
		for _, replica := range p.replicas {
			if replica.Healthy.Load() && !replica.Draining.Load() {
				return replica
			}
		}
	}

	// Fallback: try any backend that is not draining
	for _, backend := range p.backends {
		if backend.Healthy.Load() && !backend.Draining.Load() {
			return backend
		}
	}

	return nil
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
	// Get prepared statement cache stats
	var stmtCacheSize int
	var stmtHitRate float64
	if p.stmtCache != nil {
		stmtStats := p.stmtCache.Stats()
		stmtCacheSize = stmtStats.Size
		stmtHitRate = stmtStats.HitRate
	}

	return PoolStats{
		Name:                  p.name,
		Mode:                  p.mode.String(),
		ClientConnections:     p.clientCount.Load(),
		ServerConnections:     p.serverConns.size(),
		IdleConnections:       p.serverConns.idleCount(),
		ActiveConnections:     p.serverConns.activeCount(),
		WaitingClients:        len(p.waitQueue.waiters),
		TotalQueries:          p.queryCount.Load(),
		TotalTransactions:     p.txnCount.Load(),
		BackendCount:          len(p.backends),
		PreparedStmtCacheSize: stmtCacheSize,
		PreparedStmtHitRate:   stmtHitRate,
	}
}

// PreparedStatementCache returns the prepared statement cache.
func (p *Pool) PreparedStatementCache() *PreparedStatementCache {
	return p.stmtCache
}

// Close closes the pool and all its connections.
func (p *Pool) Close() error {
	p.cancel()
	close(p.closeCh)
	if p.healthTicker != nil {
		p.healthTicker.Stop()
	}
	p.serverConns.closeAll()
	return nil
}

// StartHealthChecks starts the background health checking.
func (p *Pool) StartHealthChecks(interval time.Duration) {
	p.healthTicker = time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-p.ctx.Done():
				return
			case <-p.healthTicker.C:
				p.checkBackendHealth()
			}
		}
	}()
	p.log.Info("Health checks started", "pool", p.name, "interval", interval)
}

// checkBackendHealth checks the health of all backends.
func (p *Pool) checkBackendHealth() {
	p.mu.RLock()
	backends := make([]*Backend, len(p.backends))
	copy(backends, p.backends)
	p.mu.RUnlock()

	for _, backend := range backends {
		go p.checkSingleBackend(backend)
	}
}

// checkSingleBackend checks the health of a single backend.
func (p *Pool) checkSingleBackend(backend *Backend) {
	addr := backend.Address()

	// Try to establish a connection
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		wasHealthy := backend.Healthy.Load()
		backend.Healthy.Store(false)
		if wasHealthy {
			p.log.Warn("Backend health check failed",
				"pool", p.name,
				"backend", addr,
				"error", err,
			)
		}
		return
	}
	conn.Close()

	// Mark as healthy if it was unhealthy
	wasHealthy := backend.Healthy.Load()
	backend.Healthy.Store(true)
	backend.LastCheck = time.Now()
	if !wasHealthy {
		p.log.Info("Backend became healthy",
			"pool", p.name,
			"backend", addr,
		)
	}
}

// GetBackends returns a copy of the backend list.
func (p *Pool) GetBackends() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()
	backends := make([]*Backend, len(p.backends))
	copy(backends, p.backends)
	return backends
}

// GetPrimary returns the primary backend.
func (p *Pool) GetPrimary() *Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.primary
}

// GetReplicas returns the replica backends.
func (p *Pool) GetReplicas() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()
	replicas := make([]*Backend, len(p.replicas))
	copy(replicas, p.replicas)
	return replicas
}

// DrainBackend marks a backend for draining and returns the number of active connections.
func (p *Pool) DrainBackend(backendAddr string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, backend := range p.backends {
		if backend.Address() == backendAddr {
			if backend.Draining.Load() {
				return 0, fmt.Errorf("backend %s is already draining", backendAddr)
			}
			backend.Draining.Store(true)
			backend.drainStart = time.Now()
			backend.Healthy.Store(false) // Mark unhealthy to prevent new connections

			// Count active connections to this backend
			activeCount := 0
			for _, conn := range p.serverConns.active {
				if conn.backend.Address() == backendAddr {
					activeCount++
				}
			}

			p.log.Info("Backend draining started",
				"pool", p.name,
				"backend", backendAddr,
				"active_connections", activeCount,
			)
			return activeCount, nil
		}
	}

	return 0, fmt.Errorf("backend %s not found", backendAddr)
}

// CancelDrain cancels draining for a backend.
func (p *Pool) CancelDrain(backendAddr string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, backend := range p.backends {
		if backend.Address() == backendAddr {
			if !backend.Draining.Load() {
				return fmt.Errorf("backend %s is not draining", backendAddr)
			}
			backend.Draining.Store(false)
			backend.Healthy.Store(true) // Restore health

			p.log.Info("Backend draining cancelled",
				"pool", p.name,
				"backend", backendAddr,
			)
			return nil
		}
	}

	return fmt.Errorf("backend %s not found", backendAddr)
}

// IsDraining returns true if a backend is being drained.
func (p *Pool) IsDraining(backendAddr string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, backend := range p.backends {
		if backend.Address() == backendAddr {
			return backend.Draining.Load()
		}
	}
	return false
}

// GetDrainingBackends returns list of backends currently being drained.
func (p *Pool) GetDrainingBackends() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	draining := make([]*Backend, 0)
	for _, backend := range p.backends {
		if backend.Draining.Load() {
			draining = append(draining, backend)
		}
	}
	return draining
}
