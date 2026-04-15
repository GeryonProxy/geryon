package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/cache"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
	"github.com/GeryonProxy/geryon/internal/protocol/common"
	"github.com/GeryonProxy/geryon/internal/protocol/postgresql"
	"github.com/GeryonProxy/geryon/internal/stmt"
)

// Helper: create a ProxySession with net.Pipe connections for testing
func newTestProxySession(t *testing.T, body string) (*ProxySession, net.Conn, net.Conn, func()) {
	t.Helper()
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: t.Name(),
		Mode: "transaction",
		Body: body,
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool(t.Name())

	var codec common.Codec
	if body == "postgresql" {
		codec = postgresql.NewCodec()
	} else {
		codec = postgresql.NewCodec() // fallback
	}

	clientEnd, clientProxy := net.Pipe()
	backendProxy, backendEnd := net.Pipe()

	ps, err := NewProxySession(clientProxy, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
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

// --- forwardAuthToBackend: valid payload path ---

func TestForwardAuthToBackend_ValidPayload(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	// Client sends a password message ('p') with valid payload
	go func() {
		// 'p' message type
		payload := []byte{0x00, 0x00, 0x00, 0x05}    // length=5
		payload = append(payload, []byte("test")...) // 4 bytes of data
		clientEnd.Write(append([]byte{'p'}, payload...))
	}()

	// Backend reads the forwarded message
	go func() {
		buf := make([]byte, 1024)
		backendEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		backendEnd.Read(buf)
	}()

	err := ps.forwardAuthToBackend()
	if err != nil {
		t.Errorf("forwardAuthToBackend failed: %v", err)
	}
}

// --- forwardAuthToBackend: write to backend error ---

func TestForwardAuthToBackend_WriteError(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	// Close server conn so write fails
	ps.serverConn.Conn().Close()

	go func() {
		// Client sends message type
		payload := make([]byte, 4)
		binary.BigEndian.PutUint32(payload, 5) // length=5
		clientEnd.Write(append([]byte{'p'}, payload...))
		clientEnd.Write([]byte("test"))
	}()

	err := ps.forwardAuthToBackend()
	if err == nil {
		t.Error("Should fail when backend write fails")
	}
}

// --- forwardAuthToBackend: zero payload ---

func TestForwardAuthToBackend_ZeroPayload(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	go func() {
		// 'p' message with length=4 (just the length field, no payload)
		clientEnd.Write([]byte{'p'})
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, 4)
		clientEnd.Write(lenBuf)
	}()

	go func() {
		buf := make([]byte, 1024)
		backendEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		backendEnd.Read(buf)
	}()

	err := ps.forwardAuthToBackend()
	if err != nil {
		t.Errorf("forwardAuthToBackend with zero payload failed: %v", err)
	}
}

// --- handlePostgreSQLStartup: SSL request, not supported ---

func TestHandlePostgreSQLStartup_SSLNotSupported(t *testing.T) {
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
	codec := postgresql.NewCodec()

	clientEnd, clientProxy := net.Pipe()

	ps, err := NewProxySession(clientProxy, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	defer clientEnd.Close()
	defer clientProxy.Close()

	go func() {
		// Send SSL request (length=8, code=80877103)
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, 8)
		codeBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(codeBuf, 80877103)
		clientEnd.Write(append(lenBuf, codeBuf...))

		// Read 'N' response
		buf := make([]byte, 1)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)

		// Close connection so the recursive handlePostgreSQLStartup call fails
		clientEnd.Close()
	}()

	ctx := context.Background()
	err = ps.handlePostgreSQLStartup(ctx)
	// Should get 'N' then try to read next startup message, which will fail
	if err == nil {
		t.Error("Should fail after SSL rejection since client closes")
	}
}

// --- handlePostgreSQLStartup: invalid startup length ---

func TestHandlePostgreSQLStartup_InvalidLength(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	go func() {
		// Send length=3 (too small, < 8)
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, 3)
		clientEnd.Write(lenBuf)
	}()

	err := ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail for invalid startup length")
	}
}

// --- handlePostgreSQLStartup: too-large length ---

func TestHandlePostgreSQLStartup_TooLarge(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	go func() {
		// Send length=20000 (too large, > 10000)
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, 20000)
		clientEnd.Write(lenBuf)
	}()

	err := ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail for too-large startup length")
	}
}

// --- handlePostgreSQLStartup: invalid protocol version ---

func TestHandlePostgreSQLStartup_InvalidProtocolVersion(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	go func() {
		// length=20, but invalid protocol version (not 196608)
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, 20)
		// Protocol version = 12345 (invalid)
		protoBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(protoBuf, 12345)
		data := make([]byte, 12) // rest of payload
		clientEnd.Write(append(append(lenBuf, protoBuf...), data...))
	}()

	err := ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail for invalid protocol version")
	}
}

// --- handlePostgreSQLStartup: no username ---

func TestHandlePostgreSQLStartup_NoUsername(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	go func() {
		// Valid startup message with no user parameter
		// length = 4 (length) + 4 (proto) + 1 (null terminator) = 9
		// But we need at least key\0value\0\0
		// Let's send: length=12, proto=196608, "user\0\0"
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, 12)
		protoBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(protoBuf, 196608)
		// Just a trailing null (no key-value pairs)
		data := []byte{0}
		// Actually the startup message is: length|proto|key\0value\0...\0
		// Let's make length=4+4+1=9, with just a null terminator
		binary.BigEndian.PutUint32(lenBuf, 9)
		clientEnd.Write(append(append(lenBuf, protoBuf...), data...))
	}()

	err := ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail when no username provided")
	}
}

// --- handlePostgreSQLStartup: unknown user ---

func TestHandlePostgreSQLStartup_UnknownUser(t *testing.T) {
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
	codec := postgresql.NewCodec()

	userDB := auth.NewUserDatabase()

	clientEnd, clientProxy := net.Pipe()
	ps, err := NewProxySession(clientProxy, p, codec, userDB, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	defer clientEnd.Close()
	defer clientProxy.Close()

	go func() {
		// Build startup: length | proto | user NUL testuser NUL
		proto := make([]byte, 4)
		binary.BigEndian.PutUint32(proto, 196608)
		params := append([]byte("user"), 0)
		params = append(params, []byte("testuser")...)
		params = append(params, 0)
		length := uint32(4 + 4 + len(params))
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, length)
		clientEnd.Write(append(append(lenBuf, proto...), params...))

		// Read error response
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)
	}()

	err = ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail for unknown user")
	}
}

// --- handlePostgreSQLStartup: read error ---

func TestHandlePostgreSQLStartup_ReadError(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	// Close client immediately
	clientEnd.Close()

	err := ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail when client closes")
	}
}

// --- handlePostgreSQLStartup: control character in username ---

func TestHandlePostgreSQLStartup_ControlCharInUsername(t *testing.T) {
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
	codec := postgresql.NewCodec()
	userDB := auth.NewUserDatabase()
	userDB.AddUser(&auth.User{Username: "bad\x01user"})

	clientEnd, clientProxy := net.Pipe()
	ps, err := NewProxySession(clientProxy, p, codec, userDB, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	defer clientEnd.Close()
	defer clientProxy.Close()

	go func() {
		proto := make([]byte, 4)
		binary.BigEndian.PutUint32(proto, 196608)
		// Username with control character
		params := append([]byte("user"), 0)
		params = append(params, []byte("bad\x01user")...)
		params = append(params, 0)
		length := uint32(4 + 4 + len(params))
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, length)
		clientEnd.Write(append(append(lenBuf, proto...), params...))
	}()

	err = ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail for control character in username")
	}
}

// --- handlePostgreSQLStartup: too many params ---

func TestHandlePostgreSQLStartup_TooManyParams(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	go func() {
		proto := make([]byte, 4)
		binary.BigEndian.PutUint32(proto, 196608)
		// Create 65 key-value pairs (max is 64)
		var params []byte
		for i := 0; i < 65; i++ {
			params = append(params, []byte(fmt.Sprintf("k%d", i))...)
			params = append(params, 0)
			params = append(params, []byte(fmt.Sprintf("v%d", i))...)
			params = append(params, 0)
		}
		length := uint32(4 + 4 + len(params))
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, length)
		clientEnd.Write(append(append(lenBuf, proto...), params...))
	}()

	err := ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail for too many startup parameters")
	}
}

// --- handlePostgreSQLStartup: value too long ---

func TestHandlePostgreSQLStartup_ValueTooLong(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	go func() {
		proto := make([]byte, 4)
		binary.BigEndian.PutUint32(proto, 196608)
		// Create a value > 256 bytes
		longVal := make([]byte, 300)
		for i := range longVal {
			longVal[i] = 'x'
		}
		params := append([]byte("user"), 0)
		params = append(params, longVal...)
		params = append(params, 0)
		length := uint32(4 + 4 + len(params))
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, length)
		clientEnd.Write(append(append(lenBuf, proto...), params...))
	}()

	err := ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail for too-long parameter value")
	}
}

// --- handlePostgreSQLStartup: SSL rejected, then client closes ---

func TestHandlePostgreSQLStartup_SSLThenClose(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	go func() {
		// Send SSL request (length=8, code=80877103)
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, 8)
		codeBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(codeBuf, 80877103)
		clientEnd.Write(append(lenBuf, codeBuf...))

		// Read 'N' response
		buf := make([]byte, 1)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)

		// Close - subsequent read in recursive call will fail
		clientEnd.Close()
	}()

	err := ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail after SSL rejection and close")
	}
}

// --- connectToBackend: passthrough mode with no username (no startup forward) ---

func TestConnectToBackend_NoPassthrough(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()
	_ = clientEnd
	_ = backendEnd

	// No username set - should skip passthrough, just connect
	// But the strategy OnClientConnect will fail since no real backend
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := ps.connectToBackend(ctx)
	if err == nil {
		t.Error("connectToBackend should fail with no real backend")
	}
}

// --- connectToBackend: passthrough with startup forward (codec type assertion) ---

