//go:build cgo && wiredtiger

package engine

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/wiredtiger"
)

// TestP0_30_WiredTiger_ConcurrentAutocommitAndNestedSubquery verifies that on the WiredTiger CGo engine,
// concurrent autocommit queries and nested subqueries on the same ClientSession never collide on the same WT_SESSION*.
func TestP0_30_WiredTiger_ConcurrentAutocommitAndNestedSubquery(t *testing.T) {
	dir := t.TempDir()
	eng := wiredtiger.New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("wiredtiger.Open failed: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	cfg := DefaultConfig()
	cfg.PoolConfig.MaxWorkers = 2
	cfg.PoolConfig.AcquireTimeout = 2 * time.Second
	cfg.PoolConfig.MaxIdleSessions = 4
	cfg.PoolConfig.IdleSessionTTL = 5 * time.Millisecond

	coord := NewCoordinator(cat, eng, cfg)
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE wt_p030 (id INT PRIMARY KEY, val INT);"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	for i := 1; i <= 5; i++ {
		if _, err := coord.ExecuteSQL(fmt.Sprintf("INSERT INTO wt_p030 VALUES (%d, %d);", i, i*10)); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	const workers = 4
	const iterations = 10
	var wg sync.WaitGroup
	errCh := make(chan error, workers*iterations)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sessID := "wt-shared-session"
			for i := 0; i < iterations; i++ {
				q := "SELECT id, val FROM wt_p030 WHERE id IN (SELECT id FROM wt_p030 WHERE val >= 20) ORDER BY id;"
				res, err := coord.ExecuteSessionSQL(sessID, q)
				if err != nil {
					errCh <- fmt.Errorf("wt worker %d iter %d failed: %w", workerID, i, err)
					return
				}
				if len(res.Rows) != 4 {
					errCh <- fmt.Errorf("wt worker %d iter %d expected 4 rows, got %d", workerID, i, len(res.Rows))
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
}
