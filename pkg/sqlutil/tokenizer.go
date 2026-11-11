package sqlutil

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// OrderByItem represents a single sorted column or expression.
type OrderByItem struct {
	Column string // Clean identifier or expression
	Desc   bool   // true if DESC, false if ASC
}

// SetPair represents a column = value assignment in an UPDATE or MERGE statement.
type SetPair struct {
	Column string // Column name (cleaned)
	Value  string // Expression or literal value
}

// SplitTopLevel splits string s by the given delimiter rune (e.g. comma ',' or semicolon ';')
// ONLY at depth 0, ignoring delimiters inside single quotes ('...'), double quotes ("..."),
// brackets ([...]), line comments (-- ...), block comments (/* ... */), and parentheses ((...)).
// Escaped quotes ('' or "") do not prematurely terminate quoted strings.
// Leading and trailing spaces of each part are trimmed.
func SplitTopLevel(s string, delimiter rune) []string {
	n := len(s)
	if n == 0 {
		return nil
	}

	var parts []string
	start := 0
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < n; i++ {
		ch := s[i]

		// Handle comments
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

		// Handle single quotes ('...') with '' escape
		if inSingleQuote {
			if ch == '\'' {
				if i+1 < n && s[i+1] == '\'' {
					i++ // Skip escaped quote ''
					continue
				}
				inSingleQuote = false
			}
			continue
		}

		// Handle double quotes ("...") with "" escape
		if inDoubleQuote {
			if ch == '"' {
				if i+1 < n && s[i+1] == '"' {
					i++ // Skip escaped quote ""
					continue
				}
				inDoubleQuote = false
			}
			continue
		}

		// Handle bracket identifiers ([...])
		if inBracket {
			if ch == ']' {
				if i+1 < n && s[i+1] == ']' {
					i++ // Skip escaped bracket ]]
					continue
				}
				inBracket = false
			}
			continue
		}

		// Check start of comments or quotes outside any quote/bracket
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

		// Check parentheses depth
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

		// Delimiter check at depth 0
		if depth == 0 && rune(ch) == delimiter {
			part := strings.TrimSpace(s[start:i])
			parts = append(parts, part)
			start = i + 1
		}
	}

	if start <= n {
		part := strings.TrimSpace(s[start:n])
		if part != "" || len(parts) > 0 {
			parts = append(parts, part)
		}
	}

	return parts
}

// SplitTopLevelTokens splits s by whitespace at depth 0, preserving tokens inside quotes and parentheses.
func SplitTopLevelTokens(s string) []string {
	n := len(s)
	if n == 0 {
		return nil
	}

	var tokens []string
	start := -1
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false

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
				if i+1 < n && s[i+1] == ']' {
					i++
					continue
				}
				inBracket = false
			}
			continue
		}

		if ch == '\'' {
			inSingleQuote = true
			if start == -1 {
				start = i
			}
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			if start == -1 {
				start = i
			}
			continue
		}
		if ch == '[' {
			inBracket = true
			if start == -1 {
				start = i
			}
			continue
		}

		if ch == '(' {
			depth++
			if start == -1 {
				start = i
			}
			continue
		}
		if ch == ')' {
			if depth > 0 {
				depth--
			}
			continue
		}

		isSpace := unicode.IsSpace(rune(ch))
		if depth == 0 {
			if isSpace {
				if start != -1 {
					tokens = append(tokens, s[start:i])
					start = -1
				}
			} else {
				if start == -1 {
					start = i
				}
			}
		}
	}

	if start != -1 {
		tokens = append(tokens, s[start:n])
	}

	return tokens
}

// ParseOrderBy parses an ORDER BY clause string (e.g. "col1, col2 DESC, COALESCE(col3, 0) ASC")
// into a slice of OrderByItem structs. It correctly handles multiple columns, commas inside functions,
// bracket identifiers, and case-insensitive ASC/DESC directions.
func ParseOrderBy(clause string) []OrderByItem {
	clause = strings.TrimSpace(clause)
	if clause == "" {
		return nil
	}

	parts := SplitTopLevel(clause, ',')
	items := make([]OrderByItem, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		tokens := SplitTopLevelTokens(part)
		if len(tokens) == 0 {
			continue
		}

		desc := false
		colTokens := tokens

		if len(tokens) >= 2 {
			lastToken := tokens[len(tokens)-1]
			if strings.EqualFold(lastToken, "DESC") {
				desc = true
				colTokens = tokens[:len(tokens)-1]
			} else if strings.EqualFold(lastToken, "ASC") {
				desc = false
				colTokens = tokens[:len(tokens)-1]
			}
		}

		colExpr := strings.TrimSpace(strings.Join(colTokens, " "))
		if colExpr == "" {
			continue
		}

		items = append(items, OrderByItem{
			Column: colExpr,
			Desc:   desc,
		})
	}

	return items
}

