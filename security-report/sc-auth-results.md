# sc-auth-results.md - Authentication Security Audit

## Summary

Issues found: **1** (1 critical, 0 high, 0 medium, 0 low)

---

## Findings

### 1. [CRITICAL] Non-constant-time Token Comparison in Admin Interfaces

**File(s):**
- `internal/api/rest/server.go` (line 242)
- `internal/api/grpc/server.go` (line 165)
- `internal/api/dashboard/server.go` (line 154)
- `internal/api/mcp/server.go` (line 145)

**Description:**

All four admin interfaces (REST, gRPC, Dashboard, MCP) use regular string comparison (`!=`) for bearer token authentication instead of constant-time comparison:

```go
// REST (server.go:242)
if parts[1] != s.config.Auth.Token {

// gRPC (server.go:165)
if parts[1] != s.authToken {

// Dashboard (server.go:154)
if parts[1] != s.authToken {

// MCP (server.go:145)
if parts[1] != s.authToken {
```

**Impact:**

This vulnerability allows attackers to perform timing attacks to guess the admin bearer token value character by character by measuring response time differences. While the timing difference per character is minimal, an attacker with network proximity and multiple attempts could eventually deduce the full token.

**Recommendation:**

Replace all token comparisons with `crypto/subtle.ConstantTimeCompare`:

```go
if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.config.Auth.Token)) != 1 {
```

Or use `hmac.Equal` for byte slice comparison:

```go
if !hmac.Equal([]byte(parts[1]), []byte(s.authToken)) {
```

---

## Passed Security Checks

### Weak Password Hashing
- **Status:** PASS
- Uses SCRAM-SHA-256 with PBKDF2 (SHA-256, 10000 iterations) per `internal/auth/auth.go`
- No MD5 or SHA1 used for password hashing
- NIST-recommended iteration count (10000)

### Timing-Safe Password Verification
- **Status:** PASS
- `subtle.ConstantTimeCompare` used in `internal/auth/auth.go` line 135
- `hmac.Equal` used for StoredKey comparison in `internal/auth/scram.go` line 265

### Brute Force Protection
- **Status:** PASS
- `AuthLimiter` implemented in `internal/auth/scram.go` (lines 472-585)
- Default: 10 failed attempts per 5-minute window, 5-minute lockout
- Rate limiting applied in `internal/proxy/listener.go` line 636

### Hardcoded Credentials
- **Status:** PASS
- No hardcoded passwords found in authentication code
- Admin tokens are validated in `internal/config/config.go` (lines 268-279) requiring non-empty values when auth is enabled

### SCRAM-SHA-256 Implementation
- **Status:** PASS
- Properly implements SCRAM RFC 5802
- Correct GS2 header parsing in `ParseClientFirst` (scram.go:143)
- Proper client-final verification in `VerifyClientFinal` (scram.go:227)
- Server signature generation in `GenerateServerFinal` (scram.go:273)

---

## Severity Classification

| Severity | Count |
|----------|-------|
| Critical | 1 |
| High | 0 |
| Medium | 0 |
| Low | 0 |
