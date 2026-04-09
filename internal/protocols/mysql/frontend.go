// Package mysql implements the MySQL wire protocol.
package mysql

import (
	"bufio"
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
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

// MySQL protocol constants
const (
	// Protocol version
	ProtocolVersion = 10

	// Capabilities
	ClientLongPassword     = 1 << 0
	ClientFoundRows        = 1 << 1
	ClientLongFlag         = 1 << 2
	ClientConnectWithDB    = 1 << 3
	ClientNoSchema         = 1 << 4
	ClientCompress         = 1 << 5
	ClientODBC             = 1 << 6
	ClientLocalFiles       = 1 << 7
	ClientIgnoreSpace      = 1 << 8
	ClientProtocol41       = 1 << 9
	ClientInteractive      = 1 << 10
	ClientSSL              = 1 << 11
	ClientIgnoreSigpipe    = 1 << 12
	ClientTransactions     = 1 << 13
	ClientReserved         = 1 << 14
	ClientSecureConnection = 1 << 15
	ClientMultiStatements  = 1 << 16
	ClientMultiResults     = 1 << 17
	ClientPluginAuth       = 1 << 19
	ClientConnectAttrs     = 1 << 20
	ClientPluginAuthLenencClientData = 1 << 21
	ClientSSLVerifyServerCert = 1 << 30
	ClientRememberOptions  = 1 << 31

	// Server capabilities
	ServerStatusInTransaction          = 1 << 0
	ServerStatusAutocommit             = 1 << 1
	ServerMoreResultsExists            = 1 << 3
	ServerStatusNoGoodIndexUsed        = 1 << 4
	ServerStatusNoIndexUsed            = 1 << 5
	ServerStatusCursorExists           = 1 << 6
	ServerStatusLastRowSent            = 1 << 7
	ServerStatusDBDropped              = 1 << 8
	ServerStatusNoBackslashEscapes     = 1 << 9
	ServerStatusMetadataChanged        = 1 << 10
	ServerQueryWasSlow                 = 1 << 11
	ServerPsOutParams                  = 1 << 12
	ServerStatusInTransReadonly        = 1 << 13
	ServerSessionStateChanged          = 1 << 14

	// Commands
	ComSleep = 0
	ComQuit = 1
	ComInitDB = 2
	ComQuery = 3
	ComFieldList = 4
	ComCreateDB = 5
	ComDropDB = 6
	ComRefresh = 7
	ComShutdown = 8
	ComStatistics = 9
	ComProcessInfo = 10
	ComConnect = 11
	ComProcessKill = 12
	ComDebug = 13
	ComPing = 14
	ComTime = 15
	ComDelayedInsert = 16
	ComChangeUser = 17
	ComBinlogDump = 18
	ComTableDump = 19
	ComConnectOut = 20
	ComRegisterSlave = 21
	ComStmtPrepare = 22
	ComStmtExecute = 23
	ComStmtSendLongData = 24
	ComStmtClose = 25
	ComStmtReset = 26
	ComSetOption = 27
	ComStmtFetch = 28

	// Response codes
	OK  = 0x00
	EOF = 0xfe
	ERR = 0xff
)

// Frontend handles MySQL client connections.
type Frontend struct {
	conn       net.Conn
	reader     *bufio.Reader
	writer     *bufio.Writer
	pool       *pool.Pool
	userDB     *auth.UserDatabase
	log        *logger.Logger

	// Session state
	state       FrontendState
	user        *auth.User
	database    string
	serverVer   string
	threadID    uint32
	charset     uint8
	status      uint16

	// Authentication
	authPlugin  string
	authData    []byte

	// Capabilities
	clientCaps  uint32
	serverCaps  uint32

	// TLS
	tlsConfig   *tls.Config
	tlsActive   bool

	// Prepared statements
	stmts       map[uint32]*PreparedStatement
	stmtIDGen   atomic.Uint32

	// Shutdown
	closed      atomic.Bool
	mu          sync.Mutex
}

// FrontendState represents the connection state.
type FrontendState int

const (
	StateHandshake FrontendState = iota
	StateAuthentication
	StateReady
	StateQuery
	StateClosed
)

// PreparedStatement represents a prepared statement.
type PreparedStatement struct {
	ID        uint32
	Query     string
	ParamCount uint16
	Columns   []*ColumnInfo
	Params    []*ColumnInfo
}

// ColumnInfo represents column metadata.
type ColumnInfo struct {
	Catalog    string
	Schema     string
	Table      string
	OrgTable   string
	Name       string
	OrgName    string
	Charset    uint16
	ColumnLen  uint32
	Type       uint8
	Flags      uint16
	Decimals   uint8
}

// NewFrontend creates a new MySQL frontend handler.
func NewFrontend(conn net.Conn, p *pool.Pool, userDB *auth.UserDatabase, log *logger.Logger) *Frontend {
	return &Frontend{
		conn:       conn,
		reader:     bufio.NewReader(conn),
		writer:     bufio.NewWriter(conn),
		pool:       p,
		userDB:     userDB,
		log:        log,
		state:      StateHandshake,
		serverVer:  "8.0.0-Geryon",
		charset:    33, // utf8mb4
		status:     ServerStatusAutocommit,
		stmts:      make(map[uint32]*PreparedStatement),
		serverCaps: defaultServerCapabilities(),
	}
}

// defaultServerCapabilities returns default server capabilities.
func defaultServerCapabilities() uint32 {
	return ClientLongPassword |
		ClientFoundRows |
		ClientLongFlag |
		ClientConnectWithDB |
		ClientProtocol41 |
		ClientInteractive |
		ClientTransactions |
		ClientSecureConnection |
		ClientMultiStatements |
		ClientMultiResults |
		ClientPluginAuth
}

// Handle handles the client connection.
func (f *Frontend) Handle() error {
	defer f.cleanup()

	// Send handshake
	if err := f.sendHandshake(); err != nil {
		return fmt.Errorf("failed to send handshake: %w", err)
	}

	// Receive handshake response
	if err := f.receiveHandshakeResponse(); err != nil {
		return fmt.Errorf("failed to receive handshake response: %w", err)
	}

	// Authenticate
	if err := f.authenticate(); err != nil {
		return err
	}

	f.state = StateReady

	// Main command loop
	for !f.closed.Load() {
		if err := f.handleCommand(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}

	return nil
}

// sendHandshake sends the initial handshake packet.
func (f *Frontend) sendHandshake() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Generate thread ID
	f.threadID = uint32(time.Now().Unix())

	// Generate auth data (20 bytes)
	f.authData = make([]byte, 20)
	for i := range f.authData {
		f.authData[i] = byte(time.Now().UnixNano() % 256)
	}

	// Build handshake packet
	data := make([]byte, 0, 128)

	// Protocol version
	data = append(data, ProtocolVersion)

	// Server version (null-terminated)
	data = append(data, f.serverVer...)
	data = append(data, 0)

	// Thread ID (4 bytes, little endian)
	data = append(data, byte(f.threadID), byte(f.threadID>>8), byte(f.threadID>>16), byte(f.threadID>>24))

	// Auth data part 1 (8 bytes)
	data = append(data, f.authData[:8]...)

	// Filler
	data = append(data, 0)

	// Server capabilities (lower 16 bits)
	data = append(data, byte(f.serverCaps), byte(f.serverCaps>>8))

	// Character set
	data = append(data, f.charset)

	// Server status
	data = append(data, byte(f.status), byte(f.status>>8))

	// Server capabilities (upper 16 bits)
	data = append(data, byte(f.serverCaps>>16), byte(f.serverCaps>>24))

	// Auth data length
	data = append(data, 21) // 20 bytes + 1

	// Reserved (10 zeros)
	data = append(data, make([]byte, 10)...)

	// Auth data part 2 (12 bytes)
	data = append(data, f.authData[8:20]...)

	// Auth plugin name
	f.authPlugin = "mysql_native_password"
	data = append(data, f.authPlugin...)
	data = append(data, 0)

	// Send packet
	return f.writePacket(0, data)
}

// receiveHandshakeResponse receives and parses the handshake response.
func (f *Frontend) receiveHandshakeResponse() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Read packet
	_, data, err := f.readPacket()
	if err != nil {
		return err
	}

	offset := 0

	// Client capabilities (4 bytes)
	f.clientCaps = binary.LittleEndian.Uint32(data[offset:offset+4])
	offset += 4

	// Max packet size (4 bytes)
	offset += 4

	// Character set (1 byte)
	f.charset = data[offset]
	offset++

	// Reserved (23 bytes)
	offset += 23

	// Username (null-terminated)
	usernameEnd := offset
	for usernameEnd < len(data) && data[usernameEnd] != 0 {
		usernameEnd++
	}
	username := string(data[offset:usernameEnd])
	offset = usernameEnd + 1

	// Auth response
	var authResponse []byte
	if f.clientCaps&ClientPluginAuthLenencClientData != 0 {
		// Length-encoded string
		length, read := readLengthEncodedInt(data[offset:])
		offset += read
		authResponse = data[offset:offset+int(length)]
		offset += int(length)
	} else if f.clientCaps&ClientSecureConnection != 0 {
		// Length (1 byte) + data
		length := int(data[offset])
		offset++
		authResponse = data[offset:offset+length]
		offset += length
	} else {
		// Null-terminated
		authEnd := offset
		for authEnd < len(data) && data[authEnd] != 0 {
			authEnd++
		}
		authResponse = data[offset:authEnd]
		offset = authEnd + 1
	}

	// Database (if ConnectWithDB)
	if f.clientCaps&ClientConnectWithDB != 0 && offset < len(data) {
		dbEnd := offset
		for dbEnd < len(data) && data[dbEnd] != 0 {
			dbEnd++
		}
		f.database = string(data[offset:dbEnd])
		offset = dbEnd + 1
	}

	// Look up user
	user := f.userDB.GetUser(username)
	if user == nil {
		return f.sendError(1045, "28000", "Access denied for user '"+username+"'@'%' (using password: YES)")
	}
	f.user = user

	// Verify password (simplified - always accept for now)
	_ = authResponse

	return nil
}

