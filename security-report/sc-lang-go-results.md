# sc-lang-go Security Scan Results

**Project:** GeryonProxy (pure Go database connection pooler/proxy)
**Scanner:** sc-lang-go v1.0.0
**Date:** 2026-05-01
**Severity Threshold:** All findings (INFO and above)

---

## Summary

This scan evaluated the GeryonProxy codebase across all 20 Go-specific security categories. Overall, the codebase demonstrates strong security practices, particularly in cryptographic operations, TLS configuration, and error handling. However, one medium-severity issue was identified related to context usage in the session handling path.

---

## Findings

### [MEDIUM] context.Background() Used in Session Message Handler

- **Category:** Context.Context Misuse (SC-GO-301)
- **Location:** `internal/pool/session.go:290`
- **Pattern Matched:** `ctx := context.Background()` in a request-processing context
- **Description:** The `HandleMessage` method uses `context.Background()` when acquiring a server connection for query processing. This context is passed to `s.strategy.OnQuery(ctx, s, msg)` at line 312, which means client disconnection (via request context cancellation) is not honored during connection acquisition. If a client disconnects while their query is waiting for a connection, the goroutine continues processing until a connection is available or the pool's wait timeout expires.
- **Exploitability:** An attacker who initiates a connection and sends a query, then disconnects, could cause the proxy to continue processing the query indefinitely (bounded only by the connection wait timeout). While the wait timeout provides a boundary, the query processing itself cannot be cancelled mid-flight due to the detached context.
- **Remediation:** Replace `context.Background()` with a context derived from the session's parent context, or pass the caller's context through the message handling chain:

```go
// Option 1: If session stores a cancellable context
ctx := s.ctx // where s.ctx is a context linked to the session lifecycle

// Option 2: Create a child context with timeout tied to pool's connection timeout
ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
defer cancel()
```

