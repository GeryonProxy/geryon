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

	"github.com/GeryonProxy/geryon/internal/cache"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/protocol/common"
	"github.com/GeryonProxy/geryon/internal/tokenizer"
	"github.com/GeryonProxy/geryon/internal/tlsutil"
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
	Host          string
	Port          int
	Role          string // primary or replica
	Weight        int
	Database      string
	Healthy       atomic.Bool
	LastCheck     time.Time // Exported for API access
	Draining      atomic.Bool // true if backend is being drained
	drainStart    time.Time
	ConnCount     atomic.Int64 // Active connections to this backend
}

// Address returns the backend address.
func (b *Backend) Address() string {
	return fmt.Sprintf("%s:%d", b.Host, b.Port)
}

// NewBackend creates a new backend.
func NewBackend(host string, port int, role string, weight int) *Backend {
	return &Backend{
		Host:   host,
		Port:   port,
		Role:   role,
		Weight: weight,
		Healthy: atomic.Bool{},
	}
}

// updateBackendLists updates the primary and replica backend lists.
func (p *Pool) updateBackendLists() {
	p.primary = nil
	p.replicas = nil

	for _, b := range p.backends {
		switch b.Role {
		case "primary":
			p.primary = b
		case "replica":
			p.replicas = append(p.replicas, b)
		}
	}
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

// SetConnForTest sets the underlying connection for testing purposes.
func (s *ServerConn) SetConnForTest(conn net.Conn) {
	s.conn = conn
}

// NewServerConnForTest creates a ServerConn for testing purposes.
func NewServerConnForTest(id uint64, conn net.Conn, backend *Backend) *ServerConn {
	return &ServerConn{
		id:        id,
		conn:      conn,
		backend:   backend,
		createdAt: time.Now(),
	}
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
	// Decrement backend connection counter
	if s.backend != nil {
		s.backend.ConnCount.Add(-1)
	}
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
		// Perform connection state reset before returning to pool.
		// This must complete BEFORE the connection becomes available again
		// to prevent handing out connections in an inconsistent state.
		if conn.codec != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := ResetConnection(ctx, conn.conn, conn.codec); err != nil {
				// Reset failed, close the connection
				conn.Close()
				cancel()
				return
			}
			cancel()
		}
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

// waiter represents a client waiting for a server connection.
type waiter struct {
	conn    *ServerConn // connection to deliver, or nil if not yet delivered
	ready   chan struct{} // closed when conn is ready
	timedOut bool
}

// WaitQueue manages clients waiting for a server connection.
// Uses sync.Cond to avoid race conditions between signal and timeout.
type WaitQueue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	waiters  []*waiter
	maxSize  int
	metrics  waitQueueMetrics
}

type waitQueueMetrics struct {
	totalWaits   atomic.Uint64
	totalTimeouts atomic.Uint64
}

// NewWaitQueue creates a new wait queue with a maximum capacity.
func NewWaitQueue(maxSize int) *WaitQueue {
	if maxSize <= 0 {
		maxSize = 1000 // Default cap
	}
	wq := &WaitQueue{
		waiters: make([]*waiter, 0),
		maxSize:  maxSize,
	}
	wq.cond = sync.NewCond(&wq.mu)
	return wq
}

