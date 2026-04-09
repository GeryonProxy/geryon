package logger

import (
	"testing"
	"time"
)

func TestLevel_String(t *testing.T) {
	cases := []struct {
		level Level
		want  string
	}{
		{Debug, "debug"},
		{Info, "info"},
		{Warn, "warn"},
		{Error, "error"},
		{Level(99), "info"},
	}
	for _, tc := range cases {
		if got := tc.level.String(); got != tc.want {
			t.Errorf("Level(%d).String() = %q, want %q", tc.level, got, tc.want)
		}
	}
}

func TestParseLevel(t *testing.T) {
	cases := []struct {
		input string
		want  Level
	}{
		{"debug", Debug},
		{"DEBUG", Debug},
		{"info", Info},
		{"warn", Warn},
		{"warning", Warn},
		{"error", Error},
	}
	for _, tc := range cases {
		got, err := ParseLevel(tc.input)
		if err != nil {
			t.Fatalf("ParseLevel(%q) failed: %v", tc.input, err)
		}
		if got != tc.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}

	_, err := ParseLevel("unknown")
	if err == nil {
		t.Error("Should fail for unknown level")
	}
}

func TestNew(t *testing.T) {
	l, err := New("debug", "json")
	if err != nil {
		t.Fatalf("New(debug, json) failed: %v", err)
	}
	if l == nil {
		t.Fatal("Logger should not be nil")
	}

	l2, err := New("error", "text")
	if err != nil {
		t.Fatalf("New(error, text) failed: %v", err)
	}
	if l2 == nil {
		t.Fatal("Logger should not be nil")
	}

	// Invalid format
	_, err = New("info", "xml")
	if err == nil {
		t.Error("Should fail for invalid format")
	}
}

func TestMustNew(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustNew should panic for invalid input")
		}
	}()
	MustNew("info", "invalid")
}

func TestDefault(t *testing.T) {
	l := Default()
	if l == nil {
		t.Fatal("Default should not be nil")
	}
}

func TestLogger_Methods(t *testing.T) {
	l, _ := New("debug", "json")
	// These should not panic
	l.Debug("test")
	l.Info("test")
	l.Warn("test")
	l.Error("test")
	// With should return a new logger
	l2 := l.With("key", "value")
	if l2 == nil {
		t.Error("With returned nil")
	}
}

func TestQueryLogEntry(t *testing.T) {
	entry := QueryLogEntry{
		Query:        "SELECT 1",
		Duration:     50 * time.Millisecond,
		IsSlow:       false,
		RowsAffected: 1,
	}
	if entry.Query != "SELECT 1" {
		t.Errorf("Query = %q, want SELECT 1", entry.Query)
	}
}

func TestDefaultQueryLogConfig(t *testing.T) {
	cfg := DefaultQueryLogConfig()
	if !cfg.Enabled {
		t.Error("Query log should be enabled by default")
	}
	if cfg.SlowThreshold != 100*time.Millisecond {
		t.Errorf("SlowThreshold = %v, want 100ms", cfg.SlowThreshold)
	}
	if cfg.BufferSize != 1000 {
		t.Errorf("BufferSize = %d, want 1000", cfg.BufferSize)
	}
	if !cfg.LogJSON {
		t.Error("LogJSON should be true by default")
	}
}

func TestQueryStats(t *testing.T) {
	stats := QueryStats{
		TotalQueries: 100,
		SlowQueries:  5,
		MaxDuration:  500 * time.Millisecond,
	}
	if stats.TotalQueries != 100 {
		t.Errorf("TotalQueries = %d, want 100", stats.TotalQueries)
	}
}

func TestQueryDigest(t *testing.T) {
	digest := QueryDigest{
		QueryHash: "abc123",
		Count:     42,
	}
	if digest.Count != 42 {
		t.Errorf("Count = %d, want 42", digest.Count)
	}
}
