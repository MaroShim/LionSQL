package engine_test

import (
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/engine"
	"sqlion/pkg/storage/memengine"
)

// TestRegression_BUG_RECORD_1_DML verifies that INSERT and UPDATE with mixed-case column names
// properly normalize to canonical schema column names, preventing silent loss or NULL insertion.
func TestRegression_BUG_RECORD_1_DML(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	createSQL := `CREATE TABLE customers (
		customer_id INT PRIMARY KEY,
		full_name VARCHAR(100),
		email_addr VARCHAR(100)
	);`
	if _, err := coord.ExecuteSQL(createSQL); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Insert with mixed-case column names
	insertSQL := `INSERT INTO customers (CUSTOMER_ID, FULL_NAME, Email_Addr) VALUES (1, 'Alice Smith', 'alice@test.com');`
	res, err := coord.ExecuteSQL(insertSQL)
	if err != nil || res.RowsAffected != 1 {
		t.Fatalf("INSERT with mixed-case columns failed: %v (rows: %d)", err, res.RowsAffected)
	}

	// Verify row values are non-null and match
	selRes, err := coord.ExecuteSQL("SELECT customer_id, full_name, email_addr FROM customers WHERE customer_id = 1;")
	if err != nil || len(selRes.Rows) != 1 {
		t.Fatalf("SELECT failed: %v", err)
	}
	if selRes.Rows[0]["full_name"] != "Alice Smith" {
		t.Fatalf("expected 'Alice Smith', got %v", selRes.Rows[0]["full_name"])
	}
	if selRes.Rows[0]["email_addr"] != "alice@test.com" {
		t.Fatalf("expected 'alice@test.com', got %v", selRes.Rows[0]["email_addr"])
	}

	// Update with mixed-case column names (forcing coordinator fallback with OUTPUT)
	updateSQL := `UPDATE customers SET EMAIL_ADDR = 'updated@test.com' OUTPUT INSERTED.email_addr WHERE customer_id = 1;`
	updRes, err := coord.ExecuteSQL(updateSQL)
	if err != nil || updRes.RowsAffected != 1 {
		t.Fatalf("UPDATE with mixed-case columns failed: %v", err)
	}

	// Verify updated value
	selRes2, err := coord.ExecuteSQL("SELECT email_addr FROM customers WHERE customer_id = 1;")
	if err != nil || len(selRes2.Rows) != 1 {
		t.Fatalf("second SELECT failed: %v", err)
	}
	if selRes2.Rows[0]["email_addr"] != "updated@test.com" {
		t.Fatalf("expected 'updated@test.com', got %v", selRes2.Rows[0]["email_addr"])
	}
}
