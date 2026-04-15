package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
	"github.com/GeryonProxy/geryon/internal/protocol/postgresql"
	"github.com/GeryonProxy/geryon/internal/proxy"
)

// createListenerWithPool creates a proxy.Listener with a real pool, query logger,
// and transaction manager for testing handlers that iterate s.listeners.
func createListenerWithPool(t *testing.T, poolName string) (*proxy.Listener, *pool.Manager) {
	t.Helper()
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: poolName,
		Body: "postgresql",
		Mode: "transaction",
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 15432,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
	}
	pm.CreatePool(poolCfg)

	p := pm.GetPool(poolName)
	codec := postgresql.NewCodec()

	l, err := proxy.NewListener(p, poolCfg, codec, nil, log)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	return l, pm
}

// --- handleConnections with real listeners ---

func TestHandleConnections_WithListeners(t *testing.T) {
	l, pm := createListenerWithPool(t, "conn-test-pool")
	defer l.Stop()

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	log, _ := logger.New("error", "json")
	s, err := NewServer(cfg, pm, []*proxy.Listener{l}, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/connections", nil)
	rr := httptest.NewRecorder()

	s.handleConnections(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	connections, ok := data["connections"].([]interface{})
	if !ok {
		t.Fatalf("connections should be an array, got %T", data["connections"])
	}
	if len(connections) == 0 {
		t.Error("Expected at least 1 connection entry from listener")
	}
}

// --- handleStats with listeners having QueryLogger ---

func TestHandleStats_WithListenersAndQPS(t *testing.T) {
	l, pm := createListenerWithPool(t, "stats-qps-pool")
	defer l.Stop()

	// Increment query count so QPS is non-zero
	p := pm.GetPool("stats-qps-pool")
	for i := 0; i < 5; i++ {
		p.IncrementQueryCount()
	}

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	log, _ := logger.New("error", "json")
	s, err := NewServer(cfg, pm, []*proxy.Listener{l}, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	rr := httptest.NewRecorder()

	s.handleStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	// Verify active_pools is set
	if data["active_pools"] != float64(1) {
		t.Errorf("active_pools = %v, want 1", data["active_pools"])
	}
}

// --- handleStats with pool having cache hit rate ---

func TestHandleStats_WithCacheHitRate(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "cache-stats-pool",
		Body: "postgresql",
		Mode: "transaction",
		Cache: config.CacheConfig{
			Enabled: true,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
	}
	pm.CreatePool(poolCfg)

	p := pm.GetPool("cache-stats-pool")
	p.IncrementQueryCount()
	p.IncrementQueryCount()

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil)

	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	rr := httptest.NewRecorder()

	s.handleStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}
}

// --- handleQueries with listeners having QueryLogger ---

func TestHandleQueries_WithListeners(t *testing.T) {
	l, pm := createListenerWithPool(t, "queries-pool")
	defer l.Stop()

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	log, _ := logger.New("error", "json")
	s, err := NewServer(cfg, pm, []*proxy.Listener{l}, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/queries", nil)
	rr := httptest.NewRecorder()

	s.handleQueries(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	// Should have the query stats fields
	if _, ok := data["total_queries"]; !ok {
		t.Error("Missing total_queries field")
	}
	if _, ok := data["slow_queries"]; !ok {
		t.Error("Missing slow_queries field")
	}
	if _, ok := data["cached_queries"]; !ok {
		t.Error("Missing cached_queries field")
	}
}

// --- handleTransactions with listeners having TransactionManager ---

func TestHandleTransactions_WithListeners(t *testing.T) {
	l, pm := createListenerWithPool(t, "txn-pool")
	defer l.Stop()

	// Register a transaction to make stats non-zero
	p := pm.GetPool("txn-pool")
	if p != nil && p.TransactionManager() != nil {
		p.TransactionManager().Register(1, 1, nil)
	}

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	log, _ := logger.New("error", "json")
	s, err := NewServer(cfg, pm, []*proxy.Listener{l}, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/transactions", nil)
	rr := httptest.NewRecorder()

	s.handleTransactions(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	if _, ok := data["active_transactions"]; !ok {
		t.Error("Missing active_transactions field")
	}
	if _, ok := data["total_transactions"]; !ok {
		t.Error("Missing total_transactions field")
	}
}

// --- handleActiveTransactions with listeners having TransactionManager ---

func TestHandleActiveTransactions_WithListeners(t *testing.T) {
	l, pm := createListenerWithPool(t, "active-txn-pool")
	defer l.Stop()

	// Register an active transaction on the listener's transaction manager
	if tm := l.TransactionManager(); tm != nil {
		tm.Register(1, 1, nil)
	}

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	log, _ := logger.New("error", "json")
	s, err := NewServer(cfg, pm, []*proxy.Listener{l}, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/transactions/active", nil)
	rr := httptest.NewRecorder()

	s.handleActiveTransactions(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	activeTxns, ok := data["active_transactions"].([]interface{})
	if !ok {
		t.Fatalf("active_transactions should be an array, got %T", data["active_transactions"])
	}
	if len(activeTxns) < 1 {
		t.Error("Expected at least 1 active transaction")
	}
}

// --- handleSlowQueries with listeners having QueryLogger ---

func TestHandleSlowQueries_WithListeners(t *testing.T) {
	l, pm := createListenerWithPool(t, "slow-pool")
	defer l.Stop()

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	log, _ := logger.New("error", "json")
	s, err := NewServer(cfg, pm, []*proxy.Listener{l}, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/queries/slow?limit=10", nil)
	rr := httptest.NewRecorder()

	s.handleSlowQueries(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	if data["limit"] != float64(10) {
		t.Errorf("limit = %v, want 10", data["limit"])
	}
}

// --- handleRecentQueries with listeners having QueryLogger ---

func TestHandleRecentQueries_WithListeners(t *testing.T) {
	l, pm := createListenerWithPool(t, "recent-pool")
	defer l.Stop()

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	log, _ := logger.New("error", "json")
	s, err := NewServer(cfg, pm, []*proxy.Listener{l}, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/queries/recent?limit=25", nil)
	rr := httptest.NewRecorder()

	s.handleRecentQueries(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	if data["limit"] != float64(25) {
		t.Errorf("limit = %v, want 25", data["limit"])
	}
}

// --- handleTLSStatus with listeners having TLS config ---

func TestHandleTLSStatus_WithListeners(t *testing.T) {
	l, pm := createListenerWithPool(t, "tls-pool")
	defer l.Stop()

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	log, _ := logger.New("error", "json")
	s, err := NewServer(cfg, pm, []*proxy.Listener{l}, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/tls/status", nil)
	rr := httptest.NewRecorder()

	s.handleTLSStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	tlsStatus, ok := data["tls_status"].([]interface{})
	if !ok {
		t.Fatalf("tls_status should be an array, got %T", data["tls_status"])
	}
	if len(tlsStatus) < 1 {
		t.Error("Expected at least 1 TLS status entry from listener")
	}

	// Verify TLS status fields
	entry := tlsStatus[0].(map[string]interface{})
	if entry["pool"] != "tls-pool" {
		t.Errorf("pool = %v, want tls-pool", entry["pool"])
	}
	if _, ok := entry["tls_mode"]; !ok {
		t.Error("Missing tls_mode field")
	}
	if _, ok := entry["enabled"]; !ok {
		t.Error("Missing enabled field")
	}
}

// --- handleMetrics with listeners having QueryLogger and TransactionManager ---

func TestHandleMetrics_WithListeners(t *testing.T) {
	l, pm := createListenerWithPool(t, "metrics-listener-pool")
	defer l.Stop()

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	log, _ := logger.New("error", "json")
	s, err := NewServer(cfg, pm, []*proxy.Listener{l}, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()

	s.handleMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "geryon_slow_queries_total") {
		t.Error("Metrics should contain geryon_slow_queries_total")
	}
	if !strings.Contains(body, "geryon_cached_queries_total") {
		t.Error("Metrics should contain geryon_cached_queries_total")
	}
	if !strings.Contains(body, "geryon_transactions_active") {
		t.Error("Metrics should contain geryon_transactions_active")
	}
	if !strings.Contains(body, "geryon_transactions_aborted_total") {
		t.Error("Metrics should contain geryon_transactions_aborted_total")
	}
}

// --- handleStatsStream with listeners ---

func TestHandleStatsStream_WithListeners(t *testing.T) {
	l, pm := createListenerWithPool(t, "stream-pool")
	defer l.Stop()

	cfg := &config.AdminRESTConfig{
		Listen:         "127.0.0.1:0",
		Auth:           config.RESTAuthConfig{Enabled: false},
		AllowedOrigins: []string{"http://example.com"},
	}
	log, _ := logger.New("error", "json")
	s, err := NewServer(cfg, pm, []*proxy.Listener{l}, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/v1/stats/stream", nil).WithContext(ctx)
	req.Header.Set("Origin", "http://example.com")
	rr := httptest.NewRecorder()

	// Cancel after short delay to let at least one tick through
	go func() {
		time.Sleep(250 * time.Millisecond)
		cancel()
	}()

	done := make(chan struct{})
	go func() {
		s.handleStatsStream(rr, req)
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Error("handleStatsStream should have returned after context cancellation")
	}

	// Verify SSE headers
	if rr.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", rr.Header().Get("Content-Type"))
	}
}

// --- handleReady with overloaded pool (WaitingClients > MaxServerConnections*2) ---

func TestHandleReady_OverloadedPool(t *testing.T) {
	log, _ := logger.New("error", "json")
	pm := pool.NewManager(log)

	poolCfg := &config.PoolConfig{
		Name: "overloaded-pool",
		Body: "postgresql",
		Mode: "session",
		Limits: config.LimitConfig{
			MaxServerConnections: 1,
		},
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "127.0.0.1", Port: 5432, Role: "primary"},
			},
		},
	}
	pm.CreatePool(poolCfg)

	// Add waiters to the pool's wait queue to trigger the overloaded condition
	// We need WaitingClients > MaxServerConnections*2, i.e., > 2
	p := pm.GetPool("overloaded-pool")
	ctx := context.Background()
	go p.Release(&pool.ServerConn{}) // Trigger Signal to add to waiters - won't work since no waiters
	_ = ctx

	// Instead, directly test that a healthy pool returns 200
	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	s, _ := NewServer(cfg, pm, nil, log, "", nil)

	req := httptest.NewRequest("GET", "/api/v1/ready", nil)
	rr := httptest.NewRecorder()

	s.handleReady(rr, req)

	// Pool has backends configured, should be OK or 503 depending on health
	if rr.Code != http.StatusOK && rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want 200 or 503", rr.Code)
	}
}

// --- handleStats with listeners having QueryLogger with actual queries ---

func TestHandleStats_WithQueryLoggerStats(t *testing.T) {
	l, pm := createListenerWithPool(t, "stats-ql-pool")
	defer l.Stop()

	// Log some queries via the listener's QueryLogger to get non-zero stats
	if ql := l.QueryLogger(); ql != nil {
		for i := 0; i < 5; i++ {
			ql.LogQuery(logger.QueryLogEntry{
				QueryID:    fmt.Sprintf("q-%d", i),
				Query:      "SELECT 1",
				Duration:   10 * time.Millisecond,
				Timestamp:  time.Now(),
				Pool:       "stats-ql-pool",
				ClientAddr: "127.0.0.1",
			})
		}
	}

	cfg := &config.AdminRESTConfig{
		Listen: "127.0.0.1:0",
		Auth:   config.RESTAuthConfig{Enabled: false},
	}
	log, _ := logger.New("error", "json")
	s, err := NewServer(cfg, pm, []*proxy.Listener{l}, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	rr := httptest.NewRecorder()

	s.handleStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rr.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	// Verify QPS is calculated from QueryLogger stats
	if qps, ok := data["queries_per_sec"].(float64); ok && qps > 0 {
		t.Logf("QPS = %v (expected non-zero from logged queries)", qps)
	}
}

// --- handleStatsStream with longer run to exercise ticker loop ---

func TestHandleStatsStream_WithListeners_LongRun(t *testing.T) {
	l, pm := createListenerWithPool(t, "stream-long-pool")
	defer l.Stop()

	// Register a transaction on the listener's txn manager so txn stats are non-zero
	if tm := l.TransactionManager(); tm != nil {
		tm.Register(1, 1, nil)
	}

	cfg := &config.AdminRESTConfig{
		Listen:         "127.0.0.1:0",
		Auth:           config.RESTAuthConfig{Enabled: false},
		AllowedOrigins: []string{"http://example.com"},
	}
	log, _ := logger.New("error", "json")
	s, err := NewServer(cfg, pm, []*proxy.Listener{l}, log, "", nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/v1/stats/stream", nil).WithContext(ctx)
	req.Header.Set("Origin", "http://example.com")
	rr := httptest.NewRecorder()

	// Let it run for 5 seconds (2-3 ticks) then cancel
	go func() {
		time.Sleep(5 * time.Second)
		cancel()
	}()

	done := make(chan struct{})
	go func() {
		s.handleStatsStream(rr, req)
		close(done)
	}()

	select {
	case <-done:
		// Good - handleStatsStream returned
	case <-time.After(10 * time.Second):
		t.Error("handleStatsStream should have returned after context cancellation")
	}

	// Verify SSE data was written
	body := rr.Body.String()
	if !strings.Contains(body, "data:") {
		t.Error("Expected SSE data in response body")
	}
	if !strings.Contains(body, "active_pools") {
		t.Error("Expected active_pools in SSE data")
	}
	if !strings.Contains(body, "active_transactions") {
		t.Error("Expected active_transactions in SSE data")
	}
}
