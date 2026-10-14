package proc

import (
	"fmt"
	"strings"

	"sqlion/pkg/storage"
)

// Node is the base interface for AST nodes.
type Node interface {
	String() string
}

// Expr represents an evaluable expression.
type Expr interface {
	Node
	exprNode()
}

// Statement represents an executable statement.
type Statement interface {
	Node
	stmtNode()
}

// LiteralExpr represents a constant literal.
type LiteralExpr struct {
	Val storage.Value
}

func (e *LiteralExpr) exprNode()      {}
func (e *LiteralExpr) String() string {
	if e.Val.Type == storage.TypeVarChar && !e.Val.IsNull {
		return fmt.Sprintf("'%s'", strings.ReplaceAll(e.Val.SVal, "'", "''"))
	}
	return e.Val.String()
}

// VariableExpr represents a variable reference (@var).
type VariableExpr struct {
	Name string
}

func (e *VariableExpr) exprNode()      {}
func (e *VariableExpr) String() string { return e.Name }

// ColumnRefExpr represents a column name reference.
type ColumnRefExpr struct {
	Name string
}

func (e *ColumnRefExpr) exprNode()      {}
func (e *ColumnRefExpr) String() string { return e.Name }

// BinaryExpr represents binary operation (A + B, A = B, etc.).
type BinaryExpr struct {
	Left  Expr
	Op    TokenType
	Right Expr
}

func (e *BinaryExpr) exprNode() {}
func (e *BinaryExpr) String() string {
	leftStr := ""
	if e.Left != nil {
		leftStr = e.Left.String()
	}
	rightStr := ""
	if e.Right != nil {
		rightStr = e.Right.String()
	}
	return fmt.Sprintf("%s %s %s", leftStr, e.Op.String(), rightStr)
}

// UnaryExpr represents unary operations (NOT, -).
type UnaryExpr struct {
	Op   TokenType
	Expr Expr
}

func (e *UnaryExpr) exprNode() {}
func (e *UnaryExpr) String() string {
	if e.Expr != nil {
		return fmt.Sprintf("%s %s", e.Op.String(), e.Expr.String())
	}
	return e.Op.String()
}

// CastExpr represents CAST(expr AS targetType) or CONVERT(targetType, expr [, style]) or expr::targetType.
type CastExpr struct {
	Expr       Expr
	TargetType storage.DataType
	Precision  int
	Scale      int
	Style      int
	Try        bool
}

func (e *CastExpr) exprNode() {}
func (e *CastExpr) String() string {
	fn := "CAST"
	if e.Try {
		fn = "TRY_CAST"
	}
	exprStr := ""
	if e.Expr != nil {
		exprStr = e.Expr.String()
	}
	return fmt.Sprintf("%s(%s AS %v)", fn, exprStr, e.TargetType)
}

// CreateProcStmt represents CREATE PROCEDURE ... AS BEGIN ... END
type CreateProcStmt struct {
	Name    string
	Params  []ParamDef
	Body    Statement
	OrAlter bool
	IsAlter bool
}

// CreateFunctionStmt represents CREATE FUNCTION dbo.Name (@param type) RETURNS type AS BEGIN ... END
type CreateFunctionStmt struct {
	Name       string
	Params     []ParamDef
	ReturnType storage.DataType
	Body       Statement
}

func (s *CreateFunctionStmt) stmtNode() {}
func (s *CreateFunctionStmt) String() string {
	return fmt.Sprintf("CREATE FUNCTION %s RETURNS %v AS\n%s", s.Name, s.ReturnType, s.Body.String())
}

// CreateTriggerStmt represents CREATE TRIGGER name ON table AFTER/INSTEAD OF INSERT, UPDATE, DELETE AS BEGIN ... END
type CreateTriggerStmt struct {
	Name   string
	Table  string
	Timing string   // "AFTER", "INSTEAD OF"
	Events []string // "INSERT", "UPDATE", "DELETE"
	Body   Statement
}

