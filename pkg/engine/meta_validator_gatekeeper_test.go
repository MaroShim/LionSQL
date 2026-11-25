package engine

import (
	"strings"
	"testing"
)

func TestMetaValidatorAndBinder_ScopeIsolation(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_meta"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	initSQL := `
		CREATE TABLE t1 (id INT PRIMARY KEY, val INT);
		CREATE TABLE t2 (id INT PRIMARY KEY, score INT);
		INSERT INTO t1 VALUES (1, 10), (2, 20);
		INSERT INTO t2 VALUES (1, 100), (2, 200);
	`
	for _, stmt := range strings.Split(initSQL, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt != "" {
			if _, err := coord.ExecuteSessionSQL(sess, stmt); err != nil {
				t.Fatalf("setup failed on %q: %v", stmt, err)
			}
		}
	}

	testCases := []struct {
		name        string
		sql         string
		expectedErr string
	}{
		{
			name:        "WHERE count(*) > 0 disallowed",
			sql:         "SELECT * FROM t1 WHERE count(*) > 0",
			expectedErr: "aggregate functions are not allowed in WHERE",
		},
		{
			name:        "GROUP BY count(*) disallowed",
			sql:         "SELECT count(*) FROM t1 GROUP BY count(*)",
			expectedErr: "aggregate functions are not allowed in GROUP BY",
		},
		{
			name:        "JOIN ON count(*) > 0 disallowed",
			sql:         "SELECT * FROM t1 JOIN t2 ON count(*) > 0",
			expectedErr: "aggregate functions are not allowed in JOIN ON",
		},
		{
			name:        "GROUP BY ROW_NUMBER() OVER () disallowed",
			sql:         "SELECT count(*) FROM t1 GROUP BY ROW_NUMBER() OVER ()",
			expectedErr: "window functions are not allowed in GROUP BY",
		},
		{
			name:        "GROUP BY negative ordinal -1 disallowed",
			sql:         "SELECT count(*) FROM t1 GROUP BY -1",
			expectedErr: "GROUP BY position -1 is not in select list",
		},
		{
			name:        "HAVING without GROUP BY non-aggregate column projection disallowed",
			sql:         "SELECT id FROM t1 HAVING 1=1",
			expectedErr: "must appear in the GROUP BY clause or be used in an aggregate function",
		},
		{
			name:        "WHERE IN subquery missing table fail-closed",
			sql:         "SELECT * FROM t1 WHERE id IN (SELECT id FROM non_existent_tbl)",
			expectedErr: "table not found",
		},
		{
			name:        "Scalar subquery in projection missing table fail-closed",
			sql:         "SELECT (SELECT id FROM non_existent_tbl)",
			expectedErr: "table not found",
		},
		{
			name:        "EXISTS subquery missing table fail-closed",
			sql:         "SELECT 1 WHERE EXISTS (SELECT id FROM non_existent_tbl)",
			expectedErr: "table not found",
		},
		{
			name:        "UPDATE WHERE IN subquery missing table fail-closed",
			sql:         "UPDATE t1 SET val = 99 WHERE id IN (SELECT id FROM non_existent_tbl)",
			expectedErr: "table not found",
		},
		{
			name:        "DELETE WHERE IN subquery missing table fail-closed",
			sql:         "DELETE FROM t1 WHERE id IN (SELECT id FROM non_existent_tbl)",
			expectedErr: "table not found",
		},
		{
			name:        "UPDATE RETURNING non-existent column rejected",
			sql:         "UPDATE t1 SET val = 10 WHERE id = 1 RETURNING ghost_col",
			expectedErr: "column \"ghost_col\" does not exist",
		},
		{
			name:        "INSERT VALUES ghost column rejected",
			sql:         "INSERT INTO t1 (id, val) VALUES (3, ghost_col)",
			expectedErr: "column \"ghost_col\" does not exist",
		},
		{
			name:        "JOIN ORDER BY ambiguous column rejected",
			sql:         "SELECT t1.id FROM t1 JOIN t2 ON t1.id = t2.id ORDER BY id",
			expectedErr: "column reference \"id\" is ambiguous",
		},
		{
			name:        "JOIN GROUP BY ambiguous column rejected",
			sql:         "SELECT count(*) FROM t1 JOIN t2 ON t1.id = t2.id GROUP BY id",
			expectedErr: "column reference \"id\" is ambiguous",
		},
		{
			name:        "Division by zero error",
			sql:         "SELECT val / 0 FROM t1",
			expectedErr: "division by zero",
		},
		{
			name:        "WHERE arithmetic overflow fail-closed",
			sql:         "SELECT * FROM t1 WHERE (val * 1000000000000000000) > 0",
			expectedErr: "arithmetic overflow error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := coord.ExecuteSessionSQL(sess, tc.sql)
			if err == nil {
				t.Fatalf("[%s] expected error containing %q, got nil", tc.name, tc.expectedErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.expectedErr)) {
				t.Fatalf("[%s] expected error containing %q, got: %v", tc.name, tc.expectedErr, err)
			}
		})
	}

	// Positive test: valid aggregate projection under HAVING without GROUP BY must succeed
	{
		validSQL := "SELECT SUM(val) FROM t1 HAVING SUM(val) >= 0;"
		res, err := coord.ExecuteSessionSQL(sess, validSQL)
		if err != nil {
			t.Fatalf("valid aggregate projection under HAVING failed: %v", err)
		}
		if res == nil {
			t.Fatalf("expected non-nil result for valid aggregate projection under HAVING")
		}
	}
}

func TestBinder_Phase1_ShapeAndArityValidation(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_binder_p1"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	setupSQLs := []string{
		"CREATE TABLE t_arity (id INT PRIMARY KEY, name VARCHAR(50), score INT);",
		"INSERT INTO t_arity VALUES (1, 'Alice', 100);",
		"CREATE TABLE t_arity_dst (id INT PRIMARY KEY, name VARCHAR(50));",
	}
	for _, sql := range setupSQLs {
		if _, err := coord.ExecuteSessionSQL(sess, sql); err != nil {
			t.Fatalf("setup failed on %q: %v", sql, err)
		}
	}

	testCases := []struct {
		name        string
		sql         string
		expectedErr string
	}{
		{
			name:        "INSERT columns count < values count",
			sql:         "INSERT INTO t_arity (id, name) VALUES (2, 'Bob', 200);",
			expectedErr: "column count mismatch: expected 2 values, got 3",
		},
		{
			name:        "INSERT columns count > values count",
			sql:         "INSERT INTO t_arity (id, name, score) VALUES (2, 'Bob');",
			expectedErr: "column count mismatch: expected 3 values, got 2",
		},
		{
			name:        "INSERT more expressions than target columns without col list",
			sql:         "INSERT INTO t_arity VALUES (2, 'Bob', 200, 'extra');",
			expectedErr: "INSERT has more expressions than target columns",
		},
		{
			name:        "INSERT fewer expressions than target columns without col list",
			sql:         "INSERT INTO t_arity VALUES (2, 'Bob');",
			expectedErr: "column count mismatch: expected 3 values, got 2",
		},
		{
			name:        "INSERT multi-tuple second tuple arity mismatch",
			sql:         "INSERT INTO t_arity (id, name, score) VALUES (2, 'Bob', 200), (3, 'Charlie');",
			expectedErr: "column count mismatch: expected 3 values, got 2",
		},
		{
			name:        "INSERT ... SELECT target columns mismatch",
			sql:         "INSERT INTO t_arity_dst SELECT id, name, score FROM t_arity;",
			expectedErr: "column count mismatch: target table \"t_arity_dst\" expects 2 columns, but SELECT produced 3 columns",
		},
		{
			name:        "WHERE IN multi-column subquery arity violation",
			sql:         "SELECT * FROM t_arity WHERE id IN (SELECT id, name FROM t_arity);",
			expectedErr: "subquery must return only one column",
		},
		{
			name:        "INSERT into virtual system catalog view rejected",
			sql:         "INSERT INTO sys.tables (name) VALUES ('hacked');",
			expectedErr: "cannot modify system catalog view",
		},
		{
			name:        "UPDATE virtual system catalog view rejected",
			sql:         "UPDATE sys.tables SET name = 'hacked' WHERE 1=1;",
			expectedErr: "cannot modify system catalog view",
		},
		{
			name:        "DELETE from virtual system catalog view rejected",
			sql:         "DELETE FROM sys.columns WHERE 1=1;",
			expectedErr: "cannot modify system catalog view",
		},
		{
			name:        "FROM subquery without alias rejected",
			sql:         "SELECT id FROM (SELECT id FROM t_arity);",
			expectedErr: "subquery in FROM must have an alias",
		},
		{
			name:        "HAVING without GROUP BY with unaggregated column in composite projection rejected",
			sql:         "SELECT SUM(score) + name FROM t_arity HAVING SUM(score) > 10;",
			expectedErr: "must appear in the GROUP BY clause or be used in an aggregate function",
		},
		{
			name:        "JOIN ON with window function disallowed",
			sql:         "SELECT * FROM t_arity t1 JOIN t_arity t2 ON ROW_NUMBER() OVER () = 1;",
			expectedErr: "window functions are not allowed in JOIN ON",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := coord.ExecuteSessionSQL(sess, tc.sql)
			if err == nil {
				t.Fatalf("[%s] expected error containing %q, got nil", tc.name, tc.expectedErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.expectedErr)) {
				t.Fatalf("[%s] expected error containing %q, got: %v", tc.name, tc.expectedErr, err)
			}
		})
	}

	// Positive test: valid aggregate projection under HAVING without GROUP BY must succeed
	{
		validSQL := "SELECT SUM(score) FROM t_arity HAVING SUM(score) >= 0;"
		res, err := coord.ExecuteSessionSQL(sess, validSQL)
		if err != nil {
			t.Fatalf("valid aggregate projection under HAVING failed: %v", err)
		}
		if res == nil {
			t.Fatalf("expected non-nil result for valid aggregate projection under HAVING")
		}
	}
}

func TestBinder_Phase2_IdentifierAndScopeBinding(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_binder_p2"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	setupSQLs := []string{
		"CREATE TABLE employees (id INT PRIMARY KEY, name VARCHAR(50), dept_id INT);",
		"CREATE TABLE departments (id INT PRIMARY KEY, dept_name VARCHAR(50));",
		"INSERT INTO employees VALUES (1, 'Alice', 10);",
		"INSERT INTO departments VALUES (10, 'Engineering');",
	}
	for _, sql := range setupSQLs {
		if _, err := coord.ExecuteSessionSQL(sess, sql); err != nil {
			t.Fatalf("setup failed for %q: %v", sql, err)
		}
	}

	testCases := []struct {
		name        string
		sql         string
		expectedErr string
	}{
		{
			name:        "SELECT unqualified non-existent ghost column rejected",
			sql:         "SELECT ghost_col FROM employees;",
			expectedErr: "column \"ghost_col\" does not exist",
		},
		{
			name:        "SELECT qualified non-existent ghost column on valid alias rejected",
			sql:         "SELECT e.ghost_col FROM employees e;",
			expectedErr: "does not exist in table or alias",
		},
		{
			name:        "SELECT qualified column on non-existent table alias rejected",
			sql:         "SELECT e.id FROM employees x;",
			expectedErr: "does not exist in query scope",
		},
		{
			name:        "WHERE unqualified non-existent ghost column rejected",
			sql:         "SELECT * FROM employees WHERE ghost_col = 1;",
			expectedErr: "column \"ghost_col\" does not exist",
		},
		{
			name:        "WHERE qualified non-existent column rejected",
			sql:         "SELECT * FROM employees e WHERE e.ghost_col = 1;",
			expectedErr: "does not exist in table or alias",
		},
		{
			name:        "WHERE qualified column on wrong alias rejected",
			sql:         "SELECT * FROM employees x WHERE e.id = 1;",
			expectedErr: "does not exist in query scope",
		},
		{
			name:        "ORDER BY non-existent column rejected",
			sql:         "SELECT id FROM employees ORDER BY ghost_col;",
			expectedErr: "column \"ghost_col\" does not exist",
		},
		{
			name:        "GROUP BY non-existent column rejected",
			sql:         "SELECT count(*) FROM employees GROUP BY ghost_col;",
			expectedErr: "column \"ghost_col\" does not exist",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := coord.ExecuteSessionSQL(sess, tc.sql)
			if err == nil {
				t.Fatalf("[%s] expected error containing %q, got nil", tc.name, tc.expectedErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.expectedErr)) {
				t.Fatalf("[%s] expected error containing %q, got: %v", tc.name, tc.expectedErr, err)
			}
		})
	}

	// Positive tests: valid qualified columns and projection aliases in ORDER BY
	positiveSQLs := []string{
		"SELECT e.id, e.name FROM employees e;",
		"SELECT id, name FROM employees;",
		"SELECT id AS my_alias FROM employees ORDER BY my_alias;",
		"SELECT e.id FROM employees e WHERE e.id = 1;",
	}
	for _, sql := range positiveSQLs {
		res, err := coord.ExecuteSessionSQL(sess, sql)
		if err != nil {
			t.Fatalf("valid SQL %q failed: %v", sql, err)
		}
		if res == nil {
			t.Fatalf("expected non-nil result for %q", sql)
		}
	}
}

func TestBinder_Phase3_JoinScopeAndAmbiguity(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_binder_p3"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	// Create two tables with overlapping column 'id' and 'val'
	setupSQLs := []string{
		"CREATE TABLE t1 (id INT PRIMARY KEY, name VARCHAR(50), val INT);",
		"CREATE TABLE t2 (id INT PRIMARY KEY, dept VARCHAR(50), val INT);",
		"INSERT INTO t1 VALUES (1, 'Alice', 100);",
		"INSERT INTO t2 VALUES (1, 'Engineering', 200);",
	}
	for _, sql := range setupSQLs {
		if _, err := coord.ExecuteSessionSQL(sess, sql); err != nil {
			t.Fatalf("setup failed for %q: %v", sql, err)
		}
	}

	testCases := []struct {
		name        string
		sql         string
		expectedErr string
	}{
		{
			name:        "JOIN ON unqualified ambiguous column rejected",
			sql:         "SELECT * FROM t1 JOIN t2 ON id = id;",
			expectedErr: "ambiguous",
		},
		{
			name:        "JOIN ON non-existent table alias rejected",
			sql:         "SELECT * FROM t1 JOIN t2 ON t3.id = t2.id;",
			expectedErr: "does not exist in query scope",
		},
		{
			name:        "JOIN ON non-existent column on valid table rejected",
			sql:         "SELECT * FROM t1 JOIN t2 ON t1.ghost_col = t2.id;",
			expectedErr: "does not exist in table or alias",
		},
		{
			name:        "JOIN ON window function disallowed",
			sql:         "SELECT * FROM t1 JOIN t2 ON ROW_NUMBER() OVER () = 1;",
			expectedErr: "window functions are not allowed in JOIN ON",
		},
		{
			name:        "JOIN ON aggregate function disallowed",
			sql:         "SELECT * FROM t1 JOIN t2 ON count(*) > 0;",
			expectedErr: "aggregate functions are not allowed in JOIN ON",
		},
		{
			name:        "Chained JOIN referencing table not yet joined rejected",
			sql:         "SELECT * FROM t1 JOIN t2 ON t1.id = t3.id JOIN t1 t3 ON t2.id = t3.id;",
			expectedErr: "does not exist in query scope",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := coord.ExecuteSessionSQL(sess, tc.sql)
			if err == nil {
				t.Fatalf("[%s] expected error containing %q, got nil", tc.name, tc.expectedErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.expectedErr)) {
				t.Fatalf("[%s] expected error containing %q, got: %v", tc.name, tc.expectedErr, err)
			}
		})
	}

	// Positive tests
	positiveSQLs := []string{
		"SELECT t1.id, t2.id FROM t1 JOIN t2 ON t1.id = t2.id;",
		"SELECT a.id, b.dept FROM t1 a JOIN t2 b ON a.id = b.id WHERE a.val > 50;",
	}
	for _, sql := range positiveSQLs {
		res, err := coord.ExecuteSessionSQL(sess, sql)
		if err != nil {
			t.Fatalf("valid SQL %q failed: %v", sql, err)
		}
		if res == nil {
			t.Fatalf("expected non-nil result for %q", sql)
		}
	}
}
