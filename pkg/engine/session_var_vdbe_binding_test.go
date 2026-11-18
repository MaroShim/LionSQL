package engine

import (
	"testing"
)

// TestSessionVarVDBE_NoTextualCorruption verifies that pre-existing session variables
// do not corrupt assignments, return codes, filters, or projections via textual pre-substitution.
func TestSessionVarVDBE_NoTextualCorruption(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sessID := "test_sess_vdbe_var"

	// 1. Setup table
	_, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE accounts (id INT PRIMARY KEY, name VARCHAR(50), balance INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sessID, "INSERT INTO accounts (id, name, balance) VALUES (1, 'Alice', 1000), (2, 'Bob', 2000);")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. Initial state: @id is 1
	_, err = coord.ExecuteSessionSQL(sessID, "SET @id = 1;")
	if err != nil {
		t.Fatalf("SET @id = 1 failed: %v", err)
	}

	// 3. Volcano filter with @var: SELECT name, balance FROM accounts WHERE id = @id
	res, err := coord.ExecuteSessionSQL(sessID, "SELECT name, balance FROM accounts WHERE id = @id;")
	if err != nil {
		t.Fatalf("SELECT WHERE id = @id failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Alice" {
		t.Fatalf("expected Alice, got: %+v", res.Rows)
	}

	// 4. Update variable via relational SELECT: SELECT @id = id FROM accounts WHERE balance = 2000
	// Under legacy textual substitution, this became: SELECT 1 = id FROM accounts WHERE balance = 2000 (corrupted to comparison!)
	_, err = coord.ExecuteSessionSQL(sessID, "SELECT @id = id FROM accounts WHERE balance = 2000;")
	if err != nil {
		t.Fatalf("SELECT @id = id assignment failed: %v", err)
	}

	res, err = coord.ExecuteSessionSQL(sessID, "SELECT @id AS current_id;")
	if err != nil {
		t.Fatalf("SELECT @id failed: %v", err)
	}
	if v, ok := res.Rows[0]["current_id"].(int64); !ok || v != 2 {
		t.Fatalf("expected @id = 2, got: %v (%T)", res.Rows[0]["current_id"], res.Rows[0]["current_id"])
	}

	// 5. Arithmetic update without textual expansion corruption: SET @id = @id + 10
	_, err = coord.ExecuteSessionSQL(sessID, "SET @id = @id + 10;")
	if err != nil {
		t.Fatalf("SET @id = @id + 10 failed: %v", err)
	}
	res, err = coord.ExecuteSessionSQL(sessID, "SELECT @id AS current_id;")
	if err != nil {
		t.Fatalf("SELECT @id failed: %v", err)
	}
	if v, ok := res.Rows[0]["current_id"].(int64); !ok || v != 12 {
		t.Fatalf("expected @id = 12, got: %v (%T)", res.Rows[0]["current_id"], res.Rows[0]["current_id"])
	}

	// 6. Volcano projection with @var: SELECT @id AS current_id, name FROM accounts WHERE id = 1
	res, err = coord.ExecuteSessionSQL(sessID, "SELECT @id AS current_id, name FROM accounts WHERE id = 1;")
	if err != nil {
		t.Fatalf("SELECT @id, name failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if v, ok := res.Rows[0]["current_id"].(int64); !ok || v != 12 {
		t.Fatalf("expected current_id = 12 in projection, got: %v", res.Rows[0]["current_id"])
	}

	// 7. Procedure return code binding: EXEC @rc = sp_sample
	_, err = coord.ExecuteSessionSQL(sessID, `
		CREATE PROCEDURE sp_sample_calc
			@in_val INT,
			@out_val INT OUTPUT
		AS
		BEGIN
			SET @out_val = @in_val * 2;
			RETURN 99;
		END;
	`)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	// Pre-seed @rc = 0 (legacy text substitution would turn `EXEC @rc = ...` into `EXEC 0 = ...` syntax error!)
	_, err = coord.ExecuteSessionSQL(sessID, "SET @rc = 0; SET @res = 0;")
	if err != nil {
		t.Fatalf("SET @rc = 0 failed: %v", err)
	}

	_, err = coord.ExecuteSessionSQL(sessID, "EXEC @rc = sp_sample_calc @in_val = 25, @out_val = @res OUTPUT;")
	if err != nil {
		t.Fatalf("EXEC @rc = sp_sample_calc failed: %v", err)
	}

	res, err = coord.ExecuteSessionSQL(sessID, "SELECT @rc AS ret_code, @res AS out_code;")
	if err != nil {
		t.Fatalf("SELECT @rc, @res failed: %v", err)
	}
	if v, ok := res.Rows[0]["ret_code"].(int64); !ok || v != 99 {
		t.Fatalf("expected @rc = 99, got: %v", res.Rows[0]["ret_code"])
	}
	if v, ok := res.Rows[0]["out_code"].(int64); !ok || v != 50 {
		t.Fatalf("expected @res = 50, got: %v", res.Rows[0]["out_code"])
	}
}
