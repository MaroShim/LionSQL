package sqlutil

import (
	"strings"
)

// StripParentheses safely strips outer matching parentheses if and only if
// the outermost '(' at index 0 is balanced exactly by the ')' at the end of the string.
// For example:
//   - "(a + b)" -> "a + b", true
//   - "((a))"   -> "(a)", true
//   - "(a) + (b)" -> "(a) + (b)", false (because the first paren closes early)
func StripParentheses(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "(") {
		return trimmed, false
	}
	end := FindMatchingParen(trimmed, 0)
	if end == len(trimmed)-1 {
		return strings.TrimSpace(trimmed[1 : len(trimmed)-1]), true
	}
	return trimmed, false
}

// CleanLeafIdentifier strips quotes/brackets and returns the final column name after any table/schema prefix.
// Example: "[t].[status]" -> "status", "schema.table.col" -> "col".
func CleanLeafIdentifier(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, "[]\"'`")
	if idx := strings.LastIndex(name, "->>"); idx != -1 {
		return strings.Trim(strings.TrimSpace(name[idx+3:]), "[]\"'`")
	}
	if dot := strings.LastIndex(name, "."); dot != -1 {
		name = name[dot+1:]
	}
	return strings.Trim(strings.TrimSpace(name), "[]\"'`")
}

// InPredicate represents a parsed SQL IN predicate: "column [NOT] IN (val1, val2, ...)".
type InPredicate struct {
	Column     string   // Cleaned leaf column name (e.g. "status")
	FullColumn string   // Cleaned multipart name (e.g. "t.status")
	RawCol     string   // Original column expression (e.g. "[t].[status]")
	IsNot      bool     // true if NOT IN
	ListRaw    string   // Inner contents of parentheses
	Items      []string // Top-level split list items
}

// ParseInPredicate extracts an IN or NOT IN clause from s at top-level depth (depth 0).
func ParseInPredicate(s string) (*InPredicate, bool) {
	s = strings.TrimSpace(s)
	inIdx := FindTopLevelKeyword(s, "IN")
	if inIdx == -1 {
		return nil, false
	}
	left := strings.TrimSpace(s[:inIdx])
	right := strings.TrimSpace(s[inIdx+2:])

	isNot := false
	upperLeft := strings.ToUpper(left)
	if strings.HasSuffix(upperLeft, "NOT") {
		nLen := len(left)
		if nLen == 3 || isWhitespaceByte(left[nLen-4]) {
			isNot = true
			left = strings.TrimSpace(left[:nLen-3])
		}
	}

	inner, ok := StripParentheses(right)
	if !ok {
		return nil, false
	}

	// Subquery IN (e.g. IN (SELECT ...)) is not a static item list
	if HasPrefixKeyword(inner, "SELECT") {
		return nil, false
	}

	items := SplitTopLevel(inner, ',')
	return &InPredicate{
		Column:     CleanLeafIdentifier(left),
		FullColumn: CleanIdentifierParts(left),
		RawCol:     left,
		IsNot:      isNot,
		ListRaw:    inner,
		Items:      items,
	}, true
}

// IsNullPredicate represents a parsed "column IS [NOT] NULL" expression.
type IsNullPredicate struct {
	Column     string // Cleaned leaf column name (e.g. "name")
	FullColumn string // Cleaned multipart name (e.g. "t.name")
	RawCol     string // Original column expression
	IsNot      bool   // true if IS NOT NULL
}

// ParseIsNullPredicate extracts an IS NULL or IS NOT NULL clause from s at depth 0.
func ParseIsNullPredicate(s string) (*IsNullPredicate, bool) {
	s = strings.TrimSpace(s)
	// Check if this condition is compound (contains top-level AND / OR); if so, do not parse as simple IsNull
	if ContainsKeyword(s, "AND", false) || ContainsKeyword(s, "OR", false) {
		return nil, false
	}
	isIdx := FindTopLevelKeyword(s, "IS")
	if isIdx == -1 {
		return nil, false
	}
	left := strings.TrimSpace(s[:isIdx])
	right := strings.TrimSpace(s[isIdx+2:])
	words := strings.Fields(strings.ToUpper(right))
	if len(words) == 2 && words[0] == "NOT" && words[1] == "NULL" {
		return &IsNullPredicate{
			Column:     CleanLeafIdentifier(left),
			FullColumn: CleanIdentifierParts(left),
			RawCol:     left,
			IsNot:      true,
		}, true
	}
	if len(words) == 1 && words[0] == "NULL" {
		return &IsNullPredicate{
			Column:     CleanLeafIdentifier(left),
			FullColumn: CleanIdentifierParts(left),
			RawCol:     left,
			IsNot:      false,
		}, true
	}
	return nil, false
}

