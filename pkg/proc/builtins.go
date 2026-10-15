package proc

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"math"
	"math/bits"
	mathrand "math/rand/v2"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"sqlion/pkg/storage"
)

var commonDateFormats = []string{
	"2006-01-02 15:04:05.999999999 -07:00",
	"2006-01-02 15:04:05.9999999 -07:00",
	"2006-01-02 15:04:05 -07:00",
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

// ScalarFunction represents an in-process native Go scalar function callback.
type ScalarFunction func(args []storage.Value) storage.Value

var customScalarFunctions sync.Map

// RegisterScalarFunction registers an in-process custom Go function as a user-defined scalar function.
// It can be invoked directly in SQL queries and stored procedures with zero FFI boundary overhead.
func RegisterScalarFunction(name string, fn ScalarFunction) {
	upper := strings.ToUpper(cleanBuiltinName(name))
	customScalarFunctions.Store(upper, fn)
}

// UnregisterScalarFunction unregisters a custom Go scalar function.
func UnregisterScalarFunction(name string) {
	upper := strings.ToUpper(cleanBuiltinName(name))
	customScalarFunctions.Delete(upper)
}

// HasScalarFunction checks if a custom scalar function is registered.
func HasScalarFunction(name string) bool {
	upper := strings.ToUpper(cleanBuiltinName(name))
	_, ok := customScalarFunctions.Load(upper)
	return ok
}

// EvalBuiltin evaluates standard T-SQL scalar functions and registered native Go scalar UDFs.
func EvalBuiltin(name string, args []storage.Value) storage.Value {
	upper := strings.ToUpper(cleanBuiltinName(name))

	// 1. Fast-path lookup for registered native Go scalar functions
	if val, ok := customScalarFunctions.Load(upper); ok {
		if fn, isFn := val.(ScalarFunction); isFn && fn != nil {
			return fn(args)
		}
	}

	switch upper {
	// ----------------------------------------------------
	// 1. Date & Time Functions
	// ----------------------------------------------------
	case "GETDATE", "NOW", "CURRENT_TIMESTAMP":
		return storage.StringValue(time.Now().Format("2006-01-02 15:04:05"))

	case "GETUTCDATE":
		return storage.StringValue(time.Now().UTC().Format("2006-01-02 15:04:05"))

	case "SYSDATETIME":
		return storage.StringValue(time.Now().Format("2006-01-02 15:04:05.0000000"))

	case "SYSUTCDATETIME":
		return storage.StringValue(time.Now().UTC().Format("2006-01-02 15:04:05.0000000"))

	case "SYSDATETIMEOFFSET":
		return storage.StringValue(time.Now().Format("2006-01-02 15:04:05.0000000 -07:00"))

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

	case "DATEPART", "DATE_PART", "EXTRACT":
		if len(args) < 2 || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		part := normalizeDatePart(valToString(args[0]))
		t, ok := parseDateValue(args[1])
		if !ok {
			return storage.NullValue(storage.TypeInt)
		}
		return evalDatePart(part, t)

	case "DATENAME":
		if len(args) < 2 || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		part := normalizeDatePart(valToString(args[0]))
		t, ok := parseDateValue(args[1])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		return evalDateName(part, t)

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

	case "DATEDIFF_BIG":
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

	case "DATETRUNC", "DATE_TRUNC":
		if len(args) < 2 || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		part := normalizeDatePart(valToString(args[0]))
		t, ok := parseDateValue(args[1])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		res := evalDateTrunc(part, t)
		return storage.StringValue(res.Format("2006-01-02 15:04:05"))

	case "DATE_BUCKET", "DATEBUCKET":
		if len(args) < 3 || args[2].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		part := normalizeDatePart(valToString(args[0]))
		width := args[1].IVal
		if args[1].Type == storage.TypeFloat {
			width = int64(args[1].FVal)
		} else if args[1].Type == storage.TypeVarChar {
			if w, err := strconv.ParseInt(strings.TrimSpace(args[1].SVal), 10, 64); err == nil {
				width = w
			}
		}
		t, ok := parseDateValue(args[2])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		var origin time.Time
		hasOrigin := false
		if len(args) >= 4 && !args[3].IsNull {
			if o, ok := parseDateValue(args[3]); ok {
				origin = o
				hasOrigin = true
			}
		}
		res := evalDateBucket(part, width, t, origin, hasOrigin)
		return storage.StringValue(res.Format("2006-01-02 15:04:05"))

	case "EOMONTH":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		t, ok := parseDateValue(args[0])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		monthsToAdd := 0
		if len(args) >= 2 && !args[1].IsNull {
			if args[1].Type == storage.TypeInt {
				monthsToAdd = int(args[1].IVal)
			} else if args[1].Type == storage.TypeFloat {
				monthsToAdd = int(args[1].FVal)
			} else if args[1].Type == storage.TypeVarChar {
				if m, err := strconv.Atoi(strings.TrimSpace(args[1].SVal)); err == nil {
					monthsToAdd = m
				}
			}
		}
		target := time.Date(t.Year(), t.Month()+time.Month(monthsToAdd)+1, 1, 0, 0, 0, 0, t.Location())
		lastDay := target.AddDate(0, 0, -1)
		return storage.StringValue(lastDay.Format("2006-01-02"))

	case "ISDATE":
		if len(args) == 0 || args[0].IsNull {
			return storage.IntValue(0)
		}
		_, ok := parseDateValue(args[0])
		if ok {
			return storage.IntValue(1)
		}
		return storage.IntValue(0)

	case "DATEFROMPARTS":
		if len(args) < 3 || args[0].IsNull || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		y, m, d := int(args[0].IVal), int(args[1].IVal), int(args[2].IVal)
		if y < 1 || m < 1 || m > 12 || d < 1 || d > 31 {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue(fmt.Sprintf("%04d-%02d-%02d", y, m, d))

	case "TIMEFROMPARTS":
		if len(args) < 3 || args[0].IsNull || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		h, m, s := int(args[0].IVal), int(args[1].IVal), int(args[2].IVal)
		frac := int64(0)
		prec := int64(0)
		if len(args) >= 4 && !args[3].IsNull {
			frac = args[3].IVal
		}
		if len(args) >= 5 && !args[4].IsNull {
			prec = args[4].IVal
		}
		if prec > 0 {
			return storage.StringValue(fmt.Sprintf("%02d:%02d:%02d.%0*d", h, m, s, int(prec), frac))
		}
		return storage.StringValue(fmt.Sprintf("%02d:%02d:%02d", h, m, s))

	case "DATETIMEFROMPARTS":
		if len(args) < 7 || args[0].IsNull || args[1].IsNull || args[2].IsNull || args[3].IsNull || args[4].IsNull || args[5].IsNull || args[6].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		y, m, d := int(args[0].IVal), int(args[1].IVal), int(args[2].IVal)
		h, min, s, ms := int(args[3].IVal), int(args[4].IVal), int(args[5].IVal), int(args[6].IVal)
		return storage.StringValue(fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d.%03d", y, m, d, h, min, s, ms))

	case "SMALLDATETIMEFROMPARTS":
		if len(args) < 5 {
			return storage.NullValue(storage.TypeVarChar)
		}
		for i := 0; i < 5; i++ {
			if args[i].IsNull {
				return storage.NullValue(storage.TypeVarChar)
			}
		}
		y, m, d := int(args[0].IVal), int(args[1].IVal), int(args[2].IVal)
		hh, mm := int(args[3].IVal), int(args[4].IVal)
		if m < 1 || m > 12 || d < 1 || d > 31 || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
			return storage.NullValue(storage.TypeVarChar)
		}
		t := time.Date(y, time.Month(m), d, hh, mm, 0, 0, time.UTC)
		return storage.StringValue(t.Format("2006-01-02 15:04:00"))

	case "SWITCHOFFSET":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		t, ok := parseDateValue(args[0])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		loc := parseOffsetLocation(valToString(args[1]))
		shifted := t.In(loc)
		return storage.StringValue(shifted.Format("2006-01-02 15:04:05.0000000 -07:00"))

	case "TODATETIMEOFFSET":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		t, ok := parseDateValue(args[0])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		loc := parseOffsetLocation(valToString(args[1]))
		res := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
		return storage.StringValue(res.Format("2006-01-02 15:04:05.0000000 -07:00"))

	case "TO_DATE":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		if t, ok := parseDateValue(args[0]); ok {
			return storage.StringValue(t.Format("2006-01-02"))
		}
		return storage.NullValue(storage.TypeVarChar)

	case "TO_TIMESTAMP":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		if n, ok := toFloatNum(args[0]); ok {
			sec := int64(n)
			nsec := int64((n - float64(sec)) * 1e9)
			t := time.Unix(sec, nsec).UTC()
			return storage.StringValue(t.Format("2006-01-02 15:04:05"))
		}
		if t, ok := parseDateValue(args[0]); ok {
			return storage.StringValue(t.Format("2006-01-02 15:04:05"))
		}
		return storage.NullValue(storage.TypeVarChar)

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

	case "CONCAT_WS":
		if len(args) < 2 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		sep := valToString(args[0])
		var parts []string
		for _, a := range args[1:] {
			if !a.IsNull {
				parts = append(parts, valToString(a))
			}
		}
		return storage.StringValue(strings.Join(parts, sep))

	case "QUOTENAME", "QUOTE_IDENT", "QUOTE_LITERAL":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		str := valToString(args[0])
		quoteChar := "["
		if upper == "QUOTE_IDENT" {
			quoteChar = "\""
		} else if upper == "QUOTE_LITERAL" {
			quoteChar = "'"
		}
		if len(args) >= 2 && !args[1].IsNull {
			q := valToString(args[1])
			if len(q) > 0 {
				quoteChar = string([]rune(q)[0])
			}
		}
		var openChar, closeChar string
		switch quoteChar {
		case "[", "]":
			openChar, closeChar = "[", "]"
		case "'":
			openChar, closeChar = "'", "'"
		case "\"":
			openChar, closeChar = "\"", "\""
		case "(", ")":
			openChar, closeChar = "(", ")"
		case "{", "}":
			openChar, closeChar = "{", "}"
		case "<", ">":
			openChar, closeChar = "<", ">"
		default:
			return storage.NullValue(storage.TypeVarChar)
		}
		escaped := strings.ReplaceAll(str, closeChar, closeChar+closeChar)
		return storage.StringValue(openChar + escaped + closeChar)

	case "ASCII":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		str := valToString(args[0])
		if len(str) == 0 {
			return storage.NullValue(storage.TypeInt)
		}
		return storage.IntValue(int64(str[0]))

	case "CHAR", "CHR":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		code := int(args[0].IVal)
		if args[0].Type == storage.TypeFloat {
			code = int(args[0].FVal)
		}
		if code < 0 || code > 255 {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue(string([]byte{byte(code)}))

	case "LEN", "LENGTH":
		if len(args) == 0 || args[0].IsNull {
			return storage.IntValue(0)
		}
		return storage.IntValue(int64(len([]rune(valToString(args[0])))))

	case "DATALENGTH":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		switch args[0].Type {
		case storage.TypeVarChar, storage.TypeJSON:
			return storage.IntValue(int64(len(args[0].SVal)))
		case storage.TypeInt, storage.TypeFloat, storage.TypeDateTime:
			return storage.IntValue(8)
		case storage.TypeDecimal, storage.TypeUUID:
			return storage.IntValue(16)
		case storage.TypeBool:
			return storage.IntValue(1)
		default:
			return storage.IntValue(int64(len(valToString(args[0]))))
		}

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
		if len(args) == 1 {
			return storage.StringValue(strings.TrimSpace(valToString(args[0])))
		}
		// TRIM(cutset, target) or TRIM(target, cutset)
		arg0 := valToString(args[0])
		arg1 := valToString(args[1])
		// Check if first arg is cutset (e.g. TRIM('.,! ' FROM target))
		return storage.StringValue(strings.Trim(arg1, arg0))

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

	case "SUBSTRING", "SUBSTR":
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

	case "REPLICATE", "REPEAT":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull || args[1].IVal <= 0 {
			return storage.StringValue("")
		}
		str := valToString(args[0])
		n := int(args[1].IVal)
		if n > 8000 {
			n = 8000
		}
		return storage.StringValue(strings.Repeat(str, n))

	case "GREP", "REGEXP_LIKE":
		// GREP(text, pattern) or REGEXP_LIKE(text, pattern): returns true if pattern matches text.
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.BoolValue(false)
		}
		text := valToString(args[0])
		pattern := valToString(args[1])
		if pattern == "" {
			return storage.BoolValue(true)
		}
		matched, err := regexp.MatchString(pattern, text)
		if err != nil {
			// Fallback to case-insensitive literal substring match on malformed regex
			matched = strings.Contains(strings.ToLower(text), strings.ToLower(pattern))
		}
		return storage.BoolValue(matched)

	case "AWK", "SPLIT_PART":
		// AWK(text, delimiter, field_index) or AWK(text, field_index):
		// 1-based indexing ($1, $2, ...). If delimiter is omitted or empty, splits on whitespace.
		if len(args) < 2 || args[0].IsNull {
			return storage.StringValue("")
		}
		text := valToString(args[0])
		var delim string
		var fieldIdx int

		if len(args) == 2 {
			// AWK(text, field_index) -> default whitespace delimiter
			delim = ""
			fieldIdx = int(args[1].IVal)
		} else {
			// AWK(text, delimiter, field_index)
			delim = valToString(args[1])
			fieldIdx = int(args[2].IVal)
		}

		if fieldIdx <= 0 {
			return storage.StringValue("")
		}

		var fields []string
		if delim == "" || delim == " " {
			fields = strings.Fields(text)
		} else {
			fields = strings.Split(text, delim)
		}

		if fieldIdx > len(fields) {
			return storage.StringValue("")
		}
		return storage.StringValue(fields[fieldIdx-1])

	case "PATINDEX":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		pattern := valToString(args[0])
		target := valToString(args[1])
		return storage.IntValue(ComputePatindex(pattern, target))

	case "UNICODE":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		s := valToString(args[0])
		if len(s) == 0 {
			return storage.NullValue(storage.TypeInt)
		}
		runes := []rune(s)
		return storage.IntValue(int64(runes[0]))

	case "NCHAR":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		code := args[0].IVal
		if args[0].Type == storage.TypeFloat {
			code = int64(args[0].FVal)
		}
		if code < 0 || code > unicode.MaxRune {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue(string(rune(code)))

	case "SOUNDEX":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue(computeSoundex(valToString(args[0])))

	case "DIFFERENCE":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		s1 := computeSoundex(valToString(args[0]))
		s2 := computeSoundex(valToString(args[1]))
		score := 0
		for i := 0; i < 4 && i < len(s1) && i < len(s2); i++ {
			if s1[i] == s2[i] {
				score++
			}
		}
		return storage.IntValue(int64(score))

	case "TRANSLATE":
		if len(args) < 3 || args[0].IsNull || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		input := valToString(args[0])
		fromChars := []rune(valToString(args[1]))
		toChars := []rune(valToString(args[2]))
		if len(fromChars) != len(toChars) {
			return storage.NullValue(storage.TypeVarChar)
		}
		mapping := make(map[rune]rune, len(fromChars))
		for i, r := range fromChars {
			mapping[r] = toChars[i]
		}
		var sb strings.Builder
		for _, r := range input {
			if tr, ok := mapping[r]; ok {
				sb.WriteRune(tr)
			} else {
				sb.WriteRune(r)
			}
		}
		return storage.StringValue(sb.String())

	case "FORMAT":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		fmtStr := valToString(args[1])
		if t, ok := parseDateValue(args[0]); ok {
			return storage.StringValue(formatDateCustom(t, fmtStr))
		}
		if num, ok := toFloatNum(args[0]); ok {
			return storage.StringValue(formatNumberCustom(num, fmtStr))
		}
		return storage.StringValue(valToString(args[0]))

	case "STRPOS":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.IntValue(0)
		}
		target := valToString(args[0])
		sub := valToString(args[1])
		if sub == "" {
			return storage.IntValue(1)
		}
		idx := strings.Index(target, sub)
		if idx == -1 {
			return storage.IntValue(0)
		}
		runeOffset := len([]rune(target[:idx]))
		return storage.IntValue(int64(runeOffset + 1))

	case "MD5":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		data := []byte(valToString(args[0]))
		h := md5.Sum(data)
		return storage.StringValue(hex.EncodeToString(h[:]))

	case "LPAD":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		str := valToString(args[0])
		targetLen := int(args[1].IVal)
		if args[1].Type == storage.TypeFloat {
			targetLen = int(args[1].FVal)
		}
		if targetLen <= 0 {
			return storage.StringValue("")
		}
		pad := " "
		if len(args) >= 3 && !args[2].IsNull {
			pad = valToString(args[2])
			if pad == "" {
				pad = " "
			}
		}
		runes := []rune(str)
		if len(runes) >= targetLen {
			return storage.StringValue(string(runes[:targetLen]))
		}
		padRunes := []rune(pad)
		needed := targetLen - len(runes)
		var sb strings.Builder
		for i := 0; i < needed; i++ {
			sb.WriteRune(padRunes[i%len(padRunes)])
		}
		sb.WriteString(str)
		return storage.StringValue(sb.String())

	case "RPAD":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		str := valToString(args[0])
		targetLen := int(args[1].IVal)
		if args[1].Type == storage.TypeFloat {
			targetLen = int(args[1].FVal)
		}
		if targetLen <= 0 {
			return storage.StringValue("")
		}
		pad := " "
		if len(args) >= 3 && !args[2].IsNull {
			pad = valToString(args[2])
			if pad == "" {
				pad = " "
			}
		}
		runes := []rune(str)
		if len(runes) >= targetLen {
			return storage.StringValue(string(runes[:targetLen]))
		}
		padRunes := []rune(pad)
		needed := targetLen - len(runes)
		var sb strings.Builder
		sb.WriteString(str)
		for i := 0; i < needed; i++ {
			sb.WriteRune(padRunes[i%len(padRunes)])
		}
		return storage.StringValue(sb.String())

	case "OCTET_LENGTH":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		return storage.IntValue(int64(len([]byte(valToString(args[0])))))

	case "BIT_LENGTH":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		return storage.IntValue(int64(len([]byte(valToString(args[0]))) * 8))

	case "INITCAP":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		s := valToString(args[0])
		var sb strings.Builder
		newWord := true
		for _, r := range s {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				if newWord {
					sb.WriteRune(unicode.ToUpper(r))
					newWord = false
				} else {
					sb.WriteRune(unicode.ToLower(r))
				}
			} else {
				sb.WriteRune(r)
				newWord = true
			}
		}
		return storage.StringValue(sb.String())

	case "TO_CHAR":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		return EvalBuiltin("FORMAT", args)

	case "PARSENAME":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		piece := args[1].IVal
		if piece < 1 || piece > 4 {
			return storage.NullValue(storage.TypeVarChar)
		}
		objStr := strings.TrimSpace(valToString(args[0]))
		parts := parseObjectPieces(objStr)
		if parts == nil {
			return storage.NullValue(storage.TypeVarChar)
		}
		idx := len(parts) - int(piece)
		if idx < 0 || idx >= len(parts) {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue(parts[idx])

	case "STRING_ESCAPE":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		escapeType := "json"
		if len(args) >= 2 && !args[1].IsNull {
			escapeType = strings.ToLower(strings.TrimSpace(valToString(args[1])))
		}
		str := valToString(args[0])
		if escapeType != "json" {
			return storage.StringValue(str)
		}
		var b strings.Builder
		for _, r := range str {
			switch r {
			case '\\':
				b.WriteString(`\\`)
			case '"':
				b.WriteString(`\"`)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if r < 0x20 {
					b.WriteString(fmt.Sprintf(`\u%04x`, r))
				} else {
					b.WriteRune(r)
				}
			}
		}
		return storage.StringValue(b.String())

	case "FORMATMESSAGE":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		fmtStr := valToString(args[0])
		paramIdx := 1
		var sb strings.Builder
		for i := 0; i < len(fmtStr); i++ {
			if fmtStr[i] == '%' && i+1 < len(fmtStr) {
				spec := fmtStr[i+1]
				if spec == '%' {
					sb.WriteByte('%')
					i++
					continue
				}
				if paramIdx < len(args) {
					argVal := args[paramIdx]
					paramIdx++
					if argVal.IsNull {
						sb.WriteString("(null)")
					} else {
						sb.WriteString(valToString(argVal))
					}
					i++
					continue
				}
			}
			sb.WriteByte(fmtStr[i])
		}
		return storage.StringValue(sb.String())

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

	case "SQUARE":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if args[0].Type == storage.TypeInt {
			v := args[0].IVal
			return storage.IntValue(v * v)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(n * n)
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

	case "EXP":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(math.Exp(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "LOG":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		n, ok := toFloatNum(args[0])
		if !ok || n <= 0 {
			return storage.NullValue(storage.TypeFloat)
		}
		if len(args) >= 2 && !args[1].IsNull {
			base, okBase := toFloatNum(args[1])
			if !okBase || base <= 0 || base == 1 {
				return storage.NullValue(storage.TypeFloat)
			}
			return storage.FloatValue(math.Log(n) / math.Log(base))
		}
		return storage.FloatValue(math.Log(n))

	case "LOG10":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		n, ok := toFloatNum(args[0])
		if !ok || n <= 0 {
			return storage.NullValue(storage.TypeFloat)
		}
		return storage.FloatValue(math.Log10(n))

	case "SIN":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(math.Sin(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "COS":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(math.Cos(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "TAN":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(math.Tan(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "COT":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			tanVal := math.Tan(n)
			if tanVal == 0 {
				return storage.NullValue(storage.TypeFloat)
			}
			return storage.FloatValue(1.0 / tanVal)
		}
		return storage.NullValue(storage.TypeFloat)

	case "ASIN":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			if n < -1 || n > 1 {
				return storage.NullValue(storage.TypeFloat)
			}
			return storage.FloatValue(math.Asin(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "ACOS":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			if n < -1 || n > 1 {
				return storage.NullValue(storage.TypeFloat)
			}
			return storage.FloatValue(math.Acos(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "ATAN":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(math.Atan(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "ATN2":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		y, okY := toFloatNum(args[0])
		x, okX := toFloatNum(args[1])
		if !okY || !okX {
			return storage.NullValue(storage.TypeFloat)
		}
		return storage.FloatValue(math.Atan2(y, x))

	case "DEGREES":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(n * 180.0 / math.Pi)
		}
		return storage.NullValue(storage.TypeFloat)

	case "RADIANS":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(n * math.Pi / 180.0)
		}
		return storage.NullValue(storage.TypeFloat)

	case "RAND", "RANDOM":
		if len(args) > 0 && !args[0].IsNull {
			seedVal := args[0].IVal
			if args[0].Type == storage.TypeFloat {
				seedVal = int64(args[0].FVal)
			}
			r := mathrand.New(mathrand.NewPCG(uint64(seedVal), uint64(seedVal*6364136223846793005+1)))
			return storage.FloatValue(r.Float64())
		}
		return storage.FloatValue(mathrand.Float64())

	case "BIT_COUNT":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		val := uint64(args[0].IVal)
		if args[0].Type == storage.TypeFloat {
			val = uint64(int64(args[0].FVal))
		}
		return storage.IntValue(int64(bits.OnesCount64(val)))

	case "GET_BIT":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		val := uint64(args[0].IVal)
		offset := args[1].IVal
		if offset < 0 || offset > 63 {
			return storage.NullValue(storage.TypeInt)
		}
		bit := (val >> offset) & 1
		return storage.IntValue(int64(bit))

	case "SET_BIT":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		val := uint64(args[0].IVal)
		offset := args[1].IVal
		if offset < 0 || offset > 63 {
			return storage.NullValue(storage.TypeInt)
		}
		bitVal := int64(1)
		if len(args) >= 3 && !args[2].IsNull {
			bitVal = args[2].IVal
		}
		if bitVal == 0 {
			val &^= (1 << offset)
		} else {
			val |= (1 << offset)
		}
		return storage.IntValue(int64(val))

	case "LEFT_SHIFT":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		val := args[0].IVal
		shift := args[1].IVal
		if shift < 0 || shift > 63 {
			return storage.NullValue(storage.TypeInt)
		}
		return storage.IntValue(val << shift)

	case "RIGHT_SHIFT":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		val := args[0].IVal
		shift := args[1].IVal
		if shift < 0 || shift > 63 {
			return storage.NullValue(storage.TypeInt)
		}
		return storage.IntValue(val >> shift)

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

	case "NULLIF":
		if len(args) < 2 {
			return storage.NullValue(storage.TypeUnknown)
		}
		if args[0].IsNull {
			return args[0]
		}
		if !args[1].IsNull && args[0].Equal(args[1]) {
			return storage.NullValue(args[0].Type)
		}
		return args[0]

	case "ISNUMERIC":
		if len(args) == 0 || args[0].IsNull {
			return storage.IntValue(0)
		}
		switch args[0].Type {
		case storage.TypeInt, storage.TypeFloat, storage.TypeDecimal:
			return storage.IntValue(1)
		case storage.TypeVarChar:
			s := strings.TrimSpace(args[0].SVal)
			if s == "" || strings.EqualFold(s, "nan") || strings.EqualFold(s, "infinity") || strings.EqualFold(s, "+infinity") || strings.EqualFold(s, "-infinity") || strings.EqualFold(s, "inf") || strings.EqualFold(s, "+inf") || strings.EqualFold(s, "-inf") {
				return storage.IntValue(0)
			}
			if _, err := strconv.ParseFloat(s, 64); err == nil {
				return storage.IntValue(1)
			}
			return storage.IntValue(0)
		default:
			return storage.IntValue(0)
		}

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

	case "GREATEST":
		if len(args) == 0 {
			return storage.NullValue(storage.TypeUnknown)
		}
		var maxVal storage.Value
		hasVal := false
		for _, a := range args {
			if a.IsNull {
				continue
			}
			if !hasVal {
				maxVal = a
				hasVal = true
			} else if compareValues(a, maxVal) > 0 {
				maxVal = a
			}
		}
		if !hasVal {
			return storage.NullValue(storage.TypeUnknown)
		}
		return maxVal

	case "LEAST":
		if len(args) == 0 {
			return storage.NullValue(storage.TypeUnknown)
		}
		var minVal storage.Value
		hasVal := false
		for _, a := range args {
			if a.IsNull {
				continue
			}
			if !hasVal {
				minVal = a
				hasVal = true
			} else if compareValues(a, minVal) < 0 {
				minVal = a
			}
		}
		if !hasVal {
			return storage.NullValue(storage.TypeUnknown)
		}
		return minVal

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

	case "ISJSON":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		str := strings.TrimSpace(valToString(args[0]))
		if str == "" {
			return storage.IntValue(0)
		}
		var parsed any
		if err := json.Unmarshal([]byte(str), &parsed); err != nil {
			return storage.IntValue(0)
		}
		if len(args) >= 2 && !args[1].IsNull {
			opt := strings.ToUpper(strings.TrimSpace(valToString(args[1])))
			switch opt {
			case "ARRAY":
				if _, ok := parsed.([]any); !ok {
					return storage.IntValue(0)
				}
			case "OBJECT":
				if _, ok := parsed.(map[string]any); !ok {
					return storage.IntValue(0)
				}
			case "SCALAR":
				switch parsed.(type) {
				case map[string]any, []any:
					return storage.IntValue(0)
				}
			}
		}
		return storage.IntValue(1)

	case "JSON_MODIFY":
		if len(args) < 3 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		doc := valToString(args[0])
		path := valToString(args[1])
		newVal := args[2]
		res, err := modifyJSONByPath(doc, path, newVal)
		if err != nil {
			return storage.StringValue(doc)
		}
		return storage.StringValue(res)

	// ----------------------------------------------------
	// 6. System Functions & Metadata
	// ----------------------------------------------------
	case "NEWID":
		return storage.StringValue(generateUUIDv4())

	case "NEWID7", "UUIDV7", "UUID_GENERATE_V7":
		return storage.Value{Type: storage.TypeUUID, SVal: generateUUIDv7()}

	case "GEN_RANDOM_UUID":
		if len(args) > 0 && (args[0].IVal == 7 || args[0].SVal == "7") {
			return storage.Value{Type: storage.TypeUUID, SVal: generateUUIDv7()}
		}
		return storage.Value{Type: storage.TypeUUID, SVal: generateUUIDv4()}

	case "SERVERPROPERTY":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		prop := strings.ToUpper(strings.TrimSpace(valToString(args[0])))
		switch prop {
		case "PRODUCTVERSION":
			return storage.StringValue("15.0.2000.5")
		case "PRODUCTMAJORVERSION":
			return storage.StringValue("15")
		case "PRODUCTLEVEL":
			return storage.StringValue("RTM")
		case "EDITION":
			return storage.StringValue("Developer Edition (64-bit)")
		case "ENGINEEDITION":
			return storage.IntValue(3)
		case "INSTANCENAME":
			return storage.StringValue("MSSQLSERVER")
		case "MACHINENAME", "SERVERNAME":
			return storage.StringValue("SQLION")
		case "COLLATION":
			return storage.StringValue("SQL_Latin1_General_CP1_CI_AS")
		case "ISCLUSTERED", "ISHADRENABLED", "ISINTEGRATEDSECURITYONLY":
			return storage.IntValue(0)
		default:
			return storage.StringValue("")
		}

	// ----------------------------------------------------
	// 7. Error Handling & Execution Context (Group A)
	// ----------------------------------------------------
	case "ERROR_NUMBER", "@@ERROR":
		return storage.IntValue(0)

	case "ERROR_MESSAGE":
		return storage.StringValue("")

	case "ERROR_LINE":
		return storage.IntValue(0)

	case "ERROR_SEVERITY":
		return storage.IntValue(16)

	case "ERROR_STATE":
		return storage.IntValue(1)

	case "ERROR_PROCEDURE":
		return storage.NullValue(storage.TypeVarChar)

	// ----------------------------------------------------
	// 8. Metadata & Schema Introspection (Group D)
	// ----------------------------------------------------
	case "OBJECT_ID":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		objName := strings.TrimSpace(valToString(args[0]))
		if objName == "" {
			return storage.NullValue(storage.TypeInt)
		}
		clean := strings.ToLower(cleanBuiltinName(objName))
		id := int64(crc32.ChecksumIEEE([]byte(clean)))
		if id <= 0 {
			id = 1
		}
		return storage.IntValue(id)

	case "OBJECT_NAME":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.NullValue(storage.TypeVarChar)

	case "COL_LENGTH":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		return storage.IntValue(50)

	case "SCHEMA_NAME":
		schemaID := int64(1)
		if len(args) > 0 && !args[0].IsNull {
			schemaID = args[0].IVal
		}
		if schemaID == 1 {
			return storage.StringValue("dbo")
		}
		return storage.StringValue("dbo")

	case "SCHEMA_ID":
		sName := "dbo"
		if len(args) > 0 && !args[0].IsNull {
			sName = valToString(args[0])
		}
		if strings.EqualFold(cleanBuiltinName(sName), "dbo") {
			return storage.IntValue(1)
		}
		return storage.IntValue(1)

	case "DB_NAME":
		return storage.StringValue("SQLion")

	case "DB_ID":
		return storage.IntValue(1)

	case "APP_NAME":
		return storage.StringValue("SQLion")

	case "IDENT_CURRENT":
		return storage.IntValue(1)

	case "COL_NAME":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		colID := args[1].IVal
		if colID == 1 {
			return storage.StringValue("id")
		}
		return storage.StringValue(fmt.Sprintf("col%d", colID))

	case "HOST_NAME":
		h, err := os.Hostname()
		if err != nil || h == "" {
			return storage.StringValue("localhost")
		}
		return storage.StringValue(h)

	case "HOST_ID":
		return storage.IntValue(int64(os.Getpid()))

	case "CURRENT_USER", "USER_NAME":
		return storage.StringValue("dbo")

	case "SUSER_NAME", "SUSER_SNAME":
		return storage.StringValue("sa")

	case "USER_ID":
		return storage.IntValue(1)

	case "SUSER_ID":
		return storage.IntValue(1)

	case "@@VERSION", "VERSION":
		return storage.StringValue("Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64) / SQLion Embedded Engine")

	case "@@SERVERNAME":
		return storage.StringValue("SQLION")

	case "@@SPID", "SPID":
		return storage.IntValue(51)

	case "@@MAX_CONNECTIONS":
		return storage.IntValue(32767)

	case "@@LANGUAGE":
		return storage.StringValue("us_english")

	case "@@TRANCOUNT", "TRANCOUNT":
		return storage.IntValue(0)

	case "@@LOCK_TIMEOUT", "LOCK_TIMEOUT":
		return storage.IntValue(-1)

	case "@@OPTIONS", "OPTIONS":
		return storage.IntValue(5496)

	case "@@NESTLEVEL", "NESTLEVEL":
		return storage.IntValue(0)

	case "@@CPU_BUSY":
		return storage.IntValue(100)

	case "@@IO_BUSY":
		return storage.IntValue(50)

	case "@@IDLE":
		return storage.IntValue(1000)

	case "DATABASEPROPERTYEX":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		prop := strings.ToUpper(strings.TrimSpace(valToString(args[1])))
		switch prop {
		case "STATUS":
			return storage.StringValue("ONLINE")
		case "COLLATION":
			return storage.StringValue("SQL_Latin1_General_CP1_CI_AS")
		case "COMPARISONSTYLE":
			return storage.IntValue(196609)
		case "EDITION":
			return storage.StringValue("Developer")
		case "RECOVERY":
			return storage.StringValue("SIMPLE")
		case "VERSION":
			return storage.IntValue(904)
		case "USERACCESS":
			return storage.StringValue("MULTI_USER")
		case "ISAUTOCLOSE", "ISAUTOSHRINK", "ISINMEMCAPABLE":
			return storage.IntValue(0)
		default:
			return storage.StringValue("")
		}

	case "OBJECTPROPERTY":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		prop := strings.ToUpper(strings.TrimSpace(valToString(args[1])))
		switch prop {
		case "ISTABLE", "ISUSERTABLE", "ISPROCEDURE", "ISVIEW", "ISTRIGGER", "ISQUOTEDIDON", "ISANSINULLSON":
			return storage.IntValue(1)
		case "ISMSSHIPPED", "ISREPLICATED":
			return storage.IntValue(0)
		default:
			return storage.IntValue(0)
		}

	case "COLUMNPROPERTY":
		if len(args) < 3 || args[0].IsNull || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		colName := strings.ToLower(strings.TrimSpace(valToString(args[1])))
		prop := strings.ToUpper(strings.TrimSpace(valToString(args[2])))
		switch prop {
		case "ISIDENTITY":
			if colName == "id" {
				return storage.IntValue(1)
			}
			return storage.IntValue(0)
		case "ALLOWSNULL", "PRECISION", "SCALE":
			return storage.IntValue(1)
		default:
			return storage.IntValue(0)
		}

	case "INDEXPROPERTY":
		if len(args) < 3 || args[0].IsNull || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		prop := strings.ToUpper(strings.TrimSpace(valToString(args[2])))
		switch prop {
		case "ISUNIQUE", "ISCLUSTERED":
			return storage.IntValue(1)
		default:
			return storage.IntValue(0)
		}

	case "OBJECT_SCHEMA_NAME":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue("dbo")

	case "TYPE_ID":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		tName := strings.ToLower(strings.TrimSpace(valToString(args[0])))
		switch tName {
		case "int":
			return storage.IntValue(56)
		case "bigint":
			return storage.IntValue(127)
		case "smallint":
			return storage.IntValue(52)
		case "tinyint":
			return storage.IntValue(48)
		case "varchar":
			return storage.IntValue(167)
		case "nvarchar":
			return storage.IntValue(231)
		case "datetime":
			return storage.IntValue(61)
		case "bit":
			return storage.IntValue(104)
		case "decimal", "numeric":
			return storage.IntValue(106)
		case "float":
			return storage.IntValue(62)
		case "uniqueidentifier":
			return storage.IntValue(36)
		default:
			return storage.NullValue(storage.TypeInt)
		}

	case "TYPE_NAME":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		tID := args[0].IVal
		switch tID {
		case 56:
			return storage.StringValue("int")
		case 127:
			return storage.StringValue("bigint")
		case 52:
			return storage.StringValue("smallint")
		case 48:
			return storage.StringValue("tinyint")
		case 167:
			return storage.StringValue("varchar")
		case 231:
			return storage.StringValue("nvarchar")
		case 61:
			return storage.StringValue("datetime")
		case 104:
			return storage.StringValue("bit")
		case 106:
			return storage.StringValue("decimal")
		case 62:
			return storage.StringValue("float")
		case 36:
			return storage.StringValue("uniqueidentifier")
		default:
			return storage.NullValue(storage.TypeVarChar)
		}

	case "IS_SRVROLEMEMBER":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		role := strings.ToLower(strings.TrimSpace(valToString(args[0])))
		if role == "sysadmin" || role == "serveradmin" || role == "setupadmin" || role == "securityadmin" {
			return storage.IntValue(1)
		}
		return storage.IntValue(0)

	case "IS_MEMBER":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		role := strings.ToLower(strings.TrimSpace(valToString(args[0])))
		if role == "db_owner" || role == "db_datareader" || role == "db_datawriter" {
			return storage.IntValue(1)
		}
		return storage.IntValue(0)

	case "HAS_PERMS_BY_NAME":
		return storage.IntValue(1)

	case "PWDENCRYPT":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		pw := valToString(args[0])
		h := sha512.Sum512([]byte(pw))
		return storage.StringValue("0x0200" + strings.ToUpper(hex.EncodeToString(h[:])))

	case "PWDCOMPARE":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		pw := valToString(args[0])
		hashStr := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(valToString(args[1]))), "0X")
		if strings.HasPrefix(hashStr, "0200") {
			h := sha512.Sum512([]byte(pw))
			expected := "0200" + strings.ToUpper(hex.EncodeToString(h[:]))
			if hashStr == expected {
				return storage.IntValue(1)
			}
			return storage.IntValue(0)
		}
		h256 := sha256.Sum256([]byte(pw))
		if strings.EqualFold(hashStr, hex.EncodeToString(h256[:])) {
			return storage.IntValue(1)
		}
		return storage.IntValue(0)

	// ----------------------------------------------------
	// 9. Cryptography & Checksum (Group E)
	// ----------------------------------------------------
	case "HASHBYTES":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		algo := strings.ToUpper(strings.TrimSpace(valToString(args[0])))
		data := []byte(valToString(args[1]))
		var hashBytes []byte
		switch algo {
		case "MD5":
			h := md5.Sum(data)
			hashBytes = h[:]
		case "SHA", "SHA1":
			h := sha1.Sum(data)
			hashBytes = h[:]
		case "SHA2_256":
			h := sha256.Sum256(data)
			hashBytes = h[:]
		case "SHA2_512":
			h := sha512.Sum512(data)
			hashBytes = h[:]
		default:
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue("0x" + strings.ToUpper(hex.EncodeToString(hashBytes)))

	case "CHECKSUM":
		if len(args) == 0 {
			return storage.NullValue(storage.TypeInt)
		}
		h := fnv.New32a()
		for _, a := range args {
			if !a.IsNull {
				h.Write([]byte(valToString(a)))
			}
		}
		return storage.IntValue(int64(int32(h.Sum32())))

	case "BINARY_CHECKSUM":
		if len(args) == 0 {
			return storage.NullValue(storage.TypeInt)
		}
		var buf []byte
		for _, a := range args {
			if !a.IsNull {
				buf = append(buf, []byte(valToString(a))...)
			}
		}
		crc := crc32.ChecksumIEEE(buf)
		return storage.IntValue(int64(int32(crc)))

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
	case "GETDATE", "NOW", "CURRENT_TIMESTAMP", "GETUTCDATE", "SYSDATETIME", "SYSUTCDATETIME", "SYSDATETIMEOFFSET",
		"YEAR", "MONTH", "DAY", "DATEPART", "DATE_PART", "EXTRACT", "DATENAME", "DATEDIFF", "DATEDIFF_BIG", "DATEADD", "EOMONTH", "ISDATE",
		"DATETRUNC", "DATE_TRUNC", "DATE_BUCKET", "DATEBUCKET",
		"DATEFROMPARTS", "TIMEFROMPARTS", "DATETIMEFROMPARTS", "SMALLDATETIMEFROMPARTS", "TO_DATE", "TO_TIMESTAMP",
		"SWITCHOFFSET", "TODATETIMEOFFSET",
		"CONCAT", "CONCAT_WS", "QUOTENAME", "QUOTE_IDENT", "QUOTE_LITERAL", "ASCII", "CHAR", "CHR", "LEN", "LENGTH", "DATALENGTH", "UPPER", "LOWER", "TRIM", "LTRIM", "RTRIM",
		"LEFT", "RIGHT", "SUBSTRING", "SUBSTR", "REPLACE", "REVERSE", "CHARINDEX", "SPACE", "REPLICATE", "REPEAT", "STUFF",
		"PARSENAME", "STRING_ESCAPE", "FORMATMESSAGE",
		"PATINDEX", "UNICODE", "NCHAR", "SOUNDEX", "DIFFERENCE", "TRANSLATE", "FORMAT",
		"STRPOS", "MD5", "LPAD", "RPAD", "OCTET_LENGTH", "BIT_LENGTH", "INITCAP", "TO_CHAR",
		"GREP", "REGEXP_LIKE", "AWK", "SPLIT_PART",
		"ABS", "ROUND", "CEILING", "CEIL", "FLOOR", "POWER", "SQRT", "SQUARE", "SIGN", "PI",
		"EXP", "LOG", "LOG10", "SIN", "COS", "TAN", "COT", "ASIN", "ACOS", "ATAN", "ATN2", "DEGREES", "RADIANS", "RAND", "RANDOM",
		"BIT_COUNT", "GET_BIT", "SET_BIT", "LEFT_SHIFT", "RIGHT_SHIFT",
		"ISNULL", "COALESCE", "NULLIF", "ISNUMERIC", "IIF", "CHOOSE", "GREATEST", "LEAST",
		"ISJSON", "JSON_VALUE", "JSON_QUERY", "JSON_MODIFY",
		"NEWID", "NEWID7", "UUIDV7", "UUID_GENERATE_V7", "GEN_RANDOM_UUID",
		"SCOPE_IDENTITY", "SERVERPROPERTY",
		"ERROR_NUMBER", "ERROR_MESSAGE", "ERROR_LINE", "ERROR_SEVERITY", "ERROR_STATE", "ERROR_PROCEDURE", "@@ERROR",
		"OBJECT_ID", "OBJECT_NAME", "COL_LENGTH", "COL_NAME", "SCHEMA_NAME", "SCHEMA_ID", "DB_NAME", "DB_ID", "APP_NAME", "IDENT_CURRENT",
		"DATABASEPROPERTYEX", "OBJECTPROPERTY", "COLUMNPROPERTY", "INDEXPROPERTY", "OBJECT_SCHEMA_NAME", "TYPE_ID", "TYPE_NAME",
		"HOST_NAME", "HOST_ID", "CURRENT_USER", "USER_NAME", "SUSER_NAME", "SUSER_SNAME", "USER_ID", "SUSER_ID",
		"IS_SRVROLEMEMBER", "IS_MEMBER", "HAS_PERMS_BY_NAME", "PWDENCRYPT", "PWDCOMPARE",
		"@@VERSION", "VERSION", "@@SERVERNAME", "@@SPID", "SPID", "@@MAX_CONNECTIONS", "@@LANGUAGE",
		"@@TRANCOUNT", "TRANCOUNT", "@@LOCK_TIMEOUT", "LOCK_TIMEOUT", "@@OPTIONS", "OPTIONS", "@@NESTLEVEL", "NESTLEVEL",
		"@@CPU_BUSY", "@@IO_BUSY", "@@IDLE",
		"HASHBYTES", "CHECKSUM", "BINARY_CHECKSUM":
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
	case "day", "dd", "d":
		return "day"
	case "weekday", "dw", "w":
		return "weekday"
	case "dayofyear", "dy", "y":
		return "dayofyear"
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
	case "weekday":
		return storage.IntValue(int64(t.Weekday()) + 1)
	case "dayofyear":
		return storage.IntValue(int64(t.YearDay()))
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

func evalDateTrunc(part string, t time.Time) time.Time {
	switch part {
	case "year":
		return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
	case "quarter":
		qMonth := time.Month(((int(t.Month())-1)/3)*3 + 1)
		return time.Date(t.Year(), qMonth, 1, 0, 0, 0, 0, t.Location())
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	case "week":
		weekday := int(t.Weekday())
		truncatedDay := t.AddDate(0, 0, -weekday)
		return time.Date(truncatedDay.Year(), truncatedDay.Month(), truncatedDay.Day(), 0, 0, 0, 0, t.Location())
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	case "hour":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
	case "minute":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, t.Location())
	case "second":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, t.Location())
	case "millisecond":
		return t.Truncate(time.Millisecond)
	default:
		return t
	}
}

func evalDateBucket(part string, width int64, t time.Time, origin time.Time, hasOrigin bool) time.Time {
	if width <= 0 {
		width = 1
	}

	if !hasOrigin {
		switch part {
		case "second":
			secsFromMidnight := int64(t.Hour()*3600 + t.Minute()*60 + t.Second())
			bucketSec := (secsFromMidnight / width) * width
			dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			return dayStart.Add(time.Duration(bucketSec) * time.Second)
		case "minute":
			bucketWidthSec := width * 60
			secsFromMidnight := int64(t.Hour()*3600 + t.Minute()*60 + t.Second())
			bucketSec := (secsFromMidnight / bucketWidthSec) * bucketWidthSec
			dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			return dayStart.Add(time.Duration(bucketSec) * time.Second)
		case "hour":
			bucketWidthSec := width * 3600
			secsFromMidnight := int64(t.Hour()*3600 + t.Minute()*60 + t.Second())
			bucketSec := (secsFromMidnight / bucketWidthSec) * bucketWidthSec
			dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			return dayStart.Add(time.Duration(bucketSec) * time.Second)
		case "day":
			diffDays := int64(t.YearDay() - 1)
			bucketDay := (diffDays / width) * width
			yearStart := time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
			return yearStart.AddDate(0, 0, int(bucketDay))
		case "week":
			weekday := int(t.Weekday())
			return time.Date(t.Year(), t.Month(), t.Day()-weekday, 0, 0, 0, 0, t.Location())
		case "month":
			m := int64(t.Month() - 1)
			bucketM := (m / width) * width
			return time.Date(t.Year(), time.Month(bucketM+1), 1, 0, 0, 0, 0, t.Location())
		case "year":
			diffYears := int64(t.Year())
			bucketY := (diffYears / width) * width
			return time.Date(int(bucketY), 1, 1, 0, 0, 0, 0, t.Location())
		default:
			return evalDateTrunc(part, t)
		}
	}

	// Custom origin provided: align relative to custom origin
	switch part {
	case "year":
		diff := int64(t.Year() - origin.Year())
		bucketIdx := diff / width
		if diff < 0 && diff%width != 0 {
			bucketIdx--
		}
		return time.Date(origin.Year()+int(bucketIdx*width), origin.Month(), origin.Day(), origin.Hour(), origin.Minute(), origin.Second(), 0, t.Location())
	case "quarter":
		q1 := int64((origin.Year() * 4) + int((origin.Month()-1)/3))
		q2 := int64((t.Year() * 4) + int((t.Month()-1)/3))
		diff := q2 - q1
		bucketIdx := diff / width
		if diff < 0 && diff%width != 0 {
			bucketIdx--
		}
		return origin.AddDate(0, int(bucketIdx*width*3), 0)
	case "month":
		diff := int64((t.Year()-origin.Year())*12 + int(t.Month()-origin.Month()))
		bucketIdx := diff / width
		if diff < 0 && diff%width != 0 {
			bucketIdx--
		}
		totalMonths := bucketIdx * width
		return origin.AddDate(0, int(totalMonths), 0)
	case "week":
		diffSec := int64(t.Sub(origin).Seconds())
		bucketWidthSec := width * (7 * 24 * 3600)
		bucketIdx := diffSec / bucketWidthSec
		if diffSec < 0 && diffSec%bucketWidthSec != 0 {
			bucketIdx--
		}
		return origin.Add(time.Duration(bucketIdx*bucketWidthSec) * time.Second)
	case "day":
		diffSec := int64(t.Sub(origin).Seconds())
		bucketWidthSec := width * (24 * 3600)
		bucketIdx := diffSec / bucketWidthSec
		if diffSec < 0 && diffSec%bucketWidthSec != 0 {
			bucketIdx--
		}
		return origin.Add(time.Duration(bucketIdx*bucketWidthSec) * time.Second)
	case "hour":
		diffSec := int64(t.Sub(origin).Seconds())
		bucketWidthSec := width * 3600
		bucketIdx := diffSec / bucketWidthSec
		if diffSec < 0 && diffSec%bucketWidthSec != 0 {
			bucketIdx--
		}
		return origin.Add(time.Duration(bucketIdx*bucketWidthSec) * time.Second)
	case "minute":
		diffSec := int64(t.Sub(origin).Seconds())
		bucketWidthSec := width * 60
		bucketIdx := diffSec / bucketWidthSec
		if diffSec < 0 && diffSec%bucketWidthSec != 0 {
			bucketIdx--
		}
		return origin.Add(time.Duration(bucketIdx*bucketWidthSec) * time.Second)
	case "second":
		diffSec := int64(t.Sub(origin).Seconds())
		bucketIdx := diffSec / width
		if diffSec < 0 && diffSec%width != 0 {
			bucketIdx--
		}
		return origin.Add(time.Duration(bucketIdx*width) * time.Second)
	default:
		return evalDateTrunc(part, t)
	}
}

var (
	uuidv7Mu sync.Mutex
	lastV7Ms int64
	v7Seq    uint16
)

// GenerateUUIDv7 generates an RFC 9562 Version 7 UUID string with 48-bit millisecond timestamp
// and sub-millisecond sequence counter to guarantee strict monotonic ordering for B+Tree primary keys.
func GenerateUUIDv7() string {
	return generateUUIDv7()
}

// GenerateUUIDv4 generates an RFC 4122 Version 4 random UUID string.
func GenerateUUIDv4() string {
	return generateUUIDv4()
}

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
		// Seed 12-bit sequence randomly with headroom (0..0x7FF) to avoid early overflow
		var seed [2]byte
		_, _ = rand.Read(seed[:])
		v7Seq = (uint16(seed[0]&0x07) << 8) | uint16(seed[1])
	} else {
		// Same millisecond or clock backwards: increment sequence counter for strict monotonicity
		v7Seq = (v7Seq + 1) & 0x0FFF
		if v7Seq == 0 {
			// Sequence counter rolled over within same millisecond; advance timestamp by 1ms
			lastV7Ms++
		}
		nowMs = lastV7Ms
	}
	seq := v7Seq
	uuidv7Mu.Unlock()

	// 48-bit timestamp (big-endian)
	b[0] = byte(nowMs >> 40)
	b[1] = byte(nowMs >> 32)
	b[2] = byte(nowMs >> 24)
	b[3] = byte(nowMs >> 16)
	b[4] = byte(nowMs >> 8)
	b[5] = byte(nowMs)

	// 4-bit version (0x7) + 12-bit sequence counter (rand_a)
	b[6] = 0x70 | byte((seq>>8)&0x0F)
	b[7] = byte(seq & 0xFF)

	// 62-bit rand_b: fill bytes 8..15 with cryptographically secure random bytes
	_, _ = rand.Read(b[8:])
	// 2-bit variant (10xxxxxx)
	b[8] = (b[8] & 0x3F) | 0x80

	return strings.ToUpper(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

func computeSoundex(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) == 0 {
		return "0000"
	}
	runes := []rune(s)
	var filtered []rune
	for _, r := range runes {
		if r >= 'A' && r <= 'Z' {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 {
		return "0000"
	}
	firstChar := filtered[0]
	getDigit := func(r rune) byte {
		switch r {
		case 'B', 'F', 'P', 'V':
			return '1'
		case 'C', 'G', 'J', 'K', 'Q', 'S', 'X', 'Z':
			return '2'
		case 'D', 'T':
			return '3'
		case 'L':
			return '4'
		case 'M', 'N':
			return '5'
		case 'R':
			return '6'
		default:
			return '0'
		}
	}

	var digits []byte
	lastDigit := getDigit(firstChar)
	for i := 1; i < len(filtered); i++ {
		d := getDigit(filtered[i])
		if d != '0' && d != lastDigit {
			digits = append(digits, d)
		}
		if filtered[i] != 'H' && filtered[i] != 'W' {
			lastDigit = d
		}
		if len(digits) == 3 {
			break
		}
	}
	for len(digits) < 3 {
		digits = append(digits, '0')
	}
	return string(firstChar) + string(digits[:3])
}

func ComputePatindex(pattern, text string) int64 {
	if pattern == "" || text == "" {
		return 0
	}
	leadingPercent := strings.HasPrefix(pattern, "%")
	trailingPercent := strings.HasSuffix(pattern, "%")

	sub := pattern
	if leadingPercent {
		sub = sub[1:]
	}
	if trailingPercent && len(sub) > 0 {
		sub = sub[:len(sub)-1]
	}
	if sub == "" {
		return 1
	}

	var rx strings.Builder
	if !leadingPercent {
		rx.WriteString("^")
	}

	runes := []rune(sub)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch ch {
		case '%':
			rx.WriteString(".*?")
		case '_':
			rx.WriteString(".")
		case '[':
			j := i + 1
			if j < len(runes) && runes[j] == '^' {
				j++
			}
			if j < len(runes) && runes[j] == ']' {
				j++
			}
			for j < len(runes) && runes[j] != ']' {
				j++
			}
			if j < len(runes) && runes[j] == ']' {
				bracketContent := string(runes[i : j+1])
				rx.WriteString(bracketContent)
				i = j
			} else {
				rx.WriteString(regexp.QuoteMeta(string(ch)))
			}
		default:
			rx.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}

	if !trailingPercent {
		rx.WriteString("$")
	}

	re, err := regexp.Compile("(?i)" + rx.String())
	if err != nil {
		return 0
	}

	loc := re.FindStringIndex(text)
	if loc == nil {
		return 0
	}
	matchedRunes := []rune(text[:loc[0]])
	return int64(len(matchedRunes) + 1)
}

func formatDateCustom(t time.Time, format string) string {
	switch strings.ToLower(format) {
	case "d":
		return t.Format("2006-01-02")
	case "s":
		return t.Format("2006-01-02T15:04:05")
	}
	var sb strings.Builder
	runes := []rune(format)
	for i := 0; i < len(runes); {
		r := runes[i]
		j := i + 1
		for j < len(runes) && runes[j] == r {
			j++
		}
		tokenLen := j - i
		matched := true
		switch r {
		case 'y', 'Y':
			if tokenLen >= 4 {
				sb.WriteString(t.Format("2006"))
			} else {
				sb.WriteString(t.Format("06"))
			}
		case 'M':
			if tokenLen >= 4 {
				sb.WriteString(t.Format("January"))
			} else if tokenLen == 3 {
				sb.WriteString(t.Format("Jan"))
			} else if tokenLen == 2 {
				sb.WriteString(t.Format("01"))
			} else {
				sb.WriteString(t.Format("1"))
			}
		case 'd':
			if tokenLen >= 4 {
				sb.WriteString(t.Format("Monday"))
			} else if tokenLen == 3 {
				sb.WriteString(t.Format("Mon"))
			} else if tokenLen == 2 {
				sb.WriteString(t.Format("02"))
			} else {
				sb.WriteString(t.Format("2"))
			}
		case 'H':
			if tokenLen >= 2 {
				sb.WriteString(t.Format("15"))
			} else {
				sb.WriteString(fmt.Sprintf("%d", t.Hour()))
			}
		case 'h':
			h12 := t.Hour() % 12
			if h12 == 0 {
				h12 = 12
			}
			if tokenLen >= 2 {
				sb.WriteString(fmt.Sprintf("%02d", h12))
			} else {
				sb.WriteString(fmt.Sprintf("%d", h12))
			}
		case 'm':
			if tokenLen >= 2 {
				sb.WriteString(t.Format("04"))
			} else {
				sb.WriteString(fmt.Sprintf("%d", t.Minute()))
			}
		case 's':
			if tokenLen >= 2 {
				sb.WriteString(t.Format("05"))
			} else {
				sb.WriteString(fmt.Sprintf("%d", t.Second()))
			}
		case 'f', 'F':
			ms := t.Nanosecond() / 1_000_000
			if tokenLen >= 3 {
				sb.WriteString(fmt.Sprintf("%03d", ms))
			} else {
				sb.WriteString(fmt.Sprintf("%d", ms))
			}
		case 't':
			if t.Hour() >= 12 {
				sb.WriteString("PM")
			} else {
				sb.WriteString("AM")
			}
		default:
			matched = false
		}
		if matched {
			i = j
		} else {
			sb.WriteRune(r)
			i++
		}
	}
	return sb.String()
}

func formatNumberCustom(num float64, format string) string {
	f := strings.TrimSpace(format)
	upper := strings.ToUpper(f)
	if strings.HasPrefix(upper, "N") {
		dec := 2
		if len(upper) > 1 {
			if d, err := strconv.Atoi(upper[1:]); err == nil {
				dec = d
			}
		}
		return formatWithCommas(num, dec)
	}
	if strings.HasPrefix(upper, "C") {
		dec := 2
		if len(upper) > 1 {
			if d, err := strconv.Atoi(upper[1:]); err == nil {
				dec = d
			}
		}
		return "$" + formatWithCommas(num, dec)
	}
	if strings.HasPrefix(upper, "P") {
		dec := 2
		if len(upper) > 1 {
			if d, err := strconv.Atoi(upper[1:]); err == nil {
				dec = d
			}
		}
		return formatWithCommas(num*100, dec) + "%"
	}
	if strings.HasPrefix(upper, "D") {
		width := 1
		if len(upper) > 1 {
			if w, err := strconv.Atoi(upper[1:]); err == nil {
				width = w
			}
		}
		return fmt.Sprintf("%0*d", width, int64(num))
	}
	if strings.Count(f, "0") == len(f) && len(f) > 0 {
		return fmt.Sprintf("%0*d", len(f), int64(num))
	}
	return strconv.FormatFloat(num, 'f', -1, 64)
}

func formatWithCommas(n float64, decimals int) string {
	str := strconv.FormatFloat(n, 'f', decimals, 64)
	parts := strings.Split(str, ".")
	intPart := parts[0]
	isNeg := false
	if strings.HasPrefix(intPart, "-") {
		isNeg = true
		intPart = intPart[1:]
	}
	var res []byte
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			res = append(res, ',')
		}
		res = append(res, byte(c))
	}
	out := string(res)
	if isNeg {
		out = "-" + out
	}
	if len(parts) > 1 {
		out += "." + parts[1]
	}
	return out
}

func evalDateName(part string, t time.Time) storage.Value {
	switch part {
	case "year":
		return storage.StringValue(strconv.Itoa(t.Year()))
	case "quarter":
		q := (int(t.Month())-1)/3 + 1
		return storage.StringValue(strconv.Itoa(q))
	case "month":
		return storage.StringValue(t.Month().String())
	case "day":
		return storage.StringValue(strconv.Itoa(t.Day()))
	case "weekday":
		return storage.StringValue(t.Weekday().String())
	case "dayofyear":
		return storage.StringValue(strconv.Itoa(t.YearDay()))
	case "week":
		_, w := t.ISOWeek()
		return storage.StringValue(strconv.Itoa(w))
	case "hour":
		return storage.StringValue(strconv.Itoa(t.Hour()))
	case "minute":
		return storage.StringValue(strconv.Itoa(t.Minute()))
	case "second":
		return storage.StringValue(strconv.Itoa(t.Second()))
	case "millisecond":
		return storage.StringValue(strconv.Itoa(t.Nanosecond() / 1e6))
	default:
		return storage.NullValue(storage.TypeVarChar)
	}
}

func parseObjectPieces(s string) []string {
	var parts []string
	var current strings.Builder
	inBracket := false
	inQuote := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '[' && !inQuote {
			inBracket = true
			continue
		} else if ch == ']' && inBracket {
			inBracket = false
			continue
		} else if ch == '"' && !inBracket {
			inQuote = !inQuote
			continue
		} else if ch == '.' && !inBracket && !inQuote {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	parts = append(parts, current.String())

	if len(parts) > 4 {
		return nil
	}
	return parts
}

func modifyJSONByPath(doc, path string, newVal storage.Value) (string, error) {
	var data any
	if err := json.Unmarshal([]byte(doc), &data); err != nil {
		return doc, err
	}
	cleanPath := strings.TrimPrefix(strings.TrimSpace(path), "$")
	cleanPath = strings.TrimPrefix(cleanPath, ".")
	if cleanPath == "" {
		if newVal.IsNull {
			return "null", nil
		}
		marshaled, err := json.Marshal(storageValueToInterface(newVal))
		return string(marshaled), err
	}

	parts := strings.Split(cleanPath, ".")
	if obj, ok := data.(map[string]any); ok {
		current := obj
		for i := 0; i < len(parts)-1; i++ {
			p := parts[i]
			sub, exists := current[p]
			if !exists {
				nextMap := make(map[string]any)
				current[p] = nextMap
				current = nextMap
			} else if nextMap, isMap := sub.(map[string]any); isMap {
				current = nextMap
			} else {
				return doc, fmt.Errorf("path is not an object")
			}
		}
		lastKey := parts[len(parts)-1]
		if newVal.IsNull {
			delete(current, lastKey)
		} else {
			current[lastKey] = storageValueToInterface(newVal)
		}
		res, err := json.Marshal(obj)
		if err != nil {
			return doc, err
		}
		return string(res), nil
	}
	return doc, nil
}

func storageValueToInterface(v storage.Value) any {
	if v.IsNull {
		return nil
	}
	switch v.Type {
	case storage.TypeInt:
		return v.IVal
	case storage.TypeFloat:
		return v.FVal
	case storage.TypeBool:
		return v.BVal
	case storage.TypeJSON:
		var parsed any
		if err := json.Unmarshal([]byte(v.SVal), &parsed); err == nil {
			return parsed
		}
		return v.SVal
	default:
		return v.SVal
	}
}

func parseOffsetLocation(s string) *time.Location {
	s = strings.TrimSpace(s)
	sign := 1
	if strings.HasPrefix(s, "-") {
		sign = -1
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	parts := strings.Split(s, ":")
	hours, _ := strconv.Atoi(parts[0])
	mins := 0
	if len(parts) > 1 {
		mins, _ = strconv.Atoi(parts[1])
	}
	sec := sign * (hours*3600 + mins*60)
	return time.FixedZone(fmt.Sprintf("%+03d:%02d", sign*hours, mins), sec)
}


