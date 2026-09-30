package api_test

import (
	"fmt"
	"testing"
)

func TestSavepoint_Basic(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	sessID := "sess_sp_basic"

	_, err := server.ExecuteSessionSQL(sessID, `CREATE TABLE accounts (
		id INT PRIMARY KEY,
		balance INT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSessionSQL(sessID, `INSERT INTO accounts VALUES (1, 1000);`)
	if err != nil {
		t.Fatalf("INSERT initial row failed: %v", err)
	}

	// 1. Begin transaction
	res, err := server.ExecuteSessionSQL(sessID, `BEGIN TRANSACTION;`)
	if err != nil || !res.Success {
		t.Fatalf("BEGIN TRANSACTION failed: %v", err)
	}

	// 2. First update
	_, err = server.ExecuteSessionSQL(sessID, `UPDATE accounts SET balance = 1200 WHERE id = 1;`)
	if err != nil {
		t.Fatalf("First UPDATE failed: %v", err)
	}

	// 3. Create savepoint sp1
	res, err = server.ExecuteSessionSQL(sessID, `SAVE TRANSACTION sp1;`)
	if err != nil || !res.Success {
		t.Fatalf("SAVE TRANSACTION sp1 failed: %v", err)
	}

	// 4. Second update after savepoint
	_, err = server.ExecuteSessionSQL(sessID, `UPDATE accounts SET balance = 1500 WHERE id = 1;`)
	if err != nil {
		t.Fatalf("Second UPDATE failed: %v", err)
	}

	// Verify balance is 1500 before rollback to savepoint
	res, err = server.ExecuteSessionSQL(sessID, `SELECT balance FROM accounts WHERE id = 1;`)
	if err != nil || len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["balance"]) != "1500" {
		t.Fatalf("expected balance 1500, got: %+v", res.Rows)
	}

	// 5. Rollback to savepoint sp1
	res, err = server.ExecuteSessionSQL(sessID, `ROLLBACK TRANSACTION sp1;`)
	if err != nil || !res.Success {
		t.Fatalf("ROLLBACK TRANSACTION sp1 failed: %v", err)
	}

	// Verify balance reverted to 1200
	res, err = server.ExecuteSessionSQL(sessID, `SELECT balance FROM accounts WHERE id = 1;`)
	if err != nil || len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["balance"]) != "1200" {
		t.Fatalf("expected balance reverted to 1200, got: %+v", res.Rows)
	}

	// 6. Commit transaction
	res, err = server.ExecuteSessionSQL(sessID, `COMMIT TRANSACTION;`)
	if err != nil || !res.Success {
		t.Fatalf("COMMIT TRANSACTION failed: %v", err)
	}

	// Verify final committed state is 1200
	res, err = server.ExecuteSessionSQL(sessID, `SELECT balance FROM accounts WHERE id = 1;`)
	if err != nil || len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["balance"]) != "1200" {
		t.Fatalf("expected committed balance 1200, got: %+v", res.Rows)
	}
}

func TestSavepoint_MultipleNested(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	sessID := "sess_sp_nested"

	_, err := server.ExecuteSessionSQL(sessID, `CREATE TABLE accounts (
		id INT PRIMARY KEY,
		balance INT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSessionSQL(sessID, `INSERT INTO accounts VALUES (1, 1000);`)
	if err != nil {
		t.Fatalf("INSERT initial row failed: %v", err)
	}

	// Begin tran
	_, err = server.ExecuteSessionSQL(sessID, `BEGIN TRAN;`)
	if err != nil {
		t.Fatalf("BEGIN TRAN failed: %v", err)
	}

	// Step 1: Insert row 2 and savepoint sp_a
	_, err = server.ExecuteSessionSQL(sessID, `INSERT INTO accounts VALUES (2, 2000);`)
	if err != nil {
		t.Fatalf("INSERT row 2 failed: %v", err)
	}

	res, err := server.ExecuteSessionSQL(sessID, `SAVE TRAN sp_a;`)
	if err != nil || !res.Success {
		t.Fatalf("SAVE TRAN sp_a failed: %v", err)
	}

	// Step 2: Insert row 3 and savepoint sp_b
	_, err = server.ExecuteSessionSQL(sessID, `INSERT INTO accounts VALUES (3, 3000);`)
	if err != nil {
		t.Fatalf("INSERT row 3 failed: %v", err)
	}

	res, err = server.ExecuteSessionSQL(sessID, `SAVEPOINT sp_b;`)
	if err != nil || !res.Success {
		t.Fatalf("SAVEPOINT sp_b failed: %v", err)
	}

	// Step 3: Insert row 4
	_, err = server.ExecuteSessionSQL(sessID, `INSERT INTO accounts VALUES (4, 4000);`)
	if err != nil {
		t.Fatalf("INSERT row 4 failed: %v", err)
	}

	res, err = server.ExecuteSessionSQL(sessID, `SELECT COUNT(*) AS total FROM accounts;`)
	if err != nil || fmt.Sprintf("%v", res.Rows[0]["total"]) != "4" {
		t.Fatalf("expected 4 rows before rollback, got: %+v", res.Rows)
	}

	// Rollback to sp_b (row 4 discarded, row 3 kept)
	res, err = server.ExecuteSessionSQL(sessID, `ROLLBACK TO SAVEPOINT sp_b;`)
	if err != nil || !res.Success {
		t.Fatalf("ROLLBACK TO SAVEPOINT sp_b failed: %v", err)
	}

	res, err = server.ExecuteSessionSQL(sessID, `SELECT COUNT(*) AS total FROM accounts;`)
	if err != nil || fmt.Sprintf("%v", res.Rows[0]["total"]) != "3" {
		t.Fatalf("expected 3 rows after rollback to sp_b, got: %+v", res.Rows)
	}

	// Rollback to sp_a (row 3 discarded, row 2 kept)
	res, err = server.ExecuteSessionSQL(sessID, `ROLLBACK TRAN sp_a;`)
	if err != nil || !res.Success {
		t.Fatalf("ROLLBACK TRAN sp_a failed: %v", err)
	}

	res, err = server.ExecuteSessionSQL(sessID, `SELECT COUNT(*) AS total FROM accounts;`)
	if err != nil || fmt.Sprintf("%v", res.Rows[0]["total"]) != "2" {
		t.Fatalf("expected 2 rows after rollback to sp_a, got: %+v", res.Rows)
	}

	// Commit
	_, err = server.ExecuteSessionSQL(sessID, `COMMIT TRAN;`)
	if err != nil {
		t.Fatalf("COMMIT TRAN failed: %v", err)
	}

	res, err = server.ExecuteSessionSQL(sessID, `SELECT id FROM accounts ORDER BY id;`)
	if err != nil || len(res.Rows) != 2 {
		t.Fatalf("expected 2 accounts committed, got: %+v", res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["id"]) != "1" || fmt.Sprintf("%v", res.Rows[1]["id"]) != "2" {
		t.Fatalf("expected rows [1, 2], got: %+v", res.Rows)
	}
}

func TestSavepoint_FullRollbackAfterSavepoint(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	sessID := "sess_sp_full_rb"

	_, err := server.ExecuteSessionSQL(sessID, `CREATE TABLE accounts (
		id INT PRIMARY KEY,
		balance INT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSessionSQL(sessID, `BEGIN TRANSACTION;`)
	if err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}

	_, err = server.ExecuteSessionSQL(sessID, `INSERT INTO accounts VALUES (5, 5000);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	_, err = server.ExecuteSessionSQL(sessID, `SAVE TRANSACTION sp_x;`)
	if err != nil {
		t.Fatalf("SAVE failed: %v", err)
	}

	_, err = server.ExecuteSessionSQL(sessID, `INSERT INTO accounts VALUES (6, 6000);`)
	if err != nil {
		t.Fatalf("INSERT 6 failed: %v", err)
	}

	_, err = server.ExecuteSessionSQL(sessID, `ROLLBACK TRANSACTION sp_x;`)
	if err != nil {
		t.Fatalf("ROLLBACK to sp_x failed: %v", err)
	}

	// Full rollback should undo everything including row 5
	res, err := server.ExecuteSessionSQL(sessID, `ROLLBACK TRANSACTION;`)
	if err != nil || !res.Success {
		t.Fatalf("full ROLLBACK failed: %v", err)
	}

	res, err = server.ExecuteSessionSQL(sessID, `SELECT COUNT(*) AS total FROM accounts;`)
	if err != nil || fmt.Sprintf("%v", res.Rows[0]["total"]) != "0" {
		t.Fatalf("expected 0 rows after full rollback, got: %+v", res.Rows)
	}
}

func TestSavepoint_Errors(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	sessID := "sess_sp_err"

	// 1. Savepoint without active transaction
	res, err := server.ExecuteSessionSQL(sessID, `SAVE TRANSACTION sp1;`)
	if err == nil && (res != nil && res.Success) {
		t.Fatalf("expected error creating savepoint without transaction, got success")
	}

	// 2. Begin transaction and rollback to nonexistent savepoint
	_, err = server.ExecuteSessionSQL(sessID, `BEGIN TRANSACTION;`)
	if err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}

	res, err = server.ExecuteSessionSQL(sessID, `ROLLBACK TRANSACTION nonexistent_sp;`)
	if err == nil && (res != nil && res.Success) {
		t.Fatalf("expected error rolling back to nonexistent savepoint, got success")
	}

	// Cleanup
	_, _ = server.ExecuteSessionSQL(sessID, `ROLLBACK;`)
}

func TestSavepoint_InStoredProcedure(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	sessID := "sess_sp_proc"

	_, err := server.ExecuteSessionSQL(sessID, `CREATE TABLE accounts (
		id INT PRIMARY KEY,
		balance INT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSessionSQL(sessID, `
CREATE PROCEDURE TestProcWithSavepoint
AS
BEGIN
    BEGIN TRANSACTION;
    INSERT INTO accounts (id, balance) VALUES (10, 100);
    SAVE TRANSACTION sp_inner;
    INSERT INTO accounts (id, balance) VALUES (11, 200);
    ROLLBACK TRANSACTION sp_inner;
    COMMIT TRANSACTION;
END;`)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	res, err := server.ExecuteSessionSQL(sessID, `EXEC TestProcWithSavepoint;`)
	if err != nil || !res.Success {
		t.Fatalf("EXEC TestProcWithSavepoint failed: %v", err)
	}

	// Verify row 10 exists, row 11 does not
	res, err = server.ExecuteSessionSQL(sessID, `SELECT id, balance FROM accounts ORDER BY id;`)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("expected exactly 1 account row, got: %+v", res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["id"]) != "10" || fmt.Sprintf("%v", res.Rows[0]["balance"]) != "100" {
		t.Fatalf("expected row (10, 100), got: %+v", res.Rows)
	}
}

func TestSavepoint_Release(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	sessID := "sess_sp_release"

	_, err := server.ExecuteSessionSQL(sessID, `CREATE TABLE release_test (id INT PRIMARY KEY, val VARCHAR(50));`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, _ = server.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;")
	_, _ = server.ExecuteSessionSQL(sessID, "INSERT INTO release_test VALUES (1, 'Initial');")
	_, _ = server.ExecuteSessionSQL(sessID, "SAVE TRANSACTION sp_rel;")
	_, _ = server.ExecuteSessionSQL(sessID, "INSERT INTO release_test VALUES (2, 'Second');")

	// Release savepoint
	relRes, err := server.ExecuteSessionSQL(sessID, "RELEASE SAVEPOINT sp_rel;")
	if err != nil || !relRes.Success {
		t.Fatalf("RELEASE SAVEPOINT failed: %v", err)
	}

	// Attempting to rollback to released savepoint should now error
	_, err = server.ExecuteSessionSQL(sessID, "ROLLBACK TRANSACTION sp_rel;")
	if err == nil {
		t.Fatalf("expected rollback to released savepoint to fail")
	}

	// Commit should preserve both rows
	_, err = server.ExecuteSessionSQL(sessID, "COMMIT TRANSACTION;")
	if err != nil {
		t.Fatalf("COMMIT failed: %v", err)
	}

	selRes, err := server.ExecuteSessionSQL(sessID, "SELECT * FROM release_test ORDER BY id;")
	if err != nil || len(selRes.Rows) != 2 {
		t.Fatalf("expected 2 committed rows, got %d", len(selRes.Rows))
	}
}
