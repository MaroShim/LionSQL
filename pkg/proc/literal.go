package proc

import (
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/storage"
)

// ParseLiteralDefault parses a literal SQL expression or default value string into a storage.Value.
// It handles outer parentheses (e.g. `((0))`), function calls (e.g. `GETDATE()`, `NEWID()`),
// string literals, and type-specific data parsing.
func ParseLiteralDefault(s string, targetType storage.DataType) storage.Value {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "NULL") {
		return storage.NullValue(targetType)
	}

	// Strip matched outer parentheses: e.g. `((0))` -> `0`
	for strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		depth := 0
		allWrapped := true
		for i := 0; i < len(s)-1; i++ {
			if s[i] == '(' {
				depth++
			} else if s[i] == ')' {
				depth--
				if depth == 0 {
					allWrapped = false
					break
				}
			}
		}
		if allWrapped && depth == 1 {
			s = strings.TrimSpace(s[1 : len(s)-1])
		} else {
			break
		}
	}

	upper := strings.ToUpper(s)
	switch upper {
	case "NEWID7()", "UUIDV7()", "UUID_GENERATE_V7()":
		return storage.UUIDValue(GenerateUUIDv7())
	case "NEWID()", "GEN_RANDOM_UUID()":
		return storage.UUIDValue(GenerateUUIDv4())
	case "GETDATE()", "NOW()", "CURRENT_TIMESTAMP":
		return storage.DateTimeValue(time.Now())
	case "GETUTCDATE()", "SYSDATETIME()":
		return storage.DateTimeValue(time.Now().UTC())
	}

	isNStr := (strings.HasPrefix(s, "N'") || strings.HasPrefix(s, "n'")) && strings.HasSuffix(s, "'") && len(s) >= 3
	isRegularStr := strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2
	if isNStr || isRegularStr {
		var strVal string
		if isNStr {
			strVal = s[2 : len(s)-1]
		} else {
			strVal = s[1 : len(s)-1]
		}
		strVal = strings.ReplaceAll(strVal, "''", "'")
		if targetType == storage.TypeJSON {
			return storage.JSONValue(strVal)
		}
		if targetType == storage.TypeUUID {
			return storage.UUIDValue(strVal)
		}
		if targetType == storage.TypeDate {
			return storage.DateValueFromString(strVal)
		}
		if targetType == storage.TypeTime {
			return storage.TimeValueFromString(strVal)
		}
		if targetType == storage.TypeBinary {
			return storage.BinaryValueFromString(strVal)
		}
		if targetType == storage.TypeBool {
			return storage.BoolValueFromAny(storage.StringValue(strVal))
		}
		return storage.StringValue(strVal)
	}

	switch targetType {
	case storage.TypeJSON:
		return storage.JSONValue(s)
	case storage.TypeUUID:
		return storage.UUIDValue(s)
	case storage.TypeBinary:
		return storage.BinaryValueFromString(s)
	case storage.TypeDate:
		return storage.DateValueFromString(s)
	case storage.TypeTime:
		return storage.TimeValueFromString(s)
	case storage.TypeInt:
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return storage.IntValue(i)
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.IntValue(int64(f))
		}
	case storage.TypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.FloatValue(f)
		}
	case storage.TypeDecimal:
		return storage.DecimalValue(s)
	case storage.TypeBool:
		return storage.BoolValueFromAny(storage.StringValue(s))
	}
	return storage.StringValue(s)
}
