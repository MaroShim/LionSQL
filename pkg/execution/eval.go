package execution

import (
	"fmt"
	"math"
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

// eval3VLAnd computes the SQL 3-valued logic AND of two nullable booleans.
// A nil *bool represents NULL (unknown).
func eval3VLAnd(a, b *bool) *bool {
	if (a != nil && !*a) || (b != nil && !*b) {
		f := false
		return &f
	}
	if a != nil && *a && b != nil && *b {
		t := true
		return &t
	}
	return nil
}

// EvalTupleExpr recursively evaluates an AST expression directly against a Tuple
// with zero heap allocations for standard column lookups, literals, and binary arithmetic.
func EvalTupleExpr(t Tuple, expr proc.Expr) (any, bool) {
	return EvalTupleExprWithSubquery(t, expr, nil)
}

// EvalTupleExprWithSubquery recursively evaluates an AST expression directly against a Tuple
// with optional SubqueryExecutor callback for correlated or scalar subqueries.
func EvalTupleExprWithSubquery(t Tuple, expr proc.Expr, execSubquery proc.SubqueryExecutor) (any, bool) {
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
		// ONLY if the tuple's relation matches the qualifier prefix or has no known table qualifiers.
		if dotIdx := strings.Index(name, "."); dotIdx != -1 {
			prefix := strings.ToLower(cleanIdentifierParts(name[:dotIdx]))
			cmap := t.Index
			if cmap == nil || len(cmap.tableCols) == 0 || cmap.tableCols[prefix] != nil {
				bare := name[dotIdx+1:]
				if val, ok := t.GetOK(bare); ok {
					return val, true
				}
			}
		}
		// Try trimmed identifier
		trimmed := strings.Trim(name, "\"`[]")
		if trimmed != name {
			if val, ok := t.GetOK(trimmed); ok {
				return val, true
			}
			if dotIdx := strings.Index(trimmed, "."); dotIdx != -1 {
				prefix := strings.ToLower(cleanIdentifierParts(trimmed[:dotIdx]))
				cmap := t.Index
				if cmap == nil || len(cmap.tableCols) == 0 || cmap.tableCols[prefix] != nil {
					bare := trimmed[dotIdx+1:]
					if val, ok := t.GetOK(bare); ok {
						return val, true
					}
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
		val, ok := EvalTupleExprWithSubquery(t, e.Expr, execSubquery)
		if !ok {
			return nil, false
		}
		if val == nil {
			return nil, true
		}
		if sv, ok := val.(storage.Value); ok && sv.IsNull {
			return nil, true
		}
		switch e.Op {
		case proc.TokenPlus:
			if fVal, ok := val.(float64); ok {
				return fVal, true
			}
			if fVal, ok := val.(float32); ok {
				return float64(fVal), true
			}
			if iVal, ok := toInt64(val); ok {
				return iVal, true
			}
			if fVal, ok := toFloat64(val); ok {
				return fVal, true
			}
			return val, true
		case proc.TokenMinus:
			if fVal, ok := val.(float64); ok {
				return -fVal, true
			}
			if fVal, ok := val.(float32); ok {
				return -float64(fVal), true
			}
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
		case proc.TokenBitwiseNot:
			if iVal, ok := toInt64(val); ok {
				return ^iVal, true
			}
			return nil, false
		default:
			return nil, false
		}

	case *proc.BinaryExpr:
		res, ok := evalTupleBinaryExprWithSubquery(t, e, execSubquery)
		if ok {
			return res, true
		}
		// Fallback for complex binary expressions (e.g. JSON operators ->, ->>, etc.)
		res, err := proc.EvalRowExpr(e, t.ToEvalMap(), execSubquery)
		if err == nil {
			return res, true
		}
		return nil, false

	case *proc.BetweenExpr:
		val, ok := EvalTupleExprWithSubquery(t, e.Expr, execSubquery)
		if !ok || val == nil {
			val = nil
		}
		if sv, ok := val.(storage.Value); ok && sv.IsNull {
			val = nil
		}
		low, okL := EvalTupleExprWithSubquery(t, e.Low, execSubquery)
		if !okL || low == nil {
			low = nil
		}
		if sv, ok := low.(storage.Value); ok && sv.IsNull {
			low = nil
		}
		high, okH := EvalTupleExprWithSubquery(t, e.High, execSubquery)
		if !okH || high == nil {
			high = nil
		}
		if sv, ok := high.(storage.Value); ok && sv.IsNull {
			high = nil
		}

		var condLow, condHigh *bool
		if val != nil && low != nil {
			res := CompareValues(val, low) >= 0
			condLow = &res
		}
		if val != nil && high != nil {
			res := CompareValues(val, high) <= 0
			condHigh = &res
		}

		betweenRes := eval3VLAnd(condLow, condHigh)
		if betweenRes == nil {
			return nil, true
		}
		if e.Not {
			return !*betweenRes, true
		}
		return *betweenRes, true

	case *proc.InExpr:
		if len(e.List) == 0 && e.Query == nil {
			return e.Not, true
		}
		val, ok := EvalTupleExprWithSubquery(t, e.Expr, execSubquery)
		if !ok || val == nil {
			return nil, true
		}
		if sv, ok := val.(storage.Value); ok && sv.IsNull {
			return nil, true
		}
		found := false
		hasNull := false
		for _, item := range e.List {
			itemVal, okItem := EvalTupleExprWithSubquery(t, item, execSubquery)
			if !okItem || itemVal == nil {
				hasNull = true
				continue
			}
			if sv, ok := itemVal.(storage.Value); ok && sv.IsNull {
				hasNull = true
				continue
			}
			if CompareValues(val, itemVal) == 0 {
				found = true
				break
			}
		}
		if found {
			return !e.Not, true
		}
		if hasNull {
			return nil, true
		}
		return e.Not, true

	case *proc.IsNullExpr:
		val, ok := EvalTupleExprWithSubquery(t, e.Expr, execSubquery)
		if !ok || val == nil {
			return !e.Not, true
		}
		if sVal, ok := val.(storage.Value); ok && sVal.IsNull {
			return !e.Not, true
		}
		return e.Not, true

	case *proc.CastExpr:
		val, ok := EvalTupleExprWithSubquery(t, e.Expr, execSubquery)
		if !ok || val == nil {
			return nil, true
		}
		sVal := toStorageValFromAny(val)
		casted, err := proc.EvalCast(sVal, e.TargetType, e.Precision, e.Scale, e.Style, e.Try)
		if err != nil {
			return nil, false
		}
		return formatStorageVal(casted), true

	case *proc.ExistsExpr:
		val, err := proc.EvalRowExpr(e, t.ToEvalMap(), execSubquery)
		if err != nil || val == nil {
			return false, false
		}
		if b, ok := val.(bool); ok {
			return b, true
		}
		return false, false

	case *proc.ScalarSubqueryExpr:
		val, err := proc.EvalRowExpr(e, t.ToEvalMap(), execSubquery)
		if err != nil {
			return nil, false
		}
		return val, true

	default:
		// Fallback for complex functions or expressions: use ToEvalMap
		res, err := proc.EvalRowExpr(expr, t.ToEvalMap(), execSubquery)
		if err != nil {
			return nil, false
		}
		return res, true
	}
}

// evalTupleBinaryExpr evaluates binary operations (arithmetic, comparison, logical) with zero heap allocs.
func evalTupleBinaryExpr(t Tuple, e *proc.BinaryExpr) (any, bool) {
	return evalTupleBinaryExprWithSubquery(t, e, nil)
}

// evalTupleBinaryExprWithSubquery evaluates binary operations with optional SubqueryExecutor.
func evalTupleBinaryExprWithSubquery(t Tuple, e *proc.BinaryExpr, execSubquery proc.SubqueryExecutor) (any, bool) {
	// Logical AND/OR short-circuiting with SQL 3-Valued Logic (NULL = UNKNOWN)
	if e.Op == proc.TokenAnd {
		leftVal, okL := EvalTupleExprWithSubquery(t, e.Left, execSubquery)
		if !okL {
			return false, false
		}
		leftNull := leftVal == nil
		if sv, ok := leftVal.(storage.Value); ok && sv.IsNull {
			leftNull = true
		}
		if !leftNull {
			if b, ok := leftVal.(bool); ok && !b {
				return false, true
			}
		}
		rightVal, okR := EvalTupleExprWithSubquery(t, e.Right, execSubquery)
		if !okR {
			return false, false
		}
		rightNull := rightVal == nil
		if sv, ok := rightVal.(storage.Value); ok && sv.IsNull {
			rightNull = true
		}
		if !rightNull {
			if b, ok := rightVal.(bool); ok && !b {
				return false, true
			}
		}
		if leftNull || rightNull {
			return nil, true
		}
		bL, ok1 := leftVal.(bool)
		bR, ok2 := rightVal.(bool)
		if ok1 && ok2 {
			return bL && bR, true
		}
		return false, false
	}

	if e.Op == proc.TokenOr {
		leftVal, okL := EvalTupleExprWithSubquery(t, e.Left, execSubquery)
		if !okL {
			return false, false
		}
		leftNull := leftVal == nil
		if sv, ok := leftVal.(storage.Value); ok && sv.IsNull {
			leftNull = true
		}
		if !leftNull {
			if b, ok := leftVal.(bool); ok && b {
				return true, true
			}
		}
		rightVal, okR := EvalTupleExprWithSubquery(t, e.Right, execSubquery)
		if !okR {
			return false, false
		}
		rightNull := rightVal == nil
		if sv, ok := rightVal.(storage.Value); ok && sv.IsNull {
			rightNull = true
		}
		if !rightNull {
			if b, ok := rightVal.(bool); ok && b {
				return true, true
			}
		}
		if leftNull || rightNull {
			return nil, true
		}
		bL, ok1 := leftVal.(bool)
		bR, ok2 := rightVal.(bool)
		if ok1 && ok2 {
			return bL || bR, true
		}
		return false, false
	}

	left, okL := EvalTupleExprWithSubquery(t, e.Left, execSubquery)
	right, okR := EvalTupleExprWithSubquery(t, e.Right, execSubquery)
	if !okL || !okR {
		return nil, false
	}
	if left == nil || right == nil {
		return nil, true
	}
	if sVal, ok := left.(storage.Value); ok && sVal.IsNull {
		return nil, true
	}
	if sVal, ok := right.(storage.Value); ok && sVal.IsNull {
		return nil, true
	}

	// 1. Arithmetic Operators (+, -, *, /, DIV, %) and Bitwise Operators (|, &, ^)
	switch e.Op {
	case proc.TokenBitwiseOr, proc.TokenBitwiseAnd, proc.TokenBitwiseXor:
		lInt, lOk := toInt64(left)
		rInt, rOk := toInt64(right)
		if lOk && rOk {
			switch e.Op {
			case proc.TokenBitwiseOr:
				return lInt | rInt, true
			case proc.TokenBitwiseAnd:
				return lInt & rInt, true
			case proc.TokenBitwiseXor:
				return lInt ^ rInt, true
			}
		}
		return nil, false

	case proc.TokenPlus, proc.TokenMinus, proc.TokenAsterisk, proc.TokenSlash, proc.TokenDiv, proc.TokenPercent:
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
			case proc.TokenSlash, proc.TokenDiv:
				if rInt == 0 || (lInt == math.MinInt64 && rInt == -1) {
					return nil, true
				}
				return lInt / rInt, true
			case proc.TokenPercent:
				if rInt == 0 {
					return nil, true
				}
				if lInt == math.MinInt64 && rInt == -1 {
					return int64(0), true
				}
				return lInt % rInt, true
			}
			return nil, false
		}
		if e.Op == proc.TokenDiv {
			lF, lOkF := toFloat64(left)
			rF, rOkF := toFloat64(right)
			if lOkF && rOkF {
				rI := int64(rF)
				lI := int64(lF)
				if rI == 0 {
					return nil, true
				}
				if lI == math.MinInt64 && rI == -1 {
					return nil, false
				}
				return lI / rI, true
			}
			return nil, false
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

	case proc.TokenConcat:
		if left == nil || right == nil {
			return nil, true
		}
		if sv, ok := left.(storage.Value); ok && sv.IsNull {
			return nil, true
		}
		if sv, ok := right.(storage.Value); ok && sv.IsNull {
			return nil, true
		}
		return fmt.Sprintf("%v%v", left, right), true

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
	return EvalTupleBoolExprWithSubquery(t, expr, nil)
}

// EvalTupleBoolExprWithSubquery evaluates a boolean condition directly against a Tuple with optional SubqueryExecutor.
func EvalTupleBoolExprWithSubquery(t Tuple, expr proc.Expr, execSubquery proc.SubqueryExecutor) bool {
	res, ok := EvalTupleExprWithSubquery(t, expr, execSubquery)
	if !ok {
		return proc.EvalBoolExpr(expr, t.ToEvalMap(), execSubquery)
	}
	if res == nil {
		return false
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
