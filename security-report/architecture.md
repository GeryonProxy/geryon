# GeryonProxy Architecture Security Report

## Component Diagram

```
                                    +------------------+
                                    |   REST API       |
                                    |   (:8080)        |
                                    +--------+---------+
                                             |
+----------+  +---------+  +---------+  +---+----+  +---------+  +----------+
|  Client  |--| Proxy   |--| Pool    |--|Cache|--| Manager|--|  Admin   |
| (PG/MySQL|  |Listener |  | Manager |  |Store|  |        |  |  Servers |
| /MSSQL)  |  | (:5432) |  |         |  |    |  |        |  |  gRPC    |
+----------+  +---------+  +---------+  +----+  +--------+  | (:9090)  |
                     |          |                             |  MCP     |
                     |          +---------+--------+          | (:8081)  |
                     |                   |        |          |Dashboard |
                     +--------+--------+ |        | +----+   | (:8082)  |
                                 |        | |Raft|   +----------+
                            +----+---+    |   |  SWIM  |   |
                            |PostgreSQL|  |   | Gossip |
                            | Codec    |  |   +--------+
                            +----------+  |     +--------+
                                          +-----| Cluster|
                                                |Coordina-|
                                                |tor      |
                                                +---------+
```

## Entry Points Table

| Component | File | Purpose | Line Reference |
|-----------|------|---------|----------------|
| Main CLI | `cmd/geryon/main.go` | Entry point, signal handling, config loading | 36-327 |
| Password Generation | `cmd/geryon/main.go` | `generatePasswordHash()` for SCRAM-SHA-256 password hashing | 329-360 |
| Certificate Generation | `cmd/geryon/main.go` | `generateSelfSignedCert()` for TLS cert generation | 362-391 |
| Config Hot-Reload | `cmd/geryon/main.go` | SIGHUP handler with `HotReload()` | 261-274 |

## Network Listeners Table

| Listener Type | Default Address | Protocol | TLS | Auth | Config Key |
|---------------|-----------------|----------|-----|------|------------|
| PostgreSQL Proxy | `0.0.0.0:5432` | PostgreSQL v3 | Optional | Per-pool | `pools[].listen` |
| MySQL Proxy | `0.0.0.0:3306` | MySQL | Optional | Per-pool | `pools[].listen` |
| MSSQL Proxy | `0.0.0.0:1433` | MSSQL/TDS | Optional | Per-pool | `pools[].listen` |
| REST Admin API | `127.0.0.1:8080` | HTTP | Optional | Token | `admin.rest.listen` |
| gRPC API | `127.0.0.1:9090` | HTTP/2 | Optional | Token | `admin.grpc.listen` |
| MCP Server | `127.0.0.1:8081` | HTTP/SSE | Optional | Token | `admin.mcp.listen` |
| Dashboard | `127.0.0.1:8082` | HTTP | Optional | Token | `admin.dashboard.listen` |
| Raft Cluster | `0.0.0.0:7000` | Raft RPC (TCP/JSON) | No | None | `cluster.raft.listen` |
| SWIM Gossip | `0.0.0.0:7001` | UDP | No | None | `cluster.gossip.listen` |

## Core Components

### internal/pool/
| File | Purpose | Key Security Notes |
|------|---------|-------------------|
| `pool.go` | Connection pool management, backend selection | Circuit breaker, memory limits |
| `manager.go` | Global pool manager | Memory tracking via `TryAlloc()`/`Free()` |
| `session.go` | Per-client session state | Per-user connection limits |
| `health.go` | Backend health checking | Failure tracking for circuit breaker |
| `routing.go` | Read/write query routing | Determines replica vs primary |
| `transaction.go` | Transaction lifecycle management | Timeout enforcement |
| `reset.go` | Connection state reset | Resets connection before reuse |

### internal/proxy/
| File | Purpose | Key Security Notes |
|------|---------|-------------------|
| `listener.go` | Accepts client connections | Rate limiting, TLS, `authLimiter` for brute-force protection |

### internal/auth/
| File | Purpose | Key Security Notes |
|------|---------|-------------------|
| `auth.go` | User database, SCRAM-SHA-256 | 120000 iterations (OWASP), `AuthLimiter` for rate limiting |
| `scram.go` | SCRAM client/server implementation | Constant-time comparison, password zeroing |

