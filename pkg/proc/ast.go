package proc

import (
	"fmt"
	"strings"

	"lionsql/pkg/storage"
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

func binaryPrecedence(op TokenType) int {
	switch op {
	case TokenOr:
		return 1
	case TokenAnd:
		return 2
	case TokenBitwiseOr, TokenBitwiseXor:
		return 3
	case TokenBitwiseAnd:
		return 4
	case TokenNot:
		return 5
	case TokenEq, TokenNeq, TokenLt, TokenLte, TokenGt, TokenGte, TokenLike:
		return 6
	case TokenPlus, TokenMinus:
		return 7
	case TokenAsterisk, TokenSlash, TokenDiv, TokenPercent:
		return 8
	default:
		return 0
	}
}

func (e *BinaryExpr) String() string {
	leftStr := ""
	if e.Left != nil {
		leftStr = e.Left.String()
		if childBin, ok := e.Left.(*BinaryExpr); ok {
			if binaryPrecedence(childBin.Op) < binaryPrecedence(e.Op) {
				leftStr = "(" + leftStr + ")"
			}
		}
	}
	rightStr := ""
	if e.Right != nil {
		rightStr = e.Right.String()
		if childBin, ok := e.Right.(*BinaryExpr); ok {
			pChild := binaryPrecedence(childBin.Op)
			pParent := binaryPrecedence(e.Op)
			if pChild < pParent || (pChild == pParent && (e.Op == TokenMinus || e.Op == TokenSlash || e.Op == TokenDiv || e.Op == TokenPercent || childBin.Op != e.Op)) {
				rightStr = "(" + rightStr + ")"
			}
		}
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
		if e.Op == TokenBitwiseNot {
			if _, isBin := e.Expr.(*BinaryExpr); isBin {
				return fmt.Sprintf("~(%s)", e.Expr.String())
			}
			return fmt.Sprintf("~%s", e.Expr.String())
		}
		if _, isBin := e.Expr.(*BinaryExpr); isBin {
			return fmt.Sprintf("%s (%s)", e.Op.String(), e.Expr.String())
		}
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
	IsFixed    bool
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
	Name    string
	Type    storage.DataType
	Output  bool // declared OUTPUT / OUT (by-reference)
	Default Expr // declared default expression, nil when the parameter is required
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

// DeclareStmt represents DECLARE @var TYPE [= Expr]
type DeclareStmt struct {
	Name      string
	Type      storage.DataType
	Precision int
	Init      Expr
}

func (s *DeclareStmt) stmtNode() {}
func (s *DeclareStmt) String() string {
	if s.Init != nil {
		return fmt.Sprintf("DECLARE %s %s = %s", s.Name, s.Type, s.Init.String())
	}
	return fmt.Sprintf("DECLARE %s %s", s.Name, s.Type)
}

// DeclareTableStmt represents DECLARE @var TABLE (colDefs...)
type DeclareTableStmt struct {
	Name       string
	Columns    []ColumnDef
	RawColDefs string
}

func (s *DeclareTableStmt) stmtNode() {}
func (s *DeclareTableStmt) String() string {
	if len(s.Columns) > 0 {
		var parts []string
		for _, c := range s.Columns {
			pkStr := ""
			if c.PrimaryKey {
				pkStr = " PRIMARY KEY"
			}
			parts = append(parts, fmt.Sprintf("%s %s%s", c.Name, c.Type, pkStr))
		}
		return fmt.Sprintf("DECLARE %s TABLE (%s)", s.Name, strings.Join(parts, ", "))
	}
	if s.RawColDefs != "" {
		return fmt.Sprintf("DECLARE %s TABLE (%s)", s.Name, s.RawColDefs)
	}
	return fmt.Sprintf("DECLARE %s TABLE", s.Name)
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

// ProcError represents a structured procedural execution error.
type ProcError struct {
	Number   int32
	Severity byte
	State    byte
	Message  string
}

func (e *ProcError) Error() string {
	return e.Message
}

func (e *ProcError) TdsErrorInfo() (int32, byte, byte) {
	return e.Number, e.Severity, e.State
}

// RaiseErrorStmt represents RAISERROR('message', severity, state, [args...])
type RaiseErrorStmt struct {
	Number     int32
	Severity   byte
	State      byte
	Message    string
	FormatArgs []Expr
}

func (s *RaiseErrorStmt) stmtNode() {}
func (s *RaiseErrorStmt) String() string {
	return fmt.Sprintf("RAISERROR('%s', %d, %d)", s.Message, s.Severity, s.State)
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
	Table         string
	Columns       []string
	Values        []Expr
	OutputCols    []string
	IntoVars      []string
	IntoTable     string
	IntoTableCols []string
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
	Table         string
	Assignments   []UpdateAssignment
	WhereCol      string
	WhereVal      Expr
	OutputCols    []string
	IntoVars      []string
	IntoTable     string
	IntoTableCols []string
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
	Table         string
	WhereCol      string
	WhereVal      Expr
	OutputCols    []string
	IntoVars      []string
	IntoTable     string
	IntoTableCols []string
}

func (s *DeleteStmt) stmtNode() {}
func (s *DeleteStmt) String() string {
	return fmt.Sprintf("DELETE FROM %s WHERE %s = %s", s.Table, s.WhereCol, s.WhereVal.String())
}

// ExecStmt represents EXEC [@return_var =] ProcName @arg1 [OUTPUT], @arg2 [OUTPUT]
type ExecStmt struct {
	ProcName  string
	Args      []Expr
	OutVars   []string
	ReturnVar string
}

func (s *ExecStmt) stmtNode() {}
func (s *ExecStmt) String() string {
	argStrs := make([]string, len(s.Args))
	for i, a := range s.Args {
		argStrs[i] = a.String()
	}
	prefix := ""
	if s.ReturnVar != "" {
		prefix = s.ReturnVar + " = "
	}
	return fmt.Sprintf("EXEC %s%s %s", prefix, s.ProcName, strings.Join(argStrs, ", "))
}

// DynamicExecStmt represents dynamic SQL execution: EXEC [@return_var =] ( string_expression )
type DynamicExecStmt struct {
	Expr      Expr
	ReturnVar string
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
	ErrorNumber int32
	Severity    byte
	State       byte
	Message     string
}

func (s *ThrowStmt) stmtNode() {}
func (s *ThrowStmt) String() string {
	return fmt.Sprintf("THROW %d, %q, %d", s.ErrorNumber, s.Message, s.State)
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

// WaitforStmt represents WAITFOR DELAY 'time' or WAITFOR TIME 'time'
type WaitforStmt struct {
	IsDelay bool
	Value   Expr
}

func (s *WaitforStmt) stmtNode() {}
func (s *WaitforStmt) String() string {
	kind := "DELAY"
	if !s.IsDelay {
		kind = "TIME"
	}
	return fmt.Sprintf("WAITFOR %s %s", kind, s.Value)
}

// CaseWhen represents a single WHEN cond THEN result arm
type CaseWhen struct {
	Condition Expr
	Result    Expr
}

// CaseExpr represents CASE [operand] WHEN ... THEN ... [ELSE ...] END
type CaseExpr struct {
	Operand Expr // nil for searched CASE, non-nil for simple CASE
	Whens   []CaseWhen
	Else    Expr
}

func (e *CaseExpr) exprNode() {}
func (e *CaseExpr) String() string {
	var b strings.Builder
	b.WriteString("CASE")
	if e.Operand != nil {
		b.WriteString(" ")
		b.WriteString(e.Operand.String())
	}
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
	Name       string
	Args       []Expr
	Distinct   bool
	HasOver    bool
	OverClause string
	Filter     Expr // for FILTER (WHERE condition)
}

func (e *FunctionCallExpr) exprNode() {}
func (e *FunctionCallExpr) String() string {
	argStrs := make([]string, len(e.Args))
	for i, a := range e.Args {
		argStrs[i] = a.String()
	}
	base := fmt.Sprintf("%s(%s)", e.Name, strings.Join(argStrs, ", "))
	if e.Distinct {
		base = fmt.Sprintf("%s(DISTINCT %s)", e.Name, strings.Join(argStrs, ", "))
	}
	if e.Filter != nil {
		base = fmt.Sprintf("%s FILTER (WHERE %s)", base, e.Filter.String())
	}
	if e.HasOver {
		return fmt.Sprintf("%s OVER (%s)", base, e.OverClause)
	}
	return base
}

// WithinGroupExpr represents an ordered-set aggregate like PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY col [ASC|DESC])
type WithinGroupExpr struct {
	Function   *FunctionCallExpr
	OrderBy    Expr
	Desc       bool
	HasOver    bool
	OverClause string
}

func (e *WithinGroupExpr) exprNode() {}
func (e *WithinGroupExpr) String() string {
	dir := ""
	if e.Desc {
		dir = " DESC"
	}
	base := fmt.Sprintf("%s WITHIN GROUP (ORDER BY %s%s)", e.Function.String(), e.OrderBy.String(), dir)
	if e.HasOver {
		return fmt.Sprintf("%s OVER (%s)", base, e.OverClause)
	}
	return base
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

// LikeExpr represents Expr [NOT] LIKE Pattern [ESCAPE Escape] or Expr [NOT] ILIKE Pattern [ESCAPE Escape]
type LikeExpr struct {
	Expr            Expr
	Not             bool
	Pattern         Expr
	Escape          Expr
	CaseInsensitive bool
	Collation       string
}

func (e *LikeExpr) exprNode() {}
func (e *LikeExpr) String() string {
	notStr := ""
	if e.Not {
		notStr = "NOT "
	}
	exprStr := ""
	if e.Expr != nil {
		exprStr = e.Expr.String()
	}
	patStr := ""
	if e.Pattern != nil {
		patStr = e.Pattern.String()
	}
	escStr := ""
	if e.Escape != nil {
		escStr = fmt.Sprintf(" ESCAPE %s", e.Escape.String())
	}
	opStr := "LIKE"
	if e.CaseInsensitive {
		opStr = "ILIKE"
	}
	collateStr := ""
	if e.Collation != "" {
		collateStr = fmt.Sprintf(" COLLATE %s", e.Collation)
	}
	return fmt.Sprintf("%s%s %s%s %s%s", exprStr, collateStr, notStr, opStr, patStr, escStr)
}

// CollateExpr represents Expr COLLATE collation_name
type CollateExpr struct {
	Expr      Expr
	Collation string
}

func (e *CollateExpr) exprNode() {}
func (e *CollateExpr) String() string {
	exprStr := ""
	if e.Expr != nil {
		exprStr = e.Expr.String()
	}
	return fmt.Sprintf("%s COLLATE %s", exprStr, e.Collation)
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
	if e.Table == "" {
		return fmt.Sprintf("(SELECT %s)", e.Column)
	}
	if e.Where != nil {
		return fmt.Sprintf("(SELECT %s FROM %s WHERE %s)", e.Column, e.Table, e.Where.String())
	}
	return fmt.Sprintf("(SELECT %s FROM %s)", e.Column, e.Table)
}

// QuantifiedComparisonExpr represents Expr Op (ALL | ANY | SOME) (Subquery | List)
type QuantifiedComparisonExpr struct {
	Expr       Expr
	Op         TokenType // TokenEq, TokenNeq, TokenLt, TokenLte, TokenGt, TokenGte
	Quantifier TokenType // TokenAll, TokenAny, TokenSome
	Query      *GeneralSelectStmt
	List       []Expr
}

func (e *QuantifiedComparisonExpr) exprNode() {}
func (e *QuantifiedComparisonExpr) String() string {
	qStr := "ANY"
	if e.Quantifier == TokenAll {
		qStr = "ALL"
	} else if e.Quantifier == TokenSome {
		qStr = "SOME"
	}
	opStr := e.Op.String()
	if e.Query != nil {
		return fmt.Sprintf("%s %s %s (%s)", e.Expr.String(), opStr, qStr, e.Query.String())
	}
	var items []string
	for _, item := range e.List {
		items = append(items, item.String())
	}
	return fmt.Sprintf("%s %s %s (%s)", e.Expr.String(), opStr, qStr, strings.Join(items, ", "))
}

// UpdateWhereStmt represents UPDATE table SET col1 = expr1, ... [OUTPUT ...] WHERE <arbitrary condition>
type UpdateWhereStmt struct {
	Table         string
	Assignments   []UpdateAssignment
	OutputCols    []string
	IntoVars      []string
	IntoTable     string
	IntoTableCols []string
	Where         Expr
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
	Table         string
	OutputCols    []string
	IntoVars      []string
	IntoTable     string
	IntoTableCols []string
	Where         Expr
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
	OrderBy []OrderByItem
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
	Expr     Expr
	Alias    string
	IsAssign bool
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
	Columns  []string
	Query    *GeneralSelectStmt
	InnerSQL string
}

func (c CTEClause) String() string {
	if len(c.Columns) > 0 {
		return fmt.Sprintf("%s(%s) AS (%s)", c.Name, strings.Join(c.Columns, ", "), c.InnerSQL)
	}
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

// SetOpBranch represents a chained UNION [ALL], INTERSECT, or EXCEPT/MINUS branch.
type SetOpBranch struct {
	Op    string // "UNION", "UNION ALL", "INTERSECT", "EXCEPT", "MINUS"
	Query *GeneralSelectStmt
}

// GeneralSelectStmt represents a complete interactive SELECT statement.
type GeneralSelectStmt struct {
	CTEs         []CTEClause
	Projections  []Projection
	IntoTable    string
	From         TableRef
	Joins        []JoinClause
	Where        Expr
	GroupBy      []Expr
	GroupByMod   GroupByModifier
	GroupingSets [][]string
	Having       Expr
	OrderBy      []OrderByItem
	Limit        int64
	LimitExpr    Expr
	Offset       int64
	OffsetExpr   Expr
	SetOps       []SetOpBranch
	Distinct     bool
	TopPercent   bool
	TopWithTies  bool
	IsExplain    bool
	IsAnalyze    bool
	QueryHints   []string
	RawSQL       string
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
	if s.IntoTable != "" {
		b.WriteString(" INTO ")
		b.WriteString(s.IntoTable)
	}
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
	if s.LimitExpr != nil {
		b.WriteString(fmt.Sprintf(" LIMIT %s", s.LimitExpr.String()))
	} else if s.Limit >= 0 {
		b.WriteString(fmt.Sprintf(" LIMIT %d", s.Limit))
	}
	if s.OffsetExpr != nil {
		b.WriteString(fmt.Sprintf(" OFFSET %s", s.OffsetExpr.String()))
	} else if s.Offset > 0 {
		b.WriteString(fmt.Sprintf(" OFFSET %d", s.Offset))
	}
	for _, sop := range s.SetOps {
		if sop.Query != nil {
			b.WriteString(fmt.Sprintf(" %s %s", sop.Op, sop.Query.String()))
		}
	}
	if len(s.QueryHints) > 0 {
		b.WriteString(fmt.Sprintf(" OPTION (%s)", strings.Join(s.QueryHints, ", ")))
	}
	return b.String()
}

// ColumnDef defines a table column in DDL.
type ColumnDef struct {
	Name         string
	Type         storage.DataType
	RawType      string
	IsFixed      bool
	PrimaryKey   bool
	Nullable     bool
	Identity          bool
	IdentitySeed      int64
	IdentityIncrement int64
	IsRowVersion      bool
	Unique       bool
	Precision    int
	Scale        int
	Default      string
	CheckExpr    string
	ComputedExpr string
	IsComputed   bool
	ForeignKey   *storage.ForeignKeySchema
}

// CreateTableStmt represents CREATE [TEMP|TEMPORARY] TABLE [IF NOT EXISTS] ...
type CreateTableStmt struct {
	Name             string
	IfNotExists      bool
	Temporary        bool
	Columns          []ColumnDef
	PrimaryKeyCols   []string
	ForeignKeys      []storage.ForeignKeySchema
	UniqueIndexes    []storage.IndexSchema
	CheckConstraints []string
	Partition        *storage.PartitionDef
	Durability       string
	MemoryOptimized  bool
}

func (s *CreateTableStmt) stmtNode() {}
func (s *CreateTableStmt) String() string {
	return fmt.Sprintf("CREATE TABLE %s", s.Name)
}

// AlterActionType enumerates the sub-actions of an ALTER TABLE statement.
type AlterActionType string

const (
	AlterAddColumn         AlterActionType = "ADD_COLUMN"
	AlterDropColumn        AlterActionType = "DROP_COLUMN"
	AlterColumnSetDefault  AlterActionType = "ALTER_COLUMN_SET_DEFAULT"
	AlterColumnDropDefault AlterActionType = "ALTER_COLUMN_DROP_DEFAULT"
	AlterColumnSetNotNull  AlterActionType = "ALTER_COLUMN_SET_NOT_NULL"
	AlterColumnDropNotNull AlterActionType = "ALTER_COLUMN_DROP_NOT_NULL"
	AlterColumnType        AlterActionType = "ALTER_COLUMN_TYPE"
	AlterRenameTable       AlterActionType = "RENAME_TABLE"
	AlterRenameColumn      AlterActionType = "RENAME_COLUMN"
	AlterAddPrimaryKey     AlterActionType = "ADD_PRIMARY_KEY"
	AlterAddUnique         AlterActionType = "ADD_UNIQUE"
	AlterAddForeignKey     AlterActionType = "ADD_FOREIGN_KEY"
	AlterAddCheck          AlterActionType = "ADD_CHECK"
	AlterDropConstraint    AlterActionType = "DROP_CONSTRAINT"
	AlterDropPartition     AlterActionType = "DROP_PARTITION"
	AlterSetStorageParams  AlterActionType = "SET_STORAGE_PARAMS"
)

// AlterTableAction represents a single clause inside ALTER TABLE.
type AlterTableAction struct {
	Type           AlterActionType
	IfExists       bool
	IfNotExists    bool
	Column         ColumnDef
	ColumnName     string
	NewName        string
	DropColumns    []string
	DefaultExpr    string
	ConstraintName string
	ConstraintCols []string
	ForeignKey     *storage.ForeignKeySchema
	CheckExpr      string
	PartitionName  string
}

func (a AlterTableAction) String() string {
	switch a.Type {
	case AlterAddColumn:
		if a.IfNotExists {
			return fmt.Sprintf("ADD COLUMN IF NOT EXISTS %s %s", a.Column.Name, a.Column.RawType)
		}
		nullStr := ""
		if !a.Column.Nullable {
			nullStr = " NOT NULL"
		}
		defStr := ""
		if a.Column.Default != "" {
			defStr = " DEFAULT " + a.Column.Default
		}
		rawType := a.Column.RawType
		if rawType == "" {
			rawType = a.Column.Type.String()
		}
		return fmt.Sprintf("ADD %s %s%s%s", a.Column.Name, rawType, nullStr, defStr)
	case AlterDropColumn:
		if len(a.DropColumns) > 0 {
			return fmt.Sprintf("DROP COLUMN %s", strings.Join(a.DropColumns, ", "))
		}
		if a.IfExists {
			return fmt.Sprintf("DROP COLUMN IF EXISTS %s", a.ColumnName)
		}
		return fmt.Sprintf("DROP COLUMN %s", a.ColumnName)
	case AlterRenameColumn:
		return fmt.Sprintf("RENAME COLUMN %s TO %s", a.ColumnName, a.NewName)
	case AlterRenameTable:
		return fmt.Sprintf("RENAME TO %s", a.NewName)
	case AlterColumnSetNotNull:
		return fmt.Sprintf("ALTER COLUMN %s SET NOT NULL", a.ColumnName)
	case AlterColumnDropNotNull:
		return fmt.Sprintf("ALTER COLUMN %s DROP NOT NULL", a.ColumnName)
	case AlterColumnType:
		rawType := a.Column.RawType
		if rawType == "" {
			rawType = a.Column.Type.String()
		}
		return fmt.Sprintf("ALTER COLUMN %s TYPE %s", a.ColumnName, rawType)
	case AlterAddPrimaryKey:
		return fmt.Sprintf("ADD PRIMARY KEY (%s)", strings.Join(a.ConstraintCols, ", "))
	case AlterAddUnique:
		return fmt.Sprintf("ADD UNIQUE (%s)", strings.Join(a.ConstraintCols, ", "))
	case AlterAddCheck:
		return fmt.Sprintf("ADD CHECK (%s)", a.CheckExpr)
	case AlterDropConstraint:
		return fmt.Sprintf("DROP CONSTRAINT %s", a.ConstraintName)
	case AlterDropPartition:
		return fmt.Sprintf("DROP PARTITION %s", a.PartitionName)
	default:
		return ""
	}
}

// AlterTableStmt represents ALTER TABLE [IF EXISTS] <table> <actions...>
type AlterTableStmt struct {
	Table    string
	IfExists bool
	Actions  []AlterTableAction
}

func (s *AlterTableStmt) stmtNode() {}
func (s *AlterTableStmt) String() string {
	var sb strings.Builder
	sb.WriteString("ALTER TABLE ")
	if s.IfExists {
		sb.WriteString("IF EXISTS ")
	}
	sb.WriteString(s.Table)
	if len(s.Actions) > 0 {
		sb.WriteString(" ")
		acts := make([]string, 0, len(s.Actions))
		for _, a := range s.Actions {
			actStr := a.String()
			if actStr != "" {
				acts = append(acts, actStr)
			}
		}
		sb.WriteString(strings.Join(acts, ", "))
	}
	return sb.String()
}

// CreateIndexStmt represents CREATE [UNIQUE] INDEX ... ON table (cols...) [INCLUDE (inc_cols...)]
type CreateIndexStmt struct {
	Name        string
	Table       string
	Columns     []string
	IncludeCols []string
	Unique      bool
	IfNotExists bool
	WhereClause string
	Expression  string
	ExprColumn  string
	ExprPath    string
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
	if s.Expression != "" {
		return fmt.Sprintf("CREATE %sINDEX %s ON %s ((%s))%s", u, s.Name, s.Table, s.Expression, inc)
	}
	return fmt.Sprintf("CREATE %sINDEX %s ON %s (%s)%s", u, s.Name, s.Table, strings.Join(s.Columns, ", "), inc)
}

// DropTableStmt represents DROP TABLE [IF EXISTS] table [, table2 ...] [CASCADE]
type DropTableStmt struct {
	Name     string
	Names    []string
	IfExists bool
	Cascade  bool
}

func (s *DropTableStmt) stmtNode() {}
func (s *DropTableStmt) String() string {
	return fmt.Sprintf("DROP TABLE %s", s.Name)
}

// DropIndexStmt represents DROP INDEX [IF EXISTS] index [ON table] [CASCADE]
type DropIndexStmt struct {
	Name     string
	Table    string
	IfExists bool
	Cascade  bool
}

func (s *DropIndexStmt) stmtNode() {}
func (s *DropIndexStmt) String() string {
	return fmt.Sprintf("DROP INDEX %s", s.Name)
}

// TruncateStmt represents TRUNCATE [TABLE] [ONLY] t1 [, t2 ...] [RESTART IDENTITY | CONTINUE IDENTITY] [CASCADE | RESTRICT]
type TruncateStmt struct {
	Tables          []string
	Cascade         bool
	RestartIdentity bool
}

func (s *TruncateStmt) stmtNode() {}
func (s *TruncateStmt) String() string {
	return fmt.Sprintf("TRUNCATE TABLE %s", strings.Join(s.Tables, ", "))
}

// MergeStmt represents a T-SQL MERGE statement.
type MergeStmt struct {
	SQL string
}

func (s *MergeStmt) stmtNode() {}
func (s *MergeStmt) String() string {
	return s.SQL
}

// CreateViewStmt represents CREATE [OR REPLACE|OR ALTER] VIEW [IF NOT EXISTS] <name> AS <select> or ALTER VIEW <name> AS <select>
type CreateViewStmt struct {
	Name        string
	SelectSQL   string
	OrReplace   bool
	IfNotExists bool
	IsAlter     bool
}

func (s *CreateViewStmt) stmtNode() {}
func (s *CreateViewStmt) String() string {
	if s.IsAlter {
		return fmt.Sprintf("ALTER VIEW %s AS %s", s.Name, s.SelectSQL)
	}
	return fmt.Sprintf("CREATE VIEW %s AS %s", s.Name, s.SelectSQL)
}

// DropViewStmt represents DROP VIEW [IF EXISTS] <name>
type DropViewStmt struct {
	Name     string
	IfExists bool
}

func (s *DropViewStmt) stmtNode() {}
func (s *DropViewStmt) String() string {
	return fmt.Sprintf("DROP VIEW %s", s.Name)
}

// CreateStatisticsStmt represents CREATE STATISTICS <name> ON <table> (<cols...>)
type CreateStatisticsStmt struct {
	Name    string
	Table   string
	Columns []string
}

func (s *CreateStatisticsStmt) stmtNode() {}
func (s *CreateStatisticsStmt) String() string {
	return fmt.Sprintf("CREATE STATISTICS %s ON %s (%s)", s.Name, s.Table, strings.Join(s.Columns, ", "))
}

// DropStatisticsStmt represents DROP STATISTICS <name> ON <table> or DROP STATISTICS <table>.<name>
type DropStatisticsStmt struct {
	Name  string
	Table string
}

func (s *DropStatisticsStmt) stmtNode() {}
func (s *DropStatisticsStmt) String() string {
	return fmt.Sprintf("DROP STATISTICS %s", s.Name)
}

// ReindexStmt represents REINDEX [TABLE|INDEX] <target>
type ReindexStmt struct {
	Target string
}

func (s *ReindexStmt) stmtNode() {}
func (s *ReindexStmt) String() string {
	return fmt.Sprintf("REINDEX %s", s.Target)
}

// ServerCursorDeclareStmt represents a PostgreSQL/T-SQL server-side DECLARE <name> ... CURSOR ... FOR <query>.
type ServerCursorDeclareStmt struct {
	Name     string
	Binary   bool
	Scroll   bool
	WithHold bool
	Query    string
}

// ServerCursorFetchStmt represents FETCH or MOVE [direction] [count] [FROM|IN] <cursor_name>.
type ServerCursorFetchStmt struct {
	Direction  string
	Count      int
	IsAll      bool
	CursorName string
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

// GeneralInsertStmt represents INSERT INTO table (cols) [OUTPUT ...] VALUES (tuples...) or INSERT INTO table (cols) [OUTPUT ...] SELECT ... or INSERT INTO table (cols) [OUTPUT ...] EXEC ...
type GeneralInsertStmt struct {
	Table         string
	Columns       []string
	OutputCols    []string
	IntoVars      []string
	IntoTable     string
	IntoTableCols []string
	Tuples        [][]Expr
	Select        *GeneralSelectStmt
	Exec          *ExecStmt
}

func (s *GeneralInsertStmt) stmtNode() {}
func (s *GeneralInsertStmt) String() string {
	outStr := ""
	if len(s.OutputCols) > 0 {
		outStr = " OUTPUT " + strings.Join(s.OutputCols, ", ")
	}
	if s.Exec != nil {
		if len(s.Columns) > 0 {
			return fmt.Sprintf("INSERT INTO %s (%s)%s %s", s.Table, strings.Join(s.Columns, ", "), outStr, s.Exec.String())
		}
		return fmt.Sprintf("INSERT INTO %s%s %s", s.Table, outStr, s.Exec.String())
	}
	if s.Select != nil {
		if len(s.Columns) > 0 {
			return fmt.Sprintf("INSERT INTO %s (%s)%s %s", s.Table, strings.Join(s.Columns, ", "), outStr, s.Select.String())
		}
		return fmt.Sprintf("INSERT INTO %s%s %s", s.Table, outStr, s.Select.String())
	}
	return fmt.Sprintf("INSERT INTO %s (%s)%s VALUES ...", s.Table, strings.Join(s.Columns, ", "), outStr)
}


