// Package postgresql implements the PostgreSQL wire protocol v3.
package postgresql

import (
	"bufio"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Message types
const (
	// Frontend messages (client -> server)
	MsgBind             = 'B'
	MsgClose            = 'C'
	MsgDescribe         = 'D'
	MsgExecute          = 'E'
	MsgFunctionCall     = 'F'
	MsgParse            = 'P'
	MsgPasswordMessage  = 'p'
	MsgQuery            = 'Q'
	MsgSync             = 'S'
	MsgTerminate        = 'X'
	MsgCopyData         = 'd'
	MsgCopyDone         = 'c'
	MsgCopyFail         = 'f'
	MsgExecutePrepared  = 'E'

	// Backend messages (server -> client)
	MsgAuthentication      = 'R'
	MsgBackendKeyData      = 'K'
	MsgBindComplete        = '2'
	MsgCloseComplete       = '3'
	MsgCommandComplete     = 'C'
	MsgCopyInResponse      = 'G'
	MsgCopyOutResponse     = 'H'
	MsgCopyBothResponse    = 'W'
	MsgDataRow             = 'D'
	MsgEmptyQueryResponse  = 'I'
	MsgErrorResponse       = 'E'
	MsgFunctionCallResponse= 'V'
	MsgNoData              = 'n'
	MsgNoticeResponse      = 'N'
	MsgNotificationResponse= 'A'
	MsgParameterDescription= 't'
	MsgParameterStatus     = 'S'
	MsgParseComplete       = '1'
	MsgPortalSuspended     = 's'
	MsgReadyForQuery       = 'Z'
	MsgRowDescription      = 'T'
)

// Authentication types
const (
	AuthOK                = 0
	AuthKerberosV5        = 2
	AuthCleartextPassword = 3
	AuthMD5Password       = 5
	AuthSCMCredential     = 6
	AuthGSS               = 7
	AuthGSSContinue       = 8
	AuthSSPI              = 9
	AuthSASL              = 10
	AuthSASLContinue      = 11
	AuthSASLFinal         = 12
)

// Transaction status
const (
	TxIdle    = 'I'
	TxActive  = 'T'
	TxError   = 'E'
)

// Message represents a PostgreSQL protocol message.
type Message struct {
	Type byte
	Data []byte
}

// Header represents the message header.
type Header struct {
	Type byte
	Length int32
}

// StartupMessage represents the initial connection message.
type StartupMessage struct {
	ProtocolVersion int32
	Parameters      map[string]string
}

// SSLRequest represents an SSL upgrade request.
type SSLRequest struct {
	// SSL request is identified by special protocol version number
}

// PasswordMessage represents a password response.
type PasswordMessage struct {
	Password string
}

// QueryMessage represents a simple query.
type QueryMessage struct {
	Query string
}

// ParseMessage represents a parse request for prepared statements.
type ParseMessage struct {
	Name          string
	Query         string
	ParameterTypes []int32
}

// BindMessage represents a bind request.
type BindMessage struct {
	PortalName     string
	StatementName  string
	ParameterFormats []int16
	Parameters       [][]byte
	ResultFormats    []int16
}

// ExecuteMessage represents an execute request.
type ExecuteMessage struct {
	PortalName string
	MaxRows    int32
}

// CommandCompleteMessage represents command completion.
type CommandCompleteMessage struct {
	Tag string
}

// ErrorResponseMessage represents an error.
type ErrorResponseMessage struct {
	Severity         string
	SQLState         string
	Message          string
	Detail           string
	Hint             string
	Position         string
	InternalPosition string
	InternalQuery    string
	Where            string
	SchemaName       string
	TableName        string
	ColumnName       string
	DataTypeName     string
	ConstraintName   string
	File             string
	Line             string
	Routine          string
}

// FieldDescription represents a field in a row description.
type FieldDescription struct {
	Name         string
	TableOID     int32
	ColumnNumber int16
	TypeOID      int32
	TypeSize     int16
	TypeModifier int32
	Format       int16
}

// RowDescriptionMessage represents the structure of rows.
type RowDescriptionMessage struct {
	Fields []FieldDescription
}

// DataRowMessage represents a row of data.
type DataRowMessage struct {
	Values [][]byte
}

// ParameterStatusMessage represents a parameter status update.
type ParameterStatusMessage struct {
	Name  string
	Value string
}

// AuthenticationMessage represents an authentication request.
type AuthenticationMessage struct {
	Type   int32
	Salt   []byte // For MD5
	Data   []byte // For SASL
}

// ReadyForQueryMessage represents transaction status.
type ReadyForQueryMessage struct {
	Status byte
}

// Connection represents a PostgreSQL protocol connection.
type Connection struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer

	// Connection state
	ProcessID int32
	SecretKey int32
	TxStatus  byte

	// Parameters
	Parameters map[string]string

	// Prepared statements
	PreparedStatements map[string]*PreparedStatement

	// Portals
	Portals map[string]*Portal
}

