package execution_test

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// setupSSBDatabase creates the standard 5 tables (customer, supplier, part, date, lineorder)
// and seeds them based on the scale factor sf (e.g. 0.001 for ~6,000 lineorder rows).
func setupSSBDatabase(t testing.TB, sf float64) (*engine.Coordinator, storage.Engine) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())

	// 1. DDL
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
			t.Fatalf("SSB DDL failed: %v", err)
		}
	}

	// 2. Deterministic Seeding
	rng := rand.New(rand.NewSource(42))
	regions := []string{"AMERICA", "ASIA", "EUROPE", "AFRICA", "MIDDLE EAST"}
	nations := map[string][]string{
		"AMERICA":     {"UNITED STATES", "CANADA", "BRAZIL"},
		"ASIA":        {"JAPAN", "CHINA", "INDIA"},
		"EUROPE":      {"UNITED KINGDOM", "GERMANY", "FRANCE"},
		"AFRICA":      {"EGYPT", "ALGERIA", "KENYA"},
		"MIDDLE EAST": {"SAUDI ARABIA", "JORDAN", "IRAQ"},
	}

	numCust := int(30000 * sf)
	if numCust < 20 {
		numCust = 20
	}
	numSupp := int(2000 * sf)
	if numSupp < 10 {
		numSupp = 10
	}
	numPart := int(20000 * sf)
	if numPart < 20 {
		numPart = 20
	}
	numOrders := int(6000000 * sf)
	if numOrders < 1000 {
		numOrders = 1000
	}

	// Dates (1992 ~ 1998, 7 years)
	dateKeys := make([]int, 0, 7*365)
	curDate := time.Date(1992, 1, 1, 0, 0, 0, 0, time.UTC)
	for y := 1992; y <= 1998; y++ {
		for d := 1; d <= 365; d++ {
			dKey := curDate.Year()*10000 + int(curDate.Month())*100 + curDate.Day()
			ymNum := curDate.Year()*100 + int(curDate.Month())
			dateKeys = append(dateKeys, dKey)
			insertSQL := fmt.Sprintf("INSERT INTO date VALUES (%d, %d, %d);", dKey, curDate.Year(), ymNum)
			_, _ = coord.ExecuteSQL(insertSQL)
			curDate = curDate.AddDate(0, 0, 1)
		}
	}

	// Seed Customer
	for i := 1; i <= numCust; i++ {
		reg := regions[rng.Intn(len(regions))]
		natList := nations[reg]
		nat := natList[rng.Intn(len(natList))]
		insertSQL := fmt.Sprintf("INSERT INTO customer VALUES (%d, 'Cust_%d', 'City_%d', '%s', '%s');",
			i, i, i%10, nat, reg)
		_, _ = coord.ExecuteSQL(insertSQL)
	}

	// Seed Supplier
	for i := 1; i <= numSupp; i++ {
		reg := regions[rng.Intn(len(regions))]
		natList := nations[reg]
		nat := natList[rng.Intn(len(natList))]
		insertSQL := fmt.Sprintf("INSERT INTO supplier VALUES (%d, 'Supp_%d', 'City_%d', '%s', '%s');",
			i, i, i%10, nat, reg)
		_, _ = coord.ExecuteSQL(insertSQL)
	}

	// Seed Part
	for i := 1; i <= numPart; i++ {
		mfgr := fmt.Sprintf("MFGR#%d", (i%5)+1)
		cat := fmt.Sprintf("MFGR#%d%d", (i%5)+1, (i%5)+1)
		brand := fmt.Sprintf("%s%d", cat, (i%10)+1)
		insertSQL := fmt.Sprintf("INSERT INTO part VALUES (%d, 'Part_%d', '%s', '%s', '%s');",
			i, i, mfgr, cat, brand)
		_, _ = coord.ExecuteSQL(insertSQL)
	}

	// Seed LineOrder (Fact table)
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

		insertSQL := fmt.Sprintf("INSERT INTO lineorder VALUES (%d, %d, %d, %d, %d, %d, %d, %d, %d, %d, %d);",
			i, 1, cKey, pKey, sKey, dKey, qty, extPrice, disc, revenue, suppCost)
		_, _ = coord.ExecuteSQL(insertSQL)
	}

	return coord, eng
}

