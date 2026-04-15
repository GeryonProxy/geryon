//go:build integration
// +build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

// TestPostgreSQLPooling tests PostgreSQL connection pooling
func TestPostgreSQLPooling(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("Skipping integration test. Set INTEGRATION=1 to run.")
	}

	pgHost := env("POSTGRES_HOST", "localhost")
	pgPort := env("POSTGRES_PORT", "5432")
	pgUser := env("POSTGRES_USER", "geryon")
	pgPass := env("POSTGRES_PASSWORD", "geryon_password")
	pgDB := env("POSTGRES_DB", "testdb")

	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		pgHost, pgPort, pgUser, pgPass, pgDB)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test basic query
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("Failed to ping PostgreSQL: %v", err)
	}

	// Test query execution
	var result int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil {
		t.Fatalf("Failed to execute query: %v", err)
	}
	if result != 1 {
		t.Errorf("Expected 1, got %d", result)
	}

	// Test connection pooling
	t.Run("PoolExhaustion", func(t *testing.T) {
		conns := make([]*sql.Conn, 0, 10)
		for i := 0; i < 10; i++ {
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatalf("Failed to get connection %d: %v", i, err)
			}
			conns = append(conns, conn)
		}

		// Release connections
		for _, conn := range conns {
			conn.Close()
		}
	})

	// Test transaction handling
	t.Run("Transaction", func(t *testing.T) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("Failed to begin transaction: %v", err)
		}

		if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE test (id INT)"); err != nil {
			tx.Rollback()
			t.Fatalf("Failed to create temp table: %v", err)
		}

		if _, err := tx.ExecContext(ctx, "INSERT INTO test VALUES (1)"); err != nil {
			tx.Rollback()
			t.Fatalf("Failed to insert: %v", err)
		}

		if err := tx.Commit(); err != nil {
			t.Fatalf("Failed to commit: %v", err)
		}
	})
}

// TestMySQLPooling tests MySQL connection pooling
func TestMySQLPooling(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("Skipping integration test. Set INTEGRATION=1 to run.")
	}

	mysqlHost := env("MYSQL_HOST", "localhost")
	mysqlPort := env("MYSQL_PORT", "3306")
	mysqlUser := env("MYSQL_USER", "geryon")
	mysqlPass := env("MYSQL_PASSWORD", "geryon_password")
	mysqlDB := env("MYSQL_DB", "testdb")

	connStr := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s",
		mysqlUser, mysqlPass, mysqlHost, mysqlPort, mysqlDB)

	db, err := sql.Open("mysql", connStr)
	if err != nil {
		t.Fatalf("Failed to connect to MySQL: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test basic query
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("Failed to ping MySQL: %v", err)
	}

	// Test query execution
	var result int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil {
		t.Fatalf("Failed to execute query: %v", err)
	}
	if result != 1 {
		t.Errorf("Expected 1, got %d", result)
	}

	// Test connection pooling
	t.Run("PoolExhaustion", func(t *testing.T) {
		conns := make([]*sql.Conn, 0, 10)
		for i := 0; i < 10; i++ {
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatalf("Failed to get connection %d: %v", i, err)
			}
			conns = append(conns, conn)
		}

		// Release connections
		for _, conn := range conns {
			conn.Close()
		}
	})

	// Test transaction handling
	t.Run("Transaction", func(t *testing.T) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("Failed to begin transaction: %v", err)
		}

		if _, err := tx.ExecContext(ctx, "CREATE TEMPORARY TABLE test (id INT)"); err != nil {
			tx.Rollback()
			t.Fatalf("Failed to create temp table: %v", err)
		}

		if _, err := tx.ExecContext(ctx, "INSERT INTO test VALUES (1)"); err != nil {
			tx.Rollback()
			t.Fatalf("Failed to insert: %v", err)
		}

		if err := tx.Commit(); err != nil {
			t.Fatalf("Failed to commit: %v", err)
		}
	})
}

// TestAuthModes tests different authentication modes
func TestAuthModes(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("Skipping integration test. Set INTEGRATION=1 to run.")
	}

	// This test would require running Geryon with different auth configurations
	// and verifying behavior. For now, we just document what should be tested.

	testCases := []struct {
		name     string
		authMode string
	}{
		{"Passthrough", "passthrough"},
		{"SCRAM", "scram-sha-256"},
		{"MD5", "md5"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Testing auth mode: %s", tc.authMode)
			// Implementation would require starting Geryon with specific config
		})
	}
}

// TestPreparedStatements tests prepared statement handling
func TestPreparedStatements(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("Skipping integration test. Set INTEGRATION=1 to run.")
	}

	// This test verifies that prepared statements work correctly
	// across different pooling modes

	testCases := []struct {
		name string
		mode string
	}{
		{"Session", "session"},
		{"Transaction", "transaction"},
		{"Statement", "statement"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Testing prepared statements in %s mode", tc.mode)
			// Implementation would require starting Geryon with specific pool mode
		})
	}
}

// TestCacheInvalidation tests cache invalidation behavior
func TestCacheInvalidation(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("Skipping integration test. Set INTEGRATION=1 to run.")
	}

	// Test cases:
	// 1. SELECT query should be cached
	// 2. INSERT/UPDATE/DELETE should invalidate cache
	// 3. Manual cache invalidation via API
}

// TestReadWriteSplitting tests read/write routing
func TestReadWriteSplitting(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("Skipping integration test. Set INTEGRATION=1 to run.")
	}

	// Test cases:
	// 1. SELECT should route to replica
	// 2. INSERT/UPDATE/DELETE should route to primary
	// 3. Transaction should stick to one backend
}

// TestFailover tests backend failover behavior
func TestFailover(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("Skipping integration test. Set INTEGRATION=1 to run.")
	}

	// Test cases:
	// 1. Kill primary backend, verify failover
	// 2. Backend recovery detection
	// 3. Connection retry behavior
}
