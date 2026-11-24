package engine_test

import (
	"fmt"
	"strings"
	"testing"
)

func execSQL(t *testing.T, coord interface{ ExecuteSessionSQL(string, string) (any, error) }, sess, query string) (any, error) {
	t.Helper()
	return coord.ExecuteSessionSQL(sess, query)
}

// 1. BUG-DML-INSERT-COLUMN-COUNT-MISMATCH-1 (/tmp/sql2.txt #1)
func TestIntegrity_InsertColumnCountMismatch(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sess := "test_sess"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	_, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_col_mismatch (id INT, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Case 1: More expressions than target columns
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_col_mismatch (id, name) VALUES (999, 'Dave', 30);")
	if err == nil {
		t.Fatalf("expected error for INSERT with more expressions than target columns, got success")
	}
	if !strings.Contains(err.Error(), "INSERT has more expressions than target columns") &&
		!strings.Contains(err.Error(), "column count mismatch") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Case 2: Fewer expressions than target columns
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_col_mismatch (id, name) VALUES (999);")
	if err == nil {
		t.Fatalf("expected error for INSERT with fewer expressions than target columns, got success")
	}
	if !strings.Contains(err.Error(), "INSERT has more target columns than expressions") &&
		!strings.Contains(err.Error(), "column count mismatch") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Case 3: Inferred columns from table schema with too many values
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_col_mismatch VALUES (1, 'Alice', 100);")
	if err == nil {
		t.Fatalf("expected error for INSERT VALUES with too many expressions for schema, got success")
	}

	// Verify table remains empty
	res, err := coord.ExecuteSessionSQL(sess, "SELECT count(*) FROM t_col_mismatch;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Values[0][0] != int64(0) {
		t.Errorf("expected 0 rows in table, got %v", res.Values)
	}
}

// 2. BUG-JOIN-AMBIGUOUS-COLUMN-VALIDATION-1 (/tmp/sql2.txt #2 & /tmp/sql3.txt #3)
func TestIntegrity_JoinAmbiguousColumnValidation(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sess := "test_sess"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	_, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_j1 (id INT, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("CREATE TABLE t_j1 failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sess, "CREATE TABLE t_j2 (id INT, age INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE t_j2 failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_j1 VALUES (1, 'Alice');")
	if err != nil {
		t.Fatalf("INSERT t_j1 failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_j2 VALUES (1, 30);")
	if err != nil {
		t.Fatalf("INSERT t_j2 failed: %v", err)
	}

	// Case 1: Ambiguous column in projection (/tmp/sql2.txt #2)
	_, err = coord.ExecuteSessionSQL(sess, "SELECT id FROM t_j1 JOIN t_j2 ON t_j1.id = t_j2.id;")
	if err == nil {
		t.Fatalf("expected ambiguous column error for SELECT id, got success")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous column error, got: %v", err)
	}

	// Case 2: Ambiguous column in WHERE (/tmp/sql3.txt #3)
	_, err = coord.ExecuteSessionSQL(sess, "SELECT * FROM t_j1 JOIN t_j2 ON t_j1.id = t_j2.id WHERE id = 1;")
	if err == nil {
		t.Fatalf("expected ambiguous column error for WHERE id = 1, got success")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous column error, got: %v", err)
	}

	// Case 3: Properly qualified query succeeds
	res, err := coord.ExecuteSessionSQL(sess, "SELECT t_j1.id, name, age FROM t_j1 JOIN t_j2 ON t_j1.id = t_j2.id WHERE t_j1.id = 1;")
	if err != nil {
		t.Fatalf("qualified SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(res.Rows))
	}
}

// 3. BUG-DML-UPDATE-NON-EXISTENT-COLUMN-1 (/tmp/sql3.txt #1)
func TestIntegrity_UpdateNonExistentColumnRHS(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sess := "test_sess"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	_, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_u1 (id INT, val INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_u1 VALUES (1, 100);")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// UPDATE with non-existent column in SET value
	_, err = coord.ExecuteSessionSQL(sess, "UPDATE t_u1 SET val = non_existent_col WHERE id = 1;")
	if err == nil {
		t.Fatalf("expected error for non-existent column in UPDATE SET, got success")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected 'does not exist' error, got: %v", err)
	}

	// Verify original value was NOT overwritten with NULL
	res, err := coord.ExecuteSessionSQL(sess, "SELECT val FROM t_u1 WHERE id = 1;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Values[0][0]) != "100" {
		t.Errorf("expected val to remain 100, got: %v", res.Values)
	}
}

// 4. FEAT-FROM-DERIVED-TABLE-ALIAS-REQUIRED-1 (/tmp/sql3.txt #2)
func TestIntegrity_DerivedTableSubqueryMustHaveAlias(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sess := "test_sess"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	_, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_dt1 (id INT, val INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_dt1 VALUES (1, 10);")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// Unaliased subquery in FROM must fail
	_, err = coord.ExecuteSessionSQL(sess, "SELECT id FROM (SELECT id FROM t_dt1);")
	if err == nil {
		t.Fatalf("expected error for unaliased subquery in FROM, got success")
	}
	if !strings.Contains(err.Error(), "subquery in FROM must have an alias") {
		t.Errorf("expected 'subquery in FROM must have an alias' error, got: %v", err)
	}

	// Aliased subquery in FROM succeeds
	res, err := coord.ExecuteSessionSQL(sess, "SELECT sub.id FROM (SELECT id FROM t_dt1) AS sub;")
	if err != nil {
		t.Fatalf("aliased subquery failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Values[0][0] != int64(1) {
		t.Errorf("expected 1 row with id=1, got: %v", res.Values)
	}
}

// 5. BUG-DML-INSERT-NOT-NULL-OMISSION-1 (/tmp/sql4.txt #1)
func TestIntegrity_InsertNotNullColumnOmission(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sess := "test_sess"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	_, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_nn (id INT, val INT NOT NULL);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Valid insert supplying all columns
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_nn (id, val) VALUES (1, 10);")
	if err != nil {
		t.Fatalf("valid INSERT failed: %v", err)
	}

	// Invalid insert omitting val (which is NOT NULL and has no DEFAULT)
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_nn (id) VALUES (2);")
	if err == nil {
		t.Fatalf("expected error for omitting NOT NULL column without DEFAULT, got success")
	}
	if !strings.Contains(err.Error(), "not-null constraint") && !strings.Contains(err.Error(), "does not allow nulls") {
		t.Errorf("expected not-null constraint error, got: %v", err)
	}

	// Verify only 1 row exists
	res, err := coord.ExecuteSessionSQL(sess, "SELECT count(*) FROM t_nn;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Values[0][0] != int64(1) {
		t.Errorf("expected 1 row in table, got: %v", res.Values)
	}
}

// 6. BUG-DATE-LITERAL-VALIDATION-FAIL-CLOSED-1 (/tmp/sql4.txt #2)
func TestIntegrity_DateLiteralValidationFailClosed(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sess := "test_sess"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	_, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_dt (id INT, d DATE);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Case 1: Out of range date (Feb 31)
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_dt VALUES (1, '2026-02-31');")
	if err == nil {
		t.Fatalf("expected error for '2026-02-31', got success")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("expected 'out of range' error, got: %v", err)
	}

	// Case 2: Invalid syntax
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_dt VALUES (2, 'not-a-date');")
	if err == nil {
		t.Fatalf("expected error for 'not-a-date', got success")
	}
	if !strings.Contains(err.Error(), "invalid input syntax") && !strings.Contains(err.Error(), "cannot parse date") {
		t.Errorf("expected invalid input syntax error, got: %v", err)
	}

	// Case 3: Valid date succeeds
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_dt VALUES (3, '2026-10-15');")
	if err != nil {
		t.Fatalf("valid date insert failed: %v", err)
	}

	res, err := coord.ExecuteSessionSQL(sess, "SELECT d FROM t_dt WHERE id = 3;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Values[0][0] != "2026-10-15" {
		t.Errorf("expected '2026-10-15', got: %v", res.Values)
	}
}

// 7. BUG-EXISTS-SUBQUERY-SCALAR-WHERE-EVAL-1 (/tmp/sql5.txt #1)
func TestIntegrity_ExistsSubqueryScalarWhere(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sess := "test_sess"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	_, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_sub1 (id INT, val INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_sub1 VALUES (1, 10);")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// Scalar SELECT with WHERE EXISTS on non-empty table
	res, err := coord.ExecuteSessionSQL(sess, "SELECT 1 WHERE EXISTS (SELECT 1 FROM t_sub1);")
	if err != nil {
		t.Fatalf("SELECT 1 WHERE EXISTS failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Values[0][0] != int64(1) {
		t.Errorf("expected 1 row with value 1, got %d rows: %v", len(res.Rows), res.Values)
	}

	// Scalar SELECT with WHERE NOT EXISTS on non-empty table -> 0 rows
	res, err = coord.ExecuteSessionSQL(sess, "SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM t_sub1);")
	if err != nil {
		t.Fatalf("SELECT 1 WHERE NOT EXISTS failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Errorf("expected 0 rows for NOT EXISTS, got %d rows: %v", len(res.Rows), res.Values)
	}
}

// 8. BUG-MATH-MOD-ZERO-DIVISION-ERROR-1 (/tmp/sql5.txt #2)
func TestIntegrity_ModZeroDivisionError(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sess := "test_sess"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	// Case 1: MOD(10, 0)
	_, err := coord.ExecuteSessionSQL(sess, "SELECT MOD(10, 0);")
	if err == nil {
		t.Fatalf("expected division by zero error for MOD(10, 0), got success")
	}
	if !strings.Contains(err.Error(), "division by zero") && !strings.Contains(err.Error(), "divide by zero") {
		t.Errorf("expected division by zero error, got: %v", err)
	}

	// Case 2: MOD(10, 3) succeeds
	res, err := coord.ExecuteSessionSQL(sess, "SELECT MOD(10, 3);")
	if err != nil {
		t.Fatalf("MOD(10, 3) failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Values[0][0] != int64(1) {
		t.Errorf("expected 1, got: %v", res.Values)
	}
}
