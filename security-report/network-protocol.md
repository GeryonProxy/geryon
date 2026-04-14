# Network Security and Protocol Handling Audit

## Executive Summary

GeryonProxy implements a multi-database connection pooler supporting PostgreSQL, MySQL, and MSSQL wire protocols. This audit identifies network exposure, protocol-level vulnerabilities, and DoS attack vectors.

---

## 1. Network Attack Surface

### 1.1 Exposed Network Endpoints

| Endpoint | Default Bind | Port | Exposure | TLS |
|----------|-------------|------|----------|-----|
| PostgreSQL Pool | `0.0.0.0` | 5432 | PUBLIC | `prefer` (opt-in) |
| MySQL Pool | `0.0.0.0` | 3306 | PUBLIC | `prefer` (opt-in) |
| MSSQL Pool | `0.0.0.0` | 1433 | PUBLIC | `prefer` (opt-in) |
| REST Admin API | `127.0.0.1` | 8080 | LOCAL | token-only |
| gRPC Admin API | `127.0.0.1` | 9090 | LOCAL | token-only |
| MCP Server | `127.0.0.1` | 8081 | LOCAL | token-only |
| Dashboard | `127.0.0.1` | 8082 | LOCAL | token-only |
| Raft Cluster | `0.0.0.0` | 7000 | CLUSTER | none |
| Gossip Cluster | `0.0.0.0` | 7001 | CLUSTER | none |

**Findings:**
- Database pool listeners bind to `0.0.0.0` by default, accepting connections from any network interface
- Admin interfaces correctly bound to localhost
- Cluster communication (Raft/Gossip) has no encryption

### 1.2 Listener Binding Issues

**Location:** `internal/proxy/listener.go:204`
```go
ln, err := net.Listen("tcp", l.address)  // l.address from config
```

**Issue:** Pool listeners inherit `host: "0.0.0.0"` from config, accepting connections on all interfaces.

**Recommendation:** Support `localhost` or `127.0.0.1` binding for internal-only pools.

**CVSS:** CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N (4.8 Medium)

### 1.3 TLS Enforcement Gaps

**Location:** `internal/tlsutil/config.go:62-71`
```go
switch cfg.Mode {
case "allow", "prefer":
    serverConfig.ClientAuth = tls.RequestClientCert
case "require":
    serverConfig.ClientAuth = tls.RequireAnyClientCert
// ...
```

**Issues:**
1. Default TLS mode is `prefer` (config line 72 in example), allowing clients to use unencrypted connections
2. No option to mandate TLS; `require` only demands a certificate, not encryption
3. Backend TLS verification defaults to skip when not configured

**CVSS:** CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:N (9.3 Critical) - Unencrypted database traffic

### 1.4 Proxy Protocol Handling

**Finding:** No proxy protocol (HAProxy PROXY protocol) header support detected.

**Impact:** Original client source IP is lost when connecting to backends. Backend sees GeryonProxy's IP.

**Location:** Backend connection in `internal/pool/pool.go:799-815`
```go
netConn, err = net.DialTimeout("tcp", addr, dialTimeout)
// No proxy protocol header sent
```

**Recommendation:** Add PROXY protocol v2 support for source IP forwarding.

**CVSS:** CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N (5.3 Medium)

---

## 2. Protocol-Level Attack Vectors

### 2.1 PostgreSQL Startup Sequence Spoofing

**Location:** `internal/proxy/listener.go:516-571`

**Validations Applied:**
```go
// Length validation (line 526)
if length < 8 || length > 10000 {
    return fmt.Errorf("invalid startup message length: %d", length)
}

// Protocol version check (line 570)
if protoVersion != 196608 {  // Protocol 3.0
    return fmt.Errorf("unsupported protocol version: %d", protoVersion)
}

// Parameter limits (line 579-582)
const maxStartupParams = 64
const maxValueLen = 256
if paramCount >= maxStartupParams {
    return fmt.Errorf("too many startup parameters (max %d)", maxStartupParams)
}

// Null byte validation (line 621-627)
for i := 0; i < len(s); i++ {
    if s[i] < 0x20 || s[i] == 0x7F {
        return fmt.Errorf("invalid character in startup parameter value")
    }
}
```

**Assessment:** Startup message handling has reasonable bounds checking.

**CVSS:** CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N (0.0 None) - Mitigated

### 2.2 Malformed Message Handling

**Location:** `internal/protocol/postgresql/codec.go:34-84`

**Message Length Validation:**
```go
// Line 52-54: minimum length
if length < 4 {
    return nil, fmt.Errorf("invalid message length: %d", length)
}

// Line 57-60: integer overflow check
payloadLen := int(length) - 4
if payloadLen < 0 {
    return nil, fmt.Errorf("invalid message length: %d", length)
}

// Line 61-63: max payload limit
if payloadLen > MaxPayloadLen {  // 16MB
    return nil, fmt.Errorf("message too large: %d bytes", payloadLen)
}
```

