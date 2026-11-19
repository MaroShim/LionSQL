package engine_test

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestCoordinator_ProcViewAndCTEQuery(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	ctx := context.Background()
	sessID := "tsql-sess-proc-view"

	// 1. Setup base table and view
	setupSQL := []string{
		"CREATE TABLE t_view_base (id INT PRIMARY KEY, val INT);",
		"INSERT INTO t_view_base VALUES (1, 10), (2, 20);",
		"CREATE VIEW v_test_base AS SELECT id, val, val * 2 AS double_val FROM t_view_base;",
	}
	for _, sql := range setupSQL {
		if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, sql); err != nil {
			t.Fatalf("setup SQL %q failed: %v", sql, err)
		}
	}

	// 2. Test Stored Procedure querying VIEW directly
	createProc1 := `
CREATE PROCEDURE sp_TestViewInsideProc
AS
BEGIN
    SELECT id, val, double_val FROM v_test_base WHERE double_val >= 20 ORDER BY id;
END;
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, createProc1); err != nil {
		t.Fatalf("create sp_TestViewInsideProc failed: %v", err)
	}

	res1, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_TestViewInsideProc;")
	if err != nil {
		t.Fatalf("exec sp_TestViewInsideProc failed: %v", err)
	}
	if len(res1.Rows) != 2 {
		t.Fatalf("expected 2 rows from sp_TestViewInsideProc, got %d rows: %+v", len(res1.Rows), res1.Rows)
	}
	if res1.Rows[0]["id"] != int64(1) || res1.Rows[0]["double_val"] != int64(20) {
		t.Errorf("unexpected row 0: %+v", res1.Rows[0])
	}
	if res1.Rows[1]["id"] != int64(2) || res1.Rows[1]["double_val"] != int64(40) {
		t.Errorf("unexpected row 1: %+v", res1.Rows[1])
	}

	// 3. Test Stored Procedure querying CTE (WITH ... SELECT)
	createProc2 := `
CREATE PROCEDURE sp_TestCTEInsideProc
AS
BEGIN
    WITH cte AS (
        SELECT id, val FROM t_view_base WHERE id = 1
    )
    SELECT id, val FROM cte;
END;
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, createProc2); err != nil {
		t.Fatalf("create sp_TestCTEInsideProc failed: %v", err)
	}

	res2, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_TestCTEInsideProc;")
	if err != nil {
		t.Fatalf("exec sp_TestCTEInsideProc failed: %v", err)
	}
	if len(res2.Rows) != 1 {
		t.Fatalf("expected 1 row from sp_TestCTEInsideProc, got %d rows: %+v", len(res2.Rows), res2.Rows)
	}
	if res2.Rows[0]["id"] != int64(1) || res2.Rows[0]["val"] != int64(10) {
		t.Errorf("unexpected CTE row 0: %+v", res2.Rows[0])
	}

	// 4. Test Stored Procedure joining table and view
	createProc3 := `
CREATE PROCEDURE sp_TestViewJoinInsideProc
AS
BEGIN
    SELECT t.id, t.val, v.double_val
    FROM t_view_base t
    JOIN v_test_base v ON t.id = v.id
    ORDER BY t.id;
END;
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, createProc3); err != nil {
		t.Fatalf("create sp_TestViewJoinInsideProc failed: %v", err)
	}

	res3, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_TestViewJoinInsideProc;")
	if err != nil {
		t.Fatalf("exec sp_TestViewJoinInsideProc failed: %v", err)
	}
	if len(res3.Rows) != 2 {
		t.Fatalf("expected 2 rows from sp_TestViewJoinInsideProc, got %d rows: %+v", len(res3.Rows), res3.Rows)
	}

	// 5. Test Stored Procedure assigning variable from view
	createProc4 := `
CREATE PROCEDURE sp_TestViewAssignInsideProc
AS
BEGIN
    DECLARE @dv INT;
    SELECT @dv = double_val FROM v_test_base WHERE id = 2;
    SELECT @dv AS dv;
END;
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, createProc4); err != nil {
		t.Fatalf("create sp_TestViewAssignInsideProc failed: %v", err)
	}

	res4, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_TestViewAssignInsideProc;")
	if err != nil {
		t.Fatalf("exec sp_TestViewAssignInsideProc failed: %v", err)
	}
	if len(res4.Rows) != 1 {
		t.Fatalf("expected 1 row from sp_TestViewAssignInsideProc, got %d rows: %+v", len(res4.Rows), res4.Rows)
	}
	if res4.Rows[0]["dv"] != int64(40) {
		t.Errorf("unexpected dv value: %+v", res4.Rows[0])
	}
}