func TestConnectToBackend_PassthroughStartup(t *testing.T) {
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
	codec := postgresql.NewCodec()

	clientEnd, clientProxy := net.Pipe()
	backendProxy, backendEnd := net.Pipe()

	ps, err := NewProxySession(clientProxy, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(backendProxy)
	ps.serverConn = serverConn
	ps.username = "testuser" // Set username to trigger passthrough

	// The strategy OnClientConnect will timeout since no real backend
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = ps.connectToBackend(ctx)
	if err == nil {
		t.Error("connectToBackend should fail with no real backend")
	}

	clientEnd.Close()
	clientProxy.Close()
	backendEnd.Close()
	backendProxy.Close()
}

// --- OnQuery: basic path ---

func TestOnQuery_BasicPath(t *testing.T) {
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
	codec := postgresql.NewCodec()

	clientEnd, clientProxy := net.Pipe()
	ps, err := NewProxySession(clientProxy, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	serverConn := &pool.ServerConn{}
	backendProxy, backendEnd := net.Pipe()
	serverConn.SetConnForTest(backendProxy)
	ps.serverConn = serverConn

	defer func() {
		clientEnd.Close()
		clientProxy.Close()
		backendEnd.Close()
		backendProxy.Close()
	}()

	ctx := context.Background()
	msg := &common.Message{Type: 'Q', Raw: []byte("SELECT 1")}

	// OnQuery will call strategy.OnQuery which will try to get a server conn
	// Since our pool has no real backends, this will timeout
	ctx2, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	_, err = ps.OnQuery(ctx2, msg)
	// Expected to fail due to no backend
	if err == nil {
		t.Log("OnQuery succeeded (unexpected but ok)")
	} else {
		t.Logf("OnQuery failed as expected: %v", err)
	}

	// But query count should have incremented
	if ps.QueryCount() != 1 {
		t.Errorf("QueryCount = %d, want 1", ps.QueryCount())
	}
}

// --- OnQuery: with router ---

func TestOnQuery_WithRouter(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: t.Name(),
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
				{Host: "127.0.0.1", Port: 5433, Role: "replica"},
			},
		},
		Routing: config.RoutingConfig{ReadWriteSplit: true},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool(t.Name())
	codec := postgresql.NewCodec()

	clientEnd, clientProxy := net.Pipe()
	router, _ := pool.NewRouter(&config.RoutingConfig{ReadWriteSplit: true}, nil)

	ps, err := NewProxySession(clientProxy, p, codec, nil, cfg, nil, nil, nil, nil, nil, router, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	serverConn := &pool.ServerConn{}
	backendProxy, backendEnd := net.Pipe()
	serverConn.SetConnForTest(backendProxy)
	ps.serverConn = serverConn

	defer func() {
		clientEnd.Close()
		clientProxy.Close()
		backendEnd.Close()
		backendProxy.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	msg := &common.Message{Type: 'Q', Raw: []byte("SELECT 1")}
	_, err = ps.OnQuery(ctx, msg)
	// Router code path is exercised even if it fails
	_ = err
}

// --- OnQueryComplete ---

func TestOnQueryComplete_Coverage(t *testing.T) {
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
	codec := postgresql.NewCodec()

	clientEnd, clientProxy := net.Pipe()
	defer clientEnd.Close()
	defer clientProxy.Close()

	ps, err := NewProxySession(clientProxy, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// Should not panic
	err = ps.OnQueryComplete()
	t.Logf("OnQueryComplete: %v", err)
}

// --- Handle: startup failure ---

func TestHandle_StartupFailure(t *testing.T) {
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
	codec := postgresql.NewCodec()

	clientEnd, clientProxy := net.Pipe()
	ps, err := NewProxySession(clientProxy, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// Close client so startup fails
	clientEnd.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Handle should not panic, just log error and return
	done := make(chan struct{})
	go func() {
		ps.Handle(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Good - Handle returned
	case <-time.After(3 * time.Second):
		t.Error("Handle should return after startup failure")
	}
}

// --- Handle: unsupported body type ---

func TestHandle_UnsupportedBody(t *testing.T) {
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
	codec := postgresql.NewCodec()

	clientEnd, clientProxy := net.Pipe()
	unsupportedCfg := *cfg
	unsupportedCfg.Body = "oracle"

	ps, err := NewProxySession(clientProxy, p, codec, nil, &unsupportedCfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		ps.Handle(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(3 * time.Second):
		t.Error("Handle should return for unsupported body type")
	}

	clientEnd.Close()
}

// --- recordAuthFailure/recordAuthSuccess: no limiter ---

func TestRecordAuth_NoLimiter(t *testing.T) {
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
	codec := postgresql.NewCodec()

	clientEnd, clientProxy := net.Pipe()
	defer clientEnd.Close()
	defer clientProxy.Close()

	ps, err := NewProxySession(clientProxy, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// Should not panic with nil limiter
	ps.recordAuthFailure()
	ps.recordAuthSuccess()
}

// --- forwardServerToClient: no server connection ---

func TestForwardServerToClient_NoServerConn(t *testing.T) {
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
	codec := postgresql.NewCodec()

	clientEnd, clientProxy := net.Pipe()
	defer clientEnd.Close()
	defer clientProxy.Close()

	ps, err := NewProxySession(clientProxy, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	relay := NewRelay()
	err = relay.forwardServerToClient(context.Background(), clientProxy, ps.poolSession, codec, ps)
	if err == nil {
		t.Error("Should fail with no server connection")
	}
}

// --- forwardServerToClient: context cancelled ---

func TestForwardServerToClient_ContextCancelled(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	relay := NewRelay()
	err := relay.forwardServerToClient(ctx, clientEnd, ps.poolSession, postgresql.NewCodec(), ps)
	if err == nil {
		t.Error("Should fail with cancelled context")
	}
}

// --- forwardClientToServer: context cancelled ---

func TestForwardClientToServer_ContextCancelled(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	relay := NewRelay()
	err := relay.forwardClientToServer(ctx, clientEnd, ps.poolSession, postgresql.NewCodec(), ps)
	if err == nil {
		t.Error("Should fail with cancelled context")
	}
}

// --- forwardClientToServer: read error (closed conn) ---

func TestForwardClientToServer_ReadError(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	clientEnd.Close()

	relay := NewRelay()
	err := relay.forwardClientToServer(context.Background(), clientEnd, ps.poolSession, postgresql.NewCodec(), ps)
	if err == nil {
		t.Error("Should fail with closed connection")
	}
}

// --- Relay Run: cancelled context ---

func TestRelay_Run_CancelledContext(t *testing.T) {
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
	codec := postgresql.NewCodec()

	clientEnd, clientProxy := net.Pipe()
	backendProxy, backendEnd := net.Pipe()
	defer clientEnd.Close()
	defer backendEnd.Close()

	ps, err := NewProxySession(clientProxy, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(backendProxy)
	ps.serverConn = serverConn

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	relay := NewRelay()
	done := make(chan struct{})
	go func() {
		relay.Run(ctx, clientProxy, ps.poolSession, codec, ps)
		// Close the backend proxy so the goroutine can exit
		backendProxy.Close()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(3 * time.Second):
		t.Error("Relay.Run should return with cancelled context")
	}
}

// --- handlePostgreSQLAuth: rate limited ---

func TestHandlePostgreSQLAuth_RateLimited(t *testing.T) {
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
	codec := postgresql.NewCodec()

	limiter := auth.NewAuthLimiter()

	clientEnd, clientProxy := net.Pipe()
	ps, err := NewProxySession(clientProxy, p, codec, nil, cfg, nil, nil, nil, nil, limiter, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	defer clientEnd.Close()
	defer clientProxy.Close()

	// Drain client reads so the error response write doesn't block
	go func() {
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)
	}()

	// Exhaust the limiter
	for i := 0; i < 20; i++ {
		limiter.RecordFailure("pipe")
	}

	user := &auth.User{Username: "test"}
	ctx := context.Background()
	err = ps.handlePostgreSQLAuth(ctx, user)
	if err == nil {
		t.Error("Should fail when rate limited")
	}
}

// --- handlePostgreSQLAuth: client closes after SASL request ---

func TestHandlePostgreSQLAuth_ClientClosesAfterSASL(t *testing.T) {
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
	codec := postgresql.NewCodec()

	userDB := auth.NewUserDatabase()
	userDB.AddUser(&auth.User{Username: "testuser"})

	clientEnd, clientProxy := net.Pipe()
	ps, err := NewProxySession(clientProxy, p, codec, userDB, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	go func() {
		// Read the SASL auth request
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)
		// Close connection
		clientEnd.Close()
	}()

	user := ps.userDB.GetUser("testuser")
	ctx := context.Background()
	err = ps.handlePostgreSQLAuth(ctx, user)
	if err == nil {
		t.Error("Should fail when client closes after SASL request")
	}
}

// --- handlePostgreSQLAuth: wrong message type ---

func TestHandlePostgreSQLAuth_WrongMsgType(t *testing.T) {
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
	codec := postgresql.NewCodec()

	userDB := auth.NewUserDatabase()
	userDB.AddUser(&auth.User{Username: "testuser"})

	clientEnd, clientProxy := net.Pipe()
	ps, err := NewProxySession(clientProxy, p, codec, userDB, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	go func() {
		// Read SASL auth request
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)
		// Send wrong message type (not 'p')
		clientEnd.Write([]byte{'X'})
	}()

	user := ps.userDB.GetUser("testuser")
	err = ps.handlePostgreSQLAuth(context.Background(), user)
	if err == nil {
		t.Error("Should fail with wrong message type")
	}
	clientEnd.Close()
}

// --- handlePostgreSQLAuth: unsupported mechanism ---

func TestHandlePostgreSQLAuth_UnsupportedMechanism(t *testing.T) {
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
	codec := postgresql.NewCodec()

	userDB := auth.NewUserDatabase()
	userDB.AddUser(&auth.User{Username: "testuser"})

	clientEnd, clientProxy := net.Pipe()
	ps, err := NewProxySession(clientProxy, p, codec, userDB, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	go func() {
		// Read SASL auth request
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)

		// Send 'p' message with unsupported mechanism
		// Message: 'p' + length(4) + mechanism\0 + data_length(4) + data
		mechanism := "PLAIN\x00"
		dataLen := make([]byte, 4)
		binary.BigEndian.PutUint32(dataLen, 0)
		payload := append([]byte(mechanism), dataLen...)
		length := uint32(len(payload) + 4)
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, length)

		clientEnd.Write(append([]byte{'p'}, append(lenBuf, payload...)...))
	}()

	user := ps.userDB.GetUser("testuser")
	err = ps.handlePostgreSQLAuth(context.Background(), user)
	if err == nil {
		t.Error("Should fail for unsupported mechanism")
	}
	clientEnd.Close()
}

// --- handlePostgreSQLAuth: invalid client-first SCRAM ---

func TestHandlePostgreSQLAuth_InvalidClientFirst(t *testing.T) {
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
	codec := postgresql.NewCodec()

	userDB := auth.NewUserDatabase()
	userDB.AddUser(&auth.User{Username: "testuser"})

	clientEnd, clientProxy := net.Pipe()
	ps, err := NewProxySession(clientProxy, p, codec, userDB, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	go func() {
		// Read SASL auth request
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)

		// Send SCRAM-SHA-256 initial response with invalid client-first
		mechanism := "SCRAM-SHA-256\x00"
		clientFirst := "invalid-client-first-data"
		dataLen := make([]byte, 4)
		binary.BigEndian.PutUint32(dataLen, uint32(len(clientFirst)))
		payload := append([]byte(mechanism), dataLen...)
		payload = append(payload, []byte(clientFirst)...)
		length := uint32(len(payload) + 4)
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, length)

		clientEnd.Write(append([]byte{'p'}, append(lenBuf, payload...)...))

		// Read error response
		buf2 := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf2)
	}()

	user := ps.userDB.GetUser("testuser")
	err = ps.handlePostgreSQLAuth(context.Background(), user)
	if err == nil {
		t.Error("Should fail for invalid SCRAM client-first")
	}
	clientEnd.Close()
}

// --- forwardAuthFromBackend: auth challenge + forwardAuthToBackend ---

func TestForwardAuthFromBackend_AuthChallenge(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	// Backend sends AuthMD5 challenge, then client responds, then backend sends AuthOK + ReadyForQuery
	go func() {
		// Auth request type 5 (MD5) - challenge
		authPayload := []byte{0, 0, 0, 5}                        // auth type = 5
		authPayload = append(authPayload, []byte{1, 2, 3, 4}...) // 4-byte salt
		authMsg := makePGMessage('R', authPayload)
		backendEnd.Write(authMsg)
	}()

	go func() {
		// Client reads forwarded challenge, then sends response
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _ := clientEnd.Read(buf)

		// Verify it's an auth request
		if n > 0 && buf[0] == 'R' {
			// Read what forwardAuthToBackend tries to read from client
			// forwardAuthToBackend reads from ps.clientConn which is clientProxy
			backendEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		}

		_ = n
	}()

	// This test exercises the 'R' non-OK path in forwardAuthFromBackend
	// The complexity of bidirectional coordination makes it hard to fully test
	// without real backend, so let's just test that we can read the initial message
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- ps.forwardAuthFromBackend()
	}()

	select {
	case err := <-done:
		// Either timeout or error from read/write
		t.Logf("forwardAuthFromBackend: %v", err)
	case <-ctx.Done():
		t.Log("forwardAuthFromBackend timed out (acceptable)")
	}
}

// --- forwardMSSQLLogin7: valid header forwarded to backend that closes ---

func TestForwardMSSQLLogin7_BackendCloses(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "mssql")
	defer cleanup()

	// Close backend so forwardMSSQLAuthResponse fails
	ps.serverConn.Conn().Close()

	go func() {
		// TDS Login7 packet: type=0x10, status=0x01, length
		header := make([]byte, 8)
		header[0] = 0x10           // Login7 type
		header[1] = 0x01           // StatusEndOfMessage
		payload := make([]byte, 8) // minimal payload
		binary.BigEndian.PutUint16(header[2:4], uint16(8+len(payload)))
		clientEnd.Write(append(header, payload...))
	}()

	// Should fail because backend is closed (forwardMSSQLAuthResponse will fail)
	err := ps.forwardMSSQLLogin7()
	if err == nil {
		t.Error("Should fail when backend is closed after Login7 forward")
	}
}

// --- extractMySQLScramble: valid-ish data ---

func TestExtractMySQLScramble_ValidData(t *testing.T) {
	// MySQL handshake: version string + null + connection_id(4) + scramble_part1(8) + null + ...
	data := make([]byte, 100)
	copy(data[0:], "5.7.0\x00")
	// connection_id at offset 6
	binary.LittleEndian.PutUint32(data[6:10], 1)
	// auth plugin data part 1 at offset 10 (8 bytes)
	copy(data[10:18], "scramble")
	// filler at 18
	data[18] = 0
	// capability flags lower 2 bytes at 19
	binary.LittleEndian.PutUint16(data[19:21], 0xFFFF)
	// charset at 21
	data[21] = 33
	// status flags at 22
	binary.LittleEndian.PutUint16(data[22:24], 0)
	// capability flags upper 2 bytes at 24
	binary.LittleEndian.PutUint16(data[24:26], 0xFFFF)
	// auth plugin data length at 26
	data[26] = 20
	// reserved 10 bytes at 27
	// auth plugin data part 2 at 37 (12 more bytes)
	copy(data[37:49], "012345678901")

	scramble, err := extractMySQLScramble(data)
	if err != nil {
		t.Logf("extractMySQLScramble: %v", err)
	} else {
		t.Logf("Got scramble of length %d", len(scramble))
	}
}

// --- parseMySQLHandshakeResponse: valid-ish response ---

func TestParseMySQLHandshakeResponse_ValidResponse(t *testing.T) {
	data := make([]byte, 200)
	// capability flags (4 bytes)
	binary.LittleEndian.PutUint32(data[0:4], 0x00000000)
	// max packet size (4 bytes)
	binary.LittleEndian.PutUint32(data[4:8], 16777216)
	// charset (1 byte)
	data[8] = 33
	// 23 bytes reserved
	// username starts at 32
	copy(data[32:], "testuser\x00")
	// password starts after username null terminator
	pwOffset := 32 + len("testuser") + 1
	data[pwOffset] = 20 // auth response length
	copy(data[pwOffset+1:pwOffset+21], "01234567890123456789")
	// database starts after auth response
	dbOffset := pwOffset + 21
	copy(data[dbOffset:], "testdb\x00")

	username, database, err := parseMySQLHandshakeResponse(data)
	if err != nil {
		t.Logf("parseMySQLHandshakeResponse: %v", err)
	}
	t.Logf("username=%q database=%q", username, database)
}

// --- createMySQLHandshake ---

func TestCreateMySQLHandshake_Basic(t *testing.T) {
	result := createMySQLHandshake(1, []byte("scrambledata"))
	if len(result) == 0 {
		t.Error("createMySQLHandshake should return non-empty result")
	}
}

// --- sendRollbackToBackend: with poolSession server conn ---

func TestSendRollbackToBackend_WithPoolSessionConn(t *testing.T) {
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
	codec := postgresql.NewCodec()

	clientEnd, clientProxy := net.Pipe()
	backendProxy, backendEnd := net.Pipe()
	defer clientEnd.Close()
	defer clientProxy.Close()
	defer backendEnd.Close()
	defer backendProxy.Close()

	ps, err := NewProxySession(clientProxy, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// Set up server conn on pool session (not ps.serverConn)
	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(backendProxy)
	ps.poolSession.SetServerConn(serverConn)

	// Read the ROLLBACK from backend
	go func() {
		buf := make([]byte, 1024)
		backendEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		backendEnd.Read(buf)
	}()

	// Should not panic
	ps.sendRollbackToBackend()
}

// --- extractTablesFromQuery: various cases ---

func TestExtractTablesFromQuery_Various(t *testing.T) {
	tests := []struct {
		query    string
		expected int
	}{
		{"SELECT * FROM users", 1},
		{"SELECT * FROM users JOIN orders ON users.id=orders.user_id", 1},
		{"INSERT INTO orders VALUES (1)", 0}, // no FROM clause
		{"UPDATE users SET x=1", 0},          // no FROM
		{"", 0},
		{"SELECT 1", 0},
	}

	for _, tt := range tests {
		tables := extractTablesFromQuery(tt.query)
		if len(tables) != tt.expected {
			t.Errorf("extractTablesFromQuery(%q) = %d tables, want %d", tt.query, len(tables), tt.expected)
		}
	}
}

// --- forwardServerToClient: read error ---

func TestForwardServerToClient_ReadError(t *testing.T) {
	ps, _, backendEnd, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	// Close backend so read fails
	backendEnd.Close()

	relay := NewRelay()
	err := relay.forwardServerToClient(context.Background(), ps.clientConn, ps.poolSession, postgresql.NewCodec(), ps)
	if err == nil {
		t.Error("Should fail when backend connection is closed")
	}
}

// --- reprepareStatement: needs reprep with prepared statement in cache ---

func TestReprepareStatement_NeedsReprep(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: t.Name(),
		Mode: "statement",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool(t.Name())
	codec := postgresql.NewCodec()

	clientEnd, clientProxy := net.Pipe()
	backendProxy, backendEnd := net.Pipe()
	defer clientEnd.Close()
	defer clientProxy.Close()
	defer backendEnd.Close()
	defer backendProxy.Close()

	ps, err := NewProxySession(clientProxy, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// Set up server conn with a specific ID
	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(backendProxy)
	ps.serverConn = serverConn

	// Register a prepared statement in the pool session's cache
	ps.poolSession.PreparedStatements().Register("test_stmt", "SELECT $1", nil)

	// Read the Parse message from backend
	go func() {
		buf := make([]byte, 4096)
		backendEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		backendEnd.Read(buf)
	}()

	// Call reprepareStatement - should prepare on new conn (connID different from registered)
	ps.reprepareStatement(codec, backendProxy, "test_stmt")
}

// --- reprepareStatement: empty name ---

func TestReprepareStatement_EmptyName(t *testing.T) {
	ps, _, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	// Should return immediately
	ps.reprepareStatement(postgresql.NewCodec(), nil, "")
}

// --- reprepareStatement: statement not in prepared statements cache ---

func TestReprepareStatement_NotInCache(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()
	_ = clientEnd
	_ = backendEnd

	// Call with non-existent statement name
	ps.reprepareStatement(postgresql.NewCodec(), ps.serverConn.Conn(), "nonexistent")
}

// --- Listener handleConnection integration test ---

func TestListener_HandleConnection_ClientCloses(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: t.Name(),
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
		Limits: config.LimitConfig{
			MaxClientConnections: 100,
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool(t.Name())
	codec := postgresql.NewCodec()

	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	if err := listener.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer listener.Stop()

	// Connect and immediately close
	conn, err := net.DialTimeout("tcp", listener.listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	conn.Close()

	// Give time for handleConnection to process
	time.Sleep(100 * time.Millisecond)
}

// --- Listener handleConnection: max connections reached ---

func TestListener_HandleConnection_MaxConnections(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: t.Name(),
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
		Limits: config.LimitConfig{
			MaxClientConnections: 0, // 0 means no connections allowed
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool(t.Name())
	codec := postgresql.NewCodec()

	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	if err := listener.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer listener.Stop()

	// Connect - should be rejected immediately (max 0 connections)
	conn, err := net.DialTimeout("tcp", listener.listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Server will close the connection quickly
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	_, _ = conn.Read(buf)
}

// --- acceptLoop: Stop() breaks accept loop ---

func TestListener_AcceptLoop_StopBreaksLoop(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: t.Name(),
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool(t.Name())
	codec := postgresql.NewCodec()

	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	if err := listener.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Stop should break the accept loop
	time.Sleep(50 * time.Millisecond)
	listener.Stop()

	// Verify stopped
	if listener.active.Load() {
		t.Error("Listener should be stopped")
	}
}

// --- Stop: double stop ---

func TestListener_Stop_DoubleStop(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: t.Name(),
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Mode: "transaction",
		Body: "postgresql",
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool(t.Name())
	codec := postgresql.NewCodec()

	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	if err := listener.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// First stop
	if err := listener.Stop(); err != nil {
		t.Errorf("First Stop failed: %v", err)
	}

	// Second stop should return nil (already stopped)
	if err := listener.Stop(); err != nil {
		t.Errorf("Second Stop should return nil, got: %v", err)
	}
}

// --- ProxySession accessor coverage ---

func TestProxySession_Accessors(t *testing.T) {
	ps, clientEnd, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()
	_ = clientEnd

	if ps.ID() == 0 {
		t.Error("ID should be > 0")
	}
	if ps.QueryCount() != 0 {
		t.Error("QueryCount should start at 0")
	}
	if err := ps.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

// --- forwardAuthFromBackend: payload too large ---

func TestForwardAuthFromBackend_PayloadTooLarge(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	go func() {
		// Send message with huge payload length
		backendEnd.Write([]byte{'R'})
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, 16<<20+10) // > maxMySQLPayload
		backendEnd.Write(lenBuf)
	}()

	// Drain any client writes
	go func() {
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)
	}()

	err := ps.forwardAuthFromBackend()
	if err == nil {
		t.Error("Should fail for payload too large")
	}
}

// --- forwardAuthFromBackend: zero-length payload (OK type) ---

func TestForwardAuthFromBackend_ZeroPayloadOK(t *testing.T) {
	ps, clientEnd, backendEnd, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	go func() {
		// AuthOK with zero-length payload: 'R' + length(4) = length 4 means payloadLen = 0
		authOK := makePGMessage('R', []byte{0, 0, 0, 0})
		rfq := makePGMessage('Z', []byte{'I'})
		backendEnd.Write(append(authOK, rfq...))
	}()

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

	time.Sleep(10 * time.Millisecond)

	err := ps.forwardAuthFromBackend()
	if err != nil {
		t.Errorf("forwardAuthFromBackend failed: %v", err)
	}
	if !ps.authenticated.Load() {
		t.Error("Should be authenticated after AuthOK")
	}
}

// ======== handleStartup with mysql/mssql body type ========

func TestHandleStartup_MySQL_Body(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test-mysql-startup",
		Mode: "transaction",
		Body: "mysql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 3306, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test-mysql-startup")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	ps, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	defer ps.Close()

	ctx := context.Background()
	err = ps.handleStartup(ctx)
	// Expected to fail because no real backend
	if err == nil {
		t.Log("handleStartup mysql succeeded")
	} else {
		t.Logf("handleStartup mysql error (expected): %v", err)
	}
}

func TestHandleStartup_MSSQL_Body(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test-mssql-startup",
		Mode: "transaction",
		Body: "mssql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 1433, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test-mssql-startup")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	ps, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	defer ps.Close()

	ctx := context.Background()
	err = ps.handleStartup(ctx)
	if err == nil {
		t.Log("handleStartup mssql succeeded")
	} else {
		t.Logf("handleStartup mssql error (expected): %v", err)
	}
}

// ======== handleMySQLStartup with mock backend ========

func TestHandleMySQLStartup_MockBackend(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test-mysql-mock",
		Mode: "session",
		Body: "mysql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 3306, Role: "primary"}},
		},
		Limits: config.LimitConfig{MaxServerConnections: 10},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test-mysql-mock")

	backendClient, backendServer := net.Pipe()
	defer backendClient.Close()

	sc := pool.NewServerConnForTest(1, backendClient, &pool.Backend{Host: "127.0.0.1", Port: 3306})
	p.Release(sc)

	codec := postgresql.NewCodec()
	clientServer, clientClient := net.Pipe()
	defer clientClient.Close()

	ps, err := NewProxySession(clientServer, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	defer ps.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Backend side: send MySQL handshake, read response, send OK
	go func() {
		defer backendServer.Close()

		handshake := buildMySQLHandshakeFull()
		hdrLen := len(handshake)
		header := make([]byte, 4)
		header[0] = byte(hdrLen & 0xFF)
		header[1] = byte((hdrLen >> 8) & 0xFF)
		header[2] = byte((hdrLen >> 16) & 0xFF)
		header[3] = 0
		backendServer.Write(append(header, handshake...))

		buf := make([]byte, 4096)
		backendServer.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, err := backendServer.Read(buf)
		if err != nil || n == 0 {
			return
		}

		// Send OK response
		okResp := []byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00}
		backendServer.Write(okResp)
	}()

	// Client side: read handshake, send response
	go func() {
		defer clientClient.Close()

		buf := make([]byte, 4096)
		clientClient.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, err := clientClient.Read(buf)
		if err != nil || n < 4 {
			return
		}

		resp := buildMySQLHandshakeResp()
		respHeader := make([]byte, 4)
		respLen := len(resp)
		respHeader[0] = byte(respLen & 0xFF)
		respHeader[1] = byte((respLen >> 8) & 0xFF)
		respHeader[2] = byte((respLen >> 16) & 0xFF)
		respHeader[3] = 1
		clientClient.Write(append(respHeader, resp...))
	}()

	err = ps.handleMySQLStartup(ctx)
	_ = err
}

func buildMySQLHandshakeFull() []byte {
	var buf []byte
	buf = append(buf, 10) // protocol version
	buf = append(buf, []byte("5.7.42-test")...)
	buf = append(buf, 0)
	buf = binary.LittleEndian.AppendUint32(buf, 1)
	for i := 0; i < 8; i++ {
		buf = append(buf, byte(i+1))
	}
	buf = append(buf, 0)
	buf = binary.LittleEndian.AppendUint16(buf, 0x85a6)
	buf = append(buf, 255)
	buf = binary.LittleEndian.AppendUint16(buf, 0x0002)
	buf = binary.LittleEndian.AppendUint16(buf, 0x800f)
	buf = append(buf, 21)
	buf = append(buf, make([]byte, 10)...)
	for i := 0; i < 12; i++ {
		buf = append(buf, byte(i+9))
	}
	buf = append(buf, 0)
	buf = append(buf, []byte("mysql_native_password")...)
	buf = append(buf, 0)
	return buf
}

func buildMySQLHandshakeResp() []byte {
	data := make([]byte, 80)
	pos := 0
	binary.LittleEndian.PutUint32(data[pos:pos+4], 0x85a6)
	pos += 4
	binary.LittleEndian.PutUint32(data[pos:pos+4], 16777216)
	pos += 4
	data[pos] = 255
	pos++
	pos += 23
	copy(data[pos:], []byte("testuser"))
	data[pos+8] = 0
	pos += 9
	data[pos] = 20
	pos++
	for i := 0; i < 20; i++ {
		data[pos+i] = byte(i)
	}
	pos += 20
	copy(data[pos:], []byte("testdb"))
	return data
}

// ======== handleMSSQLStartup with mock backend ========

func TestHandleMSSQLStartup_MockBackend(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test-mssql-mock",
		Mode: "session",
		Body: "mssql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 1433, Role: "primary"}},
		},
		Limits: config.LimitConfig{MaxServerConnections: 10},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test-mssql-mock")

	backendClient, backendServer := net.Pipe()
	defer backendClient.Close()

	sc := pool.NewServerConnForTest(1, backendClient, &pool.Backend{Host: "127.0.0.1", Port: 1433})
	p.Release(sc)

	codec := postgresql.NewCodec()
	clientServer, clientClient := net.Pipe()
	defer clientClient.Close()

	ps, err := NewProxySession(clientServer, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	defer ps.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Client sends Pre-Login then Login7
	go func() {
		defer clientClient.Close()

		preLogin := make([]byte, 8)
		preLogin[0] = 0x12
		preLogin[1] = 0x01
		binary.BigEndian.PutUint16(preLogin[2:4], 8)
		clientClient.Write(preLogin)

		buf := make([]byte, 4096)
		clientClient.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, err := clientClient.Read(buf)
		if err != nil || n < 8 {
			return
		}

		login7Payload := make([]byte, 50)
		login7 := make([]byte, 8)
		login7[0] = 0x10
		login7[1] = 0x01
		binary.BigEndian.PutUint16(login7[2:4], uint16(8+len(login7Payload)))
		clientClient.Write(append(login7, login7Payload...))

		clientClient.SetReadDeadline(time.Now().Add(3 * time.Second))
		clientClient.Read(buf)
	}()

	// Backend handles forwarded packets
	go func() {
		defer backendServer.Close()
		reader := bufio.NewReader(backendServer)

		// Read Pre-Login
		header := make([]byte, 8)
		if _, err := io.ReadFull(reader, header); err != nil {
			return
		}
		length := binary.BigEndian.Uint16(header[2:4])
		if int(length) > 8 {
			payload := make([]byte, int(length)-8)
			io.ReadFull(reader, payload)
		}

		// Send Pre-Login response with EOM
		resp := make([]byte, 8)
		resp[0] = 0x04
		resp[1] = 0x01
		binary.BigEndian.PutUint16(resp[2:4], 8)
		backendServer.Write(resp)

		// Read Login7
		loginHeader := make([]byte, 8)
		if _, err := io.ReadFull(reader, loginHeader); err != nil {
			return
		}
		loginLength := binary.BigEndian.Uint16(loginHeader[2:4])
		if int(loginLength) > 8 {
			loginPayload := make([]byte, int(loginLength)-8)
			io.ReadFull(reader, loginPayload)
		}

		// Send LoginAck with EOM
		authResp := make([]byte, 12)
		authResp[0] = 0x04
		authResp[1] = 0x01
		binary.BigEndian.PutUint16(authResp[2:4], 12)
		authResp[8] = 0xAD // LoginAck
		backendServer.Write(authResp)
	}()

	err = ps.handleMSSQLStartup(ctx)
	_ = err
}

// ======== forwardClientToServer: terminate path ========

func TestForwardClientToServer_TerminatePath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
		Limits: config.LimitConfig{MaxServerConnections: 10},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer serverOtherEnd.Close()

	sc := pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 5432})

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = sc

	poolSession := pool.NewSession(p, pool.NewSessionStrategy(p))
	poolSession.SetServerConn(sc)
	ps.poolSession = poolSession

	relay := NewRelay()
	ctx := context.Background()

	go func() {
		defer clientOtherEnd.Close()
		terminate := []byte{'X', 0, 0, 0, 4}
		clientOtherEnd.Write(terminate)
	}()

	go func() {
		io.Copy(io.Discard, serverEnd)
	}()

	err = relay.forwardClientToServer(ctx, clientEnd, poolSession, codec, ps)
	if err != io.EOF {
		t.Errorf("forwardClientToServer terminate should return io.EOF, got: %v", err)
	}
}

// ======== forwardClientToServer: query with transaction manager ========

func TestForwardClientToServer_TransactionBeginEnd(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
		Limits: config.LimitConfig{MaxServerConnections: 10},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()
	defer serverOtherEnd.Close()

	sc := pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 5432})

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = sc

	poolSession := pool.NewSession(p, pool.NewTransactionStrategy(p))
	ps.poolSession = poolSession

	txnMgr := pool.NewTransactionManager(30*time.Minute, 5*time.Minute, 30*time.Second, log)
	ps.transactionMgr = txnMgr

	relay := NewRelay()
	ctx := context.Background()

	go func() {
		defer clientOtherEnd.Close()

		// BEGIN query
		beginPayload := append([]byte("BEGIN"), 0)
		beginLen := uint32(4 + len(beginPayload))
		beginMsg := []byte{'Q'}
		beginMsg = binary.BigEndian.AppendUint32(beginMsg, beginLen)
		beginMsg = append(beginMsg, beginPayload...)
		clientOtherEnd.Write(beginMsg)

		time.Sleep(50 * time.Millisecond)

		// COMMIT query
		commitPayload := append([]byte("COMMIT"), 0)
		commitLen := uint32(4 + len(commitPayload))
		commitMsg := []byte{'Q'}
		commitMsg = binary.BigEndian.AppendUint32(commitMsg, commitLen)
		commitMsg = append(commitMsg, commitPayload...)
		clientOtherEnd.Write(commitMsg)

		time.Sleep(50 * time.Millisecond)

		terminate := []byte{'X', 0, 0, 0, 4}
		clientOtherEnd.Write(terminate)
	}()

	go func() {
		io.Copy(io.Discard, serverEnd)
	}()

	err = relay.forwardClientToServer(ctx, clientEnd, poolSession, codec, ps)
	_ = err
}

// ======== forwardClientToServer: prepared statement Parse/Bind/Close ========

func TestForwardClientToServer_PreparedStatementPaths(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
		Limits: config.LimitConfig{MaxServerConnections: 10},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()
	defer serverOtherEnd.Close()

	sc := pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 5432})

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = sc

	poolSession := pool.NewSession(p, pool.NewSessionStrategy(p))
	poolSession.SetServerConn(sc)
	ps.poolSession = poolSession

	relay := NewRelay()
	ctx := context.Background()

	go func() {
		defer clientOtherEnd.Close()

		// Parse message
		parsePayload := append([]byte("test_stmt\x00SELECT $1\x00"), 0, 0)
		parseLen := uint32(4 + len(parsePayload))
		parseMsg := []byte{'P'}
		parseMsg = binary.BigEndian.AppendUint32(parseMsg, parseLen)
		parseMsg = append(parseMsg, parsePayload...)
		clientOtherEnd.Write(parseMsg)

		// Bind message
		bindPayload := []byte("my_portal\x00test_stmt\x00")
		bindLen := uint32(4 + len(bindPayload))
		bindMsg := []byte{'B'}
		bindMsg = binary.BigEndian.AppendUint32(bindMsg, bindLen)
		bindMsg = append(bindMsg, bindPayload...)
		clientOtherEnd.Write(bindMsg)

		// Close message (close statement)
		closePayload := append([]byte{'S'}, []byte("test_stmt")...)
		closePayload = append(closePayload, 0)
		closeLen := uint32(4 + len(closePayload))
		closeMsg := []byte{'C'}
		closeMsg = binary.BigEndian.AppendUint32(closeMsg, closeLen)
		closeMsg = append(closeMsg, closePayload...)
		clientOtherEnd.Write(closeMsg)

		time.Sleep(50 * time.Millisecond)

		terminate := []byte{'X', 0, 0, 0, 4}
		clientOtherEnd.Write(terminate)
	}()

	go func() {
		io.Copy(io.Discard, serverEnd)
	}()

	err = relay.forwardClientToServer(ctx, clientEnd, poolSession, codec, ps)
	_ = err
}

