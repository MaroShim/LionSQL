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
	if len(sess.TempTables) == 0 {
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
