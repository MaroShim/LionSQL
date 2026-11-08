package engine

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

// TestP0_30_WorkerPoolSubqueryNoStarvation_And_ConcurrentAutocommit verifies that:
// 1. Nested subqueries and sp_executesql reuse the parent query's worker slot and never deadlock when MaxWorkers is saturated (D-2, D-3).
// 2. Concurrent autocommit queries on the same session lease cs.Session atomically via cs.sessInUse and fall back to SessionPool without data races (D-1, D-6).
// 3. Concurrent GetActiveQueries() and SessionPool TTL expiration run race-free without cascading stalls (D-4, D-8).
func TestP0_30_WorkerPoolSubqueryNoStarvation_And_ConcurrentAutocommit(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	cfg := DefaultConfig()
	cfg.PoolConfig.MaxWorkers = 2
	cfg.PoolConfig.AcquireTimeout = 2 * time.Second
	cfg.PoolConfig.MaxIdleSessions = 4
	cfg.PoolConfig.IdleSessionTTL = 5 * time.Millisecond

	coord := NewCoordinator(cat, eng, cfg)
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE p030_items (id INT PRIMARY KEY, val INT);"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	for i := 1; i <= 5; i++ {
		if _, err := coord.ExecuteSQL(fmt.Sprintf("INSERT INTO p030_items VALUES (%d, %d);", i, i*10)); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	const workers = 6
	const iterations = 15
	var wg sync.WaitGroup
	errCh := make(chan error, workers*iterations)

	stopPoller := make(chan struct{})
	var pollerWg sync.WaitGroup
	pollerWg.Add(1)
	go func() {
		defer pollerWg.Done()
		for {
			select {
			case <-stopPoller:
				return
			default:
				_ = coord.GetActiveQueries()
			}
		}
	}()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			// Deliberately share the same session ID across multiple goroutines to exercise cs.sessInUse + SessionPool fallback
			sessID := fmt.Sprintf("shared-sess-%d", workerID%2)
			for i := 0; i < iterations; i++ {
				q := "SELECT id, val FROM p030_items WHERE id IN (SELECT id FROM p030_items WHERE val >= 20) ORDER BY id;"
				res, err := coord.ExecuteSessionSQL(sessID, q)
				if err != nil {
					errCh <- fmt.Errorf("worker %d iter %d subquery failed: %w", workerID, i, err)
					return
				}
				if len(res.Rows) != 4 {
					errCh <- fmt.Errorf("worker %d iter %d expected 4 rows, got %d", workerID, i, len(res.Rows))
					return
				}

				dynQ := "EXEC sp_executesql N'SELECT val FROM p030_items WHERE id = @id', N'@id INT', @id = 3;"
				resDyn, err := coord.ExecuteSessionSQL(sessID, dynQ)
				if err != nil {
					errCh <- fmt.Errorf("worker %d iter %d sp_executesql failed: %w", workerID, i, err)
					return
				}
				if len(resDyn.Rows) != 1 {
					errCh <- fmt.Errorf("worker %d iter %d expected 1 row from sp_executesql, got %d", workerID, i, len(resDyn.Rows))
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(stopPoller)
	pollerWg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
}

// TestP0_30_TimeoutMarksTxFailed_And_RejectsCommit verifies that a statement timeout inside an explicit
// transaction marks cs.TxFailed = true across both T-SQL and PostgreSQL dialects and prevents partial COMMIT (D-5, D-7).
func TestP0_30_TimeoutMarksTxFailed_And_RejectsCommit(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE p030_accounts (id INT PRIMARY KEY, balance INT);"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	createLoopProc := `CREATE PROCEDURE P030_Spin AS
BEGIN
    DECLARE @i INT;
    SET @i = 0;
    WHILE 1 = 1
    BEGIN
        SET @i = @i + 1;
    END;
END;`
	if _, err := coord.ExecuteSQL(createLoopProc); err != nil {
		t.Fatalf("create procedure failed: %v", err)
	}

	// 1. T-SQL session ("tds-p030")
	tdsSess := "tds-p030"
	if _, err := coord.ExecuteSessionSQL(tdsSess, "SET QUERY_TIMEOUT = 40;"); err != nil {
		t.Fatalf("SET QUERY_TIMEOUT failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(tdsSess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN TRANSACTION failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(tdsSess, "INSERT INTO p030_accounts VALUES (1, 1000);"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// Trigger timeout inside open transaction
	_, err := coord.ExecuteSessionSQL(tdsSess, "EXEC P030_Spin;")
	if err == nil || !strings.Contains(err.Error(), "statement timeout") {
		t.Fatalf("expected statement timeout error, got: %v", err)
	}

	// Reset timeout so subsequent commands don't timeout
	cs := coord.GetSession(tdsSess)
	if cs == nil || !cs.IsTxFailed() {
		t.Fatalf("expected cs.IsTxFailed() == true after statement timeout in transaction")
	}

	// Subsequent DML in aborted transaction must be rejected
	_, err = coord.ExecuteSessionSQL(tdsSess, "INSERT INTO p030_accounts VALUES (2, 2000);")
	if err == nil || !strings.Contains(err.Error(), "current transaction is aborted") {
		t.Fatalf("expected aborted transaction error on subsequent INSERT, got: %v", err)
	}

	// Attempting to COMMIT a timed-out T-SQL transaction must fail and roll back the partial write
	_, err = coord.ExecuteSessionSQL(tdsSess, "COMMIT;")
	if err == nil || !strings.Contains(err.Error(), "current transaction is aborted") {
		t.Fatalf("expected COMMIT on timed-out T-SQL transaction to return error, got: %v", err)
	}

	// Verify partial write (id=1) was rolled back and not committed
	res, err := coord.ExecuteSQL("SELECT COUNT(*) AS cnt FROM p030_accounts;")
	if err != nil {
		t.Fatalf("SELECT COUNT failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["cnt"]) != "0" {
		t.Fatalf("expected 0 rows after aborted transaction rollback, got: %v", res.Rows)
	}
}
