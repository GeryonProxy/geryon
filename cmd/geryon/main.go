package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/GeryonProxy/geryon/internal/api/rest"
	"github.com/GeryonProxy/geryon/internal/auth"
	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/pool"
	"github.com/GeryonProxy/geryon/internal/proxy"
)

var version = "dev"

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

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid config: %v\n", err)
		os.Exit(1)
	}

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

	// Create user database
	userDB := auth.NewUserDatabase()
	if err := userDB.LoadFromConfig(&cfg.Auth); err != nil {
		log.Error("Failed to load user database", "error", err)
		os.Exit(1)
	}

	// Create pool manager
	poolMgr := pool.NewManager(log)

	// Create pools from config
	for _, poolCfg := range cfg.Pools {
		if err := poolMgr.CreatePool(&poolCfg); err != nil {
			log.Error("Failed to create pool", "name", poolCfg.Name, "error", err)
			os.Exit(1)
		}
	}

	// Create and start proxy listeners
	listeners := make([]*proxy.Listener, 0, len(cfg.Pools))
	for _, poolCfg := range cfg.Pools {
		p := poolMgr.GetPool(poolCfg.Name)
		if p == nil {
			continue
		}

		listener, err := proxy.NewListener(p, &poolCfg, p.Codec(), userDB, log)
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
	restServer, err := rest.NewServer(&cfg.Admin.REST, poolMgr, log)
	if err != nil {
		log.Error("Failed to create REST server", "error", err)
		os.Exit(1)
	}

	if err := restServer.Start(); err != nil {
		log.Error("Failed to start REST server", "error", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		for sig := range sigCh {
			switch sig {
			case syscall.SIGHUP:
				log.Info("Received SIGHUP, reloading configuration")
				// TODO: Implement hot reload
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

	// Close pools
	if err := poolMgr.Close(); err != nil {
		log.Error("Failed to close pool manager", "error", err)
	}

	log.Info("Geryon shutdown complete")
}

func generatePasswordHash() error {
	fmt.Println("Enter password: ")
	// TODO: Implement SCRAM-SHA-256 hash generation
	return fmt.Errorf("not yet implemented")
}

func generateSelfSignedCert() error {
	// TODO: Implement self-signed certificate generation
	return fmt.Errorf("not yet implemented")
}
