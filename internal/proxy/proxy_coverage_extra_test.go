package proxy

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
	"github.com/GeryonProxy/geryon/internal/protocol/common"
	"github.com/GeryonProxy/geryon/internal/protocol/postgresql"
	"github.com/GeryonProxy/geryon/internal/stmt"
)

// --- extractMySQLScramble edge cases ---

func TestExtractMySQLScramble_TooShort(t *testing.T) {
	_, err := extractMySQLScramble([]byte{1, 2, 3})
	if err == nil {
		t.Error("Should fail for data too short")
	}
}

func TestExtractMySQLScramble_BadProtoVersion(t *testing.T) {
	data := []byte{9, '5', '.', '7', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	_, err := extractMySQLScramble(data)
	if err == nil {
		t.Error("Should fail for unsupported protocol version")
	}
}

func TestExtractMySQLScramble_ValidShort(t *testing.T) {
	data := []byte{
		10, '5', '.', '7', 0, 1, 0, 0, 0,
		1, 2, 3, 4, 5, 6, 7, 8,
		0, 0xa6, 0x85, 255, 0x02, 0x00, 0x0f, 0x80,
		21, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}
	scramble, err := extractMySQLScramble(data)
	if err != nil {
		t.Fatalf("extractMySQLScramble failed: %v", err)
	}
	if len(scramble) != 20 {
		t.Errorf("scramble length = %d, want 20", len(scramble))
	}
}

func TestExtractMySQLScramble_ValidFull(t *testing.T) {
	data := []byte{
		10, '5', '.', '7', 0, 1, 0, 0, 0,
		1, 2, 3, 4, 5, 6, 7, 8,
		0, 0xa6, 0x85, 255, 0x02, 0x00, 0x0f, 0x80,
		21, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20,
	}
	scramble, err := extractMySQLScramble(data)
	if err != nil {
		t.Fatalf("extractMySQLScramble failed: %v", err)
	}
	if len(scramble) != 20 {
		t.Errorf("scramble length = %d, want 20", len(scramble))
	}
	for i := 0; i < 8; i++ {
		if scramble[i] != byte(i+1) {
			t.Errorf("scramble[%d] = %d, want %d", i, scramble[i], i+1)
		}
	}
}

func TestExtractMySQLScramble_ShortAuthData(t *testing.T) {
	data := []byte{
		10, '5', '.', '7', 0, 1, 0, 0, 0,
		1, 2, 3, 4, 5, 6, 7, 8,
		0, 0xa6, 0x85, 255, 0x02, 0x00, 0x0f, 0x80,
		5, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}
	scramble, err := extractMySQLScramble(data)
	if err != nil {
		t.Fatalf("extractMySQLScramble failed: %v", err)
	}
	if len(scramble) != 20 {
		t.Errorf("scramble length = %d, want 20", len(scramble))
	}
}

// --- createMySQLHandshake full scramble ---

func TestCreateMySQLHandshake_FullScramble(t *testing.T) {
	scramble := make([]byte, 20)
	for i := range scramble {
		scramble[i] = byte(i)
	}
	result := createMySQLHandshake(100, scramble)
	if len(result) == 0 {
		t.Error("Should produce output with full scramble")
	}
}

// --- parseMySQLHandshakeResponse ---

func TestParseMySQLHandshakeResponse_TooShort(t *testing.T) {
	_, _, err := parseMySQLHandshakeResponse([]byte{1, 2, 3})
	if err == nil {
		t.Error("Should fail for too-short response")
	}
}

func TestParseMySQLHandshakeResponse_BasicFields(t *testing.T) {
	data := make([]byte, 64)
	pos := 0
	binary.LittleEndian.PutUint32(data[pos:], 0x00000001)
	pos += 4
	binary.LittleEndian.PutUint32(data[pos:], 0x01000000)
	pos += 4
	data[pos] = 255
	pos++
	pos += 23
	copy(data[pos:], []byte("root"))
	pos += 4
	data[pos] = 0
	pos++
	data[pos] = 5
	pos++
	copy(data[pos:], []byte("auth!"))
	pos += 5
	copy(data[pos:], []byte("testdb"))
	pos += 6
	data[pos] = 0

	username, database, err := parseMySQLHandshakeResponse(data[:pos+1])
	if err != nil {
		t.Fatalf("parseMySQLHandshakeResponse failed: %v", err)
	}
	if username != "root" {
		t.Errorf("username = %q, want root", username)
	}
	if database != "testdb" {
		t.Errorf("database = %q, want testdb", database)
	}
}

// --- parseMemoryString edge cases ---

func TestParseMemoryString_Empty(t *testing.T) {
	if parseMemoryString("") != 64*1024*1024 {
		t.Error("Empty should return default 64MB")
	}
}

func TestParseMemoryString_ZeroValue(t *testing.T) {
	if parseMemoryString("0MB") != 64*1024*1024 {
		t.Error("Zero value should return default 64MB")
	}
}

func TestParseMemoryString_NoUnit(t *testing.T) {
	if parseMemoryString("128") != 128 {
		t.Error("No unit should use raw value")
	}
}

func TestParseMemoryString_InvalidUnit(t *testing.T) {
	// "10TB" - no GB/MB/KB suffix match, Sscanf parses "10" from "10TB", returns 10*1=10
	if parseMemoryString("10TB") != 10 {
		t.Errorf("Expected 10, got %d", parseMemoryString("10TB"))
	}
}

// --- parseTxnDuration edge cases ---

func TestParseTxnDuration_Empty(t *testing.T) {
	if parseTxnDuration("", 30*time.Minute) != 30*time.Minute {
		t.Error("Empty should return default")
	}
}

func TestParseTxnDuration_Valid(t *testing.T) {
	if parseTxnDuration("5s", 30*time.Minute) != 5*time.Second {
		t.Error("5s should parse to 5s")
	}
}

func TestParseTxnDuration_Invalid(t *testing.T) {
	if parseTxnDuration("notaduration", 30*time.Minute) != 30*time.Minute {
		t.Error("Invalid should return default")
	}
}

func TestParseTxnDuration_Minutes(t *testing.T) {
	if parseTxnDuration("10m", 0) != 10*time.Minute {
		t.Error("10m should parse to 10m")
	}
}

// --- handleConnection with max connections reached ---

func TestListener_handleConnection_MaxConns(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "maxconns-test-x",
		Listen: config.ListenConfig{Host: "127.0.0.1", Port: 0},
		Mode: "transaction", Body: "postgresql",
		Limits: config.LimitConfig{MaxClientConnections: 0},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm := pool.NewManager(log)
	pm.CreatePool(cfg)
	p := pm.GetPool("maxconns-test-x")
	codec := postgresql.NewCodec()
	listener, err := NewListener(p, cfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}
	client, _ := net.Pipe()
	defer client.Close()
	listener.handleConnection(client)
}

// --- OnQuery without router ---

func TestProxySession_OnQuery_NoRouter(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "no-router-x", Mode: "transaction", Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("no-router-x")
	client, _ := net.Pipe()
	defer client.Close()
	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)

	msg := &common.Message{Type: 'Q', Payload: []byte("INSERT INTO t VALUES (1)\x00")}
	_, err := ps.OnQuery(context.Background(), msg)
	if err != nil {
		t.Logf("OnQuery: %v (expected without backend)", err)
	}
	if ps.QueryCount() != 1 {
		t.Errorf("QueryCount = %d, want 1", ps.QueryCount())
	}
}

