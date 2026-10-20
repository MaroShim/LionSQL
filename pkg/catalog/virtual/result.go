package virtual

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
)

var (
	reProjAs           = regexp.MustCompile(`(?is)^(.*?)\s+AS\s+([a-zA-Z0-9_#$]+|"[^"]+")$`)
	reEvalInClause     = regexp.MustCompile(`(?is)^\s*([a-zA-Z0-9_\.]+(?:->>'[^']+')?)\s+(NOT\s+IN|IN)\s*\((.*?)\)\s*$`)
	reEvalBinaryOp     = regexp.MustCompile(`(?is)^\s*([a-zA-Z0-9_\.]+(?:->>'[^']+')?)\s*(=|!=|<>|>=|<=|>|<|LIKE|NOT\s+LIKE|ILIKE|NOT\s+ILIKE)\s*(.*?)\s*$`)
	reEvalIsNull       = regexp.MustCompile(`(?is)^\s*([a-zA-Z0-9_\.]+(?:->>'[^']+')?)\s+IS\s+NULL\s*$`)
	reEvalIsNotNull    = regexp.MustCompile(`(?is)^\s*([a-zA-Z0-9_\.]+(?:->>'[^']+')?)\s+IS\s+NOT\s+NULL\s*$`)
	reEvalBetween      = regexp.MustCompile(`(?is)^\s*([a-zA-Z0-9_\.]+(?:->>'[^']+')?)\s+(NOT\s+BETWEEN|BETWEEN)\s+(.*?)\s+AND\s+(.*?)\s*$`)
)

// FormatResult filters, sorts, limits, and projects virtual catalog rows according to the SQL query parts.
func FormatResult(columns []string, allRows []map[string]any, projStr, whereClause, orderByClause string, limitCount int) (*Result, error) {
	// 1. Filter rows by whereClause
	var filteredRows []map[string]any
	for _, r := range allRows {
		if whereClause == "" || evaluateWhereConditionMap(r, whereClause) {
			filteredRows = append(filteredRows, r)
		}
	}

	// 2. Order by
	if orderByClause != "" && len(filteredRows) > 0 {
		orderItems := sqlutil.ParseOrderBy(orderByClause)
		if len(orderItems) > 0 {
			sort.Slice(filteredRows, func(i, j int) bool {
				for _, item := range orderItems {
					orderCol := cleanIdentifier(item.Column)
					v1Val := getMapValueCaseInsensitive(filteredRows[i], orderCol)
					v2Val := getMapValueCaseInsensitive(filteredRows[j], orderCol)
					v1 := fmt.Sprintf("%v", v1Val)
					v2 := fmt.Sprintf("%v", v2Val)
					if v1 == v2 {
						continue
					}
					if n1, err1 := strconv.ParseFloat(v1, 64); err1 == nil {
						if n2, err2 := strconv.ParseFloat(v2, 64); err2 == nil {
							if n1 == n2 {
								continue
							}
							if item.Desc {
								return n1 > n2
							}
							return n1 < n2
						}
					}
					if item.Desc {
						return v1 > v2
					}
					return v1 < v2
				}
				return false
			})
		}
	}

	// 3. Limit
	if limitCount >= 0 && len(filteredRows) > limitCount {
		filteredRows = filteredRows[:limitCount]
	}

	// 4. Projection
	var selectedCols []string
	colSourceMap := make(map[string]string)
	trimmedProj := strings.TrimSpace(projStr)
	if trimmedProj == "*" || trimmedProj == "" || strings.HasSuffix(trimmedProj, ".*") {
		selectedCols = columns
		for _, col := range columns {
			colSourceMap[col] = col
		}
	} else {
		for _, c := range splitScalarProjections(projStr) {
			cTrimmed := strings.TrimSpace(c)
			if cTrimmed == "" {
				continue
			}
			if cTrimmed == "*" || strings.HasSuffix(cTrimmed, ".*") {
				for _, col := range columns {
					selectedCols = append(selectedCols, col)
					colSourceMap[col] = col
				}
				continue
			}
			alias := ""
			colExpr := cTrimmed
			if m := reProjAs.FindStringSubmatch(cTrimmed); len(m) == 3 {
				colExpr = strings.TrimSpace(m[1])
				alias = cleanIdentifier(m[2])
			} else {
				fields := strings.Fields(cTrimmed)
				if len(fields) == 2 && !strings.Contains(fields[1], "(") {
					colExpr = fields[0]
					alias = cleanIdentifier(fields[1])
				}
			}
			rawCol := strings.ToLower(cleanIdentifier(colExpr))
			if strings.Contains(rawCol, ".") {
				rawCol = rawCol[strings.LastIndex(rawCol, ".")+1:]
			}
			outName := alias
			if outName == "" {
				outName = rawCol
			}
			selectedCols = append(selectedCols, outName)
			colSourceMap[outName] = rawCol
		}
	}

	finalRows := make([]map[string]any, 0, len(filteredRows))
	for _, fr := range filteredRows {
		row := make(map[string]any, len(selectedCols))
		for _, col := range selectedCols {
			src := colSourceMap[col]
			if src == "" {
				src = col
			}
			if v, ok := fr[src]; ok {
				row[col] = v
			} else if v, ok := fr[strings.ToLower(col)]; ok {
				row[col] = v
			} else {
				row[col] = nil
			}
		}
		finalRows = append(finalRows, row)
	}

	return &Result{
		Columns: selectedCols,
		Rows:    finalRows,
	}, nil
}

