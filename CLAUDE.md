# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Geryon is a high-performance, multi-database connection pooler and proxy built in **pure Go** with **minimal dependencies**. Named after the three-bodied giant of Greek mythology, Geryon speaks PostgreSQL, MySQL, and MSSQL wire protocols from a single static binary.

## Development Commands

### Build
```bash
# Build binary to bin/geryon
make build

# Cross-compile releases for all platforms
make release

# Build manually (CGO must be disabled for release builds)
CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/geryon ./cmd/geryon

# Build with race detector (requires CGO=1, development only)
CGO_ENABLED=1 go build -race -o bin/geryon ./cmd/geryon
```

### Test
```bash
# Run all tests with race detection
make test
# or: go test -race -cover ./...

# Run tests without integration tests (CI mode)
go test -short -race -cover ./...

# Run specific package tests
go test -race ./internal/pool/
go test -race ./internal/protocol/postgresql/

# Run single test
go test -race -run TestPoolMode ./internal/pool/

# Run benchmarks
make bench
# or: go test -bench=. -benchmem -run=^$ ./benchmarks/...

# Run integration tests (requires running databases)
go test -v ./integration-tests/
```

### Lint & Security
```bash
make lint
# Runs: go vet, gofmt check, gosec security scan

# Individual checks
go vet ./...
gofmt -s -l .  # Should return nothing
gosec -exclude=G115,G401,G104,G304,G301,G302,G306,G501,G505 ./...
```

### Docker
```bash
make docker  # Build container image
```

### Clean
```bash
make clean  # Remove bin/ directory
```

### Run
```bash
# Generate example config first
./bin/geryon --generate-config > geryon.yaml

# Edit config with your backend settings, then start
./bin/geryon --config geryon.yaml

# Validate config without starting
./bin/geryon --validate

# Generate SCRAM-SHA-256 password hash
./bin/geryon --generate-password

# Generate self-signed TLS certificate
./bin/geryon --generate-cert
```

## Architecture

### Three Bodies Architecture
Geryon implements three database protocol handlers ("Bodies"):

| Body | Package | Port | Protocol Version |
|------|---------|------|------------------|
| PostgreSQL | `internal/protocol/postgresql/` | 5432 | Frontend/Backend v3 |
| MySQL | `internal/protocol/mysql/` | 3306 | Handshake v10 |
| MSSQL | `internal/protocol/mssql/` | 1433 | TDS 7.4+ |

### Protocol Implementation
Protocol handling is done directly in `internal/proxy/` using codecs from `internal/protocol/`:

- **`internal/protocol/`** — Low-level wire protocol codecs (message framing, parsing, serialization)
- **`internal/proxy/`** — TCP listener, client acceptance, protocol-specific connection state machines

### Pooling Modes
Three pooling strategies implemented in `internal/pool/`:

- **Session Mode** (`ModeSession`): 1:1 client-to-backend connection. Use for temp tables, SET vars, LISTEN/NOTIFY.
- **Transaction Mode** (`ModeTransaction`): N:M multiplexing. Best for web apps (default).
- **Statement Mode** (`ModeStatement`): N:1 aggressive multiplexing. For simple query patterns.

### Key Components

```
cmd/geryon/           # Main entry point
├── main.go          # Service orchestration, signal handling, hot-reload
└── embed.go         # Dashboard static assets via embed.FS

internal/
├── pool/            # Connection pooling core
│   ├── pool.go      # Pool implementation, backend connections
│   ├── manager.go   # Multi-pool lifecycle management
│   ├── session.go   # Session tracking for session mode
│   ├── transaction.go # Transaction boundary detection
│   ├── routing.go   # Read/write splitting, backend selection
│   ├── health.go    # Backend health checking
│   └── reset.go     # Connection state reset for reuse
├── protocol/        # Wire protocol codecs (low-level)
│   ├── common/      # Shared message types
│   ├── postgresql/  # PostgreSQL codec
│   ├── mysql/       # MySQL codec
│   └── mssql/       # MSSQL/TDS codec
├── auth/            # Authentication
│   ├── auth.go      # User database, credential verification
│   └── scram.go     # SCRAM-SHA-256 implementation
├── config/          # Configuration
│   ├── config.go    # Config structs, validation
│   ├── loader.go    # YAML loading
│   └── watcher.go   # File watching for hot-reload
├── proxy/           # Client listeners
│   └── listener.go  # TCP listener, client acceptance
├── api/             # Management interfaces
│   ├── rest/        # REST API (:8080)
│   ├── grpc/        # HTTP/2 Admin API (:9090) - JSON over HTTP/2
│   ├── mcp/         # MCP server (:8081)
│   └── dashboard/   # Web dashboard
├── cluster/         # Clustering
│   ├── cluster.go   # Raft + SWIM integration
├── raft/            # Raft consensus (custom implementation)
├── swim/            # SWIM gossip protocol (custom implementation)
├── cache/           # Query result cache (LRU with TTL)
├── stmt/            # Prepared statement cache
├── tlsutil/         # TLS utilities
├── tokenizer/       # SQL tokenizer (for query classification)
├── metrics/         # Prometheus metrics
└── logger/          # Structured logging
```