- **Reference:** [Go Context Documentation](https://pkg.go.dev/context), CWE-400

---

## Positive Security Findings (No Issues)

### 1. Unsafe Package Usage
No usage of `unsafe.Pointer`, `reflect.NewAt`, or other unsafe operations that bypass Go's type system.

### 2. CGo Boundary Safety
No CGo usage in production code (CGO_ENABLED=0 for releases per project policy).

### 3. Goroutine Leaks
All goroutines have termination paths:
- Health checker uses `stopCh` channel for clean shutdown (pool/health.go)
- Transaction manager uses `stopCh` for clean shutdown (pool/transaction.go)
- WaitQueue uses context cancellation and explicit cleanup

### 4. Race Conditions
The codebase demonstrates careful concurrent access management:
- `serverConnPool` uses `sync.Mutex` to protect all map operations (`active`, `idle`, `idleIndex`)
- `ServerConn.preparedStmts` map protected by `sync.Mutex` (pool/pool.go:190-192)
- Atomic operations used for counters (`atomic.Int64`, `atomic.Bool`)
- Auth limiter uses nested mutexes to prevent lock inversion deadlocks (auth/auth.go:488-540)

### 5. html/template vs text/template XSS
No template rendering found in the codebase. Dashboard uses embedded static files.

### 6. crypto/rand vs math/rand
**Correctly uses `crypto/rand` for all security-sensitive operations:**
- `auth/scram.go:19` - Salt generation for password hashing
- `auth/scram.go:165` - Client nonce generation
- `auth/scram.go:216` - Server nonce generation
- `auth/auth.go:290` - Salt generation in hash generation
- `internal/pool/session.go` and related files use `crypto/rand` for nonces

### 7. TLS Configuration
**TLS configuration is secure:**
- `tlsutil/tls.go:22-23` - Server config: MinVersion TLS 1.2, CipherSuites12()
- `tlsutil/tls.go:71-72` - Client config: MinVersion TLS 1.2, CipherSuites12()
- `tlsutil/config.go:93` - InsecureSkipVerify only set explicitly for insecure modes (defaults to false)
- `pool/pool.go:941-943` - Backend TLS: InsecureSkipVerify set to false
- `CipherSuites12()` returns only strong cipher suites (ECDHE with AES-256-GCM, AES-128-GCM)

### 8. os/exec Command Injection
No `exec.Command` usage in production code. Only found in integration tests (integration-tests/) which are excluded by `-short` flag.

### 9. filepath.Join Traversal
No file serving operations that could be affected by path traversal. Configuration loading uses safe path handling.

### 10. net/http Missing Timeouts
**All HTTP servers have proper timeouts configured:**

REST API Server (internal/api/rest/server.go:132-140):
```go
ReadTimeout:  30 * time.Second,
WriteTimeout: 30 * time.Second,
IdleTimeout:  60 * time.Second,
```

HTTP/2 Admin API Server (internal/api/grpc/server.go:100-108):
```go
ReadTimeout:  30 * time.Second,
WriteTimeout: 30 * time.Second,
IdleTimeout:  60 * time.Second,
```

### 11. encoding/json Deserialization
**Body size limits enforced:**
- `rest/server.go:647` - Pool creation: 1MB limit via `http.MaxBytesReader`
- `rest/server.go:436` - Backend queries: 1MB limit
- `rest/server.go:647,862,1235` - Multiple endpoints use `http.MaxBytesReader`
- `grpc/server.go:341` - Stats streaming: 1MB limit

### 12. Integer Overflow in Type Conversions
**Proper error handling on all integer conversions:**
- `rest/server.go:1509` - `strconv.Atoi(limitStr)` with bounds check: `n > 0 && n <= 1000`
- `rest/server.go:1820` - Same pattern for recent queries limit
- `config/loader.go:224` - `strconv.ParseInt(valueStr, 10, 64)` with error handling
- `auth/scram.go:109` - `strconv.Atoi(iterSalt[0])` with error check

### 13. Go Module Supply Chain
- All dependencies are standard, well-maintained packages
- No replace directives pointing to mutable remote repos
- go.sum is committed to version control
- Uses `govulncheck` for vulnerability scanning (per CLAUDE.md)

### 14. context.Context Misuse
**See main finding above.** Additionally:
- REST and gRPC servers use `r.Context()` for request-scoped operations
- Defer cancel pattern correctly used in tests

### 15. Defer Ordering Bugs
No defer-in-loop anti-patterns found. All defers execute at function return, and the pattern is correctly used for:
- Mutex unlocking (defer after Lock)
- Connection closing (defer after acquire)
- Context cancellation (defer cancel after WithTimeout)

### 16. Panic Recovery Anti-Patterns
**Panic recovery is properly implemented:**
- `rest/server.go:230-239` - Logs error with stack trace, returns generic 500
- `grpc/server.go:149-159` - Same pattern, logs error with path context

### 17. gRPC Security
N/A - The `internal/api/grpc` package uses JSON-over-HTTP, not protobuf gRPC (per package comments). HTTP timeouts are properly configured (see finding #10).

### 18. sql.DB Pool Exhaustion
Not applicable - GeryonProxy is a database proxy that manages raw TCP connections, not sql.DB connections. Connection pool exhaustion is handled via:
- `serverConnPool` with max size limits
- `WaitQueue` with configurable max size (default 1000)
- Memory limit via `Manager.TryAlloc()`

### 19. Slice/Map Concurrent Access
All maps are protected by appropriate synchronization:
- `serverConnPool` maps protected by `sync.Mutex`
- `UserDatabase.users` map protected by `sync.RWMutex`
- `TransactionManager.transactions` map protected by `sync.RWMutex`
- `AuthLimiter.attempts` map protected by `sync.Mutex`
- `rateLimiter.limiters` uses `sync.Map` for lock-free concurrent access

### 20. Error Wrapping Info Disclosure
**REST API properly sanitizes errors:**
- `rest/server.go:513-526` - `sanitizeErr()` strips file paths and connection strings
- All internal errors wrapped and sanitized before client exposure
- Generic error messages returned to clients

---

## Recommendations

1. **Consider passing a proper context to `HandleMessage`** to enable cancellation on client disconnect, especially for long-running query operations.

2. **Add a lint rule** to block `math/rand` in `internal/` packages to prevent accidental cryptographic use.

3. **Continue following existing security practices** - the codebase demonstrates strong security awareness in critical areas like cryptography, TLS configuration, and error handling.

---

*Report generated by sc-lang-go security scanner.*
