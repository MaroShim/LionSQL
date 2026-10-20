package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestRegression_BUG_PK_1 verifies that UPDATE, DELETE, and TRUNCATE on tables with
// non-"id" primary keys (e.g., account_no, code) reliably resolve the true PK column
// and do not randomly select an arbitrary map entry.
func TestRegression_BUG_PK_1(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Create table with many columns before the PK to guarantee map randomization failures if unhandled
	createSQL := `CREATE TABLE bank_accounts (
		c1 VARCHAR(20),
		c2 VARCHAR(20),
		c3 VARCHAR(20),
		c4 VARCHAR(20),
		c5 VARCHAR(20),
		c6 VARCHAR(20),
		c7 VARCHAR(20),
		c8 VARCHAR(20),
		c9 VARCHAR(20),
		balance FLOAT,
		owner_name VARCHAR(50),
		account_no INT PRIMARY KEY,
		status VARCHAR(20)
	);`
	if _, err := coord.ExecuteSQL(createSQL); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	insertSQL := `INSERT INTO bank_accounts (account_no, owner_name, balance, status, c1, c2, c3, c4, c5, c6, c7, c8, c9) VALUES
		(101, 'Alice', 1000.0, 'ACTIVE', 'v1', 'v2', 'v3', 'v4', 'v5', 'v6', 'v7', 'v8', 'v9'),
		(102, 'Bob', 2000.0, 'PENDING', 'v1', 'v2', 'v3', 'v4', 'v5', 'v6', 'v7', 'v8', 'v9'),
		(103, 'Charlie', 3000.0, 'ACTIVE', 'v1', 'v2', 'v3', 'v4', 'v5', 'v6', 'v7', 'v8', 'v9');`
	if _, err := coord.ExecuteSQL(insertSQL); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. Perform UPDATE with OUTPUT clause across multiple iterations
	for i := 0; i < 20; i++ {
		updateSQL := fmt.Sprintf("UPDATE bank_accounts SET balance = %f OUTPUT INSERTED.balance WHERE account_no = 101;", 1500.0+float64(i))
		res, err := coord.ExecuteSQL(updateSQL)
		if err != nil {
			t.Fatalf("iteration %d: UPDATE with OUTPUT on non-id PK failed: %v", i, err)
		}
		if res.RowsAffected != 1 {
			t.Fatalf("iteration %d: expected 1 row affected, got %d", i, res.RowsAffected)
		}
	}

	// Verify row 101 was updated and other rows unaffected
	selRes, err := coord.ExecuteSQL("SELECT account_no, balance FROM bank_accounts WHERE account_no = 101;")
	if err != nil || len(selRes.Rows) != 1 {
		t.Fatalf("SELECT row 101 failed: %v (rows: %d)", err, len(selRes.Rows))
	}
	if fmt.Sprintf("%v", selRes.Rows[0]["balance"]) != "1519" {
		t.Fatalf("expected balance 1519, got %v", selRes.Rows[0]["balance"])
	}

	// 3. Perform DELETE with OUTPUT clause on account_no = 102
	delRes, err := coord.ExecuteSQL("DELETE FROM bank_accounts OUTPUT DELETED.account_no WHERE account_no = 102;")
	if err != nil {
		t.Fatalf("DELETE with OUTPUT on non-id PK failed: %v", err)
	}
	if delRes.RowsAffected != 1 {
		t.Fatalf("expected 1 row deleted, got %d", delRes.RowsAffected)
	}

	// Verify only row 102 was deleted
	selAll, err := coord.ExecuteSQL("SELECT account_no FROM bank_accounts ORDER BY account_no ASC;")
	if err != nil || len(selAll.Rows) != 2 {
		t.Fatalf("expected 2 remaining rows, got %d (err: %v)", len(selAll.Rows), err)
	}

	// 4. Test VDBE Stored Procedure with non-id PK UPDATE & DELETE
	procSQL := `CREATE PROCEDURE ModifyAccount @acc INT, @new_bal FLOAT AS
BEGIN
	UPDATE bank_accounts SET balance = @new_bal WHERE account_no = @acc;
	DELETE FROM bank_accounts WHERE account_no = 103;
END;`
	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	execSQL := "EXEC ModifyAccount @acc = 101, @new_bal = 9999.0;"
	if _, err := coord.ExecuteSQL(execSQL); err != nil {
		t.Fatalf("EXEC ModifyAccount failed: %v", err)
	}

	// Verify row 103 deleted and row 101 updated to 9999
	finalRes, err := coord.ExecuteSQL("SELECT account_no, balance FROM bank_accounts WHERE account_no = 101;")
	if err != nil || len(finalRes.Rows) != 1 {
		t.Fatalf("final SELECT failed: %v (rows: %d)", err, len(finalRes.Rows))
	}
	if fmt.Sprintf("%v", finalRes.Rows[0]["balance"]) != "9999" {
		t.Fatalf("expected balance 9999, got %v", finalRes.Rows[0]["balance"])
	}

	// 5. Test TRUNCATE TABLE on non-id PK table
	truncRes, err := coord.ExecuteSQL("TRUNCATE TABLE bank_accounts;")
	if err != nil || !truncRes.Success {
		t.Fatalf("TRUNCATE TABLE failed: %v", err)
	}
	emptyRes, err := coord.ExecuteSQL("SELECT account_no FROM bank_accounts;")
	if err != nil || len(emptyRes.Rows) != 0 {
		t.Fatalf("expected 0 rows after TRUNCATE, got %d", len(emptyRes.Rows))
	}
}
