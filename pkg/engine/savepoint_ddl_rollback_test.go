package engine_test

import (
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
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
