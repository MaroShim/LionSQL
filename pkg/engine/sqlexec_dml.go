package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lionsql/pkg/auth"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

func (s *Coordinator) fireVolcanoTriggers(tx storage.Transaction, cs *ClientSession, tableName, event string, insertedRecs, deletedRecs []storage.Record) error {
	if s.executor == nil || !s.executor.HasTriggers(tableName, event) {
		return nil
	}
	var sess storage.Session
	if cs != nil {
		sess = cs.Session
	}
	return s.executor.FireRecordTriggers(context.Background(), tx, sess, tableName, event, insertedRecs, deletedRecs)
}

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
		for _, c := range sqlutil.SplitTopLevel(innerCols, ',') {
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

	whereIdx := sqlutil.FindTopLevelKeyword(setBody, "WHERE")
	if whereIdx != -1 {
		whereCond = strings.TrimSpace(setBody[whereIdx+5:])
		setBody = strings.TrimSpace(setBody[:whereIdx])
	}

	assignmentParts := splitAssignments(setBody)
	var assignments []onConflictAssignment
	for _, part := range assignmentParts {
		eqIdx := sqlutil.FindTopLevelChar(part, '=')
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

func (s *Coordinator) routeOutputRowDirect(tx storage.Transaction, outInfo outputClauseInfo, oldRec, newRec storage.Record, schema *storage.TableSchema, cs *ClientSession, outputRows *[]map[string]any, outputVals *[][]any) error {
	if len(outInfo.Projs) == 0 {
		return nil
	}
	if outInfo.IntoTable != "" {
		targetTable := outInfo.IntoTable
		if cs != nil && cs.TableVars != nil {
			if phys, ok := cs.TableVars[strings.ToLower(targetTable)]; ok {
				targetTable = phys
			} else if phys, ok := cs.TableVars["@"+strings.ToLower(targetTable)]; ok {
				targetTable = phys
			}
		}
		targetSchema, err := s.engine.GetTable(targetTable)
		if err != nil || targetSchema == nil {
			return fmt.Errorf("target table %q in OUTPUT INTO not found: %w", outInfo.IntoTable, err)
		}

		rec, cols := evaluateOutputRecord(outInfo.Projs, oldRec, newRec, schema)
		targetRec := make(storage.Record)
		if len(outInfo.IntoCols) > 0 {
			for i, colName := range outInfo.IntoCols {
				if i < len(cols) {
					targetRec[colName] = rec[cols[i]]
				}
			}
		} else {
			var targetCols []storage.Column
			for _, c := range targetSchema.Columns {
				if !c.Hidden {
					targetCols = append(targetCols, c)
				}
			}
			for i, col := range targetCols {
				if i < len(cols) {
					targetRec[col.Name] = rec[cols[i]]
				}
			}
		}

		for _, col := range targetSchema.Columns {
			if val, exists := targetRec.Get(col.Name); exists {
				if !val.IsNull && val.Type != col.Type {
					targetRec[col.Name] = proc.ParseLiteralDefault(val.String(), col.Type)
				}
			} else {
				if col.Default != "" {
					targetRec[col.Name] = proc.ParseLiteralDefault(col.Default, col.Type)
				} else if col.Identity {
					seed := col.GetIdentitySeed()
					step := col.GetIdentityIncrement()
					nextID := s.AllocTableIdentityWithStep(targetTable, 1, seed, step, func() (int64, bool) {
						var maxID int64 = 0
						hasRows := false
						if cur, err := tx.Scan(targetTable); err == nil {
							for cur.Next() {
								if r, err := cur.Record(); err == nil {
									if v, ok := r.Get(col.Name); ok && v.Type == storage.TypeInt && !v.IsNull {
										if !hasRows || v.IVal > maxID {
											maxID = v.IVal
										}
										hasRows = true
									}
								}
							}
							cur.Close()
						}
						return maxID, hasRows
					})
					targetRec[col.Name] = storage.IntValue(nextID)
				}
			}
		}
		return tx.Insert(targetTable, targetRec)
	}

	appendOutputRow(outInfo.Projs, oldRec, newRec, schema, outputRows, outputVals)
	return nil
}

func (s *Coordinator) execInsert(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// RBAC Permission Check
	dml, parseErr := ParseDMLCommand(cleaned)
	if dml != nil {
		if err := s.checkTableAccess(cs, cleanIdentifier(dml.Table), auth.PrivInsert); err != nil {
			return nil, err
		}
	}

	isUpsert := dml != nil && dml.Upsert

	// Branch: INSERT INTO ... SELECT
	if dml != nil && dml.Select != "" {
		return s.execInsertSelect(dml.Table, dml.Columns, dml.Output, dml.Select, start, cs)
	}

	// Primary Fast-Path: Unified VDBE Virtual Machine execution (bypass if OUTPUT/RETURNING clause or ON CONFLICT/UPSERT present)
	hasOutput := (dml != nil && dml.Output != "") || sqlutil.ContainsKeyword(cleaned, "OUTPUT", false) || sqlutil.ContainsKeyword(cleaned, "RETURNING", false)
	hasConflict := sqlutil.ContainsKeyword(cleaned, "ON CONFLICT", false) || isUpsert
	if !hasOutput && !hasConflict {
		if res, err := s.execVBDEDML("INSERT", cleaned, start, cs); err == nil {
			return res, nil
		} else if strings.Contains(err.Error(), "IDENTITY_INSERT") {
			return nil, err
		} else if dml != nil && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(dml.Values)), "EXEC") {
			return nil, err
		}
	}

	if parseErr != nil || dml == nil {
		return nil, fmt.Errorf("invalid INSERT INTO syntax")
	}

	tableName := cleanIdentifier(dml.Table)
	rawCols := dml.Columns
	rawOutput := dml.Output
	rawValues := dml.Values

	var onConflict *onConflictClause
	valuesPart := rawValues
	if conflictIdx := sqlutil.FindTopLevelKeyword(rawValues, "ON CONFLICT"); conflictIdx != -1 {
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
		if cs.IsPostgres() {
			identityInsertAllowed = true
		}
		cs.Mu.Unlock()
	}

	var targetCols []string
	if rawCols != "" {
		for _, c := range sqlutil.SplitTopLevel(rawCols, ',') {
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

	tupleStrings := sqlutil.ExtractTopLevelTuples(valuesPart)
	if len(tupleStrings) == 0 {
		return nil, fmt.Errorf("no values supplied in INSERT statement")
	}

	var tx storage.Transaction
	var isEphemeral bool
	if cs != nil && cs.Tx() != nil {
		tx = wrapSessionTx(cs, cs.Tx())
		isEphemeral = false
	} else {
		session, closeFn, err := s.acquireSessionForQuery(cs)
		if err != nil {
			return nil, err
		}
		defer closeFn()

		t, err := session.BeginTx(cs.TxOptions())
		if err != nil {
			return nil, err
		}
		tx = wrapSessionTx(cs, t)
		isEphemeral = true
		defer tx.Rollback()
	}

	var nextAllocID int64 = 0
	if identityCol != nil && !hasIdentityInTarget && len(tupleStrings) > 0 {
		count := int64(len(tupleStrings))
		seed := identityCol.GetIdentitySeed()
		step := identityCol.GetIdentityIncrement()
		nextAllocID = s.AllocTableIdentityWithStep(schema.Name, count, seed, step, func() (int64, bool) {
			var scanMax int64 = 0
			hasRows := false
			if cur, err := tx.Scan(tableName); err == nil {
				for cur.Next() {
					if r, err := cur.Record(); err == nil {
						if v, ok := r[identityCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull {
							if !hasRows || v.IVal > scanMax {
								scanMax = v.IVal
							}
							hasRows = true
						}
					}
				}
				cur.Close()
			}
			return scanMax, hasRows
		})
	}

	var outInfo outputClauseInfo
	if rawOutput != "" {
		outInfo = parseOutputClauseInfo(rawOutput)
	}
	outputProjs := outInfo.Projs
	var outputRows []map[string]any
	var outputVals [][]any
	var outputCols []string
	if len(outputProjs) > 0 && outInfo.IntoTable == "" {
		_, outputCols = evaluateOutputRow(outputProjs, nil, nil, schema)
	}

	hasInsTriggers := s.executor != nil && s.executor.HasTriggers(tableName, "INSERT")
	hasUpdTriggers := s.executor != nil && s.executor.HasTriggers(tableName, "UPDATE")
	var trigInsertedRecs []storage.Record
	var trigUpdInserted []storage.Record
	var trigUpdDeleted []storage.Record

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

			// Resolve scalar expressions such as NEXT VALUE FOR <seq> or @@TRANCOUNT
			evalVal := func(sVal string, targetType storage.DataType) (storage.Value, error) {
				if strings.EqualFold(sVal, "@@TRANCOUNT") {
					if cs != nil {
						tc := int64(cs.TranCount())
						return storage.IntValue(tc), nil
					}
					return storage.IntValue(0), nil
				}
				if sqlutil.HasPrefixKeyword(sVal, "NEXT VALUE FOR") {
					val, _ := s.evalScalarExprCore(sVal, cs)
					if err, isErr := val.(error); isErr {
						return storage.Value{}, err
					}
					if n, ok := val.(int64); ok {
						return storage.IntValue(n), nil
					}
				}
				if val, _ := s.evalScalarExprCore(sVal, cs); val != nil {
					if n, ok := val.(int64); ok {
						return storage.IntValue(n), nil
					}
					if n, ok := val.(int); ok {
						return storage.IntValue(int64(n)), nil
					}
				}
				return parseLiteralValue(sVal, targetType), nil
			}

			if !found {
				if strings.EqualFold(valStr, "DEFAULT") {
					rec[colName] = storage.NullValue(storage.TypeVarChar)
				} else {
					v, err := evalVal(valStr, storage.TypeVarChar)
					if err != nil {
						if isEphemeral {
							_ = tx.Rollback()
						}
						return nil, err
					}
					rec[colName] = v
				}
			} else {
				canonicalName := colSchema.Name
				if strings.EqualFold(valStr, "DEFAULT") {
					if colSchema.Default != "" {
						v, err := evalVal(colSchema.Default, colSchema.Type)
						if err != nil {
							if isEphemeral {
								_ = tx.Rollback()
							}
							return nil, err
						}
						rec[canonicalName] = v
					} else {
						rec[canonicalName] = storage.NullValue(colSchema.Type)
					}
				} else {
					v, err := evalVal(valStr, colSchema.Type)
					if err != nil {
						if isEphemeral {
							_ = tx.Rollback()
						}
						return nil, err
					}
					rec[canonicalName] = v
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
				curID := nextAllocID
				step := identityCol.GetIdentityIncrement()
				nextAllocID += step
				rec[identityCol.Name] = storage.IntValue(curID)
				if cs != nil {
					cs.Mu.Lock()
					cs.Vars["identity"] = curID
					cs.Vars["scope_identity"] = curID
					cs.Mu.Unlock()
				}
				s.SetTableLastIdentity(schema.Name, curID)
				s.RecordLastIdentity(curID, curID)
			} else {
				if v, ok := rec[identityCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull {
					if cs != nil {
						cs.Mu.Lock()
						cs.Vars["identity"] = v.IVal
						cs.Vars["scope_identity"] = v.IVal
						cs.Mu.Unlock()
					}
					s.SetTableLastIdentity(schema.Name, v.IVal)
					s.RecordLastIdentity(v.IVal, v.IVal)
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
			} else if schema.IsCompositePK() {
				for _, c := range schema.Columns {
					if c.PrimaryKey {
						conflictCols = append(conflictCols, c.Name)
					}
				}
			} else if pkCol, err := schema.PrimaryKeyColumn(); err == nil && pkCol != nil {
				conflictCols = []string{pkCol.Name}
			}

			var existingRec storage.Record
			var existingPk storage.Value
			pkCol, _ := schema.PrimaryKeyColumn()

			if schema.IsCompositePK() {
				if pkVal, ok := schema.ResolveRecordPK(rec); ok {
					if ex, err := tx.Get(tableName, pkVal); err == nil && ex != nil {
						existingRec = ex
						existingPk = pkVal
					}
				}
			} else if len(conflictCols) == 1 && pkCol != nil && strings.EqualFold(conflictCols[0], pkCol.Name) {
				if targetVal, ok := rec.Get(pkCol.Name); ok {
					if ex, err := tx.Get(tableName, targetVal); err == nil && ex != nil {
						existingRec = ex
						existingPk = targetVal
					}
				}
			}

			if existingRec == nil && len(conflictCols) > 0 {
				var matchingIdx *storage.IndexSchema
				for _, idx := range schema.Indexes {
					if len(idx.Columns) == len(conflictCols) {
						allMatch := true
						for ci, colName := range conflictCols {
							if !strings.EqualFold(idx.Columns[ci], colName) {
								allMatch = false
								break
							}
						}
						if allMatch {
							matchingIdx = &idx
							break
						}
					}
				}

				if matchingIdx != nil && len(conflictCols) == 1 {
					colName := conflictCols[0]
					if targetVal, ok := rec.Get(colName); ok {
						if cur, err := tx.IndexScan(tableName, matchingIdx.Name, &targetVal, &targetVal); err == nil {
							for cur.Next() {
								if r, err := cur.Record(); err == nil {
									existingRec = r
									if pkVal, ok := schema.ResolveRecordPK(r); ok {
										existingPk = pkVal
									} else if pkCol != nil {
										existingPk, _ = r.Get(pkCol.Name)
									}
									break
								}
							}
							cur.Close()
						}
					}
				}

				if existingRec == nil {
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
								if pkVal, ok := schema.ResolveRecordPK(r); ok {
									existingPk = pkVal
								} else if pkCol != nil {
									existingPk, _ = r.Get(pkCol.Name)
								}
								break
							}
						}
						cur.Close()
					}
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
						// CockroachDB / LionSQL UPSERT default: update all non-PK columns
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

					if err := proc.ValidateRecordChecks(schema, updatedRec); err != nil {
						if isEphemeral {
							_ = tx.Rollback()
						}
						return nil, fmt.Errorf("check constraint violation on %q: %w", tableName, err)
					}
					if err := tx.Update(tableName, existingPk, updatedRec); err != nil {
						if isEphemeral {
							_ = tx.Rollback()
						}
						return nil, fmt.Errorf("update row error in ON CONFLICT: %w", err)
					}
					if hasUpdTriggers {
						trigUpdInserted = append(trigUpdInserted, updatedRec.Clone())
						trigUpdDeleted = append(trigUpdDeleted, existingRec.Clone())
					}
					if err := s.routeOutputRowDirect(tx, outInfo, existingRec, updatedRec, schema, cs, &outputRows, &outputVals); err != nil {
						if isEphemeral {
							_ = tx.Rollback()
						}
						return nil, err
					}
					insertedCount++
					continue
				}
			}
		}

		if err := proc.ValidateRecordChecks(schema, rec); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("check constraint violation on %q: %w", tableName, err)
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
		if hasInsTriggers {
			trigInsertedRecs = append(trigInsertedRecs, rec.Clone())
		}

		if err := s.routeOutputRowDirect(tx, outInfo, nil, rec, schema, cs, &outputRows, &outputVals); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, err
		}
		insertedCount++
	}

	if hasInsTriggers && len(trigInsertedRecs) > 0 {
		if err := s.fireVolcanoTriggers(tx, cs, tableName, "INSERT", trigInsertedRecs, nil); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, err
		}
	}
	if hasUpdTriggers && len(trigUpdInserted) > 0 {
		if err := s.fireVolcanoTriggers(tx, cs, tableName, "UPDATE", trigUpdInserted, trigUpdDeleted); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, err
		}
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
		Values:       outputVals,
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
	if conflictIdx := sqlutil.FindTopLevelKeyword(selectPart, "ON CONFLICT"); conflictIdx != -1 {
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
		if cs.IsPostgres() {
			identityInsertAllowed = true
		}
		cs.Mu.Unlock()
	}

	var targetCols []string
	if rawCols != "" {
		for _, c := range sqlutil.SplitTopLevel(rawCols, ',') {
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
	if cs != nil && cs.Tx() != nil {
		tx = wrapSessionTx(cs, cs.Tx())
		isEphemeral = false
	} else {
		session, closeFn, err := s.acquireSessionForQuery(cs)
		if err != nil {
			return nil, err
		}
		defer closeFn()

		t, err := session.BeginTx(cs.TxOptions())
		if err != nil {
			return nil, err
		}
		tx = wrapSessionTx(cs, t)
		isEphemeral = true
		defer tx.Rollback()
	}

	var nextAllocID int64 = 0
	if identityCol != nil && !hasIdentityInTarget && len(selRes.Rows) > 0 {
		count := int64(len(selRes.Rows))
		seed := identityCol.GetIdentitySeed()
		step := identityCol.GetIdentityIncrement()
		nextAllocID = s.AllocTableIdentityWithStep(schema.Name, count, seed, step, func() (int64, bool) {
			var scanMax int64 = 0
			hasRows := false
			if cur, err := tx.Scan(tableName); err == nil {
				for cur.Next() {
					if r, err := cur.Record(); err == nil {
						if v, ok := r[identityCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull {
							if !hasRows || v.IVal > scanMax {
								scanMax = v.IVal
							}
							hasRows = true
						}
					}
				}
				cur.Close()
			}
			return scanMax, hasRows
		})
	}

	var outInfo outputClauseInfo
	rawOutput = strings.TrimSpace(rawOutput)
	if rawOutput != "" {
		outInfo = parseOutputClauseInfo(rawOutput)
	}
	outputProjs := outInfo.Projs
	var outputRows []map[string]any
	var outputVals [][]any
	var outputCols []string
	if len(outputProjs) > 0 && outInfo.IntoTable == "" {
		_, outputCols = evaluateOutputRow(outputProjs, nil, nil, schema)
	}

	hasInsTriggers := s.executor != nil && s.executor.HasTriggers(tableName, "INSERT")
	hasUpdTriggers := s.executor != nil && s.executor.HasTriggers(tableName, "UPDATE")
	var trigInsertedRecs []storage.Record
	var trigUpdInserted []storage.Record
	var trigUpdDeleted []storage.Record

	insertedCount := int64(0)
	for rowIdx, row := range selRes.Rows {
		rec := make(storage.Record, len(schema.Columns))
		for i, colName := range targetCols {
			colSchema, _, _ := schema.FindColumn(colName)
			var val any
			if rowIdx < len(selRes.Values) && i < len(selRes.Values[rowIdx]) {
				val = selRes.Values[rowIdx][i]
			} else {
				srcCol := selRes.Columns[i]
				val = row[srcCol]
			}
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
				curID := nextAllocID
				step := identityCol.GetIdentityIncrement()
				nextAllocID += step
				rec[identityCol.Name] = storage.IntValue(curID)
				if cs != nil {
					cs.Mu.Lock()
					cs.Vars["identity"] = curID
					cs.Vars["scope_identity"] = curID
					cs.Mu.Unlock()
				}
				s.SetTableLastIdentity(schema.Name, curID)
				s.RecordLastIdentity(curID, curID)
			} else {
				if v, ok := rec[identityCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull {
					if cs != nil {
						cs.Mu.Lock()
						cs.Vars["identity"] = v.IVal
						cs.Vars["scope_identity"] = v.IVal
						cs.Mu.Unlock()
					}
					s.SetTableLastIdentity(schema.Name, v.IVal)
					s.RecordLastIdentity(v.IVal, v.IVal)
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
			} else if schema.IsCompositePK() {
				for _, c := range schema.Columns {
					if c.PrimaryKey {
						conflictCols = append(conflictCols, c.Name)
					}
				}
			} else if pkCol, err := schema.PrimaryKeyColumn(); err == nil && pkCol != nil {
				conflictCols = []string{pkCol.Name}
			}

			var existingRec storage.Record
			var existingPk storage.Value
			pkCol, _ := schema.PrimaryKeyColumn()

			if schema.IsCompositePK() {
				if pkVal, ok := schema.ResolveRecordPK(rec); ok {
					if ex, err := tx.Get(tableName, pkVal); err == nil && ex != nil {
						existingRec = ex
						existingPk = pkVal
					}
				}
			} else if len(conflictCols) == 1 && pkCol != nil && strings.EqualFold(conflictCols[0], pkCol.Name) {
				if targetVal, ok := rec.Get(pkCol.Name); ok {
					if ex, err := tx.Get(tableName, targetVal); err == nil && ex != nil {
						existingRec = ex
						existingPk = targetVal
					}
				}
			}

			if existingRec == nil && len(conflictCols) > 0 {
				var matchingIdx *storage.IndexSchema
				for _, idx := range schema.Indexes {
					if len(idx.Columns) == len(conflictCols) {
						allMatch := true
						for ci, colName := range conflictCols {
							if !strings.EqualFold(idx.Columns[ci], colName) {
								allMatch = false
								break
							}
						}
						if allMatch {
							matchingIdx = &idx
							break
						}
					}
				}

				if matchingIdx != nil && len(conflictCols) == 1 {
					colName := conflictCols[0]
					if targetVal, ok := rec.Get(colName); ok {
						if cur, err := tx.IndexScan(tableName, matchingIdx.Name, &targetVal, &targetVal); err == nil {
							for cur.Next() {
								if r, err := cur.Record(); err == nil {
									existingRec = r
									if pkVal, ok := schema.ResolveRecordPK(r); ok {
										existingPk = pkVal
									} else if pkCol != nil {
										existingPk, _ = r.Get(pkCol.Name)
									}
									break
								}
							}
							cur.Close()
						}
					}
				}

				if existingRec == nil {
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
								if pkVal, ok := schema.ResolveRecordPK(r); ok {
									existingPk = pkVal
								} else if pkCol != nil {
									existingPk, _ = r.Get(pkCol.Name)
								}
								break
							}
						}
						cur.Close()
					}
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

					if err := proc.ValidateRecordChecks(schema, updatedRec); err != nil {
						if isEphemeral {
							_ = tx.Rollback()
						}
						return nil, fmt.Errorf("check constraint violation on %q: %w", tableName, err)
					}
					if err := tx.Update(tableName, existingPk, updatedRec); err != nil {
						if isEphemeral {
							_ = tx.Rollback()
						}
						return nil, fmt.Errorf("update row error in ON CONFLICT: %w", err)
					}
					if hasUpdTriggers {
						trigUpdInserted = append(trigUpdInserted, updatedRec.Clone())
						trigUpdDeleted = append(trigUpdDeleted, existingRec.Clone())
					}
					if err := s.routeOutputRowDirect(tx, outInfo, existingRec, updatedRec, schema, cs, &outputRows, &outputVals); err != nil {
						if isEphemeral {
							_ = tx.Rollback()
						}
						return nil, err
					}
					insertedCount++
					continue
				}
			}
		}

		if err := proc.ValidateRecordChecks(schema, rec); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("check constraint violation on %q: %w", tableName, err)
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
		if hasInsTriggers {
			trigInsertedRecs = append(trigInsertedRecs, rec.Clone())
		}

		if err := s.routeOutputRowDirect(tx, outInfo, nil, rec, schema, cs, &outputRows, &outputVals); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, err
		}
		insertedCount++
	}

	if hasInsTriggers && len(trigInsertedRecs) > 0 {
		if err := s.fireVolcanoTriggers(tx, cs, tableName, "INSERT", trigInsertedRecs, nil); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, err
		}
	}
	if hasUpdTriggers && len(trigUpdInserted) > 0 {
		if err := s.fireVolcanoTriggers(tx, cs, tableName, "UPDATE", trigUpdInserted, trigUpdDeleted); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, err
		}
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
		if nextAllocID > 0 {
			cs.Vars["identity"] = nextAllocID - 1
		}
		cs.Mu.Unlock()
	}

	return &QueryResult{
		Success:      true,
		Command:      "INSERT",
		Columns:      outputCols,
		Rows:         outputRows,
		Values:       outputVals,
		RowsAffected: insertedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows affected into %q", insertedCount, tableName)},
	}, nil
}