func (s *CreateTriggerStmt) stmtNode() {}
func (s *CreateTriggerStmt) String() string {
	return fmt.Sprintf("CREATE TRIGGER %s ON %s %s %s AS\n%s", s.Name, s.Table, s.Timing, strings.Join(s.Events, ", "), s.Body.String())
}

// ParamDef defines a procedure parameter.
type ParamDef struct {
	Name string
	Type storage.DataType
}

func (s *CreateProcStmt) stmtNode() {}
func (s *CreateProcStmt) String() string {
	return fmt.Sprintf("CREATE PROCEDURE %s AS\n%s", s.Name, s.Body.String())
}

// BlockStmt represents BEGIN ... END
type BlockStmt struct {
	Statements []Statement
}

func (s *BlockStmt) stmtNode() {}
func (s *BlockStmt) String() string {
	var b strings.Builder
	b.WriteString("BEGIN\n")
	for _, stmt := range s.Statements {
		b.WriteString("  ")
		b.WriteString(stmt.String())
		b.WriteString(";\n")
	}
	b.WriteString("END")
	return b.String()
}

// DeclareStmt represents DECLARE @var TYPE
type DeclareStmt struct {
	Name string
	Type storage.DataType
}

func (s *DeclareStmt) stmtNode() {}
func (s *DeclareStmt) String() string {
	return fmt.Sprintf("DECLARE %s %s", s.Name, s.Type)
}

// SetStmt represents SET @var = Expr
type SetStmt struct {
	Name  string
	Value Expr
}

func (s *SetStmt) stmtNode() {}
func (s *SetStmt) String() string {
	return fmt.Sprintf("SET %s = %s", s.Name, s.Value.String())
}

// SetOptionStmt represents SET <option> [ON | OFF | value] (e.g. SET NOCOUNT ON)
type SetOptionStmt struct {
	Options map[string]string
}

func (s *SetOptionStmt) stmtNode() {}
func (s *SetOptionStmt) String() string {
	var parts []string
	for k, v := range s.Options {
		parts = append(parts, fmt.Sprintf("%s %s", k, v))
	}
	return "SET " + strings.Join(parts, ", ")
}

// IfStmt represents IF Cond Statement [ELSE Statement]
type IfStmt struct {
	Condition Expr
	Then      Statement
	Else      Statement
}

func (s *IfStmt) stmtNode() {}
func (s *IfStmt) String() string {
	if s.Else != nil {
		return fmt.Sprintf("IF %s\n%s\nELSE\n%s", s.Condition.String(), s.Then.String(), s.Else.String())
	}
	return fmt.Sprintf("IF %s\n%s", s.Condition.String(), s.Then.String())
}

// WhileStmt represents WHILE Cond Statement
type WhileStmt struct {
	Condition Expr
	Body      Statement
}

func (s *WhileStmt) stmtNode() {}
func (s *WhileStmt) String() string {
	return fmt.Sprintf("WHILE %s\n%s", s.Condition.String(), s.Body.String())
}

// ReturnStmt represents RETURN Expr
type ReturnStmt struct {
	Value Expr
}

func (s *ReturnStmt) stmtNode() {}
func (s *ReturnStmt) String() string {
	if s.Value != nil {
		return fmt.Sprintf("RETURN %s", s.Value.String())
	}
	return "RETURN"
}

// PrintStmt represents PRINT Expr
type PrintStmt struct {
	Expr Expr
}

func (s *PrintStmt) stmtNode() {}
func (s *PrintStmt) String() string {
	return fmt.Sprintf("PRINT %s", s.Expr.String())
}

// RaiseErrorStmt represents RAISERROR('message')
type RaiseErrorStmt struct {
	Message string
}

func (s *RaiseErrorStmt) stmtNode() {}
func (s *RaiseErrorStmt) String() string {
	return fmt.Sprintf("RAISERROR('%s')", s.Message)
}

// TxAction represents transaction control.
type TxAction int

const (
	TxBegin TxAction = iota
	TxCommit
	TxRollback
	TxSavepoint
	TxRollbackTo
)

