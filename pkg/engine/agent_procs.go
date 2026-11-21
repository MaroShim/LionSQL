package engine

import (
	"context"
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// EnsureAgentTables initializes the msdb sysjobs, sysjobsteps, sysschedules, and sysjobhistory tables if they do not exist.
func (s *Coordinator) EnsureAgentTables() error {
	s.agentInitOnce.Do(func() {
		s.agentInitErr = s.initAgentTables()
	})
	return s.agentInitErr
}

func (s *Coordinator) initAgentTables() error {
	jobsSchema := `CREATE TABLE IF NOT EXISTS sysjobs (
		job_id VARCHAR(64) PRIMARY KEY,
		name VARCHAR(128),
		enabled INT,
		description VARCHAR,
		start_step_id INT,
		category_id INT,
		date_created DATETIME,
		date_modified DATETIME,
		version_number INT
	);`
	if _, err := s.ExecuteSessionSQL("__sys_agent", jobsSchema); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return fmt.Errorf("failed to create sysjobs table: %w", err)
	}

	stepsSchema := `CREATE TABLE IF NOT EXISTS sysjobsteps (
		step_uid VARCHAR(64) PRIMARY KEY,
		job_id VARCHAR(64),
		step_id INT,
		step_name VARCHAR(128),
		subsystem VARCHAR(40),
		command VARCHAR,
		flags INT
	);`
	if _, err := s.ExecuteSessionSQL("__sys_agent", stepsSchema); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return fmt.Errorf("failed to create sysjobsteps table: %w", err)
	}

	schedulesSchema := `CREATE TABLE IF NOT EXISTS sysschedules (
		schedule_id INT PRIMARY KEY,
		job_id VARCHAR(64),
		name VARCHAR(128),
		enabled INT,
		freq_type INT,
		freq_interval INT,
		cron_expr VARCHAR(64),
		date_created DATETIME
	);`
	if _, err := s.ExecuteSessionSQL("__sys_agent", schedulesSchema); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return fmt.Errorf("failed to create sysschedules table: %w", err)
	}

	historySchema := `CREATE TABLE IF NOT EXISTS sysjobhistory (
		instance_id INT PRIMARY KEY,
		job_id VARCHAR(64),
		step_id INT,
		step_name VARCHAR(128),
		sql_message_id INT,
		sql_severity INT,
		message VARCHAR,
		run_status INT,
		run_date INT,
		run_time INT,
		run_duration INT
	);`
	if _, err := s.ExecuteSessionSQL("__sys_agent", historySchema); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return fmt.Errorf("failed to create sysjobhistory table: %w", err)
	}

	// Initialize history counter with current max instance_id
	if res, err := s.ExecuteSessionSQL("__sys_agent", "SELECT MAX(instance_id) AS max_id FROM sysjobhistory;"); err == nil && len(res.Rows) > 0 {
		if maxID, ok := res.Rows[0]["max_id"].(int64); ok {
			s.agentHistoryCounter.Store(maxID)
		}
	}
	if res, err := s.ExecuteSessionSQL("__sys_agent", "SELECT MAX(schedule_id) AS max_id FROM sysschedules;"); err == nil && len(res.Rows) > 0 {
		if maxID, ok := res.Rows[0]["max_id"].(int64); ok {
			s.agentScheduleCounter.Store(maxID)
		}
	}

	return nil
}

