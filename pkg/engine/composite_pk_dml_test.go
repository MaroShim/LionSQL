package engine

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestCompositePK_CTEDML_OverwritePrevention(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE _t_cte_comp (k1 INT, k2 INT, val INT, PRIMARY KEY (k1, k2));"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _t_cte_comp VALUES (1, 10, 100), (1, 20, 200);"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 1. CTE UPDATE targeting only (1, 20)
	resUpd, err := coord.ExecuteSQL(`
		WITH cte AS (SELECT k1, k2, val FROM _t_cte_comp)
		UPDATE cte SET val = 999 WHERE k2 = 20;
	`)
	if err != nil {
		t.Fatalf("CTE UPDATE failed: %v", err)
	}
	if resUpd.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", resUpd.RowsAffected)
	}

	resCheck, err := coord.ExecuteSQL("SELECT k1, k2, val FROM _t_cte_comp ORDER BY k1, k2;")
	if err != nil {
		t.Fatalf("select failed: %v", err)
	}
	if len(resCheck.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(resCheck.Rows))
	}

	// (1, 10) must remain 100 (NEVER overwritten!)
	if fmt.Sprintf("%v", resCheck.Rows[0]["val"]) != "100" {
		t.Errorf("data corruption detected: row (1, 10) was overwritten to %v, expected 100", resCheck.Rows[0]["val"])
	}
	// (1, 20) must be updated to 999
	if fmt.Sprintf("%v", resCheck.Rows[1]["val"]) != "999" {
		t.Errorf("expected row (1, 20) to be 999, got %v", resCheck.Rows[1]["val"])
	}

	// 2. CTE DELETE targeting only (1, 20)
	resDel, err := coord.ExecuteSQL(`
		WITH cte AS (SELECT k1, k2, val FROM _t_cte_comp)
		DELETE FROM cte WHERE k2 = 20;
	`)
	if err != nil {
		t.Fatalf("CTE DELETE failed: %v", err)
	}
	if resDel.RowsAffected != 1 {
		t.Errorf("expected 1 row deleted, got %d", resDel.RowsAffected)
	}

	resCheckDel, err := coord.ExecuteSQL("SELECT k1, k2, val FROM _t_cte_comp ORDER BY k1, k2;")
	if err != nil {
		t.Fatalf("select after delete failed: %v", err)
	}
	if len(resCheckDel.Rows) != 1 {
		t.Fatalf("expected 1 row remaining, got %d", len(resCheckDel.Rows))
	}
	if fmt.Sprintf("%v", resCheckDel.Rows[0]["k2"]) != "10" {
		t.Errorf("expected remaining row to have k2=10, got %v", resCheckDel.Rows[0]["k2"])
	}
}

func TestCompositePK_JoinDML_OverwritePrevention(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE _t_b_comp (k1 INT, k2 INT, status VARCHAR(20), PRIMARY KEY (k1, k2));"); err != nil {
		t.Fatalf("create base table failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("CREATE TABLE _t_f_comp (k1 INT, k2 INT, active INT);"); err != nil {
		t.Fatalf("create filter table failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _t_b_comp VALUES (1, 10, 'INIT'), (1, 20, 'INIT');"); err != nil {
		t.Fatalf("insert base failed: %v", err)
	}
	// Only (1, 20) is active
	if _, err := coord.ExecuteSQL("INSERT INTO _t_f_comp VALUES (1, 20, 1), (1, 10, 0);"); err != nil {
		t.Fatalf("insert filter failed: %v", err)
	}

	// 1. UPDATE ... FROM JOIN with composite PK
	resUpd, err := coord.ExecuteSQL(`
		UPDATE b
		SET status = 'ACTIVE'
		FROM _t_b_comp b
		JOIN _t_f_comp f ON b.k1 = f.k1 AND b.k2 = f.k2
		WHERE f.active = 1;
	`)
	if err != nil {
		t.Fatalf("JOIN UPDATE failed: %v", err)
	}
	if resUpd.RowsAffected != 1 {
		t.Errorf("expected 1 row updated, got %d", resUpd.RowsAffected)
	}

	resCheck, err := coord.ExecuteSQL("SELECT k1, k2, status FROM _t_b_comp ORDER BY k1, k2;")
	if err != nil {
		t.Fatalf("select check failed: %v", err)
	}
	if len(resCheck.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(resCheck.Rows))
	}
	// Row (1, 10) must remain 'INIT'
	if fmt.Sprintf("%v", resCheck.Rows[0]["status"]) != "INIT" {
		t.Errorf("data corruption: row (1, 10) overwritten to %v, expected 'INIT'", resCheck.Rows[0]["status"])
	}
	// Row (1, 20) must be 'ACTIVE'
	if fmt.Sprintf("%v", resCheck.Rows[1]["status"]) != "ACTIVE" {
		t.Errorf("row (1, 20) expected 'ACTIVE', got %v", resCheck.Rows[1]["status"])
	}

	// 2. DELETE ... FROM JOIN with composite PK
	resDel, err := coord.ExecuteSQL(`
		DELETE b
		FROM _t_b_comp b
		JOIN _t_f_comp f ON b.k1 = f.k1 AND b.k2 = f.k2
		WHERE f.active = 1;
	`)
	if err != nil {
		t.Fatalf("JOIN DELETE failed: %v", err)
	}
	if resDel.RowsAffected != 1 {
		t.Errorf("expected 1 row deleted, got %d", resDel.RowsAffected)
	}

	resCheckDel, err := coord.ExecuteSQL("SELECT k1, k2, status FROM _t_b_comp ORDER BY k1, k2;")
	if err != nil {
		t.Fatalf("select check after delete failed: %v", err)
	}
	if len(resCheckDel.Rows) != 1 {
		t.Fatalf("expected 1 row remaining, got %d", len(resCheckDel.Rows))
	}
	if fmt.Sprintf("%v", resCheckDel.Rows[0]["k2"]) != "10" {
		t.Errorf("expected remaining row k2=10, got %v", resCheckDel.Rows[0]["k2"])
	}
}
