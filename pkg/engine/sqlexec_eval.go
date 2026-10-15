package engine

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/planner"
	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

func parseValueTuples(s string) []string {
	var tuples []string
	var cur strings.Builder
	inTuple := false
	inString := false
	var quoteChar byte
	depth := 0

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			cur.WriteByte(c)
			if c == quoteChar {
				if i+1 < len(s) && s[i+1] == quoteChar {
					i++
					cur.WriteByte(s[i])
					continue
				}
				inString = false
			}
			continue
		}
		if c == '\'' || c == '"' {
			inString = true
			quoteChar = c
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

func splitValues(s string) []string {
	var vals []string
	var cur strings.Builder
	inString := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' && (i == 0 || s[i-1] != '\\') {
			inString = !inString
			cur.WriteByte(c)
			continue
		}
		if !inString && c == ',' {
			vals = append(vals, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if cur.Len() > 0 {
		vals = append(vals, strings.TrimSpace(cur.String()))
	}
	return vals
}

func parseLiteralValue(s string, targetType storage.DataType) storage.Value {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "NULL") {
		return storage.NullValue(targetType)
	}

	upper := strings.ToUpper(s)
	switch upper {
	case "NEWID7()", "UUIDV7()", "UUID_GENERATE_V7()":
		return storage.UUIDValue(proc.GenerateUUIDv7())
	case "NEWID()", "GEN_RANDOM_UUID()":
		return storage.UUIDValue(proc.GenerateUUIDv4())
	case "GETDATE()", "NOW()", "CURRENT_TIMESTAMP":
		return storage.DateTimeValue(time.Now())
	case "GETUTCDATE()", "SYSDATETIME()":
		return storage.DateTimeValue(time.Now().UTC())
	}

	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
		strVal := s[1 : len(s)-1]
		strVal = strings.ReplaceAll(strVal, "''", "'")
		if targetType == storage.TypeJSON {
			return storage.JSONValue(strVal)
		}
		if targetType == storage.TypeUUID {
			return storage.UUIDValue(strVal)
		}
		if targetType == storage.TypeDate {
			return storage.DateValueFromString(strVal)
		}
		if targetType == storage.TypeTime {
			return storage.TimeValueFromString(strVal)
		}
		if targetType == storage.TypeBinary {
			return storage.BinaryValueFromString(strVal)
		}
		return storage.StringValue(strVal)
	}

	switch targetType {
	case storage.TypeJSON:
		return storage.JSONValue(s)
	case storage.TypeUUID:
		return storage.UUIDValue(s)
	case storage.TypeBinary:
		return storage.BinaryValueFromString(s)
	case storage.TypeDate:
		return storage.DateValueFromString(s)
	case storage.TypeTime:
		return storage.TimeValueFromString(s)
	case storage.TypeInt:
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return storage.IntValue(i)
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.IntValue(int64(f))
		}
	case storage.TypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.FloatValue(f)
		}
	case storage.TypeDecimal:
		return storage.DecimalValue(s)
	case storage.TypeBool:
		if strings.EqualFold(s, "TRUE") || s == "1" {
			return storage.BoolValue(true)
		}
		if strings.EqualFold(s, "FALSE") || s == "0" {
			return storage.BoolValue(false)
		}
	}
	return storage.StringValue(s)
}

func formatStorageValue(v storage.Value) any {
	if v.IsNull {
		return nil
	}
	switch v.Type {
	case storage.TypeInt:
		return v.IVal
	case storage.TypeVarChar, storage.TypeJSON:
		return v.SVal
	case storage.TypeFloat:
		return v.FVal
	case storage.TypeDecimal:
		return v.Decimal().String()
	case storage.TypeBool:
		return v.BVal
	case storage.TypeDateTime:
		return v.TVal
	case storage.TypeDate:
		return v.String()
	case storage.TypeTime:
		return v.String()
	case storage.TypeUUID:
		return v.SVal
	case storage.TypeBinary:
		return v.String()
	default:
		return v.String()
	}
}

