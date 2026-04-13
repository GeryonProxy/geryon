package stmt

import (
	"testing"
)

func TestCache_PutAndGet(t *testing.T) {
	c := NewCache(10)
	stmt := &Statement{Name: "s1", SQL: "SELECT * FROM t", NumParams: 0}

	if err := c.Put(stmt); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	got := c.Get("s1")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.SQL != stmt.SQL {
		t.Errorf("Get SQL = %q, want %q", got.SQL, stmt.SQL)
	}

	if got = c.Get("nonexistent"); got != nil {
		t.Error("Get nonexistent should return nil")
	}
}

func TestCache_GetBySQL(t *testing.T) {
	c := NewCache(10)
	stmt := &Statement{Name: "s1", SQL: "SELECT * FROM t", NumParams: 0}
	c.Put(stmt)

	got := c.GetBySQL("SELECT * FROM t")
	if got == nil {
		t.Fatal("GetBySQL returned nil")
	}
	if got.Name != "s1" {
		t.Errorf("GetBySQL Name = %q, want %q", got.Name, "s1")
	}
}

func TestCache_Eviction(t *testing.T) {
	c := NewCache(3)

	c.Put(&Statement{Name: "s1", SQL: "SQL1"})
	c.Put(&Statement{Name: "s2", SQL: "SQL2"})
	c.Put(&Statement{Name: "s3", SQL: "SQL3"})

	// s1 should be evicted when adding s4
	c.Put(&Statement{Name: "s4", SQL: "SQL4"})

	if c.Get("s1") != nil {
		t.Error("s1 should have been evicted")
	}
	if c.Get("s4") == nil {
		t.Error("s4 should exist")
	}
}

func TestCache_Remove(t *testing.T) {
	c := NewCache(10)
	c.Put(&Statement{Name: "s1", SQL: "SQL1"})

	c.Remove("s1")
	if c.Get("s1") != nil {
		t.Error("Removed statement should return nil")
	}
	if c.GetBySQL("SQL1") != nil {
		t.Error("SQL mapping should be removed too")
	}
}

func TestCache_Clear(t *testing.T) {
	c := NewCache(10)
	c.Put(&Statement{Name: "s1", SQL: "SQL1"})
	c.Put(&Statement{Name: "s2", SQL: "SQL2"})
	c.Clear()

	if c.Size() != 0 {
		t.Errorf("Size after clear = %d, want 0", c.Size())
	}
}

func TestCache_Size(t *testing.T) {
	c := NewCache(10)
	if c.Size() != 0 {
		t.Errorf("Initial size = %d, want 0", c.Size())
	}
	c.Put(&Statement{Name: "s1", SQL: "SQL1"})
	if c.Size() != 1 {
		t.Errorf("Size after put = %d, want 1", c.Size())
	}
}

func TestConnTracker(t *testing.T) {
	tr := NewConnTracker(42)

	if tr.IsPrepared("s1") {
		t.Error("Should not be prepared initially")
	}

	tr.MarkPrepared("s1")
	if !tr.IsPrepared("s1") {
		t.Error("Should be prepared after marking")
	}

	tr.UnmarkPrepared("s1")
	if tr.IsPrepared("s1") {
		t.Error("Should not be prepared after unmarking")
	}
}

func TestConnTracker_ListPrepared(t *testing.T) {
	tr := NewConnTracker(1)
	tr.MarkPrepared("a")
	tr.MarkPrepared("b")

	names := tr.ListPrepared()
	if len(names) != 2 {
		t.Fatalf("ListPrepared returned %d items, want 2", len(names))
	}
	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}
	if !found["a"] || !found["b"] {
		t.Errorf("ListPrepared = %v, want [a, b]", names)
	}
}

func TestConnTracker_Clear(t *testing.T) {
	tr := NewConnTracker(1)
	tr.MarkPrepared("s1")
	tr.Clear()
	if tr.IsPrepared("s1") {
		t.Error("Should be cleared")
	}
}

