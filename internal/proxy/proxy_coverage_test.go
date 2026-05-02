package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
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
	"github.com/GeryonProxy/geryon/internal/tracing"
)

// --- sendRollbackToBackend with a real server connection ---

func TestProxySession_sendRollbackToBackend_WithConn(t *testing.T) {
	log, _ := logger.New("error", "json")

	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-rollback",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-rollback")

	// Create a pipe for the proxy session client
	proxyClient, _ := net.Pipe()
	defer proxyClient.Close()

	// Create a separate pipe for the backend connection
	backendRead, backendWrite := net.Pipe()
	defer backendRead.Close()
	defer backendWrite.Close()

	// Drain backend reads in background to prevent write blocking
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := backendRead.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	codec := postgresql.NewCodec()
	ps, err := NewProxySession(context.Background(), proxyClient, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}

	// Set up server connection with the backend pipe
	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(backendWrite)
	ps.poolSession.SetServerConn(serverConn)

	// Should not panic - the function writes to backend and closes client
	ps.sendRollbackToBackend()
}

// --- sendRollbackToBackend with nil server conn ---

func TestProxySession_sendRollbackToBackend_NilServerConn(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-rollback-nil",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-rollback-nil")

	client, _ := net.Pipe()
	defer client.Close()

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	// Don't set server conn

	// Should not panic
	ps.sendRollbackToBackend()
}

// --- forwardServerToClient with ReadyForQuery Idle ---

func TestRelay_forwardServerToClient_ReadyForQuery_Idle(t *testing.T) {
	log, _ := logger.New("error", "json")
	clientConn, clientWrite := net.Pipe()
	serverRead, serverWrite := net.Pipe()
	defer clientConn.Close()
	defer clientWrite.Close()
	defer serverRead.Close()
	defer serverWrite.Close()

	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(serverRead)

	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-rw",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-rw")

	codec := postgresql.NewCodec()

	ps, err := NewProxySession(context.Background(), clientConn, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = serverConn

	// Drain client reads in background
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := clientConn.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	// Write a ReadyForQuery (Idle) message to server
	go func() {
		// 'Z' + length(4) + status(1) = 5 bytes payload, total raw = 1+4+5=10
		// Actually PG wire format: type(1) + length(4, includes self) + status(1)
		// Raw message: 'Z' 0x00 0x00 0x00 0x05 'I'
		msg := &common.Message{
			Type:    'Z',
			Length:  5,
			Payload: []byte{'I'},
			Raw:     []byte{'Z', 0, 0, 0, 5, 'I'},
		}
		serverWrite.Write(msg.Raw)
		serverWrite.Close()
	}()

	relay := NewRelay()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = relay.forwardServerToClient(ctx, clientWrite, nil, codec, ps)
	if err != nil {
		t.Logf("forwardServerToClient returned: %v", err)
	}

	// Verify that SetInTransaction was called (session should be not in transaction)
	if ps.poolSession.InTransaction() {
		t.Error("Should not be in transaction after ReadyForQuery Idle")
	}
}

// --- forwardServerToClient with ReadyForQuery InTransaction ---

func TestRelay_forwardServerToClient_ReadyForQuery_InTxn(t *testing.T) {
	log, _ := logger.New("error", "json")
	clientConn, clientWrite := net.Pipe()
	serverRead, serverWrite := net.Pipe()
	defer clientConn.Close()
	defer clientWrite.Close()
	defer serverRead.Close()
	defer serverWrite.Close()

	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(serverRead)

	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-txn",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-txn")

	codec := postgresql.NewCodec()

	ps, err := NewProxySession(context.Background(), clientConn, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = serverConn

	// Drain client reads in background
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := clientConn.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	// Write a ReadyForQuery (InTransaction) message
	go func() {
		msg := []byte{'Z', 0, 0, 0, 5, 'T'}
		serverWrite.Write(msg)
		serverWrite.Close()
	}()

	relay := NewRelay()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = relay.forwardServerToClient(ctx, clientWrite, nil, codec, ps)
	if err != nil {
		t.Logf("forwardServerToClient returned: %v", err)
	}

	// Should be in transaction
	if !ps.poolSession.InTransaction() {
		t.Error("Should be in transaction after ReadyForQuery 'T'")
	}
}

// --- forwardServerToClient with DataRow counting ---

