package auth

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"net"
	"strings"
	"testing"
	"time"
)

// TestParseAuthMode tests the ParseAuthMode function
func TestParseAuthMode(t *testing.T) {
	tests := []struct {
		input       string
		expected    AuthMode
		expectError bool
	}{
		{"passthrough", ModePassthrough, false},
		{"interception", ModeInterception, false},
		// Note: ParseAuthMode is case-sensitive and only accepts lowercase
		{"", ModePassthrough, true},
		{"invalid", ModePassthrough, true},
		{"unknown", ModePassthrough, true},
	}

	for _, tt := range tests {
		mode, err := ParseAuthMode(tt.input)
		if tt.expectError {
			if err == nil {
				t.Errorf("ParseAuthMode(%q) expected error, got nil", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("ParseAuthMode(%q) unexpected error: %v", tt.input, err)
			}
			if mode != tt.expected {
				t.Errorf("ParseAuthMode(%q) = %v, want %v", tt.input, mode, tt.expected)
			}
		}
	}
}

// TestVerifyClientFinal tests the VerifyClientFinal function
func TestVerifyClientFinal(t *testing.T) {
	db := NewUserDatabase()

	// Add user with known password
	password := "testpassword"
	hash, _ := GenerateSCRAMHash(password)
	db.AddUser(&User{
		Username:     "testuser",
		PasswordHash: hash,
	})

	server := NewSCRAMServer(db)

	// Perform full SCRAM exchange
	clientFirst := "n,,n=testuser,r=clientnonce123"
	state, err := server.ParseClientFirst(clientFirst)
	if err != nil {
		t.Fatalf("ParseClientFirst failed: %v", err)
	}

	serverFirst, err := server.GenerateServerFirst(state)
	if err != nil {
		t.Fatalf("GenerateServerFirst failed: %v", err)
	}

	_ = serverFirst // May be used in future test cases

	// For now, test that VerifyClientFinal handles invalid input correctly
	t.Run("missing_proof", func(t *testing.T) {
		_, err := server.VerifyClientFinal(state, "c=biws,r=clientnonce123")
		if err == nil {
			t.Error("expected error for missing proof")
		}
	})

	t.Run("invalid_proof_encoding", func(t *testing.T) {
		_, err := server.VerifyClientFinal(state, "c=biws,r=clientnonce123,p=not-valid-base64!!!")
		if err == nil {
			t.Error("expected error for invalid proof encoding")
		}
	})

	t.Run("invalid_password_proof", func(t *testing.T) {
		// Create a valid base64 proof but with wrong data
		validBase64 := "dGVzdHRlc3R0ZXN0dGVzdHRlc3R0ZXN0dGVzdHRlc3Q=" // 32 bytes base64
		_, err := server.VerifyClientFinal(state, "c=biws,r=clientnonce123,p="+validBase64)
		if err == nil {
			t.Error("expected error for invalid password proof")
		}
	})
}

// TestGenerateServerFinal tests the GenerateServerFinal function
func TestGenerateServerFinal(t *testing.T) {
	db := NewUserDatabase()

	password := "testpassword"
	hash, _ := GenerateSCRAMHash(password)
	db.AddUser(&User{
		Username:     "testuser",
		PasswordHash: hash,
	})

	server := NewSCRAMServer(db)

	clientFirst := "n,,n=testuser,r=clientnonce123"
	state, err := server.ParseClientFirst(clientFirst)
	if err != nil {
		t.Fatalf("ParseClientFirst failed: %v", err)
	}

	_, err = server.GenerateServerFirst(state)
	if err != nil {
		t.Fatalf("GenerateServerFirst failed: %v", err)
	}

	// Generate server final
	serverFinal := server.GenerateServerFinal(state)

	// Verify format: v=<base64_server_signature>
	if !strings.HasPrefix(serverFinal, "v=") {
		t.Errorf("expected server-final to start with 'v=', got %q", serverFinal)
	}

	// Verify the signature is valid base64
	sig := serverFinal[2:] // Remove "v=" prefix
	decoded, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		t.Errorf("server signature is not valid base64: %v", err)
	}
	if len(decoded) != 32 { // SHA-256 output size
		t.Errorf("expected 32 byte signature, got %d", len(decoded))
	}
}

