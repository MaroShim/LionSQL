package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/auth"
	"sqlion/pkg/execution"
	"sqlion/pkg/planner"
	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

func (s *Coordinator) execVolcanoSelect(qInfo *planner.QueryInfo, start time.Time, cs *ClientSession) (*QueryResult, error) {
	p := s.newPlanner()
	_ = p.DecorrelateSubqueries(qInfo, func(tbl string) bool {
		_, err := s.engine.GetTable(cleanIdentifier(tbl))
		return err == nil
	})

	if qInfo.WhereClause != "" {
		if resolved, err := s.resolveSubqueriesWithSession(qInfo.WhereClause, cs); err == nil {
			qInfo.WhereClause = resolved
		}
	}

	leftTable := cleanIdentifier(qInfo.TableName)
	leftSchema, err := s.engine.GetTable(leftTable)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", leftTable, err)
	}

	rightSchemas := make(map[string]*storage.TableSchema)
	var joins []*planner.JoinInfo
	if len(qInfo.Joins) > 0 {
		joins = qInfo.Joins
	} else if qInfo.Join != nil {
		joins = []*planner.JoinInfo{qInfo.Join}
	}

	for _, j := range joins {
		rightTable := cleanIdentifier(j.Table)
		rSchema, err := s.engine.GetTable(rightTable)
		if err != nil {
			return nil, fmt.Errorf("table %q not found: %w", rightTable, err)
		}
		rightSchemas[j.Table] = rSchema
		rightSchemas[rightTable] = rSchema
		if j.Alias != "" {
			rightSchemas[j.Alias] = rSchema
			rightSchemas[cleanIdentifier(j.Alias)] = rSchema
		}
	}

	var tx storage.Transaction
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		t, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		tx = t
		defer tx.Rollback()
	}

	var leftStats *planner.TableStats
	rightStats := make(map[string]*planner.TableStats)
	if s.catalog != nil {
		leftStats, _ = s.catalog.GetTableStats(leftTable)
		for _, j := range joins {
			rTable := cleanIdentifier(j.Table)
			if st, ok := s.catalog.GetTableStats(rTable); ok && st != nil {
				rightStats[j.Table] = st
				rightStats[rTable] = st
				if j.Alias != "" {
					rightStats[j.Alias] = st
					rightStats[cleanIdentifier(j.Alias)] = st
				}
			}
		}
	}
	plan := p.BuildMultiJoinPlanWithStats(qInfo, leftSchema, leftStats, rightSchemas, rightStats)

	opts := execution.DefaultExecutorOptions()
	arena := storage.GetArena()
	defer storage.PutArena(arena)
	opts.Arena = arena

	if wm := s.DefaultWorkMem(); wm > 0 {
		opts.WorkMem = wm
	}
	if cs != nil {
		cs.Mu.Lock()
		if wmVal, ok := cs.Vars["work_mem"]; ok {
			switch v := wmVal.(type) {
			case int64:
				opts.WorkMem = v
			case int:
				opts.WorkMem = int64(v)
			case string:
				if b, err := execution.ParseMemSize(v); err == nil {
					opts.WorkMem = b
				}
			}
		}
		cs.Mu.Unlock()
	}

	exec, err := execution.BuildExecutorWithOptions(plan, tx, s.engine.GetTable, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to build volcano executor: %w", err)
	}

	ctx := context.Background()
	if cs != nil && cs.QueryCtx != nil {
		ctx = cs.QueryCtx
	}
	if err := exec.Open(ctx); err != nil {
		if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
			return nil, fmt.Errorf("canceling statement due to user request")
		}
		return nil, fmt.Errorf("volcano execution open error: %w", err)
	}
	defer exec.Close()

	var rows []map[string]any
	chunk := execution.NewChunk(execution.DefaultChunkSize)
	for {
		err := execution.PullChunk(ctx, exec, chunk, execution.DefaultChunkSize)
		if err != nil && err != io.EOF {
			if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
				return nil, fmt.Errorf("canceling statement due to user request")
			}
			return nil, err
		}
		if chunk.Len() == 0 && err == io.EOF {
			break
		}
		for _, tuple := range chunk.Tuples {
			rows = append(rows, tuple.ToMap())
		}
		if err == io.EOF {
			break
		}
	}

	if qInfo.Distinct {
		rows = deduplicateRows(rows)
	}

	cols := exec.Schema()
	if len(cols) == 0 && len(rows) > 0 {
		for k := range rows[0] {
			cols = append(cols, k)
		}
		sort.Strings(cols)
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      cols,
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func hasFromClause(query string) bool {
	upper := strings.ToUpper(query)
	inSingleQuote := false
	inDoubleQuote := false
	parenDepth := 0

	for i := 0; i < len(upper); i++ {
		ch := upper[i]
		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			continue
		}
		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			continue
		}
		if inSingleQuote || inDoubleQuote {
			continue
		}
		if ch == '(' {
			parenDepth++
			continue
		}
		if ch == ')' {
			if parenDepth > 0 {
				parenDepth--
			}
			continue
		}
		if parenDepth == 0 {
			if i+4 <= len(upper) && upper[i:i+4] == "FROM" {
				before := i == 0 || !isIdentChar(upper[i-1])
				after := i+4 == len(upper) || !isIdentChar(upper[i+4])
				if before && after {
					return true
				}
			}
		}
	}
	return false
}

