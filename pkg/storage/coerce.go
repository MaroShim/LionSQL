package storage

import (
	"math"
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