func splitLogicalConditions(whereStr string, op string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)
	parenDepth := 0
	opLen := len(op)
	upper := strings.ToUpper(whereStr)

	for i := 0; i < len(whereStr); i++ {
		ch := whereStr[i]
		if inQuote {
			if ch == quoteChar && (i == 0 || whereStr[i-1] != '\\') {
				inQuote = false
			}
			cur.WriteByte(ch)
		} else {
			if ch == '\'' || ch == '"' {
				inQuote = true
				quoteChar = ch
				cur.WriteByte(ch)
			} else if ch == '(' {
				parenDepth++
				cur.WriteByte(ch)
			} else if ch == ')' {
				if parenDepth > 0 {
					parenDepth--
				}
				cur.WriteByte(ch)
			} else if parenDepth == 0 && i+opLen <= len(whereStr) && upper[i:i+opLen] == op {
				part := strings.TrimSpace(cur.String())
				if part != "" {
					parts = append(parts, part)
				}
				cur.Reset()
				i += opLen - 1
			} else {
				cur.WriteByte(ch)
			}
		}
	}
	if cur.Len() > 0 {
		part := strings.TrimSpace(cur.String())
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func parseValueToType(s string, t storage.DataType) storage.Value {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "NULL") {
		return storage.NullValue(t)
	}
	s = strings.Trim(s, "'\"")
	switch t {
	case storage.TypeInt:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return storage.IntValue(n)
		}
	case storage.TypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.FloatValue(f)
		}
	case storage.TypeDecimal:
		return storage.DecimalValue(s)
	case storage.TypeBool:
		if b, err := strconv.ParseBool(s); err == nil {
			return storage.BoolValue(b)
		}
	case storage.TypeDateTime:
		if tVal, err := time.Parse(time.RFC3339, s); err == nil {
			return storage.DateTimeValue(tVal)
		}
	case storage.TypeDate:
		return storage.DateValueFromString(s)
	case storage.TypeTime:
		return storage.TimeValueFromString(s)
	case storage.TypeUUID:
		return storage.UUIDValue(s)
	case storage.TypeBinary:
		return storage.BinaryValueFromString(s)
	}
	return storage.StringValue(s)
}

