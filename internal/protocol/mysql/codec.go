// Package mysql implements the MySQL Client/Server wire protocol codec
// (handshake v10). It handles capability negotiation, authentication
// (mysql_native_password, caching_sha2_password), COM_QUERY, prepared
// statement binary protocol, and COM_CHANGE_USER.
package mysql

import (
	"bufio"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

// MySQLCodec implements the MySQL Client/Server Protocol.
type MySQLCodec struct {
	capabilityFlags uint32
}

// NewCodec creates a new MySQL codec.
func NewCodec() *MySQLCodec {
	return &MySQLCodec{
		capabilityFlags: ClientProtocol41 | ClientSecureConnection | ClientLongPassword |
			ClientTransactions | ClientLongFlag | ClientConnectWithDB,
	}
}

// Protocol returns the protocol identifier.
func (c *MySQLCodec) Protocol() common.Protocol {
	return common.ProtocolMySQL
}

// ReadMessage reads one complete packet from the connection.
func (c *MySQLCodec) ReadMessage(r io.Reader) (*common.Message, error) {
	// MySQL packet format: 3 bytes length + 1 byte sequence + payload
	reader := bufio.NewReader(r)

	// Read header (4 bytes)
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}

	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	_ = header[3] // sequence number, not used in message processing

	if length > MaxPayloadLen {
		return nil, fmt.Errorf("packet too large: %d", length)
	}

	// Read payload
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, err
		}
	}

	// Construct raw message (include header)
	raw := make([]byte, 4+length)
	copy(raw[0:4], header)
	copy(raw[4:], payload)

	// Determine message type from first byte of payload (if available)
	msgType := byte(0)
	if length > 0 {
		msgType = payload[0]
	}

	return &common.Message{
		Type:      msgType,
		Length:    int32(length),
		Payload:   payload,
		Raw:       raw,
		Direction: common.Frontend,
	}, nil
}

// WriteMessage writes one complete packet to the connection.
func (c *MySQLCodec) WriteMessage(w io.Writer, msg *common.Message) error {
	_, err := w.Write(msg.Raw)
	return err
}

// IsStartup returns true if this is a startup/handshake message.
func (c *MySQLCodec) IsStartup(msg *common.Message) bool {
	// SSL Request packet has capability flags with ClientSSL set
	if len(msg.Payload) < 4 {
		return false
	}
	// Check for SSL request pattern
	return msg.Payload[0] == 0x20 // SSL request packet starts with capability flags
}

// IsTerminate returns true if this is a termination message.
func (c *MySQLCodec) IsTerminate(msg *common.Message) bool {
	return msg.Type == ComQuit
}

// IsQuery returns true if this is a query message.
func (c *MySQLCodec) IsQuery(msg *common.Message) bool {
	return msg.Type == ComQuery
}

// IsTransactionBegin returns true if message starts a transaction.
func (c *MySQLCodec) IsTransactionBegin(msg *common.Message) bool {
	if msg.Type != ComQuery {
		return false
	}
	query := c.extractQuery(msg)
	upperQuery := strings.ToUpper(strings.TrimSpace(query))
	return strings.HasPrefix(upperQuery, "BEGIN") ||
		strings.HasPrefix(upperQuery, "START TRANSACTION")
}

// IsTransactionEnd returns true if message ends a transaction.
func (c *MySQLCodec) IsTransactionEnd(msg *common.Message) bool {
	if msg.Type != ComQuery {
		return false
	}
	query := c.extractQuery(msg)
	upperQuery := strings.ToUpper(strings.TrimSpace(query))
	return strings.HasPrefix(upperQuery, "COMMIT") ||
		strings.HasPrefix(upperQuery, "ROLLBACK")
}

// IsPrepare returns true if this is a prepare statement message.
func (c *MySQLCodec) IsPrepare(msg *common.Message) bool {
	return msg.Type == ComStmtPrepare
}

