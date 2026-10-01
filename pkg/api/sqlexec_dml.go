package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"sqlion/pkg/auth"
	"sqlion/pkg/execution"
	"sqlion/pkg/planner"
	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

func (s *Server) execInsert(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// RBAC Permission Check
	insRe := regexp.MustCompile(`(?is)^INSERT(?:\s+INTO)?\s+([^\s(]+)`)
	if m := insRe.FindStringSubmatch(cleaned); len(m) >= 2 {
		if err := s.checkTableAccess(cs, cleanIdentifier(m[1]), auth.PrivInsert); err != nil {
			return nil, err
		}
	}

	// Primary Fast-Path: Unified VDBE Virtual Machine execution (bypass if OUTPUT clause present)
	hasOutput := strings.Contains(strings.ToUpper(cleaned), " OUTPUT ")
	if !hasOutput {
		if res, err := s.execVBDEDML("INSERT", cleaned, start, cs); err == nil {
			return res, nil
		} else if strings.Contains(err.Error(), "IDENTITY_INSERT") {
			return nil, err
		}
	}

	re := regexp.MustCompile(`(?is)^INSERT\s+INTO\s+([^\s(]+)(?:\s*\(([^)]+)\))?(?:\s+OUTPUT\s+(.*?))?\s+VALUES\s*(.*)$`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 5 {
		return nil, fmt.Errorf("invalid INSERT INTO syntax")
	}

	tableName := cleanIdentifier(matches[1])
	rawCols := matches[2]
	rawOutput := strings.TrimSpace(matches[3])
	rawValues := matches[4]

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
		cs.mu.Lock()
		if cur, ok := cs.Vars["identity_insert_table"].(string); ok {
			if strings.EqualFold(cur, tableName) {
				identityInsertAllowed = true
			}
		}
		cs.mu.Unlock()
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

	tupleStrings := parseValueTuples(rawValues)
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

		t, err := session.BeginTx(storage.TxOptions{})
		if err != nil {
			return nil, err
		}
		tx = t
		isEphemeral = true
	}

	var maxID int64 = 0
	if identityCol != nil {
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
				rec[colName] = parseLiteralValue(valStr, colSchema.Type)
			}
		}

		if identityCol != nil {
			if !hasIdentityInTarget {
				maxID++
				rec[identityCol.Name] = storage.IntValue(maxID)
				if cs != nil {
					cs.mu.Lock()
					cs.Vars["identity"] = maxID
					cs.mu.Unlock()
				}
			} else {
				if v, ok := rec[identityCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull {
					if cs != nil {
						cs.mu.Lock()
						cs.Vars["identity"] = v.IVal
						cs.mu.Unlock()
					}
					if v.IVal > maxID {
						maxID = v.IVal
					}
				}
			}
		}

		if err := tx.Insert(tableName, rec); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
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

	return &QueryResult{
		Success:      true,
		Command:      "INSERT",
		Columns:      outputCols,
		Rows:         outputRows,
		RowsAffected: insertedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows inserted into %q", insertedCount, tableName)},
	}, nil
}

