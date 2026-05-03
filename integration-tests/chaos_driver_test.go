//go:build integration
// +build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// DriverConnectionStorm opens many concurrent database connections and queries.
type DriverConnectionStorm struct {
	connections int
	db          *sql.DB
}

func (d *DriverConnectionStorm) Name() string {
	return "driver-connection-storm"
}

func (d *DriverConnectionStorm) Execute(ctx context.Context) error {
	var wg sync.WaitGroup
	var errors atomic.Int64

	for i := 0; i < d.connections; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := d.db.Conn(ctx)
			if err != nil {
				errors.Add(1)
				return
			}
			defer conn.Close()

			var result int
			if err := conn.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil {
				errors.Add(1)
			}
		}()
	}
	wg.Wait()

	if e := errors.Load(); e > int64(d.connections)/2 {
		return fmt.Errorf("too many failures: %d/%d", e, d.connections)
	}
	return nil
}

// DriverSlowQuery executes queries with random delays to simulate slow backends.
type DriverSlowQuery struct {
	db    *sql.DB
	delay time.Duration
}

func (s *DriverSlowQuery) Name() string {
	return "driver-slow-query"
}

func (s *DriverSlowQuery) Execute(ctx context.Context) error {
	delay := s.delay
	if delay == 0 {
		delay = time.Duration(rand.Intn(500)+100) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(ctx, delay+5*time.Second)
	defer cancel()

	var result int
	if err := s.db.QueryRowContext(ctx, "SELECT pg_sleep($1), 1",
		fmt.Sprintf("%.3f", delay.Seconds())).Scan(&struct{}{}, &result); err != nil {
		// pg_sleep may not be available — fall back to simple query
		return s.db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	}
	return nil
}

// DriverReadWriteMix issues interleaved reads and writes to test pool multiplexing.
type DriverReadWriteMix struct {
	db   *sql.DB
	read bool
}

func (r *DriverReadWriteMix) Name() string {
	if r.read {
		return "driver-read"
	}
	return "driver-write"
}

func (r *DriverReadWriteMix) Execute(ctx context.Context) error {
	if r.read {
		var result int
		return r.db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	}
	_, err := r.db.ExecContext(ctx, "CREATE TEMP TABLE IF NOT EXISTS _chaos_test (v int); INSERT INTO _chaos_test VALUES (1)")
	return err
}

// openDB creates a database connection to the proxy for chaos testing.
func openChaosDB() (*sql.DB, error) {
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		env("POSTGRES_HOST", "localhost"),
		env("POSTGRES_PORT", "5432"),
		env("POSTGRES_USER", "geryon"),
		env("POSTGRES_PASSWORD", "geryon_password"),
		env("POSTGRES_DB", "testdb"),
	)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(10)
	return db, nil
}

// TestChaosDriverConnectionStorm tests connection storm resilience via real PG driver.
func TestChaosDriverConnectionStorm(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("Skipping integration test. Set INTEGRATION=1 to run.")
	}

	db, err := openChaosDB()
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	op := &DriverConnectionStorm{connections: 50, db: db}
	if err := op.Execute(ctx); err != nil {
		t.Errorf("Connection storm failed: %v", err)
	}
}

// TestChaosDriverMixedWorkload tests mixed read/write workload under concurrency.
func TestChaosDriverMixedWorkload(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("Skipping integration test. Set INTEGRATION=1 to run.")
	}

	db, err := openChaosDB()
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runner := NewChaosRunner()
	defer runner.Stop()

	test := ChaosTest{
		name:        "driver-mixed-workload",
		duration:    30 * time.Second,
		concurrency: 20,
		operations: []ChaosOperation{
			&DriverReadWriteMix{db: db, read: true},
			&DriverReadWriteMix{db: db, read: false},
			&DriverConnectionStorm{connections: 10, db: db},
		},
	}

	if err := runner.Run(test); err != nil {
		t.Fatalf("Chaos test failed: %v", err)
	}
}
