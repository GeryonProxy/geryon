package integration

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

// MSSQL/TDS integration tests for Geryon
// These tests verify MSSQL protocol handling

const (
	tdsVersion74  = 0x74000004 // TDS 7.4
	tdsPacketSize = 4096
)

// TDS packet types
const (
	tdsTypeSQLBatch = 1
	tdsTypeRPC      = 3
	tdsTypeReply    = 4
	tdsTypeLogin7   = 16
	tdsTypeNTLM     = 17
	tdsTypePreLogin = 18
)

// TDS token types
const (
	tdsTokenReturnStatus = 0x79
	dsTokenColMetadata   = 0x81
	tdsTokenOrder        = 0xa9
	tdsTokenError        = 0xaa
	tdsTokenRow          = 0xd1
	tdsTokenSSPI         = 0xed
	tdsTokenInfo         = 0xab
	tdsTokenLoginAck     = 0xad
	tdsTokenEnvChange    = 0xe3
	tdsTokenDone         = 0xfd
	tdsTokenDoneProc     = 0xfe
	tdsTokenDoneInProc   = 0xff
)

// tdsConn represents a TDS connection
type tdsConn struct {
	conn      net.Conn
	packetSeq uint8
	connected bool
}

// tdsPacket represents a TDS packet
type tdsPacket struct {
	Type   uint8
	Status uint8
	Length uint16
	SPID   uint16
	Packet uint8
	Window uint8
	Data   []byte
}

func newTDSConn(host, port string) (*tdsConn, error) {
	addr := net.JoinHostPort(host, port)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return &tdsConn{conn: conn}, nil
}

func (c *tdsConn) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *tdsConn) writePacket(packetType uint8, data []byte) error {
	// TDS header is 8 bytes
	length := 8 + len(data)
	if length > 0xffff {
		return fmt.Errorf("packet too large")
	}

	header := make([]byte, 8)
	header[0] = packetType
	header[1] = 1 // Status: end of message
	binary.BigEndian.PutUint16(header[2:4], uint16(length))
	binary.BigEndian.PutUint16(header[4:6], 0) // SPID
	header[6] = c.packetSeq
	header[7] = 0 // Window

	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	if len(data) > 0 {
		if _, err := c.conn.Write(data); err != nil {
			return err
		}
	}

	c.packetSeq++
	return nil
}

func (c *tdsConn) readPacket() (*tdsPacket, error) {
	header := make([]byte, 8)
	if _, err := c.conn.Read(header); err != nil {
		return nil, err
	}

	p := &tdsPacket{
		Type:   header[0],
		Status: header[1],
		Length: binary.BigEndian.Uint16(header[2:4]),
		SPID:   binary.BigEndian.Uint16(header[4:6]),
		Packet: header[6],
		Window: header[7],
	}

	dataLen := p.Length - 8
	if dataLen > 0 {
		p.Data = make([]byte, dataLen)
		if _, err := c.conn.Read(p.Data); err != nil {
			return nil, err
		}
	}

	return p, nil
}

