# Project Roadmap

> Based on comprehensive codebase analysis performed on 2026-04-11
> This roadmap prioritizes work needed to bring the project to production quality.

## Current State Assessment

**Status:** Geryon is approximately 98% feature-complete with a functional core (PostgreSQL, MySQL, MSSQL protocols; connection pooling; management APIs; clustering framework). The codebase demonstrates high quality with ~55% test coverage and zero external dependencies beyond golang.org/x libraries.

**Key Blockers for Production Readiness:**
1. YAML config parser uses basic string parsing (not full YAML spec)
2. MSSQL NTLM passthrough incomplete (affects Windows Authentication users)
3. Some TODO items in critical paths (pool timeouts, transaction tracking)

**What's Working Well:**
- Full PostgreSQL and MySQL protocol implementations
- Three pooling modes (session, transaction, statement) fully functional
- REST/gRPC/MCP APIs complete with authentication
- Web dashboard with real-time SSE streaming
- Raft consensus and SWIM gossip clustering foundation
- Comprehensive test coverage with race detection

---

## Phase 1: Critical Fixes (Week 1-2)

### Must-fix items blocking basic functionality

- [ ] **Fix YAML Parser** — `internal/config/loader.go`
  - Current implementation uses basic string splitting
  - Replace with proper YAML parsing or complete custom parser
  - Affected: Config loading for complex YAML structures
  - Effort: 16 hours

- [ ] **Address Critical TODOs** — Various files
  - `internal/pool/pool.go:394` — Configurable wait queue timeout (currently hardcoded 5s)
  - `internal/pool/pool.go` — Backend authentication implementation
  - `internal/api/rest/server.go` — Pool configuration update endpoint
  - `internal/cluster/coordinator.go` — Leader tracking from Raft
  - Effort: 12 hours

- [ ] **Complete Transaction Manager** — `internal/pool/`
  - Implement `GetActiveTransactions()` method (referenced in REST API)
  - Complete TODO in `internal/pool/session.go` for message handling
  - Effort: 8 hours

**Phase 1 Total:** 36 hours

---

## Phase 2: Core Completion (Week 3-6)

### Complete missing core features from specification

- [ ] **MSSQL NTLM Passthrough** — `internal/protocols/mssql/`
  - Spec: T065, Task T065
  - Windows Authentication support for enterprise users
  - Test exists but implementation incomplete
  - Effort: 40 hours (complex protocol)

- [ ] **MSSQL Prepared Statements** — `internal/protocols/mssql/`
  - Spec: T069, Task T069
  - Implement sp_prepare/sp_execute/sp_unprepare
  - Test exists but implementation incomplete
  - Effort: 24 hours

- [ ] **Complete Pool Features** — `internal/pool/`
  - Weighted replica selection (TODO in pool.go)
  - Backend authentication (TODO in pool.go)
  - Full read/write splitting with fallback
  - Effort: 16 hours

- [ ] **Dashboard Connection Tracking** — `internal/api/dashboard/`
  - TODO in server.go
  - Live connection table with filtering
  - Effort: 12 hours

**Phase 2 Total:** 92 hours

---

## Phase 3: Hardening (Week 7-8)

### Security, error handling, edge cases

- [ ] **Input Validation Hardening**
  - Add stricter validation for all user inputs
  - Path traversal protection for file operations
  - SQL injection audit (tokenzier boundaries)
  - Effort: 16 hours

- [ ] **Error Handling Improvements**
  - Wrap all errors with proper context
  - Add custom error types for better handling
  - Improve error messages for users
  - Effort: 12 hours

- [ ] **Graceful Degradation**
  - Circuit breaker pattern for backend failures
  - Retry logic with exponential backoff
  - Better handling of partial cluster failures
  - Effort: 16 hours

- [ ] **Connection Pool Hardening**
  - Handle edge cases in pool exhaustion
  - Better cleanup of orphaned connections
  - Stress testing under extreme load
  - Effort: 12 hours

**Phase 3 Total:** 56 hours

---

## Phase 4: Testing (Week 9-12)

### Comprehensive test coverage

- [ ] **Increase Unit Test Coverage**
  - Target: 75% overall coverage (from ~55%)
  - Focus on `internal/raft/` and `internal/swim/` (currently ~50-60%)
  - Add missing tests for error paths
  - Effort: 40 hours

- [ ] **Integration Test Expansion**
  - Complete 3-node cluster integration tests
  - Add more multi-protocol test scenarios
  - Test all three pooling modes with real databases
  - Effort: 32 hours

- [ ] **Fuzz Testing**
  - Add fuzz tests for protocol parsers
  - Fuzz test SQL tokenizer
  - Fuzz test config parsing
  - Effort: 16 hours

- [ ] **Load Testing**
  - Create automated load test suite
  - Test 10K+ concurrent connections
  - Memory leak validation under sustained load
  - Benchmark performance targets
  - Effort: 24 hours

- [ ] **Chaos Testing**
  - Expand existing chaos_test.go
  - Network partition simulations
  - Backend failure injection
  - Graceful degradation validation
  - Effort: 16 hours

**Phase 4 Total:** 128 hours

---

## Phase 5: Performance & Optimization (Week 13-14)

### Performance tuning and optimization

- [ ] **Memory Optimization**
  - Implement buffer pooling with `sync.Pool`
  - Reduce allocations in hot paths
  - Profile and optimize memory usage per connection
  - Target: < 8KB per idle connection
  - Effort: 24 hours

- [ ] **Query Latency Optimization**
  - Optimize SQL tokenizer (cache compiled regex)
  - Reduce lock contention in metrics collection
  - Profile query proxy overhead
  - Target: < 100μs per query
  - Effort: 16 hours

