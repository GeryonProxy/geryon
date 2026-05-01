// Package rest provides the REST API server for the Geryon proxy,
// offering full CRUD for pools, backends, connections, users, and cache,
// plus Prometheus metrics, SSE streaming stats, and config hot-reload.
package rest

import (
	"bytes"
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
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"golang.org/x/time/rate"

	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/cluster"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
	"github.com/GeryonProxy/geryon/internal/proxy"
)

//go:embed static/*
var staticFS embed.FS

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

const (
	// usernameContextKey is stored in context after successful auth.
	usernameContextKey contextKey = "username"
)

// Cluster provides cluster state for metrics export.
type Cluster interface {
	StateString() string
	GetNodeCount() int
	GetTerm() uint64
	GetNodes() []*cluster.Node
	GetLeader() string
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
	userMu     sync.Mutex // serializes user persistence operations
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
	mux.HandleFunc("/api/v1/pools/{poolName}/backends", s.handlePoolBackends)
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
	mux.HandleFunc("/api/v1/stats/users", s.handleUserStats)
	mux.HandleFunc("/api/v1/stats/clients", s.handleClientStats)
	mux.HandleFunc("/api/v1/transactions", s.handleTransactions)
	mux.HandleFunc("/api/v1/transactions/active", s.handleActiveTransactions)
	mux.HandleFunc("/api/v1/config", s.handleConfig)
	mux.HandleFunc("/api/v1/config/reload", s.handleConfigReload)
	mux.HandleFunc("/api/v1/config/validate", s.handleConfigValidate)
	mux.HandleFunc("/api/v1/config/file", s.handleConfigFile)
	mux.HandleFunc("/api/v1/users", s.handleUsers)
	mux.HandleFunc("/api/v1/users/", s.handleUserDetail)
	mux.HandleFunc("/api/v1/tls/status", s.handleTLSStatus)
	mux.HandleFunc("/api/v1/cluster", s.handleCluster)

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
		IdleTimeout:  60 * time.Second,
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
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
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
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, X-CSRF-Token")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// H-3 fix: CSRF protection for state-changing requests
		// Reject browser-safe Content-Types that don't trigger CORS preflight.
		// Browsers can send application/x-www-form-urlencoded, multipart/form-data,
		// and text/plain cross-origin without preflight — these are the attack vector.
		if r.Method == http.MethodPost || r.Method == http.MethodPut ||
			r.Method == http.MethodDelete || r.Method == http.MethodPatch {
			// Layer 1: Require X-Requested-With header
				if r.Header.Get("X-Requested-With") == "" {
					http.Error(w, "Forbidden: missing X-Requested-With header", http.StatusForbidden)
					return
				}

				// Layer 3: Origin check: when AllowedOrigins is empty, accept same-origin requests
			// (Origin matching the request's own scheme+Host).
			if origin != "" && !s.isAllowedOrigin(origin) {
				// Same-origin check: Origin should match our Host
				hostOrigin := "http://" + r.Host
				hostOriginHTTPS := "https://" + r.Host
				if origin != hostOrigin && origin != hostOriginHTTPS {
					http.Error(w, "Forbidden: origin not allowed", http.StatusForbidden)
					return
				}
			}
			ct := r.Header.Get("Content-Type")
			if ct != "" {
				lower := strings.ToLower(ct)
				switch {
				case strings.HasPrefix(lower, "application/json"):
				case strings.HasPrefix(lower, "application/yaml"):
				case strings.HasPrefix(lower, "text/yaml"):
				case strings.HasPrefix(lower, "application/octet-stream"):
				default:
					// Reject browser-safe content types (CSRF attack vector)
					if strings.HasPrefix(lower, "application/x-www-form-urlencoded") ||
						strings.HasPrefix(lower, "multipart/form-data") ||
						strings.HasPrefix(lower, "text/plain") {
						http.Error(w, "Forbidden: unsupported Content-Type", http.StatusForbidden)
						return
					}
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}

// withAuth adds authentication middleware.
// C-1 FIX: Admin APIs ALWAYS require authentication regardless of auth.enabled flag.
// The auth.enabled config option only controls proxy-client database authentication,
// not admin API access. Admin endpoints must always be authenticated.
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

		if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.config.Auth.Token)) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// M-8 FIX: Store username in context for rate limiting.
		// Auth token is the admin token, not a per-user token, so we use "admin" as username.
		ctx := context.WithValue(r.Context(), usernameContextKey, "admin")
		next.ServeHTTP(w, r.WithContext(ctx))
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

func (rl *rateLimiter) GetLimiter(key string) *rate.Limiter {
	// Check if key already has a limiter
	if limiter, ok := rl.limiters.Load(key); ok {
		rl.lastSeen.Store(key, time.Now()) // Update last seen
		return limiter.(*rate.Limiter)
	}

	// Evict oldest entry if at capacity
	if rl.size.Load() >= rl.maxSize.Load() {
		var oldestKey string
		var oldestTime time.Time
		rl.lastSeen.Range(func(k, value interface{}) bool {
			if last, ok := value.(time.Time); ok {
				if oldestKey == "" || last.Before(oldestTime) {
					oldestKey = k.(string)
					oldestTime = last
				}
			}
			return true
		})
		if oldestKey != "" {
			rl.limiters.Delete(oldestKey)
			rl.lastSeen.Delete(oldestKey)
			rl.size.Add(-1)
		}
	}

	// Create new limiter
	limiter := rate.NewLimiter(rl.rate, rl.burst)
	rl.limiters.Store(key, limiter)
	rl.lastSeen.Store(key, time.Now())
	rl.size.Add(1)
	return limiter
}

// withRateLimit adds rate limiting middleware per client IP.
// M-8 FIX: Uses composite key (IP:username) for authenticated requests.
func (s *Server) withRateLimit(next http.Handler) http.Handler {
	rl := newRateLimiter(10, 20) // 10 req/s, burst 20
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

		limiter := rl.GetLimiter(key)
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
	// Strip file paths (Windows and Unix)
	msg = fileStripRegex.ReplaceAllString(msg, "[PATH]")
	// Strip connection strings (host:port patterns)
	msg = connStripRegex.ReplaceAllString(msg, "[CONN]")
	// Truncate to prevent leaking sensitive context
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

var fileStripRegex = regexp.MustCompile(`(?:[a-zA-Z]:)?[/\\][\w./\\_-]+(?:\.\w+)?`)
var connStripRegex = regexp.MustCompile(`(?:[a-zA-Z0-9.-]+):(\d{1,5})`)

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

// getAuthMode determines auth mode for new pool creation (M-4 fix).
// Derives from existing listeners at runtime; falls back to config file.
// Returns empty string on total failure, causing pool creation to fail closed.
func (s *Server) getAuthMode() string {
	// First try: derive from existing listeners (they store auth mode)
	for _, l := range s.listeners {
		if am := l.AuthMode(); am != "" {
			return am
		}
	}
	// Second try: read from config file
	if s.configPath != "" {
		cfg, err := config.Load(s.configPath)
		if err == nil && cfg != nil {
			return cfg.Auth.Mode
		}
	}
	return "" // Fail closed: no listeners, no config
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
		// M-4 fix: Use a restricted request struct to prevent mass assignment
		// of system-controlled fields (AuthMode is set server-side)
		var req struct {
			Name         string                    `json:"name"`
			Body         string                    `json:"body"`
			Mode         string                    `json:"mode"`
			Listen       config.ListenConfig       `json:"listen"`
			Backend      config.BackendConfig      `json:"backend"`
			Limits       config.LimitConfig        `json:"limits"`
			Health       config.HealthConfig       `json:"health"`
			TLS          config.TLSConfig          `json:"tls"`
			Cache        config.CacheConfig        `json:"cache"`
			PreparedStmt config.PreparedStmtConfig `json:"prepared_stmt"`
			Routing      config.RoutingConfig      `json:"routing"`
			Transaction  config.TransactionConfig  `json:"transaction"`
			// AuthMode intentionally excluded - set server-side
		}
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

		// Build pool config with system-controlled fields set server-side
		poolCfg := &config.PoolConfig{
			Name:         req.Name,
			Body:         req.Body,
			Mode:         req.Mode,
			Listen:       req.Listen,
			Backend:      req.Backend,
			Limits:       req.Limits,
			Health:       req.Health,
			TLS:          req.TLS,
			Cache:        req.Cache,
			PreparedStmt: req.PreparedStmt,
			Routing:      req.Routing,
			Transaction:  req.Transaction,
			AuthMode:     s.getAuthMode(), // M-4 fix: set server-side from current config
		}

		if err := s.poolMgr.CreatePool(poolCfg); err != nil {
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

// handlePoolBackends manages backends for a specific pool.
func (s *Server) handlePoolBackends(w http.ResponseWriter, r *http.Request) {
	// Extract pool name from {poolName} path variable
	poolName := strings.TrimPrefix(r.URL.Path, "/api/v1/pools/")
	poolName = strings.TrimSuffix(poolName, "/backends")
	if poolName == "" {
		writeError(w, http.StatusBadRequest, "pool name required")
		return
	}

	p := s.poolMgr.GetPool(poolName)
	if p == nil {
		writeError(w, http.StatusNotFound, "pool not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		backends := p.GetBackends()
		result := make([]map[string]interface{}, 0, len(backends))
		for _, b := range backends {
			result = append(result, map[string]interface{}{
				"address":     b.Address(),
				"host":        b.Host,
				"port":        b.Port,
				"role":        b.Role,
				"weight":      b.Weight,
				"database":    b.Database,
				"healthy":     b.Healthy.Load(),
				"draining":    b.Draining.Load(),
				"connections": b.ConnCount.Load(),
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"backends": result})

	case http.MethodPost:
		var req struct {
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Role     string `json:"role"`
			Weight   int    `json:"weight"`
			Database string `json:"database"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+sanitizeErr(err))
			return
		}
		if req.Host == "" || req.Port <= 0 {
			writeError(w, http.StatusBadRequest, "host and port (positive integer) are required")
			return
		}
		if req.Role == "" {
			req.Role = "primary"
		}
		if req.Role != "primary" && req.Role != "replica" {
			writeError(w, http.StatusBadRequest, "invalid role: must be primary or replica")
			return
		}
		if req.Weight <= 0 {
			req.Weight = 1
		}
		if req.Database == "" {
			req.Database = ""
		}
		if err := p.AddBackend(req.Host, req.Port, req.Role, req.Weight, req.Database); err != nil {
			writeError(w, http.StatusBadRequest, sanitizeErr(err))
			return
		}
		// Refresh router on the listener for read/write splitting
		for _, l := range s.listeners {
			if l.Pool() == p {
				l.RefreshRouter()
				break
			}
		}
		s.log.Info("Backend added via API", "pool", poolName, "backend", fmt.Sprintf("%s:%d", req.Host, req.Port))
		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"status":  "success",
			"message": "Backend added",
			"address": fmt.Sprintf("%s:%d", req.Host, req.Port),
		})

	case http.MethodDelete:
		// Parse address from query param or body
		addr := r.URL.Query().Get("address")
		if addr == "" {
			var req struct {
				Address string `json:"address"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Address == "" {
				writeError(w, http.StatusBadRequest, "address query parameter or JSON body required")
				return
			}
			addr = req.Address
		}
		if err := p.RemoveBackend(addr); err != nil {
			writeError(w, http.StatusNotFound, sanitizeErr(err))
			return
		}
		// Refresh router on the listener for read/write splitting
		for _, l := range s.listeners {
			if l.Pool() == p {
				l.RefreshRouter()
				break
			}
		}
		s.log.Info("Backend removed via API", "pool", poolName, "backend", addr)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "success",
			"message": "Backend removed",
			"address": addr,
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

		// H-1 fix: prevent auth section modification via config file endpoint
		currentData, err := os.ReadFile(s.configPath)
		if err == nil {
			var currentCfg config.Config
			if err := yaml.Unmarshal(currentData, &currentCfg); err == nil {
				currentAuthYAML, _ := yaml.Marshal(currentCfg.Auth)
				newAuthYAML, _ := yaml.Marshal(testCfg.Auth)
				if !bytes.Equal(bytes.TrimSpace(currentAuthYAML), bytes.TrimSpace(newAuthYAML)) {
					writeError(w, http.StatusForbidden, "Auth section cannot be modified via this endpoint. Use user management API instead.")
					return
				}
			}
		}

		// Write to a temp file first, then rename for atomicity.
		// On Windows, os.Rename cannot overwrite an existing file. Instead of
		// deleting the original (which risks data loss if the retry fails), we
		// copy the temp file contents over the original using WriteFile.
		tmpPath := s.configPath + ".tmp"
		if err := os.WriteFile(tmpPath, data, 0600); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to write config: "+sanitizeErr(err))
			return
		}
		if err := os.Rename(tmpPath, s.configPath); err != nil {
			// Rename failed (likely Windows with existing file). Overwrite the
			// original contents instead of deleting it first.
			if writeErr := os.WriteFile(s.configPath, data, 0600); writeErr != nil {
				writeError(w, http.StatusInternalServerError, "Failed to save config: "+sanitizeErr(err))
				return
			}
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

// handleConfigValidate validates YAML config without saving it.
func (s *Server) handleConfigValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read request body: "+sanitizeErr(err))
		return
	}

	var testCfg config.Config
	if err := yaml.Unmarshal(data, &testCfg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid YAML: "+sanitizeErr(err))
		return
	}
	if err := config.Validate(&testCfg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid config: "+sanitizeErr(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "valid",
		"message":   "Configuration is valid",
		"timestamp": time.Now().UTC(),
	})
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

// handleUserStats returns per-user query statistics.
func (s *Server) handleUserStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Aggregate per-user stats from all listeners
	userMap := make(map[string]*logger.UserStats)
	for _, l := range s.listeners {
		if ql := l.QueryLogger(); ql != nil {
			for _, us := range ql.GetPerUserStats() {
				existing, ok := userMap[us.Username]
				if !ok {
					copy := us
					userMap[us.Username] = &copy
				} else {
					totalQ := existing.TotalQueries + us.TotalQueries
					if totalQ > 0 {
						avgNs := (float64(existing.AvgDuration)*float64(existing.TotalQueries) +
							float64(us.AvgDuration)*float64(us.TotalQueries)) / float64(totalQ)
						existing.AvgDuration = time.Duration(avgNs)
					}
					existing.TotalQueries += us.TotalQueries
					existing.SlowQueries += us.SlowQueries
					if us.MaxDuration > existing.MaxDuration {
						existing.MaxDuration = us.MaxDuration
					}
					if us.LastQuery.After(existing.LastQuery) {
						existing.LastQuery = us.LastQuery
					}
				}
			}
		}
	}

	users := make([]logger.UserStats, 0, len(userMap))
	for _, us := range userMap {
		users = append(users, *us)
	}
	sort.Slice(users, func(i, j int) bool {
		return users[i].TotalQueries > users[j].TotalQueries
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"users":     users,
		"timestamp": time.Now().UTC(),
	})
}

// handleClientStats returns per-client query statistics.
func (s *Server) handleClientStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Aggregate per-client stats from all listeners
	clientMap := make(map[string]*logger.ClientStats)
	for _, l := range s.listeners {
		if ql := l.QueryLogger(); ql != nil {
			for _, cs := range ql.GetPerClientStats() {
				key := cs.ClientAddr + "|" + cs.Username + "|" + cs.Pool
				existing, ok := clientMap[key]
				if !ok {
					copy := cs
					clientMap[key] = &copy
				} else {
					totalQ := existing.TotalQueries + cs.TotalQueries
					if totalQ > 0 {
						avgNs := (float64(existing.AvgDuration)*float64(existing.TotalQueries) +
							float64(cs.AvgDuration)*float64(cs.TotalQueries)) / float64(totalQ)
						existing.AvgDuration = time.Duration(avgNs)
					}
					existing.TotalQueries += cs.TotalQueries
					existing.SlowQueries += cs.SlowQueries
					if cs.MaxDuration > existing.MaxDuration {
						existing.MaxDuration = cs.MaxDuration
					}
					if cs.LastQuery.After(existing.LastQuery) {
						existing.LastQuery = cs.LastQuery
					}
				}
			}
		}
	}

	clients := make([]logger.ClientStats, 0, len(clientMap))
	for _, cs := range clientMap {
		clients = append(clients, *cs)
	}
	sort.Slice(clients, func(i, j int) bool {
		return clients[i].TotalQueries > clients[j].TotalQueries
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"clients":   clients,
		"timestamp": time.Now().UTC(),
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
	// Serialize persistence operations to prevent concurrent writes from
	// overwriting each other
	s.userMu.Lock()
	defer s.userMu.Unlock()
	// Check for duplicate inside the lock to prevent TOCTOU race
	if s.userDB.GetUser(req.Username) != nil {
		writeError(w, http.StatusConflict, "user "+req.Username+" already exists")
		return
	}
	// Persist to disk first to avoid inconsistency if save fails
	if err := s.saveUsersWithUser(user); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to persist user: "+sanitizeErr(err))
		return
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
	// Serialize persistence operations
	s.userMu.Lock()
	defer s.userMu.Unlock()
	// Persist to disk first (without this user) to avoid inconsistency
	if err := s.saveUsersWithoutUser(username); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to persist user deletion: "+sanitizeErr(err))
		return
	}
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

// saveUsers persists the current userDB to the config file.
func (s *Server) saveUsers() error {
	return s.saveUsersMutate(nil, "")
}

// saveUsersWithUser persists userDB plus one additional user (for create).
func (s *Server) saveUsersWithUser(newUser *auth.User) error {
	return s.saveUsersMutate(newUser, "")
}

// saveUsersWithoutUser persists userDB minus one user (for delete).
func (s *Server) saveUsersWithoutUser(username string) error {
	return s.saveUsersMutate(nil, username)
}

func (s *Server) saveUsersMutate(addUser *auth.User, removeUser string) error {
	if s.configPath == "" {
		// No config file — user changes are in-memory only.
		// This is expected in test/embedded deployments.
		return nil
	}

	cfg, err := config.Load(s.configPath)
	if err != nil {
		return err
	}

	users := s.userDB.ListUsers()
	cfg.Auth.Users = make([]config.User, 0, len(users)+1)
	for _, u := range users {
		if removeUser != "" && u.Username == removeUser {
			continue
		}
		cfg.Auth.Users = append(cfg.Auth.Users, config.User{
			Username:          u.Username,
			PasswordHash:      u.PasswordHash,
			MysqlPasswordHash: u.MysqlPasswordHash,
			MaxConnections:    u.MaxConnections,
			DefaultPool:       u.DefaultPool,
			AllowedPools:      u.AllowedPools,
		})
	}
	if addUser != nil {
		cfg.Auth.Users = append(cfg.Auth.Users, config.User{
			Username:          addUser.Username,
			PasswordHash:      addUser.PasswordHash,
			MysqlPasswordHash: addUser.MysqlPasswordHash,
			MaxConnections:    addUser.MaxConnections,
			DefaultPool:       addUser.DefaultPool,
			AllowedPools:      addUser.AllowedPools,
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
	if err := os.Rename(tmpPath, s.configPath); err != nil {
		// Windows: overwrite original instead of deleting it
		if writeErr := os.WriteFile(s.configPath, data, 0600); writeErr != nil {
			return fmt.Errorf("rename failed and write fallback failed: %v (original: %v)", err, writeErr)
		}
	}
	return nil
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

// handleCluster returns cluster status.
func (s *Server) handleCluster(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	c := s.cluster
	s.mu.RUnlock()

	if c != nil {
		nodes := c.GetNodes()
		nodeList := make([]map[string]interface{}, 0, len(nodes))
		for _, n := range nodes {
			nodeList = append(nodeList, map[string]interface{}{
				"id":        n.ID,
				"healthy":   n.State != cluster.NodeStateDead,
				"last_seen": n.LastSeen,
			})
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":    c.StateString(),
			"leader":    c.GetLeader(),
			"nodes":     nodeList,
			"term":      c.GetTerm(),
			"timestamp": time.Now().UTC(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "disabled",
		"message": "Clustering is not enabled for this node.",
		"nodes":   []interface{}{},
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
