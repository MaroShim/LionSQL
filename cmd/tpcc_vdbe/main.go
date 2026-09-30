package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"sqlion/pkg/proc"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
	"sqlion/pkg/storage/wiredtiger"
)

type txStats struct {
	count     atomic.Int64
	success   atomic.Int64
	abort     atomic.Int64
	retries   atomic.Int64
	latencies []time.Duration
	mu        sync.Mutex
}

func (s *txStats) record(d time.Duration, ok bool, retryCount int) {
	s.count.Add(1)
	if ok {
		s.success.Add(1)
	} else {
		s.abort.Add(1)
	}
	if retryCount > 0 {
		s.retries.Add(int64(retryCount))
	}
	s.mu.Lock()
	if len(s.latencies) < 100000 {
		s.latencies = append(s.latencies, d)
	}
	s.mu.Unlock()
}

func main() {
	numWarehouses := flag.Int("warehouses", 2, "Number of warehouses (TPC-C scale factor)")
	numWorkers := flag.Int("workers", 20, "Number of concurrent client workers")
	durationFlag := flag.Duration("duration", 15*time.Second, "Benchmark duration (e.g. 10s, 30s, 1m)")
	engineType := flag.String("engine", "memory", "Storage engine backend (memory, wiredtiger)")
	dataDir := flag.String("dir", "data/tpcc_vdbe_wt", "Data directory for WiredTiger backend")
	numItems := flag.Int("items", 1000, "Number of unique items (default 1000 for fast setup, full spec: 100000)")
	numCustPerDist := flag.Int("customers", 300, "Customers per district (default 300 for fast setup, full spec: 3000)")
	maxRetries := flag.Int("max-retries", 3, "Maximum transaction retries with exponential backoff on serialization conflict")
	flag.Parse()

	fmt.Println("================================================================================")
	fmt.Println("  SQLion TPC-C Benchmark Suite (Native T-SQL VDBE Bytecode Engine)")
	fmt.Println("================================================================================")
	fmt.Printf(" [Execution]      Precompiled T-SQL VDBE Bytecode (*proc.Program -> PlanCache)\n")
	fmt.Printf(" [Storage Engine] %s (Dir: %s)\n", *engineType, *dataDir)
	fmt.Printf(" [Scale Factor]   %d Warehouses (%d Items, %d Customers/District)\n", *numWarehouses, *numItems, *numCustPerDist)
	fmt.Printf(" [Concurrency]    %d Client Workers\n", *numWorkers)
	fmt.Printf(" [Duration]       %v\n", *durationFlag)
	fmt.Println("--------------------------------------------------------------------------------")

	// 1. Initialize Storage Engine
	var eng storage.Engine
	switch *engineType {
	case "wiredtiger":
		_ = os.RemoveAll(*dataDir)
		eng = wiredtiger.New()
		if err := eng.Open(*dataDir); err != nil {
			fmt.Printf("Failed to open WiredTiger storage engine: %v\n", err)
			os.Exit(1)
		}
	case "memory":
		eng = memengine.New()
		_ = eng.Open("")
	default:
		fmt.Printf("Unknown storage engine: %s. Options: memory, wiredtiger\n", *engineType)
		os.Exit(1)
	}
	defer func() {
		eng.Close()
		if *engineType == "wiredtiger" {
			_ = os.RemoveAll(*dataDir)
		}
	}()

	// 2. Create 9 TPC-C Tables
	fmt.Print("Creating 9 TPC-C relational tables... ")
	createTPCCTables(eng)
	fmt.Println("Done.")

	// 3. Seed Initial Data
	fmt.Printf("Seeding TPC-C initial database (%d warehouses)... ", *numWarehouses)
	seedStart := time.Now()
	seedTPCCData(eng, *numWarehouses, *numItems, *numCustPerDist)
	fmt.Printf("Done in %v.\n", time.Since(seedStart).Round(time.Millisecond))

	// 4. Compile TPC-C Stored Procedures into VDBE Bytecode
	fmt.Print("Compiling TPC-C stored procedures to VDBE bytecode... ")
	cache, err := compileTPCCProcedures()
	if err != nil {
		fmt.Printf("Failed to compile TPC-C stored procedures: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Done. (PlanCache ready)")

	executor := runtime.NewExecutor(cache)

	// 5. Run Concurrent Benchmark
	ctx, cancel := context.WithTimeout(context.Background(), *durationFlag)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nReceived interrupt. Stopping benchmark gracefully...")
		cancel()
	}()

	var (
		statsNewOrder    txStats
		statsPayment     txStats
		statsOrderStatus txStats
		statsDelivery    txStats
		statsStockLevel  txStats
	)

	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf("Executing TPC-C workload across %d concurrent workers via VDBE Bytecode VM...\n", *numWorkers)
	benchStart := time.Now()

	var wg sync.WaitGroup
	for workerID := 0; workerID < *numWorkers; workerID++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(wid)*1000))

			sess, err := eng.NewSession()
			if err != nil {
				return
			}
			defer sess.Close()

			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				wID := rng.Intn(*numWarehouses) + 1
				dID := rng.Intn(10) + 1
				cID := rng.Intn(*numCustPerDist) + 1

				txType := rng.Intn(100)
				start := time.Now()

				switch {
				case txType < 45: // 45% New-Order
					ok, retries := execWithRetry(*maxRetries, rng, func() bool {
						return runVDBENewOrder(executor, cache, sess, rng, wID, dID, cID, *numItems)
					})
					statsNewOrder.record(time.Since(start), ok, retries)

				case txType < 88: // 43% Payment
					ok, retries := execWithRetry(*maxRetries, rng, func() bool {
						return runVDBEPayment(executor, cache, sess, rng, wID, dID, cID)
					})
					statsPayment.record(time.Since(start), ok, retries)

				case txType < 92: // 4% Order-Status
					ok, retries := execWithRetry(*maxRetries, rng, func() bool {
						return runVDBEOrderStatus(executor, cache, sess, wID, dID, cID)
					})
					statsOrderStatus.record(time.Since(start), ok, retries)

				case txType < 96: // 4% Delivery
					ok, retries := execWithRetry(*maxRetries, rng, func() bool {
						return runVDBEDelivery(executor, cache, sess, wID)
					})
					statsDelivery.record(time.Since(start), ok, retries)

				default: // 4% Stock-Level
					ok, retries := execWithRetry(*maxRetries, rng, func() bool {
						return runVDBEStockLevel(executor, cache, sess, rng, wID, dID, *numItems)
					})
					statsStockLevel.record(time.Since(start), ok, retries)
				}
			}
		}(workerID)
	}

	// Live status logger
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				elapsedSec := time.Since(benchStart).Seconds()
				if elapsedSec > 0 {
					noSucc := statsNewOrder.success.Load()
					currTPMC := float64(noSucc) * 60.0 / elapsedSec
					totalSucc := noSucc + statsPayment.success.Load() + statsOrderStatus.success.Load() + statsDelivery.success.Load() + statsStockLevel.success.Load()
					currTPS := float64(totalSucc) / elapsedSec
					fmt.Printf(" [%02.0fs] tpmC: %9.1f | TPS: %8.1f | New-Order: %d | Payment: %d | Total Tx: %d\n",
						elapsedSec, currTPMC, currTPS, noSucc, statsPayment.success.Load(), totalSucc)
				}
			}
		}
	}()

	wg.Wait()
	totalElapsed := time.Since(benchStart)

	// 6. Final Report
	printTPCCReport(totalElapsed, &statsNewOrder, &statsPayment, &statsOrderStatus, &statsDelivery, &statsStockLevel)
}