func escapeAgentString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func parseAgentArgs(args []string, rawArgs []string, positionalNames []string) (map[string]string, map[string]string) {
	paramValues := make(map[string]string)
	outputBindings := make(map[string]string)

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
			isQuoted := strings.HasPrefix(valStr, "'") || strings.HasPrefix(valStr, "N'") || strings.HasPrefix(valStr, "n'") || strings.HasPrefix(valStr, "\"")
			if !isQuoted && strings.EqualFold(valStr, "NULL") {
				paramValues[paramName] = ""
			} else {
				paramValues[paramName] = unwrapSQLString(valStr)
			}
		}
	}
	return paramValues, outputBindings
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// execSpAddJob implements msdb.dbo.sp_add_job.
// Parameters: @job_name, @enabled, @description, @start_step_id, @category_id, @job_id OUTPUT
func (s *Coordinator) execSpAddJob(args []string, rawArgs []string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if err := s.EnsureAgentTables(); err != nil {
		return nil, fmt.Errorf("agent tables initialization failed: %w", err)
	}

	positional := []string{"job_name", "enabled", "description", "start_step_id", "category_id", "job_id"}
	params, outputBindings := parseAgentArgs(args, rawArgs, positional)

	jobName := strings.TrimSpace(params["job_name"])
	if jobName == "" {
		return nil, fmt.Errorf("Msg 14262, Level 16, State 1, Procedure sp_add_job: The specified @job_name cannot be null or empty.")
	}

	// Check for duplicate job_name
	dupCheckSQL := fmt.Sprintf("SELECT job_id FROM sysjobs WHERE LOWER(name) = LOWER('%s');", escapeAgentString(jobName))
	if dupRes, err := s.ExecuteSessionSQL("__sys_agent", dupCheckSQL); err == nil && len(dupRes.Rows) > 0 {
		return nil, fmt.Errorf("Msg 14261, Level 16, State 1, Procedure sp_add_job: The specified @job_name ('%s') already exists.", jobName)
	}

	enabled := 1
	if v, ok := params["enabled"]; ok && v != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			enabled = parsed
		}
	}

	desc := strings.TrimSpace(params["description"])
	jobID := generateUUID()
	now := time.Now().Format("2006-01-02 15:04:05")

	insertSQL := fmt.Sprintf(`INSERT INTO sysjobs (job_id, name, enabled, description, start_step_id, category_id, date_created, date_modified, version_number)
		VALUES ('%s', '%s', %d, '%s', 1, 0, '%s', '%s', 1);`,
		jobID, escapeAgentString(jobName), enabled, escapeAgentString(desc), now, now)

	if _, err := s.ExecuteSessionSQL("__sys_agent", insertSQL); err != nil {
		return nil, fmt.Errorf("failed to register job in sysjobs: %w", err)
	}

	// Handle @job_id OUTPUT if specified
	if cs != nil && len(outputBindings) > 0 {
		cs.Mu.Lock()
		if cs.Vars == nil {
			cs.Vars = make(map[string]any)
		}
		for targetParam, callerVar := range outputBindings {
			if targetParam == "job_id" {
				cs.Vars[callerVar] = jobID
				cs.Vars[strings.TrimPrefix(callerVar, "@")] = jobID
			}
		}
		cs.Mu.Unlock()
	}

	return &QueryResult{
		Success:    true,
		Command:    "EXEC",
		Columns:    []string{"job_id"},
		Rows:       []map[string]any{{"job_id": jobID}},
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Job '%s' created successfully (job_id: %s)", jobName, jobID)},
	}, nil
}

