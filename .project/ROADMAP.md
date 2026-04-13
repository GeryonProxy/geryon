# Project Roadmap

> Based on comprehensive codebase audit performed on 2026-04-11
> This roadmap prioritizes work needed to bring the project to production quality.
> **Key change from previous roadmap:** Priorities reflect actual code state — dead code integration and critical bug fixes come before feature completion.

## Current State Assessment

**Status:** Geryon is approximately 70% complete. The core proxy relay works for basic bidirectional forwarding, but critical bugs exist in metrics, auth, and connection pooling. Many advertised features (query cache, prepared statement proxy, read/write splitting, deadlock detection) are implemented as standalone components but never wired into the actual relay path.

**Critical Blockers for Production Readiness:**
1. Server connections not reused from pool — defeats purpose of connection pooler
2. Histogram Sum returns garbage — monitoring is broken
3. Certificate fingerprint uses raw bytes, not hash — auth bypass possible
4. SQL injection in SmartResetter temp table cleanup
5. Mock frontends (~3000 lines) are dead code — misleading architecture

**What's Working Well:**
- Bidirectional relay in `internal/proxy/listener.go` for basic proxy forwarding
- SCRAM-SHA-256 authentication with hand-rolled PBKDF2
- Connection state reset (DISCARD ALL, COM_RESET_CONNECTION, sp_reset_connection)
- REST API, MCP server, and basic dashboard endpoints
- Atomic config access for hot-reload
- SQL tokenizer for query classification

---

## Phase 1: Critical Bug Fixes (Week 1-2)

### P0 — Must fix before ANY production deployment

- [x] **Fix Histogram Sum Calculation** — `internal/metrics/metrics.go:195`
  - ~~`math.Float64frombits(h.sumBits.Load())`~~ — **FIXED** (2026-04-13) - Uses mutex-protected float64
  - Effort: 2 hours
  - Impact: All histogram metrics now return correct Sum() values

- [x] **Fix Certificate Fingerprint** — `internal/auth/cert.go:375-382`
  - ~~`fmt.Sprintf("%x", cert.Raw[:32])`~~ — **FIXED** (2026-04-13) - Uses `sha256.Sum256(cert.Raw)`
  - Effort: 1 hour
  - Impact: Potential auth bypass eliminated

- [x] **Fix SQL Injection in SmartResetter** — `internal/pool/reset.go:281`
  - ~~`fmt.Sprintf("DROP TABLE IF EXISTS %s", table)`~~ — **FIXED** (2026-04-13) - Uses regex validation
  - Effort: 2 hours
  - Impact: SQL injection risk eliminated

- [ ] **Fix Server Connection Reuse** — `internal/pool/pool.go` + strategy files
  - Current: `Acquire()` may create new TCP connections instead of reusing from pool
  - Fix: Wire up `servers` slice reuse — check for idle connections before creating new ones
  - Effort: 16 hours
  - Impact: Without this, Geryon is NOT a connection pooler — every client creates new backend connection

- [x] **Delete or Integrate Mock Frontends** — `internal/protocols/`
  - ~~3000+ lines of unused protocol frontends~~ — **DELETED** (2026-04-13)
  - Effort: 8 hours (delete)
  - Impact: Maintenance burden eliminated, architecture clarified

**Phase 1 Status:** 4/5 items complete. Remaining: Server Connection Reuse verification.

---

## Phase 2: Wire Up Dead Features (Week 3-5)

### P1 — Advertised features that don't actually work

- [ ] **Wire Transaction Manager into Relay** — `internal/pool/transaction.go` + `internal/proxy/listener.go`
  - Current: TransactionManager exists, `checkTimeouts()` sets status but doesn't abort backend
  - Fix: Register transactions in relay, send ROLLBACK to backend on timeout
  - Effort: 16 hours

- [ ] **Wire Query Cache into Relay** — `internal/cache/` + `internal/proxy/listener.go`
  - Current: Cache exists with LRU+TTL but never instantiated in relay path
  - Fix: Check cache before forwarding SELECT queries, store results on response
  - Effort: 16 hours

- [ ] **Wire Prepared Statement Proxy** — `internal/stmt/cache.go` + `internal/proxy/listener.go`
  - Current: TransparentRepreparer exists but never instantiated
  - Fix: Intercept PREPARE/EXECUTE/DEALLOCATE, use cache for re-preparation
  - Effort: 16 hours

