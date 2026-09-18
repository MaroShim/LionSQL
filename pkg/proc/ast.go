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
func (e *LiteralExpr) String() string { return e.Val.String() }

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
	return fmt.Sprintf("(%s %v %s)", e.Left.String(), e.Op, e.Right.String())
}

// UnaryExpr represents unary operations (NOT, -).
type UnaryExpr struct {
	Op   TokenType
	Expr Expr
}

func (e *UnaryExpr) exprNode() {}
func (e *UnaryExpr) String() string {
	return fmt.Sprintf("%v(%s)", e.Op, e.Expr.String())
}

// CreateProcStmt represents CREATE PROCEDURE ... AS BEGIN ... END
type CreateProcStmt struct {
	Name   string
	Params []ParamDef
	Body   Statement
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
)

// TxStmt represents BEGIN TRAN, COMMIT TRAN, ROLLBACK TRAN
type TxStmt struct {
	Action TxAction
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

// InsertStmt represents INSERT INTO table (cols...) VALUES (exprs...)
type InsertStmt struct {
	Table   string
	Columns []string
	Values  []Expr
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

// UpdateStmt represents UPDATE table SET col1 = expr1, ... WHERE pk_col = expr
type UpdateStmt struct {
	Table       string
	Assignments []UpdateAssignment
	WhereCol    string
	WhereVal    Expr
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

// DeleteStmt represents DELETE FROM table WHERE pk_col = expr
type DeleteStmt struct {
	Table    string
	WhereCol string
	WhereVal Expr
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

