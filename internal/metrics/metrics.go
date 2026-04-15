package metrics

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// MetricType represents the type of metric.
type MetricType int

const (
	TypeCounter MetricType = iota
	TypeGauge
	TypeHistogram
)

// Metric is the interface for all metrics.
type Metric interface {
	Name() string
	Type() MetricType
	Value() float64
	Reset()
}

// Counter is a monotonically increasing counter.
type Counter struct {
	name  string
	value atomic.Uint64
}

// NewCounter creates a new counter.
func NewCounter(name string) *Counter {
	return &Counter{name: name}
}

// Name returns the metric name.
func (c *Counter) Name() string {
	return c.name
}

// Type returns the metric type.
func (c *Counter) Type() MetricType {
	return TypeCounter
}

// Value returns the current value.
func (c *Counter) Value() float64 {
	return float64(c.value.Load())
}

// Inc increments the counter by 1.
func (c *Counter) Inc() {
	c.value.Add(1)
}

// Add adds a value to the counter.
func (c *Counter) Add(n uint64) {
	c.value.Add(n)
}

// Reset resets the counter to 0.
func (c *Counter) Reset() {
	c.value.Store(0)
}

// Gauge is a value that can go up or down.
type Gauge struct {
	name  string
	value atomic.Int64
}

// NewGauge creates a new gauge.
func NewGauge(name string) *Gauge {
	return &Gauge{name: name}
}

// Name returns the metric name.
func (g *Gauge) Name() string {
	return g.name
}

// Type returns the metric type.
func (g *Gauge) Type() MetricType {
	return TypeGauge
}

// Value returns the current value.
func (g *Gauge) Value() float64 {
	return float64(g.value.Load())
}

// Set sets the gauge value.
func (g *Gauge) Set(v float64) {
	g.value.Store(int64(v * 1000000)) // Store as fixed-point
}

// Inc increments the gauge by 1.
func (g *Gauge) Inc() {
	g.value.Add(1)
}

// Dec decrements the gauge by 1.
func (g *Gauge) Dec() {
	g.value.Add(-1)
}

// Add adds a value to the gauge.
func (g *Gauge) Add(n int64) {
	g.value.Add(n)
}

// Reset resets the gauge to 0.
func (g *Gauge) Reset() {
	g.value.Store(0)
}

// Histogram tracks the distribution of values.
type Histogram struct {
	name    string
	buckets []float64
	counts  []atomic.Uint64
	sum     float64    // Actual sum, protected by mu
	mu      sync.Mutex // Protects sum field
	count   atomic.Uint64
}

// NewHistogram creates a new histogram with the given buckets.
func NewHistogram(name string, buckets []float64) *Histogram {
	sortedBuckets := make([]float64, len(buckets))
	copy(sortedBuckets, buckets)
	sort.Float64s(sortedBuckets)

	return &Histogram{
		name:    name,
		buckets: sortedBuckets,
		counts:  make([]atomic.Uint64, len(sortedBuckets)),
	}
}

// Name returns the metric name.
func (h *Histogram) Name() string {
	return h.name
}

// Type returns the metric type.
func (h *Histogram) Type() MetricType {
	return TypeHistogram
}

// Value returns the count (for Value interface compatibility).
func (h *Histogram) Value() float64 {
	return float64(h.count.Load())
}

// Observe adds a value to the histogram.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	h.sum += v
	h.mu.Unlock()
	h.count.Add(1)

	// Find the bucket
	for i, bucket := range h.buckets {
		if v <= bucket {
			h.counts[i].Add(1)
			return
		}
	}
}

// Reset resets the histogram.
func (h *Histogram) Reset() {
	h.mu.Lock()
	h.sum = 0
	h.mu.Unlock()
	h.count.Store(0)
	for i := range h.counts {
		h.counts[i].Store(0)
	}
}

// Buckets returns bucket values and counts.
func (h *Histogram) Buckets() ([]float64, []uint64) {
	counts := make([]uint64, len(h.counts))
	for i := range h.counts {
		counts[i] = h.counts[i].Load()
	}
	return h.buckets, counts
}

// Sum returns the sum of all observed values.
func (h *Histogram) Sum() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sum
}

// Count returns the total count of observations.
func (h *Histogram) Count() uint64 {
	return h.count.Load()
}

// Registry holds all metrics.
type Registry struct {
	mu         sync.RWMutex
	metrics    map[string]Metric
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram
}

// NewRegistry creates a new metrics registry.
func NewRegistry() *Registry {
	return &Registry{
		metrics:    make(map[string]Metric),
		counters:   make(map[string]*Counter),
		gauges:     make(map[string]*Gauge),
		histograms: make(map[string]*Histogram),
	}
}

// RegisterCounter registers a counter.
func (r *Registry) RegisterCounter(name string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.counters[name]; ok {
		return existing
	}

	c := NewCounter(name)
	r.counters[name] = c
	r.metrics[name] = c
	return c
}

// RegisterGauge registers a gauge.
func (r *Registry) RegisterGauge(name string) *Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.gauges[name]; ok {
		return existing
	}

	g := NewGauge(name)
	r.gauges[name] = g
	r.metrics[name] = g
	return g
}

// RegisterHistogram registers a histogram.
func (r *Registry) RegisterHistogram(name string, buckets []float64) *Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.histograms[name]; ok {
		return existing
	}

	h := NewHistogram(name, buckets)
	r.histograms[name] = h
	r.metrics[name] = h
	return h
}

