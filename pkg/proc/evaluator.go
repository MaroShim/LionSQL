package proc

import (
	"errors"
	"fmt"
	"time"

	"sqltiger/pkg/storage"
)

var (
	ErrVariableUndefined = errors.New("variable is not declared")
	ErrDivisionByZero    = errors.New("division by zero")
	ErrExecutionAborted  = errors.New("execution aborted by error")
	ErrInfiniteLoopGuard = errors.New("infinite loop limit exceeded (max 100000 iterations)")
)

// ExecutionContext holds the runtime state for a stored procedure invocation.
type ExecutionContext struct {
	Tx           storage.Transaction
	Variables    map[string]storage.Value
	Printed      []string
	ReturnValue  storage.Value
	Returned     bool
	ExplicitTx   bool
	CurrentRow   storage.Record // For column reference resolution in UPDATE
}

// NewExecutionContext creates an execution environment with an active transaction.
func NewExecutionContext(tx storage.Transaction) *ExecutionContext {
	return &ExecutionContext{
		Tx:        tx,
		Variables: make(map[string]storage.Value),
		Printed:   make([]string, 0),
	}
}

// ExecutionResult captures the output of a procedure execution.
type ExecutionResult struct {
	ReturnValue storage.Value
	Variables   map[string]storage.Value
	Printed     []string
	Duration    time.Duration
}

// Run executes a parsed stored procedure AST within an ambient transaction.
func Run(proc *CreateProcStmt, args map[string]storage.Value, session storage.Session) (*ExecutionResult, error) {
	start := time.Now()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to begin ambient transaction: %w", err)
	}

	ctx := NewExecutionContext(tx)

	// Bind parameters
	for _, p := range proc.Params {
		val, exists := args[p.Name]
		if !exists {
			val = storage.NullValue(p.Type)
		}
		ctx.Variables[p.Name] = val
	}

	// Execute procedure body
	evalErr := evalStmt(proc.Body, ctx)

	// Transaction boundary: auto-rollback on error, auto-commit on success
	if evalErr != nil {
		_ = tx.Rollback()
		return nil, evalErr
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &ExecutionResult{
		ReturnValue: ctx.ReturnValue,
		Variables:   ctx.Variables,
		Printed:     ctx.Printed,
		Duration:    time.Since(start),
	}, nil
}

