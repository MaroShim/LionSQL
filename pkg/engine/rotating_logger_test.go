package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRegression_RotatingLogWriterPermissions verifies TASK-SEC-02:
// Rotating log writers (slow query and general logs) must strictly enforce 0600 permissions
// on initial open, reopening, and log rotation ([RULE:PathSecurity]).
func TestRegression_RotatingLogWriterPermissions(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test_query.log")

	// 1. Initial creation must enforce 0600
	writer, err := NewRotatingLogWriter(logPath, 500)
	if err != nil {
		t.Fatalf("NewRotatingLogWriter failed: %v", err)
	}

	stat, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("failed to stat initial log file: %v", err)
	}
	if stat.Mode().Perm() != 0600 {
		t.Fatalf("expected initial log permission 0600, got %04o", stat.Mode().Perm())
	}

	// 2. Write enough records to trigger rotation (maxBytes = 500)
	entry := map[string]string{
		"query": "SELECT * FROM sensitive_accounts WHERE ssn = '123-45-6789'",
	}
	for i := 0; i < 20; i++ {
		if err := writer.WriteLine(entry); err != nil {
			t.Fatalf("failed to write log line: %v", err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	// Verify rotated backup file (.1) exists and has 0600 permissions
	backupPath := logPath + ".1"
	backupStat, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("expected rotated backup file %s to exist: %v", backupPath, err)
	}
	if backupStat.Mode().Perm() != 0600 {
		t.Fatalf("expected backup log permission 0600, got %04o", backupStat.Mode().Perm())
	}

	// Verify active log file after rotation also has 0600 permissions
	activeStat, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("expected active log file to exist: %v", err)
	}
	if activeStat.Mode().Perm() != 0600 {
		t.Fatalf("expected active log permission 0600 after rotation, got %04o", activeStat.Mode().Perm())
	}

	// 3. Re-open with relaxed permission (0644) to ensure openFile tightens it to 0600
	if err := os.Chmod(logPath, 0644); err != nil {
		t.Fatalf("failed to chmod 0644: %v", err)
	}
	writer2, err := NewRotatingLogWriter(logPath, 1024)
	if err != nil {
		t.Fatalf("re-opening writer failed: %v", err)
	}
	_ = writer2.Close()

	tightenedStat, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("failed to stat tightened log: %v", err)
	}
	if tightenedStat.Mode().Perm() != 0600 {
		t.Fatalf("log permission was not tightened to 0600: got %04o", tightenedStat.Mode().Perm())
	}
}