// SplitAndConditions splits a WHERE clause on top-level AND keywords,
// strictly ignoring AND inside quotes ('...', "..."), brackets ([...]), and parentheses ((...)).
// It guarantees that escaped quotes ('' or "") do not falsely toggle quote state.
func SplitAndConditions(where string) []string {
	where = strings.TrimSpace(where)
	if where == "" {
		return nil
	}

	// If WHERE clause contains unparenthesized OR at depth 0,
	// the top-level expression is a disjunction and cannot be split by AND.
	if HasTopLevelOr(where) {
		return []string{where}
	}

	n := len(where)
	var conds []string
	start := 0
	depth := 0
	betweenCount := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false

	for i := 0; i < n; i++ {
		ch := where[i]

		if inSingleQuote {
			if ch == '\'' {
				if i+1 < n && where[i+1] == '\'' {
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			if ch == '"' {
				if i+1 < n && where[i+1] == '"' {
					i++
					continue
				}
				inDoubleQuote = false
			}
			continue
		}
		if inBracket {
			if ch == ']' {
				if i+1 < n && where[i+1] == ']' {
					i++
					continue
				}
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

		if depth == 0 && i+7 <= n {
			word := where[i : i+7]
			if strings.EqualFold(word, "BETWEEN") {
				isLeftBoundary := (i == 0 || isWordBoundary(where[i-1]))
				isRightBoundary := (i+7 == n || isWordBoundary(where[i+7]))
				if isLeftBoundary && isRightBoundary {
					betweenCount++
					i += 6
					continue
				}
			}
		}

		if depth == 0 && i+3 <= n {
			word := where[i : i+3]
			if strings.EqualFold(word, "AND") {
				isLeftBoundary := (i == 0 || isWordBoundary(where[i-1]))
				isRightBoundary := (i+3 == n || isWordBoundary(where[i+3]))
				if isLeftBoundary && isRightBoundary {
					if betweenCount > 0 {
						betweenCount--
						i += 2
						continue
					}
					cond := strings.TrimSpace(where[start:i])
					if cond != "" {
						conds = append(conds, cond)
					}
					i += 2
					start = i + 1
					continue
				}
			}
		}
	}

	if start < n {
		cond := strings.TrimSpace(where[start:n])
		if cond != "" {
			conds = append(conds, cond)
		}
	}

	return conds
}

// HasTopLevelOr checks if a SQL expression contains an unparenthesized, unquoted OR keyword at depth 0.
func HasTopLevelOr(s string) bool {
	n := len(s)
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false

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

		if depth == 0 && i+2 <= n {
			word := s[i : i+2]
			if strings.EqualFold(word, "OR") {
				isLeftBoundary := (i == 0 || isWordBoundary(s[i-1]))
				isRightBoundary := (i+2 == n || isWordBoundary(s[i+2]))
				if isLeftBoundary && isRightBoundary {
					return true
				}
			}
		}
	}
	return false
}

// SplitSetClauses parses comma-separated assignment expressions in an UPDATE or MERGE statement
// (e.g. "name = 'Doe, John', count = count + 1, score = COALESCE(src.score, 0)").
// It correctly handles commas inside quotes and parentheses and returns structured SetPair items.
func SplitSetClauses(setClause string) []SetPair {
	rawPairs := SplitTopLevel(setClause, ',')
	var pairs []SetPair

	for _, p := range rawPairs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		eqIdx := FindTopLevelChar(p, '=')
		if eqIdx != -1 {
			col := strings.TrimSpace(p[:eqIdx])
			val := strings.TrimSpace(p[eqIdx+1:])
			pairs = append(pairs, SetPair{
				Column: CleanIdentifier(col),
				Value:  val,
			})
		}
	}

	return pairs
}

// SplitAsAlias checks for a top-level " AS " keyword outside parentheses, brackets, and quotes.
// If found, it returns the trimmed expression, the unquoted alias, and true.
// Otherwise, it returns the original item trimmed, empty alias, and false.
func SplitAsAlias(item string) (expr string, alias string, hasAs bool) {
	item = strings.TrimSpace(item)
	n := len(item)
	if n == 0 {
		return "", "", false
	}

	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false

	for i := 0; i < n; i++ {
		ch := item[i]

		if inSingleQuote {
			if ch == '\'' {
				if i+1 < n && item[i+1] == '\'' {
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			if ch == '"' {
				if i+1 < n && item[i+1] == '"' {
					i++
					continue
				}
				inDoubleQuote = false
			}
			continue
		}
		if inBracket {
			if ch == ']' {
				if i+1 < n && item[i+1] == ']' {
					i++
					continue
				}
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

		if depth == 0 && i+4 <= n {
			candidate := item[i : i+4]
			if strings.EqualFold(candidate, " AS ") {
				expr = strings.TrimSpace(item[:i])
				rawAlias := strings.TrimSpace(item[i+4:])
				alias = CleanIdentifier(rawAlias)
				return expr, alias, true
			}
		}
	}

	return item, "", false
}

func isWhitespaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

// matchWordsAt checks if words match in sequence starting at index start in s.
// Between consecutive words, arbitrary whitespace (spaces, tabs, newlines) is accepted.
// Returns the index after the matched sequence and true on success.
func matchWordsAt(s string, start int, words []string) (int, bool) {
	n := len(s)
	curr := start

	// Check left boundary for first word
	if curr > 0 && !isWordBoundary(s[curr-1]) {
		return 0, false
	}

	for wIdx, word := range words {
		if wIdx > 0 {
			// Between words, there MUST be at least one whitespace character
			if curr >= n || !isWhitespaceByte(s[curr]) {
				return 0, false
			}
			for curr < n && isWhitespaceByte(s[curr]) {
				curr++
			}
		}

		wLen := len(word)
		if curr+wLen > n {
			return 0, false
		}
		if !strings.EqualFold(s[curr:curr+wLen], word) {
			return 0, false
		}
		curr += wLen
		// Right boundary of this word
		if curr < n && !isWordBoundary(s[curr]) {
			return 0, false
		}
	}

	return curr, true
}

// HasPrefixKeyword checks whether the statement starts with keyword at top level,
// ignoring leading whitespace and SQL comments (-- ... and /* ... */).
// Multi-word keywords (e.g. "CREATE TABLE") accept arbitrary whitespace between words.
func HasPrefixKeyword(s string, keyword string) bool {
	n := len(s)
	if n == 0 {
		return false
	}
	words := strings.Fields(keyword)
	if len(words) == 0 {
		return false
	}

	inLineComment := false
	inBlockComment := false

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

		if isWhitespaceByte(ch) {
			continue
		}

		// First non-whitespace, non-comment byte reached.
		_, ok := matchWordsAt(s, i, words)
		return ok
	}
	return false
}

// scanKeyword scans s for standalone occurrences of keyword outside single quotes,
// double quotes, brackets, and comments.
// If keyword consists of multiple words (e.g. "IF EXISTS", "WITHIN GROUP"), arbitrary
// whitespace (spaces, tabs, newlines) between the words is transparently matched.
// If allowInsideParens is true, matches inside nested parentheses are accepted.
// If firstOnly is true, scanning stops immediately upon the first match.
func scanKeyword(s string, keyword string, allowInsideParens bool, firstOnly bool) []int {
	words := strings.Fields(keyword)
	if len(words) == 0 {
		return nil
	}
	n := len(s)
	firstWordLen := len(words[0])
	if n < firstWordLen {
		return nil
	}

	var indices []int
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i <= n-firstWordLen; i++ {
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
				if i+1 < n && s[i+1] == ']' {
					i++
					continue
				}
				inBracket = false
			}
			continue
		}

		// Check start of comments or quotes outside any quote/bracket
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

		if depth == 0 || allowInsideParens {
			if end, ok := matchWordsAt(s, i, words); ok {
				indices = append(indices, i)
				if firstOnly {
					return indices
				}
				i = end - 1
			}
		}
	}

	return indices
}

// FindAllTopLevelKeyword returns all starting byte indices of standalone keyword in s
// occurring at depth 0 outside single/double quotes, brackets, comments, and parentheses.
func FindAllTopLevelKeyword(s string, keyword string) []int {
	return scanKeyword(s, keyword, false, false)
}

// ContainsKeyword reports whether keyword appears as a standalone token in s
// outside single quotes, double quotes, brackets, and comments.
// If allowInsideParens is true, occurrences inside nested parentheses (e.g. "(a = 1 OR b = 2)")
// are matched; otherwise only depth-0 occurrences are matched.
func ContainsKeyword(s string, keyword string, allowInsideParens bool) bool {
	return len(scanKeyword(s, keyword, allowInsideParens, true)) > 0
}

// FindTopLevelKeyword finds the byte index of a keyword (e.g. "FROM") in s where:
// 1. It is outside single/double quotes, brackets, and parentheses (depth == 0).
// 2. It is flanked by word boundaries (spaces, punctuation, or string start/end).
// Returns -1 if not found.
func FindTopLevelKeyword(s string, keyword string) int {
	indices := scanKeyword(s, keyword, false, true)
	if len(indices) == 0 {
		return -1
	}
	return indices[0]
}

// SplitJoinCond splits a simple or parenthesized join condition (e.g. "a.id = b.id", "(a.id = b.id)")
// by the top-level '=' operator. It returns leftKey and rightKey trimmed, or empty strings if invalid.
func SplitJoinCond(cond string) (string, string) {
	cond = strings.TrimSpace(cond)
	// Strip optional outer parentheses if wrapping the entire condition
	for strings.HasPrefix(cond, "(") && strings.HasSuffix(cond, ")") {
		// Only strip if parentheses match depth 0
		trimmed := strings.TrimSpace(cond[1 : len(cond)-1])
		if isBalanced(trimmed) {
			cond = trimmed
		} else {
			break
		}
	}

	eqIdx := FindTopLevelChar(cond, '=')
	if eqIdx != -1 {
		left := strings.TrimSpace(cond[:eqIdx])
		right := strings.TrimSpace(cond[eqIdx+1:])
		return left, right
	}

	return "", ""
}

// CleanIdentifier strips quotes (`"`, `'`), backticks (```), and brackets (`[`, `]`) from an identifier.
func CleanIdentifier(s string) string {
	s = strings.TrimSpace(s)
	return strings.Trim(s, "\"`'[]")
}

// FindTopLevelChar finds the byte index of target in s outside quotes, brackets, and parentheses (depth == 0).
func FindTopLevelChar(s string, target byte) int {
	n := len(s)
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false

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
				if i+1 < n && s[i+1] == ']' {
					i++
					continue
				}
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

		if depth == 0 && ch == target {
			return i
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
	}

	return -1
}

// IsWordBoundary returns true if ch represents a word boundary separator in SQL.
func IsWordBoundary(ch byte) bool {
	return isWordBoundary(ch)
}

// IsIdentChar returns true if ch is a valid character inside an unquoted SQL identifier.
func IsIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
}

func isWordBoundary(ch byte) bool {
	return unicode.IsSpace(rune(ch)) || ch == '(' || ch == ')' || ch == ',' || ch == ';' || ch == '\n' || ch == '\r' || ch == '\t' ||
		ch == '[' || ch == ']' || ch == '=' || ch == '<' || ch == '>' || ch == '!' || ch == '+' || ch == '-' || ch == '*' || ch == '/' || ch == '%'
}

func isBalanced(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '(' {
			depth++
		} else if s[i] == ')' {
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// FindMatchingParen finds the index of the matching closing parenthesis ')' for the '(' at openIdx,
// safely skipping quotes ('...', "..."), brackets ([...]), comments, and nested parentheses.
// Returns -1 if no matching parenthesis is found.
func FindMatchingParen(s string, openIdx int) int {
	n := len(s)
	if openIdx < 0 || openIdx >= n || s[openIdx] != '(' {
		return -1
	}

	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false
	inLineComment := false
	inBlockComment := false

	for i := openIdx; i < n; i++ {
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
				if i+1 < n && s[i+1] == ']' {
					i++
					continue
				}
				inBracket = false
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
			depth--
			if depth == 0 {
				return i
			}
			if depth < 0 {
				return -1
			}
		}
	}

	return -1
}

// SplitTopLevelKeyword splits string s by a standalone keyword (e.g. "AND", "OR") at depth 0,
// safely skipping string literals ('...', "..."), brackets ([...]), comments, and parentheses ((...)).
func SplitTopLevelKeyword(s string, kw string) []string {
	n := len(s)
	kwLen := len(kw)
	if n == 0 || kwLen == 0 {
		return nil
	}

	upperS := strings.ToUpper(s)
	upperKw := strings.ToUpper(kw)

	var parts []string
	start := 0
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false
	inLineComment := false
	inBlockComment := false

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
				if i+1 < n && s[i+1] == ']' {
					i++
					continue
				}
				inBracket = false
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

		if depth == 0 && i+kwLen <= n && upperS[i:i+kwLen] == upperKw {
			beforeOk := i == 0 || isWordBoundary(s[i-1])
			afterOk := i+kwLen == n || isWordBoundary(s[i+kwLen])
			if beforeOk && afterOk {
				part := strings.TrimSpace(s[start:i])
				if part != "" {
					parts = append(parts, part)
				}
				start = i + kwLen
				i += kwLen - 1
			}
		}
	}

	if start <= n {
		part := strings.TrimSpace(s[start:n])
		if part != "" {
			parts = append(parts, part)
		}
	}

	return parts
}

// FindTopLevelArithmeticOp returns the index and operator character of the primary arithmetic operator (+, -, *, /)
// at depth 0, giving precedence to lowest binding operators (+, -) before (*, /).
func FindTopLevelArithmeticOp(s string) (int, byte) {
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false

	addSubIdx := -1
	var addSubOp byte
	mulDivIdx := -1
	var mulDivOp byte

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inSingleQuote {
			if ch == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			if ch == '"' {
				if i+1 < len(s) && s[i+1] == '"' {
					i++
					continue
				}
				inDoubleQuote = false
			}
			continue
		}
		if inBracket {
			if ch == ']' {
				if i+1 < len(s) && s[i+1] == ']' {
					i++
					continue
				}
				inBracket = false
			}
			continue
		}

		switch ch {
		case '\'':
			inSingleQuote = true
		case '"':
			inDoubleQuote = true
		case '[':
			inBracket = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '+', '-':
			if depth == 0 && i > 0 {
				prev := s[i-1]
				if prev != 'e' && prev != 'E' && prev != '+' && prev != '-' && prev != '*' && prev != '/' {
					addSubIdx = i
					addSubOp = ch
				}
			}
		case '*', '/':
			if depth == 0 && i > 0 {
				mulDivIdx = i
				mulDivOp = ch
			}
		}
	}

	if addSubIdx != -1 {
		return addSubIdx, addSubOp
	}
	if mulDivIdx != -1 {
		return mulDivIdx, mulDivOp
	}
	return -1, 0
}

// ExtractTopLevelTuples extracts raw strings of depth-1 parentheses tuples from VALUES clause, e.g. "(1, 'a'), (2, 'b')".
func ExtractTopLevelTuples(s string) []string {
	var tuples []string
	var cur strings.Builder
	inTuple := false
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inSingleQuote {
			cur.WriteByte(c)
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
					cur.WriteByte(s[i])
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			cur.WriteByte(c)
			if c == '"' {
				if i+1 < len(s) && s[i+1] == '"' {
					i++
					cur.WriteByte(s[i])
					continue
				}
				inDoubleQuote = false
			}
			continue
		}

		if c == '\'' {
			inSingleQuote = true
			if inTuple {
				cur.WriteByte(c)
			}
			continue
		}
		if c == '"' {
			inDoubleQuote = true
			if inTuple {
				cur.WriteByte(c)
			}
			continue
		}

		if c == '(' {
			if !inTuple {
				inTuple = true
				depth = 1
				cur.Reset()
				continue
			}
			depth++
			cur.WriteByte(c)
			continue
		}
		if c == ')' {
			if inTuple {
				depth--
				if depth == 0 {
					tuples = append(tuples, cur.String())
					inTuple = false
					cur.Reset()
					continue
				}
				cur.WriteByte(c)
			}
			continue
		}
		if inTuple {
			cur.WriteByte(c)
		}
	}
	return tuples
}