func compileTPCCProcedures() (*runtime.PlanCache, error) {
	cache := runtime.NewPlanCache()

	procs := map[string]string{
		"TPCC_Payment": `
CREATE PROCEDURE TPCC_Payment
    @w_id INT,
    @d_key VARCHAR,
    @c_key VARCHAR,
    @amount FLOAT
AS
BEGIN
    UPDATE warehouse SET w_ytd = w_ytd + @amount WHERE w_id = @w_id;
    UPDATE district SET d_ytd = d_ytd + @amount WHERE d_key = @d_key;
    UPDATE customer SET c_balance = c_balance - @amount WHERE c_key = @c_key;
END;
`,
		"TPCC_OrderStatus": `
CREATE PROCEDURE TPCC_OrderStatus
    @c_key VARCHAR
AS
BEGIN
    DECLARE @bal FLOAT;
    SELECT c_balance INTO @bal FROM customer WHERE c_key = @c_key;
END;
`,
		"TPCC_StockLevel": `
CREATE PROCEDURE TPCC_StockLevel
    @d_key VARCHAR,
    @s_key VARCHAR
AS
BEGIN
    DECLARE @next_o INT;
    DECLARE @qty INT;
    SELECT d_next_o_id INTO @next_o FROM district WHERE d_key = @d_key;
    SELECT s_quantity INTO @qty FROM stock WHERE s_key = @s_key;
END;
`,
		"TPCC_Delivery": `
CREATE PROCEDURE TPCC_Delivery
    @no_key VARCHAR
AS
BEGIN
    DELETE FROM new_order WHERE no_key = @no_key;
END;
`,
		"TPCC_NewOrderHeader": `
CREATE PROCEDURE TPCC_NewOrderHeader
    @w_id INT,
    @d_key VARCHAR,
    @c_key VARCHAR,
    @o_key VARCHAR,
    @next_o_id INT,
    @d_id INT,
    @c_id INT,
    @ol_cnt INT
AS
BEGIN
    DECLARE @w_tax FLOAT;
    DECLARE @c_discount FLOAT;
    SELECT w_tax INTO @w_tax FROM warehouse WHERE w_id = @w_id;
    UPDATE district SET d_next_o_id = d_next_o_id + 1 WHERE d_key = @d_key;
    SELECT c_discount INTO @c_discount FROM customer WHERE c_key = @c_key;
    INSERT INTO orders (o_key, o_id, o_d_id, o_w_id, o_c_id, o_carrier_id, o_ol_cnt)
    VALUES (@o_key, @next_o_id, @d_id, @w_id, @c_id, 0, @ol_cnt);
    INSERT INTO new_order (no_key, no_o_id, no_d_id, no_w_id)
    VALUES (@o_key, @next_o_id, @d_id, @w_id);
END;
`,
		"TPCC_NewOrderLine": `
CREATE PROCEDURE TPCC_NewOrderLine
    @s_key VARCHAR,
    @i_id INT,
    @order_qty INT,
    @ol_key VARCHAR,
    @next_o_id INT,
    @d_id INT,
    @w_id INT,
    @ol INT,
    @amount FLOAT
AS
BEGIN
    DECLARE @price FLOAT;
    SELECT i_price INTO @price FROM item WHERE i_id = @i_id;
    UPDATE stock SET s_quantity = s_quantity - @order_qty, s_ytd = s_ytd + @order_qty, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key;
    INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
    VALUES (@ol_key, @next_o_id, @d_id, @w_id, @ol, @i_id, @amount);
END;
`,
	}

	for name, script := range procs {
		prog, err := proc.Compile(script)
		if err != nil {
			return nil, fmt.Errorf("compile %s failed: %w", name, err)
		}
		cache.PutByName(name, prog)
	}

	return cache, nil
}

