package engine_test

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestCoordinator_ResetSession_IsolatesConnectionPoolState(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	defer coord.Close()

	ctx := context.Background()
	sessID := "pool-worker-1"

	// 1. Setup persistent table
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE accounts (id INT PRIMARY KEY, balance INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO accounts VALUES (1, 1000);"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. Client borrows connection from pool, opens transaction, updates balance, creates temp table, sets session variable
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "UPDATE accounts SET balance = 500 WHERE id = 1;"); err != nil {
		t.Fatalf("UPDATE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE #tmp_pool (id INT, val VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE temp table failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SET @my_session_var = 'secret123';"); err != nil {
		t.Fatalf("SET session variable failed: %v", err)
	}

	// Verify session state is dirty
	sess := coord.GetSession(sessID)
	if sess == nil {
		t.Fatalf("expected active session %s", sessID)
	}
	sess.Mu.Lock()
	if sess.Tx() == nil {
		sess.Mu.Unlock()
		t.Fatalf("expected active uncommitted tx")
	}
	if len(sess.TempTableMap) == 0 {
		sess.Mu.Unlock()
		t.Fatalf("expected active temp tables")
	}
	if sess.Vars["@my_session_var"] != "secret123" {
		sess.Mu.Unlock()
		t.Fatalf("expected session var @my_session_var")
	}
	sess.Mu.Unlock()

	// 3. Connection is returned to pool and ResetSession is invoked
	if err := coord.ResetSession(sessID, true); err != nil {
		t.Fatalf("ResetSession failed: %v", err)
	}

	// 4. Verify transaction is rolled back: accounts balance should still be 1000
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT balance FROM accounts WHERE id = 1;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) == 0 || res.Rows[0]["balance"] != int64(1000) {
		t.Fatalf("expected balance=1000 after rollback, got %v", res.Rows)
	}

	// 5. Verify temp table was dropped: querying #tmp_pool should fail
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM #tmp_pool;")
	if err == nil {
		t.Fatalf("expected query on dropped temp table #tmp_pool to fail, but succeeded")
	}

	// 6. Verify session variables were reset
	sess.Mu.Lock()
	if sess.Tx() != nil {
		sess.Mu.Unlock()
		t.Fatalf("expected nil Tx after reset")
	}
	if _, exists := sess.Vars["@my_session_var"]; exists {
		sess.Mu.Unlock()
		t.Fatalf("expected user session variable to be cleared after reset")
	}
	sess.Mu.Unlock()
}

func TestCoordinator_ResetSessionWithOptions_GranularOptions(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	defer coord.Close()

	ctx := context.Background()

	// 1. Test ResetOptionsPlansOnly
	sessID1 := "sess-plans-only"
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID1, "CREATE TABLE #tmp_keep (id INT);"); err != nil {
		t.Fatalf("CREATE temp table failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID1, "PREPARE p1 AS SELECT 1;"); err != nil {
		t.Fatalf("PREPARE failed: %v", err)
	}

	sess1 := coord.GetSession(sessID1)
	sess1.Mu.Lock()
	if len(sess1.PreparedStmts) == 0 {
		sess1.Mu.Unlock()
		t.Fatalf("expected prepared statement to exist")
	}
	sess1.Mu.Unlock()

	if err := coord.ResetSessionWithOptions(sessID1, engine.ResetOptionsPlansOnly()); err != nil {
		t.Fatalf("ResetSessionWithOptions failed: %v", err)
	}

	sess1.Mu.Lock()
	if len(sess1.PreparedStmts) != 0 {
		sess1.Mu.Unlock()
		t.Fatalf("expected prepared statement to be cleared")
	}
	if len(sess1.TempTableMap) == 0 {
		sess1.Mu.Unlock()
		t.Fatalf("expected temp table #tmp_keep to be preserved")
	}
	sess1.Mu.Unlock()

	// 2. Test ResetOptionsTempOnly
	sessID2 := "sess-temp-only"
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID2, "CREATE TABLE #tmp_drop (id INT);"); err != nil {
		t.Fatalf("CREATE temp table failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID2, "PREPARE p2 AS SELECT 2;"); err != nil {
		t.Fatalf("PREPARE failed: %v", err)
	}

	if err := coord.ResetSessionWithOptions(sessID2, engine.ResetOptionsTempOnly()); err != nil {
		t.Fatalf("ResetSessionWithOptions failed: %v", err)
	}

	sess2 := coord.GetSession(sessID2)
	sess2.Mu.Lock()
	if len(sess2.TempTableMap) != 0 {
		sess2.Mu.Unlock()
		t.Fatalf("expected temp table #tmp_drop to be drained")
	}
	if len(sess2.PreparedStmts) == 0 {
		sess2.Mu.Unlock()
		t.Fatalf("expected prepared statement p2 to be preserved")
	}
	sess2.Mu.Unlock()

	// 3. Test ResetOptionsSkipTran with Cursor & Temp Table
	sessID3 := "sess-skip-tran"
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID3, "CREATE TABLE items (id INT PRIMARY KEY);"); err != nil {
		t.Fatalf("CREATE items failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID3, "INSERT INTO items VALUES (1), (2);"); err != nil {
		t.Fatalf("INSERT items failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID3, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID3, "INSERT INTO items VALUES (3);"); err != nil {
		t.Fatalf("INSERT in tx failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID3, "CREATE TABLE #tmp_skip (id INT);"); err != nil {
		t.Fatalf("CREATE temp failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID3, "DECLARE cur_test CURSOR FOR SELECT * FROM items;"); err != nil {
		t.Fatalf("DECLARE CURSOR failed: %v", err)
	}

	sess3 := coord.GetSession(sessID3)
	sess3.Mu.Lock()
	if sess3.Tx() == nil {
		sess3.Mu.Unlock()
		t.Fatalf("expected active tx")
	}
	if len(sess3.Cursors) == 0 {
		sess3.Mu.Unlock()
		t.Fatalf("expected active cursor")
	}
	sess3.Mu.Unlock()

	// Reset with SkipTran
	if err := coord.ResetSessionWithOptions(sessID3, engine.ResetOptionsSkipTran()); err != nil {
		t.Fatalf("ResetSessionWithOptions failed: %v", err)
	}

	sess3.Mu.Lock()
	if sess3.Tx() == nil {
		sess3.Mu.Unlock()
		t.Fatalf("expected tx to be preserved with SkipTran")
	}
	if len(sess3.Cursors) != 0 {
		sess3.Mu.Unlock()
		t.Fatalf("expected cursor to be cleared")
	}
	if len(sess3.TempTableMap) != 0 {
		sess3.Mu.Unlock()
		t.Fatalf("expected temp table to be dropped")
	}
	sess3.Mu.Unlock()

	// Transaction is still alive and can commit
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID3, "COMMIT;"); err != nil {
		t.Fatalf("COMMIT failed: %v", err)
	}

	// Verify item 3 committed
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID3, "SELECT COUNT(*) AS cnt FROM items;")
	if err != nil {
		t.Fatalf("SELECT count failed: %v", err)
	}
	if len(res.Rows) == 0 || res.Rows[0]["cnt"] != int64(3) {
		t.Fatalf("expected 3 items, got %v", res.Rows)
	}
}
