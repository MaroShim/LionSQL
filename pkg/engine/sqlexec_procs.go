package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// unwrapSQLString removes SQL quote delimiters and unescapes double single-quotes.
func unwrapSQLString(s string) string {
	s = strings.TrimSpace(s)
	if (strings.HasPrefix(s, "N'") || strings.HasPrefix(s, "n'")) && strings.HasSuffix(s, "'") && len(s) >= 3 {
		inner := s[2 : len(s)-1]
		return strings.ReplaceAll(inner, "''", "'")
	}
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
		inner := s[1 : len(s)-1]
		return strings.ReplaceAll(inner, "''", "'")
	}
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2 {
		return s[1 : len(s)-1]
	}
	return s
}

// ParseProcArgs parses procedure name and argument tokens from EXEC / CALL query string.
func ParseProcArgs(raw string) (string, []string) {
	raw = strings.TrimSpace(raw)
	if rem, ok := sqlutil.TrimAnyPrefixKeyword(raw, "EXECUTE", "EXEC", "CALL"); ok {
		raw = rem
	}

	// Strip optional return variable: EXEC @ret = procName or EXEC @ret = sp_executesql
	if strings.HasPrefix(raw, "@") {
		eqIdx := strings.Index(raw, "=")
		if eqIdx != -1 {
			// Ensure it's not a parameter name assignment like @p = 1
			candidate := strings.TrimSpace(raw[:eqIdx])
			if !strings.ContainsAny(candidate, " \t,()") {
				raw = strings.TrimSpace(raw[eqIdx+1:])
			}
		}
	}

	// Dynamic SQL invocation with parentheses: EXEC ('SELECT ...') or EXEC (@sqlVar)
	if strings.HasPrefix(raw, "(") && strings.HasSuffix(raw, ")") {
		inner := strings.TrimSpace(raw[1 : len(raw)-1])
		return "", []string{inner}
	}

	// Dynamic SQL invocation without parentheses:
	// e.g. EXEC 'SELECT ...' or EXEC N'SELECT ...' or EXEC @sql
	if strings.HasPrefix(raw, "'") || strings.HasPrefix(raw, "N'") || strings.HasPrefix(raw, "n'") || strings.HasPrefix(raw, "\"") || strings.HasPrefix(raw, "@") {
		return "", []string{raw}
	}

	idx := strings.IndexAny(raw, " \t,(")
	if idx == -1 {
		return cleanIdentifier(raw), nil
	}

	procName := cleanIdentifier(raw[:idx])
	rem := strings.TrimSpace(raw[idx:])
	if strings.HasPrefix(rem, "(") && strings.HasSuffix(rem, ")") {
		rem = strings.TrimSpace(rem[1 : len(rem)-1])
	}
	rem = strings.TrimRight(rem, ";")

	if rem == "" {
		return procName, nil
	}

	args := sqlutil.SplitTopLevel(rem, ',')
	return procName, args
}

// parseProcParams maps arguments to parameter names, supporting both named (@param = val) and positional syntax.
func parseProcParams(args []string, paramNames []string) map[string]string {
	params := make(map[string]string, len(args))
	posIdx := 0
	for _, raw := range args {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "@") && strings.Contains(trimmed, "=") {
			parts := strings.SplitN(trimmed, "=", 2)
			pName := strings.ToLower(strings.TrimSpace(parts[0]))
			val := strings.TrimSpace(parts[1])
			val = unwrapSQLString(val)
			params[pName] = val
		} else {
			val := unwrapSQLString(trimmed)
			if posIdx < len(paramNames) {
				params[paramNames[posIdx]] = val
			}
			posIdx++
		}
	}
	return params
}

func cleanTableNameParam(s string) string {
	s = strings.TrimSpace(s)
	s = unwrapSQLString(s)
	s = strings.TrimSpace(s)
	if strings.Contains(s, ".") {
		parts := strings.Split(s, ".")
		s = parts[len(parts)-1]
	}
	s = cleanIdentifier(s)
	return s
}

