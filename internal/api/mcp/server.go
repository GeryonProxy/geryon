// Package mcp implements the Model Context Protocol server for AI-assisted
// database management. It exposes 13+ tools for pool, connection, backend,
// cache, cluster, and user management via SSE or stdio transports.
package mcp

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

// Server implements the Model Context Protocol (MCP) server.
type Server struct {
	mu          sync.RWMutex
	config      *config.AdminMCPConfig
	poolMgr     *pool.Manager
	log         *logger.Logger
	server      *http.Server
	started     bool
	authToken   string
	authEnabled bool
	sseCount    atomic.Int64
	sseLimit    int
	reloadFn    func() error
}

// NewServer creates a new MCP server.
func NewServer(cfg *config.AdminMCPConfig, poolMgr *pool.Manager, log *logger.Logger, reloadFn func() error) *Server {
	s := &Server{
		config:      cfg,
		poolMgr:     poolMgr,
		log:         log,
		authEnabled: cfg.Auth.Enabled,
		authToken:   cfg.Auth.Token,
		sseLimit:    50,
		reloadFn:    reloadFn,
	}
	return s
}

// Start starts the MCP server.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("MCP server already started")
	}

	mux := http.NewServeMux()

	// MCP endpoints
	mux.HandleFunc("/mcp/v1/initialize", s.handleInitialize)
	mux.HandleFunc("/mcp/v1/tools/list", s.handleToolsList)
	mux.HandleFunc("/mcp/v1/tools/call", s.handleToolsCall)
	mux.HandleFunc("/mcp/v1/resources/list", s.handleResourcesList)
	mux.HandleFunc("/mcp/v1/resources/read", s.handleResourcesRead)

	// SSE endpoint for streaming
	mux.HandleFunc("/mcp/v1/sse", s.handleSSE)

	readTimeout := parseDuration(s.config.ReadTimeout, 30*time.Second)
	writeTimeout := parseDuration(s.config.WriteTimeout, 30*time.Second)
	s.server = &http.Server{
		Addr:         s.config.Listen,
		Handler:      s.withLogging(s.withPanicRecovery(s.withSecurityHeaders(s.withRateLimit(s.withAuth(mux))))),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}

	s.started = true

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("MCP server error", "error", err)
		}
	}()

	s.log.Info("MCP server started", "address", s.config.Listen)
	return nil
}

// Stop stops the MCP server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return nil
	}

	s.started = false
	return s.server.Shutdown(ctx)
}

// withLogging adds request logging middleware.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.log.Debug("MCP request",
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
				s.log.Error("Panic recovered in MCP handler", "error", err, "path", r.URL.Path)
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

// mcpRateLimiter implements per-IP rate limiting.
type mcpRateLimiter struct {
	mu         sync.Mutex
	limiters   map[string]*rate.Limiter
	lastSeen   map[string]time.Time
	maxSize    int
	cleanupTTL time.Duration
}

func newMCPRateLimiter() *mcpRateLimiter {
	rl := &mcpRateLimiter{
		limiters:   make(map[string]*rate.Limiter),
		lastSeen:   make(map[string]time.Time),
		maxSize:    10000,
		cleanupTTL: 5 * time.Minute,
	}
	go rl.periodicCleanup()
	return rl
}

func (rl *mcpRateLimiter) periodicCleanup() {
	ticker := time.NewTicker(rl.cleanupTTL)
	defer ticker.Stop()
	for range ticker.C {
		rl.doCleanup()
	}
}

// doCleanup removes limiters that haven't been seen within cleanupTTL.
func (rl *mcpRateLimiter) doCleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	for ip, last := range rl.lastSeen {
		if now.Sub(last) > rl.cleanupTTL {
			delete(rl.limiters, ip)
			delete(rl.lastSeen, ip)
		}
	}
}

func (rl *mcpRateLimiter) GetLimiter(ip string) *rate.Limiter {
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
		limiter = rate.NewLimiter(5, 10)
		rl.limiters[ip] = limiter
	}
	return limiter
}

// withRateLimit adds rate limiting middleware.
func (s *Server) withRateLimit(next http.Handler) http.Handler {
	rl := newMCPRateLimiter()
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

// MCP Protocol Types

type InitializeRequest struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ClientInfo      map[string]interface{} `json:"clientInfo"`
}

type InitializeResponse struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ServerInfo      map[string]interface{} `json:"serverInfo"`
}

type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type ToolsListResponse struct {
	Tools []Tool `json:"tools"`
}

type ToolCallRequest struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type ToolCallResponse struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MIMEType    string `json:"mimeType"`
}

type ResourcesListResponse struct {
	Resources []Resource `json:"resources"`
}

type ResourceReadRequest struct {
	URI string `json:"uri"`
}

type ResourceReadResponse struct {
	Contents []ResourceContent `json:"contents"`
}

