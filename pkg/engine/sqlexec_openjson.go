package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
)

type openJsonColDef struct {
	name     string
	dataType string
	path     string
	asJson   bool
}

func isOpenJsonSelect(query string) bool {
	trimmed := strings.TrimSpace(query)
	if !sqlutil.HasPrefixKeyword(trimmed, "SELECT") {
		return false
	}
	fromIdx := sqlutil.FindTopLevelKeyword(trimmed, "FROM")
	if fromIdx == -1 {
		return false
	}
	afterFrom := strings.TrimSpace(trimmed[fromIdx+4:])
	if rem, ok := sqlutil.TrimPrefixKeyword(afterFrom, "DBO."); ok {
		afterFrom = rem
	} else if rem, ok := sqlutil.TrimPrefixKeyword(afterFrom, "[DBO]."); ok {
		afterFrom = rem
	}
	afterFrom = strings.TrimPrefix(afterFrom, "[")
	return sqlutil.HasPrefixKeyword(afterFrom, "OPENJSON") || sqlutil.HasPrefixKeyword(afterFrom, "OPENJSON]")
}

func parseOpenJsonWithClause(withPart string) ([]openJsonColDef, string) {
	trimmed := strings.TrimSpace(withPart)
	if !sqlutil.HasPrefixKeyword(trimmed, "WITH") {
		return nil, trimmed
	}
	afterWith := strings.TrimSpace(trimmed[4:])
	if !strings.HasPrefix(afterWith, "(") {
		return nil, trimmed
	}
	closeParen := sqlutil.FindMatchingParen(afterWith, 0)
	if closeParen == -1 {
		return nil, trimmed
	}
	inner := strings.TrimSpace(afterWith[1:closeParen])
	rest := strings.TrimSpace(afterWith[closeParen+1:])

	rawDefs := sqlutil.SplitTopLevel(inner, ',')
	var cols []openJsonColDef
	for _, defStr := range rawDefs {
		defStr = strings.TrimSpace(defStr)
		if defStr == "" {
			continue
		}
		asJson := false
		if jsonIdx := sqlutil.FindTopLevelKeyword(defStr, "AS JSON"); jsonIdx != -1 {
			asJson = true
			defStr = strings.TrimSpace(defStr[:jsonIdx])
		}

		// Find string literal path if present (e.g. '$.name')
		path := ""
		quoteStart := strings.IndexAny(defStr, "'\"")
		if quoteStart != -1 {
			qChar := defStr[quoteStart]
			quoteEnd := strings.IndexByte(defStr[quoteStart+1:], qChar)
			if quoteEnd != -1 {
				path = defStr[quoteStart+1 : quoteStart+1+quoteEnd]
				defStr = strings.TrimSpace(defStr[:quoteStart])
			}
		}

		fields := strings.Fields(defStr)
		if len(fields) == 0 {
			continue
		}
		name := cleanIdentifier(fields[0])
		dataType := "VARCHAR(MAX)"
		if len(fields) > 1 {
			dataType = strings.ToUpper(strings.Join(fields[1:], " "))
		}
		if path == "" {
			path = "$." + name
		}

		cols = append(cols, openJsonColDef{
			name:     name,
			dataType: dataType,
			path:     path,
			asJson:   asJson,
		})
	}

	return cols, rest
}

func evalJsonPath(root any, path string) any {
	path = strings.TrimSpace(path)
	if path == "" || path == "$" {
		return root
	}
	if strings.HasPrefix(path, "$.") {
		path = path[2:]
	} else if strings.HasPrefix(path, "$") {
		path = path[1:]
	}

	tokens := parsePathTokens(path)
	current := root
	for _, tok := range tokens {
		if current == nil {
			return nil
		}
		if tok.isIndex {
			arr, ok := current.([]any)
			if !ok || tok.index < 0 || tok.index >= len(arr) {
				return nil
			}
			current = arr[tok.index]
		} else {
			m, ok := current.(map[string]any)
			if !ok {
				return nil
			}
			val, found := m[tok.prop]
			if !found {
				// Case-insensitive match fallback
				matched := false
				for k, v := range m {
					if strings.EqualFold(k, tok.prop) {
						current = v
						matched = true
						break
					}
				}
				if !matched {
					return nil
				}
			} else {
				current = val
			}
		}
	}
	return current
}

type pathToken struct {
	prop    string
	index   int
	isIndex bool
}

