package cache

import (
	"container/list"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/GeryonProxy/geryon/internal/tokenizer"
)

// CacheEntry represents a cached query result.
type CacheEntry struct {
	Key        string
	Value      []byte
	Size       int64
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Tables     []string // Tables referenced in the query (for invalidation)
	listElem   *list.Element
}

// IsExpired returns true if the entry has expired.
func (e *CacheEntry) IsExpired() bool {
	return time.Now().After(e.ExpiresAt)
}

// Store implements an LRU cache with TTL support.
type Store struct {
	mu          sync.RWMutex
	entries     map[string]*CacheEntry
	evictionList *list.List // LRU list
	maxMemory   int64
	currentMemory int64
	defaultTTL  time.Duration
	hits        uint64
	misses      uint64
	evictions   uint64
}

// NewStore creates a new cache store.
func NewStore(maxMemory int64, defaultTTL time.Duration) *Store {
	return &Store{
		entries:       make(map[string]*CacheEntry),
		evictionList:  list.New(),
		maxMemory:     maxMemory,
		defaultTTL:    defaultTTL,
	}
}

// Get retrieves a value from the cache.
func (s *Store) Get(key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.entries[key]
	if !exists {
		s.misses++
		return nil, false
	}

	// Check if expired
	if entry.IsExpired() {
		s.removeEntry(entry)
		s.misses++
		return nil, false
	}

	// Move to front (most recently used)
	s.evictionList.MoveToFront(entry.listElem)
	s.hits++

	return entry.Value, true
}

// Set stores a value in the cache.
func (s *Store) Set(key string, value []byte, tables []string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Use default TTL if not specified
	if ttl == 0 {
		ttl = s.defaultTTL
	}

	size := int64(len(value))

	// Check if value is too large for cache
	if size > s.maxMemory {
		return fmt.Errorf("value too large for cache: %d > %d", size, s.maxMemory)
	}

	// Remove existing entry if present
	if existing, exists := s.entries[key]; exists {
		s.removeEntry(existing)
	}

	// Evict entries if necessary to make room
	for s.currentMemory+size > s.maxMemory && s.evictionList.Len() > 0 {
		s.evictLRU()
	}

	// Create new entry
	entry := &CacheEntry{
		Key:       key,
		Value:     value,
		Size:      size,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(ttl),
		Tables:    tables,
	}

	// Add to cache
	s.entries[key] = entry
	entry.listElem = s.evictionList.PushFront(entry)
	s.currentMemory += size

	return nil
}

// Delete removes a value from the cache.
func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry, exists := s.entries[key]; exists {
		s.removeEntry(entry)
	}
}

// InvalidateTable removes all entries that reference the given table.
func (s *Store) InvalidateTable(table string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	toRemove := make([]*CacheEntry, 0)
	for _, entry := range s.entries {
		for _, t := range entry.Tables {
			if t == table {
				toRemove = append(toRemove, entry)
				break
			}
		}
	}

	for _, entry := range toRemove {
		s.removeEntry(entry)
	}
}

// InvalidateTables removes all entries that reference any of the given tables.
func (s *Store) InvalidateTables(tables []string) {
	for _, table := range tables {
		s.InvalidateTable(table)
	}
}

// Clear removes all entries from the cache.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = make(map[string]*CacheEntry)
	s.evictionList.Init()
	s.currentMemory = 0
}

// Stats returns cache statistics.
type Stats struct {
	Entries       int
	MemoryUsed    int64
	MemoryMax     int64
	Hits          uint64
	Misses        uint64
	Evictions     uint64
	HitRate       float64
}

// Stats returns cache statistics.
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := s.hits + s.misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(s.hits) / float64(total) * 100
	}

	return Stats{
		Entries:    len(s.entries),
		MemoryUsed: s.currentMemory,
		MemoryMax:  s.maxMemory,
		Hits:       s.hits,
		Misses:     s.misses,
		Evictions:  s.evictions,
		HitRate:    hitRate,
	}
}

// removeEntry removes an entry from the cache.
func (s *Store) removeEntry(entry *CacheEntry) {
	s.evictionList.Remove(entry.listElem)
	delete(s.entries, entry.Key)
	s.currentMemory -= entry.Size
}

// evictLRU removes the least recently used entry.
func (s *Store) evictLRU() {
	elem := s.evictionList.Back()
	if elem == nil {
		return
	}

	entry := elem.Value.(*CacheEntry)
	s.removeEntry(entry)
	s.evictions++
}

// StartCleanup starts a background goroutine to clean up expired entries.
func (s *Store) StartCleanup(interval time.Duration) *time.Ticker {
	ticker := time.NewTicker(interval)

	go func() {
		for range ticker.C {
			s.cleanupExpired()
		}
	}()

	return ticker
}

// cleanupExpired removes expired entries from the cache.
func (s *Store) cleanupExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()

	toRemove := make([]*CacheEntry, 0)
	for _, entry := range s.entries {
		if entry.IsExpired() {
			toRemove = append(toRemove, entry)
		}
	}

	for _, entry := range toRemove {
		s.removeEntry(entry)
	}
}

// Key represents a cache key.
type Key struct {
	Query     string
	Normalized string
}

// GenerateKey generates a cache key from a query.
func GenerateKey(query string) Key {
	normalized := tokenizer.NormalizeQuery(query)
	return Key{
		Query:      query,
		Normalized: normalized,
	}
}

// String returns the string representation of the key.
func (k Key) String() string {
	return k.Normalized
}

// Rule represents a cache rule with TTL.
type Rule struct {
	Pattern *regexp.Regexp
	TTL     time.Duration
	Cache   bool // If false, never cache matching queries
}

// RulesEngine manages cache rules.
type RulesEngine struct {
	rules     []Rule
	maxRules  int
	maxPatternLen int
}

// NewRulesEngine creates a new rules engine.
func NewRulesEngine() *RulesEngine {
	return &RulesEngine{
		rules:         make([]Rule, 0),
		maxRules:      100,
		maxPatternLen: 1024,
	}
}

// AddRule adds a cache rule.
func (e *RulesEngine) AddRule(pattern string, ttl time.Duration, cache bool) error {
	// Bound pattern length to prevent ReDoS
	if len(pattern) > e.maxPatternLen {
		return fmt.Errorf("pattern too long: %d > %d", len(pattern), e.maxPatternLen)
	}

	// Bound number of rules
	if len(e.rules) >= e.maxRules {
		return fmt.Errorf("too many cache rules: %d >= %d", len(e.rules), e.maxRules)
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid pattern: %w", err)
	}

	e.rules = append(e.rules, Rule{
		Pattern: re,
		TTL:     ttl,
		Cache:   cache,
	})

	return nil
}

// Match finds the matching rule for a query.
func (e *RulesEngine) Match(query string) *Rule {
	// Return last matching rule (allows overrides)
	var matched *Rule
	for _, rule := range e.rules {
		if rule.Pattern.MatchString(query) {
			matched = &rule
		}
	}
	return matched
}

// ShouldCache returns true if the query should be cached.
func (e *RulesEngine) ShouldCache(query string) bool {
	rule := e.Match(query)
	if rule == nil {
		return true // Default: cache everything
	}
	return rule.Cache
}

// GetTTL returns the TTL for a query.
func (e *RulesEngine) GetTTL(query string, defaultTTL time.Duration) time.Duration {
	rule := e.Match(query)
	if rule == nil || rule.TTL == 0 {
		return defaultTTL
	}
	return rule.TTL
}

// ParseDuration parses a duration string (e.g., "300s", "5m", "1h").
func ParseDuration(s string) (time.Duration, error) {
	if s == "0" || s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}
