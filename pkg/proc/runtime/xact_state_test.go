package runtime_test

import (
	"strings"
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage/memengine"
)

func TestTSQL_XactState_Procedure(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	script := `
CREATE PROCEDURE TestXactStateLifecycle
AS
BEGIN
    DECLARE @s_init INT, @s_active INT, @s_failed INT, @s_after_rb INT;
    
    -- In ambient transaction (Executor.Run starts ambient tx if tranCount > 0)
    SET @s_init = XACT_STATE();

    BEGIN TRANSACTION;
    SET @s_active = XACT_STATE();

    BEGIN TRY
        -- Trigger runtime error to cause uncommittable transaction
        RAISERROR('simulated error', 16, 1);
    END TRY
    BEGIN CATCH
        SET @s_failed = XACT_STATE();
        ROLLBACK TRANSACTION;
        SET @s_after_rb = XACT_STATE();
    END CATCH

    SELECT @s_init AS s_init, @s_active AS s_active, @s_failed AS s_failed, @s_after_rb AS s_after_rb;
END;
`
	prog, err := proc.Compile(script)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)

	res, err := executor.Run(prog, "TestXactStateLifecycle", nil, session)
	if err != nil {
		t.Fatalf("Executor.Run failed: %v", err)
	}

	if len(res.Rows) == 0 {
		t.Fatalf("expected 1 row result, got 0")
	}

	row := res.Rows[0]
	t.Logf("XACT_STATE lifecycle results: %+v", row)

	// In Executor.Run, an ambient transaction is active (tranCount=1), so s_init is 1.
	if row["s_active"] != int64(1) {
		t.Errorf("expected s_active=1, got %v", row["s_active"])
	}
	if row["s_failed"] != int64(-1) {
		t.Errorf("expected s_failed=-1, got %v", row["s_failed"])
	}
	if row["s_after_rb"] != int64(0) {
		t.Errorf("expected s_after_rb=0, got %v", row["s_after_rb"])
	}
}

func TestTSQL_XactState_Uncommittable_RejectsCommit(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	script := `
CREATE PROCEDURE TestXactStateCommitFails
AS
BEGIN
    BEGIN TRANSACTION;
    BEGIN TRY
        RAISERROR('trigger failure', 16, 1);
    END TRY
    BEGIN CATCH
        -- Trying to commit an uncommittable transaction (-1) must fail
        COMMIT TRANSACTION;
    END CATCH
END;
`
	prog, err := proc.Compile(script)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)

	_, err = executor.Run(prog, "TestXactStateCommitFails", nil, session)
	if err == nil {
		t.Fatalf("expected error when committing uncommittable transaction, got nil")
	}
	if !strings.Contains(err.Error(), "Uncommittable transaction") {
		t.Fatalf("expected 'Uncommittable transaction' in error, got: %v", err)
	}
}
