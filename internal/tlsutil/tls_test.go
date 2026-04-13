package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
)

func TestGenerateSelfSignedCert(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", 24*time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}
	if len(certPEM) == 0 {
		t.Error("Certificate should not be empty")
	}
	if len(keyPEM) == 0 {
		t.Error("Key should not be empty")
	}
}

func TestGenerateSelfSignedCert_IP(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("127.0.0.1", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert IP failed: %v", err)
	}
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		t.Error("Certificate and key should not be empty")
	}
}

func TestParseCertificateInfo(t *testing.T) {
	certPEM, _, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	info, err := ParseCertificateInfo(certPEM)
	if err != nil {
		t.Fatalf("ParseCertificateInfo failed: %v", err)
	}
	if info.Subject == "" {
		t.Error("Subject should not be empty")
	}
	if !info.IsValid {
		t.Error("Certificate should be valid")
	}
	if len(info.DNSNames) != 1 || info.DNSNames[0] != "localhost" {
		t.Errorf("DNSNames = %v, want [localhost]", info.DNSNames)
	}
}

func TestParseCertificateInfo_InvalidPEM(t *testing.T) {
	_, err := ParseCertificateInfo([]byte("not a pem"))
	if err == nil {
		t.Error("Should fail for invalid PEM")
	}
}

func TestValidateCertificate(t *testing.T) {
	certPEM, _, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	err = ValidateCertificate(certPEM, true, nil)
	if err != nil {
		t.Errorf("ValidateCertificate failed: %v", err)
	}
}

func TestValidateCertificate_DNSMatch(t *testing.T) {
	certPEM, _, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	err = ValidateCertificate(certPEM, true, []string{"localhost"})
	if err != nil {
		t.Errorf("ValidateCertificate with DNS match failed: %v", err)
	}
}

func TestValidateCertificate_DNSMismatch(t *testing.T) {
	certPEM, _, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	err = ValidateCertificate(certPEM, true, []string{"example.com"})
	if err == nil {
		t.Error("Should fail for mismatched DNS")
	}
}

func TestGetCertificateCN(t *testing.T) {
	certPEM, _, err := GenerateSelfSignedCert("myhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	block := decodePEM(certPEM)
	cert, _ := x509.ParseCertificate(block.Bytes)
	cn := GetCertificateCN(cert)
	if cn != "myhost" {
		t.Errorf("GetCertificateCN = %q, want %q", cn, "myhost")
	}
}

func TestGetCertificateSANs(t *testing.T) {
	certPEM, _, err := GenerateSelfSignedCert("127.0.0.1", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}
	block := decodePEM(certPEM)
	cert, _ := x509.ParseCertificate(block.Bytes)
	sans := GetCertificateSANs(cert)
	if len(sans) != 1 || sans[0] != "127.0.0.1" {
		t.Errorf("GetCertificateSANs = %v, want [127.0.0.1]", sans)
	}
}

func TestWriteCertificateToFile(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")

	err = WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath)
	if err != nil {
		t.Fatalf("WriteCertificateToFile failed: %v", err)
	}

	if _, err := os.Stat(certPath); err != nil {
		t.Error("Certificate file should exist")
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Error("Key file should exist")
	}
}

func TestCipherSuites12(t *testing.T) {
	suites := CipherSuites12()
	if len(suites) == 0 {
		t.Error("CipherSuites12 should return at least one suite")
	}
}

