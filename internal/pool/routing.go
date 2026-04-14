package pool

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/tokenizer"
)

// Router handles query routing decisions.
type Router struct {
	mu           sync.RWMutex
	primary      *Backend
	replicas     []*Backend
	replicaIndex int64 // Round-robin counter (atomic for RLock-safe access)
	rules        []RoutingRule
	defaultRead  bool
}

// RoutingRule defines a custom routing rule.
type RoutingRule struct {
	Pattern  *regexp.Regexp
	Target   string // "primary" or "replica"
	Fallback string // "primary" or "replica"
}

// NewRouter creates a new query router.
func NewRouter(cfg *config.RoutingConfig, backends []*Backend) (*Router, error) {
	router := &Router{
		replicas: make([]*Backend, 0),
		rules:    make([]RoutingRule, 0),
	}

	// Separate primary and replicas
	for _, b := range backends {
		switch b.Role {
		case "primary":
			router.primary = b
		case "replica":
			router.replicas = append(router.replicas, b)
		}
	}

	if router.primary == nil && len(router.replicas) > 0 {
		return nil, fmt.Errorf("no primary backend configured")
	}

	// Parse routing rules
	if cfg.ReadWriteSplit {
		router.defaultRead = true
		for _, rule := range cfg.Rules {
			pattern, err := regexp.Compile(rule.Match)
			if err != nil {
				return nil, fmt.Errorf("invalid routing rule pattern %q: %w", rule.Match, err)
			}
			router.rules = append(router.rules, RoutingRule{
				Pattern:  pattern,
				Target:   rule.Target,
				Fallback: rule.Fallback,
			})
		}
	}

	return router, nil
}

// RouteQuery determines where to route a query.
func (r *Router) RouteQuery(query string, inTransaction bool) (*Backend, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// If in a transaction, route to primary (writes need consistency)
	if inTransaction {
		if r.primary == nil {
			return nil, fmt.Errorf("no primary backend available")
		}
		return r.primary, nil
	}

	// Check custom routing rules
	for _, rule := range r.rules {
		if rule.Pattern.MatchString(query) {
			return r.selectBackend(rule.Target, rule.Fallback)
		}
	}

	// Default read/write split
	if r.defaultRead {
		queryType, _ := tokenizer.ClassifyQuery(query)
		if tokenizer.IsReadQuery(queryType) {
			backend := r.selectReplica()
			if backend == nil {
				return nil, fmt.Errorf("no replica available")
			}
			return backend, nil
		}
	}

	// Default to primary for writes
	if r.primary == nil {
		return nil, fmt.Errorf("no primary backend available")
	}
	return r.primary, nil
}

// selectBackend selects a backend based on target and fallback.
func (r *Router) selectBackend(target, fallback string) (*Backend, error) {
	var backend *Backend

	switch target {
	case "primary":
		backend = r.primary
	case "replica":
		backend = r.selectReplica()
	}

	// If target is unavailable, try fallback
	if backend == nil || !backend.Healthy.Load() {
		switch fallback {
		case "primary":
			backend = r.primary
		case "replica":
			backend = r.selectReplica()
		}
	}

	if backend == nil {
		return nil, fmt.Errorf("no backend available (target=%s, fallback=%s)", target, fallback)
	}

	return backend, nil
}

// selectReplica selects a replica using weighted round-robin.
func (r *Router) selectReplica() *Backend {
	if len(r.replicas) == 0 {
		return r.primary // Fallback to primary if no replicas
	}

	// Filter healthy replicas
	healthy := make([]*Backend, 0)
	for _, r := range r.replicas {
		if r.Healthy.Load() {
			healthy = append(healthy, r)
		}
	}

	if len(healthy) == 0 {
		return r.primary // Fallback to primary
	}

	// Weighted round-robin
	if len(healthy) == 1 {
		return healthy[0]
	}

	// Simple round-robin for now (atomic for RLock-safe access)
	idx := atomic.AddInt64(&r.replicaIndex, 1) - 1
	return healthy[int(idx)%len(healthy)]
}

// Primary returns the primary backend.
func (r *Router) Primary() *Backend {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.primary
}

// Replicas returns the replica backends.
func (r *Router) Replicas() []*Backend {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]*Backend{}, r.replicas...)
}

// UpdateBackends updates the backend list.
func (r *Router) UpdateBackends(backends []*Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.primary = nil
	r.replicas = make([]*Backend, 0)

	for _, b := range backends {
		switch b.Role {
		case "primary":
			r.primary = b
		case "replica":
			r.replicas = append(r.replicas, b)
		}
	}
}

// RouteResult represents the result of a routing decision.
type RouteResult struct {
	Backend     *Backend
	IsPrimary   bool
	IsReplica   bool
	RouteType   string // "primary", "replica", "fallback"
	QueryType   string // "read", "write", "transaction"
}

// RouteQueryDetailed returns detailed routing information.
func (r *Router) RouteQueryDetailed(query string, inTransaction bool) (*RouteResult, error) {
	backend, err := r.RouteQuery(query, inTransaction)
	if err != nil {
		return nil, err
	}

	result := &RouteResult{
		Backend: backend,
	}

	if backend == r.primary {
		result.IsPrimary = true
		result.RouteType = "primary"
	} else {
		result.IsReplica = true
		result.RouteType = "replica"
	}

	if inTransaction {
		result.QueryType = "transaction"
	} else {
		queryType, _ := tokenizer.ClassifyQuery(query)
		if tokenizer.IsReadQuery(queryType) {
			result.QueryType = "read"
		} else {
			result.QueryType = "write"
		}
	}

	return result, nil
}

// ShouldRouteToReplica checks if a query should go to a replica.
func ShouldRouteToReplica(query string) bool {
	queryType, _ := tokenizer.ClassifyQuery(query)
	return tokenizer.IsReadQuery(queryType)
}

// ExtractHint extracts routing hints from SQL comments.
// Example: /* route:primary */ or /* route:replica */
func ExtractHint(query string) (string, bool) {
	// Look for routing hints in comments
	patterns := []string{
		`(?i)/\*\s*route:(primary|replica)\s*\*/`,
		`(?i)--\s*route:(primary|replica)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(query)
		if len(matches) > 1 {
			return strings.ToLower(matches[1]), true
		}
	}

	return "", false
}

// StripHints removes routing hints from SQL.
func StripHints(query string) string {
	patterns := []string{
		`(?i)/\*\s*route:(primary|replica)\s*\*/\s*`,
		`(?i)--\s*route:(primary|replica)\s*\n?`,
	}

	result := query
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		result = re.ReplaceAllString(result, "")
	}

	return strings.TrimSpace(result)
}
