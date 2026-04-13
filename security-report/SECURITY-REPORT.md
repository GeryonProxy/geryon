# Security Assessment Report

**Project:** GeryonProxy — Multi-Database Connection Pooler
**Date:** 2026-04-13
**Scanner:** security-check v1.0.0
**Risk Score:** 6.1/10 (Medium Risk)

---

## Executive Summary

A security assessment was performed on GeryonProxy, a pure Go database connection pooler and proxy that speaks PostgreSQL, MySQL, and MSSQL wire protocols, using 17 automated security skills across 30+ vulnerability categories. The scan analyzed 108 Go source files across the codebase.

### Key Metrics
| Metric | Value |
|--------|-------|
| Total Findings | 8 |
| Critical | 1 |
| High | 2 |
| Medium | 3 |
| Low | 2 |
| Info | 0 |

### Top Risks
1. **Non-constant-time token comparison** (Critical) — Admin APIs vulnerable to timing attacks
2. **Orphaned backend transactions** (High) — Database lock exhaustion from timed-out transactions
3. **Race condition in session handling** (High) — Data race on `lastQuery` field
4. **Race condition in SWIM gossip** (Medium) — Cross-structure race in cluster code

---

## Scan Statistics

| Statistic | Value |
|-----------|-------|
| Files Scanned | 108 .go files |
| Languages Detected | Go (100%) |
| Skills Executed | 17 |
| Skills with Findings | 4 |
| Findings Before Verification | 12 |
| False Positives Eliminated | 4 |
| Final Verified Findings | 8 |

### Finding Distribution by Category

| Vulnerability Category | Critical | High | Medium | Low | Info |
|-----------------------|----------|------|--------|-----|------|
| Authentication | 1 | 0 | 0 | 0 | 0 |
| Race Conditions | 0 | 2 | 1 | 0 | 0 |
| Resource Management | 0 | 0 | 2 | 2 | 0 |
| Timing Attacks | 1 | 0 | 0 | 0 | 0 |

---

## Critical Findings

### VULN-001: Non-Constant-Time Token Comparison in Admin APIs

**Severity:** Critical
**Confidence:** 85/100
**CWE:** CWE-208 — Observable Timing Discrepancy
**OWASP:** A02:2021 — Cryptographic Failures

**Location:** `internal/api/rest/server.go:222-248`, `internal/api/grpc/server.go:145-172`, `internal/api/mcp/server.go`, `internal/api/dashboard/server.go`

**Description:**
All four admin interfaces (REST, gRPC, MCP, Dashboard) use direct string comparison (`!=`) for bearer token validation instead of `subtle.ConstantTimeCompare`. This allows timing attacks to deduce the token value character by character by measuring response times.

**Vulnerable Code:**
```go
// internal/api/rest/server.go:246
parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
if len(parts) != 2 || parts[0] != "Bearer" || parts[1] != s.config.Auth.Token {
    w.Header().Set("WWW-Authenticate", `Bearer realm="geryon"`)
    http.Error(w, "invalid token", http.StatusUnauthorized)
    return
}
```

**Impact:**
An attacker can perform a timing attack to progressively determine the admin token character by character. Once the token is known, full administrative access is obtained, including ability to reload configuration, view metrics, and manage pools.

**Remediation:**
```go
import "crypto/subtle"

func validToken(token, expected string) bool {
    return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}
```

**References:**
- https://cwe.mitre.org/data/definitions/208.html
- https://owasp.org/Top10/A02_2021-Cryptographic_Failures/
- https://pkg.go.dev/crypto/subtle#ConstantTimeCompare

---

## High Findings

### VULN-002: Orphaned Backend Transactions

**Severity:** High
**Confidence:** 75/100
**CWE:** CWE-400 — Uncontrolled Resource Consumption
**OWASP:** A04:2021 — Insecure Design

**Location:** `internal/pool/transaction.go:200-266`

**Description:**
When transactions timeout, `checkTimeouts()` sets status to `TxnAborted`/`TxnIdle` but does NOT send `ROLLBACK` to the backend database server. Orphaned transactions hold database locks until the backend's own timeout fires (potentially hours later).

**Vulnerable Code:**
```go
// internal/pool/transaction.go:225-240 (approximate)
func (tm *TransactionManager) checkTimeouts() {
    for _, txn := range tm.transactions {
        if txn.IsTimeout(tm.maxLifetime) {
            txn.abortLocked() // Only updates internal state
            // Missing: txn.backendConn.Execute("ROLLBACK")
        }
    }
}
```

**Proof of Concept:**
1. Client connects to GeryonProxy and issues `BEGIN`
2. Client sends query that acquires row lock (e.g., `SELECT * FROM accounts FOR UPDATE`)
3. Client stops sending requests (simulates think time)
4. GeryonProxy marks transaction as timed out after 30 minutes
5. Backend continues to hold row lock until backend's timeout (default 1 hour to several hours, or never)
6. Repeated attacks can exhaust database lock resources

