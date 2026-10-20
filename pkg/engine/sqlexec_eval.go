package engine

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

var (
	reEvalInClause    = regexp.MustCompile(`(?is)^\s*([a-zA-Z0-9_\.]+)\s+(NOT\s+)?IN\s*\((.*?)\)\s*$`)
	reEvalEqCondition = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*=\s*(.+?)\s*$`)
	reEvalArithExpr   = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_]+)\s*([\+\-\*\/])\s*([0-9\.]+)\s*$`)
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
	return sqlutil.SplitTopLevel(s, ',')
}

// ParseLiteralValue parses a literal SQL expression or default value string into a storage.Value.
func ParseLiteralValue(s string, targetType storage.DataType) storage.Value {
	return parseLiteralValue(s, targetType)
}

func parseLiteralValue(s string, targetType storage.DataType) storage.Value {
	return proc.ParseLiteralDefault(s, targetType)
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
	return planner.ParseValueToType(s, t)
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

	row := NewSQLRow(rec)

	if m := reEvalInClause.FindStringSubmatch(whereStr); len(m) == 4 {
		colName := cleanIdentifier(m[1])
		isNot := strings.TrimSpace(m[2]) != ""
		valList := m[3]
		val, ok := row.Get(colName)
		if !ok || val == nil {
			return false
		}
		var isBool bool
		var bVal bool
		if b, ok := val.(bool); ok {
			isBool = true
			bVal = b
		} else if sv, ok := val.(storage.Value); ok && sv.Type == storage.TypeBool {
			isBool = true
			bVal = sv.BVal
		}

		strVal := fmt.Sprintf("%v", val)
		found := false
		for _, rawItem := range sqlutil.SplitTopLevel(valList, ',') {
			rawItem = strings.TrimSpace(rawItem)
			var item string
			if (strings.HasPrefix(rawItem, "N'") || strings.HasPrefix(rawItem, "n'")) && strings.HasSuffix(rawItem, "'") && len(rawItem) >= 3 {
				item = strings.ReplaceAll(rawItem[2:len(rawItem)-1], "''", "'")
			} else {
				item = strings.Trim(rawItem, "'\"")
			}
			if strings.EqualFold(strVal, item) ||
				strings.EqualFold(strings.TrimRight(strVal, " "), strings.TrimRight(item, " ")) ||
				(isBool && bVal && (item == "1" || strings.EqualFold(item, "true"))) ||
				(isBool && !bVal && (item == "0" || strings.EqualFold(item, "false"))) {
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
		val, ok := row.Get(colName)
		return ok && val != nil
	}
	if strings.HasSuffix(upperWhere, " IS NULL") {
		colName := cleanIdentifier(whereStr[:len(whereStr)-len(" IS NULL")])
		val, ok := row.Get(colName)
		return !ok || val == nil
	}

	ops := []string{" NOT LIKE ", " NOT ILIKE ", "!=", "<>", "<=", ">=", "=", "<", ">", " LIKE ", " ILIKE "}
	for _, op := range ops {
		idx := strings.Index(strings.ToUpper(whereStr), op)
		if idx != -1 {
			leftCol := cleanIdentifier(whereStr[:idx])
			rawRight := strings.TrimSpace(whereStr[idx+len(op):])
			var rightVal string
			if (strings.HasPrefix(rawRight, "N'") || strings.HasPrefix(rawRight, "n'")) && strings.HasSuffix(rawRight, "'") && len(rawRight) >= 3 {
				rightVal = strings.ReplaceAll(rawRight[2:len(rawRight)-1], "''", "'")
			} else {
				rightVal = strings.Trim(rawRight, "';\"")
			}

			if leftCol == "1" && rightVal == "1" && op == "=" {
				return true
			}
			if leftCol == "1" && rightVal == "0" && op == "=" {
				return false
			}

			val, ok := row.Get(leftCol)
			if !ok {
				return false
			}

			var isBool bool
			var bVal bool
			if b, ok := val.(bool); ok {
				isBool = true
				bVal = b
			} else if sv, ok := val.(storage.Value); ok && sv.Type == storage.TypeBool {
				isBool = true
				bVal = sv.BVal
			}

			strVal := fmt.Sprintf("%v", val)
			switch strings.TrimSpace(op) {
			case "=":
				if isBool {
					if rightVal == "1" || strings.EqualFold(rightVal, "true") {
						return bVal
					}
					if rightVal == "0" || strings.EqualFold(rightVal, "false") {
						return !bVal
					}
				}
				return strings.EqualFold(strVal, rightVal) || strings.EqualFold(strings.TrimRight(strVal, " "), strings.TrimRight(rightVal, " "))
			case "!=", "<>":
				if isBool {
					if rightVal == "1" || strings.EqualFold(rightVal, "true") {
						return !bVal
					}
					if rightVal == "0" || strings.EqualFold(rightVal, "false") {
						return bVal
					}
				}
				return !strings.EqualFold(strVal, rightVal) && !strings.EqualFold(strings.TrimRight(strVal, " "), strings.TrimRight(rightVal, " "))
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
		re := getCompiledRegexp(`(?i)\b` + esc + `\b`)
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
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if m := reEvalEqCondition.FindStringSubmatch(part); len(m) == 3 {
			rawCol := m[1]
			// Strip table qualifier (tbl.col -> col)
			if dot := strings.LastIndex(rawCol, "."); dot != -1 {
				rawCol = rawCol[dot+1:]
			}
			rawVal := strings.TrimSpace(m[2])
			var val string
			if (strings.HasPrefix(rawVal, "N'") || strings.HasPrefix(rawVal, "n'")) && strings.HasSuffix(rawVal, "'") && len(rawVal) >= 3 {
				val = strings.ReplaceAll(rawVal[2:len(rawVal)-1], "''", "'")
			} else {
				val = strings.Trim(rawVal, "'\"")
			}
			result[strings.ToLower(rawCol)] = val
		}
	}
	return result
}

// splitOnAnd splits a WHERE clause on top-level AND keywords,
// respecting parentheses depth so subexpressions are not split.
func splitOnAnd(where string) []string {
	return planner.SplitOnAnd(where)
}

// buildCompositePrefixKey builds a \x00-separated composite key string from the
// prefix columns of a composite index given an equality-condition value map.
func buildCompositePrefixKey(cols []string, eqColVals map[string]string, schema *storage.TableSchema) string {
	return planner.BuildCompositePrefixKey(cols, eqColVals, schema)
}

type indexRangeCond = planner.IndexRangeCond

func parseWhereConditions(where string) (map[string]string, map[string]*planner.IndexRangeCond) {
	return planner.ParseWhereConditions(where)
}

func (s *Coordinator) findIndexAndBounds(schema *storage.TableSchema, whereClause string) (*storage.IndexSchema, *storage.Value, *storage.Value, bool) {
	return planner.FindIndexAndBounds(schema, whereClause)
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
			if sv, ok := targetRec.Get(p.column); ok {
				val = formatStorageValue(sv)
			}
		}
		row[p.alias] = val
		cols = append(cols, p.alias)
	}
	return row, cols
}

func findArithmeticOp(s string) (int, byte) {
	inQuote := false
	var quoteChar byte
	bracketDepth := 0
	parenDepth := 0

	addSubIdx := -1
	var addSubOp byte
	mulDivIdx := -1
	var mulDivOp byte

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inQuote {
			if ch == quoteChar {
				inQuote = false
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			inQuote = true
			quoteChar = ch
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '+', '-':
			if bracketDepth == 0 && parenDepth == 0 && i > 0 {
				prev := s[i-1]
				if prev != 'e' && prev != 'E' && prev != '+' && prev != '-' && prev != '*' && prev != '/' {
					addSubIdx = i
					addSubOp = ch
				}
			}
		case '*', '/':
			if bracketDepth == 0 && parenDepth == 0 && i > 0 {
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

func resolveOperand(token string, rec storage.Record) (storage.Value, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return storage.NullValue(storage.TypeVarChar), false
	}
	clean := cleanIdentifier(token)
	if val, ok := rec.Get(clean); ok {
		return val, true
	}
	if i, err := strconv.ParseInt(token, 10, 64); err == nil {
		return storage.IntValue(i), true
	}
	if f, err := strconv.ParseFloat(token, 64); err == nil {
		return storage.FloatValue(f), true
	}
	return storage.NullValue(storage.TypeVarChar), false
}

func toNumeric(val storage.Value) (float64, int64, bool) {
	if val.IsNull {
		return 0, 0, false
	}
	switch val.Type {
	case storage.TypeInt:
		return float64(val.IVal), val.IVal, true
	case storage.TypeFloat:
		return val.FVal, int64(val.FVal), true
	case storage.TypeDecimal:
		f, err := strconv.ParseFloat(val.String(), 64)
		if err == nil {
			return f, int64(f), true
		}
		return 0, 0, false
	case storage.TypeVarChar:
		s := strings.TrimSpace(val.SVal)
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return float64(i), i, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, int64(f), true
		}
		return 0, 0, false
	default:
		return 0, 0, false
	}
}

func evalUpdateExpr(exprStr string, rec storage.Record, targetType storage.DataType) (storage.Value, error) {
	exprStr = strings.TrimSpace(exprStr)
	if opIdx, op := findArithmeticOp(exprStr); opIdx != -1 {
		leftStr := strings.TrimSpace(exprStr[:opIdx])
		rightStr := strings.TrimSpace(exprStr[opIdx+1:])

		leftVal, ok1 := resolveOperand(leftStr, rec)
		rightVal, ok2 := resolveOperand(rightStr, rec)
		if ok1 && ok2 {
			if leftVal.IsNull || rightVal.IsNull {
				return storage.NullValue(targetType), nil
			}
			leftF, leftI, num1 := toNumeric(leftVal)
			rightF, rightI, num2 := toNumeric(rightVal)
			if num1 && num2 {
				if op == '/' {
					if rightF == 0 {
						return storage.NullValue(targetType), fmt.Errorf("divide by zero error encountered")
					}
				}
				switch targetType {
				case storage.TypeInt:
					var res int64
					switch op {
					case '+':
						res = leftI + rightI
					case '-':
						res = leftI - rightI
					case '*':
						res = leftI * rightI
					case '/':
						res = leftI / rightI
					}
					return storage.IntValue(res), nil
				case storage.TypeFloat:
					var res float64
					switch op {
					case '+':
						res = leftF + rightF
					case '-':
						res = leftF - rightF
					case '*':
						res = leftF * rightF
					case '/':
						res = leftF / rightF
					}
					return storage.FloatValue(res), nil
				case storage.TypeDecimal:
					var res float64
					switch op {
					case '+':
						res = leftF + rightF
					case '-':
						res = leftF - rightF
					case '*':
						res = leftF * rightF
					case '/':
						res = leftF / rightF
					}
					return storage.DecimalValue(strconv.FormatFloat(res, 'f', 2, 64)), nil
				}
			}
		}
	}

	clean := cleanIdentifier(exprStr)
	if val, ok := rec.Get(clean); ok {
		return val, nil
	}

	return parseLiteralValue(exprStr, targetType), nil
}

// -----------------------------------------------------------------------------
// PIVOT Execution Engine
// -----------------------------------------------------------------------------
