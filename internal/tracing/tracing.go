// Package tracing provides distributed tracing support for Geryon.
// This is a lightweight implementation that provides correlation ID
// generation and propagation. For full OpenTelemetry integration,
// run: go get go.opentelemetry.io/otel ...
package tracing

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"

	"github.com/GeryonProxy/geryon/internal/logger"
)

// Config holds tracing configuration.
type Config struct {
	Enabled      bool    `yaml:"enabled"`
	Exporter     string  `yaml:"exporter"`      // "otlpgrpc" | "jaeger" | "zipkin"
	Endpoint     string  `yaml:"endpoint"`      // OTLP endpoint
	SamplingRate float64 `yaml:"sampling_rate"` // 0.0 - 1.0
	ServiceName  string  `yaml:"service_name"`
}

// Tracer is the Geryon tracer.
type Tracer struct {
	config *Config
	log    *logger.Logger
	mu     sync.RWMutex
}

// NewTracer creates a new Tracer.
func NewTracer(cfg *Config, log *logger.Logger) *Tracer {
	return &Tracer{
		config: cfg,
		log:    log,
	}
}

// StartSpan starts a new span (placeholder for future OpenTelemetry integration).
func (t *Tracer) StartSpan(ctx context.Context, name string) (context.Context, func()) {
	// For now, just return the context as-is.
	// Full OpenTelemetry integration can be added later.
	return ctx, func() {}
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

// Span represents a trace span (placeholder).
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