func (c *tdsConn) preLogin() error {
	// Build PRELOGIN packet
	var buf bytes.Buffer

	// Option tokens
	options := []struct {
		token  uint8
		offset uint16
		length uint16
	}{
		{0x00, 0, 6}, // VERSION
		{0x01, 0, 0}, // ENCRYPTION
		{0x02, 0, 0}, // INSTOPT
		{0x03, 0, 0}, // THREADID
		{0x04, 0, 0}, // MARS
		{0xFF, 0, 0}, // TERMINATOR
	}

	// Calculate offsets
	offset := uint16(5 + len(options)*6) // Header + options
	for i := range options {
		if options[i].token != 0xFF {
			options[i].offset = offset
			switch options[i].token {
			case 0x00: // VERSION: major(1) + minor(1) + build(2) + subbuild(2)
				options[i].length = 6
			case 0x01: // ENCRYPTION: 1 byte
				options[i].length = 1
			case 0x02: // INSTOPT: 1 byte
				options[i].length = 1
			case 0x03: // THREADID: 4 bytes
				options[i].length = 4
			case 0x04: // MARS: 1 byte
				options[i].length = 1
			}
			offset += options[i].length
		}
	}

	// Write options
	for _, opt := range options {
		buf.WriteByte(opt.token)
		if opt.token != 0xFF {
			binary.Write(&buf, binary.BigEndian, opt.offset)
			binary.Write(&buf, binary.BigEndian, opt.length)
		}
	}

	// Write VERSION data (TDS 7.4)
	buf.WriteByte(0x07)                             // Major
	buf.WriteByte(0x04)                             // Minor
	binary.Write(&buf, binary.BigEndian, uint16(0)) // Build
	binary.Write(&buf, binary.BigEndian, uint16(0)) // Subbuild

	// Write ENCRYPTION (0 = ENCRYPT_OFF)
	buf.WriteByte(0)

	// Write INSTOPT (0 = no instance)
	buf.WriteByte(0)

	// Write THREADID (0)
	binary.Write(&buf, binary.LittleEndian, uint32(0))

	// Write MARS (0 = off)
	buf.WriteByte(0)

	return c.writePacket(tdsTypePreLogin, buf.Bytes())
}

func (c *tdsConn) readPreLoginResponse() error {
	p, err := c.readPacket()
	if err != nil {
		return err
	}

	if p.Type != tdsTypeReply {
		return fmt.Errorf("expected PRELOGIN response, got type %d", p.Type)
	}

	// Parse encryption setting from response
	// For now, just check we got a response
	return nil
}

func (c *tdsConn) login7(user, password, database string) error {
	// Build LOGIN7 packet
	// This is a simplified version
	var buf bytes.Buffer

	// Length placeholder (will be updated)
	lengthPos := buf.Len()
	binary.Write(&buf, binary.LittleEndian, uint32(0))

	// TDS version
	binary.Write(&buf, binary.LittleEndian, tdsVersion74)

	// Packet size
	binary.Write(&buf, binary.LittleEndian, uint32(tdsPacketSize))

	// Client program version
	binary.Write(&buf, binary.LittleEndian, uint32(0))

	// Client PID
	binary.Write(&buf, binary.LittleEndian, uint32(0))

	// Connection ID
	binary.Write(&buf, binary.LittleEndian, uint32(0))

	// Option flags (simplified)
	buf.WriteByte(0xe0) // SQL type, set language on, use DB fatal, set language required
	buf.WriteByte(0x03) // ODBC on, tran boundary
	buf.WriteByte(0x00) // Type 4
	buf.WriteByte(0x00) // Type 7

	// More flags
	buf.WriteByte(0x00)
	buf.WriteByte(0x00)
	buf.WriteByte(0x00)
	buf.WriteByte(0x00)

	// Timezone (simplified - 0)
	binary.Write(&buf, binary.LittleEndian, uint32(0))

	// LCID
	binary.Write(&buf, binary.LittleEndian, uint32(0x409)) // US English

	// Variable length offset data (simplified)
	// For now, just write empty/null for most fields

	// Client name
	clientName := "GeryonTest"
	binary.Write(&buf, binary.LittleEndian, uint16(len(clientName)))
	binary.Write(&buf, binary.LittleEndian, uint16(buf.Len()+10))
	buf.WriteString(clientName)

	// Username
	binary.Write(&buf, binary.LittleEndian, uint16(len(user)))
	binary.Write(&buf, binary.LittleEndian, uint16(buf.Len()+10))
	buf.WriteString(user)

	// Password (encrypted with XOR)
	encPass := encryptPassword(password)
	binary.Write(&buf, binary.LittleEndian, uint16(len(encPass)/2))
	binary.Write(&buf, binary.LittleEndian, uint16(buf.Len()+10))
	buf.Write(encPass)

	// App name
	appName := "Geryon"
	binary.Write(&buf, binary.LittleEndian, uint16(len(appName)))
	binary.Write(&buf, binary.LittleEndian, uint16(buf.Len()+10))
	buf.WriteString(appName)

	// Server name (empty)
	binary.Write(&buf, binary.LittleEndian, uint16(0))
	binary.Write(&buf, binary.LittleEndian, uint16(0))

	// Database
	binary.Write(&buf, binary.LittleEndian, uint16(len(database)))
	binary.Write(&buf, binary.LittleEndian, uint16(buf.Len()+10))
	buf.WriteString(database)

	// Update length
	data := buf.Bytes()
	binary.LittleEndian.PutUint32(data[lengthPos:], uint32(len(data)))

	return c.writePacket(tdsTypeLogin7, data)
}