// Wait blocks until a server connection is available or timeout.
func (wq *WaitQueue) Wait(ctx context.Context, timeout time.Duration) (*ServerConn, error) {
	w := &waiter{ready: make(chan struct{})}

	wq.mu.Lock()
	if len(wq.waiters) >= wq.maxSize {
		wq.mu.Unlock()
		return nil, fmt.Errorf("connection queue full (max %d)", wq.maxSize)
	}
	wq.waiters = append(wq.waiters, w)
	wq.mu.Unlock()

	// Remove waiter from queue on exit
	defer func() {
		wq.mu.Lock()
		for i, waiter := range wq.waiters {
			if waiter == w {
				wq.waiters = append(wq.waiters[:i], wq.waiters[i+1:]...)
				break
			}
		}
		wq.mu.Unlock()
	}()

	// Wait for signal or timeout
	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timeoutCh = time.After(timeout)
	}

	for {
		wq.mu.Lock()
		if w.conn != nil {
			wq.mu.Unlock()
			wq.metrics.totalWaits.Add(1)
			return w.conn, nil
		}
		if w.timedOut {
			wq.mu.Unlock()
			wq.metrics.totalTimeouts.Add(1)
			return nil, fmt.Errorf("connection timeout")
		}
		wq.mu.Unlock()

		select {
		case <-w.ready:
			// Got signal
			wq.mu.Lock()
			if w.conn != nil {
				wq.mu.Unlock()
				wq.metrics.totalWaits.Add(1)
				return w.conn, nil
			}
			wq.mu.Unlock()
		case <-timeoutCh:
			wq.mu.Lock()
			w.timedOut = true
			wq.mu.Unlock()
			wq.metrics.totalTimeouts.Add(1)
			return nil, fmt.Errorf("connection timeout")
		case <-ctx.Done():
			wq.mu.Lock()
			w.timedOut = true
			wq.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

// Signal gives a connection to the longest-waiting client.
// Returns true if a waiter received the connection.
func (wq *WaitQueue) Signal(conn *ServerConn) bool {
	wq.mu.Lock()
	defer wq.mu.Unlock()

	if len(wq.waiters) == 0 {
		return false
	}

	// Get first waiter (FIFO)
	w := wq.waiters[0]
	wq.waiters = wq.waiters[1:]

	// Deliver connection
	w.conn = conn
	close(w.ready)

	wq.metrics.totalWaits.Add(1)
	return true
}

// Pool manages a set of backend connections for a single listen endpoint.
type Pool struct {
	mu              sync.RWMutex
	name            string
	config          *config.PoolConfig
	mode            PoolMode
	codec           common.Codec
	backends        []*Backend
	primary         *Backend
	replicas        []*Backend
	serverConns     *serverConnPool
	waitQueue       *WaitQueue
	clientCount     atomic.Int64
	queryCount      atomic.Int64
	txnCount        atomic.Int64
	stmtCache       *PreparedStatementCache
	queryCache      *cache.Store
	log             *logger.Logger
	tlsConfig       *tls.Config
	ctx             context.Context
	cancel          context.CancelFunc
	closeCh         chan struct{}
	healthChecker   *HealthChecker
	txnManager      *TransactionManager
	userConnCounts  sync.Map // map[string]*atomic.Int64 - per-user connection counts
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
	ActiveTransactions    int           `json:"active_transactions"`
	MaxServerConnections  int           `json:"max_server_connections"`
	TotalQueries          int64         `json:"total_queries"`
	TotalTransactions     int64         `json:"total_transactions"`
	BackendCount          int           `json:"backend_count"`
	PreparedStmtCacheSize int           `json:"prepared_stmt_cache_size"`
	PreparedStmtHitRate   float64       `json:"prepared_stmt_hit_rate"`
	QueryCacheEntries     int           `json:"query_cache_entries"`
	QueryCacheMemoryUsed  int64         `json:"query_cache_memory_used"`
	QueryCacheHitRate     float64       `json:"query_cache_hit_rate"`
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
		waitQueue:   NewWaitQueue(1000),
		log:         log,
		ctx:         ctx,
		cancel:      cancel,
		closeCh:     make(chan struct{}),
	}

	// Initialize prepared statement cache (default: 1000 statements, 30min TTL)
	pool.stmtCache = NewPreparedStatementCache(1000, 30*time.Minute)

	// Initialize transaction manager with configurable timeouts
	txnTimeout := parseDuration(cfg.Transaction.Timeout, 30*time.Minute)
	txnIdleTimeout := parseDuration(cfg.Transaction.IdleTimeout, 5*time.Minute)
	txnCheckInterval := parseDuration(cfg.Transaction.CheckInterval, 30*time.Second)
	pool.txnManager = NewTransactionManager(txnTimeout, txnIdleTimeout, txnCheckInterval, log)

	// Initialize query result cache if enabled
	if cfg.Cache.Enabled {
		cacheSize := int64(100 * 1024 * 1024) // 100MB default
		if cfg.Cache.MaxMemory != "" {
			// Parse max memory (e.g., "100MB", "1GB")
			// Simplified: just use default for now
		}
		pool.queryCache = cache.NewStore(cacheSize, 5*time.Minute)
		log.Info("Query cache enabled", "pool", cfg.Name)
	}

	// Load backend TLS config if enabled
	if cfg.Backend.TLS.Mode != "disable" && cfg.Backend.TLS.Mode != "" {
		pool.tlsConfig, err = loadBackendTLSConfig(cfg.Backend.TLS)
		if err != nil {
			return nil, fmt.Errorf("failed to load backend TLS config: %w", err)
		}
		log.Info("Backend TLS enabled", "pool", cfg.Name, "mode", cfg.Backend.TLS.Mode)
	}

	// Initialize health checker with protocol-specific checks
	pool.healthChecker = NewHealthChecker(&cfg.Health, cfg.Body, log)

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
		pool.healthChecker.AddBackend(backend)

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
	waitTimeout := parseDuration(p.config.Limits.ConnectionTimeout, 5*time.Second)
	conn, err := p.waitQueue.Wait(ctx, waitTimeout)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// AcquireToRole gets a server connection from the pool targeting a specific backend role.
func (p *Pool) AcquireToRole(ctx context.Context, role string) (*ServerConn, error) {
	// Try to get from pool immediately (any connection works)
	if conn := p.serverConns.acquire(); conn != nil {
		return conn, nil
	}

	// Need to create a new connection targeting the specific role
	if p.serverConns.size() < p.config.Limits.MaxServerConnections {
		conn, err := p.createServerConnToRole(role)
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

	waitTimeout := parseDuration(p.config.Limits.ConnectionTimeout, 5*time.Second)
	conn, err := p.waitQueue.Wait(ctx, waitTimeout)
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

		// Check circuit breaker
		if p.isCircuitOpen(backend) {
			p.log.Debug("Circuit breaker open, skipping backend",
				"backend", backend.Address(),
			)
			continue
		}

		// Try to connect
		conn, err := p.tryConnect(backend)
		if err == nil {
			// Record success through circuit breaker
			p.recordBackendSuccess(backend)
			return conn, nil
		}

		// Record failure through circuit breaker
		p.recordBackendFailure(backend)

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

// createServerConnToRole creates a new server connection to a specific backend role.
func (p *Pool) createServerConnToRole(role string) (*ServerConn, error) {
	if len(p.backends) == 0 {
		return nil, fmt.Errorf("no backends available")
	}

	maxRetries := 3
	baseDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		backend := p.selectBackendByRole(role)
		if backend == nil {
			return nil, fmt.Errorf("no healthy %s backend available", role)
		}

		// Check circuit breaker
		if p.isCircuitOpen(backend) {
			p.log.Debug("Circuit breaker open, skipping backend",
				"backend", backend.Address(),
			)
			continue
		}

		conn, err := p.tryConnect(backend)
		if err == nil {
			// Record success through circuit breaker
			p.recordBackendSuccess(backend)
			return conn, nil
		}

		// Record failure through circuit breaker
		p.recordBackendFailure(backend)

		backend.Healthy.Store(false)
		p.log.Warn("Backend connection failed, marking unhealthy",
			"backend", backend.Address(),
			"error", err,
			"attempt", attempt+1,
		)

		if attempt < maxRetries-1 {
			delay := baseDelay * time.Duration(1<<attempt)
			if delay > 2*time.Second {
				delay = 2 * time.Second
			}
			time.Sleep(delay)
		}
	}

	return nil, fmt.Errorf("failed to connect to %s backend after %d attempts", role, maxRetries)
}

// loadBackendTLSConfig loads TLS configuration for backend connections.
func loadBackendTLSConfig(cfg config.TLSConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		CipherSuites: tlsutil.CipherSuites12(),
	}

	// Configure server certificate verification
	switch cfg.Mode {
	case "require":
		// Verify against system CAs (InsecureSkipVerify defaults to false)
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

	dialTimeout := parseDuration(p.config.Limits.ConnectionTimeout, 5*time.Second)

	if p.tlsConfig != nil {
		// TLS connection
		dialer := &net.Dialer{Timeout: dialTimeout}
		netConn, err = tls.DialWithDialer(dialer, "tcp", addr, p.tlsConfig)
	} else {
		// Plain TCP connection
		netConn, err = net.DialTimeout("tcp", addr, dialTimeout)
	}

	if err != nil {
		return nil, err
	}

	// Set TCP keepalive (only for non-TLS or if we can access underlying conn)
	if tcpConn, ok := netConn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(3 * time.Minute)
	}

	// Backend authentication is performed by the frontend handler after connection
	// This ensures proper protocol-specific authentication (SCRAM-SHA-256, MD5, etc.)
	// The pool uses backend credentials from config for auth interception mode

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
	backend.ConnCount.Add(1)

	return conn, nil
}

// selectBackendWithFallback selects a healthy backend, preferring primary.
// Respects circuit breaker state - skips backends with open circuits.
func (p *Pool) selectBackendWithFallback() *Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Try primary first if healthy, not draining, and circuit is not open
	if p.primary != nil && p.primary.Healthy.Load() && !p.primary.Draining.Load() {
		if !p.isCircuitOpen(p.primary) {
			return p.primary
		}
	}

	// Try replicas with round-robin
	if len(p.replicas) > 0 {
		// Simple round-robin: find first healthy replica that is not draining and circuit not open
		for _, replica := range p.replicas {
			if replica.Healthy.Load() && !replica.Draining.Load() && !p.isCircuitOpen(replica) {
				return replica
			}
		}
	}

	// Fallback: try any backend that is not draining and circuit not open
	for _, backend := range p.backends {
		if backend.Healthy.Load() && !backend.Draining.Load() && !p.isCircuitOpen(backend) {
			return backend
		}
	}

	return nil
}

// isCircuitOpen checks if the circuit breaker is open for a backend.
func (p *Pool) isCircuitOpen(backend *Backend) bool {
	if p.healthChecker == nil {
		return false
	}
	health := p.healthChecker.GetHealth(backend)
	if health == nil {
		return false
	}
	return health.IsCircuitOpen()
}

// recordBackendSuccess records a successful connection to the backend.
func (p *Pool) recordBackendSuccess(backend *Backend) {
	if p.healthChecker == nil {
		return
	}
	health := p.healthChecker.GetHealth(backend)
	if health == nil {
		return
	}
	health.RecordSuccess()
}

// recordBackendFailure records a failed connection to the backend.
func (p *Pool) recordBackendFailure(backend *Backend) {
	if p.healthChecker == nil {
		return
	}
	health := p.healthChecker.GetHealth(backend)
	if health == nil {
		return
	}
	health.RecordFailure()
}

// selectBackendByRole selects a healthy backend matching the requested role.
// Falls back to any available backend if the requested role is unavailable.
// Respects circuit breaker state.
func (p *Pool) selectBackendByRole(role string) *Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	switch role {
	case "replica":
		// Round-robin across healthy replicas
		for _, replica := range p.replicas {
			if replica.Healthy.Load() && !replica.Draining.Load() && !p.isCircuitOpen(replica) {
				return replica
			}
		}
		// Fallback to primary
		if p.primary != nil && p.primary.Healthy.Load() && !p.primary.Draining.Load() && !p.isCircuitOpen(p.primary) {
			return p.primary
		}
	case "primary":
		if p.primary != nil && p.primary.Healthy.Load() && !p.primary.Draining.Load() && !p.isCircuitOpen(p.primary) {
			return p.primary
		}
	}

	// Fallback: try any backend
	for _, backend := range p.backends {
		if backend.Healthy.Load() && !backend.Draining.Load() && !p.isCircuitOpen(backend) {
			return backend
		}
	}

	return nil
}

