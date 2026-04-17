// Package rest provides the REST API server for the Geryon proxy,
// offering full CRUD for pools, backends, connections, users, and cache,
// plus Prometheus metrics, SSE streaming stats, and config hot-reload.
package rest

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"golang.org/x/time/rate"

	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
	"github.com/GeryonProxy/geryon/internal/proxy"
)

//go:embed static/*
var staticFS embed.FS

// Cluster provides cluster state for metrics export.
type Cluster interface {
	StateString() string
	GetNodeCount() int
	GetTerm() uint64
}

// Server represents the REST API server.
type Server struct {
	mu         sync.RWMutex
	config     *config.AdminRESTConfig
	listener   net.Listener
	httpServer *http.Server
	poolMgr    *pool.Manager
	listeners  []*proxy.Listener
	log        *logger.Logger
	started    bool
	configPath string
	reloadFn   func() error
	cluster    Cluster
	userDB     *auth.UserDatabase
}

// NewServer creates a new REST API server.
func NewServer(cfg *config.AdminRESTConfig, poolMgr *pool.Manager, listeners []*proxy.Listener, log *logger.Logger, configPath string, reloadFn func() error, userDB *auth.UserDatabase) (*Server, error) {
	s := &Server{
		config:     cfg,
		poolMgr:    poolMgr,
		listeners:  listeners,
		log:        log,
		configPath: configPath,
		reloadFn:   reloadFn,
		userDB:     userDB,
	}

	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/v1/pools", s.handlePools)
	mux.HandleFunc("/api/v1/pools/", s.handlePoolDetail)
	mux.HandleFunc("/api/v1/connections", s.handleConnections)
	mux.HandleFunc("/api/v1/backends", s.handleBackends)
	mux.HandleFunc("/api/v1/backends/", s.handleBackendAction)
	mux.HandleFunc("/api/v1/stats", s.handleStats)
	mux.HandleFunc("/api/v1/stats/stream", s.handleStatsStream)
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/ready", s.handleReady)
	mux.HandleFunc("/api/v1/queries", s.handleQueries)
	mux.HandleFunc("/api/v1/queries/slow", s.handleSlowQueries)
	mux.HandleFunc("/api/v1/queries/recent", s.handleRecentQueries)
	mux.HandleFunc("/api/v1/transactions", s.handleTransactions)
	mux.HandleFunc("/api/v1/transactions/active", s.handleActiveTransactions)
	mux.HandleFunc("/api/v1/config", s.handleConfig)
	mux.HandleFunc("/api/v1/config/reload", s.handleConfigReload)
	mux.HandleFunc("/api/v1/config/file", s.handleConfigFile)
	mux.HandleFunc("/api/v1/users", s.handleUsers)
	mux.HandleFunc("/api/v1/users/", s.handleUserDetail)
	mux.HandleFunc("/api/v1/tls/status", s.handleTLSStatus)

	// Prometheus metrics endpoint (always requires auth)
	mux.Handle("/metrics", s.requireAuth(http.HandlerFunc(s.handleMetrics)))

	// pprof endpoints (always requires auth for security)
	mux.Handle("/debug/pprof/", s.requireAuth(http.HandlerFunc(pprof.Index)))
	mux.Handle("/debug/pprof/cmdline", s.requireAuth(http.HandlerFunc(pprof.Cmdline)))
	mux.Handle("/debug/pprof/profile", s.requireAuth(http.HandlerFunc(pprof.Profile)))
	mux.Handle("/debug/pprof/symbol", s.requireAuth(http.HandlerFunc(pprof.Symbol)))
	mux.Handle("/debug/pprof/trace", s.requireAuth(http.HandlerFunc(pprof.Trace)))

	// Dashboard routes
	if err := s.setupDashboard(mux); err != nil {
		return nil, fmt.Errorf("failed to setup dashboard: %w", err)
	}

	readTimeout := parseDuration(cfg.ReadTimeout, 30*time.Second)
	writeTimeout := parseDuration(cfg.WriteTimeout, 30*time.Second)
	s.httpServer = &http.Server{
		Addr:         cfg.Listen,
		Handler:      s.withLogging(s.withPanicRecovery(s.withRateLimit(s.withSecurityHeaders(s.withCORS(s.withAuth(mux)))))),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}

	return s, nil
}

// SetCluster sets the cluster for metrics export.
func (s *Server) SetCluster(c Cluster) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cluster = c
}

// setupDashboard sets up the dashboard routes.
func (s *Server) setupDashboard(mux *http.ServeMux) error {
	staticContent, err := fs.Sub(staticFS, "static")
	if err != nil {
		return err
	}

	fileServer := http.FileServer(http.FS(staticContent))
	mux.Handle("/", fileServer)

	return nil
}

// Start starts the REST API server.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("server already started")
	}

	listener, err := net.Listen("tcp", s.config.Listen)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.config.Listen, err)
	}
	s.listener = listener

	s.started = true

	go func() {
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.log.Error("REST server error", "error", err)
		}
	}()

	s.log.Info("REST API server started", "address", s.config.Listen)

	return nil
}

