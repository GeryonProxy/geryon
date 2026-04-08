# GERYON — IMPLEMENTATION

> Technical implementation guide for the Geryon multi-database connection pooler.

## 1. PROJECT STRUCTURE

```
geryon/
├── cmd/
│   └── geryon/
│       └── main.go                  # Entry point, CLI flags, signal handling
├── internal/
│   ├── config/
│   │   ├── config.go                # Config struct definitions
│   │   ├── loader.go                # YAML loading + validation
│   │   ├── watcher.go               # File change detection + hot reload
│   │   └── defaults.go              # Default values
│   ├── protocol/
│   │   ├── common/
│   │   │   ├── message.go           # Common message interface
│   │   │   ├── buffer.go            # Read/write buffer utilities
│   │   │   └── types.go             # Shared protocol types
│   │   ├── postgresql/
│   │   │   ├── codec.go             # PG wire protocol v3 codec
│   │   │   ├── messages.go          # PG message type definitions
│   │   │   ├── auth.go              # SCRAM-SHA-256, MD5 auth
│   │   │   ├── startup.go           # Startup message handling
│   │   │   ├── extended.go          # Extended query protocol (Parse/Bind/Execute)
│   │   │   ├── copy.go              # COPY protocol handling
│   │   │   ├── notify.go            # LISTEN/NOTIFY passthrough
│   │   │   └── params.go            # Parameter status tracking
│   │   ├── mysql/
│   │   │   ├── codec.go             # MySQL wire protocol codec
│   │   │   ├── messages.go          # MySQL packet definitions
│   │   │   ├── auth.go              # mysql_native_password, caching_sha2
│   │   │   ├── handshake.go         # Handshake v10
│   │   │   ├── command.go           # COM_QUERY, COM_STMT_*, COM_CHANGE_USER
│   │   │   └── capability.go        # Capability flags negotiation
│   │   └── mssql/
│   │       ├── codec.go             # TDS 7.4+ protocol codec
│   │       ├── messages.go          # TDS packet/message definitions
│   │       ├── auth.go              # SQL Auth + NTLM passthrough
│   │       ├── prelogin.go          # Pre-login handshake
│   │       ├── batch.go             # SQL Batch handling
│   │       ├── rpc.go               # RPC Request (sp_executesql)
│   │       └── token.go             # TDS token stream parser
│   ├── pool/
│   │   ├── manager.go               # Pool manager (creates/destroys pools)
│   │   ├── pool.go                  # Single pool: backend list + conn slots
│   │   ├── session.go               # Session pooling strategy
│   │   ├── transaction.go           # Transaction pooling strategy
│   │   ├── statement.go             # Statement pooling strategy
│   │   ├── backend.go               # Backend server connection
│   │   ├── health.go                # Backend health checker
│   │   ├── reset.go                 # Connection state reset per protocol
│   │   ├── wait_queue.go            # Client wait queue (FIFO)
│   │   └── routing.go               # Read/write split routing
│   ├── proxy/
│   │   ├── listener.go              # TCP listener per pool
│   │   ├── session.go               # Client session lifecycle
│   │   ├── relay.go                 # Bidirectional message relay
│   │   ├── interceptor.go           # Query interception (cache check, routing)
│   │   └── tls.go                   # TLS/mTLS termination
│   ├── auth/
│   │   ├── interceptor.go           # Auth interception mode
│   │   ├── passthrough.go           # Auth passthrough mode
│   │   ├── users.go                 # User database (in-memory + Raft replicated)
│   │   ├── scram.go                 # SCRAM-SHA-256 implementation
│   │   ├── md5.go                   # MD5 password hashing (PG legacy)
│   │   ├── mysql_native.go          # mysql_native_password
│   │   ├── sha2.go                  # caching_sha2_password
│   │   └── cert.go                  # Certificate-based auth (mTLS CN/SAN mapping)
│   ├── cache/
│   │   ├── store.go                 # In-memory result cache (LRU + TTL)
│   │   ├── key.go                   # Cache key generation (normalized query)
│   │   ├── invalidation.go          # Write-triggered + manual invalidation
│   │   └── rules.go                 # Per-pattern TTL rules
│   ├── stmt/
│   │   ├── cache.go                 # Prepared statement metadata cache
│   │   ├── tracker.go               # Per-server-conn prepared stmt tracking
│   │   └── remapper.go              # Client→server stmt ID remapping
│   ├── cluster/
│   │   ├── raft/
│   │   │   ├── raft.go              # Raft consensus implementation
│   │   │   ├── log.go               # Raft log (append-only, WAL)
│   │   │   ├── state.go             # Raft state machine
│   │   │   ├── transport.go         # Raft RPC over TCP
│   │   │   ├── snapshot.go          # Raft snapshotting
│   │   │   └── election.go          # Leader election
│   │   ├── gossip/
│   │   │   ├── swim.go              # SWIM protocol implementation
│   │   │   ├── membership.go        # Membership list management
│   │   │   ├── detector.go          # Failure detector (suspicion)
│   │   │   └── metadata.go          # Node metadata dissemination
│   │   └── cluster.go               # Cluster coordinator (Raft + SWIM)
│   ├── api/
│   │   ├── rest/
│   │   │   ├── server.go            # HTTP server + router
│   │   │   ├── pools.go             # Pool endpoints
│   │   │   ├── connections.go       # Connection endpoints
│   │   │   ├── backends.go          # Backend endpoints
│   │   │   ├── stats.go             # Stats endpoints
│   │   │   ├── cache.go             # Cache endpoints
│   │   │   ├── cluster.go           # Cluster endpoints
│   │   │   ├── users.go             # User management endpoints
│   │   │   ├── config.go            # Config endpoints
│   │   │   └── middleware.go        # Auth, logging, CORS middleware
│   │   ├── grpc/
│   │   │   ├── server.go            # gRPC server
│   │   │   ├── admin.go             # GeryonAdmin service implementation
│   │   │   └── proto/
│   │   │       └── geryon.go        # Hand-written protobuf serialization
│   │   └── mcp/
│   │       ├── server.go            # MCP server (stdio + SSE transport)
│   │       ├── tools.go             # Tool definitions + handlers
│   │       └── resources.go         # Resource providers
│   ├── dashboard/
│   │   ├── handler.go               # HTTP handler for dashboard
│   │   ├── sse.go                   # SSE stats streaming
│   │   └── static/                  # Embedded static files
│   │       ├── index.html
│   │       ├── app.js
│   │       └── style.css
│   ├── metrics/
│   │   ├── collector.go             # Metrics collection engine
│   │   ├── counters.go              # Atomic counter types
│   │   ├── histogram.go             # Histogram implementation
│   │   └── registry.go              # Metrics registry
│   ├── tokenizer/
│   │   ├── tokenizer.go             # Lightweight SQL tokenizer
│   │   ├── classify.go              # Query classification (SELECT/INSERT/UPDATE/DELETE/DDL)
│   │   └── tables.go                # Table name extraction
│   └── logger/
│       ├── logger.go                # Structured JSON logger
│       └── levels.go                # Log level management
├── embed.go                         # go:embed directives for dashboard
├── go.mod
├── go.sum                           # Empty (zero deps)
├── Makefile
├── Dockerfile
├── geryon.example.yaml
├── SPECIFICATION.md
├── IMPLEMENTATION.md
├── TASKS.md
├── BRANDING.md
├── README.md
└── PROMPT.md
```

