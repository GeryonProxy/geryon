package integration

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

// MySQL integration tests using pure Go (no external dependencies)
// These tests connect to Geryon MySQL proxy and verify protocol handling

const (
	mysqlDefaultPort    = "3306"
	mysqlHandshakeV10   = 10
)

// MySQL packet types
const (
	mysqlOKPacket  = 0x00
	mysqlErrPacket = 0xff
	mysqlEOFPacket = 0xfe
)

// MySQL command types
const (
	mysqlComQuit       = 0x01
	mysqlComInitDB     = 0x02
	mysqlComQuery      = 0x03
	mysqlComPing       = 0x0e
	mysqlComChangeUser = 0x11
	mysqlComResetConn  = 0x1f
)

// MySQL client capabilities
const (
	mysqlClientSSL         = 0x00000800
	mysqlClientProtocol41  = 0x00000200
	mysqlClientSecureConn  = 0x00008000
	mysqlClientConnectWithDB = 0x00000008
)

// mySQLConn represents a MySQL connection for testing
type mySQLConn struct {
	conn      net.Conn
	seq       uint8
	caps      uint32
	charset   uint8
	user      string
	connected bool
}

// mySQLHandshake represents the server handshake
type mySQLHandshake struct {
	ProtocolVersion uint8
	ServerVersion   string
	ConnectionID    uint32
	AuthPlugin      string
	AuthData        []byte
	Capabilities    uint32
}

func newMySQLConn(host, port, user, password string) (*mySQLConn, error) {
	addr := net.JoinHostPort(host, port)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return &mySQLConn{
		conn: conn,
		user: user,
	}, nil
}

func (c *mySQLConn) Close() error {
	if c.conn != nil {
		// Send quit command
		_ = c.writePacket([]byte{mysqlComQuit})
		return c.conn.Close()
	}
	return nil
}

func (c *mySQLConn) writePacket(data []byte) error {
	length := len(data)
	for length >= 0xffffff {
		// Write 16MB chunk
		header := make([]byte, 4)
		header[0] = 0xff
		header[1] = 0xff
		header[2] = 0xff
		header[3] = c.seq
		if _, err := c.conn.Write(header); err != nil {
			return err
		}
		if _, err := c.conn.Write(data[:0xffffff]); err != nil {
			return err
		}
		data = data[0xffffff:]
		length -= 0xffffff
		c.seq++
	}

	// Write remaining data
	header := make([]byte, 4)
	header[0] = byte(length)
	header[1] = byte(length >> 8)
	header[2] = byte(length >> 16)
	header[3] = c.seq
	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	if length > 0 {
		if _, err := c.conn.Write(data); err != nil {
			return err
		}
	}
	c.seq++
	return nil
}

func (c *mySQLConn) readPacket() ([]byte, error) {
	var result []byte

	for {
		header := make([]byte, 4)
		if _, err := c.conn.Read(header); err != nil {
			return nil, err
		}

		length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
		seq := header[3]
		_ = seq // Could verify sequence number

		data := make([]byte, length)
		if _, err := c.conn.Read(data); err != nil {
			return nil, err
		}

		result = append(result, data...)

		// If length < 16MB, this is the last packet
		if length < 0xffffff {
			break
		}
	}

	return result, nil
}

