package engine

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/auth"
	"lionsql/pkg/catalog"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

func parseColumnDefault(cDef string) string {
	if sqlutil.ContainsKeyword(cDef, "AS IDENTITY", false) {
		return ""
	}
	defIdx := sqlutil.FindTopLevelKeyword(cDef, "DEFAULT")
	if defIdx == -1 {
		return ""
	}
	rest := strings.TrimSpace(cDef[defIdx+len("DEFAULT"):])
	if rest == "" {
		return ""
	}

	if rest[0] == '\'' {
		for i := 1; i < len(rest); i++ {
			if rest[i] == '\'' {
				if i+1 < len(rest) && rest[i+1] == '\'' {
					i++
					continue
				}
				return cleanDefaultExpr(rest[:i+1])
			}
		}
		return cleanDefaultExpr(rest)
	}

	if rest[0] == '(' {
		closeIdx := sqlutil.FindMatchingParen(rest, 0)
		if closeIdx != -1 {
			return cleanDefaultExpr(rest[:closeIdx+1])
		}
	}

	tokens := sqlutil.SplitTopLevelTokens(rest)
	if len(tokens) > 0 {
		tok := tokens[0]
		upper := strings.ToUpper(tok)
		if upper != "NOT" && upper != "NULL" && upper != "PRIMARY" && upper != "UNIQUE" && upper != "CHECK" && upper != "REFERENCES" && upper != "CONSTRAINT" {
			return cleanDefaultExpr(tok)
		}
	}
	return ""
}

func parseDecimalPrecisionScale(cDef string) (int, int) {
	upper := strings.ToUpper(cDef)
	idx := strings.Index(upper, "DECIMAL")
	if idx == -1 {
		idx = strings.Index(upper, "NUMERIC")
	}
	if idx == -1 {
		return 18, 2
	}
	rest := cDef[idx:]
	openIdx := strings.Index(rest, "(")
	if openIdx == -1 {
		return 18, 2
	}
	closeIdx := strings.Index(rest, ")")
	if closeIdx == -1 || closeIdx <= openIdx {
		return 18, 2
	}
	args := strings.Split(rest[openIdx+1:closeIdx], ",")
	p := 18
	s := 2
	if len(args) > 0 {
		if val, err := strconv.Atoi(strings.TrimSpace(args[0])); err == nil {
			p = val
		}
	}
	if len(args) > 1 {
		if val, err := strconv.Atoi(strings.TrimSpace(args[1])); err == nil {
			s = val
		}
	} else {
		s = 0
	}
	return p, s
}

func parseCreateTablePrefix(cleaned string) (tableName string, ifNotExists bool, openParenIdx int, closeParenIdx int, remainder string, err error) {
	if !sqlutil.HasPrefixKeyword(cleaned, "CREATE TABLE") && !sqlutil.HasPrefixKeyword(cleaned, "CREATE TEMPORARY TABLE") && !sqlutil.HasPrefixKeyword(cleaned, "CREATE TEMP TABLE") {
		return "", false, -1, -1, "", fmt.Errorf("invalid CREATE TABLE syntax")
	}
	var rest string
	if sqlutil.HasPrefixKeyword(cleaned, "CREATE TEMPORARY TABLE") {
		rest = strings.TrimSpace(cleaned[len("CREATE TEMPORARY TABLE"):])
	} else if sqlutil.HasPrefixKeyword(cleaned, "CREATE TEMP TABLE") {
		rest = strings.TrimSpace(cleaned[len("CREATE TEMP TABLE"):])
	} else {
		rest = strings.TrimSpace(cleaned[len("CREATE TABLE"):])
	}

	if sqlutil.HasPrefixKeyword(rest, "IF NOT EXISTS") {
		ifNotExists = true
		rest = strings.TrimSpace(rest[len("IF NOT EXISTS"):])
	}

	openParenIdx = sqlutil.FindTopLevelChar(rest, '(')
	if openParenIdx == -1 {
		return "", false, -1, -1, "", fmt.Errorf("invalid CREATE TABLE syntax: missing opening parenthesis")
	}

	tableName = cleanIdentifier(strings.TrimSpace(rest[:openParenIdx]))
	if tableName == "" {
		return "", false, -1, -1, "", fmt.Errorf("invalid CREATE TABLE syntax: missing table name")
	}

	closeParenIdx = sqlutil.FindMatchingParen(rest, openParenIdx)
	if closeParenIdx == -1 {
		return "", false, -1, -1, "", fmt.Errorf("invalid CREATE TABLE syntax: unbalanced parentheses")
	}

	remainder = strings.TrimSpace(rest[closeParenIdx+1:])
	baseOffset := len(cleaned) - len(rest)
	return tableName, ifNotExists, baseOffset + openParenIdx, baseOffset + closeParenIdx, remainder, nil
}

func parseAlterTablePrefix(cleaned string) (tableName string, ifExists bool, actionBody string, err error) {
	if !sqlutil.HasPrefixKeyword(cleaned, "ALTER TABLE") {
		return "", false, "", fmt.Errorf("invalid ALTER TABLE syntax")
	}
	rest := strings.TrimSpace(cleaned[len("ALTER TABLE"):])
	if sqlutil.HasPrefixKeyword(rest, "IF EXISTS") {
		ifExists = true
		rest = strings.TrimSpace(rest[len("IF EXISTS"):])
	}
	tokens := sqlutil.SplitTopLevelTokens(rest)
	if len(tokens) == 0 {
		return "", false, "", fmt.Errorf("invalid ALTER TABLE syntax: missing table name")
	}
	tableName = cleanIdentifier(tokens[0])
	actionBody = strings.TrimSpace(rest[len(tokens[0]):])
	return tableName, ifExists, actionBody, nil
}