## 2. CORE DATA STRUCTURES

### 2.1 Protocol Message Interface

```go
// internal/protocol/common/message.go

// Direction indicates message flow direction.
type Direction uint8

const (
    Frontend Direction = iota  // Client → Proxy
    Backend                    // Proxy → Server
)

// Message represents a database wire protocol message.
type Message struct {
    Type      byte      // Protocol-specific message type byte
    Length    int32     // Total message length (including self)
    Payload   []byte    // Raw message payload
    Direction Direction // Frontend or Backend
}

// Codec is the interface each protocol body must implement.
type Codec interface {
    // ReadMessage reads one complete message from the connection.
    ReadMessage(r io.Reader) (*Message, error)
    
    // WriteMessage writes one complete message to the connection.
    WriteMessage(w io.Writer, msg *Message) error
    
    // IsStartup returns true if this is a startup/handshake message.
    IsStartup(msg *Message) bool
    
    // IsTerminate returns true if this is a termination message.
    IsTerminate(msg *Message) bool
    
    // IsQuery returns true if this is a query message.
    IsQuery(msg *Message) bool
    
    // IsTransactionBegin returns true if message starts a transaction.
    IsTransactionBegin(msg *Message) bool
    
    // IsTransactionEnd returns true if message ends a transaction.
    IsTransactionEnd(msg *Message) bool
    
    // IsPrepare returns true if this is a prepare statement message.
    IsPrepare(msg *Message) bool
    
    // IsExecute returns true if this is an execute prepared stmt message.
    IsExecute(msg *Message) bool
    
    // ExtractQuery extracts the SQL query string from a query message.
    ExtractQuery(msg *Message) (string, error)
    
    // GenerateResetSequence returns messages to reset server state.
    GenerateResetSequence() []*Message
    
    // Protocol returns the protocol identifier.
    Protocol() Protocol
}

// Protocol identifies the database protocol.
type Protocol uint8

const (
    ProtocolPostgreSQL Protocol = iota
    ProtocolMySQL
    ProtocolMSSQL
)
```

