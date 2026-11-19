package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestPreventative_SQLStandard_Regression tests SQL standard invariants
// to catch and prevent regressions before they reach production.
func TestPreventative_SQLStandard_Regression(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "preventative_sess"
	_, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	toString := func(v any) string {
		if v == nil {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}

	// [TEST 1] Three-Valued Logic: NOT IN with NULL must yield UNKNOWN (0 rows returned)
	t.Run("ThreeValuedLogic_NOT_IN_With_NULL", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
			CREATE TABLE t_3vl (id INT, val INT);
			INSERT INTO t_3vl VALUES (1, 10), (2, 20), (3, 30);
		`)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}

		// 1) Literal list containing NULL: val NOT IN (10, NULL) -> must return 0 rows
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id FROM t_3vl WHERE val NOT IN (10, NULL);")
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if len(res.Rows) != 0 {
			t.Errorf("expected 0 rows for val NOT IN (10, NULL), got %d rows: %+v", len(res.Rows), res.Rows)
		}

		// 2) Subquery returning NULL: subquery returns {10, NULL} -> main query must return 0 rows
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `
			CREATE TABLE t_filter (bad_val INT);
			INSERT INTO t_filter VALUES (10), (NULL);
		`)
		if err != nil {
			t.Fatalf("setup t_filter failed: %v", err)
		}

		res2, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id FROM t_3vl WHERE val NOT IN (SELECT bad_val FROM t_filter);")
		if err != nil {
			t.Fatalf("subquery NOT IN failed: %v", err)
		}
		if len(res2.Rows) != 0 {
			t.Errorf("expected 0 rows for NOT IN (subquery with NULL), got %d rows: %+v", len(res2.Rows), res2.Rows)
		}
	})

	// [TEST 2] Strict Full GROUP BY: Prohibiting unaggregated non-group-by columns
	t.Run("Strict_Full_GroupBy_NonAgg_Column_Rejected", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
			CREATE TABLE t_emp (dept INT, name VARCHAR(20), salary INT);
			INSERT INTO t_emp VALUES (1, 'Alice', 100), (1, 'Bob', 200), (2, 'Charlie', 300);
		`)
		if err != nil {
			t.Fatalf("setup t_emp failed: %v", err)
		}

		// Unaggregated column 'name' not in GROUP BY -> must fail with ERROR 42803 or Msg 8120
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT dept, name, SUM(salary) FROM t_emp GROUP BY dept;")
		if err == nil {
			t.Errorf("expected error for unaggregated column 'name' in SELECT with GROUP BY, but succeeded")
		} else {
			t.Logf("correctly rejected full group by violation: %v", err)
		}

		// HAVING referring to unaggregated column 'salary' not in GROUP BY -> must fail
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT dept, SUM(salary) FROM t_emp GROUP BY dept HAVING salary > 150;")
		if err == nil {
			t.Errorf("expected error for unaggregated column 'salary' in HAVING with GROUP BY, but succeeded")
		} else {
			t.Logf("correctly rejected HAVING unaggregated column: %v", err)
		}
	})

	// [TEST 3] Window Function: Reject invalid frame specification (inverted frame) and negative LEAD/LAG offset
	t.Run("Window_Function_Invalid_Frame_And_Offset", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
			CREATE TABLE t_win (id INT, v INT);
			INSERT INTO t_win VALUES (1, 10), (2, 20), (3, 30);
		`)
		if err != nil {
			t.Fatalf("setup t_win failed: %v", err)
		}

		// 1) Inverted frame: CURRENT ROW AND UNBOUNDED PRECEDING
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, SUM(v) OVER (ORDER BY id ROWS BETWEEN CURRENT ROW AND UNBOUNDED PRECEDING) FROM t_win;")
		if err == nil {
			t.Errorf("expected error for inverted window frame (CURRENT ROW AND UNBOUNDED PRECEDING), but succeeded")
		} else {
			t.Logf("correctly rejected inverted frame: %v", err)
		}

		// 2) Negative offset for LAG/LEAD
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, LAG(v, -1) OVER (ORDER BY id) FROM t_win;")
		if err == nil {
			t.Errorf("expected error for negative LAG offset, but succeeded")
		} else {
			t.Logf("correctly rejected negative LAG offset: %v", err)
		}
	})

	// [TEST 4] Arithmetic Overflow & Division by Zero Guard
	t.Run("Arithmetic_Overflow_And_DivisionByZero", func(t *testing.T) {
		// Division by zero
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT 100 / 0;")
		if err == nil {
			t.Errorf("expected error for division by zero (SELECT 100 / 0), but succeeded")
		} else {
			t.Logf("correctly caught division by zero: %v", err)
		}

		// 32-bit INT overflow: 2147483647 + 1
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT CAST(2147483647 AS INT) + 1 AS r;")
		if err != nil {
			t.Logf("overflow raised error: %v", err)
		} else if len(res.Rows) > 0 {
			val := res.Rows[0]["r"]
			// If it returned, it should have promoted to int64 (2147483648), NOT wrapped around to -2147483648
			if strVal := strings.TrimSpace(toString(val)); strVal == "-2147483648" {
				t.Errorf("32-bit INT overflow wrapped around silently to -2147483648 instead of promotion or error")
			}
		}
	})

	// [TEST 5] Strict Type Casting: Garbage string to integer must fail
	t.Run("Strict_Type_Casting_Garbage_String_To_Int", func(t *testing.T) {
		// Non-numeric alphanumeric string
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT CAST('123abc' AS INT) AS r;")
		if err == nil {
			t.Errorf("expected error casting '123abc' to INT, but succeeded")
		} else {
			t.Logf("correctly caught invalid cast: %v", err)
		}

		// Empty string to INT
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT CAST('' AS INT) AS r;")
		if err == nil {
			t.Errorf("expected error casting '' to INT, but succeeded")
		} else {
			t.Logf("correctly caught empty string cast: %v", err)
		}
	})

	// [TEST 6] Transactional DDL Rollback: Failed DDL in transaction leaves zero catalog corruption
	t.Run("Transactional_DDL_Failed_State_Cleanup", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;")
		if err != nil {
			t.Fatalf("BEGIN failed: %v", err)
		}

		// Valid DDL inside transaction
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_tx_ddl (id INT PRIMARY KEY);")
		if err != nil {
			t.Fatalf("CREATE TABLE in tx failed: %v", err)
		}

		// Rollback
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;")
		if err != nil {
			t.Fatalf("ROLLBACK failed: %v", err)
		}

		// Table must NOT exist
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM t_tx_ddl;")
		if err == nil {
			t.Errorf("table t_tx_ddl should not exist after ROLLBACK, but query succeeded")
		} else {
			t.Logf("correctly confirmed table does not exist: %v", err)
		}
	})

	// [TEST 7] Self-Join Alias Independence
	t.Run("Self_Join_Alias_Resolution", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
			CREATE TABLE t_tree (id INT, parent_id INT, name VARCHAR(20));
			INSERT INTO t_tree VALUES (1, NULL, 'Root'), (2, 1, 'Child1'), (3, 1, 'Child2');
		`)
		if err != nil {
			t.Fatalf("setup t_tree failed: %v", err)
		}

		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
			SELECT c.name AS child, p.name AS parent
			FROM t_tree c
			LEFT JOIN t_tree p ON c.parent_id = p.id
			ORDER BY c.id;
		`)
		if err != nil {
			t.Fatalf("self join query failed: %v", err)
		}

		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 rows, got %d", len(res.Rows))
		}
		// Row 0: Root, nil
		// Row 1: Child1, Root
		// Row 2: Child2, Root
		if toString(res.Rows[1]["child"]) != "Child1" || toString(res.Rows[1]["parent"]) != "Root" {
			t.Errorf("row 1 expected (Child1, Root), got (%v, %v)", res.Rows[1]["child"], res.Rows[1]["parent"])
		}
	})
}
