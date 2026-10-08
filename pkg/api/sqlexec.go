package api

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
	"sqlion/pkg/replication"
	"sqlion/pkg/storage"
)

// QueryResult represents the outcome of a SQL statement execution.
type QueryResult struct {
	Success      bool             `json:"success"`
	Command      string           `json:"command"`
	Columns      []string         `json:"columns,omitempty"`
	Rows         []map[string]any `json:"rows,omitempty"`
	RowsAffected int64            `json:"rows_affected"`
	DurationUS   int64            `json:"duration_us"`
	Messages     []string         `json:"messages,omitempty"`
	Error        string           `json:"error,omitempty"`
}

func parseSessionVarValue(s string) any {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "NULL") {
		return nil
	}
	if strings.EqualFold(s, "TRUE") {
		return true
	}
	if strings.EqualFold(s, "FALSE") {
		return false
	}
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
		return s[1 : len(s)-1]
	}
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2 {
		return s[1 : len(s)-1]
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

func isIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '@' || ch == '#'
}

func splitStatements(query string) []string {
	var stmts []string
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)
	blockDepth := 0
	isProcOrFunc := false

	checkProc := func(s string) bool {
		sUpper := strings.ToUpper(strings.TrimSpace(s))
		return strings.HasPrefix(sUpper, "CREATE PROCEDURE") ||
			strings.HasPrefix(sUpper, "CREATE OR ALTER PROCEDURE") ||
			strings.HasPrefix(sUpper, "ALTER PROCEDURE") ||
			strings.HasPrefix(sUpper, "CREATE FUNCTION") ||
			strings.HasPrefix(sUpper, "CREATE OR ALTER FUNCTION") ||
			strings.HasPrefix(sUpper, "ALTER FUNCTION") ||
			strings.HasPrefix(sUpper, "CREATE TRIGGER") ||
			strings.HasPrefix(sUpper, "CREATE OR ALTER TRIGGER") ||
			strings.HasPrefix(sUpper, "ALTER TRIGGER")
	}

	for i := 0; i < len(query); i++ {
		ch := query[i]
		if inQuote {
			if ch == quoteChar && (i == 0 || query[i-1] != '\\') {
				inQuote = false
			}
			cur.WriteByte(ch)
		} else {
			if ch == '\'' || ch == '"' {
				inQuote = true
				quoteChar = ch
				cur.WriteByte(ch)
			} else {
				if !isProcOrFunc && checkProc(cur.String()) {
					isProcOrFunc = true
				}

				if isProcOrFunc {
					if (i == 0 || !isIdentChar(query[i-1])) && i+5 <= len(query) && strings.EqualFold(query[i:i+5], "BEGIN") && (i+5 == len(query) || !isIdentChar(query[i+5])) {
						blockDepth++
					} else if (i == 0 || !isIdentChar(query[i-1])) && i+4 <= len(query) && strings.EqualFold(query[i:i+4], "CASE") && (i+4 == len(query) || !isIdentChar(query[i+4])) {
						blockDepth++
					} else if (i == 0 || !isIdentChar(query[i-1])) && i+3 <= len(query) && strings.EqualFold(query[i:i+3], "END") && (i+3 == len(query) || !isIdentChar(query[i+3])) {
						if blockDepth > 0 {
							blockDepth--
						}
					}
				}

				if ch == ';' {
					if isProcOrFunc && blockDepth > 0 {
						cur.WriteByte(ch)
					} else {
						stmt := strings.TrimSpace(cur.String())
						if stmt != "" {
							stmts = append(stmts, stmt)
						}
						cur.Reset()
						isProcOrFunc = false
						blockDepth = 0
					}
				} else {
					cur.WriteByte(ch)
				}
			}
		}
	}
	if cur.Len() > 0 {
		stmt := strings.TrimSpace(cur.String())
		if stmt != "" {
			stmts = append(stmts, stmt)
		}
	}
	return stmts
}

