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

	"lionsql/pkg/execution"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/storage/wiredtiger"
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
	dataDir := flag.String("dir", "data/tpcc_kernel_wt", "Data directory for WiredTiger backend")
	keepDir := flag.Bool("keep-dir", false, "Preserve benchmark data directory after completion")
	numItems := flag.Int("items", 1000, "Number of unique items (default 1000 for fast setup, full spec: 100000)")
	numCustPerDist := flag.Int("customers", 300, "Customers per district (default 300 for fast setup, full spec: 3000)")
	maxRetries := flag.Int("max-retries", 3, "Maximum transaction retries with exponential backoff on serialization conflict")
	flag.Parse()
	execution.InitRuntimeTuning(0, 0)

	fmt.Println("================================================================================")
	fmt.Println("  LionSQL TPC-C Benchmark Suite (Storage Engine Kernel - tpcc-kernel)")
	fmt.Println("================================================================================")
	fmt.Printf(" [Storage Engine] %s (Dir: %s)\n", *engineType, *dataDir)
	fmt.Printf(" [Scale Factor]   %d Warehouses (%d Items, %d Customers/District)\n", *numWarehouses, *numItems, *numCustPerDist)
	fmt.Printf(" [Concurrency]    %d Client Workers\n", *numWorkers)
	fmt.Printf(" [Duration]       %v\n", *durationFlag)
	fmt.Println("--------------------------------------------------------------------------------")

	// 1. Initialize Storage Engine
	var eng storage.Engine
	switch *engineType {
	case "wiredtiger":
		subDir, cleanup, err := storage.PrepareBenchmarkDir(*dataDir, "tpcc_kernel_wt", *keepDir)
		if err != nil {
			fmt.Printf("Failed to prepare WiredTiger benchmark directory: %v\n", err)
			os.Exit(1)
		}
		defer cleanup()
		eng = wiredtiger.New()
		if err := eng.Open(subDir); err != nil {
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
	}()

	// 2. Create TPC-C Tables
	fmt.Print("Creating 9 TPC-C relational tables... ")
	createTPCCTables(eng)
	fmt.Println("Done.")

	// 3. Seed TPC-C Data
	fmt.Printf("Seeding TPC-C initial database (%d warehouses)... ", *numWarehouses)
	seedStart := time.Now()
	seedTPCCData(eng, *numWarehouses, *numItems, *numCustPerDist)
	fmt.Printf("Done in %v.\n", time.Since(seedStart).Round(time.Millisecond))

	// 4. Set up Benchmark Workers & Metrics
	var statsNewOrder txStats
	var statsPayment txStats
	var statsOrderStatus txStats
	var statsDelivery txStats
	var statsStockLevel txStats

	ctx, cancel := context.WithTimeout(context.Background(), *durationFlag)
	defer cancel()

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stopChan
		fmt.Println("\nReceived interrupt signal. Gracefully finishing current transactions...")
		cancel()
	}()

	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("Starting benchmark workload execution (Mix: 45% New-Order, 43% Payment, 4% Order-Status, 4% Delivery, 4% Stock-Level)...")
	fmt.Println("--------------------------------------------------------------------------------")

	benchStart := time.Now()
	var wg sync.WaitGroup

	for w := 0; w < *numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sess, err := eng.NewSession()
			if err != nil {
				return
			}
			defer sess.Close()

			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID*7919)))

			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Standard TPC-C Transaction Mix selection
				roll := rng.Intn(100)
				wID := rng.Intn(*numWarehouses) + 1
				dID := rng.Intn(10) + 1
				cID := rng.Intn(*numCustPerDist) + 1

				t0 := time.Now()
				switch {
				case roll < 45:
					// New-Order Transaction (45%)
					ok, r := execWithRetry(*maxRetries, rng, func() bool {
						return execNewOrder(sess, rng, wID, dID, cID, *numItems)
					})
					statsNewOrder.record(time.Since(t0), ok, r)
				case roll < 88:
					// Payment Transaction (43%)
					ok, r := execWithRetry(*maxRetries, rng, func() bool {
						return execPayment(sess, rng, wID, dID, cID)
					})
					statsPayment.record(time.Since(t0), ok, r)
				case roll < 92:
					// Order-Status Transaction (4%)
					ok, r := execWithRetry(*maxRetries, rng, func() bool {
						return execOrderStatus(sess, wID, dID, cID)
					})
					statsOrderStatus.record(time.Since(t0), ok, r)
				case roll < 96:
					// Delivery Transaction (4%)
					ok, r := execWithRetry(*maxRetries, rng, func() bool {
						return execDelivery(sess, wID)
					})
					statsDelivery.record(time.Since(t0), ok, r)
				default:
					// Stock-Level Transaction (4%)
					ok, r := execWithRetry(*maxRetries, rng, func() bool {
						return execStockLevel(sess, rng, wID, dID, *numItems)
					})
					statsStockLevel.record(time.Since(t0), ok, r)
				}
			}
		}(w)
	}

	// Live progress ticker
	ticker := time.NewTicker(1 * time.Second)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case t := <-ticker.C:
				elapsedSec := t.Sub(benchStart).Seconds()
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

	// 5. Final Report
	printTPCCReport(totalElapsed, &statsNewOrder, &statsPayment, &statsOrderStatus, &statsDelivery, &statsStockLevel)
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
			"w_name": storage.StringValue(fmt.Sprintf("WH_%d", w)),
			"w_tax":  storage.FloatValue(0.08),
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
				"s_quantity":   storage.IntValue(100),
				"s_ytd":        storage.IntValue(0),
				"s_order_cnt":  storage.IntValue(0),
				"s_remote_cnt": storage.IntValue(0),
			})
		}
		_ = tx.Commit()
	}
}

