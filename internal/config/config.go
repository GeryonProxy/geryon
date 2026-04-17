// Package config handles configuration loading, validation, and file
// watching for the Geryon proxy. It defines all configuration structs
// (global, pools, auth, admin, cluster) and supports safe hot-reload
// detection to distinguish changes requiring a restart.
package config

import "fmt"

// GlobalConfig contains global application settings.
type GlobalConfig struct {
	LogLevel  string `yaml:"log_level"`
	LogFormat string `yaml:"log_format"`
	PIDFile   string `yaml:"pid_file"`
	MaxMemory string `yaml:"max_memory"`
}

// TLSConfig contains TLS settings.
type TLSConfig struct {
	Mode       string `yaml:"mode"`
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
	CAFile     string `yaml:"ca_file"`
	ClientAuth string `yaml:"client_auth"`
}

// BackendHost represents a single backend server.
type BackendHost struct {
	Host   string `yaml:"host"`
	Port   int    `yaml:"port"`
	Role   string `yaml:"role"`
	Weight int    `yaml:"weight"`
}

// BackendAuth contains authentication for backend connections.
type BackendAuth struct {
	Method       string `yaml:"method"`
	Username     string `yaml:"username"`
	PasswordFile string `yaml:"password_file"` // Read password from file for backend auth in interception mode
}

// BackendConfig contains backend connection settings.
type BackendConfig struct {
	Hosts    []BackendHost `yaml:"hosts"`
	Database string        `yaml:"database"`
	Auth     BackendAuth   `yaml:"auth"`
	TLS      TLSConfig     `yaml:"tls"`
}

// LimitConfig contains pool limit settings.
type LimitConfig struct {
	MaxClientConnections   int    `yaml:"max_client_connections"`
	MaxServerConnections   int    `yaml:"max_server_connections"`
	MinServerConnections   int    `yaml:"min_server_connections"`
	MaxIdleTime            string `yaml:"max_idle_time"`
	MaxConnectionLifetime  string `yaml:"max_connection_lifetime"`
	ConnectionTimeout      string `yaml:"connection_timeout"`
	QueryTimeout           string `yaml:"query_timeout"`
	IdleTransactionTimeout string `yaml:"idle_transaction_timeout"`
}

// HealthConfig contains health check settings.
type HealthConfig struct {
	CheckInterval string `yaml:"check_interval"`
	CheckQuery    string `yaml:"check_query"`
	MaxFailures   int    `yaml:"max_failures"`
}

// CacheRule represents a cache rule.
type CacheRule struct {
	Match      string `yaml:"match"`
	TTL        string `yaml:"ttl"`
	NeverCache bool   `yaml:"never_cache"` // If true, never cache matching queries
}

// CacheConfig contains query cache settings.
type CacheConfig struct {
	Enabled    bool        `yaml:"enabled"`
	MaxMemory  string      `yaml:"max_memory"`
	DefaultTTL string      `yaml:"default_ttl"`
	Rules      []CacheRule `yaml:"rules"`
}

// PreparedStmtConfig contains prepared statement cache settings.
type PreparedStmtConfig struct {
	Enabled bool   `yaml:"enabled"`
	MaxSize int    `yaml:"max_size"`
	TTL     string `yaml:"ttl"`
}

// RoutingRule represents a read/write routing rule.
type RoutingRule struct {
	Match    string `yaml:"match"`
	Target   string `yaml:"target"`
	Fallback string `yaml:"fallback"`
}

// RoutingConfig contains routing settings.
type RoutingConfig struct {
	ReadWriteSplit bool          `yaml:"read_write_split"`
	Rules          []RoutingRule `yaml:"rules"`
}

// TransactionConfig contains transaction manager settings.
type TransactionConfig struct {
	Timeout       string `yaml:"timeout"`        // e.g. "30m"
	IdleTimeout   string `yaml:"idle_timeout"`   // e.g. "5m"
	CheckInterval string `yaml:"check_interval"` // e.g. "30s"
}

// PoolConfig represents a single pool configuration.
type PoolConfig struct {
	Name         string             `yaml:"name"`
	Body         string             `yaml:"body"`
	Mode         string             `yaml:"mode"`
	Listen       ListenConfig       `yaml:"listen"`
	Backend      BackendConfig      `yaml:"backend"`
	Limits       LimitConfig        `yaml:"limits"`
	Health       HealthConfig       `yaml:"health"`
	TLS          TLSConfig          `yaml:"tls"`
	Cache        CacheConfig        `yaml:"cache"`
	PreparedStmt PreparedStmtConfig `yaml:"prepared_stmt"`
	Routing      RoutingConfig      `yaml:"routing"`
	Transaction  TransactionConfig  `yaml:"transaction"`
	AuthMode     string             `yaml:"auth_mode"` // "passthrough" or "interception"
}

