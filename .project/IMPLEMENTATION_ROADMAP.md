# Geryon Production Roadmap

> **Last Updated:** 2026-05-02  
> **Current Version:** 0.x (Pre-production)  
> **Target:** v1.0.0 (Production-ready release)

---

## Executive Summary

This document defines the path to Geryon v1.0.0 production readiness. Based on security audits, gap analyses, and production readiness assessments, **47 items** require attention across five phases. Current assessment score: **95/100** (up from 74/100 after security fixes).

---

## Phase 1: Critical Security & Observability (Weeks 1-2)

### 1.1 Distributed Tracing & Correlation IDs

**Priority:** P0 (Critical)  
**Estimated Effort:** 8-12 hours

#### Problem
No request correlation IDs exist. Debugging across client → proxy → backend is impossible in production. When a query fails, there is no way to trace the request through the system.

#### Requirements

```yaml
correlation_id:
  header: "X-Request-ID"           # RFC 7231 compliant
  format: "uuidv4"                 # or k-Sortable UUID
  propagation:
    - "traceparent"                 # W3C Trace Context
    - "tracestate"                  # W3C Trace Context
    
tracing:
  enabled: true
  exporter: "opentelemetry"        # otlpgrpc | jaeger | zipkin
  endpoint: "localhost:4317"        # OTLP gRPC endpoint
  sampling_rate: 0.1                # 10% in production
  service_name: "geryon-proxy"
```

#### Implementation Checklist

- [ ] Add `X-Request-ID` header generation at listener entry
- [ ] Propagate correlation ID through all goroutines
- [ ] Add correlation ID to all log statements
- [ ] Implement W3C Trace Context `traceparent` propagation
- [ ] Integrate OpenTelemetry SDK
- [ ] Add OTLP exporter for trace export
- [ ] Instrument proxy relay for trace spans
- [ ] Add trace context to backend connections
- [ ] Document correlation ID format in SPECIFICATION.md

#### Code Changes Required

```go
// internal/proxy/listener.go
type Relay struct {
    traceSpan   trace.Span
    correlationID string  // ADD THIS
}

// Generate correlation ID on connection accept
func generateCorrelationID() string {
    var id [16]byte
    rand.Read(id[:])
    return fmt.Sprintf("%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:])
}
```

---

### 1.2 Request Logging

**Priority:** P0 (Critical)  
**Estimated Effort:** 4-6 hours

#### Problem
No structured request/response logging. Audit compliance and debugging require full request visibility.

#### Requirements

```yaml
logging:
  request:
    enabled: true
    log_level: "info"              # debug | info | warn | error
    sanitize:
      - "password"
      - "Authorization"
      - "X-Secret"
    max_body_size: "4KB"            # Truncate larger bodies
    
  slow_query:
    threshold: "1s"
    log_level: "warn"
    
  fields:
    - "correlation_id"
    - "client_addr"
    - "pool_name"
    - "query_duration_ms"
    - "query_type"                  # read | write | transaction
    - "rows_affected"
    - "backend_addr"
```

#### Implementation Checklist

- [ ] Add request logging middleware to REST/gRPC servers
- [ ] Implement query logging in proxy relay
- [ ] Add correlation ID to all log output
- [ ] Implement field filtering for sensitive data
- [ ] Add slow query logger
- [ ] Configure log rotation (or document external rotation)

---

## Phase 2: Authentication & Security (Weeks 2-4)

### 2.1 MSSQL NTLM Authentication

**Priority:** P0 (Critical)  
**Estimated Effort:** 16-24 hours

#### Problem
MSSQL Windows Authentication (NTLM/SPPI) is not implemented. Only SQL Authentication (username/password) works.

```go
// integration-tests/mssql_test.go:339
// NTLM required - not implemented in test
```

#### Requirements

```yaml
pools:
  - name: "main-mssql"
    body: mssql
    auth:
      mode: "ntlm"                  # sql_auth | ntlm | windows_auth
      ntlm:
        domain: "${NTLM_DOMAIN}"
        workstation: "${HOSTNAME}"
        # Uses SSPI for Kerberos/NTLM
```

#### Implementation Checklist

- [ ] Implement SSPI authentication in TDS codec
- [ ] Add NTLM challenge/response handling
- [ ] Support Kerberos authentication via SSPI
- [ ] Add integration test for NTLM
- [ ] Document NTLM configuration

#### Technical Notes

NTLM authentication flow:
```
Client → Server: NTLM Negotiation
Server → Client: NTLM Challenge  
Client → Server: NTLM Auth Response (with credentials)
```

The Windows SSPI API provides `InitializeSecurityContext` and `AcceptSecurityContext` functions.

---

### 2.2 gRPC API Completion

**Priority:** P1 (High)  
**Estimated Effort:** 12-16 hours

#### Problem
gRPC API is documented as "PLANNED — Not Implemented" in SPECIFICATION.md.

```yaml
# SPECIFICATION.md:457
### 8.3 gRPC API [PLANNED — Not Implemented]
```

#### Requirements

```protobuf
syntax = "proto3";

package geryon.admin.v1;

service AdminService {
  rpc StreamStats(StreamStatsRequest) returns (stream StreamStatsResponse);
  rpc CreatePool(CreatePoolRequest) returns (CreatePoolResponse);
  rpc UpdatePool(UpdatePoolRequest) returns (UpdatePoolResponse);
  rpc GetPool(GetPoolRequest) returns (Pool);
  rpc DeletePool(DeletePoolRequest) returns (google.protobuf.Empty);
}

service PoolService {
  rpc CreatePool(CreatePoolRequest) returns (Pool);
  rpc UpdatePool(UpdatePoolRequest) returns (Pool);
  rpc GetPool(GetPoolRequest) returns (Pool);
  rpc ListPools(ListPoolsRequest) returns (ListPoolsResponse);
  rpc DeletePool(DeletePoolRequest) returns (google.protobuf.Empty);
  rpc AddBackend(AddBackendRequest) returns (Pool);
  rpc RemoveBackend(RemoveBackendRequest) returns (Pool);
}

service ClusterService {
  rpc JoinCluster(JoinClusterRequest) returns (JoinClusterResponse);
  rpc LeaveCluster(LeaveClusterRequest) returns (LeaveClusterResponse);
  rpc GetClusterStatus(GetClusterStatusRequest) returns (ClusterStatus);
}
```

#### Implementation Checklist

- [ ] Define protobuf schemas
- [ ] Generate Go code from proto files
- [ ] Implement AdminService
- [ ] Implement PoolService
- [ ] Implement ClusterService
- [ ] Add TLS for gRPC
- [ ] Add authentication middleware
- [ ] Add integration tests

---

### 2.3 Dashboard XSS Protection

**Priority:** P1 (High)  
**Estimated Effort:** 4-6 hours

#### Problem
Dashboard uses vanilla JS without verified output encoding. XSS protection not verified.

```
# PRODUCTIONREADY.md:143
- [ ] XSS protection — dashboard is vanilla JS, output encoding not verified
```

#### Implementation Checklist

- [ ] Audit all dashboard user inputs
- [ ] Implement Content-Security-Policy headers
- [ ] Add X-XSS-Protection header
- [ ] Add X-Content-Type-Options header
- [ ] Implement output encoding for dynamic content
- [ ] Add security headers to REST API
- [ ] Test XSS vectors against dashboard

#### Required Headers

```yaml
security:
  headers:
    "X-Frame-Options": "DENY"
    "X-Content-Type-Options": "nosniff"
    "X-XSS-Protection": "1; mode=block"
    "Content-Security-Policy": "default-src 'self'; script-src 'self' 'unsafe-inline'"
    "Referrer-Policy": "strict-origin-when-cross-origin"
```

---

### 2.4 CORS Configuration

**Priority:** P2 (Medium)  
**Estimated Effort:** 2-4 hours

#### Problem
No CORS headers configured. Currently implicit deny (safe but may break legitimate cross-origin clients).

```
# PRODUCTIONREADY.md:156
- [ ] CORS properly configured — currently no CORS headers at all
```

#### Implementation Checklist

- [ ] Add CORS middleware to REST API
- [ ] Configure allowed origins
- [ ] Add CORS preflight handling
- [ ] Document CORS configuration