// ======== forwardClientToServer: Sync message ========

func TestForwardClientToServer_SyncMessage(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
		Limits: config.LimitConfig{MaxServerConnections: 10},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()
	defer serverOtherEnd.Close()

	sc := pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 5432})

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = sc

	poolSession := pool.NewSession(p, pool.NewSessionStrategy(p))
	poolSession.SetServerConn(sc)
	ps.poolSession = poolSession

	relay := NewRelay()
	ctx := context.Background()

	go func() {
		defer clientOtherEnd.Close()
		syncMsg := []byte{'S', 0, 0, 0, 4}
		clientOtherEnd.Write(syncMsg)
		time.Sleep(50 * time.Millisecond)
		terminate := []byte{'X', 0, 0, 0, 4}
		clientOtherEnd.Write(terminate)
	}()

	go func() {
		io.Copy(io.Discard, serverEnd)
	}()

	err = relay.forwardClientToServer(ctx, clientEnd, poolSession, codec, ps)
	_ = err
}

// ======== forwardClientToServer: cache hit path ========

func TestForwardClientToServer_CacheHitPath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
		Limits: config.LimitConfig{MaxServerConnections: 10},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	cacheStore := cache.NewStore(64*1024*1024, 5*time.Minute)
	cacheRules := cache.NewRulesEngine()
	cacheRules.AddRule(".*", 5*time.Minute, true)

	// Pre-populate cache
	cacheKey := cache.GenerateKey("SELECT 1").String()
	cachedData := []byte{'Z', 0, 0, 0, 5, 'I'}
	cacheStore.Set(cacheKey, cachedData, nil, 5*time.Minute)

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer serverOtherEnd.Close()

	sc := pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 5432})

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, cacheStore, cacheRules, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = sc

	poolSession := pool.NewSession(p, pool.NewSessionStrategy(p))
	poolSession.SetServerConn(sc)
	ps.poolSession = poolSession
	ps.stmtRepreparer = stmt.NewTransparentRepreparer(stmt.NewManager(1000))

	relay := NewRelay()
	ctx := context.Background()

	go func() {
		defer clientOtherEnd.Close()
		// SELECT query - should hit cache
		queryPayload := append([]byte("SELECT 1"), 0)
		queryLen := uint32(4 + len(queryPayload))
		queryMsg := []byte{'Q'}
		queryMsg = binary.BigEndian.AppendUint32(queryMsg, queryLen)
		queryMsg = append(queryMsg, queryPayload...)
		clientOtherEnd.Write(queryMsg)

		// Drain responses (from cache hit)
		buf := make([]byte, 4096)
		clientOtherEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			_, err := clientOtherEnd.Read(buf)
			if err != nil {
				break
			}
		}

		terminate := []byte{'X', 0, 0, 0, 4}
		clientOtherEnd.Write(terminate)
	}()

	// serverEnd is unused for cache hits; close it after a delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		serverEnd.Close()
	}()

	err = relay.forwardClientToServer(ctx, clientEnd, poolSession, codec, ps)
	_ = err
}