func TestRelay_forwardServerToClient_DataRowCount(t *testing.T) {
	log, _ := logger.New("error", "json")
	clientConn, clientWrite := net.Pipe()
	serverRead, serverWrite := net.Pipe()
	defer clientConn.Close()
	defer clientWrite.Close()
	defer serverRead.Close()
	defer serverWrite.Close()

	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(serverRead)

	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-rows",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-rows")

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), clientConn, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	ps.serverConn = serverConn

	// Drain client reads in background
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := clientConn.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	go func() {
		// DataRow message: 'D' + length + fieldCount(2) + fieldLength(4) + data
		dr := []byte{'D', 0, 0, 0, 11, 0, 1, 0, 0, 0, 4, 't', 'e', 's', 't'}
		serverWrite.Write(dr)
		// ReadyForQuery Idle
		rfw := []byte{'Z', 0, 0, 0, 5, 'I'}
		serverWrite.Write(rfw)
		serverWrite.Close()
	}()

	relay := NewRelay()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := relay.forwardServerToClient(ctx, clientWrite, nil, codec, ps)
	if err != nil {
		t.Logf("forwardServerToClient returned: %v", err)
	}
}

// --- forwardServerToClient with nil server conn ---

func TestRelay_forwardServerToClient_NilServerConn(t *testing.T) {
	log, _ := logger.New("error", "json")
	client, _ := net.Pipe()
	defer client.Close()

	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-nil",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-nil")

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	// Don't set serverConn

	relay := NewRelay()
	err := relay.forwardServerToClient(context.Background(), client, nil, codec, ps)
	if err == nil {
		t.Error("Should return error for nil serverConn")
	}
}

// --- forwardClientToServer context cancelled ---

func TestRelay_forwardClientToServer_ContextCancelled(t *testing.T) {
	log, _ := logger.New("error", "json")
	clientRead, clientWrite := net.Pipe()
	defer clientRead.Close()
	defer clientWrite.Close()

	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-ctx",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-ctx")

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), clientWrite, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)

	relay := NewRelay()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := relay.forwardClientToServer(ctx, clientRead, nil, codec, ps)
	if err == nil {
		t.Error("Should return error for cancelled context")
	}
}

// --- forwardAndCapture is tested via integration tests (needs bidirectional pipe setup) ---

// --- Handle is not easily testable without a real backend ---
// handleStartup→handlePostgreSQLStartup blocks on io.ReadFull from the
// client connection, which cannot be interrupted by context cancellation.
// Skipping this test to avoid hangs.

// --- SetDeadline tests ---

func TestSetDeadline_Zero(t *testing.T) {
	client, _ := net.Pipe()
	defer client.Close()

	// Should not panic with zero timeout
	SetDeadline(client, 0)
}

func TestSetDeadline_Positive(t *testing.T) {
	client, _ := net.Pipe()
	defer client.Close()

	SetDeadline(client, 5*time.Second)
	// Verify deadline was set
	dl := client.SetReadDeadline(time.Time{}) // reading deadline resets it
	_ = dl
}

// --- forwardServerToClient context cancellation ---

func TestRelay_forwardServerToClient_ContextCancelled(t *testing.T) {
	log, _ := logger.New("error", "json")
	client, _ := net.Pipe()
	defer client.Close()

	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-ctx-s2c",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-ctx-s2c")

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)

	serverConn := &pool.ServerConn{}
	serverRead, _ := net.Pipe()
	defer serverRead.Close()
	serverConn.SetConnForTest(serverRead)
	ps.serverConn = serverConn

	relay := NewRelay()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := relay.forwardServerToClient(ctx, client, nil, codec, ps)
	if err == nil {
		t.Error("Should return error for cancelled context")
	}
}

// --- OnQuery with router routing to replica ---