func evaluateWhereConditionMap(rec map[string]any, whereStr string) bool {
	whereStr = strings.TrimSpace(whereStr)
	if whereStr == "" || whereStr == "1=1" || whereStr == "1 = 1" || strings.EqualFold(whereStr, "NOT 1=0") || strings.EqualFold(whereStr, "NOT 1 = 0") {
		return true
	}
	if whereStr == "1=0" || whereStr == "1 = 0" || strings.EqualFold(whereStr, "NOT 1=1") || strings.EqualFold(whereStr, "NOT 1 = 1") {
		return false
	}

	p := proc.NewParser(proc.NewLexer(whereStr))
	expr := p.ParseExpr()
	if expr != nil && len(p.Errors()) == 0 {
		return proc.EvalBoolExpr(expr, rec, nil)
	}

	orParts := splitLogicalConditions(whereStr, " OR ")
	if len(orParts) > 1 {
		for _, part := range orParts {
			if evaluateWhereConditionMap(rec, part) {
				return true
			}
		}
		return false
	}

	andParts := splitLogicalConditions(whereStr, " AND ")
	if len(andParts) > 1 {
		for _, part := range andParts {
			if !evaluateWhereConditionMap(rec, part) {
				return false
			}
		}
		return true
	}

	return evaluateSingleConditionMap(rec, whereStr)
}

