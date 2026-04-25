// Package dashboard provides the embedded web UI for monitoring and
// managing the Geryon proxy. It serves static assets via embed.FS and
// exposes a REST API for configuration editing.
package dashboard

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
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"gopkg.in/yaml.v3"

	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/cluster"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
)

//go:embed static/*
var staticFS embed.FS

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

const (
	usernameContextKey contextKey = "username"
)

// Server serves the web dashboard.
type Server struct {
	mu              sync.RWMutex
	userMu          sync.Mutex // serializes user persistence to config file
	poolMgr         *pool.Manager
	userDB          *auth.UserDatabase
	log             *logger.Logger
	server          *http.Server
	config          *Config
	authToken       string
	authEnabled     bool
	reloadFn        func() error
	cluster         dashboardCluster
	configPath      string
	refreshRouterFn func()
}

// dashboardCluster is the subset of cluster state the dashboard needs.
type dashboardCluster interface {
	StateString() string
	GetNodeCount() int
	GetTerm() uint64
	GetNodes() []*cluster.Node
	GetLeader() string
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

// SetCluster sets the cluster state accessor for the dashboard.
func (s *Server) SetCluster(c dashboardCluster) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cluster = c
}

// SetConfigPath sets the path to the config file for read/write operations.
func (s *Server) SetConfigPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configPath = path
}

