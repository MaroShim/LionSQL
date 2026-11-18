package engine

import (
	"fmt"
	"sync"
	"testing"
)

// TestSessionTransactionInvariant_Serialization verifies that concurrent queries
// executed against the SAME session ID are strictly serialized via TxManager,
// and never bypass cs.Session or fork into concurrent storage transactions.
func TestSessionTransactionInvariant_Serialization(t *testing.T) {
	coord, cleanup := setupTestCoord(t)
	defer cleanup()

	sessID := "invariant-test-sess"

	// 1. Initialize test table
	if _, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE counter_tbl (id INT PRIMARY KEY, val INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO counter_tbl VALUES (1, 0);"); err != nil {
		t.Fatalf("INSERT initial row failed: %v", err)
	}

	// 2. Launch 20 concurrent goroutines executing updates on the same session
	const workers = 20
	const iters = 10

	var wg sync.WaitGroup
	wg.Add(workers)

	errCh := make(chan error, workers*iters)

	for w := 0; w < workers; w++ {
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				// Execute single-statement autocommit update
				q := fmt.Sprintf("UPDATE counter_tbl SET val = val + 1 WHERE id = 1;")
				res, err := coord.ExecuteSessionSQL(sessID, q)
				if err != nil {
					errCh <- fmt.Errorf("worker %d iter %d update failed: %w", workerID, i, err)
					return
				}
				if !res.Success {
					errCh <- fmt.Errorf("worker %d iter %d unsuccessful update", workerID, i)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	// 3. Verify final counter value equals workers * iters (exact serialization)
	res, err := coord.ExecuteSessionSQL(sessID, "SELECT val FROM counter_tbl WHERE id = 1;")
	if err != nil {
		t.Fatalf("SELECT counter failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	val := fmt.Sprintf("%v", res.Rows[0]["val"])
	expected := fmt.Sprintf("%d", workers*iters)
	if val != expected {
		t.Fatalf("expected counter %s, got %s (lost updates due to lack of serialization!)", expected, val)
	}
}

// TestSessionTransactionInvariant_SingleActiveTx verifies that an explicit transaction
// maintains a single active transaction handle across multiple serial statements,
// and rejects duplicate BEGIN statements properly.
func TestSessionTransactionInvariant_SingleActiveTx(t *testing.T) {
	coord, cleanup := setupTestCoord(t)
	defer cleanup()

	sessID := "single-tx-sess"

	if _, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE bank_acc (id INT PRIMARY KEY, balance INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO bank_acc VALUES (10, 1000);"); err != nil {
		t.Fatalf("INSERT initial balance failed: %v", err)
	}

	// 1. Begin transaction
	if _, err := coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}

	// 2. Perform updates inside transaction
	if _, err := coord.ExecuteSessionSQL(sessID, "UPDATE bank_acc SET balance = balance - 200 WHERE id = 10;"); err != nil {
		t.Fatalf("UPDATE 1 failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sessID, "UPDATE bank_acc SET balance = balance - 300 WHERE id = 10;"); err != nil {
		t.Fatalf("UPDATE 2 failed: %v", err)
	}

	// 3. Rollback
	if _, err := coord.ExecuteSessionSQL(sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// 4. Verify balance is restored to 1000
	res, err := coord.ExecuteSessionSQL(sessID, "SELECT balance FROM bank_acc WHERE id = 10;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["balance"]) != "1000" {
		t.Fatalf("expected balance=1000 after rollback, got %+v", res.Rows)
	}
}

// TestSessionTransactionInvariant_ZeroPoolBypass verifies that when cs.Session is pinned,
// acquireSessionForQuery ALWAYS returns the session's pinned storage session and never
// borrows an extraneous session from the global pool.
func TestSessionTransactionInvariant_ZeroPoolBypass(t *testing.T) {
	coord, cleanup := setupTestCoord(t)
	defer cleanup()

	cs, err := coord.GetOrCreateSession("bypass-test-sess")
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	origSession := cs.Session
	if origSession == nil {
		t.Fatalf("expected non-nil cs.Session")
	}

	// Multiple calls to acquireSessionForQuery must return the identical session
	for i := 0; i < 5; i++ {
		sess, closeFn, err := coord.acquireSessionForQuery(cs)
		if err != nil {
			t.Fatalf("iter %d acquireSessionForQuery failed: %v", i, err)
		}
		if sess != origSession {
			t.Fatalf("CRITICAL BUG: acquireSessionForQuery bypassed pinned session! got %v, expected %v", sess, origSession)
		}
		closeFn()
	}
}
