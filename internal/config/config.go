package config

import "fmt"

// GlobalConfig contains global application settings.
type GlobalConfig struct {
	LogLevel  string `yaml:"log_level"`
	LogFormat string `yaml:"log_format"`
	PIDFile   string `yaml:"pid_file"`
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
	PasswordFile string `yaml:"password_file"`
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
	Match string `yaml:"match"`
	TTL   string `yaml:"ttl"`
}

// CacheConfig contains query cache settings.
type CacheConfig struct {
	Enabled    bool        `yaml:"enabled"`
	MaxMemory  string      `yaml:"max_memory"`
	DefaultTTL string      `yaml:"default_ttl"`
	Rules      []CacheRule `yaml:"rules"`
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

// PoolConfig represents a single pool configuration.
type PoolConfig struct {
	Name    string        `yaml:"name"`
	Body    string        `yaml:"body"`
	Mode    string        `yaml:"mode"`
	Listen  ListenConfig  `yaml:"listen"`
	Backend BackendConfig `yaml:"backend"`
	Limits  LimitConfig   `yaml:"limits"`
	Health  HealthConfig  `yaml:"health"`
	TLS     TLSConfig     `yaml:"tls"`
	Cache   CacheConfig   `yaml:"cache"`
	Routing RoutingConfig `yaml:"routing"`
}

// ListenConfig contains listener settings.
type ListenConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// User represents a proxy user.
type User struct {
	Username       string   `yaml:"username"`
	PasswordHash   string   `yaml:"password_hash"`
	MaxConnections int      `yaml:"max_connections"`
	DefaultPool    string   `yaml:"default_pool"`
	AllowedPools   []string `yaml:"allowed_pools"`
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
	Listen string         `yaml:"listen"`
	Auth   RESTAuthConfig `yaml:"auth"`
}

// RESTAuthConfig contains REST API authentication settings.
type RESTAuthConfig struct {
	Enabled bool   `yaml:"enabled"`
	Token   string `yaml:"token"`
}

// AdminGRPCConfig contains gRPC API settings.
type AdminGRPCConfig struct {
	Listen string `yaml:"listen"`
}

// AdminMCPConfig contains MCP server settings.
type AdminMCPConfig struct {
	Transport string `yaml:"transport"`
	Listen    string `yaml:"listen"`
}

// AdminDashboardConfig contains dashboard settings.
type AdminDashboardConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
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
				Listen: "0.0.0.0:8080",
				Auth: RESTAuthConfig{
					Enabled: false,
				},
			},
			GRPC: AdminGRPCConfig{
				Listen: "0.0.0.0:9090",
			},
			MCP: AdminMCPConfig{
				Transport: "sse",
				Listen:    "0.0.0.0:8081",
			},
			Dashboard: AdminDashboardConfig{
				Enabled: true,
				Path:    "/",
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

		// Check for port conflicts
		if pool.Listen.Port != 0 {
			if ports[pool.Listen.Port] {
				return fmt.Errorf("port %d is used by multiple pools", pool.Listen.Port)
			}
			ports[pool.Listen.Port] = true
		}
	}

	return nil
}
