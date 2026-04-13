package dashboard

import (
	"embed"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

//go:embed static/*
var staticFS embed.FS

// Server serves the web dashboard.
type Server struct {
	poolMgr *pool.Manager
	log     *logger.Logger
	server  *http.Server
	config  *Config
	authToken  string
	authEnabled bool
	reloadFn   func() error
}

// Config holds dashboard configuration.
type Config struct {
	Enabled bool
	Listen  string
	Path    string
	Auth    config.RESTAuthConfig
}

// NewServer creates a new dashboard server.
func NewServer(cfg *Config, poolMgr *pool.Manager, log *logger.Logger, reloadFn func() error) *Server {
	return &Server{
		config:      cfg,
		poolMgr:     poolMgr,
		log:         log,
		authEnabled: cfg.Auth.Enabled,
		authToken:   cfg.Auth.Token,
		reloadFn:    reloadFn,
	}
}

// Start starts the dashboard server.
func (s *Server) Start() error {
	if !s.config.Enabled {
		s.log.Info("Dashboard disabled")
		return nil
	}

	mux := http.NewServeMux()

	// Static files
	staticContent, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("failed to create static subdir: %w", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticContent))))

	// Dashboard root - serve index.html
	mux.HandleFunc("/", s.handleIndex)

	// API endpoints
	mux.HandleFunc("/api/v1/pools", s.handlePools)
	mux.HandleFunc("/api/v1/stats", s.handleStats)
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/backends", s.handleBackends)
	mux.HandleFunc("/api/v1/connections", s.handleConnections)
	mux.HandleFunc("/api/v1/queries", s.handleQueries)
	mux.HandleFunc("/api/v1/config", s.handleConfig)

	s.server = &http.Server{
		Addr:         s.config.Listen,
		Handler:      s.withLogging(s.withSecurityHeaders(s.withRateLimit(s.withAuth(mux)))),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	ready := make(chan struct{})
	go func() {
		ln, err := net.Listen("tcp", s.config.Listen)
		if err != nil {
			s.log.Error("Dashboard server error", "error", err)
			return
		}
		close(ready)
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.log.Error("Dashboard server error", "error", err)
		}
	}()

	<-ready

	s.log.Info("Dashboard server started", "path", s.config.Path)
	return nil
}

// Stop stops the dashboard server.
func (s *Server) Stop() error {
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// withLogging adds request logging middleware.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.log.Debug("Dashboard request", "method", r.Method, "path", r.URL.Path)
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

// dashboardRateLimiter implements per-IP rate limiting with bounded map.
type dashboardRateLimiter struct {
	mu         sync.Mutex
	limiters   map[string]*rate.Limiter
	lastSeen   map[string]time.Time
	maxSize    int
	cleanupTTL time.Duration
}

func newDashboardRateLimiter(r rate.Limit, burst int) *dashboardRateLimiter {
	rl := &dashboardRateLimiter{
		limiters:   make(map[string]*rate.Limiter),
		lastSeen:   make(map[string]time.Time),
		maxSize:    10000,
		cleanupTTL: 5 * time.Minute,
	}
	go rl.periodicCleanup()
	return rl
}

func (rl *dashboardRateLimiter) periodicCleanup() {
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

func (rl *dashboardRateLimiter) GetLimiter(ip string) *rate.Limiter {
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
		limiter = rate.NewLimiter(5, 15)
		rl.limiters[ip] = limiter
	}
	return limiter
}

// withRateLimit adds per-IP rate limiting middleware.
func (s *Server) withRateLimit(next http.Handler) http.Handler {
	rl := newDashboardRateLimiter(5, 15)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip == "" {
			ip = r.RemoteAddr
		}

		if !rl.GetLimiter(ip).Allow() {
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// handleIndex serves the main dashboard HTML.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}

	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		s.log.Error("Failed to read index.html", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write(data)
}

// handlePools returns pool information.
func (s *Server) handlePools(w http.ResponseWriter, r *http.Request) {
	pools := s.poolMgr.ListPools()
	result := make([]map[string]any, 0, len(pools))

	for _, p := range pools {
		stats := p.Stats()
		result = append(result, map[string]any{
			"name":               stats.Name,
			"body":               stats.Mode, // Use mode as body indicator
			"mode":               stats.Mode,
			"client_connections": stats.ClientConnections,
			"server_connections": stats.ServerConnections,
			"backend_count":      stats.BackendCount,
			"query_cache_entries": stats.QueryCacheEntries,
			"query_cache_hit_rate": stats.QueryCacheHitRate,
		})
	}

	s.writeJSON(w, map[string]any{"pools": result})
}

// handleStats returns statistics.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	var totalQueries int64
	var totalTransactions int64
	var totalCacheHits uint64
	var totalCacheMisses uint64

	for _, p := range s.poolMgr.ListPools() {
		stats := p.Stats()
		totalQueries += stats.TotalQueries
		totalTransactions += stats.TotalTransactions
		// Estimate hits/misses from hit rate
		if stats.QueryCacheHitRate > 0 {
			totalCacheHits += uint64(float64(stats.QueryCacheEntries) * stats.QueryCacheHitRate / 100)
			totalCacheMisses += uint64(float64(stats.QueryCacheEntries) * (100 - stats.QueryCacheHitRate) / 100)
		}
	}

	// Calculate hit rate
	cacheHitRate := 0.0
	totalCacheRequests := totalCacheHits + totalCacheMisses
	if totalCacheRequests > 0 {
		cacheHitRate = float64(totalCacheHits) / float64(totalCacheRequests) * 100
	}

	s.writeJSON(w, map[string]any{
		"queries_per_sec":    0, // Requires time-series tracking
		"cache_hit_rate":     cacheHitRate,
		"total_queries":      totalQueries,
		"total_transactions": totalTransactions,
	})
}

// handleHealth returns health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{
		"status":    "healthy",
		"timestamp": time.Now().Unix(),
	})
}

