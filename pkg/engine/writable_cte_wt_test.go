//go:build cgo && wiredtiger

package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/wiredtiger"
)

func TestWritableCTE_WiredTiger_50ConcurrentWorkers(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_cte_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := wiredtiger.New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	setupSQL := `
	CREATE TABLE inventories (
		item_id VARCHAR(64) PRIMARY KEY,
		warehouse_id VARCHAR(64),
		quantity INT,
		reserved_quantity INT,
		safety_stock_threshold INT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		created_by VARCHAR(64),
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_by VARCHAR(64)
	);

	CREATE TABLE inventory_transactions (
		id VARCHAR(64) PRIMARY KEY,
		item_id VARCHAR(64),
		warehouse_id VARCHAR(64),
		transaction_type VARCHAR(64),
		quantity INT,
		balance_after INT,
		reference_id VARCHAR(64),
		created_by VARCHAR(64),
		updated_by VARCHAR(64)
	);
	`
	if _, err := coord.ExecuteSQL(setupSQL); err != nil {
		t.Fatalf("schema setup failed: %v", err)
	}

	const workerCount = 50
	for i := 0; i < workerCount; i++ {
		seedSQL := fmt.Sprintf(`
			INSERT INTO inventories (item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_by, updated_by)
			VALUES ('sku-%d', 'wh-1', 1000, 0, 10, 'system', 'system');
		`, i)
		if _, err := coord.ExecuteSQL(seedSQL); err != nil {
			t.Fatalf("seed sku-%d failed: %v", i, err)
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, workerCount)

	startSync := make(chan struct{})
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sessID := fmt.Sprintf("wt-worker-sess-%d", workerID)
			cs, err := coord.GetOrCreateSession(sessID)
			if err != nil {
				errCh <- fmt.Errorf("worker %d session error: %w", workerID, err)
				return
			}
			defer coord.CloseSession(sessID)

			<-startSync

			reserveSQL := fmt.Sprintf(`
			WITH upd_inv AS (
				UPDATE inventories
				SET reserved_quantity = reserved_quantity + 5,
				    updated_by = 'worker-%d',
				    updated_at = CURRENT_TIMESTAMP
				WHERE item_id = 'sku-%d'
				  AND (quantity - reserved_quantity) >= 5
				RETURNING item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_at, created_by, updated_at, updated_by
			), ins_tx AS (
				INSERT INTO inventory_transactions (
					id, item_id, warehouse_id, transaction_type, quantity, balance_after, reference_id, created_by, updated_by
				)
				SELECT 'tx-%d', item_id, 'wh-1', 'RESERVE', 5, (quantity - reserved_quantity), 'user-%d', 'user-%d', 'user-%d'
				FROM upd_inv
			)
			SELECT item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_at, created_by, updated_at, updated_by
			FROM upd_inv;
			`, workerID, workerID, workerID, workerID, workerID, workerID)

			res, err := coord.ExecuteSessionSQL(cs.ID, reserveSQL)
			if err != nil {
				errCh <- fmt.Errorf("worker %d query error: %w", workerID, err)
				return
			}
			if len(res.Rows) != 1 {
				errCh <- fmt.Errorf("worker %d expected 1 row, got %d", workerID, len(res.Rows))
				return
			}
			if fmt.Sprintf("%v", res.Rows[0]["reserved_quantity"]) != "5" {
				errCh <- fmt.Errorf("worker %d expected reserved_quantity=5, got %v", workerID, res.Rows[0]["reserved_quantity"])
				return
			}
		}(i)
	}

	close(startSync)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent worker failed on WiredTiger: %v", err)
	}

	// Verify all 50 items and transactions
	for i := 0; i < workerCount; i++ {
		chkInv, err := coord.ExecuteSQL(fmt.Sprintf("SELECT reserved_quantity FROM inventories WHERE item_id = 'sku-%d';", i))
		if err != nil || len(chkInv.Rows) != 1 || fmt.Sprintf("%v", chkInv.Rows[0]["reserved_quantity"]) != "5" {
			t.Fatalf("sku-%d verification failed on WiredTiger: %v, rows: %v", i, err, chkInv)
		}
		chkTx, err := coord.ExecuteSQL(fmt.Sprintf("SELECT quantity FROM inventory_transactions WHERE id = 'tx-%d';", i))
		if err != nil || len(chkTx.Rows) != 1 || fmt.Sprintf("%v", chkTx.Rows[0]["quantity"]) != "5" {
			t.Fatalf("tx-%d verification failed on WiredTiger: %v, rows: %v", i, err, chkTx)
		}
	}
}