### Configuration Hot-Reload
Configuration supports hot-reload via:
1. **SIGHUP**: Signal triggers reload of safe-to-change settings
2. **File watch**: Config file changes detected automatically
3. **API**: POST `/api/v1/config/reload`

Safe reloads (no restart needed): pool limits, auth users, logging level.
Unsafe reloads (require restart): port changes, body type, TLS cert paths.

See `internal/config/watcher.go` and `config.IsSafeReload()` for logic.

### Connection State Reset
When a server connection is returned to the pool (in transaction/statement mode), Geryon resets the connection state to ensure a clean slate for the next client:

**PostgreSQL:**
- Sends `DISCARD ALL` command
- Clears session variables, temp tables, prepared statements

**MySQL:**
- Sends `COM_RESET_CONNECTION` (MySQL 5.7.3+)
- Falls back to `COM_CHANGE_USER` if not supported

**MSSQL:**
- Sends RPC request for `sp_reset_connection`

See `internal/pool/reset.go` for implementation:
- `ConnectionResetter` interface for protocol-specific reset logic
- `SmartResetter` for tracking state modifications and minimizing round-trips

### Clustering
Optional clustering via Raft consensus + SWIM gossip:
- **Raft** (`internal/raft/`): Configuration replication, leader election
- **SWIM** (`internal/swim/`): Node discovery, failure detection

## Code Patterns

### Dependency Philosophy
- **Stdlib-first**: Prefer standard library where possible
- **No CGo for releases**: `CGO_ENABLED=0` for production builds (cross-compile friendly)
- **CGO required for race detector**: `CGO_ENABLED=1` for `go test -race`
- **Single Binary**: Embedded assets via `embed.FS`
- **External deps** (go.mod): `go-sql-driver/mysql`, `lib/pq`, `yaml.v3`, `golang.org/x/term`, `golang.org/x/time`
- **Go version**: go.mod requires 1.26.1; CI tests against Go 1.25 and 1.26

### Atomic Configuration Access
Configuration uses `atomic.Pointer[config.Config]` for lock-free concurrent reads during hot-reload:
```go
var cfgHolder atomic.Pointer[config.Config]
// Read: cfgHolder.Load()
// Write: cfgHolder.Store(newCfg)
```

### Pool Architecture
Each pool manages:
- **Client connections**: Incoming connections from applications
- **Server connections**: Outbound connections to backends (managed in `Pool.servers`)
- **Wait queue**: Clients waiting for available backend connections
- **Session tracking**: Maps client conns to backend conns (for session mode)

### Protocol Implementation Pattern
Each database protocol follows this structure:
1. **Codec** (`internal/protocol/{db}/codec.go`): Low-level message framing/parsing
2. **Proxy handler** (`internal/proxy/listener.go`): Connection state machine, auth, pool integration
3. **Authentication**: Body-specific auth (SCRAM for PG, caching_sha2 for MySQL, etc.)

## CI/CD

GitHub Actions runs three jobs on push/PR to master:
1. **Test** — `go test -short -race -cover` on Ubuntu, macOS, Windows with Go 1.25/1.26
2. **Lint** — `go vet`, `gofmt` check, `gosec` with excluded rules (G115, G401, G104, G304, G301, G302, G306, G501, G505)
3. **Build** — `make build`, benchmarks, binary upload

Use `-short` flag to skip integration tests in CI. Integration tests (`integration-tests/`) cover: smoke, pooling, routing, TLS, chaos, memory, prepared statements, MySQL, MSSQL.

## Testing

Tests use table-driven patterns with `_test.go` files co-located with source:
```go
func TestFeature(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        // test cases
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // test logic
        })
    }
}
```

Run tests with race detection enabled: `go test -race ./...`

Benchmarks are in `benchmarks/` and use `testing.B` with parallel execution patterns.

## Configuration File

Example config at `geryon.example.yaml`. Key sections:
- `global`: Logging settings
- `pools[]`: Database pool definitions (one per listen port)
- `auth`: Proxy user authentication (interception or passthrough mode)
- `admin`: REST/HTTP2/MCP/dashboard endpoints
- `cluster`: Raft/SWIM clustering settings

## RTK (Rust Token Killer)

**Always prefix commands with `rtk`**. See global `~/.claude/CLAUDE.md` for full command reference.

**Important**: Even in command chains with `&&`, use `rtk`:
```bash
# ❌ Wrong
git add . && git commit -m "msg" && git push

# ✅ Correct
rtk git add . && rtk git commit -m "msg" && rtk git push
```

Key Go-specific RTK filters: `rtk go test`, `rtk go build`, `rtk go vet`, `rtk tsc`, `rtk lint`
<!-- /rtk-instructions -->