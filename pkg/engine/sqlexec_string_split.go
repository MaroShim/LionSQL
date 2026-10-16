package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/planner"
	"sqlion/pkg/proc"
)

var (
	reSplitLimit   = regexp.MustCompile(`(?is)\bLIMIT\s+(\d+)`)
	reSplitOffset  = regexp.MustCompile(`(?is)\bOFFSET\s+(\d+)`)
	reSplitOrderBy = regexp.MustCompile(`(?is)\bORDER\s+BY\s+(.*?)(?:;|$)`)
	reSplitWhere   = regexp.MustCompile(`(?is)\bWHERE\s+(.*?)(?:;|$)`)
)

func isStringSplitSelect(query string) bool {
	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT") {
		return false
	}
	fromIdx := strings.Index(upper, " FROM ")
	if fromIdx == -1 {
		return false
	}
	// The immediate table after the first FROM must be STRING_SPLIT
	afterFrom := strings.TrimSpace(upper[fromIdx+6:])
	afterFrom = strings.TrimPrefix(afterFrom, "DBO.")
	afterFrom = strings.TrimPrefix(afterFrom, "[DBO].")
	afterFrom = strings.TrimPrefix(afterFrom, "[")
	return strings.HasPrefix(afterFrom, "STRING_SPLIT(") || strings.HasPrefix(afterFrom, "STRING_SPLIT (") ||
		strings.HasPrefix(afterFrom, "STRING_SPLIT](") || strings.HasPrefix(afterFrom, "STRING_SPLIT] (")
}

