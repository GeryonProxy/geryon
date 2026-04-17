// Package grpc provides a JSON-over-HTTP admin API for programmatic management
// of the Geryon proxy. Despite the package name, this uses JSON encoding rather
// than protobuf/gRPC to maintain zero-dependency operation. The package name is
// retained for import compatibility.
package grpc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

// Server implements an HTTP Admin API for streaming stats.
// Uses JSON encoding with HTTP server-sent events for streaming.
// Note: Despite the grpc package name, this is JSON-over-HTTP, not gRPC.
// gRPC-compatible route names are retained for import compatibility.
type Server struct {
	mu          sync.RWMutex
	poolMgr     *pool.Manager
	log         *logger.Logger
	server      *http.Server
	config      *Config
	streams     map[string]*Stream
	streamLimit int
	streamCount atomic.Int64
	authToken   string
	authEnabled bool
	reloadFn    func() error
}

// Config holds HTTP/2 Admin API server configuration.
type Config struct {
	Listen       string
	ReadTimeout  string
	WriteTimeout string
	Auth         config.RESTAuthConfig
	MaxStreams   int // Max concurrent streaming connections (0 = unlimited)
}

// Stream represents an active streaming connection.
type Stream struct {
	ID      string
	Client  string
	Type    string // "stats", "events", "logs"
	Started time.Time
	Cancel  context.CancelFunc
}

// NewServer creates a new HTTP/2 Admin API server.
func NewServer(cfg *Config, poolMgr *pool.Manager, log *logger.Logger, reloadFn func() error) *Server {
	s := &Server{
		config:      cfg,
		poolMgr:     poolMgr,
		log:         log,
		streams:     make(map[string]*Stream),
		streamLimit: cfg.MaxStreams,
		authEnabled: cfg.Auth.Enabled,
		authToken:   cfg.Auth.Token,
		reloadFn:    reloadFn,
	}
	if s.streamLimit <= 0 {
		s.streamLimit = 100 // Default limit
	}
	return s
}

// Start starts the HTTP/2 Admin API server.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/grpc.health.v1.Health/Check", s.handleHealthCheck)

	// Stats streaming
	mux.HandleFunc("/geryon.v1.Stats/Stream", s.handleStatsStream)
	mux.HandleFunc("/geryon.v1.Stats/GetPools", s.handleGetPools)
	mux.HandleFunc("/geryon.v1.Stats/GetBackends", s.handleGetBackends)
	mux.HandleFunc("/geryon.v1.Stats/GetConnections", s.handleGetConnections)

	// Events streaming
	mux.HandleFunc("/geryon.v1.Events/Subscribe", s.handleEventsSubscribe)

	// Admin operations
	mux.HandleFunc("/geryon.v1.Admin/DrainBackend", s.handleDrainBackend)
	mux.HandleFunc("/geryon.v1.Admin/ReloadConfig", s.handleReloadConfig)

	readTimeout := parseDuration(s.config.ReadTimeout, 30*time.Second)
	writeTimeout := parseDuration(s.config.WriteTimeout, 30*time.Second)
	s.server = &http.Server{
		Addr:         s.config.Listen,
		Handler:      s.withLogging(s.withPanicRecovery(s.withSecurityHeaders(s.withRateLimit(s.withAuth(mux))))),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("Admin API server error", "error", err)
		}
	}()

	s.log.Info("Admin API server started", "address", s.config.Listen)
	return nil
}

// Stop stops the Admin API server.
func (s *Server) Stop(ctx context.Context) error {
	// Cancel all active streams
	s.mu.Lock()
	for _, stream := range s.streams {
		if stream.Cancel != nil {
			stream.Cancel()
		}
	}
	s.streams = make(map[string]*Stream)
	s.mu.Unlock()

	return s.server.Shutdown(ctx)
}

// withLogging adds request logging middleware.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.log.Debug("HTTP/2 Admin API request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start),
		)
	})
}

// withPanicRecovery recovers from panics in handlers and returns 500.
func (s *Server) withPanicRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				s.log.Error("Panic recovered in HTTP/2 Admin API handler", "error", err, "path", r.URL.Path)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// withSecurityHeaders adds security headers to all responses.
func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// withAuth adds authentication middleware.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authEnabled {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.authToken)) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// checkStreamLimit returns false if the stream limit is reached.
func (s *Server) checkStreamLimit() bool {
	if s.streamLimit <= 0 {
		return true
	}
	if s.streamCount.Load() >= int64(s.streamLimit) {
		return false
	}
	s.streamCount.Add(1)
	return true
}