func mapStorageColumnToSpColumn(col storage.Column, tblName string, dbName string, ordinal int) map[string]any {
	row := map[string]any{
		"TABLE_QUALIFIER":   dbName,
		"TABLE_OWNER":       "dbo",
		"TABLE_NAME":        tblName,
		"COLUMN_NAME":       col.Name,
		"ORDINAL_POSITION":  ordinal,
		"REMARKS":           nil,
		"COLUMN_DEF":        nil,
		"SQL_DATETIME_SUB":  nil,
		"CHAR_OCTET_LENGTH": nil,
	}
	if col.Default != "" {
		row["COLUMN_DEF"] = col.Default
	}
	if col.Nullable {
		row["NULLABLE"] = 1
		row["IS_NULLABLE"] = "YES"
	} else {
		row["NULLABLE"] = 0
		row["IS_NULLABLE"] = "NO"
	}

	switch col.Type {
	case storage.TypeInt:
		row["DATA_TYPE"] = 4 // SQL_INTEGER
		row["TYPE_NAME"] = "int"
		row["PRECISION"] = 10
		row["LENGTH"] = 4
		row["SCALE"] = 0
		row["RADIX"] = 10
		row["SQL_DATA_TYPE"] = 4
		row["SS_DATA_TYPE"] = 56
	case storage.TypeVarChar:
		prec := col.Precision
		if prec <= 0 {
			prec = 255
		}
		row["DATA_TYPE"] = 12 // SQL_VARCHAR
		row["TYPE_NAME"] = "varchar"
		row["PRECISION"] = prec
		row["LENGTH"] = prec
		row["SCALE"] = nil
		row["RADIX"] = nil
		row["SQL_DATA_TYPE"] = 12
		row["CHAR_OCTET_LENGTH"] = prec
		row["SS_DATA_TYPE"] = 39
	case storage.TypeReal:
		row["DATA_TYPE"] = 7 // SQL_REAL
		row["TYPE_NAME"] = "real"
		row["PRECISION"] = 24
		row["LENGTH"] = 4
		row["SCALE"] = nil
		row["RADIX"] = 2
		row["SQL_DATA_TYPE"] = 7
		row["SS_DATA_TYPE"] = 59
	case storage.TypeFloat:
		row["DATA_TYPE"] = 6 // SQL_FLOAT
		row["TYPE_NAME"] = "float"
		row["PRECISION"] = 53
		row["LENGTH"] = 8
		row["SCALE"] = nil
		row["RADIX"] = 2
		row["SQL_DATA_TYPE"] = 6
		row["SS_DATA_TYPE"] = 62
	case storage.TypeBool:
		row["DATA_TYPE"] = -7 // SQL_BIT
		row["TYPE_NAME"] = "bit"
		row["PRECISION"] = 1
		row["LENGTH"] = 1
		row["SCALE"] = nil
		row["RADIX"] = nil
		row["SQL_DATA_TYPE"] = -7
		row["SS_DATA_TYPE"] = 104
	case storage.TypeDateTime:
		row["DATA_TYPE"] = 93 // SQL_TYPE_TIMESTAMP
		row["TYPE_NAME"] = "datetime"
		row["PRECISION"] = 23
		row["LENGTH"] = 16
		row["SCALE"] = 3
		row["RADIX"] = nil
		row["SQL_DATA_TYPE"] = 9
		row["SQL_DATETIME_SUB"] = 3
		row["SS_DATA_TYPE"] = 111
	case storage.TypeDate:
		row["DATA_TYPE"] = 91 // SQL_TYPE_DATE
		row["TYPE_NAME"] = "date"
		row["PRECISION"] = 10
		row["LENGTH"] = 6
		row["SCALE"] = 0
		row["RADIX"] = nil
		row["SQL_DATA_TYPE"] = 9
		row["SQL_DATETIME_SUB"] = 1
		row["SS_DATA_TYPE"] = 40
	case storage.TypeTime:
		row["DATA_TYPE"] = -154 // SQL_SS_TIME2
		row["TYPE_NAME"] = "time"
		row["PRECISION"] = 16
		row["LENGTH"] = 5
		row["SCALE"] = 7
		row["RADIX"] = nil
		row["SQL_DATA_TYPE"] = -154
		row["SS_DATA_TYPE"] = 41
	case storage.TypeDecimal:
		prec := col.Precision
		if prec <= 0 {
			prec = 18
		}
		scale := col.Scale
		row["DATA_TYPE"] = 3 // SQL_DECIMAL
		row["TYPE_NAME"] = "decimal"
		row["PRECISION"] = prec
		row["LENGTH"] = 9
		row["SCALE"] = scale
		row["RADIX"] = 10
		row["SQL_DATA_TYPE"] = 3
		row["SS_DATA_TYPE"] = 108
	case storage.TypeUUID:
		row["DATA_TYPE"] = -11 // SQL_GUID
		row["TYPE_NAME"] = "uniqueidentifier"
		row["PRECISION"] = 36
		row["LENGTH"] = 16
		row["SCALE"] = nil
		row["RADIX"] = nil
		row["SQL_DATA_TYPE"] = -11
		row["SS_DATA_TYPE"] = 36
	case storage.TypeBinary:
		row["DATA_TYPE"] = -3 // SQL_VARBINARY
		row["TYPE_NAME"] = "varbinary"
		row["PRECISION"] = 8000
		row["LENGTH"] = 8000
		row["SCALE"] = nil
		row["RADIX"] = nil
		row["SQL_DATA_TYPE"] = -3
		row["SS_DATA_TYPE"] = 165
	case storage.TypeJSON:
		row["DATA_TYPE"] = 12 // SQL_VARCHAR
		row["TYPE_NAME"] = "nvarchar"
		row["PRECISION"] = 8000
		row["LENGTH"] = 8000
		row["SCALE"] = nil
		row["RADIX"] = nil
		row["SQL_DATA_TYPE"] = 12
		row["SS_DATA_TYPE"] = 231
	default:
		row["DATA_TYPE"] = 12
		row["TYPE_NAME"] = "varchar"
		row["PRECISION"] = 255
		row["LENGTH"] = 255
		row["SCALE"] = nil
		row["RADIX"] = nil
		row["SQL_DATA_TYPE"] = 12
		row["SS_DATA_TYPE"] = 39
	}
	return row
}

