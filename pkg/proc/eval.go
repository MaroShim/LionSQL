package proc

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// SubqueryExecutor is a callback function to evaluate nested subqueries.
type SubqueryExecutor func(sql string) ([]map[string]any, []string, error)

// EvalBoolExpr evaluates an AST Expr in a boolean context against a row map.
func EvalBoolExpr(expr Expr, row map[string]any, execSubquery SubqueryExecutor) bool {
	if expr == nil {
		return true
	}
	val, err := EvalRowExpr(expr, row, execSubquery)
	if err != nil || val == nil {
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
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

// eval3VLAnd computes the SQL 3-valued logic AND of two nullable booleans.
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

// EvalUpdateRowExpr evaluates an assignment expression for UPDATE statements,
// returning ErrDivisionByZero if any division or modulo by zero occurs.
func EvalUpdateRowExpr(expr Expr, row map[string]any, execSubquery SubqueryExecutor) (any, error) {
	if err := checkDivByZero(expr, row, execSubquery); err != nil {
		return nil, err
	}
	return EvalRowExpr(expr, row, execSubquery)
}

func checkDivByZero(expr Expr, row map[string]any, execSubquery SubqueryExecutor) error {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *BinaryExpr:
		if err := checkDivByZero(e.Left, row, execSubquery); err != nil {
			return err
		}
		if err := checkDivByZero(e.Right, row, execSubquery); err != nil {
			return err
		}
		if e.Op == TokenSlash || e.Op == TokenDiv || e.Op == TokenPercent {
			rVal, err := EvalRowExpr(e.Right, row, execSubquery)
			if err == nil && !isNullVal(rVal) {
				if n, ok := toArithmeticFloat(rVal); ok && n == 0 {
					return ErrDivisionByZero
				}
			}
		}
	case *UnaryExpr:
		return checkDivByZero(e.Expr, row, execSubquery)
	}
	return nil
}

// EvalRowExpr recursively evaluates an AST expression against a row map.
func EvalRowExpr(expr Expr, row map[string]any, execSubquery SubqueryExecutor) (any, error) {
	if expr == nil {
		return nil, nil
	}

	switch e := expr.(type) {
	case *LiteralExpr:
		if e.Val.IsNull {
			return nil, nil
		}
		switch e.Val.Type {
		case storage.TypeInt:
			return e.Val.IVal, nil
		case storage.TypeFloat:
			return e.Val.FVal, nil
		case storage.TypeBool:
			return e.Val.BVal, nil
		case storage.TypeDateTime:
			return e.Val.TVal, nil
		default:
			return e.Val.SVal, nil
		}

	case *VariableExpr:
		if row != nil {
			if v, ok := row[e.Name]; ok {
				return v, nil
			}
			lower := strings.ToLower(e.Name)
			if v, ok := row[lower]; ok {
				return v, nil
			}
			bare := strings.TrimPrefix(lower, "@")
			if v, ok := row[bare]; ok {
				return v, nil
			}
			if v, ok := row["@"+bare]; ok {
				return v, nil
			}
		}
		return e.Name, nil

	case *ColumnRefExpr:
		if row == nil {
			return nil, nil
		}
		col := cleanSchemaPrefix(e.Name)
		if v, ok := row[e.Name]; ok {
			return v, nil
		}
		if v, ok := row[col]; ok {
			return v, nil
		}
		// Try bare column without table prefix if present (e.g. u.id -> id)
		if dotIdx := strings.Index(col, "."); dotIdx != -1 {
			bare := col[dotIdx+1:]
			if v, ok := row[bare]; ok {
				return v, nil
			}
		}
		// Try matching case-insensitively
		for k, v := range row {
			if strings.EqualFold(k, e.Name) || strings.EqualFold(k, col) {
				return v, nil
			}
			if dotIdx := strings.Index(k, "."); dotIdx != -1 && strings.EqualFold(k[dotIdx+1:], col) {
				return v, nil
			}
		}
		return nil, nil

	case *UnaryExpr:
		val, err := EvalRowExpr(e.Expr, row, execSubquery)
		if err != nil {
			return nil, err
		}
		if isNullVal(val) {
			return nil, nil
		}
		switch e.Op {
		case TokenPlus:
			if n, ok := toFloat(val); ok {
				if n == math.Trunc(n) {
					return int64(n), nil
				}
				return n, nil
			}
			return val, nil
		case TokenMinus:
			if n, ok := toFloat(val); ok {
				if n == math.Trunc(n) {
					return int64(-n), nil
				}
				return -n, nil
			}
			return nil, fmt.Errorf("cannot negate non-numeric value: %v", val)
		case TokenNot:
			return !isTruthyValue(val), nil
		case TokenBitwiseNot:
			if i, ok := isInteger(val); ok {
				return ^i, nil
			}
			return nil, fmt.Errorf("cannot bitwise NOT non-integer value: %v", val)
		}

	case *BinaryExpr:
		switch e.Op {
		case TokenAnd:
			leftVal, err := EvalRowExpr(e.Left, row, execSubquery)
			if err != nil {
				return nil, err
			}
			leftNull := isNullVal(leftVal)
			if !leftNull && !isTruthyValue(leftVal) {
				return false, nil
			}
			rightVal, err := EvalRowExpr(e.Right, row, execSubquery)
			if err != nil {
				return nil, err
			}
			rightNull := isNullVal(rightVal)
			if !rightNull && !isTruthyValue(rightVal) {
				return false, nil
			}
			if leftNull || rightNull {
				return nil, nil
			}
			return true, nil

		case TokenOr:
			leftVal, err := EvalRowExpr(e.Left, row, execSubquery)
			if err != nil {
				return nil, err
			}
			leftNull := isNullVal(leftVal)
			if !leftNull && isTruthyValue(leftVal) {
				return true, nil
			}
			rightVal, err := EvalRowExpr(e.Right, row, execSubquery)
			if err != nil {
				return nil, err
			}
			rightNull := isNullVal(rightVal)
			if !rightNull && isTruthyValue(rightVal) {
				return true, nil
			}
			if leftNull || rightNull {
				return nil, nil
			}
			return false, nil

		default:
			if fn, ok := e.Right.(*FunctionCallExpr); ok && strings.EqualFold(fn.Name, "ANY") && len(fn.Args) == 1 {
				leftVal, err := EvalRowExpr(e.Left, row, execSubquery)
				if err != nil {
					return nil, err
				}
				rightVal, err := EvalRowExpr(fn.Args[0], row, execSubquery)
				if err != nil {
					return nil, err
				}
				return evalRowAnyComparison(e.Op, leftVal, rightVal)
			}
			leftVal, err := EvalRowExpr(e.Left, row, execSubquery)
			if err != nil {
				return nil, err
			}
			rightVal, err := EvalRowExpr(e.Right, row, execSubquery)
			if err != nil {
				return nil, err
			}
			return evalRowBinaryOp(e.Op, leftVal, rightVal)
		}

	case *InExpr:
		if len(e.List) == 0 && e.Query == nil {
			return e.Not, nil
		}
		val, err := EvalRowExpr(e.Expr, row, execSubquery)
		if err != nil || isNullVal(val) {
			return nil, nil
		}
		strVal := fmt.Sprintf("%v", val)
		found := false
		hasNull := false
		for _, item := range e.List {
			itemVal, err := EvalRowExpr(item, row, execSubquery)
			if err != nil {
				continue
			}
			if isNullVal(itemVal) {
				hasNull = true
				continue
			}
			s2 := fmt.Sprintf("%v", itemVal)
			if strings.EqualFold(strVal, s2) || strings.EqualFold(strings.TrimRight(strVal, " "), strings.TrimRight(s2, " ")) {
				found = true
				break
			}
		}
		if found {
			return !e.Not, nil
		}
		if hasNull {
			return nil, nil
		}
		return e.Not, nil

	case *BetweenExpr:
		val, err := EvalRowExpr(e.Expr, row, execSubquery)
		if err != nil || isNullVal(val) {
			val = nil
		}
		lowVal, err := EvalRowExpr(e.Low, row, execSubquery)
		if err != nil || isNullVal(lowVal) {
			lowVal = nil
		}
		highVal, err := EvalRowExpr(e.High, row, execSubquery)
		if err != nil || isNullVal(highVal) {
			highVal = nil
		}

		var condLow, condHigh *bool
		if val != nil && lowVal != nil {
			vNum, isNum1 := toFloat(val)
			lNum, isNum2 := toFloat(lowVal)
			if isNum1 && isNum2 {
				res := vNum >= lNum
				condLow = &res
			} else {
				res := fmt.Sprintf("%v", val) >= fmt.Sprintf("%v", lowVal)
				condLow = &res
			}
		}
		if val != nil && highVal != nil {
			vNum, isNum1 := toFloat(val)
			hNum, isNum3 := toFloat(highVal)
			if isNum1 && isNum3 {
				res := vNum <= hNum
				condHigh = &res
			} else {
				res := fmt.Sprintf("%v", val) <= fmt.Sprintf("%v", highVal)
				condHigh = &res
			}
		}

		betweenRes := eval3VLAnd(condLow, condHigh)
		if betweenRes == nil {
			return nil, nil
		}
		if e.Not {
			return !*betweenRes, nil
		}
		return *betweenRes, nil

	case *LikeExpr:
		val, err := EvalRowExpr(e.Expr, row, execSubquery)
		if err != nil {
			return nil, err
		}
		if isNullVal(val) {
			return nil, nil
		}
		patVal, err := EvalRowExpr(e.Pattern, row, execSubquery)
		if err != nil {
			return nil, err
		}
		if isNullVal(patVal) {
			return nil, nil
		}
		strVal := fmt.Sprintf("%v", val)
		if sv, ok := val.(storage.Value); ok {
			strVal = fmt.Sprintf("%v", sv.Interface())
		}
		strPat := fmt.Sprintf("%v", patVal)
		if sv, ok := patVal.(storage.Value); ok {
			strPat = fmt.Sprintf("%v", sv.Interface())
		}
		matched := sqlutil.MatchLike(strPat, strVal, true)
		if e.Not {
			return !matched, nil
		}
		return matched, nil

	case *IsNullExpr:
		val, err := EvalRowExpr(e.Expr, row, execSubquery)
		if err != nil {
			return nil, err
		}
		isNull := val == nil
		if e.Not {
			return !isNull, nil
		}
		return isNull, nil

	case *CaseExpr:
		if e.Operand != nil {
			opVal, err := EvalRowExpr(e.Operand, row, execSubquery)
			if err != nil {
				return nil, err
			}
			opStVal := anyToStorageVal(opVal)
			if !opStVal.IsNull {
				for _, w := range e.Whens {
					condVal, err := EvalRowExpr(w.Condition, row, execSubquery)
					if err != nil {
						return nil, err
					}
					condStVal := anyToStorageVal(condVal)
					if !condStVal.IsNull && opStVal.Equal(condStVal) {
						return EvalRowExpr(w.Result, row, execSubquery)
					}
				}
			}
			if e.Else != nil {
				return EvalRowExpr(e.Else, row, execSubquery)
			}
			return nil, nil
		}
		for _, w := range e.Whens {
			if EvalBoolExpr(w.Condition, row, execSubquery) {
				return EvalRowExpr(w.Result, row, execSubquery)
			}
		}
		if e.Else != nil {
			return EvalRowExpr(e.Else, row, execSubquery)
		}
		return nil, nil

	case *CastExpr:
		val, err := EvalRowExpr(e.Expr, row, execSubquery)
		if err != nil {
			if e.Try {
				return nil, nil
			}
			return nil, err
		}
		stVal := anyToStorageVal(val)
		res, err := EvalCast(stVal, e.TargetType, e.Precision, e.Scale, e.Style, e.Try)
		if err != nil {
			return nil, err
		}
		if res.IsNull {
			return nil, nil
		}
		switch res.Type {
		case storage.TypeInt:
			return res.IVal, nil
		case storage.TypeFloat:
			return res.FVal, nil
		case storage.TypeBool:
			return res.BVal, nil
		case storage.TypeDateTime, storage.TypeDate, storage.TypeTime:
			return res.TVal, nil
		default:
			return res.SVal, nil
		}

	case *FunctionCallExpr:
		return evalRowFunction(e, e.Name, e.Args, row, execSubquery)

	case *WithinGroupExpr:
		return nil, nil

	case *ExistsExpr:
		if execSubquery == nil || e.Query == nil {
			return false, nil
		}
		rows, _, err := execSubquery(e.Query.String())
		hasRows := err == nil && len(rows) > 0
		if e.Not {
			return !hasRows, nil
		}
		return hasRows, nil

	case *ScalarSubqueryExpr:
		if e.Query != nil && e.Query.From.Name == "" && len(e.Query.Projections) > 0 && e.Query.Projections[0].Expr != nil {
			return EvalRowExpr(e.Query.Projections[0].Expr, row, execSubquery)
		}
		if execSubquery == nil {
			return nil, nil
		}
		sql := e.String()
		sql = strings.Trim(sql, "()")
		rows, cols, err := execSubquery(sql)
		if err != nil || len(rows) == 0 {
			return nil, nil
		}
		firstRow := rows[0]
		if len(cols) > 0 {
			if v, ok := firstRow[cols[0]]; ok {
				return v, nil
			}
			for k, v := range firstRow {
				if strings.EqualFold(k, cols[0]) {
					return v, nil
				}
			}
		}
		for _, v := range firstRow {
			return v, nil
		}
		return nil, nil

	case *QuantifiedComparisonExpr:
		val, err := EvalRowExpr(e.Expr, row, execSubquery)
		if err != nil || isNullVal(val) {
			return nil, nil
		}
		var targetVals []any
		if e.Query != nil {
			if execSubquery == nil {
				return nil, fmt.Errorf("subquery executor not available for quantified expression")
			}
			sql := e.Query.String()
			subRows, cols, err := execSubquery(sql)
			if err != nil {
				return nil, err
			}
			for _, r := range subRows {
				if r == nil {
					targetVals = append(targetVals, nil)
					continue
				}
				var foundVal any
				if len(cols) > 0 {
					if v, ok := r[cols[0]]; ok {
						foundVal = v
					} else {
						for k, v := range r {
							if strings.EqualFold(k, cols[0]) {
								foundVal = v
								break
							}
						}
					}
				} else {
					for _, v := range r {
						foundVal = v
						break
					}
				}
				targetVals = append(targetVals, foundVal)
			}
		} else {
			for _, item := range e.List {
				iVal, err := EvalRowExpr(item, row, execSubquery)
				if err != nil {
					return nil, err
				}
				targetVals = append(targetVals, iVal)
			}
		}

		if (e.Quantifier == TokenAny || e.Quantifier == TokenSome) && len(targetVals) == 1 {
			if sVal, ok := targetVals[0].(string); ok {
				trimmed := strings.TrimSpace(sVal)
				if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "'{") ||
					strings.HasPrefix(strings.ToUpper(trimmed), "ARRAY[") {
					return evalRowAnyComparison(e.Op, val, sVal)
				}
			}
		}

		if len(targetVals) == 0 {
			if e.Quantifier == TokenAll {
				return true, nil
			}
			return false, nil
		}

		isAll := e.Quantifier == TokenAll
		hasNull := false

		for _, tVal := range targetVals {
			if isNullVal(tVal) {
				hasNull = true
				continue
			}
			cmpRes, err := evalRowBinaryOp(e.Op, val, tVal)
			if err != nil {
				return nil, err
			}
			if isNullVal(cmpRes) {
				hasNull = true
				continue
			}
			boolRes, ok := cmpRes.(bool)
			if !ok {
				continue
			}
			if isAll {
				if !boolRes {
					return false, nil
				}
			} else { // ANY or SOME
				if boolRes {
					return true, nil
				}
			}
		}

		if isAll {
			if hasNull {
				return nil, nil
			}
			return true, nil
		} else {
			if hasNull {
				return nil, nil
			}
			return false, nil
		}
	}

	return nil, fmt.Errorf("unsupported AST expression: %T", expr)
}