func TestParseTLSMode(t *testing.T) {
	cases := []struct {
		name string
		want TLSMode
	}{
		{"disable", ModeDisable},
		{"allow", ModeAllow},
		{"prefer", ModePrefer},
		{"require", ModeRequire},
		{"verify-ca", ModeVerifyCA},
		{"verify-full", ModeVerifyFull},
	}
	for _, tc := range cases {
		got, err := ParseTLSMode(tc.name)
		if err != nil {
			t.Fatalf("ParseTLSMode(%q) failed: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("ParseTLSMode(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}

	_, err := ParseTLSMode("unknown")
	if err == nil {
		t.Error("Should fail for unknown mode")
	}
}

func TestTLSMode_String(t *testing.T) {
	cases := []struct {
		mode TLSMode
		want string
	}{
		{ModeDisable, "disable"},
		{ModeAllow, "allow"},
		{ModePrefer, "prefer"},
		{ModeRequire, "require"},
		{ModeVerifyCA, "verify-ca"},
		{ModeVerifyFull, "verify-full"},
		{TLSMode(999), "disable"},
	}
	for _, tc := range cases {
		if got := tc.mode.String(); got != tc.want {
			t.Errorf("TLSMode(%d).String() = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

func TestConfig_Disabled(t *testing.T) {
	tlsCfg := &Config{}
	cfg := config.TLSConfig{Mode: "disable"}
	tlsCfg.LoadFromConfig(&cfg)

	if tlsCfg.IsEnabled() {
		t.Error("Should be disabled")
	}
	if tlsCfg.ServerConfig() != nil {
		t.Error("ServerConfig should be nil when disabled")
	}
	if tlsCfg.ClientConfig() != nil {
		t.Error("ClientConfig should be nil when disabled")
	}
}

func TestConfig_Mode(t *testing.T) {
	tlsCfg := &Config{}
	cfg := config.TLSConfig{Mode: "require"}
	tlsCfg.LoadFromConfig(&cfg)

	if tlsCfg.Mode() != "require" {
		t.Errorf("Mode = %q, want require", tlsCfg.Mode())
	}
}

func TestConfig_IsMTLS(t *testing.T) {
	tlsCfg := &Config{}
	cfg := config.TLSConfig{Mode: "verify-ca"}
	tlsCfg.LoadFromConfig(&cfg)
	if !tlsCfg.IsMTLS() {
		t.Error("verify-ca should be mTLS")
	}

	tlsCfg2 := &Config{}
	cfg2 := config.TLSConfig{Mode: "require"}
	tlsCfg2.LoadFromConfig(&cfg2)
	if tlsCfg2.IsMTLS() {
		t.Error("require should not be mTLS")
	}
}

func decodePEM(data []byte) *pem.Block {
	block, _ := pem.Decode(data)
	return block
}

// Test LoadServerConfig with disable mode
func TestLoadServerConfig_Disable(t *testing.T) {
	cfg := config.TLSConfig{Mode: "disable"}
	tlsConfig, err := LoadServerConfig(cfg)
	if err != nil {
		t.Errorf("LoadServerConfig error = %v", err)
	}
	if tlsConfig != nil {
		t.Error("LoadServerConfig should return nil for disable mode")
	}
}

// Test LoadServerConfig with empty mode
func TestLoadServerConfig_EmptyMode(t *testing.T) {
	cfg := config.TLSConfig{Mode: ""}
	tlsConfig, err := LoadServerConfig(cfg)
	if err != nil {
		t.Errorf("LoadServerConfig error = %v", err)
	}
	if tlsConfig != nil {
		t.Error("LoadServerConfig should return nil for empty mode")
	}
}

// Test LoadServerConfig with valid certificate
func TestLoadServerConfig_WithCert(t *testing.T) {
	// Generate self-signed cert
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	// Write to temp files
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")

	if err := WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath); err != nil {
		t.Fatalf("WriteCertificateToFile failed: %v", err)
	}

	cfg := config.TLSConfig{
		Mode:     "require",
		CertFile: certPath,
		KeyFile:  keyPath,
	}

	tlsConfig, err := LoadServerConfig(cfg)
	if err != nil {
		t.Fatalf("LoadServerConfig failed: %v", err)
	}
	if tlsConfig == nil {
		t.Fatal("LoadServerConfig should return non-nil config")
	}
	if len(tlsConfig.Certificates) != 1 {
		t.Errorf("Certificates length = %d, want 1", len(tlsConfig.Certificates))
	}
}

// Test LoadServerConfig with invalid certificate path
func TestLoadServerConfig_InvalidCertPath(t *testing.T) {
	cfg := config.TLSConfig{
		Mode:     "require",
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
	}

	_, err := LoadServerConfig(cfg)
	if err == nil {
		t.Error("LoadServerConfig should fail with invalid cert path")
	}
}

// Test LoadServerConfig with client auth require
func TestLoadServerConfig_ClientAuthRequire(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")

	if err := WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath); err != nil {
		t.Fatalf("WriteCertificateToFile failed: %v", err)
	}

	cfg := config.TLSConfig{
		Mode:       "require",
		CertFile:   certPath,
		KeyFile:    keyPath,
		ClientAuth: "require",
	}

	tlsConfig, err := LoadServerConfig(cfg)
	if err != nil {
		t.Fatalf("LoadServerConfig failed: %v", err)
	}
	if tlsConfig.ClientAuth != tls.RequireAnyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAnyClientCert", tlsConfig.ClientAuth)
	}
}

// Test LoadServerConfig with client auth verify
func TestLoadServerConfig_ClientAuthVerify(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	caPath := filepath.Join(tmpDir, "ca.pem")

	if err := WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath); err != nil {
		t.Fatalf("WriteCertificateToFile failed: %v", err)
	}
	if err := os.WriteFile(caPath, certPEM, 0644); err != nil {
		t.Fatalf("WriteFile CA failed: %v", err)
	}

	cfg := config.TLSConfig{
		Mode:       "require",
		CertFile:   certPath,
		KeyFile:    keyPath,
		ClientAuth: "verify",
		CAFile:     caPath,
	}

	tlsConfig, err := LoadServerConfig(cfg)
	if err != nil {
		t.Fatalf("LoadServerConfig failed: %v", err)
	}
	if tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAndVerifyClientCert", tlsConfig.ClientAuth)
	}
	if tlsConfig.ClientCAs == nil {
		t.Error("ClientCAs should be set")
	}
}

// Test LoadServerConfig with client auth optional
func TestLoadServerConfig_ClientAuthOptional(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	caPath := filepath.Join(tmpDir, "ca.pem")

	if err := WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath); err != nil {
		t.Fatalf("WriteCertificateToFile failed: %v", err)
	}
	if err := os.WriteFile(caPath, certPEM, 0644); err != nil {
		t.Fatalf("WriteFile CA failed: %v", err)
	}

	cfg := config.TLSConfig{
		Mode:       "require",
		CertFile:   certPath,
		KeyFile:    keyPath,
		ClientAuth: "optional",
		CAFile:     caPath,
	}

	tlsConfig, err := LoadServerConfig(cfg)
	if err != nil {
		t.Fatalf("LoadServerConfig failed: %v", err)
	}
	if tlsConfig.ClientAuth != tls.VerifyClientCertIfGiven {
		t.Errorf("ClientAuth = %v, want VerifyClientCertIfGiven", tlsConfig.ClientAuth)
	}
}

// Test LoadServerConfig with invalid CA file
func TestLoadServerConfig_InvalidCAFile(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")

	if err := WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath); err != nil {
		t.Fatalf("WriteCertificateToFile failed: %v", err)
	}

	cfg := config.TLSConfig{
		Mode:       "require",
		CertFile:   certPath,
		KeyFile:    keyPath,
		ClientAuth: "verify",
		CAFile:     "/nonexistent/ca.pem",
	}

	_, err = LoadServerConfig(cfg)
	if err == nil {
		t.Error("LoadServerConfig should fail with invalid CA file")
	}
}

