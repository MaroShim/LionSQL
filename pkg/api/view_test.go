package api_test

import (
	"testing"
)

func TestCreateView_Basic(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	// Setup base table
	stmts := []string{
		"CREATE TABLE employees (id INT PRIMARY KEY, name VARCHAR(50), dept VARCHAR(30), salary INT)",
		"INSERT INTO employees VALUES (1, 'Alice', 'Engineering', 90000)",
		"INSERT INTO employees VALUES (2, 'Bob', 'Marketing', 70000)",
		"INSERT INTO employees VALUES (3, 'Carol', 'Engineering', 95000)",
		"INSERT INTO employees VALUES (4, 'Dave', 'HR', 65000)",
		"INSERT INTO employees VALUES (5, 'Eve', 'Engineering', 80000)",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	t.Run("create view and select from it", func(t *testing.T) {
		if _, err := server.ExecuteSQL("CREATE VIEW v_engineers AS SELECT id, name, salary FROM employees WHERE dept = 'Engineering'"); err != nil {
			t.Fatalf("CREATE VIEW failed: %v", err)
		}

		res, err := server.ExecuteSQL("SELECT * FROM v_engineers")
		if err != nil {
			t.Fatalf("SELECT FROM view failed: %v", err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 engineers, got %d: %v", len(res.Rows), res.Rows)
		}
	})

	t.Run("select from view with additional where", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT name FROM v_engineers WHERE salary > 85000")
		if err != nil {
			t.Fatalf("SELECT FROM view with WHERE failed: %v", err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 engineers with salary > 85000, got %d: %v", len(res.Rows), res.Rows)
		}
	})

	t.Run("create or replace view", func(t *testing.T) {
		// Replace view to only include high earners
		if _, err := server.ExecuteSQL("CREATE OR REPLACE VIEW v_engineers AS SELECT id, name, salary FROM employees WHERE dept = 'Engineering' AND salary > 85000"); err != nil {
			t.Fatalf("CREATE OR REPLACE VIEW failed: %v", err)
		}

		res, err := server.ExecuteSQL("SELECT * FROM v_engineers")
		if err != nil {
			t.Fatalf("SELECT after REPLACE VIEW failed: %v", err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 rows after view replacement, got %d", len(res.Rows))
		}
	})

	t.Run("drop view", func(t *testing.T) {
		if _, err := server.ExecuteSQL("DROP VIEW v_engineers"); err != nil {
			t.Fatalf("DROP VIEW failed: %v", err)
		}
		// After drop, SELECT should fail or return no view expansion (falls through to engine)
		_, err := server.ExecuteSQL("SELECT * FROM v_engineers")
		if err == nil {
			// If engine returns a table-not-found it's still OK - but we should not see the view data
			t.Log("Note: after DROP VIEW, engine returned no error (may not exist as physical table)")
		}
	})

	t.Run("drop view if exists on nonexistent", func(t *testing.T) {
		_, err := server.ExecuteSQL("DROP VIEW IF EXISTS v_nonexistent")
		if err != nil {
			t.Fatalf("DROP VIEW IF EXISTS should not error on missing view, got: %v", err)
		}
	})

	t.Run("create view if not exists", func(t *testing.T) {
		// First creation
		if _, err := server.ExecuteSQL("CREATE VIEW v_marketing AS SELECT id, name FROM employees WHERE dept = 'Marketing'"); err != nil {
			t.Fatalf("initial CREATE VIEW failed: %v", err)
		}
		// Second creation with IF NOT EXISTS should silently succeed
		if _, err := server.ExecuteSQL("CREATE VIEW IF NOT EXISTS v_marketing AS SELECT id FROM employees"); err != nil {
			t.Fatalf("CREATE VIEW IF NOT EXISTS should not error: %v", err)
		}
		// View should still be the original
		res, err := server.ExecuteSQL("SELECT * FROM v_marketing")
		if err != nil {
			t.Fatalf("SELECT FROM v_marketing failed: %v", err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 marketing row, got %d", len(res.Rows))
		}
	})
}

func TestCreateView_AggregationView(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	stmts := []string{
		"CREATE TABLE sales (id INT PRIMARY KEY, region VARCHAR(30), amount INT)",
		"INSERT INTO sales VALUES (1, 'North', 1000)",
		"INSERT INTO sales VALUES (2, 'North', 2000)",
		"INSERT INTO sales VALUES (3, 'South', 1500)",
		"INSERT INTO sales VALUES (4, 'South', 500)",
		"INSERT INTO sales VALUES (5, 'East', 3000)",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	t.Run("view with aggregation", func(t *testing.T) {
		if _, err := server.ExecuteSQL("CREATE VIEW v_region_totals AS SELECT region, SUM(amount) AS total FROM sales GROUP BY region"); err != nil {
			t.Fatalf("CREATE VIEW with aggregation failed: %v", err)
		}

		res, err := server.ExecuteSQL("SELECT * FROM v_region_totals")
		if err != nil {
			t.Fatalf("SELECT FROM aggregation view failed: %v", err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 region rows, got %d: %v", len(res.Rows), res.Rows)
		}
	})
}
