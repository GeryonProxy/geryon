package pool

import (
	"testing"

	"github.com/GeryonProxy/geryon/internal/config"
)

func testBackend(role string, healthy bool) *Backend {
	b := &Backend{
		Host: "127.0.0.1",
		Port: 5432,
		Role: role,
	}
	b.Healthy.Store(healthy)
	return b
}

func TestNewRouter_NoPrimary(t *testing.T) {
	replica := testBackend("replica", true)
	_, err := NewRouter(&config.RoutingConfig{ReadWriteSplit: true}, []*Backend{replica})
	if err == nil {
		t.Error("Should error when no primary")
	}
}

func TestNewRouter_Basic(t *testing.T) {
	primary := testBackend("primary", true)
	replica := testBackend("replica", true)

	r, err := NewRouter(&config.RoutingConfig{}, []*Backend{primary, replica})
	if err != nil {
		t.Fatalf("NewRouter failed: %v", err)
	}

	if r.Primary() == nil {
		t.Error("Primary should be set")
	}
	if len(r.Replicas()) != 1 {
		t.Errorf("Replicas count = %d, want 1", len(r.Replicas()))
	}
}

func TestRouter_RouteQuery_Transaction(t *testing.T) {
	primary := testBackend("primary", true)
	r, _ := NewRouter(&config.RoutingConfig{}, []*Backend{primary})

	b, err := r.RouteQuery("SELECT 1", true)
	if err != nil {
		t.Fatalf("RouteQuery failed: %v", err)
	}
	if b != primary {
		t.Error("In-transaction query should route to primary")
	}
}

func TestRouter_RouteQuery_NoPrimaryInTransaction(t *testing.T) {
	r := &Router{primary: nil, defaultRead: false}
	_, err := r.RouteQuery("SELECT 1", true)
	if err == nil {
		t.Error("Should error when no primary and in transaction")
	}
}

func TestRouter_RouteQuery_WriteToPrimary(t *testing.T) {
	primary := testBackend("primary", true)
	r, _ := NewRouter(&config.RoutingConfig{}, []*Backend{primary})

	b, err := r.RouteQuery("INSERT INTO t VALUES (1)", false)
	if err != nil {
		t.Fatalf("RouteQuery failed: %v", err)
	}
	if b != primary {
		t.Error("Write query should route to primary")
	}
}

func TestRouter_RouteQuery_ReadToReplica(t *testing.T) {
	primary := testBackend("primary", true)
	replica := testBackend("replica", true)
	r, _ := NewRouter(&config.RoutingConfig{ReadWriteSplit: true}, []*Backend{primary, replica})

	b, err := r.RouteQuery("SELECT 1", false)
	if err != nil {
		t.Fatalf("RouteQuery failed: %v", err)
	}
	if b != replica {
		t.Error("Read query should route to replica")
	}
}

func TestRouter_RouteQuery_ReadNoReplica(t *testing.T) {
	primary := testBackend("primary", true)
	r, _ := NewRouter(&config.RoutingConfig{ReadWriteSplit: true}, []*Backend{primary})

	// Falls back to primary when no replicas
	b, err := r.RouteQuery("SELECT 1", false)
	if err != nil {
		t.Fatalf("RouteQuery failed: %v", err)
	}
	if b != primary {
		t.Error("Should fall back to primary when no replicas")
	}
}

func TestRouter_RouteQuery_CustomRule(t *testing.T) {
	primary := testBackend("primary", true)
	replica := testBackend("replica", true)
	cfg := &config.RoutingConfig{
		ReadWriteSplit: true,
		Rules: []config.RoutingRule{
			{Match: "FOR UPDATE", Target: "primary", Fallback: "primary"},
		},
	}
	r, _ := NewRouter(cfg, []*Backend{primary, replica})

	b, err := r.RouteQuery("SELECT * FROM t FOR UPDATE", false)
	if err != nil {
		t.Fatalf("RouteQuery failed: %v", err)
	}
	if b != primary {
		t.Error("FOR UPDATE query should route to primary")
	}
}

func TestRouter_UnhealthyReplica(t *testing.T) {
	primary := testBackend("primary", true)
	replica := testBackend("replica", false)
	r, _ := NewRouter(&config.RoutingConfig{ReadWriteSplit: true}, []*Backend{primary, replica})

	// Falls back to primary when replica is unhealthy
	b, err := r.RouteQuery("SELECT 1", false)
	if err != nil {
		t.Fatalf("RouteQuery failed: %v", err)
	}
	if b != primary {
		t.Error("Should fall back to primary when replica unhealthy")
	}
}

func TestRouter_UpdateBackends(t *testing.T) {
	primary := testBackend("primary", true)
	replica := testBackend("replica", true)
	r, _ := NewRouter(&config.RoutingConfig{}, []*Backend{primary})

	r.UpdateBackends([]*Backend{primary, replica})
	if r.Primary() != primary {
		t.Error("Primary should be updated")
	}
	if len(r.Replicas()) != 1 {
		t.Errorf("Replicas = %d, want 1", len(r.Replicas()))
	}
}

func TestRouter_RouteQueryDetailed(t *testing.T) {
	primary := testBackend("primary", true)
	replica := testBackend("replica", true)
	r, _ := NewRouter(&config.RoutingConfig{ReadWriteSplit: true}, []*Backend{primary, replica})

	result, err := r.RouteQueryDetailed("SELECT 1", false)
	if err != nil {
		t.Fatalf("RouteQueryDetailed failed: %v", err)
	}
	if result.QueryType != "read" {
		t.Errorf("QueryType = %q, want read", result.QueryType)
	}
	if !result.IsReplica {
		t.Error("Should be routed to replica")
	}
}

func TestShouldRouteToReplica(t *testing.T) {
	if !ShouldRouteToReplica("SELECT 1") {
		t.Error("SELECT should route to replica")
	}
	if ShouldRouteToReplica("INSERT INTO t VALUES (1)") {
		t.Error("INSERT should not route to replica")
	}
}

func TestExtractHint(t *testing.T) {
	hint, ok := ExtractHint("/* route:replica */ SELECT 1")
	if !ok || hint != "replica" {
		t.Errorf("ExtractHint = (%q, %v), want (%q, true)", hint, ok, "replica")
	}

	_, ok = ExtractHint("SELECT 1")
	if ok {
		t.Error("Should not find hint when none present")
	}
}

func TestExtractHint_LineComment(t *testing.T) {
	hint, ok := ExtractHint("-- route:primary\nSELECT 1")
	if !ok || hint != "primary" {
		t.Errorf("ExtractHint = (%q, %v), want (%q, true)", hint, ok, "primary")
	}
}

func TestStripHints(t *testing.T) {
	got := StripHints("/* route:replica */ SELECT 1")
	want := "SELECT 1"
	if got != want {
		t.Errorf("StripHints = %q, want %q", got, want)
	}
}
