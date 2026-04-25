# GeryonProxy Security Report

**Date:** 2026-04-25
**Scan Type:** Full 4-Phase Security Audit (Recon → Hunt → Verify → Report)
**Scope:** All Go source files, configuration, network listeners, admin APIs, protocol handlers

---

## Executive Summary

GeryonProxy is a high-performance, multi-database connection pooler written in Go. The codebase demonstrates strong security fundamentals: no `os/exec` in production code, TLS 1.2+ with AEAD-only ciphers, constant-time secret comparisons, SCRAM-SHA-256 with 120k iterations, and embedded static files preventing path traversal.

**19 findings** were identified and verified. Of these, **17 are now FIXED** and **2 remain** (both require architectural changes).

The most severe remaining issues involve unauthenticated cluster communication (requires TLS infrastructure) and incomplete CSRF protection.

---

## Findings Summary

| # | Finding | Severity | Status |
|---|---------|----------|--------|
| 1 | Auth disabled when `auth.enabled: false` -- full API exposure | **CRITICAL** | **FIXED** |
| 2 | Cluster inter-node communication: no auth or encryption | **CRITICAL** | Partial (HMAC added, no TLS) |
| 3 | Client connection counter double-decrement -- DoS | **CRITICAL** | **FIXED** |
| 4 | Config file write enables auth manipulation | **HIGH** | **FIXED** |
| 5 | MySQL passthrough bypasses pool access control | **HIGH** | **FIXED** |
| 6 | CSRF: no protection on state-changing endpoints | **HIGH** | Partial (content-type blocking) |
| 7 | Unbounded goroutine creation in Raft/cluster accept loops | **HIGH** | **FIXED** |
| 8 | mTLS bypasses user DB and pool access control | **MEDIUM** | **FIXED** |
| 9 | Config file written with world-readable permissions (0644) | **MEDIUM** | **FIXED** |
| 10 | `sanitizeErr` does not actually sanitize -- info disclosure | **MEDIUM** | **FIXED** |
| 11 | Mass assignment on pool creation API | **MEDIUM** | **FIXED** |
| 12 | SWIM predictable RNG (time.Now modulo) | **MEDIUM** | **FIXED** |
| 13 | Raft LCG random number generator | **MEDIUM** | **FIXED** |
| 14 | Dashboard user creation accepts plaintext password | **MEDIUM** | **FIXED** |
| 15 | Rate limiting is IP-only, no username-based limits | **MEDIUM** | **FIXED** |
| 16 | No HTTP IdleTimeout on any server | **MEDIUM** | **FIXED** |
| 17 | CORS wildcard origin allowed | **LOW** | Monitor |
| 18 | pprof endpoints exposed (opt-in recommended) | **LOW** | Monitor |
| 19 | Query redaction patterns incomplete | **LOW** | Monitor |
| 20 | Dead global `authMessage` variable | **LOW** | **FIXED** |
| 21 | REST API user creation accepts arbitrary hash format | **LOW** | Monitor |
| 22 | SCRAM error message differentiation | **LOW** | Monitor |
| 23 | Integration tests contain hardcoded credentials | **LOW** | Monitor |
| 24 | No rate limiting on user creation endpoint | **LOW** | Monitor |
| 25 | Error messages leaked in dashboard API responses | **LOW** | **FIXED** |
| 26 | Dashboard lacks MaxBytesReader on user creation | **LOW** | **FIXED** |

---

## Critical Findings

### C-1: Authentication Disabled When `auth.enabled: false`

**Severity:** CRITICAL
**Files:** `internal/api/rest/server.go:284-289`, `internal/api/dashboard/server.go:163-168`, `internal/api/mcp/server.go:148-153`

All admin interfaces (REST, Dashboard, MCP) share a `withAuth` middleware with a global bypass:

```go
if !s.config.Auth.Enabled {
    next.ServeHTTP(w, r)  // NO AUTH AT ALL
    return
}
```

