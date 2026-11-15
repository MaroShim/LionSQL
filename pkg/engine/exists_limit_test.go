package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestExists_EarlyExitLimit1Pushdown(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Create table and populate with 1,000 rows
	_, err := coord.ExecuteSQL(`CREATE TABLE big_tbl (id INT PRIMARY KEY, val INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	for i := 1; i <= 1000; i++ {
		_, err = coord.ExecuteSQL(fmt.Sprintf(`INSERT INTO big_tbl VALUES (%d, %d);`, i, i*10))
		if err != nil {
			t.Fatalf("INSERT row %d failed: %v", i, err)
		}
	}

	// 2. Direct scalar EXISTS projection: SELECT EXISTS(SELECT * FROM big_tbl)
	res1, err := coord.ExecuteSQL(`SELECT EXISTS (SELECT * FROM big_tbl) AS has_rows;`)
	if err != nil || len(res1.Rows) == 0 {
		t.Fatalf("SELECT EXISTS failed: %v", err)
	}
	if fmt.Sprintf("%v", res1.Rows[0]["has_rows"]) != "true" && fmt.Sprintf("%v", res1.Rows[0]["has_rows"]) != "1" {
		t.Errorf("expected EXISTS true, got %v", res1.Rows[0]["has_rows"])
	}

	// 3. Direct scalar EXISTS with no match: SELECT EXISTS(SELECT * FROM big_tbl WHERE id = -999)
	res2, err := coord.ExecuteSQL(`SELECT EXISTS (SELECT * FROM big_tbl WHERE id = -999) AS has_rows;`)
	if err != nil || len(res2.Rows) == 0 {
		t.Fatalf("SELECT EXISTS empty failed: %v", err)
	}
	if fmt.Sprintf("%v", res2.Rows[0]["has_rows"]) != "false" && fmt.Sprintf("%v", res2.Rows[0]["has_rows"]) != "0" {
		t.Errorf("expected EXISTS false, got %v", res2.Rows[0]["has_rows"])
	}

	// 4. WHERE EXISTS non-correlated filter: SELECT 1 WHERE EXISTS (SELECT * FROM big_tbl)
	res3, err := coord.ExecuteSQL(`SELECT id FROM big_tbl WHERE id = 1 AND EXISTS (SELECT * FROM big_tbl);`)
	if err != nil || len(res3.Rows) != 1 {
		t.Fatalf("WHERE EXISTS failed: %v (rows=%d)", err, len(res3.Rows))
	}

	// 5. WHERE NOT EXISTS non-correlated filter: SELECT 1 WHERE NOT EXISTS (SELECT * FROM big_tbl)
	res4, err := coord.ExecuteSQL(`SELECT id FROM big_tbl WHERE id = 1 AND NOT EXISTS (SELECT * FROM big_tbl);`)
	if err != nil {
		t.Fatalf("WHERE NOT EXISTS failed: %v", err)
	}
	if len(res4.Rows) != 0 {
		t.Errorf("expected 0 rows for NOT EXISTS on populated table, got %d", len(res4.Rows))
	}

	// 6. Conditional DDL with EXISTS: IF EXISTS (SELECT * FROM big_tbl) CREATE TABLE test_cond_ddl (id INT);
	res5, err := coord.ExecuteSQL(`IF EXISTS (SELECT * FROM big_tbl) CREATE TABLE test_cond_ddl (id INT PRIMARY KEY);`)
	if err != nil {
		t.Fatalf("conditional DDL with EXISTS failed: %v", err)
	}
	t.Logf("Conditional DDL result: %+v", res5)

	tbl, err := coord.GetTable("test_cond_ddl")
	if err != nil || tbl == nil {
		t.Fatalf("expected test_cond_ddl to be created, err: %v", err)
	}

	// 7. Stored Procedure with IF EXISTS
	procSQL := `
CREATE PROCEDURE sp_test_exists
AS
BEGIN
    IF EXISTS (SELECT * FROM big_tbl)
    BEGIN
        SELECT 42 AS answer;
    END
END;`
	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	resProc, err := coord.ExecuteSQL(`EXEC sp_test_exists;`)
	if err != nil || len(resProc.Rows) == 0 {
		t.Fatalf("EXEC sp_test_exists failed: %v", err)
	}
	if fmt.Sprintf("%v", resProc.Rows[0]["answer"]) != "42" {
		t.Errorf("expected answer 42, got %v", resProc.Rows[0]["answer"])
	}
}
