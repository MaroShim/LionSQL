package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
	upper := strings.ToUpper(raw)
	if strings.HasPrefix(upper, "EXEC ") {
		raw = strings.TrimSpace(raw[4:])
	} else if strings.HasPrefix(upper, "EXECUTE ") {
		raw = strings.TrimSpace(raw[7:])
	} else if strings.HasPrefix(upper, "CALL ") {
		raw = strings.TrimSpace(raw[4:])
	} else if strings.HasPrefix(upper, "EXEC(") {
		raw = strings.TrimSpace(raw[4:])
		raw = "(" + raw
	} else if strings.HasPrefix(upper, "EXECUTE(") {
		raw = strings.TrimSpace(raw[7:])
		raw = "(" + raw
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

	var args []string
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(rem); i++ {
		ch := rem[i]
		if !inQuote {
			if ch == '\'' || ch == '"' {
				inQuote = true
				quoteChar = ch
				cur.WriteByte(ch)
				continue
			}
			if (ch == 'N' || ch == 'n') && i+1 < len(rem) && rem[i+1] == '\'' {
				inQuote = true
				quoteChar = '\''
				cur.WriteByte(ch)
				cur.WriteByte('\'')
				i++
				continue
			}
			if ch == ',' {
				args = append(args, strings.TrimSpace(cur.String()))
				cur.Reset()
				continue
			}
			cur.WriteByte(ch)
		} else {
			if ch == quoteChar {
				if i+1 < len(rem) && rem[i+1] == quoteChar {
					cur.WriteByte(ch)
					cur.WriteByte(ch)
					i++
					continue
				}
				inQuote = false
				cur.WriteByte(ch)
				continue
			}
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		args = append(args, strings.TrimSpace(cur.String()))
	}

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

			// Read all records
			sess, err := s.engine.NewSession()
			if err != nil {
				return nil, err
			}
			tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
			if err != nil {
				sess.Close()
				return nil, err
			}

			cur, err := tx.Scan(tableName)
			if err != nil {
				_ = tx.Rollback()
				sess.Close()
				return nil, err
			}
			var records []storage.Record
			for cur.Next() {
				r, recErr := cur.Record()
				if recErr != nil {
					continue
				}
				newRec := make(storage.Record, len(r))
				for k, v := range r {
					if strings.EqualFold(k, oldColName) {
						newRec[newColName] = v
					} else {
						newRec[k] = v
					}
				}
				records = append(records, newRec)
			}
			cur.Close()
			_ = tx.Rollback()
			sess.Close()

			// Update schema
			newCols := make([]storage.Column, len(tbl.Columns))
			copy(newCols, tbl.Columns)
			newCols[colIdx].Name = newColName

			newSchema := &storage.TableSchema{
				Name:    tbl.Name,
				Columns: newCols,
				Indexes: tbl.Indexes,
			}

			if err := s.engine.DropTable(tableName); err != nil {
				return nil, err
			}
			if err := s.engine.CreateTable(newSchema); err != nil {
				return nil, err
			}
			insSess, err := s.engine.NewSession()
			if err != nil {
				return nil, err
			}
			defer insSess.Close()
			insTx, err := insSess.BeginTx(storage.TxOptions{})
			if err != nil {
				return nil, err
			}
			for _, rec := range records {
				if err := insTx.Insert(tableName, rec); err != nil {
					_ = insTx.Rollback()
					return nil, err
				}
			}
			if err := insTx.Commit(); err != nil {
				return nil, err
			}

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

		tbl, err := s.engine.GetTable(oldTblName)
		if err != nil {
			return nil, fmt.Errorf("table %q not found: %w", oldTblName, err)
		}
		if _, err := s.engine.GetTable(newTblName); err == nil {
			return nil, fmt.Errorf("table %q already exists", newTblName)
		}

		sess, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			sess.Close()
			return nil, err
		}

		cur, err := tx.Scan(oldTblName)
		if err != nil {
			_ = tx.Rollback()
			sess.Close()
			return nil, err
		}
		var records []storage.Record
		for cur.Next() {
			if r, recErr := cur.Record(); recErr == nil {
				records = append(records, r)
			}
		}
		cur.Close()
		_ = tx.Rollback()
		sess.Close()

		newSchema := &storage.TableSchema{
			Name:    newTblName,
			Columns: tbl.Columns,
			Indexes: tbl.Indexes,
		}

		if err := s.engine.DropTable(oldTblName); err != nil {
			return nil, err
		}
		if err := s.engine.CreateTable(newSchema); err != nil {
			return nil, err
		}
		insSess, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer insSess.Close()
		insTx, err := insSess.BeginTx(storage.TxOptions{})
		if err != nil {
			return nil, err
		}
		for _, rec := range records {
			if err := insTx.Insert(newTblName, rec); err != nil {
				_ = insTx.Rollback()
				return nil, err
			}
		}
		if err := insTx.Commit(); err != nil {
			return nil, err
		}

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
	}

	return nil, fmt.Errorf("unrecognized system stored procedure: %s", procLower)
}