// TxStmt represents BEGIN TRAN, COMMIT TRAN, ROLLBACK TRAN, SAVE TRAN, ROLLBACK TRAN <sp>
type TxStmt struct {
	Action TxAction
	Name   string
}

func (s *TxStmt) stmtNode() {}
func (s *TxStmt) String() string {
	switch s.Action {
	case TxBegin:
		return "BEGIN TRANSACTION"
	case TxCommit:
		return "COMMIT TRANSACTION"
	case TxRollback:
		return "ROLLBACK TRANSACTION"
	case TxSavepoint:
		return "SAVE TRANSACTION " + s.Name
	case TxRollbackTo:
		return "ROLLBACK TRANSACTION " + s.Name
	default:
		return "TX"
	}
}

// SelectIntoStmt represents SELECT col INTO @var FROM table WHERE pk_col = expr
type SelectIntoStmt struct {
	Column   string
	IntoVar  string
	Table    string
	WhereCol string
	WhereVal Expr
}

func (s *SelectIntoStmt) stmtNode() {}
func (s *SelectIntoStmt) String() string {
	return fmt.Sprintf("SELECT %s INTO %s FROM %s WHERE %s = %s",
		s.Column, s.IntoVar, s.Table, s.WhereCol, s.WhereVal.String())
}

// InsertStmt represents INSERT INTO table (cols...) [OUTPUT ...] VALUES (exprs...)
type InsertStmt struct {
	Table      string
	Columns    []string
	Values     []Expr
	OutputCols []string
}

func (s *InsertStmt) stmtNode() {}
func (s *InsertStmt) String() string {
	valStrs := make([]string, len(s.Values))
	for i, v := range s.Values {
		valStrs[i] = v.String()
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		s.Table, strings.Join(s.Columns, ", "), strings.Join(valStrs, ", "))
}

// UpdateAssignment maps column name to assigned expression.
type UpdateAssignment struct {
	Column string
	Value  Expr
}

// UpdateStmt represents UPDATE table SET col1 = expr1, ... [OUTPUT ...] WHERE pk_col = expr
type UpdateStmt struct {
	Table       string
	Assignments []UpdateAssignment
	WhereCol    string
	WhereVal    Expr
	OutputCols  []string
}

func (s *UpdateStmt) stmtNode() {}
func (s *UpdateStmt) String() string {
	assigns := make([]string, len(s.Assignments))
	for i, a := range s.Assignments {
		assigns[i] = fmt.Sprintf("%s = %s", a.Column, a.Value.String())
	}
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s = %s",
		s.Table, strings.Join(assigns, ", "), s.WhereCol, s.WhereVal.String())
}

// DeleteStmt represents DELETE FROM table [OUTPUT ...] WHERE pk_col = expr
type DeleteStmt struct {
	Table      string
	WhereCol   string
	WhereVal   Expr
	OutputCols []string
}

func (s *DeleteStmt) stmtNode() {}
func (s *DeleteStmt) String() string {
	return fmt.Sprintf("DELETE FROM %s WHERE %s = %s", s.Table, s.WhereCol, s.WhereVal.String())
}

// ExecStmt represents EXEC ProcName @arg1, @arg2
type ExecStmt struct {
	ProcName string
	Args     []Expr
}

func (s *ExecStmt) stmtNode() {}
func (s *ExecStmt) String() string {
	argStrs := make([]string, len(s.Args))
	for i, a := range s.Args {
		argStrs[i] = a.String()
	}
	return fmt.Sprintf("EXEC %s %s", s.ProcName, strings.Join(argStrs, ", "))
}

// DynamicExecStmt represents dynamic SQL execution: EXEC ( string_expression )
type DynamicExecStmt struct {
	Expr Expr
}

func (s *DynamicExecStmt) stmtNode() {}
func (s *DynamicExecStmt) String() string {
	return fmt.Sprintf("EXEC (%s)", s.Expr.String())
}

