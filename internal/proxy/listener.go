// Package proxy implements the TCP listener and protocol-specific
// connection handling for the Geryon database proxy. It manages client
// acceptance, authentication, and the relay between client and backend
// server connections for PostgreSQL, MySQL, and MSSQL protocols.
package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/cache"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
	"github.com/GeryonProxy/geryon/internal/protocol/common"
	"github.com/GeryonProxy/geryon/internal/protocol/postgresql"
	"github.com/GeryonProxy/geryon/internal/stmt"
	"github.com/GeryonProxy/geryon/internal/tlsutil"
)

// MySQL packet size limit (16MB is protocol max)
const maxMySQLPayload = 16 << 20

// bufferPool provides reusable buffers for relay operations to reduce allocations.
// Using sync.Pool for lock-free get/put under high concurrency.
var bufferPool = sync.Pool{
	New: func() interface{} {
		// Default 4KB buffer, will grow as needed
		return make([]byte, 4096)
	},
}

// getBuffer retrieves a buffer from the pool.
func getBuffer() []byte {
	return bufferPool.Get().([]byte)
}

// putBuffer returns a buffer to the pool.
func putBuffer(buf []byte) {
	// Reset to 4KB capacity if grown too large
	if cap(buf) > 8192 {
		buf = buf[:4096][:0:4096]
	}
	bufferPool.Put(buf)
}

// Listener manages incoming client connections for a pool.
type Listener struct {
	mu             sync.RWMutex
	pool           *pool.Pool
	config         *config.PoolConfig
	codec          common.Codec
	listener       net.Listener
	address        string
	active         atomic.Bool
	sessions       map[uint64]*ProxySession
	connWG         sync.WaitGroup // Tracks in-flight handleConnection goroutines
	tlsConfig      *tls.Config
	userDB         *auth.UserDatabase
	cacheStore     *cache.Store
	cacheRules     *cache.RulesEngine
	queryLogger    *logger.QueryLogger
	transactionMgr *pool.TransactionManager
	authLimiter    *auth.AuthLimiter
	router         *pool.Router
	authMode       string // "passthrough" or "interception"
	ctx            context.Context
	cancel         context.CancelFunc
	log            *logger.Logger
}

// NewListener creates a new proxy listener.
func NewListener(poolInstance *pool.Pool, cfg *config.PoolConfig, codec common.Codec, userDB *auth.UserDatabase, log *logger.Logger) (*Listener, error) {
	ctx, cancel := context.WithCancel(context.Background())

	l := &Listener{
		pool:     poolInstance,
		config:   cfg,
		codec:    codec,
		address:  fmt.Sprintf("%s:%d", cfg.Listen.Host, cfg.Listen.Port),
		sessions: make(map[uint64]*ProxySession),
		userDB:   userDB,
		ctx:      ctx,
		cancel:   cancel,
		log:      log,
		authMode: cfg.AuthMode,
	}

	// Setup query logger if enabled
	qlConfig := logger.DefaultQueryLogConfig()
	qlConfig.Enabled = true
	// Sanitize pool name to prevent path traversal
	safeName := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(cfg.Name, "_")
	qlConfig.Directory = filepath.Join("logs", "queries", safeName)
	qlConfig.LogAllQueries = false // Set to true for debug mode
	if queryLogger, err := logger.NewQueryLogger(qlConfig); err == nil {
		l.queryLogger = queryLogger
		log.Info("Query logger enabled", "pool", cfg.Name, "directory", qlConfig.Directory)
	} else {
		log.Warn("Failed to create query logger", "error", err)
	}

	// Setup transaction manager with configurable timeouts
	txnTimeout := parseTxnDuration(l.config.Transaction.Timeout, 30*time.Minute)
	txnIdleTimeout := parseTxnDuration(l.config.Transaction.IdleTimeout, 5*time.Minute)
	txnCheckInterval := parseTxnDuration(l.config.Transaction.CheckInterval, 30*time.Second)
	l.transactionMgr = pool.NewTransactionManager(txnTimeout, txnIdleTimeout, txnCheckInterval, log)

	// Setup auth rate limiter (10 failures per 5min window, 5min lockout)
	l.authLimiter = auth.NewAuthLimiter()

	// Setup cache if enabled
	if cfg.Cache.Enabled {
		maxMemory := parseMemoryString(cfg.Cache.MaxMemory)
		defaultTTL, err := cache.ParseDuration(cfg.Cache.DefaultTTL)
		if err != nil {
			defaultTTL = 5 * time.Minute
		}
		l.cacheStore = cache.NewStore(maxMemory, defaultTTL)
		l.cacheRules = cache.NewRulesEngine()

		// Load cache rules from config
		for _, rule := range cfg.Cache.Rules {
			ttl, _ := cache.ParseDuration(rule.TTL)
			l.cacheRules.AddRule(rule.Match, ttl, !rule.NeverCache)
		}

		// Start cleanup goroutine
		l.cacheStore.StartCleanup(1 * time.Minute)
		log.Info("Query cache enabled", "pool", cfg.Name, "max_memory", cfg.Cache.MaxMemory)
	}

	// Setup TLS if configured
	if cfg.TLS.Mode != "disable" {
		if err := l.setupTLS(); err != nil {
			return nil, fmt.Errorf("failed to setup TLS: %w", err)
		}
	}

	// Setup query router for read/write splitting
	if cfg.Routing.ReadWriteSplit {
		backends := poolInstance.GetBackends()
		if len(backends) > 0 {
			router, err := pool.NewRouter(&cfg.Routing, backends)
			if err != nil {
				log.Warn("Failed to create query router", "error", err)
			} else {
				l.router = router
				log.Info("Read/write splitting enabled", "pool", cfg.Name)
			}
		}
	}

	return l, nil
}

// setupTLS configures TLS for the listener.
func (l *Listener) setupTLS() error {
	tlsConfig, err := tlsutil.LoadServerConfig(l.config.TLS)
	if err != nil {
		return err
	}
	l.tlsConfig = tlsConfig
	return nil
}

// parseMemoryString parses memory string like "64MB", "1GB" to bytes.
func parseMemoryString(s string) int64 {
	if s == "" {
		return 64 * 1024 * 1024 // 64MB default
	}

	var multiplier int64 = 1
	s = strings.ToUpper(strings.TrimSpace(s))

	if strings.HasSuffix(s, "GB") {
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "GB")
	} else if strings.HasSuffix(s, "MB") {
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "MB")
	} else if strings.HasSuffix(s, "KB") {
		multiplier = 1024
		s = strings.TrimSuffix(s, "KB")
	}

	var value int64
	fmt.Sscanf(s, "%d", &value)
	if value == 0 {
		return 64 * 1024 * 1024 // 64MB default
	}

	return value * multiplier
}

// parseTxnDuration parses a transaction timeout string like "30m", "5s" to time.Duration.
func parseTxnDuration(s string, defaultVal time.Duration) time.Duration {
	if s == "" {
		return defaultVal
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return defaultVal
}

// Start starts the listener.
func (l *Listener) Start() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.active.Load() {
		return fmt.Errorf("listener already started")
	}

	// Create TCP listener
	ln, err := net.Listen("tcp", l.address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", l.address, err)
	}

	// Wrap with TLS if configured
	if l.tlsConfig != nil {
		ln = tls.NewListener(ln, l.tlsConfig)
	}

	l.listener = ln
	l.active.Store(true)

	// Start protocol-aware health checks
	if l.pool != nil {
		l.pool.StartHealthChecks()
	}

	l.log.Info("Listener started",
		"address", l.address,
		"pool", l.config.Name,
		"mode", l.config.Mode,
	)

	// Accept connections
	go l.acceptLoop()

	return nil
}

// acceptLoop accepts incoming connections.
func (l *Listener) acceptLoop() {
	for {
		conn, err := l.listener.Accept()
		if err != nil {
			if l.ctx.Err() != nil {
				// Listener closed
				return
			}
			l.log.Error("Failed to accept connection", "error", err)
			continue
		}

		// Set deadlines to prevent slowloris attacks and idle connection buildup
		conn.SetDeadline(time.Now().Add(2 * time.Minute)) // Overall idle timeout (M-5 fix)
		go l.handleConnection(conn)
	}
}

// handleConnection handles a new client connection.
func (l *Listener) handleConnection(conn net.Conn) {
	l.connWG.Add(1)
	defer func() {
		l.connWG.Done()
		if r := recover(); r != nil {
			l.log.Error("panic in handleConnection", "pool", l.pool.Name(), "client", conn.RemoteAddr(), "panic", r)
		}
		conn.Close()
	}()

	// Atomically check limit and increment to prevent race condition
	maxConns := int64(l.config.Limits.MaxClientConnections)
	if !l.pool.TryIncrementClientCount(maxConns) {
		l.log.Warn("Max client connections reached", "pool", l.config.Name)
		return
	}

	// Ensure counter is decremented on all exit paths (H-3 fix)
	defer l.pool.DecrementClientCount()

	// Set TCP keepalive to prevent half-open connections
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}

	// Set read deadline for slowloris protection
	idleTimeout := parseDuration(l.config.Limits.MaxIdleTime, 5*time.Minute)
	if idleTimeout > 0 {
		conn.SetReadDeadline(time.Now().Add(idleTimeout))
	}

	// Create proxy session with cache, query logger, and transaction manager
	session, err := NewProxySession(conn, l.pool, l.codec, l.userDB, l.config, l.cacheStore, l.cacheRules, l.queryLogger, l.transactionMgr, l.authLimiter, l.router, l.tlsConfig, l.log)
	if err != nil {
		l.log.Error("Failed to create session", "error", err)
		return
	}

	// Set auth mode from listener config (M-1 fix)
	session.authMode = l.authMode

	// Register session
	l.mu.Lock()
	l.sessions[session.ID()] = session
	l.mu.Unlock()

	// Handle session
	session.Handle(l.ctx)

	// Cleanup
	l.pool.DecrementClientCount()

	l.mu.Lock()
	delete(l.sessions, session.ID())
	l.mu.Unlock()

	session.Close()

	l.log.Info("Session closed",
		"id", session.ID(),
		"pool", l.config.Name,
		"queries", session.QueryCount(),
	)
}

// Stop stops the listener.
func (l *Listener) Stop() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.active.Load() {
		return nil
	}

	l.active.Store(false)
	l.cancel()

	if l.listener != nil {
		l.listener.Close()
	}

	// Wait for in-flight handleConnection goroutines to complete (M-4 fix)
	l.connWG.Wait()

	// Close all active sessions
	for _, session := range l.sessions {
		session.Close()
	}
	l.sessions = make(map[uint64]*ProxySession)

	// Stop query logger
	if l.queryLogger != nil {
		if err := l.queryLogger.Stop(); err != nil {
			l.log.Debug("Failed to stop query logger", "error", err)
		}
	}

	// Stop transaction manager
	if l.transactionMgr != nil {
		l.transactionMgr.Stop()
	}

	l.log.Info("Listener stopped", "address", l.address)

	return nil
}

// Address returns the listener address.
func (l *Listener) Address() string {
	return l.address
}

// IsActive returns true if the listener is active.
func (l *Listener) IsActive() bool {
	return l.active.Load()
}

// SessionCount returns the number of active sessions.
func (l *Listener) SessionCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.sessions)
}

// QueryLogger returns the query logger.
func (l *Listener) QueryLogger() *logger.QueryLogger {
	return l.queryLogger
}

// TransactionManager returns the transaction manager.
func (l *Listener) TransactionManager() *pool.TransactionManager {
	return l.transactionMgr
}

// Pool returns the connection pool.
func (l *Listener) Pool() *pool.Pool {
	return l.pool
}

// Config returns the pool config.
func (l *Listener) Config() *config.PoolConfig {
	return l.config
}

// ProxySession represents a client connection session.
type ProxySession struct {
	id              uint64
	clientConn      net.Conn
	serverConn      *pool.ServerConn
	pool            *pool.Pool
	codec           common.Codec
	userDB          *auth.UserDatabase
	config          *config.PoolConfig
	poolSession     *pool.Session
	relay           *Relay
	log             *logger.Logger
	queryLogger     *logger.QueryLogger
	transactionMgr  *pool.TransactionManager
	transactionInfo *pool.TransactionInfo
	authLimiter     *auth.AuthLimiter
	router          *pool.Router
	stmtRepreparer  *stmt.TransparentRepreparer
	closed          atomic.Bool
	queryCount      atomic.Int64
	authenticated   atomic.Bool
	username        string
	database        string
	authMode        string // "passthrough" or "interception"
	scramState      *auth.SCRAMState
	cacheStore      *cache.Store
	cacheRules      *cache.RulesEngine
	tlsConfig       *tls.Config
	// Query timing for logging
	currentQuery   string
	queryStartTime time.Time
	lastBoundStmt  string // last bound statement name for re-preparation
	// M-8 fix: pending parse tracking for confirmed prepared statements
	pendingParseMu    sync.Mutex
	pendingParseStmt  string // statement name waiting for ParseComplete confirmation
	pendingParseQuery string // query for the pending parse
}

var (
	sessionIDCounter atomic.Uint64
)

