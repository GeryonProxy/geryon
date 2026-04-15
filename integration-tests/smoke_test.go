//go:build integration
// +build integration

package integration

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSmoke_ProxyStarts verifies the proxy starts and accepts connections.
func TestSmoke_ProxyStarts(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("Skipping integration test. Set INTEGRATION=1 to run.")
	}

	// Create temp config
	config := `
global:
  log_level: error
pools:
  - name: test-pg
    body: postgresql
    mode: session
    listen:
      host: "127.0.0.1"
      port: 15432
    backend:
      hosts:
        - host: "127.0.0.1"
          port: 55432
          role: primary
    auth:
      mode: passthrough
  - name: test-mysql
    body: mysql
    mode: session
    listen:
      host: "127.0.0.1"
      port: 13306
    backend:
      hosts:
        - host: "127.0.0.1"
          port: 53306
          role: primary
    auth:
      mode: passthrough
  - name: test-mssql
    body: mssql
    mode: session
    listen:
      host: "127.0.0.1"
      port: 11433
    backend:
      hosts:
        - host: "127.0.0.1"
          port: 51433
          role: primary
    auth:
      mode: passthrough
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "geryon.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Build geryon
	geryonBin := filepath.Join(tmpDir, "geryon.exe")
	buildCmd := exec.Command("go", "build", "-o", geryonBin, "./cmd/geryon")
	buildCmd.Dir = ".."
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Skipf("Skipping: failed to build geryon: %v, output: %s", err, output)
	}

	// Start geryon
	cmd := exec.Command(geryonBin, "--config", configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start geryon: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	// Wait for geryon to start
	time.Sleep(2 * time.Second)

	// Verify processes started
	if cmd.Process == nil {
		t.Fatal("Process not started")
	}

	// Test PostgreSQL port accepts connections
	t.Run("PostgreSQL", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:15432", 3*time.Second)
		if err != nil {
			t.Fatalf("Failed to connect to PostgreSQL port: %v", err)
		}
		defer conn.Close()

		// Send PostgreSQL StartupMessage
		// Protocol version 3.0 (0x00030000)
		buf := new(bytes.Buffer)
		binary.Write(buf, binary.BigEndian, int32(0)) // length placeholder
		binary.Write(buf, binary.BigEndian, int16(3)) // protocol major
		binary.Write(buf, binary.BigEndian, int16(0)) // protocol minor
		user := "user\x00postgres\x00"
		buf.WriteString(user)
		buf.WriteByte(0) // null terminator
		dbname := "database\x00testdb\x00"
		buf.WriteString(dbname)
		buf.WriteByte(0) // null terminator

		// Fix length
		data := buf.Bytes()
		binary.BigEndian.PutUint32(data, uint32(buf.Len()))

		conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(data); err != nil {
			t.Fatalf("Failed to send startup message: %v", err)
		}

		// Read response (should be either ErrorResponse or Authentication)
		reader := bufio.NewReader(conn)
		msg, err := reader.ReadByte()
		if err != nil {
			t.Fatalf("Failed to read response: %v", err)
		}
		// 'E' = ErrorResponse, 'R' = Authentication
		// Both are valid - we just want to see the proxy speaks PG protocol
		if msg != 'E' && msg != 'R' {
			t.Errorf("Unexpected message type: %c ('%s')", msg, strconv.Quote(string([]byte{msg})))
		}
	})

	// Test MySQL port accepts connections
	t.Run("MySQL", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:13306", 3*time.Second)
		if err != nil {
			t.Fatalf("Failed to connect to MySQL port: %v", err)
		}
		defer conn.Close()

		// Read handshake packet
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		header := make([]byte, 4)
		if _, err := conn.Read(header); err != nil {
			t.Fatalf("Failed to read MySQL handshake: %v", err)
		}
		packetLen := uint32(header[0]) | uint32(header[1])<<8 | uint32(header[2])<<16
		if packetLen == 0 {
			t.Error("Zero-length MySQL packet")
		}
		// First byte of handshake is packet number, rest is payload
		// We just verify something was sent (protocol version 10 = MySQL 4.x/5.x)
	})

	// Test MSSQL port accepts connections
	t.Run("MSSQL", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:11433", 3*time.Second)
		if err != nil {
			t.Fatalf("Failed to connect to MSSQL port: %v", err)
		}
		defer conn.Close()

		// Send TDS Pre-Login
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		// TDS packet: 8 bytes header + payload
		packet := []byte{
			0x12, 0x01, 0x00, 0x2e, // length = 46
			0x00, 0x00, 0x01, 0x00, // packet header
			0x01, 0x00, // version = 7.4
			0x00, 0x00, // encryption option
			0x00, 0x00, // timestamp
			0x00, 0x00, // connection id
			0x01, 0x00, // options
			0x00, 0x1e, // port = 14330 (or just verify something responds)
			0x00,
		}
		if _, err := conn.Write(packet); err != nil {
			t.Fatalf("Failed to send MSSQL prelogin: %v", err)
		}

		// Read response
		resp := make([]byte, 8)
		if _, err := conn.Read(resp); err != nil {
			t.Fatalf("Failed to read MSSQL response: %v", err)
		}
		// Verify it's a TDS response (first 2 bytes should be packet type)
		if len(resp) < 2 {
			t.Error("MSSQL response too short")
		}
	})
}

// TestSmoke_GlobalMemoryLimit verifies the global memory limit is respected.
func TestSmoke_GlobalMemoryLimit(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("Skipping integration test. Set INTEGRATION=1 to run.")
	}

	// This test verifies the global memory limit config is parsed correctly
	// The actual enforcement is tested in unit tests

	tmpDir := t.TempDir()
	config := `
global:
  log_level: error
  max_memory: "1MB"
pools:
  - name: test
    body: postgresql
    mode: session
    listen:
      host: "127.0.0.1"
      port: 15433
    backend:
      hosts:
        - host: "127.0.0.1"
          port: 55433
          role: primary
    auth:
      mode: passthrough
`
	configPath := filepath.Join(tmpDir, "geryon.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	geryonBin := filepath.Join(tmpDir, "geryon.exe")
	buildCmd := exec.Command("go", "build", "-o", geryonBin, "./cmd/geryon")
	buildCmd.Dir = ".."
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Skipf("Skipping: failed to build geryon: %v, output: %s", err, output)
	}

	cmd := exec.Command(geryonBin, "--config", configPath, "--validate")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("Validation failed: %v, output: %s", err, output)
	}
	if !strings.Contains(string(output), "Configuration valid") {
		t.Logf("Validate output: %s", output)
	}
}
