package integration

import (
	"context"
	"fmt"
	"math/rand"
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
	mu         sync.RWMutex
	results    []ChaosResult
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// ChaosResult represents the result of a chaos test
type ChaosResult struct {
	Operation   string
	StartTime   time.Time
	EndTime     time.Time
	Duration    time.Duration
	Error       error
	Success     bool
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
		byOp[r.Operation] = append(byOp[r.Operation], r.results)
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

// ConnectionStorm simulates a connection storm
type ConnectionStorm struct {
	connections int
}

func (c *ConnectionStorm) Name() string {
	return "connection-storm"
}

func (c *ConnectionStorm) Execute(ctx context.Context) error {
	// Simulate creating many connections
	// This would connect to Geryon and create connections
	time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
	return nil
}

// SlowQuery simulates slow queries
type SlowQuery struct {
	delay time.Duration
}

func (s *SlowQuery) Name() string {
	return "slow-query"
}

func (s *SlowQuery) Execute(ctx context.Context) error {
	delay := time.Duration(rand.Intn(1000)+100) * time.Millisecond
	time.Sleep(delay)
	return nil
}

// BackendFailure simulates backend failures
type BackendFailure struct {
	backend string
}

func (b *BackendFailure) Name() string {
	return "backend-failure"
}

func (b *BackendFailure) Execute(ctx context.Context) error {
	// This would simulate killing a backend
	// For safety, this is a no-op in the example
	return nil
}

// NetworkPartition simulates network partitions
type NetworkPartition struct {
	duration time.Duration
}

func (n *NetworkPartition) Name() string {
	return "network-partition"
}

func (n *NetworkPartition) Execute(ctx context.Context) error {
	// This would simulate network issues
	time.Sleep(time.Duration(rand.Intn(500)) * time.Millisecond)
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