- [ ] **Cache Optimization**
  - Optimize cache key generation
  - Improve write invalidation performance
  - Add cache warming for hot queries
  - Effort: 12 hours

- [ ] **Connection Pool Tuning**
  - Optimize wait queue signaling
  - Reduce connection acquisition latency
  - Target: < 1ms connection setup
  - Effort: 8 hours

**Phase 5 Total:** 60 hours

---

## Phase 6: Documentation & DX (Week 15-16)

### Documentation and developer experience

- [ ] **API Documentation**
  - Generate OpenAPI/Swagger spec for REST API
  - Document gRPC service definitions
  - Add API usage examples
  - Effort: 16 hours

- [ ] **Architecture Documentation**
  - Document internal architecture for contributors
  - Add sequence diagrams for key flows
  - Document protocol implementations
  - Effort: 20 hours

- [ ] **Operations Guide**
  - Create production deployment guide
  - Document monitoring and alerting
  - Add troubleshooting guide
  - Create runbook for common issues
  - Effort: 16 hours

- [ ] **Developer Documentation**
  - CONTRIBUTING.md with development workflow
  - Code style guide
  - Testing guidelines
  - Architecture Decision Records (ADRs)
  - Effort: 12 hours

**Phase 6 Total:** 64 hours

---

## Phase 7: Release Preparation (Week 17-18)

### Final production preparation

- [ ] **CI/CD Pipeline Completion**
  - Add integration test stage to CI
  - Add performance regression tests
  - Add security scanning (gosec)
  - Add dependency vulnerability scanning
  - Effort: 12 hours

- [ ] **Docker Production Optimization**
  - Optimize Docker image size further
  - Add health checks to Dockerfile
  - Create docker-compose production template
  - Add Kubernetes deployment examples
  - Effort: 12 hours

- [ ] **Release Automation**
  - Complete .goreleaser.yml configuration
  - Automate changelog generation
  - Add release signing
  - Create release checklist
  - Effort: 8 hours

- [ ] **Monitoring & Observability**
  - Add OpenTelemetry tracing support
  - Create Grafana dashboard templates
  - Add alertmanager rules
  - Document metrics and their meaning
  - Effort: 16 hours

- [ ] **Final Security Audit**
  - Run gosec security scanner
  - Review all TODOs for security implications
  - Penetration testing basics
  - Certificate validation testing
  - Effort: 12 hours

**Phase 7 Total:** 60 hours

---

## Beyond v1.0: Future Enhancements

### Features and improvements for future versions

- [ ] **Kubernetes Operator** — Helm chart + CRD for cloud-native deployments
- [ ] **Query Plan Cache** — Cache query plans for repeated queries
- [ ] **Adaptive Pool Sizing** — Auto-scale pool sizes based on load
- [ ] **Connection Prefetching** — Predictive connection warming
- [ ] **Plugin System** — Custom middleware for query transformation
- [ ] **Query Rewriting** — Automatic query optimization
- [ ] **Cross-Database Federation** — Query across different database types
- [ ] **PostgreSQL COPY Protocol** — Full COPY IN/OUT support
- [ ] **PostgreSQL LISTEN/NOTIFY** — Event forwarding
- [ ] **MSSQL Bulk Copy** — BCP protocol support
- [ ] **WebSocket Support** — Real-time dashboard updates
- [ ] **REST API v2** — GraphQL or improved REST design

---

## Effort Summary

| Phase | Estimated Hours | Priority | Dependencies |
|-------|-----------------|----------|--------------|
| Phase 1: Critical Fixes | 36h | CRITICAL | None |
| Phase 2: Core Completion | 92h | HIGH | Phase 1 |
| Phase 3: Hardening | 56h | HIGH | Phase 1 |
| Phase 4: Testing | 128h | HIGH | Phase 2, 3 |
| Phase 5: Performance | 60h | MEDIUM | Phase 4 |
| Phase 6: Documentation | 64h | MEDIUM | Phase 4 |
| Phase 7: Release Prep | 60h | HIGH | Phase 4, 5, 6 |
| **Total** | **496 hours** | | |
| **~12 weeks** (1 FTE) | | | |

---

## Risk Assessment

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| NTLM implementation complexity | High | Medium | Allocate extra time; consider postponing to v1.1 |
| Test coverage gaps reveal bugs | Medium | High | Prioritize Phase 4; add fuzz testing |
| Performance targets not met | Medium | Medium | Early benchmarking in Phase 5; set realistic targets |
| Breaking changes needed for YAML parser | Medium | Medium | Backward compatibility layer; deprecation warnings |
| Clustering issues in production | Medium | High | Thorough testing in Phase 4; beta program |
| Scope creep | High | Low | Strict adherence to roadmap; defer non-critical features |

---

## Milestones

| Milestone | Target Date | Deliverables | Success Criteria |
|-----------|-------------|--------------|------------------|
| Alpha 1 | Week 2 | Critical fixes complete | All tests pass, no TODOs in critical paths |
| Alpha 2 | Week 6 | Core features complete | NTLM working, all spec features implemented |
| Beta 1 | Week 12 | Testing complete | 75% coverage, load tests pass |
| Beta 2 | Week 14 | Performance optimized | Targets met, benchmarks published |
| RC 1 | Week 16 | Documentation complete | Full docs, ops guide ready |
| v1.0.0 | Week 18 | Production release | Security audit pass, signed release |

---

## Recommended Team Allocation

For fastest time to production:

- **1 Senior Go Engineer** — Phases 1, 2, 3, 5 (core implementation)
- **1 QA/Testing Engineer** — Phase 4 (testing infrastructure)
- **1 Technical Writer/DevOps** — Phase 6, 7 (docs, CI/CD, release)

Total: 3 FTEs for 6 weeks, or 1 FTE for 18 weeks

---

*End of Roadmap*
