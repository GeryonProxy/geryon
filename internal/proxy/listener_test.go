package proxy

import (
	"testing"
)

func TestParseMemoryString(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"", 64 * 1024 * 1024},
		{"64MB", 64 * 1024 * 1024},
		{"1GB", 1024 * 1024 * 1024},
		{"512KB", 512 * 1024},
		{"0MB", 64 * 1024 * 1024},
		{"2GB", 2 * 1024 * 1024 * 1024},
	}
	for _, tc := range cases {
		if got := parseMemoryString(tc.input); got != tc.want {
			t.Errorf("parseMemoryString(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestNewRelay(t *testing.T) {
	r := NewRelay()
	if r == nil {
		t.Fatal("NewRelay returned nil")
	}
}

func TestIsSelectQuery(t *testing.T) {
	cases := []struct {
		query string
		want  bool
	}{
		{"SELECT * FROM users", true},
		{"  select 1", true},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"INSERT INTO users VALUES (1)", false},
		{"UPDATE users SET x=1", false},
		{"DELETE FROM users", false},
		{"DROP TABLE users", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isSelectQuery(tc.query); got != tc.want {
			t.Errorf("isSelectQuery(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
}

func TestIsModificationQuery(t *testing.T) {
	cases := []struct {
		query string
		want  bool
	}{
		{"INSERT INTO t VALUES (1)", true},
		{"UPDATE t SET x=1", true},
		{"DELETE FROM t", true},
		{"TRUNCATE TABLE t", true},
		{"DROP TABLE t", true},
		{"ALTER TABLE t ADD x INT", true},
		{"CREATE TABLE t (x INT)", true},
		{"REPLACE INTO t VALUES (1)", true},
		{"SELECT * FROM t", false},
		{"  select 1", false},
	}
	for _, tc := range cases {
		if got := isModificationQuery(tc.query); got != tc.want {
			t.Errorf("isModificationQuery(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
}

func TestExtractTablesFromQuery(t *testing.T) {
	cases := []struct {
		query string
		want  []string
	}{
		{"SELECT * FROM users WHERE id=1", []string{"users"}},
		{"SELECT * FROM orders", []string{"orders"}},
		{"SELECT 1", nil},
	}
	for _, tc := range cases {
		got := extractTablesFromQuery(tc.query)
		if len(got) != len(tc.want) {
			t.Errorf("extractTablesFromQuery(%q) = %v, want %v", tc.query, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("extractTablesFromQuery(%q)[%d] = %q, want %q", tc.query, i, got[i], tc.want[i])
			}
		}
	}
}