// authenticate sends OK packet after successful authentication.
func (f *Frontend) authenticate() error {
	return f.sendOK()
}

// handleCommand handles a single client command.
func (f *Frontend) handleCommand() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Read packet
	seq, data, err := f.readPacket()
	if err != nil {
		return err
	}
	_ = seq

	if len(data) == 0 {
		return nil
	}

	command := data[0]
	payload := data[1:]

	switch command {
	case ComQuit:
		f.closed.Store(true)
		return nil

	case ComInitDB:
		return f.handleInitDB(payload)

	case ComQuery:
		return f.handleQuery(payload)

	case ComPing:
		return f.sendOK()

	case ComStmtPrepare:
		return f.handleStmtPrepare(payload)

	case ComStmtExecute:
		return f.handleStmtExecute(payload)

	case ComStmtClose:
		return f.handleStmtClose(payload)

	case ComFieldList:
		return f.handleFieldList(payload)

	case ComRefresh:
		return f.sendOK()

	case ComStatistics:
		return f.handleStatistics()

	case ComProcessInfo:
		return f.handleProcessInfo()

	case ComSetOption:
		return f.sendOK()

	default:
		f.log.Debug("Unhandled MySQL command", "command", command)
		return f.sendOK()
	}
}

// handleInitDB handles database selection.
func (f *Frontend) handleInitDB(data []byte) error {
	f.database = string(data)
	return f.sendOK()
}

