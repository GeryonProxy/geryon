package pool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

// Session represents a client's connection lifecycle.
type Session struct {
	id          uint64
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	pool        *Pool
	serverConn  *ServerConn
	strategy    Strategy
	user        string
	database    string
	authDone    atomic.Bool
	inTxn       atomic.Bool
	autoCommit  atomic.Bool
	txnStart    time.Time
	startedAt   time.Time
	lastActive  atomic.Value // time.Time
	queryCount  atomic.Int64
	bytesIn     atomic.Int64
	bytesOut    atomic.Int64
	lastQuery   string
	targetRole  atomic.Value // string: "primary" or "replica"
	stmtTracker *SessionPreparedStatements
}

// SessionStats contains session statistics.
type SessionStats struct {
	ID           uint64    `json:"id"`
	Pool         string    `json:"pool"`
	User         string    `json:"user"`
	Database     string    `json:"database"`
	AuthDone     bool      `json:"auth_done"`
	InTxn        bool      `json:"in_transaction"`
	StartedAt    time.Time `json:"started_at"`
	LastActive   time.Time `json:"last_active"`
	QueryCount   int64     `json:"query_count"`
	BytesIn      int64     `json:"bytes_in"`
	BytesOut     int64     `json:"bytes_out"`
	ServerConnID uint64    `json:"server_conn_id,omitempty"`
}

var (
	sessionIDCounter atomic.Uint64
)

// NewSession creates a new client session.
func NewSession(ctx context.Context, cancel context.CancelFunc, pool *Pool, strategy Strategy) *Session {
	now := time.Now()
	s := &Session{
		id:          sessionIDCounter.Add(1),
		ctx:         ctx,
		cancel:      cancel,
		pool:        pool,
		strategy:    strategy,
		startedAt:   now,
		stmtTracker: NewSessionPreparedStatements(pool.PreparedStatementCache()),
	}
	s.lastActive.Store(now)
	s.autoCommit.Store(true)
	return s
}

// ID returns the session ID.
func (s *Session) ID() uint64 {
	return s.id
}

// Pool returns the pool this session belongs to.
func (s *Session) Pool() *Pool {
	return s.pool
}

// Strategy returns the session's strategy.
func (s *Session) Strategy() Strategy {
	return s.strategy
}

// Close cancels the session context and releases resources.
func (s *Session) Close() {
	if s.cancel != nil {
		s.cancel()
	}
}

// ServerConn returns the assigned server connection.
func (s *Session) ServerConn() *ServerConn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.serverConn
}

// SetServerConn sets the server connection.
func (s *Session) SetServerConn(conn *ServerConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serverConn = conn
}

// User returns the authenticated user.
func (s *Session) User() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.user
}

// SetUser sets the authenticated user.
func (s *Session) SetUser(user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.user = user
}

// Database returns the database name.
func (s *Session) Database() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.database
}

// SetDatabase sets the database name.
func (s *Session) SetDatabase(database string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.database = database
}

// AuthDone returns true if authentication is complete.
func (s *Session) AuthDone() bool {
	return s.authDone.Load()
}

// SetAuthDone marks authentication as complete.
func (s *Session) SetAuthDone() {
	s.authDone.Store(true)
}

// InTransaction returns true if the session is in a transaction.
func (s *Session) InTransaction() bool {
	return s.inTxn.Load()
}

// SetInTransaction sets the transaction state.
func (s *Session) SetInTransaction(inTxn bool) {
	s.inTxn.Store(inTxn)
	if inTxn {
		s.txnStart = time.Now()
	}
}

// AutoCommitRelease returns true if the connection should be released after autocommit query.
func (s *Session) AutoCommitRelease() bool {
	return s.autoCommit.Load()
}

// SetAutoCommitRelease sets the autocommit release flag.
func (s *Session) SetAutoCommitRelease(release bool) {
	s.autoCommit.Store(release)
}

// TransactionStart returns the transaction start time.
func (s *Session) TransactionStart() time.Time {
	return s.txnStart
}

// StartedAt returns when the session started.
func (s *Session) StartedAt() time.Time {
	return s.startedAt
}

// LastActive returns when the session was last active.
func (s *Session) LastActive() time.Time {
	return s.lastActive.Load().(time.Time)
}

// UpdateLastActive updates the last active timestamp.
func (s *Session) UpdateLastActive() {
	s.lastActive.Store(time.Now())
}

// QueryCount returns the number of queries executed.
func (s *Session) QueryCount() int64 {
	return s.queryCount.Load()
}