### internal/protocol/ (singular - wire codecs)
| File | Purpose |
|------|---------|
| `postgresql/codec.go` | PostgreSQL v3.0 protocol codec |
| `mysql/codec.go` | MySQL protocol codec |
| `mssql/codec.go` | MSSQL/TDS protocol codec |

### internal/cluster/
| File | Purpose | Key Security Notes |
|------|---------|-------------------|
| `cluster.go` | Raft consensus, leader election | 10s read deadline for RPC, bounded JSON decoding (1MB) |
| `coordinator.go` | Cluster coordination | - |

### internal/swim/
| File | Purpose | Key Security Notes |
|------|---------|-------------------|
| `swim.go` | SWIM gossip protocol for node discovery | Address validation (`isValidAddress()`), max 64KB messages |

### internal/api/
| File | Purpose | Key Security Notes |
|------|---------|-------------------|
| `rest/server.go` | REST API, metrics, dashboard | Bearer token auth, rate limiting (10 req/s), CORS, pprof secured |
| `mcp/server.go` | Model Context Protocol server | Bearer token, per-IP rate limiting (5 req/s, burst 10) |
| `dashboard/server.go` | Web dashboard | - |
| `grpc/server.go` | gRPC API | - |

### internal/config/
| File | Purpose | Key Security Notes |
|------|---------|-------------------|
| `loader.go` | YAML config parsing | Only `GERYON_` env vars expanded |
| `watcher.go` | Config file monitoring | SHA-256 hash comparison |

## Technology Inventory

| Category | Technology | Version/Notes |
|----------|------------|---------------|
| Language | Go | 1.26.1 |
| TLS | `crypto/tls` | TLS 1.2+ required (`MinVersion: tls.VersionTLS12`) |
| Ed25519 | `filippo.io/edwards25519` | For EdDSA support |
| YAML | `gopkg.in/yaml.v3` | Config parsing |
| MySQL Driver | `github.com/go-sql-driver/mysql` | v1.9.3 |
| PostgreSQL Driver | `lib/pq` | v1.12.3 |
| Rate Limiting | `golang.org/x/time/rate` | Token bucket algorithm |
| Terminal | `golang.org/x/term` | Password reading without echo |

### CGO Dependencies
- None identified - pure Go implementation

### System Calls
- `net.Listen`, `net.Dial` - TCP/UDP networking
- `os.ReadFile`, `os.WriteFile` - File I/O
- `signal.Notify` - Signal handling (SIGHUP, SIGINT, SIGTERM)
- `tls.Server`, `tls.Client` - TLS handshake

## Trust Boundary Analysis

### Unauthenticated (External) Attack Surface

| Endpoint | Risk Level | Notes |
|----------|------------|-------|
| Database proxy listeners (PG/MySQL/MSSQL) | HIGH | Accepts raw database protocol connections |
| Config file watcher | MEDIUM | Reloads config on file change |
| SIGHUP handler | MEDIUM | Triggers config hot-reload |

### Authenticated Client Capabilities

| Capability | Condition | Risk |
|------------|-----------|------|
| Execute SQL queries | Authenticated via SCRAM or passthrough | Core function |
| Prepared statement handling | Authenticated | Re-preparation on new connections |
| Transaction management | Authenticated | Timeout enforcement |
| Read/write split routing | Pool has `routing.read_write_split` enabled | Query classification |
| Query cache | Pool has `cache.enabled` | Cache invalidation on writes |

### Admin API Capabilities (Authenticated)

| Endpoint | Operations | Risk |
|----------|------------|------|
| `POST /api/v1/pools` | Create pool | HIGH - Can create new listeners |
| `PUT /api/v1/pools/{name}` | Update pool config | MEDIUM - Modifies limits |
| `DELETE /api/v1/pools/{name}` | Remove pool | MEDIUM - Service disruption |
| `POST /api/v1/backends/{addr}/drain` | Drain backend | LOW - Graceful removal |
| `POST /api/v1/config/reload` | Hot reload config | HIGH - Applies config changes |
| `/metrics` | Prometheus metrics | MEDIUM - Information disclosure |
| `/debug/pprof/*` | Profiling endpoints | HIGH - Requires auth (protected) |

### Backend Connection Handling