- [ ] **Wire Read/Write Splitting** — `internal/pool/routing.go` + `internal/proxy/listener.go`
  - Current: Router with round-robin replica selection exists but not used
  - Fix: Classify queries with tokenizer, route to primary/replica accordingly
  - Effort: 12 hours

- [x] **Delete DeadlockDetector** — **DELETED** (2026-04-13)

- [x] **Delete ConnectionTracker** — **DELETED** (2026-04-13)

**Phase 2 Status:** 2/6 items complete. Remaining wiring tasks are significant refactoring (62 hours).

---

## Phase 3: Security & Hardening (Week 6-7)

### P1 — Security improvements

- [ ] **Add Auth Rate Limiting** — `internal/auth/auth.go`
  - Current: No rate limiting on SCRAM-SHA-256 handshakes
  - Fix: Add per-IP or per-connection rate limiter using `golang.org/x/time/rate`
  - Effort: 8 hours

- [ ] **Handle Write Errors in Relay** — `internal/proxy/listener.go`
  - Current: `io.Copy` write errors ignored in relay goroutines
  - Fix: Propagate errors, close both directions on write failure
  - Effort: 8 hours

- [ ] **Fix Running Average Overflow** — `internal/logger/querylog.go`
  - Current: Running average can overflow with enough samples
  - Fix: Use Welford's online algorithm or cap samples
  - Effort: 2 hours

- [x] **Use Full SHA256 for Prepared Statement Hash** — `internal/pool/prepared.go`
  - ~~`hex.EncodeToString(h[:8])`~~ — **FIXED** (uses `h[:]`)
  - Effort: 1 hour

- [ ] **Replace Fragile YAML Parser** — `internal/config/loader.go`
  - Current: Line-by-line, indent-based custom parser
  - Fix: Either use `gopkg.in/yaml.v3` (adds 1 dependency) or significantly harden custom parser
  - Effort: 16 hours

- [ ] **Make Timeouts Configurable** — `internal/pool/transaction.go`
  - Current: 30-min txn timeout, 5-min idle, 30-sec check interval hardcoded
  - Fix: Accept as constructor parameters, expose via config
  - Effort: 4 hours

**Phase 3 Total:** 40 hours

---

## Phase 4: Protocol Completeness (Week 8-10)

### P2 — Complete missing protocol features

- [ ] **MSSQL NTLM Passthrough** — `internal/protocols/mssql/`
  - Windows Authentication support for enterprise users
  - Effort: 40 hours (complex protocol)

- [ ] **MSSQL Prepared Statements** — `internal/protocols/mssql/`
  - Implement sp_prepare/sp_execute/sp_unprepare
  - Effort: 24 hours

- [ ] **PostgreSQL Extended Query Protocol** — `internal/protocol/postgresql/`
  - Verify Parse/Bind/Execute fully works through relay
  - Effort: 8 hours

- [ ] **Health Check Protocol Queries** — `internal/pool/health.go`
  - Current: TCP-only health checks (dial and close)
  - Fix: Add protocol-specific health queries (SELECT 1 for PG/MySQL, SELECT 1 for MSSQL)
  - Effort: 8 hours

**Phase 4 Total:** 80 hours

---

## Phase 5: Testing & Validation (Week 11-13)

### P1 — Test coverage for critical paths

- [ ] **Integration Tests for Relay** — `internal/proxy/listener.go`
  - Test bidirectional relay with actual database backends
  - Test connection reuse behavior
  - Effort: 24 hours

- [ ] **Concurrency Tests for Pool** — `internal/pool/`
  - Test Acquire/Release under concurrent load
  - Test pool exhaustion and wait queue behavior
  - Effort: 16 hours

- [ ] **Raft Edge Case Tests** — `internal/raft/`
  - Test leader election, network partitions, log replication
  - Effort: 16 hours

- [ ] **Fuzz Testing for Protocol Parsers** — `internal/protocol/`
  - Fuzz PostgreSQL, MySQL, MSSQL message parsers
  - Effort: 16 hours

- [ ] **Load Testing** — `benchmarks/`
  - Test 10K+ concurrent connections
  - Memory leak validation under sustained load
  - Effort: 24 hours

- [ ] **Fix Integration Test Timeout** — `integration-tests/`
  - Current: Tests timeout at 512s
  - Fix: Either mock database backends or provide test containers
  - Effort: 16 hours