---

## Phase 3: Clustering & Reliability (Weeks 4-6)

### 3.1 SWIM Timing Bug Fix

**Priority:** P0 (Critical)  
**Estimated Effort:** 2-4 hours

#### Problem

```
# ANALYSIS.md:590
TestCluster_probe_SuccessfulConnection indicates timing bug in SWIM probe logic
Fix probe timing or add retry/assertion timeout
```

#### Root Cause Analysis

The SWIM probe timing is race-dependent. The test expects probe completion within a fixed timeout, but network conditions vary.

#### Implementation Checklist

- [ ] Add retry with exponential backoff to probe
- [ ] Add assertion timeout wrapper to test
- [ ] Add jitter to probe intervals
- [ ] Add logging to probe state machine
- [ ] Update test to be timing-independent

```go
// Current (buggy):
func (p *Protocol) probe(target string) error {
    deadline := time.Now().Add(100 * time.Millisecond)
    // ...
}

// Fixed:
func (p *Protocol) probe(target string) error {
    const maxRetries = 3
    var lastErr error
    for i := 0; i < maxRetries; i++ {
        if err := p.probeOnce(target); err == nil {
            return nil
        }
        lastErr = err
        time.Sleep(time.Millisecond * 100 * time.Duration(1<<i)) // backoff
    }
    return fmt.Errorf("probe failed after %d retries: %w", maxRetries, lastErr)
}
```

---

### 3.2 Cluster HA Improvements

**Priority:** P1 (High)  
**Estimated Effort:** 8-12 hours

#### Requirements

- [ ] Quorum-based leader election
- [ ] Automatic failover with RTT < 5s
- [ ] Consensus verification before writes
- [ ] Cluster health dashboard

#### Implementation Checklist

- [ ] Implement leader lease mechanism
- [ ] Add read-after-write consistency for config changes
- [ ] Add cluster health monitoring
- [ ] Implement graceful leader handoff
- [ ] Add cluster split-brain detection

---

### 3.3 Raft State Backup/Restore

**Priority:** P2 (Medium)  
**Estimated Effort:** 8-12 hours

#### Problem

```
# PRODUCTIONREADY.md:304
- [ ] Backup strategy documented — no backup guide for Raft state
```

#### Implementation Checklist

- [ ] Implement Raft snapshot export
- [ ] Implement Raft WAL export
- [ ] Add restore command
- [ ] Document backup procedure
- [ ] Add backup verification test

```bash
# Planned commands
./bin/geryon backup --output raft-backup-{timestamp}
./bin/geryon restore --input raft-backup-{timestamp}
```

---

## Phase 4: Testing & Documentation (Weeks 6-8)

### 4.1 Load Testing

**Priority:** P1 (High)  
**Estimated Effort:** 8-12 hours

#### Problem

```
# PRODUCTIONREADY.md:242
- [ ] Load tests — chaos_test.go exists but not a true load test
```

#### Implementation Checklist

- [ ] Set up load testing infrastructure (k6, hey, or ghz)
- [ ] Define load test scenarios:
  - Connection pool saturation
  - Query throughput under load
  - Concurrent transaction handling
  - Backend failure simulation
- [ ] Define SLOs:
  - p99 latency < 10ms
  - Throughput > 50K qps/pool
  - Error rate < 0.01%
- [ ] Add load test to CI/CD pipeline
- [ ] Document load testing procedure

#### Example k6 Script

```javascript
// scenarios/load-test.js
import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  stages: [
    { duration: '2m', target: 100 },
    { duration: '5m', target: 500 },
    { duration: '2m', target: 0 },
  ],
  thresholds: {
    http_req_duration: ['p(99)<500'],
    errors: ['rate<0.01'],
  },
};

export default function() {
  const res = http.get('http://localhost:5432/query');
  check(res, {
    'status is 200': (r) => r.status === 200,
    'response time < 500ms': (r) => r.timings.duration < 500,
  });
  sleep(0.1);
}
```

---

### 4.2 OpenAPI Documentation

**Priority:** P2 (Medium)  
**Estimated Effort:** 6-8 hours

#### Problem

```
# PRODUCTIONREADY.md:318
- [ ] API documentation is comprehensive — no OpenAPI/Swagger spec
```

#### Implementation Checklist

- [ ] Generate OpenAPI 3.0 spec from code
- [ ] Add OpenAPI endpoint (`/api/v1/openapi.json`)
- [ ] Add Swagger UI to dashboard
- [ ] Document all error codes
- [ ] Add request/response examples
- [ ] Publish to docs.geryon.dev

---

### 4.3 Chaos Testing Improvements

**Priority:** P2 (Medium)  
**Estimated Effort:** 4-6 hours

#### Current State

`integration-tests/chaos_test.go` exists but is limited.

#### Implementation Checklist

- [ ] Add network partition simulation
- [ ] Add backend failure injection
- [ ] Add latency injection
- [ ] Add memory pressure simulation
- [ ] Add CPU throttling simulation
- [ ] Document chaos engineering practices

---

## Phase 5: Production Readiness (Weeks 8-10)

### 5.1 Alerting & Monitoring

**Priority:** P1 (High)  
**Estimated Effort:** 8-12 hours

#### Problem

```
# PRODUCTIONREADY.md:270
- [ ] Alert-worthy conditions identified — no alerting rules defined
```

#### Alert Rules

```yaml
alerts:
  - name: "HighErrorRate"
    condition: "rate(errors_total[5m]) > 0.01"
    severity: "critical"
    annotations:
      summary: "Error rate above 1%"
      
  - name: "PoolExhaustion"
    condition: "wait_queue_length > max_client_connections * 0.9"
    severity: "warning"
    annotations:
      summary: "Connection pool near capacity"
      
  - name: "HighLatency"
    condition: "histogram_quantile(0.99, query_duration_seconds) > 1"
    severity: "warning"
    annotations:
      summary: "p99 latency above 1 second"
      
  - name: "BackendDown"
    condition: "up{backend=~".*"} == 0"
    severity: "critical"
    annotations:
      summary: "All backends are down"
```

#### Implementation Checklist

- [ ] Define Prometheus alert rules
- [ ] Add Alertmanager configuration
- [ ] Document alert response procedures
- [ ] Create on-call runbook
- [ ] Add PagerDuty/OpsGenie integration

---

### 5.2 Feature Flags System

**Priority:** P2 (Medium)  
**Estimated Effort:** 6-8 hours

#### Problem

```
# PRODUCTIONREADY.md:298
- [ ] Feature flags system — not present
```

#### Implementation Checklist

- [ ] Implement feature flag struct
- [ ] Add flag evaluation engine
- [ ] Add flags to config
- [ ] Add runtime flag toggle API
- [ ] Document flag naming convention

```go
type FeatureFlags struct {
    QueryCache       bool           `yaml:"query_cache"`
    ReadWriteSplit   bool           `yaml:"read_write_split"`
    StatementPooling bool           `yaml:"statement_pooling"`
}

func (f *FeatureFlags) Enabled(name string) bool {
    v := reflect.ValueOf(*f).FieldByName(name)
    return v.IsValid() && v.Bool()
}
```

---

### 5.3 Kubernetes Operator

**Priority:** P3 (Future)  
**Estimated Effort:** 40-60 hours

#### Long-term roadmap item

```
# ROADMAP.md:111
- [ ] Kubernetes operator for automated deployment and management
```

#### Operator Features

- [ ] CRD for GeryonPool
- [ ] Controller for pool lifecycle
- [ ] Admission webhook for validation
- [ ] Helm chart
- [ ] ArgoCD integration

---

### 5.4 CI/CD Improvements

**Priority:** P1 (High)  
**Estimated Effort:** 4-6 hours

#### Problem

```
# GAP-ANALYSIS.md:132
CI configuration bug — CI Go versions should match or exceed go.mod version
```

#### Implementation Checklist

- [ ] Fix CI Go version matrix
- [ ] Add integration tests to CI
- [ ] Add performance regression tests
- [ ] Add security scanning to CI
- [ ] Add SBOM generation

---

## Implementation Timeline