func (s *Server) execExplain(query string, start time.Time) (*QueryResult, error) {
	upper := strings.ToUpper(strings.TrimSpace(query))
	if strings.HasPrefix(upper, "EXPLAIN BYTECODE ") {
		innerSQL := strings.TrimSpace(query[len("EXPLAIN BYTECODE "):])
		prog, err := proc.Compile(innerSQL)
		if err != nil {
			return nil, fmt.Errorf("EXPLAIN BYTECODE compilation error: %w", err)
		}
		dis := prog.Disassemble()
		lines := strings.Split(strings.TrimRight(dis, "\n"), "\n")
		var rows []map[string]any
		for _, line := range lines {
			rows = append(rows, map[string]any{
				"Bytecode": line,
			})
		}
		return &QueryResult{
			Success:      true,
			Command:      "EXPLAIN BYTECODE",
			Columns:      []string{"Bytecode"},
			Rows:         rows,
			RowsAffected: int64(len(rows)),
			DurationUS:   time.Since(start).Microseconds(),
			Messages:     []string{fmt.Sprintf("VDBE Program: %s (%s)", prog.Name, prog.Type)},
		}, nil
	}

	opts, innerExplainSQL, err := planner.ParseExplainOptions(query)
	if err != nil {
		opts = planner.ExplainOptions{Costs: true, Format: planner.FormatText}
		innerExplainSQL = query
		if strings.HasPrefix(upper, "EXPLAIN ANALYZE ") {
			innerExplainSQL = strings.TrimSpace(query[len("EXPLAIN ANALYZE "):])
			opts.Analyze = true
		} else if strings.HasPrefix(upper, "EXPLAIN ") {
			innerExplainSQL = strings.TrimSpace(query[len("EXPLAIN "):])
		}
	}

	innerUpper := strings.ToUpper(strings.TrimSpace(innerExplainSQL))
	if strings.HasPrefix(innerUpper, "INSERT ") || strings.HasPrefix(innerUpper, "UPDATE ") || strings.HasPrefix(innerUpper, "DELETE ") {
		return s.execDMLExplain(innerExplainSQL, opts, start)
	}

	if setQuery, ok := planner.FindTopLevelSetOps(innerExplainSQL); ok && setQuery != nil && len(setQuery.Segments) >= 2 {
		return s.execSetOpExplain(setQuery, opts.Analyze, start)
	}

	p := planner.NewPlanner()
	qInfo, err := p.ParseSelect(innerExplainSQL)
	if err != nil {
		return nil, fmt.Errorf("EXPLAIN parse error: %w", err)
	}
	qInfo.IsAnalyze = opts.Analyze
	_ = p.DecorrelateSubqueries(qInfo, func(tbl string) bool {
		_, err := s.engine.GetTable(cleanIdentifier(tbl))
		return err == nil
	})

	rawTable := qInfo.TableName
	tableName := cleanIdentifier(rawTable)

	// Check if Table-Valued Function (TVF) invocation: e.g. FROM GetTopAccounts(100)
	tvfCallRe := regexp.MustCompile(`(?is)^([a-zA-Z0-9_#]+)\s*\((.*?)\)$`)
	if m := tvfCallRe.FindStringSubmatch(rawTable); len(m) == 3 {
		tvfName := cleanIdentifier(m[1])
		argsRaw := m[2]
		fnMeta, err := s.catalog.GetFunction(tvfName)
		if err == nil && fnMeta != nil && fnMeta.IsTableValued {
			var argVals []string
			if strings.TrimSpace(argsRaw) != "" {
				for _, arg := range strings.Split(argsRaw, ",") {
					argVals = append(argVals, strings.TrimSpace(arg))
				}
			}
			innerSQL := fnMeta.Body
			for i, p := range fnMeta.Params {
				if i < len(argVals) {
					innerSQL = strings.ReplaceAll(innerSQL, p.Name, argVals[i])
					innerSQL = strings.ReplaceAll(innerSQL, strings.TrimPrefix(p.Name, "@"), argVals[i])
				}
			}
			prefix := "EXPLAIN "
			if qInfo.IsAnalyze {
				prefix = "EXPLAIN ANALYZE "
			}
			return s.execExplain(prefix+innerSQL, start)
		}
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	var leftStats *planner.TableStats
	if s.catalog != nil {
		leftStats, _ = s.catalog.GetTableStats(tableName)
	}

	var totalRows int64 = 0
	if leftStats != nil && leftStats.RowCount > 0 {
		totalRows = leftStats.RowCount
	} else {
		session, err := s.engine.NewSession()
		if err == nil {
			defer session.Close()
			tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
			if err == nil {
				defer tx.Rollback()
				if cur, err := tx.Scan(tableName); err == nil {
					defer cur.Close()
					for cur.Next() {
						totalRows++
					}
				}
			}
		}
		leftStats = planner.NewTableStats(tableName, totalRows)
	}

	var planTree *planner.PlanNode
	var joins []*planner.JoinInfo
	if len(qInfo.Joins) > 0 {
		joins = qInfo.Joins
	} else if qInfo.Join != nil {
		joins = []*planner.JoinInfo{qInfo.Join}
	}

	if len(joins) > 0 {
		rightSchemas := make(map[string]*storage.TableSchema)
		rightStats := make(map[string]*planner.TableStats)
		for _, j := range joins {
			rTable := cleanIdentifier(j.Table)
			rSchema, _ := s.engine.GetTable(rTable)
			if rSchema != nil {
				rightSchemas[j.Table] = rSchema
				rightSchemas[rTable] = rSchema
				if j.Alias != "" {
					rightSchemas[j.Alias] = rSchema
					rightSchemas[cleanIdentifier(j.Alias)] = rSchema
				}
			}
			var rStat *planner.TableStats
			if s.catalog != nil {
				rStat, _ = s.catalog.GetTableStats(rTable)
			}
			if rStat == nil {
				var rRows int64 = 0
				if session, err := s.engine.NewSession(); err == nil {
					func() {
						defer session.Close()
						if rTx, err := session.BeginTx(storage.TxOptions{ReadOnly: true}); err == nil {
							defer rTx.Rollback()
							if rCur, err := rTx.Scan(rTable); err == nil {
								defer rCur.Close()
								for rCur.Next() {
									rRows++
								}
							}
						}
					}()
				}
				rStat = planner.NewTableStats(rTable, rRows)
			}
			if rStat != nil {
				rightStats[j.Table] = rStat
				rightStats[rTable] = rStat
				if j.Alias != "" {
					rightStats[j.Alias] = rStat
					rightStats[cleanIdentifier(j.Alias)] = rStat
				}
			}
		}
		planTree = p.BuildMultiJoinPlanWithStats(qInfo, schema, leftStats, rightSchemas, rightStats)
	} else {
		planTree = p.BuildMultiJoinPlanWithStats(qInfo, schema, leftStats, nil, nil)
	}

	if qInfo.IsAnalyze {
		// Execute query to collect actual metrics
		execStart := time.Now()
		selectSQL := innerExplainSQL
		res, execErr := s.execSelect(selectSQL, execStart, nil)
		if execErr == nil && res != nil {
			planTree.ActualRows = int64(len(res.Rows))
			planTree.ActualDurationUS = res.DurationUS

			// Recursively populate actual rows / durations down the tree
			var populateActuals func(node *planner.PlanNode)
			populateActuals = func(node *planner.PlanNode) {
				if node == nil {
					return
				}
				if node.ActualRows == 0 && len(res.Rows) > 0 {
					node.ActualRows = int64(len(res.Rows))
					node.ActualDurationUS = res.DurationUS
				}
				for _, child := range node.Children {
					populateActuals(child)
				}
			}
			populateActuals(planTree)
		}
	}

	if opts.Format == planner.FormatJSON {
		jsonStr, err := planner.FormatPostgresPlanJSON(planTree, qInfo.IsAnalyze)
		if err != nil {
			return nil, err
		}
		return &QueryResult{
			Success:      true,
			Command:      "EXPLAIN",
			Columns:      []string{"QUERY PLAN"},
			Rows:         []map[string]any{{"QUERY PLAN": jsonStr}},
			RowsAffected: 1,
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	lines := planner.FormatPlanText(planTree, qInfo.IsAnalyze)
	rows := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, map[string]any{
			"QUERY PLAN": line,
		})
	}

	return &QueryResult{
		Success:      true,
		Command:      "EXPLAIN",
		Columns:      []string{"QUERY PLAN"},
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execDMLExplain(dmlSQL string, opts planner.ExplainOptions, start time.Time) (*QueryResult, error) {
	upper := strings.ToUpper(strings.TrimSpace(dmlSQL))
	p := planner.NewPlanner()

	var dmlType, tableName, whereCond string
	if strings.HasPrefix(upper, "INSERT ") {
		dmlType = "INSERT"
		re := regexp.MustCompile(`(?i)^INSERT\s+INTO\s+([^\s(]+)`)
		if m := re.FindStringSubmatch(dmlSQL); len(m) > 1 {
			tableName = cleanIdentifier(m[1])
		}
	} else if strings.HasPrefix(upper, "UPDATE ") {
		dmlType = "UPDATE"
		re := regexp.MustCompile(`(?i)^UPDATE\s+([^\s]+)\s+SET\s+(.*?)(?:\s+WHERE\s+(.*))?$`)
		if m := re.FindStringSubmatch(dmlSQL); len(m) > 1 {
			tableName = cleanIdentifier(m[1])
			if len(m) > 3 {
				whereCond = strings.TrimSpace(m[3])
			}
		}
	} else if strings.HasPrefix(upper, "DELETE ") {
		dmlType = "DELETE"
		re := regexp.MustCompile(`(?i)^DELETE\s+(?:FROM\s+)?([^\s]+)(?:\s+WHERE\s+(.*))?$`)
		if m := re.FindStringSubmatch(dmlSQL); len(m) > 1 {
			tableName = cleanIdentifier(m[1])
			if len(m) > 2 {
				whereCond = strings.TrimSpace(m[2])
			}
		}
	}

	schema, _ := s.engine.GetTable(tableName)
	var stats *planner.TableStats
	if s.catalog != nil && tableName != "" {
		stats, _ = s.catalog.GetTableStats(tableName)
	}

	planTree := p.BuildDMLPlanNode(dmlType, tableName, whereCond, schema, stats, 1)

	if opts.Format == planner.FormatJSON {
		jsonStr, err := planner.FormatPostgresPlanJSON(planTree, opts.Analyze)
		if err != nil {
			return nil, err
		}
		return &QueryResult{
			Success:      true,
			Command:      "EXPLAIN",
			Columns:      []string{"QUERY PLAN"},
			Rows:         []map[string]any{{"QUERY PLAN": jsonStr}},
			RowsAffected: 1,
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	lines := planner.FormatPlanText(planTree, opts.Analyze)
	rows := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, map[string]any{"QUERY PLAN": line})
	}
	return &QueryResult{
		Success:      true,
		Command:      "EXPLAIN",
		Columns:      []string{"QUERY PLAN"},
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func findSubquery(s string, prefixPattern *regexp.Regexp) (matchStart int, matchEnd int, prefixStr string, innerSQL string, found bool) {
	return findSubqueryFrom(s, prefixPattern, 0)
}

func findSubqueryFrom(s string, prefixPattern *regexp.Regexp, startOffset int) (matchStart int, matchEnd int, prefixStr string, innerSQL string, found bool) {
	if startOffset >= len(s) {
		return 0, 0, "", "", false
	}
	loc := prefixPattern.FindStringSubmatchIndex(s[startOffset:])
	if loc == nil {
		return 0, 0, "", "", false
	}
	actualStart := loc[0] + startOffset
	openParenIdx := -1
	for i := actualStart; i < len(s); i++ {
		if s[i] == '(' {
			openParenIdx = i
			break
		}
	}
	if openParenIdx == -1 {
		return 0, 0, "", "", false
	}

	depth := 0
	closeParenIdx := -1
	for i := openParenIdx; i < len(s); i++ {
		if s[i] == '(' {
			depth++
		} else if s[i] == ')' {
			depth--
			if depth == 0 {
				closeParenIdx = i
				break
			}
		}
	}
	if closeParenIdx == -1 {
		return 0, 0, "", "", false
	}

	inner := strings.TrimSpace(s[openParenIdx+1 : closeParenIdx])
	return actualStart, closeParenIdx + 1, s[actualStart:openParenIdx], inner, true
}

func (s *Server) execAnalyze(query string, start time.Time) (*QueryResult, error) {
	parts := strings.Fields(strings.TrimSpace(query))

	var targetTable string
	if len(parts) >= 2 {
		if strings.EqualFold(parts[1], "TABLE") {
			if len(parts) >= 3 {
				targetTable = cleanIdentifier(strings.Trim(parts[2], ";"))
			}
		} else {
			targetTable = cleanIdentifier(strings.Trim(parts[1], ";"))
		}
	}

	var tablesToAnalyze []string
	if targetTable != "" {
		actualTable := targetTable
		if _, err := s.engine.GetTable(targetTable); err != nil {
			if all, listErr := s.engine.ListTables(); listErr == nil {
				for _, t := range all {
					if strings.EqualFold(t.Name, targetTable) {
						actualTable = t.Name
						break
					}
				}
			}
		}
		tablesToAnalyze = append(tablesToAnalyze, actualTable)
	} else {
		all, err := s.engine.ListTables()
		if err != nil {
			return nil, fmt.Errorf("ANALYZE failed to list tables: %w", err)
		}
		for _, t := range all {
			tablesToAnalyze = append(tablesToAnalyze, t.Name)
		}
	}

	if len(tablesToAnalyze) == 0 {
		return &QueryResult{
			Success:    true,
			Command:    "ANALYZE",
			Messages:   []string{"No tables found to analyze."},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	session, err := s.engine.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ANALYZE session error: %w", err)
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("ANALYZE transaction error: %w", err)
	}
	defer tx.Rollback()

	var messages []string
	for _, tbl := range tablesToAnalyze {
		schema, err := s.engine.GetTable(tbl)
		if err != nil {
			return nil, fmt.Errorf("table %q not found: %w", tbl, err)
		}

		cursor, err := tx.Scan(tbl)
		if err != nil {
			return nil, fmt.Errorf("ANALYZE scan error on %q: %w", tbl, err)
		}

		colVals := make(map[string][]storage.Value)
		nullCounts := make(map[string]int64)
		for _, c := range schema.Columns {
			colVals[c.Name] = make([]storage.Value, 0)
		}

		var rowCount int64 = 0
		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
				break
			}
			rowCount++
			for _, c := range schema.Columns {
				v, ok := rec[c.Name]
				if !ok || v.IsNull {
					nullCounts[c.Name]++
				} else {
					colVals[c.Name] = append(colVals[c.Name], v)
				}
			}
		}
		_ = cursor.Close()

		ts := planner.NewTableStats(tbl, rowCount)
		for _, c := range schema.Columns {
			ts.Columns[c.Name] = planner.BuildColumnStats(c.Name, c.Type, colVals[c.Name], nullCounts[c.Name], rowCount)
		}

		if s.catalog != nil {
			s.catalog.SetTableStats(tbl, ts)
		}
		messages = append(messages, fmt.Sprintf("Table %q analyzed successfully (%d rows, %d columns).", tbl, rowCount, len(schema.Columns)))
	}

	return &QueryResult{
		Success:      true,
		Command:      "ANALYZE",
		Messages:     messages,
		RowsAffected: int64(len(tablesToAnalyze)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) resolveSubqueries(whereClause string) (string, error) {
	if whereClause == "" {
		return "", nil
	}

	result := whereClause

	// 1. [NOT] EXISTS (SELECT ...)
	existsRe := regexp.MustCompile(`(?is)(?:NOT\s+)?EXISTS\s*\(\s*\(?\s*SELECT\b`)
	offset := 0
	for {
		mStart, mEnd, prefix, innerSQL, found := findSubqueryFrom(result, existsRe, offset)
		if !found {
			break
		}
		innerRes, err := s.ExecuteSQL(innerSQL)
		if err != nil {
			// If inner query failed (e.g. contains outer reference), skip so planner can decorrelate
			offset = mEnd
			continue
		}
		isNot := strings.Contains(strings.ToUpper(prefix), "NOT")
		hasRows := len(innerRes.Rows) > 0
		replacement := "1=0"
		if isNot {
			if !hasRows {
				replacement = "1=1"
			}
		} else {
			if hasRows {
				replacement = "1=1"
			}
		}
		result = result[:mStart] + replacement + result[mEnd:]
		offset = mStart + len(replacement)
	}

	// 2. [NOT] IN (SELECT ...) -> [NOT] IN (val1, val2, ...)
	inSelectRe := regexp.MustCompile(`(?is)\b([a-zA-Z0-9_\.]+)\s+(?:NOT\s+)?IN\s*\(\s*\(?\s*SELECT\b`)
	offset = 0
	for {
		mStart, mEnd, prefix, innerSQL, found := findSubqueryFrom(result, inSelectRe, offset)
		if !found {
			break
		}
		tokens := strings.Fields(prefix)
		colName := cleanIdentifier(tokens[0])
		isNot := strings.Contains(strings.ToUpper(prefix), "NOT")
		innerRes, err := s.ExecuteSQL(innerSQL)
		if err != nil {
			offset = mEnd
			continue
		}
		hasRows := len(innerRes.Rows) > 0
		replacement := "1=0"
		if isNot {
			if !hasRows {
				replacement = "1=1"
			} else {
				var vals []string
				for _, r := range innerRes.Rows {
					if len(innerRes.Columns) > 0 {
						colKey := innerRes.Columns[0]
						if v, ok := r[colKey]; ok {
							vals = append(vals, fmt.Sprintf("'%v'", v))
							continue
						}
					}
					for _, v := range r {
						vals = append(vals, fmt.Sprintf("'%v'", v))
						break
					}
				}
				if len(vals) > 0 {
					replacement = fmt.Sprintf("%s NOT IN (%s)", colName, strings.Join(vals, ", "))
				}
			}
		} else {
			if hasRows {
				var vals []string
				for _, r := range innerRes.Rows {
					if len(innerRes.Columns) > 0 {
						colKey := innerRes.Columns[0]
						if v, ok := r[colKey]; ok {
							vals = append(vals, formatSubqueryVal(v))
							continue
						}
					}
					for _, v := range r {
						vals = append(vals, formatSubqueryVal(v))
						break
					}
				}
				if len(vals) > 0 {
					replacement = fmt.Sprintf("%s IN (%s)", colName, strings.Join(vals, ", "))
				}
			}
		}
		result = result[:mStart] + replacement + result[mEnd:]
		offset = mStart + len(replacement)
	}

	// 3. Scalar Subquery in comparisons: e.g. > (SELECT ...), = (SELECT ...)
	scalarRe := regexp.MustCompile(`(?is)\(\s*SELECT\b`)
	offset = 0
	for {
		mStart, mEnd, _, innerSQL, found := findSubqueryFrom(result, scalarRe, offset)
		if !found {
			break
		}
		innerRes, err := s.ExecuteSQL(innerSQL)
		if err != nil {
			offset = mEnd
			continue
		}
		replacement := "NULL"
		if len(innerRes.Rows) > 0 {
			r := innerRes.Rows[0]
			var foundVal any
			if len(innerRes.Columns) > 0 {
				colKey := innerRes.Columns[0]
				if v, ok := r[colKey]; ok && v != nil {
					foundVal = v
				}
			}
			if foundVal == nil {
				for _, v := range r {
					if v != nil {
						foundVal = v
						break
					}
				}
			}
			if foundVal != nil {
				replacement = formatSubqueryVal(foundVal)
			}
		}
		result = result[:mStart] + replacement + result[mEnd:]
		offset = mStart + len(replacement)
	}

	return result, nil
}

func formatSubqueryVal(v any) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", val)
	case float32, float64:
		return fmt.Sprintf("%v", val)
	case bool:
		if val {
			return "1"
		}
		return "0"
	case storage.Decimal:
		return val.String()
	case string:
		if _, err := strconv.ParseFloat(val, 64); err == nil {
			return val
		}
		return fmt.Sprintf("'%s'", strings.ReplaceAll(val, "'", "''"))
	default:
		str := fmt.Sprintf("%v", val)
		if _, err := strconv.ParseFloat(str, 64); err == nil {
			return str
		}
		return fmt.Sprintf("'%s'", strings.ReplaceAll(str, "'", "''"))
	}
}

func (s *Server) execCTE(query string, start time.Time) (*QueryResult, error) {
	p := planner.NewPlanner()
	qInfo, err := p.ParseSelect(query)
	if err != nil {
		return nil, fmt.Errorf("parse CTE error: %w", err)
	}

	if len(qInfo.CTEs) == 0 {
		return s.execSelect(query, start, nil)
	}

	var tempTableNames []string
	defer func() {
		for _, t := range tempTableNames {
			_, _ = s.ExecuteSQL(fmt.Sprintf("DROP TABLE %s;", t))
		}
	}()

	mainSQL := query
	upper := strings.ToUpper(query)
	if strings.HasPrefix(upper, "WITH ") {
		depth := 0
		mainSelectIdx := -1
		for i := 0; i < len(query); i++ {
			if query[i] == '(' {
				depth++
			} else if query[i] == ')' {
				depth--
			} else if depth == 0 && strings.HasPrefix(strings.ToUpper(query[i:]), "SELECT") {
				mainSelectIdx = i
				break
			}
		}
		if mainSelectIdx != -1 {
			mainSQL = strings.TrimSpace(query[mainSelectIdx:])
		}
	}

	for _, cte := range qInfo.CTEs {
		// 1. Run CTE inner SQL
		res, err := s.ExecuteSQL(cte.InnerSQL)
		if err != nil {
			return nil, fmt.Errorf("CTE %q execution error: %w", cte.Name, err)
		}

		// 2. Materialize into #temp table
		tempName := fmt.Sprintf("#__cte_%s", cte.Name)
		tempTableNames = append(tempTableNames, tempName)

		if len(res.Columns) == 0 {
			continue
		}

		var colDefs []string
		for i, col := range res.Columns {
			pk := ""
			if i == 0 {
				pk = " PRIMARY KEY"
			}
			colDefs = append(colDefs, fmt.Sprintf("%s VARCHAR(255)%s", col, pk))
		}
		createSQL := fmt.Sprintf("CREATE TABLE %s (%s);", tempName, strings.Join(colDefs, ", "))
		if _, err := s.ExecuteSQL(createSQL); err != nil {
			return nil, fmt.Errorf("materialize CTE %q table error: %w", cte.Name, err)
		}

		if len(res.Rows) > 0 {
			var insertCols = res.Columns
			for _, row := range res.Rows {
				var valStrs []string
				for _, col := range insertCols {
					v := row[col]
					if v == nil {
						valStrs = append(valStrs, "NULL")
					} else {
						valStrs = append(valStrs, fmt.Sprintf("'%v'", v))
					}
				}
				insSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);", tempName, strings.Join(insertCols, ", "), strings.Join(valStrs, ", "))
				if _, err := s.ExecuteSQL(insSQL); err != nil {
					return nil, fmt.Errorf("materialize CTE insert error: %w", err)
				}
			}
		}

		// Rewrite occurrences of cte.Name in mainSQL with tempName
		cteWordRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(cte.Name) + `\b`)
		mainSQL = cteWordRe.ReplaceAllString(mainSQL, tempName)
	}

	return s.ExecuteSQL(mainSQL)
}

func (s *Server) execVolcanoSelect(qInfo *planner.QueryInfo, start time.Time, cs *ClientSession) (*QueryResult, error) {
	p := planner.NewPlanner()
	_ = p.DecorrelateSubqueries(qInfo, func(tbl string) bool {
		_, err := s.engine.GetTable(cleanIdentifier(tbl))
		return err == nil
	})

	if qInfo.WhereClause != "" {
		if resolved, err := s.resolveSubqueries(qInfo.WhereClause); err == nil {
			qInfo.WhereClause = resolved
		}
	}

	leftTable := cleanIdentifier(qInfo.TableName)
	leftSchema, err := s.engine.GetTable(leftTable)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", leftTable, err)
	}

	rightSchemas := make(map[string]*storage.TableSchema)
	var joins []*planner.JoinInfo
	if len(qInfo.Joins) > 0 {
		joins = qInfo.Joins
	} else if qInfo.Join != nil {
		joins = []*planner.JoinInfo{qInfo.Join}
	}

	for _, j := range joins {
		rightTable := cleanIdentifier(j.Table)
		rSchema, err := s.engine.GetTable(rightTable)
		if err != nil {
			return nil, fmt.Errorf("table %q not found: %w", rightTable, err)
		}
		rightSchemas[j.Table] = rSchema
		rightSchemas[rightTable] = rSchema
		if j.Alias != "" {
			rightSchemas[j.Alias] = rSchema
			rightSchemas[cleanIdentifier(j.Alias)] = rSchema
		}
	}

	var tx storage.Transaction
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		t, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		tx = t
		defer tx.Rollback()
	}

	var leftStats *planner.TableStats
	rightStats := make(map[string]*planner.TableStats)
	if s.catalog != nil {
		leftStats, _ = s.catalog.GetTableStats(leftTable)
		for _, j := range joins {
			rTable := cleanIdentifier(j.Table)
			if st, ok := s.catalog.GetTableStats(rTable); ok && st != nil {
				rightStats[j.Table] = st
				rightStats[rTable] = st
				if j.Alias != "" {
					rightStats[j.Alias] = st
					rightStats[cleanIdentifier(j.Alias)] = st
				}
			}
		}
	}
	plan := p.BuildMultiJoinPlanWithStats(qInfo, leftSchema, leftStats, rightSchemas, rightStats)

	opts := execution.DefaultExecutorOptions()
	if s.defaultWorkMem > 0 {
		opts.WorkMem = s.defaultWorkMem
	}
	if cs != nil {
		cs.mu.Lock()
		if wmVal, ok := cs.Vars["work_mem"]; ok {
			switch v := wmVal.(type) {
			case int64:
				opts.WorkMem = v
			case int:
				opts.WorkMem = int64(v)
			case string:
				if b, err := execution.ParseMemSize(v); err == nil {
					opts.WorkMem = b
				}
			}
		}
		cs.mu.Unlock()
	}

	exec, err := execution.BuildExecutorWithOptions(plan, tx, s.engine.GetTable, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to build volcano executor: %w", err)
	}

	ctx := context.Background()
	if cs != nil && cs.QueryCtx != nil {
		ctx = cs.QueryCtx
	}
	if err := exec.Open(ctx); err != nil {
		if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
			return nil, fmt.Errorf("canceling statement due to user request")
		}
		return nil, fmt.Errorf("volcano execution open error: %w", err)
	}
	defer exec.Close()

	var rows []map[string]any
	for {
		tuple, err := exec.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
				return nil, fmt.Errorf("canceling statement due to user request")
			}
			return nil, err
		}
		rows = append(rows, tuple.ToMap())
	}

	if qInfo.Distinct {
		rows = deduplicateRows(rows)
	}

	cols := exec.Schema()
	if len(cols) == 0 && len(rows) > 0 {
		for k := range rows[0] {
			cols = append(cols, k)
		}
		sort.Strings(cols)
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      cols,
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func hasFromClause(query string) bool {
	upper := strings.ToUpper(query)
	inSingleQuote := false
	inDoubleQuote := false
	parenDepth := 0

	for i := 0; i < len(upper); i++ {
		ch := upper[i]
		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			continue
		}
		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			continue
		}
		if inSingleQuote || inDoubleQuote {
			continue
		}
		if ch == '(' {
			parenDepth++
			continue
		}
		if ch == ')' {
			if parenDepth > 0 {
				parenDepth--
			}
			continue
		}
		if parenDepth == 0 {
			if i+4 <= len(upper) && upper[i:i+4] == "FROM" {
				before := i == 0 || !isIdentChar(upper[i-1])
				after := i+4 == len(upper) || !isIdentChar(upper[i+4])
				if before && after {
					return true
				}
			}
		}
	}
	return false
}

