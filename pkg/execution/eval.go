package execution

import (
	"fmt"
	"math"
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
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
	return EvalTupleExprWithContext(t, expr, execSubquery, nil)
}

// EvalTupleExprWithContext recursively evaluates an AST expression directly against a Tuple
// with optional SubqueryExecutor callback and session variables.
func EvalTupleExprWithContext(t Tuple, expr proc.Expr, execSubquery proc.SubqueryExecutor, sessionVars map[string]any) (any, bool) {
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
		if sessionVars != nil {
			if val, ok := sessionVars[e.Name]; ok {
				return val, true
			}
			lower := strings.ToLower(e.Name)
			if val, ok := sessionVars[lower]; ok {
				return val, true
			}
			bare := strings.TrimPrefix(lower, "@")
			if val, ok := sessionVars[bare]; ok {
				return val, true
			}
			if val, ok := sessionVars["@"+bare]; ok {
				return val, true
			}
		}
		return nil, false

	case *proc.UnaryExpr:
		val, ok := EvalTupleExprWithContext(t, e.Expr, execSubquery, sessionVars)
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
			if d, ok := val.(storage.Decimal); ok {
				return d, true
			}
			if sv, ok := val.(storage.Value); ok && sv.Type == storage.TypeDecimal {
				return sv.Decimal(), true
			}
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
			if d, ok := val.(storage.Decimal); ok {
				return d.Neg(), true
			}
			if sv, ok := val.(storage.Value); ok && sv.Type == storage.TypeDecimal {
				return sv.Decimal().Neg(), true
			}
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
		res, ok := evalTupleBinaryExprWithContext(t, e, execSubquery, sessionVars)
		if ok {
			return res, true
		}
		// Fallback for complex binary expressions (e.g. JSON operators ->, ->>, etc.)
		evalMap := t.ToEvalMap()
		if len(sessionVars) > 0 {
			combined := make(map[string]any, len(evalMap)+len(sessionVars))
			for k, v := range evalMap {
				combined[k] = v
			}
			for k, v := range sessionVars {
				combined[k] = v
			}
			evalMap = combined
		}
		res, err := proc.EvalRowExpr(e, evalMap, execSubquery)
		if err == nil {
			return res, true
		}
		return nil, false

	case *proc.BetweenExpr:
		val, ok := EvalTupleExprWithContext(t, e.Expr, execSubquery, sessionVars)
		if !ok || val == nil {
			val = nil
		}
		if sv, ok := val.(storage.Value); ok && sv.IsNull {
			val = nil
		}
		low, okL := EvalTupleExprWithContext(t, e.Low, execSubquery, sessionVars)
		if !okL || low == nil {
			low = nil
		}
		if sv, ok := low.(storage.Value); ok && sv.IsNull {
			low = nil
		}
		high, okH := EvalTupleExprWithContext(t, e.High, execSubquery, sessionVars)
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
		val, ok := EvalTupleExprWithContext(t, e.Expr, execSubquery, sessionVars)
		if !ok || val == nil {
			return nil, true
		}
		if sv, ok := val.(storage.Value); ok && sv.IsNull {
			return nil, true
		}

		// Fast-Path: Materialized In-Memory Subquery HashSet ($O(1)$)
		if len(e.List) == 1 {
			if colRef, ok := e.List[0].(*proc.ColumnRefExpr); ok && strings.HasPrefix(colRef.Name, "__subq_hashset_") && strings.HasSuffix(colRef.Name, "__") {
				if set, found := proc.LookupSubqueryHashSet(colRef.Name); found {
					normKey, isNull := proc.NormalizeSubqueryKey(val)
					res := set.Evaluate(normKey, isNull, e.Not)
					return res, true
				}
			}
		}

		found := false
		hasNull := false
		if e.Query != nil {
			if execSubquery == nil {
				return nil, false
			}
			subRows, cols, err := execSubquery(e.Query.String())
			if err != nil {
				return nil, false
			}
			for _, r := range subRows {
				var itemVal any
				if len(cols) > 0 {
					itemVal = r[cols[0]]
				} else {
					for _, v := range r {
						itemVal = v
						break
					}
				}
				if itemVal == nil {
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
		} else {
			for _, item := range e.List {
				itemVal, okItem := EvalTupleExprWithContext(t, item, execSubquery, sessionVars)
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
		}
		if found {
			return !e.Not, true
		}
		if hasNull {
			return nil, true
		}
		return e.Not, true

	case *proc.IsNullExpr:
		val, ok := EvalTupleExprWithContext(t, e.Expr, execSubquery, sessionVars)
		if !ok || val == nil {
			return !e.Not, true
		}
		if sVal, ok := val.(storage.Value); ok && sVal.IsNull {
			return !e.Not, true
		}
		return e.Not, true

	case *proc.CastExpr:
		val, ok := EvalTupleExprWithContext(t, e.Expr, execSubquery, sessionVars)
		if !ok || val == nil {
			return nil, true
		}
		sVal := toStorageValFromAny(val)
		casted, err := proc.EvalCast(sVal, e.TargetType, e.Precision, e.Scale, e.Style, e.Try, e.IsFixed)
		if err != nil {
			return nil, false
		}
		return formatStorageVal(casted), true

	case *proc.ExistsExpr:
		evalMap := t.ToEvalMap()
		if len(sessionVars) > 0 {
			combined := make(map[string]any, len(evalMap)+len(sessionVars))
			for k, v := range evalMap {
				combined[k] = v
			}
			for k, v := range sessionVars {
				combined[k] = v
			}
			evalMap = combined
		}
		val, err := proc.EvalRowExpr(e, evalMap, execSubquery)
		if err != nil || val == nil {
			return false, false
		}
		if b, ok := val.(bool); ok {
			return b, true
		}
		return false, false

	case *proc.CollateExpr:
		return EvalTupleExprWithContext(t, e.Expr, execSubquery, sessionVars)

	case *proc.LikeExpr:
		val, okV := EvalTupleExprWithContext(t, e.Expr, execSubquery, sessionVars)
		pat, okP := EvalTupleExprWithContext(t, e.Pattern, execSubquery, sessionVars)
		if !okV || !okP {
			return nil, false
		}
		if val == nil || pat == nil {
			return nil, true
		}
		if sVal, ok := val.(storage.Value); ok && sVal.IsNull {
			return nil, true
		}
		if sVal, ok := pat.(storage.Value); ok && sVal.IsNull {
			return nil, true
		}
		valStr := fmt.Sprintf("%v", val)
		patStr := fmt.Sprintf("%v", pat)
		caseInsensitive := true
		if e.Collation != "" {
			caseInsensitive = !sqlutil.IsCaseSensitiveCollation(e.Collation)
		} else if !e.CaseInsensitive {
			if c, ok := e.Expr.(*proc.CollateExpr); ok && sqlutil.IsCaseSensitiveCollation(c.Collation) {
				caseInsensitive = false
			}
		}
		matched := sqlutil.MatchLike(patStr, valStr, caseInsensitive)
		if e.Not {
			matched = !matched
		}
		return matched, true

	case *proc.ScalarSubqueryExpr:
		evalMap := t.ToEvalMap()
		if len(sessionVars) > 0 {
			combined := make(map[string]any, len(evalMap)+len(sessionVars))
			for k, v := range evalMap {
				combined[k] = v
			}
			for k, v := range sessionVars {
				combined[k] = v
			}
			evalMap = combined
		}
		val, err := proc.EvalRowExpr(e, evalMap, execSubquery)
		if err != nil {
			return nil, false
		}
		return val, true

	default:
		// Fallback for complex functions or expressions: use ToEvalMap
		evalMap := t.ToEvalMap()
		if len(sessionVars) > 0 {
			combined := make(map[string]any, len(evalMap)+len(sessionVars))
			for k, v := range evalMap {
				combined[k] = v
			}
			for k, v := range sessionVars {
				combined[k] = v
			}
			evalMap = combined
		}
		res, err := proc.EvalRowExpr(expr, evalMap, execSubquery)
		if err != nil {
			if _, isCol := expr.(*proc.ColumnRefExpr); !isCol {
				return err, true
			}
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
	return evalTupleBinaryExprWithContext(t, e, execSubquery, nil)
}

// evalTupleBinaryExprWithContext evaluates binary operations with optional SubqueryExecutor and session variables.
func evalTupleBinaryExprWithContext(t Tuple, e *proc.BinaryExpr, execSubquery proc.SubqueryExecutor, sessionVars map[string]any) (any, bool) {
	// Logical AND/OR short-circuiting with SQL 3-Valued Logic (NULL = UNKNOWN)
	if e.Op == proc.TokenAnd {
		leftVal, okL := EvalTupleExprWithContext(t, e.Left, execSubquery, sessionVars)
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
		rightVal, okR := EvalTupleExprWithContext(t, e.Right, execSubquery, sessionVars)
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
		leftVal, okL := EvalTupleExprWithContext(t, e.Left, execSubquery, sessionVars)
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
		rightVal, okR := EvalTupleExprWithContext(t, e.Right, execSubquery, sessionVars)
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

	left, okL := EvalTupleExprWithContext(t, e.Left, execSubquery, sessionVars)
	right, okR := EvalTupleExprWithContext(t, e.Right, execSubquery, sessionVars)
	if errL, isErr := left.(error); isErr {
		return errL, true
	}
	if errR, isErr := right.(error); isErr {
		return errR, true
	}
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

	var isCS bool
	if c, ok := e.Left.(*proc.CollateExpr); ok && sqlutil.IsCaseSensitiveCollation(c.Collation) {
		isCS = true
	} else if c, ok := e.Right.(*proc.CollateExpr); ok && sqlutil.IsCaseSensitiveCollation(c.Collation) {
		isCS = true
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

		// Float arithmetic: if either operand is explicitly a float, evaluate as float
		if isFloatValue(left) || isFloatValue(right) {
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
				case proc.TokenPercent:
					if int64(rF) == 0 {
						return nil, true
					}
					return int64(lF) % int64(rF), true
				}
			}
		}

		// Fast-path: both operands are integer
		lInt, lOk := toInt64(left)
		rInt, rOk := toInt64(right)
		if lOk && rOk {
			if isInt32Val(left) && isInt32Val(right) {
				switch e.Op {
				case proc.TokenPlus:
					res := lInt + rInt
					if res < math.MinInt32 || res > math.MaxInt32 {
						return fmt.Errorf("Msg 8115, Level 16, State 2: Arithmetic overflow error converting expression to data type int."), true
					}
					return res, true
				case proc.TokenMinus:
					res := lInt - rInt
					if res < math.MinInt32 || res > math.MaxInt32 {
						return fmt.Errorf("Msg 8115, Level 16, State 2: Arithmetic overflow error converting expression to data type int."), true
					}
					return res, true
				case proc.TokenAsterisk:
					res := lInt * rInt
					if (lInt != 0 && res/lInt != rInt) || res < math.MinInt32 || res > math.MaxInt32 {
						return fmt.Errorf("Msg 8115, Level 16, State 2: Arithmetic overflow error converting expression to data type int."), true
					}
					return res, true
				}
			}
			switch e.Op {
			case proc.TokenPlus:
				if (rInt > 0 && lInt > math.MaxInt64-rInt) || (rInt < 0 && lInt < math.MinInt64-rInt) {
					return fmt.Errorf("arithmetic overflow error: bigint out of range"), true
				}
				return lInt + rInt, true
			case proc.TokenMinus:
				if (rInt < 0 && lInt > math.MaxInt64+rInt) || (rInt > 0 && lInt < math.MinInt64+rInt) {
					return fmt.Errorf("arithmetic overflow error: bigint out of range"), true
				}
				return lInt - rInt, true
			case proc.TokenAsterisk:
				if lInt != 0 && rInt != 0 {
					if (lInt == -1 && rInt == math.MinInt64) || (rInt == -1 && lInt == math.MinInt64) || lInt*rInt/rInt != lInt {
						return fmt.Errorf("arithmetic overflow error: bigint out of range"), true
					}
				}
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
		return CompareValuesCS(left, right, isCS) == 0, true
	case proc.TokenNeq:
		return CompareValuesCS(left, right, isCS) != 0, true
	case proc.TokenLt:
		return CompareValuesCS(left, right, isCS) < 0, true
	case proc.TokenLte:
		return CompareValuesCS(left, right, isCS) <= 0, true
	case proc.TokenGt:
		return CompareValuesCS(left, right, isCS) > 0, true
	case proc.TokenGte:
		return CompareValuesCS(left, right, isCS) >= 0, true

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
	return EvalTupleBoolExprWithContext(t, expr, execSubquery, nil)
}

// EvalTupleBoolExprWithContext evaluates a boolean condition directly against a Tuple with optional SubqueryExecutor and session variables.
func EvalTupleBoolExprWithContext(t Tuple, expr proc.Expr, execSubquery proc.SubqueryExecutor, sessionVars map[string]any) bool {
	res, ok := EvalTupleExprWithContext(t, expr, execSubquery, sessionVars)
	if !ok {
		evalMap := t.ToEvalMap()
		if len(sessionVars) > 0 {
			combined := make(map[string]any, len(evalMap)+len(sessionVars))
			for k, v := range evalMap {
				combined[k] = v
			}
			for k, v := range sessionVars {
				combined[k] = v
			}
			evalMap = combined
		}
		return proc.EvalBoolExpr(expr, evalMap, execSubquery)
	}
	if res == nil {
		return false
	}
	return proc.IsTruthyValue(res)
}

func isInt32Val(v any) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case int32, int16, int8, uint16, uint8:
		return true
	case int:
		return val >= math.MinInt32 && val <= math.MaxInt32
	case int64:
		return val >= math.MinInt32 && val <= math.MaxInt32
	case storage.Value:
		if val.Type == storage.TypeInt {
			if val.Precision == 8 {
				return false
			}
			return val.Precision == 4 || (val.Precision == 0 && val.IVal >= math.MinInt32 && val.IVal <= math.MaxInt32)
		}
		return false
	default:
		return false
	}
}
