package api_test

import (
	"testing"
)

func TestCompositeIndex_PrefixScan(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	// Setup: table with composite index on (dept, level)
	stmts := []string{
		"CREATE TABLE emp_ci (id INT PRIMARY KEY, dept VARCHAR(30), level INT, salary INT)",
		"CREATE INDEX idx_dept_level ON emp_ci (dept, level)",
		"INSERT INTO emp_ci VALUES (1, 'Engineering', 3, 90000)",
		"INSERT INTO emp_ci VALUES (2, 'Engineering', 2, 75000)",
		"INSERT INTO emp_ci VALUES (3, 'Engineering', 3, 95000)",
		"INSERT INTO emp_ci VALUES (4, 'Marketing', 2, 70000)",
		"INSERT INTO emp_ci VALUES (5, 'Marketing', 3, 80000)",
		"INSERT INTO emp_ci VALUES (6, 'HR', 1, 60000)",
		"INSERT INTO emp_ci VALUES (7, 'Engineering', 1, 65000)",
		"INSERT INTO emp_ci VALUES (8, 'HR', 2, 62000)",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	t.Run("single column prefix (dept only)", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT id FROM emp_ci WHERE dept = 'Engineering'")
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		if len(res.Rows) != 4 {
			t.Fatalf("expected 4 Engineering rows, got %d: %v", len(res.Rows), res.Rows)
		}
	})

	t.Run("two-column prefix (dept AND level)", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT id FROM emp_ci WHERE dept = 'Engineering' AND level = 3")
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 Engineering level-3 rows, got %d: %v", len(res.Rows), res.Rows)
		}
	})

	t.Run("two-column prefix Marketing level 2", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT id FROM emp_ci WHERE dept = 'Marketing' AND level = 2")
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 Marketing level-2 row, got %d: %v", len(res.Rows), res.Rows)
		}
	})

	t.Run("non-existent combination returns empty", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT id FROM emp_ci WHERE dept = 'HR' AND level = 3")
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		if len(res.Rows) != 0 {
			t.Fatalf("expected 0 HR level-3 rows, got %d: %v", len(res.Rows), res.Rows)
		}
	})

	t.Run("full table scan unaffected", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT id FROM emp_ci WHERE salary >= 80000")
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 rows with salary >= 80000, got %d", len(res.Rows))
		}
	})
}

func TestCompositeIndex_ThreeColumn(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	stmts := []string{
		"CREATE TABLE orders_ci (id INT PRIMARY KEY, region VARCHAR(20), year INT, month INT, amount INT)",
		"CREATE INDEX idx_region_year_month ON orders_ci (region, year, month)",
		"INSERT INTO orders_ci VALUES (1, 'North', 2024, 1, 1000)",
		"INSERT INTO orders_ci VALUES (2, 'North', 2024, 2, 2000)",
		"INSERT INTO orders_ci VALUES (3, 'North', 2023, 1, 500)",
		"INSERT INTO orders_ci VALUES (4, 'South', 2024, 1, 1500)",
		"INSERT INTO orders_ci VALUES (5, 'South', 2024, 2, 2500)",
		"INSERT INTO orders_ci VALUES (6, 'East',  2024, 1, 3000)",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	t.Run("two-column prefix (region, year)", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT id FROM orders_ci WHERE region = 'North' AND year = 2024")
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 North-2024 rows, got %d: %v", len(res.Rows), res.Rows)
		}
	})

	t.Run("three-column prefix (region, year, month)", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT id FROM orders_ci WHERE region = 'North' AND year = 2024 AND month = 1")
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 North-2024-Jan row, got %d: %v", len(res.Rows), res.Rows)
		}
	})
}

