package proc

import (
	"fmt"
	"math"
	"strings"
	"time"

	"sqlion/pkg/storage"
)

// IsDeterministicFunction reports whether the named built-in function produces identical results given identical arguments.
func IsDeterministicFunction(name string) bool {
	upper := strings.ToUpper(cleanBuiltinName(name))
	switch upper {
	case "GETDATE", "NOW", "GETUTCDATE", "SYSDATETIME", "RAND", "NEWID", "NEWID7", "UUIDV7", "UUID_GENERATE_V7", "GEN_RANDOM_UUID", "UUID", "SCOPE_IDENTITY", "CURRENT_TIMESTAMP":
		return false
	default:
		return IsBuiltinFunction(name)
	}
}

// FoldConstants evaluates compile-time constant expressions and applies logical simplifications across an AST.
func FoldConstants(expr Expr) Expr {
	if expr == nil {
		return nil
	}

	switch e := expr.(type) {
	case *LiteralExpr, *VariableExpr, *ColumnRefExpr, *ScalarSubqueryExpr, *ExistsExpr:
		return expr

	case *UnaryExpr:
		e.Expr = FoldConstants(e.Expr)
		if isConstantExpr(e.Expr) {
			val, err := EvalRowExpr(e, nil, nil)
			if err == nil {
				return anyToLiteralExpr(val)
			}
		}
		// Double negation elimination: NOT (NOT x) -> x
		if e.Op == TokenNot {
			if inner, ok := e.Expr.(*UnaryExpr); ok && inner.Op == TokenNot {
				return inner.Expr
			}
		}
		return e

	case *BinaryExpr:
		e.Left = FoldConstants(e.Left)
		e.Right = FoldConstants(e.Right)

		// Logical boolean simplifications
		if e.Op == TokenAnd {
			if isBoolLiteral(e.Left, true) {
				return e.Right
			}
			if isBoolLiteral(e.Right, true) {
				return e.Left
			}
			if isBoolLiteral(e.Left, false) {
				return &LiteralExpr{Val: storage.BoolValue(false)}
			}
			if isBoolLiteral(e.Right, false) {
				return &LiteralExpr{Val: storage.BoolValue(false)}
			}
		} else if e.Op == TokenOr {
			if isBoolLiteral(e.Left, true) {
				return &LiteralExpr{Val: storage.BoolValue(true)}
			}
			if isBoolLiteral(e.Right, true) {
				return &LiteralExpr{Val: storage.BoolValue(true)}
			}
			if isBoolLiteral(e.Left, false) {
				return e.Right
			}
			if isBoolLiteral(e.Right, false) {
				return e.Left
			}
		}

		// Compile-time evaluation of constant binary operations
		if isConstantExpr(e.Left) && isConstantExpr(e.Right) {
			leftVal := literalToAny(e.Left.(*LiteralExpr).Val)
			rightVal := literalToAny(e.Right.(*LiteralExpr).Val)
			val, err := evalRowBinaryOp(e.Op, leftVal, rightVal)
			if err == nil {
				return anyToLiteralExpr(val)
			}
		}
		return e

	case *FunctionCallExpr:
		allConst := IsDeterministicFunction(e.Name)
		newArgs := make([]Expr, len(e.Args))
		for i, a := range e.Args {
			newArgs[i] = FoldConstants(a)
			if !isConstantExpr(newArgs[i]) {
				allConst = false
			}
		}
		e.Args = newArgs
		if allConst {
			val, err := evalRowFunction(e.Name, newArgs, nil, nil)
			if err == nil {
				return anyToLiteralExpr(val)
			}
		}
		return e

	case *WithinGroupExpr:
		return e

	case *IsNullExpr:
		e.Expr = FoldConstants(e.Expr)
		if isConstantExpr(e.Expr) {
			val, err := EvalRowExpr(e, nil, nil)
			if err == nil {
				return anyToLiteralExpr(val)
			}
		}
		return e

	case *BetweenExpr:
		e.Expr = FoldConstants(e.Expr)
		e.Low = FoldConstants(e.Low)
		e.High = FoldConstants(e.High)
		if isConstantExpr(e.Expr) && isConstantExpr(e.Low) && isConstantExpr(e.High) {
			val, err := EvalRowExpr(e, nil, nil)
			if err == nil {
				return anyToLiteralExpr(val)
			}
		}
		return e

	case *InExpr:
		e.Expr = FoldConstants(e.Expr)
		allConst := (e.Query == nil) && isConstantExpr(e.Expr)
		for i, item := range e.List {
			e.List[i] = FoldConstants(item)
			if !isConstantExpr(e.List[i]) {
				allConst = false
			}
		}
		if allConst {
			val, err := EvalRowExpr(e, nil, nil)
			if err == nil {
				return anyToLiteralExpr(val)
			}
		}
		return e

	case *CaseExpr:
		if e.Else != nil {
			e.Else = FoldConstants(e.Else)
		}
		for i := range e.Whens {
			e.Whens[i].Condition = FoldConstants(e.Whens[i].Condition)
			e.Whens[i].Result = FoldConstants(e.Whens[i].Result)
		}
		if isConstantExpr(e) {
			val, err := EvalRowExpr(e, nil, nil)
			if err == nil {
				return anyToLiteralExpr(val)
			}
		}
		return e

	default:
		return expr
	}
}

