package proc

import (
	"crypto/rand"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/storage"
)

var commonDateFormats = []string{
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02",
	"2006/01/02 15:04:05",
	"2006/01/02",
	"01/02/2006 15:04:05",
	"01/02/2006",
}

// EvalBuiltin evaluates standard T-SQL scalar functions.
func EvalBuiltin(name string, args []storage.Value) storage.Value {
	upper := strings.ToUpper(cleanBuiltinName(name))

	switch upper {
	// ----------------------------------------------------
	// 1. Date & Time Functions
	// ----------------------------------------------------
	case "GETDATE", "NOW":
		return storage.StringValue(time.Now().Format("2006-01-02 15:04:05"))

	case "GETUTCDATE":
		return storage.StringValue(time.Now().UTC().Format("2006-01-02 15:04:05"))

	case "SYSDATETIME":
		return storage.StringValue(time.Now().Format("2006-01-02 15:04:05.0000000"))

	case "YEAR":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		if t, ok := parseDateValue(args[0]); ok {
			return storage.IntValue(int64(t.Year()))
		}
		return storage.NullValue(storage.TypeInt)

	case "MONTH":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		if t, ok := parseDateValue(args[0]); ok {
			return storage.IntValue(int64(t.Month()))
		}
		return storage.NullValue(storage.TypeInt)

	case "DAY":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		if t, ok := parseDateValue(args[0]); ok {
			return storage.IntValue(int64(t.Day()))
		}
		return storage.NullValue(storage.TypeInt)

	case "DATEPART":
		if len(args) < 2 || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		part := normalizeDatePart(valToString(args[0]))
		t, ok := parseDateValue(args[1])
		if !ok {
			return storage.NullValue(storage.TypeInt)
		}
		return evalDatePart(part, t)

	case "DATEDIFF":
		if len(args) < 3 || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		part := normalizeDatePart(valToString(args[0]))
		t1, ok1 := parseDateValue(args[1])
		t2, ok2 := parseDateValue(args[2])
		if !ok1 || !ok2 {
			return storage.NullValue(storage.TypeInt)
		}
		return evalDateDiff(part, t1, t2)

	case "DATEADD":
		if len(args) < 3 || args[2].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		part := normalizeDatePart(valToString(args[0]))
		n := args[1].IVal
		if args[1].Type == storage.TypeFloat {
			n = int64(args[1].FVal)
		} else if args[1].Type == storage.TypeVarChar {
			if parsedN, err := strconv.ParseInt(strings.TrimSpace(args[1].SVal), 10, 64); err == nil {
				n = parsedN
			}
		}
		t, ok := parseDateValue(args[2])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		res := evalDateAdd(part, n, t)
		return storage.StringValue(res.Format("2006-01-02 15:04:05"))

	// ----------------------------------------------------
	// 2. String Functions
	// ----------------------------------------------------
	case "CONCAT":
		var b strings.Builder
		for _, a := range args {
			if !a.IsNull {
				b.WriteString(valToString(a))
			}
		}
		return storage.StringValue(b.String())

	case "LEN", "LENGTH":
		if len(args) == 0 || args[0].IsNull {
			return storage.IntValue(0)
		}
		return storage.IntValue(int64(len([]rune(valToString(args[0])))))

	case "UPPER":
		if len(args) == 0 || args[0].IsNull {
			return storage.StringValue("")
		}
		return storage.StringValue(strings.ToUpper(valToString(args[0])))

	case "LOWER":
		if len(args) == 0 || args[0].IsNull {
			return storage.StringValue("")
		}
		return storage.StringValue(strings.ToLower(valToString(args[0])))

	case "TRIM":
		if len(args) == 0 || args[0].IsNull {
			return storage.StringValue("")
		}
		return storage.StringValue(strings.TrimSpace(valToString(args[0])))

	case "LTRIM":
		if len(args) == 0 || args[0].IsNull {
			return storage.StringValue("")
		}
		return storage.StringValue(strings.TrimLeft(valToString(args[0]), " \t\r\n"))

	case "RTRIM":
		if len(args) == 0 || args[0].IsNull {
			return storage.StringValue("")
		}
		return storage.StringValue(strings.TrimRight(valToString(args[0]), " \t\r\n"))

	case "LEFT":
		if len(args) < 2 || args[0].IsNull {
			return storage.StringValue("")
		}
		runes := []rune(valToString(args[0]))
		n := int(args[1].IVal)
		if n <= 0 {
			return storage.StringValue("")
		}
		if n > len(runes) {
			n = len(runes)
		}
		return storage.StringValue(string(runes[:n]))

	case "RIGHT":
		if len(args) < 2 || args[0].IsNull {
			return storage.StringValue("")
		}
		runes := []rune(valToString(args[0]))
		n := int(args[1].IVal)
		if n <= 0 {
			return storage.StringValue("")
		}
		if n > len(runes) {
			n = len(runes)
		}
		return storage.StringValue(string(runes[len(runes)-n:]))

	case "SUBSTRING":
		if len(args) < 3 || args[0].IsNull {
			return storage.StringValue("")
		}
		runes := []rune(valToString(args[0]))
		start := int(args[1].IVal) - 1 // T-SQL is 1-indexed
		length := int(args[2].IVal)
		if length <= 0 {
			return storage.StringValue("")
		}
		if start < 0 {
			start = 0
		}
		if start >= len(runes) {
			return storage.StringValue("")
		}
		end := start + length
		if end > len(runes) {
			end = len(runes)
		}
		return storage.StringValue(string(runes[start:end]))

	case "REPLACE":
		if len(args) < 3 || args[0].IsNull {
			return storage.StringValue("")
		}
		str := valToString(args[0])
		oldSub := valToString(args[1])
		newSub := valToString(args[2])
		return storage.StringValue(strings.ReplaceAll(str, oldSub, newSub))

	case "REVERSE":
		if len(args) == 0 || args[0].IsNull {
			return storage.StringValue("")
		}
		runes := []rune(valToString(args[0]))
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return storage.StringValue(string(runes))

	case "CHARINDEX":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.IntValue(0)
		}
		sub := valToString(args[0])
		target := valToString(args[1])
		start := 1
		if len(args) >= 3 && !args[2].IsNull {
			start = int(args[2].IVal)
		}
		if start < 1 {
			start = 1
		}
		runes := []rune(target)
		if start > len(runes) {
			return storage.IntValue(0)
		}
		subSlice := string(runes[start-1:])
		idx := strings.Index(strings.ToUpper(subSlice), strings.ToUpper(sub))
		if idx == -1 {
			return storage.IntValue(0)
		}
		runeOffset := len([]rune(subSlice[:idx]))
		return storage.IntValue(int64(start + runeOffset))

	case "STUFF":
		if len(args) < 4 || args[0].IsNull || args[1].IsNull || args[2].IsNull || args[3].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		runes := []rune(valToString(args[0]))
		start := int(args[1].IVal)
		if args[1].Type == storage.TypeFloat {
			start = int(args[1].FVal)
		}
		length := int(args[2].IVal)
		if args[2].Type == storage.TypeFloat {
			length = int(args[2].FVal)
		}
		replacement := valToString(args[3])

		if start < 1 || start > len(runes)+1 || length < 0 {
			return storage.NullValue(storage.TypeVarChar)
		}

		deleteStart := start - 1
		deleteEnd := deleteStart + length
		if deleteEnd > len(runes) {
			deleteEnd = len(runes)
		}

		var b strings.Builder
		b.WriteString(string(runes[:deleteStart]))
		b.WriteString(replacement)
		b.WriteString(string(runes[deleteEnd:]))
		return storage.StringValue(b.String())

	case "SPACE":
		if len(args) == 0 || args[0].IsNull || args[0].IVal <= 0 {
			return storage.StringValue("")
		}
		n := int(args[0].IVal)
		if n > 8000 {
			n = 8000 // defensive upper bound
		}
		return storage.StringValue(strings.Repeat(" ", n))

	case "REPLICATE":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull || args[1].IVal <= 0 {
			return storage.StringValue("")
		}
		str := valToString(args[0])
		n := int(args[1].IVal)
		if n > 8000 {
			n = 8000
		}
		return storage.StringValue(strings.Repeat(str, n))

	// ----------------------------------------------------
	// 3. Mathematical Functions
	// ----------------------------------------------------
	case "ABS":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			absVal := math.Abs(n)
			if absVal == math.Trunc(absVal) {
				return storage.IntValue(int64(absVal))
			}
			return storage.FloatValue(absVal)
		}
		return storage.NullValue(storage.TypeFloat)

	case "ROUND":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			decimals := 0
			if len(args) >= 2 && !args[1].IsNull {
				decimals = int(args[1].IVal)
			}
			factor := math.Pow(10, float64(decimals))
			rounded := math.Round(n*factor) / factor
			if decimals == 0 {
				return storage.IntValue(int64(rounded))
			}
			return storage.FloatValue(rounded)
		}
		return storage.NullValue(storage.TypeFloat)

	case "CEILING", "CEIL":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.IntValue(int64(math.Ceil(n)))
		}
		return storage.NullValue(storage.TypeInt)

	case "FLOOR":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.IntValue(int64(math.Floor(n)))
		}
		return storage.NullValue(storage.TypeInt)

	case "POWER":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		b, ok1 := toFloatNum(args[0])
		e, ok2 := toFloatNum(args[1])
		if !ok1 || !ok2 {
			return storage.NullValue(storage.TypeFloat)
		}
		res := math.Pow(b, e)
		if res == math.Trunc(res) && math.Abs(res) < float64(math.MaxInt64) {
			return storage.IntValue(int64(res))
		}
		return storage.FloatValue(res)

	case "SQRT":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			if n < 0 {
				return storage.NullValue(storage.TypeFloat)
			}
			return storage.FloatValue(math.Sqrt(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "SIGN":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		if n, ok := toFloatNum(args[0]); ok {
			if n > 0 {
				return storage.IntValue(1)
			} else if n < 0 {
				return storage.IntValue(-1)
			}
			return storage.IntValue(0)
		}
		return storage.NullValue(storage.TypeInt)

	case "PI":
		return storage.FloatValue(math.Pi)

	// ----------------------------------------------------
	// 4. Conditional & Logical Functions
	// ----------------------------------------------------
	case "ISNULL", "COALESCE":
		for _, a := range args {
			if !a.IsNull {
				return a
			}
		}
		return storage.NullValue(storage.TypeUnknown)

	case "IIF":
		if len(args) < 3 {
			return storage.NullValue(storage.TypeUnknown)
		}
		cond := isTruthyStorageValue(args[0])
		if cond {
			return args[1]
		}
		return args[2]

	case "CHOOSE":
		if len(args) < 2 || args[0].IsNull {
			return storage.NullValue(storage.TypeUnknown)
		}
		idx := int(args[0].IVal)
		if idx >= 1 && idx < len(args) {
			return args[idx]
		}
		return storage.NullValue(storage.TypeUnknown)

	// ----------------------------------------------------
	// 5. JSON Functions
	// ----------------------------------------------------
	case "JSON_VALUE":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		doc := valToString(args[0])
		path := valToString(args[1])
		val, ok := storage.JSONValueByPath(doc, path)
		if !ok || val == nil {
			return storage.NullValue(storage.TypeVarChar)
		}
		switch v := val.(type) {
		case string:
			return storage.StringValue(v)
		case int64:
			return storage.IntValue(v)
		case float64:
			return storage.FloatValue(v)
		case bool:
			return storage.BoolValue(v)
		default:
			return storage.StringValue(fmt.Sprintf("%v", v))
		}

	case "JSON_QUERY":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeJSON)
		}
		doc := valToString(args[0])
		path := valToString(args[1])
		val, ok := storage.JSONQueryByPath(doc, path)
		if !ok || val == "" {
			return storage.NullValue(storage.TypeJSON)
		}
		return storage.JSONValue(val)

	// ----------------------------------------------------
	// 6. System Functions & Metadata
	// ----------------------------------------------------
	case "NEWID":
		return storage.StringValue(generateUUIDv4())

	default:
		if len(args) > 0 {
			return args[0]
		}
		return storage.NullValue(storage.TypeUnknown)
	}
}