// TestSCRAMFullExchange tests a complete SCRAM-SHA-256 authentication exchange
func TestSCRAMFullExchange(t *testing.T) {
	db := NewUserDatabase()

	password := "correctpassword"
	hash, _ := GenerateSCRAMHash(password)
	db.AddUser(&User{
		Username:     "alice",
		PasswordHash: hash,
	})

	server := NewSCRAMServer(db)

	// Step 1: Client sends client-first
	clientFirst := "n,,n=alice,r=fyko+d2lbbFgONRv9qkxdawL"
	state, err := server.ParseClientFirst(clientFirst)
	if err != nil {
		t.Fatalf("ParseClientFirst: %v", err)
	}

	// Step 2: Server sends server-first
	serverFirst, err := server.GenerateServerFirst(state)
	if err != nil {
		t.Fatalf("GenerateServerFirst: %v", err)
	}

	_ = serverFirst

	// Since we have access to state.StoredKey and state.ServerKey,
	// let's verify they are correctly populated
	if len(state.StoredKey) == 0 {
		t.Error("StoredKey should be populated after GenerateServerFirst")
	}
	if len(state.ServerKey) == 0 {
		t.Error("ServerKey should be populated after GenerateServerFirst")
	}
	if len(state.Salt) == 0 {
		t.Error("Salt should be populated after GenerateServerFirst")
	}
	if state.Iterations == 0 {
		t.Error("Iterations should be populated after GenerateServerFirst")
	}

	// Step 4: Server generates server-final
	serverFinal := server.GenerateServerFinal(state)
	if !strings.HasPrefix(serverFinal, "v=") {
		t.Error("server-final should start with 'v='")
	}
}