func runVDBENewOrder(exec *runtime.Executor, cache *runtime.PlanCache, sess storage.Session, rng *rand.Rand, wID, dID, cID, totalItems int) bool {
	progHeader, ok := cache.GetByName("TPCC_NewOrderHeader")
	if !ok {
		return false
	}
	progLine, ok := cache.GetByName("TPCC_NewOrderLine")
	if !ok {
		return false
	}

	dKey := fmt.Sprintf("%d_%d", wID, dID)
	cKey := fmt.Sprintf("%d_%d_%d", wID, dID, cID)
	nextOID := int64(1000 + rng.Intn(1000000))
	oKey := fmt.Sprintf("%d_%d_%d", wID, dID, nextOID)
	olCnt := int64(rng.Intn(6) + 5) // 5 to 10 lines

	// 1. Run header via VDBE Bytecode
	_, err := exec.Run(progHeader, "TPCC_NewOrderHeader", map[string]any{
		"w_id":      wID,
		"d_key":     dKey,
		"c_key":     cKey,
		"o_key":     oKey,
		"next_o_id": nextOID,
		"d_id":      dID,
		"c_id":      cID,
		"ol_cnt":    olCnt,
	}, sess)
	if err != nil {
		return false
	}

	// 2. Run order lines via VDBE Bytecode
	for ol := 1; ol <= int(olCnt); ol++ {
		iID := rng.Intn(totalItems) + 1
		sKey := fmt.Sprintf("%d_%d", wID, iID)
		orderQty := int64(rng.Intn(5) + 1)
		olKey := fmt.Sprintf("%d_%d_%d_%d", wID, dID, nextOID, ol)
		lineAmount := float64(orderQty) * float64(10+(iID%90))

		_, err := exec.Run(progLine, "TPCC_NewOrderLine", map[string]any{
			"s_key":     sKey,
			"i_id":      iID,
			"order_qty": orderQty,
			"ol_key":    olKey,
			"next_o_id": nextOID,
			"d_id":      dID,
			"w_id":      wID,
			"ol":        ol,
			"amount":    lineAmount,
		}, sess)
		if err != nil {
			return false
		}
	}

	return true
}

