package engine_test

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestCoordinator_Trigger_ScopeIdentityIsolation(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	ctx := context.Background()
	sessID := "tsql-sess-scope-id"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
CREATE TABLE t_scope_trig (id INT IDENTITY(1, 1) PRIMARY KEY, val VARCHAR(20));
CREATE TABLE t_audit_log (audit_id INT IDENTITY(100, 1) PRIMARY KEY, msg VARCHAR(50));
`); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	trgSQL := `
CREATE TRIGGER trg_after_insert_scope
ON t_scope_trig AFTER INSERT AS
BEGIN
    INSERT INTO t_audit_log (msg) VALUES ('inserted');
END;`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, trgSQL); err != nil {
		t.Fatalf("CREATE TRIGGER failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO t_scope_trig (val) VALUES ('test1');"); err != nil {
		t.Fatalf("INSERT into t_scope_trig failed: %v", err)
	}

	resAudit, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM t_audit_log;")
	if err != nil {
		t.Fatalf("SELECT t_audit_log failed: %v", err)
	}
	t.Logf("Audit log rows: %+v", resAudit.Rows)

	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @@IDENTITY AS at_at_identity, SCOPE_IDENTITY() AS scope_identity;")
	if err != nil {
		t.Fatalf("SELECT @@IDENTITY, SCOPE_IDENTITY() failed: %v", err)
	}

	t.Logf("Result rows: %+v", res.Rows)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}

	atAtID := res.Rows[0]["at_at_identity"]
	scopeID := res.Rows[0]["scope_identity"]
	t.Logf("@@IDENTITY = %v, SCOPE_IDENTITY() = %v", atAtID, scopeID)

	// @@IDENTITY must be 100 (from trigger insert into t_audit_log)
	if atAtID != int64(100) && atAtID != 100 {
		t.Errorf("expected @@IDENTITY = 100, got %v", atAtID)
	}

	// SCOPE_IDENTITY() must be 1 (from top-level scope insert into t_scope_trig)
	if scopeID != int64(1) && scopeID != 1 {
		t.Errorf("expected SCOPE_IDENTITY() = 1, got %v", scopeID)
	}

	// Sub-test: Inside stored procedure
	t.Run("InsideProcedure", func(t *testing.T) {
		procDDL := `
CREATE PROCEDURE sp_TestTriggerScopeID AS
BEGIN
    INSERT INTO t_scope_trig (val) VALUES ('test_proc');
    SELECT @@IDENTITY AS at_at_id, SCOPE_IDENTITY() AS scope_id;
END;`
		if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, procDDL); err != nil {
			t.Fatalf("CREATE PROCEDURE failed: %v", err)
		}

		resProc, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_TestTriggerScopeID;")
		if err != nil {
			t.Fatalf("EXEC sp_TestTriggerScopeID failed: %v", err)
		}

		procMeta, _ := cat.GetProcedure("sp_TestTriggerScopeID")
		if procMeta != nil {
			t.Logf("sp_TestTriggerScopeID Disasm:\n%s", procMeta.BytecodeDisasm)
		}

		if len(resProc.Rows) != 1 {
			t.Fatalf("expected 1 row from sp_TestTriggerScopeID, got %d", len(resProc.Rows))
		}

		pAtAtID := resProc.Rows[0]["at_at_id"]
		pScopeID := resProc.Rows[0]["scope_id"]
		t.Logf("Proc @@IDENTITY = %v, SCOPE_IDENTITY() = %v", pAtAtID, pScopeID)

		// t_scope_trig was 1, so test_proc got id=2.
		// Trigger inserted into t_audit_log which had 100, so it got audit_id=101.
		if pAtAtID != int64(101) && pAtAtID != 101 {
			t.Errorf("expected Proc @@IDENTITY = 101, got %v", pAtAtID)
		}
		if pScopeID != int64(2) && pScopeID != 2 {
			t.Errorf("expected Proc SCOPE_IDENTITY() = 2, got %v", pScopeID)
		}
	})
}
