# Verified Security Findings

**Scan Date:** 2026-05-01
**Project:** GeryonProxy (github.com/GeryonProxy/geryon)
**Scanner:** security-check pipeline

## Summary
- Total raw findings from Phase 2: 10 skills executed
- After duplicate merging: 3 findings
- After false positive elimination: 2 findings
- Final verified findings: 2

## Confidence Distribution
- Confirmed (90-100): 1
- High Probability (70-89): 0
- Probable (50-69): 1
- Possible (30-49): 0
- Low Confidence (0-29): 0

## Verified Findings

### VULN-001: gRPC Admin API Authentication Bypass When Auth is Disabled

- **Severity:** High
- **Confidence:** 85/100 (High Probability)
- **Original Skill:** sc-authz
- **Vulnerability Type:** CWE-306 — Missing Authentication for Critical Function
- **File:** `internal/api/grpc/server.go:176`
- **Reachability:** Direct (HTTP handler)
- **Sanitization:** None (auth check is the control itself)
- **Framework Protection:** None
- **Description:** When `auth.enabled=false` is set in config, the gRPC server's `withAuth` middleware skips authentication entirely. This makes all admin API endpoints publicly accessible without any credentials. REST and MCP servers correctly always require authentication regardless of the `auth.enabled` config flag.

- **Verification Notes:**
  - Confirmed: `if !s.authEnabled { return next(w, r) }` at line 176 bypasses `withAuth`
  - REST server (`internal/api/rest/server.go:342`) always calls `withAuth` — does not check `authEnabled`
  - MCP server (`internal/api/mcp/server.go`) — verified always requires auth
  - Only the gRPC server has this bypass logic
  - If `auth.enabled=false` in config, all gRPC endpoints (pool management, config reload, cluster ops) are accessible without credentials

- **Remediation:** Remove the `if !s.authEnabled` bypass in gRPC server. Always require authentication regardless of `auth.enabled` config setting, or align with REST/MCP behavior. The `auth.enabled` config should only control whether proxy clients authenticate to Geryon, not whether admin API requires auth.

- **Reference:** [CWE-306](https://cwe.mitre.org/data/definitions/306.html)

---

### VULN-002: context.Background() Used in Session Message Handler

- **Severity:** Medium
- **Confidence:** 65/100 (Probable)
- **Original Skill:** sc-lang-go
- **Vulnerability Type:** CWE-400 — Resource Exhaustion (unbounded goroutine waiting)
- **File:** `internal/pool/session.go:290`
- **Reachability:** Indirect (reached from proxy session loop via HandleMessage)
- **Sanitization:** N/A (context issue, not input validation)
- **Framework Protection:** None
- **Description:** The `HandleMessage` method in session.go uses `context.Background()` when acquiring a server connection for query processing. This means client disconnection is not honored during connection acquisition — a client that disconnects while waiting for a pool connection will have its goroutine continue until a connection is available or the pool's wait timeout expires.

- **Verification Notes:**
  - Confirmed at line 290: `ctx := context.Background()` in HandleMessage
  - Session is created per-client-connection from `NewProxySession` in `proxy/listener.go`
  - The parent context from the proxy session is available and should be used instead
  - Bounded by pool's `WaitTimeout` — not completely unbounded
  - No sensitive info leaked in error path (uses `sanitizeErr()` in REST API, generic errors in proxy)
  - This is a resource management issue rather than information disclosure

- **Remediation:** Replace `context.Background()` with the session's parent context that respects client disconnection:
  ```go
  // Before (line 290):
  ctx := context.Background()

  // After — use parent session context:
  ctx := s.ctx  // or derive from s.parentCtx
  ```

- **Reference:** [Go Context Documentation](https://pkg.go.dev/context), [CWE-400](https://cwe.mitre.org/data/definitions/400.html)

---

## Informational Findings

### INFO-001: Admin API SSRF Risk — Backend Address Validation

- **Severity:** Info
- **Confidence:** 40/100 (Possible)
- **Original Skill:** sc-ssrf
- **Vulnerability Type:** CWE-918 — Server-Side Request Forgery (SSRF)
- **File:** `internal/api/rest/server.go` (backend config endpoints)
- **Reachability:** Requires authenticated admin access
- **Description:** The Admin API allows backend addresses to be configured to arbitrary IP addresses (e.g., internal cloud metadata endpoints like 169.254.169.254). The `validateBackendAddr()` function only checks address format, not IP ranges or blocklists.
- **Impact:** An authenticated admin could configure a backend to point at internal services (cloud metadata, internal databases). This requires valid admin credentials — not client-exploitable.
- **Remediation:** Add IP range blocklisting for known internal ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16) in `validateBackendAddr()`.
- **Reference:** [CWE-918](https://cwe.mitre.org/data/definitions/918.html)

---

## Eliminated Findings (False Positives)

| Finding | Reason Eliminated |
|---------|-------------------|
| SC-001: REST API constant-time token comparison | Already remediated in recent commits — all 4 admin interfaces now use `subtle.ConstantTimeCompare` |
| auth bypass in REST/MCP | Both servers correctly always require auth regardless of `auth.enabled` |
| Hardcoded secrets in test files | Test fixtures use placeholder values (mock passwords for redaction testing), not production secrets |
| SQL injection in routing | Transparent pass-through proxy — no dynamic SQL construction from user input |
| Path traversal in config | File paths are operator-controlled via CLI flags, not client-influenced |
| RCE via exec.Command | Only in test files (integration test Docker/Go build commands), not production code |
| CORS misconfiguration | Not a web app — database proxy uses wire protocols, not HTTP for client traffic |

---

## Overall Assessment

**Risk Score:** 5.3/10 (Moderate Risk)

GeryonProxy has a strong security baseline with proper authentication (SCRAM-SHA-256), rate limiting, and careful resource management. The two verified findings represent:
- 1 High (auth bypass — could expose admin API without credentials in specific config)
- 1 Medium (context misuse — bounded resource exhaustion risk)

No critical vulnerabilities were found. The codebase demonstrates good security practices in most areas.