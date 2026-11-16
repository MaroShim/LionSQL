package planner

import (
	"strings"

	"lionsql/pkg/sqlutil"
)

// DecorrelateSubqueries inspects the query's WHERE clause and transforms correlated or equijoin
// subqueries (such as EXISTS and IN) into SEMI or ANTI joins.
func (p *Planner) DecorrelateSubqueries(q *QueryInfo, tableExists func(string) bool) error {
	if q == nil || q.WhereClause == "" {
		return nil
	}

	// 1. Decorrelate [NOT] EXISTS (SELECT ... FROM ... WHERE ...)
	p.decorrelateExists(q, tableExists)

	// 2. Decorrelate [col] [NOT] IN (SELECT ... FROM ... [WHERE ...])
	p.decorrelateIn(q, tableExists)

	if len(q.Joins) > 0 && q.Join == nil {
		q.Join = q.Joins[0]
	}
	return nil
}

func (p *Planner) decorrelateExists(q *QueryInfo, tableExists func(string) bool) {
	for {
		mStart, mEnd, prefix, innerSQL, found := findExistsSubqueryParen(q.WhereClause)
		if !found {
			break
		}
		if isInsideOrBranch(q.WhereClause, mStart, mEnd) {
			break
		}

		isNot := sqlutil.ContainsKeyword(prefix, "NOT", false)
		_, subTbl, subAlias, subWhere, ok := parseInnerSelectWithProj(innerSQL)
		if !ok || subTbl == "" {
			break
		}
		if subAlias == "" {
			subAlias = subTbl
		}
		if tableExists != nil && !tableExists(subTbl) {
			break
		}

		// Look for correlation conditions in subWhere (e.g. o.user_id = u.id)
		conds := splitAndConditions(subWhere)
		var leftKey, rightKey string
		var remainingConds []string

		outerNames := []string{strings.ToLower(cleanIdent(q.TableName)), strings.ToLower(cleanIdent(q.TableAlias))}
		innerNames := []string{strings.ToLower(cleanIdent(subTbl)), strings.ToLower(cleanIdent(subAlias))}

		for _, c := range conds {
			cTrim := strings.TrimSpace(c)
			if cTrim == "" {
				continue
			}
			eqParts := strings.Split(cTrim, "=")
			if len(eqParts) == 2 && leftKey == "" {
				p1 := strings.TrimSpace(eqParts[0])
				p2 := strings.TrimSpace(eqParts[1])

				if isIdent(p1) && isIdent(p2) {
					p1Outer := hasPrefixTable(p1, outerNames)
					p2Outer := hasPrefixTable(p2, outerNames)
					p1Inner := hasPrefixTable(p1, innerNames)
					p2Inner := hasPrefixTable(p2, innerNames)

					if p1Outer && (p2Inner || !strings.Contains(p2, ".")) {
						leftKey = p1
						rightKey = p2
						continue
					} else if p2Outer && (p1Inner || !strings.Contains(p1, ".")) {
						leftKey = p2
						rightKey = p1
						continue
					} else if p1Inner && !strings.Contains(p2, ".") && !p2Inner {
						leftKey = p2
						rightKey = p1
						continue
					} else if p2Inner && !strings.Contains(p1, ".") && !p1Inner {
						leftKey = p1
						rightKey = p2
						continue
					}
				}
			}
			remainingConds = append(remainingConds, cTrim)
		}

		if leftKey == "" {
			// Uncorrelated EXISTS, leave for existing boolean resolution
			break
		}

		jType := "SEMI"
		if isNot {
			jType = "ANTI"
		}

		join := &JoinInfo{
			Type:     jType,
			Table:    subTbl,
			Alias:    subAlias,
			LeftKey:  leftKey,
			RightKey: rightKey,
			Filter:   strings.Join(remainingConds, " AND "),
		}
		q.Joins = append(q.Joins, join)
		q.WhereClause = removeClause(q.WhereClause, mStart, mEnd)
	}
}

func (p *Planner) decorrelateIn(q *QueryInfo, tableExists func(string) bool) {
	for {
		mStart, mEnd, prefix, innerSQL, found := findInSubqueryParen(q.WhereClause)
		if !found {
			break
		}
		if isInsideOrBranch(q.WhereClause, mStart, mEnd) {
			break
		}

		isNot := sqlutil.ContainsKeyword(prefix, "NOT", false)
		fields := strings.Fields(prefix)
		if len(fields) == 0 {
			break
		}
		outerCol := fields[0]

		subProj, subTbl, subAlias, subWhere, ok := parseInnerSelectWithProj(innerSQL)
		if !ok || subTbl == "" || subProj == "" {
			break
		}
		if subAlias == "" {
			subAlias = subTbl
		}
		if tableExists != nil && !tableExists(subTbl) {
			break
		}

		jType := "SEMI"
		if isNot {
			jType = "ANTI"
		}

		rightKey := subProj
		if !strings.Contains(rightKey, ".") && subAlias != "" {
			rightKey = subAlias + "." + rightKey
		}

		join := &JoinInfo{
			Type:     jType,
			Table:    subTbl,
			Alias:    subAlias,
			LeftKey:  outerCol,
			RightKey: rightKey,
			Filter:   subWhere,
		}
		q.Joins = append(q.Joins, join)
		q.WhereClause = removeClause(q.WhereClause, mStart, mEnd)
	}
}