### 2.2 Pool Core

```go
// internal/pool/pool.go

// PoolMode defines the connection pooling strategy.
type PoolMode uint8

const (
    ModeSession     PoolMode = iota
    ModeTransaction
    ModeStatement
)

// Pool manages a set of backend connections for a single listen endpoint.
type Pool struct {
    mu          sync.RWMutex
    name        string
    config      *config.PoolConfig
    mode        PoolMode
    codec       common.Codec
    
    // Backend servers
    backends    []*Backend
    primary     *Backend          // Current primary (for write routing)
    replicas    []*Backend        // Replica list (for read routing)
    
    // Connection slots
    serverConns *serverConnPool   // Available server connections
    waitQueue   *WaitQueue        // Clients waiting for a connection
    
    // State
    clientCount atomic.Int64      // Active client connections
    queryCount  atomic.Int64      // Total queries processed
    
    // Prepared statements
    stmtCache   *stmt.Cache       // Global stmt metadata cache
    
    // Result cache
    resultCache *cache.Store      // Optional query result cache
    
    // Metrics
    metrics     *metrics.PoolMetrics
    
    // Lifecycle
    ctx         context.Context
    cancel      context.CancelFunc
}

// serverConnPool is a protocol-aware pool of server connections.
type serverConnPool struct {
    mu       sync.Mutex
    idle     []*ServerConn        // Idle connections ready for use
    active   map[uint64]*ServerConn // Active connections by ID
    maxSize  int
    minSize  int
    
    // Per-backend pools for routing
    byBackend map[string][]*ServerConn
}

// ServerConn represents a single connection to a backend server.
type ServerConn struct {
    id          uint64
    conn        net.Conn
    backend     *Backend
    codec       common.Codec
    createdAt   time.Time
    lastUsedAt  time.Time
    txnActive   bool
    
    // Prepared statements known to this server connection
    preparedStmts map[string]bool
    
    // Protocol-specific state
    paramStatus   map[string]string   // PG parameter status
    capabilities  uint32              // MySQL capability flags
}
```

### 2.3 Client Session