// handleQuery handles a query command.
func (f *Frontend) handleQuery(data []byte) error {
	query := string(data)
	f.log.Debug("MySQL query", "query", query[:min(len(query), 100)])

	// Check for special queries
	upperQuery := strings.ToUpper(strings.TrimSpace(query))

	switch {
	case upperQuery == "SELECT 1":
		return f.sendSimpleResult([][]interface{}{{1}}, []string{"1"})

	case strings.HasPrefix(upperQuery, "SET "):
		return f.sendOK()

	case strings.HasPrefix(upperQuery, "SHOW "):
		return f.handleShowQuery(upperQuery)

	default:
		// Send OK for other queries (simplified)
		return f.sendOK()
	}
}

// handleShowQuery handles SHOW commands.
func (f *Frontend) handleShowQuery(query string) error {
	if strings.Contains(query, "VARIABLES") {
		// Return some system variables
		return f.sendSimpleResult([][]interface{}{
			{"version", f.serverVer},
			{"character_set_server", "utf8mb4"},
			{"collation_server", "utf8mb4_general_ci"},
		}, []string{"Variable_name", "Value"})
	}

	if strings.Contains(query, "DATABASES") {
		return f.sendSimpleResult([][]interface{}{
			{f.database},
		}, []string{"Database"})
	}

	return f.sendOK()
}

// handleStmtPrepare handles prepared statement preparation.
func (f *Frontend) handleStmtPrepare(data []byte) error {
	query := string(data)

	// Create prepared statement
	stmt := &PreparedStatement{
		ID:    f.stmtIDGen.Add(1),
		Query: query,
	}
	f.stmts[stmt.ID] = stmt

	// Send prepare OK response
	return f.sendStmtPrepareOK(stmt)
}

