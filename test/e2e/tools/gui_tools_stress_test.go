package tools_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lionsql/test/e2e/harness"
)

// TestE2E_DBeaverAndBeekeeper_DualEngineStress runs concurrent GUI schema inspection
// and data grid queries under background write pressure on MemEngine and WiredTiger.
func TestE2E_DBeaverAndBeekeeper_DualEngineStress(t *testing.T) {
	storageEngines := []harness.StorageType{
		harness.StorageMemEngine,
	}
	if harness.IsWiredTigerAvailable() {
		storageEngines = append(storageEngines, harness.StorageWiredTiger)
	}

	for _, sType := range storageEngines {
		t.Run(string(sType), func(t *testing.T) {
			server := harness.StartDualServer(t, sType)
			defer server.Cleanup()

			pgDB, tdsDB := server.OpenClients(t)
			defer pgDB.Close()
			defer tdsDB.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			// 1. Setup base schema matching DBeaver / Beekeeper introspection expectations
			for _, stmt := range []string{
				"CREATE TABLE customers (customer_id INT PRIMARY KEY, name VARCHAR(100) NOT NULL, email VARCHAR(100), credit_limit DECIMAL(18,2), is_active BOOLEAN, created_at DATETIME);",
				"CREATE TABLE orders (order_id INT PRIMARY KEY, customer_id INT, amount DECIMAL(18,2), status VARCHAR(20));",
				"CREATE INDEX idx_cust_name ON customers (name);",
				"CREATE INDEX idx_ord_cust ON orders (customer_id);",
				"CREATE VIEW v_active_customers AS SELECT customer_id, name, email FROM customers WHERE is_active = true;",
			} {
				if _, err := pgDB.ExecContext(ctx, stmt); err != nil {
					t.Fatalf("[%s] setup DDL failed: %v", sType, err)
				}
			}

			// Seed initial records
			for i := 1; i <= 20; i++ {
				_, err := pgDB.ExecContext(ctx, fmt.Sprintf(
					"INSERT INTO customers VALUES (%d, 'Customer %d', 'cust%d@corp.com', %d.50, true, '2026-01-01 10:00:00');",
					i, i, i, i*100,
				))
				if err != nil {
					t.Fatalf("[%s] seed customer %d failed: %v", sType, i, err)
				}
			}

			// 2. High-Concurrency GUI Tool Introspection & Data Grid Paging Stress
			const numWorkers = 8
			const iterationsPerWorker = 25

			var wg sync.WaitGroup
			var writeCounter atomic.Int64
			writeCounter.Store(100)

			startBarrier := make(chan struct{})
			wg.Add(numWorkers)

			for w := 0; w < numWorkers; w++ {
				go func(workerID int) {
					defer wg.Done()
					<-startBarrier

					wCtx, wCancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer wCancel()

					// Distribute workers across PGWire and TDS protocols
					clientDB := pgDB
					isTDS := (workerID % 2) == 1
					if isTDS {
						clientDB = tdsDB
					}

					for it := 0; it < iterationsPerWorker; it++ {
						action := (workerID + it) % 4
						switch action {
						case 0:
							// DBeaver Introspection Pattern: pg_class & information_schema joins
							var q string
							if isTDS {
								q = "SELECT table_name, table_type FROM information_schema.tables WHERE table_name = 'customers';"
							} else {
								q = "SELECT c.relname, n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind IN ('r', 'v');"
							}
							rows, err := clientDB.QueryContext(wCtx, q)
							if err != nil {
								t.Errorf("[%s/Worker %d] introspection failed: %v", sType, workerID, err)
								return
							}
							cnt := 0
							for rows.Next() {
								cnt++
							}
							rows.Close()
							if cnt == 0 {
								t.Errorf("[%s/Worker %d] introspection returned 0 rows", sType, workerID)
								return
							}

						case 1:
							// Beekeeper Data Grid Pattern: Paged limit/offset query with order by
							q := "SELECT customer_id, name, credit_limit FROM customers ORDER BY customer_id LIMIT 5 OFFSET 0;"
							if isTDS {
								q = "SELECT customer_id, name, credit_limit FROM customers ORDER BY customer_id OFFSET 0 ROWS FETCH NEXT 5 ROWS ONLY;"
							}
							rows, err := clientDB.QueryContext(wCtx, q)
							if err != nil {
								t.Errorf("[%s/Worker %d] grid paging query failed: %v", sType, workerID, err)
								return
							}
							rowCount := 0
							for rows.Next() {
								var id int
								var name string
								var limit any
								if err := rows.Scan(&id, &name, &limit); err == nil {
									rowCount++
								}
							}
							rows.Close()
							if rowCount == 0 {
								t.Errorf("[%s/Worker %d] grid paging returned 0 rows", sType, workerID)
								return
							}

						case 2:
							// Beekeeper Schema Column Introspection: information_schema.columns
							q := "SELECT column_name, data_type FROM information_schema.columns WHERE table_name = 'customers' ORDER BY ordinal_position;"
							rows, err := clientDB.QueryContext(wCtx, q)
							if err != nil {
								t.Errorf("[%s/Worker %d] column introspection failed: %v", sType, workerID, err)
								return
							}
							cCount := 0
							for rows.Next() {
								cCount++
							}
							rows.Close()
							if cCount != 6 {
								t.Errorf("[%s/Worker %d] expected 6 columns, got %d", sType, workerID, cCount)
								return
							}

						case 3:
							// Background Concurrent DML (Insert order / customer edit)
							nextID := writeCounter.Add(1)
							insertQ := fmt.Sprintf(
								"INSERT INTO orders VALUES (%d, %d, %d.00, 'PENDING');",
								nextID, (nextID%20)+1, nextID*10,
							)
							if _, err := clientDB.ExecContext(wCtx, insertQ); err != nil {
								t.Errorf("[%s/Worker %d] concurrent order insert failed: %v", sType, workerID, err)
								return
							}
						}
					}
				}(w)
			}

			close(startBarrier)
			wg.Wait()

			// Final state sanity check
			var totalOrders int
			err := pgDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM orders;").Scan(&totalOrders)
			if err != nil {
				t.Fatalf("[%s] final orders count query failed: %v", sType, err)
			}
			if totalOrders == 0 {
				t.Fatalf("[%s] expected orders inserted during stress, got 0", sType)
			}
		})
	}
}