// IsBind returns true if this is a bind message (COM_STMT_SEND_LONG_DATA in MySQL).
func (c *MySQLCodec) IsBind(msg *common.Message) bool {
	// MySQL uses COM_STMT_SEND_LONG_DATA for parameter binding
	return msg.Type == ComStmtSendLongData
}

// IsExecute returns true if this is an execute prepared stmt message.
func (c *MySQLCodec) IsExecute(msg *common.Message) bool {
	return msg.Type == ComStmtExecute
}

// IsClose returns true if this is a close statement message.
func (c *MySQLCodec) IsClose(msg *common.Message) bool {
	return msg.Type == ComStmtClose
}

// IsSync returns true if this is a sync/reset message.
func (c *MySQLCodec) IsSync(msg *common.Message) bool {
	// MySQL doesn't have a direct equivalent to PostgreSQL's Sync
	// COM_STMT_RESET is the closest equivalent
	return msg.Type == ComStmtReset
}

// ExtractQuery extracts the SQL query string from a query message.
func (c *MySQLCodec) ExtractQuery(msg *common.Message) (string, error) {
	if msg.Type == ComQuery {
		return c.extractQuery(msg), nil
	}
	if msg.Type == ComStmtPrepare {
		return c.extractPrepareQuery(msg), nil
	}
	return "", fmt.Errorf("message type 0x%02x does not contain a query", msg.Type)
}

func (c *MySQLCodec) extractQuery(msg *common.Message) string {
	// COM_QUERY: command (1 byte) + query string (null-terminated or rest of packet)
	if len(msg.Payload) < 2 {
		return ""
	}
	// Skip command byte
	query := msg.Payload[1:]
	// Remove null terminator if present
	if len(query) > 0 && query[len(query)-1] == 0 {
		query = query[:len(query)-1]
	}
	return string(query)
}

func (c *MySQLCodec) extractPrepareQuery(msg *common.Message) string {
	// COM_STMT_PREPARE: command (1 byte) + query string
	if len(msg.Payload) < 2 {
		return ""
	}
	return string(msg.Payload[1:])
}

// GenerateResetSequence returns messages to reset server state.
func (c *MySQLCodec) GenerateResetSequence() []*common.Message {
	// MySQL: COM_RESET_CONNECTION or COM_CHANGE_USER
	return []*common.Message{c.createResetConnectionMessage()}
}

func (c *MySQLCodec) createResetConnectionMessage() *common.Message {
	// COM_RESET_CONNECTION packet
	payload := []byte{ComResetConnection}
	return c.createPacket(0, payload)
}

func (c *MySQLCodec) createPacket(seqNum byte, payload []byte) *common.Message {
	length := len(payload)
	raw := make([]byte, 4+length)
	raw[0] = byte(length)
	raw[1] = byte(length >> 8)
	raw[2] = byte(length >> 16)
	raw[3] = seqNum
	copy(raw[4:], payload)

	return &common.Message{
		Type:    payload[0],
		Length:  int32(length),
		Payload: payload,
		Raw:     raw,
	}
}

// CreateHandshakeV10 creates an initial handshake packet (server -> client).
func CreateHandshakeV10(serverVersion string, connID uint32, authData []byte, capabilityFlags uint32) []byte {
	versionBytes := []byte(serverVersion)
	buf := make([]byte, 0, 128)

	// Protocol version (1 byte)
	buf = append(buf, 10)

	// Server version (null-terminated string)
	buf = append(buf, versionBytes...)
	buf = append(buf, 0)

	// Connection ID (4 bytes)
	buf = binary.LittleEndian.AppendUint32(buf, connID)

	// Auth plugin data part 1 (8 bytes)
	if len(authData) >= 8 {
		buf = append(buf, authData[:8]...)
	} else {
		buf = append(buf, make([]byte, 8)...)
	}

	// Filler (1 byte)
	buf = append(buf, 0)

	// Capability flags lower 2 bytes
	buf = binary.LittleEndian.AppendUint16(buf, uint16(capabilityFlags))

	// Character set (1 byte) - utf8mb4 = 255
	buf = append(buf, 255)

	// Status flags (2 bytes)
	buf = binary.LittleEndian.AppendUint16(buf, ServerStatusAutocommit)

	// Capability flags upper 2 bytes
	buf = binary.LittleEndian.AppendUint16(buf, uint16(capabilityFlags>>16))

	// Auth plugin data length (1 byte) - at least 21 bytes total
	buf = append(buf, 21)

	// Reserved (10 bytes)
	buf = append(buf, make([]byte, 10)...)

	// Auth plugin data part 2 (12 bytes) + null terminator
	if len(authData) >= 20 {
		buf = append(buf, authData[8:20]...)
	} else {
		buf = append(buf, make([]byte, 12)...)
	}
	buf = append(buf, 0)

	// Auth plugin name (null-terminated)
	buf = append(buf, []byte("mysql_native_password")...)
	buf = append(buf, 0)

	return buf
}