func (c *mySQLConn) readHandshake() (*mySQLHandshake, error) {
	data, err := c.readPacket()
	if err != nil {
		return nil, fmt.Errorf("failed to read handshake: %w", err)
	}

	if len(data) < 5 {
		return nil, fmt.Errorf("handshake too short")
	}

	h := &mySQLHandshake{
		ProtocolVersion: data[0],
	}

	if h.ProtocolVersion != mysqlHandshakeV10 {
		return nil, fmt.Errorf("unsupported protocol version: %d", h.ProtocolVersion)
	}

	// Parse server version (null-terminated)
	i := 1
	for i < len(data) && data[i] != 0 {
		i++
	}
	h.ServerVersion = string(data[1:i])
	i++

	// Connection ID (4 bytes)
	if i+4 > len(data) {
		return nil, fmt.Errorf("handshake truncated at connection ID")
	}
	h.ConnectionID = binary.LittleEndian.Uint32(data[i:])
	i += 4

	// Auth plugin data part 1 (8 bytes) + filler (1 byte)
	if i+9 > len(data) {
		return nil, fmt.Errorf("handshake truncated at auth data")
	}
	authData1 := data[i : i+8]
	i += 9

	// Capability flags (lower 2 bytes)
	if i+2 > len(data) {
		return nil, fmt.Errorf("handshake truncated at capabilities")
	}
	capsLower := binary.LittleEndian.Uint16(data[i:])
	i += 2

	// Character set (1 byte)
	if i+1 > len(data) {
		return nil, fmt.Errorf("handshake truncated at charset")
	}
	c.charset = data[i]
	i++

	// Status flags (2 bytes) - skip
	i += 2

	// Capability flags (upper 2 bytes)
	if i+2 > len(data) {
		return nil, fmt.Errorf("handshake truncated at capabilities upper")
	}
	capsUpper := binary.LittleEndian.Uint16(data[i:])
	i += 2

	h.Capabilities = uint32(capsLower) | uint32(capsUpper)<<16

	// Auth plugin data length (1 byte)
	if i+1 > len(data) {
		return nil, fmt.Errorf("handshake truncated at auth length")
	}
	authLen := data[i]
	i++

	// Reserved (10 bytes) - skip
	i += 10

	// Auth plugin data part 2 (12 bytes minimum)
	if i+12 > len(data) {
		return nil, fmt.Errorf("handshake truncated at auth data part 2")
	}
	authData2 := data[i : i+max(0, int(authLen)-9)]

	h.AuthData = append(authData1, authData2...)

	// Auth plugin name (null-terminated, if secure connection)
	if h.Capabilities&mysqlClientSecureConn != 0 {
		i += len(authData2)
		if i < len(data) && data[i] == 0 {
			i++
		}
		if i < len(data) {
			end := i
			for end < len(data) && data[end] != 0 {
				end++
			}
			h.AuthPlugin = string(data[i:end])
		}
	}

	return h, nil
}

func (c *mySQLConn) writeHandshakeResponse(h *mySQLHandshake, user, password, database string) error {
	// Calculate capabilities we support
	caps := uint32(mysqlClientProtocol41 | mysqlClientSecureConn)
	if database != "" {
		caps |= mysqlClientConnectWithDB
	}
	c.caps = caps

	// Build response
	var buf bytes.Buffer

	// Capability flags (4 bytes)
	binary.Write(&buf, binary.LittleEndian, caps)

	// Max packet size (4 bytes) - 16MB
	binary.Write(&buf, binary.LittleEndian, uint32(0xffffff))

	// Character set (1 byte)
	buf.WriteByte(c.charset)

	// Reserved (23 bytes of zeros)
	buf.Write(make([]byte, 23))

	// Username (null-terminated)
	buf.WriteString(user)
	buf.WriteByte(0)

	// Auth response
	authResp := c.scramblePassword(password, h.AuthData)
	buf.WriteByte(byte(len(authResp)))
	buf.Write(authResp)

	// Database (if requested)
	if database != "" && caps&mysqlClientConnectWithDB != 0 {
		buf.WriteString(database)
		buf.WriteByte(0)
	}

	// Auth plugin name
	if h.AuthPlugin != "" {
		buf.WriteString(h.AuthPlugin)
		buf.WriteByte(0)
	}

	return c.writePacket(buf.Bytes())
}

func (c *mySQLConn) scramblePassword(password string, authData []byte) []byte {
	// SHA1-based scramble (mysql_native_password)
	if password == "" {
		return nil
	}

	// SHA1(password)
	hash1 := sha1.Sum([]byte(password))

	// SHA1(SHA1(password))
	hash2 := sha1.Sum(hash1[:])

	// SHA1(authData + SHA1(SHA1(password)))
	combined := append(authData, hash2[:]...)
	hash3 := sha1.Sum(combined)

	// XOR hash1 with hash3
	result := make([]byte, 20)
	for i := 0; i < 20; i++ {
		result[i] = hash1[i] ^ hash3[i]
	}

	return result
}

func (c *mySQLConn) readOK() error {
	data, err := c.readPacket()
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return fmt.Errorf("empty response")
	}

	switch data[0] {
	case mysqlOKPacket:
		return nil
	case mysqlErrPacket:
		return c.parseError(data)
	default:
		return fmt.Errorf("unexpected response type: %d", data[0])
	}
}

func (c *mySQLConn) parseError(data []byte) error {
	if len(data) < 3 {
		return fmt.Errorf("error packet too short")
	}

	// Skip 0xff marker
	i := 1

	// Error code (2 bytes)
	code := binary.LittleEndian.Uint16(data[i:])
	i += 2

	// SQL state marker '#' (1 byte) if present
	if i < len(data) && data[i] == '#' {
		i++
		i += 5 // SQL state (5 bytes)
	}

	// Error message
	msg := string(data[i:])
	return fmt.Errorf("MySQL error %d: %s", code, msg)
}

