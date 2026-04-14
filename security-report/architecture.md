# GeryonProxy Security Architecture Report

**Project:** GeryonProxy - Multi-Database Connection Pooler
**Language:** Go 1.26.1 (100% stdlib, zero external runtime dependencies)
**Files:** 108 Go source files
**Date:** 2026-04-14

---

## 1. Entry Points

### 1.1 Network Listeners

All network listeners are configurable via `geryon.yaml`. Default binds are localhost-only for admin interfaces.

| Interface | Default Address | Protocol | Purpose |
|-----------|-----------------|----------|---------|
| PostgreSQL Proxy | `0.0.0.0:5432` | PostgreSQL Wire | Database connection pooling |
| MySQL Proxy | `0.0.0.0:3306` | MySQL Wire | Database connection pooling |
| MSSQL Proxy | `0.0.0.0:1433` | TDS Protocol | Database connection pooling |
| REST API | `127.0.0.1:8080` | HTTP | Admin management interface |
| gRPC API | `127.0.0.1:9090` | HTTP/2 | Admin management interface |
| MCP Server | `127.0.0.1:8081` | HTTP/SSE | Admin management interface |
| Dashboard | `127.0.0.1:8082` | HTTP | Web dashboard |
| Raft Cluster | Configurable | Raft | Distributed consensus |
| Gossip Cluster | Configurable | SWIM | Node discovery |

### 1.2 CLI Entry Point

**File:** `cmd/geryon/main.go`

```go
// Command-line flags (lines 37-44)
--config           // Path to configuration file
--validate         // Validate config without starting
--version          // Print version and exit
--generate-config  // Generate example configuration
--generate-password // Generate SCRAM-SHA-256 password hash
--generate-cert    // Generate self-signed TLS certificate
```

### 1.3 Signal Handling

The proxy handles three signals:
- **SIGINT/SIGTERM** (lines 273-276): Graceful shutdown - stops listeners, closes pools
- **SIGHUP** (lines 260-272): Hot configuration reload via `config.HotReload()`

### 1.4 Proxy Listener Startup

**File:** `internal/proxy/listener.go`

Each pool creates a `Listener` (lines 59-141) that:
1. Creates TCP listener on configured host:port
2. Optionally wraps with TLS via `tls.NewListener()`
3. Starts protocol-aware health checks
4. Runs `acceptLoop()` goroutine for incoming connections

```go
// Listener creation chain (main.go lines 144-165)
for _, poolCfg := range cfg.Pools {
    listener, err := proxy.NewListener(pool, poolCfg, codec, userDB, log)
    listener.Start()
}
```

---

## 2. Trust Boundaries

### 2.1 Client-to-Proxy Boundary

**Location:** `internal/proxy/listener.go:254-306` (`handleConnection`)

Untrusted input enters the system at this boundary:
- Client network connections from application clients
- Startup message parameters (username, database, SSL preferences)
- SQL query payloads
- Authentication credentials

**Trust boundary enforcement:**
- `MaxClientConnections` limit check (line 266)
- Client connection counter atomic increment (line 266)
- Connection deadline set to prevent slowloris (line 248)

### 2.2 Admin API Boundary

**Location:** `internal/api/rest/server.go:224-251` (`withAuth`)

All admin API endpoints require Bearer token authentication:
```go
// Token comparison using constant-time equality
subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.config.Auth.Token))
```

**Security headers applied to all responses:**
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `X-XSS-Protection: 1; mode=block`
- `Cache-Control: no-store, no-cache, must-revalidate`

### 2.3 Backend-to-Proxy Boundary

**Location:** `internal/pool/pool.go:798-848` (`tryConnect`)

The proxy initiates connections to backend databases. Backend responses are treated as untrusted and validated:
- PostgreSQL message type validation
- MySQL packet length bounds checking (`maxMySQLPayload = 16<<20`)
- TDS message length validation

### 2.4 Configuration File Boundary

**Location:** `internal/config/loader.go:36-53`

Configuration is loaded from disk with environment variable expansion restricted to `GERYON_*` prefix only (line 34):
```go
var allowedEnvPrefix = "GERYON_"
```

---

## 3. Authentication & Authorization

### 3.1 Authentication Mechanisms

#### PostgreSQL: SCRAM-SHA-256 (Interception Mode)

**Files:**
- Server: `internal/auth/auth.go:115-277` (`SCRAMServer`)
- Client: `internal/auth/scram.go:138-245` (`SCRAMClient`)
- Password generation: `internal/auth/scram.go:15-43`

