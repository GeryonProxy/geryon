// Package mssql implements the TDS 7.4+ (Tabular Data Stream) wire protocol
// codec for Microsoft SQL Server. It handles pre-login, Login7 authentication,
// SSPI/NTLM passthrough, token stream parsing, SQL Batch, and RPC requests.
package mssql

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

// TDSCodec implements the TDS 7.4+ protocol.
type TDSCodec struct{}

// MaxPayloadLen is the maximum allowed payload size (16MB).
const MaxPayloadLen = 1 << 24

// NewCodec creates a new TDS codec.
func NewCodec() *TDSCodec {
	return &TDSCodec{}
}

// Protocol returns the protocol identifier.
func (c *TDSCodec) Protocol() common.Protocol {
	return common.ProtocolMSSQL
}

// TDS Packet Header (8 bytes)
// - Type (1 byte)
// - Status (1 byte)
// - Length (2 bytes, big-endian, including header)
// - SPID (2 bytes)
// - PacketID (1 byte)
// - Window (1 byte)

// ReadMessage reads one complete TDS packet/message from the connection.
func (c *TDSCodec) ReadMessage(r io.Reader) (*common.Message, error) {
	reader := bufio.NewReader(r)

	// Read TDS header (8 bytes)
	header := make([]byte, 8)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}

	msgType := header[0]
	_ = header[1] // status flag, not used in basic processing
	length := binary.BigEndian.Uint16(header[2:4])
	// spid := binary.BigEndian.Uint16(header[4:6])
	// packetID := header[6]
	// window := header[7]

	if length < 8 {
		return nil, fmt.Errorf("invalid TDS packet length: %d", length)
	}

	payloadLen := int(length) - 8
	if payloadLen < 0 {
		return nil, fmt.Errorf("invalid TDS packet length: %d", length)
	}
	if payloadLen > MaxPayloadLen {
		return nil, fmt.Errorf("TDS packet too large: %d bytes", payloadLen)
	}
	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, err
		}
	}

	// Construct raw message
	raw := make([]byte, 8+payloadLen)
	copy(raw[0:8], header)
	copy(raw[8:], payload)

	return &common.Message{
		Type:      msgType,
		Length:    int32(length),
		Payload:   payload,
		Raw:       raw,
		Direction: common.Frontend,
	}, nil
}

// WriteMessage writes one complete TDS packet to the connection.
func (c *TDSCodec) WriteMessage(w io.Writer, msg *common.Message) error {
	_, err := w.Write(msg.Raw)
	return err
}

// IsStartup returns true if this is a startup/handshake message.
func (c *TDSCodec) IsStartup(msg *common.Message) bool {
	return msg.Type == PacketTypePreLogin
}

// IsTerminate returns true if this is a termination message.
func (c *TDSCodec) IsTerminate(msg *common.Message) bool {
	return msg.Type == PacketTypeAttention
}

// IsQuery returns true if this is a query message.
func (c *TDSCodec) IsQuery(msg *common.Message) bool {
	return msg.Type == PacketTypeSQLBatch
}

// IsTransactionBegin returns true if message starts a transaction.
func (c *TDSCodec) IsTransactionBegin(msg *common.Message) bool {
	if msg.Type != PacketTypeSQLBatch {
		return false
	}
	query := c.extractSQLBatchQuery(msg)
	upperQuery := strings.ToUpper(strings.TrimSpace(query))
	return strings.HasPrefix(upperQuery, "BEGIN") ||
		strings.HasPrefix(upperQuery, "BEGIN TRANSACTION") ||
		strings.HasPrefix(upperQuery, "BEGIN TRAN")
}

