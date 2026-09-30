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