**Flow:** `handlePostgreSQLAuth()` (lines 663-824)
1. Client sends `SASLInitialResponse` with mechanism `SCRAM-SHA-256`
2. Server parses `client-first-message` (GS2 header + nonce)
3. Server generates `server-first-message` with salt and server nonce
4. Client sends `client-final-message` with proof
5. Server verifies proof using stored key
6. Server sends `server-final-message` with server signature
7. Authentication success: `authenticated.Store(true)`

**Password hash format:**
```
SCRAM-SHA-256$<iterations>:<salt>$<storedkey>:<serverkey>
```
- Iterations: 120000 (OWASP 2023+ recommendation)
- Salt: 32 bytes random
- StoredKey: SHA256(ClientKey)
- ServerKey: HMAC-SaltedPassword, "Server Key"

#### MySQL: Handshake V10 (Passthrough)

**File:** `internal/proxy/listener.go:1167-1274`

MySQL authentication is passthrough - the proxy forwards the handshake between client and backend without interception.

#### MSSQL: TDS Login7 (Passthrough)

**File:** `internal/proxy/listener.go:1522-1551`

MSSQL authentication is passthrough via `forwardMSSQLLogin7()`.

### 3.2 Authorization

**File:** `internal/auth/auth.go:16-33`

Pool access authorization via `User.CanAccessPool()`:
```go
func (u *User) CanAccessPool(poolName string) bool {
    for _, allowed := range u.AllowedPools {
        if allowed == "*" || allowed == poolName {
            return true
        }
    }
    return false
}
```

**Authorization check location:** `proxy/listener.go:644-652`
```go
if !user.CanAccessPool(ps.pool.Name()) {
    errMsg := postgresql.CreateErrorResponse("28000", "access to pool denied")
    ps.clientConn.Write(errMsg)
    return fmt.Errorf("access denied for user %s to pool %s", ps.username, ps.pool.Name())
}
```

### 3.3 Client Certificate Authentication (mTLS)

**File:** `internal/proxy/listener.go:2419-2460`

Optional TLS client certificate authentication via `authenticateWithCertificate()`:
1. Extract identity from certificate CN or SAN
2. Validate certificate NotBefore/NotAfter
3. Map certificate to user in UserDatabase

### 3.4 Brute Force Protection

**File:** `internal/auth/auth.go:472-585`

`AuthLimiter` tracks failed authentication attempts per source IP:
```go
// Default limits
maxAttempts:   10
window:        5 * time.Minute
lockoutPeriod: 5 * time.Minute
```

**Applied in:**
- PostgreSQL interception auth: `proxy/listener.go:665-672`
- MySQL/MSSQL passthrough: rate limiting not applied (relies on backend)

### 3.5 Admin API Authentication

**File:** `internal/api/rest/server.go:224-251`

Bearer token authentication for all admin endpoints:
- Header format: `Authorization: Bearer <token>`
- Constant-time comparison to prevent timing attacks

---

## 4. Data Flow

### 4.1 Client Connection Lifecycle

```
Client TCP Connection
        |
        v
proxy.Listener.acceptLoop()  [listener.go:235-251]
        |
        v
proxy.Listener.handleConnection()  [listener.go:254-306]
        |  - Atomic client count increment
        |  - MaxClientConnections check
        |  - Creates ProxySession
        v
proxy.ProxySession.Handle()  [listener.go:474-499]
        |
        +---> handleStartup()  [listener.go:501-513]
        |         |
        |         +---> handlePostgreSQLStartup() [listener.go:516-661]
        |         |         - Parse startup message (max 10000 bytes, max 64 params)
        |         |         - Validate username/database for null bytes/control chars
        |         |         - User lookup via userDB.GetUser()
        |         |         - Pool access check via user.CanAccessPool()
        |         |         +---> handlePostgreSQLAuth() (interception mode)
        |         |         |         - AuthLimiter check
        |         |         |         - SCRAM server authentication
        |         |         +---> connectToBackend() (passthrough mode)
        |         |
        |         +---> handleMySQLStartup() [listener.go:1168-1274]
        |         |         - Passthrough to backend
        |         |
        |         +---> handleMSSQLStartup() [listener.go:1523-1552]
        |                   - Passthrough to backend
        |
        +---> poolSession.Strategy().OnClientConnect()  [pool/strategy.go]
        |         - Acquires server connection from pool
        |         - Handles backend authentication
        |
        v
proxy.Relay.Run()  [listener.go:1862-1915]
        |
        +---> forwardClientToServer()  [listener.go:1918-2108]
        |         - codec.ReadMessage() - reads from client
        |         - IsQuery() - checks if SQL query
        |         - RouteQuery() - read/write splitting
        |         - Cache check (if enabled)
        |         - OnQuery() - get server connection
        |         - codec.WriteMessage() - send to backend
        |
        +---> forwardServerToClient()  [listener.go:2295-2410]
                  - codec.ReadMessage() - reads from backend
                  - codec.WriteMessage() - send to client
```