func (s *Coordinator) execProcedureCall(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	procName, args := ParseProcArgs(query)

	ctx := context.Background()
	if cs != nil && cs.QueryCtx != nil {
		ctx = cs.QueryCtx
	}

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
	case "sp_help", "sp_spaceused", "sp_who", "sp_who2", "sp_lock", "sp_rename", "sp_tables", "sp_columns":
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

	if len(args) > 0 && strings.HasPrefix(strings.ToUpper(args[0]), "USING ") {
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

	session, err := s.engine.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	argMap := make(map[string]any)
	for i, arg := range args {
		arg = strings.TrimSpace(arg)
		targetParam := ""
		valStr := arg
		if eqIdx := strings.IndexByte(arg, '='); eqIdx != -1 {
			k := strings.TrimSpace(arg[:eqIdx])
			v := strings.TrimSpace(arg[eqIdx+1:])
			k = strings.TrimPrefix(k, "@")
			for _, p := range procMeta.Params {
				if strings.EqualFold(strings.TrimPrefix(p.Name, "@"), k) {
					targetParam = p.Name
					break
				}
			}
			valStr = v
		} else if i < len(procMeta.Params) {
			targetParam = procMeta.Params[i].Name
		}

		if targetParam != "" {
			raw := unwrapSQLString(valStr)
			if num, err := strconv.ParseInt(raw, 10, 64); err == nil {
				argMap[targetParam] = num
			} else if f, err := strconv.ParseFloat(raw, 64); err == nil {
				argMap[targetParam] = f
			} else {
				argMap[targetParam] = raw
			}
		}
	}

	res, err := s.executor.RunContext(ctx, prog, procName, argMap, session)
	if err != nil {
		if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
			return nil, fmt.Errorf("canceling statement due to user request")
		}
		return nil, fmt.Errorf("procedure execution error: %w", err)
	}

	messages := make([]string, 0)
	if len(res.Logs) > 0 {
		messages = append(messages, res.Logs...)
	}
	messages = append(messages, fmt.Sprintf("Return code: %v", res.ReturnValue))

	rowsAffected := res.RowsAffected
	if rowsAffected == 0 && len(res.Rows) > 0 {
		rowsAffected = int64(len(res.Rows))
	}

	return &QueryResult{
		Success:      true,
		Command:      "EXEC",
		Columns:      res.Columns,
		Rows:         res.Rows,
		RowsAffected: rowsAffected,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     messages,
	}, nil
}

