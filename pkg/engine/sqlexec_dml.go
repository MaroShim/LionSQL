package engine

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"lionsql/pkg/auth"
	"lionsql/pkg/storage"
)

var (
	reDmlInsertCheck  = regexp.MustCompile(`(?is)^(?:INSERT|UPSERT)(?:\s+INTO)?\s+([^\s(]+)`)
	reDmlInsertSelect = regexp.MustCompile(`(?is)^INSERT(?:\s+INTO)?\s+([^\s(]+)(?:\s*\(([^)]+)\))?(?:\s+OUTPUT\s+(.*?))?\s+(\(?\s*SELECT\s+.*)$`)
	reDmlInsertValues = regexp.MustCompile(`(?is)^INSERT\s+INTO\s+([^\s(]+)(?:\s*\(([^)]+)\))?(?:\s+OUTPUT\s+(.*?))?\s+VALUES\s*(.*)$`)
	reDmlUpdateCheck  = regexp.MustCompile(`(?is)^UPDATE\s+([^\s]+)`)
	reDmlUpdate       = regexp.MustCompile(`(?is)^UPDATE\s+([^\s]+)\s+SET\s+(.*?)(?:\s+OUTPUT\s+(.*?))?(?:\s+WHERE\s+(.*))?$`)
	reDmlDeleteCheck  = regexp.MustCompile(`(?is)^DELETE(?:\s+FROM)?\s+([^\s;]+)`)
	reDmlDelete       = regexp.MustCompile(`(?is)^DELETE\s+(?:FROM\s+)?([^\s;]+)(?:\s+OUTPUT\s+(.*?))?(?:\s+WHERE\s+(.*))?$`)
)

type onConflictAction int

const (
	conflictActionNone onConflictAction = iota
	conflictActionDoNothing
	conflictActionDoUpdate
)

type onConflictAssignment struct {
	column string
	expr   string
}

type onConflictClause struct {
	action      onConflictAction
	targetCols  []string
	assignments []onConflictAssignment
	whereCond   string
}

func findOnConflictIndex(s string) int {
	inString := false
	var quoteChar byte
	depth := 0
	upper := strings.ToUpper(s)

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if c == quoteChar {
				if i+1 < len(s) && s[i+1] == quoteChar {
					i++
					continue
				}
				inString = false
			}
			continue
		}
		if c == '\'' || c == '"' {
			inString = true
			quoteChar = c
			continue
		}
		if c == '(' {
			depth++
			continue
		}
		if c == ')' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 && i+11 <= len(s) {
			if strings.HasPrefix(upper[i:], "ON CONFLICT") {
				if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' || s[i-1] == '\n' || s[i-1] == ')' {
					nextIdx := i + 11
					if nextIdx == len(s) || s[nextIdx] == ' ' || s[nextIdx] == '\t' || s[nextIdx] == '\n' || s[nextIdx] == '(' {
						return i
					}
				}
			}
		}
	}
	return -1
}