```
Week 1-2: Phase 1 (Critical)
├── 1.1 Distributed Tracing (8-12h)
└── 1.2 Request Logging (4-6h)

Week 2-4: Phase 2 (Security)
├── 2.1 MSSQL NTLM Auth (16-24h)
├── 2.2 gRPC API Completion (12-16h)
├── 2.3 Dashboard XSS Protection (4-6h)
└── 2.4 CORS Configuration (2-4h)

Week 4-6: Phase 3 (Clustering)
├── 3.1 SWIM Timing Bug Fix (2-4h)
├── 3.2 Cluster HA Improvements (8-12h)
└── 3.3 Raft Backup/Restore (8-12h)

Week 6-8: Phase 4 (Testing/Docs)
├── 4.1 Load Testing (8-12h)
├── 4.2 OpenAPI Documentation (6-8h)
└── 4.3 Chaos Testing (4-6h)

Week 8-10: Phase 5 (Production)
├── 5.1 Alerting & Monitoring (8-12h)
├── 5.2 Feature Flags (6-8h)
└── 5.4 CI/CD Improvements (4-6h)

Week 10+: Future
├── 5.3 Kubernetes Operator (40-60h)
└── Long-term roadmap items
```

---

## Resource Requirements

| Phase | Hours | Skills Required |
|-------|-------|-----------------|
| Phase 1 | 12-18h | Go, observability |
| Phase 2 | 34-50h | Go, Windows auth, security |
| Phase 3 | 18-28h | Go, distributed systems |
| Phase 4 | 18-26h | Go, testing, documentation |
| Phase 5 | 18-26h | Go, DevOps, Kubernetes |
| **Total** | **100-148h** | |

---

## Definition of Done

For Geryon to be considered v1.0.0 production-ready:

- [ ] All P0 and P1 items completed
- [ ] Load tests passing with SLOs met
- [ ] Security audit passed (no critical/high findings)
- [ ] OpenAPI documentation complete
- [ ] Chaos tests passing
- [ ] Alert rules documented and tested
- [ ] Backup/restore tested
- [ ] Performance regression tests in CI

---

## Appendix A: Issue References

| Issue | Description | Source |
|-------|-------------|--------|
| MSSQL-NTLM | MSSQL NTLM Authentication incomplete | `mssql_test.go:339` |
| SWIM-TIMING | SWIM probe timing bug | `ANALYSIS.md:590` |
| NO-CORR-ID | Correlation IDs not implemented | `PRODUCTIONREADY.md:275` |
| NO-TRACE | Distributed tracing not implemented | `PRODUCTIONREADY.md:274` |
| GRPC-NI | gRPC API not implemented | `SPECIFICATION.md:457` |
| NO-OPENAPI | OpenAPI spec missing | `PRODUCTIONREADY.md:318` |
| XSS-VERIFY | XSS protection not verified | `PRODUCTIONREADY.md:143` |
| NO-CORS | CORS headers not configured | `PRODUCTIONREADY.md:156` |
| CI-VERSION | CI Go version mismatch | `GAP-ANALYSIS.md:132` |

---

## Appendix B: Configuration Templates

### Distributed Tracing Configuration

```yaml
# config/tracing.yaml
tracing:
  enabled: true
  exporter: "otlpgrpc"
  endpoint: "localhost:4317"
  sampling_rate: 0.1
  service_name: "geryon-proxy"
  propagators:
    - "traceparent"
    - "tracestate"
    - "b3"
```

### Security Headers Configuration

```yaml
# config/security.yaml
security:
  headers:
    "X-Frame-Options": "DENY"
    "X-Content-Type-Options": "nosniff"
    "X-XSS-Protection": "1; mode=block"
    "Content-Security-Policy": "default-src 'self'"
    "Referrer-Policy": "strict-origin-when-cross-origin"
  cors:
    allowed_origins:
      - "https://dashboard.geryon.dev"
    allowed_methods:
      - "GET"
      - "POST"
      - "PUT"
      - "DELETE"
    allowed_headers:
      - "Authorization"
      - "Content-Type"
      - "X-Request-ID"
    max_age: "1h"
```

### Alert Rules Configuration

```yaml
# config/alerts.yaml
alerts:
  prometheus:
    rules:
      - alert: HighErrorRate
        expr: rate(geryon_errors_total[5m]) > 0.01
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Error rate exceeds 1%"
          
      - alert: PoolExhaustion
        expr: geryon_pool_wait_queue_length > 0.9 * geryon_pool_max_client_connections
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "Connection pool near capacity"
          
      - alert: HighLatency
        expr: histogram_quantile(0.99, rate(geryon_query_duration_seconds_bucket[5m])) > 1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "p99 latency exceeds 1 second"
          
      - alert: BackendDown
        expr: geryon_backend_up == 0
        for: 30s
        labels:
          severity: critical
        annotations:
          summary: "Backend is down"
```

---

## Appendix C: Testing Strategy

### Unit Tests
```bash
go test -short -race ./...
```

### Integration Tests
```bash
# Requires running databases
docker-compose up -d postgres mysql mssql
go test -v ./integration-tests/
```

### Load Tests
```bash
# Using k6
k6 run scenarios/load-test.js
```

### Chaos Tests
```bash
# Network partition
go test -v -run TestChaos ./integration-tests/chaos_test.go
```

### Security Tests
```bash
# Run gosec
gosec -exclude=G115,G401,G104,G304,G301,G302,G306,G501,G505 ./...

# Run mutation tests
go-mutesting ./...
```

---

## Appendix D: Detailed Technical Specifications

### D.1 Correlation ID Implementation

#### File: `internal/proxy/listener.go`

```go
// Add to Relay struct
type Relay struct {
    // ... existing fields ...
    correlationID string
    traceSpan     trace.Span
}

// Add middleware function
func (r *Relay) withCorrelationID(ctx context.Context) context.Context {
    if r.correlationID == "" {
        r.correlationID = generateCorrelationID()
    }
    return context.WithValue(ctx, correlationIDKey{}, r.correlationID)
}

func generateCorrelationID() string {
    var id [16]byte
    if _, err := rand.Read(id[:]); err != nil {
        return fmt.Sprintf("unknown-%d", time.Now().UnixNano())
    }
    return fmt.Sprintf("%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:])
}
```

#### File: `internal/logger/logger.go`

```go
// Add correlation ID to all log output
func (l *Logger) LogWithCorrelationID(ctx context.Context, msg string, args ...any) {
    if corrID, ok := ctx.Value(correlationIDKey{}).(string); ok {
        args = append(args, "correlation_id", corrID)
    }
    l.Info(msg, args...)
}
```

### D.2 OpenTelemetry Integration

#### File: `internal/tracing/tracing.go` (NEW)

```go
package tracing

import (
    "context"
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlpgrpc"
    "go.opentelemetry.io/otel/propagation"
    "go.opentelemetry.io/otel/sdk/resource"
    semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

type Config struct {
    Enabled      bool
    Exporter     string  // "otlpgrpc" | "jaeger" | "zipkin"
    Endpoint     string
    SamplingRate float64
    ServiceName  string
}

func NewTracerProvider(ctx context.Context, cfg *Config) (*tracesdk.Provider, error) {
    exporter, err := newExporter(ctx, cfg)
    if err != nil {
        return nil, err
    }

    res, err := resource.New(ctx,
        resource.WithAttributes(
            semconv.ServiceName(cfg.ServiceName),
        ),
    )
    if err != nil {
        return nil, err
    }

    tp := tracesdk.NewTracerProvider(
        tracesdk.WithBatcher(exporter),
        tracesdk.WithResource(res),
        tracesdk.WithSampler(tracesdk.ParentBased(
            tracesdk.TraceIDRatioBased(cfg.SamplingRate),
        )),
    )

    otel.SetTracerProvider(tp)
    otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
        propagation.TraceContext{},
        propagation.Baggage{},
    ))

    return tp, nil
}
```

### D.3 MSSQL NTLM Authentication

#### File: `internal/protocol/mssql/ntlm.go` (NEW)

