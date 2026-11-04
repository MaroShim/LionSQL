package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"lionsql/pkg/catalog/virtual"
	"lionsql/pkg/execution"
	"lionsql/pkg/proc"
	"lionsql/pkg/replication"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// QueryResult represents the outcome of a SQL statement execution (aliased from engine.QueryResult).

// parseTableVarDecl parses DECLARE @var TABLE [(colDefs...)] using lexical token scanning.
// Returns (varName, colDefs, isTableDecl, hasColDefs).
func parseTableVarDecl(query string) (varName string, colDefs string, isTableDecl bool, hasColDefs bool) {
	s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), ";"))
	if !sqlutil.HasPrefixKeyword(s, "DECLARE") {
		return "", "", false, false
	}
	rest := strings.TrimSpace(s[len("DECLARE"):])
	if !strings.HasPrefix(rest, "@") {
		return "", "", false, false
	}
	idx := 1
	for idx < len(rest) && isIdentChar(rest[idx]) {
		idx++
	}
	if idx == 1 {
		return "", "", false, false
	}
	varName = rest[:idx]
	afterVar := strings.TrimSpace(rest[idx:])
	if !sqlutil.HasPrefixKeyword(afterVar, "TABLE") {
		return "", "", false, false
	}
	isTableDecl = true
	afterTable := strings.TrimSpace(afterVar[len("TABLE"):])
	if strings.HasPrefix(afterTable, "(") && strings.HasSuffix(afterTable, ")") {
		colDefs = afterTable[1 : len(afterTable)-1]
		hasColDefs = true
	}
	return varName, colDefs, true, hasColDefs
}

// parseKillCommand parses KILL <spid> [WITH STATUSONLY].
func parseKillCommand(query string) (int, bool) {
	s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), ";"))
	fields := strings.Fields(s)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "KILL") {
		return 0, false
	}
	if len(fields) != 2 && !(len(fields) == 4 && strings.EqualFold(fields[2], "WITH") && strings.EqualFold(fields[3], "STATUSONLY")) {
		return 0, false
	}
	spid, err := strconv.Atoi(fields[1])
	if err != nil || spid < 0 {
		return 0, false
	}
	return spid, true
}

// parseUseCommand parses USE <database>.
func parseUseCommand(query string) (string, bool) {
	s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), ";"))
	if !sqlutil.HasPrefixKeyword(s, "USE") {
		return "", false
	}
	arg := strings.TrimSpace(s[3:])
	if arg == "" {
		return "", false
	}
	if (strings.HasPrefix(arg, "[") && strings.HasSuffix(arg, "]")) ||
		(strings.HasPrefix(arg, "\"") && strings.HasSuffix(arg, "\"")) ||
		(strings.HasPrefix(arg, "'") && strings.HasSuffix(arg, "'")) {
		inner := arg[1 : len(arg)-1]
		if strings.ContainsAny(inner, "[]\"'") {
			return "", false
		}
		if inner == "" {
			inner = "master"
		}
		return inner, true
	}
	for i := 0; i < len(arg); i++ {
		ch := arg[i]
		if !isIdentChar(ch) && ch != '.' && ch != '-' {
			return "", false
		}
	}
	return arg, true
}

// parseSetConfigRest parses [SESSION] <param> [= | TO] <val>.
func parseSetConfigRest(rest string) (param string, val string, ok bool) {
	s := strings.TrimSpace(rest)
	if sqlutil.HasPrefixKeyword(s, "SESSION") {
		s = strings.TrimSpace(s[len("SESSION"):])
	}
	idx := 0
	for idx < len(s) && isIdentChar(s[idx]) {
		idx++
	}
	if idx == 0 {
		return "", "", false
	}
	param = s[:idx]
	rem := strings.TrimSpace(s[idx:])
	if strings.HasPrefix(rem, "=") {
		return param, strings.TrimSpace(rem[1:]), true
	}
	if sqlutil.HasPrefixKeyword(rem, "TO") {
		return param, strings.TrimSpace(rem[2:]), true
	}
	return "", "", false
}

// parseDescTableCommand parses (DESC|DESCRIBE) <table> [;].
func parseDescTableCommand(query string) (string, bool) {
	s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), ";"))
	var arg string
	if sqlutil.HasPrefixKeyword(s, "DESCRIBE") {
		arg = strings.TrimSpace(s[len("DESCRIBE"):])
	} else if sqlutil.HasPrefixKeyword(s, "DESC") {
		arg = strings.TrimSpace(s[len("DESC"):])
	} else {
		return "", false
	}
	if arg == "" || strings.ContainsAny(arg, " \t\n\r;") {
		return "", false
	}
	return arg, true
}

