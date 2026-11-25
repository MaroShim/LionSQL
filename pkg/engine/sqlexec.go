package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"lionsql/pkg/catalog/virtual"
	"lionsql/pkg/execution"
	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/replication"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
	"lionsql/pkg/tx"
)

// QueryResult represents the outcome of a SQL statement execution (aliased from engine.QueryResult).

func truncateForLog(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + " ...[truncated]"
}

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

func evalSessionVarExpr(rawExpr string, vars map[string]any) any {
	expr := strings.TrimSpace(rawExpr)
	expr = strings.TrimRight(expr, ";")
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}

	// 1. Fast-path for single literals without operators or variable references
	if !strings.ContainsAny(expr, "@+-*/%()") {
		return parseSessionVarValue(expr)
	}

	// 2. Parse into AST expression and evaluate via proc.EvalRowExpr
	evalVars := make(map[string]any, len(vars)*2)
	for k, v := range vars {
		evalVars[k] = v
		evalVars[strings.ToLower(k)] = v
	}

	p := proc.NewParser(proc.NewLexer(expr))
	if ast := p.ParseExpr(); ast != nil {
		if val, err := proc.EvalRowExpr(ast, evalVars, nil); err == nil {
			if sv, ok := val.(storage.Value); ok {
				return formatStorageValue(sv)
			}
			return val
		}
	}

	// 3. Fallback: replace known session variables and attempt scalar literal parse
	substituted := expr
	if len(vars) > 0 {
		keys := make([]string, 0, len(vars))
		for k := range vars {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			return len(keys[i]) > len(keys[j])
		})
		for _, k := range keys {
			v := vars[k]
			switch val := v.(type) {
			case string:
				substituted = strings.ReplaceAll(substituted, k, "'"+strings.ReplaceAll(val, "'", "''")+"'")
			default:
				substituted = strings.ReplaceAll(substituted, k, fmt.Sprintf("%v", val))
			}
		}
	}
	return parseSessionVarValue(substituted)
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

type reentrantQueryKey struct{}

// executeNestedSessionSQL runs a subquery or internal SQL statement within an already-active
// worker slot and preserves the parent query's cancellation context and active query metadata.
func (s *Coordinator) executeNestedSessionSQL(cs *ClientSession, query string) (*QueryResult, error) {
	ctx := context.Background()
	sessID := "default"
	if cs != nil {
		sessID = cs.ID
		cs.Mu.Lock()
		if cs.QueryCtx != nil {
			ctx = cs.QueryCtx
		}
		cs.Mu.Unlock()
	}
	return s.ExecuteSessionSQLContext(context.WithValue(ctx, reentrantQueryKey{}, true), sessID, query)
}

// ExecuteSQL parses and runs one or more SQL statements against the engine and catalog on the default session.
func (s *Coordinator) ExecuteSQL(query string) (*QueryResult, error) {
	return s.ExecuteSessionSQL("default", query)
}

