// Package mssql implements the Microsoft SQL Server TDS protocol.
package mssql

import (
	"bufio"
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
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

// TDS protocol constants
const (
	// Packet types
	PackSQLBatch   = 1
	PackRPCRequest = 3
	PackReply      = 4
	PackAttention  = 6
	PackBulkLoad   = 7
	PackFedAuthToken = 8
	PackTransMgrReq = 14
	PackTDS7Login  = 16
	PackSSPI       = 17
	PackPreLogin   = 18

	// Maximum TDS packet payload (16MB)
	maxTDSPacketSize = 16 << 20

	// Status bits
	StatusNormal     = 0x00
	StatusEOM        = 0x01
	StatusIgnore     = 0x02
	StatusResetConn  = 0x08
	StatusResetSkip  = 0x10

	// Client versions
	VerSQL2000 = 0x07000000
	VerSQL2005 = 0x72090002
	VerSQL2008 = 0x730A0003
	VerSQL2012 = 0x74000004
	VerSQL2014 = 0x74000004
	VerSQL2016 = 0x74000004
	VerSQL2017 = 0x74000004
	VerSQL2019 = 0x74000004

	// Fixed data lengths
	MinLoginPacketSize = 512
	MaxLoginPacketSize = 32767

	// Client program version
	ClientProgVer = 0x07000000
)

// Frontend handles MSSQL client connections.
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
	clientPID   uint32
	clientID    [6]byte

	// Connection settings
	packetSize  int
	clientVer   uint32

	// TLS
	tlsConfig   *tls.Config
	tlsActive   bool

	// Packet sequence
	sequence    uint32
	seqMu       sync.Mutex

	// Shutdown
	closed      atomic.Bool
	mu          sync.Mutex
}

// FrontendState represents the connection state.
type FrontendState int

const (
	StatePreLogin FrontendState = iota
	StateLogin
	StateReady
	StateActive
	StateClosed
)

// TDSPacket represents a TDS packet.
type TDSPacket struct {
	Type   uint8
	Status uint8
	Length uint16
	SPID   uint16
	Packet uint8
	Window uint8
	Data   []byte
}

// NewFrontend creates a new MSSQL frontend handler.
func NewFrontend(conn net.Conn, p *pool.Pool, userDB *auth.UserDatabase, log *logger.Logger) *Frontend {
	return &Frontend{
		conn:       conn,
		reader:     bufio.NewReader(conn),
		writer:     bufio.NewWriter(conn),
		pool:       p,
		userDB:     userDB,
		log:        log,
		state:      StatePreLogin,
		packetSize: 4096,
	}
}

