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
	if len(rest) == 0 || rest[0] == ';' {
		return true
	}
	upper := strings.ToUpper(rest)
	if strings.HasPrefix(upper, "TRANSACTION") {
		after := upper[len("TRANSACTION"):]
		return len(after) == 0 || !IsIdentChar(after[0])
	}
	if strings.HasPrefix(upper, "TRAN") {
		after := upper[len("TRAN"):]
		return len(after) == 0 || !IsIdentChar(after[0])
	}
	if strings.HasPrefix(upper, "WORK") {
		after := upper[len("WORK"):]
		return len(after) == 0 || !IsIdentChar(after[0])
	}
	if strings.HasPrefix(upper, "DISTRIBUTED") {
		after := upper[len("DISTRIBUTED"):]
		if len(after) == 0 || !IsIdentChar(after[0]) {
			return true
		}
	}
	if strings.HasPrefix(upper, "READ") || strings.HasPrefix(upper, "ISOLATION") || strings.HasPrefix(upper, "DEFERRABLE") || strings.HasPrefix(upper, "NOT") {
		return true
	}
	return false
}

func isProcOrFuncHeader(s string) bool {
	s = StripLeadingComments(s)
	if len(s) < 10 {
		return false
	}
	ch := s[0]
	if ch != 'C' && ch != 'c' && ch != 'A' && ch != 'a' {
		return false
	}
	sUpper := strings.ToUpper(s)
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

func isIfHeader(s string) bool {
	s = StripLeadingComments(s)
	if len(s) < 2 {
		return false
	}
	ch := s[0]
	if ch != 'I' && ch != 'i' {
		return false
	}
	sUpper := strings.ToUpper(s)
	if strings.HasPrefix(sUpper, "IF") {
		rest := sUpper[2:]
		return len(rest) == 0 || !IsIdentChar(rest[0])
	}
	return false
}

func hasNextTokenElse(s string, idx int) bool {
	n := len(s)
	for idx < n {
		ch := s[idx]
		if ch <= ' ' {
			idx++
			continue
		}
		if ch == '-' && idx+1 < n && s[idx+1] == '-' {
			idx += 2
			for idx < n && s[idx] != '\n' {
				idx++
			}
			continue
		}
		if ch == '/' && idx+1 < n && s[idx+1] == '*' {
			idx += 2
			for idx+1 < n && !(s[idx] == '*' && s[idx+1] == '/') {
				idx++
			}
			idx += 2
			continue
		}
		break
	}
	if idx >= n {
		return false
	}
	if idx+4 <= n && strings.EqualFold(s[idx:idx+4], "ELSE") {
		if idx+4 == n || !IsIdentChar(s[idx+4]) {
			return true
		}
	}
	return false
}

// SplitStatements splits a multi-statement SQL batch by semicolons, properly respecting quotes and BEGIN...END blocks.
// It uses an index-based slice scanner with zero intermediate string builder copies and zero heap allocations during scanning.
func SplitStatements(query string) []string {
	n := len(query)
	if n == 0 {
		return nil
	}

	// Fast-path: single statement without semicolons
	if strings.IndexByte(query, ';') == -1 {
		stmt := strings.TrimSpace(query)
		if stmt != "" && StripLeadingComments(stmt) != "" {
			return []string{stmt}
		}
		return nil
	}

	var stmts []string
	start := 0
	inQuote := false
	quoteChar := byte(0)
	inLineComment := false
	inBlockComment := false
	blockDepth := 0
	isIfStmt := false
	needHeaderCheck := true

	for i := 0; i < n; i++ {
		ch := query[i]

		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}

		if inBlockComment {
			if ch == '*' && i+1 < n && query[i+1] == '/' {
				i++
				inBlockComment = false
			}
			continue
		}

		if inQuote {
			if ch == quoteChar && (i == 0 || query[i-1] != '\\') {
				inQuote = false
			}
			continue
		}

		if ch == '\'' || ch == '"' {
			inQuote = true
			quoteChar = ch
			needHeaderCheck = false
			continue
		}

		if ch == '-' && i+1 < n && query[i+1] == '-' {
			inLineComment = true
			i++
			continue
		}

		if ch == '/' && i+1 < n && query[i+1] == '*' {
			inBlockComment = true
			i++
			continue
		}

		if needHeaderCheck {
			if ch > ' ' {
				isIfStmt = isIfHeader(query[start:])
				needHeaderCheck = false
			}
		}

		if (i == 0 || !IsIdentChar(query[i-1])) && i+5 <= n && strings.EqualFold(query[i:i+5], "BEGIN") && (i+5 == n || !IsIdentChar(query[i+5])) {
			if !isBeginTransaction(query[i+5:]) {
				blockDepth++
			}
		} else if (i == 0 || !IsIdentChar(query[i-1])) && i+4 <= n && strings.EqualFold(query[i:i+4], "CASE") && (i+4 == n || !IsIdentChar(query[i+4])) {
			blockDepth++
		} else if (i == 0 || !IsIdentChar(query[i-1])) && i+3 <= n && strings.EqualFold(query[i:i+3], "END") && (i+3 == n || !IsIdentChar(query[i+3])) {
			if blockDepth > 0 {
				blockDepth--
			}
		}

		if ch == ';' {
			if blockDepth > 0 {
				continue
			}
			if isIfStmt && hasNextTokenElse(query, i+1) {
				continue
			}
			stmt := strings.TrimSpace(query[start:i])
			if stmt != "" && StripLeadingComments(stmt) != "" {
				stmts = append(stmts, stmt)
			}
			start = i + 1
			isIfStmt = false
			blockDepth = 0
			needHeaderCheck = true
		}
	}

	if start < n {
		stmt := strings.TrimSpace(query[start:])
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
		inner := s[1 : len(s)-1]
		return strings.ReplaceAll(inner, "''", "'")
	}
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2 {
		inner := s[1 : len(s)-1]
		return strings.ReplaceAll(inner, "\"\"", "\"")
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

// SplitDeclarations splits a comma-separated list of variable declarations while preserving commas inside quotes and parentheses.
func SplitDeclarations(s string) []string {
	var parts []string
	var cur strings.Builder
	inParen := 0
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inQuote {
			cur.WriteByte(ch)
			if ch == quoteChar {
				if i+1 < len(s) && s[i+1] == quoteChar {
					i++
					cur.WriteByte(s[i])
				} else {
					inQuote = false
				}
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			inQuote = true
			quoteChar = ch
			cur.WriteByte(ch)
			continue
		}
		if ch == '(' {
			inParen++
			cur.WriteByte(ch)
			continue
		}
		if ch == ')' {
			if inParen > 0 {
				inParen--
			}
			cur.WriteByte(ch)
			continue
		}
		if ch == ',' && inParen == 0 {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

func splitDeclarations(s string) []string {
	return SplitDeclarations(s)
}