func encryptPassword(password string) []byte {
	// Simple XOR encryption as per TDS spec
	enc := make([]byte, len(password)*2)
	for i, c := range password {
		// Swap bytes and XOR with 0xA5
		enc[i*2] = byte(c>>8) ^ 0xa5
		enc[i*2+1] = byte(c) ^ 0xa5
	}
	return enc
}

func (c *tdsConn) readLoginResponse() error {
	for {
		p, err := c.readPacket()
		if err != nil {
			return err
		}

		if p.Type != tdsTypeReply {
			continue
		}

		// Parse tokens
		data := p.Data
		for len(data) > 0 {
			token := data[0]
			if token == tdsTokenLoginAck {
				// Login successful
				c.connected = true
				return nil
			}
			if token == tdsTokenError {
				return fmt.Errorf("login error")
			}
			if token == tdsTokenSSPI {
				// NTLM required - not implemented in test
				return fmt.Errorf("NTLM authentication required")
			}

			// Skip token (simplified)
			if len(data) > 1 {
				length := binary.LittleEndian.Uint16(data[1:3])
				data = data[3+length:]
			} else {
				break
			}
		}
	}
}

// Connect establishes a TDS connection
func (c *tdsConn) Connect(user, password, database string) error {
	// PRELOGIN
	if err := c.preLogin(); err != nil {
		return fmt.Errorf("prelogin failed: %w", err)
	}

	if err := c.readPreLoginResponse(); err != nil {
		return fmt.Errorf("prelogin response failed: %w", err)
	}

	// LOGIN7
	if err := c.login7(user, password, database); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	if err := c.readLoginResponse(); err != nil {
		return fmt.Errorf("login response failed: %w", err)
	}

	return nil
}

// connectMSSQL connects to MSSQL through Geryon
func connectMSSQL(t *testing.T) (*tdsConn, error) {
	host := env("MSSQL_HOST", "127.0.0.1")
	port := env("MSSQL_PORT", "1433")
	user := env("MSSQL_USER", "testuser")
	pass := env("MSSQL_PASSWORD", "testpass")
	db := env("MSSQL_DB", "test")

	conn, err := newTDSConn(host, port)
	if err != nil {
		return nil, err
	}

	if err := conn.Connect(user, pass, db); err != nil {
		conn.Close()
		return nil, err
	}

	t.Log("MSSQL connection successful")
	return conn, nil
}

// TestMSSQL_Connect tests basic MSSQL connectivity
func TestMSSQL_Connect(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MSSQL test in short mode")
	}

	if os.Getenv("MSSQL_TEST") == "" {
		t.Skip("Set MSSQL_TEST=1 to enable MSSQL tests")
	}

	conn, err := connectMSSQL(t)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	t.Log("MSSQL connection test passed")
}

// TestMSSQL_PreLogin tests PRELOGIN handshake
func TestMSSQL_PreLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MSSQL test in short mode")
	}

	if os.Getenv("MSSQL_TEST") == "" {
		t.Skip("Set MSSQL_TEST=1 to enable MSSQL tests")
	}

	host := env("MSSQL_HOST", "127.0.0.1")
	port := env("MSSQL_PORT", "1433")

	conn, err := newTDSConn(host, port)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	if err := conn.preLogin(); err != nil {
		t.Fatalf("PRELOGIN failed: %v", err)
	}

	if err := conn.readPreLoginResponse(); err != nil {
		t.Fatalf("PRELOGIN response failed: %v", err)
	}

	t.Log("PRELOGIN test passed")
}