// execNewOrder executes the TPC-C New-Order transaction.
func execNewOrder(sess storage.Session, rng *rand.Rand, wID, dID, cID, totalItems int) bool {
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		return false
	}
	defer tx.Rollback()

	// 1. Get warehouse tax
	_, err = tx.GetRow("warehouse", storage.IntValue(int64(wID)))
	if err != nil {
		return false
	}

	// 2. Get and increment district next order ID
	dKey := fmt.Sprintf("%d_%d", wID, dID)
	distRow, err := tx.GetRow("district", storage.StringValue(dKey))
	if err != nil {
		return false
	}
	nextOID := distRow.Values[5].IVal
	distRow.Values[5] = storage.IntValue(nextOID + 1)
	if err := tx.UpdateRow("district", storage.StringValue(dKey), distRow); err != nil {
		return false
	}

	// 3. Get customer discount
	cKey := fmt.Sprintf("%d_%d_%d", wID, dID, cID)
	custRow, err := tx.GetRow("customer", storage.StringValue(cKey))
	if err != nil {
		return false
	}
	discount := custRow.Values[9].FVal

	// 4. Insert orders and new_order
	oKey := fmt.Sprintf("%d_%d_%d", wID, dID, nextOID)
	olCnt := int64(rng.Intn(6) + 5) // 5 to 10 lines
	if err := tx.InsertRow("orders", storage.Row{Values: []storage.Value{
		storage.StringValue(oKey),
		storage.IntValue(nextOID),
		storage.IntValue(int64(dID)),
		storage.IntValue(int64(wID)),
		storage.IntValue(int64(cID)),
		storage.IntValue(0),
		storage.IntValue(olCnt),
	}}); err != nil {
		return false
	}

	if err := tx.InsertRow("new_order", storage.Row{Values: []storage.Value{
		storage.StringValue(oKey),
		storage.IntValue(nextOID),
		storage.IntValue(int64(dID)),
		storage.IntValue(int64(wID)),
	}}); err != nil {
		return false
	}

	// 5. Update stock and insert order lines
	for ol := 1; ol <= int(olCnt); ol++ {
		iID := rng.Intn(totalItems) + 1
		sKey := fmt.Sprintf("%d_%d", wID, iID)

		itemRow, err := tx.GetRow("item", storage.IntValue(int64(iID)))
		if err != nil {
			return false
		}
		itemPrice := itemRow.Values[2].FVal

		stockRow, err := tx.GetRow("stock", storage.StringValue(sKey))
		if err != nil {
			return false
		}
		qty := stockRow.Values[3].IVal
		orderQty := int64(rng.Intn(5) + 1)
		if qty >= orderQty+10 {
			qty -= orderQty
		} else {
			qty = qty - orderQty + 91
		}
		stockRow.Values[3] = storage.IntValue(qty)
		stockRow.Values[4] = storage.IntValue(stockRow.Values[4].IVal + orderQty)
		stockRow.Values[5] = storage.IntValue(stockRow.Values[5].IVal + 1)
		if err := tx.UpdateRow("stock", storage.StringValue(sKey), stockRow); err != nil {
			return false
		}

		lineAmount := float64(orderQty) * itemPrice * (1.0 - discount)
		olKey := fmt.Sprintf("%d_%d_%d_%d", wID, dID, nextOID, ol)
		if err := tx.InsertRow("order_line", storage.Row{Values: []storage.Value{
			storage.StringValue(olKey),
			storage.IntValue(nextOID),
			storage.IntValue(int64(dID)),
			storage.IntValue(int64(wID)),
			storage.IntValue(int64(ol)),
			storage.IntValue(int64(iID)),
			storage.FloatValue(lineAmount),
		}}); err != nil {
			return false
		}
	}

	return tx.Commit() == nil
}

