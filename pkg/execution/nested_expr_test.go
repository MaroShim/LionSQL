package execution_test

import (
	"math"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// setupNestedExprTestDB sets up an in-memory database with test tables for deep expression testing.
func setupNestedExprTestDB(t testing.TB) (*engine.Coordinator, func()) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())

	ddls := []string{
		`CREATE TABLE items (
			id INT PRIMARY KEY,
			cat VARCHAR(10),
			price INT,
			qty INT,
			discount INT,
			tax INT
		);`,
		`INSERT INTO items VALUES (1, 'A', 100, 2, 10, 5);`, // price*qty=200, net=195
		`INSERT INTO items VALUES (2, 'A', 200, 3, 20, 10);`, // price*qty=600, net=590
		`INSERT INTO items VALUES (3, 'B', 300, 1, 50, 15);`, // price*qty=300, net=265
		`INSERT INTO items VALUES (4, 'B', 50, 4, 5, 2);`,   // price*qty=200, net=197
	}

	for _, ddl := range ddls {
		if _, err := coord.ExecuteSQL(ddl); err != nil {
			t.Fatalf("setup failed on %q: %v", ddl, err)
		}
	}

	return coord, func() {
		_ = coord.Close()
		_ = eng.Close()
	}
}

// toFloatHelper converts any numeric value to float64 for deep testing comparison.
func toFloatHelper(v any) float64 {
	switch n := v.(type) {
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case float64:
		return n
	case float32:
		return float64(n)
	default:
		return 0
	}
}

// TestDeep_AggregateWithArithmetic verifies SUM, AVG, MIN, MAX with nested arithmetic expressions.
func TestDeep_AggregateWithArithmetic(t *testing.T) {
	coord, cleanup := setupNestedExprTestDB(t)
	defer cleanup()

	// 1. Global SUM with multiplication
	q1 := `SELECT SUM(price * qty) AS total_val, MIN(price * qty) AS min_val, MAX(price * qty) AS max_val FROM items;`
	res1, err := coord.ExecuteSQL(q1)
	if err != nil {
		t.Fatalf("Q1 failed: %v", err)
	}
	if len(res1.Rows) != 1 {
		t.Fatalf("Q1 expected 1 row, got %d", len(res1.Rows))
	}

	// 200 + 600 + 300 + 200 = 1300
	row1 := res1.Rows[0]
	if total := toFloatHelper(row1["total_val"]); total != 1300 {
		t.Errorf("Q1 total_val: expected 1300, got %v (raw: %+v)", total, row1["total_val"])
	}
	if minVal := toFloatHelper(row1["min_val"]); minVal != 200 {
		t.Errorf("Q1 min_val: expected 200, got %v", minVal)
	}
	if maxVal := toFloatHelper(row1["max_val"]); maxVal != 600 {
		t.Errorf("Q1 max_val: expected 600, got %v", maxVal)
	}

	// 2. Polynomial nested arithmetic: price * qty - discount + tax
	// Row 1: 100*2 - 10 + 5 = 195
	// Row 2: 200*3 - 20 + 10 = 590
	// Row 3: 300*1 - 50 + 15 = 265
	// Row 4: 50*4 - 5 + 2 = 197
	// Sum = 195 + 590 + 265 + 197 = 1247
	q2 := `SELECT SUM(price * qty - discount + tax) AS net_sum FROM items;`
	res2, err := coord.ExecuteSQL(q2)
	if err != nil {
		t.Fatalf("Q2 failed: %v", err)
	}
	if len(res2.Rows) != 1 {
		t.Fatalf("Q2 expected 1 row, got %d", len(res2.Rows))
	}
	if netSum := toFloatHelper(res2.Rows[0]["net_sum"]); netSum != 1247 {
		t.Errorf("Q2 net_sum: expected 1247, got %v (raw: %+v)", netSum, res2.Rows[0])
	}
}

