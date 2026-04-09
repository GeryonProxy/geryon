package postgresql

import (
	"bufio"
	"crypto/md5"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

// PGCodec implements the PostgreSQL v3 wire protocol.
type PGCodec struct{}

// NewCodec creates a new PostgreSQL codec.
func NewCodec() *PGCodec {
	return &PGCodec{}
}

// Protocol returns the protocol identifier.
func (c *PGCodec) Protocol() common.Protocol {
	return common.ProtocolPostgreSQL
}

// ReadMessage reads one complete message from the connection.
func (c *PGCodec) ReadMessage(r io.Reader) (*common.Message, error) {
	// PostgreSQL message format: 1 byte type + 4 bytes length + payload
	// Note: Startup messages don't have the type byte
	reader := bufio.NewReader(r)

	// Read message type
	msgType, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}

	// Read length (4 bytes, big-endian, includes itself)
	lengthBytes := make([]byte, 4)
	if _, err := io.ReadFull(reader, lengthBytes); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lengthBytes)

	if length < 4 {
		return nil, fmt.Errorf("invalid message length: %d", length)
	}

	// Read payload
	payloadLen := int(length) - 4
	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, err
		}
	}

	// Construct raw message
	raw := make([]byte, 1+4+payloadLen)
	raw[0] = msgType
	copy(raw[1:5], lengthBytes)
	copy(raw[5:], payload)

	return &common.Message{
		Type:      msgType,
		Length:    int32(length),
		Payload:   payload,
		Raw:       raw,
		Direction: common.Frontend,
	}, nil
}

// WriteMessage writes one complete message to the connection.
func (c *PGCodec) WriteMessage(w io.Writer, msg *common.Message) error {
	_, err := w.Write(msg.Raw)
	return err
}

// IsStartup returns true if this is a startup/handshake message.
func (c *PGCodec) IsStartup(msg *common.Message) bool {
	// PostgreSQL startup message doesn't have a message type byte
	// It's identified by the first 4 bytes being the protocol version
	// SSLRequest: 0x04, 0x01, 0x00, 0x00 (80877103 in int32)
	// GSSENCRequest: 0x04, 0x01, 0x00, 0x00 (80877104 in int32)
	// StartupMessage: protocol version (usually 0x00, 0x03, 0x00, 0x00 = 196608)
	if len(msg.Raw) < 8 {
		return false
	}
	// Check for common startup patterns
	firstByte := msg.Raw[0]
	return firstByte == 0x00
}

// IsTerminate returns true if this is a termination message.
func (c *PGCodec) IsTerminate(msg *common.Message) bool {
	return msg.Type == 'X'
}

// IsQuery returns true if this is a query message.
func (c *PGCodec) IsQuery(msg *common.Message) bool {
	return msg.Type == 'Q'
}

// IsTransactionBegin returns true if message starts a transaction.
func (c *PGCodec) IsTransactionBegin(msg *common.Message) bool {
	if msg.Type != 'Q' {
		return false
	}
	query := strings.ToUpper(strings.TrimSpace(c.extractSimpleQuery(msg)))
	return strings.HasPrefix(query, "BEGIN") ||
		strings.HasPrefix(query, "START TRANSACTION")
}

// IsTransactionEnd returns true if message ends a transaction.
func (c *PGCodec) IsTransactionEnd(msg *common.Message) bool {
	if msg.Type == 'Q' {
		query := strings.ToUpper(strings.TrimSpace(c.extractSimpleQuery(msg)))
		return strings.HasPrefix(query, "COMMIT") ||
			strings.HasPrefix(query, "ROLLBACK") ||
			strings.HasPrefix(query, "END")
	}
	// Also check ReadyForQuery with 'I' (idle) status after error
	if msg.Type == 'Z' && msg.Direction == common.Backend {
		return len(msg.Payload) > 0 && msg.Payload[0] == 'I' // Idle = not in transaction
	}
	return false
}

// IsPrepare returns true if this is a prepare statement message.
func (c *PGCodec) IsPrepare(msg *common.Message) bool {
	return msg.Type == 'P' // Parse message
}

// IsExecute returns true if this is an execute prepared stmt message.
func (c *PGCodec) IsExecute(msg *common.Message) bool {
	return msg.Type == 'E' // Execute message
}

