package storage

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// CoerceBinaryValues coerces two values of different data types into a common comparable
// data type according to canonical SQL type precedence rules:
//
//	Decimal > Float > Int > Bool
//	DateTime / Date > VarChar
//
// If the types are already identical or cannot be coerced, it returns the original values
// and coerced=false.
func CoerceBinaryValues(a, b Value) (Value, Value, bool) {
	if a.IsNull || b.IsNull || a.Type == b.Type {
		return a, b, false
	}

	// 1. Decimal promotion (highest numeric precedence)
	if a.Type == TypeDecimal || b.Type == TypeDecimal {
		da := a.Decimal()
		db := b.Decimal()
		return DecimalValueFromDecimal(da), DecimalValueFromDecimal(db), true
	}

	// 2. Numeric promotion: Float > Int
	if (a.Type == TypeInt && b.Type == TypeFloat) || (a.Type == TypeFloat && b.Type == TypeInt) {
		if a.Type == TypeInt {
			return FloatValue(float64(a.IVal)), b, true
		}
		return a, FloatValue(float64(b.IVal)), true
	}

	// 3. Boolean and Integer coercion (0 / 1)
	if (a.Type == TypeBool && b.Type == TypeInt) || (a.Type == TypeInt && b.Type == TypeBool) {
		if a.Type == TypeBool {
			return IntValue(boolToInt(a.BVal)), b, true
		}
		return a, IntValue(boolToInt(b.BVal)), true
	}

	// 4. DateTime <-> VarChar coercion
	if a.Type == TypeDateTime && b.Type == TypeVarChar {
		if dt, err := DateTimeValueFromAny(b); err == nil && !dt.IsNull {
			return a, dt, true
		}
	} else if a.Type == TypeVarChar && b.Type == TypeDateTime {
		if dt, err := DateTimeValueFromAny(a); err == nil && !dt.IsNull {
			return dt, b, true
		}
	}

	// 5. Date <-> VarChar coercion
	if a.Type == TypeDate && b.Type == TypeVarChar {
		d := DateValueFromString(b.SVal)
		if !d.IsNull {
			return a, d, true
		}
	} else if a.Type == TypeVarChar && b.Type == TypeDate {
		d := DateValueFromString(a.SVal)
		if !d.IsNull {
			return d, b, true
		}
	}

	// 6. DateTime <-> Date coercion
	if (a.Type == TypeDateTime && b.Type == TypeDate) || (a.Type == TypeDate && b.Type == TypeDateTime) {
		return a, b, true
	}

	return a, b, false
}

// CompareFloatEpsilon compares two float64 numbers with IEEE-754 epsilon precision (1e-9).
func CompareFloatEpsilon(f1, f2 float64) int {
	if f1 == f2 || math.Abs(f1-f2) < 1e-9 {
		return 0
	}
	if f1 < f2 {
		return -1
	}
	return 1
}

