package runtime_test

import (
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestP0_32_NestedTransaction_Commit_DoesNotCommitPhysically(t *testing.T) {
	eng := setupTestDB(t)
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	// Nested transaction script:
	// Outer BEGIN (tranCount=1) -> INSERT id=10
	// Inner BEGIN (tranCount=2) -> INSERT id=20
	// Inner COMMIT (tranCount=1) -> MUST NOT commit physically!
	// Outer ROLLBACK (tranCount=0) -> MUST rollback BOTH id=10 and id=20!
	script := `
CREATE PROCEDURE TestNestedProc
AS
BEGIN
    BEGIN TRANSACTION;
    INSERT INTO accounts (id, holder, balance) VALUES (10, 'Nest1', 100);

    BEGIN TRANSACTION;
    INSERT INTO accounts (id, holder, balance) VALUES (20, 'Nest2', 200);
    COMMIT TRANSACTION;

    ROLLBACK TRANSACTION;
END;
`
	prog, err := proc.Compile(script)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	t.Logf("Compiled instructions: %d", len(prog.Instructions))
	for i, inst := range prog.Instructions {
		t.Logf("  [%d] op=%v A=%d B=%d C=%d", i, inst.Op, inst.A, inst.B, inst.C)
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)

	_, err = executor.Run(prog, "TestNestedProc", nil, session)
	if err != nil {
		t.Fatalf("Executor.Run failed: %v", err)
	}

	// Verify durability & atomicity: NEITHER id 10 NOR id 20 should exist in accounts
	txCheck, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx for verification failed: %v", err)
	}
	defer txCheck.Rollback()

	rec10, err10 := txCheck.Get("accounts", storage.IntValue(10))
	if err10 == nil && rec10 != nil {
		t.Fatalf("ACID VIOLATION: record id=10 was physically committed despite outer ROLLBACK: %+v", rec10)
	}

	rec20, err20 := txCheck.Get("accounts", storage.IntValue(20))
	if err20 == nil && rec20 != nil {
		t.Fatalf("ACID VIOLATION: record id=20 was physically committed by nested COMMIT despite outer ROLLBACK: %+v", rec20)
	}
}

func TestP0_32_NestedTransaction_Rollback_ResetsTranCountToZero(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	script := `
CREATE PROCEDURE TestTranCountProc
AS
BEGIN
    BEGIN TRANSACTION;
    BEGIN TRANSACTION;
    BEGIN TRANSACTION;
    ROLLBACK TRANSACTION;
    SELECT @@TRANCOUNT AS tc;
END;
`
	prog, err := proc.Compile(script)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)

	res, err := executor.Run(prog, "TestTranCountProc", nil, session)
	if err != nil {
		t.Fatalf("Executor.Run failed: %v", err)
	}

	if len(res.Rows) == 0 {
		t.Fatalf("expected 1 row from SELECT @@TRANCOUNT, got 0")
	}

	tcVal := res.Rows[0]["tc"]
	if tcVal != int64(0) {
		t.Fatalf("expected @@TRANCOUNT=0 after ROLLBACK, got %v", tcVal)
	}
}