// NewProxySession creates a new proxy session.
func NewProxySession(clientConn net.Conn, p *pool.Pool, codec common.Codec, userDB *auth.UserDatabase, cfg *config.PoolConfig, cacheStore *cache.Store, cacheRules *cache.RulesEngine, queryLogger *logger.QueryLogger, transactionMgr *pool.TransactionManager, authLimiter *auth.AuthLimiter, router *pool.Router, tlsConfig *tls.Config, log *logger.Logger) (*ProxySession, error) {
	// Create pool strategy
	strategy, err := pool.DefaultStrategyFactory.CreateStrategy(p)
	if err != nil {
		return nil, fmt.Errorf("failed to create strategy: %w", err)
	}

	// Create pool session
	poolSession := pool.NewSession(p, strategy)

	ps := &ProxySession{
		id:             sessionIDCounter.Add(1),
		clientConn:     clientConn,
		pool:           p,
		codec:          codec,
		userDB:         userDB,
		config:         cfg,
		poolSession:    poolSession,
		relay:          NewRelay(),
		cacheStore:     cacheStore,
		cacheRules:     cacheRules,
		queryLogger:    queryLogger,
		transactionMgr: transactionMgr,
		authLimiter:    authLimiter,
		router:         router,
		stmtRepreparer: stmt.NewTransparentRepreparer(stmt.NewManager(1000)),
		tlsConfig:      tlsConfig,
		log:            log,
	}

	return ps, nil
}

// ID returns the session ID.
func (ps *ProxySession) ID() uint64 {
	return ps.id
}

// QueryCount returns the query count.
func (ps *ProxySession) QueryCount() int64 {
	return ps.queryCount.Load()
}

// Handle processes the client connection.
func (ps *ProxySession) Handle(ctx context.Context) {
	defer func() {
		// Decrement per-user connection count (NEW fix)
		if ps.username != "" {
			ps.pool.DecrementUserCount(ps.username)
		}
		if err := ps.poolSession.Strategy().OnClientDisconnect(ps.poolSession); err != nil {
			ps.log.Error("Strategy disconnect error", "error", err)
		}
		if ps.serverConn != nil {
			ps.pool.Release(ps.serverConn)
			ps.serverConn = nil
		}
	}()

	// Handle startup/authentication
	if err := ps.handleStartup(ctx); err != nil {
		ps.log.Error("Startup failed", "error", err)
		return
	}

	// Call strategy connect handler
	if err := ps.poolSession.Strategy().OnClientConnect(ctx, ps.poolSession); err != nil {
		ps.log.Error("Strategy connect error", "error", err)
		return
	}

	// Relay messages between client and server
	ps.relay.Run(ctx, ps.clientConn, ps.poolSession, ps.codec, ps)
}

// handleStartup handles the initial startup/authentication phase.
func (ps *ProxySession) handleStartup(ctx context.Context) error {
	switch ps.config.Body {
	case "postgresql":
		return ps.handlePostgreSQLStartup(ctx)
	case "mysql":
		return ps.handleMySQLStartup(ctx)
	case "mssql":
		return ps.handleMSSQLStartup(ctx)
	default:
		return fmt.Errorf("unsupported body type: %s", ps.config.Body)
	}
}

// handlePostgreSQLStartup handles PostgreSQL startup handshake.
func (ps *ProxySession) handlePostgreSQLStartup(ctx context.Context) error {
	reader := bufio.NewReader(ps.clientConn)

	// Read startup message length
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(reader, lenBuf); err != nil {
		return fmt.Errorf("failed to read startup length: %w", err)
	}
	length := binary.BigEndian.Uint32(lenBuf)

	if length < 8 || length > 10000 {
		return fmt.Errorf("invalid startup message length: %d", length)
	}

	// Read the rest of the startup message
	startupData := make([]byte, length-4)
	if _, err := io.ReadFull(reader, startupData); err != nil {
		return fmt.Errorf("failed to read startup data: %w", err)
	}

	// Check for SSL request
	if length == 8 {
		code := binary.BigEndian.Uint32(startupData)
		if code == 80877103 {
			// SSL Request
			if ps.config.TLS.Mode != "disable" && ps.tlsConfig != nil {
				// Send 'S' to indicate SSL supported
				if _, err := ps.clientConn.Write([]byte{'S'}); err != nil {
					return fmt.Errorf("failed to send SSL response: %w", err)
				}
				// Wrap connection with TLS
				tlsConn := tls.Server(ps.clientConn, ps.tlsConfig)
				if err := tlsConn.HandshakeContext(ctx); err != nil {
					return fmt.Errorf("TLS handshake failed: %w", err)
				}
				ps.clientConn = tlsConn

				// Check for client certificate authentication
				if err := ps.authenticateWithCertificate(); err != nil {
					return err
				}
			} else {
				// TLS mode is "require", "verify-ca", or "verify-full" - SSL must be used
				if ps.config.TLS.Mode == "require" || ps.config.TLS.Mode == "verify-ca" || ps.config.TLS.Mode == "verify-full" {
					// Client rejected SSL upgrade - close connection since TLS is mandatory
					ps.log.Warn("Client rejected SSL when TLS is required", "client", ps.clientConn.RemoteAddr())
					ps.clientConn.Close()
					return fmt.Errorf("TLS required but client refused SSL upgrade")
				}
				// Send 'N' to indicate SSL not supported
				if _, err := ps.clientConn.Write([]byte{'N'}); err != nil {
					return fmt.Errorf("failed to send SSL rejection: %w", err)
				}
			}
			// Read actual startup message
			return ps.handlePostgreSQLStartup(ctx)
		}
	}

	// Parse startup parameters
	protoVersion := binary.BigEndian.Uint32(startupData[0:4])
	if protoVersion != 196608 {
		return fmt.Errorf("unsupported protocol version: %d", protoVersion)
	}

	// Parse key-value parameters
	params := make(map[string]string)
	pos := 4
	paramCount := 0
	const maxStartupParams = 64
	const maxValueLen = 256
	for pos < len(startupData)-1 {
		if paramCount >= maxStartupParams {
			return fmt.Errorf("too many startup parameters (max %d)", maxStartupParams)
		}
		// Find null terminator for key
		keyStart := pos
		for pos < len(startupData) && startupData[pos] != 0 {
			pos++
		}
		if pos >= len(startupData) {
			break
		}
		key := string(startupData[keyStart:pos])
		pos++ // skip null

		// Find null terminator for value
		valStart := pos
		for pos < len(startupData) && startupData[pos] != 0 {
			pos++
		}
		if pos >= len(startupData) {
			break
		}
		val := string(startupData[valStart:pos])
		pos++ // skip null

		// Validate value length
		if len(val) > maxValueLen {
			return fmt.Errorf("startup parameter %q value exceeds max length (%d bytes)", key, maxValueLen)
		}

		if key != "" {
			params[key] = val
			paramCount++
		}
	}

	ps.username = params["user"]
	ps.database = params["database"]

	// Validate username and database for null bytes and control characters
	for _, s := range []string{ps.username, ps.database} {
		for i := 0; i < len(s); i++ {
			if s[i] < 0x20 || s[i] == 0x7F {
				return fmt.Errorf("invalid character in startup parameter value")
			}
		}
	}

	if ps.username == "" {
		return fmt.Errorf("no username provided")
	}

	// Check if user exists
	user := ps.userDB.GetUser(ps.username)
	if user == nil {
		// Send error and close
		errMsg := postgresql.CreateErrorResponse("28P01", "authentication failed")
		if _, werr := ps.clientConn.Write(errMsg); werr != nil {
			return fmt.Errorf("unknown user: %s, failed to send error: %w", ps.username, werr)
		}
		// Add artificial delay to prevent username enumeration via timing attack (H-5 fix)
		time.Sleep(50 * time.Millisecond)
		return fmt.Errorf("unknown user: %s", ps.username)
	}

	// Check per-user connection limit (NEW fix)
	if user.MaxConnections > 0 {
		if !ps.pool.TryIncrementUserCount(ps.username, user.MaxConnections) {
			ps.log.Warn("Per-user connection limit exceeded", "user", ps.username, "limit", user.MaxConnections)
			errMsg := postgresql.CreateErrorResponse("28P01", "too many connections for user")
			if _, werr := ps.clientConn.Write(errMsg); werr != nil {
				return fmt.Errorf("user %s connection limit exceeded, failed to send error: %w", ps.username, werr)
			}
			return fmt.Errorf("too many connections for user %s", ps.username)
		}
	}

	// Check pool access authorization
	if !user.CanAccessPool(ps.pool.Name()) {
		ps.log.Warn("Pool access denied", "user", ps.username, "pool", ps.pool.Name())
		errMsg := postgresql.CreateErrorResponse("28000", "access to pool denied")
		if _, werr := ps.clientConn.Write(errMsg); werr != nil {
			return fmt.Errorf("access denied for user %s to pool %s, failed to send error: %w", ps.username, ps.pool.Name(), werr)
		}
		return fmt.Errorf("access denied for user %s to pool %s", ps.username, ps.pool.Name())
	}

	// Handle authentication based on auth mode (M-1 fix)
	if ps.authMode == "passthrough" {
		// Passthrough: connect to backend and let it handle client authentication
		return ps.connectToBackend(ctx)
	}
	// Interception (default): proxy handles client authentication via SCRAM
	return ps.handlePostgreSQLAuth(ctx, user)
}

// handlePostgreSQLAuth handles PostgreSQL authentication.
func (ps *ProxySession) handlePostgreSQLAuth(ctx context.Context, user *auth.User) error {
	// Check auth rate limiter before starting
	clientIP := ps.clientConn.RemoteAddr().String()
	if ps.authLimiter != nil && ps.authLimiter.IsLimited(clientIP) {
		ps.log.Warn("Authentication blocked: client rate limited", "client", clientIP)
		errMsg := postgresql.CreateErrorResponse("28P01", "too many failed attempts, try again later")
		if _, werr := ps.clientConn.Write(errMsg); werr != nil {
			return fmt.Errorf("client %s is rate limited (failed to send error: %w)", clientIP, werr)
		}
		return fmt.Errorf("client %s is rate limited", clientIP)
	}

	scramServer := auth.NewSCRAMServer(ps.userDB)

	// Send AuthenticationSASL
	saslMsg := postgresql.CreateAuthenticationSCRAM()
	if _, err := ps.clientConn.Write(saslMsg); err != nil {
		return fmt.Errorf("failed to send SASL auth request: %w", err)
	}

	// Read SASLInitialResponse
	reader := bufio.NewReader(ps.clientConn)
	msgType, err := reader.ReadByte()
	if err != nil {
		return fmt.Errorf("failed to read SASL response type: %w", err)
	}
	if msgType != 'p' {
		return fmt.Errorf("expected password message, got: %c", msgType)
	}

	// Read length
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(reader, lenBuf); err != nil {
		return fmt.Errorf("failed to read SASL response length: %w", err)
	}
	respLen := binary.BigEndian.Uint32(lenBuf)

	// Read mechanism and data
	respData := make([]byte, respLen-4)
	if _, err := io.ReadFull(reader, respData); err != nil {
		return fmt.Errorf("failed to read SASL response data: %w", err)
	}

	// Parse mechanism (null-terminated)
	mechEnd := 0
	for mechEnd < len(respData) && respData[mechEnd] != 0 {
		mechEnd++
	}
	mechanism := string(respData[:mechEnd])
	if mechanism != "SCRAM-SHA-256" {
		return fmt.Errorf("unsupported mechanism: %s", mechanism)
	}

	// Read client data length and data
	clientDataStart := mechEnd + 1
	clientDataLen := binary.BigEndian.Uint32(respData[clientDataStart : clientDataStart+4])
	clientFirst := string(respData[clientDataStart+4 : clientDataStart+4+int(clientDataLen)])

	// Parse client-first
	state, err := scramServer.ParseClientFirst(clientFirst)
	if err != nil {
		ps.recordAuthFailure()
		errMsg := postgresql.CreateErrorResponse("28P01", "authentication failed")
		if _, werr := ps.clientConn.Write(errMsg); werr != nil {
			return fmt.Errorf("authentication failed: %w (failed to send error: %v)", err, werr)
		}
		return err
	}
	ps.scramState = state

	// Generate server-first
	serverFirst, err := scramServer.GenerateServerFirst(state)
	if err != nil {
		ps.recordAuthFailure()
		errMsg := postgresql.CreateErrorResponse("28P01", "authentication failed")
		if _, werr := ps.clientConn.Write(errMsg); werr != nil {
			return fmt.Errorf("authentication failed: %w (failed to send error: %v)", err, werr)
		}
		return err
	}

	// Send AuthenticationSASLContinue
	contMsg := postgresql.CreateAuthenticationSASLContinue([]byte(serverFirst))
	if _, err := ps.clientConn.Write(contMsg); err != nil {
		return fmt.Errorf("failed to send SASL continue: %w", err)
	}

	// Read client-final
	msgType, err = reader.ReadByte()
	if err != nil {
		return fmt.Errorf("failed to read client final type: %w", err)
	}
	if msgType != 'p' {
		return fmt.Errorf("expected password message, got: %c", msgType)
	}

	// Read length
	lenBuf = make([]byte, 4)
	if _, err := io.ReadFull(reader, lenBuf); err != nil {
		return fmt.Errorf("failed to read client final length: %w", err)
	}
	finalLen := binary.BigEndian.Uint32(lenBuf)

	// Read client final data
	finalData := make([]byte, finalLen-4)
	if _, err := io.ReadFull(reader, finalData); err != nil {
		return fmt.Errorf("failed to read client final data: %w", err)
	}

	// Verify client-final
	ok, err := scramServer.VerifyClientFinal(state, string(finalData))
	if err != nil || !ok {
		ps.recordAuthFailure()
		errMsg := postgresql.CreateErrorResponse("28P01", "authentication failed: invalid password")
		if _, werr := ps.clientConn.Write(errMsg); werr != nil {
			return fmt.Errorf("authentication failed: %w (failed to send error: %v)", err, werr)
		}
		return fmt.Errorf("authentication failed: %w", err)
	}

	// Send AuthenticationSASLFinal
	serverFinal := scramServer.GenerateServerFinal(state)
	finalAuthMsg := postgresql.CreateAuthenticationSASLFinal([]byte(serverFinal))
	if _, err := ps.clientConn.Write(finalAuthMsg); err != nil {
		return fmt.Errorf("failed to send SASL final: %w", err)
	}

	// Send AuthenticationOK
	authOk := postgresql.CreateAuthenticationOk()
	if _, err := ps.clientConn.Write(authOk); err != nil {
		return fmt.Errorf("failed to send auth ok: %w", err)
	}

	// Send ParameterStatus messages
	params := []struct{ name, value string }{
		{"server_version", "14.0 (Geryon)"},
		{"server_encoding", "UTF8"},
		{"client_encoding", "UTF8"},
		{"DateStyle", "ISO, MDY"},
		{"TimeZone", "UTC"},
		{"is_superuser", "off"},
	}

	for _, p := range params {
		paramMsg := postgresql.CreateParameterStatus(p.name, p.value)
		if _, err := ps.clientConn.Write(paramMsg); err != nil {
			return fmt.Errorf("failed to send parameter status: %w", err)
		}
	}

	// Send ReadyForQuery
	readyMsg := postgresql.CreateReadyForQuery('I')
	if _, err := ps.clientConn.Write(readyMsg); err != nil {
		return fmt.Errorf("failed to send ready for query: %w", err)
	}

	ps.authenticated.Store(true)
	ps.recordAuthSuccess()
	ps.poolSession.SetAuthDone()

	// Connect to backend
	return ps.connectToBackend(ctx)
}

