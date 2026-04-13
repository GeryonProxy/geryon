package logger

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// --- New with invalid level ---

func TestNew_InvalidLevel(t *testing.T) {
	_, err := New("badlevel", "json")
	if err == nil {
		t.Error("Should fail for invalid level")
	}
}

// --- New with warn level covers levelToSlogLevel Warn case ---

func TestNew_WarnLevel(t *testing.T) {
	l, err := New("warn", "json")
	if err != nil {
		t.Fatalf("New(warn, json) failed: %v", err)
	}
	if l == nil {
		t.Fatal("Logger should not be nil")
	}
}

// --- levelToSlogLevel default case ---

func TestLevelToSlogLevel_Default(t *testing.T) {
	result := levelToSlogLevel(Level(99))
	if result != 0 {
		// LevelInfo is slog.LevelInfo = 0
		t.Errorf("levelToSlogLevel(99) = %v, want LevelInfo", result)
	}
}

// --- Default when logger is already set ---

func TestDefault_AlreadySet(t *testing.T) {
	// Reset state
	defaultLogger = nil
	once = sync.Once{}

	l, _ := New("debug", "text")
	SetDefault(l)

	// Now Default should return the set logger, not create a new one
	d := Default()
	if d != l {
		t.Error("Default should return the previously set logger")
	}

	// Cleanup
	defaultLogger = nil
	once = sync.Once{}
}

// --- flushLoop ticker tick ---

func TestQueryLogger_FlushLoop_Ticker(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		BufferSize:    100,
		FlushInterval: 50 * time.Millisecond, // Short interval for testing
		LogJSON:       true,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}

	// Log an entry
	ql.LogQuery(QueryLogEntry{
		QueryID:  "ticker-test",
		Query:    "SELECT 1",
		Duration: 1 * time.Millisecond,
	})

	// Wait for flush ticker to fire
	time.Sleep(150 * time.Millisecond)

	// Verify JSON log has content (flushed by ticker)
	jsonPath := filepath.Join(config.Directory, "queries.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("Failed to read JSON log: %v", err)
	}
	if len(data) == 0 {
		t.Error("JSON log should have content after ticker flush")
	}

	ql.Stop()
}

// --- GetRecentQueries with negative limit ---

func TestQueryLogger_GetRecentQueries_NegativeLimit(t *testing.T) {
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
			Query:    "SELECT 1",
			Duration: 1 * time.Millisecond,
		})
	}

	// Negative limit should return all
	recent := ql.GetRecentQueries(-1)
	if len(recent) != 5 {
		t.Errorf("GetRecentQueries(-1) = %d entries, want 5", len(recent))
	}
}

// --- GetRecentQueries with limit > len ---

func TestQueryLogger_GetRecentQueries_LimitOverCount(t *testing.T) {
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

	// Log 3 queries
	for i := 0; i < 3; i++ {
		ql.LogQuery(QueryLogEntry{
			QueryID:  string(rune('a' + i)),
			Query:    "SELECT 1",
			Duration: 1 * time.Millisecond,
		})
	}

	// Limit > count should return all
	recent := ql.GetRecentQueries(100)
	if len(recent) != 3 {
		t.Errorf("GetRecentQueries(100) = %d entries, want 3", len(recent))
	}
}

// --- NewQueryLogger with slow log open error ---

func TestNewQueryLogger_SlowLogOpenError(t *testing.T) {
	tempDir := t.TempDir()
	queryDir := filepath.Join(tempDir, "queries")

	// Create the directory but make slow.log a directory so OpenFile fails
	os.MkdirAll(queryDir, 0755)
	os.MkdirAll(filepath.Join(queryDir, "slow.log"), 0755)

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     queryDir,
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
		LogAllQueries: false,
		LogJSON:       false,
	}

	_, err := NewQueryLogger(config)
	if err == nil {
		t.Error("Should fail when slow.log is a directory")
	}
}

// --- NewQueryLogger with all log open error ---

