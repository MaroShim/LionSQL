package planner

import (
	"fmt"
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// ScopeContext represents a hierarchical query scope (root query or nested subquery).
type ScopeContext struct {
	Parent      *ScopeContext
	OuterLevel  int
	Tables      []BoundTableRef
	TableLookup map[string]int // lowercase table/alias -> TableID in this scope
	SlotOffsets []int          // TableID -> starting slot in joined Tuple.Values
	Scope       *TableScope
}

func newScopeContext(parent *ScopeContext) *ScopeContext {
	outerLevel := 0
	var parentScope *TableScope
	if parent != nil {
		outerLevel = parent.OuterLevel + 1
		parentScope = parent.Scope
	}
	return &ScopeContext{
		Parent:      parent,
		OuterLevel:  outerLevel,
		TableLookup: make(map[string]int),
		Scope:       NewTableScopeWithParent(parentScope),
	}
}

// Binder performs semantic binding of an AST against database catalog schemas,
// resolving all identifiers to physical table offsets, column indices, and data types,
// and producing a typed BoundSelectStmt with support for hierarchical correlated subquery scopes.
type Binder struct {
	schemas map[string]*storage.TableSchema
	current *ScopeContext
}

// NewBinder creates a dedicated Binder instance with the given catalog schemas.
func NewBinder(schemas map[string]*storage.TableSchema) *Binder {
	return &Binder{
		schemas: schemas,
		current: newScopeContext(nil),
	}
}

// BindSelect binds a GeneralSelectStmt into a fully resolved BoundSelectStmt.
func (b *Binder) BindSelect(stmt *proc.GeneralSelectStmt) (*BoundSelectStmt, error) {
	if stmt == nil {
		return nil, nil
	}
	return b.bindSelectInternal(stmt)
}

func (b *Binder) bindSelectInternal(stmt *proc.GeneralSelectStmt) (*BoundSelectStmt, error) {
	if stmt == nil {
		return nil, nil
	}

	// 1. Semantic verification gatekeeper pass
	var parentScope *TableScope
	if b.current != nil && b.current.Parent != nil {
		parentScope = b.current.Parent.Scope
	}
	if err := ValidateQueryASTWithParent(stmt, b.schemas, parentScope); err != nil {
		return nil, err
	}

	cur := b.current

	// 2. Register base relation (FROM)
	if stmt.From.Name != "" {
		cleanTbl := strings.ToLower(sqlutil.CleanIdentifier(stmt.From.Name))
		bareTbl := strings.ToLower(proc.CleanSchemaPrefix(stmt.From.Name))
		cleanAlias := strings.ToLower(sqlutil.CleanIdentifier(stmt.From.Alias))

		sch := b.findSchema(stmt.From.Name)
		tableID := len(cur.Tables)
		slotOffset := 0

		tRef := BoundTableRef{
			TableID:    tableID,
			Name:       stmt.From.Name,
			Alias:      stmt.From.Alias,
			Schema:     sch,
			SlotOffset: slotOffset,
		}
		cur.Tables = append(cur.Tables, tRef)
		cur.SlotOffsets = append(cur.SlotOffsets, slotOffset)

		if cleanTbl != "" {
			cur.TableLookup[cleanTbl] = tableID
		}
		if bareTbl != "" {
			cur.TableLookup[bareTbl] = tableID
		}
		if cleanAlias != "" {
			cur.TableLookup[cleanAlias] = tableID
		}
		cur.Scope.Register(stmt.From.Name, stmt.From.Alias, sch)
	}

	// 3. Register joined relations (JOINs)
	for _, j := range stmt.Joins {
		if j.Table.Name != "" {
			cleanTbl := strings.ToLower(sqlutil.CleanIdentifier(j.Table.Name))
			bareTbl := strings.ToLower(proc.CleanSchemaPrefix(j.Table.Name))
			cleanAlias := strings.ToLower(sqlutil.CleanIdentifier(j.Table.Alias))

			sch := b.findSchema(j.Table.Name)
			tableID := len(cur.Tables)

			// Calculate starting slot offset in joined Tuple.Values
			prevOffset := 0
			if len(cur.SlotOffsets) > 0 {
				lastIdx := len(cur.Tables) - 1
				prevOffset = cur.SlotOffsets[lastIdx]
				if cur.Tables[lastIdx].Schema != nil {
					prevOffset += len(cur.Tables[lastIdx].Schema.Columns)
				}
			}
			slotOffset := prevOffset

			tRef := BoundTableRef{
				TableID:    tableID,
				Name:       j.Table.Name,
				Alias:      j.Table.Alias,
				Schema:     sch,
				SlotOffset: slotOffset,
			}
			cur.Tables = append(cur.Tables, tRef)
			cur.SlotOffsets = append(cur.SlotOffsets, slotOffset)

			if cleanTbl != "" {
				cur.TableLookup[cleanTbl] = tableID
			}
			if bareTbl != "" {
				cur.TableLookup[bareTbl] = tableID
			}
			if cleanAlias != "" {
				cur.TableLookup[cleanAlias] = tableID
			}
			cur.Scope.Register(j.Table.Name, j.Table.Alias, sch)
		}
	}

	// 4. Register projection aliases for ORDER BY resolution
	for _, p := range stmt.Projections {
		if p.Alias != "" {
			cur.Scope.AddProjectionAlias(p.Alias)
		}
	}

	// 5. Bind Projections
	var boundProjections []BoundProjection
	for _, p := range stmt.Projections {
		bExpr, err := b.bindExpr(p.Expr)
		if err != nil {
			return nil, err
		}
		alias := p.Alias
		if alias == "" {
			alias = p.String()
		}
		tupleSlot := -1
		if colRef, ok := bExpr.(*BoundColumnRef); ok {
			tupleSlot = colRef.TupleSlot
		}
		boundProjections = append(boundProjections, BoundProjection{
			Expr:      bExpr,
			Alias:     alias,
			Type:      bExpr.BoundType(),
			TupleSlot: tupleSlot,
		})
	}

	// 6. Bind JOIN ON conditions
	var boundJoins []BoundJoin
	for i, j := range stmt.Joins {
		var boundOn BoundExpr
		if j.On != nil {
			var err error
			boundOn, err = b.bindExpr(j.On)
			if err != nil {
				return nil, err
			}
		}
		var tRef BoundTableRef
		if i+1 < len(cur.Tables) {
			tRef = cur.Tables[i+1]
		}
		boundJoins = append(boundJoins, BoundJoin{
			Table:    tRef,
			JoinType: j.Type,
			On:       boundOn,
		})
	}

	// 7. Bind WHERE clause
	var boundWhere BoundExpr
	if stmt.Where != nil {
		var err error
		boundWhere, err = b.bindExpr(stmt.Where)
		if err != nil {
			return nil, err
		}
	}

	// 8. Bind GROUP BY clauses
	var boundGroupBy []BoundExpr
	for _, g := range stmt.GroupBy {
		bg, err := b.bindExpr(g)
		if err != nil {
			return nil, err
		}
		boundGroupBy = append(boundGroupBy, bg)
	}

	// 9. Bind HAVING clause
	var boundHaving BoundExpr
	if stmt.Having != nil {
		var err error
		boundHaving, err = b.bindExpr(stmt.Having)
		if err != nil {
			return nil, err
		}
	}

	// 10. Bind ORDER BY clauses
	var boundOrderBy []BoundOrderBy
	for _, ob := range stmt.OrderBy {
		bob, err := b.bindOrderBy(ob.Expr)
		if err != nil {
			return nil, err
		}
		boundOrderBy = append(boundOrderBy, BoundOrderBy{
			Expr:       bob,
			Desc:       ob.Desc,
			NullsFirst: ob.NullsFirst,
		})
	}

	return &BoundSelectStmt{
		RawStmt:     stmt,
		Tables:      cur.Tables,
		Projections: boundProjections,
		Where:       boundWhere,
		Joins:       boundJoins,
		GroupBy:     boundGroupBy,
		Having:      boundHaving,
		OrderBy:     boundOrderBy,
		Limit:       stmt.Limit,
		Offset:      stmt.Offset,
		Distinct:    stmt.Distinct,
	}, nil
}

func (b *Binder) findSchema(name string) *storage.TableSchema {
	if b.schemas == nil {
		return nil
	}
	clean := strings.ToLower(sqlutil.CleanIdentifier(name))
	if sch, ok := b.schemas[clean]; ok {
		return sch
	}
	bare := strings.ToLower(proc.CleanSchemaPrefix(name))
	if sch, ok := b.schemas[bare]; ok {
		return sch
	}
	return nil
}

func (b *Binder) hasAnySchema() bool {
	for s := b.current; s != nil; s = s.Parent {
		if len(s.Tables) > 0 && s.Tables[0].Schema != nil {
			return true
		}
	}
	return false
}

// ResolveColumn resolves a column reference identifier against registered tables in the scope hierarchy.
func (b *Binder) ResolveColumn(raw string) (*BoundColumnRef, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "*" {
		return &BoundColumnRef{Name: raw, ColumnIdx: -1, TupleSlot: -1, DataType: storage.TypeUnknown}, nil
	}

	parts := strings.Split(trimmed, ".")
	if len(parts) > 1 {
		// Qualified reference: e.g. "e.salary" or "dbo.employees.salary"
		last := len(parts) - 1
		colPart := sqlutil.CleanIdentifier(parts[last])
		tblPart := strings.ToLower(sqlutil.CleanIdentifier(parts[last-1]))

		for s := b.current; s != nil; s = s.Parent {
			tableID, ok := s.TableLookup[tblPart]
			if ok {
				tRef := s.Tables[tableID]
				outerLevel := b.current.OuterLevel - s.OuterLevel
				if tRef.Schema != nil {
					col, colIdx, found := tRef.Schema.FindColumn(colPart)
					if !found {
						return nil, fmt.Errorf("column %q does not exist in table or alias %q", parts[last], parts[last-1])
					}
					tupleSlot := s.SlotOffsets[tableID] + colIdx
					return &BoundColumnRef{
						Name:       raw,
						TableName:  tRef.Name,
						TableID:    tableID,
						ColumnIdx:  colIdx,
						TupleSlot:  tupleSlot,
						DataType:   col.Type,
						OuterLevel: outerLevel,
					}, nil
				}
				// Schema unknown at bind-time
				return &BoundColumnRef{
					Name:       raw,
					TableName:  tblPart,
					TableID:    tableID,
					ColumnIdx:  -1,
					TupleSlot:  -1,
					DataType:   storage.TypeUnknown,
					OuterLevel: outerLevel,
				}, nil
			}
		}
		return nil, fmt.Errorf("table or alias %q does not exist in query scope", parts[last-1])
	}

	// Unqualified reference: e.g. "salary"
	colName := sqlutil.CleanIdentifier(trimmed)
	type matchInfo struct {
		outerLevel int
		tableID    int
		colIdx     int
		colType    storage.DataType
		tblName    string
		slotOffset int
	}

	for s := b.current; s != nil; s = s.Parent {
		var matches []matchInfo
		seenSchemas := make(map[*storage.TableSchema]bool)
		outerLevel := b.current.OuterLevel - s.OuterLevel

		for _, tRef := range s.Tables {
			if tRef.Schema != nil {
				if seenSchemas[tRef.Schema] {
					continue
				}
				seenSchemas[tRef.Schema] = true
				if col, colIdx, found := tRef.Schema.FindColumn(colName); found {
					matches = append(matches, matchInfo{
						outerLevel: outerLevel,
						tableID:    tRef.TableID,
						colIdx:     colIdx,
						colType:    col.Type,
						tblName:    tRef.Name,
						slotOffset: s.SlotOffsets[tRef.TableID],
					})
				}
			}
		}

		if len(matches) > 1 {
			return nil, fmt.Errorf("column reference %q is ambiguous", raw)
		}
		if len(matches) == 1 {
			m := matches[0]
			tupleSlot := m.slotOffset + m.colIdx
			return &BoundColumnRef{
				Name:       raw,
				TableName:  m.tblName,
				TableID:    m.tableID,
				ColumnIdx:  m.colIdx,
				TupleSlot:  tupleSlot,
				DataType:   m.colType,
				OuterLevel: m.outerLevel,
			}, nil
		}
	}

	// If schemas are populated and column is not found
	if b.hasAnySchema() {
		return nil, fmt.Errorf("column %q does not exist", raw)
	}

	return &BoundColumnRef{Name: raw, ColumnIdx: -1, TupleSlot: -1, DataType: storage.TypeUnknown}, nil
}

func (b *Binder) bindOrderBy(expr proc.Expr) (BoundExpr, error) {
	if expr == nil {
		return nil, nil
	}
	// Check projection alias in ORDER BY
	if col, ok := expr.(*proc.ColumnRefExpr); ok {
		clean := strings.ToLower(sqlutil.CleanIdentifier(col.Name))
		if b.current.Scope.projectionAliases[clean] {
			return &BoundColumnRef{
				Name:       col.Name,
				ColumnIdx:  -1,
				TupleSlot:  -1,
				DataType:   storage.TypeUnknown,
				OuterLevel: 0,
			}, nil
		}
	}
	return b.bindExpr(expr)
}

func (b *Binder) bindExpr(expr proc.Expr) (BoundExpr, error) {
	if expr == nil {
		return nil, nil
	}

	switch e := expr.(type) {
	case *proc.ColumnRefExpr:
		return b.ResolveColumn(e.Name)

	case *proc.LiteralExpr:
		return &BoundLiteral{Val: e.Val}, nil

	case *proc.BinaryExpr:
		left, err := b.bindExpr(e.Left)
		if err != nil {
			return nil, err
		}
		right, err := b.bindExpr(e.Right)
		if err != nil {
			return nil, err
		}
		resType := inferBinaryOpType(e.Op, left.BoundType(), right.BoundType())
		return &BoundBinaryExpr{
			Left:       left,
			Op:         e.Op,
			Right:      right,
			ResultType: resType,
		}, nil

	case *proc.UnaryExpr:
		inner, err := b.bindExpr(e.Expr)
		if err != nil {
			return nil, err
		}
		resType := inner.BoundType()
		if e.Op == proc.TokenNot {
			resType = storage.TypeBool
		}
		return &BoundUnaryExpr{
			Op:         e.Op,
			Expr:       inner,
			ResultType: resType,
		}, nil

	case *proc.FunctionCallExpr:
		var boundArgs []BoundExpr
		for _, arg := range e.Args {
			ba, err := b.bindExpr(arg)
			if err != nil {
				return nil, err
			}
			boundArgs = append(boundArgs, ba)
		}
		retType := inferFunctionReturnType(e.Name, boundArgs)
		return &BoundFunctionCall{
			Name:        e.Name,
			Args:        boundArgs,
			ReturnType:  retType,
			IsAggregate: proc.IsAggregateFunctionName(e.Name),
			HasOver:     e.HasOver,
		}, nil

	case *proc.ScalarSubqueryExpr:
		if e.Query != nil {
			childScope := newScopeContext(b.current)
			b.current = childScope
			boundSubquery, err := b.bindSelectInternal(e.Query)
			b.current = childScope.Parent
			if err != nil {
				return nil, err
			}
			retType := storage.TypeUnknown
			if len(boundSubquery.Projections) > 0 {
				retType = boundSubquery.Projections[0].Type
			}
			return &BoundSubqueryExpr{
				Subquery:   boundSubquery,
				ResultType: retType,
			}, nil
		}
		return &BoundGenericExpr{Inner: expr, ResultType: storage.TypeUnknown}, nil

	case *proc.ExistsExpr:
		if e.Query != nil {
			childScope := newScopeContext(b.current)
			b.current = childScope
			boundSubquery, err := b.bindSelectInternal(e.Query)
			b.current = childScope.Parent
			if err != nil {
				return nil, err
			}
			return &BoundExistsExpr{
				Subquery: boundSubquery,
				Not:      e.Not,
			}, nil
		}
		return &BoundGenericExpr{Inner: expr, ResultType: storage.TypeBool}, nil

	case *proc.InExpr:
		left, err := b.bindExpr(e.Expr)
		if err != nil {
			return nil, err
		}
		if e.Query != nil {
			childScope := newScopeContext(b.current)
			b.current = childScope
			boundSubquery, err := b.bindSelectInternal(e.Query)
			b.current = childScope.Parent
			if err != nil {
				return nil, err
			}
			return &BoundInSubqueryExpr{
				Expr:       left,
				Subquery:   boundSubquery,
				Not:        e.Not,
				ResultType: storage.TypeBool,
			}, nil
		}
		var boundList []BoundExpr
		for _, itm := range e.List {
			bi, err := b.bindExpr(itm)
			if err != nil {
				return nil, err
			}
			boundList = append(boundList, bi)
		}
		return &BoundInListExpr{
			Expr:       left,
			List:       boundList,
			Not:        e.Not,
			ResultType: storage.TypeBool,
		}, nil

	case *proc.QuantifiedComparisonExpr:
		left, err := b.bindExpr(e.Expr)
		if err != nil {
			return nil, err
		}
		if e.Query != nil {
			childScope := newScopeContext(b.current)
			b.current = childScope
			boundSubquery, err := b.bindSelectInternal(e.Query)
			b.current = childScope.Parent
			if err != nil {
				return nil, err
			}
			return &BoundQuantifiedComparisonExpr{
				Expr:       left,
				Op:         e.Op,
				Quantifier: e.Quantifier,
				Subquery:   boundSubquery,
				ResultType: storage.TypeBool,
			}, nil
		}
		return &BoundGenericExpr{Inner: expr, ResultType: storage.TypeBool}, nil

	default:
		// Bridge for extension expressions (e.g. CaseExpr)
		return &BoundGenericExpr{
			Inner:      expr,
			ResultType: storage.TypeUnknown,
		}, nil
	}
}

func inferBinaryOpType(op proc.TokenType, left, right storage.DataType) storage.DataType {
	switch op {
	case proc.TokenEq, proc.TokenNeq, proc.TokenLt, proc.TokenLte, proc.TokenGt, proc.TokenGte,
		proc.TokenLike, proc.TokenIn, proc.TokenAnd, proc.TokenOr, proc.TokenNot:
		return storage.TypeBool

	case proc.TokenPlus, proc.TokenMinus, proc.TokenAsterisk, proc.TokenSlash, proc.TokenPercent:
		if left == storage.TypeVarChar || right == storage.TypeVarChar {
			if op == proc.TokenPlus {
				return storage.TypeVarChar
			}
		}
		if left == storage.TypeFloat || right == storage.TypeFloat ||
			left == storage.TypeReal || right == storage.TypeReal {
			return storage.TypeFloat
		}
		if left == storage.TypeDecimal || right == storage.TypeDecimal {
			return storage.TypeDecimal
		}
		return storage.TypeInt

	default:
		return storage.TypeUnknown
	}
}

func inferFunctionReturnType(name string, args []BoundExpr) storage.DataType {
	uName := strings.ToUpper(strings.TrimSpace(name))
	switch uName {
	case "COUNT":
		return storage.TypeInt
	case "AVG":
		return storage.TypeFloat
	case "SUM", "MIN", "MAX":
		if len(args) > 0 && args[0] != nil {
			return args[0].BoundType()
		}
		return storage.TypeDecimal
	case "CONCAT", "SUBSTRING", "LOWER", "UPPER", "TRIM", "LEFT", "RIGHT", "REPLACE":
		return storage.TypeVarChar
	case "ABS", "ROUND", "FLOOR", "CEILING":
		if len(args) > 0 && args[0] != nil {
			return args[0].BoundType()
		}
		return storage.TypeFloat
	case "NOW", "GETDATE", "CURRENT_TIMESTAMP":
		return storage.TypeDateTime
	default:
		return storage.TypeUnknown
	}
}