**Phase 5 Total:** 112 hours

---

## Phase 6: Clustering Production Readiness (Week 14-15)

### P2 — Make clustering actually work

- [ ] **Consolidate Raft Implementations** — `internal/raft/`
  - Current: Simplified Raft is active, full Raft was planned but never completed
  - Fix: Choose one implementation and make it production-ready
  - Effort: 24 hours

- [ ] **Fix hasMajority Bug** — `internal/raft/`
  - Current: May incorrectly report quorum
  - Fix: Proper majority calculation: `(n/2) + 1`
  - Effort: 2 hours

- [ ] **SWIM Production Hardening** — `internal/swim/`
  - Current: Basic implementation, not production-ready
  - Fix: Add failure detection tuning, network partition handling
  - Effort: 16 hours

- [ ] **Cluster Integration Tests** — `internal/cluster/`
  - 3-node cluster test with leader election
  - Network partition simulation
  - Effort: 16 hours

**Phase 6 Total:** 58 hours

---

## Phase 7: Documentation & Release (Week 16-17)

- [ ] **Operations Guide** — Production deployment, monitoring, troubleshooting
  - Effort: 16 hours

- [ ] **API Documentation** — OpenAPI/Swagger spec for REST API
  - Effort: 12 hours

- [ ] **Architecture Documentation** — Sequence diagrams, protocol flow docs
  - Effort: 12 hours

- [ ] **CI/CD Improvements** — Security scanning (gosec), performance regression tests
  - Effort: 8 hours

- [ ] **Final Security Audit** — gosec, manual review of all fixed vulnerabilities
  - Effort: 8 hours

**Phase 7 Total:** 56 hours

---

## Effort Summary

| Phase | Estimated Hours | Priority | Dependencies |
|-------|-----------------|----------|--------------|
| Phase 1: Critical Bug Fixes | 29-61h | CRITICAL | None |
| Phase 2: Wire Up Dead Features | 66h | CRITICAL | Phase 1 |
| Phase 3: Security & Hardening | 40h | HIGH | Phase 1 |
| Phase 4: Protocol Completeness | 80h | MEDIUM | Phase 2 |
| Phase 5: Testing & Validation | 112h | HIGH | Phase 2 |
| Phase 6: Clustering | 58h | MEDIUM | Phase 1 |
| Phase 7: Documentation & Release | 56h | MEDIUM | Phase 5 |
| **Total** | **441-473 hours** | | |
| **~11-12 weeks** (1 FTE) | | | |

---

## Risk Assessment

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| Connection reuse fix reveals deeper architectural issues | High | High | Prototype fix first before full implementation |
| Mock frontend integration is too complex to justify | Medium | Medium | Delete approach (8h) is the safe fallback |
| Raft consolidation requires complete rewrite | Medium | High | Use simplified Raft, document limitations |
| Protocol tests require real databases that are hard to CI | High | Medium | Use Docker containers in CI (already in docker-compose) |
| Scope creep from "while we're here" refactors | High | Low | Strict adherence to roadmap phases |

---

## Milestones

| Milestone | Target | Deliverables | Success Criteria |
|-----------|--------|--------------|------------------|
| M1: Critical Fixes | Week 2 | All P0 bugs fixed | `go test -race ./...` passes, no critical bugs |
| M2: Features Wired | Week 5 | Cache, txns, routing functional | End-to-end test of wired features |
| M3: Hardened | Week 7 | Security fixes complete | gosec clean, no injection vectors |
| M4: Protocol Complete | Week 10 | All protocol features working | Integration tests pass |
| M5: Tested | Week 13 | 75%+ coverage, load tests pass | All tests green, benchmarks published |
| M6: Cluster Ready | Week 15 | Raft+SWIM production-ready | 3-node cluster test passes |
| M7: v1.0.0 | Week 17 | Production release | Security audit pass, docs complete |

---

## Recommended Team Allocation

For fastest time to production:

- **1 Senior Go Engineer** — Phases 1, 2, 3, 4 (core implementation)
- **1 QA Engineer** — Phase 5 (testing infrastructure)
- **1 Technical Writer/DevOps** — Phase 7 (docs, CI/CD, release)

Total: 3 FTEs for 6 weeks, or 1 FTE for 17 weeks

---

*End of Roadmap*
