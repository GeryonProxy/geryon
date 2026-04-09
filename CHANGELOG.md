# Changelog

All notable changes to Geryon will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased] - 2026-04-10

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

### Known Limitations
- MSSQL NTLM passthrough: Test implemented, full feature pending
- MSSQL sp_prepare/sp_execute: Test implemented, full feature pending
- PostgreSQL COPY protocol: Not implemented
- PostgreSQL LISTEN/NOTIFY: Not implemented

## [0.1.0] - 2026-04-10

### Initial Release
- First public release of Geryon
- Multi-database proxy support (PostgreSQL, MySQL, MSSQL)
- Connection pooling with three modes
- Web dashboard and management APIs
- Basic clustering support

---

## Roadmap

### Future Releases

#### v0.2.0 - Enhanced MSSQL
- Full NTLM passthrough for Windows Authentication
- sp_prepare/sp_execute/sp_unprepare support
- Bulk copy protocol (BCP)

#### v0.3.0 - PostgreSQL Features
- COPY protocol passthrough
- LISTEN/NOTIFY forwarding
- Logical replication support

#### v0.4.0 - Performance
- Query plan caching
- Adaptive pool sizing
- Connection prefetching

#### v1.0.0 - Production Ready
- Full production hardening
- Complete observability stack
- Enterprise support options