func splitScalarProjections(s string) []string {
	var res []string
	var cur strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	parenDepth := 0

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			cur.WriteByte(ch)
			continue
		}
		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			cur.WriteByte(ch)
			continue
		}
		if inSingleQuote || inDoubleQuote {
			cur.WriteByte(ch)
			continue
		}
		if ch == '(' {
			parenDepth++
			cur.WriteByte(ch)
			continue
		}
		if ch == ')' {
			if parenDepth > 0 {
				parenDepth--
			}
			cur.WriteByte(ch)
			continue
		}
		if ch == ',' && parenDepth == 0 {
			res = append(res, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	if cur.Len() > 0 {
		res = append(res, strings.TrimSpace(cur.String()))
	}
	return res
}

func (s *Coordinator) evalScalarExpr(expr string, cs *ClientSession) (any, string) {
	trimmed := strings.TrimSpace(expr)
	upper := strings.ToUpper(trimmed)

	// Builtin system / database functions
	currDB := "sqlion"
	if cs != nil {
		cs.Mu.Lock()
		if cs.Database != "" {
			currDB = cs.Database
		} else if strings.HasPrefix(cs.ID, "tds-") {
			currDB = "master"
		}
		cs.Mu.Unlock()
	}

	switch upper {
	case "CURRENT_DATABASE()", "CURRENT_DATABASE":
		return currDB, "current_database"
	case "CURRENT_SCHEMA()", "CURRENT_SCHEMA":
		return "dbo", "current_schema"
	case "CURRENT_SCHEMAS(TRUE)", "CURRENT_SCHEMAS(FALSE)", "CURRENT_SCHEMAS":
		return "{dbo,public}", "current_schemas"
	case "CURRENT_USER", "USER", "SESSION_USER":
		return "postgres", "current_user"
	case "VERSION()":
		return "PostgreSQL 15.0 (SQLion OLTP & Stored Procedure Server)", "version"
	case "DB_NAME()", "DB_NAME":
		return currDB, "db_name"
	case "DB_ID()", "DB_ID":
		return int64(1), "db_id"
	case "ORIGINAL_DB_NAME()", "ORIGINAL_DB_NAME":
		if cs != nil && strings.HasPrefix(cs.ID, "tds-") {
			return "master", "original_db_name"
		}
		return "sqlion", "original_db_name"
	case "SCHEMA_NAME()", "SCHEMA_NAME":
		return "dbo", "schema_name"
	case "PG_BACKEND_PID()", "PG_BACKEND_PID":
		if cs != nil && cs.SPID > 0 {
			return int64(cs.SPID), "pg_backend_pid"
		}
		return int64(1), "pg_backend_pid"
	case "PG_IS_IN_RECOVERY()", "PG_IS_IN_RECOVERY":
		return false, "pg_is_in_recovery"
	case "PG_TRIGGER_DEPTH()", "PG_TRIGGER_DEPTH":
		return int64(0), "pg_trigger_depth"
	case "NOW()", "GETDATE()", "CURRENT_TIMESTAMP":
		return time.Now().Format("2006-01-02 15:04:05"), "now"
	case "@@VERSION":
		return "Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64) - SQLion Engine", "@@version"
	case "@@SPID":
		if cs != nil && cs.SPID > 0 {
			return int64(cs.SPID), "@@spid"
		}
		return int64(51), "@@spid"
	case "@@TRANCOUNT":
		if cs != nil && cs.Tx != nil {
			return int64(1), "@@trancount"
		}
		return int64(0), "@@trancount"
	case "@@ROWCOUNT":
		if cs != nil {
			cs.Mu.Lock()
			val, ok := cs.Vars["rowcount"]
			cs.Mu.Unlock()
			if ok && val != nil {
				return val, "@@rowcount"
			}
		}
		return int64(0), "@@rowcount"
	case "@@SERVERNAME", "@@SERVICENAME":
		return "SQLION", "@@servername"
	case "@@OPTIONS":
		return int64(5432), "@@options"
	case "@@ERROR", "ERROR_NUMBER()", "ERROR_NUMBER":
		if cs != nil {
			cs.Mu.Lock()
			val, ok := cs.Vars["error"]
			cs.Mu.Unlock()
			if ok && val != nil {
				return val, "@@error"
			}
		}
		return int64(0), "@@error"
	case "@@IDENTITY":
		if cs != nil {
			cs.Mu.Lock()
			val, ok := cs.Vars["identity"]
			cs.Mu.Unlock()
			if ok && val != nil {
				return val, "@@identity"
			}
		}
		return nil, "@@identity"
	case "SCOPE_IDENTITY()", "SCOPE_IDENTITY":
		if cs != nil {
			cs.Mu.Lock()
			val, ok := cs.Vars["scope_identity"]
			if !ok || val == nil {
				val, ok = cs.Vars["identity"]
			}
			cs.Mu.Unlock()
			if ok && val != nil {
				return val, "scope_identity"
			}
		}
		return nil, "scope_identity"
	}

	// System variable @@var
	if strings.HasPrefix(upper, "@@") {
		varName := strings.ToLower(trimmed[2:])
		if cs != nil {
			cs.Mu.Lock()
			v, exists := cs.Vars[varName]
			cs.Mu.Unlock()
			if exists {
				return v, trimmed
			}
		}
		return "", trimmed
	}

	// SERVERPROPERTY('prop')
	if strings.HasPrefix(upper, "SERVERPROPERTY(") && strings.HasSuffix(upper, ")") {
		arg := strings.TrimSpace(trimmed[len("SERVERPROPERTY(") : len(trimmed)-1])
		arg = strings.Trim(arg, "'\"")
		switch strings.ToUpper(arg) {
		case "PRODUCTVERSION":
			return "15.0.2000.5", "productversion"
		case "PRODUCTMAJORVERSION":
			return "15", "productmajorversion"
		case "PRODUCTLEVEL":
			return "RTM", "productlevel"
		case "EDITION":
			return "Developer Edition (64-bit)", "edition"
		case "ENGINEEDITION":
			return int64(3), "engineedition"
		case "INSTANCENAME":
			return "MSSQLSERVER", "instancename"
		case "MACHINENAME", "SERVERNAME":
			return "SQLION", "servername"
		case "COLLATION":
			return "SQL_Latin1_General_CP1_CI_AS", "collation"
		case "ISCLUSTERED", "ISHADRENABLED", "ISINTEGRATEDSECURITYONLY":
			return int64(0), strings.ToLower(arg)
		default:
			return "", strings.ToLower(arg)
		}
	}

	// SCHEMA_NAME(id)
	if strings.HasPrefix(upper, "SCHEMA_NAME(") && strings.HasSuffix(upper, ")") {
		return "dbo", "schema_name"
	}

	// SCHEMA_ID(name)
	if strings.HasPrefix(upper, "SCHEMA_ID(") && strings.HasSuffix(upper, ")") {
		return int64(1), "schema_id"
	}

	// OBJECT_ID('name', ['type'])
	if strings.HasPrefix(upper, "OBJECT_ID(") && strings.HasSuffix(upper, ")") {
		inner := strings.TrimSpace(trimmed[len("OBJECT_ID(") : len(trimmed)-1])
		arg := inner
		if commaIdx := strings.Index(inner, ","); commaIdx != -1 {
			arg = strings.TrimSpace(inner[:commaIdx])
		}
		clean := strings.TrimSpace(arg)
		clean = strings.TrimPrefix(clean, "N")
		clean = strings.TrimPrefix(clean, "n")
		clean = strings.Trim(clean, "'\"")
		clean = strings.ReplaceAll(clean, "[", "")
		clean = strings.ReplaceAll(clean, "]", "")
		if dotIdx := strings.LastIndex(clean, "."); dotIdx != -1 {
			clean = clean[dotIdx+1:]
		}
		tables := s.getSortedTables()
		for tIdx, t := range tables {
			if strings.EqualFold(t.Name, clean) {
				return int64(1000 + tIdx), "id"
			}
		}
		views := s.getSortedViews()
		for vIdx, v := range views {
			if strings.EqualFold(v.Name, clean) {
				return int64(2000 + vIdx), "id"
			}
		}
		procs := s.getSortedProcedures()
		for pIdx, p := range procs {
			if strings.EqualFold(p.Name, clean) {
				return int64(3000 + pIdx), "id"
			}
		}
		return nil, "id"
	}

	// OBJECT_NAME(id)
	if strings.HasPrefix(upper, "OBJECT_NAME(") && strings.HasSuffix(upper, ")") {
		inner := strings.TrimSpace(trimmed[len("OBJECT_NAME(") : len(trimmed)-1])
		clean := strings.Trim(inner, "'\"")
		if id, err := strconv.Atoi(clean); err == nil {
			if id >= 1000 && id < 2000 {
				tables := s.getSortedTables()
				idx := id - 1000
				if idx >= 0 && idx < len(tables) {
					return tables[idx].Name, "object_name"
				}
			} else if id >= 2000 && id < 3000 {
				views := s.getSortedViews()
				idx := id - 2000
				if idx >= 0 && idx < len(views) {
					return views[idx].Name, "object_name"
				}
			} else if id >= 3000 && id < 4000 {
				procs := s.getSortedProcedures()
				idx := id - 3000
				if idx >= 0 && idx < len(procs) {
					return procs[idx].Name, "object_name"
				}
			}
		}
		return nil, "object_name"
	}

	// QUOTENAME('name')
	if strings.HasPrefix(upper, "QUOTENAME(") && strings.HasSuffix(upper, ")") {
		inner := strings.TrimSpace(trimmed[len("QUOTENAME(") : len(trimmed)-1])
		arg := inner
		if commaIdx := strings.Index(inner, ","); commaIdx != -1 {
			arg = strings.TrimSpace(inner[:commaIdx])
		}
		clean := strings.Trim(strings.TrimSpace(arg), "'\"")
		return "[" + clean + "]", "quotename"
	}

	// IDENT_CURRENT('tableName')
	if strings.HasPrefix(upper, "IDENT_CURRENT(") && strings.HasSuffix(upper, ")") {
		inner := strings.TrimSpace(trimmed[len("IDENT_CURRENT(") : len(trimmed)-1])
		clean := strings.Trim(strings.TrimSpace(inner), "'\"[]")
		if lastID, ok := s.GetTableLastIdentity(clean); ok {
			return lastID, "ident_current"
		}
		tbl, err := s.engine.GetTable(clean)
		if err == nil && tbl != nil {
			return int64(1), "ident_current"
		}
		return nil, "ident_current"
	}

	// COLUMNPROPERTY(object_id, 'column_name', 'property')
	if strings.HasPrefix(upper, "COLUMNPROPERTY(") && strings.HasSuffix(upper, ")") {
		inner := strings.TrimSpace(trimmed[len("COLUMNPROPERTY(") : len(trimmed)-1])
		parts := strings.Split(inner, ",")
		if len(parts) >= 3 {
			objIDStr := strings.TrimSpace(parts[0])
			colName := strings.Trim(strings.TrimSpace(parts[1]), "'\"[]")
			propName := strings.Trim(strings.TrimSpace(parts[2]), "'\"")

			var id int
			if strings.HasPrefix(strings.ToUpper(objIDStr), "OBJECT_ID(") && strings.HasSuffix(objIDStr, ")") {
				tblName := strings.Trim(strings.TrimSpace(objIDStr[len("OBJECT_ID(") : len(objIDStr)-1]), "'\"[]")
				tables := s.getSortedTables()
				for tIdx, t := range tables {
					if strings.EqualFold(t.Name, tblName) {
						id = 1000 + tIdx
						break
					}
				}
			} else if parsedID, err := strconv.Atoi(objIDStr); err == nil {
				id = parsedID
			}

			if id >= 1000 && id < 2000 {
				tables := s.getSortedTables()
				idx := id - 1000
				if idx >= 0 && idx < len(tables) {
					t := tables[idx]
					for _, col := range t.Columns {
						if strings.EqualFold(col.Name, colName) {
							switch strings.ToUpper(propName) {
							case "ISIDENTITY":
								if col.Identity {
									return int64(1), "columnproperty"
								}
								return int64(0), "columnproperty"
							case "ALLOWSNULL":
								if col.Nullable {
									return int64(1), "columnproperty"
								}
								return int64(0), "columnproperty"
							case "PRECISION":
								return int64(col.Precision), "columnproperty"
							case "SCALE":
								return int64(col.Scale), "columnproperty"
							case "ISCOMPUTED":
								return int64(0), "columnproperty"
							default:
								return int64(0), "columnproperty"
							}
						}
					}
				}
			}
		}
		return nil, "columnproperty"
	}

	// ISNUMERIC(expr)
	if strings.HasPrefix(upper, "ISNUMERIC(") && strings.HasSuffix(upper, ")") {
		inner := strings.TrimSpace(trimmed[len("ISNUMERIC(") : len(trimmed)-1])
		clean := strings.Trim(strings.TrimSpace(inner), "'\"")
		if _, err := strconv.ParseFloat(clean, 64); err == nil {
			return int64(1), "isnumeric"
		}
		return int64(0), "isnumeric"
	}

	// CHARINDEX(sub, target [, start])
	if strings.HasPrefix(upper, "CHARINDEX(") && strings.HasSuffix(upper, ")") {
		inner := strings.TrimSpace(trimmed[len("CHARINDEX(") : len(trimmed)-1])
		parts := strings.Split(inner, ",")
		if len(parts) >= 2 {
			sub := strings.Trim(strings.TrimSpace(parts[0]), "'\"")
			target := strings.Trim(strings.TrimSpace(parts[1]), "'\"")
			start := 1
			if len(parts) >= 3 {
				if s, err := strconv.Atoi(strings.TrimSpace(parts[2])); err == nil {
					start = s
				}
			}
			if start < 1 {
				start = 1
			}
			runes := []rune(target)
			if start > len(runes) {
				return int64(0), "charindex"
			}
			subSlice := string(runes[start-1:])
			idx := strings.Index(strings.ToUpper(subSlice), strings.ToUpper(sub))
			if idx == -1 {
				return int64(0), "charindex"
			}
			runeOffset := len([]rune(subSlice[:idx]))
			return int64(start + runeOffset), "charindex"
		}
		return int64(0), "charindex"
	}

	// PATINDEX(pattern, target)
	if strings.HasPrefix(upper, "PATINDEX(") && strings.HasSuffix(upper, ")") {
		inner := strings.TrimSpace(trimmed[len("PATINDEX(") : len(trimmed)-1])
		parts := strings.Split(inner, ",")
		if len(parts) >= 2 {
			pattern := strings.Trim(strings.TrimSpace(parts[0]), "'\"")
			target := strings.Trim(strings.TrimSpace(parts[1]), "'\"")
			idx := proc.ComputePatindex(pattern, target)
			return idx, "patindex"
		}
		return int64(0), "patindex"
	}

	// STUFF(str, start, length, replace)
	if strings.HasPrefix(upper, "STUFF(") && strings.HasSuffix(upper, ")") {
		inner := strings.TrimSpace(trimmed[len("STUFF(") : len(trimmed)-1])
		parts := strings.Split(inner, ",")
		if len(parts) >= 4 {
			str := strings.Trim(strings.TrimSpace(parts[0]), "'\"")
			start, err1 := strconv.Atoi(strings.TrimSpace(parts[1]))
			length, err2 := strconv.Atoi(strings.TrimSpace(parts[2]))
			replace := strings.Trim(strings.TrimSpace(parts[3]), "'\"")
			if err1 == nil && err2 == nil {
				runes := []rune(str)
				if start >= 1 && start <= len(runes)+1 && length >= 0 {
					deleteStart := start - 1
					deleteEnd := deleteStart + length
					if deleteEnd > len(runes) {
						deleteEnd = len(runes)
					}
					res := string(runes[:deleteStart]) + replace + string(runes[deleteEnd:])
					return res, "stuff"
				}
			}
		}
		return nil, "stuff"
	}

	// pg_cancel_backend(pid)
	if strings.HasPrefix(upper, "PG_CANCEL_BACKEND(") && strings.HasSuffix(upper, ")") {
		arg := strings.TrimSpace(trimmed[len("PG_CANCEL_BACKEND(") : len(trimmed)-1])
		if pid, err := strconv.Atoi(arg); err == nil {
			return s.CancelQueryBySPID(pid), "pg_cancel_backend"
		}
		return false, "pg_cancel_backend"
	}

	// pg_terminate_backend(pid)
	if strings.HasPrefix(upper, "PG_TERMINATE_BACKEND(") && strings.HasSuffix(upper, ")") {
		arg := strings.TrimSpace(trimmed[len("PG_TERMINATE_BACKEND(") : len(trimmed)-1])
		if pid, err := strconv.Atoi(arg); err == nil {
			canceled := s.CancelQueryBySPID(pid)
			var targetID string
			if target := s.FindSessionBySPID(pid); target != nil {
				targetID = target.ID
			}
			if targetID != "" {
				s.CloseSession(targetID)
			}
			return canceled || targetID != "", "pg_terminate_backend"
		}
		return false, "pg_terminate_backend"
	}

	// current_setting('setting_name')
	if strings.HasPrefix(upper, "CURRENT_SETTING(") && strings.HasSuffix(upper, ")") {
		arg := strings.TrimSpace(trimmed[len("CURRENT_SETTING(") : len(trimmed)-1])
		arg = strings.Trim(arg, "'\"")
		switch strings.ToLower(arg) {
		case "server_version":
			return "15.0", "current_setting"
		case "server_version_num":
			return "150000", "current_setting"
		case "search_path":
			return "public", "current_setting"
		case "is_superuser":
			return "on", "current_setting"
		case "standard_conforming_strings":
			return "on", "current_setting"
		default:
			return "", "current_setting"
		}
	}

	// pg_is_other_temp_schema(oid)
	if strings.HasPrefix(upper, "PG_IS_OTHER_TEMP_SCHEMA(") {
		return false, "pg_is_other_temp_schema"
	}

	// Session variable @var
	if strings.HasPrefix(trimmed, "@") {
		if cs != nil {
			cs.Mu.Lock()
			v, exists := cs.Vars[trimmed]
			if !exists {
				v = cs.Vars[strings.TrimPrefix(trimmed, "@")]
			}
			cs.Mu.Unlock()
			if v != nil {
				return v, trimmed
			}
		}
		return nil, trimmed
	}

	// String literal
	if (strings.HasPrefix(trimmed, "'") && strings.HasSuffix(trimmed, "'")) ||
		(strings.HasPrefix(trimmed, "\"") && strings.HasSuffix(trimmed, "\"")) {
		unquoted := trimmed[1 : len(trimmed)-1]
		return unquoted, "?column?"
	}

	// Integer literal
	if n, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return n, trimmed
	}

	// Float literal
	if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return f, trimmed
	}

	// Boolean literal
	if strings.EqualFold(trimmed, "TRUE") {
		return true, "bool"
	}
	if strings.EqualFold(trimmed, "FALSE") {
		return false, "bool"
	}
	if strings.EqualFold(trimmed, "NULL") {
		return nil, "?column?"
	}

	// Try evaluating expression with proc Parser and EvalRowExpr
	p := proc.NewParser(proc.NewLexer(trimmed))
	astExpr := p.ParseExpr()
	if astExpr != nil && len(p.Errors()) == 0 {
		var varRow map[string]any
		if cs != nil {
			cs.Mu.Lock()
			if len(cs.Vars) > 0 {
				varRow = make(map[string]any, len(cs.Vars))
				for k, v := range cs.Vars {
					varRow[k] = v
				}
			}
			cs.Mu.Unlock()
		}
		if res, err := proc.EvalRowExpr(astExpr, varRow, nil); err == nil && res != nil {
			return res, trimmed
		}
	}

	return trimmed, trimmed
}

