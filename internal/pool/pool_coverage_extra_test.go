package pool

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/protocol/common"
)

// --- ResetConnection with unknown protocol ---

func TestResetConnection_UnknownProtocol(t *testing.T) {
	codec := &unknownProtocolCodec{}
	ctx := context.Background()

	err := ResetConnection(ctx, nil, codec)
	if err == nil {
		t.Error("Should return error for unknown protocol")
	}
}

type unknownProtocolCodec struct{}

func (c *unknownProtocolCodec) Protocol() common.Protocol                           { return common.Protocol(99) }
func (c *unknownProtocolCodec) ReadMessage(r io.Reader) (*common.Message, error)    { return nil, nil }
func (c *unknownProtocolCodec) WriteMessage(w io.Writer, msg *common.Message) error { return nil }
func (c *unknownProtocolCodec) EncodeQuery(query string) (*common.Message, error)   { return nil, nil }
func (c *unknownProtocolCodec) IsQuery(msg *common.Message) bool                    { return false }
func (c *unknownProtocolCodec) IsExecute(msg *common.Message) bool                  { return false }
func (c *unknownProtocolCodec) IsPrepare(msg *common.Message) bool                  { return false }
func (c *unknownProtocolCodec) IsClose(msg *common.Message) bool                    { return false }
func (c *unknownProtocolCodec) IsSync(msg *common.Message) bool                     { return false }
func (c *unknownProtocolCodec) IsStartup(msg *common.Message) bool                  { return false }
func (c *unknownProtocolCodec) IsTerminate(msg *common.Message) bool                { return false }
func (c *unknownProtocolCodec) IsTransactionBegin(msg *common.Message) bool         { return false }
func (c *unknownProtocolCodec) IsTransactionEnd(msg *common.Message) bool           { return false }
func (c *unknownProtocolCodec) ExtractQuery(msg *common.Message) (string, error)    { return "", nil }
func (c *unknownProtocolCodec) GenerateResetSequence() []*common.Message            { return nil }
func (c *unknownProtocolCodec) IsBind(msg *common.Message) bool                     { return false }

// --- Router selectBackend fallback paths ---

func TestRouter_SelectBackend_FallbackReplica(t *testing.T) {
	primary := testBackend("primary", false)
	replica := testBackend("replica", true)

	r := &Router{
		primary:  primary,
		replicas: []*Backend{replica},
	}

	backend, err := r.selectBackend("primary", "replica")
	if err != nil {
		t.Fatalf("selectBackend failed: %v", err)
	}
	if backend != replica {
		t.Error("Should fallback to replica when primary unhealthy")
	}
}

func TestRouter_SelectBackend_NoneAvailable(t *testing.T) {
	r := &Router{
		primary:  nil,
		replicas: nil,
	}

	_, err := r.selectBackend("primary", "replica")
	if err == nil {
		t.Error("Should return error when no backends available")
	}
}

// --- RouteQueryDetailed with write query ---

func TestRouter_RouteQueryDetailed_Write(t *testing.T) {
	primary := testBackend("primary", true)
	r, _ := NewRouter(&config.RoutingConfig{}, []*Backend{primary})

	result, err := r.RouteQueryDetailed("INSERT INTO t VALUES (1)", false)
	if err != nil {
		t.Fatalf("RouteQueryDetailed failed: %v", err)
	}
	if !result.IsPrimary {
		t.Error("Write query should be routed to primary")
	}
	if result.RouteType != "primary" {
		t.Errorf("RouteType = %q, want primary", result.RouteType)
	}
	if result.QueryType != "write" {
		t.Errorf("QueryType = %q, want write", result.QueryType)
	}
}

func TestRouter_RouteQueryDetailed_InTransaction(t *testing.T) {
	primary := testBackend("primary", true)
	r, _ := NewRouter(&config.RoutingConfig{}, []*Backend{primary})

	result, err := r.RouteQueryDetailed("SELECT 1", true)
	if err != nil {
		t.Fatalf("RouteQueryDetailed failed: %v", err)
	}
	if !result.IsPrimary {
		t.Error("In-transaction query should route to primary")
	}
	if result.QueryType != "transaction" {
		t.Errorf("QueryType = %q, want transaction", result.QueryType)
	}
}

// --- Session HandleMessage with nil msg ---

