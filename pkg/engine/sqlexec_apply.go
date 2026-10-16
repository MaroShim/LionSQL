package engine

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"sqlion/pkg/proc"
)

var (
	reApplyMatch   = regexp.MustCompile(`(?i)\b(CROSS|OUTER)\s+APPLY\b`)
	rePatternCache sync.Map // map[string]*regexp.Regexp
)

func getCompiledRegexp(pat string) *regexp.Regexp {
	if val, ok := rePatternCache.Load(pat); ok {
		return val.(*regexp.Regexp)
	}
	re := regexp.MustCompile(pat)
	rePatternCache.Store(pat, re)
	return re
}

// hasApply reports whether the query contains a CROSS APPLY or OUTER APPLY join.
func hasApply(query string) bool {
	upper := strings.ToUpper(query)
	if !strings.Contains(upper, "APPLY") {
		return false
	}
	return reApplyMatch.MatchString(query)
}

// execApplySelect handles SELECT queries containing CROSS APPLY or OUTER APPLY clauses.
func (s *Coordinator) execApplySelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// Pre-strip table hints like WITH (NOLOCK) and normalize
	cleaned = normalizeMSSQLPagination(cleaned)

	parsedStmt, err := proc.ParseSQL(cleaned)
	if err != nil {
		return nil, fmt.Errorf("failed to parse APPLY query: %w", err)
	}

	selStmt, ok := parsedStmt.(*proc.GeneralSelectStmt)
	if !ok {
		return nil, fmt.Errorf("expected SELECT statement, got %T", parsedStmt)
	}

	if len(selStmt.Joins) == 0 {
		return nil, fmt.Errorf("no joins found in APPLY query")
	}

	// 1. Build and execute query for the left source
	leftTableRef := selStmt.From
	leftQuery := fmt.Sprintf("SELECT * FROM %s", leftTableRef.String())
	if leftTableRef.Subquery != nil {
		leftQuery = leftTableRef.Subquery.String()
	}

	leftRes, err := s.execSelect(leftQuery, start, cs)
	if err != nil {
		return nil, fmt.Errorf("failed to execute left source in APPLY: %w", err)
	}
	if !leftRes.Success {
		return leftRes, nil
	}

	currentRows := leftRes.Rows
	leftAlias := leftTableRef.Alias
	if leftAlias == "" {
		leftAlias = leftTableRef.Name
	}

	// 2. Process each APPLY join iteratively
	for _, join := range selStmt.Joins {
		isOuter := strings.HasPrefix(strings.ToUpper(join.Type), "OUTER")
		rightAlias := join.Table.Alias
		if rightAlias == "" {
			rightAlias = join.Table.Name
		}

		var nextRows []map[string]any

		for _, row := range currentRows {
			// Parameterize the inner query using values from the current row
			innerSQL, err := buildParameterizedApplyQuery(join.Table, row, leftAlias, leftTableRef.Name)
			if err != nil {
				return nil, err
			}

			innerRes, err := s.execSelect(innerSQL, time.Now(), cs)
			if err != nil {
				return nil, fmt.Errorf("error executing APPLY subquery (%s): %w", innerSQL, err)
			}

			if len(innerRes.Rows) > 0 {
				for _, inRow := range innerRes.Rows {
					combined := make(map[string]any, len(row)+len(inRow)*2)
					// Copy left row
					for k, v := range row {
						combined[k] = v
						if leftAlias != "" && !strings.Contains(k, ".") {
							combined[leftAlias+"."+k] = v
						}
					}
					// Copy right row
					for k, v := range inRow {
						combined[k] = v
						if rightAlias != "" && !strings.Contains(k, ".") {
							combined[rightAlias+"."+k] = v
						}
					}
					nextRows = append(nextRows, combined)
				}
			} else if isOuter {
				// OUTER APPLY preserves the left row with NULLs for right side
				combined := make(map[string]any, len(row)+len(innerRes.Columns)*2)
				for k, v := range row {
					combined[k] = v
					if leftAlias != "" && !strings.Contains(k, ".") {
						combined[leftAlias+"."+k] = v
					}
				}
				for _, col := range innerRes.Columns {
					combined[col] = nil
					if rightAlias != "" && !strings.Contains(col, ".") {
						combined[rightAlias+"."+col] = nil
					}
				}
				nextRows = append(nextRows, combined)
			}
		}

		currentRows = nextRows
	}

	// 3. Apply outer WHERE filter
	if selStmt.Where != nil {
		var filtered []map[string]any
		for _, row := range currentRows {
			match, err := evalApplyWhere(selStmt.Where, row)
			if err != nil {
				return nil, err
			}
			if match {
				filtered = append(filtered, row)
			}
		}
		currentRows = filtered
	}

	// 4. Project columns
	finalCols, projectedRows, err := projectApplyRows(selStmt.Projections, currentRows)
	if err != nil {
		return nil, err
	}

	// 5. Apply ORDER BY
	if len(selStmt.OrderBy) > 0 {
		sortApplyRows(projectedRows, selStmt.OrderBy)
	}

	// 6. Apply OFFSET & LIMIT
	if selStmt.Offset > 0 {
		if int(selStmt.Offset) < len(projectedRows) {
			projectedRows = projectedRows[selStmt.Offset:]
		} else {
			projectedRows = []map[string]any{}
		}
	}
	if selStmt.Limit >= 0 && int(selStmt.Limit) < len(projectedRows) {
		projectedRows = projectedRows[:selStmt.Limit]
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

// buildParameterizedApplyQuery substitutes outer row values into the inner subquery or TVF call.
func buildParameterizedApplyQuery(tbl proc.TableRef, row map[string]any, leftAlias, leftTableName string) (string, error) {
	if tbl.Subquery != nil {
		rawSQL := tbl.Subquery.String()
		// Replace correlated references
		for k, v := range row {
			lit := formatApplyLiteral(v)
			// Replace alias.column
			if leftAlias != "" {
				pat := fmt.Sprintf(`(?i)\b%s\.%s\b`, regexp.QuoteMeta(leftAlias), regexp.QuoteMeta(k))
				rawSQL = getCompiledRegexp(pat).ReplaceAllString(rawSQL, lit)
			}
			if leftTableName != "" && leftTableName != leftAlias {
				pat := fmt.Sprintf(`(?i)\b%s\.%s\b`, regexp.QuoteMeta(leftTableName), regexp.QuoteMeta(k))
				rawSQL = getCompiledRegexp(pat).ReplaceAllString(rawSQL, lit)
			}
		}
		return rawSQL, nil
	}

	if tbl.IsTVF {
		var argStrs []string
		for _, a := range tbl.TVFArgs {
			s := a.String()
			for k, v := range row {
				lit := formatApplyLiteral(v)
				if leftAlias != "" {
					pat := fmt.Sprintf(`(?i)\b%s\.%s\b`, regexp.QuoteMeta(leftAlias), regexp.QuoteMeta(k))
					s = getCompiledRegexp(pat).ReplaceAllString(s, lit)
				}
				patCol := fmt.Sprintf(`(?i)\b%s\b`, regexp.QuoteMeta(k))
				s = getCompiledRegexp(patCol).ReplaceAllString(s, lit)
			}
			argStrs = append(argStrs, s)
		}
		return fmt.Sprintf("SELECT * FROM %s(%s)", tbl.Name, strings.Join(argStrs, ", ")), nil
	}

	return fmt.Sprintf("SELECT * FROM %s", tbl.Name), nil
}

func formatApplyLiteral(v any) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("'%s'", strings.ReplaceAll(val, "'", "''"))
	case []byte:
		return fmt.Sprintf("'%s'", strings.ReplaceAll(string(val), "'", "''"))
	case time.Time:
		return fmt.Sprintf("'%s'", val.Format("2006-01-02 15:04:05"))
	case bool:
		if val {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprintf("%v", val)
	}
}