// ParseHandshakeResponse parses a handshake response packet.
func ParseHandshakeResponse(data []byte) (*HandshakeResponse, error) {
	if len(data) < 32 {
		return nil, fmt.Errorf("handshake response too short")
	}

	resp := &HandshakeResponse{}
	pos := 0

	// Capability flags (4 bytes)
	resp.CapabilityFlags = binary.LittleEndian.Uint32(data[pos:])
	pos += 4

	// Max packet size (4 bytes)
	resp.MaxPacketSize = binary.LittleEndian.Uint32(data[pos:])
	pos += 4

	// Character set (1 byte)
	resp.CharacterSet = data[pos]
	pos++

	// Reserved (23 bytes)
	pos += 23

	// Username (null-terminated)
	usernameStart := pos
	for pos < len(data) && data[pos] != 0 {
		pos++
	}
	resp.Username = string(data[usernameStart:pos])
	pos++ // skip null

	// Auth response (length-encoded or null-terminated depending on capabilities)
	if resp.CapabilityFlags&ClientPluginAuthLenencClientData != 0 {
		// Length-encoded string
		authLen, _, n := readLengthEncodedInt(data[pos:])
		pos += n
		resp.AuthResponse = data[pos : pos+int(authLen)]
		pos += int(authLen)
	} else if resp.CapabilityFlags&ClientSecureConnection != 0 {
		// 1 byte length + auth data
		authLen := int(data[pos])
		pos++
		resp.AuthResponse = data[pos : pos+authLen]
		pos += authLen
	} else {
		// Null-terminated
		authStart := pos
		for pos < len(data) && data[pos] != 0 {
			pos++
		}
		resp.AuthResponse = data[authStart:pos]
		pos++ // skip null
	}

	// Database (if ClientConnectWithDB)
	if resp.CapabilityFlags&ClientConnectWithDB != 0 {
		dbStart := pos
		for pos < len(data) && data[pos] != 0 {
			pos++
		}
		resp.Database = string(data[dbStart:pos])
		pos++ // skip null
	}

	// Auth plugin name (if ClientPluginAuth)
	if resp.CapabilityFlags&ClientPluginAuth != 0 && pos < len(data) {
		pluginStart := pos
		for pos < len(data) && data[pos] != 0 {
			pos++
		}
		resp.AuthPluginName = string(data[pluginStart:pos])
	}

	return resp, nil
}

// HandshakeResponse represents a client handshake response.
type HandshakeResponse struct {
	CapabilityFlags uint32
	MaxPacketSize   uint32
	CharacterSet    byte
	Username        string
	AuthResponse    []byte
	Database        string
	AuthPluginName  string
}

// CreateAuthSwitchRequest creates an auth switch request packet.
func CreateAuthSwitchRequest(pluginName string, authData []byte) []byte {
	buf := []byte{0xfe} // AuthSwitchRequest status byte
	buf = append(buf, []byte(pluginName)...)
	buf = append(buf, 0)
	buf = append(buf, authData...)
	return buf
}