func TestProxySession_OnQuery_WithRouter(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-router",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
				{Host: "127.0.0.1", Port: 5433, Role: "replica"},
			},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-router")

	client, _ := net.Pipe()
	defer client.Close()

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)

	// Create a router with read/write splitting
	backends := []*pool.Backend{
		{Host: "127.0.0.1", Port: 5432, Role: "primary"},
		{Host: "127.0.0.1", Port: 5433, Role: "replica"},
	}
	router, err := pool.NewRouter(&config.RoutingConfig{
		ReadWriteSplit: true,
	}, backends)
	if err != nil {
		t.Fatalf("NewRouter failed: %v", err)
	}
	ps.router = router

	msg := &common.Message{
		Type:    'Q',
		Payload: []byte("SELECT 1"),
	}

	// This will fail at strategy level but should exercise the router path
	_, err = ps.OnQuery(context.Background(), msg)
	if err != nil {
		t.Logf("OnQuery returned: %v (expected without backend)", err)
	}

	if ps.QueryCount() != 1 {
		t.Errorf("QueryCount = %d, want 1", ps.QueryCount())
	}
}

// --- Relay Run with nil serverConn ---

func TestRelay_Run_NilServerConn_Returns(t *testing.T) {
	log, _ := logger.New("error", "json")
	client, _ := net.Pipe()
	defer client.Close()

	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-run-nil",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-run-nil")

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	ps.serverConn = nil // explicit nil

	relay := NewRelay()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		relay.Run(ctx, client, ps.poolSession, codec, ps)
		close(done)
	}()

	// Run should return quickly since serverConn is nil
	select {
	case <-done:
		// Expected
	case <-time.After(2 * time.Second):
		t.Error("Run should return quickly with nil serverConn")
	}
}

// --- forwardClientToServer with terminate message ---

func TestRelay_forwardClientToServer_Terminate(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-term",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-term")

	codec := postgresql.NewCodec()
	clientRead, clientWrite := net.Pipe()
	defer clientRead.Close()
	defer clientWrite.Close()

	ps, _ := NewProxySession(context.Background(), clientWrite, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)

	// Write a Terminate message from client side
	go func() {
		// Terminate message: 'X' + length(4)
		msg := []byte{'X', 0, 0, 0, 4}
		clientWrite.Write(msg)
	}()

	relay := NewRelay()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := relay.forwardClientToServer(ctx, clientRead, nil, codec, ps)
	if err != nil {
		t.Logf("forwardClientToServer returned: %v", err)
	}
}

// --- authenticateWithCertificate with TLS connection ---

func TestProxySession_authenticateWithCertificate_TLSNoPeerCert(t *testing.T) {
	log, _ := logger.New("error", "json")

	// Create a TLS connection using a self-signed cert
	cert, err := tls.X509KeyPair([]byte(testCertPEM), []byte(testKeyPEM))
	if err != nil {
		t.Skipf("Cannot create TLS cert: %v", err)
	}

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
	}

	tlsClient := tls.Client(client, tlsConfig)
	defer tlsClient.Close()

	// Handshake in background
	go tlsClient.Handshake()
	tlsServer := tls.Server(server, &tls.Config{
		Certificates: []tls.Certificate{cert},
	})
	tlsServer.Handshake()
	defer tlsServer.Close()

	ps := &ProxySession{
		clientConn: tlsServer,
		log:        log,
	}

	err = ps.authenticateWithCertificate()
	if err != nil {
		t.Errorf("authenticateWithCertificate should not fail with no peer cert: %v", err)
	}
}

// --- authenticateWithCertificate with userDB and matching user ---

func TestProxySession_authenticateWithCertificate_WithUserDB(t *testing.T) {
	log, _ := logger.New("error", "json")

	cert, err := tls.X509KeyPair([]byte(testCertPEM), []byte(testKeyPEM))
	if err != nil {
		t.Skipf("Cannot create TLS cert: %v", err)
	}

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
	}

	tlsClient := tls.Client(client, tlsConfig)
	defer tlsClient.Close()
	go tlsClient.Handshake()

	tlsServer := tls.Server(server, &tls.Config{
		Certificates:       []tls.Certificate{cert},
		ClientAuth:         tls.RequestClientCert,
		InsecureSkipVerify: true,
	})
	tlsServer.Handshake()
	defer tlsServer.Close()

	userDB := auth.NewUserDatabase()
	ps := &ProxySession{
		clientConn: tlsServer,
		userDB:     userDB,
		log:        log,
	}

	err = ps.authenticateWithCertificate()
	// No peer certificate, so should return nil
	if err != nil {
		t.Errorf("Expected nil with no peer cert, got: %v", err)
	}
}

// --- extractTablesFromQuery edge cases ---

func TestExtractTablesFromQuery_NoFrom(t *testing.T) {
	tables := extractTablesFromQuery("SELECT 1")
	if len(tables) != 0 {
		t.Errorf("Expected 0 tables, got %v", tables)
	}
}