func evalApplyWhere(whereExpr proc.Expr, row map[string]any) (bool, error) {
	return proc.EvalBoolExpr(whereExpr, row, nil), nil
}

func projectApplyRows(projections []proc.Projection, rows []map[string]any) ([]string, []map[string]any, error) {
	if len(rows) == 0 {
		var cols []string
		for _, p := range projections {
			alias := p.Alias
			if alias == "" {
				alias = p.Expr.String()
			}
			if alias != "*" {
				cols = append(cols, cleanIdentifier(alias))
			}
		}
		return cols, rows, nil
	}

	isSelectStar := len(projections) == 1 && projections[0].Expr.String() == "*"
	if isSelectStar {
		// Collect all unique unqualified column names in first row
		var cols []string
		seen := make(map[string]bool)
		for k := range rows[0] {
			if !strings.Contains(k, ".") && !seen[k] {
				cols = append(cols, k)
				seen[k] = true
			}
		}
		sort.Strings(cols)
		return cols, rows, nil
	}

	var cols []string
	for _, p := range projections {
		alias := p.Alias
		if alias == "" {
			alias = p.Expr.String()
		}
		cols = append(cols, cleanIdentifier(alias))
	}

	projected := make([]map[string]any, len(rows))
	for i, row := range rows {
		pRow := make(map[string]any, len(projections))
		for _, p := range projections {
			alias := p.Alias
			if alias == "" {
				alias = p.Expr.String()
			}
			cleanedAlias := cleanIdentifier(alias)
			val, err := proc.EvalRowExpr(p.Expr, row, nil)
			if err != nil {
				// Fallback to direct map lookup
				val = row[alias]
				if val == nil {
					val = row[p.Expr.String()]
				}
			}
			pRow[cleanedAlias] = val
			if dot := strings.Index(cleanedAlias, "."); dot != -1 {
				pRow[cleanedAlias[dot+1:]] = val
			}
		}
		projected[i] = pRow
	}

	return cols, projected, nil
}

func sortApplyRows(rows []map[string]any, orderBys []proc.OrderByItem) {
	if len(rows) <= 1 || len(orderBys) == 0 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		for _, ob := range orderBys {
			col := cleanIdentifier(ob.Expr.String())
			v1 := rows[i][col]
			v2 := rows[j][col]
			if v1 == nil && v2 == nil {
				continue
			}
			if v1 == nil {
				return !ob.Desc
			}
			if v2 == nil {
				return ob.Desc
			}
			cmp := compareApplyValues(v1, v2)
			if cmp == 0 {
				continue
			}
			if ob.Desc {
				return cmp > 0
			}
			return cmp < 0
		}
		return false
	})
}

func compareApplyValues(v1, v2 any) int {
	s1 := fmt.Sprintf("%v", v1)
	s2 := fmt.Sprintf("%v", v2)
	switch val1 := v1.(type) {
	case int:
		if val2, ok := v2.(int); ok {
			if val1 < val2 {
				return -1
			} else if val1 > val2 {
				return 1
			}
			return 0
		}
	case int64:
		if val2, ok := v2.(int64); ok {
			if val1 < val2 {
				return -1
			} else if val1 > val2 {
				return 1
			}
			return 0
		}
	case float64:
		if val2, ok := v2.(float64); ok {
			if val1 < val2 {
				return -1
			} else if val1 > val2 {
				return 1
			}
			return 0
		}
	}
	return strings.Compare(s1, s2)
}
