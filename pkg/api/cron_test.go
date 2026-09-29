package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func TestCronScheduler_Basics(t *testing.T) {
	executed := false
	var execCmd string

	cs := NewCronScheduler(func(ctx context.Context, cmdType, cmd string) error {
		executed = true
		execCmd = cmd
		return nil
	})

	job := &CronJob{
		Name:        "Test Job",
		Schedule:    "*/5 * * * *",
		CommandType: "sql",
		Command:     "DELETE FROM audit_logs WHERE id < 10",
		Enabled:     true,
	}

	if err := cs.AddJob(job); err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	if job.ID == "" {
		t.Fatalf("expected generated job ID")
	}

	// Verify GetJob & ListJobs
	fetched, ok := cs.GetJob(job.ID)
	if !ok || fetched.Name != "Test Job" {
		t.Fatalf("GetJob failed or mismatched: %+v", fetched)
	}
	list := cs.ListJobs()
	if len(list) != 1 {
		t.Fatalf("expected 1 job, got %d", len(list))
	}

	// Manual execution via RunJobNow
	ctx := context.Background()
	if err := cs.RunJobNow(ctx, job.ID); err != nil {
		t.Fatalf("RunJobNow failed: %v", err)
	}
	if !executed || execCmd != "DELETE FROM audit_logs WHERE id < 10" {
		t.Fatalf("expected job to execute with correct command, got executed=%v cmd=%q", executed, execCmd)
	}

	jobPostRun, _ := cs.GetJob(job.ID)
	if jobPostRun.LastStatus != "success" {
		t.Fatalf("expected last status success, got %q", jobPostRun.LastStatus)
	}

	// Update job
	job.Name = "Updated Job"
	job.Schedule = "0 0 * * *"
	if err := cs.UpdateJob(job); err != nil {
		t.Fatalf("UpdateJob failed: %v", err)
	}
	updated, _ := cs.GetJob(job.ID)
	if updated.Name != "Updated Job" || updated.Schedule != "0 0 * * *" {
		t.Fatalf("UpdateJob failed to mutate fields: %+v", updated)
	}

	// Delete job
	if err := cs.DeleteJob(job.ID); err != nil {
		t.Fatalf("DeleteJob failed: %v", err)
	}
	if _, ok := cs.GetJob(job.ID); ok {
		t.Fatalf("job still exists after delete")
	}
}

func TestCronScheduler_Validation(t *testing.T) {
	cs := NewCronScheduler(nil)

	// Invalid fields count
	err := cs.AddJob(&CronJob{Name: "Bad", Schedule: "* * *", CommandType: "sql", Command: "SELECT 1"})
	if err == nil {
		t.Fatalf("expected error for 3-field cron expression")
	}

	// Invalid minute
	err = cs.AddJob(&CronJob{Name: "Bad", Schedule: "60 * * * *", CommandType: "sql", Command: "SELECT 1"})
	if err == nil {
		t.Fatalf("expected error for minute 60")
	}

	// Invalid command type
	err = cs.AddJob(&CronJob{Name: "Bad", Schedule: "* * * * *", CommandType: "python", Command: "print()"})
	if err == nil {
		t.Fatalf("expected error for invalid command_type")
	}
}

func TestCronScheduler_TimeMatching(t *testing.T) {
	// Schedule: "15 10 1 5 *" -> 10:15 on May 1st
	may1 := time.Date(2026, 5, 1, 10, 15, 0, 0, time.UTC)
	if !matchCron("15 10 1 5 *", may1) {
		t.Errorf("expected schedule to match on 2026-05-01 10:15")
	}

	may2 := time.Date(2026, 5, 2, 10, 15, 0, 0, time.UTC)
	if matchCron("15 10 1 5 *", may2) {
		t.Errorf("schedule should not match on May 2nd")
	}

	// Every 10 minutes: "*/10 * * * *"
	at20 := time.Date(2026, 9, 29, 14, 20, 0, 0, time.UTC)
	if !matchCron("*/10 * * * *", at20) {
		t.Errorf("expected */10 to match minute 20")
	}
	at25 := time.Date(2026, 9, 29, 14, 25, 0, 0, time.UTC)
	if matchCron("*/10 * * * *", at25) {
		t.Errorf("expected */10 not to match minute 25")
	}
}

func TestCronAPI_Endpoints(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	server := NewServer(cat, eng)
	defer server.Crons().Stop()

	// 1. Create Cron Job
	body := map[string]interface{}{
		"name":         "Nightly Cleanup",
		"schedule":     "0 2 * * *",
		"command_type": "sql",
		"command":      "DELETE FROM sessions WHERE expires < NOW()",
		"enabled":      true,
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/crons", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/crons expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created CronJob
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatalf("expected non-empty created.ID")
	}

	// 2. List Crons
	req = httptest.NewRequest("GET", "/api/crons", nil)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/crons expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listRes struct {
		Items []CronJob `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listRes)
	if len(listRes.Items) != 1 || listRes.Items[0].Name != "Nightly Cleanup" {
		t.Fatalf("unexpected list response: %+v", listRes)
	}

	// 3. Trigger Run Now
	req = httptest.NewRequest("POST", "/api/crons/"+created.ID+"/run", nil)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/crons/:id/run expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 4. Delete Cron Job
	req = httptest.NewRequest("DELETE", "/api/crons/"+created.ID, nil)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("DELETE /api/crons/:id expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
