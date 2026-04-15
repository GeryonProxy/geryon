package mcp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/GeryonProxy/geryon/internal/pool"
)

// toolPoolList returns list of pools.
func (s *Server) toolPoolList() string {
	pools := s.poolMgr.ListPools()
	if len(pools) == 0 {
		return "No pools configured"
	}

	result := "Connection Pools:\n\n"
	for _, p := range pools {
		stats := p.Stats()
		result += fmt.Sprintf("• %s\n", stats.Name)
		result += fmt.Sprintf("  Mode: %s\n", stats.Mode)
		result += fmt.Sprintf("  Clients: %d | Servers: %d (Idle: %d, Active: %d)\n",
			stats.ClientConnections, stats.ServerConnections,
			stats.IdleConnections, stats.ActiveConnections)
		result += fmt.Sprintf("  Backends: %d\n", stats.BackendCount)
		result += "\n"
	}
	return result
}

// toolPoolStats returns stats for a specific pool.
func (s *Server) toolPoolStats(poolName string) string {
	p := s.poolMgr.GetPool(poolName)
	if p == nil {
		return fmt.Sprintf("Pool '%s' not found", poolName)
	}

	stats := p.Stats()
	return fmt.Sprintf(`Pool: %s

Mode: %s
Client Connections: %d
Server Connections: %d (Idle: %d, Active: %d)
Waiting Clients: %d
Total Queries: %d
Total Transactions: %d
Backend Count: %d
Prepared Statement Cache: %d statements (%.1f%% hit rate)
`,
		stats.Name,
		stats.Mode,
		stats.ClientConnections,
		stats.ServerConnections,
		stats.IdleConnections,
		stats.ActiveConnections,
		stats.WaitingClients,
		stats.TotalQueries,
		stats.TotalTransactions,
		stats.BackendCount,
		stats.PreparedStmtCacheSize,
		stats.PreparedStmtHitRate*100,
	)
}

// toolConnectionList returns active connections.
func (s *Server) toolConnectionList() string {
	pools := s.poolMgr.ListPools()
	if len(pools) == 0 {
		return "No pools configured"
	}

	result := "Active Connections:\n\n"
	totalClients := int64(0)

	for _, p := range pools {
		stats := p.Stats()
		totalClients += stats.ClientConnections
		result += fmt.Sprintf("• %s: %d clients, %d servers (%d idle, %d active)\n",
			stats.Name,
			stats.ClientConnections,
			stats.ServerConnections,
			stats.IdleConnections,
			stats.ActiveConnections)
	}

	result += fmt.Sprintf("\nTotal Client Connections: %d\n", totalClients)
	return result
}

// toolBackendList returns list of backends.
func (s *Server) toolBackendList() string {
	pools := s.poolMgr.ListPools()
	if len(pools) == 0 {
		return "No pools configured"
	}

	result := "Backends:\n\n"
	for _, p := range pools {
		backends := p.GetBackends()
		if len(backends) == 0 {
			continue
		}

		result += fmt.Sprintf("Pool: %s\n", p.Name())
		for _, b := range backends {
			status := "❌"
			if b.Healthy.Load() {
				status = "✅"
			}
			if b.Draining.Load() {
				status = "🔄"
			}

			role := b.Role
			if role == "" {
				role = "unknown"
			}

			result += fmt.Sprintf("  %s %s (%s) - %s\n", status, b.Address(), role, p.Name())
		}
		result += "\n"
	}

	result += "Legend: ✅ Healthy | ❌ Unhealthy | 🔄 Draining\n"
	return result
}

// toolBackendDrain starts draining a backend.
func (s *Server) toolBackendDrain(address string) string {
	// Find the pool that has this backend
	var targetPool *pool.Pool
	for _, p := range s.poolMgr.ListPools() {
		for _, b := range p.GetBackends() {
			if b.Address() == address {
				targetPool = p
				break
			}
		}
		if targetPool != nil {
			break
		}
	}

	if targetPool == nil {
		return fmt.Sprintf("Backend '%s' not found in any pool", address)
	}

	activeConns, err := targetPool.DrainBackend(address)
	if err != nil {
		return fmt.Sprintf("Error draining backend: %v", err)
	}

	return fmt.Sprintf("Draining initiated for %s\nActive connections: %d\n\nThe backend will stop accepting new connections. Monitor until active connections reach 0 before removing the backend.",
		address, activeConns)
}