func (s *Coordinator) execCreateTable(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	tableName, _, openParenIdx, closeParenIdx, remainder, err := parseCreateTablePrefix(cleaned)
	if err != nil {
		return nil, err
	}

	colsDef := cleaned[openParenIdx+1 : closeParenIdx]
	rawPartition := ""
	if sqlutil.HasPrefixKeyword(remainder, "PARTITION BY") {
		rawPartition = strings.TrimSpace(remainder[len("PARTITION BY"):])
	}

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
		tokens := strings.Fields(cDefTrimmed)
		if len(tokens) == 0 {
			continue
		}
		firstTokUpper := strings.ToUpper(tokens[0])
		isConstraintClause := firstTokUpper == "CONSTRAINT"

		if strings.HasPrefix(cUpper, "PRIMARY KEY") || (isConstraintClause && strings.Contains(cUpper, "PRIMARY KEY")) {
			// e.g. PRIMARY KEY (id) or PRIMARY KEY (col1, col2) or CONSTRAINT pk_foo PRIMARY KEY (col1, col2)
			if openIdx := strings.Index(cDefTrimmed, "("); openIdx != -1 {
				if closeIdx := strings.LastIndex(cDefTrimmed, ")"); closeIdx > openIdx {
					innerCols := cDefTrimmed[openIdx+1 : closeIdx]
					for _, pkCol := range sqlutil.SplitTopLevel(innerCols, ',') {
						cleaned := cleanIdentifier(pkCol)
						for i := range schema.Columns {
							if strings.EqualFold(schema.Columns[i].Name, cleaned) {
								schema.Columns[i].PrimaryKey = true
							}
						}
					}
				}
			}
			continue
		}

		if strings.HasPrefix(cUpper, "FOREIGN KEY") || (isConstraintClause && strings.Contains(cUpper, "FOREIGN KEY")) {
			if fk, ok := parseForeignKeyClause(cDefTrimmed, "", ""); ok {
				fk.ChildTable = tableName
				schema.ForeignKeys = append(schema.ForeignKeys, *fk)
			}
			continue
		}

		isTableUnique := firstTokUpper == "UNIQUE" || strings.HasPrefix(firstTokUpper, "UNIQUE(") || (isConstraintClause && strings.Contains(cUpper, "UNIQUE"))
		if isTableUnique {
			var cName string
			if isConstraintClause && len(tokens) >= 2 {
				cName = cleanIdentifier(tokens[1])
			}
			if openIdx := strings.Index(cDefTrimmed, "("); openIdx != -1 {
				if closeIdx := strings.LastIndex(cDefTrimmed, ")"); closeIdx > openIdx {
					innerCols := cDefTrimmed[openIdx+1 : closeIdx]
					var uniqueCols []string
					for _, col := range sqlutil.SplitTopLevel(innerCols, ',') {
						cleaned := cleanIdentifier(col)
						if cleaned != "" {
							uniqueCols = append(uniqueCols, cleaned)
						}
					}
					if len(uniqueCols) > 0 {
						if cName == "" {
							cName = fmt.Sprintf("uq_%s_%s", tableName, strings.Join(uniqueCols, "_"))
						}
						schema.Indexes = append(schema.Indexes, storage.IndexSchema{
							Name:      cName,
							TableName: tableName,
							Columns:   uniqueCols,
							Unique:    true,
						})
					}
				}
			}
			continue
		}

		isTableCheck := firstTokUpper == "CHECK" || strings.HasPrefix(firstTokUpper, "CHECK(") || (isConstraintClause && strings.Contains(cUpper, "CHECK"))
		if isTableCheck {
			continue
		}

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
				precision, scale = parseDecimalPrecisionScale(cDefTrimmed)
			}
		}

		isGeneratedIdentity := sqlutil.ContainsKeyword(cDefTrimmed, "AS IDENTITY", false) ||
			(sqlutil.ContainsKeyword(cDefTrimmed, "GENERATED", false) && sqlutil.ContainsKeyword(cDefTrimmed, "IDENTITY", false))
		isSerial := rawType == "SERIAL" || rawType == "BIGSERIAL" || rawType == "SMALLSERIAL"
		isPK := strings.Contains(cUpper, "PRIMARY KEY")
		isNotNull := strings.Contains(cUpper, "NOT NULL") || isPK || isSerial || isGeneratedIdentity
		isIdentity := strings.Contains(cUpper, "IDENTITY") || strings.Contains(cUpper, "AUTO_INCREMENT") || isSerial || isGeneratedIdentity

		defaultExpr := parseColumnDefault(cDefTrimmed)

		schema.Columns = append(schema.Columns, storage.Column{
			Name:       colName,
			Type:       dataType,
			PrimaryKey: isPK,
			Nullable:   !isNotNull,
			Identity:   isIdentity,
			Precision:  precision,
			Scale:      scale,
			Default:    defaultExpr,
		})

		if strings.Contains(cUpper, "REFERENCES") {
			if fk, ok := parseForeignKeyClause(cDefTrimmed, colName, ""); ok {
				fk.ChildTable = tableName
				schema.ForeignKeys = append(schema.ForeignKeys, *fk)
			}
		}
	}

	// Ensure table has a primary key: if none marked, inject synthetic hidden _row_id
	if !schema.HasPrimaryKey() {
		schema.EnsurePrimaryKey("_row_id")
	}

	if rawPartition != "" {
		fn := "COLUMN"
		col := rawPartition
		if openIdx := strings.Index(rawPartition, "("); openIdx != -1 && strings.HasSuffix(rawPartition, ")") {
			fn = strings.ToUpper(strings.TrimSpace(rawPartition[:openIdx]))
			col = strings.TrimSpace(rawPartition[openIdx+1 : len(rawPartition)-1])
		}
		col = cleanIdentifier(col)

		colFound := false
		for _, c := range schema.Columns {
			if strings.EqualFold(c.Name, col) {
				colFound = true
				break
			}
		}
		if !colFound {
			return nil, fmt.Errorf("partition column %q not found in table %q", col, tableName)
		}

		schema.Partition = &storage.PartitionDef{
			Expr:   rawPartition,
			Column: col,
			Func:   fn,
		}
	}

	if err := s.engine.CreateTable(schema); err != nil {
		if sqlutil.ContainsKeyword(query, "IF NOT EXISTS", false) && errors.Is(err, storage.ErrTableExists) {
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
		creatorID := "default"
		if cs != nil && cs.ID != "" {
			creatorID = cs.ID
		}
		s.RegisterGlobalTemp(tableName, creatorID)
	} else if strings.HasPrefix(tableName, "#") {
		if cs != nil {
			cs.Mu.Lock()
			cs.TempTables = append(cs.TempTables, tableName)
			cs.Mu.Unlock()
		}
	}

	s.InvalidatePKBypassCache()

	return &QueryResult{
		Success:    true,
		Command:    "CREATE TABLE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Table %q created successfully with %d columns", tableName, len(schema.Columns))},
	}, nil
}

func (s *Coordinator) execDropTable(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	if !sqlutil.HasPrefixKeyword(cleaned, "DROP TABLE") {
		return nil, fmt.Errorf("invalid DROP TABLE syntax")
	}
	rest := strings.TrimSpace(cleaned[len("DROP TABLE"):])
	if sqlutil.HasPrefixKeyword(rest, "IF EXISTS") {
		rest = strings.TrimSpace(rest[len("IF EXISTS"):])
	}
	tokens := sqlutil.SplitTopLevelTokens(rest)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("invalid DROP TABLE syntax: missing table name")
	}

	tableName := cleanIdentifier(tokens[0])
	if err := s.engine.DropTable(tableName); err != nil {
		if sqlutil.ContainsKeyword(query, "IF EXISTS", false) && errors.Is(err, storage.ErrTableNotFound) {
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
		s.UnregisterGlobalTemp(tableName)
	} else if cs != nil {
		cs.UnregisterTempTable(tableName)
	}

	// Clean up any triggers attached to this table
	if s.catalog != nil {
		for _, trg := range s.catalog.GetTableTriggers(tableName) {
			_ = s.catalog.DropTrigger(trg.Name)
			if s.executor != nil {
				s.executor.UnregisterTrigger(trg.Name)
			}
		}
	}

	s.InvalidatePKBypassCache()
	s.SetTableLastIdentity(tableName, 0)

	return &QueryResult{
		Success:    true,
		Command:    "DROP TABLE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Table %q dropped successfully", tableName)},
	}, nil
}

// parseTruncateClause parses TRUNCATE [TABLE] [ONLY] t1 [*] [, t2 ...] [RESTART IDENTITY | CONTINUE IDENTITY] [CASCADE | RESTRICT].
func parseTruncateClause(cleaned string) (tables []string, cascade bool, restartIdentity bool, err error) {
	if !sqlutil.HasPrefixKeyword(cleaned, "TRUNCATE") {
		return nil, false, false, fmt.Errorf("invalid TRUNCATE TABLE syntax")
	}
	rest := strings.TrimSpace(cleaned[len("TRUNCATE"):])
	if sqlutil.HasPrefixKeyword(rest, "TABLE") {
		rest = strings.TrimSpace(rest[len("TABLE"):])
	}
	if rest == "" {
		return nil, false, false, fmt.Errorf("invalid TRUNCATE TABLE syntax: missing table name")
	}

	// Parse trailing options: CASCADE, RESTRICT, RESTART IDENTITY, CONTINUE IDENTITY
	cascade = false
	restartIdentity = true // default in standard TRUNCATE
	changed := true
	for changed {
		changed = false
		trimmed := strings.TrimSpace(rest)
		upper := strings.ToUpper(trimmed)
		if strings.HasSuffix(upper, " CASCADE") {
			cascade = true
			rest = strings.TrimSpace(trimmed[:len(trimmed)-len(" CASCADE")])
			changed = true
		} else if strings.HasSuffix(upper, " RESTRICT") {
			cascade = false
			rest = strings.TrimSpace(trimmed[:len(trimmed)-len(" RESTRICT")])
			changed = true
		} else if strings.HasSuffix(upper, " RESTART IDENTITY") {
			restartIdentity = true
			rest = strings.TrimSpace(trimmed[:len(trimmed)-len(" RESTART IDENTITY")])
			changed = true
		} else if strings.HasSuffix(upper, " CONTINUE IDENTITY") {
			restartIdentity = false
			rest = strings.TrimSpace(trimmed[:len(trimmed)-len(" CONTINUE IDENTITY")])
			changed = true
		}
	}

	rawParts := sqlutil.SplitTopLevel(rest, ',')
	if len(rawParts) == 0 {
		return nil, false, false, fmt.Errorf("invalid TRUNCATE TABLE syntax: missing table name")
	}

	for _, p := range rawParts {
		s := strings.TrimSpace(p)
		if sqlutil.HasPrefixKeyword(s, "ONLY") {
			s = strings.TrimSpace(s[len("ONLY"):])
		}
		s = strings.TrimPrefix(s, "(")
		s = strings.TrimSuffix(s, ")")
		s = strings.TrimSuffix(strings.TrimSpace(s), "*")
		name := cleanIdentifier(s)
		if name != "" {
			tables = append(tables, name)
		}
	}

	if len(tables) == 0 {
		return nil, false, false, fmt.Errorf("invalid TRUNCATE TABLE syntax: missing table name")
	}
	return tables, cascade, restartIdentity, nil
}

