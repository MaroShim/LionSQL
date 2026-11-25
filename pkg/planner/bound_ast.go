package planner

import (
	"fmt"
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

// BoundExpr represents an AST expression whose identifiers, types, and tuple offsets
// have been statically resolved against the catalog schemas.
type BoundExpr interface {
	proc.Expr
	BoundType() storage.DataType
}

// BoundColumnRef represents a column reference bound to a specific table and physical slot offset.
type BoundColumnRef struct {
	proc.BaseExpr
	Name       string           // Original column identifier (e.g. "age", "e.salary")
	TableName  string           // Resolved table name or alias
	TableID    int              // Index in BoundSelectStmt.Tables (0 = base FROM, 1..N = JOINs)
	ColumnIdx  int              // 0-based column index in TableSchema.Columns (0 <= ColumnIdx < 1024)
	TupleSlot  int              // Flattened index in Volcano joined Tuple.Values (Table SlotOffset + ColumnIdx)
	DataType   storage.DataType // Statically resolved storage data type
	OuterLevel int              // 0 = current query scope, 1 = parent scope (correlated subquery)
}

func (b *BoundColumnRef) String() string             { return b.Name }
func (b *BoundColumnRef) BoundType() storage.DataType { return b.DataType }

// BoundLiteral represents a statically evaluated or parsed literal constant.
type BoundLiteral struct {
	proc.BaseExpr
	Val storage.Value
}

func (b *BoundLiteral) String() string             { return b.Val.String() }
func (b *BoundLiteral) BoundType() storage.DataType { return b.Val.Type }

// BoundBinaryExpr represents a binary operation with statically resolved operand types and result type.
type BoundBinaryExpr struct {
	proc.BaseExpr
	Left       BoundExpr
	Op         proc.TokenType
	Right      BoundExpr
	ResultType storage.DataType
}

func (b *BoundBinaryExpr) String() string {
	leftStr := ""
	if b.Left != nil {
		leftStr = b.Left.String()
	}
	rightStr := ""
	if b.Right != nil {
		rightStr = b.Right.String()
	}
	return fmt.Sprintf("(%s %s %s)", leftStr, b.Op, rightStr)
}
func (b *BoundBinaryExpr) BoundType() storage.DataType { return b.ResultType }

// BoundUnaryExpr represents a unary operation with a statically resolved result type.
type BoundUnaryExpr struct {
	proc.BaseExpr
	Op         proc.TokenType
	Expr       BoundExpr
	ResultType storage.DataType
}

func (b *BoundUnaryExpr) String() string {
	if b.Expr != nil {
		return fmt.Sprintf("%s(%s)", b.Op, b.Expr.String())
	}
	return b.Op.String()
}
func (b *BoundUnaryExpr) BoundType() storage.DataType { return b.ResultType }

// BoundFunctionCall represents a built-in or user-defined scalar or aggregate function call.
type BoundFunctionCall struct {
	proc.BaseExpr
	Name        string
	Args        []BoundExpr
	ReturnType  storage.DataType
	IsAggregate bool
	HasOver     bool
}

func (b *BoundFunctionCall) String() string {
	var args []string
	for _, a := range b.Args {
		if a != nil {
			args = append(args, a.String())
		}
	}
	return fmt.Sprintf("%s(%s)", b.Name, strings.Join(args, ", "))
}
func (b *BoundFunctionCall) BoundType() storage.DataType { return b.ReturnType }

// BoundSubqueryExpr represents a scalar subquery returning a single row and column.
type BoundSubqueryExpr struct {
	proc.BaseExpr
	Subquery   *BoundSelectStmt
	ResultType storage.DataType
}

func (b *BoundSubqueryExpr) String() string {
	if b.Subquery != nil && b.Subquery.RawStmt != nil {
		return fmt.Sprintf("(%s)", b.Subquery.RawStmt.String())
	}
	return "(SELECT ...)"
}
func (b *BoundSubqueryExpr) BoundType() storage.DataType { return b.ResultType }

// BoundExistsExpr represents an EXISTS (SELECT ...) or NOT EXISTS (SELECT ...) predicate.
type BoundExistsExpr struct {
	proc.BaseExpr
	Subquery *BoundSelectStmt
	Not      bool
}

func (b *BoundExistsExpr) String() string {
	prefix := "EXISTS"
	if b.Not {
		prefix = "NOT EXISTS"
	}
	if b.Subquery != nil && b.Subquery.RawStmt != nil {
		return fmt.Sprintf("%s (%s)", prefix, b.Subquery.RawStmt.String())
	}
	return fmt.Sprintf("%s (SELECT ...)", prefix)
}
func (b *BoundExistsExpr) BoundType() storage.DataType { return storage.TypeBool }

// BoundInSubqueryExpr represents an expr IN (SELECT ...) or expr NOT IN (SELECT ...) predicate.
type BoundInSubqueryExpr struct {
	proc.BaseExpr
	Expr       BoundExpr
	Subquery   *BoundSelectStmt
	Not        bool
	ResultType storage.DataType
}

func (b *BoundInSubqueryExpr) String() string {
	op := "IN"
	if b.Not {
		op = "NOT IN"
	}
	leftStr := ""
	if b.Expr != nil {
		leftStr = b.Expr.String()
	}
	if b.Subquery != nil && b.Subquery.RawStmt != nil {
		return fmt.Sprintf("%s %s (%s)", leftStr, op, b.Subquery.RawStmt.String())
	}
	return fmt.Sprintf("%s %s (SELECT ...)", leftStr, op)
}
func (b *BoundInSubqueryExpr) BoundType() storage.DataType { return storage.TypeBool }

// BoundInListExpr represents an expr IN (val1, val2, ...) predicate.
type BoundInListExpr struct {
	proc.BaseExpr
	Expr       BoundExpr
	List       []BoundExpr
	Not        bool
	ResultType storage.DataType
}

func (b *BoundInListExpr) String() string {
	op := "IN"
	if b.Not {
		op = "NOT IN"
	}
	leftStr := ""
	if b.Expr != nil {
		leftStr = b.Expr.String()
	}
	var items []string
	for _, itm := range b.List {
		if itm != nil {
			items = append(items, itm.String())
		}
	}
	return fmt.Sprintf("%s %s (%s)", leftStr, op, strings.Join(items, ", "))
}
func (b *BoundInListExpr) BoundType() storage.DataType { return storage.TypeBool }

// BoundQuantifiedComparisonExpr represents an expr Op (ALL | ANY | SOME) (SELECT ...) predicate.
type BoundQuantifiedComparisonExpr struct {
	proc.BaseExpr
	Expr       BoundExpr
	Op         proc.TokenType
	Quantifier proc.TokenType
	Subquery   *BoundSelectStmt
	ResultType storage.DataType
}

func (b *BoundQuantifiedComparisonExpr) String() string {
	qStr := "ANY"
	if b.Quantifier == proc.TokenAll {
		qStr = "ALL"
	} else if b.Quantifier == proc.TokenSome {
		qStr = "SOME"
	}
	leftStr := ""
	if b.Expr != nil {
		leftStr = b.Expr.String()
	}
	if b.Subquery != nil && b.Subquery.RawStmt != nil {
		return fmt.Sprintf("%s %s %s (%s)", leftStr, b.Op, qStr, b.Subquery.RawStmt.String())
	}
	return fmt.Sprintf("%s %s %s (SELECT ...)", leftStr, b.Op, qStr)
}
func (b *BoundQuantifiedComparisonExpr) BoundType() storage.DataType { return storage.TypeBool }

// BoundGenericExpr wraps any proc.Expr whose sub-expressions have been bound,
// serving as a bridge for expressions such as CASE, CAST, BETWEEN, and IN.
type BoundGenericExpr struct {
	proc.BaseExpr
	Inner      proc.Expr
	ResultType storage.DataType
}

func (b *BoundGenericExpr) String() string             { return b.Inner.String() }
func (b *BoundGenericExpr) BoundType() storage.DataType { return b.ResultType }

// BoundTableRef holds metadata for a bound relation in FROM or JOIN clauses.
type BoundTableRef struct {
	TableID    int                  // 0 = primary FROM table, 1..N = joined tables
	Name       string               // Base table name
	Alias      string               // Optional alias
	Schema     *storage.TableSchema // Resolved catalog table schema
	SlotOffset int                  // Starting index in Volcano joined Tuple.Values
}

// BoundProjection represents a single projected output column or expression.
type BoundProjection struct {
	Expr      BoundExpr        // Resolved bound expression
	Alias     string           // Output column name or alias
	Type      storage.DataType // Output data type
	TupleSlot int              // Direct slot index if projecting an unmutated column (-1 if computed expr)
}

// BoundJoin represents a bound JOIN clause.
type BoundJoin struct {
	Table    BoundTableRef
	JoinType string
	On       BoundExpr
}

// BoundOrderBy represents a bound ORDER BY element.
type BoundOrderBy struct {
	Expr       BoundExpr
	Desc       bool
	NullsFirst *bool
}

// BoundSelectStmt represents a fully bound, typed, and validated SELECT statement.
// All column references have been resolved to physical TableID and TupleSlot offsets,
// eliminating runtime string hashing and name lookups in execution engines.
type BoundSelectStmt struct {
	RawStmt     *proc.GeneralSelectStmt
	Tables      []BoundTableRef
	Projections []BoundProjection
	Where       BoundExpr
	Joins       []BoundJoin
	GroupBy     []BoundExpr
	Having      BoundExpr
	OrderBy     []BoundOrderBy
	Limit       int64
	Offset      int64
	Distinct    bool
}