```go
// internal/proxy/session.go

// Session represents a client's connection lifecycle.
type Session struct {
    id          uint64
    clientConn  net.Conn
    pool        *pool.Pool
    serverConn  *pool.ServerConn    // nil until assigned
    codec       common.Codec
    
    // Auth state
    user        string
    database    string
    authDone    bool
    
    // Transaction state
    inTxn       bool
    txnStart    time.Time
    
    // Session state
    startedAt   time.Time
    queryCount  int64
    bytesIn     int64
    bytesOut    int64
    lastQuery   string
    
    // Pool mode behavior
    strategy    PoolStrategy
}

// PoolStrategy defines mode-specific behavior.
type PoolStrategy interface {
    // OnQuery is called when a query message arrives.
    // Returns the server connection to use (may acquire from pool).
    OnQuery(s *Session, msg *common.Message) (*pool.ServerConn, error)
    
    // OnQueryComplete is called when query response is complete.
    // May release server connection back to pool.
    OnQueryComplete(s *Session) error
    
    // OnTransactionBegin is called on BEGIN/START TRANSACTION.
    OnTransactionBegin(s *Session) error
    
    // OnTransactionEnd is called on COMMIT/ROLLBACK.
    OnTransactionEnd(s *Session) error
    
    // OnDisconnect is called when client disconnects.
    OnDisconnect(s *Session) error
}
```

### 2.4 Cluster State

```go
// internal/cluster/cluster.go

// Node represents a Geryon cluster member.
type Node struct {
    ID       string    `json:"id"`
    Addr     string    `json:"addr"`        // host:port for Raft
    APIAddr  string    `json:"api_addr"`    // host:port for REST
    GRPCAddr string    `json:"grpc_addr"`   // host:port for gRPC
    State    NodeState `json:"state"`
    
    // SWIM metadata
    Load        float64   `json:"load"`
    Connections int64     `json:"connections"`
    Uptime      int64     `json:"uptime"`
    LastSeen    time.Time `json:"last_seen"`
}

// NodeState represents a node's current state.
type NodeState uint8

const (
    NodeAlive NodeState = iota
    NodeSuspect
    NodeDead
    NodeLeft
)

// Cluster coordinates Raft + SWIM for the Geryon cluster.
type Cluster struct {
    localNode   *Node
    raft        *raft.Raft
    swim        *gossip.SWIM
    
    // Replicated state (via Raft)
    poolConfigs map[string]*config.PoolConfig
    users       map[string]*auth.User
    
    // Discovered nodes (via SWIM)
    nodes       map[string]*Node
    
    mu          sync.RWMutex
}
```

## 3. IMPLEMENTATION PATTERNS

### 3.1 Zero-Allocation Message Relay

The hot path (message relay between client and server) must minimize allocations:

```go
// internal/proxy/relay.go

// Relay performs bidirectional message forwarding.
// Uses double-buffering to avoid allocation per message.
func (r *Relay) Run(ctx context.Context, client, server net.Conn) error {
    errCh := make(chan error, 2)
    
    // Client → Server
    go func() {
        buf := make([]byte, 32*1024) // 32KB buffer, reused
        errCh <- r.forward(ctx, client, server, buf, Frontend)
    }()
    
    // Server → Client
    go func() {
        buf := make([]byte, 32*1024)
        errCh <- r.forward(ctx, server, client, buf, Backend)
    }()
    
    // Wait for first error (or ctx cancellation)
    err := <-errCh
    return err
}

// forward copies messages one direction with minimal parsing.
// Only inspects messages when needed (txn boundary detection, cache check).
func (r *Relay) forward(ctx context.Context, src, dst net.Conn, buf []byte, dir Direction) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }
        
        // Read message header (type + length)
        n, err := io.ReadFull(src, buf[:5])
        if err != nil {
            return err
        }
        
        msgType := buf[0]
        msgLen := int(binary.BigEndian.Uint32(buf[1:5]))
        
        // Check if we need to inspect this message
        if r.needsInspection(msgType, dir) {
            // Read full message for inspection
            msg, err := r.readFull(src, buf, msgType, msgLen)
            if err != nil {
                return err
            }
            
            // Intercept (cache check, txn tracking, etc.)
            action := r.intercept(msg, dir)
            
            switch action {
            case ActionForward:
                _, err = dst.Write(msg.Raw)
            case ActionCache:
                // Cache the response, then forward
                r.cache.Store(r.currentQuery, msg.Payload)
                _, err = dst.Write(msg.Raw)
            case ActionServeCached:
                // Serve from cache, don't forward to server
                _, err = dst.Write(r.cachedResponse)
            }
        } else {
            // Fast path: write header + splice remaining bytes
            _, err = dst.Write(buf[:5])
            if err != nil {
                return err
            }
            remaining := msgLen - 4 // length includes self
            _, err = io.CopyBuffer(dst, io.LimitReader(src, int64(remaining)), buf)
        }
        
        if err != nil {
            return err
        }
    }
}
```