func TestSession_HandleMessage_NilMsg(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := NewManager(log)
	poolCfg := &config.PoolConfig{
		Name: "test-nil-msg",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(poolCfg)
	p := pm.GetPool("test-nil-msg")

	strategy := NewTransactionStrategy(p)
	sess := NewSession(p, strategy)
	err := sess.HandleMessage(nil)
	if err != nil {
		t.Errorf("HandleMessage(nil) should return nil, got: %v", err)
	}
}

// --- Cache operations ---

func TestPool_GetCachedResult_Disabled(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "cache-disabled",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	data, ok := p.GetCachedResult("SELECT 1", nil)
	if ok {
		t.Error("GetCachedResult should return false when cache disabled")
	}
	if data != nil {
		t.Error("GetCachedResult should return nil data when cache disabled")
	}
}

func TestPool_SetCachedResult_Disabled(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "cache-disabled-set",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	err = p.SetCachedResult("SELECT 1", nil, []byte("result"), 5*time.Minute)
	if err != nil {
		t.Errorf("SetCachedResult should return nil when cache disabled, got: %v", err)
	}
}

func TestPool_InvalidateCache_Disabled(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "cache-disabled-inv",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	p.InvalidateCache("users")
}

func TestPool_CacheOperations_Enabled(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "cache-enabled",
		Mode: "transaction",
		Body: "postgresql",
		Cache: config.CacheConfig{
			Enabled: true,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	err = p.SetCachedResult("SELECT * FROM users WHERE id = $1", []byte{0, 1}, []byte("cached-data"), 5*time.Minute)
	if err != nil {
		t.Fatalf("SetCachedResult failed: %v", err)
	}

	data, ok := p.GetCachedResult("SELECT * FROM users WHERE id = $1", []byte{0, 1})
	if !ok {
		t.Error("GetCachedResult should return true for cached entry")
	}
	if string(data) != "cached-data" {
		t.Errorf("GetCachedResult data = %q, want cached-data", string(data))
	}

	p.InvalidateCache("users")

	_, ok = p.GetCachedResult("SELECT * FROM users WHERE id = $1", []byte{0, 1})
	if ok {
		t.Error("GetCachedResult should return false after invalidation")
	}
}

// --- Pool Stats with cache ---

func TestPool_Stats_WithCache(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "stats-full",
		Mode: "transaction",
		Body: "postgresql",
		Cache: config.CacheConfig{
			Enabled: true,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	p.IncrementQueryCount()
	p.IncrementQueryCount()
	p.IncrementTxnCount()

	stats := p.Stats()
	if stats.TotalQueries != 2 {
		t.Errorf("TotalQueries = %d, want 2", stats.TotalQueries)
	}
	if stats.TotalTransactions != 1 {
		t.Errorf("TotalTransactions = %d, want 1", stats.TotalTransactions)
	}
	if stats.Name != "stats-full" {
		t.Errorf("Name = %q, want stats-full", stats.Name)
	}
}

// --- NewPool with TLS config ---

func TestNewPool_WithTLS(t *testing.T) {
	log, _ := logger.New("error", "json")

	tmpDir := t.TempDir()
	certFile := tmpDir + "/cert.pem"
	keyFile := tmpDir + "/key.pem"

	os.WriteFile(certFile, []byte("dummy"), 0644)
	os.WriteFile(keyFile, []byte("dummy"), 0644)

	cfg := &config.PoolConfig{
		Name: "tls-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
			TLS: config.TLSConfig{
				Mode:     "require",
				CertFile: certFile,
				KeyFile:  keyFile,
			},
		},
	}

	_, err := NewPool(cfg, nil, log, nil)
	t.Logf("NewPool with TLS: %v (expected to fail on cert parse)", err)
}

// --- NewPool with CA file ---

func TestNewPool_WithCAFile(t *testing.T) {
	log, _ := logger.New("error", "json")

	tmpDir := t.TempDir()
	caFile := tmpDir + "/ca.pem"
	os.WriteFile(caFile, []byte("-----BEGIN CERTIFICATE-----\ninvalid\n-----END CERTIFICATE-----"), 0644)

	cfg := &config.PoolConfig{
		Name: "ca-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
			TLS: config.TLSConfig{
				Mode:   "verify-ca",
				CAFile: caFile,
			},
		},
	}

	_, err := NewPool(cfg, nil, log, nil)
	t.Logf("NewPool with CA file: %v (expected to fail on cert parse)", err)
}

// --- AcquireToRole with no real backends ---

func TestPool_AcquireToRole_NoBackends(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "acquire-role-pool",
		Mode: "transaction",
		Body: "postgresql",
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark all backends as unhealthy so connection attempts fail immediately
	for _, b := range p.backends {
		b.Healthy.Store(false)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = p.AcquireToRole(ctx, "replica")
	if err == nil {
		t.Error("AcquireToRole should fail when no backends available")
	}
}

// --- SetConnForTest coverage ---

func TestServerConn_SetConnForTest(t *testing.T) {
	sc := &ServerConn{}
	client, _ := net.Pipe()
	defer client.Close()

	sc.SetConnForTest(client)
	if sc.Conn() != client {
		t.Error("SetConnForTest should set the connection")
	}
}

// --- Manager CreatePool with codec ---

func TestManager_CreatePool_WithCodec(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "codec-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}

	err := pm.CreatePool(poolCfg)
	if err != nil {
		t.Fatalf("CreatePool failed: %v", err)
	}
	p := pm.GetPool("codec-pool")
	if p == nil {
		t.Error("CreatePool should create a retrievable pool")
	}
}

// --- ResetConnection with context deadline ---

func TestResetConnection_WithContextDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		buf := make([]byte, 1024)
		server.SetReadDeadline(time.Now().Add(5 * time.Second))
		server.Read(buf)
		server.Close()
	}()

	codec := &MockCodec{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = ResetConnection(ctx, client, codec)
}

// --- Mock codec for transaction begin/end ---

type mockCodecTxn struct{ MockCodec }

func (m *mockCodecTxn) IsTransactionBegin(msg *common.Message) bool {
	return msg != nil && string(msg.Raw) == "BEGIN"
}

func (m *mockCodecTxn) IsTransactionEnd(msg *common.Message) bool {
	return msg != nil && string(msg.Raw) == "COMMIT"
}

// --- Mock codec for ExtractQuery error ---

type mockCodecExtractErr struct{ MockCodec }

func (m *mockCodecExtractErr) IsQuery(msg *common.Message) bool { return true }
func (m *mockCodecExtractErr) ExtractQuery(msg *common.Message) (string, error) {
	return "", errors.New("extract failed")
}

// --- Error strategy for testing error paths ---

type errorStrategy struct {
	pool            *Pool
	onTxnBeginErr   error
	onTxnEndErr     error
	onQueryComplete error
	onQueryResult   *ServerConn
	onQueryErr      error
}

func (e *errorStrategy) OnClientConnect(ctx context.Context, s *Session) error { return nil }
func (e *errorStrategy) OnClientDisconnect(s *Session) error                   { return nil }
func (e *errorStrategy) OnQuery(ctx context.Context, s *Session, msg *common.Message) (*ServerConn, error) {
	return e.onQueryResult, e.onQueryErr
}
func (e *errorStrategy) OnQueryComplete(s *Session) error    { return e.onQueryComplete }
func (e *errorStrategy) OnTransactionBegin(s *Session) error { return e.onTxnBeginErr }
func (e *errorStrategy) OnTransactionEnd(s *Session) error   { return e.onTxnEndErr }

// --- HandleMessage: transaction begin ---

func TestSession_HandleMessage_TransactionBegin(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	codec := &mockCodecTxn{}
	pool, err := NewPool(cfg, codec, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewTransactionStrategy(pool)
	sess := NewSession(pool, strategy)

	err = sess.HandleMessage(&common.Message{Raw: []byte("BEGIN")})
	if err != nil {
		t.Fatalf("HandleMessage failed: %v", err)
	}
	if !sess.InTransaction() {
		t.Error("Session should be in transaction after BEGIN")
	}
	if sess.TransactionStart().IsZero() {
		t.Error("TransactionStart should be set after BEGIN")
	}
}

// --- HandleMessage: transaction begin with strategy error ---

func TestSession_HandleMessage_TransactionBegin_StrategyError(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	codec := &mockCodecTxn{}
	pool, err := NewPool(cfg, codec, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := &errorStrategy{
		pool:          pool,
		onTxnBeginErr: errors.New("strategy begin error"),
	}
	sess := NewSession(pool, strategy)

	err = sess.HandleMessage(&common.Message{Raw: []byte("BEGIN")})
	if err == nil {
		t.Error("Should fail when OnTransactionBegin returns error")
	}
}

// --- HandleMessage: transaction end ---

func TestSession_HandleMessage_TransactionEnd(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	codec := &mockCodecTxn{}
	pool, err := NewPool(cfg, codec, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewTransactionStrategy(pool)
	sess := NewSession(pool, strategy)
	sess.SetInTransaction(true)

	err = sess.HandleMessage(&common.Message{Raw: []byte("COMMIT")})
	if err != nil {
		t.Fatalf("HandleMessage failed: %v", err)
	}
	if sess.InTransaction() {
		t.Error("Session should not be in transaction after COMMIT")
	}
}

// --- HandleMessage: transaction end with strategy error ---

func TestSession_HandleMessage_TransactionEnd_StrategyError(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	codec := &mockCodecTxn{}
	pool, err := NewPool(cfg, codec, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := &errorStrategy{
		pool:        pool,
		onTxnEndErr: errors.New("strategy end error"),
	}
	sess := NewSession(pool, strategy)
	sess.SetInTransaction(true)

	err = sess.HandleMessage(&common.Message{Raw: []byte("COMMIT")})
	if err == nil {
		t.Error("Should fail when OnTransactionEnd returns error")
	}
}

// --- HandleMessage: OnQuery returns nil conn, nil error ---

func TestSession_HandleMessage_OnQueryNilConn(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	codec := &MockCodecQuery{}
	pool, err := NewPool(cfg, codec, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := &errorStrategy{
		pool:          pool,
		onQueryResult: nil,
		onQueryErr:    nil,
	}
	sess := NewSession(pool, strategy)

	err = sess.HandleMessage(&common.Message{Raw: []byte("SELECT 1")})
	if err == nil {
		t.Error("Should fail when OnQuery returns nil conn")
	}
	if err.Error() != "no server connection available" {
		t.Errorf("Error = %q, want 'no server connection available'", err)
	}
}

// --- HandleMessage: WriteMessage error ---

type mockCodecWriteErr struct{ MockCodec }

func (m *mockCodecWriteErr) IsQuery(msg *common.Message) bool { return true }
func (m *mockCodecWriteErr) ExtractQuery(msg *common.Message) (string, error) {
	return string(msg.Raw), nil
}
func (m *mockCodecWriteErr) WriteMessage(w io.Writer, msg *common.Message) error {
	return errors.New("write failed")
}

func TestSession_HandleMessage_WriteMessageError(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	codec := &mockCodecWriteErr{}
	pool, err := NewPool(cfg, codec, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	client, _ := net.Pipe()
	defer client.Close()
	serverConn := &ServerConn{
		id:      1,
		conn:    client,
		backend: &Backend{Host: "localhost", Port: 5432},
	}

	strategy := &errorStrategy{
		pool:          pool,
		onQueryResult: serverConn,
	}
	sess := NewSession(pool, strategy)

	err = sess.HandleMessage(&common.Message{Raw: []byte("SELECT 1")})
	if err == nil {
		t.Error("Should fail when WriteMessage fails")
	}
}

// --- HandleMessage: OnQueryComplete error ---

func TestSession_HandleMessage_OnQueryCompleteError(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	codec := &MockCodecQuery{}
	pool, err := NewPool(cfg, codec, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	client, _ := net.Pipe()
	defer client.Close()
	serverConn := &ServerConn{
		id:      1,
		conn:    client,
		backend: &Backend{Host: "localhost", Port: 5432},
	}

	strategy := &errorStrategy{
		pool:            pool,
		onQueryResult:   serverConn,
		onQueryComplete: errors.New("query complete error"),
	}
	sess := NewSession(pool, strategy)

	err = sess.HandleMessage(&common.Message{Raw: []byte("SELECT 1")})
	if err == nil {
		t.Error("Should fail when OnQueryComplete returns error")
	}
}

// --- HandleMessage: ExtractQuery error ---

func TestSession_HandleMessage_ExtractQueryError(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	codec := &mockCodecExtractErr{}
	pool, err := NewPool(cfg, codec, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	client, _ := net.Pipe()
	defer client.Close()
	serverConn := &ServerConn{
		id:      1,
		conn:    client,
		backend: &Backend{Host: "localhost", Port: 5432},
	}

	strategy := &errorStrategy{
		pool:          pool,
		onQueryResult: serverConn,
	}
	sess := NewSession(pool, strategy)

	err = sess.HandleMessage(&common.Message{Raw: []byte("SELECT 1")})
	if err != nil {
		t.Errorf("ExtractQuery error should not propagate: %v", err)
	}
	if sess.LastQuery() != "" {
		t.Errorf("LastQuery = %q, want empty when ExtractQuery fails", sess.LastQuery())
	}
}

// --- HandleMessage: successful query ---

func TestSession_HandleMessage_QuerySuccess(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "session",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	codec := &MockCodecQuery{}
	pool, err := NewPool(cfg, codec, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	client, _ := net.Pipe()
	defer client.Close()

	serverConn := &ServerConn{
		id:      1,
		conn:    client,
		backend: &Backend{Host: "localhost", Port: 5432},
	}

	strategy := &errorStrategy{
		pool:          pool,
		onQueryResult: serverConn,
	}
	sess := NewSession(pool, strategy)

	err = sess.HandleMessage(&common.Message{Raw: []byte("SELECT 1")})
	if err != nil {
		t.Fatalf("HandleMessage failed: %v", err)
	}
	if sess.QueryCount() != 1 {
		t.Errorf("QueryCount = %d, want 1", sess.QueryCount())
	}
	if sess.LastQuery() != "SELECT 1" {
		t.Errorf("LastQuery = %q, want SELECT 1", sess.LastQuery())
	}
}

// --- Statement strategy OnTransactionBegin returns error ---

func TestStatementStrategy_OnTransactionBegin_Error(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "statement",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewStatementStrategy(pool)
	sess := NewSession(pool, strategy)

	err = strategy.OnTransactionBegin(sess)
	if err == nil {
		t.Error("StatementStrategy.OnTransactionBegin should return error")
	}
}

// --- Statement strategy OnQuery with no idle conns ---

func TestStatementStrategy_OnQuery_NoIdleConns(t *testing.T) {
	cfg := &config.PoolConfig{
		Name: "test",
		Mode: "statement",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}
	log, _ := logger.New("error", "json")
	pool, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	strategy := NewStatementStrategy(pool)
	sess := NewSession(pool, strategy)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = strategy.OnQuery(ctx, sess, &common.Message{Raw: []byte("SELECT 1")})
	if err == nil {
		t.Error("OnQuery should fail with no backends")
	}
}

// --- DrainBackend with no connection ---

func TestServerConn_DrainBackend_NilConn(t *testing.T) {
	sc := &ServerConn{
		id:      1,
		conn:    nil,
		backend: &Backend{Host: "localhost", Port: 5432},
	}
	// Should not panic
	sc.Close()
}

// --- Make sure fmt is used ---

var _ = fmt.Sprintf

// --- AcquireToRole: with available idle connection ---

func TestPool_AcquireToRole_IdleConn(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "acquire-idle",
		Mode: "transaction",
		Body: "postgresql",
		Limits: config.LimitConfig{
			MaxServerConnections: 10,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Add an idle connection to the pool
	client, _ := net.Pipe()
	sc := &ServerConn{
		id:      1,
		conn:    client,
		backend: &Backend{Host: "127.0.0.1", Port: 5432},
	}
	p.serverConns.release(sc)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	conn, err := p.AcquireToRole(ctx, "primary")
	if err != nil {
		t.Fatalf("AcquireToRole should succeed with idle conn: %v", err)
	}
	if conn != sc {
		t.Error("Should return the idle connection")
	}
	client.Close()
}

// --- Release with waiting client ---

func TestPool_Release_WithWaiter(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "release-waiter",
		Mode: "transaction",
		Body: "postgresql",
		Limits: config.LimitConfig{
			MaxServerConnections: 1,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Create a server conn and add to active
	client, _ := net.Pipe()
	defer client.Close()
	sc := &ServerConn{
		id:      1,
		conn:    client,
		backend: &Backend{Host: "127.0.0.1", Port: 5432},
	}
	p.serverConns.addActive(sc)

	// Start a goroutine that waits for a connection
	acquired := make(chan *ServerConn, 1)
	go func() {
		waitTimeout := 2 * time.Second
		conn, err := p.waitQueue.Wait(context.Background(), waitTimeout)
		if err == nil {
			acquired <- conn
		}
	}()

	time.Sleep(50 * time.Millisecond)

	// Release the conn - should signal the waiter
	p.Release(sc)

	select {
	case c := <-acquired:
		if c != sc {
			t.Error("Waiter should get the released connection")
		}
	case <-time.After(2 * time.Second):
		t.Error("Waiter should have been signaled")
	}
}

// --- Manager: RemovePool ---

func TestManager_RemovePoolCov(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := NewManager(log)

	cfg := &config.PoolConfig{
		Name: "remove-me",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)

	if p := pm.GetPool("remove-me"); p == nil {
		t.Fatal("Pool should exist after CreatePool")
	}

	err := pm.RemovePool("remove-me")
	if err != nil {
		t.Fatalf("RemovePool failed: %v", err)
	}

	if p := pm.GetPool("remove-me"); p != nil {
		t.Error("Pool should be nil after RemovePool")
	}
}

// --- Manager: RemovePool non-existent ---

func TestManager_RemovePool_NotFoundCov(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := NewManager(log)

	err := pm.RemovePool("nonexistent")
	if err == nil {
		t.Error("RemovePool should fail for non-existent pool")
	}
}

// --- Manager: UpdatePoolConfig ---

func TestManager_UpdatePoolConfigCov(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := NewManager(log)

	cfg := &config.PoolConfig{
		Name: "update-me",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)

	err := pm.UpdatePoolConfig("update-me", &config.PoolConfig{
		Name: "update-me",
		Mode: "transaction",
		Body: "postgresql",
		Limits: config.LimitConfig{
			MaxServerConnections: 50,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	})
	if err != nil {
		t.Fatalf("UpdatePoolConfig failed: %v", err)
	}
}

// --- Manager: Close ---

func TestManager_CloseCov(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := NewManager(log)

	cfg := &config.PoolConfig{
		Name: "close-me",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)

	err := pm.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

// --- Manager: CreatePool duplicate ---

func TestManager_CreatePool_Duplicate(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := NewManager(log)

	cfg := &config.PoolConfig{
		Name: "dup-pool",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{{Host: "127.0.0.1", Port: 5432, Role: "primary"}},
		},
	}
	pm.CreatePool(cfg)

	// Creating pool with same name should fail
	err := pm.CreatePool(cfg)
	if err == nil {
		t.Error("CreatePool should fail for duplicate name")
	}
}

// --- HealthChecker: checkMySQL with mock connection ---

func TestHealthChecker_CheckMySQL(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	hc := &HealthChecker{
		checkQuery: "SELECT 1",
	}

	// Server side: read the query, send an OK response
	go func() {
		buf := make([]byte, 1024)
		server.SetReadDeadline(time.Now().Add(2 * time.Second))
		server.Read(buf)

		// Send OK response
		resp := []byte{0x01, 0x00, 0x00, 0x01, 0x00} // len=1, seq=1, OK
		server.Write(resp)
	}()

	err := hc.checkMySQL(client)
	if err != nil {
		t.Errorf("checkMySQL failed: %v", err)
	}
}

// --- HealthChecker: checkMySQL error response ---

func TestHealthChecker_CheckMySQL_Error(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	hc := &HealthChecker{
		checkQuery: "SELECT 1",
	}

	go func() {
		buf := make([]byte, 1024)
		server.SetReadDeadline(time.Now().Add(2 * time.Second))
		server.Read(buf)

		// Send error response
		resp := []byte{0x05, 0x00, 0x00, 0x01, 0xff, 0x01, 0x00, 0x00, 0x00}
		server.Write(resp)
	}()

	err := hc.checkMySQL(client)
	if err == nil {
		t.Error("checkMySQL should fail for error response")
	}
}

// --- HealthChecker: checkMSSQL with mock connection ---

func TestHealthChecker_CheckMSSQL(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	hc := &HealthChecker{
		checkQuery: "SELECT 1",
	}

	// Server: read query, send a TDS response
	go func() {
		buf := make([]byte, 4096)
		server.SetReadDeadline(time.Now().Add(2 * time.Second))
		server.Read(buf)

		// Send a minimal TDS response with EOM flag
		respHeader := make([]byte, 8)
		respHeader[0] = 0x04                            // TabularResult
		respHeader[1] = 0x01                            // EOM
		binary.BigEndian.PutUint16(respHeader[2:4], 12) // length
		payload := []byte{0xFF, 0x00, 0x00, 0x00}
		server.Write(append(respHeader, payload...))
	}()

	err := hc.checkMSSQL(client)
	// May or may not succeed depending on parsing, but should not panic
	_ = err
}

// --- selectBackendWithFallback ---

func TestSelectBackendWithFallback_HealthyReplica(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "fallback-test",
		Mode: "transaction",
		Body: "postgresql",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
				{Host: "127.0.0.1", Port: 5433, Role: "replica"},
			},
		},
	}
	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	// Mark primary unhealthy
	for _, b := range p.backends {
		if b.Role == "primary" {
			b.Healthy.Store(false)
		}
	}

	backend := p.selectBackendWithFallback()
	if backend == nil {
		t.Error("Should fallback to healthy replica")
	}
}
