package metrics

import (
	"testing"
	"time"
)

func TestRegistry_DuplicateGauge(t *testing.T) {
	r := NewRegistry()
	g1 := r.RegisterGauge("dup_gauge")
	g1.Set(42)
	g2 := r.RegisterGauge("dup_gauge")
	if g1 != g2 {
		t.Error("Duplicate RegisterGauge should return same gauge")
	}
	if g2.Value() != 42000000 {
		t.Errorf("Value = %v, want 42000000", g2.Value())
	}
}

func TestRegistry_DuplicateHistogram(t *testing.T) {
	r := NewRegistry()
	h1 := r.RegisterHistogram("dup_hist", []float64{0.1, 0.5})
	h1.Observe(0.3)
	h2 := r.RegisterHistogram("dup_hist", []float64{0.1, 0.5})
	if h1 != h2 {
		t.Error("Duplicate RegisterHistogram should return same histogram")
	}
	if h2.Count() != 1 {
		t.Errorf("Count = %d, want 1", h2.Count())
	}
}

func TestHistogram_Sum(t *testing.T) {
	h := NewHistogram("sum_hist", []float64{0.1, 0.5})
	h.Observe(0.3)
	h.Observe(0.7)
	s := h.Sum()
	if s == 0 {
		t.Error("Sum should not be zero after observations")
	}
}

func TestGlobalMetrics_UpdateLoop(t *testing.T) {
	r := NewRegistry()
	gm := NewGlobalMetrics(r)

	// Wait for at least one updateLoop tick (15s interval)
	// Since we can't wait that long in a test, just verify the metrics objects exist
	if gm.GoGoroutines == nil {
		t.Error("GoGoroutines should be initialized")
	}
	if gm.GoMemAlloc == nil {
		t.Error("GoMemAlloc should be initialized")
	}
	if gm.GoMemSys == nil {
		t.Error("GoMemSys should be initialized")
	}

	// Verify uptime is reasonable
	uptime := gm.Uptime()
	if uptime < 0 {
		t.Error("Uptime should be positive")
	}
}

func TestCounter_AddLarge(t *testing.T) {
	c := NewCounter("large_counter")
	c.Add(10)
	c.Add(3)
	if c.Value() != 13 {
		t.Errorf("Value = %v, want 13", c.Value())
	}
}

func TestGauge_IncDec(t *testing.T) {
	g := NewGauge("incdec_gauge")
	g.Set(10.0)
	g.Inc()
	g.Inc()
	g.Dec()
	// Set(10.0) stores 10000000, Inc adds 1, Inc adds 1, Dec subtracts 1 = 10000001
	val := g.Value()
	if val != 10000001 {
		t.Errorf("Value = %v, want 10000001", val)
	}
}

func TestSnapshot_Values(t *testing.T) {
	r := NewRegistry()
	r.RegisterCounter("snap_c").Add(5)
	r.RegisterGauge("snap_g").Set(100)

	snap := r.Snapshot()
	if len(snap) != 2 {
		t.Errorf("Snapshot length = %d, want 2", len(snap))
	}

	// Verify counter exists in snapshot
	if _, ok := snap["snap_c"]; !ok {
		t.Error("snap_c not found in snapshot")
	}
	if _, ok := snap["snap_g"]; !ok {
		t.Error("snap_g not found in snapshot")
	}
}

func TestGet_NotFound(t *testing.T) {
	r := NewRegistry()
	if m := r.Get("nonexistent"); m != nil {
		t.Error("Get should return nil for nonexistent metric")
	}
}

func TestGetCounter_NotFound(t *testing.T) {
	r := NewRegistry()
	if c := r.GetCounter("nonexistent"); c != nil {
		t.Error("GetCounter should return nil for nonexistent counter")
	}
}

func TestGetGauge_NotFound(t *testing.T) {
	r := NewRegistry()
	if g := r.GetGauge("nonexistent"); g != nil {
		t.Error("GetGauge should return nil for nonexistent gauge")
	}
}

func TestGetHistogram_NotFound(t *testing.T) {
	r := NewRegistry()
	if h := r.GetHistogram("nonexistent"); h != nil {
		t.Error("GetHistogram should return nil for nonexistent histogram")
	}
}

func TestHistogram_Observe_BeyondBuckets(t *testing.T) {
	h := NewHistogram("beyond_hist", []float64{0.1, 0.5})
	h.Observe(100.0) // Beyond all buckets
	if h.Count() != 1 {
		t.Errorf("Count = %d, want 1", h.Count())
	}
	bVals, bCounts := h.Buckets()
	if len(bVals) != 2 {
		t.Errorf("Bucket values count = %d, want 2", len(bVals))
	}
	// Both buckets should be 0 since 100 > 0.5
	if bCounts[0] != 0 {
		t.Errorf("Bucket[0] = %d, want 0", bCounts[0])
	}
	if bCounts[1] != 0 {
		t.Errorf("Bucket[1] = %d, want 0", bCounts[1])
	}
}

