package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

		"sqlion/pkg/execution"
	"sqlion/pkg/proc"
	"sqlion/pkg/replication"
	"sqlion/pkg/storage"
)

// QueryResult represents the outcome of a SQL statement execution (aliased from engine.QueryResult).

var (
	reTblVarDecl   = regexp.MustCompile(`(?is)^DECLARE\s+@[a-zA-Z0-9_]+\s+TABLE\b`)
	reKillCmd      = regexp.MustCompile(`(?is)^KILL\s+(\d+)(?:\s+WITH\s+STATUSONLY)?$`)
	reUseCmd       = regexp.MustCompile(`(?is)^USE\s+(?:\[([^\]]+)\]|"([^"]+)"|'([^']+)'|([a-zA-Z0-9_.-]+))\s*;?$`)
	reSetConfig    = regexp.MustCompile(`(?is)^(?:SESSION\s+)?([a-zA-Z0-9_]+)\s*(?:TO|=)\s*(.*)$`)
	reShowParts    = regexp.MustCompile(`(?is)^SHOW\s+PARTITIONS(?:\s+FROM)?\s+([^\s;]+)\s*;?$`)
	reShowParam    = regexp.MustCompile(`(?is)^SHOW\s+(.*?)\s*;?$`)
	reSelectSysVar = regexp.MustCompile(`(?is)^SELECT\s+@@([a-zA-Z0-9_]+)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?$`)
	reTblVarCreate = regexp.MustCompile(`(?is)^DECLARE\s+(@[a-zA-Z0-9_]+)\s+TABLE\s*\((.*)\)\s*;?$`)
	reSetVar       = regexp.MustCompile(`(?is)^SET\s+@([a-zA-Z0-9_]+)\s*(?::=|=)\s*(.*)$`)
	reSelectVar    = regexp.MustCompile(`(?is)^SELECT\s+(@[a-zA-Z0-9_]+)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?$`)
)

func parseSessionVarValue(s string) any {
	return ParseSessionVarValue(s)
}

func isIdentChar(ch byte) bool {
	return IsIdentChar(ch)
}

func splitStatements(query string) []string {
	return SplitStatements(query)
}

// parseTimeoutDuration parses duration strings with units (ms, s, m) or plain integers as milliseconds.
func parseTimeoutDuration(s string) (time.Duration, error) {
	return ParseTimeoutDuration(s)
}

// ExecuteSQL parses and runs one or more SQL statements against the engine and catalog on the default session.
func (s *Coordinator) ExecuteSQL(query string) (*QueryResult, error) {
	return s.ExecuteSessionSQL("default", query)
}

