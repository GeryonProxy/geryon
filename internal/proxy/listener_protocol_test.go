package proxy

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

// Helper: create a ProxySession with net.Pipe connections for client and server
func newProxySessionWithPipes(t *testing.T) (*ProxySession, net.Conn, net.Conn, func()) {
	t.Helper()
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: t.Name(),
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool(t.Name())

	clientEnd, clientProxy := net.Pipe()
	backendProxy, backendEnd := net.Pipe()

	ps, err := NewProxySession(clientProxy, p, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(backendProxy)
	ps.serverConn = serverConn

	cleanup := func() {
		clientEnd.Close()
		clientProxy.Close()
		backendEnd.Close()
		backendProxy.Close()
	}

	return ps, clientEnd, backendEnd, cleanup
}

// --- forwardMSSQLPreLogin tests ---

func TestForwardMSSQLPreLogin_ReadHeaderError(t *testing.T) {
	ps, clientEnd, _, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	// Client closes immediately -> read header fails
	clientEnd.Close()

	err := ps.forwardMSSQLPreLogin()
	if err == nil {
		t.Error("Should fail when client closes")
	}
}

func TestForwardMSSQLPreLogin_InvalidPacketType(t *testing.T) {
	ps, clientEnd, _, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	// Send wrong packet type (not 0x12)
	go func() {
		header := make([]byte, 8)
		header[0] = 0x01 // Wrong type
		binary.BigEndian.PutUint16(header[2:4], 8) // length=8 (header only)
		header[1] = 0x01 // StatusEndOfMessage
		clientEnd.Write(header)
	}()

	err := ps.forwardMSSQLPreLogin()
	if err == nil {
		t.Error("Should fail for invalid packet type")
	}
}

func TestForwardMSSQLPreLogin_InvalidLength(t *testing.T) {
	ps, clientEnd, _, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	// Send valid packet type but invalid length (< 8)
	go func() {
		header := make([]byte, 8)
		header[0] = 0x12 // PreLogin type
		binary.BigEndian.PutUint16(header[2:4], 4) // length < 8, invalid
		header[1] = 0x01
		clientEnd.Write(header)
	}()

	err := ps.forwardMSSQLPreLogin()
	if err == nil {
		t.Error("Should fail for invalid length")
	}
}

func TestForwardMSSQLPreLogin_ValidPreLogin(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	// Client sends Pre-Login
	go func() {
		// TDS Pre-Login packet: type=0x12, status=0x01 (EOM), length=16, payload=8 bytes
		header := make([]byte, 8)
		header[0] = 0x12 // PreLogin
		header[1] = 0x01 // StatusEndOfMessage
		binary.BigEndian.PutUint16(header[2:4], 16) // total length
		payload := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}
		clientEnd.Write(append(header, payload...))

		// Read the forwarded response from backend
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)
	}()

	// Backend reads forwarded Pre-Login and responds
	go func() {
		buf := make([]byte, 1024)
		backendEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		backendEnd.Read(buf)

		// Send Pre-Login response with EOM flag
		respHeader := make([]byte, 8)
		respHeader[0] = 0x04 // Response type
		respHeader[1] = 0x01 // StatusEndOfMessage
		binary.BigEndian.PutUint16(respHeader[2:4], 12) // length
		respPayload := []byte{0x00, 0x01, 0x02, 0x03}
		backendEnd.Write(append(respHeader, respPayload...))
	}()

	err := ps.forwardMSSQLPreLogin()
	if err != nil {
		t.Errorf("forwardMSSQLPreLogin failed: %v", err)
	}
}

// --- forwardMSSQLLogin7 tests ---

func TestForwardMSSQLLogin7_ReadHeaderError(t *testing.T) {
	ps, clientEnd, _, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	// Client closes -> read fails
	clientEnd.Close()

	err := ps.forwardMSSQLLogin7()
	if err == nil {
		t.Error("Should fail when client closes")
	}
}

func TestForwardMSSQLLogin7_InvalidPacketType(t *testing.T) {
	ps, clientEnd, _, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	go func() {
		header := make([]byte, 8)
		header[0] = 0xFF // Wrong type
		binary.BigEndian.PutUint16(header[2:4], 8)
		header[1] = 0x01
		clientEnd.Write(header)
	}()

	err := ps.forwardMSSQLLogin7()
	if err == nil {
		t.Error("Should fail for invalid Login7 packet type")
	}
}

// --- forwardMSSQLAuthResponse tests ---

func TestForwardMSSQLAuthResponse_ReadHeaderError(t *testing.T) {
	ps, _, _, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	// Backend closes -> read fails
	ps.serverConn.Conn().Close()

	err := ps.forwardMSSQLAuthResponse()
	if err == nil {
		t.Error("Should fail when backend closes")
	}
}

func TestForwardMSSQLAuthResponse_LoginAck(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	go func() {
		// Backend sends LoginAck with EOM flag
		header := make([]byte, 8)
		header[0] = 0x04 // TabularResult
		header[1] = 0x01 // StatusEndOfMessage
		binary.BigEndian.PutUint16(header[2:4], 13) // length = 8 + 5
		payload := []byte{0xAD, 0x00, 0x01, 0x00, 0x00} // LoginAck token
		backendEnd.Write(append(header, payload...))

		// Read response forwarded to client (discard)
		buf := make([]byte, 1024)
		backendEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		backendEnd.Read(buf)
	}()

	// Client reads forwarded response
	go func() {
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)
	}()

	err := ps.forwardMSSQLAuthResponse()
	if err != nil {
		t.Errorf("forwardMSSQLAuthResponse failed: %v", err)
	}
}