// handleBackends returns backend information.
func (s *Server) handleBackends(w http.ResponseWriter, r *http.Request) {
	result := make([]map[string]any, 0)

	for _, p := range s.poolMgr.ListPools() {
		poolName := p.Name()
		for _, b := range p.GetBackends() {
			result = append(result, map[string]any{
				"address":     b.Address(),
				"pool":        poolName,
				"role":        b.Role,
				"healthy":     b.Healthy.Load(),
				"draining":    b.Draining.Load(),
				"connections": b.ConnCount.Load(),
			})
		}
	}

	s.writeJSON(w, map[string]any{"backends": result})
}

// handleConnections returns connection information.
func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	var connections []map[string]any

	for _, p := range s.poolMgr.ListPools() {
		stats := p.Stats()

		// Add pool-level connection info
		poolConn := map[string]any{
			"pool_name":          stats.Name,
			"client_connections": stats.ClientConnections,
			"server_connections": stats.ServerConnections,
			"idle_connections":   stats.IdleConnections,
			"active_connections": stats.ActiveConnections,
			"waiting_clients":    stats.WaitingClients,
		}
		connections = append(connections, poolConn)

		// Get active transactions if available
		if stats.ActiveTransactions > 0 {
			txnConn := map[string]any{
				"pool_name":           stats.Name,
				"active_transactions": stats.ActiveTransactions,
			}
			connections = append(connections, txnConn)
		}
	}

	s.writeJSON(w, map[string]any{"connections": connections})
}

// handleQueries returns query statistics.
func (s *Server) handleQueries(w http.ResponseWriter, r *http.Request) {
	var totalQueries int64
	var totalTransactions int64

	for _, p := range s.poolMgr.ListPools() {
		stats := p.Stats()
		totalQueries += stats.TotalQueries
		totalTransactions += stats.TotalTransactions
	}

	s.writeJSON(w, map[string]any{
		"total_queries":      totalQueries,
		"total_transactions": totalTransactions,
	})
}

// handleConfig returns configuration.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleConfigReload(w, r)
		return
	}

	// Return minimal config for now
	s.writeJSON(w, map[string]any{
		"dashboard": s.config.Enabled,
	})
}

// handleConfigReload handles config reload.
func (s *Server) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	s.log.Info("Config reload requested from dashboard")
	if s.reloadFn != nil {
		if err := s.reloadFn(); err != nil {
			s.writeJSON(w, map[string]any{"status": "error", "message": err.Error()})
			return
		}
	}
	s.writeJSON(w, map[string]any{"status": "reloaded"})
}

// writeJSON writes JSON response.
func (s *Server) writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}