// execSpAddJobStep implements msdb.dbo.sp_add_jobstep.
// Parameters: @job_name, @job_id, @step_id, @step_name, @subsystem, @command
func (s *Coordinator) execSpAddJobStep(args []string, rawArgs []string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if err := s.EnsureAgentTables(); err != nil {
		return nil, err
	}

	positional := []string{"job_name", "step_id", "step_name", "subsystem", "command", "job_id"}
	params, _ := parseAgentArgs(args, rawArgs, positional)

	jobName := strings.TrimSpace(params["job_name"])
	jobID := strings.TrimSpace(params["job_id"])

	resolvedJobID := jobID
	if resolvedJobID == "" && jobName != "" {
		res, err := s.ExecuteSessionSQL("__sys_agent", fmt.Sprintf("SELECT job_id FROM sysjobs WHERE LOWER(name) = LOWER('%s');", escapeAgentString(jobName)))
		if err != nil || len(res.Rows) == 0 {
			return nil, fmt.Errorf("Msg 14262, Level 16, State 1, Procedure sp_add_jobstep: The specified @job_name ('%s') does not exist.", jobName)
		}
		if id, ok := res.Rows[0]["job_id"].(string); ok {
			resolvedJobID = id
		}
	}
	if resolvedJobID == "" {
		return nil, fmt.Errorf("Msg 14262, Level 16, State 1, Procedure sp_add_jobstep: Either @job_name or @job_id must be specified.")
	}

	stepID := 1
	if v, ok := params["step_id"]; ok && v != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && parsed > 0 {
			stepID = parsed
		}
	}

	stepName := strings.TrimSpace(params["step_name"])
	if stepName == "" {
		stepName = fmt.Sprintf("Step %d", stepID)
	}

	subsystem := strings.ToUpper(strings.TrimSpace(params["subsystem"]))
	if subsystem == "" {
		subsystem = "TSQL"
	}

	command := strings.TrimSpace(params["command"])
	if command == "" {
		return nil, fmt.Errorf("Msg 14262, Level 16, State 1, Procedure sp_add_jobstep: @command cannot be null or empty.")
	}

	stepUID := fmt.Sprintf("%s_%d", resolvedJobID, stepID)
	insertSQL := fmt.Sprintf(`INSERT INTO sysjobsteps (step_uid, job_id, step_id, step_name, subsystem, command, flags)
		VALUES ('%s', '%s', %d, '%s', '%s', '%s', 0);`,
		stepUID, resolvedJobID, stepID, escapeAgentString(stepName), subsystem, escapeAgentString(command))

	if _, err := s.ExecuteSessionSQL("__sys_agent", insertSQL); err != nil {
		return nil, fmt.Errorf("failed to register job step: %w", err)
	}

	return &QueryResult{
		Success:    true,
		Command:    "EXEC",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Job step '%s' (step_id: %d) added successfully", stepName, stepID)},
	}, nil
}

// execSpAddJobSchedule implements msdb.dbo.sp_add_jobschedule.
// Parameters: @job_name, @job_id, @name, @enabled, @freq_type, @freq_interval, @cron_expr
func (s *Coordinator) execSpAddJobSchedule(args []string, rawArgs []string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if err := s.EnsureAgentTables(); err != nil {
		return nil, err
	}

	positional := []string{"job_name", "name", "enabled", "freq_type", "freq_interval", "cron_expr", "job_id"}
	params, _ := parseAgentArgs(args, rawArgs, positional)

	jobName := strings.TrimSpace(params["job_name"])
	jobID := strings.TrimSpace(params["job_id"])

	resolvedJobID := jobID
	if resolvedJobID == "" && jobName != "" {
		res, err := s.ExecuteSessionSQL("__sys_agent", fmt.Sprintf("SELECT job_id FROM sysjobs WHERE LOWER(name) = LOWER('%s');", escapeAgentString(jobName)))
		if err != nil || len(res.Rows) == 0 {
			return nil, fmt.Errorf("Msg 14262, Level 16, State 1, Procedure sp_add_jobschedule: The specified @job_name ('%s') does not exist.", jobName)
		}
		if id, ok := res.Rows[0]["job_id"].(string); ok {
			resolvedJobID = id
		}
	}
	if resolvedJobID == "" {
		return nil, fmt.Errorf("Msg 14262, Level 16, State 1, Procedure sp_add_jobschedule: Either @job_name or @job_id must be specified.")
	}

	scheduleName := strings.TrimSpace(params["name"])
	if scheduleName == "" {
		scheduleName = "Schedule 1"
	}

	enabled := 1
	if v, ok := params["enabled"]; ok && v != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			enabled = parsed
		}
	}

	freqType := 4 // default: Daily (4)
	if v, ok := params["freq_type"]; ok && v != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			freqType = parsed
		}
	}

	freqInterval := 1
	if v, ok := params["freq_interval"]; ok && v != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			freqInterval = parsed
		}
	}

	cronExpr := strings.TrimSpace(params["cron_expr"])
	if cronExpr == "" {
		switch freqType {
		case 1: // Once -> default to once per day
			cronExpr = "0 0 * * *"
		case 4: // Daily
			cronExpr = "0 0 * * *"
		case 8: // Weekly
			cronExpr = "0 0 * * 0"
		case 16: // Monthly
			cronExpr = "0 0 1 * *"
		default:
			cronExpr = "0 * * * *" // Hourly
		}
	}

	scheduleID := int(s.agentScheduleCounter.Add(1))
	now := time.Now().Format("2006-01-02 15:04:05")

	insertSQL := fmt.Sprintf(`INSERT INTO sysschedules (schedule_id, job_id, name, enabled, freq_type, freq_interval, cron_expr, date_created)
		VALUES (%d, '%s', '%s', %d, %d, %d, '%s', '%s');`,
		scheduleID, resolvedJobID, escapeAgentString(scheduleName), enabled, freqType, freqInterval, escapeAgentString(cronExpr), now)

	if _, err := s.ExecuteSessionSQL("__sys_agent", insertSQL); err != nil {
		return nil, fmt.Errorf("failed to register schedule in sysschedules: %w", err)
	}

	return &QueryResult{
		Success:    true,
		Command:    "EXEC",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Schedule '%s' (schedule_id: %d) bound to job %s", scheduleName, scheduleID, resolvedJobID)},
	}, nil
}