func parsePathTokens(path string) []pathToken {
	var tokens []pathToken
	n := len(path)
	i := 0
	for i < n {
		if path[i] == '.' {
			i++
			continue
		}
		if path[i] == '[' {
			end := strings.IndexByte(path[i:], ']')
			if end == -1 {
				break
			}
			idxStr := path[i+1 : i+end]
			if idx, err := strconv.Atoi(idxStr); err == nil {
				tokens = append(tokens, pathToken{index: idx, isIndex: true})
			}
			i += end + 1
			continue
		}
		start := i
		for i < n && path[i] != '.' && path[i] != '[' {
			i++
		}
		if i > start {
			tokens = append(tokens, pathToken{prop: path[start:i]})
		}
	}
	return tokens
}

func castJsonValueToType(val any, dataType string) any {
	if val == nil {
		return nil
	}
	dt := strings.ToUpper(dataType)
	if idx := strings.IndexByte(dt, '('); idx != -1 {
		dt = strings.TrimSpace(dt[:idx])
	}

	switch dt {
	case "INT", "INTEGER", "BIGINT", "SMALLINT", "TINYINT":
		switch v := val.(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		case string:
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return n
			}
		case bool:
			if v {
				return int64(1)
			}
			return int64(0)
		}
	case "FLOAT", "REAL", "DECIMAL", "NUMERIC":
		switch v := val.(type) {
		case float64:
			return v
		case int64:
			return float64(v)
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f
			}
		}
	case "BIT", "BOOL", "BOOLEAN":
		switch v := val.(type) {
		case bool:
			return v
		case float64:
			return v != 0
		case int64:
			return v != 0
		case string:
			return strings.EqualFold(v, "true") || v == "1"
		}
	default:
		// Text/string types
		switch v := val.(type) {
		case string:
			return v
		default:
			bytes, err := json.Marshal(v)
			if err == nil {
				return string(bytes)
			}
			return fmt.Sprintf("%v", v)
		}
	}
	return val
}