// ListenConfig contains listener settings.
type ListenConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// User represents a proxy user.
type User struct {
	Username          string   `yaml:"username"`
	PasswordHash      string   `yaml:"password_hash"`       // SCRAM-SHA-256 (PostgreSQL)
	MysqlPasswordHash string   `yaml:"mysql_password_hash"` // SHA256(SHA256(password)) for MySQL
	MaxConnections    int      `yaml:"max_connections"`
	DefaultPool       string   `yaml:"default_pool"`
	AllowedPools      []string `yaml:"allowed_pools"`
}

// AuthConfig contains authentication settings.
type AuthConfig struct {
	Mode  string    `yaml:"mode"`
	Users []User    `yaml:"users"`
	TLS   TLSConfig `yaml:"tls"`
}

// RaftConfig contains Raft consensus settings.
type RaftConfig struct {
	Listen            string   `yaml:"listen"`
	Peers             []string `yaml:"peers"`
	ElectionTimeout   string   `yaml:"election_timeout"`
	HeartbeatInterval string   `yaml:"heartbeat_interval"`
}

// GossipConfig contains SWIM gossip protocol settings.
type GossipConfig struct {
	Listen string   `yaml:"listen"`
	Join   []string `yaml:"join"`
}

// ClusterConfig contains cluster settings.
type ClusterConfig struct {
	Enabled bool         `yaml:"enabled"`
	NodeID  string       `yaml:"node_id"`
	Raft    RaftConfig   `yaml:"raft"`
	Gossip  GossipConfig `yaml:"gossip"`
}

// AdminRESTConfig contains REST API settings.
type AdminRESTConfig struct {
	Listen         string         `yaml:"listen"`
	ReadTimeout    string         `yaml:"read_timeout"`
	WriteTimeout   string         `yaml:"write_timeout"`
	Auth           RESTAuthConfig `yaml:"auth"`
	AllowedOrigins []string       `yaml:"allowed_origins"`
}

// RESTAuthConfig contains authentication settings for admin APIs.
type RESTAuthConfig struct {
	Enabled bool   `yaml:"enabled"`
	Token   string `yaml:"token"`
}

// AdminGRPCConfig contains HTTP/2 API settings (streaming stats, not protobuf gRPC).
type AdminGRPCConfig struct {
	Listen       string         `yaml:"listen"`
	ReadTimeout  string         `yaml:"read_timeout"`
	WriteTimeout string         `yaml:"write_timeout"`
	Auth         RESTAuthConfig `yaml:"auth"`
}

// AdminMCPConfig contains MCP server settings.
type AdminMCPConfig struct {
	Transport    string         `yaml:"transport"`
	Listen       string         `yaml:"listen"`
	ReadTimeout  string         `yaml:"read_timeout"`
	WriteTimeout string         `yaml:"write_timeout"`
	Auth         RESTAuthConfig `yaml:"auth"`
}

// AdminDashboardConfig contains dashboard settings.
type AdminDashboardConfig struct {
	Enabled      bool           `yaml:"enabled"`
	Listen       string         `yaml:"listen"`
	Path         string         `yaml:"path"`
	ReadTimeout  string         `yaml:"read_timeout"`
	WriteTimeout string         `yaml:"write_timeout"`
	Auth         RESTAuthConfig `yaml:"auth"`
}

// AdminConfig contains management interface settings.
type AdminConfig struct {
	REST      AdminRESTConfig      `yaml:"rest"`
	GRPC      AdminGRPCConfig      `yaml:"grpc"`
	MCP       AdminMCPConfig       `yaml:"mcp"`
	Dashboard AdminDashboardConfig `yaml:"dashboard"`
}

// Config represents the complete Geryon configuration.
type Config struct {
	Global  GlobalConfig  `yaml:"global"`
	Admin   AdminConfig   `yaml:"admin"`
	Cluster ClusterConfig `yaml:"cluster"`
	Auth    AuthConfig    `yaml:"auth"`
	Pools   []PoolConfig  `yaml:"pools"`
}

