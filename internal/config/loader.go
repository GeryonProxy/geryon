package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// envVarPattern matches ${VAR} or ${VAR:-default} syntax.
var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// stripComment removes inline comments that are outside quoted values.
// A # inside "..." or '...' is preserved as part of the value.
func stripComment(s string) string {
	inDoubleQuote := false
	inSingleQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
		} else if c == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
		} else if c == '#' && !inDoubleQuote && !inSingleQuote && i > 0 && s[i-1] == ' ' {
			return s[:i]
		}
	}
	return s
}

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

	// Parse YAML using the custom parser
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

// parserState represents the current parsing state
type parserState struct {
	cfg            *Config
	currentSection []string
	currentPool    *PoolConfig
	currentBackend *BackendHost
	currentUser    *User
	currentRule    *CacheRule
	inList         bool
	parseError     string // first parse error with line number
}

// parseYAML parses YAML content into Config.
// Uses gopkg.in/yaml.v3 for standard YAML parsing with full spec support.
// Starts with DefaultConfig to preserve legacy behavior (auth enabled by default).
func parseYAML(content string) (*Config, error) {
	cfg := DefaultConfig() // Start with defaults (auth enabled by default)

	// Override with YAML content
	if err := yaml.Unmarshal([]byte(content), cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Set defaults for any remaining unspecified fields
	setDefaults(cfg)

	return cfg, nil
}

// setDefaults applies default values to unspecified fields.
func setDefaults(cfg *Config) {
	if cfg == nil {
		return
	}

	// Set global defaults
	if cfg.Global.LogLevel == "" {
		cfg.Global.LogLevel = "info"
	}
	if cfg.Global.LogFormat == "" {
		cfg.Global.LogFormat = "json"
	}

	// Ensure slices are non-nil (yaml.v3 unmarshal sets them to empty slices)
	if cfg.Auth.Users == nil {
		cfg.Auth.Users = []User{}
	}
	if cfg.Pools == nil {
		cfg.Pools = []PoolConfig{}
	}

	// Set admin defaults - only set listen addresses, don't force auth enabled
	if cfg.Admin.REST.Listen == "" {
		cfg.Admin.REST.Listen = "127.0.0.1:8080"
	}

	if cfg.Admin.GRPC.Listen == "" {
		cfg.Admin.GRPC.Listen = "127.0.0.1:9090"
	}

	if cfg.Admin.MCP.Listen == "" {
		cfg.Admin.MCP.Listen = "127.0.0.1:8081"
	}
	if cfg.Admin.MCP.Transport == "" {
		cfg.Admin.MCP.Transport = "sse"
	}

	if cfg.Admin.Dashboard.Listen == "" {
		cfg.Admin.Dashboard.Listen = "127.0.0.1:8082"
	}

	// Set pool defaults
	for i := range cfg.Pools {
		setPoolDefaults(&cfg.Pools[i])
	}
}

// setPoolDefaults applies defaults to a pool config.
func setPoolDefaults(pool *PoolConfig) {
	if pool == nil {
		return
	}

	if pool.Mode == "" {
		pool.Mode = "transaction"
	}
	if pool.Listen.Host == "" {
		pool.Listen.Host = "0.0.0.0"
	}
	if pool.Listen.Port == 0 {
		pool.Listen.Port = 5432
	}
	if pool.Limits.MaxClientConnections == 0 {
		pool.Limits.MaxClientConnections = 10000
	}
	if pool.Limits.MaxServerConnections == 0 {
		pool.Limits.MaxServerConnections = 100
	}
	if pool.Limits.MinServerConnections == 0 {
		pool.Limits.MinServerConnections = 5
	}
	if pool.Limits.IdleTransactionTimeout == "" {
		pool.Limits.IdleTransactionTimeout = "30m"
	}
	if pool.Health.CheckInterval == "" {
		pool.Health.CheckInterval = "10s"
	}
	if pool.Health.MaxFailures == 0 {
		pool.Health.MaxFailures = 3
	}
	if pool.Cache.DefaultTTL == "" {
		pool.Cache.DefaultTTL = "5m"
	}
	if pool.Transaction.Timeout == "" {
		pool.Transaction.Timeout = "30m"
	}
	if pool.Transaction.IdleTimeout == "" {
		pool.Transaction.IdleTimeout = "5m"
	}
	if pool.Transaction.CheckInterval == "" {
		pool.Transaction.CheckInterval = "30s"
	}
}

// parseLine parses a single YAML line.
func parseLine(state *parserState, line string, lineNum int) error {
	trimmed := strings.TrimSpace(line)

	// Skip empty lines and full-line comments
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return nil
	}

	// Calculate indent level (number of leading spaces)
	indent := len(line) - len(strings.TrimLeft(line, " \t"))

	// Remove inline comments (respecting quoted values)
	trimmed = stripComment(trimmed)
	trimmed = strings.TrimSpace(trimmed)

	// Determine section from indent
	sectionDepth := indent / 2
	if sectionDepth < len(state.currentSection) {
		state.currentSection = state.currentSection[:sectionDepth]
	}

	// Check if this is a list item
	if strings.HasPrefix(trimmed, "- ") {
		return parseListItem(state, trimmed, indent, lineNum)
	}

	// Parse key-value pairs
	return parseKeyValue(state, trimmed, indent, lineNum)
}

