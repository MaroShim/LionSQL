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

func TestCoordinator_Trigger_RowCountPreservation_R1_08(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	ctx := context.Background()
	sessID := "sess-rc-test"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
CREATE TABLE t_rowcount (id INT PRIMARY KEY, val INT);
INSERT INTO t_rowcount VALUES (1, 10), (2, 20);
CREATE TABLE t_audit_rc (cnt INT);
`); err != nil {
		t.Fatalf("setup tables failed: %v", err)
	}

	trgSQL := `
CREATE TRIGGER trg_test_rowcount ON t_rowcount AFTER UPDATE AS
BEGIN
    DECLARE @trig_rc INT = @@ROWCOUNT;
    INSERT INTO t_audit_rc (cnt) VALUES (@trig_rc);
END;`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, trgSQL); err != nil {
		t.Fatalf("CREATE TRIGGER failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "UPDATE t_rowcount SET val = val * 2;"); err != nil {
		t.Fatalf("UPDATE failed: %v", err)
	}

	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT cnt FROM t_audit_rc;")
	if err != nil {
		t.Fatalf("SELECT t_audit_rc failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(res.Rows))
	}
	cnt := res.Rows[0]["cnt"]
	if cnt != int64(2) && cnt != 2 {
		t.Fatalf("expected @@ROWCOUNT in trigger = 2, got %v", cnt)
	}
}

func TestCoordinator_Session_Identity_Preservation_R2_N11(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	ctx := context.Background()
	sessID := "sess-ident-r2-n11"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE r2_main (id INT IDENTITY(1,1) PRIMARY KEY, v VARCHAR(10));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO r2_main (v) VALUES ('x');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @@IDENTITY AS at_at_id, SCOPE_IDENTITY() AS scope_id;")
	if err != nil {
		t.Fatalf("SELECT @@IDENTITY, SCOPE_IDENTITY() failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	atAtID := res.Rows[0]["at_at_id"]
	scopeID := res.Rows[0]["scope_id"]
	t.Logf("Result: @@IDENTITY = %v, SCOPE_IDENTITY() = %v", atAtID, scopeID)

	if atAtID != int64(1) && atAtID != 1 {
		t.Errorf("expected @@IDENTITY = 1, got %v", atAtID)
	}
	if scopeID != int64(1) && scopeID != 1 {
		t.Errorf("expected SCOPE_IDENTITY() = 1, got %v", scopeID)
	}

	// Simulate separate `psql -c` connection querying @@IDENTITY, SCOPE_IDENTITY() without aliases
	coord.CloseSession(sessID)
	resSep, err := coord.ExecuteSessionSQLContext(ctx, "sess-ident-r2-n11-conn2", "SELECT @@IDENTITY, SCOPE_IDENTITY();")
	if err != nil {
		t.Fatalf("separate session SELECT @@IDENTITY, SCOPE_IDENTITY() failed: %v", err)
	}
	if len(resSep.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(resSep.Rows))
	}
	sepAtAt := resSep.Rows[0]["@@identity"]
	sepScope := resSep.Rows[0]["scope_identity"]
	if sepAtAt != int64(1) && sepAtAt != 1 {
		t.Errorf("expected separate session @@IDENTITY = 1, got %v", sepAtAt)
	}
	if sepScope != int64(1) && sepScope != 1 {
		t.Errorf("expected separate session SCOPE_IDENTITY() = 1, got %v", sepScope)
	}

	// Anonymous BEGIN...END block INSERT followed by SELECT @@IDENTITY, SCOPE_IDENTITY()
	if _, err := coord.ExecuteSessionSQLContext(ctx, "sess-ident-r2-n11-conn3", "BEGIN INSERT INTO r2_main (v) VALUES ('y'); END;"); err != nil {
		t.Fatalf("anonymous block INSERT failed: %v", err)
	}
	resBlk, err := coord.ExecuteSessionSQLContext(ctx, "sess-ident-r2-n11-conn3", "SELECT @@IDENTITY, SCOPE_IDENTITY();")
	if err != nil {
		t.Fatalf("post-block SELECT @@IDENTITY, SCOPE_IDENTITY() failed: %v", err)
	}
	if resBlk.Rows[0]["@@identity"] != int64(2) && resBlk.Rows[0]["@@identity"] != 2 {
		t.Errorf("expected post-block @@IDENTITY = 2, got %v", resBlk.Rows[0]["@@identity"])
	}
	if resBlk.Rows[0]["scope_identity"] != int64(2) && resBlk.Rows[0]["scope_identity"] != 2 {
		t.Errorf("expected post-block SCOPE_IDENTITY() = 2, got %v", resBlk.Rows[0]["scope_identity"])
	}
}