func (s *Coordinator) execBuiltinSystemProc(procLower string, rawArgs []string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleanArgs := make([]string, len(rawArgs))
	for i, a := range rawArgs {
		cleanArgs[i] = unwrapSQLString(a)
	}
	args := cleanArgs

	switch procLower {
	case "sp_help":
		if len(args) == 0 {
			// List all database objects
			tables, _ := s.engine.ListTables()
			cols := []string{"Name", "Owner", "Type", "Created_datetime"}
			var rows []map[string]any
			for _, t := range tables {
				rows = append(rows, map[string]any{
					"Name":             t.Name,
					"Owner":            "dbo",
					"Type":             "user table",
					"Created_datetime": "2026-01-01T00:00:00Z",
				})
			}
			if s.catalog != nil {
				for _, p := range s.catalog.ListProcedures() {
					rows = append(rows, map[string]any{
						"Name":             p.Name,
						"Owner":            "dbo",
						"Type":             "stored procedure",
						"Created_datetime": p.CreatedAt.Format(time.RFC3339),
					})
				}
			}
			return &QueryResult{
				Success:      true,
				Command:      "EXEC",
				Columns:      cols,
				Rows:         rows,
				RowsAffected: int64(len(rows)),
				DurationUS:   time.Since(start).Microseconds(),
			}, nil
		}

		objName := cleanIdentifier(args[0])
		tbl, err := s.engine.GetTable(objName)
		if err == nil {
			cols := []string{"Column_name", "Type", "Length", "Nullable", "PrimaryKey"}
			var rows []map[string]any
			for _, c := range tbl.Columns {
				nullableStr := "yes"
				if !c.Nullable {
					nullableStr = "no"
				}
				rows = append(rows, map[string]any{
					"Column_name": c.Name,
					"Type":        c.Type.String(),
					"Length":      255,
					"Nullable":    nullableStr,
					"PrimaryKey":  c.PrimaryKey,
				})
			}
			return &QueryResult{
				Success:      true,
				Command:      "EXEC",
				Columns:      cols,
				Rows:         rows,
				RowsAffected: int64(len(rows)),
				DurationUS:   time.Since(start).Microseconds(),
			}, nil
		}

		if s.catalog != nil {
			procMeta, pErr := s.catalog.GetProcedure(objName)
			if pErr == nil {
				cols := []string{"Parameter_name", "Type", "Length", "Prec", "Param_order"}
				var rows []map[string]any
				for i, p := range procMeta.Params {
					rows = append(rows, map[string]any{
						"Parameter_name": p.Name,
						"Type":           p.Type.String(),
						"Length":         255,
						"Prec":           0,
						"Param_order":    i + 1,
					})
				}
				return &QueryResult{
					Success:      true,
					Command:      "EXEC",
					Columns:      cols,
					Rows:         rows,
					RowsAffected: int64(len(rows)),
					DurationUS:   time.Since(start).Microseconds(),
				}, nil
			}
		}
		return nil, fmt.Errorf("object %q not found", objName)

	case "sp_spaceused":
		if len(args) > 0 {
			objName := cleanIdentifier(args[0])
			tbl, err := s.engine.GetTable(objName)
			if err != nil {
				return nil, fmt.Errorf("table %q not found: %w", objName, err)
			}
			sess, err := s.engine.NewSession()
			if err != nil {
				return nil, err
			}
			defer sess.Close()
			tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
			if err != nil {
				return nil, err
			}
			defer tx.Rollback()

			cur, err := tx.Scan(objName)
			if err != nil {
				return nil, err
			}
			defer cur.Close()
			rowCount := int64(0)
			for cur.Next() {
				rowCount++
			}

			cols := []string{"name", "rows", "reserved", "data", "index_size", "unused"}
			rows := []map[string]any{{
				"name":       tbl.Name,
				"rows":       fmt.Sprintf("%d", rowCount),
				"reserved":   fmt.Sprintf("%d KB", (rowCount*64+1023)/1024),
				"data":       fmt.Sprintf("%d KB", (rowCount*48+1023)/1024),
				"index_size": fmt.Sprintf("%d KB", (rowCount*16+1023)/1024),
				"unused":     "0 KB",
			}}
			return &QueryResult{
				Success:      true,
				Command:      "EXEC",
				Columns:      cols,
				Rows:         rows,
				RowsAffected: 1,
				DurationUS:   time.Since(start).Microseconds(),
			}, nil
		}

		cols := []string{"database_name", "database_size", "unallocated space"}
		rows := []map[string]any{{
			"database_name":     "master",
			"database_size":     "16.00 MB",
			"unallocated space": "4.00 MB",
		}}
		return &QueryResult{
			Success:      true,
			Command:      "EXEC",
			Columns:      cols,
			Rows:         rows,
			RowsAffected: 1,
			DurationUS:   time.Since(start).Microseconds(),
		}, nil

	case "sp_who", "sp_who2":
		cols := []string{"spid", "ecid", "status", "loginame", "hostname", "blk", "dbname", "cmd", "request_id"}
		var rows []map[string]any

		idx := int64(51)
		for _, sess := range s.SnapshotSessions() {
			cmdStr := "AWAITING COMMAND"
			sess.Mu.Lock()
			if sess.Tx != nil {
				cmdStr = "TRANSACTION ACTIVE"
			}
			sessDb := sess.Database
			if sessDb == "" {
				sessDb = "master"
			}
			sess.Mu.Unlock()
			rows = append(rows, map[string]any{
				"spid":       idx,
				"ecid":       int64(0),
				"status":     "runnable",
				"loginame":   "sa",
				"hostname":   "localhost",
				"blk":        "0",
				"dbname":     sessDb,
				"cmd":        cmdStr,
				"request_id": int64(0),
			})
			idx++
		}

		if len(rows) == 0 {
			rows = append(rows, map[string]any{
				"spid":       int64(51),
				"ecid":       int64(0),
				"status":     "runnable",
				"loginame":   "sa",
				"hostname":   "localhost",
				"blk":        "0",
				"dbname":     "master",
				"cmd":        "SELECT",
				"request_id": int64(0),
			})
		}

		return &QueryResult{
			Success:      true,
			Command:      "EXEC",
			Columns:      cols,
			Rows:         rows,
			RowsAffected: int64(len(rows)),
			DurationUS:   time.Since(start).Microseconds(),
		}, nil

	case "sp_lock":
		cols := []string{"spid", "dbid", "objid", "indid", "type", "resource", "mode", "status"}
		rows := []map[string]any{
			{"spid": int64(51), "dbid": int64(1), "objid": int64(0), "indid": int64(0), "type": "DB", "resource": "", "mode": "S", "status": "GRANT"},
		}
		tables, _ := s.engine.ListTables()
		for i, t := range tables {
			rows = append(rows, map[string]any{
				"spid":     int64(51),
				"dbid":     int64(1),
				"objid":    int64(1000 + i),
				"indid":    int64(0),
				"type":     "TAB",
				"resource": t.Name,
				"mode":     "IS",
				"status":   "GRANT",
			})
		}
		return &QueryResult{
			Success:      true,
			Command:      "EXEC",
			Columns:      cols,
			Rows:         rows,
			RowsAffected: int64(len(rows)),
			DurationUS:   time.Since(start).Microseconds(),
		}, nil

	case "sp_rename":
		if len(args) < 2 {
			return nil, fmt.Errorf("sp_rename requires at least old_name and new_name")
		}
		oldName := args[0]
		newName := args[1]
		objType := ""
		if len(args) > 2 {
			objType = strings.ToUpper(args[2])
		}

		if strings.Contains(oldName, ".") || objType == "COLUMN" {
			// Renaming a column: oldName is "table.col"
			parts := strings.SplitN(oldName, ".", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid column rename syntax: expected 'table.column'")
			}
			tableName := cleanIdentifier(parts[0])
			oldColName := cleanIdentifier(parts[1])
			newColName := cleanIdentifier(newName)

			tbl, err := s.engine.GetTable(tableName)
			if err != nil {
				return nil, fmt.Errorf("table %q not found: %w", tableName, err)
			}

			colIdx := -1
			for i, c := range tbl.Columns {
				if strings.EqualFold(c.Name, oldColName) {
					colIdx = i
					break
				}
			}
			if colIdx == -1 {
				return nil, fmt.Errorf("column %q not found in table %q", oldColName, tableName)
			}

			if err := s.engine.AlterTable(tableName, func(sch *storage.TableSchema) error {
				for i := range sch.Columns {
					if strings.EqualFold(sch.Columns[i].Name, oldColName) {
						sch.Columns[i].Name = newColName
						break
					}
				}
				return nil
			}); err != nil {
				return nil, err
			}
			if cs != nil {
				cs.Mu.Lock()
				if cs.Tx != nil {
					cs.ddlUndoLog = append(cs.ddlUndoLog, DDLUndoOp{
						Kind:       DDLUndoRenameColumn,
						TableName:  tableName,
						OldName:    oldColName,
						NewName:    newColName,
					})
				}
				cs.Mu.Unlock()
			}
			s.InvalidatePKBypassCache()

			return &QueryResult{
				Success:    true,
				Command:    "EXEC",
				Messages:   []string{"Caution: Changing any part of an object name could break scripts and stored procedures."},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}

		// Renaming a table: oldName to newName
		oldTblName := cleanIdentifier(oldName)
		newTblName := cleanIdentifier(newName)

		if err := s.engine.RenameTable(oldTblName, newTblName); err != nil {
			return nil, err
		}
		if cs != nil {
			cs.Mu.Lock()
			if cs.Tx != nil {
				cs.ddlUndoLog = append(cs.ddlUndoLog, DDLUndoOp{
					Kind:    DDLUndoRenameTable,
					OldName: oldTblName,
					NewName: newTblName,
				})
			}
			cs.Mu.Unlock()
		}
		s.InvalidatePKBypassCache()

		return &QueryResult{
			Success:    true,
			Command:    "EXEC",
			Messages:   []string{"Caution: Changing any part of an object name could break scripts and stored procedures."},
			DurationUS: time.Since(start).Microseconds(),
		}, nil

		return &QueryResult{
			Success:    true,
			Command:    "EXEC",
			Messages:   []string{"Caution: Changing any part of an object name could break scripts and stored procedures."},
			DurationUS: time.Since(start).Microseconds(),
		}, nil

	case "sp_tables":
		params := parseProcParams(rawArgs, []string{"@table_name", "@table_owner", "@table_qualifier", "@table_type", "@fusepattern"})
		dbName := "master"
		if cs != nil && cs.Database != "" {
			dbName = cs.Database
		}
		if q, ok := params["@table_qualifier"]; ok && q != "" && !strings.EqualFold(q, "null") {
			dbName = cleanTableNameParam(q)
		}

		tblFilter := params["@table_name"]
		typeFilter := params["@table_type"]
		ownerFilter := params["@table_owner"]

		wantTable := true
		wantView := true
		if typeFilter != "" && !strings.EqualFold(typeFilter, "null") {
			upperType := strings.ToUpper(typeFilter)
			wantTable = strings.Contains(upperType, "TABLE") && !strings.Contains(upperType, "SYSTEM TABLE")
			wantView = strings.Contains(upperType, "VIEW")
		}

		matchName := func(name string) bool {
			if tblFilter == "" || tblFilter == "%" || strings.EqualFold(tblFilter, "null") {
				return true
			}
			cleanFilter := cleanTableNameParam(tblFilter)
			if cleanFilter == "" || cleanFilter == "%" {
				return true
			}
			if strings.Contains(cleanFilter, "%") {
				if strings.HasPrefix(cleanFilter, "%") && strings.HasSuffix(cleanFilter, "%") {
					mid := strings.Trim(cleanFilter, "%")
					return strings.Contains(strings.ToLower(name), strings.ToLower(mid))
				}
				if strings.HasPrefix(cleanFilter, "%") {
					suffix := strings.TrimPrefix(cleanFilter, "%")
					return strings.HasSuffix(strings.ToLower(name), strings.ToLower(suffix))
				}
				prefix := strings.TrimSuffix(cleanFilter, "%")
				return strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix))
			}
			return strings.EqualFold(name, cleanFilter)
		}

		matchOwner := func(owner string) bool {
			if ownerFilter == "" || ownerFilter == "%" || strings.EqualFold(ownerFilter, "null") {
				return true
			}
			cleanOwner := cleanTableNameParam(ownerFilter)
			return strings.EqualFold(cleanOwner, owner) || cleanOwner == "%"
		}

		cols := []string{"TABLE_QUALIFIER", "TABLE_OWNER", "TABLE_NAME", "TABLE_TYPE", "REMARKS"}
		var rows []map[string]any

		if wantTable && matchOwner("dbo") {
			tables, _ := s.engine.ListTables()
			for _, t := range tables {
				if matchName(t.Name) {
					rows = append(rows, map[string]any{
						"TABLE_QUALIFIER": dbName,
						"TABLE_OWNER":     "dbo",
						"TABLE_NAME":      t.Name,
						"TABLE_TYPE":      "TABLE",
						"REMARKS":         nil,
					})
				}
			}
		}

		if wantView && matchOwner("dbo") && s.catalog != nil {
			views := s.catalog.ListViews()
			for _, v := range views {
				if matchName(v.Name) {
					rows = append(rows, map[string]any{
						"TABLE_QUALIFIER": dbName,
						"TABLE_OWNER":     "dbo",
						"TABLE_NAME":      v.Name,
						"TABLE_TYPE":      "VIEW",
						"REMARKS":         nil,
					})
				}
			}
		}

		return &QueryResult{
			Success:      true,
			Command:      "EXEC",
			Columns:      cols,
			Rows:         rows,
			RowsAffected: int64(len(rows)),
			DurationUS:   time.Since(start).Microseconds(),
		}, nil

	case "sp_columns":
		params := parseProcParams(rawArgs, []string{"@table_name", "@table_owner", "@table_qualifier", "@column_name", "@odbcver"})
		dbName := "master"
		if cs != nil && cs.Database != "" {
			dbName = cs.Database
		}
		if q, ok := params["@table_qualifier"]; ok && q != "" && !strings.EqualFold(q, "null") {
			dbName = cleanTableNameParam(q)
		}

		tblParam := cleanTableNameParam(params["@table_name"])
		colParam := params["@column_name"]
		ownerFilter := params["@table_owner"]

		cols := []string{
			"TABLE_QUALIFIER",
			"TABLE_OWNER",
			"TABLE_NAME",
			"COLUMN_NAME",
			"DATA_TYPE",
			"TYPE_NAME",
			"PRECISION",
			"LENGTH",
			"SCALE",
			"RADIX",
			"NULLABLE",
			"REMARKS",
			"COLUMN_DEF",
			"SQL_DATA_TYPE",
			"SQL_DATETIME_SUB",
			"CHAR_OCTET_LENGTH",
			"ORDINAL_POSITION",
			"IS_NULLABLE",
			"SS_DATA_TYPE",
		}
		var rows []map[string]any

		matchOwner := func(owner string) bool {
			if ownerFilter == "" || ownerFilter == "%" || strings.EqualFold(ownerFilter, "null") {
				return true
			}
			cleanOwner := cleanTableNameParam(ownerFilter)
			return strings.EqualFold(cleanOwner, owner) || cleanOwner == "%"
		}

		matchCol := func(name string) bool {
			if colParam == "" || colParam == "%" || strings.EqualFold(colParam, "null") {
				return true
			}
			cleanCol := cleanTableNameParam(colParam)
			if cleanCol == "" || cleanCol == "%" {
				return true
			}
			if strings.Contains(cleanCol, "%") {
				if strings.HasPrefix(cleanCol, "%") && strings.HasSuffix(cleanCol, "%") {
					mid := strings.Trim(cleanCol, "%")
					return strings.Contains(strings.ToLower(name), strings.ToLower(mid))
				}
				if strings.HasPrefix(cleanCol, "%") {
					suffix := strings.TrimPrefix(cleanCol, "%")
					return strings.HasSuffix(strings.ToLower(name), strings.ToLower(suffix))
				}
				prefix := strings.TrimSuffix(cleanCol, "%")
				return strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix))
			}
			return strings.EqualFold(name, cleanCol)
		}

		if tblParam != "" && !strings.EqualFold(tblParam, "null") && matchOwner("dbo") {
			tbl, err := s.engine.GetTable(tblParam)
			if err == nil && tbl != nil {
				for i, c := range tbl.Columns {
					if !matchCol(c.Name) {
						continue
					}
					rows = append(rows, mapStorageColumnToSpColumn(c, tbl.Name, dbName, i+1))
				}
			}
		}

		return &QueryResult{
			Success:      true,
			Command:      "EXEC",
			Columns:      cols,
			Rows:         rows,
			RowsAffected: int64(len(rows)),
			DurationUS:   time.Since(start).Microseconds(),
		}, nil

	case "sp_pkeys":
		params := parseProcParams(rawArgs, []string{"@table_name", "@table_owner", "@table_qualifier"})
		dbName := "master"
		if cs != nil && cs.Database != "" {
			dbName = cs.Database
		}
		if q, ok := params["@table_qualifier"]; ok && q != "" && !strings.EqualFold(q, "null") {
			dbName = cleanTableNameParam(q)
		}

		tblParam := cleanTableNameParam(params["@table_name"])
		cols := []string{
			"TABLE_QUALIFIER",
			"TABLE_OWNER",
			"TABLE_NAME",
			"COLUMN_NAME",
			"KEY_SEQ",
			"PK_NAME",
		}
		var rows []map[string]any

		if tblParam != "" && !strings.EqualFold(tblParam, "null") {
			tbl, err := s.engine.GetTable(tblParam)
			if err == nil && tbl != nil {
				seq := 1
				for _, c := range tbl.Columns {
					if c.PrimaryKey {
						rows = append(rows, map[string]any{
							"TABLE_QUALIFIER": dbName,
							"TABLE_OWNER":     "dbo",
							"TABLE_NAME":      tbl.Name,
							"COLUMN_NAME":     c.Name,
							"KEY_SEQ":         seq,
							"PK_NAME":         "PK_" + tbl.Name,
						})
						seq++
					}
				}
			}
		}

		return &QueryResult{
			Success:      true,
			Command:      "EXEC",
			Columns:      cols,
			Rows:         rows,
			RowsAffected: int64(len(rows)),
			DurationUS:   time.Since(start).Microseconds(),
		}, nil

	case "sp_fkeys":
		params := parseProcParams(rawArgs, []string{"@pktable_name", "@pktable_owner", "@pktable_qualifier", "@fktable_name", "@fktable_owner", "@fktable_qualifier"})
		dbName := "master"
		if cs != nil && cs.Database != "" {
			dbName = cs.Database
		}
		pkParam := cleanTableNameParam(params["@pktable_name"])
		fkParam := cleanTableNameParam(params["@fktable_name"])

		cols := []string{
			"PKTABLE_QUALIFIER", "PKTABLE_OWNER", "PKTABLE_NAME", "PKCOLUMN_NAME",
			"FKTABLE_QUALIFIER", "FKTABLE_OWNER", "FKTABLE_NAME", "FKCOLUMN_NAME",
			"KEY_SEQ", "UPDATE_RULE", "DELETE_RULE", "FK_NAME", "PK_NAME", "DEFERRABILITY",
		}
		var rows []map[string]any
		tables, _ := s.engine.ListTables()
		for _, t := range tables {
			for _, fk := range t.ForeignKeys {
				if pkParam != "" && !strings.EqualFold(pkParam, "null") && !strings.EqualFold(fk.ParentTable, pkParam) {
					continue
				}
				if fkParam != "" && !strings.EqualFold(fkParam, "null") && !strings.EqualFold(t.Name, fkParam) {
					continue
				}
				rows = append(rows, map[string]any{
					"PKTABLE_QUALIFIER": dbName,
					"PKTABLE_OWNER":     "dbo",
					"PKTABLE_NAME":      fk.ParentTable,
					"PKCOLUMN_NAME":     strings.Join(fk.ParentColumns, ","),
					"FKTABLE_QUALIFIER": dbName,
					"FKTABLE_OWNER":     "dbo",
					"FKTABLE_NAME":      t.Name,
					"FKCOLUMN_NAME":     strings.Join(fk.ChildColumns, ","),
					"KEY_SEQ":           1,
					"UPDATE_RULE":       3,
					"DELETE_RULE":       3,
					"FK_NAME":           fk.Name,
					"PK_NAME":           "PK_" + fk.ParentTable,
					"DEFERRABILITY":     7,
				})
			}
		}

		return &QueryResult{
			Success:      true,
			Command:      "EXEC",
			Columns:      cols,
			Rows:         rows,
			RowsAffected: int64(len(rows)),
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	return nil, fmt.Errorf("unrecognized system stored procedure: %s", procLower)
}