type subqueryScannerToken struct {
	text  string
	start int
	end   int
}

func tokenizeForSubqueryScan(s string) []subqueryScannerToken {
	var tokens []subqueryScannerToken
	i := 0
	n := len(s)
	for i < n {
		for i < n && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
			i++
		}
		if i >= n {
			break
		}
		if s[i] == '\'' {
			start := i
			i++
			for i < n {
				if s[i] == '\'' {
					if i+1 < n && s[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			tokens = append(tokens, subqueryScannerToken{text: s[start:i], start: start, end: i})
			continue
		}
		if s[i] == '(' || s[i] == ')' || s[i] == ',' || s[i] == '=' || s[i] == '<' || s[i] == '>' || s[i] == '!' {
			tokens = append(tokens, subqueryScannerToken{text: s[i : i+1], start: i, end: i + 1})
			i++
			continue
		}
		start := i
		for i < n && s[i] != ' ' && s[i] != '\t' && s[i] != '\n' && s[i] != '\r' &&
			s[i] != '(' && s[i] != ')' && s[i] != ',' && s[i] != '=' && s[i] != '<' && s[i] != '>' && s[i] != '!' && s[i] != '\'' {
			i++
		}
		if i > start {
			tokens = append(tokens, subqueryScannerToken{text: s[start:i], start: start, end: i})
		}
	}
	return tokens
}

func extractBalancedParen(s string, matchStart int, openParenIdx int) (int, int, string, string, bool) {
	depth := 0
	closeParenIdx := -1
	inQuote := false
	for i := openParenIdx; i < len(s); i++ {
		if s[i] == '\'' {
			if inQuote && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		if s[i] == '(' {
			depth++
		} else if s[i] == ')' {
			depth--
			if depth == 0 {
				closeParenIdx = i
				break
			}
		}
	}
	if closeParenIdx == -1 {
		return 0, 0, "", "", false
	}
	inner := strings.TrimSpace(s[openParenIdx+1 : closeParenIdx])
	return matchStart, closeParenIdx + 1, s[matchStart:openParenIdx], inner, true
}

func findExistsSubqueryParen(s string) (matchStart int, matchEnd int, prefixStr string, innerSQL string, found bool) {
	tokens := tokenizeForSubqueryScan(s)
	for i := 0; i < len(tokens); i++ {
		if strings.EqualFold(tokens[i].text, "EXISTS") && i+1 < len(tokens) && tokens[i+1].text == "(" {
			startIdx := tokens[i].start
			if i > 0 && strings.EqualFold(tokens[i-1].text, "NOT") {
				startIdx = tokens[i-1].start
			}
			return extractBalancedParen(s, startIdx, tokens[i+1].start)
		}
	}
	return 0, 0, "", "", false
}

func findInSubqueryParen(s string) (matchStart int, matchEnd int, prefixStr string, innerSQL string, found bool) {
	tokens := tokenizeForSubqueryScan(s)
	for i := 0; i < len(tokens); i++ {
		if strings.EqualFold(tokens[i].text, "IN") && i+1 < len(tokens) && tokens[i+1].text == "(" {
			colTokIdx := i - 1
			if colTokIdx >= 0 && strings.EqualFold(tokens[colTokIdx].text, "NOT") {
				colTokIdx--
			}
			if colTokIdx >= 0 && isIdent(tokens[colTokIdx].text) {
				return extractBalancedParen(s, tokens[colTokIdx].start, tokens[i+1].start)
			}
		}
	}
	return 0, 0, "", "", false
}

func parseInnerSelectWithProj(sql string) (projections, table, alias, whereClause string, ok bool) {
	sql = strings.TrimSpace(sql)
	upper := strings.ToUpper(sql)
	if !strings.HasPrefix(upper, "SELECT") {
		return "", "", "", "", false
	}
	for _, kw := range []string{"UNION", "INTERSECT", "EXCEPT", "MINUS", "GROUP BY", "HAVING"} {
		if findKeywordIndex(sql, kw) != -1 {
			return "", "", "", "", false
		}
	}
	fromIdx := findKeywordIndex(sql, "FROM")
	if fromIdx == -1 {
		return "", "", "", "", false
	}
	projections = strings.TrimSpace(sql[6:fromIdx])

	rest := strings.TrimSpace(sql[fromIdx+4:])
	whereIdx := findKeywordIndex(rest, "WHERE")
	var fromClause string
	if whereIdx != -1 {
		fromClause = strings.TrimSpace(rest[:whereIdx])
		whereClause = strings.TrimSpace(rest[whereIdx+5:])
	} else {
		fromClause = rest
		whereClause = ""
	}
	if strings.Contains(fromClause, ",") || findKeywordIndex(fromClause, "JOIN") != -1 {
		return "", "", "", "", false
	}

	table, alias = parseTableAndAlias(fromClause)
	return projections, table, alias, whereClause, true
}

func splitAndConditions(where string) []string {
	return sqlutil.SplitAndConditions(where)
}

func findKeywordIndex(s string, kw string) int {
	depth := 0
	inQuote := false
	kwUpper := strings.ToUpper(kw)
	n := len(kw)
	for i := 0; i <= len(s)-n; i++ {
		c := s[i]
		if c == '\'' {
			if inQuote && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		if c == '(' {
			depth++
			continue
		} else if c == ')' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 && (i == 0 || isSpaceOrParen(s[i-1])) {
			if strings.EqualFold(s[i:i+n], kwUpper) {
				if i+n == len(s) || isSpaceOrParen(s[i+n]) {
					return i
				}
			}
		}
	}
	return -1
}

func trimKeywordSuffix(s, kw string) string {
	s = strings.TrimSpace(s)
	n := len(kw)
	if len(s) < n {
		return s
	}
	if strings.EqualFold(s[len(s)-n:], kw) {
		if len(s) == n {
			return ""
		}
		prev := s[len(s)-n-1]
		if isSpaceOrParen(prev) {
			return strings.TrimSpace(s[:len(s)-n])
		}
	}
	return s
}

func trimKeywordPrefix(s, kw string) string {
	s = strings.TrimSpace(s)
	n := len(kw)
	if len(s) < n {
		return s
	}
	if strings.EqualFold(s[:n], kw) {
		if len(s) == n {
			return ""
		}
		next := s[n]
		if isSpaceOrParen(next) {
			return strings.TrimSpace(s[n:])
		}
	}
	return s
}

func removeClause(where string, start, end int) string {
	before := strings.TrimSpace(where[:start])
	after := strings.TrimSpace(where[end:])

	before = trimKeywordSuffix(before, "AND")
	before = trimKeywordSuffix(before, "OR")
	before = strings.TrimSpace(before)

	after = trimKeywordPrefix(after, "AND")
	after = trimKeywordPrefix(after, "OR")
	after = strings.TrimSpace(after)

	if before != "" && after != "" {
		return before + " AND " + after
	}
	if before != "" {
		return before
	}
	return after
}

func isInsideOrBranch(where string, start, end int) bool {
	if start < 0 || end > len(where) || start > end {
		return true
	}

	// 1. Direct adjacent connectors
	before := strings.TrimSpace(where[:start])
	if trimKeywordSuffix(before, "OR") != before {
		return true
	}
	after := strings.TrimSpace(where[end:])
	if trimKeywordPrefix(after, "OR") != after {
		return true
	}

	// 2. Find enclosing parenthesis boundaries and nesting depth of [start, end]
	depthAtStart := 0
	scopeStart := 0
	inQuote := false

	for i := 0; i < start; i++ {
		c := where[i]
		if c == '\'' {
			if inQuote && i+1 < len(where) && where[i+1] == '\'' {
				i++
				continue
			}
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		if c == '(' {
			depthAtStart++
			scopeStart = i + 1
		} else if c == ')' {
			if depthAtStart > 0 {
				depthAtStart--
				if depthAtStart == 0 {
					scopeStart = 0
				}
			}
		}
	}

	scopeEnd := len(where)
	d := depthAtStart
	inQuote = false
	for i := end; i < len(where); i++ {
		c := where[i]
		if c == '\'' {
			if inQuote && i+1 < len(where) && where[i+1] == '\'' {
				i++
				continue
			}
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		if c == '(' {
			d++
		} else if c == ')' {
			if d == depthAtStart {
				scopeEnd = i
				break
			}
			if d > 0 {
				d--
			}
		}
	}

	// 3. In where[scopeStart:scopeEnd], check if 'OR' exists at the same scope depth
	scopeStr := where[scopeStart:scopeEnd]
	return findKeywordIndex(scopeStr, "OR") != -1
}

func isIdent(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, " \t\r\n><!=+-*/%^&|~(),;") {
		return false
	}
	parts := strings.Split(s, ".")
	for _, p := range parts {
		p = strings.Trim(p, "\"`'[]")
		if p == "" {
			return false
		}
		r := p[0]
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == '@' || r == '#') {
			return false
		}
		for i := 1; i < len(p); i++ {
			c := p[i]
			if !sqlutil.IsIdentChar(c) && c != '@' && c != '#' {
				return false
			}
		}
	}
	return true
}

func hasPrefixTable(col string, names []string) bool {
	colLower := strings.ToLower(cleanIdent(col))
	for _, n := range names {
		if n != "" && strings.HasPrefix(colLower, n+".") {
			return true
		}
	}
	return false
}

func isSpaceOrParen(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '(' || b == ')'
}

func isSpaceOrParenRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '(' || r == ')'
}
