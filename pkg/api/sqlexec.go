package api

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

	"sqlion/pkg/catalog"
	"sqlion/pkg/execution"
	"sqlion/pkg/planner"
	"sqlion/pkg/proc"
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

	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cs.mu.Lock()
	cs.QueryCtx = queryCtx
	cs.CancelFunc = cancel
	cs.RunningQuery = query
	cs.QueryStartTime = time.Now()
	cs.CancelRequested = false
	cs.mu.Unlock()

	defer func() {
		cs.mu.Lock()
		cs.QueryCtx = nil
		cs.CancelFunc = nil
		cs.RunningQuery = ""
		cs.mu.Unlock()
	}()

	stmts := splitStatements(query)
	if len(stmts) == 0 {
		return &QueryResult{Success: true, Command: "EMPTY"}, nil
	}

	var lastRes *QueryResult
	for _, stmt := range stmts {
		select {
		case <-queryCtx.Done():
			return nil, fmt.Errorf("canceling statement due to user request")
		default:
		}

		res, err := s.execSingleStatement(cs, stmt)
		if err != nil {
			if cs != nil {
				cs.mu.Lock()
				cs.Vars["error"] = int64(50000)
				cs.mu.Unlock()
			}
			if errors.Is(err, context.Canceled) || (queryCtx.Err() != nil && errors.Is(queryCtx.Err(), context.Canceled)) {
				return nil, fmt.Errorf("canceling statement due to user request")
			}
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
	if strings.HasPrefix(upper, "BEGIN TRANSACTION") || strings.HasPrefix(upper, "BEGIN TRAN") || upper == "BEGIN" || strings.HasPrefix(upper, "START TRANSACTION") {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		if cs.Tx != nil {
			return nil, fmt.Errorf("transaction already active on session %s", cs.ID)
		}
		tx, err := cs.Session.BeginTx(storage.TxOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to start transaction: %w", err)
		}
		cs.Tx = tx
		return &QueryResult{
			Success:    true,
			Command:    "BEGIN",
			Messages:   []string{"Transaction started."},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	if strings.HasPrefix(upper, "COMMIT TRANSACTION") || strings.HasPrefix(upper, "COMMIT TRAN") || upper == "COMMIT" {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		if cs.Tx == nil {
			return nil, fmt.Errorf("no active transaction to commit on session %s", cs.ID)
		}
		err := cs.Tx.Commit()
		cs.Tx = nil
		if err != nil {
			return nil, fmt.Errorf("commit error: %w", err)
		}
		return &QueryResult{
			Success:    true,
			Command:    "COMMIT",
			Messages:   []string{"Transaction committed."},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	if strings.HasPrefix(upper, "ROLLBACK TRANSACTION") || strings.HasPrefix(upper, "ROLLBACK TRAN") || upper == "ROLLBACK" {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		if cs.Tx == nil {
			return nil, fmt.Errorf("no active transaction to rollback on session %s", cs.ID)
		}
		err := cs.Tx.Rollback()
		cs.Tx = nil
		if err != nil {
			return nil, fmt.Errorf("rollback error: %w", err)
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

	// 0.3 Substitute session variables in SQL query if present
	cs.mu.Lock()
	if len(cs.Vars) > 0 {
		for k, v := range cs.Vars {
			if strings.Contains(trimmed, k) {
				varRep := fmt.Sprintf("'%v'", v)
				switch v.(type) {
				case int, int64, float64, bool:
					varRep = fmt.Sprintf("%v", v)
				}
				trimmed = strings.ReplaceAll(trimmed, k, varRep)
			}
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

	// 1. Procedural DDL: CREATE PROCEDURE / CREATE FUNCTION / CREATE TRIGGER
	if strings.HasPrefix(upper, "CREATE PROCEDURE") || strings.HasPrefix(upper, "CREATE PROC") {
		return s.execCreateProcedure(trimmed, start)
	}
	if strings.HasPrefix(upper, "CREATE FUNCTION") {
		return s.execCreateFunction(trimmed, start)
	}
	if strings.HasPrefix(upper, "CREATE TRIGGER") {
		return s.execCreateTrigger(trimmed, start)
	}

	// 2. Procedure Invocation: EXEC / CALL / sp_*
	if strings.HasPrefix(upper, "EXEC ") || strings.HasPrefix(upper, "EXECUTE ") || strings.HasPrefix(upper, "CALL ") || strings.HasPrefix(upper, "SP_") {
		return s.execProcedureCall(trimmed, start, cs)
	}

	// 3. DDL: CREATE TABLE
	if strings.HasPrefix(upper, "CREATE TABLE") {
		return s.execCreateTable(trimmed, start, cs)
	}

	// 3.1 DDL: CREATE INDEX
	if strings.HasPrefix(upper, "CREATE INDEX") || strings.HasPrefix(upper, "CREATE UNIQUE INDEX") {
		return s.execCreateIndex(trimmed, start)
	}

	// 4. DDL: DROP TABLE
	if strings.HasPrefix(upper, "DROP TABLE") {
		return s.execDropTable(trimmed, start)
	}

	// 4.1 DDL: DROP INDEX
	if strings.HasPrefix(upper, "DROP INDEX") {
		return s.execDropIndex(trimmed, start)
	}

	// 4.2 DDL/DML: TRUNCATE TABLE
	if strings.HasPrefix(upper, "TRUNCATE TABLE") || strings.HasPrefix(upper, "TRUNCATE ") {
		return s.execTruncateTable(trimmed, start)
	}

	// 5. DML: INSERT INTO
	if strings.HasPrefix(upper, "INSERT INTO") || strings.HasPrefix(upper, "INSERT ") {
		return s.execInsert(trimmed, start, cs)
	}

	// 6. DQL: WITH (Common Table Expressions) / SELECT
	if strings.HasPrefix(upper, "WITH ") {
		return s.execCTE(trimmed, start)
	}
	if strings.HasPrefix(upper, "SELECT") {
		return s.execSelect(trimmed, start, cs)
	}

	// 7. DML: UPDATE
	if strings.HasPrefix(upper, "UPDATE ") {
		return s.execUpdate(trimmed, start, cs)
	}

	// 8. DML: DELETE
	if strings.HasPrefix(upper, "DELETE FROM") || strings.HasPrefix(upper, "DELETE ") {
		return s.execDelete(trimmed, start, cs)
	}

	return &QueryResult{
		Success:    false,
		Error:      fmt.Sprintf("unrecognized or unsupported SQL statement: %q", trimmed),
		DurationUS: time.Since(start).Microseconds(),
	}, fmt.Errorf("unsupported SQL statement")
}

func (s *Server) execCreateTable(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	re := regexp.MustCompile(`(?is)^CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([^\s(]+)\s*\((.*)\)$`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid CREATE TABLE syntax")
	}

	tableName := cleanIdentifier(matches[1])
	colsDef := matches[2]

	colDefs := splitColumnDefs(colsDef)
	if len(colDefs) == 0 {
		return nil, fmt.Errorf("no columns defined for table %s", tableName)
	}

	schema := &storage.TableSchema{
		Name:    tableName,
		Columns: make([]storage.Column, 0, len(colDefs)),
	}

	for _, cDef := range colDefs {
		cDefTrimmed := strings.TrimSpace(cDef)
		if cDefTrimmed == "" {
			continue
		}

		cUpper := strings.ToUpper(cDefTrimmed)
		if strings.HasPrefix(cUpper, "PRIMARY KEY") || (strings.HasPrefix(cUpper, "CONSTRAINT") && strings.Contains(cUpper, "PRIMARY KEY")) {
			// e.g. PRIMARY KEY (id) or CONSTRAINT pk_foo PRIMARY KEY (id)
			if idx := strings.Index(cDefTrimmed, "("); idx != -1 {
				pkCol := cleanIdentifier(strings.Trim(cDefTrimmed[idx:], "() "))
				for i := range schema.Columns {
					if strings.EqualFold(schema.Columns[i].Name, pkCol) {
						schema.Columns[i].PrimaryKey = true
					}
				}
			}
			continue
		}

		if strings.HasPrefix(cUpper, "FOREIGN KEY") || (strings.HasPrefix(cUpper, "CONSTRAINT") && strings.Contains(cUpper, "FOREIGN KEY")) {
			if fk, ok := parseForeignKeyClause(cDefTrimmed, "", ""); ok {
				fk.ChildTable = tableName
				schema.ForeignKeys = append(schema.ForeignKeys, *fk)
			}
			continue
		}

		tokens := strings.Fields(cDefTrimmed)
		if len(tokens) < 2 {
			continue
		}

		colName := cleanIdentifier(tokens[0])
		rawType := strings.ToUpper(tokens[1])
		if idx := strings.Index(rawType, "("); idx != -1 {
			rawType = rawType[:idx]
		}

		dataType := parseSQLDataType(rawType)
		precision := 0
		scale := 0
		if dataType == storage.TypeDecimal {
			precision = 18
			scale = 2
			if rawType == "MONEY" {
				precision = 19
				scale = 4
			} else {
				decRe := regexp.MustCompile(`(?i)\b(?:DECIMAL|NUMERIC)\s*\(\s*(\d+)(?:\s*,\s*(\d+))?\s*\)`)
				if m := decRe.FindStringSubmatch(cDefTrimmed); len(m) > 1 {
					if p, err := strconv.Atoi(m[1]); err == nil {
						precision = p
					}
					if len(m) > 2 && m[2] != "" {
						if sc, err := strconv.Atoi(m[2]); err == nil {
							scale = sc
						}
					} else {
						scale = 0
					}
				}
			}
		}

		isPK := strings.Contains(cUpper, "PRIMARY KEY")
		isNotNull := strings.Contains(cUpper, "NOT NULL") || isPK
		isIdentity := strings.Contains(cUpper, "IDENTITY") || strings.Contains(cUpper, "AUTO_INCREMENT")

		schema.Columns = append(schema.Columns, storage.Column{
			Name:       colName,
			Type:       dataType,
			PrimaryKey: isPK,
			Nullable:   !isNotNull,
			Identity:   isIdentity,
			Precision:  precision,
			Scale:      scale,
		})

		if strings.Contains(cUpper, "REFERENCES") {
			if fk, ok := parseForeignKeyClause(cDefTrimmed, colName, ""); ok {
				fk.ChildTable = tableName
				schema.ForeignKeys = append(schema.ForeignKeys, *fk)
			}
		}
	}

	// Default first column as PK if none marked
	hasPK := false
	for _, c := range schema.Columns {
		if c.PrimaryKey {
			hasPK = true
			break
		}
	}
	if !hasPK && len(schema.Columns) > 0 {
		schema.Columns[0].PrimaryKey = true
	}

	if err := s.engine.CreateTable(schema); err != nil {
		if strings.Contains(strings.ToUpper(query), "IF NOT EXISTS") && errors.Is(err, storage.ErrTableExists) {
			return &QueryResult{
				Success:    true,
				Command:    "CREATE TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Table %q already exists (skipped)", tableName)},
			}, nil
		}
		return nil, err
	}

	if strings.HasPrefix(tableName, "##") {
		s.globalTempMu.Lock()
		if s.globalTempCreators == nil {
			s.globalTempCreators = make(map[string]string)
		}
		creatorID := "default"
		if cs != nil && cs.ID != "" {
			creatorID = cs.ID
		}
		s.globalTempCreators[tableName] = creatorID
		s.globalTempMu.Unlock()
	} else if strings.HasPrefix(tableName, "#") {
		if cs != nil {
			cs.mu.Lock()
			cs.TempTables = append(cs.TempTables, tableName)
			cs.mu.Unlock()
		}
	}

	return &QueryResult{
		Success:    true,
		Command:    "CREATE TABLE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Table %q created successfully with %d columns", tableName, len(schema.Columns))},
	}, nil
}

func (s *Server) execDropTable(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	re := regexp.MustCompile(`(?is)^DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([^\s;]+)`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 2 {
		return nil, fmt.Errorf("invalid DROP TABLE syntax")
	}

	tableName := cleanIdentifier(matches[1])
	if err := s.engine.DropTable(tableName); err != nil {
		if strings.Contains(strings.ToUpper(query), "IF EXISTS") && errors.Is(err, storage.ErrTableNotFound) {
			return &QueryResult{
				Success:    true,
				Command:    "DROP TABLE",
				DurationUS: time.Since(start).Microseconds(),
				Messages:   []string{fmt.Sprintf("Table %q does not exist (skipped)", tableName)},
			}, nil
		}
		return nil, err
	}

	if strings.HasPrefix(tableName, "##") {
		s.globalTempMu.Lock()
		delete(s.globalTempCreators, tableName)
		s.globalTempMu.Unlock()
	}

	return &QueryResult{
		Success:    true,
		Command:    "DROP TABLE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Table %q dropped successfully", tableName)},
	}, nil
}

func (s *Server) execTruncateTable(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	re := regexp.MustCompile(`(?is)^TRUNCATE\s+(?:TABLE\s+)?([^\s;]+)`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 2 {
		return nil, fmt.Errorf("invalid TRUNCATE TABLE syntax")
	}

	tableName := cleanIdentifier(matches[1])
	if err := s.engine.TruncateTable(tableName); err != nil {
		return nil, err
	}

	return &QueryResult{
		Success:    true,
		Command:    "TRUNCATE TABLE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Table %q truncated successfully", tableName)},
	}, nil
}

func (s *Server) execCreateIndex(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	re := regexp.MustCompile(`(?is)^CREATE\s+(UNIQUE\s+)?INDEX\s+([^\s]+)\s+ON\s+([^\s(]+)\s*\((.*?)\)$`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 5 {
		return nil, fmt.Errorf("invalid CREATE INDEX syntax: expected CREATE [UNIQUE] INDEX <name> ON <table> (<cols>)")
	}

	isUnique := strings.TrimSpace(matches[1]) != ""
	indexName := cleanIdentifier(matches[2])
	tableName := cleanIdentifier(matches[3])
	colsRaw := matches[4]

	var cols []string
	for _, c := range strings.Split(colsRaw, ",") {
		cTrimmed := cleanIdentifier(strings.TrimSpace(c))
		if cTrimmed != "" {
			cols = append(cols, cTrimmed)
		}
	}

	if len(cols) == 0 {
		return nil, fmt.Errorf("no columns specified for index %q", indexName)
	}

	idxSchema := &storage.IndexSchema{
		Name:      indexName,
		TableName: tableName,
		Columns:   cols,
		Unique:    isUnique,
	}

	if err := s.engine.CreateIndex(idxSchema); err != nil {
		return nil, err
	}

	msg := fmt.Sprintf("Index %q created successfully on table %q.", indexName, tableName)
	if isUnique {
		msg = fmt.Sprintf("Unique index %q created successfully on table %q.", indexName, tableName)
	}

	return &QueryResult{
		Success:    true,
		Command:    "CREATE INDEX",
		Messages:   []string{msg},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execDropIndex(query string, start time.Time) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")
	re := regexp.MustCompile(`(?is)^DROP\s+INDEX\s+([^\s]+)(?:\s+ON\s+([^\s]+))?$`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 2 {
		return nil, fmt.Errorf("invalid DROP INDEX syntax: expected DROP INDEX <name> [ON <table>]")
	}

	indexName := cleanIdentifier(matches[1])
	tableName := ""
	if len(matches) > 2 {
		tableName = cleanIdentifier(matches[2])
	}

	if tableName == "" {
		tables, err := s.engine.ListTables()
		if err != nil {
			return nil, err
		}
		for _, t := range tables {
			if _, _, found := t.FindIndex(indexName); found {
				tableName = t.Name
				break
			}
		}
		if tableName == "" {
			return nil, fmt.Errorf("index %q not found on any table", indexName)
		}
	}

	if err := s.engine.DropIndex(tableName, indexName); err != nil {
		return nil, err
	}

	return &QueryResult{
		Success:    true,
		Command:    "DROP INDEX",
		Messages:   []string{fmt.Sprintf("Index %q dropped successfully from table %q.", indexName, tableName)},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execInsert(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// Primary Fast-Path: Unified VDBE Virtual Machine execution
	if res, err := s.execVBDEDML("INSERT", cleaned, start, cs); err == nil {
		return res, nil
	} else if strings.Contains(err.Error(), "IDENTITY_INSERT") {
		return nil, err
	}
	re := regexp.MustCompile(`(?is)^INSERT\s+INTO\s+([^\s(]+)(?:\s*\(([^)]+)\))?\s*VALUES\s*(.*)$`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 4 {
		return nil, fmt.Errorf("invalid INSERT INTO syntax")
	}

	tableName := cleanIdentifier(matches[1])
	rawCols := matches[2]
	rawValues := matches[3]

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	var identityCol *storage.Column
	for i := range schema.Columns {
		if schema.Columns[i].Identity {
			identityCol = &schema.Columns[i]
			break
		}
	}

	identityInsertAllowed := false
	if cs != nil {
		cs.mu.Lock()
		if cur, ok := cs.Vars["identity_insert_table"].(string); ok {
			if strings.EqualFold(cur, tableName) {
				identityInsertAllowed = true
			}
		}
		cs.mu.Unlock()
	}

	var targetCols []string
	if rawCols != "" {
		for _, c := range strings.Split(rawCols, ",") {
			targetCols = append(targetCols, cleanIdentifier(c))
		}
	} else {
		if identityCol != nil && !identityInsertAllowed {
			for _, c := range schema.Columns {
				if !c.Identity {
					targetCols = append(targetCols, c.Name)
				}
			}
		} else {
			for _, c := range schema.Columns {
				targetCols = append(targetCols, c.Name)
			}
		}
	}

	hasIdentityInTarget := false
	if identityCol != nil {
		for _, c := range targetCols {
			if strings.EqualFold(c, identityCol.Name) {
				hasIdentityInTarget = true
				break
			}
		}
		if hasIdentityInTarget && !identityInsertAllowed {
			return nil, fmt.Errorf("Cannot insert explicit value for identity column in table %q when IDENTITY_INSERT is set to OFF", tableName)
		}
	}

	tupleStrings := parseValueTuples(rawValues)
	if len(tupleStrings) == 0 {
		return nil, fmt.Errorf("no values supplied in INSERT statement")
	}

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

		t, err := session.BeginTx(storage.TxOptions{})
		if err != nil {
			return nil, err
		}
		tx = t
		isEphemeral = true
	}

	var maxID int64 = 0
	if identityCol != nil {
		if cur, err := tx.Scan(tableName); err == nil {
			for cur.Next() {
				if r, err := cur.Record(); err == nil {
					if v, ok := r[identityCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull && v.IVal > maxID {
						maxID = v.IVal
					}
				}
			}
			cur.Close()
		}
	}

	insertedCount := int64(0)
	for _, tupleStr := range tupleStrings {
		vals := splitValues(tupleStr)
		if len(vals) != len(targetCols) {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("column count mismatch: expected %d values, got %d in tuple (%s)", len(targetCols), len(vals), tupleStr)
		}

		rec := make(storage.Record)
		for i, colName := range targetCols {
			colSchema, _, found := schema.FindColumn(colName)
			valStr := strings.TrimSpace(vals[i])
			if !found {
				rec[colName] = parseLiteralValue(valStr, storage.TypeVarChar)
			} else {
				rec[colName] = parseLiteralValue(valStr, colSchema.Type)
			}
		}

		if identityCol != nil {
			if !hasIdentityInTarget {
				maxID++
				rec[identityCol.Name] = storage.IntValue(maxID)
				if cs != nil {
					cs.mu.Lock()
					cs.Vars["identity"] = maxID
					cs.mu.Unlock()
				}
			} else {
				if v, ok := rec[identityCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull {
					if cs != nil {
						cs.mu.Lock()
						cs.Vars["identity"] = v.IVal
						cs.mu.Unlock()
					}
					if v.IVal > maxID {
						maxID = v.IVal
					}
				}
			}
		}

		if err := tx.Insert(tableName, rec); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("insert row error: %w", err)
		}
		insertedCount++
	}

	if isEphemeral {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("transaction commit error: %w", err)
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "INSERT",
		RowsAffected: insertedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows inserted into %q", insertedCount, tableName)},
	}, nil
}

func (s *Server) execExplain(query string, start time.Time) (*QueryResult, error) {
	upper := strings.ToUpper(strings.TrimSpace(query))
	if strings.HasPrefix(upper, "EXPLAIN BYTECODE ") {
		innerSQL := strings.TrimSpace(query[len("EXPLAIN BYTECODE "):])
		prog, err := proc.Compile(innerSQL)
		if err != nil {
			return nil, fmt.Errorf("EXPLAIN BYTECODE compilation error: %w", err)
		}
		dis := prog.Disassemble()
		lines := strings.Split(strings.TrimRight(dis, "\n"), "\n")
		var rows []map[string]any
		for _, line := range lines {
			rows = append(rows, map[string]any{
				"Bytecode": line,
			})
		}
		return &QueryResult{
			Success:      true,
			Command:      "EXPLAIN BYTECODE",
			Columns:      []string{"Bytecode"},
			Rows:         rows,
			RowsAffected: int64(len(rows)),
			DurationUS:   time.Since(start).Microseconds(),
			Messages:     []string{fmt.Sprintf("VDBE Program: %s (%s)", prog.Name, prog.Type)},
		}, nil
	}

	innerExplainSQL := query
	isAnalyze := false
	if strings.HasPrefix(upper, "EXPLAIN ANALYZE ") {
		innerExplainSQL = strings.TrimSpace(query[len("EXPLAIN ANALYZE "):])
		isAnalyze = true
	} else if strings.HasPrefix(upper, "EXPLAIN ") {
		innerExplainSQL = strings.TrimSpace(query[len("EXPLAIN "):])
	}
	if setQuery, ok := planner.FindTopLevelSetOps(innerExplainSQL); ok && setQuery != nil && len(setQuery.Segments) >= 2 {
		return s.execSetOpExplain(setQuery, isAnalyze, start)
	}

	p := planner.NewPlanner()
	qInfo, err := p.ParseSelect(query)
	if err != nil {
		return nil, fmt.Errorf("EXPLAIN parse error: %w", err)
	}

	rawTable := qInfo.TableName
	tableName := cleanIdentifier(rawTable)

	// Check if Table-Valued Function (TVF) invocation: e.g. FROM GetTopAccounts(100)
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
			prefix := "EXPLAIN "
			if qInfo.IsAnalyze {
				prefix = "EXPLAIN ANALYZE "
			}
			return s.execExplain(prefix+innerSQL, start)
		}
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	var leftStats *planner.TableStats
	if s.catalog != nil {
		leftStats, _ = s.catalog.GetTableStats(tableName)
	}

	var totalRows int64 = 0
	if leftStats != nil && leftStats.RowCount > 0 {
		totalRows = leftStats.RowCount
	} else {
		session, err := s.engine.NewSession()
		if err == nil {
			defer session.Close()
			tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
			if err == nil {
				defer tx.Rollback()
				if cur, err := tx.Scan(tableName); err == nil {
					defer cur.Close()
					for cur.Next() {
						totalRows++
					}
				}
			}
		}
		leftStats = planner.NewTableStats(tableName, totalRows)
	}

	var planTree *planner.PlanNode
	var joins []*planner.JoinInfo
	if len(qInfo.Joins) > 0 {
		joins = qInfo.Joins
	} else if qInfo.Join != nil {
		joins = []*planner.JoinInfo{qInfo.Join}
	}

	if len(joins) > 0 {
		rightSchemas := make(map[string]*storage.TableSchema)
		rightStats := make(map[string]*planner.TableStats)
		for _, j := range joins {
			rTable := cleanIdentifier(j.Table)
			rSchema, _ := s.engine.GetTable(rTable)
			if rSchema != nil {
				rightSchemas[j.Table] = rSchema
				rightSchemas[rTable] = rSchema
				if j.Alias != "" {
					rightSchemas[j.Alias] = rSchema
					rightSchemas[cleanIdentifier(j.Alias)] = rSchema
				}
			}
			var rStat *planner.TableStats
			if s.catalog != nil {
				rStat, _ = s.catalog.GetTableStats(rTable)
			}
			if rStat == nil {
				var rRows int64 = 0
				if session, err := s.engine.NewSession(); err == nil {
					func() {
						defer session.Close()
						if rTx, err := session.BeginTx(storage.TxOptions{ReadOnly: true}); err == nil {
							defer rTx.Rollback()
							if rCur, err := rTx.Scan(rTable); err == nil {
								defer rCur.Close()
								for rCur.Next() {
									rRows++
								}
							}
						}
					}()
				}
				rStat = planner.NewTableStats(rTable, rRows)
			}
			if rStat != nil {
				rightStats[j.Table] = rStat
				rightStats[rTable] = rStat
				if j.Alias != "" {
					rightStats[j.Alias] = rStat
					rightStats[cleanIdentifier(j.Alias)] = rStat
				}
			}
		}
		planTree = p.BuildMultiJoinPlanWithStats(qInfo, schema, leftStats, rightSchemas, rightStats)
	} else {
		planTree = p.BuildMultiJoinPlanWithStats(qInfo, schema, leftStats, nil, nil)
	}

	if qInfo.IsAnalyze {
		// Execute query to collect actual metrics
		execStart := time.Now()
		selectSQL := query
		upper := strings.ToUpper(strings.TrimSpace(query))
		if strings.HasPrefix(upper, "EXPLAIN ANALYZE ") {
			selectSQL = strings.TrimSpace(query[16:])
		} else if strings.HasPrefix(upper, "EXPLAIN ") {
			selectSQL = strings.TrimSpace(query[8:])
		}
		res, execErr := s.execSelect(selectSQL, execStart, nil)
		if execErr == nil && res != nil {
			planTree.ActualRows = int64(len(res.Rows))
			planTree.ActualDurationUS = res.DurationUS

			// Recursively populate actual rows / durations down the tree
			var populateActuals func(node *planner.PlanNode)
			populateActuals = func(node *planner.PlanNode) {
				if node == nil {
					return
				}
				if node.ActualRows == 0 && len(res.Rows) > 0 {
					node.ActualRows = int64(len(res.Rows))
					node.ActualDurationUS = res.DurationUS
				}
				for _, child := range node.Children {
					populateActuals(child)
				}
			}
			populateActuals(planTree)
		}
	}

	lines := planner.FormatPlanText(planTree, qInfo.IsAnalyze)
	rows := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, map[string]any{
			"QUERY PLAN": line,
		})
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

func findSubquery(s string, prefixPattern *regexp.Regexp) (matchStart int, matchEnd int, prefixStr string, innerSQL string, found bool) {
	loc := prefixPattern.FindStringSubmatchIndex(s)
	if loc == nil {
		return 0, 0, "", "", false
	}
	openParenIdx := -1
	for i := loc[0]; i < len(s); i++ {
		if s[i] == '(' {
			openParenIdx = i
			break
		}
	}
	if openParenIdx == -1 {
		return 0, 0, "", "", false
	}

	depth := 0
	closeParenIdx := -1
	for i := openParenIdx; i < len(s); i++ {
		if s[i] == '(' {
			depth++
		} else if s[i] == ')' {
			depth--
			if depth == 0 {
				closeParenIdx = i
				break
			}
		}
	}
	if closeParenIdx == -1 {
		return 0, 0, "", "", false
	}

	inner := strings.TrimSpace(s[openParenIdx+1 : closeParenIdx])
	return loc[0], closeParenIdx + 1, s[loc[0]:openParenIdx], inner, true
}

func (s *Server) execAnalyze(query string, start time.Time) (*QueryResult, error) {
	parts := strings.Fields(strings.TrimSpace(query))

	var targetTable string
	if len(parts) >= 2 {
		if strings.EqualFold(parts[1], "TABLE") {
			if len(parts) >= 3 {
				targetTable = cleanIdentifier(strings.Trim(parts[2], ";"))
			}
		} else {
			targetTable = cleanIdentifier(strings.Trim(parts[1], ";"))
		}
	}

	var tablesToAnalyze []string
	if targetTable != "" {
		actualTable := targetTable
		if _, err := s.engine.GetTable(targetTable); err != nil {
			if all, listErr := s.engine.ListTables(); listErr == nil {
				for _, t := range all {
					if strings.EqualFold(t.Name, targetTable) {
						actualTable = t.Name
						break
					}
				}
			}
		}
		tablesToAnalyze = append(tablesToAnalyze, actualTable)
	} else {
		all, err := s.engine.ListTables()
		if err != nil {
			return nil, fmt.Errorf("ANALYZE failed to list tables: %w", err)
		}
		for _, t := range all {
			tablesToAnalyze = append(tablesToAnalyze, t.Name)
		}
	}

	if len(tablesToAnalyze) == 0 {
		return &QueryResult{
			Success:    true,
			Command:    "ANALYZE",
			Messages:   []string{"No tables found to analyze."},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	session, err := s.engine.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ANALYZE session error: %w", err)
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("ANALYZE transaction error: %w", err)
	}
	defer tx.Rollback()

	var messages []string
	for _, tbl := range tablesToAnalyze {
		schema, err := s.engine.GetTable(tbl)
		if err != nil {
			return nil, fmt.Errorf("table %q not found: %w", tbl, err)
		}

		cursor, err := tx.Scan(tbl)
		if err != nil {
			return nil, fmt.Errorf("ANALYZE scan error on %q: %w", tbl, err)
		}

		colVals := make(map[string][]storage.Value)
		nullCounts := make(map[string]int64)
		for _, c := range schema.Columns {
			colVals[c.Name] = make([]storage.Value, 0)
		}

		var rowCount int64 = 0
		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
				break
			}
			rowCount++
			for _, c := range schema.Columns {
				v, ok := rec[c.Name]
				if !ok || v.IsNull {
					nullCounts[c.Name]++
				} else {
					colVals[c.Name] = append(colVals[c.Name], v)
				}
			}
		}
		_ = cursor.Close()

		ts := planner.NewTableStats(tbl, rowCount)
		for _, c := range schema.Columns {
			ts.Columns[c.Name] = planner.BuildColumnStats(c.Name, c.Type, colVals[c.Name], nullCounts[c.Name], rowCount)
		}

		if s.catalog != nil {
			s.catalog.SetTableStats(tbl, ts)
		}
		messages = append(messages, fmt.Sprintf("Table %q analyzed successfully (%d rows, %d columns).", tbl, rowCount, len(schema.Columns)))
	}

	return &QueryResult{
		Success:      true,
		Command:      "ANALYZE",
		Messages:     messages,
		RowsAffected: int64(len(tablesToAnalyze)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) resolveSubqueries(whereClause string) (string, error) {
	if whereClause == "" {
		return "", nil
	}

	result := whereClause

	// 1. [NOT] EXISTS (SELECT ...)
	existsRe := regexp.MustCompile(`(?is)(?:NOT\s+)?EXISTS\s*\(\s*\(?\s*SELECT\b`)
	for {
		mStart, mEnd, prefix, innerSQL, found := findSubquery(result, existsRe)
		if !found {
			break
		}
		isNot := strings.Contains(strings.ToUpper(prefix), "NOT")
		innerRes, err := s.ExecuteSQL(innerSQL)
		hasRows := err == nil && innerRes != nil && len(innerRes.Rows) > 0
		replacement := "1=0"
		if isNot {
			if !hasRows {
				replacement = "1=1"
			}
		} else {
			if hasRows {
				replacement = "1=1"
			}
		}
		result = result[:mStart] + replacement + result[mEnd:]
	}

	// 2. [NOT] IN (SELECT ...) -> [NOT] IN (val1, val2, ...)
	inSelectRe := regexp.MustCompile(`(?is)\b([a-zA-Z0-9_\.]+)\s+(?:NOT\s+)?IN\s*\(\s*\(?\s*SELECT\b`)
	for {
		mStart, mEnd, prefix, innerSQL, found := findSubquery(result, inSelectRe)
		if !found {
			break
		}
		tokens := strings.Fields(prefix)
		colName := cleanIdentifier(tokens[0])
		isNot := strings.Contains(strings.ToUpper(prefix), "NOT")
		innerRes, err := s.ExecuteSQL(innerSQL)
		hasRows := err == nil && innerRes != nil && len(innerRes.Rows) > 0
		replacement := "1=0"
		if isNot {
			if !hasRows {
				replacement = "1=1"
			} else {
				var vals []string
				for _, r := range innerRes.Rows {
					if len(innerRes.Columns) > 0 {
						colKey := innerRes.Columns[0]
						if v, ok := r[colKey]; ok {
							vals = append(vals, fmt.Sprintf("'%v'", v))
							continue
						}
					}
					for _, v := range r {
						vals = append(vals, fmt.Sprintf("'%v'", v))
						break
					}
				}
				if len(vals) > 0 {
					replacement = fmt.Sprintf("%s NOT IN (%s)", colName, strings.Join(vals, ", "))
				}
			}
		} else {
			if hasRows {
				var vals []string
				for _, r := range innerRes.Rows {
					if len(innerRes.Columns) > 0 {
						colKey := innerRes.Columns[0]
						if v, ok := r[colKey]; ok {
							vals = append(vals, fmt.Sprintf("'%v'", v))
							continue
						}
					}
					for _, v := range r {
						vals = append(vals, fmt.Sprintf("'%v'", v))
						break
					}
				}
				if len(vals) > 0 {
					replacement = fmt.Sprintf("%s IN (%s)", colName, strings.Join(vals, ", "))
				}
			}
		}
		result = result[:mStart] + replacement + result[mEnd:]
	}

	// 3. Scalar Subquery in comparisons: e.g. > (SELECT ...), = (SELECT ...)
	scalarRe := regexp.MustCompile(`(?is)\(\s*SELECT\b`)
	for {
		mStart, mEnd, _, innerSQL, found := findSubquery(result, scalarRe)
		if !found {
			break
		}
		innerRes, err := s.ExecuteSQL(innerSQL)
		replacement := "NULL"
		if err == nil && innerRes != nil && len(innerRes.Rows) > 0 {
			r := innerRes.Rows[0]
			var foundVal any
			if len(innerRes.Columns) > 0 {
				colKey := innerRes.Columns[0]
				if v, ok := r[colKey]; ok && v != nil {
					foundVal = v
				}
			}
			if foundVal == nil {
				for _, v := range r {
					if v != nil {
						foundVal = v
						break
					}
				}
			}
			if foundVal != nil {
				replacement = fmt.Sprintf("'%v'", foundVal)
			}
		}
		result = result[:mStart] + replacement + result[mEnd:]
	}

	return result, nil
}

func (s *Server) execCTE(query string, start time.Time) (*QueryResult, error) {
	p := planner.NewPlanner()
	qInfo, err := p.ParseSelect(query)
	if err != nil {
		return nil, fmt.Errorf("parse CTE error: %w", err)
	}

	if len(qInfo.CTEs) == 0 {
		return s.execSelect(query, start, nil)
	}

	var tempTableNames []string
	defer func() {
		for _, t := range tempTableNames {
			_, _ = s.ExecuteSQL(fmt.Sprintf("DROP TABLE %s;", t))
		}
	}()

	mainSQL := query
	upper := strings.ToUpper(query)
	if strings.HasPrefix(upper, "WITH ") {
		depth := 0
		mainSelectIdx := -1
		for i := 0; i < len(query); i++ {
			if query[i] == '(' {
				depth++
			} else if query[i] == ')' {
				depth--
			} else if depth == 0 && strings.HasPrefix(strings.ToUpper(query[i:]), "SELECT") {
				mainSelectIdx = i
				break
			}
		}
		if mainSelectIdx != -1 {
			mainSQL = strings.TrimSpace(query[mainSelectIdx:])
		}
	}

	for _, cte := range qInfo.CTEs {
		// 1. Run CTE inner SQL
		res, err := s.ExecuteSQL(cte.InnerSQL)
		if err != nil {
			return nil, fmt.Errorf("CTE %q execution error: %w", cte.Name, err)
		}

		// 2. Materialize into #temp table
		tempName := fmt.Sprintf("#__cte_%s", cte.Name)
		tempTableNames = append(tempTableNames, tempName)

		if len(res.Columns) == 0 {
			continue
		}

		var colDefs []string
		for i, col := range res.Columns {
			pk := ""
			if i == 0 {
				pk = " PRIMARY KEY"
			}
			colDefs = append(colDefs, fmt.Sprintf("%s VARCHAR(255)%s", col, pk))
		}
		createSQL := fmt.Sprintf("CREATE TABLE %s (%s);", tempName, strings.Join(colDefs, ", "))
		if _, err := s.ExecuteSQL(createSQL); err != nil {
			return nil, fmt.Errorf("materialize CTE %q table error: %w", cte.Name, err)
		}

		if len(res.Rows) > 0 {
			var insertCols = res.Columns
			for _, row := range res.Rows {
				var valStrs []string
				for _, col := range insertCols {
					v := row[col]
					if v == nil {
						valStrs = append(valStrs, "NULL")
					} else {
						valStrs = append(valStrs, fmt.Sprintf("'%v'", v))
					}
				}
				insSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);", tempName, strings.Join(insertCols, ", "), strings.Join(valStrs, ", "))
				if _, err := s.ExecuteSQL(insSQL); err != nil {
					return nil, fmt.Errorf("materialize CTE insert error: %w", err)
				}
			}
		}

		// Rewrite occurrences of cte.Name in mainSQL with tempName
		cteWordRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(cte.Name) + `\b`)
		mainSQL = cteWordRe.ReplaceAllString(mainSQL, tempName)
	}

	return s.ExecuteSQL(mainSQL)
}

func (s *Server) execVolcanoSelect(qInfo *planner.QueryInfo, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if qInfo.WhereClause != "" {
		if resolved, err := s.resolveSubqueries(qInfo.WhereClause); err == nil {
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

	p := planner.NewPlanner()
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
	if s.defaultWorkMem > 0 {
		opts.WorkMem = s.defaultWorkMem
	}
	if cs != nil {
		cs.mu.Lock()
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
		cs.mu.Unlock()
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
	for {
		tuple, err := exec.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
				return nil, fmt.Errorf("canceling statement due to user request")
			}
			return nil, err
		}
		rows = append(rows, tuple.ToMap())
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

func (s *Server) evalScalarExpr(expr string, cs *ClientSession) (any, string) {
	trimmed := strings.TrimSpace(expr)
	upper := strings.ToUpper(trimmed)

	// Builtin system / database functions
	switch upper {
	case "CURRENT_DATABASE()", "CURRENT_DATABASE":
		return "sqlion", "current_database"
	case "CURRENT_SCHEMA()", "CURRENT_SCHEMA":
		return "dbo", "current_schema"
	case "CURRENT_SCHEMAS(TRUE)", "CURRENT_SCHEMAS(FALSE)", "CURRENT_SCHEMAS":
		return "{dbo,public}", "current_schemas"
	case "CURRENT_USER", "USER", "SESSION_USER":
		return "postgres", "current_user"
	case "VERSION()":
		return "PostgreSQL 15.0 (SQLion OLTP & Stored Procedure Server)", "version"
	case "DB_NAME()", "DB_NAME":
		return "sqlion", "db_name"
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
			cs.mu.Lock()
			val, ok := cs.Vars["rowcount"]
			cs.mu.Unlock()
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
			cs.mu.Lock()
			val, ok := cs.Vars["error"]
			cs.mu.Unlock()
			if ok && val != nil {
				return val, "@@error"
			}
		}
		return int64(0), "@@error"
	case "@@IDENTITY", "SCOPE_IDENTITY()", "SCOPE_IDENTITY":
		if cs != nil {
			cs.mu.Lock()
			val, ok := cs.Vars["identity"]
			cs.mu.Unlock()
			if ok && val != nil {
				return val, "@@identity"
			}
		}
		return nil, "@@identity"
	}

	// System variable @@var
	if strings.HasPrefix(upper, "@@") {
		varName := strings.ToLower(trimmed[2:])
		if cs != nil {
			cs.mu.Lock()
			v, exists := cs.Vars[varName]
			cs.mu.Unlock()
			if exists {
				return v, trimmed
			}
		}
		return "", trimmed
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
			s.sessMu.RLock()
			var targetID string
			for _, sess := range s.sessions {
				if sess.SPID == pid {
					targetID = sess.ID
					break
				}
			}
			s.sessMu.RUnlock()
			if targetID != "" {
				s.CloseSession(targetID)
			}
			return canceled || targetID != "", "pg_terminate_backend"
		}
		return false, "pg_terminate_backend"
	}

	// Session variable @var
	if strings.HasPrefix(trimmed, "@") {
		if cs != nil {
			cs.mu.Lock()
			v, exists := cs.Vars[trimmed]
			if !exists {
				v = cs.Vars[strings.TrimPrefix(trimmed, "@")]
			}
			cs.mu.Unlock()
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
			cs.mu.Lock()
			if len(cs.Vars) > 0 {
				varRow = make(map[string]any, len(cs.Vars))
				for k, v := range cs.Vars {
					varRow[k] = v
				}
			}
			cs.mu.Unlock()
		}
		if res, err := proc.EvalRowExpr(astExpr, varRow, nil); err == nil && res != nil {
			return res, trimmed
		}
	}

	return trimmed, trimmed
}

func (s *Server) execScalarSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
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

func (s *Server) execSelectInto(targetTable, projections, fromClause string, start time.Time, cs *ClientSession) (*QueryResult, error) {
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
		s.globalTempMu.Lock()
		if s.globalTempCreators == nil {
			s.globalTempCreators = make(map[string]string)
		}
		creatorID := "default"
		if cs != nil && cs.ID != "" {
			creatorID = cs.ID
		}
		s.globalTempCreators[targetTable] = creatorID
		s.globalTempMu.Unlock()
	} else if strings.HasPrefix(targetTable, "#") {
		if cs != nil {
			cs.mu.Lock()
			cs.TempTables = append(cs.TempTables, targetTable)
			cs.mu.Unlock()
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

			t, err := session.BeginTx(storage.TxOptions{})
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

func (s *Server) execVBDEDML(command, cleaned string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	prog, err := proc.CompileStatement(cleaned)
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

		t, err := session.BeginTx(storage.TxOptions{})
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
		cs.mu.Lock()
		if len(cs.Vars) > 0 {
			params = make(map[string]any, len(cs.Vars))
			for k, v := range cs.Vars {
				params[k] = v
			}
		}
		cs.mu.Unlock()
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
		cs.mu.Lock()
		if execRes.LastIdentity > 0 {
			cs.Vars["identity"] = execRes.LastIdentity
			cs.Vars["@@identity"] = execRes.LastIdentity
		}
		cs.Vars["rowcount"] = execRes.RowsAffected
		cs.Vars["@@rowcount"] = execRes.RowsAffected
		cs.mu.Unlock()
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

func (s *Server) execVDBESelect(cleaned string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	prog, err := proc.CompileQuery(cleaned)
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
		cs.mu.Lock()
		if len(cs.Vars) > 0 {
			params = make(map[string]any, len(cs.Vars))
			for k, v := range cs.Vars {
				params[k] = v
			}
		}
		cs.mu.Unlock()
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

func (s *Server) execSetOpSelect(setQuery *planner.SetOpQuery, start time.Time, cs *ClientSession) (*QueryResult, error) {
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

func (s *Server) execSetOpExplain(setQuery *planner.SetOpQuery, analyze bool, start time.Time) (*QueryResult, error) {
	p := planner.NewPlanner()
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

func (s *Server) execSelect(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	if resolved, err := s.resolveSubqueries(cleaned); err == nil {
		cleaned = resolved
	}

	// 0. Set Operations: UNION [ALL], INTERSECT, EXCEPT / MINUS
	if setQuery, ok := planner.FindTopLevelSetOps(cleaned); ok && setQuery != nil && len(setQuery.Segments) >= 2 {
		return s.execSetOpSelect(setQuery, start, cs)
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
		return s.execScalarSelect(cleaned, start, cs)
	}

	p := planner.NewPlanner()
	qInfo, parseErr := p.ParseSelect(cleaned)
	if parseErr == nil && qInfo != nil {
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

		// Execute via unified VDBE Virtual Machine Engine (Primary Fast-Path)
		if res, err := s.execVDBESelect(cleaned, start, cs); err == nil {
			return res, nil
		}

		// Secondary Fallback: Direct Volcano execution
		if res, err := s.execVolcanoSelect(qInfo, start, cs); err == nil {
			return res, nil
		}
	}

	// Parse projection, table, WHERE, ORDER BY, LIMIT
	selectRe := regexp.MustCompile(`(?is)^SELECT\s+(.*?)\s+FROM\s+([^\s,;]+)(?:\s+WHERE\s+(.*?))?(?:\s+GROUP\s+BY\s+(.*?))?(?:\s+HAVING\s+(.*?))?(?:\s+ORDER\s+BY\s+(.*?))?(?:\s+LIMIT\s+(\d+))?$`)
	matches := selectRe.FindStringSubmatch(cleaned)
	if len(matches) < 3 {
		if !hasFromClause(cleaned) {
			return s.execScalarSelect(cleaned, start, cs)
		}
		return nil, fmt.Errorf("invalid or unsupported SELECT syntax")
	}

	projStr := strings.TrimSpace(matches[1])
	rawTable := strings.TrimSpace(matches[2])
	tableName := cleanIdentifier(rawTable)
	whereClause := ""
	if len(matches) > 3 {
		whereClause = strings.TrimSpace(matches[3])
		if resolved, err := s.resolveSubqueries(whereClause); err == nil {
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
		return s.execVirtualCatalogSelect(tableName, projStr, whereClause, orderByClause, limitCount, start)
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
		eqRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_]+)\s*=\s*(.+)$`)
		if m := eqRe.FindStringSubmatch(whereClause); len(m) == 3 {
			colName := cleanIdentifier(m[1])
			if idx, found := schema.FindIndexByColumn(colName); found {
				valStr := strings.Trim(strings.TrimSpace(m[2]), "'\"")
				if colMeta, _, colFound := schema.FindColumn(colName); colFound && colMeta != nil {
					parsedVal := parseValueToType(valStr, colMeta.Type)
					if cur, err := tx.IndexScan(tableName, idx.Name, &parsedVal, &parsedVal); err == nil {
						cursor = cur
						usedIndex = true
					}
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

	// 4. Apply LIMIT if present
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
				row[pi.alias] = nil
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

func (s *Server) execAggregate(rows []map[string]any, qInfo *planner.QueryInfo, start time.Time) (*QueryResult, error) {
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

	// 6. Apply LIMIT
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

func (s *Server) execJoin(qInfo *planner.QueryInfo, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if qInfo.WhereClause != "" {
		if resolved, err := s.resolveSubqueries(qInfo.WhereClause); err == nil {
			qInfo.WhereClause = resolved
		}
	}
	rightTable := cleanIdentifier(qInfo.Join.Table)
	rightSchema, err := s.engine.GetTable(rightTable)
	if err == nil && rightSchema != nil {
		rightCol := cleanIdentifier(qInfo.Join.RightKey)
		if idx := strings.Index(rightCol, "."); idx != -1 {
			rightCol = rightCol[idx+1:]
		}
		if idxMeta, found := rightSchema.FindIndexByColumn(rightCol); found {
			return s.execNestedLoopJoin(qInfo, start, idxMeta, cs)
		}
	}
	return s.execHashJoin(qInfo, start, cs)
}

func (s *Server) execNestedLoopJoin(qInfo *planner.QueryInfo, start time.Time, rightIdx *storage.IndexSchema, cs *ClientSession) (*QueryResult, error) {
	leftTable := cleanIdentifier(qInfo.TableName)
	rightTable := cleanIdentifier(qInfo.Join.Table)

	leftSchema, err := s.engine.GetTable(leftTable)
	if err != nil {
		return nil, fmt.Errorf("left table %q not found: %w", leftTable, err)
	}
	rightSchema, err := s.engine.GetTable(rightTable)
	if err != nil {
		return nil, fmt.Errorf("right table %q not found: %w", rightTable, err)
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

	leftCol := cleanIdentifier(qInfo.Join.LeftKey)
	if idx := strings.Index(leftCol, "."); idx != -1 {
		leftCol = leftCol[idx+1:]
	}

	type projField struct {
		expr  string
		alias string
	}
	var projFields []projField
	var selectedCols []string
	isSelectAll := len(qInfo.Projections) == 0 || (len(qInfo.Projections) == 1 && qInfo.Projections[0] == "*")
	if isSelectAll {
		for _, col := range leftSchema.Columns {
			projFields = append(projFields, projField{expr: col.Name, alias: col.Name})
			selectedCols = append(selectedCols, col.Name)
		}
		for _, col := range rightSchema.Columns {
			projFields = append(projFields, projField{expr: col.Name, alias: col.Name})
			selectedCols = append(selectedCols, col.Name)
		}
	} else {
		for _, p := range qInfo.Projections {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			asIdx := strings.Index(strings.ToUpper(p), " AS ")
			if asIdx != -1 {
				expr := cleanIdentifier(strings.TrimSpace(p[:asIdx]))
				alias := cleanIdentifier(strings.TrimSpace(p[asIdx+4:]))
				projFields = append(projFields, projField{expr: expr, alias: alias})
				selectedCols = append(selectedCols, alias)
			} else {
				fields := strings.Fields(p)
				if len(fields) == 2 {
					expr := cleanIdentifier(fields[0])
					alias := cleanIdentifier(fields[1])
					projFields = append(projFields, projField{expr: expr, alias: alias})
					selectedCols = append(selectedCols, alias)
				} else {
					col := cleanIdentifier(p)
					projFields = append(projFields, projField{expr: col, alias: col})
					selectedCols = append(selectedCols, col)
				}
			}
		}
	}

	// Outer Scan on leftTable
	lCur, err := tx.Scan(leftTable)
	if err != nil {
		return nil, fmt.Errorf("scan error on %s: %w", leftTable, err)
	}
	defer lCur.Close()

	var joinedRows []map[string]any

	for lCur.Next() {
		lRec, err := lCur.Record()
		if err != nil {
			break
		}
		lMap := make(map[string]any)
		for _, col := range leftSchema.Columns {
			v := formatStorageValue(lRec[col.Name])
			lMap[col.Name] = v
			lMap[leftTable+"."+col.Name] = v
			if qInfo.TableAlias != "" {
				lMap[qInfo.TableAlias+"."+col.Name] = v
			}
		}

		leftVal, ok := lRec[leftCol]
		matchedCount := 0

		if ok && !leftVal.IsNull {
			// Inner Index Scan on rightTable
			rIdxCur, err := tx.IndexScan(rightTable, rightIdx.Name, &leftVal, &leftVal)
			if err == nil {
				for rIdxCur.Next() {
					rRec, err := rIdxCur.Record()
					if err != nil {
						break
					}
					matchedCount++
					rMap := make(map[string]any)
					for _, col := range rightSchema.Columns {
						v := formatStorageValue(rRec[col.Name])
						rMap[col.Name] = v
						rMap[rightTable+"."+col.Name] = v
						if qInfo.Join.Alias != "" {
							rMap[qInfo.Join.Alias+"."+col.Name] = v
						}
					}

					combined := make(map[string]any)
					for k, v := range lMap {
						combined[k] = v
					}
					for k, v := range rMap {
						combined[k] = v
					}

					if qInfo.WhereClause != "" && !evaluateWhereConditionMap(combined, qInfo.WhereClause) {
						continue
					}

					row := make(map[string]any)
					for k, v := range combined {
						row[k] = v
					}
					for _, pf := range projFields {
						var val any
						if v, ok := combined[pf.expr]; ok {
							val = v
						} else {
							bare := pf.expr
							if idx := strings.Index(pf.expr, "."); idx != -1 {
								bare = pf.expr[idx+1:]
							}
							if v, ok := combined[bare]; ok {
								val = v
							}
						}
						row[pf.alias] = val
						row[pf.expr] = val
						if dotIdx := strings.Index(pf.expr, "."); dotIdx != -1 {
							row[pf.expr[dotIdx+1:]] = val
						}
					}
					joinedRows = append(joinedRows, row)
				}
				_ = rIdxCur.Close()
			}
		}

		if matchedCount == 0 && qInfo.Join.Type == "LEFT" {
			combined := make(map[string]any)
			for k, v := range lMap {
				combined[k] = v
			}
			for _, col := range rightSchema.Columns {
				combined[col.Name] = nil
				combined[rightTable+"."+col.Name] = nil
				if qInfo.Join.Alias != "" {
					combined[qInfo.Join.Alias+"."+col.Name] = nil
				}
			}

			if qInfo.WhereClause != "" && !evaluateWhereConditionMap(combined, qInfo.WhereClause) {
				continue
			}

			row := make(map[string]any)
			for k, v := range combined {
				row[k] = v
			}
			for _, pf := range projFields {
				var val any
				if v, ok := combined[pf.expr]; ok {
					val = v
				} else {
					bare := pf.expr
					if idx := strings.Index(pf.expr, "."); idx != -1 {
						bare = pf.expr[idx+1:]
					}
					if v, ok := combined[bare]; ok {
						val = v
					}
				}
				row[pf.alias] = val
				row[pf.expr] = val
				if dotIdx := strings.Index(pf.expr, "."); dotIdx != -1 {
					row[pf.expr[dotIdx+1:]] = val
				}
			}
			joinedRows = append(joinedRows, row)
		}
	}

	// Apply ORDER BY
	if qInfo.OrderByClause != "" && len(joinedRows) > 0 {
		orderParts := strings.Fields(qInfo.OrderByClause)
		orderCol := cleanIdentifier(orderParts[0])
		desc := len(orderParts) > 1 && strings.EqualFold(orderParts[1], "DESC")
		bareCol := orderCol
		if dotIdx := strings.Index(orderCol, "."); dotIdx != -1 {
			bareCol = orderCol[dotIdx+1:]
		}
		sort.Slice(joinedRows, func(i, j int) bool {
			val1, ok1 := joinedRows[i][orderCol]
			if !ok1 {
				val1 = joinedRows[i][bareCol]
			}
			val2, ok2 := joinedRows[j][orderCol]
			if !ok2 {
				val2 = joinedRows[j][bareCol]
			}
			v1 := fmt.Sprintf("%v", val1)
			v2 := fmt.Sprintf("%v", val2)
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

	// Apply LIMIT
	if qInfo.LimitCount >= 0 && int64(len(joinedRows)) > qInfo.LimitCount {
		joinedRows = joinedRows[:qInfo.LimitCount]
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      selectedCols,
		Rows:         joinedRows,
		RowsAffected: int64(len(joinedRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execHashJoin(qInfo *planner.QueryInfo, start time.Time, cs *ClientSession) (*QueryResult, error) {
	leftTable := cleanIdentifier(qInfo.TableName)
	rightTable := cleanIdentifier(qInfo.Join.Table)

	leftSchema, err := s.engine.GetTable(leftTable)
	if err != nil {
		return nil, fmt.Errorf("left table %q not found: %w", leftTable, err)
	}
	rightSchema, err := s.engine.GetTable(rightTable)
	if err != nil {
		return nil, fmt.Errorf("right table %q not found: %w", rightTable, err)
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

	// Extract join columns (strip dot prefix if present)
	leftCol := cleanIdentifier(qInfo.Join.LeftKey)
	if idx := strings.Index(leftCol, "."); idx != -1 {
		leftCol = leftCol[idx+1:]
	}
	rightCol := cleanIdentifier(qInfo.Join.RightKey)
	if idx := strings.Index(rightCol, "."); idx != -1 {
		rightCol = rightCol[idx+1:]
	}

	// 1. Build Phase: scan right table into hash map
	rCur, err := tx.Scan(rightTable)
	if err != nil {
		return nil, fmt.Errorf("scan error on %s: %w", rightTable, err)
	}
	defer rCur.Close()

	hashTable := make(map[string][]map[string]any)
	for rCur.Next() {
		rRec, err := rCur.Record()
		if err != nil {
			break
		}
		val, ok := rRec[rightCol]
		if !ok || val.IsNull {
			continue
		}
		key := fmt.Sprintf("%v", formatStorageValue(val))

		rMap := make(map[string]any)
		for _, col := range rightSchema.Columns {
			v := formatStorageValue(rRec[col.Name])
			rMap[col.Name] = v
			rMap[rightTable+"."+col.Name] = v
			if qInfo.Join.Alias != "" {
				rMap[qInfo.Join.Alias+"."+col.Name] = v
			}
		}
		hashTable[key] = append(hashTable[key], rMap)
	}

	// 2. Probe Phase: scan left table and probe hash map
	lCur, err := tx.Scan(leftTable)
	if err != nil {
		return nil, fmt.Errorf("scan error on %s: %w", leftTable, err)
	}
	defer lCur.Close()

	type projField struct {
		expr  string
		alias string
	}
	var projFields []projField
	var selectedCols []string
	isSelectAll := len(qInfo.Projections) == 0 || (len(qInfo.Projections) == 1 && qInfo.Projections[0] == "*")
	if isSelectAll {
		for _, col := range leftSchema.Columns {
			projFields = append(projFields, projField{expr: col.Name, alias: col.Name})
			selectedCols = append(selectedCols, col.Name)
		}
		for _, col := range rightSchema.Columns {
			projFields = append(projFields, projField{expr: col.Name, alias: col.Name})
			selectedCols = append(selectedCols, col.Name)
		}
	} else {
		for _, p := range qInfo.Projections {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			asIdx := strings.Index(strings.ToUpper(p), " AS ")
			if asIdx != -1 {
				expr := cleanIdentifier(strings.TrimSpace(p[:asIdx]))
				alias := cleanIdentifier(strings.TrimSpace(p[asIdx+4:]))
				projFields = append(projFields, projField{expr: expr, alias: alias})
				selectedCols = append(selectedCols, alias)
			} else {
				fields := strings.Fields(p)
				if len(fields) == 2 {
					expr := cleanIdentifier(fields[0])
					alias := cleanIdentifier(fields[1])
					projFields = append(projFields, projField{expr: expr, alias: alias})
					selectedCols = append(selectedCols, alias)
				} else {
					col := cleanIdentifier(p)
					projFields = append(projFields, projField{expr: col, alias: col})
					selectedCols = append(selectedCols, col)
				}
			}
		}
	}

	var joinedRows []map[string]any

	for lCur.Next() {
		lRec, err := lCur.Record()
		if err != nil {
			break
		}
		lMap := make(map[string]any)
		for _, col := range leftSchema.Columns {
			v := formatStorageValue(lRec[col.Name])
			lMap[col.Name] = v
			lMap[leftTable+"."+col.Name] = v
			if qInfo.TableAlias != "" {
				lMap[qInfo.TableAlias+"."+col.Name] = v
			}
		}

		val, ok := lRec[leftCol]
		var matchedRows []map[string]any
		if ok && !val.IsNull {
			key := fmt.Sprintf("%v", formatStorageValue(val))
			matchedRows = hashTable[key]
		}

		if len(matchedRows) > 0 {
			for _, rMap := range matchedRows {
				combined := make(map[string]any)
				for k, v := range lMap {
					combined[k] = v
				}
				for k, v := range rMap {
					combined[k] = v
				}

				if qInfo.WhereClause != "" && !evaluateWhereConditionMap(combined, qInfo.WhereClause) {
					continue
				}

				row := make(map[string]any)
				for k, v := range combined {
					row[k] = v
				}
				for _, pf := range projFields {
					var val any
					if v, ok := combined[pf.expr]; ok {
						val = v
					} else {
						bare := pf.expr
						if idx := strings.Index(pf.expr, "."); idx != -1 {
							bare = pf.expr[idx+1:]
						}
						if v, ok := combined[bare]; ok {
							val = v
						}
					}
					row[pf.alias] = val
					row[pf.expr] = val
					if dotIdx := strings.Index(pf.expr, "."); dotIdx != -1 {
						row[pf.expr[dotIdx+1:]] = val
					}
				}
				joinedRows = append(joinedRows, row)
			}
		} else if qInfo.Join.Type == "LEFT" {
			combined := make(map[string]any)
			for k, v := range lMap {
				combined[k] = v
			}
			for _, col := range rightSchema.Columns {
				combined[col.Name] = nil
				combined[rightTable+"."+col.Name] = nil
				if qInfo.Join.Alias != "" {
					combined[qInfo.Join.Alias+"."+col.Name] = nil
				}
			}

			if qInfo.WhereClause != "" && !evaluateWhereConditionMap(combined, qInfo.WhereClause) {
				continue
			}

			row := make(map[string]any)
			for _, pf := range projFields {
				var val any
				if v, ok := combined[pf.expr]; ok {
					val = v
				} else {
					bare := pf.expr
					if idx := strings.Index(pf.expr, "."); idx != -1 {
						bare = pf.expr[idx+1:]
					}
					if v, ok := combined[bare]; ok {
						val = v
					}
				}
				row[pf.alias] = val
				row[pf.expr] = val
				if dotIdx := strings.Index(pf.expr, "."); dotIdx != -1 {
					row[pf.expr[dotIdx+1:]] = val
				}
			}
			joinedRows = append(joinedRows, row)
		}
	}

	// 3. Apply ORDER BY
	if qInfo.OrderByClause != "" && len(joinedRows) > 0 {
		orderParts := strings.Fields(qInfo.OrderByClause)
		orderCol := cleanIdentifier(orderParts[0])
		desc := len(orderParts) > 1 && strings.EqualFold(orderParts[1], "DESC")
		bareCol := orderCol
		if dotIdx := strings.Index(orderCol, "."); dotIdx != -1 {
			bareCol = orderCol[dotIdx+1:]
		}
		sort.Slice(joinedRows, func(i, j int) bool {
			val1, ok1 := joinedRows[i][orderCol]
			if !ok1 {
				val1 = joinedRows[i][bareCol]
			}
			val2, ok2 := joinedRows[j][orderCol]
			if !ok2 {
				val2 = joinedRows[j][bareCol]
			}
			v1 := fmt.Sprintf("%v", val1)
			v2 := fmt.Sprintf("%v", val2)
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

	// 4. Apply LIMIT
	if qInfo.LimitCount >= 0 && int64(len(joinedRows)) > qInfo.LimitCount {
		joinedRows = joinedRows[:qInfo.LimitCount]
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      selectedCols,
		Rows:         joinedRows,
		RowsAffected: int64(len(joinedRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execUpdate(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// Primary Fast-Path: Unified VDBE Virtual Machine execution
	if res, err := s.execVBDEDML("UPDATE", cleaned, start, cs); err == nil {
		return res, nil
	}
	re := regexp.MustCompile(`(?is)^UPDATE\s+([^\s]+)\s+SET\s+(.*?)(?:\s+WHERE\s+(.*))?$`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid UPDATE syntax")
	}

	tableName := cleanIdentifier(matches[1])
	setClause := matches[2]
	whereClause := ""
	if len(matches) > 3 {
		whereClause = strings.TrimSpace(matches[3])
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

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

		t, err := session.BeginTx(storage.TxOptions{})
		if err != nil {
			return nil, err
		}
		tx = t
		isEphemeral = true
	}

	cursor, err := tx.Scan(tableName)
	if err != nil {
		if isEphemeral {
			_ = tx.Rollback()
		}
		return nil, err
	}
	defer cursor.Close()

	setPairs := strings.Split(setClause, ",")
	updatesMap := make(map[string]string)
	for _, pair := range setPairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			updatesMap[cleanIdentifier(kv[0])] = strings.TrimSpace(kv[1])
		}
	}

	type updateEntry struct {
		pk  storage.Value
		rec storage.Record
	}
	toUpdate := make([]updateEntry, 0)

	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			break
		}

		if whereClause != "" && !evaluateWhereCondition(rec, whereClause) {
			continue
		}

		pkVal, hasPK := rec["id"]
		if !hasPK {
			for _, v := range rec {
				pkVal = v
				hasPK = true
				break
			}
		}

		recUpdates := make(storage.Record)
		for colName, exprStr := range updatesMap {
			colSchema, _, found := schema.FindColumn(colName)
			targetType := storage.TypeVarChar
			if found {
				targetType = colSchema.Type
			}
			recUpdates[colName] = parseLiteralValue(exprStr, targetType)
		}

		if hasPK {
			toUpdate = append(toUpdate, updateEntry{pk: pkVal, rec: recUpdates})
		}
	}

	updatedCount := int64(0)
	for _, item := range toUpdate {
		if err := tx.Update(tableName, item.pk, item.rec); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("update row error: %w", err)
		}
		updatedCount++
	}

	if isEphemeral {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit error: %w", err)
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "UPDATE",
		RowsAffected: updatedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows updated in %q", updatedCount, tableName)},
	}, nil
}

func (s *Server) execDelete(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// Primary Fast-Path: Unified VDBE Virtual Machine execution
	if res, err := s.execVBDEDML("DELETE", cleaned, start, cs); err == nil {
		return res, nil
	}
	re := regexp.MustCompile(`(?is)^DELETE\s+FROM\s+([^\s;]+)(?:\s+WHERE\s+(.*))?$`)
	matches := re.FindStringSubmatch(cleaned)
	if len(matches) < 2 {
		return nil, fmt.Errorf("invalid DELETE syntax")
	}

	tableName := cleanIdentifier(matches[1])
	whereClause := ""
	if len(matches) > 2 {
		whereClause = strings.TrimSpace(matches[2])
	}

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

		t, err := session.BeginTx(storage.TxOptions{})
		if err != nil {
			return nil, err
		}
		tx = t
		isEphemeral = true
	}

	cursor, err := tx.Scan(tableName)
	if err != nil {
		if isEphemeral {
			_ = tx.Rollback()
		}
		return nil, err
	}
	defer cursor.Close()

	toDelete := make([]storage.Value, 0)
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			break
		}

		if whereClause != "" && !evaluateWhereCondition(rec, whereClause) {
			continue
		}

		pkVal, hasPK := rec["id"]
		if !hasPK {
			for _, v := range rec {
				pkVal = v
				hasPK = true
				break
			}
		}

		if hasPK {
			toDelete = append(toDelete, pkVal)
		}
	}

	deletedCount := int64(0)
	for _, pk := range toDelete {
		if err := tx.Delete(tableName, pk); err != nil {
			if isEphemeral {
				_ = tx.Rollback()
			}
			return nil, fmt.Errorf("delete error: %w", err)
		}
		deletedCount++
	}

	if isEphemeral {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit error: %w", err)
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "DELETE",
		RowsAffected: deletedCount,
		DurationUS:   time.Since(start).Microseconds(),
		Messages:     []string{fmt.Sprintf("%d rows deleted from %q", deletedCount, tableName)},
	}, nil
}

func (s *Server) execCreateProcedure(query string, start time.Time) (*QueryResult, error) {
	plan, err := proc.Parse(query)
	if err != nil {
		return nil, fmt.Errorf("parse procedure error: %w", err)
	}

	prog, err := proc.CompileProcedure(plan)
	if err != nil {
		return nil, fmt.Errorf("bytecode compilation error: %w", err)
	}

	params := make([]catalog.ParamDef, len(plan.Params))
	for i, p := range plan.Params {
		params[i] = catalog.ParamDef{Name: p.Name, Type: p.Type}
	}

	procMeta := &catalog.Procedure{
		Name:      plan.Name,
		Body:      query,
		JSCode:    prog.Disassemble(),
		Params:    params,
		CreatedAt: time.Now(),
	}

	if err := s.catalog.RegisterProcedure(procMeta); err != nil {
		return nil, fmt.Errorf("catalog error: %w", err)
	}

	s.executor.Cache().PutByName(plan.Name, prog)

	return &QueryResult{
		Success:    true,
		Command:    "CREATE PROCEDURE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Procedure %q registered and bytecode compiled successfully", plan.Name)},
	}, nil
}

func (s *Server) execCreateFunction(query string, start time.Time) (*QueryResult, error) {
	// 1. Check if Table-Valued Function (TVF): CREATE FUNCTION ... RETURNS TABLE AS RETURN ( ... )
	tvfRe := regexp.MustCompile(`(?is)^CREATE\s+FUNCTION\s+([^\s(]+)\s*\((.*?)\)\s*RETURNS\s+TABLE\s+AS\s+RETURN\s*\(\s*(SELECT\s+.*)\s*\)\s*;?$`)
	if m := tvfRe.FindStringSubmatch(strings.TrimSpace(query)); len(m) == 4 {
		fnName := cleanIdentifier(m[1])
		rawParams := m[2]
		innerQuery := strings.TrimSpace(m[3])

		var params []catalog.ParamDef
		if strings.TrimSpace(rawParams) != "" {
			for _, p := range strings.Split(rawParams, ",") {
				pParts := strings.Fields(strings.TrimSpace(p))
				if len(pParts) >= 2 {
					pName := pParts[0]
					pType := parseSQLDataType(pParts[1])
					params = append(params, catalog.ParamDef{Name: pName, Type: pType})
				}
			}
		}

		fnMeta := &catalog.Function{
			Name:          fnName,
			Params:        params,
			IsTableValued: true,
			Body:          innerQuery,
			CreatedAt:     time.Now(),
		}

		if err := s.catalog.RegisterFunction(fnMeta); err != nil {
			return nil, fmt.Errorf("catalog register TVF error: %w", err)
		}

		return &QueryResult{
			Success:    true,
			Command:    "CREATE FUNCTION",
			DurationUS: time.Since(start).Microseconds(),
			Messages:   []string{fmt.Sprintf("Table-valued function %q registered successfully", fnName)},
		}, nil
	}

	fnStmt, err := proc.ParseFunction(query)
	if err != nil {
		return nil, fmt.Errorf("parse function error: %w", err)
	}

	prog, err := proc.CompileFunction(fnStmt)
	if err != nil {
		return nil, fmt.Errorf("compile function error: %w", err)
	}

	params := make([]catalog.ParamDef, len(fnStmt.Params))
	for i, p := range fnStmt.Params {
		params[i] = catalog.ParamDef{Name: p.Name, Type: p.Type}
	}

	fnMeta := &catalog.Function{
		Name:       fnStmt.Name,
		ReturnType: fnStmt.ReturnType,
		Body:       query,
		JSCode:     prog.Disassemble(),
		Params:     params,
		CreatedAt:  time.Now(),
	}

	if err := s.catalog.RegisterFunction(fnMeta); err != nil {
		return nil, fmt.Errorf("catalog register function error: %w", err)
	}

	s.executor.RegisterUDF(fnStmt.Name, prog)

	return &QueryResult{
		Success:    true,
		Command:    "CREATE FUNCTION",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("User-defined function %q registered successfully", fnStmt.Name)},
	}, nil
}

func (s *Server) execCreateTrigger(query string, start time.Time) (*QueryResult, error) {
	trgStmt, err := proc.ParseTrigger(query)
	if err != nil {
		return nil, fmt.Errorf("parse trigger error: %w", err)
	}

	prog, err := proc.CompileTrigger(trgStmt)
	if err != nil {
		return nil, fmt.Errorf("compile trigger error: %w", err)
	}

	trgMeta := &catalog.Trigger{
		Name:      trgStmt.Name,
		Table:     trgStmt.Table,
		Timing:    trgStmt.Timing,
		Events:    trgStmt.Events,
		Body:      query,
		JSCode:    prog.Disassemble(),
		CreatedAt: time.Now(),
	}

	if err := s.catalog.RegisterTrigger(trgMeta); err != nil {
		return nil, fmt.Errorf("catalog register trigger error: %w", err)
	}

	for _, ev := range trgStmt.Events {
		s.executor.RegisterTrigger(trgStmt.Name, trgStmt.Table, ev, prog)
	}

	return &QueryResult{
		Success:    true,
		Command:    "CREATE TRIGGER",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Trigger %q registered on %q for %s", trgStmt.Name, trgStmt.Table, strings.Join(trgStmt.Events, ", "))},
	}, nil
}

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

// --- Virtual System Catalog Views (sys.* and information_schema.*) ---

func (s *Server) isVirtualCatalogTable(tableName string) bool {
	lower := strings.ToLower(cleanIdentifier(tableName))
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
		"information_schema.tables",
		"information_schema.columns",
		"information_schema.schemata",
		"information_schema.routines",
		"pg_catalog.pg_database", "pg_database",
		"pg_catalog.pg_namespace", "pg_namespace",
		"pg_catalog.pg_tables", "pg_tables",
		"pg_catalog.pg_class", "pg_class",
		"pg_catalog.pg_type", "pg_type",
		"pg_catalog.pg_attribute", "pg_attribute",
		"pg_catalog.pg_roles", "pg_roles", "pg_user", "pg_catalog.pg_user",
		"pg_catalog.pg_am", "pg_am",
		"pg_catalog.pg_index", "pg_index",
		"pg_catalog.pg_settings", "pg_settings",
		"pg_catalog.pg_description", "pg_description":
		return true
	default:
		return false
	}
}

func (s *Server) execVirtualCatalogSelect(tableName, projStr, whereClause, orderByClause string, limitCount int, start time.Time) (*QueryResult, error) {
	norm := strings.ToLower(cleanIdentifier(tableName))
	norm = strings.TrimPrefix(norm, "dbo.")

	var columns []string
	var allRows []map[string]any

	tables, _ := s.engine.ListTables()

	switch norm {
	case "sys.tables", "systables", "sys_tables", "information_schema.tables":
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

	case "sys.columns", "syscolumns", "sys_columns", "information_schema.columns":
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

	case "sys.databases", "sysdatabases", "sys_databases", "information_schema.schemata":
		columns = []string{"name", "schema_name", "database_id", "create_date", "state_desc"}
		allRows = append(allRows, map[string]any{
			"name":        "sqlion",
			"schema_name": "sqlion",
			"database_id": 1,
			"create_date": "2026-01-01T00:00:00Z",
			"state_desc":  "ONLINE",
		})

	case "sys.procedures", "sysprocedures", "sys_procedures", "information_schema.routines":
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

	case "pg_catalog.pg_database", "pg_database":
		columns = []string{"datname", "oid", "encoding", "datcollate", "datctype", "datistemplate", "datallowconn"}
		allRows = append(allRows, map[string]any{
			"datname":       "sqlion",
			"oid":           1,
			"encoding":      6,
			"datcollate":    "en_US.utf8",
			"datctype":      "en_US.utf8",
			"datistemplate": false,
			"datallowconn":  true,
		})

	case "pg_catalog.pg_namespace", "pg_namespace":
		columns = []string{"nspname", "oid", "nspowner"}
		allRows = append(allRows,
			map[string]any{"nspname": "public", "oid": 2200, "nspowner": 10},
			map[string]any{"nspname": "dbo", "oid": 1, "nspowner": 10},
			map[string]any{"nspname": "pg_catalog", "oid": 11, "nspowner": 10},
			map[string]any{"nspname": "information_schema", "oid": 12, "nspowner": 10},
		)

	case "pg_catalog.pg_tables", "pg_tables":
		columns = []string{"schemaname", "tablename", "tableowner", "tablespace", "hasindexes", "hasrules", "hastriggers"}
		for _, t := range tables {
			allRows = append(allRows, map[string]any{
				"schemaname":  "dbo",
				"tablename":   t.Name,
				"tableowner":  "postgres",
				"tablespace":  "",
				"hasindexes":  len(t.Indexes) > 0,
				"hasrules":    false,
				"hastriggers": false,
			})
		}

	case "pg_catalog.pg_class", "pg_class":
		columns = []string{"relname", "oid", "relnamespace", "reltype", "relowner", "relam", "relkind", "relispartition"}
		for i, t := range tables {
			allRows = append(allRows, map[string]any{
				"relname":        t.Name,
				"oid":            1000 + i,
				"relnamespace":   1,
				"reltype":        0,
				"relowner":       10,
				"relam":          0,
				"relkind":        "r",
				"relispartition": false,
			})
		}

	case "pg_catalog.pg_type", "pg_type":
		columns = []string{"typname", "oid", "typnamespace", "typlen", "typtype"}
		typesList := []struct {
			name string
			oid  int
			len  int
		}{
			{"int4", 23, 4},
			{"int8", 20, 8},
			{"varchar", 1043, -1},
			{"text", 25, -1},
			{"bool", 16, 1},
			{"float8", 701, 8},
			{"json", 114, -1},
			{"jsonb", 3802, -1},
			{"timestamp", 1114, 8},
		}
		for _, ty := range typesList {
			allRows = append(allRows, map[string]any{
				"typname":      ty.name,
				"oid":          ty.oid,
				"typnamespace": 11,
				"typlen":       ty.len,
				"typtype":      "b",
			})
		}

	case "pg_catalog.pg_attribute", "pg_attribute":
		columns = []string{"attrelid", "attname", "atttypid", "attnum", "attnotnull", "attisdropped"}
		for tIdx, t := range tables {
			for cIdx, c := range t.Columns {
				allRows = append(allRows, map[string]any{
					"attrelid":     1000 + tIdx,
					"attname":      c.Name,
					"atttypid":     23,
					"attnum":       cIdx + 1,
					"attnotnull":   !c.Nullable,
					"attisdropped": false,
				})
			}
		}

	case "pg_catalog.pg_roles", "pg_roles", "pg_catalog.pg_user", "pg_user":
		columns = []string{"rolname", "oid", "rolsuper", "rolinherit", "usename"}
		allRows = append(allRows, map[string]any{
			"rolname":    "postgres",
			"oid":        10,
			"rolsuper":   true,
			"rolinherit": true,
			"usename":    "postgres",
		})

	case "pg_catalog.pg_am", "pg_am":
		columns = []string{"amname", "oid", "amtype"}
		allRows = append(allRows,
			map[string]any{"amname": "btree", "oid": 403, "amtype": "i"},
			map[string]any{"amname": "hash", "oid": 405, "amtype": "i"},
		)

	case "pg_catalog.pg_settings", "pg_settings":
		columns = []string{"name", "setting", "category", "short_desc"}
		allRows = append(allRows,
			map[string]any{"name": "server_version", "setting": "15.0 (SQLion)", "category": "Preset Options", "short_desc": "Shows the server version."},
			map[string]any{"name": "client_encoding", "setting": "UTF8", "category": "Client Connection Defaults", "short_desc": "Sets the client's character set encoding."},
		)

	case "pg_catalog.pg_index", "pg_index", "pg_catalog.pg_description", "pg_description":
		columns = []string{"objoid", "classoid", "objsubid", "description"}

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

	case "sys.types", "systypes", "sys_types":
		columns = []string{"name", "system_type_id", "user_type_id", "schema_id", "max_length", "precision", "scale", "is_nullable"}
		allRows = append(allRows,
			map[string]any{"name": "int", "system_type_id": 56, "user_type_id": 56, "schema_id": 4, "max_length": 4, "precision": 10, "scale": 0, "is_nullable": true},
			map[string]any{"name": "varchar", "system_type_id": 167, "user_type_id": 167, "schema_id": 4, "max_length": 8000, "precision": 0, "scale": 0, "is_nullable": true},
			map[string]any{"name": "float", "system_type_id": 62, "user_type_id": 62, "schema_id": 4, "max_length": 8, "precision": 53, "scale": 0, "is_nullable": true},
			map[string]any{"name": "bit", "system_type_id": 104, "user_type_id": 104, "schema_id": 4, "max_length": 1, "precision": 1, "scale": 0, "is_nullable": true},
			map[string]any{"name": "datetime", "system_type_id": 61, "user_type_id": 61, "schema_id": 4, "max_length": 8, "precision": 23, "scale": 3, "is_nullable": true},
		)

	default:
		return nil, fmt.Errorf("unsupported virtual system table: %s", tableName)
	}

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
		for _, c := range strings.Split(projStr, ",") {
			cTrimmed := strings.TrimSpace(c)
			if cTrimmed == "" {
				continue
			}
			alias := ""
			colExpr := cTrimmed
			// Check for AS
			asRe := regexp.MustCompile(`(?is)^(.*?)\s+AS\s+([a-zA-Z0-9_#$]+|"[^"]+")$`)
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

// Helpers

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

func parseSQLDataType(s string) storage.DataType {
	switch strings.ToUpper(s) {
	case "INT", "INTEGER", "BIGINT", "SMALLINT", "TINYINT", "IDENTITY":
		return storage.TypeInt
	case "FLOAT", "DOUBLE", "REAL":
		return storage.TypeFloat
	case "NUMERIC", "DECIMAL", "MONEY":
		return storage.TypeDecimal
	case "BOOL", "BOOLEAN", "BIT":
		return storage.TypeBool
	case "DATETIME", "TIMESTAMP", "DATE", "TIME":
		return storage.TypeDateTime
	case "JSON", "JSONB":
		return storage.TypeJSON
	default:
		return storage.TypeVarChar
	}
}

func splitColumnDefs(s string) []string {
	var defs []string
	var cur strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '(':
			depth++
			cur.WriteByte(c)
		case ')':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
		case ',':
			if depth == 0 {
				defs = append(defs, strings.TrimSpace(cur.String()))
				cur.Reset()
			} else {
				cur.WriteByte(c)
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		defs = append(defs, strings.TrimSpace(cur.String()))
	}
	return defs
}

func parseForeignKeyClause(def string, defaultChildCol string, defaultName string) (*storage.ForeignKeySchema, bool) {
	defTrimmed := strings.TrimSpace(def)
	upper := strings.ToUpper(defTrimmed)
	if !strings.Contains(upper, "REFERENCES") {
		return nil, false
	}

	fk := storage.ForeignKeySchema{
		Name:     defaultName,
		OnDelete: storage.ActionNoAction,
		OnUpdate: storage.ActionNoAction,
	}

	reOnDelete := regexp.MustCompile(`(?i)\bON\s+DELETE\s+(CASCADE|SET\s+NULL|SET\s+DEFAULT|RESTRICT|NO\s+ACTION)`)
	if m := reOnDelete.FindStringSubmatch(defTrimmed); len(m) > 1 {
		fk.OnDelete = storage.ParseForeignKeyAction(m[1])
	}

	reOnUpdate := regexp.MustCompile(`(?i)\bON\s+UPDATE\s+(CASCADE|SET\s+NULL|SET\s+DEFAULT|RESTRICT|NO\s+ACTION)`)
	if m := reOnUpdate.FindStringSubmatch(defTrimmed); len(m) > 1 {
		fk.OnUpdate = storage.ParseForeignKeyAction(m[1])
	}

	reTableFK := regexp.MustCompile(`(?i)(?:CONSTRAINT\s+([^\s(]+)\s+)?FOREIGN\s+KEY\s*\(([^)]+)\)\s*REFERENCES\s+([^\s(]+)(?:\s*\(([^)]+)\))?`)
	if m := reTableFK.FindStringSubmatch(defTrimmed); len(m) > 0 {
		if m[1] != "" {
			fk.Name = cleanIdentifier(m[1])
		}
		rawChildCols := strings.Split(m[2], ",")
		for _, c := range rawChildCols {
			if col := cleanIdentifier(c); col != "" {
				fk.ChildColumns = append(fk.ChildColumns, col)
			}
		}
		fk.ParentTable = cleanIdentifier(m[3])
		if len(m) > 4 && strings.TrimSpace(m[4]) != "" {
			rawParentCols := strings.Split(m[4], ",")
			for _, c := range rawParentCols {
				if col := cleanIdentifier(c); col != "" {
					fk.ParentColumns = append(fk.ParentColumns, col)
				}
			}
		}
		if fk.Name == "" && len(fk.ChildColumns) > 0 {
			fk.Name = fmt.Sprintf("fk_%s_%s", fk.ParentTable, fk.ChildColumns[0])
		}
		return &fk, true
	}

	reInlineFK := regexp.MustCompile(`(?i)\bREFERENCES\s+([^\s(]+)(?:\s*\(([^)]+)\))?`)
	if m := reInlineFK.FindStringSubmatch(defTrimmed); len(m) > 0 {
		fk.ParentTable = cleanIdentifier(m[1])
		if len(m) > 2 && strings.TrimSpace(m[2]) != "" {
			rawParentCols := strings.Split(m[2], ",")
			for _, c := range rawParentCols {
				if col := cleanIdentifier(c); col != "" {
					fk.ParentColumns = append(fk.ParentColumns, col)
				}
			}
		}
		if defaultChildCol != "" {
			fk.ChildColumns = []string{defaultChildCol}
		}
		if fk.Name == "" && defaultChildCol != "" {
			fk.Name = fmt.Sprintf("fk_%s_%s", fk.ParentTable, defaultChildCol)
		}
		return &fk, true
	}

	return nil, false
}

func parseValueTuples(s string) []string {
	var tuples []string
	var cur strings.Builder
	inTuple := false
	inString := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' && (i == 0 || s[i-1] != '\\') {
			inString = !inString
			cur.WriteByte(c)
			continue
		}
		if inString {
			cur.WriteByte(c)
			continue
		}

		if c == '(' {
			inTuple = true
			cur.Reset()
			continue
		}
		if c == ')' {
			if inTuple {
				tuples = append(tuples, cur.String())
				inTuple = false
				cur.Reset()
			}
			continue
		}
		if inTuple {
			cur.WriteByte(c)
		}
	}
	return tuples
}

func splitValues(s string) []string {
	var vals []string
	var cur strings.Builder
	inString := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' && (i == 0 || s[i-1] != '\\') {
			inString = !inString
			cur.WriteByte(c)
			continue
		}
		if !inString && c == ',' {
			vals = append(vals, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if cur.Len() > 0 {
		vals = append(vals, strings.TrimSpace(cur.String()))
	}
	return vals
}

func parseLiteralValue(s string, targetType storage.DataType) storage.Value {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "NULL") {
		return storage.NullValue(targetType)
	}
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
		strVal := s[1 : len(s)-1]
		strVal = strings.ReplaceAll(strVal, "''", "'")
		if targetType == storage.TypeJSON {
			return storage.JSONValue(strVal)
		}
		return storage.StringValue(strVal)
	}

	switch targetType {
	case storage.TypeJSON:
		return storage.JSONValue(s)
	case storage.TypeInt:
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return storage.IntValue(i)
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.IntValue(int64(f))
		}
	case storage.TypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.FloatValue(f)
		}
	case storage.TypeDecimal:
		return storage.DecimalValue(s)
	case storage.TypeBool:
		if strings.EqualFold(s, "TRUE") || s == "1" {
			return storage.BoolValue(true)
		}
		if strings.EqualFold(s, "FALSE") || s == "0" {
			return storage.BoolValue(false)
		}
	}
	return storage.StringValue(s)
}

func formatStorageValue(v storage.Value) any {
	if v.IsNull {
		return nil
	}
	switch v.Type {
	case storage.TypeInt:
		return v.IVal
	case storage.TypeVarChar, storage.TypeJSON:
		return v.SVal
	case storage.TypeFloat:
		return v.FVal
	case storage.TypeDecimal:
		return v.Decimal().String()
	case storage.TypeBool:
		return v.BVal
	case storage.TypeDateTime:
		return v.TVal
	default:
		return v.String()
	}
}

func splitLogicalConditions(whereStr string, op string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)
	parenDepth := 0
	opLen := len(op)
	upper := strings.ToUpper(whereStr)

	for i := 0; i < len(whereStr); i++ {
		ch := whereStr[i]
		if inQuote {
			if ch == quoteChar && (i == 0 || whereStr[i-1] != '\\') {
				inQuote = false
			}
			cur.WriteByte(ch)
		} else {
			if ch == '\'' || ch == '"' {
				inQuote = true
				quoteChar = ch
				cur.WriteByte(ch)
			} else if ch == '(' {
				parenDepth++
				cur.WriteByte(ch)
			} else if ch == ')' {
				if parenDepth > 0 {
					parenDepth--
				}
				cur.WriteByte(ch)
			} else if parenDepth == 0 && i+opLen <= len(whereStr) && upper[i:i+opLen] == op {
				part := strings.TrimSpace(cur.String())
				if part != "" {
					parts = append(parts, part)
				}
				cur.Reset()
				i += opLen - 1
			} else {
				cur.WriteByte(ch)
			}
		}
	}
	if cur.Len() > 0 {
		part := strings.TrimSpace(cur.String())
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func parseValueToType(s string, t storage.DataType) storage.Value {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "NULL") {
		return storage.NullValue(t)
	}
	s = strings.Trim(s, "'\"")
	switch t {
	case storage.TypeInt:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return storage.IntValue(n)
		}
	case storage.TypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.FloatValue(f)
		}
	case storage.TypeDecimal:
		return storage.DecimalValue(s)
	case storage.TypeBool:
		if b, err := strconv.ParseBool(s); err == nil {
			return storage.BoolValue(b)
		}
	case storage.TypeDateTime:
		if tVal, err := time.Parse(time.RFC3339, s); err == nil {
			return storage.DateTimeValue(tVal)
		}
	}
	return storage.StringValue(s)
}

func evaluateWhereCondition(rec storage.Record, whereStr string) bool {
	whereStr = strings.TrimSpace(whereStr)
	if whereStr == "" || whereStr == "1=1" || whereStr == "1 = 1" || strings.EqualFold(whereStr, "NOT 1=0") || strings.EqualFold(whereStr, "NOT 1 = 0") {
		return true
	}
	if whereStr == "1=0" || whereStr == "1 = 0" || strings.EqualFold(whereStr, "NOT 1=1") || strings.EqualFold(whereStr, "NOT 1 = 1") {
		return false
	}

	rMap := make(map[string]any, len(rec))
	for k, v := range rec {
		rMap[k] = formatStorageValue(v)
	}
	return evaluateWhereConditionMap(rMap, whereStr)
}

func evaluateWhereConditionMap(rec map[string]any, whereStr string) bool {
	whereStr = strings.TrimSpace(whereStr)
	if whereStr == "" || whereStr == "1=1" || whereStr == "1 = 1" || strings.EqualFold(whereStr, "NOT 1=0") || strings.EqualFold(whereStr, "NOT 1 = 0") {
		return true
	}
	if whereStr == "1=0" || whereStr == "1 = 0" || strings.EqualFold(whereStr, "NOT 1=1") || strings.EqualFold(whereStr, "NOT 1 = 1") {
		return false
	}

	p := proc.NewParser(proc.NewLexer(whereStr))
	expr := p.ParseExpr()
	if expr != nil && len(p.Errors()) == 0 {
		return proc.EvalBoolExpr(expr, rec, nil)
	}

	orParts := splitLogicalConditions(whereStr, " OR ")
	if len(orParts) > 1 {
		for _, part := range orParts {
			if evaluateWhereConditionMap(rec, part) {
				return true
			}
		}
		return false
	}

	andParts := splitLogicalConditions(whereStr, " AND ")
	if len(andParts) > 1 {
		for _, part := range andParts {
			if !evaluateWhereConditionMap(rec, part) {
				return false
			}
		}
		return true
	}

	return evaluateSingleConditionMap(rec, whereStr)
}

func evaluateSingleConditionMap(rec map[string]any, whereStr string) bool {
	whereStr = strings.TrimSpace(whereStr)
	if strings.HasPrefix(whereStr, "(") && strings.HasSuffix(whereStr, ")") {
		inner := strings.TrimSpace(whereStr[1 : len(whereStr)-1])
		if !strings.Contains(inner, "(") && !strings.Contains(inner, ")") {
			whereStr = inner
		}
	}

	inRe := regexp.MustCompile(`(?is)^\s*([a-zA-Z0-9_\.]+)\s+(NOT\s+)?IN\s*\((.*?)\)\s*$`)
	if m := inRe.FindStringSubmatch(whereStr); len(m) == 4 {
		colName := cleanIdentifier(m[1])
		isNot := strings.TrimSpace(m[2]) != ""
		valList := m[3]
		val, ok := rec[colName]
		if !ok {
			if idx := strings.Index(colName, "."); idx != -1 {
				val, ok = rec[colName[idx+1:]]
			}
		}
		if !ok || val == nil {
			return false
		}
		strVal := fmt.Sprintf("%v", val)
		found := false
		for _, rawItem := range strings.Split(valList, ",") {
			item := strings.Trim(strings.TrimSpace(rawItem), "'\"")
			if strings.EqualFold(strVal, item) {
				found = true
				break
			}
		}
		if isNot {
			return !found
		}
		return found
	}

	ops := []string{"!=", "<>", "<=", ">=", "=", "<", ">", " LIKE "}
	for _, op := range ops {
		idx := strings.Index(strings.ToUpper(whereStr), op)
		if idx != -1 {
			leftCol := cleanIdentifier(whereStr[:idx])
			rightVal := strings.TrimSpace(whereStr[idx+len(op):])
			rightVal = strings.Trim(rightVal, "';\"")

			if leftCol == "1" && rightVal == "1" && op == "=" {
				return true
			}
			if leftCol == "1" && rightVal == "0" && op == "=" {
				return false
			}

			val, ok := rec[leftCol]
			if !ok {
				if dotIdx := strings.Index(leftCol, "."); dotIdx != -1 {
					val, ok = rec[leftCol[dotIdx+1:]]
				}
			}
			if !ok {
				return false
			}

			strVal := fmt.Sprintf("%v", val)
			switch strings.TrimSpace(op) {
			case "=":
				return strings.EqualFold(strVal, rightVal)
			case "!=", "<>":
				return !strings.EqualFold(strVal, rightVal)
			case ">":
				n1, err1 := strconv.ParseFloat(strVal, 64)
				n2, err2 := strconv.ParseFloat(rightVal, 64)
				if err1 == nil && err2 == nil {
					return n1 > n2
				}
				return strVal > rightVal
			case ">=":
				n1, err1 := strconv.ParseFloat(strVal, 64)
				n2, err2 := strconv.ParseFloat(rightVal, 64)
				if err1 == nil && err2 == nil {
					return n1 >= n2
				}
				return strVal >= rightVal
			case "<":
				n1, err1 := strconv.ParseFloat(strVal, 64)
				n2, err2 := strconv.ParseFloat(rightVal, 64)
				if err1 == nil && err2 == nil {
					return n1 < n2
				}
				return strVal < rightVal
			case "<=":
				n1, err1 := strconv.ParseFloat(strVal, 64)
				n2, err2 := strconv.ParseFloat(rightVal, 64)
				if err1 == nil && err2 == nil {
					return n1 <= n2
				}
				return strVal <= rightVal
			case "LIKE":
				pat := strings.ReplaceAll(regexp.QuoteMeta(rightVal), "%", ".*")
				pat = strings.ReplaceAll(pat, "_", ".")
				matched, _ := regexp.MatchString("(?i)^"+pat+"$", strVal)
				return matched
			}
		}
	}
	return true
}