// extractExecReturnVarAndOutputArgs extracts optional @return_var, procName, and per-arg @out_var OUTPUT bindings from raw EXEC query.
func extractExecReturnVarAndOutputArgs(raw string) (string, string, []string) {
	raw = strings.TrimSpace(raw)
	if rem, ok := sqlutil.TrimAnyPrefixKeyword(raw, "EXECUTE", "EXEC", "CALL"); ok {
		raw = rem
	}
	returnVar := ""
	if strings.HasPrefix(raw, "@") {
		if eqIdx := sqlutil.FindTopLevelChar(raw, '='); eqIdx != -1 {
			candidate := strings.TrimSpace(raw[:eqIdx])
			if !strings.ContainsAny(candidate, " \t,()") {
				returnVar = candidate
				raw = strings.TrimSpace(raw[eqIdx+1:])
			}
		}
	}
	rawProcName, rawArgs := ParseProcArgs("EXEC " + raw)
	return returnVar, rawProcName, rawArgs
}

func stripTrailingOutputKeyword(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	fields := strings.Fields(trimmed)
	if len(fields) >= 2 {
		last := fields[len(fields)-1]
		if strings.EqualFold(last, "OUTPUT") || strings.EqualFold(last, "OUT") {
			idx := strings.LastIndex(trimmed, last)
			if idx > 0 {
				return strings.TrimSpace(trimmed[:idx]), true
			}
		}
	}
	return trimmed, false
}