// handleStmtExecute handles prepared statement execution.
func (f *Frontend) handleStmtExecute(data []byte) error {
	if len(data) < 4 {
		return f.sendError(1243, "HY000", "Incorrect arguments to execute")
	}

	stmtID := binary.LittleEndian.Uint32(data[0:4])
	stmt, exists := f.stmts[stmtID]
	if !exists {
		return f.sendError(1243, "HY000", fmt.Sprintf("Unknown prepared statement handler (%d) given to execute", stmtID))
	}

	_ = stmt
	return f.sendOK()
}

// handleStmtClose closes a prepared statement.
func (f *Frontend) handleStmtClose(data []byte) error {
	if len(data) >= 4 {
		stmtID := binary.LittleEndian.Uint32(data[0:4])
		delete(f.stmts, stmtID)
	}
	return nil // No response for COM_STMT_CLOSE
}

// handleFieldList handles field list request.
func (f *Frontend) handleFieldList(data []byte) error {
	// Send EOF
	return f.sendEOF(0)
}

// handleStatistics handles statistics request.
func (f *Frontend) handleStatistics() error {
	// Send statistics string
	stats := "Uptime: 0  Threads: 1  Questions: 1  Slow queries: 0  Opens: 0  Flush tables: 0  Open tables: 0  Queries per second avg: 0.000"
	return f.writePacket(0, []byte(stats))
}

// handleProcessInfo handles process info request.
func (f *Frontend) handleProcessInfo() error {
	// Return process list
	return f.sendSimpleResult([][]interface{}{
		{1, "root", "localhost", f.database, "Query", 0, "active", "SHOW PROCESSLIST"},
	}, []string{"Id", "User", "Host", "db", "Command", "Time", "State", "Info"})
}

// sendOK sends an OK packet.
func (f *Frontend) sendOK() error {
	data := make([]byte, 0, 32)

	// Header
	data = append(data, OK)

	// Affected rows (length encoded)
	data = append(data, 0) // 0 affected rows

	// Last insert ID (length encoded)
	data = append(data, 0) // 0

	// Server status
	data = append(data, byte(f.status), byte(f.status>>8))

	// Warnings
	data = append(data, 0, 0)

	return f.writePacket(0, data)
}

// sendError sends an error packet.
func (f *Frontend) sendError(code uint16, sqlState string, message string) error {
	data := make([]byte, 0, 128)

	// Header
	data = append(data, ERR)

	// Error code (2 bytes, little endian)
	data = append(data, byte(code), byte(code>>8))

	// SQL state marker
	data = append(data, '#')

	// SQL state (5 bytes)
	data = append(data, sqlState...)

	// Error message
	data = append(data, message...)

	return f.writePacket(0, data)
}

// sendEOF sends an EOF packet.
func (f *Frontend) sendEOF(warnings uint16) error {
	data := make([]byte, 5)
	data[0] = EOF
	data[1] = byte(warnings)
	data[2] = byte(warnings >> 8)
	data[3] = byte(f.status)
	data[4] = byte(f.status >> 8)
	return f.writePacket(0, data)
}

// sendStmtPrepareOK sends a prepare OK response.
func (f *Frontend) sendStmtPrepareOK(stmt *PreparedStatement) error {
	data := make([]byte, 0, 32)

	// Status (1 byte) - 0 = OK
	data = append(data, 0)

	// Statement ID (4 bytes)
	data = append(data, byte(stmt.ID), byte(stmt.ID>>8), byte(stmt.ID>>16), byte(stmt.ID>>24))

	// Number of columns (2 bytes)
	data = append(data, 0, 0)

	// Number of params (2 bytes)
	data = append(data, 0, 0)

	// Reserved (1 byte)
	data = append(data, 0)

	// Warning count (2 bytes)
	data = append(data, 0, 0)

	return f.writePacket(0, data)
}

// sendSimpleResult sends a simple result set.
func (f *Frontend) sendSimpleResult(rows [][]interface{}, columns []string) error {
	// Send column count
	if err := f.writePacket(0, []byte{byte(len(columns))}); err != nil {
		return err
	}

	// Send column definitions
	for i, col := range columns {
		colDef := f.buildColumnDef(col, i)
		if err := f.writePacket(0, colDef); err != nil {
			return err
		}
	}

	// Send EOF
	if err := f.sendEOF(0); err != nil {
		return err
	}

	// Send rows
	for _, row := range rows {
		rowData := f.buildRowData(row)
		if err := f.writePacket(0, rowData); err != nil {
			return err
		}
	}

	// Send EOF
	return f.sendEOF(0)
}

