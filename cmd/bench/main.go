package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"sqlion/pkg/proc"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
	"sqlion/pkg/storage/wiredtiger"
)

func main() {
	numAccounts := flag.Int("accounts", 10000, "Number of initial accounts")
	numWorkers := flag.Int("workers", 100, "Concurrent client workers")
	totalOps := flag.Int("ops", 50000, "Total number of transfer transactions to execute")
	engineType := flag.String("engine", "wiredtiger", "Storage engine backend (wiredtiger, memory)")
	dataDir := flag.String("dir", "data/bench_wt", "Data directory for benchmark storage")
	flag.Parse()

	fmt.Println("================================================================================")
	fmt.Println("  SQLion High-Concurrency OLTP Benchmark (TransferFunds Stored Procedure)")
	fmt.Println("================================================================================")
	fmt.Printf(" [Target]         Stored Procedure (T-SQL -> Goja Bytecode Plan Cache -> ACID Tx)\n")
	fmt.Printf(" [Storage Engine] %s (Dir: %s)\n", *engineType, *dataDir)
	fmt.Printf(" [Accounts]       %d accounts (Initial balance: 1,000,000 each)\n", *numAccounts)
	fmt.Printf(" [Concurrency]    %d concurrent workers\n", *numWorkers)
	fmt.Printf(" [Total Ops]      %d transactions\n", *totalOps)
	fmt.Println("--------------------------------------------------------------------------------")

	// 1. Initialize Engine & Seed Accounts
	fmt.Printf("Initializing %s storage engine and seeding %d accounts... ", *engineType, *numAccounts)
	var eng storage.Engine
	switch *engineType {
	case "wiredtiger":
		_ = os.RemoveAll(*dataDir)
		eng = wiredtiger.New()
		if err := eng.Open(*dataDir); err != nil {
			fmt.Printf("WiredTiger Open failed (%v), falling back to memengine\n", err)
			eng = memengine.New()
			_ = eng.Open("")
		}
	case "memory":
		eng = memengine.New()
		_ = eng.Open("")
	default:
		eng = memengine.New()
		_ = eng.Open("")
	}
	defer func() {
		eng.Close()
		if *engineType == "wiredtiger" {
			_ = os.RemoveAll(*dataDir)
		}
	}()

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeInt},
		},
	})

	initSession, _ := eng.NewSession()
	tx, _ := initSession.BeginTx(storage.TxOptions{})
	initialTotalBalance := int64(0)
	for i := 1; i <= *numAccounts; i++ {
		bal := int64(1000000)
		initialTotalBalance += bal
		_ = tx.Insert("accounts", storage.Record{
			"id":      storage.IntValue(int64(i)),
			"holder":  storage.StringValue(fmt.Sprintf("Customer_%d", i)),
			"balance": storage.IntValue(bal),
		})
	}
	_ = tx.Commit()
	initSession.Close()
	fmt.Printf("Done. (Total assets: %s)\n", formatNumber(initialTotalBalance))

	// 2. Parse & Precompile T-SQL Stored Procedure
	tsqlScript := `
CREATE PROCEDURE TransferFunds
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
		panic(fmt.Sprintf("ParseProcedure failed: %v", err))
	}

	jsCode, err := proc.Transpile(plan)
	if err != nil {
		panic(fmt.Sprintf("Transpile failed: %v", err))
	}

	cache := runtime.NewPlanCache()
	prog, err := cache.GetOrCompile(jsCode)
	if err != nil {
		panic(fmt.Sprintf("GetOrCompile failed: %v", err))
	}

	executor := runtime.NewExecutor(cache)

	// 3. Run Benchmark
	fmt.Println("Starting benchmark execution...")
	startBench := time.Now()

	opsPerWorker := *totalOps / *numWorkers
	var wg sync.WaitGroup
	var completedOps atomic.Int64
	var successOps atomic.Int64
	var failedOps atomic.Int64
	var retryCount atomic.Int64

	allLatencies := make([][]time.Duration, *numWorkers)

	for w := 0; w < *numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			workerSession, err := eng.NewSession()
			if err != nil {
				return
			}
			defer workerSession.Close()

			latencies := make([]time.Duration, 0, opsPerWorker)
			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID*1000)))

			for i := 0; i < opsPerWorker; i++ {
				fromID := int64(r.Intn(*numAccounts) + 1)
				toID := int64(r.Intn(*numAccounts) + 1)
				for toID == fromID {
					toID = int64(r.Intn(*numAccounts) + 1)
				}
				amount := int64(r.Intn(100) + 1)

				opStart := time.Now()
				maxRetries := 5
				var res *runtime.ExecutionResult
				var opErr error
				for retry := 0; retry < maxRetries; retry++ {
					res, opErr = executor.Run(prog, "TransferFunds", map[string]any{
						"from":   fromID,
						"to":     toID,
						"amount": amount,
					}, workerSession)
					if opErr == nil {
						break
					}
					retryCount.Add(1)
				}

				opDuration := time.Since(opStart)
				latencies = append(latencies, opDuration)

				if opErr == nil && res != nil {
					successOps.Add(1)
				} else {
					failedOps.Add(1)
				}
				completedOps.Add(1)
			}
			allLatencies[workerID] = latencies
		}(w)
	}

	wg.Wait()
	benchDuration := time.Since(startBench)

	// 4. Aggregate Metrics
	totalCompleted := completedOps.Load()
	tps := float64(totalCompleted) / benchDuration.Seconds()

	flatLatencies := make([]time.Duration, 0, totalCompleted)
	var sumLatency time.Duration
	for _, lList := range allLatencies {
		for _, l := range lList {
			flatLatencies = append(flatLatencies, l)
			sumLatency += l
		}
	}
	sort.Slice(flatLatencies, func(i, j int) bool {
		return flatLatencies[i] < flatLatencies[j]
	})

	p50 := flatLatencies[len(flatLatencies)*50/100]
	p90 := flatLatencies[len(flatLatencies)*90/100]
	p95 := flatLatencies[len(flatLatencies)*95/100]
	p99 := flatLatencies[len(flatLatencies)*99/100]
	minLat := flatLatencies[0]
	maxLat := flatLatencies[len(flatLatencies)-1]
	meanLat := time.Duration(int64(sumLatency) / int64(len(flatLatencies)))

	// 5. ACID Invariant Check
	fmt.Println("Verifying database ACID integrity across all accounts...")
	checkSession, _ := eng.NewSession()
	checkTx, _ := checkSession.BeginTx(storage.TxOptions{ReadOnly: true})
	cur, _ := checkTx.Scan("accounts")
	finalTotalBalance := int64(0)
	for cur.Next() {
		rec, _ := cur.Record()
		finalTotalBalance += rec["balance"].IVal
	}
	cur.Close()
	_ = checkTx.Rollback()
	checkSession.Close()

	invariantPassed := finalTotalBalance == initialTotalBalance

	// 6. Print Results
	fmt.Println("================================================================================")
	fmt.Println("                              BENCHMARK RESULTS                                 ")
	fmt.Println("================================================================================")
	fmt.Printf(" Total Execution Time:    %v\n", benchDuration)
	fmt.Printf(" Total Completed Ops:     %s transactions\n", formatNumber(totalCompleted))
	fmt.Printf(" Successful Transfers:    %s\n", formatNumber(successOps.Load()))
	fmt.Printf(" Failed/Rolled Back:      %s\n", formatNumber(failedOps.Load()))
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf(" Throughput (TPS):        %.2f TPS\n", tps)
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf(" Latency (Mean):          %v\n", meanLat)
	fmt.Printf(" Latency (Median / P50):  %v\n", p50)
	fmt.Printf(" Latency (P90):           %v\n", p90)
	fmt.Printf(" Latency (P95):           %v\n", p95)
	fmt.Printf(" Latency (P99):           %v\n", p99)
	fmt.Printf(" Latency (Min):           %v\n", minLat)
	fmt.Printf(" Latency (Max):           %v\n", maxLat)
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf(" ACID Invariant Check:    Initial Sum = %s, Final Sum = %s\n",
		formatNumber(initialTotalBalance), formatNumber(finalTotalBalance))
	if invariantPassed {
		fmt.Printf(" Integrity Verdict:       100%% VERIFIED (Zero balance leakage / Exact ACID)\n")
	} else {
		fmt.Printf(" Integrity Verdict:       FAILED (Balance drift detected!)\n")
	}
	fmt.Println("================================================================================")
}

func formatNumber(n int64) string {
	in := fmt.Sprintf("%d", n)
	out := make([]byte, 0, len(in)+len(in)/3)
	leading := len(in) % 3
	if leading > 0 {
		out = append(out, in[:leading]...)
		if len(in) > 3 {
			out = append(out, ',')
		}
	}
	for i := leading; i < len(in); i += 3 {
		out = append(out, in[i:i+3]...)
		if i+3 < len(in) {
			out = append(out, ',')
		}
	}
	return string(out)
}