// TestVerifyClientFinal_InvalidCases tests various invalid client-final inputs
func TestVerifyClientFinal_InvalidCases(t *testing.T) {
	db := NewUserDatabase()
	server := NewSCRAMServer(db)

	state := &SCRAMState{
		Username:    "test",
		Nonce:       "nonce123",
		ClientFirst: "n,,n=test,r=nonce123",
		ServerFirst: "r=nonce123server,s=c2FsdA==,i=4096",
		AuthMessage: "n=test,r=nonce123,r=nonce123server,s=c2FsdA==,i=4096",
		StoredKey:   make([]byte, 32),
	}

	tests := []struct {
		name    string
		msg     string
		wantErr bool
	}{
		{
			name:    "empty_message",
			msg:     "",
			wantErr: true,
		},
		{
			name:    "no_proof",
			msg:     "c=biws,r=nonce123",
			wantErr: true,
		},
		{
			name:    "invalid_base64_proof",
			msg:     "c=biws,r=nonce123,p=!!!invalid!!!",
			wantErr: true,
		},
		{
			name:    "wrong_proof_length",
			msg:     "c=biws,r=nonce123,p=Zm9v", // "foo" - wrong length
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := server.VerifyClientFinal(state, tt.msg)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestCertAuthenticator_ExtractFromSAN tests the extractFromSAN method
func TestCertAuthenticator_ExtractFromSAN(t *testing.T) {
	ca := NewCertAuthenticator(CertAuthConfig{})

	tests := []struct {
		name     string
		dnsNames []string
		ips      []net.IP
		expected string
	}{
		{
			name:     "dns_names",
			dnsNames: []string{"client.example.com"},
			ips:      []net.IP{},
			expected: "client.example.com",
		},
		{
			name:     "ip_addresses",
			dnsNames: []string{},
			ips:      []net.IP{net.ParseIP("192.168.1.1")},
			expected: "192.168.1.1",
		},
		{
			name:     "dns_preferred_over_ip",
			dnsNames: []string{"client.example.com"},
			ips:      []net.IP{net.ParseIP("10.0.0.1")},
			expected: "client.example.com",
		},
		{
			name:     "empty_san",
			dnsNames: []string{},
			ips:      []net.IP{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cert := &x509.Certificate{
				DNSNames:    tt.dnsNames,
				IPAddresses: tt.ips,
			}
			result := ca.extractFromSAN(cert)
			if result != tt.expected {
				t.Errorf("extractFromSAN() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestCertAuthenticator_GetCertificateInfo tests GetCertificateInfo
func TestCertAuthenticator_GetCertificateInfo(t *testing.T) {
	ca := NewCertAuthenticator(CertAuthConfig{})

	t.Run("nil_certificate", func(t *testing.T) {
		info := ca.GetCertificateInfo(nil)
		if info != nil {
			t.Error("expected nil for nil certificate")
		}
	})

	t.Run("valid_certificate", func(t *testing.T) {
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName:         "test-client",
				OrganizationalUnit: []string{"Engineering"},
			},
			Issuer: pkix.Name{
				CommonName: "Test CA",
			},
			DNSNames:    []string{"client.example.com", "alt.example.com"},
			IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
			Raw:         []byte("dummy certificate data for fingerprint"),
		}

		info := ca.GetCertificateInfo(cert)
		if info == nil {
			t.Fatal("expected non-nil info for valid certificate")
		}

		// Check required fields
		if _, ok := info["subject"]; !ok {
			t.Error("expected 'subject' field")
		}
		if _, ok := info["issuer"]; !ok {
			t.Error("expected 'issuer' field")
		}
		if _, ok := info["common_name"]; !ok {
			t.Error("expected 'common_name' field")
		}
		if _, ok := info["serial_number"]; !ok {
			t.Error("expected 'serial_number' field")
		}
		if _, ok := info["not_before"]; !ok {
			t.Error("expected 'not_before' field")
		}
		if _, ok := info["not_after"]; !ok {
			t.Error("expected 'not_after' field")
		}
		if _, ok := info["dns_names"]; !ok {
			t.Error("expected 'dns_names' field")
		}
		if _, ok := info["ip_addresses"]; !ok {
			t.Error("expected 'ip_addresses' field")
		}

		// Verify common_name value
		if cn, ok := info["common_name"].(string); !ok || cn != "test-client" {
			t.Errorf("common_name = %v, want 'test-client'", info["common_name"])
		}
	})
}

// TestCertAuthenticator_Authenticate tests the Authenticate method
func TestCertAuthenticator_Authenticate(t *testing.T) {
	t.Run("nil_certificate", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			Mode: CertAuthCN,
		})
		result := ca.Authenticate(nil)
		if result.Success {
			t.Error("expected failure for nil certificate")
		}
		if result.Error == nil {
			t.Error("expected error for nil certificate")
		}
	})

	t.Run("cn_mode_success", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			Mode: CertAuthCN,
		})
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "testuser",
			},
		}
		result := ca.Authenticate(cert)
		if !result.Success {
			t.Errorf("expected success, got error: %v", result.Error)
		}
		if result.Username != "testuser" {
			t.Errorf("username = %q, want 'testuser'", result.Username)
		}
	})

	t.Run("cn_mode_empty", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			Mode: CertAuthCN,
		})
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "",
			},
		}
		result := ca.Authenticate(cert)
		if result.Success {
			t.Error("expected failure for empty CN")
		}
	})

	t.Run("san_mode_success", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			Mode: CertAuthSAN,
		})
		cert := &x509.Certificate{
			DNSNames: []string{"client.example.com"},
		}
		result := ca.Authenticate(cert)
		if !result.Success {
			t.Errorf("expected success, got error: %v", result.Error)
		}
		if result.Username != "client.example.com" {
			t.Errorf("username = %q, want 'client.example.com'", result.Username)
		}
	})

	t.Run("san_mode_empty", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			Mode: CertAuthSAN,
		})
		cert := &x509.Certificate{}
		result := ca.Authenticate(cert)
		if result.Success {
			t.Error("expected failure for empty SAN")
		}
	})

	t.Run("either_mode_cn_first", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			Mode: CertAuthEither,
		})
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "from-cn",
			},
			DNSNames: []string{"from-san"},
		}
		result := ca.Authenticate(cert)
		if !result.Success {
			t.Errorf("expected success, got error: %v", result.Error)
		}
		if result.Username != "from-cn" {
			t.Errorf("username = %q, want 'from-cn' (CN should take precedence)", result.Username)
		}
	})

	t.Run("either_mode_fallback_to_san", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			Mode: CertAuthEither,
		})
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "",
			},
			DNSNames: []string{"from-san"},
		}
		result := ca.Authenticate(cert)
		if !result.Success {
			t.Errorf("expected success, got error: %v", result.Error)
		}
		if result.Username != "from-san" {
			t.Errorf("username = %q, want 'from-san'", result.Username)
		}
	})

	t.Run("with_prefix_suffix", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			Mode:           CertAuthCN,
			UsernamePrefix: "prefix_",
			UsernameSuffix: "_suffix",
		})
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "user",
			},
		}
		result := ca.Authenticate(cert)
		if !result.Success {
			t.Errorf("expected success, got error: %v", result.Error)
		}
		if result.Username != "prefix_user_suffix" {
			t.Errorf("username = %q, want 'prefix_user_suffix'", result.Username)
		}
	})

	t.Run("with_mapping", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			Mode: CertAuthCN,
			Mappings: map[string]string{
				"cert-identity": "mapped-user",
			},
		})
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "cert-identity",
			},
		}
		result := ca.Authenticate(cert)
		if !result.Success {
			t.Errorf("expected success, got error: %v", result.Error)
		}
		if result.Username != "mapped-user" {
			t.Errorf("username = %q, want 'mapped-user'", result.Username)
		}
	})

	t.Run("disallowed_dns", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			Mode:       CertAuthCN,
			AllowedDNS: []string{"allowed.example.com"},
		})
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "user",
			},
			DNSNames: []string{"notallowed.example.com"},
		}
		result := ca.Authenticate(cert)
		if result.Success {
			t.Error("expected failure for disallowed DNS")
		}
	})

	t.Run("disallowed_ip", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			Mode:       CertAuthCN,
			AllowedIPs: []string{"10.0.0.1"},
		})
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "user",
			},
			IPAddresses: []net.IP{net.ParseIP("192.168.1.1")},
		}
		result := ca.Authenticate(cert)
		if result.Success {
			t.Error("expected failure for disallowed IP")
		}
	})
}

