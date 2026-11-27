package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestDEFECT_Merge_StatementLevelAtomicRollback_InExplicitTx(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_merge_atomic"
	coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	mustExec := func(sql string) *engine.QueryResult {
		t.Helper()
		res, err := coord.ExecuteSessionSQL(sessID, sql)
		if err != nil {
			t.Fatalf("failed to execute %q: %v", sql, err)
		}
		return res
	}

	mustExec("CREATE TABLE m_target (id INT PRIMARY KEY, val INT UNIQUE);")
	mustExec("INSERT INTO m_target (id, val) VALUES (1, 100), (2, 200);")
	mustExec("CREATE TABLE m_source (id INT PRIMARY KEY, val INT);")
	mustExec("INSERT INTO m_source (id, val) VALUES (3, 300), (4, 100);")

	// Begin explicit transaction
	mustExec("BEGIN TRANSACTION;")

	// Execute MERGE:
	mergeSQL := `
MERGE m_target AS t
USING m_source AS s
ON t.id = s.id
WHEN NOT MATCHED THEN
    INSERT (id, val) VALUES (s.id, s.val);
`
	_, mergeErr := coord.ExecuteSessionSQL(sessID, mergeSQL)
	if mergeErr == nil {
		t.Fatalf("expected MERGE to fail due to unique constraint on val, but succeeded")
	}

	// In ANSI SQL and statement-level atomicity:
	// A failed MERGE statement must roll back its own partial mutations,
	// keeping the transaction intact and active. Row id=3 must NOT be in m_target!
	res := mustExec("SELECT COUNT(*) AS cnt FROM m_target WHERE id = 3;")
	cnt := res.Rows[0]["cnt"]
	if cnt != int64(0) && cnt != 0 {
		t.Fatalf("DEFECT CONFIRMED: MERGE lacked statement-level atomic rollback! Expected id=3 count=0, got: %v", cnt)
	}

	// Transaction should still be committable
	mustExec("COMMIT;")
}

func TestRegression_Merge_ZeroMatch_UpfrontSchemaValidation(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_merge_zero_match"
	coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	mustExec := func(sql string) *engine.QueryResult {
		t.Helper()
		res, err := coord.ExecuteSessionSQL(sessID, sql)
		if err != nil {
			t.Fatalf("failed to execute %q: %v", sql, err)
		}
		return res
	}

	mustExec("CREATE TABLE m_target_empty (id INT PRIMARY KEY, name VARCHAR(50));")
	mustExec("CREATE TABLE m_source_empty (id INT PRIMARY KEY, name VARCHAR(50));")
	// 0 rows in source -> 0 rows will match!

	// 1. UPDATE with ghost_col: Even with 0 matched rows, upfront validation must reject non-existent column!
	sqlBadUpdate := `
MERGE m_target_empty AS t
USING m_source_empty AS s
ON t.id = s.id
WHEN MATCHED THEN
    UPDATE SET t.ghost_col = 'invalid';
`
	if _, err := coord.ExecuteSessionSQL(sessID, sqlBadUpdate); err == nil {
		t.Errorf("expected MERGE with non-existent column ghost_col in WHEN MATCHED UPDATE to fail upfront, got nil")
	}

	// 2. INSERT with non-existent column in WHEN NOT MATCHED: must fail upfront!
	sqlBadInsert := `
MERGE m_target_empty AS t
USING m_source_empty AS s
ON t.id = s.id
WHEN NOT MATCHED THEN
    INSERT (id, invalid_col) VALUES (s.id, 'val');
`
	if _, err := coord.ExecuteSessionSQL(sessID, sqlBadInsert); err == nil {
		t.Errorf("expected MERGE with non-existent column invalid_col in WHEN NOT MATCHED INSERT to fail upfront, got nil")
	}

	// 3. INSERT column count mismatch: must fail upfront!
	sqlColMismatch := `
MERGE m_target_empty AS t
USING m_source_empty AS s
ON t.id = s.id
WHEN NOT MATCHED THEN
    INSERT (id, name) VALUES (s.id);
`
	if _, err := coord.ExecuteSessionSQL(sessID, sqlColMismatch); err == nil {
		t.Errorf("expected MERGE with column count mismatch to fail upfront, got nil")
	}

	// 4. Source nonexistent column in UPDATE SET right-hand expression (0 rows matched):
	// Even though 0 rows match, s.ghost_source_col must be detected upfront and fail!
	sqlBadSourceUpdate := `
MERGE m_target_empty AS t
USING m_source_empty AS s
ON t.id = s.id
WHEN MATCHED THEN
    UPDATE SET t.name = s.ghost_source_col;
`
	if _, err := coord.ExecuteSessionSQL(sessID, sqlBadSourceUpdate); err == nil {
		t.Errorf("expected MERGE with non-existent source column s.ghost_source_col in UPDATE SET to fail upfront, got nil")
	}

	// 5. Source nonexistent column in ON clause:
	sqlBadOn := `
MERGE m_target_empty AS t
USING m_source_empty AS s
ON t.id = s.ghost_source_id
WHEN NOT MATCHED THEN
    INSERT (id, name) VALUES (s.id, s.name);
`
	if _, err := coord.ExecuteSessionSQL(sessID, sqlBadOn); err == nil {
		t.Errorf("expected MERGE with non-existent source column in ON clause to fail upfront, got nil")
	}

	// 6. Source nonexistent column in WHEN MATCHED AND clause:
	sqlBadAnd := `
MERGE m_target_empty AS t
USING m_source_empty AS s
ON t.id = s.id
WHEN MATCHED AND s.ghost_filter = 1 THEN
    DELETE;
`
	if _, err := coord.ExecuteSessionSQL(sessID, sqlBadAnd); err == nil {
		t.Errorf("expected MERGE with non-existent source column in WHEN AND condition to fail upfront, got nil")
	}
}

func TestDEFECT_Merge_Triggers_Executed(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_merge_trig"
	coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	mustExec := func(sql string) *engine.QueryResult {
		t.Helper()
		res, err := coord.ExecuteSessionSQL(sessID, sql)
		if err != nil {
			t.Fatalf("failed to execute %q: %v", sql, err)
		}
		return res
	}

	mustExec("CREATE TABLE m_items (id INT PRIMARY KEY, val INT);")
	mustExec("CREATE TABLE m_audit (msg VARCHAR(100));")

	mustExec(`
CREATE TRIGGER tr_m_items_ins ON m_items AFTER INSERT AS
BEGIN
    INSERT INTO m_audit VALUES ('INS_TRIGGER_FIRED');
END;
`)

	mustExec("CREATE TABLE m_src (id INT PRIMARY KEY, val INT);")
	mustExec("INSERT INTO m_src (id, val) VALUES (10, 100);")

	mergeSQL := `
MERGE m_items AS t
USING m_src AS s
ON t.id = s.id
WHEN NOT MATCHED THEN
    INSERT (id, val) VALUES (s.id, s.val);
`
	mustExec(mergeSQL)

	res := mustExec("SELECT msg FROM m_audit;")
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["msg"]) != "INS_TRIGGER_FIRED" {
		t.Fatalf("DEFECT CONFIRMED: expected 1 audit row 'INS_TRIGGER_FIRED' after MERGE INSERT, got %d rows: %+v", len(res.Rows), res.Rows)
	}
}
