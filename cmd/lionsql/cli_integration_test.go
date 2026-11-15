package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_IsCLICall(t *testing.T) {
	tests := []struct {
		args     []string
		expected bool
	}{
		{[]string{"-i"}, true},
		{[]string{"--interactive"}, true},
		{[]string{"-shell"}, true},
		{[]string{"--shell"}, true},
		{[]string{"-e", "SELECT 1;"}, true},
		{[]string{"--execute", "SELECT 1;"}, true},
		{[]string{"-e=SELECT 1;"}, true},
		{[]string{"-f", "test.sql"}, true},
		{[]string{"--file=test.sql"}, true},
		{[]string{"-dir=/tmp", "-e", "SELECT 1;"}, true},
		{[]string{"-engine=memory", "-i"}, true},
		{[]string{}, false},
		{[]string{"-http=:8090"}, false},
		{[]string{"-dir=/tmp/data"}, false},
		{[]string{"serve"}, false},
		{[]string{"start"}, false},
		{[]string{"status"}, false},
	}

	for _, tc := range tests {
		got := isCLICall(tc.args)
		if got != tc.expected {
			t.Errorf("isCLICall(%v) = %v, expected %v", tc.args, got, tc.expected)
		}
	}
}

func TestCLI_RunCLI_ExecuteFlag(t *testing.T) {
	tempDir := t.TempDir()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runCLI([]string{"-dir=" + tempDir, "-engine=memory", "-e", "CREATE TABLE users (id INT, name VARCHAR(50)); INSERT INTO users VALUES (1, 'Alice'); SELECT * FROM users;"})

	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runCLI failed: %v", err)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "id") || !strings.Contains(output, "name") {
		t.Errorf("expected columns id and name in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Alice") {
		t.Errorf("expected 'Alice' in output, got:\n%s", output)
	}
}

func TestCLI_RunCLI_FileFlag(t *testing.T) {
	tempDir := t.TempDir()
	sqlFile := filepath.Join(tempDir, "script.sql")
	sqlContent := "CREATE TABLE items (id INT, item_name VARCHAR(100));\nINSERT INTO items VALUES (10, 'Gadget');\nSELECT * FROM items;"
	if err := os.WriteFile(sqlFile, []byte(sqlContent), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runCLI([]string{"-dir=" + tempDir, "-engine=memory", "-f", sqlFile})

	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runCLI failed: %v", err)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "Gadget") {
		t.Errorf("expected 'Gadget' in output, got:\n%s", output)
	}
}

func TestCLI_RunCLI_InteractiveExit(t *testing.T) {
	tempDir := t.TempDir()

	// Feed "exit\n" via stdin pipe
	inR, inW, _ := os.Pipe()
	_, _ = inW.WriteString("exit\n")
	inW.Close()

	oldStdin := os.Stdin
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()

	os.Stdin = inR
	os.Stdout = w

	err := runCLI([]string{"-dir=" + tempDir, "-engine=memory", "-i"})

	w.Close()
	os.Stdin = oldStdin
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runCLI interactive failed: %v", err)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "LionSQL Interactive Shell") {
		t.Errorf("expected welcome banner in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Bye") {
		t.Errorf("expected 'Bye' in output, got:\n%s", output)
	}
}