**Assessment:** Length field integer underflow/overflow is checked. Max message size enforced.

**CVSS:** CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L (4.0 Medium) - Limited DoS via large messages

### 2.3 Buffer Overflow in Message Parsing

**Finding:** No buffer overflow vulnerabilities detected.

**Evidence:**
- All reads use `io.ReadFull` with explicit size limits
- Slice allocations bounded by validated length fields
- No `unsafe` package usage

**CVSS:** CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H (10.0 Critical) - None Found

### 2.4 Integer Overflow in Length Fields

**Location:** `internal/pool/health.go:309`

**Health Check Message Length Parsing:**
```go
msgLen := int(buf[0])<<24 | int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
payloadLen := msgLen - 4
```

**Issue:** If `msgLen < 4`, `payloadLen` becomes negative (int underflow). However, caller checks for negative later.

**Location:** `internal/pool/health.go:312-319`
```go
for payloadLen > 0 {
    n := min(payloadLen, len(buf))
    read, err := conn.Read(buf[:n])
    // ...
    payloadLen -= read
}
```

**Issue:** If `msgLen > len(buf)*iterations`, loop may take excessive iterations.

**CVSS:** CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L (4.0 Medium)

### 2.5 Connection Exhaustion (CWE-400)

**Location:** `internal/proxy/listener.go:264-269`

**Client Connection Limiting:**
```go
maxConns := int64(l.config.Limits.MaxClientConnections)
if !l.pool.TryIncrementClientCount(maxConns) {
    l.log.Warn("Max client connections reached", "pool", l.config.Name)
    return  // Reject immediately
}
```

**Wait Queue Limits:**
**Location:** `internal/pool/pool.go:358-368`
```go
func NewWaitQueue(maxSize int) *WaitQueue {
    if maxSize <= 0 {
        maxSize = 1000 // Default cap
    }
    // ...
}
```

**Location:** `internal/pool/pool.go:374-378`
```go
if len(wq.waiters) >= wq.maxSize {
    wq.mu.Unlock()
    return nil, fmt.Errorf("connection queue full (max %d)", wq.maxSize)
}
```

**Server Connection Limits:**
**Location:** `internal/pool/pool.go:608`
```go
if p.serverConns.size() < p.config.Limits.MaxServerConnections {
```

**Assessment:** Connection limits are enforced. Wait queue has 1000 default cap.

**CVSS:** CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L (4.0 Medium) - Partially mitigated

### 2.6 Backend Connection Reuse Across Clients

**Location:** `internal/pool/pool.go:242-270`

**Connection Reset Before Reuse:**
```go
func (p *serverConnPool) release(conn *ServerConn) {
    // ...
    if len(p.idle) < p.maxSize {
        // Reset MUST complete before returning to pool
        if conn.codec != nil {
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            if err := ResetConnection(ctx, conn.conn, conn.codec); err != nil {
                conn.Close()  // Close if reset fails
                cancel()
                return
            }
            cancel()
        }
        conn.MarkIdle()
        p.idle = append(p.idle, conn)
    }
}
```

**PostgreSQL Reset:** `DISCARD ALL` command sent to clear session state.

**Assessment:** Connection reset is performed before reuse. However, in transaction mode, connection is returned to pool while still associated with a client session context.

**Potential Issue:** Transaction mode pools may reuse connections across clients if transactions are not properly tracked.

**CVSS:** CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H (8.1 High) - Potential session data leakage

---

## 3. DDoS Vectors

### 3.1 Client Connection Limits

| Control | Location | Default | Config Field |
|---------|----------|---------|--------------|
| Max Clients | `listener.go:265` | unlimited* | `max_client_connections` |
| Wait Queue | `pool.go:360` | 1000 | (hardcoded) |

*No explicit default; uses config value which defaults to 0 (unlimited)

**Issue:** If `max_client_connections` is 0 or unset, no client connection limit exists.

**CVSS:** CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L (5.3 Medium)

### 3.2 Backend Connection Limits

**Location:** `internal/pool/pool.go:608`

```go
if p.serverConns.size() < p.config.Limits.MaxServerConnections {
```

**Defaults from example config:**
- PostgreSQL: max_server_connections: 100
- MySQL: max_server_connections: 50
- MSSQL: max_server_connections: 30

**Assessment:** Properly configured defaults.

### 3.3 Wait Queue Limits

**Location:** `internal/pool/pool.go:374`

```go
if len(wq.waiters) >= wq.maxSize {
    return nil, fmt.Errorf("connection queue full (max %d)", wq.maxSize)
}
```

**Issue:** Wait queue max size is 1000 (hardcoded default in `NewWaitQueue`).

**CVSS:** CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L (5.3 Medium) - Queue overflow possible

