package engine

import (
	"fmt"
	"sync"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func setupTestCoord(t *testing.T) (*Coordinator, func()) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	return coord, func() {
		coord.Close()
	}
}

// TestConcurrent_Update_LostUpdate_Prevention verifies that concurrent single-statement updates
// do not overwrite each other's committed values (Lost Update prevention).
func TestConcurrent_Update_LostUpdate_Prevention(t *testing.T) {
	coord, cleanup := setupTestCoord(t)
	defer cleanup()

	// 1. Create table and seed initial row
	if _, err := coord.ExecuteSQL("CREATE TABLE bank_accounts (id INT PRIMARY KEY, balance INT)"); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO bank_accounts (id, balance) VALUES (1, 0)"); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// 2. Launch 10 concurrent workers incrementing balance by +10
	const numWorkers = 10
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	errCh := make(chan error, numWorkers)

	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()
			sessID := fmt.Sprintf("worker-%d", workerID)
			res, err := coord.ExecuteSessionSQL(sessID, "UPDATE bank_accounts SET balance = balance + 10 WHERE id = 1")
			if err != nil {
				errCh <- fmt.Errorf("worker %d failed: %w", workerID, err)
				return
			}
			if res.RowsAffected != 1 {
				errCh <- fmt.Errorf("worker %d expected 1 row affected, got %d", workerID, res.RowsAffected)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("Concurrent update failed: %v", err)
	}

	// 3. Verify final balance is exactly 10 * 10 = 100
	res, err := coord.ExecuteSQL("SELECT balance FROM bank_accounts WHERE id = 1")
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}

	val, ok := res.Rows[0]["balance"]
	if !ok {
		t.Fatalf("column 'balance' not found in result: %v", res.Rows[0])
	}

	var balance int64
	switch v := val.(type) {
	case int64:
		balance = v
	case int:
		balance = int64(v)
	case storage.Value:
		balance = v.IVal
	default:
		t.Fatalf("unexpected balance type %T: %v", val, val)
	}

	if balance != 100 {
		t.Fatalf("Lost Update detected! Expected balance = 100, got %d", balance)
	}
}

// TestConcurrent_Update_PredicateExclusion verifies that when multiple workers compete
// for a row matching a condition (e.g. status = 'PENDING'), exactly one worker updates the row
// and subsequent/concurrent workers do not double-update.
func TestConcurrent_Update_PredicateExclusion(t *testing.T) {
	coord, cleanup := setupTestCoord(t)
	defer cleanup()

	if _, err := coord.ExecuteSQL("CREATE TABLE tasks (id INT PRIMARY KEY, status VARCHAR(50))"); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO tasks (id, status) VALUES (1, 'PENDING')"); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	const numWorkers = 5
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	rowsAffectedCh := make(chan int64, numWorkers)

	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()
			sessID := fmt.Sprintf("task-worker-%d", workerID)
			res, err := coord.ExecuteSessionSQL(sessID, "UPDATE tasks SET status = 'PROCESSING' WHERE id = 1 AND status = 'PENDING'")
			if err != nil {
				rowsAffectedCh <- 0
				return
			}
			rowsAffectedCh <- res.RowsAffected
		}(i)
	}

	wg.Wait()
	close(rowsAffectedCh)

	totalUpdated := int64(0)
	for cnt := range rowsAffectedCh {
		totalUpdated += cnt
	}

	if totalUpdated != 1 {
		t.Fatalf("expected exactly 1 worker to transition task from PENDING to PROCESSING, but total updated = %d", totalUpdated)
	}
}

// TestExplicitTx_UpdateConflict_FailsFast verifies that concurrent explicit transactions
// updating the same row produce a fast-fail serialization conflict instead of overwriting.
func TestExplicitTx_UpdateConflict_FailsFast(t *testing.T) {
	coord, cleanup := setupTestCoord(t)
	defer cleanup()

	if _, err := coord.ExecuteSQL("CREATE TABLE items (id INT PRIMARY KEY, qty INT)"); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO items (id, qty) VALUES (1, 10)"); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	sess1 := "sess1"
	sess2 := "sess2"

	// sess1 begins tx
	if _, err := coord.ExecuteSessionSQL(sess1, "BEGIN TRANSACTION"); err != nil {
		t.Fatalf("sess1 BeginTx failed: %v", err)
	}
	// sess2 begins tx concurrently
	if _, err := coord.ExecuteSessionSQL(sess2, "BEGIN TRANSACTION"); err != nil {
		t.Fatalf("sess2 BeginTx failed: %v", err)
	}

	// Both sessions read row 1 concurrently
	if _, err := coord.ExecuteSessionSQL(sess1, "SELECT qty FROM items WHERE id = 1"); err != nil {
		t.Fatalf("sess1 Select failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess2, "SELECT qty FROM items WHERE id = 1"); err != nil {
		t.Fatalf("sess2 Select failed: %v", err)
	}

	// sess1 updates row 1 and commits
	if _, err := coord.ExecuteSessionSQL(sess1, "UPDATE items SET qty = 20 WHERE id = 1"); err != nil {
		t.Fatalf("sess1 Update failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "COMMIT"); err != nil {
		t.Fatalf("sess1 Commit failed: %v", err)
	}

	// sess2 now attempts to update row 1: must encounter serialization failure or write conflict
	_, err := coord.ExecuteSessionSQL(sess2, "UPDATE items SET qty = 30 WHERE id = 1")
	commitRes, commitErr := coord.ExecuteSessionSQL(sess2, "COMMIT")
	_ = commitRes

	conflictOccurred := (err != nil && storage.IsWriteConflict(err)) || (commitErr != nil && storage.IsWriteConflict(commitErr))
	if !conflictOccurred {
		t.Fatalf("expected write conflict or serialization failure on concurrent explicit transaction update, got err=%v, commitErr=%v", err, commitErr)
	}
}
