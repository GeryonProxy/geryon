package tokenizer

import (
	"regexp"
	"strings"
)

// QueryType represents the type of SQL query.
type QueryType int

const (
	QueryUnknown QueryType = iota
	QuerySelect
	QueryInsert
	QueryUpdate
	QueryDelete
	QueryBegin
	QueryCommit
	QueryRollback
	QueryDDL
	QueryOther
)

// String returns the string representation of the query type.
func (t QueryType) String() string {
	switch t {
	case QuerySelect:
		return "SELECT"
	case QueryInsert:
		return "INSERT"
	case QueryUpdate:
		return "UPDATE"
	case QueryDelete:
		return "DELETE"
	case QueryBegin:
		return "BEGIN"
	case QueryCommit:
		return "COMMIT"
	case QueryRollback:
		return "ROLLBACK"
	case QueryDDL:
		return "DDL"
	case QueryOther:
		return "OTHER"
	default:
		return "UNKNOWN"
	}
}

// ClassifyQuery determines the type of SQL query.
func ClassifyQuery(query string) (QueryType, error) {
	// M-7 fix: strip comments and handle multi-statement queries
	query = stripSQLComments(query)
	query = strings.TrimSpace(query)

	// Get only the first statement (before semicolon)
	if idx := strings.Index(query, ";"); idx != -1 {
		query = strings.TrimSpace(query[:idx])
	}

	queryUpper := strings.ToUpper(query)

	if strings.HasPrefix(queryUpper, "SELECT") {
		return QuerySelect, nil
	}
	if strings.HasPrefix(queryUpper, "INSERT") {
		return QueryInsert, nil
	}
	if strings.HasPrefix(queryUpper, "UPDATE") {
		return QueryUpdate, nil
	}
	if strings.HasPrefix(queryUpper, "DELETE") {
		return QueryDelete, nil
	}
	if strings.HasPrefix(queryUpper, "BEGIN") || strings.HasPrefix(queryUpper, "START TRANSACTION") {
		return QueryBegin, nil
	}
	if strings.HasPrefix(queryUpper, "COMMIT") {
		return QueryCommit, nil
	}
	if strings.HasPrefix(queryUpper, "ROLLBACK") {
		return QueryRollback, nil
	}
	if strings.HasPrefix(queryUpper, "CREATE") {
		return QueryDDL, nil
	}
	if strings.HasPrefix(queryUpper, "DROP") {
		return QueryDDL, nil
	}
	if strings.HasPrefix(queryUpper, "ALTER") {
		return QueryDDL, nil
	}
	if strings.HasPrefix(queryUpper, "TRUNCATE") {
		return QueryDDL, nil
	}

	return QueryUnknown, nil
}

// stripSQLComments removes SQL comments from a query string.
func stripSQLComments(query string) string {
	// Remove line comments (-- ...)
	result := ""
	for i := 0; i < len(query); i++ {
		if i+2 <= len(query) && query[i] == '-' && query[i+1] == '-' {
			// Skip to end of line
			for i < len(query) && query[i] != '\n' {
				i++
			}
			continue
		}
		result += string(query[i])
	}
	query = result

	// Remove block comments (/* ... */)
	result = ""
	i := 0
	for i < len(query) {
		if i+1 < len(query) && query[i] == '/' && query[i+1] == '*' {
			// Skip block comment
			i += 2
			for i < len(query) {
				if i+1 < len(query) && query[i] == '*' && query[i+1] == '/' {
					i += 2
					break
				}
				i++
			}
			continue
		}
		result += string(query[i])
		i++
	}
	return result
}

// IsWriteQuery returns true if the query is a write operation.
func IsWriteQuery(queryType QueryType) bool {
	return queryType == QueryInsert || queryType == QueryUpdate || queryType == QueryDelete || queryType == QueryDDL
}

// IsReadQuery returns true if the query is a read operation.
func IsReadQuery(queryType QueryType) bool {
	return queryType == QuerySelect
}

// ExtractTables extracts table names from a SQL query.
// This is a simplified implementation.
func ExtractTables(query string) []string {
	tables := make([]string, 0)
	queryUpper := strings.ToUpper(query)

	// Very simple table extraction - would need full SQL parser for complete solution
	// Look for FROM clause
	if idx := strings.Index(queryUpper, "FROM"); idx != -1 {
		afterFrom := query[idx+4:]
		afterFrom = strings.TrimSpace(afterFrom)
		// Get first token after FROM
		parts := strings.FieldsFunc(afterFrom, func(r rune) bool {
			return r == ' ' || r == ',' || r == ';' || r == '\n' || r == '\t'
		})
		if len(parts) > 0 {
			tableName := strings.TrimSpace(parts[0])
			tableName = strings.Trim(tableName, "`\"[]")
			if tableName != "" {
				tables = append(tables, tableName)
			}
		}
	}

	// Look for INSERT INTO
	if idx := strings.Index(queryUpper, "INSERT INTO"); idx != -1 {
		afterInsert := query[idx+11:]
		afterInsert = strings.TrimSpace(afterInsert)
		parts := strings.FieldsFunc(afterInsert, func(r rune) bool {
			return r == ' ' || r == '(' || r == ';'
		})
		if len(parts) > 0 {
			tableName := strings.TrimSpace(parts[0])
			tableName = strings.Trim(tableName, "`\"[]")
			if tableName != "" {
				tables = append(tables, tableName)
			}
		}
	}

	// Look for UPDATE
	if idx := strings.Index(queryUpper, "UPDATE"); idx != -1 {
		afterUpdate := query[idx+6:]
		afterUpdate = strings.TrimSpace(afterUpdate)
		parts := strings.FieldsFunc(afterUpdate, func(r rune) bool {
			return r == ' ' || r == ',' || r == ';' || r == '\n' || r == '\t' || r == '='
		})
		if len(parts) > 0 {
			tableName := strings.TrimSpace(parts[0])
			tableName = strings.Trim(tableName, "`\"[]")
			if tableName != "" {
				tables = append(tables, tableName)
			}
		}
	}

	return tables
}