// parseTimeoutDuration parses duration strings with units (ms, s, m) or plain integers as milliseconds.
func parseTimeoutDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d < 0 {
			return 0, nil
		}
		return d, nil
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		if ms <= 0 {
			return 0, nil
		}
		return time.Duration(ms) * time.Millisecond, nil
	}
	return 0, fmt.Errorf("invalid timeout duration: %s", s)
}

// getQueryTimeout returns the configured query timeout duration for this session, or 0 if disabled.
func (cs *ClientSession) getQueryTimeout() time.Duration {
	if cs == nil {
		return 0
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()

	var raw any
	if v, ok := cs.Vars["query_timeout"]; ok && v != nil {
		raw = v
	} else if v, ok := cs.Vars["statement_timeout"]; ok && v != nil {
		raw = v
	}
	if raw == nil {
		return 0
	}

	switch val := raw.(type) {
	case int:
		if val > 0 {
			return time.Duration(val) * time.Millisecond
		}
	case int64:
		if val > 0 {
			return time.Duration(val) * time.Millisecond
		}
	case float64:
		if val > 0 {
			return time.Duration(val) * time.Millisecond
		}
	case string:
		d, err := parseTimeoutDuration(val)
		if err == nil && d > 0 {
			return d
		}
	case time.Duration:
		if val > 0 {
			return val
		}
	}
	return 0
}

// ExecuteSQL parses and runs one or more SQL statements against the engine and catalog on the default session.
func (s *Server) ExecuteSQL(query string) (*QueryResult, error) {
	return s.ExecuteSessionSQL("default", query)
}

// ExecuteSessionSQLContext runs SQL statements within a specific client session and cancellation context.
func (s *Server) ExecuteSessionSQLContext(ctx context.Context, sessionID string, query string) (*QueryResult, error) {
	// Acquire worker slot from built-in connection pool to prevent thread thrashing
	if s.pool != nil {
		if err := s.pool.AcquireWorker(ctx); err != nil {
			return nil, err
		}
		defer s.pool.ReleaseWorker()
	}

	cs, err := s.GetOrCreateSession(sessionID)
	if err != nil {
		return nil, err
	}

	overallStart := time.Now()
	var queryCtx context.Context
	var cancel context.CancelFunc

	if timeout := cs.getQueryTimeout(); timeout > 0 {
		queryCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		queryCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	cs.mu.Lock()
	cs.QueryCtx = queryCtx
	cs.CancelFunc = cancel
	cs.RunningQuery = query
	cs.QueryStartTime = overallStart
	cs.CancelRequested = false
	cs.mu.Unlock()

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
		if s.slowQueries != nil {
			isSlow = s.slowQueries.Record(sessionID, query, dur, rowsAffected, execErr)
		}
		if s.generalLog != nil {
			s.generalLog.Record(sessionID, query, dur, rowsAffected, execErr)
		}
		if s.metrics != nil {
			s.metrics.RecordQuery(cmd, dur, isErr, isSlow)
		}

		cs.mu.Lock()
		cs.QueryCtx = nil
		cs.CancelFunc = nil
		cs.RunningQuery = ""
		cs.mu.Unlock()
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
			cs.mu.Lock()
			failed := cs.TxFailed
			cs.mu.Unlock()
			if failed && !isRollbackOrResetQuery(stmt) {
				execErr = fmt.Errorf("current transaction is aborted, commands ignored until end of transaction block")
				return nil, execErr
			}
		}

		res, err := s.execSingleStatement(cs, stmt)
		if err != nil {
			if cs != nil {
				cs.mu.Lock()
				cs.Vars["error"] = int64(50000)
				if cs.Tx != nil && strings.HasPrefix(cs.ID, "pg-") {
					cs.TxFailed = true
				}
				cs.mu.Unlock()
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
			cs.mu.Lock()
			cs.Vars["error"] = int64(0)
			cs.mu.Unlock()
		}
		lastRes = res
	}
	return lastRes, nil
}

// ExecuteSessionSQL runs SQL statements within a specific client session context.
func (s *Server) ExecuteSessionSQL(sessionID string, query string) (*QueryResult, error) {
	return s.ExecuteSessionSQLContext(context.Background(), sessionID, query)
}

func (s *Server) execSingleStatement(cs *ClientSession, query string) (*QueryResult, error) {
	start := time.Now()
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return &QueryResult{Success: true, Command: "EMPTY", DurationUS: time.Since(start).Microseconds()}, nil
	}

	upper := strings.ToUpper(trimmed)

	// Rewrite table variables (@table -> physical table)
	tblVarDeclRe := regexp.MustCompile(`(?is)^DECLARE\s+@[a-zA-Z0-9_]+\s+TABLE\b`)
	if cs != nil {
		cs.mu.Lock()
		if len(cs.TableVars) > 0 && !tblVarDeclRe.MatchString(trimmed) {
			for varName, physTable := range cs.TableVars {
				pattern := `(?i)@` + regexp.QuoteMeta(strings.TrimPrefix(varName, "@")) + `\b`
				trimmed = regexp.MustCompile(pattern).ReplaceAllString(trimmed, physTable)
			}
			upper = strings.ToUpper(trimmed)
		}
		cs.mu.Unlock()
	}

	// Process Management: KILL <spid> [WITH STATUSONLY]
	killRe := regexp.MustCompile(`(?is)^KILL\s+(\d+)(?:\s+WITH\s+STATUSONLY)?$`)
	if m := killRe.FindStringSubmatch(trimmed); len(m) == 2 {
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
		cs.mu.Lock()
		defer cs.mu.Unlock()
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
		cs.mu.Lock()
		defer cs.mu.Unlock()
		if cs.Tx == nil {
			return nil, fmt.Errorf("no active transaction to commit on session %s", cs.ID)
		}
		err := cs.Tx.Commit()
		cs.Tx = nil
		cs.TxFailed = false
		if err != nil {
			return nil, fmt.Errorf("commit error: %w", err)
		}
		if s.metrics != nil {
			s.metrics.RecordTransaction("commit")
		}
		return &QueryResult{
			Success:    true,
			Command:    "COMMIT",
			Messages:   []string{"Transaction committed."},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	if namePart, ok := trimAnyPrefix(upper, trimmed, "SAVE TRANSACTION ", "SAVE TRAN ", "SAVEPOINT "); ok {
		cs.mu.Lock()
		defer cs.mu.Unlock()
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
		cs.mu.Lock()
		defer cs.mu.Unlock()
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
		cs.mu.Lock()
		defer cs.mu.Unlock()
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
		if err != nil {
			return nil, fmt.Errorf("rollback error: %w", err)
		}
		if s.metrics != nil {
			s.metrics.RecordTransaction("rollback")
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
			cs.mu.Lock()
			cs.Vars["transaction_isolation"] = strings.ToLower(level)
			cs.mu.Unlock()
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
			cs.mu.Lock()
			cs.Vars["transaction_read_only"] = strings.EqualFold(mode, "ONLY")
			cs.mu.Unlock()
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
			cs.mu.Lock()
			cs.Vars["timezone"] = tz
			cs.mu.Unlock()
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
			cs.mu.Lock()
			cs.Vars["client_encoding"] = enc
			cs.mu.Unlock()
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
					cs.mu.Lock()
					if state == "ON" {
						if cur, ok := cs.Vars["identity_insert_table"].(string); ok && cur != "" && !strings.EqualFold(cur, targetTable) {
							cs.mu.Unlock()
							return nil, fmt.Errorf("cannot SET IDENTITY_INSERT ON for table %q when IDENTITY_INSERT is already ON for table %q", targetTable, cur)
						}
						cs.Vars["identity_insert_table"] = strings.ToLower(targetTable)
					} else {
						delete(cs.Vars, "identity_insert_table")
					}
					cs.mu.Unlock()
				}

				return &QueryResult{
					Success:    true,
					Command:    "SET",
					Messages:   []string{fmt.Sprintf("SET IDENTITY_INSERT for %q is %s.", targetTable, state)},
					DurationUS: time.Since(start).Microseconds(),
				}, nil
			}
		}

		// E. SET <option> ON / OFF / <val> (e.g. SET NOCOUNT ON, SET ANSI_NULLS ON, SET LOCK_TIMEOUT 10000)
		setParts := strings.Fields(rest)
		if len(setParts) >= 2 && !strings.Contains(setParts[0], "=") && setParts[1] != "=" && !strings.EqualFold(setParts[1], "TO") {
			optName := strings.ToLower(cleanIdentifier(setParts[0]))
			optVal := strings.Trim(strings.Join(setParts[1:], " "), "';\"")
			cs.mu.Lock()
			if strings.EqualFold(optVal, "ON") {
				cs.Vars[optName] = true
			} else if strings.EqualFold(optVal, "OFF") {
				cs.Vars[optName] = false
			} else {
				cs.Vars[optName] = optVal
			}
			cs.mu.Unlock()
			return &QueryResult{
				Success:    true,
				Command:    "SET",
				Messages:   []string{fmt.Sprintf("SET %s %s.", optName, optVal)},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}

		// F. Standard SET [SESSION] param [= | TO] value
		setConfigRe := regexp.MustCompile(`(?is)^(?:SESSION\s+)?([a-zA-Z0-9_]+)\s*(?:TO|=)\s*(.*)$`)
		if m := setConfigRe.FindStringSubmatch(rest); len(m) == 3 {
			param := strings.ToLower(cleanIdentifier(m[1]))
			valStr := strings.Trim(strings.TrimSpace(m[2]), "';\"")
			if param == "work_mem" {
				bytes, err := execution.ParseMemSize(valStr)
				if err != nil {
					return nil, fmt.Errorf("invalid value for work_mem %q: %w", valStr, err)
				}
				cs.mu.Lock()
				cs.Vars["work_mem"] = bytes
				cs.mu.Unlock()
				return &QueryResult{
					Success:    true,
					Command:    "SET",
					Messages:   []string{fmt.Sprintf("SET: work_mem set to %s (%d bytes).", execution.FormatMemSize(bytes), bytes)},
					DurationUS: time.Since(start).Microseconds(),
				}, nil
			}
			cs.mu.Lock()
			cs.Vars[param] = valStr
			cs.mu.Unlock()
			return &QueryResult{
				Success:    true,
				Command:    "SET",
				Messages:   []string{fmt.Sprintf("SET: %s set to %s.", param, valStr)},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}
	}

	// 0.05 SHOW PARTITIONS [FROM] <table>
	showPartsRe := regexp.MustCompile(`(?is)^SHOW\s+PARTITIONS(?:\s+FROM)?\s+([^\s;]+)\s*;?$`)
	if m := showPartsRe.FindStringSubmatch(trimmed); len(m) == 2 {
		return s.execShowPartitions(cleanIdentifier(m[1]), start)
	}

	// 0.06 SHOW <parameter>
	showRe := regexp.MustCompile(`(?is)^SHOW\s+(.*?)\s*;?$`)
	if m := showRe.FindStringSubmatch(trimmed); len(m) == 2 {
		param := strings.ToLower(strings.TrimSpace(m[1]))
		paramNorm := strings.ReplaceAll(param, " ", "_")
		cs.mu.Lock()
		val, exists := cs.Vars[paramNorm]
		if !exists {
			val, exists = cs.Vars[param]
		}
		cs.mu.Unlock()

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
	selectSysVarRe := regexp.MustCompile(`(?is)^SELECT\s+@@([a-zA-Z0-9_]+)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?$`)
	if m := selectSysVarRe.FindStringSubmatch(trimmed); len(m) >= 2 {
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

	// 0.08 T-SQL DECLARE variables: DECLARE @var [type] [= expr], ... or DECLARE @table TABLE (...)
	if strings.HasPrefix(upper, "DECLARE ") {
		tblVarRe := regexp.MustCompile(`(?is)^DECLARE\s+(@[a-zA-Z0-9_]+)\s+TABLE\s*\((.*)\)\s*;?$`)
		if m := tblVarRe.FindStringSubmatch(trimmed); len(m) >= 3 {
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
				cs.mu.Lock()
				if cs.TableVars == nil {
					cs.TableVars = make(map[string]string)
				}
				cs.TableVars[varName] = physTable
				cs.mu.Unlock()
			}

			return &QueryResult{
				Success:    true,
				Command:    "DECLARE",
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}

		rawDecls := strings.TrimSpace(trimmed[len("DECLARE "):])
		parts := strings.Split(rawDecls, ",")
		cs.mu.Lock()
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
		cs.mu.Unlock()
		return &QueryResult{
			Success:    true,
			Command:    "DECLARE",
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	// 0.1 Session Variables: SET @var = expr / SET @var := expr
	setRe := regexp.MustCompile(`(?is)^SET\s+@([a-zA-Z0-9_]+)\s*(?::=|=)\s*(.*)$`)
	if m := setRe.FindStringSubmatch(trimmed); len(m) == 3 {
		cs.mu.Lock()
		defer cs.mu.Unlock()
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
	selectVarRe := regexp.MustCompile(`(?is)^SELECT\s+(@[a-zA-Z0-9_]+)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?$`)
	if m := selectVarRe.FindStringSubmatch(trimmed); len(m) >= 2 {
		varName := m[1]
		colHeader := varName
		if len(m) > 2 && m[2] != "" {
			colHeader = m[2]
		}
		cs.mu.Lock()
		val := cs.Vars[varName]
		cs.mu.Unlock()
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
	cs.mu.Lock()
	if len(cs.Vars) > 0 {
		keys := make([]string, 0, len(cs.Vars))
		for k := range cs.Vars {
			keys = append(keys, k)
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
	cs.mu.Unlock()

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

	// 4. DDL: DROP TABLE
	if strings.HasPrefix(upper, "DROP TABLE") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDropTable(trimmed, start)
	}

	// 4.1 DDL: DROP INDEX
	if strings.HasPrefix(upper, "DROP INDEX") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDropIndex(trimmed, start)
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
	parts := strings.Split(s, ".")
	for i, p := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(p), "[]\"'`")
	}
	cleaned := strings.Join(parts, ".")
	lower := strings.ToLower(cleaned)
	if strings.HasPrefix(lower, "dbo.") {
		cleaned = cleaned[4:]
	} else if strings.HasPrefix(lower, "public.") {
		cleaned = cleaned[7:]
	}
	if strings.HasPrefix(cleaned, "##") {
		cleaned = strings.ToLower(cleaned)
	}
	return cleaned
}

// cleanSavepointName strips delimiters, semicolon, quotes, and brackets from savepoint names.
func cleanSavepointName(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, ";")
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "[]\"'`")
	return strings.TrimSpace(raw)
}

// trimAnyPrefix returns original sliced past the first matching prefix matched case-insensitively against upper.
func trimAnyPrefix(upper, original string, prefixes ...string) (string, bool) {
	for _, p := range prefixes {
		if strings.HasPrefix(upper, p) {
			return original[len(p):], true
		}
	}
	return "", false
}

// isRollbackOrResetQuery reports whether query is a transaction rollback or session reset command.
func isRollbackOrResetQuery(q string) bool {
	upper := strings.ToUpper(strings.TrimSpace(q))
	return strings.HasPrefix(upper, "ROLLBACK") ||
		strings.HasPrefix(upper, "DISCARD") ||
		strings.HasPrefix(upper, "RESET") ||
		strings.HasPrefix(upper, "DEALLOCATE") ||
		strings.HasPrefix(upper, "UNLISTEN")
}

// checkWriteAllowed ensures that write operations (DDL/DML) cannot be executed on read-only secondary replicas.
func (s *Server) checkWriteAllowed() error {
	if s.IsReadOnly() {
		return fmt.Errorf("cannot execute write operation on read-only replica: %w", replication.ErrReadOnlyReplica)
	}
	return nil
}