// ExtractQuery extracts the SQL query string from a query message.
func (c *PGCodec) ExtractQuery(msg *common.Message) (string, error) {
	switch msg.Type {
	case 'Q':
		return c.extractSimpleQuery(msg), nil
	case 'P':
		return c.extractParseQuery(msg)
	default:
		return "", fmt.Errorf("message type %c does not contain a query", msg.Type)
	}
}

// extractSimpleQuery extracts the query from a Q (Query) message.
func (c *PGCodec) extractSimpleQuery(msg *common.Message) string {
	// Query message: null-terminated string
	for i, b := range msg.Payload {
		if b == 0 {
			return string(msg.Payload[:i])
		}
	}
	return string(msg.Payload)
}

// extractParseQuery extracts the query from a P (Parse) message.
func (c *PGCodec) extractParseQuery(msg *common.Message) (string, error) {
	// Parse message: [name]\0[query]\0[param_types...]
	if len(msg.Payload) < 2 {
		return "", fmt.Errorf("parse message too short")
	}

	// Skip statement name (null-terminated)
	pos := 0
	for pos < len(msg.Payload) && msg.Payload[pos] != 0 {
		pos++
	}
	if pos >= len(msg.Payload) {
		return "", fmt.Errorf("parse message missing null terminator")
	}
	pos++ // skip null

	// Read query (null-terminated)
	queryStart := pos
	for pos < len(msg.Payload) && msg.Payload[pos] != 0 {
		pos++
	}
	if pos >= len(msg.Payload) {
		return "", fmt.Errorf("parse message missing query null terminator")
	}

	return string(msg.Payload[queryStart:pos]), nil
}

// ExtractStatementName extracts the statement name from a Parse message.
func (c *PGCodec) ExtractStatementName(msg *common.Message) (string, error) {
	if msg.Type != 'P' {
		return "", fmt.Errorf("not a Parse message")
	}
	if len(msg.Payload) < 1 {
		return "", fmt.Errorf("parse message too short")
	}

	// Read statement name (null-terminated)
	pos := 0
	for pos < len(msg.Payload) && msg.Payload[pos] != 0 {
		pos++
	}

	return string(msg.Payload[:pos]), nil
}

// ExtractPortalName extracts the portal name from a Bind message.
func (c *PGCodec) ExtractPortalName(msg *common.Message) (string, error) {
	if msg.Type != 'B' {
		return "", fmt.Errorf("not a Bind message")
	}
	if len(msg.Payload) < 1 {
		return "", fmt.Errorf("bind message too short")
	}

	// Bind message: [portal]\0[statement]\0[params...]
	// Read portal name (null-terminated)
	pos := 0
	for pos < len(msg.Payload) && msg.Payload[pos] != 0 {
		pos++
	}

	return string(msg.Payload[:pos]), nil
}

// ExtractBindStatementName extracts the statement name from a Bind message.
func (c *PGCodec) ExtractBindStatementName(msg *common.Message) (string, error) {
	if msg.Type != 'B' {
		return "", fmt.Errorf("not a Bind message")
	}
	if len(msg.Payload) < 2 {
		return "", fmt.Errorf("bind message too short")
	}

	// Bind message: [portal]\0[statement]\0[params...]
	// Skip portal name
	pos := 0
	for pos < len(msg.Payload) && msg.Payload[pos] != 0 {
		pos++
	}
	if pos >= len(msg.Payload) {
		return "", fmt.Errorf("bind message missing null terminator")
	}
	pos++ // skip null

	// Read statement name (null-terminated)
	start := pos
	for pos < len(msg.Payload) && msg.Payload[pos] != 0 {
		pos++
	}

	return string(msg.Payload[start:pos]), nil
}

// IsBind returns true if this is a Bind message.
func (c *PGCodec) IsBind(msg *common.Message) bool {
	return msg.Type == 'B'
}

// IsClose returns true if this is a Close message.
func (c *PGCodec) IsClose(msg *common.Message) bool {
	return msg.Type == 'C'
}

// IsSync returns true if this is a Sync message.
func (c *PGCodec) IsSync(msg *common.Message) bool {
	return msg.Type == 'S'
}

// GenerateResetSequence returns messages to reset server state.
func (c *PGCodec) GenerateResetSequence() []*common.Message {
	// Send DISCARD ALL to reset the connection state
	query := "DISCARD ALL"
	return []*common.Message{c.createQueryMessage(query)}
}

