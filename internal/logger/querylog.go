package logger

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// QueryLogger handles query logging for analytics and debugging.
type QueryLogger struct {
	mu          sync.RWMutex
	config      QueryLogConfig
	slowLog     *os.File
	allLog      *os.File
	jsonLog     *os.File
	buffer      []QueryLogEntry
	bufferMu    sync.Mutex
	running     atomic.Bool
	stopCh      chan struct{}
	flushTicker *time.Ticker

	// In-memory query store for recent/slow queries
	recentQueries    []QueryLogEntry
	slowQueries      []QueryLogEntry
	queryMu          sync.RWMutex
	maxRecentQueries int
	maxSlowQueries   int
	maxClientStats   int // cap for clientStats map to prevent unbounded growth

	// Per-user and per-client statistics
	userStats   map[string]*UserStats
	clientStats map[string]*ClientStats

	// Query statistics
	stats     QueryStats
	statsMu   sync.RWMutex
	lastReset time.Time
}

// QueryLogConfig contains query logging configuration.
type QueryLogConfig struct {
	Enabled       bool
	Directory     string
	SlowThreshold time.Duration
	MaxFileSize   int64 // bytes
	MaxFiles      int
	BufferSize    int
	FlushInterval time.Duration
	LogAllQueries bool
	LogJSON       bool
}

// DefaultQueryLogConfig returns default configuration.
func DefaultQueryLogConfig() QueryLogConfig {
	return QueryLogConfig{
		Enabled:       true,
		Directory:     "logs/queries",
		SlowThreshold: 100 * time.Millisecond,
		MaxFileSize:   100 * 1024 * 1024, // 100MB
		MaxFiles:      10,
		BufferSize:    1000,
		FlushInterval: 5 * time.Second,
		LogAllQueries: false,
		LogJSON:       true,
	}
}

// QueryLogEntry represents a single query log entry.
type QueryLogEntry struct {
	Timestamp     time.Time     `json:"timestamp"`
	QueryID       string        `json:"query_id"`
	CorrelationID string        `json:"correlation_id,omitempty"`
	Pool          string        `json:"pool"`
	ClientAddr    string        `json:"client_addr"`
	BackendAddr   string        `json:"backend_addr"`
	Username      string        `json:"username"`
	Database      string        `json:"database"`
	Query         string        `json:"query"`
	QueryHash     string        `json:"query_hash"`
	Duration      time.Duration `json:"duration"`
	RowsAffected  int64         `json:"rows_affected"`
	RowsReturned  int64         `json:"rows_returned"`
	IsSlow        bool          `json:"is_slow"`
	IsCached      bool          `json:"is_cached"`
	IsError       bool          `json:"is_error"`
	ErrorMessage  string        `json:"error_message,omitempty"`
	TransactionID string        `json:"transaction_id,omitempty"`
}

// QueryStats aggregates query statistics.
type QueryStats struct {
	TotalQueries  uint64        `json:"total_queries"`
	SlowQueries   uint64        `json:"slow_queries"`
	CachedQueries uint64        `json:"cached_queries"`
	ErrorQueries  uint64        `json:"error_queries"`
	AvgDuration   time.Duration `json:"avg_duration"`
	MaxDuration   time.Duration `json:"max_duration"`
	MinDuration   time.Duration `json:"min_duration"`
	P95Duration   time.Duration `json:"p95_duration"`
	P99Duration   time.Duration `json:"p99_duration"`
	UniqueQueries uint64        `json:"unique_queries"`
	TopQueries    []QueryDigest `json:"top_queries"`
}

// QueryDigest represents aggregated query statistics.
type QueryDigest struct {
	QueryHash    string        `json:"query_hash"`
	QueryPattern string        `json:"query_pattern"`
	Count        uint64        `json:"count"`
	AvgDuration  time.Duration `json:"avg_duration"`
	TotalTime    time.Duration `json:"total_time"`
	MinDuration  time.Duration `json:"min_duration"`
	MaxDuration  time.Duration `json:"max_duration"`
}