func (s *Coordinator) execProcedureCall(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	return s.execProcedureCallWithRaw(query, query, start, cs)
}

func (s *Coordinator) execProcedureCallWithRaw(query string, rawQuery string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	returnVar, rawProcName, rawArgs := extractExecReturnVarAndOutputArgs(rawQuery)
	procName, args := ParseProcArgs(query)
	if returnVar != "" {
		// When rawQuery had `EXEC @rc = procName ...`, variable substitution on `query`
		// may have replaced `@rc` with `NULL` or a literal (e.g. `EXEC NULL = procName ...`).
		// Strip the leading `... =` from `query` before parsing procName and args.
		trimmedQ := strings.TrimSpace(query)
		if rem, ok := sqlutil.TrimAnyPrefixKeyword(trimmedQ, "EXECUTE", "EXEC", "CALL"); ok {
			if eqIdx := sqlutil.FindTopLevelChar(rem, '='); eqIdx != -1 {
				procName, args = ParseProcArgs("EXEC " + strings.TrimSpace(rem[eqIdx+1:]))
			}
		}
		if procName == "" && rawProcName != "" {
			procName = rawProcName
		}
	}

	ctx := cs.GetQueryContext()

	// 1. Dynamic SQL execution: EXEC ('SELECT ...') or EXEC (@sqlVar)
	if procName == "" && len(args) > 0 {
		return s.execDynamicStringExec(args, start, cs, ctx)
	}

	if procName == "" {
		return nil, fmt.Errorf("invalid EXEC syntax")
	}

	procLower := strings.ToLower(procName)
	switch procLower {
	case "sp_executesql":
		return s.execSpExecuteSQL(args, start, cs, ctx)
	case "sp_send_dbmail":
		return s.execSpSendDbMail(args, rawArgs, start, cs)
	case "sp_babelfish_configure":
		// Tolerant virtual No-Op for Babelfish environment configuration
		return &QueryResult{
			Success:    true,
			Command:    "EXEC",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"Babelfish configuration synchronized"},
		}, nil
	case "sp_syspolicy_configure", "sp_syspolicy_set_config_enabled", "sp_syspolicy_set_config_history_retention":
		// Tolerant virtual No-Op for SSMS Policy-Based Management configuration
		return &QueryResult{
			Success:    true,
			Command:    "EXEC",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"Policy-Based Management configuration synchronized"},
		}, nil
	case "sp_help", "sp_spaceused", "sp_who", "sp_who2", "sp_lock", "sp_rename", "sp_tables", "sp_columns", "sp_pkeys", "sp_fkeys":
		return s.execBuiltinSystemProc(procLower, args, start, cs)
	}

	// Check if this is a prepared statement in the active session
	if cs != nil {
		cs.Mu.Lock()
		prep, isPrep := cs.PreparedStmts[procLower]
		cs.Mu.Unlock()
		if isPrep && prep != nil {
			return s.execPreparedStatement(prep, args, start, cs, ctx)
		}
	}

	if len(args) > 0 && sqlutil.HasPrefixKeyword(args[0], "USING") {
		return nil, fmt.Errorf("prepared statement %q not found", procName)
	}

	procMeta, err := s.catalog.GetProcedure(procName)
	if err != nil {
		return nil, fmt.Errorf("procedure %q not found: %w", procName, err)
	}

	prog, found := s.executor.Cache().GetByName(procName)
	if !found {
		prog, err = s.executor.Cache().GetOrCompile(procMeta.Body)
		if err != nil {
			return nil, fmt.Errorf("bytecode compilation failed: %w", err)
		}
		s.executor.Cache().PutByName(procName, prog)
	}

	argMap := make(map[string]any)
	outputBindings := make(map[string]string) // targetParam -> caller @varName
	for i, arg := range args {
		arg = strings.TrimSpace(arg)
		targetParam := ""
		var paramType storage.DataType = storage.TypeUnknown
		valStr := arg
		if eqIdx := strings.IndexByte(arg, '='); eqIdx != -1 {
			k := strings.TrimSpace(arg[:eqIdx])
			v := strings.TrimSpace(arg[eqIdx+1:])
			k = strings.TrimPrefix(k, "@")
			for _, p := range procMeta.Params {
				if strings.EqualFold(strings.TrimPrefix(p.Name, "@"), k) {
					targetParam = p.Name
					paramType = p.Type
					break
				}
			}
			if targetParam == "" {
				return nil, fmt.Errorf("Error 8145, Level 16, State 1, Procedure %s: @%s is not a parameter for procedure %s.", procName, k, procName)
			}
			valStr = v
		} else if i < len(procMeta.Params) {
			targetParam = procMeta.Params[i].Name
			paramType = procMeta.Params[i].Type
		} else {
			return nil, fmt.Errorf("Error 8144, Level 16, State 2, Procedure %s: Procedure or function %s has too many arguments specified.", procName, procName)
		}

		valStr, isOut := stripTrailingOutputKeyword(valStr)
		if i < len(rawArgs) {
			rawArg := strings.TrimSpace(rawArgs[i])
			if eqIdx := strings.IndexByte(rawArg, '='); eqIdx != -1 {
				rawArg = strings.TrimSpace(rawArg[eqIdx+1:])
			}
			rawValExpr, rawIsOut := stripTrailingOutputKeyword(rawArg)
			if (isOut || rawIsOut) && strings.HasPrefix(rawValExpr, "@") && targetParam != "" {
				outputBindings[targetParam] = rawValExpr
			}
		}

		if targetParam != "" {
			isQuoted := strings.HasPrefix(valStr, "'") || strings.HasPrefix(valStr, "N'") || strings.HasPrefix(valStr, "n'") || strings.HasPrefix(valStr, "\"")
			if !isQuoted && strings.EqualFold(valStr, "NULL") {
				argMap[targetParam] = nil
				continue
			}
			if !isQuoted && strings.EqualFold(valStr, "DEFAULT") {
				var hasDefault bool
				if prog != nil {
					cleanTarget := strings.ToLower(strings.TrimPrefix(targetParam, "@"))
					for _, pd := range prog.Params {
						if strings.ToLower(strings.TrimPrefix(pd.Name, "@")) == cleanTarget {
							if pd.Default != nil {
								hasDefault = true
							}
							break
						}
					}
				}
				if !hasDefault {
					return nil, fmt.Errorf("The DEFAULT keyword cannot be used as an argument to procedure %q because the parameter %q has no default value.", procName, targetParam)
				}
				continue
			}
			raw := unwrapSQLString(valStr)
			if isQuoted || paramType == storage.TypeVarChar {
				argMap[targetParam] = raw
			} else if num, err := strconv.ParseInt(raw, 10, 64); err == nil {
				argMap[targetParam] = num
			} else if f, err := strconv.ParseFloat(raw, 64); err == nil {
				argMap[targetParam] = f
			} else {
				argMap[targetParam] = raw
			}
		}
	}

	res, txAborted, err := cs.ExecuteProgram(ctx, prog, s.executor, argMap, s.engine, procName)
	if txAborted {
		return nil, errors.New("current transaction is aborted, commands ignored until end of transaction block")
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
			return nil, fmt.Errorf("canceling statement due to user request")
		}
		return nil, fmt.Errorf("procedure execution error: %w", err)
	}

	if res != nil && cs != nil {
		cs.Mu.Lock()
		if cs.Vars == nil {
			cs.Vars = make(map[string]any)
		}
		if res.LastIdentity > 0 {
			cs.Vars["identity"] = res.LastIdentity
			cs.Vars["@@identity"] = res.LastIdentity
		}
		if returnVar != "" {
			retVal := res.ReturnValue
			if retVal == nil {
				retVal = int64(0)
			}
			cs.Vars[returnVar] = retVal
			cs.Vars[strings.ToLower(returnVar)] = retVal
		}
		if len(outputBindings) > 0 && res.Vars != nil {
			for targetParam, callerVar := range outputBindings {
				cleanParam := strings.ToLower(strings.TrimPrefix(targetParam, "@"))
				if outVal, ok := res.Vars[cleanParam]; ok {
					cs.Vars[callerVar] = outVal
				} else if outVal, ok := res.Vars[targetParam]; ok {
					cs.Vars[callerVar] = outVal
				}
			}
		}
		cs.Mu.Unlock()
	}

	messages := make([]string, 0)
	if len(res.Logs) > 0 {
		messages = append(messages, res.Logs...)
	}
	messages = append(messages, fmt.Sprintf("Return code: %v", res.ReturnValue))

	if len(res.ResultSets) > 1 {
		var headRes *QueryResult
		var tailRes *QueryResult
		for i, rs := range res.ResultSets {
			qr := &QueryResult{
				Success:         true,
				Command:         "EXEC",
				Columns:         rs.Columns,
				Rows:            rs.Rows,
				Values:          rs.Values,
				RowsAffected:    int64(len(rs.Rows)),
				DurationUS:      time.Since(start).Microseconds(),
				Messages:        messages,
				ReturnStatus:    ReturnStatusFromValue(res.ReturnValue),
				HasReturnStatus: true,
			}
			if i == 0 {
				headRes = qr
				tailRes = qr
			} else {
				tailRes.Next = qr
				tailRes = qr
			}
		}
		return headRes, nil
	}

	rowsAffected := res.RowsAffected
	if rowsAffected == 0 && len(res.Rows) > 0 {
		rowsAffected = int64(len(res.Rows))
	}

	return &QueryResult{
		Success:         true,
		Command:         "EXEC",
		Columns:         res.Columns,
		Rows:            res.Rows,
		Values:          res.Values,
		RowsAffected:    rowsAffected,
		DurationUS:      time.Since(start).Microseconds(),
		Messages:        messages,
		ReturnStatus:    ReturnStatusFromValue(res.ReturnValue),
		HasReturnStatus: true,
	}, nil
}