// parseListItem parses a list item (- item).
func parseListItem(state *parserState, line string, indent int, lineNum int) error {
	// Extract the value after "- "
	content := strings.TrimPrefix(line, "- ")
	content = strings.TrimSpace(content)

	// Check for key: value format in list item
	if strings.Contains(content, ":") {
		return parseKeyValue(state, content, indent, lineNum)
	}

	// Simple list item value
	return parseSimpleListItem(state, content, indent, lineNum)
}

// parseSimpleListItem parses a simple list item value.
func parseSimpleListItem(state *parserState, value string, indent int, lineNum int) error {
	parentSection := getParentSection(state.currentSection, indent/2)

	switch parentSection {
	case "cluster.raft.peers":
		state.cfg.Cluster.Raft.Peers = append(state.cfg.Cluster.Raft.Peers, value)
	case "cluster.gossip.join":
		state.cfg.Cluster.Gossip.Join = append(state.cfg.Cluster.Gossip.Join, value)
	case "auth.users":
		// Starting a new user - handled by key parsing
	case "pools":
		// Starting a new pool - handled by key parsing
	}

	return nil
}

// parseKeyValue parses a key: value line.
func parseKeyValue(state *parserState, line string, indent int, lineNum int) error {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 1 {
		return nil
	}

	key := strings.TrimSpace(parts[0])
	value := ""
	if len(parts) > 1 {
		value = strings.TrimSpace(parts[1])
		value = unquote(value)
	}

	// Track section hierarchy
	sectionDepth := indent / 2
	if sectionDepth >= len(state.currentSection) {
		state.currentSection = append(state.currentSection, key)
	} else {
		state.currentSection[sectionDepth] = key
		state.currentSection = state.currentSection[:sectionDepth+1]
	}

	// Handle section starts
	if value == "" {
		switch key {
		case "users":
			state.cfg.Auth.Users = []User{}
		case "pools":
			state.cfg.Pools = []PoolConfig{}
		}
		return nil
	}

	// Handle list items (starting with -)
	if strings.HasPrefix(line, "- ") {
		key = strings.TrimPrefix(key, "- ")
		key = strings.TrimSpace(key)
	}

	return assignValue(state, key, value, indent, lineNum)
}

// assignValue assigns a value to the appropriate configuration field.
func assignValue(state *parserState, key, value string, indent int, lineNum int) error {
	section := getCurrentSection(state.currentSection)
	parentSection := getParentSection(state.currentSection, indent/2)

	// Global settings
	if section == "global" || parentSection == "global" {
		switch key {
		case "log_level":
			state.cfg.Global.LogLevel = value
		case "log_format":
			state.cfg.Global.LogFormat = value
		case "pid_file":
			state.cfg.Global.PIDFile = value
		case "max_memory":
			state.cfg.Global.MaxMemory = value
		}
		return nil
	}

	// Admin settings
	if strings.HasPrefix(section, "admin") {
		return parseAdminValue(state, key, value, parentSection)
	}

	// Cluster settings
	if strings.HasPrefix(section, "cluster") {
		return parseClusterValue(state, key, value, parentSection)
	}

	// Auth settings
	if strings.HasPrefix(section, "auth") {
		return parseAuthValue(state, key, value, parentSection, indent)
	}

	// Pool settings
	if strings.HasPrefix(section, "pools") || strings.HasPrefix(parentSection, "pools") {
		return parsePoolValue(state, key, value, parentSection, indent)
	}

	return nil
}

