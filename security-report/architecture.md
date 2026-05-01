# Architecture Report — GeryonProxy Security Audit

**Target:** `github.com/GeryonProxy/geryon`  
**Language:** Go (100% — no other languages detected)  
**Application Type:** Database connection pooler/proxy (multi-protocol)  
**Scan Date:** 2026-05-01

---

## 1. Technology Stack Detection

### Languages
| Language | Files | Percentage | Notes |
|----------|-------|------------|-------|
| Go | 85+ | ~100% | Primary and only language |

### Frameworks & Libraries
| Library | Version | Purpose |
|---------|---------|---------|
| `github.com/go-sql-driver/mysql` | v1.9.3 | MySQL protocol driver |
| `github.com/lib/pq` | v1.12.3 | PostgreSQL driver |
| `golang.org/x/term` | v0.36.0 | Terminal utilities |
| `golang.org/x/time` | v0.15.0 | Rate limiting |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML config parsing |
| `filippo.io/edwards25519` | v1.1.1 | Ed25519 cryptography (indirect) |

### Build Tools
- Go 1.26.1 (go.mod)
- Make-based build system (`make build`, `make test`, `make lint`)
- CGO disabled for release builds (`CGO_ENABLED=0`)

---

## 2. Application Type Classification

**Database Connection Pooler & Proxy**

Geryon speaks three database wire protocols from a single static binary:
- **PostgreSQL** (port 5432) — Frontend/Backend v3
- **MySQL** (port 3306) — Handshake v10
- **MSSQL** (port 1433) — TDS 7.4+

Not a web application. No HTML templates, no REST-first architecture for client traffic. Management APIs (REST/HTTP2/MCP/Dashboard) are separate from the proxy traffic.

---

## 3. Entry Points Mapping

### Database Proxy Entry Points
| Endpoint | Port | Protocol | Handler |
|----------|------|----------|---------|
| PostgreSQL proxy | configurable | PostgreSQL wire | `internal/proxy/listener.go` |
| MySQL proxy | configurable | MySQL wire | `internal/proxy/listener.go` |
| MSSQL proxy | configurable | TDS wire | `internal/proxy/listener.go` |

### Admin/Management APIs
| Endpoint | Port | Protocol | Handler |
|----------|------|----------|---------|
| REST API | 8080 (default) | HTTP/1.1 | `internal/api/rest/server.go` |
| HTTP/2 Admin API | 9090 (default) | HTTP/2 JSON | `internal/api/grpc/server.go` |
| MCP Server | 8081 (default) | MCP (Model Context Protocol) | `internal/api/mcp/server.go` |
| Web Dashboard | 8080 (same as REST) | HTTP/1.1 | `internal/api/dashboard/server.go` |

### CLI Entry Points
| Command | Handler |
|---------|---------|
| `geryon --generate-config` | Config generation |
| `geryon --validate` | Config validation |
| `geryon --generate-password` | SCRAM-SHA-256 password hash generation |
| `geryon --generate-cert` | TLS certificate generation |

---

## 4. Data Flow Map

### Client-to-Backend Data Flow
```
Client (PostgreSQL/MySQL/MSSQL client)
  → TCP Listener (`internal/proxy/listener.go`)
  → Protocol-specific proxy session (`ProxySession`)
  → Authentication (`internal/auth/auth.go` — SCRAM-SHA-256)
  → Pool acquisition (`internal/pool/pool.go`)
  → Backend connection
  → Query routing (`internal/pool/routing.go` — read/write split)
  → Response back to client
  → Connection state reset (`internal/pool/reset.go`)
  → Return to pool
```

### Config Hot-Reload Flow
```
SIGHUP / File Watch / API POST → `internal/config/watcher.go`
  → `internal/config/loader.go` → YAML parsing
  → `atomic.Pointer[config.Config]` for lock-free update
```

### Admin API Flow
```
HTTP Request
  → `internal/api/rest/server.go` (REST) or `internal/api/grpc/server.go` (gRPC)
  → Auth middleware (token-based)
  → Handler (pools, backends, connections, users, cache management)
  → Response
```

---

## 5. Trust Boundaries