// execSpStartJob implements msdb.dbo.sp_start_job.
// Triggers all job steps sequentially in the background and records execution history in sysjobhistory.
func (s *Coordinator) execSpStartJob(args []string, rawArgs []string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if err := s.EnsureAgentTables(); err != nil {
		return nil, err
	}

	positional := []string{"job_name", "job_id"}
	params, _ := parseAgentArgs(args, rawArgs, positional)

	jobName := strings.TrimSpace(params["job_name"])
	jobID := strings.TrimSpace(params["job_id"])

	resolvedJobID := jobID
	if resolvedJobID == "" && jobName != "" {
		res, err := s.ExecuteSessionSQL("__sys_agent", fmt.Sprintf("SELECT job_id FROM sysjobs WHERE LOWER(name) = LOWER('%s');", escapeAgentString(jobName)))
		if err != nil || len(res.Rows) == 0 {
			return nil, fmt.Errorf("Msg 14262, Level 16, State 1, Procedure sp_start_job: The specified @job_name ('%s') does not exist.", jobName)
		}
		if id, ok := res.Rows[0]["job_id"].(string); ok {
			resolvedJobID = id
		}
	}
	if resolvedJobID == "" {
		return nil, fmt.Errorf("Msg 14262, Level 16, State 1, Procedure sp_start_job: Either @job_name or @job_id must be specified.")
	}

	// Trigger asynchronous job execution
	go s.runJobStepsAsync(resolvedJobID)

	return &QueryResult{
		Success:    true,
		Command:    "EXEC",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Job '%s' (job_id: %s) started asynchronously", jobName, resolvedJobID)},
	}, nil
}

// runJobStepsAsync executes all steps for a job sequentially, capturing timing and status.
func (s *Coordinator) runJobStepsAsync(jobID string) {
	stepsSQL := fmt.Sprintf("SELECT step_id, step_name, subsystem, command FROM sysjobsteps WHERE job_id = '%s' ORDER BY step_id ASC;", jobID)
	stepsRes, err := s.ExecuteSessionSQL("__sys_agent", stepsSQL)
	if err != nil || len(stepsRes.Rows) == 0 {
		return
	}

	overallStatus := 1
	var lastErrMsg string

	for _, step := range stepsRes.Rows {
		stepID := 1
		if id, ok := step["step_id"].(int64); ok {
			stepID = int(id)
		}
		stepName, _ := step["step_name"].(string)
		cmd, _ := step["command"].(string)

		stepStart := time.Now()
		stepErr := s.executeAgentStep(cmd)
		stepDurSec := int(time.Since(stepStart).Seconds())

		runStatus := 1
		msg := "Step executed successfully."
		if stepErr != nil {
			runStatus = 0
			overallStatus = 0
			msg = stepErr.Error()
			lastErrMsg = msg
		}

		s.recordJobHistory(jobID, stepID, stepName, runStatus, msg, stepStart, stepDurSec)
		if stepErr != nil {
			break // Stop on step failure
		}
	}

	// Record master job completion (step_id = 0)
	masterMsg := "The job succeeded."
	if overallStatus == 0 {
		masterMsg = fmt.Sprintf("The job failed. Last error: %s", lastErrMsg)
	}
	s.recordJobHistory(jobID, 0, "(Job outcome)", overallStatus, masterMsg, time.Now(), 0)
}

