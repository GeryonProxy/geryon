package tracing

import (
	"context"
	"strings"
	"testing"

	"github.com/GeryonProxy/geryon/internal/logger"
)

func newTestLogger(t *testing.T) *logger.Logger {
	t.Helper()
	l, err := logger.New("debug", "text")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	return l
}

func TestNewTracer(t *testing.T) {
	cfg := &Config{Enabled: false}
	log := newTestLogger(t)
	tr := NewTracer(cfg, log)
	if tr == nil {
		t.Fatal("NewTracer returned nil")
	}
	if tr.inited.Load() {
		t.Error("tracer should not be initialized after creation")
	}
}

func TestTracer_Init_Disabled(t *testing.T) {
	cfg := &Config{Enabled: false}
	log := newTestLogger(t)
	tr := NewTracer(cfg, log)

	if err := tr.Init(context.Background()); err != nil {
		t.Fatalf("Init with disabled tracing should not error: %v", err)
	}
	if tr.inited.Load() {
		t.Error("tracer should not be marked initialized when disabled")
	}
}

func TestTracer_Init_Stdout(t *testing.T) {
	cfg := &Config{
		Enabled:      true,
		Exporter:     "stdout",
		Endpoint:     "localhost:4317",
		SamplingRate: 1.0,
		ServiceName:  "test-geryon",
	}
	log := newTestLogger(t)
	tr := NewTracer(cfg, log)

	if err := tr.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !tr.inited.Load() {
		t.Error("tracer should be initialized")
	}
}

func TestTracer_Init_UnknownExporter(t *testing.T) {
	cfg := &Config{
		Enabled:      true,
		Exporter:     "unknown",
		Endpoint:     "localhost:4317",
		SamplingRate: 1.0,
		ServiceName:  "test",
	}
	log := newTestLogger(t)
	tr := NewTracer(cfg, log)

	if err := tr.Init(context.Background()); err != nil {
		t.Fatalf("Unknown exporter should not error: %v", err)
	}
	if tr.inited.Load() {
		t.Error("tracer should not be initialized for unknown exporter")
	}
}

func TestTracer_Init_Idempotent(t *testing.T) {
	cfg := &Config{
		Enabled:      true,
		Exporter:     "stdout",
		Endpoint:     "localhost:4317",
		SamplingRate: 1.0,
		ServiceName:  "test-idempotent",
	}
	log := newTestLogger(t)
	tr := NewTracer(cfg, log)

	if err := tr.Init(context.Background()); err != nil {
		t.Fatalf("first Init failed: %v", err)
	}
	if err := tr.Init(context.Background()); err != nil {
		t.Fatalf("second Init failed: %v", err)
	}
}

func TestTracer_StartSpan_Disabled(t *testing.T) {
	cfg := &Config{Enabled: false}
	log := newTestLogger(t)
	tr := NewTracer(cfg, log)

	ctx, end := tr.StartSpan(context.Background(), "test-span")
	defer end()

	if ctx == nil {
		t.Error("context should not be nil")
	}
}

func TestTracer_StartSpan_NilTracer(t *testing.T) {
	var tr *Tracer
	ctx, end := tr.StartSpan(context.Background(), "test-span")
	defer end()

	if ctx == nil {
		t.Error("context should not be nil")
	}
}

func TestTracer_StartSpan_Enabled(t *testing.T) {
	cfg := &Config{
		Enabled:      true,
		Exporter:     "stdout",
		Endpoint:     "localhost:4317",
		SamplingRate: 1.0,
		ServiceName:  "test-span",
	}
	log := newTestLogger(t)
	tr := NewTracer(cfg, log)

	if err := tr.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ctx, end := tr.StartSpan(context.Background(), "test-operation")
	defer end()

	if ctx == nil {
		t.Error("context should not be nil")
	}
}

func TestTracer_StartSpan_ZeroSamplingRate(t *testing.T) {
	cfg := &Config{
		Enabled:      true,
		Exporter:     "stdout",
		Endpoint:     "localhost:4317",
		SamplingRate: 0,
		ServiceName:  "test-zero-sample",
	}
	log := newTestLogger(t)
	tr := NewTracer(cfg, log)

	if err := tr.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ctx, end := tr.StartSpan(context.Background(), "test-op")
	defer end()

	span := SpanFromContext(ctx)
	if span == nil {
		t.Error("span should not be nil")
	}
}

func TestTracer_Shutdown(t *testing.T) {
	cfg := &Config{
		Enabled:      true,
		Exporter:     "stdout",
		Endpoint:     "localhost:4317",
		SamplingRate: 1.0,
		ServiceName:  "test-shutdown",
	}
	log := newTestLogger(t)
	tr := NewTracer(cfg, log)

	if err := tr.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}
}

func TestTracer_Shutdown_NoInit(t *testing.T) {
	cfg := &Config{Enabled: false}
	log := newTestLogger(t)
	tr := NewTracer(cfg, log)

	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown without init should not error: %v", err)
	}
}

func TestGenerateCorrelationID(t *testing.T) {
	id := GenerateCorrelationID()
	if id == "" {
		t.Error("correlation ID should not be empty")
	}

	parts := strings.Split(id, "-")
	if len(parts) != 4 {
		t.Errorf("expected 4 parts in correlation ID, got %d: %s", len(parts), id)
	}

	id2 := GenerateCorrelationID()
	if id == id2 {
		t.Error("correlation IDs should be unique")
	}
}

func TestAddSpanAttributes(t *testing.T) {
	cfg := &Config{
		Enabled:      true,
		Exporter:     "stdout",
		Endpoint:     "localhost:4317",
		SamplingRate: 1.0,
		ServiceName:  "test-attrs",
	}
	log := newTestLogger(t)
	tr := NewTracer(cfg, log)

	if err := tr.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ctx, end := tr.StartSpan(context.Background(), "test-span")
	defer end()

	AddSpanAttributes(ctx)
}

func TestSpanWithCorrelationID(t *testing.T) {
	cfg := &Config{
		Enabled:      true,
		Exporter:     "stdout",
		Endpoint:     "localhost:4317",
		SamplingRate: 1.0,
		ServiceName:  "test-corr",
	}
	log := newTestLogger(t)
	tr := NewTracer(cfg, log)

	if err := tr.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ctx, end := tr.StartSpan(context.Background(), "test-span")
	defer end()

	SpanWithCorrelationID(ctx, "test-corr-123")
}

func TestSpanConstants(t *testing.T) {
	consts := []string{
		AttrPoolName,
		AttrBackendAddr,
		AttrQueryType,
		AttrClientAddr,
		AttrCorrID,
		AttrDurationMs,
	}
	for _, c := range consts {
		if c == "" {
			t.Error("attribute constant should not be empty")
		}
	}
}
