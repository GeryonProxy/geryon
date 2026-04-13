package pool

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
)

// HealthStatus represents the health status of a backend.
type HealthStatus int

const (
	HealthUnknown HealthStatus = iota
	HealthHealthy
	HealthDegraded
	HealthUnhealthy
)

func (s HealthStatus) String() string {
	switch s {
	case HealthHealthy:
		return "healthy"
	case HealthDegraded:
		return "degraded"
	case HealthUnhealthy:
		return "unhealthy"
	default:
		return "unknown"
	}
}

// HealthChecker performs health checks on backends.
type HealthChecker struct {
	mu          sync.RWMutex
	backends    map[string]*BackendHealth
	config      *config.HealthConfig
	checkQuery  string
	body        string // protocol body: postgresql, mysql, mssql
	interval    time.Duration
	timeout     time.Duration
	maxFailures int
	ctx         context.Context
	cancel      context.CancelFunc
	log         *logger.Logger
	running     atomic.Bool
}

// BackendHealth tracks health state for a backend.
type BackendHealth struct {
	Backend        *Backend
	Status         HealthStatus
	LastCheck      time.Time
	LastSuccess    time.Time
	LastFailure    time.Time
	ConsecutiveFailures int
	Latency        time.Duration
	mu             sync.RWMutex
}

// NewHealthChecker creates a new health checker.
func NewHealthChecker(cfg *config.HealthConfig, body string, log *logger.Logger) *HealthChecker {
	ctx, cancel := context.WithCancel(context.Background())

	hc := &HealthChecker{
		backends:    make(map[string]*BackendHealth),
		config:      cfg,
		checkQuery:  cfg.CheckQuery,
		body:        body,
		interval:    parseDuration(cfg.CheckInterval, 5*time.Second),
		timeout:     parseDuration("5s", 5*time.Second),
		maxFailures: cfg.MaxFailures,
		ctx:         ctx,
		cancel:      cancel,
		log:         log,
	}

	if hc.checkQuery == "" {
		hc.checkQuery = "SELECT 1"
	}
	if hc.maxFailures == 0 {
		hc.maxFailures = 3
	}

	return hc
}

// AddBackend adds a backend to health checking.
func (hc *HealthChecker) AddBackend(backend *Backend) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	key := backend.Address()
	if _, exists := hc.backends[key]; exists {
		return
	}

	hc.backends[key] = &BackendHealth{
		Backend: backend,
		Status:  HealthUnknown,
	}
}

// RemoveBackend removes a backend from health checking.
func (hc *HealthChecker) RemoveBackend(backend *Backend) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	delete(hc.backends, backend.Address())
}

// GetHealth returns the health status for a backend.
func (hc *HealthChecker) GetHealth(backend *Backend) *BackendHealth {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	return hc.backends[backend.Address()]
}

// Start starts the health checker.
func (hc *HealthChecker) Start() {
	if hc.running.CompareAndSwap(false, true) {
		go hc.checkLoop()
		hc.log.Info("Health checker started", "interval", hc.interval)
	}
}

// Stop stops the health checker.
func (hc *HealthChecker) Stop() {
	if hc.running.CompareAndSwap(true, false) {
		hc.cancel()
	}
}

// checkLoop runs health checks periodically.
func (hc *HealthChecker) checkLoop() {
	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()

	// Run initial checks
	hc.runChecks()

	for {
		select {
		case <-hc.ctx.Done():
			hc.log.Info("Health checker stopped")
			return
		case <-ticker.C:
			hc.runChecks()
		}
	}
}

// runChecks runs health checks on all backends.
func (hc *HealthChecker) runChecks() {
	hc.mu.RLock()
	backends := make([]*BackendHealth, 0, len(hc.backends))
	for _, bh := range hc.backends {
		backends = append(backends, bh)
	}
	hc.mu.RUnlock()

	for _, bh := range backends {
		go hc.checkBackend(bh)
	}
}

