package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

// execPrepare parses and compiles PREPARE statement into a cached session PreparedStatement.
func (s *Coordinator) execPrepare(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "PREPARE ") {
		return nil, fmt.Errorf("invalid PREPARE syntax")
	}

	raw := strings.TrimSpace(trimmed[len("PREPARE "):])

	var stmtName string
	var queryPart string

	// Find AS or FROM outside quotes/parentheses
	asIdx := findKeywordOutsideQuotes(raw, "AS")
	fromIdx := findKeywordOutsideQuotes(raw, "FROM")

	if asIdx != -1 && (fromIdx == -1 || asIdx < fromIdx) {
		stmtHeader := strings.TrimSpace(raw[:asIdx])
		queryPart = strings.TrimSpace(raw[asIdx+2:])
		// stmtHeader might be "plan_name" or "plan_name (int, text)"
		if idx := strings.Index(stmtHeader, "("); idx != -1 {
			stmtName = strings.TrimSpace(stmtHeader[:idx])
		} else {
			fields := strings.Fields(stmtHeader)
			if len(fields) > 0 {
				stmtName = fields[0]
			}
		}
	} else if fromIdx != -1 {
		stmtName = strings.TrimSpace(raw[:fromIdx])
		queryPart = strings.TrimSpace(raw[fromIdx+4:])
		// If queryPart is a string literal, unwrap it
		if strings.HasPrefix(queryPart, "'") || strings.HasPrefix(queryPart, "N'") || strings.HasPrefix(queryPart, "n'") || strings.HasPrefix(queryPart, "\"") {
			queryPart = unwrapSQLString(queryPart)
		} else if strings.HasPrefix(queryPart, "@") {
			// Session variable
			varName := strings.TrimRight(queryPart, ";")
			if cs == nil {
				return nil, fmt.Errorf("session variable %s requires an active session", varName)
			}
			cs.Mu.Lock()
			v, ok := cs.Vars[varName]
			if !ok {
				v, ok = cs.Vars["@"+strings.TrimPrefix(varName, "@")]
			}
			cs.Mu.Unlock()
			if !ok || v == nil {
				return nil, fmt.Errorf("session variable %s not found", varName)
			}
			strVal, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("session variable %s must contain a string query", varName)
			}
			queryPart = strVal
		}
	} else {
		return nil, fmt.Errorf("syntax error: PREPARE requires 'AS <statement>' or 'FROM <string/var>'")
	}

	stmtName = cleanIdentifier(stmtName)
	if stmtName == "" {
		return nil, fmt.Errorf("syntax error: prepared statement name required")
	}

	queryPart = strings.TrimSpace(strings.TrimRight(queryPart, ";"))
	if queryPart == "" {
		return nil, fmt.Errorf("syntax error: prepared statement query cannot be empty")
	}

	normalized, paramNames, err := normalizePreparedStatementQuery(queryPart)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize query: %w", err)
	}

	var prog *proc.Program
	if s.executor != nil && s.executor.Cache() != nil {
		prog, err = s.executor.Cache().GetOrCompile(normalized)
	} else {
		prog, err = proc.Compile(normalized)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to prepare statement %q: %w", stmtName, err)
	}

	if cs != nil {
		cs.Mu.Lock()
		if cs.PreparedStmts == nil {
			cs.PreparedStmts = make(map[string]*PreparedStatement)
		}
		cs.PreparedStmts[strings.ToLower(stmtName)] = &PreparedStatement{
			Name:       stmtName,
			Query:      normalized,
			Program:    prog,
			ParamNames: paramNames,
		}
		cs.Mu.Unlock()
	}

	return &QueryResult{
		Success:    true,
		Command:    "PREPARE",
		Messages:   []string{fmt.Sprintf("Statement %q prepared successfully.", stmtName)},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}

