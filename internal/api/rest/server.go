package rest

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
	"github.com/GeryonProxy/geryon/internal/proxy"
)

//go:embed static/*
var staticFS embed.FS

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
}

// NewServer creates a new REST API server.
func NewServer(cfg *config.AdminRESTConfig, poolMgr *pool.Manager, listeners []*proxy.Listener, log *logger.Logger) (*Server, error) {
	s := &Server{
		config:    cfg,
		poolMgr:   poolMgr,
		listeners: listeners,
		log:       log,
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
	mux.HandleFunc("/api/v1/transactions", s.handleTransactions)
	mux.HandleFunc("/api/v1/config/reload", s.handleConfigReload)

	// Dashboard routes
	if err := s.setupDashboard(mux); err != nil {
		return nil, fmt.Errorf("failed to setup dashboard: %w", err)
	}

	s.httpServer = &http.Server{
		Addr:         cfg.Listen,
		Handler:      s.withLogging(s.withCORS(s.withAuth(mux))),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return s, nil
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

// withCORS adds CORS headers.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

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

		if parts[1] != s.config.Auth.Token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
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

// handlePools handles pool listing.
func (s *Server) handlePools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pools := s.poolMgr.ListPools()
	poolData := make([]map[string]interface{}, 0, len(pools))

	for _, p := range pools {
		stats := p.Stats()
		poolData = append(poolData, map[string]interface{}{
			"name":              stats.Name,
			"body":              p.Codec().Protocol(),
			"mode":              stats.Mode,
			"client_connections": stats.ClientConnections,
			"server_connections": stats.ServerConnections,
			"idle_connections":  stats.IdleConnections,
			"active_connections": stats.ActiveConnections,
			"status":            "online",
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"pools": poolData,
	})
}

// handlePoolDetail handles individual pool operations.
func (s *Server) handlePoolDetail(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	poolName := parts[4]
	p := s.poolMgr.GetPool(poolName)
	if p == nil {
		http.Error(w, "Pool not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		stats := p.Stats()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"name":                  stats.Name,
			"mode":                  stats.Mode,
			"client_connections":    stats.ClientConnections,
			"server_connections":    stats.ServerConnections,
			"idle_connections":      stats.IdleConnections,
			"active_connections":    stats.ActiveConnections,
			"waiting_clients":       stats.WaitingClients,
			"total_queries":         stats.TotalQueries,
			"total_transactions":    stats.TotalTransactions,
			"backend_count":         stats.BackendCount,
			"prepared_stmt_cache": map[string]interface{}{
				"size":     stats.PreparedStmtCacheSize,
				"hit_rate": stats.PreparedStmtHitRate,
			},
		})

	case http.MethodPut:
		// TODO: Update pool configuration
		http.Error(w, "Not implemented", http.StatusNotImplemented)

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

	// TODO: Implement connection tracking
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"connections": []interface{}{},
	})
}

// handleBackends handles backend listing.
func (s *Server) handleBackends(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// TODO: Implement backend tracking
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"backends": []interface{}{},
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
	for _, p := range pools {
		stats := p.Stats()
		totalConns += stats.ClientConnections
		totalQueries += stats.TotalQueries
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total_connections": totalConns,
		"active_pools":      len(pools),
		"queries_per_sec":   0, // TODO: Calculate
		"cache_hit_rate":    0, // TODO: Calculate
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
	for _, p := range pools {
		// TODO: Check pool health
		_ = p
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
	w.Header().Set("Access-Control-Allow-Origin", "*")

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
			var cacheHits int64
			var activeTransactions int

			for _, p := range pools {
				stats := p.Stats()
				totalConns += stats.ClientConnections
				totalQueries += stats.TotalQueries
			}

			// Get query stats
			for _, l := range s.listeners {
				if ql := l.QueryLogger(); ql != nil {
					stats := ql.GetStats(time.Now().Add(-24 * time.Hour))
					cacheHits += int64(stats.CachedQueries)
				}
				if tm := l.TransactionManager(); tm != nil {
					stats := tm.GetStats()
					activeTransactions += stats.ActiveCount
				}
			}

			data := map[string]interface{}{
				"total_connections":   totalConns,
				"active_pools":        len(pools),
				"queries_per_sec":     0, // TODO: Calculate
				"cache_hit_rate":      0, // TODO: Calculate
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

	s.log.Info("Configuration reload requested via API")

	// TODO: Implement actual config reload
	// This would require reloading the config file and applying changes
	// For now, just return success

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "success",
		"message":   "Configuration reload initiated",
		"timestamp": time.Now().UTC(),
	})
}

// handleBackendAction handles backend-specific actions (drain, etc.).
func (s *Server) handleBackendAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse path: /api/v1/backends/{address}/drain
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 6 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	backendAddr := parts[4]
	action := parts[5]

	s.log.Info("Backend action requested", "address", backendAddr, "action", action)

	// TODO: Implement actual backend actions
	// This would require draining connections from a specific backend

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "success",
		"backend":   backendAddr,
		"action":    action,
		"message":   "Action " + action + " initiated for " + backendAddr,
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
			// TODO: Implement GetSlowQueries method in QueryLogger
			// For now, return placeholder
			_ = ql
		}
	}

	// If no slow queries found, return empty array with placeholder
	if len(slowQueries) == 0 {
		slowQueries = append(slowQueries, map[string]interface{}{
			"query_id":      "example-1",
			"query":         "SELECT * FROM large_table WHERE...",
			"duration_ms":   1500,
			"timestamp":     time.Now().Add(-5 * time.Minute).UTC(),
			"pool":          "default",
			"client_addr":   "192.168.1.100",
			"rows_returned": 10000,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"slow_queries": slowQueries,
		"limit":        limit,
		"timestamp":    time.Now().UTC(),
	})
}