func isConstantExpr(expr Expr) bool {
	if expr == nil {
		return true
	}
	switch e := expr.(type) {
	case *LiteralExpr:
		return true
	case *VariableExpr, *ColumnRefExpr, *ScalarSubqueryExpr, *ExistsExpr, *WithinGroupExpr:
		return false
	case *UnaryExpr:
		return isConstantExpr(e.Expr)
	case *BinaryExpr:
		return isConstantExpr(e.Left) && isConstantExpr(e.Right)
	case *FunctionCallExpr:
		if !IsDeterministicFunction(e.Name) {
			return false
		}
		for _, arg := range e.Args {
			if !isConstantExpr(arg) {
				return false
			}
		}
		return true
	case *IsNullExpr:
		return isConstantExpr(e.Expr)
	case *BetweenExpr:
		return isConstantExpr(e.Expr) && isConstantExpr(e.Low) && isConstantExpr(e.High)
	case *InExpr:
		if e.Query != nil || !isConstantExpr(e.Expr) {
			return false
		}
		for _, item := range e.List {
			if !isConstantExpr(item) {
				return false
			}
		}
		return true
	case *CaseExpr:
		if e.Else != nil && !isConstantExpr(e.Else) {
			return false
		}
		for _, w := range e.Whens {
			if !isConstantExpr(w.Condition) || !isConstantExpr(w.Result) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func isBoolLiteral(expr Expr, target bool) bool {
	if lit, ok := expr.(*LiteralExpr); ok {
		if lit.Val.Type == storage.TypeBool {
			return lit.Val.BVal == target
		}
		if lit.Val.Type == storage.TypeInt {
			if target {
				return lit.Val.IVal != 0
			}
			return lit.Val.IVal == 0
		}
	}
	return false
}

func literalToAny(val storage.Value) any {
	if val.IsNull {
		return nil
	}
	switch val.Type {
	case storage.TypeInt:
		return val.IVal
	case storage.TypeFloat:
		return val.FVal
	case storage.TypeBool:
		return val.BVal
	case storage.TypeDateTime:
		return val.TVal
	case storage.TypeDecimal:
		return val.Decimal()
	default:
		return val.SVal
	}
}

func anyToLiteralExpr(v any) *LiteralExpr {
	if v == nil {
		return &LiteralExpr{Val: storage.NullValue(storage.TypeUnknown)}
	}
	switch val := v.(type) {
	case storage.Value:
		return &LiteralExpr{Val: val}
	case bool:
		return &LiteralExpr{Val: storage.BoolValue(val)}
	case int64:
		return &LiteralExpr{Val: storage.IntValue(val)}
	case int:
		return &LiteralExpr{Val: storage.IntValue(int64(val))}
	case float64:
		if val == math.Trunc(val) {
			return &LiteralExpr{Val: storage.IntValue(int64(val))}
		}
		return &LiteralExpr{Val: storage.FloatValue(val)}
	case float32:
		return &LiteralExpr{Val: storage.FloatValue(float64(val))}
	case storage.Decimal:
		return &LiteralExpr{Val: storage.DecimalValueFromDecimal(val)}
	case string:
		return &LiteralExpr{Val: storage.StringValue(val)}
	case time.Time:
		return &LiteralExpr{Val: storage.DateTimeValue(val)}
	default:
		return &LiteralExpr{Val: storage.StringValue(fmt.Sprintf("%v", val))}
	}
}