```go
package mssql

import (
    "bytes"
    "encoding/binary"
)

// NTLM Message Types
const (
    NTLM_NEGOTIATE = 1
    NTLM_CHALLENGE = 2
    NTLM_AUTHENTICATE = 3
)

// NTLM Message Structure
type NTLMMessage struct {
    Signature      [8]byte
    MessageType    uint32
    NegotiateFlags uint32
    Domain         []byte
    User           []byte
    Host           []byte
    Version        [8]byte
    Cchallenge     []byte
    CchallengeRes  []byte // Contains auth payload
}

// BuildNegotiate creates NTLM negotiate message
func BuildNegotiate(domain, workstation string) ([]byte, error) {
    // ... implementation
}

// ProcessChallenge handles NTLM challenge from server
func ProcessChallenge(challenge []byte) (*NTLMMessage, error) {
    // ... implementation
}

// BuildAuthenticate creates NTLM authenticate message
func BuildAuthenticate(domain, username, password, workstation string, challenge []byte) ([]byte, error) {
    // ... implementation using NTLM hash functions
}
```

### D.4 gRPC Proto Definitions

#### File: `api/v1/admin.proto`

```protobuf
syntax = "proto3";

package geryon.admin.v1;

import "google/protobuf/empty.proto";
import "google/protobuf/timestamp.proto";

option go_package = "github.com/geryon/geryon/api/v1/admin";

service AdminService {
  // Stream real-time statistics
  rpc StreamStats(StreamStatsRequest) returns (stream StatsResponse);
  
  // Pool management
  rpc CreatePool(CreatePoolRequest) returns (PoolResponse);
  rpc UpdatePool(UpdatePoolRequest) returns (PoolResponse);
  rpc GetPool(GetPoolRequest) returns (PoolResponse);
  rpc DeletePool(DeletePoolRequest) returns (google.protobuf.Empty);
  rpc ListPools(ListPoolsRequest) returns (ListPoolsResponse);
  
  // Backend management
  rpc AddBackend(AddBackendRequest) returns (PoolResponse);
  rpc RemoveBackend(RemoveBackendRequest) returns (PoolResponse);
  
  // Cluster management
  rpc GetClusterStatus(GetClusterStatusRequest) returns (ClusterStatus);
  rpc JoinCluster(JoinClusterRequest) returns (JoinClusterResponse);
  rpc LeaveCluster(LeaveClusterRequest) returns (google.protobuf.Empty);
  
  // Configuration
  rpc ReloadConfig(ReloadConfigRequest) returns (ReloadConfigResponse);
}

message StreamStatsRequest {
  repeated string pools = 1;  // Empty = all pools
  int64 interval_ms = 2;
}

message StatsResponse {
  string pool_name = 1;
  PoolStats stats = 2;
  google.protobuf.Timestamp timestamp = 3;
}

message PoolStats {
  int64 connections_active = 1;
  int64 connections_idle = 2;
  int64 wait_queue_length = 3;
  int64 queries_total = 4;
  double query_duration_p99_ms = 5;
  int64 errors_total = 6;
}

message PoolResponse {
  bool success = 1;
  string error = 2;
  Pool pool = 3;
}

message Pool {
  string name = 1;
  string body = 2;           // postgresql | mysql | mssql
  string mode = 3;           // session | transaction | statement
  ListenConfig listen = 4;
  BackendConfig backend = 5;
  LimitsConfig limits = 6;
}

message ListenConfig {
  string host = 1;
  int32 port = 2;
}

message BackendConfig {
  repeated BackendHost hosts = 1;
  string database = 2;
  BackendAuth auth = 3;
}

message BackendHost {
  string host = 1;
  int32 port = 2;
  string role = 3;           // primary | replica
}

message BackendAuth {
  string username = 1;
  string password = 2;
}

message LimitsConfig {
  int64 max_client_connections = 1;
  int64 max_server_connections = 2;
  int64 min_server_connections = 3;
  string connection_timeout = 4;
  string query_timeout = 5;
}

// ... additional message definitions
```

---

## Appendix E: Task Breakdown

### Phase 1 Tasks

| Task ID | Description | File(s) | Effort | Dependencies |
|---------|-------------|---------|--------|---------------|
| T001 | Add correlation ID struct field | `internal/proxy/listener.go` | 1h | - |
| T002 | Implement correlation ID generation | `internal/proxy/listener.go` | 1h | T001 |
| T003 | Add correlation ID to log context | `internal/logger/` | 2h | T001 |
| T004 | Create tracing package | `internal/tracing/` | 3h | - |
| T005 | Integrate OpenTelemetry SDK | `cmd/geryon/main.go` | 2h | T004 |
| T006 | Instrument proxy relay | `internal/proxy/listener.go` | 2h | T004 |
| T007 | Add trace context propagation | `internal/proxy/listener.go` | 1h | T004 |
| T008 | Request logging middleware | `internal/api/rest/` | 2h | T001 |
| T009 | Query logging in relay | `internal/proxy/listener.go` | 2h | T001 |
| T010 | Slow query logger | `internal/logger/querylog.go` | 1h | - |

### Phase 2 Tasks

| Task ID | Description | File(s) | Effort | Dependencies |
|---------|-------------|---------|--------|---------------|
| T020 | Implement NTLM message types | `internal/protocol/mssql/ntlm.go` | 4h | - |
| T021 | NTLM negotiation handler | `internal/protocol/mssql/` | 4h | T020 |
| T022 | NTLM challenge/response | `internal/protocol/mssql/` | 4h | T021 |
| T023 | Add NTLM integration test | `integration-tests/mssql_test.go` | 2h | T022 |
| T024 | Define gRPC proto schemas | `api/v1/admin.proto` | 2h | - |
| T025 | Generate Go from proto | `api/v1/` | 1h | T024 |
| T026 | Implement gRPC AdminService | `internal/api/grpc/` | 4h | T025 |
| T027 | Implement gRPC PoolService | `internal/api/grpc/` | 4h | T025 |
| T028 | Implement gRPC ClusterService | `internal/api/grpc/` | 4h | T025 |
| T029 | Add gRPC auth middleware | `internal/api/grpc/` | 2h | T026 |
| T030 | Security headers in REST | `internal/api/rest/server.go` | 1h | - |
| T031 | Security headers in gRPC | `internal/api/grpc/` | 1h | - |
| T032 | CSP headers for dashboard | `internal/api/dashboard/` | 1h | - |
| T033 | XSS audit dashboard | `internal/api/dashboard/` | 2h | - |
| T034 | CORS middleware | `internal/api/rest/` | 2h | - |
| T035 | CORS config | `internal/config/` | 1h | T034 |

### Phase 3 Tasks

| Task ID | Description | File(s) | Effort | Dependencies |
|---------|-------------|---------|--------|---------------|
| T040 | Add retry to SWIM probe | `internal/swim/` | 1h | - |
| T041 | Fix timing-dependent test | `internal/swim/swim_test.go` | 1h | T040 |
| T042 | Leader lease mechanism | `internal/raft/` | 3h | - |
| T043 | Consensus verification | `internal/raft/` | 2h | T042 |
| T044 | Cluster health monitor | `internal/cluster/` | 3h | - |
| T045 | Leader handoff | `internal/raft/` | 2h | T042 |
| T046 | Split-brain detection | `internal/cluster/` | 2h | T044 |
| T047 | Raft snapshot export | `internal/raft/snapshot.go` | 3h | - |
| T048 | Raft WAL export | `internal/raft/wal.go` | 3h | - |
| T049 | Restore command | `cmd/geryon/` | 2h | T047, T048 |

### Phase 4 Tasks

| Task ID | Description | File(s) | Effort | Dependencies |
|---------|-------------|---------|--------|---------------|
| T050 | Setup k6 | `scenarios/` | 1h | - |
| T051 | Load test scenarios | `scenarios/` | 4h | T050 |
| T052 | SLO definitions | `scenarios/` | 1h | T051 |
| T053 | Load test in CI | `.github/` | 2h | T051 |
| T054 | OpenAPI spec | `api/openapi.yaml` | 3h | - |
| T055 | Swagger UI endpoint | `internal/api/dashboard/` | 2h | T054 |
| T056 | Error code documentation | `docs/` | 1h | - |
| T057 | Network partition test | `integration-tests/chaos_test.go` | 1h | - |
| T058 | Latency injection | `integration-tests/chaos_test.go` | 1h | - |
| T059 | Backend failure test | `integration-tests/chaos_test.go` | 1h | - |

### Phase 5 Tasks

