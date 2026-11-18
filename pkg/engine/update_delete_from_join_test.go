package engine

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestDML_DeleteFromJoin_Propagation(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE _b_del_ord (id INT PRIMARY KEY, cust_id INT);"); err != nil {
		t.Fatalf("create _b_del_ord failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("CREATE TABLE _b_del_cust (id INT PRIMARY KEY, tier VARCHAR(10));"); err != nil {
		t.Fatalf("create _b_del_cust failed: %v", err)
	}

	if _, err := coord.ExecuteSQL("INSERT INTO _b_del_cust VALUES (1, 'VIP'), (2, 'NORMAL');"); err != nil {
		t.Fatalf("insert cust failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _b_del_ord VALUES (101, 1), (102, 2), (103, 1);"); err != nil {
		t.Fatalf("insert ord failed: %v", err)
	}

	// Delete VIP orders (101, 103)
	res, err := coord.ExecuteSQL(`
DELETE _b_del_ord
FROM _b_del_ord
JOIN _b_del_cust ON _b_del_ord.cust_id = _b_del_cust.id
WHERE _b_del_cust.tier = 'VIP';
`)
	if err != nil {
		t.Fatalf("DELETE FROM JOIN failed: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Fatalf("expected 2 rows affected, got %d", res.RowsAffected)
	}

	// Verify remaining rows in target table
	selRes, err := coord.ExecuteSQL("SELECT id, cust_id FROM _b_del_ord ORDER BY id;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(selRes.Rows) != 1 {
		t.Fatalf("expected 1 remaining row, got %d (rows: %+v)", len(selRes.Rows), selRes.Rows)
	}
	if fmt.Sprintf("%v", selRes.Rows[0]["id"]) != "102" {
		t.Errorf("expected remaining order id 102, got %v", selRes.Rows[0]["id"])
	}
}

func TestDML_UpdateFromJoin_Propagation(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE _b_upd_ord (id INT PRIMARY KEY, cust_id INT, amount INT);"); err != nil {
		t.Fatalf("create _b_upd_ord failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("CREATE TABLE _b_upd_cust (id INT PRIMARY KEY, tier VARCHAR(10));"); err != nil {
		t.Fatalf("create _b_upd_cust failed: %v", err)
	}

	if _, err := coord.ExecuteSQL("INSERT INTO _b_upd_cust VALUES (1, 'VIP'), (2, 'NORMAL');"); err != nil {
		t.Fatalf("insert cust failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _b_upd_ord VALUES (101, 1, 100), (102, 2, 200), (103, 1, 300);"); err != nil {
		t.Fatalf("insert ord failed: %v", err)
	}

	// Update VIP orders
	res, err := coord.ExecuteSQL(`
UPDATE _b_upd_ord
SET amount = 999
FROM _b_upd_ord
JOIN _b_upd_cust ON _b_upd_ord.cust_id = _b_upd_cust.id
WHERE _b_upd_cust.tier = 'VIP';
`)
	if err != nil {
		t.Fatalf("UPDATE FROM JOIN failed: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Fatalf("expected 2 rows affected, got %d", res.RowsAffected)
	}

	// Verify updated values in target table
	selRes, err := coord.ExecuteSQL("SELECT id, amount FROM _b_upd_ord ORDER BY id;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(selRes.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(selRes.Rows))
	}
	if fmt.Sprintf("%v", selRes.Rows[0]["amount"]) != "999" || fmt.Sprintf("%v", selRes.Rows[2]["amount"]) != "999" {
		t.Errorf("expected 101 and 103 updated to 999, got: %+v", selRes.Rows)
	}
	if fmt.Sprintf("%v", selRes.Rows[1]["amount"]) != "200" {
		t.Errorf("expected 102 to remain 200, got %v", selRes.Rows[1]["amount"])
	}
}
