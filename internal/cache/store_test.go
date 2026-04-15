package cache

import (
	"fmt"
	"testing"
	"time"
)

func TestNewStore(t *testing.T) {
	s := NewStore(1024*1024, 5*time.Minute)
	if s == nil {
		t.Fatal("NewStore returned nil")
	}
	if s.maxMemory != 1024*1024 {
		t.Errorf("expected maxMemory 1048576, got %d", s.maxMemory)
	}
	if s.defaultTTL != 5*time.Minute {
		t.Errorf("expected defaultTTL 5m, got %v", s.defaultTTL)
	}
	if len(s.entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(s.entries))
	}
}

func TestStoreSetAndGet(t *testing.T) {
	s := NewStore(1024*1024, 5*time.Minute)

	// Test basic set and get
	key := "test-key"
	value := []byte("test-value")
	tables := []string{"users"}

	err := s.Set(key, value, tables, time.Minute)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got, hit := s.Get(key)
	if !hit {
		t.Fatal("expected cache hit, got miss")
	}
	if string(got) != string(value) {
		t.Errorf("expected %s, got %s", value, got)
	}
}

func TestStoreGetMiss(t *testing.T) {
	s := NewStore(1024*1024, 5*time.Minute)

	_, hit := s.Get("non-existent-key")
	if hit {
		t.Fatal("expected cache miss for non-existent key")
	}

	stats := s.Stats()
	if stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", stats.Misses)
	}
}