// Stop stops the REST API server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return nil
	}

	s.started = false

	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return err
	}

	s.log.Info("REST API server stopped")

	return nil
}

// withLogging adds request logging middleware.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.log.Debug("HTTP request",
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
				s.log.Error("Panic recovered in HTTP handler", "error", err, "path", r.URL.Path)
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
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		next.ServeHTTP(w, r)
	})
}

// isAllowedOrigin checks if an origin is in the allowed list.
func (s *Server) isAllowedOrigin(origin string) bool {
	if len(s.config.AllowedOrigins) == 0 {
		return origin == ""
	}
	for _, o := range s.config.AllowedOrigins {
		if o == "*" || o == origin {
			return true
		}
	}
	return false
}

// withCORS adds CORS headers.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false
		if len(s.config.AllowedOrigins) == 0 {
			// Default: only allow same-origin requests
			allowed = origin == ""
		} else {
			for _, o := range s.config.AllowedOrigins {
				if o == "*" || o == origin {
					allowed = true
					break
				}
			}
		}

		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// withAuth adds authentication middleware.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.config.Auth.Enabled {
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

		if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.config.Auth.Token)) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// requireAuth always requires authentication (ignores Auth.Enabled flag).
// Used for sensitive endpoints like /metrics.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.config.Auth.Token)) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// rateLimiter implements a simple token bucket rate limiter per IP.
// Uses sync.Map for concurrent access without mutex contention.
type rateLimiter struct {
	limiters   sync.Map // map[string]*rate.Limiter
	lastSeen   sync.Map // map[string]time.Time
	rate       rate.Limit
	burst      int
	maxSize    atomic.Int64
	cleanupTTL time.Duration
	size       atomic.Int64
}

func newRateLimiter(r rate.Limit, burst int) *rateLimiter {
	rl := &rateLimiter{
		rate:       r,
		burst:      burst,
		cleanupTTL: 5 * time.Minute,
	}
	rl.maxSize.Store(10000)
	go rl.periodicCleanup()
	return rl
}

func (rl *rateLimiter) periodicCleanup() {
	ticker := time.NewTicker(rl.cleanupTTL)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		rl.lastSeen.Range(func(key, value interface{}) bool {
			if last, ok := value.(time.Time); ok {
				if now.Sub(last) > rl.cleanupTTL {
					rl.limiters.Delete(key)
					rl.lastSeen.Delete(key)
					rl.size.Add(-1)
				}
			}
			return true
		})
	}
}

func (rl *rateLimiter) GetLimiter(ip string) *rate.Limiter {
	// Check if IP already has a limiter
	if limiter, ok := rl.limiters.Load(ip); ok {
		rl.lastSeen.Store(ip, time.Now()) // Update last seen
		return limiter.(*rate.Limiter)
	}

	// Evict oldest entry if at capacity
	if rl.size.Load() >= rl.maxSize.Load() {
		var oldestIP string
		var oldestTime time.Time
		rl.lastSeen.Range(func(key, value interface{}) bool {
			if last, ok := value.(time.Time); ok {
				if oldestIP == "" || last.Before(oldestTime) {
					oldestIP = key.(string)
					oldestTime = last
				}
			}
			return true
		})
		if oldestIP != "" {
			rl.limiters.Delete(oldestIP)
			rl.lastSeen.Delete(oldestIP)
			rl.size.Add(-1)
		}
	}

	// Create new limiter
	limiter := rate.NewLimiter(rl.rate, rl.burst)
	rl.limiters.Store(ip, limiter)
	rl.lastSeen.Store(ip, time.Now())
	rl.size.Add(1)
	return limiter
}

// withRateLimit adds rate limiting middleware per client IP.
func (s *Server) withRateLimit(next http.Handler) http.Handler {
	rl := newRateLimiter(10, 20) // 10 req/s, burst 20
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

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// writeError writes a sanitized error response without leaking internal details.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{"error": msg})
}