### 3.2 Wait Queue with Timeout

```go
// internal/pool/wait_queue.go

// WaitQueue manages clients waiting for a server connection.
type WaitQueue struct {
    mu      sync.Mutex
    waiters list.List  // *waiter elements
    metrics *metrics.WaitQueueMetrics
}

type waiter struct {
    ch      chan *ServerConn
    timer   *time.Timer
    added   time.Time
}

// Wait blocks until a server connection is available or timeout.
func (wq *WaitQueue) Wait(ctx context.Context, timeout time.Duration) (*ServerConn, error) {
    w := &waiter{
        ch:    make(chan *ServerConn, 1),
        timer: time.NewTimer(timeout),
        added: time.Now(),
    }
    
    wq.mu.Lock()
    elem := wq.waiters.PushBack(w)
    wq.metrics.WaitingInc()
    wq.mu.Unlock()
    
    defer func() {
        w.timer.Stop()
        wq.mu.Lock()
        wq.waiters.Remove(elem)
        wq.metrics.WaitingDec()
        wq.mu.Unlock()
    }()
    
    select {
    case conn := <-w.ch:
        wq.metrics.WaitDuration(time.Since(w.added))
        return conn, nil
    case <-w.timer.C:
        return nil, ErrConnectionTimeout
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}

// Signal gives a connection to the longest-waiting client.
func (wq *WaitQueue) Signal(conn *ServerConn) bool {
    wq.mu.Lock()
    defer wq.mu.Unlock()
    
    for elem := wq.waiters.Front(); elem != nil; elem = elem.Next() {
        w := elem.Value.(*waiter)
        select {
        case w.ch <- conn:
            return true
        default:
            // Waiter already timed out, skip
            continue
        }
    }
    return false // No waiters
}
```

### 3.3 SCRAM-SHA-256 From Scratch

```go
// internal/auth/scram.go

// SCRAMServer implements SCRAM-SHA-256 server-side authentication.
type SCRAMServer struct {
    iterations int
    users      UserLookup
}

// Authenticate performs the full SCRAM handshake.
func (s *SCRAMServer) Authenticate(username string, exchange func(challenge []byte) ([]byte, error)) error {
    user, err := s.users.Lookup(username)
    if err != nil {
        return ErrAuthFailed
    }
    
    // Step 1: Receive client-first-message
    // Step 2: Generate server-first-message (nonce, salt, iterations)
    // Step 3: Receive client-final-message
    // Step 4: Verify client proof
    // Step 5: Send server-final-message (server signature)
    
    // SaltedPassword = Hi(Normalize(password), salt, iterations)
    // ClientKey = HMAC(SaltedPassword, "Client Key")
    // StoredKey = H(ClientKey)
    // AuthMessage = client-first-message-bare + "," + server-first-message + "," + client-final-message-without-proof
    // ClientSignature = HMAC(StoredKey, AuthMessage)
    // ClientProof = ClientKey XOR ClientSignature
    // ServerKey = HMAC(SaltedPassword, "Server Key")
    // ServerSignature = HMAC(ServerKey, AuthMessage)
    
    // All using crypto/sha256 and crypto/hmac from stdlib
    // ...
    
    return nil
}
```

### 3.4 Transaction Boundary Detection