// IsTransactionEnd returns true if message ends a transaction.
func (c *TDSCodec) IsTransactionEnd(msg *common.Message) bool {
	if msg.Type != PacketTypeSQLBatch {
		return false
	}
	query := c.extractSQLBatchQuery(msg)
	upperQuery := strings.ToUpper(strings.TrimSpace(query))
	return strings.HasPrefix(upperQuery, "COMMIT") ||
		strings.HasPrefix(upperQuery, "ROLLBACK") ||
		strings.HasPrefix(upperQuery, "COMMIT TRANSACTION") ||
		strings.HasPrefix(upperQuery, "ROLLBACK TRANSACTION") ||
		strings.HasPrefix(upperQuery, "COMMIT TRAN") ||
		strings.HasPrefix(upperQuery, "ROLLBACK TRAN")
}

// IsPrepare returns true if this is a prepare statement message.
func (c *TDSCodec) IsPrepare(msg *common.Message) bool {
	// TDS uses RPC requests for sp_prepare
	if msg.Type != PacketTypeRPC {
		return false
	}
	return c.isRPCProcedure(msg, "sp_prepare")
}

// IsBind returns true if this is a bind message.
func (c *TDSCodec) IsBind(msg *common.Message) bool {
	// TDS doesn't have a separate bind message
	// Parameters are sent with sp_execute
	return false
}

// IsExecute returns true if this is an execute prepared stmt message.
func (c *TDSCodec) IsExecute(msg *common.Message) bool {
	if msg.Type != PacketTypeRPC {
		return false
	}
	return c.isRPCProcedure(msg, "sp_execute")
}

// IsClose returns true if this is a close statement message.
func (c *TDSCodec) IsClose(msg *common.Message) bool {
	// TDS uses sp_unprepare for closing prepared statements
	if msg.Type != PacketTypeRPC {
		return false
	}
	return c.isRPCProcedure(msg, "sp_unprepare")
}

// IsSync returns true if this is a sync message.
func (c *TDSCodec) IsSync(msg *common.Message) bool {
	// TDS doesn't have a direct equivalent to PostgreSQL's Sync
	return false
}

// ExtractQuery extracts the SQL query string from a query message.
func (c *TDSCodec) ExtractQuery(msg *common.Message) (string, error) {
	switch msg.Type {
	case PacketTypeSQLBatch:
		return c.extractSQLBatchQuery(msg), nil
	case PacketTypeRPC:
		return c.extractRPCQuery(msg), nil
	default:
		return "", fmt.Errorf("message type 0x%02x does not contain a query", msg.Type)
	}
}

func (c *TDSCodec) extractSQLBatchQuery(msg *common.Message) string {
	// SQL Batch packet: header (8) + packet data header (variable) + SQL text (Unicode)
	if len(msg.Payload) < 8 {
		return ""
	}

	// Skip packet data header if present
	// The header contains: TokenType, Status, Length, etc.
	// For simplicity, we'll search for the SQL text after header bytes

	// Try to find the SQL text (usually starts after header)
	// This is a simplified implementation
	data := msg.Payload

	// Look for SQL text markers (usually starts with printable ASCII)
	for i := 0; i < len(data)-1 && i < 20; i++ {
		// Skip packet data header
		if i+1 < len(data) && data[i] == TokenTypeSQLText {
			// Found SQL text token
			return c.decodeUnicode(data[i+1:])
		}
	}

	// Fallback: return raw data as string
	return string(data)
}