func evalRowAnyComparison(op TokenType, leftVal, rightVal any) (any, error) {
	if leftVal == nil || isNullVal(leftVal) || rightVal == nil || isNullVal(rightVal) {
		return nil, nil
	}
	leftStr := fmt.Sprintf("%v", leftVal)
	if sv, ok := leftVal.(storage.Value); ok {
		leftStr = fmt.Sprintf("%v", sv.Interface())
	}
	var elements []string
	switch arr := rightVal.(type) {
	case []string:
		elements = arr
	case []any:
		for _, item := range arr {
			if isNullVal(item) {
				elements = append(elements, "NULL")
			} else if sv, ok := item.(storage.Value); ok {
				elements = append(elements, fmt.Sprintf("%v", sv.Interface()))
			} else {
				elements = append(elements, fmt.Sprintf("%v", item))
			}
		}
	default:
		raw := fmt.Sprintf("%v", rightVal)
		if sv, ok := rightVal.(storage.Value); ok {
			raw = fmt.Sprintf("%v", sv.Interface())
		}
		raw = strings.TrimSpace(raw)
		raw = strings.TrimPrefix(raw, "ARRAY[")
		raw = strings.TrimPrefix(raw, "array[")
		raw = strings.TrimSuffix(raw, "]")
		raw = strings.Trim(raw, "'\"")
		raw = strings.TrimPrefix(raw, "{")
		raw = strings.TrimSuffix(raw, "}")
		for _, item := range strings.Split(raw, ",") {
			cleaned := strings.TrimSpace(strings.Trim(item, "'\""))
			if cleaned != "" {
				elements = append(elements, cleaned)
			}
		}
	}

	hasUnknown := false
	for _, elem := range elements {
		if strings.EqualFold(elem, "NULL") {
			hasUnknown = true
			continue
		}
		cmp, err := evalRowBinaryOp(op, leftStr, elem)
		if err != nil {
			return nil, err
		}
		if isNullVal(cmp) {
			hasUnknown = true
			continue
		}
		if isTruthyValue(cmp) {
			return true, nil
		}
	}
	if hasUnknown {
		return nil, nil
	}
	return false, nil
}

