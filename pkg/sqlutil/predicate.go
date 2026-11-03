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
	upper := strings.ToUpper(s)
	if strings.HasSuffix(upper, " IS NOT NULL") {
		col := strings.TrimSpace(s[:len(s)-12])
		return &IsNullPredicate{
			Column:     CleanLeafIdentifier(col),
			FullColumn: CleanIdentifierParts(col),
			RawCol:     col,
			IsNot:      true,
		}, true
	}
	if strings.HasSuffix(upper, " IS NULL") {
		col := strings.TrimSpace(s[:len(s)-8])
		return &IsNullPredicate{
			Column:     CleanLeafIdentifier(col),
			FullColumn: CleanIdentifierParts(col),
			RawCol:     col,
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
	inner = strings.TrimPrefix(inner, "ARRAY[")
	inner = strings.TrimPrefix(inner, "array[")
	inner = strings.TrimSuffix(inner, "]")
	inner = strings.Trim(inner, "'\"")
	inner = strings.TrimPrefix(inner, "{")
	inner = strings.TrimSuffix(inner, "}")

	items := SplitTopLevel(inner, ',')
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

	// Comparison operators to test at depth 0, ordered by descending length.
	ops := []string{
		"NOT ILIKE", "NOT LIKE", "ILIKE", "LIKE",
		"!~*", "!~", "~*", "~",
		"!=", "<>", ">=", "<=",
		"=", ">", "<",
	}

	for i := 0; i < n; i++ {
		ch := s[i]

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