func parseOnConflict(s string) (*onConflictClause, error) {
	s = strings.TrimSpace(s)
	upper := strings.ToUpper(s)
	if !strings.HasPrefix(upper, "ON CONFLICT") {
		return nil, nil
	}

	rest := strings.TrimSpace(s[len("ON CONFLICT"):])
	var targetCols []string

	if strings.HasPrefix(rest, "(") {
		closeIdx := strings.Index(rest, ")")
		if closeIdx == -1 {
			return nil, fmt.Errorf("malformed ON CONFLICT target columns: missing closing parenthesis")
		}
		innerCols := rest[1:closeIdx]
		for _, c := range strings.Split(innerCols, ",") {
			cleaned := cleanIdentifier(c)
			if cleaned != "" {
				targetCols = append(targetCols, cleaned)
			}
		}
		rest = strings.TrimSpace(rest[closeIdx+1:])
	} else if strings.HasPrefix(strings.ToUpper(rest), "ON CONSTRAINT") {
		rest = strings.TrimSpace(rest[len("ON CONSTRAINT"):])
		spIdx := strings.IndexAny(rest, " \t\r\n")
		if spIdx != -1 {
			rest = strings.TrimSpace(rest[spIdx:])
		} else {
			rest = ""
		}
	}

	restUpper := strings.ToUpper(rest)
	if !strings.HasPrefix(restUpper, "DO") {
		return nil, fmt.Errorf("expected DO after ON CONFLICT, got %q", rest)
	}
	afterDo := strings.TrimSpace(rest[2:])
	afterDoUpper := strings.ToUpper(afterDo)

	if strings.HasPrefix(afterDoUpper, "NOTHING") {
		return &onConflictClause{
			action:     conflictActionDoNothing,
			targetCols: targetCols,
		}, nil
	}

	if !strings.HasPrefix(afterDoUpper, "UPDATE") {
		return nil, fmt.Errorf("expected NOTHING or UPDATE after DO, got %q", afterDo)
	}

	afterUpdate := strings.TrimSpace(afterDo[6:])
	afterUpdateUpper := strings.ToUpper(afterUpdate)
	if !strings.HasPrefix(afterUpdateUpper, "SET") {
		return nil, fmt.Errorf("expected SET after DO UPDATE, got %q", afterUpdate)
	}

	setBody := strings.TrimSpace(afterUpdate[3:])
	whereCond := ""

	whereIdx := findTopLevelKeyword(setBody, "WHERE")
	if whereIdx != -1 {
		whereCond = strings.TrimSpace(setBody[whereIdx+5:])
		setBody = strings.TrimSpace(setBody[:whereIdx])
	}

	assignmentParts := splitAssignments(setBody)
	var assignments []onConflictAssignment
	for _, part := range assignmentParts {
		eqIdx := strings.Index(part, "=")
		if eqIdx == -1 {
			return nil, fmt.Errorf("invalid assignment in DO UPDATE SET: %q", part)
		}
		col := cleanIdentifier(part[:eqIdx])
		expr := strings.TrimSpace(part[eqIdx+1:])
		assignments = append(assignments, onConflictAssignment{
			column: col,
			expr:   expr,
		})
	}

	return &onConflictClause{
		action:      conflictActionDoUpdate,
		targetCols:  targetCols,
		assignments: assignments,
		whereCond:   whereCond,
	}, nil
}

func splitAssignments(s string) []string {
	var parts []string
	var cur strings.Builder
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
			cur.WriteByte(c)
			continue
		}
		if c == '(' {
			depth++
			cur.WriteByte(c)
			continue
		}
		if c == ')' {
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
			continue
		}
		if c == ',' && depth == 0 {
			if str := strings.TrimSpace(cur.String()); str != "" {
				parts = append(parts, str)
			}
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if str := strings.TrimSpace(cur.String()); str != "" {
		parts = append(parts, str)
	}
	return parts
}