func (s *Coordinator) execOpenJsonSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimSpace(query)

	fromIdx := sqlutil.FindTopLevelKeyword(cleaned, "FROM")
	if fromIdx == -1 {
		return nil, fmt.Errorf("invalid OPENJSON query: missing FROM")
	}
	projStr := strings.TrimSpace(cleaned[6:fromIdx])

	isDistinct := false
	if rem, ok := sqlutil.TrimPrefixKeyword(projStr, "DISTINCT"); ok {
		isDistinct = true
		projStr = rem
	}

	openJsonIdx := sqlutil.FindKeyword(cleaned[fromIdx:], "OPENJSON", false)
	if openJsonIdx == -1 {
		return nil, fmt.Errorf("invalid OPENJSON query: missing OPENJSON")
	}
	openParen := fromIdx + openJsonIdx + len("OPENJSON")
	for openParen < len(cleaned) && cleaned[openParen] != '(' {
		openParen++
	}
	if openParen >= len(cleaned) {
		return nil, fmt.Errorf("invalid OPENJSON query: missing opening parenthesis")
	}

	closeParen := sqlutil.FindMatchingParen(cleaned, openParen)
	if closeParen == -1 {
		return nil, fmt.Errorf("invalid OPENJSON query: unclosed parenthesis")
	}

	argsRaw := strings.TrimSpace(cleaned[openParen+1 : closeParen])
	rest := strings.TrimSpace(cleaned[closeParen+1:])

	// Parse optional WITH clause
	var withCols []openJsonColDef
	if sqlutil.HasPrefixKeyword(rest, "WITH") {
		withCols, rest = parseOpenJsonWithClause(rest)
	}

	// Parse alias and tail clauses
	alias, whereClause, orderByClause, limitCount, offsetCount := extractStringSplitTailClauses(rest)
	if sqlutil.FindTopLevelKeyword(rest, "WHERE") != -1 && whereClause == "" {
		return nil, fmt.Errorf("syntax error: empty or invalid expression in WHERE clause")
	}
	if err := ValidateWhereClause(whereClause); err != nil {
		return nil, err
	}

	args := sqlutil.SplitTopLevel(argsRaw, ',')
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return nil, fmt.Errorf("OPENJSON requires at least 1 JSON expression argument")
	}

	jsonRaw := resolveArgString(args[0], cs)
	var rootPath string
	if len(args) > 1 {
		rootPath = resolveArgString(args[1], cs)
	}

	if jsonRaw == "" || strings.EqualFold(jsonRaw, "NULL") {
		return &QueryResult{
			Success:      true,
			Command:      "SELECT",
			Columns:      []string{"key", "value", "type"},
			Rows:         []map[string]any{},
			RowsAffected: 0,
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	var parsedJson any
	if err := json.Unmarshal([]byte(jsonRaw), &parsedJson); err != nil {
		return nil, fmt.Errorf("invalid JSON text passed to OPENJSON: %w", err)
	}

	targetNode := parsedJson
	if rootPath != "" {
		targetNode = evalJsonPath(parsedJson, rootPath)
	}

	var rows []map[string]any
	var columns []string

	if len(withCols) > 0 {
		// Explicit Schema Mode
		for _, col := range withCols {
			columns = append(columns, col.name)
		}

		var items []any
		if arr, ok := targetNode.([]any); ok {
			items = arr
		} else if targetNode != nil {
			items = []any{targetNode}
		}

		for _, item := range items {
			r := make(map[string]any, len(withCols))
			for _, col := range withCols {
				val := evalJsonPath(item, col.path)
				if col.asJson {
					if val != nil {
						b, err := json.Marshal(val)
						if err == nil {
							r[col.name] = string(b)
						} else {
							r[col.name] = fmt.Sprintf("%v", val)
						}
					} else {
						r[col.name] = nil
					}
				} else {
					r[col.name] = castJsonValueToType(val, col.dataType)
				}
				if alias != "" {
					r[alias+"."+col.name] = r[col.name]
				}
			}
			rows = append(rows, r)
		}
	} else {
		// Default 3-column Mode: key, value, type
		columns = []string{"key", "value", "type"}

		switch node := targetNode.(type) {
		case map[string]any:
			keys := make([]string, 0, len(node))
			for k := range node {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			for _, k := range keys {
				val := node[k]
				vStr, vType := formatOpenJsonDefaultValue(val)
				r := make(map[string]any, 3)
				r["key"] = k
				r["value"] = vStr
				r["type"] = vType
				if alias != "" {
					r[alias+".key"] = k
					r[alias+".value"] = vStr
					r[alias+".type"] = vType
				}
				rows = append(rows, r)
			}

		case []any:
			for i, val := range node {
				vStr, vType := formatOpenJsonDefaultValue(val)
				kStr := strconv.Itoa(i)
				r := make(map[string]any, 3)
				r["key"] = kStr
				r["value"] = vStr
				r["type"] = vType
				if alias != "" {
					r[alias+".key"] = kStr
					r[alias+".value"] = vStr
					r[alias+".type"] = vType
				}
				rows = append(rows, r)
			}
		}
	}

	// Apply WHERE filter
	if whereClause != "" {
		var filtered []map[string]any
		for _, r := range rows {
			if evaluateWhereConditionMap(r, whereClause) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	// Apply ORDER BY
	if orderByClause != "" {
		if parsed, err := proc.ParseSQL("SELECT 1 ORDER BY " + orderByClause); err == nil {
			if selStmt, ok := parsed.(*proc.GeneralSelectStmt); ok && selStmt != nil && len(selStmt.OrderBy) > 0 {
				sortApplyRows(rows, selStmt.OrderBy)
			}
		}
	}

	// Handle Projections
	trimmedProj := strings.TrimSpace(projStr)
	if trimmedProj != "*" {
		if strings.EqualFold(trimmedProj, "COUNT(*)") {
			return &QueryResult{
				Success:      true,
				Command:      "SELECT",
				Columns:      []string{"count"},
				Rows:         []map[string]any{{"count": int64(len(rows))}},
				RowsAffected: 1,
				DurationUS:   time.Since(start).Microseconds(),
			}, nil
		}

		projCols := planner.SplitProjections(projStr)
		var projNames []string
		for _, pc := range projCols {
			aliasName := pc
			if asIdx := sqlutil.FindTopLevelKeyword(pc, "AS"); asIdx != -1 {
				aliasName = strings.TrimSpace(pc[asIdx+len("AS"):])
			} else if strings.Contains(aliasName, ".") {
				aliasName = aliasName[strings.LastIndex(aliasName, ".")+1:]
			}
			projNames = append(projNames, cleanIdentifier(aliasName))
		}

		projected := make([]map[string]any, len(rows))
		for i, r := range rows {
			pRow := make(map[string]any, len(projNames))
			for j, pc := range projCols {
				colName := projNames[j]
				rawCol := pc
				if asIdx := sqlutil.FindTopLevelKeyword(pc, "AS"); asIdx != -1 {
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
		columns = projNames
	}

	if isDistinct {
		rows = deduplicateRows(rows)
	}

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

func formatOpenJsonDefaultValue(val any) (any, int) {
	if val == nil {
		return nil, 0
	}
	switch v := val.(type) {
	case string:
		return v, 1
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v)), 2
		}
		return fmt.Sprintf("%v", v), 2
	case bool:
		if v {
			return "true", 3
		}
		return "false", 3
	case []any:
		b, err := json.Marshal(v)
		if err == nil {
			return string(b), 4
		}
		return fmt.Sprintf("%v", v), 4
	case map[string]any:
		b, err := json.Marshal(v)
		if err == nil {
			return string(b), 5
		}
		return fmt.Sprintf("%v", v), 5
	default:
		return fmt.Sprintf("%v", v), 1
	}
}