func splitConcatenationOutsideQuotes(s string) []string {
	parts := sqlutil.SplitTopLevel(s, '+')
	if len(parts) == 0 && s != "" {
		return []string{s}
	}
	return parts
}

func evalDynamicSQLExpression(raw string, cs *ClientSession) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	parts := splitConcatenationOutsideQuotes(raw)
	var b strings.Builder
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "@") && cs != nil {
			varName := strings.ToLower(part)
			cs.Mu.Lock()
			if v, ok := cs.Vars[varName]; ok && v != nil {
				b.WriteString(fmt.Sprintf("%v", v))
			} else if v, ok := cs.Vars[strings.TrimPrefix(varName, "@")]; ok && v != nil {
				b.WriteString(fmt.Sprintf("%v", v))
			}
			cs.Mu.Unlock()
		} else {
			b.WriteString(unwrapSQLString(part))
		}
	}
	return b.String()
}

func (s *Coordinator) execDynamicStringExec(args []string, start time.Time, cs *ClientSession, ctx context.Context) (*QueryResult, error) {
	var evaluatedParts []string
	for _, arg := range args {
		val := evalDynamicSQLExpression(arg, cs)
		if val != "" {
			evaluatedParts = append(evaluatedParts, val)
		}
	}
	dynamicSQL := strings.TrimSpace(strings.Join(evaluatedParts, " "))
	if dynamicSQL == "" {
		return &QueryResult{Success: true, Command: "EXEC", DurationUS: time.Since(start).Microseconds()}, nil
	}

	nestedCtx := context.WithValue(ctx, reentrantQueryKey{}, true)
	if cs != nil {
		return s.ExecuteSessionSQLContext(nestedCtx, cs.ID, dynamicSQL)
	}
	return s.ExecuteSessionSQLContext(nestedCtx, "default", dynamicSQL)
}

