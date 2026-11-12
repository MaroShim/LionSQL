package sqlutil

import (
	"testing"
)

func TestSplitSelectClauses_Basic(t *testing.T) {
	sql := "SELECT id, name FROM sys.tables WHERE is_ms_shipped = 0 ORDER BY name ASC"
	c := SplitSelectClauses(sql)

	if c.Projection != "id, name" {
		t.Fatalf("expected projection 'id, name', got '%s'", c.Projection)
	}
	if c.From != "sys.tables" {
		t.Fatalf("expected from 'sys.tables', got '%s'", c.From)
	}
	if c.Where != "is_ms_shipped = 0" {
		t.Fatalf("expected where 'is_ms_shipped = 0', got '%s'", c.Where)
	}
	if c.OrderBy != "name ASC" {
		t.Fatalf("expected orderBy 'name ASC', got '%s'", c.OrderBy)
	}
}

func TestSplitSelectClauses_NoWhere(t *testing.T) {
	sql := "SELECT * FROM sys.tables ORDER BY object_id DESC"
	c := SplitSelectClauses(sql)

	if c.Projection != "*" {
		t.Fatalf("expected projection '*', got '%s'", c.Projection)
	}
	if c.From != "sys.tables" {
		t.Fatalf("expected from 'sys.tables', got '%s'", c.From)
	}
	if c.Where != "" {
		t.Fatalf("expected empty where, got '%s'", c.Where)
	}
	if c.OrderBy != "object_id DESC" {
		t.Fatalf("expected orderBy 'object_id DESC', got '%s'", c.OrderBy)
	}
}

func TestSplitSelectClauses_SubqueriesAndQuotes(t *testing.T) {
	sql := `SELECT (SELECT col FROM t2 WHERE val = 'FROM WHERE ORDER BY') AS sub, name
		FROM sys.tables
		WHERE name NOT IN (SELECT tbl FROM t3 WHERE code = 1 ORDER BY tbl)
		ORDER BY name`
	c := SplitSelectClauses(sql)

	expectedProj := "(SELECT col FROM t2 WHERE val = 'FROM WHERE ORDER BY') AS sub, name"
	if c.Projection != expectedProj {
		t.Fatalf("projection mismatch:\nexpected: %s\ngot:      %s", expectedProj, c.Projection)
	}
	if c.From != "sys.tables" {
		t.Fatalf("expected from 'sys.tables', got '%s'", c.From)
	}
	expectedWhere := "name NOT IN (SELECT tbl FROM t3 WHERE code = 1 ORDER BY tbl)"
	if c.Where != expectedWhere {
		t.Fatalf("where mismatch:\nexpected: %s\ngot:      %s", expectedWhere, c.Where)
	}
	if c.OrderBy != "name" {
		t.Fatalf("expected orderBy 'name', got '%s'", c.OrderBy)
	}
}

func TestSplitSelectClauses_CommentsAndEscapes(t *testing.T) {
	sql := `/* leading comment */
		SELECT col -- comment with FROM inside
		FROM my_table /* block comment with WHERE inside */
		WHERE val = 'it''s a string with WHERE and ORDER BY'
		ORDER BY col DESC;`
	c := SplitSelectClauses(sql)

	if c.Projection != "col -- comment with FROM inside" {
		t.Fatalf("expected projection 'col -- comment with FROM inside', got '%s'", c.Projection)
	}
	if c.From != "my_table /* block comment with WHERE inside */" {
		t.Fatalf("expected from 'my_table /* block comment with WHERE inside */', got '%s'", c.From)
	}
	if c.Where != "val = 'it''s a string with WHERE and ORDER BY'" {
		t.Fatalf("expected where with string literal intact, got '%s'", c.Where)
	}
	if c.OrderBy != "col DESC;" {
		t.Fatalf("expected orderBy 'col DESC;', got '%s'", c.OrderBy)
	}
}
