package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/storage"
)

func parseProcArgs(raw string) (string, []string) {
	raw = strings.TrimSpace(raw)
	upper := strings.ToUpper(raw)
	if strings.HasPrefix(upper, "EXEC ") {
		raw = strings.TrimSpace(raw[4:])
	} else if strings.HasPrefix(upper, "EXECUTE ") {
		raw = strings.TrimSpace(raw[7:])
	} else if strings.HasPrefix(upper, "CALL ") {
		raw = strings.TrimSpace(raw[4:])
	}

	idx := strings.IndexAny(raw, " \t,(")
	if idx == -1 {
		return cleanIdentifier(raw), nil
	}

	procName := cleanIdentifier(raw[:idx])
	rem := strings.TrimSpace(raw[idx:])
	rem = strings.Trim(rem, "();")

	if rem == "" {
		return procName, nil
	}

	var args []string
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(rem); i++ {
		ch := rem[i]
		if (ch == '\'' || ch == '"') && !inQuote {
			inQuote = true
			quoteChar = ch
			continue
		}
		if inQuote && ch == quoteChar {
			inQuote = false
			continue
		}
		if !inQuote && ch == ',' {
			args = append(args, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	if cur.Len() > 0 {
		args = append(args, strings.TrimSpace(cur.String()))
	}

	for i, a := range args {
		args[i] = strings.Trim(a, "'\"[]`")
	}

	return procName, args
}

func (s *Server) execBuiltinSystemProc(procLower string, args []string, start time.Time) (*QueryResult, error) {
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
			"database_name":     "sqlion",
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

		s.sessMu.RLock()
		idx := int64(51)
		for id, sess := range s.sessions {
			cmdStr := "AWAITING COMMAND"
			sess.mu.Lock()
			if sess.Tx != nil {
				cmdStr = "TRANSACTION ACTIVE"
			}
			sess.mu.Unlock()
			rows = append(rows, map[string]any{
				"spid":       idx,
				"ecid":       int64(0),
				"status":     "runnable",
				"loginame":   "sa",
				"hostname":   "localhost",
				"blk":        "0",
				"dbname":     "sqlion",
				"cmd":        cmdStr,
				"request_id": int64(0),
			})
			idx++
			_ = id
		}
		s.sessMu.RUnlock()

		if len(rows) == 0 {
			rows = append(rows, map[string]any{
				"spid":       int64(51),
				"ecid":       int64(0),
				"status":     "runnable",
				"loginame":   "sa",
				"hostname":   "localhost",
				"blk":        "0",
				"dbname":     "sqlion",
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
				Success:      true,
				Command:      "EXEC",
				Messages:     []string{"Caution: Changing any part of an object name could break scripts and stored procedures."},
				DurationUS:   time.Since(start).Microseconds(),
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
			Success:      true,
			Command:      "EXEC",
			Messages:     []string{"Caution: Changing any part of an object name could break scripts and stored procedures."},
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	return nil, fmt.Errorf("unrecognized system stored procedure: %s", procLower)
}

func (s *Server) execProcedureCall(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	procName, args := parseProcArgs(query)
	if procName == "" {
		return nil, fmt.Errorf("invalid EXEC syntax")
	}

	procLower := strings.ToLower(procName)
	switch procLower {
	case "sp_help", "sp_spaceused", "sp_who", "sp_who2", "sp_lock", "sp_rename":
		return s.execBuiltinSystemProc(procLower, args, start)
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
	for i, param := range procMeta.Params {
		if i < len(args) {
			raw := args[i]
			if num, err := strconv.ParseInt(raw, 10, 64); err == nil {
				argMap[param.Name] = num
			} else if f, err := strconv.ParseFloat(raw, 64); err == nil {
				argMap[param.Name] = f
			} else {
				argMap[param.Name] = raw
			}
		}
	}

	ctx := context.Background()
	if cs != nil && cs.QueryCtx != nil {
		ctx = cs.QueryCtx
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

	return &QueryResult{
		Success:      true,
		Command:      "EXEC",
		RowsAffected: 1,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     messages,
	}, nil
}