// selectBackend selects a backend server using weighted round-robin.
func (p *Pool) selectBackend() *Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Get healthy backends
	var healthyBackends []*Backend
	for _, b := range p.backends {
		if b.Healthy.Load() && !b.Draining.Load() {
			healthyBackends = append(healthyBackends, b)
		}
	}

	if len(healthyBackends) == 0 {
		// Fallback to first backend even if unhealthy
		if len(p.backends) > 0 {
			return p.backends[0]
		}
		return nil
	}

	if len(healthyBackends) == 1 {
		return healthyBackends[0]
	}

	// Weighted round-robin selection
	// Find backend with highest effective weight
	var selected *Backend
	maxWeight := -1

	for _, b := range healthyBackends {
		weight := b.Weight
		if weight <= 0 {
			weight = 100 // Default weight
		}

		// Factor in connection count for load balancing
		connCount := int(b.ConnCount.Load())
		effectiveWeight := weight - connCount*10

		if effectiveWeight > maxWeight {
			maxWeight = effectiveWeight
			selected = b
		}
	}

	return selected
}

// selectBackendForQuery selects a backend based on query type (read/write splitting).
// isWrite indicates if this is a write query (INSERT, UPDATE, DELETE, etc.)
func (p *Pool) selectBackendForQuery(isWrite bool) *Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// For writes, always use primary
	if isWrite {
		if p.primary != nil && p.primary.Healthy.Load() && !p.primary.Draining.Load() {
			return p.primary
		}
		// Fallback to any healthy backend if primary is down
		for _, b := range p.backends {
			if b.Healthy.Load() && !b.Draining.Load() {
				return b
			}
		}
		return nil
	}

	// For reads, prefer replicas if available and read_write_split is enabled
	if p.config.Routing.ReadWriteSplit && len(p.replicas) > 0 {
		var healthyReplicas []*Backend
		for _, r := range p.replicas {
			if r.Healthy.Load() && !r.Draining.Load() {
				healthyReplicas = append(healthyReplicas, r)
			}
		}

		if len(healthyReplicas) > 0 {
			// Weighted round-robin among replicas
			return selectWeightedBackend(healthyReplicas)
		}
	}

	// Fallback to primary for reads if no replicas available
	if p.primary != nil && p.primary.Healthy.Load() && !p.primary.Draining.Load() {
		return p.primary
	}

	// Last resort: any healthy backend
	for _, b := range p.backends {
		if b.Healthy.Load() && !b.Draining.Load() {
			return b
		}
	}

	return nil
}

