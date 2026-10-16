package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/storage"
)

// StripLeadingComments removes leading SQL line comments (--) and block comments (/* */) as well as whitespace.
func StripLeadingComments(s string) string {
	for {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "--") {
			idx := strings.IndexByte(s, '\n')
			if idx == -1 {
				return ""
			}
			s = s[idx+1:]
		} else if strings.HasPrefix(s, "/*") {
			idx := strings.Index(s, "*/")
			if idx == -1 {
				return ""
			}
			s = s[idx+2:]
		} else {
			break
		}
	}
	return s
}

func isBeginTransaction(rest string) bool {
	rest = strings.TrimLeft(rest, " \t\r\n")
	upper := strings.ToUpper(rest)
	if strings.HasPrefix(upper, "TRANSACTION") {
		after := upper[len("TRANSACTION"):]
		return len(after) == 0 || !IsIdentChar(after[0])
	}
	if strings.HasPrefix(upper, "TRAN") {
		after := upper[len("TRAN"):]
		return len(after) == 0 || !IsIdentChar(after[0])
	}
	if strings.HasPrefix(upper, "DISTRIBUTED") {
		after := upper[len("DISTRIBUTED"):]
		if len(after) == 0 || !IsIdentChar(after[0]) {
			return true
		}
	}
	return false
}

// SplitStatements splits a multi-statement SQL batch by semicolons, properly respecting quotes and BEGIN...END blocks.
func SplitStatements(query string) []string {
	var stmts []string
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)
	inLineComment := false
	inBlockComment := false
	blockDepth := 0
	isProcOrFunc := false

	checkProc := func(s string) bool {
		sUpper := strings.ToUpper(StripLeadingComments(s))
		return strings.HasPrefix(sUpper, "CREATE PROCEDURE") ||
			strings.HasPrefix(sUpper, "CREATE PROC") ||
			strings.HasPrefix(sUpper, "CREATE OR ALTER PROCEDURE") ||
			strings.HasPrefix(sUpper, "CREATE OR ALTER PROC") ||
			strings.HasPrefix(sUpper, "ALTER PROCEDURE") ||
			strings.HasPrefix(sUpper, "ALTER PROC") ||
			strings.HasPrefix(sUpper, "CREATE FUNCTION") ||
			strings.HasPrefix(sUpper, "CREATE OR ALTER FUNCTION") ||
			strings.HasPrefix(sUpper, "ALTER FUNCTION") ||
			strings.HasPrefix(sUpper, "CREATE TRIGGER") ||
			strings.HasPrefix(sUpper, "CREATE OR ALTER TRIGGER") ||
			strings.HasPrefix(sUpper, "ALTER TRIGGER")
	}

	for i := 0; i < len(query); i++ {
		ch := query[i]

		if inLineComment {
			cur.WriteByte(ch)
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}

		if inBlockComment {
			cur.WriteByte(ch)
			if ch == '*' && i+1 < len(query) && query[i+1] == '/' {
				cur.WriteByte('/')
				i++
				inBlockComment = false
			}
			continue
		}

		if inQuote {
			if ch == quoteChar && (i == 0 || query[i-1] != '\\') {
				inQuote = false
			}
			cur.WriteByte(ch)
			continue
		}

		if ch == '\'' || ch == '"' {
			inQuote = true
			quoteChar = ch
			cur.WriteByte(ch)
			continue
		}

		if ch == '-' && i+1 < len(query) && query[i+1] == '-' {
			inLineComment = true
			cur.WriteByte(ch)
			cur.WriteByte(query[i+1])
			i++
			continue
		}

		if ch == '/' && i+1 < len(query) && query[i+1] == '*' {
			inBlockComment = true
			cur.WriteByte(ch)
			cur.WriteByte(query[i+1])
			i++
			continue
		}

		if !isProcOrFunc && checkProc(cur.String()) {
			isProcOrFunc = true
		}

		if isProcOrFunc {
			if (i == 0 || !IsIdentChar(query[i-1])) && i+5 <= len(query) && strings.EqualFold(query[i:i+5], "BEGIN") && (i+5 == len(query) || !IsIdentChar(query[i+5])) {
				if !isBeginTransaction(query[i+5:]) {
					blockDepth++
				}
			} else if (i == 0 || !IsIdentChar(query[i-1])) && i+4 <= len(query) && strings.EqualFold(query[i:i+4], "CASE") && (i+4 == len(query) || !IsIdentChar(query[i+4])) {
				blockDepth++
			} else if (i == 0 || !IsIdentChar(query[i-1])) && i+3 <= len(query) && strings.EqualFold(query[i:i+3], "END") && (i+3 == len(query) || !IsIdentChar(query[i+3])) {
				if blockDepth > 0 {
					blockDepth--
				}
			}
		}

		if ch == ';' {
			if isProcOrFunc && blockDepth > 0 {
				cur.WriteByte(ch)
			} else {
				stmt := strings.TrimSpace(cur.String())
				if stmt != "" && StripLeadingComments(stmt) != "" {
					stmts = append(stmts, stmt)
				}
				cur.Reset()
				isProcOrFunc = false
				blockDepth = 0
			}
		} else {
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		stmt := strings.TrimSpace(cur.String())
		if stmt != "" && StripLeadingComments(stmt) != "" {
			stmts = append(stmts, stmt)
		}
	}
	return stmts
}

