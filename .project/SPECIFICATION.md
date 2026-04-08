# GERYON — SPECIFICATION

> **Three Bodies. One Proxy. Every Connection.**

## 1. PROJECT IDENTITY

| Field | Value |
|---|---|
| Name | Geryon |
| Tagline | Three Bodies. One Proxy. Every Connection. |
| Domain | geryonproxy.com |
| GitHub | github.com/GeryonProxy/geryon |
| License | Apache 2.0 |
| Language | Go (stdlib-only, zero external dependencies) |
| Binary | Single static binary, cross-compiled |
| Philosophy | #NOFORKANYMORE — pure Go, no CGo, no vendor |
| Mythology | Geryon — three-bodied giant from Greek mythology (Heracles' 10th labor). One entity with three bodies, each representing a database protocol. |

## 2. PROBLEM STATEMENT

Modern applications connect to multiple database engines (PostgreSQL, MySQL, MSSQL) but existing connection poolers are single-database:

- **PgBouncer** — PostgreSQL only, C, limited observability, no native clustering
- **ProxySQL** — MySQL only, C++, complex configuration
- **No MSSQL pooler** — Microsoft ecosystem relies on driver-level pooling only

Teams running polyglot database architectures must deploy and manage 2-3 separate poolers, each with different configs, monitoring, and failure modes. There is no unified solution.

**Geryon solves this** by providing a single binary that speaks all three wire protocols, with unified pooling, monitoring, and clustering.

## 3. THREE BODIES ARCHITECTURE

Geryon's architecture maps directly to the mythological three-bodied giant:

```
                    ┌─────────────────────────────────────────┐
                    │              GERYON PROXY                │
                    │                                         │
  Client ──────►   │  ┌───────────┐ ┌──────────┐ ┌────────┐  │
  (PG proto)       │  │  BODY I   │ │ BODY II  │ │BODY III│  │
                   │  │PostgreSQL │ │  MySQL   │ │  MSSQL │  │
  Client ──────►   │  │  Codec    │ │  Codec   │ │  Codec │  │
  (MySQL proto)    │  └─────┬─────┘ └────┬─────┘ └───┬────┘  │
                   │        │            │            │       │
  Client ──────►   │        ▼            ▼            ▼       │
  (TDS proto)      │  ┌─────────────────────────────────────┐ │
                   │  │          UNIFIED POOL MANAGER        │ │
                   │  │  Session │ Transaction │ Statement   │ │
                   │  └──────────────────┬──────────────────┘ │
                   │                     │                    │
                   │  ┌──────────────────┴──────────────────┐ │
                   │  │         BACKEND CONNECTOR            │ │
                   │  │  PG Server │ MySQL Server │ MSSQL   │ │
                   │  └─────────────────────────────────────┘ │
                   └─────────────────────────────────────────┘
```

### 3.1 Body I — PostgreSQL Codec

- Wire protocol: PostgreSQL v3 (Frontend/Backend message format)
- Auth methods: SCRAM-SHA-256, MD5, trust, password, certificate
- Features: Extended Query protocol, COPY support, LISTEN/NOTIFY passthrough
- Prepared statements: Named + unnamed, server-side tracking with deallocation on release
- SSL: Full TLS + mTLS with client certificate validation
- Parameter status tracking: server_version, server_encoding, client_encoding, DateStyle, TimeZone, integer_datetimes, standard_conforming_strings

### 3.2 Body II — MySQL Codec

- Wire protocol: MySQL Client/Server Protocol (handshake v10)
- Auth methods: mysql_native_password, caching_sha2_password, sha256_password
- Features: COM_QUERY, COM_STMT_PREPARE/EXECUTE/CLOSE, COM_CHANGE_USER
- Prepared statements: Binary protocol with parameter binding, server-side stmt ID mapping
- SSL: Full TLS + mTLS
- Capability flags negotiation between client and pooled server connections

### 3.3 Body III — MSSQL Codec

- Wire protocol: TDS 7.4+ (Tabular Data Stream)
- Auth methods: SQL Server Authentication, Windows/NTLM (passthrough)
- Features: SQL Batch, RPC Request (sp_executesql), Bulk Load passthrough
- Prepared statements: sp_prepare/sp_execute/sp_unprepare mapping
- SSL: TDS-level encryption negotiation + full TLS
- MARS (Multiple Active Result Sets) awareness

## 4. POOLING ENGINE

### 4.1 Pool Modes

#### Session Pooling
- Client gets a dedicated server connection for entire session lifetime
- Server connection returned to pool only when client disconnects
- Use case: Applications using session-level features (temp tables, SET variables, LISTEN/NOTIFY)
- Lowest multiplexing ratio, highest compatibility

#### Transaction Pooling
- Server connection assigned at transaction start (BEGIN)
- Released back to pool at transaction end (COMMIT/ROLLBACK)
- Auto-commit statements get a connection for single statement duration
- Use case: Most web applications, microservices
- Optimal multiplexing ratio for typical workloads
- Restrictions: No session-level state between transactions

#### Statement Pooling
- Server connection assigned per individual statement
- Released immediately after response is complete
- Most aggressive multiplexing
- Use case: Simple query patterns with no multi-statement transactions
- Restrictions: No transactions, no session state, no prepared statements across statements

### 4.2 Pool Configuration

```yaml
pools:
  - name: "primary-pg"
    body: postgresql          # postgresql | mysql | mssql
    mode: transaction         # session | transaction | statement
    listen:
      host: "0.0.0.0"
      port: 5432
    backend:
      hosts:
        - host: "pg-primary.internal"
          port: 5432
          role: primary       # primary | replica
          weight: 100
        - host: "pg-replica-1.internal"
          port: 5432
          role: replica
          weight: 50
      database: "myapp"
      auth:
        method: scram-sha-256
        username: "geryon_pool"
        password_file: "/etc/geryon/secrets/pg-password"
    limits:
      max_client_connections: 10000
      max_server_connections: 100
      min_server_connections: 10
      max_idle_time: "300s"
      max_connection_lifetime: "3600s"
      connection_timeout: "5s"
      query_timeout: "30s"
      idle_transaction_timeout: "60s"
    health:
      check_interval: "5s"
      check_query: "SELECT 1"
      max_failures: 3
      
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
          role: primary
      database: "analytics"
      auth:
        method: caching_sha2_password
        username: "geryon_pool"
        password_file: "/etc/geryon/secrets/mysql-password"
    limits:
      max_client_connections: 5000
      max_server_connections: 50
```

### 4.3 Connection Lifecycle

```
Client Connect
     │
     ▼
┌──────────────┐
│  TLS Handshake│ (if configured)
└──────┬───────┘
       ▼
┌──────────────┐
│ Auth Intercept│ → validate against Geryon user database
└──────┬───────┘
       ▼
┌──────────────┐
│ Pool Assign  │ → based on pool mode:
│              │   session: immediate server assignment
│              │   transaction: deferred until BEGIN/first query
│              │   statement: deferred until each statement
└──────┬───────┘
       ▼
┌──────────────┐
│ Query Proxy  │ → forward queries, track state
└──────┬───────┘
       ▼
┌──────────────┐
│ Connection   │ → release server back to pool
│ Release      │   (timing depends on pool mode)
└──────────────┘
```

### 4.4 Server Connection State Reset

When a server connection is returned to the pool (transaction/statement mode), Geryon must reset server state:

**PostgreSQL:**
- `DISCARD ALL` or selective: `RESET ALL; DEALLOCATE ALL; UNLISTEN *; SET SESSION AUTHORIZATION DEFAULT;`
- Track which resets are actually needed to minimize round-trips

**MySQL:**
- `COM_RESET_CONNECTION` (MySQL 5.7.3+) or `COM_CHANGE_USER`
- Fallback: manual `SET` commands for older versions

**MSSQL:**
- `sp_reset_connection` (TDS protocol level)
- Environment change token tracking

### 4.5 Read/Write Splitting

```yaml
routing:
  read_write_split: true
  rules:
    - match: "SELECT"          # Simple keyword match
      target: replica
      fallback: primary        # If no replica available
    - match: "SELECT.*FOR UPDATE"
      target: primary          # Write-intent reads
    - match: "*"
      target: primary          # Default: all writes to primary
```

- Heuristic-based: SELECT → replica, everything else → primary
- Transaction-aware: All queries within an explicit transaction go to the same server
- Configurable per pool

## 5. PREPARED STATEMENT MANAGEMENT

### 5.1 Server-Side Prepared Statement Cache

The core challenge: in transaction/statement pooling, the client's prepared statements may not exist on the next server connection assigned.

**Strategy: Transparent re-preparation**

```
Client                  Geryon                    Server Pool
  │                       │                           │
  │──PREPARE stmt_1──────►│                           │
  │                       │──PREPARE stmt_1──────────►│ (Server A)
  │                       │◄─ParseComplete────────────│
  │◄─ParseComplete────────│                           │
  │                       │  [cache: stmt_1 → SQL]    │
  │                       │  [release Server A]        │
  │                       │                           │
  │──EXECUTE stmt_1──────►│                           │
  │                       │  [assign Server B]         │
  │                       │  [stmt_1 not on B!]        │
  │                       │──PREPARE stmt_1──────────►│ (Server B)
  │                       │◄─ParseComplete────────────│
  │                       │──EXECUTE stmt_1──────────►│
  │                       │◄─Results─────────────────│
  │◄─Results──────────────│                           │
```

- Geryon maintains a local map: `{client_stmt_name → (SQL, param_types)}`
- On EXECUTE, if assigned server lacks the statement, transparently re-prepares
- Server-side tracking: each server connection tracks which statements are prepared on it
- LRU eviction when server statement count exceeds configurable limit

### 5.2 Query Result Cache

Optional caching layer for read-heavy workloads:

```yaml
cache:
  enabled: true
  max_memory: "256MB"
  default_ttl: "60s"
  rules:
    - match: "SELECT.*FROM products WHERE"
      ttl: "300s"
    - match: "SELECT.*FROM sessions"
      ttl: "0"               # Never cache
  invalidation:
    - on_write: true          # Invalidate on INSERT/UPDATE/DELETE to same table
    - manual: true            # REST API / MCP invalidation endpoints
```

- In-memory result cache with configurable TTL
- Table-level write invalidation (parse query → extract tables → invalidate)
- Per-query-pattern TTL rules
- Cache hit/miss metrics exposed on dashboard
- Manual invalidation via REST/MCP/gRPC

## 6. AUTH INTERCEPTION

Geryon can operate in two auth modes:

### 6.1 Passthrough Mode
- Client authenticates directly with backend server through Geryon
- Geryon transparently forwards auth messages
- Simple setup, no user management in Geryon

### 6.2 Interception Mode
- Geryon maintains its own user database
- Client authenticates against Geryon
- Geryon uses pooled credentials to connect to backends
- Allows N:1 client-to-backend-user mapping

```yaml
auth:
  mode: interception          # passthrough | interception
  users:
    - username: "app_readonly"
      password_hash: "SCRAM-SHA-256$4096:..."
      max_connections: 100
      default_pool: "primary-pg"
      allowed_pools: ["primary-pg", "analytics-mysql"]
      
    - username: "app_admin"
      password_hash: "SCRAM-SHA-256$4096:..."
      max_connections: 10
      allowed_pools: ["*"]
      
  tls:
    mode: require             # disable | allow | prefer | require | verify-ca | verify-full
    cert_file: "/etc/geryon/tls/server.crt"
    key_file: "/etc/geryon/tls/server.key"
    ca_file: "/etc/geryon/tls/ca.crt"
    client_auth: optional     # none | optional | required (mTLS)
```

### 6.3 mTLS (Mutual TLS)

- Client certificate validation against configurable CA
- Certificate CN/SAN → Geryon username mapping
- Certificate-based auth eliminates password management
- Per-pool TLS policy (some pools may require mTLS, others allow password)

## 7. CLUSTERING

### 7.1 Raft Consensus

Used for **configuration consistency** across cluster nodes:

- Pool configurations synchronized via Raft log
- User database replicated across all nodes
- Leader election for coordinated operations (schema changes, cache invalidation)
- From-scratch Raft implementation (no external deps)

### 7.2 Gossip Protocol (SWIM)

Used for **node discovery and health**:

- Membership protocol: nodes discover and track each other
- Failure detection: configurable suspicion timeout
- Metadata dissemination: node load, connection counts, backend health
- Lightweight: minimal overhead even with large clusters

### 7.3 Cluster Topology

```
                    ┌──────────────┐
                    │   Client     │
                    │   (any DB    │
                    │    proto)    │
                    └──────┬───────┘
                           │
                    ┌──────▼───────┐
                    │ Load Balancer│ (external: DNS/HAProxy/etc)
                    └──────┬───────┘
                           │
            ┌──────────────┼──────────────┐
            │              │              │
     ┌──────▼──────┐ ┌────▼────┐ ┌──────▼──────┐
     │  Geryon N1  │ │Geryon N2│ │  Geryon N3  │
     │  (Leader)   │ │(Follower)│ │ (Follower)  │
     │             │ │         │ │             │
     │ Raft + SWIM │◄►│Raft+SWIM│◄►│ Raft + SWIM │
     └──────┬──────┘ └────┬────┘ └──────┬──────┘
            │              │              │
            ▼              ▼              ▼
     ┌─────────────────────────────────────────┐
     │          Database Backends              │
     │  PG Primary/Replicas                    │
     │  MySQL Primary/Replicas                 │
     │  MSSQL Primary/Replicas                 │
     └─────────────────────────────────────────┘
```

- Each Geryon node maintains its own connection pools to backends
- Raft ensures config consistency; SWIM ensures health awareness
- Nodes can share backend health information to avoid thundering herd on failover

## 8. MANAGEMENT INTERFACES

### 8.1 REST API

```
GET    /api/v1/pools                    # List all pools
GET    /api/v1/pools/{name}             # Pool details + stats
PUT    /api/v1/pools/{name}             # Update pool config (hot reload)
POST   /api/v1/pools/{name}/pause       # Pause pool (drain connections)
POST   /api/v1/pools/{name}/resume      # Resume pool

GET    /api/v1/connections               # Active connections
DELETE /api/v1/connections/{id}          # Kill connection

GET    /api/v1/backends                  # Backend server status
POST   /api/v1/backends/{id}/detach     # Remove backend from rotation
POST   /api/v1/backends/{id}/attach     # Add backend back

GET    /api/v1/stats                     # Global statistics
GET    /api/v1/stats/pools/{name}        # Per-pool statistics
GET    /api/v1/stats/queries             # Query statistics

GET    /api/v1/cache/stats               # Cache hit/miss stats
POST   /api/v1/cache/invalidate          # Manual cache invalidation

GET    /api/v1/cluster                   # Cluster status
GET    /api/v1/cluster/nodes             # Node list + health

GET    /api/v1/users                     # User list (interception mode)
POST   /api/v1/users                     # Create user
PUT    /api/v1/users/{name}              # Update user
DELETE /api/v1/users/{name}              # Delete user

POST   /api/v1/reload                    # Hot-reload config from file
GET    /api/v1/health                    # Health check endpoint
GET    /api/v1/ready                     # Readiness probe
```

### 8.2 MCP Server (Model Context Protocol)

LLM-native management for AI-assisted database operations:

**Tools:**
- `geryon_pool_list` — List pools with status
- `geryon_pool_stats` — Detailed pool statistics
- `geryon_connection_list` — Active connections with metadata
- `geryon_connection_kill` — Terminate a connection
- `geryon_backend_status` — Backend health overview
- `geryon_backend_detach` — Remove backend from rotation
- `geryon_backend_attach` — Restore backend
- `geryon_cache_stats` — Cache performance metrics
- `geryon_cache_invalidate` — Invalidate cache entries
- `geryon_cluster_status` — Cluster health and node list
- `geryon_config_reload` — Hot-reload configuration
- `geryon_query_stats` — Top queries by time/frequency
- `geryon_user_manage` — CRUD operations on proxy users

**Resources:**
- `geryon://config` — Current running configuration
- `geryon://pools/{name}` — Pool details
- `geryon://stats/overview` — Global stats snapshot
- `geryon://cluster/topology` — Cluster node map

### 8.3 gRPC API

Protocol buffer service for programmatic integration:

```protobuf
service GeryonAdmin {
  // Pool Management
  rpc ListPools(Empty) returns (PoolList);
  rpc GetPool(PoolRequest) returns (PoolDetail);
  rpc UpdatePool(PoolConfig) returns (PoolDetail);
  rpc PausePool(PoolRequest) returns (Status);
  rpc ResumePool(PoolRequest) returns (Status);
  
  // Connection Management
  rpc ListConnections(ConnectionFilter) returns (ConnectionList);
  rpc KillConnection(ConnectionRequest) returns (Status);
  
  // Backend Management
  rpc ListBackends(Empty) returns (BackendList);
  rpc DetachBackend(BackendRequest) returns (Status);
  rpc AttachBackend(BackendRequest) returns (Status);
  
  // Statistics
  rpc GetStats(StatsRequest) returns (StatsResponse);
  rpc StreamStats(StatsRequest) returns (stream StatsResponse);
  
  // Cache
  rpc GetCacheStats(Empty) returns (CacheStats);
  rpc InvalidateCache(InvalidateRequest) returns (Status);
  
  // Cluster
  rpc GetClusterStatus(Empty) returns (ClusterStatus);
  rpc ListNodes(Empty) returns (NodeList);
  
  // Config
  rpc ReloadConfig(Empty) returns (Status);
}
```

### 8.4 Web Dashboard

Embedded web dashboard (no external dependencies, served from binary):

**Pages:**
1. **Overview** — Global stats: total connections, queries/sec, cache hit rate, cluster health
2. **Pools** — Per-pool view: connection counts, wait queue, avg query time, mode indicator
3. **Backends** — Backend server list: status (up/down/degraded), latency, connection count
4. **Connections** — Live connection table: client IP, pool, state, duration, current query
5. **Query Stats** — Top queries by time, frequency; slow query log
6. **Cache** — Hit/miss rate graph, memory usage, top cached queries
7. **Cluster** — Node map, Raft state, leader indicator, gossip health
8. **Config** — Live config editor with validation + hot-reload button
9. **Users** — User management (interception mode): create, edit, permissions

**Tech stack:**
- Vanilla HTML/CSS/JS (embedded in binary via `embed.FS`)
- SSE (Server-Sent Events) for real-time stats streaming
- No build step, no npm, no bundler

## 9. OBSERVABILITY

### 9.1 Built-in Metrics

Exposed via REST API and dashboard, covering:

**Pool Metrics:**
- `geryon_pool_client_connections_active` — Current active client connections
- `geryon_pool_client_connections_waiting` — Clients waiting for server connection
- `geryon_pool_server_connections_active` — Server connections in use
- `geryon_pool_server_connections_idle` — Idle server connections
- `geryon_pool_server_connections_total` — Total server connections (active + idle)
- `geryon_pool_queries_total` — Total queries processed
- `geryon_pool_queries_duration_ms` — Query duration histogram
- `geryon_pool_transactions_total` — Total transactions
- `geryon_pool_errors_total` — Error count by type
- `geryon_pool_wait_time_ms` — Client wait time for server connection

**Backend Metrics:**
- `geryon_backend_status` — 0=down, 1=up, 2=degraded
- `geryon_backend_latency_ms` — Health check latency
- `geryon_backend_connections` — Connection count per backend

**Cache Metrics:**
- `geryon_cache_hits_total` — Cache hits
- `geryon_cache_misses_total` — Cache misses
- `geryon_cache_memory_bytes` — Cache memory usage
- `geryon_cache_entries` — Number of cached entries
- `geryon_cache_evictions_total` — Eviction count

**Cluster Metrics:**
- `geryon_cluster_nodes` — Number of known nodes
- `geryon_cluster_raft_state` — leader/follower/candidate
- `geryon_cluster_raft_term` — Current Raft term

### 9.2 Logging

- Structured JSON logging to stdout/file
- Log levels: debug, info, warn, error
- Per-component log level configuration
- Slow query logging with configurable threshold
- Connection lifecycle logging (connect, auth, assign, release, disconnect)

## 10. HOT RELOAD

- YAML config file watched for changes (fsnotify equivalent via stdlib polling)
- `SIGHUP` signal triggers reload
- REST/MCP/gRPC reload endpoints
- Validation before applying: syntax check + semantic check (port conflicts, pool name uniqueness)
- Graceful pool reconfiguration: new connections use new config, existing drain on old
- Zero-downtime: listener ports do not restart on config change

## 11. CLI INTERFACE

```bash
geryon                           # Start with default config (geryon.yaml)
geryon --config /path/to/config  # Start with specific config
geryon --validate                # Validate config without starting
geryon --version                 # Print version
geryon --generate-config         # Generate example config
geryon --generate-password       # Generate SCRAM-SHA-256 password hash
geryon --generate-cert           # Generate self-signed TLS cert for testing
```

## 12. PERFORMANCE TARGETS

| Metric | Target |
|---|---|
| Max client connections | 100,000+ per node |
| Connection setup latency | < 1ms (pool assignment) |
| Query proxy overhead | < 100μs per query |
| Memory per idle connection | < 8KB |
| Config reload | < 100ms, zero downtime |
| Binary size | < 30MB |
| Startup time | < 2s |
| Health check interval | Configurable, default 5s |

## 13. PLATFORM SUPPORT

| Platform | Status |
|---|---|
| Linux (amd64, arm64) | Primary target |
| macOS (amd64, arm64) | Development + Production |
| Windows (amd64) | Production |
| Docker | Official images |
| Kubernetes | Helm chart + operator (future) |

## 14. NON-GOALS (v1)

- Full SQL parser (uses lightweight tokenizer for routing/cache, not full AST)
- Query rewriting / transformation
- Cross-database query federation
- Built-in Prometheus exporter (built-in metrics only; Prometheus scrape via REST API metrics endpoint is acceptable)
- Kubernetes operator (future consideration)
- Plugin/extension system (future consideration)
