package planner_test

import (
	"strings"
	"testing"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

func setupContractCatalog() map[string]*storage.TableSchema {
	return map[string]*storage.TableSchema{
		"users": {
			Name: "users",
			Columns: []storage.Column{
				{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
				{Name: "name", Type: storage.TypeVarChar},
				{Name: "age", Type: storage.TypeInt},
				{Name: "dept_id", Type: storage.TypeInt},
			},
		},
		"orders": {
			Name: "orders",
			Columns: []storage.Column{
				{Name: "order_id", Type: storage.TypeInt, PrimaryKey: true},
				{Name: "user_id", Type: storage.TypeInt},
				{Name: "amount", Type: storage.TypeFloat},
			},
		},
		"emp": {
			Name: "emp",
			Columns: []storage.Column{
				{Name: "emp_id", Type: storage.TypeInt, PrimaryKey: true},
				{Name: "mgr_id", Type: storage.TypeInt},
				{Name: "salary", Type: storage.TypeInt},
			},
		},
	}
}

func parseAndValidate(t *testing.T, sqlStr string, schemas map[string]*storage.TableSchema) error {
	t.Helper()
	parsed, err := proc.ParseSQL(sqlStr)
	if err != nil {
		return err
	}
	sel, ok := parsed.(*proc.GeneralSelectStmt)
	if !ok {
		t.Fatalf("expected *proc.GeneralSelectStmt, got %T", parsed)
	}
	return planner.ValidateQueryAST(sel, schemas)
}

// -----------------------------------------------------------------------------
// 1. LIMIT / OFFSET Contract Tests
// -----------------------------------------------------------------------------
func TestContract_LimitOffset(t *testing.T) {
	schemas := setupContractCatalog()

	// 1. Negative LIMIT -> 2201W
	err := parseAndValidate(t, "SELECT id FROM users LIMIT -1;", schemas)
	if err == nil || !strings.Contains(err.Error(), "2201W") || !strings.Contains(err.Error(), "LIMIT must not be negative") {
		t.Errorf("expected 2201W for negative LIMIT, got: %v", err)
	}

	// 2. Negative OFFSET -> 2201X
	err = parseAndValidate(t, "SELECT id FROM users OFFSET -10;", schemas)
	if err == nil || !strings.Contains(err.Error(), "2201X") || !strings.Contains(err.Error(), "OFFSET must not be negative") {
		t.Errorf("expected 2201X for negative OFFSET, got: %v", err)
	}

	// 3. Valid LIMIT 0, OFFSET 0, LIMIT 10 OFFSET 5 -> PASS
	if err := parseAndValidate(t, "SELECT id FROM users LIMIT 0 OFFSET 0;", schemas); err != nil {
		t.Errorf("expected LIMIT 0 OFFSET 0 to pass, got: %v", err)
	}
	if err := parseAndValidate(t, "SELECT id FROM users LIMIT 10 OFFSET 5;", schemas); err != nil {
		t.Errorf("expected LIMIT 10 OFFSET 5 to pass, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// 2. FROM / JOIN Contract Tests
// -----------------------------------------------------------------------------
func TestContract_FromJoins(t *testing.T) {
	schemas := setupContractCatalog()

	// 1. FROM subquery without alias -> 42601
	err := parseAndValidate(t, "SELECT * FROM (SELECT id FROM users);", schemas)
	if err == nil || !strings.Contains(err.Error(), "42601") || !strings.Contains(err.Error(), "must have an alias") {
		t.Errorf("expected 42601 for FROM subquery without alias, got: %v", err)
	}

	// 2. JOIN subquery without alias -> 42601
	err = parseAndValidate(t, "SELECT * FROM users u JOIN (SELECT order_id FROM orders) ON 1=1;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42601") || !strings.Contains(err.Error(), "must have an alias") {
		t.Errorf("expected 42601 for JOIN subquery without alias, got: %v", err)
	}

	// 3. Duplicate alias in same scope -> FAIL
	err = parseAndValidate(t, "SELECT * FROM users u JOIN orders u ON 1=1;", schemas)
	if err == nil || !strings.Contains(err.Error(), "specified more than once") {
		t.Errorf("expected duplicate alias error, got: %v", err)
	}

	// 4. Duplicate unaliased table in same scope -> FAIL
	err = parseAndValidate(t, "SELECT * FROM users JOIN users ON 1=1;", schemas)
	if err == nil || !strings.Contains(err.Error(), "specified more than once") {
		t.Errorf("expected duplicate unaliased table error, got: %v", err)
	}

	// 5. Scope Shadowing: table aliased as 'u' cannot be referenced by original name 'users'
	err = parseAndValidate(t, "SELECT users.id FROM users u;", schemas)
	if err == nil {
		t.Errorf("expected scope shadowing error when referencing original table name, got nil")
	}

	// 6. Ambiguous unqualified column in Self-Join -> 42702
	err = parseAndValidate(t, "SELECT emp_id FROM emp e1 JOIN emp e2 ON e1.mgr_id = e2.emp_id;", schemas)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous column error for unqualified emp_id, got: %v", err)
	}

	// 7. Aggregate in JOIN ON -> 42803
	err = parseAndValidate(t, "SELECT * FROM users u JOIN orders o ON SUM(u.id) = o.user_id;", schemas)
	if err == nil || !strings.Contains(err.Error(), "aggregate functions are not allowed in JOIN ON") {
		t.Errorf("expected aggregate in JOIN ON error, got: %v", err)
	}

	// 8. Window function in JOIN ON -> 42P20 / ScopeJoinOn
	err = parseAndValidate(t, "SELECT * FROM users u JOIN orders o ON ROW_NUMBER() OVER () = 1;", schemas)
	if err == nil || !strings.Contains(err.Error(), "window functions are not allowed in JOIN ON") {
		t.Errorf("expected window function in JOIN ON error, got: %v", err)
	}

	// 9. Valid JOIN ON with qualified references -> PASS
	if err := parseAndValidate(t, "SELECT u.name, o.amount FROM users u JOIN orders o ON u.id = o.user_id;", schemas); err != nil {
		t.Errorf("expected valid qualified JOIN to pass, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// 3. WHERE Clause Contract Tests
// -----------------------------------------------------------------------------
func TestContract_WhereClause(t *testing.T) {
	schemas := setupContractCatalog()

	// 1. Aggregate in WHERE -> ScopeWhere (disallowed)
	err := parseAndValidate(t, "SELECT id FROM users WHERE SUM(age) > 100;", schemas)
	if err == nil || !strings.Contains(err.Error(), "aggregate functions are not allowed in WHERE") {
		t.Errorf("expected aggregate in WHERE error, got: %v", err)
	}

	// 2. Window function in WHERE -> ScopeWhere (disallowed)
	err = parseAndValidate(t, "SELECT id FROM users WHERE ROW_NUMBER() OVER () = 1;", schemas)
	if err == nil || !strings.Contains(err.Error(), "window functions are not allowed in WHERE") {
		t.Errorf("expected window function in WHERE error, got: %v", err)
	}

	// 3. Scalar subquery returning multiple columns -> 42601
	err = parseAndValidate(t, "SELECT id FROM users WHERE id = (SELECT id, name FROM users LIMIT 1);", schemas)
	if err == nil || !strings.Contains(err.Error(), "42601") || !strings.Contains(err.Error(), "must return only one column") {
		t.Errorf("expected 42601 for scalar subquery multi-column, got: %v", err)
	}

	// 4. IN subquery returning multiple columns -> 42601
	err = parseAndValidate(t, "SELECT id FROM users WHERE id IN (SELECT id, name FROM users);", schemas)
	if err == nil || !strings.Contains(err.Error(), "42601") || !strings.Contains(err.Error(), "must return only one column") {
		t.Errorf("expected 42601 for IN subquery multi-column, got: %v", err)
	}

	// 5. Non-existent column in WHERE -> 42703
	err = parseAndValidate(t, "SELECT id FROM users WHERE ghost_column > 0;", schemas)
	if err == nil || !strings.Contains(err.Error(), "ghost_column") {
		t.Errorf("expected non-existent column error in WHERE, got: %v", err)
	}

	// 6. Valid WHERE predicate -> PASS
	if err := parseAndValidate(t, "SELECT id, name FROM users WHERE age >= 18 AND dept_id IS NOT NULL;", schemas); err != nil {
		t.Errorf("expected valid WHERE to pass, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// 4. GROUP BY Clause Contract Tests
// -----------------------------------------------------------------------------
func TestContract_GroupByClause(t *testing.T) {
	schemas := setupContractCatalog()

	// 1. Aggregate in GROUP BY -> disallowed
	err := parseAndValidate(t, "SELECT dept_id FROM users GROUP BY SUM(age);", schemas)
	if err == nil || !strings.Contains(err.Error(), "aggregate functions are not allowed in GROUP BY") {
		t.Errorf("expected aggregate in GROUP BY error, got: %v", err)
	}

	// 2. Window function in GROUP BY -> disallowed
	err = parseAndValidate(t, "SELECT dept_id FROM users GROUP BY ROW_NUMBER() OVER ();", schemas)
	if err == nil || !strings.Contains(err.Error(), "window functions are not allowed in GROUP BY") {
		t.Errorf("expected window function in GROUP BY error, got: %v", err)
	}

	// 3. Negative position in GROUP BY (e.g. GROUP BY -1) -> disallowed
	err = parseAndValidate(t, "SELECT dept_id FROM users GROUP BY -1;", schemas)
	if err == nil || !strings.Contains(err.Error(), "GROUP BY position") {
		t.Errorf("expected negative position error in GROUP BY, got: %v", err)
	}

	// 4. Out-of-bounds ordinal position -> disallowed
	err = parseAndValidate(t, "SELECT dept_id, name FROM users GROUP BY 99;", schemas)
	if err == nil || !strings.Contains(err.Error(), "GROUP BY position 99 is not in select list") {
		t.Errorf("expected out-of-bounds ordinal error in GROUP BY, got: %v", err)
	}

	// 5. Ordinal referring to aggregate column position -> disallowed
	err = parseAndValidate(t, "SELECT dept_id, COUNT(*) FROM users GROUP BY 2;", schemas)
	if err == nil || !strings.Contains(err.Error(), "aggregate functions are not allowed in GROUP BY") {
		t.Errorf("expected aggregate column ordinal in GROUP BY error, got: %v", err)
	}

	// 6. Table Column Primacy: physical column takes precedence over projection alias!
	// SELECT age + 100 AS id, name FROM users GROUP BY id;
	// 'id' binds to users.id, so unaggregated 'name' must cause 42803 error!
	err = parseAndValidate(t, "SELECT age + 100 AS id, name FROM users GROUP BY id;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42803") || !strings.Contains(err.Error(), "must appear in the GROUP BY clause") {
		t.Errorf("expected 42803 for Table Column Primacy test, got: %v", err)
	}

	// 7. Pure aggregate select list with constant literal GROUP BY -> PASS
	if err := parseAndValidate(t, "SELECT COUNT(*) FROM users GROUP BY 1;", schemas); err != nil {
		t.Errorf("expected SELECT COUNT(*) GROUP BY 1 to pass, got: %v", err)
	}
	if err := parseAndValidate(t, "SELECT COUNT(*) FROM users GROUP BY 100;", schemas); err != nil {
		t.Errorf("expected SELECT COUNT(*) GROUP BY 100 to pass, got: %v", err)
	}

	// 8. Projection alias fallback when NOT conflicting with table column -> PASS
	if err := parseAndValidate(t, "SELECT dept_id, age + 10 AS adjusted_age FROM users GROUP BY dept_id, adjusted_age;", schemas); err != nil {
		t.Errorf("expected non-conflicting projection alias in GROUP BY to pass, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// 5. HAVING Clause Contract Tests
// -----------------------------------------------------------------------------
func TestContract_HavingClause(t *testing.T) {
	schemas := setupContractCatalog()

	// 1. Window function in HAVING -> disallowed
	err := parseAndValidate(t, "SELECT dept_id, COUNT(*) FROM users GROUP BY dept_id HAVING ROW_NUMBER() OVER () > 1;", schemas)
	if err == nil || !strings.Contains(err.Error(), "window functions are not allowed in HAVING") {
		t.Errorf("expected window function in HAVING error, got: %v", err)
	}

	// 2. Nested aggregates in HAVING -> 42803
	err = parseAndValidate(t, "SELECT dept_id FROM users GROUP BY dept_id HAVING SUM(AVG(age)) > 10;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42803") || !strings.Contains(err.Error(), "cannot be nested") {
		t.Errorf("expected 42803 for nested aggregates in HAVING, got: %v", err)
	}

	// 3. Having without GROUP BY referencing unaggregated column -> 42803
	err = parseAndValidate(t, "SELECT dept_id FROM users HAVING age > 30;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42803") {
		t.Errorf("expected 42803 for unaggregated column in standalone HAVING, got: %v", err)
	}

	// 4. Projection alias referenced in HAVING -> PASS
	if err := parseAndValidate(t, "SELECT dept_id, COUNT(*) AS cnt FROM users GROUP BY dept_id HAVING cnt > 5;", schemas); err != nil {
		t.Errorf("expected projection alias in HAVING to pass, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// 6. PROJECTIONS Clause Contract Tests
// -----------------------------------------------------------------------------
func TestContract_ProjectionsClause(t *testing.T) {
	schemas := setupContractCatalog()

	// 1. Nested aggregate function calls -> 42803
	err := parseAndValidate(t, "SELECT SUM(AVG(age)) FROM users;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42803") || !strings.Contains(err.Error(), "cannot be nested") {
		t.Errorf("expected 42803 for nested aggregates in projection, got: %v", err)
	}

	// 2. Window function inside aggregate -> 42P20
	err = parseAndValidate(t, "SELECT SUM(ROW_NUMBER() OVER ()) FROM users;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42P20") || !strings.Contains(err.Error(), "cannot contain window function calls") {
		t.Errorf("expected 42P20 for window function inside aggregate, got: %v", err)
	}

	// 3. Nested window function calls -> 42P20
	err = parseAndValidate(t, "SELECT ROW_NUMBER() OVER (ORDER BY RANK() OVER ()) FROM users;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42P20") || !strings.Contains(err.Error(), "cannot be nested") {
		t.Errorf("expected 42P20 for nested window functions in projection, got: %v", err)
	}

	// 4. Unaggregated and un-grouped column in projection under GROUP BY -> 42803
	err = parseAndValidate(t, "SELECT dept_id, name, COUNT(*) FROM users GROUP BY dept_id;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42803") || !strings.Contains(err.Error(), "must appear in the GROUP BY clause") {
		t.Errorf("expected 42803 for unaggregated column in projection under GROUP BY, got: %v", err)
	}

	// 5. NTILE(0) invalid argument -> 22003
	err = parseAndValidate(t, "SELECT NTILE(0) OVER (ORDER BY id) FROM users;", schemas)
	if err == nil || !strings.Contains(err.Error(), "22003") || !strings.Contains(err.Error(), "must be greater than zero") {
		t.Errorf("expected 22003 for NTILE(0), got: %v", err)
	}

	// 6. PERCENTILE_CONT out of bounds -> 22003
	err = parseAndValidate(t, "SELECT PERCENTILE_CONT(1.5) WITHIN GROUP (ORDER BY age) OVER () FROM users;", schemas)
	if err == nil || !strings.Contains(err.Error(), "22003") || !strings.Contains(err.Error(), "between 0.0 and 1.0") {
		t.Errorf("expected 22003 for PERCENTILE_CONT(1.5), got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// 7. ORDER BY Clause Contract Tests
// -----------------------------------------------------------------------------
func TestContract_OrderByClause(t *testing.T) {
	schemas := setupContractCatalog()

	// 1. SELECT DISTINCT with ORDER BY item NOT in projection list -> 42P10
	err := parseAndValidate(t, "SELECT DISTINCT name FROM users ORDER BY age;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42P10") || !strings.Contains(err.Error(), "must appear in select list") {
		t.Errorf("expected 42P10 for DISTINCT ORDER BY mismatch, got: %v", err)
	}

	// 2. Nested aggregates in ORDER BY -> 42803
	err = parseAndValidate(t, "SELECT dept_id FROM users GROUP BY dept_id ORDER BY SUM(AVG(age));", schemas)
	if err == nil || !strings.Contains(err.Error(), "42803") || !strings.Contains(err.Error(), "cannot be nested") {
		t.Errorf("expected 42803 for nested aggregates in ORDER BY, got: %v", err)
	}

	// 3. Nested window function in ORDER BY -> 42P20
	err = parseAndValidate(t, "SELECT id FROM users ORDER BY ROW_NUMBER() OVER (ORDER BY RANK() OVER ());", schemas)
	if err == nil || !strings.Contains(err.Error(), "42P20") || !strings.Contains(err.Error(), "cannot be nested") {
		t.Errorf("expected 42P20 for nested window function in ORDER BY, got: %v", err)
	}

	// 4. GROUP BY: Unaggregated column in ORDER BY not in GROUP BY -> 42803
	err = parseAndValidate(t, "SELECT dept_id, COUNT(*) FROM users GROUP BY dept_id ORDER BY age;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42803") || !strings.Contains(err.Error(), "must appear in the GROUP BY clause") {
		t.Errorf("expected 42803 for unaggregated column in ORDER BY under GROUP BY, got: %v", err)
	}

	// 5. GROUP BY: Projection alias in ORDER BY -> PASS
	if err := parseAndValidate(t, "SELECT dept_id, COUNT(*) AS cnt FROM users GROUP BY dept_id ORDER BY cnt DESC;", schemas); err != nil {
		t.Errorf("expected projection alias in ORDER BY under GROUP BY to pass, got: %v", err)
	}

	// 6. SELECT DISTINCT with matching ORDER BY -> PASS
	if err := parseAndValidate(t, "SELECT DISTINCT name, age FROM users ORDER BY name, age DESC;", schemas); err != nil {
		t.Errorf("expected matching DISTINCT ORDER BY to pass, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// 8. Recursive Subquery Validation Contract Tests
// -----------------------------------------------------------------------------
func TestContract_RecursiveSubqueries(t *testing.T) {
	schemas := setupContractCatalog()

	// 1. Aggregate in subquery WHERE clause -> disallowed
	err := parseAndValidate(t, "SELECT id FROM users WHERE id IN (SELECT dept_id FROM users WHERE SUM(age) > 100);", schemas)
	if err == nil || !strings.Contains(err.Error(), "aggregate functions are not allowed in WHERE") {
		t.Errorf("expected inner WHERE aggregate error in subquery, got: %v", err)
	}

	// 2. DISTINCT ORDER BY mismatch in scalar subquery -> 42P10
	err = parseAndValidate(t, "SELECT (SELECT DISTINCT name FROM users ORDER BY age LIMIT 1) FROM orders;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42P10") {
		t.Errorf("expected 42P10 in nested subquery, got: %v", err)
	}

	// 3. Subquery without alias inside FROM subquery -> 42601
	err = parseAndValidate(t, "SELECT * FROM (SELECT * FROM (SELECT 1)) AS outer_sub;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42601") || !strings.Contains(err.Error(), "must have an alias") {
		t.Errorf("expected 42601 for unaliased subquery in subquery, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// 9. Separate Window and Aggregate State Validation Contract Tests
// -----------------------------------------------------------------------------
func TestContract_WindowAndAggregateStateSeparation(t *testing.T) {
	schemas := setupContractCatalog()

	// 1. Window function inside aggregate function -> 42P20
	err := parseAndValidate(t, "SELECT dept_id, SUM(ROW_NUMBER() OVER ()) FROM users GROUP BY dept_id;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42P20") || !strings.Contains(err.Error(), "cannot contain window function calls") {
		t.Errorf("expected 42P20 for window function inside aggregate, got: %v", err)
	}

	// 2. Unaggregated, un-grouped column inside window function argument under GROUP BY -> 42803
	err = parseAndValidate(t, "SELECT dept_id, AVG(age) OVER () FROM users GROUP BY dept_id;", schemas)
	if err == nil || !strings.Contains(err.Error(), "42803") || !strings.Contains(err.Error(), "must appear in the GROUP BY clause") {
		t.Errorf("expected 42803 for unaggregated column inside window under GROUP BY, got: %v", err)
	}

	// 3. Aggregated column inside window function argument under GROUP BY -> PASS
	if err := parseAndValidate(t, "SELECT dept_id, AVG(SUM(age)) OVER () FROM users GROUP BY dept_id;", schemas); err != nil {
		t.Errorf("expected aggregated column inside window under GROUP BY to pass, got: %v", err)
	}

	// 4. Grouped column inside window function argument under GROUP BY -> PASS
	if err := parseAndValidate(t, "SELECT dept_id, ROW_NUMBER() OVER (PARTITION BY dept_id) FROM users GROUP BY dept_id;", schemas); err != nil {
		t.Errorf("expected grouped column inside window under GROUP BY to pass, got: %v", err)
	}

	// 5. Window function in HAVING -> 42803 disallowed
	err = parseAndValidate(t, "SELECT dept_id FROM users GROUP BY dept_id HAVING ROW_NUMBER() OVER () > 1;", schemas)
	if err == nil || !strings.Contains(err.Error(), "window functions are not allowed in HAVING") {
		t.Errorf("expected error for window function in HAVING, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// 10. Nil-Safe Subquery AST Validation Contract Tests
// -----------------------------------------------------------------------------
func TestContract_NilSafeSubqueryHandling(t *testing.T) {
	schemas := setupContractCatalog()

	// 1. Synthetic InExpr with nil Query -> must return nil without panic
	inExpr := &proc.InExpr{
		Expr:  &proc.ColumnRefExpr{Name: "id"},
		Query: nil,
	}
	if err := planner.ValidateSubqueryExprs(inExpr, schemas); err != nil {
		t.Errorf("expected InExpr with nil Query to pass safely, got: %v", err)
	}

	// 2. Synthetic ScalarSubqueryExpr with nil Query -> must return nil without panic
	scalarExpr := &proc.ScalarSubqueryExpr{
		Query: nil,
	}
	if err := planner.ValidateSubqueryExprs(scalarExpr, schemas); err != nil {
		t.Errorf("expected ScalarSubqueryExpr with nil Query to pass safely, got: %v", err)
	}

	// 3. Synthetic QuantifiedComparisonExpr with nil Query -> must return nil without panic
	quantExpr := &proc.QuantifiedComparisonExpr{
		Expr:       &proc.ColumnRefExpr{Name: "age"},
		Op:         proc.TokenGt,
		Quantifier: proc.TokenAll,
		Query:      nil,
	}
	if err := planner.ValidateSubqueryExprs(quantExpr, schemas); err != nil {
		t.Errorf("expected QuantifiedComparisonExpr with nil Query to pass safely, got: %v", err)
	}

	// 4. Synthetic ExistsExpr with nil Query -> must return nil without panic
	existsExpr := &proc.ExistsExpr{
		Query: nil,
	}
	if err := planner.ValidateSubqueryExprs(existsExpr, schemas); err != nil {
		t.Errorf("expected ExistsExpr with nil Query to pass safely, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// 11. Unified Common Filter Expression Contract Tests
// -----------------------------------------------------------------------------
func TestContract_CommonFilterExpression(t *testing.T) {
	schemas := setupContractCatalog()

	// 1. Aggregate in WHERE filter scope -> rejected
	aggExpr := &proc.FunctionCallExpr{
		Name: "COUNT",
		Args: []proc.Expr{&proc.ColumnRefExpr{Name: "*"}},
	}
	if err := planner.ValidateCommonFilterExpr(aggExpr, planner.ScopeWhere, schemas); err == nil {
		t.Errorf("expected aggregate in ScopeWhere to be rejected")
	}

	// 2. Window function in WHERE filter scope -> rejected
	winExpr := &proc.FunctionCallExpr{
		Name:    "ROW_NUMBER",
		HasOver: true,
	}
	if err := planner.ValidateCommonFilterExpr(winExpr, planner.ScopeWhere, schemas); err == nil {
		t.Errorf("expected window function in ScopeWhere to be rejected")
	}

	// 3. Aggregate allowed in ScopeHaving, but nested aggregate rejected (SQLSTATE 42803)
	nestedAgg := &proc.FunctionCallExpr{
		Name: "SUM",
		Args: []proc.Expr{
			&proc.FunctionCallExpr{
				Name: "AVG",
				Args: []proc.Expr{&proc.ColumnRefExpr{Name: "age"}},
			},
		},
	}
	if err := planner.ValidateCommonFilterExpr(nestedAgg, planner.ScopeHaving, schemas); err == nil || !strings.Contains(err.Error(), "42803") {
		t.Errorf("expected nested aggregate in ScopeHaving to fail with 42803, got: %v", err)
	}

	// 4. Valid scalar binary comparison in ScopeWhere -> PASS
	validFilter := &proc.BinaryExpr{
		Left:  &proc.ColumnRefExpr{Name: "age"},
		Op:    proc.TokenGt,
		Right: &proc.LiteralExpr{Val: storage.Value{Type: storage.TypeInt, IVal: 18}},
	}
	if err := planner.ValidateCommonFilterExpr(validFilter, planner.ScopeWhere, schemas); err != nil {
		t.Errorf("expected valid filter to pass, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// 12. ORDER BY & GROUP BY Subquery Arity Contract Tests
// -----------------------------------------------------------------------------
func TestContract_OrderByAndGroupBySubqueryArity(t *testing.T) {
	schemas := setupContractCatalog()

	// 1. ORDER BY with multi-column scalar subquery -> 42601
	err := parseAndValidate(t, "SELECT id FROM users ORDER BY (SELECT order_id, amount FROM orders);", schemas)
	if err == nil || !strings.Contains(err.Error(), "42601") || !strings.Contains(err.Error(), "must return only one column") {
		t.Errorf("expected 42601 for multi-column subquery in ORDER BY, got: %v", err)
	}

	// 2. ORDER BY with single-column scalar subquery -> PASS
	if err := parseAndValidate(t, "SELECT id FROM users ORDER BY (SELECT order_id FROM orders LIMIT 1);", schemas); err != nil {
		t.Errorf("expected single-column subquery in ORDER BY to pass, got: %v", err)
	}

	// 3. GROUP BY with multi-column scalar subquery -> 42601
	err = parseAndValidate(t, "SELECT count(*) FROM users GROUP BY (SELECT order_id, amount FROM orders);", schemas)
	if err == nil || !strings.Contains(err.Error(), "42601") || !strings.Contains(err.Error(), "must return only one column") {
		t.Errorf("expected 42601 for multi-column subquery in GROUP BY, got: %v", err)
	}

	// 4. GROUP BY with single-column scalar subquery -> PASS
	if err := parseAndValidate(t, "SELECT count(*) FROM users GROUP BY (SELECT order_id FROM orders LIMIT 1);", schemas); err != nil {
		t.Errorf("expected single-column subquery in GROUP BY to pass, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// 13. Derived Table (FROM Subquery) Schema & Nil Schema Column Validation
// -----------------------------------------------------------------------------
func TestContract_DerivedTableSubqueryColumnValidation(t *testing.T) {
	schemas := setupContractCatalog()

	// 1. Valid column reference from derived table -> PASS
	if err := parseAndValidate(t, "SELECT id FROM (SELECT id FROM users) AS sub;", schemas); err != nil {
		t.Errorf("expected valid column from derived table to pass, got: %v", err)
	}

	// 2. Valid qualified column reference from derived table -> PASS
	if err := parseAndValidate(t, "SELECT sub.id FROM (SELECT id FROM users) AS sub;", schemas); err != nil {
		t.Errorf("expected valid qualified column from derived table to pass, got: %v", err)
	}

	// 3. Non-existent unqualified column from derived table -> FAIL
	err := parseAndValidate(t, "SELECT ghost_col FROM (SELECT id FROM users) AS sub;", schemas)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected non-existent column in derived table to fail, got: %v", err)
	}

	// 4. Non-existent qualified column from derived table -> FAIL
	err = parseAndValidate(t, "SELECT sub.ghost_col FROM (SELECT id FROM users) AS sub;", schemas)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected non-existent qualified column in derived table to fail, got: %v", err)
	}
}