// NormalizeQuery normalizes a query for cache key generation.
// Removes extra whitespace, converts to lowercase, normalizes parameter placeholders.
func NormalizeQuery(query string) string {
	// Convert to lowercase
	query = strings.ToLower(query)

	// Replace multiple whitespace with single space
	var result strings.Builder
	inWhitespace := false
	for _, ch := range query {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			if !inWhitespace {
				result.WriteByte(' ')
				inWhitespace = true
			}
		} else {
			result.WriteRune(ch)
			inWhitespace = false
		}
	}

	normalized := strings.TrimSpace(result.String())

	// Normalize parameter placeholders ($1, $2, ?) to ?
	normalized = normalizeParameters(normalized)

	return normalized
}

func normalizeParameters(query string) string {
	// Replace $N style parameters with ?
	query = regexp.MustCompile(`\$\d+`).ReplaceAllString(query, "?")

	// Already using ? style, no change needed
	return query
}

// RewriteResult holds the result of query rewriting.
type RewriteResult struct {
	Query        string
	Target       string // "primary" or "replica"
	WasRewritten bool
}

// RewriteEngine manages query rewriting rules.
type RewriteEngine struct {
	forcePrimary  map[string]bool // Patterns that must go to primary
	forceReplica  map[string]bool // Patterns that can go to replica
	preferReplica bool            // Whether to prefer replicas for reads
}

// NewRewriteEngine creates a new query rewrite engine.
func NewRewriteEngine(preferReplica bool) *RewriteEngine {
	return &RewriteEngine{
		forcePrimary:  make(map[string]bool),
		forceReplica:  make(map[string]bool),
		preferReplica: preferReplica,
	}
}

// AddForcePrimaryPattern adds a pattern that must route to primary.
func (e *RewriteEngine) AddForcePrimaryPattern(pattern string) {
	e.forcePrimary[pattern] = true
}

// AddForceReplicaPattern adds a pattern that can route to replica.
func (e *RewriteEngine) AddForceReplicaPattern(pattern string) {
	e.forceReplica[pattern] = true
}

// ShouldRouteToPrimary determines if query should route to primary.
func (e *RewriteEngine) ShouldRouteToPrimary(query string, inTransaction bool) bool {
	// All writes go to primary
	queryType, _ := ClassifyQuery(query)
	if IsWriteQuery(queryType) {
		return true
	}

	// In transaction, all queries go to primary
	if inTransaction {
		return true
	}

	// Check force primary patterns
	normalized := NormalizeQuery(query)
	for pattern := range e.forcePrimary {
		if strings.Contains(normalized, strings.ToLower(pattern)) {
			return true
		}
	}

	// SELECT queries can go to replica
	return false
}

// RewriteQuery potentially rewrites a query for optimization.
func (e *RewriteEngine) RewriteQuery(query string) *RewriteResult {
	result := &RewriteResult{
		Query:        query,
		WasRewritten: false,
	}

	// Remove SQL comments
	query = removeComments(query)

	// Normalize whitespace
	query = NormalizeQuery(query)

	result.Query = query
	return result
}

// removeComments removes SQL comments from query.
func removeComments(query string) string {
	// Remove single-line comments (-- comment)
	query = regexp.MustCompile(`--[^\n]*`).ReplaceAllString(query, "")

	// Remove multi-line comments (/* comment */)
	query = regexp.MustCompile(`/\*.*?\*/`).ReplaceAllString(query, "")

	return query
}

// AddHint adds a routing hint to the query.
func AddHint(query string, hint string) string {
	return "/* " + hint + " */ " + query
}

// ExtractHint extracts routing hints from query.
func ExtractHint(query string) (string, string) {
	// Check for /* hint */ at the start
	re := regexp.MustCompile(`^/\*\s*(.+?)\s*\*/\s*`)
	matches := re.FindStringSubmatch(query)
	if len(matches) > 1 {
		// Return hint and query without hint
		return matches[1], re.ReplaceAllString(query, "")
	}
	return "", query
}