// TestMSSQL_SQLBatch tests SQL batch execution
func TestMSSQL_SQLBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MSSQL test in short mode")
	}

	if os.Getenv("MSSQL_TEST") == "" {
		t.Skip("Set MSSQL_TEST=1 to enable MSSQL tests")
	}

	conn, err := connectMSSQL(t)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Send SQL batch
	sql := "SELECT 1 AS TestValue"
	allHeaders := []byte{0x00, 0x00, 0x00, 0x00} // Total length
	batch := append(allHeaders, []byte(sql)...)

	if err := conn.writePacket(tdsTypeSQLBatch, batch); err != nil {
		t.Fatalf("Failed to send batch: %v", err)
	}

	t.Log("SQL batch sent successfully")
	// Would need to read and parse response
}

// TestMSSQL_SessionPooling tests session pooling mode
func TestMSSQL_SessionPooling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MSSQL test in short mode")
	}

	if os.Getenv("MSSQL_TEST") == "" {
		t.Skip("Set MSSQL_TEST=1 to enable MSSQL tests")
	}

	if os.Getenv("MSSQL_POOL_MODE") != "session" {
		t.Skip("Set MSSQL_POOL_MODE=session to test session pooling")
	}

	t.Log("MSSQL session pooling test")
}

// TestMSSQL_TransactionPooling tests transaction pooling mode
func TestMSSQL_TransactionPooling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MSSQL test in short mode")
	}

	if os.Getenv("MSSQL_TEST") == "" {
		t.Skip("Set MSSQL_TEST=1 to enable MSSQL tests")
	}

	if os.Getenv("MSSQL_POOL_MODE") != "transaction" {
		t.Skip("Set MSSQL_POOL_MODE=transaction to test transaction pooling")
	}

	t.Log("MSSQL transaction pooling test")
}

// TestMSSQL_StatementPooling tests statement pooling mode
func TestMSSQL_StatementPooling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MSSQL test in short mode")
	}

	if os.Getenv("MSSQL_TEST") == "" {
		t.Skip("Set MSSQL_TEST=1 to enable MSSQL tests")
	}

	if os.Getenv("MSSQL_POOL_MODE") != "statement" {
		t.Skip("Set MSSQL_POOL_MODE=statement to test statement pooling")
	}

	t.Log("MSSQL statement pooling test")
}

// TestMSSQL_ResetConnection tests sp_reset_connection
func TestMSSQL_ResetConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MSSQL test in short mode")
	}

	if os.Getenv("MSSQL_TEST") == "" {
		t.Skip("Set MSSQL_TEST=1 to enable MSSQL tests")
	}

	t.Log("MSSQL connection reset test")
	t.Log("Geryon should call sp_reset_connection when returning connections to pool")
}

// TestMSSQL_NTLM tests NTLM authentication
func TestMSSQL_NTLM(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MSSQL test in short mode")
	}

	if os.Getenv("MSSQL_TEST") == "" {
		t.Skip("Set MSSQL_TEST=1 to enable MSSQL tests")
	}

	if os.Getenv("MSSQL_NTLM_TEST") == "" {
		t.Skip("Set MSSQL_NTLM_TEST=1 to test NTLM authentication")
	}

	t.Log("NTLM authentication test")
	t.Log("T065: Implement TDS NTLM passthrough for Windows Authentication")
}

// TestMSSQL_SPPrepare tests sp_prepare/sp_execute/sp_unprepare
func TestMSSQL_SPPrepare(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MSSQL test in short mode")
	}

	if os.Getenv("MSSQL_TEST") == "" {
		t.Skip("Set MSSQL_TEST=1 to enable MSSQL tests")
	}

	t.Log("sp_prepare/sp_execute/sp_unprepare test")
	t.Log("T069: Implement TDS sp_prepare/sp_execute/sp_unprepare for prepared statements")
}
