package planner

import (
	"regexp"
	"strconv"
	"strings"
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

func splitSelectClauses(sql string) (projStr, fromClause, whereClause, groupByStr, havingClause, orderByClause string, limitCount int64, ok bool) {
	limitCount = -1
	sql = strings.TrimSpace(strings.TrimRight(sql, ";"))
	if !strings.HasPrefix(strings.ToUpper(sql), "SELECT") {
		return "", "", "", "", "", "", -1, false
	}

	fromIdx := findKeywordIndex(sql, "FROM")
	if fromIdx == -1 {
		return "", "", "", "", "", "", -1, false
	}
	projStr = strings.TrimSpace(sql[6:fromIdx])

	rest := strings.TrimSpace(sql[fromIdx+4:])
	whereIdx := findKeywordIndex(rest, "WHERE")
	groupByIdx := findKeywordIndex(rest, "GROUP BY")
	havingIdx := findKeywordIndex(rest, "HAVING")
	orderByIdx := findKeywordIndex(rest, "ORDER BY")
	limitIdx := findKeywordIndex(rest, "LIMIT")

	firstAfterFrom := len(rest)
	for _, idx := range []int{whereIdx, groupByIdx, havingIdx, orderByIdx, limitIdx} {
		if idx != -1 && idx < firstAfterFrom {
			firstAfterFrom = idx
		}
	}
	fromClause = strings.TrimSpace(rest[:firstAfterFrom])

	if whereIdx != -1 {
		end := len(rest)
		for _, idx := range []int{groupByIdx, havingIdx, orderByIdx, limitIdx} {
			if idx != -1 && idx > whereIdx && idx < end {
				end = idx
			}
		}
		whereClause = strings.TrimSpace(rest[whereIdx+5 : end])
	}

	if groupByIdx != -1 {
		end := len(rest)
		for _, idx := range []int{havingIdx, orderByIdx, limitIdx} {
			if idx != -1 && idx > groupByIdx && idx < end {
				end = idx
			}
		}
		groupByStr = strings.TrimSpace(rest[groupByIdx+8 : end])
	}

	if havingIdx != -1 {
		end := len(rest)
		for _, idx := range []int{orderByIdx, limitIdx} {
			if idx != -1 && idx > havingIdx && idx < end {
				end = idx
			}
		}
		havingClause = strings.TrimSpace(rest[havingIdx+6 : end])
	}

	if orderByIdx != -1 {
		end := len(rest)
		if limitIdx != -1 && limitIdx > orderByIdx {
			end = limitIdx
		}
		orderByClause = strings.TrimSpace(rest[orderByIdx+8 : end])
	}

	if limitIdx != -1 {
		limitStr := strings.TrimSpace(rest[limitIdx+5:])
		if n, err := strconv.ParseInt(limitStr, 10, 64); err == nil {
			limitCount = n
		}
	}

	return projStr, fromClause, whereClause, groupByStr, havingClause, orderByClause, limitCount, true
}

func splitAndConditions(where string) []string {
	where = strings.TrimSpace(where)
	if where == "" {
		return nil
	}
	var conds []string
	depth := 0
	inQuote := false
	var quoteChar rune
	start := 0

	runes := []rune(where)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if inQuote {
			if r == quoteChar {
				inQuote = false
			}
			continue
		}
		if r == '\'' || r == '"' {
			inQuote = true
			quoteChar = r
			continue
		}
		if r == '(' {
			depth++
			continue
		}
		if r == ')' {
			depth--
			continue
		}
		if depth == 0 && i+3 <= len(runes) {
			word := string(runes[i : i+3])
			if strings.EqualFold(word, "AND") {
				isLeftBoundary := (i == 0 || isSpaceOrParenRune(runes[i-1]))
				isRightBoundary := (i+3 == len(runes) || isSpaceOrParenRune(runes[i+3]))
				if isLeftBoundary && isRightBoundary {
					cond := strings.TrimSpace(string(runes[start:i]))
					if cond != "" {
						conds = append(conds, cond)
					}
					i += 2
					start = i + 1
				}
			}
		}
	}
	if start < len(runes) {
		cond := strings.TrimSpace(string(runes[start:]))
		if cond != "" {
			conds = append(conds, cond)
		}
	}
	return conds
}

func findKeywordIndex(s string, kw string) int {
	depth := 0
	kwUpper := strings.ToUpper(kw)
	n := len(kw)
	for i := 0; i <= len(s)-n; i++ {
		c := s[i]
		if c == '(' {
			depth++
			continue
		} else if c == ')' {
			depth--
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

func removeClause(where string, start, end int) string {
	before := strings.TrimSpace(where[:start])
	after := strings.TrimSpace(where[end:])

	before = strings.TrimSuffix(before, "AND")
	before = strings.TrimSuffix(before, "and")
	before = strings.TrimSuffix(before, "And")
	before = strings.TrimSpace(before)

	after = strings.TrimPrefix(after, "AND")
	after = strings.TrimPrefix(after, "and")
	after = strings.TrimPrefix(after, "And")
	after = strings.TrimSpace(after)

	if before != "" && after != "" {
		return before + " AND " + after
	}
	if before != "" {
		return before
	}
	return after
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
