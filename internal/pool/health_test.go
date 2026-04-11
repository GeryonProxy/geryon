package pool

import (
	"net"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
)

// TestHealthStatus_String_Extended tests the String method of HealthStatus
func TestHealthStatus_String_Extended(t *testing.T) {
	tests := []struct {
		status HealthStatus
		want   string
	}{
		{HealthHealthy, "healthy"},
		{HealthDegraded, "degraded"},
		{HealthUnhealthy, "unhealthy"},
		{HealthUnknown, "unknown"},
		{HealthStatus(99), "unknown"}, // Invalid status
	}

	for _, tt := range tests {
		got := tt.status.String()
		if got != tt.want {
			t.Errorf("HealthStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}

// TestNewHealthChecker tests the creation of a new health checker
func TestNewHealthChecker(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.HealthConfig{
		CheckInterval: "10s",
		CheckQuery:    "SELECT 1",
		MaxFailures:   5,
	}

	hc := NewHealthChecker(cfg, log)
	if hc == nil {
		t.Fatal("NewHealthChecker returned nil")
	}

	if hc.checkQuery != "SELECT 1" {
		t.Errorf("checkQuery = %q, want SELECT 1", hc.checkQuery)
	}

	if hc.maxFailures != 5 {
		t.Errorf("maxFailures = %d, want 5", hc.maxFailures)
	}

	if hc.interval != 10*time.Second {
		t.Errorf("interval = %v, want 10s", hc.interval)
	}
}

// TestNewHealthChecker_Defaults tests default values
func TestNewHealthChecker_Defaults(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.HealthConfig{
		CheckInterval: "",
		CheckQuery:    "",
		MaxFailures:   0,
	}

	hc := NewHealthChecker(cfg, log)

	if hc.checkQuery != "SELECT 1" {
		t.Errorf("default checkQuery = %q, want SELECT 1", hc.checkQuery)
	}

	if hc.maxFailures != 3 {
		t.Errorf("default maxFailures = %d, want 3", hc.maxFailures)
	}
}

// TestHealthChecker_AddBackend_Extended tests adding backends
func TestHealthChecker_AddBackend_Extended(t *testing.T) {
	log, _ := logger.New("error", "json")
	hc := NewHealthChecker(&config.HealthConfig{}, log)

	backend := &Backend{
		Host: "127.0.0.1",
		Port: 5432,
	}
	backend.Healthy.Store(true)

	hc.AddBackend(backend)

	bh := hc.GetHealth(backend)
	if bh == nil {
		t.Fatal("GetHealth returned nil after AddBackend")
	}

	if bh.Status != HealthUnknown {
		t.Errorf("initial status = %v, want HealthUnknown", bh.Status)
	}

	if bh.Backend != backend {
		t.Error("Backend reference mismatch")
	}
}

// TestHealthChecker_AddBackend_Duplicate tests adding the same backend twice
func TestHealthChecker_AddBackend_Duplicate(t *testing.T) {
	log, _ := logger.New("error", "json")
	hc := NewHealthChecker(&config.HealthConfig{}, log)

	backend := &Backend{
		Host: "127.0.0.1",
		Port: 5432,
	}

	hc.AddBackend(backend)
	hc.AddBackend(backend) // Should not panic or add duplicate

	count := 0
	for range hc.backends {
		count++
	}

	if count != 1 {
		t.Errorf("backend count = %d, want 1", count)
	}
}

// TestHealthChecker_RemoveBackend_Extended tests removing backends
func TestHealthChecker_RemoveBackend_Extended(t *testing.T) {
	log, _ := logger.New("error", "json")
	hc := NewHealthChecker(&config.HealthConfig{}, log)

	backend := &Backend{
		Host: "127.0.0.1",
		Port: 5432,
	}

	hc.AddBackend(backend)
	hc.RemoveBackend(backend)

	bh := hc.GetHealth(backend)
	if bh != nil {
		t.Error("GetHealth should return nil after RemoveBackend")
	}
}

// TestHealthChecker_StartStop_Extended tests starting and stopping the health checker
func TestHealthChecker_StartStop_Extended(t *testing.T) {
	log, _ := logger.New("error", "json")
	hc := NewHealthChecker(&config.HealthConfig{
		CheckInterval: "100ms",
	}, log)

	// Should start without panic
	hc.Start()

	// Give it a moment to run
	time.Sleep(50 * time.Millisecond)

	// Should stop without panic
	hc.Stop()
	hc.Stop() // Idempotent
}

// TestHealthChecker_Stats_Extended tests the Stats method
func TestHealthChecker_Stats_Extended(t *testing.T) {
	log, _ := logger.New("error", "json")
	hc := NewHealthChecker(&config.HealthConfig{}, log)

	// Add multiple backends with different statuses
	backends := []*Backend{
		{Host: "127.0.0.1", Port: 5432},
		{Host: "127.0.0.1", Port: 5433},
		{Host: "127.0.0.1", Port: 5434},
	}

	for _, b := range backends {
		hc.AddBackend(b)
	}

	// Set different statuses
	hc.backends["127.0.0.1:5432"].Status = HealthHealthy
	hc.backends["127.0.0.1:5433"].Status = HealthUnhealthy
	hc.backends["127.0.0.1:5434"].Status = HealthDegraded

	stats := hc.Stats()

	if stats.Backends != 3 {
		t.Errorf("Backends = %d, want 3", stats.Backends)
	}

	if stats.Healthy != 1 {
		t.Errorf("Healthy = %d, want 1", stats.Healthy)
	}

	if stats.Unhealthy != 1 {
		t.Errorf("Unhealthy = %d, want 1", stats.Unhealthy)
	}

	if stats.Degraded != 1 {
		t.Errorf("Degraded = %d, want 1", stats.Degraded)
	}
}

// TestPerformCheck_Success tests performCheck with a successful connection
func TestPerformCheck_Success(t *testing.T) {
	log, _ := logger.New("error", "json")

	// Create a test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create test listener: %v", err)
	}
	defer listener.Close()

	// Accept connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	hc := NewHealthChecker(&config.HealthConfig{}, log)

	backend := &Backend{
		Host: "127.0.0.1",
		Port: listener.Addr().(*net.TCPAddr).Port,
	}

	err = hc.performCheck(backend)
	if err != nil {
		t.Errorf("performCheck failed: %v", err)
	}
}

// TestPerformCheck_Failure tests performCheck with a failed connection
func TestPerformCheck_Failure(t *testing.T) {
	log, _ := logger.New("error", "json")
	hc := NewHealthChecker(&config.HealthConfig{
		CheckInterval: "1s",
	}, log)

	// Use a port that's unlikely to be open
	backend := &Backend{
		Host: "127.0.0.1",
		Port: 65432,
	}

	err := hc.performCheck(backend)
	if err == nil {
		t.Error("performCheck should fail for unreachable backend")
	}
}

// TestCheckBackend_Success tests checkBackend with a healthy backend
func TestCheckBackend_Success(t *testing.T) {
	log, _ := logger.New("error", "json")

	// Create a test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create test listener: %v", err)
	}
	defer listener.Close()

	// Accept connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	hc := NewHealthChecker(&config.HealthConfig{}, log)

	backend := &Backend{
		Host: "127.0.0.1",
		Port: listener.Addr().(*net.TCPAddr).Port,
	}
	backend.Healthy.Store(false) // Start as unhealthy

	bh := &BackendHealth{
		Backend: backend,
		Status:  HealthUnknown,
	}

	hc.checkBackend(bh)

	if bh.Status != HealthHealthy {
		t.Errorf("status = %v, want HealthHealthy", bh.Status)
	}

	if !backend.Healthy.Load() {
		t.Error("backend should be marked as healthy")
	}

	if bh.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0", bh.ConsecutiveFailures)
	}
}

// TestCheckBackend_Failure tests checkBackend with a failing backend
func TestCheckBackend_Failure(t *testing.T) {
	log, _ := logger.New("error", "json")
	hc := NewHealthChecker(&config.HealthConfig{
		MaxFailures: 3,
	}, log)

	backend := &Backend{
		Host: "127.0.0.1",
		Port: 65432, // Unlikely to be open
	}
	backend.Healthy.Store(true) // Start as healthy

	bh := &BackendHealth{
		Backend:             backend,
		Status:              HealthHealthy,
		ConsecutiveFailures: 0,
	}

	// First failure
	hc.checkBackend(bh)

	if bh.Status != HealthDegraded {
		t.Errorf("status after 1 failure = %v, want HealthDegraded", bh.Status)
	}

	// Second failure
	hc.checkBackend(bh)

	if bh.Status != HealthDegraded {
		t.Errorf("status after 2 failures = %v, want HealthDegraded", bh.Status)
	}

	// Third failure - should become unhealthy
	hc.checkBackend(bh)

	if bh.Status != HealthUnhealthy {
		t.Errorf("status after 3 failures = %v, want HealthUnhealthy", bh.Status)
	}

	if backend.Healthy.Load() {
		t.Error("backend should be marked as unhealthy")
	}
}

// TestCheckBackend_Recovery tests backend recovery after failures
func TestCheckBackend_Recovery(t *testing.T) {
	log, _ := logger.New("error", "json")

	// Create a test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create test listener: %v", err)
	}
	defer listener.Close()

	// Accept connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	hc := NewHealthChecker(&config.HealthConfig{
		MaxFailures: 3,
	}, log)

	backend := &Backend{
		Host: "127.0.0.1",
		Port: listener.Addr().(*net.TCPAddr).Port,
	}

	bh := &BackendHealth{
		Backend:             backend,
		Status:              HealthUnhealthy,
		ConsecutiveFailures: 3,
	}
	backend.Healthy.Store(false)

	// Successful check should recover
	hc.checkBackend(bh)

	if bh.Status != HealthHealthy {
		t.Errorf("status = %v, want HealthHealthy", bh.Status)
	}

	if bh.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0", bh.ConsecutiveFailures)
	}
}