// execPayment executes the TPC-C Payment transaction.
func execPayment(sess storage.Session, rng *rand.Rand, wID, dID, cID int) bool {
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		return false
	}
	defer tx.Rollback()

	amount := float64(rng.Intn(50000)+100) / 100.0 // $1.00 to $500.00

	// 1. Update warehouse YTD
	wRow, err := tx.GetRow("warehouse", storage.IntValue(int64(wID)))
	if err != nil {
		return false
	}
	wRow.Values[3] = storage.FloatValue(wRow.Values[3].FVal + amount)
	if err := tx.UpdateRow("warehouse", storage.IntValue(int64(wID)), wRow); err != nil {
		return false
	}

	// 2. Update district YTD
	dKey := fmt.Sprintf("%d_%d", wID, dID)
	dRow, err := tx.GetRow("district", storage.StringValue(dKey))
	if err != nil {
		return false
	}
	dRow.Values[4] = storage.FloatValue(dRow.Values[4].FVal + amount)
	if err := tx.UpdateRow("district", storage.StringValue(dKey), dRow); err != nil {
		return false
	}

	// 3. Update customer balance
	cKey := fmt.Sprintf("%d_%d_%d", wID, dID, cID)
	cRow, err := tx.GetRow("customer", storage.StringValue(cKey))
	if err != nil {
		return false
	}
	cRow.Values[6] = storage.FloatValue(cRow.Values[6].FVal - amount)
	cRow.Values[7] = storage.FloatValue(cRow.Values[7].FVal + amount)
	cRow.Values[8] = storage.IntValue(cRow.Values[8].IVal + 1)
	if err := tx.UpdateRow("customer", storage.StringValue(cKey), cRow); err != nil {
		return false
	}

	// 4. Insert history record
	hID := fmt.Sprintf("h_%d_%d_%d_%d", wID, dID, cID, time.Now().UnixNano())
	if err := tx.InsertRow("history", storage.Row{Values: []storage.Value{
		storage.StringValue(hID),
		storage.IntValue(int64(cID)),
		storage.IntValue(int64(dID)),
		storage.IntValue(int64(wID)),
		storage.IntValue(int64(dID)),
		storage.IntValue(int64(wID)),
		storage.FloatValue(amount),
	}}); err != nil {
		return false
	}

	return tx.Commit() == nil
}

// execOrderStatus executes the TPC-C Order-Status read transaction.
func execOrderStatus(sess storage.Session, wID, dID, cID int) bool {
	tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return false
	}
	defer tx.Rollback()

	cKey := fmt.Sprintf("%d_%d_%d", wID, dID, cID)
	if _, err := tx.GetRow("customer", storage.StringValue(cKey)); err != nil {
		return false
	}

	return tx.Commit() == nil
}

// execDelivery executes the TPC-C Delivery transaction.
func execDelivery(sess storage.Session, wID int) bool {
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		return false
	}
	defer tx.Rollback()

	// Process oldest new_order across districts
	for d := 1; d <= 10; d++ {
		noKey := fmt.Sprintf("%d_%d_1001", wID, d)
		if _, err := tx.GetRow("new_order", storage.StringValue(noKey)); err == nil {
			_ = tx.Delete("new_order", storage.StringValue(noKey))
		}
	}

	return tx.Commit() == nil
}

