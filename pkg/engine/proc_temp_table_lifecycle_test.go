package engine_test

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestCoordinator_ProcedureLocalTempTable_LifecycleAndMultiCall(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	defer coord.Close()

	ctx := context.Background()
	sessID := "pool-session-proc-temp"

	// 1. Defect 6 Reproduction: Repeated procedure calls creating #local_temp in same pooled session
	procDDL := `CREATE PROCEDURE sp_TestLocalTempTableMultiCall AS
BEGIN
    CREATE TABLE #multi_temp (id INT PRIMARY KEY, val VARCHAR(50));
    INSERT INTO #multi_temp VALUES (1, 'hello');
    SELECT id, val FROM #multi_temp;
END;`

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, procDDL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	for i := 1; i <= 3; i++ {
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_TestLocalTempTableMultiCall;")
		if err != nil {
			t.Fatalf("Call %d: EXEC sp_TestLocalTempTableMultiCall failed: %v", i, err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("Call %d: expected 1 row, got %d", i, len(res.Rows))
		}
		if res.Rows[0]["id"] != int64(1) || res.Rows[0]["val"] != "hello" {
			t.Fatalf("Call %d: unexpected row data: %v", i, res.Rows[0])
		}
	}

	// 2. Explicit DROP inside stored procedure
	procDropDDL := `CREATE PROCEDURE sp_TestLocalTempExplicitDrop AS
BEGIN
    CREATE TABLE #explicit_drop (x INT PRIMARY KEY);
    INSERT INTO #explicit_drop VALUES (42);
    DROP TABLE #explicit_drop;
END;`

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, procDropDDL); err != nil {
		t.Fatalf("CREATE PROCEDURE with explicit DROP failed: %v", err)
	}

	for i := 1; i <= 2; i++ {
		if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_TestLocalTempExplicitDrop;"); err != nil {
			t.Fatalf("Call %d: EXEC sp_TestLocalTempExplicitDrop failed: %v", i, err)
		}
	}

	// 3. Shadowing: Procedure local temp table shadows outer session temp table and does not corrupt outer table
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE #shared_temp (id INT PRIMARY KEY, scope VARCHAR(20));"); err != nil {
		t.Fatalf("outer CREATE TABLE #shared_temp failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO #shared_temp VALUES (10, 'outer');"); err != nil {
		t.Fatalf("outer INSERT into #shared_temp failed: %v", err)
	}

	procShadowDDL := `CREATE PROCEDURE sp_TestInnerTempTable AS
BEGIN
    CREATE TABLE #shared_temp (id INT PRIMARY KEY, scope VARCHAR(20));
    INSERT INTO #shared_temp VALUES (20, 'inner');
    SELECT id, scope FROM #shared_temp;
END;`

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, procShadowDDL); err != nil {
		t.Fatalf("CREATE PROCEDURE sp_TestInnerTempTable failed: %v", err)
	}

	resInner, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_TestInnerTempTable;")
	if err != nil {
		t.Fatalf("EXEC sp_TestInnerTempTable failed: %v", err)
	}
	if len(resInner.Rows) != 1 || resInner.Rows[0]["id"] != int64(20) || resInner.Rows[0]["scope"] != "inner" {
		t.Fatalf("unexpected inner procedure result: %v", resInner.Rows)
	}

	// Verify outer table is intact
	resOuter, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, scope FROM #shared_temp;")
	if err != nil {
		t.Fatalf("outer SELECT #shared_temp failed: %v", err)
	}
	if len(resOuter.Rows) != 1 || resOuter.Rows[0]["id"] != int64(10) || resOuter.Rows[0]["scope"] != "outer" {
		t.Fatalf("expected outer #shared_temp to be intact, got: %v", resOuter.Rows)
	}

	// Call inner procedure a second time
	resInner2, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_TestInnerTempTable;")
	if err != nil {
		t.Fatalf("second EXEC sp_TestInnerTempTable failed: %v", err)
	}
	if len(resInner2.Rows) != 1 || resInner2.Rows[0]["id"] != int64(20) || resInner2.Rows[0]["scope"] != "inner" {
		t.Fatalf("second call: unexpected inner procedure result: %v", resInner2.Rows)
	}

	// 4. Procedure accessing outer session temp table
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE #outer_only (num INT PRIMARY KEY);"); err != nil {
		t.Fatalf("outer CREATE TABLE #outer_only failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO #outer_only VALUES (999);"); err != nil {
		t.Fatalf("outer INSERT into #outer_only failed: %v", err)
	}

	procReadOuterDDL := `CREATE PROCEDURE sp_ReadOuterTemp AS
BEGIN
    SELECT num FROM #outer_only;
END;`

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, procReadOuterDDL); err != nil {
		t.Fatalf("CREATE PROCEDURE sp_ReadOuterTemp failed: %v", err)
	}

	resReadOuter, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_ReadOuterTemp;")
	if err != nil {
		t.Fatalf("EXEC sp_ReadOuterTemp failed: %v", err)
	}
	if len(resReadOuter.Rows) != 1 || resReadOuter.Rows[0]["num"] != int64(999) {
		t.Fatalf("expected outer num 999, got: %v", resReadOuter.Rows)
	}
}