// TryCatchStmt represents BEGIN TRY ... END TRY BEGIN CATCH ... END CATCH
type TryCatchStmt struct {
	TryBody   Statement
	CatchBody Statement
}

func (s *TryCatchStmt) stmtNode() {}
func (s *TryCatchStmt) String() string {
	return fmt.Sprintf("BEGIN TRY\n%s\nEND TRY\nBEGIN CATCH\n%s\nEND CATCH", s.TryBody.String(), s.CatchBody.String())
}

// ThrowStmt represents THROW 50000, 'msg', 1
type ThrowStmt struct {
	Message string
}

func (s *ThrowStmt) stmtNode() {}
func (s *ThrowStmt) String() string {
	return fmt.Sprintf("THROW 50000, %q, 1", s.Message)
}

// BreakStmt represents BREAK
type BreakStmt struct{}

func (s *BreakStmt) stmtNode() {}
func (s *BreakStmt) String() string { return "BREAK" }

// ContinueStmt represents CONTINUE
type ContinueStmt struct{}

func (s *ContinueStmt) stmtNode() {}
func (s *ContinueStmt) String() string { return "CONTINUE" }

// GotoStmt represents GOTO <label>
type GotoStmt struct {
	Label string
}

func (s *GotoStmt) stmtNode() {}
func (s *GotoStmt) String() string { return fmt.Sprintf("GOTO %s", s.Label) }

// LabelStmt represents a label definition (<label>:)
type LabelStmt struct {
	Label string
}

func (s *LabelStmt) stmtNode() {}
func (s *LabelStmt) String() string { return fmt.Sprintf("%s:", s.Label) }

// CaseWhen represents a single WHEN cond THEN result arm
type CaseWhen struct {
	Condition Expr
	Result    Expr
}

// CaseExpr represents CASE WHEN ... THEN ... [ELSE ...] END
type CaseExpr struct {
	Whens []CaseWhen
	Else  Expr
}

func (e *CaseExpr) exprNode() {}
func (e *CaseExpr) String() string {
	var b strings.Builder
	b.WriteString("CASE")
	for _, w := range e.Whens {
		b.WriteString(fmt.Sprintf(" WHEN %s THEN %s", w.Condition.String(), w.Result.String()))
	}
	if e.Else != nil {
		b.WriteString(fmt.Sprintf(" ELSE %s", e.Else.String()))
	}
	b.WriteString(" END")
	return b.String()
}

// FunctionCallExpr represents scalar functions like ISNULL, COALESCE, GETDATE, LEN, SUBSTRING, etc.
type FunctionCallExpr struct {
	Name string
	Args []Expr
}

func (e *FunctionCallExpr) exprNode() {}
func (e *FunctionCallExpr) String() string {
	argStrs := make([]string, len(e.Args))
	for i, a := range e.Args {
		argStrs[i] = a.String()
	}
	return fmt.Sprintf("%s(%s)", e.Name, strings.Join(argStrs, ", "))
}

// WithinGroupExpr represents an ordered-set aggregate like PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY col [ASC|DESC])
type WithinGroupExpr struct {
	Function *FunctionCallExpr
	OrderBy  Expr
	Desc     bool
}

func (e *WithinGroupExpr) exprNode() {}
func (e *WithinGroupExpr) String() string {
	dir := ""
	if e.Desc {
		dir = " DESC"
	}
	return fmt.Sprintf("%s WITHIN GROUP (ORDER BY %s%s)", e.Function.String(), e.OrderBy.String(), dir)
}

// InExpr represents Expr [NOT] IN (Expr1, Expr2, ...) or Expr [NOT] IN (SELECT ...)
type InExpr struct {
	Expr  Expr
	Not   bool
	List  []Expr
	Query *GeneralSelectStmt
}

