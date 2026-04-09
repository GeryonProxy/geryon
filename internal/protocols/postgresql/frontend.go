package postgresql

import (
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

// Frontend handles PostgreSQL client connections.
type Frontend struct {
	conn      net.Conn
	pgConn    *Connection
	pool      *pool.Pool
	userDB    *auth.UserDatabase
	log       *logger.Logger

	// Session state
	state       FrontendState
	user        *auth.User
	database    string
	processID   int32
	secretKey   int32

	// Prepared statements and portals
	preparedStmts map[string]*PreparedStatement
	portals       map[string]*Portal

	// Connection to backend
	backendConn net.Conn
	backendMu   sync.Mutex

	// Shutdown
	closed atomic.Bool
}

// FrontendState represents the connection state.
type FrontendState int

const (
	StateStartup FrontendState = iota
	StateSSLHandshake
	StateAuthentication
	StateIdle
	StateActive
	StateCopy
	StateClosed
)

// NewFrontend creates a new PostgreSQL frontend handler.
func NewFrontend(conn net.Conn, p *pool.Pool, userDB *auth.UserDatabase, log *logger.Logger) *Frontend {
	return &Frontend{
		conn:          conn,
		pgConn:        NewConnection(conn),
		pool:          p,
		userDB:        userDB,
		log:           log,
		state:         StateStartup,
		preparedStmts: make(map[string]*PreparedStatement),
		portals:       make(map[string]*Portal),
	}
}

// Handle handles the client connection.
func (f *Frontend) Handle() error {
	defer f.cleanup()

	// Generate process ID and secret key
	f.processID = int32(time.Now().Unix())
	secretBytes := make([]byte, 4)
	rand.Read(secretBytes)
	f.secretKey = int32(secretBytes[0])<<24 | int32(secretBytes[1])<<16 |
		int32(secretBytes[2])<<8 | int32(secretBytes[3])

	// Read startup message
	startup, err := f.pgConn.ReadStartupMessage()
	if err != nil {
		if err.Error() == "SSL request" {
			// Handle SSL request
			return f.handleSSLRequest()
		}
		return fmt.Errorf("failed to read startup: %w", err)
	}

	// Extract user and database from startup parameters
	username := startup.Parameters["user"]
	database := startup.Parameters["database"]
	if database == "" {
		database = username // Default to username
	}

	f.database = database

	// Authenticate user
	if err := f.authenticate(username); err != nil {
		f.log.Warn("Authentication failed", "user", username, "error", err)
		return err
	}

	f.state = StateIdle

	// Send startup complete messages
	if err := f.sendStartupComplete(); err != nil {
		return err
	}

	// Main message loop
	for !f.closed.Load() {
		if err := f.handleMessage(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}

	return nil
}

// handleSSLRequest handles SSL upgrade request.
func (f *Frontend) handleSSLRequest() error {
	// For now, deny SSL (respond with 'N')
	// In production, this would upgrade to TLS
	_, err := f.conn.Write([]byte("N"))
	return err
}

// authenticate performs user authentication.
func (f *Frontend) authenticate(username string) error {
	user := f.userDB.GetUser(username)
	if user == nil {
		// Send error and return
		f.pgConn.SendErrorResponse("FATAL", "28P01", "authentication failed")
		return fmt.Errorf("unknown user: %s", username)
	}

	f.user = user

	// Check authentication method
	authMethod := "md5" // Default

	switch authMethod {
	case "trust":
		// No authentication needed
		return f.pgConn.SendAuthenticationOK()

	case "md5":
		// Send MD5 challenge
		salt := make([]byte, 4)
		rand.Read(salt)

		if err := f.pgConn.SendAuthenticationMD5(salt); err != nil {
			return err
		}

		// Read password response
		msg, err := f.pgConn.ReadMessage()
		if err != nil {
			return err
		}

		if msg.Type != MsgPasswordMessage {
			return fmt.Errorf("expected password message, got %c", msg.Type)
		}

		pwMsg, err := ParsePasswordMessage(msg.Data)
		if err != nil {
			return err
		}

		// For now, accept any password (implement real verification)
		_ = pwMsg
		return f.pgConn.SendAuthenticationOK()

	case "scram-sha-256":
		// Send SCRAM-SHA-256 request
		return f.pgConn.SendAuthenticationCleartext() // Fallback for now

	default:
		return f.pgConn.SendAuthenticationOK()
	}
}

// sendStartupComplete sends startup complete messages.
func (f *Frontend) sendStartupComplete() error {
	// Send parameter status messages
	params := map[string]string{
		"server_version": "14.0 Geryon",
		"server_encoding": "UTF8",
		"client_encoding": "UTF8",
		"DateStyle": "ISO, MDY",
		"TimeZone": "UTC",
		"integer_datetimes": "on",
		"standard_conforming_strings": "on",
	}

	for name, value := range params {
		if err := f.pgConn.SendParameterStatus(name, value); err != nil {
			return err
		}
	}

	// Send backend key data
	if err := f.pgConn.SendBackendKeyData(f.processID, f.secretKey); err != nil {
		return err
	}

	// Send ready for query
	return f.pgConn.SendReadyForQuery(TxIdle)
}

// handleMessage handles a single client message.
func (f *Frontend) handleMessage() error {
	msg, err := f.pgConn.ReadMessage()
	if err != nil {
		return err
	}

	switch msg.Type {
	case MsgQuery:
		return f.handleQuery(msg.Data)
	case MsgParse:
		return f.handleParse(msg.Data)
	case MsgBind:
		return f.handleBind(msg.Data)
	case MsgExecute:
		return f.handleExecute(msg.Data)
	case MsgSync:
		return f.handleSync()
	case MsgTerminate:
		f.closed.Store(true)
		return nil
	case MsgClose:
		return f.handleClose(msg.Data)
	case MsgDescribe:
		return f.handleDescribe(msg.Data)
	default:
		f.log.Debug("Unhandled message type", "type", string(msg.Type))
		return nil
	}
}

// handleQuery handles a simple query.
func (f *Frontend) handleQuery(data []byte) error {
	queryMsg, err := ParseQueryMessage(data)
	if err != nil {
		return err
	}

	query := queryMsg.Query
	f.log.Debug("Received query", "query", query[:min(len(query), 100)])

	// Check if this is a special command
	upperQuery := toUpper(query)

	switch {
	case upperQuery == "SELECT 1" || upperQuery == "SELECT 1;":
		// Health check query
		return f.sendSimpleResponse("SELECT 1", [][]byte{{'1'}}, []string{"?column?"})

	case hasPrefix(upperQuery, "SET "):
		// SET command - acknowledge
		if err := f.pgConn.SendCommandComplete("SET"); err != nil {
			return err
		}
		return f.pgConn.SendReadyForQuery(TxIdle)

	case hasPrefix(upperQuery, "SHOW "):
		// SHOW command - return mock value
		return f.sendSimpleResponse("SHOW", [][]byte{[]byte("on")}, []string{"show"})

	case hasPrefix(upperQuery, "BEGIN") || hasPrefix(upperQuery, "START TRANSACTION"):
		f.state = StateActive
		if err := f.pgConn.SendCommandComplete("BEGIN"); err != nil {
			return err
		}
		return f.pgConn.SendReadyForQuery(TxActive)

	case hasPrefix(upperQuery, "COMMIT") || hasPrefix(upperQuery, "END"):
		f.state = StateIdle
		if err := f.pgConn.SendCommandComplete("COMMIT"); err != nil {
			return err
		}
		return f.pgConn.SendReadyForQuery(TxIdle)

	case hasPrefix(upperQuery, "ROLLBACK"):
		f.state = StateIdle
		if err := f.pgConn.SendCommandComplete("ROLLBACK"); err != nil {
			return err
		}
		return f.pgConn.SendReadyForQuery(TxIdle)

	default:
		// For other queries, send a mock response
		// In production, this would proxy to the backend
		return f.sendSimpleResponse("SELECT 0", nil, nil)
	}
}

// handleParse handles a parse message (prepared statement).
func (f *Frontend) handleParse(data []byte) error {
	parseMsg, err := ParseParseMessage(data)
	if err != nil {
		return err
	}

	// Store prepared statement
	stmt := &PreparedStatement{
		Name:  parseMsg.Name,
		Query: parseMsg.Query,
	}
	f.preparedStmts[parseMsg.Name] = stmt

	return f.pgConn.SendParseComplete()
}

// handleBind handles a bind message.
func (f *Frontend) handleBind(data []byte) error {
	bindMsg, err := ParseBindMessage(data)
	if err != nil {
		return err
	}

	// Get the prepared statement
	stmt, ok := f.preparedStmts[bindMsg.StatementName]
	if !ok {
		return f.pgConn.SendErrorResponse("ERROR", "26000", fmt.Sprintf("prepared statement %s does not exist", bindMsg.StatementName))
	}

	// Create portal
	portal := &Portal{
		Name:             bindMsg.PortalName,
		Statement:        stmt,
		ParameterFormats: bindMsg.ParameterFormats,
		Parameters:       bindMsg.Parameters,
		ResultFormats:    bindMsg.ResultFormats,
	}
	f.portals[bindMsg.PortalName] = portal

	return f.pgConn.SendBindComplete()
}

// handleExecute handles an execute message.
func (f *Frontend) handleExecute(data []byte) error {
	execMsg, err := ParseExecuteMessage(data)
	if err != nil {
		return err
	}

	// Get the portal
	portal, ok := f.portals[execMsg.PortalName]
	if !ok {
		return f.pgConn.SendErrorResponse("ERROR", "34000", fmt.Sprintf("portal %s does not exist", execMsg.PortalName))
	}

	// Execute the query (simplified)
	f.log.Debug("Executing prepared statement", "query", portal.Statement.Query[:min(len(portal.Statement.Query), 100)])

	// Send command complete
	if err := f.pgConn.SendCommandComplete("SELECT 1"); err != nil {
		return err
	}

	return nil
}

// handleSync handles a sync message.
func (f *Frontend) handleSync() error {
	return f.pgConn.SendReadyForQuery(f.getTxStatus())
}

// handleClose handles a close message.
func (f *Frontend) handleClose(data []byte) error {
	// Parse close message
	if len(data) < 2 {
		return nil
	}

	closeType := data[0]
	name := string(data[1:len(data)-1]) // null-terminated

	switch closeType {
	case 'S': // Prepared statement
		delete(f.preparedStmts, name)
	case 'P': // Portal
		delete(f.portals, name)
	}

	return f.pgConn.SendCloseComplete()
}

// handleDescribe handles a describe message.
func (f *Frontend) handleDescribe(data []byte) error {
	if len(data) < 2 {
		return nil
	}

	describeType := data[0]
	name := string(data[1:len(data)-1]) // null-terminated

	switch describeType {
	case 'S': // Prepared statement
		stmt, ok := f.preparedStmts[name]
		if !ok {
			return f.pgConn.SendErrorResponse("ERROR", "26000", fmt.Sprintf("prepared statement %s does not exist", name))
		}
		// Send parameter description
		// For now, send NoData
		_ = stmt
		return f.pgConn.SendNoData()

	case 'P': // Portal
		portal, ok := f.portals[name]
		if !ok {
			return f.pgConn.SendErrorResponse("ERROR", "34000", fmt.Sprintf("portal %s does not exist", name))
		}
		// Send row description
		// For now, send NoData
		_ = portal
		return f.pgConn.SendNoData()
	}

	return nil
}

// sendSimpleResponse sends a simple query response.
func (f *Frontend) sendSimpleResponse(tag string, rows [][]byte, columns []string) error {
	// Send row description if there are columns
	if len(columns) > 0 {
		fields := make([]FieldDescription, len(columns))
		for i, col := range columns {
			fields[i] = FieldDescription{
				Name:     col,
				TypeOID:  25, // text
				TypeSize: -1,
				Format:   0,
			}
		}
		if err := f.pgConn.SendRowDescription(fields); err != nil {
			return err
		}
	}

	// Send data rows
	for _, row := range rows {
		values := [][]byte{row}
		if err := f.pgConn.SendDataRow(values); err != nil {
			return err
		}
	}

	// Send command complete
	if err := f.pgConn.SendCommandComplete(tag); err != nil {
		return err
	}

	// Send ready for query
	return f.pgConn.SendReadyForQuery(f.getTxStatus())
}

// getTxStatus returns current transaction status.
func (f *Frontend) getTxStatus() byte {
	if f.state == StateActive {
		return TxActive
	}
	return TxIdle
}

// cleanup cleans up resources.
func (f *Frontend) cleanup() {
	f.closed.Store(true)
	f.state = StateClosed

	f.backendMu.Lock()
	if f.backendConn != nil {
		f.backendConn.Close()
	}
	f.backendMu.Unlock()

	f.conn.Close()

	f.log.Info("Frontend connection closed", "process_id", f.processID)
}

// Helper functions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func toUpper(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c = c - 'a' + 'A'
		}
		result[i] = c
	}
	return string(result)
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
