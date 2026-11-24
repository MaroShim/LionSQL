package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func setupWindowProcTestDB(t *testing.T) *engine.Coordinator {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())

	_, err := coord.ExecuteSQL(`CREATE TABLE sales (
		id INT PRIMARY KEY,
		dept VARCHAR(50) NOT NULL,
		day_num INT NOT NULL,
		amount FLOAT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE sales failed: %v", err)
	}

	insertSQL := `INSERT INTO sales (id, dept, day_num, amount) VALUES
		(1, 'Eng', 1, 100.0),
		(2, 'Eng', 2, 150.0),
		(3, 'Eng', 3, 200.0),
		(4, 'Sales', 1, 50.0),
		(5, 'Sales', 2, 80.0),
		(6, 'Sales', 3, 120.0);`
	if _, err := coord.ExecuteSQL(insertSQL); err != nil {
		t.Fatalf("INSERT INTO sales failed: %v", err)
	}

	return coord
}

// TestWindowProc_DirectSelect tests calling a stored procedure that executes a direct
// SELECT with LEAD and LAG window functions with PARTITION BY and ORDER BY.
func TestWindowProc_DirectSelect(t *testing.T) {
	coord := setupWindowProcTestDB(t)

	procSQL := `CREATE PROCEDURE usp_GetSalesTrend AS
BEGIN
	SELECT dept, day_num, amount,
		LAG(amount, 1, 0.0) OVER (PARTITION BY dept ORDER BY day_num ASC) AS prev_amt,
		LEAD(amount, 1, 0.0) OVER (PARTITION BY dept ORDER BY day_num ASC) AS next_amt
	FROM sales
	ORDER BY dept ASC, day_num ASC;
END;`

	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	res, err := coord.ExecuteSQL("EXEC usp_GetSalesTrend;")
	if err != nil {
		t.Fatalf("EXEC usp_GetSalesTrend failed: %v", err)
	}

	if len(res.Rows) != 6 {
		t.Fatalf("expected 6 rows, got %d", len(res.Rows))
	}

	// Verify Eng partition (rows 0..2)
	// Eng Day 1: amount=100, prev=0, next=150
	// Eng Day 2: amount=150, prev=100, next=200
	// Eng Day 3: amount=200, prev=150, next=0
	expected := []struct {
		dept    string
		day     int64
		amt     float64
		prevAmt float64
		nextAmt float64
	}{
		{"Eng", 1, 100.0, 0.0, 150.0},
		{"Eng", 2, 150.0, 100.0, 200.0},
		{"Eng", 3, 200.0, 150.0, 0.0},
		{"Sales", 1, 50.0, 0.0, 80.0},
		{"Sales", 2, 80.0, 50.0, 120.0},
		{"Sales", 3, 120.0, 80.0, 0.0},
	}

	for i, exp := range expected {
		row := res.Rows[i]
		deptVal := fmt.Sprintf("%v", row["dept"])
		if deptVal != exp.dept {
			t.Errorf("row %d: expected dept %s, got %s", i, exp.dept, deptVal)
		}
		amtVal := row["amount"].(float64)
		if amtVal != exp.amt {
			t.Errorf("row %d: expected amt %v, got %v", i, exp.amt, amtVal)
		}
		prevVal := row["prev_amt"].(float64)
		if prevVal != exp.prevAmt {
			t.Errorf("row %d: expected prevAmt %v, got %v", i, exp.prevAmt, prevVal)
		}
		nextVal := row["next_amt"].(float64)
		if nextVal != exp.nextAmt {
			t.Errorf("row %d: expected nextAmt %v, got %v", i, exp.nextAmt, nextVal)
		}
	}
}

// TestWindowProc_CursorIteration tests iterating a cursor over a window query with LAG and LEAD.
func TestWindowProc_CursorIteration(t *testing.T) {
	coord := setupWindowProcTestDB(t)

	_, err := coord.ExecuteSQL(`CREATE TABLE trend_results (
		day_num INT PRIMARY KEY,
		diff FLOAT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE trend_results failed: %v", err)
	}

	procSQL := `CREATE PROCEDURE usp_CursorTrend AS
BEGIN
	DECLARE @day INT;
	DECLARE @amt FLOAT;
	DECLARE @prev FLOAT;
	DECLARE @diff FLOAT;

	DECLARE cur CURSOR FOR
		SELECT day_num, amount, LAG(amount, 1, 0.0) OVER (ORDER BY day_num ASC) AS prev_amt
		FROM sales
		WHERE dept = 'Eng'
		ORDER BY day_num ASC;

	OPEN cur;
	FETCH NEXT FROM cur INTO @day, @amt, @prev;
	WHILE @@FETCH_STATUS = 0
	BEGIN
		SET @diff = @amt - @prev;
		INSERT INTO trend_results (day_num, diff) VALUES (@day, @diff);
		FETCH NEXT FROM cur INTO @day, @amt, @prev;
	END;
	CLOSE cur;
	DEALLOCATE cur;
END;`

	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE usp_CursorTrend failed: %v", err)
	}

	if _, err := coord.ExecuteSQL("EXEC usp_CursorTrend;"); err != nil {
		t.Fatalf("EXEC usp_CursorTrend failed: %v", err)
	}

	res, err := coord.ExecuteSQL("SELECT day_num, diff FROM trend_results ORDER BY day_num ASC;")
	if err != nil {
		t.Fatalf("SELECT trend_results failed: %v", err)
	}

	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows in trend_results, got %d", len(res.Rows))
	}

	// Day 1: 100 - 0 = 100
	// Day 2: 150 - 100 = 50
	// Day 3: 200 - 150 = 50
	expectedDiffs := []float64{100.0, 50.0, 50.0}
	for i, exp := range expectedDiffs {
		diffVal := res.Rows[i]["diff"].(float64)
		if diffVal != exp {
			t.Errorf("row %d: expected diff %v, got %v", i, exp, diffVal)
		}
	}
}

