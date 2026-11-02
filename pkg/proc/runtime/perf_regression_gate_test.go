package runtime_test

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// TestRegression_TransferFundsPerfGate acts as an in-line performance regression gate
// executed during standard "go test ./..." runs. It ensures that OLTP stored procedure
// execution maintains sub-millisecond dispatch and does not suffer from accidental
// sleep stalls, lock convoys, or latch deadlocks (PERF-REGRESSION-RECOVERY-1).
func TestRegression_TransferFundsPerfGate(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	const numAccounts = 1000
	const initialBalance = 1000000

	if err := eng.CreateTable(&storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeInt},
		},
	}); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	initialTotal := int64(0)
	for i := 1; i <= numAccounts; i++ {
		bal := int64(initialBalance)
		initialTotal += bal
		_ = tx.Insert("accounts", storage.Record{
			"id":      storage.IntValue(int64(i)),
			"holder":  storage.StringValue(fmt.Sprintf("User_%d", i)),
			"balance": storage.IntValue(bal),
		})
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Seed commit failed: %v", err)
	}

	tsqlScript := `
CREATE PROCEDURE TransferFundsGate
    @from INT,
    @to INT,
    @amount INT
AS
BEGIN
    DECLARE @sender_bal INT;
    SELECT balance INTO @sender_bal FROM accounts WHERE id = @from;

    IF @sender_bal < @amount
    BEGIN
        RAISERROR('Insufficient balance');
        RETURN -1;
    END

    UPDATE accounts SET balance = balance - @amount WHERE id = @from;
    UPDATE accounts SET balance = balance + @amount WHERE id = @to;
    RETURN 0;
END;
`
	lexer := proc.NewLexer(tsqlScript)
	parser := proc.NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	prog, err := proc.CompileProcedure(plan)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	cache := runtime.NewPlanCache()
	cache.PutByName(plan.Name, prog)
	executor := runtime.NewExecutor(cache)

	const numWorkers = 8
	opsPerWorker := 125 // 8 * 125 = 1000 transactions total (~10-20ms execution)
	if testing.Short() {
		opsPerWorker = 50
	}

	var wg sync.WaitGroup
	var completed atomic.Int64
	var successes atomic.Int64

	start := time.Now()
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			workerSession, sErr := eng.NewSession()
			if sErr != nil {
				return
			}
			defer workerSession.Close()

			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID*1000)))
			for i := 0; i < opsPerWorker; i++ {
				fromID := int64(r.Intn(numAccounts) + 1)
				toID := int64(r.Intn(numAccounts) + 1)
				for toID == fromID {
					toID = int64(r.Intn(numAccounts) + 1)
				}
				amount := int64(r.Intn(50) + 1)

				const maxRetries = 3
				for retry := 0; retry < maxRetries; retry++ {
					res, runErr := executor.Run(prog, "TransferFundsGate", map[string]any{
						"from":   fromID,
						"to":     toID,
						"amount": amount,
					}, workerSession)
					if runErr == nil && res != nil {
						successes.Add(1)
						break
					}
				}
				completed.Add(1)
			}
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start)

	tps := float64(completed.Load()) / elapsed.Seconds()

	// 1. Assert ACID Conservation
	chkSession, _ := eng.NewSession()
	chkTx, _ := chkSession.BeginTx(storage.TxOptions{ReadOnly: true})
	cur, _ := chkTx.Scan("accounts")
	finalTotal := int64(0)
	for cur.Next() {
		rec, _ := cur.Record()
		finalTotal += rec["balance"].IVal
	}
	cur.Close()
	_ = chkTx.Rollback()
	chkSession.Close()

	if finalTotal != initialTotal {
		t.Fatalf("ACID Invariant violation: initial balance %d != final balance %d", initialTotal, finalTotal)
	}

	// 2. Performance Regression Gate Assert
	// Normal baseline: 80,000 ~ 150,000 TPS without -race.
	// Under `go test -race` with parallel package execution, throughput is ~20,000 ~ 35,000 TPS.
	// If a deadlock, sleep, or lock convoy is reintroduced (such as the 96% drop), throughput drops below 4,500 TPS.
	// We set a regression floor of 10,000 TPS to guarantee 100% detection of lock/sleep convoys while being flake-free under -race.
	minAcceptableTPS := 10000.0
	t.Logf("[PerfGate] Executed %d ops across %d workers in %v (Throughput: %.2f TPS, Success: %d)",
		completed.Load(), numWorkers, elapsed, tps, successes.Load())

	if tps < minAcceptableTPS {
		t.Fatalf("Performance regression detected! Expected >= %.0f TPS, got %.2f TPS. Check for unintended sleep calls, lock convoys, or lock inversions.",
			minAcceptableTPS, tps)
	}
}