func TestCompositeIndex_RangeScan(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	stmts := []string{
		"CREATE TABLE emp_range (id INT PRIMARY KEY, dept VARCHAR(30), level INT, salary INT)",
		"CREATE INDEX idx_dept_lvl ON emp_range (dept, level)",
		"INSERT INTO emp_range VALUES (1, 'Engineering', 1, 60000)",
		"INSERT INTO emp_range VALUES (2, 'Engineering', 2, 70000)",
		"INSERT INTO emp_range VALUES (3, 'Engineering', 3, 80000)",
		"INSERT INTO emp_range VALUES (4, 'Engineering', 4, 90000)",
		"INSERT INTO emp_range VALUES (5, 'Engineering', 5, 100000)",
		"INSERT INTO emp_range VALUES (6, 'Marketing',   2, 72000)",
		"INSERT INTO emp_range VALUES (7, 'Marketing',   4, 92000)",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	t.Run("prefix eq and range gte (level >= 3)", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT id, level FROM emp_range WHERE dept = 'Engineering' AND level >= 3")
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 rows (level 3, 4, 5), got %d: %v", len(res.Rows), res.Rows)
		}
	})

	t.Run("prefix eq and range gt (level > 3)", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT id, level FROM emp_range WHERE dept = 'Engineering' AND level > 3")
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 rows (level 4, 5), got %d: %v", len(res.Rows), res.Rows)
		}
	})

	t.Run("prefix eq and range lte (level <= 2)", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT id, level FROM emp_range WHERE dept = 'Engineering' AND level <= 2")
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 rows (level 1, 2), got %d: %v", len(res.Rows), res.Rows)
		}
	})

	t.Run("prefix eq and between (level BETWEEN 2 AND 4)", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT id, level FROM emp_range WHERE dept = 'Engineering' AND level BETWEEN 2 AND 4")
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 rows (level 2, 3, 4), got %d: %v", len(res.Rows), res.Rows)
		}
	})
}

func TestCompositeIndex_DescendingScan(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	stmts := []string{
		"CREATE TABLE emp_desc (id INT PRIMARY KEY, dept VARCHAR(30), level INT, salary INT)",
		"CREATE INDEX idx_dept_lvl_desc ON emp_desc (dept, level)",
		"INSERT INTO emp_desc VALUES (1, 'Engineering', 1, 60000)",
		"INSERT INTO emp_desc VALUES (2, 'Engineering', 2, 70000)",
		"INSERT INTO emp_desc VALUES (3, 'Engineering', 3, 80000)",
		"INSERT INTO emp_desc VALUES (4, 'Engineering', 4, 90000)",
		"INSERT INTO emp_desc VALUES (5, 'Engineering', 5, 100000)",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	t.Run("order by level DESC with fixed prefix", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT id, level FROM emp_desc WHERE dept = 'Engineering' ORDER BY level DESC")
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if len(res.Rows) != 5 {
			t.Fatalf("expected 5 rows, got %d", len(res.Rows))
		}
		// Verify strict descending order
		expectedLevels := []int64{5, 4, 3, 2, 1}
		for i, row := range res.Rows {
			lvl := row["level"]
			switch v := lvl.(type) {
			case int64:
				if v != expectedLevels[i] {
					t.Errorf("row %d: expected level %d, got %d", i, expectedLevels[i], v)
				}
			case float64:
				if int64(v) != expectedLevels[i] {
					t.Errorf("row %d: expected level %d, got %v", i, expectedLevels[i], v)
				}
			default:
				t.Errorf("row %d: unexpected level type %T (%v)", i, lvl, lvl)
			}
		}
	})

	t.Run("order by level DESC combined with range condition (level BETWEEN 2 AND 4)", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT id, level FROM emp_desc WHERE dept = 'Engineering' AND level BETWEEN 2 AND 4 ORDER BY level DESC")
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 rows, got %d", len(res.Rows))
		}
		expectedLevels := []int64{4, 3, 2}
		for i, row := range res.Rows {
			lvl := row["level"]
			switch v := lvl.(type) {
			case int64:
				if v != expectedLevels[i] {
					t.Errorf("row %d: expected level %d, got %d", i, expectedLevels[i], v)
				}
			case float64:
				if int64(v) != expectedLevels[i] {
					t.Errorf("row %d: expected level %d, got %v", i, expectedLevels[i], v)
				}
			}
		}
	})
}
