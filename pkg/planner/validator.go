package planner

import (
	"fmt"
	"strconv"
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
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
	ScopeUpdateSet
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
	case ScopeUpdateSet:
		return "UPDATE SET"
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
		fnUpper := strings.ToUpper(e.Name)
		isDatePartFn := fnUpper == "DATEPART" || fnUpper == "DATE_PART" || fnUpper == "DATEADD" ||
			fnUpper == "DATEDIFF" || fnUpper == "DATEDIFF_BIG" || fnUpper == "DATE_BUCKET" ||
			fnUpper == "DATEBUCKET" || fnUpper == "DATETRUNC" || fnUpper == "DATE_TRUNC"
		for idx, arg := range e.Args {
			if idx == 0 && isDatePartFn {
				if colRef, ok := arg.(*proc.ColumnRefExpr); ok && proc.IsDatePart(colRef.Name) {
					continue
				}
			}
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
		// Subquery boundary: e.Query is evaluated in inner scope
		return nil
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
	case *proc.WithinGroupExpr:
		if e.Function != nil {
			if err := WalkAST(e.Function, fn); err != nil {
				return err
			}
		}
		if e.OrderBy != nil {
			if err := WalkAST(e.OrderBy, fn); err != nil {
				return err
			}
		}
	case *proc.QuantifiedComparisonExpr:
		if err := WalkAST(e.Expr, fn); err != nil {
			return err
		}
		for _, itm := range e.List {
			if err := WalkAST(itm, fn); err != nil {
				return err
			}
		}
		// Subquery boundary: e.Query is evaluated in inner scope
		return nil
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
				// Window functions are disallowed in WHERE, HAVING, GROUP BY, JOIN ON, UPDATE SET, and VALUES
				if scope&(ScopeWhere|ScopeHaving|ScopeGroupBy|ScopeJoinOn|ScopeUpdateSet|ScopeValues) != 0 {
					return fmt.Errorf("window functions are not allowed in %s", scopeName(scope))
				}
			} else if proc.IsAggregateFunctionName(n.Name) {
				// Aggregate functions are disallowed in WHERE, JOIN ON, GROUP BY, UPDATE SET, and VALUES
				if scope&(ScopeWhere|ScopeJoinOn|ScopeGroupBy|ScopeUpdateSet|ScopeValues) != 0 {
					return fmt.Errorf("aggregate functions are not allowed in %s", scopeName(scope))
				}
			}
		}
		return nil
	})
}

// ValidateQueryAST validates semantic invariants on a parsed GeneralSelectStmt.
func ValidateQueryAST(stmt *proc.GeneralSelectStmt, schemas map[string]*storage.TableSchema) error {
	return ValidateQueryASTWithParent(stmt, schemas, nil)
}

// ValidateQueryASTWithParent validates semantic rules and column references for a query within an optional parent scope.
func ValidateQueryASTWithParent(stmt *proc.GeneralSelectStmt, schemas map[string]*storage.TableSchema, parentScope *TableScope) error {
	return validateQueryASTInternal(stmt, schemas, parentScope, 0)
}

func validateQueryASTInternal(stmt *proc.GeneralSelectStmt, schemas map[string]*storage.TableSchema, parentScope *TableScope, depth int) error {
	if stmt == nil {
		return nil
	}
	if depth > 64 {
		return fmt.Errorf("subquery nesting depth exceeds maximum limit of 64")
	}

	// 1. LIMIT and OFFSET Clause Validation
	if err := validateLimitOffset(stmt.LimitExpr, stmt.OffsetExpr); err != nil {
		return err
	}

	// 2. FROM and JOIN Clauses Validation & TableScope Initialization
	scope, err := setupTableScopeAndValidateJoins(stmt, schemas, parentScope)
	if err != nil {
		return err
	}

	// 3. WHERE Clause Validation
	if err := validateWhereClause(stmt.Where, scope, schemas); err != nil {
		return err
	}

	// 4. GROUP BY Clause Validation (with Table Column Primacy)
	resolvedGroupBy, err := validateGroupByClause(stmt, scope, schemas)
	if err != nil {
		return err
	}

	hasGroupBy := len(stmt.GroupBy) > 0
	hasHavingOnly := !hasGroupBy && stmt.Having != nil

	// 5. HAVING Clause Validation (with unaggregated column validation)
	if err := validateHavingClause(stmt.Having, resolvedGroupBy, hasGroupBy, hasHavingOnly, scope, schemas, stmt.Projections); err != nil {
		return err
	}

	// 6. PROJECTIONS Clause Validation
	if err := validateProjections(stmt.Projections, resolvedGroupBy, hasGroupBy, hasHavingOnly, scope, schemas); err != nil {
		return err
	}

	// 7. ORDER BY Clause Validation
	if err := validateOrderByClause(stmt.OrderBy, stmt.Projections, stmt.Distinct, resolvedGroupBy, hasGroupBy, hasHavingOnly, scope, schemas); err != nil {
		return err
	}

	// 8. Recursive Subquery AST Validation
	childSubqueries := collectSubqueriesFromQuery(stmt)
	for _, sub := range childSubqueries {
		if err := validateQueryASTInternal(sub, schemas, scope, depth+1); err != nil {
			return err
		}
	}

	return nil
}

// -----------------------------------------------------------------------------
// Consolidated Clause-Level Validation Contracts
// -----------------------------------------------------------------------------

func validateLimitOffset(limit, offset proc.Expr) error {
	if limit != nil && isNegativeLiteral(limit) {
		return fmt.Errorf("LIMIT must not be negative (SQLSTATE 2201W)")
	}
	if offset != nil && isNegativeLiteral(offset) {
		return fmt.Errorf("OFFSET must not be negative (SQLSTATE 2201X)")
	}
	return nil
}

func deriveSubquerySchema(alias string, subquery *proc.GeneralSelectStmt, schemas map[string]*storage.TableSchema) (*storage.TableSchema, bool) {
	if subquery == nil {
		return nil, false
	}
	var cols []storage.Column
	isOpen := false

	// Build map of subquery's tables/aliases -> TableSchema
	subTables := make(map[string]*storage.TableSchema)
	var fromSubSch *storage.TableSchema
	fromSubIsOpen := false

	if subquery.From.Subquery != nil {
		fromSubSch, fromSubIsOpen = deriveSubquerySchema(subquery.From.Alias, subquery.From.Subquery, schemas)
		if fromSubSch != nil && subquery.From.Alias != "" {
			cleanAlias := strings.ToLower(sqlutil.CleanIdentifier(subquery.From.Alias))
			subTables[cleanAlias] = fromSubSch
		}
	} else if subquery.From.Name != "" {
		tSch := findSchemaFromMap(schemas, subquery.From.Name)
		if tSch != nil {
			cleanName := strings.ToLower(sqlutil.CleanIdentifier(subquery.From.Name))
			subTables[cleanName] = tSch
			bare := strings.ToLower(proc.CleanSchemaPrefix(subquery.From.Name))
			subTables[bare] = tSch
			if subquery.From.Alias != "" {
				cleanAlias := strings.ToLower(sqlutil.CleanIdentifier(subquery.From.Alias))
				subTables[cleanAlias] = tSch
			}
		}
	}

	for _, j := range subquery.Joins {
		if j.Table.Subquery != nil {
			jSubSch, _ := deriveSubquerySchema(j.Table.Alias, j.Table.Subquery, schemas)
			if jSubSch != nil && j.Table.Alias != "" {
				cleanAlias := strings.ToLower(sqlutil.CleanIdentifier(j.Table.Alias))
				subTables[cleanAlias] = jSubSch
			}
		} else if j.Table.Name != "" {
			tSch := findSchemaFromMap(schemas, j.Table.Name)
			if tSch != nil {
				cleanName := strings.ToLower(sqlutil.CleanIdentifier(j.Table.Name))
				subTables[cleanName] = tSch
				bare := strings.ToLower(proc.CleanSchemaPrefix(j.Table.Name))
				subTables[bare] = tSch
				if j.Table.Alias != "" {
					cleanAlias := strings.ToLower(sqlutil.CleanIdentifier(j.Table.Alias))
					subTables[cleanAlias] = tSch
				}
			}
		}
	}

	for i, p := range subquery.Projections {
		// Check for wildcard: * or qual.*
		isStar := false
		var starQualifier string

		if colRef, ok := p.Expr.(*proc.ColumnRefExpr); ok {
			trimmed := strings.TrimSpace(colRef.Name)
			if trimmed == "*" {
				isStar = true
			} else {
				parts := strings.Split(trimmed, ".")
				if len(parts) > 1 && sqlutil.CleanIdentifier(parts[len(parts)-1]) == "*" {
					isStar = true
					starQualifier = strings.ToLower(sqlutil.CleanIdentifier(parts[len(parts)-2]))
				}
			}
		}

		if isStar {
			if starQualifier == "" {
				// Bare *
				if subquery.From.Subquery != nil {
					if fromSubSch != nil && !fromSubIsOpen && len(subquery.Joins) == 0 {
						cols = append(cols, fromSubSch.Columns...)
					} else {
						isOpen = true
					}
				} else if subquery.From.Name != "" && len(subquery.Joins) == 0 {
					if tSch := findSchemaFromMap(schemas, subquery.From.Name); tSch != nil {
						cols = append(cols, tSch.Columns...)
					} else {
						isOpen = true
					}
				} else {
					// Multiple tables in joins or unknown FROM
					isOpen = true
				}
			} else {
				// Qualified wildcard: qual.*
				if tSch, found := subTables[starQualifier]; found && tSch != nil {
					cols = append(cols, tSch.Columns...)
				} else {
					isOpen = true
				}
			}
			continue
		}

		// Named / regular projection
		colName := p.Alias
		if colName == "" {
			if colRef, ok := p.Expr.(*proc.ColumnRefExpr); ok {
				parts := strings.Split(colRef.Name, ".")
				colName = sqlutil.CleanIdentifier(parts[len(parts)-1])
			}
		}
		if colName == "" {
			colName = fmt.Sprintf("col%d", i)
		}
		cols = append(cols, storage.Column{
			Name: colName,
			Type: storage.TypeUnknown,
		})
	}

	return &storage.TableSchema{
		Name:    alias,
		Columns: cols,
	}, isOpen
}

