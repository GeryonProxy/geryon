package pool

import (
	"context"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
)

// TestSession_NewSession tests the NewSession function
func TestSession_NewSession(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s == nil {
		t.Fatal("NewSession returned nil")
	}
	if s.ID() == 0 {
		t.Error("Session.ID() should not be 0")
	}
}

// TestSession_User tests user getter/setter
func TestSession_User(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s.User() != "" {
		t.Errorf("Session.User() = %q, want empty", s.User())
	}

	s.SetUser("testuser")
	if s.User() != "testuser" {
		t.Errorf("Session.User() = %q, want testuser", s.User())
	}
}

// TestSession_Database tests database getter/setter
func TestSession_Database(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s.Database() != "" {
		t.Errorf("Session.Database() = %q, want empty", s.Database())
	}

	s.SetDatabase("testdb")
	if s.Database() != "testdb" {
		t.Errorf("Session.Database() = %q, want testdb", s.Database())
	}
}

// TestSession_AuthDone tests auth done getter/setter
func TestSession_AuthDone(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s.AuthDone() {
		t.Error("Session.AuthDone() should be false initially")
	}

	s.SetAuthDone()
	if !s.AuthDone() {
		t.Error("Session.AuthDone() should be true after SetAuthDone")
	}
}

// TestSession_InTransaction tests transaction state getter/setter
func TestSession_InTransaction(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s.InTransaction() {
		t.Error("Session.InTransaction() should be false initially")
	}

	s.SetInTransaction(true)
	if !s.InTransaction() {
		t.Error("Session.InTransaction() should be true after SetInTransaction")
	}
}

// TestSession_AutoCommitRelease tests auto commit release getter/setter
func TestSession_AutoCommitRelease(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if !s.AutoCommitRelease() {
		t.Error("Session.AutoCommitRelease() should be true initially")
	}

	s.SetAutoCommitRelease(false)
	if s.AutoCommitRelease() {
		t.Error("Session.AutoCommitRelease() should be false after SetAutoCommitRelease")
	}
}

// TestSession_TransactionStart tests transaction start time
func TestSession_TransactionStart(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if !s.TransactionStart().IsZero() {
		t.Error("Session.TransactionStart() should be zero initially")
	}
}

// TestSession_StartedAt tests started at time
func TestSession_StartedAt(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s.StartedAt().IsZero() {
		t.Error("Session.StartedAt() should not be zero")
	}
}

// TestSession_LastActive tests last active time
func TestSession_LastActive(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s.LastActive().IsZero() {
		t.Error("Session.LastActive() should not be zero initially")
	}

	oldTime := s.LastActive()
	time.Sleep(10 * time.Millisecond)
	s.UpdateLastActive()
	if !s.LastActive().After(oldTime) {
		t.Error("Session.LastActive() should be updated")
	}
}

// TestSession_QueryCount tests query count
func TestSession_QueryCount(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s.QueryCount() != 0 {
		t.Errorf("Session.QueryCount() = %d, want 0", s.QueryCount())
	}

	s.IncrementQueryCount()
	if s.QueryCount() != 1 {
		t.Errorf("Session.QueryCount() = %d, want 1", s.QueryCount())
	}
}

// TestSession_BytesIn tests bytes in counter
func TestSession_BytesIn(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s.BytesIn() != 0 {
		t.Errorf("Session.BytesIn() = %d, want 0", s.BytesIn())
	}

	s.AddBytesIn(100)
	if s.BytesIn() != 100 {
		t.Errorf("Session.BytesIn() = %d, want 100", s.BytesIn())
	}
}

// TestSession_BytesOut tests bytes out counter
func TestSession_BytesOut(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s.BytesOut() != 0 {
		t.Errorf("Session.BytesOut() = %d, want 0", s.BytesOut())
	}

	s.AddBytesOut(200)
	if s.BytesOut() != 200 {
		t.Errorf("Session.BytesOut() = %d, want 200", s.BytesOut())
	}
}

// TestSession_LastQuery tests last query getter/setter
func TestSession_LastQuery(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s.LastQuery() != "" {
		t.Errorf("Session.LastQuery() = %q, want empty", s.LastQuery())
	}

	s.SetLastQuery("SELECT 1")
	if s.LastQuery() != "SELECT 1" {
		t.Errorf("Session.LastQuery() = %q, want SELECT 1", s.LastQuery())
	}
}

// TestSession_PreparedStatements tests prepared statements
func TestSession_PreparedStatements(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s.PreparedStatements() == nil {
		t.Error("Session.PreparedStatements() should not be nil")
	}
}

// TestSession_Stats tests session stats
func TestSession_Stats(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	stats := s.Stats()
	if stats.ID != s.ID() {
		t.Errorf("Stats.ID = %d, want %d", stats.ID, s.ID())
	}
}

// TestSession_Pool tests pool getter
func TestSession_Pool(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s.Pool() != p {
		t.Error("Session.Pool() should return the pool")
	}
}

// TestSession_Strategy tests strategy getter
func TestSession_Strategy(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)
	if s.Strategy() != nil {
		t.Error("Session.Strategy() should be nil when not set")
	}
}

// TestSession_TargetRole tests target role getter/setter
func TestSession_TargetRole(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := &config.PoolConfig{
		Name: "test",
		Body: "postgresql",
		Mode: "transaction",
		Backend: config.BackendConfig{
			Hosts: []config.BackendHost{
				{Host: "localhost", Port: 5432, Role: "primary"},
			},
		},
	}

	p, err := NewPool(cfg, nil, log, nil)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSession(ctx, cancel, p, nil)

	// Default should be empty string
	if role := s.TargetRole(); role != "" {
		t.Errorf("TargetRole() = %q, want empty", role)
	}

	// Set to replica
	s.SetTargetRole("replica")
	if role := s.TargetRole(); role != "replica" {
		t.Errorf("TargetRole() = %q, want %q", role, "replica")
	}

	// Set to primary
	s.SetTargetRole("primary")
	if role := s.TargetRole(); role != "primary" {
		t.Errorf("TargetRole() = %q, want %q", role, "primary")
	}

	// Clear role
	s.SetTargetRole("")
	if role := s.TargetRole(); role != "" {
		t.Errorf("TargetRole() = %q, want empty after clear", role)
	}
}
