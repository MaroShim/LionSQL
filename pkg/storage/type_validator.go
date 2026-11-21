package storage

import (
	"fmt"
	"math/big"
	"strings"
	"unicode/utf8"
)

// ValidateAndNormalizeValue validates a single field value against the target column schema
// and normalizes it (e.g. padding fixed CHAR, enforcing VARCHAR length limits,
// checking TINYINT/SMALLINT/INT overflow, checking DECIMAL precision and scale limits).
func ValidateAndNormalizeValue(col *Column, val Value, tableName string) (Value, error) {
	if val.IsNull {
		if !col.Nullable && !col.PrimaryKey && !col.Identity && !col.IsRowVersion {
			return val, fmt.Errorf("cannot insert NULL into column %q of table %q (NOT NULL constraint)", col.Name, tableName)
		}
		return val, nil
	}

	switch col.Type {
	case TypeVarChar:
		s := val.String()
		charLen := utf8.RuneCountInString(s)
		if col.Precision > 0 {
			if charLen > col.Precision {
				return val, fmt.Errorf("string or binary data would be truncated: table %q, column %q, max length %d, actual length %d",
					tableName, col.Name, col.Precision, charLen)
			}
			if col.IsFixed && charLen < col.Precision {
				s = s + strings.Repeat(" ", col.Precision-charLen)
				return StringValue(s), nil
			}
		} else if col.IsFixed {
			// default CHAR(1)
			if charLen > 1 {
				return val, fmt.Errorf("string or binary data would be truncated: table %q, column %q, max length 1, actual length %d",
					tableName, col.Name, charLen)
			}
			if charLen == 0 {
				return StringValue(" "), nil
			}
		}
		if s != val.SVal {
			return StringValue(s), nil
		}
		return val, nil

	case TypeInt:
		raw := strings.ToUpper(col.RawType)
		var i int64
		switch val.Type {
		case TypeInt:
			i = val.IVal
		case TypeFloat, TypeReal:
			i = int64(val.FVal)
		case TypeBool:
			if val.BVal {
				i = 1
			} else {
				i = 0
			}
		case TypeDecimal:
			i = val.Decimal().Int64()
		default:
			return val, nil
		}

		if raw == "TINYINT" {
			if i < 0 || i > 255 {
				return val, fmt.Errorf("arithmetic overflow converting %d to data type tinyint: value out of range [0, 255] (table %q, column %q)",
					i, tableName, col.Name)
			}
		} else if raw == "SMALLINT" || raw == "INT2" {
			if i < -32768 || i > 32767 {
				return val, fmt.Errorf("arithmetic overflow converting %d to data type smallint: value out of range [-32768, 32767] (table %q, column %q)",
					i, tableName, col.Name)
			}
		} else if raw == "INT" || raw == "INT4" {
			if i < -2147483648 || i > 2147483647 {
				return val, fmt.Errorf("arithmetic overflow converting %d to data type int: value out of range [-2147483648, 2147483647] (table %q, column %q)",
					i, tableName, col.Name)
			}
		}
		if val.Type != TypeInt {
			val = IntValue(i)
		}
		if col.Precision > 0 {
			val.Precision = col.Precision
		}
		return val, nil

	case TypeDecimal:
		if col.Precision > 0 {
			var dec Decimal
			if val.Type == TypeDecimal {
				dec = val.Decimal()
			} else {
				var err error
				dec, err = ParseDecimal(val.String())
				if err != nil {
					return val, fmt.Errorf("invalid decimal value %q for column %q: %w", val.String(), col.Name, err)
				}
			}

			// Rescale to column scale
			rescaled := dec.Rescale(int32(col.Scale))

			// Check integer digit length against max allowed integer digits: (Precision - Scale)
			maxIntDigits := col.Precision - col.Scale
			if maxIntDigits < 0 {
				maxIntDigits = 0
			}

			intPart := new(big.Int).Quo(rescaled.UnscaledValue(), exp10(rescaled.scale))
			intPartAbs := new(big.Int).Abs(intPart)

			if maxIntDigits == 0 {
				if intPartAbs.Sign() != 0 {
					return val, fmt.Errorf("arithmetic overflow converting numeric to data type numeric: precision %d, scale %d (table %q, column %q)",
						col.Precision, col.Scale, tableName, col.Name)
				}
			} else {
				intStr := intPartAbs.String()
				if len(intStr) > maxIntDigits {
					return val, fmt.Errorf("arithmetic overflow converting numeric to data type numeric: precision %d, scale %d (table %q, column %q)",
						col.Precision, col.Scale, tableName, col.Name)
				}
			}

			return DecimalValueFromDecimal(rescaled), nil
		}
		return val, nil

	case TypeBinary:
		if col.Precision > 0 {
			byteLen := len(val.BinVal)
			if byteLen > col.Precision {
				return val, fmt.Errorf("string or binary data would be truncated: table %q, column %q, max length %d, actual length %d",
					tableName, col.Name, col.Precision, byteLen)
			}
			if col.IsFixed && byteLen < col.Precision {
				padded := val.BinVal + strings.Repeat("\x00", col.Precision-byteLen)
				return BinaryValue([]byte(padded)), nil
			}
		}
		return val, nil
	}

	return val, nil
}

// ValidateAndNormalizeRecord validates all fields in a record against schema and returns normalized record.
func ValidateAndNormalizeRecord(schema *TableSchema, rec Record) (Record, error) {
	if schema == nil || rec == nil {
		return rec, nil
	}
	for _, col := range schema.Columns {
		if val, exists := rec[col.Name]; exists {
			norm, err := ValidateAndNormalizeValue(&col, val, schema.Name)
			if err != nil {
				return nil, err
			}
			rec[col.Name] = norm
		}
	}
	return rec, nil
}

// ValidateAndNormalizeRow validates all column values in an ordered row against schema and returns normalized row.
func ValidateAndNormalizeRow(schema *TableSchema, row Row) (Row, error) {
	if schema == nil || len(row.Values) == 0 {
		return row, nil
	}
	for i := 0; i < len(schema.Columns) && i < len(row.Values); i++ {
		norm, err := ValidateAndNormalizeValue(&schema.Columns[i], row.Values[i], schema.Name)
		if err != nil {
			return row, err
		}
		row.Values[i] = norm
	}
	return row, nil
}
