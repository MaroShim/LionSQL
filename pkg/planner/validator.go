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
	if stmt == nil {
		return nil
	}

	// 1. WHERE clause validation
	if stmt.Where != nil {
		if err := CheckDisallowedExprs(stmt.Where, ScopeWhere); err != nil {
			return err
		}
		if err := WalkAST(stmt.Where, func(n proc.Expr) error {
			if inExpr, ok := n.(*proc.InExpr); ok && inExpr.Query != nil {
				if len(inExpr.Query.Projections) > 1 {
					return fmt.Errorf("subquery must return only one column, returned %d", len(inExpr.Query.Projections))
				}
			}
			return nil
		}); err != nil {
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
				if err := ValidateProjectionUnderHaving(p.Expr, false); err != nil {
					return err
				}
			}
		}
	}

	// Constant domain bounds validation (NTILE, PERCENTILE_CONT)
	for _, p := range stmt.Projections {
		if p.Expr != nil {
			if err := ValidateConstantDomainBounds(p.Expr); err != nil {
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
		scope := NewTableScopeFromMap(schemas)
		if stmt.From.Name != "" {
			scope.Register(stmt.From.Name, stmt.From.Alias, schemas[stmt.From.Name])
		}
		for _, j := range stmt.Joins {
			scope.Register(j.Table.Name, j.Table.Alias, schemas[j.Table.Name])
		}
		for _, p := range stmt.Projections {
			if p.Alias != "" {
				scope.AddProjectionAlias(p.Alias)
			}
		}
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
		// ORDER BY
		for _, ob := range stmt.OrderBy {
			if err := scope.ValidateOrderByExpr(ob.Expr); err != nil {
				return err
			}
		}
		// GROUP BY
		for _, gb := range stmt.GroupBy {
			if err := scope.ValidateExpr(gb); err != nil {
				return err
			}
		}
		// Incremental JOIN ON Scope validation
		joinScope := NewTableScopeFromMap(schemas)
		if stmt.From.Name != "" {
			joinScope.Register(stmt.From.Name, stmt.From.Alias, schemas[stmt.From.Name])
		}
		for _, j := range stmt.Joins {
			joinScope.Register(j.Table.Name, j.Table.Alias, schemas[j.Table.Name])
			if j.On != nil {
				if err := joinScope.ValidateExpr(j.On); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// TableScope manages visible table schemas and aliases within a query scope.
type TableScope struct {
	schemas           map[string]*storage.TableSchema
	uniqueTables      []*storage.TableSchema
	projectionAliases map[string]bool
}

// NewTableScope creates an empty TableScope.
func NewTableScope() *TableScope {
	return &TableScope{
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
		scope.Register(name, "", sch)
	}
	return scope
}

// Register registers a table name and optional alias into the scope.
func (s *TableScope) Register(table, alias string, schema *storage.TableSchema) {
	if s == nil {
		return
	}
	cleanTbl := strings.ToLower(sqlutil.CleanIdentifier(table))
	bareTbl := strings.ToLower(proc.CleanSchemaPrefix(table))
	cleanAlias := strings.ToLower(sqlutil.CleanIdentifier(alias))

	if cleanAlias != "" {
		s.schemas[cleanAlias] = schema
	}
	if cleanTbl != "" {
		s.schemas[cleanTbl] = schema
	}
	if bareTbl != "" && bareTbl != cleanTbl {
		s.schemas[bareTbl] = schema
	}

	if schema != nil {
		found := false
		for _, u := range s.uniqueTables {
			if u == schema || strings.EqualFold(u.Name, schema.Name) {
				found = true
				break
			}
		}
		if !found {
			s.uniqueTables = append(s.uniqueTables, schema)
		}
	}
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
	return s != nil && (len(s.schemas) > 0 || len(s.uniqueTables) > 0)
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
						return fmt.Errorf("table or alias %q does not exist in query scope", parts[last-1])
					}
				}
				return nil
			}

			if len(s.schemas) > 0 {
				sch, ok := s.schemas[tblPart]
				if !ok {
					return fmt.Errorf("table or alias %q does not exist in query scope", parts[last-1])
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
		if allowProjectionAlias && s.projectionAliases[cleanCol] {
			return nil
		}

		if len(s.uniqueTables) > 0 {
			matchCount := 0
			for _, sch := range s.uniqueTables {
				if sch == nil {
					continue
				}
				if _, _, ok := sch.FindColumn(cleanCol); ok {
					matchCount++
				}
			}
			if matchCount > 1 {
				return fmt.Errorf("column reference %q is ambiguous", raw)
			}
			if matchCount == 0 {
				return fmt.Errorf("column %q does not exist", raw)
			}
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
