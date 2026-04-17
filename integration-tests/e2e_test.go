//go:build e2e
// +build e2e

package e2e

import (
	"bufio"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	_ "github.com/go-sql-driver/mysql"
)

const (
	proxyPGPort    = 5432
	proxyMySQLPort = 3306
	restAPIPort    = 8080
	composeDir     = "../examples/docker"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func requireDockerCompose(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found — skipping E2E test")
	}
	if _, err := exec.LookPath("docker-compose"); err == nil {
		return // docker-compose v1
	}
	// Check docker compose v2
	cmd := exec.Command("docker", "compose", "version")
	if err := cmd.Run(); err != nil {
		t.Skip("docker compose not found — skipping E2E test")
	}
}

func dockerComposeArgs(args ...string) *exec.Cmd {
	// Try docker-compose v1 first
	if _, err := exec.LookPath("docker-compose"); err == nil {
		cmd := exec.Command("docker-compose", args...)
		cmd.Dir = filepath.Join("..", composeDir)
		return cmd
	}
	// Fall back to docker compose v2
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir = filepath.Join("..", composeDir)
	return cmd
}

func runCompose(args ...string) error {
	cmd := dockerComposeArgs(args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func waitForPort(ports []int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allUp := true
		for _, port := range ports {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
			if err != nil {
				allUp = false
				continue
			}
			conn.Close()
		}
		if allUp {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for ports %v", ports)
}

func setupCluster(t *testing.T) {
	t.Helper()
	requireDockerCompose(t)

	// Build the Docker image first
	t.Log("Building geryon Docker image...")
	buildCmd := exec.Command("docker", "build", "-t", "geryonproxy/geryon:latest", "-f", "Dockerfile", "..")
	buildCmd.Dir = ".."
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build geryon image: %v", err)
	}

	// Tear down any previous run
	_ = runCompose("down", "-v", "--remove-orphans")

	// Start services
	t.Log("Starting Docker Compose services...")
	if err := runCompose("up", "-d"); err != nil {
		t.Fatalf("docker compose up failed: %v", err)
	}

	// Wait for all services to be ready
	t.Log("Waiting for services to start...")
	if err := waitForPorts([]int{proxyPGPort, proxyMySQLPort, restAPIPort}, 60*time.Second); err != nil {
		t.Fatalf("Services did not start: %v", err)
	}

	// Additional wait for PostgreSQL to accept connections
	time.Sleep(5 * time.Second)
}

func teardownCluster(t *testing.T) {
	t.Helper()
	t.Log("Tearing down Docker Compose...")
	_ = runCompose("down", "-v", "--remove-orphans")
}

// TestE2E_PostgreSQL_Proxy tests connecting through Geryon to PostgreSQL.
func TestE2E_PostgreSQL_Proxy(t *testing.T) {
	setupCluster(t)
	defer teardownCluster(t)

	// Connect through proxy to PostgreSQL
	db, err := sql.Open("postgres", fmt.Sprintf(
		"host=127.0.0.1 port=%d user=geryon password=geryon_password dbname=testdb sslmode=disable",
		proxyPGPort,
	))
	if err != nil {
		t.Fatalf("Failed to open connection: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(5)
	db.SetConnMaxLifetime(30 * time.Second)

	// Test connectivity
	if err := db.Ping(); err != nil {
		t.Fatalf("Failed to ping through proxy: %v", err)
	}

	// Test query execution
	var result int
	if err := db.QueryRow("SELECT 42").Scan(&result); err != nil {
		t.Fatalf("Failed to execute SELECT: %v", err)
	}
	if result != 42 {
		t.Errorf("Expected 42, got %d", result)
	}

	// Test table creation and data insertion
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS e2e_test (id SERIAL PRIMARY KEY, name TEXT)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}
	defer func() { _, _ = db.Exec("DROP TABLE e2e_test") }()

	_, err = db.Exec("INSERT INTO e2e_test (name) VALUES ($1)", "e2e-test")
	if err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	var name string
	if err := db.QueryRow("SELECT name FROM e2e_test WHERE name = $1", "e2e-test").Scan(&name); err != nil {
		t.Fatalf("Failed to query inserted row: %v", err)
	}
	if name != "e2e-test" {
		t.Errorf("Expected 'e2e-test', got %q", name)
	}
}

// TestE2E_MySQL_Proxy tests connecting through Geryon to MySQL.
func TestE2E_MySQL_Proxy(t *testing.T) {
	setupCluster(t)
	defer teardownCluster(t)

	// Connect through proxy to MySQL
	db, err := sql.Open("mysql", fmt.Sprintf(
		"geryon:geryon_password@tcp(127.0.0.1:%d)/testdb",
		proxyMySQLPort,
	))
	if err != nil {
		t.Fatalf("Failed to open connection: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(5)
	db.SetConnMaxLifetime(30 * time.Second)

	// Test connectivity
	if err := db.Ping(); err != nil {
		t.Fatalf("Failed to ping through proxy: %v", err)
	}

	// Test query execution
	var result int
	if err := db.QueryRow("SELECT 42").Scan(&result); err != nil {
		t.Fatalf("Failed to execute SELECT: %v", err)
	}
	if result != 42 {
		t.Errorf("Expected 42, got %d", result)
	}

	// Test table creation and data insertion
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS e2e_test (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(255))")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}
	defer func() { _, _ = db.Exec("DROP TABLE e2e_test") }()

	_, err = db.Exec("INSERT INTO e2e_test (name) VALUES (?)", "e2e-test")
	if err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	var name string
	if err := db.QueryRow("SELECT name FROM e2e_test WHERE name = ?", "e2e-test").Scan(&name); err != nil {
		t.Fatalf("Failed to query inserted row: %v", err)
	}
	if name != "e2e-test" {
		t.Errorf("Expected 'e2e-test', got %q", name)
	}
}

// TestE2E_REST_API tests the REST API endpoints.
func TestE2E_REST_API(t *testing.T) {
	setupCluster(t)
	defer teardownCluster(t)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", restAPIPort)

	// Test health endpoint
	resp, err := http.Get(baseURL + "/api/v1/health")
	if err != nil {
		t.Fatalf("Failed to GET /api/v1/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}

	// Test pools endpoint
	resp, err = http.Get(baseURL + "/api/v1/pools")
	if err != nil {
		t.Fatalf("Failed to GET /api/v1/pools: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 for pools, got %d", resp.StatusCode)
	}

	// Verify pool names in response
	body := &strings.Builder{}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		body.WriteString(scanner.Text())
	}
	respBody := body.String()
	if !strings.Contains(respBody, "postgres-pool") {
		t.Error("Response should contain 'postgres-pool'")
	}
	if !strings.Contains(respBody, "mysql-pool") {
		t.Error("Response should contain 'mysql-pool'")
	}
}

// TestE2E_ConcurrentConnections tests concurrent connections through the proxy.
func TestE2E_ConcurrentConnections(t *testing.T) {
	setupCluster(t)
	defer teardownCluster(t)

	db, err := sql.Open("postgres", fmt.Sprintf(
		"host=127.0.0.1 port=%d user=geryon password=geryon_password dbname=testdb sslmode=disable",
		proxyPGPort,
	))
	if err != nil {
		t.Fatalf("Failed to open connection: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(20)

	// Run concurrent queries
	const concurrency = 10
	errCh := make(chan error, concurrency)
	done := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		go func(id int) {
			var result int
			err := db.QueryRow("SELECT $1", id).Scan(&result)
			if err != nil {
				errCh <- fmt.Errorf("goroutine %d: %v", id, err)
				return
			}
			if result != id {
				errCh <- fmt.Errorf("goroutine %d: expected %d, got %d", id, id, result)
				return
			}
			errCh <- nil
		}(i)
	}

	// Wait for all goroutines
	go func() {
		for i := 0; i < concurrency; i++ {
			<-errCh
		}
		close(done)
	}()

	select {
	case <-done:
		// Check for errors
		close(errCh)
		for err := range errCh {
			if err != nil {
				t.Error(err)
			}
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Timeout waiting for concurrent queries")
	}
}

// TestE2E_PoolStats verifies pool statistics are reported correctly.
func TestE2E_PoolStats(t *testing.T) {
	setupCluster(t)
	defer teardownCluster(t)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", restAPIPort)

	// Make a connection first
	db, err := sql.Open("postgres", fmt.Sprintf(
		"host=127.0.0.1 port=%d user=geryon password=geryon_password dbname=testdb sslmode=disable",
		proxyPGPort,
	))
	if err != nil {
		t.Fatalf("Failed to open connection: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		t.Fatalf("Failed to ping: %v", err)
	}

	// Get pool stats
	resp, err := http.Get(baseURL + "/api/v1/pools/postgres-pool")
	if err != nil {
		t.Fatalf("Failed to GET pool stats: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
}
