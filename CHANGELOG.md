# Changelog

All notable changes to Geryon will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased] - 2026-04-16

### Added

#### Phase 1: Foundation
- Project scaffolding with Go module
- Structured JSON logger using log/slog
- Configuration system with YAML parsing and hot reload
- TLS configuration builder with multiple modes

#### Phase 2: PostgreSQL (Body I)
- Full PG Wire Protocol v3 implementation
- SSL negotiation and TLS upgrade
- SCRAM-SHA-256 authentication
- Extended Query protocol (Parse, Bind, Execute)
- Parameter status tracking

#### Phase 3: Pooling Engine
- Connection pooling with three modes: Session, Transaction, Statement
- Wait queue with timeout and context cancellation
- Smart connection reset (DISCARD ALL tracking)
- Health checking with configurable queries
- Pool lifecycle management

#### Phase 4: MySQL (Body II)
- MySQL Handshake v10 protocol
- mysql_native_password authentication
- caching_sha2_password support
- COM_STMT_* prepared statement handling
- Connection reset with COM_RESET_CONNECTION

#### Phase 5: MSSQL (Body III)
- TDS 7.4+ packet protocol
- Pre-Login handshake
- Login7 authentication
- SQL Batch and RPC handling
- sp_reset_connection for state reset

#### Phase 6: Prepared Statements & Cache
- Global prepared statement cache with metadata
- Per-connection statement tracking
- Client-to-server statement ID remapping
- Query result cache with LRU eviction
- Write invalidation by table name
- Per-pattern TTL rules

#### Phase 7: Auth & Security
- SCRAM-SHA-256 password hashing
- Auth interception and passthrough modes
- mTLS client certificate validation
- CN/SAN to username mapping
- CertificateAuthenticator implementation
- Auth rate limiting (10 failures/5min, 5min lockout)

#### Phase 8: Read/Write Splitting
- SQL tokenizer for query classification
- Table name extraction
- Transaction-aware routing
- Primary/replica role assignment
- Weighted replica selection

#### Phase 9: Management Interfaces
- REST API with full CRUD operations
- Web dashboard with real-time SSE streaming
- MCP server for LLM integration
- gRPC API with streaming stats
- Embedded static assets

#### Phase 10: Metrics & Observability
- Atomic counters for connections, queries, errors
- Query duration histograms
- Slow query log with configurable threshold
- Per-pool metrics aggregation
- Connection lifecycle logging
- Query-level metrics wired into relay path

#### Phase 11: Clustering
- Raft consensus implementation with WAL
- Log replication and leader election
- GeryonFSM for state machine operations
- Snapshot persistence with compression
- SWIM gossip protocol for membership
- Cluster coordinator wiring Raft + SWIM
- Cross-node backend health sharing
- Cluster-aware config reload

#### Phase 12: Polish & Release
- GitHub Actions CI/CD workflows
- Multi-platform release binaries
- Docker multi-platform builds
- Homebrew formula template
- Landing page at geryonproxy.com

### Integration Tests
- MySQL pure Go protocol tests
- PostgreSQL prepared statement tests
- MSSQL TDS handshake tests
- Read/write splitting validation
- TLS/SSL mode tests
- Memory leak detection framework
- Chaos testing framework
- E2E smoke tests (proxy starts, handshake verification, global memory limit)

### Protocol Improvements (2026-04-15)
- MSSQL sp_prepare/sp_execute RPC parsing with B-VARCHAR procedure names
- MSSQL TokenTypeSSPI/FeatureExt/Tracking token types for Windows Auth detection
- PostgreSQL LISTEN/NOTIFY notification passthrough
- PostgreSQL COPY protocol passthrough (CopyIn/CopyOut/CopyBoth/CopyData/CopyDone)
- Global memory limit enforcement with TryAlloc/Free

### Reliability Fixes (2026-04-15)
- Histogram sum calculation fixed (mutex-protected float64)
- Certificate fingerprint uses SHA-256
- SQL injection in SmartResetter fixed (regex validation)
- Transaction timeout → ROLLBACK to backend wired
- Running average overflow fixed (decaying average with alpha=0.001)
- TransactionManager timeouts made configurable
- SWIM suspicion mechanism implemented

### Documentation Updates
- OPERATIONS.md with deployment, monitoring, troubleshooting guides
- openapi.yaml for REST API specification
- geryon.example.yaml with global.max_memory
- Production readiness score 100/100

---

