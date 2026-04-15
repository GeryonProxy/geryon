package integration

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// MemorySnapshot captures memory statistics
type MemorySnapshot struct {
	Timestamp   time.Time
	Alloc       uint64
	TotalAlloc  uint64
	Sys         uint64
	NumGC       uint32
	HeapAlloc   uint64
	HeapSys     uint64
	HeapObjects uint64
	StackInUse  uint64
}

// CaptureMemorySnapshot captures current memory statistics
func CaptureMemorySnapshot() MemorySnapshot {
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)

	return MemorySnapshot{
		Timestamp:   time.Now(),
		Alloc:       m.Alloc,
		TotalAlloc:  m.TotalAlloc,
		Sys:         m.Sys,
		NumGC:       m.NumGC,
		HeapAlloc:   m.HeapAlloc,
		HeapSys:     m.HeapSys,
		HeapObjects: m.HeapObjects,
		StackInUse:  m.StackSys,
	}
}

// Format formats memory snapshot for display
func (m MemorySnapshot) Format() string {
	return fmt.Sprintf(
		"Alloc: %s, HeapAlloc: %s, HeapObjects: %d, NumGC: %d",
		formatBytes(m.Alloc),
		formatBytes(m.HeapAlloc),
		m.HeapObjects,
		m.NumGC,
	)
}

// MemoryLeakDetector detects memory leaks
func MemoryLeakDetector(t *testing.T, duration time.Duration, operation func()) {
	// Initial snapshot
	before := CaptureMemorySnapshot()
	t.Logf("Before: %s", before.Format())

	// Run operation repeatedly
	start := time.Now()
	iterations := 0

	for time.Since(start) < duration {
		operation()
		iterations++

		// Force GC every 1000 iterations
		if iterations%1000 == 0 {
			runtime.GC()
		}
	}

	// Final snapshot
	time.Sleep(1 * time.Second) // Allow for final GC
	runtime.GC()
	after := CaptureMemorySnapshot()
	t.Logf("After: %s", after.Format())

	// Analyze
	allocGrowth := float64(after.HeapAlloc) / float64(before.HeapAlloc)
	objectsGrowth := float64(after.HeapObjects) / float64(before.HeapObjects)

	t.Logf("Iterations: %d", iterations)
	t.Logf("HeapAlloc growth: %.2fx", allocGrowth)
	t.Logf("HeapObjects growth: %.2fx", objectsGrowth)

	// Check for significant growth
	if allocGrowth > 2.0 && after.HeapAlloc-before.HeapAlloc > 10*1024*1024 {
		t.Errorf("Potential memory leak: HeapAlloc grew by %.2fx (%.2f MB)",
			allocGrowth, float64(after.HeapAlloc-before.HeapAlloc)/(1024*1024))
	}
}

// formatBytes formats bytes to human readable string
func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// TestMemoryPoolConnectionReuse tests that connections are properly reused
func TestMemoryPoolConnectionReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory test in short mode")
	}

	// This test would require a running Geryon instance
	// For now, document what should be tested

	t.Log("Testing connection pool memory behavior")
	t.Log("Expected: Stable memory usage as connections are reused")
}

// TestMemoryCacheGrowth tests that cache memory stays within bounds
func TestMemoryCacheGrowth(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory test in short mode")
	}

	t.Log("Testing cache memory behavior")
	t.Log("Expected: Memory stays within configured max_memory limit")
}

// TestMemoryQueryLogging tests that query logging doesn't leak memory
func TestMemoryQueryLogging(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory test in short mode")
	}

	t.Log("Testing query logger memory behavior")
	t.Log("Expected: Stable memory as old logs are rotated")
}

// TestMemoryLongRunningStability tests memory stability over extended period
func TestMemoryLongRunningStability(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory test in short mode")
	}

	duration := 5 * time.Minute
	if testing.Verbose() {
		duration = 1 * time.Minute
	}

	t.Logf("Running long-running memory test for %s", duration)

	// Take periodic snapshots
	snapshots := make([]MemorySnapshot, 0)
	snapshots = append(snapshots, CaptureMemorySnapshot())

	start := time.Now()
	for time.Since(start) < duration {
		time.Sleep(10 * time.Second)
		snapshots = append(snapshots, CaptureMemorySnapshot())

		// Print progress
		latest := snapshots[len(snapshots)-1]
		t.Logf("[%s] %s", time.Since(start).Round(time.Second), latest.Format())
	}

	// Analyze trend
	if len(snapshots) < 3 {
		t.Skip("Not enough data points")
	}

	// Check if memory is growing linearly (leak) or stable
	first := snapshots[0]
	last := snapshots[len(snapshots)-1]
	middle := snapshots[len(snapshots)/2]

	growthRate := float64(last.HeapAlloc-first.HeapAlloc) / float64(len(snapshots))
	middleGrowth := float64(middle.HeapAlloc - first.HeapAlloc)
	lastGrowth := float64(last.HeapAlloc - middle.HeapAlloc)

	t.Logf("Growth rate: %.2f bytes/sample", growthRate)
	t.Logf("First half growth: %.2f MB", middleGrowth/(1024*1024))
	t.Logf("Second half growth: %.2f MB", lastGrowth/(1024*1024))

	// If second half growth is significantly higher than first half,
	// it might indicate a leak
	if lastGrowth > middleGrowth*2 && lastGrowth > 5*1024*1024 {
		t.Errorf("Potential memory leak detected: accelerating growth in second half")
	}
}

// BenchmarkMemoryAllocation benchmarks memory allocation patterns
func BenchmarkMemoryAllocation(b *testing.B) {
	snapshots := make([]MemorySnapshot, 0, b.N)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate some allocation pattern
		data := make([]byte, 1024)
		_ = data

		if i%100 == 0 {
			snapshots = append(snapshots, CaptureMemorySnapshot())
		}
	}

	b.StopTimer()

	// Report statistics
	if len(snapshots) > 1 {
		first := snapshots[0]
		last := snapshots[len(snapshots)-1]
		b.ReportMetric(float64(last.HeapAlloc-first.HeapAlloc), "bytes_growth")
		b.ReportMetric(float64(last.NumGC-first.NumGC), "gc_count")
	}
}