### 4.2 Backend Connection Pool

**File:** `internal/pool/pool.go:461-485`

```
Pool.Acquire()  [pool.go:600-630]
        |
        +---> serverConnPool.acquire() - get from idle pool
        |
        +---> createServerConn() - create new connection
        |         - selectBackendWithFallback()
        |         - tryConnect(backend) - TCP/TLS connection
        |         - Backend auth via authenticateToBackend()
        |
        +---> waitQueue.Wait() - wait for available connection
        |
        v
Pool.Release()  [pool.go:663-672]
        |
        +---> waitQueue.Signal() - give to waiting client
        |
        +---> serverConnPool.release() - return to idle pool
                    - ResetConnection() - protocol-specific cleanup
```

### 4.3 Connection State Reset (Pool Reset)

**File:** `internal/pool/reset.go`

Before returning a connection to the idle pool, state is reset:

**PostgreSQL:** `DISCARD ALL` command
**MySQL:** `COM_RESET_CONNECTION` (5.7.3+) or `COM_CHANGE_USER`
**MSSQL:** RPC request for `sp_reset_connection`

---

## 5. Secret Handling

### 5.1 Password Storage

**User passwords (interception mode):**
- Format: `SCRAM-SHA-256$<iterations>:<salt>$<storedkey>:<serverkey>`
- Stored in: `config.Auth.Users[].PasswordHash`
- Loaded via: `auth.UserDatabase.LoadFromConfig()` [auth.go:48-68]

**Backend passwords:**
- Storage: Files referenced via `backend.auth.password_file`
- Loading: `proxy/listener.go:850-860`
- Example: `password_file: "/etc/geryon/secrets/pg"`

### 5.2 Password Processing

**Password hashing (generation):** `auth.GenerateSCRAMSHA256()` [scram.go:15-43]
```go
salt := make([]byte, 32)
iterations := 120000
saltedPassword := pbkdf2Key([]byte(password), salt, iterations, 32, sha256.New)
clientKey := hmacSum(saltedPassword, []byte("Client Key"))
storedKey := sha256.Sum256(clientKey)
```

**Password verification:** `auth.VerifySCRAMSHA256()` [scram.go:91-136]
- Uses `subtle.ConstantTimeCompare()` for timing-safe comparison

### 5.3 Memory Security

**Password zeroing (M-11 fix):** `cmd/geryon/main.go:341-346`
```go
defer func() {
    for i := range passwordBytes {
        passwordBytes[i] = 0
    }
}()
```

**Backend password zeroing:** `proxy/listener.go:856-859`
```go
for i := range passwordBytes {
    passwordBytes[i] = 0
}
```

### 5.4 TLS Secrets

**Certificate loading:** `tlsutil.LoadServerConfig()` [tlsutil/config.go]
- `tls.LoadX509KeyPair(certFile, keyFile)` for server cert
- `x509.NewCertPool()` for CA certificates
- Min TLS version: TLS 1.2

**Cipher suites:** `tlsutil.CipherSuites12()` enforces strong cryptography

### 5.5 Admin API Tokens

**Storage:** `config.Admin.REST.Auth.Token` (plaintext in config)
**Transport:** Bearer token in Authorization header
**Comparison:** Constant-time via `subtle.ConstantTimeCompare()`

---

## 6. Configuration Security

### 6.1 Configuration Loading

**File:** `internal/config/loader.go:36-53`

```go
func Load(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    expanded := expandEnvVars(string(data))  // Only GERYON_* vars
    cfg, err := parseYAML(expanded)
    return cfg, nil
}
```

### 6.2 Environment Variable Expansion

**File:** `internal/config/loader.go:55-79`

Only `GERYON_*` prefixed variables are expanded:
```go
var allowedEnvPrefix = "GERYON_"
```

Syntax: `${VAR}` or `${VAR:-default}`

### 6.3 Configuration Validation

**File:** `internal/config/config.go:259-343`

