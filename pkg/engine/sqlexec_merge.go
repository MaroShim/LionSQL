package engine

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

type mergeClauseType int

const (
	mergeMatchedUpdate mergeClauseType = iota
	mergeMatchedDelete
	mergeNotMatchedInsert
	mergeNotMatchedSourceDelete
	mergeNotMatchedSourceUpdate
)

type mergeSetPair struct {
	column string
	expr   string
}

type mergeClause struct {
	clauseType mergeClauseType
	andCond    string
	setPairs   []mergeSetPair
	insertCols []string
	insertVals []string
}

type parsedMerge struct {
	targetTable string
	targetAlias string
	sourceRaw   string
	sourceAlias string
	sourceCols  []string
	onCondition string
	clauses     []mergeClause
}

func (s *Coordinator) execMerge(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	parsed, err := parseMergeQuery(cleaned)
	if err != nil {
		return nil, fmt.Errorf("failed to parse MERGE statement: %w", err)
	}

	targetSchema, err := s.engine.GetTable(parsed.targetTable)
	if err != nil {
		return nil, fmt.Errorf("target table %q not found: %w", parsed.targetTable, err)
	}

	pkCol, err := targetSchema.PrimaryKeyColumn()
	if err != nil {
		if synCol := targetSchema.SyntheticPKColumn(); synCol != nil {
			pkCol = synCol
		} else {
			return nil, fmt.Errorf("target table %q has no primary key: %w", parsed.targetTable, err)
		}
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

		t, err := session.BeginTx(storage.TxOptions{})
		if err != nil {
			return nil, err
		}
		tx = t
		isEphemeral = true
	}
	defer func() {
		if isEphemeral && tx != nil {
			_ = tx.Rollback()
		}
	}()

	// 1. Load source rows
	sourceRows, err := s.loadMergeSourceRows(parsed, tx, start, cs)
	if err != nil {
		return nil, fmt.Errorf("failed to load MERGE source: %w", err)
	}

	// 2. Load target rows
	targetCursor, err := tx.Scan(parsed.targetTable)
	if err != nil {
		return nil, fmt.Errorf("failed to scan target table %q: %w", parsed.targetTable, err)
	}
	defer targetCursor.Close()

	var targetRows []storage.Record
	for targetCursor.Next() {
		rec, err := targetCursor.Record()
		if err != nil {
			return nil, err
		}
		targetRows = append(targetRows, cloneRecord(rec))
	}
	initialTargetCount := len(targetRows)

	matchedTargetKeys := make(map[string]bool)
	newlyInsertedKeys := make(map[string]bool)
	var rowsAffected int64

	// Detect identity column on target table
	var identityCol *storage.Column
	for i := range targetSchema.Columns {
		if targetSchema.Columns[i].Identity {
			identityCol = &targetSchema.Columns[i]
			break
		}
	}

	var maxID int64 = 0
	if identityCol != nil {
		if lastID, ok := s.GetTableLastIdentity(targetSchema.Name); ok {
			maxID = lastID
		} else {
			for _, tr := range targetRows {
				if v, ok := tr[identityCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull {
					if v.IVal > maxID {
						maxID = v.IVal
					}
				}
			}
			s.SetTableLastIdentity(targetSchema.Name, maxID)
		}
	}

	// 3. Process each Source row
	for _, sRow := range sourceRows {
		matchedTargetIdx := -1
		var matchedEvalMap map[string]any

		for idx, tRec := range targetRows {
			evalMap := buildMergeEvalMap(tRec, sRow, parsed.targetTable, parsed.targetAlias, parsed.sourceRaw, parsed.sourceAlias)
			if evaluateWhereConditionMap(evalMap, parsed.onCondition) {
				matchedTargetIdx = idx
				matchedEvalMap = evalMap
				break
			}
		}

		if matchedTargetIdx >= 0 {
			// WHEN MATCHED
			tRec := targetRows[matchedTargetIdx]
			pkVal := tRec[pkCol.Name]
			pkKey := pkVal.String()
			matchedTargetKeys[pkKey] = true

			for _, clause := range parsed.clauses {
				if clause.clauseType != mergeMatchedUpdate && clause.clauseType != mergeMatchedDelete {
					continue
				}
				if clause.andCond != "" && !evaluateWhereConditionMap(matchedEvalMap, clause.andCond) {
					continue
				}

				if clause.clauseType == mergeMatchedDelete {
					if err := tx.Delete(parsed.targetTable, pkVal); err != nil {
						return nil, fmt.Errorf("MERGE DELETE failed: %w", err)
					}
					rowsAffected++
					break
				} else if clause.clauseType == mergeMatchedUpdate {
					updatedRec := cloneRecord(tRec)
					for _, pair := range clause.setPairs {
						colName := stripQualifier(pair.column)
						col, _, found := targetSchema.FindColumn(colName)
						if !found {
							return nil, fmt.Errorf("column %q not found in target table %q", colName, parsed.targetTable)
						}
						val := evalMergeExpr(pair.expr, matchedEvalMap, col.Type)
						updatedRec[col.Name] = val
					}
					if err := tx.Update(parsed.targetTable, pkVal, updatedRec); err != nil {
						return nil, fmt.Errorf("MERGE UPDATE failed: %w", err)
					}
					targetRows[matchedTargetIdx] = updatedRec
					rowsAffected++
					break
				}
			}
		} else {
			// WHEN NOT MATCHED [BY TARGET]
			evalMap := buildMergeEvalMap(nil, sRow, parsed.targetTable, parsed.targetAlias, parsed.sourceRaw, parsed.sourceAlias)

			for _, clause := range parsed.clauses {
				if clause.clauseType != mergeNotMatchedInsert {
					continue
				}
				if clause.andCond != "" && !evaluateWhereConditionMap(evalMap, clause.andCond) {
					continue
				}

				cols := clause.insertCols
				if len(cols) == 0 {
					for _, c := range targetSchema.Columns {
						if identityCol != nil && c.Identity && len(clause.insertVals) < len(targetSchema.Columns) {
							continue
						}
						cols = append(cols, c.Name)
					}
				}

				if len(cols) != len(clause.insertVals) {
					return nil, fmt.Errorf("MERGE INSERT column count (%d) does not match value count (%d)", len(cols), len(clause.insertVals))
				}

				newRec := make(storage.Record)
				for i, colName := range cols {
					cleanedCol := stripQualifier(colName)
					col, _, found := targetSchema.FindColumn(cleanedCol)
					if !found {
						return nil, fmt.Errorf("column %q not found in target table %q", cleanedCol, parsed.targetTable)
					}
					val := evalMergeExpr(clause.insertVals[i], evalMap, col.Type)
					newRec[col.Name] = val
				}

				// Apply column defaults for missing columns
				for _, col := range targetSchema.Columns {
					if _, hasCol := newRec[col.Name]; !hasCol && col.Default != "" {
						newRec[col.Name] = parseLiteralValue(col.Default, col.Type)
					}
				}

				// Generate IDENTITY value if column was omitted
				if identityCol != nil {
					if _, hasIdent := newRec[identityCol.Name]; !hasIdent {
						maxID++
						newRec[identityCol.Name] = storage.IntValue(maxID)
						if cs != nil {
							cs.Mu.Lock()
							cs.Vars["identity"] = maxID
							cs.Vars["scope_identity"] = maxID
							cs.Mu.Unlock()
						}
						s.SetTableLastIdentity(targetSchema.Name, maxID)
					} else {
						if v, ok := newRec[identityCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull {
							if cs != nil {
								cs.Mu.Lock()
								cs.Vars["identity"] = v.IVal
								cs.Vars["scope_identity"] = v.IVal
								cs.Mu.Unlock()
							}
							s.SetTableLastIdentity(targetSchema.Name, v.IVal)
							if v.IVal > maxID {
								maxID = v.IVal
							}
						}
					}
				}

				if err := tx.Insert(parsed.targetTable, newRec); err != nil {
					return nil, fmt.Errorf("MERGE INSERT failed: %w", err)
				}
				if pkVal, ok := newRec[pkCol.Name]; ok {
					newlyInsertedKeys[pkVal.String()] = true
				}
				targetRows = append(targetRows, newRec)
				rowsAffected++
				break
			}
		}
	}

	// 4. Process WHEN NOT MATCHED BY SOURCE (only over rows that existed prior to MERGE execution)
	for _, clause := range parsed.clauses {
		if clause.clauseType != mergeNotMatchedSourceDelete && clause.clauseType != mergeNotMatchedSourceUpdate {
			continue
		}
		for idx := 0; idx < initialTargetCount; idx++ {
			tRec := targetRows[idx]
			pkVal := tRec[pkCol.Name]
			pkKey := pkVal.String()
			if matchedTargetKeys[pkKey] || newlyInsertedKeys[pkKey] {
				continue
			}

			evalMap := buildMergeEvalMap(tRec, nil, parsed.targetTable, parsed.targetAlias, parsed.sourceRaw, parsed.sourceAlias)
			if clause.andCond != "" && !evaluateWhereConditionMap(evalMap, clause.andCond) {
				continue
			}

			if clause.clauseType == mergeNotMatchedSourceDelete {
				if err := tx.Delete(parsed.targetTable, pkVal); err != nil {
					return nil, fmt.Errorf("MERGE DELETE (BY SOURCE) failed: %w", err)
				}
				rowsAffected++
			} else if clause.clauseType == mergeNotMatchedSourceUpdate {
				updatedRec := cloneRecord(tRec)
				for _, pair := range clause.setPairs {
					colName := stripQualifier(pair.column)
					col, _, found := targetSchema.FindColumn(colName)
					if !found {
						return nil, fmt.Errorf("column %q not found in target table %q", colName, parsed.targetTable)
					}
					val := evalMergeExpr(pair.expr, evalMap, col.Type)
					updatedRec[col.Name] = val
				}
				if err := tx.Update(parsed.targetTable, pkVal, updatedRec); err != nil {
					return nil, fmt.Errorf("MERGE UPDATE (BY SOURCE) failed: %w", err)
				}
				targetRows[idx] = updatedRec
				rowsAffected++
			}
		}
	}

	if isEphemeral {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit error: %w", err)
		}
		tx = nil
	}

	if cs != nil {
		cs.Mu.Lock()
		cs.Vars["@@rowcount"] = int(rowsAffected)
		cs.Mu.Unlock()
	}

	return &QueryResult{
		Success:      true,
		Command:      "MERGE",
		RowsAffected: rowsAffected,
		Messages:     []string{fmt.Sprintf("(%d rows affected)", rowsAffected)},
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) loadMergeSourceRows(parsed *parsedMerge, tx storage.Transaction, start time.Time, cs *ClientSession) ([]map[string]any, error) {
	raw := strings.TrimSpace(parsed.sourceRaw)

	// Case 1: (VALUES (...), (...))
	valuesRe := regexp.MustCompile(`(?is)^\(\s*VALUES\s*(.*)\)$`)
	if m := valuesRe.FindStringSubmatch(raw); len(m) > 1 {
		tuples := sqlutil.ExtractTopLevelTuples(m[1])
		cols := parsed.sourceCols
		var rows []map[string]any
		for _, tuple := range tuples {
			vals := splitValues(tuple)
			r := make(map[string]any)
			for i, v := range vals {
				vStr := strings.TrimSpace(v)
				vStr = strings.Trim(vStr, "'\"")
				var colName string
				if i < len(cols) {
					colName = cols[i]
				} else {
					colName = fmt.Sprintf("col%d", i+1)
				}
				r[colName] = vStr
			}
			rows = append(rows, r)
		}
		return rows, nil
	}

	// Case 2: (SELECT ...)
	if strings.HasPrefix(raw, "(") && strings.HasSuffix(raw, ")") {
		subquery := strings.TrimSpace(raw[1 : len(raw)-1])
		res, err := s.execSelect(subquery, start, cs)
		if err != nil {
			return nil, err
		}
		return res.Rows, nil
	}

	// Case 3: Table source
	sourceTable := cleanIdentifier(raw)
	cur, err := tx.Scan(sourceTable)
	if err != nil {
		return nil, fmt.Errorf("source table %q not found: %w", sourceTable, err)
	}
	defer cur.Close()

	var rows []map[string]any
	for cur.Next() {
		rec, err := cur.Record()
		if err != nil {
			return nil, err
		}
		r := make(map[string]any, len(rec))
		for k, v := range rec {
			r[k] = formatStorageValue(v)
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func buildMergeEvalMap(tRec storage.Record, sRow map[string]any, targetTable, targetAlias, sourceRaw, sourceAlias string) map[string]any {
	evalMap := make(map[string]any)

	if tRec != nil {
		for col, val := range tRec {
			v := formatStorageValue(val)
			evalMap[col] = v
			evalMap[strings.ToLower(col)] = v
			if targetAlias != "" {
				evalMap[targetAlias+"."+col] = v
				evalMap[strings.ToLower(targetAlias)+"."+strings.ToLower(col)] = v
			}
			if targetTable != "" {
				evalMap[targetTable+"."+col] = v
				evalMap[strings.ToLower(targetTable)+"."+strings.ToLower(col)] = v
			}
		}
	}

	if sRow != nil {
		for col, v := range sRow {
			evalMap[col] = v
			evalMap[strings.ToLower(col)] = v
			if sourceAlias != "" {
				evalMap[sourceAlias+"."+col] = v
				evalMap[strings.ToLower(sourceAlias)+"."+strings.ToLower(col)] = v
			}
			cleanSrc := cleanIdentifier(sourceRaw)
			if cleanSrc != "" && !strings.HasPrefix(cleanSrc, "(") {
				evalMap[cleanSrc+"."+col] = v
				evalMap[strings.ToLower(cleanSrc)+"."+strings.ToLower(col)] = v
			}
		}
	}

	return evalMap
}

func evalMergeExpr(expr string, evalMap map[string]any, targetType storage.DataType) storage.Value {
	expr = strings.TrimSpace(expr)

	// 1. Check direct variable or column identifier
	cleaned := cleanIdentifier(expr)
	if val, ok := evalMap[cleaned]; ok {
		if val == nil {
			return storage.NullValue(targetType)
		}
		return parseValueToType(fmt.Sprintf("%v", val), targetType)
	}
	if val, ok := evalMap[strings.ToLower(cleaned)]; ok {
		if val == nil {
			return storage.NullValue(targetType)
		}
		return parseValueToType(fmt.Sprintf("%v", val), targetType)
	}

	// 2. Evaluator via Pratt parser
	p := proc.NewParser(proc.NewLexer(expr))
	astExpr := p.ParseExpr()
	if astExpr != nil && len(p.Errors()) == 0 {
		resVal, err := proc.EvalRowExpr(astExpr, evalMap, nil)
		if err == nil {
			if resVal == nil {
				return storage.NullValue(targetType)
			}
			return parseValueToType(fmt.Sprintf("%v", resVal), targetType)
		}
	}

	// 3. Fallback literal parsing
	return parseLiteralValue(expr, targetType)
}

func cloneRecord(r storage.Record) storage.Record {
	if r == nil {
		return nil
	}
	cp := make(storage.Record, len(r))
	for k, v := range r {
		cp[k] = v
	}
	return cp
}

func parseMergeQuery(query string) (*parsedMerge, error) {
	upper := strings.ToUpper(query)

	// Strip MERGE [INTO]
	var rest string
	if strings.HasPrefix(upper, "MERGE INTO ") {
		rest = query[len("MERGE INTO "):]
	} else if strings.HasPrefix(upper, "MERGE ") {
		rest = query[len("MERGE "):]
	} else {
		return nil, fmt.Errorf("expected MERGE statement")
	}

	// Find top-level USING
	usingIdx := sqlutil.FindTopLevelKeyword(rest, "USING")
	if usingIdx < 0 {
		return nil, fmt.Errorf("missing USING clause in MERGE")
	}

	targetPart := strings.TrimSpace(rest[:usingIdx])
	afterUsing := strings.TrimSpace(rest[usingIdx+len("USING"):])

	// Parse target table and optional alias
	targetTable, targetAlias := parseTableAlias(targetPart)

	// Find top-level ON
	onIdx := sqlutil.FindTopLevelKeyword(afterUsing, "ON")
	if onIdx < 0 {
		return nil, fmt.Errorf("missing ON clause in MERGE")
	}

	sourcePart := strings.TrimSpace(afterUsing[:onIdx])
	afterOn := strings.TrimSpace(afterUsing[onIdx+len("ON"):])

	// Parse source table/subquery and optional alias/columns
	sourceRaw, sourceAlias, sourceCols := parseSourcePart(sourcePart)

	// Find top-level WHEN
	whenIdx := sqlutil.FindTopLevelKeyword(afterOn, "WHEN")
	if whenIdx < 0 {
		return nil, fmt.Errorf("missing WHEN clauses in MERGE")
	}

	onCondition := strings.TrimSpace(afterOn[:whenIdx])
	whenPart := strings.TrimSpace(afterOn[whenIdx:])

	// Parse WHEN clauses
	clauses, err := parseWhenClauses(whenPart)
	if err != nil {
		return nil, err
	}

	return &parsedMerge{
		targetTable: targetTable,
		targetAlias: targetAlias,
		sourceRaw:   sourceRaw,
		sourceAlias: sourceAlias,
		sourceCols:  sourceCols,
		onCondition: onCondition,
		clauses:     clauses,
	}, nil
}

func parseTableAlias(s string) (table, alias string) {
	s = strings.TrimSpace(s)
	// Strip hints like WITH (HOLDLOCK) or WITH(HOLDLOCK)
	upper := strings.ToUpper(s)
	if idx := strings.Index(upper, "WITH"); idx != -1 && (idx == 0 || s[idx-1] == ' ' || s[idx-1] == '\t') {
		beforeWith := strings.TrimSpace(s[:idx])
		afterWith := strings.TrimSpace(s[idx+len("WITH"):])
		if endParen := strings.Index(afterWith, ")"); endParen != -1 {
			rem := strings.TrimSpace(afterWith[endParen+1:])
			if rem != "" {
				s = beforeWith + " " + rem
			} else {
				s = beforeWith
			}
		}
	}
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return "", ""
	}
	table = cleanIdentifier(parts[0])
	if len(parts) == 2 {
		alias = cleanIdentifier(parts[1])
	} else if len(parts) >= 3 && strings.EqualFold(parts[1], "AS") {
		alias = cleanIdentifier(parts[2])
	}
	return table, alias
}

func parseSourcePart(s string) (sourceRaw, sourceAlias string, sourceCols []string) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "(") {
		// Parenthesized subquery or VALUES
		endIdx := sqlutil.FindMatchingParen(s, 0)
		if endIdx != -1 {
			sourceRaw = s[:endIdx+1]
			rem := strings.TrimSpace(s[endIdx+1:])
			// Check optional alias and col list
			re := regexp.MustCompile(`(?is)^(?:AS\s+)?([a-zA-Z0-9_]+)(?:\s*\((.*?)\))?`)
			if m := re.FindStringSubmatch(rem); len(m) > 1 {
				sourceAlias = cleanIdentifier(m[1])
				if len(m) > 2 && strings.TrimSpace(m[2]) != "" {
					colParts := sqlutil.SplitTopLevel(m[2], ',')
					for _, c := range colParts {
						sourceCols = append(sourceCols, cleanIdentifier(c))
					}
				}
			}
			return sourceRaw, sourceAlias, sourceCols
		}
	}

	table, alias := parseTableAlias(s)
	return table, alias, nil
}

func parseWhenClauses(s string) ([]mergeClause, error) {
	// Split by top-level "WHEN "
	indices := sqlutil.FindAllTopLevelKeyword(s, "WHEN")
	if len(indices) == 0 {
		return nil, fmt.Errorf("no WHEN clauses found")
	}

	var clauses []mergeClause
	for i := 0; i < len(indices); i++ {
		start := indices[i]
		end := len(s)
		if i+1 < len(indices) {
			end = indices[i+1]
		}
		chunk := strings.TrimSpace(s[start:end])
		cl, err := parseSingleWhenClause(chunk)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, cl)
	}

	return clauses, nil
}

func parseSingleWhenClause(s string) (mergeClause, error) {
	var cl mergeClause
	upper := strings.ToUpper(s)
	if !strings.HasPrefix(upper, "WHEN ") {
		return cl, fmt.Errorf("expected WHEN clause, got: %s", s)
	}

	rem := strings.TrimSpace(s[len("WHEN "):])

	thenIdx := sqlutil.FindTopLevelKeyword(rem, "THEN")
	if thenIdx < 0 {
		return cl, fmt.Errorf("missing THEN in WHEN clause: %s", s)
	}

	conditionPart := strings.TrimSpace(rem[:thenIdx])
	actionPart := strings.TrimSpace(rem[thenIdx+len("THEN"):])
	upperCond := strings.ToUpper(conditionPart)
	upperAction := strings.ToUpper(actionPart)

	// Parse condition: MATCHED vs NOT MATCHED [BY TARGET | BY SOURCE]
	if strings.HasPrefix(upperCond, "NOT MATCHED BY SOURCE") {
		cl.clauseType = mergeNotMatchedSourceDelete
		andPart := strings.TrimSpace(conditionPart[len("NOT MATCHED BY SOURCE"):])
		if strings.HasPrefix(strings.ToUpper(andPart), "AND ") {
			cl.andCond = strings.TrimSpace(andPart[4:])
		}
	} else if strings.HasPrefix(upperCond, "NOT MATCHED") {
		cl.clauseType = mergeNotMatchedInsert
		restCond := strings.TrimSpace(conditionPart[len("NOT MATCHED"):])
		if strings.HasPrefix(strings.ToUpper(restCond), "BY TARGET") {
			restCond = strings.TrimSpace(restCond[len("BY TARGET"):])
		}
		if strings.HasPrefix(strings.ToUpper(restCond), "AND ") {
			cl.andCond = strings.TrimSpace(restCond[4:])
		}
	} else if strings.HasPrefix(upperCond, "MATCHED") {
		cl.clauseType = mergeMatchedUpdate
		restCond := strings.TrimSpace(conditionPart[len("MATCHED"):])
		if strings.HasPrefix(strings.ToUpper(restCond), "AND ") {
			cl.andCond = strings.TrimSpace(restCond[4:])
		}
	} else {
		return cl, fmt.Errorf("unrecognized WHEN condition: %s", conditionPart)
	}

	// Parse action
	if strings.HasPrefix(upperAction, "DELETE") {
		if cl.clauseType == mergeNotMatchedSourceDelete {
			// already set
		} else {
			cl.clauseType = mergeMatchedDelete
		}
	} else if strings.HasPrefix(upperAction, "UPDATE") {
		setIdx := sqlutil.FindTopLevelKeyword(actionPart, "SET")
		if setIdx < 0 {
			return cl, fmt.Errorf("missing SET in UPDATE action: %s", actionPart)
		}
		if cl.clauseType == mergeNotMatchedSourceDelete {
			cl.clauseType = mergeNotMatchedSourceUpdate
		} else {
			cl.clauseType = mergeMatchedUpdate
		}
		setStr := strings.TrimSpace(actionPart[setIdx+len("SET"):])
		pairs := parseSetPairs(setStr)
		cl.setPairs = pairs
	} else if strings.HasPrefix(upperAction, "INSERT") {
		cl.clauseType = mergeNotMatchedInsert
		re := regexp.MustCompile(`(?is)^INSERT(?:\s*\((.*?)\))?\s*VALUES\s*\((.*?)\)$`)
		m := re.FindStringSubmatch(actionPart)
		if len(m) < 3 {
			return cl, fmt.Errorf("invalid INSERT clause in MERGE: %s", actionPart)
		}
		if strings.TrimSpace(m[1]) != "" {
			for _, c := range strings.Split(m[1], ",") {
				cl.insertCols = append(cl.insertCols, cleanIdentifier(c))
			}
		}
		vals := splitValues(m[2])
		for _, v := range vals {
			cl.insertVals = append(cl.insertVals, strings.TrimSpace(v))
		}
	} else {
		return cl, fmt.Errorf("unsupported MERGE action: %s", actionPart)
	}

	return cl, nil
}

func parseSetPairs(s string) []mergeSetPair {
	pairs := sqlutil.SplitSetClauses(s)
	res := make([]mergeSetPair, 0, len(pairs))
	for _, p := range pairs {
		res = append(res, mergeSetPair{
			column: p.Column,
			expr:    p.Value,
		})
	}
	return res
}

func stripQualifier(col string) string {
	col = cleanIdentifier(col)
	if idx := strings.LastIndex(col, "."); idx >= 0 {
		return col[idx+1:]
	}
	return col
}