func evalStmt(stmt Statement, ctx *ExecutionContext) error {
	if ctx.Returned {
		return nil
	}

	switch s := stmt.(type) {
	case *BlockStmt:
		for _, subStmt := range s.Statements {
			if err := evalStmt(subStmt, ctx); err != nil {
				return err
			}
			if ctx.Returned {
				break
			}
		}
		return nil

	case *DeclareStmt:
		ctx.Variables[s.Name] = storage.NullValue(s.Type)
		return nil

	case *SetStmt:
		val, err := evalExpr(s.Value, ctx)
		if err != nil {
			return err
		}
		ctx.Variables[s.Name] = val
		return nil

	case *IfStmt:
		condVal, err := evalExpr(s.Condition, ctx)
		if err != nil {
			return err
		}
		if isTruthy(condVal) {
			return evalStmt(s.Then, ctx)
		} else if s.Else != nil {
			return evalStmt(s.Else, ctx)
		}
		return nil

	case *WhileStmt:
		iterations := 0
		for {
			iterations++
			if iterations > 100000 {
				return ErrInfiniteLoopGuard
			}
			condVal, err := evalExpr(s.Condition, ctx)
			if err != nil {
				return err
			}
			if !isTruthy(condVal) {
				break
			}
			if err := evalStmt(s.Body, ctx); err != nil {
				return err
			}
			if ctx.Returned {
				break
			}
		}
		return nil

	case *ReturnStmt:
		if s.Value != nil {
			val, err := evalExpr(s.Value, ctx)
			if err != nil {
				return err
			}
			ctx.ReturnValue = val
		}
		ctx.Returned = true
		return nil

	case *PrintStmt:
		val, err := evalExpr(s.Expr, ctx)
		if err != nil {
			return err
		}
		ctx.Printed = append(ctx.Printed, val.String())
		return nil

	case *RaiseErrorStmt:
		return fmt.Errorf("RAISERROR: %s", s.Message)

	case *TxStmt:
		switch s.Action {
		case TxBegin:
			ctx.ExplicitTx = true
		case TxCommit:
			return ctx.Tx.Commit()
		case TxRollback:
			_ = ctx.Tx.Rollback()
			return errors.New("transaction rolled back explicitly")
		}
		return nil

	case *SelectIntoStmt:
		pkVal, err := evalExpr(s.WhereVal, ctx)
		if err != nil {
			return err
		}
		rec, err := ctx.Tx.Get(s.Table, pkVal)
		if err != nil {
			return fmt.Errorf("SELECT INTO error on %s: %w", s.Table, err)
		}
		colVal, exists := rec[s.Column]
		if !exists {
			return fmt.Errorf("column %s not found in table %s", s.Column, s.Table)
		}
		ctx.Variables[s.IntoVar] = colVal
		return nil

	case *InsertStmt:
		rec := make(storage.Record)
		for i, col := range s.Columns {
			val, err := evalExpr(s.Values[i], ctx)
			if err != nil {
				return err
			}
			rec[col] = val
		}
		return ctx.Tx.Insert(s.Table, rec)

	case *UpdateStmt:
		pkVal, err := evalExpr(s.WhereVal, ctx)
		if err != nil {
			return err
		}
		existing, err := ctx.Tx.Get(s.Table, pkVal)
		if err != nil {
			return fmt.Errorf("UPDATE error on %s: %w", s.Table, err)
		}

		ctx.CurrentRow = existing
		updatedRec := make(storage.Record)
		for _, a := range s.Assignments {
			val, err := evalExpr(a.Value, ctx)
			if err != nil {
				return err
			}
			updatedRec[a.Column] = val
		}
		ctx.CurrentRow = nil

		return ctx.Tx.Update(s.Table, pkVal, updatedRec)

	case *DeleteStmt:
		pkVal, err := evalExpr(s.WhereVal, ctx)
		if err != nil {
			return err
		}
		return ctx.Tx.Delete(s.Table, pkVal)

	default:
		return fmt.Errorf("unsupported statement type: %T", stmt)
	}
}

func evalExpr(expr Expr, ctx *ExecutionContext) (storage.Value, error) {
	if expr == nil {
		return storage.Value{IsNull: true}, nil
	}

	switch e := expr.(type) {
	case *LiteralExpr:
		return e.Val, nil

	case *VariableExpr:
		val, exists := ctx.Variables[e.Name]
		if !exists {
			return storage.Value{}, fmt.Errorf("%w: %s", ErrVariableUndefined, e.Name)
		}
		return val, nil

	case *ColumnRefExpr:
		if ctx.CurrentRow != nil {
			if val, exists := ctx.CurrentRow[e.Name]; exists {
				return val, nil
			}
		}
		return storage.Value{}, fmt.Errorf("unresolved column reference %s", e.Name)

	case *UnaryExpr:
		val, err := evalExpr(e.Expr, ctx)
		if err != nil {
			return storage.Value{}, err
		}
		switch e.Op {
		case TokenMinus:
			if val.Type == storage.TypeInt {
				return storage.IntValue(-val.IVal), nil
			} else if val.Type == storage.TypeFloat {
				return storage.FloatValue(-val.FVal), nil
			}
			return storage.Value{}, fmt.Errorf("cannot negate non-numeric type %s", val.Type)
		case TokenNot:
			return storage.BoolValue(!isTruthy(val)), nil
		}

	case *BinaryExpr:
		left, err := evalExpr(e.Left, ctx)
		if err != nil {
			return storage.Value{}, err
		}
		right, err := evalExpr(e.Right, ctx)
		if err != nil {
			return storage.Value{}, err
		}
		return evalBinary(e.Op, left, right)
	}

	return storage.Value{}, fmt.Errorf("unsupported expression: %T", expr)
}

