<p align="center">
  <strong>GERYON</strong><br/>
  <em>Three Bodies. One Proxy. Every Connection.</em>
</p>

<p align="center">
  <a href="https://github.com/GeryonProxy/geryon/releases"><img src="https://img.shields.io/github/v/release/GeryonProxy/geryon?style=flat-square" alt="Release"></a>
  <a href="https://ghcr.io/geryonproxy/geryon"><img src="https://img.shields.io/badge/container-ghcr.io-blue?style=flat-square&logo=github" alt="GHCR"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue?style=flat-square" alt="License"></a>
  <a href="https://geryonproxy.com"><img src="https://img.shields.io/badge/docs-geryonproxy.com-brightgreen?style=flat-square" alt="Docs"></a>
  <br/>
  <img src="https://img.shields.io/badge/Go-stdlib%20only-00ADD8?style=flat-square&logo=go" alt="Go">
  <img src="https://img.shields.io/badge/deps-zero-success?style=flat-square" alt="Zero Dependencies">
  <img src="https://img.shields.io/badge/CGo-disabled-inactive?style=flat-square" alt="No CGo">
</p>

---

# Geryon

A high-performance, multi-database connection pooler and proxy built in **pure Go** with **zero external dependencies**. Named after the three-bodied giant of Greek mythology, Geryon speaks PostgreSQL, MySQL, and MSSQL wire protocols — all from a single static binary.

## Why Geryon?

Running PostgreSQL, MySQL, and MSSQL? Today you need separate tools for each:

| Problem | Existing Solution | Limitation |
|---|---|---|
| PostgreSQL pooling | PgBouncer | C, limited observability, no clustering |
| MySQL pooling | ProxySQL | C++, complex configuration |
| MSSQL pooling | *(nothing)* | Driver-level pooling only |

Three tools. Three configs. Three monitoring setups. Three failure modes.

**Geryon replaces all of them with one binary.**

## Features

### Three Bodies — Protocol Support

| Body | Protocol | Wire Format | Auth Methods |
|---|---|---|---|
| **I — PostgreSQL** | v3 Frontend/Backend | Extended Query, COPY, LISTEN/NOTIFY | SCRAM-SHA-256, MD5, trust, cert |
| **II — MySQL** | Handshake v10 | COM_QUERY, COM_STMT_*, COM_CHANGE_USER | mysql_native_password, caching_sha2, sha256 |
| **III — MSSQL** | TDS 7.4+ | SQL Batch, RPC, Bulk Load | SQL Auth, NTLM passthrough |

### Pooling Modes

| Mode | Multiplexing | Best For |
|---|---|---|
| **Session** | 1:1 | Temp tables, SET vars, LISTEN/NOTIFY |
| **Transaction** | N:M | Web apps, microservices (default) |
| **Statement** | N:1 | Simple query patterns, max throughput |

### Core Capabilities

- **Prepared Statement Cache** — Transparent re-preparation across pooled connections with LRU eviction
- **Query Result Cache** — In-memory LRU cache with TTL, write invalidation, and per-pattern rules
- **Read/Write Splitting** — Route SELECTs to replicas, writes to primary, transaction-aware
- **Auth Interception** — Manage proxy users, map N clients to M backend credentials
- **TLS/mTLS** — Full TLS termination with mutual TLS client certificate validation
- **Hot Reload** — Config changes via YAML watch, SIGHUP, or API — zero downtime

### Management Interfaces

| Interface | Description | Port |
|---|---|---|
| **REST API** | Full CRUD for pools, connections, backends, users, cache | `:8080` |
| **Web Dashboard** | Real-time monitoring with SSE streaming, config editor | `:8080` |
| **MCP Server** | LLM-native management (Claude Code / Claude Desktop) | `:8081` |
| **gRPC API** | Programmatic integration with streaming stats | `:9090` |

### Clustering

- **Raft Consensus** — Configuration replication and leader election across nodes
- **SWIM Gossip** — Node discovery, failure detection, metadata dissemination
- **Backend Health Sharing** — Avoid thundering herd on failover

## Quick Start

### Binary

