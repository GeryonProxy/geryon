# Geryon

**Three Bodies. One Proxy. Every Connection.**

Geryon is a high-performance, multi-database connection pooler and proxy built in pure Go with zero external dependencies. Named after the three-bodied giant of Greek mythology, Geryon speaks PostgreSQL, MySQL, and MSSQL wire protocols — all from a single binary.

## Why Geryon?

Running PostgreSQL, MySQL, and MSSQL? Today you need PgBouncer + ProxySQL + nothing (MSSQL has no standalone pooler). Three tools, three configs, three monitoring setups, three failure modes.

Geryon replaces all of them with **one binary**.

## Features

### Three Bodies (Protocol Support)

- **Body I — PostgreSQL** — Wire protocol v3, SCRAM-SHA-256/MD5 auth, Extended Query protocol, COPY, LISTEN/NOTIFY
- **Body II — MySQL** — Handshake v10, mysql_native_password/caching_sha2, COM_QUERY, COM_STMT_*, capability negotiation
- **Body III — MSSQL** — TDS 7.4+, SQL Auth/NTLM passthrough, SQL Batch, RPC (sp_executesql), MARS awareness

### Pooling Modes

- **Session** — Dedicated server connection per client session
- **Transaction** — Server assigned per transaction, released on COMMIT/ROLLBACK
- **Statement** — Most aggressive: server assigned per individual statement

### Architecture

- **Prepared Statement Cache** — Transparent re-preparation across pooled connections
- **Query Result Cache** — In-memory LRU cache with TTL and write invalidation
- **Read/Write Splitting** — Route SELECTs to replicas, writes to primary
- **Auth Interception** — Geryon manages users, maps N clients to M backend credentials
- **TLS/mTLS** — Full TLS termination with mutual TLS client certificate support
- **Hot Reload** — YAML config changes applied without restart

### Management

- **REST API** — Full CRUD for pools, connections, backends, users, cache
- **MCP Server** — LLM-native management (Claude Code / Claude Desktop compatible)
- **gRPC API** — Programmatic integration with streaming stats
- **Web Dashboard** — Real-time monitoring, connection management, config editor

### Clustering

- **Raft Consensus** — Config replication across cluster nodes
- **SWIM Gossip** — Node discovery, failure detection, metadata dissemination
- **Zero-downtime** — Automatic leader election, backend health sharing

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

### Docker

```bash
docker run -d \
  --name geryon \
  -p 5432:5432 \
  -p 3306:3306 \
  -p 1433:1433 \
  -p 8080:8080 \
  -v ./geryon.yaml:/etc/geryon/geryon.yaml \
  geryonproxy/geryon:latest
```

### Docker Compose

```yaml
services:
  geryon:
    image: geryonproxy/geryon:latest
    ports:
      - "5432:5432"   # PostgreSQL
      - "3306:3306"   # MySQL
      - "1433:1433"   # MSSQL
      - "8080:8080"   # Dashboard + REST API
    volumes:
      - ./geryon.yaml:/etc/geryon/geryon.yaml
```

## Minimal Configuration

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

Connect your application to Geryon instead of directly to PostgreSQL:

```bash
psql -h localhost -p 5432 -U app -d myapp
```

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                    GERYON PROXY                      │
│                                                     │
│  ┌─────────────┐  ┌──────────┐  ┌───────────────┐  │
│  │   BODY I    │  │ BODY II  │  │   BODY III    │  │
│  │ PostgreSQL  │  │  MySQL   │  │    MSSQL      │  │
│  │  :5432      │  │  :3306   │  │    :1433      │  │
│  └──────┬──────┘  └────┬─────┘  └───────┬───────┘  │
│         │              │                │           │
│         ▼              ▼                ▼           │
│  ┌─────────────────────────────────────────────────┐│
│  │           UNIFIED POOL MANAGER                  ││
│  │   Session │ Transaction │ Statement Pooling     ││
│  └──────────────────┬──────────────────────────────┘│
│                     │                               │
│  ┌──────────────────┴──────────────────────────────┐│
│  │              BACKEND CONNECTORS                 ││
│  │  PG Servers │ MySQL Servers │ MSSQL Servers     ││
│  └─────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────┘
```

## Dashboard

Access the web dashboard at `http://localhost:8080` after starting Geryon. Features:

- Real-time connection monitoring
- Per-pool statistics and management
- Backend health status
- Query statistics and slow query log
- Cache performance metrics
- Cluster topology view
- Live config editor with hot-reload

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

Geryon includes a built-in MCP (Model Context Protocol) server for AI-assisted database management:

```yaml
admin:
  mcp:
    transport: sse
    listen: "0.0.0.0:8081"
```

Available tools: `geryon_pool_list`, `geryon_pool_stats`, `geryon_connection_list`, `geryon_connection_kill`, `geryon_backend_status`, `geryon_cache_stats`, `geryon_cluster_status`, and more.

## Performance

| Metric | Target |
|---|---|
| Max client connections | 100,000+ per node |
| Connection setup latency | < 1ms |
| Query proxy overhead | < 100μs |
| Memory per idle connection | < 8KB |
| Binary size | < 30MB |

## Philosophy

Geryon follows the **#NOFORKANYMORE** philosophy:

- **Pure Go** — stdlib only, zero external dependencies
- **Single Binary** — one file, runs anywhere
- **Zero CGo** — fully static, cross-compile friendly
- **No Vendor** — `go.sum` is empty

## Documentation

- [SPECIFICATION.md](./SPECIFICATION.md) — Complete technical specification
- [IMPLEMENTATION.md](./IMPLEMENTATION.md) — Implementation guide with code patterns
- [TASKS.md](./TASKS.md) — Phased task breakdown (172 tasks)
- [BRANDING.md](./BRANDING.md) — Visual identity and brand guidelines

## License

Apache 2.0 — See [LICENSE](./LICENSE) for details.

## Author

Built with ❤️ by [ECOSTACK TECHNOLOGY OÜ](https://ecostack.dev)

---

*Geryon (Γηρυών) — the three-bodied giant, guardian of the red cattle of Erytheia. In Greek mythology, defeating Geryon was the 10th of Heracles' twelve labors.*