// connectToBackend establishes connection to the backend server.
func (ps *ProxySession) connectToBackend(ctx context.Context) error {
	// Connect using strategy - this assigns server conn to session
	if err := ps.poolSession.Strategy().OnClientConnect(ctx, ps.poolSession); err != nil {
		return fmt.Errorf("failed to connect to backend: %w", err)
	}

	// Get the server connection from session
	serverConn := ps.poolSession.ServerConn()
	if serverConn == nil {
		return fmt.Errorf("no server connection available")
	}

	ps.serverConn = serverConn

	// Determine backend auth credentials
	// In interception mode, use BackendAuth credentials if configured
	// In passthrough mode, forward client's username
	backendUsername := ps.username
	backendPassword := ""

	if ps.authMode == "interception" && ps.config != nil && ps.config.Backend.Auth.Username != "" {
		backendUsername = ps.config.Backend.Auth.Username
		// Load password from file if specified
		if ps.config.Backend.Auth.PasswordFile != "" {
			passwordBytes, err := os.ReadFile(ps.config.Backend.Auth.PasswordFile)
			if err != nil {
				return fmt.Errorf("failed to read backend password file: %w", err)
			}
			backendPassword = strings.TrimSpace(string(passwordBytes))
			// M-11 fix: zero the buffer after use to reduce memory lifetime
			for i := range passwordBytes {
				passwordBytes[i] = 0
			}
		}
	}

	if backendUsername != "" {
		// Send startup message to backend
		startup := ps.codec.(*postgresql.PGCodec).CreateStartupMessage(backendUsername, ps.database)
		if _, err := serverConn.Conn().Write(startup); err != nil {
			return fmt.Errorf("failed to send startup to backend: %w", err)
		}

		// Handle backend authentication
		if backendPassword != "" {
			// Proxy has backend password - handle auth directly
			if err := ps.authenticateToBackend(serverConn, backendUsername, backendPassword); err != nil {
				return fmt.Errorf("backend authentication failed: %w", err)
			}
		} else {
			// Check rate limiter before forwarding to backend (M-4 fix)
			clientIP := ps.clientConn.RemoteAddr().String()
			if ps.authLimiter != nil && ps.authLimiter.IsLimited(clientIP) {
				ps.log.Warn("Authentication blocked: client rate limited", "client", clientIP)
				return fmt.Errorf("too many failed attempts, try again later")
			}
			// No password - forward authentication to backend
			if err := ps.forwardAuthFromBackend(); err != nil {
				return fmt.Errorf("backend authentication failed: %w", err)
			}
		}
	}

	return nil
}

// authenticateToBackend performs password authentication with the backend server.
// Supports MD5 authentication. SCRAM-SHA-256 requires additional implementation.
func (ps *ProxySession) authenticateToBackend(serverConn *pool.ServerConn, username, password string) error {
	pgCodec := ps.codec.(*postgresql.PGCodec)
	reader := bufio.NewReader(serverConn.Conn())

	for {
		// Read message type
		msgType, err := reader.ReadByte()
		if err != nil {
			return fmt.Errorf("failed to read auth message: %w", err)
		}

		switch msgType {
		case 'R':
			// Authentication request
			lenBuf := make([]byte, 4)
			if _, err := io.ReadFull(reader, lenBuf); err != nil {
				return fmt.Errorf("failed to read auth length: %w", err)
			}
			length := binary.BigEndian.Uint32(lenBuf)
			payloadLen := int(length) - 4

			payload := make([]byte, payloadLen)
			if payloadLen > 0 {
				if _, err := io.ReadFull(reader, payload); err != nil {
					return fmt.Errorf("failed to read auth payload: %w", err)
				}
			}

			authType := binary.BigEndian.Uint32(payload[0:4])

			switch authType {
			case 5: // MD5
				// Read salt
				salt := [4]byte{}
				copy(salt[:], payload[4:8])
				hash := postgresql.MD5PasswordHash(username, password, salt)
				// Send password message
				passMsg := pgCodec.CreatePasswordMessage(hash)
				if _, err := serverConn.Conn().Write(passMsg); err != nil {
					return fmt.Errorf("failed to send password: %w", err)
				}

			case 10: // SASL (SCRAM-SHA-256)
				// For SCRAM, we need to forward to client since we don't have SCRAM client impl
				// This is a limitation - SCRAM backend auth needs full client implementation
				saslData := payload[4:]
				// Parse mechanisms
				mechEnd := 0
				for mechEnd < len(saslData) && saslData[mechEnd] != 0 {
					mechEnd++
				}
				mechanism := string(saslData[:mechEnd])

				// Build and send SCRAM client first message
				scramClient := auth.NewSCRAMClient(username, password, mechanism)
				clientFirst := scramClient.BuildClientFirst(username)

				// Send SASLInitialResponse
				resp := pgCodec.CreateSCRAMResponse(mechanism, []byte(clientFirst))
				if _, err := serverConn.Conn().Write(resp); err != nil {
					return fmt.Errorf("failed to send SCRAM initial: %w", err)
				}

				// Process auth cycle
				if err := ps.processSCRAMAuthCycle(reader, serverConn, scramClient); err != nil {
					return err
				}
				return nil

			case 0: // AuthenticationOK
				return nil

			default:
				return fmt.Errorf("unsupported auth type: %d", authType)
			}

		case 'E':
			// ErrorResponse
			return ps.forwardErrorResponse(reader)

		case 'Z':
			// ReadyForQuery - auth complete
			return nil

		default:
			return fmt.Errorf("unexpected message during auth: %c", msgType)
		}
	}
}

// processSCRAMAuthCycle handles SCRAM authentication round trips.
func (ps *ProxySession) processSCRAMAuthCycle(reader *bufio.Reader, serverConn *pool.ServerConn, scramClient *auth.SCRAMClient) error {
	pgCodec := ps.codec.(*postgresql.PGCodec)

	for {
		msgType, err := reader.ReadByte()
		if err != nil {
			return fmt.Errorf("failed to read SASL message: %w", err)
		}

		if msgType == 'R' {
			lenBuf := make([]byte, 4)
			if _, err := io.ReadFull(reader, lenBuf); err != nil {
				return fmt.Errorf("failed to read SASL length: %w", err)
			}
			length := binary.BigEndian.Uint32(lenBuf)
			payloadLen := int(length) - 4

			payload := make([]byte, payloadLen)
			if payloadLen > 0 {
				if _, err := io.ReadFull(reader, payload); err != nil {
					return fmt.Errorf("failed to read SASL payload: %w", err)
				}
			}

			authType := binary.BigEndian.Uint32(payload[0:4])

			if authType == 11 { // SASLContinue
				serverData := payload[4:]
				clientFinal := scramClient.BuildClientFinal(string(serverData))

				// Send SASLResponse
				resp := pgCodec.CreateSCRAMResponse("SCRAM-SHA-256", []byte(clientFinal))
				if _, err := serverConn.Conn().Write(resp); err != nil {
					return fmt.Errorf("failed to send SCRAM final: %w", err)
				}
			} else if authType == 12 { // SASLFinal
				serverData := payload[4:]
				if !scramClient.VerifyServerFinal(string(serverData)) {
					return fmt.Errorf("server final verification failed")
				}
				// Authentication successful
				return nil
			}
		} else if msgType == 'E' {
			return ps.forwardErrorResponse(reader)
		} else if msgType == 'Z' {
			// ReadyForQuery
			return nil
		}
	}
}

// forwardErrorResponse reads and forwards an error response.
func (ps *ProxySession) forwardErrorResponse(reader *bufio.Reader) error {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(reader, lenBuf); err != nil {
		return fmt.Errorf("failed to read error length: %w", err)
	}
	length := binary.BigEndian.Uint32(lenBuf)
	payloadLen := int(length) - 4
	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(reader, payload); err != nil {
			return fmt.Errorf("failed to read error payload: %w", err)
		}
	}
	msg := make([]byte, 1+4+payloadLen)
	msg[0] = 'E'
	copy(msg[1:5], lenBuf)
	copy(msg[5:], payload)
	if _, err := ps.clientConn.Write(msg); err != nil {
		return fmt.Errorf("failed to forward error: %w", err)
	}
	return fmt.Errorf("backend error")
}

// forwardAuthFromBackend forwards authentication messages from backend to client.
func (ps *ProxySession) forwardAuthFromBackend() error {
	reader := bufio.NewReader(ps.serverConn.Conn())

	for {
		// Read message type
		msgType, err := reader.ReadByte()
		if err != nil {
			return err
		}

		// Read length
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(reader, lenBuf); err != nil {
			return err
		}
		length := binary.BigEndian.Uint32(lenBuf)

		// Read payload
		payloadLen := int(length) - 4
		if payloadLen < 0 || payloadLen > maxMySQLPayload {
			return fmt.Errorf("invalid backend message length: %d", payloadLen)
		}
		payload := make([]byte, payloadLen)
		if payloadLen > 0 {
			if _, err := io.ReadFull(reader, payload); err != nil {
				return err
			}
		}

		// Construct full message
		msg := make([]byte, 1+4+payloadLen)
		msg[0] = msgType
		copy(msg[1:5], lenBuf)
		copy(msg[5:], payload)

		// Forward to client
		if _, err := ps.clientConn.Write(msg); err != nil {
			return err
		}

		// Check for AuthenticationOK
		if msgType == 'R' {
			authType := binary.BigEndian.Uint32(payload[0:4])
			if authType == 0 {
				// AuthenticationOK
				ps.authenticated.Store(true)
				ps.recordAuthSuccess()
				ps.poolSession.SetAuthDone()
				// Continue forwarding until ReadyForQuery
				continue
			}
		}

		// Check for ReadyForQuery
		if msgType == 'Z' {
			return nil
		}

		// For auth requests, we need to forward client response
		if msgType == 'R' {
			authType := binary.BigEndian.Uint32(payload[0:4])
			if authType != 0 { // Not OK, need client response
				if err := ps.forwardAuthToBackend(); err != nil {
					return err
				}
			}
		}
	}
}

// forwardAuthToBackend forwards authentication response from client to backend.
func (ps *ProxySession) forwardAuthToBackend() error {
	reader := bufio.NewReader(ps.clientConn)

	// Read message type
	msgType, err := reader.ReadByte()
	if err != nil {
		return err
	}

	// Read length
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(reader, lenBuf); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(lenBuf)

	// Read payload
	payloadLen := int(length) - 4
	if payloadLen < 0 || payloadLen > maxMySQLPayload {
		return fmt.Errorf("invalid client message length: %d", payloadLen)
	}
	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(reader, payload); err != nil {
			return err
		}
	}

	// Construct full message
	msg := make([]byte, 1+4+payloadLen)
	msg[0] = msgType
	copy(msg[1:5], lenBuf)
	copy(msg[5:], payload)

	// Forward to backend
	_, err = ps.serverConn.Conn().Write(msg)
	return err
}

