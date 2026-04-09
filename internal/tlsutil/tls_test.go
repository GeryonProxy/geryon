package tlsutil

import (
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
