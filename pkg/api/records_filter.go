package api

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"sqlion/pkg/storage"
)

// FilterExpr represents a boolean predicate evaluated against a record row.
type FilterExpr interface {
	Matches(row map[string]any, schema *storage.TableSchema) bool
}

// ComparisonFilter evaluates a binary comparison on a table column.
type ComparisonFilter struct {
	Field  string
	Op     string
	RawVal string
}

// AndFilter evaluates the logical conjunction of multiple filter expressions.
type AndFilter struct {
	Exprs []FilterExpr
}

// OrFilter evaluates the logical disjunction of multiple filter expressions.
type OrFilter struct {
	Exprs []FilterExpr
}

func (a *AndFilter) Matches(row map[string]any, schema *storage.TableSchema) bool {
	for _, e := range a.Exprs {
		if !e.Matches(row, schema) {
			return false
		}
	}
	return true
}

func (o *OrFilter) Matches(row map[string]any, schema *storage.TableSchema) bool {
	if len(o.Exprs) == 0 {
		return false
	}
	for _, e := range o.Exprs {
		if e.Matches(row, schema) {
			return true
		}
	}
	return false
}

func (c *ComparisonFilter) Matches(row map[string]any, schema *storage.TableSchema) bool {
	val, exists := getRowValue(row, c.Field)
	op := strings.ToUpper(strings.TrimSpace(c.Op))
	raw := strings.TrimSpace(c.RawVal)

	// Check IS NULL / IS NOT NULL / =null / !=null
	if op == "IS NULL" || (op == "=" && (strings.EqualFold(raw, "null") || strings.EqualFold(raw, "nil"))) {
		return !exists || val == nil
	}
	if op == "IS NOT NULL" || ((op == "!=" || op == "<>") && (strings.EqualFold(raw, "null") || strings.EqualFold(raw, "nil"))) {
		return exists && val != nil
	}

	if !exists || val == nil {
		return false
	}

	cleanRaw := unquote(raw)

	// 1. Try numeric comparison if applicable
	if numVal, ok := toFloat64(val); ok {
		if targetNum, err := strconv.ParseFloat(cleanRaw, 64); err == nil {
			switch op {
			case ">":
				return numVal > targetNum
			case ">=":
				return numVal >= targetNum
			case "<":
				return numVal < targetNum
			case "<=":
				return numVal <= targetNum
			case "=", "==":
				return numVal == targetNum
			case "!=", "<>":
				return numVal != targetNum
			}
		}
	}

	// 2. Try boolean comparison
	if boolVal, ok := toBool(val); ok {
		if strings.EqualFold(cleanRaw, "true") || cleanRaw == "1" {
			targetBool := true
			if op == "=" || op == "==" {
				return boolVal == targetBool
			} else if op == "!=" || op == "<>" {
				return boolVal != targetBool
			}
		} else if strings.EqualFold(cleanRaw, "false") || cleanRaw == "0" {
			targetBool := false
			if op == "=" || op == "==" {
				return boolVal == targetBool
			} else if op == "!=" || op == "<>" {
				return boolVal != targetBool
			}
		}
	}

	// 3. String & pattern comparisons
	strVal := fmt.Sprint(val)
	switch op {
	case "~": // Contains substring
		return strings.Contains(strings.ToLower(strVal), strings.ToLower(cleanRaw))
	case "!~": // Does not contain
		return !strings.Contains(strings.ToLower(strVal), strings.ToLower(cleanRaw))
	case "LIKE": // SQL LIKE pattern
		return matchLike(cleanRaw, strVal)
	case "=", "==":
		return strings.EqualFold(strVal, cleanRaw)
	case "!=", "<>":
		return !strings.EqualFold(strVal, cleanRaw)
	case ">":
		return strings.ToLower(strVal) > strings.ToLower(cleanRaw)
	case ">=":
		return strings.ToLower(strVal) >= strings.ToLower(cleanRaw)
	case "<":
		return strings.ToLower(strVal) < strings.ToLower(cleanRaw)
	case "<=":
		return strings.ToLower(strVal) <= strings.ToLower(cleanRaw)
	default:
		return strings.EqualFold(strVal, cleanRaw)
	}
}

func getRowValue(row map[string]any, field string) (any, bool) {
	if val, ok := row[field]; ok {
		return val, true
	}
	for k, v := range row {
		if strings.EqualFold(k, field) {
			return v, true
		}
	}
	return nil, false
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func toBool(v any) (bool, bool) {
	switch b := v.(type) {
	case bool:
		return b, true
	case int, int64:
		return fmt.Sprintf("%v", b) == "1", true
	case string:
		s := strings.ToLower(strings.TrimSpace(b))
		if s == "true" || s == "1" || s == "t" {
			return true, true
		}
		if s == "false" || s == "0" || s == "f" {
			return false, true
		}
	}
	return false, false
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			unq := s[1 : len(s)-1]
			unq = strings.ReplaceAll(unq, "\\'", "'")
			unq = strings.ReplaceAll(unq, "\\\"", "\"")
			return unq
		}
	}
	return s
}