// handleMySQLStartup handles MySQL startup handshake.
func (ps *ProxySession) handleMySQLStartup(ctx context.Context) error {
	// Check rate limiter before starting MySQL authentication (M-4 fix)
	clientIP := ps.clientConn.RemoteAddr().String()
	if ps.authLimiter != nil && ps.authLimiter.IsLimited(clientIP) {
		ps.log.Warn("Authentication blocked: client rate limited", "client", clientIP)
		return fmt.Errorf("too many failed attempts, try again later")
	}

	// For MySQL auth, check if we're in interception mode with user DB
	// In interception mode, we read client handshake first to get username
	// In passthrough mode, we connect to backend and let it handle auth
	useInterception := ps.authMode == "interception" && ps.userDB != nil

	if useInterception {
		// Interception: read client handshake first to get username
		username, err := ps.readMySQLClientHandshake()
		if err != nil {
			return fmt.Errorf("failed to read client handshake: %w", err)
		}

		ps.username = username
		user := ps.userDB.GetUser(ps.username)

		return ps.handleMySQLInterception(ctx, user, clientIP)
	}

	// Passthrough or default: connect to backend first, forward all auth
	return ps.handleMySQLPassthrough(ctx)
}

// handleMySQLPassthrough handles MySQL passthrough authentication.
// Connects to backend and forwards all auth messages.
func (ps *ProxySession) handleMySQLPassthrough(ctx context.Context) error {
	// Connect to backend first to get the handshake
	if err := ps.poolSession.Strategy().OnClientConnect(ctx, ps.poolSession); err != nil {
		return fmt.Errorf("failed to connect to backend: %w", err)
	}

	serverConn := ps.poolSession.ServerConn()
	if serverConn == nil {
		return fmt.Errorf("no server connection available")
	}

	ps.serverConn = serverConn

	// Read handshake from backend
	reader := bufio.NewReader(serverConn.Conn())

	// Read packet header (4 bytes)
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return fmt.Errorf("failed to read handshake header: %w", err)
	}

	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	_ = header[3] // sequence

	// Read handshake packet
	if length > maxMySQLPayload {
		return fmt.Errorf("mysql handshake too large: %d bytes", length)
	}
	handshakeData := make([]byte, length)
	if _, err := io.ReadFull(reader, handshakeData); err != nil {
		return fmt.Errorf("failed to read handshake data: %w", err)
	}

	// Parse handshake to get scramble
	scramble, err := extractMySQLScramble(handshakeData)
	if err != nil {
		return fmt.Errorf("failed to extract scramble: %w", err)
	}

	// Modify handshake with our server info
	ourHandshake := createMySQLHandshake(ps.id, scramble)

	// Send to client
	pkt := make([]byte, 4+len(ourHandshake))
	copy(pkt[0:4], header)
	copy(pkt[4:], ourHandshake)
	pkt[3] = 0 // Reset sequence number

	if _, err := ps.clientConn.Write(pkt); err != nil {
		return fmt.Errorf("failed to send handshake: %w", err)
	}

	// Read handshake response from client
	respHeader := make([]byte, 4)
	if _, err := io.ReadFull(ps.clientConn, respHeader); err != nil {
		return fmt.Errorf("failed to read response header: %w", err)
	}

	respLength := int(respHeader[0]) | int(respHeader[1])<<8 | int(respHeader[2])<<16
	if respLength > maxMySQLPayload {
		return fmt.Errorf("mysql handshake response too large: %d bytes", respLength)
	}
	respData := make([]byte, respLength)
	if _, err := io.ReadFull(ps.clientConn, respData); err != nil {
		return fmt.Errorf("failed to read response data: %w", err)
	}

	// Parse response to get username/database
	username, database, err := parseMySQLHandshakeResponse(respData)
	if err != nil {
		return fmt.Errorf("failed to parse handshake response: %w", err)
	}

	ps.username = username
	ps.database = database

	// Check pool access authorization for MySQL (H-1 fix)
	if ps.userDB != nil {
		if user := ps.userDB.GetUser(ps.username); user != nil && !user.CanAccessPool(ps.pool.Name()) {
			ps.log.Warn("Pool access denied", "user", ps.username, "pool", ps.pool.Name())
			return fmt.Errorf("access denied for user %s to pool %s", ps.username, ps.pool.Name())
		}
	}

	// Forward response to backend (adjusted sequence)
	respPkt := make([]byte, 4+respLength)
	copy(respPkt[0:4], respHeader)
	respPkt[3] = 1 // Sequence should be 1 for client response
	copy(respPkt[4:], respData)

	if _, err := serverConn.Conn().Write(respPkt); err != nil {
		return fmt.Errorf("failed to forward response: %w", err)
	}

	// Forward remaining auth packets until OK or error
	if err := ps.forwardMySQLAuth(); err != nil {
		return fmt.Errorf("mysql auth failed: %w", err)
	}

	ps.authenticated.Store(true)
	ps.recordAuthSuccess()
	ps.poolSession.SetAuthDone()

	return nil
}

// handleMySQLInterception handles MySQL interception authentication.
// Geryon authenticates the client using its own user database,
// then connects to backend with pooled credentials.
func (ps *ProxySession) handleMySQLInterception(ctx context.Context, user *auth.User, clientIP string) error {
	// Verify user exists in Geryon database
	if user == nil {
		ps.log.Warn("Unknown user in interception mode", "user", ps.username)
		ps.recordAuthFailure()
		return fmt.Errorf("unknown user: %s", ps.username)
	}

	// Check per-user connection limit
	if user.MaxConnections > 0 {
		if !ps.pool.TryIncrementUserCount(ps.username, user.MaxConnections) {
			ps.log.Warn("Per-user connection limit exceeded", "user", ps.username, "limit", user.MaxConnections)
			ps.recordAuthFailure()
			return fmt.Errorf("too many connections for user %s", ps.username)
		}
	}

	// Check pool access authorization
	if !user.CanAccessPool(ps.pool.Name()) {
		ps.log.Warn("Pool access denied", "user", ps.username, "pool", ps.pool.Name())
		ps.recordAuthFailure()
		return fmt.Errorf("access denied for user %s to pool %s", ps.username, ps.pool.Name())
	}

	// Generate random scramble for challenge-response auth
	scramble := make([]byte, 20)
	if _, err := rand.Read(scramble); err != nil {
		ps.recordAuthFailure()
		return fmt.Errorf("failed to generate scramble: %w", err)
	}

	// Send our handshake to client (server speaks first in MySQL protocol)
	handshake := createMySQLHandshake(ps.id, scramble)
	pkt := make([]byte, 4+len(handshake))
	binary.LittleEndian.PutUint32(pkt[0:4], uint32(len(handshake)))
	pkt[4] = 0 // sequence number 0
	copy(pkt[5:], handshake)

	if _, err := ps.clientConn.Write(pkt); err != nil {
		ps.recordAuthFailure()
		return fmt.Errorf("failed to send handshake: %w", err)
	}

	// Read client handshake response
	reader := bufio.NewReader(ps.clientConn)

	// Read packet header (4 bytes)
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		ps.recordAuthFailure()
		return fmt.Errorf("failed to read handshake header: %w", err)
	}

	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	if length > maxMySQLPayload {
		ps.recordAuthFailure()
		return fmt.Errorf("handshake response too large: %d bytes", length)
	}

	// Read handshake payload
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		ps.recordAuthFailure()
		return fmt.Errorf("failed to read handshake payload: %w", err)
	}

	// Parse to get username and auth response
	username, authResponse, err := parseMySQLHandshakeResponseWithAuth(payload)
	if err != nil {
		ps.recordAuthFailure()
		return fmt.Errorf("failed to parse handshake response: %w", err)
	}

	// Update username if different from initial read
	if username != "" {
		ps.username = username
	}

	// Verify password if we have MySQL password hash stored
	if user.MysqlPasswordHash != "" {
		if err := auth.VerifyMySQLPassword(user.MysqlPasswordHash, scramble, authResponse); err != nil {
			ps.log.Warn("MySQL password verification failed", "user", ps.username, "error", err)
			ps.recordAuthFailure()
			// Send error packet to client
			errPkt := createMySQLErrorPacket(1045, "28000", "Access denied")
			ps.clientConn.Write(errPkt)
			return fmt.Errorf("authentication failed: %w", err)
		}
	} else if user.PasswordHash != "" {
		// Fall back to SCRAM-SHA-256 (treat as PostgreSQL format)
		ps.log.Warn("No MySQL password hash, using SCRAM fallback", "user", ps.username)
		ps.recordAuthFailure()
		errPkt := createMySQLErrorPacket(1045, "28000", "MySQL password not configured")
		ps.clientConn.Write(errPkt)
		return fmt.Errorf("MySQL password hash required for interception mode")
	} else {
		ps.log.Warn("No password hash available for user", "user", ps.username)
		ps.recordAuthFailure()
		errPkt := createMySQLErrorPacket(1045, "28000", "Access denied")
		ps.clientConn.Write(errPkt)
		return fmt.Errorf("no password hash available for user %s", ps.username)
	}

	// Send OK packet to client
	okPacket := createMySQLOKPacket()
	if _, err := ps.clientConn.Write(okPacket); err != nil {
		ps.recordAuthFailure()
		return fmt.Errorf("failed to send auth response: %w", err)
	}

	// Now connect to backend with pooled credentials
	if err := ps.poolSession.Strategy().OnClientConnect(ctx, ps.poolSession); err != nil {
		ps.recordAuthFailure()
		return fmt.Errorf("failed to connect to backend: %w", err)
	}

	serverConn := ps.poolSession.ServerConn()
	if serverConn == nil {
		ps.recordAuthFailure()
		return fmt.Errorf("no server connection available")
	}

	ps.serverConn = serverConn
	ps.authenticated.Store(true)
	ps.recordAuthSuccess()
	ps.poolSession.SetAuthDone()

	return nil
}

// readMySQLClientHandshake reads and parses the initial handshake from MySQL client.
func (ps *ProxySession) readMySQLClientHandshake() (string, error) {
	reader := bufio.NewReader(ps.clientConn)

	// Read packet header (4 bytes)
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return "", fmt.Errorf("failed to read handshake header: %w", err)
	}

	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	_ = header[3] // sequence

	if length > maxMySQLPayload {
		return "", fmt.Errorf("mysql handshake too large: %d bytes", length)
	}

	handshakeData := make([]byte, length)
	if _, err := io.ReadFull(reader, handshakeData); err != nil {
		return "", fmt.Errorf("failed to read handshake data: %w", err)
	}

	// Parse username from handshake
	username, _, err := parseMySQLHandshakeResponse(handshakeData)
	if err != nil {
		return "", fmt.Errorf("failed to parse handshake: %w", err)
	}

	return username, nil
}

// createMySQLOKPacket creates a MySQL OK packet for authentication completion.
func createMySQLOKPacket() []byte {
	// OK packet: 0x00 (header) + affected rows (1) + inserted ID (4) + server status (2) + warnings (2)
	pkt := make([]byte, 11)
	pkt[0] = 0x00 // OK packet type
	// Fields already zeroed
	pkt[5] = 0x02 // server status: autocommit
	return pkt
}

// extractMySQLScramble extracts the auth scramble from handshake packet.
func extractMySQLScramble(data []byte) ([]byte, error) {
	if len(data) < 10 {
		return nil, fmt.Errorf("handshake too short")
	}

	// Protocol version (1 byte)
	protoVersion := data[0]
	if protoVersion != 10 {
		return nil, fmt.Errorf("unsupported protocol version: %d", protoVersion)
	}

	// Skip server version (null-terminated)
	pos := 1
	for pos < len(data) && data[pos] != 0 {
		pos++
	}
	pos++ // skip null

	// Skip connection ID (4 bytes)
	pos += 4

	// Auth data part 1 (8 bytes)
	if pos+8 > len(data) {
		return nil, fmt.Errorf("handshake too short for auth part 1")
	}
	scramble := make([]byte, 0, 20)
	scramble = append(scramble, data[pos:pos+8]...)
	pos += 8

	// Skip filler (1 byte)
	pos++

	// Skip capability flags lower (2 bytes), charset (1 byte), status (2 bytes)
	pos += 5

	// Check if we have more capability flags
	if pos+2 > len(data) {
		return scramble, nil // Old protocol, only 8 bytes
	}

	// Skip capability flags upper (2 bytes)
	pos += 2

	// Auth data length (1 byte) - at least 21 bytes total
	authLen := data[pos]
	pos++

	// Skip reserved (10 bytes)
	pos += 10

	// Auth data part 2 (remaining bytes up to 12)
	part2Len := int(authLen) - 8
	if part2Len > 12 {
		part2Len = 12
	}
	if pos+part2Len > len(data) {
		part2Len = len(data) - pos
	}
	if part2Len > 0 {
		scramble = append(scramble, data[pos:pos+part2Len]...)
	}

	return scramble[:20], nil // Return exactly 20 bytes
}