// AnyPredicate represents a parsed "column (=|!=|<>) ANY(...)" expression.
type AnyPredicate struct {
	Column     string   // Cleaned leaf column name
	FullColumn string   // Cleaned multipart name
	RawCol     string   // Original column expression
	Op         string   // "=", "!=", or "<>"
	ListRaw    string   // Cleaned list expression
	Items      []string // Top-level split list items
}

// ParseAnyPredicate extracts an ANY clause (e.g. col = ANY(...)) from s at depth 0.
func ParseAnyPredicate(s string) (*AnyPredicate, bool) {
	s = strings.TrimSpace(s)
	anyIdx := FindTopLevelKeyword(s, "ANY")
	if anyIdx == -1 {
		return nil, false
	}
	left := strings.TrimSpace(s[:anyIdx])
	right := strings.TrimSpace(s[anyIdx+3:])

	op := "="
	if strings.HasSuffix(left, "!=") {
		op = "!="
		left = strings.TrimSpace(left[:len(left)-2])
	} else if strings.HasSuffix(left, "<>") {
		op = "<>"
		left = strings.TrimSpace(left[:len(left)-2])
	} else if strings.HasSuffix(left, ">=") {
		op = ">="
		left = strings.TrimSpace(left[:len(left)-2])
	} else if strings.HasSuffix(left, "<=") {
		op = "<="
		left = strings.TrimSpace(left[:len(left)-2])
	} else if strings.HasSuffix(left, ">") {
		op = ">"
		left = strings.TrimSpace(left[:len(left)-1])
	} else if strings.HasSuffix(left, "<") {
		op = "<"
		left = strings.TrimSpace(left[:len(left)-1])
	} else if strings.HasSuffix(left, "=") {
		op = "="
		left = strings.TrimSpace(left[:len(left)-1])
	} else {
		return nil, false
	}

	inner, ok := StripParentheses(right)
	if !ok {
		return nil, false
	}
	inner = strings.TrimSpace(inner)
	// Handle PostgreSQL literal formats: ARRAY['a','b'], '{a,b}'
	if strings.HasPrefix(strings.ToUpper(inner), "ARRAY[") && strings.HasSuffix(inner, "]") {
		inner = inner[6 : len(inner)-1]
	} else if (strings.HasPrefix(inner, "'{") && strings.HasSuffix(inner, "}'")) ||
		(strings.HasPrefix(inner, "\"{") && strings.HasSuffix(inner, "}\"")) {
		inner = inner[2 : len(inner)-2]
	} else if strings.HasPrefix(inner, "{") && strings.HasSuffix(inner, "}") {
		inner = inner[1 : len(inner)-1]
	}

	rawItems := SplitTopLevel(inner, ',')
	items := make([]string, 0, len(rawItems))
	for _, it := range rawItems {
		items = append(items, strings.Trim(strings.TrimSpace(it), "'\""))
	}
	return &AnyPredicate{
		Column:     CleanLeafIdentifier(left),
		FullColumn: CleanIdentifierParts(left),
		RawCol:     left,
		Op:         op,
		ListRaw:    inner,
		Items:      items,
	}, true
}

// BetweenPredicate represents a parsed "column [NOT] BETWEEN low AND high" expression.
type BetweenPredicate struct {
	Column     string // Cleaned leaf column name
	FullColumn string // Cleaned multipart name
	RawCol     string // Original column expression
	IsNot      bool   // true if NOT BETWEEN
	Low        string
	High       string
}

// ParseBetweenPredicate extracts a BETWEEN or NOT BETWEEN clause from s at depth 0.
func ParseBetweenPredicate(s string) (*BetweenPredicate, bool) {
	s = strings.TrimSpace(s)
	betIdx := FindTopLevelKeyword(s, "BETWEEN")
	if betIdx == -1 {
		return nil, false
	}
	left := strings.TrimSpace(s[:betIdx])
	rest := strings.TrimSpace(s[betIdx+7:])

	isNot := false
	upperLeft := strings.ToUpper(left)
	if strings.HasSuffix(upperLeft, "NOT") {
		nLen := len(left)
		if nLen == 3 || isWhitespaceByte(left[nLen-4]) {
			isNot = true
			left = strings.TrimSpace(left[:nLen-3])
		}
	}

	andIdx := FindTopLevelKeyword(rest, "AND")
	if andIdx == -1 {
		return nil, false
	}

	low := strings.TrimSpace(rest[:andIdx])
	high := strings.TrimSpace(rest[andIdx+3:])

	return &BetweenPredicate{
		Column:     CleanLeafIdentifier(left),
		FullColumn: CleanIdentifierParts(left),
		RawCol:     left,
		IsNot:      isNot,
		Low:        low,
		High:       high,
	}, true
}