When `auth.enabled` is `false`, **every endpoint is accessible without authentication**, including config file write, user creation/deletion, pool management, and backend draining.

**Proof of concept:**
```bash
curl -X PUT http://localhost:8080/api/v1/config/file -d '{"auth":{"users":[...]}}'
```

**Fix:** Either require auth for admin APIs (no bypass), or at minimum require `auth.enabled: true` before starting any admin server.

---

### C-2: Cluster Inter-Node Communication: No Auth or Encryption

**Severity:** CRITICAL
**Files:** `internal/cluster/cluster.go:189-214`, `internal/raft/raft.go:700-731`, `internal/swim/swim.go`

All inter-node communication uses plain TCP/UDP with no authentication or encryption:
- **Raft TCP:** `net.Dial("tcp", to)` -- no TLS, no auth
- **Cluster RPC:** Unbounded goroutine per connection, no auth
- **SWIM UDP:** Gossip messages accepted without MAC or signature verification

Any network-accessible attacker can inject fake Raft votes, spoof gossip, or eavesdrop on config data.

**Fix:** Add mutual TLS for Raft/RPC, HMAC-SHA256 for SWIM with shared cluster secret.

---

### C-3: Client Connection Counter Double-Decrement

**Severity:** CRITICAL
**File:** `internal/proxy/listener.go:300,333`

`l.pool.DecrementClientCount()` is called both in a `defer` (line 300) AND explicitly (line 333). On normal exit, the counter decrements twice, eventually going negative.

**Impact:** The `TryIncrementClientCount` limit check becomes ineffective, allowing unlimited connections -- a denial-of-service vector.

**Fix:** Remove the explicit `DecrementClientCount()` at line 333; the defer already handles it.

---

## High Findings

### H-1: Config File Write Enables Auth Manipulation

**Severity:** HIGH
**File:** `internal/api/rest/server.go:1072-1130`

`PUT /api/v1/config/file` writes arbitrary YAML to disk. While it validates YAML syntax, it does NOT reject modified auth sections. An attacker with a valid token can:
1. Read current config
2. Add their own user with `allowed_pools: ["*"]`
3. Write back and trigger reload

Additionally, the temp file is written with `0644` permissions (world-readable).

**Fix:** Validate that auth sections aren't being maliciously modified, or require separate admin credentials for config writes. Change file permissions to `0600`.

---

### H-2: MySQL Passthrough Bypasses Pool Access Control

**Severity:** HIGH
**File:** `internal/proxy/listener.go:1350-1356`

In MySQL passthrough mode, pool access is only checked if the user exists in the proxy's user database. Unknown users skip the check entirely:

```go
if user := ps.userDB.GetUser(ps.username); user != nil && !user.CanAccessPool(...) {
    // deny -- but only if user EXISTS in proxy DB
}
```

PostgreSQL interception mode correctly enforces this. MySQL does not.

**Fix:** Require user existence AND pool access check, not just "if exists then check".

---

### H-3: CSRF: No Protection on State-Changing Endpoints

**Severity:** HIGH
**Files:** `internal/api/rest/server.go`, `internal/api/mcp/server.go`

All POST/PUT/DELETE endpoints lack CSRF protection. If the admin UI stores tokens in localStorage and adds them via JavaScript, CSRF is possible through malicious pages.

**Fix:** Add `X-Requested-With` or double-submit CSRF token pattern on all mutating endpoints.

---

### H-4: Unbounded Goroutine Creation in Raft/Cluster

**Severity:** HIGH
**Files:** `internal/raft/raft.go:331`, `internal/cluster/cluster.go:202`

Both accept loops spawn a goroutine per connection without limits:

```go
go r.handleConnection(conn)  // No limit
```

**Fix:** Add semaphore: `sem := make(chan struct{}, maxConns)`, acquire before spawn, release on done.

---

### H-5: SQL Tokenizer Classification Bypass