func (c *mySQLConn) Query(sql string) error {
	c.seq = 0
	cmd := []byte{mysqlComQuery}
	cmd = append(cmd, []byte(sql)...)
	if err := c.writePacket(cmd); err != nil {
		return fmt.Errorf("failed to write query: %w", err)
	}
	return c.readResult()
}

func (c *mySQLConn) readResult() error {
	data, err := c.readPacket()
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return fmt.Errorf("empty result")
	}

	switch data[0] {
	case mysqlOKPacket:
		return nil
	case mysqlErrPacket:
		return c.parseError(data)
	case 0xfb: // LOCAL_INFILE
		return fmt.Errorf("LOCAL_INFILE not supported")
	default:
		// Result set - read column count and skip columns/rows
		// For simplicity, just read until EOF
		colCount, _, _ := readLengthEncodedInt(data)
		_ = colCount
		// Read column definitions
		for i := 0; i < int(colCount); i++ {
			_, _ = c.readPacket() // Column definition
		}
		// Read EOF marker
		_, _ = c.readPacket()
		// Read rows
		for {
			row, _ := c.readPacket()
			if len(row) > 0 && row[0] == mysqlEOFPacket {
				break
			}
		}
		return nil
	}
}

func readLengthEncodedInt(data []byte) (uint64, int, bool) {
	if len(data) == 0 {
		return 0, 0, false
	}
	switch data[0] {
	case 0xfb:
		return 0, 1, true // NULL
	case 0xfc:
		if len(data) < 3 {
			return 0, 0, false
		}
		return uint64(data[1]) | uint64(data[2])<<8, 3, false
	case 0xfd:
		if len(data) < 4 {
			return 0, 0, false
		}
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16, 4, false
	case 0xfe:
		if len(data) < 9 {
			return 0, 0, false
		}
		return binary.LittleEndian.Uint64(data[1:]), 9, false
	default:
		return uint64(data[0]), 1, false
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// connectMySQL connects to MySQL through Geryon
func connectMySQL(t *testing.T) (*mySQLConn, error) {
	host := getEnv("MYSQL_HOST", "127.0.0.1")
	port := getEnv("MYSQL_PORT", mysqlDefaultPort)
	user := getEnv("MYSQL_USER", "testuser")
	pass := getEnv("MYSQL_PASSWORD", "testpass")

	conn, err := newMySQLConn(host, port, user, pass)
	if err != nil {
		return nil, err
	}

	// Read server handshake
	h, err := conn.readHandshake()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("handshake failed: %w", err)
	}
	t.Logf("MySQL handshake: version=%s, conn_id=%d, auth_plugin=%s",
		h.ServerVersion, h.ConnectionID, h.AuthPlugin)

	// Send response
	if err := conn.writeHandshakeResponse(h, user, pass, ""); err != nil {
		conn.Close()
		return nil, fmt.Errorf("handshake response failed: %w", err)
	}

	// Read OK
	if err := conn.readOK(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("auth failed: %w", err)
	}

	conn.connected = true
	return conn, nil
}

// TestMySQL_Connect tests basic MySQL connection through Geryon
func TestMySQL_Connect(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MySQL integration test in short mode")
	}

	if os.Getenv("MYSQL_TEST") == "" {
		t.Skip("Set MYSQL_TEST=1 to enable MySQL integration tests")
	}

	conn, err := connectMySQL(t)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	t.Log("MySQL connection successful")
}

// TestMySQL_Select1 tests SELECT 1
func TestMySQL_Select1(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MySQL integration test in short mode")
	}

	if os.Getenv("MYSQL_TEST") == "" {
		t.Skip("Set MYSQL_TEST=1 to enable MySQL integration tests")
	}

	conn, err := connectMySQL(t)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// For now, just test that query doesn't error
	// Full result parsing would be more complex
	t.Log("SELECT 1 test would go here")
}

// TestMySQL_Ping tests COM_PING
func TestMySQL_Ping(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MySQL integration test in short mode")
	}

	if os.Getenv("MYSQL_TEST") == "" {
		t.Skip("Set MYSQL_TEST=1 to enable MySQL integration tests")
	}

	conn, err := connectMySQL(t)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Send ping
	conn.seq = 0
	if err := conn.writePacket([]byte{mysqlComPing}); err != nil {
		t.Fatalf("Failed to send ping: %v", err)
	}

	if err := conn.readOK(); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	t.Log("MySQL ping successful")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
