package logger

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNewQueryLogger(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		SlowThreshold: 100 * time.Millisecond,
		MaxFileSize:   100 * 1024 * 1024,
		MaxFiles:      10,
		BufferSize:    100,
		FlushInterval: 100 * time.Millisecond,
		LogAllQueries: true,
		LogJSON:       true,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	if ql == nil {
		t.Fatal("QueryLogger should not be nil")
	}

	if !ql.running.Load() {
		t.Error("QueryLogger should be running")
	}
}

func TestNewQueryLogger_Disabled(t *testing.T) {
	config := QueryLogConfig{
		Enabled: false,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}

	if ql == nil {
		t.Fatal("QueryLogger should not be nil even when disabled")
	}

	// Should not be running
	if ql.running.Load() {
		t.Error("QueryLogger should not be running when disabled")
	}
}

func TestNewQueryLogger_InvalidDirectory(t *testing.T) {
	// Use a path with invalid characters for Windows
	config := QueryLogConfig{
		Enabled:       true,
		Directory:     `\\invalid\path\that\cannot\be\created\` + string(rune(0)), // Null char is invalid
		FlushInterval: 1 * time.Second,
	}

	_, err := NewQueryLogger(config)
	if err == nil {
		// On some systems the path might be accepted, skip in that case
		t.Skip("Path was accepted on this system, skipping")
	}
}

func TestQueryLogger_LogQuery(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		SlowThreshold: 50 * time.Millisecond,
		BufferSize:    10,
		FlushInterval: 1 * time.Second,
		LogAllQueries: true,
		LogJSON:       true,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Log a normal query
	entry := QueryLogEntry{
		Timestamp:    time.Now(),
		QueryID:      "q1",
		Pool:         "test-pool",
		ClientAddr:   "127.0.0.1:1234",
		BackendAddr:  "127.0.0.1:5432",
		Username:     "testuser",
		Database:     "testdb",
		Query:        "SELECT * FROM users WHERE id = 1",
		QueryHash:    "hash123",
		Duration:     10 * time.Millisecond,
		RowsAffected: 1,
		RowsReturned: 1,
	}

	ql.LogQuery(entry)

	// Log a slow query
	slowEntry := entry
	slowEntry.QueryID = "q2"
	slowEntry.Duration = 100 * time.Millisecond
	slowEntry.Query = "SELECT * FROM large_table"

	ql.LogQuery(slowEntry)

	// Log a cached query
	cachedEntry := entry
	cachedEntry.QueryID = "q3"
	cachedEntry.IsCached = true
	cachedEntry.Duration = 1 * time.Millisecond

	ql.LogQuery(cachedEntry)

	// Log an error query
	errorEntry := entry
	errorEntry.QueryID = "q4"
	errorEntry.IsError = true
	errorEntry.ErrorMessage = "connection lost"

	ql.LogQuery(errorEntry)

	// Flush to ensure writes
	ql.Flush()

	// Verify files were created
	slowLogPath := filepath.Join(config.Directory, "slow.log")
	if _, err := os.Stat(slowLogPath); os.IsNotExist(err) {
		t.Error("Slow log file should exist")
	}

	allLogPath := filepath.Join(config.Directory, "all.log")
	if _, err := os.Stat(allLogPath); os.IsNotExist(err) {
		t.Error("All log file should exist")
	}

	jsonLogPath := filepath.Join(config.Directory, "queries.json")
	if _, err := os.Stat(jsonLogPath); os.IsNotExist(err) {
		t.Error("JSON log file should exist")
	}
}

func TestQueryLogger_LogQuery_WhenDisabled(t *testing.T) {
	config := QueryLogConfig{
		Enabled: false,
	}

	ql, _ := NewQueryLogger(config)

	// Should not panic
	ql.LogQuery(QueryLogEntry{
		Query:    "SELECT 1",
		Duration: 10 * time.Millisecond,
	})
}

func TestQueryLogger_Stop(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		BufferSize:    10,
		FlushInterval: 100 * time.Millisecond,
		LogAllQueries: false,
		LogJSON:       false,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}

	// Log some entries
	for i := 0; i < 5; i++ {
		ql.LogQuery(QueryLogEntry{
			QueryID:  string(rune('a' + i)),
			Query:    "SELECT 1",
			Duration: 1 * time.Millisecond,
		})
	}

	// Stop should flush and close files
	err = ql.Stop()
	if err != nil {
		t.Errorf("Stop failed: %v", err)
	}

	// Should not be running after stop
	if ql.running.Load() {
		t.Error("QueryLogger should not be running after Stop")
	}

	// Second stop should be safe
	err = ql.Stop()
	if err != nil {
		t.Errorf("Second Stop failed: %v", err)
	}
}

func TestQueryLogger_GetStats(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		SlowThreshold: 50 * time.Millisecond,
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Log some queries
	for i := 0; i < 10; i++ {
		ql.LogQuery(QueryLogEntry{
			QueryID:   string(rune('a' + i)),
			Query:     "SELECT * FROM users",
			QueryHash: "hash1",
			Duration:  time.Duration(i*10) * time.Millisecond,
		})
	}

	// Get stats
	stats := ql.GetStats(time.Now().Add(-1 * time.Hour))

	if stats.TotalQueries != 10 {
		t.Errorf("TotalQueries = %d, want 10", stats.TotalQueries)
	}
}

func TestQueryLogger_GetSlowQueries(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		SlowThreshold: 50 * time.Millisecond,
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Log some queries including slow ones
	for i := 0; i < 10; i++ {
		duration := 10 * time.Millisecond
		if i%2 == 0 {
			duration = 100 * time.Millisecond // Slow query
		}
		ql.LogQuery(QueryLogEntry{
			QueryID:  string(rune('0' + i)),
			Query:    "SELECT * FROM users",
			Duration: duration,
		})
	}

	// Get slow queries
	slowQueries := ql.GetSlowQueries(5)
	if len(slowQueries) == 0 {
		t.Error("Should have some slow queries")
	}

	// Test with limit 0
	allSlow := ql.GetSlowQueries(0)
	if len(allSlow) != len(slowQueries) {
		t.Error("Limit 0 should return all slow queries")
	}

	// Test with high limit
	moreSlow := ql.GetSlowQueries(1000)
	if len(moreSlow) != len(slowQueries) {
		t.Error("High limit should return available slow queries")
	}
}

func TestQueryLogger_GetRecentQueries(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Log some queries
	for i := 0; i < 5; i++ {
		ql.LogQuery(QueryLogEntry{
			QueryID:  string(rune('a' + i)),
			Query:    "SELECT * FROM users",
			Duration: 10 * time.Millisecond,
		})
	}

	// Get recent queries
	recent := ql.GetRecentQueries(3)
	if len(recent) != 3 {
		t.Errorf("GetRecentQueries(3) returned %d queries, want 3", len(recent))
	}

	// Should be in reverse order (newest first)
	if recent[0].QueryID != "e" {
		t.Error("Recent queries should be in reverse order")
	}
}

func TestQueryLogger_GetTopQueries(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Log queries with different hashes
	for i := 0; i < 10; i++ {
		hash := "hash1"
		if i >= 7 {
			hash = "hash2"
		}
		ql.LogQuery(QueryLogEntry{
			QueryID:   string(rune('a' + i)),
			Query:     "SELECT * FROM users",
			QueryHash: hash,
			Duration:  10 * time.Millisecond,
		})
	}

	// Get top queries
	top := ql.GetTopQueries(10)
	if len(top) == 0 {
		t.Error("Should have top queries")
	}

	// hash1 should have higher count
	if len(top) > 0 && top[0].Count != 7 {
		t.Errorf("Top query count = %d, want 7", top[0].Count)
	}
}

func TestQueryLogger_Flush(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		BufferSize:    100,
		FlushInterval: 1 * time.Hour, // Long interval to test manual flush
		LogJSON:       true,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Log some queries
	for i := 0; i < 5; i++ {
		ql.LogQuery(QueryLogEntry{
			QueryID:  string(rune('a' + i)),
			Query:    "SELECT 1",
			Duration: 1 * time.Millisecond,
		})
	}

	// Manual flush
	ql.Flush()

	// Flush on stopped logger should not panic
	ql.Stop()
	ql.Flush()
}

func TestQueryLogger_BufferOverflow(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		BufferSize:    5,
		FlushInterval: 1 * time.Hour,
		LogJSON:       true,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Log more queries than buffer can hold
	for i := 0; i < 100; i++ {
		ql.LogQuery(QueryLogEntry{
			QueryID:  string(rune('a' + (i%26))),
			Query:    "SELECT 1",
			Duration: 1 * time.Millisecond,
		})
	}

	// Should not panic or block
}

func TestRedactQuery(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:     "No sensitive data",
			query:    "SELECT * FROM users",
			expected: "SELECT * FROM users",
		},
		{
			name:     "CREATE USER with password",
			query:    "CREATE USER test IDENTIFIED BY 'secret123'",
			expected: "[REDACTED]",
		},
		{
			name:     "ALTER USER with password",
			query:    "ALTER USER test IDENTIFIED BY 'password'",
			expected: "[REDACTED]",
		},
		{
			name:     "SET PASSWORD",
			query:    "SET PASSWORD = 'newpass'",
			expected: "[REDACTED]",
		},
		{
			name:     "GRANT with identified by",
			query:    "GRANT ALL ON db.* TO user IDENTIFIED BY 'pass'",
			expected: "[REDACTED]",
		},
		{
			name:     "Password in query",
			query:    "UPDATE config SET password = 'secret1234'",
			expected: "UPDATE config [REDACTED]",
		},
		{
			name:     "Secret in query",
			query:    "UPDATE config SET secret = 'mysecret'",
			expected: "UPDATE config SET [REDACTED]",
		},
		{
			name:     "Token in query",
			query:    "UPDATE config SET token = 'bearer_token_123'",
			expected: "UPDATE config SET [REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := redactQuery(tt.query)
			if result != tt.expected {
				t.Errorf("redactQuery() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestMin(t *testing.T) {
	tests := []struct {
		a, b     int
		expected int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{-1, 1, -1},
		{0, 100, 0},
	}

	for _, tt := range tests {
		result := min(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("min(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestQueryLogger_AddRecentQuery(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Add more than maxRecentQueries
	ql.maxRecentQueries = 5
	for i := 0; i < 10; i++ {
		ql.addRecentQuery(QueryLogEntry{
			QueryID: string(rune('a' + i)),
		})
	}

	if len(ql.recentQueries) != 5 {
		t.Errorf("recentQueries length = %d, want 5", len(ql.recentQueries))
	}
}

func TestQueryLogger_AddSlowQuery(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Add more than maxSlowQueries
	ql.maxSlowQueries = 3
	for i := 0; i < 5; i++ {
		ql.addSlowQuery(QueryLogEntry{
			QueryID: string(rune('a' + i)),
		})
	}

	if len(ql.slowQueries) != 3 {
		t.Errorf("slowQueries length = %d, want 3", len(ql.slowQueries))
	}
}

func TestQueryLogger_UpdateStats(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Update stats with various entries
	ql.updateStats(QueryLogEntry{
		Duration: 10 * time.Millisecond,
		QueryHash: "hash1",
		Query:     "SELECT 1",
	})

	ql.updateStats(QueryLogEntry{
		Duration:  20 * time.Millisecond,
		IsSlow:    true,
		IsCached:  true,
		IsError:   true,
		QueryHash: "hash2",
		Query:     "SELECT 2",
	})

	// Update same hash again
	ql.updateStats(QueryLogEntry{
		Duration:  15 * time.Millisecond,
		QueryHash: "hash1",
		Query:     "SELECT 1",
	})

	stats := ql.GetStats(time.Time{})

	if stats.TotalQueries != 3 {
		t.Errorf("TotalQueries = %d, want 3", stats.TotalQueries)
	}
	if stats.SlowQueries != 1 {
		t.Errorf("SlowQueries = %d, want 1", stats.SlowQueries)
	}
	if stats.CachedQueries != 1 {
		t.Errorf("CachedQueries = %d, want 1", stats.CachedQueries)
	}
	if stats.ErrorQueries != 1 {
		t.Errorf("ErrorQueries = %d, want 1", stats.ErrorQueries)
	}

	// Check min/max
	if stats.MinDuration != 10*time.Millisecond {
		t.Errorf("MinDuration = %v, want 10ms", stats.MinDuration)
	}
	if stats.MaxDuration != 20*time.Millisecond {
		t.Errorf("MaxDuration = %v, want 20ms", stats.MaxDuration)
	}
}

func TestQueryLogger_WriteSlowLog(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Test writeSlowLog
	ql.writeSlowLog(QueryLogEntry{
		Timestamp:    time.Now(),
		Pool:         "test-pool",
		Username:     "testuser",
		QueryID:      "q1",
		Query:        "SELECT * FROM very_large_table",
		Duration:     100 * time.Millisecond,
		RowsReturned: 1000,
		ClientAddr:   "127.0.0.1:1234",
		BackendAddr:  "127.0.0.1:5432",
	})
}

func TestQueryLogger_WriteAllLog(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
		LogAllQueries: true,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Test writeAllLog with cached entry
	ql.writeAllLog(QueryLogEntry{
		Timestamp: time.Now(),
		Pool:      "test-pool",
		Query:     "SELECT * FROM users",
		Duration:  10 * time.Millisecond,
		IsCached:  true,
	})

	// Test writeAllLog with uncached entry
	ql.writeAllLog(QueryLogEntry{
		Timestamp: time.Now(),
		Pool:      "test-pool",
		Query:     "SELECT * FROM orders",
		Duration:  20 * time.Millisecond,
		IsCached:  false,
	})
}

func TestQueryLogger_WriteJSONLog(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
		LogJSON:       true,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Test writeJSONLog
	ql.writeJSONLog(QueryLogEntry{
		Timestamp:    time.Now(),
		QueryID:      "q1",
		Pool:         "test-pool",
		Query:        "SELECT * FROM users",
		Duration:     10 * time.Millisecond,
		RowsReturned: 5,
	})
}

func TestSetDefault(t *testing.T) {
	// Reset default logger for testing
	defaultLogger = nil
	once = sync.Once{}

	l, _ := New("info", "json")
	SetDefault(l)

	if defaultLogger != l {
		t.Error("SetDefault should set the default logger")
	}

	// Second call should be ignored (once.Do)
	l2, _ := New("debug", "text")
	SetDefault(l2)

	if defaultLogger != l {
		t.Error("SetDefault should only work once")
	}
}