// GetCounter returns a counter by name.
func (r *Registry) GetCounter(name string) *Counter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.counters[name]
}

// GetGauge returns a gauge by name.
func (r *Registry) GetGauge(name string) *Gauge {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.gauges[name]
}

// GetHistogram returns a histogram by name.
func (r *Registry) GetHistogram(name string) *Histogram {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.histograms[name]
}

// Get returns a metric by name.
func (r *Registry) Get(name string) Metric {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.metrics[name]
}

// List returns all metric names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.metrics))
	for name := range r.metrics {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Snapshot returns a snapshot of all metrics.
func (r *Registry) Snapshot() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snapshot := make(map[string]interface{})

	for name, m := range r.metrics {
		switch v := m.(type) {
		case *Counter:
			snapshot[name] = map[string]interface{}{
				"type":  "counter",
				"value": v.Value(),
			}
		case *Gauge:
			snapshot[name] = map[string]interface{}{
				"type":  "gauge",
				"value": v.Value(),
			}
		case *Histogram:
			buckets, counts := v.Buckets()
			snapshot[name] = map[string]interface{}{
				"type":    "histogram",
				"count":   v.Count(),
				"sum":     v.Sum(),
				"buckets": buckets,
				"counts":  counts,
			}
		}
	}

	return snapshot
}

// JSON returns metrics as JSON.
func (r *Registry) JSON() ([]byte, error) {
	return json.Marshal(r.Snapshot())
}

// DefaultRegistry is the global default registry.
var DefaultRegistry = NewRegistry()

// Default buckets for histograms.
var DefaultDurationBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// PoolMetrics contains metrics for a connection pool.
type PoolMetrics struct {
	ClientConnectionsActive  *Gauge
	ClientConnectionsWaiting *Gauge
	ServerConnectionsActive  *Gauge
	ServerConnectionsIdle    *Gauge
	QueriesTotal             *Counter
	QueriesDuration          *Histogram
	TransactionsTotal        *Counter
	ErrorsTotal              *Counter
}

// NewPoolMetrics creates pool metrics.
func NewPoolMetrics(registry *Registry, poolName string) *PoolMetrics {
	prefix := "geryon_pool_" + poolName + "_"

	return &PoolMetrics{
		ClientConnectionsActive:  registry.RegisterGauge(prefix + "client_connections_active"),
		ClientConnectionsWaiting: registry.RegisterGauge(prefix + "client_connections_waiting"),
		ServerConnectionsActive:  registry.RegisterGauge(prefix + "server_connections_active"),
		ServerConnectionsIdle:    registry.RegisterGauge(prefix + "server_connections_idle"),
		QueriesTotal:             registry.RegisterCounter(prefix + "queries_total"),
		QueriesDuration:          registry.RegisterHistogram(prefix+"query_duration_seconds", DefaultDurationBuckets),
		TransactionsTotal:        registry.RegisterCounter(prefix + "transactions_total"),
		ErrorsTotal:              registry.RegisterCounter(prefix + "errors_total"),
	}
}

// GlobalMetrics contains global application metrics.
type GlobalMetrics struct {
	GoGoroutines *Gauge
	GoMemAlloc   *Gauge
	GoMemSys     *Gauge
	StartTime    time.Time
}

// NewGlobalMetrics creates global metrics.
func NewGlobalMetrics(registry *Registry) *GlobalMetrics {
	m := &GlobalMetrics{
		GoGoroutines: registry.RegisterGauge("geryon_go_goroutines"),
		GoMemAlloc:   registry.RegisterGauge("geryon_go_memory_alloc_bytes"),
		GoMemSys:     registry.RegisterGauge("geryon_go_memory_sys_bytes"),
		StartTime:    time.Now(),
	}

	// Start a goroutine to update Go runtime metrics
	go m.updateLoop()

	return m
}

// updateLoop periodically updates Go runtime metrics.
func (m *GlobalMetrics) updateLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)

		m.GoGoroutines.Set(float64(runtime.NumGoroutine()))
		m.GoMemAlloc.Set(float64(stats.Alloc))
		m.GoMemSys.Set(float64(stats.Sys))
	}
}

// Uptime returns the application uptime.
func (m *GlobalMetrics) Uptime() time.Duration {
	return time.Since(m.StartTime)
}

// RecordQuery records query metrics.
func (pm *PoolMetrics) RecordQuery(duration time.Duration) {
	pm.QueriesTotal.Inc()
	pm.QueriesDuration.Observe(duration.Seconds())
}

// RecordTransaction records transaction metrics.
func (pm *PoolMetrics) RecordTransaction() {
	pm.TransactionsTotal.Inc()
}

// RecordError records an error.
func (pm *PoolMetrics) RecordError() {
	pm.ErrorsTotal.Inc()
}

// UpdateConnections updates connection gauges.
func (pm *PoolMetrics) UpdateConnections(active, waiting, serverActive, serverIdle int64) {
	pm.ClientConnectionsActive.Set(float64(active))
	pm.ClientConnectionsWaiting.Set(float64(waiting))
	pm.ServerConnectionsActive.Set(float64(serverActive))
	pm.ServerConnectionsIdle.Set(float64(serverIdle))
}

// String returns a formatted value string.
func FormatValue(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.3f", v)
}
