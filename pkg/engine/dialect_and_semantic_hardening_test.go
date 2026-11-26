package engine

import (
	"context"
	"strings"
	"testing"
)

func TestDialectSwitch_And_Semantics(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()
	ctx := context.Background()

	sessID := "test-dialect-sess"

	// 1. Initial State: Default is T-SQL
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SHOW dialect;")
	if err != nil {
		t.Fatalf("SHOW dialect failed: %v", err)
	}
	if len(res.Rows) == 0 || res.Rows[0]["dialect"] != "tsql" {
		t.Fatalf("expected default dialect 'tsql', got %v", res.Rows)
	}

	// In T-SQL dialect: 'Electronics' + 10 is permitted as string concatenation (or varchar precedence)
	resPlus, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT 'Electronics' + 10 AS res;")
	if err != nil {
		t.Fatalf("expected 'Electronics' + 10 to succeed in T-SQL dialect, got: %v", err)
	}
	if len(resPlus.Rows) == 0 || resPlus.Rows[0]["res"] != "Electronics10" {
		t.Fatalf("expected Electronics10, got %v", resPlus.Rows)
	}

	// In T-SQL dialect: LIKE is Case-Insensitive (CI)
	resLike, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT CASE WHEN 'ABC' LIKE 'abc' THEN 1 ELSE 0 END AS matched;")
	if err != nil {
		t.Fatalf("expected LIKE query to succeed in T-SQL, got: %v", err)
	}
	if len(resLike.Rows) == 0 || resLike.Rows[0]["matched"] != int64(1) {
		t.Fatalf("expected LIKE to be case-insensitive in T-SQL (got %v)", resLike.Rows)
	}

	// 2. Switch to PostgreSQL dialect
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SET DIALECT = 'postgres';")
	if err != nil {
		t.Fatalf("SET DIALECT = 'postgres' failed: %v", err)
	}

	resPG, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SHOW dialect;")
	if err != nil {
		t.Fatalf("SHOW dialect failed after switch: %v", err)
	}
	if len(resPG.Rows) == 0 || resPG.Rows[0]["dialect"] != "postgres" {
		t.Fatalf("expected dialect 'postgres', got %v", resPG.Rows)
	}

	// In PostgreSQL dialect: 'Electronics' + 10 must fail! (operator does not exist, use ||)
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT 'Electronics' + 10 AS res;")
	if err == nil {
		t.Fatalf("expected 'Electronics' + 10 to fail in PostgreSQL dialect, but succeeded")
	}
	if !strings.Contains(err.Error(), "operator does not exist") && !strings.Contains(err.Error(), "||") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// In PostgreSQL dialect: || string concatenation must succeed
	resConcat, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT 'Electronics' || 10 AS res;")
	if err != nil {
		t.Fatalf("expected 'Electronics' || 10 to succeed with ||, got: %v", err)
	}
	if len(resConcat.Rows) == 0 || resConcat.Rows[0]["res"] != "Electronics10" {
		t.Fatalf("expected Electronics10, got %v", resConcat.Rows)
	}

	// In PostgreSQL dialect: LIKE is Case-Sensitive (CS)
	resPGLike, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT CASE WHEN 'ABC' LIKE 'abc' THEN 1 ELSE 0 END AS matched;")
	if err != nil {
		t.Fatalf("LIKE query failed in PG: %v", err)
	}
	if len(resPGLike.Rows) == 0 || resPGLike.Rows[0]["matched"] != int64(0) {
		t.Fatalf("expected LIKE to be case-sensitive in PG (got %v)", resPGLike.Rows)
	}

	// In PostgreSQL dialect: ILIKE is Case-Insensitive
	resPGILike, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT CASE WHEN 'ABC' ILIKE 'abc' THEN 1 ELSE 0 END AS matched;")
	if err != nil {
		t.Fatalf("ILIKE query failed in PG: %v", err)
	}
	if len(resPGILike.Rows) == 0 || resPGILike.Rows[0]["matched"] != int64(1) {
		t.Fatalf("expected ILIKE to match in PG (got %v)", resPGILike.Rows)
	}

	// 3. Switch back to T-SQL
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SET DIALECT = 'tsql';")
	if err != nil {
		t.Fatalf("SET DIALECT = 'tsql' failed: %v", err)
	}
	resBack, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SHOW dialect;")
	if err != nil || len(resBack.Rows) == 0 || resBack.Rows[0]["dialect"] != "tsql" {
		t.Fatalf("expected back to tsql, got %v (err: %v)", resBack, err)
	}
}

func TestSemanticDefects_FailClosed(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()
	ctx := context.Background()

	sessID := "test-failclosed-sess"

	// Defect 1: CTE write query self-deadlock fix verification
	_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
		CREATE TABLE t_cte_acc (id INT PRIMARY KEY, balance INT);
		INSERT INTO t_cte_acc VALUES (1, 100);
	`)
	if err != nil {
		t.Fatalf("setup table failed: %v", err)
	}

	// Executing CTE with UPDATE should not deadlock or hang
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `
		WITH cte_bonus AS (SELECT 50 AS bonus)
		UPDATE t_cte_acc SET balance = balance + 50 WHERE id = 1;
	`)
	if err != nil {
		t.Fatalf("CTE UPDATE failed: %v", err)
	}

	// Defect 2: String division fail-closed verification
	// SELECT 10 / 'hello' should return an explicit error, not NULL
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT 10 / 'hello';")
	if err == nil {
		t.Fatalf("expected 10 / 'hello' to fail with type conversion error, but got nil")
	}

	// SELECT 10 / '2.5' should succeed with 4.0
	resNumStr, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT 10 / '2.5' AS q;")
	if err != nil {
		t.Fatalf("expected 10 / '2.5' to succeed, got %v", err)
	}
	if len(resNumStr.Rows) == 0 {
		t.Fatalf("expected result row for 10 / '2.5'")
	}

	// Defect 3: CASE WHEN branch incompatible types (Int vs String) fail-closed verification
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT CASE WHEN 1 > 0 THEN 1 ELSE 'one' END;")
	if err == nil {
		t.Fatalf("expected CASE WHEN with incompatible branches (INT vs VARCHAR) to fail, but got nil")
	}
}
