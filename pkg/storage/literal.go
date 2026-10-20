package storage

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	uuidv7Mu sync.Mutex
	lastV7Ms int64
	v7Seq    uint16
)

func generateUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant 10
	return strings.ToUpper(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

func generateUUIDv7() string {
	var b [16]byte
	nowMs := time.Now().UnixMilli()

	uuidv7Mu.Lock()
	if nowMs > lastV7Ms {
		lastV7Ms = nowMs
		v7Seq = 0
	} else {
		v7Seq++
	}
	seq := v7Seq
	uuidv7Mu.Unlock()

	b[0] = byte(nowMs >> 40)
	b[1] = byte(nowMs >> 32)
	b[2] = byte(nowMs >> 24)
	b[3] = byte(nowMs >> 16)
	b[4] = byte(nowMs >> 8)
	b[5] = byte(nowMs)

	b[6] = 0x70 | byte((seq>>8)&0x0F)
	b[7] = byte(seq & 0xFF)

	var randBytes [8]byte
	_, _ = rand.Read(randBytes[:])
	b[8] = (randBytes[0] & 0x3F) | 0x80
	copy(b[9:], randBytes[1:])

	return strings.ToUpper(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// ParseLiteralDefault parses a literal SQL expression or default value string into a Value.
// It handles outer parentheses (e.g. `((0))`), function calls (e.g. `GETDATE()`, `NEWID()`),
// string literals, and type-specific data parsing.
func ParseLiteralDefault(s string, targetType DataType) Value {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "NULL") {
		return NullValue(targetType)
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
		return UUIDValue(generateUUIDv7())
	case "NEWID()", "GEN_RANDOM_UUID()":
		return UUIDValue(generateUUIDv4())
	case "GETDATE()", "NOW()", "CURRENT_TIMESTAMP":
		return DateTimeValue(time.Now())
	case "GETUTCDATE()", "SYSDATETIME()":
		return DateTimeValue(time.Now().UTC())
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
		if targetType == TypeJSON {
			return JSONValue(strVal)
		}
		if targetType == TypeUUID {
			return UUIDValue(strVal)
		}
		if targetType == TypeDate {
			return DateValueFromString(strVal)
		}
		if targetType == TypeTime {
			return TimeValueFromString(strVal)
		}
		if targetType == TypeBinary {
			return BinaryValueFromString(strVal)
		}
		if targetType == TypeBool {
			return BoolValueFromAny(StringValue(strVal))
		}
		return StringValue(strVal)
	}

	switch targetType {
	case TypeJSON:
		return JSONValue(s)
	case TypeUUID:
		return UUIDValue(s)
	case TypeBinary:
		return BinaryValueFromString(s)
	case TypeDate:
		return DateValueFromString(s)
	case TypeTime:
		return TimeValueFromString(s)
	case TypeInt:
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return IntValue(i)
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return IntValue(int64(f))
		}
	case TypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return FloatValue(f)
		}
	case TypeDecimal:
		return DecimalValue(s)
	case TypeBool:
		return BoolValueFromAny(StringValue(s))
	}
	return StringValue(s)
}
