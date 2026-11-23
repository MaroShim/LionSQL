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