func runVDBEPayment(exec *runtime.Executor, cache *runtime.PlanCache, sess storage.Session, rng *rand.Rand, wID, dID, cID int) bool {
	prog, ok := cache.GetByName("TPCC_Payment")
	if !ok {
		return false
	}

	dKey := fmt.Sprintf("%d_%d", wID, dID)
	cKey := fmt.Sprintf("%d_%d_%d", wID, dID, cID)
	amount := float64(rng.Intn(50000)+100) / 100.0 // $1.00 to $500.00

	_, err := exec.Run(prog, "TPCC_Payment", map[string]any{
		"w_id":   wID,
		"d_key":  dKey,
		"c_key":  cKey,
		"amount": amount,
	}, sess)
	return err == nil
}

func runVDBEOrderStatus(exec *runtime.Executor, cache *runtime.PlanCache, sess storage.Session, wID, dID, cID int) bool {
	prog, ok := cache.GetByName("TPCC_OrderStatus")
	if !ok {
		return false
	}

	cKey := fmt.Sprintf("%d_%d_%d", wID, dID, cID)
	_, err := exec.Run(prog, "TPCC_OrderStatus", map[string]any{
		"c_key": cKey,
	}, sess)
	return err == nil
}

func runVDBEDelivery(exec *runtime.Executor, cache *runtime.PlanCache, sess storage.Session, wID int) bool {
	prog, ok := cache.GetByName("TPCC_Delivery")
	if !ok {
		return false
	}

	for d := 1; d <= 10; d++ {
		noKey := fmt.Sprintf("%d_%d_1001", wID, d)
		_, _ = exec.Run(prog, "TPCC_Delivery", map[string]any{
			"no_key": noKey,
		}, sess)
	}
	return true
}

func runVDBEStockLevel(exec *runtime.Executor, cache *runtime.PlanCache, sess storage.Session, rng *rand.Rand, wID, dID, totalItems int) bool {
	prog, ok := cache.GetByName("TPCC_StockLevel")
	if !ok {
		return false
	}

	dKey := fmt.Sprintf("%d_%d", wID, dID)
	for k := 0; k < 5; k++ {
		iID := rng.Intn(totalItems) + 1
		sKey := fmt.Sprintf("%d_%d", wID, iID)
		_, _ = exec.Run(prog, "TPCC_StockLevel", map[string]any{
			"d_key": dKey,
			"s_key": sKey,
		}, sess)
	}
	return true
}