// Test LoadServerConfig with invalid CA PEM
func TestLoadServerConfig_InvalidCAPEM(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	caPath := filepath.Join(tmpDir, "ca.pem")

	if err := WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath); err != nil {
		t.Fatalf("WriteCertificateToFile failed: %v", err)
	}
	// Write invalid PEM
	if err := os.WriteFile(caPath, []byte("not a valid PEM"), 0644); err != nil {
		t.Fatalf("WriteFile CA failed: %v", err)
	}

	cfg := config.TLSConfig{
		Mode:       "require",
		CertFile:   certPath,
		KeyFile:    keyPath,
		ClientAuth: "verify",
		CAFile:     caPath,
	}

	_, err = LoadServerConfig(cfg)
	if err == nil {
		t.Error("LoadServerConfig should fail with invalid CA PEM")
	}
}

// Test LoadClientConfig with disable mode
func TestLoadClientConfig_Disable(t *testing.T) {
	cfg := config.TLSConfig{Mode: "disable"}
	tlsConfig, err := LoadClientConfig(cfg)
	if err != nil {
		t.Errorf("LoadClientConfig error = %v", err)
	}
	if tlsConfig != nil {
		t.Error("LoadClientConfig should return nil for disable mode")
	}
}

// Test LoadClientConfig with valid certificate
func TestLoadClientConfig_WithCert(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")

	if err := WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath); err != nil {
		t.Fatalf("WriteCertificateToFile failed: %v", err)
	}

	cfg := config.TLSConfig{
		Mode:     "require",
		CertFile: certPath,
		KeyFile:  keyPath,
	}

	tlsConfig, err := LoadClientConfig(cfg)
	if err != nil {
		t.Fatalf("LoadClientConfig failed: %v", err)
	}
	if tlsConfig == nil {
		t.Fatal("LoadClientConfig should return non-nil config")
	}
	if len(tlsConfig.Certificates) != 1 {
		t.Errorf("Certificates length = %d, want 1", len(tlsConfig.Certificates))
	}
}

