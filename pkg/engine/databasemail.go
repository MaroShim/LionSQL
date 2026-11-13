package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// EnsureDatabaseMailTables initializes the sysmail_mailitems and sysmail_log tables if they do not exist.
func (s *Coordinator) EnsureDatabaseMailTables() error {
	s.dbMailMu.Lock()
	defer s.dbMailMu.Unlock()

	if s.dbMailInitialized {
		return nil
	}

	mailItemsSchema := `CREATE TABLE IF NOT EXISTS sysmail_mailitems (
		mailitem_id INT PRIMARY KEY,
		profile_name VARCHAR(128),
		recipients VARCHAR,
		copy_recipients VARCHAR,
		blind_copy_recipients VARCHAR,
		from_address VARCHAR,
		reply_to VARCHAR,
		subject VARCHAR,
		body VARCHAR,
		body_format VARCHAR(20),
		importance VARCHAR(10),
		sensitivity VARCHAR(20),
		file_attachments VARCHAR,
		query VARCHAR,
		send_request_date DATETIME,
		sent_date DATETIME,
		sent_status VARCHAR(20),
		last_mod_date DATETIME
	);`

	if _, err := s.ExecuteSQL(mailItemsSchema); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return fmt.Errorf("failed to create sysmail_mailitems table: %w", err)
	}

	mailLogSchema := `CREATE TABLE IF NOT EXISTS sysmail_log (
		log_id INT PRIMARY KEY,
		mailitem_id INT,
		event_type VARCHAR(20),
		log_date DATETIME,
		description VARCHAR
	);`

	if _, err := s.ExecuteSQL(mailLogSchema); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return fmt.Errorf("failed to create sysmail_log table: %w", err)
	}

	// Initialize ID counters from maximum IDs currently stored in tables
	if res, err := s.ExecuteSQL("SELECT MAX(mailitem_id) AS max_id FROM sysmail_mailitems;"); err == nil && len(res.Rows) > 0 {
		if maxID, ok := toInt64Value(res.Rows[0]["max_id"]); ok {
			s.dbMailItemCounter.Store(maxID)
		}
	}
	if res, err := s.ExecuteSQL("SELECT MAX(log_id) AS max_id FROM sysmail_log;"); err == nil && len(res.Rows) > 0 {
		if maxID, ok := toInt64Value(res.Rows[0]["max_id"]); ok {
			s.dbMailLogCounter.Store(maxID)
		}
	}

	s.dbMailInitialized = true
	return nil
}

