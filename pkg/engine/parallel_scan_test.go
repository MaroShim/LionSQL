package engine_test

import (
	"fmt"
	"strings"
	"testing"
)

// TestParallelScan_SQL_EndToEnd_Aggregation verifies that multi-core parallel scan (ARCH-PARALLEL-SCAN-1)
// produces 100% bit-for-bit identical results to single-threaded scan across COUNT, SUM, and GROUP BY aggregations.
func TestParallelScan_SQL_EndToEnd_Aggregation(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	initSQL := `
CREATE TABLE t_par_orders (
    id INT PRIMARY KEY,
    amount INT,
    category VARCHAR(50),
    status VARCHAR(20)
);
`
	if _, err := coord.ExecuteSQL(initSQL); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	const totalRows = 3000
	for i := 1; i <= totalRows; i++ {
		insertSQL := fmt.Sprintf("INSERT INTO t_par_orders VALUES (%d, %d, 'cat_%d', 'PAID');",
			i, i*10, i%5)
		if _, err := coord.ExecuteSQL(insertSQL); err != nil {
			t.Fatalf("INSERT %d failed: %v", i, err)
		}
	}

	// 1. Single-threaded execution (forced via OPTION (MAXDOP 1))
	qSingle := `
SELECT category, COUNT(*) AS cnt, SUM(amount) AS total
FROM t_par_orders
WHERE amount >= 500
GROUP BY category
ORDER BY category
OPTION (MAXDOP 1);
`
	resSingle, err := coord.ExecuteSQL(qSingle)
	if err != nil {
		t.Fatalf("single-thread query failed: %v", err)
	}

	// 2. Parallel multi-core execution (forced via OPTION (MAXDOP 4))
	qParallel := `
SELECT category, COUNT(*) AS cnt, SUM(amount) AS total
FROM t_par_orders
WHERE amount >= 500
GROUP BY category
ORDER BY category
OPTION (MAXDOP 4);
`
	resParallel, err := coord.ExecuteSQL(qParallel)
	if err != nil {
		t.Fatalf("parallel query failed: %v", err)
	}

	// 3. Auto-parallel execution (DOP determined by CBO planner)
	qAuto := `
SELECT category, COUNT(*) AS cnt, SUM(amount) AS total
FROM t_par_orders
WHERE amount >= 500
GROUP BY category
ORDER BY category;
`
	resAuto, err := coord.ExecuteSQL(qAuto)
	if err != nil {
		t.Fatalf("auto parallel query failed: %v", err)
	}

	if len(resSingle.Rows) != 5 {
		t.Fatalf("expected 5 category groups, got %d", len(resSingle.Rows))
	}
	if len(resParallel.Rows) != len(resSingle.Rows) {
		t.Fatalf("mismatch in group count: single=%d, parallel=%d", len(resSingle.Rows), len(resParallel.Rows))
	}
	if len(resAuto.Rows) != len(resSingle.Rows) {
		t.Fatalf("mismatch in group count: single=%d, auto=%d", len(resSingle.Rows), len(resAuto.Rows))
	}

	for idx, rowSingle := range resSingle.Rows {
		rowParallel := resParallel.Rows[idx]
		rowAuto := resAuto.Rows[idx]

		catSingle := fmt.Sprintf("%v", rowSingle["category"])
		catParallel := fmt.Sprintf("%v", rowParallel["category"])
		catAuto := fmt.Sprintf("%v", rowAuto["category"])

		cntSingle := fmt.Sprintf("%v", rowSingle["cnt"])
		cntParallel := fmt.Sprintf("%v", rowParallel["cnt"])
		cntAuto := fmt.Sprintf("%v", rowAuto["cnt"])

		totSingle := fmt.Sprintf("%v", rowSingle["total"])
		totParallel := fmt.Sprintf("%v", rowParallel["total"])
		totAuto := fmt.Sprintf("%v", rowAuto["total"])

		if catSingle != catParallel || catSingle != catAuto {
			t.Fatalf("group %d category mismatch: single=%s, par=%s, auto=%s", idx, catSingle, catParallel, catAuto)
		}
		if cntSingle != cntParallel || cntSingle != cntAuto {
			t.Fatalf("group %d count mismatch: single=%s, par=%s, auto=%s", idx, cntSingle, cntParallel, cntAuto)
		}
		if totSingle != totParallel || totSingle != totAuto {
			t.Fatalf("group %d total mismatch: single=%s, par=%s, auto=%s", idx, totSingle, totParallel, totAuto)
		}
	}
}

// TestParallelScan_SQL_ExplainPlan verifies that EXPLAIN displays parallel worker degree correctly
// when MAXDOP is specified or when table cardinality warrants parallelism.
func TestParallelScan_SQL_ExplainPlan(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	initSQL := `
CREATE TABLE t_par_explain (id INT PRIMARY KEY, val INT);
`
	if _, err := coord.ExecuteSQL(initSQL); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 1. EXPLAIN with OPTION (MAXDOP 4) must show (Parallel, Workers: 4)
	resPar, err := coord.ExecuteSQL("EXPLAIN SELECT * FROM t_par_explain WHERE val > 10 OPTION (MAXDOP 4);")
	if err != nil {
		t.Fatalf("EXPLAIN MAXDOP 4 failed: %v", err)
	}
	var foundParallel bool
	for _, row := range resPar.Rows {
		line := fmt.Sprintf("%v", row["QUERY PLAN"])
		if strings.Contains(line, "Parallel") && strings.Contains(line, "Workers: 4") {
			foundParallel = true
			break
		}
	}
	if !foundParallel {
		t.Fatalf("expected EXPLAIN plan to contain 'Parallel, Workers: 4', got: %+v", resPar.Rows)
	}

	// 2. EXPLAIN with OPTION (MAXDOP 1) must NOT contain Parallel
	resSingle, err := coord.ExecuteSQL("EXPLAIN SELECT * FROM t_par_explain WHERE val > 10 OPTION (MAXDOP 1);")
	if err != nil {
		t.Fatalf("EXPLAIN MAXDOP 1 failed: %v", err)
	}
	for _, row := range resSingle.Rows {
		line := fmt.Sprintf("%v", row["QUERY PLAN"])
		if strings.Contains(line, "Parallel") {
			t.Fatalf("expected single-threaded EXPLAIN plan without Parallel, got: %s", line)
		}
	}
}