```go
func Validate(cfg *Config) error {
    // Admin listen addresses must be non-empty
    // Admin auth requires token when enabled
    // Pool names must be unique
    // Pool body types: postgresql, mysql, mssql
    // Pool modes: session, transaction, statement
    // Port conflicts detected
    // Cluster requires node_id and raft.listen when enabled
}
```

### 6.4 Hot Reload

**File:** `cmd/geryon/main.go:227-252`

Configuration watched for changes via `config.NewWatcher()`:
- Safe changes applied without restart (pool limits, auth users, log level)
- Unsafe changes require restart (ports, body type, TLS cert paths)

### 6.5 Security-Sensitive Configuration

| Setting | Location | Risk |
|---------|----------|------|
| `admin.rest.auth.token` | Config file | Controls API access |
| `admin.grpc.auth.token` | Config file | Controls gRPC access |
| `admin.mcp.auth.token` | Config file | Controls MCP access |
| `admin.dashboard.auth.token` | Config file | Controls dashboard access |
| `auth.users[].password_hash` | Config file | Credentials |
| `backend.auth.password_file` | Config file | Backend credentials |
| `tls.cert_file` | Config file | TLS certificate |
| `tls.key_file` | Config file | TLS private key |
| `pools[].listen.host` | Config file | Binding address |
| `pools[].listen.port` | Config file | Service exposure |

### 6.6 Path Traversal Protection

**File:** `internal/proxy/listener.go:79-81`
```go
safeName := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(cfg.Name, "_")
qlConfig.Directory = filepath.Join("logs", "queries", safeName)
```

**Config path sanitization:** `cmd/geryon/main.go:78-79`
```go
safeConfigPath := filepath.Clean(*configPath)
```

---

## Appendix: Security Component Map

### Authentication Flow

```
Client Connection
       |
       v
handleStartup()  [proxy/listener.go:501-513]
       |
       +---> postgresql: handlePostgreSQLStartup() [516-661]
       |         |
       |         +---> handlePostgreSQLAuth() [663-824]
       |         |         - AuthLimiter check [665-672]
       |         |         - SCRAMServer.ParseClientFirst() [721]
       |         |         - SCRAMServer.GenerateServerFirst() [733]
       |         |         - SCRAMServer.VerifyClientFinal() [772]
       |         |         - recordAuthSuccess/recordAuthFailure()
       |         |
       |         +---> connectToBackend() [826-885]
       |                   - authenticateToBackend() [888-976]
       |                   - forwardAuthFromBackend() [1056-1125]
       |
       +---> mysql: handleMySQLStartup() [1168-1274]
       |         - Passthrough auth to backend
       |
       +---> mssql: handleMSSQLStartup() [1523-1552]
                 - Passthrough auth to backend
```

### Rate Limiting Layers

```
REST API: withRateLimit() [rest/server.go:329-346]
         - Per-IP token bucket (10 req/s, burst 20)
         - Periodic cleanup of old entries

Auth Limiter: AuthLimiter [auth/auth.go:472-585]
         - Per-IP failed attempt tracking
         - 10 attempts per 5 min window
         - 5 min lockout after failure

gRPC: internal/api/grpc/server.go [191-276]
MCP: internal/api/mcp/server.go [154-234]
Dashboard: internal/api/dashboard/server.go [163-243]
```

### Input Validation Points

| Location | Input Type | Validation |
|----------|------------|------------|
| listener.go:526 | Startup message length | 8 <= length <= 10000 |
| listener.go:578 | Startup parameter count | max 64 params |
| listener.go:579 | Startup parameter value length | max 256 bytes |
| listener.go:621-627 | Username/database chars | No control chars (0x00-0x1F, 0x7F) |
| listener.go:711 | SASL mechanism | Only "SCRAM-SHA-256" |
| listener.go:1076 | Backend message length | 0 <= length <= maxMySQLPayload |
| listener.go:1571 | MSSQL Pre-Login length | 0 <= length <= maxMySQLPayload |
| listener.go:1645 | MSSQL Login7 length | 0 <= length <= maxMySQLPayload |
| rest/server.go:374 | Pool name | Regex: `^[a-zA-Z0-9_-]{1,64}$` |
| rest/server.go:440 | JSON body size | Max 1MB via `http.MaxBytesReader` |
| pool/routing.go:56 | Routing rule pattern | `regexp.Compile()` with error check |

---

*Document generated: 2026-04-14*
*Purpose: Security-focused architecture reconnaissance*