**Impact:**
Malicious clients can cause database lock exhaustion, leading to denial of service for other legitimate users. This is particularly dangerous in transaction mode where locks accumulate.

**Remediation:**
Send `ROLLBACK` to the backend when a transaction times out:
```go
func (tm *TransactionManager) handleTimeout(txn *Transaction) {
    if txn.backendConn != nil {
        txn.backendConn.Execute("ROLLBACK")
    }
    txn.abortLocked()
}
```

**References:**
- https://cwe.mitre.org/data/definitions/400.html
- https://www.postgresql.org/docs/current/sql-rollback.html

---

### VULN-003: Race Condition in Session.lastQuery Access

**Severity:** High
**Confidence:** 80/100
**CWE:** CWE-362 — Race Condition
**OWASP:** A04:2021 — Insecure Design

**Location:** `internal/pool/session.go:308`

**Description:**
`HandleMessage` writes to `s.lastQuery` without holding the mutex `s.mu`, while other methods (`GetLastQuery`, `SetLastQuery`, `LastQuery`) properly acquire the mutex. This is a classic data race that can cause data corruption, crashes, or inconsistent state under concurrent load.

**Vulnerable Code:**
```go
// internal/pool/session.go:308 (approximate)
func (s *Session) HandleMessage(msg *Message) error {
    // ... other code ...
    s.lastQuery = msg.Query // WRITE without mutex!
    // ...
}
```

Other methods properly use the mutex:
```go
func (s *Session) GetLastQuery() string {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.lastQuery
}
```

**Proof of Concept:**
```go
// Thread 1
go func() {
    for {
        session.HandleMessage(msg1) // writes lastQuery
    }
}()

// Thread 2
go func() {
    for {
        session.GetLastQuery() // reads with mutex
    }
}()
```

Running `go test -race` would detect this as a data race.

**Impact:**
Under concurrent client load, this race condition can cause:
- Data corruption of `lastQuery` field
- Crashes from concurrent map writes (if lastQuery was a map)
- Inconsistent behavior in monitoring/debugging

**Remediation:**
Use atomic.Value for thread-safe lastQuery storage:
```go
import "sync/atomic"

type Session struct {
    lastQuery atomic.Value // instead of string
}

func (s *Session) HandleMessage(msg *Message) error {
    s.lastQuery.Store(msg.Query)
}
```

Or acquire the mutex around the write in HandleMessage.

**References:**
- https://cwe.mitre.org/data/definitions/362.html
- https://go.dev/blog/race-detector
- https://go.dev/pkg/sync/atomic/

---

## Medium Findings

### VULN-004: context.Background() in Request Handler

**Severity:** Medium
**Confidence:** 65/100
**CWE:** CWE-675 — Duplicate Operations on Resource
**OWASP:** A04:2021 — Insecure Design

**Location:** `internal/pool/session.go:290`

**Description:**
`HandleMessage()` uses `context.Background()` when acquiring a server connection via strategy. This creates an uncancellable operation that cannot be traced or cancelled when the session terminates.

**Impact:**
If a client disconnects while `strategy.OnQuery(ctx, s, msg)` is in progress, the operation continues using resources until it naturally completes. Under heavy load with many closing connections, this could contribute to resource exhaustion.

**Remediation:**
Use a context derived from the session's lifecycle:
```go
func (s *Session) HandleMessage(msg *Message) error {
    ctx, cancel := context.WithCancel(s.sessionCtx)
    defer cancel()
    // ... use ctx in strategy.OnQuery ...
}
```

---

### VULN-005: Ignored Write Errors in Relay Goroutines

**Severity:** Medium
**Confidence:** 60/100
**CWE:** CWE-755 — Improper Handling of Exceptional Conditions
**OWASP:** A04:2021 — Insecure Design

**Location:** `internal/proxy/listener.go` (relay operations)

**Description:**
When relaying data between client and backend connections, write errors may be silently ignored. If a write fails (e.g., client connection is slow or closed), the goroutine may continue reading from the other direction.

**Impact:**
A slow client could cause the proxy to buffer data indefinitely, or close the connection to trigger resource waste in relay goroutines that don't properly terminate on write failures.

**Remediation:**
Properly handle all io.Copy errors:
```go
_, err := io.Copy(w, r)
if err != nil {
    // Terminate the other direction's copy
    cancel()
    return err
}
```

---

### VULN-006: Race Condition in Cluster SWIM Gossip

**Severity:** Medium
**Confidence:** 55/100
**CWE:** CWE-362 — Race Condition
**OWASP:** A04:2021 — Insecure Design

**Location:** `internal/cluster/cluster.go:644-649`

**Description:**
In `SwimGossip.probe()`, the node's `LastSeen` and `State` fields are modified after releasing `s.mu`, but these fields are also accessed by `Cluster` methods under `c.mu`. Cross-structure race between SWIM mutex and Cluster mutex.

**Impact:**
Under cluster gossip operations, concurrent access to node fields could cause inconsistent cluster state or crashes.

**Remediation:**
Hold `s.mu` through the entire probe operation, or use atomic operations for `LastSeen` and `State` fields.