func (e *InExpr) exprNode() {}
func (e *InExpr) String() string {
	notStr := ""
	if e.Not {
		notStr = "NOT "
	}
	if e.Query != nil {
		return fmt.Sprintf("%s %sIN (%s)", e.Expr.String(), notStr, e.Query.String())
	}
	items := make([]string, len(e.List))
	for i, item := range e.List {
		items[i] = item.String()
	}
	return fmt.Sprintf("%s %sIN (%s)", e.Expr.String(), notStr, strings.Join(items, ", "))
}

// BetweenExpr represents Expr [NOT] BETWEEN Low AND High
type BetweenExpr struct {
	Expr Expr
	Not  bool
	Low  Expr
	High Expr
}

func (e *BetweenExpr) exprNode() {}
func (e *BetweenExpr) String() string {
	notStr := ""
	if e.Not {
		notStr = "NOT "
	}
	return fmt.Sprintf("%s %sBETWEEN %s AND %s", e.Expr.String(), notStr, e.Low.String(), e.High.String())
}

// LikeExpr represents Expr [NOT] LIKE Pattern
type LikeExpr struct {
	Expr    Expr
	Not     bool
	Pattern Expr
}

func (e *LikeExpr) exprNode() {}
func (e *LikeExpr) String() string {
	notStr := ""
	if e.Not {
		notStr = "NOT "
	}
	return fmt.Sprintf("%s %sLIKE %s", e.Expr.String(), notStr, e.Pattern.String())
}

// IsNullExpr represents Expr IS [NOT] NULL
type IsNullExpr struct {
	Expr Expr
	Not  bool
}

func (e *IsNullExpr) exprNode() {}
func (e *IsNullExpr) String() string {
	if e.Not {
		return fmt.Sprintf("%s IS NOT NULL", e.Expr.String())
	}
	return fmt.Sprintf("%s IS NULL", e.Expr.String())
}

// ScalarSubqueryExpr represents (SELECT col FROM table WHERE cond)
type ScalarSubqueryExpr struct {
	Column string
	Table  string
	Where  Expr
	Query  *GeneralSelectStmt
}

func (e *ScalarSubqueryExpr) exprNode() {}
func (e *ScalarSubqueryExpr) String() string {
	if e.Query != nil {
		return fmt.Sprintf("(%s)", e.Query.String())
	}
	if e.Where != nil {
		return fmt.Sprintf("(SELECT %s FROM %s WHERE %s)", e.Column, e.Table, e.Where.String())
	}
	return fmt.Sprintf("(SELECT %s FROM %s)", e.Column, e.Table)
}

// UpdateWhereStmt represents UPDATE table SET col1 = expr1, ... [OUTPUT ...] WHERE <arbitrary condition>
type UpdateWhereStmt struct {
	Table       string
	Assignments []UpdateAssignment
	OutputCols  []string
	Where       Expr
}

func (s *UpdateWhereStmt) stmtNode() {}
func (s *UpdateWhereStmt) String() string {
	assigns := make([]string, len(s.Assignments))
	for i, a := range s.Assignments {
		assigns[i] = fmt.Sprintf("%s = %s", a.Column, a.Value.String())
	}
	outStr := ""
	if len(s.OutputCols) > 0 {
		outStr = " OUTPUT " + strings.Join(s.OutputCols, ", ")
	}
	if s.Where != nil {
		return fmt.Sprintf("UPDATE %s SET %s%s WHERE %s", s.Table, strings.Join(assigns, ", "), outStr, s.Where.String())
	}
	return fmt.Sprintf("UPDATE %s SET %s%s", s.Table, strings.Join(assigns, ", "), outStr)
}

// DeleteWhereStmt represents DELETE FROM table [OUTPUT ...] WHERE <arbitrary condition>
type DeleteWhereStmt struct {
	Table      string
	OutputCols []string
	Where      Expr
}

func (s *DeleteWhereStmt) stmtNode() {}
func (s *DeleteWhereStmt) String() string {
	outStr := ""
	if len(s.OutputCols) > 0 {
		outStr = " OUTPUT " + strings.Join(s.OutputCols, ", ")
	}
	if s.Where != nil {
		return fmt.Sprintf("DELETE FROM %s%s WHERE %s", s.Table, outStr, s.Where.String())
	}
	return fmt.Sprintf("DELETE FROM %s%s", s.Table, outStr)
}