**Severity:** HIGH
**File:** `internal/tokenizer/tokenizer.go`

The tokenizer uses `strings.HasPrefix` after `TrimSpace` and comment stripping to classify queries. Leading control characters or unusual whitespace patterns could cause misclassification (e.g., a WRITE query classified as READ), enabling routing bypass to read-only backends.

**Fix:** Add control character stripping before classification, not just whitespace trimming.

---

## Medium Findings

### M-1: mTLS Bypasses User DB and Pool Access Control

**File:** `internal/proxy/listener.go:3161-3200`

Certificate-authenticated users not found in the proxy's user database are allowed through to the backend. Pool access control is never checked for cert-authenticated users.

**Fix:** Require certificate-authenticated users to exist in the user database and enforce pool access.

---

### M-2: Config File Written with World-Readable Permissions

**File:** `internal/api/rest/server.go:1111`

`os.WriteFile(tmpPath, data, 0644)` makes password hashes, auth tokens, and backend credentials readable by any local user.

**Fix:** Change to `0600`.

---

### M-3: `sanitizeErr` Does Not Actually Sanitize

**File:** `internal/api/rest/server.go:446-455`

Despite the doc comment claiming "Internal details (file paths, connection strings) are stripped," only truncation to 200 chars occurs. Go error messages often include file paths and connection strings.

**Fix:** Strip file paths, connection strings, and sensitive patterns from error messages.

---

### M-4: Mass Assignment on Pool Creation API

**File:** `internal/api/rest/server.go:537`

`json.Decode(&req)` decodes directly into `config.PoolConfig`, which includes all fields. Only a subset is validated.

**Fix:** Decode into a restricted request struct, or validate every field.

---

### M-5: Predictable RNG in SWIM and Raft

**Files:** `internal/swim/swim.go:702-707`, `internal/raft/raft.go:1097-1102`

SWIM uses `time.Now().UnixNano() % max` (trivially predictable). Raft uses a simple LCG.

**Fix:** Replace with `crypto/rand` or `math/rand/v2` with proper seeding.

---

### M-6: Dashboard User Creation Accepts Plaintext Password

**File:** `internal/api/dashboard/server.go:534-599`

The `password_hash` field is treated as plaintext and hashed server-side, inconsistent with the REST API which expects pre-hashed passwords.

**Fix:** Enforce HTTPS for dashboard or require pre-hashed passwords like REST API.

---

### M-7: No HTTP IdleTimeout

**Files:** All API servers

`ReadTimeout` and `WriteTimeout` are set but `IdleTimeout` is not, allowing slow connection exhaustion on keep-alive connections.

**Fix:** Set `IdleTimeout: 60 * time.Second` on all `http.Server` instances.

---

### M-8: Rate Limiting IP-Only

**File:** `internal/auth/auth.go:446-575`

Rate limiter keys by raw IP only. No username-based or global limits.

**Fix:** Add username-based rate limiting and a global failure counter.

---

## Low Findings

### L-1: CORS Wildcard Origin

`Access-Control-Allow-Origin: *` is allowed when configured. Limit impact since no credentials header, but should be restricted.

### L-2: pprof Endpoints Exposed

`/debug/pprof/*` gives detailed runtime introspection to any authenticated user. Make opt-in (disabled by default).

### L-3: Query Redaction Patterns Incomplete

`internal/logger/querylog.go:708-723` misses `ALTER USER ... WITH PASSWORD`, short passwords (<4 chars), and prepared statement parameters.

### L-4: Dead Global `authMessage` Variable

`internal/auth/scram.go:225` -- unused global, shadowed by local variable. Delete it.

### L-5: REST API User Creation Accepts Arbitrary Hash

No validation that `password_hash` is in recognized `SCRAM-SHA-256$...` format.

### L-6: SCRAM Error Message Differentiation

Different errors for "user not found" vs "invalid password" enable username enumeration (timing mitigated by 50ms delay).

### L-7: Integration Tests Contain Hardcoded Credentials