// DefaultConfig returns a configuration with default values.
func DefaultConfig() *Config {
	return &Config{
		Global: GlobalConfig{
			LogLevel:  "info",
			LogFormat: "json",
		},
		Admin: AdminConfig{
			REST: AdminRESTConfig{
				Listen: "127.0.0.1:8080",
				Auth: RESTAuthConfig{
					Enabled: true,
				},
			},
			GRPC: AdminGRPCConfig{
				Listen: "127.0.0.1:9090",
				Auth: RESTAuthConfig{
					Enabled: true,
				},
			},
			MCP: AdminMCPConfig{
				Transport: "sse",
				Listen:    "127.0.0.1:8081",
				Auth: RESTAuthConfig{
					Enabled: true,
				},
			},
			Dashboard: AdminDashboardConfig{
				Enabled: false,
				Listen:  "127.0.0.1:8082",
				Path:    "/",
				Auth: RESTAuthConfig{
					Enabled: true,
				},
			},
		},
		Cluster: ClusterConfig{
			Enabled: false,
			NodeID:  "node-1",
			Raft: RaftConfig{
				ElectionTimeout:   "1s",
				HeartbeatInterval: "150ms",
			},
		},
		Auth: AuthConfig{
			Mode: "passthrough",
		},
		Pools: []PoolConfig{},
	}
}

// Validate checks the configuration for errors.
func Validate(cfg *Config) error {
	// Validate admin listen addresses
	for _, addr := range []string{cfg.Admin.REST.Listen, cfg.Admin.GRPC.Listen, cfg.Admin.MCP.Listen} {
		if addr == "" {
			return fmt.Errorf("admin listen address cannot be empty")
		}
	}

	// Validate auth configuration
	if cfg.Admin.REST.Auth.Enabled && cfg.Admin.REST.Auth.Token == "" {
		return fmt.Errorf("REST auth is enabled but no auth token is configured")
	}
	if cfg.Admin.GRPC.Auth.Enabled && cfg.Admin.GRPC.Auth.Token == "" {
		return fmt.Errorf("gRPC auth is enabled but no auth token is configured")
	}
	if cfg.Admin.MCP.Auth.Enabled && cfg.Admin.MCP.Auth.Token == "" {
		return fmt.Errorf("MCP auth is enabled but no auth token is configured")
	}
	if cfg.Admin.Dashboard.Auth.Enabled && cfg.Admin.Dashboard.Auth.Token == "" {
		return fmt.Errorf("Dashboard auth is enabled but no auth token is configured")
	}

	// Validate pool configurations
	poolNames := make(map[string]bool)
	ports := make(map[int]bool)

	for i, pool := range cfg.Pools {
		if pool.Name == "" {
			return fmt.Errorf("pool at index %d: name is required", i)
		}

		if poolNames[pool.Name] {
			return fmt.Errorf("duplicate pool name: %s", pool.Name)
		}
		poolNames[pool.Name] = true

		// Validate body type
		switch pool.Body {
		case "postgresql", "mysql", "mssql":
			// valid
		default:
			return fmt.Errorf("pool %s: invalid body type %q, must be postgresql, mysql, or mssql", pool.Name, pool.Body)
		}

		// Validate pool mode
		switch pool.Mode {
		case "session", "transaction", "statement":
			// valid
		default:
			return fmt.Errorf("pool %s: invalid mode %q, must be session, transaction, or statement", pool.Name, pool.Mode)
		}

		// Validate limits
		if pool.Limits.MaxClientConnections < 0 {
			return fmt.Errorf("pool %s: max_client_connections cannot be negative", pool.Name)
		}
		if pool.Limits.MaxServerConnections < 0 {
			return fmt.Errorf("pool %s: max_server_connections cannot be negative", pool.Name)
		}
		if pool.Limits.MinServerConnections < 0 {
			return fmt.Errorf("pool %s: min_server_connections cannot be negative", pool.Name)
		}

		// Check for port conflicts
		if pool.Listen.Port != 0 {
			if ports[pool.Listen.Port] {
				return fmt.Errorf("port %d is used by multiple pools", pool.Listen.Port)
			}
			ports[pool.Listen.Port] = true
		}
	}

	// Validate cluster configuration
	if cfg.Cluster.Enabled {
		if cfg.Cluster.NodeID == "" {
			return fmt.Errorf("cluster is enabled but node_id is not set")
		}
		if cfg.Cluster.Raft.Listen == "" {
			return fmt.Errorf("cluster is enabled but raft.listen is not set")
		}
	}

	return nil
}