// CreateOKPacket creates an OK packet.
func CreateOKPacket(affectedRows, lastInsertID uint64, statusFlags uint16) []byte {
	buf := []byte{0x00} // OK header

	// Affected rows (length-encoded)
	buf = appendLengthEncodedInt(buf, affectedRows)

	// Last insert ID (length-encoded)
	buf = appendLengthEncodedInt(buf, lastInsertID)

	// Status flags (2 bytes)
	buf = binary.LittleEndian.AppendUint16(buf, statusFlags)

	// Warnings (2 bytes)
	buf = binary.LittleEndian.AppendUint16(buf, 0)

	return buf
}

// CreateERRPacket creates an ERROR packet.
func CreateERRPacket(errorCode uint16, sqlState, errorMessage string) []byte {
	buf := []byte{0xff} // ERR header

	// Error code (2 bytes)
	buf = binary.LittleEndian.AppendUint16(buf, errorCode)

	// SQL state marker (#)
	buf = append(buf, '#')

	// SQL state (5 bytes)
	buf = append(buf, []byte(sqlState)...)

	// Error message
	buf = append(buf, []byte(errorMessage)...)

	return buf
}

// mysql_native_password authentication
func scramblePassword(password string, scramble []byte) []byte {
	if password == "" {
		return nil
	}

	// SHA1(password)
	hash1 := sha1.Sum([]byte(password))

	// SHA1(SHA1(password))
	hash2 := sha1.Sum(hash1[:])

	// SHA1(scramble + SHA1(SHA1(password)))
	h := sha1.New()
	h.Write(scramble)
	h.Write(hash2[:])
	hash3 := h.Sum(nil)

	// XOR hash1 with hash3
	result := make([]byte, 20)
	for i := range result {
		result[i] = hash1[i] ^ hash3[i]
	}

	return result
}

// caching_sha2_password authentication
func scrambleCachingSHA2Password(password string, scramble []byte) []byte {
	if password == "" {
		return nil
	}

	// SHA256(password)
	hash1 := sha256.Sum256([]byte(password))

	// SHA256(SHA256(password))
	hash2 := sha256.Sum256(hash1[:])

	// SHA256(SHA256(SHA256(password)) + scramble)
	h := sha256.New()
	h.Write(hash2[:])
	h.Write(scramble)
	hash3 := h.Sum(nil)

	// XOR hash1 with hash3
	result := make([]byte, 32)
	for i := range result {
		result[i] = hash1[i] ^ hash3[i]
	}

	return result
}

// Helper functions
func readLengthEncodedInt(data []byte) (uint64, bool, int) {
	if len(data) == 0 {
		return 0, false, 0
	}

	switch data[0] {
	case 0xfb:
		return 0, true, 1 // NULL
	case 0xfc:
		return uint64(data[1]) | uint64(data[2])<<8, false, 3
	case 0xfd:
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16, false, 4
	case 0xfe:
		return binary.LittleEndian.Uint64(data[1:9]), false, 9
	default:
		return uint64(data[0]), false, 1
	}
}

func appendLengthEncodedInt(buf []byte, n uint64) []byte {
	switch {
	case n < 251:
		return append(buf, byte(n))
	case n < 1<<16:
		return append(buf, 0xfc, byte(n), byte(n>>8))
	case n < 1<<24:
		return append(buf, 0xfd, byte(n), byte(n>>8), byte(n>>16))
	default:
		return append(buf, 0xfe, byte(n), byte(n>>8), byte(n>>16), byte(n>>24),
			byte(n>>32), byte(n>>40), byte(n>>48), byte(n>>56))
	}
}

