# GeryonProxy Injection Attack Analysis

**Analyzed:** GeryonProxy codebase
**Date:** 2026-04-14
**Injection Types:** SQL/NoSQL, Command, Header, XSS, LDAP

---

## 1. SQL/NoSQL Injection (CWE-89)

### Vector 1: Tokenizer Query Classification

**File:** `internal/tokenizer/tokenizer.go`

**Finding:** The `ClassifyQuery()` function uses simple `strings.HasPrefix()` checks to classify SQL query types. This is a read-only classification system that does NOT construct queries.

**Code:**
```go
func ClassifyQuery(query string) (QueryType, error) {
    query = stripSQLComments(query)  // M-7 fix: strips comments
    query = strings.TrimSpace(query)
    if idx := strings.Index(query, ";"); idx != -1 {
        query = strings.TrimSpace(query[:idx])
    }
    queryUpper := strings.ToUpper(query)
    if strings.HasPrefix(queryUpper, "SELECT") {
        return QuerySelect, nil
    }
    // ... more HasPrefix checks
}
```

**Status:** MITIGATED - The tokenizer is read-only. It only examines queries for routing decisions, not modification. Queries are passed through binary protocol messages directly to backends without reconstruction.

### Vector 2: PostgreSQL Codec ExtractQuery

**File:** `internal/protocol/postgresql/codec.go`

**Finding:** `ExtractQuery()` methods extract query strings from wire protocol messages using null-terminated string parsing. No string construction occurs.

**Code:**
```go
func (c *PGCodec) extractSimpleQuery(msg *common.Message) string {
    for i, b := range msg.Payload {
        if b == 0 {
            return string(msg.Payload[:i])
        }
    }
    return string(msg.Payload)
}
```

**Status:** MITIGATED - Raw extraction only. Queries are passed directly to backends.

### Vector 3: Cache Key Generation

**File:** `internal/pool/pool.go` / `internal/cache/store.go`

**Finding:** Query normalization for cache keys converts `$1, $2` placeholders to `?`.

**Code (from tokenizer):**
```go
func normalizeParameters(query string) string {
    query = regexp.MustCompile(`\$\d+`).ReplaceAllString(query, "?")
    return query
}
```

**Status:** MITIGATED - Parameter placeholders are normalized for cache lookups only. Original parameterized queries are sent to backends. No SQL construction occurs.

### Vector 4: Table Extraction for Cache Invalidation

**File:** `internal/proxy/listener.go` (lines 2271-2292)

**Finding:** `extractTablesFromQuery()` uses simple string extraction:

```go
func extractTablesFromQuery(query string) []string {
    tables := make([]string, 0)
    upper := strings.ToUpper(query)
    fromIdx := strings.Index(upper, "FROM ")
    if fromIdx != -1 {
        rest := query[fromIdx+5:]
        fields := strings.Fields(rest)
        if len(fields) > 0 {
            table := fields[0]
            table = strings.TrimRight(table, ",;")
            tables = append(tables, table)
        }
    }
    return tables
}
```

**Status:** MITIGATED - Only used for cache invalidation key matching. Does not construct SQL. Malformed table names would simply result in no cache invalidation, not injection.

---

## 2. Command Injection (CWE-78)

**Finding:** NO COMMAND INJECTION VULNERABILITY

**Search Results:** No `os/exec` or `exec.Command` usage found in the codebase.

The codebase uses:
- `os.ReadFile` for reading password files and config
- Custom YAML parser (zero dependencies)
- No shell commands executed

**Code Evidence:**
```go
// From internal/proxy/listener.go line 850-860:
if ps.config.Backend.Auth.PasswordFile != "" {
    passwordBytes, err := os.ReadFile(ps.config.Backend.Auth.PasswordFile)
    if err != nil {
        return fmt.Errorf("failed to read backend password file: %w", err)
    }
    backendPassword = strings.TrimSpace(string(passwordBytes))
    // M-11 fix: zero the buffer after use to reduce memory lifetime
    for i := range passwordBytes {
        passwordBytes[i] = 0
    }
}
```

**Status:** SAFE - No command execution vectors exist.

---

## 3. Header Injection (CWE-93)

### Vector 1: CORS Header Handling

**File:** `internal/api/rest/server.go` (lines 192-221)

**Finding:** Origin header is read and set directly:

```go
func (s *Server) withCORS(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        origin := r.Header.Get("Origin")
        // ... validation loop
        if allowed {
            w.Header().Set("Access-Control-Allow-Origin", origin)
            // ...
        }
    })
}
```