func evalBinary(op TokenType, left, right storage.Value) (storage.Value, error) {
	switch op {
	case TokenPlus:
		if left.Type == storage.TypeInt && right.Type == storage.TypeInt {
			return storage.IntValue(left.IVal + right.IVal), nil
		}
		if left.Type == storage.TypeVarChar || right.Type == storage.TypeVarChar {
			return storage.StringValue(left.String() + right.String()), nil
		}
		if left.Type == storage.TypeFloat || right.Type == storage.TypeFloat {
			return storage.FloatValue(asFloat(left) + asFloat(right)), nil
		}
	case TokenMinus:
		if left.Type == storage.TypeInt && right.Type == storage.TypeInt {
			return storage.IntValue(left.IVal - right.IVal), nil
		}
		if left.Type == storage.TypeFloat || right.Type == storage.TypeFloat {
			return storage.FloatValue(asFloat(left) - asFloat(right)), nil
		}
	case TokenAsterisk:
		if left.Type == storage.TypeInt && right.Type == storage.TypeInt {
			return storage.IntValue(left.IVal * right.IVal), nil
		}
		if left.Type == storage.TypeFloat || right.Type == storage.TypeFloat {
			return storage.FloatValue(asFloat(left) * asFloat(right)), nil
		}
	case TokenSlash:
		if right.IVal == 0 && right.FVal == 0 {
			return storage.Value{}, ErrDivisionByZero
		}
		if left.Type == storage.TypeInt && right.Type == storage.TypeInt {
			return storage.IntValue(left.IVal / right.IVal), nil
		}
		return storage.FloatValue(asFloat(left) / asFloat(right)), nil

	case TokenEq:
		return storage.BoolValue(left.Equal(right)), nil
	case TokenNeq:
		return storage.BoolValue(!left.Equal(right)), nil
	case TokenLt:
		return storage.BoolValue(compareValues(left, right) < 0), nil
	case TokenLte:
		return storage.BoolValue(compareValues(left, right) <= 0), nil
	case TokenGt:
		return storage.BoolValue(compareValues(left, right) > 0), nil
	case TokenGte:
		return storage.BoolValue(compareValues(left, right) >= 0), nil
	case TokenAnd:
		return storage.BoolValue(isTruthy(left) && isTruthy(right)), nil
	case TokenOr:
		return storage.BoolValue(isTruthy(left) || isTruthy(right)), nil
	}

	return storage.Value{}, fmt.Errorf("invalid binary operator %v on %s and %s", op, left.Type, right.Type)
}

func asFloat(v storage.Value) float64 {
	if v.Type == storage.TypeInt {
		return float64(v.IVal)
	}
	return v.FVal
}

func compareValues(a, b storage.Value) int {
	if a.Type == storage.TypeInt && b.Type == storage.TypeInt {
		if a.IVal < b.IVal {
			return -1
		} else if a.IVal > b.IVal {
			return 1
		}
		return 0
	}
	if a.Type == storage.TypeFloat || b.Type == storage.TypeFloat {
		fa, fb := asFloat(a), asFloat(b)
		if fa < fb {
			return -1
		} else if fa > fb {
			return 1
		}
		return 0
	}
	if a.Type == storage.TypeVarChar && b.Type == storage.TypeVarChar {
		if a.SVal < b.SVal {
			return -1
		} else if a.SVal > b.SVal {
			return 1
		}
		return 0
	}
	return 0
}

func isTruthy(v storage.Value) bool {
	if v.IsNull {
		return false
	}
	switch v.Type {
	case storage.TypeBool:
		return v.BVal
	case storage.TypeInt:
		return v.IVal != 0
	case storage.TypeFloat:
		return v.FVal != 0.0
	case storage.TypeVarChar:
		return len(v.SVal) > 0 && v.SVal != "0" && v.SVal != "false"
	default:
		return false
	}
}