// selectWeightedBackend selects a backend using weighted round-robin.
func selectWeightedBackend(backends []*Backend) *Backend {
	if len(backends) == 0 {
		return nil
	}
	if len(backends) == 1 {
		return backends[0]
	}

	var selected *Backend
	maxWeight := -1

	for _, b := range backends {
		weight := b.Weight
		if weight <= 0 {
			weight = 100 // Default weight
		}

		// Factor in connection count for load balancing
		connCount := int(b.ConnCount.Load())
		effectiveWeight := weight - connCount*10

		if effectiveWeight > maxWeight {
			maxWeight = effectiveWeight
			selected = b
		}
	}

	return selected
}

// IncrementClientCount increments the client connection counter.
func (p *Pool) IncrementClientCount() {
	p.clientCount.Add(1)
}

// TryIncrementClientCount atomically checks limit and increments.
// Returns false if the connection limit would be exceeded.
func (p *Pool) TryIncrementClientCount(max int64) bool {
	for {
		current := p.clientCount.Load()
		if current >= max {
			return false
		}
		if p.clientCount.CompareAndSwap(current, current+1) {
			return true
		}
		// Retry on CAS failure
	}
}

// DecrementClientCount decrements the client connection counter.
func (p *Pool) DecrementClientCount() {
	p.clientCount.Add(-1)
}

