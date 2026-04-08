package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

// Listener manages incoming client connections for a pool.
type Listener struct {
	mu        sync.RWMutex
	pool      *pool.Pool
	config    *config.PoolConfig
	codec     common.Codec
	listener  net.Listener
	address   string
	active    atomic.Bool
	sessions  map[uint64]*ProxySession
	tlsConfig *tls.Config
	ctx       context.Context
	cancel    context.CancelFunc
	log       *logger.Logger
}

// NewListener creates a new proxy listener.
func NewListener(pool *pool.Pool, cfg *config.PoolConfig, codec common.Codec, log *logger.Logger) (*Listener, error) {
	ctx, cancel := context.WithCancel(context.Background())

	l := &Listener{
		pool:     pool,
		config:   cfg,
		codec:    codec,
		address:  fmt.Sprintf("%s:%d", cfg.Listen.Host, cfg.Listen.Port),
		sessions: make(map[uint64]*ProxySession),
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
	session, err := NewProxySession(conn, l.pool, l.codec, l.log)
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
	id         uint64
	clientConn net.Conn
	pool       *pool.Pool
	codec      common.Codec
	poolSession *pool.Session
	relay      *Relay
	log        *logger.Logger
	closed     atomic.Bool
	queryCount atomic.Int64
}

var (
	sessionIDCounter atomic.Uint64
)

// NewProxySession creates a new proxy session.
func NewProxySession(clientConn net.Conn, p *pool.Pool, codec common.Codec, log *logger.Logger) (*ProxySession, error) {
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
	// TODO: Implement proper PostgreSQL startup handshake
	// For now, just mark auth as done
	ps.poolSession.SetAuthDone()
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
		errCh <- r.forwardServerToClient(ctx, clientConn, session, codec)
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
func (r *Relay) forwardServerToClient(ctx context.Context, clientConn net.Conn, session *pool.Session, codec common.Codec) error {
	// This is a simplified version - in reality, we'd need to track
	// which server connection is active and forward from that

	// For now, just poll the active server connection
	// TODO: Implement proper server-to-client forwarding with response tracking
	return nil
}

// SetDeadline sets read/write deadlines on the connection.
func SetDeadline(conn net.Conn, timeout time.Duration) {
	if timeout > 0 {
		conn.SetDeadline(time.Now().Add(timeout))
	}
}