// createQueryMessage creates a Query message.
func (c *PGCodec) createQueryMessage(query string) *common.Message {
	queryBytes := []byte(query)
	length := 4 + len(queryBytes) + 1 // length field + query + null

	buf := make([]byte, 1+length)
	buf[0] = 'Q' // Query message type
	binary.BigEndian.PutUint32(buf[1:5], uint32(length))
	copy(buf[5:], queryBytes)
	buf[5+len(queryBytes)] = 0 // null terminator

	return &common.Message{
		Type:    'Q',
		Length:  int32(length),
		Payload: buf[5 : 5+len(queryBytes)+1],
		Raw:     buf,
	}
}

// CreateSSLRequest creates an SSLRequest message.
func (c *PGCodec) CreateSSLRequest() []byte {
	// SSLRequest: length (4) + SSL request code (80877103)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint32(buf[0:4], 8)
	binary.BigEndian.PutUint32(buf[4:8], 80877103)
	return buf
}

// CreateStartupMessage creates a StartupMessage.
func (c *PGCodec) CreateStartupMessage(user, database string) []byte {
	params := map[string]string{
		"user":     user,
		"database": database,
	}

	// Calculate length
	length := 4 // protocol version
	for k, v := range params {
		length += len(k) + 1 + len(v) + 1
	}
	length += 1 // final null terminator

	buf := make([]byte, 4+length)
	binary.BigEndian.PutUint32(buf[0:4], uint32(length+4))
	binary.BigEndian.PutUint32(buf[4:8], 196608) // Protocol version 3.0

	pos := 8
	for k, v := range params {
		copy(buf[pos:], k)
		pos += len(k)
		buf[pos] = 0
		pos++
		copy(buf[pos:], v)
		pos += len(v)
		buf[pos] = 0
		pos++
	}
	buf[pos] = 0 // final null terminator

	return buf
}

// CreatePasswordMessage creates a PasswordMessage.
func (c *PGCodec) CreatePasswordMessage(password string) []byte {
	passBytes := []byte(password)
	length := 4 + len(passBytes) + 1

	buf := make([]byte, 1+length)
	buf[0] = 'p' // PasswordMessage type
	binary.BigEndian.PutUint32(buf[1:5], uint32(length))
	copy(buf[5:], passBytes)
	buf[5+len(passBytes)] = 0

	return buf
}

// CreateSCRAMResponse creates a SASLInitialResponse for SCRAM.
func (c *PGCodec) CreateSCRAMResponse(mechanism string, data []byte) []byte {
	mechBytes := []byte(mechanism)
	length := 4 + len(mechBytes) + 1 + 4 + len(data)

	buf := make([]byte, 1+length)
	buf[0] = 'p'
	binary.BigEndian.PutUint32(buf[1:5], uint32(length))
	copy(buf[5:], mechBytes)
	buf[5+len(mechBytes)] = 0
	binary.BigEndian.PutUint32(buf[5+len(mechBytes)+1:], uint32(len(data)))
	copy(buf[5+len(mechBytes)+5:], data)

	return buf
}

// IsSSLRequest returns true if the message is an SSL request.
func (c *PGCodec) IsSSLRequest(data []byte) bool {
	if len(data) < 8 {
		return false
	}
	return binary.BigEndian.Uint32(data[4:8]) == 80877103
}

// IsGSSENCRequest returns true if the message is a GSSENC request.
func (c *PGCodec) IsGSSENCRequest(data []byte) bool {
	if len(data) < 8 {
		return false
	}
	return binary.BigEndian.Uint32(data[4:8]) == 80877104
}

// ReadSSLResponse reads the SSL response byte.
func (c *PGCodec) ReadSSLResponse(r io.Reader) (byte, error) {
	buf := make([]byte, 1)
	_, err := io.ReadFull(r, buf)
	return buf[0], err
}

// CreateAuthenticationMD5Password creates an MD5 password auth request.
func CreateAuthenticationMD5Password(salt [4]byte) []byte {
	buf := make([]byte, 1+4+4+4)
	buf[0] = 'R'
	binary.BigEndian.PutUint32(buf[1:5], 8) // length
	binary.BigEndian.PutUint32(buf[5:9], 5) // MD5 password auth type
	copy(buf[9:13], salt[:])
	return buf
}

