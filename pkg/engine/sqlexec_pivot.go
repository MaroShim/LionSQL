package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

func findKeywordFollowedByParen(s string, kw string) int {
	n := len(s)
	kwLen := len(kw)
	inSingle := false
	inDouble := false
	inBracket := false
	for i := 0; i <= n-kwLen; i++ {
		ch := s[i]
		if inSingle {
			if ch == '\'' {
				if i+1 < n && s[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '"' {
				inDouble = false
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
			inSingle = true
			continue
		}
		if ch == '"' {
			inDouble = true
			continue
		}
		if ch == '[' {
			inBracket = true
			continue
		}
		if (i == 0 || !isIdentChar(s[i-1])) && strings.EqualFold(s[i:i+kwLen], kw) {
			j := i + kwLen
			if j < n && isIdentChar(s[j]) {
				continue
			}
			for j < n && unicode.IsSpace(rune(s[j])) {
				j++
			}
			if j < n && s[j] == '(' {
				return i
			}
		}
	}
	return -1
}

func parsePivotInnerBody(pivotBody string) (aggFunc, aggCol, pivotCol, rawPivotVals string, ok bool) {
	s := strings.TrimSpace(pivotBody)
	openP := strings.IndexByte(s, '(')
	if openP <= 0 {
		return "", "", "", "", false
	}
	aggFunc = strings.ToUpper(strings.TrimSpace(s[:openP]))
	for i := 0; i < len(aggFunc); i++ {
		if !isIdentChar(aggFunc[i]) {
			return "", "", "", "", false
		}
	}
	closeP := sqlutil.FindMatchingParen(s, openP)
	if closeP == -1 {
		return "", "", "", "", false
	}
	aggCol = cleanIdentifier(strings.TrimSpace(s[openP+1 : closeP]))
	rem := strings.TrimSpace(s[closeP+1:])
	if !sqlutil.HasPrefixKeyword(rem, "FOR") {
		return "", "", "", "", false
	}
	rem = strings.TrimSpace(rem[3:])
	inIdx := sqlutil.FindTopLevelKeyword(rem, "IN")
	if inIdx <= 0 {
		return "", "", "", "", false
	}
	pivotCol = cleanIdentifier(strings.TrimSpace(rem[:inIdx]))
	afterIn := strings.TrimSpace(rem[inIdx+2:])
	if !strings.HasPrefix(afterIn, "(") || !strings.HasSuffix(afterIn, ")") {
		return "", "", "", "", false
	}
	rawPivotVals = strings.TrimSpace(afterIn[1 : len(afterIn)-1])
	return aggFunc, aggCol, pivotCol, rawPivotVals, true
}

func parseUnpivotInnerBody(unpivotBody string) (valCol, nameCol, rawCols string, ok bool) {
	s := strings.TrimSpace(unpivotBody)
	forIdx := sqlutil.FindTopLevelKeyword(s, "FOR")
	if forIdx <= 0 {
		return "", "", "", false
	}
	valCol = cleanIdentifier(strings.TrimSpace(s[:forIdx]))
	rem := strings.TrimSpace(s[forIdx+3:])
	inIdx := sqlutil.FindTopLevelKeyword(rem, "IN")
	if inIdx <= 0 {
		return "", "", "", false
	}
	nameCol = cleanIdentifier(strings.TrimSpace(rem[:inIdx]))
	afterIn := strings.TrimSpace(rem[inIdx+2:])
	if !strings.HasPrefix(afterIn, "(") || !strings.HasSuffix(afterIn, ")") {
		return "", "", "", false
	}
	rawCols = strings.TrimSpace(afterIn[1 : len(afterIn)-1])
	return valCol, nameCol, rawCols, true
}

func parsePivotTailClauses(afterPivot string) (alias, whereClause, orderByClause string) {
	s := strings.TrimSpace(afterPivot)
	if orderIdx := sqlutil.FindTopLevelKeyword(s, "ORDER BY"); orderIdx != -1 {
		rem := strings.TrimSpace(s[orderIdx+len("ORDER"):])
		if sqlutil.HasPrefixKeyword(rem, "BY") {
			orderByClause = strings.TrimSpace(rem[2:])
		}
		s = strings.TrimSpace(s[:orderIdx])
	}
	if whereIdx := sqlutil.FindTopLevelKeyword(s, "WHERE"); whereIdx != -1 {
		whereClause = strings.TrimSpace(s[whereIdx+len("WHERE"):])
		s = strings.TrimSpace(s[:whereIdx])
	}
	if sqlutil.HasPrefixKeyword(s, "AS") {
		s = strings.TrimSpace(s[2:])
	}
	alias = cleanIdentifier(s)
	return alias, whereClause, orderByClause
}

func hasPivot(query string) bool {
	return findKeywordFollowedByParen(query, "PIVOT") != -1
}

type pivotParsedQuery struct {
	OuterProjections string
	SourceQuery      string
	AggFunc          string
	AggCol           string
	PivotCol         string
	PivotValues      []string
	PivotAlias       string
	WhereClause      string
	OrderByClause    string
}

func parsePivotQuery(query string) (*pivotParsedQuery, error) {
	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT") {
		return nil, fmt.Errorf("PIVOT query must start with SELECT")
	}

	pivotIdx := findKeywordFollowedByParen(trimmed, "PIVOT")
	if pivotIdx == -1 {
		return nil, fmt.Errorf("no PIVOT clause found")
	}

	// Find top-level FROM keyword between SELECT and pivotIdx
	depth := 0
	fromIdx := -1
	fromStart := -1

	for i := 0; i < pivotIdx; i++ {
		if trimmed[i] == '(' {
			depth++
		} else if trimmed[i] == ')' {
			if depth > 0 {
				depth--
			}
		} else if depth == 0 && i+4 <= pivotIdx && strings.EqualFold(trimmed[i:i+4], "FROM") {
			beforeOK := i == 0 || unicode.IsSpace(rune(trimmed[i-1]))
			afterOK := i+4 == pivotIdx || unicode.IsSpace(rune(trimmed[i+4])) || trimmed[i+4] == '('
			if beforeOK && afterOK {
				fromStart = i
				fromIdx = i + 4
				break
			}
		}
	}
	if fromIdx == -1 {
		return nil, fmt.Errorf("invalid PIVOT syntax: missing FROM clause")
	}

	selectEnd := 6 // len("SELECT")
	for selectEnd < fromStart && unicode.IsSpace(rune(trimmed[selectEnd])) {
		selectEnd++
	}
	outerProjs := strings.TrimSpace(trimmed[selectEnd:fromStart])
	sourcePart := strings.TrimSpace(trimmed[fromIdx:pivotIdx])

	// Source can be a subquery `(SELECT ...) [AS] alias` or a table name `tbl [AS] alias`
	var sourceSQL string
	if strings.HasPrefix(sourcePart, "(") {
		depth = 0
		closeParen := -1
		for i, ch := range sourcePart {
			if ch == '(' {
				depth++
			} else if ch == ')' {
				depth--
				if depth == 0 {
					closeParen = i
					break
				}
			}
		}
		if closeParen != -1 {
			sourceSQL = strings.TrimSpace(sourcePart[1:closeParen])
		} else {
			return nil, fmt.Errorf("invalid PIVOT syntax: unmatched parenthesis in source query")
		}
	} else {
		fields := strings.Fields(sourcePart)
		if len(fields) == 0 {
			return nil, fmt.Errorf("invalid PIVOT syntax: empty source table")
		}
		sourceSQL = "SELECT * FROM " + fields[0]
	}

	// Find matching closing parenthesis for PIVOT (...)
	openParen := strings.Index(trimmed[pivotIdx:], "(")
	if openParen == -1 {
		return nil, fmt.Errorf("malformed PIVOT syntax: missing opening parenthesis")
	}
	openParen += pivotIdx

	closeParen := sqlutil.FindMatchingParen(trimmed, openParen)
	if closeParen == -1 {
		return nil, fmt.Errorf("malformed PIVOT syntax: missing closing parenthesis")
	}

	pivotBody := strings.TrimSpace(trimmed[openParen+1 : closeParen])
	afterPivot := strings.TrimSpace(trimmed[closeParen+1:])

	// Parse pivot body: <agg_func>(<agg_col>) FOR <pivot_col> IN (<pivot_vals>)
	aggFunc, aggCol, pivotCol, rawPivotVals, ok := parsePivotInnerBody(pivotBody)
	if !ok {
		return nil, fmt.Errorf("malformed PIVOT clause: %s", pivotBody)
	}

	var pivotValues []string
	for _, val := range strings.Split(rawPivotVals, ",") {
		cleanVal := cleanIdentifier(strings.Trim(strings.TrimSpace(val), "'\""))
		if cleanVal != "" {
			pivotValues = append(pivotValues, cleanVal)
		}
	}

	// Parse afterPivot: [AS] <alias> [WHERE <where>] [ORDER BY <order>]
	pivotAlias, whereClause, orderByClause := parsePivotTailClauses(afterPivot)

	return &pivotParsedQuery{
		OuterProjections: outerProjs,
		SourceQuery:      sourceSQL,
		AggFunc:          aggFunc,
		AggCol:           aggCol,
		PivotCol:         pivotCol,
		PivotValues:      pivotValues,
		PivotAlias:       pivotAlias,
		WhereClause:      whereClause,
		OrderByClause:    orderByClause,
	}, nil
}

func (s *Coordinator) execPivotSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	pvt, err := parsePivotQuery(query)
	if err != nil {
		return nil, err
	}

	// 1. Execute source query
	srcRes, err := s.execSelect(pvt.SourceQuery, start, cs)
	if err != nil {
		return nil, fmt.Errorf("pivot source query error: %w", err)
	}

	// 2. Identify grouping columns
	aggColClean := cleanIdentifier(pvt.AggCol)
	pivotColClean := cleanIdentifier(pvt.PivotCol)

	var actualAggCol, actualPivotCol string
	var groupCols []string

	for _, col := range srcRes.Columns {
		colClean := cleanIdentifier(col)
		if strings.EqualFold(colClean, aggColClean) {
			actualAggCol = col
		} else if strings.EqualFold(colClean, pivotColClean) {
			actualPivotCol = col
		} else {
			groupCols = append(groupCols, col)
		}
	}
	if actualPivotCol == "" {
		for _, col := range srcRes.Columns {
			if strings.EqualFold(cleanIdentifier(col), pivotColClean) {
				actualPivotCol = col
				break
			}
		}
	}
	if actualAggCol == "" && pvt.AggCol != "*" {
		for _, col := range srcRes.Columns {
			if strings.EqualFold(cleanIdentifier(col), aggColClean) {
				actualAggCol = col
				break
			}
		}
	}

	// 3. Accumulate data per group
	type pivotAccumulator struct {
		groupVals   map[string]any
		valuesByCol map[string][]any
	}
	var groupOrder []string
	groups := make(map[string]*pivotAccumulator)

	for _, row := range srcRes.Rows {
		var keyParts []string
		groupVals := make(map[string]any)
		for _, gCol := range groupCols {
			v := row[gCol]
			keyParts = append(keyParts, fmt.Sprintf("%v", v))
			groupVals[gCol] = v
		}
		gKey := strings.Join(keyParts, "\x00")
		acc, exists := groups[gKey]
		if !exists {
			acc = &pivotAccumulator{
				groupVals:   groupVals,
				valuesByCol: make(map[string][]any),
			}
			groups[gKey] = acc
			groupOrder = append(groupOrder, gKey)
		}

		rowPVal := cleanIdentifier(fmt.Sprintf("%v", row[actualPivotCol]))
		for _, targetVal := range pvt.PivotValues {
			if strings.EqualFold(cleanIdentifier(targetVal), rowPVal) {
				var aggVal any = nil
				if actualAggCol != "" && pvt.AggCol != "*" {
					aggVal = row[actualAggCol]
				} else {
					aggVal = 1
				}
				acc.valuesByCol[targetVal] = append(acc.valuesByCol[targetVal], aggVal)
				break
			}
		}
	}

	// 4. Compute aggregates for each group
	var computedRows []map[string]any
	for _, gKey := range groupOrder {
		acc := groups[gKey]
		rowMap := make(map[string]any)
		for k, v := range acc.groupVals {
			rowMap[k] = v
			rowMap[strings.ToLower(k)] = v
		}

		for _, targetVal := range pvt.PivotValues {
			vals := acc.valuesByCol[targetVal]
			var aggResult any = nil
			if strings.EqualFold(pvt.AggFunc, "COUNT") {
				count := 0
				for _, v := range vals {
					if v != nil {
						count++
					}
				}
				aggResult = count
			} else if len(vals) > 0 {
				switch strings.ToUpper(pvt.AggFunc) {
				case "SUM":
					var sum float64
					hasVal := false
					for _, v := range vals {
						if v != nil {
							sum += anyToFloat(v)
							hasVal = true
						}
					}
					if hasVal {
						if sum == float64(int64(sum)) {
							aggResult = int64(sum)
						} else {
							aggResult = sum
						}
					}
				case "AVG":
					var sum float64
					count := 0
					for _, v := range vals {
						if v != nil {
							sum += anyToFloat(v)
							count++
						}
					}
					if count > 0 {
						aggResult = sum / float64(count)
					}
				case "MIN":
					var minVal any = nil
					for _, v := range vals {
						if v != nil {
							if minVal == nil || compareAny(v, minVal) < 0 {
								minVal = v
							}
						}
					}
					aggResult = minVal
				case "MAX":
					var maxVal any = nil
					for _, v := range vals {
						if v != nil {
							if maxVal == nil || compareAny(v, maxVal) > 0 {
								maxVal = v
							}
						}
					}
					aggResult = maxVal
				}
			}
			rowMap[targetVal] = aggResult
			rowMap["["+targetVal+"]"] = aggResult
			rowMap[strings.ToLower(targetVal)] = aggResult
			rowMap["["+strings.ToLower(targetVal)+"]"] = aggResult
		}
		computedRows = append(computedRows, rowMap)
	}

	// 5. Apply outer WHERE filter if present
	if pvt.WhereClause != "" {
		var filteredRows []map[string]any
		for _, row := range computedRows {
			if s.evalSimpleRowWhere(row, pvt.WhereClause) {
				filteredRows = append(filteredRows, row)
			}
		}
		computedRows = filteredRows
	}

	// 6. Project columns
	var finalCols []string
	type projColDef struct {
		expr  string
		alias string
	}
	var projDefs []projColDef

	if pvt.OuterProjections == "*" || pvt.OuterProjections == "" {
		for _, gCol := range groupCols {
			finalCols = append(finalCols, gCol)
			projDefs = append(projDefs, projColDef{expr: gCol, alias: gCol})
		}
		for _, pVal := range pvt.PivotValues {
			finalCols = append(finalCols, pVal)
			projDefs = append(projDefs, projColDef{expr: pVal, alias: pVal})
		}
	} else {
		for _, item := range planner.SplitProjections(pvt.OuterProjections) {
			itemTrimmed := strings.TrimSpace(item)
			if itemTrimmed == "" {
				continue
			}
			asIdx := strings.Index(strings.ToUpper(itemTrimmed), " AS ")
			var expr, alias string
			if asIdx != -1 {
				expr = strings.TrimSpace(itemTrimmed[:asIdx])
				alias = cleanIdentifier(strings.TrimSpace(itemTrimmed[asIdx+4:]))
			} else {
				expr = itemTrimmed
				alias = cleanIdentifier(itemTrimmed)
			}
			finalCols = append(finalCols, alias)
			projDefs = append(projDefs, projColDef{expr: expr, alias: alias})
		}
	}

	var projectedRows []map[string]any
	for _, row := range computedRows {
		outRow := make(map[string]any, len(finalCols))
		for _, pDef := range projDefs {
			val := s.evalPivotExpr(row, pDef.expr)
			outRow[pDef.alias] = val
		}
		projectedRows = append(projectedRows, outRow)
	}

	// 7. Apply ORDER BY if present
	if pvt.OrderByClause != "" && len(projectedRows) > 0 {
		orderItems := sqlutil.ParseOrderBy(pvt.OrderByClause)
		if len(orderItems) > 0 {
			sort.Slice(projectedRows, func(i, j int) bool {
				for _, item := range orderItems {
					orderCol := cleanIdentifier(item.Column)
					v1 := projectedRows[i][orderCol]
					v2 := projectedRows[j][orderCol]
					if v1 == nil && v2 == nil {
						continue
					}
					cmp := sqlutil.CompareAny(v1, v2)
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

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      finalCols,
		Rows:         projectedRows,
		RowsAffected: int64(len(projectedRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) evalSimpleRowWhere(row map[string]any, where string) bool {
	if where == "" {
		return true
	}
	parts := splitOnAnd(where)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		p := proc.NewParser(proc.NewLexer(part))
		expr := p.ParseExpr()
		bin, ok := expr.(*proc.BinaryExpr)
		if !ok || bin == nil || bin.Left == nil || bin.Right == nil {
			continue
		}
		var op string
		switch bin.Op {
		case proc.TokenEq, proc.TokenAssign:
			op = "="
		case proc.TokenNeq:
			op = "!="
		case proc.TokenGt:
			op = ">"
		case proc.TokenGte:
			op = ">="
		case proc.TokenLt:
			op = "<"
		case proc.TokenLte:
			op = "<="
		default:
			continue
		}
		rawCol := cleanIdentifier(bin.Left.String())
		targetStr := strings.Trim(strings.TrimSpace(bin.Right.String()), "'\"")

		val, exists := row[rawCol]
		if !exists {
			val = row[strings.ToLower(rawCol)]
		}
		if val == nil {
			return false
		}
		valStr := fmt.Sprintf("%v", val)
		f1, err1 := strconv.ParseFloat(valStr, 64)
		f2, err2 := strconv.ParseFloat(targetStr, 64)
		if err1 == nil && err2 == nil {
			switch op {
			case "=", "==":
				if f1 != f2 {
					return false
				}
			case "!=", "<>":
				if f1 == f2 {
					return false
				}
			case ">":
				if !(f1 > f2) {
					return false
				}
			case ">=":
				if !(f1 >= f2) {
					return false
				}
			case "<":
				if !(f1 < f2) {
					return false
				}
			case "<=":
				if !(f1 <= f2) {
					return false
				}
			}
		} else {
			switch op {
			case "=", "==":
				if !strings.EqualFold(valStr, targetStr) {
					return false
				}
			case "!=", "<>":
				if strings.EqualFold(valStr, targetStr) {
					return false
				}
			case ">":
				if !(valStr > targetStr) {
					return false
				}
			case ">=":
				if !(valStr >= targetStr) {
					return false
				}
			case "<":
				if !(valStr < targetStr) {
					return false
				}
			case "<=":
				if !(valStr <= targetStr) {
					return false
				}
			}
		}
	}
	return true
}

func (s *Coordinator) evalPivotExpr(row map[string]any, expr string) any {
	exprTrim := strings.TrimSpace(expr)
	clean := cleanIdentifier(exprTrim)
	if v, ok := row[clean]; ok {
		return v
	}
	if v, ok := row[exprTrim]; ok {
		return v
	}
	if v, ok := row[strings.ToLower(clean)]; ok {
		return v
	}
	for _, op := range []string{"+", "-", "*", "/"} {
		if strings.Contains(exprTrim, op) {
			idx := strings.Index(exprTrim, op)
			left := strings.TrimSpace(exprTrim[:idx])
			right := strings.TrimSpace(exprTrim[idx+1:])
			vLeft := s.evalPivotExpr(row, left)
			vRight := s.evalPivotExpr(row, right)
			if vLeft != nil && vRight != nil {
				f1 := anyToFloat(vLeft)
				f2 := anyToFloat(vRight)
				switch op {
				case "+":
					res := f1 + f2
					if res == float64(int64(res)) {
						return int64(res)
					}
					return res
				case "-":
					res := f1 - f2
					if res == float64(int64(res)) {
						return int64(res)
					}
					return res
				case "*":
					res := f1 * f2
					if res == float64(int64(res)) {
						return int64(res)
					}
					return res
				case "/":
					if f2 != 0 {
						return f1 / f2
					}
				}
			}
		}
	}
	return nil
}

func anyToFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case int32:
		return float64(val)
	case float64:
		return val
	case float32:
		return float64(val)
	case string:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	case storage.Value:
		if val.Type == storage.TypeInt {
			return float64(val.IVal)
		}
		if val.Type == storage.TypeFloat {
			return val.FVal
		}
		if val.Type == storage.TypeDecimal {
			return val.DVal.Float64()
		}
	}
	return 0
}

func compareAny(a, b any) int {
	f1 := anyToFloat(a)
	f2 := anyToFloat(b)
	if f1 < f2 {
		return -1
	} else if f1 > f2 {
		return 1
	}
	s1 := fmt.Sprintf("%v", a)
	s2 := fmt.Sprintf("%v", b)
	if s1 < s2 {
		return -1
	} else if s1 > s2 {
		return 1
	}
	return 0
}

// -----------------------------------------------------------------------------
// UNPIVOT Operator Engine
// -----------------------------------------------------------------------------

func hasUnpivot(query string) bool {
	return findKeywordFollowedByParen(query, "UNPIVOT") != -1
}

type unpivotParsedQuery struct {
	OuterProjections string
	SourceQuery      string
	ValueCol         string
	NameCol          string
	UnpivotColumns   []string
	UnpivotAlias     string
	WhereClause      string
	OrderByClause    string
}

func parseUnpivotQuery(query string) (*unpivotParsedQuery, error) {
	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT") {
		return nil, fmt.Errorf("UNPIVOT query must start with SELECT")
	}

	unpivotIdx := findKeywordFollowedByParen(trimmed, "UNPIVOT")
	if unpivotIdx == -1 {
		return nil, fmt.Errorf("no UNPIVOT clause found")
	}

	// Find top-level FROM keyword between SELECT and unpivotIdx
	depth := 0
	fromIdx := -1
	fromStart := -1

	for i := 0; i < unpivotIdx; i++ {
		if trimmed[i] == '(' {
			depth++
		} else if trimmed[i] == ')' {
			if depth > 0 {
				depth--
			}
		} else if depth == 0 && i+4 <= unpivotIdx && strings.EqualFold(trimmed[i:i+4], "FROM") {
			beforeOK := i == 0 || unicode.IsSpace(rune(trimmed[i-1]))
			afterOK := i+4 == unpivotIdx || unicode.IsSpace(rune(trimmed[i+4])) || trimmed[i+4] == '('
			if beforeOK && afterOK {
				fromStart = i
				fromIdx = i + 4
				break
			}
		}
	}
	if fromIdx == -1 {
		return nil, fmt.Errorf("invalid UNPIVOT syntax: missing FROM clause")
	}

	selectEnd := 6 // len("SELECT")
	for selectEnd < fromStart && unicode.IsSpace(rune(trimmed[selectEnd])) {
		selectEnd++
	}
	outerProjs := strings.TrimSpace(trimmed[selectEnd:fromStart])
	sourcePart := strings.TrimSpace(trimmed[fromIdx:unpivotIdx])

	var sourceSQL string
	if strings.HasPrefix(sourcePart, "(") {
		depth = 0
		closeParen := -1
		for i, ch := range sourcePart {
			if ch == '(' {
				depth++
			} else if ch == ')' {
				depth--
				if depth == 0 {
					closeParen = i
					break
				}
			}
		}
		if closeParen != -1 {
			sourceSQL = strings.TrimSpace(sourcePart[1:closeParen])
		} else {
			return nil, fmt.Errorf("invalid UNPIVOT syntax: unmatched parenthesis in source query")
		}
	} else {
		fields := strings.Fields(sourcePart)
		if len(fields) == 0 {
			return nil, fmt.Errorf("invalid UNPIVOT syntax: empty source table")
		}
		sourceSQL = "SELECT * FROM " + fields[0]
	}

	openParen := strings.Index(trimmed[unpivotIdx:], "(")
	if openParen == -1 {
		return nil, fmt.Errorf("malformed UNPIVOT syntax: missing opening parenthesis")
	}
	openParen += unpivotIdx

	closeParen := sqlutil.FindMatchingParen(trimmed, openParen)
	if closeParen == -1 {
		return nil, fmt.Errorf("malformed UNPIVOT syntax: missing closing parenthesis")
	}

	unpivotBody := strings.TrimSpace(trimmed[openParen+1 : closeParen])
	afterUnpivot := strings.TrimSpace(trimmed[closeParen+1:])

	// Parse unpivot body: <val_col> FOR <name_col> IN (<cols>)
	valCol, nameCol, rawCols, ok := parseUnpivotInnerBody(unpivotBody)
	if !ok {
		return nil, fmt.Errorf("malformed UNPIVOT clause: %s", unpivotBody)
	}

	var unpivotCols []string
	for _, col := range strings.Split(rawCols, ",") {
		cleanCol := cleanIdentifier(strings.Trim(strings.TrimSpace(col), "'\""))
		if cleanCol != "" {
			unpivotCols = append(unpivotCols, cleanCol)
		}
	}

	unpivotAlias, whereClause, orderByClause := parsePivotTailClauses(afterUnpivot)

	return &unpivotParsedQuery{
		OuterProjections: outerProjs,
		SourceQuery:      sourceSQL,
		ValueCol:         valCol,
		NameCol:          nameCol,
		UnpivotColumns:   unpivotCols,
		UnpivotAlias:     unpivotAlias,
		WhereClause:      whereClause,
		OrderByClause:    orderByClause,
	}, nil
}

func (s *Coordinator) execUnpivotSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	unpvt, err := parseUnpivotQuery(query)
	if err != nil {
		return nil, err
	}

	// 1. Execute source query
	srcRes, err := s.execSelect(unpvt.SourceQuery, start, cs)
	if err != nil {
		return nil, fmt.Errorf("unpivot source query error: %w", err)
	}

	// 2. Identify unpivot columns and remaining preserved columns
	unpvtColsSet := make(map[string]bool)
	for _, col := range unpvt.UnpivotColumns {
		unpvtColsSet[strings.ToLower(col)] = true
	}

	var preservedCols []string
	for _, col := range srcRes.Columns {
		if !unpvtColsSet[strings.ToLower(cleanIdentifier(col))] {
			preservedCols = append(preservedCols, col)
		}
	}

	// Helper to find column in row
	findValInRow := func(row map[string]any, colName string) (any, bool) {
		clean := cleanIdentifier(colName)
		if v, ok := row[clean]; ok {
			return v, true
		}
		if v, ok := row[colName]; ok {
			return v, true
		}
		if v, ok := row[strings.ToLower(clean)]; ok {
			return v, true
		}
		for k, v := range row {
			if strings.EqualFold(cleanIdentifier(k), clean) {
				return v, true
			}
		}
		return nil, false
	}

	// 3. Unpivot rows (transform columns to rows, ignoring NULL values per T-SQL spec)
	var generatedRows []map[string]any
	for _, row := range srcRes.Rows {
		for _, colName := range unpvt.UnpivotColumns {
			val, found := findValInRow(row, colName)
			if !found || val == nil {
				continue
			}

			newRow := make(map[string]any)
			for _, pCol := range preservedCols {
				if v, ok := row[pCol]; ok {
					newRow[pCol] = v
					newRow[cleanIdentifier(pCol)] = v
					newRow[strings.ToLower(cleanIdentifier(pCol))] = v
				}
			}

			// Assign NameCol and ValueCol
			newRow[unpvt.NameCol] = colName
			newRow[strings.ToLower(unpvt.NameCol)] = colName
			newRow["["+unpvt.NameCol+"]"] = colName

			newRow[unpvt.ValueCol] = val
			newRow[strings.ToLower(unpvt.ValueCol)] = val
			newRow["["+unpvt.ValueCol+"]"] = val

			generatedRows = append(generatedRows, newRow)
		}
	}

	// 4. Apply outer WHERE filter if present
	if unpvt.WhereClause != "" {
		var filteredRows []map[string]any
		for _, row := range generatedRows {
			if s.evalSimpleRowWhere(row, unpvt.WhereClause) {
				filteredRows = append(filteredRows, row)
			}
		}
		generatedRows = filteredRows
	}

	// 5. Project columns
	var finalCols []string
	type projColDef struct {
		expr  string
		alias string
	}
	var projDefs []projColDef

	if unpvt.OuterProjections == "*" || unpvt.OuterProjections == "" {
		for _, pCol := range preservedCols {
			finalCols = append(finalCols, pCol)
			projDefs = append(projDefs, projColDef{expr: pCol, alias: pCol})
		}
		finalCols = append(finalCols, unpvt.NameCol)
		projDefs = append(projDefs, projColDef{expr: unpvt.NameCol, alias: unpvt.NameCol})
		finalCols = append(finalCols, unpvt.ValueCol)
		projDefs = append(projDefs, projColDef{expr: unpvt.ValueCol, alias: unpvt.ValueCol})
	} else {
		for _, item := range planner.SplitProjections(unpvt.OuterProjections) {
			itemTrimmed := strings.TrimSpace(item)
			if itemTrimmed == "" {
				continue
			}
			asIdx := strings.Index(strings.ToUpper(itemTrimmed), " AS ")
			var expr, alias string
			if asIdx != -1 {
				expr = strings.TrimSpace(itemTrimmed[:asIdx])
				alias = cleanIdentifier(strings.TrimSpace(itemTrimmed[asIdx+4:]))
			} else {
				expr = itemTrimmed
				alias = cleanIdentifier(itemTrimmed)
			}
			finalCols = append(finalCols, alias)
			projDefs = append(projDefs, projColDef{expr: expr, alias: alias})
		}
	}

	var projectedRows []map[string]any
	for _, row := range generatedRows {
		outRow := make(map[string]any, len(finalCols))
		for _, pDef := range projDefs {
			val := s.evalPivotExpr(row, pDef.expr)
			outRow[pDef.alias] = val
		}
		projectedRows = append(projectedRows, outRow)
	}

	// 6. Apply ORDER BY if present
	if unpvt.OrderByClause != "" && len(projectedRows) > 0 {
		orderItems := sqlutil.ParseOrderBy(unpvt.OrderByClause)
		if len(orderItems) > 0 {
			sort.Slice(projectedRows, func(i, j int) bool {
				for _, item := range orderItems {
					orderCol := cleanIdentifier(item.Column)
					v1 := projectedRows[i][orderCol]
					v2 := projectedRows[j][orderCol]
					if v1 == nil && v2 == nil {
						continue
					}
					cmp := sqlutil.CompareAny(v1, v2)
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

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      finalCols,
		Rows:         projectedRows,
		RowsAffected: int64(len(projectedRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// -----------------------------------------------------------------------------
// T-SQL FOR JSON Clause Engine (PATH, AUTO, WITHOUT_ARRAY_WRAPPER, ROOT, INCLUDE_NULL_VALUES)
// -----------------------------------------------------------------------------

func findForJSONClause(query string) (idx int, mode string, rawOptions string, ok bool) {
	idxPath := sqlutil.FindTopLevelKeyword(query, "FOR JSON PATH")
	idxAuto := sqlutil.FindTopLevelKeyword(query, "FOR JSON AUTO")
	idx = -1
	if idxPath != -1 && (idxAuto == -1 || idxPath < idxAuto) {
		idx = idxPath
		mode = "PATH"
	} else if idxAuto != -1 {
		idx = idxAuto
		mode = "AUTO"
	} else {
		return -1, "", "", false
	}
	rem := strings.TrimSpace(query[idx:])
	// Advance past FOR, JSON, and PATH/AUTO
	if sqlutil.HasPrefixKeyword(rem, "FOR") {
		rem = strings.TrimSpace(rem[3:])
	}
	if sqlutil.HasPrefixKeyword(rem, "JSON") {
		rem = strings.TrimSpace(rem[4:])
	}
	if sqlutil.HasPrefixKeyword(rem, mode) {
		rem = strings.TrimSpace(rem[len(mode):])
	}
	return idx, mode, rem, true
}

func hasForJSON(query string) bool {
	_, _, _, ok := findForJSONClause(query)
	return ok
}

func hasForXML(query string) bool {
	return sqlutil.FindTopLevelKeyword(query, "FOR XML") != -1
}

type forJSONOptions struct {
	BaseQuery           string
	Mode                string // "PATH" or "AUTO"
	WithoutArrayWrapper bool
	RootName            string
	IncludeNullValues   bool
}

func parseForJSONQuery(query string) (*forJSONOptions, error) {
	idx, mode, rawOptions, ok := findForJSONClause(query)
	if !ok {
		return nil, fmt.Errorf("no FOR JSON clause found")
	}

	baseQuery := strings.TrimSpace(query[:idx])

	opts := &forJSONOptions{
		BaseQuery: baseQuery,
		Mode:      mode,
	}

	if rawOptions != "" {
		// Split options by comma
		optParts := strings.Split(rawOptions, ",")
		for _, part := range optParts {
			p := strings.TrimSpace(part)
			pUpper := strings.ToUpper(p)
			if pUpper == "WITHOUT_ARRAY_WRAPPER" {
				opts.WithoutArrayWrapper = true
			} else if pUpper == "INCLUDE_NULL_VALUES" {
				opts.IncludeNullValues = true
			} else if strings.HasPrefix(pUpper, "ROOT") {
				// ROOT or ROOT('name')
				afterRoot := strings.TrimSpace(p[4:])
				if strings.HasPrefix(afterRoot, "(") && strings.HasSuffix(afterRoot, ")") {
					inner := strings.Trim(strings.TrimSpace(afterRoot[1:len(afterRoot)-1]), "'\"")
					if inner != "" {
						opts.RootName = inner
					} else {
						opts.RootName = "root"
					}
				} else {
					opts.RootName = "root"
				}
			}
		}
	}

	return opts, nil
}

func extractTableAliases(query string) map[string]bool {
	aliases := make(map[string]bool)
	reservedWords := map[string]bool{
		"where": true, "on": true, "join": true, "inner": true, "left": true, "right": true,
		"cross": true, "outer": true, "full": true, "group": true, "order": true, "having": true,
		"for": true, "limit": true, "offset": true, "as": true, "apply": true,
	}
	lex := proc.NewLexer(query)
	var toks []proc.Token
	for {
		t := lex.NextToken()
		if t.Type == proc.TokenEOF {
			break
		}
		toks = append(toks, t)
	}
	readQualIdent := func(pos int) (string, int) {
		if pos >= len(toks) || toks[pos].Type != proc.TokenIdent {
			return "", pos
		}
		var sb strings.Builder
		sb.WriteString(toks[pos].Literal)
		pos++
		for pos+1 < len(toks) && toks[pos].Literal == "." && toks[pos+1].Type == proc.TokenIdent {
			sb.WriteByte('.')
			sb.WriteString(toks[pos+1].Literal)
			pos += 2
		}
		return sb.String(), pos
	}
	for i := 0; i < len(toks); i++ {
		if strings.EqualFold(toks[i].Literal, "FROM") || strings.EqualFold(toks[i].Literal, "JOIN") {
			tblRaw, nextIdx := readQualIdent(i + 1)
			if tblRaw != "" {
				aliases[strings.ToLower(cleanIdentifier(tblRaw))] = true
				if nextIdx < len(toks) {
					if toks[nextIdx].Type == proc.TokenAs && nextIdx+1 < len(toks) && toks[nextIdx+1].Type == proc.TokenIdent {
						aliasCand := strings.ToLower(cleanIdentifier(toks[nextIdx+1].Literal))
						if !reservedWords[aliasCand] {
							aliases[aliasCand] = true
						}
					} else if toks[nextIdx].Type == proc.TokenIdent {
						aliasCand := strings.ToLower(cleanIdentifier(toks[nextIdx].Literal))
						if !reservedWords[aliasCand] {
							aliases[aliasCand] = true
						}
					}
				}
			}
		}
	}
	return aliases
}

func (s *Coordinator) execForJSONSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	opts, err := parseForJSONQuery(query)
	if err != nil {
		return nil, err
	}

	tableAliases := extractTableAliases(opts.BaseQuery)

	// 1. Execute the underlying base SELECT query
	baseRes, err := s.execSelect(opts.BaseQuery, start, cs)
	if err != nil {
		return nil, err
	}

	// 2. Build JSON structures from rows
	jsonObjects := make([]any, 0, len(baseRes.Rows))

	for _, row := range baseRes.Rows {
		obj := make(map[string]any)
		for _, col := range baseRes.Columns {
			val := row[col]
			if val == nil && !opts.IncludeNullValues {
				continue
			}

			cleanCol := cleanIdentifier(col)
			if strings.Contains(cleanCol, ".") {
				dotParts := strings.Split(cleanCol, ".")
				// If first segment matches a table name/alias in the query, strip table qualifier
				if len(dotParts) == 2 && tableAliases[strings.ToLower(dotParts[0])] {
					cleanCol = dotParts[1]
				}
			}

			// Support nested dot notation: e.g. "department.name" -> {"department": {"name": ...}}
			if strings.Contains(cleanCol, ".") {
				dotParts := strings.Split(cleanCol, ".")
				cur := obj
				for i := 0; i < len(dotParts)-1; i++ {
					subKey := dotParts[i]
					subMap, ok := cur[subKey].(map[string]any)
					if !ok {
						subMap = make(map[string]any)
						cur[subKey] = subMap
					}
					cur = subMap
				}
				cur[dotParts[len(dotParts)-1]] = val
			} else {
				obj[cleanCol] = val
			}
		}
		jsonObjects = append(jsonObjects, obj)
	}

	// 3. Apply ROOT and WITHOUT_ARRAY_WRAPPER options
	var finalJSON any
	if opts.WithoutArrayWrapper {
		if len(jsonObjects) == 1 {
			finalJSON = jsonObjects[0]
		} else if len(jsonObjects) == 0 {
			finalJSON = nil
		} else {
			// In T-SQL, WITHOUT_ARRAY_WRAPPER concatenates raw JSON objects: {"a":1},{"b":2}
			var sb strings.Builder
			for i, obj := range jsonObjects {
				b, _ := json.Marshal(obj)
				if i > 0 {
					sb.WriteString(",")
				}
				sb.Write(b)
			}
			return &QueryResult{
				Success:      true,
				Command:      "SELECT",
				Columns:      []string{"JSON_F52E2B61-18A1-11d1-B105-00805F49916B"},
				Rows:         []map[string]any{{"JSON_F52E2B61-18A1-11d1-B105-00805F49916B": sb.String()}},
				RowsAffected: 1,
				DurationUS:   time.Since(start).Microseconds(),
			}, nil
		}
	} else {
		finalJSON = jsonObjects
	}

	if opts.RootName != "" {
		rootMap := make(map[string]any)
		rootMap[opts.RootName] = finalJSON
		finalJSON = rootMap
	}

	var jsonBytes []byte
	if finalJSON != nil {
		jsonBytes, err = json.Marshal(finalJSON)
		if err != nil {
			return nil, fmt.Errorf("failed to format JSON: %w", err)
		}
	} else {
		jsonBytes = []byte("null")
	}

	jsonString := string(jsonBytes)
	colName := "JSON_F52E2B61-18A1-11d1-B105-00805F49916B" // Standard Microsoft SQL Server FOR JSON column name

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      []string{colName},
		Rows:         []map[string]any{{colName: jsonString}},
		RowsAffected: 1,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}