// PreparedStatement represents a prepared statement.
type PreparedStatement struct {
	Name          string
	Query         string
	ParameterTypes []int32
	RowDescription *RowDescriptionMessage
}

// Portal represents a bound portal.
type Portal struct {
	Name           string
	Statement      *PreparedStatement
	ParameterFormats []int16
	Parameters       [][]byte
	ResultFormats    []int16
}

// NewConnection creates a new PostgreSQL protocol connection.
func NewConnection(conn net.Conn) *Connection {
	return &Connection{
		conn:               conn,
		reader:             bufio.NewReader(conn),
		writer:             bufio.NewWriter(conn),
		Parameters:         make(map[string]string),
		PreparedStatements: make(map[string]*PreparedStatement),
		Portals:            make(map[string]*Portal),
		TxStatus:           TxIdle,
	}
}

// Close closes the connection.
func (c *Connection) Close() error {
	return c.conn.Close()
}

// SetDeadline sets read/write deadlines.
func (c *Connection) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

// ReadStartupMessage reads the initial startup message.
func (c *Connection) ReadStartupMessage() (*StartupMessage, error) {
	// Read message length
	var length int32
	if err := binary.Read(c.reader, binary.BigEndian, &length); err != nil {
		return nil, err
	}

	// Bound startup message size (16MB max)
	const maxStartupLen = 16 << 20
	if length < 8 || length > maxStartupLen {
		return nil, fmt.Errorf("invalid startup message length: %d", length)
	}

	// Check for SSL request (special protocol version 1234/5678)
	if length == 8 {
		var version int32
		if err := binary.Read(c.reader, binary.BigEndian, &version); err != nil {
			return nil, err
		}
		if version == 80877103 { // SSL request code
			return nil, fmt.Errorf("SSL request")
		}
		// Put back the version for normal processing
		// (This is simplified - in real impl we'd handle this differently)
	}

	// Read the rest of the message
	data := make([]byte, length-4)
	if _, err := io.ReadFull(c.reader, data); err != nil {
		return nil, err
	}

	// Parse protocol version
	version := int32(binary.BigEndian.Uint32(data[0:4]))

	// Parse parameters (null-terminated strings)
	params := make(map[string]string)
	offset := 4
	for offset < len(data)-1 {
		// Find null terminator
		end := offset
		for end < len(data) && data[end] != 0 {
			end++
		}
		if end >= len(data) {
			break
		}

		key := string(data[offset:end])
		offset = end + 1

		if offset >= len(data) {
			break
		}

		end = offset
		for end < len(data) && data[end] != 0 {
			end++
		}

		value := string(data[offset:end])
		offset = end + 1

		params[key] = value
	}

	return &StartupMessage{
		ProtocolVersion: version,
		Parameters:      params,
	}, nil
}

// ReadMessage reads a protocol message.
func (c *Connection) ReadMessage() (*Message, error) {
	// Read message type
	msgType, err := c.reader.ReadByte()
	if err != nil {
		return nil, err
	}

	// Read message length
	var length int32
	if err := binary.Read(c.reader, binary.BigEndian, &length); err != nil {
		return nil, err
	}

	// Bound message size (16MB max)
	const maxMsgLen = 16 << 20
	if length < 4 || length > maxMsgLen {
		return nil, fmt.Errorf("invalid message length: %d", length)
	}

	// Read message data (length includes the 4-byte length field itself)
	data := make([]byte, length-4)
	if _, err := io.ReadFull(c.reader, data); err != nil {
		return nil, err
	}

	return &Message{Type: msgType, Data: data}, nil
}

