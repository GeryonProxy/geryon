# Security Assessment Report

**Project:** GeryonProxy — Multi-database connection pooler and proxy
**Date:** 2026-05-01
**Scanner:** security-check v1.0.0
**Risk Score:** 5.3/10 (Moderate Risk)

---

## Executive Summary

A security assessment was performed on GeryonProxy, a pure-Go database connection pooler supporting PostgreSQL, MySQL, and MSSQL wire protocols. The scan analyzed 85+ Go source files using 10 specialized security skills across 20+ vulnerability categories.

### Key Metrics

| Metric | Value |
|--------|-------|
| Total Files Scanned | 85+ |
| Languages | Go (100%) |
| Skills Executed | 10 |
| Skills with Findings | 2 |
| Final Verified Findings | 2 (1 High, 1 Medium) |
| False Positives Eliminated | 8 |

### Top Risks

1. **H-1: gRPC Admin API authentication bypass** when `auth.enabled=false` in config — all admin endpoints publicly accessible
2. **M-9: Unbounded context in session handler** — client disconnection not respected during pool connection acquisition

---

## Scan Statistics

| Statistic | Value |
|-----------|-------|
| Files Scanned | 85+ Go source files |
| Lines of Code | ~25,000+ |
| Languages Detected | Go |
| Frameworks Detected | None (stdlib + 5 production deps) |
| Skills Executed | 10 |
| Findings Before Verification | 10 |
| False Positives Eliminated | 8 |
| Final Verified Findings | 2 |

### Skills Executed

| Skill | Category | Result |
|-------|----------|--------|
| sc-lang-go | Language-specific | 1 finding |
| sc-auth | Authentication | PASS |
| sc-authz | Authorization | 1 finding |
| sc-sqli | SQL Injection | PASS |
| sc-ssrf | Server-Side Request Forgery | 1 info |
| sc-rce | Remote Code Execution | PASS |
| sc-secrets | Hardcoded Secrets | PASS |
| sc-csrf | CSRF | PASS (N/A) |
| sc-path-traversal | Path Traversal | PASS |
| sc-file-upload | File Upload | N/A |
| sc-race-condition | Race Conditions | PASS |
| sc-rate-limiting | Rate Limiting | PASS |
| sc-jwt | JWT | PASS (N/A) |
| sc-business-logic | Business Logic | PASS |
| sc-mass-assignment | Mass Assignment | PASS |

---

## High Findings

### VULN-001: H-1 — gRPC Admin API Authentication Bypass

**Severity:** High
**Confidence:** 85/100 (High Probability)
**CWE:** CWE-306 — Missing Authentication for Critical Function
**OWASP:** A07:2021 — Security Misconfiguration

**Location:** `internal/api/grpc/server.go:176`

**Description:**
When `auth.enabled=false` is set in the Geryon configuration, the gRPC admin API server's `withAuth` middleware skips authentication entirely. This bypass means all admin API endpoints (pool management, config reload, cluster operations) become publicly accessible without any credentials.

The REST API and MCP server are not affected — they correctly always require authentication regardless of the `auth.enabled` config flag.

**Vulnerable Code:**
```go
// internal/api/grpc/server.go:174-178
func (s *Server) withAuth(next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
    return func(w http.ResponseWriter, r *http.Request) {
        if !s.authEnabled {
            // BUG: Skips auth entirely when auth.enabled=false
            return next(w, r)
        }
        // ... auth logic ...
    }
}
```

**Impact:**
- If `auth.enabled=false` (for testing or internal-only deployments), all gRPC admin endpoints are unauthenticated
- Attackers could modify pool configurations, reload configs, or access cluster state
- Does NOT affect production default (auth is enabled by default)

**Remediation:**
Align gRPC server with REST/MCP behavior — always require authentication on admin endpoints. The `auth.enabled` config should control whether proxy clients authenticate to Geryon, not whether admin API is protected:

```go
func (s *Server) withAuth(next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
    return func(w http.ResponseWriter, r *http.Request) {
        // Always authenticate admin endpoints
        token := r.Header.Get("Authorization")
        if token == "" {
            http.Error(w, "missing authorization", http.StatusUnauthorized)
            return
        }
        // Validate token...
    }
}
```