// createMySQLHandshake creates a handshake packet with our server info.
func createMySQLHandshake(connID uint64, scramble []byte) []byte {
	version := "5.7.42-geryon"
	buf := make([]byte, 0, 128)

	// Protocol version
	buf = append(buf, 10)

	// Server version
	buf = append(buf, []byte(version)...)
	buf = append(buf, 0)

	// Connection ID
	buf = binary.LittleEndian.AppendUint32(buf, uint32(connID))

	// Auth data part 1 (8 bytes)
	if len(scramble) >= 8 {
		buf = append(buf, scramble[:8]...)
	} else {
		buf = append(buf, make([]byte, 8)...)
	}

	// Filler
	buf = append(buf, 0)

	// Capability flags lower (CLIENT_PROTOCOL_41 | CLIENT_SECURE_CONNECTION | CLIENT_PLUGIN_AUTH)
	buf = binary.LittleEndian.AppendUint16(buf, 0x85a6)

	// Character set (utf8mb4 = 255)
	buf = append(buf, 255)

	// Status flags (AUTOCOMMIT)
	buf = binary.LittleEndian.AppendUint16(buf, 0x0002)

	// Capability flags upper
	buf = binary.LittleEndian.AppendUint16(buf, 0x800f)

	// Auth data length
	buf = append(buf, 21)

	// Reserved (10 bytes)
	buf = append(buf, make([]byte, 10)...)

	// Auth data part 2 (12 bytes) + null
	if len(scramble) >= 20 {
		buf = append(buf, scramble[8:20]...)
	} else if len(scramble) > 8 {
		buf = append(buf, scramble[8:]...)
		buf = append(buf, make([]byte, 20-len(scramble))...)
	} else {
		buf = append(buf, make([]byte, 12)...)
	}
	buf = append(buf, 0)

	// Auth plugin name
	buf = append(buf, []byte("mysql_native_password")...)
	buf = append(buf, 0)

	return buf
}

// parseMySQLHandshakeResponse parses the client handshake response.
func parseMySQLHandshakeResponse(data []byte) (username, database string, err error) {
	if len(data) < 32 {
		return "", "", fmt.Errorf("response too short")
	}

	pos := 0

	// Capability flags (4 bytes)
	pos += 4

	// Max packet size (4 bytes)
	pos += 4

	// Character set (1 byte)
	pos++

	// Reserved (23 bytes)
	pos += 23

	// Username (null-terminated)
	usernameStart := pos
	for pos < len(data) && data[pos] != 0 {
		pos++
	}
	username = string(data[usernameStart:pos])
	pos++ // skip null

	// Skip auth response (variable length)
	if pos < len(data) {
		authLen := int(data[pos])
		pos++
		pos += authLen
	}

	// Database (null-terminated)
	if pos < len(data) {
		dbStart := pos
		for pos < len(data) && data[pos] != 0 {
			pos++
		}
		database = string(data[dbStart:pos])
	}

	return username, database, nil
}

// parseMySQLHandshakeResponseWithAuth parses the client handshake response and extracts auth data.
// Returns username, database, and auth response (password proof).
func parseMySQLHandshakeResponseWithAuth(data []byte) (username string, authResponse []byte, err error) {
	if len(data) < 32 {
		return "", nil, fmt.Errorf("response too short")
	}

	pos := 0

	// Capability flags (4 bytes)
	pos += 4

	// Max packet size (4 bytes)
	pos += 4

	// Character set (1 byte)
	pos++

	// Reserved (23 bytes)
	pos += 23

	// Username (null-terminated)
	usernameStart := pos
	for pos < len(data) && data[pos] != 0 {
		pos++
	}
	username = string(data[usernameStart:pos])
	pos++ // skip null

	// Auth response (length-encoded string)
	if pos < len(data) {
		authLen := int(data[pos])
		pos++
		if authLen > 0 && pos+authLen <= len(data) {
			authResponse = make([]byte, authLen)
			copy(authResponse, data[pos:pos+authLen])
			pos += authLen
		}
	}

	return username, authResponse, nil
}

// createMySQLErrorPacket creates a MySQL error packet.
func createMySQLErrorPacket(code uint16, state, message string) []byte {
	// Error packet: 0xff (header) + error code (2) + SQL state (5) + message
	buf := make([]byte, 0, 9+len(message))
	buf = append(buf, 0xff) // Error packet type
	buf = binary.LittleEndian.AppendUint16(buf, code)
	buf = append(buf, '#')
	buf = append(buf, []byte(state)...)
	buf = append(buf, []byte(message)...)
	return buf
}

// forwardMySQLAuth forwards authentication packets until completion.
func (ps *ProxySession) forwardMySQLAuth() error {
	// Forward packets between client and server until OK or ERR
	for {
		// Read from server
		serverReader := bufio.NewReader(ps.serverConn.Conn())

		// Read header
		header := make([]byte, 4)
		if _, err := io.ReadFull(serverReader, header); err != nil {
			return err
		}

		length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
		seq := header[3]

		if length > maxMySQLPayload {
			return fmt.Errorf("mysql payload too large: %d bytes", length)
		}

		// Read payload
		payload := make([]byte, length)
		if _, err := io.ReadFull(serverReader, payload); err != nil {
			return err
		}

		// Forward to client
		pkt := make([]byte, 4+length)
		copy(pkt, header)
		copy(pkt[4:], payload)
		if _, err := ps.clientConn.Write(pkt); err != nil {
			return err
		}

		// Check for OK (0x00) or ERR (0xff) or EOF (0xfe for old protocol)
		if length > 0 {
			switch payload[0] {
			case 0x00: // OK
				return nil
			case 0xff: // ERR
				ps.recordAuthFailure()
				return fmt.Errorf("authentication failed")
			case 0xfe: // Auth switch request
				// Read client response
				clientHeader := make([]byte, 4)
				if _, err := io.ReadFull(ps.clientConn, clientHeader); err != nil {
					return err
				}

				clientLength := int(clientHeader[0]) | int(clientHeader[1])<<8 | int(clientHeader[2])<<16
				if clientLength > maxMySQLPayload {
					return fmt.Errorf("mysql auth response too large: %d bytes", clientLength)
				}
				clientPayload := make([]byte, clientLength)
				if _, err := io.ReadFull(ps.clientConn, clientPayload); err != nil {
					return err
				}

				// Forward to server
				clientPkt := make([]byte, 4+clientLength)
				copy(clientPkt, clientHeader)
				clientPkt[3] = seq + 1
				copy(clientPkt[4:], clientPayload)
				if _, err := ps.serverConn.Conn().Write(clientPkt); err != nil {
					return err
				}
				// Continue waiting for OK/ERR
			}
		}
	}
}

// handleMSSQLStartup handles MSSQL startup handshake.
func (ps *ProxySession) handleMSSQLStartup(ctx context.Context) error {
	// Check rate limiter before starting MSSQL authentication (M-4 fix)
	clientIP := ps.clientConn.RemoteAddr().String()
	if ps.authLimiter != nil && ps.authLimiter.IsLimited(clientIP) {
		ps.log.Warn("Authentication blocked: client rate limited", "client", clientIP)
		return fmt.Errorf("too many failed attempts, try again later")
	}

	// For MSSQL, check if we're in interception mode with user DB
	// In interception mode, we read client handshake first to get username
	// In passthrough mode, we connect to backend and let it handle auth
	useInterception := ps.authMode == "interception" && ps.userDB != nil

	if useInterception {
		// Interception: read Pre-Login and Login7 from client to get username
		preLoginData, login7Data, err := ps.readMSSQLClientHandshake()
		if err != nil {
			return fmt.Errorf("failed to read MSSQL handshake: %w", err)
		}

		// Extract username for verification
		if len(login7Data) > 0 {
			ps.extractLogin7Credentials(login7Data)
		}

		user := ps.userDB.GetUser(ps.username)
		return ps.handleMSSQLInterception(ctx, user, clientIP, preLoginData, login7Data)
	}

	// Passthrough or default: connect to backend first, forward all auth
	return ps.handleMSSQLPassthrough(ctx)
}

// readMSSQLClientHandshake reads Pre-Login and Login7 from MSSQL client.
func (ps *ProxySession) readMSSQLClientHandshake() ([]byte, []byte, error) {
	reader := bufio.NewReader(ps.clientConn)

	// Read Pre-Login header (8 bytes)
	preLoginHeader := make([]byte, 8)
	if _, err := io.ReadFull(reader, preLoginHeader); err != nil {
		return nil, nil, fmt.Errorf("failed to read Pre-Login header: %w", err)
	}

	if preLoginHeader[0] != 0x12 { // PacketTypePreLogin
		return nil, nil, fmt.Errorf("expected Pre-Login packet, got 0x%02x", preLoginHeader[0])
	}

	preLoginLength := binary.BigEndian.Uint16(preLoginHeader[2:4])
	preLoginPayloadLen := int(preLoginLength) - 8
	if preLoginPayloadLen < 0 || preLoginPayloadLen > maxMySQLPayload {
		return nil, nil, fmt.Errorf("invalid MSSQL Pre-Login length: %d", preLoginPayloadLen)
	}

	preLoginPayload := make([]byte, preLoginPayloadLen)
	if _, err := io.ReadFull(reader, preLoginPayload); err != nil {
		return nil, nil, fmt.Errorf("failed to read Pre-Login payload: %w", err)
	}

	preLoginData := make([]byte, 8+preLoginPayloadLen)
	copy(preLoginData[0:8], preLoginHeader)
	copy(preLoginData[8:], preLoginPayload)

	// Read Login7 header (8 bytes)
	login7Header := make([]byte, 8)
	if _, err := io.ReadFull(reader, login7Header); err != nil {
		return nil, nil, fmt.Errorf("failed to read Login7 header: %w", err)
	}

	if login7Header[0] != 0x10 { // PacketTypeLogin7
		return nil, nil, fmt.Errorf("expected Login7 packet, got 0x%02x", login7Header[0])
	}

	login7Length := binary.BigEndian.Uint16(login7Header[2:4])
	login7PayloadLen := int(login7Length) - 8
	if login7PayloadLen < 0 || login7PayloadLen > maxMySQLPayload {
		return nil, nil, fmt.Errorf("invalid MSSQL Login7 length: %d", login7PayloadLen)
	}

	login7Payload := make([]byte, login7PayloadLen)
	if _, err := io.ReadFull(reader, login7Payload); err != nil {
		return nil, nil, fmt.Errorf("failed to read Login7 payload: %w", err)
	}

	login7Data := make([]byte, 8+login7PayloadLen)
	copy(login7Data[0:8], login7Header)
	copy(login7Data[8:], login7Payload)

	return preLoginData, login7Data, nil
}

// handleMSSQLPassthrough handles MSSQL passthrough authentication.
func (ps *ProxySession) handleMSSQLPassthrough(ctx context.Context) error {
	// TDS protocol: Pre-Login -> Login7 -> Auth complete
	// Connect to backend first
	if err := ps.poolSession.Strategy().OnClientConnect(ctx, ps.poolSession); err != nil {
		return fmt.Errorf("failed to connect to backend: %w", err)
	}

	serverConn := ps.poolSession.ServerConn()
	if serverConn == nil {
		return fmt.Errorf("no server connection available")
	}

	ps.serverConn = serverConn

	// Read and forward Pre-Login from client to server
	if err := ps.forwardMSSQLPreLogin(); err != nil {
		return fmt.Errorf("pre-login failed: %w", err)
	}

	// Read Login7 from client
	reader := bufio.NewReader(ps.clientConn)
	loginHeader := make([]byte, 8)
	if _, err := io.ReadFull(reader, loginHeader); err != nil {
		return fmt.Errorf("failed to read Login7 header: %w", err)
	}
	if loginHeader[0] != 0x10 {
		return fmt.Errorf("expected Login7 packet, got 0x%02x", loginHeader[0])
	}
	loginLength := binary.BigEndian.Uint16(loginHeader[2:4])
	loginPayloadLen := int(loginLength) - 8
	if loginPayloadLen < 0 || loginPayloadLen > maxMySQLPayload {
		return fmt.Errorf("invalid MSSQL Login7 length: %d", loginPayloadLen)
	}
	loginPayload := make([]byte, loginPayloadLen)
	if _, err := io.ReadFull(reader, loginPayload); err != nil {
		return fmt.Errorf("failed to read Login7 payload: %w", err)
	}

	// Forward Login7 to server
	login7 := make([]byte, loginLength)
	copy(login7[0:8], loginHeader)
	copy(login7[8:], loginPayload)
	if _, err := ps.serverConn.Conn().Write(login7); err != nil {
		return fmt.Errorf("failed to forward Login7: %w", err)
	}

	// Read Login7 response from server and forward to client
	if err := ps.forwardMSSQLLogin7Response(); err != nil {
		return fmt.Errorf("login response failed: %w", err)
	}

	ps.authenticated.Store(true)
	ps.recordAuthSuccess()
	ps.poolSession.SetAuthDone()

	return nil
}

