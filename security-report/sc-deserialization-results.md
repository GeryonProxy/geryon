# Deserialization Security Report

**Scanner:** sc-deserialization  
**Target:** GeryonProxy (database connection pooler)  
**Date:** 2026-04-13  

---

## Summary

Deserialization in GeryonProxy occurs in two contexts: (1) wire protocol message parsing for PostgreSQL, MySQL, and MSSQL, and (2) JSON deserialization for cluster coordination (Raft, SWIM/Gossip). All protocol codecs impose explicit size limits on payloads. JSON deserialization for cluster messages is also size-constrained via `io.LimitReader`.

---

## Findings

### 1. PostgreSQL Protocol Codec — PASS

- **Location:** `internal/protocol/postgresql/codec.go:34-84`
- **Pattern:** `ReadMessage` reads a message type byte, 4-byte big-endian length, and validates `length >= 4` and `payloadLen <= MaxPayloadLen` (16MB). Buffer allocation matches the declared payload size.
- **Assessment:** No issues found. The codec validates message length before allocating memory and enforces a 16MB `MaxPayloadLen` cap. No unbounded `json.Unmarshal` in this file.

### 2. MySQL Protocol Codec — PASS

- **Location:** `internal/protocol/mysql/codec.go:34-77`
- **Pattern:** `ReadMessage` reads a 3-byte little-endian length, validates `length > MaxPayloadLen` (16MB-1), then allocates `payload := make([]byte, length)`. No `json.Unmarshal` found.
- **Assessment:** No issues found. Length is bounds-checked before buffer allocation. Standard MySQL protocol parser with explicit size limits.

### 3. MSSQL (TDS) Protocol Codec — PASS

- **Location:** `internal/protocol/mssql/codec.go:38-84`
- **Pattern:** `ReadMessage` reads an 8-byte TDS header, extracts a 2-byte big-endian length, validates `length >= 8` and `payloadLen <= MaxPayloadLen` (16MB). No `json.Unmarshal` found.
- **Assessment:** No issues found. TDS packet length is validated against the 16MB cap before buffer allocation.

### 4. Config Loader — PASS

- **Location:** `internal/config/loader.go`
- **Pattern:** Uses a hand-written YAML parser with no `json.Unmarshal` calls. All parsing is string-based with explicit length checks.
- **Assessment:** No issues found. No use of `encoding/json`, `gob`, or other generic deserialization libraries that could be vulnerable to unbounded memory allocation.

### 5. Cache Store — PASS

- **Location:** `internal/cache/store.go`
- **Pattern:** `Store.Set` validates `size > s.maxMemory` before inserting. `RulesEngine.AddRule` bounds pattern length at 1024 characters and limits rule count to 100.
- **Assessment:** No issues found. Cache entries are size-limited and regex patterns are bounded to prevent ReDoS attacks.

### 6. Raft JSON Deserialization — PASS

- **Location:** `internal/raft/raft.go:343` (`decoder := json.NewDecoder(io.LimitReader(conn, maxRaftMessageSize))`)
- **Pattern:** Raft message decoding uses `io.LimitReader` with `maxRaftMessageSize = 1 << 20` (1MB). All `json.Unmarshal` calls operate on `json.RawMessage` fields (`Command.Data`) that have already been received within the bounded stream.
- **Location:** `internal/raft/fsm.go:144,169,190,211,230,250,276,308` — `json.Unmarshal` calls on `json.RawMessage` with bounded struct types.
- **Assessment:** No issues found. The connection-level size limit prevents unbounded JSON payloads. The FSM's `json.Unmarshal` targets specific typed structs (`PoolConfigUpdateData`, `UserUpdateData`, etc.), not arbitrary types.

### 7. Cluster/Gossip JSON Deserialization — PASS

- **Location:** `internal/cluster/cluster.go:217` (`decoder := json.NewDecoder(io.LimitReader(conn, maxRPCPayloadSize))`)
- **Pattern:** RPC message decoding uses `io.LimitReader` with `maxRPCPayloadSize = 1 << 20` (1MB). All `json.Unmarshal` calls target specific typed structs (`VoteRequest`, `VoteResponse`, `AppendEntriesRequest`, etc. via `rpc.Payload`).
- **Location:** `internal/cluster/coordinator.go:407,624`
- **Assessment:** No issues found. The RPC layer uses size-limited readers, preventing large payload attacks.

### 8. Binary Protocol Parsing — PASS

- **Location:** `internal/protocol/common/message.go:301-355` (`ReadStartupMessage`)
- **Pattern:** Startup message length is validated: `if length < 8 || length > 10000`. Parameter parsing scans null-terminated key-value pairs with bounds checks.
- **Assessment:** No issues found. Startup message has an explicit max length of 10,000 bytes.

---

## Conclusion

**No issues found by sc-deserialization.**