```bash
# Download latest release
curl -sSL https://github.com/GeryonProxy/geryon/releases/latest/download/geryon-linux-amd64 -o geryon
chmod +x geryon

# Generate example config
./geryon --generate-config > geryon.yaml

# Edit config (set your database backends)
vim geryon.yaml

# Start
./geryon --config geryon.yaml
```

### Container (GHCR)

```bash
docker run -d \
  --name geryon \
  -p 5432:5432 \
  -p 3306:3306 \
  -p 1433:1433 \
  -p 8080:8080 \
  -v ./geryon.yaml:/etc/geryon/geryon.yaml \
  ghcr.io/geryonproxy/geryon:latest
```

### Docker Compose

```yaml
services:
  geryon:
    image: ghcr.io/geryonproxy/geryon:latest
    ports:
      - "5432:5432"   # PostgreSQL
      - "3306:3306"   # MySQL
      - "1433:1433"   # MSSQL
      - "8080:8080"   # Dashboard + REST API
    volumes:
      - ./geryon.yaml:/etc/geryon/geryon.yaml
    restart: unless-stopped
```

### Build From Source

```bash
git clone https://github.com/GeryonProxy/geryon.git
cd geryon
make build
# Binary at bin/geryon
```

## Configuration

### Minimal — Single PostgreSQL Pool

```yaml
pools:
  - name: "my-postgres"
    body: postgresql
    mode: transaction
    listen:
      host: "0.0.0.0"
      port: 5432
    backend:
      hosts:
        - host: "pg.internal"
          port: 5432
      database: "myapp"
      auth:
        username: "postgres"
        password_file: "/etc/geryon/secrets/pg"
    limits:
      max_client_connections: 10000
      max_server_connections: 100

admin:
  rest:
    listen: "0.0.0.0:8080"
  dashboard:
    enabled: true
```

### Multi-Database — All Three Bodies

```yaml
pools:
  - name: "primary-pg"
    body: postgresql
    mode: transaction
    listen:
      host: "0.0.0.0"
      port: 5432
    backend:
      hosts:
        - host: "pg-primary.internal"
          port: 5432
          role: primary
        - host: "pg-replica.internal"
          port: 5432
          role: replica
      database: "myapp"
      auth:
        method: scram-sha-256
        username: "geryon_pool"
        password_file: "/etc/geryon/secrets/pg-password"
    limits:
      max_client_connections: 10000
      max_server_connections: 100

  - name: "analytics-mysql"
    body: mysql
    mode: session
    listen:
      host: "0.0.0.0"
      port: 3306
    backend:
      hosts:
        - host: "mysql-primary.internal"
          port: 3306
      database: "analytics"
      auth:
        method: caching_sha2_password
        username: "geryon_pool"
        password_file: "/etc/geryon/secrets/mysql-password"

  - name: "reporting-mssql"
    body: mssql
    mode: transaction
    listen:
      host: "0.0.0.0"
      port: 1433
    backend:
      hosts:
        - host: "mssql.internal"
          port: 1433
      database: "reporting"
      auth:
        username: "geryon_pool"
        password_file: "/etc/geryon/secrets/mssql-password"

routing:
  read_write_split: true

admin:
  rest:
    listen: "0.0.0.0:8080"
  dashboard:
    enabled: true
  mcp:
    transport: sse
    listen: "0.0.0.0:8081"
```

Connect your application to Geryon instead of directly to the database:

```bash
# PostgreSQL
psql -h localhost -p 5432 -U app -d myapp

# MySQL
mysql -h 127.0.0.1 -P 3306 -u app -p analytics

# MSSQL
sqlcmd -S localhost,1433 -U app -d reporting
```

## Architecture

