package proc

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

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
		switch e.Op {
		case TokenMinus:
			if n, ok := toFloat(val); ok {
				return -n, nil
			}
			return nil, fmt.Errorf("cannot negate non-numeric value: %v", val)
		case TokenNot:
			return !isTruthyValue(val), nil
		}

	case *BinaryExpr:
		switch e.Op {
		case TokenAnd:
			leftBool := EvalBoolExpr(e.Left, row, execSubquery)
			if !leftBool {
				return false, nil
			}
			rightBool := EvalBoolExpr(e.Right, row, execSubquery)
			return rightBool, nil

		case TokenOr:
			leftBool := EvalBoolExpr(e.Left, row, execSubquery)
			if leftBool {
				return true, nil
			}
			rightBool := EvalBoolExpr(e.Right, row, execSubquery)
			return rightBool, nil

		default:
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
		val, err := EvalRowExpr(e.Expr, row, execSubquery)
		if err != nil || val == nil {
			if e.Not {
				return true, nil
			}
			return false, nil
		}
		strVal := fmt.Sprintf("%v", val)
		found := false
		for _, item := range e.List {
			itemVal, err := EvalRowExpr(item, row, execSubquery)
			if err == nil && itemVal != nil {
				if strings.EqualFold(strVal, fmt.Sprintf("%v", itemVal)) {
					found = true
					break
				}
			}
		}
		if e.Not {
			return !found, nil
		}
		return found, nil

	case *BetweenExpr:
		val, err := EvalRowExpr(e.Expr, row, execSubquery)
		if err != nil || val == nil {
			return false, nil
		}
		lowVal, err := EvalRowExpr(e.Low, row, execSubquery)
		if err != nil || lowVal == nil {
			return false, nil
		}
		highVal, err := EvalRowExpr(e.High, row, execSubquery)
		if err != nil || highVal == nil {
			return false, nil
		}

		vNum, isNum1 := toFloat(val)
		lNum, isNum2 := toFloat(lowVal)
		hNum, isNum3 := toFloat(highVal)
		var inRange bool
		if isNum1 && isNum2 && isNum3 {
			inRange = vNum >= lNum && vNum <= hNum
		} else {
			vStr := fmt.Sprintf("%v", val)
			lStr := fmt.Sprintf("%v", lowVal)
			hStr := fmt.Sprintf("%v", highVal)
			inRange = vStr >= lStr && vStr <= hStr
		}
		if e.Not {
			return !inRange, nil
		}
		return inRange, nil

	case *LikeExpr:
		val, err := EvalRowExpr(e.Expr, row, execSubquery)
		if err != nil || val == nil {
			return false, nil
		}
		patVal, err := EvalRowExpr(e.Pattern, row, execSubquery)
		if err != nil || patVal == nil {
			return false, nil
		}
		pat := fmt.Sprintf("%v", patVal)
		pat = strings.ReplaceAll(regexp.QuoteMeta(pat), "%", ".*")
		pat = strings.ReplaceAll(pat, "_", ".")
		matched, _ := regexp.MatchString("(?i)^"+pat+"$", fmt.Sprintf("%v", val))
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
		return evalRowFunction(e.Name, e.Args, row, execSubquery)

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
	}

	return nil, fmt.Errorf("unsupported AST expression: %T", expr)
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
		if left == nil && right == nil {
			return true, nil
		}
		if left == nil || right == nil {
			return false, nil
		}
		if s1, ok1 := isStringType(left); ok1 {
			if s2, ok2 := isStringType(right); ok2 {
				return strings.EqualFold(s1, s2), nil
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
		if left == nil || right == nil {
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
		if left == nil || right == nil {
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
		if left == nil || right == nil {
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
		if left == nil || right == nil {
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
		if left == nil || right == nil {
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
		n1, isNum1 := toArithmeticFloat(left)
		n2, isNum2 := toArithmeticFloat(right)
		if isNum1 && isNum2 {
			if n2 == 0 {
				return nil, ErrDivisionByZero
			}
			div := n1 / n2
			if div == math.Trunc(div) {
				return int64(div), nil
			}
			return div, nil
		}
		return nil, fmt.Errorf("cannot divide non-numeric values")

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
	}

	return nil, fmt.Errorf("unsupported binary operator %v", op)
}

func evalRowFunction(name string, args []Expr, row map[string]any, execSubquery SubqueryExecutor) (any, error) {
	if row != nil {
		var argStrs []string
		for _, a := range args {
			if a != nil {
				argStrs = append(argStrs, a.String())
			}
		}
		rawCall := fmt.Sprintf("%s(%s)", name, strings.Join(argStrs, ", "))
		if v, ok := row[rawCall]; ok && v != nil {
			return v, nil
		}
		for k, v := range row {
			if strings.EqualFold(k, rawCall) && v != nil {
				return v, nil
			}
		}
	}

	uName := strings.ToUpper(strings.TrimSpace(name))
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

