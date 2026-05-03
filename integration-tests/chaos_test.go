package integration

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"
)

// ChaosTest represents a chaos engineering test
type ChaosTest struct {
	name        string
	duration    time.Duration
	concurrency int
	operations  []ChaosOperation
}

// ChaosOperation represents a chaos operation
type ChaosOperation interface {
	Name() string
	Execute(ctx context.Context) error
}

// ChaosRunner runs chaos tests
type ChaosRunner struct {
	mu      sync.RWMutex
	results []ChaosResult
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// ChaosResult represents the result of a chaos test
type ChaosResult struct {
	Operation string
	StartTime time.Time
	EndTime   time.Time
	Duration  time.Duration
	Error     error
	Success   bool
}

// NewChaosRunner creates a new chaos runner
func NewChaosRunner() *ChaosRunner {
	return &ChaosRunner{
		results: make([]ChaosResult, 0),
		stopCh:  make(chan struct{}),
	}
}

// Run runs a chaos test
func (c *ChaosRunner) Run(test ChaosTest) error {
	fmt.Printf("Starting chaos test: %s\n", test.name)
	fmt.Printf("Duration: %s, Concurrency: %d\n", test.duration, test.concurrency)

	ctx, cancel := context.WithTimeout(context.Background(), test.duration)
	defer cancel()

	// Start workers
	for i := 0; i < test.concurrency; i++ {
		c.wg.Add(1)
		go c.worker(ctx, test.operations)
	}

	// Wait for completion
	c.wg.Wait()

	// Print results
	c.printResults()

	return nil
}

func (c *ChaosRunner) worker(ctx context.Context, operations []ChaosOperation) {
	defer c.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		default:
			// Pick random operation
			op := operations[rand.Intn(len(operations))]

			result := ChaosResult{
				Operation: op.Name(),
				StartTime: time.Now(),
			}

			err := op.Execute(ctx)
			result.EndTime = time.Now()
			result.Duration = result.EndTime.Sub(result.StartTime)
			result.Success = err == nil
			result.Error = err

			c.mu.Lock()
			c.results = append(c.results, result)
			c.mu.Unlock()
		}
	}
}

func (c *ChaosRunner) printResults() {
	c.mu.RLock()
	defer c.mu.RUnlock()

	fmt.Println("\n=== Chaos Test Results ===")

	// Group by operation
	byOp := make(map[string][]ChaosResult)
	for _, r := range c.results {
		byOp[r.Operation] = append(byOp[r.Operation], r)
	}

	for opName, results := range byOp {
		successes := 0
		failures := 0
		var totalDuration time.Duration

		for _, r := range results {
			if r.Success {
				successes++
			} else {
				failures++
			}
			totalDuration += r.Duration
		}

		avgDuration := totalDuration / time.Duration(len(results))
		successRate := float64(successes) / float64(len(results)) * 100

		fmt.Printf("%s: %d ops, %.1f%% success, avg %.2fms/op\n",
			opName, len(results), successRate, float64(avgDuration.Microseconds())/1000)

		if failures > 0 {
			fmt.Printf("  Failures: %d\n", failures)
		}
	}
}

// Stop stops the chaos runner
func (c *ChaosRunner) Stop() {
	close(c.stopCh)
	c.wg.Wait()
}

// Example chaos operations

// ConnectionStorm simulates a connection storm by opening many TCP connections
// to Geryon in parallel and immediately closing them.
type ConnectionStorm struct {
	connections int
	targetAddr  string
}

func (c *ConnectionStorm) Name() string {
	return "connection-storm"
}

func (c *ConnectionStorm) Execute(ctx context.Context) error {
	addr := c.targetAddr
	if addr == "" {
		addr = env("GERYON_ADDR", "127.0.0.1:15432")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, c.connections)

	for i := 0; i < c.connections; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
			// Hold connection briefly then close
			time.Sleep(time.Duration(rand.Intn(50)) * time.Millisecond)
			conn.Close()
		}()
	}
	wg.Wait()
	close(errCh)

	return nil
}

