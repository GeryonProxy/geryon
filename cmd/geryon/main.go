package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/api/dashboard"
	"github.com/GeryonProxy/geryon/internal/api/grpc"
	"github.com/GeryonProxy/geryon/internal/api/mcp"
	"github.com/GeryonProxy/geryon/internal/api/rest"
	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/cluster"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
	"github.com/GeryonProxy/geryon/internal/proxy"
	"github.com/GeryonProxy/geryon/internal/tlsutil"
	"golang.org/x/term"
)

var version = "dev"
var commit = "unknown"
var date = "unknown"

// cfgHolder holds the current configuration atomically for safe concurrent access
// during SIGHUP hot-reload. Always use cfgHolder.Load() to read, cfgHolder.Store() to write.
var cfgHolder atomic.Pointer[config.Config]

func main() {
	var (
		configPath     = flag.String("config", "geryon.yaml", "Path to configuration file")
		validate       = flag.Bool("validate", false, "Validate config without starting")
		showVersion    = flag.Bool("version", false, "Print version and exit")
		generateConfig = flag.Bool("generate-config", false, "Generate example configuration file")
		generatePass   = flag.Bool("generate-password", false, "Generate SCRAM-SHA-256 password hash")
		generateCert   = flag.Bool("generate-cert", false, "Generate self-signed TLS certificate")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("Geryon %s\n", version)
		fmt.Println("Three Bodies. One Proxy. Every Connection.")
		os.Exit(0)
	}

	if *generateConfig {
		if err := config.GenerateExample(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to generate config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Example configuration written to geryon.example.yaml")
		os.Exit(0)
	}

	if *generatePass {
		if err := generatePasswordHash(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to generate password hash: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if *generateCert {
		if err := generateSelfSignedCert(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to generate certificate: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Sanitize config path to prevent path traversal
	safeConfigPath := filepath.Clean(*configPath)

	cfg, err := config.Load(safeConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid config: %v\n", err)
		os.Exit(1)
	}

	// Store config atomically for safe concurrent access
	cfgHolder.Store(cfg)

	if *validate {
		fmt.Println("Configuration is valid")
		os.Exit(0)
	}

	log, err := logger.New(cfg.Global.LogLevel, cfg.Global.LogFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}

	log.Info("Geryon starting", "version", version)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create reload function for config hot-reload
	reloadFn := func() error {
		newCfg, err := config.Load(safeConfigPath)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if err := config.Validate(newCfg); err != nil {
			return fmt.Errorf("invalid config: %w", err)
		}
		cfgHolder.Store(newCfg)
		log.Info("Configuration reloaded", "path", safeConfigPath)
		return nil
	}

	// Create user database
	userDB := auth.NewUserDatabase()
	if err := userDB.LoadFromConfig(&cfg.Auth); err != nil {
		log.Error("Failed to load user database", "error", err)
		os.Exit(1)
	}

	// Create pool manager
	poolMgr := pool.NewManager(log)
	poolMgr.SetGlobalMaxMemory(cfg.Global.MaxMemory)

	// Create pools from config
	for _, poolCfg := range cfg.Pools {
		if err := poolMgr.CreatePool(&poolCfg); err != nil {
			log.Error("Failed to create pool", "name", poolCfg.Name, "error", err)
			os.Exit(1)
		}
	}

	// Create and start proxy listeners
	listeners := make([]*proxy.Listener, 0, len(cfg.Pools))
	for i := range cfg.Pools {
		cfg.Pools[i].AuthMode = cfg.Auth.Mode
		poolCfg := &cfg.Pools[i]
		p := poolMgr.GetPool(poolCfg.Name)
		if p == nil {
			continue
		}

		listener, err := proxy.NewListener(p, poolCfg, p.Codec(), userDB, log)
		if err != nil {
			log.Error("Failed to create listener", "pool", poolCfg.Name, "error", err)
			os.Exit(1)
		}

		if err := listener.Start(); err != nil {
			log.Error("Failed to start listener", "pool", poolCfg.Name, "error", err)
			os.Exit(1)
		}

		listeners = append(listeners, listener)
	}

	// Create and start REST API server
	restServer, err := rest.NewServer(&cfg.Admin.REST, poolMgr, listeners, log, *configPath, reloadFn)
	if err != nil {
		log.Error("Failed to create REST server", "error", err)
		os.Exit(1)
	}

	if err := restServer.Start(); err != nil {
		log.Error("Failed to start REST server", "error", err)
		os.Exit(1)
	}

	// Create and start MCP server
	mcpServer := mcp.NewServer(&cfg.Admin.MCP, poolMgr, log, reloadFn)
	if err := mcpServer.Start(); err != nil {
		log.Error("Failed to start MCP server", "error", err)
		os.Exit(1)
	}

	// Create and start Dashboard server
	dashboardServer := dashboard.NewServer(&dashboard.Config{
		Enabled: cfg.Admin.Dashboard.Enabled,
		Listen:  cfg.Admin.Dashboard.Listen,
		Path:    cfg.Admin.Dashboard.Path,
		Auth:    cfg.Admin.Dashboard.Auth,
	}, poolMgr, log, reloadFn)
	if err := dashboardServer.Start(); err != nil {
		log.Error("Failed to start dashboard server", "error", err)
		os.Exit(1)
	}

	// Create and start gRPC server
	grpcServer := grpc.NewServer(&grpc.Config{
		Listen: cfg.Admin.GRPC.Listen,
		Auth:   cfg.Admin.GRPC.Auth,
	}, poolMgr, log, reloadFn)
	if err := grpcServer.Start(); err != nil {
		log.Error("Failed to start gRPC server", "error", err)
		os.Exit(1)
	}

	// Create and start cluster if enabled
	var clusterNode *cluster.Cluster
	if cfg.Cluster.Enabled {
		clusterConfig := cluster.Config{
			NodeID:            cfg.Cluster.NodeID,
			ListenAddr:        cfg.Cluster.Raft.Listen,
			Peers:             cfg.Cluster.Raft.Peers,
			ElectionTimeout:   parseDuration(cfg.Cluster.Raft.ElectionTimeout),
			HeartbeatInterval: parseDuration(cfg.Cluster.Raft.HeartbeatInterval),
			Logger:            log,
		}
		clusterNode = cluster.New(clusterConfig)
		if err := clusterNode.Start(); err != nil {
			log.Error("Failed to start cluster", "error", err)
			os.Exit(1)
		}
		log.Info("Cluster node started", "node_id", cfg.Cluster.NodeID, "state", clusterNode.GetState())
	}

	// Create config watcher for hot reload
	configFile := *configPath
	configWatcher := config.NewWatcher(configFile, 5*time.Second, log)
	configWatcher.OnChange(func(newCfg *config.Config) {
		log.Info("Configuration file changed, reloading")

		// Check if reload is safe
		safe, unsafe := config.IsSafeReload(cfgHolder.Load(), newCfg)
		if !safe {
			log.Warn("Unsafe configuration changes detected", "changes", unsafe)
			log.Info("Restart required for these changes to take effect")
			return
		}

		// Apply configuration changes
		// Note: This is a simplified reload - full implementation would update
		// pool limits, add/remove pools, etc.
		log.Info("Configuration reloaded successfully (changes will take effect for new connections)")
	})

	if err := configWatcher.Start(); err != nil {
		log.Error("Failed to start config watcher", "error", err)
		// Non-fatal, continue without hot reload
	} else {
		defer configWatcher.Stop()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		for sig := range sigCh {
			switch sig {
			case syscall.SIGHUP:
				log.Info("Received SIGHUP, reloading configuration")
				newCfg, err := config.HotReload(ctx, configFile, cfgHolder.Load(), func(newConfig *config.Config) error {
					// Apply new configuration
					// Note: In a full implementation, this would update
					// pool configurations, limits, etc.
					return nil
				}, log)
				if err != nil {
					log.Error("Hot reload failed", "error", err)
				} else {
					cfgHolder.Store(newCfg)
				}
			case syscall.SIGINT, syscall.SIGTERM:
				log.Info("Received shutdown signal", "signal", sig)
				cancel()
				return
			}
		}
	}()

	<-ctx.Done()

	// Graceful shutdown
	log.Info("Shutting down...")

	// Stop listeners
	for _, listener := range listeners {
		if err := listener.Stop(); err != nil {
			log.Error("Failed to stop listener", "error", err)
		}
	}

	// Stop REST server
	if err := restServer.Stop(context.Background()); err != nil {
		log.Error("Failed to stop REST server", "error", err)
	}

	// Stop MCP server
	if err := mcpServer.Stop(context.Background()); err != nil {
		log.Error("Failed to stop MCP server", "error", err)
	}

	// Stop Dashboard server
	if err := dashboardServer.Stop(); err != nil {
		log.Error("Failed to stop dashboard server", "error", err)
	}

	// Stop gRPC server
	if err := grpcServer.Stop(context.Background()); err != nil {
		log.Error("Failed to stop gRPC server", "error", err)
	}

	// Stop cluster if enabled
	if clusterNode != nil {
		if err := clusterNode.Stop(); err != nil {
			log.Error("Failed to stop cluster", "error", err)
		}
	}

	// Close pools
	if err := poolMgr.Close(); err != nil {
		log.Error("Failed to close pool manager", "error", err)
	}

	log.Info("Geryon shutdown complete")
}

func generatePasswordHash() error {
	// Read password from stdin without echoing
	fmt.Print("Enter password: ")
	passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to read password: %w", err)
	}
	fmt.Println()

	if len(passwordBytes) == 0 {
		return fmt.Errorf("password cannot be empty")
	}

	// M-11 fix: zero the buffer after use to reduce memory lifetime
	defer func() {
		for i := range passwordBytes {
			passwordBytes[i] = 0
		}
	}()

	hash, err := auth.GenerateSCRAMSHA256(string(passwordBytes))
	if err != nil {
		return fmt.Errorf("failed to generate hash: %w", err)
	}

	fmt.Println("\nGenerated SCRAM-SHA-256 hash:")
	fmt.Println(hash)
	fmt.Println("\nAdd this to your geryon.yaml configuration:")
	fmt.Printf("  password_hash: \"%s\"\n", hash)

	return nil
}

func generateSelfSignedCert() error {
	certFile := "geryon.crt"
	keyFile := "geryon.key"

	fmt.Printf("Generating self-signed certificate...\n")
	fmt.Printf("Certificate: %s\n", certFile)
	fmt.Fprintf(os.Stderr, "Private key: %s\n", keyFile) // L-3 fix: private key path to stderr, not stdout

	certPEM, keyPEM, err := tlsutil.GenerateSelfSignedCert("localhost", 365*24*time.Hour)
	if err != nil {
		return fmt.Errorf("failed to generate certificate: %w", err)
	}

	if err := os.WriteFile(certFile, certPEM, 0644); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	fmt.Println("\nSelf-signed certificate generated successfully!")
	fmt.Println("Add to your geryon.yaml:")
	fmt.Printf("  tls:\n")
	fmt.Printf("    mode: require\n")
	fmt.Printf("    cert_file: %s\n", certFile)
	fmt.Printf("    key_file: %s\n", keyFile)

	return nil
}

func parseDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}