func splitConcatenationOutsideQuotes(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !inQuote {
			if ch == '\'' || ch == '"' {
				inQuote = true
				quoteChar = ch
				cur.WriteByte(ch)
				continue
			}
			if (ch == 'N' || ch == 'n') && i+1 < len(s) && s[i+1] == '\'' {
				inQuote = true
				quoteChar = '\''
				cur.WriteByte(ch)
				cur.WriteByte('\'')
				i++
				continue
			}
			if ch == '+' {
				parts = append(parts, cur.String())
				cur.Reset()
				continue
			}
			cur.WriteByte(ch)
		} else {
			if ch == quoteChar {
				if i+1 < len(s) && s[i+1] == quoteChar {
					cur.WriteByte(ch)
					cur.WriteByte(ch)
					i++
					continue
				}
				inQuote = false
			}
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	if len(parts) == 0 {
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

	if cs != nil {
		return s.ExecuteSessionSQLContext(ctx, cs.ID, dynamicSQL)
	}
	return s.ExecuteSQL(dynamicSQL)
}

func findTopLevelEquals(s string) int {
	inQuote := false
	quoteChar := byte(0)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !inQuote {
			if ch == '\'' || ch == '"' {
				inQuote = true
				quoteChar = ch
				continue
			}
			if (ch == 'N' || ch == 'n') && i+1 < len(s) && s[i+1] == '\'' {
				inQuote = true
				quoteChar = '\''
				i++
				continue
			}
			if ch == '=' {
				return i
			}
		} else {
			if ch == quoteChar {
				if i+1 < len(s) && s[i+1] == quoteChar {
					i++
					continue
				}
				inQuote = false
			}
		}
	}
	return -1
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
	if strings.HasSuffix(upper, "OUTPUT") {
		return strings.TrimSpace(s[:len(s)-6])
	}
	if strings.HasSuffix(upper, "OUT") {
		return strings.TrimSpace(s[:len(s)-3])
	}
	return s
}

func (s *Coordinator) execSpExecuteSQL(args []string, start time.Time, cs *ClientSession, ctx context.Context) (*QueryResult, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("sp_executesql requires at least 1 parameter (@stmt)")
	}

	var stmt string
	var paramsDecl string
	namedArgs := make(map[string]string)
	var posArgs []string

	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if eqIdx := findTopLevelEquals(arg); eqIdx != -1 {
			lhs := strings.TrimSpace(arg[:eqIdx])
			rhs := strings.TrimSpace(arg[eqIdx+1:])
			lhsLower := strings.ToLower(lhs)

			switch lhsLower {
			case "@stmt", "@statement":
				stmt = unwrapOrVar(rhs, cs)
			case "@params", "@parameters":
				paramsDecl = unwrapOrVar(rhs, cs)
			default:
				cleanRHS := cleanOutputSuffix(rhs)
				namedArgs[lhsLower] = formatBoundArgValue(cleanRHS, cs)
			}
		} else {
			// Positional parameter
			if stmt == "" {
				stmt = unwrapOrVar(arg, cs)
			} else if paramsDecl == "" {
				paramsDecl = unwrapOrVar(arg, cs)
			} else {
				cleanArg := cleanOutputSuffix(arg)
				posArgs = append(posArgs, formatBoundArgValue(cleanArg, cs))
			}
		}
	}

	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return &QueryResult{Success: true, Command: "EXEC", DurationUS: time.Since(start).Microseconds()}, nil
	}

	boundSQL := buildSpExecuteSQL(stmt, paramsDecl, namedArgs, posArgs)

	var res *QueryResult
	var err error
	if cs != nil {
		res, err = s.ExecuteSessionSQLContext(ctx, cs.ID, boundSQL)
	} else {
		res, err = s.ExecuteSQL(boundSQL)
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

	// 3. Substitute parameters in statement outside string literals
	for pName, formattedVal := range allBindings {
		stmt = substituteParamOutsideQuotes(stmt, pName, formattedVal)
	}

	return stmt
}

func substituteParamOutsideQuotes(query string, paramName string, formattedVal string) string {
	if !strings.HasPrefix(paramName, "@") {
		paramName = "@" + paramName
	}
	re := getCompiledRegexp(`(?i)^` + regexp.QuoteMeta(paramName) + `\b`)

	var b strings.Builder
	inString := false
	var quoteChar rune
	runes := []rune(query)

	for i := 0; i < len(runes); {
		ch := runes[i]
		if inString {
			b.WriteRune(ch)
			if ch == quoteChar {
				if i+1 < len(runes) && runes[i+1] == quoteChar {
					b.WriteRune(runes[i+1])
					i += 2
					continue
				}
				inString = false
			}
			i++
		} else {
			if ch == '\'' || ch == '"' {
				inString = true
				quoteChar = ch
				b.WriteRune(ch)
				i++
			} else if ch == '@' {
				isDoubleAtParam := strings.HasPrefix(paramName, "@@")
				prevIsAt := i > 0 && runes[i-1] == '@'
				nextIsAt := i+1 < len(runes) && runes[i+1] == '@'
				if !isDoubleAtParam && (prevIsAt || nextIsAt) {
					b.WriteRune(ch)
					i++
					continue
				}
				remainder := string(runes[i:])
				loc := re.FindStringIndex(remainder)
				if len(loc) == 2 && loc[0] == 0 {
					b.WriteString(formattedVal)
					matchedRunes := len([]rune(remainder[:loc[1]]))
					i += matchedRunes
				} else {
					b.WriteRune(ch)
					i++
				}
			} else {
				b.WriteRune(ch)
				i++
			}
		}
	}
	return b.String()
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

	var tx storage.Transaction
	var createdSession storage.Session
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
	} else {
		sess, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		createdSession = sess
		defer sess.Close()

		t, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			return nil, err
		}
		tx = t
		defer func() {
			if tx != nil {
				_ = tx.Rollback()
			}
		}()
	}

	res, err := s.executor.ExecuteInTxContext(ctx, prog, params, tx, nil)
	if err != nil {
		return nil, fmt.Errorf("execute anonymous block error: %w", err)
	}

	if createdSession != nil && tx != nil {
		if err := tx.Commit(); err != nil && !errors.Is(err, storage.ErrTxClosed) {
			return nil, fmt.Errorf("failed to commit anonymous block tx: %w", err)
		}
		tx = nil
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
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "IF ") {
		command = "IF"
	} else if strings.HasPrefix(upper, "WHILE ") {
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