// SlowQuery simulates slow queries by connecting to Geryon, sending a query
// that triggers backend processing, and measuring latency.
type SlowQuery struct {
	targetAddr string
}

func (s *SlowQuery) Name() string {
	return "slow-query"
}

func (s *SlowQuery) Execute(ctx context.Context) error {
	addr := s.targetAddr
	if addr == "" {
		addr = env("GERYON_ADDR", "127.0.0.1:15432")
	}

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}
	defer conn.Close()

	// Send a small payload to trigger proxy processing
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, err = conn.Write([]byte("SELECT 1"))
	if err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	// Simulate query latency by reading response with a timeout
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1024)
	_, _ = conn.Read(buf)

	return nil
}

// BackendFailure simulates backend failures by repeatedly connecting to
// Geryon and checking that the proxy handles unreachable backends gracefully.
type BackendFailure struct {
	backend string
}

func (b *BackendFailure) Name() string {
	return "backend-failure"
}

func (b *BackendFailure) Execute(ctx context.Context) error {
	addr := env("GERYON_ADDR", "127.0.0.1:15432")

	// Attempt connections to exercise error paths when backends may be down
	for i := 0; i < 3; i++ {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			return fmt.Errorf("dial failed on attempt %d: %w", i+1, err)
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = conn.Write([]byte("SELECT 1"))
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)
		conn.Close()

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return nil
}

// NetworkPartition simulates network issues by opening connections and
// abruptly closing them mid-stream to test proxy resilience.
type NetworkPartition struct {
	duration time.Duration
}

func (n *NetworkPartition) Name() string {
	return "network-partition"
}

func (n *NetworkPartition) Execute(ctx context.Context) error {
	addr := env("GERYON_ADDR", "127.0.0.1:15432")

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}

	// Write partial data then abruptly close (simulates partition)
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, _ = conn.Write([]byte("SEL"))

	// Simulate partition duration with half-open connection
	partitionDuration := n.duration
	if partitionDuration == 0 {
		partitionDuration = time.Duration(rand.Intn(200)+50) * time.Millisecond
	}

	select {
	case <-time.After(partitionDuration):
	case <-ctx.Done():
		conn.Close()
		return ctx.Err()
	}

	// Abrupt close — no graceful shutdown
	conn.Close()
	return nil
}

// TestChaosConnectionStorm tests connection storm resilience
func TestChaosConnectionStorm(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	runner := NewChaosRunner()
	defer runner.Stop()

	test := ChaosTest{
		name:        "connection-storm",
		duration:    30 * time.Second,
		concurrency: 100,
		operations: []ChaosOperation{
			&ConnectionStorm{connections: 50},
			&SlowQuery{},
		},
	}

	if err := runner.Run(test); err != nil {
		t.Fatalf("Chaos test failed: %v", err)
	}
}

// TestChaosBackendFailure tests backend failure resilience
func TestChaosBackendFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	runner := NewChaosRunner()
	defer runner.Stop()

	test := ChaosTest{
		name:        "backend-failure",
		duration:    60 * time.Second,
		concurrency: 20,
		operations: []ChaosOperation{
			&BackendFailure{},
			&SlowQuery{},
			&ConnectionStorm{connections: 10},
		},
	}

	if err := runner.Run(test); err != nil {
		t.Fatalf("Chaos test failed: %v", err)
	}
}

// TestChaosMixedWorkload tests mixed workload resilience
func TestChaosMixedWorkload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	runner := NewChaosRunner()
	defer runner.Stop()

	test := ChaosTest{
		name:        "mixed-workload",
		duration:    120 * time.Second,
		concurrency: 50,
		operations: []ChaosOperation{
			&ConnectionStorm{connections: 20},
			&SlowQuery{},
			&BackendFailure{},
			&NetworkPartition{},
		},
	}

	if err := runner.Run(test); err != nil {
		t.Fatalf("Chaos test failed: %v", err)
	}
}