func evaluateWhereCondition(rec storage.Record, whereStr string) bool {
	whereStr = strings.TrimSpace(whereStr)
	if whereStr == "" || whereStr == "1=1" || whereStr == "1 = 1" || strings.EqualFold(whereStr, "NOT 1=0") || strings.EqualFold(whereStr, "NOT 1 = 0") {
		return true
	}
	if whereStr == "1=0" || whereStr == "1 = 0" || strings.EqualFold(whereStr, "NOT 1=1") || strings.EqualFold(whereStr, "NOT 1 = 1") {
		return false
	}

	rMap := make(map[string]any, len(rec))
	for k, v := range rec {
		rMap[k] = formatStorageValue(v)
	}
	return evaluateWhereConditionMap(rMap, whereStr)
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

	inRe := regexp.MustCompile(`(?is)^\s*([a-zA-Z0-9_\.]+)\s+(NOT\s+)?IN\s*\((.*?)\)\s*$`)
	if m := inRe.FindStringSubmatch(whereStr); len(m) == 4 {
		colName := cleanIdentifier(m[1])
		isNot := strings.TrimSpace(m[2]) != ""
		valList := m[3]
		val, ok := rec[colName]
		if !ok {
			if idx := strings.Index(colName, "."); idx != -1 {
				val, ok = rec[colName[idx+1:]]
			}
		}
		if !ok || val == nil {
			return false
		}
		strVal := fmt.Sprintf("%v", val)
		found := false
		for _, rawItem := range strings.Split(valList, ",") {
			item := strings.Trim(strings.TrimSpace(rawItem), "'\"")
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

	upperWhere := strings.ToUpper(whereStr)
	if strings.HasSuffix(upperWhere, " IS NOT NULL") {
		colName := cleanIdentifier(whereStr[:len(whereStr)-len(" IS NOT NULL")])
		val, ok := rec[colName]
		if !ok {
			if idx := strings.Index(colName, "."); idx != -1 {
				val, ok = rec[colName[idx+1:]]
			}
		}
		if !ok {
			for k, v := range rec {
				if strings.EqualFold(k, colName) {
					val, ok = v, true
					break
				}
			}
		}
		return ok && val != nil
	}
	if strings.HasSuffix(upperWhere, " IS NULL") {
		colName := cleanIdentifier(whereStr[:len(whereStr)-len(" IS NULL")])
		val, ok := rec[colName]
		if !ok {
			if idx := strings.Index(colName, "."); idx != -1 {
				val, ok = rec[colName[idx+1:]]
			}
		}
		if !ok {
			for k, v := range rec {
				if strings.EqualFold(k, colName) {
					val, ok = v, true
					break
				}
			}
		}
		return !ok || val == nil
	}

	ops := []string{" NOT LIKE ", " NOT ILIKE ", "!=", "<>", "<=", ">=", "=", "<", ">", " LIKE ", " ILIKE "}
	for _, op := range ops {
		idx := strings.Index(strings.ToUpper(whereStr), op)
		if idx != -1 {
			leftCol := cleanIdentifier(whereStr[:idx])
			rightVal := strings.TrimSpace(whereStr[idx+len(op):])
			rightVal = strings.Trim(rightVal, "';\"")

			if leftCol == "1" && rightVal == "1" && op == "=" {
				return true
			}
			if leftCol == "1" && rightVal == "0" && op == "=" {
				return false
			}

			val, ok := rec[leftCol]
			if !ok {
				if dotIdx := strings.Index(leftCol, "."); dotIdx != -1 {
					val, ok = rec[leftCol[dotIdx+1:]]
				}
			}
			if !ok {
				for k, v := range rec {
					if strings.EqualFold(k, leftCol) {
						val, ok = v, true
						break
					}
				}
			}
			if !ok {
				return false
			}

			strVal := fmt.Sprintf("%v", val)
			switch strings.TrimSpace(op) {
			case "=":
				return strings.EqualFold(strVal, rightVal)
			case "!=", "<>":
				return !strings.EqualFold(strVal, rightVal)
			case ">":
				n1, err1 := strconv.ParseFloat(strVal, 64)
				n2, err2 := strconv.ParseFloat(rightVal, 64)
				if err1 == nil && err2 == nil {
					return n1 > n2
				}
				return strVal > rightVal
			case ">=":
				n1, err1 := strconv.ParseFloat(strVal, 64)
				n2, err2 := strconv.ParseFloat(rightVal, 64)
				if err1 == nil && err2 == nil {
					return n1 >= n2
				}
				return strVal >= rightVal
			case "<":
				n1, err1 := strconv.ParseFloat(strVal, 64)
				n2, err2 := strconv.ParseFloat(rightVal, 64)
				if err1 == nil && err2 == nil {
					return n1 < n2
				}
				return strVal < rightVal
			case "<=":
				n1, err1 := strconv.ParseFloat(strVal, 64)
				n2, err2 := strconv.ParseFloat(rightVal, 64)
				if err1 == nil && err2 == nil {
					return n1 <= n2
				}
				return strVal <= rightVal
			case "LIKE", "ILIKE":
				pat := strings.ReplaceAll(regexp.QuoteMeta(rightVal), "%", ".*")
				pat = strings.ReplaceAll(pat, "_", ".")
				matched, _ := regexp.MatchString("(?i)^"+pat+"$", strVal)
				return matched
			case "NOT LIKE", "NOT ILIKE":
				pat := strings.ReplaceAll(regexp.QuoteMeta(rightVal), "%", ".*")
				pat = strings.ReplaceAll(pat, "_", ".")
				matched, _ := regexp.MatchString("(?i)^"+pat+"$", strVal)
				return !matched
			}
		}
	}
	return true
}

// deduplicateRows removes duplicate rows from a result set for SELECT DISTINCT.
// Rows are compared by serializing all column key-value pairs in sorted key order.
func deduplicateRows(rows []map[string]any) []map[string]any {
	if len(rows) == 0 {
		return rows
	}
	seen := make(map[string]struct{}, len(rows))
	out := make([]map[string]any, 0, len(rows))
	var buf strings.Builder
	for _, row := range rows {
		// Collect and sort keys for deterministic ordering
		keys := make([]string, 0, len(row))
		for k := range row {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		buf.Reset()
		for _, k := range keys {
			buf.WriteString(k)
			buf.WriteByte('=')
			v := row[k]
			if sv, ok := v.(storage.Value); ok && sv.IsNull {
				buf.WriteString("NULL")
			} else {
				fmt.Fprintf(&buf, "%v", v)
			}
			buf.WriteByte('\x00')
		}
		key := buf.String()
		if _, dup := seen[key]; !dup {
			seen[key] = struct{}{}
			out = append(out, row)
		}
	}
	return out
}

// rewriteViewsAsCTE detects view references in a SELECT query and rewrites the query as a CTE.
// For example:
//
//	SELECT name FROM v_eng WHERE salary > 80000
//	-> WITH v_eng AS (SELECT ... FROM employees WHERE dept='Engineering')
//	   SELECT name FROM v_eng WHERE salary > 80000
//
// Multiple views are accumulated into a single WITH clause.
// Returns the rewritten query and true if at least one view was found.
func (s *Coordinator) rewriteViewsAsCTE(query string) (string, bool) {
	views := s.catalog.ListViews()
	if len(views) == 0 {
		return query, false
	}

	// Collect all views referenced in this query (word-boundary match against view names).
	var cteParts []string
	for _, v := range views {
		esc := regexp.QuoteMeta(v.Name)
		// Match the view name as a standalone identifier (not inside quotes or parens).
		re := regexp.MustCompile(`(?i)\b` + esc + `\b`)
		if re.MatchString(query) {
			cteParts = append(cteParts, fmt.Sprintf("%s AS (%s)", v.Name, v.Query))
		}
	}

	if len(cteParts) == 0 {
		return query, false
	}

	rewritten := "WITH " + strings.Join(cteParts, ", ") + " " + query
	return rewritten, true
}

// parseEqualityConditions parses AND-separated equality conditions (col = val) from a WHERE clause.
// Returns a map of lowercase column name -> raw value string (without surrounding quotes).
// Non-equality conditions (!=, <, >, LIKE, IN, etc.) are ignored.
func parseEqualityConditions(where string) map[string]string {
	result := make(map[string]string)
	// Split on AND (case-insensitive), skipping parenthesized groups.
	parts := splitOnAnd(where)
	eqRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*=\s*(.+?)\s*$`)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if m := eqRe.FindStringSubmatch(part); len(m) == 3 {
			rawCol := m[1]
			// Strip table qualifier (tbl.col -> col)
			if dot := strings.LastIndex(rawCol, "."); dot != -1 {
				rawCol = rawCol[dot+1:]
			}
			val := strings.Trim(strings.TrimSpace(m[2]), "'\"")
			result[strings.ToLower(rawCol)] = val
		}
	}
	return result
}

// splitOnAnd splits a WHERE clause on top-level AND keywords,
// respecting parentheses depth so subexpressions are not split.
func splitOnAnd(where string) []string {
	var parts []string
	depth := 0
	start := 0
	upper := strings.ToUpper(where)
	for i := 0; i < len(upper); i++ {
		switch upper[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 && i+4 <= len(upper) && upper[i:i+4] == " AND" &&
			(i+4 == len(upper) || upper[i+4] == ' ' || upper[i+4] == '(') {
			parts = append(parts, strings.TrimSpace(where[start:i]))
			i += 4
			start = i
		}
	}
	parts = append(parts, strings.TrimSpace(where[start:]))
	return parts
}

// buildCompositePrefixKey builds a \x00-separated composite key string from the
// prefix columns of a composite index given an equality-condition value map.
// The value lookup is case-insensitive on column names.
func buildCompositePrefixKey(cols []string, eqColVals map[string]string, schema *storage.TableSchema) string {
	var sb strings.Builder
	sb.Grow(64)
	for i, col := range cols {
		val, ok := eqColVals[strings.ToLower(col)]
		if !ok {
			break
		}
		if i > 0 {
			sb.WriteByte(0)
		}
		// Format the value using storage.EncodeIndexKeyPart for consistent composite key encoding.
		colMeta, _, found := schema.FindColumn(col)
		if found && colMeta != nil {
			sv := parseValueToType(val, colMeta.Type)
			sb.WriteString(storage.EncodeIndexKeyPart(sv))
		} else {
			sb.WriteString(storage.EncodeIndexKeyPart(storage.StringValue(val)))
		}
	}
	return sb.String()
}

type indexRangeCond struct {
	hasMin bool
	minVal string
	hasMax bool
	maxVal string
}

func parseWhereConditions(where string) (map[string]string, map[string]*indexRangeCond) {
	eqs := make(map[string]string)
	ranges := make(map[string]*indexRangeCond)
	parts := splitOnAnd(where)

	eqRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*=\s*(.+?)\s*$`)
	gteRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*>=\s*(.+?)\s*$`)
	gtRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*>\s*(.+?)\s*$`)
	lteRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*<=\s*(.+?)\s*$`)
	ltRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*<\s*(.+?)\s*$`)
	betweenRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s+BETWEEN\s+(.+?)\s+AND\s+(.+?)\s*$`)

	cleanCol := func(s string) string {
		raw := strings.TrimSpace(s)
		if dot := strings.LastIndex(raw, "."); dot != -1 {
			raw = raw[dot+1:]
		}
		return strings.ToLower(cleanIdentifier(raw))
	}
	cleanVal := func(s string) string {
		return strings.Trim(strings.TrimSpace(s), "'\"")
	}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if m := eqRe.FindStringSubmatch(part); len(m) == 3 {
			col := cleanCol(m[1])
			eqs[col] = cleanVal(m[2])
		} else if m := betweenRe.FindStringSubmatch(part); len(m) == 4 {
			col := cleanCol(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &indexRangeCond{}
				ranges[col] = r
			}
			r.hasMin = true
			r.minVal = cleanVal(m[2])
			r.hasMax = true
			r.maxVal = cleanVal(m[3])
		} else if m := gteRe.FindStringSubmatch(part); len(m) == 3 {
			col := cleanCol(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &indexRangeCond{}
				ranges[col] = r
			}
			r.hasMin = true
			r.minVal = cleanVal(m[2])
		} else if m := gtRe.FindStringSubmatch(part); len(m) == 3 {
			col := cleanCol(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &indexRangeCond{}
				ranges[col] = r
			}
			r.hasMin = true
			r.minVal = cleanVal(m[2])
		} else if m := lteRe.FindStringSubmatch(part); len(m) == 3 {
			col := cleanCol(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &indexRangeCond{}
				ranges[col] = r
			}
			r.hasMax = true
			r.maxVal = cleanVal(m[2])
		} else if m := ltRe.FindStringSubmatch(part); len(m) == 3 {
			col := cleanCol(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &indexRangeCond{}
				ranges[col] = r
			}
			r.hasMax = true
			r.maxVal = cleanVal(m[2])
		}
	}
	return eqs, ranges
}

func (s *Coordinator) findIndexAndBounds(schema *storage.TableSchema, whereClause string) (*storage.IndexSchema, *storage.Value, *storage.Value, bool) {
	if schema == nil || len(schema.Indexes) == 0 || whereClause == "" {
		return nil, nil, nil, false
	}
	eqColVals, rangeCols := parseWhereConditions(whereClause)
	eqColSet := make(map[string]bool, len(eqColVals))
	for col := range eqColVals {
		eqColSet[col] = true
	}

	// 1. Try composite index first
	idx, prefixLen := schema.FindBestCompositeIndex(eqColSet)
	if idx != nil && len(idx.Columns) > 1 {
		if prefixLen > 0 {
			prefixKey := buildCompositePrefixKey(idx.Columns[:prefixLen], eqColVals, schema)
			// Check if next column has a range condition
			if prefixLen < len(idx.Columns) {
				nextCol := strings.ToLower(idx.Columns[prefixLen])
				if r, ok := rangeCols[nextCol]; ok && r != nil {
					nextMeta, _, found := schema.FindColumn(nextCol)
					if found && nextMeta != nil {
						lowStr := prefixKey
						highStr := prefixKey + "\xff\xff\xff\xff"
						if r.hasMin {
							encMin := storage.EncodeIndexKeyPart(parseValueToType(r.minVal, nextMeta.Type))
							lowStr = prefixKey + "\x00" + encMin
						}
						if r.hasMax {
							encMax := storage.EncodeIndexKeyPart(parseValueToType(r.maxVal, nextMeta.Type))
							highStr = prefixKey + "\x00" + encMax + "\xff\xff\xff\xff"
						}
						lowVal := storage.StringValue(lowStr)
						highVal := storage.StringValue(highStr)
						return idx, &lowVal, &highVal, true
					}
				}
			}
			// Pure prefix match on composite index
			lowVal := storage.StringValue(prefixKey)
			highVal := storage.StringValue(prefixKey + "\xff\xff\xff\xff")
			return idx, &lowVal, &highVal, true
		}
	}

	// 2. Try single-column index
	for i := range schema.Indexes {
		candIdx := &schema.Indexes[i]
		if len(candIdx.Columns) != 1 {
			continue
		}
		colName := strings.ToLower(candIdx.Columns[0])
		colMeta, _, found := schema.FindColumn(colName)
		if !found || colMeta == nil {
			continue
		}
		// Check equality
		if valStr, ok := eqColVals[colName]; ok {
			parsed := parseValueToType(valStr, colMeta.Type)
			return candIdx, &parsed, &parsed, true
		}
		// Check range
		if r, ok := rangeCols[colName]; ok && r != nil {
			var lowVal, highVal *storage.Value
			if r.hasMin {
				v := parseValueToType(r.minVal, colMeta.Type)
				lowVal = &v
			}
			if r.hasMax {
				v := parseValueToType(r.maxVal, colMeta.Type)
				highVal = &v
			}
			return candIdx, lowVal, highVal, true
		}
	}

	// 3. Try functional expression index
	for i := range schema.Indexes {
		candIdx := &schema.Indexes[i]
		if !candIdx.IsExpression() {
			continue
		}
		targetCol := candIdx.ExprColumn
		if targetCol == "" && len(candIdx.Columns) > 0 {
			targetCol = candIdx.Columns[0]
		}
		normCandPath := strings.TrimSpace(candIdx.ExprPath)
		if !strings.HasPrefix(normCandPath, "$") {
			if strings.HasPrefix(normCandPath, "[") {
				normCandPath = "$" + normCandPath
			} else {
				normCandPath = "$." + normCandPath
			}
		}

		conds := strings.Split(whereClause, " AND ")
		for _, cond := range conds {
			cond = strings.TrimSpace(cond)
			p := proc.NewParser(proc.NewLexer(cond))
			if expr := p.ParseExpression(proc.PrecLowest); expr != nil {
				if bin, ok := expr.(*proc.BinaryExpr); ok && (bin.Op == proc.TokenAssign || bin.Op == proc.TokenEq) {
					colName, jsonPath, isJSON := proc.ExtractJSONExprInfo(bin.Left)
					if isJSON {
						if dot := strings.Index(colName, "."); dot != -1 {
							colName = colName[dot+1:]
						}
						if strings.EqualFold(colName, targetCol) && strings.EqualFold(jsonPath, normCandPath) {
							if lit, isLit := bin.Right.(*proc.LiteralExpr); isLit {
								val := lit.Val
								return candIdx, &val, &val, true
							}
							valStr := strings.Trim(strings.TrimSpace(bin.Right.String()), "'\"")
							val := storage.StringValue(valStr)
							return candIdx, &val, &val, true
						}
					}
				}
			}
		}
	}

	return nil, nil, nil, false
}

func isIndexDescendingOrder(orderByClause string, idx *storage.IndexSchema, eqColVals map[string]string) bool {
	if orderByClause == "" || idx == nil {
		return false
	}
	items := strings.Split(orderByClause, ",")
	if len(items) == 0 {
		return false
	}
	first := strings.TrimSpace(items[0])
	parts := strings.Fields(first)
	if len(parts) < 2 {
		return false
	}
	col := strings.ToLower(cleanIdentifier(parts[0]))
	dir := strings.ToUpper(parts[1])
	if dir != "DESC" {
		return false
	}
	for _, idxCol := range idx.Columns {
		idxColLower := strings.ToLower(idxCol)
		if eqColVals != nil && eqColVals[idxColLower] != "" {
			continue // fixed by equality condition
		}
		if col == idxColLower {
			return true
		}
		break
	}
	return false
}

func isIndexAscendingOrder(orderByClause string, idx *storage.IndexSchema, eqColVals map[string]string) bool {
	if orderByClause == "" || idx == nil {
		return false
	}
	items := strings.Split(orderByClause, ",")
	if len(items) == 0 {
		return false
	}
	first := strings.TrimSpace(items[0])
	parts := strings.Fields(first)
	if len(parts) == 0 {
		return false
	}
	col := strings.ToLower(cleanIdentifier(parts[0]))
	dir := "ASC"
	if len(parts) > 1 {
		dir = strings.ToUpper(parts[1])
	}
	if dir != "ASC" {
		return false
	}
	for _, idxCol := range idx.Columns {
		idxColLower := strings.ToLower(idxCol)
		if eqColVals != nil && eqColVals[idxColLower] != "" {
			continue // fixed by equality condition
		}
		if col == idxColLower {
			return true
		}
		break
	}
	return false
}

// execIndexSelect attempts to execute a simple SELECT using a composite or single-column index scan.
// It is called before the VDBE path when the WHERE clause matches a known index on the target table.
// Returns (result, true) on success, (nil, false) if no suitable index exists or the scan fails.
func (s *Coordinator) execIndexSelect(qInfo *planner.QueryInfo, tableName string, start time.Time, cs *ClientSession) (*QueryResult, bool) {
	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, false
	}
	if len(schema.Indexes) == 0 {
		return nil, false
	}

	whereClause := qInfo.WhereClause
	idx, lowVal, highVal, ok := s.findIndexAndBounds(schema, whereClause)
	if !ok {
		return nil, false
	}

	// Acquire transaction.
	var tx storage.Transaction
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, false
		}
		defer session.Close()
		t, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, false
		}
		tx = t
		defer tx.Rollback()
	}

	eqColVals, _ := parseWhereConditions(whereClause)
	descScan := isIndexDescendingOrder(qInfo.OrderByClause, idx, eqColVals)
	ascScan := isIndexAscendingOrder(qInfo.OrderByClause, idx, eqColVals)

	var cursor storage.Cursor
	if descScan {
		cursor, err = tx.IndexScanDesc(tableName, idx.Name, lowVal, highVal)
	} else {
		cursor, err = tx.IndexScan(tableName, idx.Name, lowVal, highVal)
	}
	if err != nil {
		return nil, false
	}
	defer cursor.Close()

	// Determine projection columns.
	projections := qInfo.Projections
	isSelectAll := len(projections) == 0 || (len(projections) == 1 && (projections[0] == "*" || projections[0] == ""))
	if !isSelectAll {
		for _, p := range projections {
			pTrim := strings.TrimSpace(p)
			if strings.ContainsAny(pTrim, "()->+/*") || strings.Contains(strings.ToUpper(pTrim), " AS ") {
				// Complex expression or aliased projection: delegate to Volcano engine
				return nil, false
			}
		}
	}
	var selectedCols []string
	if isSelectAll {
		for _, col := range schema.Columns {
			selectedCols = append(selectedCols, col.Name)
		}
	} else {
		for _, p := range projections {
			selectedCols = append(selectedCols, cleanIdentifier(strings.TrimSpace(p)))
		}
	}

	var rows []map[string]any
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			break
		}
		// Post-filter: apply full WHERE clause to handle non-indexed conditions.
		if !evaluateWhereCondition(rec, whereClause) {
			continue
		}
		row := make(map[string]any, len(selectedCols))
		for _, col := range selectedCols {
			row[col] = formatStorageValue(rec[col])
		}
		rows = append(rows, row)
	}

	// If ORDER BY is present and not satisfied by index scan, fall back to general execution.
	if qInfo.OrderByClause != "" && !descScan && !ascScan {
		return nil, false
	}

	// Apply LIMIT if present.
	if qInfo.LimitCount > 0 && int(qInfo.LimitCount) < len(rows) {
		rows = rows[:qInfo.LimitCount]
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      selectedCols,
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, true
}

type outputProj struct {
	alias  string
	source string // "INSERTED", "DELETED", or ""
	column string
}

func parseOutputClause(raw string) []outputProj {
	parts := splitScalarProjections(raw)
	var projs []outputProj
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		alias := ""
		expr := p
		upper := strings.ToUpper(p)
		if idx := strings.Index(upper, " AS "); idx != -1 {
			expr = strings.TrimSpace(p[:idx])
			alias = cleanIdentifier(p[idx+4:])
		}
		source := ""
		col := expr
		if dot := strings.Index(expr, "."); dot != -1 {
			prefix := strings.ToUpper(cleanIdentifier(expr[:dot]))
			if prefix == "INSERTED" || prefix == "DELETED" {
				source = prefix
				col = cleanIdentifier(expr[dot+1:])
			}
		} else {
			col = cleanIdentifier(col)
		}
		if alias == "" {
			alias = col
		}
		projs = append(projs, outputProj{
			alias:  alias,
			source: source,
			column: col,
		})
	}
	return projs
}

func evaluateOutputRow(projs []outputProj, oldRec, newRec storage.Record, schema *storage.TableSchema) (map[string]any, []string) {
	row := make(map[string]any)
	var cols []string

	for _, p := range projs {
		if p.column == "*" {
			targetRec := newRec
			if p.source == "DELETED" {
				targetRec = oldRec
			}
			if schema != nil {
				for _, c := range schema.Columns {
					cols = append(cols, c.Name)
					if targetRec != nil {
						row[c.Name] = formatStorageValue(targetRec[c.Name])
					} else {
						row[c.Name] = nil
					}
				}
			}
			continue
		}

		var val any = nil
		targetRec := newRec
		if p.source == "DELETED" {
			targetRec = oldRec
		} else if p.source == "" && newRec == nil && oldRec != nil {
			targetRec = oldRec
		}

		if targetRec != nil {
			if sv, ok := targetRec[p.column]; ok {
				val = formatStorageValue(sv)
			}
		}
		row[p.alias] = val
		cols = append(cols, p.alias)
	}
	return row, cols
}

func evalUpdateExpr(exprStr string, rec storage.Record, targetType storage.DataType) storage.Value {
	arithRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_]+)\s*([\+\-\*\/])\s*([0-9\.]+)\s*$`)
	if m := arithRe.FindStringSubmatch(exprStr); len(m) == 4 {
		colName := cleanIdentifier(m[1])
		op := m[2]
		numStr := m[3]

		if curVal, ok := rec[colName]; ok && !curVal.IsNull {
			switch targetType {
			case storage.TypeInt:
				curInt := curVal.IVal
				delta, err := strconv.ParseInt(numStr, 10, 64)
				if err == nil {
					switch op {
					case "+":
						return storage.IntValue(curInt + delta)
					case "-":
						return storage.IntValue(curInt - delta)
					case "*":
						return storage.IntValue(curInt * delta)
					case "/":
						if delta != 0 {
							return storage.IntValue(curInt / delta)
						}
					}
				}
			case storage.TypeFloat:
				curFloat := curVal.FVal
				delta, err := strconv.ParseFloat(numStr, 64)
				if err == nil {
					switch op {
					case "+":
						return storage.FloatValue(curFloat + delta)
					case "-":
						return storage.FloatValue(curFloat - delta)
					case "*":
						return storage.FloatValue(curFloat * delta)
					case "/":
						if delta != 0 {
							return storage.FloatValue(curFloat / delta)
						}
					}
				}
			}
		}
	}

	return parseLiteralValue(exprStr, targetType)
}

// -----------------------------------------------------------------------------
// PIVOT Execution Engine
// -----------------------------------------------------------------------------