// ExecuteSessionSQLContext runs SQL statements within a specific client session and cancellation context.
func (s *Coordinator) ExecuteSessionSQLContext(ctx context.Context, sessionID string, query string) (*QueryResult, error) {
	// Acquire worker slot from built-in connection pool to prevent thread thrashing
	if s.Pool() != nil {
		if err := s.Pool().AcquireWorker(ctx); err != nil {
			return nil, err
		}
		defer s.Pool().ReleaseWorker()
	}

	cs, err := s.GetOrCreateSession(sessionID)
	if err != nil {
		return nil, err
	}

	overallStart := time.Now()
	var queryCtx context.Context
	var cancel context.CancelFunc

	if timeout := cs.GetQueryTimeout(); timeout > 0 {
		queryCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		queryCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	cs.Mu.Lock()
	cs.QueryCtx = queryCtx
	cs.CancelFunc = cancel
	cs.RunningQuery = query
	cs.QueryStartTime = overallStart
	cs.CancelRequested = false
	cs.Mu.Unlock()

	stmts := splitStatements(query)
	var lastRes *QueryResult
	var execErr error

	defer func() {
		dur := time.Since(overallStart)
		cmd := "UNKNOWN"
		rowsAffected := int64(0)
		isErr := (execErr != nil) || (lastRes != nil && !lastRes.Success)
		if lastRes != nil {
			if lastRes.Command != "" {
				cmd = lastRes.Command
			}
			rowsAffected = lastRes.RowsAffected
		} else if len(stmts) > 0 {
			fields := strings.Fields(stmts[0])
			if len(fields) > 0 {
				cmd = strings.ToUpper(fields[0])
			}
		}

		isSlow := false
		if s.SlowQueries() != nil {
			isSlow = s.SlowQueries().Record(sessionID, query, dur, rowsAffected, execErr)
		}
		if s.GeneralLog() != nil {
			s.GeneralLog().Record(sessionID, query, dur, rowsAffected, execErr)
		}
		if s.Metrics() != nil {
			s.Metrics().RecordQuery(cmd, dur, isErr, isSlow)
		}

		cs.Mu.Lock()
		cs.QueryCtx = nil
		cs.CancelFunc = nil
		cs.RunningQuery = ""
		cs.Mu.Unlock()
	}()

	if len(stmts) == 0 {
		lastRes = &QueryResult{Success: true, Command: "EMPTY"}
		return lastRes, nil
	}

	for _, stmt := range stmts {
		select {
		case <-queryCtx.Done():
			if errors.Is(queryCtx.Err(), context.DeadlineExceeded) {
				execErr = fmt.Errorf("canceling statement due to statement timeout")
			} else {
				execErr = fmt.Errorf("canceling statement due to user request")
			}
			return nil, execErr
		default:
		}

		// In PostgreSQL mode (sessions prefixed with "pg-"), if transaction has failed, reject all commands except ROLLBACK
		if cs != nil && strings.HasPrefix(cs.ID, "pg-") {
			cs.Mu.Lock()
			failed := cs.TxFailed
			cs.Mu.Unlock()
			if failed && !isRollbackOrResetQuery(stmt) {
				execErr = fmt.Errorf("current transaction is aborted, commands ignored until end of transaction block")
				return nil, execErr
			}
		}

		res, err := s.execSingleStatement(cs, stmt)
		if err != nil {
			if cs != nil {
				cs.Mu.Lock()
				cs.Vars["error"] = int64(50000)
				if cs.Tx != nil && strings.HasPrefix(cs.ID, "pg-") {
					cs.TxFailed = true
				}
				cs.Mu.Unlock()
			}
			if errors.Is(err, context.DeadlineExceeded) || (queryCtx.Err() != nil && errors.Is(queryCtx.Err(), context.DeadlineExceeded)) {
				execErr = fmt.Errorf("canceling statement due to statement timeout")
				return nil, execErr
			}
			if errors.Is(err, context.Canceled) || (queryCtx.Err() != nil && errors.Is(queryCtx.Err(), context.Canceled)) {
				execErr = fmt.Errorf("canceling statement due to user request")
				return nil, execErr
			}
			execErr = err
			lastRes = res
			return res, err
		}
		if cs != nil {
			cs.Mu.Lock()
			cs.Vars["error"] = int64(0)
			cs.Mu.Unlock()
		}
		lastRes = res
	}
	return lastRes, nil
}

// ExecuteSessionSQL runs SQL statements within a specific client session context.
func (s *Coordinator) ExecuteSessionSQL(sessionID string, query string) (*QueryResult, error) {
	return s.ExecuteSessionSQLContext(context.Background(), sessionID, query)
}

func (s *Coordinator) execSingleStatement(cs *ClientSession, query string) (*QueryResult, error) {
	start := time.Now()
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return &QueryResult{Success: true, Command: "EMPTY", DurationUS: time.Since(start).Microseconds()}, nil
	}

	upper := strings.ToUpper(trimmed)

	// Rewrite table variables (@table -> physical table)
	if cs != nil {
		cs.Mu.Lock()
		if len(cs.TableVars) > 0 && !reTblVarDecl.MatchString(trimmed) {
			for varName, physTable := range cs.TableVars {
				pattern := `(?i)@` + regexp.QuoteMeta(strings.TrimPrefix(varName, "@")) + `\b`
				trimmed = getCompiledRegexp(pattern).ReplaceAllString(trimmed, physTable)
			}
			upper = strings.ToUpper(trimmed)
		}
		cs.Mu.Unlock()
	}

	// Rewrite session-isolated local temporary tables (#temp -> __temp_<sessionID>_temp)
	if cs != nil && strings.Contains(trimmed, "#") {
		trimmed = RewriteSessionTempTables(trimmed, cs)
		upper = strings.ToUpper(trimmed)
	}

	// Process Management: KILL <spid> [WITH STATUSONLY]
	if m := reKillCmd.FindStringSubmatch(trimmed); len(m) == 2 {
		spid, _ := strconv.Atoi(m[1])
		canceled := s.CancelQueryBySPID(spid)
		msg := "Command(s) completed successfully."
		if !canceled {
			msg = fmt.Sprintf("SPID %d was not active or already finished.", spid)
		}
		return &QueryResult{
			Success:    true,
			Command:    "KILL",
			Messages:   []string{msg},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	// 0.005 Database Context Switching: USE <database> (Tolerant Virtual No-Op)
	if m := reUseCmd.FindStringSubmatch(trimmed); len(m) > 0 {
		var dbName string
		for i := 1; i < len(m); i++ {
			if m[i] != "" {
				dbName = m[i]
				break
			}
		}
		if dbName == "" {
			dbName = "master"
		}
		if cs != nil {
			cs.Mu.Lock()
			cs.Database = dbName
			cs.Vars["current_database"] = dbName
			cs.Mu.Unlock()
		}

		return &QueryResult{
			Success:    true,
			Command:    "USE",
			Messages:   []string{fmt.Sprintf("Database context set to %s.", dbName)},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	// 0. Interactive Transactions: BEGIN / COMMIT / ROLLBACK
	isBeginTx := upper == "BEGIN" ||
		strings.HasPrefix(upper, "BEGIN TRANSACTION") ||
		strings.HasPrefix(upper, "BEGIN TRAN") ||
		strings.HasPrefix(upper, "BEGIN WORK") ||
		strings.HasPrefix(upper, "BEGIN READ") ||
		strings.HasPrefix(upper, "BEGIN ISOLATION") ||
		strings.HasPrefix(upper, "BEGIN DEFERRABLE") ||
		strings.HasPrefix(upper, "BEGIN NOT DEFERRABLE") ||
		strings.HasPrefix(upper, "START TRANSACTION")

	if isBeginTx {
		cs.Mu.Lock()
		defer cs.Mu.Unlock()
		if cs.Tx != nil {
			return nil, fmt.Errorf("transaction already active on session %s", cs.ID)
		}
		opts := storage.TxOptions{}
		if ro, ok := cs.Vars["transaction_read_only"].(bool); ok && ro {
			opts.ReadOnly = true
		}
		if s.IsReadOnly() {
			opts.ReadOnly = true
		}
		if strings.Contains(upper, "READ ONLY") {
			opts.ReadOnly = true
		} else if strings.Contains(upper, "READ WRITE") {
			if s.IsReadOnly() {
				return nil, fmt.Errorf("cannot start read-write transaction on read-only replica: %w", replication.ErrReadOnlyReplica)
			}
			opts.ReadOnly = false
		}
		tx, err := cs.Session.BeginTx(opts)
		if err != nil {
			return nil, fmt.Errorf("failed to start transaction: %w", err)
		}
		cs.Tx = tx
		cs.TxFailed = false
		return &QueryResult{
			Success:    true,
			Command:    "BEGIN",
			Messages:   []string{"Transaction started."},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	if strings.HasPrefix(upper, "COMMIT") {
		cs.Mu.Lock()
		defer cs.Mu.Unlock()
		if cs.Tx == nil {
			return nil, fmt.Errorf("no active transaction to commit on session %s", cs.ID)
		}
		err := cs.Tx.Commit()
		cs.Tx = nil
		cs.TxFailed = false
		// Close non-WITH HOLD cursors on COMMIT
		if cs.Cursors != nil {
			for name, cur := range cs.Cursors {
				if !cur.WithHold {
					delete(cs.Cursors, name)
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("commit error: %w", err)
		}
		if s.Metrics() != nil {
			s.Metrics().RecordTransaction("commit")
		}
		return &QueryResult{
			Success:    true,
			Command:    "COMMIT",
			Messages:   []string{"Transaction committed."},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	if namePart, ok := trimAnyPrefix(upper, trimmed, "SAVE TRANSACTION ", "SAVE TRAN ", "SAVEPOINT "); ok {
		cs.Mu.Lock()
		defer cs.Mu.Unlock()
		if cs.Tx == nil {
			return nil, fmt.Errorf("no active transaction to create savepoint on session %s", cs.ID)
		}
		spName := cleanSavepointName(namePart)
		if spName == "" {
			return nil, fmt.Errorf("syntax error: savepoint name required")
		}
		if err := cs.Tx.Savepoint(spName); err != nil {
			return nil, fmt.Errorf("savepoint error: %w", err)
		}
		return &QueryResult{
			Success:    true,
			Command:    "SAVEPOINT",
			Messages:   []string{fmt.Sprintf("Savepoint %s created.", spName)},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	if namePart, ok := trimAnyPrefix(upper, trimmed, "RELEASE SAVEPOINT ", "RELEASE "); ok {
		cs.Mu.Lock()
		defer cs.Mu.Unlock()
		if cs.Tx == nil {
			return nil, fmt.Errorf("no active transaction to release savepoint on session %s", cs.ID)
		}
		spName := cleanSavepointName(namePart)
		if spName == "" {
			return nil, fmt.Errorf("syntax error: savepoint name required")
		}
		if err := cs.Tx.ReleaseSavepoint(spName); err != nil {
			return nil, fmt.Errorf("release savepoint error: %w", err)
		}
		return &QueryResult{
			Success:    true,
			Command:    "RELEASE",
			Messages:   []string{fmt.Sprintf("Savepoint %s released.", spName)},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	if strings.HasPrefix(upper, "ROLLBACK") {
		cs.Mu.Lock()
		defer cs.Mu.Unlock()
		if cs.Tx == nil {
			return nil, fmt.Errorf("no active transaction to rollback on session %s", cs.ID)
		}

		var spName string
		if rest, ok := trimAnyPrefix(upper, trimmed, "ROLLBACK WORK TO SAVEPOINT ", "ROLLBACK TO SAVEPOINT ", "ROLLBACK TO "); ok {
			spName = cleanSavepointName(rest)
		} else if rest, ok := trimAnyPrefix(upper, trimmed, "ROLLBACK TRANSACTION ", "ROLLBACK TRAN "); ok {
			rem := cleanSavepointName(rest)
			if rem != "" && !strings.EqualFold(rem, "WORK") {
				spName = rem
			}
		}

		if spName != "" {
			err := cs.Tx.RollbackTo(spName)
			if err != nil {
				return nil, fmt.Errorf("rollback to savepoint error: %w", err)
			}
			return &QueryResult{
				Success:    true,
				Command:    "ROLLBACK",
				Messages:   []string{fmt.Sprintf("Rolled back to savepoint %s.", spName)},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}

		err := cs.Tx.Rollback()
		cs.Tx = nil
		cs.TxFailed = false
		// Close all cursors on ROLLBACK (per PostgreSQL specification)
		if cs.Cursors != nil {
			cs.Cursors = make(map[string]*SessionCursor)
		}
		if err != nil {
			return nil, fmt.Errorf("rollback error: %w", err)
		}
		if s.Metrics() != nil {
			s.Metrics().RecordTransaction("rollback")
		}
		return &QueryResult{
			Success:    true,
			Command:    "ROLLBACK",
			Messages:   []string{"Transaction rolled back."},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	// 0.045 Connection Pool Reset: DISCARD ALL / RESET ALL / DEALLOCATE ALL / UNLISTEN *
	if strings.HasPrefix(upper, "DISCARD ") || upper == "RESET ALL" || strings.HasPrefix(upper, "DEALLOCATE ALL") || strings.HasPrefix(upper, "UNLISTEN ") {
		return &QueryResult{
			Success:    true,
			Command:    "RESET",
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	// 0.046 T-SQL & Standard Configuration Parameters: SET
	if strings.HasPrefix(upper, "SET ") && !strings.HasPrefix(trimmed, "SET @") {
		rest := strings.TrimSpace(trimmed[4:])
		upperRest := strings.ToUpper(rest)

		// A. SET TRANSACTION ISOLATION LEVEL <level>
		if strings.HasPrefix(upperRest, "TRANSACTION ISOLATION LEVEL ") {
			level := strings.TrimSpace(rest[len("TRANSACTION ISOLATION LEVEL "):])
			cs.Mu.Lock()
			cs.Vars["transaction_isolation"] = strings.ToLower(level)
			cs.Mu.Unlock()
			return &QueryResult{
				Success:    true,
				Command:    "SET",
				Messages:   []string{fmt.Sprintf("Transaction isolation level set to %s.", level)},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}

		// B. SET TRANSACTION READ ONLY / READ WRITE
		if strings.HasPrefix(upperRest, "TRANSACTION READ ") {
			mode := strings.TrimSpace(rest[len("TRANSACTION READ "):])
			cs.Mu.Lock()
			cs.Vars["transaction_read_only"] = strings.EqualFold(mode, "ONLY")
			cs.Mu.Unlock()
			return &QueryResult{
				Success:    true,
				Command:    "SET",
				Messages:   []string{fmt.Sprintf("Transaction access mode set to READ %s.", mode)},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}

		// C. SET TIME ZONE <tz>
		if strings.HasPrefix(upperRest, "TIME ZONE ") || strings.HasPrefix(upperRest, "TIMEZONE ") {
			idx := strings.Index(rest, " ")
			if strings.HasPrefix(upperRest, "TIME ZONE ") {
				idx = len("TIME ZONE ")
			}
			tz := strings.Trim(strings.TrimSpace(rest[idx:]), "=';\" ")
			cs.Mu.Lock()
			cs.Vars["timezone"] = tz
			cs.Mu.Unlock()
			return &QueryResult{
				Success:    true,
				Command:    "SET",
				Messages:   []string{fmt.Sprintf("Time zone set to %s.", tz)},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}

		// D. SET NAMES <enc>
		if strings.HasPrefix(upperRest, "NAMES ") {
			enc := strings.Trim(strings.TrimSpace(rest[len("NAMES "):]), "=';\" ")
			cs.Mu.Lock()
			cs.Vars["client_encoding"] = enc
			cs.Mu.Unlock()
			return &QueryResult{
				Success:    true,
				Command:    "SET",
				Messages:   []string{fmt.Sprintf("Client encoding set to %s.", enc)},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}

		// E0. SET IDENTITY_INSERT [schema.]table ON|OFF
		if strings.HasPrefix(strings.ToUpper(rest), "IDENTITY_INSERT") {
			parts := strings.Fields(rest)
			if len(parts) >= 3 {
				targetTable := cleanIdentifier(parts[1])
				state := strings.ToUpper(strings.Trim(parts[2], ";'\""))
				if state != "ON" && state != "OFF" {
					return nil, fmt.Errorf("syntax error: SET IDENTITY_INSERT requires ON or OFF")
				}

				schema, err := s.engine.GetTable(targetTable)
				if err != nil {
					return nil, fmt.Errorf("table %q not found: %w", targetTable, err)
				}
				hasIdentity := false
				for _, c := range schema.Columns {
					if c.Identity {
						hasIdentity = true
						break
					}
				}
				if !hasIdentity {
					return nil, fmt.Errorf("table %q does not have the identity property. Cannot use SET IDENTITY_INSERT", targetTable)
				}

				if cs != nil {
					cs.Mu.Lock()
					if state == "ON" {
						if cur, ok := cs.Vars["identity_insert_table"].(string); ok && cur != "" && !strings.EqualFold(cur, targetTable) {
							cs.Mu.Unlock()
							return nil, fmt.Errorf("cannot SET IDENTITY_INSERT ON for table %q when IDENTITY_INSERT is already ON for table %q", targetTable, cur)
						}
						cs.Vars["identity_insert_table"] = strings.ToLower(targetTable)
					} else {
						delete(cs.Vars, "identity_insert_table")
					}
					cs.Mu.Unlock()
				}

				return &QueryResult{
					Success:    true,
					Command:    "SET",
					Messages:   []string{fmt.Sprintf("SET IDENTITY_INSERT for %q is %s.", targetTable, state)},
					DurationUS: time.Since(start).Microseconds(),
				}, nil
			}
		}

		// E. SET <option> ON / OFF / <val> (e.g. SET NOCOUNT ON, SET ANSI_NULLS ON, SET NOCOUNT, ANSI_NULLS ON)
		if !strings.Contains(rest, "=") && !strings.Contains(strings.ToUpper(rest), " TO ") {
			rawItems := strings.Split(rest, ",")
			var msgs []string
			cs.Mu.Lock()
			for _, item := range rawItems {
				item = strings.TrimSpace(item)
				if item == "" {
					continue
				}
				fields := strings.Fields(item)
				if len(fields) == 0 {
					continue
				}
				optName := strings.ToLower(cleanIdentifier(fields[0]))
				optVal := "ON"
				if len(fields) > 1 {
					optVal = strings.Trim(strings.Join(fields[1:], " "), "';\"")
				}
				if strings.EqualFold(optVal, "ON") {
					cs.Vars[optName] = true
				} else if strings.EqualFold(optVal, "OFF") {
					cs.Vars[optName] = false
				} else {
					cs.Vars[optName] = optVal
				}
				msgs = append(msgs, fmt.Sprintf("SET %s %s.", optName, optVal))
			}
			cs.Mu.Unlock()
			if len(msgs) > 0 {
				return &QueryResult{
					Success:    true,
					Command:    "SET",
					Messages:   msgs,
					DurationUS: time.Since(start).Microseconds(),
				}, nil
			}
		}

		// F. Standard SET [SESSION] param [= | TO] value
		if m := reSetConfig.FindStringSubmatch(rest); len(m) == 3 {
			param := strings.ToLower(cleanIdentifier(m[1]))
			valStr := strings.Trim(strings.TrimSpace(m[2]), "';\"")
			if param == "work_mem" {
				bytes, err := execution.ParseMemSize(valStr)
				if err != nil {
					return nil, fmt.Errorf("invalid value for work_mem %q: %w", valStr, err)
				}
				cs.Mu.Lock()
				cs.Vars["work_mem"] = bytes
				cs.Mu.Unlock()
				return &QueryResult{
					Success:    true,
					Command:    "SET",
					Messages:   []string{fmt.Sprintf("SET: work_mem set to %s (%d bytes).", execution.FormatMemSize(bytes), bytes)},
					DurationUS: time.Since(start).Microseconds(),
				}, nil
			}
			cs.Mu.Lock()
			cs.Vars[param] = valStr
			cs.Mu.Unlock()
			return &QueryResult{
				Success:    true,
				Command:    "SET",
				Messages:   []string{fmt.Sprintf("SET: %s set to %s.", param, valStr)},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}
	}

	// 0.05 SHOW PARTITIONS [FROM] <table>
	if m := reShowParts.FindStringSubmatch(trimmed); len(m) == 2 {
		return s.execShowPartitions(cleanIdentifier(m[1]), start)
	}

	// 0.06 SHOW <parameter>
	if m := reShowParam.FindStringSubmatch(trimmed); len(m) == 2 {
		param := strings.ToLower(strings.TrimSpace(m[1]))
		paramNorm := strings.ReplaceAll(param, " ", "_")
		cs.Mu.Lock()
		val, exists := cs.Vars[paramNorm]
		if !exists {
			val, exists = cs.Vars[param]
		}
		cs.Mu.Unlock()

		valStr := ""
		if exists {
			if paramNorm == "work_mem" {
				if b, ok := val.(int64); ok {
					valStr = execution.FormatMemSize(b)
				} else {
					valStr = fmt.Sprintf("%v", val)
				}
			} else {
				valStr = fmt.Sprintf("%v", val)
			}
		} else {
			switch paramNorm {
			case "work_mem":
				valStr = execution.FormatMemSize(s.DefaultWorkMem())
			case "search_path":
				valStr = `"$user", public`
			case "transaction_isolation", "default_transaction_isolation", "transaction_isolation_level":
				valStr = "read committed"
			case "server_version":
				valStr = "15.0 (SQLion)"
			case "server_encoding", "client_encoding":
				valStr = "UTF8"
			case "timezone", "time_zone":
				valStr = "UTC"
			case "standard_conforming_strings":
				valStr = "on"
			case "integer_datetimes":
				valStr = "on"
			case "max_connections":
				valStr = "100"
			default:
				valStr = "off"
			}
		}

		return &QueryResult{
			Success:      true,
			Command:      "SHOW",
			Columns:      []string{param},
			Rows:         []map[string]any{{param: valStr}},
			RowsAffected: 1,
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	// 0.07 System variable query: SELECT @@var [AS alias]
	if m := reSelectSysVar.FindStringSubmatch(trimmed); len(m) >= 2 {
		varName := strings.ToLower(m[1])
		colHeader := "@@" + varName
		if len(m) > 2 && m[2] != "" {
			colHeader = m[2]
		}
		val, _ := s.evalScalarExpr("@@"+varName, cs)
		return &QueryResult{
			Success:      true,
			Command:      "SELECT",
			Columns:      []string{colHeader},
			Rows:         []map[string]any{{colHeader: val}},
			RowsAffected: 1,
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	// 0.075 Server-Side SQL Cursors: DECLARE <name> CURSOR FOR <query>
	if isDeclareCursorQuery(trimmed) {
		return s.execDeclareCursor(trimmed, start, cs)
	}

	// 0.076 Server-Side SQL Cursors: FETCH
	if strings.HasPrefix(upper, "FETCH ") || upper == "FETCH" {
		return s.execFetchCursor(trimmed, start, cs)
	}

	// 0.077 Server-Side SQL Cursors: MOVE
	if strings.HasPrefix(upper, "MOVE ") || upper == "MOVE" {
		return s.execMoveCursor(trimmed, start, cs)
	}

	// 0.078 Server-Side SQL Cursors: CLOSE
	if strings.HasPrefix(upper, "CLOSE ") || upper == "CLOSE" {
		return s.execCloseCursor(trimmed, start, cs)
	}

	// 0.08 T-SQL DECLARE variables: DECLARE @var [type] [= expr], ... or DECLARE @table TABLE (...)
	if strings.HasPrefix(upper, "DECLARE ") {
		if m := reTblVarCreate.FindStringSubmatch(trimmed); len(m) >= 3 {
			varName := strings.ToLower(m[1])
			if !strings.HasPrefix(varName, "@") {
				varName = "@" + varName
			}
			colDefs := m[2]
			sessID := "default"
			if cs != nil && cs.ID != "" {
				sessID = cs.ID
			}
			physTable := fmt.Sprintf("__tv_%s_%s", sessID, strings.TrimPrefix(varName, "@"))

			// Clean up previous table if any
			_ = s.engine.DropTable(physTable)

			createSQL := fmt.Sprintf("CREATE TABLE %s (%s)", physTable, colDefs)
			res, err := s.execCreateTable(createSQL, start, cs)
			if err != nil {
				return nil, err
			}
			if !res.Success {
				return res, nil
			}

			if cs != nil {
				cs.Mu.Lock()
				if cs.TableVars == nil {
					cs.TableVars = make(map[string]string)
				}
				cs.TableVars[varName] = physTable
				cs.Mu.Unlock()
			}

			return &QueryResult{
				Success:    true,
				Command:    "DECLARE",
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}

		rawDecls := strings.TrimSpace(trimmed[len("DECLARE "):])
		parts := strings.Split(rawDecls, ",")
		cs.Mu.Lock()
		for _, p := range parts {
			p = strings.TrimSpace(strings.TrimRight(p, ";"))
			if !strings.HasPrefix(p, "@") {
				continue
			}
			eqParts := strings.SplitN(p, "=", 2)
			varHeader := strings.TrimSpace(eqParts[0])
			varFields := strings.Fields(varHeader)
			if len(varFields) == 0 {
				continue
			}
			varName := varFields[0]
			if !strings.HasPrefix(varName, "@") {
				varName = "@" + varName
			}
			var val any = nil
			if len(eqParts) == 2 {
				expr := strings.TrimSpace(eqParts[1])
				for k, v := range cs.Vars {
					expr = strings.ReplaceAll(expr, k, fmt.Sprintf("%v", v))
				}
				val = parseSessionVarValue(expr)
			}
			cs.Vars[varName] = val
		}
		cs.Mu.Unlock()
		return &QueryResult{
			Success:    true,
			Command:    "DECLARE",
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	// 0.1 Session Variables: SET @var = expr / SET @var := expr
	if m := reSetVar.FindStringSubmatch(trimmed); len(m) == 3 {
		cs.Mu.Lock()
		defer cs.Mu.Unlock()
		varName := m[1]
		expr := strings.TrimSpace(m[2])
		expr = strings.TrimRight(expr, ";")
		for k, v := range cs.Vars {
			expr = strings.ReplaceAll(expr, k, fmt.Sprintf("%v", v))
		}
		val := parseSessionVarValue(expr)
		cs.Vars["@"+varName] = val
		return &QueryResult{
			Success:    true,
			Command:    "SET",
			Messages:   []string{fmt.Sprintf("Variable @%s set to %v.", varName, val)},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	// 0.2 Query Session Variable: SELECT @var [AS alias]
	if m := reSelectVar.FindStringSubmatch(trimmed); len(m) >= 2 {
		varName := m[1]
		colHeader := varName
		if len(m) > 2 && m[2] != "" {
			colHeader = m[2]
		}
		cs.Mu.Lock()
		val := cs.Vars[varName]
		cs.Mu.Unlock()
		return &QueryResult{
			Success:      true,
			Command:      "SELECT",
			Columns:      []string{colHeader},
			Rows:         []map[string]any{{colHeader: val, varName: val}},
			RowsAffected: 1,
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	// 0.25 Prepared Statements: PREPARE / DEALLOCATE / DROP PREPARE
	if strings.HasPrefix(upper, "PREPARE ") {
		return s.execPrepare(trimmed, start, cs)
	}
	if strings.HasPrefix(upper, "DEALLOCATE ") || strings.HasPrefix(upper, "DROP PREPARE ") {
		return s.execDeallocate(trimmed, start, cs)
	}

	// 0.3 Substitute session variables in SQL query if present
	cs.Mu.Lock()
	if len(cs.Vars) > 0 {
		keys := make([]string, 0, len(cs.Vars))
		for k := range cs.Vars {
			if strings.HasPrefix(k, "@") {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			return len(keys[i]) > len(keys[j])
		})

		for _, k := range keys {
			v := cs.Vars[k]
			varRep := fmt.Sprintf("'%v'", v)
			switch v.(type) {
			case int, int64, int32, float64, float32, bool:
				varRep = fmt.Sprintf("%v", v)
			}
			trimmed = substituteParamOutsideQuotes(trimmed, k, varRep)
		}
		upper = strings.ToUpper(trimmed)
	}
	cs.Mu.Unlock()

	// 0.4 Query Execution Plan: EXPLAIN [ANALYZE]
	if strings.HasPrefix(upper, "EXPLAIN") {
		return s.execExplain(trimmed, start)
	}

	// 0.5 CBO Statistics Collection: ANALYZE [TABLE] [table_name]
	if strings.HasPrefix(upper, "ANALYZE") && !strings.HasPrefix(upper, "ANALYZE SELECT") {
		return s.execAnalyze(trimmed, start)
	}

	// 0.6 T-SQL Database Console Commands: DBCC
	if strings.HasPrefix(upper, "DBCC ") || upper == "DBCC" {
		return s.execDBCC(trimmed, start, cs)
	}

	// 1. Procedural DDL: CREATE PROCEDURE / ALTER PROCEDURE / CREATE FUNCTION / CREATE TRIGGER
	if strings.HasPrefix(upper, "CREATE PROCEDURE") || strings.HasPrefix(upper, "CREATE PROC") ||
		strings.HasPrefix(upper, "CREATE OR ALTER PROCEDURE") || strings.HasPrefix(upper, "CREATE OR ALTER PROC") ||
		strings.HasPrefix(upper, "ALTER PROCEDURE") || strings.HasPrefix(upper, "ALTER PROC") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execCreateProcedure(trimmed, start)
	}
	if strings.HasPrefix(upper, "CREATE FUNCTION") || strings.HasPrefix(upper, "CREATE OR ALTER FUNCTION") || strings.HasPrefix(upper, "ALTER FUNCTION") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execCreateFunction(trimmed, start)
	}
	if strings.HasPrefix(upper, "CREATE TRIGGER") || strings.HasPrefix(upper, "CREATE OR ALTER TRIGGER") || strings.HasPrefix(upper, "ALTER TRIGGER") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execCreateTrigger(trimmed, start)
	}

	// 1.5 DDL: CREATE VIEW / CREATE OR REPLACE VIEW / DROP VIEW
	if strings.HasPrefix(upper, "CREATE VIEW") || strings.HasPrefix(upper, "CREATE OR REPLACE VIEW") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execCreateView(trimmed, start)
	}
	if strings.HasPrefix(upper, "DROP VIEW") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDropView(trimmed, start)
	}

	// 1.6 RBAC DCL / Security: LOGIN, USER, GRANT, REVOKE, DENY, SETUSER, EXECUTE AS USER, REVERT
	if strings.HasPrefix(upper, "CREATE LOGIN ") {
		return s.execCreateLogin(trimmed, start)
	}
	if strings.HasPrefix(upper, "ALTER LOGIN ") {
		return s.execAlterLogin(trimmed, start)
	}
	if strings.HasPrefix(upper, "DROP LOGIN ") {
		return s.execDropLogin(trimmed, start)
	}
	if strings.HasPrefix(upper, "CREATE USER ") {
		return s.execCreateUser(trimmed, start)
	}
	if strings.HasPrefix(upper, "DROP USER ") {
		return s.execDropUser(trimmed, start)
	}
	if strings.HasPrefix(upper, "GRANT ") {
		return s.execGrant(trimmed, start)
	}
	if strings.HasPrefix(upper, "REVOKE ") {
		return s.execRevoke(trimmed, start)
	}
	if strings.HasPrefix(upper, "DENY ") {
		return s.execDeny(trimmed, start)
	}
	if strings.HasPrefix(upper, "SETUSER") || strings.HasPrefix(upper, "EXECUTE AS USER") || strings.HasPrefix(upper, "EXEC AS USER") || upper == "REVERT" || upper == "REVERT;" {
		return s.execSetUser(trimmed, start, cs)
	}

	// 2. Procedure Invocation: EXEC / CALL / sp_*
	if strings.HasPrefix(upper, "EXEC ") || strings.HasPrefix(upper, "EXECUTE ") || strings.HasPrefix(upper, "EXEC(") || strings.HasPrefix(upper, "EXECUTE(") || strings.HasPrefix(upper, "CALL ") || strings.HasPrefix(upper, "SP_") {
		return s.execProcedureCall(trimmed, start, cs)
	}

	// 3. DDL: CREATE TABLE
	if strings.HasPrefix(upper, "CREATE TABLE") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execCreateTable(trimmed, start, cs)
	}

	// 3.1 DDL: CREATE INDEX
	if strings.HasPrefix(upper, "CREATE INDEX") || strings.HasPrefix(upper, "CREATE UNIQUE INDEX") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execCreateIndex(trimmed, start)
	}

	// 3.2 DDL: CREATE STATISTICS (C-5)
	if strings.HasPrefix(upper, "CREATE STATISTICS") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execCreateStatistics(trimmed, start)
	}

	// 4. DDL: DROP TABLE
	if strings.HasPrefix(upper, "DROP TABLE") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDropTable(trimmed, start, cs)
	}

	// 4.1 DDL: DROP INDEX
	if strings.HasPrefix(upper, "DROP INDEX") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDropIndex(trimmed, start)
	}

	// 4.1.5 DDL: DROP STATISTICS (C-5)
	if strings.HasPrefix(upper, "DROP STATISTICS") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDropStatistics(trimmed, start)
	}

	// 4.2 DDL/DML: TRUNCATE TABLE
	if strings.HasPrefix(upper, "TRUNCATE TABLE") || strings.HasPrefix(upper, "TRUNCATE ") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execTruncateTable(trimmed, start)
	}

	// 4.3 DDL: ALTER TABLE
	if strings.HasPrefix(upper, "ALTER TABLE") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execAlterTable(trimmed, start, cs)
	}

	// 5. DML: INSERT INTO / UPSERT
	if strings.HasPrefix(upper, "INSERT INTO") || strings.HasPrefix(upper, "INSERT ") ||
		strings.HasPrefix(upper, "UPSERT INTO") || strings.HasPrefix(upper, "UPSERT ") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execInsert(trimmed, start, cs)
	}

	// 6. DQL: WITH (Common Table Expressions) / SELECT
	if strings.HasPrefix(upper, "WITH ") {
		if isHeidiSQLCatalogJoinQuery(trimmed) {
			return s.execHeidiSQLCatalogJoinSelect(trimmed, start)
		}
		return s.execCTE(trimmed, start)
	}
	if strings.HasPrefix(upper, "SELECT") {
		return s.execSelect(trimmed, start, cs)
	}

	// 7. DML: UPDATE
	if strings.HasPrefix(upper, "UPDATE ") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execUpdate(trimmed, start, cs)
	}

	// 8. DML: DELETE
	if strings.HasPrefix(upper, "DELETE FROM") || strings.HasPrefix(upper, "DELETE ") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDelete(trimmed, start, cs)
	}

	// 9. DML: MERGE INTO
	if strings.HasPrefix(upper, "MERGE INTO") || strings.HasPrefix(upper, "MERGE ") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execMerge(trimmed, start, cs)
	}

	return &QueryResult{
		Success:    false,
		Error:      fmt.Sprintf("unrecognized or unsupported SQL statement: %q", trimmed),
		DurationUS: time.Since(start).Microseconds(),
	}, fmt.Errorf("unsupported SQL statement")
}

// cleanIdentifier strips quotes, brackets, and prefixes from table/column names.
func cleanIdentifier(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.Index(strings.ToUpper(s), " WITH "); idx != -1 {
		s = strings.TrimSpace(s[:idx])
	}
	return proc.CleanSchemaPrefix(s)
}

// cleanSavepointName strips delimiters, semicolon, quotes, and brackets from savepoint names.
func cleanSavepointName(raw string) string {
	return CleanSavepointName(raw)
}

// trimAnyPrefix returns original sliced past the first matching prefix matched case-insensitively against upper.
func trimAnyPrefix(upper, original string, prefixes ...string) (string, bool) {
	return TrimAnyPrefix(upper, original, prefixes...)
}

// isRollbackOrResetQuery reports whether query is a transaction rollback or session reset command.
func isRollbackOrResetQuery(q string) bool {
	return IsRollbackOrResetQuery(q)
}

// checkWriteAllowed ensures that write operations (DDL/DML) cannot be executed on read-only secondary replicas.
func (s *Coordinator) checkWriteAllowed() error {
	if s.IsReadOnly() {
		return fmt.Errorf("cannot execute write operation on read-only replica: %w", replication.ErrReadOnlyReplica)
	}
	return nil
}
