package engine

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestCTE_UpdatableDML(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// 1. Setup tables
	if _, err := coord.ExecuteSQL("CREATE TABLE _t_dupes (id INT PRIMARY KEY, cust_id INT);"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _t_dupes VALUES (1, 10), (2, 10), (3, 20);"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 2. Updatable CTE DELETE with ROW_NUMBER partition
	cteDeleteSQL := `
	WITH dupes AS (
		SELECT id, ROW_NUMBER() OVER (PARTITION BY cust_id ORDER BY id) AS rn
		FROM _t_dupes
	)
	DELETE FROM dupes WHERE rn > 1;
	`
	resDel, err := coord.ExecuteSQL(cteDeleteSQL)
	if err != nil {
		t.Fatalf("CTE DELETE failed: %v", err)
	}
	if resDel.RowsAffected != 1 {
		t.Errorf("expected 1 row deleted, got %d", resDel.RowsAffected)
	}

	// Verify remaining rows in underlying table _t_dupes
	resRemaining, err := coord.ExecuteSQL("SELECT id, cust_id FROM _t_dupes ORDER BY id ASC;")
	if err != nil {
		t.Fatalf("select remaining failed: %v", err)
	}
	if len(resRemaining.Rows) != 2 {
		t.Fatalf("expected 2 rows remaining in _t_dupes, got %d", len(resRemaining.Rows))
	}
	if resRemaining.Rows[0]["id"] != int64(1) || resRemaining.Rows[1]["id"] != int64(3) {
		t.Errorf("expected remaining rows with id 1 and 3, got: %v", resRemaining.Rows)
	}

	// 3. Updatable CTE UPDATE
	cteUpdateSQL := `
	WITH up_target AS (
		SELECT id, cust_id FROM _t_dupes
	)
	UPDATE up_target SET cust_id = 999 WHERE id = 1;
	`
	resUpd, err := coord.ExecuteSQL(cteUpdateSQL)
	if err != nil {
		t.Fatalf("CTE UPDATE failed: %v", err)
	}
	if resUpd.RowsAffected != 1 {
		t.Errorf("expected 1 row updated, got %d", resUpd.RowsAffected)
	}

	// Verify updated value in underlying table _t_dupes
	resCheck, err := coord.ExecuteSQL("SELECT cust_id FROM _t_dupes WHERE id = 1;")
	if err != nil {
		t.Fatalf("select check failed: %v", err)
	}
	if resCheck.Rows[0]["cust_id"] != int64(999) {
		t.Errorf("expected cust_id 999, got: %v", resCheck.Rows[0]["cust_id"])
	}
}