// toolCacheStats returns cache statistics.
func (s *Server) toolCacheStats() string {
	// Aggregate cache stats from all pools
	totalSize := 0
	totalHitRate := 0.0
	poolCount := 0

	for _, p := range s.poolMgr.ListPools() {
		stats := p.Stats()
		if stats.PreparedStmtCacheSize > 0 {
			totalSize += stats.PreparedStmtCacheSize
			totalHitRate += stats.PreparedStmtHitRate
			poolCount++
		}
	}

	avgHitRate := 0.0
	if poolCount > 0 {
		avgHitRate = totalHitRate / float64(poolCount)
	}

	return fmt.Sprintf(`Cache Statistics:

Prepared Statement Cache:
Total Cached Statements: %d
Average Hit Rate: %.1f%%
Pools with Cache: %d
`, totalSize, avgHitRate*100, poolCount)
}

// toolConfigReload reloads configuration.
func (s *Server) toolConfigReload() string {
	s.log.Info("Configuration reload requested via MCP")
	if s.reloadFn != nil {
		if err := s.reloadFn(); err != nil {
			return fmt.Sprintf("Configuration reload failed: %v", err)
		}
		return "Configuration reloaded successfully."
	}
	return "Config reload not configured (no reload function provided)"
}

// toolQueryStats returns query statistics.
func (s *Server) toolQueryStats() string {
	var totalQueries int64
	var totalTransactions int64

	for _, p := range s.poolMgr.ListPools() {
		stats := p.Stats()
		totalQueries += stats.TotalQueries
		totalTransactions += stats.TotalTransactions
	}

	return fmt.Sprintf(`Query Statistics:

Total Queries: %d
Total Transactions: %d

Per Pool Breakdown:
`, totalQueries, totalTransactions)
}

// resourceConfig returns configuration resource.
func (s *Server) resourceConfig() string {
	config := map[string]interface{}{
		"mcp_version": "0.1.0",
		"timestamp":   time.Now().Format(time.RFC3339),
		"note":        "Full configuration available via REST API",
	}
	data, _ := json.MarshalIndent(config, "", "  ")
	return string(data)
}

// resourcePools returns pools resource.
func (s *Server) resourcePools() string {
	pools := s.poolMgr.ListPools()
	result := make([]map[string]interface{}, 0, len(pools))

	for _, p := range pools {
		stats := p.Stats()
		result = append(result, map[string]interface{}{
			"name":               stats.Name,
			"mode":               stats.Mode,
			"client_connections": stats.ClientConnections,
			"server_connections": stats.ServerConnections,
			"backends":           stats.BackendCount,
		})
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data)
}

// resourceStatsOverview returns stats overview resource.
func (s *Server) resourceStatsOverview() string {
	pools := s.poolMgr.ListPools()
	var totalClients, totalQueries, totalTransactions int64

	for _, p := range pools {
		stats := p.Stats()
		totalClients += stats.ClientConnections
		totalQueries += stats.TotalQueries
		totalTransactions += stats.TotalTransactions
	}

	overview := map[string]interface{}{
		"timestamp":          time.Now().Format(time.RFC3339),
		"active_pools":       len(pools),
		"total_clients":      totalClients,
		"total_queries":      totalQueries,
		"total_transactions": totalTransactions,
	}

	data, _ := json.MarshalIndent(overview, "", "  ")
	return string(data)
}

// resourcePool returns details for a specific pool.
func (s *Server) resourcePool(poolName string) string {
	p := s.poolMgr.GetPool(poolName)
	if p == nil {
		return fmt.Sprintf(`{"error": "Pool '%s' not found"}`, poolName)
	}

	stats := p.Stats()
	backends := p.GetBackends()

	backendList := make([]map[string]interface{}, 0, len(backends))
	for _, b := range backends {
		backendList = append(backendList, map[string]interface{}{
			"address":    b.Address(),
			"role":       b.Role,
			"healthy":    b.Healthy.Load(),
			"draining":   b.Draining.Load(),
			"last_check": b.LastCheck,
		})
	}

	result := map[string]interface{}{
		"name":                    stats.Name,
		"mode":                    stats.Mode,
		"client_connections":      stats.ClientConnections,
		"server_connections":      stats.ServerConnections,
		"idle_connections":        stats.IdleConnections,
		"active_connections":      stats.ActiveConnections,
		"waiting_clients":         stats.WaitingClients,
		"total_queries":           stats.TotalQueries,
		"total_transactions":      stats.TotalTransactions,
		"backends":                backendList,
		"prepared_cache_size":     stats.PreparedStmtCacheSize,
		"prepared_cache_hit_rate": stats.PreparedStmtHitRate,
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data)
}