```go
// internal/protocol/postgresql/codec.go

// IsTransactionBegin detects transaction start for PG.
func (c *PGCodec) IsTransactionBegin(msg *Message) bool {
    if msg.Type != 'Q' { // Simple Query
        return false
    }
    query := strings.ToUpper(strings.TrimSpace(c.ExtractSimpleQuery(msg)))
    return strings.HasPrefix(query, "BEGIN") ||
           strings.HasPrefix(query, "START TRANSACTION")
}

// IsTransactionEnd detects transaction end for PG.
func (c *PGCodec) IsTransactionEnd(msg *Message) bool {
    if msg.Type == 'Q' {
        query := strings.ToUpper(strings.TrimSpace(c.ExtractSimpleQuery(msg)))
        return strings.HasPrefix(query, "COMMIT") ||
               strings.HasPrefix(query, "ROLLBACK") ||
               strings.HasPrefix(query, "END")
    }
    // Also check ReadyForQuery with 'I' (idle) status after error
    if msg.Type == 'Z' && msg.Direction == Backend {
        return msg.Payload[0] == 'I' // Idle = not in transaction
    }
    return false
}
```

### 3.5 gRPC Without External Dependencies

Since we cannot use google.golang.org/grpc, Geryon implements a minimal gRPC server:

```go
// internal/api/grpc/server.go

// Server implements a minimal HTTP/2-based gRPC server using net/http.
// Go's net/http supports HTTP/2 natively, so we can handle gRPC frames.
type Server struct {
    httpServer *http.Server
    services   map[string]ServiceHandler
}

// ServeHTTP handles incoming gRPC requests over HTTP/2.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // gRPC uses HTTP/2 POST with content-type: application/grpc
    if r.ProtoMajor != 2 {
        http.Error(w, "gRPC requires HTTP/2", http.StatusBadRequest)
        return
    }
    
    // Parse service/method from path: /package.Service/Method
    service, method := parseGRPCPath(r.URL.Path)
    
    handler, ok := s.services[service]
    if !ok {
        writeGRPCError(w, CodeUnimplemented, "unknown service")
        return
    }
    
    // Read request: 1 byte compressed flag + 4 bytes length + payload
    // Deserialize using hand-rolled protobuf (no protoc dependency)
    // Call handler
    // Serialize response + write with gRPC framing
    handler.Handle(method, r.Body, w)
}

// Hand-rolled protobuf serialization for admin messages.
// Uses varint encoding, field tags, wire types — all from scratch.
// Covers: PoolList, PoolDetail, ConnectionList, StatsResponse, etc.
```

### 3.6 Raft Implementation

```go
// internal/cluster/raft/raft.go

// Raft implements the Raft consensus protocol from scratch.
type Raft struct {
    mu sync.Mutex
    
    // Persistent state
    currentTerm uint64
    votedFor    string
    log         *Log
    
    // Volatile state
    commitIndex uint64
    lastApplied uint64
    state       RaftState  // follower | candidate | leader
    
    // Leader volatile state
    nextIndex   map[string]uint64
    matchIndex  map[string]uint64
    
    // Components
    transport   *Transport     // TCP-based RPC
    fsm         StateMachine   // Applied to: config changes, user CRUD
    snapshot    *SnapshotStore
    
    // Timers
    electionTimer  *time.Timer
    heartbeatTimer *time.Timer
    
    // Configuration
    peers       []string
    localID     string
}

// StateMachine is applied when Raft log entries are committed.
type StateMachine interface {
    Apply(entry *LogEntry) error
    Snapshot() ([]byte, error)
    Restore(data []byte) error
}

// GeryonFSM applies config and user changes.
type GeryonFSM struct {
    poolConfigs map[string]*config.PoolConfig
    users       map[string]*auth.User
    mu          sync.RWMutex
}
```

## 4. WIRE PROTOCOL IMPLEMENTATION DETAILS

### 4.1 PostgreSQL v3

**Message Format:**
```
┌─────────┬──────────┬─────────────┐
│ Type(1B) │ Len(4B)  │ Payload(nB) │
└─────────┴──────────┴─────────────┘
```

**Key message types to implement:**