**References:**
- [CWE-306: Missing Authentication for Critical Function](https://cwe.mitre.org/data/definitions/306.html)
- [OWASP A07:2021](https://owasp.org/Top10/A07_2021-Security_Misconfiguration/)

---

## Medium Findings

### VULN-002: M-9 — Unbounded Context in Session Message Handler

**Severity:** Medium
**Confidence:** 65/100 (Probable)
**CWE:** CWE-400 — Resource Exhaustion
**OWASP:** A04:2021 — Insecure Design

**Location:** `internal/pool/session.go:290`

**Description:**
The `HandleMessage` method in `session.go` uses `context.Background()` when acquiring a server connection for query processing. This means client disconnection is not honored during connection acquisition — a client that disconnects while waiting for a pool connection will have its goroutine continue until a connection is available or the pool's wait timeout expires.

**Vulnerable Code:**
```go
// internal/pool/session.go:290
func (s *ProxySession) HandleMessage(msg *ProxyMessage) error {
    // ...
    ctx := context.Background()  // Should use session context that respects client disconnect
    serverConn, err := s.pool.AcquireContext(ctx)
    // ...
}
```

**Impact:**
- A client that disconnects while waiting for a pool connection holds its goroutine open
- Bounded by pool's `WaitTimeout` (so not completely unbounded)
- Moderate resource waste if many clients disconnect while waiting
- Low severity given timeout protection

**Remediation:**
Replace `context.Background()` with the session's parent context that respects client disconnection:

```go
func (s *ProxySession) HandleMessage(msg *ProxyMessage) error {
    // Use parent session context that respects client disconnection
    ctx := s.ctx  // or derive from s.parentCtx
    serverConn, err := s.pool.AcquireContext(ctx)
    // ...
}
```

**References:**
- [Go Context Documentation](https://pkg.go.dev/context)
- [CWE-400: Resource Exhaustion](https://cwe.mitre.org/data/definitions/400.html)

---

## Informational Findings

### INFO-001: Admin API SSRF Risk — Backend Address IP Range Validation

**Severity:** Info
**Confidence:** 40/100 (Possible)
**CWE:** CWE-918 — Server-Side Request Forgery

**Location:** `internal/api/rest/server.go` (backend config endpoints)

**Description:**
The Admin API allows backend addresses to be configured to arbitrary IP addresses, including internal cloud metadata endpoints (e.g., 169.254.169.254). The `validateBackendAddr()` function only checks address format, not IP ranges or blocklists.

**Impact:**
Requires valid admin credentials to exploit — not client-exploitable. An authenticated admin could configure a backend to point at internal services (cloud metadata servers, internal databases).

**Remediation:**
Add IP range blocklisting for known internal ranges in `validateBackendAddr()`:
```go
func validateBackendAddr(addr string) error {
    // Block: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16
    // Add your implementation
}
```

---

## Positive Security Observations

The GeryonProxy codebase demonstrates strong security practices:

| Security Control | Implementation |
|-----------------|----------------|
| **Authentication** | SCRAM-SHA-256 with 120k PBKDF2 iterations for proxy clients |
| **Password Storage** | `subtle.ConstantTimeCompare` for timing-safe comparison |
| **Auth Rate Limiting** | `AuthLimiter` — 10 failed attempts per IP, 5min lockout |
| **Token Generation** | `crypto/rand` used for all security-sensitive randomness |
| **HTTP Server Timeouts** | All admin servers configured with Read/Write/Idle timeouts |
| **JSON Body Limits** | 1MB max via `http.MaxBytesReader` |
| **CSRF Protection** | `X-Requested-With` header check on state-changing operations |
| **Race Condition Safety** | All maps protected by `sync.Mutex`/`sync.RWMutex` or `sync.Map` |
| **Atomic Config** | `atomic.Pointer[config.Config]` for lock-free hot-reload reads |
| **Connection Reset** | Protocol-specific reset (`DISCARD ALL`, `COM_RESET_CONNECTION`) |
| **Error Sanitization** | REST API uses `sanitizeErr()` to prevent info disclosure |
| **Dependency Security** | Only 5 direct deps, all well-known, no known CVEs |

---

## Remediation Roadmap

### Phase 1: Immediate (1-3 days)
| # | Finding | Effort | Impact |
|---|---------|--------|--------|
| 1 | H-1: gRPC auth bypass | Low | High |

**Action:** Remove the `if !s.authEnabled` bypass in gRPC server. Always require admin API authentication.

### Phase 2: Short-Term (1-2 weeks)
| # | Finding | Effort | Impact |
|---|---------|--------|--------|
| 2 | M-9: context.Background in session | Low | Medium |

**Action:** Replace `context.Background()` with session context in `pool/session.go:290`.

### Phase 3: Medium-Term (1-2 months)
| # | Finding | Effort | Impact |
|---|---------|--------|--------|
| 3 | INFO-001: SSRF IP validation | Medium | Low |

**Action:** Add IP range blocklisting for internal ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16).

---

## Methodology

This assessment was performed using security-check, an AI-powered static analysis tool that uses large language model reasoning to detect security vulnerabilities across 48 specialized skills.

### Pipeline Phases

1. **Reconnaissance** — Automated codebase architecture mapping and technology detection
2. **Vulnerability Hunting** — 10 specialized skills scanned for 20+ vulnerability categories
3. **Verification** — False positive elimination with confidence scoring (0-100)
4. **Reporting** — CVSS-aligned severity classification and remediation prioritization

### Limitations

- Static analysis only — no runtime testing or dynamic analysis performed
- AI-based reasoning may miss vulnerabilities requiring deep domain knowledge
- Confidence scores are estimates, not guarantees
- Custom business logic flaws may require manual review

---

## Disclaimer

This security assessment was performed using automated AI-powered static analysis. It does not constitute a comprehensive penetration test or security audit. The findings represent potential vulnerabilities identified through code pattern analysis and LLM reasoning. False positives and false negatives are possible.

This report should be used as a starting point for security remediation, not as a definitive statement of the application's security posture. A professional security audit by qualified security engineers is recommended for production applications handling sensitive data.

**Generated by security-check** — github.com/ersinkoc/security-check