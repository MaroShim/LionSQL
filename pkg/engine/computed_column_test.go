package engine

import (
	"testing"
)

// TestEngine_ComputedColumn_SelectEvaluation verifies that computed columns
// defined via `col AS (expr)` evaluate dynamically upon SELECT instead of returning NULL.
func TestEngine_ComputedColumn_SelectEvaluation(t *testing.T) {
	coord, cleanup := setupTestCoord(t)
	defer cleanup()

	// 1. Create table with computed column
	_, err := coord.ExecuteSQL("CREATE TABLE test_items (id INT PRIMARY KEY, qty INT, price INT, total AS (qty * price))")
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 2. Insert rows (omitting computed column)
	_, err = coord.ExecuteSQL("INSERT INTO test_items (id, qty, price) VALUES (1, 10, 5), (2, 3, 20)")
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// 3. SELECT * and verify total column is computed (50 and 60)
	res, err := coord.ExecuteSQL("SELECT id, qty, price, total FROM test_items ORDER BY id ASC")
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}

	val1 := res.Rows[0]["total"]
	if val1 == nil {
		t.Fatalf("expected row 1 total = 50, got NULL")
	}

	val2 := res.Rows[1]["total"]
	if val2 == nil {
		t.Fatalf("expected row 2 total = 60, got NULL")
	}

	// 4. Test Temporary Table (#t) with computed column (User report scenario)
	sessID := "temp-table-sess"
	_, err = coord.ExecuteSessionSQL(sessID, "CREATE TABLE #t (qty INT, price INT, total AS (qty * price))")
	if err != nil {
		t.Fatalf("Create temp table #t failed: %v", err)
	}

	_, err = coord.ExecuteSessionSQL(sessID, "INSERT INTO #t (qty, price) VALUES (5, 8)")
	if err != nil {
		t.Fatalf("Insert into #t failed: %v", err)
	}

	resTemp, err := coord.ExecuteSessionSQL(sessID, "SELECT qty, price, total FROM #t")
	if err != nil {
		t.Fatalf("Select from #t failed: %v", err)
	}
	if len(resTemp.Rows) != 1 {
		t.Fatalf("expected 1 row from #t, got %d", len(resTemp.Rows))
	}

	totVal := resTemp.Rows[0]["total"]
	if totVal == nil {
		t.Fatalf("expected total = 40 in #t, got NULL")
	}
	t.Logf("Computed column in temp table #t: total = %v", totVal)
}