// handleMSSQLInterception handles MSSQL interception authentication.
// Geryon verifies user exists and has pool access, then forwards auth to backend.
func (ps *ProxySession) handleMSSQLInterception(ctx context.Context, user *auth.User, clientIP string, preLoginData, login7Data []byte) error {
	// Verify user exists in Geryon database
	if user == nil {
		ps.log.Warn("Unknown user in interception mode", "user", ps.username)
		ps.recordAuthFailure()
		return fmt.Errorf("unknown user: %s", ps.username)
	}

	// Check per-user connection limit
	if user.MaxConnections > 0 {
		if !ps.pool.TryIncrementUserCount(ps.username, user.MaxConnections) {
			ps.log.Warn("Per-user connection limit exceeded", "user", ps.username, "limit", user.MaxConnections)
			ps.recordAuthFailure()
			return fmt.Errorf("too many connections for user %s", ps.username)
		}
	}

	// Check pool access authorization
	if !user.CanAccessPool(ps.pool.Name()) {
		ps.log.Warn("Pool access denied", "user", ps.username, "pool", ps.pool.Name())
		ps.recordAuthFailure()
		return fmt.Errorf("access denied for user %s to pool %s", ps.username, ps.pool.Name())
	}

	// Connect to backend
	if err := ps.poolSession.Strategy().OnClientConnect(ctx, ps.poolSession); err != nil {
		ps.recordAuthFailure()
		return fmt.Errorf("failed to connect to backend: %w", err)
	}

	serverConn := ps.poolSession.ServerConn()
	if serverConn == nil {
		ps.recordAuthFailure()
		return fmt.Errorf("no server connection available")
	}

	ps.serverConn = serverConn

	// Forward Pre-Login to server
	if _, err := ps.serverConn.Conn().Write(preLoginData); err != nil {
		return fmt.Errorf("failed to forward Pre-Login: %w", err)
	}

	// Read Pre-Login response from server and forward to client
	if err := ps.forwardMSSQLPreLoginResponse(); err != nil {
		return fmt.Errorf("pre-login response failed: %w", err)
	}

	// Forward Login7 to server
	if _, err := ps.serverConn.Conn().Write(login7Data); err != nil {
		return fmt.Errorf("failed to forward Login7: %w", err)
	}

	// Read Login7 response (OK or error)
	if err := ps.forwardMSSQLLogin7Response(); err != nil {
		return fmt.Errorf("login response failed: %w", err)
	}

	ps.authenticated.Store(true)
	ps.recordAuthSuccess()
	ps.poolSession.SetAuthDone()

	return nil
}

// forwardMSSQLPreLoginResponse forwards Pre-Login response to client.
func (ps *ProxySession) forwardMSSQLPreLoginResponse() error {
	// Read response from server
	serverReader := bufio.NewReader(ps.serverConn.Conn())

	for {
		// Read header
		respHeader := make([]byte, 8)
		if _, err := io.ReadFull(serverReader, respHeader); err != nil {
			return fmt.Errorf("failed to read Pre-Login response header: %w", err)
		}

		respLength := binary.BigEndian.Uint16(respHeader[2:4])
		respPayloadLen := int(respLength) - 8
		if respPayloadLen < 0 || respPayloadLen > maxMySQLPayload {
			return fmt.Errorf("invalid MSSQL Pre-Login response length: %d", respPayloadLen)
		}

		respPayload := make([]byte, respPayloadLen)
		if _, err := io.ReadFull(serverReader, respPayload); err != nil {
			return fmt.Errorf("failed to read Pre-Login response payload: %w", err)
		}

		// Forward to client
		resp := make([]byte, respLength)
		copy(resp[0:8], respHeader)
		copy(resp[8:], respPayload)

		if _, err := ps.clientConn.Write(resp); err != nil {
			return fmt.Errorf("failed to send Pre-Login response: %w", err)
		}

		// Check for end of message
		if respHeader[1]&0x01 != 0 { // StatusEndOfMessage
			break
		}
	}

	return nil
}

// forwardMSSQLLogin7Response forwards Login7 response to client.
// Handles SSPI/NTLM challenge-response rounds: if the server sends an SSPI
// token (0xED), it forwards it to the client, reads the client's response,
// and forwards it back to the server until LoginAck or error.
func (ps *ProxySession) forwardMSSQLLogin7Response() error {
	// Read response from server
	serverReader := bufio.NewReader(ps.serverConn.Conn())

	for {
		// Read header
		respHeader := make([]byte, 8)
		if _, err := io.ReadFull(serverReader, respHeader); err != nil {
			return fmt.Errorf("failed to read Login7 response header: %w", err)
		}

		respLength := binary.BigEndian.Uint16(respHeader[2:4])
		respPayloadLen := int(respLength) - 8
		if respPayloadLen < 0 || respPayloadLen > maxMySQLPayload {
			return fmt.Errorf("invalid MSSQL Login7 response length: %d", respLength)
		}

		respPayload := make([]byte, respPayloadLen)
		if _, err := io.ReadFull(serverReader, respPayload); err != nil {
			return fmt.Errorf("failed to read Login7 response payload: %w", err)
		}

		// Check for SSPI/NTLM authentication token (TokenTypeSSPI = 0xED)
		// or SSPI packet type (0x11) — indicates challenge-response is needed
		if respPayloadLen > 0 && (respPayload[0] == 0xED || respHeader[0] == 0x11) {
			// Forward SSPI challenge to client
			resp := make([]byte, respLength)
			copy(resp[0:8], respHeader)
			copy(resp[8:], respPayload)
			if _, err := ps.clientConn.Write(resp); err != nil {
				return fmt.Errorf("failed to send SSPI challenge: %w", err)
			}

			// Read client's SSPI response (NTLM Type 3 or next challenge)
			if err := ps.forwardMSSQLClientSSPIResponse(serverReader); err != nil {
				return fmt.Errorf("client SSPI response failed: %w", err)
			}
			// Continue reading — server may send more tokens or LoginAck
			continue
		}

		// Forward to client
		resp := make([]byte, respLength)
		copy(resp[0:8], respHeader)
		copy(resp[8:], respPayload)

		if _, err := ps.clientConn.Write(resp); err != nil {
			return fmt.Errorf("failed to send Login7 response: %w", err)
		}

		// Check for end of message
		if respHeader[1]&0x01 != 0 { // StatusEndOfMessage
			break
		}
	}

	return nil
}

// forwardMSSQLClientSSPIResponse reads an SSPI response from the client
// (NTLM Type 3 message) and forwards it to the server.
func (ps *ProxySession) forwardMSSQLClientSSPIResponse(serverReader *bufio.Reader) error {
	// Read client SSPI response header directly from the connection
	clientHeader := make([]byte, 8)
	if _, err := io.ReadFull(ps.clientConn, clientHeader); err != nil {
		return fmt.Errorf("failed to read client SSPI response header: %w", err)
	}

	clientLength := binary.BigEndian.Uint16(clientHeader[2:4])
	clientPayloadLen := int(clientLength) - 8
	if clientPayloadLen < 0 || clientPayloadLen > maxMySQLPayload {
		return fmt.Errorf("invalid MSSQL client SSPI response length: %d", clientLength)
	}

	clientPayload := make([]byte, clientPayloadLen)
	if _, err := io.ReadFull(ps.clientConn, clientPayload); err != nil {
		return fmt.Errorf("failed to read client SSPI response payload: %w", err)
	}

	// Forward to server
	resp := make([]byte, clientLength)
	copy(resp[0:8], clientHeader)
	copy(resp[8:], clientPayload)
	if _, err := ps.serverConn.Conn().Write(resp); err != nil {
		return fmt.Errorf("failed to forward client SSPI response: %w", err)
	}

	// Read server's response to the client's SSPI message
	for {
		respHeader := make([]byte, 8)
		if _, err := io.ReadFull(serverReader, respHeader); err != nil {
			return fmt.Errorf("failed to read server SSPI response header: %w", err)
		}

		respLength := binary.BigEndian.Uint16(respHeader[2:4])
		respPayloadLen := int(respLength) - 8
		if respPayloadLen < 0 || respPayloadLen > maxMySQLPayload {
			return fmt.Errorf("invalid MSSQL server SSPI response length: %d", respLength)
		}

		respPayload := make([]byte, respPayloadLen)
		if _, err := io.ReadFull(serverReader, respPayload); err != nil {
			return fmt.Errorf("failed to read server SSPI response payload: %w", err)
		}

		// Check if server sends another SSPI challenge (multi-round NTLM)
		if respPayloadLen > 0 && (respPayload[0] == 0xED || respHeader[0] == 0x11) {
			resp := make([]byte, respLength)
			copy(resp[0:8], respHeader)
			copy(resp[8:], respPayload)
			if _, err := ps.clientConn.Write(resp); err != nil {
				return fmt.Errorf("failed to send SSPI challenge: %w", err)
			}
			// Recursively handle next client response
			return ps.forwardMSSQLClientSSPIResponse(serverReader)
		}

		// Forward final response to client
		resp := make([]byte, respLength)
		copy(resp[0:8], respHeader)
		copy(resp[8:], respPayload)
		if _, err := ps.clientConn.Write(resp); err != nil {
			return fmt.Errorf("failed to forward server response: %w", err)
		}

		if respHeader[1]&0x01 != 0 {
			break
		}
	}

	return nil
}

// forwardMSSQLPreLogin handles Pre-Login (backward compatibility for tests).
// Reads Pre-Login from client, forwards to server, reads response, forwards to client.
func (ps *ProxySession) forwardMSSQLPreLogin() error {
	reader := bufio.NewReader(ps.clientConn)

	// Read header (8 bytes)
	header := make([]byte, 8)
	if _, err := io.ReadFull(reader, header); err != nil {
		return fmt.Errorf("failed to read Pre-Login header: %w", err)
	}

	if header[0] != 0x12 { // PacketTypePreLogin
		return fmt.Errorf("expected Pre-Login packet, got 0x%02x", header[0])
	}

	length := binary.BigEndian.Uint16(header[2:4])
	payloadLen := int(length) - 8
	if payloadLen < 0 || payloadLen > maxMySQLPayload {
		return fmt.Errorf("invalid MSSQL Pre-Login length: %d", payloadLen)
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return fmt.Errorf("failed to read Pre-Login payload: %w", err)
	}

	// Forward to server
	preLogin := make([]byte, length)
	copy(preLogin[0:8], header)
	copy(preLogin[8:], payload)

	if _, err := ps.serverConn.Conn().Write(preLogin); err != nil {
		return fmt.Errorf("failed to forward Pre-Login: %w", err)
	}

	// Read response from server and forward to client
	return ps.forwardMSSQLPreLoginResponse()
}

// forwardMSSQLLogin7 handles Login7 (backward compatibility for tests).
// Reads Login7 from client, forwards to server, reads response, forwards to client.
func (ps *ProxySession) forwardMSSQLLogin7() error {
	reader := bufio.NewReader(ps.clientConn)

	// Read header (8 bytes)
	header := make([]byte, 8)
	if _, err := io.ReadFull(reader, header); err != nil {
		return fmt.Errorf("failed to read Login7 header: %w", err)
	}

	if header[0] != 0x10 { // PacketTypeLogin7
		return fmt.Errorf("expected Login7 packet, got 0x%02x", header[0])
	}

	length := binary.BigEndian.Uint16(header[2:4])
	payloadLen := int(length) - 8
	if payloadLen < 0 || payloadLen > maxMySQLPayload {
		return fmt.Errorf("invalid MSSQL Login7 length: %d", payloadLen)
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return fmt.Errorf("failed to read Login7 payload: %w", err)
	}

	// Extract username for logging
	ps.extractLogin7Credentials(payload)

	// Forward to server
	login7 := make([]byte, length)
	copy(login7[0:8], header)
	copy(login7[8:], payload)

	if _, err := ps.serverConn.Conn().Write(login7); err != nil {
		return fmt.Errorf("failed to forward Login7: %w", err)
	}

	// Forward auth response
	return ps.forwardMSSQLAuthResponse()
}

// extractLogin7Credentials extracts username from Login7 packet.
func (ps *ProxySession) extractLogin7Credentials(data []byte) {
	if len(data) < 36 {
		return
	}

	// Offset of username is at byte 28-29 (2 bytes)
	usernameOffset := binary.LittleEndian.Uint16(data[28:30])
	usernameLen := binary.LittleEndian.Uint16(data[30:32])

	if int(usernameOffset)+int(usernameLen)*2 > len(data) {
		return
	}

	// Username is UTF-16LE
	usernameBytes := data[usernameOffset : usernameOffset+usernameLen*2]
	var username strings.Builder
	for i := 0; i < len(usernameBytes); i += 2 {
		if i+1 >= len(usernameBytes) {
			break
		}
		r := rune(binary.LittleEndian.Uint16(usernameBytes[i:]))
		if r == 0 {
			break
		}
		username.WriteRune(r)
	}

	ps.username = username.String()

	// Database is at offset 36-37
	dbOffset := binary.LittleEndian.Uint16(data[36:38])
	dbLen := binary.LittleEndian.Uint16(data[38:40])

	if int(dbOffset)+int(dbLen)*2 <= len(data) {
		dbBytes := data[dbOffset : dbOffset+dbLen*2]
		var db strings.Builder
		for i := 0; i < len(dbBytes); i += 2 {
			if i+1 >= len(dbBytes) {
				break
			}
			r := rune(binary.LittleEndian.Uint16(dbBytes[i:]))
			if r == 0 {
				break
			}
			db.WriteRune(r)
		}
		ps.database = db.String()
	}
}

