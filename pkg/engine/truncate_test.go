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