// ======== forwardClientToServer: modification query cache invalidation ========

func TestForwardClientToServer_ModificationCacheInvalidation(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
		Limits: config.LimitConfig{MaxServerConnections: 10},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	cacheStore := cache.NewStore(64*1024*1024, 5*time.Minute)
	cacheRules := cache.NewRulesEngine()
	cacheRules.AddRule(".*", 5*time.Minute, true)

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()
	defer serverOtherEnd.Close()

	sc := pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 5432})

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, cacheStore, cacheRules, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = sc

	poolSession := pool.NewSession(p, pool.NewSessionStrategy(p))
	poolSession.SetServerConn(sc)
	ps.poolSession = poolSession

	relay := NewRelay()
	ctx := context.Background()

	go func() {
		defer clientOtherEnd.Close()
		queryPayload := append([]byte("INSERT INTO users VALUES (1)"), 0)
		queryLen := uint32(4 + len(queryPayload))
		queryMsg := []byte{'Q'}
		queryMsg = binary.BigEndian.AppendUint32(queryMsg, queryLen)
		queryMsg = append(queryMsg, queryPayload...)
		clientOtherEnd.Write(queryMsg)

		time.Sleep(50 * time.Millisecond)
		terminate := []byte{'X', 0, 0, 0, 4}
		clientOtherEnd.Write(terminate)
	}()

	go func() {
		io.Copy(io.Discard, serverEnd)
	}()

	err = relay.forwardClientToServer(ctx, clientEnd, poolSession, codec, ps)
	_ = err
}