func setupTableScopeAndValidateJoins(stmt *proc.GeneralSelectStmt, schemas map[string]*storage.TableSchema, parentScope *TableScope) (*TableScope, error) {
	// Base Structural Invariant: Subqueries in FROM or JOIN must have an alias (SQLSTATE 42601)
	if stmt.From.Subquery != nil && strings.TrimSpace(stmt.From.Alias) == "" {
		return nil, fmt.Errorf("subquery in FROM must have an alias (SQLSTATE 42601)")
	}
	for _, j := range stmt.Joins {
		if j.Table.Subquery != nil && strings.TrimSpace(j.Table.Alias) == "" {
			return nil, fmt.Errorf("subquery in FROM must have an alias (SQLSTATE 42601)")
		}
	}

	scope := NewTableScopeWithParent(parentScope)
	if len(schemas) > 0 {
		if stmt.From.Subquery != nil {
			subSch, isOpen := deriveSubquerySchema(stmt.From.Alias, stmt.From.Subquery, schemas)
			if err := scope.RegisterWithOpen("", stmt.From.Alias, subSch, isOpen); err != nil {
				return nil, err
			}
		} else if stmt.From.Name != "" {
			sch := findSchemaFromMap(schemas, stmt.From.Name)
			if sch == nil {
				return nil, fmt.Errorf("table not found: %s", stmt.From.Name)
			}
			if err := scope.Register(stmt.From.Name, stmt.From.Alias, sch); err != nil {
				return nil, err
			}
		}

		for _, j := range stmt.Joins {
			if j.Table.Subquery != nil {
				subSch, isOpen := deriveSubquerySchema(j.Table.Alias, j.Table.Subquery, schemas)
				if err := scope.RegisterWithOpen("", j.Table.Alias, subSch, isOpen); err != nil {
					return nil, err
				}
			} else if j.Table.Name != "" {
				sch := findSchemaFromMap(schemas, j.Table.Name)
				if sch == nil {
					return nil, fmt.Errorf("table not found: %s", j.Table.Name)
				}
				if err := scope.Register(j.Table.Name, j.Table.Alias, sch); err != nil {
					return nil, err
				}
			}
		}

		for _, p := range stmt.Projections {
			if p.Alias != "" {
				scope.AddProjectionAlias(p.Alias)
			}
		}

		// Incremental JOIN ON Scope validation: each join ON condition can only see tables declared up to that join
		joinScope := NewTableScopeWithParent(parentScope)
		if stmt.From.Subquery != nil {
			subSch, isOpen := deriveSubquerySchema(stmt.From.Alias, stmt.From.Subquery, schemas)
			if err := joinScope.RegisterWithOpen("", stmt.From.Alias, subSch, isOpen); err != nil {
				return nil, err
			}
		} else if stmt.From.Name != "" {
			if err := joinScope.Register(stmt.From.Name, stmt.From.Alias, findSchemaFromMap(schemas, stmt.From.Name)); err != nil {
				return nil, err
			}
		}
		for _, j := range stmt.Joins {
			if j.Table.Subquery != nil {
				subSch, isOpen := deriveSubquerySchema(j.Table.Alias, j.Table.Subquery, schemas)
				if err := joinScope.RegisterWithOpen("", j.Table.Alias, subSch, isOpen); err != nil {
					return nil, err
				}
			} else if j.Table.Name != "" {
				if err := joinScope.Register(j.Table.Name, j.Table.Alias, findSchemaFromMap(schemas, j.Table.Name)); err != nil {
					return nil, err
				}
			}
			if j.On != nil {
				if err := ValidateCommonFilterExpr(j.On, ScopeJoinOn, nil); err != nil {
					return nil, err
				}
				if err := joinScope.ValidateExpr(j.On); err != nil {
					return nil, err
				}
			}
		}
	} else {
		// Non-catalog syntactic verification for JOIN ON
		for _, j := range stmt.Joins {
			if j.On != nil {
				if err := ValidateCommonFilterExpr(j.On, ScopeJoinOn, nil); err != nil {
					return nil, err
				}
			}
		}
	}

	return scope, nil
}

func validateWhereClause(where proc.Expr, scope *TableScope, schemas map[string]*storage.TableSchema) error {
	if where == nil {
		return nil
	}
	if err := ValidateCommonFilterExpr(where, ScopeWhere, schemas); err != nil {
		return err
	}
	if err := ValidateConstantDomainBounds(where); err != nil {
		return err
	}
	if scope != nil && scope.HasSchemas() {
		if err := scope.ValidateExpr(where); err != nil {
			return err
		}
	}
	return nil
}

func validateGroupByClause(stmt *proc.GeneralSelectStmt, scope *TableScope, schemas map[string]*storage.TableSchema) ([]proc.Expr, error) {
	if len(stmt.GroupBy) == 0 {
		return nil, nil
	}

	for _, g := range stmt.GroupBy {
		if err := CheckDisallowedExprs(g, ScopeGroupBy); err != nil {
			return nil, err
		}
		if err := ValidateNoNestedAggregates(g); err != nil {
			return nil, err
		}
		if err := ValidateSubqueryExprs(g, schemas); err != nil {
			return nil, err
		}
		// Negative integer position check (e.g. GROUP BY -1)
		if un, ok := g.(*proc.UnaryExpr); ok && un.Op == proc.TokenMinus {
			if lit, ok := un.Expr.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeInt {
				ord := -int(lit.Val.IVal)
				return nil, fmt.Errorf("GROUP BY position %d is not in select list", ord)
			}
		}
		if lit, ok := g.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeInt && !lit.Val.IsNull {
			ord := int(lit.Val.IVal)
			hasNonAggTableCol := false
			for _, p := range stmt.Projections {
				if !hasTopLevelAggregateExpr(p.Expr) && !proc.IsConstantExpr(p.Expr) {
					hasNonAggTableCol = true
					break
				}
			}
			if !hasNonAggTableCol {
				// Pure aggregate/constant select list (e.g. SELECT count(*) FROM t GROUP BY 1):
				// treat literal as single-group constant key, do not treat as projection ordinal position!
				continue
			}

			if ord < 1 || ord > len(stmt.Projections) {
				return nil, fmt.Errorf("GROUP BY position %d is not in select list", ord)
			}
			proj := stmt.Projections[ord-1]
			if hasTopLevelAggregateExpr(proj.Expr) {
				return nil, fmt.Errorf("aggregate functions are not allowed in GROUP BY (cannot group by aggregate column position %d)", ord)
			}
		}
	}

	if scope != nil && scope.HasSchemas() {
		for _, g := range stmt.GroupBy {
			if err := scope.ValidateGroupByExpr(g); err != nil {
				return nil, err
			}
		}
	}

	// Resolve GROUP BY expressions for Projections and OrderBy validation
	// Enforcing Table Column Primacy: physical table column strictly takes precedence over projection alias!
	var resolvedGroupBy []proc.Expr
	for _, g := range stmt.GroupBy {
		resolvedGroupBy = append(resolvedGroupBy, g)
		if colExpr, ok := g.(*proc.CollateExpr); ok {
			resolvedGroupBy = append(resolvedGroupBy, colExpr.Expr)
		}
		if lit, ok := g.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeInt && !lit.Val.IsNull {
			ord := int(lit.Val.IVal)
			if ord >= 1 && ord <= len(stmt.Projections) {
				resolvedGroupBy = append(resolvedGroupBy, stmt.Projections[ord-1].Expr)
			}
		}
		if colRef, ok := g.(*proc.ColumnRefExpr); ok {
			cleanName := strings.ToLower(sqlutil.CleanIdentifier(colRef.Name))
			isTableCol := false
			if scope != nil {
				for _, inst := range scope.instances {
					if inst.Schema != nil {
						if _, _, found := inst.Schema.FindColumn(cleanName); found {
							isTableCol = true
							break
						}
					}
				}
			}
			if !isTableCol {
				for _, p := range stmt.Projections {
					if p.Alias != "" && strings.EqualFold(sqlutil.CleanIdentifier(p.Alias), cleanName) {
						resolvedGroupBy = append(resolvedGroupBy, p.Expr)
					}
				}
			}
		}
	}

	return resolvedGroupBy, nil
}

func validateHavingClause(having proc.Expr, resolvedGroupBy []proc.Expr, hasGroupBy bool, hasHavingOnly bool, scope *TableScope, schemas map[string]*storage.TableSchema, projections []proc.Projection) error {
	if having == nil {
		return nil
	}
	if err := ValidateCommonFilterExpr(having, ScopeHaving, schemas); err != nil {
		return err
	}
	if err := ValidateConstantDomainBounds(having); err != nil {
		return err
	}
	if scope != nil && scope.HasSchemas() {
		if err := scope.ValidateHavingExpr(having); err != nil {
			return err
		}
	}
	if hasGroupBy {
		if err := ValidateProjectionUnderGroupByWithProjections(having, resolvedGroupBy, false, projections); err != nil {
			return err
		}
	} else if hasHavingOnly {
		if err := ValidateProjectionUnderHavingWithProjections(having, false, projections); err != nil {
			return err
		}
	}
	return nil
}