func (s *Coordinator) execUpdate(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// RBAC Permission Check
	if kind, table, ok := dmlHead(cleaned); ok && kind == dmlUpdate {
		if err := s.checkTableAccess(cs, cleanIdentifier(table), auth.PrivUpdate); err != nil {
			return nil, err
		}
	}

	dml, parseErr := ParseDMLCommand(cleaned)
	if parseErr != nil {
		return nil, parseErr
	}

	if dml.From != "" {
		return s.execUpdateFromJoin(dml, start, cs)
	}

	// Primary Fast-Path: Unified VDBE Virtual Machine execution (bypass if OUTPUT, RETURNING, or subquery present)
	hasOutput := sqlutil.ContainsKeyword(cleaned, "OUTPUT", false) || sqlutil.ContainsKeyword(cleaned, "RETURNING", false)
	hasSubquery := sqlutil.ContainsKeyword(cleaned, "SELECT", true) || sqlutil.ContainsKeyword(cleaned, "EXISTS", true)
	if !hasOutput && !hasSubquery {
		if res, err := s.execVBDEDML("UPDATE", cleaned, start, cs); err == nil {
			return res, nil
		}
	}

	tableName := cleanIdentifier(dml.Table)
	setClause := dml.Set
	rawOutput := dml.Output
	whereClause := dml.Where

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		if s.catalog != nil {
			if v, vErr := s.catalog.GetView(tableName); vErr == nil && v != nil {
				fromIdx := sqlutil.FindTopLevelKeyword(v.Query, "FROM")
				if fromIdx != -1 {
					rest := strings.TrimSpace(v.Query[fromIdx+4:])
					whereIdx := sqlutil.FindTopLevelKeyword(rest, "WHERE")
					tblPart := rest
					viewWhere := ""
					if whereIdx != -1 {
						tblPart = strings.TrimSpace(rest[:whereIdx])
						viewWhere = strings.TrimSpace(rest[whereIdx+5:])
					}
					if !strings.ContainsAny(tblPart, ",()") && sqlutil.FindTopLevelKeyword(tblPart, "JOIN") == -1 {
						fields := strings.Fields(tblPart)
						if len(fields) > 0 {
							baseTbl := cleanIdentifier(fields[0])
							if baseSchema, bErr := s.engine.GetTable(baseTbl); bErr == nil && baseSchema != nil {
								tableName = baseTbl
								schema = baseSchema
								err = nil
								if viewWhere != "" {
									if whereClause != "" {
										whereClause = "(" + viewWhere + ") AND (" + whereClause + ")"
									} else {
										whereClause = viewWhere
									}
								}
							}
						}
					}
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("table %q not found: %w", tableName, err)
		}
	}

	isExplicitTx := (cs != nil && cs.Tx() != nil)
	maxAttempts := 1
	if !isExplicitTx {
		maxAttempts = 5
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*5) * time.Millisecond)
		}

		var tx storage.Transaction
		var isEphemeral bool
		var closeFn func()
		if isExplicitTx {
			tx = wrapSessionTx(cs, cs.Tx())
			isEphemeral = false
		} else {
			session, fn, err := s.acquireSessionForQuery(cs)
			if err != nil {
				return nil, err
			}
			closeFn = fn

			t, err := session.BeginTx(cs.TxOptions())
			if err != nil {
				closeFn()
				return nil, err
			}
			tx = wrapSessionTx(cs, t)
			isEphemeral = true
		}

		cursor, err := tx.Scan(tableName)
		if err != nil {
			if isEphemeral {
				_ = tx.Rollback()
				if closeFn != nil {
					closeFn()
				}
			}
			return nil, err
		}

		setPairs := sqlutil.SplitSetClauses(setClause)
		for _, pair := range setPairs {
			if _, _, found := schema.FindColumn(pair.Column); !found {
				cursor.Close()
				if isEphemeral && tx != nil {
					_ = tx.Rollback()
					if closeFn != nil {
						closeFn()
					}
				}
				return nil, fmt.Errorf("column %q does not exist in table %q", pair.Column, tableName)
			}
		}

		type updateEntry struct {
			pk   storage.Value
			rec  storage.Record
			orig storage.Record
		}
		toUpdate := make([]updateEntry, 0)

		var subExec proc.SubqueryExecutor
		if hasSubquery {
			subExec = func(rawSubSQL string) ([]map[string]any, []string, error) {
				res, err := s.executeNestedSessionSQL(cs, rawSubSQL)
				if err != nil {
					return nil, nil, err
				}
				return res.Rows, res.Columns, nil
			}
		}

		var scanErr error
		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
				scanErr = fmt.Errorf("failed to read record during UPDATE scan: %w", err)
				break
			}

			// Parameterize subquery if needed for correlated subqueries
			var rowSubExec proc.SubqueryExecutor
			if subExec != nil {
				var evalMap map[string]any
				rowSubExec = func(rawSubSQL string) ([]map[string]any, []string, error) {
					if evalMap == nil {
						evalMap = make(map[string]any, len(rec)*2)
						for k, v := range rec {
							sv := formatStorageValue(v)
							evalMap[k] = sv
							evalMap[tableName+"."+k] = sv
						}
					}
					paramSQL := sqlutil.ParameterizeSubquery(rawSubSQL, evalMap)
					return subExec(paramSQL)
				}
			}

			if whereClause != "" && !evaluateWhereConditionWithSubquery(rec, whereClause, rowSubExec) {
				continue
			}

			pkVal, hasPK := schema.ResolveRecordPK(rec)
			if !hasPK {
				scanErr = fmt.Errorf("table %q has no primary key to identify row for update", tableName)
				break
			}

			origSnapshot := rec.Clone()
			recUpdates := make(storage.Record, len(setPairs))
			var evalErr error
			for _, pair := range setPairs {
				colName := pair.Column
				exprStr := pair.Value
				colSchema, _, found := schema.FindColumn(colName)
				targetType := storage.TypeVarChar
				targetCol := colName
				if found {
					targetCol = colSchema.Name
					targetType = colSchema.Type
				}
				val, err := EvalUpdateExprWithSubquery(exprStr, origSnapshot, targetType, rowSubExec)
				if err != nil {
					evalErr = err
					break
				}
				recUpdates.Set(targetCol, val)
			}
			if evalErr != nil {
				scanErr = evalErr
				break
			}

			if hasPK {
				toUpdate = append(toUpdate, updateEntry{pk: pkVal, rec: recUpdates, orig: origSnapshot})
			}
		}
		cursor.Close()

		if scanErr != nil {
			if isEphemeral && tx != nil {
				_ = tx.Rollback()
				if closeFn != nil {
					closeFn()
				}
			}
			return nil, scanErr
		}

		var outInfo outputClauseInfo
		if rawOutput != "" {
			outInfo = parseOutputClauseInfo(rawOutput)
		}
		outputProjs := outInfo.Projs
		var outputRows []map[string]any
		var outputVals [][]any
		var outputCols []string
		if len(outputProjs) > 0 && outInfo.IntoTable == "" {
			_, outputCols = evaluateOutputRow(outputProjs, nil, nil, schema)
		}

		updatedCount := int64(0)
		var updateErr error
		hasUpdTriggers := s.executor != nil && s.executor.HasTriggers(tableName, "UPDATE")
		var trigInserted []storage.Record
		var trigDeleted []storage.Record
		for _, item := range toUpdate {
			if schema != nil && schema.HasCheckConstraints() {
				merged := item.orig.Clone()
				for k, v := range item.rec {
					merged[k] = v
				}
				if err := proc.ValidateRecordChecks(schema, merged); err != nil {
					updateErr = fmt.Errorf("check constraint violation on %q: %w", tableName, err)
					break
				}
			}
			if err := tx.Update(tableName, item.pk, item.rec); err != nil {
				if isEphemeral && storage.IsWriteConflict(err) {
					updateErr = err
					break
				}
				if errors.Is(err, storage.ErrKeyExists) || errors.Is(err, storage.ErrUniqueViolation) {
					updateErr = fmt.Errorf("duplicate key value violates unique constraint on %q: %w", tableName, err)
					break
				}
				updateErr = fmt.Errorf("update row error: %w", err)
				break
			}
			if hasUpdTriggers {
				newRec := item.orig.Clone()
				for k, v := range item.rec {
					newRec[k] = v
				}
				trigInserted = append(trigInserted, newRec)
				trigDeleted = append(trigDeleted, item.orig.Clone())
			}

			if len(outputProjs) > 0 {
				newRec := make(storage.Record, len(item.orig))
				for k, v := range item.orig {
					newRec[k] = v
				}
				for k, v := range item.rec {
					newRec[k] = v
				}
				if err := s.routeOutputRowDirect(tx, outInfo, item.orig, newRec, schema, cs, &outputRows, &outputVals); err != nil {
					updateErr = err
					break
				}
			}
			updatedCount++
		}

		if updateErr == nil && hasUpdTriggers && len(trigInserted) > 0 {
			if err := s.fireVolcanoTriggers(tx, cs, tableName, "UPDATE", trigInserted, trigDeleted); err != nil {
				updateErr = err
			}
		}

		if updateErr != nil {
			if isEphemeral {
				_ = tx.Rollback()
				if closeFn != nil {
					closeFn()
				}
				if storage.IsWriteConflict(updateErr) {
					lastErr = updateErr
					continue
				}
			}
			return nil, updateErr
		}

		if isEphemeral {
			if err := tx.Commit(); err != nil {
				_ = tx.Rollback()
				if closeFn != nil {
					closeFn()
				}
				if storage.IsWriteConflict(err) {
					lastErr = err
					continue
				}
				return nil, fmt.Errorf("commit error: %w", err)
			}
			tx = nil
			if closeFn != nil {
				closeFn()
			}
		}

		return &QueryResult{
			Success:      true,
			Command:      "UPDATE",
			Columns:      outputCols,
			Rows:         outputRows,
			Values:       outputVals,
			RowsAffected: updatedCount,
			DurationUS:   time.Since(start).Microseconds(),
			Messages:     []string{fmt.Sprintf("%d rows updated in %q", updatedCount, tableName)},
		}, nil
	}

	return nil, lastErr
}