func findTopLevelEquals(s string) int {
	return sqlutil.FindTopLevelChar(s, '=')
}

func unwrapOrVar(val string, cs *ClientSession) string {
	val = strings.TrimSpace(val)
	if strings.HasPrefix(val, "@") && cs != nil {
		cs.Mu.Lock()
		defer cs.Mu.Unlock()
		varName := strings.ToLower(val)
		if v, ok := cs.Vars[varName]; ok && v != nil {
			return fmt.Sprintf("%v", v)
		}
		if v, ok := cs.Vars[strings.TrimPrefix(varName, "@")]; ok && v != nil {
			return fmt.Sprintf("%v", v)
		}
	}
	return unwrapSQLString(val)
}

func cleanOutputSuffix(s string) string {
	s = strings.TrimSpace(s)
	upper := strings.ToUpper(s)
	if strings.HasSuffix(upper, "OUTPUT") && len(s) > 6 {
		ch := s[len(s)-7]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			return strings.TrimSpace(s[:len(s)-6])
		}
	}
	if strings.HasSuffix(upper, "OUT") && len(s) > 3 {
		ch := s[len(s)-4]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			return strings.TrimSpace(s[:len(s)-3])
		}
	}
	return s
}

// extractArgValue extracts the value expression from an argument,
// stripping any named prefix (e.g. "@stmt = N'...'" -> "N'...'") if present.
func extractArgValue(arg string, cs *ClientSession) string {
	arg = strings.TrimSpace(arg)
	if eqIdx := findTopLevelEquals(arg); eqIdx != -1 {
		arg = strings.TrimSpace(arg[eqIdx+1:])
	}
	return unwrapOrVar(arg, cs)
}

func (s *Coordinator) execSpExecuteSQL(args []string, start time.Time, cs *ClientSession, ctx context.Context) (*QueryResult, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("sp_executesql requires at least 1 parameter (@stmt)")
	}

	// Positional Slot 0: Statement text (regardless of parameter name: @stmt, @statement, @query, or positional)
	stmt := extractArgValue(args[0], cs)

	var paramsDecl string
	namedArgs := make(map[string]string)
	var posArgs []string

	valArgs := args[1:]
	if len(valArgs) > 0 {
		firstVal := strings.TrimSpace(valArgs[0])
		eqIdx := findTopLevelEquals(firstVal)
		isParamsDecl := false

		if eqIdx == -1 {
			// Positional second argument is the parameter declaration string
			isParamsDecl = true
		} else {
			lhs := strings.ToLower(strings.TrimSpace(firstVal[:eqIdx]))
			// If named, check if it explicitly designates the parameters definition
			if lhs == "@params" || lhs == "@parameters" || strings.Contains(lhs, "param") {
				isParamsDecl = true
			}
		}

		if isParamsDecl {
			paramsDecl = extractArgValue(firstVal, cs)
			valArgs = valArgs[1:]
		}
	}

	// Remaining arguments are bind parameters (named or positional)
	for _, arg := range valArgs {
		arg = strings.TrimSpace(arg)
		if eqIdx := findTopLevelEquals(arg); eqIdx != -1 {
			lhs := strings.TrimSpace(arg[:eqIdx])
			rhs := strings.TrimSpace(arg[eqIdx+1:])
			lhsLower := strings.ToLower(lhs)
			cleanRHS := cleanOutputSuffix(rhs)
			namedArgs[lhsLower] = formatBoundArgValue(cleanRHS, cs)
		} else {
			cleanArg := cleanOutputSuffix(arg)
			posArgs = append(posArgs, formatBoundArgValue(cleanArg, cs))
		}
	}

	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return &QueryResult{Success: true, Command: "EXEC", DurationUS: time.Since(start).Microseconds()}, nil
	}

	boundSQL := buildSpExecuteSQL(stmt, paramsDecl, namedArgs, posArgs)

	nestedCtx := context.WithValue(ctx, reentrantQueryKey{}, true)
	var res *QueryResult
	var err error
	if cs != nil {
		res, err = s.ExecuteSessionSQLContext(nestedCtx, cs.ID, boundSQL)
	} else {
		res, err = s.ExecuteSessionSQLContext(nestedCtx, "default", boundSQL)
	}
	if err != nil {
		return nil, err
	}
	if res != nil {
		res.DurationUS = time.Since(start).Microseconds()
	}
	return res, nil
}

