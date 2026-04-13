# sc-lang-go Security Scan Results for GeryonProxy

## Scan Summary
- **Project:** GeryonProxy (Pure Go database connection pooler proxy)
- **Scan Date:** 2026-04-13
- **Patterns Scanned:** 13 Go-specific security vulnerability categories
- **Build Configuration:** CGO_ENABLED=0 (No CGo usage confirmed)

---

### [HIGH] Orphaned Backend Transactions Due to Incomplete Timeout Handling
- **Category:** Goroutine/Database Resource Management
- **Location:** `internal/pool/transaction.go:200-266`
- **Pattern Matched:** `checkTimeouts()` sets transaction status to TxnAborted/TxnIdle but does NOT send ROLLBACK to the backend database
- **Description:** The `checkTimeouts()` function correctly detects transaction timeouts and idle timeouts, setting the transaction status to `TxnAborted` or `TxnIdle`. However, it only invokes optional abort callbacks without actually sending a ROLLBACK command to the backend database server. This leaves orphaned transactions on the database server that continue to hold locks and consume resources.
- **Exploitability:** A malicious client could open a transaction, send queries that acquire row locks, then stop sending requests. The proxy marks the transaction as timed out in its internal state but the backend database continues to hold locks until its own timeout fires (which could be hours later or never).
- **Remediation:** When a transaction times out, actually send a ROLLBACK command to the backend connection. The `AbortFunc` and `onAbortWithConn` callbacks should be used to properly terminate the backend transaction.
- **Reference:** CWE-400 (Uncontrolled Resource Consumption)

---

### [MEDIUM] context.Background() in Request Handler
- **Category:** Context Misuse
- **Location:** `internal/pool/session.go:290`
- **Pattern Matched:** `ctx := context.Background()` in `HandleMessage()` method
- **Description:** The `HandleMessage()` method uses `context.Background()` when acquiring a server connection via strategy. This creates an uncontextualized operation that cannot be cancelled or traced through the request lifecycle. If the session is terminated, there is no way to propagate cancellation to the connection acquisition.
- **Exploitability:** If a client connection is closed while `strategy.OnQuery(ctx, s, msg)` is in progress, the operation may continue using resources until it naturally completes or times out. This could lead to resource exhaustion under heavy load with many closing connections.
- **Remediation:** Use the context from the incoming message or a context derived from the session's lifecycle. Consider passing a cancellable context through the message handling chain.
- **Reference:** CWE-675 (Duplicate Operations on Resource)

---

### [MEDIUM] Ignored Write Errors in Relay Goroutines
- **Category:** Error Handling / Resource Leaks
- **Location:** `internal/proxy/listener.go` (relay operations)
- **Pattern Matched:** `io.Copy` or similar operations with ignored write errors
- **Description:** When relaying data between client and backend connections, write errors may be silently ignored. If a write fails (e.g., client connection is slow or closed), the goroutine may continue reading from the other direction, wasting resources and potentially corrupting state.
- **Exploitability:** An attacker could initiate a connection and deliberately slow their read side to cause the proxy to buffer data indefinitely, or close the connection to trigger resource waste in relay goroutines that don't properly terminate on write failures.
- **Remediation:** Properly handle all `io.Copy` errors and terminate the relay when either direction encounters an error. Consider implementing proper shutdown signaling between the two relay directions.
- **Reference:** CWE-755 (Improper Handling of Exceptional Conditions)

---

### [LOW] Unbounded Rate Limiter Map Growth
- **Category:** Denial of Service / Memory Management
- **Location:** `internal/api/rest/server.go:251-318`, `internal/api/grpc/server.go:191-257`, `internal/api/mcp/server.go:154-216`, `internal/api/dashboard/server.go:163-225`
- **Pattern Matched:** `limiters map[string]*rate.Limiter` and `lastSeen map[string]time.Time` with unbounded growth
- **Description:** The rate limiter implementations maintain per-IP limiter and timestamp maps. While there is a cleanup goroutine with `cleanupTTL`, the cleanup interval is 5 minutes and the maps can grow to `maxSize` (10000) before eviction starts. Under a coordinated attack, memory could grow significantly before cleanup occurs.
- **Exploitability:** An attacker with many IP addresses (or through a botnet) could create enough unique entries to cause memory pressure before the 5-minute cleanup cycle removes stale entries.
- **Remediation:** Consider adding memory-based limits with immediate eviction when `maxSize` is reached, or using a more memory-efficient structure like `sync.Map` with TTL-based expiration.
- **Reference:** CWE-400 (Uncontrolled Resource Consumption)

