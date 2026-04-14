package pool

import (
	"context"
	"fmt"

	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

// Strategy defines the interface for pool mode-specific behavior.
type Strategy interface {
	// OnClientConnect is called when a client connects.
	OnClientConnect(ctx context.Context, s *Session) error

	// OnClientDisconnect is called when a client disconnects.
	OnClientDisconnect(s *Session) error

	// OnQuery is called when a query message arrives.
	// Returns the server connection to use (may acquire from pool).
	OnQuery(ctx context.Context, s *Session, msg *common.Message) (*ServerConn, error)

	// OnQueryComplete is called when query response is complete.
	// May release server connection back to pool.
	OnQueryComplete(s *Session) error

	// OnTransactionBegin is called on BEGIN/START TRANSACTION.
	OnTransactionBegin(s *Session) error

	// OnTransactionEnd is called on COMMIT/ROLLBACK.
	OnTransactionEnd(s *Session) error
}

// SessionStrategy implements session pooling.
// Client gets a dedicated server connection for entire session lifetime.
type SessionStrategy struct {
	pool *Pool
}

// NewSessionStrategy creates a new session strategy.
func NewSessionStrategy(pool *Pool) *SessionStrategy {
	return &SessionStrategy{pool: pool}
}

// OnClientConnect assigns a server connection immediately.
func (ss *SessionStrategy) OnClientConnect(ctx context.Context, s *Session) error {
	conn, err := ss.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire server connection: %w", err)
	}
	s.SetServerConn(conn)
	return nil
}

// OnClientDisconnect releases the server connection.
func (ss *SessionStrategy) OnClientDisconnect(s *Session) error {
	if conn := s.ServerConn(); conn != nil {
		ss.pool.Release(conn)
		s.SetServerConn(nil)
	}
	return nil
}

// OnQuery returns the existing server connection, respecting target role for read/write splitting.
func (ss *SessionStrategy) OnQuery(ctx context.Context, s *Session, msg *common.Message) (*ServerConn, error) {
	conn := s.ServerConn()
	targetRole := s.TargetRole()

	// If target role is set and differs from current connection's role (or no conn held),
	// acquire a connection to the correct backend
	if targetRole != "" && conn != nil {
		currentRole := conn.Backend().Role
		if currentRole != targetRole {
			// Role changed mid-session - release old conn and acquire new one
			ss.pool.Release(conn)
			conn = nil
		}
	}

	if conn == nil {
		var err error
		if targetRole != "" {
			conn, err = ss.pool.AcquireToRole(ctx, targetRole)
		} else {
			conn, err = ss.pool.Acquire(ctx)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to acquire server connection: %w", err)
		}
		s.SetServerConn(conn)
	}

	return conn, nil
}

// OnQueryComplete does nothing in session mode (connection held).
func (ss *SessionStrategy) OnQueryComplete(s *Session) error {
	return nil
}

// OnTransactionBegin does nothing in session mode.
func (ss *SessionStrategy) OnTransactionBegin(s *Session) error {
	s.SetInTransaction(true)
	return nil
}

// OnTransactionEnd does nothing in session mode.
func (ss *SessionStrategy) OnTransactionEnd(s *Session) error {
	s.SetInTransaction(false)
	return nil
}

// TransactionStrategy implements transaction pooling.
// Server connection assigned at transaction start, released at end.
type TransactionStrategy struct {
	pool *Pool
}

// NewTransactionStrategy creates a new transaction strategy.
func NewTransactionStrategy(pool *Pool) *TransactionStrategy {
	return &TransactionStrategy{pool: pool}
}

// OnClientConnect defers server assignment until first query.
func (ts *TransactionStrategy) OnClientConnect(ctx context.Context, s *Session) error {
	// No immediate server assignment
	return nil
}

// OnClientDisconnect releases any held server connection.
func (ts *TransactionStrategy) OnClientDisconnect(s *Session) error {
	if conn := s.ServerConn(); conn != nil {
		ts.pool.Release(conn)
		s.SetServerConn(nil)
	}
	return nil
}