func (s *Coordinator) executeAgentStep(cmd string) error {
	if strings.TrimSpace(cmd) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	sessID := fmt.Sprintf("__agent_worker_%d", time.Now().UnixNano())
	_, err := s.ExecuteSessionSQLContext(ctx, sessID, cmd)
	return err
}

func (s *Coordinator) recordJobHistory(jobID string, stepID int, stepName string, status int, msg string, startTime time.Time, durationSec int) {
	instanceID := int(s.agentHistoryCounter.Add(1))
	runDate, _ := strconv.Atoi(startTime.Format("20060102"))
	runTime, _ := strconv.Atoi(startTime.Format("150405"))

	insertSQL := fmt.Sprintf(`INSERT INTO sysjobhistory (instance_id, job_id, step_id, step_name, sql_message_id, sql_severity, message, run_status, run_date, run_time, run_duration)
		VALUES (%d, '%s', %d, '%s', 0, 0, '%s', %d, %d, %d, %d);`,
		instanceID, jobID, stepID, escapeAgentString(stepName), escapeAgentString(msg), status, runDate, runTime, durationSec)

	_, _ = s.ExecuteSessionSQL("__sys_agent", insertSQL)
}

// execSpDeleteJob implements msdb.dbo.sp_delete_job.
// Parameters: @job_name, @job_id
func (s *Coordinator) execSpDeleteJob(args []string, rawArgs []string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if err := s.EnsureAgentTables(); err != nil {
		return nil, err
	}

	positional := []string{"job_name", "job_id"}
	params, _ := parseAgentArgs(args, rawArgs, positional)

	jobName := strings.TrimSpace(params["job_name"])
	jobID := strings.TrimSpace(params["job_id"])

	resolvedJobID := jobID
	if resolvedJobID == "" && jobName != "" {
		res, err := s.ExecuteSessionSQL("__sys_agent", fmt.Sprintf("SELECT job_id FROM sysjobs WHERE LOWER(name) = LOWER('%s');", escapeAgentString(jobName)))
		if err != nil || len(res.Rows) == 0 {
			return nil, fmt.Errorf("Msg 14262, Level 16, State 1, Procedure sp_delete_job: The specified @job_name ('%s') does not exist.", jobName)
		}
		if id, ok := res.Rows[0]["job_id"].(string); ok {
			resolvedJobID = id
		}
	}
	if resolvedJobID == "" {
		return nil, fmt.Errorf("Msg 14262, Level 16, State 1, Procedure sp_delete_job: Either @job_name or @job_id must be specified.")
	}

	// Delete from all agent tables
	_, _ = s.ExecuteSessionSQL("__sys_agent", fmt.Sprintf("DELETE FROM sysjobsteps WHERE job_id = '%s';", resolvedJobID))
	_, _ = s.ExecuteSessionSQL("__sys_agent", fmt.Sprintf("DELETE FROM sysschedules WHERE job_id = '%s';", resolvedJobID))
	_, _ = s.ExecuteSessionSQL("__sys_agent", fmt.Sprintf("DELETE FROM sysjobhistory WHERE job_id = '%s';", resolvedJobID))
	_, _ = s.ExecuteSessionSQL("__sys_agent", fmt.Sprintf("DELETE FROM sysjobs WHERE job_id = '%s';", resolvedJobID))

	return &QueryResult{
		Success:    true,
		Command:    "EXEC",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Job '%s' (job_id: %s) deleted successfully", jobName, resolvedJobID)},
	}, nil
}