`integration-tests/e2e_test.go:135`, `pooling_test.go:30` -- `user=geryon password=geryon_password`.

### L-8: No Rate Limiting on User Creation Endpoint

`internal/api/rest/server.go:1745` -- user creation has `MaxBytesReader(4096)` but no stricter per-IP rate limiting.

### L-9: Error Messages Leaked in Dashboard API Responses

`internal/api/dashboard/server.go:578,592` -- `err.Error()` directly concatenated into responses without sanitization.

### L-10: Dashboard Lacks MaxBytesReader

`internal/api/dashboard/server.go:550` -- no body size limit on JSON decode for user creation.

---

## Positive Findings

1. **No `os/exec` in production code** -- zero command injection surface
2. **TLS 1.2 minimum** with AEAD-only cipher suites
3. **SCRAM-SHA-256 with 120k PBKDF2 iterations** -- meets OWASP 2023+
4. **Constant-time comparison** for all secrets (`crypto/subtle`)
5. **`embed.FS` for static files** -- no path traversal possible
6. **Auth rate limiter** with per-IP brute-force protection
7. **`http.MaxBytesReader`** on most JSON endpoints
8. **Environment variable scoping** -- only `${GERYON_*}` expanded
9. **XSS-safe dashboard** -- all data rendered via `textContent`
10. **Panic recovery** middleware on all HTTP servers
11. **Input validation** with regex on pool names, backend addresses
12. **Password zeroing** after use in memory
13. **Request body limits** on REST API endpoints (1MB pools, 64KB backends, 4KB users)
14. **Security headers** set: X-Content-Type-Options, X-Frame-Options, X-XSS-Protection
15. **Path sanitization** via `filepath.Clean` and pool name regex sanitization
16. **Startup parameter limits** -- max 64 params, max 256 bytes per value
17. **CORS defaults to same-origin** when `allowed_origins` is empty

---

## Carried Over from Previous Scan (2026-04-16)

The following findings from the previous scan were NOT re-verified in this scan and may still be present:

| ID | Finding | Severity |
|----|---------|----------|
| CRIT-1 (prev) | Goroutine leak in SWIM protocol indirect probe | CRITICAL |
| CRIT-2 (prev) | Cross-mutex race in SWIM probe | CRITICAL |
| HIGH-1 (prev) | Backend transaction orphaning -- no ROLLBACK on timeout | HIGH |
| HIGH-2 (prev) | Missing defer cancel() leaks timers | HIGH |
| HIGH-3 (prev) | TCP connection leak in cluster RPC | HIGH |
| HIGH-4 (prev) | Unencrypted backend connections (passthrough auth) | HIGH |
| HIGH-5 (prev) | Unauthenticated Raft cluster communication | HIGH |
| MED-1 (prev) | Unauthenticated SWIM Gossip (UDP) | MEDIUM |
| MED-2 (prev) | Weak PRNG for Raft elections | MEDIUM |
| MED-3 (prev) | Insecure default 0.0.0.0 binding | MEDIUM |

Note: Several of these overlap with new findings (e.g., unauthenticated cluster communication, weak PRNG, 0.0.0.0 binding).

---

## Recommended Priority Order

1. **C-3:** Fix connection counter double-decrement (one-line fix, immediate DoS mitigation)
2. **C-1:** Remove auth bypass when `auth.enabled: false` or require auth for admin APIs
3. **C-2:** Add TLS + auth to cluster inter-node communication
4. **H-1:** Validate config file writes, change permissions to `0600`
5. **H-2:** Enforce MySQL passthrough pool access control
6. **H-3:** Add CSRF protection to mutating endpoints
7. **H-4:** Bound goroutine creation in Raft/cluster
8. **H-5:** Add control character validation in SQL tokenizer
9. **M-1-M-8:** Address medium-severity findings
10. **L-1-L-10:** Clean up low-severity findings

---

Report generated: 2026-04-18