func (s *Coordinator) execScalarSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	upper := strings.ToUpper(cleaned)
	if !strings.HasPrefix(upper, "SELECT") {
		return nil, fmt.Errorf("invalid SELECT statement")
	}

	rawList := strings.TrimSpace(cleaned[6:])
	projItems := splitScalarProjections(rawList)
	if len(projItems) == 0 {
		return nil, fmt.Errorf("no projections specified in SELECT")
	}

	var columns []string
	row := make(map[string]any, len(projItems))

	asRe := regexp.MustCompile(`(?is)^(.*?)\s+AS\s+([a-zA-Z0-9_#$]+|"[^"]+")$`)

	for i, item := range projItems {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		exprStr := item
		alias := ""

		if m := asRe.FindStringSubmatch(item); len(m) == 3 {
			exprStr = strings.TrimSpace(m[1])
			alias = cleanIdentifier(m[2])
		} else {
			fields := strings.Fields(item)
			if len(fields) == 2 && !strings.Contains(fields[1], "(") && !strings.Contains(fields[1], ")") {
				exprStr = fields[0]
				alias = cleanIdentifier(fields[1])
			}
		}

		val, defAlias := s.evalScalarExpr(exprStr, cs)
		if alias == "" {
			alias = defAlias
		}
		if alias == "" {
			alias = fmt.Sprintf("col_%d", i+1)
		}

		colKey := alias
		if _, exists := row[colKey]; exists {
			colKey = fmt.Sprintf("%s_%d", alias, i+1)
		}

		columns = append(columns, colKey)
		row[colKey] = val
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         []map[string]any{row},
		RowsAffected: 1,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execSelectInto(targetTable, projections, fromClause string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	// 1. Check if target table already exists
	if _, err := s.engine.GetTable(targetTable); err == nil {
		return nil, fmt.Errorf("table %q already exists", targetTable)
	}

	// 2. Execute underlying SELECT query to retrieve columns and rows
	selectSQL := fmt.Sprintf("SELECT %s FROM %s", projections, fromClause)
	selRes, err := s.execSelect(selectSQL, start, cs)
	if err != nil {
		return nil, err
	}
	if !selRes.Success {
		return selRes, nil
	}

	// 3. Inspect source table schema if available for exact data types
	var srcSchema *storage.TableSchema
	fromFields := strings.Fields(fromClause)
	if len(fromFields) > 0 {
		srcTableName := cleanIdentifier(fromFields[0])
		if sch, err := s.engine.GetTable(srcTableName); err == nil {
			srcSchema = sch
		}
	}

	// 4. Construct target table schema
	schema := &storage.TableSchema{
		Name:    targetTable,
		Columns: make([]storage.Column, 0, len(selRes.Columns)),
	}

	// Determine PK candidate
	pkColName := ""
	if srcSchema != nil {
		if srcPK, err := srcSchema.PrimaryKeyColumn(); err == nil && srcPK != nil {
			for _, col := range selRes.Columns {
				if strings.EqualFold(col, srcPK.Name) {
					pkColName = col
					break
				}
			}
		}
	}

	// Check if pkColName is unique among rows
	if pkColName != "" && len(selRes.Rows) > 0 {
		seen := make(map[string]bool)
		for _, row := range selRes.Rows {
			valKey := fmt.Sprintf("%v", row[pkColName])
			if seen[valKey] {
				pkColName = "" // duplicate found
				break
			}
			seen[valKey] = true
		}
	}

	// If no unique PK found from source, check if any projected column is completely unique
	if pkColName == "" && len(selRes.Rows) > 0 {
		for _, col := range selRes.Columns {
			unique := true
			seen := make(map[string]bool)
			for _, row := range selRes.Rows {
				v := row[col]
				if v == nil {
					unique = false
					break
				}
				valKey := fmt.Sprintf("%v", v)
				if seen[valKey] {
					unique = false
					break
				}
				seen[valKey] = true
			}
			if unique {
				pkColName = col
				break
			}
		}
	}

	needSyntheticPK := false
	if pkColName == "" {
		if len(selRes.Rows) == 0 {
			if len(selRes.Columns) > 0 {
				pkColName = selRes.Columns[0]
			} else {
				needSyntheticPK = true
			}
		} else {
			needSyntheticPK = true
		}
	}

	if needSyntheticPK {
		schema.Columns = append(schema.Columns, storage.Column{
			Name:       "_row_id",
			Type:       storage.TypeInt,
			PrimaryKey: true,
			Nullable:   false,
		})
	}

	for _, colName := range selRes.Columns {
		colType := storage.TypeVarChar
		if srcSchema != nil {
			if c, _, found := srcSchema.FindColumn(colName); found {
				colType = c.Type
			}
		}
		if colType == storage.TypeVarChar && len(selRes.Rows) > 0 {
			for _, row := range selRes.Rows {
				if v, ok := row[colName]; ok && v != nil {
					switch v.(type) {
					case int, int64:
						colType = storage.TypeInt
					case float64:
						colType = storage.TypeFloat
					case bool:
						colType = storage.TypeBool
					}
					break
				}
			}
		}

		schema.Columns = append(schema.Columns, storage.Column{
			Name:       colName,
			Type:       colType,
			PrimaryKey: strings.EqualFold(colName, pkColName),
			Nullable:   true,
		})
	}

	// 5. Create table in engine
	if err := s.engine.CreateTable(schema); err != nil {
		return nil, fmt.Errorf("failed to create table %q: %w", targetTable, err)
	}

	// Register temp tables
	if strings.HasPrefix(targetTable, "##") {
		creatorID := "default"
		if cs != nil && cs.ID != "" {
			creatorID = cs.ID
		}
		s.RegisterGlobalTemp(targetTable, creatorID)
	} else if strings.HasPrefix(targetTable, "#") {
		if cs != nil {
			cs.Mu.Lock()
			cs.TempTables = append(cs.TempTables, targetTable)
			cs.Mu.Unlock()
		}
	}

	// 6. Insert rows if any
	if len(selRes.Rows) > 0 {
		var tx storage.Transaction
		var isEphemeral bool
		if cs != nil && cs.Tx != nil {
			tx = cs.Tx
			isEphemeral = false
		} else {
			session, err := s.engine.NewSession()
			if err != nil {
				return nil, err
			}
			defer session.Close()

			t, err := session.BeginTx(cs.TxOptions())
			if err != nil {
				return nil, err
			}
			tx = t
			isEphemeral = true
		}

		for idx, row := range selRes.Rows {
			rec := make(storage.Record, len(schema.Columns))
			if needSyntheticPK {
				rec["_row_id"] = storage.IntValue(int64(idx + 1))
			}
			for _, col := range schema.Columns {
				if col.Name == "_row_id" {
					continue
				}
				val := row[col.Name]
				rec[col.Name] = convertValue(val, col.Type)
			}
			if err := tx.Insert(targetTable, rec); err != nil {
				if isEphemeral {
					_ = tx.Rollback()
				}
				return nil, fmt.Errorf("failed inserting row into %q: %w", targetTable, err)
			}
		}

		if isEphemeral {
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("transaction commit error: %w", err)
			}
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT INTO",
		RowsAffected: int64(len(selRes.Rows)),
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("(%d rows affected)", len(selRes.Rows))},
	}, nil
}