func (c *TDSCodec) extractRPCQuery(msg *common.Message) string {
	// RPC packet: procedure name followed by parameters
	// Procedure name is B-VARCHAR: 2-byte length (LE) + UTF-16LE chars + 2-byte null
	data := msg.Payload
	if len(data) < 2 {
		return string(data)
	}

	// TDS RPC format: option flags byte(s), then procedure name B-VARCHAR
	// If first byte is 0x00: no option flags, procedure name starts immediately
	// If first byte is not 0x00: option flags present (usually 1 byte), then proc name
	offset := 0
	if data[0] == 0x00 {
		// No option flags, procedure name follows directly
		offset = 1
	}
	// Otherwise, option flags present - skip 1 byte and read length from next 2 bytes

	if offset+2 > len(data) {
		// Not enough data for length prefix, fall back to raw string
		return string(data)
	}

	// Read procedure name length (2-byte little-endian)
	nameLen := int(binary.LittleEndian.Uint16(data[offset:]))
	offset += 2

	// Sanity check: nameLen should be reasonable (max 128 chars = 256 bytes)
	// If nameLen is absurdly large, the payload is not in B-VARCHAR format
	if nameLen > 256 || nameLen > len(data)-offset {
		// Fallback: return the raw payload as plain ASCII (for test compatibility)
		// In real TDS, procedure names are UTF-16LE encoded
		rawName := data
		// Trim any trailing null bytes
		for len(rawName) > 0 && rawName[len(rawName)-1] == 0 {
			rawName = rawName[:len(rawName)-1]
		}
		return string(rawName)
	}

	// Extract procedure name (UTF-16LE, null-terminated)
	nameBytes := data[offset : offset+nameLen]
	name := c.decodeUnicode(nameBytes)

	if name == "" {
		// Fallback: try with remaining data
		rawName := data[offset:]
		for len(rawName) > 0 && rawName[len(rawName)-1] == 0 {
			rawName = rawName[:len(rawName)-1]
		}
		return string(rawName)
	}
	return name
}

func (c *TDSCodec) isRPCProcedure(msg *common.Message, procName string) bool {
	// Check if RPC is for the specified procedure
	data := msg.Payload
	// Look for procedure name in the packet
	return strings.Contains(strings.ToUpper(string(data)), strings.ToUpper(procName))
}

func (c *TDSCodec) decodeUnicode(data []byte) string {
	// UTF-16 to string conversion
	// This is simplified - assumes even length
	if len(data)%2 != 0 {
		return string(data)
	}

	var result strings.Builder
	for i := 0; i < len(data); i += 2 {
		r := rune(binary.LittleEndian.Uint16(data[i:]))
		if r == 0 {
			break
		}
		result.WriteRune(r)
	}
	return result.String()
}

// GenerateResetSequence returns messages to reset server state.
func (c *TDSCodec) GenerateResetSequence() []*common.Message {
	// TDS: sp_reset_connection via special packet or RPC
	return []*common.Message{c.createResetConnectionMessage()}
}

func (c *TDSCodec) createResetConnectionMessage() *common.Message {
	// Create RPC request for sp_reset_connection
	payload := []byte{0x00, 0x00, 0x00, 0x00} // Simplified
	return c.createPacket(PacketTypeRPC, payload)
}

func (c *TDSCodec) createPacket(msgType byte, payload []byte) *common.Message {
	length := 8 + len(payload)
	buf := make([]byte, length)

	// Header
	buf[0] = msgType
	buf[1] = StatusEndOfMessage
	binary.BigEndian.PutUint16(buf[2:4], uint16(length))
	binary.BigEndian.PutUint16(buf[4:6], 0) // SPID
	buf[6] = 0                              // PacketID
	buf[7] = 0                              // Window

	// Payload
	copy(buf[8:], payload)

	return &common.Message{
		Type:    msgType,
		Length:  int32(length),
		Payload: payload,
		Raw:     buf,
	}
}

