# GeryonProxy Access Control Security Audit

**Project:** GeryonProxy - Multi-Database Connection Pooler
**Date:** 2026-04-14
**Audit Category:** Authentication, Authorization, Session Management
**Files Analyzed:**
- `internal/auth/auth.go` - User database, SCRAM server, auth limiter
- `internal/auth/scram.go` - SCRAM-SHA-256 implementation
- `internal/proxy/listener.go` - Client authentication flow, session handling
- `internal/pool/pool.go` - Connection management, pool access control
- `internal/config/config.go` - Auth configuration structures
- `internal/api/rest/server.go` - Admin API authentication

---

## 1. Authentication (CWE-306, CWE-307)

### 1.1 Authentication Flow

```
Client                    GeryonProxy                     Backend
  |                           |                              |
  |--- TCP Connection ------->|                              |
  |--- Startup Message ------>|                              |
  |     (username, database)  |                              |
  |                           |--- Check user exists -------->|
  |                           |--- Check AllowedPools -------->|
  |                           |                              |
  | [Interception Mode]       |                              |
  |                           |                              |
  |<-- AuthenticationSASL ---<-|                              |
  |--- SASLInitialResponse --->|                              |
  |     (SCRAM-SHA-256)       |                              |
  |<-- SASLContinue ----------<-|                              |
  |--- SASLResponse ---------->|                              |
  |<-- SASLFinal -------------<-|                              |
  |<-- AuthenticationOK ------<-|                              |
  |                           |                              |
  | [Passthrough Mode]        |                              |
  |                           |--- Forward to backend ------->|
  |                           |<-- Auth exchange ------------->|
  |<-- Auth result ----------<-|                              |
```

### 1.2 SCRAM-SHA-256 Implementation Analysis

| Component | Status | Location | Notes |
|-----------|--------|----------|-------|
| PBKDF2 iterations | PASS | `auth.go:124`, `scram.go:25` | 120,000 iterations (OWASP compliant) |
| Salt generation | PASS | `auth.go:281`, `scram.go:18` | 16-32 bytes via `crypto/rand` |
| Nonce generation | PASS | `auth.go:207-211` | 24 bytes via `crypto/rand` |
| ClientKey derivation | PASS | `scram.go:29` | HMAC-SHA-256 with "Client Key" |
| StoredKey | PASS | `scram.go:32` | SHA-256(ClientKey) |
| ServerKey | PASS | `scram.go:37` | HMAC-SHA-256 with "Server Key" |
| Proof verification | PASS | `auth.go:258-265` | Uses `hmac.Equal` |
| Server signature | WARN | `scram.go:231` | Simplified calculation - see 1.2.1 |

#### 1.2.1 ServerSignature Verification (Medium)

**Location:** `scram.go:231`

```go
// VerifyServerFinal verifies the server's final message.
func (c *SCRAMClient) VerifyServerFinal(serverFinal string) bool {
    // ...
    // Recompute ServerKey and ServerSignature for verification
    serverKey := hmacSum(c.saltedPass, []byte("Server Key"))
    recomputedSig := hmacSum(serverKey, []byte(c.clientNonce)) // Simplified
    // ...
}
```

**Issue:** The recomputedSig calculation uses only `clientNonce`, but according to RFC 5802, the ServerSignature must be computed over the AuthMessage which includes client-first, server-first, and client-final-without-proof.

**Impact:** Low - This is used for proxy-to-backend authentication, and if verification fails, the connection is rejected. The simplified calculation would only cause false negatives (rejecting valid servers), not false positives.

**CVSS:** 2.9 (AV:N/AC:H/AN:C/I:H/C:N - Network, Low complexity, No privileges, High integrity impact)

#### 1.2.2 No Channel Binding Support (Low)

**Location:** `auth.go:156-158`

```go
// Parse gs2-header
gs2 := parts[0] + "," + parts[1]
if !strings.HasPrefix(gs2, "n,") {
    return nil, fmt.Errorf("unsupported GS2 mechanism")
}
```

**Issue:** Only "n," (no channel binding) is supported. "y," (channel binding) and "p," (channel binding mandatory) are rejected.