// --- reprepareStatement paths ---

func TestProxySession_reprepareStatement_EmptyName(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "reprep-empty-x", Mode: "transaction", Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("reprep-empty-x")
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	ps.reprepareStatement(codec, server, "") // Should return immediately
}

func TestProxySession_reprepareStatement_NoServerConn(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "reprep-nosrv-x", Mode: "transaction", Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("reprep-nosrv-x")
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	ps.reprepareStatement(codec, server, "stmt1")
}

func TestProxySession_reprepareStatement_WithStmt(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "reprep-stmt-x", Mode: "statement", Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("reprep-stmt-x")
	client, backend := net.Pipe()
	defer client.Close()
	defer backend.Close()

	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := backend.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)

	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(backend)
	ps.serverConn = serverConn

	ps.poolSession.PreparedStatements().Register("mystmt", "SELECT $1", nil)
	connID := serverConn.ID()
	ps.stmtRepreparer.PrepareIfNeeded(connID+1, "mystmt")

	ps.reprepareStatement(codec, serverConn.Conn(), "mystmt")
}

// --- forwardAuthFromBackend with immediate error ---

func TestProxySession_forwardAuthFromBackend_ReadError(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "fafbe-x", Mode: "transaction", Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("fafbe-x")

	client, _ := net.Pipe()
	defer client.Close()
	backendRead, backendWrite := net.Pipe()
	backendRead.Close()
	defer backendWrite.Close()

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(backendWrite)
	ps.serverConn = serverConn

	err := ps.forwardAuthFromBackend()
	if err == nil {
		t.Error("Should return error when backend closes immediately")
	}
}

// --- forwardAuthToBackend with read error ---

