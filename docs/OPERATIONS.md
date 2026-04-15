# Geryon Operations Guide

> Production deployment, monitoring, and troubleshooting guide for Geryon Proxy.

## Table of Contents

1. [Deployment](#deployment)
2. [Configuration](#configuration)
3. [Monitoring](#monitoring)
4. [Troubleshooting](#troubleshooting)
5. [Performance Tuning](#performance-tuning)
6. [Security](#security)

---

## Deployment

### Quick Start

```bash
# Generate configuration
./geryon --generate-config > geryon.yaml

# Edit configuration
vim geryon.yaml

# Start proxy
./geryon --config geryon.yaml
```

### Docker

```bash
# Pull image
docker pull geryonproxy/geryon:latest

# Run
docker run -p 5432:5432 -p 3306:3306 -p 1433:1433 \
  -v /path/to/geryon.yaml:/etc/geryon/geryon.yaml \
  geryonproxy/geryon --config /etc/geryon/geryon.yaml
```

### Systemd

```ini
[Unit]
Description=Geryon Database Proxy
After=network.target

[Service]
Type=simple
User=geryon
Group=geryon
ExecStart=/usr/local/bin/geryon --config /etc/geryon/geryon.yaml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

---

## Configuration

### Pool Configuration

```yaml
pools:
  - name: postgres-pool
    body: postgresql        # postgresql, mysql, or mssql
    mode: transaction       # session, transaction, or statement
    listen:
      host: 0.0.0.0
      port: 5432
    backend:
      hosts:
        - host: db1.internal
          port: 5432
        - host: db2.internal
          port: 5432
```

### Connection Pooling Modes

| Mode | Description | Use Case |
|------|-------------|----------|
| `session` | 1:1 client-to-backend mapping | Temp tables, SET variables, LISTEN/NOTIFY |
| `transaction` | N:M multiplexing (default) | Web applications |
| `statement` | N:1 aggressive multiplexing | Simple query patterns |

### Transaction Timeouts

```yaml
transaction:
  timeout: 30m        # Max transaction duration
  idle_timeout: 5m    # Idle transaction timeout
  check_interval: 30s # Timeout check frequency
```

### Health Checks

```yaml
health:
  check_interval: 10s
  check_query: "SELECT 1"
  max_failures: 3
```

---

## Monitoring

### REST API Endpoints

```bash
# Health check
curl http://localhost:8080/api/v1/health

# Pool statistics
curl http://localhost:8080/api/v1/pools

# Connection stats
curl http://localhost:8080/api/v1/connections

# Real-time stats stream
curl http://localhost:8080/api/v1/stats/stream
```

### Metrics (Prometheus)

Geryon exposes Prometheus metrics at `/metrics`:

```
# HELP geryon_client_connections_active Active client connections
# TYPE geryon_client_connections_active gauge
geryon_client_connections_active{pool="postgres"} 42

# HELP geryon_server_connections_idle Idle server connections
# TYPE geryon_server_connections_idle gauge
geryon_server_connections_idle{pool="postgres"} 8

# HELP geryon_query_duration_seconds Query duration
# TYPE geryon_query_duration_seconds histogram
geryon_query_duration_seconds_bucket{pool="postgres",le="0.001"} 1234
```

### Log Files

Logs are written to `logs/geryon/` (configurable):

```
logs/
├── slow.log        # Slow queries (>100ms by default)
├── all.log         # All queries (if enabled)
└── queries.json    # JSON-formatted query logs
```

---

## Troubleshooting

### Connection Issues

**Symptom:** Clients cannot connect to Geryon

```bash
# Check if Geryon is listening
netstat -tlnp | grep 5432

# Check backend connectivity
./geryon --validate
```

**Symptom:** "no available connections" errors

```bash
# Check pool status
curl http://localhost:8080/api/v1/pools

# Check backend health
curl http://localhost:8080/api/v1/backends
```

### Performance Issues

**Symptom:** High latency

1. Check slow query log:
```bash
tail -f logs/geryon/slow.log
```

2. Enable query logging temporarily:
```bash
# Via API
curl -X PUT http://localhost:8080/api/v1/config \
  -d '{"debug": {"query_logging": true}}'
```

**Symptom:** High memory usage

1. Check connection counts:
```bash
curl http://localhost:8080/api/v1/connections
```

2. Reduce pool limits:
```yaml
limits:
  max_server_connections: 100
  max_idle_time: 5m
```

### Cluster Issues

**Symptom:** Raft leader election failures

Check logs for:
```
[Raft] Failed to reach consensus
[Raft] Election timeout
```

Solutions:
- Ensure network connectivity between cluster nodes
- Check firewall rules for Raft (port 12300) and SWIM (port 13300) ports
- Verify clock sync across nodes

---

## Performance Tuning

### Buffer Pooling

Geryon uses sync.Pool for buffer reuse. Default buffer size is 4KB.

### Recommended Settings

| Workload | max_server_connections | idle_timeout |
|----------|------------------------|--------------|
| Web app (OLTP) | 50-100 | 5m |
| Batch processing | 200-500 | 1m |
| Mixed workload | 100-200 | 5m |

### Circuit Breaker

Geryon includes a circuit breaker for backend failures:
- Opens after 5 consecutive failures
- Half-open after 30 seconds
- Closes after successful probe

Monitor via `/api/v1/backends` - look for `healthy: false`.

---

## Security

### Authentication

Geryon supports two auth modes:

1. **Passthrough** (default): Authenticate directly against backend DB
2. **Interception**: Authenticate against Geryon's user database

```yaml
auth:
  mode: interception  # or "passthrough"
  users:
    - username: app_user
      password_hash: "SCRAM-SHA-256:..."
```

### TLS Configuration

```yaml
tls:
  mode: prefer        # disable, allow, prefer, require, verify-ca, verify-full
  cert_file: /path/to/cert.pem
  key_file: /path/to/key.pem
  client_ca_file: /path/to/ca.pem  # for verify-ca/verify-full
```

### Rate Limiting

Auth rate limiting is built-in:
- 10 failed attempts per 5 minutes triggers lockout
- 5 minute lockout period

---

## Hot Configuration Reload

Geryon supports configuration reload without restart:

```bash
# Via SIGHUP
kill -SIGHUP $(pidof geryon)

# Via file watch (automatic)
# Edit geryon.yaml - changes detected automatically

# Via API
curl -X POST http://localhost:8080/api/v1/config/reload
```

**Safe to change without restart:**
- Pool limits
- Logging level
- Auth users
- Health check settings

**Requires restart:**
- Port changes
- Backend host changes
- TLS cert paths

---

## Backup and Recovery

### Configuration Backup

```bash
# Backup config
cp /etc/geryon/geryon.yaml /backup/geryon.yaml.$(date +%Y%m%d)
```

### State

Geryon is stateless - no persistent state. Backend connections are recreated on restart.

---

*For more information, see:*
- [README](../README.md)
- [ROADMAP](../.project/ROADMAP.md)
- [Production Readiness](../.project/PRODUCTIONREADY.md)
