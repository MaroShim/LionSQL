package storage

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// ParseUUID normalizes a UUID string into canonical 8-4-4-4-12 lowercase format.
func ParseUUID(s string) (string, error) {
	cleaned := strings.Trim(strings.TrimSpace(s), "'\"{}")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	if len(cleaned) != 32 {
		return "", fmt.Errorf("invalid UUID length %d: %q", len(cleaned), s)
	}
	if _, err := hex.DecodeString(cleaned); err != nil {
		return "", fmt.Errorf("invalid hex in UUID %q: %w", s, err)
	}
	cleaned = strings.ToLower(cleaned)
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		cleaned[0:8], cleaned[8:12], cleaned[12:16], cleaned[16:20], cleaned[20:32]), nil
}

// UUIDValue creates a storage.Value of TypeUUID with canonical normalization.
func UUIDValue(s string) Value {
	norm, err := ParseUUID(s)
	if err != nil {
		norm = strings.ToLower(strings.TrimSpace(s))
	}
	return Value{
		Type: TypeUUID,
		SVal: norm,
	}
}

// ParseBinaryBytes decodes binary representation from hex strings (0x..., \x...) or raw bytes.
func ParseBinaryBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "'\"")
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		hexStr := s[2:]
		if len(hexStr)%2 != 0 {
			hexStr = "0" + hexStr
		}
		return hex.DecodeString(hexStr)
	}
	if strings.HasPrefix(s, "\\x") || strings.HasPrefix(s, "\\X") {
		hexStr := s[2:]
		if len(hexStr)%2 != 0 {
			hexStr = "0" + hexStr
		}
		return hex.DecodeString(hexStr)
	}
	// Try direct hex decoding if even length
	if len(s) > 0 && len(s)%2 == 0 {
		if b, err := hex.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return []byte(s), nil
}

// BinaryValue creates a storage.Value of TypeBinary from raw bytes.
func BinaryValue(b []byte) Value {
	return Value{
		Type:   TypeBinary,
		BinVal: string(b),
		SVal:   "0x" + hex.EncodeToString(b),
	}
}

// BinaryValueFromString parses a hex or string representation into a TypeBinary value.
func BinaryValueFromString(s string) Value {
	b, err := ParseBinaryBytes(s)
	if err != nil {
		b = []byte(s)
	}
	return BinaryValue(b)
}

// ParseDate parses various common date string formats into midnight UTC time.Time.
func ParseDate(s string) (time.Time, error) {
	s = strings.Trim(strings.TrimSpace(s), "'\"")
	layouts := []string{
		"2006-01-02",
		"2006/01/02",
		"2006.01.02",
		"20060102",
		time.RFC3339,
		"2006-01-02 15:04:05",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse date: %q", s)
}

// DateValue creates a storage.Value of TypeDate truncated to midnight UTC.
func DateValue(t time.Time) Value {
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return Value{
		Type: TypeDate,
		TVal: d,
		SVal: d.Format("2006-01-02"),
		IVal: d.Unix(),
	}
}

// DateValueFromString creates a storage.Value of TypeDate from string.
func DateValueFromString(s string) Value {
	t, err := ParseDate(s)
	if err != nil {
		t = time.Time{}
	}
	return DateValue(t)
}

// DateValueFromAny converts an existing storage.Value to TypeDate.
func DateValueFromAny(v Value) Value {
	if v.IsNull {
		return NullValue(TypeDate)
	}
	if v.Type == TypeDate {
		return v
	}
	if v.Type == TypeDateTime {
		return DateValue(v.TVal)
	}
	return DateValueFromString(v.String())
}

// ParseTime parses a time-of-day string into nanoseconds from midnight and time.Time.
func ParseTime(s string) (time.Time, int64, error) {
	s = strings.Trim(strings.TrimSpace(s), "'\"")
	layouts := []string{
		"15:04:05.999999999",
		"15:04:05.999999",
		"15:04:05.999",
		"15:04:05",
		"15:04",
		time.RFC3339,
		"2006-01-02 15:04:05",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			ns := int64(t.Hour())*3600*1e9 + int64(t.Minute())*60*1e9 + int64(t.Second())*1e9 + int64(t.Nanosecond())
			base := time.Date(1970, 1, 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
			return base, ns, nil
		}
	}
	return time.Time{}, 0, fmt.Errorf("cannot parse time: %q", s)
}

// TimeValue creates a storage.Value of TypeTime.
func TimeValue(t time.Time) Value {
	base := time.Date(1970, 1, 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	ns := int64(t.Hour())*3600*1e9 + int64(t.Minute())*60*1e9 + int64(t.Second())*1e9 + int64(t.Nanosecond())
	return Value{
		Type: TypeTime,
		TVal: base,
		IVal: ns,
		SVal: base.Format("15:04:05"),
	}
}

// TimeValueFromString creates a storage.Value of TypeTime from string.
func TimeValueFromString(s string) Value {
	t, _, err := ParseTime(s)
	if err != nil {
		t = time.Time{}
	}
	return TimeValue(t)
}

// TimeValueFromAny converts an existing storage.Value to TypeTime.
func TimeValueFromAny(v Value) Value {
	if v.IsNull {
		return NullValue(TypeTime)
	}
	if v.Type == TypeTime {
		return v
	}
	if v.Type == TypeDateTime {
		return TimeValue(v.TVal)
	}
	return TimeValueFromString(v.String())
}

// CompareExtendedValues compares non-standard types (UUID, Binary, Date, Time).
func CompareExtendedValues(v, other Value) int {
	switch v.Type {
	case TypeUUID:
		return strings.Compare(strings.ToLower(v.SVal), strings.ToLower(other.SVal))
	case TypeBinary:
		return strings.Compare(v.BinVal, other.BinVal)
	case TypeDate:
		if v.TVal.Before(other.TVal) {
			return -1
		} else if v.TVal.After(other.TVal) {
			return 1
		}
		return 0
	case TypeTime:
		if v.IVal < other.IVal {
			return -1
		} else if v.IVal > other.IVal {
			return 1
		}
		return 0
	default:
		return 0
	}
}