func (s *Coordinator) execTruncateTable(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	tableNames, isCascade, restartIdentity, err := parseTruncateClause(cleaned)
	if err != nil {
		return nil, err
	}

	// Resolve session temp tables
	resolvedTables := make([]string, 0, len(tableNames))
	for _, tableName := range tableNames {
		if cs != nil && cs.TempTableMap != nil {
			if realName, ok := cs.TempTableMap[tableName]; ok {
				tableName = realName
			}
		}
		resolvedTables = append(resolvedTables, tableName)
	}

	// If inside an active transaction, perform transactional truncate across all specified tables
	if cs != nil && cs.Tx != nil {
		targetSet := make(map[string]bool, len(resolvedTables))
		for _, t := range resolvedTables {
			targetSet[strings.ToLower(t)] = true
		}

		allTables, err := s.engine.ListTables()
		if err != nil {
			return nil, err
		}

		if isCascade {
			changed := true
			for changed {
				changed = false
				for _, other := range allTables {
					if targetSet[strings.ToLower(other.Name)] {
						continue
					}
					for _, fk := range other.ForeignKeys {
						if targetSet[strings.ToLower(fk.ParentTable)] {
							targetSet[strings.ToLower(other.Name)] = true
							resolvedTables = append(resolvedTables, other.Name)
							changed = true
							break
						}
					}
				}
			}
		}

		// Verify FK dependencies
		for _, other := range allTables {
			if targetSet[strings.ToLower(other.Name)] {
				continue
			}
			for _, fk := range other.ForeignKeys {
				if targetSet[strings.ToLower(fk.ParentTable)] {
					return nil, fmt.Errorf("%w: cannot truncate table %q because it is referenced by foreign key %q on table %q",
						storage.ErrDependentObjectsExist, fk.ParentTable, fk.Name, other.Name)
				}
			}
		}

		for _, tableName := range resolvedTables {
			tbl, err := s.engine.GetTable(tableName)
			if err != nil {
				return nil, err
			}

			cur, err := cs.Tx.Scan(tableName)
			if err != nil {
				return nil, err
			}

			var pkColName string
			var pkColIdx int = -1
			isCompPK := tbl.IsCompositePK()
			if !isCompPK {
				if pkCol, perr := tbl.PrimaryKeyColumn(); perr == nil && pkCol != nil {
					pkColName = pkCol.Name
					_, pkColIdx, _ = tbl.FindColumn(pkColName)
				}
			}

			var pks []storage.Value
			for cur.Next() {
				if isCompPK {
					if rec, rcerr := cur.Record(); rcerr == nil {
						if pkVal, ok := tbl.ResolveRecordPK(rec); ok {
							pks = append(pks, pkVal)
						}
					}
					continue
				}
				row, rerr := cur.Row()
				if rerr == nil && len(row.Values) > 0 {
					if pkColIdx >= 0 && pkColIdx < len(row.Values) {
						pks = append(pks, row.Values[pkColIdx])
					} else {
						pks = append(pks, row.Values[0])
					}
				} else {
					rec, rcerr := cur.Record()
					if rcerr == nil {
						if pkColName != "" {
							if v, ok := rec[pkColName]; ok {
								pks = append(pks, v)
								continue
							}
							for k, v := range rec {
								if strings.EqualFold(k, pkColName) {
									pks = append(pks, v)
									continue
								}
							}
						}
						if v, ok := rec["id"]; ok {
							pks = append(pks, v)
						}
					}
				}
			}
			cur.Close()

			for _, pk := range pks {
				if err := cs.Tx.Delete(tableName, pk); err != nil {
					return nil, err
				}
			}

			if restartIdentity {
				s.SetTableLastIdentity(tableName, 0)
			}
		}

		s.InvalidatePKBypassCache()
		return &QueryResult{
			Success:    true,
			Command:    "TRUNCATE TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Table(s) %s truncated successfully", strings.Join(resolvedTables, ", "))},
		}, nil
	}

	if err := s.engine.TruncateTables(resolvedTables, isCascade); err != nil {
		return nil, err
	}

	s.InvalidatePKBypassCache()
	if restartIdentity {
		for _, tableName := range resolvedTables {
			s.SetTableLastIdentity(tableName, 0)
		}
	}

	return &QueryResult{
		Success:    true,
		Command:    "TRUNCATE TABLE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Table(s) %s truncated successfully", strings.Join(resolvedTables, ", "))},
	}, nil
}