// TestParseDuration_Extended tests the parseDuration helper
func TestParseDuration_Extended(t *testing.T) {
	tests := []struct {
		input    string
		defaultD time.Duration
		want     time.Duration
	}{
		{"10s", 5 * time.Second, 10 * time.Second},
		{"", 5 * time.Second, 5 * time.Second},
		{"invalid", 5 * time.Second, 5 * time.Second},
		{"1m", 5 * time.Second, 1 * time.Minute},
	}

	for _, tt := range tests {
		got := parseDuration(tt.input, tt.defaultD)
		if got != tt.want {
			t.Errorf("parseDuration(%q, %v) = %v, want %v", tt.input, tt.defaultD, got, tt.want)
		}
	}
}

// TestWaitForHealthy tests the WaitForHealthy method
func TestWaitForHealthy(t *testing.T) {
	log, _ := logger.New("error", "json")
	hc := NewHealthChecker(&config.HealthConfig{}, log)

	backend := &Backend{
		Host: "127.0.0.1",
		Port: 5432,
	}

	hc.AddBackend(backend)

	// Start as unhealthy, then become healthy after a delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		hc.mu.Lock()
		if bh, ok := hc.backends["127.0.0.1:5432"]; ok {
			bh.Status = HealthHealthy
		}
		hc.mu.Unlock()
	}()

	result := hc.WaitForHealthy(backend, 500*time.Millisecond)
	if !result {
		t.Error("WaitForHealthy should return true when backend becomes healthy")
	}
}