func isNullVal(v any) bool {
	if v == nil {
		return true
	}
	if sv, ok := v.(storage.Value); ok && sv.IsNull {
		return true
	}
	return false
}

func isStringType(v any) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, true
	case storage.Value:
		if s.Type == storage.TypeVarChar || s.Type == storage.TypeUUID || s.Type == storage.TypeJSON {
			return s.SVal, true
		}
	}
	return "", false
}

func evalRowBinaryOp(op TokenType, left, right any) (any, error) {
	switch op {
	case TokenEq:
		if isNullVal(left) || isNullVal(right) {
			return nil, nil
		}
		if s1, ok1 := isStringType(left); ok1 {
			if s2, ok2 := isStringType(right); ok2 {
				return strings.EqualFold(s1, s2) || strings.EqualFold(strings.TrimRight(s1, " "), strings.TrimRight(s2, " ")), nil
			}
		}
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if !isNum1 && isNum2 {
			n1, isNum1 = toArithmeticFloat(left)
		} else if isNum1 && !isNum2 {
			n2, isNum2 = toArithmeticFloat(right)
		}
		if isNum1 && isNum2 {
			return n1 == n2, nil
		}
		return strings.EqualFold(fmt.Sprintf("%v", left), fmt.Sprintf("%v", right)), nil

	case TokenNeq:
		if isNullVal(left) || isNullVal(right) {
			return nil, nil
		}
		if s1, ok1 := isStringType(left); ok1 {
			if s2, ok2 := isStringType(right); ok2 {
				return !strings.EqualFold(s1, s2), nil
			}
		}
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if !isNum1 && isNum2 {
			n1, isNum1 = toArithmeticFloat(left)
		} else if isNum1 && !isNum2 {
			n2, isNum2 = toArithmeticFloat(right)
		}
		if isNum1 && isNum2 {
			return n1 != n2, nil
		}
		return !strings.EqualFold(fmt.Sprintf("%v", left), fmt.Sprintf("%v", right)), nil

	case TokenLt:
		if isNullVal(left) || isNullVal(right) {
			return nil, nil
		}
		if s1, ok1 := isStringType(left); ok1 {
			if s2, ok2 := isStringType(right); ok2 {
				return s1 < s2, nil
			}
		}
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if !isNum1 && isNum2 {
			n1, isNum1 = toArithmeticFloat(left)
		} else if isNum1 && !isNum2 {
			n2, isNum2 = toArithmeticFloat(right)
		}
		if isNum1 && isNum2 {
			return n1 < n2, nil
		}
		return fmt.Sprintf("%v", left) < fmt.Sprintf("%v", right), nil

	case TokenLte:
		if isNullVal(left) || isNullVal(right) {
			return nil, nil
		}
		if s1, ok1 := isStringType(left); ok1 {
			if s2, ok2 := isStringType(right); ok2 {
				return s1 <= s2, nil
			}
		}
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if !isNum1 && isNum2 {
			n1, isNum1 = toArithmeticFloat(left)
		} else if isNum1 && !isNum2 {
			n2, isNum2 = toArithmeticFloat(right)
		}
		if isNum1 && isNum2 {
			return n1 <= n2, nil
		}
		return fmt.Sprintf("%v", left) <= fmt.Sprintf("%v", right), nil

	case TokenGt:
		if isNullVal(left) || isNullVal(right) {
			return nil, nil
		}
		if s1, ok1 := isStringType(left); ok1 {
			if s2, ok2 := isStringType(right); ok2 {
				return s1 > s2, nil
			}
		}
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if !isNum1 && isNum2 {
			n1, isNum1 = toArithmeticFloat(left)
		} else if isNum1 && !isNum2 {
			n2, isNum2 = toArithmeticFloat(right)
		}
		if isNum1 && isNum2 {
			return n1 > n2, nil
		}
		return fmt.Sprintf("%v", left) > fmt.Sprintf("%v", right), nil

	case TokenGte:
		if isNullVal(left) || isNullVal(right) {
			return nil, nil
		}
		if s1, ok1 := isStringType(left); ok1 {
			if s2, ok2 := isStringType(right); ok2 {
				return s1 >= s2, nil
			}
		}
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if !isNum1 && isNum2 {
			n1, isNum1 = toArithmeticFloat(left)
		} else if isNum1 && !isNum2 {
			n2, isNum2 = toArithmeticFloat(right)
		}
		if isNum1 && isNum2 {
			return n1 >= n2, nil
		}
		return fmt.Sprintf("%v", left) >= fmt.Sprintf("%v", right), nil

	case TokenPlus:
		if left == nil || right == nil {
			return nil, nil
		}
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if isNum1 && isNum2 {
			sum := n1 + n2
			if sum == math.Trunc(sum) {
				return int64(sum), nil
			}
			return sum, nil
		}
		return fmt.Sprintf("%v%v", left, right), nil

	case TokenMinus:
		if left == nil || right == nil {
			return nil, nil
		}
		n1, isNum1 := toArithmeticFloat(left)
		n2, isNum2 := toArithmeticFloat(right)
		if isNum1 && isNum2 {
			diff := n1 - n2
			if diff == math.Trunc(diff) {
				return int64(diff), nil
			}
			return diff, nil
		}
		return nil, fmt.Errorf("cannot subtract non-numeric values")

	case TokenAsterisk:
		if left == nil || right == nil {
			return nil, nil
		}
		n1, isNum1 := toArithmeticFloat(left)
		n2, isNum2 := toArithmeticFloat(right)
		if isNum1 && isNum2 {
			prod := n1 * n2
			if prod == math.Trunc(prod) {
				return int64(prod), nil
			}
			return prod, nil
		}
		return nil, fmt.Errorf("cannot multiply non-numeric values")

	case TokenSlash:
		if left == nil || right == nil {
			return nil, nil
		}
		if i1, isInt1 := isInteger(left); isInt1 {
			if i2, isInt2 := isInteger(right); isInt2 {
				if i2 == 0 {
					return nil, nil
				}
				if i1 == math.MinInt64 && i2 == -1 {
					return nil, fmt.Errorf("arithmetic overflow error encountered")
				}
				return i1 / i2, nil
			}
		}
		n1, isNum1 := toArithmeticFloat(left)
		n2, isNum2 := toArithmeticFloat(right)
		if isNum1 && isNum2 {
			if n2 == 0 {
				return nil, nil
			}
			div := n1 / n2
			if div == math.Trunc(div) {
				return int64(div), nil
			}
			return div, nil
		}
		return nil, fmt.Errorf("cannot divide non-numeric values")

	case TokenDiv:
		if left == nil || right == nil {
			return nil, nil
		}
		n1, isNum1 := toArithmeticFloat(left)
		n2, isNum2 := toArithmeticFloat(right)
		if isNum1 && isNum2 {
			i2 := int64(n2)
			if i2 == 0 {
				return nil, nil
			}
			if int64(n1) == math.MinInt64 && i2 == -1 {
				return nil, fmt.Errorf("arithmetic overflow error encountered")
			}
			return int64(n1) / i2, nil
		}
		return nil, fmt.Errorf("cannot integer-divide non-numeric values")

	case TokenPercent:
		if left == nil || right == nil {
			return nil, nil
		}
		n1, isNum1 := toArithmeticFloat(left)
		n2, isNum2 := toArithmeticFloat(right)
		if isNum1 && isNum2 {
			i1 := int64(n1)
			i2 := int64(n2)
			if i2 == 0 {
				return nil, ErrDivisionByZero
			}
			if i1 == math.MinInt64 && i2 == -1 {
				return int64(0), nil
			}
			return i1 % i2, nil
		}
		return nil, fmt.Errorf("cannot modulo non-numeric values")

	case TokenJSONExtract:
		if left == nil {
			return nil, nil
		}
		docStr := fmt.Sprintf("%v", left)
		var res string
		var ok bool
		switch r := right.(type) {
		case int:
			res, ok = storage.ExtractJSON(docStr, r)
		case int64:
			res, ok = storage.ExtractJSON(docStr, int(r))
		case float64:
			res, ok = storage.ExtractJSON(docStr, int(r))
		default:
			res, ok = storage.ExtractJSON(docStr, fmt.Sprintf("%v", right))
		}
		if !ok {
			return nil, nil
		}
		return res, nil

	case TokenJSONExtractText:
		if left == nil {
			return nil, nil
		}
		docStr := fmt.Sprintf("%v", left)
		var res string
		var ok bool
		switch r := right.(type) {
		case int:
			res, ok = storage.ExtractJSONText(docStr, r)
		case int64:
			res, ok = storage.ExtractJSONText(docStr, int(r))
		case float64:
			res, ok = storage.ExtractJSONText(docStr, int(r))
		default:
			res, ok = storage.ExtractJSONText(docStr, fmt.Sprintf("%v", right))
		}
		if !ok {
			return nil, nil
		}
		return res, nil

	case TokenConcat:
		if isNullVal(left) || isNullVal(right) {
			return nil, nil
		}
		return fmt.Sprintf("%v%v", left, right), nil

	case TokenBitwiseOr, TokenBitwiseAnd, TokenBitwiseXor:
		if isNullVal(left) || isNullVal(right) {
			return nil, nil
		}
		i1, ok1 := isInteger(left)
		i2, ok2 := isInteger(right)
		if ok1 && ok2 {
			switch op {
			case TokenBitwiseOr:
				return i1 | i2, nil
			case TokenBitwiseAnd:
				return i1 & i2, nil
			case TokenBitwiseXor:
				return i1 ^ i2, nil
			}
		}
		return nil, fmt.Errorf("bitwise operands must be integers")
	}

	return nil, fmt.Errorf("unsupported binary operator %v", op)
}

