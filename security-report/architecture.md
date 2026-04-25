# GeryonProxy Architecture Map

**Generated:** 2026-04-18

## Entry Points

- `cmd/geryon/main.go` -- sole `func main()`, no `init()` functions anywhere

## Internal Packages

| Package | Purpose |
|---------|---------|
| `internal/api/rest/` | REST API server (admin, metrics, Prometheus, pprof) |
| `internal/api/grpc/` | JSON-over-HTTP admin API (named "grpc" but uses HTTP, not protobuf) |
| `internal/api/mcp/` | MCP (Model Context Protocol) server over HTTP/SSE |
| `internal/api/dashboard/` | Dashboard server with static file serving (embed.FS) |
| `internal/auth/` | Authentication (SCRAM-SHA-256, MySQL native, cert auth, rate limiter) |
| `internal/cache/` | Query caching store (LRU with TTL) |
| `internal/cluster/` | Raft consensus + SWIM gossip cluster |
| `internal/config/` | YAML config loading, validation, env var expansion, hot-reload watcher |
| `internal/logger/` | Structured logging + query logging to files |
| `internal/metrics/` | Metrics collection |
| `internal/pool/` | Connection pool manager, routing, transaction manager, health checks |
| `internal/protocol/common/` | Protocol codec interface |
| `internal/protocol/postgresql/` | PostgreSQL wire protocol codec |
| `internal/protocol/mysql/` | MySQL wire protocol codec |
| `internal/protocol/mssql/` | MSSQL TDS protocol codec |
| `internal/proxy/` | TCP listeners, proxy sessions, bidirectional relay |
| `internal/raft/` | Raft consensus implementation |
| `internal/stmt/` | Prepared statement cache/manager |
| `internal/swim/` | SWIM gossip protocol implementation |
| `internal/tlsutil/` | TLS configuration helpers, cert generation |
| `internal/tokenizer/` | SQL tokenizer for query classification |

## Network Listeners

| Listener | Default Address | Protocol | Purpose |
|----------|----------------|----------|---------|
| REST API | `127.0.0.1:8080` | HTTP/TCP | Admin API, Prometheus metrics, pprof, dashboard |
| "gRPC" API | `127.0.0.1:9090` | HTTP/TCP (JSON) | Streaming stats, admin operations |
| MCP Server | `127.0.0.1:8081` | HTTP/TCP (SSE) | MCP tools/resources |
| Dashboard | `127.0.0.1:8082` | HTTP/TCP | Web dashboard with static files |
| Pool listeners | Per-pool config (default `0.0.0.0`) | TCP | Database proxy (PG/MySQL/MSSQL) |
| Cluster RPC | `0.0.0.0:7000` | TCP | Raft consensus RPC |
| SWIM Gossip | `0.0.0.0:7001` | UDP | SWIM failure detection gossip |

## Key Endpoints (REST API :8080)

- `GET/POST /api/v1/pools` -- List/create pools
- `GET/PUT/DELETE /api/v1/pools/{name}` -- CRUD individual pool
- `POST /api/v1/backends/{addr}/{action}` -- Backend drain/cancel-drain
- `GET /api/v1/health` -- Health check
- `GET /api/v1/config` -- View config
- `POST /api/v1/config/reload` -- Trigger hot-reload
- `GET/PUT /api/v1/config/file` -- Read/write raw YAML config file
- `GET/POST /api/v1/users` -- List/create users
- `DELETE /api/v1/users/{name}` -- Delete user
- `GET /metrics` -- Prometheus metrics (always requires auth)
- `/debug/pprof/*` -- Go profiling (always requires auth)

## External Dependencies (go.mod)

| Dependency | Purpose | Risk |
|-----------|---------|------|
| `golang.org/x/term v0.36.0` | Password input without echo | Low |
| `golang.org/x/time v0.15.0` | Rate limiter for admin APIs | Low |
| `golang.org/x/sys v0.37.0` | Indirect (term/time dep) | Low |

Note: Custom YAML parser -- no yaml library imported. No CGO dependencies.

## Authentication Mechanisms

**Proxy-level (database clients):**
- Passthrough mode: credentials forwarded to backend
- Interception mode: proxy authenticates clients (SCRAM-SHA-256 for PG, caching_sha2_password for MySQL, NTLM/SSPI for MSSQL)
- Certificate-based auth via CN/SAN extraction
- Auth rate limiter: 10 failures per 5-minute window, then 5-minute lockout per IP
- Per-user connection limits

**Admin API:**
- Bearer token authentication via `Authorization: Bearer <token>`
- Constant-time comparison via `crypto/subtle.ConstantTimeCompare`
- Auth can be independently enabled/disabled per service

## Crypto/TLS

- TLS 1.2 minimum enforced
- AEAD-only cipher suites (no CBC, RC4)
- SCRAM-SHA-256 with 120,000 PBKDF2 iterations
- Self-signed cert generation uses ECDSA P-256
- No `InsecureSkipVerify` in production configs
- Password zeroing after use in memory
