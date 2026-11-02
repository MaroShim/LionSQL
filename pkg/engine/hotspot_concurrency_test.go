package engine

import (
	"fmt"
	"sync"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestHotspotKeyLatchConcurrency(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// 1. Create table
	if _, err := coord.ExecuteSQL("CREATE TABLE inventories (item_id INT PRIMARY KEY, stock INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 2. Insert initial stock of 1000
	if _, err := coord.ExecuteSQL("INSERT INTO inventories (item_id, stock) VALUES (1, 1000);"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 3. Launch 50 concurrent workers, each doing 20 decrements = 1000 total decrements
	const numWorkers = 50
	const numIters = 20

	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers*numIters)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sessID := fmt.Sprintf("worker-session-%d", workerID)
			for i := 0; i < numIters; i++ {
				res, err := coord.ExecuteSessionSQL(sessID, "UPDATE inventories SET stock = stock - 1 WHERE item_id = 1 AND stock >= 1;")
				if err != nil {
					errCh <- fmt.Errorf("worker %d iter %d error: %w", workerID, i, err)
					return
				}
				if res.RowsAffected != 1 {
					errCh <- fmt.Errorf("worker %d iter %d expected 1 row affected, got %d", workerID, i, res.RowsAffected)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent update failed: %v", err)
	}

	// 4. Verify final stock is exactly 0
	res, err := coord.ExecuteSQL("SELECT stock FROM inventories WHERE item_id = 1;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	stockVal := fmt.Sprintf("%v", res.Rows[0]["stock"])
	if stockVal != "0" {
		t.Fatalf("expected final stock 0, got %s", stockVal)
	}
}

func TestHotspotStoredProcedureConcurrency(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// 1. Create table
	if _, err := coord.ExecuteSQL("CREATE TABLE sku_stock (sku_id INT PRIMARY KEY, qty INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 2. Insert initial stock of 1000
	if _, err := coord.ExecuteSQL("INSERT INTO sku_stock (sku_id, qty) VALUES (42, 1000);"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 3. Create stored procedure
	procSQL := `CREATE PROCEDURE sp_ReserveStock
    @p_sku_id INT,
    @p_qty INT
AS
BEGIN
    UPDATE sku_stock
    SET qty = qty - @p_qty
    WHERE sku_id = @p_sku_id AND qty >= @p_qty;
END;`
	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	// 4. Launch 50 workers each reserving 1 unit 20 times = 1000 units
	const numWorkers = 50
	const numIters = 20

	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers*numIters)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sessID := fmt.Sprintf("sp-worker-session-%d", workerID)
			for i := 0; i < numIters; i++ {
				_, err := coord.ExecuteSessionSQL(sessID, "EXEC sp_ReserveStock @p_sku_id = 42, @p_qty = 1;")
				if err != nil {
					errCh <- fmt.Errorf("worker %d iter %d EXEC failed: %w", workerID, i, err)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent procedure execution failed: %v", err)
	}

	// 5. Verify final stock is exactly 0
	res, err := coord.ExecuteSQL("SELECT qty FROM sku_stock WHERE sku_id = 42;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	qtyVal := fmt.Sprintf("%v", res.Rows[0]["qty"])
	if qtyVal != "0" {
		t.Fatalf("expected final qty 0, got %s", qtyVal)
	}
}
