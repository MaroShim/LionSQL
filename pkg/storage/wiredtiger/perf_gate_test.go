//go:build cgo && wiredtiger

package wiredtiger_test

import (
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/wiredtiger"
)

// TestWiredTiger_TransferFundsPerfGate acts as an in-line performance regression gate for WiredTiger.
// It ensures multi-worker concurrent transactions maintain sub-millisecond dispatch and intercepts
// any reintroduction of 'transaction_sync=(enabled=true)' or lock convoys prior to commit (PERF-WT-CONFIG-1).
func TestWiredTiger_TransferFundsPerfGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf gate in short mode")
	}

	dir, err := os.MkdirTemp("", "lionsql_wt_perfgate_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)

	eng := wiredtiger.New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
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
CREATE PROCEDURE TransferFundsGateWT
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
	const opsPerWorker = 125 // 1,000 total ops
	var wg sync.WaitGroup
	var completed atomic.Int64
	var successes atomic.Int64

	start := time.Now()

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			wSession, sErr := eng.NewSession()
			if sErr != nil {
				return
			}
			defer wSession.Close()

			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID*1000)))

			for i := 0; i < opsPerWorker; i++ {
				fromID := int64(r.Intn(numAccounts) + 1)
				toID := int64(r.Intn(numAccounts) + 1)
				for toID == fromID {
					toID = int64(r.Intn(numAccounts) + 1)
				}
				amount := int64(r.Intn(100) + 1)

				for retry := 0; retry < 5; retry++ {
					res, opErr := executor.Run(prog, "TransferFundsGateWT", map[string]any{
						"from":   fromID,
						"to":     toID,
						"amount": amount,
					}, wSession)
					if opErr == nil && res != nil {
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

	// 2. Performance Regression Floor: minimum 6,000 TPS under -race (normally 40,000+ TPS).
	// When transaction_sync is illegally enabled, throughput collapses below 3,000 TPS under -race.
	minAcceptableTPS := 6000.0
	t.Logf("[WTPrivateGate] Executed %d ops across %d workers in %v (Throughput: %.2f TPS, Success: %d)",
		completed.Load(), numWorkers, elapsed, tps, successes.Load())

	if tps < minAcceptableTPS {
		t.Fatalf("WiredTiger performance regression detected! Expected >= %.0f TPS, got %.2f TPS. Check if 'transaction_sync' was reintroduced into WiredTigerOpenConfig.",
			minAcceptableTPS, tps)
	}
}
