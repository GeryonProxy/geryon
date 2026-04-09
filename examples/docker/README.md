# Geryon Docker Compose Example

This example demonstrates running Geryon with PostgreSQL and MySQL backends using Docker Compose.

## Quick Start

1. **Start the services:**
   ```bash
   docker-compose up -d
   ```

2. **Wait for services to be healthy:**
   ```bash
   docker-compose ps
   ```

3. **Connect to PostgreSQL through Geryon:**
   ```bash
   psql -h localhost -p 5432 -U geryon -d testdb
   ```
   Password: `geryon_password`

4. **Connect to MySQL through Geryon:**
   ```bash
   mysql -h localhost -P 3306 -u geryon -p testdb
   ```
   Password: `geryon_password`

## Services

| Service | Port | Description |
|---------|------|-------------|
| Geryon PostgreSQL | 5432 | PostgreSQL proxy |
| Geryon MySQL | 3306 | MySQL proxy |
| Geryon REST API | 8080 | Admin REST API |
| Geryon gRPC | 9090 | Admin gRPC API |
| Geryon MCP | 8081 | MCP Server (SSE) |
| PostgreSQL Backend | 5433 | Direct access to PostgreSQL |
| MySQL Backend | 3307 | Direct access to MySQL |

## Web Dashboard

Access the Geryon web dashboard at: http://localhost:8080

## REST API Examples

```bash
# Get pool status
curl http://localhost:8080/api/v1/pools

# Get connection stats
curl http://localhost:8080/api/v1/stats

# Get cluster status
curl http://localhost:8080/api/v1/cluster/status
```

## Configuration

The `geryon.yaml` file configures:
- Two pools: one for PostgreSQL, one for MySQL
- Transaction pooling mode (N:M multiplexing)
- Query caching enabled
- Health checks every 10 seconds
- REST, gRPC, and MCP admin interfaces

## Monitoring

View logs:
```bash
docker-compose logs -f geryon
```

Scale Geryon (multiple instances):
```bash
docker-compose up -d --scale geryon=3
```

## Cleanup

Stop and remove all containers:
```bash
docker-compose down
```

Remove volumes (data will be lost):
```bash
docker-compose down -v
```

## Production Considerations

For production use:
1. Use proper secrets management for passwords
2. Enable TLS for database connections
3. Configure proper resource limits
4. Use external service discovery for clustering
5. Set up monitoring and alerting