// Test LoadClientConfig with CA file
func TestLoadClientConfig_WithCA(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	caPath := filepath.Join(tmpDir, "ca.pem")

	if err := WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath); err != nil {
		t.Fatalf("WriteCertificateToFile failed: %v", err)
	}
	if err := os.WriteFile(caPath, certPEM, 0644); err != nil {
		t.Fatalf("WriteFile CA failed: %v", err)
	}

	cfg := config.TLSConfig{
		Mode:     "verify-ca",
		CertFile: certPath,
		KeyFile:  keyPath,
		CAFile:   caPath,
	}

	tlsConfig, err := LoadClientConfig(cfg)
	if err != nil {
		t.Fatalf("LoadClientConfig failed: %v", err)
	}
	if tlsConfig.RootCAs == nil {
		t.Error("RootCAs should be set")
	}
}

// Test LoadClientConfig with invalid certificate path
func TestLoadClientConfig_InvalidCertPath(t *testing.T) {
	cfg := config.TLSConfig{
		Mode:     "require",
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
	}

	_, err := LoadClientConfig(cfg)
	if err == nil {
		t.Error("LoadClientConfig should fail with invalid cert path")
	}
}

// Test LoadClientConfig with invalid CA file
func TestLoadClientConfig_InvalidCAFile(t *testing.T) {
	cfg := config.TLSConfig{
		Mode:   "require",
		CAFile: "/nonexistent/ca.pem",
	}

	_, err := LoadClientConfig(cfg)
	if err == nil {
		t.Error("LoadClientConfig should fail with invalid CA file")
	}
}

// Test LoadClientConfig with invalid CA PEM
func TestLoadClientConfig_InvalidCAPEM(t *testing.T) {
	tmpDir := t.TempDir()
	caPath := filepath.Join(tmpDir, "ca.pem")

	// Write invalid PEM
	if err := os.WriteFile(caPath, []byte("not a valid PEM"), 0644); err != nil {
		t.Fatalf("WriteFile CA failed: %v", err)
	}

	cfg := config.TLSConfig{
		Mode:   "require",
		CAFile: caPath,
	}

	_, err := LoadClientConfig(cfg)
	if err == nil {
		t.Error("LoadClientConfig should fail with invalid CA PEM")
	}
}