func TestSSB_FlightQueries(t *testing.T) {
	// SF 0.0005 for rapid unit test (~3,000 lineorders)
	coord, eng := setupSSBDatabase(t, 0.0005)
	defer eng.Close()
	defer coord.Close()

	// Q1.1
	q1 := `SELECT SUM(lo_extendedprice * lo_discount) AS revenue
	FROM lineorder
	JOIN date ON lo_orderdate = d_datekey
	WHERE d_year = 1993
	  AND lo_discount >= 1 AND lo_discount <= 3
	  AND lo_quantity < 25;`
	res1, err := coord.ExecuteSQL(q1)
	if err != nil {
		t.Fatalf("Q1.1 failed: %v", err)
	}
	if len(res1.Rows) == 0 {
		t.Fatalf("Q1.1 returned 0 rows")
	}
	if rev, ok := res1.Rows[0]["revenue"]; !ok || rev == nil {
		t.Fatalf("Q1.1 returned nil revenue: %+v", res1.Rows[0])
	}
	t.Logf("Q1.1 verified revenue: %v", res1.Rows[0]["revenue"])

	// Q2.1
	q2 := `SELECT SUM(lo_revenue) AS sum_rev, d_year, p_brand1
	FROM lineorder
	JOIN date ON lo_orderdate = d_datekey
	JOIN part ON lo_partkey = p_partkey
	JOIN supplier ON lo_suppkey = s_suppkey
	WHERE p_category = 'MFGR#11'
	  AND s_region = 'AMERICA'
	GROUP BY d_year, p_brand1
	ORDER BY d_year, p_brand1;`
	res2, err := coord.ExecuteSQL(q2)
	if err != nil {
		t.Fatalf("Q2.1 failed: %v", err)
	}
	t.Logf("Q2.1 returned %d group rows", len(res2.Rows))

	// Q3.1
	q3 := `SELECT c_nation, s_nation, d_year, SUM(lo_revenue) AS revenue
	FROM lineorder
	JOIN customer ON lo_custkey = c_custkey
	JOIN supplier ON lo_suppkey = s_suppkey
	JOIN date ON lo_orderdate = d_datekey
	WHERE c_region = 'ASIA'
	  AND s_region = 'ASIA'
	  AND d_year >= 1992 AND d_year <= 1997
	GROUP BY c_nation, s_nation, d_year
	ORDER BY d_year ASC, revenue DESC;`
	res3, err := coord.ExecuteSQL(q3)
	if err != nil {
		t.Fatalf("Q3.1 failed: %v", err)
	}
	t.Logf("Q3.1 returned %d nation trade rows", len(res3.Rows))

	// Q4.1
	q4 := `SELECT d_year, c_nation, SUM(lo_revenue - lo_supplycost) AS profit
	FROM lineorder
	JOIN customer ON lo_custkey = c_custkey
	JOIN supplier ON lo_suppkey = s_suppkey
	JOIN part ON lo_partkey = p_partkey
	JOIN date ON lo_orderdate = d_datekey
	WHERE c_region = 'AMERICA'
	  AND s_region = 'AMERICA'
	  AND (p_mfgr = 'MFGR#1' OR p_mfgr = 'MFGR#2')
	GROUP BY d_year, c_nation
	ORDER BY d_year, c_nation;`
	res4, err := coord.ExecuteSQL(q4)
	if err != nil {
		t.Fatalf("Q4.1 failed: %v", err)
	}
	t.Logf("Q4.1 returned %d profit breakdown rows", len(res4.Rows))
	if len(res4.Rows) > 0 {
		if profit, ok := res4.Rows[0]["profit"]; !ok || profit == nil {
			t.Fatalf("Q4.1 returned nil profit: %+v", res4.Rows[0])
		}
		t.Logf("Q4.1 verified profit row[0]: %+v", res4.Rows[0])
	}
}

func BenchmarkSSB_Q1_1(b *testing.B) {
	coord, eng := setupSSBDatabase(b, 0.001) // ~6,000 rows
	defer eng.Close()
	defer coord.Close()

	q := `SELECT SUM(lo_extendedprice * lo_discount) AS revenue
	FROM lineorder
	JOIN date ON lo_orderdate = d_datekey
	WHERE d_year = 1993
	  AND lo_discount >= 1 AND lo_discount <= 3
	  AND lo_quantity < 25;`

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		res, err := coord.ExecuteSQL(q)
		if err != nil || len(res.Rows) == 0 {
			b.Fatalf("Q1.1 benchmark error: %v", err)
		}
	}
}

func BenchmarkSSB_Q2_1(b *testing.B) {
	coord, eng := setupSSBDatabase(b, 0.001) // ~6,000 rows
	defer eng.Close()
	defer coord.Close()

	q := `SELECT SUM(lo_revenue) AS sum_rev, d_year, p_brand1
	FROM lineorder
	JOIN date ON lo_orderdate = d_datekey
	JOIN part ON lo_partkey = p_partkey
	JOIN supplier ON lo_suppkey = s_suppkey
	WHERE p_category = 'MFGR#11'
	  AND s_region = 'AMERICA'
	GROUP BY d_year, p_brand1
	ORDER BY d_year, p_brand1;`

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		res, err := coord.ExecuteSQL(q)
		if err != nil {
			b.Fatalf("Q2.1 benchmark error: %v", err)
		}
		_ = res
	}
}

func BenchmarkSSB_Q3_1(b *testing.B) {
	coord, eng := setupSSBDatabase(b, 0.001) // ~6,000 rows
	defer eng.Close()
	defer coord.Close()

	q := `SELECT c_nation, s_nation, d_year, SUM(lo_revenue) AS revenue
	FROM lineorder
	JOIN customer ON lo_custkey = c_custkey
	JOIN supplier ON lo_suppkey = s_suppkey
	JOIN date ON lo_orderdate = d_datekey
	WHERE c_region = 'ASIA'
	  AND s_region = 'ASIA'
	  AND d_year >= 1992 AND d_year <= 1997
	GROUP BY c_nation, s_nation, d_year
	ORDER BY d_year ASC, revenue DESC;`

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		res, err := coord.ExecuteSQL(q)
		if err != nil {
			b.Fatalf("Q3.1 benchmark error: %v", err)
		}
		_ = res
	}
}

func BenchmarkSSB_Q4_1(b *testing.B) {
	coord, eng := setupSSBDatabase(b, 0.001) // ~6,000 rows
	defer eng.Close()
	defer coord.Close()

	q := `SELECT d_year, c_nation, SUM(lo_revenue - lo_supplycost) AS profit
	FROM lineorder
	JOIN customer ON lo_custkey = c_custkey
	JOIN supplier ON lo_suppkey = s_suppkey
	JOIN part ON lo_partkey = p_partkey
	JOIN date ON lo_orderdate = d_datekey
	WHERE c_region = 'AMERICA'
	  AND s_region = 'AMERICA'
	  AND (p_mfgr = 'MFGR#1' OR p_mfgr = 'MFGR#2')
	GROUP BY d_year, c_nation
	ORDER BY d_year, c_nation;`

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		res, err := coord.ExecuteSQL(q)
		if err != nil {
			b.Fatalf("Q4.1 benchmark error: %v", err)
		}
		_ = res
	}
}

