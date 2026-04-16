# Architecture Decision Records

Key architectural decisions made during the development of Geryon Proxy.

---

## ADR-001: Pure Go with Minimal Dependencies

**Status:** Accepted
**Date:** 2026-04-10

### Context
Geryon needs to be deployable as a single static binary across multiple platforms (Linux, macOS, Windows) without external runtime dependencies.

### Decision
- Use pure Go for all protocol implementations (PostgreSQL, MySQL, MSSQL)
- Production dependencies limited to 3: `yaml.v3`, `x/term`, `x/time`
- No CGo for release builds (`CGO_ENABLED=0`)
- No vendor directory — dependencies managed via `go.mod`
- Custom implementations of Raft consensus and SWIM gossip instead of using external libraries

### Consequences
- **Positive:** Single binary deployment, cross-platform compilation, no supply chain risk from large dependency trees
- **Negative:** Custom Raft/SWIM implementations require more maintenance than using established libraries
- **Trade-off:** CGo is required for race detector (`CGO_ENABLED=1`), but not needed for production builds

---

## ADR-002: Vanilla JS Dashboard (No Build Step)

**Status:** Accepted
**Date:** 2026-04-10

### Context
The web dashboard needs to provide real-time monitoring and management capabilities without requiring a frontend build toolchain.

### Decision
- Build the dashboard with vanilla HTML/CSS/JavaScript
- No npm, no bundler (Webpack/Vite), no framework (React/Vue)
- Static assets embedded in the binary via `embed.FS`
- Real-time updates via Server-Sent Events (SSE)

### Consequences
- **Positive:** Zero build complexity for frontend, embedded in binary, instant page loads, no N+1 dependency chain
- **Negative:** Less developer familiarity with vanilla JS patterns, no component reuse, manual DOM manipulation
- **Trade-off:** Simpler maintenance at the cost of modern developer ergonomics

---

## ADR-003: JSON-over-HTTP/2 Admin API (Not Protobuf gRPC)

**Status:** Accepted
**Date:** 2026-04-10

### Context
The specification initially called for a protobuf-based gRPC API. However, implementing full protobuf serialization in pure Go without the `protoc` compiler is complex and adds significant build complexity.

### Decision
- Use JSON-over-HTTP/2 for the admin API (package path: `internal/api/grpc/`)
- Maintain protobuf service definitions in documentation for interface specification
- HTTP/2 provides streaming via Server-Sent Events
- Package directory retained for import compatibility despite non-gRPC implementation

### Consequences
- **Positive:** No protobuf toolchain required, easier debugging (readable JSON), simpler deployment
- **Negative:** Not strictly compatible with gRPC clients, documentation may confuse users expecting protobuf
- **Trade-off:** Ease of development vs. strict spec compliance; documented as "HTTP/2 Admin API" to avoid confusion

---

## ADR-004: External Dependencies for Database Drivers in Tests

**Status:** Accepted
**Date:** 2026-04-10

### Context
Integration tests need to connect to real PostgreSQL and MySQL instances.

### Decision
- Use `lib/pq` and `go-sql-driver/mysql` as test-only dependencies
- These are not compiled into release binaries (`CGO_ENABLED=0`)
- Production protocol handling uses custom codecs, not these drivers

### Consequences
- **Positive:** Real database testing without maintaining custom test harnesses
- **Negative:** Two additional dependencies in `go.mod` (test scope only)
- **Trade-off:** Test reliability vs. zero-dependency claim in production

---

## ADR-005: Custom Raft and SWIM Implementations

**Status:** Accepted
**Date:** 2026-04-10

### Context
Clustering requires consensus (Raft) and membership (SWIM) protocols. External libraries like `hashicorp/raft` would add significant dependency weight.

### Decision
- Implement Raft consensus from scratch (`internal/raft/`)
- Implement SWIM gossip protocol from scratch (`internal/swim/`)
- Both include WAL, snapshots, suspicion mechanisms, and metadata dissemination

### Consequences
- **Positive:** Zero external dependencies for clustering, full control over behavior
- **Negative:** Significant implementation effort, potential for subtle bugs in distributed systems code
- **Trade-off:** Independence vs. battle-tested library reliability

---

## ADR-006: Auth Interception Mode as Default

**Status:** Accepted
**Date:** 2026-04-10

### Context
Geryon can operate in passthrough mode (forward auth to backend) or interception mode (authenticate clients against Geryon's own user database).

### Decision
- Default to interception mode with SCRAM-SHA-256 authentication
- Maintain separate user database in Geryon
- Map N proxy users to M backend credentials
- Passthrough mode available for simpler deployments

### Consequences
- **Positive:** Centralized user management, N:1 credential mapping, per-user connection limits
- **Negative:** Additional configuration overhead, user database must be managed separately
- **Trade-off:** Security and flexibility vs. operational complexity

---

## ADR-007: Three Pooling Modes with Statement Mode Aggressive Multiplexing

**Status:** Accepted
**Date:** 2026-04-10

### Context
Different workloads require different connection pooling strategies.

### Decision
- **Session Mode:** 1:1 client-to-backend connection (temp tables, SET vars, LISTEN/NOTIFY)
- **Transaction Mode:** N:M multiplexing, connection released after transaction boundary (default for web apps)
- **Statement Mode:** N:1 aggressive multiplexing, connection released after each statement (max throughput for simple queries)

### Consequences
- **Positive:** Single tool handles all workload patterns
- **Negative:** Statement mode requires stateless queries only, transaction mode needs transaction boundary detection
- **Trade-off:** Flexibility vs. complexity in mode selection and connection state management
