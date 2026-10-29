package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/storage/wiredtiger"
)

func main() {
	sf := flag.Float64("sf", 0.001, "SSB Scale Factor (0.001 = ~6K rows, 0.01 = ~60K rows, 0.1 = ~600K rows)")
	engineType := flag.String("engine", "memory", "Storage engine backend: memory or wiredtiger")
	dataDir := flag.String("dir", "data/ssb_wt", "Directory for WiredTiger storage")
	keepDir := flag.Bool("keep-dir", false, "Preserve storage directory after run")
	warmup := flag.Int("warmup", 1, "Warmup iterations")
	iterations := flag.Int("iterations", 3, "Measurement iterations")
	flag.Parse()

	fmt.Println("================================================================================")
	fmt.Println("  LionSQL Star Schema Benchmark (SSB) - Complex Query & Volcano CBO Engine")
	fmt.Println("================================================================================")
	fmt.Printf(" [Storage Engine] %s\n", *engineType)
	fmt.Printf(" [Scale Factor]   %.4f (~%d LineOrder fact rows)\n", *sf, int(6000000**sf))
	fmt.Printf(" [Iterations]     %d warmup, %d measured runs\n", *warmup, *iterations)
	fmt.Println("--------------------------------------------------------------------------------")

	var eng storage.Engine
	switch *engineType {
	case "wiredtiger":
		subDir, cleanup, err := storage.PrepareBenchmarkDir(*dataDir, "ssb_wt", *keepDir)
		if err != nil {
			fmt.Printf("Failed to prepare storage directory: %v\n", err)
			os.Exit(1)
		}
		defer cleanup()
		eng = wiredtiger.New()
		if err := eng.Open(subDir); err != nil {
			fmt.Printf("Failed to open WiredTiger engine: %v\n", err)
			os.Exit(1)
		}
	case "memory":
		eng = memengine.New()
		_ = eng.Open("")
	default:
		fmt.Printf("Unknown engine: %s. Use 'memory' or 'wiredtiger'\n", *engineType)
		os.Exit(1)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	fmt.Print("Creating SSB Star Schema tables (customer, supplier, part, date, lineorder)... ")
	tables := []string{
		`CREATE TABLE customer (
			c_custkey INT PRIMARY KEY,
			c_name VARCHAR(25),
			c_city VARCHAR(10),
			c_nation VARCHAR(15),
			c_region VARCHAR(12)
		);`,
		`CREATE TABLE supplier (
			s_suppkey INT PRIMARY KEY,
			s_name VARCHAR(25),
			s_city VARCHAR(10),
			s_nation VARCHAR(15),
			s_region VARCHAR(12)
		);`,
		`CREATE TABLE part (
			p_partkey INT PRIMARY KEY,
			p_name VARCHAR(22),
			p_mfgr VARCHAR(6),
			p_category VARCHAR(7),
			p_brand1 VARCHAR(9)
		);`,
		`CREATE TABLE date (
			d_datekey INT PRIMARY KEY,
			d_year INT,
			d_yearmonthnum INT
		);`,
		`CREATE TABLE lineorder (
			lo_orderkey INT,
			lo_linenumber INT,
			lo_custkey INT,
			lo_partkey INT,
			lo_suppkey INT,
			lo_orderdate INT,
			lo_quantity INT,
			lo_extendedprice INT,
			lo_discount INT,
			lo_revenue INT,
			lo_supplycost INT,
			PRIMARY KEY (lo_orderkey, lo_linenumber)
		);`,
	}
	for _, ddl := range tables {
		if _, err := coord.ExecuteSQL(ddl); err != nil {
			fmt.Printf("\nDDL Error: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Println("Done.")

	// Seeding
	seedStart := time.Now()
	fmt.Print("Seeding dimension and fact records... ")
	rng := rand.New(rand.NewSource(42))
	regions := []string{"AMERICA", "ASIA", "EUROPE", "AFRICA", "MIDDLE EAST"}
	nations := map[string][]string{
		"AMERICA":     {"UNITED STATES", "CANADA", "BRAZIL"},
		"ASIA":        {"JAPAN", "CHINA", "INDIA"},
		"EUROPE":      {"UNITED KINGDOM", "GERMANY", "FRANCE"},
		"AFRICA":      {"EGYPT", "ALGERIA", "KENYA"},
		"MIDDLE EAST": {"SAUDI ARABIA", "JORDAN", "IRAQ"},
	}

	numCust := int(30000 * *sf)
	if numCust < 20 {
		numCust = 20
	}
	numSupp := int(2000 * *sf)
	if numSupp < 10 {
		numSupp = 10
	}
	numPart := int(20000 * *sf)
	if numPart < 20 {
		numPart = 20
	}
	numOrders := int(6000000 * *sf)
	if numOrders < 1000 {
		numOrders = 1000
	}

	dateKeys := make([]int, 0, 7*365)
	curDate := time.Date(1992, 1, 1, 0, 0, 0, 0, time.UTC)
	for y := 1992; y <= 1998; y++ {
		for d := 1; d <= 365; d++ {
			dKey := curDate.Year()*10000 + int(curDate.Month())*100 + curDate.Day()
			ymNum := curDate.Year()*100 + int(curDate.Month())
			dateKeys = append(dateKeys, dKey)
			_, _ = coord.ExecuteSQL(fmt.Sprintf("INSERT INTO date VALUES (%d, %d, %d);", dKey, curDate.Year(), ymNum))
			curDate = curDate.AddDate(0, 0, 1)
		}
	}

	for i := 1; i <= numCust; i++ {
		reg := regions[rng.Intn(len(regions))]
		natList := nations[reg]
		nat := natList[rng.Intn(len(natList))]
		_, _ = coord.ExecuteSQL(fmt.Sprintf("INSERT INTO customer VALUES (%d, 'Cust_%d', 'City_%d', '%s', '%s');",
			i, i, i%10, nat, reg))
	}

	for i := 1; i <= numSupp; i++ {
		reg := regions[rng.Intn(len(regions))]
		natList := nations[reg]
		nat := natList[rng.Intn(len(natList))]
		_, _ = coord.ExecuteSQL(fmt.Sprintf("INSERT INTO supplier VALUES (%d, 'Supp_%d', 'City_%d', '%s', '%s');",
			i, i, i%10, nat, reg))
	}

	for i := 1; i <= numPart; i++ {
		mfgr := fmt.Sprintf("MFGR#%d", (i%5)+1)
		cat := fmt.Sprintf("MFGR#%d%d", (i%5)+1, (i%5)+1)
		brand := fmt.Sprintf("%s%d", cat, (i%10)+1)
		_, _ = coord.ExecuteSQL(fmt.Sprintf("INSERT INTO part VALUES (%d, 'Part_%d', '%s', '%s', '%s');",
			i, i, mfgr, cat, brand))
	}

	for i := 1; i <= numOrders; i++ {
		cKey := rng.Intn(numCust) + 1
		pKey := rng.Intn(numPart) + 1
		sKey := rng.Intn(numSupp) + 1
		dKey := dateKeys[rng.Intn(len(dateKeys))]
		qty := rng.Intn(50) + 1
		extPrice := qty * (rng.Intn(100) + 10)
		disc := rng.Intn(10) + 1
		revenue := extPrice * (100 - disc) / 100
		suppCost := extPrice * 60 / 100

		_, _ = coord.ExecuteSQL(fmt.Sprintf("INSERT INTO lineorder VALUES (%d, %d, %d, %d, %d, %d, %d, %d, %d, %d, %d);",
			i, 1, cKey, pKey, sKey, dKey, qty, extPrice, disc, revenue, suppCost))
	}
	fmt.Printf("Done (%v)\n", time.Since(seedStart))
	fmt.Println("--------------------------------------------------------------------------------")

	// Queries
	type ssbQuery struct {
		id   string
		desc string
		sql  string
	}
	queries := []ssbQuery{
		{
			id:   "Q1.1",
			desc: "Flight 1: Fact Filtering & Discount Loss Aggregate",
			sql: `SELECT SUM(lo_extendedprice * lo_discount) AS revenue
			FROM lineorder
			JOIN date ON lo_orderdate = d_datekey
			WHERE d_year = 1993
			  AND lo_discount >= 1 AND lo_discount <= 3
			  AND lo_quantity < 25;`,
		},
		{
			id:   "Q2.1",
			desc: "Flight 2: 2-Way Join (LineOrder x Part x Supplier) with Brand Grouping",
			sql: `SELECT SUM(lo_revenue) AS sum_rev, d_year, p_brand1
			FROM lineorder
			JOIN date ON lo_orderdate = d_datekey
			JOIN part ON lo_partkey = p_partkey
			JOIN supplier ON lo_suppkey = s_suppkey
			WHERE p_category = 'MFGR#11'
			  AND s_region = 'AMERICA'
			GROUP BY d_year, p_brand1
			ORDER BY d_year, p_brand1;`,
		},
		{
			id:   "Q3.1",
			desc: "Flight 3: 3-Way Join (LineOrder x Customer x Supplier) Regional Trade Drilldown",
			sql: `SELECT c_nation, s_nation, d_year, SUM(lo_revenue) AS revenue
			FROM lineorder
			JOIN customer ON lo_custkey = c_custkey
			JOIN supplier ON lo_suppkey = s_suppkey
			JOIN date ON lo_orderdate = d_datekey
			WHERE c_region = 'ASIA'
			  AND s_region = 'ASIA'
			  AND d_year >= 1992 AND d_year <= 1997
			GROUP BY c_nation, s_nation, d_year
			ORDER BY d_year ASC, revenue DESC;`,
		},
		{
			id:   "Q4.1",
			desc: "Flight 4: 4-Way Full Star Join with Manufacturer Profit Breakdown",
			sql: `SELECT d_year, c_nation, SUM(lo_revenue - lo_supplycost) AS profit
			FROM lineorder
			JOIN customer ON lo_custkey = c_custkey
			JOIN supplier ON lo_suppkey = s_suppkey
			JOIN part ON lo_partkey = p_partkey
			JOIN date ON lo_orderdate = d_datekey
			WHERE c_region = 'AMERICA'
			  AND s_region = 'AMERICA'
			  AND (p_mfgr = 'MFGR#1' OR p_mfgr = 'MFGR#2')
			GROUP BY d_year, c_nation
			ORDER BY d_year, c_nation;`,
		},
	}

	fmt.Println("Executing SSB Benchmark Flight Queries:")
	fmt.Printf("%-6s | %-45s | %-12s | %-8s\n", "Query", "Description", "Latency (ms)", "Rows")
	fmt.Println("-------+-----------------------------------------------+--------------+---------")

	totalDuration := time.Duration(0)
	for _, q := range queries {
		// Warmup
		for w := 0; w < *warmup; w++ {
			_, _ = coord.ExecuteSQL(q.sql)
		}

		// Measured runs
		durSum := time.Duration(0)
		var lastRes *engine.QueryResult
		for it := 0; it < *iterations; it++ {
			t0 := time.Now()
			res, err := coord.ExecuteSQL(q.sql)
			elapsed := time.Since(t0)
			if err != nil {
				fmt.Printf("%-6s | ERROR: %v\n", q.id, err)
				break
			}
			durSum += elapsed
			lastRes = res
		}
		avgDur := durSum / time.Duration(*iterations)
		totalDuration += avgDur
		rowCount := 0
		if lastRes != nil {
			rowCount = len(lastRes.Rows)
		}
		fmt.Printf("%-6s | %-45s | %10.2f ms | %8d\n", q.id, q.desc, float64(avgDur.Microseconds())/1000.0, rowCount)
	}

	fmt.Println("-------+-----------------------------------------------+--------------+---------")
	fmt.Printf("Total Benchmark Flight Execution Time: %.2f ms\n", float64(totalDuration.Microseconds())/1000.0)
	fmt.Println("================================================================================")
}