func TestManager(t *testing.T) {
	m := NewManager(10)

	cache := m.GetCache()
	if cache == nil {
		t.Fatal("GetCache returned nil")
	}

	tr := m.GetOrCreateTracker(1)
	if tr == nil {
		t.Fatal("GetOrCreateTracker returned nil")
	}

	// Same tracker returned on second call
	tr2 := m.GetOrCreateTracker(1)
	if tr != tr2 {
		t.Error("Should return same tracker for same connID")
	}
}

func TestManager_GenerateName(t *testing.T) {
	m := NewManager(10)
	n1 := m.GenerateName("stmt")
	n2 := m.GenerateName("stmt")

	if n1 == n2 {
		t.Errorf("Generated names should be unique: %s == %s", n1, n2)
	}
}

func TestManager_IsPreparedOnConn(t *testing.T) {
	m := NewManager(10)

	if m.IsPreparedOnConn(1, "s1") {
		t.Error("Should not be prepared initially")
	}

	m.MarkPreparedOnConn(1, "s1")
	if !m.IsPreparedOnConn(1, "s1") {
		t.Error("Should be prepared after marking")
	}

	m.RemoveTracker(1)
}

func TestRemapper(t *testing.T) {
	r := NewRemapper()

	r.Map("client_stmt", 42)

	id, ok := r.GetServerID("client_stmt")
	if !ok || id != 42 {
		t.Errorf("GetServerID = (%d, %v), want (42, true)", id, ok)
	}

	name, ok := r.GetClientName(42)
	if !ok || name != "client_stmt" {
		t.Errorf("GetClientName = (%q, %v), want (%q, true)", name, ok, "client_stmt")
	}
}

func TestRemapper_Remove(t *testing.T) {
	r := NewRemapper()
	r.Map("client_stmt", 42)
	r.Remove("client_stmt")

	if _, ok := r.GetServerID("client_stmt"); ok {
		t.Error("Should be removed")
	}
	if _, ok := r.GetClientName(42); ok {
		t.Error("Reverse mapping should be removed too")
	}
}

func TestRemapper_Clear(t *testing.T) {
	r := NewRemapper()
	r.Map("s1", 1)
	r.Map("s2", 2)
	r.Clear()

	if _, ok := r.GetServerID("s1"); ok {
		t.Error("Should be cleared")
	}
}

func TestTransparentRepreparer(t *testing.T) {
	m := NewManager(10)
	m.GetCache().Put(&Statement{Name: "s1", SQL: "SELECT 1"})

	r := NewTransparentRepreparer(m)

	// First call: needs preparation
	stmt, needed, err := r.PrepareIfNeeded(1, "s1")
	if err != nil {
		t.Fatalf("PrepareIfNeeded failed: %v", err)
	}
	if !needed {
		t.Error("Should need preparation on first call")
	}
	if stmt == nil {
		t.Fatal("Statement should be returned")
	}

	// Second call: already prepared
	_, needed, err = r.PrepareIfNeeded(1, "s1")
	if err != nil {
		t.Fatalf("PrepareIfNeeded failed on second call: %v", err)
	}
	if needed {
		t.Error("Should not need preparation on second call")
	}
}

func TestTransparentRepreparer_UnknownStmt(t *testing.T) {
	m := NewManager(10)
	r := NewTransparentRepreparer(m)

	_, _, err := r.PrepareIfNeeded(1, "unknown")
	if err == nil {
		t.Error("Should error for unknown statement")
	}
}

func TestTransparentRepreparer_ExecuteOnAny(t *testing.T) {
	m := NewManager(10)
	m.GetCache().Put(&Statement{Name: "s1", SQL: "SELECT 1"})
	r := NewTransparentRepreparer(m)

	executed := false
	err := r.ExecuteOnAny(1, "s1", func() error {
		executed = true
		return nil
	})
	if err != nil {
		t.Fatalf("ExecuteOnAny failed: %v", err)
	}
	if !executed {
		t.Error("executeFn should have been called")
	}
}

// --- GetMissingStatements (0% coverage) ---