func TestNewQueryLogger_AllLogOpenError(t *testing.T) {
	tempDir := t.TempDir()
	queryDir := filepath.Join(tempDir, "queries")

	os.MkdirAll(queryDir, 0755)
	// Create all.log as a directory
	os.MkdirAll(filepath.Join(queryDir, "all.log"), 0755)

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     queryDir,
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
		LogAllQueries: true,
		LogJSON:       false,
	}

	_, err := NewQueryLogger(config)
	if err == nil {
		t.Error("Should fail when all.log is a directory")
	}
}

// --- NewQueryLogger with json log open error ---

func TestNewQueryLogger_JSONLogOpenError(t *testing.T) {
	tempDir := t.TempDir()
	queryDir := filepath.Join(tempDir, "queries")

	os.MkdirAll(queryDir, 0755)
	// Create queries.json as a directory
	os.MkdirAll(filepath.Join(queryDir, "queries.json"), 0755)

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     queryDir,
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
		LogAllQueries: false,
		LogJSON:       true,
	}

	_, err := NewQueryLogger(config)
	if err == nil {
		t.Error("Should fail when queries.json is a directory")
	}
}

// --- LogQuery when not running ---

func TestQueryLogger_LogQuery_NotRunning(t *testing.T) {
	config := QueryLogConfig{
		Enabled: false,
	}
	ql, _ := NewQueryLogger(config)

	// Mark as not running
	ql.running.Store(false)

	ql.LogQuery(QueryLogEntry{
		Query:    "SELECT 1",
		Duration: 1 * time.Millisecond,
	})
	// Should not panic
}

// --- LogQuery with all paths (LogAllQueries + LogJSON + slow) ---

func TestQueryLogger_LogQuery_AllOutputPaths(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		SlowThreshold: 10 * time.Millisecond,
		BufferSize:    100,
		FlushInterval: 1 * time.Hour,
		LogAllQueries: true,
		LogJSON:       true,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Log a slow query that triggers all three output paths
	ql.LogQuery(QueryLogEntry{
		Timestamp:    time.Now(),
		QueryID:      "all-paths-q",
		Pool:         "test-pool",
		ClientAddr:   "127.0.0.1:1234",
		BackendAddr:  "127.0.0.1:5432",
		Username:     "testuser",
		Database:     "testdb",
		Query:        "SELECT * FROM large_table",
		QueryHash:    "hash-all",
		Duration:     100 * time.Millisecond, // > SlowThreshold
		RowsAffected: 1000,
		RowsReturned: 1000,
		IsCached:     true,
	})

	ql.Flush()

	// Verify all three log files have content
	slowData, _ := os.ReadFile(filepath.Join(config.Directory, "slow.log"))
	if len(slowData) == 0 {
		t.Error("Slow log should have content")
	}

	allData, _ := os.ReadFile(filepath.Join(config.Directory, "all.log"))
	if len(allData) == 0 {
		t.Error("All log should have content")
	}

	jsonData, _ := os.ReadFile(filepath.Join(config.Directory, "queries.json"))
	if len(jsonData) == 0 {
		t.Error("JSON log should have content")
	}
}

// --- GetSlowQueries with negative limit ---

func TestQueryLogger_GetSlowQueries_NegativeLimit(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		SlowThreshold: 10 * time.Millisecond,
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
	}

	ql, err := NewQueryLogger(config)
	if err != nil {
		t.Fatalf("NewQueryLogger failed: %v", err)
	}
	defer ql.Stop()

	// Log slow queries
	for i := 0; i < 5; i++ {
		ql.LogQuery(QueryLogEntry{
			QueryID:  string(rune('a' + i)),
			Query:    "SELECT * FROM big_table",
			Duration: 100 * time.Millisecond,
		})
	}

	// Negative limit should return all
	slow := ql.GetSlowQueries(-5)
	if len(slow) != 5 {
		t.Errorf("GetSlowQueries(-5) = %d entries, want 5", len(slow))
	}
}

// --- GetTopQueries with negative limit ---

