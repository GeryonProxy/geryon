package tokenizer

import (
	"testing"
)

func TestClassifyQuery_Select(t *testing.T) {
	cases := []string{
		"SELECT * FROM users",
		"select * from users",
		"SELECT/* comment */ id FROM t",
	}
	for _, q := range cases {
		qt, err := ClassifyQuery(q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if qt != QuerySelect {
			t.Errorf("ClassifyQuery(%q) = %v, want QuerySelect", q, qt)
		}
	}
}

func TestClassifyQuery_Types(t *testing.T) {
	cases := []struct {
		query string
		want  QueryType
	}{
		{"INSERT INTO t VALUES (1)", QueryInsert},
		{"UPDATE t SET x=1", QueryUpdate},
		{"DELETE FROM t WHERE id=1", QueryDelete},
		{"BEGIN", QueryBegin},
		{"BEGIN WORK", QueryBegin},
		{"START TRANSACTION", QueryBegin},
		{"COMMIT", QueryCommit},
		{"COMMIT WORK", QueryCommit},
		{"ROLLBACK", QueryRollback},
		{"ROLLBACK WORK", QueryRollback},
		{"CREATE TABLE t (id INT)", QueryDDL},
		{"DROP TABLE t", QueryDDL},
		{"ALTER TABLE t ADD COLUMN x INT", QueryDDL},
		{"TRUNCATE TABLE t", QueryDDL},
		{"", QueryUnknown},
		{"SET SESSION x=1", QueryUnknown},
		{"SHOW VARIABLES", QueryUnknown},
	}
	for _, tc := range cases {
		qt, err := ClassifyQuery(tc.query)
		if err != nil {
			t.Fatalf("unexpected error on %q: %v", tc.query, err)
		}
		if qt != tc.want {
			t.Errorf("ClassifyQuery(%q) = %v, want %v", tc.query, qt, tc.want)
		}
	}
}

func TestQueryType_String(t *testing.T) {
	cases := []struct {
		qt   QueryType
		want string
	}{
		{QuerySelect, "SELECT"},
		{QueryInsert, "INSERT"},
		{QueryUpdate, "UPDATE"},
		{QueryDelete, "DELETE"},
		{QueryBegin, "BEGIN"},
		{QueryCommit, "COMMIT"},
		{QueryRollback, "ROLLBACK"},
		{QueryDDL, "DDL"},
		{QueryOther, "OTHER"},
		{QueryUnknown, "UNKNOWN"},
		{QueryType(999), "UNKNOWN"},
	}
	for _, tc := range cases {
		if got := tc.qt.String(); got != tc.want {
			t.Errorf("QueryType(%d).String() = %q, want %q", tc.qt, got, tc.want)
		}
	}
}

func TestIsReadQuery(t *testing.T) {
	if !IsReadQuery(QuerySelect) {
		t.Error("QuerySelect should be a read query")
	}
	writes := []QueryType{QueryInsert, QueryUpdate, QueryDelete, QueryDDL, QueryUnknown, QueryOther, QueryBegin, QueryCommit, QueryRollback}
	for _, qt := range writes {
		if IsReadQuery(qt) {
			t.Errorf("IsReadQuery(%v) = true, want false", qt)
		}
	}
}

func TestIsWriteQuery(t *testing.T) {
	writes := []QueryType{QueryInsert, QueryUpdate, QueryDelete, QueryDDL}
	for _, qt := range writes {
		if !IsWriteQuery(qt) {
			t.Errorf("IsWriteQuery(%v) = false, want true", qt)
		}
	}
	reads := []QueryType{QuerySelect, QueryUnknown, QueryOther, QueryBegin, QueryCommit, QueryRollback}
	for _, qt := range reads {
		if IsWriteQuery(qt) {
			t.Errorf("IsWriteQuery(%v) = true, want false", qt)
		}
	}
}

func TestExtractTables_FromSelect(t *testing.T) {
	cases := []struct {
		query string
		want  []string
	}{
		{"SELECT * FROM users", []string{"users"}},
		{"SELECT * FROM `orders`", []string{"orders"}},
		{`SELECT * FROM "products"`, []string{"products"}},
		{"SELECT * FROM [dbo].[items]", []string{"dbo].[items"}},
		{"SELECT id FROM users WHERE name='test'", []string{"users"}},
		{"SELECT 1", nil},
	}
	for _, tc := range cases {
		tables := ExtractTables(tc.query)
		if len(tables) != len(tc.want) {
			t.Errorf("ExtractTables(%q) = %v, want %v", tc.query, tables, tc.want)
			continue
		}
		for i, tbl := range tables {
			if tbl != tc.want[i] {
				t.Errorf("ExtractTables(%q)[%d] = %q, want %q", tc.query, i, tbl, tc.want[i])
			}
		}
	}
}

func TestExtractTables_Insert(t *testing.T) {
	tables := ExtractTables("INSERT INTO logs (msg) VALUES ('hello')")
	if len(tables) != 1 || tables[0] != "logs" {
		t.Errorf("ExtractTables INSERT = %v, want [logs]", tables)
	}
}

func TestExtractTables_Update(t *testing.T) {
	tables := ExtractTables("UPDATE users SET active=1")
	if len(tables) != 1 || tables[0] != "users" {
		t.Errorf("ExtractTables UPDATE = %v, want [users]", tables)
	}
}

func TestNormalizeQuery(t *testing.T) {
	cases := []struct {
		query, want string
	}{
		{"SELECT  *  FROM   users", "select * from users"},
		{"SELECT $1, $2 FROM users WHERE id = $3", "select ?, ? from users where id = ?"},
		{"SELECT ? FROM users WHERE id = ?", "select ? from users where id = ?"},
		{"  SELECT\n\t1  ", "select 1"},
		{"SELECT * FROM t WHERE id=$1", "select * from t where id=?"},
	}
	for _, tc := range cases {
		got := NormalizeQuery(tc.query)
		if got != tc.want {
			t.Errorf("NormalizeQuery(%q) = %q, want %q", tc.query, got, tc.want)
		}
	}
}

func TestRewriteEngine_ShouldRouteToPrimary(t *testing.T) {
	eng := NewRewriteEngine(true)

	// Writes go to primary
	if !eng.ShouldRouteToPrimary("INSERT INTO t VALUES (1)", false) {
		t.Error("INSERT should route to primary")
	}

	// Select in transaction goes to primary
	if !eng.ShouldRouteToPrimary("SELECT 1", true) {
		t.Error("SELECT in transaction should route to primary")
	}

	// Select not in transaction routes to replica (returns false)
	if eng.ShouldRouteToPrimary("SELECT 1", false) {
		t.Error("SELECT not in transaction should not route to primary")
	}
}

func TestRewriteEngine_ForcePrimaryPattern(t *testing.T) {
	eng := NewRewriteEngine(true)
	eng.AddForcePrimaryPattern("FOR UPDATE")

	if !eng.ShouldRouteToPrimary("SELECT * FROM t WHERE id=1 FOR UPDATE", false) {
		t.Error("SELECT with FOR UPDATE should route to primary")
	}
}

func TestRewriteEngine_RewriteQuery(t *testing.T) {
	eng := NewRewriteEngine(false)
	result := eng.RewriteQuery("SELECT  *  FROM   users  WHERE  id=$1  -- comment")

	want := "select * from users where id=?"
	if result.Query != want {
		t.Errorf("RewriteQuery result = %q, want %q", result.Query, want)
	}
}

func TestRemoveComments(t *testing.T) {
	cases := []struct {
		query, want string
	}{
		{"SELECT 1 -- comment", "SELECT 1 "},
		{"SELECT /* inline */ 2", "SELECT  2"},
		// Note: Go's regexp . does NOT match newlines, so multiline comments are not removed
		{"SELECT 3 /* multi\nline */ 4", "SELECT 3 /* multi\nline */ 4"},
	}
	for _, tc := range cases {
		got := removeComments(tc.query)
		if got != tc.want {
			t.Errorf("removeComments(%q) = %q, want %q", tc.query, got, tc.want)
		}
	}
}

func TestExtractHint(t *testing.T) {
	cases := []struct {
		query, wantHint, wantQuery string
	}{
		{"/* route:replica */ SELECT 1", "route:replica", "SELECT 1"},
		{"/* hint */SELECT * FROM t", "hint", "SELECT * FROM t"},
		{"SELECT 1", "", "SELECT 1"},
	}
	for _, tc := range cases {
		hint, q := ExtractHint(tc.query)
		if hint != tc.wantHint || q != tc.wantQuery {
			t.Errorf("ExtractHint(%q) = (%q, %q), want (%q, %q)", tc.query, hint, q, tc.wantHint, tc.wantQuery)
		}
	}
}

func TestAddHint(t *testing.T) {
	got := AddHint("SELECT 1", "route:primary")
	want := "/* route:primary */ SELECT 1"
	if got != want {
		t.Errorf("AddHint() = %q, want %q", got, want)
	}
}
