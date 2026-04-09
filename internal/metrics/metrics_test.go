package metrics

import (
	"testing"
	"time"
)

func TestCounter(t *testing.T) {
	c := NewCounter("test_counter")

	if c.Name() != "test_counter" {
		t.Errorf("Name = %q, want test_counter", c.Name())
	}
	if c.Type() != TypeCounter {
		t.Errorf("Type = %v, want TypeCounter", c.Type())
	}
	if c.Value() != 0 {
		t.Errorf("Initial value = %v, want 0", c.Value())
	}

	c.Inc()
	if c.Value() != 1 {
		t.Errorf("After Inc = %v, want 1", c.Value())
	}

	c.Add(5)
	if c.Value() != 6 {
		t.Errorf("After Add(5) = %v, want 6", c.Value())
	}

	c.Reset()
	if c.Value() != 0 {
		t.Errorf("After Reset = %v, want 0", c.Value())
	}
}

func TestGauge(t *testing.T) {
	g := NewGauge("test_gauge")

	g.Set(42.5)
	if g.Value() != 42500000 {
		t.Errorf("After Set(42.5) = %v, want 42500000", g.Value())
	}

	g.Inc()
	g.Dec()
	// Value should be back to 42500000
	if g.Value() != 42500000 {
		t.Errorf("After Inc+Dec = %v, want 42500000", g.Value())
	}

	g.Reset()
	if g.Value() != 0 {
		t.Errorf("After Reset = %v, want 0", g.Value())
	}
}

func TestHistogram(t *testing.T) {
	buckets := []float64{0.01, 0.05, 0.1, 0.5, 1.0}
	h := NewHistogram("test_hist", buckets)

	h.Observe(0.03) // Should go in 0.05 bucket
	h.Observe(0.07) // Should go in 0.1 bucket
	h.Observe(2.0)  // Beyond all buckets

	if h.Count() != 3 {
		t.Errorf("Count = %d, want 3", h.Count())
	}

	bVals, bCounts := h.Buckets()
	if len(bVals) != 5 {
		t.Errorf("Bucket count = %d, want 5", len(bVals))
	}
	// 0.03 <= 0.05, so first bucket (0.01) gets 0, second (0.05) gets 1
	if bCounts[0] != 0 {
		t.Errorf("Bucket[0] count = %d, want 0", bCounts[0])
	}
	if bCounts[1] != 1 {
		t.Errorf("Bucket[1] count = %d, want 1", bCounts[1])
	}

	h.Reset()
	if h.Count() != 0 {
		t.Errorf("After Reset count = %d, want 0", h.Count())
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()

	c := r.RegisterCounter("c1")
	if c == nil {
		t.Fatal("RegisterCounter returned nil")
	}
	// Second call returns same counter
	c2 := r.RegisterCounter("c1")
	if c != c2 {
		t.Error("Should return same counter on duplicate register")
	}

	g := r.RegisterGauge("g1")
	if g == nil {
		t.Fatal("RegisterGauge returned nil")
	}

	h := r.RegisterHistogram("h1", []float64{0.1, 0.5})
	if h == nil {
		t.Fatal("RegisterHistogram returned nil")
	}

	// Get methods
	if r.GetCounter("c1") != c {
		t.Error("GetCounter mismatch")
	}
	if r.GetGauge("g1") != g {
		t.Error("GetGauge mismatch")
	}
	if r.GetHistogram("h1") != h {
		t.Error("GetHistogram mismatch")
	}
	if r.Get("c1") != c {
		t.Error("Get mismatch")
	}

	// List
	names := r.List()
	if len(names) != 3 {
		t.Errorf("List returned %d names, want 3", len(names))
	}

	// Snapshot
	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Errorf("Snapshot has %d entries, want 3", len(snap))
	}
}

func TestRegistry_JSON(t *testing.T) {
	r := NewRegistry()
	r.RegisterCounter("c1").Inc()

	data, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON failed: %v", err)
	}
	if len(data) == 0 {
		t.Error("JSON returned empty data")
	}
}

func TestPoolMetrics(t *testing.T) {
	r := NewRegistry()
	pm := NewPoolMetrics(r, "testpool")

	pm.RecordQuery(50 * time.Millisecond)
	if pm.QueriesTotal.Value() != 1 {
		t.Errorf("QueriesTotal = %v, want 1", pm.QueriesTotal.Value())
	}

	pm.RecordTransaction()
	if pm.TransactionsTotal.Value() != 1 {
		t.Errorf("TransactionsTotal = %v, want 1", pm.TransactionsTotal.Value())
	}

	pm.RecordError()
	if pm.ErrorsTotal.Value() != 1 {
		t.Errorf("ErrorsTotal = %v, want 1", pm.ErrorsTotal.Value())
	}

	pm.UpdateConnections(10, 2, 5, 3)
}

func TestGlobalMetrics(t *testing.T) {
	r := NewRegistry()
	gm := NewGlobalMetrics(r)

	if gm.Uptime() < 0 {
		t.Error("Uptime should be positive")
	}

	// Let updateLoop run once
	time.Sleep(100 * time.Millisecond)
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		v    float64
		want string
	}{
		{0, "0"},
		{100, "100"},
		{1.234, "1.234"},
		{0.001, "0.001"},
	}
	for _, tc := range cases {
		got := FormatValue(tc.v)
		if got != tc.want {
			t.Errorf("FormatValue(%v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

func TestDefaultRegistry(t *testing.T) {
	if DefaultRegistry == nil {
		t.Error("DefaultRegistry should not be nil")
	}
	if len(DefaultDurationBuckets) == 0 {
		t.Error("DefaultDurationBuckets should not be empty")
	}
}