// TestWaitForHealthy_Timeout tests timeout in WaitForHealthy
func TestWaitForHealthy_Timeout(t *testing.T) {
	log, _ := logger.New("error", "json")
	hc := NewHealthChecker(&config.HealthConfig{}, log)

	backend := &Backend{
		Host: "127.0.0.1",
		Port: 5432,
	}

	hc.AddBackend(backend)
	// Keep it unhealthy

	result := hc.WaitForHealthy(backend, 50*time.Millisecond)
	if result {
		t.Error("WaitForHealthy should return false on timeout")
	}
}

// TestBackendHealth_ConcurrentAccess tests thread safety
func TestBackendHealth_ConcurrentAccess(t *testing.T) {
	bh := &BackendHealth{
		Backend: &Backend{Host: "127.0.0.1", Port: 5432},
		Status:  HealthUnknown,
	}

	// Concurrent writes
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(i int) {
			bh.mu.Lock()
			bh.Status = HealthStatus(i % 4)
			bh.LastCheck = time.Now()
			bh.mu.Unlock()
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		go func() {
			bh.mu.RLock()
			_ = bh.Status
			_ = bh.LastCheck
			bh.mu.RUnlock()
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestHealthChecker_RunChecks tests the runChecks method
func TestHealthChecker_RunChecks(t *testing.T) {
	log, _ := logger.New("error", "json")

	// Create test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create test listener: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	hc := NewHealthChecker(&config.HealthConfig{}, log)

	backend := &Backend{
		Host: "127.0.0.1",
		Port: listener.Addr().(*net.TCPAddr).Port,
	}

	hc.AddBackend(backend)

	// Run checks - should not panic
	hc.runChecks()

	// Give it time to complete
	time.Sleep(100 * time.Millisecond)
}

// TestHealthChecker_GetHealth_NonExistent tests GetHealth for non-existent backend
func TestHealthChecker_GetHealth_NonExistent(t *testing.T) {
	log, _ := logger.New("error", "json")
	hc := NewHealthChecker(&config.HealthConfig{}, log)

	backend := &Backend{
		Host: "127.0.0.1",
		Port: 5432,
	}

	bh := hc.GetHealth(backend)
	if bh != nil {
		t.Error("GetHealth should return nil for non-existent backend")
	}
}

// TestHealthChecker_MultipleStartStop tests multiple start/stop calls
func TestHealthChecker_MultipleStartStop(t *testing.T) {
	log, _ := logger.New("error", "json")
	hc := NewHealthChecker(&config.HealthConfig{
		CheckInterval: "100ms",
	}, log)

	// Multiple starts should be safe
	hc.Start()
	hc.Start()
	hc.Start()

	time.Sleep(50 * time.Millisecond)

	// Multiple stops should be safe
	hc.Stop()
	hc.Stop()
	hc.Stop()
}

// TestBackendHealth_LatencyTracking tests latency tracking
func TestBackendHealth_LatencyTracking(t *testing.T) {
	log, _ := logger.New("error", "json")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create test listener: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	hc := NewHealthChecker(&config.HealthConfig{}, log)

	backend := &Backend{
		Host: "127.0.0.1",
		Port: listener.Addr().(*net.TCPAddr).Port,
	}

	bh := &BackendHealth{
		Backend: backend,
		Status:  HealthUnknown,
	}

	hc.checkBackend(bh)

	if bh.Latency == 0 {
		t.Error("Latency should be recorded")
	}

	if bh.LastCheck.IsZero() {
		t.Error("LastCheck should be set")
	}
}

// TestHealthStats_EmptyBackends tests stats with no backends
func TestHealthStats_EmptyBackends(t *testing.T) {
	log, _ := logger.New("error", "json")
	hc := NewHealthChecker(&config.HealthConfig{}, log)

	stats := hc.Stats()

	if stats.Backends != 0 {
		t.Errorf("Backends = %d, want 0", stats.Backends)
	}

	if stats.AverageLatency != 0 {
		t.Errorf("AverageLatency = %v, want 0", stats.AverageLatency)
	}
}