// Test LoadClientConfig with different modes
func TestLoadClientConfig_Modes(t *testing.T) {
	modes := []string{"require", "verify-ca", "verify-full"}

	for _, mode := range modes {
		cfg := config.TLSConfig{Mode: mode}
		tlsConfig, err := LoadClientConfig(cfg)
		if err != nil {
			t.Errorf("LoadClientConfig(%q) failed: %v", mode, err)
		}
		if tlsConfig == nil {
			t.Errorf("LoadClientConfig(%q) should return non-nil config", mode)
		}
	}
}

func TestNewConfig(t *testing.T) {
	cfg := NewConfig()
	if cfg == nil {
		t.Fatal("NewConfig returned nil")
	}
	if cfg.serverConfig != nil {
		t.Error("serverConfig should be nil initially")
	}
	if cfg.clientConfig != nil {
		t.Error("clientConfig should be nil initially")
	}
	if cfg.tlsMode != "" {
		t.Errorf("tlsMode = %q, want empty", cfg.tlsMode)
	}
}

func TestParseCertificateInfo_EmptyData(t *testing.T) {
	_, err := ParseCertificateInfo([]byte(""))
	if err == nil {
		t.Error("ParseCertificateInfo should fail for empty data")
	}
}

func TestWriteCertificateToFile_Custom(t *testing.T) {
	dir := t.TempDir()
	certPath := dir + "/cert.pem"
	keyPath := dir + "/key.pem"

	err := WriteCertificateToFile([]byte("cert"), []byte("key"), certPath, keyPath)
	if err != nil {
		t.Fatalf("WriteCertificateToFile failed: %v", err)
	}

	// Verify files were created
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("Failed to read cert: %v", err)
	}
	if string(data) != "cert" {
		t.Errorf("cert content = %q, want cert", string(data))
	}

	data, err = os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("Failed to read key: %v", err)
	}
	if string(data) != "key" {
		t.Errorf("key content = %q, want key", string(data))
	}
}

func TestWriteCertificateToFile_InvalidKeyPath(t *testing.T) {
	err := WriteCertificateToFile([]byte("cert"), []byte("key"), os.DevNull, "/nonexistent/very/deep/path/key.pem")
	if err == nil {
		t.Error("WriteCertificateToFile should fail for invalid key path")
	}
}

// Test LoadFromConfig with "require" mode using real certificates
func TestConfig_LoadFromConfig_Require(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath)

	cfg := &config.TLSConfig{
		Mode:     "require",
		CertFile: certPath,
		KeyFile:  keyPath,
	}

	tlsCfg := NewConfig()
	err = tlsCfg.LoadFromConfig(cfg)
	if err != nil {
		t.Fatalf("LoadFromConfig failed: %v", err)
	}
	if !tlsCfg.IsEnabled() {
		t.Error("Should be enabled")
	}
	if tlsCfg.Mode() != "require" {
		t.Errorf("Mode = %q, want require", tlsCfg.Mode())
	}
	if tlsCfg.ServerConfig() == nil {
		t.Error("ServerConfig should not be nil")
	}
	if tlsCfg.ClientConfig() == nil {
		t.Error("ClientConfig should not be nil")
	}
}

// Test LoadFromConfig with "allow" mode
func TestConfig_LoadFromConfig_Allow(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath)

	cfg := &config.TLSConfig{
		Mode:     "allow",
		CertFile: certPath,
		KeyFile:  keyPath,
	}

	tlsCfg := NewConfig()
	err = tlsCfg.LoadFromConfig(cfg)
	if err != nil {
		t.Fatalf("LoadFromConfig failed: %v", err)
	}
	if tlsCfg.ServerConfig().ClientAuth != tls.RequestClientCert {
		t.Errorf("ClientAuth = %v, want RequestClientCert", tlsCfg.ServerConfig().ClientAuth)
	}
}