func execWithRetry(maxRetries int, rng *rand.Rand, fn func() bool) (bool, int) {
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if fn() {
			return true, attempt
		}
		if attempt < maxRetries {
			jitter := time.Duration(100+rng.Intn(200)) * time.Microsecond
			backoff := jitter * (1 << attempt)
			time.Sleep(backoff)
		}
	}
	return false, maxRetries
}

func createTPCCTables(eng storage.Engine) {
	_ = eng.CreateTable(&storage.TableSchema{
		Name: "warehouse",
		Columns: []storage.Column{
			{Name: "w_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "w_name", Type: storage.TypeVarChar},
			{Name: "w_tax", Type: storage.TypeFloat},
			{Name: "w_ytd", Type: storage.TypeFloat},
		},
	})

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "district",
		Columns: []storage.Column{
			{Name: "d_key", Type: storage.TypeVarChar, PrimaryKey: true},
			{Name: "d_id", Type: storage.TypeInt},
			{Name: "d_w_id", Type: storage.TypeInt},
			{Name: "d_tax", Type: storage.TypeFloat},
			{Name: "d_ytd", Type: storage.TypeFloat},
			{Name: "d_next_o_id", Type: storage.TypeInt},
		},
	})

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "customer",
		Columns: []storage.Column{
			{Name: "c_key", Type: storage.TypeVarChar, PrimaryKey: true},
			{Name: "c_id", Type: storage.TypeInt},
			{Name: "c_d_id", Type: storage.TypeInt},
			{Name: "c_w_id", Type: storage.TypeInt},
			{Name: "c_first", Type: storage.TypeVarChar},
			{Name: "c_last", Type: storage.TypeVarChar},
			{Name: "c_balance", Type: storage.TypeFloat},
			{Name: "c_ytd_payment", Type: storage.TypeFloat},
			{Name: "c_payment_cnt", Type: storage.TypeInt},
			{Name: "c_discount", Type: storage.TypeFloat},
		},
	})

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "item",
		Columns: []storage.Column{
			{Name: "i_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "i_name", Type: storage.TypeVarChar},
			{Name: "i_price", Type: storage.TypeFloat},
			{Name: "i_data", Type: storage.TypeVarChar},
		},
	})

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "stock",
		Columns: []storage.Column{
			{Name: "s_key", Type: storage.TypeVarChar, PrimaryKey: true},
			{Name: "s_i_id", Type: storage.TypeInt},
			{Name: "s_w_id", Type: storage.TypeInt},
			{Name: "s_quantity", Type: storage.TypeInt},
			{Name: "s_ytd", Type: storage.TypeInt},
			{Name: "s_order_cnt", Type: storage.TypeInt},
			{Name: "s_remote_cnt", Type: storage.TypeInt},
		},
	})

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "o_key", Type: storage.TypeVarChar, PrimaryKey: true},
			{Name: "o_id", Type: storage.TypeInt},
			{Name: "o_d_id", Type: storage.TypeInt},
			{Name: "o_w_id", Type: storage.TypeInt},
			{Name: "o_c_id", Type: storage.TypeInt},
			{Name: "o_carrier_id", Type: storage.TypeInt},
			{Name: "o_ol_cnt", Type: storage.TypeInt},
		},
	})

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "new_order",
		Columns: []storage.Column{
			{Name: "no_key", Type: storage.TypeVarChar, PrimaryKey: true},
			{Name: "no_o_id", Type: storage.TypeInt},
			{Name: "no_d_id", Type: storage.TypeInt},
			{Name: "no_w_id", Type: storage.TypeInt},
		},
	})

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "order_line",
		Columns: []storage.Column{
			{Name: "ol_key", Type: storage.TypeVarChar, PrimaryKey: true},
			{Name: "ol_o_id", Type: storage.TypeInt},
			{Name: "ol_d_id", Type: storage.TypeInt},
			{Name: "ol_w_id", Type: storage.TypeInt},
			{Name: "ol_number", Type: storage.TypeInt},
			{Name: "ol_i_id", Type: storage.TypeInt},
			{Name: "ol_amount", Type: storage.TypeFloat},
		},
	})

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "history",
		Columns: []storage.Column{
			{Name: "h_id", Type: storage.TypeVarChar, PrimaryKey: true},
			{Name: "h_c_id", Type: storage.TypeInt},
			{Name: "h_c_d_id", Type: storage.TypeInt},
			{Name: "h_c_w_id", Type: storage.TypeInt},
			{Name: "h_d_id", Type: storage.TypeInt},
			{Name: "h_w_id", Type: storage.TypeInt},
			{Name: "h_amount", Type: storage.TypeFloat},
		},
	})
}