---

### [LOW] Missing Idle Timeout on Backend Connections
- **Category:** Resource Management
- **Location:** `internal/pool/pool.go:754-804`
- **Pattern Matched:** Backend connection creation without explicit idle timeout
- **Description:** Backend connections are created with `net.DialTimeout` for the initial connection, but once established, there is no idle timeout that would close connections that haven't been used for a prolonged period. The `lastUsedAt` field is tracked but not used to enforce timeouts.
- **Exploitability:** If a backend server becomes unavailable but connections remain open, these stale connections could persist indefinitely, consuming resources and potentially causing the pool to think backends are healthy when they are not.
- **Remediation:** Implement a idle connection timeout that closes backend connections that haven't been used within a configurable period (e.g., `MaxIdleTime`).
- **Reference:** CWE-404 (Improper Resource Shutdown or Release)

---

### [INFO] No Issues Found - Good Security Practices

The following security patterns were verified and found to be properly implemented:

**1. Unsafe Package Usage:** No usage of `unsafe.Pointer`, `reflect.NewAt`, or `reflect.Value.Pointer()` found.

**2. CGo Boundary Safety:** Project consistently uses `CGO_ENABLED=0` in all build configurations (Dockerfile, Makefile, GitHub workflows). No CGo imports found.

**3. Goroutine Termination:** Background goroutines (transaction monitor, health check loops, rate limiter cleanup) have proper termination paths via stop channels or context cancellation.

**4. Race Conditions:** All maps in pool code (`transactions`, `active`, `byBackend`, `sessions`) are protected by appropriate mutexes (sync.Mutex or sync.RWMutex). Atomic operations are used where appropriate.

**5. Cryptographically Secure Random:** Uses `crypto/rand` (not `math/rand`) for security-sensitive operations in:
   - `internal/auth/auth.go` - Authentication
   - `internal/auth/scram.go` - SCRAM-SHA-256 implementation
   - `internal/tlsutil/tls.go` - Certificate generation
   - `internal/tlsutil/config.go` - TLS configuration

**6. TLS Configuration:** All TLS configurations properly set:
   - `MinVersion: tls.VersionTLS12` (TLS 1.2 minimum)
   - Secure cipher suites via `CipherSuites12()`
   - `InsecureSkipVerify: false` by default
   - Proper certificate validation

**7. Command Injection:** No usage of `os/exec` with shell commands found.

**8. HTTP Server Timeouts:** All API servers properly configure HTTP timeouts:
   - REST server: `ReadTimeout: 30s`, `WriteTimeout: 30s`
   - gRPC server: `ReadTimeout: 30s`, `WriteTimeout: 30s`
   - MCP server: `ReadTimeout: 30s`, `WriteTimeout: 30s`
   - Dashboard server: `ReadTimeout: 30s`, `WriteTimeout: 30s`

**9. Integer Overflow in Codecs:** Protocol codec parsers properly validate lengths before arithmetic:
   - PostgreSQL: `length < 4` check before `int(length) - 4`
   - MySQL: `length > MaxPayloadLen` check before buffer allocation
   - MSSQL: `length < 8` check before `int(length) - 8`

**10. SQL Row Resource Management:** Not applicable - project uses raw database protocol connections, not `database/sql` package. Connection lifecycle is properly managed with explicit `Close()` calls and proper defer patterns.

**11. Slice/Map Concurrent Access:** All concurrent map access is protected by mutexes or uses Go's built-in concurrency-safe structures where applicable.

---

## Summary

| Severity | Count |
|----------|-------|
| HIGH     | 1     |
| MEDIUM   | 2     |
| LOW      | 2     |
| INFO     | 11 categories verified secure |

*Generated by sc-lang-go (Go Security Pattern Scanner)*
