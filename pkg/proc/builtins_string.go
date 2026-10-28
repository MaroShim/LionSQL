package proc

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"lionsql/pkg/storage"
)

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



func isStringBuiltin(name string) bool {
	switch name {
	case "CONCAT", "CONCAT_WS", "QUOTENAME", "QUOTE_IDENT", "QUOTE_LITERAL", "ASCII", "CHAR", "CHR", "LEN", "LENGTH", "DATALENGTH", "UPPER", "LOWER", "TRIM", "LTRIM", "RTRIM",
		"LEFT", "RIGHT", "SUBSTRING", "SUBSTR", "REPLACE", "REVERSE", "CHARINDEX", "SPACE", "REPLICATE", "REPEAT", "STUFF",
		"PARSENAME", "STRING_ESCAPE", "FORMATMESSAGE",
		"PATINDEX", "UNICODE", "NCHAR", "SOUNDEX", "DIFFERENCE", "TRANSLATE", "FORMAT",
		"STRPOS", "INSTR", "MD5", "SHA256", "HEX", "UNHEX", "LPAD", "RPAD", "OCTET_LENGTH", "BIT_LENGTH", "INITCAP", "TO_CHAR",
		"GREP", "REGEXP_LIKE", "AWK", "SPLIT_PART":
		return true
	default:
		return false
	}
}

func evalStringBuiltin(upper string, args []storage.Value) storage.Value {
	switch upper {
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
			return storage.NullValue(storage.TypeInt)
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
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue(strings.ToUpper(valToString(args[0])))

	case "LOWER":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue(strings.ToLower(valToString(args[0])))

	case "TRIM":
		if len(args) == 0 || args[0].IsNull || (len(args) >= 2 && args[1].IsNull) {
			return storage.NullValue(storage.TypeVarChar)
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
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue(strings.TrimLeft(valToString(args[0]), " \t\r\n"))

	case "RTRIM":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue(strings.TrimRight(valToString(args[0]), " \t\r\n"))

	case "LEFT":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
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
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
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
		if len(args) < 3 || args[0].IsNull || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeVarChar)
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
		if len(args) < 3 || args[0].IsNull || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		str := valToString(args[0])
		oldSub := valToString(args[1])
		newSub := valToString(args[2])
		return storage.StringValue(strings.ReplaceAll(str, oldSub, newSub))

	case "REVERSE":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		runes := []rune(valToString(args[0]))
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return storage.StringValue(string(runes))

	case "CHARINDEX":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull || (len(args) >= 3 && args[2].IsNull) {
			return storage.NullValue(storage.TypeInt)
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
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		if args[0].IVal <= 0 {
			return storage.StringValue("")
		}
		n := int(args[0].IVal)
		if n > 8000 {
			n = 8000 // defensive upper bound
		}
		return storage.StringValue(strings.Repeat(" ", n))

	case "REPLICATE", "REPEAT":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		if args[1].IVal <= 0 {
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

	case "STRPOS", "INSTR":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
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

	case "SHA256":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		data := []byte(valToString(args[0]))
		h := sha256.Sum256(data)
		return storage.StringValue(hex.EncodeToString(h[:]))

	case "HEX":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		if args[0].Type == storage.TypeInt {
			return storage.StringValue(strings.ToUpper(strconv.FormatInt(args[0].IVal, 16)))
		}
		str := valToString(args[0])
		return storage.StringValue(strings.ToUpper(hex.EncodeToString([]byte(str))))

	case "UNHEX":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		str := strings.TrimSpace(valToString(args[0]))
		b, err := hex.DecodeString(str)
		if err != nil {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue(string(b))

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
	default:
		return storage.NullValue(storage.TypeUnknown)
	}
}
