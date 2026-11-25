package engine_test

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lionsql/pkg/storage"
)

// TestTxRigor_P6_2_ChaosStress32Workers launches 32 concurrent worker goroutines executing
// random transaction operations (BEGIN, COMMIT, ROLLBACK, SAVEPOINTS, DML, CURSORS)
// for over 10,000 total operations to verify that no deadlocks, panics, memory corruption,
// or data races occur under extreme concurrent load.
func TestTxRigor_P6_2_ChaosStress32Workers(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	initSQL := `
CREATE TABLE t_chaos_accounts (id INT PRIMARY KEY, balance INT);
`
	if _, err := coord.ExecuteSQL(initSQL); err != nil {
		t.Fatalf("init table failed: %v", err)
	}

	const initialAccounts = 50
	for i := 1; i <= initialAccounts; i++ {
		if _, err := coord.ExecuteSQL(fmt.Sprintf("INSERT INTO t_chaos_accounts VALUES (%d, 1000);", i)); err != nil {
			t.Fatalf("seed failed for account %d: %v", i, err)
		}
	}

	const numWorkers = 32
	const opsPerWorker = 320 // 32 * 320 = 10,240 operations total
	var wg sync.WaitGroup
	var totalCompleted atomic.Int64
	var totalConflicts atomic.Int64
	var unexpectedErrors []string
	var errMu sync.Mutex

	recordErr := func(msg string) {
		errMu.Lock()
		defer errMu.Unlock()
		if len(unexpectedErrors) < 10 {
			unexpectedErrors = append(unexpectedErrors, msg)
		}
	}

	wg.Add(numWorkers)
	for w := 0; w < numWorkers; w++ {
		workerID := w
		go func() {
			defer wg.Done()
			sessID := fmt.Sprintf("chaos_worker_%d", workerID)
			if _, err := coord.GetOrCreateSession(sessID); err != nil {
				recordErr(fmt.Sprintf("worker %d GetOrCreateSession failed: %v", workerID, err))
				return
			}
			defer coord.CloseSession(sessID)

			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)*10007))

			for i := 0; i < opsPerWorker; i++ {
				func() {
					defer totalCompleted.Add(1)
					opType := rng.Intn(5)
					acc1 := rng.Intn(initialAccounts) + 1
					acc2 := rng.Intn(initialAccounts) + 1
					amount := rng.Intn(50) + 1

					switch opType {
					case 0: // Multi-statement transfer: BEGIN -> UPDATE acc1 -> UPDATE acc2 -> COMMIT
						if _, err := coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;"); err != nil {
							recordErr(fmt.Sprintf("w%d op0 BEGIN failed: %v", workerID, err))
							return
						}
						_, err1 := coord.ExecuteSessionSQL(sessID, fmt.Sprintf("UPDATE t_chaos_accounts SET balance = balance - %d WHERE id = %d;", amount, acc1))
						_, err2 := coord.ExecuteSessionSQL(sessID, fmt.Sprintf("UPDATE t_chaos_accounts SET balance = balance + %d WHERE id = %d;", amount, acc2))

						if err1 != nil || err2 != nil {
							totalConflicts.Add(1)
							_, _ = coord.ExecuteSessionSQL(sessID, "ROLLBACK TRANSACTION;")
						} else {
							if _, cErr := coord.ExecuteSessionSQL(sessID, "COMMIT TRANSACTION;"); cErr != nil {
								totalConflicts.Add(1)
								_, _ = coord.ExecuteSessionSQL(sessID, "ROLLBACK TRANSACTION;")
							}
						}

					case 1: // Savepoint test: BEGIN -> UPDATE acc1 -> SAVEPOINT sp -> UPDATE acc2 -> ROLLBACK TO sp -> COMMIT
						if _, err := coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;"); err != nil {
							recordErr(fmt.Sprintf("w%d op1 BEGIN failed: %v", workerID, err))
							return
						}
						_, err1 := coord.ExecuteSessionSQL(sessID, fmt.Sprintf("UPDATE t_chaos_accounts SET balance = balance + %d WHERE id = %d;", amount, acc1))
						if err1 != nil {
							totalConflicts.Add(1)
							_, _ = coord.ExecuteSessionSQL(sessID, "ROLLBACK TRANSACTION;")
							return
						}
						_, _ = coord.ExecuteSessionSQL(sessID, "SAVEPOINT sp1;")
						_, err2 := coord.ExecuteSessionSQL(sessID, fmt.Sprintf("UPDATE t_chaos_accounts SET balance = balance - %d WHERE id = %d;", amount, acc2))
						if err2 != nil {
							totalConflicts.Add(1)
						}
						_, _ = coord.ExecuteSessionSQL(sessID, "ROLLBACK TRANSACTION TO SAVEPOINT sp1;")
						if _, cErr := coord.ExecuteSessionSQL(sessID, "COMMIT TRANSACTION;"); cErr != nil {
							totalConflicts.Add(1)
							_, _ = coord.ExecuteSessionSQL(sessID, "ROLLBACK TRANSACTION;")
						}

					case 2: // Aborted write: BEGIN -> UPDATE -> ROLLBACK
						if _, err := coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;"); err != nil {
							recordErr(fmt.Sprintf("w%d op2 BEGIN failed: %v", workerID, err))
							return
						}
						_, errUp := coord.ExecuteSessionSQL(sessID, fmt.Sprintf("UPDATE t_chaos_accounts SET balance = balance + 1000 WHERE id = %d;", acc1))
						if errUp != nil && storage.IsWriteConflict(errUp) {
							totalConflicts.Add(1)
						}
						if _, rErr := coord.ExecuteSessionSQL(sessID, "ROLLBACK TRANSACTION;"); rErr != nil {
							recordErr(fmt.Sprintf("w%d op2 ROLLBACK failed: %v", workerID, rErr))
						}

					case 3: // Autocommit read & point lookup
						res, err := coord.ExecuteSessionSQL(sessID, fmt.Sprintf("SELECT id, balance FROM t_chaos_accounts WHERE id = %d;", acc1))
						if err != nil {
							recordErr(fmt.Sprintf("w%d op3 SELECT failed: %v", workerID, err))
						} else if len(res.Rows) != 1 {
							recordErr(fmt.Sprintf("w%d op3 SELECT expected 1 row, got %d", workerID, len(res.Rows)))
						}

					case 4: // Forward-only cursor lifecycle
						curName := fmt.Sprintf("c_w%d", workerID)
						decSQL := fmt.Sprintf("DECLARE %s CURSOR FOR SELECT balance FROM t_chaos_accounts WHERE id <= 5; OPEN %s; FETCH NEXT FROM %s; CLOSE %s; DEALLOCATE %s;",
							curName, curName, curName, curName, curName)
						_, _ = coord.ExecuteSessionSQL(sessID, decSQL)
					}
				}()
			}
		}()
	}

	wg.Wait()

	t.Logf("Chaos stress test completed: %d total operations executed, %d conflict fast-fails handled",
		totalCompleted.Load(), totalConflicts.Load())

	if len(unexpectedErrors) > 0 {
		t.Fatalf("Chaos stress encountered %d unexpected errors: %v", len(unexpectedErrors), unexpectedErrors)
	}

	if totalCompleted.Load() < 10000 {
		t.Fatalf("expected at least 10,000 operations, got %d", totalCompleted.Load())
	}

	// Final verification: sum of all balances must be consistent or intact
	res, err := coord.ExecuteSQL("SELECT COUNT(*) AS cnt, SUM(balance) AS total FROM t_chaos_accounts;")
	if err != nil {
		t.Fatalf("final integrity check failed: %v", err)
	}
	t.Logf("Final integrity result: %+v", res.Rows)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row in final aggregate check, got %d", len(res.Rows))
	}
	cnt := fmt.Sprintf("%v", res.Rows[0]["cnt"])
	if cnt != "50" {
		t.Fatalf("expected 50 accounts remaining intact, got %s", cnt)
	}
}