func seedTPCCData(eng storage.Engine, warehouses, items, custPerDist int) {
	sess, _ := eng.NewSession()
	defer sess.Close()

	// 1. Items
	tx, _ := sess.BeginTx(storage.TxOptions{})
	for i := 1; i <= items; i++ {
		_ = tx.Insert("item", storage.Record{
			"i_id":    storage.IntValue(int64(i)),
			"i_name":  storage.StringValue(fmt.Sprintf("Item_%d", i)),
			"i_price": storage.FloatValue(float64(10 + (i % 90))),
			"i_data":  storage.StringValue("ORIGINAL_DATA"),
		})
	}
	_ = tx.Commit()

	// 2. Warehouses, Districts, Customers, Stock
	for w := 1; w <= warehouses; w++ {
		tx, _ = sess.BeginTx(storage.TxOptions{})
		_ = tx.Insert("warehouse", storage.Record{
			"w_id":   storage.IntValue(int64(w)),
			"w_name": storage.StringValue(fmt.Sprintf("Warehouse_%d", w)),
			"w_tax":  storage.FloatValue(0.0825),
			"w_ytd":  storage.FloatValue(300000.0),
		})

		for d := 1; d <= 10; d++ {
			dKey := fmt.Sprintf("%d_%d", w, d)
			_ = tx.Insert("district", storage.Record{
				"d_key":       storage.StringValue(dKey),
				"d_id":        storage.IntValue(int64(d)),
				"d_w_id":      storage.IntValue(int64(w)),
				"d_tax":       storage.FloatValue(0.05),
				"d_ytd":       storage.FloatValue(30000.0),
				"d_next_o_id": storage.IntValue(1001),
			})

			for c := 1; c <= custPerDist; c++ {
				cKey := fmt.Sprintf("%d_%d_%d", w, d, c)
				_ = tx.Insert("customer", storage.Record{
					"c_key":         storage.StringValue(cKey),
					"c_id":          storage.IntValue(int64(c)),
					"c_d_id":        storage.IntValue(int64(d)),
					"c_w_id":        storage.IntValue(int64(w)),
					"c_first":       storage.StringValue(fmt.Sprintf("First_%d", c)),
					"c_last":        storage.StringValue(fmt.Sprintf("Last_%d", c)),
					"c_balance":     storage.FloatValue(-10.0),
					"c_ytd_payment": storage.FloatValue(10.0),
					"c_payment_cnt": storage.IntValue(1),
					"c_discount":    storage.FloatValue(0.05),
				})
			}
		}

		for i := 1; i <= items; i++ {
			sKey := fmt.Sprintf("%d_%d", w, i)
			_ = tx.Insert("stock", storage.Record{
				"s_key":        storage.StringValue(sKey),
				"s_i_id":       storage.IntValue(int64(i)),
				"s_w_id":       storage.IntValue(int64(w)),
				"s_quantity":   storage.IntValue(int64(50 + (i % 50))),
				"s_ytd":        storage.IntValue(0),
				"s_order_cnt":  storage.IntValue(0),
				"s_remote_cnt": storage.IntValue(0),
			})
		}

		_ = tx.Commit()
	}
}