// FormatSQLLiteral converts an arbitrary value into a valid SQL literal representation.
func FormatSQLLiteral(v any) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case string:
		return "'" + strings.ReplaceAll(val, "'", "''") + "'"
	case []byte:
		return "'" + strings.ReplaceAll(string(val), "'", "''") + "'"
	case time.Time:
		return "'" + val.Format("2006-01-02 15:04:05") + "'"
	case bool:
		if val {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprintf("%v", val)
	}
}

// ReplaceCorrelatedTokens tokenizes input SQL and substitutes identifier references outside string literals.
func ReplaceCorrelatedTokens(s string, replacements map[string]string) string {
	n := len(s)
	if n == 0 || len(replacements) == 0 {
		return s
	}

	var sb strings.Builder
	sb.Grow(n + 16)

	inSingleQuote := false

	i := 0
	for i < n {
		ch := s[i]

		if inSingleQuote {
			sb.WriteByte(ch)
			if ch == '\'' {
				if i+1 < n && s[i+1] == '\'' {
					sb.WriteByte(s[i+1])
					i += 2
					continue
				}
				inSingleQuote = false
			}
			i++
			continue
		}

		if ch == '\'' {
			inSingleQuote = true
			sb.WriteByte(ch)
			i++
			continue
		}

		// Match identifier or qualified identifier (e.g. u.id, [u].[id], id, [id], "u"."id")
		if ch == '[' || ch == '"' || ch == '`' || unicode.IsLetter(rune(ch)) || ch == '_' {
			start := i
			for i < n {
				c := s[i]
				if c == '[' {
					i++
					for i < n && s[i] != ']' {
						i++
					}
					if i < n {
						i++
					}
				} else if c == '"' || c == '`' {
					quote := c
					i++
					for i < n && s[i] != quote {
						i++
					}
					if i < n {
						i++
					}
				} else if unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) || c == '_' || c == '.' {
					i++
				} else {
					break
				}
			}
			ident := s[start:i]
			cleanLower := strings.ToLower(CleanIdentifierParts(ident))
			if repl, ok := replacements[cleanLower]; ok {
				sb.WriteString(repl)
			} else if repl, ok := replacements[strings.ToLower(ident)]; ok {
				sb.WriteString(repl)
			} else {
				sb.WriteString(ident)
			}
			continue
		}

		sb.WriteByte(ch)
		i++
	}

	return sb.String()
}

