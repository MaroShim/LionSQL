package engine

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"lionsql/pkg/auth"
	"lionsql/pkg/catalog"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

func (s *Coordinator) execCreateTable(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	p := proc.NewParser(proc.NewLexer(cleaned))
	stmt, err := p.ParseCreateTable()
	if err != nil {
		return nil, err
	}

	tableName := stmt.Name
	if stmt.Temporary && !strings.HasPrefix(tableName, "#") && !strings.HasPrefix(tableName, "__temp_") {
		if cs != nil {
			tableName = cs.RegisterTempTable(tableName)
		} else {
			tableName = "__temp_default_" + tableName
		}
	}
	if len(stmt.Columns) == 0 {
		return nil, fmt.Errorf("no columns defined for table %s", tableName)
	}

	schema := &storage.TableSchema{
		Name:             tableName,
		Columns:          make([]storage.Column, 0, len(stmt.Columns)),
		ForeignKeys:      stmt.ForeignKeys,
		Indexes:          stmt.UniqueIndexes,
		CheckConstraints: stmt.CheckConstraints,
		Durability:       stmt.Durability,
		MemoryOptimized:  stmt.MemoryOptimized,
	}

	for _, c := range stmt.Columns {
		schema.Columns = append(schema.Columns, storage.Column{
			Name:         c.Name,
			Type:         c.Type,
			PrimaryKey:   c.PrimaryKey,
			Nullable:     c.Nullable,
			Identity:     c.Identity,
			IsRowVersion: c.IsRowVersion,
			Precision:    c.Precision,
			Scale:        c.Scale,
			Default:      c.Default,
			CheckExpr:    c.CheckExpr,
			ComputedExpr: c.ComputedExpr,
			IsComputed:   c.IsComputed,
		})
	}

	// Memory-Optimized Primary Key Invariant:
	// In SQL Server T-SQL (Hekaton), a memory-optimized table with DURABILITY = SCHEMA_AND_DATA must have a primary key (Msg 41321).
	// With DURABILITY = SCHEMA_ONLY, a primary key is not required (a synthetic _row_id will be provided).
	if schema.MemoryOptimized && schema.Durability != storage.DurabilitySchemaOnly && !schema.HasPrimaryKey() {
		return nil, fmt.Errorf("the memory optimized table %q with DURABILITY = SCHEMA_AND_DATA must have a primary key", tableName)
	}

	// Ensure table has a primary key: if none marked, inject synthetic hidden _row_id
	if !schema.HasPrimaryKey() {
		schema.EnsurePrimaryKey("_row_id")
	}

	if stmt.Partition != nil {
		colFound := false
		for _, c := range schema.Columns {
			if strings.EqualFold(c.Name, stmt.Partition.Column) {
				colFound = true
				break
			}
		}
		if !colFound {
			return nil, fmt.Errorf("partition column %q not found in table %q", stmt.Partition.Column, tableName)
		}
		schema.Partition = stmt.Partition
	}

	if err := s.engine.CreateTable(schema); err != nil {
		if strings.HasPrefix(tableName, "__temp_") && errors.Is(err, storage.ErrTableExists) {
			_ = s.engine.DropTable(tableName)
			if retryErr := s.engine.CreateTable(schema); retryErr == nil {
				err = nil
			}
		}
		if err != nil {
			if (stmt.IfNotExists || s.TolerantDDL()) && errors.Is(err, storage.ErrTableExists) {
				return &QueryResult{
					Success:    true,
					Command:    "CREATE TABLE",
					DurationUS: time.Since(start).Microseconds(),
					Messages:   []string{fmt.Sprintf("Table %q already exists (skipped)", tableName)},
				}, nil
			}
			return nil, err
		}
	}

	if strings.HasPrefix(tableName, "##") {
		creatorID := "default"
		if cs != nil && cs.ID != "" {
			creatorID = cs.ID
		}
		s.RegisterGlobalTemp(tableName, creatorID)
	} else if strings.HasPrefix(tableName, "#") || strings.HasPrefix(strings.ToLower(tableName), "__temp_") {
		if cs != nil {
			cs.RegisterTempTable(tableName)
		}
	}

	if cs != nil {
		cs.Mu.Lock()
		if cs.Tx != nil {
			if strings.HasPrefix(tableName, "#") || strings.HasPrefix(strings.ToLower(tableName), "__temp_") {
				cs.ddlUndoLog = append(cs.ddlUndoLog, DDLUndoOp{
					Kind:      DDLUndoDropTempTable,
					TableName: tableName,
				})
			} else {
				cs.ddlUndoLog = append(cs.ddlUndoLog, DDLUndoOp{
					Kind:      DDLUndoDropCreatedTable,
					TableName: schema.Name,
				})
			}
		}
		cs.Mu.Unlock()
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
	stmt, err := proc.NewParser(proc.NewLexer(cleaned)).ParseDropTable()
	if err != nil {
		return nil, err
	}

	tableName := stmt.Name
	if stmt.Cascade {
		if tables, err := s.engine.ListTables(); err == nil {
			for _, otherTbl := range tables {
				if strings.EqualFold(otherTbl.Name, tableName) {
					continue
				}
				_ = s.engine.AlterTable(otherTbl.Name, func(sch *storage.TableSchema) error {
					filtered := sch.ForeignKeys[:0]
					for _, fk := range sch.ForeignKeys {
						if !strings.EqualFold(fk.ParentTable, tableName) {
							filtered = append(filtered, fk)
						}
					}
					sch.ForeignKeys = filtered
					return nil
				})
			}
		}
	}

	inTx := false
	if cs != nil {
		cs.Mu.Lock()
		inTx = (cs.Tx != nil)
		cs.Mu.Unlock()
	}

	if inTx {
		if strings.HasPrefix(tableName, "#") && !strings.HasPrefix(tableName, "##") {
			phys, exists := cs.GetTempTablePhysical(tableName)
			if !exists {
				if stmt.IfExists || s.TolerantDDL() {
					return &QueryResult{
						Success:    true,
						Command:    "DROP TABLE",
						DurationUS: time.Since(start).Microseconds(),
						Messages:   []string{fmt.Sprintf("Table %q does not exist (skipped)", tableName)},
					}, nil
				}
				return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
			}
			tombstone := fmt.Sprintf("__temp_tombstone_%s_%d_%s", cs.ID, time.Now().UnixNano(), strings.TrimPrefix(tableName, "#"))
			if err := s.engine.RenameTable(phys, tombstone); err != nil {
				return nil, err
			}
			cs.UnregisterTempTable(tableName)
			cs.Mu.Lock()
			cs.ddlUndoLog = append(cs.ddlUndoLog, DDLUndoOp{
				Kind:          DDLUndoRestoreDroppedTempTable,
				TableName:     tableName,
				TombstoneName: tombstone,
			})
			cs.Mu.Unlock()
			s.InvalidatePKBypassCache()
			return &QueryResult{
				Success:    true,
				Command:    "DROP TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Table %q dropped successfully", tableName)},
			}, nil
		}

		// Permanent table drop inside active transaction:
		// Check table existence via s.getTable(cs, tableName)
		sch, err := s.getTable(cs, tableName)
		if err != nil {
			if (stmt.IfExists || s.TolerantDDL()) && errors.Is(err, storage.ErrTableNotFound) {
				return &QueryResult{
					Success:    true,
					Command:    "DROP TABLE",
					DurationUS: time.Since(start).Microseconds(),
					Messages:   []string{fmt.Sprintf("Table %q does not exist (skipped)", tableName)},
				}, nil
			}
			return nil, err
		}

		// 1. Mark dropped in ShadowCatalog so this transaction immediately perceives it as dropped
		if shadow := cs.GetShadowCatalog(); shadow != nil {
			shadow.MarkTableDropped(tableName)
		}

		// 2. Enqueue deferred physical drop on transaction commit (Zero Dirty DDL Read!)
		canonicalName := sch.Name
		if cs.TxMgr != nil && cs.TxMgr.Current() != nil {
			txCtx := cs.TxMgr.Current()
			txCtx.AddDeferredAction(func() error {
				_ = s.engine.DropTable(canonicalName)
				s.InvalidatePKBypassCache()
				return nil
			})
		} else {
			// Fallback legacy tombstone rename if TxMgr is inactive
			tombstone := fmt.Sprintf("__tombstone_%s_%d_%s", cs.ID, time.Now().UnixNano(), sch.Name)
			if err := s.engine.RenameTable(tableName, tombstone); err != nil {
				return nil, err
			}
			schClone := sch.Clone()
			cs.Mu.Lock()
			cs.ddlUndoLog = append(cs.ddlUndoLog, DDLUndoOp{
				Kind:          DDLUndoRestoreDroppedTable,
				TableName:     sch.Name,
				TombstoneName: tombstone,
				SchemaBackup:  &schClone,
			})
			cs.Mu.Unlock()
		}

		s.InvalidatePKBypassCache()
		s.SetTableLastIdentity(tableName, 0)

		// Clean up any triggers attached to this table
		if s.catalog != nil {
			for _, trg := range s.catalog.GetTableTriggers(tableName) {
				_ = s.catalog.DropTrigger(trg.Name)
				if s.executor != nil {
					s.executor.UnregisterTrigger(trg.Name)
				}
			}
		}

		return &QueryResult{
			Success:    true,
			Command:    "DROP TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Table %q dropped successfully", tableName)},
		}, nil
	}

	if err := s.engine.DropTable(tableName); err != nil {
		if (stmt.IfExists || s.TolerantDDL()) && errors.Is(err, storage.ErrTableNotFound) {
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

func (s *Coordinator) execTruncateTable(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	stmt, err := proc.NewParser(proc.NewLexer(cleaned)).ParseTruncate()
	if err != nil {
		return nil, err
	}
	tableNames := stmt.Tables
	isCascade := stmt.Cascade
	restartIdentity := stmt.RestartIdentity

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

			var pks []storage.Value
			for cur.Next() {
				if row, rerr := cur.Row(); rerr == nil && len(row.Values) > 0 {
					if pkVal, ok := tbl.ResolveRowPK(row); ok {
						pks = append(pks, pkVal)
						continue
					}
				}
				if rec, rcerr := cur.Record(); rcerr == nil && rec != nil {
					if pkVal, ok := tbl.ResolveRecordPK(rec); ok {
						pks = append(pks, pkVal)
						continue
					}
					if v, ok := rec.Get(storage.SyntheticPKRowIDName); ok {
						pks = append(pks, v)
						continue
					}
					if v, ok := rec.Get("id"); ok {
						pks = append(pks, v)
						continue
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

func (s *Coordinator) execCreateIndex(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

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
			if stmt.IfNotExists || (s.TolerantDDL() && strings.Contains(strings.ToLower(err.Error()), "already exists")) {
				return &QueryResult{
					Success:    true,
					Command:    "CREATE INDEX",
					Messages:   []string{fmt.Sprintf("Index %q already exists on table %q.", stmt.Name, stmt.Table)},
					DurationUS: time.Since(start).Microseconds(),
				}, nil
			}
			return nil, err
		}
		if cs != nil {
			cs.Mu.Lock()
			if cs.Tx != nil {
				cs.ddlUndoLog = append(cs.ddlUndoLog, DDLUndoOp{
					Kind:      DDLUndoDropCreatedIndex,
					TableName: stmt.Table,
					IndexName: stmt.Name,
				})
			}
			cs.Mu.Unlock()
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

func (s *Coordinator) execDropIndex(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	stmt, err := proc.NewParser(proc.NewLexer(cleaned)).ParseDropIndex()
	if err != nil {
		return nil, err
	}

	indexName := stmt.Name
	tableName := stmt.Table
	ifExists := stmt.IfExists

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
			if ifExists || s.TolerantDDL() {
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

	var idxBackup *storage.IndexSchema
	if tbl, err := s.engine.GetTable(tableName); err == nil && tbl != nil {
		if idx, _, found := tbl.FindIndex(indexName); found && idx != nil {
			idxBackup = &storage.IndexSchema{
				Name:        idx.Name,
				TableName:   tbl.Name,
				Columns:     append([]string(nil), idx.Columns...),
				IncludeCols: append([]string(nil), idx.IncludeCols...),
				Unique:      idx.Unique,
				Expression:  idx.Expression,
				ExprColumn:  idx.ExprColumn,
				ExprPath:    idx.ExprPath,
			}
		}
	}

	if err := s.engine.DropIndex(tableName, indexName); err != nil {
		if ifExists || s.TolerantDDL() {
			return &QueryResult{
				Success:    true,
				Command:    "DROP INDEX",
				Messages:   []string{fmt.Sprintf("Index %q does not exist on table %q (skipped)", indexName, tableName)},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}
		return nil, err
	}

	if cs != nil && idxBackup != nil {
		cs.Mu.Lock()
		if cs.Tx != nil {
			cs.ddlUndoLog = append(cs.ddlUndoLog, DDLUndoOp{
				Kind:        DDLUndoRestoreDroppedIndex,
				TableName:   tableName,
				IndexSchema: idxBackup,
			})
		}
		cs.Mu.Unlock()
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

func (s *Coordinator) execDropProcedure(query string, start time.Time) (*QueryResult, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(query), ";")
	var rest string
	if sqlutil.HasPrefixKeyword(trimmed, "DROP PROCEDURE") {
		rest = strings.TrimSpace(trimmed[len("DROP PROCEDURE"):])
	} else if sqlutil.HasPrefixKeyword(trimmed, "DROP PROC") {
		rest = strings.TrimSpace(trimmed[len("DROP PROC"):])
	} else {
		return nil, errors.New("invalid DROP PROCEDURE syntax")
	}

	ifExists := false
	if sqlutil.HasPrefixKeyword(rest, "IF EXISTS") {
		ifExists = true
		rest = strings.TrimSpace(rest[len("IF EXISTS"):])
	}

	// Support comma-separated multiple procedures e.g. DROP PROCEDURE p1, p2
	for _, p := range sqlutil.SplitTopLevel(rest, ',') {
		name := cleanIdentifier(strings.TrimSpace(p))
		if name == "" {
			continue
		}
		err := s.catalog.DropProcedure(name)
		if err != nil && !ifExists {
			return nil, err
		}
		if s.executor != nil && s.executor.Cache() != nil {
			s.executor.Cache().Invalidate(name)
		}
	}

	return &QueryResult{
		Success:    true,
		Command:    "DROP PROCEDURE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{"Procedure(s) dropped successfully"},
	}, nil
}

func (s *Coordinator) execDropFunction(query string, start time.Time) (*QueryResult, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(query), ";")
	if !sqlutil.HasPrefixKeyword(trimmed, "DROP FUNCTION") {
		return nil, errors.New("invalid DROP FUNCTION syntax")
	}
	rest := strings.TrimSpace(trimmed[len("DROP FUNCTION"):])

	ifExists := false
	if sqlutil.HasPrefixKeyword(rest, "IF EXISTS") {
		ifExists = true
		rest = strings.TrimSpace(rest[len("IF EXISTS"):])
	}

	// Support comma-separated multiple functions e.g. DROP FUNCTION f1(), f2() or f1, f2
	for _, p := range sqlutil.SplitTopLevel(rest, ',') {
		name := strings.TrimSpace(p)
		if idx := sqlutil.FindTopLevelChar(name, '('); idx != -1 {
			name = strings.TrimSpace(name[:idx])
		}
		name = cleanIdentifier(name)
		if name == "" {
			continue
		}
		err := s.catalog.DropFunction(name)
		if err != nil && !ifExists {
			return nil, err
		}
		if s.executor != nil {
			s.executor.UnregisterUDF(name)
			if s.executor.Cache() != nil {
				s.executor.Cache().Invalidate(name)
			}
		}
	}

	return &QueryResult{
		Success:    true,
		Command:    "DROP FUNCTION",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{"Function(s) dropped successfully"},
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
					bodyPart := sqlutil.UnwrapOuterParens(strings.TrimSpace(afterParams[returnIdx+len("RETURN"):]))
					if sqlutil.HasPrefixKeyword(bodyPart, "SELECT") {
						innerQuery := bodyPart
						var params []catalog.ParamDef
						if strings.TrimSpace(rawParams) != "" {
							for _, p := range sqlutil.SplitTopLevel(rawParams, ',') {
								pParts := sqlutil.SplitTopLevelTokens(strings.TrimSpace(p))
								if len(pParts) >= 2 {
									pName := pParts[0]
									pType := proc.ParseDataType(pParts[1])
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

	trimmed := strings.TrimSpace(query)
	if sqlutil.HasPrefixKeyword(trimmed, "CREATE OR ALTER TRIGGER") || sqlutil.HasPrefixKeyword(trimmed, "CREATE OR REPLACE TRIGGER") || sqlutil.HasPrefixKeyword(trimmed, "ALTER TRIGGER") {
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

	target := ""
	if stmt, err := proc.NewParser(proc.NewLexer(cleaned)).ParseReindex(); err == nil && stmt != nil {
		target = stmt.Target
	}

	if target == "" {
		return &QueryResult{
			Success:    true,
			Command:    "REINDEX",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"Reindexed all tables and indices successfully"},
		}, nil
	}

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

// execCreateView handles CREATE [OR REPLACE|OR ALTER] [TEMP|TEMPORARY] VIEW [IF NOT EXISTS] name AS <select> and ALTER VIEW name AS <select>.
func (s *Coordinator) execCreateView(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	asIdx := sqlutil.FindTopLevelKeyword(cleaned, "AS")
	if asIdx == -1 {
		return nil, fmt.Errorf("invalid CREATE VIEW syntax: missing AS keyword")
	}

	beforeAS := strings.TrimSpace(cleaned[:asIdx])
	selectQuery := sqlutil.UnwrapOuterParens(strings.TrimSpace(cleaned[asIdx+2:]))

	isAlter := sqlutil.HasPrefixKeyword(beforeAS, "ALTER VIEW")
	orReplace := sqlutil.ContainsKeyword(beforeAS, "OR REPLACE", false) || sqlutil.ContainsKeyword(beforeAS, "OR ALTER", false)
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

	cmdName := "CREATE VIEW"
	if isAlter {
		cmdName = "ALTER VIEW"
		if err := s.catalog.AlterView(view); err != nil {
			return nil, err
		}
	} else if orReplace {
		s.catalog.CreateOrReplaceView(view)
	} else {
		if err := s.catalog.CreateView(view); err != nil {
			if ifNotExists {
				return &QueryResult{
					Success:      true,
					Command:      cmdName,
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
		Command:      cmdName,
		RowsAffected: 0,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execDropView handles DROP VIEW [IF EXISTS] <name>.
func (s *Coordinator) execDropView(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	stmt, err := proc.NewParser(proc.NewLexer(cleaned)).ParseDropView()
	if err != nil {
		return nil, err
	}

	if err := s.catalog.DropView(stmt.Name); err != nil {
		if stmt.IfExists {
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
	stmt, err := proc.NewParser(proc.NewLexer(cleaned)).ParseAlterTable()
	if err != nil {
		return nil, err
	}

	tableName := stmt.Table
	if stmt.IfExists {
		if _, err := s.engine.GetTable(tableName); err != nil && errors.Is(err, storage.ErrTableNotFound) {
			return &QueryResult{
				Success:    true,
				Command:    "ALTER TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Table %q does not exist (skipped)", tableName)},
			}, nil
		}
	}

	// Fast-path grouping when all actions are ADD_COLUMN
	allAddCols := len(stmt.Actions) > 0
	for _, act := range stmt.Actions {
		if act.Type != proc.AlterAddColumn {
			allAddCols = false
			break
		}
	}
	if allAddCols {
		return s.execAlterTableAddColumns(tableName, stmt.Actions, start, cs)
	}

	var lastRes *QueryResult
	for _, act := range stmt.Actions {
		res, err := s.execSingleAlterAction(tableName, act, cleaned, start, cs)
		if err != nil {
			return nil, err
		}
		lastRes = res
	}
	return lastRes, nil
}

func (s *Coordinator) execAlterTableAddColumns(tableName string, actions []proc.AlterTableAction, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if err := s.checkTableAccess(cs, tableName, auth.PrivUpdate); err != nil {
		return nil, err
	}

	var addedCols []string
	for _, act := range actions {
		c := act.Column
		col := &storage.Column{
			Name:         c.Name,
			Type:         c.Type,
			Nullable:     c.Nullable,
			Identity:     c.Identity,
			IsRowVersion: c.IsRowVersion,
			Precision:    c.Precision,
			Scale:        c.Scale,
			Default:      c.Default,
			CheckExpr:    c.CheckExpr,
			ComputedExpr: c.ComputedExpr,
			IsComputed:   c.IsComputed,
		}

		if c.IsRowVersion {
			if tblSch, err := s.engine.GetTable(tableName); err == nil {
				if _, _, hasRV := tblSch.RowVersionColumn(); hasRV {
					return nil, fmt.Errorf("a table can only have one rowversion column")
				}
			}
		}

		if err := s.engine.AddColumn(tableName, col); err != nil {
			if (act.IfNotExists || s.TolerantDDL()) && strings.Contains(strings.ToLower(err.Error()), "already exists") {
				continue
			}
			return nil, err
		}
		if c.ForeignKey != nil {
			fk := *c.ForeignKey
			fk.ChildTable = tableName
			_ = s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
				sch.ForeignKeys = append(sch.ForeignKeys, fk)
				return nil
			})
		}
		addedCols = append(addedCols, c.Name)
		if cs != nil {
			cs.Mu.Lock()
			if cs.Tx != nil {
				cs.ddlUndoLog = append(cs.ddlUndoLog, DDLUndoOp{
					Kind:       DDLUndoDropAddedColumn,
					TableName:  tableName,
					ColumnName: c.Name,
				})
			}
			cs.Mu.Unlock()
		}
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

func (s *Coordinator) execSingleAlterAction(tableName string, act proc.AlterTableAction, rawQuery string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	switch act.Type {
	case proc.AlterAddColumn:
		return s.execAlterTableAddColumns(tableName, []proc.AlterTableAction{act}, start, cs)

	case proc.AlterDropPartition:
		if err := s.checkTableAccess(cs, tableName, auth.PrivDelete); err != nil {
			return nil, err
		}
		if err := s.engine.DropPartition(tableName, act.PartitionName); err != nil {
			return nil, err
		}
		s.InvalidatePKBypassCache()
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Partition %q dropped from table %q", act.PartitionName, tableName)},
		}, nil

	case proc.AlterAddUnique:
		if err := s.checkTableAccess(cs, tableName, auth.PrivUpdate); err != nil {
			return nil, err
		}
		cName := act.ConstraintName
		if cName == "" {
			cName = fmt.Sprintf("uq_%s_%s", tableName, strings.Join(act.ConstraintCols, "_"))
		}
		idxSchema := &storage.IndexSchema{
			Name:      cName,
			TableName: tableName,
			Columns:   act.ConstraintCols,
			Unique:    true,
		}
		if err := s.engine.CreateIndex(idxSchema); err != nil {
			return nil, err
		}
		s.InvalidatePKBypassCache()
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Unique constraint %q added to table %q", cName, tableName)},
		}, nil

	case proc.AlterAddForeignKey:
		fk := act.ForeignKey
		if fk == nil {
			return nil, fmt.Errorf("invalid FOREIGN KEY syntax in ALTER TABLE")
		}
		fk.ChildTable = tableName

		if !strings.EqualFold(fk.ParentTable, tableName) {
			parentTbl, err := s.engine.GetTable(fk.ParentTable)
			if err != nil || parentTbl == nil {
				return nil, fmt.Errorf("%w: referenced parent table %q does not exist", storage.ErrTableNotFound, fk.ParentTable)
			}
			fk.ParentTable = parentTbl.Name
			if len(fk.ParentColumns) == 0 {
				pkCol, err := parentTbl.PrimaryKeyColumn()
				if err != nil || pkCol == nil {
					return nil, fmt.Errorf("referenced parent table %q has no primary key", fk.ParentTable)
				}
				fk.ParentColumns = []string{pkCol.Name}
			}
		}

		if err := s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
			if strings.EqualFold(fk.ParentTable, sch.Name) && len(fk.ParentColumns) == 0 {
				pkCol, err := sch.PrimaryKeyColumn()
				if err != nil || pkCol == nil {
					return fmt.Errorf("referenced parent table %q has no primary key", fk.ParentTable)
				}
				fk.ParentColumns = []string{pkCol.Name}
			}
			for _, childCol := range fk.ChildColumns {
				if _, _, found := sch.FindColumn(childCol); !found {
					return fmt.Errorf("%w: %s", storage.ErrColumnNotFound, childCol)
				}
			}
			sch.ForeignKeys = append(sch.ForeignKeys, *fk)
			return nil
		}); err != nil {
			return nil, err
		}
		s.InvalidatePKBypassCache()
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Foreign key constraint %q added to table %q", fk.Name, tableName)},
		}, nil

	case proc.AlterAddCheck:
		if act.CheckExpr == "" {
			return nil, fmt.Errorf("invalid check constraint syntax: missing expression")
		}
		if err := s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
			sch.CheckConstraints = append(sch.CheckConstraints, act.CheckExpr)
			return nil
		}); err != nil {
			return nil, err
		}
		s.InvalidatePKBypassCache()
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Check constraint added to table %q", tableName)},
		}, nil

	case proc.AlterAddPrimaryKey:
		if err := s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
			for i := range sch.Columns {
				sch.Columns[i].PrimaryKey = false
			}
			for _, col := range act.ConstraintCols {
				for i := range sch.Columns {
					if strings.EqualFold(sch.Columns[i].Name, col) {
						sch.Columns[i].PrimaryKey = true
					}
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
		s.InvalidatePKBypassCache()
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Primary key %q added to table %q", act.ConstraintName, tableName)},
		}, nil

	case proc.AlterDropConstraint:
		cName := act.ConstraintName
		if strings.HasSuffix(strings.ToLower(cName), "_pkey") || sqlutil.ContainsKeyword(rawQuery, "PRIMARY KEY", false) {
			if err := s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
				for i := range sch.Columns {
					sch.Columns[i].PrimaryKey = false
				}
				return nil
			}); err != nil {
				return nil, err
			}
		}
		_ = s.engine.DropIndex(tableName, cName)
		s.InvalidatePKBypassCache()
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Constraint %q dropped from table %q", cName, tableName)},
		}, nil

	case proc.AlterColumnDropDefault:
		colName := act.ColumnName
		if err := s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
			found := false
			for i := range sch.Columns {
				if strings.EqualFold(sch.Columns[i].Name, colName) {
					sch.Columns[i].Default = ""
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: column %q does not exist in table %q", storage.ErrColumnNotFound, colName, tableName)
			}
			return nil
		}); err != nil {
			return nil, err
		}
		s.InvalidatePKBypassCache()
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Default dropped from column %q on table %q", colName, tableName)},
		}, nil

	case proc.AlterColumnSetDefault:
		colName := act.ColumnName
		if err := s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
			found := false
			for i := range sch.Columns {
				if strings.EqualFold(sch.Columns[i].Name, colName) {
					sch.Columns[i].Default = act.DefaultExpr
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: column %q does not exist in table %q", storage.ErrColumnNotFound, colName, tableName)
			}
			return nil
		}); err != nil {
			return nil, err
		}
		s.InvalidatePKBypassCache()
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Default set on column %q on table %q", colName, tableName)},
		}, nil

	case proc.AlterColumnType:
		colName := act.ColumnName
		newType := act.Column.Type
		if err := s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
			found := false
			for i := range sch.Columns {
				if strings.EqualFold(sch.Columns[i].Name, colName) {
					sch.Columns[i].Type = newType
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: column %q does not exist in table %q", storage.ErrColumnNotFound, colName, tableName)
			}
			return nil
		}); err != nil {
			return nil, err
		}
		s.InvalidatePKBypassCache()
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Column %q type changed on table %q to %s", colName, tableName, newType)},
		}, nil

	case proc.AlterRenameTable:
		newName := act.NewName
		if err := s.engine.RenameTable(tableName, newName); err != nil {
			return nil, err
		}
		if cs != nil {
			cs.Mu.Lock()
			if cs.Tx != nil {
				cs.ddlUndoLog = append(cs.ddlUndoLog, DDLUndoOp{
					Kind:    DDLUndoRenameTable,
					OldName: tableName,
					NewName: newName,
				})
			}
			cs.Mu.Unlock()
		}
		s.InvalidatePKBypassCache()
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Table %q renamed to %q", tableName, newName)},
		}, nil

	case proc.AlterRenameColumn:
		oldName := act.ColumnName
		newName := act.NewName
		if err := s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
			found := false
			for i := range sch.Columns {
				if strings.EqualFold(sch.Columns[i].Name, oldName) {
					sch.Columns[i].Name = newName
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: column %q does not exist in table %q", storage.ErrColumnNotFound, oldName, tableName)
			}
			return nil
		}); err != nil {
			return nil, err
		}
		s.InvalidatePKBypassCache()
		if cs != nil {
			cs.Mu.Lock()
			if cs.Tx != nil {
				cs.ddlUndoLog = append(cs.ddlUndoLog, DDLUndoOp{
					Kind:      DDLUndoRenameColumn,
					TableName: tableName,
					OldName:   oldName,
					NewName:   newName,
				})
			}
			cs.Mu.Unlock()
		}
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Column %q renamed to %q on table %q", oldName, newName, tableName)},
		}, nil

	case proc.AlterColumnSetNotNull, proc.AlterColumnDropNotNull:
		colName := act.ColumnName
		isDrop := act.Type == proc.AlterColumnDropNotNull
		action := "SET NOT NULL"
		if isDrop {
			action = "DROP NOT NULL"
		}
		if err := s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
			found := false
			for i := range sch.Columns {
				if strings.EqualFold(sch.Columns[i].Name, colName) {
					sch.Columns[i].Nullable = isDrop
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: column %q does not exist in table %q", storage.ErrColumnNotFound, colName, tableName)
			}
			return nil
		}); err != nil {
			return nil, err
		}
		s.InvalidatePKBypassCache()
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Column %q altered on table %q (%s)", colName, tableName, action)},
		}, nil

	case proc.AlterDropColumn:
		droppedCols := act.DropColumns
		dropIfExists := act.IfExists
		var droppedColBackups []*storage.Column
		if curSch, err := s.engine.GetTable(tableName); err == nil && curSch != nil {
			for _, dc := range droppedCols {
				if c, _, found := curSch.FindColumn(dc); found && c != nil {
					cCopy := *c
					droppedColBackups = append(droppedColBackups, &cCopy)
				}
			}
		}
		if err := s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
			if !dropIfExists {
				for _, dc := range droppedCols {
					if _, _, found := sch.FindColumn(dc); !found {
						return fmt.Errorf("%w: column %q does not exist in table %q", storage.ErrColumnNotFound, dc, tableName)
					}
				}
			}
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
		}); err != nil {
			return nil, err
		}
		s.InvalidatePKBypassCache()
		if cs != nil {
			cs.Mu.Lock()
			if cs.Tx != nil {
				for _, b := range droppedColBackups {
					cs.ddlUndoLog = append(cs.ddlUndoLog, DDLUndoOp{
						Kind:         DDLUndoRestoreDroppedColumn,
						TableName:    tableName,
						ColumnBackup: b,
					})
				}
			}
			cs.Mu.Unlock()
		}
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Columns %s dropped from table %q", strings.Join(droppedCols, ", "), tableName)},
		}, nil

	case proc.AlterSetStorageParams:
		return &QueryResult{
			Success:    true,
			Command:    "ALTER TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Table %q storage parameters updated (no-op)", tableName)},
		}, nil

	default:
		return nil, fmt.Errorf("unsupported ALTER TABLE action: %s", act.Type)
	}
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
	stmt, err := proc.NewParser(proc.NewLexer(cleaned)).ParseCreateStatistics()
	if err != nil {
		return nil, err
	}

	statName := stmt.Name
	tableName := stmt.Table
	cols := stmt.Columns

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
	stmt, err := proc.NewParser(proc.NewLexer(cleaned)).ParseDropStatistics()
	if err != nil {
		return nil, err
	}

	if s.catalog != nil {
		if err := s.catalog.DropStatistics(stmt.Table, stmt.Name); err != nil {
			return nil, err
		}
	}

	return &QueryResult{
		Success:    true,
		Command:    "DROP STATISTICS",
		Messages:   []string{fmt.Sprintf("Statistics %q dropped successfully.", stmt.Name)},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}
