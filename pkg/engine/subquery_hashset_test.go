package engine

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestCoordinator_SubqueryHashSet_UncorrelatedLargeIN(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE large_orders (id INT PRIMARY KEY, amount INT);"); err != nil {
		t.Fatalf("create large_orders failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("CREATE TABLE target_ids (id INT PRIMARY KEY);"); err != nil {
		t.Fatalf("create target_ids failed: %v", err)
	}

	// Insert 1000 orders
	var sb strings.Builder
	sb.WriteString("INSERT INTO large_orders (id, amount) VALUES ")
	for i := 1; i <= 1000; i++ {
		if i > 1 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("(%d, %d)", i, i*10))
	}
	sb.WriteString(";")
	if _, err := coord.ExecuteSQL(sb.String()); err != nil {
		t.Fatalf("insert large_orders failed: %v", err)
	}

	// Insert 500 target ids (even numbers 2, 4, ..., 1000)
	var sb2 strings.Builder
	sb2.WriteString("INSERT INTO target_ids (id) VALUES ")
	for i := 1; i <= 500; i++ {
		if i > 1 {
			sb2.WriteString(", ")
		}
		sb2.WriteString(fmt.Sprintf("(%d)", i*2))
	}
	sb2.WriteString(";")
	if _, err := coord.ExecuteSQL(sb2.String()); err != nil {
		t.Fatalf("insert target_ids failed: %v", err)
	}

	// Uncorrelated IN subquery with 500 items materialized into SubqueryHashSet
	q := "SELECT COUNT(*) AS cnt FROM large_orders WHERE id IN (SELECT id FROM target_ids);"
	res, err := coord.ExecuteSQL(q)
	if err != nil || !res.Success {
		t.Fatalf("subquery execute failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 aggregate row, got %d", len(res.Rows))
	}
	cntVal := fmt.Sprintf("%v", res.Rows[0]["cnt"])
	if cntVal != "500" {
		t.Fatalf("expected count 500, got %s", cntVal)
	}
}

func TestCoordinator_SubqueryHashSet_UncorrelatedNOTIN_ThreeValuedLogic(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE items_3vl (id INT PRIMARY KEY, val INT);"); err != nil {
		t.Fatalf("create items_3vl failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO items_3vl VALUES (1, 10), (2, 20), (3, 30);"); err != nil {
		t.Fatalf("insert items_3vl failed: %v", err)
	}

	// 1. Blacklist containing NULL
	if _, err := coord.ExecuteSQL("CREATE TABLE bl_null (id INT PRIMARY KEY, bad_val INT);"); err != nil {
		t.Fatalf("create bl_null failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO bl_null VALUES (1, 20), (2, NULL);"); err != nil {
		t.Fatalf("insert bl_null failed: %v", err)
	}

	// In SQL-92 3VL: x NOT IN (20, NULL) evaluates to UNKNOWN for all non-matching values -> 0 rows in WHERE
	res, err := coord.ExecuteSQL("SELECT * FROM items_3vl WHERE val NOT IN (SELECT bad_val FROM bl_null);")
	if err != nil || !res.Success {
		t.Fatalf("query failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows for NOT IN subquery containing NULL, got %d rows: %+v", len(res.Rows), res.Rows)
	}

	// 2. Blacklist without NULL
	if _, err := coord.ExecuteSQL("CREATE TABLE bl_no_null (id INT PRIMARY KEY, bad_val INT);"); err != nil {
		t.Fatalf("create bl_no_null failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO bl_no_null VALUES (1, 20);"); err != nil {
		t.Fatalf("insert bl_no_null failed: %v", err)
	}

	res2, err := coord.ExecuteSQL("SELECT id, val FROM items_3vl WHERE val NOT IN (SELECT bad_val FROM bl_no_null) ORDER BY id ASC;")
	if err != nil || !res2.Success {
		t.Fatalf("query without null failed: %v", err)
	}
	if len(res2.Rows) != 2 {
		t.Fatalf("expected 2 rows for NOT IN subquery without NULL, got %d rows: %+v", len(res2.Rows), res2.Rows)
	}
	if fmt.Sprintf("%v", res2.Rows[0]["id"]) != "1" || fmt.Sprintf("%v", res2.Rows[1]["id"]) != "3" {
		t.Errorf("expected ids 1 and 3, got: %v and %v", res2.Rows[0]["id"], res2.Rows[1]["id"])
	}
}

func TestCoordinator_SubqueryHashSet_ExceedCapacity_FailClosed(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE huge_src (id INT PRIMARY KEY);"); err != nil {
		t.Fatalf("create huge_src failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("CREATE TABLE huge_tgt (id INT PRIMARY KEY);"); err != nil {
		t.Fatalf("create huge_tgt failed: %v", err)
	}

	// Insert 10,001 rows into huge_src
	batchSize := 1000
	for b := 0; b < 10; b++ {
		var sb strings.Builder
		sb.WriteString("INSERT INTO huge_src (id) VALUES ")
		for i := 1; i <= batchSize; i++ {
			if i > 1 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("(%d)", b*batchSize+i))
		}
		sb.WriteString(";")
		if _, err := coord.ExecuteSQL(sb.String()); err != nil {
			t.Fatalf("batch %d insert failed: %v", b, err)
		}
	}
	// 10,001-th row
	if _, err := coord.ExecuteSQL("INSERT INTO huge_src VALUES (10001);"); err != nil {
		t.Fatalf("insert 10001-th row failed: %v", err)
	}

	// Subquery returning 10,001 rows (with GROUP BY so it bypasses simple table semi-join decorrelation)
	// must fail-closed with ErrSubqueryExceedsCapacity ([RULE:BoundedBuffers])
	q := "SELECT * FROM huge_tgt WHERE id IN (SELECT id FROM huge_src GROUP BY id);"
	_, err := coord.ExecuteSQL(q)
	if err == nil {
		t.Fatalf("expected subquery to fail due to capacity limit > %d", MaxSubqueryHashSetCapacity)
	}
	if !strings.Contains(err.Error(), "exceeds maximum hashset capacity") {
		t.Fatalf("expected error mentioning hashset capacity, got: %v", err)
	}
}