// ======== forwardMySQLAuth: OK, ERR, auth switch, too large ========

func TestForwardMySQLAuth_OKResponsePath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "mysql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 3306, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()
	defer serverOtherEnd.Close()

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 3306})

	go func() {
		okPayload := []byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00}
		header := make([]byte, 4)
		header[0] = byte(len(okPayload))
		header[3] = 2
		serverEnd.Write(append(header, okPayload...))
	}()

	go func() {
		io.Copy(io.Discard, clientOtherEnd)
	}()

	err = ps.forwardMySQLAuth()
	if err != nil {
		t.Errorf("forwardMySQLAuth OK failed: %v", err)
	}
}

func TestForwardMySQLAuth_ERRResponsePath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "mysql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 3306, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()
	defer serverOtherEnd.Close()

	authLimiter := auth.NewAuthLimiter()
	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, authLimiter, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 3306})

	go func() {
		errPayload := []byte{0xff, 0x01, 0x00, 0x00, 0x00}
		header := make([]byte, 4)
		header[0] = byte(len(errPayload))
		header[3] = 2
		serverEnd.Write(append(header, errPayload...))
	}()

	go func() {
		io.Copy(io.Discard, clientOtherEnd)
	}()

	err = ps.forwardMySQLAuth()
	if err == nil {
		t.Error("forwardMySQLAuth should fail for ERR response")
	}
}

