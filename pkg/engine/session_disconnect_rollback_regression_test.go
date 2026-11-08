package engine

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

// TestSession_Regression_P0_26_DisconnectRollback tests that when a client session
// abruptly disconnects (simulated via CloseSession while an active transaction is open),
// 1) The active transaction is immediately rolled back without leaking locks.
// 2) Any temporary tables are cleanly dropped without deadlock.
// 3) Subsequent sessions can immediately modify the same rows without lock contention or serialization conflicts.
func TestSession_Regression_P0_26_DisconnectRollback(t *testing.T) {
	memEng := memengine.New()
	cat := catalog.New(memEng)
	coord := NewCoordinator(cat, memEng, Config{})
	defer coord.Close()

	ctx := context.Background()

	// 1. Create test table and insert initial data
	_, err := coord.ExecuteSessionSQLContext(ctx, "default", "CREATE TABLE accounts (id INT PRIMARY KEY, balance INT);")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
	_, err = coord.ExecuteSessionSQLContext(ctx, "default", "INSERT INTO accounts (id, balance) VALUES (1, 1000);")
	if err != nil {
		t.Fatalf("failed to insert initial row: %v", err)
	}

	// 2. Session 1 connects and opens an explicit transaction
	sess1ID := "sess-p0-26-client1"
	cs1, err := coord.GetOrCreateSession(sess1ID)
	if err != nil {
		t.Fatalf("failed to create session 1: %v", err)
	}

	_, err = coord.ExecuteSessionSQLContext(ctx, sess1ID, "BEGIN TRANSACTION;")
	if err != nil {
		t.Fatalf("failed to begin tx in session 1: %v", err)
	}

	// Session 1 updates row id=1
	_, err = coord.ExecuteSessionSQLContext(ctx, sess1ID, "UPDATE accounts SET balance = 500 WHERE id = 1;")
	if err != nil {
		t.Fatalf("failed to update in session 1: %v", err)
	}

	// Session 1 also creates a temporary table
	_, err = coord.ExecuteSessionSQLContext(ctx, sess1ID, "CREATE TABLE #tmp_p026 (id INT, val VARCHAR(50));")
	if err != nil {
		t.Fatalf("failed to create temp table in session 1: %v", err)
	}

	// Verify session 1 has active transaction
	cs1.Mu.Lock()
	if cs1.Tx == nil {
		cs1.Mu.Unlock()
		t.Fatalf("expected active transaction on session 1, got nil")
	}
	cs1.Mu.Unlock()

	// 3. Simulate sudden network disconnect of Session 1
	coord.CloseSession(sess1ID)

	// Verify Session 1 is removed from coordinator
	if s := coord.GetSession(sess1ID); s != nil {
		t.Fatalf("expected session 1 to be unregistered after CloseSession, got %v", s)
	}

	// 4. Verify uncommitted changes were rolled back completely
	res, err := coord.ExecuteSessionSQLContext(ctx, "default", "SELECT balance FROM accounts WHERE id = 1;")
	if err != nil {
		t.Fatalf("failed to query accounts after rollback: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	gotBal := fmt.Sprintf("%v", res.Rows[0]["balance"])
	if gotBal != "1000" {
		t.Fatalf("expected balance to remain 1000 after rollback, got %s", gotBal)
	}

	// 5. Session 2 connects immediately and updates the exact same row (id=1)
	// If locks leaked, this would fail with serialization failure or deadlock.
	sess2ID := "sess-p0-26-client2"
	_, err = coord.ExecuteSessionSQLContext(ctx, sess2ID, "BEGIN TRANSACTION;")
	if err != nil {
		t.Fatalf("failed to begin tx in session 2: %v", err)
	}

	_, err = coord.ExecuteSessionSQLContext(ctx, sess2ID, "UPDATE accounts SET balance = 2000 WHERE id = 1;")
	if err != nil {
		t.Fatalf("session 2 failed to update row (lock leak detected!): %v", err)
	}

	_, err = coord.ExecuteSessionSQLContext(ctx, sess2ID, "COMMIT;")
	if err != nil {
		t.Fatalf("session 2 failed to commit: %v", err)
	}

	// Verify new balance is 2000
	res2, err := coord.ExecuteSessionSQLContext(ctx, "default", "SELECT balance FROM accounts WHERE id = 1;")
	if err != nil {
		t.Fatalf("failed to query accounts after session 2 commit: %v", err)
	}
	gotBal2 := fmt.Sprintf("%v", res2.Rows[0]["balance"])
	if gotBal2 != "2000" {
		t.Fatalf("expected balance to be 2000 after session 2, got %s", gotBal2)
	}

	coord.CloseSession(sess2ID)
	t.Logf("[P0-26 PASS] Session disconnect cleanly rolled back transaction and freed all row locks")
}

// TestSession_Regression_P0_26_ConcurrentCloseAndDDL tests that CloseSession does not deadlock
// when concurrent DDL operations occur on the storage engine (validates Lock Hierarchy).
func TestSession_Regression_P0_26_ConcurrentCloseAndDDL(t *testing.T) {
	memEng := memengine.New()
	cat := catalog.New(memEng)
	coord := NewCoordinator(cat, memEng, Config{})
	defer coord.Close()

	ctx := context.Background()

	// Create base table
	_, _ = coord.ExecuteSessionSQLContext(ctx, "default", "CREATE TABLE base_items (id INT PRIMARY KEY, val INT);")
	_, _ = coord.ExecuteSessionSQLContext(ctx, "default", "INSERT INTO base_items (id, val) VALUES (1, 100);")

	var wg sync.WaitGroup
	errCh := make(chan error, 50)

	// Run 20 concurrent sessions that open transactions, create temp tables, and abruptly close
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sID := fmt.Sprintf("stress-sess-%d", idx)
			_, err := coord.ExecuteSessionSQLContext(ctx, sID, "BEGIN TRAN;")
			if err != nil {
				errCh <- fmt.Errorf("begin failed: %w", err)
				return
			}
			_, err = coord.ExecuteSessionSQLContext(ctx, sID, fmt.Sprintf("CREATE TABLE #temp_%d (x INT);", idx))
			if err != nil {
				errCh <- fmt.Errorf("create temp table failed: %w", err)
				return
			}
			_, _ = coord.ExecuteSessionSQLContext(ctx, sID, fmt.Sprintf("INSERT INTO #temp_%d VALUES (%d);", idx, idx))

			// Simulate sudden disconnect
			coord.CloseSession(sID)
		}(i)
	}

	// Concurrently run DDL operations on default session
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tblName := fmt.Sprintf("ddl_tbl_%d", idx)
			_, _ = coord.ExecuteSessionSQLContext(ctx, "default", fmt.Sprintf("CREATE TABLE %s (id INT PRIMARY KEY);", tblName))
			time.Sleep(5 * time.Millisecond)
			_, _ = coord.ExecuteSessionSQLContext(ctx, "default", fmt.Sprintf("DROP TABLE %s;", tblName))
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent CloseSession and DDL failed: %v", err)
	}

	t.Logf("[P0-26 PASS] Concurrent CloseSession and DDL completed with zero deadlocks")
}