// TryIncrementUserCount atomically checks per-user limit and increments.
// Returns false if the per-user limit would be exceeded.
func (p *Pool) TryIncrementUserCount(username string, max int) bool {
	if username == "" || max <= 0 {
		return true // No limit configured
	}

	var counter *atomic.Int64
	countInterface, loaded := p.userConnCounts.LoadOrStore(username, new(atomic.Int64))
	if loaded {
		counter = countInterface.(*atomic.Int64)
	} else {
		counter = countInterface.(*atomic.Int64)
	}

	for {
		current := counter.Load()
		if int(current) >= max {
			return false
		}
		if counter.CompareAndSwap(current, current+1) {
			return true
		}
		// Retry on CAS failure
	}
}

// DecrementUserCount decrements the per-user connection counter.
func (p *Pool) DecrementUserCount(username string) {
	if username == "" {
		return
	}
	countInterface, loaded := p.userConnCounts.Load(username)
	if !loaded {
		return
	}
	counter := countInterface.(*atomic.Int64)
	counter.Add(-1)
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

	// Get query cache stats
	var queryCacheEntries int
	var queryCacheMemory int64
	var queryCacheHitRate float64
	if p.queryCache != nil {
		qcStats := p.queryCache.Stats()
		queryCacheEntries = qcStats.Entries
		queryCacheMemory = qcStats.MemoryUsed
		queryCacheHitRate = qcStats.HitRate
	}

	return PoolStats{
		Name:                  p.name,
		Mode:                  p.mode.String(),
		ClientConnections:     p.clientCount.Load(),
		ServerConnections:     p.serverConns.size(),
		IdleConnections:       p.serverConns.idleCount(),
		ActiveConnections:     p.serverConns.activeCount(),
		WaitingClients:        len(p.waitQueue.waiters),
		ActiveTransactions:    p.txnManager.GetActiveCount(),
		MaxServerConnections:  p.config.Limits.MaxServerConnections,
		TotalQueries:          p.queryCount.Load(),
		TotalTransactions:     p.txnCount.Load(),
		BackendCount:          len(p.backends),
		PreparedStmtCacheSize: stmtCacheSize,
		PreparedStmtHitRate:   stmtHitRate,
		QueryCacheEntries:     queryCacheEntries,
		QueryCacheMemoryUsed:  queryCacheMemory,
		QueryCacheHitRate:     queryCacheHitRate,
	}
}

