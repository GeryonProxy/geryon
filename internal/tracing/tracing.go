// Package tracing provides distributed tracing support for Geryon
// using OpenTelemetry. Configure via Config struct and call Init() before use.
package tracing

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/GeryonProxy/geryon/internal/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Config holds tracing configuration.
type Config struct {
	Enabled      bool    `yaml:"enabled"`
	Exporter     string  `yaml:"exporter"`      // "otlpgrpc" | "jaeger" | "zipkin"
	Endpoint     string  `yaml:"endpoint"`     // OTLP endpoint
	SamplingRate float64 `yaml:"sampling_rate"` // 0.0 - 1.0
	ServiceName  string  `yaml:"service_name"`
}

// Tracer is the Geryon tracer backed by OpenTelemetry SDK.
type Tracer struct {
	config     *Config
	log        *logger.Logger
	mu         sync.RWMutex
	shutdownFn func(context.Context) error
	tracer     trace.Tracer
	inited     atomic.Bool
}

// NewTracer creates a new Tracer.
func NewTracer(cfg *Config, log *logger.Logger) *Tracer {
	return &Tracer{
		config: cfg,
		log:    log,
	}
}

// Init initializes the OpenTelemetry SDK with the configured exporter.
// Safe to call multiple times; only the first call has effect.
func (t *Tracer) Init(ctx context.Context) error {
	if !t.config.Enabled || t.inited.Load() {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.inited.Load() {
		return nil
	}

	var exporter sdktrace.SpanExporter
	var err error

	switch t.config.Exporter {
	case "otlpgrpc":
		exporter, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(t.config.Endpoint),
			otlptracegrpc.WithInsecure(),
		)
	default:
		t.log.Warn("Unknown tracing exporter, tracing disabled", "exporter", t.config.Exporter)
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to create trace exporter: %w", err)
	}

	sampler := sdktrace.TraceIDRatioBased(t.config.SamplingRate)
	if t.config.SamplingRate <= 0 {
		sampler = sdktrace.NeverSample()
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(t.config.ServiceName),
		),
	)
	if err != nil {
		res = resource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	t.tracer = tp.Tracer(t.config.ServiceName)
	t.shutdownFn = tp.Shutdown

	t.inited.Store(true)
	t.log.Info("Tracing initialized", "exporter", t.config.Exporter, "endpoint", t.config.Endpoint)
	return nil
}

// StartSpan starts a new span with the given name.
// If tracing is disabled, returns the original context with a no-op end function.
func (t *Tracer) StartSpan(ctx context.Context, name string) (context.Context, func()) {
	if t == nil || t.config == nil || !t.config.Enabled || !t.inited.Load() || t.tracer == nil {
		return ctx, func() {}
	}

	spanCtx, span := t.tracer.Start(ctx, name)
	return spanCtx, func() { span.End() }
}

// SpanFromContext returns the current span from context, or a no-op span.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// AddSpanAttributes adds key-value attributes to the current span.
func AddSpanAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(attrs...)
	}
}

// SpanWithCorrelationID adds correlation ID to a span from context.
func SpanWithCorrelationID(ctx context.Context, corrID string) {
	AddSpanAttributes(ctx, attribute.String(AttrCorrID, corrID))
}

// Attributes for common spans.
const (
	AttrPoolName    = "pool.name"
	AttrBackendAddr = "backend.addr"
	AttrQueryType   = "query.type"
	AttrClientAddr  = "client.addr"
	AttrCorrID      = "correlation.id"
	AttrDurationMs  = "duration_ms"
)

// Span represents a trace span (placeholder for binary compat).
type Span struct {
	Name string
}

// GenerateCorrelationID generates a new UUIDv4-like correlation ID.
func GenerateCorrelationID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return fmt.Sprintf("unknown-%d", 0)
	}
	return fmt.Sprintf("%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:])
}

// Shutdown gracefully shuts down the tracing provider.
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t.shutdownFn != nil {
		return t.shutdownFn(ctx)
	}
	return nil
}