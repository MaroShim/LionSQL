package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestTableVariables_BasicBatch(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessionID := "sess-tv-1"

	// 1. Declare table variable
	res, err := coord.ExecuteSessionSQL(sessionID, "DECLARE @items TABLE (id INT PRIMARY KEY, name VARCHAR(50), price INT);")
	if err != nil || !res.Success {
		t.Fatalf("DECLARE @items TABLE failed: %v", err)
	}

	// 2. Insert into table variable
	res, err = coord.ExecuteSessionSQL(sessionID, "INSERT INTO @items VALUES (1, 'Book', 100), (2, 'Pen', 20);")
	if err != nil || !res.Success {
		t.Fatalf("INSERT INTO @items failed: %v", err)
	}

	// 3. Select from table variable
	res, err = coord.ExecuteSessionSQL(sessionID, "SELECT id, name, price FROM @items ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("SELECT FROM @items failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows from @items, got: %d (%v)", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["name"]) != "Book" || fmt.Sprintf("%v", res.Rows[1]["name"]) != "Pen" {
		t.Fatalf("unexpected rows: %v", res.Rows)
	}

	// 4. Update table variable
	res, err = coord.ExecuteSessionSQL(sessionID, "UPDATE @items SET price = 150 WHERE id = 1;")
	if err != nil || !res.Success {
		t.Fatalf("UPDATE @items failed: %v", err)
	}

	res, err = coord.ExecuteSessionSQL(sessionID, "SELECT price FROM @items WHERE id = 1;")
	if err != nil || !res.Success || len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["price"]) != "150" {
		t.Fatalf("expected updated price 150, got: %v", res.Rows)
	}

	// 5. Delete from table variable
	res, err = coord.ExecuteSessionSQL(sessionID, "DELETE FROM @items WHERE id = 2;")
	if err != nil || !res.Success {
		t.Fatalf("DELETE FROM @items failed: %v", err)
	}

	res, err = coord.ExecuteSessionSQL(sessionID, "SELECT COUNT(*) AS cnt FROM @items;")
	if err != nil || !res.Success || len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["cnt"]) != "1" {
		t.Fatalf("expected count 1, got: %v", res.Rows)
	}
}

func TestTableVariables_MultiStatementBatch(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessionID := "sess-tv-2"
	batch := `
DECLARE @cart TABLE (item_id INT PRIMARY KEY, qty INT);
INSERT INTO @cart VALUES (101, 2), (102, 5);
SELECT item_id, qty FROM @cart ORDER BY item_id ASC;
`
	res, err := coord.ExecuteSessionSQL(sessionID, batch)
	if err != nil || !res.Success {
		t.Fatalf("multi-statement batch failed: %v", err)
	}
	if len(res.Rows) != 2 || fmt.Sprintf("%v", res.Rows[0]["item_id"]) != "101" {
		t.Fatalf("expected 2 rows, got: %v", res.Rows)
	}
}

func TestTableVariables_JoinAndInsertSelect(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessionID := "sess-tv-3"
	coord.ExecuteSessionSQL(sessionID, "CREATE TABLE products (pid INT PRIMARY KEY, title VARCHAR(50), price INT);")
	coord.ExecuteSessionSQL(sessionID, "INSERT INTO products VALUES (1, 'Keyboard', 50), (2, 'Mouse', 30), (3, 'Monitor', 300);")

	batch := `
DECLARE @cheap TABLE (id INT PRIMARY KEY, name VARCHAR(50));
INSERT INTO @cheap SELECT pid, title FROM products WHERE price <= 50;
SELECT p.price, c.name FROM products p JOIN @cheap c ON p.pid = c.id ORDER BY p.price ASC;
`
	res, err := coord.ExecuteSessionSQL(sessionID, batch)
	if err != nil || !res.Success {
		t.Fatalf("Join and InsertSelect failed: %v", err)
	}
	if len(res.Rows) != 2 || fmt.Sprintf("%v", res.Rows[0]["name"]) != "Mouse" {
		t.Fatalf("expected 2 joined rows, got: %v", res.Rows)
	}
}

func TestTableVariables_StoredProcedure(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessionID := "sess-tv-4"
	spSQL := `
CREATE PROCEDURE GetBudgetSummary
AS
BEGIN
    DECLARE @summary TABLE (dept VARCHAR(50), total INT);
    INSERT INTO @summary VALUES ('Engineering', 5000), ('Sales', 3000);
    SELECT dept, total FROM @summary ORDER BY total DESC;
END;
`
	_, err := coord.ExecuteSessionSQL(sessionID, spSQL)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE with table variable failed: %v", err)
	}

	res, err := coord.ExecuteSessionSQL(sessionID, "EXEC GetBudgetSummary;")
	if err != nil || !res.Success {
		t.Fatalf("EXEC GetBudgetSummary failed: %v", err)
	}
	if len(res.Rows) != 2 || fmt.Sprintf("%v", res.Rows[0]["dept"]) != "Engineering" {
		t.Fatalf("expected 2 summary rows from procedure, got: %v", res.Rows)
	}

	res, err = coord.ExecuteSessionSQL(sessionID, "EXEC GetBudgetSummary;")
	if err != nil || !res.Success {
		t.Fatalf("EXEC GetBudgetSummary second execution failed: %v", err)
	}
	if len(res.Rows) != 2 || fmt.Sprintf("%v", res.Rows[0]["dept"]) != "Engineering" {
		t.Fatalf("expected 2 summary rows from procedure on 2nd run, got: %v", res.Rows)
	}
}

