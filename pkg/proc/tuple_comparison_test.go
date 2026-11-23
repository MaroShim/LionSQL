package proc_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestTuple_ComparisonAndInPredicate(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. (1, 2) = (3, 4) should return 0 rows
	res1, err := coord.ExecuteSQL("SELECT 1 AS val WHERE (1, 2) = (3, 4);")
	if err != nil {
		t.Fatalf("query (1, 2) = (3, 4) failed: %v", err)
	}
	if len(res1.Rows) != 0 {
		t.Fatalf("expected 0 rows for (1, 2) = (3, 4), got %d rows", len(res1.Rows))
	}

	// 2. (1, 2) = (1, 2) should return 1 row
	res2, err := coord.ExecuteSQL("SELECT 1 AS val WHERE (1, 2) = (1, 2);")
	if err != nil {
		t.Fatalf("query (1, 2) = (1, 2) failed: %v", err)
	}
	if len(res2.Rows) != 1 {
		t.Fatalf("expected 1 row for (1, 2) = (1, 2), got %d rows", len(res2.Rows))
	}

	// 3. (1, 2) IN ((3, 4)) should return 0 rows
	res3, err := coord.ExecuteSQL("SELECT 1 AS val WHERE (1, 2) IN ((3, 4));")
	if err != nil {
		t.Fatalf("query (1, 2) IN ((3, 4)) failed: %v", err)
	}
	if len(res3.Rows) != 0 {
		t.Fatalf("expected 0 rows for (1, 2) IN ((3, 4)), got %d rows", len(res3.Rows))
	}

	// 4. (1, 2) IN ((3, 4), (1, 2)) should return 1 row
	res4, err := coord.ExecuteSQL("SELECT 1 AS val WHERE (1, 2) IN ((3, 4), (1, 2));")
	if err != nil {
		t.Fatalf("query (1, 2) IN ((3, 4), (1, 2)) failed: %v", err)
	}
	if len(res4.Rows) != 1 {
		t.Fatalf("expected 1 row for (1, 2) IN ((3, 4), (1, 2)), got %d rows", len(res4.Rows))
	}
}
