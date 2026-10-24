package sqlutil

import (
	"strings"
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

	n := len(where)
	var conds []string
	start := 0
	depth := 0
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

		if depth == 0 && i+3 <= n {
			word := where[i : i+3]
			if strings.EqualFold(word, "AND") {
				isLeftBoundary := (i == 0 || isWordBoundary(where[i-1]))
				isRightBoundary := (i+3 == n || isWordBoundary(where[i+3]))
				if isLeftBoundary && isRightBoundary {
					cond := strings.TrimSpace(where[start:i])
					if cond != "" {
						conds = append(conds, cond)
					}
					i += 2
					start = i + 1
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

// FindAllTopLevelKeyword returns all starting byte indices of standalone keyword in s
// occurring at depth 0 outside single/double quotes, brackets, comments, and parentheses.
func FindAllTopLevelKeyword(s string, keyword string) []int {
	kLen := len(keyword)
	n := len(s)
	if n < kLen || kLen == 0 {
		return nil
	}

	var indices []int
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i <= n-kLen; i++ {
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

		if depth == 0 {
			if strings.EqualFold(s[i:i+kLen], keyword) {
				leftOk := (i == 0 || isWordBoundary(s[i-1]))
				rightOk := (i+kLen == n || isWordBoundary(s[i+kLen]))
				if leftOk && rightOk {
					indices = append(indices, i)
					i += kLen - 1
				}
			}
		}
	}

	return indices
}

// FindTopLevelKeyword finds the byte index of a keyword (e.g. "FROM") in s where:
// 1. It is outside single/double quotes, brackets, and parentheses (depth == 0).
// 2. It is flanked by word boundaries (spaces, punctuation, or string start/end).
// Returns -1 if not found.
func FindTopLevelKeyword(s string, keyword string) int {
	indices := FindAllTopLevelKeyword(s, keyword)
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

		if depth == 0 && ch == target {
			return i
		}
	}

	return -1
}

func isWordBoundary(ch byte) bool {
	return unicode.IsSpace(rune(ch)) || ch == '(' || ch == ')' || ch == ',' || ch == ';' || ch == '\n' || ch == '\r' || ch == '\t'
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
