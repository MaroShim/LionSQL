package planner

import (
	"fmt"
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

// ClauseScope identifies the syntactic SQL clause being validated.
type ClauseScope int

const (
	ScopeWhere ClauseScope = 1 << iota
	ScopeJoinOn
	ScopeGroupBy
	ScopeHaving
	ScopeOrderBy
	ScopeReturning
	ScopeValues
)

func scopeName(scope ClauseScope) string {
	switch scope {
	case ScopeWhere:
		return "WHERE"
	case ScopeJoinOn:
		return "JOIN ON"
	case ScopeGroupBy:
		return "GROUP BY"
	case ScopeHaving:
		return "HAVING"
	case ScopeOrderBy:
		return "ORDER BY"
	case ScopeReturning:
		return "RETURNING"
	case ScopeValues:
		return "VALUES"
	default:
		return "clause"
	}
}

// WalkAST recursively traverses an expression AST executing fn on each node.
// It stops traversal and returns error if fn returns an error.
// Subqueries are visited as boundary nodes, but their inner clauses are not walked
// as belonging to the outer scope.
func WalkAST(expr proc.Expr, fn func(node proc.Expr) error) error {
	if expr == nil {
		return nil
	}
	if err := fn(expr); err != nil {
		return err
	}
	switch e := expr.(type) {
	case *proc.FunctionCallExpr:
		for _, arg := range e.Args {
			if err := WalkAST(arg, fn); err != nil {
				return err
			}
		}
		if e.Filter != nil {
			if err := WalkAST(e.Filter, fn); err != nil {
				return err
			}
		}
	case *proc.BinaryExpr:
		if err := WalkAST(e.Left, fn); err != nil {
			return err
		}
		return WalkAST(e.Right, fn)
	case *proc.UnaryExpr:
		return WalkAST(e.Expr, fn)
	case *proc.CastExpr:
		return WalkAST(e.Expr, fn)
	case *proc.IsNullExpr:
		return WalkAST(e.Expr, fn)
	case *proc.CaseExpr:
		if err := WalkAST(e.Operand, fn); err != nil {
			return err
		}
		if err := WalkAST(e.Else, fn); err != nil {
			return err
		}
		for _, b := range e.Whens {
			if err := WalkAST(b.Condition, fn); err != nil {
				return err
			}
			if err := WalkAST(b.Result, fn); err != nil {
				return err
			}
		}
	case *proc.InExpr:
		if err := WalkAST(e.Expr, fn); err != nil {
			return err
		}
		for _, itm := range e.List {
			if err := WalkAST(itm, fn); err != nil {
				return err
			}
		}
	case *proc.BetweenExpr:
		if err := WalkAST(e.Expr, fn); err != nil {
			return err
		}
		if err := WalkAST(e.Low, fn); err != nil {
			return err
		}
		return WalkAST(e.High, fn)
	case *proc.LikeExpr:
		if err := WalkAST(e.Expr, fn); err != nil {
			return err
		}
		if err := WalkAST(e.Pattern, fn); err != nil {
			return err
		}
		if e.Escape != nil {
			return WalkAST(e.Escape, fn)
		}
	case *proc.CollateExpr:
		return WalkAST(e.Expr, fn)
	case *proc.TupleExpr:
		for _, elem := range e.Elements {
			if err := WalkAST(elem, fn); err != nil {
				return err
			}
		}
	case *proc.ScalarSubqueryExpr, *proc.ExistsExpr:
		// Subquery boundary: do not traverse inner clauses as outer scope
		return nil
	}
	return nil
}

// CheckDisallowedExprs walks an expression AST to detect disallowed constructs in the given scope.
func CheckDisallowedExprs(expr proc.Expr, scope ClauseScope) error {
	if expr == nil {
		return nil
	}
	return WalkAST(expr, func(node proc.Expr) error {
		switch n := node.(type) {
		case *proc.FunctionCallExpr:
			if n.HasOver {
				// Window functions are disallowed in WHERE, HAVING, GROUP BY
				if scope&(ScopeWhere|ScopeHaving|ScopeGroupBy) != 0 {
					return fmt.Errorf("window functions are not allowed in %s", scopeName(scope))
				}
			} else if proc.IsAggregateFunctionName(n.Name) {
				// Aggregate functions are disallowed in WHERE, JOIN ON, GROUP BY
				if scope&(ScopeWhere|ScopeJoinOn|ScopeGroupBy) != 0 {
					return fmt.Errorf("aggregate functions are not allowed in %s", scopeName(scope))
				}
			}
		}
		return nil
	})
}

// ValidateQueryAST validates semantic invariants on a parsed GeneralSelectStmt.
func ValidateQueryAST(stmt *proc.GeneralSelectStmt, schemas map[string]*storage.TableSchema) error {
	if stmt == nil {
		return nil
	}

	// 1. WHERE clause validation
	if stmt.Where != nil {
		if err := CheckDisallowedExprs(stmt.Where, ScopeWhere); err != nil {
			return err
		}
	}

	// 2. JOIN ON conditions validation
	for _, j := range stmt.Joins {
		if j.On != nil {
			if err := CheckDisallowedExprs(j.On, ScopeJoinOn); err != nil {
				return err
			}
		}
	}

	// 3. GROUP BY validation
	for _, g := range stmt.GroupBy {
		if err := CheckDisallowedExprs(g, ScopeGroupBy); err != nil {
			return err
		}
		// Check negative integer positions (e.g. GROUP BY -1, -5)
		if un, ok := g.(*proc.UnaryExpr); ok && un.Op == proc.TokenMinus {
			if lit, ok := un.Expr.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeInt {
				ord := -int(lit.Val.IVal)
				return fmt.Errorf("GROUP BY position %d is not in select list", ord)
			}
		}
		if lit, ok := g.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeInt && !lit.Val.IsNull {
			ord := int(lit.Val.IVal)
			if ord < 1 {
				return fmt.Errorf("GROUP BY position %d is not in select list", ord)
			}
		}
	}

	// 4. HAVING without GROUP BY validation
	if stmt.Having != nil && len(stmt.GroupBy) == 0 {
		for _, p := range stmt.Projections {
			if p.Expr != nil && !proc.IsConstantExpr(p.Expr) {
				err := WalkAST(p.Expr, func(node proc.Expr) error {
					if fn, ok := node.(*proc.FunctionCallExpr); ok && proc.IsAggregateFunctionName(fn.Name) {
						return nil // aggregate function inside projection is allowed
					}
					if col, ok := node.(*proc.ColumnRefExpr); ok {
						return fmt.Errorf("column %q must appear in the GROUP BY clause or be used in an aggregate function", col.Name)
					}
					return nil
				})
				if err != nil {
					return err
				}
			}
		}
	}

	// 5. Ambiguous Column References across JOINs
	if len(schemas) > 1 {
		// Projections
		for _, p := range stmt.Projections {
			if err := ValidateAmbiguousExpr(p.Expr, schemas); err != nil {
				return err
			}
		}
		// WHERE
		if stmt.Where != nil {
			if err := ValidateAmbiguousExpr(stmt.Where, schemas); err != nil {
				return err
			}
		}
		// ORDER BY
		for _, ob := range stmt.OrderBy {
			if err := ValidateAmbiguousExpr(ob.Expr, schemas); err != nil {
				return err
			}
		}
		// GROUP BY
		for _, gb := range stmt.GroupBy {
			if err := ValidateAmbiguousExpr(gb, schemas); err != nil {
				return err
			}
		}
	}

	return nil
}

// ValidateAmbiguousExpr checks whether any unqualified column in the expression exists in multiple table schemas.
func ValidateAmbiguousExpr(expr proc.Expr, schemas map[string]*storage.TableSchema) error {
	if expr == nil || len(schemas) <= 1 {
		return nil
	}
	return WalkAST(expr, func(node proc.Expr) error {
		col, ok := node.(*proc.ColumnRefExpr)
		if !ok {
			return nil
		}
		colName := col.Name
		if colName == "*" || strings.Contains(colName, ".") {
			return nil // Qualified or wildcard
		}
		cleanName := strings.Trim(strings.ToLower(colName), "\"`[]")
		matchCount := 0
		for _, schema := range schemas {
			if schema == nil {
				continue
			}
			for _, c := range schema.Columns {
				if strings.EqualFold(c.Name, cleanName) {
					matchCount++
					break
				}
			}
		}
		if matchCount > 1 {
			return fmt.Errorf("column reference %q is ambiguous", colName)
		}
		return nil
	})
}

// ValidateDMLContract enforces schema integrity contracts on DML write operations.
func ValidateDMLContract(schema *storage.TableSchema, cols []string, vals [][]proc.Expr, returning []proc.Projection) error {
	if schema == nil {
		return nil
	}

	// 1. Column count match
	if len(cols) > 0 {
		for idx, row := range vals {
			if len(row) != len(cols) {
				return fmt.Errorf("column count mismatch: expected %d values, got %d in tuple at index %d", len(cols), len(row), idx)
			}
		}
	}

	// 2. Validate column references in VALUES
	for _, row := range vals {
		for _, valExpr := range row {
			if err := ValidateExprColsAgainstSchema(valExpr, schema); err != nil {
				return err
			}
		}
	}

	// 3. Validate column references in RETURNING
	for _, ret := range returning {
		if ret.Expr != nil {
			if err := ValidateExprColsAgainstSchema(ret.Expr, schema); err != nil {
				return err
			}
		}
	}

	return nil
}

// ValidateExprColsAgainstSchema checks that column references in expr exist in the given schema.
func ValidateExprColsAgainstSchema(expr proc.Expr, schema *storage.TableSchema) error {
	if expr == nil || schema == nil {
		return nil
	}
	return WalkAST(expr, func(node proc.Expr) error {
		col, ok := node.(*proc.ColumnRefExpr)
		if !ok {
			return nil
		}
		name := col.Name
		if name == "*" {
			return nil
		}
		bare := name
		if dot := strings.Index(bare, "."); dot != -1 {
			bare = bare[dot+1:]
		}
		bare = strings.Trim(bare, "\"`[]")
		for _, c := range schema.Columns {
			if strings.EqualFold(c.Name, bare) {
				return nil
			}
		}
		return fmt.Errorf("column %q does not exist", name)
	})
}
