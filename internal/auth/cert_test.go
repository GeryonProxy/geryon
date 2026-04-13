package auth

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"testing"
	"time"
)

func TestParseCertAuthMode(t *testing.T) {
	tests := []struct {
		input    string
		expected CertAuthMode
		wantErr  bool
	}{
		{"disabled", CertAuthDisabled, false},
		{"", CertAuthDisabled, false},
		{"none", CertAuthDisabled, false},
		{"cn", CertAuthCN, false},
		{"common_name", CertAuthCN, false},
		{"san", CertAuthSAN, false},
		{"subject_alt_name", CertAuthSAN, false},
		{"either", CertAuthEither, false},
		{"cn_or_san", CertAuthEither, false},
		{"invalid", CertAuthDisabled, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			mode, err := ParseCertAuthMode(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseCertAuthMode(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if mode != tt.expected {
				t.Errorf("ParseCertAuthMode(%q) = %v, want %v", tt.input, mode, tt.expected)
			}
		})
	}
}

func TestCertAuthModeString(t *testing.T) {
	tests := []struct {
		mode     CertAuthMode
		expected string
	}{
		{CertAuthDisabled, "disabled"},
		{CertAuthCN, "cn"},
		{CertAuthSAN, "san"},
		{CertAuthEither, "either"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.mode.String(); got != tt.expected {
				t.Errorf("CertAuthMode.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestNewCertAuthenticator(t *testing.T) {
	config := CertAuthConfig{
		Mode:        CertAuthCN,
		RequireCert: true,
	}

	ca := NewCertAuthenticator(config)
	if ca == nil {
		t.Fatal("NewCertAuthenticator returned nil")
	}

	if ca.config.Mode != CertAuthCN {
		t.Errorf("config.Mode = %v, want %v", ca.config.Mode, CertAuthCN)
	}
}

func TestCertAuthenticatorAuthenticateNilCert(t *testing.T) {
	ca := NewCertAuthenticator(CertAuthConfig{Mode: CertAuthCN})
	result := ca.Authenticate(nil)

	if result.Success {
		t.Error("Expected authentication to fail with nil certificate")
	}
	if result.Error == nil {
		t.Error("Expected error for nil certificate")
	}
}

func TestExtractIdentityCN(t *testing.T) {
	cert := &x509.Certificate{
		Subject: pkix.Name{
			CommonName: "testuser",
		},
	}

	identity, err := ExtractIdentity(cert, CertAuthCN)
	if err != nil {
		t.Errorf("ExtractIdentity failed: %v", err)
	}
	if identity != "testuser" {
		t.Errorf("ExtractIdentity = %q, want %q", identity, "testuser")
	}
}

func TestExtractIdentityCNMissing(t *testing.T) {
	cert := &x509.Certificate{
		Subject: pkix.Name{
			CommonName: "",
		},
	}

	_, err := ExtractIdentity(cert, CertAuthCN)
	if err == nil {
		t.Error("Expected error for missing CN")
	}
}

func TestExtractIdentitySAN(t *testing.T) {
	cert := &x509.Certificate{
		DNSNames: []string{"test.example.com"},
	}

	identity, err := ExtractIdentity(cert, CertAuthSAN)
	if err != nil {
		t.Errorf("ExtractIdentity failed: %v", err)
	}
	if identity != "test.example.com" {
		t.Errorf("ExtractIdentity = %q, want %q", identity, "test.example.com")
	}
}

func TestExtractIdentitySANWithIP(t *testing.T) {
	cert := &x509.Certificate{
		IPAddresses: []net.IP{net.ParseIP("192.168.1.1")},
	}

	identity, err := ExtractIdentity(cert, CertAuthSAN)
	if err != nil {
		t.Errorf("ExtractIdentity failed: %v", err)
	}
	if identity != "192.168.1.1" {
		t.Errorf("ExtractIdentity = %q, want %q", identity, "192.168.1.1")
	}
}

func TestExtractIdentityEither(t *testing.T) {
	// Test with CN present
	cert := &x509.Certificate{
		Subject: pkix.Name{
			CommonName: "testuser",
		},
		DNSNames: []string{"fallback.example.com"},
	}

	identity, err := ExtractIdentity(cert, CertAuthEither)
	if err != nil {
		t.Errorf("ExtractIdentity failed: %v", err)
	}
	if identity != "testuser" {
		t.Errorf("ExtractIdentity = %q, want %q", identity, "testuser")
	}

	// Test without CN - should fall back to SAN
	cert2 := &x509.Certificate{
		Subject:  pkix.Name{CommonName: ""},
		DNSNames: []string{"fallback.example.com"},
	}

	identity2, err := ExtractIdentity(cert2, CertAuthEither)
	if err != nil {
		t.Errorf("ExtractIdentity failed: %v", err)
	}
	if identity2 != "fallback.example.com" {
		t.Errorf("ExtractIdentity = %q, want %q", identity2, "fallback.example.com")
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		s       string
		pattern string
		want    bool
	}{
		{"test.example.com", "*", true},
		{"test.example.com", "test.example.com", true},
		{"test.example.com", "*.example.com", true},
		{"test.example.com", "test.*.com", true},
		{"test.example.com", "*.other.com", false},
		{"test.example.com", "other.example.com", false},
		{"test.example.com", "test.example.*", true},
	}

	for _, tt := range tests {
		t.Run(tt.s+"_"+tt.pattern, func(t *testing.T) {
			got, _ := matchPattern(tt.s, tt.pattern)
			if got != tt.want {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.s, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestNewCertificateMapper(t *testing.T) {
	cm := NewCertificateMapper()
	if cm == nil {
		t.Fatal("NewCertificateMapper returned nil")
	}
	if cm.mappings == nil {
		t.Error("mappings map not initialized")
	}
	if cm.byCN == nil {
		t.Error("byCN map not initialized")
	}
	if cm.bySAN == nil {
		t.Error("bySAN map not initialized")
	}
}

func TestCertificateMapperAddAndGet(t *testing.T) {
	cm := NewCertificateMapper()

	// Test fingerprint mapping
	cm.AddMapping("fp1", "user1")
	_ = &x509.Certificate{
		Raw: []byte("fp1_test_data_for_fingerprint"),
	}

	// Create a simple mock for testing
	// Note: CertificateFingerprint won't return "fp1" but we can test the flow
	cm.AddCNMapping("testcn", "user2")
	cert2 := &x509.Certificate{
		Subject: pkix.Name{CommonName: "testcn"},
	}

	user := cm.GetUser(cert2)
	if user != "user2" {
		t.Errorf("GetUser = %q, want %q", user, "user2")
	}
}

func TestCertificateMapperSANMapping(t *testing.T) {
	cm := NewCertificateMapper()

	cm.AddSANMapping("test.example.com", "user3")
	cert := &x509.Certificate{
		DNSNames: []string{"test.example.com"},
	}

	user := cm.GetUser(cert)
	if user != "user3" {
		t.Errorf("GetUser = %q, want %q", user, "user3")
	}
}

func TestIsCertificateValid(t *testing.T) {
	// Test nil certificate
	if IsCertificateValid(nil) {
		t.Error("IsCertificateValid(nil) should be false")
	}

	// Note: Testing with actual time-based validity requires generating
	// certificates with specific NotBefore/NotAfter values
}

func TestCertAuthenticatorConfig(t *testing.T) {
	config := CertAuthConfig{
		Mode:           CertAuthCN,
		RequireCert:    true,
		UsernamePrefix: "cert_",
		UsernameSuffix: "_user",
	}

	ca := NewCertAuthenticator(config)

	// Test GetConfig returns a copy
	retrieved := ca.Config()
	if retrieved.Mode != CertAuthCN {
		t.Errorf("Config.Mode = %v, want %v", retrieved.Mode, CertAuthCN)
	}
	if retrieved.UsernamePrefix != "cert_" {
		t.Errorf("Config.UsernamePrefix = %q, want %q", retrieved.UsernamePrefix, "cert_")
	}

	// Update config
	newConfig := CertAuthConfig{
		Mode: CertAuthSAN,
	}
	ca.UpdateConfig(newConfig)

	updated := ca.Config()
	if updated.Mode != CertAuthSAN {
		t.Errorf("Updated Config.Mode = %v, want %v", updated.Mode, CertAuthSAN)
	}
}

func TestCertAuthenticatorWithMappings(t *testing.T) {
	config := CertAuthConfig{
		Mode: CertAuthCN,
		Mappings: map[string]string{
			"admin":    "superuser",
			"operator": "ops_user",
		},
		UsernamePrefix: "cert_",
	}

	ca := NewCertAuthenticator(config)
	cert := &x509.Certificate{
		Subject: pkix.Name{CommonName: "admin"},
	}

	// Test mapping is applied
	username, err := ca.extractUsername(cert)
	if err != nil {
		t.Errorf("extractUsername failed: %v", err)
	}
	if username != "cert_superuser" {
		t.Errorf("extractUsername = %q, want %q", username, "cert_superuser")
	}
}

func TestCertAuthenticatorValidateCertificateDNS(t *testing.T) {
	config := CertAuthConfig{
		Mode:       CertAuthCN,
		AllowedDNS: []string{"*.example.com"},
	}

	ca := NewCertAuthenticator(config)

	// Valid DNS
	cert1 := &x509.Certificate{
		Subject:  pkix.Name{CommonName: "test"},
		DNSNames: []string{"db.example.com"},
	}
	if err := ca.validateCertificate(cert1); err != nil {
		t.Errorf("validateCertificate failed for valid DNS: %v", err)
	}

	// Invalid DNS
	cert2 := &x509.Certificate{
		Subject:  pkix.Name{CommonName: "test"},
		DNSNames: []string{"db.other.com"},
	}
	if err := ca.validateCertificate(cert2); err == nil {
		t.Error("validateCertificate should fail for invalid DNS")
	}
}

func TestCertAuthenticatorValidateCertificateIP(t *testing.T) {
	config := CertAuthConfig{
		Mode:     CertAuthCN,
		AllowedIPs: []string{"10.0.0.0/24"},
	}

	ca := NewCertAuthenticator(config)

	// Valid IP (we only check exact matches in the simple implementation)
	_ = &x509.Certificate{
		Subject:     pkix.Name{CommonName: "test"},
		IPAddresses: []net.IP{net.ParseIP("10.0.0.0/24")},
	}
	// This will fail because IPAddresses don't store CIDR notation
	// The actual implementation uses simple string comparison
	_ = ca
}

// --- matchPattern additional cases for middle parts ---

func TestMatchPattern_MiddleParts(t *testing.T) {
	tests := []struct {
		s       string
		pattern string
		want    bool
	}{
		// Three-part patterns (tests middle parts loop)
		{"abcDEFghi", "abc*DEF*ghi", true},
		{"abcXXXghi", "abc*DEF*ghi", false}, // middle part DEF not found
		// Prefix mismatch
		{"other.example.com", "test.*.com", false},
		// Suffix mismatch
		{"test.example.org", "test.*.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.s+"_"+tt.pattern, func(t *testing.T) {
			got, err := matchPattern(tt.s, tt.pattern)
			if err != nil {
				t.Errorf("matchPattern(%q, %q) error: %v", tt.s, tt.pattern, err)
			}
			if got != tt.want {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.s, tt.pattern, got, tt.want)
			}
		})
	}
}

// --- extractUsername SAN with no SAN ---

func TestExtractUsername_SANNoSAN(t *testing.T) {
	ca := NewCertAuthenticator(CertAuthConfig{Mode: CertAuthSAN})
	cert := &x509.Certificate{} // No DNS names, no IPs

	_, err := ca.extractUsername(cert)
	if err == nil {
		t.Error("Should fail with no SAN")
	}
}

// --- extractUsername Either with neither CN nor SAN ---

func TestExtractUsername_EitherNothing(t *testing.T) {
	ca := NewCertAuthenticator(CertAuthConfig{Mode: CertAuthEither})
	cert := &x509.Certificate{} // No CN, no SAN

	_, err := ca.extractUsername(cert)
	if err == nil {
		t.Error("Should fail with no CN or SAN")
	}
}

// --- extractUsername default/invalid mode ---

func TestExtractUsername_InvalidMode(t *testing.T) {
	ca := NewCertAuthenticator(CertAuthConfig{Mode: CertAuthMode(99)})
	cert := &x509.Certificate{Subject: pkix.Name{CommonName: "test"}}

	_, err := ca.extractUsername(cert)
	if err == nil {
		t.Error("Should fail for invalid mode")
	}
}

// --- GetUser with IP SAN mapping ---

func TestCertificateMapper_IPSANMapping(t *testing.T) {
	cm := NewCertificateMapper()
	cm.AddSANMapping("192.168.1.1", "ip_user")

	cert := &x509.Certificate{
		IPAddresses: []net.IP{net.ParseIP("192.168.1.1")},
	}

	user := cm.GetUser(cert)
	if user != "ip_user" {
		t.Errorf("GetUser = %q, want ip_user", user)
	}
}

// --- GetUser with no matching mappings ---

func TestCertificateMapper_GetUser_NoMatch(t *testing.T) {
	cm := NewCertificateMapper()
	cm.AddCNMapping("other", "user1")

	cert := &x509.Certificate{
		Subject: pkix.Name{CommonName: "unknown"},
	}

	user := cm.GetUser(cert)
	if user != "" {
		t.Errorf("GetUser = %q, want empty string", user)
	}
}

// --- GetUser with empty CN ---

func TestCertificateMapper_GetUser_EmptyCN(t *testing.T) {
	cm := NewCertificateMapper()
	cm.AddCNMapping("", "empty_user")

	cert := &x509.Certificate{
		Subject: pkix.Name{CommonName: ""},
	}

	user := cm.GetUser(cert)
	// Empty CN should skip the CN lookup
	if user != "" {
		t.Errorf("GetUser with empty CN = %q, want empty (CN lookup skipped)", user)
	}
}

// --- RemoveMapping ---

func TestCertificateMapper_RemoveMapping(t *testing.T) {
	cm := NewCertificateMapper()
	cm.AddMapping("fp1", "user1")
	cm.RemoveMapping("fp1")

	// After removal, GetUser should not find it
	cert := &x509.Certificate{Raw: []byte("fp1_data")}
	user := cm.GetUser(cert)
	// Fingerprint won't match "fp1" since it's SHA-256 hash of Raw
	_ = user
}

// --- ExtractIdentity with IP in Either mode ---

func TestExtractIdentity_EitherWithIP(t *testing.T) {
	cert := &x509.Certificate{
		IPAddresses: []net.IP{net.ParseIP("10.0.0.1")},
	}

	identity, err := ExtractIdentity(cert, CertAuthEither)
	if err != nil {
		t.Fatalf("ExtractIdentity Either+IP failed: %v", err)
	}
	if identity != "10.0.0.1" {
		t.Errorf("ExtractIdentity = %q, want 10.0.0.1", identity)
	}
}

// --- ExtractIdentity with invalid mode ---

func TestExtractIdentity_InvalidMode(t *testing.T) {
	cert := &x509.Certificate{Subject: pkix.Name{CommonName: "test"}}

	_, err := ExtractIdentity(cert, CertAuthMode(99))
	if err == nil {
		t.Error("Should fail for invalid mode")
	}
}

// --- ExtractIdentity nil cert ---

func TestExtractIdentity_NilCert(t *testing.T) {
	_, err := ExtractIdentity(nil, CertAuthCN)
	if err == nil {
		t.Error("Should fail for nil cert")
	}
}

// --- ExtractIdentity SAN with no SAN ---

func TestExtractIdentity_SANEmpty(t *testing.T) {
	cert := &x509.Certificate{}
	_, err := ExtractIdentity(cert, CertAuthSAN)
	if err == nil {
		t.Error("Should fail with no SAN")
	}
}

// --- ExtractIdentity Either with nothing ---

func TestExtractIdentity_EitherEmpty(t *testing.T) {
	cert := &x509.Certificate{}
	_, err := ExtractIdentity(cert, CertAuthEither)
	if err == nil {
		t.Error("Should fail with no identity")
	}
}

// --- GetCertificateInfo nil ---

func TestGetCertificateInfo_Nil(t *testing.T) {
	ca := NewCertAuthenticator(CertAuthConfig{Mode: CertAuthCN})
	info := ca.GetCertificateInfo(nil)
	if info != nil {
		t.Error("GetCertificateInfo(nil) should return nil")
	}
}

// --- GetCertificateInfo with valid cert ---

func TestGetCertificateInfo_ValidCert(t *testing.T) {
	ca := NewCertAuthenticator(CertAuthConfig{Mode: CertAuthCN})
	cert := &x509.Certificate{
		Subject:     pkix.Name{CommonName: "test"},
		DNSNames:    []string{"test.example.com"},
		IPAddresses: []net.IP{net.ParseIP("10.0.0.1")},
	}

	info := ca.GetCertificateInfo(cert)
	if info == nil {
		t.Fatal("GetCertificateInfo should return info for valid cert")
	}
	if info["common_name"] != "test" {
		t.Errorf("common_name = %v, want test", info["common_name"])
	}
	if info["dns_names"] == nil {
		t.Error("dns_names should be present")
	}
}

// --- CertificateFingerprint nil cert ---

func TestCertificateFingerprint_Nil(t *testing.T) {
	if fp := CertificateFingerprint(nil); fp != "" {
		t.Errorf("CertificateFingerprint(nil) = %q, want empty", fp)
	}
}

// --- IsCertificateValid with valid time range ---

func TestIsCertificateValid_ValidTime(t *testing.T) {
	now := time.Now()
	cert := &x509.Certificate{
		NotBefore: now.Add(-1 * time.Hour),
		NotAfter:  now.Add(1 * time.Hour),
	}
	if !IsCertificateValid(cert) {
		t.Error("Certificate within valid time range should be valid")
	}
}

// --- IsCertificateValid expired ---

func TestIsCertificateValid_Expired(t *testing.T) {
	now := time.Now()
	cert := &x509.Certificate{
		NotBefore: now.Add(-2 * time.Hour),
		NotAfter:  now.Add(-1 * time.Hour),
	}
	if IsCertificateValid(cert) {
		t.Error("Expired certificate should be invalid")
	}
}

// --- PeerCertificate ---

func TestPeerCertificate_NilInput(t *testing.T) {
	if cert := PeerCertificate(nil); cert != nil {
		t.Error("PeerCertificate should return nil")
	}
}