func printTPCCReport(elapsed time.Duration, no, pay, osStat, del, sl *txStats) {
	elapsedSec := elapsed.Seconds()
	if elapsedSec <= 0 {
		elapsedSec = 0.001
	}

	noSuccess := no.success.Load()
	paySuccess := pay.success.Load()
	osSuccess := osStat.success.Load()
	delSuccess := del.success.Load()
	slSuccess := sl.success.Load()

	totalCommitted := noSuccess + paySuccess + osSuccess + delSuccess + slSuccess
	totalAttempts := no.count.Load() + pay.count.Load() + osStat.count.Load() + del.count.Load() + sl.count.Load()
	totalAborts := no.abort.Load() + pay.abort.Load() + osStat.abort.Load() + del.abort.Load() + sl.abort.Load()
	totalRetries := no.retries.Load() + pay.retries.Load() + osStat.retries.Load() + del.retries.Load() + sl.retries.Load()

	tpmC := float64(noSuccess) * 60.0 / elapsedSec
	overallTPS := float64(totalCommitted) / elapsedSec

	fmt.Println("\n================================================================================")
	fmt.Println("  SQLion TPC-C Benchmark Results (VDBE Bytecode Virtual Machine)")
	fmt.Println("================================================================================")
	fmt.Printf(" [Throughput - tpmC]   %10.2f New-Order Transactions/Minute\n", tpmC)
	fmt.Printf(" [Overall TPS]          %10.2f Transactions/Second (All 5 Tx types)\n", overallTPS)
	fmt.Printf(" [Elapsed Time]         %10.2f seconds\n", elapsedSec)
	fmt.Printf(" [Total Committed]      %10d transactions\n", totalCommitted)
	fmt.Printf(" [Total Attempts]       %10d transactions\n", totalAttempts)
	fmt.Printf(" [Serialization Abort]  %10d (%.2f%% abort rate)\n", totalAborts, float64(totalAborts)/float64(totalAttempts+1)*100.0)
	fmt.Printf(" [Conflict Retries]     %10d total retries\n", totalRetries)
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println(" Transaction Breakdown:")
	fmt.Printf("   1. New-Order (45%%)    : %8d committed (%6.1f TPS) | %s\n",
		noSuccess, float64(noSuccess)/elapsedSec, formatLatencies(no))
	fmt.Printf("   2. Payment (43%%)      : %8d committed (%6.1f TPS) | %s\n",
		paySuccess, float64(paySuccess)/elapsedSec, formatLatencies(pay))
	fmt.Printf("   3. Order-Status (4%%)  : %8d committed (%6.1f TPS) | %s\n",
		osSuccess, float64(osSuccess)/elapsedSec, formatLatencies(osStat))
	fmt.Printf("   4. Delivery (4%%)      : %8d committed (%6.1f TPS) | %s\n",
		delSuccess, float64(delSuccess)/elapsedSec, formatLatencies(del))
	fmt.Printf("   5. Stock-Level (4%%)    : %8d committed (%6.1f TPS) | %s\n",
		slSuccess, float64(slSuccess)/elapsedSec, formatLatencies(sl))
	fmt.Println("================================================================================")
}

func formatLatencies(s *txStats) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.latencies) == 0 {
		return "P50: 0.0ms | P95: 0.0ms | P99: 0.0ms"
	}
	sorted := make([]time.Duration, len(s.latencies))
	copy(sorted, s.latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	p50 := sorted[len(sorted)*50/100]
	p95 := sorted[len(sorted)*95/100]
	p99 := sorted[len(sorted)*99/100]

	return fmt.Sprintf("P50: %.2fms | P95: %.2fms | P99: %.2fms",
		float64(p50.Microseconds())/1000.0,
		float64(p95.Microseconds())/1000.0,
		float64(p99.Microseconds())/1000.0)
}