// IsIdentChar reports whether ch is a valid SQL identifier character.
func IsIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '@' || ch == '#'
}

// ParseTimeoutDuration parses duration strings with units (ms, s, m) or plain integers as milliseconds.
func ParseTimeoutDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d < 0 {
			return 0, nil
		}
		return d, nil
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		if ms <= 0 {
			return 0, nil
		}
		return time.Duration(ms) * time.Millisecond, nil
	}
	return 0, nil
}

// CleanSavepointName strips delimiters, semicolons, quotes, and brackets from savepoint names.
func CleanSavepointName(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, ";")
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "[]\"'`")
	return strings.TrimSpace(raw)
}

// TrimAnyPrefix returns original sliced past the first matching prefix matched case-insensitively against upper.
func TrimAnyPrefix(upper, original string, prefixes ...string) (string, bool) {
	for _, p := range prefixes {
		if strings.HasPrefix(upper, p) {
			return original[len(p):], true
		}
	}
	return "", false
}

// IsRollbackOrResetQuery reports whether query is a transaction rollback or session reset command.
func IsRollbackOrResetQuery(q string) bool {
	upper := strings.ToUpper(strings.TrimSpace(q))
	return strings.HasPrefix(upper, "ROLLBACK") ||
		strings.HasPrefix(upper, "DISCARD") ||
		strings.HasPrefix(upper, "RESET") ||
		strings.HasPrefix(upper, "DEALLOCATE") ||
		strings.HasPrefix(upper, "UNLISTEN")
}

// ParseSessionVarValue converts a T-SQL literal string to a native Go value (bool, int64, float64, string).
func ParseSessionVarValue(s string) any {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "NULL") {
		return nil
	}
	if strings.EqualFold(s, "TRUE") {
		return true
	}
	if strings.EqualFold(s, "FALSE") {
		return false
	}
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
		return s[1 : len(s)-1]
	}
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2 {
		return s[1 : len(s)-1]
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// ConvertValue converts an arbitrary Go value into a typed storage.Value according to targetType.
func ConvertValue(v any, targetType storage.DataType) storage.Value {
	if v == nil {
		return storage.NullValue(targetType)
	}
	switch targetType {
	case storage.TypeInt:
		switch num := v.(type) {
		case float64:
			return storage.IntValue(int64(num))
		case int:
			return storage.IntValue(int64(num))
		case int64:
			return storage.IntValue(num)
		case string:
			if num == "" {
				return storage.NullValue(targetType)
			}
			var i int64
			fmt.Sscanf(num, "%d", &i)
			return storage.IntValue(i)
		}
	case storage.TypeVarChar:
		return storage.StringValue(fmt.Sprintf("%v", v))
	case storage.TypeFloat:
		switch num := v.(type) {
		case float64:
			return storage.FloatValue(num)
		case int:
			return storage.FloatValue(float64(num))
		case int64:
			return storage.FloatValue(float64(num))
		case string:
			if num == "" {
				return storage.NullValue(targetType)
			}
			f, _ := strconv.ParseFloat(num, 64)
			return storage.FloatValue(f)
		}
	case storage.TypeDecimal:
		switch num := v.(type) {
		case storage.Decimal:
			return storage.DecimalValueFromDecimal(num)
		case float64:
			return storage.DecimalValueFromDecimal(storage.NewDecimalFromFloat(num, 4))
		case int:
			return storage.DecimalValueFromDecimal(storage.NewDecimalFromInt(int64(num), 0))
		case int64:
			return storage.DecimalValueFromDecimal(storage.NewDecimalFromInt(num, 0))
		case string:
			if num == "" {
				return storage.NullValue(targetType)
			}
			return storage.DecimalValue(num)
		default:
			return storage.DecimalValue(fmt.Sprintf("%v", v))
		}
	case storage.TypeBool:
		switch b := v.(type) {
		case bool:
			return storage.BoolValue(b)
		case string:
			s := strings.ToLower(strings.TrimSpace(b))
			return storage.BoolValue(s == "true" || s == "1" || s == "t")
		case int, int64:
			return storage.BoolValue(fmt.Sprintf("%v", b) == "1")
		}
	case storage.TypeDateTime:
		switch t := v.(type) {
		case time.Time:
			return storage.DateTimeValue(t)
		case string:
			if t == "" {
				return storage.NullValue(targetType)
			}
			for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
				if parsed, err := time.Parse(layout, t); err == nil {
					return storage.DateTimeValue(parsed)
				}
			}
		}
	}
	return storage.StringValue(fmt.Sprintf("%v", v))
}