func matchLike(pattern, s string) bool {
	pattern = strings.ToLower(pattern)
	s = strings.ToLower(s)
	if pattern == "%" {
		return true
	}
	if !strings.Contains(pattern, "%") && !strings.Contains(pattern, "_") {
		return pattern == s
	}

	var buf strings.Builder
	buf.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '%':
			buf.WriteString(".*")
		case '_':
			buf.WriteString(".")
		case '.', '+', '?', '^', '$', '(', ')', '[', ']', '{', '}', '|', '\\':
			buf.WriteByte('\\')
			buf.WriteByte(c)
		default:
			buf.WriteByte(c)
		}
	}
	buf.WriteString("$")
	re, err := regexp.Compile(buf.String())
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// Token types for the filter parser.
type filterTokenType int

const (
	tokIdent filterTokenType = iota
	tokOp
	tokAnd
	tokOr
	tokLParen
	tokRParen
	tokEOF
)

type filterToken struct {
	typ filterTokenType
	val string
}

type filterLexer struct {
	input []rune
	pos   int
}

func newFilterLexer(input string) *filterLexer {
	return &filterLexer{input: []rune(input), pos: 0}
}

func (l *filterLexer) nextToken() filterToken {
	l.skipWhitespace()
	if l.pos >= len(l.input) {
		return filterToken{typ: tokEOF}
	}

	ch := l.input[l.pos]

	if ch == '(' {
		l.pos++
		return filterToken{typ: tokLParen, val: "("}
	}
	if ch == ')' {
		l.pos++
		return filterToken{typ: tokRParen, val: ")"}
	}
	if ch == ',' {
		l.pos++
		return filterToken{typ: tokAnd, val: ","}
	}

	// Logical &&
	if ch == '&' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '&' {
		l.pos += 2
		return filterToken{typ: tokAnd, val: "&&"}
	}

	// Logical ||
	if ch == '|' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '|' {
		l.pos += 2
		return filterToken{typ: tokOr, val: "||"}
	}

	// Comparison operators
	twoCharOps := []string{">=", "<=", "!=", "<>", "!~", "=="}
	for _, op := range twoCharOps {
		if l.matchesString(op) {
			l.pos += len(op)
			return filterToken{typ: tokOp, val: op}
		}
	}

	singleCharOps := []string{">", "<", "=", "~"}
	for _, op := range singleCharOps {
		if l.matchesString(op) {
			l.pos += len(op)
			return filterToken{typ: tokOp, val: op}
		}
	}

	// Quoted string literal
	if ch == '\'' || ch == '"' {
		quote := ch
		l.pos++
		start := l.pos
		var val strings.Builder
		for l.pos < len(l.input) {
			if l.input[l.pos] == '\\' && l.pos+1 < len(l.input) {
				val.WriteRune(l.input[l.pos+1])
				l.pos += 2
				continue
			}
			if l.input[l.pos] == quote {
				l.pos++
				break
			}
			val.WriteRune(l.input[l.pos])
			l.pos++
		}
		_ = start
		return filterToken{typ: tokIdent, val: fmt.Sprintf("%c%s%c", quote, val.String(), quote)}
	}

	// Words, identifiers, numbers
	start := l.pos
	for l.pos < len(l.input) {
		c := l.input[l.pos]
		if isWhitespace(c) || c == '(' || c == ')' || c == ',' || c == '>' || c == '<' || c == '=' || c == '~' || c == '!' || c == '&' || c == '|' {
			break
		}
		l.pos++
	}
	word := string(l.input[start:l.pos])
	upper := strings.ToUpper(word)

	if upper == "AND" {
		return filterToken{typ: tokAnd, val: "AND"}
	}
	if upper == "OR" {
		return filterToken{typ: tokOr, val: "OR"}
	}
	if upper == "LIKE" {
		return filterToken{typ: tokOp, val: "LIKE"}
	}
	if upper == "IS" {
		saved := l.pos
		l.skipWhitespace()
		if l.matchesWord("NOT") {
			l.pos += 3
			l.skipWhitespace()
			if l.matchesWord("NULL") {
				l.pos += 4
				return filterToken{typ: tokOp, val: "IS NOT NULL"}
			}
		} else if l.matchesWord("NULL") {
			l.pos += 4
			return filterToken{typ: tokOp, val: "IS NULL"}
		}
		l.pos = saved
	}

	return filterToken{typ: tokIdent, val: word}
}

func (l *filterLexer) skipWhitespace() {
	for l.pos < len(l.input) && isWhitespace(l.input[l.pos]) {
		l.pos++
	}
}

func (l *filterLexer) matchesString(s string) bool {
	runes := []rune(s)
	if l.pos+len(runes) > len(l.input) {
		return false
	}
	for i, r := range runes {
		if l.input[l.pos+i] != r {
			return false
		}
	}
	return true
}