// isShowTablesCommand checks SHOW [FULL] TABLES [(FROM|IN) <db>] [;].
func isShowTablesCommand(query string) bool {
	s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), ";"))
	fields := strings.Fields(s)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "SHOW") {
		return false
	}
	idx := 1
	if strings.EqualFold(fields[idx], "FULL") {
		idx++
	}
	if idx >= len(fields) || !strings.EqualFold(fields[idx], "TABLES") {
		return false
	}
	idx++
	if idx == len(fields) {
		return true
	}
	if idx+2 == len(fields) && (strings.EqualFold(fields[idx], "FROM") || strings.EqualFold(fields[idx], "IN")) {
		return true
	}
	return false
}

// isShowDatabasesCommand checks SHOW (DATABASES|SCHEMAS) [;].
func isShowDatabasesCommand(query string) bool {
	s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), ";"))
	fields := strings.Fields(s)
	return len(fields) == 2 && strings.EqualFold(fields[0], "SHOW") &&
		(strings.EqualFold(fields[1], "DATABASES") || strings.EqualFold(fields[1], "SCHEMAS"))
}

// parseShowPartitionsCommand parses SHOW PARTITIONS [FROM] <table> [;].
func parseShowPartitionsCommand(query string) (string, bool) {
	s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), ";"))
	fields := strings.Fields(s)
	if len(fields) < 3 || !strings.EqualFold(fields[0], "SHOW") || !strings.EqualFold(fields[1], "PARTITIONS") {
		return "", false
	}
	if len(fields) == 3 {
		return fields[2], true
	}
	if len(fields) == 4 && strings.EqualFold(fields[2], "FROM") {
		return fields[3], true
	}
	return "", false
}

// parseShowParamCommand parses SHOW <param> [;].
func parseShowParamCommand(query string) (string, bool) {
	s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), ";"))
	if !sqlutil.HasPrefixKeyword(s, "SHOW") {
		return "", false
	}
	param := strings.TrimSpace(s[len("SHOW"):])
	if param == "" {
		return "", false
	}
	return param, true
}

// parseSelectSingleVar parses SELECT (@var | @@sysvar) [[AS] alias].
func parseSelectSingleVar(query string, isSysVar bool) (varName string, alias string, ok bool) {
	s := strings.TrimSpace(query)
	if !sqlutil.HasPrefixKeyword(s, "SELECT") {
		return "", "", false
	}
	rest := strings.TrimSpace(s[len("SELECT"):])
	prefix := "@"
	if isSysVar {
		prefix = "@@"
	}
	if !strings.HasPrefix(rest, prefix) {
		return "", "", false
	}
	if !isSysVar && strings.HasPrefix(rest, "@@") {
		return "", "", false
	}
	startIdx := len(prefix)
	idx := startIdx
	for idx < len(rest) && isIdentChar(rest[idx]) {
		idx++
	}
	if idx == startIdx {
		return "", "", false
	}
	if isSysVar {
		varName = rest[startIdx:idx]
	} else {
		varName = rest[:idx]
	}
	rem := strings.TrimSpace(rest[idx:])
	if rem == "" {
		return varName, "", true
	}
	fields := strings.Fields(rem)
	if len(fields) == 1 {
		for i := 0; i < len(fields[0]); i++ {
			if !isIdentChar(fields[0][i]) {
				return "", "", false
			}
		}
		if strings.EqualFold(fields[0], "FROM") || strings.EqualFold(fields[0], "WHERE") {
			return "", "", false
		}
		return varName, fields[0], true
	}
	if len(fields) == 2 && strings.EqualFold(fields[0], "AS") {
		for i := 0; i < len(fields[1]); i++ {
			if !isIdentChar(fields[1][i]) {
				return "", "", false
			}
		}
		return varName, fields[1], true
	}
	return "", "", false
}