func convertValue(v any, targetType storage.DataType) storage.Value {
	return ConvertValue(v, targetType)
}

// RewriteSessionTempTables replaces local temporary table references (#tableName) with session-isolated
// physical table names (__temp_<sessionID>_tableName), while leaving global temporary tables (##global)
// and internal tables (#__dt_, #__cte_) untouched.
func RewriteSessionTempTables(query string, cs *ClientSession) string {
	if cs == nil || !strings.Contains(query, "#") {
		return query
	}

	var b strings.Builder
	b.Grow(len(query) + 32)
	inString := false
	var quoteChar byte
	n := len(query)

	for i := 0; i < n; i++ {
		ch := query[i]

		// Track string literals '...' or "..."
		if inString {
			b.WriteByte(ch)
			if ch == quoteChar {
				// Escaped quotes '' or ""
				if i+1 < n && query[i+1] == quoteChar {
					i++
					b.WriteByte(query[i])
					continue
				}
				inString = false
			}
			continue
		}

		if ch == '\'' || ch == '"' {
			inString = true
			quoteChar = ch
			b.WriteByte(ch)
			continue
		}

		// Line comments --
		if ch == '-' && i+1 < n && query[i+1] == '-' {
			for i < n && query[i] != '\n' {
				b.WriteByte(query[i])
				i++
			}
			if i < n {
				b.WriteByte(query[i])
			}
			continue
		}

		// Block comments /* ... */
		if ch == '/' && i+1 < n && query[i+1] == '*' {
			b.WriteByte(ch)
			b.WriteByte(query[i+1])
			i += 2
			for i < n {
				if query[i] == '*' && i+1 < n && query[i+1] == '/' {
					b.WriteByte('*')
					b.WriteByte('/')
					i++
					break
				}
				b.WriteByte(query[i])
				i++
			}
			continue
		}

		// Check for #
		if ch == '#' {
			// If preceded by # (part of ##): leave as-is
			if i > 0 && query[i-1] == '#' {
				b.WriteByte(ch)
				continue
			}
			// If followed by # (start of ##): leave as-is
			if i+1 < n && query[i+1] == '#' {
				b.WriteByte(ch)
				continue
			}
			// If followed by __ (start of internal #__dt_ or #__cte_): leave as-is
			if i+2 < n && query[i+1] == '_' && query[i+2] == '_' {
				b.WriteByte(ch)
				continue
			}
			// If preceded by an identifier char (e.g. abc#): leave as-is
			if i > 0 && IsIdentChar(query[i-1]) {
				b.WriteByte(ch)
				continue
			}

			// Read identifier after #
			j := i + 1
			for j < n && (IsIdentChar(query[j]) && query[j] != '#') {
				j++
			}
			if j > i+1 {
				tblName := query[i:j]
				phys := cs.ResolveTempTableName(tblName)
				b.WriteString(phys)
				i = j - 1
				continue
			}
		}

		b.WriteByte(ch)
	}

	return b.String()
}


