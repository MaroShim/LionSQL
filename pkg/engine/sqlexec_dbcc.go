package engine

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"lionsql/pkg/catalog/virtual"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// --- T-SQL Database Console Commands (DBCC) ---

func (s *Coordinator) execDBCC(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	upper := strings.ToUpper(cleaned)

	var rest string
	if strings.HasPrefix(upper, "DBCC ") {
		rest = strings.TrimSpace(cleaned[5:])
	} else {
		rest = strings.TrimSpace(cleaned[4:])
	}
	upperRest := strings.ToUpper(rest)

	// DBCC PERSISTENCE or DBCC ENGINE
	if strings.HasPrefix(upperRest, "PERSISTENCE") || strings.HasPrefix(upperRest, "ENGINE") {
		engineType := s.engine.Type()
		persistence := "persistent"
		if me, ok := s.engine.(*memengine.Engine); ok {
			persistence = me.PersistenceSummary()
		}
		rows := []map[string]any{
			{"Property": "Engine", "Value": engineType},
			{"Property": "Persistence", "Value": persistence},
		}
		return &QueryResult{
			Success:      true,
			Command:      "DBCC",
			Columns:      []string{"Property", "Value"},
			Rows:         rows,
			RowsAffected: int64(len(rows)),
			DurationUS:   time.Since(start).Microseconds(),
			Messages:     []string{"DBCC execution completed. If DBCC printed error messages, contact your system administrator."},
		}, nil
	}

	// 1. DBCC USEROPTIONS
	if strings.HasPrefix(upperRest, "USEROPTIONS") {
		isoLevel := "read committed"
		if cs != nil {
			cs.Mu.Lock()
			if lvl, ok := cs.Vars["transaction_isolation"].(string); ok && lvl != "" {
				isoLevel = lvl
			}
			cs.Mu.Unlock()
		}

		persistence := "persistent"
		if me, ok := s.engine.(*memengine.Engine); ok {
			persistence = me.PersistenceSummary()
		}

		options := []struct {
			Option string
			Value  string
		}{
			{"textsize", "2147483647"},
			{"language", "us_english"},
			{"dateformat", "mdy"},
			{"datefirst", "7"},
			{"lock_timeout", "-1"},
			{"quoted_identifier", "SET"},
			{"arithabort", "SET"},
			{"ansi_null_dflt_on", "SET"},
			{"ansi_warnings", "SET"},
			{"ansi_padding", "SET"},
			{"ansi_nulls", "SET"},
			{"concat_null_yields_null", "SET"},
			{"isolation level", isoLevel},
			{"persistence", persistence},
		}

		rows := make([]map[string]any, len(options))
		for i, opt := range options {
			rows[i] = map[string]any{
				"Set Option": opt.Option,
				"Value":      opt.Value,
			}
		}

		return &QueryResult{
			Success:      true,
			Command:      "DBCC",
			Columns:      []string{"Set Option", "Value"},
			Rows:         rows,
			RowsAffected: int64(len(rows)),
			Messages:     []string{"DBCC execution completed. If DBCC printed error messages, contact your system administrator."},
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	// 2. DBCC CHECKTABLE ( 'table_name' )
	if strings.HasPrefix(upperRest, "CHECKTABLE") {
		sub := strings.TrimSpace(rest[10:])
		var rawTbl string
		if strings.HasPrefix(sub, "(") {
			closeParen := sqlutil.FindMatchingParen(sub, 0)
			if closeParen != -1 {
				rawTbl = strings.Trim(strings.TrimSpace(sub[1:closeParen]), "'\"")
			}
		} else {
			fields := strings.Fields(sub)
			if len(fields) > 0 {
				rawTbl = strings.Trim(fields[0], ";, '\"")
			}
		}
		if rawTbl == "" {
			return nil, fmt.Errorf("invalid DBCC CHECKTABLE syntax. Expected: DBCC CHECKTABLE ('table_name')")
		}
		targetTable := cleanIdentifier(rawTbl)

		schema, err := s.engine.GetTable(targetTable)
		if err != nil {
			return nil, fmt.Errorf("table %q not found: %w", targetTable, err)
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

		rowCount := int64(0)
		pkSet := make(map[storage.Value]struct{})
		allocErrors := 0
		consistErrors := 0

		if cur, err := tx.Scan(targetTable); err == nil {
			for cur.Next() {
				rec, err := cur.Record()
				if err != nil {
					consistErrors++
					continue
				}
				rowCount++
				if pkCol, err := schema.PrimaryKeyColumn(); err == nil && pkCol != nil {
					if val, ok := rec[pkCol.Name]; ok {
						if _, exists := pkSet[val]; exists {
							consistErrors++
						} else {
							pkSet[val] = struct{}{}
						}
					}
				}
			}
			cur.Close()
		}

		for _, idx := range schema.Indexes {
			if cur, err := tx.IndexScan(targetTable, idx.Name, nil, nil); err == nil {
				for cur.Next() {
				}
				cur.Close()
			}
		}

		messages := []string{
			fmt.Sprintf("DBCC results for %q.", targetTable),
			fmt.Sprintf("There are %d rows in 1 pages for object %q.", rowCount, targetTable),
			fmt.Sprintf("CHECKTABLE found %d allocation errors and %d consistency errors in table %q.", allocErrors, consistErrors, targetTable),
			"DBCC execution completed. If DBCC printed error messages, contact your system administrator.",
		}

		return &QueryResult{
			Success: true,
			Command: "DBCC",
			Columns: []string{"Table", "Rows", "AllocationErrors", "ConsistencyErrors", "Status"},
			Rows: []map[string]any{{
				"Table":             targetTable,
				"Rows":              rowCount,
				"AllocationErrors":  allocErrors,
				"ConsistencyErrors": consistErrors,
				"Status":            "OK",
			}},
			RowsAffected: 1,
			Messages:     messages,
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	// 3. DBCC CHECKDB [ ( 'database_name' ) ]
	if strings.HasPrefix(upperRest, "CHECKDB") {
		tables, err := s.engine.ListTables()
		if err != nil {
			return nil, fmt.Errorf("failed to list database objects: %w", err)
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

		totalAllocErrors := 0
		totalConsistErrors := 0
		dbName := "master"
		if cs != nil && cs.Database != "" {
			dbName = cs.Database
		}
		messages := []string{fmt.Sprintf("DBCC results for %q.", dbName)}

		for _, tbl := range tables {
			rowCount := int64(0)
			pkSet := make(map[storage.Value]struct{})
			if cur, err := tx.Scan(tbl.Name); err == nil {
				for cur.Next() {
					rec, err := cur.Record()
					if err != nil {
						totalConsistErrors++
						continue
					}
					rowCount++
					if pkCol, err := tbl.PrimaryKeyColumn(); err == nil && pkCol != nil {
						if val, ok := rec[pkCol.Name]; ok {
							if _, exists := pkSet[val]; exists {
								totalConsistErrors++
							} else {
								pkSet[val] = struct{}{}
							}
						}
					}
				}
				cur.Close()
			}
			messages = append(messages, fmt.Sprintf("There are %d rows in 1 pages for object %q.", rowCount, tbl.Name))
		}

		messages = append(messages,
			fmt.Sprintf("CHECKDB found %d allocation errors and %d consistency errors in database %q.", totalAllocErrors, totalConsistErrors, dbName),
			"DBCC execution completed. If DBCC printed error messages, contact your system administrator.",
		)
		return &QueryResult{
			Success: true,
			Command: "DBCC",
			Columns: []string{"Database", "TablesChecked", "AllocationErrors", "ConsistencyErrors", "Status"},
			Rows: []map[string]any{{
				"Database":          dbName,
				"TablesChecked":     len(tables),
				"AllocationErrors":  totalAllocErrors,
				"ConsistencyErrors": totalConsistErrors,
				"Status":            "ONLINE",
			}},
			RowsAffected: 1,
			Messages:     messages,
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	// 4. DBCC SHRINKDATABASE / DBCC SHRINKFILE
	if strings.HasPrefix(upperRest, "SHRINKDATABASE") || strings.HasPrefix(upperRest, "SHRINKFILE") {
		return &QueryResult{
			Success: true,
			Command: "DBCC",
			Columns: []string{"DbId", "FileId", "CurrentSize", "MinimumSize", "UsedPages", "EstimatedPages"},
			Rows: []map[string]any{{
				"DbId":           int64(1),
				"FileId":         int64(1),
				"CurrentSize":    int64(1024),
				"MinimumSize":    int64(1024),
				"UsedPages":      int64(512),
				"EstimatedPages": int64(512),
			}},
			RowsAffected: 1,
			Messages:     []string{"DBCC execution completed. If DBCC printed error messages, contact your system administrator."},
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	return nil, fmt.Errorf("unsupported DBCC command: %q", rest)
}

// --- Virtual System Catalog Views (sys.*, pg_catalog.*, and information_schema.*) ---

// buildVirtualCatalogResult applies WHERE filtering, ORDER BY sorting, LIMIT
// trimming, and column projection to the raw rows produced by a virtual catalog
// view handler and returns a QueryResult ready for wire serialization.
func buildVirtualCatalogResult(columns []string, allRows []map[string]any, projStr, whereClause, orderByClause string, limitCount int, start time.Time) (*QueryResult, error) {
	// Filter rows by whereClause
	var filteredRows []map[string]any
	for _, r := range allRows {
		if whereClause == "" || evaluateWhereConditionMap(r, whereClause) {
			filteredRows = append(filteredRows, r)
		}
	}

	type virtualProjCol struct {
		outName string
		rawCol  string
		expr    proc.Expr
	}

	// Projection
	var selectedCols []string
	var projCols []virtualProjCol
	trimmedProj := strings.TrimSpace(projStr)
	if trimmedProj == "*" || trimmedProj == "" || strings.HasSuffix(trimmedProj, ".*") {
		selectedCols = columns
		for _, col := range columns {
			projCols = append(projCols, virtualProjCol{outName: col, rawCol: col})
		}
	} else {
		for _, c := range sqlutil.SplitTopLevel(projStr, ',') {
			cTrimmed := strings.TrimSpace(c)
			if cTrimmed == "" {
				continue
			}
			if cTrimmed == "*" || strings.HasSuffix(cTrimmed, ".*") {
				for _, col := range columns {
					selectedCols = append(selectedCols, col)
					projCols = append(projCols, virtualProjCol{outName: col, rawCol: col})
				}
				continue
			}
			alias := ""
			colExpr := cTrimmed
			if asIdx := sqlutil.FindTopLevelKeyword(cTrimmed, "AS"); asIdx != -1 {
				colExpr = strings.TrimSpace(cTrimmed[:asIdx])
				alias = cleanIdentifier(strings.TrimSpace(cTrimmed[asIdx+2:]))
			} else {
				fields := strings.Fields(cTrimmed)
				if len(fields) == 2 && !strings.Contains(fields[1], "(") {
					colExpr = fields[0]
					alias = cleanIdentifier(fields[1])
				}
			}
			rawCol := strings.ToLower(cleanIdentifier(colExpr))
			if strings.Contains(rawCol, ".") && !strings.Contains(rawCol, "(") {
				rawCol = rawCol[strings.LastIndex(rawCol, ".")+1:]
			}
			outName := alias
			if outName == "" {
				outName = rawCol
			}
			selectedCols = append(selectedCols, outName)

			var astExpr proc.Expr
			if strings.ContainsAny(colExpr, "()+*/-|^") || sqlutil.HasPrefixKeyword(colExpr, "CASE") || sqlutil.HasPrefixKeyword(colExpr, "CAST") {
				p := proc.NewParser(proc.NewLexer(colExpr))
				if e := p.ParseExpr(); e != nil && len(p.Errors()) == 0 {
					astExpr = e
				}
			}
			projCols = append(projCols, virtualProjCol{
				outName: outName,
				rawCol:  rawCol,
				expr:    astExpr,
			})
		}
	}

	type catalogRowPair struct {
		src   map[string]any
		final map[string]any
	}
	pairs := make([]catalogRowPair, len(filteredRows))
	for i, fr := range filteredRows {
		row := make(map[string]any, len(projCols))
		for _, pc := range projCols {
			col := pc.outName
			src := pc.rawCol
			if src == "" {
				src = col
			}
			if v, ok := fr[src]; ok {
				row[col] = v
			} else if v, ok := fr[strings.ToLower(col)]; ok {
				row[col] = v
			} else if pc.expr != nil {
				if v, err := proc.EvalRowExpr(pc.expr, fr, nil); err == nil && v != nil {
					row[col] = v
				} else {
					row[col] = nil
				}
			} else {
				row[col] = nil
			}
		}
		pairs[i] = catalogRowPair{src: fr, final: row}
	}

	// Order by
	if orderByClause != "" && len(pairs) > 0 {
		orderItems := sqlutil.ParseOrderBy(orderByClause)
		if len(orderItems) > 0 {
			sort.Slice(pairs, func(i, j int) bool {
				for _, item := range orderItems {
					orderCol := cleanIdentifier(item.Column)
					v1 := getRowValueCaseInsensitive(pairs[i].final, orderCol)
					v2 := getRowValueCaseInsensitive(pairs[j].final, orderCol)
					if v1 == nil && v2 == nil {
						v1 = getRowValueCaseInsensitive(pairs[i].src, orderCol)
						v2 = getRowValueCaseInsensitive(pairs[j].src, orderCol)
					}
					if v1 == nil && v2 == nil && (strings.ContainsAny(item.Column, "()+*/-|^") || sqlutil.HasPrefixKeyword(item.Column, "CAST") || sqlutil.HasPrefixKeyword(item.Column, "CASE")) {
						p := proc.NewParser(proc.NewLexer(item.Column))
						if e := p.ParseExpr(); e != nil && len(p.Errors()) == 0 {
							v1, _ = proc.EvalRowExpr(e, pairs[i].src, nil)
							v2, _ = proc.EvalRowExpr(e, pairs[j].src, nil)
						}
					}
					cmp := sqlutil.CompareAny(v1, v2)
					if cmp == 0 {
						continue
					}
					if item.Desc {
						return cmp > 0
					}
					return cmp < 0
				}
				return false
			})
		}
	}

	finalRows := make([]map[string]any, len(pairs))
	for i, pair := range pairs {
		finalRows[i] = pair.final
	}

	// Limit
	if limitCount >= 0 && len(finalRows) > limitCount {
		finalRows = finalRows[:limitCount]
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      selectedCols,
		Rows:         finalRows,
		RowsAffected: int64(len(finalRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func getRowValueCaseInsensitive(row map[string]any, key string) any {
	if row == nil || key == "" {
		return nil
	}
	if v, ok := row[key]; ok {
		return v
	}
	lower := strings.ToLower(key)
	if v, ok := row[lower]; ok {
		return v
	}
	for k, v := range row {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return nil
}

func (s *Coordinator) isVirtualCatalogTable(tableName string) bool {
	return virtual.IsVirtualTable(tableName)
}

func (s *Coordinator) execVirtualCatalogSelect(tableName, projStr, whereClause, orderByClause string, limitCount int, start time.Time, cs *ClientSession) (*QueryResult, error) {
	norm := strings.ToLower(cleanIdentifier(tableName))
	norm = strings.TrimPrefix(norm, "dbo.")

	currDb := "master"
	if cs != nil {
		cs.Mu.Lock()
		if cs.Database != "" {
			currDb = cs.Database
		}
		cs.Mu.Unlock()
	}

	// Delegate pg_catalog.* and information_schema.* to the dedicated handler.
	if virtual.IsPgCatalogTable(norm) {
		res, err := virtual.ExecPgCatalogSelect(s, norm, projStr, whereClause, orderByClause, limitCount)
		if err != nil {
			return nil, err
		}
		return &QueryResult{
			Success:      true,
			Command:      "SELECT",
			Columns:      res.Columns,
			Rows:         res.Rows,
			RowsAffected: int64(len(res.Rows)),
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	var columns []string
	var allRows []map[string]any
	tables, _ := s.engine.ListTables()

	defaultDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	switch norm {
	case "sys.tables", "systables", "sys_tables":
		columns = []string{
			"table_name", "name", "object_id", "schema_id", "table_type", "table_schema",
			"column_count", "create_date", "modify_date", "principal_id", "is_ms_shipped",
			"is_replicated", "max_column_id_used", "lob_data_space_id", "filestream_data_space_id",
			"is_memory_optimized", "durability", "durability_desc", "temporal_type", "temporal_type_desc",
			"is_filetable", "is_external", "is_node", "is_edge", "is_tracked_by_cdc", "lock_on_bulk_load",
			"uses_ansi_nulls", "is_published", "is_auto_cleanup_types",
		}
		for i, t := range tables {
			allRows = append(allRows, map[string]any{
				"table_name":               t.Name,
				"name":                     t.Name,
				"object_id":                1000 + i,
				"schema_id":                1,
				"table_type":               "BASE TABLE",
				"table_schema":             "dbo",
				"column_count":             len(t.Columns),
				"create_date":              defaultDate,
				"modify_date":              defaultDate,
				"principal_id":             nil,
				"is_ms_shipped":            false,
				"is_replicated":            false,
				"max_column_id_used":       len(t.Columns),
				"lob_data_space_id":        0,
				"filestream_data_space_id": 0,
				"is_memory_optimized":      false,
				"durability":               0,
				"durability_desc":          "SCHEMA_AND_DATA",
				"temporal_type":            0,
				"temporal_type_desc":       "NON_TEMPORAL_TABLE",
				"is_filetable":             false,
				"is_external":              false,
				"is_node":                  false,
				"is_edge":                  false,
				"is_tracked_by_cdc":        false,
				"lock_on_bulk_load":        false,
				"uses_ansi_nulls":          true,
				"is_published":             false,
				"is_auto_cleanup_types":    false,
			})
		}

	case "sys.columns", "syscolumns", "sys_columns", "sys.all_columns", "sysallcolumns", "sys_all_columns":
		columns = []string{"table_name", "column_name", "name", "object_id", "column_id", "system_type_id", "user_type_id", "data_type", "is_nullable", "is_identity", "is_primary_key", "ordinal_position", "max_length", "precision", "scale"}
		for tIdx, t := range tables {
			for i, c := range t.Columns {
				nullableStr := "YES"
				if !c.Nullable {
					nullableStr = "NO"
				}
				allRows = append(allRows, map[string]any{
					"table_name":       t.Name,
					"column_name":      c.Name,
					"name":             c.Name,
					"object_id":        1000 + tIdx,
					"column_id":        i + 1,
					"system_type_id":   56,
					"user_type_id":     56,
					"data_type":        c.Type.String(),
					"is_nullable":      nullableStr,
					"is_identity":      false,
					"is_primary_key":   c.PrimaryKey,
					"ordinal_position": i + 1,
					"max_length":       255,
					"precision":        10,
					"scale":            0,
				})
			}
		}

	case "sys.indexes", "sysindexes", "sys_indexes":
		columns = []string{
			"object_id", "name", "index_name", "index_id", "type", "type_desc",
			"is_unique", "data_space_id", "ignore_dup_key", "is_primary_key",
			"is_unique_constraint", "fill_factor", "is_padded", "is_disabled",
			"is_hypothetical", "allow_row_locks", "allow_page_locks", "has_filter",
			"filter_definition", "auto_created", "table_name", "columns",
		}
		for i, t := range tables {
			objID := 1000 + i
			hasPK := false
			if pkCol, _ := t.PrimaryKeyColumn(); pkCol != nil {
				hasPK = true
				allRows = append(allRows, map[string]any{
					"object_id":            objID,
					"name":                 "PK_" + t.Name,
					"index_name":           "PK_" + t.Name,
					"index_id":             1,
					"type":                 1,
					"type_desc":            "CLUSTERED",
					"is_unique":            true,
					"data_space_id":        1,
					"ignore_dup_key":       false,
					"is_primary_key":       true,
					"is_unique_constraint": true,
					"fill_factor":          0,
					"is_padded":            false,
					"is_disabled":          false,
					"is_hypothetical":      false,
					"allow_row_locks":      true,
					"allow_page_locks":     true,
					"has_filter":           false,
					"filter_definition":    nil,
					"auto_created":         false,
					"table_name":           t.Name,
					"columns":              pkCol.Name,
				})
			}
			if !hasPK {
				allRows = append(allRows, map[string]any{
					"object_id":            objID,
					"name":                 nil,
					"index_name":           nil,
					"index_id":             0,
					"type":                 0,
					"type_desc":            "HEAP",
					"is_unique":            false,
					"data_space_id":        1,
					"ignore_dup_key":       false,
					"is_primary_key":       false,
					"is_unique_constraint": false,
					"fill_factor":          0,
					"is_padded":            false,
					"is_disabled":          false,
					"is_hypothetical":      false,
					"allow_row_locks":      true,
					"allow_page_locks":     true,
					"has_filter":           false,
					"filter_definition":    nil,
					"auto_created":         false,
					"table_name":           t.Name,
					"columns":              "",
				})
			}
			for idxSeq, idx := range t.Indexes {
				allRows = append(allRows, map[string]any{
					"object_id":            objID,
					"name":                 idx.Name,
					"index_name":           idx.Name,
					"index_id":             2 + idxSeq,
					"type":                 2,
					"type_desc":            "NONCLUSTERED",
					"is_unique":            idx.Unique,
					"data_space_id":        1,
					"ignore_dup_key":       false,
					"is_primary_key":       false,
					"is_unique_constraint": false,
					"fill_factor":          0,
					"is_padded":            false,
					"is_disabled":          false,
					"is_hypothetical":      false,
					"allow_row_locks":      true,
					"allow_page_locks":     true,
					"has_filter":           false,
					"filter_definition":    nil,
					"auto_created":         false,
					"table_name":           t.Name,
					"columns":              strings.Join(idx.Columns, ", "),
				})
			}
		}

	case "sys.databases", "sysdatabases", "sys_databases":
		columns = []string{
			"name", "schema_name", "database_id", "source_database_id", "owner_sid",
			"create_date", "compatibility_level", "collation_name", "user_access",
			"user_access_desc", "is_read_only", "is_auto_close_on", "is_auto_shrink_on",
			"state", "state_desc", "is_in_standby", "snapshot_isolation_state",
			"snapshot_isolation_state_desc", "is_read_committed_snapshot_on",
			"recovery_model", "recovery_model_desc", "page_verify_option",
			"page_verify_option_desc", "is_auto_create_stats_on", "is_auto_update_stats_on",
			"containment", "containment_desc", "is_fulltext_enabled", "is_distributor",
			"is_cdc_enabled", "is_encrypted", "catalog_collation_type", "catalog_collation_type_desc",
		}
		dbList := []struct {
			name string
			id   int
		}{
			{"master", 1},
			{"tempdb", 2},
			{"model", 3},
			{"msdb", 4},
			{"lionsql", 5},
		}
		for _, db := range dbList {
			allRows = append(allRows,
				map[string]any{
					"name":                          db.name,
					"schema_name":                   "dbo",
					"database_id":                   db.id,
					"source_database_id":            nil,
					"owner_sid":                     nil,
					"create_date":                   defaultDate,
					"compatibility_level":           150,
					"collation_name":                "SQL_Latin1_General_CP1_CI_AS",
					"user_access":                   0,
					"user_access_desc":              "MULTI_USER",
					"is_read_only":                  false,
					"is_auto_close_on":              false,
					"is_auto_shrink_on":             false,
					"state":                         0,
					"state_desc":                    "ONLINE",
					"is_in_standby":                 false,
					"snapshot_isolation_state":      1,
					"snapshot_isolation_state_desc": "ON",
					"is_read_committed_snapshot_on": true,
					"recovery_model":                1,
					"recovery_model_desc":           "FULL",
					"page_verify_option":            2,
					"page_verify_option_desc":       "CHECKSUM",
					"is_auto_create_stats_on":       true,
					"is_auto_update_stats_on":       true,
					"containment":                   0,
					"containment_desc":              "NONE",
					"is_fulltext_enabled":           false,
					"is_distributor":                false,
					"is_cdc_enabled":                false,
					"is_encrypted":                  false,
					"catalog_collation_type":        int64(0),
					"catalog_collation_type_desc":   "DATABASE_DEFAULT",
				},
			)
		}

	case "sys.resource_governor_configuration", "sysresourcegovernorconfiguration", "sys_resource_governor_configuration":
		columns = []string{
			"classifier_function_id", "is_enabled", "max_outstanding_io_per_volume",
		}
		allRows = []map[string]any{
			{
				"classifier_function_id":        int32(0),
				"is_enabled":                    false,
				"max_outstanding_io_per_volume": int32(0),
			},
		}

	case "sys.procedures", "sysprocedures", "sys_procedures":
		columns = []string{
			"name", "object_id", "principal_id", "schema_id", "parent_object_id",
			"type", "type_desc", "create_date", "modify_date",
			"is_ms_shipped", "is_published", "is_schema_published",
			"is_auto_executed", "is_execution_replicated",
			"routine_name", "routine_type", "routine_schema", "created_date",
		}
		if s.catalog != nil {
			for pIdx, p := range s.catalog.ListProcedures() {
				createdDate := p.CreatedAt
				if createdDate.IsZero() {
					createdDate = defaultDate
				}
				updatedDate := p.UpdatedAt
				if updatedDate.IsZero() {
					updatedDate = createdDate
				}
				allRows = append(allRows, map[string]any{
					"name":                    p.Name,
					"object_id":               int32(3000 + pIdx),
					"principal_id":            nil,
					"schema_id":               int32(1),
					"parent_object_id":        int32(0),
					"type":                    "P ",
					"type_desc":               "SQL_STORED_PROCEDURE",
					"create_date":             createdDate,
					"modify_date":             updatedDate,
					"is_ms_shipped":           false,
					"is_published":            false,
					"is_schema_published":     false,
					"is_auto_executed":        false,
					"is_execution_replicated": false,
					"routine_name":            p.Name,
					"routine_type":            "PROCEDURE",
					"routine_schema":          "dbo",
					"created_date":            createdDate,
				})
			}
		}

	case "sys.parameters", "sysparameters", "sys_parameters", "sys.all_parameters", "sysallparameters", "sys_all_parameters":
		columns = []string{
			"object_id", "name", "parameter_id", "system_type_id", "user_type_id",
			"max_length", "precision", "scale", "is_output", "is_cursor_ref",
			"has_default_value", "is_xml_document", "default_value",
		}
		if s.catalog != nil {
			for pIdx, p := range s.catalog.ListProcedures() {
				objID := int32(3000 + pIdx)
				for paramIdx, param := range p.Params {
					paramID := int32(paramIdx + 1)
					allRows = append(allRows, map[string]any{
						"object_id":         objID,
						"name":              param.Name,
						"parameter_id":      paramID,
						"system_type_id":    int32(56),
						"user_type_id":      int32(56),
						"max_length":        int16(8),
						"precision":         uint8(10),
						"scale":             uint8(0),
						"is_output":         false,
						"is_cursor_ref":     false,
						"has_default_value": false,
						"is_xml_document":   false,
						"default_value":     nil,
					})
				}
			}
		}

	case "sys.table_stats", "systablestats", "sys_table_stats":
		columns = []string{"table_name", "row_count", "page_count", "last_analyzed"}
		if s.catalog != nil {
			for _, st := range s.catalog.ListTableStats() {
				allRows = append(allRows, map[string]any{
					"table_name":    st.TableName,
					"row_count":     st.RowCount,
					"page_count":    st.PageCount,
					"last_analyzed": st.AnalyzedAt.Format(time.RFC3339),
				})
			}
		}

	case "sys.column_stats", "syscolumnstats", "sys_column_stats":
		columns = []string{"table_name", "column_name", "null_count", "distinct_count", "min_value", "max_value", "histogram_buckets"}
		if s.catalog != nil {
			for _, st := range s.catalog.ListTableStats() {
				for _, col := range st.Columns {
					minStr := ""
					if !col.MinVal.IsNull {
						minStr = col.MinVal.String()
					}
					maxStr := ""
					if !col.MaxVal.IsNull {
						maxStr = col.MaxVal.String()
					}
					allRows = append(allRows, map[string]any{
						"table_name":        st.TableName,
						"column_name":       col.Name,
						"null_count":        col.NullCount,
						"distinct_count":    col.DistinctCount,
						"min_value":         minStr,
						"max_value":         maxStr,
						"histogram_buckets": len(col.Histogram),
					})
				}
			}
		}

	case "sys.schemas", "sysschemas", "sys_schemas":
		columns = []string{"name", "schema_id", "principal_id"}
		allRows = append(allRows,
			map[string]any{"name": "dbo", "schema_id": 1, "principal_id": 1},
			map[string]any{"name": "guest", "schema_id": 2, "principal_id": 2},
			map[string]any{"name": "sys", "schema_id": 3, "principal_id": 3},
			map[string]any{"name": "information_schema", "schema_id": 4, "principal_id": 4},
		)

	case "sys.objects", "sysobjects", "sys_objects", "sys.all_objects", "sysallobjects", "sys_all_objects":
		columns = []string{
			"name", "object_id", "id", "uid", "principal_id", "schema_id", "parent_object_id",
			"type", "xtype", "type_desc", "create_date", "modify_date",
			"is_ms_shipped", "is_published", "is_schema_published",
		}
		for i, t := range tables {
			allRows = append(allRows, map[string]any{
				"name":                t.Name,
				"object_id":           1000 + i,
				"id":                  1000 + i,
				"uid":                 1,
				"principal_id":        nil,
				"schema_id":           1,
				"parent_object_id":    0,
				"type":                "U ",
				"xtype":               "U ",
				"type_desc":           "USER_TABLE",
				"create_date":         defaultDate,
				"modify_date":         defaultDate,
				"is_ms_shipped":       false,
				"is_published":        false,
				"is_schema_published": false,
			})
		}
		if s.catalog != nil {
			for vIdx, v := range s.catalog.ListViews() {
				allRows = append(allRows, map[string]any{
					"name":                v.Name,
					"object_id":           2000 + vIdx,
					"id":                  2000 + vIdx,
					"uid":                 1,
					"principal_id":        nil,
					"schema_id":           1,
					"parent_object_id":    0,
					"type":                "V ",
					"xtype":               "V ",
					"type_desc":           "VIEW",
					"create_date":         defaultDate,
					"modify_date":         defaultDate,
					"is_ms_shipped":       false,
					"is_published":        false,
					"is_schema_published": false,
				})
			}
			for pIdx, p := range s.catalog.ListProcedures() {
				allRows = append(allRows, map[string]any{
					"name":                p.Name,
					"object_id":           3000 + pIdx,
					"id":                  3000 + pIdx,
					"uid":                 1,
					"principal_id":        nil,
					"schema_id":           1,
					"parent_object_id":    0,
					"type":                "P ",
					"xtype":               "P ",
					"type_desc":           "SQL_STORED_PROCEDURE",
					"create_date":         p.CreatedAt,
					"modify_date":         p.CreatedAt,
					"is_ms_shipped":       false,
					"is_published":        false,
					"is_schema_published": false,
				})
			}
		}

	case "sys.views", "sysviews", "sys_views", "sys.all_views", "sysallviews", "sys_all_views":
		columns = []string{"name", "object_id", "schema_id", "create_date", "modify_date"}
		if s.catalog != nil {
			for vIdx, v := range s.catalog.ListViews() {
				allRows = append(allRows, map[string]any{
					"name":        v.Name,
					"object_id":   2000 + vIdx,
					"schema_id":   1,
					"create_date": defaultDate,
					"modify_date": defaultDate,
				})
			}
		}

	case "sys.types", "systypes", "sys_types":
		columns = []string{"name", "system_type_id", "user_type_id", "schema_id", "max_length", "precision", "scale", "is_nullable"}
		allRows = append(allRows,
			map[string]any{"name": "int", "system_type_id": 56, "user_type_id": 56, "schema_id": 4, "max_length": 4, "precision": 10, "scale": 0, "is_nullable": true},
			map[string]any{"name": "varchar", "system_type_id": 167, "user_type_id": 167, "schema_id": 4, "max_length": 8000, "precision": 0, "scale": 0, "is_nullable": true},
			map[string]any{"name": "float", "system_type_id": 62, "user_type_id": 62, "schema_id": 4, "max_length": 8, "precision": 53, "scale": 0, "is_nullable": true},
			map[string]any{"name": "bit", "system_type_id": 104, "user_type_id": 104, "schema_id": 4, "max_length": 1, "precision": 1, "scale": 0, "is_nullable": true},
			map[string]any{"name": "datetime", "system_type_id": 61, "user_type_id": 61, "schema_id": 4, "max_length": 8, "precision": 23, "scale": 3, "is_nullable": true},
		)
	case "sys.partitions", "system.partitions", "syspartitions", "sys_partitions":
		columns = []string{"table_name", "partition", "row_count"}
		for _, t := range tables {
			if t.IsPartitioned() {
				if parts, err := s.engine.GetPartitions(t.Name); err == nil {
					for _, p := range parts {
						allRows = append(allRows, map[string]any{
							"table_name": t.Name,
							"partition":  p.Name,
							"row_count":  p.RowCount,
						})
					}
				}
			}
		}

	case "sys.computed_columns", "syscomputedcolumns", "sys_computed_columns":
		columns = []string{"object_id", "name", "column_id", "definition", "is_persisted"}
		allRows = []map[string]any{}

	case "sys.triggers", "systriggers", "sys_triggers":
		columns = []string{"name", "object_id", "parent_id", "type", "type_desc", "create_date"}
		allRows = []map[string]any{}

	case "sys.foreign_keys", "sysforeignkeys", "sys_foreign_keys":
		columns = []string{"name", "object_id", "parent_object_id", "referenced_object_id"}
		for tIdx, t := range tables {
			for fkIdx, fk := range t.ForeignKeys {
				allRows = append(allRows, map[string]any{
					"name":                 fk.Name,
					"object_id":            3000 + tIdx*100 + fkIdx,
					"parent_object_id":     1000 + tIdx,
					"referenced_object_id": 1000,
				})
			}
		}

	case "sys.foreign_key_columns", "sysforeignkeycolumns", "sys_foreign_key_columns":
		columns = []string{"constraint_object_id", "parent_object_id", "parent_column_id", "referenced_object_id", "referenced_column_id"}
		allRows = []map[string]any{}

	case "sys.configurations", "sysconfigurations", "sys_configurations":
		columns = []string{
			"configuration_id", "name", "value", "minimum", "maximum",
			"value_in_use", "description", "is_dynamic", "is_advanced",
		}
		allRows = []map[string]any{
			{"configuration_id": 101, "name": "recovery interval (min)", "value": 0, "minimum": 0, "maximum": 32767, "value_in_use": 0, "description": "Maximum recovery interval in minutes", "is_dynamic": true, "is_advanced": true},
			{"configuration_id": 103, "name": "max worker threads", "value": 0, "minimum": 128, "maximum": 65535, "value_in_use": 0, "description": "Maximum worker threads", "is_dynamic": false, "is_advanced": true},
			{"configuration_id": 109, "name": "network packet size (B)", "value": 4096, "minimum": 512, "maximum": 32767, "value_in_use": 4096, "description": "Network packet size", "is_dynamic": true, "is_advanced": true},
			{"configuration_id": 115, "name": "nested triggers", "value": 1, "minimum": 0, "maximum": 1, "value_in_use": 1, "description": "Allow triggers to be invoked recursively", "is_dynamic": true, "is_advanced": false},
			{"configuration_id": 503, "name": "max server memory (MB)", "value": 2147483647, "minimum": 128, "maximum": 2147483647, "value_in_use": 2147483647, "description": "Maximum size of server memory (MB)", "is_dynamic": true, "is_advanced": true},
			{"configuration_id": 518, "name": "show advanced options", "value": 0, "minimum": 0, "maximum": 1, "value_in_use": 0, "description": "Show advanced options", "is_dynamic": true, "is_advanced": false},
			{"configuration_id": 1538, "name": "remote access", "value": 1, "minimum": 0, "maximum": 1, "value_in_use": 1, "description": "Allow remote access", "is_dynamic": false, "is_advanced": false},
			{"configuration_id": 1568, "name": "clr enabled", "value": 0, "minimum": 0, "maximum": 1, "value_in_use": 0, "description": "CLR integration enabled", "is_dynamic": true, "is_advanced": true},
			{"configuration_id": 1576, "name": "remote admin connections", "value": 0, "minimum": 0, "maximum": 1, "value_in_use": 0, "description": "Dedicated Admin Connection (DAC) allowed from remote clients", "is_dynamic": true, "is_advanced": true},
			{"configuration_id": 1579, "name": "filestream access level", "value": 0, "minimum": 0, "maximum": 2, "value_in_use": 0, "description": "FileStream access level", "is_dynamic": true, "is_advanced": true},
		}

	case "sys.dm_exec_sessions", "sysdmexecsessions", "sys_dm_exec_sessions":
		columns = []string{
			"session_id", "login_time", "host_name", "program_name", "host_process_id",
			"client_version", "client_interface_name", "security_id", "login_name",
			"nt_domain", "nt_user_name", "status", "cpu_time", "memory_usage",
			"total_scheduled_time", "total_elapsed_time", "endpoint_id",
			"last_request_start_time", "last_request_end_time", "reads", "writes",
			"logical_reads", "is_user_process", "text_size", "language", "date_format",
			"date_first", "quoted_identifier", "arithabort", "ansi_null_dflt_on",
			"ansi_defaults", "ansi_warnings", "ansi_padding", "ansi_nulls",
			"concat_null_yields_null", "transaction_isolation_level", "lock_timeout",
			"deadlock_priority", "row_count", "prev_error", "original_login_name", "database_id",
		}
		allRows = append(allRows, map[string]any{
			"session_id":                  int16(51),
			"login_time":                  defaultDate,
			"host_name":                   "localhost",
			"program_name":                "Microsoft SQL Server Management Studio",
			"host_process_id":             1000,
			"client_version":              0,
			"client_interface_name":       "Microsoft.Data.SqlClient",
			"security_id":                 nil,
			"login_name":                  "sa",
			"nt_domain":                   "",
			"nt_user_name":                "",
			"status":                      "running",
			"cpu_time":                    0,
			"memory_usage":                1024,
			"total_scheduled_time":        0,
			"total_elapsed_time":          0,
			"endpoint_id":                 1,
			"last_request_start_time":     defaultDate,
			"last_request_end_time":       defaultDate,
			"reads":                       int64(0),
			"writes":                      int64(0),
			"logical_reads":               int64(0),
			"is_user_process":             true,
			"text_size":                   4096,
			"language":                    "us_english",
			"date_format":                 "mdy",
			"date_first":                  int16(7),
			"quoted_identifier":           true,
			"arithabort":                  true,
			"ansi_null_dflt_on":           true,
			"ansi_defaults":               false,
			"ansi_warnings":               true,
			"ansi_padding":                true,
			"ansi_nulls":                  true,
			"concat_null_yields_null":     true,
			"transaction_isolation_level": int16(2),
			"lock_timeout":                -1,
			"deadlock_priority":           0,
			"row_count":                   int64(0),
			"prev_error":                  0,
			"original_login_name":         "sa",
			"database_id":                 int16(1),
		})

	case "sys.dm_exec_connections", "sysdmexecconnections", "sys_dm_exec_connections":
		columns = []string{
			"session_id", "most_recent_session_id", "connect_time", "net_transport",
			"protocol_type", "protocol_version", "endpoint_id", "encrypt_option",
			"auth_scheme", "node_affinity", "num_reads", "num_writes",
			"last_read", "last_write", "net_packet_size", "client_net_address",
			"client_tcp_port", "local_net_address", "local_tcp_port", "connection_id",
		}
		allRows = append(allRows, map[string]any{
			"session_id":             51,
			"most_recent_session_id": 51,
			"connect_time":           defaultDate,
			"net_transport":          "TCP",
			"protocol_type":          "TSQL",
			"protocol_version":       0x74000004,
			"endpoint_id":            1,
			"encrypt_option":         "TRUE",
			"auth_scheme":            "SQL",
			"node_affinity":          int16(0),
			"num_reads":              1,
			"num_writes":             1,
			"last_read":              defaultDate,
			"last_write":             defaultDate,
			"net_packet_size":        4096,
			"client_net_address":     "127.0.0.1",
			"client_tcp_port":        1433,
			"local_net_address":      "127.0.0.1",
			"local_tcp_port":         1433,
			"connection_id":          "00000000-0000-0000-0000-000000000001",
		})

	case "sys.database_principals", "sysdatabaseprincipals", "sys_database_principals":
		columns = []string{
			"name", "principal_id", "type", "type_desc", "default_schema_name",
			"create_date", "modify_date", "owning_principal_id", "sid",
			"is_fixed_role", "authentication_type", "authentication_type_desc",
			"default_language_name", "default_language_lcid",
		}
		allRows = []map[string]any{
			{"name": "dbo", "principal_id": 1, "type": "S", "type_desc": "SQL_USER", "default_schema_name": "dbo", "create_date": defaultDate, "modify_date": defaultDate, "owning_principal_id": nil, "sid": nil, "is_fixed_role": false, "authentication_type": 2, "authentication_type_desc": "INSTANCE", "default_language_name": "us_english", "default_language_lcid": 1033},
			{"name": "guest", "principal_id": 2, "type": "S", "type_desc": "SQL_USER", "default_schema_name": "guest", "create_date": defaultDate, "modify_date": defaultDate, "owning_principal_id": nil, "sid": nil, "is_fixed_role": false, "authentication_type": 0, "authentication_type_desc": "NONE", "default_language_name": nil, "default_language_lcid": nil},
			{"name": "INFORMATION_SCHEMA", "principal_id": 3, "type": "S", "type_desc": "SQL_USER", "default_schema_name": nil, "create_date": defaultDate, "modify_date": defaultDate, "owning_principal_id": nil, "sid": nil, "is_fixed_role": false, "authentication_type": 0, "authentication_type_desc": "NONE", "default_language_name": nil, "default_language_lcid": nil},
			{"name": "sys", "principal_id": 4, "type": "S", "type_desc": "SQL_USER", "default_schema_name": nil, "create_date": defaultDate, "modify_date": defaultDate, "owning_principal_id": nil, "sid": nil, "is_fixed_role": false, "authentication_type": 0, "authentication_type_desc": "NONE", "default_language_name": nil, "default_language_lcid": nil},
			{"name": "public", "principal_id": 0, "type": "R", "type_desc": "DATABASE_ROLE", "default_schema_name": nil, "create_date": defaultDate, "modify_date": defaultDate, "owning_principal_id": nil, "sid": nil, "is_fixed_role": false, "authentication_type": 0, "authentication_type_desc": "NONE", "default_language_name": nil, "default_language_lcid": nil},
			{"name": "db_owner", "principal_id": 16384, "type": "R", "type_desc": "DATABASE_ROLE", "default_schema_name": nil, "create_date": defaultDate, "modify_date": defaultDate, "owning_principal_id": 1, "sid": nil, "is_fixed_role": true, "authentication_type": 0, "authentication_type_desc": "NONE", "default_language_name": nil, "default_language_lcid": nil},
		}

	case "sys.server_principals", "sysserverprincipals", "sys_server_principals":
		columns = []string{
			"name", "principal_id", "sid", "type", "type_desc", "is_disabled",
			"create_date", "modify_date", "default_database_name", "default_language_name",
			"is_fixed_role",
		}
		allRows = []map[string]any{
			{"name": "sa", "principal_id": 1, "sid": nil, "type": "S", "type_desc": "SQL_LOGIN", "is_disabled": false, "create_date": defaultDate, "modify_date": defaultDate, "default_database_name": "master", "default_language_name": "us_english", "is_fixed_role": false},
			{"name": "public", "principal_id": 2, "sid": nil, "type": "R", "type_desc": "SERVER_ROLE", "is_disabled": false, "create_date": defaultDate, "modify_date": defaultDate, "default_database_name": nil, "default_language_name": nil, "is_fixed_role": false},
			{"name": "sysadmin", "principal_id": 3, "sid": nil, "type": "R", "type_desc": "SERVER_ROLE", "is_disabled": false, "create_date": defaultDate, "modify_date": defaultDate, "default_database_name": nil, "default_language_name": nil, "is_fixed_role": true},
		}

	case "sys.dm_os_sys_info", "sysdmosysinfo", "sys_dm_os_sys_info":
		columns = []string{
			"cpu_count", "hyperthread_ratio", "physical_memory_kb", "virtual_memory_kb",
			"sqlserver_start_time", "affinity_type", "affinity_type_desc", "os_priority_class",
		}
		allRows = []map[string]any{
			{
				"cpu_count":            runtime.NumCPU(),
				"hyperthread_ratio":    runtime.NumCPU(),
				"physical_memory_kb":   int64(16 * 1024 * 1024),
				"virtual_memory_kb":    int64(16 * 1024 * 1024),
				"sqlserver_start_time": defaultDate,
				"affinity_type":        1,
				"affinity_type_desc":   "AUTO",
				"os_priority_class":    32,
			},
		}

	case "sys.dm_os_host_info", "sysdmoshostinfo", "sys_dm_os_host_info":
		columns = []string{
			"host_platform", "host_distribution", "host_release",
			"host_service_pack_level", "os_architecture", "host_sku",
		}
		// [RULE:Invariant11] Permanent "Windows" Host Platform Invariant:
		// SSMS Object Explorer and SMO fail with InvalidOperationException if host_platform is "macOS" or anything
		// other than standard Windows. To ensure 100% client tool parity across all platforms, sys.dm_os_host_info
		// unconditionally returns "Windows" / "Windows" / "10.0". NEVER re-introduce runtime.GOOS checks here.
		platform := "Windows"
		dist := "Windows"
		release := "10.0"
		arch := "64-bit"
		if runtime.GOARCH == "386" || runtime.GOARCH == "arm" {
			arch = "32-bit"
		}
		allRows = []map[string]any{
			{
				"host_platform":           platform,
				"host_distribution":       dist,
				"host_release":            release,
				"host_service_pack_level": "",
				"os_architecture":         arch,
				"host_sku":                48,
			},
		}

	case "sys.endpoints", "sysendpoints", "sys_endpoints":
		columns = []string{
			"endpoint_id", "principal_id", "name", "protocol", "protocol_desc",
			"type", "type_desc", "state", "state_desc", "is_admin_endpoint",
		}
		allRows = []map[string]any{
			{"endpoint_id": 1, "principal_id": 1, "name": "Dedicated Admin Connection", "protocol": 2, "protocol_desc": "TCP", "type": 2, "type_desc": "TSQL", "state": 0, "state_desc": "STARTED", "is_admin_endpoint": true},
			{"endpoint_id": 2, "principal_id": 1, "name": "TSQL Default TCP", "protocol": 2, "protocol_desc": "TCP", "type": 2, "type_desc": "TSQL", "state": 0, "state_desc": "STARTED", "is_admin_endpoint": false},
		}

	case "sys.data_spaces", "sysdataspaces", "sys_data_spaces":
		columns = []string{"data_space_id", "name", "type", "type_desc", "is_default", "is_system"}
		allRows = []map[string]any{
			{"data_space_id": 1, "name": "PRIMARY", "type": "FG", "type_desc": "ROWS_FILEGROUP", "is_default": true, "is_system": false},
		}

	case "sys.extended_properties", "sysextendedproperties", "sys_extended_properties":
		columns = []string{"class", "class_desc", "major_id", "minor_id", "name", "value"}
		allRows = []map[string]any{}

	case "sys.synonyms", "syssynonyms", "sys_synonyms":
		columns = []string{
			"name", "object_id", "principal_id", "schema_id", "parent_object_id",
			"type", "type_desc", "create_date", "modify_date", "is_ms_shipped",
			"is_published", "is_schema_published", "base_object_name",
		}
		allRows = []map[string]any{}

	case "sys.sql_modules", "syssqlmodules", "sys_sql_modules",
		"sys.all_sql_modules", "sysallsqlmodules", "sys_all_sql_modules",
		"sys.system_sql_modules", "syssystemsqlmodules", "sys_system_sql_modules":
		columns = []string{
			"object_id", "definition", "uses_ansi_nulls", "uses_quoted_identifier",
			"is_schema_bound", "uses_database_collation", "is_recompiled",
			"null_on_null_input", "execute_as_principal_id", "uses_native_compilation",
		}
		allRows = []map[string]any{}
		if s != nil {
			views := s.getSortedViews()
			for vIdx, v := range views {
				allRows = append(allRows, map[string]any{
					"object_id":               int32(2000 + vIdx),
					"definition":              v.Query,
					"uses_ansi_nulls":         true,
					"uses_quoted_identifier":   true,
					"is_schema_bound":         false,
					"uses_database_collation": false,
					"is_recompiled":           false,
					"null_on_null_input":      false,
					"execute_as_principal_id": nil,
					"uses_native_compilation": int32(0),
				})
			}
			procs := s.getSortedProcedures()
			for pIdx, p := range procs {
				allRows = append(allRows, map[string]any{
					"object_id":               int32(3000 + pIdx),
					"definition":              p.Body,
					"uses_ansi_nulls":         true,
					"uses_quoted_identifier":   true,
					"is_schema_bound":         false,
					"uses_database_collation": false,
					"is_recompiled":           false,
					"null_on_null_input":      false,
					"execute_as_principal_id": nil,
					"uses_native_compilation": int32(0),
				})
			}
		}

	case "sys.syscomments", "syscomments", "sys_syscomments":
		columns = []string{"id", "number", "colid", "status", "ctext", "texttype", "language", "encrypted", "compressed", "text"}
		allRows = []map[string]any{}
		if s != nil {
			views := s.getSortedViews()
			for vIdx, v := range views {
				allRows = append(allRows, map[string]any{
					"id":         int32(2000 + vIdx),
					"number":     int32(1),
					"colid":      int32(1),
					"status":     int32(0),
					"ctext":      nil,
					"texttype":   int32(0),
					"language":   int32(0),
					"encrypted":  false,
					"compressed": false,
					"text":       v.Query,
				})
			}
			procs := s.getSortedProcedures()
			for pIdx, p := range procs {
				allRows = append(allRows, map[string]any{
					"id":         int32(3000 + pIdx),
					"number":     int32(1),
					"colid":      int32(1),
					"status":     int32(0),
					"ctext":      nil,
					"texttype":   int32(0),
					"language":   int32(0),
					"encrypted":  false,
					"compressed": false,
					"text":       p.Body,
				})
			}
		}

	case "sys.database_files", "sysdatabasefiles", "sys_database_files":
		columns = []string{"file_id", "file_guid", "type", "type_desc", "data_space_id", "name", "physical_name", "state", "state_desc", "size", "max_size", "growth", "is_media_read_only", "is_read_only", "is_sparse", "is_percent_growth"}
		allRows = append(allRows,
			map[string]any{
				"file_id":            1,
				"file_guid":          nil,
				"type":               0,
				"type_desc":          "ROWS",
				"data_space_id":      1,
				"name":               "lionsql_data",
				"physical_name":      "lionsql.mdf",
				"state":              0,
				"state_desc":         "ONLINE",
				"size":               2048,
				"max_size":           -1,
				"growth":             1024,
				"is_media_read_only": false,
				"is_read_only":       false,
				"is_sparse":          false,
				"is_percent_growth":  false,
			},
			map[string]any{
				"file_id":            2,
				"file_guid":          nil,
				"type":               1,
				"type_desc":          "LOG",
				"data_space_id":      0,
				"name":               "lionsql_log",
				"physical_name":      "lionsql_log.ldf",
				"state":              0,
				"state_desc":         "ONLINE",
				"size":               1024,
				"max_size":           -1,
				"growth":             1024,
				"is_media_read_only": false,
				"is_read_only":       false,
				"is_sparse":          false,
				"is_percent_growth":  false,
			},
		)

	case "syspolicy_configuration", "dbo.syspolicy_configuration", "sys.syspolicy_configuration":
		columns = []string{"name", "current_value", "description"}
		allRows = []map[string]any{
			{"name": "Enabled", "current_value": false, "description": "Determines whether Policy-Based Management is enabled"},
			{"name": "HistoryRetentionInDays", "current_value": int32(0), "description": "Number of days to retain history"},
			{"name": "LogOnSuccess", "current_value": false, "description": "Whether to log successful policy evaluations"},
		}

	case "syspolicy_policies", "dbo.syspolicy_policies", "sys.syspolicy_policies":
		columns = []string{
			"policy_id", "name", "condition_id", "root_condition_id", "date_created",
			"execution_mode", "policy_category", "schedule_uid", "description",
			"help_text", "help_link", "object_set_id", "is_enabled", "job_id",
		}
		allRows = []map[string]any{}

	case "syspolicy_conditions", "dbo.syspolicy_conditions", "sys.syspolicy_conditions":
		columns = []string{
			"condition_id", "name", "facet_id", "expression", "is_name_condition",
			"date_created", "modify_date", "created_by", "modified_by",
		}
		allRows = []map[string]any{}

	case "syspolicy_facets", "dbo.syspolicy_facets", "sys.syspolicy_facets":
		columns = []string{"facet_id", "name", "description"}
		allRows = []map[string]any{}

	case "syspolicy_policy_categories", "dbo.syspolicy_policy_categories", "sys.syspolicy_policy_categories":
		columns = []string{"policy_category_id", "name"}
		allRows = []map[string]any{}

	case "syspolicy_target_sets", "dbo.syspolicy_target_sets", "sys.syspolicy_target_sets":
		columns = []string{"target_set_id", "object_set_id", "type_skeleton", "type"}
		allRows = []map[string]any{}

	case "syspolicy_target_set_levels", "dbo.syspolicy_target_set_levels", "sys.syspolicy_target_set_levels":
		columns = []string{"target_set_level_id", "target_set_id", "level_name", "condition_id"}
		allRows = []map[string]any{}

	case "syspolicy_policy_execution_history", "dbo.syspolicy_policy_execution_history", "sys.syspolicy_policy_execution_history":
		columns = []string{"history_id", "policy_id", "start_date", "end_date", "result"}
		allRows = []map[string]any{}

	case "syspolicy_policy_execution_history_details", "dbo.syspolicy_policy_execution_history_details", "sys.syspolicy_policy_execution_history_details":
		columns = []string{"detail_id", "history_id", "target_query_expression", "result", "exception_message", "exception"}
		allRows = []map[string]any{}

	case "sysjobs", "dbo.sysjobs", "sys.sysjobs":
		columns = []string{"job_id", "name", "enabled", "description", "start_step_id", "category_id", "owner_sid", "notify_level_eventlog", "notify_level_email", "date_created", "date_modified", "version_number"}
		allRows = []map[string]any{}

	case "syscategories", "dbo.syscategories", "sys.syscategories":
		columns = []string{"category_id", "category_type", "name"}
		allRows = []map[string]any{}

	case "sysjobsteps", "dbo.sysjobsteps", "sys.sysjobsteps":
		columns = []string{"job_id", "step_id", "step_name", "subsystem", "command", "flags"}
		allRows = []map[string]any{}

	case "sysschedules", "dbo.sysschedules", "sys.sysschedules":
		columns = []string{"schedule_id", "schedule_uid", "name", "enabled", "freq_type", "freq_interval"}
		allRows = []map[string]any{}

	case "sysjobhistory", "dbo.sysjobhistory", "sys.sysjobhistory":
		columns = []string{"instance_id", "job_id", "step_id", "step_name", "sql_message_id", "sql_severity", "message", "run_status", "run_date", "run_time", "run_duration"}
		allRows = []map[string]any{}

	case "sysalerts", "dbo.sysalerts", "sys.sysalerts":
		columns = []string{"id", "name", "message_id", "severity", "enabled", "delay_between_responses", "last_occurrence_date", "last_occurrence_time"}
		allRows = []map[string]any{}

	case "sysoperators", "dbo.sysoperators", "sys.sysoperators":
		columns = []string{"id", "name", "enabled", "email_address", "last_email_date", "last_email_time"}
		allRows = []map[string]any{}

	case "sys.master_files", "sysmasterfiles", "sys_master_files":
		columns = []string{"database_id", "file_id", "file_guid", "type", "type_desc", "data_space_id", "name", "physical_name", "state", "state_desc", "size", "max_size", "growth", "is_media_read_only", "is_read_only", "is_sparse", "is_percent_growth"}
		allRows = append(allRows,
			map[string]any{
				"database_id":        1,
				"file_id":            1,
				"file_guid":          nil,
				"type":               0,
				"type_desc":          "ROWS",
				"data_space_id":      1,
				"name":               "master",
				"physical_name":      "master.mdf",
				"state":              0,
				"state_desc":         "ONLINE",
				"size":               2048,
				"max_size":           -1,
				"growth":             1024,
				"is_media_read_only": false,
				"is_read_only":       false,
				"is_sparse":          false,
				"is_percent_growth":  false,
			},
			map[string]any{
				"database_id":        1,
				"file_id":            2,
				"file_guid":          nil,
				"type":               1,
				"type_desc":          "LOG",
				"data_space_id":      0,
				"name":               "mastlog",
				"physical_name":      "mastlog.ldf",
				"state":              0,
				"state_desc":         "ONLINE",
				"size":               1024,
				"max_size":           -1,
				"growth":             1024,
				"is_media_read_only": false,
				"is_read_only":       false,
				"is_sparse":          false,
				"is_percent_growth":  false,
			},
		)

	default:
		return nil, fmt.Errorf("unsupported virtual system table: %s", tableName)
	}

	for _, r := range allRows {
		if _, ok := r["db_name"]; !ok {
			r["db_name"] = currDb
		}
		if _, ok := r["current_database"]; !ok {
			r["current_database"] = currDb
		}
	}

	return buildVirtualCatalogResult(columns, allRows, projStr, whereClause, orderByClause, limitCount, start)
}