// parseAdminValue parses admin configuration values.
func parseAdminValue(state *parserState, key, value, parent string) error {
	switch parent {
	case "admin.rest":
		switch key {
		case "listen":
			state.cfg.Admin.REST.Listen = value
		}
	case "admin.rest.auth":
		switch key {
		case "enabled":
			v, err := parseBool(value)
			if err != nil {
				return err
			}
			state.cfg.Admin.REST.Auth.Enabled = v
		case "token":
			state.cfg.Admin.REST.Auth.Token = value
		}
	case "admin.grpc":
		switch key {
		case "listen":
			state.cfg.Admin.GRPC.Listen = value
		}
	case "admin.grpc.auth":
		switch key {
		case "enabled":
			v, err := parseBool(value)
			if err != nil {
				return err
			}
			state.cfg.Admin.GRPC.Auth.Enabled = v
		case "token":
			state.cfg.Admin.GRPC.Auth.Token = value
		}
	case "admin.mcp":
		switch key {
		case "transport":
			state.cfg.Admin.MCP.Transport = value
		case "listen":
			state.cfg.Admin.MCP.Listen = value
		}
	case "admin.mcp.auth":
		switch key {
		case "enabled":
			v, err := parseBool(value)
			if err != nil {
				return err
			}
			state.cfg.Admin.MCP.Auth.Enabled = v
		case "token":
			state.cfg.Admin.MCP.Auth.Token = value
		}
	case "admin.dashboard":
		switch key {
		case "enabled":
			v, err := parseBool(value)
			if err != nil {
				return err
			}
			state.cfg.Admin.Dashboard.Enabled = v
		case "listen":
			state.cfg.Admin.Dashboard.Listen = value
		case "path":
			state.cfg.Admin.Dashboard.Path = value
		}
	case "admin.dashboard.auth":
		switch key {
		case "enabled":
			v, err := parseBool(value)
			if err != nil {
				return err
			}
			state.cfg.Admin.Dashboard.Auth.Enabled = v
		case "token":
			state.cfg.Admin.Dashboard.Auth.Token = value
		}
	}
	return nil
}

// parseClusterValue parses cluster configuration values.
func parseClusterValue(state *parserState, key, value, parent string) error {
	switch parent {
	case "cluster":
		switch key {
		case "enabled":
			v, err := parseBool(value)
			if err != nil {
				return err
			}
			state.cfg.Cluster.Enabled = v
		case "node_id":
			state.cfg.Cluster.NodeID = value
		}
	case "cluster.raft":
		switch key {
		case "listen":
			state.cfg.Cluster.Raft.Listen = value
		case "election_timeout":
			state.cfg.Cluster.Raft.ElectionTimeout = value
		case "heartbeat_interval":
			state.cfg.Cluster.Raft.HeartbeatInterval = value
		}
	case "cluster.raft.peers":
		if key != "peers" {
			state.cfg.Cluster.Raft.Peers = append(state.cfg.Cluster.Raft.Peers, value)
		}
	case "cluster.gossip":
		switch key {
		case "listen":
			state.cfg.Cluster.Gossip.Listen = value
		}
	case "cluster.gossip.join":
		if key != "join" {
			state.cfg.Cluster.Gossip.Join = append(state.cfg.Cluster.Gossip.Join, value)
		}
	}
	return nil
}