// ConvertValueToColumn strictly converts val into the target column definition for DDL schema
// migrations (such as ALTER TABLE ALTER COLUMN TYPE / SET NOT NULL). Invalid conversions fail
// closed with ErrInvalidDataType / ErrInvalidJSON / ErrNotNullViolation.
func ConvertValueToColumn(val Value, col Column) (Value, error) {
	if val.IsNull {
		if !col.Nullable && !col.PrimaryKey {
			return Value{}, fmt.Errorf("%w: column %q contains NULL values", ErrNotNullViolation, col.Name)
		}
		return NullValue(col.Type), nil
	}

	if val.Type == col.Type {
		if col.Type == TypeDecimal && col.Scale > 0 {
			return DecimalValueFromDecimal(val.DVal.Rescale(int32(col.Scale))), nil
		}
		if col.Type == TypeJSON && !IsValidJSON(val.SVal) {
			return Value{}, fmt.Errorf("%w: column %q", ErrInvalidJSON, col.Name)
		}
		return val, nil
	}

	switch col.Type {
	case TypeInt:
		switch val.Type {
		case TypeFloat:
			if math.IsNaN(val.FVal) || math.IsInf(val.FVal, 0) || val.FVal < math.MinInt64 || val.FVal > math.MaxInt64 {
				return Value{}, fmt.Errorf("%w: float %v out of INT range for column %q", ErrInvalidDataType, val.FVal, col.Name)
			}
			return IntValue(int64(val.FVal)), nil
		case TypeBool:
			return IntValue(boolToInt(val.BVal)), nil
		case TypeDecimal:
			s := val.DVal.Rescale(0).String()
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return Value{}, fmt.Errorf("%w: decimal %s out of INT range for column %q: %v", ErrInvalidDataType, val.String(), col.Name, err)
			}
			return IntValue(n), nil
		case TypeVarChar:
			n, err := strconv.ParseInt(strings.TrimSpace(val.SVal), 10, 64)
			if err != nil {
				return Value{}, fmt.Errorf("%w: cannot convert %q to INT for column %q: %v", ErrInvalidDataType, val.SVal, col.Name, err)
			}
			return IntValue(n), nil
		}
	case TypeFloat:
		switch val.Type {
		case TypeInt:
			return FloatValue(float64(val.IVal)), nil
		case TypeBool:
			return FloatValue(float64(boolToInt(val.BVal))), nil
		case TypeDecimal:
			f, err := strconv.ParseFloat(val.DVal.String(), 64)
			if err != nil {
				return Value{}, fmt.Errorf("%w: cannot convert decimal %s to FLOAT for column %q: %v", ErrInvalidDataType, val.String(), col.Name, err)
			}
			return FloatValue(f), nil
		case TypeVarChar:
			f, err := strconv.ParseFloat(strings.TrimSpace(val.SVal), 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return Value{}, fmt.Errorf("%w: cannot convert %q to FLOAT for column %q", ErrInvalidDataType, val.SVal, col.Name)
			}
			return FloatValue(f), nil
		}
	case TypeVarChar:
		return StringValue(val.String()), nil
	case TypeDecimal:
		var dec Decimal
		switch val.Type {
		case TypeInt:
			dec = NewDecimalFromInt(val.IVal, int32(col.Scale))
		case TypeFloat:
			if math.IsNaN(val.FVal) || math.IsInf(val.FVal, 0) {
				return Value{}, fmt.Errorf("%w: cannot convert %v to DECIMAL for column %q", ErrInvalidDataType, val.FVal, col.Name)
			}
			dec = NewDecimalFromFloat(val.FVal, int32(col.Scale))
		case TypeBool:
			dec = NewDecimalFromInt(boolToInt(val.BVal), int32(col.Scale))
		case TypeVarChar:
			parsed, err := ParseDecimal(val.SVal)
			if err != nil {
				return Value{}, fmt.Errorf("%w: cannot convert %q to DECIMAL for column %q: %v", ErrInvalidDataType, val.SVal, col.Name, err)
			}
			dec = parsed
		default:
			return Value{}, fmt.Errorf("%w: cannot convert %s to DECIMAL for column %q", ErrInvalidDataType, val.Type, col.Name)
		}
		if col.Scale > 0 {
			dec = dec.Rescale(int32(col.Scale))
		}
		return DecimalValueFromDecimal(dec), nil
	case TypeBool:
		switch val.Type {
		case TypeInt:
			return BoolValue(val.IVal != 0), nil
		case TypeFloat:
			return BoolValue(val.FVal != 0), nil
		case TypeVarChar:
			b, err := strconv.ParseBool(strings.TrimSpace(val.SVal))
			if err != nil {
				return Value{}, fmt.Errorf("%w: cannot convert %q to BOOL for column %q", ErrInvalidDataType, val.SVal, col.Name)
			}
			return BoolValue(b), nil
		}
	case TypeJSON:
		if val.Type == TypeVarChar {
			if !IsValidJSON(val.SVal) {
				return Value{}, fmt.Errorf("%w: column %q", ErrInvalidJSON, col.Name)
			}
			return JSONValue(val.SVal), nil
		}
	case TypeUUID:
		if val.Type == TypeVarChar {
			u, err := ParseUUID(val.SVal)
			if err != nil {
				return Value{}, fmt.Errorf("%w: cannot convert %q to UUID for column %q: %v", ErrInvalidDataType, val.SVal, col.Name, err)
			}
			return UUIDValue(u), nil
		}
	case TypeBinary:
		if val.Type == TypeVarChar {
			return BinaryValueFromString(val.SVal), nil
		}
	case TypeDate:
		if val.Type == TypeDateTime {
			return DateValue(val.TVal), nil
		}
		if val.Type == TypeVarChar {
			t, err := ParseDate(val.SVal)
			if err != nil {
				return Value{}, fmt.Errorf("%w: cannot convert %q to DATE for column %q: %v", ErrInvalidDataType, val.SVal, col.Name, err)
			}
			return DateValue(t), nil
		}
	case TypeTime:
		if val.Type == TypeDateTime {
			return TimeValue(val.TVal), nil
		}
		if val.Type == TypeVarChar {
			t, _, err := ParseTime(val.SVal)
			if err != nil {
				return Value{}, fmt.Errorf("%w: cannot convert %q to TIME for column %q: %v", ErrInvalidDataType, val.SVal, col.Name, err)
			}
			return TimeValue(t), nil
		}
	case TypeDateTime:
		if val.Type == TypeDate || val.Type == TypeVarChar {
			dt, err := DateTimeValueFromAny(val)
			if err != nil {
				return Value{}, fmt.Errorf("%w: cannot convert %q to DATETIME for column %q: %v", ErrInvalidDataType, val.String(), col.Name, err)
			}
			return dt, nil
		}
	}

	return Value{}, fmt.Errorf("%w: cannot convert %s value %q to %s for column %q",
		ErrInvalidDataType, val.Type, val.String(), col.Type, col.Name)
}
