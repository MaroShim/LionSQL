package api_test

import (
	"testing"
)

func TestSelectDistinct(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	// id column as explicit PK to avoid first-column PK inference on duplicates
	stmts := []string{
		"CREATE TABLE dist_test (id INT PRIMARY KEY, dept VARCHAR(50), salary INT)",
		"INSERT INTO dist_test VALUES (1, 'Engineering', 90000)",
		"INSERT INTO dist_test VALUES (2, 'Engineering', 95000)",
		"INSERT INTO dist_test VALUES (3, 'Engineering', 90000)", // same dept/salary as row 1
		"INSERT INTO dist_test VALUES (4, 'Marketing', 70000)",
		"INSERT INTO dist_test VALUES (5, 'Marketing', 70000)", // same dept/salary as row 4
		"INSERT INTO dist_test VALUES (6, 'HR', 65000)",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	t.Run("single column distinct", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT DISTINCT dept FROM dist_test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 distinct depts, got %d: %v", len(res.Rows), res.Rows)
		}
	})

	t.Run("multi column distinct", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT DISTINCT dept, salary FROM dist_test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Engineering/90000, Engineering/95000, Marketing/70000, HR/65000 = 4 unique pairs
		if len(res.Rows) != 4 {
			t.Fatalf("expected 4 distinct (dept,salary) pairs, got %d: %v", len(res.Rows), res.Rows)
		}
	})

	t.Run("distinct with where clause", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT DISTINCT dept FROM dist_test WHERE salary > 80000")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 distinct dept with salary > 80000, got %d: %v", len(res.Rows), res.Rows)
		}
	})

	t.Run("non-distinct returns all rows", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT dept FROM dist_test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.Rows) != 6 {
			t.Fatalf("expected 6 rows without DISTINCT, got %d", len(res.Rows))
		}
	})
}

func TestSelectDistinctNullHandling(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	stmts := []string{
		"CREATE TABLE null_dist (id INT PRIMARY KEY, val VARCHAR(50))",
		"INSERT INTO null_dist VALUES (1, 'alpha')",
		"INSERT INTO null_dist VALUES (2, 'alpha')", // same val, different pk
		"INSERT INTO null_dist VALUES (3, 'beta')",
		"INSERT INTO null_dist VALUES (4, NULL)",
		"INSERT INTO null_dist VALUES (5, NULL)", // same NULL val, different pk
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	t.Run("nulls deduplicated", func(t *testing.T) {
		res, err := server.ExecuteSQL("SELECT DISTINCT val FROM null_dist")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// 'alpha', 'beta', NULL = 3 distinct values
		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 distinct values (incl NULL), got %d: %v", len(res.Rows), res.Rows)
		}
	})
}
