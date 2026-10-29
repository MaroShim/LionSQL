package execution

import (
	"fmt"
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
		res, ok := evalTupleBinaryExpr(t, e)
		if ok {
			return res, true
		}
		// Fallback for complex binary expressions (e.g. JSON operators ->, ->>, etc.)
		res, err := proc.EvalRowExpr(e, t.ToEvalMap(), nil)
		if err == nil {
			return res, true
		}
		return nil, false

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
		cmpLow := CompareValues(val, low)
		cmpHigh := CompareValues(val, high)
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
			if okItem && CompareValues(val, itemVal) == 0 {
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
		return CompareValues(left, right) == 0, true
	case proc.TokenNeq:
		return CompareValues(left, right) != 0, true
	case proc.TokenLt:
		return CompareValues(left, right) < 0, true
	case proc.TokenLte:
		return CompareValues(left, right) <= 0, true
	case proc.TokenGt:
		return CompareValues(left, right) > 0, true
	case proc.TokenGte:
		return CompareValues(left, right) >= 0, true

	// 3. JSON Extract Operators (->, ->>)
	case proc.TokenJSONExtract:
		docStr := fmt.Sprintf("%v", left)
		switch r := right.(type) {
		case int:
			res, ok := storage.ExtractJSON(docStr, r)
			return res, ok
		case int64:
			res, ok := storage.ExtractJSON(docStr, int(r))
			return res, ok
		case float64:
			res, ok := storage.ExtractJSON(docStr, int(r))
			return res, ok
		default:
			res, ok := storage.ExtractJSON(docStr, fmt.Sprintf("%v", right))
			return res, ok
		}

	case proc.TokenJSONExtractText:
		docStr := fmt.Sprintf("%v", left)
		switch r := right.(type) {
		case int:
			res, ok := storage.ExtractJSONText(docStr, r)
			return res, ok
		case int64:
			res, ok := storage.ExtractJSONText(docStr, int(r))
			return res, ok
		case float64:
			res, ok := storage.ExtractJSONText(docStr, int(r))
			return res, ok
		default:
			res, ok := storage.ExtractJSONText(docStr, fmt.Sprintf("%v", right))
			return res, ok
		}
	}

	return nil, false
}

// EvalTupleBoolExpr evaluates a boolean condition directly against a Tuple with zero allocations.
func EvalTupleBoolExpr(t Tuple, expr proc.Expr) bool {
	res, ok := EvalTupleExpr(t, expr)
	if !ok || res == nil {
		return proc.EvalBoolExpr(expr, t.ToEvalMap(), nil)
	}
	if b, ok := res.(bool); ok {
		return b
	}
	switch v := res.(type) {
	case int64:
		return v != 0
	case int:
		return v != 0
	case float64:
		return v != 0.0
	case string:
		return len(v) > 0 && !strings.EqualFold(v, "false") && v != "0"
	default:
		return true
	}
}