| Type | Name | Direction | Priority |
|------|------|-----------|----------|
| - | StartupMessage | F | P0 |
| R | Authentication | B | P0 |
| p | PasswordMessage | F | P0 |
| Q | Query | F | P0 |
| T | RowDescription | B | P0 |
| D | DataRow | B | P0 |
| C | CommandComplete | B | P0 |
| Z | ReadyForQuery | B | P0 |
| E | ErrorResponse | B | P0 |
| P | Parse | F | P0 |
| B | Bind | F | P0 |
| E | Execute | F | P0 |
| S | Sync | F | P0 |
| X | Terminate | F | P0 |
| d | CopyData | F/B | P1 |
| H | CopyOutResponse | B | P1 |
| G | CopyInResponse | B | P1 |
| K | BackendKeyData | B | P1 |
| N | NoticeResponse | B | P2 |
| A | NotificationResponse | B | P2 |

### 4.2 MySQL Protocol

**Packet Format:**
```
┌────────────┬──────────────┬─────────────┐
│ Length(3B)  │ SeqNum(1B)   │ Payload(nB) │
└────────────┴──────────────┴─────────────┘
```

**Key packets to implement:**

| Command | Code | Priority |
|---------|------|----------|
| COM_QUERY | 0x03 | P0 |
| COM_QUIT | 0x01 | P0 |
| COM_INIT_DB | 0x02 | P0 |
| COM_STMT_PREPARE | 0x16 | P0 |
| COM_STMT_EXECUTE | 0x17 | P0 |
| COM_STMT_CLOSE | 0x19 | P0 |
| COM_PING | 0x0E | P1 |
| COM_CHANGE_USER | 0x11 | P1 |
| COM_RESET_CONNECTION | 0x1F | P1 |
| COM_STMT_RESET | 0x1A | P2 |

### 4.3 TDS (MSSQL) Protocol

**Packet Format:**
```
┌──────────┬───────────┬────────────┬──────────┬──────────┬─────────────┐
│ Type(1B) │ Status(1B)│ Length(2B) │ SPID(2B) │ Pkt#(1B) │ Window(1B)  │
│          │           │            │          │          │ + Payload   │
└──────────┴───────────┴────────────┴──────────┴──────────┴─────────────┘
```

**Key packet types:**

| Type | Name | Priority |
|------|------|----------|
| 0x01 | SQL Batch | P0 |
| 0x03 | RPC | P0 |
| 0x04 | Tabular Result | P0 |
| 0x07 | Attention | P0 |
| 0x0E | Transaction Manager Request | P1 |
| 0x12 | Pre-Login | P0 |
| 0x10 | Login7 | P0 |
| 0x11 | SSPI | P1 |

## 5. BUILD & DEPLOYMENT

### 5.1 Makefile

```makefile
VERSION ?= $(shell git describe --tags --always --dirty)
LDFLAGS = -s -w -X main.version=$(VERSION)

.PHONY: build test clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/geryon ./cmd/geryon

test:
	go test -race -cover ./...

lint:
	go vet ./...

docker:
	docker build -t geryonproxy/geryon:$(VERSION) .

release:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/geryon-linux-amd64 ./cmd/geryon
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/geryon-linux-arm64 ./cmd/geryon
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/geryon-darwin-amd64 ./cmd/geryon
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/geryon-darwin-arm64 ./cmd/geryon
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/geryon-windows-amd64.exe ./cmd/geryon
```

### 5.2 Dockerfile

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /build
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o geryon ./cmd/geryon

FROM scratch
COPY --from=builder /build/geryon /geryon
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
EXPOSE 5432 3306 1433 8080 9090
ENTRYPOINT ["/geryon"]
```

### 5.3 Example Config (geryon.example.yaml)

```yaml
# Geryon — Multi-Database Connection Pooler
# Three Bodies. One Proxy. Every Connection.

global:
  log_level: info           # debug | info | warn | error
  log_format: json          # json | text
  pid_file: /var/run/geryon.pid

admin:
  rest:
    listen: "0.0.0.0:8080"
    auth:
      enabled: true
      token: "${GERYON_ADMIN_TOKEN}"
  grpc:
    listen: "0.0.0.0:9090"
  mcp:
    transport: sse           # stdio | sse
    listen: "0.0.0.0:8081"
  dashboard:
    enabled: true
    path: "/"               # Served on REST port

