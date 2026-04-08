# GERYON — CLAUDE CODE PROMPT

> Single-shot prompt for Claude Code to implement Geryon.

---

## IDENTITY

You are building **Geryon** — a high-performance, multi-database connection pooler and proxy. Named after the three-bodied giant of Greek mythology (Heracles' 10th labor), Geryon speaks PostgreSQL, MySQL, and MSSQL wire protocols from a single Go binary.

**Repository:** `github.com/GeryonProxy/geryon`
**Domain:** `geryonproxy.com`
**Tagline:** "Three Bodies. One Proxy. Every Connection."

## ABSOLUTE CONSTRAINTS

1. **ZERO EXTERNAL DEPENDENCIES** — stdlib only. No third-party packages. `go.sum` must be empty. This is non-negotiable.
2. **SINGLE BINARY** — `go build ./cmd/geryon` produces one static binary. No sidecar files needed at runtime (dashboard is embedded via `embed.FS`).
3. **NO CGo** — `CGO_ENABLED=0` always. Fully static, cross-compile friendly.
4. **Go 1.23+** — Use latest stdlib features (`log/slog`, `maps`, `slices`, etc.)
5. **ALL TESTS MUST PASS** — Run `go test -race ./...` after every significant change. Fix any failures before proceeding.

## WHAT YOU ARE BUILDING

### Three Bodies (Wire Protocols)

**Body I — PostgreSQL:**
- Wire protocol v3 (Frontend/Backend message format)
- Auth: SCRAM-SHA-256, MD5, trust, password, certificate
- Full Extended Query protocol (Parse, Bind, Describe, Execute, Sync, Close)
- COPY protocol passthrough
- LISTEN/NOTIFY passthrough
- Parameter status tracking

**Body II — MySQL:**
- MySQL Client/Server Protocol (handshake v10)
- Auth: mysql_native_password, caching_sha2_password, sha256_password
- COM_QUERY, COM_STMT_PREPARE/EXECUTE/CLOSE
- COM_CHANGE_USER, COM_RESET_CONNECTION
- Capability flags negotiation

**Body III — MSSQL:**
- TDS 7.4+ (Tabular Data Stream)
- Auth: SQL Server Authentication, NTLM passthrough
- SQL Batch, RPC Request (sp_executesql)
- sp_prepare/sp_execute/sp_unprepare
- Token stream parser (COLMETADATA, ROW, DONE, ERROR, ENVCHANGE, INFO, LOGINACK)

### Pooling Engine

Three modes, per-pool configurable:
- **Session** — dedicated server conn per client session
- **Transaction** — server assigned at BEGIN, released at COMMIT/ROLLBACK
- **Statement** — server assigned per statement, released after response

Features:
- Connection wait queue (FIFO with timeout)
- Idle connection eviction (max_idle_time)
- Connection lifetime rotation (max_connection_lifetime)
- Min/max server connections enforcement
- Backend health checking (periodic query)
- Connection state reset per protocol on release

### Prepared Statement Management
- Global metadata cache: `{client_stmt_name → (SQL, param_types)}`
- Per-server-conn tracking of prepared statements
- Transparent re-preparation when statement missing on assigned server
- Client→server stmt ID remapping (MySQL numeric IDs)
- LRU eviction per server connection

### Query Result Cache
- In-memory LRU with TTL and max memory
- Query normalization for cache keys
- Per-pattern TTL rules (YAML configurable)
- Write-triggered table-level invalidation
- Manual invalidation via API

### Auth & Security
- **Passthrough mode** — forward auth to backend
- **Interception mode** — Geryon manages users, maps to backend credentials
- TLS termination (disable/allow/prefer/require/verify-ca/verify-full)
- mTLS with client certificate CN/SAN → username mapping
- Per-user connection limits and pool ACLs

### Read/Write Splitting
- Lightweight SQL tokenizer (keyword detection, not full parser)
- SELECT → replica, writes → primary (configurable rules)
- Transaction-aware: all queries in explicit txn go to same server
- Weighted backend selection

### Management Interfaces

**REST API** (net/http):
- Full CRUD: pools, connections, backends, users, cache, cluster, config
- Health + readiness probes
- Bearer token auth middleware

**gRPC** (net/http HTTP/2, hand-rolled protobuf):
- GeryonAdmin service with all management RPCs
- StreamStats for real-time stats streaming
- No protoc dependency — varint/field encoding from scratch

**MCP Server** (SSE + stdio transport):
- Tools: geryon_pool_list, geryon_pool_stats, geryon_connection_list, geryon_connection_kill, geryon_backend_status, geryon_backend_detach, geryon_backend_attach, geryon_cache_stats, geryon_cache_invalidate, geryon_cluster_status, geryon_config_reload, geryon_query_stats, geryon_user_manage
- Resources: geryon://config, geryon://pools/{name}, geryon://stats/overview, geryon://cluster/topology

**Web Dashboard** (embed.FS, SSE):
- 9 pages: Overview, Pools, Backends, Connections, Query Stats, Cache, Cluster, Config, Users
- Dark theme, vanilla HTML/CSS/JS, no build step
- Real-time updates via SSE

### Clustering

**Raft Consensus** (from scratch):
- Config replication, user database sync
- Leader election, log replication, snapshotting
- TCP transport with TLS

**SWIM Gossip** (from scratch):
- Node discovery, failure detection
- Metadata piggybacking (load, connections, uptime)
- UDP transport

### Configuration
- YAML format (from-scratch parser, supports env var substitution `${VAR}`)
- Hot reload: file watcher (os.Stat polling) + SIGHUP + REST/MCP/gRPC endpoint
- Validation before apply

### Observability
- Built-in metrics (atomic counters + histograms)
- Structured JSON logging via `log/slog`
- Slow query log (configurable threshold)
- Connection lifecycle logging

## PROJECT STRUCTURE

Follow this exact structure:

```
geryon/
├── cmd/geryon/main.go
├── internal/
│   ├── config/        (config, loader, watcher, defaults)
│   ├── protocol/
│   │   ├── common/    (Message, Codec interface, Buffer)
│   │   ├── postgresql/ (codec, messages, auth, startup, extended, copy, notify, params)
│   │   ├── mysql/     (codec, messages, auth, handshake, command, capability)
│   │   └── mssql/     (codec, messages, auth, prelogin, batch, rpc, token)
│   ├── pool/          (manager, pool, session, transaction, statement, backend, health, reset, wait_queue, routing)
│   ├── proxy/         (listener, session, relay, interceptor, tls)
│   ├── auth/          (interceptor, passthrough, users, scram, md5, mysql_native, sha2, cert)
│   ├── cache/         (store, key, invalidation, rules)
│   ├── stmt/          (cache, tracker, remapper)
│   ├── cluster/
│   │   ├── raft/      (raft, log, state, transport, snapshot, election)
│   │   ├── gossip/    (swim, membership, detector, metadata)
│   │   └── cluster.go
│   ├── api/
│   │   ├── rest/      (server, pools, connections, backends, stats, cache, cluster, users, config, middleware)
│   │   ├── grpc/      (server, admin, proto/)
│   │   └── mcp/       (server, tools, resources)
│   ├── dashboard/     (handler, sse, static/)
│   ├── metrics/       (collector, counters, histogram, registry)
│   ├── tokenizer/     (tokenizer, classify, tables)
│   └── logger/        (logger, levels)
├── embed.go
├── go.mod
├── Makefile
├── Dockerfile
└── geryon.example.yaml
```

## IMPLEMENTATION ORDER

1. **Foundation** — main.go, config, logger, common protocol types
2. **Body I** — PostgreSQL wire protocol + basic proxy (single backend, no pooling)
3. **Pooling Engine** — Pool manager, all three strategies, wait queue, health checker
4. **Body II** — MySQL wire protocol integration
5. **Body III** — MSSQL/TDS wire protocol integration
6. **Prepared Statements & Cache** — stmt cache, result cache, invalidation
7. **Auth & Security** — interception mode, TLS/mTLS
8. **Read/Write Splitting** — tokenizer, routing rules
9. **REST API** — all endpoints
10. **gRPC API** — hand-rolled protobuf, GeryonAdmin service
11. **MCP Server** — tools + resources
12. **Dashboard** — all 9 pages, SSE streaming, embed
13. **Clustering** — Raft + SWIM + coordinator
14. **Polish** — example configs, Dockerfile, tests

## CODING STANDARDS

- All exported types and functions must have doc comments
- Error messages must include context: `fmt.Errorf("pool %s: connect backend %s: %w", poolName, host, err)`
- Use `context.Context` for all operations that may block or cancel
- Use `sync.Pool` for frequently allocated buffers
- Use `atomic` operations for hot-path counters (avoid mutex on metrics)
- Test files: `*_test.go` in same package
- Benchmark files: `*_bench_test.go` for hot-path operations
- No `init()` functions
- No global mutable state (inject dependencies)
- All goroutines must be cancelable via context
- All TCP connections must have read/write deadlines

## YAML PARSER NOTE

Since we cannot use `gopkg.in/yaml.v3`, implement a minimal YAML parser that supports:
- Key-value pairs (scalars)
- Nested maps (indentation-based)
- Sequences (- items)
- Quoted strings (single and double)
- Environment variable substitution (`${VAR}` and `${VAR:-default}`)
- Comments (#)
- Multi-line strings (| and >)

This does NOT need to be a full YAML 1.2 spec parser. Only support what Geryon's config needs.

## PROTOBUF NOTE

Since we cannot use `google.golang.org/protobuf`, implement minimal protobuf encoding:
- Varint encoding/decoding
- Length-delimited fields (strings, bytes, nested messages)
- Fixed32/Fixed64 for float/double
- Field tag + wire type encoding
- Only need message types used by GeryonAdmin service

Use Go's `net/http` HTTP/2 support for gRPC transport. Implement gRPC framing (1-byte compressed flag + 4-byte length + payload).

## PERFORMANCE CRITICAL PATHS

These must be optimized for minimal allocations:
1. **Message relay** (client ↔ server) — use `io.CopyBuffer`, avoid per-message alloc
2. **Pool assignment** — lock-free where possible, atomic operations for counters
3. **Transaction boundary detection** — single-pass byte scan, no string allocation
4. **Health check** — lightweight, don't hold pool locks during network I/O

## RUN AFTER COMPLETION

```bash
# Verify zero deps
test -z "$(cat go.sum)" && echo "PASS: zero deps" || echo "FAIL: has deps"

# Build
CGO_ENABLED=0 go build -o bin/geryon ./cmd/geryon

# Test
go test -race -cover ./...

# Verify binary
./bin/geryon --version
./bin/geryon --validate --config geryon.example.yaml
```