### Authentication
- **Proxy authentication:** SCRAM-SHA-256 (interception mode) or passthrough to backend
- **Admin API:** Token-based auth via `Authorization: Bearer <token>`
- **Auth limiter:** Per-IP rate limiting on auth failures (prevents brute force)

### Pool Boundaries
- `internal/pool/pool.go` — manages client connections and server connections separately
- Session mode: 1:1 client-to-backend mapping
- Transaction/Statement modes: N:M multiplexing

### Admin API Security
- Token-based authentication on all admin endpoints
- Rate limiting on gRPC admin API (`apiRateLimiter`)

---

## 6. External Integrations

### Databases (as backends)
- PostgreSQL backends — connection pooling, SCRAM-SHA-256 auth
- MySQL backends — connection pooling, auth passthrough or interception
- MSSQL backends — connection pooling

### Configuration
- YAML configuration file (hot-reloadable)
- File watching for config changes

### Metrics & Monitoring
- Prometheus metrics endpoint (`/metrics`)
- Structured logging via `internal/logger/logger.go`

### Clustering
- Raft consensus (`internal/raft/`) — configuration replication, leader election
- SWIM gossip (`internal/swim/`) — node discovery, failure detection

---

## 7. Authentication Architecture

| Component | Implementation |
|-----------|----------------|
| Proxy auth | SCRAM-SHA-256 (`internal/auth/scram.go`) |
| Admin auth | Token-based (`internal/api/rest/server.go:342`) |
| Auth rate limiting | Per-IP brute-force protection (`internal/auth/auth.go:446`) |
| Password hashing | SCRAM-SHA-256 with SaltedPassword algorithm |

---

## 8. File Structure Analysis

### Security-Sensitive Files
| Path | Purpose |
|------|---------|
| `internal/auth/auth.go` | User database, credential verification |
| `internal/auth/scram.go` | SCRAM-SHA-256 implementation |
| `internal/auth/cert.go` | Certificate-based auth |
| `internal/proxy/listener.go` | TCP listener, proxy sessions, auth handling |
| `internal/config/loader.go` | YAML parsing, config validation |
| `internal/api/rest/server.go` | Admin REST API with auth middleware |
| `internal/api/grpc/server.go` | Admin gRPC API with rate limiter |

### Deployment Files
- `deploy/kubernetes.yaml` — Kubernetes deployment manifest
- `.github/workflows/ci.yml` — Go test + lint pipeline
- `.github/workflows/docker.yml` — Docker image build/push
- `.github/workflows/release.yml` — Release artifact builds

---

## 9. Detected Security Controls

| Control | Implementation |
|---------|----------------|
| Auth rate limiting | `AuthLimiter` in `internal/auth/auth.go:446` |
| Admin API token auth | `withAuth` middleware in `internal/api/rest/server.go:342` |
| gRPC rate limiting | `apiRateLimiter` in `internal/api/grpc/server.go:220` |
| Connection state reset | `internal/pool/reset.go` — protocol-specific reset |
| Config hot-reload safety | `config.IsSafeReload()` — distinguishes safe vs unsafe changes |
| Atomic config access | `atomic.Pointer[config.Config]` for lock-free reads |
| Query classification | `internal/tokenizer/tokenizer.go` — read/write split |
| Health checking | `internal/pool/health.go` — backend health monitoring |
| Prepared statement cache | `internal/stmt/cache.go` |
| Result cache (LRU+TTL) | `internal/cache/store.go` |

---

## 10. Language Detection Summary

```
## Detected Languages
- Go (100% of codebase) → activates sc-lang-go
```

**Detected frameworks:** None (pure stdlib + minimal deps)  
**Application type:** Database proxy/pooler (not web-facing)  
**Entry points:** 3 database protocol listeners + 4 admin API endpoints  
**Trust boundaries:** Proxy auth (SCRAM-SHA-256), Admin token auth, Pool isolation

---

## Key Architectural Notes

1. **No web-facing attack surface** — database proxy listens on database ports, not HTTP
2. **Minimal dependencies** — only 5 production deps, all well-known
3. **Pure Go** — no CGO in production builds, cross-platform friendly
4. **Single binary** — embedded assets via `embed.FS`
5. **Atomic config** — lock-free reads during hot-reload via `atomic.Pointer`
6. **Multi-protocol** — handles PostgreSQL, MySQL, MSSQL in same process