**Impact:** Low - SCRAM without channel binding is still secure against man-in-the-middle attacks when used over TLS. Channel binding provides additional protection for non-TLS scenarios.

**CVSS:** 3.1 (AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:L/A:N)

### 1.3 Auth Rate Limiting

**Location:** `auth.go:472-585`

| Setting | Value | Notes |
|---------|-------|-------|
| Max attempts | 10 | Per IP within window |
| Window | 5 minutes | Sliding window |
| Lockout period | 5 minutes | After max failures |
| Storage | In-memory map | Not distributed |

**Strengths:**
- Atomic operations prevent race conditions
- Lockout expiry is checked correctly
- Successful auth clears failure counter

**Issues:**

1. **Rate limit not applied to all protocols** (Medium)
   - PostgreSQL interception mode: RATE LIMITED (`handlePostgreSQLAuth:666-671`)
   - MySQL: NOT RATE LIMITED (forwardAuthFromBackend at line 1056-1125)
   - MSSQL: NOT RATE LIMITED (forwardMSSQLLogin7 at line 1628-1672)

2. **Rate limit key is IP only** (Low)
   - No differentiation between usernames
   - Attacker can spread attempts across many usernames from single IP
   ```go
   clientIP := ps.clientConn.RemoteAddr().String()
   ```

3. **No distributed rate limiting** (Medium)
   - In-cluster deployments, each node has independent limiter
   - Attacker can bypass by hitting different nodes

**CVSS for bypass:** 4.3 (AV:N/AC:L/Au:N/C:N/I:P/A:N)

### 1.4 Max Connections Enforcement

**Location:** `internal/auth/auth.go:20`, `internal/proxy/listener.go:1049-1061`

```go
// User struct has MaxConnections field
type User struct {
    MaxConnections int  // Line 20
}
```

**Issue:** `MaxConnections` is parsed from config and stored, but **never checked** during connection establishment.

The `TryIncrementClientCount` in `pool.go:1049-1061` checks against `MaxClientConnections` (pool limit), not per-user limit.

**Impact:** A user with `max_connections: 10` can open 1000 connections if pool limit is 1000.

**CVSS:** 5.3 (AV:N/AC:L/PR:L/UI:N/S:U/C:N/I:L/A:N)

### 1.5 Admin API Authentication

**Location:** `internal/api/rest/server.go:224-251`

```go
func (s *Server) withAuth(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !s.config.Auth.Enabled {
            next.ServeHTTP(w, r)
            return
        }
        // ...
        if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.config.Auth.Token)) != 1 {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

**Strengths:**
- Bearer token with `subtle.ConstantTimeCompare` - GOOD
- Auth can be disabled per-endpoint via config - flexibility
- 10 req/s rate limiting per IP - GOOD

**Issues:**

1. **Token is static string** (Medium)
   - No token expiration
   - No token rotation mechanism
   - No token revocation list

2. **Single token for all admin APIs** (Medium)
   - REST, gRPC, MCP, Dashboard share same token format
   - Compromise of one compromises all

3. **`/metrics` endpoint bypasses auth** (Medium)
   ```go
   mux.HandleFunc("/metrics", s.handleMetrics)  // No auth wrapper
   ```
   Prometheus scraping works without authentication. If metrics contain sensitive data (query patterns, usernames), this is information disclosure.

**CVSS:** 5.3 (AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N)

---

## 2. Authorization (CWE-284, CWE-285)

### 2.1 Client-to-Backend User Mapping

**Location:** `internal/proxy/listener.go:841-861`

```go
backendUsername := ps.username
backendPassword := ""