func (s *Coordinator) execDelete(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// RBAC Permission Check
	if kind, table, ok := dmlHead(cleaned); ok && kind == dmlDelete {
		if err := s.checkTableAccess(cs, cleanIdentifier(table), auth.PrivDelete); err != nil {
			return nil, err
		}
	}

	dml, parseErr := ParseDMLCommand(cleaned)
	if parseErr != nil {
		return nil, parseErr
	}

	if dml.From != "" {
		return s.execDeleteFromJoin(dml, start, cs)
	}

	// Primary Fast-Path: Unified VDBE Virtual Machine execution (bypass if OUTPUT, RETURNING, or subquery present)
	hasOutput := sqlutil.ContainsKeyword(cleaned, "OUTPUT", false) || sqlutil.ContainsKeyword(cleaned, "RETURNING", false)
	hasSubquery := sqlutil.ContainsKeyword(cleaned, "SELECT", true) || sqlutil.ContainsKeyword(cleaned, "EXISTS", true)
	if !hasOutput && !hasSubquery {
		if res, err := s.execVBDEDML("DELETE", cleaned, start, cs); err == nil {
			return res, nil
		}
	}

	tableName := cleanIdentifier(dml.Table)
	rawOutput := dml.Output
	whereClause := dml.Where

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		if s.catalog != nil {
			if v, vErr := s.catalog.GetView(tableName); vErr == nil && v != nil {
				fromIdx := sqlutil.FindTopLevelKeyword(v.Query, "FROM")
				if fromIdx != -1 {
					rest := strings.TrimSpace(v.Query[fromIdx+4:])
					whereIdx := sqlutil.FindTopLevelKeyword(rest, "WHERE")
					tblPart := rest
					viewWhere := ""
					if whereIdx != -1 {
						tblPart = strings.TrimSpace(rest[:whereIdx])
						viewWhere = strings.TrimSpace(rest[whereIdx+5:])
					}
					if !strings.ContainsAny(tblPart, ",()") && sqlutil.FindTopLevelKeyword(tblPart, "JOIN") == -1 {
						fields := strings.Fields(tblPart)
						if len(fields) > 0 {
							baseTbl := cleanIdentifier(fields[0])
							if baseSchema, bErr := s.engine.GetTable(baseTbl); bErr == nil && baseSchema != nil {
								tableName = baseTbl
								schema = baseSchema
								err = nil
								if viewWhere != "" {
									if whereClause != "" {
										whereClause = "(" + viewWhere + ") AND (" + whereClause + ")"
									} else {
										whereClause = viewWhere
									}
								}
							}
						}
					}
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("table %q not found: %w", tableName, err)
		}
	}

	var tx storage.Transaction
	var isEphemeral bool
	if cs != nil && cs.Tx() != nil {
		tx = wrapSessionTx(cs, cs.Tx())
		isEphemeral = false
	} else {
		session, closeFn, err := s.acquireSessionForQuery(cs)
		if err != nil {
			return nil, err
		}
		defer closeFn()

		t, err := session.BeginTx(cs.TxOptions())
		if err != nil {
			return nil, err
		}
		tx = wrapSessionTx(cs, t)
		isEphemeral = true
		defer tx.Rollback()
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

	var subExec proc.SubqueryExecutor
	if hasSubquery {
		subExec = func(rawSubSQL string) ([]map[string]any, []string, error) {
			res, err := s.executeNestedSessionSQL(cs, rawSubSQL)
			if err != nil {
				return nil, nil, err
			}
			return res.Rows, res.Columns, nil
		}
	}

	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			if isEphemeral && tx != nil {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("failed to read record during DELETE scan: %w", err)
		}

		var rowSubExec proc.SubqueryExecutor
		if subExec != nil {
			var evalMap map[string]any
			rowSubExec = func(rawSubSQL string) ([]map[string]any, []string, error) {
				if evalMap == nil {
					evalMap = make(map[string]any, len(rec)*2)
					for k, v := range rec {
						sv := formatStorageValue(v)
						evalMap[k] = sv
						evalMap[tableName+"."+k] = sv
					}
				}
				paramSQL := sqlutil.ParameterizeSubquery(rawSubSQL, evalMap)
				return subExec(paramSQL)
			}
		}

		if whereClause != "" && !evaluateWhereConditionWithSubquery(rec, whereClause, rowSubExec) {
			continue
		}

		pkVal, hasPK := schema.ResolveRecordPK(rec)
		if !hasPK {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("table %q has no primary key to identify row for delete", tableName)
		}

		if hasPK {
			toDelete = append(toDelete, deleteEntry{pk: pkVal, orig: rec.Clone()})
		}
	}

	var outInfo outputClauseInfo
	if rawOutput != "" {
		outInfo = parseOutputClauseInfo(rawOutput)
	}
	outputProjs := outInfo.Projs
	var outputRows []map[string]any
	var outputVals [][]any
	var outputCols []string
	if len(outputProjs) > 0 && outInfo.IntoTable == "" {
		_, outputCols = evaluateOutputRow(outputProjs, nil, nil, schema)
	}

	hasDelTriggers := s.executor != nil && s.executor.HasTriggers(tableName, "DELETE")
	var trigDeleted []storage.Record

	deletedCount := int64(0)
	for _, item := range toDelete {
		if err := tx.Delete(tableName, item.pk); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("delete error: %w", err)
		}
		if hasDelTriggers {
			trigDeleted = append(trigDeleted, item.orig.Clone())
		}

		if err := s.routeOutputRowDirect(tx, outInfo, item.orig, nil, schema, cs, &outputRows, &outputVals); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, err
		}
		deletedCount++
	}

	if hasDelTriggers && len(trigDeleted) > 0 {
		if err := s.fireVolcanoTriggers(tx, cs, tableName, "DELETE", nil, trigDeleted); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, err
		}
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
		Values:       outputVals,
		RowsAffected: deletedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows deleted from %q", deletedCount, tableName)},
	}, nil
}