// BinaryComparison represents a parsed binary comparison: "left op right".
type BinaryComparison struct {
	Column     string // Cleaned leaf column identifier (e.g. "relname")
	FullColumn string // Cleaned multipart identifier (e.g. "c.relname")
	RawCol     string // Raw left expression (e.g. "c.relname" or "doc->>'key'")
	Op         string // Standardized uppercase operator ("=", "!=", "<>", ">=", "<=", ">", "<", "LIKE", "NOT LIKE", "ILIKE", "NOT ILIKE", "~", "~*", "!~", "!~*")
	RightRaw   string // Raw right expression (e.g. "'Orders'")
	RightClean string // Cleaned right string with quotes stripped (e.g. "Orders")
}

// ParseBinaryComparison extracts a binary comparison operation from s at depth 0.
func ParseBinaryComparison(s string) (*BinaryComparison, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}

	n := len(s)
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false
	inLineComment := false
	inBlockComment := false

	// Comparison operators to test at depth 0, ordered by descending length.
	ops := []string{
		"NOT ILIKE", "NOT LIKE", "ILIKE", "LIKE",
		"!~*", "!~", "~*", "~",
		"!=", "<>", ">=", "<=",
		"=", ">", "<",
	}

	for i := 0; i < n; i++ {
		ch := s[i]

		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < n && s[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if ch == '-' && i+1 < n && s[i+1] == '-' {
			inLineComment = true
			i++
			continue
		}
		if ch == '/' && i+1 < n && s[i+1] == '*' {
			inBlockComment = true
			i++
			continue
		}

		if inSingleQuote {
			if ch == '\'' {
				if i+1 < n && s[i+1] == '\'' {
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			if ch == '"' {
				if i+1 < n && s[i+1] == '"' {
					i++
					continue
				}
				inDoubleQuote = false
			}
			continue
		}
		if inBracket {
			if ch == ']' {
				inBracket = false
			}
			continue
		}

		if ch == '\'' {
			inSingleQuote = true
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			continue
		}
		if ch == '[' {
			inBracket = true
			continue
		}
		if ch == '(' {
			depth++
			continue
		}
		if ch == ')' {
			if depth > 0 {
				depth--
			}
			continue
		}

		if depth == 0 {
			rem := s[i:]
			remUpper := strings.ToUpper(rem)
			for _, op := range ops {
				if strings.HasPrefix(remUpper, op) {
					// Avoid matching '>' inside PostgreSQL JSON operators '->' and '->>'
					if (op == ">" || op == ">=") && i > 0 && (s[i-1] == '-' || s[i-1] == '>') {
						continue
					}
					opLen := len(op)
					isKeyword := strings.Contains(op, "LIKE")
					if isKeyword {
						if i > 0 && !isWhitespaceByte(s[i-1]) {
							continue
						}
						if i+opLen < n && !isWhitespaceByte(s[i+opLen]) {
							continue
						}
					}
					left := strings.TrimSpace(s[:i])
					right := strings.TrimSpace(s[i+opLen:])
					// Strip trailing SQL line comment from right if present
					if commentIdx := strings.Index(right, "--"); commentIdx != -1 {
						right = strings.TrimSpace(right[:commentIdx])
					}
					if left != "" && right != "" {
						fullCol := CleanIdentifierParts(left)
						if strings.Contains(left, "->") {
							fullCol = left
						}
						return &BinaryComparison{
							Column:     CleanLeafIdentifier(left),
							FullColumn: fullCol,
							RawCol:     left,
							Op:         op,
							RightRaw:   right,
							RightClean: strings.Trim(right, "'\""),
						}, true
					}
				}
			}
		}
	}

	return nil, false
}

// DesugarBareTableIn rewrites SQLite-style bare table IN expressions:
// `WHERE x IN table` -> `WHERE x IN (SELECT * FROM table)`
// without using regular expressions, respecting string literals, comments, and identifiers.
func DesugarBareTableIn(sql string) string {
	upper := strings.ToUpper(sql)
	if !strings.Contains(upper, "IN ") && !strings.Contains(upper, "IN\t") && !strings.Contains(upper, "IN\n") {
		return sql
	}

	tokens := SplitTopLevelTokens(sql)
	if len(tokens) < 3 {
		return sql
	}

	// Reserved keywords that cannot be bare table names
	reserved := map[string]bool{
		"SELECT": true, "WHERE": true, "ORDER": true, "GROUP": true,
		"HAVING": true, "LIMIT": true, "FROM": true, "JOIN": true,
		"SET": true, "VALUES": true, "CASE": true, "WHEN": true,
		"THEN": true, "ELSE": true, "END": true, "AND": true,
		"OR": true, "NOT": true, "NULL": true, "TRUE": true, "FALSE": true,
	}

	var sb strings.Builder
	n := len(sql)
	inSingleQuote := false
	inDoubleQuote := false
	inLineComment := false
	inBlockComment := false
	depth := 0

	for i := 0; i < n; i++ {
		ch := sql[i]

		// Comments
		if inLineComment {
			sb.WriteByte(ch)
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			sb.WriteByte(ch)
			if ch == '*' && i+1 < n && sql[i+1] == '/' {
				sb.WriteByte('/')
				i++
				inBlockComment = false
			}
			continue
		}
		if ch == '-' && i+1 < n && sql[i+1] == '-' {
			sb.WriteString("--")
			i++
			inLineComment = true
			continue
		}
		if ch == '/' && i+1 < n && sql[i+1] == '*' {
			sb.WriteString("/*")
			i++
			inBlockComment = true
			continue
		}

		// Quotes
		if inSingleQuote {
			sb.WriteByte(ch)
			if ch == '\'' {
				if i+1 < n && sql[i+1] == '\'' {
					sb.WriteByte('\'')
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			sb.WriteByte(ch)
			if ch == '"' {
				if i+1 < n && sql[i+1] == '"' {
					sb.WriteByte('"')
					i++
					continue
				}
				inDoubleQuote = false
			}
			continue
		}
		if ch == '\'' {
			inSingleQuote = true
			sb.WriteByte(ch)
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			sb.WriteByte(ch)
			continue
		}

		// Track parentheses depth
		if ch == '(' {
			depth++
			sb.WriteByte(ch)
			continue
		}
		if ch == ')' {
			if depth > 0 {
				depth--
			}
			sb.WriteByte(ch)
			continue
		}

		// Check for word IN
		if (i == 0 || IsWordBoundary(sql[i-1])) && (ch == 'I' || ch == 'i') && i+1 < n && (sql[i+1] == 'N' || sql[i+1] == 'n') && (i+2 == n || IsWordBoundary(sql[i+2])) {
			// Found IN keyword outside quotes/comments
			sb.WriteString(sql[i : i+2])
			i++ // advance past 'N'

			// Scan forward to see the next non-whitespace token
			j := i + 1
			for j < n && (sql[j] == ' ' || sql[j] == '\t' || sql[j] == '\n' || sql[j] == '\r') {
				j++
			}

			if j < n && sql[j] != '(' && sql[j] != '.' {
				// Read identifier
				startIdent := j
				for j < n && (IsIdentChar(sql[j])) {
					j++
				}
				ident := sql[startIdent:j]
				if len(ident) > 0 && !reserved[strings.ToUpper(ident)] {
					// Check character right after identifier is not '(' or '.'
					k := j
					for k < n && (sql[k] == ' ' || sql[k] == '\t' || sql[k] == '\n' || sql[k] == '\r') {
						k++
					}
					if k == n || (sql[k] != '(' && sql[k] != '.') {
						// Valid bare table IN!
						// Write spaces between IN and ident
						sb.WriteString(sql[i+1 : startIdent])
						// Desugar to (SELECT * FROM ident)
						sb.WriteString("(SELECT * FROM " + ident + ")")
						i = j - 1
						continue
					}
				}
			}
			continue
		}

		sb.WriteByte(ch)
	}

	return sb.String()
}

// ExtractEqualityFilter scans a SQL string for an equality predicate:
// `<targetCol> = <literal>` or `<qualifier>.<targetCol> = <literal>`
// respecting Dyck-balanced parentheses, string literals, and comments.
// It returns the extracted string or integer literal (quotes and N prefixes stripped).
func ExtractEqualityFilter(sql string, targetCols ...string) (string, bool) {
	if len(sql) == 0 || len(targetCols) == 0 {
		return "", false
	}

	targetMap := make(map[string]bool, len(targetCols))
	for _, c := range targetCols {
		targetMap[strings.ToUpper(c)] = true
	}

	// We scan top-level binary comparisons
	n := len(sql)
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false
	inLineComment := false
	inBlockComment := false
	depth := 0

	for i := 0; i < n; i++ {
		ch := sql[i]

		// Comments
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < n && sql[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if ch == '-' && i+1 < n && sql[i+1] == '-' {
			inLineComment = true
			i++
			continue
		}
		if ch == '/' && i+1 < n && sql[i+1] == '*' {
			inBlockComment = true
			i++
			continue
		}

		// Quotes & Brackets
		if inSingleQuote {
			if ch == '\'' {
				if i+1 < n && sql[i+1] == '\'' {
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			if ch == '"' {
				if i+1 < n && sql[i+1] == '"' {
					i++
					continue
				}
				inDoubleQuote = false
			}
			continue
		}
		if inBracket {
			if ch == ']' {
				inBracket = false
			}
			continue
		}

		if ch == '\'' {
			inSingleQuote = true
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			continue
		}
		if ch == '[' {
			inBracket = true
			continue
		}

		if ch == '(' {
			depth++
			continue
		}
		if ch == ')' {
			if depth > 0 {
				depth--
			}
			continue
		}

		// Check for '=' operator at depth 0 only (prevent accidental matches inside subqueries)
		if depth == 0 && ch == '=' && i+1 < n && sql[i+1] != '=' && (i == 0 || (sql[i-1] != '!' && sql[i-1] != '<' && sql[i-1] != '>' && sql[i-1] != ':')) {
			// Find left-hand side token
			leftEnd := i - 1
			for leftEnd >= 0 && (sql[leftEnd] == ' ' || sql[leftEnd] == '\t' || sql[leftEnd] == '\n' || sql[leftEnd] == '\r') {
				leftEnd--
			}
			leftStart := leftEnd
			for leftStart >= 0 && (IsIdentChar(sql[leftStart]) || sql[leftStart] == '.' || sql[leftStart] == '[' || sql[leftStart] == ']') {
				leftStart--
			}
			leftIdent := strings.Trim(sql[leftStart+1:leftEnd+1], "[] ")
			if idx := strings.LastIndex(leftIdent, "."); idx != -1 {
				leftIdent = strings.Trim(leftIdent[idx+1:], "[] ")
			}

			// Find right-hand side token
			rightStart := i + 1
			for rightStart < n && (sql[rightStart] == ' ' || sql[rightStart] == '\t' || sql[rightStart] == '\n' || sql[rightStart] == '\r') {
				rightStart++
			}

			if targetMap[strings.ToUpper(leftIdent)] && rightStart < n {
				// Parse right value (literal string '...', N'...', or number)
				val := ""
				curr := rightStart
				if curr < n && (sql[curr] == 'N' || sql[curr] == 'n') && curr+1 < n && sql[curr+1] == '\'' {
					curr++ // skip N prefix
				}
				if curr < n && sql[curr] == '\'' {
					// Read quoted string
					curr++
					valStart := curr
					for curr < n {
						if sql[curr] == '\'' {
							if curr+1 < n && sql[curr+1] == '\'' {
								curr += 2
								continue
							}
							break
						}
						curr++
					}
					val = strings.ReplaceAll(sql[valStart:curr], "''", "'")
					return val, true
				} else {
					// Read unquoted token/number (supporting decimal '.', negative '-', and identifiers)
					valStart := curr
					if curr < n && (sql[curr] == '-' || sql[curr] == '+') {
						curr++
					}
					for curr < n && (IsIdentChar(sql[curr]) || sql[curr] == '.') {
						curr++
					}
					val = sql[valStart:curr]
					if len(val) > 0 {
						return val, true
					}
				}
			}
		}
	}

	return "", false
}

// ExtractObjectIdCall checks if a condition contains `object_id('table_name')` or `object_id(N'table_name')`.
func ExtractObjectIdCall(sql string) (string, bool) {
	// Look for both "OBJECT_ID" and "SYS.OBJECT_ID"
	candidates := []string{"OBJECT_ID", "SYS.OBJECT_ID"}
	for _, kw := range candidates {
		indices := FindAllTopLevelKeyword(sql, kw)
		for _, idx := range indices {
			rest := sql[idx+len(kw):]
			trimRest := strings.TrimLeft(rest, " \t\r\n")
			if strings.HasPrefix(trimRest, "(") {
				closeP := FindMatchingParen(trimRest, 0)
				if closeP != -1 {
					args := SplitTopLevel(trimRest[1:closeP], ',')
					if len(args) > 0 {
						firstArg := strings.TrimSpace(args[0])
						if strings.HasPrefix(strings.ToUpper(firstArg), "N'") && strings.HasSuffix(firstArg, "'") {
							firstArg = firstArg[1:]
						}
						cleaned := strings.Trim(firstArg, "'\"[] \t\r\n")
						if len(cleaned) > 0 {
							return cleaned, true
						}
					}
				}
			}
		}
	}
	return "", false
}
