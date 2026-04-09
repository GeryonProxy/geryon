package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
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
)

// Listener manages incoming client connections for a pool.
type Listener struct {
	mu               sync.RWMutex
	pool             *pool.Pool
	config           *config.PoolConfig
	codec            common.Codec
	listener         net.Listener
	address          string
	active           atomic.Bool
	sessions         map[uint64]*ProxySession
	tlsConfig        *tls.Config
	userDB           *auth.UserDatabase
	cacheStore       *cache.Store
	cacheRules       *cache.RulesEngine
	queryLogger      *logger.QueryLogger
	transactionMgr   *pool.TransactionManager
	ctx              context.Context
	cancel           context.CancelFunc
	log              *logger.Logger
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
	}

	// Setup query logger if enabled
	qlConfig := logger.DefaultQueryLogConfig()
	qlConfig.Enabled = true
	qlConfig.Directory = "logs/queries/" + cfg.Name
	qlConfig.LogAllQueries = false // Set to true for debug mode
	if queryLogger, err := logger.NewQueryLogger(qlConfig); err == nil {
		l.queryLogger = queryLogger
		log.Info("Query logger enabled", "pool", cfg.Name, "directory", qlConfig.Directory)
	} else {
		log.Warn("Failed to create query logger", "error", err)
	}

	// Setup transaction manager
	l.transactionMgr = pool.NewTransactionManager(
		30*time.Minute, // transaction timeout
		5*time.Minute,  // idle timeout
		log,
	)

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
			l.cacheRules.AddRule(rule.Match, ttl, true)
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

	return l, nil
}

// setupTLS configures TLS for the listener.
func (l *Listener) setupTLS() error {
	// TODO: Implement TLS setup
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

		go l.handleConnection(conn)
	}
}

// handleConnection handles a new client connection.
func (l *Listener) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Check max connections
	if l.pool.Stats().ClientConnections >= int64(l.config.Limits.MaxClientConnections) {
		l.log.Warn("Max client connections reached", "pool", l.config.Name)
		return
	}

	// Create proxy session with cache, query logger, and transaction manager
	session, err := NewProxySession(conn, l.pool, l.codec, l.userDB, l.config, l.cacheStore, l.cacheRules, l.queryLogger, l.transactionMgr, l.log)
	if err != nil {
		l.log.Error("Failed to create session", "error", err)
		return
	}

	// Register session
	l.mu.Lock()
	l.sessions[session.ID()] = session
	l.mu.Unlock()

	l.pool.IncrementClientCount()

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
	closed          atomic.Bool
	queryCount      atomic.Int64
	authenticated   atomic.Bool
	username        string
	database        string
	scramState      *auth.SCRAMState
	cacheStore      *cache.Store
	cacheRules      *cache.RulesEngine
	// Query timing for logging
	currentQuery    string
	queryStartTime  time.Time
}

var (
	sessionIDCounter atomic.Uint64
)

