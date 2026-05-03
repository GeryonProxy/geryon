package context

import (
	"context"
	"testing"
)

func TestWithCorrelationID(t *testing.T) {
	ctx := context.Background()
	
	result := WithCorrelationID(ctx, "test-id-123")
	
	if result == nil {
		t.Fatal("expected non-nil context")
	}
	
	val := result.Value(correlationIDKey{})
	if val == nil {
		t.Fatal("expected correlation ID to be set in context")
	}
	
	if val != "test-id-123" {
		t.Errorf("expected 'test-id-123', got %v", val)
	}
}

func TestCorrelationID(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected string
	}{
		{
			name:     "with correlation ID",
			ctx:      WithCorrelationID(context.Background(), "abc-123"),
			expected: "abc-123",
		},
		{
			name:     "without correlation ID",
			ctx:      context.Background(),
			expected: "",
		},
		{
			name:     "with empty correlation ID",
			ctx:      WithCorrelationID(context.Background(), ""),
			expected: "",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CorrelationID(tt.ctx)
			if result != tt.expected {
				t.Errorf("CorrelationID() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestCorrelationIDKeyType(t *testing.T) {
	// Ensure different key types don't collide — this is a Go context contract test
	// Two distinct key types with the same concrete value should not share entries
	type anotherKey struct{}
	
	ctx := WithCorrelationID(context.Background(), "shared")
	
	// A different key type should not see the value set by correlationIDKey
	if ctx.Value(anotherKey{}) != nil {
		t.Error("different key type should not see other key's value")
	}
	
	// But correlationIDKey should still see its value
	if CorrelationID(ctx) != "shared" {
		t.Error("correlationIDKey should still see its own value")
	}
}

func TestWithCorrelationID_Nested(t *testing.T) {
	ctx1 := WithCorrelationID(context.Background(), "first")
	ctx2 := WithCorrelationID(ctx1, "second")
	ctx3 := WithCorrelationID(ctx2, "third")
	
	if CorrelationID(ctx1) != "first" {
		t.Error("ctx1 should have first ID")
	}
	if CorrelationID(ctx2) != "second" {
		t.Error("ctx2 should have second ID")
	}
	if CorrelationID(ctx3) != "third" {
		t.Error("ctx3 should have third ID")
	}
}

func TestWithCorrelationID_Overwrite(t *testing.T) {
	ctx := context.Background()
	ctx = WithCorrelationID(ctx, "original")
	ctx = WithCorrelationID(ctx, "overwritten")
	
	if CorrelationID(ctx) != "overwritten" {
		t.Errorf("expected 'overwritten', got %q", CorrelationID(ctx))
	}
}