// DeclareCursorStmt represents DECLARE cur_name CURSOR FOR SELECT cols FROM tbl WHERE cond
type DeclareCursorStmt struct {
	Name    string
	Table   string
	Columns []string
	Where   Expr
}

func (s *DeclareCursorStmt) stmtNode() {}
func (s *DeclareCursorStmt) String() string {
	return fmt.Sprintf("DECLARE %s CURSOR FOR SELECT %s FROM %s", s.Name, strings.Join(s.Columns, ", "), s.Table)
}

// OpenCursorStmt represents OPEN cur_name
type OpenCursorStmt struct {
	Name string
}

func (s *OpenCursorStmt) stmtNode() {}
func (s *OpenCursorStmt) String() string {
	return fmt.Sprintf("OPEN %s", s.Name)
}

// FetchNextStmt represents FETCH NEXT FROM cur_name INTO @var1, @var2...
type FetchNextStmt struct {
	CursorName string
	IntoVars   []string
}

func (s *FetchNextStmt) stmtNode() {}
func (s *FetchNextStmt) String() string {
	return fmt.Sprintf("FETCH NEXT FROM %s INTO %s", s.CursorName, strings.Join(s.IntoVars, ", "))
}

// CloseCursorStmt represents CLOSE cur_name
type CloseCursorStmt struct {
	Name string
}

func (s *CloseCursorStmt) stmtNode() {}
func (s *CloseCursorStmt) String() string {
	return fmt.Sprintf("CLOSE %s", s.Name)
}

// DeallocateCursorStmt represents DEALLOCATE cur_name
type DeallocateCursorStmt struct {
	Name string
}

func (s *DeallocateCursorStmt) stmtNode() {}
func (s *DeallocateCursorStmt) String() string {
	return fmt.Sprintf("DEALLOCATE %s", s.Name)
}

// Projection represents a projected expression in a SELECT query.
type Projection struct {
	Expr  Expr
	Alias string
}

func (p Projection) String() string {
	if p.Alias != "" {
		return fmt.Sprintf("%s AS %s", p.Expr.String(), p.Alias)
	}
	return p.Expr.String()
}

// TableRef represents a referenced table, TVF call, or derived table subquery.
type TableRef struct {
	Name     string
	Alias    string
	IsTVF    bool
	TVFArgs  []Expr
	Subquery *GeneralSelectStmt
	Hints    []string
}

func (t TableRef) String() string {
	var s string
	if t.Subquery != nil {
		s = fmt.Sprintf("(%s)", t.Subquery.String())
	} else if t.IsTVF {
		var args []string
		for _, a := range t.TVFArgs {
			args = append(args, a.String())
		}
		s = fmt.Sprintf("%s(%s)", t.Name, strings.Join(args, ", "))
	} else {
		s = t.Name
	}
	if t.Alias != "" && t.Alias != t.Name {
		s = fmt.Sprintf("%s AS %s", s, t.Alias)
	}
	if len(t.Hints) > 0 {
		s = fmt.Sprintf("%s WITH (%s)", s, strings.Join(t.Hints, ", "))
	}
	return s
}

// JoinClause represents an INNER/LEFT JOIN or CROSS/OUTER APPLY clause.
type JoinClause struct {
	Type  string // "INNER", "LEFT", "CROSS APPLY", "OUTER APPLY", etc.
	Table TableRef
	On    Expr
}

func (j JoinClause) String() string {
	if strings.Contains(j.Type, "APPLY") {
		return fmt.Sprintf("%s %s", j.Type, j.Table.String())
	}
	if j.On != nil {
		return fmt.Sprintf("%s JOIN %s ON %s", j.Type, j.Table.String(), j.On.String())
	}
	return fmt.Sprintf("%s JOIN %s", j.Type, j.Table.String())
}

