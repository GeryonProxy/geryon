package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/GeryonProxy/geryon/internal/config"
)

// LoadServerConfig loads TLS configuration for server (listener).
func LoadServerConfig(cfg config.TLSConfig) (*tls.Config, error) {
	if cfg.Mode == "disable" || cfg.Mode == "" {
		return nil, nil
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Load certificate and key
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load server certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// Configure client authentication
	switch cfg.ClientAuth {
	case "require":
		tlsConfig.ClientAuth = tls.RequireAnyClientCert
	case "verify":
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	case "optional":
		tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
	default:
		tlsConfig.ClientAuth = tls.NoClientCert
	}

	// Load CA for client certificate verification
	if cfg.CAFile != "" && (cfg.ClientAuth == "verify" || cfg.ClientAuth == "optional") {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.ClientCAs = caCertPool
	}

	return tlsConfig, nil
}

// LoadClientConfig loads TLS configuration for client (backend connections).
func LoadClientConfig(cfg config.TLSConfig) (*tls.Config, error) {
	if cfg.Mode == "disable" || cfg.Mode == "" {
		return nil, nil
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Configure server certificate verification
	switch cfg.Mode {
	case "require":
		tlsConfig.InsecureSkipVerify = true
	case "verify-ca":
		tlsConfig.InsecureSkipVerify = false
	case "verify-full":
		tlsConfig.InsecureSkipVerify = false
		// Server name will be set per connection
	}

	// Load client certificate for mutual TLS
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// Load CA for server certificate verification
	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}

	return tlsConfig, nil
}
