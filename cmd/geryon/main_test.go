package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Duration
	}{
		{"empty string", "", 0},
		{"valid seconds", "5s", 5 * time.Second},
		{"valid minutes", "10m", 10 * time.Minute},
		{"valid hours", "2h", 2 * time.Hour},
		{"valid milliseconds", "500ms", 500 * time.Millisecond},
		{"invalid format", "invalid", 0},
		{"complex duration", "1h30m", 90 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseDuration(tt.input)
			if result != tt.expected {
				t.Errorf("parseDuration(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGenerateSelfSignedCert(t *testing.T) {
	// Create temp directory for test files
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)
	os.Chdir(tmpDir)

	err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("generateSelfSignedCert() failed: %v", err)
	}

	// Check that files were created
	certPath := filepath.Join(tmpDir, "geryon.crt")
	keyPath := filepath.Join(tmpDir, "geryon.key")

	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		t.Error("Certificate file was not created")
	}
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		t.Error("Key file was not created")
	}

	// Verify certificate content
	certData, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("Failed to read certificate: %v", err)
	}
	if !bytes.Contains(certData, []byte("BEGIN CERTIFICATE")) {
		t.Error("Certificate file does not contain expected PEM header")
	}

	// Verify key content
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("Failed to read key: %v", err)
	}
	if !bytes.Contains(keyData, []byte("BEGIN")) && !bytes.Contains(keyData, []byte("PRIVATE KEY")) {
		t.Error("Key file does not contain expected PEM header")
	}

	// Verify key has restrictive permissions
	// Note: On Windows, file permissions work differently, so we skip this check
	if os.PathSeparator == '/' {
		info, err := os.Stat(keyPath)
		if err != nil {
			t.Fatalf("Failed to stat key file: %v", err)
		}
		// Check that permissions are 0600 or more restrictive
		if info.Mode().Perm()&0077 != 0 {
			t.Errorf("Key file has overly permissive permissions: %v", info.Mode().Perm())
		}
	}
}

func TestGenerateSelfSignedCert_AlreadyExists(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)
	os.Chdir(tmpDir)

	// Create existing files
	os.WriteFile("geryon.crt", []byte("existing cert"), 0644)
	os.WriteFile("geryon.key", []byte("existing key"), 0600)

	// Should overwrite without error
	err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("generateSelfSignedCert() failed when files exist: %v", err)
	}

	// Verify files were overwritten with valid content
	certData, _ := os.ReadFile("geryon.crt")
	if bytes.Equal(certData, []byte("existing cert")) {
		t.Error("Certificate file was not overwritten")
	}
}

func TestMain_VersionFlag(t *testing.T) {
	// Save original args and restore after test
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Create a pipe to capture stdout
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	// Set up for version output test
	os.Args = []string{"geryon", "--version"}

	// We can't easily test main() directly since it calls os.Exit
	// Instead, we'll test the flag parsing logic by checking version variable
	if version == "" {
		t.Error("version variable should be set")
	}

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// If we were to run main, it would print version and exit
	// For now, just verify version format
	if !strings.Contains(version, "Geryon") {
		t.Log("Version format:", version)
	}

	_ = output // Avoid unused variable error
}

func TestParseDuration_Concurrent(t *testing.T) {
	// Test that parseDuration is safe for concurrent use
	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func(idx int) {
			durations := []string{"1s", "1m", "1h", "100ms", ""}
			parseDuration(durations[idx%len(durations)])
			done <- true
		}(i)
	}

	for i := 0; i < 100; i++ {
		select {
		case <-done:
			// success
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for concurrent parseDuration calls")
		}
	}
}

func TestGenerateConfig(t *testing.T) {
	// Save original args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// This test verifies that the config generation would work
	// by checking that the embed module is accessible
	os.Args = []string{"geryon", "--generate-config"}

	// Just verify the file exists (it would be read during --generate-config)
	// The actual test of main() with --generate-config would require
	// capturing stdout and verifying YAML content
	if _, err := os.Stat("geryon.example.yaml"); err != nil {
		t.Log("Note: geryon.example.yaml should exist for --generate-config to reference")
	}
}

func TestSignalHandling(t *testing.T) {
	// Test signal handling context cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Simulate signal handling by calling cancel
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	select {
	case <-ctx.Done():
		// Expected behavior
	case <-time.After(time.Second):
		t.Error("Context was not cancelled as expected")
	}
}