// forwardMSSQLAuthResponse forwards authentication response until complete.
func (ps *ProxySession) forwardMSSQLAuthResponse() error {
	serverReader := bufio.NewReader(ps.serverConn.Conn())

	for {
		// Read packet from server
		header := make([]byte, 8)
		if _, err := io.ReadFull(serverReader, header); err != nil {
			return fmt.Errorf("failed to read auth response header: %w", err)
		}

		length := binary.BigEndian.Uint16(header[2:4])
		payloadLen := int(length) - 8
		if payloadLen < 0 || payloadLen > maxMySQLPayload {
			return fmt.Errorf("invalid MSSQL message length: %d", payloadLen)
		}

		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(serverReader, payload); err != nil {
			return fmt.Errorf("failed to read auth response payload: %w", err)
		}

		// Forward to client
		pkt := make([]byte, length)
		copy(pkt[0:8], header)
		copy(pkt[8:], payload)

		if _, err := ps.clientConn.Write(pkt); err != nil {
			return fmt.Errorf("failed to forward auth response: %w", err)
		}

		// Check for LoginAck (0xAD) or Error (0xAA)
		if len(payload) > 0 {
			tokenType := payload[0]
			if tokenType == 0xAD { // LoginAck
				// Authentication successful
				// Continue until we see Done
			}
			if tokenType == 0xAA { // Error
				ps.recordAuthFailure()
				return fmt.Errorf("authentication failed")
			}
		}

		// Check for end of message
		if header[1]&0x01 != 0 { // StatusEndOfMessage
			break
		}
	}

	return nil
}

// OnQuery is called when a query is received.
func (ps *ProxySession) OnQuery(ctx context.Context, msg *common.Message) (*pool.ServerConn, error) {
	ps.queryCount.Add(1)
	ps.poolSession.IncrementQueryCount()
	ps.pool.IncrementQueryCount()

	// Use router for read/write splitting if enabled
	if ps.router != nil {
		query, _ := ps.codec.ExtractQuery(msg)
		if query != "" {
			backend, err := ps.router.RouteQuery(query, ps.poolSession.InTransaction())
			if err == nil && backend != nil {
				if backend.Role == "replica" {
					ps.poolSession.SetTargetRole("replica")
				} else {
					ps.poolSession.SetTargetRole("primary")
				}
			}
		}
	}

	// Get server connection from strategy
	conn, err := ps.poolSession.Strategy().OnQuery(ctx, ps.poolSession, msg)
	if err != nil {
		return nil, err
	}

	// Extract and store query string
	if query, err := ps.codec.ExtractQuery(msg); err == nil {
		ps.poolSession.SetLastQuery(query)
	}

	// Check for transaction boundaries
	if ps.codec.IsTransactionBegin(msg) {
		ps.poolSession.Strategy().OnTransactionBegin(ps.poolSession)
	} else if ps.codec.IsTransactionEnd(msg) {
		ps.poolSession.Strategy().OnTransactionEnd(ps.poolSession)
	}

	return conn, nil
}

// OnQueryComplete is called when a query completes.
func (ps *ProxySession) OnQueryComplete() error {
	return ps.poolSession.Strategy().OnQueryComplete(ps.poolSession)
}

// Close closes the session.
func (ps *ProxySession) Close() error {
	if ps.closed.CompareAndSwap(false, true) {
		ps.clientConn.Close()
	}
	return nil
}

// recordAuthFailure records a failed authentication attempt for rate limiting.
func (ps *ProxySession) recordAuthFailure() {
	if ps.authLimiter != nil {
		clientIP := ps.clientConn.RemoteAddr().String()
		locked := ps.authLimiter.RecordFailure(clientIP)
		if locked {
			ps.log.Warn("Client IP locked out due to repeated auth failures", "client", clientIP)
		}
	}
}

// recordAuthSuccess resets the rate limiter counter for a client IP.
func (ps *ProxySession) recordAuthSuccess() {
	if ps.authLimiter != nil {
		clientIP := ps.clientConn.RemoteAddr().String()
		ps.authLimiter.RecordSuccess(clientIP)
	}
}

// Relay handles bidirectional message forwarding.
type Relay struct {
	mu sync.Mutex
}

// NewRelay creates a new relay.
func NewRelay() *Relay {
	return &Relay{}
}

// Run runs the bidirectional relay.
func (r *Relay) Run(ctx context.Context, clientConn net.Conn, session *pool.Session, codec common.Codec, ps *ProxySession) {
	// Create error channels for both directions
	errCh := make(chan error, 2)
	// done signals the first goroutine to exit; closing both connections
	// wakes up the other goroutine's blocked read
	var done atomic.Bool
	var closeOnce sync.Once

	// Client -> Server
	go func() {
		errCh <- r.forwardClientToServer(ctx, clientConn, session, codec, ps)
		done.Store(true)
	}()

	// Server -> Client
	go func() {
		errCh <- r.forwardServerToClient(ctx, clientConn, session, codec, ps)
		done.Store(true)
	}()

	// Wait for first error or context cancellation
	select {
	case <-ctx.Done():
	case <-errCh:
	}

	// Signal both goroutines to stop and close connections
	closeOnce.Do(func() {
		if ps.serverConn != nil && ps.serverConn.Conn() != nil {
			ps.serverConn.Conn().Close()
		}
		if clientConn != nil {
			clientConn.Close()
		}
	})

	// Drain second goroutine result
	select {
	case err := <-errCh:
		if err != nil && err != io.EOF {
			ps.log.Debug("Relay error", "error", err)
		}
	default:
	}

	// Wait for both goroutines to exit (they're woken by connection close)
	for n := 0; n < 2 && !done.Load(); n++ {
		select {
		case <-errCh:
		case <-time.After(time.Second):
		}
	}
}

// forwardClientToServer forwards messages from client to server.
func (r *Relay) forwardClientToServer(ctx context.Context, clientConn net.Conn, session *pool.Session, codec common.Codec, ps *ProxySession) error {
	for {
		// Check context
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Read message from client
		msg, err := codec.ReadMessage(clientConn)
		if err != nil {
			return err
		}

		msg.Direction = common.Frontend

		// Check for terminate
		if codec.IsTerminate(msg) {
			return io.EOF
		}

		// Extract query and start timing
		var query string
		var cacheKey string
		queryStartTime := time.Now()

		if codec.IsQuery(msg) {
			query, _ = codec.ExtractQuery(msg)
			ps.currentQuery = query
			ps.queryStartTime = queryStartTime

			// Check for transaction boundaries (M-6 fix: use codec methods which strip comments)
			if ps.transactionMgr != nil {
				if codec.IsTransactionBegin(msg) {
					// Register new transaction with abort function
					if ps.serverConn != nil {
						abortFn := func() {
							ps.sendRollbackToBackend()
						}
						ps.transactionInfo = ps.transactionMgr.Register(ps.id, ps.serverConn.ID(), abortFn)
					} else {
						ps.transactionInfo = ps.transactionMgr.Register(ps.id, 0, nil)
					}
				} else if codec.IsTransactionEnd(msg) {
					// End transaction
					if ps.transactionInfo != nil {
						// Determine if commit or rollback from query text
						upperQuery := strings.ToUpper(strings.TrimSpace(query))
						if strings.HasPrefix(upperQuery, "COMMIT") {
							ps.transactionMgr.SetStatus(ps.transactionInfo.ID, pool.TxnCommitted)
						} else {
							ps.transactionMgr.SetStatus(ps.transactionInfo.ID, pool.TxnAborted)
						}
						ps.transactionMgr.Unregister(ps.transactionInfo.ID)
						ps.transactionInfo = nil
					}
				}
			}
		}

		// Handle prepared statements (Parse, Bind, Close for PG; sp_prepare/sp_execute for MSSQL)
		if ps.poolSession.PreparedStatements() != nil && (ps.config.Body == "postgresql" || ps.config.Body == "mssql") {
			switch {
			case codec.IsPrepare(msg):
				// Parse message (PG) or sp_prepare RPC (MSSQL) — track pending parse
				if ps.config.Body == "postgresql" {
					pgCodec := codec.(*postgresql.PGCodec)
					stmtName, _ := pgCodec.ExtractStatementName(msg)
					query, _ := pgCodec.ExtractQuery(msg)
					if query != "" {
						ps.pendingParseMu.Lock()
						ps.pendingParseStmt = stmtName
						ps.pendingParseQuery = query
						ps.pendingParseMu.Unlock()
						ps.log.Debug("Prepared statement pending confirmation",
							"name", stmtName,
							"query", query[:min(len(query), 50)],
						)
					}
				} else {
					// MSSQL sp_prepare — extract procedure name as statement name
					stmtName, _ := codec.ExtractQuery(msg)
					if stmtName != "" {
						ps.pendingParseMu.Lock()
						ps.pendingParseStmt = stmtName
						ps.pendingParseQuery = "" // MSSQL doesn't expose the SQL in sp_prepare
						ps.pendingParseMu.Unlock()
						ps.log.Debug("MSSQL prepared statement pending", "name", stmtName)
					}
				}

			case codec.IsBind(msg):
				// Bind message — track bound statement name for re-preparation
				if ps.config.Body == "postgresql" {
					if pgCodec, ok := codec.(*postgresql.PGCodec); ok {
						if stmtName, err := pgCodec.ExtractBindStatementName(msg); err == nil && stmtName != "" {
							ps.lastBoundStmt = stmtName
						}
					}
				}
				// MSSQL: sp_execute carries params inline, no separate bind

			case codec.IsExecute(msg):
				// Execute message — track for re-preparation
				if ps.config.Body == "mssql" {
					// Extract statement name from sp_execute
					if stmtName, _ := codec.ExtractQuery(msg); stmtName != "" {
						ps.lastBoundStmt = stmtName
					}
				}

			case codec.IsClose(msg):
				// Close message — remove from cache
				if ps.config.Body == "postgresql" {
					if len(msg.Payload) > 2 && msg.Payload[0] == 'S' {
						namePos := 1
						for namePos < len(msg.Payload) && msg.Payload[namePos] != 0 {
							namePos++
						}
						stmtName := string(msg.Payload[1:namePos])
						ps.poolSession.PreparedStatements().Register(stmtName, "", nil)
						ps.log.Debug("Prepared statement closed", "name", stmtName)
					}
				}
				// MSSQL: sp_unprepare RPC (not commonly used, connections are reset)
			}
		}

		// Check cache if enabled
		if ps.cacheStore != nil && ps.cacheRules != nil && query != "" {
			// Check if this is a data modification query
			if isModificationQuery(query) {
				// Invalidate cache for affected tables
				tables := extractTablesFromQuery(query)
				if len(tables) > 0 {
					ps.log.Debug("Cache invalidation", "tables", tables, "query", query[:min(len(query), 50)])
					ps.cacheStore.InvalidateTables(tables)
				}
			} else if ps.cacheRules.ShouldCache(query) && isSelectQuery(query) {
				cacheKey = cache.GenerateKey(query).String()
				// Check cache
				if cachedData, hit := ps.cacheStore.Get(cacheKey); hit {
					ps.log.Debug("Cache hit", "query", query[:min(len(query), 50)])
					// Send cached response to client
					if err := ps.sendCachedResponse(clientConn, cachedData); err != nil {
						ps.log.Error("Failed to send cached response", "error", err)
						// Fall through to normal handling
					} else {
						// Log cache hit as a fast query
						if ps.queryLogger != nil {
							ps.queryLogger.LogQuery(logger.QueryLogEntry{
								Timestamp:    queryStartTime,
								QueryID:      fmt.Sprintf("%d-%d", ps.id, ps.queryCount.Load()),
								Pool:         ps.config.Name,
								ClientAddr:   ps.clientConn.RemoteAddr().String(),
								BackendAddr:  "cache",
								Username:     ps.username,
								Database:     ps.database,
								Query:        query,
								QueryHash:    cacheKey,
								Duration:     time.Since(queryStartTime),
								IsCached:     true,
								RowsReturned: 0,
							})
						}
						// Cache hit served, continue to next message
						continue
					}
				}
			}
		}

		// Get server connection for this message
		serverConn, err := ps.OnQuery(ctx, msg)
		if err != nil {
			return err
		}

		// Re-prepare statement on new server connection if needed (statement mode pooling)
		if ps.stmtRepreparer != nil && serverConn != nil && codec.IsExecute(msg) && ps.lastBoundStmt != "" {
			ps.reprepareStatement(codec, serverConn.Conn(), ps.lastBoundStmt)
			ps.lastBoundStmt = ""
		}

		// Update transaction server connection if needed
		if ps.transactionInfo != nil && ps.transactionInfo.ServerConnID == 0 && serverConn != nil {
			ps.transactionInfo.ServerConnID = serverConn.ID()
		}

		// If this is a cachable query, capture the response
		if cacheKey != "" {
			// Forward and capture response
			if err := r.forwardAndCapture(serverConn.Conn(), clientConn, msg, cacheKey, ps, queryStartTime); err != nil {
				return err
			}
		} else {
			// Write message to server normally and log query
			if err := codec.WriteMessage(serverConn.Conn(), msg); err != nil {
				return err
			}

			// Log non-cached query (we'll update duration in forwardServerToClient when response completes)
			ps.currentQuery = query
			ps.queryStartTime = queryStartTime
		}

		// Handle extended query protocol (Sync message indicates end of extended query)
		if msg.Type == 'S' { // Sync
			ps.OnQueryComplete()
		}
	}
}

// isSelectQuery returns true if query is a SELECT statement.
func isSelectQuery(query string) bool {
	upper := strings.ToUpper(strings.TrimSpace(query))
	return strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH")
}

// isModificationQuery returns true if query modifies data (INSERT, UPDATE, DELETE, etc.).
func isModificationQuery(query string) bool {
	upper := strings.ToUpper(strings.TrimSpace(query))
	return strings.HasPrefix(upper, "INSERT") ||
		strings.HasPrefix(upper, "UPDATE") ||
		strings.HasPrefix(upper, "DELETE") ||
		strings.HasPrefix(upper, "TRUNCATE") ||
		strings.HasPrefix(upper, "DROP") ||
		strings.HasPrefix(upper, "ALTER") ||
		strings.HasPrefix(upper, "CREATE") ||
		strings.HasPrefix(upper, "REPLACE")
}