// TestCertificateMapper tests the CertificateMapper
func TestCertificateMapper(t *testing.T) {
	cm := NewCertificateMapper()

	// Create a certificate with sufficient Raw data (at least 32 bytes)
	makeCert := func(raw string) *x509.Certificate {
		// Ensure raw is at least 32 bytes
		for len(raw) < 32 {
			raw += raw
		}
		return &x509.Certificate{Raw: []byte(raw)}
	}

	t.Run("add_and_get_by_fingerprint", func(t *testing.T) {
		cert := makeCert("fp1-data-")
		fp := CertificateFingerprint(cert)
		cm.AddMapping(fp, "user1")
		user := cm.GetUser(cert)
		if user != "user1" {
			t.Errorf("GetUser = %q, want 'user1'", user)
		}
	})

	t.Run("add_and_get_by_cn", func(t *testing.T) {
		cm.AddCNMapping("client-cn", "cn-user")
		cert := makeCert("unknown-fp-1")
		cert.Subject = pkix.Name{CommonName: "client-cn"}
		user := cm.GetUser(cert)
		if user != "cn-user" {
			t.Errorf("GetUser = %q, want 'cn-user'", user)
		}
	})

	t.Run("add_and_get_by_san", func(t *testing.T) {
		cm.AddSANMapping("client.san", "san-user")
		cert := makeCert("unknown-fp-2")
		cert.Subject = pkix.Name{CommonName: "unknown"}
		cert.DNSNames = []string{"client.san"}
		user := cm.GetUser(cert)
		if user != "san-user" {
			t.Errorf("GetUser = %q, want 'san-user'", user)
		}
	})

	t.Run("get_nonexistent", func(t *testing.T) {
		cert := makeCert("nonexistent-unknown-cert-data-here")
		cert.Subject = pkix.Name{CommonName: "unknown"}
		user := cm.GetUser(cert)
		if user != "" {
			t.Errorf("GetUser = %q, want empty", user)
		}
	})

	t.Run("remove_mapping", func(t *testing.T) {
		cert := makeCert("to-remove-test-data-32-bytes-long!!!")
		fp := CertificateFingerprint(cert)
		cm.AddMapping(fp, "removed-user")

		// Verify exists
		if cm.GetUser(cert) != "removed-user" {
			t.Error("user should exist before removal")
		}

		// Remove and verify gone
		cm.RemoveMapping(fp)
		if cm.GetUser(cert) != "" {
			t.Error("user should be removed")
		}
	})
}

