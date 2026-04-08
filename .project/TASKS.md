# GERYON — TASKS

> Phased implementation plan. Each task is atomic and testable.

## PHASE 1: FOUNDATION (Weeks 1-4)

### 1.1 Project Scaffolding
- [ ] T001: Initialize Go module (`github.com/GeryonProxy/geryon`), create directory structure per IMPLEMENTATION.md
- [ ] T002: Implement `cmd/geryon/main.go` — CLI flags (`--config`, `--validate`, `--version`, `--generate-config`), signal handling (SIGTERM, SIGHUP)
- [ ] T003: Implement structured JSON logger using `log/slog` with per-component log levels
- [ ] T004: Create Makefile with build, test, lint, docker, release targets

### 1.2 Configuration
- [ ] T005: Define config struct hierarchy (`GlobalConfig`, `PoolConfig`, `BackendConfig`, `AuthConfig`, `ClusterConfig`, `TLSConfig`, `CacheConfig`)
- [ ] T006: Implement YAML parser from scratch (support: maps, sequences, scalars, anchors, env var substitution `${VAR}`)
- [ ] T007: Implement config validation (port conflicts, pool name uniqueness, required fields, enum values)
- [ ] T008: Implement `--generate-config` to emit `geryon.example.yaml` with comments
- [ ] T009: Implement config file watcher (os.Stat polling, configurable interval) + SIGHUP reload trigger
- [ ] T010: Implement hot reload logic — diff old/new config, apply changes, validate before swap

### 1.3 Common Protocol Layer
- [ ] T011: Define `common.Message` struct, `Codec` interface, `Direction` type, `Protocol` enum
- [ ] T012: Implement `common.Buffer` — read/write helpers for int16, int32, int64, string, bytes, null-terminated string
- [ ] T013: Implement TCP listener with TLS/mTLS support (`crypto/tls` config builder from YAML)

## PHASE 2: BODY I — POSTGRESQL (Weeks 5-8)

### 2.1 PG Wire Protocol
- [ ] T014: Implement PG startup message parsing (version, parameters map)
- [ ] T015: Implement PG SSL negotiation (SSLRequest message detection, TLS upgrade)
- [ ] T016: Implement PG message codec — read/write for all P0 message types (Query, RowDescription, DataRow, CommandComplete, ReadyForQuery, ErrorResponse, Terminate)
- [ ] T017: Implement PG Extended Query protocol messages (Parse, Bind, Describe, Execute, Sync, Close, ParseComplete, BindComplete, NoData)
- [ ] T018: Implement PG auth — SCRAM-SHA-256 full handshake (client-first, server-first, client-final, server-final)
- [ ] T019: Implement PG auth — MD5 password hashing
- [ ] T020: Implement PG parameter status tracking (server_version, client_encoding, etc.)
- [ ] T021: Implement PG COPY protocol passthrough (CopyInResponse, CopyOutResponse, CopyData, CopyDone, CopyFail)
- [ ] T022: Implement PG LISTEN/NOTIFY passthrough (NotificationResponse forwarding)
- [ ] T023: Implement PG BackendKeyData handling (cancel key forwarding)

### 2.2 PG Proxy Integration
- [ ] T024: Build end-to-end PG proxy — accept client, auth, forward to single backend, relay messages
- [ ] T025: Integration test: connect via `psql`, run queries through Geryon, verify results
- [ ] T026: Benchmark: measure proxy overhead per query (target: <100μs)

## PHASE 3: POOLING ENGINE (Weeks 9-12)

### 3.1 Pool Core
- [ ] T027: Implement `ServerConn` — wrapper around backend connection with metadata (createdAt, lastUsedAt, preparedStmts, paramStatus)
- [ ] T028: Implement `serverConnPool` — idle list, active map, min/max enforcement, LRU idle eviction
- [ ] T029: Implement backend connector — establish new server connections with auth, apply initial state
- [ ] T030: Implement `WaitQueue` — FIFO wait queue with timeout, context cancellation, metrics
- [ ] T031: Implement `Pool` — orchestrates serverConnPool + WaitQueue + metrics

### 3.2 Pool Strategies
- [ ] T032: Implement `SessionStrategy` — assign server conn on client connect, release on disconnect
- [ ] T033: Implement `TransactionStrategy` — assign on BEGIN/first query, release on COMMIT/ROLLBACK, detect auto-commit
- [ ] T034: Implement `StatementStrategy` — assign per statement, release after response complete
- [ ] T035: Implement transaction boundary detection for PG (BEGIN, COMMIT, ROLLBACK, ReadyForQuery status byte)

