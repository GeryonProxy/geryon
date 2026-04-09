package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// envVarPattern matches ${VAR} or ${VAR:-default} syntax.
var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// allowedEnvPrefix lists prefixes of environment variables that can be expanded
// in config files. This prevents accidental exposure of system secrets.
var allowedEnvPrefix = "GERYON_"

// Load reads and parses the configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Expand environment variables (restricted to GERYON_* prefix)
	expanded := expandEnvVars(string(data))

	// Parse YAML
	cfg, err := parseYAML(expanded)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return cfg, nil
}

// expandEnvVars replaces ${VAR} and ${VAR:-default} with environment values.
// Only variables prefixed with GERYON_ are expanded for security.
// Other ${VAR} references are left as-is or replaced with their default.
func expandEnvVars(input string) string {
	return envVarPattern.ReplaceAllStringFunc(input, func(match string) string {
		content := match[2 : len(match)-1] // Remove ${ and }

		// Check for default value syntax
		parts := strings.SplitN(content, ":-", 2)
		varName := parts[0]

		// Only expand GERYON_* variables for security
		if !strings.HasPrefix(varName, allowedEnvPrefix) {
			if len(parts) > 1 {
				return parts[1]
			}
			return match // Leave non-GERYON vars as-is
		}

		value := os.Getenv(varName)
		if value == "" && len(parts) > 1 {
			return parts[1]
		}
		return value
	})
}

// parseYAML parses YAML content into Config.
func parseYAML(content string) (*Config, error) {
	// For now, this is a placeholder that returns default config.
	// Full YAML parser will be implemented separately.
	cfg := DefaultConfig()

	// Basic parsing of key sections
	lines := strings.Split(content, "\n")
	section := ""
	poolIndex := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Detect sections
		if !strings.HasPrefix(trimmed, "- ") && !strings.Contains(trimmed, "  ") {
			if s, ok := strings.CutSuffix(trimmed, ":"); ok {
				section = s
				if section == "pools" {
					cfg.Pools = []PoolConfig{}
					poolIndex = -1
				}
				continue
			}
		}

		// Parse pool entries
		if section == "pools" {
			if strings.HasPrefix(trimmed, "- name:") {
				poolIndex++
				cfg.Pools = append(cfg.Pools, PoolConfig{})
				name := strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:"))
				cfg.Pools[poolIndex].Name = name
			} else if poolIndex >= 0 {
				// Parse pool fields
				if err := parsePoolField(&cfg.Pools[poolIndex], trimmed); err != nil {
					return nil, fmt.Errorf("line %d: %w", i+1, err)
				}
			}
		}
	}

	return cfg, nil
}

// parsePoolField parses a single pool field.
func parsePoolField(pool *PoolConfig, line string) error {
	// Simple key-value parsing
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return nil // Ignore complex lines for now
	}

	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	switch key {
	case "body":
		pool.Body = value
	case "mode":
		pool.Mode = value
	case "host":
		pool.Listen.Host = value
	case "port":
		port, err := parseInt(value)
		if err != nil {
			return fmt.Errorf("invalid port: %w", err)
		}
		pool.Listen.Port = port
	}

	return nil
}

func parseInt(s string) (int, error) {
	var result int
	_, err := fmt.Sscanf(s, "%d", &result)
	return result, err
}

// GenerateExample creates an example configuration file.
func GenerateExample() error {
	example := `# Geryon — Multi-Database Connection Pooler
# Three Bodies. One Proxy. Every Connection.

global:
  log_level: info           # debug | info | warn | error
  log_format: json          # json | text
  pid_file: /var/run/geryon.pid

admin:
  rest:
    listen: "127.0.0.1:8080"
    auth:
      enabled: true
      token: "${GERYON_ADMIN_TOKEN}"
  grpc:
    listen: "127.0.0.1:9090"
  mcp:
    transport: sse           # stdio | sse
    listen: "127.0.0.1:8081"
  dashboard:
    enabled: true
    path: "/"               # Served on REST port

cluster:
  enabled: false
  node_id: "node-1"
  raft:
    listen: "0.0.0.0:7000"
    peers:
      - "node-2:7000"
      - "node-3:7000"
    election_timeout: "1s"
    heartbeat_interval: "150ms"
  gossip:
    listen: "0.0.0.0:7001"
    join:
      - "node-2:7001"
      - "node-3:7001"

auth:
  mode: interception         # passthrough | interception
  users:
    - username: "app"
      password: "SCRAM-SHA-256$4096:salt:storedkey:serverkey"
      max_connections: 1000
      allowed_pools: ["*"]

pools:
  - name: "main-pg"
    body: postgresql
    mode: transaction
    listen:
      host: "0.0.0.0"
      port: 5432
    backend:
      hosts:
        - host: "localhost"
          port: 5433
          role: primary
      database: "myapp"
      auth:
        username: "postgres"
        password_file: "/etc/geryon/secrets/pg"
    limits:
      max_client_connections: 10000
      max_server_connections: 100
      min_server_connections: 5
      max_idle_time: "300s"
      connection_timeout: "5s"
      query_timeout: "30s"
    tls:
      mode: prefer
    cache:
      enabled: false

  - name: "main-mysql"
    body: mysql
    mode: transaction
    listen:
      host: "0.0.0.0"
      port: 3306
    backend:
      hosts:
        - host: "localhost"
          port: 3307
          role: primary
      database: "myapp"
      auth:
        username: "root"
        password_file: "/etc/geryon/secrets/mysql"
    limits:
      max_client_connections: 5000
      max_server_connections: 50

  - name: "main-mssql"
    body: mssql
    mode: session
    listen:
      host: "0.0.0.0"
      port: 1433
    backend:
      hosts:
        - host: "localhost"
          port: 1434
          role: primary
      database: "myapp"
      auth:
        username: "sa"
        password_file: "/etc/geryon/secrets/mssql"
    limits:
      max_client_connections: 2000
      max_server_connections: 30
`
	return os.WriteFile("geryon.example.yaml", []byte(example), 0600)
}
