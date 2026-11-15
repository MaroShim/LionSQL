package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestUpdate_ColumnSwapAtomicSnapshot(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL(`CREATE TABLE swap_tbl (id INT PRIMARY KEY, a INT, b INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO swap_tbl (id, a, b) VALUES (1, 10, 20);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. Basic Column Swap: SET a = b, b = a
	_, err = coord.ExecuteSQL(`UPDATE swap_tbl SET a = b, b = a WHERE id = 1;`)
	if err != nil {
		t.Fatalf("UPDATE SET a = b, b = a failed: %v", err)
	}

	res, err := coord.ExecuteSQL(`SELECT a, b FROM swap_tbl WHERE id = 1;`)
	if err != nil || len(res.Rows) == 0 {
		t.Fatalf("SELECT failed: %v", err)
	}
	t.Logf("Result after SET a = b, b = a: %+v", res.Rows[0])
	if fmt.Sprintf("%v", res.Rows[0]["a"]) != "20" || fmt.Sprintf("%v", res.Rows[0]["b"]) != "10" {
		t.Errorf("expected a=20, b=10 (swapped), got a=%v, b=%v", res.Rows[0]["a"], res.Rows[0]["b"])
	}

	// 2. Self-referential arithmetic: SET a = a + b, b = a
	// Original: a=20, b=10.
	// Expected: a = 20 + 10 = 30, b = 20 (original a, NOT new a 30)
	_, err = coord.ExecuteSQL(`UPDATE swap_tbl SET a = a + b, b = a WHERE id = 1;`)
	if err != nil {
		t.Fatalf("UPDATE SET a = a + b, b = a failed: %v", err)
	}
	res2, err := coord.ExecuteSQL(`SELECT a, b FROM swap_tbl WHERE id = 1;`)
	if err != nil || len(res2.Rows) == 0 {
		t.Fatalf("SELECT failed: %v", err)
	}
	t.Logf("Result after SET a = a + b, b = a: %+v", res2.Rows[0])
	if fmt.Sprintf("%v", res2.Rows[0]["a"]) != "30" || fmt.Sprintf("%v", res2.Rows[0]["b"]) != "20" {
		t.Errorf("expected a=30, b=20, got a=%v, b=%v", res2.Rows[0]["a"], res2.Rows[0]["b"])
	}

	// 3. Test with OUTPUT clause (bypasses VDBE and uses sqlexec_dml.go path)
	_, err = coord.ExecuteSQL(`INSERT INTO swap_tbl (id, a, b) VALUES (2, 100, 200);`)
	if err != nil {
		t.Fatalf("INSERT row 2 failed: %v", err)
	}

	resOut, err := coord.ExecuteSQL(`UPDATE swap_tbl SET a = b, b = a OUTPUT INSERTED.a, INSERTED.b WHERE id = 2;`)
	if err != nil || len(resOut.Rows) == 0 {
		t.Fatalf("UPDATE with OUTPUT failed: %v", err)
	}
	t.Logf("OUTPUT result: %+v", resOut.Rows[0])

	res3, err := coord.ExecuteSQL(`SELECT a, b FROM swap_tbl WHERE id = 2;`)
	if err != nil || len(res3.Rows) == 0 {
		t.Fatalf("SELECT row 2 failed: %v", err)
	}
	t.Logf("Result after UPDATE with OUTPUT: %+v", res3.Rows[0])
	if fmt.Sprintf("%v", res3.Rows[0]["a"]) != "200" || fmt.Sprintf("%v", res3.Rows[0]["b"]) != "100" {
		t.Errorf("expected row 2 a=200, b=100, got a=%v, b=%v", res3.Rows[0]["a"], res3.Rows[0]["b"])
	}

	// 4. Multi-row UPDATE without WHERE clause (all rows swap simultaneously)
	_, err = coord.ExecuteSQL(`UPDATE swap_tbl SET a = b, b = a;`)
	if err != nil {
		t.Fatalf("multi-row UPDATE without WHERE failed: %v", err)
	}

	resAll, err := coord.ExecuteSQL(`SELECT id, a, b FROM swap_tbl ORDER BY id ASC;`)
	if err != nil || len(resAll.Rows) != 2 {
		t.Fatalf("SELECT all failed: %v", err)
	}
	t.Logf("All rows after swap: %+v", resAll.Rows)
	// Row 1 was a=30, b=20 -> after swap: a=20, b=30
	if fmt.Sprintf("%v", resAll.Rows[0]["a"]) != "20" || fmt.Sprintf("%v", resAll.Rows[0]["b"]) != "30" {
		t.Errorf("row 1 after full table swap: expected a=20, b=30, got a=%v, b=%v", resAll.Rows[0]["a"], resAll.Rows[0]["b"])
	}
	// Row 2 was a=200, b=100 -> after swap: a=100, b=200
	if fmt.Sprintf("%v", resAll.Rows[1]["a"]) != "100" || fmt.Sprintf("%v", resAll.Rows[1]["b"]) != "200" {
		t.Errorf("row 2 after full table swap: expected a=100, b=200, got a=%v, b=%v", resAll.Rows[1]["a"], resAll.Rows[1]["b"])
	}

	// 5. Stored Procedure with swap
	procSQL := `
CREATE PROCEDURE sp_swap_test (@id INT)
AS
BEGIN
    UPDATE swap_tbl SET a = b, b = a WHERE id = @id;
END;`
	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	if _, err := coord.ExecuteSQL(`EXEC sp_swap_test @id = 1;`); err != nil {
		t.Fatalf("EXEC sp_swap_test failed: %v", err)
	}

	resProc, err := coord.ExecuteSQL(`SELECT a, b FROM swap_tbl WHERE id = 1;`)
	if err != nil || len(resProc.Rows) == 0 {
		t.Fatalf("SELECT after proc failed: %v", err)
	}
	t.Logf("Row 1 after proc swap: %+v", resProc.Rows[0])
	// Row 1 was a=20, b=30 -> after proc swap: a=30, b=20
	if fmt.Sprintf("%v", resProc.Rows[0]["a"]) != "30" || fmt.Sprintf("%v", resProc.Rows[0]["b"]) != "20" {
		t.Errorf("row 1 after proc swap: expected a=30, b=20, got a=%v, b=%v", resProc.Rows[0]["a"], resProc.Rows[0]["b"])
	}

	// 6. Table without explicit PRIMARY KEY (uses synthetic _rowid)
	_, err = coord.ExecuteSQL(`CREATE TABLE no_pk_swap (a INT, b INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE no_pk_swap failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO no_pk_swap VALUES (10, 20);`)
	if err != nil {
		t.Fatalf("INSERT into no_pk_swap failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`UPDATE no_pk_swap SET a = b, b = a;`)
	if err != nil {
		t.Fatalf("UPDATE no_pk_swap failed: %v", err)
	}

	resNoPK, err := coord.ExecuteSQL(`SELECT a, b FROM no_pk_swap;`)
	if err != nil || len(resNoPK.Rows) == 0 {
		t.Fatalf("SELECT no_pk_swap failed: %v", err)
	}
	t.Logf("no_pk_swap after swap: %+v", resNoPK.Rows[0])
	if fmt.Sprintf("%v", resNoPK.Rows[0]["a"]) != "20" || fmt.Sprintf("%v", resNoPK.Rows[0]["b"]) != "10" {
		t.Errorf("expected no_pk a=20, b=10, got a=%v, b=%v", resNoPK.Rows[0]["a"], resNoPK.Rows[0]["b"])
	}

	// 7. Order-independent snapshot evaluation: SET b = a, a = a + b
	// Reset row 1 to a=10, b=20
	_, err = coord.ExecuteSQL(`UPDATE swap_tbl SET a = 10, b = 20 WHERE id = 1;`)
	if err != nil {
		t.Fatalf("reset row 1 failed: %v", err)
	}

	// b is assigned first to a (10). If intermediate update is leaked, a = a + b would use b=10 instead of original b=20!
	// Snapshot semantics: b = old a (10), a = old a + old b (10 + 20 = 30).
	_, err = coord.ExecuteSQL(`UPDATE swap_tbl SET b = a, a = a + b WHERE id = 1;`)
	if err != nil {
		t.Fatalf("UPDATE SET b = a, a = a + b failed: %v", err)
	}

	resRev, err := coord.ExecuteSQL(`SELECT a, b FROM swap_tbl WHERE id = 1;`)
	if err != nil || len(resRev.Rows) == 0 {
		t.Fatalf("SELECT after reversed order failed: %v", err)
	}
	t.Logf("Result after SET b = a, a = a + b: %+v", resRev.Rows[0])
	if fmt.Sprintf("%v", resRev.Rows[0]["a"]) != "30" || fmt.Sprintf("%v", resRev.Rows[0]["b"]) != "10" {
		t.Errorf("expected a=30, b=10, got a=%v, b=%v", resRev.Rows[0]["a"], resRev.Rows[0]["b"])
	}

	// 8. Test the same with OUTPUT (sqlexec_dml.go path)
	_, err = coord.ExecuteSQL(`UPDATE swap_tbl SET a = 100, b = 200 WHERE id = 2;`)
	if err != nil {
		t.Fatalf("reset row 2 failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`UPDATE swap_tbl SET b = a, a = a + b OUTPUT INSERTED.a, INSERTED.b WHERE id = 2;`)
	if err != nil {
		t.Fatalf("UPDATE with OUTPUT SET b = a, a = a + b failed: %v", err)
	}

	resRevOut, err := coord.ExecuteSQL(`SELECT a, b FROM swap_tbl WHERE id = 2;`)
	if err != nil || len(resRevOut.Rows) == 0 {
		t.Fatalf("SELECT row 2 after reversed order with OUTPUT failed: %v", err)
	}
	t.Logf("Result row 2 after SET b = a, a = a + b with OUTPUT: %+v", resRevOut.Rows[0])
	if fmt.Sprintf("%v", resRevOut.Rows[0]["a"]) != "300" || fmt.Sprintf("%v", resRevOut.Rows[0]["b"]) != "100" {
		t.Errorf("expected row 2 a=300, b=100, got a=%v, b=%v", resRevOut.Rows[0]["a"], resRevOut.Rows[0]["b"])
	}
}