// execStockLevel executes the TPC-C Stock-Level read transaction.
func execStockLevel(sess storage.Session, rng *rand.Rand, wID, dID, totalItems int) bool {
	tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return false
	}
	defer tx.Rollback()

	dKey := fmt.Sprintf("%d_%d", wID, dID)
	if _, err := tx.GetRow("district", storage.StringValue(dKey)); err != nil {
		return false
	}

	// Sample recent item stock levels
	for k := 0; k < 5; k++ {
		iID := rng.Intn(totalItems) + 1
		sKey := fmt.Sprintf("%d_%d", wID, iID)
		_, _ = tx.GetRow("stock", storage.StringValue(sKey))
	}

	return tx.Commit() == nil
}

// execWithRetry attempts to execute a transaction up to maxRetries times upon serialization/lock conflict.
func execWithRetry(maxRetries int, rng *rand.Rand, fn func() bool) (bool, int) {
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if fn() {
			return true, attempt
		}
		if attempt < maxRetries {
			// Exponential backoff with random jitter to resolve write-write lock contention
			jitter := time.Duration(100+rng.Intn(200)) * time.Microsecond
			backoff := jitter * (1 << attempt)
			time.Sleep(backoff)
		}
	}
	return false, maxRetries
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

	totalCompleted := noSuccess + paySuccess + osSuccess + delSuccess + slSuccess
	totalAttempts := no.count.Load() + pay.count.Load() + osStat.count.Load() + del.count.Load() + sl.count.Load()
	totalAborts := no.abort.Load() + pay.abort.Load() + osStat.abort.Load() + del.abort.Load() + sl.abort.Load()
	totalRetries := no.retries.Load() + pay.retries.Load() + osStat.retries.Load() + del.retries.Load() + sl.retries.Load()

	tpmC := float64(noSuccess) * 60.0 / elapsedSec
	totalTPS := float64(totalCompleted) / elapsedSec

	fmt.Println("\n================================================================================")
	fmt.Println("  LionSQL TPC-C Benchmark Executive Summary")
	fmt.Println("================================================================================")
	fmt.Printf(" Execution Time:     %v\n", elapsed.Round(time.Millisecond))
	fmt.Printf(" Total Transactions: %d completed (%d attempted, %d retried, %d final aborts)\n", totalCompleted, totalAttempts, totalRetries, totalAborts)
	fmt.Printf(" Official Metric:    tpmC = %.2f (New-Order Transactions Per Minute)\n", tpmC)
	fmt.Printf(" Overall Throughput: TPS  = %.2f (Total Transactions Per Second)\n", totalTPS)
	fmt.Println("-----------------------------------------------------------------------------------------")
	fmt.Printf(" %-15s | %10s | %10s | %8s | %8s | %10s | %10s\n", "Transaction", "Attempted", "Committed", "Retries", "Aborts", "Avg Latency", "p95 Latency")
	fmt.Println("-----------------------------------------------------------------------------------------")

	printTxLine("New-Order", no)
	printTxLine("Payment", pay)
	printTxLine("Order-Status", osStat)
	printTxLine("Delivery", del)
	printTxLine("Stock-Level", sl)
	fmt.Println("=========================================================================================")
}

func printTxLine(name string, s *txStats) {
	s.mu.Lock()
	lats := make([]time.Duration, len(s.latencies))
	copy(lats, s.latencies)
	s.mu.Unlock()

	avgStr := "N/A"
	p95Str := "N/A"

	if len(lats) > 0 {
		sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
		total := time.Duration(0)
		for _, d := range lats {
			total += d
		}
		avg := total / time.Duration(len(lats))
		p95 := lats[int(float64(len(lats))*0.95)]
		avgStr = fmt.Sprintf("%.2fms", float64(avg.Microseconds())/1000.0)
		p95Str = fmt.Sprintf("%.2fms", float64(p95.Microseconds())/1000.0)
	}

	fmt.Printf(" %-15s | %10d | %10d | %8d | %8d | %10s | %10s\n",
		name, s.count.Load(), s.success.Load(), s.retries.Load(), s.abort.Load(), avgStr, p95Str)
}