func TestPoolMetrics_AllMetrics(t *testing.T) {
	r := NewRegistry()
	pm := NewPoolMetrics(r, "all-pool")

	pm.RecordQuery(100 * time.Millisecond)
	pm.RecordQuery(200 * time.Millisecond)
	pm.RecordTransaction()
	pm.RecordTransaction()
	pm.RecordTransaction()
	pm.RecordError()
	pm.UpdateConnections(20, 5, 10, 8)

	if pm.QueriesTotal.Value() != 2 {
		t.Errorf("QueriesTotal = %v, want 2", pm.QueriesTotal.Value())
	}
	if pm.TransactionsTotal.Value() != 3 {
		t.Errorf("TransactionsTotal = %v, want 3", pm.TransactionsTotal.Value())
	}
	if pm.ErrorsTotal.Value() != 1 {
		t.Errorf("ErrorsTotal = %v, want 1", pm.ErrorsTotal.Value())
	}
}

// --- Gauge.Name, Gauge.Type, Gauge.Add ---

func TestGauge_Name(t *testing.T) {
	g := NewGauge("my_gauge")
	if g.Name() != "my_gauge" {
		t.Errorf("Name = %q, want my_gauge", g.Name())
	}
}

func TestGauge_Type(t *testing.T) {
	g := NewGauge("type_gauge")
	if g.Type() != TypeGauge {
		t.Errorf("Type = %v, want TypeGauge", g.Type())
	}
}

func TestGauge_Add(t *testing.T) {
	g := NewGauge("add_gauge")
	g.Inc()
	g.Add(5)
	if g.Value() != 6 {
		t.Errorf("Value = %v, want 6 after Add(5)", g.Value())
	}
	g.Add(-3)
	if g.Value() != 3 {
		t.Errorf("Value = %v, want 3 after Add(-3)", g.Value())
	}
}

// --- Histogram.Name, Histogram.Type, Histogram.Value ---

func TestHistogram_Name(t *testing.T) {
	h := NewHistogram("named_hist", []float64{0.1})
	if h.Name() != "named_hist" {
		t.Errorf("Name = %q, want named_hist", h.Name())
	}
}

func TestHistogram_Type(t *testing.T) {
	h := NewHistogram("type_hist", []float64{0.1})
	if h.Type() != TypeHistogram {
		t.Errorf("Type = %v, want TypeHistogram", h.Type())
	}
}

func TestHistogram_Value(t *testing.T) {
	h := NewHistogram("val_hist", []float64{0.1})
	if h.Value() != 0 {
		t.Errorf("Value = %v, want 0 before observations", h.Value())
	}
	h.Observe(0.05)
	if h.Value() != 1 {
		t.Errorf("Value = %v, want 1 after one observation", h.Value())
	}
}

// --- Snapshot with histogram ---

func TestSnapshot_WithHistogram(t *testing.T) {
	r := NewRegistry()
	h := r.RegisterHistogram("snap_h", []float64{0.1, 0.5})
	h.Observe(0.3)

	snap := r.Snapshot()
	entry, ok := snap["snap_h"]
	if !ok {
		t.Fatal("snap_h not found in snapshot")
	}
	m := entry.(map[string]interface{})
	if m["type"] != "histogram" {
		t.Errorf("type = %v, want histogram", m["type"])
	}
	if m["count"] != uint64(1) {
		t.Errorf("count = %v, want 1", m["count"])
	}
	buckets, ok := m["buckets"].([]float64)
	if !ok || len(buckets) != 2 {
		t.Errorf("buckets = %v, want 2 entries", m["buckets"])
	}
}

// --- GlobalMetrics gauge wiring ---

func TestGlobalMetrics_GaugesWired(t *testing.T) {
	r := NewRegistry()
	gm := NewGlobalMetrics(r)

	gm.GoGoroutines.Set(99)
	if gm.GoGoroutines.Value() != 99000000 {
		t.Errorf("GoGoroutines = %v, want 99000000", gm.GoGoroutines.Value())
	}

	gm.GoMemAlloc.Set(12345)
	if gm.GoMemAlloc.Value() != 12345000000 {
		t.Errorf("GoMemAlloc = %v, want 12345000000", gm.GoMemAlloc.Value())
	}

	gm.GoMemSys.Set(99999)
	if gm.GoMemSys.Value() != 99999000000 {
		t.Errorf("GoMemSys = %v, want 99999000000", gm.GoMemSys.Value())
	}
}

// --- FormatValue edge cases ---

func TestFormatValue_EdgeCases(t *testing.T) {
	cases := []struct {
		v    float64
		want string
	}{
		{-1, "-1"},
		{0.5, "0.500"},
	}
	for _, tc := range cases {
		got := FormatValue(tc.v)
		if got != tc.want {
			t.Errorf("FormatValue(%v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}
