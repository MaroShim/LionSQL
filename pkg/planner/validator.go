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
				// Window functions are disallowed in WHERE, HAVING, GROUP BY, and JOIN ON
				if scope&(ScopeWhere|ScopeHaving|ScopeGroupBy|ScopeJoinOn) != 0 {
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

	scope := NewTableScopeWithParent(parentScope)
	if len(schemas) > 0 {
		if stmt.From.Name != "" {
			sch := findSchemaFromMap(schemas, stmt.From.Name)
			if sch == nil {
				return fmt.Errorf("table not found: %s", stmt.From.Name)
			}
			if err := scope.Register(stmt.From.Name, stmt.From.Alias, sch); err != nil {
				return err
			}
		} else if stmt.From.Subquery != nil && stmt.From.Alias != "" {
			if err := scope.Register("", stmt.From.Alias, nil); err != nil {
				return err
			}
		}
		for _, j := range stmt.Joins {
			if j.Table.Name != "" {
				sch := findSchemaFromMap(schemas, j.Table.Name)
				if sch == nil {
					return fmt.Errorf("table not found: %s", j.Table.Name)
				}
				if err := scope.Register(j.Table.Name, j.Table.Alias, sch); err != nil {
					return err
				}
			} else if j.Table.Subquery != nil && j.Table.Alias != "" {
				if err := scope.Register("", j.Table.Alias, nil); err != nil {
					return err
				}
			}
		}
		for _, p := range stmt.Projections {
			if p.Alias != "" {
				scope.AddProjectionAlias(p.Alias)
			}
		}
	}

	// 1. WHERE clause validation
	if stmt.Where != nil {
		if err := CheckDisallowedExprs(stmt.Where, ScopeWhere); err != nil {
			return err
		}
		if err := ValidateNoNestedAggregates(stmt.Where); err != nil {
			return err
		}
		if err := ValidateSubqueryColumnCount(stmt.Where); err != nil {
			return err
		}
	}

	// 2. JOIN ON conditions validation
	for _, j := range stmt.Joins {
		if j.On != nil {
			if err := CheckDisallowedExprs(j.On, ScopeJoinOn); err != nil {
				return err
			}
			if err := ValidateNoNestedAggregates(j.On); err != nil {
				return err
			}
			if err := ValidateSubqueryColumnCount(j.On); err != nil {
				return err
			}
		}
	}

	// 3. GROUP BY validation
	if len(stmt.GroupBy) > 0 {
		for _, g := range stmt.GroupBy {
			if err := CheckDisallowedExprs(g, ScopeGroupBy); err != nil {
				return err
			}
			if err := ValidateNoNestedAggregates(g); err != nil {
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
				if ord < 1 || ord > len(stmt.Projections) {
					return fmt.Errorf("GROUP BY position %d is not in select list", ord)
				}
				proj := stmt.Projections[ord-1]
				if hasTopLevelAggregateExpr(proj.Expr) {
					return fmt.Errorf("aggregate functions are not allowed in GROUP BY (cannot group by aggregate column position %d)", ord)
				}
			}
		}
		if len(schemas) > 0 {
			for _, g := range stmt.GroupBy {
				if err := scope.ValidateGroupByExpr(g); err != nil {
					return err
				}
			}

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
					for _, p := range stmt.Projections {
						if p.Alias != "" && strings.EqualFold(sqlutil.CleanIdentifier(p.Alias), cleanName) {
							resolvedGroupBy = append(resolvedGroupBy, p.Expr)
						}
					}
				}
			}

			// Validate that all projection columns are either in GROUP BY, aggregated, or constants
			for _, p := range stmt.Projections {
				if p.Expr != nil && !proc.IsConstantExpr(p.Expr) {
					if err := ValidateProjectionUnderGroupBy(p.Expr, resolvedGroupBy, false); err != nil {
						return err
					}
				}
			}
		}
	}

	// 4. HAVING validation
	if stmt.Having != nil {
		if err := CheckDisallowedExprs(stmt.Having, ScopeHaving); err != nil {
			return err
		}
		if err := ValidateNoNestedAggregates(stmt.Having); err != nil {
			return err
		}
		if err := ValidateSubqueryColumnCount(stmt.Having); err != nil {
			return err
		}
		if len(stmt.GroupBy) == 0 {
			for _, p := range stmt.Projections {
				if p.Expr != nil && !proc.IsConstantExpr(p.Expr) {
					if err := ValidateProjectionUnderHaving(p.Expr, false); err != nil {
						return err
					}
				}
			}
		}
	}

	// Constant domain bounds validation (NTILE, PERCENTILE_CONT), nested aggregates, and subquery column count
	for _, p := range stmt.Projections {
		if p.Expr != nil {
			if err := ValidateConstantDomainBounds(p.Expr); err != nil {
				return err
			}
			if err := ValidateNoNestedAggregates(p.Expr); err != nil {
				return err
			}
			if err := ValidateSubqueryColumnCount(p.Expr); err != nil {
				return err
			}
		}
	}
	if stmt.Where != nil {
		if err := ValidateConstantDomainBounds(stmt.Where); err != nil {
			return err
		}
	}

	// 5. Ambiguous Column References across JOINs and Qualified Column Binding
	if len(schemas) > 0 {
		// Projections
		for _, p := range stmt.Projections {
			if err := scope.ValidateExpr(p.Expr); err != nil {
				return err
			}
		}
		// WHERE
		if stmt.Where != nil {
			if err := scope.ValidateExpr(stmt.Where); err != nil {
				return err
			}
		}
		// HAVING
		if stmt.Having != nil {
			if err := scope.ValidateHavingExpr(stmt.Having); err != nil {
				return err
			}
		}
		// ORDER BY
		for _, ob := range stmt.OrderBy {
			if err := scope.ValidateOrderByExpr(ob.Expr); err != nil {
				return err
			}
		}
		// GROUP BY
		for _, gb := range stmt.GroupBy {
			if err := scope.ValidateGroupByExpr(gb); err != nil {
				return err
			}
		}
		// Incremental JOIN ON Scope validation
		joinScope := NewTableScopeWithParent(parentScope)
		if stmt.From.Name != "" {
			if err := joinScope.Register(stmt.From.Name, stmt.From.Alias, findSchemaFromMap(schemas, stmt.From.Name)); err != nil {
				return err
			}
		} else if stmt.From.Subquery != nil && stmt.From.Alias != "" {
			if err := joinScope.Register("", stmt.From.Alias, nil); err != nil {
				return err
			}
		}
		for _, j := range stmt.Joins {
			if j.Table.Name != "" {
				if err := joinScope.Register(j.Table.Name, j.Table.Alias, findSchemaFromMap(schemas, j.Table.Name)); err != nil {
					return err
				}
			} else if j.Table.Subquery != nil && j.Table.Alias != "" {
				if err := joinScope.Register("", j.Table.Alias, nil); err != nil {
					return err
				}
			}
			if j.On != nil {
				if err := joinScope.ValidateExpr(j.On); err != nil {
					return err
				}
			}
		}
	}

	// 6. Recursively validate all nested subquery ASTs with current scope as parent
	childSubqueries := collectSubqueriesFromQuery(stmt)
	for _, sub := range childSubqueries {
		if err := validateQueryASTInternal(sub, schemas, scope, depth+1); err != nil {
			return err
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
				if innerFn, isFn := inner.(*proc.FunctionCallExpr); isFn && proc.IsAggregateFunctionName(innerFn.Name) {
					return fmt.Errorf("aggregate function calls cannot be nested (SQLSTATE 42803)")
				}
				return nil
			}); err != nil {
				return err
			}
		}
		if fn.Filter != nil {
			if err := WalkAST(fn.Filter, func(inner proc.Expr) error {
				if innerFn, isFn := inner.(*proc.FunctionCallExpr); isFn && proc.IsAggregateFunctionName(innerFn.Name) {
					return fmt.Errorf("aggregate function calls cannot be nested (SQLSTATE 42803)")
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
				if lit.Val.Type == storage.TypeFloat {
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
			return fmt.Errorf("column %q must appear in the GROUP BY clause or be used in an aggregate function", e.Name)
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
			return fmt.Errorf("column %q must appear in the GROUP BY clause or be used in an aggregate function", e.Name)
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