### 3.4 Query Timeout Enforcement

**Location:** `internal/pool/pool.go:623`

```go
waitTimeout := parseDuration(p.config.Limits.ConnectionTimeout, 5*time.Second)
conn, err := p.waitQueue.Wait(ctx, waitTimeout)
```

**Finding:** Connection acquisition timeout is enforced (default 5s). Query execution timeout is defined in config but not consistently enforced at relay level.

**Location:** `internal/proxy/listener.go:248`

```go
conn.SetDeadline(time.Now().Add(2 * time.Minute)) // Overall idle timeout
```

**Issue:** 2-minute idle timeout is hardcoded, not configurable per connection.

**CVSS:** CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L (5.3 Medium)

### 3.5 Health Check DoS

**Location:** `internal/pool/health.go:172-186`

```go
const maxConcurrentChecks = 10
sem := make(chan struct{}, maxConcurrentChecks)
```

**Assessment:** Health check concurrency is bounded to 10, preventing health check amplification attacks.

---

## 4. Authentication Security

### 4.1 Auth Rate Limiting

**Location:** `internal/auth/auth.go:491-497`

```go
func NewAuthLimiter() *AuthLimiter {
    return &AuthLimiter{
        maxAttempts:   10,
        window:        5 * time.Minute,
        lockoutPeriod: 5 * time.Minute,
    }
}
```

**Assessment:** Bruteforce protection with 10-attempt limit and 5-minute lockout.

### 4.2 SCRAM-SHA-256 Implementation

**Location:** `internal/auth/auth.go:280-308`

- Uses 120,000 iterations (OWASP 2023+ recommendation)
- Proper salt generation with `crypto/rand`
- Server-side verification with HMAC comparison

**Assessment:** Cryptographically sound SCRAM implementation.

### 4.3 Password Memory Security

**Location:** `internal/proxy/listener.go:857-860`

```go
// M-11 fix: zero the buffer after use to reduce memory lifetime
for i := range passwordBytes {
    passwordBytes[i] = 0
}
```

**Assessment:** Password zeroized after backend auth file read.

---

## 5. Summary of Vulnerabilities

| ID | Category | Issue | CVSS | Status |
|----|----------|-------|------|--------|
| NW-1 | Network | Pool listeners bind 0.0.0.0 (exposed) | 4.8 | Known |
| NW-2 | Network | TLS not enforced ("prefer" mode) | 9.3 | Risk |
| NW-3 | Network | No proxy protocol support | 5.3 | Gap |
| NW-4 | Network | Cluster traffic unencrypted | 7.4 | Gap |
| PR-1 | Protocol | Integer underflow in health check | 4.0 | Medium |
| PR-2 | Protocol | Health check buffer size (1024B) | 4.0 | Medium |
| DD-1 | DoS | No client connection limit if unset | 5.3 | Risk |
| DD-2 | DoS | Wait queue hardcoded 1000 limit | 5.3 | Configurable |
| DD-3 | DoS | Hardcoded 2min idle timeout | 5.3 | Gap |
| CT-1 | Connection | Transaction mode connection reuse | 8.1 | Review |

---

## 6. Recommendations

### High Priority

1. **Enforce TLS** - Add `tls: "require"` option that mandates encryption. Currently "prefer" allows plaintext.

2. **Add Proxy Protocol Support** - Implement HAProxy PROXY protocol v2 to preserve client source IP.

3. **Default Connection Limits** - Ensure `max_client_connections` has a safe default (e.g., 1000) instead of 0 (unlimited).

### Medium Priority

4. **Configurable Idle Timeout** - Move hardcoded 2-minute timeout to configuration.

5. **Wait Queue Limit** - Expose wait queue max size in configuration.

6. **Cluster Encryption** - Add TLS support for Raft/Gossip inter-node communication.

### Low Priority

7. **Localhost Binding** - Support `localhost` as pool bind address for internal-only pools.

8. **Backend TLS Verification** - Default to verifying backend certificates.

---

## 7. Risk Matrix

```
Severity | Count
---------|------
Critical | 1 (NW-2)
High     | 1 (CT-1)
Medium   | 6 (NW-1, NW-3, PR-1, PR-2, DD-1, DD-3)
Low      | 2 (DD-2, NW-4)
```

---

## 8. CVSS Scores Summary

| Vulnerability | Vector | Score |
|---------------|--------|-------|
| TLS Not Enforced | AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:N | 9.3 Critical |
| Connection Reuse | AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H | 8.1 High |
| Cluster Unencrypted | AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:L/A:N | 7.4 Medium |
| Wait Queue DoS | AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L | 5.3 Medium |
| Idle Timeout | AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L | 5.3 Medium |
| Unbound Clients | AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L | 5.3 Medium |
| 0.0.0.0 Binding | AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N | 4.8 Medium |
| Health Check Issues | AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L | 4.0 Medium |
