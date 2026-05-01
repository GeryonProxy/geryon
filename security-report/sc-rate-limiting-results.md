# SC-Rate-Limiting Results

## Summary
**Project:** GeryonProxy - Pure Go database connection pooler/proxy
**Check:** Rate Limiting Analysis
**Date:** 2026-05-01

## Findings

### PASS - Rate Limiting Implemented

#### 1. Auth Rate Limiting (Brute-Force Protection)
- **Location:** `internal/auth/auth.go:448-543`
- **Component:** `AuthLimiter` struct
- **Features:**
  - Tracks failed authentication attempts per source IP
  - Configurable `maxAttempts`, `window`, and `lockoutPeriod`
  - `RecordFailure(ip string)` - Records failed attempt, returns true if locked out
  - `RecordSuccess(ip string)` - Clears attempt history on successful auth
  - `IsLimited(ip string)` - Checks if IP is currently locked out
  - Periodic cleanup goroutine removes stale entries
- **Mutex protection:** `sync.Mutex` protects `attempts` map (line 449)
- **Assessment:** PRESENT - Auth brute-force protection is implemented

#### 2. Admin API Rate Limiting
- **Location:** `internal/api/grpc/server.go:219-287`
- **Component:** `apiRateLimiter` struct
- **Features:**
  - Token bucket rate limiting per client IP
  - Uses Go's `golang.org/x/time/rate` package
  - `GetLimiter(ip string)` returns per-IP rate limiter
  - Periodic cleanup removes inactive IPs
  - `withRateLimit()` middleware applies limiting to requests
- **Mutex protection:** `sync.Mutex` protects `limiters` and `lastSeen` maps
- **Default rate:** Configurable via `newAPIRateLimiter(r rate.Limit, burst int)`
- **Assessment:** PRESENT - Admin API endpoints are protected

#### 3. Backend Connection Limits (Pool Size)
- **Location:** `internal/pool/pool.go`
- **Configuration:** `Limits.MaxServerConnections` limits backend connections per pool
- **Implementation:** `TryIncrementClientCount()` at listener level checks against `MaxClientConnections`
- **Validation:** `config.go` validates limits are non-negative
- **Assessment:** PRESENT - Backend pool size limits enforced

#### 4. Client Connection Limits
- **Location:** `internal/proxy/listener.go:301`
- **Pattern:** Atomic check against `MaxClientConnections` limit
- **Implementation:** `l.pool.TryIncrementClientCount(maxConns)` before accepting connection
- **Rejection:** Returns early if limit reached, logs warning
- **Wait queue:** Clients can wait for available backend connections (see `Pool.Wait()`)
- **Assessment:** PRESENT - Client connection overflow handled via wait queue

### Observations

1. **AuthLimiter defaults:** `NewAuthLimiter()` creates limiter with defaults (5 attempts, 15 min window, 15 min lockout)
2. **Custom AuthLimiter config:** `NewAuthLimiterConfig(maxAttempts, window, lockoutPeriod)` allows customization
3. **gRPC rate limit:** Applied per-IP with configurable rate and burst parameters
4. **Pool limit validation:** Config validates that limits are properly set

### Configuration Checks (from config validation)

- `MaxClientConnections` >= 0 validated
- `MaxServerConnections` >= 0 validated
- `MinServerConnections` >= 0 validated
- Port conflicts detected

### Conclusion
Rate limiting is properly implemented across all critical paths:
- Auth brute-force attacks are rate-limited per IP
- Admin API is rate-limited per client IP
- Backend connection pools have size limits
- Client connections respect configured limits with wait queue fallback