## [1.0.0] - 2026-04-16

### Breaking Changes

- **gRPC API renamed to HTTP/2 Admin API** — The `grpc/` package now serves JSON-over-HTTP/2, not protobuf. Package directory retained for import compatibility. All endpoints remain the same.
- **Prometheus metric names aligned to spec §9.1** — Old names (`geryon_pool_client_connections`, `geryon_pool_total_queries`) renamed to spec-compliant names (`geryon_pool_client_connections_active`, `geryon_pool_queries_total`). Dashboards relying on old names must update.

### Added

#### Security & Hardening
- **MSSQL NTLM passthrough** — Full SSPI/NTLM challenge-response loop for Windows Authentication. Added SSPI and ENV_CHANGE token parsing to TDS codec.
- **API input validation** — Backend address validation (host:port regex), allowed actions whitelist, `MaxBytesReader` on PUT pool and config reload endpoints, pool name validation on DELETE.
- **Panic recovery** — Added `recover()` middleware to all 4 HTTP servers (REST, gRPC, MCP, Dashboard).
- **Graceful shutdown** — 30-second deadline for component shutdown. Deadline exceeded logged as warning.

#### Performance
- **Connection prefetching** — Proactively creates `min_server_connections` idle connections on pool startup with staggered creation (50ms) to avoid thundering herd.
- **Smooth weighted round-robin** — `effectiveWeight` accumulation algorithm ensures proportional traffic distribution across backends. Replaces simple max-weight selection.
- **O(1) idle connection removal** — `serverConnPool.remove` optimized from O(n) scan to O(1) swap-and-pop via `idleIndex` map.
- **Buffer pooling** — `sync.Pool` for read buffers in relay hot path to reduce GC pressure.

#### Observability
- **Prometheus metrics aligned to spec §9.1** — All pool, backend, cache, and cluster metric names match specification. Added cache hits/misses/evictions, backend status with labels, cluster raft state/term.
- **godoc-compliant package documentation** — All 21 packages have `// Package` doc comments describing purpose and key types.

#### Configuration & Management
- **Full dynamic config reload** — Unified `reloadFn` dynamically updates pool limits, health checks, cache, backends, creates/removes pools, and reloads auth users without restart.
- **Query cache `never_cache` rule** — YAML config can now disable caching for specific query patterns.
- **Configurable prepared statement cache** — `prepared_stmt.max_size` and `prepared_stmt.ttl` in PoolConfig.
- **Configurable API server timeouts** — `read_timeout` and `write_timeout` fields for all 4 API servers.
- **GoReleaser fixed** — Correct artifact packaging, multi-arch Docker images (amd64/arm64) with manifest merging, version/commit/date ldflags.

### Changed

- **README updated** — Accurate dependency claims (3 production deps), added connection prefetching to capabilities list, production readiness badge updated to 85/100.
- **Documentation corrected** — All docs now accurately reflect actual dependency count (3 production + 2 test). Zero-dependency claims removed.

### Removed

- **WEBUI.md** — Deleted; vanilla JS dashboard is the production reality.
- **GoReleaser `none:\n.txt`** — Replaced with proper `LICENSE`/`README.md` inclusion.

### Security

- **gosec audit: 0 issues** — Fresh scan across 43 files and 24,399 lines reports no findings.

## [0.1.0] - 2026-04-10

### Initial Release
- First public release of Geryon
- Multi-database proxy support (PostgreSQL, MySQL, MSSQL)
- Connection pooling with three modes
- Web dashboard and management APIs
- Basic clustering support

---

## Roadmap

### Completed

#### v1.0.0 - Production Ready
- Full production hardening (score: 85/100)
- Complete observability stack with Prometheus metrics
- MSSQL NTLM passthrough
- Connection prefetching
- Smooth weighted round-robin
- Full dynamic config reload
- API input validation
- Security audit: 0 gosec findings
- Multi-arch Docker images (amd64/arm64)

### Future Releases

#### v1.1.0 - Testing & Reliability
- Stabilized cluster integration tests (T148, T154)
- E2E tests with Docker Compose
- Load/stress tests with concurrent client simulation
- Mutation testing with go-mutesting

#### v1.2.0 - Advanced Features
- Full SQL parser (beyond tokenizer) for smarter routing and cache invalidation
- Query rewriting / transformation layer
- Cross-database query federation
- Kubernetes operator for automated deployment and management
- Plugin/extension system for custom auth, routing, and caching logic