| Task ID | Description | File(s) | Effort | Dependencies |
|---------|-------------|---------|--------|---------------|
| T060 | Prometheus alert rules | `config/alerts.yml` | 2h | - |
| T061 | Alertmanager config | `config/alertmanager.yml` | 1h | T060 |
| T062 | On-call runbook | `docs/runbook.md` | 3h | T060 |
| T063 | Feature flag struct | `internal/config/` | 2h | - |
| T064 | Flag evaluation engine | `internal/config/` | 2h | T063 |
| T065 | Runtime flag API | `internal/api/rest/` | 2h | T064 |
| T066 | Fix CI Go versions | `.github/workflows/` | 1h | - |
| T067 | Integration tests in CI | `.github/workflows/` | 2h | - |
| T068 | SBOM generation | `.github/workflows/` | 1h | - |

---

## Appendix F: Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| NTLM implementation complexity | High | High | Use existing Go NTLM library |
| SWIM test flakiness persists | Medium | Medium | Increase timeout and add retries |
| OpenTelemetry breaking changes | Low | Medium | Pin to specific version |
| Performance regression from tracing | Medium | Medium | Use sampling, measure impact |
| gRPC backward compatibility | Low | High | Version API from start |

---

## Appendix G: Definition of Terms

| Term | Definition |
|------|------------|
| **Correlation ID** | Unique identifier for request tracing across services |
| **traceparent** | W3C Trace Context header for distributed tracing |
| **SWIM** | Scalable Weakly-consistent Infection-style Membership protocol |
| **NTLM** | NT LAN Manager authentication protocol |
| **SSPI** | Security Support Provider Interface (Windows) |
| **SLO** | Service Level Objective |
| **SBOM** | Software Bill of Materials |

---

## Appendix H: Implementation Guide

### H.1 Adding Correlation IDs to Existing Code

#### Step 1: Add to context package

```go
// internal/context/correlation.go
package context

type correlationIDKey struct{}

func WithCorrelationID(ctx context.Context, id string) context.Context {
    return context.WithValue(ctx, correlationIDKey{}, id)
}

func CorrelationID(ctx context.Context) string {
    if id, ok := ctx.Value(correlationIDKey{}).(string); ok {
        return id
    }
    return ""
}
```

#### Step 2: Modify proxy listener

```go
// internal/proxy/listener.go

func (r *Relay) handleConnection(ctx context.Context, clientConn net.Conn) {
    // Generate or extract correlation ID
    corrID := r.extractOrGenerateCorrelationID(clientConn)
    ctx = context.WithCorrelationID(ctx, corrID)
    r.correlationID = corrID

    // Log with correlation ID
    r.log.Info("connection started",
        "correlation_id", corrID,
        "client_addr", clientConn.RemoteAddr())
}

func (r *Relay) extractOrGenerateCorrelationID(conn net.Conn) string {
    // Try to read X-Request-ID from startup packet
    if r.body == "postgresql" {
        if reqID := r.pgReadRequestID(conn); reqID != "" {
            return reqID
        }
    }
    // Generate new ID
    var id [16]byte
    rand.Read(id[:])
    return fmt.Sprintf("%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:])
}
```

#### Step 3: Update logger to include correlation ID

```go
// Every log call should include correlation ID
func (r *Relay) logQuery(ctx context.Context, query string, duration time.Duration) {
    r.log.Info("query executed",
        "correlation_id", context.CorrelationID(ctx),
        "query", sanitizeQuery(query),
        "duration_ms", duration.Milliseconds())
}
```

### H.2 OpenTelemetry Integration Steps

#### Step 1: Add dependencies

```bash
go get go.opentelemetry.io/otel \
    go.opentelemetry.io/otel/exporters/otlp/otlpgrpc \
    go.opentelemetry.io/otel/sdk \
    go.opentelemetry.io/otel/propagation \
    go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc
```

#### Step 2: Initialize tracer in main.go

```go
// cmd/geryon/main.go

func main() {
    ctx := context.Background()
    
    // Initialize tracing if configured
    if cfg.Tracing.Enabled {
        tp, err := tracing.NewTracerProvider(ctx, &cfg.Tracing)
        if err != nil {
            log.Fatal("failed to create tracer provider", err)
        }
        defer tp.Shutdown(ctx)
    }
    
    // ... rest of main
}
```

#### Step 3: Instrument gRPC server

```go
// internal/api/grpc/server.go

import "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

func NewServer(cfg *Config) *Server {
    grpcServer := grpc.NewServer(
        grpc.UnaryInterceptor(otelgrpc.UnaryServerInterceptor()),
        grpc.StreamInterceptor(otelgrpc.StreamServerInterceptor()),
    )
    // ... rest of setup
}
```

### H.3 NTLM Authentication Flow

```
1. Client sends TDS Prelogin message (or login payload)
2. Server responds with authentication mechanism = NTLM
3. Client sends NTLM NEGOTIATE message
4. Server sends NTLM CHALLENGE message (with 8-byte challenge)
5. Client sends NTLM AUTHENTICATE message (with computed response)
6. Server validates and accepts/rejects
```

#### Key Implementation Notes

- NTLM uses MD4 hash of password (not salted)
- Response includes NTLM v2 hash with client challenge
- Workstation and domain are included in response computation
- Windows SSPI API handles all this on Windows, but we need pure Go for cross-platform

#### NTLM Hash Computation

```go
// MD4 hash (Go doesn't have native MD4, use md4golang or implement)
func NTLMHash(password string) []byte {
    // Convert password to UTF-16LE
    utf16 := utf16.Encode([]rune(password))
    // Return MD4 hash
    return md4.Sum(utf16leToBytes(utf16))
}

// NTLMv2 Response
func NTLMv2Response(password, domain, username string, serverChallenge []byte) []byte {
    // Build NTLMv2 blob with client challenge
    blob := buildNTLMv2Blob(domain, username, serverChallenge)
    // HMAC_MD5(NTLM hash, blob)
    return hmacMD5(NTLMHash(password), blob)
}
```

### H.4 SWIM Probe Retry Logic

#### Current Implementation (Timing-dependent)

```go
// internal/swim/swim.go (CURRENT - BUGGY)
func (p *Protocol) probe(target string) error {
    deadline := time.Now().Add(100 * time.Millisecond)
    for time.Now().Before(deadline) {
        if err := p.ping(target); err == nil {
            return nil
        }
        time.Sleep(10 * time.Millisecond)
    }
    return fmt.Errorf("probe timeout")
}
```

#### Fixed Implementation (With Retries)

```go
// internal/swim/swim.go (FIXED)
func (p *Protocol) probe(target string) error {
    const (
        maxRetries   = 3
        baseInterval = 50 * time.Millisecond
        maxInterval  = 200 * time.Millisecond
    )

    var lastErr error
    interval := baseInterval

    for attempt := 0; attempt < maxRetries; attempt++ {
        if attempt > 0 {
            // Add jitter to avoid thundering herd
            jitter := time.Duration(rand.Int63n(int64(interval / 2)))
            time.Sleep(interval + jitter)
            interval *= 2
            if interval > maxInterval {
                interval = maxInterval
            }
        }

        if err := p.pingOnce(target); err == nil {
            return nil
        } else {
            lastErr = err
            p.log.Debug("probe attempt failed",
                "target", target,
                "attempt", attempt+1,
                "error", err)
        }
    }

    return fmt.Errorf("probe failed after %d attempts: %w", maxRetries, lastErr)
}

func (p *Protocol) pingOnce(target string) error {
    // Single ping attempt with its own short timeout
    ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
    defer cancel()

    errCh := make(chan error, 1)
    go func() {
        errCh <- p.sendPing(target)
    }()

    select {
    case <-ctx.Done():
        return ctx.Err()
    case err := <-errCh:
        return err
    }
}
```

### H.5 Alert Rule Implementation

#### File: `config/prometheus/alerts.yml`

