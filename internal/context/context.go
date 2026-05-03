// Package context provides context utilities for Geryon.
package context

import (
	"context"
)

// correlationIDKey is the context key for correlation ID.
type correlationIDKey struct{}

// WithCorrelationID returns a new context with the given correlation ID.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey{}, id)
}

// CorrelationID returns the correlation ID from the context, or empty string if not set.
func CorrelationID(ctx context.Context) string {
	if id, ok := ctx.Value(correlationIDKey{}).(string); ok {
		return id
	}
	return ""
}