```
                     ┌─────────────────────────────────────────────────┐
                     │                  GERYON PROXY                    │
                     │                                                 │
  Clients ────────►  │  ┌───────────┐  ┌──────────┐  ┌─────────────┐  │
  (PG/MySQL/TDS)     │  │  BODY I   │  │ BODY II  │  │  BODY III   │  │
                     │  │PostgreSQL │  │  MySQL   │  │    MSSQL    │  │
                     │  │  :5432    │  │  :3306   │  │    :1433    │  │
                     │  └─────┬─────┘  └────┬─────┘  └──────┬──────┘  │
                     │        │             │               │          │
                     │        ▼             ▼               ▼          │
                     │  ┌─────────────────────────────────────────────┐│
                     │  │           UNIFIED POOL MANAGER              ││
                     │  │   Session │ Transaction │ Statement         ││
                     │  │   Prepared Stmt Cache │ Query Result Cache  ││
                     │  └──────────────────┬──────────────────────────┘│
                     │                     │                           │
                     │  ┌──────────────────┴──────────────────────────┐│
                     │  │            BACKEND CONNECTORS               ││
                     │  │  R/W Split │ Health Check │ Failover        ││
                     │  └─────────────────────────────────────────────┘│
                     └─────────────────────────────────────────────────┘

  Cluster:  N1 (Leader) ◄─── Raft + SWIM ───► N2, N3 (Followers)
```

## Dashboard

Access the web dashboard at `http://localhost:8080` after starting Geryon:

| Page | Description |
|---|---|
| **Overview** | Total connections, queries/sec, cache hit rate, cluster health |
| **Pools** | Per-pool connection counts, wait queue, avg query time |
| **Backends** | Server status (up/down/degraded), latency, connection count |
| **Connections** | Live table: client IP, pool, state, duration, current query |
| **Query Stats** | Top queries by time/frequency, slow query log |
| **Cache** | Hit/miss rate graph, memory usage, top cached queries |
| **Cluster** | Node map, Raft state, leader indicator, gossip health |
| **Config** | Live editor with validation + hot-reload |

Built with vanilla HTML/CSS/JS — no npm, no bundler, embedded in the binary via `embed.FS`.

## CLI Reference

```bash
geryon                           # Start with geryon.yaml in current dir
geryon --config /path/to/config  # Start with specific config
geryon --validate                # Validate config without starting
geryon --version                 # Print version info
geryon --generate-config         # Output example config to stdout
geryon --generate-password       # Generate SCRAM-SHA-256 hash
geryon --generate-cert           # Generate self-signed TLS cert
```

## MCP Integration

Geryon includes a built-in [MCP](https://modelcontextprotocol.io) server for AI-assisted database management, compatible with Claude Code, Claude Desktop, and other MCP clients.

**Tools:** `geryon_pool_list`, `geryon_pool_stats`, `geryon_connection_list`, `geryon_connection_kill`, `geryon_backend_status`, `geryon_backend_detach`, `geryon_backend_attach`, `geryon_cache_stats`, `geryon_cache_invalidate`, `geryon_cluster_status`, `geryon_config_reload`, `geryon_query_stats`, `geryon_user_manage`

**Resources:** `geryon://config`, `geryon://pools/{name}`, `geryon://stats/overview`, `geryon://cluster/topology`

## Performance Targets

| Metric | Target |
|---|---|
| Max client connections | 100,000+ per node |
| Connection setup latency | < 1ms |
| Query proxy overhead | < 100μs |
| Memory per idle connection | < 8KB |
| Config reload | < 100ms, zero downtime |
| Binary size | < 30MB |
| Startup time | < 2s |

## Platform Support

| Platform | Status |
|---|---|
| Linux (amd64, arm64) | Primary |
| macOS (amd64, arm64) | Supported |
| Windows (amd64) | Supported |
| Container (GHCR) | `ghcr.io/geryonproxy/geryon` |

## Philosophy

**#NOFORKANYMORE** — Geryon is built with a strict zero-dependency philosophy:

- **Pure Go** — stdlib only, `go.sum` is empty
- **Single Binary** — one file, runs anywhere Go compiles
- **Zero CGo** — fully static, cross-compile to any platform
- **No Vendor** — no dependency management, no supply chain risk

## Contributing

```bash
git clone https://github.com/GeryonProxy/geryon.git
cd geryon
make build    # Build binary
make test     # Run tests
make lint     # Run go vet
```

## License

Apache 2.0 — See [LICENSE](LICENSE) for details.

## Author

Built by [ECOSTACK TECHNOLOGY OÜ](https://ecostack.dev)

---

*Geryon (Γηρυών) — the three-bodied giant, guardian of the red cattle of Erytheia. Defeating Geryon was the 10th of Heracles' twelve labors — one entity with three bodies, each a formidable force on its own.*