// PreparedStatementCache returns the prepared statement cache.
func (p *Pool) PreparedStatementCache() *PreparedStatementCache {
	return p.stmtCache
}

// QueryCache returns the query result cache.
func (p *Pool) QueryCache() *cache.Store {
	return p.queryCache
}

// TransactionManager returns the transaction manager.
func (p *Pool) TransactionManager() *TransactionManager {
	return p.txnManager
}

// HealthChecker returns the health checker for this pool.
func (p *Pool) HealthChecker() *HealthChecker {
	return p.healthChecker
}

// GetCachedResult checks the query cache for a result.
func (p *Pool) GetCachedResult(query string, params []byte) ([]byte, bool) {
	if p.queryCache == nil {
		return nil, false
	}

	// Generate cache key
	key := fmt.Sprintf("%s:%x", query, params)
	return p.queryCache.Get(key)
}

// SetCachedResult stores a result in the query cache.
func (p *Pool) SetCachedResult(query string, params []byte, result []byte, ttl time.Duration) error {
	if p.queryCache == nil {
		return nil
	}

	// Generate cache key
	key := fmt.Sprintf("%s:%x", query, params)

	// Extract tables from query for invalidation
	tables := tokenizer.ExtractTables(query)

	return p.queryCache.Set(key, result, tables, ttl)
}

// InvalidateCache invalidates cached results for a table.
func (p *Pool) InvalidateCache(table string) {
	if p.queryCache == nil {
		return
	}

	p.queryCache.InvalidateTable(table)
}