// TestDeep_HavingWithArithmetic verifies HAVING clause comparison against aggregate arithmetic results.
func TestDeep_HavingWithArithmetic(t *testing.T) {
	coord, cleanup := setupNestedExprTestDB(t)
	defer cleanup()

	// Cat A: (100*2) + (200*3) = 800
	// Cat B: (300*1) + (50*4) = 500
	// HAVING SUM(price * qty) > 600 should ONLY return Cat A
	q1 := `SELECT cat, SUM(price * qty) AS cat_total
	FROM items
	GROUP BY cat
	HAVING SUM(price * qty) > 600;`

	res1, err := coord.ExecuteSQL(q1)
	if err != nil {
		t.Fatalf("HAVING query failed: %v", err)
	}
	if len(res1.Rows) != 1 {
		t.Fatalf("HAVING expected 1 row, got %d (rows: %+v)", len(res1.Rows), res1.Rows)
	}
	if res1.Rows[0]["cat"] != "A" {
		t.Errorf("HAVING expected cat 'A', got %v", res1.Rows[0]["cat"])
	}
	if total := toFloatHelper(res1.Rows[0]["cat_total"]); total != 800 {
		t.Errorf("HAVING expected cat_total 800, got %v", total)
	}
}

// TestDeep_SortWithArithmetic verifies ORDER BY evaluated on arithmetic expressions.
func TestDeep_SortWithArithmetic(t *testing.T) {
	coord, cleanup := setupNestedExprTestDB(t)
	defer cleanup()

	// Line total: Row 2 (600), Row 3 (300), Row 1 (200), Row 4 (200)
	// ORDER BY (price * qty) DESC
	q := `SELECT id, (price * qty) AS line_total FROM items ORDER BY (price * qty) DESC, id ASC;`
	res, err := coord.ExecuteSQL(q)
	if err != nil {
		t.Fatalf("ORDER BY expression failed: %v", err)
	}
	if len(res.Rows) != 4 {
		t.Fatalf("Expected 4 rows, got %d", len(res.Rows))
	}

	expectedIDs := []int64{2, 3, 1, 4}
	for i, expID := range expectedIDs {
		actualID := int64(toFloatHelper(res.Rows[i]["id"]))
		if actualID != expID {
			t.Errorf("Row %d: expected id %d, got %d (row: %+v)", i, expID, actualID, res.Rows[i])
		}
	}
}

// TestDeep_FilterPolynomial verifies WHERE clauses with multiple nested binary arithmetic operators.
func TestDeep_FilterPolynomial(t *testing.T) {
	coord, cleanup := setupNestedExprTestDB(t)
	defer cleanup()

	// Condition: (price * qty) >= 300 AND (discount + tax) >= 30
	// Row 1: 200 >= 300 (False)
	// Row 2: 600 >= 300 (True), 20+10 = 30 >= 30 (True) -> Match!
	// Row 3: 300 >= 300 (True), 50+15 = 65 >= 30 (True) -> Match!
	// Row 4: 200 >= 300 (False)
	q := `SELECT id, cat FROM items WHERE (price * qty) >= 300 AND (discount + tax) >= 30 ORDER BY id ASC;`
	res, err := coord.ExecuteSQL(q)
	if err != nil {
		t.Fatalf("Filter polynomial failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("Expected 2 rows, got %d (rows: %+v)", len(res.Rows), res.Rows)
	}
	if toFloatHelper(res.Rows[0]["id"]) != 2 || toFloatHelper(res.Rows[1]["id"]) != 3 {
		t.Errorf("Expected IDs [2, 3], got [%v, %v]", res.Rows[0]["id"], res.Rows[1]["id"])
	}
}

// TestDeep_WindowWithArithmetic verifies Window functions with nested expressions.
func TestDeep_WindowWithArithmetic(t *testing.T) {
	coord, cleanup := setupNestedExprTestDB(t)
	defer cleanup()

	// Window SUM(price * qty) PARTITION BY cat ORDER BY id
	// Cat A:
	//   Row 1: 200 -> running = 200
	//   Row 2: 600 -> running = 800
	// Cat B:
	//   Row 3: 300 -> running = 300
	//   Row 4: 200 -> running = 500
	q := `SELECT id, cat, SUM(price * qty) OVER (PARTITION BY cat ORDER BY id ASC) AS run_tot
	FROM items
	ORDER BY id ASC;`

	res, err := coord.ExecuteSQL(q)
	if err != nil {
		t.Fatalf("Window function with arithmetic failed: %v", err)
	}
	if len(res.Rows) != 4 {
		t.Fatalf("Expected 4 rows, got %d", len(res.Rows))
	}

	expectedRunning := []float64{200, 800, 300, 500}
	for i, exp := range expectedRunning {
		actual := toFloatHelper(res.Rows[i]["run_tot"])
		if math.Abs(actual-exp) > 0.001 {
			t.Errorf("Row %d (id %v): expected run_tot %v, got %v (row: %+v)",
				i, res.Rows[i]["id"], exp, actual, res.Rows[i])
		}
	}
}