func TestTableVariables_StoredProcedureDMLAndMultipleExecs(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessionID := "sess-tv-5"
	spSQL := `
CREATE PROCEDURE ProcessEmployeeBonus
AS
BEGIN
    DECLARE @emp TABLE (id INT PRIMARY KEY, name VARCHAR(50), bonus INT);
    INSERT INTO @emp VALUES (1, 'Alice', 100), (2, 'Bob', 200), (3, 'Charlie', 300);

    UPDATE @emp SET bonus = bonus + 50 WHERE id = 2;
    DELETE FROM @emp WHERE id = 1;

    SELECT id, name, bonus FROM @emp ORDER BY id ASC;
END;
`
	if _, err := coord.ExecuteSessionSQL(sessionID, spSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	for i := 0; i < 3; i++ {
		res, err := coord.ExecuteSessionSQL(sessionID, "EXEC ProcessEmployeeBonus;")
		if err != nil || !res.Success {
			t.Fatalf("run %d failed: %v", i, err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("run %d: expected 2 rows, got %d", i, len(res.Rows))
		}
		// Row 0 should be Bob with bonus 250
		if fmt.Sprintf("%v", res.Rows[0]["name"]) != "Bob" || fmt.Sprintf("%v", res.Rows[0]["bonus"]) != "250" {
			t.Fatalf("run %d: unexpected row 0: %v", i, res.Rows[0])
		}
		// Row 1 should be Charlie with bonus 300
		if fmt.Sprintf("%v", res.Rows[1]["name"]) != "Charlie" || fmt.Sprintf("%v", res.Rows[1]["bonus"]) != "300" {
			t.Fatalf("run %d: unexpected row 1: %v", i, res.Rows[1])
		}
	}
}

func TestTableVariables_RollbackExemptionInTryCatch(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessionID := "sess-tv-rollback"

	// Create physical table
	if _, err := coord.ExecuteSessionSQL(sessionID, "CREATE TABLE bank_accounts (id INT PRIMARY KEY, balance INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	batch := `
DECLARE @audit TABLE (step VARCHAR(50), status VARCHAR(20));
BEGIN TRY
    BEGIN TRANSACTION;
    INSERT INTO bank_accounts VALUES (1, 1000);
    INSERT INTO @audit VALUES ('step1_insert', 'STARTED');

    -- Cause error: duplicate PK insert
    INSERT INTO bank_accounts VALUES (1, 2000);
    COMMIT TRANSACTION;
END TRY
BEGIN CATCH
    ROLLBACK TRANSACTION;
    INSERT INTO @audit VALUES ('step2_error_logged', 'ROLLED_BACK');
END CATCH;

SELECT step, status FROM @audit ORDER BY step ASC;
`
	res, err := coord.ExecuteSessionSQL(sessionID, batch)
	if err != nil {
		t.Fatalf("ExecuteSessionSQL batch failed: %v", err)
	}

	// bank_accounts must be empty because the transaction was rolled back
	accRes, err := coord.ExecuteSessionSQL(sessionID, "SELECT * FROM bank_accounts;")
	if err != nil {
		t.Fatalf("SELECT bank_accounts failed: %v", err)
	}
	if len(accRes.Rows) != 0 {
		t.Fatalf("expected bank_accounts to have 0 rows after rollback, got %d", len(accRes.Rows))
	}

	// @audit table variable MUST contain BOTH records ('step1_insert' and 'step2_error_logged')
	if len(res.Rows) != 2 {
		t.Fatalf("expected @audit to retain 2 rows across rollback, got %d rows: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["step"]) != "step1_insert" || fmt.Sprintf("%v", res.Rows[1]["step"]) != "step2_error_logged" {
		t.Fatalf("unexpected audit rows: %+v", res.Rows)
	}
}

func TestTableVariables_OutputIntoClause(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessionID := "sess-tv-output-into"

	if _, err := coord.ExecuteSessionSQL(sessionID, "CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(50), price INT);"); err != nil {
		t.Fatalf("CREATE TABLE items failed: %v", err)
	}

	batch := `
DECLARE @inserted TABLE (out_id INT, out_name VARCHAR(50));
INSERT INTO items (id, name, price)
OUTPUT INSERTED.id, INSERTED.name INTO @inserted (out_id, out_name)
VALUES (10, 'Notebook', 500), (20, 'Tablet', 1200);

SELECT out_id, out_name FROM @inserted ORDER BY out_id ASC;
`
	res, err := coord.ExecuteSessionSQL(sessionID, batch)
	if err != nil {
		t.Fatalf("ExecuteSessionSQL batch failed: %v", err)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows in @inserted, got %d: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["out_name"]) != "Notebook" || fmt.Sprintf("%v", res.Rows[1]["out_name"]) != "Tablet" {
		t.Fatalf("unexpected @inserted rows: %+v", res.Rows)
	}

	// Verify items table has 2 rows
	itemRes, err := coord.ExecuteSessionSQL(sessionID, "SELECT COUNT(*) AS cnt FROM items;")
	if err != nil || len(itemRes.Rows) != 1 || fmt.Sprintf("%v", itemRes.Rows[0]["cnt"]) != "2" {
		t.Fatalf("expected items to have 2 rows: %+v", itemRes.Rows)
	}
}



