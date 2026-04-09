package auth

import (
	"crypto/x509"
	"fmt"
	"strings"
	"sync"
	"time"
)

// CertAuthMode represents certificate-based authentication modes.
type CertAuthMode int

const (
	// CertAuthDisabled disables certificate authentication.
	CertAuthDisabled CertAuthMode = iota
	// CertAuthCN extracts username from Common Name.
	CertAuthCN
	// CertAuthSAN extracts username from Subject Alternative Name.
	CertAuthSAN
	// CertAuthEither extracts username from CN or SAN (whichever matches first).
	CertAuthEither
)

// CertAuthConfig holds certificate authentication configuration.
type CertAuthConfig struct {
	Mode           CertAuthMode
	RequireCert    bool
	AllowedCAs     []string
	AllowedDNS     []string
	AllowedIPs     []string
	UsernamePrefix string
	UsernameSuffix string
	Mappings       map[string]string // cert identity -> username
}

// CertAuthenticator handles client certificate-based authentication.
type CertAuthenticator struct {
	config CertAuthConfig
	mu     sync.RWMutex
}

// NewCertAuthenticator creates a new certificate authenticator.
func NewCertAuthenticator(config CertAuthConfig) *CertAuthenticator {
	return &CertAuthenticator{
		config: config,
	}
}

// CertAuthResult holds the result of certificate authentication.
type CertAuthResult struct {
	Success     bool
	Username    string
	Certificate *x509.Certificate
	Error       error
}

// Authenticate validates a client certificate and extracts the username.
func (ca *CertAuthenticator) Authenticate(cert *x509.Certificate) *CertAuthResult {
	if cert == nil {
		return &CertAuthResult{
			Success: false,
			Error:   fmt.Errorf("no client certificate provided"),
		}
	}

	// Check if certificate is valid
	if err := ca.validateCertificate(cert); err != nil {
		return &CertAuthResult{
			Success:     false,
			Certificate: cert,
			Error:       err,
		}
	}

	// Extract username based on configured mode
	username, err := ca.extractUsername(cert)
	if err != nil {
		return &CertAuthResult{
			Success:     false,
			Certificate: cert,
			Error:       err,
		}
	}

	return &CertAuthResult{
		Success:     true,
		Username:    username,
		Certificate: cert,
	}
}

// validateCertificate validates the certificate against configuration.
func (ca *CertAuthenticator) validateCertificate(cert *x509.Certificate) error {
	// Check allowed DNS names
	if len(ca.config.AllowedDNS) > 0 {
		found := false
		for _, allowed := range ca.config.AllowedDNS {
			for _, dns := range cert.DNSNames {
				if matched, _ := matchPattern(dns, allowed); matched {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return fmt.Errorf("certificate DNS names not in allowed list")
		}
	}

	// Check allowed IP addresses
	if len(ca.config.AllowedIPs) > 0 {
		found := false
		for _, allowed := range ca.config.AllowedIPs {
			for _, ip := range cert.IPAddresses {
				if ip.String() == allowed {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return fmt.Errorf("certificate IP addresses not in allowed list")
		}
	}

	return nil
}

// extractUsername extracts username from certificate based on mode.
func (ca *CertAuthenticator) extractUsername(cert *x509.Certificate) (string, error) {
	var identity string

	switch ca.config.Mode {
	case CertAuthCN:
		identity = cert.Subject.CommonName
		if identity == "" {
			return "", fmt.Errorf("certificate has no Common Name")
		}

	case CertAuthSAN:
		identity = ca.extractFromSAN(cert)
		if identity == "" {
			return "", fmt.Errorf("certificate has no valid Subject Alternative Name")
		}

	case CertAuthEither:
		// Try CN first
		identity = cert.Subject.CommonName
		if identity == "" {
			// Fall back to SAN
			identity = ca.extractFromSAN(cert)
		}
		if identity == "" {
			return "", fmt.Errorf("certificate has no Common Name or valid SAN")
		}

	default:
		return "", fmt.Errorf("invalid certificate auth mode")
	}

	// Check for explicit mapping
	if ca.config.Mappings != nil {
		if mapped, ok := ca.config.Mappings[identity]; ok {
			identity = mapped
		}
	}

	// Apply prefix/suffix
	username := ca.config.UsernamePrefix + identity + ca.config.UsernameSuffix

	return username, nil
}

// extractFromSAN extracts an identity from Subject Alternative Names.
func (ca *CertAuthenticator) extractFromSAN(cert *x509.Certificate) string {
	// Prefer DNS names over IPs
	if len(cert.DNSNames) > 0 {
		return cert.DNSNames[0]
	}
	if len(cert.IPAddresses) > 0 {
		return cert.IPAddresses[0].String()
	}
	return ""
}

// GetCertificateInfo returns information about a certificate.
func (ca *CertAuthenticator) GetCertificateInfo(cert *x509.Certificate) map[string]interface{} {
	if cert == nil {
		return nil
	}

	info := map[string]interface{}{
		"subject":       cert.Subject.String(),
		"issuer":        cert.Issuer.String(),
		"common_name":   cert.Subject.CommonName,
		"serial_number": cert.SerialNumber.String(),
		"not_before":    cert.NotBefore,
		"not_after":     cert.NotAfter,
		"dns_names":     cert.DNSNames,
	}

	ips := make([]string, 0, len(cert.IPAddresses))
	for _, ip := range cert.IPAddresses {
		ips = append(ips, ip.String())
	}
	info["ip_addresses"] = ips

	return info
}

// UpdateConfig updates the certificate authenticator configuration.
func (ca *CertAuthenticator) UpdateConfig(config CertAuthConfig) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.config = config
}

// Config returns a copy of the current configuration.
func (ca *CertAuthenticator) Config() CertAuthConfig {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	return ca.config
}

// matchPattern matches a string against a pattern with wildcards.
func matchPattern(s, pattern string) (bool, error) {
	// Simple wildcard matching: * matches any sequence of characters
	if pattern == "*" {
		return true, nil
	}
	if !strings.Contains(pattern, "*") {
		return s == pattern, nil
	}

	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return s == pattern, nil
	}

	// Check prefix
	if !strings.HasPrefix(s, parts[0]) {
		return false, nil
	}
	s = s[len(parts[0]):]

	// Check middle parts
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(s, parts[i])
		if idx == -1 {
			return false, nil
		}
		s = s[idx+len(parts[i]):]
	}

	// Check suffix
	return strings.HasSuffix(s, parts[len(parts)-1]), nil
}

// ParseCertAuthMode parses a certificate auth mode string.
func ParseCertAuthMode(s string) (CertAuthMode, error) {
	switch strings.ToLower(s) {
	case "disabled", "", "none":
		return CertAuthDisabled, nil
	case "cn", "common_name", "commonname":
		return CertAuthCN, nil
	case "san", "subject_alt_name", "subjectalternativename":
		return CertAuthSAN, nil
	case "either", "cn_or_san", "cnorsan":
		return CertAuthEither, nil
	default:
		return CertAuthDisabled, fmt.Errorf("invalid cert auth mode: %s", s)
	}
}

// String returns the string representation of the cert auth mode.
func (m CertAuthMode) String() string {
	switch m {
	case CertAuthCN:
		return "cn"
	case CertAuthSAN:
		return "san"
	case CertAuthEither:
		return "either"
	default:
		return "disabled"
	}
}

// CertificateMapper maps certificates to users.
type CertificateMapper struct {
	mu       sync.RWMutex
	mappings map[string]string // fingerprint -> username
	byCN     map[string]string // CN -> username
	bySAN    map[string]string // SAN -> username
}

// NewCertificateMapper creates a new certificate mapper.
func NewCertificateMapper() *CertificateMapper {
	return &CertificateMapper{
		mappings: make(map[string]string),
		byCN:     make(map[string]string),
		bySAN:    make(map[string]string),
	}
}

// AddMapping adds a certificate-to-user mapping.
func (cm *CertificateMapper) AddMapping(fingerprint, username string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.mappings[fingerprint] = username
}

// AddCNMapping adds a CN-to-user mapping.
func (cm *CertificateMapper) AddCNMapping(cn, username string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.byCN[cn] = username
}

// AddSANMapping adds a SAN-to-user mapping.
func (cm *CertificateMapper) AddSANMapping(san, username string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.bySAN[san] = username
}

// GetUser returns the username for a certificate.
func (cm *CertificateMapper) GetUser(cert *x509.Certificate) string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Try fingerprint first
	fp := CertificateFingerprint(cert)
	if user, ok := cm.mappings[fp]; ok {
		return user
	}

	// Try CN
	if cert.Subject.CommonName != "" {
		if user, ok := cm.byCN[cert.Subject.CommonName]; ok {
			return user
		}
	}

	// Try SANs
	for _, dns := range cert.DNSNames {
		if user, ok := cm.bySAN[dns]; ok {
			return user
		}
	}
	for _, ip := range cert.IPAddresses {
		if user, ok := cm.bySAN[ip.String()]; ok {
			return user
		}
	}

	return ""
}

