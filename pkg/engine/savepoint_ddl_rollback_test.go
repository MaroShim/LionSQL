package engine_test

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestSavepoint_DDLRollbackIntegrity(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_sp_ddl_sess"
	coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	mustExec := func(sql string) {
		t.Helper()
		res, err := coord.ExecuteSessionSQL(sessID, sql)
		if err != nil {
			t.Fatalf("failed to execute %q: %v", sql, err)
		}
		if res != nil && !res.Success {
			t.Fatalf("query %q failed: %v", sql, res.Messages)
		}
	}

	mustExec("BEGIN;")
	mustExec("CREATE TABLE t_sp_ddl (id INT PRIMARY KEY, col_a INT);")
	mustExec("INSERT INTO t_sp_ddl VALUES (1, 10);")

	mustExec("SAVEPOINT sp1;")
	mustExec("ALTER TABLE t_sp_ddl ADD COLUMN col_b INT;")
	mustExec("UPDATE t_sp_ddl SET col_b = 99 WHERE id = 1;")

	mustExec("ROLLBACK TO SAVEPOINT sp1;")
	mustExec("COMMIT;")

	// Verify table schema directly: col_b must NOT exist in Columns!
	sch, err := eng.GetTable("t_sp_ddl")
	if err != nil {
		t.Fatalf("failed to get table schema: %v", err)
	}
	for _, col := range sch.Columns {
		if strings.EqualFold(col.Name, "col_b") {
			t.Fatalf("col_b still exists in table schema after ROLLBACK TO SAVEPOINT!")
		}
	}

	// Verify via SELECT * that only id and col_a are present
	resStar, errStar := coord.ExecuteSessionSQL(sessID, "SELECT * FROM t_sp_ddl;")
	if errStar != nil {
		t.Fatalf("failed to execute SELECT *: %v", errStar)
	}
	if len(resStar.Columns) != 2 || resStar.Columns[0] != "id" || resStar.Columns[1] != "col_a" {
		t.Fatalf("expected columns [id col_a], got %v", resStar.Columns)
	}
}

func TestSavepoint_DropTable_ShadowCatalogRecovery(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_sp_drop_shadow"
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

	mustExec("CREATE TABLE t_drop_sp (id INT PRIMARY KEY, v INT);")
	mustExec("INSERT INTO t_drop_sp VALUES (1, 100);")

	mustExec("BEGIN;")
	mustExec("SAVEPOINT sp_drop;")
	mustExec("DROP TABLE t_drop_sp;")
	mustExec("ROLLBACK TO SAVEPOINT sp_drop;")

	// Table t_drop_sp must be immediately visible inside the SAME active transaction after ROLLBACK TO SAVEPOINT!
	resInTx := mustExec("SELECT v FROM t_drop_sp WHERE id = 1;")
	if len(resInTx.Rows) != 1 || fmt.Sprintf("%v", resInTx.Rows[0]["v"]) != "100" {
		t.Fatalf("expected 1 row with v=100 inside tx after ROLLBACK TO SAVEPOINT, got %+v", resInTx.Rows)
	}
	mustExec("COMMIT;")
}

func TestSavepoint_SetActiveStorageTx_And_DirectTxMgrSavepoint(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_sp_direct_txmgr"
	cs, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	defer coord.CloseSession(sessID)

	if _, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE t_direct_sp (id INT PRIMARY KEY, v INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO t_direct_sp VALUES (1, 10);"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// Simulate transaction attached via SetActiveStorageTx (as done by stored procedure VM)
	stx, err := cs.Session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	cs.TxMgr.SetActiveStorageTx(stx)

	// 1. Calling Savepoint directly on cs.TxMgr must not panic and must also create savepoint on StorageTx
	if err := cs.TxMgr.Savepoint("sp_direct"); err != nil {
		t.Fatalf("cs.TxMgr.Savepoint failed: %v", err)
	}

	// 2. Mutate storage inside savepoint
	if err := stx.Insert("t_direct_sp", storage.Record{"id": storage.IntValue(2), "v": storage.IntValue(20)}); err != nil {
		t.Fatalf("stx.Insert failed: %v", err)
	}

	// 3. Calling RollbackTo directly on cs.TxMgr must also roll back StorageTx DML mutations!
	if err := cs.TxMgr.RollbackTo("sp_direct"); err != nil {
		t.Fatalf("cs.TxMgr.RollbackTo failed: %v", err)
	}
	if err := cs.TxMgr.Commit(nil); err != nil {
		t.Fatalf("cs.TxMgr.Commit failed: %v", err)
	}

	res, err := coord.ExecuteSessionSQL(sessID, "SELECT id, v FROM t_direct_sp ORDER BY id;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row (id=2 rolled back by cs.TxMgr.RollbackTo), got %d rows: %+v", len(res.Rows), res.Rows)
	}
}
