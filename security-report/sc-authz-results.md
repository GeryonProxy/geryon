# sc-authz-results.md - Authorization Security Audit

## Summary

Issues found: **1 high**

| Severity | Count |
|----------|-------|
| Critical | 0 |
| High | 1 |
| Medium | 0 |
| Low | 0 |

---

## Findings

### H-1: gRPC Admin API allows unauthenticated access when auth.enabled=false

- **Severity:** High
- **File:** `internal/api/grpc/server.go:174-178`
- **Details:**

The gRPC server's `withAuth` middleware has a bypass when `auth.enabled=false` in configuration:

```go
func (s *Server) withAuth(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !s.authEnabled {
            next.ServeHTTP(w, r)  // BYPASS: unauthenticated access allowed
            return
        }
        // ... authentication logic ...
    })
}
```

When `auth.enabled=false`, all gRPC endpoints become publicly accessible without any authentication. This includes:
- `/geryon.v1.Admin/ReloadConfig` — configuration reload
- `/geryon.v1.Admin/DrainBackend` — backend drain operations
- All stats and events endpoints

**Impact:** If an operator disables auth on the gRPC admin interface (intending it for internal use only), the endpoints remain unauthenticated and exposed.

**Recommendation:** The gRPC server should follow the same pattern as REST and MCP servers, where admin API access ALWAYS requires authentication regardless of the `auth.enabled` config flag. The `auth.enabled` config option should only control proxy-client database authentication, not admin API access.

---

## Passed Security Checks

### 1. REST Admin API Authorization
- **Status:** PASS
- **File:** `internal/api/rest/server.go:342-370`
- **Details:**
  - `withAuth` middleware ALWAYS requires authentication regardless of `auth.enabled` flag
  - Comment at line 343: "C-1 FIX: Admin APIs ALWAYS require authentication regardless of auth.enabled flag"
  - All routes wrapped at line 136: `s.withAuth(mux)`
  - Uses `subtle.ConstantTimeCompare` for token comparison

### 2. MCP Server Authorization
- **Status:** PASS
- **File:** `internal/api/mcp/server.go:176-212`
- **Details:**
  - Comment at line 177: "C-1 FIX: Auth is always required for MCP server regardless of auth.enabled flag"
  - `withAuth` always requires valid bearer token
  - Uses `subtle.ConstantTimeCompare` for token comparison

### 3. REST API Route Coverage
- **Status:** PASS
- **Details:**
  - All `/api/v1/*` routes protected by `withAuth`
  - Sensitive endpoints (`/metrics`, `/debug/pprof/*`) use `requireAuth` middleware
  - Pool management endpoints require auth
  - Config reload requires auth

### 4. MCP Server Tools Authorization
- **Status:** PASS
- **File:** `internal/api/mcp/tools.go`
- **Details:**
  - All tools (pool list, stats, connection list, backend drain, config reload, etc.) are invoked via `handleToolsCall` which is behind `withAuth` middleware
  - No direct tool invocation without authentication

### 5. Per-User Pool Access Control
- **Status:** PASS
- **Details:**
  - GeryonProxy uses a single shared admin token for all operations, not per-user tokens
  - Since all authenticated users share the same token, there is no concept of one user accessing another user's pools
  - The admin token grants full access to all pools and operations
  - This is acceptable for the proxy use case where admin credentials are shared among authorized operators

### 6. Config Reload Authorization
- **Status:** PASS
- **Details:**
  - REST: `/api/v1/config/reload` protected by `withAuth`
  - gRPC: `/geryon.v1.Admin/ReloadConfig` protected by `withAuth`
  - MCP: `toolConfigReload()` invoked via authenticated tool call
  - All require valid bearer token

---

## Remediation Required

| ID | Severity | Description | File | Line |
|----|----------|-------------|------|------|
| H-1 | High | gRPC allows unauthenticated access when auth.enabled=false | server.go | 174-178 |

**Fix needed:** Remove the `if !s.authEnabled` bypass in gRPC `withAuth`, similar to REST and MCP implementations.