// CreateAuthenticationSCRAM creates a SCRAM-SHA-256 auth request.
func CreateAuthenticationSCRAM() []byte {
	buf := make([]byte, 1+4+4)
	buf[0] = 'R'
	binary.BigEndian.PutUint32(buf[1:5], 8)
	binary.BigEndian.PutUint32(buf[5:9], 10) // SASL auth type
	// SCRAM-SHA-256\0SCRAM-SHA-256-PLUS\0\0
	mechanisms := []byte("SCRAM-SHA-256\x00SCRAM-SHA-256-PLUS\x00\x00")
	buf = append(buf[:5], mechanisms...)
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(buf)-1))
	return buf
}

// CreateAuthenticationSASLContinue creates a SASLContinue message.
func CreateAuthenticationSASLContinue(challenge []byte) []byte {
	length := 4 + 4 + len(challenge)
	buf := make([]byte, 1+length)
	buf[0] = 'R'
	binary.BigEndian.PutUint32(buf[1:5], uint32(length))
	binary.BigEndian.PutUint32(buf[5:9], 11) // SASL continue
	copy(buf[9:], challenge)
	return buf
}

// CreateAuthenticationSASLFinal creates a SASLFinal message.
func CreateAuthenticationSASLFinal(data []byte) []byte {
	length := 4 + 4 + len(data)
	buf := make([]byte, 1+length)
	buf[0] = 'R'
	binary.BigEndian.PutUint32(buf[1:5], uint32(length))
	binary.BigEndian.PutUint32(buf[5:9], 12) // SASL final
	copy(buf[9:], data)
	return buf
}

// CreateAuthenticationOk creates an AuthenticationOk message.
func CreateAuthenticationOk() []byte {
	buf := make([]byte, 9)
	buf[0] = 'R'
	binary.BigEndian.PutUint32(buf[1:5], 8)
	binary.BigEndian.PutUint32(buf[5:9], 0) // OK
	return buf
}

// CreateErrorResponse creates an ErrorResponse message.
func CreateErrorResponse(code, message string) []byte {
	// ErrorResponse: field type (1 byte) + null-terminated string, terminated by 0 byte
	// Fields: S (severity), C (code), M (message)
	buf := []byte{'E'}
	payload := []byte{}
	payload = append(payload, 'S', 'E', 'R', 'R', 'O', 'R', 0)
	payload = append(payload, 'C')
	payload = append(payload, []byte(code)...)
	payload = append(payload, 0)
	payload = append(payload, 'M')
	payload = append(payload, []byte(message)...)
	payload = append(payload, 0)
	payload = append(payload, 0) // terminator

	length := 4 + len(payload)
	buf = append(buf, make([]byte, 4)...)
	binary.BigEndian.PutUint32(buf[1:5], uint32(length))
	buf = append(buf, payload...)
	return buf
}

// CreateReadyForQuery creates a ReadyForQuery message.
func CreateReadyForQuery(status byte) []byte {
	buf := make([]byte, 6)
	buf[0] = 'Z'
	binary.BigEndian.PutUint32(buf[1:5], 5)
	buf[5] = status // 'I' = idle, 'T' = in transaction block, 'E' = failed transaction block
	return buf
}

// CreateParameterStatus creates a ParameterStatus message.
func CreateParameterStatus(name, value string) []byte {
	nameBytes := []byte(name)
	valueBytes := []byte(value)
	length := 4 + len(nameBytes) + 1 + len(valueBytes) + 1

	buf := make([]byte, 1+length)
	buf[0] = 'S'
	binary.BigEndian.PutUint32(buf[1:5], uint32(length))
	copy(buf[5:], nameBytes)
	buf[5+len(nameBytes)] = 0
	copy(buf[5+len(nameBytes)+1:], valueBytes)
	buf[5+len(nameBytes)+1+len(valueBytes)] = 0
	return buf
}

// MD5PasswordHash computes the MD5 password hash used by PostgreSQL.
func MD5PasswordHash(user, password string, salt [4]byte) string {
	// PostgreSQL MD5 auth: md5(password + user) + salt
	inner := md5.Sum([]byte(password + user))
	innerHex := hex.EncodeToString(inner[:])
	outer := md5.Sum(append([]byte(innerHex), salt[:]...))
	return "md5" + hex.EncodeToString(outer[:])
}

// SHA256PasswordHash computes the SHA256 password hash.
func SHA256PasswordHash(password string, salt []byte, iterations int) []byte {
	// SCRAM-SHA-256: Hi(Normalize(password), salt, iterations)
	// This is a simplified version - full SCRAM is more complex
	h := sha256.New()
	h.Write([]byte(password))
	h.Write(salt)
	return h.Sum(nil)
}