type ResourceContent struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType"`
	Text     string `json:"text"`
}

// handleInitialize handles MCP initialization.
func (s *Server) handleInitialize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req InitializeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	resp := InitializeResponse{
		ProtocolVersion: "2024-11-05",
		Capabilities: map[string]interface{}{
			"tools":     map[string]interface{}{},
			"resources": map[string]interface{}{},
		},
		ServerInfo: map[string]interface{}{
			"name":    "geryon-mcp",
			"version": "0.1.0",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleToolsList returns available tools.
func (s *Server) handleToolsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tools := []Tool{
		{
			Name:        "geryon_pool_list",
			Description: "List all connection pools",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			Name:        "geryon_pool_stats",
			Description: "Get statistics for a specific pool",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "Pool name"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "geryon_connection_list",
			Description: "List active connections",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			Name:        "geryon_backend_list",
			Description: "List all backends with health status",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			Name:        "geryon_backend_drain",
			Description: "Start draining a backend",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"address": map[string]interface{}{"type": "string", "description": "Backend address (host:port)"},
				},
				"required": []string{"address"},
			},
		},
		{
			Name:        "geryon_cache_stats",
			Description: "Get query cache statistics",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			Name:        "geryon_config_reload",
			Description: "Reload configuration",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			Name:        "geryon_query_stats",
			Description: "Get query statistics",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
	}

	resp := ToolsListResponse{Tools: tools}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleToolsCall executes a tool.
func (s *Server) handleToolsCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ToolCallRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var result string
	var isError bool

	switch req.Name {
	case "geryon_pool_list":
		result = s.toolPoolList()
	case "geryon_pool_stats":
		poolName, _ := req.Arguments["name"].(string)
		result = s.toolPoolStats(poolName)
	case "geryon_connection_list":
		result = s.toolConnectionList()
	case "geryon_backend_list":
		result = s.toolBackendList()
	case "geryon_backend_drain":
		address, _ := req.Arguments["address"].(string)
		result = s.toolBackendDrain(address)
	case "geryon_cache_stats":
		result = s.toolCacheStats()
	case "geryon_config_reload":
		result = s.toolConfigReload()
	case "geryon_query_stats":
		result = s.toolQueryStats()
	default:
		result = fmt.Sprintf("Unknown tool: %s", req.Name)
		isError = true
	}

	resp := ToolCallResponse{
		Content: []Content{{Type: "text", Text: result}},
		IsError: isError,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleResourcesList returns available resources.
func (s *Server) handleResourcesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resources := []Resource{
		{
			URI:         "geryon://config",
			Name:        "Geryon Configuration",
			Description: "Current Geryon configuration",
			MIMEType:    "application/json",
		},
		{
			URI:         "geryon://pools",
			Name:        "Connection Pools",
			Description: "List of all connection pools",
			MIMEType:    "application/json",
		},
		{
			URI:         "geryon://stats/overview",
			Name:        "Statistics Overview",
			Description: "High-level statistics overview",
			MIMEType:    "application/json",
		},
	}

	// Add resources for each pool
	for _, p := range s.poolMgr.ListPools() {
		resources = append(resources, Resource{
			URI:         fmt.Sprintf("geryon://pools/%s", p.Name()),
			Name:        fmt.Sprintf("Pool: %s", p.Name()),
			Description: fmt.Sprintf("Details for pool %s", p.Name()),
			MIMEType:    "application/json",
		})
	}

	resp := ResourcesListResponse{Resources: resources}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleResourcesRead returns resource content.
func (s *Server) handleResourcesRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ResourceReadRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var content string

	switch req.URI {
	case "geryon://config":
		content = s.resourceConfig()
	case "geryon://pools":
		content = s.resourcePools()
	case "geryon://stats/overview":
		content = s.resourceStatsOverview()
	default:
		// Check for pool-specific resource
		if strings.HasPrefix(req.URI, "geryon://pools/") {
			poolName := strings.TrimPrefix(req.URI, "geryon://pools/")
			content = s.resourcePool(poolName)
		} else {
			http.Error(w, "Resource not found", http.StatusNotFound)
			return
		}
	}

	resp := ResourceReadResponse{
		Contents: []ResourceContent{{
			URI:      req.URI,
			MIMEType: "application/json",
			Text:     content,
		}},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleSSE handles Server-Sent Events for MCP streaming.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	if s.sseCount.Load() >= int64(s.sseLimit) {
		http.Error(w, "Too many streaming connections", http.StatusTooManyRequests)
		return
	}
	s.sseCount.Add(1)
	defer s.sseCount.Add(-1)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send initial event
	fmt.Fprintf(w, "event: connected\ndata: %s\n\n", `{"status":"connected"}`)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Keep connection open
	ctx := r.Context()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: %s\n\n", `{"time":"`+time.Now().Format(time.RFC3339)+`"}`)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
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