func splitScalarProjections(s string) []string {
	var res []string
	var cur strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	parenDepth := 0

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			cur.WriteByte(ch)
			continue
		}
		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			cur.WriteByte(ch)
			continue
		}
		if inSingleQuote || inDoubleQuote {
			cur.WriteByte(ch)
			continue
		}
		if ch == '(' {
			parenDepth++
			cur.WriteByte(ch)
			continue
		}
		if ch == ')' {
			if parenDepth > 0 {
				parenDepth--
			}
			cur.WriteByte(ch)
			continue
		}
		if ch == ',' && parenDepth == 0 {
			res = append(res, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	if cur.Len() > 0 {
		res = append(res, strings.TrimSpace(cur.String()))
	}
	return res
}

func (s *Server) evalScalarExpr(expr string, cs *ClientSession) (any, string) {
	trimmed := strings.TrimSpace(expr)
	upper := strings.ToUpper(trimmed)

	// Builtin system / database functions
	switch upper {
	case "CURRENT_DATABASE()", "CURRENT_DATABASE":
		return "sqlion", "current_database"
	case "CURRENT_SCHEMA()", "CURRENT_SCHEMA":
		return "dbo", "current_schema"
	case "CURRENT_SCHEMAS(TRUE)", "CURRENT_SCHEMAS(FALSE)", "CURRENT_SCHEMAS":
		return "{dbo,public}", "current_schemas"
	case "CURRENT_USER", "USER", "SESSION_USER":
		return "postgres", "current_user"
	case "VERSION()":
		return "PostgreSQL 15.0 (SQLion OLTP & Stored Procedure Server)", "version"
	case "DB_NAME()", "DB_NAME":
		return "sqlion", "db_name"
	case "SCHEMA_NAME()", "SCHEMA_NAME":
		return "dbo", "schema_name"
	case "PG_BACKEND_PID()", "PG_BACKEND_PID":
		if cs != nil && cs.SPID > 0 {
			return int64(cs.SPID), "pg_backend_pid"
		}
		return int64(1), "pg_backend_pid"
	case "PG_IS_IN_RECOVERY()", "PG_IS_IN_RECOVERY":
		return false, "pg_is_in_recovery"
	case "PG_TRIGGER_DEPTH()", "PG_TRIGGER_DEPTH":
		return int64(0), "pg_trigger_depth"
	case "NOW()", "GETDATE()", "CURRENT_TIMESTAMP":
		return time.Now().Format("2006-01-02 15:04:05"), "now"
	case "@@VERSION":
		return "Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64) - SQLion Engine", "@@version"
	case "@@SPID":
		if cs != nil && cs.SPID > 0 {
			return int64(cs.SPID), "@@spid"
		}
		return int64(51), "@@spid"
	case "@@TRANCOUNT":
		if cs != nil && cs.Tx != nil {
			return int64(1), "@@trancount"
		}
		return int64(0), "@@trancount"
	case "@@ROWCOUNT":
		if cs != nil {
			cs.mu.Lock()
			val, ok := cs.Vars["rowcount"]
			cs.mu.Unlock()
			if ok && val != nil {
				return val, "@@rowcount"
			}
		}
		return int64(0), "@@rowcount"
	case "@@SERVERNAME", "@@SERVICENAME":
		return "SQLION", "@@servername"
	case "@@OPTIONS":
		return int64(5432), "@@options"
	case "@@ERROR", "ERROR_NUMBER()", "ERROR_NUMBER":
		if cs != nil {
			cs.mu.Lock()
			val, ok := cs.Vars["error"]
			cs.mu.Unlock()
			if ok && val != nil {
				return val, "@@error"
			}
		}
		return int64(0), "@@error"
	case "@@IDENTITY", "SCOPE_IDENTITY()", "SCOPE_IDENTITY":
		if cs != nil {
			cs.mu.Lock()
			val, ok := cs.Vars["identity"]
			cs.mu.Unlock()
			if ok && val != nil {
				return val, "@@identity"
			}
		}
		return nil, "@@identity"
	}

	// System variable @@var
	if strings.HasPrefix(upper, "@@") {
		varName := strings.ToLower(trimmed[2:])
		if cs != nil {
			cs.mu.Lock()
			v, exists := cs.Vars[varName]
			cs.mu.Unlock()
			if exists {
				return v, trimmed
			}
		}
		return "", trimmed
	}

	// pg_cancel_backend(pid)
	if strings.HasPrefix(upper, "PG_CANCEL_BACKEND(") && strings.HasSuffix(upper, ")") {
		arg := strings.TrimSpace(trimmed[len("PG_CANCEL_BACKEND(") : len(trimmed)-1])
		if pid, err := strconv.Atoi(arg); err == nil {
			return s.CancelQueryBySPID(pid), "pg_cancel_backend"
		}
		return false, "pg_cancel_backend"
	}

	// pg_terminate_backend(pid)
	if strings.HasPrefix(upper, "PG_TERMINATE_BACKEND(") && strings.HasSuffix(upper, ")") {
		arg := strings.TrimSpace(trimmed[len("PG_TERMINATE_BACKEND(") : len(trimmed)-1])
		if pid, err := strconv.Atoi(arg); err == nil {
			canceled := s.CancelQueryBySPID(pid)
			s.sessMu.RLock()
			var targetID string
			for _, sess := range s.sessions {
				if sess.SPID == pid {
					targetID = sess.ID
					break
				}
			}
			s.sessMu.RUnlock()
			if targetID != "" {
				s.CloseSession(targetID)
			}
			return canceled || targetID != "", "pg_terminate_backend"
		}
		return false, "pg_terminate_backend"
	}

	// Session variable @var
	if strings.HasPrefix(trimmed, "@") {
		if cs != nil {
			cs.mu.Lock()
			v, exists := cs.Vars[trimmed]
			if !exists {
				v = cs.Vars[strings.TrimPrefix(trimmed, "@")]
			}
			cs.mu.Unlock()
			if v != nil {
				return v, trimmed
			}
		}
		return nil, trimmed
	}

	// String literal
	if (strings.HasPrefix(trimmed, "'") && strings.HasSuffix(trimmed, "'")) ||
		(strings.HasPrefix(trimmed, "\"") && strings.HasSuffix(trimmed, "\"")) {
		unquoted := trimmed[1 : len(trimmed)-1]
		return unquoted, "?column?"
	}

	// Integer literal
	if n, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return n, trimmed
	}

	// Float literal
	if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return f, trimmed
	}

	// Boolean literal
	if strings.EqualFold(trimmed, "TRUE") {
		return true, "bool"
	}
	if strings.EqualFold(trimmed, "FALSE") {
		return false, "bool"
	}
	if strings.EqualFold(trimmed, "NULL") {
		return nil, "?column?"
	}

	// Try evaluating expression with proc Parser and EvalRowExpr
	p := proc.NewParser(proc.NewLexer(trimmed))
	astExpr := p.ParseExpr()
	if astExpr != nil && len(p.Errors()) == 0 {
		var varRow map[string]any
		if cs != nil {
			cs.mu.Lock()
			if len(cs.Vars) > 0 {
				varRow = make(map[string]any, len(cs.Vars))
				for k, v := range cs.Vars {
					varRow[k] = v
				}
			}
			cs.mu.Unlock()
		}
		if res, err := proc.EvalRowExpr(astExpr, varRow, nil); err == nil && res != nil {
			return res, trimmed
		}
	}

	return trimmed, trimmed
}

func (s *Server) execScalarSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	upper := strings.ToUpper(cleaned)
	if !strings.HasPrefix(upper, "SELECT") {
		return nil, fmt.Errorf("invalid SELECT statement")
	}

	rawList := strings.TrimSpace(cleaned[6:])
	projItems := splitScalarProjections(rawList)
	if len(projItems) == 0 {
		return nil, fmt.Errorf("no projections specified in SELECT")
	}

	var columns []string
	row := make(map[string]any, len(projItems))

	asRe := regexp.MustCompile(`(?is)^(.*?)\s+AS\s+([a-zA-Z0-9_#$]+|"[^"]+")$`)

	for i, item := range projItems {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		exprStr := item
		alias := ""

		if m := asRe.FindStringSubmatch(item); len(m) == 3 {
			exprStr = strings.TrimSpace(m[1])
			alias = cleanIdentifier(m[2])
		} else {
			fields := strings.Fields(item)
			if len(fields) == 2 && !strings.Contains(fields[1], "(") && !strings.Contains(fields[1], ")") {
				exprStr = fields[0]
				alias = cleanIdentifier(fields[1])
			}
		}

		val, defAlias := s.evalScalarExpr(exprStr, cs)
		if alias == "" {
			alias = defAlias
		}
		if alias == "" {
			alias = fmt.Sprintf("col_%d", i+1)
		}

		colKey := alias
		if _, exists := row[colKey]; exists {
			colKey = fmt.Sprintf("%s_%d", alias, i+1)
		}

		columns = append(columns, colKey)
		row[colKey] = val
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         []map[string]any{row},
		RowsAffected: 1,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execSelectInto(targetTable, projections, fromClause string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	// 1. Check if target table already exists
	if _, err := s.engine.GetTable(targetTable); err == nil {
		return nil, fmt.Errorf("table %q already exists", targetTable)
	}

	// 2. Execute underlying SELECT query to retrieve columns and rows
	selectSQL := fmt.Sprintf("SELECT %s FROM %s", projections, fromClause)
	selRes, err := s.execSelect(selectSQL, start, cs)
	if err != nil {
		return nil, err
	}
	if !selRes.Success {
		return selRes, nil
	}

	// 3. Inspect source table schema if available for exact data types
	var srcSchema *storage.TableSchema
	fromFields := strings.Fields(fromClause)
	if len(fromFields) > 0 {
		srcTableName := cleanIdentifier(fromFields[0])
		if sch, err := s.engine.GetTable(srcTableName); err == nil {
			srcSchema = sch
		}
	}

	// 4. Construct target table schema
	schema := &storage.TableSchema{
		Name:    targetTable,
		Columns: make([]storage.Column, 0, len(selRes.Columns)),
	}

	// Determine PK candidate
	pkColName := ""
	if srcSchema != nil {
		if srcPK, err := srcSchema.PrimaryKeyColumn(); err == nil && srcPK != nil {
			for _, col := range selRes.Columns {
				if strings.EqualFold(col, srcPK.Name) {
					pkColName = col
					break
				}
			}
		}
	}

	// Check if pkColName is unique among rows
	if pkColName != "" && len(selRes.Rows) > 0 {
		seen := make(map[string]bool)
		for _, row := range selRes.Rows {
			valKey := fmt.Sprintf("%v", row[pkColName])
			if seen[valKey] {
				pkColName = "" // duplicate found
				break
			}
			seen[valKey] = true
		}
	}

	// If no unique PK found from source, check if any projected column is completely unique
	if pkColName == "" && len(selRes.Rows) > 0 {
		for _, col := range selRes.Columns {
			unique := true
			seen := make(map[string]bool)
			for _, row := range selRes.Rows {
				v := row[col]
				if v == nil {
					unique = false
					break
				}
				valKey := fmt.Sprintf("%v", v)
				if seen[valKey] {
					unique = false
					break
				}
				seen[valKey] = true
			}
			if unique {
				pkColName = col
				break
			}
		}
	}

	needSyntheticPK := false
	if pkColName == "" {
		if len(selRes.Rows) == 0 {
			if len(selRes.Columns) > 0 {
				pkColName = selRes.Columns[0]
			} else {
				needSyntheticPK = true
			}
		} else {
			needSyntheticPK = true
		}
	}

	if needSyntheticPK {
		schema.Columns = append(schema.Columns, storage.Column{
			Name:       "_row_id",
			Type:       storage.TypeInt,
			PrimaryKey: true,
			Nullable:   false,
		})
	}

	for _, colName := range selRes.Columns {
		colType := storage.TypeVarChar
		if srcSchema != nil {
			if c, _, found := srcSchema.FindColumn(colName); found {
				colType = c.Type
			}
		}
		if colType == storage.TypeVarChar && len(selRes.Rows) > 0 {
			for _, row := range selRes.Rows {
				if v, ok := row[colName]; ok && v != nil {
					switch v.(type) {
					case int, int64:
						colType = storage.TypeInt
					case float64:
						colType = storage.TypeFloat
					case bool:
						colType = storage.TypeBool
					}
					break
				}
			}
		}

		schema.Columns = append(schema.Columns, storage.Column{
			Name:       colName,
			Type:       colType,
			PrimaryKey: strings.EqualFold(colName, pkColName),
			Nullable:   true,
		})
	}

	// 5. Create table in engine
	if err := s.engine.CreateTable(schema); err != nil {
		return nil, fmt.Errorf("failed to create table %q: %w", targetTable, err)
	}

	// Register temp tables
	if strings.HasPrefix(targetTable, "##") {
		s.globalTempMu.Lock()
		if s.globalTempCreators == nil {
			s.globalTempCreators = make(map[string]string)
		}
		creatorID := "default"
		if cs != nil && cs.ID != "" {
			creatorID = cs.ID
		}
		s.globalTempCreators[targetTable] = creatorID
		s.globalTempMu.Unlock()
	} else if strings.HasPrefix(targetTable, "#") {
		if cs != nil {
			cs.mu.Lock()
			cs.TempTables = append(cs.TempTables, targetTable)
			cs.mu.Unlock()
		}
	}

	// 6. Insert rows if any
	if len(selRes.Rows) > 0 {
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

		for idx, row := range selRes.Rows {
			rec := make(storage.Record, len(schema.Columns))
			if needSyntheticPK {
				rec["_row_id"] = storage.IntValue(int64(idx + 1))
			}
			for _, col := range schema.Columns {
				if col.Name == "_row_id" {
					continue
				}
				val := row[col.Name]
				rec[col.Name] = convertValue(val, col.Type)
			}
			if err := tx.Insert(targetTable, rec); err != nil {
				if isEphemeral {
					_ = tx.Rollback()
				}
				return nil, fmt.Errorf("failed inserting row into %q: %w", targetTable, err)
			}
		}

		if isEphemeral {
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("transaction commit error: %w", err)
			}
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT INTO",
		RowsAffected: int64(len(selRes.Rows)),
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("(%d rows affected)", len(selRes.Rows))},
	}, nil
}

func (s *Server) execVBDEDML(command, cleaned string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	prog, err := proc.CompileStatement(cleaned)
	if err != nil {
		return nil, err
	}

	var tx storage.Transaction
	var isEphemeral bool
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
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
		defer func() {
			if isEphemeral && tx != nil {
				_ = tx.Rollback()
			}
		}()
	}

	var params map[string]any
	if cs != nil {
		cs.mu.Lock()
		if len(cs.Vars) > 0 {
			params = make(map[string]any, len(cs.Vars))
			for k, v := range cs.Vars {
				params[k] = v
			}
		}
		cs.mu.Unlock()
	}

	ctx := context.Background()
	if cs != nil && cs.QueryCtx != nil {
		ctx = cs.QueryCtx
	}
	execRes, err := s.executor.ExecuteInTxContext(ctx, prog, params, tx, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
			return nil, fmt.Errorf("canceling statement due to user request")
		}
		return nil, err
	}

	if isEphemeral {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("failed to commit transaction: %w", err)
		}
		tx = nil
	}

	if cs != nil {
		cs.mu.Lock()
		if execRes.LastIdentity > 0 {
			cs.Vars["identity"] = execRes.LastIdentity
			cs.Vars["@@identity"] = execRes.LastIdentity
		}
		cs.Vars["rowcount"] = execRes.RowsAffected
		cs.Vars["@@rowcount"] = execRes.RowsAffected
		cs.mu.Unlock()
	}

	msg := fmt.Sprintf("(%d rows affected)", execRes.RowsAffected)
	if len(execRes.Logs) > 0 {
		msg = strings.Join(execRes.Logs, "\n")
	}

	return &QueryResult{
		Success:      true,
		Command:      command,
		RowsAffected: execRes.RowsAffected,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{msg},
	}, nil
}