// IncrementQueryCount increments the query counter.
func (s *Session) IncrementQueryCount() {
	s.queryCount.Add(1)
	s.UpdateLastActive()
}

// BytesIn returns the number of bytes received.
func (s *Session) BytesIn() int64 {
	return s.bytesIn.Load()
}

// AddBytesIn adds to the bytes received counter.
func (s *Session) AddBytesIn(n int64) {
	s.bytesIn.Add(n)
}

// BytesOut returns the number of bytes sent.
func (s *Session) BytesOut() int64 {
	return s.bytesOut.Load()
}

// AddBytesOut adds to the bytes sent counter.
func (s *Session) AddBytesOut(n int64) {
	s.bytesOut.Add(n)
}

// LastQuery returns the last query string.
func (s *Session) LastQuery() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastQuery
}

// SetLastQuery sets the last query string.
func (s *Session) SetLastQuery(query string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastQuery = query
}

// TargetRole returns the target backend role for the next query.
func (s *Session) TargetRole() string {
	if v := s.targetRole.Load(); v != nil {
		return v.(string)
	}
	return ""
}

// SetTargetRole sets the target backend role ("primary" or "replica") for the next query.
func (s *Session) SetTargetRole(role string) {
	s.targetRole.Store(role)
}

// PreparedStatements returns the session's prepared statement tracker.
func (s *Session) PreparedStatements() *SessionPreparedStatements {
	return s.stmtTracker
}

// Stats returns session statistics.
func (s *Session) Stats() SessionStats {
	s.mu.RLock()
	serverConnID := uint64(0)
	serverConn := s.serverConn // Capture reference under lock
	s.mu.RUnlock()
	if serverConn != nil {
		serverConnID = serverConn.ID()
	}

	return SessionStats{
		ID:           s.id,
		Pool:         s.pool.Name(),
		User:         s.user,
		Database:     s.database,
		AuthDone:     s.authDone.Load(),
		InTxn:        s.inTxn.Load(),
		StartedAt:    s.startedAt,
		LastActive:   s.LastActive(),
		QueryCount:   s.queryCount.Load(),
		BytesIn:      s.bytesIn.Load(),
		BytesOut:     s.bytesOut.Load(),
		ServerConnID: serverConnID,
	}
}

// HandleMessage handles an incoming message using the session strategy.
// This implements the strategy pattern for different pooling modes.
func (s *Session) HandleMessage(msg *common.Message) error {
	if msg == nil {
		return nil
	}

	codec := s.pool.Codec()
	if codec == nil {
		return fmt.Errorf("pool codec not available")
	}

	// Update activity timestamp
	s.lastActive.Store(time.Now())

	// Track bytes in
	if msg.Raw != nil {
		s.bytesIn.Add(int64(len(msg.Raw)))
	}

	// Handle different message types based on strategy
	ctx := s.ctx

	// Transaction boundary detection
	if codec.IsTransactionBegin(msg) {
		s.inTxn.Store(true)
		s.txnStart = time.Now()
		if err := s.strategy.OnTransactionBegin(s); err != nil {
			return err
		}
	}

	// Handle query messages
	if codec.IsQuery(msg) || codec.IsExecute(msg) {
		s.queryCount.Add(1)

		// Extract query for logging/tracing
		query, err := codec.ExtractQuery(msg)
		if err == nil && query != "" {
			s.SetLastQuery(query)
		}

		// Acquire server connection via strategy
		serverConn, err := s.strategy.OnQuery(ctx, s, msg)
		if err != nil {
			return fmt.Errorf("failed to acquire server connection: %w", err)
		}

		if serverConn == nil {
			return fmt.Errorf("no server connection available")
		}

		// Forward message to server
		if err := codec.WriteMessage(serverConn.Conn(), msg); err != nil {
			return fmt.Errorf("failed to forward message to server: %w", err)
		}

		// Notify strategy that query is complete
		if err := s.strategy.OnQueryComplete(s); err != nil {
			return err
		}
	}

	// Transaction end detection
	if codec.IsTransactionEnd(msg) {
		s.inTxn.Store(false)
		if err := s.strategy.OnTransactionEnd(s); err != nil {
			return err
		}
	}

	// Handle prepared statement messages
	if codec.IsPrepare(msg) {
		// Track prepared statement
		if s.stmtTracker != nil {
			query, _ := codec.ExtractQuery(msg)
			if query != "" {
				s.stmtTracker.Add(query)
			}
		}
	}

	// Handle close messages
	if codec.IsClose(msg) {
		// Clean up any resources
	}

	return nil
}

// GetLastQuery returns the last executed query.
func (s *Session) GetLastQuery() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastQuery
}
