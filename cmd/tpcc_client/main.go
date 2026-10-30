package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"lionsql/pkg/execution"

	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"
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
	hostFlag := flag.String("host", "127.0.0.1", "Target database host")
	portFlag := flag.Int("port", 0, "Target database port (default: 1433 for TDS, 5433 for Postgres)")
	protoFlag := flag.String("protocol", "tds", "Wire protocol: tds (default: SQL Server / MS-TDS) or postgres (PGWire)")
	userFlag := flag.String("user", "sa", "Database user (default: sa for TDS, postgres for PGWire)")
	passFlag := flag.String("password", "1234567890", "Database password")
	dbFlag := flag.String("database", "master", "Database name (default: master for TDS, postgres for PGWire)")

	numWarehouses := flag.Int("warehouses", 16, "Number of warehouses (TPC-C scale factor)")
	numWorkers := flag.Int("workers", 32, "Number of concurrent client workers")
	durationFlag := flag.Duration("duration", 15*time.Second, "Benchmark duration (e.g. 10s, 30s, 1m)")
	numItems := flag.Int("items", 1000, "Number of unique items (default: 1000)")
	numCustPerDist := flag.Int("customers", 300, "Customers per district (default: 300)")
	maxRetries := flag.Int("max-retries", 3, "Maximum transaction retries on conflict")
	setupFlag := flag.Bool("setup", true, "Automatically setup schemas, stored procedures, and initial seed data")
	flag.Parse()
	execution.InitRuntimeTuning(0, 0)

	protocol := strings.ToLower(strings.TrimSpace(*protoFlag))
	if protocol == "" {
		protocol = "tds"
	}
	if protocol == "pg" || protocol == "pgwire" {
		protocol = "postgres"
	}
	if protocol != "tds" && protocol != "postgres" {
		fmt.Printf("Unknown protocol %q. Choose 'tds' or 'postgres'.\n", *protoFlag)
		os.Exit(1)
	}

	targetPort := *portFlag
	if targetPort <= 0 {
		if protocol == "tds" {
			targetPort = 1433
		} else {
			targetPort = 5433
		}
	} else if targetPort == 1433 && protocol == "postgres" {
		targetPort = 5433
	}

	database := *dbFlag
	if database == "" {
		if protocol == "tds" {
			database = "master"
		} else {
			database = "postgres"
		}
	} else if database == "master" && protocol == "postgres" {
		database = "postgres"
	}

	user := *userFlag
	if user == "" {
		if protocol == "tds" {
			user = "sa"
		} else {
			user = "postgres"
		}
	} else if user == "sa" && protocol == "postgres" {
		user = "postgres"
	}

	// 1. Build DSN connection string
	var driverName, dsn string
	if protocol == "tds" {
		driverName = "sqlserver"
		query := url.Values{}
		query.Add("database", database)
		query.Add("encrypt", "disable")
		query.Add("packet size", "32768")
		u := &url.URL{
			Scheme:   "sqlserver",
			User:     url.UserPassword(user, *passFlag),
			Host:     fmt.Sprintf("%s:%d", *hostFlag, targetPort),
			RawQuery: query.Encode(),
		}
		dsn = u.String()
	} else {
		driverName = "postgres"
		dsn = fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
			*hostFlag, targetPort, user, *passFlag, database)
	}

	fmt.Println("================================================================================")
	fmt.Println("  LionSQL TPC-C Network Benchmark Client (tpcc-client)")
	fmt.Println("================================================================================")
	fmt.Printf(" [Target Endpoint] %s:%d (Protocol: %s, DB: %s, User: %s)\n", *hostFlag, targetPort, protocol, database, user)
	fmt.Printf(" [Scale Factor]    %d Warehouses (%d Items, %d Customers/District)\n", *numWarehouses, *numItems, *numCustPerDist)
	fmt.Printf(" [Concurrency]     %d Concurrent Network Client Workers\n", *numWorkers)
	fmt.Printf(" [Duration]        %v\n", *durationFlag)
	fmt.Println("--------------------------------------------------------------------------------")

	// 2. Open Root Connection Pool
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		fmt.Printf("Failed to initialize database driver: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	db.SetMaxOpenConns(*numWorkers + 4)
	db.SetMaxIdleConns(*numWorkers + 4)
	db.SetConnMaxLifetime(10 * time.Minute)

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := db.PingContext(pingCtx); err != nil {
		pingCancel()
		fmt.Printf("Failed to connect to database at %s:%d: %v\n", *hostFlag, *portFlag, err)
		fmt.Println("Please verify LionSQL server is running on the specified port.")
		os.Exit(1)
	}
	pingCancel()
	fmt.Println("Database connection verified successfully.")

	// 3. Schema & Seed Setup (Optional)
	if *setupFlag {
		fmt.Print("Setting up TPC-C tables and stored procedures... ")
		if err := setupTPCCDatabase(db, protocol); err != nil {
			fmt.Printf("\nFailed to setup TPC-C database: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Done.")

		fmt.Print("Seeding initial TPC-C datasets... ")
		seedStart := time.Now()
		if err := seedTPCCData(db, *numWarehouses, *numItems, *numCustPerDist); err != nil {
			fmt.Printf("\nWarning: Data seeding encountered error: %v (continuing if data already exists)\n", err)
		} else {
			fmt.Printf("Done in %v.\n", time.Since(seedStart).Round(time.Millisecond))
		}
	}

	// 4. Run Concurrent Network Benchmark
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
	fmt.Printf("Pre-warming %d worker connections in pool... ", *numWorkers)
	prewarmCtx, prewarmCancel := context.WithTimeout(context.Background(), 5*time.Second)
	var prewarmWg sync.WaitGroup
	for i := 0; i < *numWorkers; i++ {
		prewarmWg.Add(1)
		go func() {
			defer prewarmWg.Done()
			var dummy int
			_ = db.QueryRowContext(prewarmCtx, "SELECT 1;").Scan(&dummy)
		}()
	}
	prewarmWg.Wait()
	prewarmCancel()
	fmt.Println("Ready.")

	fmt.Printf("Executing TPC-C workload across %d concurrent network workers over wire...\n", *numWorkers)
	benchStart := time.Now()

	var wg sync.WaitGroup
	for workerID := 0; workerID < *numWorkers; workerID++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(wid)*1000))

			conn, err := db.Conn(ctx)
			if err != nil {
				return
			}
			defer conn.Close()

			stmts := prepareWorkerStmts(ctx, conn, protocol)
			defer stmts.close()

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
						return runClientNewOrder(ctx, conn, stmts, rng, wID, dID, cID, *numItems)
					})
					statsNewOrder.record(time.Since(start), ok, retries)

				case txType < 88: // 43% Payment
					ok, retries := execWithRetry(*maxRetries, rng, func() bool {
						return runClientPayment(ctx, conn, stmts, rng, wID, dID, cID)
					})
					statsPayment.record(time.Since(start), ok, retries)

				case txType < 92: // 4% Order-Status
					ok, retries := execWithRetry(*maxRetries, rng, func() bool {
						return runClientOrderStatus(ctx, conn, stmts, wID, dID, cID)
					})
					statsOrderStatus.record(time.Since(start), ok, retries)

				case txType < 96: // 4% Delivery
					ok, retries := execWithRetry(*maxRetries, rng, func() bool {
						return runClientDelivery(ctx, conn, stmts, rng, wID)
					})
					statsDelivery.record(time.Since(start), ok, retries)

				default: // 4% Stock-Level
					ok, retries := execWithRetry(*maxRetries, rng, func() bool {
						return runClientStockLevel(ctx, conn, stmts, rng, wID, dID, *numItems)
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

	// 5. Final Report
	printClientReport(totalElapsed, &statsNewOrder, &statsPayment, &statsOrderStatus, &statsDelivery, &statsStockLevel)
}

func setupTPCCDatabase(db *sql.DB, protocol string) error {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS warehouse (
			w_id INT PRIMARY KEY,
			w_name VARCHAR,
			w_tax FLOAT,
			w_ytd FLOAT
		);`,
		`CREATE TABLE IF NOT EXISTS district (
			d_key VARCHAR PRIMARY KEY,
			d_id INT,
			d_w_id INT,
			d_tax FLOAT,
			d_ytd FLOAT,
			d_next_o_id INT
		);`,
		`CREATE TABLE IF NOT EXISTS customer (
			c_key VARCHAR PRIMARY KEY,
			c_id INT,
			c_d_id INT,
			c_w_id INT,
			c_first VARCHAR,
			c_last VARCHAR,
			c_balance FLOAT,
			c_ytd_payment FLOAT,
			c_payment_cnt INT,
			c_discount FLOAT
		);`,
		`CREATE TABLE IF NOT EXISTS item (
			i_id INT PRIMARY KEY,
			i_name VARCHAR,
			i_price FLOAT,
			i_data VARCHAR
		);`,
		`CREATE TABLE IF NOT EXISTS stock (
			s_key VARCHAR PRIMARY KEY,
			s_i_id INT,
			s_w_id INT,
			s_quantity INT,
			s_ytd INT,
			s_order_cnt INT
		);`,
		`CREATE TABLE IF NOT EXISTS orders (
			o_key VARCHAR PRIMARY KEY,
			o_id INT,
			o_d_id INT,
			o_w_id INT,
			o_c_id INT,
			o_carrier_id INT,
			o_ol_cnt INT
		);`,
		`CREATE TABLE IF NOT EXISTS new_order (
			no_key VARCHAR PRIMARY KEY,
			no_o_id INT,
			no_d_id INT,
			no_w_id INT
		);`,
		`CREATE TABLE IF NOT EXISTS order_line (
			ol_key VARCHAR PRIMARY KEY,
			ol_o_id INT,
			ol_d_id INT,
			ol_w_id INT,
			ol_number INT,
			ol_i_id INT,
			ol_amount FLOAT
		);`,
	}

	for _, ddl := range tables {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("ddl failed (%s): %w", ddl, err)
		}
	}

	// Create stored procedures for fast execution
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
		"TPCC_StockLevelBatch": `
CREATE PROCEDURE TPCC_StockLevelBatch
    @d_key VARCHAR,
    @s1 VARCHAR,
    @s2 VARCHAR,
    @s3 VARCHAR,
    @s4 VARCHAR,
    @s5 VARCHAR
AS
BEGIN
    DECLARE @next_o INT;
    DECLARE @qty INT;
    SELECT d_next_o_id INTO @next_o FROM district WHERE d_key = @d_key;
    SELECT s_quantity INTO @qty FROM stock WHERE s_key = @s1;
    SELECT s_quantity INTO @qty FROM stock WHERE s_key = @s2;
    SELECT s_quantity INTO @qty FROM stock WHERE s_key = @s3;
    SELECT s_quantity INTO @qty FROM stock WHERE s_key = @s4;
    SELECT s_quantity INTO @qty FROM stock WHERE s_key = @s5;
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
		"TPCC_DeliveryBatch": `
CREATE PROCEDURE TPCC_DeliveryBatch
    @k1 VARCHAR, @k2 VARCHAR, @k3 VARCHAR, @k4 VARCHAR, @k5 VARCHAR,
    @k6 VARCHAR, @k7 VARCHAR, @k8 VARCHAR, @k9 VARCHAR, @k10 VARCHAR
AS
BEGIN
    DELETE FROM new_order WHERE no_key = @k1;
    DELETE FROM new_order WHERE no_key = @k2;
    DELETE FROM new_order WHERE no_key = @k3;
    DELETE FROM new_order WHERE no_key = @k4;
    DELETE FROM new_order WHERE no_key = @k5;
    DELETE FROM new_order WHERE no_key = @k6;
    DELETE FROM new_order WHERE no_key = @k7;
    DELETE FROM new_order WHERE no_key = @k8;
    DELETE FROM new_order WHERE no_key = @k9;
    DELETE FROM new_order WHERE no_key = @k10;
END;
`,
		"TPCC_NewOrder": `
CREATE PROCEDURE TPCC_NewOrder
    @w_id INT,
    @d_key VARCHAR,
    @c_key VARCHAR,
    @o_key VARCHAR,
    @next_o_id INT,
    @d_id INT,
    @c_id INT,
    @ol_cnt INT,
    @s_key1 VARCHAR, @i_id1 INT, @qty1 INT, @ol_key1 VARCHAR, @amt1 FLOAT,
    @s_key2 VARCHAR, @i_id2 INT, @qty2 INT, @ol_key2 VARCHAR, @amt2 FLOAT,
    @s_key3 VARCHAR, @i_id3 INT, @qty3 INT, @ol_key3 VARCHAR, @amt3 FLOAT,
    @s_key4 VARCHAR, @i_id4 INT, @qty4 INT, @ol_key4 VARCHAR, @amt4 FLOAT,
    @s_key5 VARCHAR, @i_id5 INT, @qty5 INT, @ol_key5 VARCHAR, @amt5 FLOAT,
    @s_key6 VARCHAR, @i_id6 INT, @qty6 INT, @ol_key6 VARCHAR, @amt6 FLOAT,
    @s_key7 VARCHAR, @i_id7 INT, @qty7 INT, @ol_key7 VARCHAR, @amt7 FLOAT,
    @s_key8 VARCHAR, @i_id8 INT, @qty8 INT, @ol_key8 VARCHAR, @amt8 FLOAT,
    @s_key9 VARCHAR, @i_id9 INT, @qty9 INT, @ol_key9 VARCHAR, @amt9 FLOAT,
    @s_key10 VARCHAR, @i_id10 INT, @qty10 INT, @ol_key10 VARCHAR, @amt10 FLOAT
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

    UPDATE stock SET s_quantity = s_quantity - @qty1, s_ytd = s_ytd + @qty1, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key1;
    INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
    VALUES (@ol_key1, @next_o_id, @d_id, @w_id, 1, @i_id1, @amt1);

    UPDATE stock SET s_quantity = s_quantity - @qty2, s_ytd = s_ytd + @qty2, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key2;
    INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
    VALUES (@ol_key2, @next_o_id, @d_id, @w_id, 2, @i_id2, @amt2);

    UPDATE stock SET s_quantity = s_quantity - @qty3, s_ytd = s_ytd + @qty3, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key3;
    INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
    VALUES (@ol_key3, @next_o_id, @d_id, @w_id, 3, @i_id3, @amt3);

    UPDATE stock SET s_quantity = s_quantity - @qty4, s_ytd = s_ytd + @qty4, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key4;
    INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
    VALUES (@ol_key4, @next_o_id, @d_id, @w_id, 4, @i_id4, @amt4);

    UPDATE stock SET s_quantity = s_quantity - @qty5, s_ytd = s_ytd + @qty5, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key5;
    INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
    VALUES (@ol_key5, @next_o_id, @d_id, @w_id, 5, @i_id5, @amt5);

    IF @ol_cnt >= 6
    BEGIN
        UPDATE stock SET s_quantity = s_quantity - @qty6, s_ytd = s_ytd + @qty6, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key6;
        INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
        VALUES (@ol_key6, @next_o_id, @d_id, @w_id, 6, @i_id6, @amt6);
    END;

    IF @ol_cnt >= 7
    BEGIN
        UPDATE stock SET s_quantity = s_quantity - @qty7, s_ytd = s_ytd + @qty7, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key7;
        INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
        VALUES (@ol_key7, @next_o_id, @d_id, @w_id, 7, @i_id7, @amt7);
    END;

    IF @ol_cnt >= 8
    BEGIN
        UPDATE stock SET s_quantity = s_quantity - @qty8, s_ytd = s_ytd + @qty8, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key8;
        INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
        VALUES (@ol_key8, @next_o_id, @d_id, @w_id, 8, @i_id8, @amt8);
    END;

    IF @ol_cnt >= 9
    BEGIN
        UPDATE stock SET s_quantity = s_quantity - @qty9, s_ytd = s_ytd + @qty9, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key9;
        INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
        VALUES (@ol_key9, @next_o_id, @d_id, @w_id, 9, @i_id9, @amt9);
    END;

    IF @ol_cnt >= 10
    BEGIN
        UPDATE stock SET s_quantity = s_quantity - @qty10, s_ytd = s_ytd + @qty10, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key10;
        INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
        VALUES (@ol_key10, @next_o_id, @d_id, @w_id, 10, @i_id10, @amt10);
    END;
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

	for _, procBody := range procs {
		_, _ = db.Exec(procBody)
	}

	return nil
}

func seedTPCCData(db *sql.DB, numWarehouses, numItems, numCustPerDist int) error {
	// Check if already seeded
	var itemCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM item;").Scan(&itemCount)
	if itemCount >= numItems {
		return nil
	}

	// 1. Items in chunks of 50
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for i := 1; i <= numItems; i += 50 {
		end := i + 50
		if end > numItems+1 {
			end = numItems + 1
		}
		var sb strings.Builder
		sb.WriteString("INSERT INTO item (i_id, i_name, i_price, i_data) VALUES ")
		for j := i; j < end; j++ {
			if j > i {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("(%d, 'item_%d', 10.50, 'data_%d')", j, j, j))
		}
		sb.WriteByte(';')
		if _, err := tx.Exec(sb.String()); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// 2. Warehouses & Districts & Customers & Stocks (committed per warehouse)
	for w := 1; w <= numWarehouses; w++ {
		wTx, err := db.Begin()
		if err != nil {
			return err
		}
		defer wTx.Rollback()

		if _, err := wTx.Exec(fmt.Sprintf("INSERT INTO warehouse (w_id, w_name, w_tax, w_ytd) VALUES (%d, 'wh_%d', 0.0825, 300000.0);", w, w)); err != nil {
			return err
		}

		for d := 1; d <= 10; d++ {
			dKey := fmt.Sprintf("%d_%d", w, d)
			if _, err := wTx.Exec(fmt.Sprintf("INSERT INTO district (d_key, d_id, d_w_id, d_tax, d_ytd, d_next_o_id) VALUES ('%s', %d, %d, 0.05, 30000.0, 3001);",
				dKey, d, w)); err != nil {
				return err
			}

			// Batch customers in chunks of 50
			for c := 1; c <= numCustPerDist; c += 50 {
				end := c + 50
				if end > numCustPerDist+1 {
					end = numCustPerDist + 1
				}
				var sb strings.Builder
				sb.WriteString("INSERT INTO customer (c_key, c_id, c_d_id, c_w_id, c_first, c_last, c_balance, c_ytd_payment, c_payment_cnt, c_discount) VALUES ")
				for k := c; k < end; k++ {
					if k > c {
						sb.WriteString(", ")
					}
					cKey := fmt.Sprintf("%d_%d_%d", w, d, k)
					sb.WriteString(fmt.Sprintf("('%s', %d, %d, %d, 'First_%d', 'Last_%d', -10.0, 10.0, 1, 0.05)",
						cKey, k, d, w, k, k))
				}
				sb.WriteByte(';')
				if _, err := wTx.Exec(sb.String()); err != nil {
					return err
				}
			}
		}

		// Batch stocks in chunks of 50
		for i := 1; i <= numItems; i += 50 {
			end := i + 50
			if end > numItems+1 {
				end = numItems + 1
			}
			var sb strings.Builder
			sb.WriteString("INSERT INTO stock (s_key, s_i_id, s_w_id, s_quantity, s_ytd, s_order_cnt) VALUES ")
			for j := i; j < end; j++ {
				if j > i {
					sb.WriteString(", ")
				}
				sKey := fmt.Sprintf("%d_%d", w, j)
				sb.WriteString(fmt.Sprintf("('%s', %d, %d, 50, 0, 0)", sKey, j, w))
			}
			sb.WriteByte(';')
			if _, err := wTx.Exec(sb.String()); err != nil {
				return err
			}
		}

		if err := wTx.Commit(); err != nil {
			return err
		}
	}

	return nil
}

type workerStmts struct {
	stmtPayment     *sql.Stmt
	stmtOrderStatus *sql.Stmt
	stmtStockLevel  *sql.Stmt
	stmtDelivery    *sql.Stmt
	stmtNewOrder    *sql.Stmt
}

func (ws *workerStmts) close() {
	if ws.stmtPayment != nil {
		ws.stmtPayment.Close()
	}
	if ws.stmtOrderStatus != nil {
		ws.stmtOrderStatus.Close()
	}
	if ws.stmtStockLevel != nil {
		ws.stmtStockLevel.Close()
	}
	if ws.stmtDelivery != nil {
		ws.stmtDelivery.Close()
	}
	if ws.stmtNewOrder != nil {
		ws.stmtNewOrder.Close()
	}
}

func placeholder(protocol string, idx int) string {
	if protocol == "postgres" {
		return fmt.Sprintf("$%d", idx)
	}
	return "?"
}

func prepareWorkerStmts(ctx context.Context, conn *sql.Conn, protocol string) *workerStmts {
	ws := &workerStmts{}

	paySQL := fmt.Sprintf("EXEC TPCC_Payment @w_id=%s, @d_key=%s, @c_key=%s, @amount=%s;",
		placeholder(protocol, 1), placeholder(protocol, 2), placeholder(protocol, 3), placeholder(protocol, 4))
	ws.stmtPayment, _ = conn.PrepareContext(ctx, paySQL)

	osSQL := fmt.Sprintf("EXEC TPCC_OrderStatus @c_key=%s;", placeholder(protocol, 1))
	ws.stmtOrderStatus, _ = conn.PrepareContext(ctx, osSQL)

	slSQL := fmt.Sprintf("EXEC TPCC_StockLevelBatch @d_key=%s, @s1=%s, @s2=%s, @s3=%s, @s4=%s, @s5=%s;",
		placeholder(protocol, 1), placeholder(protocol, 2), placeholder(protocol, 3), placeholder(protocol, 4), placeholder(protocol, 5), placeholder(protocol, 6))
	ws.stmtStockLevel, _ = conn.PrepareContext(ctx, slSQL)

	var delArgs []string
	for i := 1; i <= 10; i++ {
		delArgs = append(delArgs, fmt.Sprintf("@k%d=%s", i, placeholder(protocol, i)))
	}
	delSQL := fmt.Sprintf("EXEC TPCC_DeliveryBatch %s;", strings.Join(delArgs, ", "))
	ws.stmtDelivery, _ = conn.PrepareContext(ctx, delSQL)

	noParams := []string{
		"@w_id", "@d_key", "@c_key", "@o_key", "@next_o_id", "@d_id", "@c_id", "@ol_cnt",
	}
	for i := 1; i <= 10; i++ {
		noParams = append(noParams,
			fmt.Sprintf("@s_key%d", i),
			fmt.Sprintf("@i_id%d", i),
			fmt.Sprintf("@qty%d", i),
			fmt.Sprintf("@ol_key%d", i),
			fmt.Sprintf("@amt%d", i),
		)
	}
	var noArgs []string
	for i, p := range noParams {
		noArgs = append(noArgs, fmt.Sprintf("%s=%s", p, placeholder(protocol, i+1)))
	}
	noSQL := fmt.Sprintf("EXEC TPCC_NewOrder %s;", strings.Join(noArgs, ", "))
	ws.stmtNewOrder, _ = conn.PrepareContext(ctx, noSQL)

	return ws
}

func runClientNewOrder(ctx context.Context, conn *sql.Conn, stmts *workerStmts, rng *rand.Rand, wID, dID, cID, totalItems int) bool {
	dKey := fmt.Sprintf("%d_%d", wID, dID)
	cKey := fmt.Sprintf("%d_%d_%d", wID, dID, cID)
	nextOID := int64(1000 + rng.Intn(1000000))
	oKey := fmt.Sprintf("%d_%d_%d", wID, dID, nextOID)
	olCnt := 5 + rng.Intn(6) // 5 to 10 order lines

	if stmts != nil && stmts.stmtNewOrder != nil {
		args := make([]any, 8+10*5)
		args[0] = wID
		args[1] = dKey
		args[2] = cKey
		args[3] = oKey
		args[4] = nextOID
		args[5] = dID
		args[6] = cID
		args[7] = olCnt

		for ol := 1; ol <= 10; ol++ {
			base := 8 + (ol-1)*5
			if ol <= olCnt {
				iID := rng.Intn(totalItems) + 1
				orderQty := rng.Intn(10) + 1
				olAmount := float64(orderQty) * 10.50
				sKey := fmt.Sprintf("%d_%d", wID, iID)
				olKey := fmt.Sprintf("%d_%d_%d_%d", wID, dID, nextOID, ol)

				args[base] = sKey
				args[base+1] = iID
				args[base+2] = orderQty
				args[base+3] = olKey
				args[base+4] = olAmount
			} else {
				args[base] = ""
				args[base+1] = 0
				args[base+2] = 0
				args[base+3] = ""
				args[base+4] = 0.0
			}
		}

		_, err := stmts.stmtNewOrder.ExecContext(ctx, args...)
		return err == nil
	}

	var sb strings.Builder
	sb.WriteString("BEGIN TRANSACTION;\n")
	sb.WriteString(fmt.Sprintf("EXEC TPCC_NewOrderHeader @w_id=%d, @d_key='%s', @c_key='%s', @o_key='%s', @next_o_id=%d, @d_id=%d, @c_id=%d, @ol_cnt=%d;\n",
		wID, dKey, cKey, oKey, nextOID, dID, cID, olCnt))

	for ol := 1; ol <= olCnt; ol++ {
		iID := rng.Intn(totalItems) + 1
		orderQty := rng.Intn(10) + 1
		olAmount := float64(orderQty) * 10.50
		sKey := fmt.Sprintf("%d_%d", wID, iID)
		olKey := fmt.Sprintf("%d_%d_%d_%d", wID, dID, nextOID, ol)

		sb.WriteString(fmt.Sprintf("EXEC TPCC_NewOrderLine @s_key='%s', @i_id=%d, @order_qty=%d, @ol_key='%s', @next_o_id=%d, @d_id=%d, @w_id=%d, @ol=%d, @amount=%f;\n",
			sKey, iID, orderQty, olKey, nextOID, dID, wID, ol, olAmount))
	}
	sb.WriteString("COMMIT TRANSACTION;")

	_, err := conn.ExecContext(ctx, sb.String())
	return err == nil
}

func runClientPayment(ctx context.Context, conn *sql.Conn, stmts *workerStmts, rng *rand.Rand, wID, dID, cID int) bool {
	dKey := fmt.Sprintf("%d_%d", wID, dID)
	cKey := fmt.Sprintf("%d_%d_%d", wID, dID, cID)
	amount := float64(rng.Intn(50000)+100) / 100.0 // $1.00 to $500.00

	if stmts != nil && stmts.stmtPayment != nil {
		_, err := stmts.stmtPayment.ExecContext(ctx, wID, dKey, cKey, amount)
		return err == nil
	}

	query := fmt.Sprintf("EXEC TPCC_Payment @w_id=%d, @d_key='%s', @c_key='%s', @amount=%f;",
		wID, dKey, cKey, amount)
	_, err := conn.ExecContext(ctx, query)
	return err == nil
}

func runClientOrderStatus(ctx context.Context, conn *sql.Conn, stmts *workerStmts, wID, dID, cID int) bool {
	cKey := fmt.Sprintf("%d_%d_%d", wID, dID, cID)
	if stmts != nil && stmts.stmtOrderStatus != nil {
		_, err := stmts.stmtOrderStatus.ExecContext(ctx, cKey)
		return err == nil
	}
	query := fmt.Sprintf("EXEC TPCC_OrderStatus @c_key='%s';", cKey)
	_, err := conn.ExecContext(ctx, query)
	return err == nil
}

func runClientDelivery(ctx context.Context, conn *sql.Conn, stmts *workerStmts, rng *rand.Rand, wID int) bool {
	oID := int64(1000 + rng.Intn(1000000))

	if stmts != nil && stmts.stmtDelivery != nil {
		args := make([]any, 10)
		for d := 1; d <= 10; d++ {
			args[d-1] = fmt.Sprintf("%d_%d_%d", wID, d, oID)
		}
		_, err := stmts.stmtDelivery.ExecContext(ctx, args...)
		return err == nil
	}

	var sb strings.Builder
	sb.WriteString("BEGIN TRANSACTION;\n")
	for d := 1; d <= 10; d++ {
		noKey := fmt.Sprintf("%d_%d_%d", wID, d, oID)
		sb.WriteString(fmt.Sprintf("EXEC TPCC_Delivery @no_key='%s';\n", noKey))
	}
	sb.WriteString("COMMIT TRANSACTION;")

	_, err := conn.ExecContext(ctx, sb.String())
	return err == nil
}

func runClientStockLevel(ctx context.Context, conn *sql.Conn, stmts *workerStmts, rng *rand.Rand, wID, dID, totalItems int) bool {
	dKey := fmt.Sprintf("%d_%d", wID, dID)

	if stmts != nil && stmts.stmtStockLevel != nil {
		args := make([]any, 6)
		args[0] = dKey
		for k := 0; k < 5; k++ {
			iID := rng.Intn(totalItems) + 1
			args[k+1] = fmt.Sprintf("%d_%d", wID, iID)
		}
		_, err := stmts.stmtStockLevel.ExecContext(ctx, args...)
		return err == nil
	}

	var sb strings.Builder
	for k := 0; k < 5; k++ {
		iID := rng.Intn(totalItems) + 1
		sKey := fmt.Sprintf("%d_%d", wID, iID)
		sb.WriteString(fmt.Sprintf("EXEC TPCC_StockLevel @d_key='%s', @s_key='%s';\n", dKey, sKey))
	}
	_, err := conn.ExecContext(ctx, sb.String())
	return err == nil
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

func calcPercentile(durs []time.Duration, pct float64) time.Duration {
	if len(durs) == 0 {
		return 0
	}
	idx := int(math.Round(float64(len(durs)-1) * pct))
	if idx >= len(durs) {
		idx = len(durs) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return durs[idx]
}

func printClientReport(elapsed time.Duration, no, pay, osStat, del, st *txStats) {
	totalCommitted := no.success.Load() + pay.success.Load() + osStat.success.Load() + del.success.Load() + st.success.Load()
	totalAttempts := no.count.Load() + pay.count.Load() + osStat.count.Load() + del.count.Load() + st.count.Load()
	totalAborts := no.abort.Load() + pay.abort.Load() + osStat.abort.Load() + del.abort.Load() + st.abort.Load()
	totalRetries := no.retries.Load() + pay.retries.Load() + osStat.retries.Load() + del.retries.Load() + st.retries.Load()

	elapsedSec := elapsed.Seconds()
	overallTPS := float64(totalCommitted) / elapsedSec
	tpmC := float64(no.success.Load()) * 60.0 / elapsedSec

	abortRate := 0.0
	if totalAttempts > 0 {
		abortRate = float64(totalAborts) * 100.0 / float64(totalAttempts)
	}

	fmt.Println()
	fmt.Println("================================================================================")
	fmt.Println("  LionSQL TPC-C Network Benchmark Client Results (Over Wire)")
	fmt.Println("================================================================================")
	fmt.Printf(" [Throughput - tpmC]    %12.2f New-Order Transactions/Minute\n", tpmC)
	fmt.Printf(" [Overall TPS]          %12.2f Transactions/Second (All 5 Tx types)\n", overallTPS)
	fmt.Printf(" [Elapsed Time]         %12.2f seconds\n", elapsedSec)
	fmt.Printf(" [Total Committed]      %12d transactions\n", totalCommitted)
	fmt.Printf(" [Total Attempts]       %12d transactions\n", totalAttempts)
	fmt.Printf(" [Serialization Abort]  %12d (%.2f%% abort rate)\n", totalAborts, abortRate)
	fmt.Printf(" [Conflict Retries]     %12d total retries\n", totalRetries)
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println(" Transaction Breakdown:")

	formatTx := func(idx int, name string, pct int, s *txStats) {
		succ := s.success.Load()
		tps := float64(succ) / elapsedSec

		s.mu.Lock()
		sorted := make([]time.Duration, len(s.latencies))
		copy(sorted, s.latencies)
		s.mu.Unlock()

		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		p50 := calcPercentile(sorted, 0.50)
		p95 := calcPercentile(sorted, 0.95)
		p99 := calcPercentile(sorted, 0.99)

		fmt.Printf("   %-22s: %8d committed (%6.1f TPS) | P50: %0.2fms | P95: %0.2fms | P99: %0.2fms\n",
			fmt.Sprintf("%d. %s (%d%%)", idx, name, pct), succ, tps,
			float64(p50.Microseconds())/1000.0,
			float64(p95.Microseconds())/1000.0,
			float64(p99.Microseconds())/1000.0)
	}

	formatTx(1, "New-Order", 45, no)
	formatTx(2, "Payment", 43, pay)
	formatTx(3, "Order-Status", 4, osStat)
	formatTx(4, "Delivery", 4, del)
	formatTx(5, "Stock-Level", 4, st)
	fmt.Println("================================================================================")
}