// releaseStream decrements the stream counter.
func (s *Server) releaseStream() {
	s.streamCount.Add(-1)
}

// apiRateLimiter implements a simple token bucket rate limiter per IP.
type apiRateLimiter struct {
	mu         sync.Mutex
	limiters   map[string]*rate.Limiter
	lastSeen   map[string]time.Time
	rate       rate.Limit
	burst      int
	maxSize    int
	cleanupTTL time.Duration
}

func newAPIRateLimiter(r rate.Limit, burst int) *apiRateLimiter {
	rl := &apiRateLimiter{
		limiters:   make(map[string]*rate.Limiter),
		lastSeen:   make(map[string]time.Time),
		rate:       r,
		burst:      burst,
		maxSize:    10000,
		cleanupTTL: 5 * time.Minute,
	}
	go rl.periodicCleanup()
	return rl
}

func (rl *apiRateLimiter) periodicCleanup() {
	ticker := time.NewTicker(rl.cleanupTTL)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, last := range rl.lastSeen {
			if now.Sub(last) > rl.cleanupTTL {
				delete(rl.limiters, ip)
				delete(rl.lastSeen, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *apiRateLimiter) GetLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if len(rl.limiters) >= rl.maxSize {
		var oldestIP string
		var oldestTime time.Time
		for ip, last := range rl.lastSeen {
			if oldestIP == "" || last.Before(oldestTime) {
				oldestIP = ip
				oldestTime = last
			}
		}
		if oldestIP != "" {
			delete(rl.limiters, oldestIP)
			delete(rl.lastSeen, oldestIP)
		}
	}

	rl.lastSeen[ip] = time.Now()
	limiter, ok := rl.limiters[ip]
	if !ok {
		limiter = rate.NewLimiter(rl.rate, rl.burst)
		rl.limiters[ip] = limiter
	}
	return limiter
}

// withRateLimit adds rate limiting middleware per client IP.
func (s *Server) withRateLimit(next http.Handler) http.Handler {
	rl := newAPIRateLimiter(5, 10) // 5 req/s, burst 10
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip == "" {
			ip = r.RemoteAddr
		}

		limiter := rl.GetLimiter(ip)
		if !limiter.Allow() {
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// writeJSONResponse writes a JSON response.
func (s *Server) writeJSONResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// handleHealthCheck implements the gRPC health check protocol.
func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.writeJSONResponse(w, map[string]interface{}{
		"status": "SERVING",
	})
}

// handleStatsStream streams real-time statistics.
func (s *Server) handleStatsStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.checkStreamLimit() {
		http.Error(w, "Too many streaming connections", http.StatusTooManyRequests)
		return
	}
	defer s.releaseStream()

	// Parse request
	var req struct {
		Interval int `json:"interval"` // seconds
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		s.writeJSONResponse(w, map[string]interface{}{"error": "Invalid request body"})
		return
	}

	if req.Interval <= 0 {
		req.Interval = 5
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Register stream
	streamID := fmt.Sprintf("stats-%d", time.Now().UnixNano())
	s.mu.Lock()
	s.streams[streamID] = &Stream{
		ID:      streamID,
		Client:  r.RemoteAddr,
		Type:    "stats",
		Started: time.Now(),
		Cancel:  cancel,
	}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.streams, streamID)
		s.mu.Unlock()
	}()

	// Set up streaming response
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	ticker := time.NewTicker(time.Duration(req.Interval) * time.Second)
	defer ticker.Stop()

	encoder := json.NewEncoder(w)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats := s.collectStats()
			if err := encoder.Encode(stats); err != nil {
				s.log.Debug("Failed to encode stats stream", "error", err)
				return
			}
			// Flush response
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

// handleGetPools returns pool information.
func (s *Server) handleGetPools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pools := s.poolMgr.ListPools()
	result := make([]map[string]interface{}, 0, len(pools))

	for _, p := range pools {
		stats := p.Stats()
		result = append(result, map[string]interface{}{
			"name":               stats.Name,
			"mode":               stats.Mode,
			"client_connections": stats.ClientConnections,
			"server_connections": stats.ServerConnections,
			"idle_connections":   stats.IdleConnections,
			"active_connections": stats.ActiveConnections,
			"total_queries":      stats.TotalQueries,
			"backend_count":      stats.BackendCount,
		})
	}

	s.writeJSONResponse(w, map[string]interface{}{"pools": result})
}

// handleGetBackends returns backend information.
func (s *Server) handleGetBackends(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PoolName string `json:"pool_name"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)

	result := make([]map[string]interface{}, 0)

	for _, p := range s.poolMgr.ListPools() {
		if req.PoolName != "" && p.Name() != req.PoolName {
			continue
		}

		for _, b := range p.GetBackends() {
			result = append(result, map[string]interface{}{
				"address":    b.Address(),
				"pool":       p.Name(),
				"role":       b.Role,
				"healthy":    b.Healthy.Load(),
				"draining":   b.Draining.Load(),
				"last_check": b.LastCheck.Format(time.RFC3339),
			})
		}
	}

	s.writeJSONResponse(w, map[string]interface{}{"backends": result})
}

// handleGetConnections returns active connections.
func (s *Server) handleGetConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	totalClients := int64(0)
	totalServers := int64(0)

	for _, p := range s.poolMgr.ListPools() {
		stats := p.Stats()
		totalClients += stats.ClientConnections
		totalServers += int64(stats.ServerConnections)
	}

	s.writeJSONResponse(w, map[string]interface{}{
		"total_clients": totalClients,
		"total_servers": totalServers,
	})
}

// handleEventsSubscribe subscribes to cluster events.
func (s *Server) handleEventsSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.checkStreamLimit() {
		http.Error(w, "Too many streaming connections", http.StatusTooManyRequests)
		return
	}
	defer s.releaseStream()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	streamID := fmt.Sprintf("events-%d", time.Now().UnixNano())
	s.mu.Lock()
	s.streams[streamID] = &Stream{
		ID:      streamID,
		Client:  r.RemoteAddr,
		Type:    "events",
		Started: time.Now(),
		Cancel:  cancel,
	}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.streams, streamID)
		s.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "application/grpc")
	w.WriteHeader(http.StatusOK)

	encoder := json.NewEncoder(w)

	// Send initial event
	encoder.Encode(map[string]interface{}{
		"type":      "connected",
		"timestamp": time.Now().Format(time.RFC3339),
	})

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Keep connection alive
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			encoder.Encode(map[string]interface{}{
				"type":      "heartbeat",
				"timestamp": time.Now().Format(time.RFC3339),
			})
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

// handleDrainBackend drains a backend.
func (s *Server) handleDrainBackend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		s.writeJSONResponse(w, map[string]interface{}{"error": "Invalid request body"})
		return
	}

	// Find pool with this backend
	for _, p := range s.poolMgr.ListPools() {
		for _, b := range p.GetBackends() {
			if b.Address() == req.Address {
				activeConns, err := p.DrainBackend(req.Address)
				if err != nil {
					s.writeJSONResponse(w, map[string]interface{}{"error": "Failed to drain backend"})
					return
				}
				s.writeJSONResponse(w, map[string]interface{}{
					"success":            true,
					"active_connections": activeConns,
					"address":            req.Address,
				})
				return
			}
		}
	}

	s.writeJSONResponse(w, map[string]interface{}{
		"error": fmt.Sprintf("backend '%s' not found", req.Address),
	})
}

// handleReloadConfig reloads configuration.
func (s *Server) handleReloadConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.log.Info("Configuration reload requested via HTTP/2 Admin API")
	if s.reloadFn != nil {
		if err := s.reloadFn(); err != nil {
			s.writeJSONResponse(w, map[string]interface{}{"success": false, "error": "Config reload failed"})
			return
		}
	}
	s.writeJSONResponse(w, map[string]interface{}{
		"success": true,
		"message": "Configuration reloaded",
	})
}

// collectStats gathers current statistics.
func (s *Server) collectStats() map[string]interface{} {
	pools := s.poolMgr.ListPools()

	var totalClients int64
	var totalServers int64
	var totalQueries int64
	var totalTx int64

	poolStats := make([]map[string]interface{}, 0, len(pools))

	for _, p := range pools {
		stats := p.Stats()
		totalClients += stats.ClientConnections
		totalServers += int64(stats.ServerConnections)
		totalQueries += stats.TotalQueries
		totalTx += stats.TotalTransactions

		poolStats = append(poolStats, map[string]interface{}{
			"name":               stats.Name,
			"client_connections": stats.ClientConnections,
			"server_connections": stats.ServerConnections,
			"total_queries":      stats.TotalQueries,
		})
	}

	return map[string]interface{}{
		"timestamp":          time.Now().Format(time.RFC3339),
		"total_pools":        len(pools),
		"total_clients":      totalClients,
		"total_servers":      totalServers,
		"total_queries":      totalQueries,
		"total_transactions": totalTx,
		"pools":              poolStats,
	}
}

// GetStreamCount returns the number of active streams.
func (s *Server) GetStreamCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.streams)
}

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