if ps.authMode == "interception" && ps.config != nil && ps.config.Backend.Auth.Username != "" {
    backendUsername = ps.config.Backend.Auth.Username
    if ps.config.Backend.Auth.PasswordFile != "" {
        passwordBytes, err := os.ReadFile(ps.config.Backend.Auth.PasswordFile)
        // M-11 fix: zero the buffer after use
    }
}
```

**Issue:** In interception mode, the backend credentials are shared across ALL clients. All clients authenticate to the backend as the same user.

**Impact:** No per-client backend authentication. Cannot audit which client executed what query on the backend.

**CVSS:** 4.0 (AV:N/AC:L/PR:L/UI:N/S:U/C:N/I:L/A:N)

### 2.2 Pool Access Control

**Location:** `internal/proxy/listener.go:644-651` (PostgreSQL), `1246-1251` (MySQL)

```go
// Check pool access authorization (H-1 fix)
if !user.CanAccessPool(ps.pool.Name()) {
    ps.log.Warn("Pool access denied", "user", ps.username, "pool", ps.pool.Name())
    errMsg := postgresql.CreateErrorResponse("28000", "access to pool denied")
    // ...
    return fmt.Errorf("access denied for user %s to pool %s", ps.username, ps.pool.Name())
}
```

**Status:** IMPLEMENTED - The H-1 finding from the previous audit has been fixed.

**Strengths:**
- `CanAccessPool()` supports wildcard (`*`) and exact match
- Error message is generic (doesn't leak pool existence)
- Denied connections are logged

**Limitation:** No per-pool role (read-only, read-write) enforcement. If a user has access to a pool, they have full access.

### 2.3 Admin vs Proxy Users

**Finding:** No separation between admin users and proxy users.

- Admin API uses bearer token from `admin.rest.auth.token`
- Proxy users use SCRAM authentication from `auth.users`
- These are **entirely separate** authentication systems

**Implication:** A valid proxy user cannot access admin APIs. An admin token holder can manage pools but is not subject to `AllowedPools` restrictions.

**CVSS:** 3.7 (AV:N/AC:H/PR:N/UI:N/S:U/C:N/I:L/A:N) - Low risk since they are separate interfaces

### 2.4 Config Change Authorization

**Location:** `internal/api/rest/server.go:841-865`

```go
func (s *Server) handleConfigReload(w http.ResponseWriter, r *http.Request) {
    // No additional authorization beyond bearer token
    if s.reloadFn != nil {
        if err := s.reloadFn(); err != nil {
            writeError(w, http.StatusInternalServerError, "Config reload failed: ...")
            return
        }
    }
    writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", ...})
}
```

**Issue:** Any bearer token holder can reload configuration. No differentiation between:
- View-only operations (GET /api/v1/stats)
- Pool management (POST /api/v1/pools, DELETE /api/v1/pools/)
- Config reload (POST /api/v1/config/reload)

**CVSS:** 6.5 (AV:N/AC:L/PR:L/UI:N/S:U/C:N/I:H/A:N)

---

## 3. Session Management (CWE-384)

### 3.1 Session Token Generation

**Location:** `internal/proxy/listener.go:425-427`

```go
var (
    sessionIDCounter atomic.Uint64
)
```

**Finding:** Session IDs are atomic uint64 counters, not cryptographic tokens.

**Assessment:** ACCEPTABLE - Session IDs are internal identifiers, not user-facing tokens. Clients authenticate via SCRAM-SHA-256 per-connection. No session tokens are issued to clients.

### 3.2 Session Storage

**Location:** `internal/proxy/listener.go:43`, `internal/pool/session.go`

```go
// In Listener
sessions map[uint64]*ProxySession  // In-memory only

