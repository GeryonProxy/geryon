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
func NewHealthChecker(cfg *config.HealthConfig, log *logger.Logger) *HealthChecker {
	ctx, cancel := context.WithCancel(context.Background())

	hc := &HealthChecker{
		backends:    make(map[string]*BackendHealth),
		config:      cfg,
		checkQuery:  cfg.CheckQuery,
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
	_, cancel := context.WithTimeout(hc.ctx, hc.timeout)
	defer cancel()

	// Try TCP connect first
	conn, err := net.DialTimeout("tcp", backend.Address(), hc.timeout)
	if err != nil {
		return fmt.Errorf("TCP connect failed: %w", err)
	}
	conn.Close()

	// Protocol-specific health check would go here
	// For now, we just check TCP connectivity

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