// checkBackend performs a health check on a single backend.
func (hc *HealthChecker) checkBackend(bh *BackendHealth) {
	bh.mu.Lock()
	bh.LastCheck = time.Now()
	bh.mu.Unlock()

	// Connect and run check query
	start := time.Now()
	err := hc.performCheck(bh.Backend)
	latency := time.Since(start)

	bh.mu.Lock()
	defer bh.mu.Unlock()

	bh.Latency = latency

	if err != nil {
		bh.LastFailure = time.Now()
		bh.ConsecutiveFailures++

		hc.log.Debug("Health check failed",
			"backend", bh.Backend.Address(),
			"error", err,
			"failures", bh.ConsecutiveFailures,
		)

		// Update status based on failure count
		if bh.ConsecutiveFailures >= hc.maxFailures {
			if bh.Status != HealthUnhealthy {
				hc.log.Warn("Backend marked unhealthy",
					"backend", bh.Backend.Address(),
					"failures", bh.ConsecutiveFailures,
				)
				bh.Status = HealthUnhealthy
				bh.Backend.Healthy.Store(false)
			}
		} else if bh.ConsecutiveFailures > 0 {
			bh.Status = HealthDegraded
		}
	} else {
		bh.LastSuccess = time.Now()
		bh.ConsecutiveFailures = 0

		if bh.Status != HealthHealthy {
			hc.log.Info("Backend is healthy",
				"backend", bh.Backend.Address(),
				"latency", latency,
			)
			bh.Status = HealthHealthy
			bh.Backend.Healthy.Store(true)
		}
	}
}

// performCheck performs the actual health check.
func (hc *HealthChecker) performCheck(backend *Backend) error {
	ctx, cancel := context.WithTimeout(hc.ctx, hc.timeout)
	defer cancel()

	// Use context-aware dialer for proper cancellation
	dialer := net.Dialer{Timeout: hc.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", backend.Address())
	if err != nil {
		return fmt.Errorf("TCP connect failed: %w", err)
	}
	defer conn.Close()

	// Set read/write deadlines
	conn.SetReadDeadline(time.Now().Add(hc.timeout))
	conn.SetWriteDeadline(time.Now().Add(hc.timeout))

	// Protocol-specific health check query
	switch hc.body {
	case "postgresql":
		return hc.checkPostgreSQL(conn)
	case "mysql":
		return hc.checkMySQL(conn)
	case "mssql":
		return hc.checkMSSQL(conn)
	default:
		// Fallback to TCP-only
		return nil
	}
}

// checkPostgreSQL sends a simple query and waits for ReadyForQuery.
func (hc *HealthChecker) checkPostgreSQL(conn net.Conn) error {
	query := hc.checkQuery
	if query == "" {
		query = "SELECT 1"
	}

	// PostgreSQL Simple Query protocol: 'Q' + int32 length + query + null
	payload := append([]byte(query), 0)
	msgLen := 4 + len(payload)
	packet := make([]byte, 5+len(payload))
	packet[0] = 'Q'
	packet[1] = byte(msgLen >> 24)
	packet[2] = byte(msgLen >> 16)
	packet[3] = byte(msgLen >> 8)
	packet[4] = byte(msgLen)
	copy(packet[5:], payload)

	if _, err := conn.Write(packet); err != nil {
		return fmt.Errorf("failed to send query: %w", err)
	}

	// Read responses until ReadyForQuery ('Z')
	buf := make([]byte, 1024)
	for {
		// Read message type (1 byte)
		if _, err := conn.Read(buf[:1]); err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}
		msgType := buf[0]

		// Read message length (4 bytes)
		if _, err := conn.Read(buf[:4]); err != nil {
			return fmt.Errorf("failed to read length: %w", err)
		}
		msgLen := int(buf[0])<<24 | int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
		payloadLen := msgLen - 4

		// Discard payload
		for payloadLen > 0 {
			n := min(payloadLen, len(buf))
			read, err := conn.Read(buf[:n])
			if err != nil {
				return fmt.Errorf("failed to read payload: %w", err)
			}
			payloadLen -= read
		}

		if msgType == 'Z' { // ReadyForQuery
			return nil
		}
	}
}

