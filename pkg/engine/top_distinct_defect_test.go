package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestDEFECT_SelectTopPercent_DistinctEvaluationOrder(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer func() {
		coord.Close()
		eng.Close()
	}()

	// Create table with 6 rows: 1, 1, 1, 1, 2, 3
	queries := []string{
		"CREATE TABLE t_top_distinct (val INT);",
		"INSERT INTO t_top_distinct VALUES (1);",
		"INSERT INTO t_top_distinct VALUES (1);",
		"INSERT INTO t_top_distinct VALUES (1);",
		"INSERT INTO t_top_distinct VALUES (1);",
		"INSERT INTO t_top_distinct VALUES (2);",
		"INSERT INTO t_top_distinct VALUES (3);",
	}

	for _, q := range queries {
		res, err := coord.ExecuteSQL(q)
		if err != nil || !res.Success {
			t.Fatalf("query %q failed: %v, res: %+v", q, err, res)
		}
	}

	// T-SQL standard: SELECT DISTINCT TOP (50) PERCENT val FROM t_top_distinct ORDER BY val
	// DISTINCT applies before TOP PERCENT.
	res, err := coord.ExecuteSQL("SELECT DISTINCT TOP (50) PERCENT val FROM t_top_distinct ORDER BY val;")
	if err != nil || !res.Success {
		t.Fatalf("SELECT DISTINCT TOP PERCENT failed: %v, res: %+v", err, res)
	}

	// Distinct values are {1, 2, 3} (3 distinct rows).
	// 50 PERCENT of 3 distinct rows = ceil(3 * 0.5) = 2 rows ({1}, {2}).
	// If TOP PERCENT ran before DISTINCT: 50% of 6 rows = 3 rows (1, 1, 1), then DISTINCT gave 1 row ({1}).
	t.Logf("Rows: %+v", res.Rows)
	if len(res.Rows) != 2 {
		t.Fatalf("[DEFECT-SELECT-TOP-PERCENT-DISTINCT-1] Expected 2 rows from DISTINCT TOP 50 PERCENT, got %d rows: %+v", len(res.Rows), res.Rows)
	}
}