func TestProxySession_forwardAuthToBackend_ReadError(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "fatbe-x", Mode: "transaction", Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("fatbe-x")

	clientRead, clientWrite := net.Pipe()
	clientRead.Close()
	defer clientWrite.Close()

	_, backendWrite := net.Pipe()
	defer backendWrite.Close()

	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(clientWrite, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	serverConn := &pool.ServerConn{}
	serverConn.SetConnForTest(backendWrite)
	ps.serverConn = serverConn

	err := ps.forwardAuthToBackend()
	if err == nil {
		t.Error("Should return error when client is closed")
	}
}

// --- connectToBackend fails without backend ---

func TestProxySession_connectToBackend_NoBackend(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "ctb-x", Mode: "transaction", Body: "postgresql",
		Limits: config.LimitConfig{MaxServerConnections: 10},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("ctb-x")

	client, _ := net.Pipe()
	defer client.Close()
	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	ps.username = "testuser"

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := ps.connectToBackend(ctx)
	if err == nil {
		t.Error("Should fail without real backend")
	}
}

// --- handlePostgreSQLStartup bad proto version ---

func TestProxySession_handlePostgreSQLStartup_BadProtoVersion(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "bad-proto-x", Mode: "transaction", Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("bad-proto-x")

	client, server := net.Pipe()
	defer client.Close()
	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)

	go func() {
		msg := make([]byte, 12)
		binary.BigEndian.PutUint32(msg[0:4], 12)
		binary.BigEndian.PutUint32(msg[4:8], 12345)
		server.Write(msg)
		server.Close()
	}()

	err := ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail for bad protocol version")
	}
}

// --- handlePostgreSQLStartup with control characters ---

func TestProxySession_handlePostgreSQLStartup_ControlChars(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "ctrl-chars-x", Mode: "transaction", Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("ctrl-chars-x")

	client, server := net.Pipe()
	defer client.Close()

	userDB := auth.NewUserDatabase()
	userDB.AddUser(&auth.User{Username: "testuser"})
	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(client, p, codec, userDB, cfg, nil, nil, nil, nil, nil, nil, nil, log)

	go func() {
		params := []byte{'u', 's', 'e', 'r', 0, 't', 'e', 's', 't', 0x01, 'u', 's', 'e', 'r', 0, 0}
		length := 4 + 4 + len(params)
		msg := make([]byte, length)
		binary.BigEndian.PutUint32(msg[0:4], uint32(length))
		binary.BigEndian.PutUint32(msg[4:8], 196608)
		copy(msg[8:], params)
		server.Write(msg)
		server.Close()
	}()

	err := ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail for control characters in username")
	}
}

// --- handlePostgreSQLStartup too large ---

func TestProxySession_handlePostgreSQLStartup_TooLarge(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "too-large-x", Mode: "transaction", Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("too-large-x")

	client, server := net.Pipe()
	defer client.Close()
	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)

	go func() {
		msg := make([]byte, 4)
		binary.BigEndian.PutUint32(msg[0:4], 20000)
		server.Write(msg)
		server.Close()
	}()

	err := ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail for too-large startup message")
	}
}

// --- handlePostgreSQLStartup value too long ---

func TestProxySession_handlePostgreSQLStartup_ValueTooLong(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "val-long-x", Mode: "transaction", Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("val-long-x")

	client, server := net.Pipe()
	defer client.Close()
	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)

	go func() {
		longVal := make([]byte, 300)
		for i := range longVal {
			longVal[i] = 'x'
		}
		params := []byte{'u', 's', 'e', 'r', 0}
		params = append(params, longVal...)
		params = append(params, 0, 0)

		length := 4 + 4 + len(params)
		msg := make([]byte, length)
		binary.BigEndian.PutUint32(msg[0:4], uint32(length))
		binary.BigEndian.PutUint32(msg[4:8], 196608)
		copy(msg[8:], params)
		server.Write(msg)
		server.Close()
	}()

	err := ps.handlePostgreSQLStartup(context.Background())
	if err == nil {
		t.Error("Should fail for value exceeding max length")
	}
}

// --- SetDeadline with negative timeout ---

func TestSetDeadline_Negative(t *testing.T) {
	client, _ := net.Pipe()
	defer client.Close()
	SetDeadline(client, -1*time.Second)
}

// --- ProxySession ID increments ---

func TestProxySession_ID_Increments(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "id-incr-x", Mode: "transaction", Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("id-incr-x")

	c1, _ := net.Pipe()
	defer c1.Close()
	c2, _ := net.Pipe()
	defer c2.Close()

	codec := postgresql.NewCodec()
	ps1, _ := NewProxySession(c1, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)
	ps2, _ := NewProxySession(c2, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)

	if ps1.ID() >= ps2.ID() {
		t.Error("Session IDs should increment")
	}
}

// --- stmt.TransparentRepreparer is initialized ---

func TestProxySession_StmtRepreparer_Initialized(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)
	cfg := &config.PoolConfig{
		Name: "stmt-init-x", Mode: "transaction", Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)
	p := pm.GetPool("stmt-init-x")

	client, _ := net.Pipe()
	defer client.Close()
	codec := postgresql.NewCodec()
	ps, _ := NewProxySession(client, p, codec, nil, cfg, nil, nil, nil, nil, nil, nil, nil, log)

	if ps.stmtRepreparer == nil {
		t.Error("stmtRepreparer should be initialized")
	}
	_ = ps.stmtRepreparer
}

// Ensure imports are used
var _ = (*stmt.TransparentRepreparer)(nil)
var _ = (*auth.User)(nil)
var _ = binary.BigEndian