// IsBuiltinFunction reports whether the named function is a built-in scalar function.
func IsBuiltinFunction(name string) bool {
	switch strings.ToUpper(cleanBuiltinName(name)) {
	case "GETDATE", "NOW", "GETUTCDATE", "SYSDATETIME",
		"YEAR", "MONTH", "DAY", "DATEPART", "DATEDIFF", "DATEADD",
		"CONCAT", "LEN", "LENGTH", "UPPER", "LOWER", "TRIM", "LTRIM", "RTRIM",
		"LEFT", "RIGHT", "SUBSTRING", "REPLACE", "REVERSE", "CHARINDEX", "SPACE", "REPLICATE",
		"ABS", "ROUND", "CEILING", "CEIL", "FLOOR", "POWER", "SQRT", "SIGN", "PI",
		"ISNULL", "COALESCE", "IIF", "CHOOSE",
		"JSON_VALUE", "JSON_QUERY",
		"NEWID", "SCOPE_IDENTITY":
		return true
	default:
		return false
	}
}

func cleanBuiltinName(name string) string {
	n := strings.TrimSpace(name)
	if dot := strings.LastIndex(n, "."); dot != -1 {
		n = n[dot+1:]
	}
	return strings.Trim(n, "[]\"`")
}

func valToString(v storage.Value) string {
	if v.IsNull {
		return ""
	}
	switch v.Type {
	case storage.TypeVarChar, storage.TypeJSON:
		return v.SVal
	case storage.TypeInt:
		return strconv.FormatInt(v.IVal, 10)
	case storage.TypeFloat:
		return strconv.FormatFloat(v.FVal, 'f', -1, 64)
	case storage.TypeBool:
		if v.BVal {
			return "1"
		}
		return "0"
	case storage.TypeDateTime:
		return v.TVal.Format("2006-01-02 15:04:05")
	default:
		return v.String()
	}
}

