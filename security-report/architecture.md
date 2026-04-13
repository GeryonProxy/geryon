# GeryonProxy Architecture Security Report

**Project:** GeryonProxy - Multi-Database Connection Pooler
**Language:** Go (100% of codebase)
**Go Version:** 1.26.1
**Dependencies:** stdlib only (zero external runtime dependencies)
**Total Go Files:** 108

---

## 1. Technology Stack Detection

### Primary Language
- **Go** is the sole language used in this project (100% of 108 .go files)

### Framework Detection
GeryonProxy uses **no external HTTP frameworks**. All APIs are built with `net/http` from the Go standard library.

### External Dependencies (go.mod)
```
module github.com/GeryonProxy/geryon
go 1.26.1
require (
    golang.org/x/term v0.36.0
    golang.org/x/time v0.15.0
)
require golang.org/x/sys v0.37.0 // indirect
```

---

## 2. Application Type Classification

GeryonProxy is a **database proxy/pooler** that operates as both a CLI tool and a network service.

### Network Ports
| Port | Protocol | Purpose |
|------|----------|---------|
| 5432 | PostgreSQL | Database proxy |
| 3306 | MySQL | Database proxy |
| 1433 | MSSQL | Database proxy |
| 8080 | HTTP | REST API admin |
| 9090 | HTTP/2 | gRPC API admin |
| 8081 | HTTP | MCP Server admin |
| 8082 | HTTP | Web Dashboard admin |

---

## 3. Entry Points Mapping

### CLI Entry Point: cmd/geryon/main.go
- Lines 36-393

### TCP Listeners: internal/proxy/listener.go
- Lines 33-54, 191-227

### HTTP API Servers
- REST: internal/api/rest/server.go
- gRPC: internal/api/grpc/server.go
- MCP: internal/api/mcp/server.go
- Dashboard: internal/api/dashboard/server.go

---

## 4. Data Flow Map

Client -> TCP Listener -> ProxySession -> handleStartup() -> connectToBackend() -> Pool.Acquire() -> Backend connection -> Relay.Run()

---

## 5. Trust Boundaries

### Authentication
- UserDatabase: internal/auth/auth.go (lines 36-113)
- SCRAMServer: internal/auth/auth.go (lines 115-277)
- AuthLimiter: internal/auth/auth.go (lines 472-585)
- CertAuthenticator: internal/auth/cert.go (lines 38-92)

### Input Validation
- Startup message parsing: proxy/listener.go lines 496-604
- Username validation: proxy/listener.go lines 597-604
- Pool name regex: internal/api/rest/server.go line 365

### Rate Limiting
- REST API: internal/api/rest/server.go lines 251-337
- gRPC: internal/api/grpc/server.go lines 191-276
- MCP: internal/api/mcp/server.go lines 154-234
- Dashboard: internal/api/dashboard/server.go lines 163-243
- Auth: internal/auth/auth.go lines 472-585

---

## 6. External Integrations

### Database Backend Connections
- Pool: internal/pool/pool.go (lines 417-441)
- ServerConn: internal/pool/pool.go (lines 104-177)
- Manager: internal/pool/manager.go (lines 15-20)

### Backend TLS Configuration
- internal/pool/pool.go lines 713-752

### Metrics Export
- internal/metrics/metrics.go
- Prometheus format via GET /metrics

---

## 7. Authentication Architecture

### PostgreSQL SCRAM-SHA-256
- File: internal/auth/scram.go
- Hash format: SCRAM-SHA-256\$<iterations>:<salt>:<storedkey>:<serverkey>

### MySQL Caching SHA-2
- Passthrough to backend

### MSSQL TDS Authentication
- Passthrough to backend

### Client Certificate Authentication
- File: internal/auth/cert.go
- Modes: CertAuthDisabled, CertAuthCN, CertAuthSAN, CertAuthEither

---

## 8. File Structure Analysis