func TestStoreExpiration(t *testing.T) {
	s := NewStore(1024*1024, 5*time.Minute)

	// Set with very short TTL
	key := "expiring-key"
	value := []byte("expiring-value")
	err := s.Set(key, value, nil, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Should exist immediately
	_, hit := s.Get(key)
	if !hit {
		t.Fatal("expected cache hit immediately after set")
	}

	// Wait for expiration
	time.Sleep(50 * time.Millisecond)

	// Should be expired now
	_, hit = s.Get(key)
	if hit {
		t.Fatal("expected cache miss after expiration")
	}
}

func TestStoreDelete(t *testing.T) {
	s := NewStore(1024*1024, 5*time.Minute)

	key := "delete-key"
	value := []byte("delete-value")
	s.Set(key, value, nil, time.Minute)

	// Verify it exists
	_, hit := s.Get(key)
	if !hit {
		t.Fatal("expected key to exist before delete")
	}

	// Delete it
	s.Delete(key)

	// Verify it's gone
	_, hit = s.Get(key)
	if hit {
		t.Fatal("expected key to be deleted")
	}
}

func TestStoreInvalidateTable(t *testing.T) {
	s := NewStore(1024*1024, 5*time.Minute)

	// Add entries referencing different tables
	s.Set("key1", []byte("value1"), []string{"users", "accounts"}, time.Minute)
	s.Set("key2", []byte("value2"), []string{"orders"}, time.Minute)
	s.Set("key3", []byte("value3"), []string{"users", "orders"}, time.Minute)

	// Invalidate users table
	s.InvalidateTable("users")

	// key1 and key3 should be gone, key2 should remain
	_, hit := s.Get("key1")
	if hit {
		t.Error("expected key1 to be invalidated")
	}
	_, hit = s.Get("key3")
	if hit {
		t.Error("expected key3 to be invalidated")
	}
	_, hit = s.Get("key2")
	if !hit {
		t.Error("expected key2 to still exist")
	}
}

func TestStoreLRUEviction(t *testing.T) {
	// Create cache that can hold 2 entries (40 bytes) but not 3 (60 bytes)
	s := NewStore(50, 5*time.Minute)

	// Add 2 entries (40 bytes total, fits)
	s.Set("key1", []byte("value1-with-20-bytes"), nil, time.Minute)
	s.Set("key2", []byte("value2-with-20-bytes"), nil, time.Minute)

	// Access key1 to make it most recently used
	s.Get("key1")

	// Add 3rd entry to trigger eviction (60 bytes > 50 max)
	// key2 should be evicted (least recently used)
	s.Set("key3", []byte("value3-with-20-bytes"), nil, time.Minute)

	stats := s.Stats()
	if stats.Evictions == 0 {
		t.Error("expected some evictions")
	}

	// key1 should still exist (recently accessed)
	_, hit := s.Get("key1")
	if !hit {
		t.Error("expected key1 to still exist (recently used)")
	}

	// key2 should have been evicted
	_, hit = s.Get("key2")
	if hit {
		t.Error("expected key2 to be evicted (least recently used)")
	}
}

func TestStoreValueTooLarge(t *testing.T) {
	s := NewStore(50, 5*time.Minute)

	// Try to set value larger than max memory (100 bytes > 50 bytes)
	err := s.Set("key", []byte("this-is-a-very-long-value-that-exceeds-the-maximum-allowed-size-in-the-cache!!"), nil, time.Minute)
	if err == nil {
		t.Error("expected error for value too large")
	}
}

func TestStoreClear(t *testing.T) {
	s := NewStore(1024*1024, 5*time.Minute)

	s.Set("key1", []byte("value1"), nil, time.Minute)
	s.Set("key2", []byte("value2"), nil, time.Minute)

	s.Clear()

	_, hit := s.Get("key1")
	if hit {
		t.Error("expected key1 to be cleared")
	}
	_, hit = s.Get("key2")
	if hit {
		t.Error("expected key2 to be cleared")
	}

	stats := s.Stats()
	if stats.Entries != 0 {
		t.Errorf("expected 0 entries after clear, got %d", stats.Entries)
	}
	if stats.MemoryUsed != 0 {
		t.Errorf("expected 0 memory used after clear, got %d", stats.MemoryUsed)
	}
}

func TestStoreStats(t *testing.T) {
	s := NewStore(1024*1024, 5*time.Minute)

	// Generate hits and misses
	s.Set("key", []byte("value"), nil, time.Minute)
	s.Get("key")         // hit
	s.Get("key")         // hit
	s.Get("nonexistent") // miss

	stats := s.Stats()
	if stats.Hits != 2 {
		t.Errorf("expected 2 hits, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", stats.Misses)
	}
	if stats.Entries != 1 {
		t.Errorf("expected 1 entry, got %d", stats.Entries)
	}
	if stats.MemoryMax != 1024*1024 {
		t.Errorf("expected max memory 1048576, got %d", stats.MemoryMax)
	}

	// Check hit rate
	expectedHitRate := 2.0 / 3.0 * 100 // 66.67%
	if stats.HitRate < expectedHitRate-1 || stats.HitRate > expectedHitRate+1 {
		t.Errorf("expected hit rate around %.2f, got %.2f", expectedHitRate, stats.HitRate)
	}
}

func TestCacheEntryIsExpired(t *testing.T) {
	entry := &CacheEntry{
		ExpiresAt: time.Now().Add(-1 * time.Millisecond),
	}
	if !entry.IsExpired() {
		t.Error("expected expired entry to be expired")
	}

	entry.ExpiresAt = time.Now().Add(time.Hour)
	if entry.IsExpired() {
		t.Error("expected non-expired entry to not be expired")
	}
}

func TestGenerateKey(t *testing.T) {
	// Test with normalized query
	key := GenerateKey("SELECT * FROM users WHERE id = 1")
	if key.Query != "SELECT * FROM users WHERE id = 1" {
		t.Errorf("expected original query, got %s", key.Query)
	}
	if key.Normalized == "" {
		t.Error("expected normalized query to not be empty")
	}

	// Test String() method
	keyStr := key.String()
	if keyStr != key.Normalized {
		t.Error("String() should return normalized query")
	}
}

func TestRulesEngine(t *testing.T) {
	engine := NewRulesEngine()

	// Add cache rule
	err := engine.AddRule("SELECT.*FROM users", 5*time.Minute, true)
	if err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Add no-cache rule
	err = engine.AddRule("SELECT.*FROM passwords", 0, false)
	if err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Test ShouldCache
	if !engine.ShouldCache("SELECT * FROM users WHERE id = 1") {
		t.Error("expected users query to be cached")
	}
	if engine.ShouldCache("SELECT * FROM passwords") {
		t.Error("expected passwords query to not be cached")
	}
	if !engine.ShouldCache("SELECT * FROM orders") {
		t.Error("expected unmatched query to be cached by default")
	}

	// Test GetTTL
	ttl := engine.GetTTL("SELECT * FROM users WHERE id = 1", time.Minute)
	if ttl != 5*time.Minute {
		t.Errorf("expected 5m TTL, got %v", ttl)
	}

	ttl = engine.GetTTL("SELECT * FROM unknown", 10*time.Minute)
	if ttl != 10*time.Minute {
		t.Errorf("expected default TTL for unmatched query, got %v", ttl)
	}
}

func TestRulesEngineInvalidPattern(t *testing.T) {
	engine := NewRulesEngine()
	err := engine.AddRule("[invalid", time.Minute, true)
	if err == nil {
		t.Error("expected error for invalid regex pattern")
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"5m", 5 * time.Minute, false},
		{"1h", time.Hour, false},
		{"300s", 300 * time.Second, false},
		{"0", 0, false},
		{"", 0, false},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		got, err := ParseDuration(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseDuration(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseDuration(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.expected {
			t.Errorf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestStoreCleanupExpired(t *testing.T) {
	s := NewStore(1024*1024, 5*time.Minute)

	// Set entries with different TTLs
	s.Set("expire-quick", []byte("value"), nil, 1*time.Millisecond)
	s.Set("expire-slow", []byte("value"), nil, time.Hour)

	// Wait for first to expire
	time.Sleep(50 * time.Millisecond)

	// Run cleanup
	s.cleanupExpired()

	// Quick should be gone
	_, hit := s.Get("expire-quick")
	if hit {
		t.Error("expected expired entry to be cleaned up")
	}

	// Slow should still exist
	_, hit = s.Get("expire-slow")
	if !hit {
		t.Error("expected non-expired entry to remain")
	}
}

func TestStoreInvalidateTables(t *testing.T) {
	s := NewStore(1024*1024, 5*time.Minute)

	s.Set("key1", []byte("value1"), []string{"users"}, time.Hour)
	s.Set("key2", []byte("value2"), []string{"orders"}, time.Hour)
	s.Set("key3", []byte("value3"), []string{"users", "orders"}, time.Hour)

	// Invalidate by "users" table
	s.InvalidateTables([]string{"users"})

	_, hit := s.Get("key1")
	if hit {
		t.Error("key1 should be invalidated (users table)")
	}

	_, hit = s.Get("key2")
	if !hit {
		t.Error("key2 should still exist (orders table, not affected)")
	}

	_, hit = s.Get("key3")
	if hit {
		t.Error("key3 should be invalidated (users table)")
	}
}

func TestStoreStartCleanup(t *testing.T) {
	s := NewStore(1024*1024, 5*time.Minute)

	s.Set("expire", []byte("value"), nil, 50*time.Millisecond)
	s.Set("persist", []byte("value"), nil, time.Hour)

	// Start cleanup with short interval
	ticker := s.StartCleanup(25 * time.Millisecond)
	defer ticker.Stop()

	// Wait for cleanup to run
	time.Sleep(150 * time.Millisecond)

	// Expired entry should be gone
	_, hit := s.Get("expire")
	if hit {
		t.Error("expired entry should be cleaned up")
	}

	// Persistent entry should remain
	_, hit = s.Get("persist")
	if !hit {
		t.Error("non-expired entry should remain")
	}
}

func TestStoreStats_HitRate(t *testing.T) {
	s := NewStore(1024*1024, 5*time.Minute)
	s.Set("k", []byte("v"), nil, time.Hour)

	// 2 hits
	s.Get("k")
	s.Get("k")
	// 1 miss
	s.Get("nonexistent")

	stats := s.Stats()
	if stats.Entries != 1 {
		t.Errorf("Entries = %d, want 1", stats.Entries)
	}
	if stats.Hits != 2 {
		t.Errorf("Hits = %d, want 2", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Misses = %d, want 1", stats.Misses)
	}
	// Hit rate should be 2/3 * 100 = ~66.67%
	if stats.HitRate < 66.0 || stats.HitRate > 67.0 {
		t.Errorf("HitRate = %.2f, want ~66.67", stats.HitRate)
	}
}

func TestKey_String(t *testing.T) {
	k := Key{
		Query:      "SELECT * FROM users",
		Normalized: "select * from users",
	}
	s := k.String()
	if s == "" {
		t.Error("String() returned empty")
	}
}

func TestRulesEngineShouldCache_DefaultTrue(t *testing.T) {
	re := NewRulesEngine()
	// Default: cache everything when no rules match
	if !re.ShouldCache("RANDOM_QUERY") {
		t.Error("ShouldCache should return true by default (cache everything)")
	}

	// Add a rule that disables caching for SELECT
	re.AddRule("SELECT.*", time.Minute, false)
	if re.ShouldCache("SELECT * FROM users") {
		t.Error("ShouldCache should return false for SELECT when rule disables it")
	}
}

func BenchmarkStoreInvalidateTables(b *testing.B) {
	s := NewStore(100*1024*1024, 5*time.Minute)
	for i := 0; i < 100; i++ {
		s.Set(fmt.Sprintf("key-%d", i), []byte("value"), []string{"table1"}, time.Hour)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.InvalidateTables([]string{"table1"})
	}
}

func BenchmarkStoreSet(b *testing.B) {
	s := NewStore(100*1024*1024, 5*time.Minute)
	value := []byte("benchmark-value-data")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		s.Set(key, value, nil, time.Minute)
	}
}

func BenchmarkStoreGet(b *testing.B) {
	s := NewStore(100*1024*1024, 5*time.Minute)
	s.Set("bench-key", []byte("benchmark-value-data"), nil, time.Minute)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Get("bench-key")
	}
}