// parseAuthValue parses auth configuration values.
func parseAuthValue(state *parserState, key, value, parent string, indent int) error {
	if parent == "auth" {
		switch key {
		case "mode":
			state.cfg.Auth.Mode = value
		}
		return nil
	}

	if strings.HasPrefix(parent, "auth.users") || indent >= 4 {
		// Check if we're starting a new user
		if key == "username" && value != "" {
			state.cfg.Auth.Users = append(state.cfg.Auth.Users, User{})
			state.currentUser = &state.cfg.Auth.Users[len(state.cfg.Auth.Users)-1]
		}

		if state.currentUser != nil {
			switch key {
			case "username":
				state.currentUser.Username = value
			case "password_hash":
				state.currentUser.PasswordHash = value
			case "max_connections":
				state.currentUser.MaxConnections = parseInt(value)
			case "default_pool":
				state.currentUser.DefaultPool = value
			case "allowed_pools":
				state.currentUser.AllowedPools = parseStringArray(value)
			}
		}
	}

	return nil
}

// parsePoolValue parses pool configuration values.
func parsePoolValue(state *parserState, key, value, parent string, indent int) error {
	// Check if we're starting a new pool
	if key == "name" && value != "" && indent <= 4 {
		state.cfg.Pools = append(state.cfg.Pools, PoolConfig{})
		state.currentPool = &state.cfg.Pools[len(state.cfg.Pools)-1]
		state.currentPool.Name = value
		state.currentBackend = nil
		return nil
	}

	if state.currentPool == nil {
		return nil
	}

	pool := state.currentPool
	parentParts := strings.Split(parent, ".")
	lastPart := ""
	if len(parentParts) > 0 {
		lastPart = parentParts[len(parentParts)-1]
	}

	// Pool-level fields
	switch key {
	case "body":
		pool.Body = value
		return nil
	case "mode":
		pool.Mode = value
		return nil
	}

	// Listen settings
	if lastPart == "listen" || parent == "pools.listen" {
		switch key {
		case "host":
			pool.Listen.Host = value
		case "port":
			pool.Listen.Port = parseInt(value)
		}
		return nil
	}

	// Backend settings
	if lastPart == "backend" || strings.HasPrefix(parent, "pools.backend") {
		switch key {
		case "database":
			pool.Backend.Database = value
		}
		return nil
	}

	// Backend auth settings
	if lastPart == "auth" || strings.HasPrefix(parent, "pools.backend.auth") {
		switch key {
		case "method":
			pool.Backend.Auth.Method = value
		case "username":
			pool.Backend.Auth.Username = value
		case "password_file":
			pool.Backend.Auth.PasswordFile = value
		}
		return nil
	}

	// Backend hosts
	if key == "host" && strings.Contains(parent, "hosts") {
		pool.Backend.Hosts = append(pool.Backend.Hosts, BackendHost{})
		state.currentBackend = &pool.Backend.Hosts[len(pool.Backend.Hosts)-1]
		state.currentBackend.Host = value
		return nil
	}

	if state.currentBackend != nil && strings.Contains(parent, "hosts") {
		switch key {
		case "port":
			state.currentBackend.Port = parseInt(value)
		case "role":
			state.currentBackend.Role = value
		case "weight":
			state.currentBackend.Weight = parseInt(value)
		}
		return nil
	}

	// Limits
	if lastPart == "limits" || strings.HasPrefix(parent, "pools.limits") {
		switch key {
		case "max_client_connections":
			pool.Limits.MaxClientConnections = parseInt(value)
		case "max_server_connections":
			pool.Limits.MaxServerConnections = parseInt(value)
		case "min_server_connections":
			pool.Limits.MinServerConnections = parseInt(value)
		case "max_idle_time":
			pool.Limits.MaxIdleTime = value
		case "max_connection_lifetime":
			pool.Limits.MaxConnectionLifetime = value
		case "connection_timeout":
			pool.Limits.ConnectionTimeout = value
		case "query_timeout":
			pool.Limits.QueryTimeout = value
		case "idle_transaction_timeout":
			pool.Limits.IdleTransactionTimeout = value
		}
		return nil
	}

	// Health settings
	if lastPart == "health" || strings.HasPrefix(parent, "pools.health") {
		switch key {
		case "check_interval":
			pool.Health.CheckInterval = value
		case "check_query":
			pool.Health.CheckQuery = value
		case "max_failures":
			pool.Health.MaxFailures = parseInt(value)
		}
		return nil
	}

	// TLS settings
	if lastPart == "tls" || strings.HasPrefix(parent, "pools.tls") {
		switch key {
		case "mode":
			pool.TLS.Mode = value
		case "cert_file":
			pool.TLS.CertFile = value
		case "key_file":
			pool.TLS.KeyFile = value
		case "ca_file":
			pool.TLS.CAFile = value
		case "client_auth":
			pool.TLS.ClientAuth = value
		}
		return nil
	}

	// Cache settings
	if lastPart == "cache" || strings.HasPrefix(parent, "pools.cache") {
		switch key {
		case "enabled":
			v, err := parseBool(value)
			if err != nil {
				return err
			}
			pool.Cache.Enabled = v
		case "max_memory":
			pool.Cache.MaxMemory = value
		case "default_ttl":
			pool.Cache.DefaultTTL = value
		}
		return nil
	}

	// Routing settings
	if lastPart == "routing" || strings.HasPrefix(parent, "pools.routing") {
		switch key {
		case "read_write_split":
			v, err := parseBool(value)
			if err != nil {
				return err
			}
			pool.Routing.ReadWriteSplit = v
		}
		return nil
	}

	return nil
}