// WriteMessage writes a protocol message.
func (c *Connection) WriteMessage(msgType byte, data []byte) error {
	// Write message type
	if err := c.writer.WriteByte(msgType); err != nil {
		return err
	}

	// Write message length (includes the 4-byte length field)
	length := int32(len(data) + 4)
	if err := binary.Write(c.writer, binary.BigEndian, length); err != nil {
		return err
	}

	// Write data
	if _, err := c.writer.Write(data); err != nil {
		return err
	}

	return c.writer.Flush()
}

// WriteRaw writes raw bytes (for startup messages).
func (c *Connection) WriteRaw(data []byte) error {
	if _, err := c.writer.Write(data); err != nil {
		return err
	}
	return c.writer.Flush()
}

// SendAuthenticationOK sends an authentication OK message.
func (c *Connection) SendAuthenticationOK() error {
	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, AuthOK)
	return c.WriteMessage(MsgAuthentication, data)
}

// SendAuthenticationMD5 sends an MD5 password challenge.
func (c *Connection) SendAuthenticationMD5(salt []byte) error {
	data := make([]byte, 8)
	binary.BigEndian.PutUint32(data[0:4], AuthMD5Password)
	copy(data[4:8], salt)
	return c.WriteMessage(MsgAuthentication, data)
}

// SendAuthenticationCleartext requests cleartext password.
func (c *Connection) SendAuthenticationCleartext() error {
	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, AuthCleartextPassword)
	return c.WriteMessage(MsgAuthentication, data)
}

// SendParameterStatus sends a parameter status message.
func (c *Connection) SendParameterStatus(name, value string) error {
	data := []byte(name + "\x00" + value + "\x00")
	return c.WriteMessage(MsgParameterStatus, data)
}

// SendBackendKeyData sends backend key data for cancellation.
func (c *Connection) SendBackendKeyData(processID, secretKey int32) error {
	data := make([]byte, 8)
	binary.BigEndian.PutUint32(data[0:4], uint32(processID))
	binary.BigEndian.PutUint32(data[4:8], uint32(secretKey))
	c.ProcessID = processID
	c.SecretKey = secretKey
	return c.WriteMessage(MsgBackendKeyData, data)
}

// SendReadyForQuery sends ready for query message.
func (c *Connection) SendReadyForQuery(status byte) error {
	c.TxStatus = status
	return c.WriteMessage(MsgReadyForQuery, []byte{status})
}

// SendRowDescription sends row description.
func (c *Connection) SendRowDescription(fields []FieldDescription) error {
	// Calculate size
	size := 2 // field count
	for _, f := range fields {
		size += 4 + len(f.Name) + 1 // name (null-terminated) + int32
		size += 4 // table OID
		size += 2 // column number
		size += 4 // type OID
		size += 2 // type size
		size += 4 // type modifier
		size += 2 // format
	}

	data := make([]byte, size)
	offset := 0

	// Field count
	binary.BigEndian.PutUint16(data[offset:], uint16(len(fields)))
	offset += 2

	// Fields
	for _, f := range fields {
		// Name
		nameBytes := []byte(f.Name)
		copy(data[offset:], nameBytes)
		offset += len(nameBytes)
		data[offset] = 0
		offset++

		// Table OID
		binary.BigEndian.PutUint32(data[offset:], uint32(f.TableOID))
		offset += 4

		// Column number
		binary.BigEndian.PutUint16(data[offset:], uint16(f.ColumnNumber))
		offset += 2

		// Type OID
		binary.BigEndian.PutUint32(data[offset:], uint32(f.TypeOID))
		offset += 4

		// Type size
		binary.BigEndian.PutUint16(data[offset:], uint16(f.TypeSize))
		offset += 2

		// Type modifier
		binary.BigEndian.PutUint32(data[offset:], uint32(f.TypeModifier))
		offset += 4

		// Format
		binary.BigEndian.PutUint16(data[offset:], uint16(f.Format))
		offset += 2
	}

	return c.WriteMessage(MsgRowDescription, data)
}

