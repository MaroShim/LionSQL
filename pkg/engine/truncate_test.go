package engine

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestTruncateMultipleTablesAndCascade(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// 1. Create parent and child tables with foreign key
	if _, err := coord.ExecuteSQL("CREATE TABLE authors (id INT PRIMARY KEY, name VARCHAR(100));"); err != nil {
		t.Fatalf("CREATE TABLE authors failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("CREATE TABLE books (id INT PRIMARY KEY, author_id INT, title VARCHAR(100), FOREIGN KEY (author_id) REFERENCES authors(id));"); err != nil {
		t.Fatalf("CREATE TABLE books failed: %v", err)
	}

	// Insert data
	if _, err := coord.ExecuteSQL("INSERT INTO authors VALUES (1, 'Alice'), (2, 'Bob');"); err != nil {
		t.Fatalf("INSERT authors failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO books VALUES (10, 1, 'Book A'), (20, 2, 'Book B');"); err != nil {
		t.Fatalf("INSERT books failed: %v", err)
	}

	// 2. Attempt TRUNCATE authors without CASCADE should fail because books references authors
	_, err := coord.ExecuteSQL("TRUNCATE TABLE authors;")
	if err == nil {
		t.Fatalf("expected error truncating referenced table without cascade, got nil")
	}

	// 3. Multi-table TRUNCATE: TRUNCATE TABLE authors, books should succeed because child is also truncated
	res, err := coord.ExecuteSQL("TRUNCATE TABLE authors, books;")
	if err != nil {
		t.Fatalf("TRUNCATE TABLE authors, books failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success")
	}

	// Verify both tables are empty
	resA, _ := coord.ExecuteSQL("SELECT COUNT(*) AS c FROM authors;")
	if fmt.Sprintf("%v", resA.Rows[0]["c"]) != "0" {
		t.Fatalf("expected 0 authors, got %v", resA.Rows[0]["c"])
	}
	resB, _ := coord.ExecuteSQL("SELECT COUNT(*) AS c FROM books;")
	if fmt.Sprintf("%v", resB.Rows[0]["c"]) != "0" {
		t.Fatalf("expected 0 books, got %v", resB.Rows[0]["c"])
	}

	// 4. Test TRUNCATE with CASCADE
	coord.ExecuteSQL("INSERT INTO authors VALUES (1, 'Alice');")
	coord.ExecuteSQL("INSERT INTO books VALUES (10, 1, 'Book A');")

	resCascade, err := coord.ExecuteSQL("TRUNCATE TABLE authors CASCADE;")
	if err != nil {
		t.Fatalf("TRUNCATE TABLE authors CASCADE failed: %v", err)
	}
	if !resCascade.Success {
		t.Fatalf("expected success")
	}

	resA, _ = coord.ExecuteSQL("SELECT COUNT(*) AS c FROM authors;")
	if fmt.Sprintf("%v", resA.Rows[0]["c"]) != "0" {
		t.Fatalf("expected 0 authors after CASCADE, got %v", resA.Rows[0]["c"])
	}
	resB, _ = coord.ExecuteSQL("SELECT COUNT(*) AS c FROM books;")
	if fmt.Sprintf("%v", resB.Rows[0]["c"]) != "0" {
		t.Fatalf("expected 0 books after CASCADE, got %v", resB.Rows[0]["c"])
	}
}

func TestTruncateInStoredProcedure(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE sp_demo (id INT PRIMARY KEY, val INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	coord.ExecuteSQL("INSERT INTO sp_demo VALUES (1, 100), (2, 200);")

	spSQL := `
CREATE PROCEDURE sp_ResetDemo
AS
BEGIN
    TRUNCATE TABLE sp_demo;
END;
`
	if _, err := coord.ExecuteSQL(spSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE with TRUNCATE TABLE failed: %v", err)
	}

	res, err := coord.ExecuteSQL("EXEC sp_ResetDemo;")
	if err != nil {
		t.Fatalf("EXEC sp_ResetDemo failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("EXEC failed: %v", res)
	}

	resCount, err := coord.ExecuteSQL("SELECT COUNT(*) AS c FROM sp_demo;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if fmt.Sprintf("%v", resCount.Rows[0]["c"]) != "0" {
		t.Fatalf("expected 0 rows after SP truncate, got %v", resCount.Rows[0]["c"])
	}
}

func TestTransactionalTruncateWithRollback(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	coord.ExecuteSQL("CREATE TABLE tx_trunc (id INT PRIMARY KEY, name VARCHAR(50));")
	coord.ExecuteSQL("INSERT INTO tx_trunc VALUES (1, 'One'), (2, 'Two');")

	sessID := "tx-trunc-session"
	coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;")
	coord.ExecuteSessionSQL(sessID, "TRUNCATE TABLE tx_trunc;")

	// Inside tx, it should see 0 rows
	resInside, _ := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM tx_trunc;")
	if fmt.Sprintf("%v", resInside.Rows[0]["c"]) != "0" {
		t.Fatalf("expected 0 inside tx, got %v", resInside.Rows[0]["c"])
	}

	// Rollback
	coord.ExecuteSessionSQL(sessID, "ROLLBACK;")

	// Outside tx, the rows should be restored
	resAfter, _ := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM tx_trunc;")
	if fmt.Sprintf("%v", resAfter.Rows[0]["c"]) != "2" {
		t.Fatalf("expected 2 restored rows after ROLLBACK, got %v", resAfter.Rows[0]["c"])
	}
}

func TestTransactionalTruncate_NoPK_Rollback(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE tx_trunc_nopk (val INT, name VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO tx_trunc_nopk VALUES (10, 'One'), (20, 'Two');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	sessID := "tx-trunc-nopk-sess"
	coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;")
	coord.ExecuteSessionSQL(sessID, "TRUNCATE TABLE tx_trunc_nopk;")

	resInside, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM tx_trunc_nopk;")
	if err != nil {
		t.Fatalf("SELECT inside tx failed: %v", err)
	}
	if fmt.Sprintf("%v", resInside.Rows[0]["c"]) != "0" {
		t.Fatalf("expected 0 inside tx, got %v", resInside.Rows[0]["c"])
	}

	coord.ExecuteSessionSQL(sessID, "ROLLBACK;")

	resAfter, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM tx_trunc_nopk;")
	if err != nil {
		t.Fatalf("SELECT after rollback failed: %v", err)
	}
	if fmt.Sprintf("%v", resAfter.Rows[0]["c"]) != "2" {
		t.Fatalf("expected 2 restored rows after ROLLBACK, got %v", resAfter.Rows[0]["c"])
	}
}

func TestTransactionalTruncate_CompositePK_Rollback(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE tx_trunc_comp (id1 INT, id2 INT, name VARCHAR(50), PRIMARY KEY (id1, id2));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO tx_trunc_comp VALUES (1, 10, 'One'), (2, 20, 'Two');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	sessID := "tx-trunc-comp-sess"
	coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;")
	coord.ExecuteSessionSQL(sessID, "TRUNCATE TABLE tx_trunc_comp;")

	resInside, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM tx_trunc_comp;")
	if err != nil {
		t.Fatalf("SELECT inside tx failed: %v", err)
	}
	if fmt.Sprintf("%v", resInside.Rows[0]["c"]) != "0" {
		t.Fatalf("expected 0 inside tx, got %v", resInside.Rows[0]["c"])
	}

	coord.ExecuteSessionSQL(sessID, "ROLLBACK;")

	resAfter, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM tx_trunc_comp;")
	if err != nil {
		t.Fatalf("SELECT after rollback failed: %v", err)
	}
	if fmt.Sprintf("%v", resAfter.Rows[0]["c"]) != "2" {
		t.Fatalf("expected 2 restored rows after ROLLBACK, got %v", resAfter.Rows[0]["c"])
	}
}

func TestTransactionalTruncate_InSP_Rollback(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	coord.ExecuteSQL("CREATE TABLE sp_tx_tbl (id INT PRIMARY KEY, val INT);")
	coord.ExecuteSQL("INSERT INTO sp_tx_tbl VALUES (1, 100), (2, 200);")

	spSQL := `
CREATE PROCEDURE sp_TxTruncate
AS
BEGIN
    TRUNCATE TABLE sp_tx_tbl;
END;
`
	if _, err := coord.ExecuteSQL(spSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	sessID := "sp-tx-trunc-sess"
	coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;")
	resExec, err := coord.ExecuteSessionSQL(sessID, "EXEC sp_TxTruncate;")
	if err != nil || !resExec.Success {
		t.Fatalf("EXEC sp_TxTruncate failed: %v", err)
	}

	resInside, _ := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM sp_tx_tbl;")
	if fmt.Sprintf("%v", resInside.Rows[0]["c"]) != "0" {
		t.Fatalf("expected 0 rows inside tx, got %v", resInside.Rows[0]["c"])
	}

	coord.ExecuteSessionSQL(sessID, "ROLLBACK;")

	resAfter, _ := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM sp_tx_tbl;")
	if fmt.Sprintf("%v", resAfter.Rows[0]["c"]) != "2" {
		t.Fatalf("expected 2 rows restored after rollback, got %v", resAfter.Rows[0]["c"])
	}
}

func TestTransactionalTruncate_ReinsertAndUncommitted_Rollback(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	coord.ExecuteSQL("CREATE TABLE tx_trunc_re (id INT PRIMARY KEY, name VARCHAR(50));")
	coord.ExecuteSQL("INSERT INTO tx_trunc_re VALUES (1, 'One'), (2, 'Two');")

	sessID := "tx-re-sess"
	coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;")
	// Uncommitted insert before truncate
	coord.ExecuteSessionSQL(sessID, "INSERT INTO tx_trunc_re VALUES (3, 'Three');")
	coord.ExecuteSessionSQL(sessID, "TRUNCATE TABLE tx_trunc_re;")

	resInside1, _ := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM tx_trunc_re;")
	if fmt.Sprintf("%v", resInside1.Rows[0]["c"]) != "0" {
		t.Fatalf("expected 0 rows inside tx after truncate, got %v", resInside1.Rows[0]["c"])
	}

	// Re-insert same PK 1 inside tx
	resIns, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO tx_trunc_re VALUES (1, 'New One');")
	if err != nil || !resIns.Success {
		t.Fatalf("re-insert after truncate inside tx failed: %v", err)
	}

	resInside2, _ := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM tx_trunc_re;")
	if fmt.Sprintf("%v", resInside2.Rows[0]["c"]) != "1" {
		t.Fatalf("expected 1 row inside tx after reinsert, got %v", resInside2.Rows[0]["c"])
	}

	coord.ExecuteSessionSQL(sessID, "ROLLBACK;")

	resAfter, _ := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM tx_trunc_re;")
	if fmt.Sprintf("%v", resAfter.Rows[0]["c"]) != "2" {
		t.Fatalf("expected 2 rows restored after rollback, got %v", resAfter.Rows[0]["c"])
	}
}

func TestTransactionalTruncate_SingleBatch(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	coord.ExecuteSQL("CREATE TABLE batch_trunc (id INT PRIMARY KEY, name VARCHAR(50));")
	coord.ExecuteSQL("INSERT INTO batch_trunc VALUES (1, 'One'), (2, 'Two');")

	// Execute as single batch
	_, err := coord.ExecuteSQL("BEGIN TRANSACTION; TRUNCATE TABLE batch_trunc; ROLLBACK;")
	if err != nil {
		t.Fatalf("batch failed: %v", err)
	}

	res, err := coord.ExecuteSQL("SELECT COUNT(*) AS c FROM batch_trunc;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["c"]) != "2" {
		t.Fatalf("expected 2 restored rows, got %v", res.Rows[0]["c"])
	}
}

func TestTransactionalTruncate_RestartIdentity_Rollback(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE tx_ident (id INT IDENTITY(1,1) PRIMARY KEY, name VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	coord.ExecuteSQL("INSERT INTO tx_ident (name) VALUES ('One');")

	sessID := "tx-ident-sess"
	coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;")
	coord.ExecuteSessionSQL(sessID, "TRUNCATE TABLE tx_ident RESTART IDENTITY;")
	coord.ExecuteSessionSQL(sessID, "ROLLBACK;")

	// Insert new row after rollback. It must get id = 2, NOT id = 1 (which would be a duplicate key error or identity reset bug)!
	resIns, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO tx_ident (name) VALUES ('Two');")
	if err != nil || !resIns.Success {
		t.Fatalf("INSERT after rollback failed: %v", err)
	}

	resSelect, err := coord.ExecuteSessionSQL(sessID, "SELECT id, name FROM tx_ident WHERE name = 'Two';")
	if err != nil || len(resSelect.Rows) == 0 {
		t.Fatalf("SELECT failed: %v", err)
	}
	if fmt.Sprintf("%v", resSelect.Rows[0]["id"]) != "2" {
		t.Fatalf("expected id = 2 after rollback, got %v", resSelect.Rows[0]["id"])
	}
}

func TestTransactionalTruncate_PKMismatch_SyntheticRowID(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE tx_mismatch (tag VARCHAR(50), val INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	coord.ExecuteSQL("INSERT INTO tx_mismatch VALUES ('hello', 10), ('world', 20);")

	sessID := "tx-mismatch-sess"
	coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;")
	resTrunc, err := coord.ExecuteSessionSQL(sessID, "TRUNCATE TABLE tx_mismatch;")
	if err != nil || !resTrunc.Success {
		t.Fatalf("TRUNCATE TABLE failed: %v", err)
	}

	// Inside tx, it MUST be 0!
	resInside, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM tx_mismatch;")
	if err != nil {
		t.Fatalf("SELECT inside tx failed: %v", err)
	}
	if fmt.Sprintf("%v", resInside.Rows[0]["c"]) != "0" {
		t.Fatalf("FAIL: expected 0 inside tx after truncate, got %v", resInside.Rows[0]["c"])
	}

	coord.ExecuteSessionSQL(sessID, "ROLLBACK;")

	// After rollback, both rows must be preserved!
	resAfter, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM tx_mismatch;")
	if err != nil {
		t.Fatalf("SELECT after rollback failed: %v", err)
	}
	if fmt.Sprintf("%v", resAfter.Rows[0]["c"]) != "2" {
		t.Fatalf("FAIL: expected 2 after rollback, got %v", resAfter.Rows[0]["c"])
	}
}

func TestTransactionalTruncate_SP_CustomPKName_Uppercase(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE sp_emp_trunc (EMP_ID INT PRIMARY KEY, name VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	coord.ExecuteSQL("INSERT INTO sp_emp_trunc VALUES (1, 'Alice'), (2, 'Bob');")

	spSQL := `
CREATE PROCEDURE sp_TruncEmp
AS
BEGIN
    TRUNCATE TABLE sp_emp_trunc;
END;
`
	if _, err := coord.ExecuteSQL(spSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	sessID := "sp-emp-sess"
	coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;")
	resExec, err := coord.ExecuteSessionSQL(sessID, "EXEC sp_TruncEmp;")
	if err != nil || !resExec.Success {
		t.Fatalf("EXEC sp_TruncEmp failed: %v", err)
	}

	// Inside tx, it MUST be 0!
	resInside, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM sp_emp_trunc;")
	if err != nil {
		t.Fatalf("SELECT inside tx failed: %v", err)
	}
	if fmt.Sprintf("%v", resInside.Rows[0]["c"]) != "0" {
		t.Fatalf("FAIL: expected 0 inside tx after SP truncate, got %v", resInside.Rows[0]["c"])
	}

	coord.ExecuteSessionSQL(sessID, "ROLLBACK;")

	// After rollback, both rows must be preserved!
	resAfter, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM sp_emp_trunc;")
	if err != nil {
		t.Fatalf("SELECT after rollback failed: %v", err)
	}
	if fmt.Sprintf("%v", resAfter.Rows[0]["c"]) != "2" {
		t.Fatalf("FAIL: expected 2 after rollback, got %v", resAfter.Rows[0]["c"])
	}
}

func TestTransactionalTruncate_SP_NoPK_Rollback(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE sp_nopk_trunc (val INT, name VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	coord.ExecuteSQL("INSERT INTO sp_nopk_trunc VALUES (100, 'Alice'), (200, 'Bob');")

	spSQL := `
CREATE PROCEDURE sp_TruncNoPK
AS
BEGIN
    TRUNCATE TABLE sp_nopk_trunc;
END;
`
	if _, err := coord.ExecuteSQL(spSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	sessID := "sp-nopk-sess"
	coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;")
	resExec, err := coord.ExecuteSessionSQL(sessID, "EXEC sp_TruncNoPK;")
	if err != nil || !resExec.Success {
		t.Fatalf("EXEC sp_TruncNoPK failed: %v", err)
	}

	// Inside tx, it MUST be 0!
	resInside, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM sp_nopk_trunc;")
	if err != nil {
		t.Fatalf("SELECT inside tx failed: %v", err)
	}
	if fmt.Sprintf("%v", resInside.Rows[0]["c"]) != "0" {
		t.Fatalf("FAIL: expected 0 inside tx after SP truncate, got %v", resInside.Rows[0]["c"])
	}

	coord.ExecuteSessionSQL(sessID, "ROLLBACK;")

	// After rollback, both rows must be preserved!
	resAfter, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM sp_nopk_trunc;")
	if err != nil {
		t.Fatalf("SELECT after rollback failed: %v", err)
	}
	if fmt.Sprintf("%v", resAfter.Rows[0]["c"]) != "2" {
		t.Fatalf("FAIL: expected 2 after rollback, got %v", resAfter.Rows[0]["c"])
	}
}
