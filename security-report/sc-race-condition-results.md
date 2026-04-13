# Race Condition Security Report

**Scanner:** sc-race-condition
**Date:** 2026-04-13
**Project:** GeryonProxy

## Summary

Found 2 race condition issues requiring fixes.

---

## Issue 1: DATA RACE - Session.lastQuery Concurrent Access

**Severity:** High
**File:** `internal/pool/session.go`
**Line:** 308

### Description

The `HandleMessage` method performs a plain assignment to `s.lastQuery` without holding the session mutex:

```go
// Line 305-309
query, err := codec.ExtractQuery(msg)
if err == nil && query != "" {
    s.lastQuery = query  // DATA RACE - no lock held
}
```

However, other methods in the same file properly synchronize access to `s.lastQuery` using `s.mu`:

- `GetLastQuery()` (line 361) uses `s.mu.RLock()` before reading
- `SetLastQuery()` (line 221) uses `s.mu.Lock()` before writing
- `LastQuery()` (line 213) uses `s.mu.RLock()` before reading

### Race Condition

When `HandleMessage` writes to `s.lastQuery` at line 308 without acquiring `s.mu`, and another goroutine concurrently calls `GetLastQuery()` or `SetLastQuery()`, a data race occurs on the `lastQuery` field.

### Recommended Fix

Either:
1. Add `s.mu.Lock()` before the assignment and `s.mu.Unlock()` after, or
2. Call `s.SetLastQuery(query)` instead of direct assignment

---

## Issue 2: DATA RACE - Node State Modified Outside Lock in SWIM Protocol

**Severity:** Medium
**File:** `internal/cluster/cluster.go`
**Lines:** 644-649

### Description

In the `SwimGossip.probe()` method, node fields are modified after the mutex is released:

```go
func (s *SwimGossip) probe(target *Node) {
    // ...
    conn, err := net.DialTimeout("tcp", target.Address, s.probeTimeout)
    if err != nil {
        s.indirectProbe(target)
        return
    }
    defer conn.Close()

    conn.SetReadDeadline(time.Now().Add(s.probeTimeout))

    // Node responded, mark as alive
    s.mu.Lock()
    s.alive[target.ID] = time.Now()
    delete(s.suspected, target.ID)
    target.LastSeen = time.Now()       // Modifying shared node without cluster lock
    target.State = NodeStateFollower   // Modifying shared node without cluster lock
    s.mu.Unlock()
}
```

The `SwimGossip` mutex `s.mu` only protects the `alive` and `suspected` maps. However, `target.LastSeen` and `target.State` are fields on the shared `Node` struct that are also accessed by `Cluster` methods (via `c.mu`) without proper synchronization.

### Race Condition

The `Cluster` mutex (`c.mu`) protects `Node.State` and `Node.LastSeen` in methods like:
- `handleHeartbeat()` (line 414-434)
- `handleVoteRequest()` (line 316-354)
- `AddNode()` (line 575-583)
- `GetNodes()` (line 563-572)

When `probe()` modifies `target.State` and `target.LastSeen` outside of `c.mu`, concurrent calls to cluster methods that read/write these fields create a data race.

### Recommended Fix

The modification of `target.LastSeen` and `target.State` should either:
1. Be protected by `c.mu` (require acquiring cluster lock), or
2. Use a separate mutex per `Node` for these fields, or  
3. Use atomic operations for `Node.State` and `Node.LastSeen`

---

## Analysis: Well-Protected Components

The following components were verified as properly synchronized:

### internal/pool/manager.go
- `pools` map access is protected by `sync.RWMutex`
- All public methods properly acquire read or write locks

### internal/pool/pool.go
- `serverConnPool` uses `sync.Mutex` for `idle` and `active` maps
- `WaitQueue` uses `sync.Mutex` for `waiters` slice
- Counters use `atomic.Int64` (`clientCount`, `queryCount`, `txnCount`)
- `Backend.Healthy` and `Backend.Draining` use `atomic.Bool`
- `Backend.ConnCount` uses `atomic.Int64`

### internal/cache/store.go
- `entries` map access is protected by `sync.RWMutex`
- Counter operations use `atomic.Uint64`

### internal/metrics/metrics.go
- All map access protected by `sync.RWMutex`
- Counter/Gauge/Histogram use atomic operations

---

## Conclusion

2 race condition issues found requiring immediate attention.