// SendDataRow sends a data row.
func (c *Connection) SendDataRow(values [][]byte) error {
	// Calculate size
	size := 2 // value count
	for _, v := range values {
		size += 4 // length
		if v != nil {
			size += len(v)
		}
	}

	data := make([]byte, size)
	offset := 0

	// Value count
	binary.BigEndian.PutUint16(data[offset:], uint16(len(values)))
	offset += 2

	// Values
	for _, v := range values {
		if v == nil {
			// NULL value
			binary.BigEndian.PutUint32(data[offset:], 0xFFFFFFFF)
			offset += 4
		} else {
			binary.BigEndian.PutUint32(data[offset:], uint32(len(v)))
			offset += 4
			copy(data[offset:], v)
			offset += len(v)
		}
	}

	return c.WriteMessage(MsgDataRow, data)
}

// SendCommandComplete sends command complete.
func (c *Connection) SendCommandComplete(tag string) error {
	data := []byte(tag + "\x00")
	return c.WriteMessage(MsgCommandComplete, data)
}

// SendErrorResponse sends an error response.
func (c *Connection) SendErrorResponse(severity, code, message string) error {
	// Build error fields
	var fields []byte

	// Severity
	fields = append(fields, 'S')
	fields = append(fields, []byte(severity)...)
	fields = append(fields, 0)

	// Code
	fields = append(fields, 'C')
	fields = append(fields, []byte(code)...)
	fields = append(fields, 0)

	// Message
	fields = append(fields, 'M')
	fields = append(fields, []byte(message)...)
	fields = append(fields, 0)

	// Null terminator
	fields = append(fields, 0)

	return c.WriteMessage(MsgErrorResponse, fields)
}

// SendEmptyQueryResponse sends empty query response.
func (c *Connection) SendEmptyQueryResponse() error {
	return c.WriteMessage(MsgEmptyQueryResponse, nil)
}

// SendParseComplete sends parse complete.
func (c *Connection) SendParseComplete() error {
	return c.WriteMessage(MsgParseComplete, nil)
}

// SendBindComplete sends bind complete.
func (c *Connection) SendBindComplete() error {
	return c.WriteMessage(MsgBindComplete, nil)
}

// SendCloseComplete sends close complete.
func (c *Connection) SendCloseComplete() error {
	return c.WriteMessage(MsgCloseComplete, nil)
}

// SendNoData sends no data.
func (c *Connection) SendNoData() error {
	return c.WriteMessage(MsgNoData, nil)
}

// ParsePasswordMessage parses a password message.
func ParsePasswordMessage(data []byte) (*PasswordMessage, error) {
	// Password is a null-terminated string
	idx := strings.IndexByte(string(data), 0)
	if idx < 0 {
		idx = len(data)
	}
	return &PasswordMessage{Password: string(data[:idx])}, nil
}

// ParseQueryMessage parses a query message.
func ParseQueryMessage(data []byte) (*QueryMessage, error) {
	// Query is a null-terminated string
	idx := strings.IndexByte(string(data), 0)
	if idx < 0 {
		idx = len(data)
	}
	return &QueryMessage{Query: string(data[:idx])}, nil
}

// ParseParseMessage parses a parse message.
func ParseParseMessage(data []byte) (*ParseMessage, error) {
	msg := &ParseMessage{}
	offset := 0

	// Statement name
	idx := indexNull(data[offset:])
	if idx < 0 {
		return nil, fmt.Errorf("invalid parse message")
	}
	msg.Name = string(data[offset:offset+idx])
	offset += idx + 1

	// Query
	idx = indexNull(data[offset:])
	if idx < 0 {
		return nil, fmt.Errorf("invalid parse message")
	}
	msg.Query = string(data[offset:offset+idx])
	offset += idx + 1

	// Parameter types (optional in some cases)
	if offset < len(data) {
		numTypes := binary.BigEndian.Uint16(data[offset:])
		offset += 2

		msg.ParameterTypes = make([]int32, numTypes)
		for i := 0; i < int(numTypes); i++ {
			msg.ParameterTypes[i] = int32(binary.BigEndian.Uint32(data[offset:]))
			offset += 4
		}
	}

	return msg, nil
}