Key directories:
- cmd/geryon/ - Main entry point
- internal/auth/ - Authentication logic
- internal/pool/ - Connection pooling
- internal/proxy/ - Client listeners
- internal/api/ - Management APIs
- internal/protocol/ - Database wire protocols
- internal/tlsutil/ - TLS utilities
- internal/logger/ - Logging
- internal/metrics/ - Prometheus metrics
- internal/config/ - Configuration

---

## 9. Detected Security Controls

### TLS Support
- Server TLS: internal/tlsutil/tls.go lines 13-59
- Client TLS: internal/tlsutil/tls.go lines 62-107
- Min version: TLS 1.2

### Connection State Reset
- PostgreSQL DISCARD ALL: internal/pool/reset.go lines 21-57
- MySQL COM_RESET_CONNECTION: internal/pool/reset.go lines 59-89
- MSSQL sp_reset_connection: internal/pool/reset.go lines 91-127

### Security Headers
- X-Content-Type-Options: nosniff
- X-Frame-Options: DENY
- X-XSS-Protection: 1; mode=block

### Error Message Sanitization
- internal/api/rest/server.go lines 347-362

---

## 10. Detected Languages Summary

| Language | Files | Percentage |
|----------|-------|------------|
| Go | 108 | 100% |

---

## Appendix: Key Code References

### Authentication
- proxy/listener.go:478-793
- auth/auth.go:115-277
- auth/cert.go:59-92

### Input Parsing
- proxy/listener.go:496-604
- protocol/common/message.go:269-394
- config/loader.go:37-80

### Connection Handling
- proxy/listener.go:1625-1678
- pool/pool.go:754-804
- pool/reset.go:29-127

### TLS Configuration
- tlsutil/tls.go:13-59
- tlsutil/tls.go:62-107
- pool/pool.go:713-752

### Rate Limiting
- auth/auth.go:472-585
- api/rest/server.go:251-337
- api/grpc/server.go:191-276
- api/mcp/server.go:154-234

---

*Document generated for security vulnerability scanning purposes.*
*Last updated: 2026-04-13*


## Detailed Architecture Analysis

### Protocol Codec Interface
File: internal/protocol/common/message.go (lines 35-82)

The Codec interface defines the contract for all database protocol implementations:
- ReadMessage(), WriteMessage() for message I/O
- IsStartup(), IsTerminate(), IsQuery() for message type detection
- IsTransactionBegin(), IsTransactionEnd() for transaction boundaries
- ExtractQuery() for SQL extraction
- GenerateResetSequence() for connection pool reset

### PostgreSQL Frontend Handler
File: internal/protocol/postgresql/codec.go

Handles PostgreSQL wire protocol messages including:
- StartupMessage (handshake)
- SASL/SCRAM authentication
- SimpleQuery and ExtendedQuery protocols
- Parse, Bind, Execute, Close for prepared statements
- Function call protocol

### MySQL Frontend Handler  
File: internal/protocol/mysql/codec.go

Handles MySQL wire protocol messages including:
- HandshakeV10 (initial handshake)
- Client handshake response
- COM_QUERY, COM_STMT_PREPARE, COM_STMT_EXECUTE
- Binary protocol for prepared statements

### MSSQL Frontend Handler
File: internal/protocol/mssql/codec.go

Handles TDS (Tabular Data Stream) protocol including:
- Pre-Login negotiation
- Login7 authentication
- SQLBatch and RPC requests
- Attention signals (cancel, disconnect)

### Pool Modes
File: internal/pool/pool.go (lines 22-57)

Three pooling strategies:
- ModeSession: Connections held for entire client session
- ModeTransaction: Connections released after each transaction
- ModeStatement: Connections released after each statement

### Backend Selection Strategies
File: internal/pool/strategy.go

- Weighted round-robin for load balancing
- Read/write splitting based on query type
- Primary fallback when replicas unavailable

### Health Checking
File: internal/pool/health.go

Backend health checks with configurable:
- CheckInterval: How often to check backends
- CheckQuery: SQL query to execute
- MaxFailures: Failures before marking unhealthy

### Query Cache
File: internal/cache/store.go

LRU cache for query results with:
- Table-based invalidation
- TTL support
- Memory limits

### Cluster Coordination
File: internal/cluster/cluster.go