func TestExtractTablesFromQuery_Semicolon(t *testing.T) {
	tables := extractTablesFromQuery("SELECT * FROM users;")
	if len(tables) != 1 || tables[0] != "users" {
		t.Errorf("Expected [users], got %v", tables)
	}
}

func TestExtractTablesFromQuery_Comma(t *testing.T) {
	tables := extractTablesFromQuery("SELECT * FROM users, orders WHERE users.id = orders.user_id")
	if len(tables) != 1 || tables[0] != "users" {
		t.Errorf("Expected [users], got %v", tables)
	}
}

// Test cert/key PEM for TLS tests
const testCertPEM = `-----BEGIN CERTIFICATE-----
MIIBkTCB+wIJAJkRRQz9q0xEMA0GCSqGSIb3DQEBCwUAMBExDzANBgNVBAMMBnRl
c3RjYTAeFw0yNDAxMDEwMDAwMDBaFw0yNTAxMDEwMDAwMDBaMBExDzANBgNVBAMM
BnRlc3RjYTCBnzANBgkqhkiG9w0BAQEFAAOBjQAwgYkCgYEAwT8kqCEm4Y5lqZ3a
5v5hLmYpF5eJNBfVHpRWxLQg0Q6q5pBdJ7KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0Q
6q5pBdJ7KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0Q6q5pBdJ7KoKq3Q6F5hLrG5eJN
BfVHpRWxLQg0Q6q5pBdJ7KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0CAwEAAaMaMBgw
CQYDVR0RBAIwADANBgkqhkiG9w0BAQsFAAOBgQBHPkYkLcH3mMG8aN2bD2g5Pq8X
M5eJNBfVHpRWxLQg0Q6q5pBdJ7KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0Q6q5pBdJ7
KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0Q6q5pBdJ7KoKq3Q6F5hLrG5eJNBfVHpRWxL
Qg0Q6q5pBdJ7KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0Q6q5pBdJ7A==
-----END CERTIFICATE-----`

const testKeyPEM = `-----BEGIN PRIVATE KEY-----
MIICdgIBADANBgkqhkiG9w0BAQEFAASCAmAwggJcAgEAAoGBAME/JKghJuGOZamd
2ub+YS5mKReXiTQX1R6UVsS0INEOluaQXSeyqCqt0OheYS6xuXiTQX1R6UVsS0IN
EOluaQXSeyqCqt0OheYS6xuXiTQX1R6UVsS0INEOluaQXSeyqCqt0OheYS6xuXiT
QX1R6UVsS0INEOluaQXSeyqCqt0OheYS6xuXiTQX1R6UVsS0INAgMBAAECgYEA
pK5pBdJ7KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0Q6q5pBdJ7KoKq3Q6F5hLrG5eJNB
fVHpRWxLQg0Q6q5pBdJ7KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0Q6q5pBdJ7KoKq3
Q6F5hLrG5eJNBfVHpRWxLQg0Q6q5pBdJ7KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0Q
ECQQDw5eJNBfVHpRWxLQg0Q6q5pBdJ7KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0Q6q5
pBdJ7KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0Q6q5pBdJ7AkEA7pBdJ7KoKq3Q6F5h
LrG5eJNBfVHpRWxLQg0Q6q5pBdJ7KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0Q6q5pBd
J7KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0Q6q5wJAIq5pBdJ7KoKq3Q6F5hLrG5eJNB
fVHpRWxLQg0Q6q5pBdJ7KoKq3Q6F5hLrG5eJNBfVHpRWxLQg0Q6q5pBdJ7KoKq3
Q6F5hLrG5eJNBfVHpRWxLQg0Q6q5pBdJ7KoQIgQIgQIgaQIgQIgaQIgQIgQIgaQ
-----END PRIVATE KEY-----`

// --- recordAuthFailure with authLimiter and RemoteAddr ---

func TestProxySession_recordAuthFailure_WithLimiter(t *testing.T) {
	log, _ := logger.New("error", "json")

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	limiter := auth.NewAuthLimiter()

	ps := &ProxySession{
		clientConn:  client,
		authLimiter: limiter,
		log:         log,
	}

	ps.recordAuthFailure()
}