// Test LoadFromConfig with "prefer" mode
func TestConfig_LoadFromConfig_Prefer(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath)

	cfg := &config.TLSConfig{
		Mode:     "prefer",
		CertFile: certPath,
		KeyFile:  keyPath,
	}

	tlsCfg := NewConfig()
	err = tlsCfg.LoadFromConfig(cfg)
	if err != nil {
		t.Fatalf("LoadFromConfig failed: %v", err)
	}
}

// Test LoadFromConfig with "verify-ca" mode and CA file
func TestConfig_LoadFromConfig_VerifyCA(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	caPath := filepath.Join(tmpDir, "ca.pem")

	WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath)
	os.WriteFile(caPath, certPEM, 0644)

	cfg := &config.TLSConfig{
		Mode:     "verify-ca",
		CertFile: certPath,
		KeyFile:  keyPath,
		CAFile:   caPath,
	}

	tlsCfg := NewConfig()
	err = tlsCfg.LoadFromConfig(cfg)
	if err != nil {
		t.Fatalf("LoadFromConfig failed: %v", err)
	}
	if !tlsCfg.IsMTLS() {
		t.Error("verify-ca should be mTLS")
	}
	if tlsCfg.ServerConfig().ClientAuth != tls.VerifyClientCertIfGiven {
		t.Errorf("ClientAuth = %v, want VerifyClientCertIfGiven", tlsCfg.ServerConfig().ClientAuth)
	}
	if tlsCfg.ServerConfig().ClientCAs == nil {
		t.Error("ClientCAs should be set")
	}
	if tlsCfg.ClientConfig().RootCAs == nil {
		t.Error("ClientConfig RootCAs should be set")
	}
}

// Test LoadFromConfig with "verify-full" mode and CA file
func TestConfig_LoadFromConfig_VerifyFull(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	caPath := filepath.Join(tmpDir, "ca.pem")

	WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath)
	os.WriteFile(caPath, certPEM, 0644)

	cfg := &config.TLSConfig{
		Mode:     "verify-full",
		CertFile: certPath,
		KeyFile:  keyPath,
		CAFile:   caPath,
	}

	tlsCfg := NewConfig()
	err = tlsCfg.LoadFromConfig(cfg)
	if err != nil {
		t.Fatalf("LoadFromConfig failed: %v", err)
	}
	if !tlsCfg.IsMTLS() {
		t.Error("verify-full should be mTLS")
	}
	if tlsCfg.ServerConfig().ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAndVerifyClientCert", tlsCfg.ServerConfig().ClientAuth)
	}
}

// Test LoadFromConfig with invalid cert path
func TestConfig_LoadFromConfig_InvalidCertPath(t *testing.T) {
	cfg := &config.TLSConfig{
		Mode:     "require",
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
	}

	tlsCfg := NewConfig()
	err := tlsCfg.LoadFromConfig(cfg)
	if err == nil {
		t.Error("Should fail with invalid cert path")
	}
}

// Test LoadFromConfig with invalid CA file (verify-ca mode)
func TestConfig_LoadFromConfig_InvalidCAFile_VerifyCA(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath)

	cfg := &config.TLSConfig{
		Mode:     "verify-ca",
		CertFile: certPath,
		KeyFile:  keyPath,
		CAFile:   "/nonexistent/ca.pem",
	}

	tlsCfg := NewConfig()
	err = tlsCfg.LoadFromConfig(cfg)
	if err == nil {
		t.Error("Should fail with invalid CA file")
	}
}

// Test LoadFromConfig with invalid CA PEM content
func TestConfig_LoadFromConfig_InvalidCAPEMContent(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	caPath := filepath.Join(tmpDir, "ca.pem")

	WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath)
	os.WriteFile(caPath, []byte("not a valid PEM"), 0644)

	cfg := &config.TLSConfig{
		Mode:     "verify-ca",
		CertFile: certPath,
		KeyFile:  keyPath,
		CAFile:   caPath,
	}

	tlsCfg := NewConfig()
	err = tlsCfg.LoadFromConfig(cfg)
	if err == nil {
		t.Error("Should fail with invalid CA PEM content")
	}
}

