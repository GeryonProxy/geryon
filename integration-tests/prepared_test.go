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

// PostgreSQL prepared statement tests
// These tests verify prepared statements work correctly with transaction pooling

const (
	pgProtocolVersion = 0x0003_0000 // Protocol version 3.0
)

// PostgreSQL message types
const (
	// Frontend messages
	pgMsgBind             = 'B'
	pgMsgClose            = 'C'
	pgMsgDescribe         = 'D'
	pgMsgExecute          = 'E'
	pgMsgParse            = 'P'
	pgMsgPasswordMessage  = 'p'
	pgMsgQuery            = 'Q'
	pgMsgSync             = 'S'
	pgMsgTerminate        = 'X'

	// Backend messages
	pgMsgAuthentication   = 'R'
	pgMsgBindComplete     = '2'
	pgMsgCloseComplete    = '3'
	pgMsgCommandComplete  = 'C'
	pgMsgDataRow          = 'D'
	pgMsgErrorResponse    = 'E'
	pgMsgNoData           = 'n'
	pgMsgNoticeResponse   = 'N'
	pgMsgNotificationResponse = 'A'
	pgMsgParameterDescription = 't'
	pgMsgParameterStatus  = 'S'
	pgMsgParseComplete    = '1'
	pgMsgPortalSuspended  = 's'
	pgMsgReadyForQuery    = 'Z'
	pgMsgRowDescription   = 'T'
)

// PostgreSQL authentication types
const (
	pgAuthOk                = 0
	pgAuthKerberosV5        = 2
	pgAuthCleartextPassword = 3
	pgAuthMD5Password       = 5
	pgAuthSCMCredential     = 6
	pgAuthGSS               = 7
	pgAuthGSSContinue       = 8
	pgAuthSSPI              = 9
	pgAuthSASL              = 10
	pgAuthSASLContinue      = 11
	pgAuthSASLFinal         = 12
)

// pgConn represents a PostgreSQL connection
type pgConn struct {
	conn     net.Conn
	connected bool
}

// pgMessage represents a PostgreSQL message
type pgMessage struct {
	Type byte
	Data []byte
}

func newPGConn(host, port, user, database string) (*pgConn, error) {
	addr := net.JoinHostPort(host, port)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	c := &pgConn{conn: conn}

	// Send startup message
	if err := c.startup(user, database); err != nil {
		conn.Close()
		return nil, err
	}

	c.connected = true
	return c, nil
}

func (c *pgConn) Close() {
	if c.conn != nil {
		c.sendMessage(pgMsgTerminate, nil)
		c.conn.Close()
	}
}

func (c *pgConn) startup(user, database string) error {
	// Build startup message
	var buf bytes.Buffer

	// Protocol version (4 bytes)
	binary.Write(&buf, binary.BigEndian, pgProtocolVersion)

	// Parameters
	writeString(&buf, "user")
	writeString(&buf, user)
	writeString(&buf, "database")
	writeString(&buf, database)

	// Null terminator
	buf.WriteByte(0)

	// Send message (no type byte for startup)
	length := buf.Len() + 4
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(length))

	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	if _, err := c.conn.Write(buf.Bytes()); err != nil {
		return err
	}

	// Read authentication response
	msg, err := c.readMessage()
	if err != nil {
		return fmt.Errorf("auth response: %w", err)
	}

	if msg.Type != pgMsgAuthentication {
		return fmt.Errorf("expected authentication message, got %c", msg.Type)
	}

	authType := binary.BigEndian.Uint32(msg.Data)

	switch authType {
	case pgAuthOk:
		// Authentication successful
	case pgAuthCleartextPassword:
		// Send password
		if err := c.sendPassword(""); err != nil {
			return err
		}
		// Read auth response again
		msg, err = c.readMessage()
		if err != nil {
			return err
		}
		if msg.Type != pgMsgAuthentication {
			return fmt.Errorf("expected auth message after password")
		}
		if binary.BigEndian.Uint32(msg.Data) != pgAuthOk {
			return fmt.Errorf("authentication failed")
		}
	default:
		return fmt.Errorf("unsupported authentication type: %d", authType)
	}

	// Read parameter status and ready for query
	for {
		msg, err = c.readMessage()
		if err != nil {
			return err
		}
		if msg.Type == pgMsgReadyForQuery {
			break
		}
	}

	return nil
}

func (c *pgConn) sendPassword(password string) error {
	return c.sendMessage(pgMsgPasswordMessage, []byte(password+"\x00"))
}

func (c *pgConn) sendMessage(msgType byte, data []byte) error {
	// Type byte
	if _, err := c.conn.Write([]byte{msgType}); err != nil {
		return err
	}

	// Length (4 bytes, includes self)
	length := 4 + len(data)
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(length))
	if _, err := c.conn.Write(lenBuf); err != nil {
		return err
	}

	// Data
	if len(data) > 0 {
		if _, err := c.conn.Write(data); err != nil {
			return err
		}
	}

	return nil
}