// ParameterizeSubquery substitutes outer row values into a subquery string.
func ParameterizeSubquery(subquerySQL string, row map[string]any) string {
	if len(row) == 0 {
		return subquerySQL
	}
	// Check if row contains qualified keys (e.g. "e.dept_id", "emp.dept_id")
	hasQualified := false
	for k := range row {
		if strings.Contains(k, ".") {
			hasQualified = true
			break
		}
	}

	replacements := make(map[string]string, len(row)*2)
	for k, v := range row {
		// If qualified keys exist, only qualified references should be substituted
		// to prevent corrupting inner subquery unqualified column names.
		if hasQualified && !strings.Contains(k, ".") {
			continue
		}
		lit := FormatSQLLiteral(v)
		normKey := strings.ToLower(CleanIdentifierParts(k))
		replacements[normKey] = lit
		replacements[strings.ToLower(k)] = lit
	}
	return ReplaceCorrelatedTokens(subquerySQL, replacements)
}

// IsCorrelatedSubquery reports whether innerSQL references any of outerNames (e.g. outer.col, [outer].[col]).
func IsCorrelatedSubquery(innerSQL string, outerNames []string) bool {
	if len(outerNames) == 0 {
		return false
	}
	normOuters := make(map[string]bool, len(outerNames))
	for _, o := range outerNames {
		clean := strings.ToLower(CleanIdentifier(o))
		if clean != "" {
			normOuters[clean] = true
		}
	}

	n := len(innerSQL)
	inSingleQuote := false
	i := 0
	for i < n {
		ch := innerSQL[i]
		if inSingleQuote {
			if ch == '\'' {
				if i+1 < n && innerSQL[i+1] == '\'' {
					i += 2
					continue
				}
				inSingleQuote = false
			}
			i++
			continue
		}
		if ch == '\'' {
			inSingleQuote = true
			i++
			continue
		}

		if ch == '[' || ch == '"' || ch == '`' || unicode.IsLetter(rune(ch)) || ch == '_' {
			start := i
			for i < n {
				c := innerSQL[i]
				if c == '[' {
					i++
					for i < n && innerSQL[i] != ']' {
						i++
					}
					if i < n {
						i++
					}
				} else if c == '"' || c == '`' {
					quote := c
					i++
					for i < n && innerSQL[i] != quote {
						i++
					}
					if i < n {
						i++
					}
				} else if unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) || c == '_' || c == '.' {
					i++
				} else {
					break
				}
			}
			token := innerSQL[start:i]
			if dotIdx := strings.Index(token, "."); dotIdx != -1 {
				tblPrefix := strings.ToLower(CleanIdentifier(token[:dotIdx]))
				if normOuters[tblPrefix] {
					return true
				}
			}
			continue
		}
		i++
	}
	return false
}