func (s *Coordinator) execStringSplitSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimSpace(query)
	upper := strings.ToUpper(cleaned)

	// 1. Find FROM STRING_SPLIT(
	fromIdx := strings.Index(upper, "FROM")
	if fromIdx == -1 {
		return nil, fmt.Errorf("invalid STRING_SPLIT query: missing FROM")
	}
	projStr := strings.TrimSpace(cleaned[6:fromIdx])

	isDistinct := false
	if strings.HasPrefix(strings.ToUpper(projStr), "DISTINCT ") {
		isDistinct = true
		projStr = strings.TrimSpace(projStr[8:])
	}

	splitIdx := strings.Index(upper[fromIdx:], "STRING_SPLIT")
	if splitIdx == -1 {
		return nil, fmt.Errorf("invalid STRING_SPLIT query: missing STRING_SPLIT")
	}
	openParen := fromIdx + splitIdx + len("STRING_SPLIT")
	for openParen < len(cleaned) && cleaned[openParen] != '(' {
		openParen++
	}
	if openParen >= len(cleaned) {
		return nil, fmt.Errorf("invalid STRING_SPLIT query: missing opening parenthesis")
	}

	// 2. Find matching closing parenthesis
	closeParen := findMatchingParen(cleaned, openParen)
	if closeParen == -1 {
		return nil, fmt.Errorf("invalid STRING_SPLIT query: unclosed parenthesis")
	}

	argsRaw := strings.TrimSpace(cleaned[openParen+1 : closeParen])
	rest := strings.TrimSpace(cleaned[closeParen+1:])

	// 3. Parse clauses in rest: [AS alias] [WHERE whereClause] [ORDER BY orderByClause] [LIMIT limit] [OFFSET offset]
	var alias, whereClause, orderByClause string
	var limitCount, offsetCount int

	if m := reSplitLimit.FindStringSubmatch(rest); len(m) == 2 {
		limitCount, _ = strconv.Atoi(m[1])
		rest = reSplitLimit.ReplaceAllString(rest, "")
	}

	if m := reSplitOffset.FindStringSubmatch(rest); len(m) == 2 {
		offsetCount, _ = strconv.Atoi(m[1])
		rest = reSplitOffset.ReplaceAllString(rest, "")
	}

	if m := reSplitOrderBy.FindStringSubmatch(rest); len(m) == 2 {
		orderByClause = strings.TrimSpace(m[1])
		rest = reSplitOrderBy.ReplaceAllString(rest, "")
	}

	if m := reSplitWhere.FindStringSubmatch(rest); len(m) == 2 {
		whereClause = strings.TrimSpace(m[1])
		rest = reSplitWhere.ReplaceAllString(rest, "")
	}

	// Whatever remains in rest is alias (e.g. "AS s", "s", or "")
	aliasStr := strings.TrimSpace(strings.TrimSuffix(rest, ";"))
	aliasStr = strings.TrimPrefix(aliasStr, "AS ")
	aliasStr = strings.TrimPrefix(aliasStr, "as ")
	alias = cleanIdentifier(strings.TrimSpace(aliasStr))

	// 4. Parse STRING_SPLIT arguments: string, separator [, enable_ordinal]
	args := parseStringSplitArgs(argsRaw)
	if len(args) < 2 {
		return nil, fmt.Errorf("STRING_SPLIT requires at least 2 arguments (string, separator)")
	}

	targetStr := resolveArgString(args[0], cs)
	sep := resolveArgString(args[1], cs)
	if sep == "" {
		sep = ","
	}

	enableOrdinal := false
	if len(args) >= 3 {
		ordArg := resolveArgString(args[2], cs)
		if ordArg == "1" || strings.EqualFold(ordArg, "true") {
			enableOrdinal = true
		}
	}

	// 5. Generate row dataset
	var rows []map[string]any
	if targetStr != "" && !strings.EqualFold(targetStr, "NULL") {
		parts := strings.Split(targetStr, sep)
		for i, part := range parts {
			r := make(map[string]any)
			r["value"] = part
			if enableOrdinal {
				r["ordinal"] = int64(i + 1)
			}
			if alias != "" {
				r[alias+".value"] = part
				if enableOrdinal {
					r[alias+".ordinal"] = int64(i + 1)
				}
			}
			rows = append(rows, r)
		}
	}

	// 6. Apply WHERE filter if present
	if whereClause != "" {
		var filtered []map[string]any
		for _, r := range rows {
			if evaluateWhereConditionMap(r, whereClause) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	// 7. Apply ORDER BY if present
	if orderByClause != "" {
		if parsed, err := proc.ParseSQL("SELECT 1 ORDER BY " + orderByClause); err == nil {
			if selStmt, ok := parsed.(*proc.GeneralSelectStmt); ok && selStmt != nil && len(selStmt.OrderBy) > 0 {
				sortApplyRows(rows, selStmt.OrderBy)
			}
		}
	}

	// 8. Check for aggregate COUNT projection
	upperProj := strings.ToUpper(projStr)
	if upperProj == "COUNT(*)" || upperProj == "COUNT(1)" || upperProj == "COUNT(VALUE)" ||
		strings.HasPrefix(upperProj, "COUNT(*)") || strings.HasPrefix(upperProj, "COUNT(1)") || strings.HasPrefix(upperProj, "COUNT(VALUE)") {
		colName := "count"
		if asIdx := strings.Index(upperProj, " AS "); asIdx != -1 {
			colName = cleanIdentifier(strings.TrimSpace(projStr[asIdx+4:]))
		}
		countRow := map[string]any{colName: int64(len(rows))}
		return &QueryResult{
			Success:      true,
			Command:      "SELECT",
			Columns:      []string{colName},
			Rows:         []map[string]any{countRow},
			RowsAffected: 1,
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	// 9. Project columns
	var columns []string
	if projStr == "*" || projStr == "" {
		if enableOrdinal {
			columns = []string{"value", "ordinal"}
		} else {
			columns = []string{"value"}
		}
		cleanedRows := make([]map[string]any, len(rows))
		for i, r := range rows {
			cr := make(map[string]any, len(columns))
			for _, c := range columns {
				cr[c] = r[c]
			}
			cleanedRows[i] = cr
		}
		rows = cleanedRows
	} else {
		projCols := planner.SplitProjections(projStr)
		for _, pc := range projCols {
			aliasName := pc
			if asIdx := strings.Index(strings.ToUpper(pc), " AS "); asIdx != -1 {
				aliasName = strings.TrimSpace(pc[asIdx+4:])
			} else if strings.Contains(aliasName, ".") {
				aliasName = aliasName[strings.LastIndex(aliasName, ".")+1:]
			}
			columns = append(columns, cleanIdentifier(aliasName))
		}

		projected := make([]map[string]any, len(rows))
		for i, r := range rows {
			pRow := make(map[string]any, len(columns))
			for j, pc := range projCols {
				colName := columns[j]
				rawCol := pc
				if asIdx := strings.Index(strings.ToUpper(pc), " AS "); asIdx != -1 {
					rawCol = strings.TrimSpace(pc[:asIdx])
				}
				cleanedRaw := cleanIdentifier(rawCol)
				val := r[cleanedRaw]
				if val == nil {
					val = r[rawCol]
				}
				if val == nil && strings.Contains(cleanedRaw, ".") {
					val = r[cleanedRaw[strings.Index(cleanedRaw, ".")+1:]]
				}
				pRow[colName] = val
			}
			projected[i] = pRow
		}
		rows = projected
	}

	// 10. Apply DISTINCT if requested
	if isDistinct {
		rows = deduplicateRows(rows)
	}

	// 11. Apply OFFSET and LIMIT
	if offsetCount > 0 {
		if offsetCount < len(rows) {
			rows = rows[offsetCount:]
		} else {
			rows = []map[string]any{}
		}
	}
	if limitCount > 0 && limitCount < len(rows) {
		rows = rows[:limitCount]
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func findMatchingParen(s string, openIdx int) int {
	depth := 0
	inQuote := false
	for i := openIdx; i < len(s); i++ {
		ch := s[i]
		if ch == '\'' {
			inQuote = !inQuote
		} else if !inQuote {
			if ch == '(' {
				depth++
			} else if ch == ')' {
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return -1
}

func parseStringSplitArgs(argsRaw string) []string {
	var args []string
	inQuote := false
	start := 0
	for i := 0; i < len(argsRaw); i++ {
		ch := argsRaw[i]
		if ch == '\'' {
			if inQuote && i+1 < len(argsRaw) && argsRaw[i+1] == '\'' {
				i++
				continue
			}
			inQuote = !inQuote
		} else if ch == ',' && !inQuote {
			args = append(args, strings.TrimSpace(argsRaw[start:i]))
			start = i + 1
		}
	}
	if start <= len(argsRaw) {
		args = append(args, strings.TrimSpace(argsRaw[start:]))
	}
	return args
}

func resolveArgString(arg string, cs *ClientSession) string {
	arg = strings.TrimSpace(arg)
	if strings.HasPrefix(arg, "'") && strings.HasSuffix(arg, "'") && len(arg) >= 2 {
		content := arg[1 : len(arg)-1]
		return strings.ReplaceAll(content, "''", "'")
	}
	if (strings.HasPrefix(arg, "N'") || strings.HasPrefix(arg, "n'")) && strings.HasSuffix(arg, "'") && len(arg) >= 3 {
		content := arg[2 : len(arg)-1]
		return strings.ReplaceAll(content, "''", "'")
	}
	if strings.HasPrefix(arg, "@") && cs != nil {
		cs.Mu.Lock()
		defer cs.Mu.Unlock()
		if cs.Vars != nil {
			varName := arg
			if v, ok := cs.Vars[varName]; ok && v != nil {
				return fmt.Sprintf("%v", v)
			}
			if v, ok := cs.Vars[strings.TrimPrefix(varName, "@")]; ok && v != nil {
				return fmt.Sprintf("%v", v)
			}
			if v, ok := cs.Vars[strings.ToLower(varName)]; ok && v != nil {
				return fmt.Sprintf("%v", v)
			}
			if v, ok := cs.Vars[strings.ToLower(strings.TrimPrefix(varName, "@"))]; ok && v != nil {
				return fmt.Sprintf("%v", v)
			}
		}
	}
	return arg
}