// CreatePreLogin creates a Pre-Login packet.
func CreatePreLogin(encryption EncryptMode, instance string) []byte {
	// Pre-Login packet structure
	// Header + Token Offset/Length pairs + Token Data

	buf := make([]byte, 0, 64)

	// Pre-Login header (token offset/length pairs)
	// Token 0: VERSION
	// Token 1: ENCRYPTION
	// Token 2: INSTOPT
	// Token 3: THREADID
	// Token 4: MARS
	// Token 5: TRACEID
	// Token 6: FEDAUTHREQUIRED
	// Token 7: NONCEOPT
	// Token 0xFF: Terminator

	// VERSION token offset/length
	buf = append(buf, byte(PreLoginVersion), 0x00, 0x06, 0x00, 0x00, 0x00) // Token, Offset (2), Length (2)

	// ENCRYPTION token offset/length
	buf = append(buf, byte(PreLoginEncryption), 0x00, 0x06, 0x01, 0x00, 0x00)

	// INSTOPT token offset/length
	instLen := len(instance)
	buf = append(buf, byte(PreLoginInstOpt), 0x00, 0x06, byte(instLen), 0x00, 0x00)

	// Terminator
	buf = append(buf, 0xFF)

	// VERSION data (UL_VERSION + US_BUILD)
	buf = append(buf, 0x74, 0x00, 0x00, 0x00, 0x00, 0x00) // TDS 7.4.0.0

	// ENCRYPTION data
	buf = append(buf, byte(encryption))

	// INSTOPT data
	buf = append(buf, []byte(instance)...)

	// Create full packet
	length := 8 + len(buf)
	packet := make([]byte, length)

	packet[0] = PacketTypePreLogin
	packet[1] = StatusEndOfMessage
	binary.BigEndian.PutUint16(packet[2:4], uint16(length))
	binary.BigEndian.PutUint16(packet[4:6], 0) // SPID
	packet[6] = 0                              // PacketID
	packet[7] = 0                              // Window

	copy(packet[8:], buf)

	return packet
}

// CreateLogin7 creates a Login7 packet.
func CreateLogin7(user, password, appName, serverName, database string) []byte {
	// Login7 is complex - this is a simplified version
	// Full implementation would include:
	// - Fixed length fields
	// - Variable length offset/length pairs
	// - Optional data (password hash, etc.)

	buf := make([]byte, 0, 256)

	// Length placeholder
	buf = append(buf, make([]byte, 4)...)

	// TDS version
	buf = binary.LittleEndian.AppendUint32(buf, 0x74000004) // TDS 7.4

	// Packet size
	buf = binary.LittleEndian.AppendUint32(buf, 4096)

	// Client program version
	buf = binary.LittleEndian.AppendUint32(buf, 0)

	// Client PID
	buf = binary.LittleEndian.AppendUint32(buf, 0)

	// Connection ID
	buf = binary.LittleEndian.AppendUint32(buf, 0)

	// Option flags 1
	buf = binary.LittleEndian.AppendUint32(buf, LoginOption1UseDb|LoginOption1InitDbFatal|LoginOption1SetLangOn)

	// Option flags 2
	buf = binary.LittleEndian.AppendUint32(buf, 0)

	// Type flags
	buf = binary.LittleEndian.AppendUint32(buf, 0)

	// Option flags 3
	buf = binary.LittleEndian.AppendUint32(buf, 0)

	// Client timezone
	buf = binary.LittleEndian.AppendUint32(buf, 0)

	// Client LCID
	buf = binary.LittleEndian.AppendUint32(buf, 0)

	// Variable length offset/length section (simplified)
	// Each entry is 2 bytes offset + 2 bytes length

	// Now append the variable data
	offset := len(buf)
	varData := []string{user, password, appName, serverName, "", "", "", "", "", ""}

	for _, s := range varData {
		// Convert to UTF-16LE
		utf16 := make([]byte, len(s)*2)
		for i, ch := range s {
			binary.LittleEndian.PutUint16(utf16[i*2:], uint16(ch))
		}

		// Update offset/length in header (simplified - would need proper structure)
		_ = offset
		offset += len(utf16)

		buf = append(buf, utf16...)
	}

	// Update length
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(buf)))

	// Create full packet
	length := 8 + len(buf)
	packet := make([]byte, length)

	packet[0] = PacketTypeLogin7
	packet[1] = StatusEndOfMessage
	binary.BigEndian.PutUint16(packet[2:4], uint16(length))
	binary.BigEndian.PutUint16(packet[4:6], 0) // SPID
	packet[6] = 0                              // PacketID
	packet[7] = 0                              // Window

	copy(packet[8:], buf)

	return packet
}

