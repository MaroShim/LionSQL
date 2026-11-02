package engine

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestIsWriteConflictDetection(t *testing.T) {
	// 1. Direct ErrSerializationFailure
	if !storage.IsWriteConflict(storage.ErrSerializationFailure) {
		t.Errorf("expected ErrSerializationFailure to be detected as write conflict")
	}

	// 2. Wrapped ErrSerializationFailure
	wrapped := fmt.Errorf("transaction commit failed: %w", storage.ErrSerializationFailure)
	if !storage.IsWriteConflict(wrapped) {
		t.Errorf("expected wrapped ErrSerializationFailure to be detected")
	}

	// 3. WiredTiger WT_ROLLBACK (-31800) error text
	wtErr := errors.New("cursor_update failed with code -31800 (WT_ROLLBACK)")
	if !storage.IsWriteConflict(wtErr) {
		t.Errorf("expected WT_ROLLBACK string to be detected")
	}

	// 4. Non-conflict errors
	if storage.IsWriteConflict(nil) {
		t.Errorf("nil error must not be write conflict")
	}
	if storage.IsWriteConflict(storage.ErrTableNotFound) {
		t.Errorf("ErrTableNotFound must not be write conflict")
	}
	if storage.IsWriteConflict(storage.ErrUniqueViolation) {
		t.Errorf("ErrUniqueViolation must not be write conflict")
	}
}

func TestWriteConflictRetryAutocommitDML(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE accounts (id INT PRIMARY KEY, balance INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	if _, err := coord.ExecuteSQL("INSERT INTO accounts (id, balance) VALUES (1, 0);"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	const numWorkers = 30
	const itersPerWorker = 10
	var wg sync.WaitGroup
	var successCount atomic.Int64
	errCh := make(chan error, numWorkers*itersPerWorker)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sessID := fmt.Sprintf("retry-dml-worker-%d", workerID)
			for i := 0; i < itersPerWorker; i++ {
				res, err := coord.ExecuteSessionSQL(sessID, "UPDATE accounts SET balance = balance + 1 WHERE id = 1;")
				if err != nil {
					errCh <- fmt.Errorf("worker %d iter %d: %w", workerID, i, err)
					return
				}
				if res.RowsAffected != 1 {
					errCh <- fmt.Errorf("worker %d iter %d: expected 1 row, got %d", workerID, i, res.RowsAffected)
					return
				}
				successCount.Add(1)
			}
		}(w)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("unexpected autocommit DML error with retry enabled: %v", err)
	}

	expectedTotal := int64(numWorkers * itersPerWorker)
	if successCount.Load() != expectedTotal {
		t.Fatalf("expected %d successful updates, got %d", expectedTotal, successCount.Load())
	}

	res, err := coord.ExecuteSQL("SELECT balance FROM accounts WHERE id = 1;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["balance"]) != fmt.Sprintf("%d", expectedTotal) {
		t.Fatalf("expected balance = %d, got %v", expectedTotal, res.Rows[0]["balance"])
	}
}

func TestWriteConflictRetryStoredProcedure(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE sp_accounts (id INT PRIMARY KEY, balance INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	if _, err := coord.ExecuteSQL("INSERT INTO sp_accounts (id, balance) VALUES (1, 0);"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	spSQL := `
CREATE PROCEDURE sp_AddBalance
    @p_id INT,
    @p_amount INT
AS
BEGIN
    UPDATE sp_accounts SET balance = balance + @p_amount WHERE id = @p_id;
END;
`
	if _, err := coord.ExecuteSQL(spSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	const numWorkers = 25
	const itersPerWorker = 10
	var wg sync.WaitGroup
	var successCount atomic.Int64
	errCh := make(chan error, numWorkers*itersPerWorker)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sessID := fmt.Sprintf("retry-sp-worker-%d", workerID)
			for i := 0; i < itersPerWorker; i++ {
				callSQL := "EXEC sp_AddBalance @p_id = 1, @p_amount = 1;"
				res, err := coord.ExecuteSessionSQL(sessID, callSQL)
				if err != nil {
					errCh <- fmt.Errorf("worker %d iter %d: %w", workerID, i, err)
					return
				}
				if !res.Success {
					errCh <- fmt.Errorf("worker %d iter %d: procedure call indicated failure", workerID, i)
					return
				}
				successCount.Add(1)
			}
		}(w)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("unexpected SP execution error with retry enabled: %v", err)
	}

	expectedTotal := int64(numWorkers * itersPerWorker)
	if successCount.Load() != expectedTotal {
		t.Fatalf("expected %d successful SP calls, got %d", expectedTotal, successCount.Load())
	}

	res, err := coord.ExecuteSQL("SELECT balance FROM sp_accounts WHERE id = 1;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["balance"]) != fmt.Sprintf("%d", expectedTotal) {
		t.Fatalf("expected balance = %d, got %v", expectedTotal, res.Rows[0]["balance"])
	}
}