// ExecuteSessionSQLContext runs SQL statements within a specific client session and cancellation context.
func (s *Coordinator) ExecuteSessionSQLContext(ctx context.Context, sessionID string, query string) (retRes *QueryResult, retErr error) {
	isNested, _ := ctx.Value(reentrantQueryKey{}).(bool)

	// Acquire worker slot from built-in connection pool to prevent thread thrashing
	// (skipped for nested subqueries/dynamic SQL that already run inside a parent worker slot)
	if !isNested && s.Pool() != nil {
		if err := s.Pool().AcquireWorker(ctx); err != nil {
			return nil, err
		}
		defer s.Pool().ReleaseWorker()
	}

	cs, err := s.GetOrCreateSession(sessionID)
	if err != nil {
		return nil, err
	}

	if !isNested {
		if err := s.ensureClientStorageSession(cs); err != nil {
			return nil, err
		}
	}

	// [RULE:ConcurrencySafety] 1-Session 1-Transaction Invariant:
	// Top-level queries on the same session are strictly serialized via TxMgr.
	if !isNested && cs.TxMgr != nil {
		cs.TxMgr.Lock()
		defer cs.TxMgr.Unlock()
	}

	overallStart := time.Now()
	var queryCtx context.Context
	var cancel context.CancelFunc

	if isNested {
		queryCtx = ctx
	} else {
		if timeout := cs.GetQueryTimeout(); timeout > 0 {
			queryCtx, cancel = context.WithTimeout(ctx, timeout)
		} else {
			queryCtx, cancel = context.WithCancel(ctx)
		}
		defer cancel()
		queryCtx = context.WithValue(queryCtx, reentrantQueryKey{}, true)

		cs.Mu.Lock()
		cs.activeQueries++
		cs.lastUsedAt = overallStart
		cs.QueryCtx = queryCtx
		cs.RunningQuery = query
		cs.QueryStartTime = overallStart
		cs.Mu.Unlock()
		cs.SetCancel(cancel)
	}

	stmts := splitStatements(query)
	var lastRes *QueryResult
	var execErr error

	defer func() {
		// [RULE:PanicRecovery] Global panic interceptor protecting engine stability and session state
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			log.Printf("[PANIC-RECOVERY] session %s recovered from query panic: %v\n%s", sessionID, r, stack)
			if cs != nil {
				cs.Mu.Lock()
				if cs.TxMgr != nil {
					_ = cs.TxMgr.Rollback(context.Background())
				} else if cs.Tx() != nil {
					_ = cs.Tx().Rollback()
				}
				cs.ClearTxFailed()
				cs.ResetTranCount()
				cs.Vars["error"] = int64(50000)
				cs.Mu.Unlock()
			}
			execErr = fmt.Errorf("internal engine panic during query execution: %v", r)
			lastRes = &QueryResult{
				Success: false,
				Error:   execErr.Error(),
			}
			retErr = execErr
			retRes = lastRes
		}

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
		if isErr {
			var errStr string
			if execErr != nil {
				errStr = execErr.Error()
			} else if lastRes != nil {
				errStr = lastRes.Error
			}
			log.Printf("[QUERY-ERROR] session=%s duration=%s err=%s query=%q", sessionID, dur, errStr, truncateForLog(query, 500))
		} else if isSlow {
			log.Printf("[SLOW-QUERY] session=%s duration=%s rows=%d query=%q", sessionID, dur, rowsAffected, truncateForLog(query, 500))
		}

		if !isNested {
			cs.Mu.Lock()
			cs.activeQueries--
			cs.lastUsedAt = time.Now()
			lastActive := cs.activeQueries <= 0
			if lastActive {
				cs.activeQueries = 0
				cs.QueryCtx = nil
				cs.RunningQuery = ""
			}
			drainTx, drainSess, drainFn := cs.checkAndDrainClosedLocked()
			cs.Mu.Unlock()
			if lastActive {
				cs.ClearCancel()
			}
			if drainTx != nil {
				_ = drainTx.Rollback()
			}
			if drainFn != nil {
				drainFn(drainSess)
			}
		}
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

	var accumulatedMessages []string
	var headRes *QueryResult
	var tailRes *QueryResult

	initialTranCount := 0
	if cs != nil {
		initialTranCount = cs.TranCount()
	}

	for _, stmt := range stmts {
		select {
		case <-queryCtx.Done():
			if cs != nil {
				if cs.Tx() != nil {
					cs.MarkTxFailed()
				}
			}
			if errors.Is(queryCtx.Err(), context.DeadlineExceeded) {
				execErr = fmt.Errorf("canceling statement due to statement timeout")
			} else {
				execErr = fmt.Errorf("canceling statement due to user request")
			}
			return nil, execErr
		default:
		}

		// If transaction has failed (e.g. in PostgreSQL mode or after statement timeout), reject all commands except ROLLBACK (or COMMIT which rolls back in CommitSessionTx)
		if cs != nil {
			failed := cs.IsTxFailed()
			isPG := cs.IsPostgres()
			if failed {
				if isPG {
					if !isRollbackOrResetQuery(stmt) {
						execErr = fmt.Errorf("current transaction is aborted, commands ignored until end of transaction block")
						return nil, execErr
					}
				} else {
					// T-SQL uncommittable transaction semantics (XACT_STATE = -1):
					// Reads (SELECT) and ROLLBACK/RESET are permitted so CATCH blocks can inspect XACT_STATE() and error logs.
					// Writes (INSERT, UPDATE, DELETE, DDL, COMMIT) are rejected with Msg 3930.
					isReadOrRollback := isRollbackOrResetQuery(stmt) || sqlutil.HasPrefixKeyword(stmt, "SELECT")
					if !isReadOrRollback {
						execErr = fmt.Errorf("Msg 3930, Level 16, State 1: The current transaction cannot be committed and cannot support operations that write to the log file. Roll back the transaction.")
						return nil, execErr
					}
				}
			}
		}

		res, err := s.execSingleStatement(cs, stmt)
		if err != nil {
			isTimeoutOrCancel := errors.Is(err, context.DeadlineExceeded) ||
				errors.Is(err, context.Canceled) ||
				(queryCtx.Err() != nil && (errors.Is(queryCtx.Err(), context.DeadlineExceeded) || errors.Is(queryCtx.Err(), context.Canceled)))
			isConflict := errors.Is(err, storage.ErrSerializationFailure) || strings.Contains(err.Error(), "serialization failure")
			if isConflict && s.Metrics() != nil {
				s.Metrics().RecordTransaction("conflict")
			}
			if cs != nil {
				// [RULE:CleanTeardown] Semicolon batch atomicity:
				// If a multi-statement batch initiated an explicit transaction (initialTranCount == 0 && cs.TranCount() > 0)
				// and failed mid-batch, cleanly rollback the abandoned transaction to prevent dangling transaction leaks and uncommitted mutations.
				if len(stmts) > 1 && initialTranCount == 0 && cs.TranCount() > 0 {
					if cs.TxMgr != nil {
						_ = cs.TxMgr.Rollback(context.Background())
					} else if cs.Tx() != nil {
						_ = cs.Tx().Rollback()
					}
					cs.ResetTranCount()
					cs.ClearTxFailed()
				}
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
				xactAbort, _ := cs.Vars["xact_abort"].(bool)
				if cs.Tx() != nil && (cs.IsPostgres() || isTimeoutOrCancel || isConflict || xactAbort) {
					cs.MarkTxFailed()
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
			if headRes != nil {
				return headRes, err
			}
			return res, err
		}
		if cs != nil {
			cs.Mu.Lock()
			cs.Vars["error"] = int64(0)
			cs.Mu.Unlock()
		}
		if res != nil {
			if len(res.Messages) > 0 {
				accumulatedMessages = append(accumulatedMessages, res.Messages...)
			}
			shouldChain := len(res.Columns) > 0 || res.Next != nil
			if cs != nil && cs.IsPostgres() && res.Command != "PRINT" && res.Command != "EMPTY" {
				shouldChain = true
			}
			if shouldChain {
				if headRes == nil {
					headRes = res
					tailRes = res
				} else {
					tailRes.Next = res
					tailRes = res
				}
				for tailRes.Next != nil {
					tailRes = tailRes.Next
				}
			}
			if lastRes == nil || len(res.Columns) > 0 || res.Command != "PRINT" {
				lastRes = res
			}
		}
	}
	ret := headRes
	if ret == nil {
		ret = lastRes
	}
	if ret != nil && len(accumulatedMessages) > 0 {
		ret.Messages = accumulatedMessages
	}
	return ret, nil
}

// ExecuteSessionSQL runs SQL statements within a specific client session context.
func (s *Coordinator) ExecuteSessionSQL(sessionID string, query string) (*QueryResult, error) {
	return s.ExecuteSessionSQLContext(context.Background(), sessionID, query)
}

func (s *Coordinator) ensureClientStorageSession(cs *ClientSession) error {
	cs.Mu.Lock()
	cs.lastUsedAt = time.Now()
	needsSess := !cs.closed && cs.Session == nil
	cs.Mu.Unlock()
	if !needsSess {
		return nil
	}

	var acquired storage.Session
	var acqErr error
	if s.pool != nil {
		acquired, acqErr = s.pool.AcquireSession()
	} else if s.engine != nil {
		acquired, acqErr = s.engine.NewSession()
	}
	if acqErr != nil {
		return fmt.Errorf("failed to acquire session for transaction: %w", acqErr)
	}
	if acquired == nil {
		return fmt.Errorf("no storage engine session available")
	}

	cs.Mu.Lock()
	cs.lastUsedAt = time.Now()
	if !cs.closed && cs.Session == nil {
		cs.Session = acquired
		if cs.TxMgr != nil {
			cs.TxMgr.SetSession(acquired)
		}
		acquired = nil
	}
	cs.Mu.Unlock()

	if acquired != nil {
		if s.pool != nil {
			s.pool.ReleaseSession(acquired)
		} else {
			_ = acquired.Close()
		}
	}
	return nil
}

// BeginSessionTx starts a transaction on the given session directly without query parsing overhead.
func (s *Coordinator) BeginSessionTx(sessionID string, opts storage.TxOptions) error {
	cs, err := s.GetOrCreateSession(sessionID)
	if err != nil {
		return err
	}
	if err := s.ensureClientStorageSession(cs); err != nil {
		return err
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	cs.lastUsedAt = time.Now()
	if cs.closed {
		return fmt.Errorf("session %s is closed", cs.ID)
	}
	if cs.InTransaction() || cs.Tx() != nil {
		if cs.IsPostgres() {
			return fmt.Errorf("transaction already active on session %s", cs.ID)
		}
		// T-SQL dialect: nested BEGIN TRANSACTION increments @@TRANCOUNT without error
		cs.IncTranCount()
		return nil
	}
	if cs.Session == nil {
		return fmt.Errorf("no storage engine session available")
	}
	if cs.isReadOnlyLocked() || s.IsReadOnly() || opts.ReadOnly {
		opts.ReadOnly = true
		cs.Vars["transaction_read_only"] = true
	}
	if cs.TxMgr != nil {
		_, startErr := cs.TxMgr.Begin(context.Background(), tx.Options{ReadOnly: opts.ReadOnly})
		if startErr != nil {
			return fmt.Errorf("failed to start transaction: %w", startErr)
		}
	} else {
		_, startErr := cs.Session.BeginTx(opts)
		if startErr != nil {
			return fmt.Errorf("failed to start transaction: %w", startErr)
		}
	}
	cs.ClearTxFailed()
	return nil
}

// CommitSessionTx commits the active transaction on the given session directly without query parsing overhead.
func (s *Coordinator) CommitSessionTx(sessionID string) error {
	cs, err := s.GetOrCreateSession(sessionID)
	if err != nil {
		return err
	}
	cs.Mu.Lock()
	cs.lastUsedAt = time.Now()
	if cs.Tx() == nil {
		cs.Mu.Unlock()
		return fmt.Errorf("no active transaction to commit on session %s", cs.ID)
	}

	// If transaction has failed, ROLLBACK the underlying transaction across all dialects
	if cs.IsTxFailed() {
		if cs.TxMgr != nil {
			_ = cs.TxMgr.Rollback(context.Background())
		} else if tx := cs.Tx(); tx != nil {
			_ = tx.Rollback()
		}
		cs.ResetTranCount()
		cs.ClearTxFailed()
		if cs.Cursors != nil {
			for _, cur := range cs.Cursors {
				cur.IsOpen = false
				cur.IsClosed = true
				cur.Rows = nil
				cur.Pos = 0
			}
		}
		cs.Mu.Unlock()
		if s.Metrics() != nil {
			s.Metrics().RecordTransaction("rollback")
		}
		if cs.IsPostgres() {
			return nil
		}
		return fmt.Errorf("current transaction is aborted, commands ignored until end of transaction block")
	}

	if cs.TranCount() > 1 {
		// T-SQL nested transaction: decrement tranCount only, do not physically commit
		cs.DecTranCount()
		cs.Mu.Unlock()
		if s.Metrics() != nil {
			s.Metrics().RecordTransaction("commit")
		}
		return nil
	}

	tx := cs.Tx()
	txMgr := cs.TxMgr
	cs.ResetTranCount()
	cs.ClearTxFailed()
	delete(cs.Vars, "transaction_read_only")
	// Close non-WITH HOLD cursors on COMMIT
	if cs.Cursors != nil {
		for _, cur := range cs.Cursors {
			if !cur.WithHold {
				cur.IsOpen = false
				cur.IsClosed = true
				cur.Rows = nil
				cur.Pos = 0
			}
		}
	}
	cs.Mu.Unlock()

	if txMgr != nil {
		err = txMgr.Commit(context.Background())
	} else if tx != nil {
		defer tx.Rollback()
		err = tx.Commit()
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
	cs.lastUsedAt = time.Now()
	if cs.Tx() == nil {
		cs.Mu.Unlock()
		return fmt.Errorf("no active transaction to rollback on session %s", cs.ID)
	}
	tx := cs.Tx()
	txMgr := cs.TxMgr
	cs.ResetTranCount()
	cs.ClearTxFailed()
	delete(cs.Vars, "transaction_read_only")
	// Close all cursors on ROLLBACK (per SQL standard lifecycle)
	if cs.Cursors != nil {
		for _, cur := range cs.Cursors {
			cur.IsOpen = false
			cur.IsClosed = true
			cur.Rows = nil
			cur.Pos = 0
		}
	}
	cs.Mu.Unlock()

	if txMgr != nil {
		err = txMgr.Rollback(context.Background())
	} else if tx != nil {
		err = tx.Rollback()
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

	// Procedural definitions (procedures, functions, triggers) encapsulate their own scope:
	// ambient session table variables and temporary tables must NOT be rewritten inside procedure bodies.
	isProceduralDDL := sqlutil.HasAnyPrefixKeyword(trimmed,
		"CREATE PROCEDURE", "CREATE PROC", "CREATE OR ALTER PROCEDURE", "CREATE OR ALTER PROC", "ALTER PROCEDURE", "ALTER PROC",
		"CREATE FUNCTION", "CREATE OR ALTER FUNCTION", "ALTER FUNCTION",
		"CREATE TRIGGER", "CREATE OR ALTER TRIGGER", "ALTER TRIGGER",
	)

	// Rewrite table variables (@table -> physical table)
	if cs != nil && !isProceduralDDL {
		cs.Mu.Lock()
		_, _, isTblDecl, _ := parseTableVarDecl(trimmed)
		isCreateTbl := sqlutil.HasAnyPrefixKeyword(trimmed, "CREATE TABLE", "CREATE TEMP TABLE", "CREATE TEMPORARY TABLE")
		if len(cs.TableVars) > 0 && !isTblDecl {
			for varName, physTable := range cs.TableVars {
				trimmed = substituteParamOutsideQuotes(trimmed, "@"+strings.TrimPrefix(varName, "@"), "["+physTable+"]")
			}
			upper = strings.ToUpper(trimmed)
		}
		if len(cs.TempTableMap) > 0 && !isCreateTbl {
			bareBindings := make(map[string]string)
			for userTbl, physTable := range cs.TempTableMap {
				if !strings.HasPrefix(userTbl, "#") {
					bareBindings[userTbl] = "[" + physTable + "]"
				}
			}
			if len(bareBindings) > 0 {
				trimmed = sqlutil.SubstituteIdentsSinglePass(trimmed, bareBindings)
				upper = strings.ToUpper(trimmed)
			}
		}
		cs.Mu.Unlock()
	}

	// Rewrite session-isolated local temporary tables (#temp -> __temp_<sessionID>_temp)
	if cs != nil && strings.Contains(trimmed, "#") && !isProceduralDDL {
		trimmed = RewriteSessionTempTables(trimmed, cs)
		upper = strings.ToUpper(trimmed)
	}

	// Process Management: KILL <spid> [WITH STATUSONLY]
	if spid, ok := parseKillCommand(trimmed); ok {
		// [SEC-009] Check caller permission: only sysadmin (sa, dbo) can kill sessions
		if cs != nil {
			cs.Mu.Lock()
			caller := cs.User
			cs.Mu.Unlock()
			if !strings.EqualFold(caller, "sa") && !strings.EqualFold(caller, "dbo") {
				return nil, fmt.Errorf("Msg 224, Level 16, State 1: User does not have permission to use the KILL statement.")
			}
		}
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
	isBeginTx := (sqlutil.HasPrefixKeyword(trimmed, "BEGIN") && isBeginTransaction(trimmed[len("BEGIN"):])) ||
		sqlutil.HasPrefixKeyword(trimmed, "START TRANSACTION")

	if isBeginTx {
		opts := storage.TxOptions{}
		if sqlutil.ContainsKeyword(trimmed, "READ ONLY", false) {
			opts.ReadOnly = true
		} else if sqlutil.ContainsKeyword(trimmed, "READ WRITE", false) {
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

	if sqlutil.HasPrefixKeyword(trimmed, "COMMIT") {
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

	if namePart, ok := trimAnyPrefix(upper, trimmed, "SAVE TRANSACTION", "SAVE TRAN", "SAVEPOINT"); ok {
		cs.Mu.Lock()
		defer cs.Mu.Unlock()
		if cs.Tx() == nil {
			return nil, fmt.Errorf("no active transaction to create savepoint on session %s", cs.ID)
		}
		spName := cleanSavepointName(namePart)
		if spName == "" {
			return nil, fmt.Errorf("syntax error: savepoint name required")
		}
		if cs.TxMgr != nil {
			if err := cs.TxMgr.Savepoint(spName); err != nil {
				return nil, fmt.Errorf("savepoint error: %w", err)
			}
		} else if cs.Tx() != nil {
			if err := cs.Tx().Savepoint(spName); err != nil {
				return nil, fmt.Errorf("savepoint error: %w", err)
			}
		}
		return &QueryResult{
			Success:    true,
			Command:    "SAVEPOINT",
			Messages:   []string{fmt.Sprintf("Savepoint %s created.", spName)},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	if namePart, ok := trimAnyPrefix(upper, trimmed, "RELEASE SAVEPOINT", "RELEASE"); ok {
		cs.Mu.Lock()
		defer cs.Mu.Unlock()
		if cs.Tx() == nil {
			return nil, fmt.Errorf("no active transaction to release savepoint on session %s", cs.ID)
		}
		spName := cleanSavepointName(namePart)
		if spName == "" {
			return nil, fmt.Errorf("syntax error: savepoint name required")
		}
		if cs.TxMgr != nil {
			if err := cs.TxMgr.ReleaseSavepoint(spName); err != nil {
				return nil, fmt.Errorf("release savepoint error: %w", err)
			}
		} else if cs.Tx() != nil {
			if err := cs.Tx().ReleaseSavepoint(spName); err != nil {
				return nil, fmt.Errorf("release savepoint error: %w", err)
			}
		}
		return &QueryResult{
			Success:    true,
			Command:    "RELEASE",
			Messages:   []string{fmt.Sprintf("Savepoint %s released.", spName)},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	if sqlutil.HasPrefixKeyword(trimmed, "ROLLBACK") {
		var spName string
		if rest, ok := trimAnyPrefix(upper, trimmed, "ROLLBACK WORK TO SAVEPOINT", "ROLLBACK TO SAVEPOINT", "ROLLBACK TO"); ok {
			spName = cleanSavepointName(rest)
		} else if rest, ok := trimAnyPrefix(upper, trimmed, "ROLLBACK TRANSACTION", "ROLLBACK TRAN"); ok {
			rem := cleanSavepointName(rest)
			if rem != "" && !strings.EqualFold(rem, "WORK") {
				spName = rem
			}
		}

		if spName != "" {
			cs.Mu.Lock()
			defer cs.Mu.Unlock()
			if cs.Tx() == nil {
				return nil, fmt.Errorf("no active transaction to rollback on session %s", cs.ID)
			}
			if cs.TxMgr != nil {
				if err := cs.TxMgr.RollbackTo(spName); err != nil {
					return nil, fmt.Errorf("rollback to savepoint error: %w", err)
				}
			} else {
				if err := cs.Tx().RollbackTo(spName); err != nil {
					return nil, fmt.Errorf("rollback to savepoint error: %w", err)
				}
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

	// 0.045 Connection Pool Reset: DISCARD { ALL | PLANS | SEQUENCES | TEMPORARY | TEMP } / RESET ALL / DEALLOCATE ALL / UNLISTEN *
	if sqlutil.HasPrefixKeyword(trimmed, "DISCARD") {
		sub := strings.TrimRight(strings.TrimSpace(trimmed[len("DISCARD"):]), ";")
		subUpper := strings.ToUpper(strings.TrimSpace(sub))
		switch subUpper {
		case "ALL":
			if cs != nil {
				// [TX-006] DISCARD ALL cannot run inside a transaction block
				if cs.Tx() != nil {
					return nil, fmt.Errorf("ERROR 25001: DISCARD ALL cannot run inside a transaction block")
				}
				_ = s.ResetSessionWithOptions(cs.ID, ResetOptionsAll())
			}
			return &QueryResult{
				Success:    true,
				Command:    "DISCARD ALL",
				DurationUS: time.Since(start).Microseconds(),
			}, nil

		case "PLANS":
			if cs != nil {
				_ = s.ResetSessionWithOptions(cs.ID, ResetOptionsPlansOnly())
			}
			return &QueryResult{
				Success:    true,
				Command:    "DISCARD PLANS",
				DurationUS: time.Since(start).Microseconds(),
			}, nil

		case "SEQUENCES":
			// In PostgreSQL, DISCARD SEQUENCES discards cached sequence-related state in session.
			return &QueryResult{
				Success:    true,
				Command:    "DISCARD SEQUENCES",
				DurationUS: time.Since(start).Microseconds(),
			}, nil

		case "TEMP", "TEMPORARY":
			if cs != nil {
				if cs.Tx() != nil {
					return nil, fmt.Errorf("ERROR 25001: DISCARD TEMP cannot run inside a transaction block")
				}
				_ = s.ResetSessionWithOptions(cs.ID, ResetOptionsTempOnly())
			}
			return &QueryResult{
				Success:    true,
				Command:    "DISCARD TEMP",
				DurationUS: time.Since(start).Microseconds(),
			}, nil

		default:
			return nil, fmt.Errorf("syntax error at or near %q: expected ALL, PLANS, SEQUENCES, TEMPORARY, or TEMP after DISCARD", sub)
		}
	}

	if sqlutil.HasAnyPrefixKeyword(trimmed, "RESET ALL", "DEALLOCATE ALL", "UNLISTEN") {
		if cs != nil {
			_ = s.ResetSessionWithOptions(cs.ID, ResetOptionsAll())
		}
		cmd := "RESET"
		if sqlutil.HasPrefixKeyword(trimmed, "DEALLOCATE ALL") {
			cmd = "DEALLOCATE ALL"
		} else if sqlutil.HasPrefixKeyword(trimmed, "UNLISTEN") {
			cmd = "UNLISTEN"
		}
		return &QueryResult{
			Success:    true,
			Command:    cmd,
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	// 0.046 T-SQL & Standard Configuration Parameters: SET
	if sqlutil.HasPrefixKeyword(trimmed, "SET") && !strings.HasPrefix(strings.TrimSpace(trimmed[3:]), "@") {
		rest := strings.TrimSpace(trimmed[3:])

		// A. SET TRANSACTION ISOLATION LEVEL <level>
		if rem, ok := sqlutil.TrimPrefixKeyword(rest, "TRANSACTION ISOLATION LEVEL"); ok {
			level := strings.TrimSpace(rem)
			normLevel := strings.ToUpper(level)
			// [TX-005] Validate isolation level
			validLevels := map[string]bool{
				"READ UNCOMMITTED": true,
				"READ COMMITTED":   true,
				"REPEATABLE READ":  true,
				"SERIALIZABLE":     true,
				"SNAPSHOT":         true,
			}
			if !validLevels[normLevel] {
				return nil, fmt.Errorf("invalid transaction isolation level: %q", level)
			}
			cs.Mu.Lock()
			lowerLevel := strings.ToLower(normLevel)
			cs.Vars["transaction_isolation"] = lowerLevel
			cs.Vars["transaction_isolation_level"] = lowerLevel
			cs.Vars["default_transaction_isolation"] = lowerLevel
			cs.Mu.Unlock()
			return &QueryResult{
				Success:    true,
				Command:    "SET",
				Messages:   []string{fmt.Sprintf("Transaction isolation level set to %s.", level)},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}

		// B. SET TRANSACTION READ ONLY / READ WRITE
		if rem, ok := sqlutil.TrimPrefixKeyword(rest, "TRANSACTION READ"); ok {
			mode := strings.TrimSpace(rem)
			if !strings.EqualFold(mode, "ONLY") && !strings.EqualFold(mode, "WRITE") {
				return nil, fmt.Errorf("invalid transaction access mode: READ %s", mode)
			}
			isRO := strings.EqualFold(mode, "ONLY")
			cs.Mu.Lock()
			cs.Vars["transaction_read_only"] = isRO
			if cs.Tx() != nil {
				cs.Tx().SetReadOnly(isRO)
			}
			if txCtx := s.getTxContext(cs); txCtx != nil {
				txCtx.Opts.ReadOnly = isRO
			}
			cs.Mu.Unlock()
			return &QueryResult{
				Success:    true,
				Command:    "SET",
				Messages:   []string{fmt.Sprintf("Transaction access mode set to READ %s.", mode)},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}

		// C. SET TIME ZONE <tz>
		if rem, ok := sqlutil.TrimAnyPrefixKeyword(rest, "TIME ZONE", "TIMEZONE"); ok {
			tz := strings.Trim(strings.TrimSpace(rem), "=';\" ")
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
		if rem, ok := sqlutil.TrimPrefixKeyword(rest, "NAMES"); ok {
			enc := strings.Trim(strings.TrimSpace(rem), "=';\" ")
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
		if rem, ok := sqlutil.TrimPrefixKeyword(rest, "IDENTITY_INSERT"); ok {
			parts := strings.Fields(rem)
			if len(parts) >= 2 {
				targetTable := cleanIdentifier(parts[0])
				state := strings.ToUpper(strings.Trim(parts[1], ";'\""))
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
		if sqlutil.FindTopLevelChar(rest, '=') == -1 && !sqlutil.ContainsKeyword(rest, "TO", false) {
			rawItems := sqlutil.SplitTopLevel(rest, ',')
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
				if strings.HasPrefix(strings.TrimSpace(valStr), "-") {
					return nil, fmt.Errorf("ERROR 22023: invalid value for parameter \"work_mem\": %q", valStr)
				}
				bytes, err := execution.ParseMemSize(valStr)
				if err != nil || bytes < 0 {
					return nil, fmt.Errorf("ERROR 22023: invalid value for parameter \"work_mem\": %q", valStr)
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
			if param == "statement_timeout" || param == "lock_timeout" || param == "query_timeout" {
				if strings.HasPrefix(strings.TrimSpace(valStr), "-") {
					return nil, fmt.Errorf("ERROR 22023: invalid value for parameter %q: %q", param, valStr)
				}
				d, err := ParseTimeoutDurationStrict(valStr)
				if err != nil || d < 0 {
					return nil, fmt.Errorf("ERROR 22023: invalid value for parameter %q: %q", param, valStr)
				}
				cs.Mu.Lock()
				cs.Vars[param] = valStr
				cs.Mu.Unlock()
				return &QueryResult{
					Success:    true,
					Command:    "SET",
					Messages:   []string{fmt.Sprintf("SET: %s set to %v.", param, d)},
					DurationUS: time.Since(start).Microseconds(),
				}, nil
			}
			if param == "idle_in_transaction_session_timeout" || param == "tx_idle_timeout" {
				if strings.HasPrefix(strings.TrimSpace(valStr), "-") {
					return nil, fmt.Errorf("ERROR 22023: invalid value for parameter %q: %q", param, valStr)
				}
				d, err := ParseTimeoutDurationStrict(valStr)
				if err != nil || d < 0 {
					return nil, fmt.Errorf("ERROR 22023: invalid value for parameter %q: %q", param, valStr)
				}
				cs.SetIdleInTxTimeout(d)
				cs.Mu.Lock()
				cs.Vars[param] = valStr
				cs.Mu.Unlock()
				return &QueryResult{
					Success:    true,
					Command:    "SET",
					Messages:   []string{fmt.Sprintf("SET: %s set to %v.", param, d)},
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
			case "server_version_num":
				valStr = "150000"
			case "server_encoding", "client_encoding":
				valStr = "UTF8"
			case "timezone", "time_zone":
				valStr = "UTC"
			case "standard_conforming_strings":
				valStr = "on"
			case "integer_datetimes":
				valStr = "on"
			case "max_connections":
				valStr = strconv.Itoa(s.MaxConnections())
			case "idle_in_transaction_session_timeout", "tx_idle_timeout":
				var ttl time.Duration
				if cs != nil {
					ttl = cs.IdleInTxTimeout(s.ClientSessionIdleTTL())
				} else {
					ttl = s.ClientSessionIdleTTL()
				}
				if ttl <= 0 {
					valStr = "0"
				} else if ttl%time.Minute == 0 {
					valStr = fmt.Sprintf("%dmin", int(ttl/time.Minute))
				} else if ttl%time.Second == 0 {
					valStr = fmt.Sprintf("%ds", int(ttl/time.Second))
				} else {
					valStr = fmt.Sprintf("%dms", ttl.Milliseconds())
				}
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
	if sqlutil.HasPrefixKeyword(trimmed, "FETCH") {
		return s.execFetchCursor(trimmed, start, cs)
	}

	// 0.077 Server-Side SQL Cursors: MOVE
	if sqlutil.HasPrefixKeyword(trimmed, "MOVE") {
		return s.execMoveCursor(trimmed, start, cs)
	}

	// 0.078 Server-Side SQL Cursors: OPEN / CLOSE
	if sqlutil.HasPrefixKeyword(trimmed, "OPEN") {
		return s.execOpenCursor(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "CLOSE") {
		return s.execCloseCursor(trimmed, start, cs)
	}

	// 0.08 T-SQL DECLARE variables: DECLARE @var [type] [= expr], ... or DECLARE @table TABLE (...)
	if sqlutil.HasPrefixKeyword(trimmed, "DECLARE") {
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

			createSQL := fmt.Sprintf("CREATE TABLE [%s] (%s)", physTable, colDefs)
			res, err := s.execCreateTable(createSQL, start, cs)
			if err != nil {
				return nil, err
			}
			if !res.Success {
				return res, nil
			}

			if cs != nil {
				cs.RegisterTableVar(varName, physTable)
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
			if len(varFields) > 2 && strings.EqualFold(varFields[1], "AS") {
				varFields = append(varFields[:1], varFields[2:]...)
			}
			varName := varFields[0]
			if !strings.HasPrefix(varName, "@") {
				varName = "@" + varName
			}
			if len(varFields) > 1 {
				rawType := strings.ToUpper(varFields[1])
				typeName := rawType
				varLen := -1
				if idx := strings.Index(typeName, "("); idx != -1 {
					inner := strings.Trim(typeName[idx+1:], " )")
					typeName = typeName[:idx]
					switch typeName {
					case "VARCHAR", "CHAR", "NVARCHAR", "NCHAR", "BINARY", "VARBINARY":
						if inner != "MAX" {
							if l, err := strconv.Atoi(inner); err == nil {
								varLen = l
							}
						}
					}
				} else {
					// In T-SQL, VARCHAR, CHAR, NVARCHAR, NCHAR, BINARY, VARBINARY
					// default to length 1 when declared without explicit length specification.
					switch typeName {
					case "VARCHAR", "CHAR", "NVARCHAR", "NCHAR", "BINARY", "VARBINARY":
						varLen = 1
					}
				}
				if varLen > 0 {
					if cs.VarLengths == nil {
						cs.VarLengths = make(map[string]int)
					}
					cs.VarLengths[varName] = varLen
				} else if cs.VarLengths != nil {
					delete(cs.VarLengths, varName)
				}
				dt := proc.ParseDataType(typeName)
				cs.SetVarTypeLocked(varName, dt)
			}
			var val any = nil
			if len(eqParts) == 2 {
				val = evalSessionVarExpr(eqParts[1], cs.Vars)
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
			if err := cs.CoerceAndSetVarLocked(varName, val); err != nil {
				cs.Mu.Unlock()
				return nil, err
			}
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
		val := evalSessionVarExpr(rawExpr, cs.Vars)
		if err := cs.CoerceAndSetVarLocked(varName, val); err != nil {
			return nil, err
		}
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
		if sv, ok := val.(storage.Value); ok {
			val = formatStorageValue(sv)
		}
		return &QueryResult{
			Success:      true,
			Command:      "SELECT",
			Columns:      []string{colHeader},
			Rows:         []map[string]any{{colHeader: val, varName: val}},
			RowsAffected: 1,
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	// 0.21 Variable Assignment: SELECT @var = expr [, ...] [FROM ...]
	if isAssign, res, err := s.tryExecSelectVarAssignment(trimmed, start, cs); isAssign {
		return res, err
	}

	// 0.25 Prepared Statements: PREPARE / DEALLOCATE / DROP PREPARE
	if sqlutil.HasPrefixKeyword(trimmed, "PREPARE") {
		return s.execPrepare(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "DEALLOCATE") || sqlutil.HasPrefixKeyword(trimmed, "DROP PREPARE") {
		return s.execDeallocate(trimmed, start, cs)
	}

	// 0.27 Conditional DDL statements: IF [NOT] EXISTS (...) <DDL>, IF OBJECT_ID(...) <DDL>
	if sqlutil.HasPrefixKeyword(trimmed, "IF") {
		if res, handled, err := s.tryExecConditionalDDL(trimmed, start, cs); handled {
			return res, err
		}
	}

	// 0.28 Control-flow & Anonymous Blocks: IF ... ELSE, WHILE ..., BEGIN ... END
	if sqlutil.HasPrefixKeyword(trimmed, "IF") || sqlutil.HasPrefixKeyword(trimmed, "WHILE") {
		return s.execAnonymousBlock(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "BEGIN") {
		idx := sqlutil.FindTopLevelKeyword(trimmed, "BEGIN")
		if idx != -1 && !isBeginTransaction(trimmed[idx+5:]) {
			return s.execAnonymousBlock(trimmed, start, cs)
		}
	}

	// 0.4 Query Execution Plan: EXPLAIN [ANALYZE]
	if sqlutil.HasPrefixKeyword(trimmed, "EXPLAIN") {
		return s.execExplain(trimmed, start)
	}

	// 0.5 CBO Statistics Collection: ANALYZE [TABLE] [table_name]
	if sqlutil.HasPrefixKeyword(trimmed, "ANALYZE") && !sqlutil.HasPrefixKeyword(trimmed, "ANALYZE SELECT") {
		return s.execAnalyze(trimmed, start)
	}

	// 0.6 T-SQL Database Console Commands: DBCC
	if sqlutil.HasPrefixKeyword(trimmed, "DBCC") {
		return s.execDBCC(trimmed, start, cs)
	}

	// 1. Procedural DDL: CREATE PROCEDURE / ALTER PROCEDURE / DROP PROCEDURE / CREATE FUNCTION / CREATE TRIGGER
	if sqlutil.HasAnyPrefixKeyword(trimmed,
		"CREATE PROCEDURE", "CREATE PROC",
		"CREATE OR ALTER PROCEDURE", "CREATE OR ALTER PROC",
		"ALTER PROCEDURE", "ALTER PROC",
	) {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execCreateProcedure(trimmed, start)
	}
	if sqlutil.HasAnyPrefixKeyword(trimmed, "DROP PROCEDURE", "DROP PROC") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execDropProcedure(trimmed, start)
	}
	if sqlutil.HasAnyPrefixKeyword(trimmed, "CREATE FUNCTION", "CREATE OR REPLACE FUNCTION", "CREATE OR ALTER FUNCTION", "ALTER FUNCTION") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execCreateFunction(trimmed, start)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "DROP FUNCTION") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execDropFunction(trimmed, start)
	}
	if sqlutil.HasAnyPrefixKeyword(trimmed,
		"CREATE TRIGGER", "CREATE OR REPLACE TRIGGER", "CREATE OR ALTER TRIGGER", "ALTER TRIGGER",
		"CREATE TEMP TRIGGER", "CREATE TEMPORARY TRIGGER",
	) {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execCreateTrigger(trimmed, start)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "DROP TRIGGER") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execDropTrigger(trimmed, start)
	}

	// 1.5 DDL: CREATE VIEW / CREATE OR REPLACE VIEW / CREATE OR ALTER VIEW / ALTER VIEW / CREATE TEMP VIEW / DROP VIEW
	if sqlutil.HasAnyPrefixKeyword(trimmed, "CREATE VIEW", "CREATE OR REPLACE VIEW", "CREATE OR ALTER VIEW", "ALTER VIEW", "CREATE TEMP VIEW", "CREATE TEMPORARY VIEW") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execCreateView(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "DROP VIEW") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execDropView(trimmed, start, cs)
	}

	// 1.6 RBAC DCL / Security: LOGIN, USER, GRANT, REVOKE, DENY, SETUSER, EXECUTE AS USER, REVERT
	if sqlutil.HasPrefixKeyword(trimmed, "CREATE LOGIN") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execCreateLogin(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "ALTER LOGIN") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execAlterLogin(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "DROP LOGIN") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execDropLogin(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "CREATE USER") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execCreateUser(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "DROP USER") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execDropUser(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "GRANT") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execGrant(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "REVOKE") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execRevoke(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "DENY") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execDeny(trimmed, start, cs)
	}
	if sqlutil.HasAnyPrefixKeyword(trimmed, "SETUSER", "EXECUTE AS USER", "EXEC AS USER", "REVERT") {
		return s.execSetUser(trimmed, start, cs)
	}

	// 2. Procedure Invocation: EXEC / CALL / sp_*
	if sqlutil.HasAnyPrefixKeyword(trimmed, "EXEC", "EXECUTE", "CALL") || strings.HasPrefix(strings.ToUpper(trimmed), "SP_") {
		return s.execProcedureCall(trimmed, start, cs)
	}

	// 2.1 User Exception: RAISERROR
	if sqlutil.HasPrefixKeyword(trimmed, "RAISERROR") {
		return s.execRaiserror(trimmed, start)
	}

	// 2.2 User Exception: THROW
	if sqlutil.HasPrefixKeyword(trimmed, "THROW") {
		return s.execThrow(trimmed, start)
	}

	// 2.3 Informational Message: PRINT
	if sqlutil.HasPrefixKeyword(trimmed, "PRINT") {
		return s.execPrint(trimmed, start, cs)
	}

	// 3. DDL: CREATE TABLE
	if sqlutil.HasAnyPrefixKeyword(trimmed, "CREATE TABLE", "CREATE TEMP TABLE", "CREATE TEMPORARY TABLE") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execCreateTable(trimmed, start, cs)
	}

	// 3.1 DDL: CREATE INDEX
	if sqlutil.HasAnyPrefixKeyword(trimmed, "CREATE INDEX", "CREATE UNIQUE INDEX") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execCreateIndex(trimmed, start, cs)
	}

	// 3.2 DDL: CREATE STATISTICS (C-5)
	if sqlutil.HasPrefixKeyword(trimmed, "CREATE STATISTICS") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execCreateStatistics(trimmed, start)
	}

	// 4. DDL: DROP TABLE
	if sqlutil.HasPrefixKeyword(trimmed, "DROP TABLE") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execDropTable(trimmed, start, cs)
	}

	// 4.1 DDL: DROP INDEX
	if sqlutil.HasPrefixKeyword(trimmed, "DROP INDEX") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execDropIndex(trimmed, start, cs)
	}

	// 4.1.2 DDL/Maintenance: REINDEX
	if sqlutil.HasPrefixKeyword(trimmed, "REINDEX") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execReindex(trimmed, start)
	}

	// 4.1.5 DDL: DROP STATISTICS (C-5)
	if sqlutil.HasPrefixKeyword(trimmed, "DROP STATISTICS") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execDropStatistics(trimmed, start)
	}

	// 4.2 DDL/DML: TRUNCATE TABLE
	if sqlutil.HasAnyPrefixKeyword(trimmed, "TRUNCATE TABLE", "TRUNCATE") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execTruncateTable(trimmed, start, cs)
	}

	// 4.3 DDL: ALTER TABLE
	if sqlutil.HasPrefixKeyword(trimmed, "ALTER TABLE") {
		if err := s.checkDDLAllowed(cs); err != nil {
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
	if sqlutil.HasAnyPrefixKeyword(trimmed, "CREATE SCHEMA", "DROP SCHEMA") {
		return &QueryResult{
			Success:    true,
			Command:    "SCHEMA",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"Schema command completed successfully."},
		}, nil
	}

	if sqlutil.HasAnyPrefixKeyword(trimmed, "CREATE EXTENSION", "DROP EXTENSION") {
		return &QueryResult{
			Success:    true,
			Command:    "CREATE EXTENSION",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"Extension command completed successfully."},
		}, nil
	}

	// 4.38 Tolerant PG Compatibility: CREATE / DROP / ALTER ROLE
	if sqlutil.HasAnyPrefixKeyword(trimmed, "CREATE ROLE", "DROP ROLE", "ALTER ROLE") {
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

	// 4.41 DDL: SEQUENCE
	if sqlutil.HasPrefixKeyword(trimmed, "CREATE SEQUENCE") {
		return s.execCreateSequence(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "ALTER SEQUENCE") {
		return s.execAlterSequence(trimmed, start, cs)
	}
	if sqlutil.HasPrefixKeyword(trimmed, "DROP SEQUENCE") {
		return s.execDropSequence(trimmed, start, cs)
	}

	// 4.42 Tolerant PG Compatibility: LOCK TABLE
	if sqlutil.HasAnyPrefixKeyword(trimmed, "LOCK TABLE", "LOCK") {
		// [ENG-017] Verify target relation exists; fail with 42P01 if absent.
		rest := strings.TrimSpace(trimmed)
		if rem, ok := sqlutil.TrimPrefixKeyword(rest, "LOCK TABLE"); ok {
			rest = strings.TrimSpace(rem)
		} else if rem, ok := sqlutil.TrimPrefixKeyword(rest, "LOCK"); ok {
			rest = strings.TrimSpace(rem)
		}
		// Optional TABLE keyword if parsed after LOCK
		if rem, ok := sqlutil.TrimPrefixKeyword(rest, "TABLE"); ok {
			rest = strings.TrimSpace(rem)
		}
		// Extract table identifier up to whitespace, semicolon, or IN clause
		targetTable := rest
		if idx := strings.IndexAny(rest, " \t\r\n;"); idx != -1 {
			targetTable = rest[:idx]
		}
		targetTable = cleanIdentifier(targetTable)
		if targetTable != "" {
			tbl, err := s.engine.GetTable(targetTable)
			if err != nil || tbl == nil {
				return nil, fmt.Errorf("ERROR 42P01: relation %q does not exist", targetTable)
			}
		}

		return &QueryResult{
			Success:    true,
			Command:    "LOCK TABLE",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{"LOCK TABLE executed successfully."},
		}, nil
	}

	// 5. DML: INSERT INTO / UPSERT
	if kind, _, ok := dmlHead(trimmed); ok && kind == dmlInsert {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execInsert(trimmed, start, cs)
	}

	// 6. DQL: WITH (Common Table Expressions) / SELECT (including parenthesized SELECT)
	if isSelectOrParenthesizedQuery(trimmed) {
		unwrapped := sqlutil.UnwrapOuterParens(trimmed)
		if sqlutil.HasPrefixKeyword(unwrapped, "WITH") {
			if virtual.IsVirtualCatalogJoinQuery(unwrapped) {
				return s.execVirtualCatalogJoinSelect(unwrapped, start, cs)
			}
			return s.execCTE(unwrapped, start, cs)
		}
		return s.execSelect(trimmed, start, cs)
	}

	// 6.5 DQL: Standalone Table Value Constructor: VALUES (...), (...)
	if sqlutil.HasPrefixKeyword(trimmed, "VALUES") {
		return evaluateValuesToQueryResult(trimmed, nil, start)
	}

	// 7. DML: UPDATE
	if kind, _, ok := dmlHead(trimmed); ok && kind == dmlUpdate {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execUpdate(trimmed, start, cs)
	}

	// 8. DML: DELETE
	if kind, _, ok := dmlHead(trimmed); ok && kind == dmlDelete {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execDelete(trimmed, start, cs)
	}

	// 9. DML: MERGE INTO
	if sqlutil.HasPrefixKeyword(trimmed, "MERGE") {
		if err := s.checkDDLAllowed(cs); err != nil {
			return nil, err
		}
		return s.execMerge(trimmed, start, cs)
	}

	diagErr := proc.DiagnoseParseFailure(trimmed, fmt.Errorf("unrecognized or unsupported SQL statement: %q", trimmed))
	return &QueryResult{
		Success:    false,
		Error:      diagErr.Error(),
		DurationUS: time.Since(start).Microseconds(),
	}, diagErr
}

// cleanIdentifier strips quotes, brackets, and prefixes from table/column names.
func cleanIdentifier(s string) string {
	s = strings.TrimSpace(s)
	if idx := sqlutil.FindTopLevelKeyword(s, "WITH"); idx != -1 {
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

// checkDDLAllowed verifies replica read-only mode and active session read-only transaction mode.
func (s *Coordinator) checkDDLAllowed(cs *ClientSession) error {
	if err := s.checkWriteAllowed(); err != nil {
		return err
	}
	if cs != nil {
		cs.Mu.Lock()
		isRO := cs.isReadOnlyLocked()
		cs.Mu.Unlock()
		if isRO {
			return fmt.Errorf("Msg 3923, Level 16, State 1: Cannot execute DDL statements in a read-only transaction.")
		}
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
	if remAfter, ok := sqlutil.TrimPrefixKeyword(rem, "RAISERROR"); ok {
		rem = remAfter
	}
	rem = strings.TrimRight(rem, ";")
	rem = strings.TrimSpace(rem)

	// If wrapped in parentheses: RAISERROR(...)
	if strings.HasPrefix(rem, "(") {
		if closeIdx := sqlutil.FindMatchingParen(rem, 0); closeIdx != -1 {
			rem = strings.TrimSpace(rem[1:closeIdx])
		}
	}

	// Check if trailing WITH clause exists, e.g. WITH NOWAIT, WITH LOG, WITH SETERROR
	if idx := sqlutil.FindTopLevelKeyword(rem, "WITH"); idx != -1 {
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
	if remAfter, ok := sqlutil.TrimPrefixKeyword(rem, "THROW"); ok {
		rem = remAfter
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

func (s *Coordinator) execPrint(raw string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	rem := strings.TrimSpace(raw)
	if remAfter, ok := sqlutil.TrimPrefixKeyword(rem, "PRINT"); ok {
		rem = strings.TrimSpace(remAfter)
	}
	rem = strings.TrimRight(rem, ";")
	rem = strings.TrimSpace(rem)

	if rem == "" {
		return &QueryResult{
			Success:    true,
			Command:    "PRINT",
			Messages:   []string{""},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	evalMap := make(map[string]any)
	if cs != nil {
		cs.Mu.Lock()
		for k, v := range cs.Vars {
			evalMap[k] = v
			clean := strings.TrimPrefix(k, "@")
			evalMap[clean] = v
		}
		cs.Mu.Unlock()
	}

	p := proc.NewParser(proc.NewLexer(rem))
	msg := rem
	if ast := p.ParseExpr(); ast != nil {
		if res, err := proc.EvalRowExpr(ast, evalMap, nil); err == nil && res != nil {
			msg = fmt.Sprintf("%v", res)
		} else if strings.HasPrefix(rem, "'") && strings.HasSuffix(rem, "'") && len(rem) >= 2 {
			msg = strings.ReplaceAll(rem[1:len(rem)-1], "''", "'")
		}
	} else if strings.HasPrefix(rem, "'") && strings.HasSuffix(rem, "'") && len(rem) >= 2 {
		msg = strings.ReplaceAll(rem[1:len(rem)-1], "''", "'")
	}

	return &QueryResult{
		Success:    true,
		Command:    "PRINT",
		Messages:   []string{msg},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}

// isSelectOrParenthesizedQuery reports whether the query string represents a SELECT or WITH statement,
// including cases where the entire statement or its leading branch is enclosed in parentheses (e.g. (SELECT 1),
// ((SELECT 1)) UNION (SELECT 2), ((SELECT 1) UNION (SELECT 2))).
func isSelectOrParenthesizedQuery(sql string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(sql), ";")
	trimmed = sqlutil.StripLeadingComments(trimmed)
	if sqlutil.HasAnyPrefixKeyword(trimmed, "SELECT", "WITH") || planner.HasTopLevelSetOp(trimmed) {
		return true
	}
	if len(trimmed) > 0 && trimmed[0] == '(' {
		// 1) 닫는 쌍이 문장의 끝인 경우: 외곽 괄호를 모두 벗긴 결과가 SELECT/WITH인지 확인
		unwrapped := sqlutil.UnwrapOuterParens(trimmed)
		if sqlutil.HasAnyPrefixKeyword(unwrapped, "SELECT", "WITH") || planner.HasTopLevelSetOp(unwrapped) {
			return true
		}
		// 2) 닫는 쌍 뒤에 내용이 남는 경우: 첫 괄호 블록 뒤에 집합 연산자(UNION 등)가 이어지는지 확인
		firstClose := sqlutil.FindMatchingParen(trimmed, 0)
		if firstClose != -1 {
			afterParen := strings.TrimSpace(trimmed[firstClose+1:])
			if sqlutil.HasAnyPrefixKeyword(afterParen, "UNION", "INTERSECT", "EXCEPT", "MINUS") {
				return true
			}
		}
	}
	return false
}