```yaml
groups:
  - name: geryon
    rules:
      # High Error Rate
      - alert: GeryonHighErrorRate
        expr: rate(geryon_errors_total[5m]) > 0.01
        for: 5m
        labels:
          severity: critical
          component: proxy
        annotations:
          summary: "Geryon error rate exceeds 1%"
          description: "Error rate is {{ $value | humanizePercentage }} over the last 5 minutes"
          runbook_url: "https://docs.geryon.dev/runbooks/high-error-rate"

      # Pool Exhaustion Warning
      - alert: GeryonPoolNearCapacity
        expr: geryon_pool_wait_queue_length / geryon_pool_max_client_connections > 0.8
        for: 1m
        labels:
          severity: warning
          component: pool
        annotations:
          summary: "Connection pool {{ $labels.pool }} near capacity"
          description: "Wait queue length is {{ $value }} (80% of max)"
          runbook_url: "https://docs.geryon.dev/runbooks/pool-exhaustion"

      # High Latency
      - alert: GeryonHighLatency
        expr: histogram_quantile(0.99, rate(geryon_query_duration_seconds_bucket[5m])) > 1
        for: 5m
        labels:
          severity: warning
          component: proxy
        annotations:
          summary: "p99 latency exceeds 1 second for pool {{ $labels.pool }}"
          description: "p99 latency is {{ $value | humanizeDuration }}"
          runbook_url: "https://docs.geryon.dev/runbooks/high-latency"

      # Backend Down
      - alert: GeryonBackendDown
        expr: geryon_backend_up == 0
        for: 30s
        labels:
          severity: critical
          component: backend
        annotations:
          summary: "Backend {{ $labels.backend }} is down"
          description: "Health check failed for {{ $labels.backend }} in pool {{ $labels.pool }}"
          runbook_url: "https://docs.geryon.dev/runbooks/backend-down"

      # No Healthy Backends
      - alert: GeryonNoHealthyBackends
        expr: sum by (pool) (geryon_backend_up) == 0
        for: 30s
        labels:
          severity: critical
          component: pool
        annotations:
          summary: "No healthy backends for pool {{ $labels.pool }}"
          description: "All backends in pool {{ $labels.pool }} are unhealthy"
          runbook_url: "https://docs.geryon.dev/runbooks/no-healthy-backends"

      # Connection Leak
      - alert: GeryonConnectionLeak
        expr: increase(geryon_connections_total{action="created"}[1h]) - increase(geryon_connections_total{action="closed"}[1h]) > 10
        for: 5m
        labels:
          severity: warning
          component: pool
        annotations:
          summary: "Possible connection leak in pool {{ $labels.pool }}"
          description: "More connections created than closed in the last hour"
          runbook_url: "https://docs.geryon.dev/runbooks/connection-leak"

      # Cluster Leader Loss
      - alert: GeryonClusterLeaderLoss
        expr: geryon_cluster_leader == 0
        for: 10s
        labels:
          severity: critical
          component: cluster
        annotations:
          summary: "Geryon cluster has no leader"
          description: "Raft leader election failed or cluster is partitioned"
          runbook_url: "https://docs.geryon.dev/runbooks/leader-loss"
```

---

## Appendix I: File Change Summary

This section summarizes the files that need to be created or modified for each phase.

### Phase 1: Observability

| Action | File | Changes |
|--------|------|---------|
| CREATE | `internal/context/correlation.go` | New file: correlation ID context helpers |
| MODIFY | `internal/proxy/listener.go` | Add correlation ID to Relay struct |
| MODIFY | `internal/proxy/listener.go` | Instrument with trace spans |
| MODIFY | `internal/logger/logger.go` | Add correlation ID to all log calls |
| CREATE | `internal/tracing/tracing.go` | New file: OpenTelemetry setup |
| CREATE | `internal/tracing/config.go` | New file: tracing configuration |
| MODIFY | `cmd/geryon/main.go` | Initialize tracer provider |

### Phase 2: Security

| Action | File | Changes |
|--------|------|---------|
| CREATE | `internal/protocol/mssql/ntlm.go` | New file: NTLM message handling |
| MODIFY | `internal/protocol/mssql/codec.go` | Add NTLM auth flow |
| CREATE | `api/v1/admin.proto` | New file: gRPC service definitions |
| CREATE | `api/v1/admin.pb.go` | Generated from proto |
| CREATE | `api/v1/admin_grpc.go` | Generated from proto |
| MODIFY | `internal/api/grpc/server.go` | Implement gRPC services |
| MODIFY | `internal/api/rest/server.go` | Add security headers |
| MODIFY | `internal/api/dashboard/server.go` | Add CSP headers |

### Phase 3: Clustering

| Action | File | Changes |
|--------|------|---------|
| MODIFY | `internal/swim/swim.go` | Add retry with backoff |
| MODIFY | `internal/swim/swim_test.go` | Fix timing-dependent test |
| MODIFY | `internal/raft/raft.go` | Add leader lease |
| CREATE | `internal/raft/backup.go` | New file: snapshot/WAL export |
| MODIFY | `cmd/geryon/main.go` | Add backup/restore commands |

### Phase 4: Testing

| Action | File | Changes |
|--------|------|---------|
| CREATE | `scenarios/load-test.js` | New file: k6 load test |
| CREATE | `config/k6/` | New directory: k6 configuration |
| CREATE | `api/openapi.yaml` | New file: OpenAPI 3.0 spec |
| MODIFY | `integration-tests/chaos_test.go` | Add chaos scenarios |
| CREATE | `docs/runbook.md` | New file: on-call runbook |

### Phase 5: Production

| Action | File | Changes |
|--------|------|---------|
| CREATE | `config/prometheus/alerts.yml` | New file: alert rules |
| CREATE | `config/alertmanager/` | New directory: alertmanager config |
| MODIFY | `internal/config/config.go` | Add feature flags |
| MODIFY | `.github/workflows/ci.yml` | Fix Go version, add tests |

---

## Appendix J: Milestone Checklist

### Milestone 1: Tracing Foundation (End of Week 1)
- [ ] Correlation ID generation implemented
- [ ] Correlation ID propagation through proxy
- [ ] Correlation ID in all log statements
- [ ] OpenTelemetry tracer initialized
- [ ] Trace spans in proxy relay

### Milestone 2: Security Baseline (End of Week 2)
- [ ] Request logging with correlation ID
- [ ] Slow query logging
- [ ] Security headers on REST API
- [ ] Security headers on dashboard
- [ ] CORS configuration

### Milestone 3: NTLM & gRPC (End of Week 4)
- [ ] NTLM authentication for MSSQL
- [ ] NTLM integration test passing
- [ ] gRPC proto definitions complete
- [ ] gRPC AdminService implemented
- [ ] gRPC PoolService implemented
- [ ] gRPC ClusterService implemented

### Milestone 4: Cluster Reliability (End of Week 6)
- [ ] SWIM timing bug fixed
- [ ] SWIM tests passing reliably
- [ ] Leader lease mechanism working
- [ ] Cluster health monitoring
- [ ] Raft backup implemented
- [ ] Raft restore tested

### Milestone 5: Testing & Docs (End of Week 8)
- [ ] Load test scenarios defined
- [ ] Load tests passing SLOs
- [ ] OpenAPI spec complete
- [ ] Swagger UI working
- [ ] Chaos tests passing
- [ ] Runbook documented

### Milestone 6: Production Release (End of Week 10)
- [ ] All P0/P1 items complete
- [ ] Alert rules deployed
- [ ] Feature flags implemented
- [ ] CI/CD improved
- [ ] Security audit passed
- [ ] v1.0.0 release cut

---

## Appendix K: Monitoring & Runbook

### K.1 Key Metrics to Monitor

```yaml
# Critical metrics for production
critical_metrics:
  # Error tracking
  - geryon_errors_total                    # All errors
  - geryon_pool_wait_queue_length          # Clients waiting
  - geryon_backend_up                      # Backend health

  # Latency
  - geryon_query_duration_seconds          # Query latency histogram
  - geryon_connection_acquire_seconds      # Pool acquire time

  # Throughput
  - geryon_queries_total                   # Queries per second
  - geryon_pool_connections_active         # Active connections

  # Cluster
  - geryon_cluster_leader                  # 1 if leader exists
  - geryon_raft_log_entries                # Raft log size
```

### K.2 On-Call Response Procedures

#### High Error Rate