// Helper functions

func getCurrentSection(sections []string) string {
	return strings.Join(sections, ".")
}

func getParentSection(sections []string, depth int) string {
	if depth >= len(sections) {
		return strings.Join(sections, ".")
	}
	return strings.Join(sections[:depth], ".")
}

// unquote removes surrounding quotes and processes escape sequences.
// Handles double-quoted (") and single-quoted (') strings per YAML spec.
// Double-quoted strings process: \n, \t, \r, \\, \", \', \0, \xNN, \uNNNN
// Single-quoted strings treat backslash literally (except ”).
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			return unescapeValue(s[1 : len(s)-1])
		}
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			// Single-quoted: only '' -> ' is an escape
			return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
		}
	}
	return s
}

// unescapeValue processes escape sequences in double-quoted YAML strings.
func unescapeValue(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				i++
			case 't':
				b.WriteByte('\t')
				i++
			case 'r':
				b.WriteByte('\r')
				i++
			case '\\':
				b.WriteByte('\\')
				i++
			case '"':
				b.WriteByte('"')
				i++
			case '\'':
				b.WriteByte('\'')
				i++
			case '0':
				b.WriteByte(0)
				i++
			case 'x':
				if i+3 < len(s) {
					if v, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
						b.WriteByte(byte(v))
						i += 3
					} else {
						b.WriteByte('\\')
						b.WriteByte('x')
					}
				} else {
					b.WriteByte('\\')
					b.WriteByte('x')
				}
			case 'u':
				if i+5 < len(s) {
					if v, err := strconv.ParseUint(s[i+2:i+6], 16, 16); err == nil {
						b.WriteRune(rune(v))
						i += 5
					} else {
						b.WriteByte('\\')
						b.WriteByte('u')
					}
				} else {
					b.WriteByte('\\')
					b.WriteByte('u')
				}
			default:
				b.WriteByte('\\')
			}
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func parseBool(s string) (bool, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "true", "yes", "1", "on":
		return true, nil
	case "false", "no", "0", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value: %q", s)
	}
}

func parseInt(s string) int {
	s = strings.TrimSpace(s)
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func parseStringArray(s string) []string {
	// Parse ["item1", "item2"] or [item1, item2]
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return []string{s}
	}

	content := s[1 : len(s)-1]
	items := strings.Split(content, ",")
	result := make([]string, 0, len(items))

	for _, item := range items {
		item = strings.TrimSpace(item)
		item = unquote(item)
		if item != "" {
			result = append(result, item)
		}
	}

	return result
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

# Distributed tracing (OpenTelemetry OTLP)
# Requires otel collector running to receive traces
tracing:
  enabled: false
  exporter: otlpgrpc        # otlpgrpc (OTLP gRPC, requires otel-collector)
  endpoint: "localhost:4317" # OTLP receiver address
  sampling_rate: 1.0        # 0.0 to 1.0 (1.0 = trace everything)

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
      password_hash: "SCRAM-SHA-256$4096:salt:storedkey:serverkey"
      max_connections: 1000
      allowed_pools: ["*"]

pools:
  - name: "main-pg"
    body: postgresql
    mode: transaction
    listen:
      # M-6 fix: Use 127.0.0.1 for internal-only pools, 0.0.0.0 exposes on all interfaces
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