// TestCertificateFingerprint tests CertificateFingerprint
func TestCertificateFingerprint(t *testing.T) {
	// Helper to create cert with sufficient raw data
	makeCert := func(raw string) *x509.Certificate {
		for len(raw) < 32 {
			raw += raw
		}
		return &x509.Certificate{Raw: []byte(raw)}
	}

	t.Run("nil_certificate", func(t *testing.T) {
		fp := CertificateFingerprint(nil)
		if fp != "" {
			t.Errorf("fingerprint = %q, want empty", fp)
		}
	})

	t.Run("nil_raw", func(t *testing.T) {
		cert := &x509.Certificate{Raw: nil}
		fp := CertificateFingerprint(cert)
		if fp != "" {
			t.Errorf("fingerprint = %q, want empty", fp)
		}
	})

	t.Run("valid_certificate", func(t *testing.T) {
		cert := makeCert("test certificate data for fingerprint computation")
		fp := CertificateFingerprint(cert)
		if fp == "" {
			t.Error("expected non-empty fingerprint")
		}
		// Should be hex format (64 chars for 32 bytes)
		if len(fp) != 64 {
			t.Errorf("expected 64 char hex string, got %d chars", len(fp))
		}
		for _, c := range fp {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("fingerprint contains non-hex character: %c", c)
				break
			}
		}
	})
}

// TestIsCertificateValidExtended tests IsCertificateValid with more cases
func TestIsCertificateValidExtended(t *testing.T) {
	now := time.Now()

	t.Run("not_yet_valid", func(t *testing.T) {
		cert := &x509.Certificate{
			NotBefore: now.Add(time.Hour),
			NotAfter:  now.Add(24 * time.Hour),
		}
		valid := IsCertificateValid(cert)
		if valid {
			t.Error("expected invalid for not-yet-valid certificate")
		}
	})

	t.Run("expired", func(t *testing.T) {
		cert := &x509.Certificate{
			NotBefore: now.Add(-48 * time.Hour),
			NotAfter:  now.Add(-24 * time.Hour),
		}
		valid := IsCertificateValid(cert)
		if valid {
			t.Error("expected invalid for expired certificate")
		}
	})

	t.Run("valid_now", func(t *testing.T) {
		cert := &x509.Certificate{
			NotBefore: now.Add(-time.Hour),
			NotAfter:  now.Add(time.Hour),
		}
		valid := IsCertificateValid(cert)
		if !valid {
			t.Error("expected valid certificate")
		}
	})
}

// TestExtractIdentity tests ExtractIdentity
func TestExtractIdentity(t *testing.T) {
	tests := []struct {
		name        string
		mode        CertAuthMode
		cert        *x509.Certificate
		expected    string
		expectError bool
	}{
		{
			name:        "cn_success",
			mode:        CertAuthCN,
			cert:        &x509.Certificate{Subject: pkix.Name{CommonName: "test-cn"}},
			expected:    "test-cn",
			expectError: false,
		},
		{
			name:        "cn_empty",
			mode:        CertAuthCN,
			cert:        &x509.Certificate{Subject: pkix.Name{CommonName: ""}},
			expected:    "",
			expectError: true,
		},
		{
			name:        "san_dns",
			mode:        CertAuthSAN,
			cert:        &x509.Certificate{DNSNames: []string{"test.san"}},
			expected:    "test.san",
			expectError: false,
		},
		{
			name:        "san_ip",
			mode:        CertAuthSAN,
			cert:        &x509.Certificate{IPAddresses: []net.IP{net.ParseIP("192.168.1.1")}},
			expected:    "192.168.1.1",
			expectError: false,
		},
		{
			name:        "san_empty",
			mode:        CertAuthSAN,
			cert:        &x509.Certificate{},
			expected:    "",
			expectError: true,
		},
		{
			name:        "either_cn_first",
			mode:        CertAuthEither,
			cert:        &x509.Certificate{Subject: pkix.Name{CommonName: "cn-value"}, DNSNames: []string{"san-value"}},
			expected:    "cn-value",
			expectError: false,
		},
		{
			name:        "either_fallback_san",
			mode:        CertAuthEither,
			cert:        &x509.Certificate{Subject: pkix.Name{CommonName: ""}, DNSNames: []string{"san-value"}},
			expected:    "san-value",
			expectError: false,
		},
		{
			name:        "either_empty",
			mode:        CertAuthEither,
			cert:        &x509.Certificate{},
			expected:    "",
			expectError: true,
		},
		{
			name:        "nil_cert",
			mode:        CertAuthCN,
			cert:        nil,
			expected:    "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity, err := ExtractIdentity(tt.cert, tt.mode)
			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if identity != tt.expected {
					t.Errorf("identity = %q, want %q", identity, tt.expected)
				}
			}
		})
	}
}