```
1. Check which pool is affected:     geryon_pool_errors_total{pool="?"}
2. Check backend health:             geryon_backend_up{pool="?"}
3. If backend down:                  Skip to "Backend Down" procedure
4. Check query latency:              histogram_quantile(0.99, rate(...))
5. Check wait queue:                 geryon_pool_wait_queue_length
6. If queue high:                    Scale backend or increase pool size
7. Check recent config changes:      git log --since="1 hour ago"
8. Consider rolling back if needed
```

#### Backend Down

```
1. Identify affected pool:           geryon_pool_backend_up{pool="?"} == 0
2. Check backend connectivity:       telnet <backend-host> <port>
3. Verify backend service:           systemctl status <db-service>
4. If backend issue:                 Fix backend first
5. If network issue:                 Check network/path
6. After backend restored:           Verify geryon_backend_up == 1
7. Check error rate returns to normal
```

#### Pool Exhaustion

```
1. Identify affected pool:           geryon_pool_wait_queue_length > 0
2. Check current usage:              geryon_pool_connections_active / max
3. Check backend capacity:           SHOW max_connections ON backend
4. Options:
   a. Increase max_server_connections (hot reload)
   b. Add more backends (read replicas)
   c. Reduce query_timeout
   d. Scale horizontally (add more geryon nodes)
5. Monitor queue until normal
```

#### Leader Election Failure

```
1. Check cluster status:             geryon_cluster_leader
2. If no leader for >30s:            Investigate network partition
3. Check all nodes reachable:         ping <node-ips>
4. Verify Raft state:                curl :8080/api/v1/cluster
5. If split-brain suspected:         Stop writes to prevent divergence
6. Force re-election:                DELETE raft/state (last resort)
7. Review SWIM health:               Check node membership
```

### K.3 Log Analysis

```bash
# Find errors in last hour
grep -E "ERROR|error" /var/log/geryon/*.log | grep "$(date -d '1 hour ago' +%Y-%m-%dT%H)"

# Find slow queries (>1s)
grep "duration_ms.*[0-9]{4,}" /var/log/geryon/query.log

# Find correlation IDs for specific error
grep "error\|ERROR" /var/log/geryon/*.log | grep "corr-id-here"

# Connection statistics
grep "connection" /var/log/geryon/*.log | awk '{print $NF}' | sort | uniq -c | sort -rn
```

---

## Appendix L: Performance Tuning Guide

### L.1 Pool Sizing

#### Calculate Optimal Pool Size

```
Optimal max_server_connections = (2 * CPU_CORES) + effective_spindles

For SSDs (no spindle benefit):
  = 2 * CPU_CORES
```

#### Example

```yaml
# 8-core server with 2 backend DBs (4 connections each for OS)
pools:
  - name: "main"
    limits:
      max_server_connections: 12   # (2 * 8) - 4 = 12
      min_server_connections: 4    # Keep warm
```

### L.2 Query Timeout Tuning

```yaml
# Aggressive timeouts for OLTP
limits:
  query_timeout: "5s"
  connection_timeout: "1s"
  idle_timeout: "60s"

# Relaxed for OLAP
limits:
  query_timeout: "300s"
  connection_timeout: "10s"
  idle_timeout: "3600s"
```

### L.3 Memory Tuning

```yaml
# For query cache (LRU with TTL)
cache:
  enabled: true
  max_memory: "1GB"
  max_entry_memory: "10MB"
  ttl: "5m"

# For prepared statements
stmt_cache:
  max_statements: 1000
  max_memory: "100MB"
```

### L.4 Health Check Tuning

```yaml
# Aggressive health checks
health:
  interval: "5s"          # Check every 5s
  timeout: "1s"            # 1s timeout
  retries: 2               # 2 retries before marking unhealthy

# Conservative for flaky backends
health:
  interval: "30s"
  timeout: "5s"
  retries: 5
```

### L.5 Performance Benchmarks

Expected performance on 8-core, 16GB RAM server:

| Pool Mode | Max Connections | Throughput (qps) | p99 Latency |
|-----------|-----------------|------------------|-------------|
| Session   | 100             | 15,000-25,000    | 2-5ms       |
| Transaction | 100           | 50,000-80,000    | 1-3ms       |
| Statement  | 100            | 100,000-150,000  | 0.5-2ms     |

### L.6 Bottleneck Identification

```bash
# High CPU
pprof_url: /debug/pprof/profile?seconds=30
look_for: "cpu" profile spikes

# High memory allocations
pprof_url: /debug/pprof/heap
look_for: "alloc_space" growing over time

# Blocking operations
pprof_url: /debug/pprof/block
look_for: "mutex" contention

# Goroutine leaks
pprof_url: /debug/pprof/goroutine?debug=1
look_for: goroutine count increasing over time
```

---

## Appendix M: Security Hardening

### M.1 Network Security

```yaml
# Bind to internal interface only
pools:
  - listen:
      host: "127.0.0.1"    # or internal NIC IP

admin:
  rest:
    listen: "127.0.0.1:8080"
  grpc:
    listen: "127.0.0.1:9090"
```

### M.2 TLS Configuration

```yaml
# Enforce TLS for all connections
pools:
  - tls:
      mode: "require"      # require | prefer | disable

# Admin APIs
admin:
  rest:
    tls:
      enabled: true
      cert_file: "/etc/geryon/tls/server.crt"
      key_file: "/etc/geryon/tls/server.key"

# Mutual TLS (client certificates)
  client_auth: "require-and-verify-client-cert"
  ca_file: "/etc/geryon/tls/ca.crt"
```

### M.3 Authentication

```yaml
# Interception mode (proxy authenticates)
auth:
  mode: "interception"
  users:
    - username: "app"
      password_hash: "SCRAM-SHA-256$..."
      allowed_pools: ["main-pg"]

# Per-pool auth settings
pools:
  - name: "main-pg"
    backend:
      auth:
        username: "${BACKEND_USER}"
        password_file: "/etc/geryon/secrets/pg"
```

### M.4 Rate Limiting

```yaml
# Connection rate limiting
pools:
  - limits:
      max_client_connections: 1000
      # Client connection timeout
      connection_timeout: "5s"

# Auth rate limiting (built-in)
auth:
  rate_limit:
    enabled: true
    max_attempts: 5
    window: "5m"
    lockout: "15m"
```

### M.5 Security Headers

```yaml
security:
  headers:
    "X-Frame-Options": "DENY"
    "X-Content-Type-Options": "nosniff"
    "X-XSS-Protection": "1; mode=block"
    "Strict-Transport-Security": "max-age=31536000; includeSubDomains"
    "Content-Security-Policy": "default-src 'self'"
```

---

## Appendix N: Disaster Recovery

### N.1 Backup Procedures

#### Raft State Backup

```bash
# Manual snapshot
curl -X POST http://localhost:8080/api/v1/cluster/snapshot

# Automatic backup (cron)
0 2 * * * /usr/local/bin/geryon backup --output /backups/raft-$(date +\%Y\%m\%d)
```

#### Configuration Backup

```bash
# Backup config file
cp /etc/geryon/geryon.yaml /backups/config-$(date +\%Y\%m\%d).yaml
```

### N.2 Recovery Procedures

#### Full Recovery from Backup

```bash
# 1. Stop geryon
systemctl stop geryon

# 2. Restore Raft state
geryon restore --input /backups/raft-20260502

# 3. Restore config
cp /backups/config-20260502.yaml /etc/geryon/geryon.yaml

# 4. Start geryon
systemctl start geryon

# 5. Verify
curl http://localhost:8080/api/v1/health
```

#### Cluster Recovery

```bash
# If majority of nodes lost:
# 1. Identify surviving nodes
# 2. Force new cluster on most up-to-date node
geryon cluster init --force --node-id node1

# 3. Other nodes rejoin
geryon cluster join --address node1:7000
```

### N.3 RTO/RPO Targets

| Scenario | RTO | RPO |
|----------|-----|-----|
| Single node failure | < 30s | 0 |
| Full cluster failure | < 5min | < 1h (backup) |
| Data center outage | < 30min | < 1h |
| Disaster recovery | < 1h | < 1h (backup) |

---

## Appendix O: Version History

