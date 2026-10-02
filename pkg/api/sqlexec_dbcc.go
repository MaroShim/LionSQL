package api

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

// --- T-SQL Database Console Commands (DBCC) ---

func (s *Server) execDBCC(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
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
			cs.mu.Lock()
			if lvl, ok := cs.Vars["transaction_isolation"].(string); ok && lvl != "" {
				isoLevel = lvl
			}
			cs.mu.Unlock()
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
			Success:      true,
			Command:      "DBCC",
			Columns:      []string{"Table", "Rows", "AllocationErrors", "ConsistencyErrors", "Status"},
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

		return &QueryResult{
			Success:      true,
			Command:      "DBCC",
			Columns:      []string{"Database", "TablesChecked", "AllocationErrors", "ConsistencyErrors", "Status"},
			Rows: []map[string]any{{
				"Database":          "sqlion",
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
	if projStr == "*" || projStr == "" {
		selectedCols = columns
		for _, col := range columns {
			colSourceMap[col] = col
		}
	} else {
		asRe := regexp.MustCompile(`(?is)^(.*?)\s+AS\s+([a-zA-Z0-9_#$]+|"[^"]+")$`)
		for _, c := range strings.Split(projStr, ",") {
			cTrimmed := strings.TrimSpace(c)
			if cTrimmed == "" {
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

func (s *Server) isVirtualCatalogTable(tableName string) bool {
	lower := strings.ToLower(cleanIdentifier(tableName))
	// Delegate pg_catalog.* and information_schema.* to the dedicated handler.
	if isPgCatalogTable(lower) {
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
		"sys.partitions", "system.partitions", "syspartitions", "sys_partitions":
		return true
	default:
		return false
	}
}

func (s *Server) execVirtualCatalogSelect(tableName, projStr, whereClause, orderByClause string, limitCount int, start time.Time) (*QueryResult, error) {
	norm := strings.ToLower(cleanIdentifier(tableName))
	norm = strings.TrimPrefix(norm, "dbo.")

	// Delegate pg_catalog.* and information_schema.* to the dedicated handler.
	if isPgCatalogTable(norm) {
		return s.execPgCatalogSelect(norm, projStr, whereClause, orderByClause, limitCount, start)
	}

	var columns []string
	var allRows []map[string]any
	tables, _ := s.engine.ListTables()

	switch norm {
	case "sys.tables", "systables", "sys_tables":
		columns = []string{"table_name", "name", "object_id", "schema_id", "table_type", "table_schema", "column_count", "create_date"}
		for i, t := range tables {
			allRows = append(allRows, map[string]any{
				"table_name":   t.Name,
				"name":         t.Name,
				"object_id":    1000 + i,
				"schema_id":    1,
				"table_type":   "BASE TABLE",
				"table_schema": "dbo",
				"column_count": len(t.Columns),
				"create_date":  "2026-01-01T00:00:00Z",
			})
		}

	case "sys.columns", "syscolumns", "sys_columns":
		columns = []string{"table_name", "column_name", "name", "object_id", "column_id", "data_type", "is_nullable", "is_primary_key", "ordinal_position", "max_length", "precision", "scale"}
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
					"data_type":        c.Type.String(),
					"is_nullable":      nullableStr,
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
		columns = []string{"name", "schema_name", "database_id", "create_date", "state_desc"}
		allRows = append(allRows, map[string]any{
			"name":        "sqlion",
			"schema_name": "sqlion",
			"database_id": 1,
			"create_date": "2026-01-01T00:00:00Z",
			"state_desc":  "ONLINE",
		})

	case "sys.procedures", "sysprocedures", "sys_procedures":
		columns = []string{"routine_name", "routine_type", "routine_schema", "created_date"}
		if s.catalog != nil {
			for _, p := range s.catalog.ListProcedures() {
				allRows = append(allRows, map[string]any{
					"routine_name":   p.Name,
					"routine_type":   "PROCEDURE",
					"routine_schema": "dbo",
					"created_date":   p.CreatedAt.Format(time.RFC3339),
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

	case "sys.objects", "sysobjects", "sys_objects":
		columns = []string{"name", "object_id", "principal_id", "schema_id", "type", "type_desc", "create_date", "modify_date"}
		for i, t := range tables {
			allRows = append(allRows, map[string]any{
				"name":         t.Name,
				"object_id":    1000 + i,
				"principal_id": nil,
				"schema_id":    1,
				"type":         "U",
				"type_desc":    "USER_TABLE",
				"create_date":  "2026-01-01T00:00:00Z",
				"modify_date":  "2026-01-01T00:00:00Z",
			})
		}

	case "sys.views", "sysviews", "sys_views":
		columns = []string{"name", "object_id", "schema_id"}
		if s.catalog != nil {
			for _, v := range s.catalog.ListViews() {
				allRows = append(allRows, map[string]any{
					"name":      v.Name,
					"object_id": 2000,
					"schema_id": 1,
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

	default:
		return nil, fmt.Errorf("unsupported virtual system table: %s", tableName)
	}

	return buildVirtualCatalogResult(columns, allRows, projStr, whereClause, orderByClause, limitCount, start)
}