// NewProxySession creates a new proxy session.
func NewProxySession(clientConn net.Conn, p *pool.Pool, codec common.Codec, userDB *auth.UserDatabase, cfg *config.PoolConfig, cacheStore *cache.Store, cacheRules *cache.RulesEngine, queryLogger *logger.QueryLogger, transactionMgr *pool.TransactionManager, log *logger.Logger) (*ProxySession, error) {
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
			if ps.config.TLS.Mode != "disable" {
				// Send 'S' to indicate SSL supported
				ps.clientConn.Write([]byte{'S'})
				// Wrap connection with TLS
				// TODO: Implement TLS upgrade
			} else {
				// Send 'N' to indicate SSL not supported
				ps.clientConn.Write([]byte{'N'})
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
	for pos < len(startupData)-1 {
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

		if key != "" {
			params[key] = val
		}
	}

	ps.username = params["user"]
	ps.database = params["database"]

	if ps.username == "" {
		return fmt.Errorf("no username provided")
	}

	// Check if user exists
	user := ps.userDB.GetUser(ps.username)
	if user == nil {
		// Send error and close
		errMsg := postgresql.CreateErrorResponse("28P01", "authentication failed")
		ps.clientConn.Write(errMsg)
		return fmt.Errorf("unknown user: %s", ps.username)
	}

	// Authenticate based on auth mode
	// For now, use passthrough mode to let backend handle auth
	if ps.userDB == nil || ps.userDB.GetUser(ps.username) == nil {
		// Passthrough: just connect to backend and let it handle auth
		return ps.connectToBackend(ctx)
	}

	// Interception mode: handle auth ourselves
	return ps.handlePostgreSQLAuth(ctx, user)
}

// handlePostgreSQLAuth handles PostgreSQL authentication.
func (ps *ProxySession) handlePostgreSQLAuth(ctx context.Context, user *auth.User) error {
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
		errMsg := postgresql.CreateErrorResponse("28P01", "authentication failed: "+err.Error())
		ps.clientConn.Write(errMsg)
		return err
	}
	ps.scramState = state

	// Generate server-first
	serverFirst, err := scramServer.GenerateServerFirst(state)
	if err != nil {
		errMsg := postgresql.CreateErrorResponse("28P01", "authentication failed: "+err.Error())
		ps.clientConn.Write(errMsg)
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
		errMsg := postgresql.CreateErrorResponse("28P01", "authentication failed: invalid password")
		ps.clientConn.Write(errMsg)
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

	// In passthrough mode, forward startup to backend
	if ps.username != "" {
		// Forward startup message
		startup := ps.codec.(*postgresql.PGCodec).CreateStartupMessage(ps.username, ps.database)
		if _, err := serverConn.Conn().Write(startup); err != nil {
			return fmt.Errorf("failed to send startup to backend: %w", err)
		}

		// Forward authentication - just relay messages until AuthenticationOK
		if err := ps.forwardAuthFromBackend(); err != nil {
			return fmt.Errorf("backend authentication failed: %w", err)
		}
	}

	return nil
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
	// For MySQL, we need to connect to backend first to get the handshake
	// Connect to backend
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
	ps.poolSession.SetAuthDone()

	return nil
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
				return fmt.Errorf("authentication failed")
			case 0xfe: // Auth switch request
				// Read client response
				clientHeader := make([]byte, 4)
				if _, err := io.ReadFull(ps.clientConn, clientHeader); err != nil {
					return err
				}

				clientLength := int(clientHeader[0]) | int(clientHeader[1])<<8 | int(clientHeader[2])<<16
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

	// Forward Pre-Login from client to server
	if err := ps.forwardMSSQLPreLogin(); err != nil {
		return fmt.Errorf("pre-login failed: %w", err)
	}

	// Forward Login7 from client to server
	if err := ps.forwardMSSQLLogin7(); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	ps.authenticated.Store(true)
	ps.poolSession.SetAuthDone()

	return nil
}

// forwardMSSQLPreLogin forwards Pre-Login negotiation.
func (ps *ProxySession) forwardMSSQLPreLogin() error {
	// Read Pre-Login from client
	reader := bufio.NewReader(ps.clientConn)

	// Read TDS header (8 bytes)
	header := make([]byte, 8)
	if _, err := io.ReadFull(reader, header); err != nil {
		return fmt.Errorf("failed to read Pre-Login header: %w", err)
	}

	if header[0] != 0x12 { // PacketTypePreLogin
		return fmt.Errorf("expected Pre-Login packet, got 0x%02x", header[0])
	}

	length := binary.BigEndian.Uint16(header[2:4])
	payloadLen := int(length) - 8

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

// forwardMSSQLLogin7 forwards Login7 authentication.
func (ps *ProxySession) forwardMSSQLLogin7() error {
	// Read Login7 from client
	reader := bufio.NewReader(ps.clientConn)

	// Read TDS header (8 bytes)
	header := make([]byte, 8)
	if _, err := io.ReadFull(reader, header); err != nil {
		return fmt.Errorf("failed to read Login7 header: %w", err)
	}

	if header[0] != 0x10 { // PacketTypeLogin7
		return fmt.Errorf("expected Login7 packet, got 0x%02x", header[0])
	}

	length := binary.BigEndian.Uint16(header[2:4])
	payloadLen := int(length) - 8

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return fmt.Errorf("failed to read Login7 payload: %w", err)
	}

	// Extract username from Login7 for logging
	ps.extractLogin7Credentials(payload)

	// Forward to server
	login7 := make([]byte, length)
	copy(login7[0:8], header)
	copy(login7[8:], payload)

	if _, err := ps.serverConn.Conn().Write(login7); err != nil {
		return fmt.Errorf("failed to forward Login7: %w", err)
	}

	// Forward authentication response packets
	if err := ps.forwardMSSQLAuthResponse(); err != nil {
		return fmt.Errorf("auth response failed: %w", err)
	}

	return nil
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

	// Client -> Server
	go func() {
		errCh <- r.forwardClientToServer(ctx, clientConn, session, codec, ps)
	}()

	// Server -> Client
	go func() {
		errCh <- r.forwardServerToClient(ctx, clientConn, session, codec, ps)
	}()

	// Wait for first error
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && err != io.EOF {
			ps.log.Debug("Relay error", "error", err)
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

			// Check for transaction boundaries
			if ps.transactionMgr != nil {
				upperQuery := strings.ToUpper(strings.TrimSpace(query))
				if strings.HasPrefix(upperQuery, "BEGIN") || strings.HasPrefix(upperQuery, "START TRANSACTION") {
					// Register new transaction
					if ps.serverConn != nil {
						ps.transactionInfo = ps.transactionMgr.Register(ps.id, ps.serverConn.ID())
					}
				} else if strings.HasPrefix(upperQuery, "COMMIT") || strings.HasPrefix(upperQuery, "ROLLBACK") || strings.HasPrefix(upperQuery, "END") {
					// End transaction
					if ps.transactionInfo != nil {
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

		// Handle prepared statements (Parse, Bind, Execute, Close)
		if ps.poolSession.PreparedStatements() != nil && ps.config.Body == "postgresql" {
			switch {
			case codec.IsPrepare(msg):
				// Parse message - register in cache
				pgCodec := codec.(*postgresql.PGCodec)
				stmtName, _ := pgCodec.ExtractStatementName(msg)
				query, _ := pgCodec.ExtractQuery(msg)
				if query != "" {
					ps.poolSession.PreparedStatements().Register(stmtName, query, nil)
					ps.log.Debug("Prepared statement registered",
						"name", stmtName,
						"query", query[:min(len(query), 50)],
					)
				}

			case codec.IsClose(msg):
				// Close message - remove from cache
				// Close message format: 'C' + type + name
				// Type: 'S' = statement, 'P' = portal
				if len(msg.Payload) > 2 && msg.Payload[0] == 'S' {
					// Statement close
					namePos := 1
					for namePos < len(msg.Payload) && msg.Payload[namePos] != 0 {
						namePos++
					}
					stmtName := string(msg.Payload[1:namePos])
					ps.poolSession.PreparedStatements().Register(stmtName, "", nil)
					ps.log.Debug("Prepared statement closed", "name", stmtName)
				}
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
	var response bytes.Buffer
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

		// Write message to client
		if err := codec.WriteMessage(clientConn, msg); err != nil {
			return err
		}

		// Check for transaction state changes in ReadyForQuery
		if msg.Type == 'Z' && len(msg.Payload) > 0 {
			status := msg.Payload[0]
			switch status {
			case 'I': // Idle (not in transaction)
				ps.poolSession.SetInTransaction(false)
				ps.OnQueryComplete()
			case 'T', 'E': // In transaction block or failed transaction
				ps.poolSession.SetInTransaction(true)
			}

			// Log query completion for non-cached queries
			if ps.queryLogger != nil && ps.currentQuery != "" {
				duration := time.Since(ps.queryStartTime)
				backendAddr := ""
				if ps.serverConn != nil {
					backendAddr = ps.serverConn.Conn().RemoteAddr().String()
				}
				ps.queryLogger.LogQuery(logger.QueryLogEntry{
					Timestamp:   ps.queryStartTime,
					QueryID:     fmt.Sprintf("%d-%d", ps.id, ps.queryCount.Load()),
					Pool:        ps.config.Name,
					ClientAddr:  ps.clientConn.RemoteAddr().String(),
					BackendAddr: backendAddr,
					Username:    ps.username,
					Database:    ps.database,
					Query:       ps.currentQuery,
					QueryHash:   cache.GenerateKey(ps.currentQuery).String(),
					Duration:    duration,
					RowsReturned: rowCount,
					IsCached:    false,
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
}

// SetDeadline sets read/write deadlines on the connection.
func SetDeadline(conn net.Conn, timeout time.Duration) {
	if timeout > 0 {
		conn.SetDeadline(time.Now().Add(timeout))
	}
}
