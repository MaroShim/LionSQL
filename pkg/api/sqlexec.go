package api

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/catalog"
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

	// 4. DDL: DROP TABLE
	if strings.HasPrefix(upper, "DROP TABLE") {
		return s.execDropTable(trimmed, start)
	}

	// 5. DML: INSERT INTO
	if strings.HasPrefix(upper, "INSERT INTO") || strings.HasPrefix(upper, "INSERT ") {
		return s.execInsert(trimmed, start)
	}

	// 6. DQL: SELECT
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

func (s *Server) execSelect(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// Parse projection, table, WHERE, ORDER BY, LIMIT
	selectRe := regexp.MustCompile(`(?is)^SELECT\s+(.*?)\s+FROM\s+([^\s,;]+)(?:\s+WHERE\s+(.*?))?(?:\s+ORDER\s+BY\s+(.*?))?(?:\s+LIMIT\s+(\d+))?$`)
	matches := selectRe.FindStringSubmatch(cleaned)
	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid or unsupported SELECT syntax")
	}

	projStr := strings.TrimSpace(matches[1])
	tableName := cleanIdentifier(matches[2])
	whereClause := ""
	if len(matches) > 3 {
		whereClause = strings.TrimSpace(matches[3])
	}
	orderByClause := ""
	if len(matches) > 4 {
		orderByClause = strings.TrimSpace(matches[4])
	}
	limitCount := -1
	if len(matches) > 5 && matches[5] != "" {
		if n, err := strconv.Atoi(matches[5]); err == nil {
			limitCount = n
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

	cursor, err := tx.Scan(tableName)
	if err != nil {
		return nil, fmt.Errorf("scan error on %s: %w", tableName, err)
	}
	defer cursor.Close()

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
	// Simple predicate evaluator: col = val, col > val, col < val, col != val, col LIKE 'pattern'
	whereStr = strings.TrimSpace(whereStr)
	if whereStr == "" {
		return true
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