// parseSetLocalVarCommand parses SET @var (:= | =) <expr>.
func parseSetLocalVarCommand(query string) (varName string, expr string, ok bool) {
	s := strings.TrimSpace(query)
	if !sqlutil.HasPrefixKeyword(s, "SET") {
		return "", "", false
	}
	rest := strings.TrimSpace(s[len("SET"):])
	if !strings.HasPrefix(rest, "@") || strings.HasPrefix(rest, "@@") {
		return "", "", false
	}
	idx := 1
	for idx < len(rest) && isIdentChar(rest[idx]) {
		idx++
	}
	if idx == 1 {
		return "", "", false
	}
	varName = rest[1:idx]
	rem := strings.TrimSpace(rest[idx:])
	if strings.HasPrefix(rem, ":=") {
		return varName, strings.TrimSpace(rem[2:]), true
	}
	if strings.HasPrefix(rem, "=") {
		return varName, strings.TrimSpace(rem[1:]), true
	}
	return "", "", false
}

var _ = unicode.IsSpace

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
	cs.RunningQuery = query
	cs.QueryStartTime = overallStart
	cs.Mu.Unlock()
	cs.SetCancel(cancel)

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

		cs.ClearCancel()
		cs.Mu.Lock()
		cs.QueryCtx = nil
		cs.RunningQuery = ""
		cs.Mu.Unlock()
	}()

	if isJdbcMetadataBatch(query) {
		res, err := s.execJdbcMetadataBatch(query, overallStart, cs)
		if err == nil {
			lastRes = res
			return lastRes, nil
		}
	}

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

		// In PostgreSQL mode, if transaction has failed, reject all commands except ROLLBACK
		if cs.IsPostgres() {
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
				errNum := int64(50000)
				type tdsErrorDescriber interface {
					TdsErrorInfo() (int32, byte, byte)
				}
				if carrier, ok := err.(tdsErrorDescriber); ok {
					n, _, _ := carrier.TdsErrorInfo()
					if n != 0 {
						errNum = int64(n)
					}
				}
				cs.Vars["error"] = errNum
				if cs.Tx != nil && cs.IsPostgres() {
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

// BeginSessionTx starts a transaction on the given session directly without query parsing overhead.
func (s *Coordinator) BeginSessionTx(sessionID string, opts storage.TxOptions) error {
	cs, err := s.GetOrCreateSession(sessionID)
	if err != nil {
		return err
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	if cs.Tx != nil {
		return fmt.Errorf("transaction already active on session %s", cs.ID)
	}
	if ro, ok := cs.Vars["transaction_read_only"].(bool); ok && ro {
		opts.ReadOnly = true
	}
	if s.IsReadOnly() {
		opts.ReadOnly = true
	}
	tx, err := cs.Session.BeginTx(opts)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	cs.Tx = tx
	cs.TxFailed = false
	return nil
}

// CommitSessionTx commits the active transaction on the given session directly without query parsing overhead.
func (s *Coordinator) CommitSessionTx(sessionID string) error {
	cs, err := s.GetOrCreateSession(sessionID)
	if err != nil {
		return err
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	if cs.Tx == nil {
		return fmt.Errorf("no active transaction to commit on session %s", cs.ID)
	}

	// In PostgreSQL mode, if transaction has failed, COMMIT is converted to ROLLBACK
	if cs.TxFailed && cs.IsPostgres() {
		_ = cs.Tx.Rollback()
		cs.Tx = nil
		cs.TxFailed = false
		if cs.Cursors != nil {
			cs.Cursors = make(map[string]*SessionCursor)
		}
		if s.Metrics() != nil {
			s.Metrics().RecordTransaction("rollback")
		}
		return nil
	}

	err = cs.Tx.Commit()
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
		if s.Metrics() != nil {
			if errors.Is(err, storage.ErrSerializationFailure) {
				s.Metrics().RecordTransaction("conflict")
			} else {
				s.Metrics().RecordTransaction("rollback")
			}
		}
		return fmt.Errorf("commit error: %w", err)
	}
	if s.Metrics() != nil {
		s.Metrics().RecordTransaction("commit")
	}
	return nil
}

// RollbackSessionTx rolls back the active transaction on the given session directly without query parsing overhead.
func (s *Coordinator) RollbackSessionTx(sessionID string) error {
	cs, err := s.GetOrCreateSession(sessionID)
	if err != nil {
		return err
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	if cs.Tx == nil {
		return fmt.Errorf("no active transaction to rollback on session %s", cs.ID)
	}
	err = cs.Tx.Rollback()
	cs.Tx = nil
	cs.TxFailed = false
	// Close all cursors on ROLLBACK (per PostgreSQL specification)
	if cs.Cursors != nil {
		cs.Cursors = make(map[string]*SessionCursor)
	}
	if err != nil {
		return fmt.Errorf("rollback error: %w", err)
	}
	if s.Metrics() != nil {
		s.Metrics().RecordTransaction("rollback")
	}
	return nil
}

func (s *Coordinator) execSingleStatement(cs *ClientSession, query string) (*QueryResult, error) {
	start := time.Now()
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return &QueryResult{Success: true, Command: "EMPTY", DurationUS: time.Since(start).Microseconds()}, nil
	}

	trimmed = StripLeadingComments(trimmed)
	if trimmed == "" {
		return &QueryResult{Success: true, Command: "EMPTY", DurationUS: time.Since(start).Microseconds()}, nil
	}

	upper := strings.ToUpper(trimmed)

	// Rewrite table variables (@table -> physical table)
	if cs != nil {
		cs.Mu.Lock()
		_, _, isTblDecl, _ := parseTableVarDecl(trimmed)
		if len(cs.TableVars) > 0 && !isTblDecl {
			for varName, physTable := range cs.TableVars {
				trimmed = substituteParamOutsideQuotes(trimmed, "@"+strings.TrimPrefix(varName, "@"), "["+physTable+"]")
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
	if spid, ok := parseKillCommand(trimmed); ok {
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

	// 0.005 Database Context Switching: USE <database> / CREATE DATABASE <db> (Tolerant Virtual No-Op)
	if dbName, ok := parseUseCommand(trimmed); ok {
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

	// 0.006 Tolerant Virtual CREATE DATABASE <name> [ENCODING ...] (No-Op for single-catalog instances)
	if sqlutil.HasPrefixKeyword(trimmed, "CREATE DATABASE") {
		return &QueryResult{
			Success:    true,
			Command:    "CREATE DATABASE",
			Messages:   []string{"CREATE DATABASE executed successfully."},
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
		opts := storage.TxOptions{}
		if strings.Contains(upper, "READ ONLY") {
			opts.ReadOnly = true
		} else if strings.Contains(upper, "READ WRITE") {
			if s.IsReadOnly() {
				return nil, fmt.Errorf("cannot start read-write transaction on read-only replica: %w", replication.ErrReadOnlyReplica)
			}
			opts.ReadOnly = false
		}
		if err := s.BeginSessionTx(cs.ID, opts); err != nil {
			return nil, err
		}
		return &QueryResult{
			Success:    true,
			Command:    "BEGIN",
			Messages:   []string{"Transaction started."},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	if strings.HasPrefix(upper, "COMMIT") {
		if err := s.CommitSessionTx(cs.ID); err != nil {
			return nil, err
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
			cs.Mu.Lock()
			defer cs.Mu.Unlock()
			if cs.Tx == nil {
				return nil, fmt.Errorf("no active transaction to rollback on session %s", cs.ID)
			}
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

		if err := s.RollbackSessionTx(cs.ID); err != nil {
			return nil, err
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
		if cs != nil {
			cs.Mu.Lock()
			if s.engine != nil {
				for _, physTable := range cs.TableVars {
					_ = s.engine.DropTable(physTable)
				}
				for _, tempTbl := range cs.TempTables {
					_ = s.engine.DropTable(tempTbl)
				}
			}
			cs.TableVars = nil
			cs.TempTables = nil
			cs.TempTableMap = nil
			cs.EphemeralTables = nil
			if strings.HasPrefix(upper, "DISCARD ALL") || upper == "RESET ALL" || strings.HasPrefix(upper, "DEALLOCATE ALL") {
				cs.PreparedStmts = nil
			}
			cs.Mu.Unlock()
		}
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
		if rawParam, rawVal, ok := parseSetConfigRest(rest); ok {
			param := strings.ToLower(cleanIdentifier(rawParam))
			valStr := strings.Trim(strings.TrimSpace(rawVal), "';\"")
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

	// 0.048 DESC / DESCRIBE <table>
	if tblName, ok := parseDescTableCommand(trimmed); ok {
		return s.execDescTable(cleanIdentifier(tblName), start)
	}

	// 0.049 SHOW TABLES
	if isShowTablesCommand(trimmed) {
		return s.execShowTables(cs, start)
	}

	// 0.0495 SHOW DATABASES / SCHEMAS
	if isShowDatabasesCommand(trimmed) {
		return s.execShowDatabases(start)
	}

	// 0.05 SHOW PARTITIONS [FROM] <table>
	if tblName, ok := parseShowPartitionsCommand(trimmed); ok {
		return s.execShowPartitions(cleanIdentifier(tblName), start)
	}

	// 0.06 SHOW <parameter>
	if rawParam, ok := parseShowParamCommand(trimmed); ok {
		param := strings.ToLower(strings.TrimSpace(rawParam))
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
				valStr = "15.0 (LionSQL)"
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
	if rawVar, alias, ok := parseSelectSingleVar(trimmed, true); ok {
		varName := strings.ToLower(rawVar)
		colHeader := "@@" + varName
		if alias != "" {
			colHeader = alias
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
		if rawVarName, colDefs, isTbl, hasCols := parseTableVarDecl(trimmed); isTbl && hasCols {
			varName := strings.ToLower(rawVarName)
			if !strings.HasPrefix(varName, "@") {
				varName = "@" + varName
			}
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
		parts := splitDeclarations(rawDecls)
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
				if len(varFields) > 1 {
					typeName := strings.ToUpper(varFields[1])
					if idx := strings.Index(typeName, "("); idx != -1 {
						typeName = typeName[:idx]
					}
					if typeName == "BIT" || typeName == "BOOL" || typeName == "BOOLEAN" {
						if b, ok := val.(bool); ok {
							val = b
						} else if i, ok := val.(int64); ok {
							val = (i != 0)
						} else if s, ok := val.(string); ok {
							s = strings.Trim(strings.TrimSpace(s), "'\"")
							val = (strings.EqualFold(s, "true") || s == "1")
						}
					}
				}
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
	if varName, rawExpr, ok := parseSetLocalVarCommand(trimmed); ok {
		cs.Mu.Lock()
		defer cs.Mu.Unlock()
		expr := strings.TrimSpace(rawExpr)
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
	if varName, alias, ok := parseSelectSingleVar(trimmed, false); ok {
		colHeader := varName
		if alias != "" {
			colHeader = alias
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

	// 0.27 Conditional DDL statements: IF [NOT] EXISTS (...) <DDL>, IF OBJECT_ID(...) <DDL>
	if strings.HasPrefix(upper, "IF ") || strings.HasPrefix(upper, "IF\t") || strings.HasPrefix(upper, "IF\n") {
		if res, handled, err := s.tryExecConditionalDDL(trimmed, start, cs); handled {
			return res, err
		}
	}

	// 0.28 Control-flow & Anonymous Blocks: IF ... ELSE, WHILE ..., BEGIN ... END
	if strings.HasPrefix(upper, "IF ") || strings.HasPrefix(upper, "WHILE ") ||
		(strings.HasPrefix(upper, "BEGIN ") && !isBeginTransaction(trimmed[5:])) {
		return s.execAnonymousBlock(trimmed, start, cs)
	}

	// 0.3 Substitute session variables in SQL query if present
	cs.Mu.Lock()
	if len(cs.Vars) > 0 {
		keys := make([]string, 0, len(cs.Vars))
		for k := range cs.Vars {
			if strings.HasPrefix(k, "@") && !strings.HasPrefix(k, "@@") {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			return len(keys[i]) > len(keys[j])
		})

		for _, k := range keys {
			v := cs.Vars[k]
			var varRep string
			switch val := v.(type) {
			case nil:
				varRep = "NULL"
			case int, int64, int32, int16, int8, uint, uint64, uint32, uint16, uint8, float64, float32, bool:
				varRep = fmt.Sprintf("%v", val)
			case string:
				escaped := strings.ReplaceAll(val, "'", "''")
				varRep = fmt.Sprintf("'%s'", escaped)
			default:
				sVal := fmt.Sprintf("%v", val)
				escaped := strings.ReplaceAll(sVal, "'", "''")
				varRep = fmt.Sprintf("'%s'", escaped)
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

	// 1. Procedural DDL: CREATE PROCEDURE / ALTER PROCEDURE / DROP PROCEDURE / CREATE FUNCTION / CREATE TRIGGER
	if strings.HasPrefix(upper, "CREATE PROCEDURE") || strings.HasPrefix(upper, "CREATE PROC") ||
		strings.HasPrefix(upper, "CREATE OR ALTER PROCEDURE") || strings.HasPrefix(upper, "CREATE OR ALTER PROC") ||
		strings.HasPrefix(upper, "ALTER PROCEDURE") || strings.HasPrefix(upper, "ALTER PROC") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execCreateProcedure(trimmed, start)
	}
	if strings.HasPrefix(upper, "DROP PROCEDURE") || strings.HasPrefix(upper, "DROP PROC") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDropProcedure(trimmed, start)
	}
	if strings.HasPrefix(upper, "CREATE FUNCTION") || strings.HasPrefix(upper, "CREATE OR ALTER FUNCTION") || strings.HasPrefix(upper, "ALTER FUNCTION") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execCreateFunction(trimmed, start)
	}
	if strings.HasPrefix(upper, "DROP FUNCTION") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDropFunction(trimmed, start)
	}
	if strings.HasPrefix(upper, "CREATE TRIGGER") || strings.HasPrefix(upper, "CREATE OR ALTER TRIGGER") || strings.HasPrefix(upper, "ALTER TRIGGER") ||
		strings.HasPrefix(upper, "CREATE TEMP TRIGGER") || strings.HasPrefix(upper, "CREATE TEMPORARY TRIGGER") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execCreateTrigger(trimmed, start)
	}
	if strings.HasPrefix(upper, "DROP TRIGGER") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDropTrigger(trimmed, start)
	}

	// 1.5 DDL: CREATE VIEW / CREATE OR REPLACE VIEW / CREATE TEMP VIEW / DROP VIEW
	if strings.HasPrefix(upper, "CREATE VIEW") || strings.HasPrefix(upper, "CREATE OR REPLACE VIEW") || strings.HasPrefix(upper, "CREATE TEMP VIEW") || strings.HasPrefix(upper, "CREATE TEMPORARY VIEW") {
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

	// 2.1 User Exception: RAISERROR
	if strings.HasPrefix(upper, "RAISERROR ") || strings.HasPrefix(upper, "RAISERROR(") || upper == "RAISERROR" || upper == "RAISERROR;" {
		return s.execRaiserror(trimmed, start)
	}

	// 2.2 User Exception: THROW
	if strings.HasPrefix(upper, "THROW ") || strings.HasPrefix(upper, "THROW;") || upper == "THROW" || upper == "THROW;" {
		return s.execThrow(trimmed, start)
	}

	// 3. DDL: CREATE TABLE
	if sqlutil.HasPrefixKeyword(trimmed, "CREATE TABLE") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execCreateTable(trimmed, start, cs)
	}

	// 3.1 DDL: CREATE INDEX
	if sqlutil.HasPrefixKeyword(trimmed, "CREATE INDEX") || sqlutil.HasPrefixKeyword(trimmed, "CREATE UNIQUE INDEX") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execCreateIndex(trimmed, start)
	}

	// 3.2 DDL: CREATE STATISTICS (C-5)
	if sqlutil.HasPrefixKeyword(trimmed, "CREATE STATISTICS") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execCreateStatistics(trimmed, start)
	}

	// 4. DDL: DROP TABLE
	if sqlutil.HasPrefixKeyword(trimmed, "DROP TABLE") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDropTable(trimmed, start, cs)
	}

	// 4.1 DDL: DROP INDEX
	if sqlutil.HasPrefixKeyword(trimmed, "DROP INDEX") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDropIndex(trimmed, start)
	}

	// 4.1.2 DDL/Maintenance: REINDEX
	if sqlutil.HasPrefixKeyword(trimmed, "REINDEX") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execReindex(trimmed, start)
	}

	// 4.1.5 DDL: DROP STATISTICS (C-5)
	if sqlutil.HasPrefixKeyword(trimmed, "DROP STATISTICS") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDropStatistics(trimmed, start)
	}

	// 4.2 DDL/DML: TRUNCATE TABLE
	if sqlutil.HasPrefixKeyword(trimmed, "TRUNCATE TABLE") || sqlutil.HasPrefixKeyword(trimmed, "TRUNCATE") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execTruncateTable(trimmed, start, cs)
	}

	// 4.3 DDL: ALTER TABLE
	if sqlutil.HasPrefixKeyword(trimmed, "ALTER TABLE") {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execAlterTable(trimmed, start, cs)
	}

	// 4.35 DDL: ALTER DATABASE (tolerant no-op for Collation/Options)
	if sqlutil.HasPrefixKeyword(trimmed, "ALTER DATABASE") {
		return &QueryResult{
			Success:    true,
			Command:    "ALTER DATABASE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"Command(s) completed successfully."},
		}, nil
	}

	// 4.355 DDL: ALTER DEFAULT PRIVILEGES (tolerant no-op)
	if sqlutil.HasPrefixKeyword(trimmed, "ALTER DEFAULT PRIVILEGES") {
		return &QueryResult{
			Success:    true,
			Command:    "ALTER DEFAULT PRIVILEGES",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"Default privileges altered."},
		}, nil
	}

	// 4.36 Tolerant PG Compatibility: COMMENT ON
	if sqlutil.HasPrefixKeyword(trimmed, "COMMENT ON") {
		return &QueryResult{
			Success:    true,
			Command:    "COMMENT",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"Comment created successfully."},
		}, nil
	}

	// 4.37 Tolerant PG Compatibility: CREATE / DROP SCHEMA / EXTENSION
	if sqlutil.HasPrefixKeyword(trimmed, "CREATE SCHEMA") || sqlutil.HasPrefixKeyword(trimmed, "DROP SCHEMA") {
		return &QueryResult{
			Success:    true,
			Command:    "SCHEMA",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"Schema command completed successfully."},
		}, nil
	}

	if sqlutil.HasPrefixKeyword(trimmed, "CREATE EXTENSION") || sqlutil.HasPrefixKeyword(trimmed, "DROP EXTENSION") {
		return &QueryResult{
			Success:    true,
			Command:    "CREATE EXTENSION",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"Extension command completed successfully."},
		}, nil
	}

	// 4.38 Tolerant PG Compatibility: CREATE / DROP / ALTER ROLE
	if sqlutil.HasPrefixKeyword(trimmed, "CREATE ROLE") || sqlutil.HasPrefixKeyword(trimmed, "DROP ROLE") || sqlutil.HasPrefixKeyword(trimmed, "ALTER ROLE") {
		return &QueryResult{
			Success:    true,
			Command:    "ROLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"Role command completed successfully."},
		}, nil
	}

	// 4.40 Tolerant PG Compatibility: DO anonymous blocks
	if sqlutil.HasPrefixKeyword(trimmed, "DO") {
		return &QueryResult{
			Success:    true,
			Command:    "DO",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"DO block executed successfully."},
		}, nil
	}

	// 4.41 Tolerant PG Compatibility: SEQUENCE
	if sqlutil.HasPrefixKeyword(trimmed, "CREATE SEQUENCE") || sqlutil.HasPrefixKeyword(trimmed, "ALTER SEQUENCE") || sqlutil.HasPrefixKeyword(trimmed, "DROP SEQUENCE") {
		return &QueryResult{
			Success:    true,
			Command:    "SEQUENCE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"Sequence command completed successfully."},
		}, nil
	}

	// 4.42 Tolerant PG Compatibility: LOCK TABLE
	if sqlutil.HasPrefixKeyword(trimmed, "LOCK TABLE") || sqlutil.HasPrefixKeyword(trimmed, "LOCK") {
		return &QueryResult{
			Success:    true,
			Command:    "LOCK TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"LOCK TABLE executed successfully."},
		}, nil
	}

	// 5. DML: INSERT INTO / UPSERT
	if kind, _, ok := dmlHead(trimmed); ok && kind == dmlInsert {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execInsert(trimmed, start, cs)
	}

	// 6. DQL: WITH (Common Table Expressions) / SELECT
	if strings.HasPrefix(upper, "WITH") && len(trimmed) > 4 && (trimmed[4] == ' ' || trimmed[4] == '\t' || trimmed[4] == '\n' || trimmed[4] == '\r') {
		if virtual.IsVirtualCatalogJoinQuery(trimmed) {
			return s.execVirtualCatalogJoinSelect(trimmed, start)
		}
		return s.execCTE(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "SELECT") {
		return s.execSelect(trimmed, start, cs)
	}

	// 7. DML: UPDATE
	if kind, _, ok := dmlHead(trimmed); ok && kind == dmlUpdate {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execUpdate(trimmed, start, cs)
	}

	// 8. DML: DELETE
	if kind, _, ok := dmlHead(trimmed); ok && kind == dmlDelete {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
		return s.execDelete(trimmed, start, cs)
	}

	// 9. DML: MERGE INTO
	if sqlutil.HasPrefixKeyword(trimmed, "MERGE") {
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

// SQLError represents a structured SQL Server error containing error number, severity, and state.
type SQLError struct {
	Number   int32
	Severity byte
	State    byte
	Message  string
}

func (e *SQLError) Error() string {
	return e.Message
}

func (e *SQLError) TdsErrorInfo() (int32, byte, byte) {
	return e.Number, e.Severity, e.State
}

func (s *Coordinator) execRaiserror(raw string, start time.Time) (*QueryResult, error) {
	rem := strings.TrimSpace(raw)
	upper := strings.ToUpper(rem)
	if strings.HasPrefix(upper, "RAISERROR") {
		rem = strings.TrimSpace(rem[9:])
	}
	rem = strings.TrimRight(rem, ";")
	rem = strings.TrimSpace(rem)

	// If wrapped in parentheses: RAISERROR(...)
	if strings.HasPrefix(rem, "(") {
		closeIdx := strings.LastIndex(rem, ")")
		if closeIdx != -1 {
			rem = strings.TrimSpace(rem[1:closeIdx])
		}
	}

	// Check if trailing WITH clause exists, e.g. WITH NOWAIT, WITH LOG, WITH SETERROR
	if idx := strings.Index(strings.ToUpper(rem), " WITH "); idx != -1 {
		rem = strings.TrimSpace(rem[:idx])
	}

	rawArgs := SplitDeclarations(rem)
	var args []string
	for _, a := range rawArgs {
		trimmed := strings.TrimSpace(a)
		if trimmed != "" {
			args = append(args, trimmed)
		}
	}

	if len(args) == 0 {
		return nil, &SQLError{Number: 50000, Severity: 16, State: 1, Message: "RAISERROR requires at least a message"}
	}

	// 1. Message or error number
	msg := unwrapSQLString(args[0])
	var errNum int32 = 50000
	if n, err := strconv.ParseInt(args[0], 10, 32); err == nil && n >= 13000 {
		errNum = int32(n)
		msg = fmt.Sprintf("Error %d", errNum)
	}

	// 2. Severity (default 16)
	sev := 16
	if len(args) > 1 {
		if val, err := strconv.Atoi(strings.TrimSpace(args[1])); err == nil {
			sev = val
		}
	}

	// 3. State (default 1)
	state := 1
	if len(args) > 2 {
		if val, err := strconv.Atoi(strings.TrimSpace(args[2])); err == nil {
			state = val
		}
	}

	// 4. Format arguments for %s, %d, %i
	if len(args) > 3 {
		var formatArgs []any
		for _, a := range args[3:] {
			formatArgs = append(formatArgs, unwrapSQLString(a))
		}
		if strings.Contains(msg, "%") {
			goFmt := strings.ReplaceAll(msg, "%s", "%v")
			goFmt = strings.ReplaceAll(goFmt, "%d", "%v")
			goFmt = strings.ReplaceAll(goFmt, "%i", "%v")
			msg = fmt.Sprintf(goFmt, formatArgs...)
		}
	}

	// Severity <= 10 produces an informational message (success), not a terminating error
	if sev <= 10 {
		return &QueryResult{
			Success:    true,
			Command:    "PRINT",
			Messages:   []string{msg},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	return nil, &SQLError{
		Number:   errNum,
		Severity: byte(sev),
		State:    byte(state),
		Message:  msg,
	}
}

func (s *Coordinator) execThrow(raw string, start time.Time) (*QueryResult, error) {
	rem := strings.TrimSpace(raw)
	upper := strings.ToUpper(rem)
	if strings.HasPrefix(upper, "THROW") {
		rem = strings.TrimSpace(rem[5:])
	}
	rem = strings.TrimRight(rem, ";")
	rem = strings.TrimSpace(rem)

	if rem == "" {
		// Bare THROW (rethrow)
		return nil, &SQLError{
			Number:   50000,
			Severity: 16,
			State:    1,
			Message:  "An error occurred",
		}
	}

	rawArgs := SplitDeclarations(rem)
	var args []string
	for _, a := range rawArgs {
		trimmed := strings.TrimSpace(a)
		if trimmed != "" {
			args = append(args, trimmed)
		}
	}

	var errNum int32 = 50000
	msg := "An error occurred"
	state := 1

	if len(args) > 0 {
		if val, err := strconv.ParseInt(strings.TrimSpace(args[0]), 10, 32); err == nil {
			errNum = int32(val)
		}
	}
	if len(args) > 1 {
		msg = unwrapSQLString(args[1])
	}
	if len(args) > 2 {
		if val, err := strconv.Atoi(strings.TrimSpace(args[2])); err == nil {
			state = val
		}
	}

	return nil, &SQLError{
		Number:   errNum,
		Severity: 16, // In T-SQL, THROW always has severity 16
		State:    byte(state),
		Message:  msg,
	}
}
