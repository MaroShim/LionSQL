package api

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/catalog"
	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

func (s *Server) execCreateTable(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
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
		if strings.HasPrefix(cUpper, "PRIMARY KEY") || (strings.HasPrefix(cUpper, "CONSTRAINT") && strings.Contains(cUpper, "PRIMARY KEY")) {
			// e.g. PRIMARY KEY (id) or CONSTRAINT pk_foo PRIMARY KEY (id)
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

		if strings.HasPrefix(cUpper, "FOREIGN KEY") || (strings.HasPrefix(cUpper, "CONSTRAINT") && strings.Contains(cUpper, "FOREIGN KEY")) {
			if fk, ok := parseForeignKeyClause(cDefTrimmed, "", ""); ok {
				fk.ChildTable = tableName
				schema.ForeignKeys = append(schema.ForeignKeys, *fk)
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
		precision := 0
		scale := 0
		if dataType == storage.TypeDecimal {
			precision = 18
			scale = 2
			if rawType == "MONEY" {
				precision = 19
				scale = 4
			} else {
				decRe := regexp.MustCompile(`(?i)\b(?:DECIMAL|NUMERIC)\s*\(\s*(\d+)(?:\s*,\s*(\d+))?\s*\)`)
				if m := decRe.FindStringSubmatch(cDefTrimmed); len(m) > 1 {
					if p, err := strconv.Atoi(m[1]); err == nil {
						precision = p
					}
					if len(m) > 2 && m[2] != "" {
						if sc, err := strconv.Atoi(m[2]); err == nil {
							scale = sc
						}
					} else {
						scale = 0
					}
				}
			}
		}

		isPK := strings.Contains(cUpper, "PRIMARY KEY")
		isNotNull := strings.Contains(cUpper, "NOT NULL") || isPK
		isIdentity := strings.Contains(cUpper, "IDENTITY") || strings.Contains(cUpper, "AUTO_INCREMENT")

		schema.Columns = append(schema.Columns, storage.Column{
			Name:       colName,
			Type:       dataType,
			PrimaryKey: isPK,
			Nullable:   !isNotNull,
			Identity:   isIdentity,
			Precision:  precision,
			Scale:      scale,
		})

		if strings.Contains(cUpper, "REFERENCES") {
			if fk, ok := parseForeignKeyClause(cDefTrimmed, colName, ""); ok {
				fk.ChildTable = tableName
				schema.ForeignKeys = append(schema.ForeignKeys, *fk)
			}
		}
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

	if strings.HasPrefix(tableName, "##") {
		s.globalTempMu.Lock()
		if s.globalTempCreators == nil {
			s.globalTempCreators = make(map[string]string)
		}
		creatorID := "default"
		if cs != nil && cs.ID != "" {
			creatorID = cs.ID
		}
		s.globalTempCreators[tableName] = creatorID
		s.globalTempMu.Unlock()
	} else if strings.HasPrefix(tableName, "#") {
		if cs != nil {
			cs.mu.Lock()
			cs.TempTables = append(cs.TempTables, tableName)
			cs.mu.Unlock()
		}
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

	if strings.HasPrefix(tableName, "##") {
		s.globalTempMu.Lock()
		delete(s.globalTempCreators, tableName)
		s.globalTempMu.Unlock()
	}

	return &QueryResult{
		Success:    true,
		Command:    "DROP TABLE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Table %q dropped successfully", tableName)},
	}, nil
}

func (s *Server) execTruncateTable(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	re := regexp.MustCompile(`(?is)^TRUNCATE\s+(?:TABLE\s+)?([^\s;]+)`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 2 {
		return nil, fmt.Errorf("invalid TRUNCATE TABLE syntax")
	}

	tableName := cleanIdentifier(matches[1])
	if err := s.engine.TruncateTable(tableName); err != nil {
		return nil, err
	}

	return &QueryResult{
		Success:    true,
		Command:    "TRUNCATE TABLE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Table %q truncated successfully", tableName)},
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

func (s *Server) execCreateProcedure(query string, start time.Time) (*QueryResult, error) {
	plan, err := proc.Parse(query)
	if err != nil {
		return nil, fmt.Errorf("parse procedure error: %w", err)
	}

	prog, err := proc.CompileProcedure(plan)
	if err != nil {
		return nil, fmt.Errorf("bytecode compilation error: %w", err)
	}

	params := make([]catalog.ParamDef, len(plan.Params))
	for i, p := range plan.Params {
		params[i] = catalog.ParamDef{Name: p.Name, Type: p.Type}
	}

	procMeta := &catalog.Procedure{
		Name:      plan.Name,
		Body:      query,
		JSCode:    prog.Disassemble(),
		Params:    params,
		CreatedAt: time.Now(),
	}

	var regErr error
	if plan.IsAlter {
		regErr = s.catalog.AlterProcedure(procMeta)
	} else if plan.OrAlter {
		regErr = s.catalog.RegisterProcedure(procMeta)
	} else {
		regErr = s.catalog.CreateProcedure(procMeta)
	}

	if regErr != nil {
		if errors.Is(regErr, catalog.ErrProcExists) {
			return nil, fmt.Errorf("there is already an object named %q in the database (use CREATE OR ALTER PROCEDURE to replace)", plan.Name)
		}
		if errors.Is(regErr, catalog.ErrProcNotFound) {
			return nil, fmt.Errorf("cannot alter procedure %q because it does not exist", plan.Name)
		}
		return nil, fmt.Errorf("catalog error: %w", regErr)
	}

	s.executor.Cache().PutByName(plan.Name, prog)

	cmd := "CREATE PROCEDURE"
	msg := fmt.Sprintf("Procedure %q registered and bytecode compiled successfully", plan.Name)
	if plan.IsAlter {
		cmd = "ALTER PROCEDURE"
		msg = fmt.Sprintf("Procedure %q altered successfully", plan.Name)
	}

	return &QueryResult{
		Success:    true,
		Command:    cmd,
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{msg},
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

	prog, err := proc.CompileFunction(fnStmt)
	if err != nil {
		return nil, fmt.Errorf("compile function error: %w", err)
	}

	params := make([]catalog.ParamDef, len(fnStmt.Params))
	for i, p := range fnStmt.Params {
		params[i] = catalog.ParamDef{Name: p.Name, Type: p.Type}
	}

	fnMeta := &catalog.Function{
		Name:       fnStmt.Name,
		ReturnType: fnStmt.ReturnType,
		Body:       query,
		JSCode:     prog.Disassemble(),
		Params:     params,
		CreatedAt:  time.Now(),
	}

	if err := s.catalog.RegisterFunction(fnMeta); err != nil {
		return nil, fmt.Errorf("catalog register function error: %w", err)
	}

	s.executor.RegisterUDF(fnStmt.Name, prog)

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

	prog, err := proc.CompileTrigger(trgStmt)
	if err != nil {
		return nil, fmt.Errorf("compile trigger error: %w", err)
	}

	trgMeta := &catalog.Trigger{
		Name:      trgStmt.Name,
		Table:     trgStmt.Table,
		Timing:    trgStmt.Timing,
		Events:    trgStmt.Events,
		Body:      query,
		JSCode:    prog.Disassemble(),
		CreatedAt: time.Now(),
	}

	if err := s.catalog.RegisterTrigger(trgMeta); err != nil {
		return nil, fmt.Errorf("catalog register trigger error: %w", err)
	}

	for _, ev := range trgStmt.Events {
		s.executor.RegisterTrigger(trgStmt.Name, trgStmt.Table, ev, prog)
	}

	return &QueryResult{
		Success:    true,
		Command:    "CREATE TRIGGER",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Trigger %q registered on %q for %s", trgStmt.Name, trgStmt.Table, strings.Join(trgStmt.Events, ", "))},
	}, nil
}

func parseSQLDataType(s string) storage.DataType {
	switch strings.ToUpper(s) {
	case "INT", "INTEGER", "BIGINT", "SMALLINT", "TINYINT", "IDENTITY":
		return storage.TypeInt
	case "FLOAT", "DOUBLE", "REAL":
		return storage.TypeFloat
	case "NUMERIC", "DECIMAL", "MONEY":
		return storage.TypeDecimal
	case "BOOL", "BOOLEAN", "BIT":
		return storage.TypeBool
	case "DATETIME", "TIMESTAMP", "DATE", "TIME":
		return storage.TypeDateTime
	case "JSON", "JSONB":
		return storage.TypeJSON
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

func parseForeignKeyClause(def string, defaultChildCol string, defaultName string) (*storage.ForeignKeySchema, bool) {
	defTrimmed := strings.TrimSpace(def)
	upper := strings.ToUpper(defTrimmed)
	if !strings.Contains(upper, "REFERENCES") {
		return nil, false
	}

	fk := storage.ForeignKeySchema{
		Name:     defaultName,
		OnDelete: storage.ActionNoAction,
		OnUpdate: storage.ActionNoAction,
	}

	reOnDelete := regexp.MustCompile(`(?i)\bON\s+DELETE\s+(CASCADE|SET\s+NULL|SET\s+DEFAULT|RESTRICT|NO\s+ACTION)`)
	if m := reOnDelete.FindStringSubmatch(defTrimmed); len(m) > 1 {
		fk.OnDelete = storage.ParseForeignKeyAction(m[1])
	}

	reOnUpdate := regexp.MustCompile(`(?i)\bON\s+UPDATE\s+(CASCADE|SET\s+NULL|SET\s+DEFAULT|RESTRICT|NO\s+ACTION)`)
	if m := reOnUpdate.FindStringSubmatch(defTrimmed); len(m) > 1 {
		fk.OnUpdate = storage.ParseForeignKeyAction(m[1])
	}

	reTableFK := regexp.MustCompile(`(?i)(?:CONSTRAINT\s+([^\s(]+)\s+)?FOREIGN\s+KEY\s*\(([^)]+)\)\s*REFERENCES\s+([^\s(]+)(?:\s*\(([^)]+)\))?`)
	if m := reTableFK.FindStringSubmatch(defTrimmed); len(m) > 0 {
		if m[1] != "" {
			fk.Name = cleanIdentifier(m[1])
		}
		rawChildCols := strings.Split(m[2], ",")
		for _, c := range rawChildCols {
			if col := cleanIdentifier(c); col != "" {
				fk.ChildColumns = append(fk.ChildColumns, col)
			}
		}
		fk.ParentTable = cleanIdentifier(m[3])
		if len(m) > 4 && strings.TrimSpace(m[4]) != "" {
			rawParentCols := strings.Split(m[4], ",")
			for _, c := range rawParentCols {
				if col := cleanIdentifier(c); col != "" {
					fk.ParentColumns = append(fk.ParentColumns, col)
				}
			}
		}
		if fk.Name == "" && len(fk.ChildColumns) > 0 {
			fk.Name = fmt.Sprintf("fk_%s_%s", fk.ParentTable, fk.ChildColumns[0])
		}
		return &fk, true
	}

	reInlineFK := regexp.MustCompile(`(?i)\bREFERENCES\s+([^\s(]+)(?:\s*\(([^)]+)\))?`)
	if m := reInlineFK.FindStringSubmatch(defTrimmed); len(m) > 0 {
		fk.ParentTable = cleanIdentifier(m[1])
		if len(m) > 2 && strings.TrimSpace(m[2]) != "" {
			rawParentCols := strings.Split(m[2], ",")
			for _, c := range rawParentCols {
				if col := cleanIdentifier(c); col != "" {
					fk.ParentColumns = append(fk.ParentColumns, col)
				}
			}
		}
		if defaultChildCol != "" {
			fk.ChildColumns = []string{defaultChildCol}
		}
		if fk.Name == "" && defaultChildCol != "" {
			fk.Name = fmt.Sprintf("fk_%s_%s", fk.ParentTable, defaultChildCol)
		}
		return &fk, true
	}

	return nil, false
}

// execCreateView handles CREATE [OR REPLACE] VIEW name AS <select>.
func (s *Server) execCreateView(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// Pattern: CREATE [OR REPLACE] VIEW [IF NOT EXISTS] <name> AS <select>
	re := regexp.MustCompile(`(?is)^CREATE\s+(?:OR\s+REPLACE\s+)?VIEW\s+(?:IF\s+NOT\s+EXISTS\s+)?([^\s(]+)\s+AS\s+(.+)$`)
	m := re.FindStringSubmatch(cleaned)
	if len(m) < 3 {
		return nil, fmt.Errorf("invalid CREATE VIEW syntax")
	}

	viewName := cleanIdentifier(m[1])
	selectQuery := strings.TrimSpace(m[2])

	orReplace := regexp.MustCompile(`(?i)^CREATE\s+OR\s+REPLACE\s+VIEW`).MatchString(cleaned)
	ifNotExists := regexp.MustCompile(`(?i)VIEW\s+IF\s+NOT\s+EXISTS`).MatchString(cleaned)

	view := &catalog.View{
		Name:  viewName,
		Query: selectQuery,
	}

	if orReplace {
		s.catalog.CreateOrReplaceView(view)
	} else {
		if err := s.catalog.CreateView(view); err != nil {
			if ifNotExists {
				// Silently succeed when IF NOT EXISTS is specified
				return &QueryResult{
					Success:      true,
					Command:      "CREATE VIEW",
					RowsAffected: 0,
					DurationUS:   time.Since(start).Microseconds(),
				}, nil
			}
			return nil, err
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "CREATE VIEW",
		RowsAffected: 0,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execDropView handles DROP VIEW [IF EXISTS] <name>.
func (s *Server) execDropView(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	re := regexp.MustCompile(`(?is)^DROP\s+VIEW\s+(?:IF\s+EXISTS\s+)?(.+)$`)
	m := re.FindStringSubmatch(cleaned)
	if len(m) < 2 {
		return nil, fmt.Errorf("invalid DROP VIEW syntax")
	}

	ifExists := regexp.MustCompile(`(?i)^DROP\s+VIEW\s+IF\s+EXISTS`).MatchString(cleaned)
	viewName := cleanIdentifier(strings.TrimSpace(m[1]))

	if err := s.catalog.DropView(viewName); err != nil {
		if ifExists {
			return &QueryResult{
				Success:      true,
				Command:      "DROP VIEW",
				RowsAffected: 0,
				DurationUS:   time.Since(start).Microseconds(),
			}, nil
		}
		return nil, err
	}

	return &QueryResult{
		Success:      true,
		Command:      "DROP VIEW",
		RowsAffected: 0,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}
