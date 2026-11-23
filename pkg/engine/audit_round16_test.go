package engine_test

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestAuditRound16_DMLTriggerOnReturningAndSubquery(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	mustExec := func(sql string) *engine.QueryResult {
		t.Helper()
		res, err := coord.ExecuteSQL(sql)
		if err != nil {
			t.Fatalf("failed to execute %q: %v", sql, err)
		}
		return res
	}

	mustExec("CREATE TABLE t_src (id INT PRIMARY KEY, val INT);")
	mustExec("CREATE TABLE t_audit (msg VARCHAR(100));")

	mustExec(`
CREATE TRIGGER trg_audit_upd ON t_src AFTER UPDATE AS
BEGIN
    INSERT INTO t_audit VALUES ('UPD_TRIGGER_FIRED');
END;
`)
	mustExec(`
CREATE TRIGGER trg_audit_del ON t_src AFTER DELETE AS
BEGIN
    INSERT INTO t_audit VALUES ('DEL_TRIGGER_FIRED');
END;
`)
	mustExec(`
CREATE TRIGGER trg_audit_ins ON t_src AFTER INSERT AS
BEGIN
    INSERT INTO t_audit VALUES ('INS_TRIGGER_FIRED');
END;
`)

	// 1. INSERT with RETURNING * must fire AFTER INSERT trigger
	mustExec("INSERT INTO t_src VALUES (1, 10) RETURNING *;")
	res1 := mustExec("SELECT msg FROM t_audit;")
	if len(res1.Rows) != 1 || fmt.Sprintf("%v", res1.Rows[0]["msg"]) != "INS_TRIGGER_FIRED" {
		t.Fatalf("expected 1 audit row 'INS_TRIGGER_FIRED' after INSERT ... RETURNING, got %d rows: %+v", len(res1.Rows), res1.Rows)
	}

	// 2. UPDATE with RETURNING * must fire AFTER UPDATE trigger
	mustExec("UPDATE t_src SET val = 999 WHERE id = 1 RETURNING *;")
	res2 := mustExec("SELECT msg FROM t_audit;")
	if len(res2.Rows) != 2 {
		t.Fatalf("expected 2 audit rows after UPDATE ... RETURNING, got %d rows: %+v", len(res2.Rows), res2.Rows)
	}

	// 3. UPDATE with subquery must fire AFTER UPDATE trigger
	mustExec("UPDATE t_src SET val = (SELECT 555) WHERE id = 1;")
	res3 := mustExec("SELECT msg FROM t_audit;")
	if len(res3.Rows) != 3 {
		t.Fatalf("expected 3 audit rows after UPDATE with subquery, got %d rows: %+v", len(res3.Rows), res3.Rows)
	}

	// 4. DELETE with RETURNING * must fire AFTER DELETE trigger
	mustExec("DELETE FROM t_src WHERE id = 1 RETURNING *;")
	res4 := mustExec("SELECT msg FROM t_audit;")
	if len(res4.Rows) != 4 {
		t.Fatalf("expected 4 audit rows after DELETE ... RETURNING, got %d rows: %+v", len(res4.Rows), res4.Rows)
	}
}

func TestAuditRound16_OrderByNullsFirstLast(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	mustExec := func(sql string) *engine.QueryResult {
		t.Helper()
		res, err := coord.ExecuteSQL(sql)
		if err != nil {
			t.Fatalf("failed to execute %q: %v", sql, err)
		}
		return res
	}

	mustExec("CREATE TABLE t_nulls (id INT PRIMARY KEY, val INT);")
	mustExec("INSERT INTO t_nulls VALUES (1, NULL), (2, 10), (3, 20);")

	// 1. ORDER BY val NULLS LAST -> [10, 20, NULL]
	res1 := mustExec("SELECT val FROM t_nulls ORDER BY val NULLS LAST;")
	if len(res1.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res1.Rows))
	}
	if fmt.Sprintf("%v", res1.Rows[0]["val"]) != "10" ||
		fmt.Sprintf("%v", res1.Rows[1]["val"]) != "20" ||
		res1.Rows[2]["val"] != nil {
		t.Fatalf("expected [10, 20, <nil>] for ORDER BY val NULLS LAST, got [%v, %v, %v]",
			res1.Rows[0]["val"], res1.Rows[1]["val"], res1.Rows[2]["val"])
	}

	// 2. ORDER BY val DESC NULLS FIRST -> [NULL, 20, 10]
	res2 := mustExec("SELECT val FROM t_nulls ORDER BY val DESC NULLS FIRST;")
	if len(res2.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res2.Rows))
	}
	if res2.Rows[0]["val"] != nil ||
		fmt.Sprintf("%v", res2.Rows[1]["val"]) != "20" ||
		fmt.Sprintf("%v", res2.Rows[2]["val"]) != "10" {
		t.Fatalf("expected [<nil>, 20, 10] for ORDER BY val DESC NULLS FIRST, got [%v, %v, %v]",
			res2.Rows[0]["val"], res2.Rows[1]["val"], res2.Rows[2]["val"])
	}

	// 3. Derived VALUES table: SELECT val FROM (VALUES (NULL), (10), (20)) t(val) ORDER BY val NULLS LAST;
	res3 := mustExec("SELECT val FROM (VALUES (NULL), (10), (20)) t(val) ORDER BY val NULLS LAST;")
	if len(res3.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res3.Rows))
	}
	if fmt.Sprintf("%v", res3.Rows[0]["val"]) != "10" ||
		fmt.Sprintf("%v", res3.Rows[1]["val"]) != "20" ||
		res3.Rows[2]["val"] != nil {
		t.Fatalf("expected [10, 20, <nil>] for VALUES ORDER BY val NULLS LAST, got [%v, %v, %v]",
			res3.Rows[0]["val"], res3.Rows[1]["val"], res3.Rows[2]["val"])
	}
}

func TestAuditRound16_ShowTransactionIsolationLevel(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_iso_sess"
	coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	_, err := coord.ExecuteSessionSQL(sessID, "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE;")
	if err != nil {
		t.Fatalf("SET TRANSACTION ISOLATION LEVEL failed: %v", err)
	}

	res, err := coord.ExecuteSessionSQL(sessID, "SHOW TRANSACTION ISOLATION LEVEL;")
	if err != nil {
		t.Fatalf("SHOW TRANSACTION ISOLATION LEVEL failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row from SHOW, got %+v", res)
	}
	got := strings.ToLower(fmt.Sprintf("%v", res.Rows[0]["transaction isolation level"]))
	if got != "serializable" {
		t.Fatalf("expected 'serializable', got %q", got)
	}
}