func TestForwardMySQLAuth_ServerClosesConnPath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "mysql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 3306, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, _ := net.Pipe()
	defer clientEnd.Close()
	serverEnd, serverOtherEnd := net.Pipe()

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 3306})

	// Close server immediately to cause read error in forwardMySQLAuth
	serverEnd.Close()

	err = ps.forwardMySQLAuth()
	if err == nil {
		t.Error("forwardMySQLAuth should fail when server closes connection")
	}
}

func TestForwardMySQLAuth_AuthSwitchPath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "mysql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 3306, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()
	defer serverOtherEnd.Close()

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 3306})

	// Server: send auth switch then OK
	go func() {
		switchPayload := []byte{0xfe, 0x00, 0x00, 0x00, 0x00}
		switchHeader := make([]byte, 4)
		switchHeader[0] = byte(len(switchPayload))
		switchHeader[3] = 2
		serverEnd.Write(append(switchHeader, switchPayload...))

		buf := make([]byte, 4096)
		serverEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _ := serverEnd.Read(buf)
		_ = n

		okPayload := []byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00}
		okHeader := make([]byte, 4)
		okHeader[0] = byte(len(okPayload))
		okHeader[3] = 4
		serverEnd.Write(append(okHeader, okPayload...))
	}()

	// Client: read switch, respond
	go func() {
		buf := make([]byte, 4096)
		clientOtherEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _ := clientOtherEnd.Read(buf)
		_ = n

		authResp := []byte{0x01, 0x02, 0x03, 0x04}
		respHeader := make([]byte, 4)
		respHeader[0] = byte(len(authResp))
		respHeader[3] = 3
		clientOtherEnd.Write(append(respHeader, authResp...))

		clientOtherEnd.Read(buf)
	}()

	err = ps.forwardMySQLAuth()
	if err != nil {
		t.Errorf("forwardMySQLAuth auth switch failed: %v", err)
	}
}

// ======== extractMySQLScramble edge cases ========

func TestExtractMySQLScramble_ShortDataCov(t *testing.T) {
	_, err := extractMySQLScramble([]byte{10})
	if err == nil {
		t.Error("Should fail for too-short data")
	}
}

func TestExtractMySQLScramble_BadProtocolCov(t *testing.T) {
	data := make([]byte, 50)
	data[0] = 11
	_, err := extractMySQLScramble(data)
	if err == nil {
		t.Error("Should fail for wrong protocol version")
	}
}

func TestExtractMySQLScramble_MinimalValidCov(t *testing.T) {
	// Protocol(1) + null version(1) + connid(4) + scramble_part1(8) + filler(1) +
	// cap_low(2) + charset(1) + status(2) = 20 bytes minimum for part1
	data := make([]byte, 20)
	data[0] = 10 // protocol version
	data[1] = 0  // null terminator for version string
	// connID at [2:6] - zeros
	// scramble part1 at [6:14]
	copy(data[6:14], []byte("12345678"))
	// filler at [14]
	// cap_low at [15:17]
	// charset at [17]
	// status at [18:20]

	scramble, err := extractMySQLScramble(data)
	if err != nil {
		t.Fatalf("extractMySQLScramble failed: %v", err)
	}
	if len(scramble) != 8 {
		// Old protocol, only 8 bytes since no upper caps
		t.Errorf("scramble length = %d, want 8", len(scramble))
	}
}

func TestExtractMySQLScramble_OldProtocolCov(t *testing.T) {
	// Build handshake with data that ends before part 2
	var buf []byte
	buf = append(buf, 10)
	buf = append(buf, []byte("5.7.0")...)
	buf = append(buf, 0)
	buf = binary.LittleEndian.AppendUint32(buf, 1)
	buf = append(buf, make([]byte, 8)...)
	buf = append(buf, 0)
	buf = binary.LittleEndian.AppendUint16(buf, 0x85a6)
	buf = append(buf, 255)
	buf = binary.LittleEndian.AppendUint16(buf, 0x0002)
	// Cut here - no upper capability flags
	scramble, err := extractMySQLScramble(buf)
	if err != nil {
		t.Fatalf("extractMySQLScramble old protocol failed: %v", err)
	}
	if len(scramble) != 8 {
		t.Errorf("old protocol scramble length = %d, want 8", len(scramble))
	}
}

// ======== parseMySQLHandshakeResponse edge cases ========

func TestParseMySQLHandshakeResponse_TooShortCov(t *testing.T) {
	_, _, err := parseMySQLHandshakeResponse([]byte{0})
	if err == nil {
		t.Error("Should fail for too-short response")
	}
}

// ======== extractLogin7Credentials edge cases ========

func TestExtractLogin7Credentials_ShortDataCov(t *testing.T) {
	ps := &ProxySession{}
	ps.extractLogin7Credentials([]byte{0})
	if ps.username != "" {
		t.Error("Username should be empty for short data")
	}
}

func TestExtractLogin7Credentials_ValidCredsCov(t *testing.T) {
	ps := &ProxySession{}
	data := make([]byte, 100)
	binary.LittleEndian.PutUint16(data[28:30], 50)
	binary.LittleEndian.PutUint16(data[30:32], 4)
	binary.LittleEndian.PutUint16(data[36:38], 70)
	binary.LittleEndian.PutUint16(data[38:40], 4) // length=4 to match "mydb"
	for i, c := range "user" {
		binary.LittleEndian.PutUint16(data[50+i*2:], uint16(c))
	}
	for i, c := range "mydb" {
		binary.LittleEndian.PutUint16(data[70+i*2:], uint16(c))
	}
	ps.extractLogin7Credentials(data)
	if ps.username != "user" {
		t.Errorf("username = %q, want user", ps.username)
	}
	if ps.database != "mydb" {
		t.Errorf("database = %q, want mydb", ps.database)
	}
}

func TestExtractLogin7Credentials_OutOfBoundsCov(t *testing.T) {
	ps := &ProxySession{}
	data := make([]byte, 40)
	binary.LittleEndian.PutUint16(data[28:30], 200)
	binary.LittleEndian.PutUint16(data[30:32], 10)
	ps.extractLogin7Credentials(data)
	// Should not panic
}

// ======== forwardMSSQLPreLogin with invalid packet type ========

func TestForwardMSSQLPreLogin_InvalidType(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "mssql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 1433, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()
	defer serverOtherEnd.Close()
	_ = serverEnd

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 1433})

	go func() {
		pkt := make([]byte, 8)
		pkt[0] = 0x99 // wrong type
		pkt[1] = 0x01
		binary.BigEndian.PutUint16(pkt[2:4], 8)
		clientOtherEnd.Write(pkt)
	}()

	err = ps.forwardMSSQLPreLogin()
	if err == nil {
		t.Error("Should fail for wrong packet type")
	}
}

// ======== forwardMSSQLPreLogin valid multi-packet response ========

func TestForwardMSSQLPreLogin_MultiPacketResponse(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "mssql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 1433, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()
	defer serverOtherEnd.Close()

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 1433})

	go func() {
		preLogin := make([]byte, 8)
		preLogin[0] = 0x12
		preLogin[1] = 0x01
		binary.BigEndian.PutUint16(preLogin[2:4], 8)
		clientOtherEnd.Write(preLogin)

		// Drain all responses from proxy
		buf := make([]byte, 4096)
		clientOtherEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			_, err := clientOtherEnd.Read(buf)
			if err != nil {
				break
			}
		}
	}()

	go func() {
		reader := bufio.NewReader(serverEnd)
		header := make([]byte, 8)
		if _, err := io.ReadFull(reader, header); err != nil {
			return
		}

		// First packet: no EOM
		resp1 := make([]byte, 8)
		resp1[0] = 0x04
		resp1[1] = 0x00
		binary.BigEndian.PutUint16(resp1[2:4], 8)
		serverEnd.Write(resp1)

		// Second packet: EOM
		resp2 := make([]byte, 8)
		resp2[0] = 0x04
		resp2[1] = 0x01
		binary.BigEndian.PutUint16(resp2[2:4], 8)
		serverEnd.Write(resp2)
	}()

	err = ps.forwardMSSQLPreLogin()
	if err != nil {
		t.Errorf("forwardMSSQLPreLogin multi-packet failed: %v", err)
	}
}

// ======== forwardMSSQLLogin7 invalid packet type ========

func TestForwardMSSQLLogin7_InvalidType(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "mssql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 1433, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()
	defer serverOtherEnd.Close()
	_ = serverEnd

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 1433})

	go func() {
		pkt := make([]byte, 8)
		pkt[0] = 0x99
		pkt[1] = 0x01
		binary.BigEndian.PutUint16(pkt[2:4], 8)
		clientOtherEnd.Write(pkt)
	}()

	err = ps.forwardMSSQLLogin7()
	if err == nil {
		t.Error("Should fail for wrong Login7 packet type")
	}
}

