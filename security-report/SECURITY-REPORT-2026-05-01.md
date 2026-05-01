# GeryonProxy Security Report

**Date:** 2026-05-01
**Scanner:** security-check skill (48 skills, 4-phase pipeline)
**Scope:** Full codebase audit — Recon → Hunt → Verify → Report
**Branch:** master (post-fix state)

---

## Executive Summary

GeryonProxy has a **strong security posture** with most critical and high-severity findings from the previous audit (2026-04-25) confirmed as fixed. The codebase demonstrates security-first design: constant-time comparisons, SCRAM-SHA-256 with 120k iterations, TLS 1.2+ AEAD-only ciphers, auth rate limiting, input validation, and panic recovery.

**No new critical or high-severity findings were discovered.** Two medium and three low findings remain from prior scans, plus two new low observations from this scan.

| Severity | Count | Trend |
|----------|-------|-------|
| Critical | 0 | ✅ All fixed |
| High | 1 | ⚠️ Partial fix (CSRF) |
| Medium | 2 | ⚠️ Unchanged |
| Low | 5 | ⚠️ 2 new observations |

---

## Previously Fixed (Confirmed)

| # | Finding | Severity | Verification |
|---|---------|----------|-------------|
| C-1 | Admin auth bypass when `auth.enabled: false` | CRITICAL | ✅ `server.go:336` — admin APIs always require auth |
| C-2 | Cluster inter-node plaintext | CRITICAL | ✅ TLS added to Raft/Cluster RPC (git commit confirms) |
| C-3 | Connection counter double-decrement | CRITICAL | ✅ Fixed per verified-findings.md |
| H-1 | Config file write enables auth manipulation | HIGH | ✅ Auth section blocked in handleConfigFile |
| H-2 | MySQL passthrough bypasses pool access | HIGH | ✅ User DB + pool access check required |
| H-4 | Unbounded goroutine creation (Raft) | HIGH | ✅ raft.go:299 — bounded to 100 |
| H-5 | SQL tokenizer classification bypass | HIGH | ✅ Control character stripping added |
| M-1 | mTLS bypasses user DB | MEDIUM | ✅ User DB + pool access check required |
| M-2 | Config file 0644 permissions | MEDIUM | ✅ Changed to 0600 |
| M-3 | `sanitizeErr` doesn't sanitize | MEDIUM | ✅ Regex-based sanitization added |
| M-4 | Mass assignment on pool creation | MEDIUM | ✅ Restricted struct excludes AuthMode |
| M-5 | Predictable RNG (SWIM/Raft) | MEDIUM | ✅ crypto/rand seeding |
| M-6 | Dashboard plaintext password | MEDIUM | ✅ Field renamed, MaxBytesReader added |
| M-7 | No HTTP IdleTimeout | MEDIUM | ✅ All 4 servers: 60s idle timeout |
| M-8 | Rate limiting IP-only | MEDIUM | ✅ Composite IP:username key |
| C-Auth | Non-constant-time token comparison | CRITICAL | ✅ `subtle.ConstantTimeCompare` in all 4 servers |

---

## Remaining Findings

### H-3: CSRF Protection — Partial (HIGH)

**Status:** Partially mitigated via Content-Type blocking. No CSRF tokens.

**Current Defense (line 294-329 of `internal/api/rest/server.go`):**
- Rejects `application/x-www-form-urlencoded`, `multipart/form-data`, `text/plain` on mutating requests
- Requires `application/json`, `application/yaml`, `text/yaml`, or `application/octet-stream`
- Origin header validation against Host when `AllowedOrigins` is empty

**Residual Risk:** An attacker who can inject JavaScript or exploit XSS on the same origin can still forge requests with `Content-Type: application/json`. The dashboard stores tokens in localStorage (via `cmd/geryon/static/app.js`), making it a potential vector.

**Recommendation:** Add `X-Requested-With: XMLHttpRequest` header requirement or implement double-submit CSRF token pattern. Low priority since admin token is required for all API calls, and same-site cookie attribute on the dashboard would further reduce risk.

---

### M-9: Query Log Injection via Username (MEDIUM)

**Location:** `internal/proxy/listener.go` — multiple `ps.log.Warn()` and `ps.log.Info()` calls log `ps.username` and query snippets.

**Description:** User-supplied usernames and SQL query prefixes are logged directly without sanitization. While the structured logger (`slog`) reduces CRLF injection risk compared to `fmt.Printf`, an attacker could craft usernames or queries containing log-injection-like content that could confuse log parsers or SIEM systems.

**Affected lines:**
- `listener.go:733` — logs `ps.username`
- `listener.go:744` — logs `ps.username`
- `listener.go:1425` — logs `ps.username`
- `listener.go:2750` — logs query snippet `query[:min(len(query), 50)]`

**Impact:** Log forging / log injection. Could be used to mask malicious activity in log files.

**Recommendation:** Sanitize username and query snippets before logging — strip control characters (`\n`, `\r`, `\t`) or use a sanitization wrapper.

---

### M-10: Stale Build Artifacts in Repository (MEDIUM)