// ParseBindMessage parses a bind message.
func ParseBindMessage(data []byte) (*BindMessage, error) {
	msg := &BindMessage{}
	offset := 0

	// Portal name
	idx := indexNull(data[offset:])
	if idx < 0 {
		return nil, fmt.Errorf("invalid bind message")
	}
	msg.PortalName = string(data[offset:offset+idx])
	offset += idx + 1

	// Statement name
	idx = indexNull(data[offset:])
	if idx < 0 {
		return nil, fmt.Errorf("invalid bind message")
	}
	msg.StatementName = string(data[offset:offset+idx])
	offset += idx + 1

	// Parameter formats
	numFormats := binary.BigEndian.Uint16(data[offset:])
	offset += 2
	msg.ParameterFormats = make([]int16, numFormats)
	for i := 0; i < int(numFormats); i++ {
		msg.ParameterFormats[i] = int16(binary.BigEndian.Uint16(data[offset:]))
		offset += 2
	}

	// Parameters
	numParams := binary.BigEndian.Uint16(data[offset:])
	offset += 2
	msg.Parameters = make([][]byte, numParams)
	for i := 0; i < int(numParams); i++ {
		length := int32(binary.BigEndian.Uint32(data[offset:]))
		offset += 4
		if length == -1 {
			// NULL
			msg.Parameters[i] = nil
		} else {
			msg.Parameters[i] = data[offset:offset+int(length)]
			offset += int(length)
		}
	}

	// Result formats
	numResultFormats := binary.BigEndian.Uint16(data[offset:])
	offset += 2
	msg.ResultFormats = make([]int16, numResultFormats)
	for i := 0; i < int(numResultFormats); i++ {
		msg.ResultFormats[i] = int16(binary.BigEndian.Uint16(data[offset:]))
		offset += 2
	}

	return msg, nil
}

// ParseExecuteMessage parses an execute message.
func ParseExecuteMessage(data []byte) (*ExecuteMessage, error) {
	msg := &ExecuteMessage{}
	offset := 0

	// Portal name
	idx := indexNull(data[offset:])
	if idx < 0 {
		return nil, fmt.Errorf("invalid execute message")
	}
	msg.PortalName = string(data[offset:offset+idx])
	offset += idx + 1

	// Max rows
	msg.MaxRows = int32(binary.BigEndian.Uint32(data[offset:]))

	return msg, nil
}

// indexNull finds the null byte index.
func indexNull(data []byte) int {
	for i, b := range data {
		if b == 0 {
			return i
		}
	}
	return -1
}

// MD5Password computes MD5 password hash for PostgreSQL.
func MD5Password(username, password string, salt []byte) string {
	// md5(password + username)
	h1 := md5.New()
	h1.Write([]byte(password))
	h1.Write([]byte(username))
	hash1 := hex.EncodeToString(h1.Sum(nil))

	// md5(hash1 + salt)
	h2 := md5.New()
	h2.Write([]byte(hash1))
	h2.Write(salt)
	return "md5" + hex.EncodeToString(h2.Sum(nil))
}

// VerifyMD5Password verifies an MD5 password.
func VerifyMD5Password(username, password, hash string, salt []byte) bool {
	expected := MD5Password(username, password, salt)
	return expected == hash
}

// GenerateSASLSCRAMSHA256 generates SASL SCRAM-SHA-256 authentication.
func GenerateSASLSCRAMSHA256() (clientFirst, serverFirst string, salt []byte, iteration int) {
	// Simplified implementation - full SCRAM is complex
	salt = make([]byte, 16)
	// In real implementation, generate random salt
	return "", "", salt, 4096
}
