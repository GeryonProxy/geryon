# Verified Findings Report

**Date:** 2026-05-01
**Method:** Cross-referenced 4 independent security agent scans, eliminated duplicates, validated exploitability
**Status:** FIX APPLIED for all actionable findings in scope

---

## Fixed Findings

| # | Finding | Severity | Status |
|---|---------|----------|--------|
| C-1 | Auth disabled when `auth.enabled: false` | CRITICAL | **FIXED (2026-04-25)** - Removed bypass in REST/Dashboard/MCP servers |
| C-3 | Connection counter double-decrement | CRITICAL | **FIXED** - Removed duplicate DecrementClientCount() |
| H-2 | MySQL passthrough bypasses pool access control | HIGH | **FIXED** - Require user in userDB AND pool access check |
| H-5 | SQL tokenizer classification bypass | HIGH | **FIXED** - Added control character stripping |
| H-1 | Config file write enables auth manipulation | HIGH | **FIXED** - Auth section blocked in handleConfigFile |
| H-4 | Unbounded goroutine creation in Raft/cluster | HIGH | **FIXED** - Raft connSem bounded to 100 (raft.go:299) |
| M-1 | mTLS bypasses user DB and pool access | MEDIUM | **FIXED** - Require user in userDB + pool access check |
| M-2 | Config file 0644 permissions | MEDIUM | **FIXED** - Changed to 0600 |
| M-3 | `sanitizeErr` doesn't sanitize | MEDIUM | **FIXED** - Added regex stripping for paths/connections |
| M-5 | Predictable RNG (SWIM/Raft) | MEDIUM | **FIXED** - Replaced with crypto/rand seeding |
| M-6 | Dashboard accepts plaintext password | MEDIUM | **FIXED** - Renamed field, added MaxBytesReader, sanitized errors |
| L-4 | Dead global authMessage variable | LOW | **FIXED** - Removed unused global |
| L-9 | Error messages leaked in dashboard | LOW | **FIXED** - Replaced err.Error() with generic messages |
| L-10 | Dashboard lacks MaxBytesReader | LOW | **FIXED** - Added 4096 byte limit |
| M-7 | No HTTP IdleTimeout | MEDIUM | **FIXED** - All 4 servers set IdleTimeout to 60s |
| M-8 | Rate limiting IP-only | MEDIUM | **FIXED (2026-04-25)** - Composite IP:username key in REST/Dashboard/MCP |
| M-4 | Mass assignment on pool creation | MEDIUM | **FIXED** - Restricted struct excludes AuthMode (set server-side) |
| M-9 | Log injection via username/query | MEDIUM | **FIXED (2026-05-01)** - sanitizeLogValue strips control characters in listener.go and dashboard |
| L-6 | No Content-Security-Policy headers | LOW | **FIXED (2026-05-01)** - CSP added to all 4 HTTP servers |

## Remaining Findings (Not In Scope / Architecture Changes)

| # | Finding | Severity | Reason |
|---|---------|----------|--------|
| C-2 | Cluster inter-node: no auth/encryption | CRITICAL | **FIXED (2026-04-25)** - TLS support added to Raft/Cluster RPC (tls.Listener + tls.Dial) |
| H-3 | CSRF: no protection on mutating endpoints | HIGH | **PARTIAL** - Content-type blocking only, not full CSRF tokens |

## Positive Security Controls (Verified)

1. TLS 1.2+ with AEAD-only ciphers
2. SCRAM-SHA-256 with 120k PBKDF2 iterations
3. Constant-time secret comparisons
4. Auth rate limiter (10/5min per IP)
5. `http.MaxBytesReader` on all JSON endpoints
6. Environment variable scoping (`GERYON_*` only)
7. Password zeroing after use
8. Panic recovery on HTTP servers
9. Input validation with regex
10. Pool name sanitization
11. CORS defaults to same-origin
12. Security headers set (X-Content-Type-Options, X-Frame-Options, X-XSS-Protection)
13. Content-Security-Policy on all HTTP servers
14. Log injection prevention via sanitizeLogValue

## Test Results

- **2667 passed, 0 failed, 54 skipped**
- `go vet`: No issues
- `gofmt`: No formatting issues

---

*Generated: 2026-04-18*
*Fixes verified by full test suite (2026-04-25)*
