package planner

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

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
	existsRe := regexp.MustCompile("(?is)(?:NOT\\s+)?EXISTS\\s*\\(")
	for {
		mStart, mEnd, prefix, innerSQL, found := findSubqueryParen(q.WhereClause, existsRe)
		if !found {
			break
		}
		if isInsideOrBranch(q.WhereClause, mStart, mEnd) {
			break
		}

		isNot := strings.Contains(strings.ToUpper(prefix), "NOT")
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
	inRe := regexp.MustCompile("(?is)\\b([a-zA-Z0-9_\\.]+)\\s+(?:NOT\\s+)?IN\\s*\\(")
	for {
		mStart, mEnd, prefix, innerSQL, found := findSubqueryParen(q.WhereClause, inRe)
		if !found {
			break
		}
		if isInsideOrBranch(q.WhereClause, mStart, mEnd) {
			break
		}

		isNot := strings.Contains(strings.ToUpper(prefix), "NOT")
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

func findSubqueryParen(s string, prefixPattern *regexp.Regexp) (matchStart int, matchEnd int, prefixStr string, innerSQL string, found bool) {
	loc := prefixPattern.FindStringSubmatchIndex(s)
	if loc == nil {
		return 0, 0, "", "", false
	}
	openParenIdx := -1
	for i := loc[0]; i < len(s); i++ {
		if s[i] == '(' {
			openParenIdx = i
			break
		}
	}
	if openParenIdx == -1 {
		return 0, 0, "", "", false
	}

	depth := 0
	closeParenIdx := -1
	for i := openParenIdx; i < len(s); i++ {
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
	return loc[0], closeParenIdx + 1, s[loc[0]:openParenIdx], inner, true
}

func parseInnerSelectWithProj(sql string) (projections, table, alias, whereClause string, ok bool) {
	sql = strings.TrimSpace(sql)
	upper := strings.ToUpper(sql)
	if !strings.HasPrefix(upper, "SELECT") {
		return "", "", "", "", false
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

	table, alias = parseTableAndAlias(fromClause)
	return projections, table, alias, whereClause, true
}

func splitSelectClauses(sql string) (projStr, fromClause, whereClause, groupByStr, havingClause, orderByClause string, limitCount int64, offsetCount int64, ok bool) {
	limitCount = -1
	offsetCount = 0
	sql = strings.TrimSpace(strings.TrimRight(sql, ";"))
	if !strings.HasPrefix(strings.ToUpper(sql), "SELECT") {
		return "", "", "", "", "", "", -1, 0, false
	}

	fromIdx := findKeywordIndex(sql, "FROM")
	if fromIdx == -1 {
		return "", "", "", "", "", "", -1, 0, false
	}
	projStr = strings.TrimSpace(sql[6:fromIdx])

	// Check for TOP (n) / TOP n in projections
	upperProj := strings.ToUpper(projStr)
	if strings.HasPrefix(upperProj, "DISTINCT") && (len(upperProj) == 8 || unicode.IsSpace(rune(upperProj[8]))) {
		afterDistinct := strings.TrimSpace(projStr[8:])
		if lim, rem, ok := extractTop(afterDistinct); ok {
			projStr = "DISTINCT " + rem
			if limitCount == -1 {
				limitCount = lim
			}
		}
	} else if lim, rem, ok := extractTop(projStr); ok {
		projStr = rem
		if limitCount == -1 {
			limitCount = lim
		}
	}

	rest := strings.TrimSpace(sql[fromIdx+4:])
	whereIdx := findKeywordIndex(rest, "WHERE")
	groupByIdx := findKeywordIndex(rest, "GROUP BY")
	havingIdx := findKeywordIndex(rest, "HAVING")
	orderByIdx := findKeywordIndex(rest, "ORDER BY")
	limitIdx := findKeywordIndex(rest, "LIMIT")
	offsetIdx := findKeywordIndex(rest, "OFFSET")
	fetchIdx := findKeywordIndex(rest, "FETCH")

	firstAfterFrom := len(rest)
	for _, idx := range []int{whereIdx, groupByIdx, havingIdx, orderByIdx, limitIdx, offsetIdx, fetchIdx} {
		if idx != -1 && idx < firstAfterFrom {
			firstAfterFrom = idx
		}
	}
	fromClause = strings.TrimSpace(rest[:firstAfterFrom])

	if whereIdx != -1 {
		end := len(rest)
		for _, idx := range []int{groupByIdx, havingIdx, orderByIdx, limitIdx, offsetIdx, fetchIdx} {
			if idx != -1 && idx > whereIdx && idx < end {
				end = idx
			}
		}
		whereClause = strings.TrimSpace(rest[whereIdx+5 : end])
	}

	if groupByIdx != -1 {
		end := len(rest)
		for _, idx := range []int{havingIdx, orderByIdx, limitIdx, offsetIdx, fetchIdx} {
			if idx != -1 && idx > groupByIdx && idx < end {
				end = idx
			}
		}
		groupByStr = strings.TrimSpace(rest[groupByIdx+8 : end])
	}

	if havingIdx != -1 {
		end := len(rest)
		for _, idx := range []int{orderByIdx, limitIdx, offsetIdx, fetchIdx} {
			if idx != -1 && idx > havingIdx && idx < end {
				end = idx
			}
		}
		havingClause = strings.TrimSpace(rest[havingIdx+6 : end])
	}

	if orderByIdx != -1 {
		end := len(rest)
		for _, idx := range []int{limitIdx, offsetIdx, fetchIdx} {
			if idx != -1 && idx > orderByIdx && idx < end {
				end = idx
			}
		}
		orderByClause = strings.TrimSpace(rest[orderByIdx+8 : end])
	}

	if limitIdx != -1 {
		end := len(rest)
		for _, idx := range []int{offsetIdx, fetchIdx} {
			if idx != -1 && idx > limitIdx && idx < end {
				end = idx
			}
		}
		limitStr := strings.TrimSpace(rest[limitIdx+5 : end])
		fields := strings.Fields(limitStr)
		if len(fields) > 0 {
			if n, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
				limitCount = n
			}
		}
	}

	if offsetIdx != -1 {
		end := len(rest)
		for _, idx := range []int{limitIdx, fetchIdx} {
			if idx != -1 && idx > offsetIdx && idx < end {
				end = idx
			}
		}
		offsetStr := strings.TrimSpace(rest[offsetIdx+6 : end])
		fields := strings.Fields(offsetStr)
		if len(fields) > 0 {
			if n, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
				offsetCount = n
			}
		}
	}

	if fetchIdx != -1 {
		end := len(rest)
		for _, idx := range []int{limitIdx, offsetIdx} {
			if idx != -1 && idx > fetchIdx && idx < end {
				end = idx
			}
		}
		fetchStr := strings.TrimSpace(rest[fetchIdx+5 : end])
		fields := strings.Fields(fetchStr)
		for _, tok := range fields {
			if n, err := strconv.ParseInt(tok, 10, 64); err == nil {
				limitCount = n
				break
			}
		}
	}

	return projStr, fromClause, whereClause, groupByStr, havingClause, orderByClause, limitCount, offsetCount, true
}

func extractTop(proj string) (limit int64, remaining string, ok bool) {
	trimmed := strings.TrimSpace(proj)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "TOP") {
		return -1, proj, false
	}
	rem := strings.TrimSpace(trimmed[3:])
	hasParen := false
	if strings.HasPrefix(rem, "(") {
		hasParen = true
		rem = strings.TrimSpace(rem[1:])
	}
	idx := 0
	for idx < len(rem) && rem[idx] >= '0' && rem[idx] <= '9' {
		idx++
	}
	if idx == 0 {
		return -1, proj, false
	}
	n, err := strconv.ParseInt(rem[:idx], 10, 64)
	if err != nil {
		return -1, proj, false
	}
	rem = strings.TrimSpace(rem[idx:])
	if hasParen {
		if strings.HasPrefix(rem, ")") {
			rem = strings.TrimSpace(rem[1:])
		}
	}
	upperRem := strings.ToUpper(rem)
	if strings.HasPrefix(upperRem, "PERCENT") && (len(upperRem) == 7 || unicode.IsSpace(rune(upperRem[7]))) {
		rem = strings.TrimSpace(rem[7:])
		upperRem = strings.ToUpper(rem)
	}
	if strings.HasPrefix(upperRem, "WITH") && (len(upperRem) == 4 || unicode.IsSpace(rune(upperRem[4]))) {
		afterWith := strings.TrimSpace(rem[4:])
		upperWith := strings.ToUpper(afterWith)
		if strings.HasPrefix(upperWith, "TIES") && (len(upperWith) == 4 || unicode.IsSpace(rune(upperWith[4]))) {
			rem = strings.TrimSpace(afterWith[4:])
		}
	}
	return n, rem, true
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
	s = cleanIdent(s)
	if s == "" {
		return false
	}
	r := rune(s[0])
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == '@'
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
