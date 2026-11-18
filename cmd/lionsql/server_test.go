package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDefaultDataDirFromExe(t *testing.T) {
	tests := []struct {
		name     string
		exePath  string
		expected string
	}{
		{
			name:     "binary located inside bin folder",
			exePath:  "/tmp/lionsql_app/bin/lionsql",
			expected: "/tmp/lionsql_app/data",
		},
		{
			name:     "binary located inside nested bin folder",
			exePath:  "/opt/lionsql/bin/lionsql",
			expected: "/opt/lionsql/data",
		},
		{
			name:     "binary located directly in root or other dir",
			exePath:  "/usr/local/lionsql",
			expected: "data",
		},
		{
			name:     "relative bin path",
			exePath:  filepath.Join("bin", "lionsql"),
			expected: "data",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := resolveDefaultDataDirFromExe(tc.exePath)
			if actual != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, actual)
			}
		})
	}
}

func TestOpenDaemonLogFile_PermissionsAndAutoDir(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "sub", "test_lionsql.log")

	fd, err := openDaemonLogFile(logPath)
	if err != nil {
		t.Fatalf("openDaemonLogFile failed: %v", err)
	}
	defer fd.Close()

	testMsg := "[LionSQL] Foreground Tee Log Verification"
	if _, err := fd.WriteString(testMsg + "\n"); err != nil {
		t.Fatalf("failed to write to log fd: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read created log file: %v", err)
	}
	if !strings.Contains(string(data), testMsg) {
		t.Errorf("expected log file to contain %q, got %q", testMsg, string(data))
	}
}
