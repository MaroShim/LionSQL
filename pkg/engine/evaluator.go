package engine

import (
	"fmt"
	"strconv"
	"strings"

	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// EvalBinaryOp evaluates an arithmetic binary operator (+, -, *, /, %) on two storage.Values
// and casts the result to targetType, preventing integer divide-by-zero panics and
// honoring the SQL type promotion lattice (Decimal > Float > Int > Bool) (Golden Rule 2 & 11).
func EvalBinaryOp(op byte, leftVal, rightVal storage.Value, targetType storage.DataType) (storage.Value, error) {
	if leftVal.IsNull || rightVal.IsNull {
		return storage.NullValue(targetType), nil
	}

	leftF, leftI, num1 := ToNumeric(leftVal)
	rightF, rightI, num2 := ToNumeric(rightVal)
	if !num1 || !num2 {
		return storage.NullValue(targetType), fmt.Errorf("incompatible operand types for arithmetic operator %c", op)
	}

	// 1. Division / Modulo zero guard (Golden Rule 2 & 11)
	if op == '/' || op == '%' {
		if rightF == 0 || (targetType == storage.TypeInt && rightI == 0) {
			return storage.NullValue(targetType), fmt.Errorf("divide by zero error encountered")
		}
	}

	switch targetType {
	case storage.TypeInt:
		var res int64
		switch op {
		case '+':
			res = leftI + rightI
		case '-':
			res = leftI - rightI
		case '*':
			res = leftI * rightI
		case '/':
			res = leftI / rightI
		case '%':
			res = leftI % rightI
		default:
			return storage.NullValue(targetType), fmt.Errorf("unsupported operator %c for TypeInt", op)
		}
		return storage.IntValue(res), nil

	case storage.TypeFloat:
		var res float64
		switch op {
		case '+':
			res = leftF + rightF
		case '-':
			res = leftF - rightF
		case '*':
			res = leftF * rightF
		case '/':
			res = leftF / rightF
		default:
			return storage.NullValue(targetType), fmt.Errorf("unsupported operator %c for TypeFloat", op)
		}
		return storage.FloatValue(res), nil

	case storage.TypeDecimal:
		var res float64
		switch op {
		case '+':
			res = leftF + rightF
		case '-':
			res = leftF - rightF
		case '*':
			res = leftF * rightF
		case '/':
			res = leftF / rightF
		default:
			return storage.NullValue(targetType), fmt.Errorf("unsupported operator %c for TypeDecimal", op)
		}
		return storage.DecimalValue(strconv.FormatFloat(res, 'f', 2, 64)), nil

	default:
		return CoerceAndCalculate(op, leftVal, rightVal)
	}
}

// CoerceAndCalculate evaluates a binary operation by promoting operands using
// the Type Promotion Lattice (Decimal > Float > Int) from pkg/storage/coerce.go.
func CoerceAndCalculate(op byte, left, right storage.Value) (storage.Value, error) {
	cLeft, cRight, _ := storage.CoerceBinaryValues(left, right)
	promotedType := cLeft.Type
	if promotedType == storage.TypeUnknown || promotedType == 0 {
		promotedType = storage.TypeInt
	}
	return EvalBinaryOp(op, cLeft, cRight, promotedType)
}

// ToNumeric converts a storage.Value into float64 and int64 representations if numeric.
func ToNumeric(val storage.Value) (float64, int64, bool) {
	if val.IsNull {
		return 0, 0, false
	}
	switch val.Type {
	case storage.TypeInt:
		return float64(val.IVal), val.IVal, true
	case storage.TypeFloat:
		return val.FVal, int64(val.FVal), true
	case storage.TypeDecimal:
		f, err := strconv.ParseFloat(val.String(), 64)
		if err == nil {
			return f, int64(f), true
		}
		return 0, 0, false
	case storage.TypeVarChar:
		s := strings.TrimSpace(val.SVal)
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return float64(i), i, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, int64(f), true
		}
		return 0, 0, false
	default:
		return 0, 0, false
	}
}

// ResolveOperand resolves a token string (column identifier or numeric literal) against a record.
func ResolveOperand(token string, rec storage.Record) (storage.Value, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return storage.NullValue(storage.TypeVarChar), false
	}
	clean := cleanIdentifier(token)
	if val, ok := rec.Get(clean); ok {
		return val, true
	}
	if i, err := strconv.ParseInt(token, 10, 64); err == nil {
		return storage.IntValue(i), true
	}
	if f, err := strconv.ParseFloat(token, 64); err == nil {
		return storage.FloatValue(f), true
	}
	return storage.NullValue(storage.TypeVarChar), false
}

// EvalUpdateExpr evaluates an update assignment expression (e.g. "price + 20", "val / 2", "[Price]")
// against an existing record, writing the computed result into targetType.
func EvalUpdateExpr(exprStr string, rec storage.Record, targetType storage.DataType) (storage.Value, error) {
	exprStr = strings.TrimSpace(exprStr)
	if opIdx, op := sqlutil.FindTopLevelArithmeticOp(exprStr); opIdx != -1 {
		leftStr := strings.TrimSpace(exprStr[:opIdx])
		rightStr := strings.TrimSpace(exprStr[opIdx+1:])

		leftVal, ok1 := ResolveOperand(leftStr, rec)
		rightVal, ok2 := ResolveOperand(rightStr, rec)
		if ok1 && ok2 {
			return EvalBinaryOp(byte(op), leftVal, rightVal, targetType)
		}
	}

	clean := cleanIdentifier(exprStr)
	if val, ok := rec.Get(clean); ok {
		return val, nil
	}

	return parseLiteralValue(exprStr, targetType), nil
}

// Unexported package-level compatibility aliases
func evalUpdateExpr(exprStr string, rec storage.Record, targetType storage.DataType) (storage.Value, error) {
	return EvalUpdateExpr(exprStr, rec, targetType)
}

func resolveOperand(token string, rec storage.Record) (storage.Value, bool) {
	return ResolveOperand(token, rec)
}

func toNumeric(val storage.Value) (float64, int64, bool) {
	return ToNumeric(val)
}
