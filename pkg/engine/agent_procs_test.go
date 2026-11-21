package engine_test

import (
	"fmt"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func newTestCoordinatorForAgent(t *testing.T) *engine.Coordinator {
	t.Helper()
	eng := memengine.New()
	t.Cleanup(func() { eng.Close() })
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	t.Cleanup(func() { coord.Close() })
	return coord
}

func TestAgent_JobLifecycle(t *testing.T) {
	coord := newTestCoordinatorForAgent(t)
	sessID := "agent_session_1"

	// 1. Add Job
	addJobSQL := `EXEC msdb.dbo.sp_add_job @job_name = 'DailyBackup', @enabled = 1, @description = 'Performs daily backups', @job_id = @my_job_id OUTPUT;`
	res, err := coord.ExecuteSessionSQL(sessID, addJobSQL)
	if err != nil {
		t.Fatalf("sp_add_job failed: %v", err)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("expected rows from sp_add_job")
	}
	jobIDVal, ok := res.Rows[0]["job_id"].(string)
	if !ok || jobIDVal == "" {
		t.Fatalf("expected valid job_id, got: %v", res.Rows[0]["job_id"])
	}

	// Verify session output variable was bound
	sess := coord.GetSession(sessID)
	if sess == nil {
		t.Fatalf("expected session %s to exist", sessID)
	}
	sess.Mu.Lock()
	boundVar := sess.Vars["my_job_id"]
	sess.Mu.Unlock()
	if boundVar != jobIDVal {
		t.Fatalf("expected session var @my_job_id = %v, got %v", jobIDVal, boundVar)
	}

	// 2. Add Job Step
	addStepSQL := fmt.Sprintf(`EXEC msdb.dbo.sp_add_jobstep @job_id = '%s', @step_name = 'Step1_Create', @subsystem = 'TSQL', @command = 'CREATE TABLE agent_test (id INT); INSERT INTO agent_test VALUES (100);', @on_success_action = 1;`, jobIDVal)
	_, err = coord.ExecuteSessionSQL(sessID, addStepSQL)
	if err != nil {
		t.Fatalf("sp_add_jobstep failed: %v", err)
	}

	// 3. Add Job Schedule
	addSchedSQL := fmt.Sprintf(`EXEC msdb.dbo.sp_add_jobschedule @job_id = '%s', @name = 'DailyMorning', @freq_type = 4, @freq_interval = 1, @active_start_time = 090000;`, jobIDVal)
	_, err = coord.ExecuteSessionSQL(sessID, addSchedSQL)
	if err != nil {
		t.Fatalf("sp_add_jobschedule failed: %v", err)
	}

	// 4. Query msdb.dbo.sysjobs virtual table
	jobsRes, err := coord.ExecuteSessionSQL(sessID, "SELECT job_id, name, enabled, description FROM msdb.dbo.sysjobs;")
	if err != nil {
		t.Fatalf("query sysjobs failed: %v", err)
	}
	if len(jobsRes.Rows) != 1 {
		t.Fatalf("expected 1 job in sysjobs, got %d", len(jobsRes.Rows))
	}
	if jobsRes.Rows[0]["name"] != "DailyBackup" {
		t.Fatalf("expected job name 'DailyBackup', got %v", jobsRes.Rows[0]["name"])
	}

	// 5. Query msdb.dbo.sysjobsteps
	stepsRes, err := coord.ExecuteSessionSQL(sessID, "SELECT step_id, step_name, subsystem FROM msdb.dbo.sysjobsteps;")
	if err != nil {
		t.Fatalf("query sysjobsteps failed: %v", err)
	}
	if len(stepsRes.Rows) != 1 {
		t.Fatalf("expected 1 step in sysjobsteps, got %d", len(stepsRes.Rows))
	}
	if stepsRes.Rows[0]["step_name"] != "Step1_Create" {
		t.Fatalf("expected step_name 'Step1_Create', got %v", stepsRes.Rows[0]["step_name"])
	}

	// 6. Query msdb.dbo.sysschedules
	schedRes, err := coord.ExecuteSessionSQL(sessID, "SELECT name, freq_type FROM msdb.dbo.sysschedules;")
	if err != nil {
		t.Fatalf("query sysschedules failed: %v", err)
	}
	if len(schedRes.Rows) != 1 {
		t.Fatalf("expected 1 schedule in sysschedules, got %d", len(schedRes.Rows))
	}
	if schedRes.Rows[0]["name"] != "DailyMorning" {
		t.Fatalf("expected schedule name 'DailyMorning', got %v", schedRes.Rows[0]["name"])
	}
}

func TestAgent_StartJobAndHistory(t *testing.T) {
	coord := newTestCoordinatorForAgent(t)
	sessID := "agent_session_2"

	// 1. Create Job with Step that creates a table and inserts a row
	_, err := coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_add_job @job_name = 'ExecTestJob';`)
	if err != nil {
		t.Fatalf("sp_add_job failed: %v", err)
	}

	_, err = coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_add_jobstep @job_name = 'ExecTestJob', @step_name = 'ExecuteStep', @command = 'CREATE TABLE exec_test_tbl (val INT); INSERT INTO exec_test_tbl VALUES (42);';`)
	if err != nil {
		t.Fatalf("sp_add_jobstep failed: %v", err)
	}

	// 2. Start Job
	_, err = coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_start_job @job_name = 'ExecTestJob';`)
	if err != nil {
		t.Fatalf("sp_start_job failed: %v", err)
	}

	// 3. Wait for async execution to complete
	deadline := time.Now().Add(3 * time.Second)
	var histRes *engine.QueryResult
	for time.Now().Before(deadline) {
		histRes, err = coord.ExecuteSessionSQL(sessID, "SELECT job_id, step_id, run_status, message FROM msdb.dbo.sysjobhistory;")
		if err == nil && len(histRes.Rows) >= 2 { // step 1 + job completion (step 0)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if histRes == nil || len(histRes.Rows) < 2 {
		t.Fatalf("timed out waiting for sysjobhistory entries, got: %v", histRes)
	}

	// Check that run_status is 1 (Success) for all entries
	for _, row := range histRes.Rows {
		status, _ := row["run_status"].(int64)
		if status != 1 {
			t.Fatalf("expected run_status 1, got %v (message: %v)", row["run_status"], row["message"])
		}
	}

	// Verify that table was created and row inserted
	tblRes, err := coord.ExecuteSessionSQL(sessID, "SELECT val FROM exec_test_tbl;")
	if err != nil {
		t.Fatalf("failed to query table created by agent job: %v", err)
	}
	if len(tblRes.Rows) != 1 || tblRes.Rows[0]["val"] != int64(42) {
		t.Fatalf("expected row with 42, got: %v", tblRes.Rows)
	}
}

func TestAgent_JobFailureHistory(t *testing.T) {
	coord := newTestCoordinatorForAgent(t)
	sessID := "agent_session_3"

	// 1. Create Job with invalid SQL command
	_, err := coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_add_job @job_name = 'FailingJob';`)
	if err != nil {
		t.Fatalf("sp_add_job failed: %v", err)
	}

	_, err = coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_add_jobstep @job_name = 'FailingJob', @step_name = 'BadStep', @command = 'SELECT * FROM non_existent_table_for_agent_test;';`)
	if err != nil {
		t.Fatalf("sp_add_jobstep failed: %v", err)
	}

	// 2. Start Job
	_, err = coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_start_job @job_name = 'FailingJob';`)
	if err != nil {
		t.Fatalf("sp_start_job failed: %v", err)
	}

	// 3. Wait for async execution
	deadline := time.Now().Add(3 * time.Second)
	var histRes *engine.QueryResult
	for time.Now().Before(deadline) {
		histRes, err = coord.ExecuteSessionSQL(sessID, "SELECT job_id, step_id, run_status, message FROM msdb.dbo.sysjobhistory;")
		if err == nil && len(histRes.Rows) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if histRes == nil || len(histRes.Rows) < 2 {
		t.Fatalf("timed out waiting for sysjobhistory entries, got: %v", histRes)
	}

	// Assert run_status == 0 (Failure)
	foundFailure := false
	for _, row := range histRes.Rows {
		status, _ := row["run_status"].(int64)
		if status == 0 {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Fatalf("expected at least one entry with run_status 0 in sysjobhistory, got: %v", histRes.Rows)
	}
}

func TestAgent_DeleteJob(t *testing.T) {
	coord := newTestCoordinatorForAgent(t)
	sessID := "agent_session_4"

	// Add job, step, and schedule
	_, err := coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_add_job @job_name = 'DeleteMeJob';`)
	if err != nil {
		t.Fatalf("sp_add_job failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_add_jobstep @job_name = 'DeleteMeJob', @step_name = 'Step1', @command = 'SELECT 1;';`)
	if err != nil {
		t.Fatalf("sp_add_jobstep failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_add_jobschedule @job_name = 'DeleteMeJob', @name = 'Sched1';`)
	if err != nil {
		t.Fatalf("sp_add_jobschedule failed: %v", err)
	}

	// Verify entries exist
	jobsRes, err := coord.ExecuteSessionSQL(sessID, "SELECT * FROM msdb.dbo.sysjobs;")
	if err != nil || len(jobsRes.Rows) != 1 {
		t.Fatalf("expected 1 job before delete, got %v", jobsRes)
	}

	// Delete job
	_, err = coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_delete_job @job_name = 'DeleteMeJob';`)
	if err != nil {
		t.Fatalf("sp_delete_job failed: %v", err)
	}

	// Verify all cascade records are removed
	jobsRes, err = coord.ExecuteSessionSQL(sessID, "SELECT * FROM msdb.dbo.sysjobs;")
	if err != nil || len(jobsRes.Rows) != 0 {
		t.Fatalf("expected 0 jobs after delete, got %v", jobsRes)
	}
	stepsRes, err := coord.ExecuteSessionSQL(sessID, "SELECT * FROM msdb.dbo.sysjobsteps;")
	if err != nil || len(stepsRes.Rows) != 0 {
		t.Fatalf("expected 0 jobsteps after delete, got %v", stepsRes)
	}
	schedRes, err := coord.ExecuteSessionSQL(sessID, "SELECT * FROM msdb.dbo.sysschedules;")
	if err != nil || len(schedRes.Rows) != 0 {
		t.Fatalf("expected 0 schedules after delete, got %v", schedRes)
	}
}

func TestAgent_ValidationAndErrors(t *testing.T) {
	coord := newTestCoordinatorForAgent(t)
	sessID := "agent_session_5"

	// Missing job_name and job_id
	_, err := coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_add_job;`)
	if err == nil {
		t.Fatalf("expected error for missing job_name")
	}

	// Create a job
	_, err = coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_add_job @job_name = 'UniqueJob';`)
	if err != nil {
		t.Fatalf("sp_add_job failed: %v", err)
	}

	// Duplicate job name
	_, err = coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_add_job @job_name = 'UniqueJob';`)
	if err == nil {
		t.Fatalf("expected error for duplicate job name")
	}

	// Delete non-existent job
	_, err = coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_delete_job @job_name = 'NoSuchJob';`)
	if err == nil {
		t.Fatalf("expected error for non-existent job")
	}
}