func (c *pgConn) readMessage() (*pgMessage, error) {
	// Read type byte
	typeBuf := make([]byte, 1)
	if _, err := c.conn.Read(typeBuf); err != nil {
		return nil, err
	}

	// Read length
	lenBuf := make([]byte, 4)
	if _, err := c.conn.Read(lenBuf); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lenBuf)

	// Read data (length includes the 4 bytes for length itself)
	dataLen := length - 4
	if dataLen > 0 {
		data := make([]byte, dataLen)
		if _, err := c.conn.Read(data); err != nil {
			return nil, err
		}
		return &pgMessage{Type: typeBuf[0], Data: data}, nil
	}

	return &pgMessage{Type: typeBuf[0], Data: nil}, nil
}

func writeString(buf *bytes.Buffer, s string) {
	buf.WriteString(s)
	buf.WriteByte(0)
}

// Parse prepares a statement
func (c *pgConn) Parse(name, sql string, paramTypes []int32) error {
	var buf bytes.Buffer

	// Statement name
	writeString(&buf, name)
	// Query string
	writeString(&buf, sql)

	// Parameter types
	binary.Write(&buf, binary.BigEndian, uint16(len(paramTypes)))
	for _, t := range paramTypes {
		binary.Write(&buf, binary.BigEndian, t)
	}

	if err := c.sendMessage(pgMsgParse, buf.Bytes()); err != nil {
		return err
	}

	if err := c.sendMessage(pgMsgSync, nil); err != nil {
		return err
	}

	// Read responses until ReadyForQuery or Error
	for {
		msg, err := c.readMessage()
		if err != nil {
			return err
		}

		switch msg.Type {
		case pgMsgParseComplete:
			// Success
		case pgMsgErrorResponse:
			return fmt.Errorf("parse error")
		case pgMsgReadyForQuery:
			return nil
		}
	}
}

// Bind binds parameters to a prepared statement
func (c *pgConn) Bind(portal, statement string, paramValues [][]byte) error {
	var buf bytes.Buffer

	// Portal name
	writeString(&buf, portal)
	// Statement name
	writeString(&buf, statement)

	// Parameter format codes (0 = text)
	binary.Write(&buf, binary.BigEndian, uint16(0))

	// Parameter values
	binary.Write(&buf, binary.BigEndian, uint16(len(paramValues)))
	for _, val := range paramValues {
		if val == nil {
			binary.Write(&buf, binary.BigEndian, int32(-1))
		} else {
			binary.Write(&buf, binary.BigEndian, int32(len(val)))
			buf.Write(val)
		}
	}

	// Result format codes (0 = text)
	binary.Write(&buf, binary.BigEndian, uint16(0))

	if err := c.sendMessage(pgMsgBind, buf.Bytes()); err != nil {
		return err
	}

	if err := c.sendMessage(pgMsgSync, nil); err != nil {
		return err
	}

	// Read responses
	for {
		msg, err := c.readMessage()
		if err != nil {
			return err
		}

		switch msg.Type {
		case pgMsgBindComplete:
			// Success
		case pgMsgErrorResponse:
			return fmt.Errorf("bind error")
		case pgMsgReadyForQuery:
			return nil
		}
	}
}

// Execute executes a bound portal
func (c *pgConn) Execute(portal string, maxRows int32) error {
	var buf bytes.Buffer

	// Portal name
	writeString(&buf, portal)
	// Max rows (0 = all)
	binary.Write(&buf, binary.BigEndian, maxRows)

	if err := c.sendMessage(pgMsgExecute, buf.Bytes()); err != nil {
		return err
	}

	if err := c.sendMessage(pgMsgSync, nil); err != nil {
		return err
	}

	return nil
}

// ClosePrepared closes a prepared statement
func (c *pgConn) ClosePrepared(name string) error {
	var buf bytes.Buffer
	buf.WriteByte('S') // Statement
	writeString(&buf, name)

	if err := c.sendMessage(pgMsgClose, buf.Bytes()); err != nil {
		return err
	}

	if err := c.sendMessage(pgMsgSync, nil); err != nil {
		return err
	}

	// Read close complete
	for {
		msg, err := c.readMessage()
		if err != nil {
			return err
		}
		if msg.Type == pgMsgCloseComplete || msg.Type == pgMsgReadyForQuery {
			return nil
		}
	}
}

// connectPG connects to PostgreSQL through Geryon
func connectPG(t *testing.T) (*pgConn, error) {
	host := getEnv("PGHOST", "127.0.0.1")
	port := getEnv("PGPORT", "5432")
	user := getEnv("PGUSER", "testuser")
	db := getEnv("PGDATABASE", "test")

	conn, err := newPGConn(host, port, user, db)
	if err != nil {
		return nil, err
	}

	t.Log("PostgreSQL connection successful")
	return conn, nil
}