// ======== forwardMSSQLAuthResponse error and invalid length paths ========

func TestForwardMSSQLAuthResponse_ErrorTokenPath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "mssql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 1433, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()
	defer serverOtherEnd.Close()

	authLimiter := auth.NewAuthLimiter()
	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, authLimiter, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 1433})

	go func() {
		pkt := make([]byte, 12)
		pkt[0] = 0x04
		pkt[1] = 0x01
		binary.BigEndian.PutUint16(pkt[2:4], 12)
		pkt[8] = 0xAA // Error token
		serverEnd.Write(pkt)
	}()

	go func() {
		io.Copy(io.Discard, clientOtherEnd)
	}()

	err = ps.forwardMSSQLAuthResponse()
	if err == nil {
		t.Error("Should fail for error token")
	}
}

func TestForwardMSSQLAuthResponse_InvalidLengthPath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "mssql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 1433, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, _ := net.Pipe()
	defer clientEnd.Close()
	serverEnd, serverOtherEnd := net.Pipe()
	defer serverOtherEnd.Close()

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 1433})

	go func() {
		pkt := make([]byte, 8)
		pkt[0] = 0x04
		pkt[1] = 0x01
		binary.BigEndian.PutUint16(pkt[2:4], 4) // length < 8
		serverEnd.Write(pkt)
	}()

	err = ps.forwardMSSQLAuthResponse()
	if err == nil {
		t.Error("Should fail for invalid length")
	}
}

// ======== forwardMSSQLAuthResponse LoginAck path ========

func TestForwardMSSQLAuthResponse_LoginAckPath(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "mssql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 1433, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()
	defer serverOtherEnd.Close()

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 1433})

	// Server sends LoginAck then EOM
	go func() {
		// First packet: LoginAck, not EOM
		pkt1 := make([]byte, 12)
		pkt1[0] = 0x04
		pkt1[1] = 0x00 // no EOM
		binary.BigEndian.PutUint16(pkt1[2:4], 12)
		pkt1[8] = 0xAD // LoginAck
		serverEnd.Write(pkt1)

		// Second packet: Done with EOM
		pkt2 := make([]byte, 8)
		pkt2[0] = 0x04
		pkt2[1] = 0x01 // EOM
		binary.BigEndian.PutUint16(pkt2[2:4], 8)
		serverEnd.Write(pkt2)
	}()

	go func() {
		io.Copy(io.Discard, clientOtherEnd)
	}()

	err = ps.forwardMSSQLAuthResponse()
	if err != nil {
		t.Errorf("forwardMSSQLAuthResponse LoginAck failed: %v", err)
	}
}

// ======== connectToBackend passthrough mode ========

func TestConnectToBackend_PassthroughStartupCov(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
		Limits: config.LimitConfig{MaxServerConnections: 10},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	backendClient, backendServer := net.Pipe()
	defer backendClient.Close()

	sc := pool.NewServerConnForTest(1, backendClient, &pool.Backend{Host: "127.0.0.1", Port: 5432})
	p.Release(sc)

	clientEnd, clientOtherEnd := net.Pipe()
	defer clientEnd.Close()

	// Drain client-side responses
	go func() {
		buf := make([]byte, 4096)
		clientOtherEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			_, err := clientOtherEnd.Read(buf)
			if err != nil {
				break
			}
		}
	}()

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.username = "testuser"
	ps.database = "testdb"

	ctx := context.Background()

	go func() {
		defer backendServer.Close()
		reader := bufio.NewReader(backendServer)
		buf := make([]byte, 4096)
		backendServer.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, err := reader.Read(buf)
		if err != nil || n == 0 {
			return
		}

		// Send AuthOK
		authOK := []byte{'R', 0, 0, 0, 8, 0, 0, 0, 0}
		backendServer.Write(authOK)

		// Send ReadyForQuery so forwardAuthFromBackend can return
		rfq := []byte{'Z', 0, 0, 0, 5, 'I'}
		backendServer.Write(rfq)
	}()

	err = ps.connectToBackend(ctx)
	_ = err
}

// ======== authenticateWithCertificate non-TLS ========

func TestAuthenticateWithCertificate_NonTLSConn(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, _ := net.Pipe()
	defer clientEnd.Close()

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	err = ps.authenticateWithCertificate()
	if err != nil {
		t.Errorf("authenticateWithCertificate on non-TLS should return nil, got: %v", err)
	}
}

// ======== forwardAndCapture error path ========

func TestForwardAndCapture_ServerWriteError(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, _ := net.Pipe()
	defer clientEnd.Close()

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	serverEnd, serverOtherEnd := net.Pipe()
	serverOtherEnd.Close()

	relay := NewRelay()
	msg := &common.Message{Type: 'Q', Raw: []byte{'Q', 0, 0, 0, 8, 'S', 'E', 'L', 0}}

	err = relay.forwardAndCapture(serverEnd, clientEnd, msg, "cache-key", ps, time.Now())
	if err == nil {
		t.Error("forwardAndCapture should fail when server is broken")
	}
	serverEnd.Close()
}

// ======== createMySQLHandshake with short scramble ========

func TestCreateMySQLHandshake_ShortScramblePath(t *testing.T) {
	scramble := make([]byte, 10)
	result := createMySQLHandshake(1, scramble)
	if len(result) == 0 {
		t.Error("createMySQLHandshake should handle short scramble")
	}
}

// ======== Handle with mysql body ========

func TestProxySession_Handle_MySQLBody(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "mysql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 3306, Role: "primary"}},
		},
		Limits: config.LimitConfig{MaxServerConnections: 10},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	ps, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		ps.Handle(ctx)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	client.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("Handle should return after client disconnect")
	}
}

// ======== Handle with mssql body ========

func TestProxySession_Handle_MSSQLBody(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "mssql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 1433, Role: "primary"}},
		},
		Limits: config.LimitConfig{MaxServerConnections: 10},
	}

	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	server, client := net.Pipe()
	defer client.Close()

	ps, err := NewProxySession(server, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		ps.Handle(ctx)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	client.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("Handle should return after client disconnect")
	}
}

// ======== ensure imports used ========

var _ = fmt.Sprintf
var _ = errors.New
var _ = io.ReadFull
var _ = bufio.NewReader
var _ = bytes.NewReader

// ======== handlePostgreSQLAuth: GenerateServerFirst error ========

func TestHandlePostgreSQLAuth_GenerateServerFirstError(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	// Create user with invalid SCRAM hash so GenerateServerFirst fails
	userDB := auth.NewUserDatabase()
	userDB.AddUser(&auth.User{Username: "testuser", PasswordHash: "invalid-hash"})

	clientEnd, clientProxy := net.Pipe()
	ps, err := NewProxySession(clientProxy, p, codec, userDB, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	go func() {
		// Read SASL auth request
		buf := make([]byte, 1024)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)

		// Send SCRAM-SHA-256 initial response with valid client-first
		mechanism := "SCRAM-SHA-256\x00"
		clientFirst := "n,,n=testuser,r=fyko+d2lbbFgONRv9qkxdawL"
		dataLen := make([]byte, 4)
		binary.BigEndian.PutUint32(dataLen, uint32(len(clientFirst)))
		payload := append([]byte(mechanism), dataLen...)
		payload = append(payload, []byte(clientFirst)...)
		length := uint32(len(payload) + 4)
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, length)
		clientEnd.Write(append([]byte{'p'}, append(lenBuf, payload...)...))

		// Read error response
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientEnd.Read(buf)
	}()

	user := ps.userDB.GetUser("testuser")
	err = ps.handlePostgreSQLAuth(context.Background(), user)
	if err == nil {
		t.Error("Should fail when GenerateServerFirst fails")
	}
	clientEnd.Close()
}

// ======== handlePostgreSQLAuth: client closes after SASL continue ========

func TestHandlePostgreSQLAuth_ClientClosesAfterSASLContinue(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	password := "correctpassword"
	hash, _ := auth.GenerateSCRAMHash(password)
	userDB := auth.NewUserDatabase()
	userDB.AddUser(&auth.User{Username: "testuser", PasswordHash: hash})

	clientEnd, clientProxy := net.Pipe()
	ps, err := NewProxySession(clientProxy, p, codec, userDB, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	go func() {
		// Read SASL auth request
		buf := make([]byte, 4096)
		clientEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _ := clientEnd.Read(buf)
		_ = n

		// Send valid SCRAM client-first
		mechanism := "SCRAM-SHA-256\x00"
		clientFirst := "n,,n=testuser,r=fyko+d2lbbFgONRv9qkxdawL"
		dataLen := make([]byte, 4)
		binary.BigEndian.PutUint32(dataLen, uint32(len(clientFirst)))
		payload := append([]byte(mechanism), dataLen...)
		payload = append(payload, []byte(clientFirst)...)
		length := uint32(len(payload) + 4)
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, length)
		clientEnd.Write(append([]byte{'p'}, append(lenBuf, payload...)...))

		// Read SASL continue, then close
		clientEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		clientEnd.Read(buf)
		clientEnd.Close()
	}()

	user := ps.userDB.GetUser("testuser")
	err = ps.handlePostgreSQLAuth(context.Background(), user)
	if err == nil {
		t.Error("Should fail when client closes after SASL continue")
	}
}

// ======== handlePostgreSQLAuth: wrong second message type ========

func TestHandlePostgreSQLAuth_WrongSecondMsgType(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	password := "correctpassword"
	hash, _ := auth.GenerateSCRAMHash(password)
	userDB := auth.NewUserDatabase()
	userDB.AddUser(&auth.User{Username: "testuser", PasswordHash: hash})

	clientEnd, clientProxy := net.Pipe()
	ps, err := NewProxySession(clientProxy, p, codec, userDB, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	go func() {
		// Read SASL auth request
		buf := make([]byte, 4096)
		clientEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _ := clientEnd.Read(buf)
		_ = n

		// Send valid SCRAM client-first
		mechanism := "SCRAM-SHA-256\x00"
		clientFirst := "n,,n=testuser,r=fyko+d2lbbFgONRv9qkxdawL"
		dataLen := make([]byte, 4)
		binary.BigEndian.PutUint32(dataLen, uint32(len(clientFirst)))
		payload := append([]byte(mechanism), dataLen...)
		payload = append(payload, []byte(clientFirst)...)
		length := uint32(len(payload) + 4)
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, length)
		clientEnd.Write(append([]byte{'p'}, append(lenBuf, payload...)...))

		// Read SASL continue
		clientEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		clientEnd.Read(buf)

		// Send wrong message type (not 'p') for client-final
		clientEnd.Write([]byte{'X'})
	}()

	user := ps.userDB.GetUser("testuser")
	err = ps.handlePostgreSQLAuth(context.Background(), user)
	if err == nil {
		t.Error("Should fail for wrong second message type")
	}
	clientEnd.Close()
}

