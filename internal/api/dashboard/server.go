// Package dashboard provides the embedded web UI for monitoring and
// managing the Geryon proxy. It serves static assets via embed.FS and
// exposes a REST API for configuration editing.
package dashboard

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

//go:embed static/*
var staticFS embed.FS

// Server serves the web dashboard.
type Server struct {
	poolMgr     *pool.Manager
	userDB      *auth.UserDatabase
	log         *logger.Logger
	server      *http.Server
	config      *Config
	authToken   string
	authEnabled bool
	reloadFn    func() error
}

// Config holds dashboard configuration.
type Config struct {
	Enabled      bool
	Listen       string
	Path         string
	Auth         config.RESTAuthConfig
	ReadTimeout  string
	WriteTimeout string
}

// NewServer creates a new dashboard server.
func NewServer(cfg *Config, poolMgr *pool.Manager, log *logger.Logger, reloadFn func() error, userDB *auth.UserDatabase) *Server {
	return &Server{
		config:      cfg,
		poolMgr:     poolMgr,
		userDB:      userDB,
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
	mux.HandleFunc("/api/v1/transactions", s.handleTransactions)
	mux.HandleFunc("/api/v1/config", s.handleConfig)
	mux.HandleFunc("/api/v1/users", s.handleUsers)
	mux.HandleFunc("/api/v1/users/", s.handleUserDetail)

	readTimeout := parseDuration(s.config.ReadTimeout, 30*time.Second)
	writeTimeout := parseDuration(s.config.WriteTimeout, 30*time.Second)
	s.server = &http.Server{
		Addr:         s.config.Listen,
		Handler:      s.withLogging(s.withPanicRecovery(s.withSecurityHeaders(s.withRateLimit(s.withAuth(mux))))),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
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

// withPanicRecovery recovers from panics in handlers and returns 500.
func (s *Server) withPanicRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				s.log.Error("Panic recovered in dashboard handler", "error", err, "path", r.URL.Path)
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
			"name":                 stats.Name,
			"body":                 stats.Mode, // Use mode as body indicator
			"mode":                 stats.Mode,
			"client_connections":   stats.ClientConnections,
			"server_connections":   stats.ServerConnections,
			"backend_count":        stats.BackendCount,
			"query_cache_entries":  stats.QueryCacheEntries,
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
	var totalActiveConnections int
	var totalIdleConnections int
	var totalWaitingClients int
	var totalCacheEntries int

	for _, p := range s.poolMgr.ListPools() {
		stats := p.Stats()
		totalQueries += stats.TotalQueries
		totalTransactions += stats.TotalTransactions
		totalCacheHits += stats.QueryCacheHits
		totalCacheMisses += stats.QueryCacheMisses
		totalActiveConnections += stats.ActiveConnections
		totalIdleConnections += stats.IdleConnections
		totalWaitingClients += stats.WaitingClients
		totalCacheEntries += stats.QueryCacheEntries
	}

	// Calculate cache hit rate from actual counters
	cacheHitRate := 0.0
	totalCacheRequests := totalCacheHits + totalCacheMisses
	if totalCacheRequests > 0 {
		cacheHitRate = float64(totalCacheHits) / float64(totalCacheRequests) * 100
	}

	s.writeJSON(w, map[string]any{
		"total_connections":   totalActiveConnections + totalIdleConnections,
		"active_pools":        len(s.poolMgr.ListPools()),
		"queries_per_sec":     totalQueries,
		"cache_hit_rate":      cacheHitRate,
		"total_queries":       totalQueries,
		"total_transactions":  totalTransactions,
		"cached_queries":      totalCacheEntries,
		"active_transactions": totalActiveConnections,
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

	pools := s.poolMgr.ListPools()
	poolConfigs := make([]map[string]any, 0, len(pools))
	for _, p := range pools {
		stats := p.Stats()
		poolConfigs = append(poolConfigs, map[string]any{
			"name":               stats.Name,
			"mode":               stats.Mode,
			"client_connections": stats.ClientConnections,
			"server_connections": stats.ServerConnections,
			"backend_count":      stats.BackendCount,
			"total_queries":      stats.TotalQueries,
			"cache_hit_rate":     stats.QueryCacheHitRate,
		})
	}

	s.writeJSON(w, map[string]any{
		"pools":     poolConfigs,
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

// handleTransactions returns transaction statistics.
func (s *Server) handleTransactions(w http.ResponseWriter, r *http.Request) {
	var totalTransactions int64
	var activeTransactions int

	for _, p := range s.poolMgr.ListPools() {
		stats := p.Stats()
		totalTransactions += stats.TotalTransactions
		activeTransactions += stats.ActiveTransactions
	}

	s.writeJSON(w, map[string]any{
		"total_transactions":  totalTransactions,
		"active_transactions": activeTransactions,
		"aborted_count":       0,
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

// handleListUsers returns all users.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if s.userDB == nil {
		s.writeJSON(w, map[string]any{"users": []any{}})
		return
	}

	users := s.userDB.ListUsers()
	result := make([]map[string]any, 0, len(users))
	for _, u := range users {
		result = append(result, map[string]any{
			"username":        u.Username,
			"max_connections": u.MaxConnections,
			"default_pool":    u.DefaultPool,
			"allowed_pools":   u.AllowedPools,
		})
	}
	s.writeJSON(w, map[string]any{"users": result})
}

// handleCreateUser creates a new user.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if s.userDB == nil {
		s.writeJSON(w, map[string]any{"error": "user database not available"})
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var req struct {
		Username       string   `json:"username"`
		PasswordHash   string   `json:"password_hash"`
		MaxConnections int      `json:"max_connections"`
		DefaultPool    string   `json:"default_pool"`
		AllowedPools   []string `json:"allowed_pools"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, map[string]any{"error": "invalid request body"})
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if req.Username == "" {
		s.writeJSON(w, map[string]any{"error": "username is required"})
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if req.PasswordHash == "" {
		s.writeJSON(w, map[string]any{"error": "password_hash is required"})
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Check if user already exists
	if s.userDB.GetUser(req.Username) != nil {
		s.writeJSON(w, map[string]any{"error": "user already exists"})
		w.WriteHeader(http.StatusConflict)
		return
	}

	// Generate SCRAM-SHA-256 hash from plaintext password
	hash, err := auth.GenerateSCRAMHash(req.PasswordHash)
	if err != nil {
		s.writeJSON(w, map[string]any{"error": "failed to hash password: " + err.Error()})
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	user := &auth.User{
		Username:       req.Username,
		PasswordHash:   hash,
		MaxConnections: req.MaxConnections,
		DefaultPool:    req.DefaultPool,
		AllowedPools:   req.AllowedPools,
	}

	if err := s.userDB.AddUser(user); err != nil {
		s.writeJSON(w, map[string]any{"error": err.Error()})
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	s.log.Info("User created via dashboard", "username", req.Username)
	w.WriteHeader(http.StatusCreated)
	s.writeJSON(w, map[string]any{"status": "created", "username": req.Username})
}

// handleUserDetail returns or deletes a single user.
func (s *Server) handleUserDetail(w http.ResponseWriter, r *http.Request) {
	if s.userDB == nil {
		s.writeJSON(w, map[string]any{"error": "user database not available"})
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	username := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
	if username == "" {
		s.writeJSON(w, map[string]any{"error": "username required"})
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		user := s.userDB.GetUser(username)
		if user == nil {
			s.writeJSON(w, map[string]any{"error": "user not found"})
			w.WriteHeader(http.StatusNotFound)
			return
		}
		s.writeJSON(w, map[string]any{
			"username":        user.Username,
			"max_connections": user.MaxConnections,
			"default_pool":    user.DefaultPool,
			"allowed_pools":   user.AllowedPools,
		})
	case http.MethodDelete:
		if err := s.userDB.RemoveUser(username); err != nil {
			s.writeJSON(w, map[string]any{"error": err.Error()})
			w.WriteHeader(http.StatusNotFound)
			return
		}
		s.log.Info("User deleted via dashboard", "username", username)
		s.writeJSON(w, map[string]any{"status": "deleted", "username": username})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// writeJSON writes JSON response.
func (s *Server) writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
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