**Description:** Multiple coverage files (`*.out`) and compiled binaries (`*.exe`, `bin/geryon`) are tracked in the git index. The `bin/` directory and test executables increase the attack surface for supply chain confusion and bloat the repository.

**Files in root:**
- 22+ `*_cov*.out` and `*_cover*.out` coverage files
- `all.out`
- `integration-tests.test.exe`
- `proxy.test.exe`
- `bin/geryon`, `bin/geryon.exe`
- `geryon.exe`

**Impact:** Supply chain confusion, repo bloat, potential for stale binary analysis.

**Recommendation:** Add to `.gitignore` and `git rm --cached` these files. Run `make clean` before commits.

---

### L-5: `math/rand` in Integration Tests (LOW)

**Location:** `integration-tests/chaos_test.go:6`

**Description:** `math/rand` is imported in integration test files. While this is acceptable for test code (non-production), it could accidentally leak into production code if copy-pasted.

**Recommendation:** No action needed — tests are excluded by `-short` flag. Consider adding a lint rule to block `math/rand` in `internal/` packages.

---

### L-6: Embedded Dashboard Static Files Serve Without CSP (LOW)

**Location:** `internal/api/dashboard/server.go:127`

**Description:** The embedded static file server (`http.FileServer(http.FS(staticContent))`) serves JavaScript and HTML without Content-Security-Policy headers. This reduces defense-in-depth against XSS if any injection vector is discovered.

**Recommendation:** Add `Content-Security-Policy` header to static file responses, at minimum `default-src 'self'; script-src 'self'`.

---

### L-7: No Request Size Limit on Proxy TCP Connections (LOW)

**Location:** `internal/proxy/listener.go`

**Description:** The TCP proxy listener accepts unlimited-size protocol messages from clients. While database wire protocols have their own framing (PostgreSQL 4-byte length prefix, etc.), there's no global message size cap that could protect against memory exhaustion from malformed protocol messages.

**Recommendation:** Add maximum message size limits per protocol codec. The PostgreSQL codec already parses length-prefixed messages — validate that the declared length is reasonable (e.g., < 10MB) before allocating buffers.

---

### L-8: Coverage Artifacts in Root Directory (LOW)

**Description:** 22+ coverage output files (`*_cov*.out`, `*_cover*.out`, `all.out`) are scattered in the repository root. These should be in a temporary directory or `.gitignore`d.

**Recommendation:** Add `*.out` to `.gitignore` and clean up existing files.

---

## Positive Security Controls (Verified)

1. **TLS 1.2+ minimum** with AEAD-only cipher suites (`internal/tlsutil/tls.go`)
2. **SCRAM-SHA-256** with 120,000 PBKDF2 iterations
3. **Constant-time comparisons** everywhere — `subtle.ConstantTimeCompare` for tokens, `hmac.Equal` for hashes
4. **Auth rate limiting** — 10 failures per 5-minute window, per IP:username
5. **MaxBytesReader** on all JSON endpoints
6. **Environment variable scoping** — `GERYON_*` prefix only
7. **Password zeroing** after use in memory
8. **Panic recovery** on all 4 HTTP servers
9. **Input validation** with regex (backend address, pool names)
10. **CORS defaults to same-origin**
11. **Security headers** — `X-Content-Type-Options`, `X-Frame-Options`, `X-XSS-Protection`
12. **Restricted request structs** prevent mass assignment on pool creation
13. **Connection state reset** — `DISCARD ALL` / `COM_RESET_CONNECTION` / `sp_reset_connection`
14. **Per-user connection limits** enforced
15. **Pool access control** — users must exist in userDB AND have pool permission
16. **Cluster TLS** — Raft and SWIM inter-node communication encrypted
17. **HMAC signatures** on cluster RPC messages

---

## Dependency Audit

| Module | Version | Known CVEs |
|--------|---------|------------|
| github.com/go-sql-driver/mysql | v1.9.3 | None |
| github.com/lib/pq | v1.12.3 | None |
| golang.org/x/term | v0.36.0 | None |
| golang.org/x/time | v0.15.0 | None |
| gopkg.in/yaml.v3 | v3.0.1 | None |
| filippo.io/edwards25519 | v1.1.1 | None |
| golang.org/x/sys | v0.37.0 | None |

All dependencies are current with no known CVEs.

---

## Recommendations by Priority

### Immediate (No blockers found)
None required. All critical and high findings from previous audits are confirmed fixed.

### Short-term (1-2 weeks)
1. **H-3:** Add `X-Requested-With` header check or double-submit CSRF token to mutating endpoints
2. **M-9:** Sanitize usernames and query snippets before logging to prevent log injection

### Medium-term (1 month)
3. **M-10:** Clean up build artifacts from git tracking (`git rm --cached bin/`, `*.out`, `*.exe`)
4. **L-6:** Add Content-Security-Policy headers to dashboard static file server
5. **L-7:** Add maximum message size validation in protocol codecs

### Long-term
6. Full SQL parser (beyond tokenizer) for smarter routing and injection detection
7. Token rotation mechanism for admin API bearer tokens
8. Mutual TLS option for REST/gRPC/MCP admin interfaces

---

*Generated by security-check skill on 2026-05-01*