// execDeallocate drops one or all prepared statements from the active session.
func (s *Coordinator) execDeallocate(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)

	target := ""
	if strings.HasPrefix(upper, "DEALLOCATE PREPARE ") {
		target = strings.TrimSpace(trimmed[len("DEALLOCATE PREPARE "):])
	} else if strings.HasPrefix(upper, "DEALLOCATE ") {
		target = strings.TrimSpace(trimmed[len("DEALLOCATE "):])
	} else if strings.HasPrefix(upper, "DROP PREPARE ") {
		target = strings.TrimSpace(trimmed[len("DROP PREPARE "):])
	} else {
		return nil, fmt.Errorf("invalid DEALLOCATE syntax")
	}

	target = strings.TrimRight(target, ";")
	target = strings.TrimSpace(target)

	if strings.EqualFold(target, "ALL") {
		if cs != nil {
			cs.Mu.Lock()
			cs.PreparedStmts = make(map[string]*PreparedStatement)
			cs.Mu.Unlock()
		}
		return &QueryResult{
			Success:    true,
			Command:    "DEALLOCATE",
			Messages:   []string{"All prepared statements deallocated."},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	stmtName := cleanIdentifier(target)
	if stmtName == "" {
		return nil, fmt.Errorf("syntax error: statement name required for DEALLOCATE")
	}

	if cs != nil {
		cs.Mu.Lock()
		delete(cs.PreparedStmts, strings.ToLower(stmtName))
		cs.Mu.Unlock()
	}

	return &QueryResult{
		Success:    true,
		Command:    "DEALLOCATE",
		Messages:   []string{fmt.Sprintf("Statement %q deallocated.", stmtName)},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}

// execPreparedStatement binds arguments and executes a cached PreparedStatement via VDBE bytecode VM.
func (s *Coordinator) execPreparedStatement(prep *PreparedStatement, rawArgs []string, start time.Time, cs *ClientSession, ctx context.Context) (*QueryResult, error) {
	if prep == nil || prep.Program == nil {
		return nil, fmt.Errorf("invalid prepared statement")
	}

	cmdType := strings.ToUpper(prep.Program.Type)
	if cmdType == "INSERT" || cmdType == "UPDATE" || cmdType == "DELETE" || cmdType == "MERGE" || cmdType == "UPSERT" {
		if err := s.checkWriteAllowed(); err != nil {
			return nil, err
		}
	}

	// 1. Process USING syntax if present
	args := make([]string, 0, len(rawArgs))
	for _, a := range rawArgs {
		trimmedA := strings.TrimSpace(a)
		if strings.HasPrefix(strings.ToUpper(trimmedA), "USING ") {
			trimmedA = strings.TrimSpace(trimmedA[len("USING "):])
		}
		if trimmedA != "" {
			args = append(args, trimmedA)
		}
	}

	// 2. Build parameter map
	paramMap := make(map[string]any, len(args)*2)
	for i, arg := range args {
		var paramName string
		var val any

		if strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			paramName = strings.TrimSpace(parts[0])
			valStr := strings.TrimSpace(parts[1])
			val = parseArgValue(valStr, cs)
		} else {
			if i < len(prep.ParamNames) {
				paramName = prep.ParamNames[i]
			} else {
				paramName = fmt.Sprintf("@p%d", i+1)
			}
			val = parseArgValue(arg, cs)
		}

		cleanK := strings.TrimPrefix(paramName, "@")
		paramMap[paramName] = val
		paramMap[cleanK] = val
		paramMap["@"+cleanK] = val
		paramMap[strings.ToLower(cleanK)] = val
		paramMap["@"+strings.ToLower(cleanK)] = val
	}

	// 3. Acquire transaction
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

	// 4. Execute bytecode program
	execRes, err := s.executor.ExecuteInTxContext(ctx, prep.Program, paramMap, tx, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
			return nil, fmt.Errorf("canceling statement due to user request")
		}
		return nil, err
	}

	if isEphemeral {
		if err := tx.Commit(); err != nil && !errors.Is(err, storage.ErrTxClosed) {
			return nil, fmt.Errorf("failed to commit transaction: %w", err)
		}
		tx = nil
	}

	// 5. Update session variables
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

	cmd := prep.Program.Type
	if cmd == "" || cmd == "QUERY" || cmd == "INTERACTIVE_QUERY" {
		cmd = "SELECT"
	}
	if len(execRes.Rows) > 0 {
		cmd = "SELECT"
	}

	return &QueryResult{
		Success:      true,
		Command:      cmd,
		Columns:      execRes.Columns,
		Rows:         execRes.Rows,
		RowsAffected: execRes.RowsAffected,
		Messages:     execRes.Logs,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// normalizePreparedStatementQuery rewrites $1, $2 and ? placeholders into @p1, @p2, preserving string literals and comments.
func normalizePreparedStatementQuery(raw string) (string, []string, error) {
	var b strings.Builder
	var paramNames []string
	seenParams := make(map[string]bool)

	inSingleQuote := false
	inDoubleQuote := false
	inLineComment := false
	inBlockComment := false

	runes := []rune(raw)
	n := len(runes)
	pCount := 0

	for i := 0; i < n; {
		r := runes[i]

		// 1. Line comment: --
		if inLineComment {
			b.WriteRune(r)
			if r == '\n' {
				inLineComment = false
			}
			i++
			continue
		}

		// 2. Block comment: /* ... */
		if inBlockComment {
			b.WriteRune(r)
			if r == '*' && i+1 < n && runes[i+1] == '/' {
				b.WriteRune('/')
				inBlockComment = false
				i += 2
				continue
			}
			i++
			continue
		}

		// Check starting comments if outside quotes
		if !inSingleQuote && !inDoubleQuote {
			if r == '-' && i+1 < n && runes[i+1] == '-' {
				inLineComment = true
				b.WriteString("--")
				i += 2
				continue
			}
			if r == '/' && i+1 < n && runes[i+1] == '*' {
				inBlockComment = true
				b.WriteString("/*")
				i += 2
				continue
			}
		}

		// 3. String literals: '...'
		if inSingleQuote {
			b.WriteRune(r)
			if r == '\'' {
				// Escape ''
				if i+1 < n && runes[i+1] == '\'' {
					b.WriteRune('\'')
					i += 2
					continue
				}
				inSingleQuote = false
			}
			i++
			continue
		}

		// 4. Double-quoted identifiers: "..."
		if inDoubleQuote {
			b.WriteRune(r)
			if r == '"' {
				if i+1 < n && runes[i+1] == '"' {
					b.WriteRune('"')
					i += 2
					continue
				}
				inDoubleQuote = false
			}
			i++
			continue
		}

		// Check starting quotes
		if r == '\'' {
			inSingleQuote = true
			b.WriteRune(r)
			i++
			continue
		}
		if r == '"' {
			inDoubleQuote = true
			b.WriteRune(r)
			i++
			continue
		}

		// 5. Placeholders outside quotes & comments:
		// A. Positional '?'
		if r == '?' {
			pCount++
			pName := fmt.Sprintf("@p%d", pCount)
			b.WriteString(pName)
			if !seenParams[pName] {
				seenParams[pName] = true
				paramNames = append(paramNames, pName)
			}
			i++
			continue
		}

		// B. Positional '$' followed by digits (PostgreSQL style: $1, $2, ...)
		if r == '$' && i+1 < n && runes[i+1] >= '0' && runes[i+1] <= '9' {
			j := i + 1
			for j < n && runes[j] >= '0' && runes[j] <= '9' {
				j++
			}
			numStr := string(runes[i+1 : j])
			pName := fmt.Sprintf("@p%s", numStr)
			b.WriteString(pName)
			if !seenParams[pName] {
				seenParams[pName] = true
				paramNames = append(paramNames, pName)
			}
			i = j
			continue
		}

		// C. Named '@' parameter (T-SQL style: @param)
		if r == '@' && i+1 < n && isIdentStart(runes[i+1]) {
			// Don't treat @@ROWCOUNT etc. as parameter placeholders
			if runes[i+1] == '@' {
				b.WriteString("@@")
				i += 2
				continue
			}
			j := i + 1
			for j < n && isIdentPart(runes[j]) {
				j++
			}
			pName := string(runes[i:j])
			b.WriteString(pName)
			lowerP := strings.ToLower(pName)
			if !seenParams[lowerP] {
				seenParams[lowerP] = true
				paramNames = append(paramNames, pName)
			}
			i = j
			continue
		}

		b.WriteRune(r)
		i++
	}

	return b.String(), paramNames, nil
}

func findKeywordOutsideQuotes(s string, kw string) int {
	kwUpper := strings.ToUpper(kw)
	inSingleQuote := false
	inDoubleQuote := false
	parenDepth := 0
	runes := []rune(s)
	n := len(runes)
	kwLen := len(kw)

	for i := 0; i < n; i++ {
		r := runes[i]
		if inSingleQuote {
			if r == '\'' {
				if i+1 < n && runes[i+1] == '\'' {
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			if r == '"' {
				if i+1 < n && runes[i+1] == '"' {
					i++
					continue
				}
				inDoubleQuote = false
			}
			continue
		}
		if r == '\'' {
			inSingleQuote = true
			continue
		}
		if r == '"' {
			inDoubleQuote = true
			continue
		}
		if r == '(' {
			parenDepth++
			continue
		}
		if r == ')' {
			if parenDepth > 0 {
				parenDepth--
			}
			continue
		}
		if parenDepth == 0 && i+kwLen <= n {
			sub := strings.ToUpper(string(runes[i : i+kwLen]))
			if sub == kwUpper {
				beforeOk := (i == 0) || runes[i-1] == ' ' || runes[i-1] == '\t' || runes[i-1] == '\n' || runes[i-1] == ')'
				afterOk := (i+kwLen == n) || runes[i+kwLen] == ' ' || runes[i+kwLen] == '\t' || runes[i+kwLen] == '\n' || runes[i+kwLen] == '(' || runes[i+kwLen] == '\'' || runes[i+kwLen] == '@' || runes[i+kwLen] == '"'
				if beforeOk && afterOk {
					return i
				}
			}
		}
	}
	return -1
}

func parseArgValue(arg string, cs *ClientSession) any {
	arg = strings.TrimSpace(arg)
	if strings.HasPrefix(arg, "@") {
		if cs != nil {
			cs.Mu.Lock()
			defer cs.Mu.Unlock()
			if v, ok := cs.Vars[arg]; ok {
				return v
			}
			if v, ok := cs.Vars["@"+strings.TrimPrefix(arg, "@")]; ok {
				return v
			}
		}
		return nil
	}
	// 1. Literal context preservation: If explicitly enclosed in quotes, it is 100% a STRING.
	// Never coerce quoted strings like '00123' or '1_1_1001' into numbers.
	if (strings.HasPrefix(arg, "'") && strings.HasSuffix(arg, "'") && len(arg) >= 2) ||
		((strings.HasPrefix(arg, "N'") || strings.HasPrefix(arg, "n'")) && strings.HasSuffix(arg, "'") && len(arg) >= 3) ||
		(strings.HasPrefix(arg, "\"") && strings.HasSuffix(arg, "\"") && len(arg) >= 2) {
		return unwrapSQLString(arg)
	}

	raw := unwrapSQLString(arg)
	if strings.EqualFold(raw, "null") {
		return nil
	}
	if strings.EqualFold(raw, "true") {
		return true
	}
	if strings.EqualFold(raw, "false") {
		return false
	}
	if num, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return num
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}
	return raw
}

func isIdentStart(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9')
}