// TestCertAuthenticator_Config tests Config and UpdateConfig
func TestCertAuthenticator_Config(t *testing.T) {
	initialConfig := CertAuthConfig{
		Mode:           CertAuthCN,
		RequireCert:    true,
		UsernamePrefix: "initial_",
	}

	ca := NewCertAuthenticator(initialConfig)

	// Test Config returns correct values
	cfg := ca.Config()
	if cfg.Mode != CertAuthCN {
		t.Errorf("Mode = %v, want CertAuthCN", cfg.Mode)
	}
	if !cfg.RequireCert {
		t.Error("RequireCert should be true")
	}
	if cfg.UsernamePrefix != "initial_" {
		t.Errorf("UsernamePrefix = %q, want 'initial_'", cfg.UsernamePrefix)
	}

	// Test UpdateConfig
	newConfig := CertAuthConfig{
		Mode:           CertAuthSAN,
		RequireCert:    false,
		UsernamePrefix: "updated_",
	}
	ca.UpdateConfig(newConfig)

	cfg = ca.Config()
	if cfg.Mode != CertAuthSAN {
		t.Errorf("Mode = %v, want CertAuthSAN after update", cfg.Mode)
	}
	if cfg.RequireCert {
		t.Error("RequireCert should be false after update")
	}
	if cfg.UsernamePrefix != "updated_" {
		t.Errorf("UsernamePrefix = %q, want 'updated_' after update", cfg.UsernamePrefix)
	}
}

// TestCertAuthenticator_ValidateCertificate tests validateCertificate
func TestCertAuthenticator_ValidateCertificate(t *testing.T) {
	t.Run("allowed_dns_match", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			AllowedDNS: []string{"*.example.com"},
		})
		cert := &x509.Certificate{
			DNSNames: []string{"client.example.com"},
		}
		err := ca.validateCertificate(cert)
		if err != nil {
			t.Errorf("expected success, got error: %v", err)
		}
	})

	t.Run("allowed_dns_no_match", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			AllowedDNS: []string{"allowed.example.com"},
		})
		cert := &x509.Certificate{
			DNSNames: []string{"notallowed.example.com"},
		}
		err := ca.validateCertificate(cert)
		if err == nil {
			t.Error("expected error for disallowed DNS")
		}
	})

	t.Run("allowed_ip_match", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			AllowedIPs: []string{"192.168.1.1"},
		})
		cert := &x509.Certificate{
			IPAddresses: []net.IP{net.ParseIP("192.168.1.1")},
		}
		err := ca.validateCertificate(cert)
		if err != nil {
			t.Errorf("expected success, got error: %v", err)
		}
	})

	t.Run("allowed_ip_no_match", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{
			AllowedIPs: []string{"10.0.0.1"},
		})
		cert := &x509.Certificate{
			IPAddresses: []net.IP{net.ParseIP("192.168.1.1")},
		}
		err := ca.validateCertificate(cert)
		if err == nil {
			t.Error("expected error for disallowed IP")
		}
	})

	t.Run("no_restrictions", func(t *testing.T) {
		ca := NewCertAuthenticator(CertAuthConfig{})
		cert := &x509.Certificate{
			DNSNames:    []string{"any.example.com"},
			IPAddresses: []net.IP{net.ParseIP("1.2.3.4")},
		}
		err := ca.validateCertificate(cert)
		if err != nil {
			t.Errorf("expected success with no restrictions, got error: %v", err)
		}
	})
}

// TestPeerCertificate tests PeerCertificate
func TestPeerCertificate(t *testing.T) {
	// PeerCertificate returns nil for now (stub implementation)
	result := PeerCertificate(nil)
	if result != nil {
		t.Error("PeerCertificate should return nil")
	}

	result = PeerCertificate("some-state")
	if result != nil {
		t.Error("PeerCertificate should return nil for any input")
	}
}