// Constants
const (
	MaxPayloadLen = 1<<24 - 1 // 16MB - 1

	// Capability flags
	ClientLongPassword               uint32 = 1 << 0
	ClientFoundRows                  uint32 = 1 << 1
	ClientLongFlag                   uint32 = 1 << 2
	ClientConnectWithDB              uint32 = 1 << 3
	ClientNoSchema                   uint32 = 1 << 4
	ClientCompress                   uint32 = 1 << 5
	ClientODBC                       uint32 = 1 << 6
	ClientLocalFiles                 uint32 = 1 << 7
	ClientIgnoreSpace                uint32 = 1 << 8
	ClientProtocol41                 uint32 = 1 << 9
	ClientInteractive                uint32 = 1 << 10
	ClientSSL                        uint32 = 1 << 11
	ClientIgnoreSIGPIPE              uint32 = 1 << 12
	ClientTransactions               uint32 = 1 << 13
	ClientReserved                   uint32 = 1 << 14
	ClientSecureConnection           uint32 = 1 << 15
	ClientMultiStatements            uint32 = 1 << 16
	ClientMultiResults               uint32 = 1 << 17
	ClientPSMultiResults             uint32 = 1 << 18
	ClientPluginAuth                 uint32 = 1 << 19
	ClientConnectAttrs               uint32 = 1 << 20
	ClientPluginAuthLenencClientData uint32 = 1 << 21
	ClientCanHandleExpiredPasswords  uint32 = 1 << 22
	ClientSessionTrack               uint32 = 1 << 23
	ClientDeprecateEOF               uint32 = 1 << 24
	ClientOptionalResultsetMetadata  uint32 = 1 << 25
	ClientZstdCompressionEnabled     uint32 = 1 << 26
	ClientQueryAttributes            uint32 = 1 << 27
	MultiFactorAuthentication        uint32 = 1 << 28
	ClientCapabilityExtension        uint32 = 1 << 29
	ClientSSLVerifyServerCert        uint32 = 1 << 30
	ClientRememberOptions            uint32 = 1 << 31

	// Server status flags
	ServerStatusInTransaction      uint16 = 1 << 0
	ServerStatusAutocommit         uint16 = 1 << 1
	ServerMoreResultsExists        uint16 = 1 << 3
	ServerStatusNoGoodIndexUsed    uint16 = 1 << 4
	ServerStatusNoIndexUsed        uint16 = 1 << 5
	ServerStatusCursorExists       uint16 = 1 << 6
	ServerStatusLastRowSent        uint16 = 1 << 7
	ServerStatusDBDropped          uint16 = 1 << 8
	ServerStatusNoBackslashEscapes uint16 = 1 << 9
	ServerStatusMetadataChanged    uint16 = 1 << 10
	ServerQueryWasSlow             uint16 = 1 << 11
	ServerPSOutParams              uint16 = 1 << 12
	ServerStatusInTransReadonly    uint16 = 1 << 13
	ServerSessionStateChanged      uint16 = 1 << 14

	// Command packets
	ComSleep            byte = 0x00
	ComQuit             byte = 0x01
	ComInitDB           byte = 0x02
	ComQuery            byte = 0x03
	ComFieldList        byte = 0x04
	ComCreateDB         byte = 0x05
	ComDropDB           byte = 0x06
	ComRefresh          byte = 0x07
	ComShutdown         byte = 0x08
	ComStatistics       byte = 0x09
	ComProcessInfo      byte = 0x0a
	ComConnect          byte = 0x0b
	ComProcessKill      byte = 0x0c
	ComDebug            byte = 0x0d
	ComPing             byte = 0x0e
	ComTime             byte = 0x0f
	ComDelayedInsert    byte = 0x10
	ComChangeUser       byte = 0x11
	ComBinlogDump       byte = 0x12
	ComTableDump        byte = 0x13
	ComConnectOut       byte = 0x14
	ComRegisterSlave    byte = 0x15
	ComStmtPrepare      byte = 0x16
	ComStmtExecute      byte = 0x17
	ComStmtSendLongData byte = 0x18
	ComStmtClose        byte = 0x19
	ComStmtReset        byte = 0x1a
	ComSetOption        byte = 0x1b
	ComStmtFetch        byte = 0x1c
	ComDaemon           byte = 0x1d
	ComBinlogDumpGtid   byte = 0x1e
	ComResetConnection  byte = 0x1f
	ComClone            byte = 0x20
)