**Status:** MITIGATED - Origin validation requires exact match against `config.AllowedOrigins`. Wildcard `*` is only set explicitly in config, not from user input. Header value is set directly but origin validation prevents malicious values.

### Vector 2: SSE Response Header

**File:** `internal/api/rest/server.go` (lines 756-764)

**Finding:** Content-Type and Cache-Control set to constants:

```go
w.Header().Set("Content-Type", "text/event-stream")
w.Header().Set("Cache-Control", "no-cache")
w.Header().Set("Connection", "keep-alive")
```

**Status:** SAFE - No user-controlled header construction.

### Vector 3: Security Headers Middleware

**File:** `internal/api/rest/server.go` (lines 169-177)

**Finding:** All security headers use constant values:

```go
w.Header().Set("X-Content-Type-Options", "nosniff")
w.Header().Set("X-Frame-Options", "DENY")
w.Header().Set("X-XSS-Protection", "1; mode=block")
w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
```

**Status:** SAFE - Constant values, no injection possible.

### Vector 4: Database Protocol Message Headers

**File:** `internal/protocol/postgresql/codec.go`

**Finding:** Binary protocol message construction uses length-prefixed fields with null terminators. No string concatenation in header construction.

```go
func (c *PGCodec) CreateStartupMessage(user, database string) []byte {
    // Binary construction with length prefixes
    buf := make([]byte, 4+length)
    binary.BigEndian.PutUint32(buf[0:4], uint32(length+4))
    // ... null-terminated strings with explicit copying
}
```

**Status:** SAFE - Binary protocol prevents header injection via null-byte termination.

---

## 4. XSS (CWE-79)

### Vector 1: REST API JSON Responses

**File:** `internal/api/rest/server.go`

**Finding:** All API responses use `json.NewEncoder()` which automatically escapes HTML special characters:

```go
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(data)  // Automatic escaping
}
```

**Status:** MITIGATED - JSON encoding escapes special characters by default.

### Vector 2: Error Response Sanitization

**File:** `internal/api/rest/server.go` (lines 355-371)

**Finding:** Error messages are sanitized:

```go
func writeError(w http.ResponseWriter, status int, msg string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(map[string]interface{}{"error": msg})
}

func sanitizeErr(err error) string {
    msg := err.Error()
    if len(msg) > 200 {
        msg = msg[:200]  // Truncation prevents info leakage
    }
    return msg
}
```

**Status:** MITIGATED - JSON encoding + truncation prevents both XSS and info leakage.

### Vector 3: Dashboard Static Files

**File:** `internal/api/rest/server.go` (lines 95-105)

**Finding:** Dashboard served from embedded `staticFS`:

```go
func setupDashboard(mux *http.ServeMux) error {
    staticContent, err := fs.Sub(staticFS, "static")
    fileServer := http.FileServer(http.FS(staticContent))
    mux.Handle("/", fileServer)
    return nil
}
```

**Status:** SAFE - Static file serving with `http.FileServer`. No server-side rendering of user input.

### Vector 4: Log Injection

**File:** `internal/logger/querylog.go`

**Finding:** Query logging uses JSON encoding which escapes special characters:

```go
func (ql *QueryLogger) writeJSONLog(entry QueryLogEntry) {
    data, err := json.Marshal(entry)  // Automatic escaping
    if err != nil {
        return
    }
    ql.jsonLog.Write(data)
}
```

However, text log formats use `fmt.Sprintf` with truncated values:

```go
func (ql *QueryLogger) writeSlowLog(entry QueryLogEntry) {
    line := fmt.Sprintf("[%s] [%s] [%s] %s - %s (%dms) rows=%d client=%s backend=%s\n",
        entry.Timestamp.Format(time.RFC3339),
        entry.Pool,
        entry.Username,
        entry.QueryID,
        entry.Query[:min(len(entry.Query), 100)],  // Truncated to 100 chars
        // ...
    )
}
```

**Status:** MITIGATED - Query text is truncated to 100 characters in text logs. The `redactQuery()` function also strips sensitive patterns before logging.

**Additional Mitigations:**
```go
// secretPatterns strips credential-related SQL patterns
func redactQuery(query string) string {
    for _, pattern := range secretPatterns {
        query = pattern.ReplaceAllString(query, "[REDACTED]")
    }
    return query
}
```

