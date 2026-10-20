package engine

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/execution"
	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

var (
	reExplainTvfCall      = regexp.MustCompile(`(?is)^([a-zA-Z0-9_#]+)\s*\((.*?)\)$`)
	reExplainInsert       = regexp.MustCompile(`(?i)^INSERT\s+INTO\s+([^\s(]+)`)
	reExplainUpdate       = regexp.MustCompile(`(?i)^UPDATE\s+([^\s]+)\s+SET\s+(.*?)(?:\s+WHERE\s+(.*))?$`)
	reExplainDelete       = regexp.MustCompile(`(?i)^DELETE\s+(?:FROM\s+)?([^\s]+)(?:\s+WHERE\s+(.*))?$`)
	reExplainExists       = regexp.MustCompile(`(?is)(?:NOT\s+)?EXISTS\s*\(\s*\(?\s*SELECT\b`)
	reExplainInSelect     = regexp.MustCompile(`(?is)\b([a-zA-Z0-9_\.]+)\s+(?:NOT\s+)?IN\s*\(\s*\(?\s*SELECT\b`)
	reExplainScalarSelect = regexp.MustCompile(`(?is)\(\s*SELECT\b`)
)

func (s *Coordinator) execExplain(query string, start time.Time) (*QueryResult, error) {
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

	if hasApply(innerExplainSQL) {
		lines := []string{
			"-> Lateral Apply (Nested Loop / Correlated Row Evaluation)",
			fmt.Sprintf("   Query: %s", innerExplainSQL),
		}
		var rows []map[string]any
		for _, l := range lines {
			rows = append(rows, map[string]any{"QUERY PLAN": l})
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

	if hasDerivedTables(innerExplainSQL) {
		rewritten, tempTables, err := s.resolveDerivedTables(innerExplainSQL, start, nil)
		if err != nil {
			return nil, err
		}
		defer func() {
			for _, tbl := range tempTables {
				_ = s.engine.DropTable(tbl)
			}
		}()
		innerExplainSQL = rewritten
	}

	p := s.newPlanner()
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
	if m := reExplainTvfCall.FindStringSubmatch(rawTable); len(m) == 3 {
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
		execStart := time.Now()
		ranVolcano := false

		// 1. Try running Volcano execution engine with real-time operator instrumentation
		if session, sessErr := s.engine.NewSession(); sessErr == nil {
			func() {
				defer session.Close()
				if tx, txErr := session.BeginTx(storage.TxOptions{ReadOnly: true}); txErr == nil {
					defer tx.Rollback()

					execOpts := execution.DefaultExecutorOptions()
					if s.defaultWorkMem > 0 {
						execOpts.WorkMem = s.defaultWorkMem
					}

					it, buildErr := execution.BuildInstrumentedExecutor(planTree, tx, s.engine.GetTable, execOpts)
					if buildErr == nil && it != nil {
						ctx := context.Background()
						if openErr := it.Open(ctx); openErr == nil {
							defer it.Close()
							chunk := execution.GetChunk()
							defer execution.PutChunk(chunk)

							for {
								pErr := execution.PullChunk(ctx, it, chunk, execution.DefaultChunkSize)
								if pErr != nil {
									break
								}
							}
							ranVolcano = true
						}
					}
				}
			}()
		}

		// 2. Fallback to execSelect if Volcano execution was not applicable
		if !ranVolcano || planTree.ActualLoops == 0 {
			selectSQL := innerExplainSQL
			res, execErr := s.execSelect(selectSQL, execStart, nil)
			if execErr == nil && res != nil {
				planTree.ActualRows = int64(len(res.Rows))
				planTree.ActualDurationUS = res.DurationUS
				planTree.ActualLoops = 1

				// Recursively populate actual rows / durations down the tree
				var populateActuals func(node *planner.PlanNode)
				populateActuals = func(node *planner.PlanNode) {
					if node == nil {
						return
					}
					if node.ActualRows == 0 && len(res.Rows) > 0 {
						node.ActualRows = int64(len(res.Rows))
						node.ActualDurationUS = res.DurationUS
						node.ActualLoops = 1
					}
					for _, child := range node.Children {
						populateActuals(child)
					}
				}
				populateActuals(planTree)
			}
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

func (s *Coordinator) execDMLExplain(dmlSQL string, opts planner.ExplainOptions, start time.Time) (*QueryResult, error) {
	upper := strings.ToUpper(strings.TrimSpace(dmlSQL))
	p := s.newPlanner()

	var dmlType, tableName, whereCond string
	if strings.HasPrefix(upper, "INSERT ") {
		dmlType = "INSERT"
		if m := reExplainInsert.FindStringSubmatch(dmlSQL); len(m) > 1 {
			tableName = cleanIdentifier(m[1])
		}
	} else if strings.HasPrefix(upper, "UPDATE ") {
		dmlType = "UPDATE"
		if m := reExplainUpdate.FindStringSubmatch(dmlSQL); len(m) > 1 {
			tableName = cleanIdentifier(m[1])
			if len(m) > 3 {
				whereCond = strings.TrimSpace(m[3])
			}
		}
	} else if strings.HasPrefix(upper, "DELETE ") {
		dmlType = "DELETE"
		if m := reExplainDelete.FindStringSubmatch(dmlSQL); len(m) > 1 {
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
	if opts.Analyze {
		planTree.ActualLoops = 1
		planTree.ActualDurationUS = time.Since(start).Microseconds()
		for _, c := range planTree.Children {
			c.ActualLoops = 1
			c.ActualDurationUS = planTree.ActualDurationUS
			c.ActualRows = c.EstimatedRows
		}
	}

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

func (s *Coordinator) execAnalyze(query string, start time.Time) (*QueryResult, error) {
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

	var messages []string
	for _, tbl := range tablesToAnalyze {
		ts, err := s.AnalyzeTable(tbl)
		if err != nil {
			return nil, err
		}
		schema, _ := s.engine.GetTable(tbl)
		numCols := 0
		if schema != nil {
			numCols = len(schema.Columns)
		}
		messages = append(messages, fmt.Sprintf("Table %q analyzed successfully (%d rows, %d columns).", tbl, ts.RowCount, numCols))
	}

	return &QueryResult{
		Success:      true,
		Command:      "ANALYZE",
		Messages:     messages,
		RowsAffected: int64(len(tablesToAnalyze)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// AnalyzeTable scans the given table, builds parallel column statistics, and updates the catalog.
func (s *Coordinator) AnalyzeTable(tbl string) (*planner.TableStats, error) {
	schema, err := s.engine.GetTable(tbl)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tbl, err)
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

	cursor, err := tx.Scan(tbl)
	if err != nil {
		return nil, fmt.Errorf("ANALYZE scan error on %q: %w", tbl, err)
	}
	defer cursor.Close()

	colVals := make(map[string][]storage.Value)
	nullCounts := make(map[string]int64)
	for _, c := range schema.Columns {
		colVals[c.Name] = make([]storage.Value, 0)
	}

	type candGroup struct {
		name string
		cols []string
	}
	var candGroups []candGroup
	if s.catalog != nil {
		for _, statDef := range s.catalog.ListStatistics(tbl) {
			candGroups = append(candGroups, candGroup{name: statDef.Name, cols: statDef.Columns})
		}
	}
	for _, idx := range schema.Indexes {
		if len(idx.Columns) > 1 {
			candGroups = append(candGroups, candGroup{name: idx.Name, cols: idx.Columns})
		}
	}

	multiSets := make([]map[string]struct{}, len(candGroups))
	for i := range candGroups {
		multiSets[i] = make(map[string]struct{})
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

		for i, grp := range candGroups {
			var key strings.Builder
			for j, colName := range grp.cols {
				if j > 0 {
					key.WriteByte(0)
				}
				v := rec[colName]
				if v.IsNull {
					key.WriteString("\x01NULL")
				} else {
					key.WriteString(v.String())
				}
			}
			multiSets[i][key.String()] = struct{}{}
		}
	}

	ts := planner.BuildTableStatsParallel(tbl, schema, colVals, nullCounts, rowCount)
	for i, grp := range candGroups {
		distinctCount := int64(len(multiSets[i]))
		dep := planner.ComputeFunctionalDependency(grp.cols, distinctCount, ts.Columns, rowCount)
		ts.AddMultiColumnStats(grp.name, grp.cols, distinctCount, dep)
	}

	if s.catalog != nil {
		s.catalog.SetTableStats(tbl, ts)
	}
	if s.autoAnalyzer != nil {
		s.autoAnalyzer.Reset(tbl)
	}
	return ts, nil
}

func (s *Coordinator) resolveSubqueries(whereClause string) (string, error) {
	return s.resolveSubqueriesWithSession(whereClause, nil)
}

func (s *Coordinator) resolveSubqueriesWithSession(whereClause string, cs *ClientSession) (string, error) {
	if whereClause == "" {
		return "", nil
	}

	execSubquery := func(sql string) (*QueryResult, error) {
		if cs != nil {
			return s.ExecuteSessionSQL(cs.ID, sql)
		}
		return s.ExecuteSQL(sql)
	}

	result := whereClause

	// 1. [NOT] EXISTS (SELECT ...)
	offset := 0
	for {
		mStart, mEnd, prefix, innerSQL, found := findSubqueryFrom(result, reExplainExists, offset)
		if !found {
			break
		}
		innerRes, err := execSubquery(innerSQL)
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
	offset = 0
	for {
		mStart, mEnd, prefix, innerSQL, found := findSubqueryFrom(result, reExplainInSelect, offset)
		if !found {
			break
		}
		tokens := strings.Fields(prefix)
		colName := cleanIdentifier(tokens[0])
		isNot := strings.Contains(strings.ToUpper(prefix), "NOT")
		innerRes, err := execSubquery(innerSQL)
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
					row := NewSQLRow(r, innerRes.Columns)
					if rowVal, ok := row.GetFirst(); ok && rowVal != nil {
						vals = append(vals, fmt.Sprintf("'%v'", rowVal))
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
					row := NewSQLRow(r, innerRes.Columns)
					if rowVal, ok := row.GetFirst(); ok && rowVal != nil {
						vals = append(vals, formatSubqueryVal(rowVal))
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
	offset = 0
	for {
		mStart, mEnd, _, innerSQL, found := findSubqueryFrom(result, reExplainScalarSelect, offset)
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
			row := NewSQLRow(innerRes.Rows[0], innerRes.Columns)
			if foundVal, ok := row.GetFirst(); ok && foundVal != nil {
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

func (s *Coordinator) execCTE(query string, start time.Time) (*QueryResult, error) {
	p := s.newPlanner()
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
		colDefs = append(colDefs, "_cte_row_id INT IDENTITY PRIMARY KEY")
		for _, col := range res.Columns {
			colType := "VARCHAR(255)"
			if len(res.Rows) > 0 {
				for _, row := range res.Rows {
					v := row[col]
					if v != nil {
						switch v.(type) {
						case int, int32, int64:
							colType = "INT"
						case float32, float64:
							colType = "FLOAT"
						case bool:
							colType = "BOOLEAN"
						case time.Time:
							colType = "DATETIME"
						case storage.Decimal:
							colType = "DECIMAL(18, 4)"
						}
						break
					}
				}
			}
			colDefs = append(colDefs, fmt.Sprintf("%s %s", col, colType))
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
						switch val := v.(type) {
						case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
							valStrs = append(valStrs, fmt.Sprintf("%d", val))
						case float32, float64:
							valStrs = append(valStrs, fmt.Sprintf("%f", val))
						case bool:
							if val {
								valStrs = append(valStrs, "1")
							} else {
								valStrs = append(valStrs, "0")
							}
						default:
							sVal := fmt.Sprintf("%v", v)
							valStrs = append(valStrs, fmt.Sprintf("'%s'", strings.ReplaceAll(sVal, "'", "''")))
						}
					}
				}
				insSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);", tempName, strings.Join(insertCols, ", "), strings.Join(valStrs, ", "))
				if _, err := s.ExecuteSQL(insSQL); err != nil {
					return nil, fmt.Errorf("materialize CTE insert error: %w", err)
				}
			}
		}

		// Rewrite occurrences of cte.Name in mainSQL with tempName
		cteWordRe := getCompiledRegexp(`\b` + regexp.QuoteMeta(cte.Name) + `\b`)
		mainSQL = cteWordRe.ReplaceAllString(mainSQL, tempName)
	}

	return s.ExecuteSQL(mainSQL)
}