| Mode | Auth Method | Security Notes |
|------|-------------|----------------|
| Passthrough | Forward to backend | Client auth directly to DB |
| Interception | SCRAM-SHA-256 (PG), SHA256 (MySQL) | Proxy authenticates, then connects to backend |

## Network Attack Surface

### Protocol Handlers
1. **PostgreSQL** - `internal/protocol/postgresql/codec.go`
   - Startup message parsing (length validation: 8-10000 bytes)
   - Parameter parsing (max 64 params, max 256 bytes each)
   - SSL request handling
   - SCRAM-SHA-256 authentication

2. **MySQL** - `internal/protocol/mysql/codec.go`
   - Handshake packet parsing
   - Capability flag handling
   - Challenge-response auth (caching_sha2_password, mysql_native_password)

3. **MSSQL** - `internal/protocol/mssql/codec.go`
   - Pre-Login and Login7 message handling
   - UTF-16LE username extraction

### Potential Unsafe Parsing Areas

| Location | Concern | Mitigations |
|----------|---------|-------------|
| `proxy/listener.go:566-567` | Startup message length (8-10000 bytes) | Bounds checking |
| `proxy/listener.go:627-662` | Startup parameter parsing | `maxStartupParams=64`, `maxValueLen=256` |
| `proxy/listener.go:668-674` | Username/database validation | Null byte and control char rejection |
| `swim/swim.go:248-262` | Gossip message size | Bounded to 65536 bytes |
| `swim/swim.go:700-703` | Address validation | `isValidAddress()` validates host:port |
| `cluster/cluster.go:217` | RPC payload size | Limited to 1MB via `io.LimitReader` |
| `cluster/cluster.go:214` | 10s read deadline | Slowloris protection |

## Config Hot-Reload Attack Surface

### SIGHUP Handler (main.go:261-274)
- Reloads entire config file
- Stores new config atomically via `cfgHolder.Store()`
- Validation via `config.Validate()`

### File Watcher (config/watcher.go)
- Polls config file every 5 seconds
- Compares SHA-256 hash before reload
- `IsSafeReload()` identifies unsafe changes requiring restart

### Environment Variable Expansion (config/loader.go:58-82)
- Only `GERYON_*` prefixed variables expanded
- Other `${VAR}` references left as-is or use default value
- Prevents accidental exposure of system secrets

## Security Controls Identified

### Authentication
- SCRAM-SHA-256 with 120000 iterations (OWASP 2023+)
- MySQL `caching_sha2_password` and `mysql_native_password` support
- Per-user connection limits
- Certificate authentication (mTLS)

### Rate Limiting
- `AuthLimiter`: 10 failures per 5-minute window, 5-minute lockout
- REST API: 10 req/s with burst of 20
- MCP: 5 req/s per IP
- Per-IP limiter maps with LRU eviction (max 10000 entries)

### Connection Security
- TLS 1.2+ required (`MinVersion: tls.VersionTLS12`)
- Cipher suites defined in `tlsutil`
- Slowloris protection via deadlines
- TCP keepalive enabled (30s-3min intervals)

### Memory Safety
- Global memory limit tracking for server connections
- 32KB estimated per connection
- Buffer pool for relay operations (sync.Pool)
- Password buffer zeroing after use (M-11)

### Input Validation
- Pool name validation: `^[a-zA-Z0-9_-]{1,64}$`
- Error message sanitization (200 char limit)
- SQL comment stripping for transaction detection (M-6 fix)

## Files Analyzed

- `cmd/geryon/main.go` - Entry point, 403 lines
- `internal/proxy/listener.go` - Proxy listener, 3126 lines
- `internal/pool/pool.go` - Connection pool, 1542 lines
- `internal/pool/manager.go` - Pool manager, 245 lines
- `internal/auth/auth.go` - Authentication, 695 lines
- `internal/auth/scram.go` - SCRAM implementation, 255 lines
- `internal/config/config.go` - Config structures, 346 lines
- `internal/config/loader.go` - Config parsing, 942 lines
- `internal/config/watcher.go` - Config watching, 395 lines
- `internal/cluster/cluster.go` - Cluster/Raft, 726 lines
- `internal/swim/swim.go` - SWIM gossip, 704 lines
- `internal/api/rest/server.go` - REST API, 1337 lines
- `internal/api/mcp/server.go` - MCP server, 575 lines
- `internal/protocol/postgresql/codec.go` - PostgreSQL codec (partial)
