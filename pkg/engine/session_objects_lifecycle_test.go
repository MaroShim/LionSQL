package engine

import (
	"testing"

	"lionsql/pkg/storage"
)

func TestClientSession_SessionObjectsLifecycle_SSOT(t *testing.T) {
	cs := &ClientSession{
		ID: "sess_lifecycle_test",
	}

	// 1. Register Table Variables
	cs.RegisterTableVar("@my_tbl_var", "phys_tbl_var_1")
	phys, exists := cs.GetTableVarPhysical("@my_tbl_var")
	if !exists || phys != "phys_tbl_var_1" {
		t.Fatalf("expected phys_tbl_var_1, got %q (exists=%v)", phys, exists)
	}
	phys2, exists2 := cs.GetTableVarPhysical("my_tbl_var")
	if !exists2 || phys2 != "phys_tbl_var_1" {
		t.Fatalf("expected lookup without @ to match, got %q (exists=%v)", phys2, exists2)
	}

	// 2. Register Temp Tables
	tempPhys := cs.RegisterTempTable("#my_temp")
	if tempPhys == "" {
		t.Fatalf("expected non-empty physical name for #my_temp")
	}
	pgTempPhys := cs.RegisterTempTable("pg_temp_tbl")
	if pgTempPhys == "" {
		t.Fatalf("expected non-empty physical name for pg_temp_tbl")
	}

	// 3. Register Ephemeral Table, Prepared Stmt, and Cursor
	cs.EphemeralTables = map[string]*EphemeralTable{
		"cte_mat": {Schema: &storage.TableSchema{Name: "cte_mat"}},
	}
	cs.PreparedStmts = map[string]*PreparedStatement{
		"stmt1": {Query: "SELECT 1"},
	}
	cs.Cursors = map[string]*SessionCursor{
		"cur1": {Name: "cur1"},
	}

	// 4. Drain all storage tables
	toDrop := cs.DrainStorageTablesToDrop()
	if len(toDrop) != 3 {
		t.Fatalf("expected 3 physical tables to drop, got %d (%v)", len(toDrop), toDrop)
	}

	// Verify all temporary maps are nil or cleared
	cs.Mu.Lock()
	if cs.TableVars != nil {
		t.Errorf("expected TableVars to be nil, got %v", cs.TableVars)
	}
	if cs.TempTableMap != nil {
		t.Errorf("expected TempTableMap to be nil, got %v", cs.TempTableMap)
	}
	if cs.EphemeralTables != nil {
		t.Errorf("expected EphemeralTables to be nil, got %v", cs.EphemeralTables)
	}
	if cs.PreparedStmts != nil {
		t.Errorf("expected PreparedStmts to be nil, got %v", cs.PreparedStmts)
	}
	if len(cs.Cursors) != 0 {
		t.Errorf("expected Cursors to be cleared, got len %d", len(cs.Cursors))
	}
	cs.Mu.Unlock()

	// 5. Subsequent Drain returns nil
	toDropSecond := cs.DrainStorageTablesToDrop()
	if len(toDropSecond) != 0 {
		t.Fatalf("expected 0 tables on second drain, got %d", len(toDropSecond))
	}
}

func TestCoordinator_SessionObjectsLifecycle_DropOnCloseAndReset(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sessID := "sess_coord_lifecycle"
	_, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE #tmp_coord (id INT PRIMARY KEY);")
	if err != nil {
		t.Fatalf("CREATE TABLE #tmp_coord failed: %v", err)
	}

	sess := coord.GetSession(sessID)
	if sess == nil {
		t.Fatalf("expected active session %s", sessID)
	}

	// Verify temp table exists in storage
	phys, exists := sess.GetTempTablePhysical("#tmp_coord")
	if !exists || phys == "" {
		t.Fatalf("expected physical table for #tmp_coord")
	}
	_, err = coord.engine.GetTable(phys)
	if err != nil {
		t.Fatalf("expected physical table %s to exist in engine: %v", phys, err)
	}

	// ResetSession must drop the physical table from engine and clear session maps
	if err := coord.ResetSession(sessID, true); err != nil {
		t.Fatalf("ResetSession failed: %v", err)
	}

	sess.Mu.Lock()
	if len(sess.TempTableMap) != 0 || len(sess.TableVars) != 0 {
		sess.Mu.Unlock()
		t.Fatalf("expected session temp tables and table vars to be drained after ResetSession")
	}
	sess.Mu.Unlock()

	_, err = coord.engine.GetTable(phys)
	if err == nil {
		t.Fatalf("expected physical table %s to be dropped from engine after ResetSession", phys)
	}
}