func TestForwardMSSQLAuthResponse_ErrorToken(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	go func() {
		// Backend sends Error token with EOM flag
		header := make([]byte, 8)
		header[0] = 0x04
		header[1] = 0x01 // StatusEndOfMessage
		binary.BigEndian.PutUint16(header[2:4], 13)
		payload := []byte{0xAA, 0x00, 0x01, 0x00, 0x00} // Error token
		backendEnd.Write(append(header, payload...))
	}()

	go func() {
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)
	}()

	err := ps.forwardMSSQLAuthResponse()
	if err == nil {
		t.Error("Should fail when backend sends error token")
	}
}

// --- forwardMySQLAuth tests ---

func TestForwardMySQLAuth_OKResponse(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	go func() {
		// Backend sends OK response packet
		// MySQL packet: 4-byte header (3 len + 1 seq) + payload
		payload := []byte{0x00} // OK packet
		header := make([]byte, 4)
		header[0] = byte(len(payload))
		header[1] = 0
		header[2] = 0
		header[3] = 2 // sequence
		backendEnd.Write(append(header, payload...))
	}()

	go func() {
		// Client reads forwarded response
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)
	}()

	err := ps.forwardMySQLAuth()
	if err != nil {
		t.Errorf("forwardMySQLAuth failed: %v", err)
	}
}

func TestForwardMySQLAuth_ERRResponse(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	go func() {
		// Backend sends ERR response packet
		payload := []byte{0xFF, 0x01, 0x00, 0x00, 0x41} // ERR packet
		header := make([]byte, 4)
		header[0] = byte(len(payload))
		header[3] = 2
		backendEnd.Write(append(header, payload...))
	}()

	go func() {
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)
	}()

	err := ps.forwardMySQLAuth()
	if err == nil {
		t.Error("Should fail when backend sends ERR")
	}
}

func TestForwardMySQLAuth_ReadError(t *testing.T) {
	ps, _, _, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	// Backend closes -> read fails
	ps.serverConn.Conn().Close()

	err := ps.forwardMySQLAuth()
	if err == nil {
		t.Error("Should fail when backend closes")
	}
}

func TestForwardMySQLAuth_ClientWriteError(t *testing.T) {
	ps, _, backendEnd, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	// Close client so write fails
	ps.clientConn.Close()

	go func() {
		// Send a packet that will try to be forwarded to (closed) client
		payload := []byte{0x00} // OK packet
		header := make([]byte, 4)
		header[0] = byte(len(payload))
		header[3] = 2
		backendEnd.Write(append(header, payload...))
	}()

	err := ps.forwardMySQLAuth()
	if err == nil {
		t.Error("Should fail when client is closed")
	}
}

// --- forwardAuthFromBackend: more paths ---

func TestForwardAuthFromBackend_AuthOK(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	// Send AuthOK + ReadyForQuery from backend
	authOK := makePGMessage('R', []byte{0, 0, 0, 0})
	rfq := makePGMessage('Z', []byte{'I'})

	// Write and close to ensure forwardAuthFromBackend sees EOF after data
	go func() {
		backendEnd.Write(append(authOK, rfq...))
	}()

	// Drain client (reads forwarded messages)
	go func() {
		buf := make([]byte, 4096)
		clientEnd.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			_, err := clientEnd.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	// Wait briefly for goroutines to start
	time.Sleep(10 * time.Millisecond)

	err := ps.forwardAuthFromBackend()
	if err != nil {
		t.Errorf("forwardAuthFromBackend failed: %v", err)
	}
	if !ps.authenticated.Load() {
		t.Error("Should be authenticated after AuthOK")
	}
}

func TestForwardAuthFromBackend_InvalidLength(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	go func() {
		// Send message with negative payload length
		backendEnd.Write([]byte{'R'})
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, 1) // length=1 means payloadLen=-3
		backendEnd.Write(lenBuf)
	}()

	go func() {
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		clientEnd.Read(buf)
	}()

	err := ps.forwardAuthFromBackend()
	if err == nil {
		t.Error("Should fail for invalid message length")
	}
}

func TestForwardAuthFromBackend_ReadHeaderError(t *testing.T) {
	ps, clientEnd, _, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	// Backend closes -> read fails
	_ = clientEnd // just cleanup
	ps.serverConn.Conn().Close()

	err := ps.forwardAuthFromBackend()
	if err == nil {
		t.Error("Should fail when backend closes")
	}
}

func TestForwardAuthToBackend_ReadError(t *testing.T) {
	ps, clientEnd, _, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	// Client closes -> read fails
	clientEnd.Close()

	err := ps.forwardAuthToBackend()
	if err == nil {
		t.Error("Should fail when client closes")
	}
}

func TestForwardAuthToBackend_InvalidLength(t *testing.T) {
	ps, clientEnd, _, cleanup := newProxySessionWithPipes(t)
	defer cleanup()

	go func() {
		// Send message type but bad length
		clientEnd.Write([]byte{'p'})
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, 1) // length=1, payloadLen=-3
		clientEnd.Write(lenBuf)
	}()

	err := ps.forwardAuthToBackend()
	if err == nil {
		t.Error("Should fail for invalid client message length")
	}
}

// Helper: construct a PG wire-protocol message (type byte + length uint32 + payload)
func makePGMessage(msgType byte, payload []byte) []byte {
	length := uint32(len(payload) + 4)
	msg := make([]byte, 1+4+len(payload))
	msg[0] = msgType
	binary.BigEndian.PutUint32(msg[1:5], length)
	copy(msg[5:], payload)
	return msg
}
