package engine_test

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestP0_32_Procedure_CalledWithinOuterTransaction_NestedCommit_Rollback(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	ctx := context.Background()
	sessID := "nested-tx-sess-1"

	// Setup table
	_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
CREATE TABLE accounts (
    id INT PRIMARY KEY,
    holder VARCHAR(50),
    balance INT
);
`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Create stored procedure with its own BEGIN ... COMMIT
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `
CREATE PROCEDURE usp_InnerWork
AS
BEGIN
    BEGIN TRANSACTION;
    INSERT INTO accounts (id, holder, balance) VALUES (20, 'InnerUser', 200);
    COMMIT TRANSACTION;
END;
`)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	// Execution scenario:
	// 1. Outer BEGIN TRANSACTION
	// 2. Outer INSERT id=10
	// 3. EXEC usp_InnerWork (which does inner BEGIN -> INSERT id=20 -> inner COMMIT)
	// 4. Outer ROLLBACK TRANSACTION
	// Result: BOTH id=10 AND id=20 MUST BE ROLLED BACK!
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("Outer BEGIN failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO accounts (id, holder, balance) VALUES (10, 'OuterUser', 100);"); err != nil {
		t.Fatalf("Outer INSERT failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC usp_InnerWork;"); err != nil {
		t.Fatalf("EXEC usp_InnerWork failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("Outer ROLLBACK failed: %v", err)
	}

	// Verify durability & atomicity: NEITHER 10 NOR 20 should exist!
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT COUNT(*) AS cnt FROM accounts;")
	if err != nil {
		t.Fatalf("SELECT COUNT failed: %v", err)
	}

	if len(res.Rows) == 0 {
		t.Fatalf("expected 1 row, got 0")
	}

	cnt := res.Rows[0]["cnt"]
	if cnt != int64(0) {
		t.Fatalf("ACID VIOLATION: expected 0 accounts after outer ROLLBACK, got %v (inner COMMIT physically committed outer tx!)", cnt)
	}
}

func TestP0_32_Interactive_NestedTransactions_TranCount(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	ctx := context.Background()
	sessID := "interactive-nested-tx"

	// 1. Initial @@TRANCOUNT should be 0
	r0, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @@TRANCOUNT AS tc;")
	if err != nil || len(r0.Rows) == 0 || r0.Rows[0]["tc"] != int64(0) {
		t.Fatalf("expected @@TRANCOUNT=0 initially, got %v", r0.Rows)
	}

	// 2. BEGIN TRAN 1 -> @@TRANCOUNT = 1
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN 1 failed: %v", err)
	}
	r1, _ := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @@TRANCOUNT AS tc;")
	if r1.Rows[0]["tc"] != int64(1) {
		t.Fatalf("expected @@TRANCOUNT=1 after 1st BEGIN, got %v", r1.Rows[0]["tc"])
	}

	// 3. Nested BEGIN TRAN 2 -> @@TRANCOUNT = 2
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("nested BEGIN 2 failed: %v", err)
	}
	r2, _ := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @@TRANCOUNT AS tc;")
	if r2.Rows[0]["tc"] != int64(2) {
		t.Fatalf("expected @@TRANCOUNT=2 after 2nd BEGIN, got %v", r2.Rows[0]["tc"])
	}

	// 4. Nested COMMIT 1 -> @@TRANCOUNT = 1
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "COMMIT TRANSACTION;"); err != nil {
		t.Fatalf("nested COMMIT failed: %v", err)
	}
	r3, _ := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @@TRANCOUNT AS tc;")
	if r3.Rows[0]["tc"] != int64(1) {
		t.Fatalf("expected @@TRANCOUNT=1 after nested COMMIT, got %v", r3.Rows[0]["tc"])
	}

	// 5. Outer ROLLBACK -> @@TRANCOUNT = 0
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("outer ROLLBACK failed: %v", err)
	}
	r4, _ := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @@TRANCOUNT AS tc;")
	if r4.Rows[0]["tc"] != int64(0) {
		t.Fatalf("expected @@TRANCOUNT=0 after outer ROLLBACK, got %v", r4.Rows[0]["tc"])
	}
}