### 3.3 Connection Reset
- [ ] T036: Implement PG connection reset — `DISCARD ALL` or selective reset, track dirty state
- [ ] T037: Implement smart reset — only reset what was actually modified (avoid unnecessary round-trips)

### 3.4 Pool Manager
- [ ] T038: Implement `PoolManager` — creates/manages multiple pools from config, handles lifecycle
- [ ] T039: Implement pool health checker — periodic SELECT 1 (configurable query), mark backend up/down
- [ ] T040: Implement connection limits enforcement — max_client_connections, max_server_connections per pool
- [ ] T041: Implement idle connection timeout — evict connections exceeding max_idle_time
- [ ] T042: Implement max connection lifetime — rotate connections exceeding max_connection_lifetime

### 3.5 Integration Testing
- [ ] T043: Test session pooling — client gets dedicated conn, verify session state preserved
- [ ] T044: Test transaction pooling — verify conn release on COMMIT, re-acquire on next txn
- [ ] T045: Test statement pooling — verify conn release after each statement
- [ ] T046: Test wait queue — max connections reached, client waits, gets conn when one frees
- [ ] T047: Stress test — 1000 concurrent clients, transaction pooling, verify no connection leaks

## PHASE 4: BODY II — MYSQL (Weeks 13-16)

### 4.1 MySQL Wire Protocol
- [ ] T048: Implement MySQL packet codec — 3-byte length + 1-byte sequence + payload
- [ ] T049: Implement MySQL handshake v10 — server greeting, capability negotiation
- [ ] T050: Implement MySQL auth — mysql_native_password (SHA1-based challenge-response)
- [ ] T051: Implement MySQL auth — caching_sha2_password (SHA256 + RSA)
- [ ] T052: Implement MySQL COM_QUERY handling — text protocol query + result set
- [ ] T053: Implement MySQL COM_STMT_PREPARE / COM_STMT_EXECUTE / COM_STMT_CLOSE — binary protocol
- [ ] T054: Implement MySQL COM_CHANGE_USER for connection reset
- [ ] T055: Implement MySQL COM_RESET_CONNECTION for connection reset (5.7.3+)
- [ ] T056: Implement MySQL capability flags negotiation between client and pooled server
- [ ] T057: Implement MySQL SSL handshake (SSL_REQUEST packet, TLS upgrade)

### 4.2 MySQL Pool Integration
- [ ] T058: Wire MySQL codec into Pool, implement MySQL-specific transaction detection (BEGIN, COMMIT, ROLLBACK, autocommit)
- [ ] T059: Implement MySQL connection state reset strategy (COM_RESET_CONNECTION → COM_CHANGE_USER fallback)
- [ ] T060: Integration test: connect via `mysql` CLI, run queries through Geryon
- [ ] T061: Test all three pooling modes with MySQL backend

## PHASE 5: BODY III — MSSQL (Weeks 17-20)