// sendCachedResponse sends a cached response to the client.
func (ps *ProxySession) sendCachedResponse(clientConn net.Conn, data []byte) error {
	_, err := clientConn.Write(data)
	return err
}

// reprepareStatement checks if a bound statement needs re-preparation
// on the current server connection, and sends the original Parse if needed.
func (ps *ProxySession) reprepareStatement(codec common.Codec, serverConn net.Conn, boundStmtName string) {
	if boundStmtName == "" {
		return
	}

	connID := uint64(0)
	if ps.serverConn != nil {
		connID = ps.serverConn.ID()
	}

	_, needsReprep, err := ps.stmtRepreparer.PrepareIfNeeded(connID, boundStmtName)
	if err != nil || !needsReprep {
		return
	}

	// Statement needs re-preparation — get the original SQL and send Parse
	if prepStmt, found := ps.poolSession.PreparedStatements().GetQuery(boundStmtName); found && prepStmt != nil {
		query := prepStmt.Query
		pgCodec, ok := codec.(*postgresql.PGCodec)
		if !ok {
			return
		}
		// Send Parse (named statement) to server before the Execute
		parseMsg := &common.Message{
			Type:    'P', // Parse
			Payload: pgCodec.BuildParsePayload(boundStmtName, query, nil),
		}
		if err := codec.WriteMessage(serverConn, parseMsg); err != nil {
			ps.log.Warn("Failed to re-prepare statement on server", "name", boundStmtName, "error", err)
		} else {
			ps.log.Debug("Statement re-prepared on server", "name", boundStmtName)
		}
	}
}

// sendRollbackToBackend sends a ROLLBACK to the backend server to abort an active transaction.
// Called by the transaction manager when a timeout is detected.
func (ps *ProxySession) sendRollbackToBackend() {
	serverConn := ps.poolSession.ServerConn()
	if serverConn == nil {
		ps.log.Warn("sendRollbackToBackend: no server connection to rollback")
		return
	}

	// Ensure connection is released back to pool after rollback
	defer func() {
		if serverConn != nil {
			ps.poolSession.Strategy().OnClientDisconnect(ps.poolSession)
		}
	}()

	// Send ROLLBACK query to backend
	rollbackMsg := &common.Message{
		Type:    'Q', // Simple Query
		Payload: append([]byte("ROLLBACK"), 0),
	}
	if err := ps.codec.WriteMessage(serverConn.Conn(), rollbackMsg); err != nil {
		ps.log.Warn("Failed to send ROLLBACK to backend", "error", err)
	} else {
		ps.log.Info("Transaction rolled back due to timeout",
			"client", ps.clientConn.RemoteAddr(),
			"username", ps.username,
			"pool", ps.config.Name,
		)
	}
}

// forwardAndCapture forwards request to server and captures response for caching.
func (r *Relay) forwardAndCapture(serverConn net.Conn, clientConn net.Conn, msg *common.Message, cacheKey string, ps *ProxySession, queryStartTime time.Time) error {
	codec := ps.codec
	query := ps.currentQuery

	// Write request to server
	if err := codec.WriteMessage(serverConn, msg); err != nil {
		return err
	}

	// Read and capture response
	// For PostgreSQL, we need to read multiple messages until ReadyForQuery
	// Use pooled buffer for response aggregation
	responseBuf := getBuffer()
	defer putBuffer(responseBuf)
	response := bytes.NewBuffer(responseBuf)
	var rowCount int64

	for {
		respMsg, err := codec.ReadMessage(serverConn)
		if err != nil {
			return err
		}

		respMsg.Direction = common.Backend

		// Count rows in DataRow messages
		if respMsg.Type == 'D' { // DataRow
			rowCount++
		}

		// Add to response buffer
		response.Write(respMsg.Raw)

		// Forward to client
		if err := codec.WriteMessage(clientConn, respMsg); err != nil {
			return err
		}

		// Check for end of response
		if respMsg.Type == 'Z' { // ReadyForQuery
			// Log query with timing
			if ps.queryLogger != nil && query != "" {
				duration := time.Since(queryStartTime)
				if ps.pool != nil {
					ps.pool.Metrics().RecordQuery(duration)
				}
				backendAddr := ""
				if ps.serverConn != nil {
					backendAddr = ps.serverConn.Conn().RemoteAddr().String()
				}
				ps.queryLogger.LogQuery(logger.QueryLogEntry{
					Timestamp:    queryStartTime,
					QueryID:      fmt.Sprintf("%d-%d", ps.id, ps.queryCount.Load()),
					Pool:         ps.config.Name,
					ClientAddr:   ps.clientConn.RemoteAddr().String(),
					BackendAddr:  backendAddr,
					Username:     ps.username,
					Database:     ps.database,
					Query:        query,
					QueryHash:    cacheKey,
					Duration:     duration,
					RowsReturned: rowCount,
					IsCached:     false,
					TransactionID: func() string {
						if ps.transactionInfo != nil {
							return fmt.Sprintf("%d", ps.transactionInfo.ID)
						}
						return ""
					}(),
				})
			}
			break
		}
	}

	// Store in cache
	tables := extractTablesFromQuery(query)
	ttl := ps.cacheRules.GetTTL(query, 5*time.Minute)
	if err := ps.cacheStore.Set(cacheKey, response.Bytes(), tables, ttl); err != nil {
		ps.log.Debug("Failed to cache response", "error", err)
	}

	return nil
}

// extractTablesFromQuery extracts table names from a query for invalidation.
func extractTablesFromQuery(query string) []string {
	// Simple extraction - look for FROM and JOIN clauses
	tables := make([]string, 0)
	upper := strings.ToUpper(query)

	// Simple regex-like extraction
	fromIdx := strings.Index(upper, "FROM ")
	if fromIdx != -1 {
		rest := query[fromIdx+5:]
		// Extract table name
		fields := strings.Fields(rest)
		if len(fields) > 0 {
			table := fields[0]
			// Remove any trailing commas or semicolons
			table = strings.TrimRight(table, ",;")
			tables = append(tables, table)
		}
	}

	return tables
}

// forwardServerToClient forwards messages from server to client.
func (r *Relay) forwardServerToClient(ctx context.Context, clientConn net.Conn, session *pool.Session, codec common.Codec, ps *ProxySession) error {
	// Use the server connection from the pool session
	serverConn := ps.serverConn
	if serverConn == nil {
		return fmt.Errorf("no server connection available")
	}

	var rowCount int64

	for {
		// Check context
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Read message from server
		msg, err := codec.ReadMessage(serverConn.Conn())
		if err != nil {
			return err
		}

		msg.Direction = common.Backend

		// Count rows in DataRow messages
		if msg.Type == 'D' { // DataRow
			rowCount++
		}

		// M-8 fix: handle ParseComplete and ErrorResponse for prepared statements
		if msg.Type == '1' { // ParseComplete - confirm pending parse
			ps.pendingParseMu.Lock()
			if ps.pendingParseStmt != "" {
				// Register confirmed statement in cache and mark on server conn
				ps.poolSession.PreparedStatements().Register(ps.pendingParseStmt, ps.pendingParseQuery, nil)
				if ps.serverConn != nil {
					ps.serverConn.AddPreparedStatement(ps.pendingParseStmt)
				}
				ps.log.Debug("Prepared statement confirmed",
					"name", ps.pendingParseStmt,
				)
				ps.pendingParseStmt = ""
				ps.pendingParseQuery = ""
			}
			ps.pendingParseMu.Unlock()
		} else if msg.Type == 'E' { // ErrorResponse - cancel pending parse
			ps.pendingParseMu.Lock()
			if ps.pendingParseStmt != "" {
				ps.log.Debug("Prepared statement failed",
					"name", ps.pendingParseStmt,
				)
				ps.pendingParseStmt = ""
				ps.pendingParseQuery = ""
			}
			ps.pendingParseMu.Unlock()
		}

		// Handle async notifications and notices from server
		// NotificationResponse ('A') — LISTEN/NOTIFY result, not a query response
		// NoticeResponse ('N') — server notice, not a query response
		if msg.Type == 'A' || msg.Type == 'N' {
			// Forward async notifications/notices immediately without logging as queries
			if err := codec.WriteMessage(clientConn, msg); err != nil {
				return err
			}
			continue // don't treat as query completion
		}

		// Handle COPY operations — these have their own flow
		if msg.Type == 'G' || msg.Type == 'H' || msg.Type == 'W' {
			// CopyIn/CopyOut/CopyBoth response — forward and continue
			if err := codec.WriteMessage(clientConn, msg); err != nil {
				return err
			}
			continue
		}
		if msg.Type == 'd' || msg.Type == 'c' {
			// CopyData/CopyDone — forward and continue
			if err := codec.WriteMessage(clientConn, msg); err != nil {
				return err
			}
			continue
		}

		// Check for query completion (protocol-specific)
		// PostgreSQL: 'Z' ReadyForQuery with 'I' (Idle) status
		// MySQL: 0x00 OK packet or 0xfe EOF packet
		queryComplete := false
		if msg.Type == 'Z' && len(msg.Payload) > 0 {
			// PostgreSQL ReadyForQuery
			status := msg.Payload[0]
			switch status {
			case 'I': // Idle (not in transaction)
				ps.poolSession.SetInTransaction(false)
				queryComplete = true
			case 'T', 'E': // In transaction block or failed transaction
				ps.poolSession.SetInTransaction(true)
			}
		} else if msg.Type == 0x00 || msg.Type == 0xfe {
			// MySQL OK or EOF packet - indicates end of result/completion
			ps.poolSession.SetInTransaction(false)
			queryComplete = true
		}

		if queryComplete {
			ps.OnQueryComplete()
		}

		// Write message to client
		if err := codec.WriteMessage(clientConn, msg); err != nil {
			return err
		}

		// Log query completion for non-cached queries
		if ps.queryLogger != nil && ps.currentQuery != "" {
			duration := time.Since(ps.queryStartTime)
			if ps.pool != nil {
				ps.pool.Metrics().RecordQuery(duration)
			}
			backendAddr := ""
			if ps.serverConn != nil {
				backendAddr = ps.serverConn.Conn().RemoteAddr().String()
			}
			ps.queryLogger.LogQuery(logger.QueryLogEntry{
				Timestamp:    ps.queryStartTime,
				QueryID:      fmt.Sprintf("%d-%d", ps.id, ps.queryCount.Load()),
				Pool:         ps.config.Name,
				ClientAddr:   ps.clientConn.RemoteAddr().String(),
				BackendAddr:  backendAddr,
				Username:     ps.username,
				Database:     ps.database,
				Query:        ps.currentQuery,
				QueryHash:    cache.GenerateKey(ps.currentQuery).String(),
				Duration:     duration,
				RowsReturned: rowCount,
				IsCached:     false,
				TransactionID: func() string {
					if ps.transactionInfo != nil {
						return fmt.Sprintf("%d", ps.transactionInfo.ID)
					}
					return ""
				}(),
			})

			// Reset query tracking
			ps.currentQuery = ""
			ps.queryStartTime = time.Time{}
			rowCount = 0
		}

		// Update transaction activity if in a transaction
		if ps.transactionInfo != nil {
			ps.transactionMgr.UpdateActivity(ps.transactionInfo.ID)
			ps.transactionMgr.IncrementQueryCount(ps.transactionInfo.ID)
		}
	}
}

// SetDeadline sets read/write deadlines on the connection.
func SetDeadline(conn net.Conn, timeout time.Duration) {
	if timeout > 0 {
		conn.SetDeadline(time.Now().Add(timeout))
	}
}

// authenticateWithCertificate attempts to authenticate using the client certificate.
// This should be called after TLS handshake for mTLS connections.
func (ps *ProxySession) authenticateWithCertificate() error {
	tlsConn, ok := ps.clientConn.(*tls.Conn)
	if !ok {
		return nil // Not a TLS connection
	}

	connState := tlsConn.ConnectionState()
	if len(connState.PeerCertificates) == 0 {
		// No client certificate provided - this is ok if not in verify-full mode
		return nil
	}

	// Get the first peer certificate (client certificate)
	cert := connState.PeerCertificates[0]

	// Extract username from certificate
	username, err := auth.ExtractIdentity(cert, auth.CertAuthEither)
	if err != nil {
		ps.log.Debug("Failed to extract identity from certificate", "error", err)
		return nil // Don't fail - username may be provided via startup params
	}

	// Validate certificate time validity
	if !auth.IsCertificateValid(cert) {
		return fmt.Errorf("client certificate is not valid (expired or not yet valid)")
	}

	// Check if user exists in database
	user := ps.userDB.GetUser(username)
	if user == nil {
		ps.log.Debug("Certificate authenticated user not found in database", "username", username)
		return nil // Allow through, backend will handle auth
	}

	// Set authenticated username
	ps.username = username
	ps.log.Info("Client authenticated via certificate", "username", username, "cn", cert.Subject.CommonName)

	return nil
}

// parseDuration parses a duration string with a default fallback.
func parseDuration(s string, defaultVal time.Duration) time.Duration {
	if s == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultVal
	}
	return d
}
