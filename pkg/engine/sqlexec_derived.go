package engine

import (
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"lionsql/pkg/storage"
)

var (
	derivedTableCounter int64
	reDerivedCheck      = regexp.MustCompile(`(?is)\b(?:FROM|JOIN)\s*\(\s*SELECT\b`)
	reDerivedMatch      = regexp.MustCompile(`(?is)\b(FROM|JOIN|INNER\s+JOIN|LEFT\s+JOIN|LEFT\s+OUTER\s+JOIN|RIGHT\s+JOIN|RIGHT\s+OUTER\s+JOIN|CROSS\s+JOIN|FULL\s+JOIN|FULL\s+OUTER\s+JOIN)\s*\(\s*SELECT\b`)
	reDerivedAlias      = regexp.MustCompile(`(?is)^\s*(?:AS\s+)?([a-zA-Z0-9_#]+)`)
	reSelectStarFrom    = regexp.MustCompile(`(?i)\bSELECT\s+\*\s+FROM\b`)
)

func hasDerivedTables(sql string) bool {
	upper := strings.ToUpper(sql)
	if !strings.Contains(upper, "SELECT") {
		return false
	}
	return reDerivedCheck.MatchString(sql)
}

func (s *Coordinator) resolveDerivedTables(query string, start time.Time, cs *ClientSession) (string, []string, error) {
	currentSQL := query
	var createdTables []string

	for {
		loc := reDerivedMatch.FindStringIndex(currentSQL)
		if loc == nil {
			break
		}

		openParenIdx := -1
		for i := loc[0]; i < len(currentSQL); i++ {
			if currentSQL[i] == '(' {
				openParenIdx = i
				break
			}
		}
		if openParenIdx == -1 {
			break
		}

		depth := 0
		closeParenIdx := -1
		for i := openParenIdx; i < len(currentSQL); i++ {
			if currentSQL[i] == '(' {
				depth++
			} else if currentSQL[i] == ')' {
				depth--
				if depth == 0 {
					closeParenIdx = i
					break
				}
			}
		}
		if closeParenIdx == -1 {
			break
		}

		innerSQL := strings.TrimSpace(currentSQL[openParenIdx+1 : closeParenIdx])
		afterClose := currentSQL[closeParenIdx+1:]
		alias := ""
		matchEnd := closeParenIdx + 1

		if m := reDerivedAlias.FindStringSubmatch(afterClose); len(m) >= 2 {
			alias = cleanIdentifier(m[1])
			matchEnd = closeParenIdx + 1 + len(m[0])
		}

		counterVal := atomic.AddInt64(&derivedTableCounter, 1)
		if alias == "" {
			alias = fmt.Sprintf("dt_%d", counterVal)
		}
		tempName := fmt.Sprintf("#__dt_%d_%s", counterVal, alias)

		// Execute inner SELECT query
		innerRes, err := s.execSelect(innerSQL, start, cs)
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

		schema.Columns = append(schema.Columns, storage.Column{
			Name:       "_dt_row_id",
			Type:       storage.TypeInt,
			PrimaryKey: true,
			Nullable:   false,
			Identity:   true,
			Hidden:     true,
		})

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
			session, err := s.engine.NewSession()
			if err != nil {
				return "", createdTables, err
			}
			tx, err := session.BeginTx(storage.TxOptions{})
			if err != nil {
				session.Close()
				return "", createdTables, err
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
					_ = tx.Rollback()
					session.Close()
					return "", createdTables, fmt.Errorf("failed to populate derived table %q: %w", tempName, err)
				}
			}
			if err := tx.Commit(); err != nil {
				session.Close()
				return "", createdTables, fmt.Errorf("failed to commit derived table %q: %w", tempName, err)
			}
			session.Close()
		}

		prefixPart := currentSQL[:openParenIdx]
		suffixPart := currentSQL[matchEnd:]
		if needSyntheticPK && len(innerRes.Columns) > 0 {
			if reSelectStarFrom.MatchString(prefixPart) {
				var qualifiedCols []string
				for _, col := range innerRes.Columns {
					qualifiedCols = append(qualifiedCols, fmt.Sprintf("%s.%s", alias, col))
				}
				prefixPart = reSelectStarFrom.ReplaceAllString(prefixPart, "SELECT "+strings.Join(qualifiedCols, ", ")+" FROM ")
			}
		}

		currentSQL = prefixPart + tempName + " AS " + alias + suffixPart
	}

	return currentSQL, createdTables, nil
}
