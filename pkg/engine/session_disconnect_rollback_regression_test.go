package engine

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
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

// TestSession_Regression_P0_26_DisconnectDuringInFlightQuery_NoEarlyPoolRelease verifies that
// when CloseSession is invoked while a query is still in-flight on cs.Session,
// the storage session is NOT prematurely released back to SessionPool while the query goroutine
// is still executing (preventing CGo WT_SESSION* concurrent access races), and is safely
// reclaimed only after the in-flight query has completed.
func TestSession_Regression_P0_26_DisconnectDuringInFlightQuery_NoEarlyPoolRelease(t *testing.T) {
	memEng := memengine.New()
	cat := catalog.New(memEng)
	coord := NewCoordinator(cat, memEng, Config{})
	defer coord.Close()

	sessID := "inflight-sess"
	cs, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	cs.Mu.Lock()
	underlyingSess := cs.Session
	cs.Mu.Unlock()
	if underlyingSess == nil {
		t.Fatalf("expected underlying storage session on cs")
	}

	queryRunning := make(chan struct{})
	queryFinished := make(chan struct{})

	// Spawn in-flight query goroutine that leases cs.Session
	go func() {
		defer close(queryFinished)
		sess, closeFn, acqErr := coord.acquireSessionForQuery(cs)
		if acqErr != nil {
			t.Errorf("acquireSessionForQuery failed: %v", acqErr)
			return
		}
		defer closeFn()

		close(queryRunning)

		// Simulate active engine execution work taking 40ms
		time.Sleep(40 * time.Millisecond)
		_ = sess
	}()

	<-queryRunning

	// While query is actively in-flight on underlyingSess, client disconnects
	coord.CloseSession(sessID)

	// Step 1: Verify that while query is still in-flight, underlyingSess was NOT dumped to pool
	statsDuring := coord.pool.Stats()
	if statsDuring.IdleSessions != 0 {
		t.Fatalf("DATA RACE VIOLATION: storage session was prematurely released to SessionPool while query was still in-flight (IdleSessions=%d)", statsDuring.IdleSessions)
	}

	// Step 2: Wait for in-flight query to finish and drain
	<-queryFinished

	// Step 3: Verify that after query drain, the session was cleanly reclaimed into the pool
	statsAfter := coord.pool.Stats()
	if statsAfter.IdleSessions != 1 {
		t.Fatalf("expected 1 idle session in pool after query drain, got %d", statsAfter.IdleSessions)
	}

	// Reacquire from pool and verify it's the exact same underlying session
	reacquired, err := coord.pool.AcquireSession()
	if err != nil || reacquired != underlyingSess {
		t.Fatalf("expected reacquired session to match drained underlyingSess, got %v (err: %v)", reacquired, err)
	}
	coord.pool.ReleaseSession(reacquired)
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

func TestSession_ExecuteProgram_TSQL_NonAbortingError(t *testing.T) {
	memEng := memengine.New()
	defer memEng.Close()
	cat := catalog.New(memEng)
	coord := NewCoordinator(cat, memEng, Config{})
	defer coord.Close()

	ctx := context.Background()

	// 1. Create table with PK
	_, err := coord.ExecuteSQL("CREATE TABLE items_prog (id INT PRIMARY KEY, val INT);")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	// 2. Create T-SQL session and start transaction
	sessID := "tsql-prepared-sess"
	cs, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	coord.SetSessionDialect(sessID, DialectTSQL)

	if err := coord.BeginSessionTx(sessID, storage.TxOptions{ReadOnly: false}); err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}

	exec := coord.Executor()

	// Program 1: valid insert (id=1, val=100)
	prog1, err := proc.Compile("INSERT INTO items_prog (id, val) VALUES (1, 100);")
	if err != nil {
		t.Fatalf("compile prog1: %v", err)
	}
	_, txAborted, err := cs.ExecuteProgram(ctx, prog1, exec, nil, memEng, "stmt1")
	if err != nil || txAborted {
		t.Fatalf("prog1 failed: %v, txAborted=%v", err, txAborted)
	}

	// Program 2: deliberate PK violation (id=1, val=200)
	prog2, err := proc.Compile("INSERT INTO items_prog (id, val) VALUES (1, 200);")
	if err != nil {
		t.Fatalf("compile prog2: %v", err)
	}
	_, _, err = cs.ExecuteProgram(ctx, prog2, exec, nil, memEng, "stmt2")
	if err == nil {
		t.Fatalf("expected duplicate key error on prog2, got nil")
	}

	// In T-SQL, statement-level error MUST NOT abort the transaction (TxFailed should be false)!
	if cs.IsTxFailed() {
		t.Fatalf("expected TxFailed=false for T-SQL session after statement error, but got true")
	}

	// Program 3: valid subsequent insert in the SAME transaction (id=2, val=300)
	prog3, err := proc.Compile("INSERT INTO items_prog (id, val) VALUES (2, 300);")
	if err != nil {
		t.Fatalf("compile prog3: %v", err)
	}
	_, txAborted, err = cs.ExecuteProgram(ctx, prog3, exec, nil, memEng, "stmt3")
	if err != nil || txAborted {
		t.Fatalf("prog3 failed in subsequent execution: err=%v, txAborted=%v", err, txAborted)
	}

	// Commit transaction
	if err := coord.CommitSessionTx(sessID); err != nil {
		t.Fatalf("failed to commit T-SQL transaction: %v", err)
	}

	// Verify both rows (1 and 2) were committed successfully
	res, err := coord.ExecuteSQL("SELECT COUNT(*) AS cnt FROM items_prog;")
	if err != nil || !res.Success {
		t.Fatalf("failed to select count: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["cnt"]) != "2" {
		t.Fatalf("expected 2 committed rows, got %v", res.Rows[0]["cnt"])
	}
}