---

## 5. LDAP Injection (CWE-90)

**Finding:** NO LDAP INJECTION VULNERABILITY

**Search Results:** No LDAP protocol usage found in the codebase.

**Analysis:** Authentication in GeryonProxy uses:
- SCRAM-SHA-256 for PostgreSQL (handled in `internal/auth/scram.go`)
- MySQL native password authentication (passthrough)
- MSSQL TDS authentication (passthrough)

User lookup is performed against an in-memory UserDatabase loaded from config:

```go
func (db *UserDatabase) GetUser(username string) *User {
    db.mu.RLock()
    defer db.mu.RUnlock()
    return db.users[username]  // Direct map lookup, no LDAP
}
```

**Status:** NOT APPLICABLE - No LDAP implementation exists.

---

## 6. Additional Injection Vectors

### Vector: Configuration Environment Variable Expansion

**File:** `internal/config/loader.go` (lines 55-79)

**Finding:** Environment variable expansion is restricted to `GERYON_` prefix:

```go
var allowedEnvPrefix = "GERYON_"

func expandEnvVars(input string) string {
    return envVarPattern.ReplaceAllStringFunc(input, func(match string) string {
        content := match[2 : len(match)-1]
        parts := strings.SplitN(content, ":-", 2)
        varName := parts[0]

        // Only expand GERYON_* variables for security
        if !strings.HasPrefix(varName, allowedEnvPrefix) {
            if len(parts) > 1 {
                return parts[1]
            }
            return match // Leave non-GERYON vars as-is
        }
        // ...
    })
}
```

**Status:** MITIGATED - Only `GERYON_*` environment variables are expanded. Other `${VAR}` references are left as-is or replaced with their default value.

### Vector: Pool Name Validation

**File:** `internal/api/rest/server.go` (lines 373-379)

**Finding:** Pool names are validated by regex:

```go
var poolNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func validatePoolName(name string) bool {
    return poolNameRegex.MatchString(name)
}
```

**Status:** MITIGATED - Strict alphanumeric validation prevents path traversal and injection in pool names.

### Vector: Path Traversal Prevention in Query Logger

**File:** `internal/proxy/listener.go` (lines 77-88)

**Finding:** Pool names are sanitized before use in file paths:

```go
safeName := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(cfg.Name, "_")
qlConfig.Directory = filepath.Join("logs", "queries", safeName)
```

**Status:** MITIGATED - Pool names are sanitized to alphanumeric, underscore, and hyphen only before use in file paths.

---

## Summary Table

| Injection Type | Status | Mitigation |
|----------------|--------|------------|
| SQL/NoSQL (tokenizer) | MITIGATED | Read-only classification, no query construction |
| SQL/NoSQL (codec) | MITIGATED | Raw extraction, direct forwarding to backend |
| SQL/NoSQL (cache keys) | MITIGATED | Normalization for lookup only, parameterized queries forwarded |
| Command Injection | SAFE | No exec.Command usage in codebase |
| Header Injection (HTTP) | MITIGATED | Origin validation, constant headers, binary protocol |
| Header Injection (DB) | SAFE | Binary protocol with length prefixes |
| XSS (REST API) | MITIGATED | JSON encoding auto-escapes |
| XSS (dashboard) | MITIGATED | Static file serving only |
| XSS (logs) | MITIGATED | Truncation + JSON encoding + redaction |
| LDAP Injection | N/A | No LDAP implementation |

---

## Proof of Concept for Exploitable Vectors

**None identified.** All identified vectors are mitigated by one or more of:

1. **Read-only processing** - Tokenizer only examines, doesn't construct
2. **Binary protocols** - PostgreSQL/MySQL/MSSQL wire protocols use length-prefixed fields
3. **JSON encoding** - Automatic escaping of special characters
4. **Input validation** - Regex validation on pool names, restricted env var expansion
5. **Output encoding** - Truncation, sanitization, redaction before logging or display

---

## Recommendations

1. **Consider prepared statement tracking** - Currently the re-prepare mechanism tracks statement names, but a malicious server could potentially send unexpected ParseComplete messages. The M-8 fix addresses this partially.

2. **Rate limit on auth failures** - The `AuthLimiter` implementation (lines 472-585 in auth.go) should be verified as enabled in production config to prevent brute force.

3. **Query length limits** - While startup parameters have length limits (256 bytes), consider adding query length limits at the protocol level to prevent memory exhaustion via oversized queries.
