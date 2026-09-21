package api

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/catalog"
	"sqlion/pkg/planner"
	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

// QueryResult represents the outcome of a SQL statement execution.
type QueryResult struct {
	Success      bool             `json:"success"`
	Command      string           `json:"command"`
	Columns      []string         `json:"columns,omitempty"`
	Rows         []map[string]any `json:"rows,omitempty"`
	RowsAffected int64            `json:"rows_affected"`
	DurationUS   int64            `json:"duration_us"`
	Messages     []string         `json:"messages,omitempty"`
	Error        string           `json:"error,omitempty"`
}

// ExecuteSQL parses and runs one or more SQL statements against the engine and catalog.
func (s *Server) ExecuteSQL(query string) (*QueryResult, error) {
	start := time.Now()
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return &QueryResult{Success: true, Command: "EMPTY", DurationUS: time.Since(start).Microseconds()}, nil
	}

	upper := strings.ToUpper(trimmed)

	// 0. Query Execution Plan: EXPLAIN [ANALYZE]
	if strings.HasPrefix(upper, "EXPLAIN") {
		return s.execExplain(trimmed, start)
	}

	// 1. Procedural DDL: CREATE PROCEDURE / CREATE FUNCTION / CREATE TRIGGER
	if strings.HasPrefix(upper, "CREATE PROCEDURE") || strings.HasPrefix(upper, "CREATE PROC") {
		return s.execCreateProcedure(trimmed, start)
	}
	if strings.HasPrefix(upper, "CREATE FUNCTION") {
		return s.execCreateFunction(trimmed, start)
	}
	if strings.HasPrefix(upper, "CREATE TRIGGER") {
		return s.execCreateTrigger(trimmed, start)
	}

	// 2. Procedure Invocation: EXEC / CALL
	if strings.HasPrefix(upper, "EXEC ") || strings.HasPrefix(upper, "CALL ") {
		return s.execProcedureCall(trimmed, start)
	}

	// 3. DDL: CREATE TABLE
	if strings.HasPrefix(upper, "CREATE TABLE") {
		return s.execCreateTable(trimmed, start)
	}

	// 3.1 DDL: CREATE INDEX
	if strings.HasPrefix(upper, "CREATE INDEX") || strings.HasPrefix(upper, "CREATE UNIQUE INDEX") {
		return s.execCreateIndex(trimmed, start)
	}

	// 4. DDL: DROP TABLE
	if strings.HasPrefix(upper, "DROP TABLE") {
		return s.execDropTable(trimmed, start)
	}

	// 4.1 DDL: DROP INDEX
	if strings.HasPrefix(upper, "DROP INDEX") {
		return s.execDropIndex(trimmed, start)
	}

	// 5. DML: INSERT INTO
	if strings.HasPrefix(upper, "INSERT INTO") || strings.HasPrefix(upper, "INSERT ") {
		return s.execInsert(trimmed, start)
	}

	// 6. DQL: WITH (Common Table Expressions) / SELECT
	if strings.HasPrefix(upper, "WITH ") {
		return s.execCTE(trimmed, start)
	}
	if strings.HasPrefix(upper, "SELECT") {
		return s.execSelect(trimmed, start)
	}

	// 7. DML: UPDATE
	if strings.HasPrefix(upper, "UPDATE ") {
		return s.execUpdate(trimmed, start)
	}

	// 8. DML: DELETE
	if strings.HasPrefix(upper, "DELETE FROM") || strings.HasPrefix(upper, "DELETE ") {
		return s.execDelete(trimmed, start)
	}

	return &QueryResult{
		Success:    false,
		Error:      fmt.Sprintf("unrecognized or unsupported SQL statement: %q", trimmed),
		DurationUS: time.Since(start).Microseconds(),
	}, fmt.Errorf("unsupported SQL statement")
}