// UserStats represents per-user query statistics.
type UserStats struct {
	Username     string        `json:"username"`
	TotalQueries uint64        `json:"total_queries"`
	SlowQueries  uint64        `json:"slow_queries"`
	AvgDuration  time.Duration `json:"avg_duration"`
	MaxDuration  time.Duration `json:"max_duration"`
	LastQuery    time.Time     `json:"last_query"`
}

// ClientStats represents per-client query statistics.
type ClientStats struct {
	ClientAddr   string        `json:"client_addr"`
	Username     string        `json:"username"`
	Pool         string        `json:"pool"`
	TotalQueries uint64        `json:"total_queries"`
	SlowQueries  uint64        `json:"slow_queries"`
	AvgDuration  time.Duration `json:"avg_duration"`
	MaxDuration  time.Duration `json:"max_duration"`
	LastQuery    time.Time     `json:"last_query"`
}

// NewQueryLogger creates a new query logger.
func NewQueryLogger(config QueryLogConfig) (*QueryLogger, error) {
	ql := &QueryLogger{
		config:           config,
		stopCh:           make(chan struct{}),
		recentQueries:    make([]QueryLogEntry, 0, 1000),
		slowQueries:      make([]QueryLogEntry, 0, 100),
		maxRecentQueries: 1000,
		maxSlowQueries:   100,
		maxClientStats:   10000, // cap per-client stats to bound memory
		lastReset:        time.Now(),
		stats:            QueryStats{TopQueries: make([]QueryDigest, 0)},
		userStats:        make(map[string]*UserStats),
		clientStats:      make(map[string]*ClientStats),
	}

	if !config.Enabled {
		return ql, nil
	}

	// Create log directory
	if err := os.MkdirAll(config.Directory, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Open slow query log
	slowLogPath := filepath.Join(config.Directory, "slow.log")
	slowLog, err := os.OpenFile(slowLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open slow log: %w", err)
	}
	ql.slowLog = slowLog

	// Open all queries log if enabled
	if config.LogAllQueries {
		allLogPath := filepath.Join(config.Directory, "all.log")
		allLog, err := os.OpenFile(allLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			slowLog.Close()
			return nil, fmt.Errorf("failed to open all log: %w", err)
		}
		ql.allLog = allLog
	}

	// Open JSON log if enabled
	if config.LogJSON {
		jsonLogPath := filepath.Join(config.Directory, "queries.json")
		jsonLog, err := os.OpenFile(jsonLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			slowLog.Close()
			if ql.allLog != nil {
				ql.allLog.Close()
			}
			return nil, fmt.Errorf("failed to open json log: %w", err)
		}
		ql.jsonLog = jsonLog
	}

	ql.buffer = make([]QueryLogEntry, 0, config.BufferSize)
	ql.running.Store(true)
	ql.flushTicker = time.NewTicker(config.FlushInterval)

	// Start background flush
	go ql.flushLoop()

	return ql, nil
}

// LogQuery logs a query execution.
func (ql *QueryLogger) LogQuery(entry QueryLogEntry) {
	if !ql.running.Load() || !ql.config.Enabled {
		return
	}

	// Redact sensitive data
	entry.Query = redactQuery(entry.Query)

	// Determine if slow
	entry.IsSlow = entry.Duration >= ql.config.SlowThreshold

	// Update in-memory statistics
	ql.updateStats(entry)

	// Add to recent queries
	ql.addRecentQuery(entry)

	// Add to slow queries if applicable
	if entry.IsSlow {
		ql.addSlowQuery(entry)
	}

	// Buffer the entry with hard cap to prevent unbounded growth
	maxBuf := ql.config.BufferSize * 2 // Hard cap at 2x buffer size
	ql.bufferMu.Lock()
	if len(ql.buffer) >= maxBuf {
		ql.bufferMu.Unlock()
		return // Drop entry under extreme load
	}
	ql.buffer = append(ql.buffer, entry)
	shouldFlush := len(ql.buffer) >= ql.config.BufferSize
	ql.bufferMu.Unlock()

	// Flush if buffer is full
	if shouldFlush {
		ql.Flush()
	}

	// Log slow queries immediately
	if entry.IsSlow && ql.slowLog != nil {
		ql.writeSlowLog(entry)
	}

	// Log all queries if enabled
	if ql.config.LogAllQueries && ql.allLog != nil {
		ql.writeAllLog(entry)
	}

	// Log JSON if enabled
	if ql.config.LogJSON && ql.jsonLog != nil {
		ql.writeJSONLog(entry)
	}
}

// writeSlowLog writes to slow query log.
func (ql *QueryLogger) writeSlowLog(entry QueryLogEntry) {
	// Check if rotation is needed before writing
	if ql.shouldRotate(ql.slowLog) {
		ql.rotateLogFile(&ql.slowLog, "slow")
	}

	line := fmt.Sprintf("[%s] [%s] [%s] %s - %s (%dms) rows=%d client=%s backend=%s\n",
		entry.Timestamp.Format(time.RFC3339),
		entry.Pool,
		entry.Username,
		entry.QueryID,
		entry.Query[:min(len(entry.Query), 100)],
		entry.Duration.Milliseconds(),
		entry.RowsReturned,
		entry.ClientAddr,
		entry.BackendAddr,
	)
	ql.slowLog.WriteString(line)
	ql.slowLog.Sync()
}

// writeAllLog writes to all queries log.
func (ql *QueryLogger) writeAllLog(entry QueryLogEntry) {
	// Check if rotation is needed before writing
	if ql.shouldRotate(ql.allLog) {
		ql.rotateLogFile(&ql.allLog, "all")
	}

	cached := ""
	if entry.IsCached {
		cached = " [CACHED]"
	}
	line := fmt.Sprintf("[%s]%s [%s] %dms: %s\n",
		entry.Timestamp.Format(time.RFC3339),
		cached,
		entry.Pool,
		entry.Duration.Milliseconds(),
		entry.Query[:min(len(entry.Query), 200)],
	)
	ql.allLog.WriteString(line)
}

// writeJSONLog writes JSON formatted log.
func (ql *QueryLogger) writeJSONLog(entry QueryLogEntry) {
	// Check if rotation is needed before writing
	if ql.shouldRotate(ql.jsonLog) {
		ql.rotateLogFile(&ql.jsonLog, "queries")
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	ql.jsonLog.Write(data)
	ql.jsonLog.WriteString("\n")
}

// rotateLogFile rotates a log file if it exceeds the max size.
// It renames the current file with a timestamp, opens a new file, and cleans up old files.
func (ql *QueryLogger) rotateLogFile(file **os.File, baseName string) error {
	if file == nil || *file == nil {
		return nil
	}

	// Check current file size
	info, err := (*file).Stat()
	if err != nil {
		return err
	}

	if info.Size() < ql.config.MaxFileSize {
		return nil
	}

	// Close current file
	(*file).Close()

	// Generate rotated filename with timestamp
	rotatedName := fmt.Sprintf("%s.%s.log", baseName, time.Now().Format("20060102-150405"))

	// Build paths
	dir := ql.config.Directory
	oldPath := filepath.Join(dir, baseName+".log")
	newPath := filepath.Join(dir, rotatedName)

	// Rename current file to rotated name
	if err := os.Rename(oldPath, newPath); err != nil {
		// Try to re-open original file
		*file, err = os.OpenFile(oldPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		return err
	}

	// Open new log file
	*file, err = os.OpenFile(oldPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}

	// Clean up old files exceeding MaxFiles limit
	if ql.config.MaxFiles > 0 {
		ql.cleanupOldFiles(baseName)
	}

	return nil
}

// cleanupOldFiles removes the oldest rotated log files when count exceeds MaxFiles.
func (ql *QueryLogger) cleanupOldFiles(baseName string) {
	pattern := filepath.Join(ql.config.Directory, baseName+".*.log")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}

	if len(matches) <= ql.config.MaxFiles {
		return
	}

	// Sort by modification time (oldest first)
	sort.Slice(matches, func(i, j int) bool {
		infoI, _ := os.Stat(matches[i])
		infoJ, _ := os.Stat(matches[j])
		if infoI == nil || infoJ == nil {
			return false
		}
		return infoI.ModTime().Before(infoJ.ModTime())
	})

	// Delete oldest files to meet MaxFiles limit
	filesToDelete := len(matches) - ql.config.MaxFiles
	for i := 0; i < filesToDelete; i++ {
		os.Remove(matches[i])
	}
}

// shouldRotate checks if a log file should be rotated based on size.
func (ql *QueryLogger) shouldRotate(file *os.File) bool {
	if file == nil || ql.config.MaxFileSize == 0 {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Size() >= ql.config.MaxFileSize
}

// Flush writes buffered entries to disk.
func (ql *QueryLogger) Flush() {
	if !ql.running.Load() {
		return
	}

	ql.bufferMu.Lock()
	buffer := ql.buffer
	ql.buffer = make([]QueryLogEntry, 0, ql.config.BufferSize)
	ql.bufferMu.Unlock()

	// Write JSON entries
	if ql.jsonLog != nil && len(buffer) > 0 {
		for _, entry := range buffer {
			ql.writeJSONLog(entry)
		}
		ql.jsonLog.Sync()
	}
}

// flushLoop periodically flushes the buffer.
func (ql *QueryLogger) flushLoop() {
	for {
		select {
		case <-ql.stopCh:
			return
		case <-ql.flushTicker.C:
			ql.Flush()
		}
	}
}

// Stop stops the query logger.
func (ql *QueryLogger) Stop() error {
	if !ql.running.CompareAndSwap(true, false) {
		return nil
	}

	close(ql.stopCh)
	ql.flushTicker.Stop()

	// Final flush
	ql.Flush()

	// Close files
	if ql.slowLog != nil {
		ql.slowLog.Close()
	}
	if ql.allLog != nil {
		ql.allLog.Close()
	}
	if ql.jsonLog != nil {
		ql.jsonLog.Close()
	}

	return nil
}

// GetStats returns query statistics for a time range.
func (ql *QueryLogger) GetStats(since time.Time) QueryStats {
	ql.statsMu.RLock()
	defer ql.statsMu.RUnlock()

	// Return copy of stats
	return ql.stats
}

// updateStats updates query statistics.
func (ql *QueryLogger) updateStats(entry QueryLogEntry) {
	ql.statsMu.Lock()
	defer ql.statsMu.Unlock()

	ql.stats.TotalQueries++

	if entry.IsSlow {
		ql.stats.SlowQueries++
	}
	if entry.IsCached {
		ql.stats.CachedQueries++
	}
	if entry.IsError {
		ql.stats.ErrorQueries++
	}

	// Update duration statistics
	duration := entry.Duration
	if ql.stats.TotalQueries == 1 {
		ql.stats.MinDuration = duration
		ql.stats.MaxDuration = duration
		ql.stats.AvgDuration = duration
	} else {
		if duration < ql.stats.MinDuration {
			ql.stats.MinDuration = duration
		}
		if duration > ql.stats.MaxDuration {
			ql.stats.MaxDuration = duration
		}
		// Decaying average: avg = avg * (1 - alpha) + value * alpha
		// This avoids integer overflow issues when TotalQueries is very large
		const avgAlpha = 0.001 // 0.1% weight for new values
		avgNs := float64(ql.stats.AvgDuration)
		durationNs := float64(duration)
		newAvgNs := avgNs*(1-avgAlpha) + durationNs*avgAlpha
		ql.stats.AvgDuration = time.Duration(newAvgNs)
	}

	// Update top queries (simple implementation - could be optimized)
	found := false
	for i := range ql.stats.TopQueries {
		if ql.stats.TopQueries[i].QueryHash == entry.QueryHash {
			ql.stats.TopQueries[i].Count++
			ql.stats.TopQueries[i].TotalTime += duration
			ql.stats.TopQueries[i].AvgDuration = ql.stats.TopQueries[i].TotalTime / time.Duration(ql.stats.TopQueries[i].Count)
			if duration < ql.stats.TopQueries[i].MinDuration {
				ql.stats.TopQueries[i].MinDuration = duration
			}
			if duration > ql.stats.TopQueries[i].MaxDuration {
				ql.stats.TopQueries[i].MaxDuration = duration
			}
			found = true
			break
		}
	}
	if !found && len(ql.stats.TopQueries) < 100 {
		ql.stats.TopQueries = append(ql.stats.TopQueries, QueryDigest{
			QueryHash:    entry.QueryHash,
			QueryPattern: entry.Query[:min(len(entry.Query), 100)],
			Count:        1,
			AvgDuration:  duration,
			TotalTime:    duration,
			MinDuration:  duration,
			MaxDuration:  duration,
		})
	}

	// Update per-user stats
	if entry.Username != "" {
		us, ok := ql.userStats[entry.Username]
		if !ok {
			us = &UserStats{Username: entry.Username}
			ql.userStats[entry.Username] = us
		}
		us.TotalQueries++
		if entry.IsSlow {
			us.SlowQueries++
		}
		if us.TotalQueries == 1 {
			us.AvgDuration = duration
			us.MaxDuration = duration
		} else {
			if duration > us.MaxDuration {
				us.MaxDuration = duration
			}
			const avgAlpha = 0.001
			avgNs := float64(us.AvgDuration)
			durationNs := float64(duration)
			us.AvgDuration = time.Duration(avgNs*(1-avgAlpha) + durationNs*avgAlpha)
		}
		us.LastQuery = entry.Timestamp
	}

	// Update per-client stats
	// Normalize ClientAddr to IP only (strip ephemeral port to avoid cardinality explosion)
	clientIP := entry.ClientAddr
	if host, _, err := net.SplitHostPort(entry.ClientAddr); err == nil {
		clientIP = host
	}
	// Key by IP + username + pool to avoid merging stats for different users behind same IP
	clientKey := clientIP + "|" + entry.Username + "|" + entry.Pool
	if entry.ClientAddr != "" {
		cs, ok := ql.clientStats[clientKey]
		if !ok {
			// Cap clientStats map to bound memory growth under high client cardinality.
			if len(ql.clientStats) >= ql.maxClientStats {
				return // skip per-client stats; global stats already updated above
			}
			cs = &ClientStats{ClientAddr: clientIP, Username: entry.Username, Pool: entry.Pool}
			ql.clientStats[clientKey] = cs
		}
		cs.TotalQueries++
		if entry.IsSlow {
			cs.SlowQueries++
		}
		if cs.TotalQueries == 1 {
			cs.AvgDuration = duration
			cs.MaxDuration = duration
		} else {
			if duration > cs.MaxDuration {
				cs.MaxDuration = duration
			}
			const avgAlpha = 0.001
			avgNs := float64(cs.AvgDuration)
			durationNs := float64(duration)
			cs.AvgDuration = time.Duration(avgNs*(1-avgAlpha) + durationNs*avgAlpha)
		}
		cs.Username = entry.Username // Keep latest username
		cs.Pool = entry.Pool         // Keep latest pool
		cs.LastQuery = entry.Timestamp
	}
}

// addRecentQuery adds a query to recent queries list.
func (ql *QueryLogger) addRecentQuery(entry QueryLogEntry) {
	ql.queryMu.Lock()
	defer ql.queryMu.Unlock()

	ql.recentQueries = append(ql.recentQueries, entry)
	if len(ql.recentQueries) > ql.maxRecentQueries {
		ql.recentQueries = ql.recentQueries[len(ql.recentQueries)-ql.maxRecentQueries:]
	}
}

// addSlowQuery adds a query to slow queries list.
func (ql *QueryLogger) addSlowQuery(entry QueryLogEntry) {
	ql.queryMu.Lock()
	defer ql.queryMu.Unlock()

	ql.slowQueries = append(ql.slowQueries, entry)
	if len(ql.slowQueries) > ql.maxSlowQueries {
		ql.slowQueries = ql.slowQueries[len(ql.slowQueries)-ql.maxSlowQueries:]
	}
}

// GetSlowQueries returns slow queries.
func (ql *QueryLogger) GetSlowQueries(limit int) []QueryLogEntry {
	ql.queryMu.RLock()
	defer ql.queryMu.RUnlock()

	if limit <= 0 || limit > len(ql.slowQueries) {
		limit = len(ql.slowQueries)
	}

	// Return copy in reverse order (newest first)
	result := make([]QueryLogEntry, limit)
	for i := 0; i < limit; i++ {
		result[i] = ql.slowQueries[len(ql.slowQueries)-1-i]
	}
	return result
}

// GetRecentQueries returns recent queries.
func (ql *QueryLogger) GetRecentQueries(limit int) []QueryLogEntry {
	ql.queryMu.RLock()
	defer ql.queryMu.RUnlock()

	if limit <= 0 || limit > len(ql.recentQueries) {
		limit = len(ql.recentQueries)
	}

	// Return copy in reverse order (newest first)
	result := make([]QueryLogEntry, limit)
	for i := 0; i < limit; i++ {
		result[i] = ql.recentQueries[len(ql.recentQueries)-1-i]
	}
	return result
}

// GetTopQueries returns top queries by frequency.
func (ql *QueryLogger) GetTopQueries(limit int) []QueryDigest {
	ql.statsMu.RLock()
	defer ql.statsMu.RUnlock()

	if limit <= 0 || limit > len(ql.stats.TopQueries) {
		limit = len(ql.stats.TopQueries)
	}

	// Return copy
	result := make([]QueryDigest, limit)
	copy(result, ql.stats.TopQueries[:limit])
	return result
}

// GetPerUserStats returns per-user query statistics.
func (ql *QueryLogger) GetPerUserStats() []UserStats {
	ql.statsMu.RLock()
	defer ql.statsMu.RUnlock()

	result := make([]UserStats, 0, len(ql.userStats))
	for _, us := range ql.userStats {
		result = append(result, *us)
	}

	// Sort by total queries descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].TotalQueries > result[j].TotalQueries
	})
	return result
}

// GetPerClientStats returns per-client query statistics.
func (ql *QueryLogger) GetPerClientStats() []ClientStats {
	ql.statsMu.RLock()
	defer ql.statsMu.RUnlock()

	result := make([]ClientStats, 0, len(ql.clientStats))
	for _, cs := range ql.clientStats {
		result = append(result, *cs)
	}

	// Sort by total queries descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].TotalQueries > result[j].TotalQueries
	})
	return result
}

// secretPatterns matches common SQL patterns that may contain credentials.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:CREATE\s+(?:USER|ROLE)|ALTER\s+(?:USER|ROLE)).*?(?:IDENTIFIED\s+BY|PASSWORD)\s+['"][^'"]*['"]`),
	regexp.MustCompile(`(?i)(?:SET\s+PASSWORD(?:\s+FOR\s+\S+)?)\s*=\s*['"][^'"]*['"]`),
	regexp.MustCompile(`(?i)(?:GRANT|REVOKE)\s+.*?ON\s+.*?\s+TO\s+\S+\s+(?:IDENTIFIED\s+BY\s+)?['"][^'"]*['"]`),
	regexp.MustCompile(`(?i)INSERT\s+INTO\s+\S*(?:credential|secret|password|auth)\S*\s+VALUES\s*\([^)]+\)`),
	regexp.MustCompile(`(?i)(?:password|secret|token|credential)\s*=\s*['"][^'"]{4,}['"]`),
}

// redactQuery removes sensitive data from SQL queries.
func redactQuery(query string) string {
	for _, pattern := range secretPatterns {
		query = pattern.ReplaceAllString(query, "[REDACTED]")
	}
	return query
}