// execSpSendDbMail implements the Microsoft SQL Server msdb.dbo.sp_send_dbmail stored procedure.
func (s *Coordinator) execSpSendDbMail(args []string, rawArgs []string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if err := s.EnsureDatabaseMailTables(); err != nil {
		return nil, err
	}

	positionalNames := []string{
		"profile_name",
		"recipients",
		"copy_recipients",
		"blind_copy_recipients",
		"from_address",
		"reply_to",
		"subject",
		"body",
		"body_format",
		"importance",
		"sensitivity",
		"file_attachments",
		"query",
		"mailitem_id",
	}

	paramValues := make(map[string]string)
	paramPresent := make(map[string]bool)
	outputBindings := make(map[string]string) // paramName -> callerVarName (e.g. "mailitem_id" -> "@mailId")

	for i, arg := range args {
		trimmed := strings.TrimSpace(arg)
		paramName := ""
		valStr := trimmed

		if eqIdx := strings.IndexByte(trimmed, '='); eqIdx != -1 {
			paramName = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(trimmed[:eqIdx]), "@"))
			valStr = strings.TrimSpace(trimmed[eqIdx+1:])
		} else if i < len(positionalNames) {
			paramName = positionalNames[i]
		}

		valStr, isOut := stripTrailingOutputKeyword(valStr)

		if i < len(rawArgs) {
			rawArg := strings.TrimSpace(rawArgs[i])
			if eqIdx := strings.IndexByte(rawArg, '='); eqIdx != -1 {
				rawArg = strings.TrimSpace(rawArg[eqIdx+1:])
			}
			rawValExpr, rawIsOut := stripTrailingOutputKeyword(rawArg)
			if (isOut || rawIsOut) && strings.HasPrefix(rawValExpr, "@") && paramName != "" {
				outputBindings[paramName] = rawValExpr
			}
		}

		if paramName != "" {
			paramPresent[paramName] = true
			isQuoted := strings.HasPrefix(valStr, "'") || strings.HasPrefix(valStr, "N'") || strings.HasPrefix(valStr, "n'") || strings.HasPrefix(valStr, "\"")
			if !isQuoted && strings.EqualFold(valStr, "NULL") {
				paramValues[paramName] = ""
				paramPresent[paramName] = false
			} else {
				paramValues[paramName] = unwrapSQLString(valStr)
			}
		}
	}

	recipients := strings.TrimSpace(paramValues["recipients"])
	copyRecipients := strings.TrimSpace(paramValues["copy_recipients"])
	blindCopyRecipients := strings.TrimSpace(paramValues["blind_copy_recipients"])

	if recipients == "" && copyRecipients == "" && blindCopyRecipients == "" {
		return nil, fmt.Errorf("Msg 14618, Level 16, State 1, Procedure sp_send_dbmail: At least one of the following parameters must be specified: \"@recipients\", \"@copy_recipients\", or \"@blind_copy_recipients\".")
	}

	bodyFormat := "TEXT"
	if paramPresent["body_format"] && strings.TrimSpace(paramValues["body_format"]) != "" {
		bodyFormat = strings.ToUpper(strings.TrimSpace(paramValues["body_format"]))
		if bodyFormat != "TEXT" && bodyFormat != "HTML" {
			return nil, fmt.Errorf("Msg 14626, Level 16, State 1, Procedure sp_send_dbmail: The value specified for \"@body_format\" must be 'TEXT' or 'HTML'.")
		}
	}

	importance := "NORMAL"
	if paramPresent["importance"] && strings.TrimSpace(paramValues["importance"]) != "" {
		importance = strings.ToUpper(strings.TrimSpace(paramValues["importance"]))
	}

	sensitivity := "NORMAL"
	if paramPresent["sensitivity"] && strings.TrimSpace(paramValues["sensitivity"]) != "" {
		sensitivity = strings.ToUpper(strings.TrimSpace(paramValues["sensitivity"]))
	}

	mailItemID := s.dbMailItemCounter.Add(1)

	sqlQuoteParam := func(name string) string {
		if !paramPresent[name] {
			return "NULL"
		}
		val := paramValues[name]
		return "'" + strings.ReplaceAll(val, "'", "''") + "'"
	}

	insertSQL := fmt.Sprintf(`INSERT INTO sysmail_mailitems (
		mailitem_id, profile_name, recipients, copy_recipients, blind_copy_recipients,
		from_address, reply_to, subject, body, body_format,
		importance, sensitivity, file_attachments, query,
		send_request_date, sent_status, last_mod_date
	) VALUES (%d, %s, %s, %s, %s, %s, %s, %s, %s, '%s', '%s', '%s', %s, %s, CURRENT_TIMESTAMP, 'unsent', CURRENT_TIMESTAMP);`,
		mailItemID,
		sqlQuoteParam("profile_name"),
		sqlQuoteParam("recipients"),
		sqlQuoteParam("copy_recipients"),
		sqlQuoteParam("blind_copy_recipients"),
		sqlQuoteParam("from_address"),
		sqlQuoteParam("reply_to"),
		sqlQuoteParam("subject"),
		sqlQuoteParam("body"),
		bodyFormat,
		importance,
		sensitivity,
		sqlQuoteParam("file_attachments"),
		sqlQuoteParam("query"),
	)

	var insertErr error
	if cs != nil {
		ctx := cs.GetQueryContext()
		_, insertErr = s.ExecuteSessionSQLContext(ctx, cs.ID, insertSQL)
	} else {
		_, insertErr = s.ExecuteSQL(insertSQL)
	}
	if insertErr != nil {
		return nil, fmt.Errorf("failed to queue mail item: %w", insertErr)
	}

	// Update output parameter bindings for the session
	if cs != nil && len(outputBindings) > 0 {
		cs.Mu.Lock()
		if cs.Vars == nil {
			cs.Vars = make(map[string]any)
		}
		for targetParam, callerVar := range outputBindings {
			if targetParam == "mailitem_id" {
				cs.Vars[callerVar] = mailItemID
				cs.Vars[strings.TrimPrefix(callerVar, "@")] = mailItemID
			}
		}
		cs.Mu.Unlock()
	}

	msg := fmt.Sprintf("Mail (Id: %d) queued.", mailItemID)
	return &QueryResult{
		Success:         true,
		Command:         "EXEC",
		Messages:        []string{msg},
		RowsAffected:    1,
		DurationUS:      time.Since(start).Microseconds(),
		ReturnStatus:    0,
		HasReturnStatus: true,
	}, nil
}

func toInt64Value(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case string:
		if val, err := strconv.ParseInt(n, 10, 64); err == nil {
			return val, true
		}
	}
	return 0, false
}