func (s *Server) execCreateTable(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	re := regexp.MustCompile(`(?is)^CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([^\s(]+)\s*\((.*)\)$`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid CREATE TABLE syntax")
	}

	tableName := cleanIdentifier(matches[1])
	colsDef := matches[2]

	colDefs := splitColumnDefs(colsDef)
	if len(colDefs) == 0 {
		return nil, fmt.Errorf("no columns defined for table %s", tableName)
	}

	schema := &storage.TableSchema{
		Name:    tableName,
		Columns: make([]storage.Column, 0, len(colDefs)),
	}

	for _, cDef := range colDefs {
		cDefTrimmed := strings.TrimSpace(cDef)
		if cDefTrimmed == "" {
			continue
		}

		cUpper := strings.ToUpper(cDefTrimmed)
		if strings.HasPrefix(cUpper, "PRIMARY KEY") || strings.HasPrefix(cUpper, "CONSTRAINT") {
			// e.g. PRIMARY KEY (id)
			if idx := strings.Index(cDefTrimmed, "("); idx != -1 {
				pkCol := cleanIdentifier(strings.Trim(cDefTrimmed[idx:], "() "))
				for i := range schema.Columns {
					if strings.EqualFold(schema.Columns[i].Name, pkCol) {
						schema.Columns[i].PrimaryKey = true
					}
				}
			}
			continue
		}

		tokens := strings.Fields(cDefTrimmed)
		if len(tokens) < 2 {
			continue
		}

		colName := cleanIdentifier(tokens[0])
		rawType := strings.ToUpper(tokens[1])
		if idx := strings.Index(rawType, "("); idx != -1 {
			rawType = rawType[:idx]
		}

		dataType := parseSQLDataType(rawType)
		isPK := strings.Contains(cUpper, "PRIMARY KEY")
		isNotNull := strings.Contains(cUpper, "NOT NULL") || isPK

		schema.Columns = append(schema.Columns, storage.Column{
			Name:       colName,
			Type:       dataType,
			PrimaryKey: isPK,
			Nullable:   !isNotNull,
		})
	}

	// Default first column as PK if none marked
	hasPK := false
	for _, c := range schema.Columns {
		if c.PrimaryKey {
			hasPK = true
			break
		}
	}
	if !hasPK && len(schema.Columns) > 0 {
		schema.Columns[0].PrimaryKey = true
	}

	if err := s.engine.CreateTable(schema); err != nil {
		if strings.Contains(strings.ToUpper(query), "IF NOT EXISTS") && errors.Is(err, storage.ErrTableExists) {
			return &QueryResult{
				Success:    true,
				Command:    "CREATE TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Table %q already exists (skipped)", tableName)},
			}, nil
		}
		return nil, err
	}

	return &QueryResult{
		Success:    true,
		Command:    "CREATE TABLE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Table %q created successfully with %d columns", tableName, len(schema.Columns))},
	}, nil
}

func (s *Server) execDropTable(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	re := regexp.MustCompile(`(?is)^DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([^\s;]+)`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 2 {
		return nil, fmt.Errorf("invalid DROP TABLE syntax")
	}

	tableName := cleanIdentifier(matches[1])
	if err := s.engine.DropTable(tableName); err != nil {
		if strings.Contains(strings.ToUpper(query), "IF EXISTS") && errors.Is(err, storage.ErrTableNotFound) {
			return &QueryResult{
				Success:    true,
				Command:    "DROP TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Table %q does not exist (skipped)", tableName)},
			}, nil
		}
		return nil, err
	}

	return &QueryResult{
		Success:    true,
		Command:    "DROP TABLE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Table %q dropped successfully", tableName)},
	}, nil
}