func (s *Server) execVDBESelect(cleaned string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	prog, err := proc.CompileQuery(cleaned)
	if err != nil {
		return nil, err
	}

	var tx storage.Transaction
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		t, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		tx = t
		defer tx.Rollback()
	}

	var params map[string]any
	if cs != nil {
		cs.mu.Lock()
		if len(cs.Vars) > 0 {
			params = make(map[string]any, len(cs.Vars))
			for k, v := range cs.Vars {
				params[k] = v
			}
		}
		cs.mu.Unlock()
	}

	execRes, err := s.executor.ExecuteInTx(prog, params, tx, nil)
	if err != nil {
		return nil, err
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      execRes.Columns,
		Rows:         execRes.Rows,
		RowsAffected: int64(len(execRes.Rows)),
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     execRes.Logs,
	}, nil
}

func (s *Server) execSetOpSelect(setQuery *planner.SetOpQuery, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if len(setQuery.Segments) < 2 {
		return nil, fmt.Errorf("set operator requires at least 2 query branches")
	}

	// 1. Execute branch 0
	firstRes, err := s.execSelect(setQuery.Segments[0].Query, start, cs)
	if err != nil {
		return nil, fmt.Errorf("set operation initial query failed: %w", err)
	}
	outCols := firstRes.Columns
	var currIter execution.Iterator = execution.NewSliceIterator(firstRes.Rows, outCols)

	// 2. Chained execution of subsequent branches
	for i := 1; i < len(setQuery.Segments); i++ {
		seg := setQuery.Segments[i]
		nextRes, err := s.execSelect(seg.Query, start, cs)
		if err != nil {
			return nil, fmt.Errorf("set operation branch %d (%s) failed: %w", i, seg.Op, err)
		}
		if len(nextRes.Columns) != len(outCols) {
			return nil, fmt.Errorf("all queries combined using %s must have an equal number of expressions (%d != %d)",
				seg.Op, len(outCols), len(nextRes.Columns))
		}

		rightIter := execution.NewSliceIterator(nextRes.Rows, nextRes.Columns)
		setOpType, err := execution.ParseSetOpType(seg.Op)
		if err != nil {
			return nil, err
		}

		currIter = execution.NewSetOpIterator(currIter, rightIter, setOpType)
	}

	// 3. Trailing ORDER BY across entire set operation result
	if setQuery.OrderBy != "" {
		currIter = execution.NewSortIterator(currIter, setQuery.OrderBy)
	}

	// 4. Trailing LIMIT across entire set operation result
	if setQuery.Limit >= 0 {
		currIter = execution.NewLimitIterator(currIter, setQuery.Limit, 0)
	}

	// 5. Open and materialize results
	ctx := context.Background()
	if cs != nil && cs.QueryCtx != nil {
		ctx = cs.QueryCtx
	}
	if err := currIter.Open(ctx); err != nil {
		if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
			return nil, fmt.Errorf("canceling statement due to user request")
		}
		return nil, fmt.Errorf("set operation execution open error: %w", err)
	}
	defer currIter.Close()

	var finalRows []map[string]any
	for {
		tuple, err := currIter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
				return nil, fmt.Errorf("canceling statement due to user request")
			}
			return nil, fmt.Errorf("set operation iterator error: %w", err)
		}
		finalRows = append(finalRows, tuple.ToMap())
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      outCols,
		Rows:         finalRows,
		RowsAffected: int64(len(finalRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execSetOpExplain(setQuery *planner.SetOpQuery, analyze bool, start time.Time) (*QueryResult, error) {
	p := planner.NewPlanner()
	plan, err := p.BuildSetOpPlan(setQuery, func(subSQL string) (*planner.PlanNode, error) {
		qInfo, err := p.ParseSelect(subSQL)
		if err != nil {
			return &planner.PlanNode{
				Type:          planner.NodeTableScan,
				Relation:      cleanIdentifier(subSQL),
				TotalCost:     10.0,
				EstimatedRows: 10,
			}, nil
		}
		tblName := cleanIdentifier(qInfo.TableName)
		schema, err := s.engine.GetTable(tblName)
		if err != nil {
			return &planner.PlanNode{
				Type:          planner.NodeTableScan,
				Relation:      tblName,
				TotalCost:     10.0,
				EstimatedRows: 10,
			}, nil
		}
		var stats *planner.TableStats
		if s.catalog != nil {
			stats, _ = s.catalog.GetTableStats(tblName)
		}
		return p.BuildJoinPlanWithStats(qInfo, schema, stats, nil, nil), nil
	})
	if err != nil {
		return nil, fmt.Errorf("set operation explain plan error: %w", err)
	}

	lines := planner.FormatPlanText(plan, analyze)
	rows := make([]map[string]any, len(lines))
	for i, l := range lines {
		rows[i] = map[string]any{"QUERY PLAN": l}
	}

	return &QueryResult{
		Success:      true,
		Command:      "EXPLAIN",
		Columns:      []string{"QUERY PLAN"},
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// -1. T-SQL FOR JSON clause: SELECT ... FOR JSON PATH / AUTO [,...]
	if hasForJSON(cleaned) {
		return s.execForJSONSelect(cleaned, start, cs)
	}

	// -1. View expansion: if FROM clause references a known view, rewrite as a CTE.
	//     e.g. SELECT name FROM v_engineers WHERE salary > 85000
	//     becomes WITH v_engineers AS (SELECT id, name, salary FROM employees WHERE dept='Engineering')
	//             SELECT name FROM v_engineers WHERE salary > 85000
	if cteQuery, ok := s.rewriteViewsAsCTE(cleaned); ok {
		return s.execCTE(cteQuery, start)
	}

	// 0. Set Operations: UNION [ALL], INTERSECT, EXCEPT / MINUS
	if setQuery, ok := planner.FindTopLevelSetOps(cleaned); ok && setQuery != nil && len(setQuery.Segments) >= 2 {
		return s.execSetOpSelect(setQuery, start, cs)
	}

	// 0. PIVOT: SELECT ... FROM ... PIVOT (...) ...
	if hasPivot(cleaned) {
		return s.execPivotSelect(cleaned, start, cs)
	}

	// 0. UNPIVOT: SELECT ... FROM ... UNPIVOT (...) ...
	if hasUnpivot(cleaned) {
		return s.execUnpivotSelect(cleaned, start, cs)
	}

	// 0. SELECT ... INTO <target_table> FROM ...
	selectIntoRe := regexp.MustCompile(`(?is)^SELECT\s+(.*?)\s+INTO\s+([^\s,;]+)\s+FROM\s+(.*)$`)
	if m := selectIntoRe.FindStringSubmatch(cleaned); len(m) == 4 {
		projections := strings.TrimSpace(m[1])
		targetTable := cleanIdentifier(m[2])
		fromClause := strings.TrimSpace(m[3])
		return s.execSelectInto(targetTable, projections, fromClause, start, cs)
	}

	// 0. Table-less scalar SELECT: e.g. SELECT 1, SELECT current_database(), SELECT @@VERSION
	if !hasFromClause(cleaned) {
		upperClean := strings.ToUpper(cleaned)
		if !strings.Contains(upperClean, "@@") && !strings.Contains(upperClean, "SCOPE_IDENTITY") {
			if res, err := s.execVDBESelect(cleaned, start, cs); err == nil && res != nil {
				return res, nil
			}
		}
		return s.execScalarSelect(cleaned, start, cs)
	}

	p := planner.NewPlanner()
	qInfo, parseErr := p.ParseSelect(cleaned)
	if parseErr == nil && qInfo != nil {
		_ = p.DecorrelateSubqueries(qInfo, func(tbl string) bool {
			_, err := s.engine.GetTable(cleanIdentifier(tbl))
			return err == nil
		})

		rawTable := qInfo.TableName
		tableName := cleanIdentifier(rawTable)

		// Check for Table-Valued Function (TVF) invocation: e.g. FROM GetTopAccounts(100)
		tvfCallRe := regexp.MustCompile(`(?is)^([a-zA-Z0-9_#]+)\s*\((.*?)\)$`)
		if m := tvfCallRe.FindStringSubmatch(rawTable); len(m) == 3 {
			tvfName := cleanIdentifier(m[1])
			argsRaw := m[2]
			fnMeta, err := s.catalog.GetFunction(tvfName)
			if err == nil && fnMeta != nil && fnMeta.IsTableValued {
				var argVals []string
				if strings.TrimSpace(argsRaw) != "" {
					for _, arg := range strings.Split(argsRaw, ",") {
						argVals = append(argVals, strings.TrimSpace(arg))
					}
				}
				innerSQL := fnMeta.Body
				for i, p := range fnMeta.Params {
					if i < len(argVals) {
						innerSQL = strings.ReplaceAll(innerSQL, p.Name, argVals[i])
						innerSQL = strings.ReplaceAll(innerSQL, strings.TrimPrefix(p.Name, "@"), argVals[i])
					}
				}
				return s.execSelect(innerSQL, start, cs)
			}
		}

		// Intercept Virtual System Catalog Views (sys.* and information_schema.*)
		if s.isVirtualCatalogTable(tableName) {
			return s.execVirtualCatalogSelect(tableName, strings.Join(qInfo.Projections, ", "), qInfo.WhereClause, qInfo.OrderByClause, int(qInfo.LimitCount), start)
		}

		// RBAC Permission Check for primary table and joined relations
		if err := s.checkTableAccess(cs, tableName, auth.PrivSelect); err != nil {
			return nil, err
		}
		for _, j := range qInfo.Joins {
			if err := s.checkTableAccess(cs, j.Table, auth.PrivSelect); err != nil {
				return nil, err
			}
		}
		if qInfo.Join != nil {
			if err := s.checkTableAccess(cs, qInfo.Join.Table, auth.PrivSelect); err != nil {
				return nil, err
			}
		}

		// If query has joins (including subqueries decorrelated into joins), execute via Volcano CBO Engine
		if len(qInfo.Joins) > 0 || qInfo.Join != nil {
			return s.execVolcanoSelect(qInfo, start, cs)
		}

		rawWhere := qInfo.WhereClause
		if qInfo.WhereClause != "" {
			if resolved, err := s.resolveSubqueries(qInfo.WhereClause); err == nil {
				qInfo.WhereClause = resolved
			}
		}

		// If query has subqueries in WHERE, execute via Volcano
		if strings.Contains(strings.ToUpper(rawWhere), "EXISTS") || strings.Contains(strings.ToUpper(rawWhere), "SELECT") {
			return s.execVolcanoSelect(qInfo, start, cs)
		}

		// Composite / single-column index-accelerated SELECT path.
		// Try this before VDBE so that equality conditions on indexed columns
		// benefit from prefix scans even for single-column composite indexes.
		if qInfo.WhereClause != "" && !qInfo.HasAggregates && len(qInfo.GroupByCols) == 0 {
			if res, ok := s.execIndexSelect(qInfo, tableName, start, cs); ok {
				if qInfo.Distinct {
					res.Rows = deduplicateRows(res.Rows)
					res.RowsAffected = int64(len(res.Rows))
				}
				return res, nil
			}
		}

		// Execute via unified VDBE Virtual Machine Engine (Primary Fast-Path)
		if res, err := s.execVDBESelect(cleaned, start, cs); err == nil {
			if qInfo.Distinct {
				res.Rows = deduplicateRows(res.Rows)
				res.RowsAffected = int64(len(res.Rows))
			}
			return res, nil
		}

		// Secondary Fallback: Direct Volcano execution
		if res, err := s.execVolcanoSelect(qInfo, start, cs); err == nil {
			return res, nil
		}
	}

	// Parse projection, table, WHERE, ORDER BY, LIMIT
	selectRe := regexp.MustCompile(`(?is)^SELECT\s+(.*?)\s+FROM\s+([^\s,;]+)(?:\s+WHERE\s+(.*?))?(?:\s+GROUP\s+BY\s+(.*?))?(?:\s+HAVING\s+(.*?))?(?:\s+ORDER\s+BY\s+(.*?))?(?:\s+LIMIT\s+(\d+))?$`)
	matches := selectRe.FindStringSubmatch(cleaned)
	if len(matches) < 3 {
		if !hasFromClause(cleaned) {
			return s.execScalarSelect(cleaned, start, cs)
		}
		return nil, fmt.Errorf("invalid or unsupported SELECT syntax")
	}

	projStr := strings.TrimSpace(matches[1])
	rawTable := strings.TrimSpace(matches[2])
	tableName := cleanIdentifier(rawTable)
	whereClause := ""
	if len(matches) > 3 {
		whereClause = strings.TrimSpace(matches[3])
		if resolved, err := s.resolveSubqueries(whereClause); err == nil {
			whereClause = resolved
		}
	}
	orderByClause := ""
	if len(matches) > 6 {
		orderByClause = strings.TrimSpace(matches[6])
	}
	limitCount := -1
	if len(matches) > 7 && matches[7] != "" {
		if n, err := strconv.Atoi(matches[7]); err == nil {
			limitCount = n
		}
	}

	// Check for Table-Valued Function (TVF) invocation: e.g. FROM GetTopAccounts(100)
	tvfCallRe := regexp.MustCompile(`(?is)^([a-zA-Z0-9_#]+)\s*\((.*?)\)$`)
	if m := tvfCallRe.FindStringSubmatch(rawTable); len(m) == 3 {
		tvfName := cleanIdentifier(m[1])
		argsRaw := m[2]
		fnMeta, err := s.catalog.GetFunction(tvfName)
		if err == nil && fnMeta != nil && fnMeta.IsTableValued {
			var argVals []string
			if strings.TrimSpace(argsRaw) != "" {
				for _, arg := range strings.Split(argsRaw, ",") {
					argVals = append(argVals, strings.TrimSpace(arg))
				}
			}
			innerSQL := fnMeta.Body
			for i, p := range fnMeta.Params {
				if i < len(argVals) {
					innerSQL = strings.ReplaceAll(innerSQL, p.Name, argVals[i])
					innerSQL = strings.ReplaceAll(innerSQL, strings.TrimPrefix(p.Name, "@"), argVals[i])
				}
			}
			return s.execSelect(innerSQL, start, cs)
		}
	}

	// Intercept Virtual System Catalog Views (sys.* and information_schema.*)
	if s.isVirtualCatalogTable(tableName) {
		return s.execVirtualCatalogSelect(tableName, projStr, whereClause, orderByClause, limitCount, start)
	}

	// RBAC Permission Check
	if err := s.checkTableAccess(cs, tableName, auth.PrivSelect); err != nil {
		return nil, err
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	var tx storage.Transaction
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		t, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		tx = t
		defer tx.Rollback()
	}

	var cursor storage.Cursor
	usedIndex := false

	if whereClause != "" {
		// Parse all equality conditions from the WHERE clause (AND-separated).
		// Collect col -> val pairs for composite index prefix matching.
		eqColVals := parseEqualityConditions(whereClause)

		if len(eqColVals) > 0 {
			eqColSet := make(map[string]bool, len(eqColVals))
			for col := range eqColVals {
				eqColSet[strings.ToLower(col)] = true
			}

			// Try composite multi-column prefix first, then fall back to single-column.
			if idx, prefixLen := schema.FindBestCompositeIndex(eqColSet); idx != nil && prefixLen > 0 {
				var lowVal, highVal storage.Value
				if len(idx.Columns) == 1 {
					// True single-column index: use exact typed value for point lookup.
					colName := idx.Columns[0]
					if colMeta, _, colFound := schema.FindColumn(colName); colFound && colMeta != nil {
						valStr := eqColVals[strings.ToLower(colName)]
						parsed := parseValueToType(valStr, colMeta.Type)
						lowVal = parsed
						highVal = parsed
					} else {
						lowVal = storage.StringValue(eqColVals[strings.ToLower(colName)])
						highVal = lowVal
					}
				} else {
					// Composite index: build NUL-separated prefix key range.
					// e.g. dept='Engineering' on (dept,level) → scan ["Engineering\x00", "Engineering\x00\xff...]
					prefixKey := buildCompositePrefixKey(idx.Columns[:prefixLen], eqColVals, schema)
					lowVal = storage.StringValue(prefixKey)
					highVal = storage.StringValue(prefixKey + "\xff\xff\xff\xff")
				}
				if cur, err := tx.IndexScan(tableName, idx.Name, &lowVal, &highVal); err == nil {
					cursor = cur
					usedIndex = true
				}
			} else if len(eqColVals) == 1 {
				// Fallback: single equality, original path.
				for col, val := range eqColVals {
					if idx, found := schema.FindIndexByColumn(col); found {
						if colMeta, _, colFound := schema.FindColumn(col); colFound && colMeta != nil {
							parsed := parseValueToType(val, colMeta.Type)
							if cur, err := tx.IndexScan(tableName, idx.Name, &parsed, &parsed); err == nil {
								cursor = cur
								usedIndex = true
							}
						}
					}
					break
				}
			}
		}
	}

	if !usedIndex {
		cur, err := tx.Scan(tableName)
		if err != nil {
			return nil, fmt.Errorf("scan error on %s: %w", tableName, err)
		}
		cursor = cur
	}
	defer cursor.Close()

	if qInfo != nil && qInfo.HasAggregates {
		rawRows := make([]map[string]any, 0)
		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
				break
			}
			if whereClause != "" && !evaluateWhereCondition(rec, whereClause) {
				continue
			}
			rMap := make(map[string]any)
			for _, col := range schema.Columns {
				rMap[col.Name] = formatStorageValue(rec[col.Name])
			}
			rawRows = append(rawRows, rMap)
		}
		return s.execAggregate(rawRows, qInfo, start)
	}

	// 1. Determine columns
	type fallbackProjItem struct {
		expr   string
		alias  string
		parsed proc.Expr
	}
	var projItems []fallbackProjItem
	var selectedCols []string
	isSelectAll := projStr == "*" || projStr == ""
	if isSelectAll {
		for _, col := range schema.Columns {
			selectedCols = append(selectedCols, col.Name)
			projItems = append(projItems, fallbackProjItem{expr: col.Name, alias: col.Name})
		}
	} else {
		for _, c := range planner.SplitProjections(projStr) {
			cTrimmed := strings.TrimSpace(c)
			if cTrimmed == "" {
				continue
			}
			aliasIdx := strings.Index(strings.ToUpper(cTrimmed), " AS ")
			var expr, alias string
			if aliasIdx != -1 {
				expr = strings.TrimSpace(cTrimmed[:aliasIdx])
				alias = cleanIdentifier(strings.TrimSpace(cTrimmed[aliasIdx+4:]))
			} else {
				expr = cTrimmed
				alias = cleanIdentifier(cTrimmed)
			}
			selectedCols = append(selectedCols, alias)
			item := fallbackProjItem{expr: expr, alias: alias}
			if strings.Contains(expr, "->") || strings.Contains(expr, "(") {
				p := proc.NewParser(proc.NewLexer(expr))
				if ast := p.ParseExpr(); ast != nil && len(p.Errors()) == 0 {
					item.parsed = ast
				}
			}
			projItems = append(projItems, item)
		}
	}

	// 2. Scan and filter rows
	type fullRow struct {
		row map[string]any
	}
	var scannedRows []fullRow
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			break
		}

		if whereClause != "" && !evaluateWhereCondition(rec, whereClause) {
			continue
		}

		rMap := make(map[string]any)
		for _, col := range schema.Columns {
			rMap[col.Name] = formatStorageValue(rec[col.Name])
		}
		for k, v := range rec {
			rMap[k] = formatStorageValue(v)
		}
		scannedRows = append(scannedRows, fullRow{row: rMap})
	}

	// 3. Apply ORDER BY if present
	if orderByClause != "" && len(scannedRows) > 0 {
		orderParts := strings.Fields(orderByClause)
		orderCol := cleanIdentifier(orderParts[0])
		desc := len(orderParts) > 1 && strings.EqualFold(orderParts[1], "DESC")
		sort.Slice(scannedRows, func(i, j int) bool {
			v1 := fmt.Sprintf("%v", scannedRows[i].row[orderCol])
			v2 := fmt.Sprintf("%v", scannedRows[j].row[orderCol])
			if n1, err1 := strconv.ParseFloat(v1, 64); err1 == nil {
				if n2, err2 := strconv.ParseFloat(v2, 64); err2 == nil {
					if desc {
						return n1 > n2
					}
					return n1 < n2
				}
			}
			if desc {
				return v1 > v2
			}
			return v1 < v2
		})
	}

	// 4. Apply LIMIT if present
	if limitCount >= 0 && len(scannedRows) > limitCount {
		scannedRows = scannedRows[:limitCount]
	}

	// 5. Final projection down to selectedCols
	rows := make([]map[string]any, 0, len(scannedRows))
	for _, sr := range scannedRows {
		row := make(map[string]any)
		for _, pi := range projItems {
			if pi.parsed != nil {
				if v, err := proc.EvalRowExpr(pi.parsed, sr.row, nil); err == nil {
					row[pi.alias] = v
					continue
				}
			}
			if v, ok := sr.row[pi.alias]; ok {
				row[pi.alias] = v
			} else if v, ok := sr.row[pi.expr]; ok {
				row[pi.alias] = v
			} else {
				row[pi.alias] = nil
			}
		}
		rows = append(rows, row)
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      selectedCols,
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

type aggItem struct {
	RawStr   string
	Alias    string
	FuncType string // "COUNT", "SUM", "AVG", "MIN", "MAX", "NONE"
	ArgCol   string
}

func parseAggItem(proj string) aggItem {
	proj = strings.TrimSpace(proj)
	alias := ""
	rawExpr := proj
	if idx := strings.Index(strings.ToUpper(proj), " AS "); idx != -1 {
		rawExpr = strings.TrimSpace(proj[:idx])
		alias = cleanIdentifier(strings.TrimSpace(proj[idx+4:]))
	} else if tokens := strings.Fields(proj); len(tokens) == 2 && !strings.Contains(tokens[1], "(") {
		rawExpr = strings.TrimSpace(tokens[0])
		alias = cleanIdentifier(tokens[1])
	}

	upper := strings.ToUpper(rawExpr)
	var funcType, argCol string
	if strings.HasPrefix(upper, "COUNT(") && strings.HasSuffix(upper, ")") {
		funcType = "COUNT"
		argCol = cleanIdentifier(strings.TrimSpace(rawExpr[6 : len(rawExpr)-1]))
	} else if strings.HasPrefix(upper, "SUM(") && strings.HasSuffix(upper, ")") {
		funcType = "SUM"
		argCol = cleanIdentifier(strings.TrimSpace(rawExpr[4 : len(rawExpr)-1]))
	} else if strings.HasPrefix(upper, "AVG(") && strings.HasSuffix(upper, ")") {
		funcType = "AVG"
		argCol = cleanIdentifier(strings.TrimSpace(rawExpr[4 : len(rawExpr)-1]))
	} else if strings.HasPrefix(upper, "MIN(") && strings.HasSuffix(upper, ")") {
		funcType = "MIN"
		argCol = cleanIdentifier(strings.TrimSpace(rawExpr[4 : len(rawExpr)-1]))
	} else if strings.HasPrefix(upper, "MAX(") && strings.HasSuffix(upper, ")") {
		funcType = "MAX"
		argCol = cleanIdentifier(strings.TrimSpace(rawExpr[4 : len(rawExpr)-1]))
	} else {
		funcType = "NONE"
		argCol = cleanIdentifier(rawExpr)
	}

	return aggItem{
		RawStr:   rawExpr,
		Alias:    alias,
		FuncType: funcType,
		ArgCol:   argCol,
	}
}

func (s *Server) execAggregate(rows []map[string]any, qInfo *planner.QueryInfo, start time.Time) (*QueryResult, error) {
	// 1. Parse projections into aggItems
	var items []aggItem
	var colHeaders []string
	for _, p := range qInfo.Projections {
		item := parseAggItem(p)
		items = append(items, item)
		header := item.Alias
		if header == "" {
			header = item.RawStr
		}
		colHeaders = append(colHeaders, header)
	}

	// 2. Grouping
	type groupBucket struct {
		keys map[string]any
		rows []map[string]any
	}

	var groupOrder []string
	groups := make(map[string]*groupBucket)

	if len(qInfo.GroupByCols) == 0 {
		groupOrder = append(groupOrder, "")
		groups[""] = &groupBucket{
			keys: make(map[string]any),
			rows: rows,
		}
	} else {
		for _, r := range rows {
			var keyParts []string
			keyMap := make(map[string]any)
			for _, gCol := range qInfo.GroupByCols {
				gClean := cleanIdentifier(gCol)
				var v any
				if val, ok := r[gClean]; ok {
					v = val
				} else {
					bare := gClean
					if idx := strings.Index(gClean, "."); idx != -1 {
						bare = gClean[idx+1:]
					}
					v = r[bare]
				}
				keyParts = append(keyParts, fmt.Sprintf("%v", v))
				keyMap[gClean] = v
				if idx := strings.Index(gClean, "."); idx != -1 {
					keyMap[gClean[idx+1:]] = v
				}
			}
			compositeKey := strings.Join(keyParts, "|")
			if _, exists := groups[compositeKey]; !exists {
				groups[compositeKey] = &groupBucket{
					keys: keyMap,
					rows: make([]map[string]any, 0),
				}
				groupOrder = append(groupOrder, compositeKey)
			}
			groups[compositeKey].rows = append(groups[compositeKey].rows, r)
		}
	}

	// 3. Compute Aggregates per bucket
	var aggRows []map[string]any

	for _, gKey := range groupOrder {
		bucket := groups[gKey]
		aggRow := make(map[string]any)
		if len(bucket.rows) > 0 {
			for k, v := range bucket.rows[0] {
				aggRow[k] = v
			}
		}
		for k, v := range bucket.keys {
			aggRow[k] = v
		}

		for _, item := range items {
			var val any
			switch item.FuncType {
			case "COUNT":
				if item.ArgCol == "*" || item.ArgCol == "" {
					val = int64(len(bucket.rows))
				} else {
					var count int64 = 0
					for _, r := range bucket.rows {
						v := r[item.ArgCol]
						if v == nil {
							if dotIdx := strings.Index(item.ArgCol, "."); dotIdx != -1 {
								v = r[item.ArgCol[dotIdx+1:]]
							}
						}
						if v != nil {
							count++
						}
					}
					val = count
				}
			case "SUM":
				var sum float64 = 0
				var decSum storage.Decimal
				isDec := false
				hasVal := false
				for _, r := range bucket.rows {
					v := r[item.ArgCol]
					if v == nil {
						if dotIdx := strings.Index(item.ArgCol, "."); dotIdx != -1 {
							v = r[item.ArgCol[dotIdx+1:]]
						}
					}
					if v != nil {
						strVal := fmt.Sprintf("%v", v)
						if strings.Contains(strVal, ".") {
							isDec = true
						}
						if d, err := storage.ParseDecimal(strVal); err == nil {
							decSum = decSum.Add(d)
							hasVal = true
						} else if n, err := strconv.ParseFloat(strVal, 64); err == nil {
							sum += n
							hasVal = true
						}
					}
				}
				if hasVal {
					if isDec {
						val = decSum.String()
					} else if sum == math.Trunc(sum) {
						val = int64(sum)
					} else {
						val = sum
					}
				} else {
					val = nil
				}
			case "AVG":
				var sum float64 = 0
				var decSum storage.Decimal
				isDec := false
				var count int64 = 0
				for _, r := range bucket.rows {
					v := r[item.ArgCol]
					if v == nil {
						if dotIdx := strings.Index(item.ArgCol, "."); dotIdx != -1 {
							v = r[item.ArgCol[dotIdx+1:]]
						}
					}
					if v != nil {
						strVal := fmt.Sprintf("%v", v)
						if strings.Contains(strVal, ".") {
							isDec = true
						}
						if d, err := storage.ParseDecimal(strVal); err == nil {
							decSum = decSum.Add(d)
							count++
						} else if n, err := strconv.ParseFloat(strVal, 64); err == nil {
							sum += n
							count++
						}
					}
				}
				if count > 0 {
					if isDec {
						cDec := storage.NewDecimalFromInt(count, 0)
						if q, err := decSum.Div(cDec, 4); err == nil {
							val = q.String()
						} else {
							val = decSum.String()
						}
					} else {
						avg := sum / float64(count)
						if avg == math.Trunc(avg) {
							val = int64(avg)
						} else {
							val = avg
						}
					}
				} else {
					val = nil
				}
			case "MIN":
				var minVal *float64
				for _, r := range bucket.rows {
					v := r[item.ArgCol]
					if v == nil {
						if dotIdx := strings.Index(item.ArgCol, "."); dotIdx != -1 {
							v = r[item.ArgCol[dotIdx+1:]]
						}
					}
					if v != nil {
						if n, err := strconv.ParseFloat(fmt.Sprintf("%v", v), 64); err == nil {
							if minVal == nil || n < *minVal {
								nCopy := n
								minVal = &nCopy
							}
						}
					}
				}
				if minVal != nil {
					if *minVal == math.Trunc(*minVal) {
						val = int64(*minVal)
					} else {
						val = *minVal
					}
				} else {
					val = nil
				}
			case "MAX":
				var maxVal *float64
				for _, r := range bucket.rows {
					v := r[item.ArgCol]
					if v == nil {
						if dotIdx := strings.Index(item.ArgCol, "."); dotIdx != -1 {
							v = r[item.ArgCol[dotIdx+1:]]
						}
					}
					if v != nil {
						if n, err := strconv.ParseFloat(fmt.Sprintf("%v", v), 64); err == nil {
							if maxVal == nil || n > *maxVal {
								nCopy := n
								maxVal = &nCopy
							}
						}
					}
				}
				if maxVal != nil {
					if *maxVal == math.Trunc(*maxVal) {
						val = int64(*maxVal)
					} else {
						val = *maxVal
					}
				} else {
					val = nil
				}
			default:
				if v, ok := bucket.keys[item.ArgCol]; ok {
					val = v
				} else if len(bucket.rows) > 0 {
					val = bucket.rows[0][item.ArgCol]
				}
			}

			header := item.Alias
			if header == "" {
				header = item.RawStr
			}
			aggRow[header] = val
			aggRow[item.RawStr] = val
			if item.Alias != "" {
				aggRow[item.Alias] = val
			}
			if item.FuncType != "NONE" {
				aggRow[strings.ToLower(item.RawStr)] = val
				aggRow[strings.ToUpper(item.RawStr)] = val
			}
		}

		// 4. Evaluate HAVING clause
		if qInfo.HavingClause != "" && !evaluateWhereConditionMap(aggRow, qInfo.HavingClause) {
			continue
		}

		// Collect aggregated row
		aggRows = append(aggRows, aggRow)
	}

	// 5. Apply ORDER BY
	if qInfo.OrderByClause != "" && len(aggRows) > 0 {
		orderParts := strings.Fields(qInfo.OrderByClause)
		orderCol := cleanIdentifier(orderParts[0])
		bareCol := orderCol
		if idx := strings.Index(bareCol, "."); idx != -1 {
			bareCol = bareCol[idx+1:]
		}
		desc := len(orderParts) > 1 && strings.EqualFold(orderParts[1], "DESC")
		sort.SliceStable(aggRows, func(i, j int) bool {
			v1, ok1 := aggRows[i][orderCol]
			if !ok1 || v1 == nil {
				v1 = aggRows[i][bareCol]
			}
			v2, ok2 := aggRows[j][orderCol]
			if !ok2 || v2 == nil {
				v2 = aggRows[j][bareCol]
			}
			s1 := fmt.Sprintf("%v", v1)
			s2 := fmt.Sprintf("%v", v2)
			if n1, err1 := strconv.ParseFloat(s1, 64); err1 == nil {
				if n2, err2 := strconv.ParseFloat(s2, 64); err2 == nil {
					if desc {
						return n1 > n2
					}
					return n1 < n2
				}
			}
			if desc {
				return s1 > s2
			}
			return s1 < s2
		})
	}

	// 6. Apply LIMIT
	if qInfo.LimitCount >= 0 && int64(len(aggRows)) > qInfo.LimitCount {
		aggRows = aggRows[:qInfo.LimitCount]
	}

	// 7. Project final rows strictly to colHeaders
	finalRows := make([]map[string]any, len(aggRows))
	for i, r := range aggRows {
		fr := make(map[string]any, len(colHeaders))
		for _, h := range colHeaders {
			fr[h] = r[h]
		}
		finalRows[i] = fr
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      colHeaders,
		Rows:         finalRows,
		RowsAffected: int64(len(finalRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execJoin(qInfo *planner.QueryInfo, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if qInfo.WhereClause != "" {
		if resolved, err := s.resolveSubqueries(qInfo.WhereClause); err == nil {
			qInfo.WhereClause = resolved
		}
	}
	rightTable := cleanIdentifier(qInfo.Join.Table)
	rightSchema, err := s.engine.GetTable(rightTable)
	if err == nil && rightSchema != nil {
		rightCol := cleanIdentifier(qInfo.Join.RightKey)
		if idx := strings.Index(rightCol, "."); idx != -1 {
			rightCol = rightCol[idx+1:]
		}
		if idxMeta, found := rightSchema.FindIndexByColumn(rightCol); found {
			return s.execNestedLoopJoin(qInfo, start, idxMeta, cs)
		}
	}
	return s.execHashJoin(qInfo, start, cs)
}

func (s *Server) execNestedLoopJoin(qInfo *planner.QueryInfo, start time.Time, rightIdx *storage.IndexSchema, cs *ClientSession) (*QueryResult, error) {
	leftTable := cleanIdentifier(qInfo.TableName)
	rightTable := cleanIdentifier(qInfo.Join.Table)

	leftSchema, err := s.engine.GetTable(leftTable)
	if err != nil {
		return nil, fmt.Errorf("left table %q not found: %w", leftTable, err)
	}
	rightSchema, err := s.engine.GetTable(rightTable)
	if err != nil {
		return nil, fmt.Errorf("right table %q not found: %w", rightTable, err)
	}

	var tx storage.Transaction
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		t, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		tx = t
		defer tx.Rollback()
	}

	leftCol := cleanIdentifier(qInfo.Join.LeftKey)
	if idx := strings.Index(leftCol, "."); idx != -1 {
		leftCol = leftCol[idx+1:]
	}

	type projField struct {
		expr  string
		alias string
	}
	var projFields []projField
	var selectedCols []string
	isSelectAll := len(qInfo.Projections) == 0 || (len(qInfo.Projections) == 1 && qInfo.Projections[0] == "*")
	if isSelectAll {
		for _, col := range leftSchema.Columns {
			projFields = append(projFields, projField{expr: col.Name, alias: col.Name})
			selectedCols = append(selectedCols, col.Name)
		}
		for _, col := range rightSchema.Columns {
			projFields = append(projFields, projField{expr: col.Name, alias: col.Name})
			selectedCols = append(selectedCols, col.Name)
		}
	} else {
		for _, p := range qInfo.Projections {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			asIdx := strings.Index(strings.ToUpper(p), " AS ")
			if asIdx != -1 {
				expr := cleanIdentifier(strings.TrimSpace(p[:asIdx]))
				alias := cleanIdentifier(strings.TrimSpace(p[asIdx+4:]))
				projFields = append(projFields, projField{expr: expr, alias: alias})
				selectedCols = append(selectedCols, alias)
			} else {
				fields := strings.Fields(p)
				if len(fields) == 2 {
					expr := cleanIdentifier(fields[0])
					alias := cleanIdentifier(fields[1])
					projFields = append(projFields, projField{expr: expr, alias: alias})
					selectedCols = append(selectedCols, alias)
				} else {
					col := cleanIdentifier(p)
					projFields = append(projFields, projField{expr: col, alias: col})
					selectedCols = append(selectedCols, col)
				}
			}
		}
	}

	// Outer Scan on leftTable
	lCur, err := tx.Scan(leftTable)
	if err != nil {
		return nil, fmt.Errorf("scan error on %s: %w", leftTable, err)
	}
	defer lCur.Close()

	var joinedRows []map[string]any

	for lCur.Next() {
		lRec, err := lCur.Record()
		if err != nil {
			break
		}
		lMap := make(map[string]any)
		for _, col := range leftSchema.Columns {
			v := formatStorageValue(lRec[col.Name])
			lMap[col.Name] = v
			lMap[leftTable+"."+col.Name] = v
			if qInfo.TableAlias != "" {
				lMap[qInfo.TableAlias+"."+col.Name] = v
			}
		}

		leftVal, ok := lRec[leftCol]
		matchedCount := 0

		if ok && !leftVal.IsNull {
			// Inner Index Scan on rightTable
			rIdxCur, err := tx.IndexScan(rightTable, rightIdx.Name, &leftVal, &leftVal)
			if err == nil {
				for rIdxCur.Next() {
					rRec, err := rIdxCur.Record()
					if err != nil {
						break
					}
					matchedCount++
					rMap := make(map[string]any)
					for _, col := range rightSchema.Columns {
						v := formatStorageValue(rRec[col.Name])
						rMap[col.Name] = v
						rMap[rightTable+"."+col.Name] = v
						if qInfo.Join.Alias != "" {
							rMap[qInfo.Join.Alias+"."+col.Name] = v
						}
					}

					combined := make(map[string]any)
					for k, v := range lMap {
						combined[k] = v
					}
					for k, v := range rMap {
						combined[k] = v
					}

					if qInfo.WhereClause != "" && !evaluateWhereConditionMap(combined, qInfo.WhereClause) {
						continue
					}

					row := make(map[string]any)
					for k, v := range combined {
						row[k] = v
					}
					for _, pf := range projFields {
						var val any
						if v, ok := combined[pf.expr]; ok {
							val = v
						} else {
							bare := pf.expr
							if idx := strings.Index(pf.expr, "."); idx != -1 {
								bare = pf.expr[idx+1:]
							}
							if v, ok := combined[bare]; ok {
								val = v
							}
						}
						row[pf.alias] = val
						row[pf.expr] = val
						if dotIdx := strings.Index(pf.expr, "."); dotIdx != -1 {
							row[pf.expr[dotIdx+1:]] = val
						}
					}
					joinedRows = append(joinedRows, row)
				}
				_ = rIdxCur.Close()
			}
		}

		if matchedCount == 0 && qInfo.Join.Type == "LEFT" {
			combined := make(map[string]any)
			for k, v := range lMap {
				combined[k] = v
			}
			for _, col := range rightSchema.Columns {
				combined[col.Name] = nil
				combined[rightTable+"."+col.Name] = nil
				if qInfo.Join.Alias != "" {
					combined[qInfo.Join.Alias+"."+col.Name] = nil
				}
			}

			if qInfo.WhereClause != "" && !evaluateWhereConditionMap(combined, qInfo.WhereClause) {
				continue
			}

			row := make(map[string]any)
			for k, v := range combined {
				row[k] = v
			}
			for _, pf := range projFields {
				var val any
				if v, ok := combined[pf.expr]; ok {
					val = v
				} else {
					bare := pf.expr
					if idx := strings.Index(pf.expr, "."); idx != -1 {
						bare = pf.expr[idx+1:]
					}
					if v, ok := combined[bare]; ok {
						val = v
					}
				}
				row[pf.alias] = val
				row[pf.expr] = val
				if dotIdx := strings.Index(pf.expr, "."); dotIdx != -1 {
					row[pf.expr[dotIdx+1:]] = val
				}
			}
			joinedRows = append(joinedRows, row)
		}
	}

	// Apply ORDER BY
	if qInfo.OrderByClause != "" && len(joinedRows) > 0 {
		orderParts := strings.Fields(qInfo.OrderByClause)
		orderCol := cleanIdentifier(orderParts[0])
		desc := len(orderParts) > 1 && strings.EqualFold(orderParts[1], "DESC")
		bareCol := orderCol
		if dotIdx := strings.Index(orderCol, "."); dotIdx != -1 {
			bareCol = orderCol[dotIdx+1:]
		}
		sort.Slice(joinedRows, func(i, j int) bool {
			val1, ok1 := joinedRows[i][orderCol]
			if !ok1 {
				val1 = joinedRows[i][bareCol]
			}
			val2, ok2 := joinedRows[j][orderCol]
			if !ok2 {
				val2 = joinedRows[j][bareCol]
			}
			v1 := fmt.Sprintf("%v", val1)
			v2 := fmt.Sprintf("%v", val2)
			if n1, err1 := strconv.ParseFloat(v1, 64); err1 == nil {
				if n2, err2 := strconv.ParseFloat(v2, 64); err2 == nil {
					if desc {
						return n1 > n2
					}
					return n1 < n2
				}
			}
			if desc {
				return v1 > v2
			}
			return v1 < v2
		})
	}

	// Apply LIMIT
	if qInfo.LimitCount >= 0 && int64(len(joinedRows)) > qInfo.LimitCount {
		joinedRows = joinedRows[:qInfo.LimitCount]
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      selectedCols,
		Rows:         joinedRows,
		RowsAffected: int64(len(joinedRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execHashJoin(qInfo *planner.QueryInfo, start time.Time, cs *ClientSession) (*QueryResult, error) {
	leftTable := cleanIdentifier(qInfo.TableName)
	rightTable := cleanIdentifier(qInfo.Join.Table)

	leftSchema, err := s.engine.GetTable(leftTable)
	if err != nil {
		return nil, fmt.Errorf("left table %q not found: %w", leftTable, err)
	}
	rightSchema, err := s.engine.GetTable(rightTable)
	if err != nil {
		return nil, fmt.Errorf("right table %q not found: %w", rightTable, err)
	}

	var tx storage.Transaction
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		t, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		tx = t
		defer tx.Rollback()
	}

	// Extract join columns (strip dot prefix if present)
	leftCol := cleanIdentifier(qInfo.Join.LeftKey)
	if idx := strings.Index(leftCol, "."); idx != -1 {
		leftCol = leftCol[idx+1:]
	}
	rightCol := cleanIdentifier(qInfo.Join.RightKey)
	if idx := strings.Index(rightCol, "."); idx != -1 {
		rightCol = rightCol[idx+1:]
	}

	// 1. Build Phase: scan right table into hash map
	rCur, err := tx.Scan(rightTable)
	if err != nil {
		return nil, fmt.Errorf("scan error on %s: %w", rightTable, err)
	}
	defer rCur.Close()

	hashTable := make(map[string][]map[string]any)
	for rCur.Next() {
		rRec, err := rCur.Record()
		if err != nil {
			break
		}
		val, ok := rRec[rightCol]
		if !ok || val.IsNull {
			continue
		}
		key := fmt.Sprintf("%v", formatStorageValue(val))

		rMap := make(map[string]any)
		for _, col := range rightSchema.Columns {
			v := formatStorageValue(rRec[col.Name])
			rMap[col.Name] = v
			rMap[rightTable+"."+col.Name] = v
			if qInfo.Join.Alias != "" {
				rMap[qInfo.Join.Alias+"."+col.Name] = v
			}
		}
		hashTable[key] = append(hashTable[key], rMap)
	}

	// 2. Probe Phase: scan left table and probe hash map
	lCur, err := tx.Scan(leftTable)
	if err != nil {
		return nil, fmt.Errorf("scan error on %s: %w", leftTable, err)
	}
	defer lCur.Close()

	type projField struct {
		expr  string
		alias string
	}
	var projFields []projField
	var selectedCols []string
	isSelectAll := len(qInfo.Projections) == 0 || (len(qInfo.Projections) == 1 && qInfo.Projections[0] == "*")
	if isSelectAll {
		for _, col := range leftSchema.Columns {
			projFields = append(projFields, projField{expr: col.Name, alias: col.Name})
			selectedCols = append(selectedCols, col.Name)
		}
		for _, col := range rightSchema.Columns {
			projFields = append(projFields, projField{expr: col.Name, alias: col.Name})
			selectedCols = append(selectedCols, col.Name)
		}
	} else {
		for _, p := range qInfo.Projections {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			asIdx := strings.Index(strings.ToUpper(p), " AS ")
			if asIdx != -1 {
				expr := cleanIdentifier(strings.TrimSpace(p[:asIdx]))
				alias := cleanIdentifier(strings.TrimSpace(p[asIdx+4:]))
				projFields = append(projFields, projField{expr: expr, alias: alias})
				selectedCols = append(selectedCols, alias)
			} else {
				fields := strings.Fields(p)
				if len(fields) == 2 {
					expr := cleanIdentifier(fields[0])
					alias := cleanIdentifier(fields[1])
					projFields = append(projFields, projField{expr: expr, alias: alias})
					selectedCols = append(selectedCols, alias)
				} else {
					col := cleanIdentifier(p)
					projFields = append(projFields, projField{expr: col, alias: col})
					selectedCols = append(selectedCols, col)
				}
			}
		}
	}

	var joinedRows []map[string]any

	for lCur.Next() {
		lRec, err := lCur.Record()
		if err != nil {
			break
		}
		lMap := make(map[string]any)
		for _, col := range leftSchema.Columns {
			v := formatStorageValue(lRec[col.Name])
			lMap[col.Name] = v
			lMap[leftTable+"."+col.Name] = v
			if qInfo.TableAlias != "" {
				lMap[qInfo.TableAlias+"."+col.Name] = v
			}
		}

		val, ok := lRec[leftCol]
		var matchedRows []map[string]any
		if ok && !val.IsNull {
			key := fmt.Sprintf("%v", formatStorageValue(val))
			matchedRows = hashTable[key]
		}

		if len(matchedRows) > 0 {
			for _, rMap := range matchedRows {
				combined := make(map[string]any)
				for k, v := range lMap {
					combined[k] = v
				}
				for k, v := range rMap {
					combined[k] = v
				}

				if qInfo.WhereClause != "" && !evaluateWhereConditionMap(combined, qInfo.WhereClause) {
					continue
				}

				row := make(map[string]any)
				for k, v := range combined {
					row[k] = v
				}
				for _, pf := range projFields {
					var val any
					if v, ok := combined[pf.expr]; ok {
						val = v
					} else {
						bare := pf.expr
						if idx := strings.Index(pf.expr, "."); idx != -1 {
							bare = pf.expr[idx+1:]
						}
						if v, ok := combined[bare]; ok {
							val = v
						}
					}
					row[pf.alias] = val
					row[pf.expr] = val
					if dotIdx := strings.Index(pf.expr, "."); dotIdx != -1 {
						row[pf.expr[dotIdx+1:]] = val
					}
				}
				joinedRows = append(joinedRows, row)
			}
		} else if qInfo.Join.Type == "LEFT" {
			combined := make(map[string]any)
			for k, v := range lMap {
				combined[k] = v
			}
			for _, col := range rightSchema.Columns {
				combined[col.Name] = nil
				combined[rightTable+"."+col.Name] = nil
				if qInfo.Join.Alias != "" {
					combined[qInfo.Join.Alias+"."+col.Name] = nil
				}
			}

			if qInfo.WhereClause != "" && !evaluateWhereConditionMap(combined, qInfo.WhereClause) {
				continue
			}

			row := make(map[string]any)
			for _, pf := range projFields {
				var val any
				if v, ok := combined[pf.expr]; ok {
					val = v
				} else {
					bare := pf.expr
					if idx := strings.Index(pf.expr, "."); idx != -1 {
						bare = pf.expr[idx+1:]
					}
					if v, ok := combined[bare]; ok {
						val = v
					}
				}
				row[pf.alias] = val
				row[pf.expr] = val
				if dotIdx := strings.Index(pf.expr, "."); dotIdx != -1 {
					row[pf.expr[dotIdx+1:]] = val
				}
			}
			joinedRows = append(joinedRows, row)
		}
	}

	// 3. Apply ORDER BY
	if qInfo.OrderByClause != "" && len(joinedRows) > 0 {
		orderParts := strings.Fields(qInfo.OrderByClause)
		orderCol := cleanIdentifier(orderParts[0])
		desc := len(orderParts) > 1 && strings.EqualFold(orderParts[1], "DESC")
		bareCol := orderCol
		if dotIdx := strings.Index(orderCol, "."); dotIdx != -1 {
			bareCol = orderCol[dotIdx+1:]
		}
		sort.Slice(joinedRows, func(i, j int) bool {
			val1, ok1 := joinedRows[i][orderCol]
			if !ok1 {
				val1 = joinedRows[i][bareCol]
			}
			val2, ok2 := joinedRows[j][orderCol]
			if !ok2 {
				val2 = joinedRows[j][bareCol]
			}
			v1 := fmt.Sprintf("%v", val1)
			v2 := fmt.Sprintf("%v", val2)
			if n1, err1 := strconv.ParseFloat(v1, 64); err1 == nil {
				if n2, err2 := strconv.ParseFloat(v2, 64); err2 == nil {
					if desc {
						return n1 > n2
					}
					return n1 < n2
				}
			}
			if desc {
				return v1 > v2
			}
			return v1 < v2
		})
	}

	// 4. Apply LIMIT
	if qInfo.LimitCount >= 0 && int64(len(joinedRows)) > qInfo.LimitCount {
		joinedRows = joinedRows[:qInfo.LimitCount]
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      selectedCols,
		Rows:         joinedRows,
		RowsAffected: int64(len(joinedRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execUpdate(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// RBAC Permission Check
	updRe := regexp.MustCompile(`(?is)^UPDATE\s+([^\s]+)`)
	if m := updRe.FindStringSubmatch(cleaned); len(m) >= 2 {
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
	re := regexp.MustCompile(`(?is)^UPDATE\s+([^\s]+)\s+SET\s+(.*?)(?:\s+OUTPUT\s+(.*?))?(?:\s+WHERE\s+(.*))?$`)
	matches := re.FindStringSubmatch(cleaned)
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

		t, err := session.BeginTx(storage.TxOptions{})
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

		pkVal, hasPK := rec["id"]
		if !hasPK {
			for _, v := range rec {
				pkVal = v
				hasPK = true
				break
			}
		}

		recUpdates := make(storage.Record)
		for colName, exprStr := range updatesMap {
			colSchema, _, found := schema.FindColumn(colName)
			targetType := storage.TypeVarChar
			if found {
				targetType = colSchema.Type
			}
			recUpdates[colName] = evalUpdateExpr(exprStr, rec, targetType)
		}

		if hasPK {
			origClone := make(storage.Record, len(rec))
			for k, v := range rec {
				origClone[k] = v
			}
			toUpdate = append(toUpdate, updateEntry{pk: pkVal, rec: recUpdates, orig: origClone})
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

func (s *Server) execDelete(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// RBAC Permission Check
	delRe := regexp.MustCompile(`(?is)^DELETE(?:\s+FROM)?\s+([^\s;]+)`)
	if m := delRe.FindStringSubmatch(cleaned); len(m) >= 2 {
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
	re := regexp.MustCompile(`(?is)^DELETE\s+(?:FROM\s+)?([^\s;]+)(?:\s+OUTPUT\s+(.*?))?(?:\s+WHERE\s+(.*))?$`)
	matches := re.FindStringSubmatch(cleaned)
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

		t, err := session.BeginTx(storage.TxOptions{})
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

		pkVal, hasPK := rec["id"]
		if !hasPK {
			for _, v := range rec {
				pkVal = v
				hasPK = true
				break
			}
		}

		if hasPK {
			origClone := make(storage.Record, len(rec))
			for k, v := range rec {
				origClone[k] = v
			}
			toDelete = append(toDelete, deleteEntry{pk: pkVal, orig: origClone})
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


func parseValueTuples(s string) []string {
	var tuples []string
	var cur strings.Builder
	inTuple := false
	inString := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' && (i == 0 || s[i-1] != '\\') {
			inString = !inString
			cur.WriteByte(c)
			continue
		}
		if inString {
			cur.WriteByte(c)
			continue
		}

		if c == '(' {
			inTuple = true
			cur.Reset()
			continue
		}
		if c == ')' {
			if inTuple {
				tuples = append(tuples, cur.String())
				inTuple = false
				cur.Reset()
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
	var vals []string
	var cur strings.Builder
	inString := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' && (i == 0 || s[i-1] != '\\') {
			inString = !inString
			cur.WriteByte(c)
			continue
		}
		if !inString && c == ',' {
			vals = append(vals, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if cur.Len() > 0 {
		vals = append(vals, strings.TrimSpace(cur.String()))
	}
	return vals
}

func parseLiteralValue(s string, targetType storage.DataType) storage.Value {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "NULL") {
		return storage.NullValue(targetType)
	}
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
		strVal := s[1 : len(s)-1]
		strVal = strings.ReplaceAll(strVal, "''", "'")
		if targetType == storage.TypeJSON {
			return storage.JSONValue(strVal)
		}
		if targetType == storage.TypeUUID {
			return storage.UUIDValue(strVal)
		}
		if targetType == storage.TypeDate {
			return storage.DateValueFromString(strVal)
		}
		if targetType == storage.TypeTime {
			return storage.TimeValueFromString(strVal)
		}
		if targetType == storage.TypeBinary {
			return storage.BinaryValueFromString(strVal)
		}
		return storage.StringValue(strVal)
	}

	switch targetType {
	case storage.TypeJSON:
		return storage.JSONValue(s)
	case storage.TypeUUID:
		return storage.UUIDValue(s)
	case storage.TypeBinary:
		return storage.BinaryValueFromString(s)
	case storage.TypeDate:
		return storage.DateValueFromString(s)
	case storage.TypeTime:
		return storage.TimeValueFromString(s)
	case storage.TypeInt:
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return storage.IntValue(i)
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.IntValue(int64(f))
		}
	case storage.TypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.FloatValue(f)
		}
	case storage.TypeDecimal:
		return storage.DecimalValue(s)
	case storage.TypeBool:
		if strings.EqualFold(s, "TRUE") || s == "1" {
			return storage.BoolValue(true)
		}
		if strings.EqualFold(s, "FALSE") || s == "0" {
			return storage.BoolValue(false)
		}
	}
	return storage.StringValue(s)
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
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "NULL") {
		return storage.NullValue(t)
	}
	s = strings.Trim(s, "'\"")
	switch t {
	case storage.TypeInt:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return storage.IntValue(n)
		}
	case storage.TypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.FloatValue(f)
		}
	case storage.TypeDecimal:
		return storage.DecimalValue(s)
	case storage.TypeBool:
		if b, err := strconv.ParseBool(s); err == nil {
			return storage.BoolValue(b)
		}
	case storage.TypeDateTime:
		if tVal, err := time.Parse(time.RFC3339, s); err == nil {
			return storage.DateTimeValue(tVal)
		}
	case storage.TypeDate:
		return storage.DateValueFromString(s)
	case storage.TypeTime:
		return storage.TimeValueFromString(s)
	case storage.TypeUUID:
		return storage.UUIDValue(s)
	case storage.TypeBinary:
		return storage.BinaryValueFromString(s)
	}
	return storage.StringValue(s)
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

	inRe := regexp.MustCompile(`(?is)^\s*([a-zA-Z0-9_\.]+)\s+(NOT\s+)?IN\s*\((.*?)\)\s*$`)
	if m := inRe.FindStringSubmatch(whereStr); len(m) == 4 {
		colName := cleanIdentifier(m[1])
		isNot := strings.TrimSpace(m[2]) != ""
		valList := m[3]
		val, ok := rec[colName]
		if !ok {
			if idx := strings.Index(colName, "."); idx != -1 {
				val, ok = rec[colName[idx+1:]]
			}
		}
		if !ok || val == nil {
			return false
		}
		strVal := fmt.Sprintf("%v", val)
		found := false
		for _, rawItem := range strings.Split(valList, ",") {
			item := strings.Trim(strings.TrimSpace(rawItem), "'\"")
			if strings.EqualFold(strVal, item) {
				found = true
				break
			}
		}
		if isNot {
			return !found
		}
		return found
	}

	ops := []string{"!=", "<>", "<=", ">=", "=", "<", ">", " LIKE "}
	for _, op := range ops {
		idx := strings.Index(strings.ToUpper(whereStr), op)
		if idx != -1 {
			leftCol := cleanIdentifier(whereStr[:idx])
			rightVal := strings.TrimSpace(whereStr[idx+len(op):])
			rightVal = strings.Trim(rightVal, "';\"")

			if leftCol == "1" && rightVal == "1" && op == "=" {
				return true
			}
			if leftCol == "1" && rightVal == "0" && op == "=" {
				return false
			}

			val, ok := rec[leftCol]
			if !ok {
				if dotIdx := strings.Index(leftCol, "."); dotIdx != -1 {
					val, ok = rec[leftCol[dotIdx+1:]]
				}
			}
			if !ok {
				return false
			}

			strVal := fmt.Sprintf("%v", val)
			switch strings.TrimSpace(op) {
			case "=":
				return strings.EqualFold(strVal, rightVal)
			case "!=", "<>":
				return !strings.EqualFold(strVal, rightVal)
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
			case "LIKE":
				pat := strings.ReplaceAll(regexp.QuoteMeta(rightVal), "%", ".*")
				pat = strings.ReplaceAll(pat, "_", ".")
				matched, _ := regexp.MatchString("(?i)^"+pat+"$", strVal)
				return matched
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
//   SELECT name FROM v_eng WHERE salary > 80000
//   -> WITH v_eng AS (SELECT ... FROM employees WHERE dept='Engineering')
//      SELECT name FROM v_eng WHERE salary > 80000
//
// Multiple views are accumulated into a single WITH clause.
// Returns the rewritten query and true if at least one view was found.
func (s *Server) rewriteViewsAsCTE(query string) (string, bool) {
	views := s.catalog.ListViews()
	if len(views) == 0 {
		return query, false
	}

	// Collect all views referenced in this query (word-boundary match against view names).
	var cteParts []string
	for _, v := range views {
		esc := regexp.QuoteMeta(v.Name)
		// Match the view name as a standalone identifier (not inside quotes or parens).
		re := regexp.MustCompile(`(?i)\b` + esc + `\b`)
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
	eqRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*=\s*(.+?)\s*$`)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if m := eqRe.FindStringSubmatch(part); len(m) == 3 {
			rawCol := m[1]
			// Strip table qualifier (tbl.col -> col)
			if dot := strings.LastIndex(rawCol, "."); dot != -1 {
				rawCol = rawCol[dot+1:]
			}
			val := strings.Trim(strings.TrimSpace(m[2]), "'\"")
			result[strings.ToLower(rawCol)] = val
		}
	}
	return result
}

// splitOnAnd splits a WHERE clause on top-level AND keywords,
// respecting parentheses depth so subexpressions are not split.
func splitOnAnd(where string) []string {
	var parts []string
	depth := 0
	start := 0
	upper := strings.ToUpper(where)
	for i := 0; i < len(upper); i++ {
		switch upper[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 && i+4 <= len(upper) && upper[i:i+4] == " AND" &&
			(i+4 == len(upper) || upper[i+4] == ' ' || upper[i+4] == '(') {
			parts = append(parts, strings.TrimSpace(where[start:i]))
			i += 4
			start = i
		}
	}
	parts = append(parts, strings.TrimSpace(where[start:]))
	return parts
}

// buildCompositePrefixKey builds a \x00-separated composite key string from the
// prefix columns of a composite index given an equality-condition value map.
// The value lookup is case-insensitive on column names.
func buildCompositePrefixKey(cols []string, eqColVals map[string]string, schema *storage.TableSchema) string {
	var parts []string
	for _, col := range cols {
		val, ok := eqColVals[strings.ToLower(col)]
		if !ok {
			break
		}
		// Format the value using storage.EncodeIndexKeyPart for consistent composite key encoding.
		colMeta, _, found := schema.FindColumn(col)
		if found && colMeta != nil {
			sv := parseValueToType(val, colMeta.Type)
			parts = append(parts, storage.EncodeIndexKeyPart(sv))
		} else {
			parts = append(parts, storage.EncodeIndexKeyPart(storage.StringValue(val)))
		}
	}
	return strings.Join(parts, "\x00")
}

type indexRangeCond struct {
	hasMin bool
	minVal string
	hasMax bool
	maxVal string
}

func parseWhereConditions(where string) (map[string]string, map[string]*indexRangeCond) {
	eqs := make(map[string]string)
	ranges := make(map[string]*indexRangeCond)
	parts := splitOnAnd(where)

	eqRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*=\s*(.+?)\s*$`)
	gteRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*>=\s*(.+?)\s*$`)
	gtRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*>\s*(.+?)\s*$`)
	lteRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*<=\s*(.+?)\s*$`)
	ltRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*<\s*(.+?)\s*$`)
	betweenRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s+BETWEEN\s+(.+?)\s+AND\s+(.+?)\s*$`)

	cleanCol := func(s string) string {
		raw := strings.TrimSpace(s)
		if dot := strings.LastIndex(raw, "."); dot != -1 {
			raw = raw[dot+1:]
		}
		return strings.ToLower(cleanIdentifier(raw))
	}
	cleanVal := func(s string) string {
		return strings.Trim(strings.TrimSpace(s), "'\"")
	}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if m := eqRe.FindStringSubmatch(part); len(m) == 3 {
			col := cleanCol(m[1])
			eqs[col] = cleanVal(m[2])
		} else if m := betweenRe.FindStringSubmatch(part); len(m) == 4 {
			col := cleanCol(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &indexRangeCond{}
				ranges[col] = r
			}
			r.hasMin = true
			r.minVal = cleanVal(m[2])
			r.hasMax = true
			r.maxVal = cleanVal(m[3])
		} else if m := gteRe.FindStringSubmatch(part); len(m) == 3 {
			col := cleanCol(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &indexRangeCond{}
				ranges[col] = r
			}
			r.hasMin = true
			r.minVal = cleanVal(m[2])
		} else if m := gtRe.FindStringSubmatch(part); len(m) == 3 {
			col := cleanCol(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &indexRangeCond{}
				ranges[col] = r
			}
			r.hasMin = true
			r.minVal = cleanVal(m[2])
		} else if m := lteRe.FindStringSubmatch(part); len(m) == 3 {
			col := cleanCol(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &indexRangeCond{}
				ranges[col] = r
			}
			r.hasMax = true
			r.maxVal = cleanVal(m[2])
		} else if m := ltRe.FindStringSubmatch(part); len(m) == 3 {
			col := cleanCol(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &indexRangeCond{}
				ranges[col] = r
			}
			r.hasMax = true
			r.maxVal = cleanVal(m[2])
		}
	}
	return eqs, ranges
}

func (s *Server) findIndexAndBounds(schema *storage.TableSchema, whereClause string) (*storage.IndexSchema, *storage.Value, *storage.Value, bool) {
	if schema == nil || len(schema.Indexes) == 0 || whereClause == "" {
		return nil, nil, nil, false
	}
	eqColVals, rangeCols := parseWhereConditions(whereClause)
	eqColSet := make(map[string]bool, len(eqColVals))
	for col := range eqColVals {
		eqColSet[col] = true
	}

	// 1. Try composite index first
	idx, prefixLen := schema.FindBestCompositeIndex(eqColSet)
	if idx != nil && len(idx.Columns) > 1 {
		if prefixLen > 0 {
			prefixKey := buildCompositePrefixKey(idx.Columns[:prefixLen], eqColVals, schema)
			// Check if next column has a range condition
			if prefixLen < len(idx.Columns) {
				nextCol := strings.ToLower(idx.Columns[prefixLen])
				if r, ok := rangeCols[nextCol]; ok && r != nil {
					nextMeta, _, found := schema.FindColumn(nextCol)
					if found && nextMeta != nil {
						lowStr := prefixKey
						highStr := prefixKey + "\xff\xff\xff\xff"
						if r.hasMin {
							encMin := storage.EncodeIndexKeyPart(parseValueToType(r.minVal, nextMeta.Type))
							lowStr = prefixKey + "\x00" + encMin
						}
						if r.hasMax {
							encMax := storage.EncodeIndexKeyPart(parseValueToType(r.maxVal, nextMeta.Type))
							highStr = prefixKey + "\x00" + encMax + "\xff\xff\xff\xff"
						}
						lowVal := storage.StringValue(lowStr)
						highVal := storage.StringValue(highStr)
						return idx, &lowVal, &highVal, true
					}
				}
			}
			// Pure prefix match on composite index
			lowVal := storage.StringValue(prefixKey)
			highVal := storage.StringValue(prefixKey + "\xff\xff\xff\xff")
			return idx, &lowVal, &highVal, true
		}
	}

	// 2. Try single-column index
	for i := range schema.Indexes {
		candIdx := &schema.Indexes[i]
		if len(candIdx.Columns) != 1 {
			continue
		}
		colName := strings.ToLower(candIdx.Columns[0])
		colMeta, _, found := schema.FindColumn(colName)
		if !found || colMeta == nil {
			continue
		}
		// Check equality
		if valStr, ok := eqColVals[colName]; ok {
			parsed := parseValueToType(valStr, colMeta.Type)
			return candIdx, &parsed, &parsed, true
		}
		// Check range
		if r, ok := rangeCols[colName]; ok && r != nil {
			var lowVal, highVal *storage.Value
			if r.hasMin {
				v := parseValueToType(r.minVal, colMeta.Type)
				lowVal = &v
			}
			if r.hasMax {
				v := parseValueToType(r.maxVal, colMeta.Type)
				highVal = &v
			}
			return candIdx, lowVal, highVal, true
		}
	}

	return nil, nil, nil, false
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
func (s *Server) execIndexSelect(qInfo *planner.QueryInfo, tableName string, start time.Time, cs *ClientSession) (*QueryResult, bool) {
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
			if sv, ok := targetRec[p.column]; ok {
				val = formatStorageValue(sv)
			}
		}
		row[p.alias] = val
		cols = append(cols, p.alias)
	}
	return row, cols
}

func evalUpdateExpr(exprStr string, rec storage.Record, targetType storage.DataType) storage.Value {
	arithRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_]+)\s*([\+\-\*\/])\s*([0-9\.]+)\s*$`)
	if m := arithRe.FindStringSubmatch(exprStr); len(m) == 4 {
		colName := cleanIdentifier(m[1])
		op := m[2]
		numStr := m[3]

		if curVal, ok := rec[colName]; ok && !curVal.IsNull {
			switch targetType {
			case storage.TypeInt:
				curInt := curVal.IVal
				delta, err := strconv.ParseInt(numStr, 10, 64)
				if err == nil {
					switch op {
					case "+":
						return storage.IntValue(curInt + delta)
					case "-":
						return storage.IntValue(curInt - delta)
					case "*":
						return storage.IntValue(curInt * delta)
					case "/":
						if delta != 0 {
							return storage.IntValue(curInt / delta)
						}
					}
				}
			case storage.TypeFloat:
				curFloat := curVal.FVal
				delta, err := strconv.ParseFloat(numStr, 64)
				if err == nil {
					switch op {
					case "+":
						return storage.FloatValue(curFloat + delta)
					case "-":
						return storage.FloatValue(curFloat - delta)
					case "*":
						return storage.FloatValue(curFloat * delta)
					case "/":
						if delta != 0 {
							return storage.FloatValue(curFloat / delta)
						}
					}
				}
			}
		}
	}

	return parseLiteralValue(exprStr, targetType)
}

// -----------------------------------------------------------------------------
// PIVOT Execution Engine
// -----------------------------------------------------------------------------

func hasPivot(query string) bool {
	return regexp.MustCompile(`(?i)\bPIVOT\s*\(`).MatchString(query)
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

	re := regexp.MustCompile(`(?i)\bPIVOT\s*\(`)
	loc := re.FindStringIndex(trimmed)
	if loc == nil {
		return nil, fmt.Errorf("no PIVOT clause found")
	}
	pivotIdx := loc[0]

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

	depth = 0
	closeParen := -1
	for i := openParen; i < len(trimmed); i++ {
		if trimmed[i] == '(' {
			depth++
		} else if trimmed[i] == ')' {
			depth--
			if depth == 0 {
				closeParen = i
				break
			}
		}
	}
	if closeParen == -1 {
		return nil, fmt.Errorf("malformed PIVOT syntax: missing closing parenthesis")
	}

	pivotBody := strings.TrimSpace(trimmed[openParen+1 : closeParen])
	afterPivot := strings.TrimSpace(trimmed[closeParen+1:])

	// Parse pivot body: <agg_func>(<agg_col>) FOR <pivot_col> IN (<pivot_vals>)
	pivotBodyRe := regexp.MustCompile(`(?is)^\s*([a-zA-Z0-9_]+)\s*\((.*?)\)\s+FOR\s+([a-zA-Z0-9_#\[\].]+)\s+IN\s*\((.*?)\)\s*$`)
	m := pivotBodyRe.FindStringSubmatch(pivotBody)
	if len(m) != 5 {
		return nil, fmt.Errorf("malformed PIVOT clause: %s", pivotBody)
	}

	aggFunc := strings.ToUpper(strings.TrimSpace(m[1]))
	aggCol := cleanIdentifier(strings.TrimSpace(m[2]))
	pivotCol := cleanIdentifier(strings.TrimSpace(m[3]))
	rawPivotVals := strings.TrimSpace(m[4])

	var pivotValues []string
	for _, val := range strings.Split(rawPivotVals, ",") {
		cleanVal := cleanIdentifier(strings.Trim(strings.TrimSpace(val), "'\""))
		if cleanVal != "" {
			pivotValues = append(pivotValues, cleanVal)
		}
	}

	// Parse afterPivot: [AS] <alias> [WHERE <where>] [ORDER BY <order>]
	var orderByClause, whereClause, pivotAlias string
	orderRe := regexp.MustCompile(`(?is)\s+ORDER\s+BY\s+(.*)$`)
	if mOrder := orderRe.FindStringSubmatch(afterPivot); len(mOrder) == 2 {
		orderByClause = strings.TrimSpace(mOrder[1])
		afterPivot = afterPivot[:len(afterPivot)-len(mOrder[0])]
	}
	whereRe := regexp.MustCompile(`(?is)\s+WHERE\s+(.*)$`)
	if mWhere := whereRe.FindStringSubmatch(afterPivot); len(mWhere) == 2 {
		whereClause = strings.TrimSpace(mWhere[1])
		afterPivot = afterPivot[:len(afterPivot)-len(mWhere[0])]
	}
	pivotAlias = cleanIdentifier(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(afterPivot), "AS ")))

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

func (s *Server) execPivotSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
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
			if len(vals) > 0 {
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
				case "COUNT":
					count := 0
					for _, v := range vals {
						if v != nil {
							count++
						}
					}
					aggResult = count
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
		orderParts := strings.Fields(pvt.OrderByClause)
		orderCol := cleanIdentifier(orderParts[0])
		desc := len(orderParts) > 1 && strings.EqualFold(orderParts[1], "DESC")
		sort.Slice(projectedRows, func(i, j int) bool {
			v1 := projectedRows[i][orderCol]
			v2 := projectedRows[j][orderCol]
			if v1 == nil && v2 != nil {
				return desc
			}
			if v1 != nil && v2 == nil {
				return !desc
			}
			if v1 == nil && v2 == nil {
				return false
			}
			f1, err1 := strconv.ParseFloat(fmt.Sprintf("%v", v1), 64)
			f2, err2 := strconv.ParseFloat(fmt.Sprintf("%v", v2), 64)
			if err1 == nil && err2 == nil {
				if desc {
					return f1 > f2
				}
				return f1 < f2
			}
			s1 := fmt.Sprintf("%v", v1)
			s2 := fmt.Sprintf("%v", v2)
			if desc {
				return s1 > s2
			}
			return s1 < s2
		})
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

func (s *Server) evalSimpleRowWhere(row map[string]any, where string) bool {
	if where == "" {
		return true
	}
	parts := splitOnAnd(where)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		condRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_#\[\].]+)\s*(=|!=|<>|>=|<=|>|<)\s*(.+?)\s*$`)
		m := condRe.FindStringSubmatch(part)
		if len(m) != 4 {
			continue
		}
		rawCol := cleanIdentifier(m[1])
		op := m[2]
		targetStr := strings.Trim(strings.TrimSpace(m[3]), "'\"")

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

func (s *Server) evalPivotExpr(row map[string]any, expr string) any {
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
	return regexp.MustCompile(`(?i)\bUNPIVOT\s*\(`).MatchString(query)
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

	re := regexp.MustCompile(`(?i)\bUNPIVOT\s*\(`)
	loc := re.FindStringIndex(trimmed)
	if loc == nil {
		return nil, fmt.Errorf("no UNPIVOT clause found")
	}
	unpivotIdx := loc[0]

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

	depth = 0
	closeParen := -1
	for i := openParen; i < len(trimmed); i++ {
		if trimmed[i] == '(' {
			depth++
		} else if trimmed[i] == ')' {
			depth--
			if depth == 0 {
				closeParen = i
				break
			}
		}
	}
	if closeParen == -1 {
		return nil, fmt.Errorf("malformed UNPIVOT syntax: missing closing parenthesis")
	}

	unpivotBody := strings.TrimSpace(trimmed[openParen+1 : closeParen])
	afterUnpivot := strings.TrimSpace(trimmed[closeParen+1:])

	// Parse unpivot body: <val_col> FOR <name_col> IN (<cols>)
	unpivotBodyRe := regexp.MustCompile(`(?is)^\s*([a-zA-Z0-9_#\[\].]+)\s+FOR\s+([a-zA-Z0-9_#\[\].]+)\s+IN\s*\((.*?)\)\s*$`)
	m := unpivotBodyRe.FindStringSubmatch(unpivotBody)
	if len(m) != 4 {
		return nil, fmt.Errorf("malformed UNPIVOT clause: %s", unpivotBody)
	}

	valCol := cleanIdentifier(strings.TrimSpace(m[1]))
	nameCol := cleanIdentifier(strings.TrimSpace(m[2]))
	rawCols := strings.TrimSpace(m[3])

	var unpivotCols []string
	for _, col := range strings.Split(rawCols, ",") {
		cleanCol := cleanIdentifier(strings.Trim(strings.TrimSpace(col), "'\""))
		if cleanCol != "" {
			unpivotCols = append(unpivotCols, cleanCol)
		}
	}

	var orderByClause, whereClause, unpivotAlias string
	orderRe := regexp.MustCompile(`(?is)\s+ORDER\s+BY\s+(.*)$`)
	if mOrder := orderRe.FindStringSubmatch(afterUnpivot); len(mOrder) == 2 {
		orderByClause = strings.TrimSpace(mOrder[1])
		afterUnpivot = afterUnpivot[:len(afterUnpivot)-len(mOrder[0])]
	}
	whereRe := regexp.MustCompile(`(?is)\s+WHERE\s+(.*)$`)
	if mWhere := whereRe.FindStringSubmatch(afterUnpivot); len(mWhere) == 2 {
		whereClause = strings.TrimSpace(mWhere[1])
		afterUnpivot = afterUnpivot[:len(afterUnpivot)-len(mWhere[0])]
	}
	unpivotAlias = cleanIdentifier(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(afterUnpivot), "AS ")))

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

func (s *Server) execUnpivotSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
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
		orderParts := strings.Fields(unpvt.OrderByClause)
		orderCol := cleanIdentifier(orderParts[0])
		desc := len(orderParts) > 1 && strings.EqualFold(orderParts[1], "DESC")
		sort.Slice(projectedRows, func(i, j int) bool {
			v1 := projectedRows[i][orderCol]
			v2 := projectedRows[j][orderCol]
			if v1 == nil && v2 != nil {
				return desc
			}
			if v1 != nil && v2 == nil {
				return !desc
			}
			if v1 == nil && v2 == nil {
				return false
			}
			f1, err1 := strconv.ParseFloat(fmt.Sprintf("%v", v1), 64)
			f2, err2 := strconv.ParseFloat(fmt.Sprintf("%v", v2), 64)
			if err1 == nil && err2 == nil {
				if desc {
					return f1 > f2
				}
				return f1 < f2
			}
			s1 := fmt.Sprintf("%v", v1)
			s2 := fmt.Sprintf("%v", v2)
			if desc {
				return s1 > s2
			}
			return s1 < s2
		})
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

func hasForJSON(query string) bool {
	return regexp.MustCompile(`(?i)\bFOR\s+JSON\s+(PATH|AUTO)\b`).MatchString(query)
}

type forJSONOptions struct {
	BaseQuery           string
	Mode                string // "PATH" or "AUTO"
	WithoutArrayWrapper bool
	RootName            string
	IncludeNullValues   bool
}

func parseForJSONQuery(query string) (*forJSONOptions, error) {
	re := regexp.MustCompile(`(?i)\bFOR\s+JSON\s+(PATH|AUTO)(.*)$`)
	loc := re.FindStringSubmatchIndex(query)
	if loc == nil {
		return nil, fmt.Errorf("no FOR JSON clause found")
	}

	baseQuery := strings.TrimSpace(query[:loc[0]])
	mode := strings.ToUpper(query[loc[2]:loc[3]])
	rawOptions := ""
	if len(loc) >= 6 && loc[4] != -1 {
		rawOptions = strings.TrimSpace(query[loc[4]:loc[5]])
	}

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
				rootRe := regexp.MustCompile(`(?i)^ROOT(?:\s*\(\s*['"]?([^'"]+)['"]?\s*\))?`)
				if m := rootRe.FindStringSubmatch(p); len(m) >= 2 && m[1] != "" {
					opts.RootName = m[1]
				} else {
					opts.RootName = "root"
				}
			}
		}
	}

	return opts, nil
}

func (s *Server) execForJSONSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	opts, err := parseForJSONQuery(query)
	if err != nil {
		return nil, err
	}

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

			// Support nested dot notation: e.g. "department.name" -> {"department": {"name": ...}}
			cleanCol := cleanIdentifier(col)
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