func (s *Coordinator) execInsert(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// RBAC Permission Check
	if m := reDmlInsertCheck.FindStringSubmatch(cleaned); len(m) >= 2 {
		if err := s.checkTableAccess(cs, cleanIdentifier(m[1]), auth.PrivInsert); err != nil {
			return nil, err
		}
	}

	isUpsert := strings.HasPrefix(strings.ToUpper(cleaned), "UPSERT")
	insertSQL := cleaned
	if isUpsert {
		if strings.HasPrefix(strings.ToUpper(cleaned), "UPSERT INTO ") {
			insertSQL = "INSERT INTO " + cleaned[len("UPSERT INTO "):]
		} else if strings.HasPrefix(strings.ToUpper(cleaned), "UPSERT ") {
			insertSQL = "INSERT INTO " + cleaned[len("UPSERT "):]
		}
	}

	// Branch: INSERT INTO ... SELECT
	if selMatches := reDmlInsertSelect.FindStringSubmatch(insertSQL); len(selMatches) >= 5 {
		return s.execInsertSelect(selMatches[1], selMatches[2], selMatches[3], selMatches[4], start, cs)
	}

	// Primary Fast-Path: Unified VDBE Virtual Machine execution (bypass if OUTPUT clause or ON CONFLICT/UPSERT present)
	hasOutput := strings.Contains(strings.ToUpper(cleaned), " OUTPUT ")
	hasConflict := strings.Contains(strings.ToUpper(cleaned), "ON CONFLICT") || isUpsert
	if !hasOutput && !hasConflict {
		if res, err := s.execVBDEDML("INSERT", cleaned, start, cs); err == nil {
			return res, nil
		} else if strings.Contains(err.Error(), "IDENTITY_INSERT") {
			return nil, err
		}
	}

	matches := reDmlInsertValues.FindStringSubmatch(insertSQL)
	if len(matches) < 5 {
		return nil, fmt.Errorf("invalid INSERT INTO syntax")
	}

	tableName := cleanIdentifier(matches[1])
	rawCols := matches[2]
	rawOutput := strings.TrimSpace(matches[3])
	rawValues := matches[4]

	var onConflict *onConflictClause
	valuesPart := rawValues
	if conflictIdx := findOnConflictIndex(rawValues); conflictIdx != -1 {
		conflictStr := strings.TrimSpace(rawValues[conflictIdx:])
		valuesPart = strings.TrimSpace(rawValues[:conflictIdx])
		clause, err := parseOnConflict(conflictStr)
		if err != nil {
			return nil, err
		}
		onConflict = clause
	} else if isUpsert {
		onConflict = &onConflictClause{
			action: conflictActionDoUpdate,
		}
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	var identityCol *storage.Column
	for i := range schema.Columns {
		if schema.Columns[i].Identity {
			identityCol = &schema.Columns[i]
			break
		}
	}

	identityInsertAllowed := false
	if cs != nil {
		cs.Mu.Lock()
		if cur, ok := cs.Vars["identity_insert_table"].(string); ok {
			if strings.EqualFold(cur, tableName) {
				identityInsertAllowed = true
			}
		}
		cs.Mu.Unlock()
	}

	var targetCols []string
	if rawCols != "" {
		for _, c := range strings.Split(rawCols, ",") {
			targetCols = append(targetCols, cleanIdentifier(c))
		}
	} else {
		if identityCol != nil && !identityInsertAllowed {
			for _, c := range schema.Columns {
				if !c.Identity {
					targetCols = append(targetCols, c.Name)
				}
			}
		} else {
			for _, c := range schema.Columns {
				targetCols = append(targetCols, c.Name)
			}
		}
	}

	hasIdentityInTarget := false
	if identityCol != nil {
		for _, c := range targetCols {
			if strings.EqualFold(c, identityCol.Name) {
				hasIdentityInTarget = true
				break
			}
		}
		if hasIdentityInTarget && !identityInsertAllowed {
			return nil, fmt.Errorf("Cannot insert explicit value for identity column in table %q when IDENTITY_INSERT is set to OFF", tableName)
		}
	}

	tupleStrings := parseValueTuples(valuesPart)
	if len(tupleStrings) == 0 {
		return nil, fmt.Errorf("no values supplied in INSERT statement")
	}

	var tx storage.Transaction
	var isEphemeral bool
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
		isEphemeral = false
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		t, err := session.BeginTx(cs.TxOptions())
		if err != nil {
			return nil, err
		}
		tx = t
		isEphemeral = true
	}

	var maxID int64 = 0
	if identityCol != nil {
		if lastID, ok := s.GetTableLastIdentity(schema.Name); ok {
			maxID = lastID
		} else {
			if cur, err := tx.Scan(tableName); err == nil {
				for cur.Next() {
					if r, err := cur.Record(); err == nil {
						if v, ok := r[identityCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull && v.IVal > maxID {
							maxID = v.IVal
						}
					}
				}
				cur.Close()
			}
			s.SetTableLastIdentity(schema.Name, maxID)
		}
	}

	var outputProjs []outputProj
	if rawOutput != "" {
		outputProjs = parseOutputClause(rawOutput)
	}
	var outputRows []map[string]any
	var outputCols []string

	insertedCount := int64(0)
	for _, tupleStr := range tupleStrings {
		vals := splitValues(tupleStr)
		if len(vals) != len(targetCols) {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("column count mismatch: expected %d values, got %d in tuple (%s)", len(targetCols), len(vals), tupleStr)
		}

		rec := make(storage.Record)
		for i, colName := range targetCols {
			colSchema, _, found := schema.FindColumn(colName)
			valStr := strings.TrimSpace(vals[i])
			if !found {
				rec[colName] = parseLiteralValue(valStr, storage.TypeVarChar)
			} else {
				canonicalName := colSchema.Name
				if strings.EqualFold(valStr, "DEFAULT") && colSchema.Default != "" {
					rec[canonicalName] = parseLiteralValue(colSchema.Default, colSchema.Type)
				} else {
					rec[canonicalName] = parseLiteralValue(valStr, colSchema.Type)
				}
			}
		}

		// Apply column default values for columns not provided in targetCols
		for _, col := range schema.Columns {
			if _, exists := rec[col.Name]; !exists && col.Default != "" {
				rec[col.Name] = parseLiteralValue(col.Default, col.Type)
			}
		}

		if identityCol != nil {
			if !hasIdentityInTarget {
				maxID++
				rec[identityCol.Name] = storage.IntValue(maxID)
				if cs != nil {
					cs.Mu.Lock()
					cs.Vars["identity"] = maxID
					cs.Vars["scope_identity"] = maxID
					cs.Mu.Unlock()
				}
				s.SetTableLastIdentity(schema.Name, maxID)
			} else {
				if v, ok := rec[identityCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull {
					if cs != nil {
						cs.Mu.Lock()
						cs.Vars["identity"] = v.IVal
						cs.Vars["scope_identity"] = v.IVal
						cs.Mu.Unlock()
					}
					s.SetTableLastIdentity(schema.Name, v.IVal)
					if v.IVal > maxID {
						maxID = v.IVal
					}
				}
			}
		}

		if onConflict != nil {
			var conflictCols []string
			if len(onConflict.targetCols) > 0 {
				for _, tc := range onConflict.targetCols {
					if colDef, _, found := schema.FindColumn(tc); found && colDef != nil {
						conflictCols = append(conflictCols, colDef.Name)
					} else {
						conflictCols = append(conflictCols, tc)
					}
				}
			} else if pkCol, err := schema.PrimaryKeyColumn(); err == nil && pkCol != nil {
				conflictCols = []string{pkCol.Name}
			}

			var existingRec storage.Record
			var existingPk storage.Value
			pkCol, _ := schema.PrimaryKeyColumn()

			if len(conflictCols) == 1 && pkCol != nil && strings.EqualFold(conflictCols[0], pkCol.Name) {
				if targetVal, ok := rec.Get(pkCol.Name); ok {
					if ex, err := tx.Get(tableName, targetVal); err == nil && ex != nil {
						existingRec = ex
						existingPk = targetVal
					}
				}
			}

			if existingRec == nil && len(conflictCols) > 0 {
				if cur, err := tx.Scan(tableName); err == nil {
					for cur.Next() {
						r, err := cur.Record()
						if err != nil {
							continue
						}
						matched := true
						for _, cc := range conflictCols {
							rv, ok1 := r.Get(cc)
							iv, ok2 := rec.Get(cc)
							if !ok1 || !ok2 || rv.Compare(iv) != 0 {
								matched = false
								break
							}
						}
						if matched {
							existingRec = r
							if pkCol != nil {
								existingPk, _ = r.Get(pkCol.Name)
							}
							break
						}
					}
					cur.Close()
				}
			}

			if existingRec != nil {
				if onConflict.action == conflictActionDoNothing {
					continue
				} else if onConflict.action == conflictActionDoUpdate {
					evalMap := make(map[string]any)
					for k, v := range existingRec {
						val := formatStorageValue(v)
						evalMap[k] = val
						evalMap[strings.ToLower(k)] = val
						evalMap[tableName+"."+k] = val
						evalMap[strings.ToLower(tableName)+"."+strings.ToLower(k)] = val
					}
					for k, v := range rec {
						val := formatStorageValue(v)
						evalMap["EXCLUDED."+k] = val
						evalMap["excluded."+strings.ToLower(k)] = val
						evalMap["EXCLUDED."+strings.ToLower(k)] = val
						evalMap["excluded."+k] = val
					}

					if onConflict.whereCond != "" {
						if !evaluateWhereConditionMap(evalMap, onConflict.whereCond) {
							continue
						}
					}

					updatedRec := existingRec.Clone()
					assignments := onConflict.assignments
					if len(assignments) == 0 {
						// CockroachDB / SQLion UPSERT default: update all non-PK columns
						for _, c := range schema.Columns {
							if c.PrimaryKey {
								continue
							}
							if newVal, ok := rec.Get(c.Name); ok {
								updatedRec.Set(c.Name, newVal)
							}
						}
					} else {
						for _, assign := range assignments {
							colSchema, _, found := schema.FindColumn(assign.column)
							if !found {
								if isEphemeral {
									_ = tx.Rollback()
								}
								return nil, fmt.Errorf("column %q not found in table %q", assign.column, tableName)
							}
							val := evalMergeExpr(assign.expr, evalMap, colSchema.Type)
							updatedRec[colSchema.Name] = val
						}
					}

					if err := tx.Update(tableName, existingPk, updatedRec); err != nil {
						if isEphemeral {
							_ = tx.Rollback()
						}
						return nil, fmt.Errorf("update row error in ON CONFLICT: %w", err)
					}
					if len(outputProjs) > 0 {
						outRow, cols := evaluateOutputRow(outputProjs, existingRec, updatedRec, schema)
						outputRows = append(outputRows, outRow)
						outputCols = cols
					}
					insertedCount++
					continue
				}
			}
		}

		if err := tx.Insert(tableName, rec); err != nil {
			if onConflict != nil && onConflict.action == conflictActionDoNothing {
				errLower := strings.ToLower(err.Error())
				if errors.Is(err, storage.ErrKeyExists) || strings.Contains(errLower, "already exists") || strings.Contains(errLower, "unique") {
					continue
				}
			}
			if isEphemeral {
				_ = tx.Rollback()
			}
			if errors.Is(err, storage.ErrKeyExists) || errors.Is(err, storage.ErrUniqueViolation) {
				return nil, fmt.Errorf("duplicate key value violates unique constraint on %q: %w", tableName, err)
			}
			return nil, fmt.Errorf("insert row error: %w", err)
		}

		if len(outputProjs) > 0 {
			outRow, cols := evaluateOutputRow(outputProjs, nil, rec, schema)
			outputRows = append(outputRows, outRow)
			outputCols = cols
		}
		insertedCount++
	}

	if isEphemeral {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("transaction commit error: %w", err)
		}
	}

	cmdName := "INSERT"
	if isUpsert {
		cmdName = "UPSERT"
	}

	return &QueryResult{
		Success:      true,
		Command:      cmdName,
		Columns:      outputCols,
		Rows:         outputRows,
		RowsAffected: insertedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows affected into %q", insertedCount, tableName)},
	}, nil
}

func (s *Coordinator) execInsertSelect(rawTable, rawCols, rawOutput, rawSelect string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	tableName := cleanIdentifier(rawTable)
	selectPart := strings.TrimSpace(rawSelect)
	if strings.HasPrefix(selectPart, "(") && strings.HasSuffix(selectPart, ")") {
		selectPart = strings.TrimSpace(selectPart[1 : len(selectPart)-1])
	}

	var onConflict *onConflictClause
	if conflictIdx := findOnConflictIndex(selectPart); conflictIdx != -1 {
		conflictStr := strings.TrimSpace(selectPart[conflictIdx:])
		selectPart = strings.TrimSpace(selectPart[:conflictIdx])
		clause, err := parseOnConflict(conflictStr)
		if err != nil {
			return nil, err
		}
		onConflict = clause
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	var identityCol *storage.Column
	for i := range schema.Columns {
		if schema.Columns[i].Identity {
			identityCol = &schema.Columns[i]
			break
		}
	}

	identityInsertAllowed := false
	if cs != nil {
		cs.Mu.Lock()
		if cur, ok := cs.Vars["identity_insert_table"].(string); ok {
			if strings.EqualFold(cur, tableName) {
				identityInsertAllowed = true
			}
		}
		cs.Mu.Unlock()
	}

	var targetCols []string
	if rawCols != "" {
		for _, c := range strings.Split(rawCols, ",") {
			cleanedCol := cleanIdentifier(c)
			if colSchema, _, found := schema.FindColumn(cleanedCol); found {
				targetCols = append(targetCols, colSchema.Name)
			} else {
				return nil, fmt.Errorf("invalid column name %q in table %q", cleanedCol, tableName)
			}
		}
	} else {
		if identityCol != nil && !identityInsertAllowed {
			for _, c := range schema.Columns {
				if !c.Identity {
					targetCols = append(targetCols, c.Name)
				}
			}
		} else {
			for _, c := range schema.Columns {
				targetCols = append(targetCols, c.Name)
			}
		}
	}

	hasIdentityInTarget := false
	if identityCol != nil {
		for _, c := range targetCols {
			if strings.EqualFold(c, identityCol.Name) {
				hasIdentityInTarget = true
				break
			}
		}
		if hasIdentityInTarget && !identityInsertAllowed {
			return nil, fmt.Errorf("Cannot insert explicit value for identity column in table %q when IDENTITY_INSERT is set to OFF", tableName)
		}
	}

	// 1. Execute the SELECT query
	selRes, err := s.execSelect(selectPart, start, cs)
	if err != nil {
		return nil, err
	}
	if !selRes.Success {
		return selRes, nil
	}

	// 2. Validate projected column count matches target column count
	if len(selRes.Columns) != len(targetCols) {
		return nil, fmt.Errorf("column count mismatch: target table %q expects %d columns, but SELECT produced %d columns", tableName, len(targetCols), len(selRes.Columns))
	}

	// 3. Begin Transaction if not already in session transaction
	var tx storage.Transaction
	var isEphemeral bool
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
		isEphemeral = false
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		t, err := session.BeginTx(cs.TxOptions())
		if err != nil {
			return nil, err
		}
		tx = t
		isEphemeral = true
	}

	var maxID int64 = 0
	if identityCol != nil {
		if lastID, ok := s.GetTableLastIdentity(schema.Name); ok {
			maxID = lastID
		} else {
			if cur, err := tx.Scan(tableName); err == nil {
				for cur.Next() {
					if r, err := cur.Record(); err == nil {
						if v, ok := r[identityCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull && v.IVal > maxID {
							maxID = v.IVal
						}
					}
				}
				cur.Close()
			}
			s.SetTableLastIdentity(schema.Name, maxID)
		}
	}

	var outputProjs []outputProj
	rawOutput = strings.TrimSpace(rawOutput)
	if rawOutput != "" {
		outputProjs = parseOutputClause(rawOutput)
	}
	var outputRows []map[string]any
	var outputCols []string

	insertedCount := int64(0)
	for _, row := range selRes.Rows {
		rec := make(storage.Record, len(schema.Columns))
		for i, colName := range targetCols {
			colSchema, _, _ := schema.FindColumn(colName)
			srcCol := selRes.Columns[i]
			val := row[srcCol]
			rec[colName] = ConvertValue(val, colSchema.Type)
		}

		// Apply column default values for columns not provided in targetCols
		for _, col := range schema.Columns {
			if _, exists := rec[col.Name]; !exists && col.Default != "" {
				rec[col.Name] = parseLiteralValue(col.Default, col.Type)
			}
		}

		if identityCol != nil {
			if !hasIdentityInTarget {
				maxID++
				rec[identityCol.Name] = storage.IntValue(maxID)
				if cs != nil {
					cs.Mu.Lock()
					cs.Vars["identity"] = maxID
					cs.Vars["scope_identity"] = maxID
					cs.Mu.Unlock()
				}
				s.SetTableLastIdentity(schema.Name, maxID)
			} else {
				if v, ok := rec[identityCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull {
					if cs != nil {
						cs.Mu.Lock()
						cs.Vars["identity"] = v.IVal
						cs.Vars["scope_identity"] = v.IVal
						cs.Mu.Unlock()
					}
					s.SetTableLastIdentity(schema.Name, v.IVal)
					if v.IVal > maxID {
						maxID = v.IVal
					}
				}
			}
		}

		if onConflict != nil {
			var conflictCols []string
			if len(onConflict.targetCols) > 0 {
				for _, tc := range onConflict.targetCols {
					if colDef, _, found := schema.FindColumn(tc); found && colDef != nil {
						conflictCols = append(conflictCols, colDef.Name)
					} else {
						conflictCols = append(conflictCols, tc)
					}
				}
			} else if pkCol, err := schema.PrimaryKeyColumn(); err == nil && pkCol != nil {
				conflictCols = []string{pkCol.Name}
			}

			var existingRec storage.Record
			var existingPk storage.Value
			pkCol, _ := schema.PrimaryKeyColumn()

			if len(conflictCols) == 1 && pkCol != nil && strings.EqualFold(conflictCols[0], pkCol.Name) {
				if targetVal, ok := rec.Get(pkCol.Name); ok {
					if ex, err := tx.Get(tableName, targetVal); err == nil && ex != nil {
						existingRec = ex
						existingPk = targetVal
					}
				}
			}

			if existingRec == nil && len(conflictCols) > 0 {
				if cur, err := tx.Scan(tableName); err == nil {
					for cur.Next() {
						r, err := cur.Record()
						if err != nil {
							continue
						}
						matched := true
						for _, cc := range conflictCols {
							rv, ok1 := r.Get(cc)
							iv, ok2 := rec.Get(cc)
							if !ok1 || !ok2 || rv.Compare(iv) != 0 {
								matched = false
								break
							}
						}
						if matched {
							existingRec = r
							if pkCol != nil {
								existingPk, _ = r.Get(pkCol.Name)
							}
							break
						}
					}
					cur.Close()
				}
			}

			if existingRec != nil {
				if onConflict.action == conflictActionDoNothing {
					continue
				} else if onConflict.action == conflictActionDoUpdate {
					evalMap := make(map[string]any)
					for k, v := range existingRec {
						val := formatStorageValue(v)
						evalMap[k] = val
						evalMap[strings.ToLower(k)] = val
						evalMap[tableName+"."+k] = val
						evalMap[strings.ToLower(tableName)+"."+strings.ToLower(k)] = val
					}
					for k, v := range rec {
						val := formatStorageValue(v)
						evalMap["EXCLUDED."+k] = val
						evalMap["excluded."+strings.ToLower(k)] = val
						evalMap["EXCLUDED."+strings.ToLower(k)] = val
						evalMap["excluded."+k] = val
					}

					if onConflict.whereCond != "" {
						if !evaluateWhereConditionMap(evalMap, onConflict.whereCond) {
							continue
						}
					}

					updatedRec := existingRec.Clone()
					assignments := onConflict.assignments
					if len(assignments) == 0 {
						for _, c := range schema.Columns {
							if c.PrimaryKey {
								continue
							}
							if newVal, ok := rec.Get(c.Name); ok {
								updatedRec.Set(c.Name, newVal)
							}
						}
					} else {
						for _, assign := range assignments {
							colSchema, _, found := schema.FindColumn(assign.column)
							if !found {
								if isEphemeral {
									_ = tx.Rollback()
								}
								return nil, fmt.Errorf("column %q not found in table %q", assign.column, tableName)
							}
							val := evalMergeExpr(assign.expr, evalMap, colSchema.Type)
							updatedRec[colSchema.Name] = val
						}
					}

					if err := tx.Update(tableName, existingPk, updatedRec); err != nil {
						if isEphemeral {
							_ = tx.Rollback()
						}
						return nil, fmt.Errorf("update row error in ON CONFLICT: %w", err)
					}
					if len(outputProjs) > 0 {
						outRow, cols := evaluateOutputRow(outputProjs, existingRec, updatedRec, schema)
						outputRows = append(outputRows, outRow)
						outputCols = cols
					}
					insertedCount++
					continue
				}
			}
		}

		if err := tx.Insert(tableName, rec); err != nil {
			if onConflict != nil && onConflict.action == conflictActionDoNothing {
				errLower := strings.ToLower(err.Error())
				if errors.Is(err, storage.ErrKeyExists) || strings.Contains(errLower, "already exists") || strings.Contains(errLower, "unique") {
					continue
				}
			}
			if isEphemeral {
				_ = tx.Rollback()
			}
			if errors.Is(err, storage.ErrKeyExists) || errors.Is(err, storage.ErrUniqueViolation) {
				return nil, fmt.Errorf("duplicate key value violates unique constraint on %q: %w", tableName, err)
			}
			return nil, fmt.Errorf("insert row error on %q: %w", tableName, err)
		}

		if len(outputProjs) > 0 {
			outRow, cols := evaluateOutputRow(outputProjs, nil, rec, schema)
			outputRows = append(outputRows, outRow)
			outputCols = cols
		}
		insertedCount++
	}

	if isEphemeral {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("transaction commit error: %w", err)
		}
	}

	if identityCol != nil && cs != nil {
		cs.Mu.Lock()
		if cs.Vars == nil {
			cs.Vars = make(map[string]any)
		}
		cs.Vars["identity"] = maxID
		cs.Mu.Unlock()
	}

	return &QueryResult{
		Success:      true,
		Command:      "INSERT",
		Columns:      outputCols,
		Rows:         outputRows,
		RowsAffected: insertedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows affected into %q", insertedCount, tableName)},
	}, nil
}

func (s *Coordinator) execUpdate(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// RBAC Permission Check
	if m := reDmlUpdateCheck.FindStringSubmatch(cleaned); len(m) >= 2 {
		if err := s.checkTableAccess(cs, cleanIdentifier(m[1]), auth.PrivUpdate); err != nil {
			return nil, err
		}
	}

	// Primary Fast-Path: Unified VDBE Virtual Machine execution (bypass if OUTPUT clause present)
	hasOutput := strings.Contains(strings.ToUpper(cleaned), " OUTPUT ")
	if !hasOutput {
		if res, err := s.execVBDEDML("UPDATE", cleaned, start, cs); err == nil {
			return res, nil
		}
	}
	matches := reDmlUpdate.FindStringSubmatch(cleaned)
	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid UPDATE syntax")
	}

	tableName := cleanIdentifier(matches[1])
	setClause := matches[2]
	rawOutput := ""
	whereClause := ""
	if len(matches) > 3 {
		rawOutput = strings.TrimSpace(matches[3])
	}
	if len(matches) > 4 {
		whereClause = strings.TrimSpace(matches[4])
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	var tx storage.Transaction
	var isEphemeral bool
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
		isEphemeral = false
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		t, err := session.BeginTx(cs.TxOptions())
		if err != nil {
			return nil, err
		}
		tx = t
		isEphemeral = true
	}

	cursor, err := tx.Scan(tableName)
	if err != nil {
		if isEphemeral {
			_ = tx.Rollback()
		}
		return nil, err
	}
	defer cursor.Close()

	setPairs := strings.Split(setClause, ",")
	updatesMap := make(map[string]string)
	for _, pair := range setPairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			updatesMap[cleanIdentifier(kv[0])] = strings.TrimSpace(kv[1])
		}
	}

	type updateEntry struct {
		pk   storage.Value
		rec  storage.Record
		orig storage.Record
	}
	toUpdate := make([]updateEntry, 0)

	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			break
		}

		if whereClause != "" && !evaluateWhereCondition(rec, whereClause) {
			continue
		}

		pkVal, hasPK := schema.ResolveRecordPK(rec)
		if !hasPK {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("table %q has no primary key to identify row for update", tableName)
		}

		recUpdates := make(storage.Record)
		for colName, exprStr := range updatesMap {
			colSchema, _, found := schema.FindColumn(colName)
			targetType := storage.TypeVarChar
			targetCol := colName
			if found {
				targetCol = colSchema.Name
				targetType = colSchema.Type
			}
			recUpdates[targetCol] = evalUpdateExpr(exprStr, rec, targetType)
		}

		if hasPK {
			toUpdate = append(toUpdate, updateEntry{pk: pkVal, rec: recUpdates, orig: rec.Clone()})
		}
	}

	var outputProjs []outputProj
	if rawOutput != "" {
		outputProjs = parseOutputClause(rawOutput)
	}
	var outputRows []map[string]any
	var outputCols []string

	updatedCount := int64(0)
	for _, item := range toUpdate {
		if err := tx.Update(tableName, item.pk, item.rec); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("update row error: %w", err)
		}

		if len(outputProjs) > 0 {
			newRec := make(storage.Record, len(item.orig))
			for k, v := range item.orig {
				newRec[k] = v
			}
			for k, v := range item.rec {
				newRec[k] = v
			}
			outRow, cols := evaluateOutputRow(outputProjs, item.orig, newRec, schema)
			outputRows = append(outputRows, outRow)
			outputCols = cols
		}
		updatedCount++
	}

	if isEphemeral {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit error: %w", err)
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "UPDATE",
		Columns:      outputCols,
		Rows:         outputRows,
		RowsAffected: updatedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows updated in %q", updatedCount, tableName)},
	}, nil
}