// OnQuery acquires a connection if needed and returns it.
func (ts *TransactionStrategy) OnQuery(ctx context.Context, s *Session, msg *common.Message) (*ServerConn, error) {
	// If we already have a connection, use it
	if conn := s.ServerConn(); conn != nil {
		return conn, nil
	}

	// Acquire a new connection, respecting target role if set
	var conn *ServerConn
	var err error
	if role := s.TargetRole(); role != "" {
		conn, err = ts.pool.AcquireToRole(ctx, role)
	} else {
		conn, err = ts.pool.Acquire(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to acquire server connection: %w", err)
	}
	s.SetServerConn(conn)

	// Check if this starts an implicit transaction (not autocommit)
	if !s.InTransaction() && !s.Pool().Codec().IsTransactionBegin(msg) {
		// For autocommit mode, we'll release after query completes
		s.SetAutoCommitRelease(true)
	}

	return conn, nil
}

// OnQueryComplete releases the connection if not in a transaction.
func (ts *TransactionStrategy) OnQueryComplete(s *Session) error {
	// Only release if not in an explicit transaction and autocommit is enabled
	if !s.InTransaction() && s.AutoCommitRelease() {
		if conn := s.ServerConn(); conn != nil {
			ts.pool.Release(conn)
			s.SetServerConn(nil)
			s.SetAutoCommitRelease(false)
		}
	}
	return nil
}

// OnTransactionBegin marks the session as in-transaction.
func (ts *TransactionStrategy) OnTransactionBegin(s *Session) error {
	s.SetInTransaction(true)
	return nil
}

// OnTransactionEnd releases the connection.
func (ts *TransactionStrategy) OnTransactionEnd(s *Session) error {
	s.SetInTransaction(false)

	// Release server connection after transaction ends
	if conn := s.ServerConn(); conn != nil {
		ts.pool.Release(conn)
		s.SetServerConn(nil)
	}

	ts.pool.IncrementTxnCount()
	return nil
}

// StatementStrategy implements statement pooling.
// Server connection assigned per statement, released immediately after.
type StatementStrategy struct {
	pool *Pool
}

// NewStatementStrategy creates a new statement strategy.
func NewStatementStrategy(pool *Pool) *StatementStrategy {
	return &StatementStrategy{pool: pool}
}

// OnClientConnect defers server assignment.
func (ss *StatementStrategy) OnClientConnect(ctx context.Context, s *Session) error {
	return nil
}

// OnClientDisconnect ensures no connection is leaked.
func (ss *StatementStrategy) OnClientDisconnect(s *Session) error {
	if conn := s.ServerConn(); conn != nil {
		ss.pool.Release(conn)
		s.SetServerConn(nil)
	}
	return nil
}

// OnQuery acquires a connection for each statement.
func (ss *StatementStrategy) OnQuery(ctx context.Context, s *Session, msg *common.Message) (*ServerConn, error) {
	// Always acquire a fresh connection, respecting target role if set
	var conn *ServerConn
	var err error
	if role := s.TargetRole(); role != "" {
		conn, err = ss.pool.AcquireToRole(ctx, role)
	} else {
		conn, err = ss.pool.Acquire(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to acquire server connection: %w", err)
	}
	s.SetServerConn(conn)
	return conn, nil
}

// OnQueryComplete always releases the connection.
func (ss *StatementStrategy) OnQueryComplete(s *Session) error {
	if conn := s.ServerConn(); conn != nil {
		ss.pool.Release(conn)
		s.SetServerConn(nil)
	}
	return nil
}

// OnTransactionBegin returns an error (transactions not supported in statement mode).
func (ss *StatementStrategy) OnTransactionBegin(s *Session) error {
	return fmt.Errorf("explicit transactions not supported in statement pooling mode")
}

// OnTransactionEnd is a no-op (transactions shouldn't happen in statement mode).
func (ss *StatementStrategy) OnTransactionEnd(s *Session) error {
	return nil
}

// StrategyFactory creates strategies based on pool mode.
type StrategyFactory struct{}

// CreateStrategy creates the appropriate strategy for the pool mode.
func (sf *StrategyFactory) CreateStrategy(pool *Pool) (Strategy, error) {
	switch pool.Mode() {
	case ModeSession:
		return NewSessionStrategy(pool), nil
	case ModeTransaction:
		return NewTransactionStrategy(pool), nil
	case ModeStatement:
		return NewStatementStrategy(pool), nil
	default:
		return nil, fmt.Errorf("unknown pool mode: %v", pool.Mode())
	}
}

// Global strategy factory instance.
var DefaultStrategyFactory = &StrategyFactory{}