func (s *Server) execCreateIndex(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	re := regexp.MustCompile(`(?is)^CREATE\s+(UNIQUE\s+)?INDEX\s+([^\s]+)\s+ON\s+([^\s(]+)\s*\((.*?)\)$`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 5 {
		return nil, fmt.Errorf("invalid CREATE INDEX syntax: expected CREATE [UNIQUE] INDEX <name> ON <table> (<cols>)")
	}

	isUnique := strings.TrimSpace(matches[1]) != ""
	indexName := cleanIdentifier(matches[2])
	tableName := cleanIdentifier(matches[3])
	colsRaw := matches[4]

	var cols []string
	for _, c := range strings.Split(colsRaw, ",") {
		cTrimmed := cleanIdentifier(strings.TrimSpace(c))
		if cTrimmed != "" {
			cols = append(cols, cTrimmed)
		}
	}

	if len(cols) == 0 {
		return nil, fmt.Errorf("no columns specified for index %q", indexName)
	}

	idxSchema := &storage.IndexSchema{
		Name:      indexName,
		TableName: tableName,
		Columns:   cols,
		Unique:    isUnique,
	}

	if err := s.engine.CreateIndex(idxSchema); err != nil {
		return nil, err
	}

	msg := fmt.Sprintf("Index %q created successfully on table %q.", indexName, tableName)
	if isUnique {
		msg = fmt.Sprintf("Unique index %q created successfully on table %q.", indexName, tableName)
	}

	return &QueryResult{
		Success:    true,
		Command:    "CREATE INDEX",
		Messages:   []string{msg},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execDropIndex(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	re := regexp.MustCompile(`(?is)^DROP\s+INDEX\s+([^\s]+)(?:\s+ON\s+([^\s]+))?$`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 2 {
		return nil, fmt.Errorf("invalid DROP INDEX syntax: expected DROP INDEX <name> [ON <table>]")
	}

	indexName := cleanIdentifier(matches[1])
	tableName := ""
	if len(matches) > 2 {
		tableName = cleanIdentifier(matches[2])
	}

	if tableName == "" {
		tables, err := s.engine.ListTables()
		if err != nil {
			return nil, err
		}
		for _, t := range tables {
			if _, _, found := t.FindIndex(indexName); found {
				tableName = t.Name
				break
			}
		}
		if tableName == "" {
			return nil, fmt.Errorf("index %q not found on any table", indexName)
		}
	}

	if err := s.engine.DropIndex(tableName, indexName); err != nil {
		return nil, err
	}

	return &QueryResult{
		Success:    true,
		Command:    "DROP INDEX",
		Messages:   []string{fmt.Sprintf("Index %q dropped successfully from table %q.", indexName, tableName)},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execInsert(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	re := regexp.MustCompile(`(?is)^INSERT\s+INTO\s+([^\s(]+)(?:\s*\(([^)]+)\))?\s*VALUES\s*(.*)$`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 4 {
		return nil, fmt.Errorf("invalid INSERT INTO syntax")
	}

	tableName := cleanIdentifier(matches[1])
	rawCols := matches[2]
	rawValues := matches[3]

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	var targetCols []string
	if rawCols != "" {
		for _, c := range strings.Split(rawCols, ",") {
			targetCols = append(targetCols, cleanIdentifier(c))
		}
	} else {
		for _, c := range schema.Columns {
			targetCols = append(targetCols, c.Name)
		}
	}

	tupleStrings := parseValueTuples(rawValues)
	if len(tupleStrings) == 0 {
		return nil, fmt.Errorf("no values supplied in INSERT statement")
	}

	session, err := s.engine.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		return nil, err
	}

	insertedCount := int64(0)
	for _, tupleStr := range tupleStrings {
		vals := splitValues(tupleStr)
		if len(vals) != len(targetCols) {
			_ = tx.Rollback()
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

		if err := tx.Insert(tableName, rec); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("insert row error: %w", err)
		}
		insertedCount++
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("transaction commit error: %w", err)
	}

	return &QueryResult{
		Success:      true,
		Command:      "INSERT",
		RowsAffected: insertedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows inserted into %q", insertedCount, tableName)},
	}, nil
}

func (s *Server) execExplain(query string, start time.Time) (*QueryResult, error) {
	p := planner.NewPlanner()
	qInfo, err := p.ParseSelect(query)
	if err != nil {
		return nil, fmt.Errorf("EXPLAIN parse error: %w", err)
	}

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

	var totalRows int64 = 0
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

	var planTree *planner.PlanNode
	if qInfo.Join != nil {
		rTable := cleanIdentifier(qInfo.Join.Table)
		rSchema, _ := s.engine.GetTable(rTable)
		var rRows int64 = 0
		if session != nil {
			if rTx, err := session.BeginTx(storage.TxOptions{ReadOnly: true}); err == nil {
				defer rTx.Rollback()
				if rCur, err := rTx.Scan(rTable); err == nil {
					defer rCur.Close()
					for rCur.Next() {
						rRows++
					}
				}
			}
		}
		planTree = p.BuildJoinPlan(qInfo, schema, totalRows, rSchema, rRows)
	} else {
		planTree = p.BuildPlan(qInfo, schema, totalRows)
	}

	if qInfo.IsAnalyze {
		// Execute query to collect actual metrics
		execStart := time.Now()
		selectSQL := query
		upper := strings.ToUpper(strings.TrimSpace(query))
		if strings.HasPrefix(upper, "EXPLAIN ANALYZE ") {
			selectSQL = strings.TrimSpace(query[16:])
		} else if strings.HasPrefix(upper, "EXPLAIN ") {
			selectSQL = strings.TrimSpace(query[8:])
		}
		res, execErr := s.execSelect(selectSQL, execStart)
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

func findSubquery(s string, prefixPattern *regexp.Regexp) (matchStart int, matchEnd int, prefixStr string, innerSQL string, found bool) {
	loc := prefixPattern.FindStringSubmatchIndex(s)
	if loc == nil {
		return 0, 0, "", "", false
	}
	openParenIdx := -1
	for i := loc[0]; i < len(s); i++ {
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
	return loc[0], closeParenIdx + 1, s[loc[0]:openParenIdx], inner, true
}

func (s *Server) resolveSubqueries(whereClause string) (string, error) {
	if whereClause == "" {
		return "", nil
	}

	result := whereClause

	// 1. [NOT] EXISTS (SELECT ...)
	existsRe := regexp.MustCompile(`(?is)(?:NOT\s+)?EXISTS\s*\(\s*SELECT\b`)
	for {
		mStart, mEnd, prefix, innerSQL, found := findSubquery(result, existsRe)
		if !found {
			break
		}
		isNot := strings.Contains(strings.ToUpper(prefix), "NOT")
		innerRes, err := s.ExecuteSQL(innerSQL)
		hasRows := err == nil && innerRes != nil && len(innerRes.Rows) > 0
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
	}

	// 2. [NOT] IN (SELECT ...) -> [NOT] IN (val1, val2, ...)
	inSelectRe := regexp.MustCompile(`(?is)\b([a-zA-Z0-9_\.]+)\s+(?:NOT\s+)?IN\s*\(\s*SELECT\b`)
	for {
		mStart, mEnd, prefix, innerSQL, found := findSubquery(result, inSelectRe)
		if !found {
			break
		}
		tokens := strings.Fields(prefix)
		colName := cleanIdentifier(tokens[0])
		isNot := strings.Contains(strings.ToUpper(prefix), "NOT")
		innerRes, err := s.ExecuteSQL(innerSQL)
		hasRows := err == nil && innerRes != nil && len(innerRes.Rows) > 0
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
					replacement = fmt.Sprintf("%s IN (%s)", colName, strings.Join(vals, ", "))
				}
			}
		}
		result = result[:mStart] + replacement + result[mEnd:]
	}

	// 3. Scalar Subquery in comparisons: e.g. > (SELECT ...), = (SELECT ...)
	scalarRe := regexp.MustCompile(`(?is)\(\s*SELECT\b`)
	for {
		mStart, mEnd, _, innerSQL, found := findSubquery(result, scalarRe)
		if !found {
			break
		}
		innerRes, err := s.ExecuteSQL(innerSQL)
		replacement := "NULL"
		if err == nil && innerRes != nil && len(innerRes.Rows) > 0 {
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
				replacement = fmt.Sprintf("'%v'", foundVal)
			}
		}
		result = result[:mStart] + replacement + result[mEnd:]
	}

	return result, nil
}

func (s *Server) execCTE(query string, start time.Time) (*QueryResult, error) {
	p := planner.NewPlanner()
	qInfo, err := p.ParseSelect(query)
	if err != nil {
		return nil, fmt.Errorf("parse CTE error: %w", err)
	}

	if len(qInfo.CTEs) == 0 {
		return s.execSelect(query, start)
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

func (s *Server) execSelect(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	p := planner.NewPlanner()
	qInfo, parseErr := p.ParseSelect(cleaned)
	if parseErr == nil && qInfo != nil && qInfo.Join != nil {
		joinRes, err := s.execJoin(qInfo, start)
		if err != nil {
			return nil, err
		}
		if qInfo.HasAggregates {
			return s.execAggregate(joinRes.Rows, qInfo, start)
		}
		return joinRes, nil
	}

	// Parse projection, table, WHERE, ORDER BY, LIMIT
	selectRe := regexp.MustCompile(`(?is)^SELECT\s+(.*?)\s+FROM\s+([^\s,;]+)(?:\s+WHERE\s+(.*?))?(?:\s+GROUP\s+BY\s+(.*?))?(?:\s+HAVING\s+(.*?))?(?:\s+ORDER\s+BY\s+(.*?))?(?:\s+LIMIT\s+(\d+))?$`)
	matches := selectRe.FindStringSubmatch(cleaned)
	if len(matches) < 3 {
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
			return s.execSelect(innerSQL, start)
		}
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	session, err := s.engine.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var cursor storage.Cursor
	usedIndex := false

	if whereClause != "" {
		eqRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_]+)\s*=\s*(.+)$`)
		if m := eqRe.FindStringSubmatch(whereClause); len(m) == 3 {
			colName := cleanIdentifier(m[1])
			if idx, found := schema.FindIndexByColumn(colName); found {
				valStr := strings.Trim(strings.TrimSpace(m[2]), "'\"")
				if colMeta, _, colFound := schema.FindColumn(colName); colFound && colMeta != nil {
					parsedVal := parseValueToType(valStr, colMeta.Type)
					if cur, err := tx.IndexScan(tableName, idx.Name, &parsedVal, &parsedVal); err == nil {
						cursor = cur
						usedIndex = true
					}
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
	var selectedCols []string
	isSelectAll := projStr == "*" || projStr == ""
	if isSelectAll {
		for _, col := range schema.Columns {
			selectedCols = append(selectedCols, col.Name)
		}
	} else {
		for _, c := range strings.Split(projStr, ",") {
			cTrimmed := cleanIdentifier(strings.TrimSpace(c))
			if cTrimmed != "" {
				selectedCols = append(selectedCols, cTrimmed)
			}
		}
	}

	// 2. Scan and filter rows
	rows := make([]map[string]any, 0)
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			break
		}

		if whereClause != "" && !evaluateWhereCondition(rec, whereClause) {
			continue
		}

		row := make(map[string]any)
		for _, col := range selectedCols {
			if v, ok := rec[col]; ok {
				row[col] = formatStorageValue(v)
			} else {
				row[col] = nil
			}
		}
		rows = append(rows, row)
	}

	// 3. Apply ORDER BY if present
	if orderByClause != "" && len(rows) > 0 {
		orderParts := strings.Fields(orderByClause)
		orderCol := cleanIdentifier(orderParts[0])
		desc := len(orderParts) > 1 && strings.EqualFold(orderParts[1], "DESC")
		sort.Slice(rows, func(i, j int) bool {
			v1 := fmt.Sprintf("%v", rows[i][orderCol])
			v2 := fmt.Sprintf("%v", rows[j][orderCol])
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
	if limitCount >= 0 && len(rows) > limitCount {
		rows = rows[:limitCount]
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
				hasVal := false
				for _, r := range bucket.rows {
					v := r[item.ArgCol]
					if v == nil {
						if dotIdx := strings.Index(item.ArgCol, "."); dotIdx != -1 {
							v = r[item.ArgCol[dotIdx+1:]]
						}
					}
					if v != nil {
						if n, err := strconv.ParseFloat(fmt.Sprintf("%v", v), 64); err == nil {
							sum += n
							hasVal = true
						}
					}
				}
				if hasVal {
					if sum == math.Trunc(sum) {
						val = int64(sum)
					} else {
						val = sum
					}
				} else {
					val = nil
				}
			case "AVG":
				var sum float64 = 0
				var count int64 = 0
				for _, r := range bucket.rows {
					v := r[item.ArgCol]
					if v == nil {
						if dotIdx := strings.Index(item.ArgCol, "."); dotIdx != -1 {
							v = r[item.ArgCol[dotIdx+1:]]
						}
					}
					if v != nil {
						if n, err := strconv.ParseFloat(fmt.Sprintf("%v", v), 64); err == nil {
							sum += n
							count++
						}
					}
				}
				if count > 0 {
					val = sum / float64(count)
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

		// Construct final projected row matching colHeaders
		finalRow := make(map[string]any)
		for _, h := range colHeaders {
			finalRow[h] = aggRow[h]
		}
		aggRows = append(aggRows, finalRow)
	}

	// 5. Apply ORDER BY
	if qInfo.OrderByClause != "" && len(aggRows) > 0 {
		orderParts := strings.Fields(qInfo.OrderByClause)
		orderCol := cleanIdentifier(orderParts[0])
		desc := len(orderParts) > 1 && strings.EqualFold(orderParts[1], "DESC")
		sort.Slice(aggRows, func(i, j int) bool {
			v1 := fmt.Sprintf("%v", aggRows[i][orderCol])
			v2 := fmt.Sprintf("%v", aggRows[j][orderCol])
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

	// 6. Apply LIMIT
	if qInfo.LimitCount >= 0 && int64(len(aggRows)) > qInfo.LimitCount {
		aggRows = aggRows[:qInfo.LimitCount]
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      colHeaders,
		Rows:         aggRows,
		RowsAffected: int64(len(aggRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execJoin(qInfo *planner.QueryInfo, start time.Time) (*QueryResult, error) {
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
			return s.execNestedLoopJoin(qInfo, start, idxMeta)
		}
	}
	return s.execHashJoin(qInfo, start)
}

func (s *Server) execNestedLoopJoin(qInfo *planner.QueryInfo, start time.Time, rightIdx *storage.IndexSchema) (*QueryResult, error) {
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

	session, err := s.engine.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

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
		sort.Slice(joinedRows, func(i, j int) bool {
			v1 := fmt.Sprintf("%v", joinedRows[i][orderCol])
			v2 := fmt.Sprintf("%v", joinedRows[j][orderCol])
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

func (s *Server) execHashJoin(qInfo *planner.QueryInfo, start time.Time) (*QueryResult, error) {
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

	session, err := s.engine.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

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
		sort.Slice(joinedRows, func(i, j int) bool {
			v1 := fmt.Sprintf("%v", joinedRows[i][orderCol])
			v2 := fmt.Sprintf("%v", joinedRows[j][orderCol])
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

func (s *Server) execUpdate(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	re := regexp.MustCompile(`(?is)^UPDATE\s+([^\s]+)\s+SET\s+(.*?)(?:\s+WHERE\s+(.*))?$`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid UPDATE syntax")
	}

	tableName := cleanIdentifier(matches[1])
	setClause := matches[2]
	whereClause := ""
	if len(matches) > 3 {
		whereClause = strings.TrimSpace(matches[3])
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	session, err := s.engine.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		return nil, err
	}

	cursor, err := tx.Scan(tableName)
	if err != nil {
		_ = tx.Rollback()
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
		pk  storage.Value
		rec storage.Record
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
			recUpdates[colName] = parseLiteralValue(exprStr, targetType)
		}

		if hasPK {
			toUpdate = append(toUpdate, updateEntry{pk: pkVal, rec: recUpdates})
		}
	}

	updatedCount := int64(0)
	for _, item := range toUpdate {
		if err := tx.Update(tableName, item.pk, item.rec); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("update row error: %w", err)
		}
		updatedCount++
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit error: %w", err)
	}

	return &QueryResult{
		Success:      true,
		Command:      "UPDATE",
		RowsAffected: updatedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows updated in %q", updatedCount, tableName)},
	}, nil
}

func (s *Server) execDelete(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	re := regexp.MustCompile(`(?is)^DELETE\s+FROM\s+([^\s;]+)(?:\s+WHERE\s+(.*))?$`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 2 {
		return nil, fmt.Errorf("invalid DELETE syntax")
	}

	tableName := cleanIdentifier(matches[1])
	whereClause := ""
	if len(matches) > 2 {
		whereClause = strings.TrimSpace(matches[2])
	}

	session, err := s.engine.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		return nil, err
	}

	cursor, err := tx.Scan(tableName)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	defer cursor.Close()

	toDelete := make([]storage.Value, 0)
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
			toDelete = append(toDelete, pkVal)
		}
	}

	deletedCount := int64(0)
	for _, pk := range toDelete {
		if err := tx.Delete(tableName, pk); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("delete error: %w", err)
		}
		deletedCount++
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit error: %w", err)
	}

	return &QueryResult{
		Success:      true,
		Command:      "DELETE",
		RowsAffected: deletedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows deleted from %q", deletedCount, tableName)},
	}, nil
}

func (s *Server) execCreateProcedure(query string, start time.Time) (*QueryResult, error) {
	plan, err := proc.Parse(query)
	if err != nil {
		return nil, fmt.Errorf("parse procedure error: %w", err)
	}

	jsCode, err := proc.Transpile(plan)
	if err != nil {
		return nil, fmt.Errorf("transpile error: %w", err)
	}

	params := make([]catalog.ParamDef, len(plan.Params))
	for i, p := range plan.Params {
		params[i] = catalog.ParamDef{Name: p.Name, Type: p.Type}
	}

	procMeta := &catalog.Procedure{
		Name:      plan.Name,
		Body:      query,
		JSCode:    jsCode,
		Params:    params,
		CreatedAt: time.Now(),
	}

	if err := s.catalog.RegisterProcedure(procMeta); err != nil {
		return nil, fmt.Errorf("catalog error: %w", err)
	}

	prog, err := s.executor.Cache().GetOrCompile(jsCode)
	if err == nil {
		s.executor.Cache().PutByName(plan.Name, prog)
	}

	return &QueryResult{
		Success:    true,
		Command:    "CREATE PROCEDURE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Procedure %q registered and bytecode compiled successfully", plan.Name)},
	}, nil
}

func (s *Server) execCreateFunction(query string, start time.Time) (*QueryResult, error) {
	// 1. Check if Table-Valued Function (TVF): CREATE FUNCTION ... RETURNS TABLE AS RETURN ( ... )
	tvfRe := regexp.MustCompile(`(?is)^CREATE\s+FUNCTION\s+([^\s(]+)\s*\((.*?)\)\s*RETURNS\s+TABLE\s+AS\s+RETURN\s*\(\s*(SELECT\s+.*)\s*\)\s*;?$`)
	if m := tvfRe.FindStringSubmatch(strings.TrimSpace(query)); len(m) == 4 {
		fnName := cleanIdentifier(m[1])
		rawParams := m[2]
		innerQuery := strings.TrimSpace(m[3])

		var params []catalog.ParamDef
		if strings.TrimSpace(rawParams) != "" {
			for _, p := range strings.Split(rawParams, ",") {
				pParts := strings.Fields(strings.TrimSpace(p))
				if len(pParts) >= 2 {
					pName := pParts[0]
					pType := parseSQLDataType(pParts[1])
					params = append(params, catalog.ParamDef{Name: pName, Type: pType})
				}
			}
		}

		fnMeta := &catalog.Function{
			Name:          fnName,
			Params:        params,
			IsTableValued: true,
			Body:          innerQuery,
			CreatedAt:     time.Now(),
		}

		if err := s.catalog.RegisterFunction(fnMeta); err != nil {
			return nil, fmt.Errorf("catalog register TVF error: %w", err)
		}

		return &QueryResult{
			Success:    true,
			Command:    "CREATE FUNCTION",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Table-valued function %q registered successfully", fnName)},
		}, nil
	}

	fnStmt, err := proc.ParseFunction(query)
	if err != nil {
		return nil, fmt.Errorf("parse function error: %w", err)
	}

	jsCode, err := proc.TranspileFunction(fnStmt)
	if err != nil {
		return nil, fmt.Errorf("transpile function error: %w", err)
	}

	params := make([]catalog.ParamDef, len(fnStmt.Params))
	for i, p := range fnStmt.Params {
		params[i] = catalog.ParamDef{Name: p.Name, Type: p.Type}
	}

	fnMeta := &catalog.Function{
		Name:       fnStmt.Name,
		ReturnType: fnStmt.ReturnType,
		Body:       query,
		JSCode:     jsCode,
		Params:     params,
		CreatedAt:  time.Now(),
	}

	if err := s.catalog.RegisterFunction(fnMeta); err != nil {
		return nil, fmt.Errorf("catalog register function error: %w", err)
	}

	prog, err := s.executor.Cache().GetOrCompile(jsCode)
	if err == nil {
		s.executor.RegisterUDF(fnStmt.Name, prog)
	}

	return &QueryResult{
		Success:    true,
		Command:    "CREATE FUNCTION",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("User-defined function %q registered successfully", fnStmt.Name)},
	}, nil
}

func (s *Server) execCreateTrigger(query string, start time.Time) (*QueryResult, error) {
	trgStmt, err := proc.ParseTrigger(query)
	if err != nil {
		return nil, fmt.Errorf("parse trigger error: %w", err)
	}

	jsCode, err := proc.TranspileTrigger(trgStmt)
	if err != nil {
		return nil, fmt.Errorf("transpile trigger error: %w", err)
	}

	trgMeta := &catalog.Trigger{
		Name:      trgStmt.Name,
		Table:     trgStmt.Table,
		Timing:    trgStmt.Timing,
		Events:    trgStmt.Events,
		Body:      query,
		JSCode:    jsCode,
		CreatedAt: time.Now(),
	}

	if err := s.catalog.RegisterTrigger(trgMeta); err != nil {
		return nil, fmt.Errorf("catalog register trigger error: %w", err)
	}

	prog, err := s.executor.Cache().GetOrCompile(jsCode)
	if err == nil {
		for _, ev := range trgStmt.Events {
			s.executor.RegisterTrigger(trgStmt.Name, trgStmt.Table, ev, prog)
		}
	}

	return &QueryResult{
		Success:    true,
		Command:    "CREATE TRIGGER",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Trigger %q registered on %q for %s", trgStmt.Name, trgStmt.Table, strings.Join(trgStmt.Events, ", "))},
	}, nil
}

func (s *Server) execProcedureCall(query string, start time.Time) (*QueryResult, error) {
	fields := strings.Fields(query)
	if len(fields) < 2 {
		return nil, fmt.Errorf("invalid EXEC syntax")
	}

	procName := cleanIdentifier(fields[1])
	procMeta, err := s.catalog.GetProcedure(procName)
	if err != nil {
		return nil, fmt.Errorf("procedure %q not found: %w", procName, err)
	}

	prog, found := s.executor.Cache().GetByName(procName)
	if !found {
		prog, err = s.executor.Cache().GetOrCompile(procMeta.JSCode)
		if err != nil {
			return nil, fmt.Errorf("bytecode compilation failed: %w", err)
		}
		s.executor.Cache().PutByName(procName, prog)
	}

	session, err := s.engine.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	args := make(map[string]any)
	argTokens := fields[2:]
	for i, param := range procMeta.Params {
		if i < len(argTokens) {
			raw := strings.Trim(argTokens[i], ",; '\"")
			if num, err := strconv.ParseInt(raw, 10, 64); err == nil {
				args[param.Name] = num
			} else if f, err := strconv.ParseFloat(raw, 64); err == nil {
				args[param.Name] = f
			} else {
				args[param.Name] = raw
			}
		}
	}

	res, err := s.executor.Run(prog, procName, args, session)
	if err != nil {
		return nil, fmt.Errorf("procedure execution error: %w", err)
	}

	messages := make([]string, 0)
	if len(res.Logs) > 0 {
		messages = append(messages, res.Logs...)
	}
	messages = append(messages, fmt.Sprintf("Return code: %v", res.ReturnValue))

	return &QueryResult{
		Success:      true,
		Command:      "EXEC",
		RowsAffected: 1,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     messages,
	}, nil
}

// Helpers

func cleanIdentifier(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "[]\"'`")
	if strings.HasPrefix(strings.ToLower(s), "dbo.") {
		s = s[4:]
	}
	return strings.Trim(s, "[]\"'`")
}

func parseSQLDataType(s string) storage.DataType {
	switch strings.ToUpper(s) {
	case "INT", "INTEGER", "BIGINT", "SMALLINT", "TINYINT":
		return storage.TypeInt
	case "FLOAT", "DOUBLE", "REAL", "NUMERIC", "DECIMAL", "MONEY":
		return storage.TypeFloat
	case "BOOL", "BOOLEAN", "BIT":
		return storage.TypeBool
	case "DATETIME", "TIMESTAMP", "DATE", "TIME":
		return storage.TypeDateTime
	default:
		return storage.TypeVarChar
	}
}

func splitColumnDefs(s string) []string {
	var defs []string
	var cur strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '(':
			depth++
			cur.WriteByte(c)
		case ')':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
		case ',':
			if depth == 0 {
				defs = append(defs, strings.TrimSpace(cur.String()))
				cur.Reset()
			} else {
				cur.WriteByte(c)
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		defs = append(defs, strings.TrimSpace(cur.String()))
	}
	return defs
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
		return storage.StringValue(strVal)
	}

	switch targetType {
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
	case storage.TypeVarChar:
		return v.SVal
	case storage.TypeFloat:
		return v.FVal
	case storage.TypeBool:
		return v.BVal
	case storage.TypeDateTime:
		return v.TVal
	default:
		return v.String()
	}
}

func evaluateWhereCondition(rec storage.Record, whereStr string) bool {
	// Simple predicate evaluator: col = val, col > val, col < val, col != val, col LIKE 'pattern', col [NOT] IN (...)
	whereStr = strings.TrimSpace(whereStr)
	if whereStr == "" || whereStr == "1=1" || whereStr == "1 = 1" || strings.EqualFold(whereStr, "NOT 1=0") || strings.EqualFold(whereStr, "NOT 1 = 0") {
		return true
	}
	if whereStr == "1=0" || whereStr == "1 = 0" || strings.EqualFold(whereStr, "NOT 1=1") || strings.EqualFold(whereStr, "NOT 1 = 1") {
		return false
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
		if !ok || val.IsNull {
			return false
		}
		strVal := fmt.Sprintf("%v", formatStorageValue(val))
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

			val, ok := rec[leftCol]
			if !ok {
				return false
			}

			strVal := fmt.Sprintf("%v", formatStorageValue(val))
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
	case storage.TypeBool:
		if b, err := strconv.ParseBool(s); err == nil {
			return storage.BoolValue(b)
		}
	case storage.TypeDateTime:
		if tVal, err := time.Parse(time.RFC3339, s); err == nil {
			return storage.DateTimeValue(tVal)
		}
	}
	return storage.StringValue(s)
}

func evaluateWhereConditionMap(rec map[string]any, whereStr string) bool {
	whereStr = strings.TrimSpace(whereStr)
	if whereStr == "" || whereStr == "1=1" || whereStr == "1 = 1" || strings.EqualFold(whereStr, "NOT 1=0") || strings.EqualFold(whereStr, "NOT 1 = 0") {
		return true
	}
	if whereStr == "1=0" || whereStr == "1 = 0" || strings.EqualFold(whereStr, "NOT 1=1") || strings.EqualFold(whereStr, "NOT 1 = 1") {
		return false
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