---

## Low Findings

### VULN-007: Unbounded Rate Limiter Map Growth

**Severity:** Low
**Confidence:** 45/100
**CWE:** CWE-400 — Uncontrolled Resource Consumption
**OWASP:** A04:2021 — Insecure Design

**Location:** `internal/api/rest/server.go:251-318` and similar in grpc/mcp/dashboard

**Description:**
Rate limiter maps can grow to 10,000 entries before eviction. Under a coordinated attack with many unique IPs, memory could grow significantly before the 5-minute cleanup cycle removes stale entries.

**Impact:**
A distributed attack (botnet) could cause memory pressure before cleanup occurs.

**Remediation:**
Consider immediate eviction when `maxSize` is reached, or use `sync.Map` with TTL-based expiration.

---

### VULN-008: Missing Idle Timeout on Backend Connections

**Severity:** Low
**Confidence:** 40/100
**CWE:** CWE-404 — Improper Resource Shutdown or Release
**OWASP:** A04:2021 — Insecure Design

**Location:** `internal/pool/pool.go:754-804`

**Description:**
Backend connections track `lastUsedAt` but don't enforce idle timeouts. Stale connections could persist indefinitely if a backend becomes unavailable.

**Impact:**
Stale connections consume resources and could cause the pool to think backends are healthy when they are not.

**Remediation:**
Implement `MaxIdleTime` that closes connections unused for a configurable period.

---

## Remediation Roadmap

### Phase 1: Immediate (1-3 days)
Address all Critical findings. These represent immediate security risks.

| # | Finding | Effort | Impact |
|---|---------|--------|--------|
| 1 | VULN-001: Non-constant-time token comparison | Low | Critical |
| 2 | VULN-002: Orphaned backend transactions | Medium | High |

### Phase 2: Short-Term (1-2 weeks)
Address High findings and any quick-win Medium findings.

| # | Finding | Effort | Impact |
|---|---------|--------|--------|
| 3 | VULN-003: Race condition in session handling | Low | High |
| 4 | VULN-004: context.Background() misuse | Low | Medium |
| 5 | VULN-005: Ignored relay write errors | Medium | Medium |
| 6 | VULN-006: Race condition in SWIM gossip | Medium | Medium |

### Phase 3: Medium-Term (1-2 months)
Address remaining Medium findings and Low findings.

| # | Finding | Effort | Impact |
|---|---------|--------|--------|
| 7 | VULN-007: Unbounded rate limiter maps | Low | Low |
| 8 | VULN-008: Missing idle timeout | Medium | Low |

### Phase 4: Hardening (Ongoing)
Defense-in-depth improvements.

| # | Recommendation | Effort | Impact |
|---|---------------|--------|--------|
| 1 | Add go test -race to CI | Low | Info |
| 2 | Consider adding transaction audit logging | Medium | Info |
| 3 | Add connection pool metrics for orphaned txns | Low | Info |

---

## Positive Security Observations

The following security measures are properly implemented:

1. **Zero-dependency philosophy** — Minimal attack surface from dependencies (only golang.org/x/term and golang.org/x/time)
2. **CGO_ENABLED=0** — No CGo boundary vulnerabilities
3. **TLS 1.2 minimum** — Strong TLS configuration with secure ciphers
4. **crypto/rand usage** — Cryptographically secure random for all security-sensitive operations
5. **Mutex-protected maps** — All concurrent map access properly synchronized
6. **SCRAM-SHA-256** — NIST-compliant password hashing with PBKDF2
7. **Constant-time comparison for passwords** — Password verification uses subtle.ConstantTimeCompare
8. **Payload size limits** — 16MB max in all protocol codecs
9. **Proper defer patterns** — Connection lifecycle properly managed
10. **Rate limiting** — Per-IP token buckets with brute force protection

---

## Methodology

This assessment was performed using security-check, an AI-powered static analysis tool that uses large language model reasoning to detect security vulnerabilities.

### Pipeline Phases
1. **Reconnaissance** — Automated codebase architecture mapping and technology detection
2. **Vulnerability Hunting** — 17 specialized skills scanned for 30+ vulnerability categories
3. **Verification** — False positive elimination with confidence scoring (0-100)
4. **Reporting** — CVSS-aligned severity classification and remediation prioritization

### Limitations
- Static analysis only — no runtime testing or dynamic analysis performed
- AI-based reasoning may miss vulnerabilities requiring deep domain knowledge
- Confidence scores are estimates, not guarantees
- Custom business logic flaws may require manual review
- Race conditions require concurrent load to manifest

---

## Disclaimer

This security assessment was performed using automated AI-powered static analysis. It does not constitute a comprehensive penetration test or security audit. The findings represent potential vulnerabilities identified through code pattern analysis and LLM reasoning. False positives and false negatives are possible.

This report should be used as a starting point for security remediation, not as a definitive statement of the application's security posture. A professional security audit by qualified security engineers is recommended for production applications handling sensitive data.

Generated by security-check — github.com/ersinkoc/security-check