// checkMySQL sends a COM_QUERY and checks for OK/ResultSet.
func (hc *HealthChecker) checkMySQL(conn net.Conn) error {
	query := hc.checkQuery
	if query == "" {
		query = "SELECT 1"
	}

	// MySQL COM_QUERY = 0x03
	payload := make([]byte, 1+len(query))
	payload[0] = 0x03 // COM_QUERY
	copy(payload[1:], query)

	// MySQL packet header: 3 bytes length + 1 byte sequence
	header := make([]byte, 4)
	header[0] = byte(len(payload) & 0xFF)
	header[1] = byte((len(payload) >> 8) & 0xFF)
	header[2] = byte((len(payload) >> 16) & 0xFF)
	header[3] = 1 // sequence number

	if _, err := conn.Write(append(header, payload...)); err != nil {
		return fmt.Errorf("failed to send query: %w", err)
	}

	// Read response header (4 bytes)
	respHeader := make([]byte, 4)
	if _, err := conn.Read(respHeader); err != nil {
		return fmt.Errorf("failed to read response header: %w", err)
	}

	respLen := int(respHeader[0]) | int(respHeader[1])<<8 | int(respHeader[2])<<16
	if respLen == 0 || respLen > 1<<24 {
		return fmt.Errorf("invalid response length: %d", respLen)
	}

	// Read response payload
	respPayload := make([]byte, respLen)
	if _, err := conn.Read(respPayload); err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check for OK packet (0x00) or ResultSet header
	switch respPayload[0] {
	case 0x00: // OK packet
		return nil
	case 0xff: // Error packet
		return fmt.Errorf("MySQL query error")
	default:
		// ResultSet - health check succeeded
		return nil
	}
}

// checkMSSQL sends a SQL batch and checks for response.
func (hc *HealthChecker) checkMSSQL(conn net.Conn) error {
	query := hc.checkQuery
	if query == "" {
		query = "SELECT 1"
	}

	// TDS header (8 bytes) for SQL Batch (type 0x01)
	queryBytes := []byte(query)
	payloadLen := 8 + len(queryBytes) + 2 // header + query + terminator
	if payloadLen > 1<<16 {
		return fmt.Errorf("query too large")
	}

	packet := make([]byte, payloadLen)
	packet[0] = 0x01 // TDS_QUERY
	packet[1] = 0x01 // Status: end of message
	packet[2] = byte(payloadLen >> 8)
	packet[3] = byte(payloadLen)
	packet[4] = 0x00 // SPID high
	packet[5] = 0x01 // SPID low
	packet[6] = 0x00 // Packet ID
	packet[7] = 0x00 // Window

	// SQL Batch header (8 bytes): AllHeaders length + header type
	copy(packet[8:], queryBytes)

	if _, err := conn.Write(packet); err != nil {
		return fmt.Errorf("failed to send query: %w", err)
	}

	// Read TDS response header
	respHeader := make([]byte, 8)
	if _, err := conn.Read(respHeader); err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	respLen := int(respHeader[2])<<8 | int(respHeader[3])
	if respLen < 8 || respLen > 1<<16 {
		return fmt.Errorf("invalid response length: %d", respLen)
	}

	// Read response payload
	respPayload := make([]byte, respLen-8)
	if _, err := conn.Read(respPayload); err != nil {
		return fmt.Errorf("failed to read response payload: %w", err)
	}

	return nil
}

// parseDuration parses a duration string with default.
func parseDuration(s string, defaultVal time.Duration) time.Duration {
	if s == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultVal
	}
	return d
}

// Stats returns health checker statistics.
type HealthStats struct {
	Backends       int
	Healthy        int
	Degraded       int
	Unhealthy      int
	Unknown        int
	AverageLatency time.Duration
}

// Stats returns health statistics.
func (hc *HealthChecker) Stats() HealthStats {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	stats := HealthStats{
		Backends: len(hc.backends),
	}

	var totalLatency time.Duration
	for _, bh := range hc.backends {
		bh.mu.RLock()
		status := bh.Status
		latency := bh.Latency
		bh.mu.RUnlock()

		switch status {
		case HealthHealthy:
			stats.Healthy++
		case HealthDegraded:
			stats.Degraded++
		case HealthUnhealthy:
			stats.Unhealthy++
		default:
			stats.Unknown++
		}

		totalLatency += latency
	}

	if stats.Backends > 0 {
		stats.AverageLatency = totalLatency / time.Duration(stats.Backends)
	}

	return stats
}

// WaitForHealthy waits for a backend to become healthy.
func (hc *HealthChecker) WaitForHealthy(backend *Backend, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if bh := hc.GetHealth(backend); bh != nil {
				bh.mu.RLock()
				status := bh.Status
				bh.mu.RUnlock()
				if status == HealthHealthy {
					return true
				}
			}
		}
	}
}