type spParamDecl struct {
	Name string
	Type string
}

func parseSpParamDeclarations(declStr string) []spParamDecl {
	declStr = strings.TrimSpace(declStr)
	if declStr == "" {
		return nil
	}
	parts := sqlutil.SplitTopLevel(declStr, ',')
	var decls []spParamDecl
	for _, p := range parts {
		p = strings.TrimSpace(p)
		tokens := sqlutil.SplitTopLevelTokens(p)
		if len(tokens) >= 1 {
			name := tokens[0]
			tp := ""
			if len(tokens) >= 2 {
				tp = strings.Join(tokens[1:], " ")
			}
			decls = append(decls, spParamDecl{
				Name: name,
				Type: tp,
			})
		}
	}
	return decls
}

func formatBoundArgValue(rawVal string, cs *ClientSession) string {
	rawVal = strings.TrimSpace(rawVal)
	if strings.HasPrefix(rawVal, "@") && cs != nil {
		cs.Mu.Lock()
		v, ok := cs.Vars[strings.ToLower(rawVal)]
		if !ok {
			v, ok = cs.Vars[strings.ToLower(strings.TrimPrefix(rawVal, "@"))]
		}
		cs.Mu.Unlock()
		if ok {
			if v == nil {
				return "NULL"
			}
			switch val := v.(type) {
			case string:
				return "'" + strings.ReplaceAll(val, "'", "''") + "'"
			case bool:
				if val {
					return "1"
				}
				return "0"
			case int, int64, int32, float64, float32:
				return fmt.Sprintf("%v", val)
			default:
				return fmt.Sprintf("%v", val)
			}
		}
	}

	if (strings.HasPrefix(rawVal, "N'") || strings.HasPrefix(rawVal, "n'")) && strings.HasSuffix(rawVal, "'") && len(rawVal) >= 3 {
		inner := rawVal[2 : len(rawVal)-1]
		return "'" + strings.ReplaceAll(inner, "''", "'") + "'"
	}
	return rawVal
}

func buildSpExecuteSQL(stmt string, paramsDecl string, namedArgs map[string]string, posArgs []string) string {
	stmt = strings.TrimSpace(stmt)
	if len(namedArgs) == 0 && len(posArgs) == 0 && strings.TrimSpace(paramsDecl) == "" {
		return stmt
	}

	decls := parseSpParamDeclarations(paramsDecl)
	allBindings := make(map[string]string)

	// 1. Map positional parameters to declaration order
	for i, posVal := range posArgs {
		if i < len(decls) {
			pName := strings.ToLower(decls[i].Name)
			allBindings[pName] = posVal
		}
	}

	// 2. Map named parameters (overriding or supplementing positional)
	for pName, val := range namedArgs {
		allBindings[strings.ToLower(pName)] = val
	}

	// 3. Substitute parameters in statement outside string literals via deterministic Single-Pass scanner (Anti-Cascading P0-31)
	return substituteParamsSinglePass(stmt, allBindings)
}

func substituteParamsSinglePass(query string, bindings map[string]string) string {
	return sqlutil.SubstituteParamsSinglePass(query, bindings)
}

func substituteParamOutsideQuotes(query string, paramName string, formattedVal string) string {
	return sqlutil.SubstituteParamOutsideQuotes(query, paramName, formattedVal)
}

// execAnonymousBlock compiles and executes ad-hoc anonymous control-flow statements (IF...ELSE, WHILE, BEGIN...END).
func (s *Coordinator) execAnonymousBlock(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	trimmed := strings.TrimSpace(query)

	prog, err := proc.CompileAnonymousBlock(trimmed)
	if err != nil {
		return nil, fmt.Errorf("compile anonymous block error: %w", err)
	}

	ctx := context.Background()
	if cs != nil {
		cs.Mu.Lock()
		if cs.QueryCtx != nil {
			ctx = cs.QueryCtx
		}
		cs.Mu.Unlock()
	}

	params := make(map[string]any)
	if cs != nil {
		cs.Mu.Lock()
		for k, v := range cs.Vars {
			clean := strings.TrimPrefix(k, "@")
			if !strings.HasPrefix(k, "@") {
				params[clean] = v
				params["@"+clean] = v
			}
		}
		for k, v := range cs.Vars {
			if strings.HasPrefix(k, "@") {
				clean := strings.TrimPrefix(k, "@")
				params[clean] = v
				params["@"+clean] = v
			}
		}
		cs.Mu.Unlock()
	}

	res, txAborted, err := cs.ExecuteProgram(ctx, prog, s.executor, params, s.engine, "AnonymousBlock")
	if txAborted {
		return nil, errors.New("current transaction is aborted, commands ignored until end of transaction block")
	}
	if err != nil {
		return nil, fmt.Errorf("execute anonymous block error: %w", err)
	}

	if cs != nil && res.Vars != nil {
		cs.Mu.Lock()
		for k, v := range res.Vars {
			clean := strings.TrimPrefix(k, "@")
			cs.Vars["@"+clean] = v
			delete(cs.Vars, clean)
		}
		cs.Mu.Unlock()
	}

	rowsAffected := res.RowsAffected
	if rowsAffected == 0 && len(res.Rows) > 0 {
		rowsAffected = int64(len(res.Rows))
	}

	command := "BLOCK"
	if sqlutil.HasPrefixKeyword(trimmed, "IF") {
		command = "IF"
	} else if sqlutil.HasPrefixKeyword(trimmed, "WHILE") {
		command = "WHILE"
	}

	return &QueryResult{
		Success:      true,
		Command:      command,
		Columns:      res.Columns,
		Rows:         res.Rows,
		RowsAffected: rowsAffected,
		Messages:     res.Logs,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}
