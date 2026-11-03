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
	// No package-level regexes
)

func findTopLevelKeyword(s, kw string) int {
	depth := 0
	inQuote := false
	var quoteChar byte
	kwUpper := strings.ToUpper(kw)
	nKw := len(kw)
	n := len(s)

	for i := 0; i < n; i++ {
		ch := s[i]
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
		if ch == '(' || ch == '[' {
			depth++
			continue
		}
		if ch == ')' || ch == ']' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 && i+nKw <= n {
			beforeOk := (i == 0) || isWhitespaceOrPunctByte(s[i-1])
			afterOk := (i+nKw == n) || isWhitespaceOrPunctByte(s[i+nKw])
			if beforeOk && afterOk && strings.EqualFold(s[i:i+nKw], kwUpper) {
				return i
			}
		}
	}
	return -1
}

func isWhitespaceOrPunctByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == ',' || b == ';' || b == '(' || b == ')'
}

func parseImplicitAlias(s string) (alias, colExpr string, ok bool) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, ")") || strings.HasSuffix(s, "]") {
		return "", s, false
	}
	depth := 0
	inQuote := false
	var quoteChar byte
	lastSpaceIdx := -1

	for i := 0; i < len(s); i++ {
		ch := s[i]
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
		if ch == '(' || ch == '[' {
			depth++
			continue
		}
		if ch == ')' || ch == ']' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 && (ch == ' ' || ch == '\t') {
			lastSpaceIdx = i
		}
	}

	if lastSpaceIdx > 0 {
		expr := strings.TrimSpace(s[:lastSpaceIdx])
		potentialAlias := strings.TrimSpace(s[lastSpaceIdx+1:])
		if potentialAlias != "" {
			if strings.HasPrefix(potentialAlias, "[") && strings.HasSuffix(potentialAlias, "]") && len(potentialAlias) > 2 {
				return cleanIdentifier(potentialAlias), expr, true
			}
			if strings.HasPrefix(potentialAlias, "`") && strings.HasSuffix(potentialAlias, "`") && len(potentialAlias) > 2 {
				return cleanIdentifier(potentialAlias), expr, true
			}
			if strings.HasPrefix(potentialAlias, "\"") && strings.HasSuffix(potentialAlias, "\"") && len(potentialAlias) > 2 {
				return cleanIdentifier(potentialAlias), expr, true
			}
			if !strings.ContainsAny(potentialAlias, " ()[],.") {
				return cleanIdentifier(potentialAlias), expr, true
			}
		}
	}
	return "", s, false
}

// ExtractSQLParts extracts projStr, whereClause, and orderByClause from a SELECT query.
func ExtractSQLParts(sql string) (projStr, whereClause, orderByClause string) {
	projStr = "*"
	if fromIdx := findTopLevelKeyword(sql, "FROM"); fromIdx != -1 {
		selectPart := sql[:fromIdx]
		if selectIdx := findTopLevelKeyword(selectPart, "SELECT"); selectIdx != -1 {
			proj := strings.TrimSpace(selectPart[selectIdx+6:])
			if proj != "" {
				projStr = proj
			}
		}
	}
	if whereIdx := findTopLevelKeyword(sql, "WHERE"); whereIdx != -1 {
		rest := sql[whereIdx+5:]
		if orderIdx := findTopLevelKeyword(rest, "ORDER BY"); orderIdx != -1 {
			whereClause = strings.TrimSpace(rest[:orderIdx])
			orderByClause = strings.TrimSpace(rest[orderIdx+8:])
		} else {
			whereClause = strings.TrimSpace(rest)
		}
	} else if orderIdx := findTopLevelKeyword(sql, "ORDER BY"); orderIdx != -1 {
		orderByClause = strings.TrimSpace(sql[orderIdx+8:])
	}
	return projStr, whereClause, orderByClause
}