func TestManager_GetMissingStatements(t *testing.T) {
	m := NewManager(10)
	m.MarkPreparedOnConn(1, "s1")
	m.MarkPreparedOnConn(1, "s3")

	missing := m.GetMissingStatements(1, []string{"s1", "s2", "s3", "s4"})
	if len(missing) != 2 {
		t.Fatalf("GetMissingStatements = %v, want 2 missing", missing)
	}
	found := make(map[string]bool)
	for _, name := range missing {
		found[name] = true
	}
	if !found["s2"] || !found["s4"] {
		t.Errorf("Missing should be [s2, s4], got %v", missing)
	}
}

func TestManager_GetMissingStatements_AllPrepared(t *testing.T) {
	m := NewManager(10)
	m.MarkPreparedOnConn(1, "s1")
	m.MarkPreparedOnConn(1, "s2")

	missing := m.GetMissingStatements(1, []string{"s1", "s2"})
	if len(missing) != 0 {
		t.Errorf("GetMissingStatements = %v, want empty", missing)
	}
}

func TestManager_GetMissingStatements_EmptyList(t *testing.T) {
	m := NewManager(10)
	missing := m.GetMissingStatements(1, []string{})
	if len(missing) != 0 {
		t.Errorf("GetMissingStatements = %v, want empty for empty input", missing)
	}
}

// --- ExecuteOnAny error path ---

func TestTransparentRepreparer_ExecuteOnAny_Error(t *testing.T) {
	m := NewManager(10)
	r := NewTransparentRepreparer(m)

	err := r.ExecuteOnAny(1, "nonexistent", func() error {
		t.Error("executeFn should not be called on error")
		return nil
	})
	if err == nil {
		t.Error("Should return error for nonexistent statement")
	}
}

func TestTransparentRepreparer_ExecuteOnAny_AlreadyPrepared(t *testing.T) {
	m := NewManager(10)
	m.GetCache().Put(&Statement{Name: "s1", SQL: "SELECT 1"})
	r := NewTransparentRepreparer(m)

	// First call prepares
	r.ExecuteOnAny(1, "s1", func() error { return nil })

	// Second call - already prepared, needed=false path
	executed := false
	err := r.ExecuteOnAny(1, "s1", func() error {
		executed = true
		return nil
	})
	if err != nil {
		t.Fatalf("ExecuteOnAny (already prepared) failed: %v", err)
	}
	if !executed {
		t.Error("executeFn should still be called even when already prepared")
	}
}

// --- evictLRU with already-removed statement ---

func TestCache_EvictLRU_AlreadyRemoved(t *testing.T) {
	c := NewCache(3)

	c.Put(&Statement{Name: "s1", SQL: "SQL1"})
	c.Put(&Statement{Name: "s2", SQL: "SQL2"})
	c.Put(&Statement{Name: "s3", SQL: "SQL3"})

	// Remove s1 via Remove (doesn't remove from lruList)
	c.Remove("s1")

	// Now evict by adding s4 - this should trigger evictLRU which finds
	// s1 in lruList but not in statements map
	c.Put(&Statement{Name: "s4", SQL: "SQL4"})

	// s2 should still be there (s1 was already removed from statements)
	if c.Get("s2") == nil {
		t.Error("s2 should still exist")
	}
}

func TestCache_GetBySQL_NotFound(t *testing.T) {
	c := NewCache(10)
	if c.GetBySQL("nonexistent") != nil {
		t.Error("GetBySQL should return nil for nonexistent SQL")
	}
}

func TestRemapper_GetServerID_NotFound(t *testing.T) {
	r := NewRemapper()
	if _, ok := r.GetServerID("nonexistent"); ok {
		t.Error("Should not find nonexistent client name")
	}
}

func TestRemapper_GetClientName_NotFound(t *testing.T) {
	r := NewRemapper()
	if _, ok := r.GetClientName(999); ok {
		t.Error("Should not find nonexistent server ID")
	}
}

func TestRemapper_Remove_NotFound(t *testing.T) {
	r := NewRemapper()
	r.Remove("nonexistent") // Should not panic
}

func TestCache_Remove_NotFound(t *testing.T) {
	c := NewCache(10)
	c.Remove("nonexistent") // Should not panic
}