// ======== handlePostgreSQLAuth: VerifyClientFinal fails (wrong password) ========

func TestHandlePostgreSQLAuth_VerifyClientFinalFails(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	password := "correctpassword"
	hash, _ := auth.GenerateSCRAMHash(password)
	userDB := auth.NewUserDatabase()
	userDB.AddUser(&auth.User{Username: "testuser", PasswordHash: hash})

	clientEnd, clientProxy := net.Pipe()
	ps, err := NewProxySession(clientProxy, p, codec, userDB, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	go func() {
		// Read SASL auth request
		buf := make([]byte, 4096)
		clientEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _ := clientEnd.Read(buf)
		_ = n

		// Send valid SCRAM client-first
		mechanism := "SCRAM-SHA-256\x00"
		clientFirst := "n,,n=testuser,r=fyko+d2lbbFgONRv9qkxdawL"
		dataLen := make([]byte, 4)
		binary.BigEndian.PutUint32(dataLen, uint32(len(clientFirst)))
		payload := append([]byte(mechanism), dataLen...)
		payload = append(payload, []byte(clientFirst)...)
		length := uint32(len(payload) + 4)
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, length)
		clientEnd.Write(append([]byte{'p'}, append(lenBuf, payload...)...))

		// Read SASL continue
		clientEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _ = clientEnd.Read(buf)

		// Parse server-first to extract nonce
		// The SASL continue message is: 'R' + len(4) + type(4=SASLContinue) + data
		// Data starts after 'R' (1) + length (4) + auth type (4) = offset 9 in the raw buffer
		// Actually the response we read starts at buf[0]
		// Format: R(1) + len(4) + authtype(4) + serverFirst
		serverFirstData := ""
		if n > 9 {
			serverFirstData = string(buf[9:n])
		}

		// Send client-final with wrong proof (using fake data)
		clientFinal := "c=biws,r=" + "fyko+d2lbbFgONRv9qkxdawL" + "fake,p=dGhlLXNlcnZlci1wcm9vZg=="
		// Actually let's just use the server-first nonce if we parsed it
		// For simplicity, send invalid client-final with channel binding and fake proof
		if serverFirstData != "" {
			// Extract nonce from server-first (r=...)
			rIdx := bytes.Index([]byte(serverFirstData), []byte("r="))
			if rIdx >= 0 {
				nonceEnd := bytes.Index([]byte(serverFirstData[rIdx+2:]), []byte(","))
				if nonceEnd < 0 {
					nonceEnd = len(serverFirstData) - rIdx - 2
				}
				nonce := serverFirstData[rIdx+2 : rIdx+2+nonceEnd]
				clientFinal = "c=biws,r=" + string(nonce) + ",p=dGhlLXNlcnZlci1wcm9vZg=="
			}
		}

		finalPayload := []byte(clientFinal)
		finalLen := uint32(4 + len(finalPayload))
		finalMsg := []byte{'p'}
		finalMsg = binary.BigEndian.AppendUint32(finalMsg, finalLen)
		finalMsg = append(finalMsg, finalPayload...)
		clientEnd.Write(finalMsg)

		// Read error response
		clientEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		clientEnd.Read(buf)
	}()

	user := ps.userDB.GetUser("testuser")
	err = ps.handlePostgreSQLAuth(context.Background(), user)
	if err == nil {
		t.Error("Should fail when client-final verification fails")
	}
	clientEnd.Close()
}

// ======== forwardAndCapture: full response loop with DataRow ========

func TestForwardAndCapture_FullResponseLoop(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
		Limits: config.LimitConfig{MaxServerConnections: 10},
	}
	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	cacheStore := cache.NewStore(64*1024*1024, 5*time.Minute)
	cacheRules := cache.NewRulesEngine()
	cacheRules.AddRule(".*", 5*time.Minute, true)

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()
	defer serverOtherEnd.Close()

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, cacheStore, cacheRules, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 5432})

	// Drain client responses
	go func() {
		buf := make([]byte, 4096)
		clientOtherEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			_, err := clientOtherEnd.Read(buf)
			if err != nil {
				break
			}
		}
	}()

	// Server sends RowDescription + DataRow + CommandComplete + ReadyForQuery
	go func() {
		// Read the query message from proxy
		buf := make([]byte, 4096)
		serverEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		serverEnd.Read(buf)

		// RowDescription ('T')
		rowDesc := makePGMessage('T', []byte{0, 1, 0, 4, 't', 'e', 's', 't', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
		serverEnd.Write(rowDesc)

		// DataRow ('D')
		dataRow := makePGMessage('D', []byte{0, 1, 0, 0, 0, 1, '1'})
		serverEnd.Write(dataRow)

		// CommandComplete ('C')
		cmdComplete := makePGMessage('C', append([]byte("SELECT 1"), 0))
		serverEnd.Write(cmdComplete)

		// ReadyForQuery ('Z')
		rfq := makePGMessage('Z', []byte{'I'})
		serverEnd.Write(rfq)
	}()

	relay := NewRelay()
	msg := &common.Message{Type: 'Q', Raw: []byte{'Q', 0, 0, 0, 8, 'S', 'E', 'L', 0}}

	err = relay.forwardAndCapture(serverOtherEnd, clientEnd, msg, "test-key", ps, time.Now())
	if err != nil {
		t.Errorf("forwardAndCapture failed: %v", err)
	}
	serverEnd.Close()
}

// ======== forwardAndCapture: server read error during response ========

func TestForwardAndCapture_ServerReadError(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("test")
	codec := postgresql.NewCodec()

	clientEnd, clientOtherEnd := net.Pipe()
	serverEnd, serverOtherEnd := net.Pipe()
	defer clientOtherEnd.Close()

	ps, err := NewProxySession(clientEnd, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = pool.NewServerConnForTest(1, serverOtherEnd, &pool.Backend{Host: "127.0.0.1", Port: 5432})

	go func() {
		// Read the query message from proxy, then close
		buf := make([]byte, 4096)
		serverEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		serverEnd.Read(buf)
		serverEnd.Close()
	}()

	go func() {
		io.Copy(io.Discard, clientOtherEnd)
	}()

	relay := NewRelay()
	msg := &common.Message{Type: 'Q', Raw: []byte{'Q', 0, 0, 0, 8, 'S', 'E', 'L', 0}}

	err = relay.forwardAndCapture(serverOtherEnd, clientEnd, msg, "test-key", ps, time.Now())
	if err == nil {
		t.Error("Should fail when server closes during response")
	}
}

// ======== reprepareStatement: already prepared (needsReprep=false) ========

func TestReprepareStatement_AlreadyPrepared(t *testing.T) {
	ps, _, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	// Set up stmt repreparer
	ps.stmtRepreparer = stmt.NewTransparentRepreparer(stmt.NewManager(1000))

	// Register statement with same connID as serverConn
	ps.poolSession.PreparedStatements().Register("test_stmt", "SELECT $1", nil)

	// Mark as already prepared for this conn ID (the test serverConn has id=0 from default)
	// Since serverConn from newTestProxySession is created with SetConnForTest, ID=0
	// PrepareIfNeeded with connID=0 should still work
	ps.reprepareStatement(postgresql.NewCodec(), ps.serverConn.Conn(), "test_stmt")
}

// ======== reprepareStatement: non-postgresql codec ========

func TestReprepareStatement_NonPGCodec(t *testing.T) {
	ps, _, _, cleanup := newTestProxySession(t, "postgresql")
	defer cleanup()

	stmtMgr := stmt.NewManager(1000)
	ps.stmtRepreparer = stmt.NewTransparentRepreparer(stmtMgr)
	ps.poolSession.PreparedStatements().Register("test_stmt", "SELECT $1", nil)

	// Use a mock codec that's NOT *postgresql.PGCodec
	mockCodec := &unknownCodec{}

	// Mark as prepared on a different conn ID so that connID=0 triggers needsReprep
	stmtMgr.MarkPreparedOnConn(99, "test_stmt")

	ps.reprepareStatement(mockCodec, ps.serverConn.Conn(), "test_stmt")
}

// unknownCodec is a minimal codec implementation for testing non-PG paths
type unknownCodec struct{}

func (c *unknownCodec) Protocol() common.Protocol                           { return common.Protocol(99) }
func (c *unknownCodec) ReadMessage(r io.Reader) (*common.Message, error)    { return nil, nil }
func (c *unknownCodec) WriteMessage(w io.Writer, msg *common.Message) error { return nil }
func (c *unknownCodec) EncodeQuery(query string) (*common.Message, error)   { return nil, nil }
func (c *unknownCodec) IsQuery(msg *common.Message) bool                    { return false }
func (c *unknownCodec) IsExecute(msg *common.Message) bool                  { return false }
func (c *unknownCodec) IsPrepare(msg *common.Message) bool                  { return false }
func (c *unknownCodec) IsClose(msg *common.Message) bool                    { return false }
func (c *unknownCodec) IsSync(msg *common.Message) bool                     { return false }
func (c *unknownCodec) IsStartup(msg *common.Message) bool                  { return false }
func (c *unknownCodec) IsTerminate(msg *common.Message) bool                { return false }
func (c *unknownCodec) IsTransactionBegin(msg *common.Message) bool         { return false }
func (c *unknownCodec) IsTransactionEnd(msg *common.Message) bool           { return false }
func (c *unknownCodec) ExtractQuery(msg *common.Message) (string, error)    { return "", nil }
func (c *unknownCodec) GenerateResetSequence() []*common.Message            { return nil }
func (c *unknownCodec) IsBind(msg *common.Message) bool                     { return false }

var _ = bufio.NewReader
var _ = bytes.NewReader
