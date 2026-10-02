package proc

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/storage"
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
		for _, w := range e.Whens {
			if EvalBoolExpr(w.Condition, row, execSubquery) {
				return EvalRowExpr(w.Result, row, execSubquery)
			}
		}
		if e.Else != nil {
			return EvalRowExpr(e.Else, row, execSubquery)
		}
		return nil, nil

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
		}
		for _, v := range firstRow {
			return v, nil
		}
		return nil, nil
	}

	return nil, fmt.Errorf("unsupported AST expression: %T", expr)
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
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if isNum1 && isNum2 {
			return n1 == n2, nil
		}
		return strings.EqualFold(fmt.Sprintf("%v", left), fmt.Sprintf("%v", right)), nil

	case TokenNeq:
		if left == nil && right == nil {
			return false, nil
		}
		if left == nil || right == nil {
			return true, nil
		}
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if isNum1 && isNum2 {
			return n1 != n2, nil
		}
		return !strings.EqualFold(fmt.Sprintf("%v", left), fmt.Sprintf("%v", right)), nil

	case TokenLt:
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if isNum1 && isNum2 {
			return n1 < n2, nil
		}
		return fmt.Sprintf("%v", left) < fmt.Sprintf("%v", right), nil

	case TokenLte:
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if isNum1 && isNum2 {
			return n1 <= n2, nil
		}
		return fmt.Sprintf("%v", left) <= fmt.Sprintf("%v", right), nil

	case TokenGt:
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if isNum1 && isNum2 {
			return n1 > n2, nil
		}
		return fmt.Sprintf("%v", left) > fmt.Sprintf("%v", right), nil

	case TokenGte:
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if isNum1 && isNum2 {
			return n1 >= n2, nil
		}
		return fmt.Sprintf("%v", left) >= fmt.Sprintf("%v", right), nil

	case TokenPlus:
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
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if isNum1 && isNum2 {
			diff := n1 - n2
			if diff == math.Trunc(diff) {
				return int64(diff), nil
			}
			return diff, nil
		}
		return nil, fmt.Errorf("cannot subtract non-numeric values")

	case TokenAsterisk:
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
		if isNum1 && isNum2 {
			prod := n1 * n2
			if prod == math.Trunc(prod) {
				return int64(prod), nil
			}
			return prod, nil
		}
		return nil, fmt.Errorf("cannot multiply non-numeric values")

	case TokenSlash:
		n1, isNum1 := toFloat(left)
		n2, isNum2 := toFloat(right)
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

	stArgs := make([]storage.Value, len(args))
	for i, a := range args {
		v, err := EvalRowExpr(a, row, execSubquery)
		if err != nil {
			return nil, err
		}
		if v == nil && a != nil {
			if colRef, ok := a.(*ColumnRefExpr); ok {
				v = colRef.Name
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

func toFloat(v any) (float64, bool) {
	if v == nil {
		return 0, false
	}
	switch val := v.(type) {
	case int64:
		return float64(val), true
	case int:
		return float64(val), true
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case string:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f, true
		}
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
