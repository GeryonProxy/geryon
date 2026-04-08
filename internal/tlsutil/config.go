package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"sync"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
)

// Config holds TLS configuration.
type Config struct {
	mu           sync.RWMutex
	serverConfig *tls.Config
	clientConfig *tls.Config
	tlsMode      string
}

// NewConfig creates a new TLS configuration.
func NewConfig() *Config {
	return &Config{}
}

// LoadFromConfig loads TLS configuration from the config file.
func (c *Config) LoadFromConfig(cfg *config.TLSConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.tlsMode = cfg.Mode

	// If TLS is disabled, return early
	if cfg.Mode == "disable" {
		c.serverConfig = nil
		c.clientConfig = nil
		return nil
	}

	// Load certificates
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	// Create server config
	serverConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// Set client auth mode
	switch cfg.Mode {
	case "allow", "prefer":
		serverConfig.ClientAuth = tls.RequestClientCert
	case "require":
		serverConfig.ClientAuth = tls.RequireAnyClientCert
	case "verify-ca":
		serverConfig.ClientAuth = tls.VerifyClientCertIfGiven
	case "verify-full":
		serverConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	// Load CA certificate for client verification
	if cfg.CAFile != "" && cfg.Mode != "allow" && cfg.Mode != "prefer" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return fmt.Errorf("failed to load CA certificate: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return fmt.Errorf("failed to parse CA certificate")
		}

		serverConfig.ClientCAs = caCertPool
	}

	// Create client config
	clientConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.Mode == "allow" || cfg.Mode == "prefer",
	}

	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return fmt.Errorf("failed to load CA certificate: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return fmt.Errorf("failed to parse CA certificate")
		}

		clientConfig.RootCAs = caCertPool
	}

	c.serverConfig = serverConfig
	c.clientConfig = clientConfig

	return nil
}

// ServerConfig returns the TLS configuration for servers.
func (c *Config) ServerConfig() *tls.Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverConfig
}

// ClientConfig returns the TLS configuration for clients.
func (c *Config) ClientConfig() *tls.Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.clientConfig
}

// IsEnabled returns true if TLS is enabled.
func (c *Config) IsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverConfig != nil
}

// Mode returns the TLS mode.
func (c *Config) Mode() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tlsMode
}

// IsMTLS returns true if mutual TLS is required.
func (c *Config) IsMTLS() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tlsMode == "verify-ca" || c.tlsMode == "verify-full"
}

// GenerateSelfSignedCert generates a self-signed certificate for testing.
func GenerateSelfSignedCert(host string, validFor time.Duration) ([]byte, []byte, error) {
	// Generate private key
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Geryon"},
			CommonName:   host,
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(validFor),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{host, "localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	// Generate certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode certificate
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	// Encode private key
	privKeyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privKeyDER})

	return certPEM, keyPEM, nil
}

// CertInfo holds certificate information.
type CertInfo struct {
	Subject    string
	Issuer     string
	NotBefore  time.Time
	NotAfter   time.Time
	DNSNames   []string
	IPAddresses []string
	IsValid    bool
}

// ParseCertificateInfo parses certificate information.
func ParseCertificateInfo(certPEM []byte) (*CertInfo, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to parse certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	info := &CertInfo{
		Subject:     cert.Subject.String(),
		Issuer:      cert.Issuer.String(),
		NotBefore:   cert.NotBefore,
		NotAfter:    cert.NotAfter,
		DNSNames:    cert.DNSNames,
		IsValid:     time.Now().After(cert.NotBefore) && time.Now().Before(cert.NotAfter),
	}

	for _, ip := range cert.IPAddresses {
		info.IPAddresses = append(info.IPAddresses, ip.String())
	}

	return info, nil
}

// ValidateCertificate validates a certificate against requirements.
func ValidateCertificate(certPEM []byte, requireCN bool, allowedDNS []string) error {
	info, err := ParseCertificateInfo(certPEM)
	if err != nil {
		return err
	}

	if !info.IsValid {
		return fmt.Errorf("certificate is not valid (expired or not yet valid)")
	}

	if requireCN && info.Subject == "" {
		return fmt.Errorf("certificate missing Common Name")
	}

	if len(allowedDNS) > 0 {
		found := false
		for _, allowed := range allowedDNS {
			for _, dns := range info.DNSNames {
				if dns == allowed {
					found = true
					break
				}
			}
		}
		if !found {
			return fmt.Errorf("certificate DNS names do not match allowed list")
		}
	}

	return nil
}

// GetCertificateCN extracts the Common Name from a certificate.
func GetCertificateCN(cert *x509.Certificate) string {
	return cert.Subject.CommonName
}

// GetCertificateSANs extracts Subject Alternative Names from a certificate.
func GetCertificateSANs(cert *x509.Certificate) []string {
	sans := make([]string, 0)
	sans = append(sans, cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		sans = append(sans, ip.String())
	}
	return sans
}

// WriteCertificateToFile writes a certificate and key to files.
func WriteCertificateToFile(certPEM, keyPEM []byte, certPath, keyPath string) error {
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	return nil
}

// TLSMode represents the TLS mode.
type TLSMode int

const (
	ModeDisable TLSMode = iota
	ModeAllow
	ModePrefer
	ModeRequire
	ModeVerifyCA
	ModeVerifyFull
)

// ParseTLSMode parses a TLS mode string.
func ParseTLSMode(s string) (TLSMode, error) {
	switch s {
	case "disable":
		return ModeDisable, nil
	case "allow":
		return ModeAllow, nil
	case "prefer":
		return ModePrefer, nil
	case "require":
		return ModeRequire, nil
	case "verify-ca":
		return ModeVerifyCA, nil
	case "verify-full":
		return ModeVerifyFull, nil
	default:
		return ModeDisable, fmt.Errorf("unknown TLS mode: %s", s)
	}
}

// String returns the string representation of the TLS mode.
func (m TLSMode) String() string {
	switch m {
	case ModeAllow:
		return "allow"
	case ModePrefer:
		return "prefer"
	case ModeRequire:
		return "require"
	case ModeVerifyCA:
		return "verify-ca"
	case ModeVerifyFull:
		return "verify-full"
	default:
		return "disable"
	}
}

// CipherSuites returns recommended TLS cipher suites.
func CipherSuites() []uint16 {
	return []uint16{
		tls.TLS_AES_256_GCM_SHA384,
		tls.TLS_CHACHA20_POLY1305_SHA256,
		tls.TLS_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	}
}