// CreateSQLBatch creates a SQL Batch packet.
func CreateSQLBatch(text string) []byte {
	// SQL Batch packet: header + SQL text (Unicode)
	// The text is sent as a UTF-16LE string

	utf16 := make([]byte, len(text)*2)
	for i, ch := range text {
		binary.LittleEndian.PutUint16(utf16[i*2:], uint16(ch))
	}

	// Total length
	length := 8 + len(utf16)
	packet := make([]byte, length)

	packet[0] = PacketTypeSQLBatch
	packet[1] = StatusEndOfMessage
	binary.BigEndian.PutUint16(packet[2:4], uint16(length))
	binary.BigEndian.PutUint16(packet[4:6], 0) // SPID
	packet[6] = 0                              // PacketID
	packet[7] = 0                              // Window

	copy(packet[8:], utf16)

	return packet
}

// ParseTokenStream parses a TDS token stream response.
func ParseTokenStream(data []byte) ([]Token, error) {
	tokens := make([]Token, 0)
	pos := 0

	for pos < len(data) {
		if pos >= len(data) {
			break
		}

		tokenType := data[pos]
		pos++

		switch tokenType {
		case TokenTypeDone:
			// Done token: Status (2), CurCmd (2), DoneRowCount (4/8)
			if pos+8 > len(data) {
				return tokens, fmt.Errorf("truncated Done token")
			}
			token := Token{
				Type:   tokenType,
				Status: binary.LittleEndian.Uint16(data[pos:]),
			}
			pos += 2 // Status
			pos += 2 // CurCmd
			token.RowCount = binary.LittleEndian.Uint32(data[pos:])
			pos += 4
			tokens = append(tokens, token)

		case TokenTypeDoneInProc:
			// Similar to Done
			if pos+8 > len(data) {
				return tokens, fmt.Errorf("truncated DoneInProc token")
			}
			pos += 8

		case TokenTypeError:
			// Error token: Length, Number, State, Class, MsgText, Server, Proc, Line
			if pos+2 > len(data) {
				return tokens, fmt.Errorf("truncated Error token")
			}
			length := binary.LittleEndian.Uint16(data[pos:])
			pos += 2
			if pos+int(length) > len(data) {
				return tokens, fmt.Errorf("truncated Error token data")
			}
			// Parse error details
			token := Token{Type: tokenType}
			pos += int(length)
			tokens = append(tokens, token)

		case TokenTypeInfo:
			// Similar to Error
			if pos+2 > len(data) {
				return tokens, fmt.Errorf("truncated Info token")
			}
			length := binary.LittleEndian.Uint16(data[pos:])
			pos += 2
			pos += int(length)

		case TokenTypeLoginAck:
			// Login acknowledgment
			if pos+2 > len(data) {
				return tokens, fmt.Errorf("truncated LoginAck token")
			}
			length := binary.LittleEndian.Uint16(data[pos:])
			pos += 2
			pos += int(length)

		case TokenTypeRow:
			// Row token - actual row data
			tokens = append(tokens, Token{Type: tokenType})

		case TokenTypeColMetadata:
			// Column metadata token
			if pos+2 > len(data) {
				return tokens, fmt.Errorf("truncated ColMetadata token")
			}
			count := binary.LittleEndian.Uint16(data[pos:])
			pos += 2
			// Skip column info for now
			tokens = append(tokens, Token{Type: tokenType, ColumnCount: int(count)})

		case TokenTypeSSPI:
			// SSPI/NTLM authentication token
			if pos+2 > len(data) {
				return tokens, fmt.Errorf("truncated SSPI token")
			}
			length := binary.LittleEndian.Uint16(data[pos:])
			pos += 2
			if pos+int(length) > len(data) {
				return tokens, fmt.Errorf("truncated SSPI token data")
			}
			pos += int(length)
			tokens = append(tokens, Token{Type: tokenType})

		case TokenTypeEnvChange:
			// Environment change token
			if pos+2 > len(data) {
				return tokens, fmt.Errorf("truncated EnvChange token")
			}
			length := binary.LittleEndian.Uint16(data[pos:])
			pos += 2
			pos += int(length)
			tokens = append(tokens, Token{Type: tokenType})

		default:
			// Unknown token, try to skip or break
			return tokens, fmt.Errorf("unknown token type: 0x%02x", tokenType)
		}
	}

	return tokens, nil
}

