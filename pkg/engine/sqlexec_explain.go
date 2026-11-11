package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"lionsql/pkg/execution"
	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

type subqueryKind int

const (
	subqueryKindExists subqueryKind = iota
	subqueryKindIn
	subqueryKindQuantified
	subqueryKindScalar
)

func (s *Coordinator) execExplain(query string, start time.Time) (*QueryResult, error) {
	if innerSQL, ok := sqlutil.TrimPrefixKeyword(query, "EXPLAIN BYTECODE"); ok {
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
		if rem, ok := sqlutil.TrimPrefixKeyword(query, "EXPLAIN ANALYZE"); ok {
			innerExplainSQL = rem
			opts.Analyze = true
		} else if rem, ok := sqlutil.TrimPrefixKeyword(query, "EXPLAIN"); ok {
			innerExplainSQL = rem
		}
	}

	if !strings.ContainsAny(innerExplainSQL, " \t\r\n();") {
		cleanedTable := cleanIdentifier(innerExplainSQL)
		if cleanedTable != "" {
			if _, err := s.engine.GetTable(cleanedTable); err == nil {
				return s.execDescTable(cleanedTable, start)
			}
		}
	}
	if sqlutil.HasPrefixKeyword(innerExplainSQL, "INSERT") || sqlutil.HasPrefixKeyword(innerExplainSQL, "UPDATE") || sqlutil.HasPrefixKeyword(innerExplainSQL, "DELETE") {
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
	if strings.HasSuffix(rawTable, ")") {
		if openIdx := strings.Index(rawTable, "("); openIdx != -1 {
			tvfName := cleanIdentifier(rawTable[:openIdx])
			argsRaw := rawTable[openIdx+1 : len(rawTable)-1]
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
	p := s.newPlanner()

	var dmlType, tableName, whereCond string
	tokens := sqlutil.SplitTopLevelTokens(dmlSQL)
	if sqlutil.HasPrefixKeyword(dmlSQL, "INSERT") {
		dmlType = "INSERT"
		if len(tokens) >= 3 && strings.EqualFold(tokens[1], "INTO") {
			tableName = cleanIdentifier(tokens[2])
		} else if len(tokens) >= 2 {
			tableName = cleanIdentifier(tokens[1])
		}
	} else if sqlutil.HasPrefixKeyword(dmlSQL, "UPDATE") {
		dmlType = "UPDATE"
		if len(tokens) >= 2 {
			tableName = cleanIdentifier(tokens[1])
		}
		if wIdx := sqlutil.FindTopLevelKeyword(dmlSQL, "WHERE"); wIdx != -1 {
			whereCond = strings.TrimSpace(dmlSQL[wIdx+5:])
		}
	} else if sqlutil.HasPrefixKeyword(dmlSQL, "DELETE") {
		dmlType = "DELETE"
		idx := 1
		if len(tokens) >= 2 && strings.EqualFold(tokens[1], "FROM") {
			idx = 2
		}
		if len(tokens) > idx {
			tableName = cleanIdentifier(tokens[idx])
		}
		if wIdx := sqlutil.FindTopLevelKeyword(dmlSQL, "WHERE"); wIdx != -1 {
			whereCond = strings.TrimSpace(dmlSQL[wIdx+5:])
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

func findSubqueryFromKind(s string, kind subqueryKind, startOffset int) (matchStart int, matchEnd int, prefixStr string, innerSQL string, found bool) {
	if startOffset >= len(s) {
		return 0, 0, "", "", false
	}
	sub := s[startOffset:]

	switch kind {
	case subqueryKindExists:
		indices := sqlutil.FindAllTopLevelKeyword(sub, "EXISTS")
		for _, idx := range indices {
			actualIdx := idx + startOffset
			matchStart = actualIdx
			before := strings.TrimRight(s[:actualIdx], " \t\n")
			if strings.HasSuffix(strings.ToUpper(before), "NOT") {
				notStart := len(before) - 3
				if notStart == 0 || !sqlutil.IsIdentChar(before[notStart-1]) {
					matchStart = notStart
				}
			}
			afterExists := s[actualIdx+6:]
			trimmed := strings.TrimLeft(afterExists, " \t\n")
			if strings.HasPrefix(trimmed, "(") {
				openParenIdx := actualIdx + 6 + (len(afterExists) - len(trimmed))
				closeParenIdx := sqlutil.FindMatchingParen(s, openParenIdx)
				if closeParenIdx != -1 {
					inner := strings.TrimSpace(s[openParenIdx+1 : closeParenIdx])
					if sqlutil.HasPrefixKeyword(inner, "SELECT") || (strings.HasPrefix(inner, "(") && sqlutil.HasPrefixKeyword(inner[1:], "SELECT")) {
						return matchStart, closeParenIdx + 1, s[matchStart:openParenIdx], inner, true
					}
				}
			}
		}

	case subqueryKindIn:
		indices := sqlutil.FindAllTopLevelKeyword(sub, "IN")
		for _, idx := range indices {
			actualIdx := idx + startOffset
			afterIn := s[actualIdx+2:]
			trimmed := strings.TrimLeft(afterIn, " \t\n")
			if strings.HasPrefix(trimmed, "(") {
				openParenIdx := actualIdx + 2 + (len(afterIn) - len(trimmed))
				closeParenIdx := sqlutil.FindMatchingParen(s, openParenIdx)
				if closeParenIdx != -1 {
					inner := strings.TrimSpace(s[openParenIdx+1 : closeParenIdx])
					if sqlutil.HasPrefixKeyword(inner, "SELECT") || (strings.HasPrefix(inner, "(") && sqlutil.HasPrefixKeyword(inner[1:], "SELECT")) {
						before := strings.TrimRight(s[:actualIdx], " \t\n")
						if strings.HasSuffix(strings.ToUpper(before), "NOT") {
							notStart := len(before) - 3
							if notStart == 0 || !sqlutil.IsIdentChar(before[notStart-1]) {
								before = strings.TrimRight(before[:notStart], " \t\n")
							}
						}
						if len(before) == 0 {
							continue
						}
						lhsEnd := len(before)
						lhsStart := lhsEnd
						lastChar := before[lhsEnd-1]
						if lastChar == ')' {
							depth := 1
							for i := lhsEnd - 2; i >= 0; i-- {
								if before[i] == ')' {
									depth++
								} else if before[i] == '(' {
									depth--
									if depth == 0 {
										lhsStart = i
										break
									}
								}
							}
						} else if lastChar == ']' {
							if openBracket := strings.LastIndex(before, "["); openBracket != -1 {
								lhsStart = openBracket
							}
						} else if lastChar == '\'' || lastChar == '"' {
							if openQuote := strings.LastIndex(before[:lhsEnd-1], string(lastChar)); openQuote != -1 {
								lhsStart = openQuote
							}
						} else {
							for lhsStart > 0 {
								prev := before[lhsStart-1]
								if sqlutil.IsIdentChar(prev) || prev == '.' || prev == '@' || prev == '#' {
									lhsStart--
								} else {
									break
								}
							}
						}
						return lhsStart, closeParenIdx + 1, s[lhsStart:openParenIdx], inner, true
					}
				}
			}
		}

	case subqueryKindQuantified:
		for _, kw := range []string{"ALL", "ANY", "SOME"} {
			indices := sqlutil.FindAllTopLevelKeyword(sub, kw)
			for _, idx := range indices {
				actualIdx := idx + startOffset
				afterKw := s[actualIdx+len(kw):]
				trimmed := strings.TrimLeft(afterKw, " \t\n")
				if strings.HasPrefix(trimmed, "(") {
					openParenIdx := actualIdx + len(kw) + (len(afterKw) - len(trimmed))
					closeParenIdx := sqlutil.FindMatchingParen(s, openParenIdx)
					if closeParenIdx != -1 {
						inner := strings.TrimSpace(s[openParenIdx+1 : closeParenIdx])
						if sqlutil.HasPrefixKeyword(inner, "SELECT") || (strings.HasPrefix(inner, "(") && sqlutil.HasPrefixKeyword(inner[1:], "SELECT")) {
							before := strings.TrimRight(s[:actualIdx], " \t\n")
							ops := []string{"!=", "<>", ">=", "<=", "!<", "!>", "=", ">", "<"}
							matchedOp := ""
							for _, op := range ops {
								if strings.HasSuffix(before, op) {
									matchedOp = op
									break
								}
							}
							if matchedOp == "" {
								continue
							}
							beforeOp := strings.TrimRight(before[:len(before)-len(matchedOp)], " \t\n")
							if len(beforeOp) == 0 {
								continue
							}
							lhsEnd := len(beforeOp)
							lhsStart := lhsEnd
							lastChar := beforeOp[lhsEnd-1]
							if lastChar == ')' {
								depth := 1
								for i := lhsEnd - 2; i >= 0; i-- {
									if beforeOp[i] == ')' {
										depth++
									} else if beforeOp[i] == '(' {
										depth--
										if depth == 0 {
											lhsStart = i
											break
										}
									}
								}
							} else if lastChar == ']' {
								if openBracket := strings.LastIndex(beforeOp, "["); openBracket != -1 {
									lhsStart = openBracket
								}
							} else if lastChar == '\'' || lastChar == '"' {
								if openQuote := strings.LastIndex(beforeOp[:lhsEnd-1], string(lastChar)); openQuote != -1 {
									lhsStart = openQuote
								}
							} else {
								for lhsStart > 0 {
									prev := beforeOp[lhsStart-1]
									if sqlutil.IsIdentChar(prev) || prev == '.' || prev == '@' || prev == '#' {
										lhsStart--
									} else {
										break
									}
								}
							}
							return lhsStart, closeParenIdx + 1, s[lhsStart:openParenIdx], inner, true
						}
					}
				}
			}
		}

	case subqueryKindScalar:
		for i := startOffset; i < len(s); i++ {
			if s[i] == '(' {
				closeParenIdx := sqlutil.FindMatchingParen(s, i)
				if closeParenIdx != -1 {
					inner := strings.TrimSpace(s[i+1 : closeParenIdx])
					if sqlutil.HasPrefixKeyword(inner, "SELECT") {
						return i, closeParenIdx + 1, "", inner, true
					}
				}
			}
		}
	}

	return 0, 0, "", "", false
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
			return nil, fmt.Errorf("ANALYZE failed to read record on %q: %w", tbl, err)
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

func (s *Coordinator) resolveSubqueriesWithSession(whereClause string, cs *ClientSession, outerNames ...string) (string, error) {
	if whereClause == "" {
		return "", nil
	}

	execSubquery := func(sql string) (*QueryResult, error) {
		return s.executeNestedSessionSQL(cs, sql)
	}

	result := whereClause

	// 1. [NOT] EXISTS (SELECT ...)
	offset := 0
	for {
		mStart, mEnd, prefix, innerSQL, found := findSubqueryFromKind(result, subqueryKindExists, offset)
		if !found {
			break
		}
		if len(outerNames) > 0 && sqlutil.IsCorrelatedSubquery(innerSQL, outerNames) {
			offset = mEnd
			continue
		}
		if sqlutil.IsVolatileSubquery(innerSQL) {
			offset = mEnd
			continue
		}
		innerRes, err := execSubquery(innerSQL)
		if err != nil {
			// If inner query failed (e.g. contains outer reference), skip so planner can decorrelate
			offset = mEnd
			continue
		}
		isNot := sqlutil.ContainsKeyword(prefix, "NOT", false)
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
		mStart, mEnd, prefix, innerSQL, found := findSubqueryFromKind(result, subqueryKindIn, offset)
		if !found {
			break
		}
		if len(outerNames) > 0 && sqlutil.IsCorrelatedSubquery(innerSQL, outerNames) {
			offset = mEnd
			continue
		}
		if sqlutil.IsVolatileSubquery(innerSQL) {
			offset = mEnd
			continue
		}
		isNot := sqlutil.ContainsKeyword(prefix, "NOT", false)
		inIdx := strings.LastIndex(strings.ToUpper(prefix), "IN")
		lhs := prefix
		if inIdx != -1 {
			lhs = strings.TrimSpace(prefix[:inIdx])
			if isNot && strings.HasSuffix(strings.ToUpper(lhs), "NOT") {
				lhs = strings.TrimSpace(lhs[:len(lhs)-3])
			}
		}
		innerRes, err := execSubquery(innerSQL)
		if err != nil {
			offset = mEnd
			continue
		}
		if len(innerRes.Columns) > 1 {
			return "", fmt.Errorf("subquery must return only one column, returned %d", len(innerRes.Columns))
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
					if rowVal, ok := row.GetFirst(); ok {
						if rowVal == nil {
							vals = append(vals, "NULL")
						} else {
							vals = append(vals, fmt.Sprintf("'%v'", rowVal))
						}
					}
				}
				if len(vals) > 0 {
					replacement = fmt.Sprintf("%s NOT IN (%s)", lhs, strings.Join(vals, ", "))
				}
			}
		} else {
			if hasRows {
				var vals []string
				for _, r := range innerRes.Rows {
					row := NewSQLRow(r, innerRes.Columns)
					if rowVal, ok := row.GetFirst(); ok {
						vals = append(vals, formatSubqueryVal(rowVal))
					}
				}
				if len(vals) > 0 {
					replacement = fmt.Sprintf("%s IN (%s)", lhs, strings.Join(vals, ", "))
				}
			}
		}
		result = result[:mStart] + replacement + result[mEnd:]
		offset = mStart + len(replacement)
	}

	// 3. Quantified Subqueries: col op (ALL|ANY|SOME) (SELECT ...)
	offset = 0
	for {
		mStart, mEnd, prefix, innerSQL, found := findSubqueryFromKind(result, subqueryKindQuantified, offset)
		if !found {
			break
		}
		if len(outerNames) > 0 && sqlutil.IsCorrelatedSubquery(innerSQL, outerNames) {
			offset = mEnd
			continue
		}
		if sqlutil.IsVolatileSubquery(innerSQL) {
			offset = mEnd
			continue
		}
		pTrimmed := strings.TrimSpace(prefix)
		var kw string
		upperP := strings.ToUpper(pTrimmed)
		if strings.HasSuffix(upperP, "ALL") {
			kw = "ALL"
		} else if strings.HasSuffix(upperP, "ANY") {
			kw = "ANY"
		} else if strings.HasSuffix(upperP, "SOME") {
			kw = "SOME"
		}
		if kw == "" {
			offset = mEnd
			continue
		}
		beforeKw := strings.TrimRight(pTrimmed[:len(pTrimmed)-len(kw)], " \t\n")
		ops := []string{"!=", "<>", ">=", "<=", "!<", "!>", "=", ">", "<"}
		var matchedOp string
		for _, op := range ops {
			if strings.HasSuffix(beforeKw, op) {
				matchedOp = op
				break
			}
		}
		if matchedOp == "" {
			offset = mEnd
			continue
		}
		lhs := strings.TrimSpace(beforeKw[:len(beforeKw)-len(matchedOp)])

		innerRes, err := execSubquery(innerSQL)
		if err != nil {
			offset = mEnd
			continue
		}
		if len(innerRes.Columns) > 1 {
			return "", fmt.Errorf("subquery must return only one column, returned %d", len(innerRes.Columns))
		}

		normOp := matchedOp
		if normOp == "!<" {
			normOp = ">="
		} else if normOp == "!>" {
			normOp = "<="
		}

		replacement := "1=0"
		if len(innerRes.Rows) == 0 {
			if kw == "ALL" {
				replacement = "1=1"
			} else {
				replacement = "1=0"
			}
		} else {
			hasNullVal := false
			var vals []any
			for _, r := range innerRes.Rows {
				row := NewSQLRow(r, innerRes.Columns)
				if rowVal, ok := row.GetFirst(); ok {
					if rowVal == nil {
						hasNullVal = true
					}
					vals = append(vals, rowVal)
				}
			}

			if kw == "ALL" && hasNullVal {
				replacement = "1=0"
			} else if (kw == "ANY" || kw == "SOME") && normOp == "=" {
				var strVals []string
				for _, v := range vals {
					strVals = append(strVals, formatSubqueryVal(v))
				}
				replacement = fmt.Sprintf("%s IN (%s)", lhs, strings.Join(strVals, ", "))
			} else if kw == "ALL" && (normOp == "<>" || normOp == "!=") {
				var strVals []string
				for _, v := range vals {
					strVals = append(strVals, formatSubqueryVal(v))
				}
				replacement = fmt.Sprintf("%s NOT IN (%s)", lhs, strings.Join(strVals, ", "))
			} else {
				minVal, maxVal := findMinMaxSubqueryVals(vals)
				if kw == "ALL" {
					switch normOp {
					case ">":
						replacement = fmt.Sprintf("%s > %s", lhs, formatSubqueryVal(maxVal))
					case ">=":
						replacement = fmt.Sprintf("%s >= %s", lhs, formatSubqueryVal(maxVal))
					case "<":
						replacement = fmt.Sprintf("%s < %s", lhs, formatSubqueryVal(minVal))
					case "<=":
						replacement = fmt.Sprintf("%s <= %s", lhs, formatSubqueryVal(minVal))
					case "=":
						if allValsEqual(vals) {
							replacement = fmt.Sprintf("%s = %s", lhs, formatSubqueryVal(vals[0]))
						} else {
							replacement = "1=0"
						}
					default:
						replacement = "1=1"
					}
				} else { // ANY or SOME
					switch normOp {
					case ">":
						replacement = fmt.Sprintf("%s > %s", lhs, formatSubqueryVal(minVal))
					case ">=":
						replacement = fmt.Sprintf("%s >= %s", lhs, formatSubqueryVal(minVal))
					case "<":
						replacement = fmt.Sprintf("%s < %s", lhs, formatSubqueryVal(maxVal))
					case "<=":
						replacement = fmt.Sprintf("%s <= %s", lhs, formatSubqueryVal(maxVal))
					case "!=", "<>":
						nonNullVals := filterNonNullSubqueryVals(vals)
						if len(nonNullVals) == 0 {
							replacement = "1=0"
						} else if len(nonNullVals) == 1 || allValsEqual(nonNullVals) {
							replacement = fmt.Sprintf("(%s IS NOT NULL AND %s <> %s)", lhs, lhs, formatSubqueryVal(nonNullVals[0]))
						} else {
							replacement = fmt.Sprintf("(%s IS NOT NULL)", lhs)
						}
					default:
						replacement = "1=0"
					}
				}
			}
		}
		result = result[:mStart] + replacement + result[mEnd:]
		offset = mStart + len(replacement)
	}

	// 4. Scalar Subquery in comparisons: e.g. > (SELECT ...), = (SELECT ...)
	offset = 0
	for {
		mStart, mEnd, _, innerSQL, found := findSubqueryFromKind(result, subqueryKindScalar, offset)
		if !found {
			break
		}
		if len(outerNames) > 0 && sqlutil.IsCorrelatedSubquery(innerSQL, outerNames) {
			offset = mEnd
			continue
		}
		if sqlutil.IsVolatileSubquery(innerSQL) {
			offset = mEnd
			continue
		}
		innerRes, err := execSubquery(innerSQL)
		if err != nil {
			offset = mEnd
			continue
		}
		if len(innerRes.Columns) > 1 {
			return "", fmt.Errorf("subquery must return only one column, returned %d", len(innerRes.Columns))
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

var cteGlobalSeq uint64

func isCTEWriteStmt(sqlStr string) bool {
	trimmed := strings.TrimSpace(sqlStr)
	if _, _, ok := dmlHead(trimmed); ok {
		return true
	}
	return sqlutil.HasPrefixKeyword(trimmed, "MERGE")
}

func (s *Coordinator) insertMaterializedCTERows(cs *ClientSession, tempName string, targetColsWithID []string, srcCols []string, batchRes *QueryResult, currentTotalRows *int) (int, error) {
	if batchRes == nil || len(batchRes.Rows) == 0 {
		return 0, nil
	}
	colsHeader := strings.Join(targetColsWithID, ", ")
	inserted := 0
	for rIdx, row := range batchRes.Rows {
		*currentTotalRows++
		valStrs := make([]string, 0, len(srcCols)+1)
		valStrs = append(valStrs, fmt.Sprintf("%d", *currentTotalRows))
		for cIdx, srcCol := range srcCols {
			var v any
			if rIdx < len(batchRes.Values) && cIdx < len(batchRes.Values[rIdx]) {
				v = batchRes.Values[rIdx][cIdx]
			} else {
				v = row[srcCol]
			}
			valStrs = append(valStrs, formatCTELiteralValue(v))
		}
		insSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);", tempName, colsHeader, strings.Join(valStrs, ", "))
		if _, err := s.execSingleStatement(cs, insSQL); err != nil {
			return inserted, fmt.Errorf("materialize CTE insert error: %w", err)
		}
		inserted++
	}
	return inserted, nil
}

func buildCTEEphemeralTable(tempName string, targetCols []string, srcCols []string, res *QueryResult, eng storage.Engine, innerSQL string) (*storage.TableSchema, []storage.Row, []storage.Record) {
	schema := &storage.TableSchema{
		Name: tempName,
	}
	schema.Columns = append(schema.Columns, storage.Column{
		Name:       "_cte_row_id",
		Type:       storage.TypeInt,
		PrimaryKey: true,
		Nullable:   false,
	})

	for idx, col := range targetCols {
		srcCol := col
		if idx < len(srcCols) {
			srcCol = srcCols[idx]
		}
		var dataType storage.DataType
		foundType := false

		if res != nil && len(res.Rows) > 0 {
			for _, row := range res.Rows {
				if v := row[srcCol]; v != nil {
					switch v.(type) {
					case int, int32, int64:
						dataType = storage.TypeInt
					case float32, float64:
						dataType = storage.TypeFloat
					case bool:
						dataType = storage.TypeBool
					case time.Time:
						dataType = storage.TypeDateTime
					case storage.Decimal:
						dataType = storage.TypeDecimal
					default:
						dataType = storage.TypeVarChar
					}
					foundType = true
					break
				}
			}
		}
		if !foundType {
			typeStr := inferCTEColumnTypeFromStmt(eng, innerSQL, srcCol)
			switch typeStr {
			case "INT":
				dataType = storage.TypeInt
			case "FLOAT":
				dataType = storage.TypeFloat
			case "BOOLEAN":
				dataType = storage.TypeBool
			case "DATETIME":
				dataType = storage.TypeDateTime
			case "DECIMAL(18, 4)":
				dataType = storage.TypeDecimal
			default:
				dataType = storage.TypeVarChar
			}
		}
		schema.Columns = append(schema.Columns, storage.Column{
			Name:     col,
			Type:     dataType,
			Nullable: true,
		})
	}

	if res == nil || len(res.Rows) == 0 {
		return schema, nil, nil
	}

	rows := make([]storage.Row, 0, len(res.Rows))
	recs := make([]storage.Record, 0, len(res.Rows))

	for rIdx, rMap := range res.Rows {
		rec := make(storage.Record, len(schema.Columns))
		rec["_cte_row_id"] = storage.IntValue(int64(rIdx + 1))
		for cIdx, col := range targetCols {
			srcCol := col
			if cIdx < len(srcCols) {
				srcCol = srcCols[cIdx]
			}
			var val any
			if rIdx < len(res.Values) && cIdx < len(res.Values[rIdx]) {
				val = res.Values[rIdx][cIdx]
			} else {
				val = rMap[srcCol]
			}
			colDef := schema.Columns[cIdx+1]
			rec[col] = storage.ValueFromAny(val, colDef.Type)
		}
		row := schema.RecordToRow(rec)
		rows = append(rows, row)
		recs = append(recs, rec)
	}

	return schema, rows, recs
}

func convertBatchToRows(schema *storage.TableSchema, targetCols []string, srcCols []string, batchRes *QueryResult, baseRowOffset int) ([]storage.Row, []storage.Record) {
	if batchRes == nil || len(batchRes.Rows) == 0 {
		return nil, nil
	}
	rows := make([]storage.Row, 0, len(batchRes.Rows))
	recs := make([]storage.Record, 0, len(batchRes.Rows))
	for rIdx, rMap := range batchRes.Rows {
		rec := make(storage.Record, len(schema.Columns))
		rec["_cte_row_id"] = storage.IntValue(int64(baseRowOffset + rIdx + 1))
		for cIdx, col := range targetCols {
			srcCol := col
			if cIdx < len(srcCols) {
				srcCol = srcCols[cIdx]
			}
			var val any
			if rIdx < len(batchRes.Values) && cIdx < len(batchRes.Values[rIdx]) {
				val = batchRes.Values[rIdx][cIdx]
			} else {
				val = rMap[srcCol]
			}
			colDef := schema.Columns[cIdx+1]
			rec[col] = storage.ValueFromAny(val, colDef.Type)
		}
		row := schema.RecordToRow(rec)
		rows = append(rows, row)
		recs = append(recs, rec)
	}
	return rows, recs
}


func inferCTEColumnTypeFromStmt(eng storage.Engine, stmtSQL string, colName string) string {
	if eng == nil {
		return ""
	}
	qc := proc.ExtractQueryCharacteristics(stmtSQL)
	cleanCol := cleanIdentifier(colName)
	if idx := strings.LastIndex(cleanCol, "."); idx != -1 {
		cleanCol = cleanCol[idx+1:]
	}
	for _, tbl := range qc.TargetTables {
		if schema, err := eng.GetTable(cleanIdentifier(tbl)); err == nil && schema != nil {
			if c, _, found := schema.FindColumn(cleanCol); found && c != nil {
				switch c.Type {
				case storage.TypeInt:
					return "INT"
				case storage.TypeFloat:
					return "FLOAT"
				case storage.TypeBool:
					return "BOOLEAN"
				case storage.TypeDateTime:
					return "DATETIME"
				case storage.TypeDecimal:
					return "DECIMAL(18, 4)"
				default:
					return "VARCHAR(255)"
				}
			}
		}
	}
	return ""
}

func formatCTELiteralValue(v any) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", val)
	case float32, float64:
		return fmt.Sprintf("%f", val)
	case bool:
		if val {
			return "1"
		}
		return "0"
	case time.Time:
		return fmt.Sprintf("'%s'", val.UTC().Format("2006-01-02 15:04:05.999999"))
	case storage.Decimal:
		return val.String()
	default:
		sVal := fmt.Sprintf("%v", v)
		return fmt.Sprintf("'%s'", strings.ReplaceAll(sVal, "'", "''"))
	}
}

func (s *Coordinator) execCTE(query string, start time.Time, cs *ClientSession) (retRes *QueryResult, retErr error) {
	ctes, mainSQL := planner.ParseCTEs(query)
	if len(ctes) == 0 {
		return s.execSelect(query, start, cs)
	}

	if cs == nil {
		cs, _ = s.GetOrCreateSession("default")
	}

	// Fast-Path: Lower linear mutation CTEs directly to VDBE bytecode VM (PERF-CTE-LOWERING-VDBE-1)
	if detectLinearWritableCTE(ctes, mainSQL) {
		res, err := s.tryExecuteLoweredLinearWritableCTE(context.Background(), ctes, mainSQL, cs, start)
		if err != nil {
			return nil, err
		}
		if res != nil {
			return res, nil
		}
	}

	hasWrite := isCTEWriteStmt(mainSQL)
	if !hasWrite {
		for _, cte := range ctes {
			if isCTEWriteStmt(cte.InnerSQL) {
				hasWrite = true
				break
			}
		}
	}

	startedImplicitTx := false
	usedSavepoint := false
	const cteSavepointName = "__lionsql_cte_atomic_sp"

	if hasWrite && cs != nil {
		cs.Mu.Lock()
		hasActiveTx := cs.Tx != nil
		cs.Mu.Unlock()

		if !hasActiveTx {
			if err := s.BeginSessionTx(cs.ID, cs.TxOptions()); err != nil {
				return nil, fmt.Errorf("begin implicit CTE transaction error: %w", err)
			}
			startedImplicitTx = true
		} else {
			cs.Mu.Lock()
			if cs.Tx != nil {
				if err := cs.Tx.Savepoint(cteSavepointName); err == nil {
					usedSavepoint = true
				}
			}
			cs.Mu.Unlock()
		}
	}

	var tempTableNames []string
	defer func() {
		if retErr != nil {
			if startedImplicitTx && cs != nil {
				_ = s.RollbackSessionTx(cs.ID)
				cs.Mu.Lock()
				cs.TxFailed = false
				cs.Mu.Unlock()
			} else if usedSavepoint && cs != nil {
				cs.Mu.Lock()
				if cs.Tx != nil {
					_ = cs.Tx.RollbackTo(cteSavepointName)
				}
				cs.Mu.Unlock()
			}
		}
		for _, t := range tempTableNames {
			if cs != nil {
				cs.UnregisterEphemeralTable(t)
			}
		}
	}()

	for i := range ctes {
		cte := ctes[i]
		isRecursive := sqlutil.ContainsKeyword(cte.InnerSQL, cte.Name, false)

		var res *QueryResult
		var recSQL string

		if isRecursive {
			// Find top-level UNION ALL or UNION
			unionIdx := sqlutil.FindTopLevelKeyword(cte.InnerSQL, "UNION ALL")
			unionLen := len("UNION ALL")
			if unionIdx == -1 {
				unionIdx = sqlutil.FindTopLevelKeyword(cte.InnerSQL, "UNION")
				unionLen = len("UNION")
			}
			if unionIdx != -1 {
				baseSQL := strings.TrimSpace(cte.InnerSQL[:unionIdx])
				recSQL = strings.TrimSpace(cte.InnerSQL[unionIdx+unionLen:])
				var err error
				res, err = s.execSingleStatement(cs, baseSQL)
				if err != nil {
					return nil, fmt.Errorf("CTE %q base query error: %w", cte.Name, err)
				}
			} else {
				var err error
				res, err = s.execSingleStatement(cs, cte.InnerSQL)
				if err != nil {
					return nil, fmt.Errorf("CTE %q execution error: %w", cte.Name, err)
				}
			}
		} else {
			var err error
			res, err = s.execSingleStatement(cs, cte.InnerSQL)
			if err != nil {
				return nil, fmt.Errorf("CTE %q execution error: %w", cte.Name, err)
			}
		}

		// 2. Materialize into session-local in-memory ephemeral table (Zero-DDL)
		tempName := fmt.Sprintf("#__cte_%s_%d", cte.Name, atomic.AddUint64(&cteGlobalSeq, 1))
		tempTableNames = append(tempTableNames, tempName)

		if len(res.Columns) == 0 {
			continue
		}

		var targetCols []string
		if len(cte.Columns) == len(res.Columns) {
			targetCols = cte.Columns
		} else {
			targetCols = res.Columns
		}

		schema, rows, recs := buildCTEEphemeralTable(tempName, targetCols, res.Columns, res, s.engine, cte.InnerSQL)
		if cs != nil {
			cs.RegisterEphemeralTable(tempName, schema, rows, recs)
		}

		// 3. If recursive, iteratively execute recursive step and append new rows
		// Standard SQL semantics: The recursive query references only the delta (working table) of the previous step.
		if isRecursive && recSQL != "" && len(res.Rows) > 0 {
			workTempName := fmt.Sprintf("%s_work", tempName)
			tempTableNames = append(tempTableNames, workTempName)

			// Initialize working table with initial base query rows
			if cs != nil {
				wSchema, wRows, wRecs := buildCTEEphemeralTable(workTempName, targetCols, res.Columns, res, s.engine, cte.InnerSQL)
				cs.RegisterEphemeralTable(workTempName, wSchema, wRows, wRecs)
			}

			maxRecursion := parseMaxRecursion(query)
			recSQLWithWork := replaceIdentOutsideQuotes(recSQL, cte.Name, workTempName)
			for iter := 0; ; iter++ {
				if maxRecursion > 0 && iter >= maxRecursion {
					return nil, fmt.Errorf("The statement terminated. The maximum recursion %d has been exhausted before statement completion.", maxRecursion)
				}
				recRes, recErr := s.execSingleStatement(cs, recSQLWithWork)
				if recErr != nil || recRes == nil || len(recRes.Rows) == 0 {
					break
				}
				newRows, newRecs := convertBatchToRows(schema, targetCols, recRes.Columns, recRes, len(res.Rows))
				if len(newRows) == 0 {
					break
				}
				if cs != nil {
					cs.AppendEphemeralRows(tempName, newRows, newRecs)
					// Update working table to hold ONLY the newly produced delta rows for the next recursive step
					wSchema, wRows, wRecs := buildCTEEphemeralTable(workTempName, targetCols, recRes.Columns, recRes, s.engine, "")
					cs.RegisterEphemeralTable(workTempName, wSchema, wRows, wRecs)
				}
				res.Rows = append(res.Rows, recRes.Rows...)
			}
		}

		// Rewrite occurrences of cte.Name in subsequent CTEs and in mainSQL with tempName
		for j := i + 1; j < len(ctes); j++ {
			ctes[j].InnerSQL = replaceIdentOutsideQuotes(ctes[j].InnerSQL, cte.Name, tempName)
		}
		mainSQL = replaceIdentOutsideQuotes(mainSQL, cte.Name, tempName)
	}

	mainRes, mainErr := s.execSingleStatement(cs, mainSQL)
	if mainErr != nil {
		return nil, mainErr
	}

	if startedImplicitTx && cs != nil {
		if err := s.CommitSessionTx(cs.ID); err != nil {
			return nil, fmt.Errorf("commit implicit CTE transaction error: %w", err)
		}
		startedImplicitTx = false
	} else if usedSavepoint && cs != nil {
		cs.Mu.Lock()
		if cs.Tx != nil {
			_ = cs.Tx.ReleaseSavepoint(cteSavepointName)
		}
		cs.Mu.Unlock()
		usedSavepoint = false
	}

	return mainRes, nil
}

func replaceIdentOutsideQuotes(sqlStr, oldIdent, newIdent string) string {
	n := len(sqlStr)
	oldLen := len(oldIdent)
	if n == 0 || oldLen == 0 {
		return sqlStr
	}
	var sb strings.Builder
	sb.Grow(n)
	inSingle := false
	inDouble := false
	for i := 0; i < n; {
		ch := sqlStr[i]
		if inSingle {
			sb.WriteByte(ch)
			if ch == '\'' {
				if i+1 < n && sqlStr[i+1] == '\'' {
					sb.WriteByte(sqlStr[i+1])
					i += 2
					continue
				}
				inSingle = false
			}
			i++
			continue
		}
		if inDouble {
			sb.WriteByte(ch)
			if ch == '"' {
				inDouble = false
			}
			i++
			continue
		}
		if ch == '\'' {
			inSingle = true
			sb.WriteByte(ch)
			i++
			continue
		}
		if ch == '"' {
			inDouble = true
			sb.WriteByte(ch)
			i++
			continue
		}
		if i+oldLen <= n && (i == 0 || !isIdentChar(sqlStr[i-1])) &&
			(i+oldLen == n || !isIdentChar(sqlStr[i+oldLen])) &&
			strings.EqualFold(sqlStr[i:i+oldLen], oldIdent) {
			sb.WriteString(newIdent)
			i += oldLen
			continue
		}
		sb.WriteByte(ch)
		i++
	}
	return sb.String()
}

// parseMaxRecursion extracts the recursion limit from OPTION (MAXRECURSION <n>) if present,
// defaulting to 100 as per SQL Server T-SQL semantics ([RULE:SafeDefaults]).
func parseMaxRecursion(sql string) int {
	idx := sqlutil.FindTopLevelKeyword(sql, "MAXRECURSION")
	if idx == -1 {
		idx = strings.Index(strings.ToUpper(sql), "MAXRECURSION")
	}
	if idx == -1 {
		return 100
	}
	rest := strings.TrimSpace(sql[idx+len("MAXRECURSION"):])
	if strings.HasPrefix(rest, "(") {
		rest = strings.TrimSpace(rest[1:])
	}
	var numStr string
	for _, r := range rest {
		if r >= '0' && r <= '9' {
			numStr += string(r)
		} else if numStr != "" {
			break
		}
	}
	if n, err := strconv.Atoi(numStr); err == nil {
		return n
	}
	return 100
}

func findMinMaxSubqueryVals(vals []any) (any, any) {
	if len(vals) == 0 {
		return nil, nil
	}
	minVal, maxVal := vals[0], vals[0]
	for _, v := range vals[1:] {
		if v == nil {
			continue
		}
		if minVal == nil || sqlutil.CompareAny(v, minVal) < 0 {
			minVal = v
		}
		if maxVal == nil || sqlutil.CompareAny(v, maxVal) > 0 {
			maxVal = v
		}
	}
	return minVal, maxVal
}

func allValsEqual(vals []any) bool {
	if len(vals) <= 1 {
		return true
	}
	first := vals[0]
	for _, v := range vals[1:] {
		if sqlutil.CompareAny(first, v) != 0 {
			return false
		}
	}
	return true
}

func filterNonNullSubqueryVals(vals []any) []any {
	var res []any
	for _, v := range vals {
		if v != nil {
			res = append(res, v)
		}
	}
	return res
}
