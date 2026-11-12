package engine

import (
	"fmt"
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

	columns, allRows, handled, err := virtual.GenerateSysCatalogRows(s, norm, currDb)
	if err != nil {
		return nil, err
	}
	if !handled {
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