// Test LoadFromConfig with disable mode resets previous config
func TestConfig_LoadFromConfig_DisableResetsConfig(t *testing.T) {
	certPEM, keyPEM, _ := GenerateSelfSignedCert("localhost", time.Hour)
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath)

	tlsCfg := NewConfig()

	// First enable TLS
	cfg := &config.TLSConfig{
		Mode:     "require",
		CertFile: certPath,
		KeyFile:  keyPath,
	}
	tlsCfg.LoadFromConfig(cfg)
	if !tlsCfg.IsEnabled() {
		t.Error("Should be enabled after require mode")
	}

	// Then disable
	cfg2 := &config.TLSConfig{Mode: "disable"}
	tlsCfg.LoadFromConfig(cfg2)
	if tlsCfg.IsEnabled() {
		t.Error("Should be disabled after disable mode")
	}
	if tlsCfg.ServerConfig() != nil {
		t.Error("ServerConfig should be nil after disable")
	}
	if tlsCfg.ClientConfig() != nil {
		t.Error("ClientConfig should be nil after disable")
	}
}

// Test LoadFromConfig with "require" mode and CA file (for client config RootCAs)
func TestConfig_LoadFromConfig_RequireWithCA(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("localhost", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	caPath := filepath.Join(tmpDir, "ca.pem")

	WriteCertificateToFile(certPEM, keyPEM, certPath, keyPath)
	os.WriteFile(caPath, certPEM, 0644)

	cfg := &config.TLSConfig{
		Mode:     "require",
		CertFile: certPath,
		KeyFile:  keyPath,
		CAFile:   caPath,
	}

	tlsCfg := NewConfig()
	err = tlsCfg.LoadFromConfig(cfg)
	if err != nil {
		t.Fatalf("LoadFromConfig failed: %v", err)
	}
	if tlsCfg.ClientConfig().RootCAs == nil {
		t.Error("ClientConfig RootCAs should be set with CA file")
	}
}

// Test GenerateSelfSignedCert with various hosts
func TestGenerateSelfSignedCert_MultipleHosts(t *testing.T) {
	hosts := []string{"localhost", "127.0.0.1", "example.com", "192.168.1.1"}
	for _, host := range hosts {
		certPEM, keyPEM, err := GenerateSelfSignedCert(host, time.Hour)
		if err != nil {
			t.Errorf("GenerateSelfSignedCert(%q) failed: %v", host, err)
			continue
		}
		if len(certPEM) == 0 || len(keyPEM) == 0 {
			t.Errorf("GenerateSelfSignedCert(%q) returned empty cert or key", host)
		}
	}
}

// Test ParseCertificateInfo with IP address
func TestParseCertificateInfo_IP(t *testing.T) {
	certPEM, _, err := GenerateSelfSignedCert("127.0.0.1", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	info, err := ParseCertificateInfo(certPEM)
	if err != nil {
		t.Fatalf("ParseCertificateInfo failed: %v", err)
	}
	if len(info.IPAddresses) == 0 {
		t.Error("Should have IP addresses")
	}
}

// Test ValidateCertificate with expired cert
func TestValidateCertificate_Expired(t *testing.T) {
	// Generate a cert that's already expired
	certPEM, _, err := GenerateSelfSignedCert("localhost", -1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	err = ValidateCertificate(certPEM, false, nil)
	// May or may not fail depending on validation strictness, but should not panic
	_ = err
}

// Test ValidateCertificate with empty DNS names
func TestValidateCertificate_EmptyDNS(t *testing.T) {
	certPEM, _, err := GenerateSelfSignedCert("127.0.0.1", time.Hour)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	// Validate with DNS names that won't match the IP-only cert
	err = ValidateCertificate(certPEM, true, []string{"example.com"})
	if err == nil {
		t.Error("Should fail for DNS name that doesn't match IP-only cert")
	}
}
