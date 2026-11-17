package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// TestP1_BatchM5_WritePidFileAtomic tests Scope-130 [RULE:AtomicSaves]:
// PID file writes must be atomic via temporary swap files, set 0644 permissions,
// and clean up temporary files.
func TestP1_BatchM5_WritePidFileAtomic(t *testing.T) {
	tempDir := t.TempDir()
	pidFile := filepath.Join(tempDir, "test.pid")

	testPid := 42424

	if err := writePidFileAtomic(pidFile, testPid); err != nil {
		t.Fatalf("writePidFileAtomic failed: %v", err)
	}

	// Verify PID content
	content, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("failed to read written pid file: %v", err)
	}
	if strings.TrimSpace(string(content)) != strconv.Itoa(testPid) {
		t.Fatalf("expected pid %d, got %s", testPid, string(content))
	}

	// Verify permissions (0644)
	stat, err := os.Stat(pidFile)
	if err != nil {
		t.Fatalf("failed to stat pid file: %v", err)
	}
	if stat.Mode().Perm() != 0644 {
		t.Fatalf("expected permissions 0644, got %v", stat.Mode().Perm())
	}

	// Verify no left-over .tmp files in directory
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("found leftover temporary swap file: %s", entry.Name())
		}
	}
}

// TestP1_BatchM5_StorageSyncerContract tests Scope-129 [RULE:ExplicitError]:
// Storage engines (memengine and wiredtiger) must implement storage.Syncer.
func TestP1_BatchM5_StorageSyncerContract(t *testing.T) {
	eng := memengine.New()
	opts := memengine.DefaultOptions(t.TempDir())
	opts.EnableAOF = true
	if err := eng.OpenWithOptions(opts); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	defer eng.Close()

	syncer, ok := storage.Engine(eng).(storage.Syncer)
	if !ok {
		t.Fatalf("Scope-129 violated: memengine does not implement storage.Syncer")
	}

	if err := syncer.Sync(); err != nil {
		t.Fatalf("syncer.Sync failed: %v", err)
	}
}

// TestRegression_DaemonLogFilePermissions verifies TASK-SEC-01:
// Daemon log files must be created with strict 0600 permissions to protect sensitive query data ([RULE:PathSecurity]).
func TestRegression_DaemonLogFilePermissions(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "lionsql_daemon.log")

	// 1. Initial creation must enforce 0600
	fd, err := openDaemonLogFile(logPath)
	if err != nil {
		t.Fatalf("openDaemonLogFile failed: %v", err)
	}
	_ = fd.Close()

	stat, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("failed to stat log file: %v", err)
	}
	if stat.Mode().Perm() != 0600 {
		t.Fatalf("expected log file permission 0600, got %04o", stat.Mode().Perm())
	}

	// 2. Existing file with relaxed permission (e.g. 0644) must be tightened to 0600 upon re-opening
	if err := os.Chmod(logPath, 0644); err != nil {
		t.Fatalf("failed to set 0644 on test file: %v", err)
	}
	fd2, err := openDaemonLogFile(logPath)
	if err != nil {
		t.Fatalf("openDaemonLogFile reopen failed: %v", err)
	}
	_ = fd2.Close()

	stat2, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("failed to stat log file after reopen: %v", err)
	}
	if stat2.Mode().Perm() != 0600 {
		t.Fatalf("reopened log file permission was not tightened to 0600: got %04o", stat2.Mode().Perm())
	}
}