// buildColumnDef builds a column definition packet.
func (f *Frontend) buildColumnDef(name string, index int) []byte {
	data := make([]byte, 0, 64)

	// Catalog (length encoded string)
	data = append(data, 0) // def

	// Schema (length encoded string)
	data = append(data, 0)

	// Table (length encoded string)
	data = append(data, 0)

	// Org table (length encoded string)
	data = append(data, 0)

	// Name (length encoded string)
	data = appendLengthEncodedString(data, name)

	// Org name (length encoded string)
	data = appendLengthEncodedString(data, name)

	// Next length (0x0c for protocol 4.1)
	data = append(data, 0x0c)

	// Character set (2 bytes)
	data = append(data, 0x21, 0x00) // utf8mb4

	// Column length (4 bytes)
	data = append(data, 0xff, 0xff, 0xff, 0xff)

	// Type (1 byte) - 253 = VAR_STRING
	data = append(data, 253)

	// Flags (2 bytes)
	data = append(data, 0x00, 0x00)

	// Decimals (1 byte)
	data = append(data, 0x00)

	return data
}

// buildRowData builds row data packet.
func (f *Frontend) buildRowData(row []interface{}) []byte {
	data := make([]byte, 0, 256)

	for _, val := range row {
		switch v := val.(type) {
		case string:
			data = appendLengthEncodedString(data, v)
		case int:
			data = appendLengthEncodedString(data, fmt.Sprintf("%d", v))
		case nil:
			data = append(data, 0xfb) // NULL
		default:
			data = appendLengthEncodedString(data, fmt.Sprintf("%v", v))
		}
	}

	return data
}

// appendLengthEncodedString appends a length-encoded string.
func appendLengthEncodedString(data []byte, s string) []byte {
	length := len(s)
	if length < 251 {
		data = append(data, byte(length))
	} else if length < 65536 {
		data = append(data, 0xfc, byte(length), byte(length>>8))
	} else {
		data = append(data, 0xfe, byte(length), byte(length>>8), byte(length>>16), byte(length>>24), byte(length>>32), byte(length>>40), byte(length>>48), byte(length>>56))
	}
	data = append(data, s...)
	return data
}

// readLengthEncodedInt reads a length-encoded integer.
func readLengthEncodedInt(data []byte) (uint64, int) {
	if len(data) == 0 {
		return 0, 0
	}

	first := data[0]
	switch {
	case first < 0xfb:
		return uint64(first), 1
	case first == 0xfc:
		return uint64(data[1]) | uint64(data[2])<<8, 3
	case first == 0xfd:
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16, 4
	case first == 0xfe:
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16 | uint64(data[4])<<24 |
			uint64(data[5])<<32 | uint64(data[6])<<40 | uint64(data[7])<<48 | uint64(data[8])<<56, 9
	default:
		return 0, 0
	}
}

// writePacket writes a MySQL protocol packet.
func (f *Frontend) writePacket(seq byte, data []byte) error {
	// Write packet header (3 bytes length + 1 byte sequence)
	length := len(data)
	header := make([]byte, 4)
	header[0] = byte(length)
	header[1] = byte(length >> 8)
	header[2] = byte(length >> 16)
	header[3] = seq

	if _, err := f.writer.Write(header); err != nil {
		return err
	}

	if _, err := f.writer.Write(data); err != nil {
		return err
	}

	return f.writer.Flush()
}

// readPacket reads a MySQL protocol packet.
func (f *Frontend) readPacket() (byte, []byte, error) {
	// Read header
	header := make([]byte, 4)
	if _, err := io.ReadFull(f.reader, header); err != nil {
		return 0, nil, err
	}

	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	seq := header[3]

	// Bound payload size (16MB max)
	const maxMySQLPayload = 16 << 20
	if length > maxMySQLPayload {
		return 0, nil, fmt.Errorf("mysql packet too large: %d bytes", length)
	}

	// Read payload
	data := make([]byte, length)
	if _, err := io.ReadFull(f.reader, data); err != nil {
		return 0, nil, err
	}

	return seq, data, nil
}

// cleanup cleans up resources.
func (f *Frontend) cleanup() {
	f.closed.Store(true)
	f.conn.Close()
	f.log.Info("MySQL frontend connection closed", "thread_id", f.threadID)
}

// Helper function
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