func toFloatNum(v storage.Value) (float64, bool) {
	if v.IsNull {
		return 0, false
	}
	switch v.Type {
	case storage.TypeInt:
		return float64(v.IVal), true
	case storage.TypeFloat:
		return v.FVal, true
	case storage.TypeVarChar:
		f, err := strconv.ParseFloat(strings.TrimSpace(v.SVal), 64)
		return f, err == nil
	case storage.TypeBool:
		if v.BVal {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

func isTruthyStorageValue(v storage.Value) bool {
	if v.IsNull {
		return false
	}
	switch v.Type {
	case storage.TypeBool:
		return v.BVal
	case storage.TypeInt:
		return v.IVal != 0
	case storage.TypeFloat:
		return v.FVal != 0
	case storage.TypeVarChar:
		s := strings.TrimSpace(v.SVal)
		return len(s) > 0 && !strings.EqualFold(s, "false") && s != "0"
	default:
		return true
	}
}

func parseDateValue(v storage.Value) (time.Time, bool) {
	if v.IsNull {
		return time.Time{}, false
	}
	if v.Type == storage.TypeDateTime && !v.TVal.IsZero() {
		return v.TVal, true
	}
	str := strings.TrimSpace(valToString(v))
	if str == "" {
		return time.Time{}, false
	}
	for _, layout := range commonDateFormats {
		if t, err := time.ParseInLocation(layout, str, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func normalizeDatePart(dp string) string {
	s := strings.ToLower(strings.TrimSpace(dp))
	switch s {
	case "year", "yy", "yyyy":
		return "year"
	case "quarter", "qq", "q":
		return "quarter"
	case "month", "mm", "m":
		return "month"
	case "day", "dd", "d", "dayofyear", "dy", "y":
		return "day"
	case "week", "wk", "ww":
		return "week"
	case "hour", "hh":
		return "hour"
	case "minute", "mi", "n":
		return "minute"
	case "second", "ss", "s":
		return "second"
	case "millisecond", "ms":
		return "millisecond"
	default:
		return s
	}
}

func evalDatePart(part string, t time.Time) storage.Value {
	switch part {
	case "year":
		return storage.IntValue(int64(t.Year()))
	case "quarter":
		return storage.IntValue(int64((t.Month()-1)/3 + 1))
	case "month":
		return storage.IntValue(int64(t.Month()))
	case "day":
		return storage.IntValue(int64(t.Day()))
	case "week":
		_, w := t.ISOWeek()
		return storage.IntValue(int64(w))
	case "hour":
		return storage.IntValue(int64(t.Hour()))
	case "minute":
		return storage.IntValue(int64(t.Minute()))
	case "second":
		return storage.IntValue(int64(t.Second()))
	case "millisecond":
		return storage.IntValue(int64(t.Nanosecond() / 1e6))
	default:
		return storage.NullValue(storage.TypeInt)
	}
}

func evalDateDiff(part string, t1, t2 time.Time) storage.Value {
	switch part {
	case "year":
		return storage.IntValue(int64(t2.Year() - t1.Year()))
	case "quarter":
		q1 := (t1.Year() * 4) + int((t1.Month()-1)/3)
		q2 := (t2.Year() * 4) + int((t2.Month()-1)/3)
		return storage.IntValue(int64(q2 - q1))
	case "month":
		m1 := t1.Year()*12 + int(t1.Month())
		m2 := t2.Year()*12 + int(t2.Month())
		return storage.IntValue(int64(m2 - m1))
	case "day":
		diff := t2.Sub(t1)
		return storage.IntValue(int64(diff.Hours() / 24))
	case "week":
		diff := t2.Sub(t1)
		return storage.IntValue(int64(diff.Hours() / (24 * 7)))
	case "hour":
		diff := t2.Sub(t1)
		return storage.IntValue(int64(diff.Hours()))
	case "minute":
		diff := t2.Sub(t1)
		return storage.IntValue(int64(diff.Minutes()))
	case "second":
		diff := t2.Sub(t1)
		return storage.IntValue(int64(diff.Seconds()))
	case "millisecond":
		diff := t2.Sub(t1)
		return storage.IntValue(diff.Milliseconds())
	default:
		return storage.NullValue(storage.TypeInt)
	}
}

func evalDateAdd(part string, n int64, t time.Time) time.Time {
	switch part {
	case "year":
		return t.AddDate(int(n), 0, 0)
	case "quarter":
		return t.AddDate(0, int(n*3), 0)
	case "month":
		return t.AddDate(0, int(n), 0)
	case "day":
		return t.AddDate(0, 0, int(n))
	case "week":
		return t.AddDate(0, 0, int(n*7))
	case "hour":
		return t.Add(time.Duration(n) * time.Hour)
	case "minute":
		return t.Add(time.Duration(n) * time.Minute)
	case "second":
		return t.Add(time.Duration(n) * time.Second)
	case "millisecond":
		return t.Add(time.Duration(n) * time.Millisecond)
	default:
		return t
	}
}

func generateUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant 10
	return strings.ToUpper(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}
