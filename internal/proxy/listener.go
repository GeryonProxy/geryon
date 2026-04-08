package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
	"github.com/GeryonProxy/geryon/internal/protocol/common"
	"github.com/GeryonProxy/geryon/internal/protocol/postgresql"
)

// Listener manages incoming client connections for a pool.
type Listener struct {
	mu          sync.RWMutex
	pool        *pool.Pool
	config      *config.PoolConfig
	codec       common.Codec
	listener    net.Listener
	address     string
	active      atomic.Bool
	sessions    map[uint64]*ProxySession
	tlsConfig   *tls.Config
	userDB      *auth.UserDatabase
	ctx         context.Context
	cancel      context.CancelFunc
	log         *logger.Logger
}

// NewListener creates a new proxy listener.
func NewListener(pool *pool.Pool, cfg *config.PoolConfig, codec common.Codec, userDB *auth.UserDatabase, log *logger.Logger) (*Listener, error) {
	ctx, cancel := context.WithCancel(context.Background())

	l := &Listener{
		pool:     pool,
		config:   cfg,
		codec:    codec,
		address:  fmt.Sprintf("%s:%d", cfg.Listen.Host, cfg.Listen.Port),
		sessions: make(map[uint64]*ProxySession),
		userDB:   userDB,
		ctx:      ctx,
		cancel:   cancel,
		log:      log,
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

	// Create proxy session
	session, err := NewProxySession(conn, l.pool, l.codec, l.userDB, l.config, l.log)
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

// ProxySession represents a client connection session.
type ProxySession struct {
	id            uint64
	clientConn    net.Conn
	serverConn    *pool.ServerConn
	pool          *pool.Pool
	codec         common.Codec
	userDB        *auth.UserDatabase
	config        *config.PoolConfig
	poolSession   *pool.Session
	relay         *Relay
	log           *logger.Logger
	closed        atomic.Bool
	queryCount    atomic.Int64
	authenticated atomic.Bool
	username      string
	database      string
	scramState    *auth.SCRAMState
}

var (
	sessionIDCounter atomic.Uint64
)

// NewProxySession creates a new proxy session.
func NewProxySession(clientConn net.Conn, p *pool.Pool, codec common.Codec, userDB *auth.UserDatabase, cfg *config.PoolConfig, log *logger.Logger) (*ProxySession, error) {
	// Create pool strategy
	strategy, err := pool.DefaultStrategyFactory.CreateStrategy(p)
	if err != nil {
		return nil, fmt.Errorf("failed to create strategy: %w", err)
	}

	// Create pool session
	poolSession := pool.NewSession(p, strategy)

	ps := &ProxySession{
		id:          sessionIDCounter.Add(1),
		clientConn:  clientConn,
		pool:        p,
		codec:       codec,
		userDB:      userDB,
		config:      cfg,
		poolSession: poolSession,
		relay:       NewRelay(),
		log:         log,
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
	// TODO: Implement MySQL startup handshake
	return fmt.Errorf("MySQL startup not yet implemented")
}

// handleMSSQLStartup handles MSSQL startup handshake.
func (ps *ProxySession) handleMSSQLStartup(ctx context.Context) error {
	// TODO: Implement MSSQL startup handshake
	return fmt.Errorf("MSSQL startup not yet implemented")
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

		// Get server connection for this message
		serverConn, err := ps.OnQuery(ctx, msg)
		if err != nil {
			return err
		}

		// Write message to server
		if err := codec.WriteMessage(serverConn.Conn(), msg); err != nil {
			return err
		}

		// Handle extended query protocol (Sync message indicates end of extended query)
		if msg.Type == 'S' { // Sync
			ps.OnQueryComplete()
		}
	}
}

// forwardServerToClient forwards messages from server to client.
func (r *Relay) forwardServerToClient(ctx context.Context, clientConn net.Conn, session *pool.Session, codec common.Codec, ps *ProxySession) error {
	// Use the server connection from the pool session
	serverConn := ps.serverConn
	if serverConn == nil {
		return fmt.Errorf("no server connection available")
	}

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
		}
	}
}

// SetDeadline sets read/write deadlines on the connection.
func SetDeadline(conn net.Conn, timeout time.Duration) {
	if timeout > 0 {
		conn.SetDeadline(time.Now().Add(timeout))
	}
}