func evalRowFunction(fn *FunctionCallExpr, name string, args []Expr, row map[string]any, execSubquery SubqueryExecutor) (any, error) {
	uName := strings.ToUpper(strings.TrimSpace(name))
	isAggFn := uName == "COUNT" || uName == "SUM" || uName == "AVG" || uName == "MIN" || uName == "MAX"
	if row != nil {
		var argStrs []string
		for _, a := range args {
			if a != nil {
				argStrs = append(argStrs, a.String())
			}
		}
		rawArgs := strings.Join(argStrs, ", ")
		var candidates []string
		if fn != nil && fn.Distinct {
			candidates = append(candidates, fmt.Sprintf("%s(DISTINCT %s)", uName, rawArgs))
		}
		candidates = append(candidates,
			fmt.Sprintf("%s(%s)", uName, rawArgs),
			fmt.Sprintf("%s(DISTINCT %s)", uName, rawArgs),
			fmt.Sprintf("%s(ALL %s)", uName, rawArgs),
		)
		for _, cand := range candidates {
			if v, ok := row[cand]; ok && (v != nil || isAggFn) {
				return v, nil
			}
			for k, v := range row {
				if strings.EqualFold(k, cand) && (v != nil || isAggFn) {
					return v, nil
				}
			}
		}
	}

	if isAggFn {
		if uName == "COUNT" {
			if len(args) == 0 {
				return int64(1), nil
			}
			if colRef, ok := args[0].(*ColumnRefExpr); ok && colRef.Name == "*" {
				return int64(1), nil
			}
			v, err := EvalRowExpr(args[0], row, execSubquery)
			if err != nil {
				return nil, err
			}
			if v == nil || isNullVal(v) {
				return int64(0), nil
			}
			return int64(1), nil
		}
		if len(args) > 0 {
			v, err := EvalRowExpr(args[0], row, execSubquery)
			if err != nil {
				return nil, err
			}
			if v == nil || isNullVal(v) {
				return nil, nil
			}
			return v, nil
		}
		return nil, nil
	}

	if uName == "ISNULL" || uName == "COALESCE" {
		for _, a := range args {
			v, err := EvalRowExpr(a, row, execSubquery)
			if err != nil {
				return nil, err
			}
			if v != nil {
				return v, nil
			}
		}
		return nil, nil
	}

	if (uName == "DB_NAME" || uName == "CURRENT_DATABASE") && len(args) == 0 && row != nil {
		for _, k := range []string{"db_name", "current_database", "database_name", "database"} {
			if v, ok := row[k]; ok && v != nil {
				if s, ok := v.(string); ok && s != "" {
					return s, nil
				}
			}
		}
	}

	stArgs := make([]storage.Value, len(args))
	for i, a := range args {
		v, err := EvalRowExpr(a, row, execSubquery)
		if err != nil {
			return nil, err
		}
		if v == nil && a != nil {
			if colRef, ok := a.(*ColumnRefExpr); ok {
				inRow := false
				if row != nil {
					col := cleanSchemaPrefix(colRef.Name)
					if _, ok := row[colRef.Name]; ok {
						inRow = true
					} else if _, ok := row[col]; ok {
						inRow = true
					} else {
						for k := range row {
							if strings.EqualFold(k, colRef.Name) || strings.EqualFold(k, col) {
								inRow = true
								break
							}
						}
					}
				}
				if !inRow {
					v = colRef.Name
				}
			}
		}
		stArgs[i] = anyToStorageVal(v)
	}

	if !IsBuiltinFunction(name) && !isAggFn && uName != "ISNULL" && uName != "COALESCE" && uName != "NULLIF" {
		return nil, fmt.Errorf("unknown scalar function %s", name)
	}

	res := EvalBuiltin(name, stArgs)
	if res.IsNull {
		return nil, nil
	}
	switch res.Type {
	case storage.TypeInt:
		return res.IVal, nil
	case storage.TypeFloat:
		return res.FVal, nil
	case storage.TypeBool:
		return res.BVal, nil
	case storage.TypeDateTime:
		return res.TVal, nil
	default:
		return res.SVal, nil
	}
}

