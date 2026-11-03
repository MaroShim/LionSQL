package engine

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"lionsql/pkg/planner"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

var (
	derivedTableCounter int64
)

func hasDerivedTables(sql string) bool {
	upper := strings.ToUpper(sql)
	if !strings.Contains(upper, "SELECT") {
		return false
	}
	openParen, _ := findNextDerivedTable(sql)
	return openParen != -1
}

func findNextDerivedTable(sql string) (openParenIdx int, closeParenIdx int) {
	upper := strings.ToUpper(sql)
	i := 0
	n := len(upper)
	sqlLen := len(sql)
	for i < n {
		ch := upper[i]
		if ch == 'F' || ch == 'J' {
			isFrom := strings.HasPrefix(upper[i:], "FROM") && (i == 0 || isSep(upper[i-1])) && (i+4 == n || (i+4 < n && isSep(upper[i+4])))
			isJoin := strings.HasPrefix(upper[i:], "JOIN") && (i == 0 || isSep(upper[i-1])) && (i+4 == n || (i+4 < n && isSep(upper[i+4])))
			if isFrom || isJoin {
				kwLen := 4
				pos := i + kwLen
				for pos < n && (upper[pos] == ' ' || upper[pos] == '\t' || upper[pos] == '\r' || upper[pos] == '\n') {
					pos++
				}
				if pos < n && upper[pos] == '(' && pos < sqlLen {
					innerPos := pos + 1
					for innerPos < n && (upper[innerPos] == ' ' || upper[innerPos] == '\t' || upper[innerPos] == '\r' || upper[innerPos] == '\n') {
						innerPos++
					}
					if strings.HasPrefix(upper[innerPos:], "SELECT") && (innerPos+6 == n || (innerPos+6 < n && isSep(upper[innerPos+6]))) {
						cIdx := sqlutil.FindMatchingParen(sql, pos)
						if cIdx != -1 {
							return pos, cIdx
						}
					}
				}
			}
		}
		// Skip single-quoted string literals safely
		if ch == '\'' {
			i++
			for i < n && upper[i] != '\'' {
				if upper[i] == '\\' && i+1 < n {
					i++
				}
				i++
			}
			if i < n {
				i++
			}
			if i >= n {
				break
			}
			continue
		}
		i++
	}
	return -1, -1
}

func isSep(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == '(' || ch == ')' || ch == ',' || ch == ';'
}

func parseDerivedTableAlias(afterClose string) (string, int) {
	trimmed := strings.TrimLeft(afterClose, " \t\r\n")
	leadingWs := len(afterClose) - len(trimmed)
	if trimmed == "" {
		return "", 0
	}
	// Check optional "AS "
	if len(trimmed) >= 3 && strings.EqualFold(trimmed[:2], "AS") && (trimmed[2] == ' ' || trimmed[2] == '\t' || trimmed[2] == '\r' || trimmed[2] == '\n') {
		afterAs := strings.TrimLeft(trimmed[2:], " \t\r\n")
		asWs := len(trimmed) - 2 - len(afterAs)
		ident, idLen := parseIdentifierToken(afterAs)
		if ident != "" {
			return ident, leadingWs + 2 + asWs + idLen
		}
	} else {
		ident, idLen := parseIdentifierToken(trimmed)
		if ident != "" {
			upperIdent := strings.ToUpper(ident)
			switch upperIdent {
			case "WHERE", "GROUP", "ORDER", "LIMIT", "OFFSET", "JOIN", "INNER", "LEFT", "RIGHT", "FULL", "CROSS", "ON", "UNION", "HAVING":
				return "", 0
			}
			return ident, leadingWs + idLen
		}
	}
	return "", 0
}