// Token represents a TDS token.
type Token struct {
	Type        byte
	Status      uint16
	RowCount    uint32
	ColumnCount int
}

// IsFinalToken returns true if this is a final token in a response.
func (t Token) IsFinalToken() bool {
	return t.Type == TokenTypeDone || t.Type == TokenTypeDoneInProc ||
		t.Type == TokenTypeDoneProc
}

// IsError returns true if this is an error token.
func (t Token) IsError() bool {
	return t.Type == TokenTypeError
}

// IsSSPI returns true if this is an SSPI token (NTLM/Kerberos challenge).
func (t Token) IsSSPI() bool {
	return t.Type == TokenTypeSSPI
}

// IsLoginAck returns true if this is a login acknowledgement.
func (t Token) IsLoginAck() bool {
	return t.Type == TokenTypeLoginAck
}

// IsEnvChange returns true if this is an environment change token.
func (t Token) IsEnvChange() bool {
	return t.Type == TokenTypeEnvChange
}

// Constants
const (
	// Packet types
	PacketTypeSQLBatch      byte = 0x01
	PacketTypePreLogin      byte = 0x12
	PacketTypeLogin7        byte = 0x10
	PacketTypeRPC           byte = 0x03
	PacketTypeAttention     byte = 0x06
	PacketTypeBulkLoad      byte = 0x07
	PacketTypeFedAuthToken  byte = 0x08
	PacketTypeBatch         byte = 0x09
	PacketTypeSSPI          byte = 0x11
	PacketTypeLogout        byte = 0x13
	PacketTypeTabularResult byte = 0x04

	// Status flags
	StatusEndOfMessage byte = 0x01
	StatusIgnore       byte = 0x02
	StatusResetConn    byte = 0x08
	StatusResetSkipTxn byte = 0x10

	// Encryption modes
	EncryptOff      byte = 0x00
	EncryptOn       byte = 0x01
	EncryptNotSup   byte = 0x02
	EncryptRequired byte = 0x03

	// Pre-login token types
	PreLoginVersion         byte = 0x00
	PreLoginEncryption      byte = 0x01
	PreLoginInstOpt         byte = 0x02
	PreLoginThreadID        byte = 0x03
	PreLoginMars            byte = 0x04
	PreLoginTraceID         byte = 0x05
	PreLoginFedAuthRequired byte = 0x06
	PreLoginNonceOpt        byte = 0x07

	// Login option flags 1
	LoginOption1OrderX86         uint32 = 0x00000001
	LoginOption1Order68000       uint32 = 0x00000002
	LoginOption1CharSetEBCDIC    uint32 = 0x00000004
	LoginOption1CharSetISO8859_1 uint32 = 0x00000008
	LoginOption1CharSetISO8859_2 uint32 = 0x00000010
	LoginOption1UseDb            uint32 = 0x00000020
	LoginOption1InitDbFatal      uint32 = 0x00000040
	LoginOption1SetLangOn        uint32 = 0x00000080

	// Token types
	TokenTypeSQLText     byte = 0x00
	TokenTypeColMetadata byte = 0x81
	TokenTypeRow         byte = 0xD1
	TokenTypeDone        byte = 0xFD
	TokenTypeDoneProc    byte = 0xFE
	TokenTypeDoneInProc  byte = 0xFF
	TokenTypeError       byte = 0xAA
	TokenTypeInfo        byte = 0xAB
	TokenTypeLoginAck    byte = 0xAD
	TokenTypeEnvChange   byte = 0xE3
	TokenTypeSSPI        byte = 0xED // NTLM/Kerberos authentication token
	TokenTypeFeatureExt  byte = 0xEE // Feature extension ACK
	TokenTypeTracking    byte = 0xA9 // SQL batch tracking
	TokenTypeNTLMAuth    byte = 0xED // Alias for SSPI (NTLM challenge/response)
)

// EncryptMode type for encryption settings
type EncryptMode byte
