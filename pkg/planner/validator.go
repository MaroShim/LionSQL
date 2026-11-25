package planner

import (
	"fmt"
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
				// Window functions are disallowed in WHERE, HAVING, GROUP BY, JOIN ON, and UPDATE SET
				if scope&(ScopeWhere|ScopeHaving|ScopeGroupBy|ScopeJoinOn|ScopeUpdateSet) != 0 {
					return fmt.Errorf("window functions are not allowed in %s", scopeName(scope))
				}
			} else if proc.IsAggregateFunctionName(n.Name) {
				// Aggregate functions are disallowed in WHERE, JOIN ON, GROUP BY, and UPDATE SET
				if scope&(ScopeWhere|ScopeJoinOn|ScopeGroupBy|ScopeUpdateSet) != 0 {
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
	if err := validateWhereClause(stmt.Where, scope); err != nil {
		return err
	}

	// 4. GROUP BY Clause Validation (with Table Column Primacy)
	resolvedGroupBy, err := validateGroupByClause(stmt, scope)
	if err != nil {
		return err
	}

	// 5. HAVING Clause Validation
	if err := validateHavingClause(stmt.Having, scope); err != nil {
		return err
	}

	hasGroupBy := len(stmt.GroupBy) > 0
	hasHavingOnly := !hasGroupBy && stmt.Having != nil

	// 6. PROJECTIONS Clause Validation
	if err := validateProjections(stmt.Projections, resolvedGroupBy, hasGroupBy, hasHavingOnly, scope); err != nil {
		return err
	}

	// 7. ORDER BY Clause Validation
	if err := validateOrderByClause(stmt.OrderBy, stmt.Projections, stmt.Distinct, resolvedGroupBy, hasGroupBy, hasHavingOnly, scope); err != nil {
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
			if err := scope.Register("", stmt.From.Alias, nil); err != nil {
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
				if err := scope.Register("", j.Table.Alias, nil); err != nil {
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
			if err := joinScope.Register("", stmt.From.Alias, nil); err != nil {
				return nil, err
			}
		} else if stmt.From.Name != "" {
			if err := joinScope.Register(stmt.From.Name, stmt.From.Alias, findSchemaFromMap(schemas, stmt.From.Name)); err != nil {
				return nil, err
			}
		}
		for _, j := range stmt.Joins {
			if j.Table.Subquery != nil {
				if err := joinScope.Register("", j.Table.Alias, nil); err != nil {
					return nil, err
				}
			} else if j.Table.Name != "" {
				if err := joinScope.Register(j.Table.Name, j.Table.Alias, findSchemaFromMap(schemas, j.Table.Name)); err != nil {
					return nil, err
				}
			}
			if j.On != nil {
				if err := CheckDisallowedExprs(j.On, ScopeJoinOn); err != nil {
					return nil, err
				}
				if err := ValidateNoNestedAggregates(j.On); err != nil {
					return nil, err
				}
				if err := ValidateSubqueryColumnCount(j.On); err != nil {
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
				if err := CheckDisallowedExprs(j.On, ScopeJoinOn); err != nil {
					return nil, err
				}
				if err := ValidateNoNestedAggregates(j.On); err != nil {
					return nil, err
				}
				if err := ValidateSubqueryColumnCount(j.On); err != nil {
					return nil, err
				}
			}
		}
	}

	return scope, nil
}

func validateWhereClause(where proc.Expr, scope *TableScope) error {
	if where == nil {
		return nil
	}
	if err := CheckDisallowedExprs(where, ScopeWhere); err != nil {
		return err
	}
	if err := ValidateConstantDomainBounds(where); err != nil {
		return err
	}
	if err := ValidateNoNestedAggregates(where); err != nil {
		return err
	}
	if err := ValidateNoNestedWindowFunctions(where); err != nil {
		return err
	}
	if err := ValidateSubqueryColumnCount(where); err != nil {
		return err
	}
	if scope != nil && scope.HasSchemas() {
		if err := scope.ValidateExpr(where); err != nil {
			return err
		}
	}
	return nil
}

func validateGroupByClause(stmt *proc.GeneralSelectStmt, scope *TableScope) ([]proc.Expr, error) {
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

func validateHavingClause(having proc.Expr, scope *TableScope) error {
	if having == nil {
		return nil
	}
	if err := CheckDisallowedExprs(having, ScopeHaving); err != nil {
		return err
	}
	if err := ValidateNoNestedAggregates(having); err != nil {
		return err
	}
	if err := ValidateNoNestedWindowFunctions(having); err != nil {
		return err
	}
	if err := ValidateSubqueryColumnCount(having); err != nil {
		return err
	}
	if scope != nil && scope.HasSchemas() {
		if err := scope.ValidateHavingExpr(having); err != nil {
			return err
		}
	}
	return nil
}

func validateProjections(projections []proc.Projection, resolvedGroupBy []proc.Expr, hasGroupBy bool, hasHavingOnly bool, scope *TableScope) error {
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
		if err := ValidateSubqueryColumnCount(p.Expr); err != nil {
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

func validateOrderByClause(orderBy []proc.OrderByItem, projections []proc.Projection, isDistinct bool, resolvedGroupBy []proc.Expr, hasGroupBy bool, hasHavingOnly bool, scope *TableScope) error {
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
		if err := ValidateNoNestedAggregates(ob.Expr); err != nil {
			return err
		}
		if err := ValidateNoNestedWindowFunctions(ob.Expr); err != nil {
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
}

// TableScope manages visible table schemas and aliases within a query scope.
type TableScope struct {
	parent            *TableScope
	schemas           map[string]*storage.TableSchema
	instances         []TableInstance
	projectionAliases map[string]bool
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
	}
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

// Register registers a table name and optional alias into the scope.
// Enforces table alias shadowing: when an alias is specified, the original table name
// is shadowed and hidden from the scope.
// Rejects duplicate table names or duplicate aliases in the same scope.
func (s *TableScope) Register(table, alias string, schema *storage.TableSchema) error {
	if s == nil {
		return nil
	}
	cleanTbl := strings.ToLower(sqlutil.CleanIdentifier(table))
	bareTbl := strings.ToLower(proc.CleanSchemaPrefix(table))
	cleanAlias := strings.ToLower(sqlutil.CleanIdentifier(alias))

	if cleanAlias != "" {
		if _, exists := s.schemas[cleanAlias]; exists {
			return fmt.Errorf("table name or alias %q specified more than once", alias)
		}
		s.schemas[cleanAlias] = schema
		// Original table name is shadowed!
		s.instances = append(s.instances, TableInstance{
			Alias:        cleanAlias,
			OriginalName: cleanTbl,
			Schema:       schema,
			IsAliased:    true,
		})
	} else if cleanTbl != "" {
		if _, exists := s.schemas[cleanTbl]; exists {
			return fmt.Errorf("table name %q specified more than once", table)
		}
		s.schemas[cleanTbl] = schema
		if bareTbl != "" && bareTbl != cleanTbl {
			s.schemas[bareTbl] = schema
		}
		s.instances = append(s.instances, TableInstance{
			Alias:        cleanTbl,
			OriginalName: cleanTbl,
			Schema:       schema,
			IsAliased:    false,
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

func (s *TableScope) validateExprInternal(expr proc.Expr, allowProjectionAlias bool) error {
	if s == nil || expr == nil || !s.HasSchemas() {
		return nil
	}
	return WalkAST(expr, func(node proc.Expr) error {
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

			if colPart == "*" {
				if len(s.schemas) > 0 {
					if _, ok := s.schemas[tblPart]; !ok {
						foundInParent := false
						for p := s.parent; p != nil; p = p.parent {
							if _, pok := p.schemas[tblPart]; pok {
								foundInParent = true
								break
							}
						}
						if !foundInParent {
							return fmt.Errorf("table or alias %q does not exist in query scope", parts[last-1])
						}
					}
				}
				return nil
			}

			if len(s.schemas) > 0 {
				sch, ok := s.schemas[tblPart]
				if !ok {
					foundInParent := false
					for p := s.parent; p != nil; p = p.parent {
						if psch, pok := p.schemas[tblPart]; pok {
							sch = psch
							foundInParent = true
							break
						}
					}
					if !foundInParent {
						return fmt.Errorf("table or alias %q does not exist in query scope", parts[last-1])
					}
				}
				if sch != nil {
					if _, _, found := sch.FindColumn(colPart); !found {
						return fmt.Errorf("column %q does not exist in table or alias %q", parts[last], parts[last-1])
					}
				}
			}
			return nil
		}

		// Unqualified column reference
		cleanCol := strings.ToLower(sqlutil.CleanIdentifier(raw))

		if len(s.instances) > 0 {
			matchCount := 0
			for _, inst := range s.instances {
				if inst.Schema == nil {
					continue
				}
				if _, _, ok := inst.Schema.FindColumn(cleanCol); ok {
					matchCount++
				}
			}
			if matchCount > 1 {
				return fmt.Errorf("column reference %q is ambiguous", raw)
			}
			if matchCount == 1 {
				return nil
			}
			if matchCount == 0 {
				// Table column strictly takes precedence over projection alias:
				// Only fall back to projection alias if column is not found in table instances.
				if allowProjectionAlias && s.projectionAliases[cleanCol] {
					return nil
				}
				foundInParent := false
				for p := s.parent; p != nil; p = p.parent {
					pmatchCount := 0
					for _, pinst := range p.instances {
						if pinst.Schema != nil {
							if _, _, ok := pinst.Schema.FindColumn(cleanCol); ok {
								pmatchCount++
							}
						}
					}
					if pmatchCount > 1 {
						return fmt.Errorf("column reference %q is ambiguous", raw)
					}
					if pmatchCount == 1 {
						foundInParent = true
						break
					}
				}
				if !foundInParent {
					return fmt.Errorf("column %q does not exist", raw)
				}
			}
			return nil
		}

		if allowProjectionAlias && s.projectionAliases[cleanCol] {
			return nil
		}
		return nil
	})
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
		if !ok || !proc.IsAggregateFunctionName(fn.Name) {
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

// ValidateSubqueryColumnCount verifies that scalar subqueries and IN subqueries return exactly one column.
func ValidateSubqueryColumnCount(expr proc.Expr) error {
	if expr == nil {
		return nil
	}
	return WalkAST(expr, func(n proc.Expr) error {
		if inExpr, ok := n.(*proc.InExpr); ok && inExpr.Query != nil {
			if len(inExpr.Query.Projections) > 1 {
				return fmt.Errorf("subquery must return only one column, returned %d (SQLSTATE 42601)", len(inExpr.Query.Projections))
			}
		}
		if sub, ok := n.(*proc.ScalarSubqueryExpr); ok && sub.Query != nil {
			if len(sub.Query.Projections) > 1 {
				return fmt.Errorf("subquery must return only one column, returned %d (SQLSTATE 42601)", len(sub.Query.Projections))
			}
		}
		if qc, ok := n.(*proc.QuantifiedComparisonExpr); ok && qc.Query != nil {
			if len(qc.Query.Projections) > 1 {
				return fmt.Errorf("subquery must return only one column, returned %d (SQLSTATE 42601)", len(qc.Query.Projections))
			}
		}
		return nil
	})
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

// ValidateConstantDomainBounds checks constant argument bounds for functions such as NTILE and PERCENTILE_CONT.
func ValidateConstantDomainBounds(expr proc.Expr) error {
	if expr == nil {
		return nil
	}
	return WalkAST(expr, func(node proc.Expr) error {
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
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *proc.ColumnRefExpr:
		if !inAggregate {
			return fmt.Errorf("column %q must appear in the GROUP BY clause or be used in an aggregate function (SQLSTATE 42803)", e.Name)
		}
		return nil
	case *proc.FunctionCallExpr:
		isAgg := inAggregate || proc.IsAggregateFunctionName(e.Name)
		for _, arg := range e.Args {
			if err := ValidateProjectionUnderHaving(arg, isAgg); err != nil {
				return err
			}
		}
		if e.Filter != nil {
			if err := ValidateProjectionUnderHaving(e.Filter, isAgg); err != nil {
				return err
			}
		}
		return nil
	case *proc.BinaryExpr:
		if err := ValidateProjectionUnderHaving(e.Left, inAggregate); err != nil {
			return err
		}
		return ValidateProjectionUnderHaving(e.Right, inAggregate)
	case *proc.UnaryExpr:
		return ValidateProjectionUnderHaving(e.Expr, inAggregate)
	case *proc.CastExpr:
		return ValidateProjectionUnderHaving(e.Expr, inAggregate)
	case *proc.IsNullExpr:
		return ValidateProjectionUnderHaving(e.Expr, inAggregate)
	case *proc.CaseExpr:
		if err := ValidateProjectionUnderHaving(e.Operand, inAggregate); err != nil {
			return err
		}
		if err := ValidateProjectionUnderHaving(e.Else, inAggregate); err != nil {
			return err
		}
		for _, b := range e.Whens {
			if err := ValidateProjectionUnderHaving(b.Condition, inAggregate); err != nil {
				return err
			}
			if err := ValidateProjectionUnderHaving(b.Result, inAggregate); err != nil {
				return err
			}
		}
		return nil
	case *proc.InExpr:
		if err := ValidateProjectionUnderHaving(e.Expr, inAggregate); err != nil {
			return err
		}
		for _, itm := range e.List {
			if err := ValidateProjectionUnderHaving(itm, inAggregate); err != nil {
				return err
			}
		}
		return nil
	case *proc.BetweenExpr:
		if err := ValidateProjectionUnderHaving(e.Expr, inAggregate); err != nil {
			return err
		}
		if err := ValidateProjectionUnderHaving(e.Low, inAggregate); err != nil {
			return err
		}
		return ValidateProjectionUnderHaving(e.High, inAggregate)
	case *proc.LikeExpr:
		if err := ValidateProjectionUnderHaving(e.Expr, inAggregate); err != nil {
			return err
		}
		if err := ValidateProjectionUnderHaving(e.Pattern, inAggregate); err != nil {
			return err
		}
		if e.Escape != nil {
			return ValidateProjectionUnderHaving(e.Escape, inAggregate)
		}
		return nil
	case *proc.CollateExpr:
		return ValidateProjectionUnderHaving(e.Expr, inAggregate)
	case *proc.TupleExpr:
		for _, elem := range e.Elements {
			if err := ValidateProjectionUnderHaving(elem, inAggregate); err != nil {
				return err
			}
		}
		return nil
	case *proc.WithinGroupExpr:
		isAgg := inAggregate
		if e.Function != nil {
			isAgg = isAgg || proc.IsAggregateFunctionName(e.Function.Name)
			if err := ValidateProjectionUnderHaving(e.Function, isAgg); err != nil {
				return err
			}
		}
		if e.OrderBy != nil {
			if err := ValidateProjectionUnderHaving(e.OrderBy, isAgg); err != nil {
				return err
			}
		}
		return nil
	case *proc.QuantifiedComparisonExpr:
		if err := ValidateProjectionUnderHaving(e.Expr, inAggregate); err != nil {
			return err
		}
		for _, itm := range e.List {
			if err := ValidateProjectionUnderHaving(itm, inAggregate); err != nil {
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
	if expr == nil {
		return nil
	}
	if !inAggregate && exprMatchesGroupBy(expr, groupByExprs) {
		return nil
	}
	switch e := expr.(type) {
	case *proc.ColumnRefExpr:
		if !inAggregate && !exprMatchesGroupBy(e, groupByExprs) {
			return fmt.Errorf("column %q must appear in the GROUP BY clause or be used in an aggregate function (SQLSTATE 42803)", e.Name)
		}
		return nil
	case *proc.FunctionCallExpr:
		isAgg := inAggregate || proc.IsAggregateFunctionName(e.Name) || e.HasOver
		for _, arg := range e.Args {
			if err := ValidateProjectionUnderGroupBy(arg, groupByExprs, isAgg); err != nil {
				return err
			}
		}
		if e.Filter != nil {
			if err := ValidateProjectionUnderGroupBy(e.Filter, groupByExprs, isAgg); err != nil {
				return err
			}
		}
		return nil
	case *proc.BinaryExpr:
		if err := ValidateProjectionUnderGroupBy(e.Left, groupByExprs, inAggregate); err != nil {
			return err
		}
		return ValidateProjectionUnderGroupBy(e.Right, groupByExprs, inAggregate)
	case *proc.UnaryExpr:
		return ValidateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate)
	case *proc.CastExpr:
		return ValidateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate)
	case *proc.IsNullExpr:
		return ValidateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate)
	case *proc.CaseExpr:
		if err := ValidateProjectionUnderGroupBy(e.Operand, groupByExprs, inAggregate); err != nil {
			return err
		}
		if err := ValidateProjectionUnderGroupBy(e.Else, groupByExprs, inAggregate); err != nil {
			return err
		}
		for _, b := range e.Whens {
			if err := ValidateProjectionUnderGroupBy(b.Condition, groupByExprs, inAggregate); err != nil {
				return err
			}
			if err := ValidateProjectionUnderGroupBy(b.Result, groupByExprs, inAggregate); err != nil {
				return err
			}
		}
		return nil
	case *proc.InExpr:
		if err := ValidateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate); err != nil {
			return err
		}
		for _, itm := range e.List {
			if err := ValidateProjectionUnderGroupBy(itm, groupByExprs, inAggregate); err != nil {
				return err
			}
		}
		return nil
	case *proc.BetweenExpr:
		if err := ValidateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate); err != nil {
			return err
		}
		if err := ValidateProjectionUnderGroupBy(e.Low, groupByExprs, inAggregate); err != nil {
			return err
		}
		return ValidateProjectionUnderGroupBy(e.High, groupByExprs, inAggregate)
	case *proc.LikeExpr:
		if err := ValidateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate); err != nil {
			return err
		}
		if err := ValidateProjectionUnderGroupBy(e.Pattern, groupByExprs, inAggregate); err != nil {
			return err
		}
		if e.Escape != nil {
			return ValidateProjectionUnderGroupBy(e.Escape, groupByExprs, inAggregate)
		}
		return nil
	case *proc.CollateExpr:
		return ValidateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate)
	case *proc.TupleExpr:
		for _, elem := range e.Elements {
			if err := ValidateProjectionUnderGroupBy(elem, groupByExprs, inAggregate); err != nil {
				return err
			}
		}
		return nil
	case *proc.WithinGroupExpr:
		isAgg := inAggregate
		if e.Function != nil {
			isAgg = isAgg || proc.IsAggregateFunctionName(e.Function.Name)
			if err := ValidateProjectionUnderGroupBy(e.Function, groupByExprs, isAgg); err != nil {
				return err
			}
		}
		if e.OrderBy != nil {
			if err := ValidateProjectionUnderGroupBy(e.OrderBy, groupByExprs, isAgg); err != nil {
				return err
			}
		}
		return nil
	case *proc.QuantifiedComparisonExpr:
		if err := ValidateProjectionUnderGroupBy(e.Expr, groupByExprs, inAggregate); err != nil {
			return err
		}
		for _, itm := range e.List {
			if err := ValidateProjectionUnderGroupBy(itm, groupByExprs, inAggregate); err != nil {
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
		if len(s.Select.Projections) != len(targetCols) {
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
	if err := CheckDisallowedExprs(whereExpr, ScopeWhere); err != nil {
		return err
	}
	return validateDMLScopeExpr(whereExpr, schema, schemas)
}

func validateDMLValueExpr(expr proc.Expr) error {
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
	return WalkAST(expr, func(node proc.Expr) error {
		switch n := node.(type) {
		case *proc.InExpr:
			if n.Query != nil {
				if len(n.Query.Projections) != 1 {
					return fmt.Errorf("subquery must return only one column, got %d", len(n.Query.Projections))
				}
				if err := ValidateQueryAST(n.Query, schemas); err != nil {
					return err
				}
			}
		case *proc.ScalarSubqueryExpr:
			if len(n.Query.Projections) != 1 {
				return fmt.Errorf("subquery must return only one column, got %d", len(n.Query.Projections))
			}
			if err := ValidateQueryAST(n.Query, schemas); err != nil {
				return err
			}
		case *proc.ExistsExpr:
			if err := ValidateQueryAST(n.Query, schemas); err != nil {
				return err
			}
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