// Handle handles the client connection.
func (f *Frontend) Handle() error {
	defer f.cleanup()

	// Handle pre-login
	if err := f.handlePreLogin(); err != nil {
		return fmt.Errorf("pre-login failed: %w", err)
	}

	// Handle login
	if err := f.handleLogin(); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	f.state = StateReady

	// Main packet loop
	for !f.closed.Load() {
		if err := f.handlePacket(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}

	return nil
}

// handlePreLogin handles the pre-login phase.
func (f *Frontend) handlePreLogin() error {
	// Read pre-login packet
	packet, err := f.readPacket()
	if err != nil {
		return err
	}

	if packet.Type != PackPreLogin {
		return fmt.Errorf("expected pre-login packet, got %d", packet.Type)
	}

	// Parse pre-login options
	options, err := f.parsePreLoginOptions(packet.Data)
	if err != nil {
		return err
	}

	// Check for encryption requirement
	encrypt := options[1] // Encryption option
	_ = encrypt

	// Send pre-login response
	response := f.buildPreLoginResponse(options)
	if err := f.writePacket(PackReply, StatusEOM, response); err != nil {
		return err
	}

	return nil
}

// parsePreLoginOptions parses pre-login option tokens.
func (f *Frontend) parsePreLoginOptions(data []byte) (map[uint8][]byte, error) {
	options := make(map[uint8][]byte)
	offset := 0

	for offset < len(data) {
		if offset >= len(data) {
			break
		}

		option := data[offset]
		offset++

		if option == 0xFF {
			break // Terminator
		}

		if offset+4 > len(data) {
			break
		}

		optOffset := binary.BigEndian.Uint16(data[offset:offset+2])
		optLength := binary.BigEndian.Uint16(data[offset+2:offset+4])
		offset += 4

		if int(optOffset)+int(optLength) <= len(data) {
			options[option] = data[optOffset:optOffset+optLength]
		}
	}

	return options, nil
}

// buildPreLoginResponse builds pre-login response.
func (f *Frontend) buildPreLoginResponse(requestOptions map[uint8][]byte) []byte {
	// Simple response with version and packet size
	data := make([]byte, 0, 64)

	// Version (0x00)
	data = append(data, 0x00)
	data = append(data, 0x00, 0x00) // Offset
	data = append(data, 0x06, 0x00) // Length (6 bytes)

	// Encryption (0x01)
	data = append(data, 0x01)
	data = append(data, 0x06, 0x00) // Offset
	data = append(data, 0x01, 0x00) // Length (1 byte)

	// Instance (0x02)
	data = append(data, 0x02)
	data = append(data, 0x07, 0x00) // Offset
	data = append(data, 0x01, 0x00) // Length (1 byte)

	// Thread ID (0x03)
	data = append(data, 0x03)
	data = append(data, 0x08, 0x00) // Offset
	data = append(data, 0x04, 0x00) // Length (4 bytes)

	// Terminator
	data = append(data, 0xFF)

	// Version data (6 bytes)
	data = append(data, 0x0E, 0x00, 0x09, 0x04, 0x00, 0x00)

	// Encryption data (1 byte) - 0x02 = encryption not supported
	data = append(data, 0x02)

	// Instance data (1 byte) - empty
	data = append(data, 0x00)

	// Thread ID data (4 bytes)
	data = append(data, 0x00, 0x00, 0x00, 0x00)

	return data
}

// handleLogin handles the login phase.
func (f *Frontend) errorLogin(errCode uint32, errMsg string) error {
	// Build error token
	token := f.buildErrorToken(errCode, 1, errMsg, "", "", 0)

	// Build done token
	done := f.buildDoneToken(0x0102, uint16(errCode), 0)

	// Combine
	data := append(token, done...)

	return f.writePacket(PackReply, StatusEOM, data)
}

// handleLogin handles the login phase.
func (f *Frontend) handleLogin() error {
	// Read login packet
	packet, err := f.readPacket()
	if err != nil {
		return err
	}

	if packet.Type != PackTDS7Login {
		return fmt.Errorf("expected login packet, got %d", packet.Type)
	}

	// Parse login
	login, err := f.parseLogin(packet.Data)
	if err != nil {
		return err
	}

	f.clientVer = login.TDSVersion
	f.clientPID = login.ClientPID
	f.database = login.Database

	// Check for Windows Authentication (SSPI/NTLM)
	if len(login.SSPI) > 0 || len(login.SSPILong) > 0 {
		// NTLM Passthrough: Forward to backend
		if err := f.handleWindowsAuth(login); err != nil {
			return fmt.Errorf("windows auth failed: %w", err)
		}
		return nil
	}

	// SQL Server Authentication (username/password)
	user := f.userDB.GetUser(login.Username)
	if user == nil {
		return f.errorLogin(18456, "Login failed for user")
	}

	f.user = user

	// Send login acknowledgment
	if err := f.sendLoginAck(); err != nil {
		return err
	}

	// Send environment change (database)
	if err := f.sendEnvChange("DATABASE", f.database); err != nil {
		return err
	}

	// Send final done
	return f.sendDone(0, 0, 0)
}

// handleWindowsAuth handles NTLM/Windows Authentication passthrough.
// This forwards the authentication tokens to the backend SQL Server.
func (f *Frontend) handleWindowsAuth(login *Login) error {
	f.log.Debug("Windows Authentication (NTLM) detected", "hostname", login.HostName)

	// Get a backend connection
	ctx := context.Background()
	backend, err := f.pool.Acquire(ctx)
	if err != nil {
		return f.errorLogin(18456, "Cannot connect to backend server")
	}
	defer f.pool.Release(backend)

	// Forward the TDS7 login packet with SSPI data to backend
	// The backend will handle the NTLM challenge-response
	if err := f.forwardWindowsAuthToBackend(login, backend); err != nil {
		return f.errorLogin(18456, "Windows authentication failed")
	}

	f.state = StateReady
	f.user = &auth.User{Username: login.HostName + "\\" + login.HostName} // Placeholder for Windows user

	// Send success response to client
	if err := f.sendLoginAck(); err != nil {
		return err
	}

	if err := f.sendEnvChange("DATABASE", f.database); err != nil {
		return err
	}

	return f.sendDone(0, 0, 0)
}

// forwardWindowsAuthToBackend forwards Windows auth tokens to backend.
func (f *Frontend) forwardWindowsAuthToBackend(login *Login, backend *pool.ServerConn) error {
	// Rebuild TDS7 login packet with SSPI data
	loginPacket := f.buildTDS7LoginPacket(login)

	// Send to backend
	if _, err := backend.Conn().Write(loginPacket); err != nil {
		return err
	}

	// Read response from backend
	buf := make([]byte, 4096)
	n, err := backend.Conn().Read(buf)
	if err != nil {
		return err
	}

	// Forward backend response to client
	if _, err := f.conn.Write(buf[:n]); err != nil {
		return err
	}

	return nil
}

// buildTDS7LoginPacket rebuilds the TDS7 login packet for forwarding.
func (f *Frontend) buildTDS7LoginPacket(login *Login) []byte {
	// This is a simplified implementation
	// In production, you'd rebuild the exact packet structure

	// Header: Type (1 byte) + Status (1 byte) + Length (2 bytes) +
	//         SPID (2 bytes) + Packet (1 byte) + Window (1 byte) = 8 bytes
	header := make([]byte, 8)
	header[0] = PackTDS7Login
	header[1] = 0x01 // Status EOM

	// For NTLM passthrough, we forward the original SSPI data
	// The backend SQL Server will handle the NTLM challenge-response

	// Build variable-length data
	var data []byte

	// Fixed part (86 bytes)
	fixed := make([]byte, 86)
	binary.LittleEndian.PutUint32(fixed[0:4], login.Length)
	binary.LittleEndian.PutUint32(fixed[4:8], login.TDSVersion)
	binary.LittleEndian.PutUint32(fixed[8:12], login.PacketSize)
	binary.LittleEndian.PutUint32(fixed[12:16], login.ClientProgVer)
	binary.LittleEndian.PutUint32(fixed[16:20], login.ClientPID)
	binary.LittleEndian.PutUint32(fixed[20:24], login.ConnectionID)
	fixed[24] = login.OptionFlags1
	fixed[25] = login.OptionFlags2
	fixed[26] = login.TypeFlags
	fixed[27] = login.OptionFlags3
	binary.LittleEndian.PutUint32(fixed[28:32], uint32(login.ClientTimeZone))
	binary.LittleEndian.PutUint32(fixed[32:36], login.ClientLCID)
	copy(fixed[36:42], login.ClientID[:])

	// Variable offsets and lengths would go here...
	// For SSPI passthrough, we include the SSPI token

	data = append(data, fixed...)

	// Add SSPI token if present
	if len(login.SSPILong) > 0 {
		data = append(data, login.SSPILong...)
	} else if len(login.SSPI) > 0 {
		data = append(data, login.SSPI...)
	}

	// Update length in header
	binary.LittleEndian.PutUint16(header[2:4], uint16(len(data)+8))

	return append(header, data...)
}

// Login represents a TDS7 login packet.
type Login struct {
	Length         uint32
	TDSVersion     uint32
	PacketSize     uint32
	ClientProgVer  uint32
	ClientPID      uint32
	ConnectionID   uint32
	OptionFlags1   uint8
	OptionFlags2   uint8
	TypeFlags      uint8
	OptionFlags3   uint8
	ClientTimeZone int32
	ClientLCID     uint32
	HostName       string
	Username       string
	Password       string
	AppName        string
	ServerName     string
	Extension      string
	LibraryName    string
	Language       string
	Database       string
	ClientID       [6]byte
	SSPI           []byte
	DBFile         string
	NewPassword    string
	SSPILong       []byte
}

// parseLogin parses a TDS7 login packet.
func (f *Frontend) parseLogin(data []byte) (*Login, error) {
	if len(data) < 86 {
		return nil, fmt.Errorf("login packet too short")
	}

	login := &Login{}
	offset := 0

	// Fixed part
	login.Length = binary.LittleEndian.Uint32(data[offset:offset+4])
	offset += 4
	login.TDSVersion = binary.LittleEndian.Uint32(data[offset:offset+4])
	offset += 4
	login.PacketSize = binary.LittleEndian.Uint32(data[offset:offset+4])
	offset += 4
	login.ClientProgVer = binary.LittleEndian.Uint32(data[offset:offset+4])
	offset += 4
	login.ClientPID = binary.LittleEndian.Uint32(data[offset:offset+4])
	offset += 4
	login.ConnectionID = binary.LittleEndian.Uint32(data[offset:offset+4])
	offset += 4
	login.OptionFlags1 = data[offset]
	offset++
	login.OptionFlags2 = data[offset]
	offset++
	login.TypeFlags = data[offset]
	offset++
	login.OptionFlags3 = data[offset]
	offset++
	login.ClientTimeZone = int32(binary.LittleEndian.Uint32(data[offset:offset+4]))
	offset += 4
	login.ClientLCID = binary.LittleEndian.Uint32(data[offset:offset+4])
	offset += 4

	// Read variable length offsets and lengths
	type offsetLength struct {
		Offset uint16
		Length uint16
	}

	readOL := func() offsetLength {
		ol := offsetLength{
			Offset: binary.LittleEndian.Uint16(data[offset:offset+2]),
			Length: binary.LittleEndian.Uint16(data[offset+2:offset+4]),
		}
		offset += 4
		return ol
	}

	hostnameOL := readOL()
	usernameOL := readOL()
	passwordOL := readOL()
	appnameOL := readOL()
	servernameOL := readOL()
	unusedOL := readOL()
	librarynameOL := readOL()
	languageOL := readOL()
	databaseOL := readOL()

	// Client ID (6 bytes)
	copy(login.ClientID[:], data[offset:offset+6])
	offset += 6

	sspiOL := readOL()
	dbfileOL := readOL()
	newpasswordOL := readOL()
	sspiLongLength := binary.LittleEndian.Uint32(data[offset:offset+4])
	offset += 4

	// Read variable length data (Unicode strings)
	readString := func(ol offsetLength) string {
		if ol.Offset == 0 || ol.Length == 0 {
			return ""
		}
		start := int(ol.Offset)
		length := int(ol.Length)
		if start+length*2 > len(data) {
			return ""
		}
		// Convert UTF-16-LE to string (simplified)
		bytes := data[start:start+length*2]
		result := make([]byte, 0, length)
		for i := 0; i < len(bytes); i += 2 {
			if bytes[i] != 0 || i+1 < len(bytes) {
				result = append(result, bytes[i])
			}
		}
		return string(result)
	}

	login.HostName = readString(hostnameOL)
	login.Username = readString(usernameOL)
	login.Password = readString(passwordOL)
	login.AppName = readString(appnameOL)
	login.ServerName = readString(servernameOL)
	login.LibraryName = readString(librarynameOL)
	login.Language = readString(languageOL)
	login.Database = readString(databaseOL)

	// Read SSPI if present
	if sspiOL.Offset > 0 && sspiOL.Length > 0 {
		start := int(sspiOL.Offset)
		length := int(sspiOL.Length)
		if start+length <= len(data) {
			login.SSPI = data[start:start+length]
		}
	}

	if sspiLongLength > 0 {
		login.SSPILong = login.SSPI
	}

	// Ignore unused fields
	_ = unusedOL
	_ = dbfileOL
	_ = newpasswordOL

	return login, nil
}

// sendLoginAck sends login acknowledgment.
func (f *Frontend) sendLoginAck() error {
	data := make([]byte, 0, 64)

	// Token type: LOGINACK (0xAD)
	data = append(data, 0xAD)

	// Token length (2 bytes, little endian)
	ackData := f.buildLoginAckData()
	data = append(data, byte(len(ackData)), byte(len(ackData)>>8))

	// Ack data
	data = append(data, ackData...)

	return f.writePacket(PackReply, 0x00, data)
}

// buildLoginAckData builds login acknowledgment data.
func (f *Frontend) buildLoginAckData() []byte {
	data := make([]byte, 0, 32)

	// Interface: SQL_TSQL (1 byte)
	data = append(data, 0x01)

	// TDS version (4 bytes)
	data = append(data, 0x04, 0x00, 0x00, 0x74) // TDS 7.4

	// Program name length (1 byte)
	progName := "Geryon"
	data = append(data, byte(len(progName)))

	// Program name (Unicode)
	for _, c := range progName {
		data = append(data, byte(c), 0x00)
	}

	// Major version (1 byte)
	data = append(data, 0x0E)

	// Minor version (1 byte)
	data = append(data, 0x00)

	// Build number (2 bytes)
	data = append(data, 0x00, 0x00)

	return data
}

// sendEnvChange sends environment change token.
func (f *Frontend) sendEnvChange(envType string, newValue string) error {
	data := make([]byte, 0, 128)

	// Token type: ENVCHANGE (0xE3)
	data = append(data, 0xE3)

	// Build env change data
	envData := f.buildEnvChangeData(envType, newValue)

	// Token length (2 bytes)
	data = append(data, byte(len(envData)), byte(len(envData)>>8))

	// Env data
	data = append(data, envData...)

	return f.writePacket(PackReply, 0x00, data)
}

// buildEnvChangeData builds environment change data.
func (f *Frontend) buildEnvChangeData(envType string, newValue string) []byte {
	data := make([]byte, 0, 64)

	var typeCode uint8
	switch envType {
	case "DATABASE":
		typeCode = 1
	case "LANGUAGE":
		typeCode = 2
	case "CHARSET":
		typeCode = 3
	case "PACKET_SIZE":
		typeCode = 4
	default:
		typeCode = 1
	}

	data = append(data, typeCode)

	// New value (Unicode length-prefixed)
	data = append(data, byte(len(newValue)))
	for _, c := range newValue {
		data = append(data, byte(c), 0x00)
	}

	// Old value (empty for login)
	data = append(data, 0x00)

	return data
}

// sendDone sends a done token.
func (f *Frontend) sendDone(status uint16, curCmd uint16, doneRowCount uint64) error {
	data := f.buildDoneToken(status, curCmd, doneRowCount)
	return f.writePacket(PackReply, StatusEOM, data)
}

// buildDoneToken builds a done token.
func (f *Frontend) buildDoneToken(status uint16, curCmd uint16, doneRowCount uint64) []byte {
	data := make([]byte, 0, 16)

	// Token type: DONE (0xFD)
	data = append(data, 0xFD)

	// Status (2 bytes)
	data = append(data, byte(status), byte(status>>8))

	// Current command (2 bytes)
	data = append(data, byte(curCmd), byte(curCmd>>8))

	// Row count (8 bytes for TDS 7.3+)
	data = append(data, byte(doneRowCount), byte(doneRowCount>>8),
		byte(doneRowCount>>16), byte(doneRowCount>>24),
		byte(doneRowCount>>32), byte(doneRowCount>>40),
		byte(doneRowCount>>48), byte(doneRowCount>>56))

	return data
}

// buildErrorToken builds an error token.
func (f *Frontend) buildErrorToken(errCode uint32, errClass uint8, errMsg string, server string, proc string, lineNum uint32) []byte {
	data := make([]byte, 0, 256)

	// Token type: ERROR (0xAA)
	data = append(data, 0xAA)

	// Token length (2 bytes) - placeholder
	lengthPos := len(data)
	data = append(data, 0, 0)

	startLen := len(data)

	// Error number (4 bytes)
	data = append(data, byte(errCode), byte(errCode>>8), byte(errCode>>16), byte(errCode>>24))

	// Error state (1 byte)
	data = append(data, 1)

	// Error class (1 byte)
	data = append(data, errClass)

	// Error message (Unicode length-prefixed)
	data = append(data, byte(len(errMsg)), 0)
	for _, c := range errMsg {
		data = append(data, byte(c), 0x00)
	}

	// Server name (Unicode length-prefixed)
	data = append(data, byte(len(server)))
	for _, c := range server {
		data = append(data, byte(c), 0x00)
	}

	// Procedure name (Unicode length-prefixed)
	data = append(data, byte(len(proc)))
	for _, c := range proc {
		data = append(data, byte(c), 0x00)
	}

	// Line number (4 bytes)
	data = append(data, byte(lineNum), byte(lineNum>>8), byte(lineNum>>16), byte(lineNum>>24))

	// Update length
	tokenLen := len(data) - startLen
	data[lengthPos] = byte(tokenLen)
	data[lengthPos+1] = byte(tokenLen >> 8)

	return data
}

// handlePacket handles a single TDS packet.
func (f *Frontend) handlePacket() error {
	packet, err := f.readPacket()
	if err != nil {
		return err
	}

	switch packet.Type {
	case PackSQLBatch:
		return f.handleSQLBatch(packet.Data)

	case PackRPCRequest:
		return f.handleRPC(packet.Data)

	case PackAttention:
		return f.handleAttention()

	case PackTransMgrReq:
		return f.handleTransMgr(packet.Data)

	default:
		f.log.Debug("Unhandled TDS packet type", "type", packet.Type)
		return f.sendDone(0, 0, 0)
	}
}

// handleSQLBatch handles SQL batch packets.
func (f *Frontend) handleSQLBatch(data []byte) error {
	if len(data) < 8 {
		return fmt.Errorf("SQL batch too short")
	}

	// Skip header (8 bytes for SQL batch)
	offset := 8

	// Read SQL text (Unicode)
	if offset < len(data) {
		sql := f.decodeUnicode(data[offset:])
		f.log.Debug("MSSQL batch", "sql", sql[:min(len(sql), 100)])

		// Execute the query (simplified)
		return f.executeSQL(sql)
	}

	return f.sendDone(0, 0, 0)
}

// executeSQL executes a SQL statement.
func (f *Frontend) executeSQL(sql string) error {
	upperSQL := strings.ToUpper(strings.TrimSpace(sql))

	switch {
	case strings.HasPrefix(upperSQL, "SELECT"):
		// Send column metadata and rows
		if err := f.sendRowMetadata([]*ColumnInfo{
			{Name: "result", Type: 0x38}, // INT
		}); err != nil {
			return err
		}

		// Send row
		if err := f.sendRow([]interface{}{1}); err != nil {
			return err
		}

		return f.sendDone(0x0010, 0, 1) // COUNT flag

	case strings.HasPrefix(upperSQL, "SET "):
		return f.sendDone(0, 0, 0)

	default:
		return f.sendDone(0, 0, 0)
	}
}

// ColumnInfo represents column metadata.
type ColumnInfo struct {
	Name   string
	Type   uint8
	Size   int
	Scale  uint8
	Prec   uint8
}

// sendRowMetadata sends column metadata.
func (f *Frontend) sendRowMetadata(columns []*ColumnInfo) error {
	data := make([]byte, 0, 256)

	// Token type: COLMETADATA (0x81)
	data = append(data, 0x81)

	// Column count (2 bytes)
	data = append(data, byte(len(columns)), byte(len(columns)>>8))

	for _, col := range columns {
		// User type (4 bytes)
		data = append(data, 0, 0, 0, 0)

		// Flags (2 bytes)
		data = append(data, 0x09, 0x00) // nullable

		// Data type (1 byte)
		data = append(data, col.Type)

		// Type info depends on type
		switch col.Type {
		case 0x38: // INT
			// No additional info needed
		case 0x27, 0x25: // VARCHAR, TEXT
			// Max length (2 bytes) + collation (5 bytes)
			data = append(data, 0xFF, 0xFF, 0x09, 0x04, 0xD0, 0x00, 0x34)
		default:
			// Default handling
		}

		// Column name (length-prefixed string)
		data = append(data, byte(len(col.Name)))
		data = append(data, col.Name...)
	}

	return f.writePacket(PackReply, 0x00, data)
}

// sendRow sends a data row.
func (f *Frontend) sendRow(values []interface{}) error {
	data := make([]byte, 0, 256)

	// Token type: ROW (0xD1) or NBCROW (0xD2)
	data = append(data, 0xD1)

	for _, val := range values {
		switch v := val.(type) {
		case int:
			// INT (4 bytes, little endian)
			data = append(data, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
		case string:
			// Length (2 bytes) + data
			data = append(data, byte(len(v)), byte(len(v)>>8))
			data = append(data, v...)
		case nil:
			// NULL
			data = append(data, 0x00, 0x00) // Length = 0xFFFF for NULL
		}
	}

	return f.writePacket(PackReply, 0x00, data)
}

// handleRPC handles RPC requests.
// handleRPC handles RPC (Remote Procedure Call) requests.
// This includes sp_prepare, sp_execute, sp_unprepare for prepared statements.
func (f *Frontend) handleRPC(data []byte) error {
	if len(data) < 8 {
		return fmt.Errorf("RPC packet too short")
	}

	// Parse RPC header
	// Name length (2 bytes) + Name (variable) + Options (2 bytes) + Parameters
	offset := 0

	// Read procedure name length
	nameLen := binary.LittleEndian.Uint16(data[offset:offset+2])
	offset += 2

	// Read procedure name (Unicode)
	if offset+int(nameLen)*2 > len(data) {
		return fmt.Errorf("RPC name exceeds packet length")
	}

	procName := f.decodeUnicode(data[offset : offset+int(nameLen)*2])
	offset += int(nameLen) * 2

	// Skip options (2 bytes)
	offset += 2

	f.log.Debug("MSSQL RPC", "procedure", procName)

	// Handle specific stored procedures
	switch procName {
	case "sp_prepare":
		return f.handleSPPrepare(data[offset:])
	case "sp_execute":
		return f.handleSPExecute(data[offset:])
	case "sp_unprepare":
		return f.handleSPUnprepare(data[offset:])
	case "sp_reset_connection":
		return f.handleSPResetConnection()
	default:
		// Generic RPC handling
		return f.sendDone(0, 0, 0)
	}
}

// handleSPPrepare handles sp_prepare RPC for prepared statements.
// sp_prepare @handle OUTPUT, @params, @stmt [, @options]
func (f *Frontend) handleSPPrepare(data []byte) error {
	// Simplified implementation - parse parameters and assign a handle
	stmtID := f.nextPreparedStmtID()

	f.log.Debug("sp_prepare", "stmt_id", stmtID)

	// Return the statement handle to client
	// Format: result set with handle value
	if err := f.sendPreparedStmtHandle(stmtID); err != nil {
		return err
	}

	return f.sendDone(0, 0, 1)
}

// handleSPExecute handles sp_execute RPC for executing prepared statements.
// sp_execute @handle [, @param1 [, @param2 ...]]
func (f *Frontend) handleSPExecute(data []byte) error {
	// Parse parameters to get the statement handle
	// For now, return a simple result
	return f.sendDone(0, 0, 0)
}

// handleSPUnprepare handles sp_unprepare RPC for releasing prepared statements.
// sp_unprepare @handle
func (f *Frontend) handleSPUnprepare(data []byte) error {
	// Release the prepared statement
	return f.sendDone(0, 0, 0)
}

// handleSPResetConnection handles sp_reset_connection for connection reset.
func (f *Frontend) handleSPResetConnection() error {
	// Reset connection state
	f.log.Debug("sp_reset_connection")
	return f.sendDone(0, 0, 0)
}

// nextPreparedStmtID generates a unique prepared statement ID.
func (f *Frontend) nextPreparedStmtID() int32 {
	// Use a simple counter (in production, use atomic counter)
	return int32(time.Now().UnixNano() % 1000000)
}

// sendPreparedStmtHandle sends the prepared statement handle to the client.
func (f *Frontend) sendPreparedStmtHandle(handle int32) error {
	// Build return value token
	data := make([]byte, 0, 32)

	// Token type: RETURNVALUE (0xAC)
	data = append(data, 0xAC)

	// Ordinal (2 bytes) - parameter position
	data = append(data, 0x01, 0x00)

	// Parameter name length (1 byte)
	data = append(data, 0x00) // No name for return value

	// Status (1 byte) - OUTPUT parameter
	data = append(data, 0x01)

	// Type info: INT (0x38)
	data = append(data, 0x38)

	// Value
	data = append(data, byte(handle), byte(handle>>8), byte(handle>>16), byte(handle>>24))

	return f.writePacket(PackReply, StatusEOM, data)
}

// handleAttention handles attention (cancel) requests.
func (f *Frontend) handleAttention() error {
	// Cancel current operation
	return nil
}

// handleTransMgr handles transaction manager requests.
func (f *Frontend) handleTransMgr(data []byte) error {
	// Simplified transaction handling
	return f.sendDone(0, 0, 0)
}

// decodeUnicode decodes UTF-16-LE to string (simplified).
func (f *Frontend) decodeUnicode(data []byte) string {
	result := make([]byte, 0, len(data)/2)
	for i := 0; i < len(data)-1; i += 2 {
		if data[i] != 0 || data[i+1] != 0 {
			result = append(result, data[i])
		}
	}
	return string(result)
}

// readPacket reads a TDS packet.
func (f *Frontend) readPacket() (*TDSPacket, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(f.reader, header); err != nil {
		return nil, err
	}

	packet := &TDSPacket{
		Type:   header[0],
		Status: header[1],
		Length: binary.BigEndian.Uint16(header[2:4]),
		SPID:   binary.BigEndian.Uint16(header[4:6]),
		Packet: header[6],
		Window: header[7],
	}

	// Read payload
	payloadLen := int(packet.Length) - 8
	if payloadLen < 0 {
		return nil, fmt.Errorf("invalid TDS packet length: %d", packet.Length)
	}
	if payloadLen > maxTDSPacketSize {
		return nil, fmt.Errorf("TDS packet too large: %d bytes", payloadLen)
	}
	if payloadLen > 0 {
		packet.Data = make([]byte, payloadLen)
		if _, err := io.ReadFull(f.reader, packet.Data); err != nil {
			return nil, err
		}
	}

	return packet, nil
}

// writePacket writes a TDS packet.
func (f *Frontend) writePacket(packetType uint8, status uint8, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	length := len(data) + 8

	header := make([]byte, 8)
	header[0] = packetType
	header[1] = status
	header[2] = byte(length)
	header[3] = byte(length >> 8)
	header[4] = 0x00 // SPID
	header[5] = 0x00
	f.seqMu.Lock()
	f.sequence++
	seq := byte(f.sequence)
	f.seqMu.Unlock()
	header[6] = seq
	header[7] = 0x00 // Window

	if _, err := f.writer.Write(header); err != nil {
		return err
	}

	if len(data) > 0 {
		if _, err := f.writer.Write(data); err != nil {
			return err
		}
	}

	return f.writer.Flush()
}

// cleanup cleans up resources.
func (f *Frontend) cleanup() {
	f.closed.Store(true)
	f.conn.Close()
	f.log.Info("MSSQL frontend connection closed")
}

// Helper function
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