func evaluateSingleConditionMap(rec map[string]any, whereStr string) bool {
	whereStr = strings.TrimSpace(whereStr)
	if strings.HasPrefix(whereStr, "(") && strings.HasSuffix(whereStr, ")") {
		inner := strings.TrimSpace(whereStr[1 : len(whereStr)-1])
		if !strings.Contains(inner, "(") && !strings.Contains(inner, ")") {
			whereStr = inner
		}
	}

	if m := reEvalInClause.FindStringSubmatch(whereStr); len(m) == 4 {
		colName := cleanIdentifier(m[1])
		isNot := strings.TrimSpace(m[2]) != "" && strings.HasPrefix(strings.ToUpper(m[2]), "NOT")
		valList := m[3]
		val, ok := rec[colName]
		if !ok {
			if idx := strings.Index(colName, "."); idx != -1 {
				val, ok = rec[colName[idx+1:]]
			}
		}
		if !ok || val == nil {
			return isNot
		}
		strVal := fmt.Sprintf("%v", val)
		items := sqlutil.SplitTopLevel(valList, ',')
		found := false
		for _, item := range items {
			item = strings.TrimSpace(strings.Trim(item, "'\""))
			if strings.EqualFold(strVal, item) {
				found = true
				break
			}
		}
		if isNot {
			return !found
		}
		return found
	}

	if m := reEvalIsNull.FindStringSubmatch(whereStr); len(m) == 2 {
		colName := cleanIdentifier(m[1])
		val, ok := rec[colName]
		if !ok {
			if idx := strings.Index(colName, "."); idx != -1 {
				val, ok = rec[colName[idx+1:]]
			}
		}
		return !ok || val == nil
	}

	if m := reEvalIsNotNull.FindStringSubmatch(whereStr); len(m) == 2 {
		colName := cleanIdentifier(m[1])
		val, ok := rec[colName]
		if !ok {
			if idx := strings.Index(colName, "."); idx != -1 {
				val, ok = rec[colName[idx+1:]]
			}
		}
		return ok && val != nil
	}

	if m := reEvalBinaryOp.FindStringSubmatch(whereStr); len(m) == 4 {
		colName := cleanIdentifier(m[1])
		op := strings.ToUpper(strings.TrimSpace(m[2]))
		target := strings.TrimSpace(strings.Trim(m[3], "'\""))

		val, ok := rec[colName]
		if !ok {
			if idx := strings.Index(colName, "."); idx != -1 {
				val, ok = rec[colName[idx+1:]]
			}
		}
		if !ok || val == nil {
			return op == "!=" || op == "<>"
		}

		strVal := fmt.Sprintf("%v", val)
		switch op {
		case "=":
			return strings.EqualFold(strVal, target)
		case "!=", "<>":
			return !strings.EqualFold(strVal, target)
		case "LIKE", "ILIKE":
			pattern := strings.ReplaceAll(regexp.QuoteMeta(target), "%", ".*")
			pattern = strings.ReplaceAll(pattern, "_", ".")
			re, err := regexp.Compile("(?i)^" + pattern + "$")
			if err != nil {
				return false
			}
			return re.MatchString(strVal)
		case "NOT LIKE", "NOT ILIKE":
			pattern := strings.ReplaceAll(regexp.QuoteMeta(target), "%", ".*")
			pattern = strings.ReplaceAll(pattern, "_", ".")
			re, err := regexp.Compile("(?i)^" + pattern + "$")
			if err != nil {
				return false
			}
			return !re.MatchString(strVal)
		}
	}

	return true
}

func splitLogicalConditions(whereStr, op string) []string {
	var parts []string
	parenDepth := 0
	inQuote := false
	quoteChar := byte(0)
	lastIdx := 0

	for i := 0; i < len(whereStr); i++ {
		ch := whereStr[i]
		if inQuote {
			if ch == quoteChar {
				inQuote = false
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			inQuote = true
			quoteChar = ch
			continue
		}
		if ch == '(' {
			parenDepth++
			continue
		}
		if ch == ')' {
			if parenDepth > 0 {
				parenDepth--
			}
			continue
		}
		if parenDepth == 0 && i+len(op) <= len(whereStr) && strings.EqualFold(whereStr[i:i+len(op)], op) {
			part := strings.TrimSpace(whereStr[lastIdx:i])
			if part != "" {
				parts = append(parts, part)
			}
			lastIdx = i + len(op)
			i += len(op) - 1
		}
	}
	if lastIdx < len(whereStr) {
		part := strings.TrimSpace(whereStr[lastIdx:])
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func splitScalarProjections(s string) []string {
	var res []string
	var cur strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	parenDepth := 0

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			cur.WriteByte(ch)
			continue
		}
		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			cur.WriteByte(ch)
			continue
		}
		if inSingleQuote || inDoubleQuote {
			cur.WriteByte(ch)
			continue
		}
		if ch == '(' {
			parenDepth++
			cur.WriteByte(ch)
			continue
		}
		if ch == ')' {
			if parenDepth > 0 {
				parenDepth--
			}
			cur.WriteByte(ch)
			continue
		}
		if ch == ',' && parenDepth == 0 {
			res = append(res, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	if cur.Len() > 0 {
		res = append(res, strings.TrimSpace(cur.String()))
	}
	return res
}

func cleanIdentifier(s string) string {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ".")
	last := parts[len(parts)-1]
	return sqlutil.CleanIdentifier(last)
}

func getMapValueCaseInsensitive(rec map[string]any, col string) any {
	if val, ok := rec[col]; ok {
		return val
	}
	if idx := strings.LastIndex(col, "."); idx != -1 {
		if val, ok := rec[col[idx+1:]]; ok {
			return val
		}
	}
	for k, v := range rec {
		if strings.EqualFold(k, col) {
			return v
		}
		if idx := strings.LastIndex(col, "."); idx != -1 {
			if strings.EqualFold(k, col[idx+1:]) {
				return v
			}
		}
	}
	return nil
}