func resolveTargetTableAndRef(dmlTable, dmlFrom string, eng storage.Engine) (targetTable, targetRef, fromClause string) {
	cleanTable := cleanIdentifier(dmlTable)
	if _, err := eng.GetTable(cleanTable); err == nil {
		targetTable = cleanTable
		targetRef = cleanTable
	} else {
		tokens := strings.Fields(dmlFrom)
		for i, tok := range tokens {
			cleanTok := cleanIdentifier(tok)
			if strings.EqualFold(cleanTok, cleanTable) && i > 0 {
				prevTok := cleanIdentifier(tokens[i-1])
				if _, err := eng.GetTable(prevTok); err == nil {
					targetTable = prevTok
					targetRef = cleanTable
					break
				}
			}
		}
		if targetTable == "" {
			targetTable = cleanTable
			targetRef = cleanTable
		}
	}

	fromClause = dmlFrom
	if !sqlutil.ContainsKeyword(fromClause, targetTable, false) && !sqlutil.ContainsKeyword(fromClause, targetRef, false) {
		fromClause = targetTable + ", " + fromClause
	}
	return targetTable, targetRef, fromClause
}

func (s *Coordinator) execDeleteFromJoin(dml *DMLStatement, start time.Time, cs *ClientSession) (*QueryResult, error) {
	targetTable, targetRef, fromClause := resolveTargetTableAndRef(dml.Table, dml.From, s.engine)
	schema, err := s.engine.GetTable(targetTable)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", targetTable, err)
	}
	pkCols := schema.PrimaryKeyColumns()
	if len(pkCols) == 0 {
		return nil, fmt.Errorf("table %q has no primary key: %w", targetTable, err)
	}

	var pkExprs []string
	for i, col := range pkCols {
		pkExprs = append(pkExprs, fmt.Sprintf("%s.%s AS __lionsql_pk_%d__", targetRef, col.Name, i))
	}

	drivingQuery := fmt.Sprintf("SELECT %s FROM %s", strings.Join(pkExprs, ", "), fromClause)
	if dml.Where != "" {
		drivingQuery += " WHERE " + dml.Where
	}

	selectRes, err := s.execSelect(drivingQuery, start, cs)
	if err != nil {
		return nil, fmt.Errorf("executing driving query for DELETE FROM: %w", err)
	}

	var tx storage.Transaction
	var isEphemeral bool
	if cs != nil && cs.Tx() != nil {
		tx = wrapSessionTx(cs, cs.Tx())
		isEphemeral = false
	} else {
		session, closeFn, err := s.acquireSessionForQuery(cs)
		if err != nil {
			return nil, err
		}
		defer closeFn()
		tx, err = session.BeginTx(storage.TxOptions{})
		if err != nil {
			return nil, err
		}
		isEphemeral = true
	}

	deletedCount := int64(0)
	for _, row := range selectRes.Rows {
		var pkParts []string
		var singleVal storage.Value
		valid := true
		for i, col := range pkCols {
			pkRaw, ok := row[fmt.Sprintf("__lionsql_pk_%d__", i)]
			if !ok || pkRaw == nil {
				valid = false
				break
			}
			v := storage.ValueFromAny(pkRaw, col.Type)
			singleVal = v
			pkParts = append(pkParts, v.String())
		}
		if !valid {
			continue
		}
		var pkVal storage.Value
		if len(pkCols) > 1 {
			pkVal = storage.StringValue(strings.Join(pkParts, "\x1f"))
		} else {
			pkVal = singleVal
		}
		if err := tx.Delete(targetTable, pkVal); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, err
		}
		deletedCount++
	}

	if isEphemeral {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "DELETE",
		Columns:      []string{"rows_affected"},
		Rows:         []map[string]any{{"rows_affected": deletedCount}},
		RowsAffected: deletedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows deleted from %q", deletedCount, targetTable)},
	}, nil
}

