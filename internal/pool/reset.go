package pool

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"time"

	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

// ConnectionResetter handles connection state reset for different protocols.
type ConnectionResetter interface {
	// Reset resets the connection state before returning to pool.
	Reset(conn net.Conn, codec common.Codec) error
	// Protocol returns the protocol this resetter supports.
	Protocol() common.Protocol
}

// PostgreSQLResetter handles PostgreSQL connection resets.
type PostgreSQLResetter struct{}

// Protocol returns the PostgreSQL protocol.
func (r *PostgreSQLResetter) Protocol() common.Protocol {
	return common.ProtocolPostgreSQL
}

// Reset resets a PostgreSQL connection using DISCARD ALL.
func (r *PostgreSQLResetter) Reset(conn net.Conn, codec common.Codec) error {
	// PostgreSQL: Send DISCARD ALL command
	resetSequence := codec.GenerateResetSequence()

	for _, msg := range resetSequence {
		if err := codec.WriteMessage(conn, msg); err != nil {
			return fmt.Errorf("failed to write reset message: %w", err)
		}
	}

	// Read and discard responses with timeout
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetReadDeadline(time.Time{})

	// Drain responses
	for {
		if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			return err
		}
		_, err := codec.ReadMessage(conn)
		if err != nil {
			// Timeout or connection closed - expected
			break
		}
	}

	return nil
}

// MySQLResetter handles MySQL connection resets.
type MySQLResetter struct{}

// Protocol returns the MySQL protocol.
func (r *MySQLResetter) Protocol() common.Protocol {
	return common.ProtocolMySQL
}

// Reset resets a MySQL connection using COM_RESET_CONNECTION.
func (r *MySQLResetter) Reset(conn net.Conn, codec common.Codec) error {
	// MySQL: Send COM_RESET_CONNECTION
	resetSequence := codec.GenerateResetSequence()

	for _, msg := range resetSequence {
		if err := codec.WriteMessage(conn, msg); err != nil {
			return fmt.Errorf("failed to write reset message: %w", err)
		}
	}

	// Read OK packet response with timeout
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetReadDeadline(time.Time{})

	// Wait for OK packet
	_, err := codec.ReadMessage(conn)
	if err != nil {
		return fmt.Errorf("failed to read reset response: %w", err)
	}

	return nil
}

// MSSQLResetter handles MSSQL connection resets.
type MSSQLResetter struct{}

// Protocol returns the MSSQL protocol.
func (r *MSSQLResetter) Protocol() common.Protocol {
	return common.ProtocolMSSQL
}

// Reset resets an MSSQL connection using sp_reset_connection.
func (r *MSSQLResetter) Reset(conn net.Conn, codec common.Codec) error {
	// MSSQL: Send RPC request for sp_reset_connection
	resetSequence := codec.GenerateResetSequence()

	for _, msg := range resetSequence {
		if err := codec.WriteMessage(conn, msg); err != nil {
			return fmt.Errorf("failed to write reset message: %w", err)
		}
	}

	// Read and process response with timeout
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetReadDeadline(time.Time{})

	// Drain responses
	for {
		if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			return err
		}
		_, err := codec.ReadMessage(conn)
		if err != nil {
			// Timeout or connection closed - expected
			break
		}
	}

	return nil
}

// ResetterRegistry holds all protocol resetters.
type ResetterRegistry struct {
	resetters map[common.Protocol]ConnectionResetter
}

// NewResetterRegistry creates a new resetter registry with all protocols.
func NewResetterRegistry() *ResetterRegistry {
	return &ResetterRegistry{
		resetters: map[common.Protocol]ConnectionResetter{
			common.ProtocolPostgreSQL: &PostgreSQLResetter{},
			common.ProtocolMySQL:      &MySQLResetter{},
			common.ProtocolMSSQL:      &MSSQLResetter{},
		},
	}
}

// Get returns the resetter for the given protocol.
func (r *ResetterRegistry) Get(protocol common.Protocol) (ConnectionResetter, bool) {
	resetter, ok := r.resetters[protocol]
	return resetter, ok
}

