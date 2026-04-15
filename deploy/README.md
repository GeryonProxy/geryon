# Deployment

This directory contains Kubernetes and Helm deployment configurations for Geryon.

## Files

- `kubernetes.yaml` - Single-node Kubernetes deployment manifest
- `helm/` - Helm chart for production deployments

## Quick Start

### Using Kubernetes manifest (single-node)

```bash
kubectl apply -f kubernetes.yaml
```

### Using Helm (recommended for production)

```bash
# Add the chart (when published)
helm repo add geryon https://charts.geryonproxy.dev
helm repo update

# Install
helm install geryon geryon/geryon -n geryon --create-namespace

# With custom values
helm install geryon geryon/geryon -n geryon -f values-overrides.yaml
```

## Configuration

See `helm/geryon/values.yaml` for all configuration options.

Key settings:
- `image.repository` - Container image
- `replicaCount` - Number of replicas (use 1 for single-node mode)
- `pools` - Database pool configurations
- `resources` - CPU/memory limits
- `persistence.enabled` - Enable persistent storage

## Ports

| Port | Service | Description |
|------|---------|-------------|
| 5432 | proxy | Database proxy (PG/MySQL/MSSQL) |
| 8080 | rest | REST API |
| 9090 | grpc | gRPC API |
| 8081 | mcp | MCP server |
| 8082 | dashboard | Web dashboard |

## Documentation

- [Operations Guide](../../docs/OPERATIONS.md) - Full deployment instructions
- [Production Readiness](../../docs/PRODUCTIONREADY.md) - Production readiness assessment