SWIM-based membership and failure detection
Raft consensus for distributed state

### Configuration Hot Reload
File: internal/config/watcher.go

Monitors config file for changes and triggers graceful reload
Safe reload validates changes before applying

### Connection Idle Timeout
File: internal/pool/pool.go

max_idle_time: Maximum time a connection can be idle before closing
max_connection_lifetime: Maximum total lifetime of a connection

### Query Timeout
File: internal/pool/pool.go

query_timeout: Maximum time a query can execute before being cancelled
connection_timeout: Time to wait for a backend connection

### Idle Transaction Timeout
File: internal/pool/pool.go

idle_transaction_timeout: Maximum time a transaction can be idle before rollback

### Prepared Statement Cache
File: internal/pool/prepared.go

Transparent re-preparation on new backend connections
Tracks statement name to SQL mapping
Automatic re-prepare on connection acquisition in statement mode

### Query Routing
File: internal/pool/routing.go

RouteQuery() determines backend based on:
- Query type (SELECT vs INSERT/UPDATE/DELETE)
- Transaction state
- User-specified routing hints

### Transaction Manager
File: internal/pool/transaction.go

Tracks active transactions with:
- Timeout enforcement
- Idle timeout enforcement
- Automatic rollback on timeout

### Logger
File: internal/logger/logger.go

Structured logging with:
- JSON and text formats
- Log levels: debug, info, warn, error
- Configurable output

### Query Logger
File: internal/logger/querylog.go

Per-pool query logging with:
- Query text
- Execution time
- Rows returned
- Client address
- Cache hit/miss

### Metrics Registry
File: internal/metrics/metrics.go

In-memory metrics with:
- Counter, Gauge, Histogram types
- Prometheus text format export
- Per-pool and global metrics

### TLS Configuration
File: internal/tlsutil/config.go

CipherSuites12() returns allowed TLS 1.2 cipher suites
Enforces strong cryptography

### Certificate Authentication
File: internal/auth/cert.go

Certificate validation:
- Expiry checking via NotBefore/NotAfter
- Identity extraction from CN or SAN
- Wildcard pattern matching
- Certificate-to-user mapping

### SCRAM Implementation
File: internal/auth/scram.go

Full SCRAM-SHA-256 implementation:
- PBKDF2 with SHA-256
- HMAC-based signatures
- Server and client nonces
- Auth message construction

### Auth Rate Limiter
File: internal/auth/auth.go

Per-IP tracking:
- Failed attempt counting
- Sliding window
- Automatic lockout
- Lockout duration

### REST API Middleware
File: internal/api/rest/server.go

- withLogging: Request logging
- withSecurityHeaders: Security headers
- withCORS: Cross-origin requests
- withAuth: Bearer token validation
- withRateLimit: Per-IP rate limiting

### Error Response Sanitization
File: internal/api/rest/server.go

writeError() ensures error messages do not leak:
- Internal file paths
- Connection strings
- Stack traces
- Database error details

### Path Sanitization
File: internal/proxy/listener.go

Pool name sanitized before use:
- Alphanumeric only
- Underscores and hyphens allowed
- Max 64 characters

### Table Name Validation
File: internal/pool/reset.go

validTableName regex for cache invalidation:
- Must start with letter or underscore
- Alphanumeric and underscores only
- Max 128 characters

### Command-line Flags
File: cmd/geryon/main.go

- --config: Config file path
- --validate: Validate config only
- --version: Show version
- --generate-config: Generate example config
- --generate-password: Generate SCRAM password hash
- --generate-cert: Generate self-signed TLS certificate

### Signal Handling
File: cmd/geryon/main.go

- SIGINT/SIGTERM: Graceful shutdown
- SIGHUP: Hot config reload

### Admin API Ports (Default)
File: internal/config/config.go

- REST: 127.0.0.1:8080
- gRPC: 127.0.0.1:9090
- MCP: 127.0.0.1:8081
- Dashboard: 127.0.0.1:8082

### Config File Environment Variables
File: internal/config/loader.go

Only GERYON_* prefixed env vars expanded
Syntax:  or default

