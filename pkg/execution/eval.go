package execution

import (
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

// EvalTupleExpr recursively evaluates an AST expression directly against a Tuple
// with zero heap allocations for standard column lookups, literals, and binary arithmetic.
func EvalTupleExpr(t Tuple, expr proc.Expr) (any, bool) {
	if expr == nil {
		return nil, false
	}

	switch e := expr.(type) {
	case *proc.ColumnRefExpr:
		name := e.Name
		if val, ok := t.GetOK(name); ok {
			return val, true
		}
		// Try bare column name if qualified (e.g. "lo.lo_extendedprice" -> "lo_extendedprice")
		if dotIdx := strings.Index(name, "."); dotIdx != -1 {
			bare := name[dotIdx+1:]
			if val, ok := t.GetOK(bare); ok {
				return val, true
			}
		}
		// Try trimmed identifier
		trimmed := strings.Trim(name, "\"`[]")
		if trimmed != name {
			if val, ok := t.GetOK(trimmed); ok {
				return val, true
			}
			if dotIdx := strings.Index(trimmed, "."); dotIdx != -1 {
				bare := trimmed[dotIdx+1:]
				if val, ok := t.GetOK(bare); ok {
					return val, true
				}
			}
		}
		return nil, false

	case *proc.LiteralExpr:
		if e.Val.IsNull {
			return nil, true
		}
		switch e.Val.Type {
		case storage.TypeInt:
			return e.Val.IVal, true
		case storage.TypeFloat:
			return e.Val.FVal, true
		case storage.TypeBool:
			return e.Val.BVal, true
		case storage.TypeDateTime:
			return e.Val.TVal, true
		case storage.TypeDecimal:
			return e.Val.DVal, true
		default:
			return e.Val.SVal, true
		}

	case *proc.VariableExpr:
		if val, ok := t.GetOK(e.Name); ok {
			return val, true
		}
		return nil, false

	case *proc.UnaryExpr:
		val, ok := EvalTupleExpr(t, e.Expr)
		if !ok || val == nil {
			return nil, false
		}
		switch e.Op {
		case proc.TokenMinus:
			if iVal, ok := toInt64(val); ok {
				return -iVal, true
			}
			if fVal, ok := toFloat64(val); ok {
				return -fVal, true
			}
			return nil, false
		case proc.TokenNot:
			if bVal, ok := val.(bool); ok {
				return !bVal, true
			}
			return nil, false
		default:
			return nil, false
		}

	case *proc.BinaryExpr:
		return evalTupleBinaryExpr(t, e)

	case *proc.BetweenExpr:
		val, ok := EvalTupleExpr(t, e.Expr)
		if !ok || val == nil {
			return e.Not, true
		}
		low, okL := EvalTupleExpr(t, e.Low)
		high, okH := EvalTupleExpr(t, e.High)
		if !okL || !okH || low == nil || high == nil {
			return e.Not, true
		}
		cmpLow := compareAny(val, low)
		cmpHigh := compareAny(val, high)
		inRange := cmpLow >= 0 && cmpHigh <= 0
		if e.Not {
			return !inRange, true
		}
		return inRange, true

	case *proc.InExpr:
		val, ok := EvalTupleExpr(t, e.Expr)
		if !ok || val == nil {
			return e.Not, true
		}
		found := false
		for _, item := range e.List {
			itemVal, okItem := EvalTupleExpr(t, item)
			if okItem && compareAny(val, itemVal) == 0 {
				found = true
				break
			}
		}
		if e.Not {
			return !found, true
		}
		return found, true

	case *proc.IsNullExpr:
		val, ok := EvalTupleExpr(t, e.Expr)
		if !ok || val == nil {
			return !e.Not, true
		}
		if sVal, ok := val.(storage.Value); ok && sVal.IsNull {
			return !e.Not, true
		}
		return e.Not, true

	case *proc.CastExpr:
		val, ok := EvalTupleExpr(t, e.Expr)
		if !ok || val == nil {
			return nil, true
		}
		sVal := toStorageValFromAny(val)
		casted, err := proc.EvalCast(sVal, e.TargetType, e.Precision, e.Scale, e.Style, e.Try)
		if err != nil {
			return nil, false
		}
		return formatStorageVal(casted), true

	default:
		// Fallback for complex functions or expressions: use ToEvalMap
		res, err := proc.EvalRowExpr(expr, t.ToEvalMap(), nil)
		if err != nil {
			return nil, false
		}
		return res, true
	}
}

// evalTupleBinaryExpr evaluates binary operations (arithmetic, comparison, logical) with zero heap allocs.
func evalTupleBinaryExpr(t Tuple, e *proc.BinaryExpr) (any, bool) {
	// Logical AND/OR short-circuiting
	if e.Op == proc.TokenAnd {
		leftVal, okL := EvalTupleExpr(t, e.Left)
		if okL && leftVal != nil {
			if b, ok := leftVal.(bool); ok && !b {
				return false, true
			}
		}
		rightVal, okR := EvalTupleExpr(t, e.Right)
		if okL && okR {
			bL, ok1 := leftVal.(bool)
			bR, ok2 := rightVal.(bool)
			if ok1 && ok2 {
				return bL && bR, true
			}
		}
		return false, false
	}

	if e.Op == proc.TokenOr {
		leftVal, okL := EvalTupleExpr(t, e.Left)
		if okL && leftVal != nil {
			if b, ok := leftVal.(bool); ok && b {
				return true, true
			}
		}
		rightVal, okR := EvalTupleExpr(t, e.Right)
		if okL && okR {
			bL, ok1 := leftVal.(bool)
			bR, ok2 := rightVal.(bool)
			if ok1 && ok2 {
				return bL || bR, true
			}
		}
		return false, false
	}

	left, okL := EvalTupleExpr(t, e.Left)
	right, okR := EvalTupleExpr(t, e.Right)
	if !okL || !okR || left == nil || right == nil {
		return nil, false
	}

	// 1. Arithmetic Operators (+, -, *, /, %)
	switch e.Op {
	case proc.TokenPlus, proc.TokenMinus, proc.TokenAsterisk, proc.TokenSlash, proc.TokenPercent:
		// Fast-path: both operands are integer
		lInt, lOk := toInt64(left)
		rInt, rOk := toInt64(right)
		if lOk && rOk {
			switch e.Op {
			case proc.TokenPlus:
				return lInt + rInt, true
			case proc.TokenMinus:
				return lInt - rInt, true
			case proc.TokenAsterisk:
				return lInt * rInt, true
			case proc.TokenSlash:
				if rInt == 0 {
					return nil, false
				}
				return lInt / rInt, true
			case proc.TokenPercent:
				if rInt == 0 {
					return nil, false
				}
				return lInt % rInt, true
			}
		}

		// Decimal support
		if isDecValue(left) || isDecValue(right) {
			lDec, _ := tryDecimal(left)
			rDec, _ := tryDecimal(right)
			switch e.Op {
			case proc.TokenPlus:
				return lDec.Add(rDec), true
			case proc.TokenMinus:
				return lDec.Sub(rDec), true
			case proc.TokenAsterisk:
				return lDec.Mul(rDec), true
			case proc.TokenSlash:
				divRes, err := lDec.Div(rDec, 4)
				if err == nil {
					return divRes, true
				}
				return nil, false
			}
		}

		// Float arithmetic
		lF, lOkF := toFloat64(left)
		rF, rOkF := toFloat64(right)
		if lOkF && rOkF {
			switch e.Op {
			case proc.TokenPlus:
				return lF + rF, true
			case proc.TokenMinus:
				return lF - rF, true
			case proc.TokenAsterisk:
				return lF * rF, true
			case proc.TokenSlash:
				if rF == 0 {
					return nil, false
				}
				return lF / rF, true
			}
		}
		return nil, false

	// 2. Comparison Operators (=, ==, !=, <>, <, <=, >, >=)
	case proc.TokenEq:
		return compareAny(left, right) == 0, true
	case proc.TokenNeq:
		return compareAny(left, right) != 0, true
	case proc.TokenLt:
		return compareAny(left, right) < 0, true
	case proc.TokenLte:
		return compareAny(left, right) <= 0, true
	case proc.TokenGt:
		return compareAny(left, right) > 0, true
	case proc.TokenGte:
		return compareAny(left, right) >= 0, true
	}

	return nil, false
}

// EvalTupleBoolExpr evaluates a boolean condition directly against a Tuple with zero allocations.
func EvalTupleBoolExpr(t Tuple, expr proc.Expr) bool {
	res, ok := EvalTupleExpr(t, expr)
	if !ok || res == nil {
		return false
	}
	if b, ok := res.(bool); ok {
		return b
	}
	return false
}
