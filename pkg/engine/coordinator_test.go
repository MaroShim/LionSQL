package engine_test

import (
	"context"
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/engine"
	"sqlion/pkg/storage/memengine"
)

func TestCoordinator_DirectExecutionLifecycle(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Direct DDL
	createSQL := "CREATE TABLE accounts (id INT PRIMARY KEY, name VARCHAR(100), balance FLOAT);"
	res, err := coord.ExecuteSQL(createSQL)
	if err != nil || !res.Success {
		t.Fatalf("CREATE TABLE failed: %v (res: %+v)", err, res)
	}

	// 2. Direct DML
	insertSQL := "INSERT INTO accounts (id, name, balance) VALUES (1, 'Alice', 1500.5), (2, 'Bob', 2500.0);"
	res, err = coord.ExecuteSQL(insertSQL)
	if err != nil || !res.Success {
		t.Fatalf("INSERT failed: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Fatalf("expected 2 rows affected, got %d", res.RowsAffected)
	}

	// 3. Direct DQL
	selectSQL := "SELECT id, name, balance FROM accounts WHERE balance > 2000 ORDER BY id ASC;"
	res, err = coord.ExecuteSQL(selectSQL)
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0]["name"] != "Bob" {
		t.Fatalf("expected 'Bob', got %v", res.Rows[0]["name"])
	}

	// 4. Session Transactions: Explicit Begin, Insert, Rollback
	sessID := "test-sess-1"
	if _, err := coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO accounts VALUES (3, 'Charlie', 500.0);"); err != nil {
		t.Fatalf("Session INSERT failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Verify Charlie is not present
	checkRes, err := coord.ExecuteSQL("SELECT * FROM accounts WHERE id = 3;")
	if err != nil || !checkRes.Success {
		t.Fatalf("verify query failed: %v", err)
	}
	if len(checkRes.Rows) != 0 {
		t.Fatalf("expected 0 rows after rollback, got %d", len(checkRes.Rows))
	}

	// 5. Context Cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately
	_, err = coord.ExecuteSessionSQLContext(ctx, "cancel-sess", "SELECT * FROM accounts;")
	if err == nil {
		t.Fatalf("expected error on cancelled context, got nil")
	}
}