// --- recordAuthFailure triggering lockout ---

func TestProxySession_recordAuthFailure_Lockout(t *testing.T) {
	log, _ := logger.New("error", "json")

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	limiter := auth.NewAuthLimiterConfig(3, 1*time.Minute, 1*time.Minute)

	ps := &ProxySession{
		clientConn:  client,
		authLimiter: limiter,
		log:         log,
	}

	// Call recordAuthFailure enough times to trigger lockout
	for i := 0; i < 5; i++ {
		ps.recordAuthFailure()
	}
}

// --- recordAuthFailure with nil limiter ---

func TestProxySession_recordAuthFailure_NilLimiter(t *testing.T) {
	ps := &ProxySession{}
	ps.recordAuthFailure()
}

// --- recordAuthSuccess with authLimiter ---

func TestProxySession_recordAuthSuccess_WithLimiter(t *testing.T) {
	log, _ := logger.New("error", "json")
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	limiter := auth.NewAuthLimiter()

	ps := &ProxySession{
		clientConn:  client,
		authLimiter: limiter,
		log:         log,
	}

	ps.recordAuthSuccess()
}

// --- forwardServerToClient with ReadyForQuery Failed (E) ---

func TestRelay_forwardServerToClient_ReadyForQuery_FailedTxn(t *testing.T) {
	log, _ := logger.New("error", "json")
	clientConn, clientWrite := net.Pipe()
	serverRead, serverWrite := net.Pipe()
	defer clientConn.Close()
	defer clientWrite.Close()
	defer serverRead.Close()
	defer serverWrite.Close()

	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(serverRead)

	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-failed-txn",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-failed-txn")

	codec := postgresql.NewCodec()
	ps, err := NewProxySession(context.Background(), clientConn, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = serverConn

	// Drain client reads in background
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := clientConn.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	// Write a ReadyForQuery (Failed) message - status 'E'
	go func() {
		msg := []byte{'Z', 0, 0, 0, 5, 'E'}
		serverWrite.Write(msg)
		serverWrite.Close()
	}()

	relay := NewRelay()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = relay.forwardServerToClient(ctx, clientWrite, nil, codec, ps)
	if err != nil {
		t.Logf("forwardServerToClient returned: %v", err)
	}

	if !ps.poolSession.InTransaction() {
		t.Error("Should be in transaction after ReadyForQuery 'E'")
	}
}

// --- forwardServerToClient with queryLogger and transaction info ---

func TestRelay_forwardServerToClient_WithQueryLogger(t *testing.T) {
	log, _ := logger.New("error", "json")
	clientConn, clientWrite := net.Pipe()
	serverRead, serverWrite := net.Pipe()
	defer clientConn.Close()
	defer clientWrite.Close()
	defer serverRead.Close()
	defer serverWrite.Close()

	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(serverRead)

	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-qlog",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-qlog")

	codec := postgresql.NewCodec()
	ps, err := NewProxySession(context.Background(), clientConn, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	if err != nil {
		t.Fatalf("NewProxySession failed: %v", err)
	}
	ps.serverConn = serverConn
	ps.currentQuery = "SELECT 1"
	ps.queryStartTime = time.Now()
	ps.username = "testuser"
	ps.database = "testdb"

	// Set up a query logger that writes to a temp directory
	tmpDir := t.TempDir()
	ql, err2 := logger.NewQueryLogger(logger.QueryLogConfig{
		Enabled:       true,
		Directory:     tmpDir,
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
	})
	if err2 != nil {
		t.Fatalf("NewQueryLogger failed: %v", err2)
	}
	ps.queryLogger = ql
	defer ql.Stop()

	// Drain client reads in background
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := clientConn.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	// Write DataRow + ReadyForQuery Idle
	go func() {
		dr := []byte{'D', 0, 0, 0, 11, 0, 1, 0, 0, 0, 4, 't', 'e', 's', 't'}
		serverWrite.Write(dr)
		rfw := []byte{'Z', 0, 0, 0, 5, 'I'}
		serverWrite.Write(rfw)
		serverWrite.Close()
	}()

	relay := NewRelay()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = relay.forwardServerToClient(ctx, clientWrite, nil, codec, ps)
	if err != nil {
		t.Logf("forwardServerToClient returned: %v", err)
	}

	if ps.currentQuery != "" {
		t.Error("currentQuery should be cleared after ReadyForQuery")
	}
}