func anyToStorageVal(v any) storage.Value {
	if v == nil {
		return storage.NullValue(storage.TypeUnknown)
	}
	switch val := v.(type) {
	case storage.Value:
		return val
	case int64:
		return storage.IntValue(val)
	case int:
		return storage.IntValue(int64(val))
	case float64:
		return storage.FloatValue(val)
	case float32:
		return storage.FloatValue(float64(val))
	case bool:
		return storage.BoolValue(val)
	case string:
		return storage.StringValue(val)
	case time.Time:
		return storage.DateTimeValue(val)
	default:
		return storage.StringValue(fmt.Sprintf("%v", val))
	}
}

func isInteger(v any) (int64, bool) {
	if v == nil {
		return 0, false
	}
	switch val := v.(type) {
	case int:
		return int64(val), true
	case int64:
		return val, true
	case int32:
		return int64(val), true
	case int16:
		return int64(val), true
	case int8:
		return int64(val), true
	case uint:
		return int64(val), true
	case uint64:
		return int64(val), true
	case uint32:
		return int64(val), true
	case uint16:
		return int64(val), true
	case uint8:
		return int64(val), true
	case storage.Value:
		if val.Type == storage.TypeInt {
			return val.IVal, true
		}
	case string:
		if iv, err := strconv.ParseInt(strings.TrimSpace(val), 0, 64); err == nil {
			return iv, true
		}
	}
	return 0, false
}