func (s *Coordinator) execCreateIndex(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// 1. Try Pratt parser first (full support for functional expression indexes & double parens)
	p := proc.NewParser(proc.NewLexer(cleaned))
	if stmt, err := p.ParseCreateIndex(); err == nil && stmt != nil && (len(stmt.Columns) > 0 || stmt.Expression != "") {
		idxSchema := &storage.IndexSchema{
			Name:        stmt.Name,
			TableName:   stmt.Table,
			Columns:     stmt.Columns,
			IncludeCols: stmt.IncludeCols,
			Unique:      stmt.Unique,
			Expression:  stmt.Expression,
			ExprColumn:  stmt.ExprColumn,
			ExprPath:    stmt.ExprPath,
		}
		if err := s.engine.CreateIndex(idxSchema); err != nil {
			if stmt.IfNotExists || strings.Contains(strings.ToLower(err.Error()), "already exists") {
				return &QueryResult{
					Success:    true,
					Command:    "CREATE INDEX",
					Messages:   []string{fmt.Sprintf("Index %q already exists on table %q.", stmt.Name, stmt.Table)},
					DurationUS: time.Since(start).Microseconds(),
				}, nil
			}
			return nil, err
		}
		msg := fmt.Sprintf("Index %q created successfully on table %q.", stmt.Name, stmt.Table)
		if stmt.Unique {
			msg = fmt.Sprintf("Unique index %q created successfully on table %q.", stmt.Name, stmt.Table)
		}
		return &QueryResult{
			Success:    true,
			Command:    "CREATE INDEX",
			Messages:   []string{msg},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	return nil, fmt.Errorf("invalid CREATE INDEX syntax: expected CREATE [UNIQUE] INDEX [IF NOT EXISTS] <name> ON <table> (<cols>) [INCLUDE (<cols>)] [WHERE ...]")
}

func (s *Coordinator) execDropIndex(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	if !sqlutil.HasPrefixKeyword(cleaned, "DROP INDEX") {
		return nil, fmt.Errorf("invalid DROP INDEX syntax: expected DROP INDEX [IF EXISTS] <name> [ON <table>] [CASCADE]")
	}
	rest := strings.TrimSpace(cleaned[len("DROP INDEX"):])
	ifExists := false
	if sqlutil.HasPrefixKeyword(rest, "IF EXISTS") {
		ifExists = true
		rest = strings.TrimSpace(rest[len("IF EXISTS"):])
	}
	tokens := sqlutil.SplitTopLevelTokens(rest)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("invalid DROP INDEX syntax: expected DROP INDEX [IF EXISTS] <name> [ON <table>] [CASCADE]")
	}

	indexName := cleanIdentifier(tokens[0])
	tableName := ""
	for i := 1; i < len(tokens); i++ {
		if strings.EqualFold(tokens[i], "ON") && i+1 < len(tokens) {
			tableName = cleanIdentifier(tokens[i+1])
			i++
		}
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
			if ifExists {
				return &QueryResult{
					Success:    true,
					Command:    "DROP INDEX",
					Messages:   []string{fmt.Sprintf("Index %q does not exist (skipped)", indexName)},
					DurationUS: time.Since(start).Microseconds(),
				}, nil
			}
			return nil, fmt.Errorf("index %q not found on any table", indexName)
		}
	}

	if err := s.engine.DropIndex(tableName, indexName); err != nil {
		if ifExists {
			return &QueryResult{
				Success:    true,
				Command:    "DROP INDEX",
				Messages:   []string{fmt.Sprintf("Index %q does not exist on table %q (skipped)", indexName, tableName)},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}
		return nil, err
	}

	return &QueryResult{
		Success:    true,
		Command:    "DROP INDEX",
		Messages:   []string{fmt.Sprintf("Index %q dropped successfully from table %q.", indexName, tableName)},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execCreateProcedure(query string, start time.Time) (*QueryResult, error) {
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
		Name:           plan.Name,
		Body:           query,
		BytecodeDisasm: prog.Disassemble(),
		Params:         params,
		CreatedAt:      time.Now(),
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

func (s *Coordinator) execCreateFunction(query string, start time.Time) (*QueryResult, error) {
	// 1. Check if Table-Valued Function (TVF): CREATE FUNCTION ... RETURNS TABLE AS RETURN ( ... )
	cleanedFunc := strings.TrimRight(strings.TrimSpace(query), ";")
	if sqlutil.HasPrefixKeyword(cleanedFunc, "CREATE FUNCTION") && sqlutil.ContainsKeyword(cleanedFunc, "RETURNS TABLE", false) && sqlutil.ContainsKeyword(cleanedFunc, "RETURN", false) {
		openParen := sqlutil.FindTopLevelChar(cleanedFunc, '(')
		if openParen != -1 {
			fnName := cleanIdentifier(strings.TrimSpace(cleanedFunc[len("CREATE FUNCTION"):openParen]))
			closeParen := sqlutil.FindMatchingParen(cleanedFunc, openParen)
			if closeParen != -1 {
				rawParams := cleanedFunc[openParen+1 : closeParen]
				afterParams := cleanedFunc[closeParen+1:]
				returnIdx := sqlutil.FindTopLevelKeyword(afterParams, "RETURN")
				if returnIdx != -1 {
					bodyPart := strings.TrimSpace(afterParams[returnIdx+len("RETURN"):])
					if strings.HasPrefix(bodyPart, "(") && strings.HasSuffix(bodyPart, ")") {
						bodyPart = strings.TrimSpace(bodyPart[1 : len(bodyPart)-1])
					}
					if sqlutil.HasPrefixKeyword(bodyPart, "SELECT") {
						innerQuery := bodyPart
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
				}
			}
		}
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
		Name:           fnStmt.Name,
		ReturnType:     fnStmt.ReturnType,
		Body:           query,
		BytecodeDisasm: prog.Disassemble(),
		Params:         params,
		CreatedAt:      time.Now(),
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

func (s *Coordinator) execCreateTrigger(query string, start time.Time) (*QueryResult, error) {
	trgStmt, err := proc.ParseTrigger(query)
	if err != nil {
		return nil, fmt.Errorf("parse trigger error: %w", err)
	}

	prog, err := proc.CompileTrigger(trgStmt)
	if err != nil {
		return nil, fmt.Errorf("compile trigger error: %w", err)
	}

	trgMeta := &catalog.Trigger{
		Name:           trgStmt.Name,
		Table:          trgStmt.Table,
		Timing:         trgStmt.Timing,
		Events:         trgStmt.Events,
		Body:           query,
		BytecodeDisasm: prog.Disassemble(),
		CreatedAt:      time.Now(),
	}

	upper := strings.ToUpper(strings.TrimSpace(query))
	if strings.HasPrefix(upper, "CREATE OR ALTER TRIGGER") || strings.HasPrefix(upper, "ALTER TRIGGER") {
		if err := s.catalog.RegisterTrigger(trgMeta); err != nil {
			return nil, fmt.Errorf("catalog register trigger error: %w", err)
		}
	} else {
		if err := s.catalog.CreateTrigger(trgMeta); err != nil {
			return nil, fmt.Errorf("catalog create trigger error: %w", err)
		}
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

func (s *Coordinator) execDropTrigger(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	if !sqlutil.HasPrefixKeyword(cleaned, "DROP TRIGGER") {
		return nil, fmt.Errorf("invalid DROP TRIGGER syntax")
	}
	rest := strings.TrimSpace(cleaned[len("DROP TRIGGER"):])
	ifExists := false
	if sqlutil.HasPrefixKeyword(rest, "IF EXISTS") {
		ifExists = true
		rest = strings.TrimSpace(rest[len("IF EXISTS"):])
	}
	tokens := sqlutil.SplitTopLevelTokens(rest)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("invalid DROP TRIGGER syntax: missing trigger name")
	}

	trgName := cleanIdentifier(tokens[0])
	if err := s.catalog.DropTrigger(trgName); err != nil {
		if ifExists && errors.Is(err, catalog.ErrTriggerNotFound) {
			return &QueryResult{
				Success:    true,
				Command:    "DROP TRIGGER",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Trigger %q does not exist (skipped)", trgName)},
			}, nil
		}
		return nil, err
	}

	if s.executor != nil {
		s.executor.UnregisterTrigger(trgName)
	}

	return &QueryResult{
		Success:    true,
		Command:    "DROP TRIGGER",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Trigger %q dropped successfully", trgName)},
	}, nil
}

func (s *Coordinator) execReindex(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	if !sqlutil.HasPrefixKeyword(cleaned, "REINDEX") {
		return nil, fmt.Errorf("invalid REINDEX syntax")
	}
	rest := strings.TrimSpace(cleaned[len("REINDEX"):])
	tokens := sqlutil.SplitTopLevelTokens(rest)

	target := ""
	if len(tokens) > 0 {
		target = cleanIdentifier(tokens[0])
	}

	if target == "" {
		// Reindex all tables
		return &QueryResult{
			Success:    true,
			Command:    "REINDEX",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"Reindexed all tables and indices successfully"},
		}, nil
	}

	// Verify target exists as either a table or an index
	tables, err := s.engine.ListTables()
	if err != nil {
		return nil, err
	}
	found := false
	for _, t := range tables {
		if sqlutil.EqualFoldIdent(t.Name, target) {
			found = true
			break
		}
		if _, _, ok := t.FindIndex(target); ok {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("table or index %q not found", target)
	}

	return &QueryResult{
		Success:    true,
		Command:    "REINDEX",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Index or table %q reindexed successfully", target)},
	}, nil
}

func parseSQLDataType(s string) storage.DataType {
	switch strings.ToUpper(s) {
	case "INT", "INTEGER", "BIGINT", "SMALLINT", "TINYINT", "IDENTITY", "SERIAL", "BIGSERIAL", "SMALLSERIAL", "INT2", "INT4", "INT8":
		return storage.TypeInt
	case "FLOAT", "DOUBLE", "REAL", "FLOAT4", "FLOAT8", "DOUBLE PRECISION":
		return storage.TypeFloat
	case "NUMERIC", "DECIMAL", "MONEY":
		return storage.TypeDecimal
	case "BOOL", "BOOLEAN", "BIT":
		return storage.TypeBool
	case "DATETIME", "TIMESTAMP", "TIMESTAMPTZ", "DATETIME2", "SMALLDATETIME", "DATETIMEOFFSET", "TIMESTAMP WITH TIME ZONE", "TIMESTAMP WITHOUT TIME ZONE":
		return storage.TypeDateTime
	case "DATE":
		return storage.TypeDate
	case "TIME", "TIMETZ", "TIME WITH TIME ZONE", "TIME WITHOUT TIME ZONE":
		return storage.TypeTime
	case "UUID", "UNIQUEIDENTIFIER", "GUID":
		return storage.TypeUUID
	case "VARBINARY", "BINARY", "BYTEA", "BLOB", "IMAGE":
		return storage.TypeBinary
	case "JSON", "JSONB":
		return storage.TypeJSON
	default:
		return storage.TypeVarChar
	}
}

func splitColumnDefs(s string) []string {
	return sqlutil.SplitTopLevel(s, ',')
}

func cleanDefaultExpr(s string) string {
	s = strings.TrimSpace(s)
	// Strip redundant outer parentheses e.g. ((0)) -> 0 or (NEWID7()) -> NEWID7()
	for strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		inner := s[1 : len(s)-1]
		depth := 0
		balanced := true
		for i := 0; i < len(inner); i++ {
			if inner[i] == '(' {
				depth++
			} else if inner[i] == ')' {
				depth--
				if depth < 0 {
					balanced = false
					break
				}
			}
		}
		if balanced && depth == 0 {
			// Don't strip arguments from function calls e.g. "NEWID7()"
			if strings.HasSuffix(s, "()") && !strings.HasPrefix(inner, "(") && strings.Index(s, "(") == len(s)-2 {
				break
			}
			s = strings.TrimSpace(inner)
		} else {
			break
		}
	}
	return s
}

func parseForeignKeyClause(def string, defaultChildCol string, defaultName string) (*storage.ForeignKeySchema, bool) {
	defTrimmed := strings.TrimSpace(def)
	if !sqlutil.ContainsKeyword(defTrimmed, "REFERENCES", false) {
		return nil, false
	}

	fk := storage.ForeignKeySchema{
		Name:     defaultName,
		OnDelete: storage.ActionNoAction,
		OnUpdate: storage.ActionNoAction,
	}

	if delIdx := sqlutil.FindTopLevelKeyword(defTrimmed, "ON DELETE"); delIdx != -1 {
		actionStr := strings.TrimSpace(defTrimmed[delIdx+len("ON DELETE"):])
		tokens := strings.Fields(actionStr)
		if len(tokens) > 0 {
			act := tokens[0]
			if len(tokens) > 1 && (strings.EqualFold(tokens[0], "SET") || strings.EqualFold(tokens[0], "NO")) {
				act = tokens[0] + " " + tokens[1]
			}
			fk.OnDelete = storage.ParseForeignKeyAction(act)
		}
	}

	if updIdx := sqlutil.FindTopLevelKeyword(defTrimmed, "ON UPDATE"); updIdx != -1 {
		actionStr := strings.TrimSpace(defTrimmed[updIdx+len("ON UPDATE"):])
		tokens := strings.Fields(actionStr)
		if len(tokens) > 0 {
			act := tokens[0]
			if len(tokens) > 1 && (strings.EqualFold(tokens[0], "SET") || strings.EqualFold(tokens[0], "NO")) {
				act = tokens[0] + " " + tokens[1]
			}
			fk.OnUpdate = storage.ParseForeignKeyAction(act)
		}
	}

	refIdx := sqlutil.FindTopLevelKeyword(defTrimmed, "REFERENCES")
	if refIdx == -1 {
		return nil, false
	}

	beforeRef := strings.TrimSpace(defTrimmed[:refIdx])
	afterRef := strings.TrimSpace(defTrimmed[refIdx+len("REFERENCES"):])

	if delIdx := sqlutil.FindTopLevelKeyword(afterRef, "ON DELETE"); delIdx != -1 {
		afterRef = strings.TrimSpace(afterRef[:delIdx])
	}
	if updIdx := sqlutil.FindTopLevelKeyword(afterRef, "ON UPDATE"); updIdx != -1 {
		afterRef = strings.TrimSpace(afterRef[:updIdx])
	}

	if openIdx := sqlutil.FindTopLevelChar(afterRef, '('); openIdx != -1 {
		fk.ParentTable = cleanIdentifier(strings.TrimSpace(afterRef[:openIdx]))
		closeIdx := sqlutil.FindMatchingParen(afterRef, openIdx)
		if closeIdx != -1 {
			for _, c := range strings.Split(afterRef[openIdx+1:closeIdx], ",") {
				if col := cleanIdentifier(c); col != "" {
					fk.ParentColumns = append(fk.ParentColumns, col)
				}
			}
		}
	} else {
		tokens := sqlutil.SplitTopLevelTokens(afterRef)
		if len(tokens) > 0 {
			fk.ParentTable = cleanIdentifier(tokens[0])
		}
	}

	if fkIdx := sqlutil.FindTopLevelKeyword(beforeRef, "FOREIGN KEY"); fkIdx != -1 {
		beforeFK := strings.TrimSpace(beforeRef[:fkIdx])
		if cIdx := sqlutil.FindTopLevelKeyword(beforeFK, "CONSTRAINT"); cIdx != -1 {
			cTokens := sqlutil.SplitTopLevelTokens(strings.TrimSpace(beforeFK[cIdx+len("CONSTRAINT"):]))
			if len(cTokens) > 0 {
				fk.Name = cleanIdentifier(cTokens[0])
			}
		}
		afterFK := strings.TrimSpace(beforeRef[fkIdx+len("FOREIGN KEY"):])
		if openIdx := sqlutil.FindTopLevelChar(afterFK, '('); openIdx != -1 {
			closeIdx := sqlutil.FindMatchingParen(afterFK, openIdx)
			if closeIdx != -1 {
				for _, c := range strings.Split(afterFK[openIdx+1:closeIdx], ",") {
					if col := cleanIdentifier(c); col != "" {
						fk.ChildColumns = append(fk.ChildColumns, col)
					}
				}
			}
		}
		if fk.Name == "" && len(fk.ChildColumns) > 0 {
			fk.Name = fmt.Sprintf("fk_%s_%s", fk.ParentTable, fk.ChildColumns[0])
		}
		return &fk, true
	}

	if defaultChildCol != "" {
		fk.ChildColumns = []string{defaultChildCol}
	}
	if fk.Name == "" && defaultChildCol != "" {
		fk.Name = fmt.Sprintf("fk_%s_%s", fk.ParentTable, defaultChildCol)
	}
	return &fk, true
}

// execCreateView handles CREATE [OR REPLACE] [TEMP|TEMPORARY] VIEW [IF NOT EXISTS] name AS <select>.
func (s *Coordinator) execCreateView(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	asIdx := sqlutil.FindTopLevelKeyword(cleaned, "AS")
	if asIdx == -1 {
		return nil, fmt.Errorf("invalid CREATE VIEW syntax: missing AS keyword")
	}

	beforeAS := strings.TrimSpace(cleaned[:asIdx])
	selectQuery := strings.TrimSpace(cleaned[asIdx+2:])

	orReplace := sqlutil.ContainsKeyword(beforeAS, "OR REPLACE", false)
	ifNotExists := sqlutil.ContainsKeyword(beforeAS, "IF NOT EXISTS", false)

	viewIdx := sqlutil.FindTopLevelKeyword(beforeAS, "VIEW")
	if viewIdx == -1 {
		return nil, fmt.Errorf("invalid CREATE VIEW syntax")
	}

	afterView := strings.TrimSpace(beforeAS[viewIdx+4:])
	if sqlutil.HasPrefixKeyword(afterView, "IF NOT EXISTS") {
		afterView = strings.TrimSpace(afterView[len("IF NOT EXISTS"):])
	}

	tokens := sqlutil.SplitTopLevelTokens(afterView)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("invalid CREATE VIEW syntax: missing view name")
	}

	viewName := cleanIdentifier(tokens[0])

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

	s.InvalidatePKBypassCache()

	return &QueryResult{
		Success:      true,
		Command:      "CREATE VIEW",
		RowsAffected: 0,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execDropView handles DROP VIEW [IF EXISTS] <name>.
func (s *Coordinator) execDropView(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	if !sqlutil.HasPrefixKeyword(cleaned, "DROP VIEW") {
		return nil, fmt.Errorf("invalid DROP VIEW syntax")
	}

	rest := strings.TrimSpace(cleaned[len("DROP VIEW"):])
	ifExists := false
	if sqlutil.HasPrefixKeyword(rest, "IF EXISTS") {
		ifExists = true
		rest = strings.TrimSpace(rest[len("IF EXISTS"):])
	}

	tokens := sqlutil.SplitTopLevelTokens(rest)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("invalid DROP VIEW syntax: missing view name")
	}

	viewName := cleanIdentifier(tokens[0])

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

	s.InvalidatePKBypassCache()

	return &QueryResult{
		Success:      true,
		Command:      "DROP VIEW",
		RowsAffected: 0,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execAlterTable(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	tableName, ifExists, body, err := parseAlterTablePrefix(cleaned)
	if err != nil {
		return nil, err
	}

	// 0. Compound ALTER TABLE with multiple top-level comma-separated actions:
	// e.g. "ALTER TABLE attachments ALTER COLUMN file_size TYPE BIGINT, ALTER COLUMN file_size SET NOT NULL;"
	// or "ALTER TABLE sso_users DROP CONSTRAINT ..., ADD CONSTRAINT ..."
	segments := sqlutil.SplitTopLevel(body, ',')
	if len(segments) > 1 {
		firstTok := strings.ToUpper(strings.Fields(strings.TrimSpace(segments[1]))[0])
		if firstTok == "ADD" || firstTok == "DROP" || firstTok == "ALTER" || firstTok == "RENAME" {
			prefix := "ALTER TABLE "
			if ifExists {
				prefix += "IF EXISTS "
			}
			prefix += tableName
			var lastRes *QueryResult
			for _, seg := range segments {
				segTrimmed := strings.TrimSpace(seg)
				if segTrimmed == "" {
					continue
				}
				subSQL := prefix + " " + segTrimmed
				res, err := s.execAlterTable(subSQL, start, cs)
				if err != nil {
					return nil, err
				}
				lastRes = res
			}
			if lastRes != nil {
				return lastRes, nil
			}
		}
	}

	// 1. Check for IF EXISTS table clause
	if ifExists {
		if _, err := s.engine.GetTable(tableName); err != nil && errors.Is(err, storage.ErrTableNotFound) {
			return &QueryResult{
				Success:    true,
				Command:    "ALTER TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Table %q does not exist (skipped)", tableName)},
			}, nil
		}
	}

	bodyUpper := strings.ToUpper(body)

	// 2. ALTER TABLE <table> DROP PARTITION '<part>' (or without quotes)
	if sqlutil.HasPrefixKeyword(body, "DROP PARTITION") {
		partRaw := strings.TrimSpace(body[len("DROP PARTITION"):])
		partitionName := strings.Trim(strings.TrimSpace(partRaw), "';\" ")

		if err := s.checkTableAccess(cs, tableName, auth.PrivDelete); err != nil {
			return nil, err
		}

		if err := s.engine.DropPartition(tableName, partitionName); err != nil {
			return nil, err
		}

		s.InvalidatePKBypassCache()

		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Partition %q dropped from table %q", partitionName, tableName)},
		}, nil
	}

	// 3. ALTER TABLE <table> ADD [CONSTRAINT <name>] UNIQUE (<cols>)
	if sqlutil.HasPrefixKeyword(body, "ADD") && (sqlutil.ContainsKeyword(body, "UNIQUE", false) || strings.Contains(bodyUpper, "UNIQUE(")) && !sqlutil.ContainsKeyword(body, "COLUMN", false) {
		openIdx := sqlutil.FindTopLevelChar(body, '(')
		if openIdx != -1 {
			closeIdx := sqlutil.FindMatchingParen(body, openIdx)
			if closeIdx != -1 {
				colsRaw := body[openIdx+1 : closeIdx]
				var cols []string
				for _, c := range strings.Split(colsRaw, ",") {
					ct := cleanIdentifier(strings.TrimSpace(c))
					if ct != "" {
						cols = append(cols, ct)
					}
				}
				beforeOpen := strings.TrimSpace(body[:openIdx])
				cName := ""
				if cIdx := sqlutil.FindTopLevelKeyword(beforeOpen, "CONSTRAINT"); cIdx != -1 {
					cTokens := sqlutil.SplitTopLevelTokens(strings.TrimSpace(beforeOpen[cIdx+len("CONSTRAINT"):]))
					if len(cTokens) > 0 {
						cName = cleanIdentifier(cTokens[0])
					}
				}
				if cName == "" {
					cName = fmt.Sprintf("uq_%s_%s", tableName, strings.Join(cols, "_"))
				}

				if err := s.checkTableAccess(cs, tableName, auth.PrivUpdate); err != nil {
					return nil, err
				}

				idxSchema := &storage.IndexSchema{
					Name:      cName,
					TableName: tableName,
					Columns:   cols,
					Unique:    true,
				}
				_ = s.engine.CreateIndex(idxSchema)
				s.InvalidatePKBypassCache()

				return &QueryResult{
					Success:    true,
					Command:    "ALTER TABLE",
					DurationUS: time.Since(start).Microseconds(),
					Messages:   []string{fmt.Sprintf("Unique constraint %q added to table %q", cName, tableName)},
				}, nil
			}
		}
	}

	// 4. ALTER TABLE <table> ADD [CONSTRAINT <name>] FOREIGN KEY (<cols>) REFERENCES ...
	if sqlutil.HasPrefixKeyword(body, "ADD") && sqlutil.ContainsKeyword(body, "FOREIGN KEY", false) && sqlutil.ContainsKeyword(body, "REFERENCES", false) {
		cName := ""
		if cIdx := sqlutil.FindTopLevelKeyword(body, "CONSTRAINT"); cIdx != -1 {
			cTokens := sqlutil.SplitTopLevelTokens(strings.TrimSpace(body[cIdx+len("CONSTRAINT"):]))
			if len(cTokens) > 0 {
				cName = cleanIdentifier(cTokens[0])
			}
		}
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Foreign key constraint %q added to table %q", cName, tableName)},
		}, nil
	}

	// 5. ALTER TABLE <table> ADD [CONSTRAINT <name>] CHECK (...)
	if sqlutil.HasPrefixKeyword(body, "ADD") && (sqlutil.ContainsKeyword(body, "CHECK", false) || strings.Contains(bodyUpper, "CHECK(")) && !sqlutil.ContainsKeyword(body, "COLUMN", false) {
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Check constraint added to table %q", tableName)},
		}, nil
	}

	// 6. ALTER TABLE <table> ADD [CONSTRAINT <name>] PRIMARY KEY (<cols>)
	if sqlutil.HasPrefixKeyword(body, "ADD") && sqlutil.ContainsKeyword(body, "PRIMARY KEY", false) && !sqlutil.ContainsKeyword(body, "COLUMN", false) {
		openIdx := sqlutil.FindTopLevelChar(body, '(')
		if openIdx != -1 {
			closeIdx := sqlutil.FindMatchingParen(body, openIdx)
			if closeIdx != -1 {
				colsRaw := body[openIdx+1 : closeIdx]
				var cols []string
				for _, c := range strings.Split(colsRaw, ",") {
					ct := cleanIdentifier(strings.TrimSpace(c))
					if ct != "" {
						cols = append(cols, ct)
					}
				}
				cName := ""
				beforeOpen := strings.TrimSpace(body[:openIdx])
				if cIdx := sqlutil.FindTopLevelKeyword(beforeOpen, "CONSTRAINT"); cIdx != -1 {
					cTokens := sqlutil.SplitTopLevelTokens(strings.TrimSpace(beforeOpen[cIdx+len("CONSTRAINT"):]))
					if len(cTokens) > 0 {
						cName = cleanIdentifier(cTokens[0])
					}
				}
				_ = s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
					for i := range sch.Columns {
						sch.Columns[i].PrimaryKey = false
					}
					for _, col := range cols {
						for i := range sch.Columns {
							if strings.EqualFold(sch.Columns[i].Name, col) {
								sch.Columns[i].PrimaryKey = true
							}
						}
					}
					return nil
				})
				s.InvalidatePKBypassCache()
				return &QueryResult{
					Success:    true,
					Command:    "ALTER TABLE",
					DurationUS: time.Since(start).Microseconds(),
					Messages:   []string{fmt.Sprintf("Primary key %q added to table %q", cName, tableName)},
				}, nil
			}
		}
	}

	// 7. ALTER TABLE <table> DROP CONSTRAINT [IF EXISTS] <name>
	if sqlutil.HasPrefixKeyword(body, "DROP CONSTRAINT") {
		rest := strings.TrimSpace(body[len("DROP CONSTRAINT"):])
		if sqlutil.HasPrefixKeyword(rest, "IF EXISTS") {
			rest = strings.TrimSpace(rest[len("IF EXISTS"):])
		}
		tokens := sqlutil.SplitTopLevelTokens(rest)
		if len(tokens) > 0 {
			cName := cleanIdentifier(tokens[0])
			if strings.HasSuffix(strings.ToLower(cName), "_pkey") || strings.Contains(cleaned, "PRIMARY KEY") {
				_ = s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
					for i := range sch.Columns {
						sch.Columns[i].PrimaryKey = false
					}
					return nil
				})
			}
			_ = s.engine.DropIndex(tableName, cName)
			s.InvalidatePKBypassCache()

			return &QueryResult{
				Success:    true,
				Command:    "ALTER TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Constraint %q dropped from table %q", cName, tableName)},
			}, nil
		}
	}

	// 8. ALTER TABLE <table> ALTER [COLUMN] <col> DROP DEFAULT
	if sqlutil.HasPrefixKeyword(body, "ALTER") && sqlutil.ContainsKeyword(body, "DROP DEFAULT", false) {
		afterAlter := strings.TrimSpace(body[len("ALTER"):])
		if sqlutil.HasPrefixKeyword(afterAlter, "COLUMN") {
			afterAlter = strings.TrimSpace(afterAlter[len("COLUMN"):])
		}
		ddIdx := sqlutil.FindTopLevelKeyword(afterAlter, "DROP DEFAULT")
		if ddIdx != -1 {
			colName := cleanIdentifier(strings.TrimSpace(afterAlter[:ddIdx]))
			_ = s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
				for i := range sch.Columns {
					if strings.EqualFold(sch.Columns[i].Name, colName) {
						sch.Columns[i].Default = ""
						break
					}
				}
				return nil
			})
			return &QueryResult{
				Success:    true,
				Command:    "ALTER TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Default dropped from column %q on table %q", colName, tableName)},
			}, nil
		}
	}

	// 9. ALTER TABLE <table> ALTER [COLUMN] <col> SET DEFAULT <expr>
	if sqlutil.HasPrefixKeyword(body, "ALTER") && sqlutil.ContainsKeyword(body, "SET DEFAULT", false) {
		afterAlter := strings.TrimSpace(body[len("ALTER"):])
		if sqlutil.HasPrefixKeyword(afterAlter, "COLUMN") {
			afterAlter = strings.TrimSpace(afterAlter[len("COLUMN"):])
		}
		sdIdx := sqlutil.FindTopLevelKeyword(afterAlter, "SET DEFAULT")
		if sdIdx != -1 {
			colName := cleanIdentifier(strings.TrimSpace(afterAlter[:sdIdx]))
			defExpr := cleanDefaultExpr(strings.TrimSpace(afterAlter[sdIdx+len("SET DEFAULT"):]))
			_ = s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
				for i := range sch.Columns {
					if strings.EqualFold(sch.Columns[i].Name, colName) {
						sch.Columns[i].Default = defExpr
						break
					}
				}
				return nil
			})
			return &QueryResult{
				Success:    true,
				Command:    "ALTER TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Default set on column %q on table %q", colName, tableName)},
			}, nil
		}
	}

	// 10. ALTER TABLE <table> ALTER [COLUMN] <col> [SET DATA] TYPE <new_type> [USING ...]
	if sqlutil.HasPrefixKeyword(body, "ALTER") && (sqlutil.ContainsKeyword(body, "TYPE", false) || sqlutil.ContainsKeyword(body, "SET DATA TYPE", false)) {
		afterAlter := strings.TrimSpace(body[len("ALTER"):])
		if sqlutil.HasPrefixKeyword(afterAlter, "COLUMN") {
			afterAlter = strings.TrimSpace(afterAlter[len("COLUMN"):])
		}
		typeIdx := -1
		typeKwLen := 0
		if idx := sqlutil.FindTopLevelKeyword(afterAlter, "SET DATA TYPE"); idx != -1 {
			typeIdx = idx
			typeKwLen = len("SET DATA TYPE")
		} else if idx := sqlutil.FindTopLevelKeyword(afterAlter, "TYPE"); idx != -1 {
			typeIdx = idx
			typeKwLen = len("TYPE")
		}
		if typeIdx != -1 {
			colName := cleanIdentifier(strings.TrimSpace(afterAlter[:typeIdx]))
			rawTypePart := strings.TrimSpace(afterAlter[typeIdx+typeKwLen:])
			if uIdx := sqlutil.FindTopLevelKeyword(rawTypePart, "USING"); uIdx != -1 {
				rawTypePart = strings.TrimSpace(rawTypePart[:uIdx])
			}
			baseType := strings.ToUpper(rawTypePart)
			if idx := strings.Index(baseType, "("); idx != -1 {
				baseType = strings.TrimSpace(baseType[:idx])
			}
			newType := parseSQLDataType(baseType)
			_ = s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
				for i := range sch.Columns {
					if strings.EqualFold(sch.Columns[i].Name, colName) {
						sch.Columns[i].Type = newType
						break
					}
				}
				return nil
			})
			return &QueryResult{
				Success:    true,
				Command:    "ALTER TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Column %q type changed on table %q to %s", colName, tableName, newType)},
			}, nil
		}
	}

	// 10b. ALTER TABLE <table> RENAME TO <new>
	if sqlutil.HasPrefixKeyword(body, "RENAME TO") {
		newName := cleanIdentifier(strings.TrimSpace(body[len("RENAME TO"):]))
		if err := s.engine.RenameTable(tableName, newName); err != nil {
			return nil, err
		}
		s.InvalidatePKBypassCache()
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Table %q renamed to %q", tableName, newName)},
		}, nil
	}

	// 11. ALTER TABLE <table> RENAME [COLUMN] <old> TO <new>
	if sqlutil.HasPrefixKeyword(body, "RENAME") && sqlutil.ContainsKeyword(body, "TO", false) && !sqlutil.HasPrefixKeyword(body, "RENAME TO") {
		afterRename := strings.TrimSpace(body[len("RENAME"):])
		if sqlutil.HasPrefixKeyword(afterRename, "COLUMN") {
			afterRename = strings.TrimSpace(afterRename[len("COLUMN"):])
		}
		toIdx := sqlutil.FindTopLevelKeyword(afterRename, "TO")
		if toIdx != -1 {
			oldName := cleanIdentifier(strings.TrimSpace(afterRename[:toIdx]))
			newName := cleanIdentifier(strings.TrimSpace(afterRename[toIdx+len("TO"):]))
			_ = s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
				for i := range sch.Columns {
					if strings.EqualFold(sch.Columns[i].Name, oldName) {
						sch.Columns[i].Name = newName
						break
					}
				}
				return nil
			})
			return &QueryResult{
				Success:    true,
				Command:    "ALTER TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Column %q renamed to %q on table %q", oldName, newName, tableName)},
			}, nil
		}
	}

	// 12. ALTER TABLE <table> ALTER [COLUMN] <col> (DROP NOT NULL | SET NOT NULL)
	if sqlutil.HasPrefixKeyword(body, "ALTER") && (sqlutil.ContainsKeyword(body, "DROP NOT NULL", false) || sqlutil.ContainsKeyword(body, "SET NOT NULL", false)) {
		afterAlter := strings.TrimSpace(body[len("ALTER"):])
		if sqlutil.HasPrefixKeyword(afterAlter, "COLUMN") {
			afterAlter = strings.TrimSpace(afterAlter[len("COLUMN"):])
		}
		isDrop := sqlutil.ContainsKeyword(afterAlter, "DROP NOT NULL", false)
		action := "SET NOT NULL"
		nullIdx := sqlutil.FindTopLevelKeyword(afterAlter, "SET NOT NULL")
		if isDrop {
			action = "DROP NOT NULL"
			nullIdx = sqlutil.FindTopLevelKeyword(afterAlter, "DROP NOT NULL")
		}
		if nullIdx != -1 {
			colName := cleanIdentifier(strings.TrimSpace(afterAlter[:nullIdx]))
			_ = s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
				for i := range sch.Columns {
					if strings.EqualFold(sch.Columns[i].Name, colName) {
						sch.Columns[i].Nullable = isDrop
						break
					}
				}
				return nil
			})
			return &QueryResult{
				Success:    true,
				Command:    "ALTER TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Column %q altered on table %q (%s)", colName, tableName, action)},
			}, nil
		}
	}

	// 13. ALTER TABLE <table> DROP [COLUMN] [IF EXISTS] <col> (including multiple comma-separated)
	if sqlutil.HasPrefixKeyword(body, "DROP") && !sqlutil.ContainsKeyword(body, "CONSTRAINT", false) && !sqlutil.ContainsKeyword(body, "PARTITION", false) && !sqlutil.ContainsKeyword(body, "DEFAULT", false) {
		rest := strings.TrimSpace(body[len("DROP"):])
		if sqlutil.HasPrefixKeyword(rest, "COLUMN") {
			rest = strings.TrimSpace(rest[len("COLUMN"):])
		}
		if sqlutil.HasPrefixKeyword(rest, "IF EXISTS") {
			rest = strings.TrimSpace(rest[len("IF EXISTS"):])
		}
		var droppedCols []string
		for _, part := range strings.Split(rest, ",") {
			c := cleanIdentifier(part)
			if c != "" && !strings.EqualFold(c, "IF") && !strings.EqualFold(c, "EXISTS") {
				droppedCols = append(droppedCols, c)
			}
		}
		if len(droppedCols) > 0 {
			_ = s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
				newCols := make([]storage.Column, 0, len(sch.Columns))
				for _, col := range sch.Columns {
					shouldDrop := false
					for _, dc := range droppedCols {
						if strings.EqualFold(col.Name, dc) {
							shouldDrop = true
							break
						}
					}
					if !shouldDrop {
						newCols = append(newCols, col)
					}
				}
				sch.Columns = newCols
				return nil
			})
			return &QueryResult{
				Success:    true,
				Command:    "ALTER TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Columns %s dropped from table %q", strings.Join(droppedCols, ", "), tableName)},
			}, nil
		}
	}

	// 14. ALTER TABLE <table> ADD [COLUMN] ...
	if sqlutil.HasPrefixKeyword(body, "ADD") {
		// RBAC Check
		if err := s.checkTableAccess(cs, tableName, auth.PrivUpdate); err != nil {
			return nil, err
		}

		segments := sqlutil.SplitTopLevel(body, ',')
		var addedCols []string
		for _, seg := range segments {
			segTrimmed := strings.TrimSpace(seg)
			if sqlutil.HasPrefixKeyword(segTrimmed, "ADD") {
				segTrimmed = strings.TrimSpace(segTrimmed[len("ADD"):])
			}
			if sqlutil.HasPrefixKeyword(segTrimmed, "COLUMN") {
				segTrimmed = strings.TrimSpace(segTrimmed[len("COLUMN"):])
			}
			ifNotExists := false
			if sqlutil.HasPrefixKeyword(segTrimmed, "IF NOT EXISTS") {
				ifNotExists = true
				segTrimmed = strings.TrimSpace(segTrimmed[len("IF NOT EXISTS"):])
			}

			fields := strings.Fields(segTrimmed)
			if len(fields) < 2 {
				continue
			}
			colName := cleanIdentifier(fields[0])
			colUpper := strings.ToUpper(colName)
			if colUpper == "CONSTRAINT" || colUpper == "UNIQUE" || colUpper == "CHECK" || colUpper == "FOREIGN" || colUpper == "PRIMARY" {
				continue
			}

			rawType := strings.ToUpper(fields[1])
			baseType := rawType
			if idx := strings.Index(baseType, "("); idx != -1 {
				baseType = strings.TrimSpace(baseType[:idx])
			}
			dataType := parseSQLDataType(baseType)

			precision := 0
			scale := 0
			if dataType == storage.TypeDecimal {
				precision, scale = parseDecimalPrecisionScale(segTrimmed)
			}

			isGeneratedIdentity := sqlutil.ContainsKeyword(segTrimmed, "AS IDENTITY", false) ||
				(sqlutil.ContainsKeyword(segTrimmed, "GENERATED", false) && sqlutil.ContainsKeyword(segTrimmed, "IDENTITY", false))
			segUpperRest := strings.ToUpper(segTrimmed)
			isNotNull := strings.Contains(segUpperRest, "NOT NULL") || isGeneratedIdentity
			nullable := !isNotNull
			isIdentity := strings.Contains(segUpperRest, "IDENTITY") || strings.Contains(segUpperRest, "AUTO_INCREMENT") || isGeneratedIdentity

			defaultExpr := parseColumnDefault(segTrimmed)

			col := &storage.Column{
				Name:      colName,
				Type:      dataType,
				Nullable:  nullable,
				Identity:  isIdentity,
				Precision: precision,
				Scale:     scale,
				Default:   defaultExpr,
			}

			if err := s.engine.AddColumn(tableName, col); err != nil {
				if ifNotExists && strings.Contains(strings.ToLower(err.Error()), "already exists") {
					continue
				}
				return nil, err
			}
			addedCols = append(addedCols, colName)
		}

		s.InvalidatePKBypassCache()

		msg := fmt.Sprintf("Columns %s added to table %q", strings.Join(addedCols, ", "), tableName)
		if len(addedCols) == 0 {
			msg = fmt.Sprintf("No columns added to table %q (already existed)", tableName)
		}
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{msg},
		}, nil
	}

	return nil, fmt.Errorf("unsupported ALTER TABLE action: %s", body)
}