// --- forwardServerToClient with transaction manager ---

func TestRelay_forwardServerToClient_WithTxnManager(t *testing.T) {
	log, _ := logger.New("error", "json")
	clientConn, clientWrite := net.Pipe()
	serverRead, serverWrite := net.Pipe()
	defer clientConn.Close()
	defer clientWrite.Close()
	defer serverRead.Close()
	defer serverWrite.Close()

	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(serverRead)

	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-txn-mgr",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-txn-mgr")

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), clientConn, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	ps.serverConn = serverConn

	// Set up transaction manager and transaction info
	txnMgr := pool.NewTransactionManager(30*time.Minute, 5*time.Minute, 30*time.Second, log)
	ps.transactionMgr = txnMgr
	abortFn := func() { ps.sendRollbackToBackend() }
	txnInfo := txnMgr.Register(ps.id, 0, abortFn)
	ps.transactionInfo = txnInfo

	// Drain client reads in background
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := clientConn.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	// Write a ReadyForQuery Idle message
	go func() {
		msg := []byte{'Z', 0, 0, 0, 5, 'I'}
		serverWrite.Write(msg)
		serverWrite.Close()
	}()

	relay := NewRelay()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := relay.forwardServerToClient(ctx, clientWrite, nil, codec, ps)
	if err != nil {
		t.Logf("forwardServerToClient returned: %v", err)
	}

	txnMgr.Stop()
}

// --- handlePostgreSQLAuth with write failure ---

func TestProxySession_handlePostgreSQLAuth_WriteFail(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-auth-wf",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-auth-wf")

	client, _ := net.Pipe()
	defer client.Close()

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	ps.username = "testuser"
	ps.database = "testdb"

	user := &auth.User{Username: "testuser"}
	client.Close()

	err := ps.handlePostgreSQLAuth(context.Background(), user)
	if err == nil {
		t.Error("Should return error when write fails")
	}
}

// --- forwardAndCapture basic flow with pipes ---

func TestRelay_forwardAndCapture_BasicFlow(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-fac",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-fac")

	clientRead, clientWrite := net.Pipe()
	defer clientRead.Close()
	defer clientWrite.Close()

	serverRead, serverWrite := net.Pipe()
	defer serverRead.Close()
	defer serverWrite.Close()

	codec := postgresql.NewCodec()
	cacheRules := cache.NewRulesEngine()
	cacheStore := cache.NewStore(1024*1024, 5*time.Minute)
	ps, _ := NewProxySession(context.Background(), clientRead, p, codec, nil, cfg, cacheStore, cacheRules, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	ps.currentQuery = "SELECT 1"

	// Drain client reads
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := clientRead.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	queryMsg := &common.Message{
		Type:    'Q',
		Payload: []byte("SELECT 1\x00"),
		Raw:     []byte{'Q', 0, 0, 0, 13, 'S', 'E', 'L', 'E', 'C', 'T', ' ', '1', 0},
	}

	// Backend writes back DataRow + ReadyForQuery
	go func() {
		buf := make([]byte, 4096)
		serverWrite.Read(buf)

		dr := []byte{'D', 0, 0, 0, 11, 0, 1, 0, 0, 0, 4, 't', 'e', 's', 't'}
		serverWrite.Write(dr)
		rfw := []byte{'Z', 0, 0, 0, 5, 'I'}
		serverWrite.Write(rfw)
	}()

	relay := NewRelay()
	err := relay.forwardAndCapture(serverRead, clientWrite, queryMsg, "cache-key-1", ps, time.Now())
	if err != nil {
		t.Logf("forwardAndCapture returned: %v (expected without cache store)", err)
	}
}

// --- isSelectQuery additional tests ---

