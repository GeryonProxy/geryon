# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Geryon is a high-performance, multi-database connection pooler and proxy built in **pure Go** with **zero external dependencies** (stdlib only). Named after the three-bodied giant of Greek mythology, Geryon speaks PostgreSQL, MySQL, and MSSQL wire protocols from a single static binary.

## Development Commands

### Build
```bash
# Build binary to bin/geryon
make build

# Cross-compile releases for all platforms
make release

# Build manually (CGO must be disabled)
CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/geryon ./cmd/geryon
```

### Test
```bash
# Run all tests with race detection
make test
# or: go test -race -cover ./...

# Run specific package tests
go test -race ./internal/pool/
go test -race ./internal/protocol/postgresql/

# Run single test
go test -race -run TestPoolMode ./internal/pool/

# Run benchmarks
go test -bench=. ./benchmarks/
go test -bench=. ./internal/tokenizer/

# Run integration tests (requires running databases)
go test -v ./integration-tests/
```

### Lint
```bash
make lint
# or: go vet ./...
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
| PostgreSQL | `internal/protocols/postgresql/` | 5432 | Frontend/Backend v3 |
| MySQL | `internal/protocols/mysql/` | 3306 | Handshake v10 |
| MSSQL | `internal/protocols/mssql/` | 1433 | TDS 7.4+ |

### Protocol vs Protocols
The codebase uses two distinct layers for database protocol handling:

- **`internal/protocol/` (singular)** — Low-level wire protocol codecs:
  - Message framing, parsing, serialization
  - Binary protocol implementation
  - No connection state logic

- **`internal/protocols/` (plural)** — High-level protocol frontend handlers:
  - Connection state machines
  - Authentication handling
  - Command processing
  - Integrates with pool manager

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
├── protocols/       # Protocol frontend handlers (high-level)
│   ├── postgresql/  # PG frontend handler
│   ├── mysql/       # MySQL frontend handler
│   └── mssql/       # MSSQL frontend handler
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
│   ├── grpc/        # gRPC API (:9090)
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

### Zero Dependencies Philosophy
- **Pure Go**: Only standard library + `golang.org/x/term`, `golang.org/x/time`
- **No CGo**: `CGO_ENABLED=0` required for builds
- **Single Binary**: Embedded assets via `embed.FS`

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
2. **Frontend** (`internal/protocols/{db}/frontend.go`): High-level connection state machine
3. **Authentication**: Body-specific auth (SCRAM for PG, caching_sha2 for MySQL, etc.)

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
- `admin`: REST/gRPC/MCP/dashboard endpoints
- `cluster`: Raft/SWIM clustering settings

<!-- rtk-instructions v2 -->
# RTK (Rust Token Killer) - Token-Optimized Commands

## Golden Rule

**Always prefix commands with `rtk`**. If RTK has a dedicated filter, it uses it. If not, it passes through unchanged. This means RTK is always safe to use.

**Important**: Even in command chains with `&&`, use `rtk`:
```bash
# ❌ Wrong
git add . && git commit -m "msg" && git push

# ✅ Correct
rtk git add . && rtk git commit -m "msg" && rtk git push
```

## RTK Commands by Workflow

### Build & Compile (80-90% savings)
```bash
rtk cargo build         # Cargo build output
rtk cargo check         # Cargo check output
rtk cargo clippy        # Clippy warnings grouped by file (80%)
rtk tsc                 # TypeScript errors grouped by file/code (83%)
rtk lint                # ESLint/Biome violations grouped (84%)
rtk prettier --check    # Files needing format only (70%)
rtk next build          # Next.js build with route metrics (87%)
```

### Test (90-99% savings)
```bash
rtk cargo test          # Cargo test failures only (90%)
rtk vitest run          # Vitest failures only (99.5%)
rtk playwright test     # Playwright failures only (94%)
rtk test <cmd>          # Generic test wrapper - failures only
```

### Git (59-80% savings)
```bash
rtk git status          # Compact status
rtk git log             # Compact log (works with all git flags)
rtk git diff            # Compact diff (80%)
rtk git show            # Compact show (80%)
rtk git add             # Ultra-compact confirmations (59%)
rtk git commit          # Ultra-compact confirmations (59%)
rtk git push            # Ultra-compact confirmations
rtk git pull            # Ultra-compact confirmations
rtk git branch          # Compact branch list
rtk git fetch           # Compact fetch
rtk git stash           # Compact stash
rtk git worktree        # Compact worktree
```

Note: Git passthrough works for ALL subcommands, even those not explicitly listed.

### GitHub (26-87% savings)
```bash
rtk gh pr view <num>    # Compact PR view (87%)
rtk gh pr checks        # Compact PR checks (79%)
rtk gh run list         # Compact workflow runs (82%)
rtk gh issue list       # Compact issue list (80%)
rtk gh api              # Compact API responses (26%)
```

### JavaScript/TypeScript Tooling (70-90% savings)
```bash
rtk pnpm list           # Compact dependency tree (70%)
rtk pnpm outdated       # Compact outdated packages (80%)
rtk pnpm install        # Compact install output (90%)
rtk npm run <script>    # Compact npm script output
rtk npx <cmd>           # Compact npx command output
rtk prisma              # Prisma without ASCII art (88%)
```

### Files & Search (60-75% savings)
```bash
rtk ls <path>           # Tree format, compact (65%)
rtk read <file>         # Code reading with filtering (60%)
rtk grep <pattern>      # Search grouped by file (75%)
rtk find <pattern>      # Find grouped by directory (70%)
```

### Analysis & Debug (70-90% savings)
```bash
rtk err <cmd>           # Filter errors only from any command
rtk log <file>          # Deduplicated logs with counts
rtk json <file>         # JSON structure without values
rtk deps                # Dependency overview
rtk env                 # Environment variables compact
rtk summary <cmd>       # Smart summary of command output
rtk diff                # Ultra-compact diffs
```

### Infrastructure (85% savings)
```bash
rtk docker ps           # Compact container list
rtk docker images       # Compact image list
rtk docker logs <c>     # Deduplicated logs
rtk kubectl get         # Compact resource list
rtk kubectl logs        # Deduplicated pod logs
```

### Network (65-70% savings)
```bash
rtk curl <url>          # Compact HTTP responses (70%)
rtk wget <url>          # Compact download output (65%)
```

### Meta Commands
```bash
rtk gain                # View token savings statistics
rtk gain --history      # View command history with savings
rtk discover            # Analyze Claude Code sessions for missed RTK usage
rtk proxy <cmd>         # Run command without filtering (for debugging)
rtk init                # Add RTK instructions to CLAUDE.md
rtk init --global       # Add RTK to ~/.claude/CLAUDE.md
```

## Token Savings Overview

| Category | Commands | Typical Savings |
|----------|----------|-----------------|
| Tests | vitest, playwright, cargo test | 90-99% |
| Build | next, tsc, lint, prettier | 70-87% |
| Git | status, log, diff, add, commit | 59-80% |
| GitHub | gh pr, gh run, gh issue | 26-87% |
| Package Managers | pnpm, npm, npx | 70-90% |
| Files | ls, read, grep, find | 60-75% |
| Infrastructure | docker, kubectl | 85% |
| Network | curl, wget | 65-70% |

Overall average: **60-90% token reduction** on common development operations.
<!-- /rtk-instructions -->