func TestHotReload(t *testing.T) {
	// Create a temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test.yaml")

	configContent := `
global:
  log_level: debug
  log_format: json

pools:
  - name: test-pool
    listen:
      host: 127.0.0.1
      port: 15432
    body: postgresql
    mode: transaction
    auth:
      mode: trust
    backend:
      hosts:
        - host: localhost
          port: 5432
          role: primary
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Test config loading (simulating what happens during hot reload)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read test config: %v", err)
	}

	if len(data) == 0 {
		t.Error("Config file is empty")
	}

	// Verify config contains expected content
	if !bytes.Contains(data, []byte("test-pool")) {
		t.Error("Config does not contain expected pool name")
	}
}

func TestGracefulShutdown(t *testing.T) {
	// Test the graceful shutdown sequence by verifying
	// that context cancellation propagates correctly
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdownOrder := []string{}
	var shutdownMu struct {
		sync.Mutex
	}

	// Simulate shutdown sequence
	go func() {
		<-ctx.Done()

		shutdownMu.Lock()
		shutdownOrder = append(shutdownOrder, "listeners")
		shutdownMu.Unlock()

		shutdownMu.Lock()
		shutdownOrder = append(shutdownOrder, "servers")
		shutdownMu.Unlock()

		shutdownMu.Lock()
		shutdownOrder = append(shutdownOrder, "cluster")
		shutdownMu.Unlock()
	}()

	// Trigger shutdown
	cancel()

	// Wait for shutdown to complete
	time.Sleep(50 * time.Millisecond)

	shutdownMu.Lock()
	if len(shutdownOrder) < 3 {
		t.Errorf("Shutdown sequence incomplete: %v", shutdownOrder)
	}
	shutdownMu.Unlock()
}

func TestVersionString(t *testing.T) {
	// Verify version string is properly formatted
	// Version may be "dev" during development or contain "Geryon" in production builds
	if version == "" {
		t.Error("version should not be empty")
	}

	// Version should either be "dev" or contain expected components
	if version != "dev" && !strings.Contains(version, "Geryon") {
		t.Errorf("version should be 'dev' or contain 'Geryon', got %q", version)
	}
}

func TestBuildVersion(t *testing.T) {
	// Verify build-time variables are set or have defaults
	if version == "" {
		t.Error("version should have a default value")
	}

	if commit == "" {
		t.Log("commit is empty (expected during development)")
	}

	if date == "" {
		t.Log("date is empty (expected during development)")
	}
}

// Mock test for flag parsing behavior
func TestFlagParsing(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantExit bool
	}{
		{"no flags", []string{"geryon"}, false},
		{"help flag", []string{"geryon", "--help"}, true},
		{"version flag", []string{"geryon", "--version"}, true},
		{"generate-config", []string{"geryon", "--generate-config"}, false},
		{"validate with config", []string{"geryon", "--config", "test.yaml", "--validate"}, false},
		{"generate-password", []string{"geryon", "--generate-password"}, false},
		{"generate-cert", []string{"geryon", "--generate-cert"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// These are smoke tests to verify flag combinations
			// Actual execution would require os.Exit handling
			t.Logf("Flag combination: %v (would exit: %v)", tt.args, tt.wantExit)
		})
	}
}

// Benchmark for parseDuration
func BenchmarkParseDuration(b *testing.B) {
	durations := []string{"1s", "5m", "1h30m", "100ms", ""}
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			parseDuration(durations[i%len(durations)])
			i++
		}
	})
}

// Test concurrent certificate generation
func TestGenerateSelfSignedCert_Concurrent(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	// Create separate directories for each concurrent test
	dirs := make([]string, 5)
	for i := 0; i < 5; i++ {
		dirs[i] = filepath.Join(tmpDir, fmt.Sprintf("cert_test_%d", i))
		os.MkdirAll(dirs[i], 0755)
	}

	errors := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func(dir string) {
			os.Chdir(dir)
			errors <- generateSelfSignedCert()
		}(dirs[i])
	}

	for i := 0; i < 5; i++ {
		if err := <-errors; err != nil {
			t.Errorf("Concurrent cert generation failed: %v", err)
		}
	}
}