// In ProxySession
authenticated atomic.Bool  // Tracks auth state
username string
database string
```

**Issues:**

1. **In-memory only** (Medium)
   - Sessions are not replicated across cluster nodes
   - If a node fails, active sessions are lost
   - No session migration/failover

2. **No session timeout enforcement** (Medium)
   - `ProxySession` has no idle timeout
   - Connection-level deadline is set at accept (2 minutes in listener.go:248)
   - But this is for slowloris protection, not session management

### 3.3 Session Timeout

**Location:** `internal/proxy/listener.go:247-248`

```go
// Set deadlines to prevent slowloris attacks and idle connection buildup
conn.SetDeadline(time.Now().Add(2 * time.Minute)) // Overall idle timeout
```

**Issues:**

1. **2-minute idle timeout is aggressive** (Low)
   - Legitimate queries taking >2 minutes (e.g., large exports) will timeout
   - But this IS correct for slowloris protection

2. **No session-level timeout independent of TCP** (Low)
   - Cannot configure separate idle timeout for sessions vs. connection lifetime
   - `max_connection_lifetime` and `max_idle_time` exist in config but are not enforced at session level

### 3.4 Session Fixation

**Finding:** NOT APPLICABLE - GeryonProxy does not issue session tokens. Each connection requires fresh SCRAM authentication. There is no concept of a "logged-in session" that persists across connections.

---

## 4. Auth Flow Diagrams

### 4.1 PostgreSQL Interception Mode

```
1. Client connects to proxy port
2. Proxy reads StartupMessage (username, database, params)
3. Proxy checks if user exists in UserDatabase
4. IF user not found -> ErrorResponse "28P01" -> close connection
5. Proxy checks user.CanAccessPool(currentPool)
6. IF access denied -> ErrorResponse "28000" -> close connection
7. Proxy checks AuthLimiter.IsLimited(clientIP)
8. IF rate limited -> ErrorResponse "28P01" -> close connection
9. Proxy sends AuthenticationSASL (SCRAM-SHA-256)
10. Client sends SASLInitialResponse (client-first)
11. Proxy parses client-first, extracts nonce, username
12. Proxy generates server-first (with server nonce extension)
13. Proxy sends AuthenticationSASLContinue
14. Client sends SASLResponse (client-final with proof)
15. Proxy verifies proof using StoredKey
16. IF verify fails -> ErrorResponse "28P01" -> recordAuthFailure -> close
17. Proxy sends AuthenticationSASLFinal (server signature)
18. Proxy sends AuthenticationOK
19. Proxy sends ReadyForQuery ('I')
20. Connection now authenticated and ready
21. recordAuthSuccess (clears rate limit counter)
22. Proxy connects to backend with BackendAuth credentials
```

### 4.2 Admin REST API

```
1. Request arrives at /api/v1/*
2. Rate limiting check (per IP, 10 req/s)
3. Security headers added (X-Content-Type-Options, X-Frame-Options, etc.)
4. CORS check (origin validation)
5. Auth check:
   a. If Auth.Enabled == false -> proceed
   b. If no Authorization header -> 401 Unauthorized
   c. If not "Bearer <token>" -> 401 Unauthorized
   d. If token != config.Auth.Token (ConstantTimeCompare) -> 401 Unauthorized
6. Handler executes
```

---

## 5. Bypass Scenarios

### 5.1 Username Enumeration via Timing

**Scenario:** Attacker can determine valid usernames by observing response times.

**Location:** `internal/proxy/listener.go:633-641`

```go
user := ps.userDB.GetUser(ps.username)
if user == nil {
    errMsg := postgresql.CreateErrorResponse("28P01", "authentication failed")
    // ... send error
    return fmt.Errorf("unknown user: %s", ps.username)
}
// THEN later pool access check happens
if !user.CanAccessPool(ps.pool.Name()) {
    // ...
}
```

**Issue:** If user doesn't exist, error is returned BEFORE pool access check. An attacker can iterate usernames and measure response time to enumerate valid users.

**CVSS:** 4.3 (AV:N/AC:L/Au:N/C:N/I:L/A:N)

### 5.2 Rate Limit Bypass via Protocol Switching

**Scenario:** Attacker bypasses rate limit by switching between PostgreSQL, MySQL, MSSQL protocols.

**Location:** Multiple - see section 1.3

**Issue:** MySQL and MSSQL authentication paths do not call `authLimiter.IsLimited()`.

**CVSS:** 4.3 (AV:N/AC:L/Au:N/C:N/I:P/A:N)

### 5.3 Pool Access Bypass via Pool Name Guessing

**Scenario:** Attacker with valid credentials tries to access pools they shouldn't.

**Location:** `internal/proxy/listener.go:645-651`

**Mitigated:** Error message does not reveal whether pool exists:
```go
errMsg := postgresql.CreateErrorResponse("28000", "access to pool denied")
```

**Issue:** Since PostgreSQL protocol already requires user existence check, attacker can first enumerate users, then try pools.

**CVSS:** 3.1 (AV:N/AC:L/PR:L/UI:R/S:U/C:N/I:L/A:N)

---

## 6. Missing Auth Checks

| Check | Status | Location | Notes |
|-------|--------|----------|-------|
| User existence before pool access | PARTIAL | listener.go:633-651 | Timing leak allows enumeration |
| Pool access (AllowedPools) | IMPLEMENTED | listener.go:645-651 | Fixed from H-1 |
| Auth rate limiting (PG) | IMPLEMENTED | listener.go:666-671 | |
| Auth rate limiting (MySQL) | MISSING | listener.go:1167-1274 | |
| Auth rate limiting (MSSQL) | MISSING | listener.go:1522-1552 | |
| Per-user MaxConnections | MISSING | pool.go:1049-1061 | Only pool-level limit checked |
| Admin API auth | IMPLEMENTED | server.go:224-251 | Uses ConstantTimeCompare |
| Metrics endpoint auth | MISSING | server.go:77 | No auth wrapper |
| Config change auth | PARTIAL | server.go:841-865 | Only bearer token, no细分 |
| Client certificate validation | PARTIAL | listener.go:2419-2460 | Logs but doesn't enforce |

---

## 7. CVSS Summary

| Finding | CVSS | Vector | Severity |
|---------|------|--------|----------|
| ServerSignature simplified calc | 2.9 | AV:N/AC:H/AN:C/I:H/C:N | Low |
| No channel binding | 3.1 | AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:L/A:N | Low |
| Rate limit bypass (MySQL/MSSQL) | 4.3 | AV:N/AC:L/Au:N/C:N/I:P/A:N | Medium |
| Admin token static/no rotation | 5.3 | AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N | Medium |
| MaxConnections per-user not enforced | 5.3 | AV:N/AC:L/PR:L/UI:N/S:U/C:N/I:L/A:N | Medium |
| Metrics endpoint unauthenticated | 5.3 | AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N | Medium |
| Config reload no细分权限 | 6.5 | AV:N/AC:L/PR:L/UI:N/S:U/C:N/I:H/A:N | Medium |
| Shared backend credentials | 4.0 | AV:N/AC:L/PR:L/UI:N/S:U/C:N/I:L/A:N | Medium |
| Username enumeration via timing | 4.3 | AV:N/AC:L/Au:N/C:N/I:L/A:N | Medium |

---

## 8. Recommendations

### P0 - Critical (Fix Immediately)

1. **Add per-user MaxConnections enforcement**
   - Check `user.MaxConnections` before allowing connection
   - Track per-user connection count separately

2. **Add rate limiting to MySQL/MSSQL auth paths**
   - Call `authLimiter.IsLimited()` in those handlers

### P1 - High (Fix Soon)

3. **Add authentication to /metrics endpoint**
   - Either require auth or restrict to localhost
   - Consider a separate read-only token

4. **Implement token rotation mechanism for admin APIs**
   - Add `token_last_rotated` tracking
   - Warn if token unchanged > 90 days

5. **Prevent username enumeration**
   - Add artificial delay for non-existent users
   - Or return same error for all auth failures

### P2 - Medium (Plan for Next Release)

6. **Add role-based admin authorization**
   - view stats, manage pools, reload config as separate roles
   - Single token is insufficient for production

7. **Add distributed rate limiting**
   - Redis-backed rate limiter for cluster deployments

8. **Add per-client backend authentication**
   - Currently all clients share backend credentials in interception mode

---

## 9. Positive Security Findings

| Control | Implementation | Location |
|---------|---------------|----------|
| Constant-time token comparison | `subtle.ConstantTimeCompare` | REST auth |
| Constant-time password comparison | `hmac.Equal` | SCRAM verify |
| PBKDF2 with 120k iterations | OWASP compliant | SCRAM hash |
| TLS 1.2 minimum | Safe ciphers only | tlsutil |
| Auth rate limiting | 10 attempts/5min lockout | AuthLimiter |
| Pool access control | CanAccessPool check | listener.go |
| Input validation | Max params, max lengths | listener.go |
| Error message sanitization | Generic errors | All handlers |
| Security headers | X-Frame-Options, etc. | REST server |
| Request body size limit | 1MB max | REST API |
| Admin APIs bound to localhost | 127.0.0.1 default | config.go |

---

*Report generated: 2026-04-14*
*Previous audit findings tracked in SECURITY-REPORT.md (2026-04-13)*