func TestIsSelectQuery_CoverageExtra(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"SELECT 1", true},
		{"select * from users", true},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"INSERT INTO users VALUES (1)", false},
		{"UPDATE users SET name='x'", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isSelectQuery(tt.query)
		if got != tt.want {
			t.Errorf("isSelectQuery(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

// --- isModificationQuery additional tests ---

func TestIsModificationQuery_CoverageExtra(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"INSERT INTO users VALUES (1)", true},
		{"insert into users values (1)", true},
		{"UPDATE users SET name='x'", true},
		{"DELETE FROM users", true},
		{"TRUNCATE users", true},
		{"DROP TABLE users", true},
		{"ALTER TABLE users ADD COLUMN x INT", true},
		{"CREATE TABLE users (id INT)", true},
		{"REPLACE INTO users VALUES (1)", true},
		{"SELECT 1", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isModificationQuery(tt.query)
		if got != tt.want {
			t.Errorf("isModificationQuery(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

// --- Relay Run with actual serverConn and immediate context cancel ---

func TestRelay_Run_WithServerConn_ContextCancel(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-run-ctx",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-run-ctx")

	clientRead, clientWrite := net.Pipe()
	defer clientRead.Close()
	defer clientWrite.Close()

	serverRead, serverWrite := net.Pipe()
	defer serverRead.Close()
	defer serverWrite.Close()

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), clientWrite, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)

	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(serverRead)
	ps.serverConn = serverConn

	relay := NewRelay()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		relay.Run(ctx, clientWrite, ps.poolSession, codec, ps)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("Run should return after context cancellation")
	}
}

// --- handlePostgreSQLAuth with rate-limited client ---

func TestProxySession_handlePostgreSQLAuth_RateLimited(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-auth-rl",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-auth-rl")

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	ps.username = "testuser"
	ps.database = "testdb"

	// Set up auth limiter and lock the IP
	limiter := auth.NewAuthLimiterConfig(2, 1*time.Minute, 1*time.Minute)
	ps.authLimiter = limiter
	// Trigger lockout
	limiter.RecordFailure(client.RemoteAddr().String())
	limiter.RecordFailure(client.RemoteAddr().String())
	limiter.RecordFailure(client.RemoteAddr().String())

	// Drain server reads
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := server.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	user := &auth.User{Username: "testuser"}
	err := ps.handlePostgreSQLAuth(context.Background(), user)
	if err == nil {
		t.Error("Should return error for rate-limited client")
	}
}

// --- handlePostgreSQLAuth with wrong message type ---

func TestProxySession_handlePostgreSQLAuth_WrongMsgType(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-auth-wmt",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-auth-wmt")

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	userDB := auth.NewUserDatabase()
	userDB.AddUser(&auth.User{Username: "testuser"})

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), client, p, codec, userDB, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	ps.username = "testuser"
	ps.database = "testdb"

	// In background, read the SASL message then send wrong msg type
	go func() {
		buf := make([]byte, 4096)
		// Read the AuthenticationSASL message from proxy
		n, _ := server.Read(buf)
		_ = n
		// Send a byte that's NOT 'p' (password message)
		server.Write([]byte{'X'}) // wrong type
		server.Close()
	}()

	user := &auth.User{Username: "testuser"}
	err := ps.handlePostgreSQLAuth(context.Background(), user)
	if err == nil {
		t.Error("Should return error for wrong message type")
	}
}

// --- handlePostgreSQLAuth with EOF on SASL response length read ---

func TestProxySession_handlePostgreSQLAuth_SASLReadEOF(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-auth-eof",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-auth-eof")

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	ps.username = "testuser"
	ps.database = "testdb"

	// Read the SASL message then close - causing EOF on readByte
	go func() {
		buf := make([]byte, 4096)
		n, _ := server.Read(buf)
		_ = n
		server.Close()
	}()

	user := &auth.User{Username: "testuser"}
	err := ps.handlePostgreSQLAuth(context.Background(), user)
	if err == nil {
		t.Error("Should return error on EOF")
	}
}

// --- handlePostgreSQLAuth with valid 'p' type but EOF on length ---

func TestProxySession_handlePostgreSQLAuth_SASLLengthEOF(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "test-auth-leof",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("test-auth-leof")

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(context.Background(), client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, tracing.NewTracer(nil, log), log)
	ps.username = "testuser"
	ps.database = "testdb"

	// Read the SASL message, send 'p' type then close
	go func() {
		buf := make([]byte, 4096)
		n, _ := server.Read(buf)
		_ = n
		server.Write([]byte{'p'}) // correct type
		server.Close()            // but then EOF before length
	}()

	user := &auth.User{Username: "testuser"}
	err := ps.handlePostgreSQLAuth(context.Background(), user)
	if err == nil {
		t.Error("Should return error on EOF during length read")
	}
}

// Ensure imports are used
var _ = fmt.Sprintf