// Close closes the pool and all its connections.
func (p *Pool) Close() error {
	p.cancel()
	close(p.closeCh)
	if p.healthChecker != nil {
		p.healthChecker.Stop()
	}
	if p.txnManager != nil {
		p.txnManager.Stop()
	}
	p.serverConns.closeAll()
	return nil
}

// UpdateConfig updates pool configuration dynamically.
// Only safe-to-change fields can be updated without restart.
func (p *Pool) UpdateConfig(cfg *config.PoolConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Validate that critical fields haven't changed
	if cfg.Body != p.config.Body {
		return fmt.Errorf("cannot change pool body dynamically (requires restart)")
	}
	if cfg.Listen.Host != p.config.Listen.Host || cfg.Listen.Port != p.config.Listen.Port {
		return fmt.Errorf("cannot change pool listen address dynamically (requires restart)")
	}

	// Update safe fields
	p.config.Limits.MaxClientConnections = cfg.Limits.MaxClientConnections
	p.config.Limits.MaxServerConnections = cfg.Limits.MaxServerConnections
	p.config.Limits.MinServerConnections = cfg.Limits.MinServerConnections
	p.config.Limits.MaxIdleTime = cfg.Limits.MaxIdleTime
	p.config.Limits.MaxConnectionLifetime = cfg.Limits.MaxConnectionLifetime
	p.config.Limits.ConnectionTimeout = cfg.Limits.ConnectionTimeout
	p.config.Limits.QueryTimeout = cfg.Limits.QueryTimeout
	p.config.Limits.IdleTransactionTimeout = cfg.Limits.IdleTransactionTimeout

	// Update health check settings
	p.config.Health.CheckInterval = cfg.Health.CheckInterval
	p.config.Health.CheckQuery = cfg.Health.CheckQuery
	p.config.Health.MaxFailures = cfg.Health.MaxFailures

	// Update cache settings
	p.config.Cache.Enabled = cfg.Cache.Enabled
	p.config.Cache.MaxMemory = cfg.Cache.MaxMemory
	p.config.Cache.DefaultTTL = cfg.Cache.DefaultTTL

	// Update routing settings
	p.config.Routing.ReadWriteSplit = cfg.Routing.ReadWriteSplit

	// Update backends (add new, remove old)
	p.updateBackends(cfg)

	p.log.Info("Pool configuration updated dynamically", "pool", p.name)
	return nil
}

// updateBackends updates the backend list while preserving connection state.
func (p *Pool) updateBackends(cfg *config.PoolConfig) {
	// Create map of new backends
	newBackends := make(map[string]bool)
	for _, h := range cfg.Backend.Hosts {
		key := fmt.Sprintf("%s:%d", h.Host, h.Port)
		newBackends[key] = true
	}

	// Remove backends that no longer exist
	var keptBackends []*Backend
	for _, b := range p.backends {
		key := b.Address()
		if newBackends[key] {
			keptBackends = append(keptBackends, b)
			delete(newBackends, key)
		} else {
			p.log.Info("Removing backend from pool", "pool", p.name, "backend", key)
			// Mark for draining
			b.Draining.Store(true)
			if p.healthChecker != nil {
				p.healthChecker.RemoveBackend(b)
			}
		}
	}

	// Add new backends
	for _, h := range cfg.Backend.Hosts {
		key := fmt.Sprintf("%s:%d", h.Host, h.Port)
		if newBackends[key] {
			backend := NewBackend(h.Host, h.Port, h.Role, h.Weight)
			keptBackends = append(keptBackends, backend)
			if p.healthChecker != nil {
				p.healthChecker.AddBackend(backend)
			}
			p.log.Info("Adding new backend to pool", "pool", p.name, "backend", key, "role", h.Role)
		}
	}

	p.backends = keptBackends
	p.updateBackendLists()
}

// StartHealthChecks starts the background health checking.
func (p *Pool) StartHealthChecks() {
	if p.healthChecker != nil {
		p.healthChecker.Start()
		p.log.Info("Health checks started", "pool", p.name, "interval", p.config.Health.CheckInterval)
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