func (s *Coordinator) execVBDEDML(command, cleaned string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	var prog *proc.Program
	var err error
	if s.executor != nil && s.executor.Cache() != nil {
		prog, err = s.executor.Cache().GetOrCompile(cleaned)
	} else {
		prog, err = proc.CompileStatement(cleaned)
	}
	if err != nil {
		return nil, err
	}

	var tx storage.Transaction
	var isEphemeral bool
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		t, err := session.BeginTx(cs.TxOptions())
		if err != nil {
			return nil, err
		}
		tx = t
		isEphemeral = true
		defer func() {
			if isEphemeral && tx != nil {
				_ = tx.Rollback()
			}
		}()
	}

	var params map[string]any
	if cs != nil {
		cs.Mu.Lock()
		if len(cs.Vars) > 0 {
			params = make(map[string]any, len(cs.Vars))
			for k, v := range cs.Vars {
				params[k] = v
			}
		}
		cs.Mu.Unlock()
	}

	ctx := context.Background()
	if cs != nil && cs.QueryCtx != nil {
		ctx = cs.QueryCtx
	}
	execRes, err := s.executor.ExecuteInTxContext(ctx, prog, params, tx, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
			return nil, fmt.Errorf("canceling statement due to user request")
		}
		return nil, err
	}

	if isEphemeral {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("failed to commit transaction: %w", err)
		}
		tx = nil
	}

	if cs != nil {
		cs.Mu.Lock()
		if execRes.LastIdentity > 0 {
			cs.Vars["identity"] = execRes.LastIdentity
			cs.Vars["@@identity"] = execRes.LastIdentity
		}
		cs.Vars["rowcount"] = execRes.RowsAffected
		cs.Vars["@@rowcount"] = execRes.RowsAffected
		cs.Mu.Unlock()
	}

	msg := fmt.Sprintf("(%d rows affected)", execRes.RowsAffected)
	if len(execRes.Logs) > 0 {
		msg = strings.Join(execRes.Logs, "\n")
	}

	return &QueryResult{
		Success:      true,
		Command:      command,
		RowsAffected: execRes.RowsAffected,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{msg},
	}, nil
}

func (s *Coordinator) execVDBESelect(cleaned string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	var prog *proc.Program
	var err error
	if s.executor != nil && s.executor.Cache() != nil {
		prog, err = s.executor.Cache().GetOrCompile(cleaned)
	} else {
		prog, err = proc.CompileQuery(cleaned)
	}
	if err != nil {
		return nil, err
	}

	var tx storage.Transaction
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		t, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		tx = t
		defer tx.Rollback()
	}

	var params map[string]any
	if cs != nil {
		cs.Mu.Lock()
		params = make(map[string]any, len(cs.Vars)+2)
		for k, v := range cs.Vars {
			params[k] = v
		}
		if cs.Database != "" {
			params["current_database"] = cs.Database
			params["db_name"] = cs.Database
		}
		cs.Mu.Unlock()
	}

	execRes, err := s.executor.ExecuteInTx(prog, params, tx, nil)
	if err != nil {
		return nil, err
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      execRes.Columns,
		Rows:         execRes.Rows,
		RowsAffected: int64(len(execRes.Rows)),
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     execRes.Logs,
	}, nil
}