// OrderByItem represents an ORDER BY term.
type OrderByItem struct {
	Expr Expr
	Desc bool
}

func (o OrderByItem) String() string {
	if o.Desc {
		return fmt.Sprintf("%s DESC", o.Expr.String())
	}
	return fmt.Sprintf("%s ASC", o.Expr.String())
}

// CTEClause represents a Common Table Expression.
type CTEClause struct {
	Name     string
	Query    *GeneralSelectStmt
	InnerSQL string
}

func (c CTEClause) String() string {
	return fmt.Sprintf("%s AS (%s)", c.Name, c.InnerSQL)
}

// GroupByModifier defines the subtotal / grand total grouping modifier.
type GroupByModifier int

const (
	GroupByNormal GroupByModifier = iota
	GroupByRollup
	GroupByCube
	GroupByGroupingSets
)

// GeneralSelectStmt represents a complete interactive SELECT statement.
type GeneralSelectStmt struct {
	CTEs         []CTEClause
	Projections  []Projection
	From         TableRef
	Joins        []JoinClause
	Where        Expr
	GroupBy      []Expr
	GroupByMod   GroupByModifier
	GroupingSets [][]string
	Having       Expr
	OrderBy     []OrderByItem
	Limit       int64
	Offset      int64
	Distinct    bool
	TopPercent  bool
	TopWithTies bool
	IsExplain   bool
	IsAnalyze   bool
	QueryHints  []string
	RawSQL      string
}

func (s *GeneralSelectStmt) stmtNode() {}
func (s *GeneralSelectStmt) String() string {
	var b strings.Builder
	if s.IsExplain {
		if s.IsAnalyze {
			b.WriteString("EXPLAIN ANALYZE ")
		} else {
			b.WriteString("EXPLAIN ")
		}
	}
	if len(s.CTEs) > 0 {
		b.WriteString("WITH ")
		var cteStrs []string
		for _, c := range s.CTEs {
			cteStrs = append(cteStrs, c.String())
		}
		b.WriteString(strings.Join(cteStrs, ", "))
		b.WriteString(" ")
	}
	b.WriteString("SELECT ")
	if s.Distinct {
		b.WriteString("DISTINCT ")
	}
	var projStrs []string
	for _, p := range s.Projections {
		projStrs = append(projStrs, p.String())
	}
	b.WriteString(strings.Join(projStrs, ", "))
	if s.From.Name != "" {
		b.WriteString(" FROM ")
		b.WriteString(s.From.String())
	}
	for _, j := range s.Joins {
		b.WriteString(" ")
		b.WriteString(j.String())
	}
	if s.Where != nil {
		b.WriteString(" WHERE ")
		b.WriteString(s.Where.String())
	}
	if len(s.GroupBy) > 0 {
		b.WriteString(" GROUP BY ")
		var gbStrs []string
		for _, g := range s.GroupBy {
			gbStrs = append(gbStrs, g.String())
		}
		inner := strings.Join(gbStrs, ", ")
		switch s.GroupByMod {
		case GroupByRollup:
			b.WriteString(fmt.Sprintf("ROLLUP(%s)", inner))
		case GroupByCube:
			b.WriteString(fmt.Sprintf("CUBE(%s)", inner))
		case GroupByGroupingSets:
			var setStrs []string
			for _, set := range s.GroupingSets {
				setStrs = append(setStrs, fmt.Sprintf("(%s)", strings.Join(set, ", ")))
			}
			b.WriteString(fmt.Sprintf("GROUPING SETS (%s)", strings.Join(setStrs, ", ")))
		default:
			b.WriteString(inner)
		}
	}
	if s.Having != nil {
		b.WriteString(" HAVING ")
		b.WriteString(s.Having.String())
	}
	if len(s.OrderBy) > 0 {
		b.WriteString(" ORDER BY ")
		var obStrs []string
		for _, o := range s.OrderBy {
			obStrs = append(obStrs, o.String())
		}
		b.WriteString(strings.Join(obStrs, ", "))
	}
	if s.Limit >= 0 {
		b.WriteString(fmt.Sprintf(" LIMIT %d", s.Limit))
	}
	if s.Offset > 0 {
		b.WriteString(fmt.Sprintf(" OFFSET %d", s.Offset))
	}
	if len(s.QueryHints) > 0 {
		b.WriteString(fmt.Sprintf(" OPTION (%s)", strings.Join(s.QueryHints, ", ")))
	}
	return b.String()
}