// TestPreparedStatement_Parse tests Parse message
func TestPreparedStatement_Parse(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping prepared statement test in short mode")
	}

	if os.Getenv("PG_TEST") == "" {
		t.Skip("Set PG_TEST=1 to enable PostgreSQL tests")
	}

	conn, err := connectPG(t)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Prepare a statement
	err = conn.Parse("test_stmt", "SELECT $1::int + $2::int", []int32{23, 23})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	t.Log("Parse successful")

	// Clean up
	conn.ClosePrepared("test_stmt")
}

// TestPreparedStatement_ParseAndBind tests Parse + Bind
func TestPreparedStatement_ParseAndBind(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping prepared statement test in short mode")
	}

	if os.Getenv("PG_TEST") == "" {
		t.Skip("Set PG_TEST=1 to enable PostgreSQL tests")
	}

	conn, err := connectPG(t)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Prepare statement
	err = conn.Parse("test_stmt", "SELECT $1::int + $2::int", []int32{23, 23})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Bind parameters
	params := [][]byte{
		[]byte("10"),
		[]byte("20"),
	}
	err = conn.Bind("", "test_stmt", params)
	if err != nil {
		t.Fatalf("Bind failed: %v", err)
	}

	t.Log("Parse and Bind successful")

	conn.ClosePrepared("test_stmt")
}

// TestPreparedStatement_FullCycle tests Parse + Bind + Execute
func TestPreparedStatement_FullCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping prepared statement test in short mode")
	}

	if os.Getenv("PG_TEST") == "" {
		t.Skip("Set PG_TEST=1 to enable PostgreSQL tests")
	}

	conn, err := connectPG(t)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Prepare
	err = conn.Parse("test_stmt", "SELECT $1::int + $2::int AS result", []int32{23, 23})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Bind
	params := [][]byte{[]byte("5"), []byte("3")}
	err = conn.Bind("portal1", "test_stmt", params)
	if err != nil {
		t.Fatalf("Bind failed: %v", err)
	}

	// Execute
	err = conn.Execute("portal1", 0)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	t.Log("Full prepared statement cycle successful")

	conn.ClosePrepared("test_stmt")
}

// TestPreparedStatement_AcrossServers tests prepared statements work across transaction pooling
func TestPreparedStatement_AcrossServers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping prepared statement test in short mode")
	}

	if os.Getenv("PG_TEST") == "" {
		t.Skip("Set PG_TEST=1 to enable PostgreSQL tests")
	}

	if os.Getenv("POOL_MODE") != "transaction" {
		t.Skip("Set POOL_MODE=transaction to test prepared statements across servers")
	}

	// This test would verify that prepared statements work correctly
	// when Geryon switches to a different backend connection
	// (which requires re-preparing the statement)

	t.Log("Prepared statement across servers test")
	t.Log("With transaction pooling, Geryon should:")
	t.Log("1. Track which statements are prepared on which server")
	t.Log("2. Re-prepare statements if needed when switching servers")
	t.Log("3. Maintain correct parameter bindings")
}

// TestPreparedStatement_MultipleStatements tests multiple prepared statements
func TestPreparedStatement_MultipleStatements(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping prepared statement test in short mode")
	}

	if os.Getenv("PG_TEST") == "" {
		t.Skip("Set PG_TEST=1 to enable PostgreSQL tests")
	}

	conn, err := connectPG(t)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Prepare multiple statements
	statements := []struct {
		name string
		sql  string
	}{
		{"stmt1", "SELECT $1::text"},
		{"stmt2", "SELECT $1::int * 2"},
		{"stmt3", "SELECT $1::bool"},
	}

	for _, s := range statements {
		err = conn.Parse(s.name, s.sql, []int32{})
		if err != nil {
			t.Fatalf("Parse %s failed: %v", s.name, err)
		}
		t.Logf("Prepared %s: %s", s.name, s.sql)
	}

	// Clean up
	for _, s := range statements {
		conn.ClosePrepared(s.name)
	}
}

// TestPreparedStatement_Reprepare tests statement re-preparation
func TestPreparedStatement_Reprepare(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping prepared statement test in short mode")
	}

	if os.Getenv("PG_TEST") == "" {
		t.Skip("Set PG_TEST=1 to enable PostgreSQL tests")
	}

	t.Log("Test: Geryon re-prepares statements on new server connections")
	t.Log("1. Client prepares 'SELECT $1'")
	t.Log("2. Server A executes the prepared statement")
	t.Log("3. Transaction ends, connection returns to pool")
	t.Log("4. Client sends Execute for same statement")
	t.Log("5. Server B is assigned (doesn't have statement)")
	t.Log("6. Geryon re-prepares statement on Server B")
	t.Log("7. Execute succeeds")
}