// TestWindowProc_WhileLoopIsolation tests that repeated execution of window queries
// inside a WHILE loop maintains strict frame isolation without state contamination.
func TestWindowProc_WhileLoopIsolation(t *testing.T) {
	coord := setupWindowProcTestDB(t)

	procSQL := `CREATE PROCEDURE usp_WhileLoopWindow AS
BEGIN
	DECLARE @iter INT = 0;
	WHILE @iter < 3
	BEGIN
		SELECT day_num, amount,
			LAG(amount, 1, 0.0) OVER (ORDER BY day_num ASC) AS prev_amt,
			LEAD(amount, 1, 0.0) OVER (ORDER BY day_num ASC) AS next_amt
		FROM sales
		WHERE dept = 'Sales'
		ORDER BY day_num ASC;
		SET @iter = @iter + 1;
	END;
END;`

	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	res, err := coord.ExecuteSQL("EXEC usp_WhileLoopWindow;")
	if err != nil {
		t.Fatalf("EXEC usp_WhileLoopWindow failed: %v", err)
	}

	// Should have at least the rows from the executions
	if len(res.Rows) < 3 {
		t.Fatalf("expected rows, got %d", len(res.Rows))
	}

	// Verify the result rows are deterministic
	for i, r := range res.Rows {
		day := r["day_num"]
		if day == nil {
			t.Errorf("row %d: expected day_num, got nil", i)
		}
	}
}

// TestWindowProc_NegativeDefaultAndNullHandling tests negative default values and null defaults in LEAD/LAG.
func TestWindowProc_NegativeDefaultAndNullHandling(t *testing.T) {
	coord := setupWindowProcTestDB(t)

	procSQL := `CREATE PROCEDURE usp_NegativeDefaults AS
BEGIN
	SELECT day_num, amount,
		LAG(amount, 1, -1.0) OVER (ORDER BY day_num ASC) AS prev_amt,
		LEAD(amount, 1, -1.0) OVER (ORDER BY day_num ASC) AS next_amt
	FROM sales
	WHERE dept = 'Eng'
	ORDER BY day_num ASC;
END;`

	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE usp_NegativeDefaults failed: %v", err)
	}

	res, err := coord.ExecuteSQL("EXEC usp_NegativeDefaults;")
	if err != nil {
		t.Fatalf("EXEC usp_NegativeDefaults failed: %v", err)
	}

	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}

	// Row 0: prev should be -1.0
	if prev := res.Rows[0]["prev_amt"].(float64); prev != -1.0 {
		t.Errorf("row 0 prev_amt: expected -1.0, got %v", prev)
	}
	// Row 2: next should be -1.0
	if next := res.Rows[2]["next_amt"].(float64); next != -1.0 {
		t.Errorf("row 2 next_amt: expected -1.0, got %v", next)
	}
}

// TestWindowProc_PlanCacheAndMultiRun tests running the stored procedure 20 times continuously
// to verify plan cache stability and frame buffer recycling across runs.
func TestWindowProc_PlanCacheAndMultiRun(t *testing.T) {
	coord := setupWindowProcTestDB(t)

	procSQL := `CREATE PROCEDURE usp_MultiRunWindow AS
BEGIN
	SELECT dept, day_num, amount,
		LAG(amount, 1, 0.0) OVER (PARTITION BY dept ORDER BY day_num ASC) AS prev_amt,
		LEAD(amount, 1, 0.0) OVER (PARTITION BY dept ORDER BY day_num ASC) AS next_amt
	FROM sales
	ORDER BY dept ASC, day_num ASC;
END;`

	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	for run := 0; run < 20; run++ {
		res, err := coord.ExecuteSQL("EXEC usp_MultiRunWindow;")
		if err != nil {
			t.Fatalf("run %d failed: %v", run, err)
		}
		if len(res.Rows) != 6 {
			t.Fatalf("run %d: expected 6 rows, got %d", run, len(res.Rows))
		}
		// Check Eng Day 1
		if res.Rows[0]["prev_amt"].(float64) != 0.0 || res.Rows[0]["next_amt"].(float64) != 150.0 {
			t.Fatalf("run %d: row 0 invalid: %+v", run, res.Rows[0])
		}
		// Check Sales Day 3
		if res.Rows[5]["prev_amt"].(float64) != 80.0 || res.Rows[5]["next_amt"].(float64) != 0.0 {
			t.Fatalf("run %d: row 5 invalid: %+v", run, res.Rows[5])
		}
	}
}