func (s *Coordinator) execShowPartitions(tableName string, start time.Time) (*QueryResult, error) {
	parts, err := s.engine.GetPartitions(tableName)
	if err != nil {
		return nil, err
	}

	rows := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		rows = append(rows, map[string]any{
			"partition": p.Name,
			"table":     tableName,
			"row_count": p.RowCount,
		})
	}

	return &QueryResult{
		Success:      true,
		Command:      "SHOW PARTITIONS",
		Columns:      []string{"partition", "table", "row_count"},
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execDescTable(tableName string, start time.Time) (*QueryResult, error) {
	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found", tableName)
	}

	cols := []string{"Field", "Type", "Null", "Key", "Default", "Extra"}
	rows := make([]map[string]any, 0, len(schema.Columns))

	for _, col := range schema.Columns {
		typeStr := col.Type.String()
		if col.Type == storage.TypeDecimal && col.Precision > 0 {
			typeStr = fmt.Sprintf("DECIMAL(%d,%d)", col.Precision, col.Scale)
		}

		nullStr := "NO"
		if col.Nullable {
			nullStr = "YES"
		}

		keyStr := ""
		if col.PrimaryKey {
			keyStr = "PRI"
		} else {
			for _, idx := range schema.Indexes {
				for _, c := range idx.Columns {
					if strings.EqualFold(c, col.Name) {
						if idx.Unique {
							keyStr = "UNI"
						} else if keyStr == "" {
							keyStr = "MUL"
						}
						break
					}
				}
				if keyStr == "UNI" {
					break
				}
			}
		}

		var defaultVal any = nil
		if col.Default != "" {
			defaultVal = col.Default
		}

		extraStr := ""
		if col.Identity {
			extraStr = "auto_increment"
		}

		row := map[string]any{
			"Field":   col.Name,
			"Type":    typeStr,
			"Null":    nullStr,
			"Key":     keyStr,
			"Default": defaultVal,
			"Extra":   extraStr,
		}
		rows = append(rows, row)
	}

	return &QueryResult{
		Success:      true,
		Command:      "DESCRIBE",
		Columns:      cols,
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execShowTables(cs *ClientSession, start time.Time) (*QueryResult, error) {
	tables, err := s.engine.ListTables()
	if err != nil {
		return nil, err
	}

	dbName := "lionsql"
	if cs != nil {
		cs.Mu.Lock()
		if cs.Database != "" {
			dbName = cs.Database
		}
		cs.Mu.Unlock()
	}

	colName := "Tables_in_" + dbName
	var tableNames []string
	for _, t := range tables {
		if !t.IsTemporary() {
			tableNames = append(tableNames, t.Name)
		}
	}
	sort.Strings(tableNames)

	rows := make([]map[string]any, 0, len(tableNames))
	for _, name := range tableNames {
		rows = append(rows, map[string]any{
			colName: name,
		})
	}

	return &QueryResult{
		Success:      true,
		Command:      "SHOW TABLES",
		Columns:      []string{colName},
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execShowDatabases(start time.Time) (*QueryResult, error) {
	dbs := []string{"lionsql", "master", "tempdb", "model", "msdb"}
	rows := make([]map[string]any, 0, len(dbs))
	for _, db := range dbs {
		rows = append(rows, map[string]any{
			"Database": db,
		})
	}

	return &QueryResult{
		Success:      true,
		Command:      "SHOW DATABASES",
		Columns:      []string{"Database"},
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execCreateStatistics(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	if !sqlutil.HasPrefixKeyword(cleaned, "CREATE STATISTICS") {
		return nil, fmt.Errorf("invalid CREATE STATISTICS syntax: expected CREATE STATISTICS <name> ON <table> (<cols>)")
	}
	onIdx := sqlutil.FindTopLevelKeyword(cleaned, "ON")
	if onIdx == -1 {
		return nil, fmt.Errorf("invalid CREATE STATISTICS syntax: expected CREATE STATISTICS <name> ON <table> (<cols>)")
	}

	beforeON := strings.TrimSpace(cleaned[:onIdx])
	statName := cleanIdentifier(strings.TrimSpace(beforeON[len("CREATE STATISTICS"):]))
	afterON := strings.TrimSpace(cleaned[onIdx+2:])
	openParen := sqlutil.FindTopLevelChar(afterON, '(')
	if openParen == -1 {
		return nil, fmt.Errorf("invalid CREATE STATISTICS syntax: expected CREATE STATISTICS <name> ON <table> (<cols>)")
	}
	closeParen := sqlutil.FindMatchingParen(afterON, openParen)
	if closeParen == -1 {
		return nil, fmt.Errorf("invalid CREATE STATISTICS syntax: expected CREATE STATISTICS <name> ON <table> (<cols>)")
	}

	tableName := cleanIdentifier(strings.TrimSpace(afterON[:openParen]))
	colsRaw := afterON[openParen+1 : closeParen]

	var cols []string
	for _, c := range strings.Split(colsRaw, ",") {
		cTrimmed := cleanIdentifier(strings.TrimSpace(c))
		if cTrimmed != "" {
			cols = append(cols, cTrimmed)
		}
	}

	if len(cols) < 2 {
		return nil, fmt.Errorf("CREATE STATISTICS requires at least 2 columns, got %d", len(cols))
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	colMap := make(map[string]bool, len(schema.Columns))
	for _, c := range schema.Columns {
		colMap[strings.ToLower(c.Name)] = true
	}
	for _, col := range cols {
		if !colMap[strings.ToLower(col)] {
			return nil, fmt.Errorf("column %q does not exist in table %q", col, tableName)
		}
	}

	if s.catalog != nil {
		statDef := &catalog.StatisticDef{
			Name:      statName,
			TableName: tableName,
			Columns:   cols,
			CreatedAt: time.Now(),
		}
		if err := s.catalog.CreateStatistics(statDef); err != nil {
			return nil, err
		}
	}

	_, _ = s.AnalyzeTable(tableName)

	msg := fmt.Sprintf("Statistics %q created successfully on table %q (%s).", statName, tableName, strings.Join(cols, ", "))
	return &QueryResult{
		Success:    true,
		Command:    "CREATE STATISTICS",
		Messages:   []string{msg},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execDropStatistics(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	if !sqlutil.HasPrefixKeyword(cleaned, "DROP STATISTICS") {
		return nil, fmt.Errorf("invalid DROP STATISTICS syntax: expected DROP STATISTICS <name> ON <table> or DROP STATISTICS <table>.<name>")
	}
	rest := strings.TrimSpace(cleaned[len("DROP STATISTICS"):])

	var statName, tableName string
	if onIdx := sqlutil.FindTopLevelKeyword(rest, "ON"); onIdx != -1 {
		statName = cleanIdentifier(strings.TrimSpace(rest[:onIdx]))
		tableName = cleanIdentifier(strings.TrimSpace(rest[onIdx+2:]))
	} else {
		tokens := sqlutil.SplitTopLevelTokens(rest)
		if len(tokens) == 0 {
			return nil, fmt.Errorf("invalid DROP STATISTICS syntax: expected DROP STATISTICS <name> ON <table> or DROP STATISTICS <table>.<name>")
		}
		target := cleanIdentifier(tokens[0])
		if idx := strings.Index(target, "."); idx != -1 {
			tableName = target[:idx]
			statName = target[idx+1:]
		} else {
			statName = target
		}
	}

	if s.catalog != nil {
		if err := s.catalog.DropStatistics(tableName, statName); err != nil {
			return nil, err
		}
	}

	return &QueryResult{
		Success:    true,
		Command:    "DROP STATISTICS",
		Messages:   []string{fmt.Sprintf("Statistics %q dropped successfully.", statName)},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}