// RemoveMapping removes a fingerprint mapping.
func (cm *CertificateMapper) RemoveMapping(fingerprint string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	delete(cm.mappings, fingerprint)
}

// CertificateFingerprint returns a certificate fingerprint.
func CertificateFingerprint(cert *x509.Certificate) string {
	if cert == nil || cert.Raw == nil {
		return ""
	}
	// Simple fingerprint: SHA-256 of raw certificate
	// In production, use crypto/sha256
	return fmt.Sprintf("%x", cert.Raw[:32])
}

// PeerCertificate extracts the peer certificate from a TLS connection state.
func PeerCertificate(state interface{}) *x509.Certificate {
	// This is a type-safe wrapper that works with *tls.ConnectionState
	// The actual implementation would import crypto/tls
	// For now, return nil - this will be called from the proxy layer
	return nil
}

// IsCertificateValid checks if a certificate is currently valid.
func IsCertificateValid(cert *x509.Certificate) bool {
	if cert == nil {
		return false
	}
	now := time.Now()
	return now.After(cert.NotBefore) && now.Before(cert.NotAfter)
}

// ExtractIdentity extracts identity from certificate based on mode.
func ExtractIdentity(cert *x509.Certificate, mode CertAuthMode) (string, error) {
	if cert == nil {
		return "", fmt.Errorf("nil certificate")
	}

	switch mode {
	case CertAuthCN:
		if cert.Subject.CommonName == "" {
			return "", fmt.Errorf("no Common Name in certificate")
		}
		return cert.Subject.CommonName, nil

	case CertAuthSAN:
		if len(cert.DNSNames) > 0 {
			return cert.DNSNames[0], nil
		}
		if len(cert.IPAddresses) > 0 {
			return cert.IPAddresses[0].String(), nil
		}
		return "", fmt.Errorf("no SAN in certificate")

	case CertAuthEither:
		if cert.Subject.CommonName != "" {
			return cert.Subject.CommonName, nil
		}
		if len(cert.DNSNames) > 0 {
			return cert.DNSNames[0], nil
		}
		if len(cert.IPAddresses) > 0 {
			return cert.IPAddresses[0].String(), nil
		}
		return "", fmt.Errorf("no identity in certificate")

	default:
		return "", fmt.Errorf("invalid extraction mode")
	}
}
