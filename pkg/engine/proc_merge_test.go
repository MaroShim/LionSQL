package engine_test

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestCoordinator_ProcMergeStatement(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	ctx := context.Background()
	sessID := "tsql-sess-proc-merge"

	// 1. Setup tables
	setupSQL := []string{
		"CREATE TABLE t_merge_tgt (id INT PRIMARY KEY, val INT, status VARCHAR(20));",
		"CREATE TABLE t_merge_src (id INT PRIMARY KEY, val INT, status VARCHAR(20));",
		"INSERT INTO t_merge_tgt VALUES (1, 10, 'old'), (2, 20, 'old');",
		"INSERT INTO t_merge_src VALUES (2, 25, 'updated'), (3, 30, 'new');",
	}
	for _, sql := range setupSQL {
		if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, sql); err != nil {
			t.Fatalf("setup SQL %q failed: %v", sql, err)
		}
	}

	// 2. Create Stored Procedure containing MERGE INTO
	createProc := `
CREATE PROCEDURE sp_TestMergeInsideProc
AS
BEGIN
    MERGE INTO t_merge_tgt AS t
    USING t_merge_src AS s
    ON t.id = s.id
    WHEN MATCHED THEN
        UPDATE SET val = s.val, status = s.status
    WHEN NOT MATCHED THEN
        INSERT (id, val, status) VALUES (s.id, s.val, s.status);
END;
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, createProc); err != nil {
		t.Fatalf("create sp_TestMergeInsideProc failed: %v", err)
	}

	// 3. Execute procedure
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_TestMergeInsideProc;"); err != nil {
		t.Fatalf("exec sp_TestMergeInsideProc failed: %v", err)
	}

	// 4. Verify target table contents
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, val, status FROM t_merge_tgt ORDER BY id;")
	if err != nil {
		t.Fatalf("SELECT t_merge_tgt failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows after MERGE in proc, got %d rows: %+v", len(res.Rows), res.Rows)
	}

	// Row 1: untouched (id=1, val=10, status='old')
	if res.Rows[0]["id"] != int64(1) || res.Rows[0]["val"] != int64(10) || res.Rows[0]["status"] != "old" {
		t.Errorf("unexpected row 0: %+v", res.Rows[0])
	}
	// Row 2: updated (id=2, val=25, status='updated')
	if res.Rows[1]["id"] != int64(2) || res.Rows[1]["val"] != int64(25) || res.Rows[1]["status"] != "updated" {
		t.Errorf("unexpected row 1: %+v", res.Rows[1])
	}
	// Row 3: inserted (id=3, val=30, status='new')
	if res.Rows[2]["id"] != int64(3) || res.Rows[2]["val"] != int64(30) || res.Rows[2]["status"] != "new" {
		t.Errorf("unexpected row 2: %+v", res.Rows[2])
	}

	// 5. Test MERGE with transaction rollback inside stored procedure
	createProcRollback := `
CREATE PROCEDURE sp_TestMergeRollbackInsideProc
AS
BEGIN
    BEGIN TRANSACTION;
    MERGE INTO t_merge_tgt AS t
    USING (SELECT 4 AS id, 40 AS val, 'temp' AS status) AS s
    ON t.id = s.id
    WHEN NOT MATCHED THEN
        INSERT (id, val, status) VALUES (s.id, s.val, s.status);
    ROLLBACK TRANSACTION;
END;
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, createProcRollback); err != nil {
		t.Fatalf("create sp_TestMergeRollbackInsideProc failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_TestMergeRollbackInsideProc;"); err != nil {
		t.Fatalf("exec sp_TestMergeRollbackInsideProc failed: %v", err)
	}

	resAfterRollback, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT COUNT(*) AS cnt FROM t_merge_tgt WHERE id = 4;")
	if err != nil {
		t.Fatalf("count check failed: %v", err)
	}
	if resAfterRollback.Rows[0]["cnt"] != int64(0) {
		t.Errorf("expected 0 rows for rolled back merge, got: %+v", resAfterRollback.Rows[0])
	}
}