// FormatResultWithSQL parses projection, WHERE, and ORDER BY clauses from sql and applies them to allRows.
func FormatResultWithSQL(columns []string, allRows []map[string]any, sql string) (*Result, error) {
	projStr, whereClause, orderByClause := ExtractSQLParts(sql)
	return FormatResult(columns, allRows, projStr, whereClause, orderByClause, -1)
}

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
					cmp := sqlutil.CompareAny(v1Val, v2Val)
					if cmp == 0 {
						continue
					}
					if item.Desc {
						return cmp > 0
					}
					return cmp < 0
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
			if a, expr, ok := parseTopLevelAssignment(cTrimmed); ok {
				alias = a
				colExpr = expr
			} else if asIdx := sqlutil.FindTopLevelKeyword(cTrimmed, "AS"); asIdx != -1 {
				colExpr = strings.TrimSpace(cTrimmed[:asIdx])
				alias = cleanIdentifier(strings.TrimSpace(cTrimmed[asIdx+2:]))
			} else if a, expr, ok := parseImplicitAlias(cTrimmed); ok {
				alias = a
				colExpr = expr
			}
			rawCol := strings.ToLower(cleanIdentifier(colExpr))
			if !strings.Contains(colExpr, " ") && !strings.Contains(colExpr, "(") && strings.Contains(rawCol, ".") {
				rawCol = rawCol[strings.LastIndex(rawCol, ".")+1:]
			}
			outName := alias
			if outName == "" {
				outName = rawCol
			}
			selectedCols = append(selectedCols, outName)
			colSourceMap[outName] = colExpr
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
			var val any
			var found bool
			if v, ok := fr[col]; ok {
				val = v
				found = true
			} else if v, ok := fr[strings.ToLower(col)]; ok {
				val = v
				found = true
			} else if v, ok := fr[strings.ReplaceAll(strings.ToLower(col), "-", "_")]; ok {
				val = v
				found = true
			} else if src != "" {
				cleanedSrc := cleanIdentifier(src)
				if v, ok := fr[src]; ok {
					val = v
					found = true
				} else if v, ok := fr[strings.ToLower(src)]; ok {
					val = v
					found = true
				} else if v, ok := fr[cleanedSrc]; ok {
					val = v
					found = true
				} else if v, ok := fr[strings.ToLower(cleanedSrc)]; ok {
					val = v
					found = true
				} else if dotIdx := strings.LastIndex(cleanedSrc, "."); dotIdx != -1 {
					colOnly := strings.ToLower(cleanedSrc[dotIdx+1:])
					if v, ok := fr[colOnly]; ok {
						val = v
						found = true
					}
				}
			}

			if !found {
				upperSrc := strings.ToUpper(src)
				upperCol := strings.ToUpper(col)
				if upperCol == "DATABASE-TYPE" || strings.Contains(upperSrc, "UDT_NAME") || strings.Contains(upperSrc, "TYPNAME") {
					if dt, ok := fr["data_type"]; ok && dt != nil {
						val = dt
						found = true
					} else if udt, ok := fr["udt_name"]; ok && udt != nil {
						val = udt
						found = true
					}
				} else if upperCol == "DATABASE-POSITION" || strings.Contains(upperSrc, "ORDINAL_POSITION - 1") || strings.Contains(upperSrc, "ATTNUM - 1") {
					if ord, ok := fr["ordinal_position"]; ok && ord != nil {
						switch n := ord.(type) {
						case int:
							val = n - 1
						case int64:
							val = n - 1
						case int32:
							val = n - 1
						}
						found = true
					} else if att, ok := fr["attnum"]; ok && att != nil {
						switch n := att.(type) {
						case int:
							val = n - 1
						case int64:
							val = n - 1
						case int32:
							val = n - 1
						}
						found = true
					}
				} else if upperCol == "PK?" {
					isPK := false
					if pk, ok := fr["is_primary_key"].(bool); ok && pk {
						isPK = true
					} else if pk, ok := fr["pk?"].(bool); ok && pk {
						isPK = true
					}
					val = isPK
					found = true
				} else if (strings.Contains(upperSrc, "CASE WHEN") && (strings.Contains(upperSrc, "IC.COLUMN_ID") || strings.Contains(upperSrc, "PRIMARY"))) ||
					strings.Contains(upperSrc, "ISNULL(P.IS_PRIMARY_KEY") ||
					strings.Contains(upperCol, "PRIMARY_KEY") || strings.Contains(upperCol, "IS_PK") {
					isPK := false
					if pk, ok := fr["is_primary_key"].(bool); ok && pk {
						isPK = true
					}
					if isPK {
						val = int64(1)
					} else {
						val = int64(0)
					}
					found = true
				} else if upperCol == "DATABASE-REQUIRED" {
					if isNull, ok := fr["is_nullable"].(string); ok {
						val = (strings.ToUpper(isNull) == "NO")
						found = true
					}
				} else if upperCol == "DATABASE-IS-NULLABLE" {
					if isNull, ok := fr["is_nullable"].(string); ok {
						val = (strings.ToUpper(isNull) == "YES")
						found = true
					}
				} else if upperCol == "DATABASE-IS-AUTO-INCREMENT" {
					val = false
					if isIdent, ok := fr["is_identity"].(string); ok {
						val = (strings.ToUpper(isIdent) == "YES")
					}
					found = true
				} else if upperCol == "DATABASE-IS-GENERATED" {
					val = false
					found = true
				} else if upperCol == "DATABASE-DEFAULT" {
					val = fr["column_default"]
					found = true
				} else if upperCol == "FIELD-COMMENT" {
					val = fr["comment"]
					found = true
				} else if (strings.Contains(upperSrc, "CASE WHEN") && strings.Contains(upperSrc, "TEXT IS NULL")) ||
					upperCol == "DEFAULT_IS_NULL" {
					val = true
					found = true
				} else if strings.Contains(upperSrc, "REPLACE") && strings.Contains(upperSrc, "ISNULL") {
					val = ""
					found = true
				} else if strings.Contains(upperSrc, "HAS_PERMS_BY_NAME") {
					val = 1
					found = true
				} else if upperSrc == "NULL" {
					val = nil
					found = true
				}
			}

			if found {
				row[col] = val
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

	if m := reMssqlObjectIdCallEq.FindStringSubmatch(whereStr); len(m) == 2 {
		targetName := cleanTargetTableName(m[1])
		tbl := getMapValueCaseInsensitive(rec, "tableName")
		if tbl == nil {
			tbl = getMapValueCaseInsensitive(rec, "table_name")
		}
		if tbl != nil {
			return strings.EqualFold(fmt.Sprintf("%v", tbl), targetName)
		}
		return true
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
	whereStr = strings.TrimRight(strings.TrimSpace(whereStr), ";")
	if inner, ok := sqlutil.StripParentheses(whereStr); ok {
		whereStr = inner
	}

	lookupRec := func(col string) (any, bool) {
		val, ok := rec[col]
		if !ok {
			if idx := strings.Index(col, "."); idx != -1 {
				val, ok = rec[col[idx+1:]]
			}
		}
		return val, ok
	}

	// 1. IS NULL / IS NOT NULL
	if pred, ok := sqlutil.ParseIsNullPredicate(whereStr); ok {
		val, ok := lookupRec(pred.Column)
		if pred.IsNot {
			return ok && val != nil
		}
		return !ok || val == nil
	}

	// 2. IN / NOT IN
	if pred, ok := sqlutil.ParseInPredicate(whereStr); ok {
		val, ok := lookupRec(pred.Column)
		if !ok || val == nil {
			return pred.IsNot
		}
		strVal := fmt.Sprintf("%v", val)
		found := false
		for _, item := range pred.Items {
			item = strings.TrimSpace(strings.Trim(item, "'\""))
			if strings.EqualFold(strVal, item) {
				found = true
				break
			}
		}
		if pred.IsNot {
			return !found
		}
		return found
	}

	// 3. ANY clause: e.g. col = ANY(...) or col != ANY(...)
	if pred, ok := sqlutil.ParseAnyPredicate(whereStr); ok {
		val, ok := lookupRec(pred.Column)
		if !ok || val == nil {
			return pred.Op == "!=" || pred.Op == "<>"
		}
		strVal := fmt.Sprintf("%v", val)
		found := false
		for _, item := range pred.Items {
			item = strings.TrimSpace(strings.Trim(item, "'\""))
			if strings.EqualFold(strVal, item) {
				found = true
				break
			}
		}
		if pred.Op == "=" {
			return found
		}
		return !found
	}

	if comp, ok := sqlutil.ParseBinaryComparison(whereStr); ok {
		colName := comp.Column
		op := comp.Op
		rawTarget := comp.RightRaw
		target := comp.RightClean

		// Handle Join conditions (e.g. "c"."relnamespace" = "n"."oid" or "n"."oid" = "c"."relnamespace")
		targetCol := cleanIdentifier(rawTarget)
		if op == "=" {
			if (colName == "relnamespace" || strings.HasSuffix(colName, "relnamespace")) &&
				(targetCol == "oid" || strings.HasSuffix(targetCol, "oid") || targetCol == "relnamespace") {
				return true
			}
			if (targetCol == "relnamespace" || strings.HasSuffix(targetCol, "relnamespace")) &&
				(colName == "oid" || strings.HasSuffix(colName, "oid") || colName == "relnamespace") {
				return true
			}
		}

		val, ok := lookupRec(colName)
		if !ok || val == nil {
			return op == "!=" || op == "<>" || op == "!~" || op == "!~*"
		}

		strVal := fmt.Sprintf("%v", val)
		switch op {
		case "=":
			return strings.EqualFold(strVal, target)
		case "!=", "<>":
			return !strings.EqualFold(strVal, target)
		case "~", "~*":
			rePat := target
			if op == "~*" {
				rePat = "(?i)" + rePat
			}
			re, err := regexp.Compile(rePat)
			if err != nil {
				return false
			}
			return re.MatchString(strVal)
		case "!~", "!~*":
			rePat := target
			if op == "!~*" {
				rePat = "(?i)" + rePat
			}
			re, err := regexp.Compile(rePat)
			if err != nil {
				return false
			}
			return !re.MatchString(strVal)
		case ">", ">=", "<", "<=":
			if n1, err1 := strconv.ParseFloat(strVal, 64); err1 == nil {
				if n2, err2 := strconv.ParseFloat(target, 64); err2 == nil {
					switch op {
					case ">":
						return n1 > n2
					case ">=":
						return n1 >= n2
					case "<":
						return n1 < n2
					case "<=":
						return n1 <= n2
					}
				}
			}
			return false
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

func parseTopLevelAssignment(s string) (alias string, colExpr string, ok bool) {
	eqIdx := sqlutil.FindTopLevelChar(s, '=')
	if eqIdx == -1 {
		return "", s, false
	}
	left := strings.TrimSpace(s[:eqIdx])
	right := strings.TrimSpace(s[eqIdx+1:])
	if left == "" || right == "" {
		return "", s, false
	}
	upperLeft := strings.ToUpper(left)
	if strings.HasPrefix(upperLeft, "CASE") || strings.HasPrefix(upperLeft, "WHEN") {
		return "", s, false
	}
	cleaned := sqlutil.CleanIdentifier(left)
	if strings.Contains(cleaned, " ") && !strings.HasPrefix(left, "[") && !strings.HasPrefix(left, "\"") && !strings.HasPrefix(left, "'") {
		return "", s, false
	}
	return cleaned, right, true
}

func cleanIdentifier(s string) string {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "(") || strings.Contains(s, " ") {
		return sqlutil.CleanIdentifier(s)
	}
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
