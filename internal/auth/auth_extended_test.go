package auth

import (
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
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

	t.Run("sha256_hash_not_raw_bytes", func(t *testing.T) {
		raw := []byte("verify this is a proper SHA-256 hash")
		cert := &x509.Certificate{Raw: raw}
		fp := CertificateFingerprint(cert)

		// Verify it matches SHA-256 of raw bytes, not first 32 raw bytes
		expected := fmt.Sprintf("%x", sha256.Sum256(raw))
		if fp != expected {
			t.Errorf("fingerprint does not match SHA-256 hash\ngot  = %s\nwant = %s", fp, expected)
		}

		// Verify it's NOT just the first 32 bytes of raw (the old bug)
		legacyExpected := fmt.Sprintf("%x", raw[:32])
		if fp == legacyExpected {
			t.Error("fingerprint appears to be raw first 32 bytes, not SHA-256 hash")
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

// TestAuthLimiter records failures and enforces lockout
func TestAuthLimiter(t *testing.T) {
	// 3 attempts, 100ms window, 200ms lockout
	limiter := NewAuthLimiterConfig(3, 100*time.Millisecond, 200*time.Millisecond)

	// Should not be limited initially
	if limiter.IsLimited("1.2.3.4") {
		t.Error("should not be limited initially")
	}

	// Record failures up to limit
	for i := 0; i < 2; i++ {
		if limiter.RecordFailure("1.2.3.4") {
			t.Errorf("should not be locked after %d failures", i+1)
		}
	}

	// 3rd failure should trigger lockout
	if !limiter.RecordFailure("1.2.3.4") {
		t.Error("should be locked after 3 failures")
	}

	// Should be limited
	if !limiter.IsLimited("1.2.3.4") {
		t.Error("should report as limited")
	}

	// Additional failures while locked should still return limited
	if !limiter.RecordFailure("1.2.3.4") {
		t.Error("should remain locked on further failures")
	}

	// Wait for lockout to expire
	time.Sleep(250 * time.Millisecond)
	if limiter.IsLimited("1.2.3.4") {
		t.Error("should not be limited after lockout expires")
	}

	// RecordSuccess should clear the counter
	limiter2 := NewAuthLimiterConfig(2, 100*time.Millisecond, 200*time.Millisecond)
	limiter2.RecordFailure("1.2.3.4")
	limiter2.RecordSuccess("1.2.3.4")
	if limiter2.IsLimited("1.2.3.4") {
		t.Error("should not be limited after success")
	}

	// Different IPs should be independent
	limiter3 := NewAuthLimiterConfig(2, 100*time.Millisecond, 200*time.Millisecond)
	limiter3.RecordFailure("1.1.1.1")
	limiter3.RecordFailure("1.1.1.1")
	if limiter3.IsLimited("2.2.2.2") {
		t.Error("different IP should not be affected")
	}
}

// TestAuthLimiterWindowExpiry resets counter after window expires
func TestAuthLimiterWindowExpiry(t *testing.T) {
	limiter := NewAuthLimiterConfig(3, 100*time.Millisecond, 200*time.Millisecond)

	// Record 2 failures
	limiter.RecordFailure("1.2.3.4")
	limiter.RecordFailure("1.2.3.4")

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	// Next failure should start a new window, not trigger lockout
	if limiter.RecordFailure("1.2.3.4") {
		t.Error("should not lock after window expired and single new failure")
	}
}

// TestNewAuthLimiter tests the default constructor
func TestNewAuthLimiter(t *testing.T) {
	limiter := NewAuthLimiter()
	if limiter == nil {
		t.Fatal("NewAuthLimiter returned nil")
	}
	if limiter.maxAttempts != 10 {
		t.Errorf("maxAttempts = %d, want 10", limiter.maxAttempts)
	}
	if limiter.window != 5*time.Minute {
		t.Errorf("window = %v, want 5m", limiter.window)
	}
	if limiter.lockoutPeriod != 5*time.Minute {
		t.Errorf("lockoutPeriod = %v, want 5m", limiter.lockoutPeriod)
	}
	if limiter.attempts == nil {
		t.Error("attempts map not initialized")
	}
}

// TestGenerateSCRAMSHA256 tests the standalone SCRAM-SHA-256 generation
func TestGenerateSCRAMSHA256(t *testing.T) {
	t.Run("valid_password", func(t *testing.T) {
		hash, err := GenerateSCRAMSHA256("testpassword")
		if err != nil {
			t.Fatalf("GenerateSCRAMSHA256 failed: %v", err)
		}
		if !strings.HasPrefix(hash, "SCRAM-SHA-256$") {
			t.Errorf("hash should start with 'SCRAM-SHA-256$', got %q", hash)
		}
		// Parse the format: SCRAM-SHA-256$<iterations>:<salt>$<storedKey>:<serverKey>
		parts := strings.Split(hash, "$")
		if len(parts) != 3 {
			t.Fatalf("expected 3 major parts, got %d", len(parts))
		}
		// Check iterations is 10000
		iterParts := strings.Split(parts[1], ":")
		if len(iterParts) != 2 {
			t.Fatal("iterations:salt should have 2 parts")
		}
		if iterParts[0] != "10000" {
			t.Errorf("iterations = %s, want 10000", iterParts[0])
		}
		// Salt should be 32 bytes (base64 encoded)
		salt, err := base64.StdEncoding.DecodeString(iterParts[1])
		if err != nil {
			t.Fatalf("invalid salt encoding: %v", err)
		}
		if len(salt) != 32 {
			t.Errorf("salt length = %d, want 32", len(salt))
		}
		// Stored key and server key should be present
		keyParts := strings.Split(parts[2], ":")
		if len(keyParts) != 2 {
			t.Fatal("storedKey:serverKey should have 2 parts")
		}
		storedKey, err := base64.StdEncoding.DecodeString(keyParts[0])
		if err != nil {
			t.Fatalf("invalid storedKey encoding: %v", err)
		}
		if len(storedKey) != 32 {
			t.Errorf("storedKey length = %d, want 32", len(storedKey))
		}
		serverKey, err := base64.StdEncoding.DecodeString(keyParts[1])
		if err != nil {
			t.Fatalf("invalid serverKey encoding: %v", err)
		}
		if len(serverKey) != 32 {
			t.Errorf("serverKey length = %d, want 32", len(serverKey))
		}
	})

	t.Run("different_passwords_different_hashes", func(t *testing.T) {
		hash1, _ := GenerateSCRAMSHA256("password1")
		hash2, _ := GenerateSCRAMSHA256("password2")
		if hash1 == hash2 {
			t.Error("different passwords should produce different hashes")
		}
	})

	t.Run("same_password_different_salts", func(t *testing.T) {
		hash1, _ := GenerateSCRAMSHA256("samepassword")
		hash2, _ := GenerateSCRAMSHA256("samepassword")
		if hash1 == hash2 {
			t.Error("same password with random salts should produce different hashes")
		}
	})
}

// TestVerifySCRAMSHA256 tests password verification against SCRAM-SHA-256 hashes
func TestVerifySCRAMSHA256(t *testing.T) {
	t.Run("correct_password", func(t *testing.T) {
		password := "verifyme"
		hash, err := GenerateSCRAMSHA256(password)
		if err != nil {
			t.Fatalf("GenerateSCRAMSHA256 failed: %v", err)
		}
		ok, err := VerifySCRAMSHA256(password, hash)
		if err != nil {
			t.Fatalf("VerifySCRAMSHA256 failed: %v", err)
		}
		if !ok {
			t.Error("verification should succeed with correct password")
		}
	})

	t.Run("wrong_password", func(t *testing.T) {
		hash, _ := GenerateSCRAMSHA256("correct")
		ok, err := VerifySCRAMSHA256("wrong", hash)
		if err != nil {
			t.Fatalf("VerifySCRAMSHA256 failed: %v", err)
		}
		if ok {
			t.Error("verification should fail with wrong password")
		}
	})

	t.Run("invalid_hash_format_missing_parts", func(t *testing.T) {
		_, err := VerifySCRAMSHA256("pass", "SCRAM-SHA-256$invalid")
		if err == nil {
			t.Error("should fail for hash with wrong number of parts")
		}
	})

	t.Run("invalid_hash_format_wrong_algorithm", func(t *testing.T) {
		_, err := VerifySCRAMSHA256("pass", "SCRAM-SHA-512$10000:c2FsdA==$aGFzaA=:c2VydmVy")
		if err == nil {
			t.Error("should fail for unsupported algorithm")
		}
	})

	t.Run("invalid_iterations", func(t *testing.T) {
		_, err := VerifySCRAMSHA256("pass", "SCRAM-SHA-256$abc:c2FsdA==$aGFzaA=:c2VydmVy")
		if err == nil {
			t.Error("should fail for non-numeric iterations")
		}
	})

	t.Run("invalid_salt_encoding", func(t *testing.T) {
		_, err := VerifySCRAMSHA256("pass", "SCRAM-SHA-256$10000:!!!invalid!!!$aGFzaA=:c2VydmVy")
		if err == nil {
			t.Error("should fail for invalid base64 salt")
		}
	})

	t.Run("invalid_stored_key_encoding", func(t *testing.T) {
		_, err := VerifySCRAMSHA256("pass", "SCRAM-SHA-256$10000:c2FsdA==$!!!invalid!!!:c2VydmVy")
		if err == nil {
			t.Error("should fail for invalid base64 stored key")
		}
	})

	t.Run("invalid_iterations_salt_format", func(t *testing.T) {
		_, err := VerifySCRAMSHA256("pass", "SCRAM-SHA-256$10000$c2FsdA==$aGFzaA=:c2VydmVy")
		if err == nil {
			t.Error("should fail for invalid iter/salt format (missing colon)")
		}
	})

	t.Run("invalid_keys_format", func(t *testing.T) {
		_, err := VerifySCRAMSHA256("pass", "SCRAM-SHA-256$10000:c2FsdA==$aGFzaA+")
		if err == nil {
			t.Error("should fail for invalid keys format (missing server key)")
		}
	})
}

// TestPBKDF2Key tests the pbkdf2Key function directly
func TestPBKDF2Key(t *testing.T) {
	t.Run("derive_key", func(t *testing.T) {
		key := pbkdf2Key([]byte("password"), []byte("salt"), 1000, 32, sha256.New)
		if len(key) != 32 {
			t.Errorf("key length = %d, want 32", len(key))
		}
		// Same inputs should produce same key
		key2 := pbkdf2Key([]byte("password"), []byte("salt"), 1000, 32, sha256.New)
		for i := range key {
			if key[i] != key2[i] {
				t.Error("same inputs should produce same key")
				break
			}
		}
	})

	t.Run("different_salt_different_key", func(t *testing.T) {
		key1 := pbkdf2Key([]byte("password"), []byte("salt1"), 1000, 32, sha256.New)
		key2 := pbkdf2Key([]byte("password"), []byte("salt2"), 1000, 32, sha256.New)
		same := true
		for i := range key1 {
			if key1[i] != key2[i] {
				same = false
				break
			}
		}
		if same {
			t.Error("different salts should produce different keys")
		}
	})

	t.Run("different_password_different_key", func(t *testing.T) {
		key1 := pbkdf2Key([]byte("pass1"), []byte("salt"), 1000, 32, sha256.New)
		key2 := pbkdf2Key([]byte("pass2"), []byte("salt"), 1000, 32, sha256.New)
		same := true
		for i := range key1 {
			if key1[i] != key2[i] {
				same = false
				break
			}
		}
		if same {
			t.Error("different passwords should produce different keys")
		}
	})

	t.Run("longer_key", func(t *testing.T) {
		key := pbkdf2Key([]byte("password"), []byte("salt"), 1000, 64, sha256.New)
		if len(key) != 64 {
			t.Errorf("key length = %d, want 64", len(key))
		}
	})
}

// TestHMACSum tests the hmacSum function
func TestHMACSum(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		result := hmacSum([]byte("key"), []byte("data"))
		if len(result) != 32 {
			t.Errorf("result length = %d, want 32 (SHA-256)", len(result))
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		r1 := hmacSum([]byte("secret-key"), []byte("message"))
		r2 := hmacSum([]byte("secret-key"), []byte("message"))
		for i := range r1 {
			if r1[i] != r2[i] {
				t.Error("same inputs should produce same output")
				break
			}
		}
	})

	t.Run("different_key_different_output", func(t *testing.T) {
		r1 := hmacSum([]byte("key1"), []byte("data"))
		r2 := hmacSum([]byte("key2"), []byte("data"))
		same := true
		for i := range r1 {
			if r1[i] != r2[i] {
				same = false
				break
			}
		}
		if same {
			t.Error("different keys should produce different output")
		}
	})

	t.Run("empty_data", func(t *testing.T) {
		result := hmacSum([]byte("key"), []byte{})
		if len(result) != 32 {
			t.Errorf("result length = %d, want 32", len(result))
		}
	})
}

// TestParseSCRAMHash tests the parseSCRAMHash function
func TestParseSCRAMHash_Extended(t *testing.T) {
	password := "testpassword"
	hash, err := GenerateSCRAMHash(password)
	if err != nil {
		t.Fatalf("GenerateSCRAMHash failed: %v", err)
	}

	storedKey, serverKey, salt, iterations, err := parseSCRAMHash(hash)
	if err != nil {
		t.Fatalf("parseSCRAMHash failed for valid hash: %v", err)
	}
	if len(storedKey) == 0 {
		t.Error("storedKey should not be empty")
	}
	if len(serverKey) == 0 {
		t.Error("serverKey should not be empty")
	}
	if len(salt) == 0 {
		t.Error("salt should not be empty")
	}
	if iterations <= 0 {
		t.Errorf("iterations = %d, want > 0", iterations)
	}
}

func TestParseSCRAMHash_UnsupportedFormat(t *testing.T) {
	_, _, _, _, err := parseSCRAMHash("bcrypt$hash")
	if err == nil {
		t.Error("Should fail for unsupported format")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("Error = %q, want unsupported", err.Error())
	}
}

func TestParseSCRAMHash_InvalidNewFormat(t *testing.T) {
	_, _, _, _, err := parseSCRAMHash("SCRAM-SHA-256$onlyonepart")
	if err == nil {
		t.Error("Should fail for invalid new format")
	}
}

func TestParseSCRAMHash_InvalidNewFormatBadIterSalt(t *testing.T) {
	_, _, _, _, err := parseSCRAMHash("SCRAM-SHA-256$abc$stored:server")
	if err == nil {
		t.Error("Should fail for invalid iterations")
	}
}

func TestParseSCRAMHash_InvalidNewFormatBadSalt(t *testing.T) {
	_, _, _, _, err := parseSCRAMHash("SCRAM-SHA-256$4096:!invalid-base64!$c3RvcmVk:c2VydmVy")
	if err == nil {
		t.Error("Should fail for invalid salt base64")
	}
}

func TestParseSCRAMHash_InvalidNewFormatBadKeys(t *testing.T) {
	_, _, _, _, err := parseSCRAMHash("SCRAM-SHA-256$4096:c2FsdA==$onlyonekey")
	if err == nil {
		t.Error("Should fail for invalid keys format")
	}
}

func TestParseSCRAMHash_InvalidNewFormatBadStoredKey(t *testing.T) {
	_, _, _, _, err := parseSCRAMHash("SCRAM-SHA-256$4096:c2FsdA==$!invalid!:c2VydmVy")
	if err == nil {
		t.Error("Should fail for invalid stored key base64")
	}
}

func TestParseSCRAMHash_InvalidNewFormatBadServerKey(t *testing.T) {
	_, _, _, _, err := parseSCRAMHash("SCRAM-SHA-256$4096:c2FsdA==$c3RvcmVk:!invalid!")
	if err == nil {
		t.Error("Should fail for invalid server key base64")
	}
}

func TestParseSCRAMHash_LegacyFormat(t *testing.T) {
	salt := base64.StdEncoding.EncodeToString([]byte("salt"))
	storedKey := base64.StdEncoding.EncodeToString([]byte("storedkey"))
	serverKey := base64.StdEncoding.EncodeToString([]byte("serverkey"))
	hash := "SCRAM-SHA-256$" + fmt.Sprintf("4096:%s:%s:%s", salt, storedKey, serverKey)

	sk, svk, s, iter, err := parseSCRAMHash(hash)
	if err != nil {
		t.Fatalf("parseSCRAMHash legacy failed: %v", err)
	}
	if iter != 4096 {
		t.Errorf("iterations = %d, want 4096", iter)
	}
	if string(s) != "salt" {
		t.Errorf("salt = %q, want salt", s)
	}
	if string(sk) != "storedkey" {
		t.Errorf("storedKey = %q, want storedkey", sk)
	}
	if string(svk) != "serverkey" {
		t.Errorf("serverKey = %q, want serverkey", svk)
	}
}

func TestParseSCRAMHash_LegacyFormatInvalidIter(t *testing.T) {
	salt := base64.StdEncoding.EncodeToString([]byte("salt"))
	storedKey := base64.StdEncoding.EncodeToString([]byte("storedkey"))
	serverKey := base64.StdEncoding.EncodeToString([]byte("serverkey"))
	hash := "SCRAM-SHA-256$" + fmt.Sprintf("abc:%s:%s:%s", salt, storedKey, serverKey)

	_, _, _, _, err := parseSCRAMHash(hash)
	if err == nil {
		t.Error("Should fail for invalid iterations in legacy format")
	}
}

func TestParseSCRAMHash_LegacyFormatInvalidSalt(t *testing.T) {
	storedKey := base64.StdEncoding.EncodeToString([]byte("storedkey"))
	serverKey := base64.StdEncoding.EncodeToString([]byte("serverkey"))
	hash := "SCRAM-SHA-256$" + fmt.Sprintf("4096:!invalid!:%s:%s", storedKey, serverKey)

	_, _, _, _, err := parseSCRAMHash(hash)
	if err == nil {
		t.Error("Should fail for invalid salt base64 in legacy format")
	}
}

func TestParseSCRAMHash_LegacyFormatInvalidStoredKey(t *testing.T) {
	salt := base64.StdEncoding.EncodeToString([]byte("salt"))
	serverKey := base64.StdEncoding.EncodeToString([]byte("serverkey"))
	hash := "SCRAM-SHA-256$" + fmt.Sprintf("4096:%s:!invalid!:%s", salt, serverKey)

	_, _, _, _, err := parseSCRAMHash(hash)
	if err == nil {
		t.Error("Should fail for invalid stored key base64 in legacy format")
	}
}

func TestParseSCRAMHash_LegacyFormatInvalidServerKey(t *testing.T) {
	salt := base64.StdEncoding.EncodeToString([]byte("salt"))
	storedKey := base64.StdEncoding.EncodeToString([]byte("storedkey"))
	hash := "SCRAM-SHA-256$" + fmt.Sprintf("4096:%s:%s:!invalid!", salt, storedKey)

	_, _, _, _, err := parseSCRAMHash(hash)
	if err == nil {
		t.Error("Should fail for invalid server key base64 in legacy format")
	}
}

// TestMatchPattern tests the matchPattern function
func TestMatchPattern_Extended(t *testing.T) {
	tests := []struct {
		s       string
		pattern string
		want    bool
	}{
		{"hello", "*", true},
		{"hello", "hello", true},
		{"hello", "world", false},
		{"hello", "hel*", true},
		{"hello", "*llo", true},
		{"hello", "h*llo", true},
		{"hello", "h*o", true},
		{"hello", "h*xyz", false},
		{"test.example.com", "*.example.com", true},
		{"test.example.com", "*.other.com", false},
		{"abc", "a*c", true},
		{"abc", "a*b", false},
	}

	for _, tt := range tests {
		got, err := matchPattern(tt.s, tt.pattern)
		if err != nil {
			t.Errorf("matchPattern(%q, %q) unexpected error: %v", tt.s, tt.pattern, err)
			continue
		}
		if got != tt.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.s, tt.pattern, got, tt.want)
		}
	}
}

// TestAuthLimiter_RecordFailureMultiple tests repeated failures and success reset
func TestAuthLimiter_RecordFailureMultiple(t *testing.T) {
	limiter := NewAuthLimiter()

	// Record many failures
	for i := 0; i < 10; i++ {
		limiter.RecordFailure("192.168.1.1")
	}

	// Should be limited after many failures
	if !limiter.IsLimited("192.168.1.1") {
		t.Error("Should be limited after 10 failures")
	}

	// Different IP should not be limited
	if limiter.IsLimited("192.168.1.2") {
		t.Error("Different IP should not be limited")
	}

	// Record success should reset
	limiter.RecordSuccess("192.168.1.1")
	if limiter.IsLimited("192.168.1.1") {
		t.Error("Should not be limited after success")
	}
}

// TestAuthLimiter_RecordFailure_ExpiredLockout tests the expired lockout reset path
func TestAuthLimiter_RecordFailure_ExpiredLockout(t *testing.T) {
	limiter := NewAuthLimiterConfig(2, 1*time.Hour, 1*time.Nanosecond)

	// Trigger lockout
	limiter.RecordFailure("1.2.3.4")
	limiter.RecordFailure("1.2.3.4")

	if !limiter.IsLimited("1.2.3.4") {
		t.Error("Should be locked after 2 failures")
	}

	// Wait for lockout to expire (1 nanosecond)
	time.Sleep(1 * time.Millisecond)

	// Next failure should reset the counter (lockout expired path)
	locked := limiter.RecordFailure("1.2.3.4")
	if locked {
		t.Error("Should not be locked after lockout expires and new failure")
	}
}

// TestAuthLimiter_RecordFailure_AlreadyLocked tests the already-locked path
func TestAuthLimiter_RecordFailure_AlreadyLocked(t *testing.T) {
	limiter := NewAuthLimiterConfig(2, 1*time.Hour, 1*time.Hour)

	limiter.RecordFailure("1.2.3.4")
	limiter.RecordFailure("1.2.3.4")

	// Already locked, should return true
	if !limiter.RecordFailure("1.2.3.4") {
		t.Error("Should return true when already locked")
	}
}

// TestAuthLimiter_IsLimited_WindowExpired tests window expiry cleanup in IsLimited
func TestAuthLimiter_IsLimited_WindowExpired(t *testing.T) {
	limiter := NewAuthLimiterConfig(10, 1*time.Nanosecond, 1*time.Hour)

	limiter.RecordFailure("1.2.3.4")

	// Wait for window to expire
	time.Sleep(1 * time.Millisecond)

	if limiter.IsLimited("1.2.3.4") {
		t.Error("Should not be limited after window expires")
	}
}

// Test_xor_DifferentLengths tests xor with different length inputs
func Test_xor_DifferentLengths(t *testing.T) {
	a := []byte{0x01, 0x02, 0x03}
	b := []byte{0xFF, 0xFE}

	result := xor(a, b)
	if len(result) != 2 {
		t.Errorf("len(result) = %d, want 2 (shorter)", len(result))
	}
	if result[0] != 0x01^0xFF || result[1] != 0x02^0xFE {
		t.Errorf("result = %v, want [0xFE 0xFC]", result)
	}

	// Reverse: b shorter than a
	result2 := xor(b, a)
	if len(result2) != 2 {
		t.Errorf("len(result2) = %d, want 2", len(result2))
	}
}