func (s *Coordinator) execUpdateFromJoin(dml *DMLStatement, start time.Time, cs *ClientSession) (*QueryResult, error) {
	targetTable, targetRef, fromClause := resolveTargetTableAndRef(dml.Table, dml.From, s.engine)
	schema, err := s.engine.GetTable(targetTable)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", targetTable, err)
	}
	pkCols := schema.PrimaryKeyColumns()
	if len(pkCols) == 0 {
		return nil, fmt.Errorf("table %q has no primary key: %w", targetTable, err)
	}

	assignments := splitAssignments(dml.Set)
	if len(assignments) == 0 {
		return nil, fmt.Errorf("empty SET clause in UPDATE")
	}

	type colUpd struct {
		colName  string
		aliasCol string
	}
	var updCols []colUpd
	var selectExprs []string
	for i, col := range pkCols {
		selectExprs = append(selectExprs, fmt.Sprintf("%s.%s AS __lionsql_pk_%d__", targetRef, col.Name, i))
	}

	for i, a := range assignments {
		eqIdx := sqlutil.FindTopLevelChar(a, '=')
		if eqIdx == -1 {
			return nil, fmt.Errorf("invalid assignment in SET: %q", a)
		}
		targetCol := cleanIdentifier(a[:eqIdx])
		if idx := strings.Index(targetCol, "."); idx != -1 {
			targetCol = cleanIdentifier(targetCol[idx+1:])
		}
		exprVal := strings.TrimSpace(a[eqIdx+1:])
		alias := fmt.Sprintf("__upd_%d__", i)
		selectExprs = append(selectExprs, fmt.Sprintf("(%s) AS %s", exprVal, alias))
		updCols = append(updCols, colUpd{colName: targetCol, aliasCol: alias})
	}

	drivingQuery := fmt.Sprintf("SELECT %s FROM %s", strings.Join(selectExprs, ", "), fromClause)
	if dml.Where != "" {
		drivingQuery += " WHERE " + dml.Where
	}

	selectRes, err := s.execSelect(drivingQuery, start, cs)
	if err != nil {
		return nil, fmt.Errorf("executing driving query for UPDATE FROM: %w", err)
	}

	var tx storage.Transaction
	var isEphemeral bool
	if cs != nil && cs.Tx() != nil {
		tx = wrapSessionTx(cs, cs.Tx())
		isEphemeral = false
	} else {
		session, closeFn, err := s.acquireSessionForQuery(cs)
		if err != nil {
			return nil, err
		}
		defer closeFn()
		tx, err = session.BeginTx(storage.TxOptions{})
		if err != nil {
			return nil, err
		}
		isEphemeral = true
	}

	updatedCount := int64(0)
	for _, row := range selectRes.Rows {
		var pkParts []string
		var singleVal storage.Value
		valid := true
		for i, col := range pkCols {
			pkRaw, ok := row[fmt.Sprintf("__lionsql_pk_%d__", i)]
			if !ok || pkRaw == nil {
				valid = false
				break
			}
			v := storage.ValueFromAny(pkRaw, col.Type)
			singleVal = v
			pkParts = append(pkParts, v.String())
		}
		if !valid {
			continue
		}
		var pkVal storage.Value
		if len(pkCols) > 1 {
			pkVal = storage.StringValue(strings.Join(pkParts, "\x1f"))
		} else {
			pkVal = singleVal
		}
		rec, err := tx.Get(targetTable, pkVal)
		if err != nil {
			continue
		}
		for _, u := range updCols {
			newVal := row[u.aliasCol]
			colDef, _, found := schema.FindColumn(u.colName)
			if found {
				rec[u.colName] = storage.ValueFromAny(newVal, colDef.Type)
			} else {
				rec[u.colName] = storage.ValueFromAny(newVal, storage.TypeVarChar)
			}
		}
		if err := tx.Update(targetTable, pkVal, rec); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, err
		}
		updatedCount++
	}

	if isEphemeral {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "UPDATE",
		Columns:      []string{"rows_affected"},
		Rows:         []map[string]any{{"rows_affected": updatedCount}},
		RowsAffected: updatedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows updated in %q", updatedCount, targetTable)},
	}, nil
}