var volatileFuncs = map[string]bool{
	"RAND":                true,
	"RANDOM":              true,
	"NEWID":               true,
	"NEWID7":              true,
	"NEWSEQUENTIALID":     true,
	"UUID":                true,
	"UUIDV7":              true,
	"UUID_GENERATE_V7":    true,
	"GEN_RANDOM_UUID":     true,
	"GETDATE":             true,
	"GETUTCDATE":          true,
	"SYSDATETIME":         true,
	"SYSUTCDATETIME":      true,
	"SYSDATETIMEOFFSET":   true,
	"CURRENT_TIMESTAMP":   true,
	"NOW":                 true,
	"CLOCK_TIMESTAMP":     true,
	"TIMEOFDAY":           true,
	"STATEMENT_TIMESTAMP": true,
	"SYSDATE":             true,
}

// IsVolatileSubquery reports whether subquerySQL contains non-deterministic functions
// (e.g. RAND, NEWID, UUID, GETDATE, NOW, CURRENT_TIMESTAMP, etc.) that must not be
// memoized across outer tuple iterations or folded into static constants at compile time.
func IsVolatileSubquery(sql string) bool {
	if sql == "" {
		return false
	}
	upper := strings.ToUpper(sql)
	// Fast path: return false if none of the volatile substrings exist
	if !strings.Contains(upper, "RAND") &&
		!strings.Contains(upper, "NEWID") &&
		!strings.Contains(upper, "UUID") &&
		!strings.Contains(upper, "DATE") &&
		!strings.Contains(upper, "TIME") &&
		!strings.Contains(upper, "NOW") {
		return false
	}

	n := len(sql)
	inSingleQuote := false
	i := 0
	for i < n {
		ch := sql[i]
		if inSingleQuote {
			if ch == '\'' {
				if i+1 < n && sql[i+1] == '\'' {
					i += 2
					continue
				}
				inSingleQuote = false
			}
			i++
			continue
		}
		if ch == '\'' {
			inSingleQuote = true
			i++
			continue
		}

		// Skip single-line comments (-- ...)
		if ch == '-' && i+1 < n && sql[i+1] == '-' {
			i += 2
			for i < n && sql[i] != '\n' && sql[i] != '\r' {
				i++
			}
			continue
		}
		// Skip multi-line comments (/* ... */)
		if ch == '/' && i+1 < n && sql[i+1] == '*' {
			i += 2
			for i+1 < n && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			if i+1 < n {
				i += 2
			}
			continue
		}

		if ch == '[' || ch == '"' || ch == '`' || unicode.IsLetter(rune(ch)) || ch == '_' {
			start := i
			for i < n {
				c := sql[i]
				if c == '[' {
					i++
					for i < n && sql[i] != ']' {
						i++
					}
					if i < n {
						i++
					}
				} else if c == '"' || c == '`' {
					quote := c
					i++
					for i < n && sql[i] != quote {
						i++
					}
					if i < n {
						i++
					}
				} else if unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) || c == '_' {
					i++
				} else {
					break
				}
			}
			token := sql[start:i]
			clean := strings.ToUpper(CleanIdentifier(token))
			if volatileFuncs[clean] {
				return true
			}
			continue
		}
		i++
	}
	return false
}

// CleanIdentifierParts strips quotes from each dot-separated segment of an identifier.
func CleanIdentifierParts(name string) string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = CleanIdentifier(p)
	}
	return strings.Join(parts, ".")
}

