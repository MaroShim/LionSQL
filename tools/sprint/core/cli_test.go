package core

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type mockBackend struct {
	execFn func(sql string) (*QueryResp, error)
	log    []string
}

func (m *mockBackend) Name() string { return "MockDB" }
func (m *mockBackend) Init() error  { return nil }
func (m *mockBackend) Close() error { return nil }
func (m *mockBackend) Exec(sql string) (*QueryResp, error) {
	m.log = append(m.log, sql)
	if m.execFn != nil {
		return m.execFn(sql)
	}
	return &QueryResp{Success: true}, nil
}

func TestStatusOrder(t *testing.T) {
	cases := []struct {
		status string
		want   int
	}{
		{"IN_PROGRESS", 0},
		{"in_progress", 0},
		{"PENDING", 1},
		{"DONE", 2},
		{"SPEC_OUT", 3},
	}
	for _, tc := range cases {
		if got := statusOrder(tc.status); got != tc.want {
			t.Fatalf("statusOrder(%q) = %d, want %d", tc.status, got, tc.want)
		}
	}
}

func TestEnsureMilestoneColumn_MigratesLegacyTable(t *testing.T) {
	migrated := false
	mb := &mockBackend{
		execFn: func(sql string) (*QueryResp, error) {
			if strings.Contains(sql, "SELECT milestone FROM") && !migrated {
				return nil, errors.New("no such column: milestone")
			}
			if strings.Contains(sql, "ALTER TABLE sprint_tasks ADD COLUMN milestone") {
				migrated = true
				return &QueryResp{Success: true}, nil
			}
			return &QueryResp{Success: true}, nil
		},
	}
	if err := EnsureMilestoneColumn(mb); err != nil {
		t.Fatalf("EnsureMilestoneColumn failed: %v", err)
	}
	if !migrated {
		t.Fatalf("expected ALTER TABLE migration query to run")
	}
}

func TestBackupAndRestore_PreservesMilestone(t *testing.T) {
	mb := &mockBackend{
		execFn: func(sql string) (*QueryResp, error) {
			if strings.HasPrefix(sql, "SELECT id, status, COALESCE(milestone") {
				return &QueryResp{
					Success: true,
					Rows: []map[string]any{
						{
							"id":          "TASK-1",
							"status":      "PENDING",
							"milestone":   "Post-1.0",
							"domain":      "Storage",
							"priority":    2,
							"title":       "Test task",
							"plan_doc":    "notes/TEST.ko.md",
							"metrics":     "",
							"target_files": "pkg/storage/a.go",
							"phases":      "",
							"description": "Details here",
							"updated_at":  "2026-11-11 12:00:00",
						},
					},
				}, nil
			}
			return &QueryResp{Success: true}, nil
		},
	}

	tmpPath := filepath.Join(t.TempDir(), "TASKS.md")
	if err := BackupToFile(mb, tmpPath); err != nil {
		t.Fatalf("BackupToFile failed: %v", err)
	}

	var restoredSQLs []string
	restoreMB := &mockBackend{
		execFn: func(sql string) (*QueryResp, error) {
			restoredSQLs = append(restoredSQLs, sql)
			return &QueryResp{Success: true}, nil
		},
	}
	cnt, err := RestoreFromFile(restoreMB, tmpPath)
	if err != nil {
		t.Fatalf("RestoreFromFile failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected 1 restored task, got %d", cnt)
	}
	joined := strings.Join(restoredSQLs, "\n")
	if !strings.Contains(joined, "'Post-1.0'") {
		t.Fatalf("expected restored SQL to preserve milestone 'Post-1.0', got:\n%s", joined)
	}
}

func TestRunCLI_ListActiveAndDelete(t *testing.T) {
	var queries []string
	mb := &mockBackend{
		execFn: func(sql string) (*QueryResp, error) {
			queries = append(queries, sql)
			if strings.HasPrefix(sql, "SELECT id FROM sprint_tasks") {
				return &QueryResp{Success: true, Rows: []map[string]any{{"id": "T-DEL"}}}, nil
			}
			return &QueryResp{Success: true}, nil
		},
	}
	RunCLI(mb, "Test", []string{"list", "active", "1.0-GA"})
	if len(queries) == 0 || !strings.Contains(queries[0], "UPPER(status) != 'DONE'") || !strings.Contains(queries[0], "1.0-ga") {
		t.Fatalf("expected active + milestone SQL filter, got: %v", queries)
	}

	queries = nil
	RunCLI(mb, "Test", []string{"milestone", "T-DEL", "Post-1.0"})
	joined := strings.Join(queries, "\n")
	if !strings.Contains(joined, "SET milestone = 'Post-1.0'") {
		t.Fatalf("expected milestone update query, got:\n%s", joined)
	}
}