func toArithmeticFloat(v any) (float64, bool) {
	if f, ok := toFloat(v); ok {
		return f, true
	}
	if s, ok := v.(string); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	if v == nil {
		return 0, false
	}
	switch val := v.(type) {
	case bool:
		if val {
			return 1.0, true
		}
		return 0.0, true
	case storage.Value:
		if val.Type == storage.TypeBool {
			if val.BVal {
				return 1.0, true
			}
			return 0.0, true
		} else if val.Type == storage.TypeInt {
			return float64(val.IVal), true
		} else if val.Type == storage.TypeFloat {
			return val.FVal, true
		} else if val.Type == storage.TypeDecimal {
			return val.Decimal().Float64(), true
		}
	case storage.Decimal:
		return val.Float64(), true
	case int64:
		return float64(val), true
	case int:
		return float64(val), true
	case float64:
		return val, true
	case float32:
		return float64(val), true
	}
	return 0, false
}

func isTruthyValue(v any) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case int64:
		return val != 0
	case int:
		return val != 0
	case float64:
		return val != 0.0
	case string:
		return len(val) > 0 && !strings.EqualFold(val, "false") && val != "0"
	default:
		return true
	}
}

// EvalCast performs type conversion matching SQL Server CAST and CONVERT semantics.
func EvalCast(v storage.Value, targetType storage.DataType, prec, scale, style int, try bool) (storage.Value, error) {
	if v.IsNull {
		return storage.NullValue(targetType), nil
	}

	fail := func(err error) (storage.Value, error) {
		if try {
			return storage.NullValue(targetType), nil
		}
		return storage.Value{}, err
	}

	switch targetType {
	case storage.TypeInt:
		switch v.Type {
		case storage.TypeInt:
			return v, nil
		case storage.TypeFloat:
			return storage.IntValue(int64(v.FVal)), nil
		case storage.TypeBool:
			if v.BVal {
				return storage.IntValue(1), nil
			}
			return storage.IntValue(0), nil
		case storage.TypeVarChar:
			s := strings.TrimSpace(v.SVal)
			i, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				if f, err2 := strconv.ParseFloat(s, 64); err2 == nil {
					return storage.IntValue(int64(f)), nil
				}
				return fail(fmt.Errorf("conversion failed when converting %q to data type int", v.SVal))
			}
			return storage.IntValue(i), nil
		case storage.TypeDateTime, storage.TypeDate, storage.TypeTime:
			return storage.IntValue(v.TVal.Unix()), nil
		case storage.TypeDecimal:
			f := v.DVal.Float64()
			return storage.IntValue(int64(f)), nil
		default:
			return fail(fmt.Errorf("unsupported conversion from %v to int", v.Type))
		}

	case storage.TypeFloat:
		switch v.Type {
		case storage.TypeFloat:
			return v, nil
		case storage.TypeInt:
			return storage.FloatValue(float64(v.IVal)), nil
		case storage.TypeBool:
			if v.BVal {
				return storage.FloatValue(1.0), nil
			}
			return storage.FloatValue(0.0), nil
		case storage.TypeVarChar:
			s := strings.TrimSpace(v.SVal)
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return fail(fmt.Errorf("conversion failed when converting %q to data type float", v.SVal))
			}
			return storage.FloatValue(f), nil
		case storage.TypeDecimal:
			f := v.DVal.Float64()
			return storage.FloatValue(f), nil
		default:
			return fail(fmt.Errorf("unsupported conversion from %v to float", v.Type))
		}

	case storage.TypeVarChar:
		var str string
		switch v.Type {
		case storage.TypeDateTime, storage.TypeDate, storage.TypeTime:
			str = formatDateTimeByStyle(v.TVal, style)
		default:
			if style > 0 {
				if t, err := parseDateTimeByStyle(v.String(), 120); err == nil {
					str = formatDateTimeByStyle(t, style)
				} else if dt, err := storage.DateTimeValueFromAny(v); err == nil && !dt.IsNull {
					str = formatDateTimeByStyle(dt.TVal, style)
				} else {
					str = v.String()
				}
			} else {
				str = v.String()
			}
		}
		if prec > 0 && len(str) > prec {
			str = str[:prec]
		}
		return storage.StringValue(str), nil

	case storage.TypeBool:
		switch v.Type {
		case storage.TypeBool:
			return v, nil
		case storage.TypeInt:
			return storage.BoolValue(v.IVal != 0), nil
		case storage.TypeFloat:
			return storage.BoolValue(v.FVal != 0.0), nil
		case storage.TypeDecimal:
			return storage.BoolValue(!v.Decimal().IsZero()), nil
		case storage.TypeVarChar:
			s := strings.Trim(strings.TrimSpace(strings.ToUpper(v.SVal)), "'\"")
			if s == "1" || s == "TRUE" || s == "T" {
				return storage.BoolValue(true), nil
			} else if s == "0" || s == "FALSE" || s == "F" {
				return storage.BoolValue(false), nil
			}
			return fail(fmt.Errorf("conversion failed when converting %q to data type bit/bool", v.SVal))
		default:
			return fail(fmt.Errorf("unsupported conversion from %v to bit/bool", v.Type))
		}

	case storage.TypeDateTime:
		switch v.Type {
		case storage.TypeDateTime:
			return v, nil
		case storage.TypeDate:
			return storage.DateTimeValue(v.TVal), nil
		case storage.TypeTime:
			return storage.DateTimeValue(v.TVal), nil
		case storage.TypeVarChar:
			t, err := parseDateTimeByStyle(v.SVal, style)
			if err != nil {
				dtVal, err2 := storage.DateTimeValueFromAny(v)
				if err2 != nil {
					return fail(fmt.Errorf("conversion failed when converting date and/or time from character string: %w", err))
				}
				return dtVal, nil
			}
			return storage.DateTimeValue(t), nil
		default:
			return fail(fmt.Errorf("unsupported conversion from %v to datetime", v.Type))
		}

	case storage.TypeDate:
		switch v.Type {
		case storage.TypeDate:
			return v, nil
		case storage.TypeDateTime:
			return storage.DateValue(v.TVal), nil
		case storage.TypeVarChar:
			t, err := parseDateTimeByStyle(v.SVal, style)
			if err != nil {
				t2, err2 := storage.ParseDate(v.SVal)
				if err2 != nil {
					return fail(fmt.Errorf("conversion failed when converting %q to data type date", v.SVal))
				}
				return storage.DateValue(t2), nil
			}
			return storage.DateValue(t), nil
		default:
			return fail(fmt.Errorf("unsupported conversion from %v to date", v.Type))
		}

	case storage.TypeTime:
		switch v.Type {
		case storage.TypeTime:
			return v, nil
		case storage.TypeDateTime:
			return storage.TimeValue(v.TVal), nil
		case storage.TypeVarChar:
			t, _, err := storage.ParseTime(v.SVal)
			if err != nil {
				return fail(fmt.Errorf("conversion failed when converting %q to data type time", v.SVal))
			}
			return storage.TimeValue(t), nil
		default:
			return fail(fmt.Errorf("unsupported conversion from %v to time", v.Type))
		}

	case storage.TypeDecimal:
		decVal := storage.DecimalValue(v.String())
		if scale > 0 {
			decVal = storage.DecimalValueFromDecimal(decVal.DVal.Rescale(int32(scale)))
		}
		return decVal, nil

	case storage.TypeUUID:
		norm, err := storage.ParseUUID(v.String())
		if err != nil {
			return fail(fmt.Errorf("conversion failed when converting %q to uniqueidentifier: %w", v.String(), err))
		}
		return storage.UUIDValue(norm), nil

	case storage.TypeBinary:
		return storage.BinaryValueFromString(v.String()), nil

	default:
		return v, nil
	}
}