| Version | Date | Changes |
|---------|------|---------|
| 1.0.0-rc1 | TBD | First release candidate |
| 0.5.0 | 2026-05-02 | Current pre-release, security fixes applied |
| 0.4.0 | 2026-04-15 | Authentication, pooling modes |
| 0.3.0 | 2026-03-01 | Clustering (Raft + SWIM) |
| 0.2.0 | 2026-02-01 | Multi-protocol support |
| 0.1.0 | 2026-01-01 | Initial release |

---

## Appendix P: Contributing

### Code Standards

- Follow Go idioms (effective Go)
- All new code requires tests
- Run `go fmt` before commit
- Run `go vet` and fix warnings
- Use table-driven tests

### Commit Message Format

```
<type>(<scope>): <subject>

<body>

<footer>
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`

### Pull Request Process

1. Fork and create feature branch
2. Run all tests: `go test -race -short ./...`
3. Update documentation if needed
4. Submit PR with description
5. Address review feedback
6. Squash merge to master

### Code Review Checklist

- [ ] Tests pass and coverage maintained
- [ ] No race conditions
- [ ] Error handling complete
- [ ] No sensitive data in logs
- [ ] Configuration documented
- [ ] Backward compatibility maintained

---

## Appendix Q: Troubleshooting Guide

### Q.1 Common Issues

#### Connection Refused

```
Error: "dial tcp: connection refused"

Causes:
1. Geryon not running
   Solution: systemctl status geryon

2. Wrong port configured
   Solution: Check listen port in geryon.yaml

3. Firewall blocking
   Solution: iptables -L -n | grep 5432
```

#### Pool Exhaustion

```
Error: "pool: no available connections"

Causes:
1. Backend connections exhausted
   Solution: Increase max_server_connections

2. Queries taking too long
   Solution: Reduce query_timeout

3. Backend unresponsive
   Solution: Check backend health

4. Too many idle connections
   Solution: Reduce connection_timeout
```

#### Authentication Failures

```
Error: "authentication failed"

Causes:
1. Wrong credentials
   Solution: Verify password_hash in config

2. SCRAM-SHA-256 mismatch
   Solution: Regenerate password hash

3. Auth rate limited
   Solution: Wait for lockout to expire

Debug:
   grep "authentication" /var/log/geryon/*.log
```

#### High Latency

```
Error: "query timeout" or "connection timeout"

Diagnosis:
1. Check backend load:
   SELECT * FROM pg_stat_activity;

2. Check network latency:
   ping <backend-host>

3. Check query performance:
   EXPLAIN ANALYZE <slow-query>

Solutions:
1. Add read replicas for read queries
2. Optimize slow queries
3. Increase query_timeout temporarily
4. Scale backend resources
```

#### Cluster Issues

```
Error: "raft: leader not available"

Causes:
1. Network partition
   Solution: Check inter-node connectivity

2. Leader crashed
   Solution: Wait for election timeout

3. Majority lost
   Solution: Add more nodes or recover failed nodes

Debug:
   curl http://localhost:8080/api/v1/cluster/status
```

### Q.2 Debugging Tools

#### pprof Profiling

```bash
# CPU profile
go tool pprof http://localhost:8080/debug/pprof/profile?seconds=30

# Memory profile
go tool pprof http://localhost:8080/debug/pprof/heap

# Goroutine dump
curl http://localhost:8080/debug/pprof/goroutine?debug=1

# Mutex profiling
curl http://localhost:8080/debug/pprof/block?debug=1
```

#### Network Debugging

```bash
# TCP connections
ss -tlnp | grep geryon

# Connection states
cat /proc/net/tcp

# Socket statistics
netstat -s | grep -E "retransmit|timeout|error"
```

#### Database Debugging

```sql
-- PostgreSQL: Show blocking queries
SELECT pid, usename, pg_blocking_pids(pid) AS blocked_by,
       query, state, wait_event_type, wait_event
FROM pg_stat_activity
WHERE cardinality(pg_blocking_pids(pid)) > 0;

-- MySQL: Show thread states
SHOW PROCESSLIST;

-- MSSQL: Show blocked sessions
SELECT * FROM sys.dm_exec_requests WHERE blocking_session_id > 0;
```

### Q.3 Health Check Commands

```bash
# Check all pool health
curl http://localhost:8080/api/v1/health

# Check specific pool
curl http://localhost:8080/api/v1/pools/main-pg

# Check backend status
curl http://localhost:8080/api/v1/pools/main-pg/backends

# Check cluster status
curl http://localhost:9090/api/v1/cluster/status

# Prometheus metrics
curl http://localhost:8080/metrics
```

---

## Appendix R: FAQ

### General

**Q: What is Geryon?**
A: Geryon is a multi-database connection pooler supporting PostgreSQL, MySQL, and MSSQL from a single binary.

**Q: What are the three "Bodies"?**
A: The three database protocol handlers: PostgreSQL (port 5432), MySQL (port 3306), MSSQL (port 1433).

**Q: How does pooling differ from proxying?**
A: A proxy simply forwards connections. A pool maintains a cache of backend connections for reuse, improving performance.

**Q: What pooling modes are supported?**
A: Session (1:1), Transaction (N:M), and Statement (N:1) modes.

### Configuration

**Q: How do I configure hot-reload?**
A: Send SIGHUP to the process, use POST /api/v1/config/reload, or enable file watching in config.

**Q: What is safe vs unsafe reload?**
A: Safe: pool limits, auth users, logging level. Unsafe: port changes, body type, TLS cert paths.

**Q: How do I generate a SCRAM-SHA-256 password?**
A: `geryon --generate-password`

**Q: How do I enable TLS?**
A: Set `tls.mode: "require"` in pool config.

### Clustering

**Q: What is the minimum cluster size?**
A: 3 nodes for quorum-based Raft consensus.

**Q: How does leader election work?**
A: Raft consensus with election timeout and heartbeat interval.

**Q: Can I run a single node cluster?**
A: Yes, but it's not recommended for production (no HA).

### Performance

**Q: How many connections can Geryon handle?**
A: Depends on resources, but 10,000+ client connections per pool is typical.

**Q: What is the latency overhead?**
A: Typically 0.1-0.5ms per query, depending on pool mode and load.

**Q: How do I tune pool size?**
A: Formula: `optimal = (2 * CPU_CORES) + effective_spindles`

### Troubleshooting

**Q: Geryon won't start. What do I do?**
A: Check logs: `journalctl -u geryon -n 50`. Common issues: port already in use, invalid config.

**Q: How do I debug slow queries?**
A: Enable query logging, check slow query log, use EXPLAIN ANALYZE on backends.

**Q: How do I recover from a split-brain?**
A: Stop writes, identify last consistent state, force re-election, resync.

### Security

**Q: Does Geryon support mTLS?**
A: Yes, configure `client_auth: "require-and-verify-client-cert"`.

**Q: How does SCRAM-SHA-256 work?**
A: Salted Challenge Response Authentication Mechanism using SHA-256.

**Q: Is the dashboard secure?**
A: Enable auth, configure CORS, add security headers.

---

## Appendix S: Open Source References

### Inspired By

- **PgBouncer** - PostgreSQL pooler reference
- **ProxySQL** - MySQL pooler reference  
- **Raft** - Consensus algorithm (Ongaro & Ousterhout)
- **SWIM** - Membership protocol (Das et al.)

### Go Libraries Used

```go
// External dependencies (go.mod)
github.com/go-sql-driver/mysql     // MySQL driver
github.com/lib/pq                  // PostgreSQL driver
github.com/denisenkom/go-mssqldb    // MSSQL driver
gopkg.in/yaml.v3                   // YAML parsing
golang.org/x/term                  // Terminal utilities
golang.org/x/time                  // Time utilities
```

### Further Reading

- [Raft Paper](https://raft.github.io/raft.pdf)
- [SWIM Paper](https://www.cs.cornell.edu/~asdas/research/dsn02-swim.pdf)
- [PostgreSQL Protocol](https://www.postgresql.org/docs/current/protocol.html)
- [MySQL Protocol](https://dev.mysql.com/doc/internals/en/client-server-protocol.html)
- [TDS Protocol](https://docs.microsoft.com/en-us/sql/relational-databases/native-client-odbc-extensions-bulk-copy-instructions/tabular-data-stream-protocol)
- [SCRAM-SHA-256](https://tools.ietf.org/html/rfc5802)