func parseIdentifierToken(s string) (string, int) {
	if s == "" {
		return "", 0
	}
	first := s[0]
	if first == '"' || first == '`' {
		end := strings.IndexByte(s[1:], first)
		if end != -1 {
			return cleanIdentifier(s[:end+2]), end + 2
		}
		return "", 0
	}
	if first == '[' {
		end := strings.IndexByte(s[1:], ']')
		if end != -1 {
			return cleanIdentifier(s[:end+2]), end + 2
		}
		return "", 0
	}
	i := 0
	for i < len(s) {
		ch := s[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '#' || ch == '$' {
			i++
		} else {
			break
		}
	}
	if i > 0 {
		return s[:i], i
	}
	return "", 0
}

func (s *Coordinator) resolveDerivedTables(query string, start time.Time, cs *ClientSession) (string, []string, error) {
	currentSQL := query
	var createdTables []string

	for {
		openParenIdx, closeParenIdx := findNextDerivedTable(currentSQL)
		if openParenIdx == -1 || closeParenIdx == -1 {
			break
		}

		innerSQL := strings.TrimSpace(currentSQL[openParenIdx+1 : closeParenIdx])
		afterClose := currentSQL[closeParenIdx+1:]
		alias, consumed := parseDerivedTableAlias(afterClose)
		matchEnd := closeParenIdx + 1 + consumed

		counterVal := atomic.AddInt64(&derivedTableCounter, 1)
		if alias == "" {
			alias = fmt.Sprintf("dt_%d", counterVal)
		}
		tempName := fmt.Sprintf("#__dt_%d_%s", counterVal, alias)

		// If the enclosing query defines CTEs (WITH ...), pass the CTE definitions to the inner query
		// so inner subqueries can reference CTE tables (e.g. Vikunja permission queries).
		queryToExec := innerSQL
		trimmedCurrent := strings.TrimSpace(currentSQL)
		if strings.HasPrefix(strings.ToUpper(trimmedCurrent), "WITH") && len(trimmedCurrent) > 4 && (trimmedCurrent[4] == ' ' || trimmedCurrent[4] == '\t' || trimmedCurrent[4] == '\n' || trimmedCurrent[4] == '\r') {
			if ctes, mainQ := planner.ParseCTEs(trimmedCurrent); len(ctes) > 0 {
				ctePrefix := strings.TrimSpace(trimmedCurrent[:len(trimmedCurrent)-len(mainQ)])
				if ctePrefix != "" && openParenIdx >= len(ctePrefix) {
					queryToExec = ctePrefix + " " + innerSQL
				}
			}
		}

		// Execute inner SELECT query
		innerRes, err := s.execSelect(queryToExec, start, cs)
		if err != nil {
			return "", createdTables, fmt.Errorf("derived table subquery error: %w", err)
		}
		if !innerRes.Success {
			return "", createdTables, fmt.Errorf("derived table subquery failed: %s", innerRes.Error)
		}

		// Materialized derived tables always use synthetic _dt_row_id as PK to support duplicates and 0 rows safely
		needSyntheticPK := true
		schema := &storage.TableSchema{
			Name:    tempName,
			Columns: make([]storage.Column, 0, len(innerRes.Columns)+1),
		}

		schema.Columns = append(schema.Columns, storage.NewSyntheticPKColumn(storage.SyntheticPKDerivedTable))

		for _, colName := range innerRes.Columns {
			colType := storage.TypeVarChar
			if len(innerRes.Rows) > 0 {
				for _, row := range innerRes.Rows {
					if v, ok := row[colName]; ok && v != nil {
						switch v.(type) {
						case int, int64:
							colType = storage.TypeInt
						case float32, float64:
							colType = storage.TypeFloat
						case bool:
							colType = storage.TypeBool
						case storage.Decimal:
							colType = storage.TypeDecimal
						}
						break
					}
				}
			}
			schema.Columns = append(schema.Columns, storage.Column{
				Name:       colName,
				Type:       colType,
				PrimaryKey: false,
				Nullable:   true,
			})
		}

		if err := s.engine.CreateTable(schema); err != nil {
			return "", createdTables, fmt.Errorf("failed to materialize derived table %q: %w", tempName, err)
		}
		createdTables = append(createdTables, tempName)

		if len(innerRes.Rows) > 0 {
			var tx storage.Transaction
			var session storage.Session
			var shouldCommit bool
			if cs != nil && cs.Tx != nil {
				tx = cs.Tx
			} else {
				var err error
				session, err = s.engine.NewSession()
				if err != nil {
					return "", createdTables, err
				}
				tx, err = session.BeginTx(storage.TxOptions{})
				if err != nil {
					session.Close()
					return "", createdTables, err
				}
				shouldCommit = true
			}
			for rowIdx, row := range innerRes.Rows {
				rec := make(storage.Record, len(schema.Columns))
				if needSyntheticPK {
					rec["_dt_row_id"] = storage.IntValue(int64(rowIdx + 1))
				}
				for _, colSchema := range schema.Columns {
					if colSchema.Name == "_dt_row_id" {
						continue
					}
					var v any
					if val, ok := row[colSchema.Name]; ok {
						v = val
					} else if val, ok := row[strings.ToLower(colSchema.Name)]; ok {
						v = val
					}
					rec[colSchema.Name] = ConvertValue(v, colSchema.Type)
				}
				if err := tx.Insert(tempName, rec); err != nil {
					if shouldCommit {
						_ = tx.Rollback()
						session.Close()
					}
					return "", createdTables, fmt.Errorf("failed to populate derived table %q: %w", tempName, err)
				}
			}
			if shouldCommit {
				if err := tx.Commit(); err != nil {
					session.Close()
					return "", createdTables, fmt.Errorf("failed to commit derived table %q: %w", tempName, err)
				}
				session.Close()
			}
		}

		prefixPart := currentSQL[:openParenIdx]
		suffixPart := currentSQL[matchEnd:]
		if needSyntheticPK && len(innerRes.Columns) > 0 {
			fromIdx := sqlutil.FindTopLevelKeyword(prefixPart, "FROM")
			if fromIdx != -1 {
				afterFrom := strings.TrimSpace(prefixPart[fromIdx+4:])
				hasJoinOrComma := sqlutil.ContainsKeyword(prefixPart, "JOIN", false) ||
					sqlutil.ContainsKeyword(suffixPart, "JOIN", false) ||
					strings.Contains(prefixPart[fromIdx+4:], ",")
				if afterFrom == "" && !hasJoinOrComma {
					trimmedPrefix := strings.TrimSpace(prefixPart[:fromIdx])
					upperPrefix := strings.ToUpper(trimmedPrefix)
					if strings.HasPrefix(upperPrefix, "SELECT") && strings.TrimSpace(trimmedPrefix[6:]) == "*" {
						var qualifiedCols []string
						for _, col := range innerRes.Columns {
							qualifiedCols = append(qualifiedCols, fmt.Sprintf("%s.%s", alias, col))
						}
						prefixPart = "SELECT " + strings.Join(qualifiedCols, ", ") + " FROM "
					}
				}
			}
		}

		currentSQL = prefixPart + tempName + " AS " + alias + suffixPart
	}

	return currentSQL, createdTables, nil
}