// ResetConnection resets a connection using the appropriate resetter.
func ResetConnection(ctx context.Context, conn net.Conn, codec common.Codec) error {
	registry := NewResetterRegistry()
	resetter, ok := registry.Get(codec.Protocol())
	if !ok {
		return fmt.Errorf("no resetter for protocol %v", codec.Protocol())
	}

	// Apply timeout from context
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
		defer conn.SetDeadline(time.Time{})
	}

	return resetter.Reset(conn, codec)
}

// SmartResetOptions configures smart reset behavior.
type SmartResetOptions struct {
	// TrackSessionVars tracks which session variables were modified
	TrackSessionVars bool
	// TrackTempTables tracks temporary tables created
	TrackTempTables bool
	// TrackPreparedStmts tracks prepared statements
	TrackPreparedStmts bool
	// MinimizeRoundTrips minimizes round trips when possible
	MinimizeRoundTrips bool
}

// DefaultSmartResetOptions returns default smart reset options.
func DefaultSmartResetOptions() SmartResetOptions {
	return SmartResetOptions{
		TrackSessionVars:   true,
		TrackTempTables:    true,
		TrackPreparedStmts: true,
		MinimizeRoundTrips: true,
	}
}

// SmartResetter performs intelligent connection resets based on state tracking.
type SmartResetter struct {
	options SmartResetOptions
	state   ConnectionState
}

// ConnectionState tracks modifications made to a connection.
type ConnectionState struct {
	// SessionVarsModified lists modified session variables
	SessionVarsModified map[string]string
	// TempTablesCreated lists created temp tables
	TempTablesCreated []string
	// PreparedStmts lists prepared statements
	PreparedStmts map[string]bool
	// InTransaction indicates if connection is in a transaction
	InTransaction bool
}

// NewSmartResetter creates a new smart resetter.
func NewSmartResetter(opts SmartResetOptions) *SmartResetter {
	return &SmartResetter{
		options: opts,
		state: ConnectionState{
			SessionVarsModified: make(map[string]string),
			PreparedStmts:       make(map[string]bool),
		},
	}
}

// MarkSessionVarModified marks a session variable as modified.
func (s *SmartResetter) MarkSessionVarModified(name, value string) {
	if s.options.TrackSessionVars {
		s.state.SessionVarsModified[name] = value
	}
}

// MarkTempTableCreated marks a temp table as created.
func (s *SmartResetter) MarkTempTableCreated(name string) {
	if s.options.TrackTempTables {
		s.state.TempTablesCreated = append(s.state.TempTablesCreated, name)
	}
}

// MarkPreparedStmt marks a prepared statement.
func (s *SmartResetter) MarkPreparedStmt(name string) {
	if s.options.TrackPreparedStmts {
		s.state.PreparedStmts[name] = true
	}
}

// NeedsReset returns true if the connection needs to be reset.
func (s *SmartResetter) NeedsReset() bool {
	if s.state.InTransaction {
		return true
	}
	if len(s.state.SessionVarsModified) > 0 {
		return true
	}
	if len(s.state.TempTablesCreated) > 0 {
		return true
	}
	if len(s.state.PreparedStmts) > 0 && !s.options.MinimizeRoundTrips {
		return true
	}
	return false
}

// ResetState clears the tracked state.
func (s *SmartResetter) ResetState() {
	s.state = ConnectionState{
		SessionVarsModified: make(map[string]string),
		TempTablesCreated:   nil,
		PreparedStmts:       make(map[string]bool),
		InTransaction:       false,
	}
}

var validTableName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// isValidTableName validates that a table name contains only safe characters.
func isValidTableName(name string) bool {
	return validTableName.MatchString(name) && len(name) <= 128
}

// GetResetSQL returns SQL statements needed for reset (PostgreSQL specific).
func (s *SmartResetter) GetResetSQL() []string {
	var stmts []string

	if !s.NeedsReset() {
		return stmts
	}

	// Reset session variables
	if len(s.state.SessionVarsModified) > 0 {
		stmts = append(stmts, "RESET ALL")
	}

	// Drop temp tables (validated to prevent SQL injection)
	for _, table := range s.state.TempTablesCreated {
		if isValidTableName(table) {
			stmts = append(stmts, fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		}
	}

	// Deallocate prepared statements
	if len(s.state.PreparedStmts) > 0 && !s.options.MinimizeRoundTrips {
		stmts = append(stmts, "DEALLOCATE ALL")
	}

	return stmts
}