// sanitizeErr returns a short, safe error message.
// Internal details (file paths, connection strings) are stripped.
func sanitizeErr(err error) string {
	msg := err.Error()
	// Truncate to prevent leaking sensitive context
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

// poolNameRegex validates pool names: alphanumeric, underscores, hyphens, 1-64 chars.
var poolNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// validatePoolName returns true if the pool name is valid.
func validatePoolName(name string) bool {
	return poolNameRegex.MatchString(name)
}

// backendAddrRegex validates backend addresses: host:port format.
var backendAddrRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+:\d{1,5}$`)

// validateBackendAddr returns true if the backend address is valid.
func validateBackendAddr(addr string) bool {
	return backendAddrRegex.MatchString(addr)
}

// validateBackendAction returns true if the action is valid.
func validateBackendAction(action string) bool {
	return action == "drain" || action == "cancel-drain"
}

// validatePoolConfig validates pool configuration.
func validatePoolConfig(cfg *config.PoolConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("pool name is required")
	}
	if cfg.Body != "postgresql" && cfg.Body != "mysql" && cfg.Body != "mssql" {
		return fmt.Errorf("invalid body type: %s (must be postgresql, mysql, or mssql)", cfg.Body)
	}
	if cfg.Mode != "session" && cfg.Mode != "transaction" && cfg.Mode != "statement" {
		return fmt.Errorf("invalid mode: %s (must be session, transaction, or statement)", cfg.Mode)
	}
	if cfg.Listen.Port <= 0 || cfg.Listen.Port > 65535 {
		return fmt.Errorf("invalid port: %d", cfg.Listen.Port)
	}
	if len(cfg.Backend.Hosts) == 0 {
		return fmt.Errorf("at least one backend host is required")
	}
	for _, h := range cfg.Backend.Hosts {
		if h.Host == "" {
			return fmt.Errorf("backend host cannot be empty")
		}
		if h.Port <= 0 || h.Port > 65535 {
			return fmt.Errorf("invalid backend port: %d", h.Port)
		}
		if h.Role != "primary" && h.Role != "replica" {
			return fmt.Errorf("invalid backend role: %s (must be primary or replica)", h.Role)
		}
	}
	return nil
}

// handlePools handles pool listing and creation.
func (s *Server) handlePools(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		pools := s.poolMgr.ListPools()
		poolData := make([]map[string]interface{}, 0, len(pools))

		for _, p := range pools {
			stats := p.Stats()
			poolData = append(poolData, map[string]interface{}{
				"name":               stats.Name,
				"body":               p.Codec().Protocol(),
				"mode":               stats.Mode,
				"client_connections": stats.ClientConnections,
				"server_connections": stats.ServerConnections,
				"idle_connections":   stats.IdleConnections,
				"active_connections": stats.ActiveConnections,
				"status":             "online",
			})
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"pools": poolData,
		})

	case http.MethodPost:
		// Create new pool
		var req config.PoolConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if req.Name == "" {
			http.Error(w, "Pool name is required", http.StatusBadRequest)
			return
		}

		if !validatePoolName(req.Name) {
			writeError(w, http.StatusBadRequest, "Invalid pool name: must be 1-64 alphanumeric characters, underscores, or hyphens")
			return
		}

		// Validate body type
		switch req.Body {
		case "postgresql", "mysql", "mssql":
		default:
			writeError(w, http.StatusBadRequest, "Invalid body type: must be postgresql, mysql, or mssql")
			return
		}

		// Validate mode
		switch req.Mode {
		case "session", "transaction", "statement":
		default:
			writeError(w, http.StatusBadRequest, "Invalid mode: must be session, transaction, or statement")
			return
		}

		// Validate at least one backend is configured
		if len(req.Backend.Hosts) == 0 {
			writeError(w, http.StatusBadRequest, "At least one backend host is required")
			return
		}

		// Validate backend hosts
		for _, host := range req.Backend.Hosts {
			if host.Host == "" || host.Port <= 0 || host.Port > 65535 {
				writeError(w, http.StatusBadRequest, "Invalid backend host/port")
				return
			}
		}

		if err := s.poolMgr.CreatePool(&req); err != nil {
			writeError(w, http.StatusConflict, "Failed to create pool: "+sanitizeErr(err))
			return
		}

		s.log.Info("Pool created via API", "pool", req.Name)
		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"status":  "success",
			"message": "Pool created",
			"pool":    req.Name,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handlePoolDetail handles individual pool operations.
func (s *Server) handlePoolDetail(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	poolName := parts[4]
	if !validatePoolName(poolName) {
		writeError(w, http.StatusBadRequest, "Invalid pool name: must be 1-64 alphanumeric characters, underscores, or hyphens")
		return
	}

	p := s.poolMgr.GetPool(poolName)
	if p == nil {
		http.Error(w, "Pool not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		stats := p.Stats()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"name":               stats.Name,
			"mode":               stats.Mode,
			"client_connections": stats.ClientConnections,
			"server_connections": stats.ServerConnections,
			"idle_connections":   stats.IdleConnections,
			"active_connections": stats.ActiveConnections,
			"waiting_clients":    stats.WaitingClients,
			"total_queries":      stats.TotalQueries,
			"total_transactions": stats.TotalTransactions,
			"backend_count":      stats.BackendCount,
			"prepared_stmt_cache": map[string]interface{}{
				"size":     stats.PreparedStmtCacheSize,
				"hit_rate": stats.PreparedStmtHitRate,
			},
		})

	case http.MethodPut:
		// Update pool configuration
		var req config.PoolConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+sanitizeErr(err))
			return
		}

		// Ensure pool name matches URL
		req.Name = poolName

		// Validate the configuration
		if err := validatePoolConfig(&req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Update the pool
		if err := s.poolMgr.UpdatePoolConfig(poolName, &req); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to update pool: "+sanitizeErr(err))
			return
		}

		s.log.Info("Pool updated via API", "pool", poolName)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "success",
			"message": "Pool updated",
			"pool":    poolName,
		})

	case http.MethodDelete:
		// Remove the pool
		if !validatePoolName(poolName) {
			writeError(w, http.StatusBadRequest, "Invalid pool name")
			return
		}
		if err := s.poolMgr.RemovePool(poolName); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to delete pool: "+sanitizeErr(err))
			return
		}

		s.log.Info("Pool deleted via API", "pool", poolName)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "success",
			"message": "Pool deleted",
			"pool":    poolName,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleConnections handles connection listing.
func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Aggregate connections from all pools
	connections := make([]map[string]interface{}, 0)
	for _, l := range s.listeners {
		// Get connection info from listener
		connCount := l.SessionCount()
		pool := l.Pool()
		if pool != nil {
			stats := pool.Stats()
			connections = append(connections, map[string]interface{}{
				"pool":         stats.Name,
				"client_count": connCount,
				"server_count": stats.ServerConnections,
				"idle_count":   stats.IdleConnections,
				"active_count": stats.ActiveConnections,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"connections": connections,
	})
}

// handleBackends handles backend listing.
func (s *Server) handleBackends(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Aggregate backends from all pools
	backends := make([]map[string]interface{}, 0)
	for _, p := range s.poolMgr.ListPools() {
		for _, b := range p.GetBackends() {
			backends = append(backends, map[string]interface{}{
				"pool":       p.Name(),
				"address":    b.Address(),
				"role":       b.Role,
				"healthy":    b.Healthy.Load(),
				"draining":   b.Draining.Load(),
				"last_check": b.LastCheck,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"backends": backends,
	})
}

// handleStats handles global statistics.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pools := s.poolMgr.ListPools()
	var totalConns, totalQueries int64
	var totalCacheHitRate float64
	var cacheCount int

	for _, p := range pools {
		stats := p.Stats()
		totalConns += stats.ClientConnections
		totalQueries += stats.TotalQueries
		if stats.QueryCacheHitRate > 0 {
			totalCacheHitRate += stats.QueryCacheHitRate
			cacheCount++
		}
	}

	// Calculate average cache hit rate
	var avgCacheHitRate float64
	if cacheCount > 0 {
		avgCacheHitRate = totalCacheHitRate / float64(cacheCount)
	}

	// Calculate QPS (queries per second) from listener query loggers
	var qps float64
	for _, l := range s.listeners {
		if ql := l.QueryLogger(); ql != nil {
			stats := ql.GetStats(time.Now().Add(-time.Minute))
			if stats.TotalQueries > 0 {
				// Calculate average over last minute
				qps = float64(stats.TotalQueries) / 60.0
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total_connections": totalConns,
		"active_pools":      len(pools),
		"queries_per_sec":   qps,
		"cache_hit_rate":    avgCacheHitRate,
	})
}

// handleHealth handles health check requests.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
	})
}

// handleReady handles readiness probe requests.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if all pools are ready
	pools := s.poolMgr.ListPools()
	unhealthyPools := []string{}
	for _, p := range pools {
		stats := p.Stats()
		// Check if pool has healthy backends
		if stats.BackendCount == 0 {
			unhealthyPools = append(unhealthyPools, stats.Name+": no backends")
			continue
		}
		// Check if pool can accept connections
		if stats.WaitingClients > stats.MaxServerConnections*2 {
			unhealthyPools = append(unhealthyPools, stats.Name+": overloaded")
		}
	}

	if len(unhealthyPools) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"ready":     false,
			"reason":    "unhealthy pools",
			"pools":     unhealthyPools,
			"timestamp": time.Now().UTC(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ready":     true,
		"timestamp": time.Now().UTC(),
	})
}

// handleStatsStream handles SSE streaming for real-time stats.
func (s *Server) handleStatsStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if len(s.config.AllowedOrigins) > 0 {
		origin := r.Header.Get("Origin")
		if s.isAllowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
	}

	// Flush headers
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Send stats every 2 seconds
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Collect stats
			pools := s.poolMgr.ListPools()
			var totalConns, totalQueries int64
			var totalCacheHitRate float64
			var cacheCount int
			var activeTransactions int

			for _, p := range pools {
				stats := p.Stats()
				totalConns += stats.ClientConnections
				totalQueries += stats.TotalQueries
				if stats.QueryCacheHitRate > 0 {
					totalCacheHitRate += stats.QueryCacheHitRate
					cacheCount++
				}
			}

			// Calculate average cache hit rate
			var avgCacheHitRate float64
			if cacheCount > 0 {
				avgCacheHitRate = totalCacheHitRate / float64(cacheCount)
			}

			// Get query stats and calculate QPS
			var qps float64
			var cacheHits int64
			for _, l := range s.listeners {
				if ql := l.QueryLogger(); ql != nil {
					stats := ql.GetStats(time.Now().Add(-time.Minute))
					if stats.TotalQueries > 0 {
						qps = float64(stats.TotalQueries) / 60.0
					}
					cacheHits = int64(stats.CachedQueries)
				}
				if tm := l.TransactionManager(); tm != nil {
					stats := tm.GetStats()
					activeTransactions += stats.ActiveCount
				}
			}

			data := map[string]interface{}{
				"total_connections":   totalConns,
				"active_pools":        len(pools),
				"queries_per_sec":     qps,
				"cache_hit_rate":      avgCacheHitRate,
				"cached_queries":      cacheHits,
				"active_transactions": activeTransactions,
				"timestamp":           time.Now().UTC(),
			}

			jsonData, _ := json.Marshal(data)
			fmt.Fprintf(w, "data: %s\n\n", jsonData)

			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

// handleConfigReload handles configuration reload requests.
func (s *Server) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Consume and limit request body to prevent large payload abuse
	http.MaxBytesReader(w, r.Body, 1024)

	s.log.Info("Configuration reload requested via API")

	if s.reloadFn != nil {
		if err := s.reloadFn(); err != nil {
			writeError(w, http.StatusInternalServerError, "Configuration reload failed: "+sanitizeErr(err))
			return
		}
	} else {
		writeError(w, http.StatusNotImplemented, "Config reload not configured")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "success",
		"message":   "Configuration reloaded",
		"timestamp": time.Now().UTC(),
	})
}

// handleConfigFile reads (GET) or writes (PUT) the raw YAML config file.
func (s *Server) handleConfigFile(w http.ResponseWriter, r *http.Request) {
	if s.configPath == "" {
		writeError(w, http.StatusNotImplemented, "Config file path not available")
		return
	}

	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(s.configPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to read config file: "+sanitizeErr(err))
			return
		}
		w.Header().Set("Content-Type", "text/yaml")
		w.Write(data)

	case http.MethodPut:
		// Limit request body to 1MB
		r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)

		data, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Failed to read request body: "+sanitizeErr(err))
			return
		}

		// Validate the YAML before saving
		var testCfg config.Config
		if err := yaml.Unmarshal(data, &testCfg); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid YAML: "+sanitizeErr(err))
			return
		}
		if err := config.Validate(&testCfg); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid config: "+sanitizeErr(err))
			return
		}

		// Write to a temp file first, then rename for atomicity
		tmpPath := s.configPath + ".tmp"
		if err := os.WriteFile(tmpPath, data, 0644); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to write config: "+sanitizeErr(err))
			return
		}
		if err := os.Rename(tmpPath, s.configPath); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save config: "+sanitizeErr(err))
			return
		}

		s.log.Info("Configuration file updated", "path", s.configPath)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":    "success",
			"message":   "Configuration saved. Reload to apply.",
			"timestamp": time.Now().UTC(),
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleBackendAction handles backend-specific actions (drain, etc.).
func (s *Server) handleBackendAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse path: /api/v1/backends/{address}/{action}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 6 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	backendAddr := parts[4]
	if !validateBackendAddr(backendAddr) {
		writeError(w, http.StatusBadRequest, "Invalid backend address: must be host:port format")
		return
	}

	action := parts[5]
	if !validateBackendAction(action) {
		writeError(w, http.StatusBadRequest, "Invalid backend action: must be drain or cancel-drain")
		return
	}

	s.log.Info("Backend action requested", "address", backendAddr, "action", action)

	switch action {
	case "drain":
		s.handleBackendDrain(w, r, backendAddr)
	case "cancel-drain":
		s.handleBackendCancelDrain(w, r, backendAddr)
	default:
		writeError(w, http.StatusBadRequest, "Unknown backend action: "+action)
	}
}

// handleBackendDrain initiates draining for a backend.
func (s *Server) handleBackendDrain(w http.ResponseWriter, r *http.Request, backendAddr string) {
	// Find the pool that has this backend
	var targetPool *pool.Pool
	for _, p := range s.poolMgr.ListPools() {
		for _, b := range p.GetBackends() {
			if b.Address() == backendAddr {
				targetPool = p
				break
			}
		}
		if targetPool != nil {
			break
		}
	}

	if targetPool == nil {
		http.Error(w, "Backend not found", http.StatusNotFound)
		return
	}

	activeConns, err := targetPool.DrainBackend(backendAddr)
	if err != nil {
		http.Error(w, "Failed to drain backend", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":             "success",
		"backend":            backendAddr,
		"action":             "drain",
		"active_connections": activeConns,
		"message":            "Draining initiated for " + backendAddr,
		"timestamp":          time.Now().UTC(),
	})
}

// handleBackendCancelDrain cancels draining for a backend.
func (s *Server) handleBackendCancelDrain(w http.ResponseWriter, r *http.Request, backendAddr string) {
	// Find the pool that has this backend
	var targetPool *pool.Pool
	for _, p := range s.poolMgr.ListPools() {
		for _, b := range p.GetBackends() {
			if b.Address() == backendAddr {
				targetPool = p
				break
			}
		}
		if targetPool != nil {
			break
		}
	}

	if targetPool == nil {
		http.Error(w, "Backend not found", http.StatusNotFound)
		return
	}

	err := targetPool.CancelDrain(backendAddr)
	if err != nil {
		http.Error(w, "Failed to cancel drain", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "success",
		"backend":   backendAddr,
		"action":    "cancel-drain",
		"message":   "Drain cancelled for " + backendAddr,
		"timestamp": time.Now().UTC(),
	})
}

// handleQueries handles query log statistics.
func (s *Server) handleQueries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Aggregate query stats from all listeners
	var totalQueries int64
	var slowQueries int64
	var cachedQueries int64

	for _, l := range s.listeners {
		if ql := l.QueryLogger(); ql != nil {
			stats := ql.GetStats(time.Now().Add(-24 * time.Hour))
			totalQueries += int64(stats.TotalQueries)
			slowQueries += int64(stats.SlowQueries)
			cachedQueries += int64(stats.CachedQueries)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total_queries":  totalQueries,
		"slow_queries":   slowQueries,
		"cached_queries": cachedQueries,
		"timestamp":      time.Now().UTC(),
	})
}

// handleTransactions handles transaction statistics.
func (s *Server) handleTransactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Aggregate transaction stats from all listeners
	var activeTransactions int
	var totalTransactions int
	var abortedTransactions int

	for _, l := range s.listeners {
		if tm := l.TransactionManager(); tm != nil {
			stats := tm.GetStats()
			activeTransactions += stats.ActiveCount
			totalTransactions += stats.TotalCount
			abortedTransactions += stats.AbortedCount
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"active_transactions": activeTransactions,
		"total_transactions":  totalTransactions,
		"aborted_count":       abortedTransactions,
		"timestamp":           time.Now().UTC(),
	})
}

// handleSlowQueries handles slow query listing.
func (s *Server) handleSlowQueries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get limit from query params
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	// Collect slow queries from all listeners
	slowQueries := make([]map[string]interface{}, 0)

	for _, l := range s.listeners {
		if ql := l.QueryLogger(); ql != nil {
			entries := ql.GetSlowQueries(limit)
			for _, entry := range entries {
				slowQueries = append(slowQueries, map[string]interface{}{
					"query_id":      entry.QueryID,
					"query":         entry.Query,
					"duration_ms":   entry.Duration.Milliseconds(),
					"timestamp":     entry.Timestamp.UTC(),
					"pool":          entry.Pool,
					"client_addr":   entry.ClientAddr,
					"rows_returned": entry.RowsReturned,
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"slow_queries": slowQueries,
		"limit":        limit,
		"timestamp":    time.Now().UTC(),
	})
}

// handleMetrics returns Prometheus-compatible metrics.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	var output strings.Builder

	// Helper function to write metric
	writeMetric := func(name, help, metricType string, value interface{}, labels ...string) {
		output.WriteString(fmt.Sprintf("# HELP %s %s\n", name, help))
		output.WriteString(fmt.Sprintf("# TYPE %s %s\n", name, metricType))
		if len(labels) > 0 && len(labels)%2 == 0 {
			labelPairs := make([]string, 0, len(labels)/2)
			for i := 0; i < len(labels); i += 2 {
				labelPairs = append(labelPairs, fmt.Sprintf("%s=\"%s\"", labels[i], labels[i+1]))
			}
			output.WriteString(fmt.Sprintf("%s{%s} %v\n", name, strings.Join(labelPairs, ","), value))
		} else {
			output.WriteString(fmt.Sprintf("%s %v\n", name, value))
		}
	}

	// Pool metrics (spec §9.1 names)
	pools := s.poolMgr.ListPools()
	writeMetric("geryon_pools_total", "Total number of pools", "gauge", len(pools))

	var totalClientConns int64
	var totalServerConns int64
	var totalQueries int64
	var totalTransactions int64
	var totalCacheHits uint64
	var totalCacheMisses uint64
	var totalCacheEvictions uint64
	var totalCacheMemory int64
	var totalCacheEntries int

	for _, p := range pools {
		stats := p.Stats()
		poolName := stats.Name

		// Spec §9.1 pool metric names
		writeMetric("geryon_pool_client_connections_active", "Current active client connections", "gauge",
			stats.ClientConnections, "pool", poolName)
		writeMetric("geryon_pool_client_connections_waiting", "Clients waiting for server connection", "gauge",
			stats.WaitingClients, "pool", poolName)
		writeMetric("geryon_pool_server_connections_active", "Server connections in use", "gauge",
			stats.ActiveConnections, "pool", poolName)
		writeMetric("geryon_pool_server_connections_idle", "Idle server connections", "gauge",
			stats.IdleConnections, "pool", poolName)
		writeMetric("geryon_pool_server_connections_total", "Total server connections (active + idle)", "gauge",
			stats.ServerConnections, "pool", poolName)
		writeMetric("geryon_pool_queries_total", "Total queries processed", "counter",
			stats.TotalQueries, "pool", poolName)
		writeMetric("geryon_pool_transactions_total", "Total transactions", "counter",
			stats.TotalTransactions, "pool", poolName)

		// Query cache metrics per pool (spec §9.1 cache metrics)
		writeMetric("geryon_cache_hits_total", "Cache hits", "counter",
			stats.QueryCacheHits, "pool", poolName)
		writeMetric("geryon_cache_misses_total", "Cache misses", "counter",
			stats.QueryCacheMisses, "pool", poolName)
		writeMetric("geryon_cache_evictions_total", "Cache evictions", "counter",
			stats.QueryCacheEvictions, "pool", poolName)
		writeMetric("geryon_cache_memory_bytes", "Cache memory usage", "gauge",
			stats.QueryCacheMemoryUsed, "pool", poolName)
		writeMetric("geryon_cache_entries", "Number of cached entries", "gauge",
			stats.QueryCacheEntries, "pool", poolName)

		// Prepared statement cache metrics
		writeMetric("geryon_pool_prepared_cache_size", "Prepared statement cache size", "gauge",
			stats.PreparedStmtCacheSize, "pool", poolName)
		writeMetric("geryon_pool_prepared_cache_hit_rate", "Prepared statement cache hit rate", "gauge",
			fmt.Sprintf("%.2f", stats.PreparedStmtHitRate), "pool", poolName)

		// Backend metrics (spec §9.1 backend metrics)
		for _, b := range p.ListBackends() {
			healthStatus := 0
			if b.Healthy.Load() {
				healthStatus = 1
			}
			if b.Draining.Load() {
				healthStatus = 2 // degraded
			}
			writeMetric("geryon_backend_status", "Backend health: 0=down, 1=up, 2=degraded", "gauge",
				healthStatus, "pool", poolName, "backend", b.Address(), "role", b.Role)
			writeMetric("geryon_backend_connections", "Connection count per backend", "gauge",
				b.ConnCount.Load(), "pool", poolName, "backend", b.Address(), "role", b.Role)
		}

		totalClientConns += stats.ClientConnections
		totalServerConns += int64(stats.ServerConnections)
		totalQueries += stats.TotalQueries
		totalTransactions += stats.TotalTransactions
		totalCacheHits += stats.QueryCacheHits
		totalCacheMisses += stats.QueryCacheMisses
		totalCacheEvictions += stats.QueryCacheEvictions
		totalCacheMemory += stats.QueryCacheMemoryUsed
		totalCacheEntries += stats.QueryCacheEntries
	}

	// Global metrics
	writeMetric("geryon_connections_total", "Total client connections", "gauge", totalClientConns)
	writeMetric("geryon_server_connections_total", "Total server connections", "gauge", totalServerConns)
	writeMetric("geryon_queries_total", "Total queries processed", "counter", totalQueries)
	writeMetric("geryon_transactions_total", "Total transactions processed", "counter", totalTransactions)

	// Aggregate cache metrics
	writeMetric("geryon_cache_hits_total", "Total cache hits", "counter", totalCacheHits)
	writeMetric("geryon_cache_misses_total", "Total cache misses", "counter", totalCacheMisses)
	writeMetric("geryon_cache_evictions_total", "Total cache evictions", "counter", totalCacheEvictions)
	writeMetric("geryon_cache_memory_bytes", "Total cache memory usage", "gauge", totalCacheMemory)
	writeMetric("geryon_cache_entries", "Total cached entries", "gauge", totalCacheEntries)

	// Query log metrics
	var totalSlowQueries int64
	var totalCachedQueries int64
	for _, l := range s.listeners {
		if ql := l.QueryLogger(); ql != nil {
			stats := ql.GetStats(time.Now().Add(-24 * time.Hour))
			totalSlowQueries += int64(stats.SlowQueries)
			totalCachedQueries += int64(stats.CachedQueries)
		}
	}
	writeMetric("geryon_slow_queries_total", "Total slow queries", "counter", totalSlowQueries)
	writeMetric("geryon_cached_queries_total", "Total cached queries", "counter", totalCachedQueries)

	// Transaction metrics
	var activeTransactions int
	var abortedTransactions int
	for _, l := range s.listeners {
		if tm := l.TransactionManager(); tm != nil {
			stats := tm.GetStats()
			activeTransactions += stats.ActiveCount
			abortedTransactions += stats.AbortedCount
		}
	}
	writeMetric("geryon_transactions_active", "Active transactions", "gauge", activeTransactions)
	writeMetric("geryon_transactions_aborted_total", "Total aborted transactions", "counter", abortedTransactions)

	// Cluster metrics (spec §9.1 cluster metrics)
	if s.cluster != nil {
		state := s.cluster.StateString()
		nodeCount := s.cluster.GetNodeCount()
		term := s.cluster.GetTerm()

		stateValue := 0.0
		switch state {
		case "leader":
			stateValue = 1.0
		case "follower":
			stateValue = 2.0
		case "candidate":
			stateValue = 3.0
		}

		writeMetric("geryon_cluster_nodes", "Number of known nodes", "gauge", nodeCount)
		writeMetric("geryon_cluster_raft_state", "Raft state: 1=leader, 2=follower, 3=candidate", "gauge", stateValue)
		writeMetric("geryon_cluster_raft_term", "Current Raft term", "gauge", term)
	}

	w.Write([]byte(output.String()))
}

// handleRecentQueries returns recent queries.
func (s *Server) handleRecentQueries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get limit from query params
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	// Aggregate recent queries from all listeners
	recentQueries := make([]map[string]interface{}, 0)

	for _, l := range s.listeners {
		if ql := l.QueryLogger(); ql != nil {
			entries := ql.GetRecentQueries(limit)
			for _, entry := range entries {
				recentQueries = append(recentQueries, map[string]interface{}{
					"query_id":      entry.QueryID,
					"query":         entry.Query,
					"duration_ms":   entry.Duration.Milliseconds(),
					"timestamp":     entry.Timestamp.UTC(),
					"pool":          entry.Pool,
					"client_addr":   entry.ClientAddr,
					"is_cached":     entry.IsCached,
					"rows_returned": entry.RowsReturned,
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"recent_queries": recentQueries,
		"limit":          limit,
		"timestamp":      time.Now().UTC(),
	})
}

// handleActiveTransactions returns active transaction details.
func (s *Server) handleActiveTransactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	activeTxns := make([]map[string]interface{}, 0)

	for _, l := range s.listeners {
		if tm := l.TransactionManager(); tm != nil {
			txns := tm.GetActiveTransactions()
			for _, txn := range txns {
				activeTxns = append(activeTxns, map[string]interface{}{
					"id":             txn.ID,
					"session_id":     txn.SessionID,
					"server_conn_id": txn.ServerConnID,
					"start_time":     txn.StartTime.UTC(),
					"last_activity":  txn.LastActivity.UTC(),
					"query_count":    txn.QueryCount.Load(),
					"status":         txn.Status.String(),
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"active_transactions": activeTxns,
		"count":               len(activeTxns),
		"timestamp":           time.Now().UTC(),
	})
}

// handleConfig returns current configuration.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Return sanitized configuration (without sensitive data)
	pools := s.poolMgr.ListPools()
	poolConfigs := make([]map[string]interface{}, 0, len(pools))

	for _, p := range pools {
		stats := p.Stats()
		poolConfigs = append(poolConfigs, map[string]interface{}{
			"name":     stats.Name,
			"mode":     stats.Mode,
			"body":     p.Codec().Protocol(),
			"backends": stats.BackendCount,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"pools":     poolConfigs,
		"timestamp": time.Now().UTC(),
	})
}

// handleUsers lists users (GET) or creates a new user (POST).
func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListUsers(w, r)
	case http.MethodPost:
		s.handleCreateUser(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListUsers returns all configured users.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users := s.userDB.ListUsers()
	result := make([]map[string]interface{}, 0, len(users))
	for _, u := range users {
		result = append(result, map[string]interface{}{
			"username":        u.Username,
			"max_connections": u.MaxConnections,
			"default_pool":    u.DefaultPool,
			"allowed_pools":   u.AllowedPools,
			"has_password":    u.PasswordHash != "",
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"users":     result,
		"count":     len(result),
		"timestamp": time.Now().UTC(),
	})
}

// handleCreateUser adds a new user.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username       string   `json:"username"`
		PasswordHash   string   `json:"password_hash"`
		MaxConnections int      `json:"max_connections"`
		DefaultPool    string   `json:"default_pool"`
		AllowedPools   []string `json:"allowed_pools"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "Username is required")
		return
	}
	if req.PasswordHash == "" {
		writeError(w, http.StatusBadRequest, "Password hash is required")
		return
	}

	user := &auth.User{
		Username:       req.Username,
		PasswordHash:   req.PasswordHash,
		MaxConnections: req.MaxConnections,
		DefaultPool:    req.DefaultPool,
		AllowedPools:   req.AllowedPools,
	}
	if err := s.userDB.AddUser(user); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status":    "created",
		"username":  user.Username,
		"timestamp": time.Now().UTC(),
	})
}

// handleUserDetail deletes a user (DELETE).
func (s *Server) handleUserDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 || parts[4] == "" {
		writeError(w, http.StatusBadRequest, "Username is required")
		return
	}
	username := parts[4]
	if err := s.userDB.RemoveUser(username); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "deleted",
		"username":  username,
		"timestamp": time.Now().UTC(),
	})
}

// handleTLSStatus returns TLS configuration status.
func (s *Server) handleTLSStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check TLS status for all listeners
	tlsStatus := make([]map[string]interface{}, 0)

	for _, l := range s.listeners {
		cfg := l.Config()
		tlsStatus = append(tlsStatus, map[string]interface{}{
			"pool":      cfg.Name,
			"tls_mode":  cfg.TLS.Mode,
			"enabled":   cfg.TLS.Mode != "disable" && cfg.TLS.Mode != "",
			"cert_file": cfg.TLS.CertFile,
			"ca_file":   cfg.TLS.CAFile,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tls_status": tlsStatus,
		"timestamp":  time.Now().UTC(),
	})
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