func validateProjections(projections []proc.Projection, resolvedGroupBy []proc.Expr, hasGroupBy bool, hasHavingOnly bool, scope *TableScope, schemas map[string]*storage.TableSchema) error {
	for _, p := range projections {
		if p.Expr == nil {
			continue
		}
		if err := ValidateConstantDomainBounds(p.Expr); err != nil {
			return err
		}
		if err := ValidateNoNestedAggregates(p.Expr); err != nil {
			return err
		}
		if err := ValidateNoNestedWindowFunctions(p.Expr); err != nil {
			return err
		}
		if err := ValidateSubqueryExprs(p.Expr, schemas); err != nil {
			return err
		}

		if hasHavingOnly {
			if !proc.IsConstantExpr(p.Expr) {
				if err := ValidateProjectionUnderHaving(p.Expr, false); err != nil {
					return err
				}
			}
		}

		if scope != nil && scope.HasSchemas() {
			if hasGroupBy {
				if !proc.IsConstantExpr(p.Expr) {
					if err := ValidateProjectionUnderGroupBy(p.Expr, resolvedGroupBy, false); err != nil {
						return err
					}
				}
			}

			if err := scope.ValidateExpr(p.Expr); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOrderByClause(orderBy []proc.OrderByItem, projections []proc.Projection, isDistinct bool, resolvedGroupBy []proc.Expr, hasGroupBy bool, hasHavingOnly bool, scope *TableScope, schemas map[string]*storage.TableSchema) error {
	if len(orderBy) == 0 {
		return nil
	}

	// 1. DISTINCT ORDER BY invariant (SQLSTATE 42P10)
	if isDistinct {
		if err := ValidateDistinctOrderBy(projections, orderBy); err != nil {
			return err
		}
	}

	for _, ob := range orderBy {
		if ob.Expr == nil {
			continue
		}
		if err := ValidateConstantDomainBounds(ob.Expr); err != nil {
			return err
		}
		if err := ValidateNoNestedAggregates(ob.Expr); err != nil {
			return err
		}
		if err := ValidateNoNestedWindowFunctions(ob.Expr); err != nil {
			return err
		}
		if err := ValidateSubqueryExprs(ob.Expr, schemas); err != nil {
			return err
		}

		// Positional ORDER BY integer literal (e.g. ORDER BY 1, 2)
		isPositional := false
		if lit, ok := ob.Expr.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeInt && !lit.Val.IsNull {
			ord := int(lit.Val.IVal)
			if ord >= 1 && ord <= len(projections) {
				isPositional = true
			}
		}

		if scope != nil && scope.HasSchemas() {
			if hasGroupBy && !isPositional && !proc.IsConstantExpr(ob.Expr) {
				// In GROUP BY, allow projection aliases in ORDER BY if not conflicting with a table column
				if colRef, ok := ob.Expr.(*proc.ColumnRefExpr); ok {
					cleanName := strings.ToLower(sqlutil.CleanIdentifier(colRef.Name))
					isTableCol := false
					for _, inst := range scope.instances {
						if inst.Schema != nil {
							if _, _, found := inst.Schema.FindColumn(cleanName); found {
								isTableCol = true
								break
							}
						}
					}
					if !isTableCol {
						isAlias := false
						for _, p := range projections {
							if p.Alias != "" && strings.EqualFold(sqlutil.CleanIdentifier(p.Alias), cleanName) {
								isAlias = true
								break
							}
						}
						if isAlias {
							goto validateScope
						}
					}
				}
				if err := ValidateProjectionUnderGroupBy(ob.Expr, resolvedGroupBy, false); err != nil {
					return err
				}
			} else if hasHavingOnly && !isPositional && !proc.IsConstantExpr(ob.Expr) {
				if err := ValidateProjectionUnderHaving(ob.Expr, false); err != nil {
					return err
				}
			}

		validateScope:
			if err := scope.ValidateOrderByExpr(ob.Expr); err != nil {
				return err
			}
		}
	}
	return nil
}

// TableInstance represents a specific reference to a table in a query (FROM or JOIN clause).
type TableInstance struct {
	Alias        string
	OriginalName string
	Schema       *storage.TableSchema
	IsAliased    bool
	IsOpen       bool
}

// TableScope manages visible table schemas and aliases within a query scope.
type TableScope struct {
	parent            *TableScope
	schemas           map[string]*storage.TableSchema
	instances         []TableInstance
	projectionAliases map[string]bool
	openTables        map[string]bool
}

func findSchemaFromMap(schemas map[string]*storage.TableSchema, name string) *storage.TableSchema {
	if schemas == nil {
		return nil
	}
	clean := strings.ToLower(sqlutil.CleanIdentifier(name))
	if sch, ok := schemas[clean]; ok {
		return sch
	}
	bare := strings.ToLower(proc.CleanSchemaPrefix(name))
	if sch, ok := schemas[bare]; ok {
		return sch
	}
	return nil
}

// NewTableScope creates an empty TableScope.
func NewTableScope() *TableScope {
	return NewTableScopeWithParent(nil)
}

// NewTableScopeWithParent creates an empty TableScope linked to a parent scope for subquery resolution.
func NewTableScopeWithParent(parent *TableScope) *TableScope {
	return &TableScope{
		parent:            parent,
		schemas:           make(map[string]*storage.TableSchema),
		projectionAliases: make(map[string]bool),
		openTables:        make(map[string]bool),
	}
}

// isTableOpen checks whether the table or alias is marked as open/dynamic in this scope or parent scopes.
func (s *TableScope) isTableOpen(tblName string) bool {
	clean := strings.ToLower(sqlutil.CleanIdentifier(tblName))
	for cur := s; cur != nil; cur = cur.parent {
		if cur.openTables != nil && cur.openTables[clean] {
			return true
		}
		for _, inst := range cur.instances {
			if strings.EqualFold(inst.Alias, clean) || strings.EqualFold(inst.OriginalName, clean) {
				if inst.IsOpen {
					return true
				}
			}
		}
	}
	return false
}

// NewTableScopeFromMap constructs a TableScope from a map of table/alias names to schemas.
func NewTableScopeFromMap(schemas map[string]*storage.TableSchema) *TableScope {
	scope := NewTableScope()
	if schemas == nil {
		return scope
	}
	for name, sch := range schemas {
		_ = scope.Register(name, "", sch)
	}
	return scope
}

// Register registers a table name and optional alias into the scope as a closed schema.
func (s *TableScope) Register(table, alias string, schema *storage.TableSchema) error {
	return s.RegisterWithOpen(table, alias, schema, false)
}

// RegisterWithOpen registers a table name and optional alias into the scope with an open/closed flag.
// Enforces table alias shadowing: when an alias is specified, the original table name
// is shadowed and hidden from the scope.
// Rejects duplicate table names or duplicate aliases in the same scope.
func (s *TableScope) RegisterWithOpen(table, alias string, schema *storage.TableSchema, isOpen bool) error {
	if s == nil {
		return nil
	}
	if s.openTables == nil {
		s.openTables = make(map[string]bool)
	}
	cleanTbl := strings.ToLower(sqlutil.CleanIdentifier(table))
	bareTbl := strings.ToLower(proc.CleanSchemaPrefix(table))
	cleanAlias := strings.ToLower(sqlutil.CleanIdentifier(alias))

	if cleanAlias != "" {
		if _, exists := s.schemas[cleanAlias]; exists {
			return fmt.Errorf("table name or alias %q specified more than once", alias)
		}
		s.schemas[cleanAlias] = schema
		if isOpen {
			s.openTables[cleanAlias] = true
		}
		// Original table name is shadowed!
		s.instances = append(s.instances, TableInstance{
			Alias:        cleanAlias,
			OriginalName: cleanTbl,
			Schema:       schema,
			IsAliased:    true,
			IsOpen:       isOpen,
		})
	} else if cleanTbl != "" {
		if _, exists := s.schemas[cleanTbl]; exists {
			return fmt.Errorf("table name %q specified more than once", table)
		}
		s.schemas[cleanTbl] = schema
		if isOpen {
			s.openTables[cleanTbl] = true
		}
		if bareTbl != "" && bareTbl != cleanTbl {
			s.schemas[bareTbl] = schema
			if isOpen {
				s.openTables[bareTbl] = true
			}
		}
		s.instances = append(s.instances, TableInstance{
			Alias:        cleanTbl,
			OriginalName: cleanTbl,
			Schema:       schema,
			IsAliased:    false,
			IsOpen:       isOpen,
		})
	}
	return nil
}

// AddProjectionAlias registers a projection alias (allowed in ORDER BY).
func (s *TableScope) AddProjectionAlias(alias string) {
	if s == nil {
		return
	}
	clean := strings.ToLower(sqlutil.CleanIdentifier(alias))
	if clean != "" {
		s.projectionAliases[clean] = true
	}
}

// HasSchemas returns whether any schemas are known in this scope.
func (s *TableScope) HasSchemas() bool {
	return s != nil && (len(s.schemas) > 0 || len(s.instances) > 0)
}

// ValidateExpr checks all column references within an expression against the table scope.
func (s *TableScope) ValidateExpr(expr proc.Expr) error {
	return s.validateExprInternal(expr, false)
}

// ValidateOrderByExpr checks all column references within an ORDER BY expression against the table scope,
// permitting projection aliases.
func (s *TableScope) ValidateOrderByExpr(expr proc.Expr) error {
	return s.validateExprInternal(expr, true)
}

// ValidateHavingExpr checks all column references within a HAVING expression against the table scope,
// permitting projection aliases.
func (s *TableScope) ValidateHavingExpr(expr proc.Expr) error {
	return s.validateExprInternal(expr, true)
}

// ValidateGroupByExpr checks all column references within a GROUP BY expression against the table scope,
// permitting projection aliases as fallback.
func (s *TableScope) ValidateGroupByExpr(expr proc.Expr) error {
	return s.validateExprInternal(expr, true)
}

// ColumnResolution represents the tri-state resolution outcome for a column reference during AST validation:
// 1. ResolveDefinite: The column is definitively proven to exist in the schema (존재 확정).
// 2. ResolveNotFound: The schema is closed and the column is definitively proven NOT to exist (부존재 확정 -> Fail-Closed).
// 3. ResolveIndeterminate: The schema is open or dynamic (wildcards, multi-table joins, or nil metadata),
//    so column presence cannot be statically proven or disproven from current metadata (현재 메타데이터로 판단 불가 -> Defer to Binder).
type ColumnResolution int

const (
	// ResolveDefinite indicates that the column was definitively found in the catalog or scope.
	ResolveDefinite ColumnResolution = iota
	// ResolveNotFound indicates that all candidate tables are closed and the column is definitively absent.
	ResolveNotFound
	// ResolveIndeterminate indicates that presence cannot be determined from current metadata; deferred to Binder.
	ResolveIndeterminate
)

// String returns the canonical human-readable label for the resolution state.
func (r ColumnResolution) String() string {
	switch r {
	case ResolveDefinite:
		return "Definite"
	case ResolveNotFound:
		return "NotFound"
	case ResolveIndeterminate:
		return "Indeterminate"
	default:
		return fmt.Sprintf("Unknown(%d)", int(r))
	}
}

// ResolveQualifiedColumn evaluates a qualified column reference (tblPart.colPart) against the scope.
// It returns:
// - (ResolveDefinite, nil) if the column is confirmed to exist.
// - (ResolveNotFound, err) if the table does not exist or the table is closed and the column is confirmed NOT to exist.
// - (ResolveIndeterminate, nil) if the table's schema is open/dynamic or nil, deferring resolution to the Binder.
func (s *TableScope) ResolveQualifiedColumn(tblPart, colPart string) (ColumnResolution, error) {
	if s == nil || len(s.schemas) == 0 {
		return ResolveIndeterminate, nil
	}
	cleanTbl := strings.ToLower(sqlutil.CleanIdentifier(tblPart))
	sch, ok := s.schemas[cleanTbl]
	if !ok {
		foundInParent := false
		for p := s.parent; p != nil; p = p.parent {
			if psch, pok := p.schemas[cleanTbl]; pok {
				sch = psch
				foundInParent = true
				break
			}
		}
		if !foundInParent {
			return ResolveNotFound, fmt.Errorf("table or alias %q does not exist in query scope", tblPart)
		}
	}

	if colPart == "*" {
		return ResolveDefinite, nil
	}

	if sch == nil {
		return ResolveIndeterminate, nil
	}

	if _, _, found := sch.FindColumn(colPart); found {
		return ResolveDefinite, nil
	}

	if s.isTableOpen(cleanTbl) {
		return ResolveIndeterminate, nil
	}

	return ResolveNotFound, fmt.Errorf("column %q does not exist in table or alias %q", colPart, tblPart)
}

// ResolveUnqualifiedColumn evaluates an unqualified column reference against all table instances in the scope.
// It returns:
// - (ResolveDefinite, nil) if the column is confirmed to exist uniquely, or (ResolveDefinite, err) if ambiguous.
// - (ResolveNotFound, err) if all instances are closed and the column is confirmed NOT to exist in any table.
// - (ResolveIndeterminate, nil) if the column is not found in known columns but an open/dynamic or nil-schema instance exists.
func (s *TableScope) ResolveUnqualifiedColumn(raw string, allowProjectionAlias bool) (ColumnResolution, error) {
	if s == nil || len(s.instances) == 0 {
		return ResolveIndeterminate, nil
	}
	cleanCol := strings.ToLower(sqlutil.CleanIdentifier(raw))

	matchCount := 0
	hasNilSchemaInstance := false
	hasOpenInstance := false
	for _, inst := range s.instances {
		if inst.Schema == nil {
			hasNilSchemaInstance = true
			continue
		}
		if inst.IsOpen {
			hasOpenInstance = true
		}
		if _, _, ok := inst.Schema.FindColumn(cleanCol); ok {
			matchCount++
		}
	}

	if matchCount > 1 {
		return ResolveDefinite, fmt.Errorf("column reference %q is ambiguous", raw)
	}
	if matchCount == 1 {
		// If there is an open or nil-schema table in scope, its full columns are unknown.
		// It might also produce this column, causing ambiguity, so uniqueness cannot be confirmed.
		if hasOpenInstance || hasNilSchemaInstance {
			return ResolveIndeterminate, nil
		}
		return ResolveDefinite, nil
	}

	// matchCount == 0: check projection aliases if permitted
	if allowProjectionAlias && s.projectionAliases[cleanCol] {
		if hasOpenInstance || hasNilSchemaInstance {
			return ResolveIndeterminate, nil
		}
		return ResolveDefinite, nil
	}

	// If the current inner scope has open or nil-schema tables, we cannot confirm whether
	// the column belongs to an inner open table (shadowing parent) or a parent scope.
	if hasOpenInstance || hasNilSchemaInstance {
		return ResolveIndeterminate, nil
	}

	// Inner scope is completely closed and does not have the column.
	// Check parent scopes in ascending order.
	for p := s.parent; p != nil; p = p.parent {
		pmatchCount := 0
		phasNil := false
		phasOpen := false
		for _, pinst := range p.instances {
			if pinst.Schema == nil {
				phasNil = true
				continue
			}
			if pinst.IsOpen {
				phasOpen = true
			}
			if _, _, ok := pinst.Schema.FindColumn(cleanCol); ok {
				pmatchCount++
			}
		}
		if pmatchCount > 1 {
			return ResolveDefinite, fmt.Errorf("column reference %q is ambiguous", raw)
		}
		if pmatchCount == 1 {
			// Found in parent, but if parent itself has open/nil instances,
			// parent uniqueness is not confirmed.
			if phasOpen || phasNil {
				return ResolveIndeterminate, nil
			}
			return ResolveDefinite, nil
		}
		if phasNil || phasOpen {
			// Parent has open table which might produce this column.
			return ResolveIndeterminate, nil
		}
	}

	// All scopes (inner and all parents) are closed, and column was not found anywhere.
	return ResolveNotFound, fmt.Errorf("column %q does not exist", raw)
}

func (s *TableScope) validateExprInternal(expr proc.Expr, allowProjectionAlias bool) error {
	if s == nil || expr == nil || !s.HasSchemas() {
		return nil
	}
	return WalkAST(expr, func(node proc.Expr) error {
		// 1. Static validation: Arithmetic operators must not operate on non-numeric string literals
		if bin, ok := node.(*proc.BinaryExpr); ok {
			switch bin.Op {
			case proc.TokenSlash, proc.TokenAsterisk, proc.TokenMinus, proc.TokenPercent, proc.TokenDiv:
				if isNonNumericStringLiteral(bin.Left) || isNonNumericStringLiteral(bin.Right) {
					var nonNum string
					if isNonNumericStringLiteral(bin.Left) {
						nonNum = bin.Left.(*proc.LiteralExpr).Val.SVal
					} else {
						nonNum = bin.Right.(*proc.LiteralExpr).Val.SVal
					}
					return fmt.Errorf("conversion failed when converting the varchar value %q to data type numeric", nonNum)
				}
			}
		}

		col, ok := node.(*proc.ColumnRefExpr)
		if !ok {
			return nil
		}
		raw := strings.TrimSpace(col.Name)
		if raw == "" || raw == "*" {
			return nil
		}
		if strings.HasPrefix(raw, "@") {
			return nil
		}
		if proc.IsBuiltinFunction(raw) {
			return nil
		}

		parts := strings.Split(raw, ".")
		if len(parts) > 1 {
			// Qualified column reference: e.g. tbl.col or db.tbl.col
			last := len(parts) - 1
			colPart := sqlutil.CleanIdentifier(parts[last])
			tblPart := strings.ToLower(sqlutil.CleanIdentifier(parts[last-1]))

			res, err := s.ResolveQualifiedColumn(tblPart, colPart)
			if err != nil {
				return err
			}
			_ = res // ResolveDefinite or ResolveIndeterminate passes
			return nil
		}

		// Unqualified column reference
		res, err := s.ResolveUnqualifiedColumn(raw, allowProjectionAlias)
		if err != nil {
			return err
		}
		_ = res // ResolveDefinite or ResolveIndeterminate passes
		return nil
	})
}

func isNonNumericStringLiteral(expr proc.Expr) bool {
	if expr == nil {
		return false
	}
	lit, ok := expr.(*proc.LiteralExpr)
	if !ok || lit.Val.Type != storage.TypeVarChar || lit.Val.IsNull {
		return false
	}
	str := strings.TrimSpace(lit.Val.SVal)
	if _, err := strconv.ParseFloat(str, 64); err != nil {
		return true
	}
	return false
}

// ValidateAmbiguousExpr checks whether any unqualified column in the expression exists in multiple table schemas.
func ValidateAmbiguousExpr(expr proc.Expr, schemas map[string]*storage.TableSchema) error {
	if expr == nil || len(schemas) == 0 {
		return nil
	}
	scope := NewTableScopeFromMap(schemas)
	return scope.ValidateExpr(expr)
}

// ValidateNoNestedAggregates ensures that aggregate functions are not nested within other aggregate functions.
func ValidateNoNestedAggregates(expr proc.Expr) error {
	if expr == nil {
		return nil
	}
	return WalkAST(expr, func(node proc.Expr) error {
		fn, ok := node.(*proc.FunctionCallExpr)
		if !ok || !proc.IsAggregateFunctionName(fn.Name) || fn.HasOver {
			return nil
		}
		for _, arg := range fn.Args {
			if err := WalkAST(arg, func(inner proc.Expr) error {
				if innerFn, isFn := inner.(*proc.FunctionCallExpr); isFn {
					if proc.IsAggregateFunctionName(innerFn.Name) {
						return fmt.Errorf("aggregate function calls cannot be nested (SQLSTATE 42803)")
					}
					if innerFn.HasOver {
						return fmt.Errorf("aggregate function calls cannot contain window function calls (SQLSTATE 42P20)")
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}
		if fn.Filter != nil {
			if err := WalkAST(fn.Filter, func(inner proc.Expr) error {
				if innerFn, isFn := inner.(*proc.FunctionCallExpr); isFn {
					if proc.IsAggregateFunctionName(innerFn.Name) {
						return fmt.Errorf("aggregate function calls cannot be nested (SQLSTATE 42803)")
					}
					if innerFn.HasOver {
						return fmt.Errorf("aggregate function calls cannot contain window function calls (SQLSTATE 42P20)")
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ValidateCommonFilterExpr performs unified semantic validation for filter expressions across SELECT and DML:
// 1. Prohibits expressions disallowed for the clause scope (e.g. aggregates and window functions in WHERE/ON).
// 2. Prohibits nested aggregate functions (SQLSTATE 42803).
// 3. Prohibits nested window functions (SQLSTATE 42P20).
// 4. Enforces unified subquery validation (single-column arity for scalar/IN/quantified subqueries, nil safety, recursive schema validation).
func ValidateCommonFilterExpr(expr proc.Expr, scope ClauseScope, schemas map[string]*storage.TableSchema) error {
	if expr == nil {
		return nil
	}
	if err := CheckDisallowedExprs(expr, scope); err != nil {
		return err
	}
	if err := ValidateNoNestedAggregates(expr); err != nil {
		return err
	}
	if err := ValidateNoNestedWindowFunctions(expr); err != nil {
		return err
	}
	// LIKE ... ESCAPE single-character constraint
	if err := WalkAST(expr, func(node proc.Expr) error {
		if likeNode, ok := node.(*proc.LikeExpr); ok && likeNode.Escape != nil {
			if lit, ok := likeNode.Escape.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeVarChar {
				s := lit.Val.SVal
				if len(s) == 0 {
					return fmt.Errorf("invalid escape string: escape character must be a single character (SQLSTATE 2200C)")
				}
				if len(s) > 1 {
					return fmt.Errorf("invalid escape string: escape character must be a single character (SQLSTATE 22019)")
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return ValidateSubqueryExprs(expr, schemas)
}

// ValidateSubqueryExprs performs unified subquery semantic validation across SELECT and DML statements:
// 1. Column arity check: scalar, IN, and ANY/ALL subqueries must return exactly one column (SQLSTATE 42601).
// 2. Nil-safe query validation: safely handles expressions where subquery AST is nil.
// 3. Recursive query AST validation: if schemas is provided, validates subquery table scopes and predicates.
func ValidateSubqueryExprs(expr proc.Expr, schemas map[string]*storage.TableSchema) error {
	if expr == nil {
		return nil
	}
	return WalkAST(expr, func(n proc.Expr) error {
		var subQuery *proc.GeneralSelectStmt
		checkArity := false

		switch sub := n.(type) {
		case *proc.InExpr:
			if sub.Query != nil {
				subQuery = sub.Query
				checkArity = true
			}
		case *proc.ScalarSubqueryExpr:
			if sub.Query != nil {
				subQuery = sub.Query
				checkArity = true
			}
		case *proc.QuantifiedComparisonExpr:
			if sub.Query != nil {
				subQuery = sub.Query
				checkArity = true
			}
		case *proc.ExistsExpr:
			if sub.Query != nil {
				subQuery = sub.Query
				checkArity = false
			}
		}

		if subQuery != nil {
			if checkArity {
				arity := len(subQuery.Projections)
				if arity == 1 && subQuery.From.Name != "" {
					if colRef, isCol := subQuery.Projections[0].Expr.(*proc.ColumnRefExpr); isCol {
						clean := strings.TrimSpace(colRef.Name)
						if clean == "*" || strings.HasSuffix(clean, ".*") {
							var tblSchema *storage.TableSchema
							if schemas != nil {
								tblSchema = findSchemaFromMap(schemas, subQuery.From.Name)
							}
							if tblSchema != nil && len(tblSchema.Columns) > 1 {
								return fmt.Errorf("subquery must return only one column, returned %d (SQLSTATE 42601)", len(tblSchema.Columns))
							}
						}
					}
				}
				if arity != 1 {
					return fmt.Errorf("subquery must return only one column, returned %d (SQLSTATE 42601)", arity)
				}
			}
		}
		return nil
	})
}

// ValidateSubqueryColumnCount verifies that scalar subqueries and IN subqueries return exactly one column.
func ValidateSubqueryColumnCount(expr proc.Expr) error {
	return ValidateSubqueryExprs(expr, nil)
}

// ValidateNoNestedWindowFunctions verifies that window function calls do not contain nested window functions (SQLSTATE 42P20).
func ValidateNoNestedWindowFunctions(expr proc.Expr) error {
	if expr == nil {
		return nil
	}
	return WalkAST(expr, func(node proc.Expr) error {
		fn, ok := node.(*proc.FunctionCallExpr)
		if !ok || !fn.HasOver {
			return nil
		}
		if sqlutil.ContainsKeyword(fn.OverClause, "OVER", true) {
			return fmt.Errorf("window function calls cannot be nested (SQLSTATE 42P20)")
		}
		for _, arg := range fn.Args {
			if err := WalkAST(arg, func(inner proc.Expr) error {
				if innerFn, isFn := inner.(*proc.FunctionCallExpr); isFn && innerFn.HasOver {
					return fmt.Errorf("window function calls cannot be nested (SQLSTATE 42P20)")
				}
				return nil
			}); err != nil {
				return err
			}
		}
		if fn.Filter != nil {
			if err := WalkAST(fn.Filter, func(inner proc.Expr) error {
				if innerFn, isFn := inner.(*proc.FunctionCallExpr); isFn && innerFn.HasOver {
					return fmt.Errorf("window function calls cannot be nested (SQLSTATE 42P20)")
				}
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// isNegativeLiteral checks if an expression is a negative integer literal (e.g. -1, -5).
func isNegativeLiteral(expr proc.Expr) bool {
	if expr == nil {
		return false
	}
	if lit, ok := expr.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeInt && lit.Val.IVal < 0 {
		return true
	}
	if un, ok := expr.(*proc.UnaryExpr); ok && un.Op == proc.TokenMinus {
		if lit, ok := un.Expr.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeInt && lit.Val.IVal > 0 {
			return true
		}
	}
	return false
}

// ValidateDistinctOrderBy verifies that for SELECT DISTINCT, every ORDER BY expression appears in the projection select list (SQLSTATE 42P10).
func ValidateDistinctOrderBy(projections []proc.Projection, orderBy []proc.OrderByItem) error {
	if len(orderBy) == 0 {
		return nil
	}
	var projExprs []proc.Expr
	var projAliases []string
	hasWildcard := false

	for _, p := range projections {
		if p.Expr != nil {
			projExprs = append(projExprs, p.Expr)
			if colRef, ok := p.Expr.(*proc.ColumnRefExpr); ok {
				clean := strings.TrimSpace(colRef.Name)
				if clean == "*" || strings.HasSuffix(clean, ".*") {
					hasWildcard = true
				}
			}
		}
		if p.Alias != "" {
			projAliases = append(projAliases, strings.ToLower(sqlutil.CleanIdentifier(p.Alias)))
		}
	}

	if hasWildcard {
		return nil
	}

	for _, ob := range orderBy {
		if ob.Expr == nil || proc.IsConstantExpr(ob.Expr) {
			continue
		}
		// Check integer ordinal (e.g. ORDER BY 1, 2)
		if lit, ok := ob.Expr.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeInt && !lit.Val.IsNull {
			ord := int(lit.Val.IVal)
			if ord >= 1 && ord <= len(projections) {
				continue
			}
		}
		// Check alias match
		if colRef, ok := ob.Expr.(*proc.ColumnRefExpr); ok {
			cleanName := strings.ToLower(sqlutil.CleanIdentifier(colRef.Name))
			isAlias := false
			for _, alias := range projAliases {
				if alias == cleanName {
					isAlias = true
					break
				}
			}
			if isAlias {
				continue
			}
		}
		// Check projection expression match
		if exprMatchesGroupBy(ob.Expr, projExprs) {
			continue
		}
		return fmt.Errorf("for SELECT DISTINCT, ORDER BY expressions must appear in select list (SQLSTATE 42P10)")
	}
	return nil
}

// ValidateConstantDomainBounds checks constant argument bounds for functions such as NTILE and PERCENTILE_CONT,
// division or modulo by literal zero, and semantic type invariants on expressions like CASE.
func ValidateConstantDomainBounds(expr proc.Expr) error {
	if expr == nil {
		return nil
	}
	return WalkAST(expr, func(node proc.Expr) error {
		// 1. Division or modulo by literal zero in constant expressions (SQLSTATE 22012)
		if bin, ok := node.(*proc.BinaryExpr); ok {
			if bin.Op == proc.TokenSlash || bin.Op == proc.TokenPercent {
				if isLiteralZero(bin.Right) {
					return fmt.Errorf("division by zero (SQLSTATE 22012)")
				}
			}
		}

		// 2. LIKE ... ESCAPE single-character constraint (SQLSTATE 22019 / 2200C)
		if likeNode, ok := node.(*proc.LikeExpr); ok && likeNode.Escape != nil {
			if lit, ok := likeNode.Escape.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeVarChar {
				s := lit.Val.SVal
				if len(s) == 0 {
					return fmt.Errorf("invalid escape string: escape character must be a single character (SQLSTATE 2200C)")
				}
				if len(s) > 1 {
					return fmt.Errorf("invalid escape string: escape character must be a single character (SQLSTATE 22019)")
				}
			}
		}

		// 3. Static validation: CASE branches must have compatible result types
		if caseNode, ok := node.(*proc.CaseExpr); ok {
			var branchTypes []storage.DataType
			for _, w := range caseNode.Whens {
				if w.Result != nil {
					if lit, isLit := w.Result.(*proc.LiteralExpr); isLit && !lit.Val.IsNull {
						branchTypes = append(branchTypes, lit.Val.Type)
					}
				}
			}
			if caseNode.Else != nil {
				if lit, isLit := caseNode.Else.(*proc.LiteralExpr); isLit && !lit.Val.IsNull {
					branchTypes = append(branchTypes, lit.Val.Type)
				}
			}
			for i := 0; i < len(branchTypes); i++ {
				for j := i + 1; j < len(branchTypes); j++ {
					t1, t2 := branchTypes[i], branchTypes[j]
					if t1 != t2 && !areCompatibleCaseTypes(t1, t2) {
						return fmt.Errorf("incompatible types in CASE branches: cannot match %s and %s", t1.String(), t2.String())
					}
				}
			}
		}

		fn, ok := node.(*proc.FunctionCallExpr)
		if !ok {
			return nil
		}
		upperName := strings.ToUpper(fn.Name)
		if upperName == "NTILE" && len(fn.Args) > 0 {
			if lit, ok := fn.Args[0].(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeInt && !lit.Val.IsNull {
				if lit.Val.IVal <= 0 {
					return fmt.Errorf("ERROR 22003: Msg 7812, Level 16, State 1: The argument of NTILE must be greater than zero.")
				}
			}
			if un, ok := fn.Args[0].(*proc.UnaryExpr); ok && un.Op == proc.TokenMinus {
				return fmt.Errorf("ERROR 22003: Msg 7812, Level 16, State 1: The argument of NTILE must be greater than zero.")
			}
		}
		if (upperName == "PERCENTILE_CONT" || upperName == "PERCENTILE_DISC") && len(fn.Args) > 0 {
			if lit, ok := fn.Args[0].(*proc.LiteralExpr); ok && !lit.Val.IsNull {
				val := 0.0
				if lit.Val.Type == storage.TypeFloat || lit.Val.Type == storage.TypeDecimal {
					val = lit.Val.FVal
				} else if lit.Val.Type == storage.TypeInt {
					val = float64(lit.Val.IVal)
				}
				if val < 0.0 || val > 1.0 {
					return fmt.Errorf("ERROR 22003: Msg 8704, Level 16, State 1: Percentile value %v must be between 0.0 and 1.0.", val)
				}
			}
			if un, ok := fn.Args[0].(*proc.UnaryExpr); ok && un.Op == proc.TokenMinus {
				return fmt.Errorf("ERROR 22003: Msg 8704, Level 16, State 1: Percentile value must be between 0.0 and 1.0.")
			}
		}
		return nil
	})
}

func areCompatibleCaseTypes(t1, t2 storage.DataType) bool {
	if t1 == t2 {
		return true
	}
	if isNumericType(t1) && isNumericType(t2) {
		return true
	}
	// In T-SQL / SQL standards, binary/hex literals (e.g. 0x200, 0x01) are integers / bitmasks and seamlessly coerce with integer numeric types.
	if (t1 == storage.TypeBinary && isNumericType(t2)) || (t2 == storage.TypeBinary && isNumericType(t1)) {
		return true
	}
	if (t1 == storage.TypeDateTime || t1 == storage.TypeDate) && (t2 == storage.TypeDateTime || t2 == storage.TypeDate) {
		return true
	}
	return false
}

func isLiteralZero(expr proc.Expr) bool {
	if expr == nil {
		return false
	}
	if u, ok := expr.(*proc.UnaryExpr); ok && (u.Op == proc.TokenMinus || u.Op == proc.TokenPlus) {
		return isLiteralZero(u.Expr)
	}
	if lit, ok := expr.(*proc.LiteralExpr); ok && !lit.Val.IsNull {
		switch lit.Val.Type {
		case storage.TypeInt:
			return lit.Val.IVal == 0
		case storage.TypeFloat, storage.TypeReal:
			return lit.Val.FVal == 0.0
		case storage.TypeDecimal:
			return lit.Val.DVal.IsZero()
		}
	}
	return false
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

// ValidateProjectionUnderHaving recursively verifies that any column reference
// in a projection without GROUP BY is strictly enclosed within an aggregate function.
func ValidateProjectionUnderHaving(expr proc.Expr, inAggregate bool) error {
	return validateProjectionUnderHaving(expr, inAggregate, false, nil)
}

// ValidateProjectionUnderHavingWithProjections recursively verifies that any column reference
// in a projection without GROUP BY is strictly enclosed within an aggregate function or matches a projection alias.
func ValidateProjectionUnderHavingWithProjections(expr proc.Expr, inAggregate bool, projections []proc.Projection) error {
	return validateProjectionUnderHaving(expr, inAggregate, false, projections)
}

func validateProjectionUnderHaving(expr proc.Expr, inAggregate bool, inWindow bool, projections []proc.Projection) error {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *proc.ColumnRefExpr:
		if !inAggregate {
			if projections != nil {
				for _, p := range projections {
					if p.Alias != "" && strings.EqualFold(p.Alias, e.Name) {
						if p.Expr != nil && validateProjectionUnderHaving(p.Expr, inAggregate, inWindow, projections) == nil {
							return nil
						}
						return fmt.Errorf("column %q must appear in the GROUP BY clause or be used in an aggregate function (SQLSTATE 42803)", e.Name)
					}
				}
			}
			return fmt.Errorf("column %q must appear in the GROUP BY clause or be used in an aggregate function (SQLSTATE 42803)", e.Name)
		}
		return nil
	case *proc.FunctionCallExpr:
		if inAggregate && e.HasOver {
			return fmt.Errorf("aggregate function calls cannot contain window function calls (SQLSTATE 42P20)")
		}
		if inAggregate && proc.IsAggregateFunctionName(e.Name) && !e.HasOver {
			return fmt.Errorf("aggregate function calls cannot be nested (SQLSTATE 42803)")
		}
		if e.HasOver {
			return fmt.Errorf("window functions are not allowed in HAVING (SQLSTATE 42803)")
		}
		isAgg := inAggregate
		if proc.IsAggregateFunctionName(e.Name) && !e.HasOver {
			isAgg = true
		}
		for _, arg := range e.Args {
			if err := validateProjectionUnderHaving(arg, isAgg, inWindow, projections); err != nil {
				return err
			}
		}
		if e.Filter != nil {
			if err := validateProjectionUnderHaving(e.Filter, isAgg, inWindow, projections); err != nil {
				return err
			}
		}
		return nil
	case *proc.BinaryExpr:
		if err := validateProjectionUnderHaving(e.Left, inAggregate, inWindow, projections); err != nil {
			return err
		}
		return validateProjectionUnderHaving(e.Right, inAggregate, inWindow, projections)
	case *proc.UnaryExpr:
		return validateProjectionUnderHaving(e.Expr, inAggregate, inWindow, projections)
	case *proc.CastExpr:
		return validateProjectionUnderHaving(e.Expr, inAggregate, inWindow, projections)
	case *proc.IsNullExpr:
		return validateProjectionUnderHaving(e.Expr, inAggregate, inWindow, projections)
	case *proc.CaseExpr:
		if err := validateProjectionUnderHaving(e.Operand, inAggregate, inWindow, projections); err != nil {
			return err
		}
		if err := validateProjectionUnderHaving(e.Else, inAggregate, inWindow, projections); err != nil {
			return err
		}
		for _, b := range e.Whens {
			if err := validateProjectionUnderHaving(b.Condition, inAggregate, inWindow, projections); err != nil {
				return err
			}
			if err := validateProjectionUnderHaving(b.Result, inAggregate, inWindow, projections); err != nil {
				return err
			}
		}
		return nil
	case *proc.InExpr:
		if err := validateProjectionUnderHaving(e.Expr, inAggregate, inWindow, projections); err != nil {
			return err
		}
		for _, itm := range e.List {
			if err := validateProjectionUnderHaving(itm, inAggregate, inWindow, projections); err != nil {
				return err
			}
		}
		return nil
	case *proc.BetweenExpr:
		if err := validateProjectionUnderHaving(e.Expr, inAggregate, inWindow, projections); err != nil {
			return err
		}
		if err := validateProjectionUnderHaving(e.Low, inAggregate, inWindow, projections); err != nil {
			return err
		}
		return validateProjectionUnderHaving(e.High, inAggregate, inWindow, projections)
	case *proc.LikeExpr:
		if err := validateProjectionUnderHaving(e.Expr, inAggregate, inWindow, projections); err != nil {
			return err
		}
		if err := validateProjectionUnderHaving(e.Pattern, inAggregate, inWindow, projections); err != nil {
			return err
		}
		if e.Escape != nil {
			return validateProjectionUnderHaving(e.Escape, inAggregate, inWindow, projections)
		}
		return nil
	case *proc.CollateExpr:
		return validateProjectionUnderHaving(e.Expr, inAggregate, inWindow, projections)
	case *proc.TupleExpr:
		for _, elem := range e.Elements {
			if err := validateProjectionUnderHaving(elem, inAggregate, inWindow, projections); err != nil {
				return err
			}
		}
		return nil
	case *proc.WithinGroupExpr:
		isAgg := inAggregate
		if e.Function != nil {
			isAgg = isAgg || proc.IsAggregateFunctionName(e.Function.Name)
			if err := validateProjectionUnderHaving(e.Function, isAgg, inWindow, projections); err != nil {
				return err
			}
		}
		if e.OrderBy != nil {
			if err := validateProjectionUnderHaving(e.OrderBy, isAgg, inWindow, projections); err != nil {
				return err
			}
		}
		return nil
	case *proc.QuantifiedComparisonExpr:
		if err := validateProjectionUnderHaving(e.Expr, inAggregate, inWindow, projections); err != nil {
			return err
		}
		for _, itm := range e.List {
			if err := validateProjectionUnderHaving(itm, inAggregate, inWindow, projections); err != nil {
				return err
			}
		}
		return nil
	case *proc.ScalarSubqueryExpr, *proc.ExistsExpr:
		return nil
	}
	return nil
}

// collectSubqueriesFromExpr scans an expression and collects all immediate child subquery ASTs.
func collectSubqueriesFromExpr(expr proc.Expr) []*proc.GeneralSelectStmt {
	if expr == nil {
		return nil
	}
	var subqueries []*proc.GeneralSelectStmt
	_ = WalkAST(expr, func(node proc.Expr) error {
		switch n := node.(type) {
		case *proc.ScalarSubqueryExpr:
			if n.Query != nil {
				subqueries = append(subqueries, n.Query)
			}
		case *proc.ExistsExpr:
			if n.Query != nil {
				subqueries = append(subqueries, n.Query)
			}
		case *proc.InExpr:
			if n.Query != nil {
				subqueries = append(subqueries, n.Query)
			}
		case *proc.QuantifiedComparisonExpr:
			if n.Query != nil {
				subqueries = append(subqueries, n.Query)
			}
		}
		return nil
	})
	return subqueries
}

// collectSubqueriesFromQuery scans all clauses of a GeneralSelectStmt to discover nested subquery ASTs.
func collectSubqueriesFromQuery(stmt *proc.GeneralSelectStmt) []*proc.GeneralSelectStmt {
	if stmt == nil {
		return nil
	}
	var subqueries []*proc.GeneralSelectStmt

	if stmt.From.Subquery != nil {
		subqueries = append(subqueries, stmt.From.Subquery)
	}
	for _, j := range stmt.Joins {
		if j.Table.Subquery != nil {
			subqueries = append(subqueries, j.Table.Subquery)
		}
		if j.On != nil {
			subqueries = append(subqueries, collectSubqueriesFromExpr(j.On)...)
		}
	}
	for _, p := range stmt.Projections {
		if p.Expr != nil {
			subqueries = append(subqueries, collectSubqueriesFromExpr(p.Expr)...)
		}
	}
	if stmt.Where != nil {
		subqueries = append(subqueries, collectSubqueriesFromExpr(stmt.Where)...)
	}
	if stmt.Having != nil {
		subqueries = append(subqueries, collectSubqueriesFromExpr(stmt.Having)...)
	}
	for _, g := range stmt.GroupBy {
		subqueries = append(subqueries, collectSubqueriesFromExpr(g)...)
	}
	for _, ob := range stmt.OrderBy {
		if ob.Expr != nil {
			subqueries = append(subqueries, collectSubqueriesFromExpr(ob.Expr)...)
		}
	}
	return subqueries
}

// exprMatchesGroupBy checks if targetExpr matches any expression in groupByExprs (by String() or identifier match).
func exprMatchesGroupBy(targetExpr proc.Expr, groupByExprs []proc.Expr) bool {
	if targetExpr == nil {
		return false
	}
	if colExpr, ok := targetExpr.(*proc.CollateExpr); ok {
		targetExpr = colExpr.Expr
	}
	targetStr := strings.ToLower(strings.TrimSpace(targetExpr.String()))
	for _, g := range groupByExprs {
		if g == nil {
			continue
		}
		if colExpr, ok := g.(*proc.CollateExpr); ok {
			g = colExpr.Expr
		}
		if strings.EqualFold(strings.TrimSpace(g.String()), targetStr) {
			return true
		}
		if tCol, ok := targetExpr.(*proc.ColumnRefExpr); ok {
			if gCol, ok := g.(*proc.ColumnRefExpr); ok {
				tParts := strings.Split(tCol.Name, ".")
				gParts := strings.Split(gCol.Name, ".")
				tLeaf := strings.ToLower(sqlutil.CleanIdentifier(tParts[len(tParts)-1]))
				gLeaf := strings.ToLower(sqlutil.CleanIdentifier(gParts[len(gParts)-1]))
				if tLeaf == gLeaf {
					if len(tParts) == 1 || len(gParts) == 1 || strings.EqualFold(tCol.Name, gCol.Name) {
						return true
					}
				}
			}
		}
	}
	return false
}

// ValidateProjectionUnderGroupBy verifies that every column in the projection expression is either
// contained within the GROUP BY list, enclosed within an aggregate function, or a constant.
func ValidateProjectionUnderGroupBy(expr proc.Expr, groupByExprs []proc.Expr, inAggregate bool) error {
	return validateProjectionUnderGroupBy(expr, groupByExprs, inAggregate, false, nil)
}

// ValidateProjectionUnderGroupByWithProjections verifies that every column in the projection expression is either
// contained within the GROUP BY list, enclosed within an aggregate function, a projection alias, or a constant.
func ValidateProjectionUnderGroupByWithProjections(expr proc.Expr, groupByExprs []proc.Expr, inAggregate bool, projections []proc.Projection) error {
	return validateProjectionUnderGroupBy(expr, groupByExprs, inAggregate, false, projections)
}

func validateProjectionUnderGroupBy(expr proc.Expr, groupByExprs []proc.Expr, inAggregate bool, inWindow bool, projections []proc.Projection) error {
	if expr == nil {
		return nil
	}
	if !inAggregate && exprMatchesGroupBy(expr, groupByExprs) {
		return nil
	}
	switch e := expr.(type) {
	case *proc.ColumnRefExpr:
		if !inAggregate && !exprMatchesGroupBy(e, groupByExprs) {
			if projections != nil {
				for _, p := range projections {
					if p.Alias != "" && strings.EqualFold(p.Alias, e.Name) {
						if p.Expr != nil && validateProjectionUnderGroupBy(p.Expr, groupByExprs, inAggregate, inWindow, projections) == nil {
							return nil
						}
						return fmt.Errorf("column %q must appear in the GROUP BY clause or be used in an aggregate function (SQLSTATE 42803)", e.Name)
					}
				}
			}
			return fmt.Errorf("column %q must appear in the GROUP BY clause or be used in an aggregate function (SQLSTATE 42803)", e.Name)
		}
		return nil
	case *proc.FunctionCallExpr:
		if e.HasOver {
			if inAggregate {
				return fmt.Errorf("aggregate function calls cannot contain window function calls (SQLSTATE 42P20)")
			}
			if inWindow {
				return fmt.Errorf("window function calls cannot be nested (SQLSTATE 42P20)")
			}
			// In a window function, its arguments are evaluated in the grouped context.
			// Therefore, columns inside window function arguments must be in GROUP BY or aggregated.
			for _, arg := range e.Args {
				if err := validateProjectionUnderGroupBy(arg, groupByExprs, inAggregate, true, projections); err != nil {
					return err
				}
			}
			if e.Filter != nil {
				if err := validateProjectionUnderGroupBy(e.Filter, groupByExprs, inAggregate, true, projections); err != nil {
					return err
				}
			}
			return nil
		}

		isAgg := inAggregate
		if proc.IsAggregateFunctionName(e.Name) {
			if inAggregate {
				return fmt.Errorf("aggregate function calls cannot be nested (SQLSTATE 42803)")
			}
			isAgg = true
		}
		for _, arg := range e.Args {
			if err := validateProjectionUnderGroupBy(arg, groupByExprs, isAgg, inWindow, projections); err != nil {
				return err
			}
		}
		if e.Filter != nil {
			if err := validateProjectionUnderGroupBy(e.Filter, groupByExprs, isAgg, inWindow, projections); err != nil {
				return err
			}
		}
		return nil
	case *proc.BinaryExpr:
		if err := validateProjectionUnderGroupBy(e.Left, groupByExprs, inAggregate, inWindow, projections); err != nil {
			return err
		}
		return validateProjectionUnderGroupBy(e.Right, groupByExprs, inAggregate, inWindow, projections)
	case *proc.UnaryExpr:
		return validateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate, inWindow, projections)
	case *proc.CastExpr:
		return validateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate, inWindow, projections)
	case *proc.IsNullExpr:
		return validateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate, inWindow, projections)
	case *proc.CaseExpr:
		if err := validateProjectionUnderGroupBy(e.Operand, groupByExprs, inAggregate, inWindow, projections); err != nil {
			return err
		}
		if err := validateProjectionUnderGroupBy(e.Else, groupByExprs, inAggregate, inWindow, projections); err != nil {
			return err
		}
		for _, b := range e.Whens {
			if err := validateProjectionUnderGroupBy(b.Condition, groupByExprs, inAggregate, inWindow, projections); err != nil {
				return err
			}
			if err := validateProjectionUnderGroupBy(b.Result, groupByExprs, inAggregate, inWindow, projections); err != nil {
				return err
			}
		}
		return nil
	case *proc.InExpr:
		if err := validateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate, inWindow, projections); err != nil {
			return err
		}
		for _, itm := range e.List {
			if err := validateProjectionUnderGroupBy(itm, groupByExprs, inAggregate, inWindow, projections); err != nil {
				return err
			}
		}
		return nil
	case *proc.BetweenExpr:
		if err := validateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate, inWindow, projections); err != nil {
			return err
		}
		if err := validateProjectionUnderGroupBy(e.Low, groupByExprs, inAggregate, inWindow, projections); err != nil {
			return err
		}
		return validateProjectionUnderGroupBy(e.High, groupByExprs, inAggregate, inWindow, projections)
	case *proc.LikeExpr:
		if err := validateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate, inWindow, projections); err != nil {
			return err
		}
		if err := validateProjectionUnderGroupBy(e.Pattern, groupByExprs, inAggregate, inWindow, projections); err != nil {
			return err
		}
		if e.Escape != nil {
			return validateProjectionUnderGroupBy(e.Escape, groupByExprs, inAggregate, inWindow, projections)
		}
		return nil
	case *proc.CollateExpr:
		return validateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate, inWindow, projections)
	case *proc.TupleExpr:
		for _, elem := range e.Elements {
			if err := validateProjectionUnderGroupBy(elem, groupByExprs, inAggregate, inWindow, projections); err != nil {
				return err
			}
		}
		return nil
	case *proc.WithinGroupExpr:
		isAgg := inAggregate
		if e.Function != nil {
			isAgg = isAgg || proc.IsAggregateFunctionName(e.Function.Name)
			if err := validateProjectionUnderGroupBy(e.Function, groupByExprs, isAgg, inWindow, projections); err != nil {
				return err
			}
		}
		if e.OrderBy != nil {
			if err := validateProjectionUnderGroupBy(e.OrderBy, groupByExprs, isAgg, inWindow, projections); err != nil {
				return err
			}
		}
		return nil
	case *proc.QuantifiedComparisonExpr:
		if err := validateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate, inWindow, projections); err != nil {
			return err
		}
		for _, itm := range e.List {
			if err := validateProjectionUnderGroupBy(itm, groupByExprs, inAggregate, inWindow, projections); err != nil {
				return err
			}
		}
		return nil
	case *proc.ScalarSubqueryExpr, *proc.ExistsExpr:
		return nil
	}
	return nil
}

// -----------------------------------------------------------------------------
// DML Statement Validation Contracts (Phase 8: DML Contract Consolidation)
// -----------------------------------------------------------------------------

// ValidateDMLStmt validates semantic invariants and contracts on DML statements (INSERT, UPDATE, DELETE, MERGE).
func ValidateDMLStmt(stmt proc.Statement, schemas map[string]*storage.TableSchema) error {
	if stmt == nil {
		return nil
	}
	switch s := stmt.(type) {
	case *proc.GeneralInsertStmt:
		return validateGeneralInsertStmt(s, schemas)
	case *proc.InsertStmt:
		return validateLegacyInsertStmt(s, schemas)
	case *proc.UpdateWhereStmt:
		return validateUpdateWhereStmt(s, schemas)
	case *proc.UpdateStmt:
		return validateLegacyUpdateStmt(s, schemas)
	case *proc.DeleteWhereStmt:
		return validateDeleteWhereStmt(s, schemas)
	case *proc.DeleteStmt:
		return validateLegacyDeleteStmt(s, schemas)
	case *proc.MergeStmt:
		return validateMergeStmt(s, schemas)
	default:
		return nil
	}
}

func validateDMLTargetTable(rawTable string, schemas map[string]*storage.TableSchema) (*storage.TableSchema, string, error) {
	clean := proc.CleanSchemaPrefix(rawTable)
	lower := strings.ToLower(clean)
	if strings.HasPrefix(lower, "sys.") || strings.HasPrefix(lower, "information_schema.") || strings.HasPrefix(lower, "pg_catalog.") {
		return nil, clean, fmt.Errorf("cannot modify system catalog view %q (read-only)", rawTable)
	}
	if schemas == nil {
		return nil, clean, nil
	}
	schema, ok := schemas[clean]
	if !ok {
		schema, ok = schemas[lower]
	}
	if !ok {
		for k, v := range schemas {
			if strings.EqualFold(k, clean) || (v != nil && strings.EqualFold(v.Name, clean)) {
				schema = v
				ok = true
				break
			}
		}
	}
	if !ok {
		return nil, clean, fmt.Errorf("table %q does not exist", rawTable)
	}
	return schema, clean, nil
}

func validateGeneralInsertStmt(s *proc.GeneralInsertStmt, schemas map[string]*storage.TableSchema) error {
	schema, _, err := validateDMLTargetTable(s.Table, schemas)
	if err != nil {
		return err
	}

	targetCols := s.Columns
	if len(targetCols) == 0 && schema != nil {
		targetCols = make([]string, 0, len(schema.Columns))
		for _, col := range schema.Columns {
			if col.Hidden {
				continue
			}
			targetCols = append(targetCols, col.Name)
		}
	}

	if len(s.Columns) > 0 {
		colSet := make(map[string]bool)
		for _, col := range s.Columns {
			norm := strings.ToLower(proc.CleanSchemaPrefix(col))
			if colSet[norm] {
				return fmt.Errorf("column %q specified more than once in INSERT", col)
			}
			colSet[norm] = true
		}

		if schema != nil {
			for _, col := range s.Columns {
				norm := strings.ToLower(proc.CleanSchemaPrefix(col))
				schemaCol, _, ok := schema.FindColumn(norm)
				if !ok {
					return fmt.Errorf("column %q does not exist in table %q", col, schema.Name)
				}
				if schemaCol.ComputedExpr != "" {
					return fmt.Errorf("cannot insert into computed column %q", col)
				}
			}

			// Validate NOT NULL constraints on missing columns
			for _, col := range schema.Columns {
				if !col.Nullable && col.Default == "" && !col.Identity && col.ComputedExpr == "" && !col.IsRowVersion {
					if !colSet[strings.ToLower(col.Name)] {
						return fmt.Errorf("column %q violates not-null constraint: no default value provided", col.Name)
					}
				}
			}
		}
	}

	// Arity and value validation for VALUES
	if len(s.Tuples) > 0 {
		for _, tuple := range s.Tuples {
			if len(tuple) != len(targetCols) {
				return fmt.Errorf("column count mismatch: expected %d columns, got %d expressions", len(targetCols), len(tuple))
			}
			for _, expr := range tuple {
				if err := CheckDisallowedExprs(expr, ScopeValues); err != nil {
					return err
				}
				if err := validateDMLValueExpr(expr); err != nil {
					return err
				}
			}
		}
	}

	// Arity and query validation for SELECT
	if s.Select != nil {
		if err := ValidateQueryAST(s.Select, schemas); err != nil {
			return err
		}
		hasWildcard := false
		for _, p := range s.Select.Projections {
			if p.Expr != nil {
				if colRef, ok := p.Expr.(*proc.ColumnRefExpr); ok {
					clean := strings.TrimSpace(colRef.Name)
					if clean == "*" || strings.HasSuffix(clean, ".*") {
						hasWildcard = true
						break
					}
				}
			}
		}
		if !hasWildcard && len(s.Select.Projections) != len(targetCols) {
			return fmt.Errorf("column count mismatch: target has %d columns, but query returns %d columns", len(targetCols), len(s.Select.Projections))
		}
	}

	return nil
}

func validateLegacyInsertStmt(s *proc.InsertStmt, schemas map[string]*storage.TableSchema) error {
	schema, _, err := validateDMLTargetTable(s.Table, schemas)
	if err != nil {
		return err
	}

	targetCols := s.Columns
	if len(targetCols) == 0 && schema != nil {
		targetCols = make([]string, 0, len(schema.Columns))
		for _, col := range schema.Columns {
			if col.Hidden {
				continue
			}
			targetCols = append(targetCols, col.Name)
		}
	}

	if len(s.Columns) > 0 {
		colSet := make(map[string]bool)
		for _, col := range s.Columns {
			norm := strings.ToLower(proc.CleanSchemaPrefix(col))
			if colSet[norm] {
				return fmt.Errorf("column %q specified more than once in INSERT", col)
			}
			colSet[norm] = true
		}

		if schema != nil {
			for _, col := range s.Columns {
				norm := strings.ToLower(proc.CleanSchemaPrefix(col))
				schemaCol, _, ok := schema.FindColumn(norm)
				if !ok {
					return fmt.Errorf("column %q does not exist in table %q", col, schema.Name)
				}
				if schemaCol.ComputedExpr != "" {
					return fmt.Errorf("cannot insert into computed column %q", col)
				}
			}

			// Validate NOT NULL constraints on missing columns
			for _, col := range schema.Columns {
				if !col.Nullable && col.Default == "" && !col.Identity && col.ComputedExpr == "" && !col.IsRowVersion {
					if !colSet[strings.ToLower(col.Name)] {
						return fmt.Errorf("column %q violates not-null constraint: no default value provided", col.Name)
					}
				}
			}
		}
	}

	if len(s.Values) != len(targetCols) {
		return fmt.Errorf("column count mismatch: expected %d columns, got %d expressions", len(targetCols), len(s.Values))
	}
	for _, expr := range s.Values {
		if err := CheckDisallowedExprs(expr, ScopeValues); err != nil {
			return err
		}
		if err := validateDMLValueExpr(expr); err != nil {
			return err
		}
	}

	return nil
}

func validateUpdateWhereStmt(s *proc.UpdateWhereStmt, schemas map[string]*storage.TableSchema) error {
	schema, _, err := validateDMLTargetTable(s.Table, schemas)
	if err != nil {
		return err
	}

	if err := validateUpdateAssignments(s.Assignments, schema, schemas); err != nil {
		return err
	}

	if s.Where != nil {
		if err := validateDMLWhereExpr(s.Where, schema, schemas); err != nil {
			return err
		}
	}

	return nil
}

func validateLegacyUpdateStmt(s *proc.UpdateStmt, schemas map[string]*storage.TableSchema) error {
	schema, _, err := validateDMLTargetTable(s.Table, schemas)
	if err != nil {
		return err
	}

	if err := validateUpdateAssignments(s.Assignments, schema, schemas); err != nil {
		return err
	}

	if s.WhereCol != "" && schema != nil {
		norm := strings.ToLower(proc.CleanSchemaPrefix(s.WhereCol))
		if _, _, ok := schema.FindColumn(norm); !ok {
			return fmt.Errorf("column %q does not exist in table %q", s.WhereCol, schema.Name)
		}
	}

	if s.WhereVal != nil {
		if err := validateDMLWhereExpr(s.WhereVal, schema, schemas); err != nil {
			return err
		}
	}

	return nil
}

func validateUpdateAssignments(assignments []proc.UpdateAssignment, schema *storage.TableSchema, schemas map[string]*storage.TableSchema) error {
	colSet := make(map[string]bool)
	for _, a := range assignments {
		norm := strings.ToLower(proc.CleanSchemaPrefix(a.Column))
		if colSet[norm] {
			return fmt.Errorf("column %q specified more than once in UPDATE SET", a.Column)
		}
		colSet[norm] = true

		if schema != nil {
			schemaCol, _, ok := schema.FindColumn(norm)
			if !ok {
				return fmt.Errorf("column %q does not exist in table %q", a.Column, schema.Name)
			}
			if schemaCol.ComputedExpr != "" {
				return fmt.Errorf("cannot update computed column %q", a.Column)
			}
			if schemaCol.IsRowVersion {
				return fmt.Errorf("cannot update timestamp/rowversion column %q", a.Column)
			}
		}

		if err := CheckDisallowedExprs(a.Value, ScopeUpdateSet); err != nil {
			return err
		}
		if err := validateDMLScopeExpr(a.Value, schema, schemas); err != nil {
			return err
		}
	}
	return nil
}

func validateDeleteWhereStmt(s *proc.DeleteWhereStmt, schemas map[string]*storage.TableSchema) error {
	schema, _, err := validateDMLTargetTable(s.Table, schemas)
	if err != nil {
		return err
	}

	if s.Where != nil {
		if err := validateDMLWhereExpr(s.Where, schema, schemas); err != nil {
			return err
		}
	}

	return nil
}

func validateLegacyDeleteStmt(s *proc.DeleteStmt, schemas map[string]*storage.TableSchema) error {
	schema, _, err := validateDMLTargetTable(s.Table, schemas)
	if err != nil {
		return err
	}

	if s.WhereCol != "" && schema != nil {
		norm := strings.ToLower(proc.CleanSchemaPrefix(s.WhereCol))
		if _, _, ok := schema.FindColumn(norm); !ok {
			return fmt.Errorf("column %q does not exist in table %q", s.WhereCol, schema.Name)
		}
	}

	if s.WhereVal != nil {
		if err := validateDMLWhereExpr(s.WhereVal, schema, schemas); err != nil {
			return err
		}
	}

	return nil
}

func validateMergeStmt(s *proc.MergeStmt, schemas map[string]*storage.TableSchema) error {
	// MERGE parsing and validation fallback
	return nil
}

func validateDMLWhereExpr(whereExpr proc.Expr, schema *storage.TableSchema, schemas map[string]*storage.TableSchema) error {
	if whereExpr == nil {
		return nil
	}
	if err := ValidateCommonFilterExpr(whereExpr, ScopeWhere, schemas); err != nil {
		return err
	}
	return validateDMLScopeExpr(whereExpr, schema, schemas)
}

func validateDMLValueExpr(expr proc.Expr) error {
	if err := CheckDisallowedExprs(expr, ScopeValues); err != nil {
		return err
	}
	return WalkAST(expr, func(node proc.Expr) error {
		col, ok := node.(*proc.ColumnRefExpr)
		if !ok {
			return nil
		}
		name := col.Name
		if strings.HasPrefix(name, "@") || proc.IsBuiltinFunction(name) ||
			strings.EqualFold(name, "NULL") || strings.EqualFold(name, "DEFAULT") ||
			strings.EqualFold(name, "TRUE") || strings.EqualFold(name, "FALSE") {
			return nil
		}
		return fmt.Errorf("column %q does not exist in VALUES expression", name)
	})
}

func validateDMLScopeExpr(expr proc.Expr, schema *storage.TableSchema, schemas map[string]*storage.TableSchema) error {
	if expr == nil {
		return nil
	}
	if err := ValidateSubqueryExprs(expr, schemas); err != nil {
		return err
	}
	return WalkAST(expr, func(node proc.Expr) error {
		switch n := node.(type) {
		case *proc.ColumnRefExpr:
			name := n.Name
			if strings.HasPrefix(name, "@") || proc.IsBuiltinFunction(name) ||
				strings.EqualFold(name, "NULL") || strings.EqualFold(name, "DEFAULT") ||
				strings.EqualFold(name, "TRUE") || strings.EqualFold(name, "FALSE") ||
				name == "*" {
				return nil
			}
			if schema != nil {
				colName := proc.CleanSchemaPrefix(name)
				if _, _, ok := schema.FindColumn(colName); !ok {
					return fmt.Errorf("column %q does not exist in table %q", name, schema.Name)
				}
			}
		}
		return nil
	})
}