cluster:
  enabled: false
  node_id: "node-1"
  raft:
    listen: "0.0.0.0:7000"
    peers:
      - "node-2:7000"
      - "node-3:7000"
    election_timeout: "1s"
    heartbeat_interval: "150ms"
  gossip:
    listen: "0.0.0.0:7001"
    join:
      - "node-2:7001"
      - "node-3:7001"

auth:
  mode: interception         # passthrough | interception
  users:
    - username: "app"
      password: "SCRAM-SHA-256$4096:salt:storedkey:serverkey"
      max_connections: 1000
      allowed_pools: ["*"]

pools:
  - name: "main-pg"
    body: postgresql
    mode: transaction
    listen:
      host: "0.0.0.0"
      port: 5432
    backend:
      hosts:
        - host: "localhost"
          port: 5433
          role: primary
      database: "myapp"
      auth:
        username: "postgres"
        password_file: "/etc/geryon/secrets/pg"
    limits:
      max_client_connections: 10000
      max_server_connections: 100
      min_server_connections: 5
      max_idle_time: "300s"
      connection_timeout: "5s"
      query_timeout: "30s"
    tls:
      mode: prefer
    cache:
      enabled: false

  - name: "main-mysql"
    body: mysql
    mode: transaction
    listen:
      host: "0.0.0.0"
      port: 3306
    backend:
      hosts:
        - host: "localhost"
          port: 3307
          role: primary
      database: "myapp"
      auth:
        username: "root"
        password_file: "/etc/geryon/secrets/mysql"
    limits:
      max_client_connections: 5000
      max_server_connections: 50

  - name: "main-mssql"
    body: mssql
    mode: session
    listen:
      host: "0.0.0.0"
      port: 1433
    backend:
      hosts:
        - host: "localhost"
          port: 1434
          role: primary
      database: "myapp"
      auth:
        username: "sa"
        password_file: "/etc/geryon/secrets/mssql"
    limits:
      max_client_connections: 2000
      max_server_connections: 30
```

## 6. TESTING STRATEGY

### 6.1 Unit Tests
- Protocol codec tests: encode/decode round-trip for each message type
- Pool logic: session/transaction/statement mode behavior
- Auth: SCRAM-SHA-256, MD5, mysql_native_password verification
- Tokenizer: query classification, table extraction
- Cache: LRU eviction, TTL expiry, invalidation
- Raft: leader election, log replication, snapshot
- SWIM: join, failure detection, metadata dissemination

### 6.2 Integration Tests
- Full proxy round-trip: client → Geryon → real database
- Pooling mode behavior with actual transactions
- Prepared statement re-preparation across server connections
- TLS/mTLS handshake with real certificates
- Hot reload: config change → verify new behavior
- Cluster: 3-node formation, leader failover

### 6.3 Benchmark Tests
- Message relay throughput (messages/sec)
- Connection setup latency (pool assignment time)
- Prepared statement cache hit/miss performance
- Concurrent client load (1K, 10K, 100K connections)
- Memory usage under load

## 7. DEPENDENCY INVENTORY

| Need | stdlib Solution |
|---|---|
| TCP server | `net` |
| TLS/mTLS | `crypto/tls` |
| HTTP/2 (gRPC) | `net/http` (native h2 support) |
| JSON | `encoding/json` |
| YAML config | From-scratch YAML parser or embedded simple parser |
| SHA-256 / HMAC | `crypto/sha256`, `crypto/hmac` |
| MD5 | `crypto/md5` |
| Random bytes | `crypto/rand` |
| Binary encoding | `encoding/binary` |
| Embedded files | `embed` |
| File watching | `os.Stat` polling (no fsnotify) |
| Logging | `log/slog` (Go 1.21+) |
| Signals | `os/signal` |
| Context | `context` |
| Sync primitives | `sync`, `sync/atomic` |
| Time | `time` |
| Testing | `testing` |
| Protobuf | Hand-rolled varint + field encoding |

**Zero external dependencies confirmed.** `go.sum` will be empty.