func (s *Coordinator) execSetOpSelect(setQuery *planner.SetOpQuery, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if len(setQuery.Segments) < 2 {
		return nil, fmt.Errorf("set operator requires at least 2 query branches")
	}

	// 1. Execute branch 0
	firstRes, err := s.execSelect(setQuery.Segments[0].Query, start, cs)
	if err != nil {
		return nil, fmt.Errorf("set operation initial query failed: %w", err)
	}
	outCols := firstRes.Columns
	var currIter execution.Iterator = execution.NewSliceIterator(firstRes.Rows, outCols)

	// 2. Chained execution of subsequent branches
	for i := 1; i < len(setQuery.Segments); i++ {
		seg := setQuery.Segments[i]
		nextRes, err := s.execSelect(seg.Query, start, cs)
		if err != nil {
			return nil, fmt.Errorf("set operation branch %d (%s) failed: %w", i, seg.Op, err)
		}
		if len(nextRes.Columns) != len(outCols) {
			return nil, fmt.Errorf("all queries combined using %s must have an equal number of expressions (%d != %d)",
				seg.Op, len(outCols), len(nextRes.Columns))
		}

		rightIter := execution.NewSliceIterator(nextRes.Rows, nextRes.Columns)
		setOpType, err := execution.ParseSetOpType(seg.Op)
		if err != nil {
			return nil, err
		}

		currIter = execution.NewSetOpIterator(currIter, rightIter, setOpType)
	}

	// 3. Trailing ORDER BY across entire set operation result
	if setQuery.OrderBy != "" {
		currIter = execution.NewSortIterator(currIter, setQuery.OrderBy)
	}

	// 4. Trailing LIMIT across entire set operation result
	if setQuery.Limit >= 0 {
		currIter = execution.NewLimitIterator(currIter, setQuery.Limit, 0)
	}

	// 5. Open and materialize results
	ctx := context.Background()
	if cs != nil && cs.QueryCtx != nil {
		ctx = cs.QueryCtx
	}
	if err := currIter.Open(ctx); err != nil {
		if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
			return nil, fmt.Errorf("canceling statement due to user request")
		}
		return nil, fmt.Errorf("set operation execution open error: %w", err)
	}
	defer currIter.Close()

	var finalRows []map[string]any
	for {
		tuple, err := currIter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
				return nil, fmt.Errorf("canceling statement due to user request")
			}
			return nil, fmt.Errorf("set operation iterator error: %w", err)
		}
		finalRows = append(finalRows, tuple.ToMap())
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      outCols,
		Rows:         finalRows,
		RowsAffected: int64(len(finalRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execSetOpExplain(setQuery *planner.SetOpQuery, analyze bool, start time.Time) (*QueryResult, error) {
	p := s.newPlanner()
	plan, err := p.BuildSetOpPlan(setQuery, func(subSQL string) (*planner.PlanNode, error) {
		qInfo, err := p.ParseSelect(subSQL)
		if err != nil {
			return &planner.PlanNode{
				Type:          planner.NodeTableScan,
				Relation:      cleanIdentifier(subSQL),
				TotalCost:     10.0,
				EstimatedRows: 10,
			}, nil
		}
		tblName := cleanIdentifier(qInfo.TableName)
		schema, err := s.engine.GetTable(tblName)
		if err != nil {
			return &planner.PlanNode{
				Type:          planner.NodeTableScan,
				Relation:      tblName,
				TotalCost:     10.0,
				EstimatedRows: 10,
			}, nil
		}
		var stats *planner.TableStats
		if s.catalog != nil {
			stats, _ = s.catalog.GetTableStats(tblName)
		}
		return p.BuildJoinPlanWithStats(qInfo, schema, stats, nil, nil), nil
	})
	if err != nil {
		return nil, fmt.Errorf("set operation explain plan error: %w", err)
	}

	lines := planner.FormatPlanText(plan, analyze)
	rows := make([]map[string]any, len(lines))
	for i, l := range lines {
		rows[i] = map[string]any{"QUERY PLAN": l}
	}

	return &QueryResult{
		Success:      true,
		Command:      "EXPLAIN",
		Columns:      []string{"QUERY PLAN"},
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

var mssqlOffsetFetchRe = regexp.MustCompile(`(?is)\s+OFFSET\s+(\d+)\s+ROWS?\s+FETCH\s+(?:NEXT|FIRST)\s+(\d+)\s+ROWS?\s+ONLY`)
var mssqlOffsetOnlyRe = regexp.MustCompile(`(?is)\s+OFFSET\s+(\d+)\s+ROWS?(?:\s*;|\s*$)`)
var mssqlFetchOnlyRe = regexp.MustCompile(`(?is)\s+FETCH\s+(?:NEXT|FIRST)\s+(\d+)\s+ROWS?\s+ONLY`)
var mssqlTopRe = regexp.MustCompile(`(?is)^(\s*SELECT\s+)TOP\s*\(?\s*(\d+)\s*\)?\s+(DISTINCT\s+)?(.*)$`)
var mssqlDistinctTopRe = regexp.MustCompile(`(?is)^(\s*SELECT\s+DISTINCT\s+)TOP\s*\(?\s*(\d+)\s*\)?\s+(.*)$`)
var mssqlNolockRe = regexp.MustCompile(`(?is)\bWITH\s*\(\s*(?:NOLOCK|READUNCOMMITTED)\s*\)`)
var mssqlOptionRe = regexp.MustCompile(`(?is)\s+OPTION\s*\(.*?\)\s*$`)

func normalizeMSSQLPagination(sql string) string {
	if mssqlNolockRe.MatchString(sql) {
		sql = mssqlNolockRe.ReplaceAllString(sql, "")
	}
	if mssqlOptionRe.MatchString(sql) {
		sql = mssqlOptionRe.ReplaceAllString(sql, "")
	}

	if mssqlOffsetFetchRe.MatchString(sql) {
		sql = mssqlOffsetFetchRe.ReplaceAllString(sql, " LIMIT $2 OFFSET $1")
	} else if mssqlFetchOnlyRe.MatchString(sql) {
		sql = mssqlFetchOnlyRe.ReplaceAllString(sql, " LIMIT $1")
	} else if mssqlOffsetOnlyRe.MatchString(sql) {
		sql = mssqlOffsetOnlyRe.ReplaceAllString(sql, " OFFSET $1")
	}

	upper := strings.ToUpper(sql)
	if strings.Contains(upper, " TOP ") || strings.Contains(upper, " TOP(") {
		if !strings.Contains(upper, " LIMIT ") {
			if m := mssqlDistinctTopRe.FindStringSubmatch(sql); len(m) == 4 {
				sql = fmt.Sprintf("%s%s LIMIT %s", m[1], m[3], m[2])
			} else if m := mssqlTopRe.FindStringSubmatch(sql); len(m) == 5 {
				dist := m[3]
				sql = fmt.Sprintf("%s%s%s LIMIT %s", m[1], dist, m[4], m[2])
			}
		}
	}
	return sql
}

func (s *Coordinator) execSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// P-5 Fast-Path: Zero-overhead storage bypass for simple single-PK lookups
	if res, err, ok := s.tryPKLookupBypass(cleaned, start, cs); ok {
		return res, err
	}

	cleaned = normalizeMSSQLPagination(cleaned)

	// -1. T-SQL FOR JSON clause: SELECT ... FOR JSON PATH / AUTO [,...]
	if hasForJSON(cleaned) {
		return s.execForJSONSelect(cleaned, start, cs)
	}

	// -1. View expansion: if FROM clause references a known view, rewrite as a CTE.
	//     e.g. SELECT name FROM v_engineers WHERE salary > 85000
	//     becomes WITH v_engineers AS (SELECT id, name, salary FROM employees WHERE dept='Engineering')
	//             SELECT name FROM v_engineers WHERE salary > 85000
	if cteQuery, ok := s.rewriteViewsAsCTE(cleaned); ok {
		return s.execCTE(cteQuery, start)
	}

	// 0. Set Operations: UNION [ALL], INTERSECT, EXCEPT / MINUS
	if setQuery, ok := planner.FindTopLevelSetOps(cleaned); ok && setQuery != nil && len(setQuery.Segments) >= 2 {
		return s.execSetOpSelect(setQuery, start, cs)
	}

	// 0. HeidiSQL & PG GUI virtual catalog multi-table JOIN intercept (e.g. pg_class JOIN pg_namespace)
	if isHeidiSQLCatalogJoinQuery(cleaned) {
		return s.execHeidiSQLCatalogJoinSelect(cleaned, start)
	}

	// 0. PIVOT: SELECT ... FROM ... PIVOT (...) ...
	if hasPivot(cleaned) {
		return s.execPivotSelect(cleaned, start, cs)
	}

	// 0. UNPIVOT: SELECT ... FROM ... UNPIVOT (...) ...
	if hasUnpivot(cleaned) {
		return s.execUnpivotSelect(cleaned, start, cs)
	}

	// 0. SELECT ... INTO <target_table> FROM ...
	selectIntoRe := regexp.MustCompile(`(?is)^SELECT\s+(.*?)\s+INTO\s+([^\s,;]+)\s+FROM\s+(.*)$`)
	if m := selectIntoRe.FindStringSubmatch(cleaned); len(m) == 4 {
		projections := strings.TrimSpace(m[1])
		targetTable := cleanIdentifier(m[2])
		fromClause := strings.TrimSpace(m[3])
		return s.execSelectInto(targetTable, projections, fromClause, start, cs)
	}

	// 0. Table-less scalar SELECT: e.g. SELECT 1, SELECT current_database(), SELECT @@VERSION
	if !hasFromClause(cleaned) {
		upperClean := strings.ToUpper(cleaned)
		if !strings.Contains(upperClean, "@@") && !strings.Contains(upperClean, "SCOPE_IDENTITY") && !strings.Contains(upperClean, "OBJECT_ID") && !strings.Contains(upperClean, "OBJECT_NAME") && !strings.Contains(upperClean, "DB_NAME") && !strings.Contains(upperClean, "CURRENT_DATABASE") {
			if res, err := s.execVDBESelect(cleaned, start, cs); err == nil && res != nil {
				return res, nil
			}
		}
		return s.execScalarSelect(cleaned, start, cs)
	}

	// 0.4. CROSS APPLY and OUTER APPLY joins
	if hasApply(cleaned) {
		return s.execApplySelect(cleaned, start, cs)
	}

	// 0.5. Derived Tables in FROM / JOIN: e.g. FROM (SELECT ...) AS t
	if hasDerivedTables(cleaned) {
		rewritten, tempTables, err := s.resolveDerivedTables(cleaned, start, cs)
		if err != nil {
			return nil, err
		}
		defer func() {
			for _, tbl := range tempTables {
				_ = s.engine.DropTable(tbl)
			}
		}()
		return s.execSelect(rewritten, start, cs)
	}

	// 0.6. T-SQL STRING_SPLIT table-valued function: SELECT ... FROM STRING_SPLIT(...)
	if isStringSplitSelect(cleaned) {
		return s.execStringSplitSelect(cleaned, start, cs)
	}

	p := s.newPlanner()
	qInfo, parseErr := p.ParseSelect(cleaned)
	if parseErr == nil && qInfo != nil {
		_ = p.DecorrelateSubqueries(qInfo, func(tbl string) bool {
			_, err := s.engine.GetTable(cleanIdentifier(tbl))
			return err == nil
		})

		rawTable := qInfo.TableName
		tableName := cleanIdentifier(rawTable)

		// Check for Table-Valued Function (TVF) invocation: e.g. FROM GetTopAccounts(100)
		tvfCallRe := regexp.MustCompile(`(?is)^([a-zA-Z0-9_#]+)\s*\((.*?)\)$`)
		if m := tvfCallRe.FindStringSubmatch(rawTable); len(m) == 3 {
			tvfName := cleanIdentifier(m[1])
			argsRaw := m[2]
			fnMeta, err := s.catalog.GetFunction(tvfName)
			if err == nil && fnMeta != nil && fnMeta.IsTableValued {
				var argVals []string
				if strings.TrimSpace(argsRaw) != "" {
					for _, arg := range strings.Split(argsRaw, ",") {
						argVals = append(argVals, strings.TrimSpace(arg))
					}
				}
				innerSQL := fnMeta.Body
				for i, p := range fnMeta.Params {
					if i < len(argVals) {
						innerSQL = strings.ReplaceAll(innerSQL, p.Name, argVals[i])
						innerSQL = strings.ReplaceAll(innerSQL, strings.TrimPrefix(p.Name, "@"), argVals[i])
					}
				}
				return s.execSelect(innerSQL, start, cs)
			}
		}

		// Intercept Virtual System Catalog Views (sys.* and information_schema.*)
		if s.isVirtualCatalogTable(tableName) {
			return s.execVirtualCatalogSelect(tableName, strings.Join(qInfo.Projections, ", "), qInfo.WhereClause, qInfo.OrderByClause, int(qInfo.LimitCount), start)
		}

		// RBAC Permission Check for primary table and joined relations
		if err := s.checkTableAccess(cs, tableName, auth.PrivSelect); err != nil {
			return nil, err
		}
		for _, j := range qInfo.Joins {
			if err := s.checkTableAccess(cs, j.Table, auth.PrivSelect); err != nil {
				return nil, err
			}
		}
		if qInfo.Join != nil {
			if err := s.checkTableAccess(cs, qInfo.Join.Table, auth.PrivSelect); err != nil {
				return nil, err
			}
		}

		// If query has joins, table/query hints, or subqueries, execute via Volcano CBO Engine
		if len(qInfo.Joins) > 0 || qInfo.Join != nil || len(qInfo.TableHints) > 0 || len(qInfo.QueryHints) > 0 {
			return s.execVolcanoSelect(qInfo, start, cs)
		}

		if qInfo.WhereClause != "" {
			if resolved, err := s.resolveSubqueriesWithSession(qInfo.WhereClause, cs); err == nil && resolved != qInfo.WhereClause {
				cleaned = strings.Replace(cleaned, qInfo.WhereClause, resolved, 1)
				qInfo.WhereClause = resolved
			}
		}

		// If query has unresolved subqueries in WHERE (e.g. correlated), execute via Volcano
		if strings.Contains(strings.ToUpper(qInfo.WhereClause), "EXISTS") || strings.Contains(strings.ToUpper(qInfo.WhereClause), "SELECT") {
			return s.execVolcanoSelect(qInfo, start, cs)
		}

		// Composite / single-column index-accelerated SELECT path.
		// Try this before VDBE so that equality conditions on indexed columns
		// benefit from prefix scans even for single-column composite indexes.
		if qInfo.WhereClause != "" && !qInfo.HasAggregates && len(qInfo.GroupByCols) == 0 {
			if res, ok := s.execIndexSelect(qInfo, tableName, start, cs); ok {
				if qInfo.Distinct {
					res.Rows = deduplicateRows(res.Rows)
					res.RowsAffected = int64(len(res.Rows))
				}
				return res, nil
			}
		}

		// Execute via unified VDBE Virtual Machine Engine (Primary Fast-Path)
		if res, err := s.execVDBESelect(cleaned, start, cs); err == nil {
			if qInfo.Distinct {
				res.Rows = deduplicateRows(res.Rows)
				res.RowsAffected = int64(len(res.Rows))
			}
			return res, nil
		}

		// Secondary Fallback: Direct Volcano execution
		if res, err := s.execVolcanoSelect(qInfo, start, cs); err == nil {
			return res, nil
		}
	}

	// Parse projection, table, WHERE, ORDER BY, LIMIT, OFFSET
	selectRe := regexp.MustCompile(`(?is)^SELECT\s+(.*?)\s+FROM\s+(.*?)(?:\s+WHERE\s+(.*?))?(?:\s+GROUP\s+BY\s+(.*?))?(?:\s+HAVING\s+(.*?))?(?:\s+ORDER\s+BY\s+(.*?))?(?:\s+LIMIT\s+(\d+))?(?:\s+OFFSET\s+(\d+))?$`)
	matches := selectRe.FindStringSubmatch(cleaned)
	if len(matches) < 3 {
		if !hasFromClause(cleaned) {
			return s.execScalarSelect(cleaned, start, cs)
		}
		return nil, fmt.Errorf("invalid or unsupported SELECT syntax")
	}

	projStr := strings.TrimSpace(matches[1])
	rawTable := strings.TrimSpace(matches[2])
	baseTableName := rawTable
	if f := strings.Fields(rawTable); len(f) > 0 {
		baseTableName = f[0]
	}
	tableName := cleanIdentifier(baseTableName)
	whereClause := ""
	if len(matches) > 3 {
		whereClause = strings.TrimSpace(matches[3])
		if resolved, err := s.resolveSubqueriesWithSession(whereClause, cs); err == nil {
			whereClause = resolved
		}
	}
	orderByClause := ""
	if len(matches) > 6 {
		orderByClause = strings.TrimSpace(matches[6])
	}
	limitCount := -1
	if len(matches) > 7 && matches[7] != "" {
		if n, err := strconv.Atoi(matches[7]); err == nil {
			limitCount = n
		}
	}
	offsetCount := 0
	if len(matches) > 8 && matches[8] != "" {
		if n, err := strconv.Atoi(matches[8]); err == nil {
			offsetCount = n
		}
	}

	// Check for Table-Valued Function (TVF) invocation: e.g. FROM GetTopAccounts(100)
	tvfCallRe := regexp.MustCompile(`(?is)^([a-zA-Z0-9_#]+)\s*\((.*?)\)$`)
	if m := tvfCallRe.FindStringSubmatch(baseTableName); len(m) == 3 {
		tvfName := cleanIdentifier(m[1])
		argsRaw := m[2]
		fnMeta, err := s.catalog.GetFunction(tvfName)
		if err == nil && fnMeta != nil && fnMeta.IsTableValued {
			var argVals []string
			if strings.TrimSpace(argsRaw) != "" {
				for _, arg := range strings.Split(argsRaw, ",") {
					argVals = append(argVals, strings.TrimSpace(arg))
				}
			}
			innerSQL := fnMeta.Body
			for i, p := range fnMeta.Params {
				if i < len(argVals) {
					innerSQL = strings.ReplaceAll(innerSQL, p.Name, argVals[i])
					innerSQL = strings.ReplaceAll(innerSQL, strings.TrimPrefix(p.Name, "@"), argVals[i])
				}
			}
			return s.execSelect(innerSQL, start, cs)
		}
	}

	// Intercept Virtual System Catalog Views (sys.* and information_schema.*)
	if s.isVirtualCatalogTable(tableName) {
		return s.execVirtualCatalogSelect(tableName, projStr, whereClause, orderByClause, limitCount, start)
	}

	// RBAC Permission Check
	if err := s.checkTableAccess(cs, tableName, auth.PrivSelect); err != nil {
		return nil, err
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	var tx storage.Transaction
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		t, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		tx = t
		defer tx.Rollback()
	}

	var cursor storage.Cursor
	usedIndex := false

	if whereClause != "" {
		// Parse all equality conditions from the WHERE clause (AND-separated).
		// Collect col -> val pairs for composite index prefix matching.
		eqColVals := parseEqualityConditions(whereClause)

		if len(eqColVals) > 0 {
			eqColSet := make(map[string]bool, len(eqColVals))
			for col := range eqColVals {
				eqColSet[strings.ToLower(col)] = true
			}

			// Try composite multi-column prefix first, then fall back to single-column.
			if idx, prefixLen := schema.FindBestCompositeIndex(eqColSet); idx != nil && prefixLen > 0 {
				var lowVal, highVal storage.Value
				if len(idx.Columns) == 1 {
					// True single-column index: use exact typed value for point lookup.
					colName := idx.Columns[0]
					if colMeta, _, colFound := schema.FindColumn(colName); colFound && colMeta != nil {
						valStr := eqColVals[strings.ToLower(colName)]
						parsed := parseValueToType(valStr, colMeta.Type)
						lowVal = parsed
						highVal = parsed
					} else {
						lowVal = storage.StringValue(eqColVals[strings.ToLower(colName)])
						highVal = lowVal
					}
				} else {
					// Composite index: build NUL-separated prefix key range.
					// e.g. dept='Engineering' on (dept,level) → scan ["Engineering\x00", "Engineering\x00\xff...]
					prefixKey := buildCompositePrefixKey(idx.Columns[:prefixLen], eqColVals, schema)
					lowVal = storage.StringValue(prefixKey)
					highVal = storage.StringValue(prefixKey + "\xff\xff\xff\xff")
				}
				if cur, err := tx.IndexScan(tableName, idx.Name, &lowVal, &highVal); err == nil {
					cursor = cur
					usedIndex = true
				}
			} else if len(eqColVals) == 1 {
				// Fallback: single equality, original path.
				for col, val := range eqColVals {
					if idx, found := schema.FindIndexByColumn(col); found {
						if colMeta, _, colFound := schema.FindColumn(col); colFound && colMeta != nil {
							parsed := parseValueToType(val, colMeta.Type)
							if cur, err := tx.IndexScan(tableName, idx.Name, &parsed, &parsed); err == nil {
								cursor = cur
								usedIndex = true
							}
						}
					}
					break
				}
			}
		}
	}

	if !usedIndex {
		cur, err := tx.Scan(tableName)
		if err != nil {
			return nil, fmt.Errorf("scan error on %s: %w", tableName, err)
		}
		cursor = cur
	}
	defer cursor.Close()

	if qInfo != nil && qInfo.HasAggregates {
		rawRows := make([]map[string]any, 0)
		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
				break
			}
			if whereClause != "" && !evaluateWhereCondition(rec, whereClause) {
				continue
			}
			rMap := make(map[string]any)
			for _, col := range schema.Columns {
				rMap[col.Name] = formatStorageValue(rec[col.Name])
			}
			rawRows = append(rawRows, rMap)
		}
		return s.execAggregate(rawRows, qInfo, start)
	}

	// 1. Determine columns
	type fallbackProjItem struct {
		expr   string
		alias  string
		parsed proc.Expr
	}
	var projItems []fallbackProjItem
	var selectedCols []string
	isSelectAll := projStr == "*" || projStr == ""
	if isSelectAll {
		for _, col := range schema.Columns {
			selectedCols = append(selectedCols, col.Name)
			projItems = append(projItems, fallbackProjItem{expr: col.Name, alias: col.Name})
		}
	} else {
		for _, c := range planner.SplitProjections(projStr) {
			cTrimmed := strings.TrimSpace(c)
			if cTrimmed == "" {
				continue
			}
			aliasIdx := strings.Index(strings.ToUpper(cTrimmed), " AS ")
			var expr, alias string
			if aliasIdx != -1 {
				expr = strings.TrimSpace(cTrimmed[:aliasIdx])
				alias = cleanIdentifier(strings.TrimSpace(cTrimmed[aliasIdx+4:]))
			} else {
				expr = cTrimmed
				alias = cleanIdentifier(cTrimmed)
			}
			selectedCols = append(selectedCols, alias)
			item := fallbackProjItem{expr: expr, alias: alias}
			if strings.Contains(expr, "->") || strings.Contains(expr, "(") {
				p := proc.NewParser(proc.NewLexer(expr))
				if ast := p.ParseExpr(); ast != nil && len(p.Errors()) == 0 {
					item.parsed = ast
				}
			}
			projItems = append(projItems, item)
		}
	}

	// 2. Scan and filter rows
	type fullRow struct {
		row map[string]any
	}
	var scannedRows []fullRow
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			break
		}

		if whereClause != "" && !evaluateWhereCondition(rec, whereClause) {
			continue
		}

		rMap := make(map[string]any)
		for _, col := range schema.Columns {
			rMap[col.Name] = formatStorageValue(rec[col.Name])
		}
		for k, v := range rec {
			rMap[k] = formatStorageValue(v)
		}
		scannedRows = append(scannedRows, fullRow{row: rMap})
	}

	// 3. Apply ORDER BY if present
	if orderByClause != "" && len(scannedRows) > 0 {
		orderParts := strings.Fields(orderByClause)
		orderCol := cleanIdentifier(orderParts[0])
		desc := len(orderParts) > 1 && strings.EqualFold(orderParts[1], "DESC")
		sort.Slice(scannedRows, func(i, j int) bool {
			v1 := fmt.Sprintf("%v", scannedRows[i].row[orderCol])
			v2 := fmt.Sprintf("%v", scannedRows[j].row[orderCol])
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

	// 4. Apply OFFSET & LIMIT if present
	if offsetCount > 0 {
		if offsetCount >= len(scannedRows) {
			scannedRows = nil
		} else {
			scannedRows = scannedRows[offsetCount:]
		}
	}
	if limitCount >= 0 && len(scannedRows) > limitCount {
		scannedRows = scannedRows[:limitCount]
	}

	// 5. Final projection down to selectedCols
	rows := make([]map[string]any, 0, len(scannedRows))
	for _, sr := range scannedRows {
		row := make(map[string]any)
		for _, pi := range projItems {
			if pi.parsed != nil {
				if v, err := proc.EvalRowExpr(pi.parsed, sr.row, nil); err == nil {
					row[pi.alias] = v
					continue
				}
			}
			if v, ok := sr.row[pi.alias]; ok {
				row[pi.alias] = v
			} else if v, ok := sr.row[pi.expr]; ok {
				row[pi.alias] = v
			} else {
				cleanExpr := cleanIdentifier(pi.expr)
				if v, ok := sr.row[cleanExpr]; ok {
					row[pi.alias] = v
				} else {
					colPart := cleanExpr
					if idx := strings.LastIndex(cleanExpr, "."); idx != -1 {
						colPart = cleanExpr[idx+1:]
					}
					if v, ok := sr.row[colPart]; ok {
						row[pi.alias] = v
					} else {
						row[pi.alias] = nil
					}
				}
			}
		}
		rows = append(rows, row)
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      selectedCols,
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

type aggItem struct {
	RawStr   string
	Alias    string
	FuncType string // "COUNT", "SUM", "AVG", "MIN", "MAX", "NONE"
	ArgCol   string
}

func parseAggItem(proj string) aggItem {
	proj = strings.TrimSpace(proj)
	alias := ""
	rawExpr := proj
	if idx := strings.Index(strings.ToUpper(proj), " AS "); idx != -1 {
		rawExpr = strings.TrimSpace(proj[:idx])
		alias = cleanIdentifier(strings.TrimSpace(proj[idx+4:]))
	} else if tokens := strings.Fields(proj); len(tokens) == 2 && !strings.Contains(tokens[1], "(") {
		rawExpr = strings.TrimSpace(tokens[0])
		alias = cleanIdentifier(tokens[1])
	}

	upper := strings.ToUpper(rawExpr)
	var funcType, argCol string
	if strings.HasPrefix(upper, "COUNT(") && strings.HasSuffix(upper, ")") {
		funcType = "COUNT"
		argCol = cleanIdentifier(strings.TrimSpace(rawExpr[6 : len(rawExpr)-1]))
	} else if strings.HasPrefix(upper, "SUM(") && strings.HasSuffix(upper, ")") {
		funcType = "SUM"
		argCol = cleanIdentifier(strings.TrimSpace(rawExpr[4 : len(rawExpr)-1]))
	} else if strings.HasPrefix(upper, "AVG(") && strings.HasSuffix(upper, ")") {
		funcType = "AVG"
		argCol = cleanIdentifier(strings.TrimSpace(rawExpr[4 : len(rawExpr)-1]))
	} else if strings.HasPrefix(upper, "MIN(") && strings.HasSuffix(upper, ")") {
		funcType = "MIN"
		argCol = cleanIdentifier(strings.TrimSpace(rawExpr[4 : len(rawExpr)-1]))
	} else if strings.HasPrefix(upper, "MAX(") && strings.HasSuffix(upper, ")") {
		funcType = "MAX"
		argCol = cleanIdentifier(strings.TrimSpace(rawExpr[4 : len(rawExpr)-1]))
	} else {
		funcType = "NONE"
		argCol = cleanIdentifier(rawExpr)
	}

	return aggItem{
		RawStr:   rawExpr,
		Alias:    alias,
		FuncType: funcType,
		ArgCol:   argCol,
	}
}

func (s *Coordinator) execAggregate(rows []map[string]any, qInfo *planner.QueryInfo, start time.Time) (*QueryResult, error) {
	// 1. Parse projections into aggItems
	var items []aggItem
	var colHeaders []string
	for _, p := range qInfo.Projections {
		item := parseAggItem(p)
		items = append(items, item)
		header := item.Alias
		if header == "" {
			header = item.RawStr
		}
		colHeaders = append(colHeaders, header)
	}

	// 2. Grouping
	type groupBucket struct {
		keys map[string]any
		rows []map[string]any
	}

	var groupOrder []string
	groups := make(map[string]*groupBucket)

	if len(qInfo.GroupByCols) == 0 {
		groupOrder = append(groupOrder, "")
		groups[""] = &groupBucket{
			keys: make(map[string]any),
			rows: rows,
		}
	} else {
		for _, r := range rows {
			var keyParts []string
			keyMap := make(map[string]any)
			for _, gCol := range qInfo.GroupByCols {
				gClean := cleanIdentifier(gCol)
				var v any
				if val, ok := r[gClean]; ok {
					v = val
				} else {
					bare := gClean
					if idx := strings.Index(gClean, "."); idx != -1 {
						bare = gClean[idx+1:]
					}
					v = r[bare]
				}
				keyParts = append(keyParts, fmt.Sprintf("%v", v))
				keyMap[gClean] = v
				if idx := strings.Index(gClean, "."); idx != -1 {
					keyMap[gClean[idx+1:]] = v
				}
			}
			compositeKey := strings.Join(keyParts, "|")
			if _, exists := groups[compositeKey]; !exists {
				groups[compositeKey] = &groupBucket{
					keys: keyMap,
					rows: make([]map[string]any, 0),
				}
				groupOrder = append(groupOrder, compositeKey)
			}
			groups[compositeKey].rows = append(groups[compositeKey].rows, r)
		}
	}

	// 3. Compute Aggregates per bucket
	var aggRows []map[string]any

	for _, gKey := range groupOrder {
		bucket := groups[gKey]
		aggRow := make(map[string]any)
		if len(bucket.rows) > 0 {
			for k, v := range bucket.rows[0] {
				aggRow[k] = v
			}
		}
		for k, v := range bucket.keys {
			aggRow[k] = v
		}

		for _, item := range items {
			var val any
			switch item.FuncType {
			case "COUNT":
				if item.ArgCol == "*" || item.ArgCol == "" {
					val = int64(len(bucket.rows))
				} else {
					var count int64 = 0
					for _, r := range bucket.rows {
						v := r[item.ArgCol]
						if v == nil {
							if dotIdx := strings.Index(item.ArgCol, "."); dotIdx != -1 {
								v = r[item.ArgCol[dotIdx+1:]]
							}
						}
						if v != nil {
							count++
						}
					}
					val = count
				}
			case "SUM":
				var sum float64 = 0
				var decSum storage.Decimal
				isDec := false
				hasVal := false
				for _, r := range bucket.rows {
					v := r[item.ArgCol]
					if v == nil {
						if dotIdx := strings.Index(item.ArgCol, "."); dotIdx != -1 {
							v = r[item.ArgCol[dotIdx+1:]]
						}
					}
					if v != nil {
						strVal := fmt.Sprintf("%v", v)
						if strings.Contains(strVal, ".") {
							isDec = true
						}
						if d, err := storage.ParseDecimal(strVal); err == nil {
							decSum = decSum.Add(d)
							hasVal = true
						} else if n, err := strconv.ParseFloat(strVal, 64); err == nil {
							sum += n
							hasVal = true
						}
					}
				}
				if hasVal {
					if isDec {
						val = decSum.String()
					} else if sum == math.Trunc(sum) {
						val = int64(sum)
					} else {
						val = sum
					}
				} else {
					val = nil
				}
			case "AVG":
				var sum float64 = 0
				var decSum storage.Decimal
				isDec := false
				var count int64 = 0
				for _, r := range bucket.rows {
					v := r[item.ArgCol]
					if v == nil {
						if dotIdx := strings.Index(item.ArgCol, "."); dotIdx != -1 {
							v = r[item.ArgCol[dotIdx+1:]]
						}
					}
					if v != nil {
						strVal := fmt.Sprintf("%v", v)
						if strings.Contains(strVal, ".") {
							isDec = true
						}
						if d, err := storage.ParseDecimal(strVal); err == nil {
							decSum = decSum.Add(d)
							count++
						} else if n, err := strconv.ParseFloat(strVal, 64); err == nil {
							sum += n
							count++
						}
					}
				}
				if count > 0 {
					if isDec {
						cDec := storage.NewDecimalFromInt(count, 0)
						if q, err := decSum.Div(cDec, 4); err == nil {
							val = q.String()
						} else {
							val = decSum.String()
						}
					} else {
						avg := sum / float64(count)
						if avg == math.Trunc(avg) {
							val = int64(avg)
						} else {
							val = avg
						}
					}
				} else {
					val = nil
				}
			case "MIN":
				var minVal *float64
				for _, r := range bucket.rows {
					v := r[item.ArgCol]
					if v == nil {
						if dotIdx := strings.Index(item.ArgCol, "."); dotIdx != -1 {
							v = r[item.ArgCol[dotIdx+1:]]
						}
					}
					if v != nil {
						if n, err := strconv.ParseFloat(fmt.Sprintf("%v", v), 64); err == nil {
							if minVal == nil || n < *minVal {
								nCopy := n
								minVal = &nCopy
							}
						}
					}
				}
				if minVal != nil {
					if *minVal == math.Trunc(*minVal) {
						val = int64(*minVal)
					} else {
						val = *minVal
					}
				} else {
					val = nil
				}
			case "MAX":
				var maxVal *float64
				for _, r := range bucket.rows {
					v := r[item.ArgCol]
					if v == nil {
						if dotIdx := strings.Index(item.ArgCol, "."); dotIdx != -1 {
							v = r[item.ArgCol[dotIdx+1:]]
						}
					}
					if v != nil {
						if n, err := strconv.ParseFloat(fmt.Sprintf("%v", v), 64); err == nil {
							if maxVal == nil || n > *maxVal {
								nCopy := n
								maxVal = &nCopy
							}
						}
					}
				}
				if maxVal != nil {
					if *maxVal == math.Trunc(*maxVal) {
						val = int64(*maxVal)
					} else {
						val = *maxVal
					}
				} else {
					val = nil
				}
			default:
				if v, ok := bucket.keys[item.ArgCol]; ok {
					val = v
				} else if len(bucket.rows) > 0 {
					val = bucket.rows[0][item.ArgCol]
				}
			}

			header := item.Alias
			if header == "" {
				header = item.RawStr
			}
			aggRow[header] = val
			aggRow[item.RawStr] = val
			if item.Alias != "" {
				aggRow[item.Alias] = val
			}
			if item.FuncType != "NONE" {
				aggRow[strings.ToLower(item.RawStr)] = val
				aggRow[strings.ToUpper(item.RawStr)] = val
			}
		}

		// 4. Evaluate HAVING clause
		if qInfo.HavingClause != "" && !evaluateWhereConditionMap(aggRow, qInfo.HavingClause) {
			continue
		}

		// Collect aggregated row
		aggRows = append(aggRows, aggRow)
	}

	// 5. Apply ORDER BY
	if qInfo.OrderByClause != "" && len(aggRows) > 0 {
		orderParts := strings.Fields(qInfo.OrderByClause)
		orderCol := cleanIdentifier(orderParts[0])
		bareCol := orderCol
		if idx := strings.Index(bareCol, "."); idx != -1 {
			bareCol = bareCol[idx+1:]
		}
		desc := len(orderParts) > 1 && strings.EqualFold(orderParts[1], "DESC")
		sort.SliceStable(aggRows, func(i, j int) bool {
			v1, ok1 := aggRows[i][orderCol]
			if !ok1 || v1 == nil {
				v1 = aggRows[i][bareCol]
			}
			v2, ok2 := aggRows[j][orderCol]
			if !ok2 || v2 == nil {
				v2 = aggRows[j][bareCol]
			}
			s1 := fmt.Sprintf("%v", v1)
			s2 := fmt.Sprintf("%v", v2)
			if n1, err1 := strconv.ParseFloat(s1, 64); err1 == nil {
				if n2, err2 := strconv.ParseFloat(s2, 64); err2 == nil {
					if desc {
						return n1 > n2
					}
					return n1 < n2
				}
			}
			if desc {
				return s1 > s2
			}
			return s1 < s2
		})
	}

	// 6. Apply OFFSET & LIMIT
	if qInfo.OffsetCount > 0 {
		if int64(len(aggRows)) > qInfo.OffsetCount {
			aggRows = aggRows[qInfo.OffsetCount:]
		} else {
			aggRows = nil
		}
	}
	if qInfo.LimitCount >= 0 && int64(len(aggRows)) > qInfo.LimitCount {
		aggRows = aggRows[:qInfo.LimitCount]
	}

	// 7. Project final rows strictly to colHeaders
	finalRows := make([]map[string]any, len(aggRows))
	for i, r := range aggRows {
		fr := make(map[string]any, len(colHeaders))
		for _, h := range colHeaders {
			fr[h] = r[h]
		}
		finalRows[i] = fr
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      colHeaders,
		Rows:         finalRows,
		RowsAffected: int64(len(finalRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}