func formatDateTimeByStyle(t time.Time, style int) string {
	switch style {
	case 1:
		return t.Format("01/02/06")
	case 101:
		return t.Format("01/02/2006")
	case 2:
		return t.Format("06.01.02")
	case 102:
		return t.Format("2006.01.02")
	case 3:
		return t.Format("02/01/06")
	case 103:
		return t.Format("02/01/2006")
	case 12:
		return t.Format("060102")
	case 112:
		return t.Format("20060102")
	case 23:
		return t.Format("2006-01-02")
	case 120, 20:
		return t.Format("2006-01-02 15:04:05")
	case 121, 21:
		return t.Format("2006-01-02 15:04:05.000")
	case 126:
		return t.Format(time.RFC3339)
	case 8, 108:
		return t.Format("15:04:05")
	case 24:
		return t.Format("15:04:05")
	default:
		return t.Format("2006-01-02 15:04:05")
	}
}

func parseDateTimeByStyle(s string, style int) (time.Time, error) {
	s = strings.Trim(strings.TrimSpace(s), "'\"")
	var layout string
	switch style {
	case 1:
		layout = "01/02/06"
	case 101:
		layout = "01/02/2006"
	case 2:
		layout = "06.01.02"
	case 102:
		layout = "2006.01.02"
	case 3:
		layout = "02/01/06"
	case 103:
		layout = "02/01/2006"
	case 12:
		layout = "060102"
	case 112:
		layout = "20060102"
	case 23:
		layout = "2006-01-02"
	case 120, 20:
		layout = "2006-01-02 15:04:05"
	case 121, 21:
		layout = "2006-01-02 15:04:05.000"
	case 126:
		layout = time.RFC3339
	case 8, 108, 24:
		layout = "15:04:05"
	default:
		return time.Time{}, fmt.Errorf("unknown date style: %d", style)
	}
	return time.Parse(layout, s)
}