// ColumnDef defines a table column in DDL.
type ColumnDef struct {
	Name       string
	Type       storage.DataType
	PrimaryKey bool
	Nullable   bool
}

// CreateTableStmt represents CREATE TABLE ...
type CreateTableStmt struct {
	Name        string
	IfNotExists bool
	Columns     []ColumnDef
}

func (s *CreateTableStmt) stmtNode() {}
func (s *CreateTableStmt) String() string {
	return fmt.Sprintf("CREATE TABLE %s", s.Name)
}

// CreateIndexStmt represents CREATE [UNIQUE] INDEX ... ON table (cols...) [INCLUDE (inc_cols...)]
type CreateIndexStmt struct {
	Name        string
	Table       string
	Columns     []string
	IncludeCols []string
	Unique      bool
}

func (s *CreateIndexStmt) stmtNode() {}
func (s *CreateIndexStmt) String() string {
	u := ""
	if s.Unique {
		u = "UNIQUE "
	}
	inc := ""
	if len(s.IncludeCols) > 0 {
		inc = fmt.Sprintf(" INCLUDE (%s)", strings.Join(s.IncludeCols, ", "))
	}
	return fmt.Sprintf("CREATE %sINDEX %s ON %s (%s)%s", u, s.Name, s.Table, strings.Join(s.Columns, ", "), inc)
}

// DropTableStmt represents DROP TABLE [IF EXISTS] table
type DropTableStmt struct {
	Name     string
	IfExists bool
}

func (s *DropTableStmt) stmtNode() {}
func (s *DropTableStmt) String() string {
	return fmt.Sprintf("DROP TABLE %s", s.Name)
}

// DropIndexStmt represents DROP INDEX index [ON table]
type DropIndexStmt struct {
	Name     string
	Table    string
	IfExists bool
}

func (s *DropIndexStmt) stmtNode() {}
func (s *DropIndexStmt) String() string {
	return fmt.Sprintf("DROP INDEX %s", s.Name)
}

// ExistsExpr represents [NOT] EXISTS (SELECT ...)
type ExistsExpr struct {
	Query *GeneralSelectStmt
	Not   bool
}

func (e *ExistsExpr) exprNode() {}
func (e *ExistsExpr) String() string {
	if e.Not {
		return fmt.Sprintf("NOT EXISTS (%s)", e.Query.String())
	}
	return fmt.Sprintf("EXISTS (%s)", e.Query.String())
}

// GeneralInsertStmt represents INSERT INTO table (cols) [OUTPUT ...] VALUES (tuples...) or INSERT INTO table (cols) [OUTPUT ...] SELECT ...
type GeneralInsertStmt struct {
	Table      string
	Columns    []string
	OutputCols []string
	Tuples     [][]Expr
	Select     *GeneralSelectStmt
}

func (s *GeneralInsertStmt) stmtNode() {}
func (s *GeneralInsertStmt) String() string {
	outStr := ""
	if len(s.OutputCols) > 0 {
		outStr = " OUTPUT " + strings.Join(s.OutputCols, ", ")
	}
	if s.Select != nil {
		if len(s.Columns) > 0 {
			return fmt.Sprintf("INSERT INTO %s (%s)%s %s", s.Table, strings.Join(s.Columns, ", "), outStr, s.Select.String())
		}
		return fmt.Sprintf("INSERT INTO %s%s %s", s.Table, outStr, s.Select.String())
	}
	return fmt.Sprintf("INSERT INTO %s (%s)%s VALUES ...", s.Table, strings.Join(s.Columns, ", "), outStr)
}