func (l *filterLexer) matchesWord(s string) bool {
	upperInput := strings.ToUpper(string(l.input[l.pos:]))
	upperTarget := strings.ToUpper(s)
	if strings.HasPrefix(upperInput, upperTarget) {
		rest := upperInput[len(upperTarget):]
		if len(rest) == 0 || isWhitespace(rune(rest[0])) || rest[0] == ')' || rest[0] == ',' {
			return true
		}
	}
	return false
}

func isWhitespace(ch rune) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}

type filterParser struct {
	tokens []filterToken
	pos    int
}

// ParseFilter parses a URL filter string into a FilterExpr.
func ParseFilter(filterStr string) (FilterExpr, error) {
	trimmed := strings.TrimSpace(filterStr)
	if trimmed == "" {
		return nil, nil
	}

	lexer := newFilterLexer(trimmed)
	var tokens []filterToken
	for {
		tok := lexer.nextToken()
		tokens = append(tokens, tok)
		if tok.typ == tokEOF {
			break
		}
	}

	p := &filterParser{tokens: tokens, pos: 0}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	return expr, nil
}

func (p *filterParser) peek() filterToken {
	if p.pos >= len(p.tokens) {
		return filterToken{typ: tokEOF}
	}
	return p.tokens[p.pos]
}

func (p *filterParser) advance() filterToken {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *filterParser) parseOr() (FilterExpr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	var exprs []FilterExpr
	exprs = append(exprs, left)

	for p.peek().typ == tokOr {
		p.advance() // consume OR / ||
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, right)
	}

	if len(exprs) == 1 {
		return exprs[0], nil
	}
	return &OrFilter{Exprs: exprs}, nil
}

func (p *filterParser) parseAnd() (FilterExpr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	var exprs []FilterExpr
	exprs = append(exprs, left)

	for p.peek().typ == tokAnd {
		p.advance() // consume AND / && / ,
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, right)
	}

	if len(exprs) == 1 {
		return exprs[0], nil
	}
	return &AndFilter{Exprs: exprs}, nil
}

func (p *filterParser) parsePrimary() (FilterExpr, error) {
	tok := p.peek()
	if tok.typ == tokLParen {
		p.advance()
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().typ != tokRParen {
			return nil, fmt.Errorf("expected closing parenthesis, got %q", p.peek().val)
		}
		p.advance() // consume )
		return expr, nil
	}

	if tok.typ != tokIdent {
		return nil, fmt.Errorf("expected field name, got %q", tok.val)
	}
	fieldName := p.advance().val

	opTok := p.peek()
	if opTok.typ != tokOp {
		return nil, fmt.Errorf("expected operator after %q, got %q", fieldName, opTok.val)
	}
	p.advance()

	if opTok.val == "IS NULL" || opTok.val == "IS NOT NULL" {
		return &ComparisonFilter{Field: fieldName, Op: opTok.val}, nil
	}

	valTok := p.peek()
	if valTok.typ != tokIdent {
		return nil, fmt.Errorf("expected value after operator %q, got %q", opTok.val, valTok.val)
	}
	p.advance()

	return &ComparisonFilter{
		Field:  fieldName,
		Op:     opTok.val,
		RawVal: valTok.val,
	}, nil
}

// ParseQueryParamsFilter extracts both explicit ?filter= expressions and individual column query params.
func ParseQueryParamsFilter(q url.Values, schema *storage.TableSchema) (FilterExpr, error) {
	var andExprs []FilterExpr

	// 1. Explicit filter parameter(s)
	filterParams := q["filter"]
	for _, fp := range filterParams {
		fp = strings.TrimSpace(fp)
		if fp == "" {
			continue
		}
		parsed, err := ParseFilter(fp)
		if err != nil {
			return nil, fmt.Errorf("invalid filter expression %q: %w", fp, err)
		}
		if parsed != nil {
			andExprs = append(andExprs, parsed)
		}
	}

	// 2. Direct column query parameters (e.g. ?balance>500 or ?status=active)
	reserved := map[string]bool{
		"page":     true,
		"perpage":  true,
		"per_page": true,
		"limit":    true,
		"sort":     true,
		"search":   true,
		"filter":   true,
		"format":   true,
		"token":    true,
	}

	for key, vals := range q {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		if reserved[lowerKey] || len(vals) == 0 {
			continue
		}
		val := vals[0]

		// Check if key ends with an operator (e.g. "balance>", "balance>=", "status!=")
		op := "="
		field := key
		for _, opSuffix := range []string{">=", "<=", "!=", "<>", "!~", "==", ">", "<", "~"} {
			if strings.HasSuffix(key, opSuffix) {
				field = strings.TrimSuffix(key, opSuffix)
				op = opSuffix
				break
			}
		}

		field = strings.TrimSpace(field)
		if schema != nil {
			if col, _, found := schema.FindColumn(field); found {
				field = col.Name
			}
		}

		andExprs = append(andExprs, &ComparisonFilter{
			Field:  field,
			Op:     op,
			RawVal: val,
		})
	}

	if len(andExprs) == 0 {
		return nil, nil
	}
	if len(andExprs) == 1 {
		return andExprs[0], nil
	}
	return &AndFilter{Exprs: andExprs}, nil
}