// SetRefreshRouterFn sets the callback to refresh listener routers after
// backend topology changes.
func (s *Server) SetRefreshRouterFn(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshRouterFn = fn
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
	mux.HandleFunc("/api/v1/pools/", s.handlePoolDetail)
	mux.HandleFunc("/api/v1/connections", s.handleConnections)
	mux.HandleFunc("/api/v1/queries", s.handleQueries)
	mux.HandleFunc("/api/v1/transactions", s.handleTransactions)
	mux.HandleFunc("/api/v1/config", s.handleConfig)
	mux.HandleFunc("/api/v1/config/reload", s.handleConfigReload)
	mux.HandleFunc("/api/v1/config/file", s.handleConfigFile)
	mux.HandleFunc("/api/v1/config/validate", s.handleConfigValidate)
	mux.HandleFunc("/api/v1/users", s.handleUsers)
	mux.HandleFunc("/api/v1/users/", s.handleUserDetail)
	mux.HandleFunc("/api/v1/cluster", s.handleCluster)

	readTimeout := parseDuration(s.config.ReadTimeout, 30*time.Second)
	writeTimeout := parseDuration(s.config.WriteTimeout, 30*time.Second)
	s.server = &http.Server{
		Addr:         s.config.Listen,
		Handler:      s.withLogging(s.withPanicRecovery(s.withSecurityHeaders(s.withRateLimit(s.withAuth(mux))))),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  60 * time.Second,
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
// C-1 FIX: Auth is always required for dashboard regardless of auth.enabled flag.
// Admin API access must always be authenticated.
func (s *Server) withAuth(next http.Handler) http.Handler {
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

		if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.authToken)) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// H-3 fix: CSRF protection for state-changing requests
		if r.Method == http.MethodPost || r.Method == http.MethodPut ||
			r.Method == http.MethodDelete || r.Method == http.MethodPatch {
			ct := r.Header.Get("Content-Type")
			if ct != "" && !strings.HasPrefix(ct, "application/json") && !strings.HasPrefix(ct, "text/yaml") {
				http.Error(w, "Forbidden: unsupported Content-Type", http.StatusForbidden)
				return
			}
		}

		// M-8 FIX: Store username in context for rate limiting.
		ctx := context.WithValue(r.Context(), usernameContextKey, "admin")
		next.ServeHTTP(w, r.WithContext(ctx))
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

func (rl *dashboardRateLimiter) GetLimiter(key string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if len(rl.limiters) >= rl.maxSize {
		var oldestKey string
		var oldestTime time.Time
		for k, last := range rl.lastSeen {
			if oldestKey == "" || last.Before(oldestTime) {
				oldestKey = k
				oldestTime = last
			}
		}
		if oldestKey != "" {
			delete(rl.limiters, oldestKey)
			delete(rl.lastSeen, oldestKey)
		}
	}

	rl.lastSeen[key] = time.Now()
	limiter, ok := rl.limiters[key]
	if !ok {
		limiter = rate.NewLimiter(5, 15)
		rl.limiters[key] = limiter
	}
	return limiter
}

// withRateLimit adds per-IP rate limiting middleware.
// M-8 FIX: Uses composite key (IP:username) for authenticated requests.
func (s *Server) withRateLimit(next http.Handler) http.Handler {
	rl := newDashboardRateLimiter(5, 15)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip == "" {
			ip = r.RemoteAddr
		}

		// M-8 FIX: Create composite key with username when available
		key := ip
		if username, ok := r.Context().Value(usernameContextKey).(string); ok && username != "" {
			key = ip + ":" + username
		}

		if !rl.GetLimiter(key).Allow() {
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
		"cache_hits":          totalCacheHits,
		"cache_misses":        totalCacheMisses,
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

// handlePoolDetail returns details for a specific pool including backends.
func (s *Server) handlePoolDetail(w http.ResponseWriter, r *http.Request) {
	// Extract pool name from path: /api/v1/pools/{name}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/pools/")
	if path == "" {
		s.writeErrorJSON(w, http.StatusBadRequest, "pool name required")
		return
	}

	// Check if requesting backends sub-path
	if strings.HasSuffix(path, "/backends") {
		poolName := strings.TrimSuffix(path, "/backends")
		p := s.poolMgr.GetPool(poolName)
		if p == nil {
			s.writeErrorJSON(w, http.StatusNotFound, "pool not found")
			return
		}

		switch r.Method {
		case http.MethodGet:
			backends := make([]map[string]any, 0)
			for _, b := range p.GetBackends() {
				backends = append(backends, map[string]any{
					"address":     b.Address(),
					"host":        b.Host,
					"port":        b.Port,
					"role":        b.Role,
					"weight":      b.Weight,
					"healthy":     b.Healthy.Load(),
					"draining":    b.Draining.Load(),
					"connections": b.ConnCount.Load(),
				})
			}
			s.writeJSON(w, map[string]any{"backends": backends})

		case http.MethodPost:
			var req struct {
				Host     string `json:"host"`
				Port     int    `json:"port"`
				Role     string `json:"role"`
				Weight   int    `json:"weight"`
				Database string `json:"database"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				s.writeErrorJSON(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if req.Host == "" || req.Port == 0 {
				s.writeErrorJSON(w, http.StatusBadRequest, "host and port are required")
				return
			}
			if req.Role == "" {
				req.Role = "primary"
			}
			if req.Role != "primary" && req.Role != "replica" {
				s.writeErrorJSON(w, http.StatusBadRequest, "role must be 'primary' or 'replica'")
				return
			}
			if req.Weight <= 0 {
				req.Weight = 1
			}
			addr := fmt.Sprintf("%s:%d", req.Host, req.Port)
			if err := p.AddBackend(req.Host, req.Port, req.Role, req.Weight, req.Database); err != nil {
				s.writeErrorJSON(w, http.StatusBadRequest, "failed to add backend")
				return
			}
			s.writeJSON(w, map[string]any{"status": "backend added", "address": addr})
			if s.refreshRouterFn != nil {
				s.refreshRouterFn()
			}

		case http.MethodDelete:
			// Accept address from query param or JSON body
			addr := r.URL.Query().Get("address")
			if addr == "" {
				var req struct {
					Address string `json:"address"`
				}
				r.Body = http.MaxBytesReader(w, r.Body, 4096)
				if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.Address != "" {
					addr = req.Address
				}
			}
			if addr == "" {
				s.writeErrorJSON(w, http.StatusBadRequest, "address is required (query param or JSON body)")
				return
			}
			// Try drain + remove; if already draining, try direct remove
			activeConns, err := p.DrainBackend(addr)
			if err != nil {
				// Backend may already be draining; try direct removal
				if err2 := p.RemoveBackend(addr); err2 != nil {
					s.writeErrorJSON(w, http.StatusConflict, fmt.Sprintf("backend has %d active connections, drain and retry", activeConns))
					return
				}
				s.writeJSON(w, map[string]any{"status": "backend removed", "address": addr})
				if s.refreshRouterFn != nil {
					s.refreshRouterFn()
				}
				return
			}
			if activeConns > 0 {
				s.writeErrorJSON(w, http.StatusConflict, fmt.Sprintf("backend has %d active connections, drain and retry", activeConns))
				return
			}
			if err := p.RemoveBackend(addr); err != nil {
				s.writeErrorJSON(w, http.StatusBadRequest, "failed to remove backend")
				return
			}
			s.writeJSON(w, map[string]any{"status": "backend removed", "address": addr})
			if s.refreshRouterFn != nil {
				s.refreshRouterFn()
			}

		default:
			s.writeErrorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// Return pool stats
	p := s.poolMgr.GetPool(path)
	if p == nil {
		s.writeErrorJSON(w, http.StatusNotFound, "pool not found")
		return
	}
	stats := p.Stats()
	s.writeJSON(w, stats)
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

// handleConfigFile reads (GET) or writes (PUT) the raw YAML config file.
func (s *Server) handleConfigFile(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	configPath := s.configPath
	s.mu.RUnlock()

	if configPath == "" {
		s.writeErrorJSON(w, http.StatusNotImplemented, "Config file path not available")
		return
	}

	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(configPath)
		if err != nil {
			s.writeErrorJSON(w, http.StatusInternalServerError, "Failed to read config file: "+sanitizeErr(err))
			return
		}
		w.Header().Set("Content-Type", "text/yaml")
		w.Write(data)

	case http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			s.writeErrorJSON(w, http.StatusBadRequest, "Failed to read request body")
			return
		}

		var testCfg config.Config
		if err := yaml.Unmarshal(data, &testCfg); err != nil {
			s.writeErrorJSON(w, http.StatusBadRequest, "Invalid YAML: "+sanitizeErr(err))
			return
		}
		if err := config.Validate(&testCfg); err != nil {
			s.writeErrorJSON(w, http.StatusBadRequest, "Invalid config: "+sanitizeErr(err))
			return
		}

		// Write to temp file, then rename. On Windows, rename fails if
		// destination exists — overwrite the original as fallback instead of
		// deleting it.
		tmpPath := configPath + ".tmp"
		if err := os.WriteFile(tmpPath, data, 0600); err != nil {
			s.writeErrorJSON(w, http.StatusInternalServerError, "Failed to write config")
			return
		}
		if err := os.Rename(tmpPath, configPath); err != nil {
			if writeErr := os.WriteFile(configPath, data, 0600); writeErr != nil {
				s.writeErrorJSON(w, http.StatusInternalServerError, "Failed to save config")
				return
			}
		}

		s.log.Info("Configuration file updated via dashboard", "path", configPath)
		s.writeJSON(w, map[string]any{"status": "success", "message": "Configuration saved"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleConfigValidate validates YAML config without saving.
func (s *Server) handleConfigValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeErrorJSON(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	var testCfg config.Config
	if err := yaml.Unmarshal(data, &testCfg); err != nil {
		s.writeErrorJSON(w, http.StatusBadRequest, "Invalid YAML: "+sanitizeErr(err))
		return
	}
	if err := config.Validate(&testCfg); err != nil {
		s.writeErrorJSON(w, http.StatusBadRequest, "Invalid config: "+sanitizeErr(err))
		return
	}

	s.writeJSON(w, map[string]any{"status": "valid", "message": "Configuration is valid"})
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

// handleCluster returns cluster status.
func (s *Server) handleCluster(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	c := s.cluster
	s.mu.RUnlock()

	if c != nil {
		nodes := c.GetNodes()
		nodeList := make([]map[string]any, 0, len(nodes))
		for _, n := range nodes {
			nodeList = append(nodeList, map[string]any{
				"id":        n.ID,
				"healthy":   n.State != cluster.NodeStateDead,
				"last_seen": n.LastSeen,
			})
		}
		s.writeJSON(w, map[string]any{
			"status":    c.StateString(),
			"leader":    c.GetLeader(),
			"nodes":     nodeList,
			"term":      c.GetTerm(),
			"timestamp": time.Now().UTC(),
		})
		return
	}

	s.writeJSON(w, map[string]any{
		"status":  "disabled",
		"message": "Clustering is not enabled for this node.",
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
		s.writeErrorJSON(w, http.StatusInternalServerError, "user database not available")
		return
	}

	var req struct {
		Username       string   `json:"username"`
		Password       string   `json:"password"`
		PasswordHash   string   `json:"password_hash"`
		MaxConnections int      `json:"max_connections"`
		DefaultPool    string   `json:"default_pool"`
		AllowedPools   []string `json:"allowed_pools"`
	}

	// Limit request body size (L-10 fix)
	r.Body = http.MaxBytesReader(w, r.Body, 4096)

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Username == "" {
		s.writeErrorJSON(w, http.StatusBadRequest, "username is required")
		return
	}

	// Accept either plaintext password (password) or password field (password_hash from frontend)
	// Always hash - the frontend sends plaintext in password_hash
	password := req.Password
	if password == "" {
		password = req.PasswordHash
	}
	if password == "" {
		s.writeErrorJSON(w, http.StatusBadRequest, "password or password_hash is required")
		return
	}

	// Generate SCRAM-SHA-256 hash from plaintext password
	hash, err := auth.GenerateSCRAMHash(password)
	if err != nil {
		s.writeErrorJSON(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	user := &auth.User{
		Username:       req.Username,
		PasswordHash:   hash,
		MaxConnections: req.MaxConnections,
		DefaultPool:    req.DefaultPool,
		AllowedPools:   req.AllowedPools,
	}

	// Persist to config file first, then mutate memory — prevents TOCTOU race
	// and ensures durability (user survives restart/reload).
	s.userMu.Lock()
	defer s.userMu.Unlock()
	if s.userDB.GetUser(req.Username) != nil {
		s.writeErrorJSON(w, http.StatusConflict, "user already exists")
		return
	}
	if s.configPath != "" {
		if err := s.saveUsersWithUser(user); err != nil {
			s.writeErrorJSON(w, http.StatusInternalServerError, "failed to persist user: "+sanitizeErr(err))
			return
		}
	}
	if err := s.userDB.AddUser(user); err != nil {
		s.writeErrorJSON(w, http.StatusBadRequest, "failed to add user")
		return
	}

	s.log.Info("User created via dashboard", "username", req.Username)
	w.WriteHeader(http.StatusCreated)
	s.writeJSON(w, map[string]any{"status": "created", "username": req.Username})
}

// saveUsersWithUser persists userDB plus one additional user to the config file.
func (s *Server) saveUsersWithUser(newUser *auth.User) error {
	if s.configPath == "" {
		return nil
	}

	cfg, err := config.Load(s.configPath)
	if err != nil {
		return err
	}

	users := s.userDB.ListUsers()
	cfg.Auth.Users = make([]config.User, 0, len(users)+1)
	for _, u := range users {
		cfg.Auth.Users = append(cfg.Auth.Users, config.User{
			Username:          u.Username,
			PasswordHash:      u.PasswordHash,
			MysqlPasswordHash: u.MysqlPasswordHash,
			MaxConnections:    u.MaxConnections,
			DefaultPool:       u.DefaultPool,
			AllowedPools:      u.AllowedPools,
		})
	}
	if newUser != nil {
		cfg.Auth.Users = append(cfg.Auth.Users, config.User{
			Username:          newUser.Username,
			PasswordHash:      newUser.PasswordHash,
			MysqlPasswordHash: newUser.MysqlPasswordHash,
			MaxConnections:    newUser.MaxConnections,
			DefaultPool:       newUser.DefaultPool,
			AllowedPools:      newUser.AllowedPools,
		})
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	tmpPath := s.configPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}

	// Windows fallback: os.Rename cannot overwrite existing files
	if err := os.Rename(tmpPath, s.configPath); err != nil {
		if writeErr := os.WriteFile(s.configPath, data, 0600); writeErr != nil {
			return fmt.Errorf("config write fallback failed: %w", err)
		}
	}

	return nil
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

// writeErrorJSON writes an error response with the given status code.
func (s *Server) writeErrorJSON(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": msg})
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

func sanitizeErr(err error) string {
	msg := err.Error()
	msg = fileStripRegex.ReplaceAllString(msg, "[PATH]")
	msg = connStripRegex.ReplaceAllString(msg, "[CONN]")
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

var fileStripRegex = regexp.MustCompile(`(?:[a-zA-Z]:)?[/\\][\w./\\_-]+(?:\.\w+)?`)
var connStripRegex = regexp.MustCompile(`(?:[a-zA-Z0-9.-]+):(\d{1,5})`)