func (s *Coordinator) execDelete(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// RBAC Permission Check
	if m := reDmlDeleteCheck.FindStringSubmatch(cleaned); len(m) >= 2 {
		if err := s.checkTableAccess(cs, cleanIdentifier(m[1]), auth.PrivDelete); err != nil {
			return nil, err
		}
	}

	// Primary Fast-Path: Unified VDBE Virtual Machine execution (bypass if OUTPUT clause present)
	hasOutput := strings.Contains(strings.ToUpper(cleaned), " OUTPUT ")
	if !hasOutput {
		if res, err := s.execVBDEDML("DELETE", cleaned, start, cs); err == nil {
			return res, nil
		}
	}
	matches := reDmlDelete.FindStringSubmatch(cleaned)
	if len(matches) < 2 {
		return nil, fmt.Errorf("invalid DELETE syntax")
	}

	tableName := cleanIdentifier(matches[1])
	rawOutput := ""
	whereClause := ""
	if len(matches) > 2 {
		rawOutput = strings.TrimSpace(matches[2])
	}
	if len(matches) > 3 {
		whereClause = strings.TrimSpace(matches[3])
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	var tx storage.Transaction
	var isEphemeral bool
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
		isEphemeral = false
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		t, err := session.BeginTx(cs.TxOptions())
		if err != nil {
			return nil, err
		}
		tx = t
		isEphemeral = true
	}

	cursor, err := tx.Scan(tableName)
	if err != nil {
		if isEphemeral {
			_ = tx.Rollback()
		}
		return nil, err
	}
	defer cursor.Close()

	type deleteEntry struct {
		pk   storage.Value
		orig storage.Record
	}
	toDelete := make([]deleteEntry, 0)
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			break
		}

		if whereClause != "" && !evaluateWhereCondition(rec, whereClause) {
			continue
		}

		pkVal, hasPK := schema.ResolveRecordPK(rec)
		if !hasPK {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("table %q has no primary key to identify row for update", tableName)
		}

		if hasPK {
			toDelete = append(toDelete, deleteEntry{pk: pkVal, orig: rec.Clone()})
		}
	}

	var outputProjs []outputProj
	if rawOutput != "" {
		outputProjs = parseOutputClause(rawOutput)
	}
	var outputRows []map[string]any
	var outputCols []string

	deletedCount := int64(0)
	for _, item := range toDelete {
		if err := tx.Delete(tableName, item.pk); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("delete error: %w", err)
		}

		if len(outputProjs) > 0 {
			outRow, cols := evaluateOutputRow(outputProjs, item.orig, nil, schema)
			outputRows = append(outputRows, outRow)
			outputCols = cols
		}
		deletedCount++
	}

	if isEphemeral {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit error: %w", err)
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "DELETE",
		Columns:      outputCols,
		Rows:         outputRows,
		RowsAffected: deletedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows deleted from %q", deletedCount, tableName)},
	}, nil
}