func TestQueryLogger_GetTopQueries_NegativeLimit(t *testing.T) {
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

	for i := 0; i < 3; i++ {
		ql.LogQuery(QueryLogEntry{
			QueryID:   string(rune('a' + i)),
			Query:     "SELECT 1",
			QueryHash: "hash1",
			Duration:  1 * time.Millisecond,
		})
	}

	// Negative limit should return all
	top := ql.GetTopQueries(-1)
	if len(top) != 1 {
		t.Errorf("GetTopQueries(-1) = %d entries, want 1", len(top))
	}
}

// --- updateStats with existing hash min/max duration update ---

func TestQueryLogger_UpdateStats_ExistingHashMinMax(t *testing.T) {
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

	// First entry with hash1
	ql.updateStats(QueryLogEntry{
		Duration:  20 * time.Millisecond,
		QueryHash: "hash1",
		Query:     "SELECT 1",
	})

	// Second entry with same hash but higher duration
	ql.updateStats(QueryLogEntry{
		Duration:  50 * time.Millisecond,
		QueryHash: "hash1",
		Query:     "SELECT 1",
	})

	// Third entry with same hash but lower duration
	ql.updateStats(QueryLogEntry{
		Duration:  5 * time.Millisecond,
		QueryHash: "hash1",
		Query:     "SELECT 1",
	})

	stats := ql.GetStats(time.Time{})
	if stats.TotalQueries != 3 {
		t.Errorf("TotalQueries = %d, want 3", stats.TotalQueries)
	}

	top := ql.GetTopQueries(10)
	if len(top) != 1 {
		t.Fatalf("TopQueries = %d entries, want 1", len(top))
	}
	if top[0].Count != 3 {
		t.Errorf("Count = %d, want 3", top[0].Count)
	}
	if top[0].MinDuration != 5*time.Millisecond {
		t.Errorf("MinDuration = %v, want 5ms", top[0].MinDuration)
	}
	if top[0].MaxDuration != 50*time.Millisecond {
		t.Errorf("MaxDuration = %v, want 50ms", top[0].MaxDuration)
	}
}

// --- MustNew success path ---

func TestMustNew_Success(t *testing.T) {
	l := MustNew("info", "json")
	if l == nil {
		t.Error("MustNew should return a valid logger")
	}
}

// --- Buffer overflow at 2x limit ---

func TestQueryLogger_BufferOverflow_2xLimit(t *testing.T) {
	tempDir := t.TempDir()

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(tempDir, "queries"),
		BufferSize:    5,
		FlushInterval: 1 * time.Hour,
		LogJSON:       false,
	}

	ql, _ := NewQueryLogger(config)
	defer ql.Stop()

	// Manually fill buffer to 2x capacity
	ql.bufferMu.Lock()
	for i := 0; i < 10; i++ {
		ql.buffer = append(ql.buffer, QueryLogEntry{QueryID: "filler", Query: "SELECT 1"})
	}
	ql.bufferMu.Unlock()

	// Now log a query - should be dropped because buffer >= 2*BufferSize
	ql.LogQuery(QueryLogEntry{
		QueryID:  "overflow",
		Query:    "SELECT 1",
		Duration: 1 * time.Millisecond,
	})

	// Buffer should still be at 10 (the new entry was dropped)
	ql.bufferMu.Lock()
	bufLen := len(ql.buffer)
	ql.bufferMu.Unlock()

	if bufLen != 10 {
		t.Errorf("Buffer should be capped at 10 (2x5), got %d", bufLen)
	}
}

// --- NewQueryLogger MkdirAll error (file blocking directory creation) ---

func TestNewQueryLogger_MkdirAllError(t *testing.T) {
	tempDir := t.TempDir()
	// Create a file where the directory should be
	blockingFile := filepath.Join(tempDir, "blocked")
	os.WriteFile(blockingFile, []byte("not a dir"), 0644)

	config := QueryLogConfig{
		Enabled:       true,
		Directory:     filepath.Join(blockingFile, "sub", "queries"),
		BufferSize:    100,
		FlushInterval: 1 * time.Second,
	}

	_, err := NewQueryLogger(config)
	if err == nil {
		t.Error("Should fail when directory can't be created (file blocking path)")
	}
}
