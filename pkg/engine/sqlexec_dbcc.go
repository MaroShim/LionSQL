package engine

import (
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/catalog/virtual"
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
		re := regexp.MustCompile(`(?is)^CHECKTABLE\s*(?:\(\s*['"]?([^'")\s]+)['"]?\s*\)|([^\s;]+))`)
		m := re.FindStringSubmatch(rest)
		if len(m) < 2 {
			return nil, fmt.Errorf("invalid DBCC CHECKTABLE syntax. Expected: DBCC CHECKTABLE ('table_name')")
		}
		rawTbl := m[1]
		if rawTbl == "" && len(m) > 2 {
			rawTbl = m[2]
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
		messages := []string{"DBCC results for 'sqlion'."}

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
			fmt.Sprintf("CHECKDB found %d allocation errors and %d consistency errors in database 'sqlion'.", totalAllocErrors, totalConsistErrors),
			"DBCC execution completed. If DBCC printed error messages, contact your system administrator.",
		)

		dbName := "master"
		if cs != nil && cs.Database != "" {
			dbName = cs.Database
		}
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

	// Order by
	if orderByClause != "" && len(filteredRows) > 0 {
		orderParts := strings.Fields(orderByClause)
		orderCol := cleanIdentifier(orderParts[0])
		desc := len(orderParts) > 1 && strings.EqualFold(orderParts[1], "DESC")
		sort.Slice(filteredRows, func(i, j int) bool {
			v1 := fmt.Sprintf("%v", filteredRows[i][orderCol])
			v2 := fmt.Sprintf("%v", filteredRows[j][orderCol])
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

	// Limit
	if limitCount >= 0 && len(filteredRows) > limitCount {
		filteredRows = filteredRows[:limitCount]
	}

	// Projection
	var selectedCols []string
	colSourceMap := make(map[string]string)
	trimmedProj := strings.TrimSpace(projStr)
	if trimmedProj == "*" || trimmedProj == "" || strings.HasSuffix(trimmedProj, ".*") {
		selectedCols = columns
		for _, col := range columns {
			colSourceMap[col] = col
		}
	} else {
		asRe := regexp.MustCompile(`(?is)^(.*?)\s+AS\s+([a-zA-Z0-9_#$]+|"[^"]+")$`)
		for _, c := range splitScalarProjections(projStr) {
			cTrimmed := strings.TrimSpace(c)
			if cTrimmed == "" {
				continue
			}
			if cTrimmed == "*" || strings.HasSuffix(cTrimmed, ".*") {
				for _, col := range columns {
					selectedCols = append(selectedCols, col)
					colSourceMap[col] = col
				}
				continue
			}
			alias := ""
			colExpr := cTrimmed
			if m := asRe.FindStringSubmatch(cTrimmed); len(m) == 3 {
				colExpr = strings.TrimSpace(m[1])
				alias = cleanIdentifier(m[2])
			} else {
				fields := strings.Fields(cTrimmed)
				if len(fields) == 2 && !strings.Contains(fields[1], "(") {
					colExpr = fields[0]
					alias = cleanIdentifier(fields[1])
				}
			}
			rawCol := strings.ToLower(cleanIdentifier(colExpr))
			if strings.Contains(rawCol, ".") {
				rawCol = rawCol[strings.LastIndex(rawCol, ".")+1:]
			}
			outName := alias
			if outName == "" {
				outName = rawCol
			}
			selectedCols = append(selectedCols, outName)
			colSourceMap[outName] = rawCol
		}
	}

	finalRows := make([]map[string]any, 0, len(filteredRows))
	for _, fr := range filteredRows {
		row := make(map[string]any, len(selectedCols))
		for _, col := range selectedCols {
			src := colSourceMap[col]
			if src == "" {
				src = col
			}
			if v, ok := fr[src]; ok {
				row[col] = v
			} else if v, ok := fr[strings.ToLower(col)]; ok {
				row[col] = v
			} else {
				row[col] = nil
			}
		}
		finalRows = append(finalRows, row)
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

func (s *Coordinator) isVirtualCatalogTable(tableName string) bool {
	lower := strings.ToLower(cleanIdentifier(tableName))
	// Delegate pg_catalog.* and information_schema.* to the dedicated handler.
	if virtual.IsPgCatalogTable(lower) {
		return true
	}
	switch lower {
	case "sys.tables", "systables", "sys_tables",
		"sys.columns", "syscolumns", "sys_columns",
		"sys.indexes", "sysindexes", "sys_indexes",
		"sys.databases", "sysdatabases", "sys_databases",
		"sys.procedures", "sysprocedures", "sys_procedures",
		"sys.table_stats", "systablestats", "sys_table_stats",
		"sys.column_stats", "syscolumnstats", "sys_column_stats",
		"sys.schemas", "sysschemas", "sys_schemas",
		"sys.objects", "sysobjects", "sys_objects",
		"sys.views", "sysviews", "sys_views",
		"sys.types", "systypes", "sys_types",
		"sys.partitions", "system.partitions", "syspartitions", "sys_partitions",
		"sys.computed_columns", "syscomputedcolumns", "sys_computed_columns",
		"sys.triggers", "systriggers", "sys_triggers",
		"sys.foreign_keys", "sysforeignkeys", "sys_foreign_keys",
		"sys.foreign_key_columns", "sysforeignkeycolumns", "sys_foreign_key_columns",
		"sys.configurations", "sysconfigurations", "sys_configurations",
		"sys.dm_exec_sessions", "sysdmexecsessions", "sys_dm_exec_sessions",
		"sys.dm_exec_connections", "sysdmexecconnections", "sys_dm_exec_connections",
		"sys.database_principals", "sysdatabaseprincipals", "sys_database_principals",
		"sys.server_principals", "sysserverprincipals", "sys_server_principals",
		"sys.dm_os_sys_info", "sysdmosysinfo", "sys_dm_os_sys_info",
		"sys.endpoints", "sysendpoints", "sys_endpoints",
		"sys.database_files", "sysdatabasefiles", "sys_database_files",
		"sys.master_files", "sysmasterfiles", "sys_master_files",
		"sys.all_objects", "sysallobjects", "sys_all_objects",
		"sys.all_columns", "sysallcolumns", "sys_all_columns",
		"sys.all_views", "sysallviews", "sys_all_views":
		return true
	default:
		return false
	}
}

func (s *Coordinator) execVirtualCatalogSelect(tableName, projStr, whereClause, orderByClause string, limitCount int, start time.Time) (*QueryResult, error) {
	norm := strings.ToLower(cleanIdentifier(tableName))
	norm = strings.TrimPrefix(norm, "dbo.")

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
		columns = []string{"table_name", "name", "object_id", "schema_id", "table_type", "table_schema", "column_count", "create_date", "modify_date"}
		for i, t := range tables {
			allRows = append(allRows, map[string]any{
				"table_name":   t.Name,
				"name":         t.Name,
				"object_id":    1000 + i,
				"schema_id":    1,
				"table_type":   "BASE TABLE",
				"table_schema": "dbo",
				"column_count": len(t.Columns),
				"create_date":  defaultDate,
				"modify_date":  defaultDate,
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
		columns = []string{"table_name", "index_name", "is_unique", "columns"}
		for _, t := range tables {
			for _, idx := range t.Indexes {
				allRows = append(allRows, map[string]any{
					"table_name": t.Name,
					"index_name": idx.Name,
					"is_unique":  idx.Unique,
					"columns":    strings.Join(idx.Columns, ", "),
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
		}
		allRows = append(allRows,
			map[string]any{
				"name":                          "master",
				"schema_name":                   "dbo",
				"database_id":                   1,
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
			},
		)

	case "sys.procedures", "sysprocedures", "sys_procedures":
		columns = []string{"routine_name", "routine_type", "routine_schema", "created_date"}
		if s.catalog != nil {
			for _, p := range s.catalog.ListProcedures() {
				allRows = append(allRows, map[string]any{
					"routine_name":   p.Name,
					"routine_type":   "PROCEDURE",
					"routine_schema": "dbo",
					"created_date":   p.CreatedAt,
				})
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
			"name", "object_id", "principal_id", "schema_id", "parent_object_id",
			"type", "type_desc", "create_date", "modify_date",
			"is_ms_shipped", "is_published", "is_schema_published",
		}
		for i, t := range tables {
			allRows = append(allRows, map[string]any{
				"name":                t.Name,
				"object_id":           1000 + i,
				"principal_id":        nil,
				"schema_id":           1,
				"parent_object_id":    0,
				"type":                "U ",
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
					"principal_id":        nil,
					"schema_id":           1,
					"parent_object_id":    0,
					"type":                "V ",
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
					"principal_id":        nil,
					"schema_id":           1,
					"parent_object_id":    0,
					"type":                "P ",
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

	case "sys.endpoints", "sysendpoints", "sys_endpoints":
		columns = []string{
			"endpoint_id", "principal_id", "name", "protocol", "protocol_desc",
			"type", "type_desc", "state", "state_desc", "is_admin_endpoint",
		}
		allRows = []map[string]any{
			{"endpoint_id": 1, "principal_id": 1, "name": "Dedicated Admin Connection", "protocol": 2, "protocol_desc": "TCP", "type": 2, "type_desc": "TSQL", "state": 0, "state_desc": "STARTED", "is_admin_endpoint": true},
			{"endpoint_id": 2, "principal_id": 1, "name": "TSQL Default TCP", "protocol": 2, "protocol_desc": "TCP", "type": 2, "type_desc": "TSQL", "state": 0, "state_desc": "STARTED", "is_admin_endpoint": false},
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
				"name":               "sqlion_data",
				"physical_name":      "sqlion.mdf",
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
				"name":               "sqlion_log",
				"physical_name":      "sqlion_log.ldf",
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

	return buildVirtualCatalogResult(columns, allRows, projStr, whereClause, orderByClause, limitCount, start)
}