### 5.1 TDS Protocol
- [ ] T062: Implement TDS packet codec — 8-byte header (type, status, length, SPID, packet#, window)
- [ ] T063: Implement TDS Pre-Login handshake — version negotiation, encryption negotiation
- [ ] T064: Implement TDS Login7 message — SQL Server Authentication
- [ ] T065: Implement TDS NTLM passthrough for Windows Authentication
- [ ] T066: Implement TDS SQL Batch handling — send SQL text, parse token stream response
- [ ] T067: Implement TDS token stream parser — COLMETADATA, ROW, DONE, ERROR, ENVCHANGE, INFO, LOGINACK
- [ ] T068: Implement TDS RPC Request — sp_executesql for parameterized queries
- [ ] T069: Implement TDS sp_prepare / sp_execute / sp_unprepare for prepared statements
- [ ] T070: Implement TDS sp_reset_connection for connection state reset
- [ ] T071: Implement TDS encryption negotiation + TLS upgrade

### 5.2 MSSQL Pool Integration
- [ ] T072: Wire MSSQL codec into Pool, implement MSSQL-specific transaction detection (BEGIN TRAN, COMMIT, ROLLBACK)
- [ ] T073: Integration test: connect via `sqlcmd` or Go driver, run queries through Geryon
- [ ] T074: Test all three pooling modes with MSSQL backend

## PHASE 6: PREPARED STATEMENTS & CACHE (Weeks 21-24)

### 6.1 Prepared Statement Management
- [ ] T075: Implement `stmt.Cache` — global metadata cache: {client_stmt_name → (SQL, param_types)}
- [ ] T076: Implement `stmt.Tracker` — per-server-conn tracking of which statements are prepared
- [ ] T077: Implement `stmt.Remapper` — client→server stmt ID remapping (MySQL numeric IDs, PG named stmts)
- [ ] T078: Implement transparent re-preparation — detect stmt not on assigned server, re-prepare before execute
- [ ] T079: Implement LRU eviction for server-side prepared statements (configurable max per conn)
- [ ] T080: Test prepared statements across transaction pooling — prepare on server A, execute on server B

### 6.2 Query Result Cache
- [ ] T081: Implement `cache.Store` — LRU cache with TTL, max memory enforcement, atomic operations
- [ ] T082: Implement `cache.Key` — query normalization (strip whitespace, lowercase keywords, parameter placeholder normalization)
- [ ] T083: Implement cache rules engine — per-pattern TTL matching, never-cache rules
- [ ] T084: Implement write invalidation — parse write queries, extract table names, invalidate matching cache entries
- [ ] T085: Implement manual cache invalidation API
- [ ] T086: Test cache hit/miss, TTL expiry, write invalidation, memory limit eviction
- [ ] T087: Benchmark cache performance — hit latency, miss latency, memory overhead

## PHASE 7: AUTH & SECURITY (Weeks 25-27)

### 7.1 Auth Interception
- [ ] T088: Implement user database — in-memory store with SCRAM-SHA-256 password hashes
- [ ] T089: Implement auth interceptor — client authenticates vs Geryon, Geryon uses pool credentials for backend
- [ ] T090: Implement auth passthrough — transparent forwarding of auth messages
- [ ] T091: Implement per-user connection limits and pool access control (allowed_pools)
- [ ] T092: Implement `--generate-password` CLI command (input password → output SCRAM hash)

### 7.2 TLS & mTLS
- [ ] T093: Implement TLS termination — configurable modes (disable/allow/prefer/require/verify-ca/verify-full)
- [ ] T094: Implement mTLS — client certificate validation, CN/SAN→username mapping
- [ ] T095: Implement per-pool TLS policy (some pools require mTLS, others allow password)
- [ ] T096: Implement `--generate-cert` CLI command (self-signed cert for testing)
- [ ] T097: Test: psql with sslmode=verify-full through Geryon

## PHASE 8: READ/WRITE SPLITTING & ROUTING (Week 28)

- [ ] T098: Implement SQL tokenizer — lightweight keyword detection (SELECT, INSERT, UPDATE, DELETE, BEGIN, COMMIT, etc.)
- [ ] T099: Implement table name extractor from tokenized query
- [ ] T100: Implement read/write routing rules engine (YAML-configurable)
- [ ] T101: Implement transaction-aware routing — all queries in explicit txn go to same backend
- [ ] T102: Implement primary/replica backend role assignment and weighted selection
- [ ] T103: Test: SELECT queries route to replica, writes to primary, verify correctness

## PHASE 9: MANAGEMENT INTERFACES (Weeks 29-33)

### 9.1 REST API
- [ ] T104: Implement HTTP router from stdlib `net/http` — path matching, method routing, middleware chain
- [ ] T105: Implement auth middleware — Bearer token validation
- [ ] T106: Implement pool endpoints (list, get, update, pause, resume)
- [ ] T107: Implement connection endpoints (list, kill)
- [ ] T108: Implement backend endpoints (list, detach, attach)
- [ ] T109: Implement stats endpoints (global, per-pool, query stats)
- [ ] T110: Implement cache endpoints (stats, invalidate)
- [ ] T111: Implement cluster endpoints (status, nodes)
- [ ] T112: Implement user management endpoints (CRUD)
- [ ] T113: Implement config endpoints (reload, validate)
- [ ] T114: Implement health + readiness probe endpoints

### 9.2 gRPC API
- [ ] T115: Implement hand-rolled protobuf serializer — varint, field tags, wire types for all admin messages
- [ ] T116: Implement minimal gRPC server over HTTP/2 (`net/http` native h2)
- [ ] T117: Implement GeryonAdmin service — all RPC methods mapped to pool/conn/backend/stats/cache/cluster handlers
- [ ] T118: Implement `StreamStats` — server-side streaming of stats snapshots

### 9.3 MCP Server
- [ ] T119: Implement MCP server with SSE transport (+ stdio for CLI integration)
- [ ] T120: Implement all MCP tools (geryon_pool_list, geryon_pool_stats, geryon_connection_list, geryon_connection_kill, geryon_backend_status, geryon_backend_detach, geryon_backend_attach, geryon_cache_stats, geryon_cache_invalidate, geryon_cluster_status, geryon_config_reload, geryon_query_stats, geryon_user_manage)
- [ ] T121: Implement MCP resources (geryon://config, geryon://pools/{name}, geryon://stats/overview, geryon://cluster/topology)
- [ ] T122: Test MCP tools with Claude Code / Claude Desktop

### 9.4 Web Dashboard
- [ ] T123: Design dashboard HTML/CSS — dark theme, responsive layout, 9 pages
- [ ] T124: Implement Overview page — global stats cards, connection graphs, cluster health indicator
- [ ] T125: Implement Pools page — pool list with mode badges, connection bar charts, drill-down
- [ ] T126: Implement Backends page — backend table with status indicators, latency sparklines
- [ ] T127: Implement Connections page — live connection table with filtering, kill button
- [ ] T128: Implement Query Stats page — top queries table, slow query log
- [ ] T129: Implement Cache page — hit/miss rate chart, memory gauge, top cached queries
- [ ] T130: Implement Cluster page — node topology view, Raft state, leader badge
- [ ] T131: Implement Config page — YAML editor with syntax highlighting, validate + reload buttons
- [ ] T132: Implement Users page — user table, create/edit modal, permission checkboxes
- [ ] T133: Implement SSE real-time stats streaming to dashboard
- [ ] T134: Embed all static files using `embed.FS`, serve from binary

## PHASE 10: METRICS & OBSERVABILITY (Weeks 34-35)

- [ ] T135: Implement atomic counters (pool connections, queries, errors, cache hits/misses)
- [ ] T136: Implement histogram — query duration distribution with configurable buckets
- [ ] T137: Implement metrics registry — collect all metrics, expose via REST `/api/v1/stats`
- [ ] T138: Implement per-pool metrics aggregation
- [ ] T139: Implement slow query log — configurable threshold, structured JSON output
- [ ] T140: Implement connection lifecycle logging (connect, auth, pool assign, release, disconnect)
- [ ] T141: Implement query stats collector — top N queries by time, frequency, error rate

## PHASE 11: CLUSTERING (Weeks 36-40)

### 11.1 Raft Consensus
- [ ] T142: Implement Raft log — append-only WAL with fsync, entry serialization
- [ ] T143: Implement Raft leader election — RequestVote RPC, randomized election timeout
- [ ] T144: Implement Raft log replication — AppendEntries RPC, heartbeat, commit index advancement
- [ ] T145: Implement Raft TCP transport — connection pool, message framing, TLS
- [ ] T146: Implement GeryonFSM — apply pool config changes, user CRUD, cache invalidation
- [ ] T147: Implement Raft snapshotting — compact log, restore from snapshot
- [ ] T148: Test: 3-node cluster, leader election, config change replication

### 11.2 Gossip Protocol (SWIM)
- [ ] T149: Implement SWIM protocol — ping, ping-req, join, leave
- [ ] T150: Implement membership list — alive, suspect, dead states, incarnation numbers
- [ ] T151: Implement suspicion mechanism — configurable timeout before declaring dead
- [ ] T152: Implement metadata piggybacking — node load, connection count, uptime dissemination
- [ ] T153: Implement UDP transport for SWIM messages
- [ ] T154: Test: 3-node discovery, failure detection, rejoin after recovery

### 11.3 Cluster Coordinator
- [ ] T155: Implement Cluster coordinator — wire Raft + SWIM together, expose unified cluster API
- [ ] T156: Implement cluster-aware config reload — changes via Raft, all nodes apply simultaneously
- [ ] T157: Implement cross-node backend health sharing — avoid thundering herd on failover
- [ ] T158: Integration test: 3-node cluster, kill leader, verify automatic failover + config consistency

## PHASE 12: POLISH & RELEASE (Weeks 41-44)

### 12.1 Documentation
- [ ] T159: Write comprehensive README.md with quick start, architecture diagram, config reference
- [ ] T160: Write PROMPT.md for Claude Code development
- [ ] T161: Create example configs for common scenarios (single PG, multi-DB, clustered)
- [ ] T162: Create Docker Compose examples (Geryon + PG + MySQL + MSSQL)

### 12.2 Testing & Hardening
- [ ] T163: Full integration test suite — all 3 protocols × all 3 pool modes × auth modes
- [ ] T164: Chaos testing — random backend failures, network partitions, connection storms
- [ ] T165: Memory leak testing — long-running load test, verify stable memory
- [ ] T166: Benchmark suite — publish performance numbers

### 12.3 Release
- [ ] T167: Set up GitHub Actions — CI/CD, test matrix (Linux/macOS/Windows)
- [ ] T168: Build release binaries for all platforms
- [ ] T169: Create Docker images and push to Docker Hub (geryonproxy/geryon)
- [ ] T170: Create Homebrew formula
- [ ] T171: Create GitHub release with changelog
- [ ] T172: Launch geryonproxy.com landing page

---

**Total: 172 tasks across 12 phases, ~44 weeks estimated timeline.**
