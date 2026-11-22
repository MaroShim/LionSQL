package spec_defects

import (
	"context"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestSpecDefect_Postgres_EscapeStringLiteral_E verifies:
// Defect ID: DEFECT-PG-ESCAPE-STRING-LITERAL-1
// Root Cause: PostgreSQL E'...' escape string literals are misparsed as column 'E' and alias,
// causing psql \l (array_to_string(d.datacl, E'\n')) to fail with "expected projection expression, got \n".
func TestSpecDefect_Postgres_EscapeStringLiteral_E(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	defer coord.Close()

	sessID := "TestSpecDefect_PG_EscapeString_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	ctx := context.Background()

	// Case 1: Simple E'hello' should return string 'hello', NOT column header 'hello' with value 'E'
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT E'hello' AS test_col;")
	if err != nil {
		t.Fatalf("unexpected error executing E'hello': %v", err)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("expected 1 row, got 0")
	}
	val := res.Rows[0]["test_col"]
	if val != "hello" {
		t.Errorf("expected value 'hello', got %v (columns=%v)", val, res.Columns)
	}

	// Case 2: E'\n' should return a single newline character
	res2, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT E'\\n' AS nl;")
	if err != nil {
		t.Fatalf("unexpected error executing E'\\n': %v", err)
	}
	if len(res2.Rows) == 0 {
		t.Fatalf("expected 1 row, got 0")
	}
	val2 := res2.Rows[0]["nl"]
	if val2 != "\n" {
		t.Errorf("expected newline '\\n', got %q", val2)
	}

	// Case 3: E'hello\tworld\r\n'
	res3, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT E'hello\\tworld' AS tab_str;")
	if err != nil {
		t.Fatalf("unexpected error executing E'hello\\tworld': %v", err)
	}
	val3 := res3.Rows[0]["tab_str"]
	if val3 != "hello\tworld" {
		t.Errorf("expected 'hello\\tworld', got %q", val3)
	}
}

// TestSpecDefect_Postgres_PgPolicy_VirtualCatalog verifies:
// Defect ID: DEFECT-PG-CATALOG-PG-POLICY-1
// Root Cause: psql \d queries pg_catalog.pg_policy, which fails with "table not found: pg_catalog.pg_policy".
func TestSpecDefect_Postgres_PgPolicy_VirtualCatalog(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	defer coord.Close()

	sessID := "TestSpecDefect_PG_Policy_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	ctx := context.Background()
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT pol.polname FROM pg_catalog.pg_policy pol WHERE pol.polrelid = '16384';")
	if err != nil {
		t.Fatalf("failed querying pg_catalog.pg_policy: %v", err)
	}
	if !res.Success {
		t.Fatalf("query failed on pg_catalog.pg_policy")
	}
}

// TestSpecDefect_Postgres_FilterClause_TableName verifies:
// Defect ID: DEFECT-ENGINE-FILTER-CLAUSE-FROM-TABLE-LOSS-1
// Root Cause: FILTER (WHERE ...) clause in aggregation strips the FROM table identifier,
// resulting in "table not found: ".
func TestSpecDefect_Postgres_FilterClause_TableName(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	defer coord.Close()

	sessID := "TestSpecDefect_FilterClause_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	ctx := context.Background()
	_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_filter_test (id INT, val INT);")
	_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO t_filter_test VALUES (1, 10), (2, 20);")

	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT count(*) FILTER (WHERE id > 1) FROM t_filter_test;")
	if err != nil && strings.Contains(err.Error(), "table not found:") {
		t.Fatalf("TDD RED: table name was lost during FILTER clause parsing: %v", err)
	}
	if err != nil {
		t.Logf("FILTER clause returned: %v (verifying table name not lost)", err)
	} else if len(res.Rows) > 0 {
		t.Logf("FILTER clause result: %v", res.Rows)
	}
}
