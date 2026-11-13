package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func setupDerivedValuesCoord(t *testing.T) (*engine.Coordinator, func()) {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	cleanup := func() {
		coord.Close()
		eng.Close()
	}
	return coord, cleanup
}

func TestDerivedValues_Basic(t *testing.T) {
	coord, cleanup := setupDerivedValuesCoord(t)
	defer cleanup()

	// 1. SELECT with WHERE on derived values table
	sql1 := `SELECT v.id, v.name FROM (VALUES (1, 'Alice'), (2, 'Bob')) AS v(id, name) WHERE v.id = 2;`
	res1, err := coord.ExecuteSQL(sql1)
	if err != nil {
		t.Fatalf("query 1 failed: %v", err)
	}
	if len(res1.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d (%+v)", len(res1.Rows), res1.Rows)
	}
	if res1.Rows[0]["id"] != int64(2) || res1.Rows[0]["name"] != "Bob" {
		t.Fatalf("unexpected row 1: %+v", res1.Rows[0])
	}

	// 2. SELECT * with multiple columns and rows
	sql2 := `SELECT * FROM (VALUES (10, 'Pen', 100), (20, 'Book', 500)) AS items(id, name, price) ORDER BY id ASC;`
	res2, err := coord.ExecuteSQL(sql2)
	if err != nil {
		t.Fatalf("query 2 failed: %v", err)
	}
	if len(res2.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d (%+v)", len(res2.Rows), res2.Rows)
	}
	if res2.Rows[0]["name"] != "Pen" || res2.Rows[1]["name"] != "Book" {
		t.Fatalf("unexpected row 2: %+v", res2.Rows)
	}

	// 3. JOIN between two derived VALUES tables
	sql3 := `
		SELECT a.id, a.name, b.score 
		FROM (VALUES (1, 'Alice'), (2, 'Bob')) AS a(id, name)
		JOIN (VALUES (1, 95), (2, 88)) AS b(id, score) ON a.id = b.id
		ORDER BY a.id ASC;
	`
	res3, err := coord.ExecuteSQL(sql3)
	if err != nil {
		t.Fatalf("query 3 (JOIN) failed: %v", err)
	}
	if len(res3.Rows) != 2 {
		t.Fatalf("expected 2 rows from JOIN, got %d (%+v)", len(res3.Rows), res3.Rows)
	}
	if res3.Rows[0]["score"] != int64(95) || res3.Rows[1]["score"] != int64(88) {
		t.Fatalf("unexpected JOIN rows: %+v", res3.Rows)
	}

	// 4. Derived column renaming on a SELECT derived table: AS t(col_a, col_b)
	sql4 := `SELECT col_a, col_b FROM (SELECT 100 AS num, 'test' AS str) AS t(col_a, col_b);`
	res4, err := coord.ExecuteSQL(sql4)
	if err != nil {
		t.Fatalf("query 4 (SELECT derived column rename) failed: %v", err)
	}
	if len(res4.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d (%+v)", len(res4.Rows), res4.Rows)
	}
	if res4.Rows[0]["col_a"] != int64(100) || res4.Rows[0]["col_b"] != "test" {
		t.Fatalf("unexpected row 4: %+v", res4.Rows[0])
	}

	// 5. Standalone VALUES statement execution
	sql5 := `VALUES (1, 'Alpha'), (2, 'Beta');`
	res5, err := coord.ExecuteSQL(sql5)
	if err != nil {
		t.Fatalf("query 5 (standalone VALUES) failed: %v", err)
	}
	if len(res5.Rows) != 2 {
		t.Fatalf("expected 2 rows from standalone VALUES, got %d (%+v)", len(res5.Rows), res5.Rows)
	}
	if res5.Rows[0]["column1"] != int64(1) || res5.Rows[1]["column2"] != "Beta" {
		t.Fatalf("unexpected standalone VALUES rows: %+v", res5.Rows)
	}

	// 6. Arithmetic expressions and CAST in VALUES
	sql6 := `SELECT * FROM (VALUES (1 + 2, CAST('99' AS INT))) AS calc(sum_val, cast_val);`
	res6, err := coord.ExecuteSQL(sql6)
	if err != nil {
		t.Fatalf("query 6 (expressions in VALUES) failed: %v", err)
	}
	if len(res6.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d (%+v)", len(res6.Rows), res6.Rows)
	}
	if res6.Rows[0]["sum_val"] != int64(3) || res6.Rows[0]["cast_val"] != int64(99) {
		t.Fatalf("unexpected row 6: %+v", res6.Rows[0])
	}
}
