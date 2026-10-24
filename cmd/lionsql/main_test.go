package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureStdout(fn func() error) (string, error) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := fn()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String(), err
}

func TestLionsql_MigrateNew(t *testing.T) {
	tempDir := t.TempDir()

	err := runMigrateNew([]string{"-dir=" + tempDir, "create_posts_table"})
	if err != nil {
		t.Fatalf("runMigrateNew failed: %v", err)
	}

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Expected 2 files (.up.sql, .down.sql), got %d", len(entries))
	}

	hasUp, hasDown := false, false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			hasUp = true
		}
		if strings.HasSuffix(e.Name(), ".down.sql") {
			hasDown = true
		}
	}
	if !hasUp || !hasDown {
		t.Fatalf("Missing up or down migration file")
	}
}

func TestLionsql_MigrateLifecycle(t *testing.T) {
	tempMigrations := t.TempDir()
	tempData := t.TempDir()

	// 1. Create a migration
	err := runMigrateNew([]string{"-dir=" + tempMigrations, "create_items"})
	if err != nil {
		t.Fatalf("Create migration failed: %v", err)
	}

	entries, _ := os.ReadDir(tempMigrations)
	var upFile, downFile string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			upFile = filepath.Join(tempMigrations, e.Name())
		}
		if strings.HasSuffix(e.Name(), ".down.sql") {
			downFile = filepath.Join(tempMigrations, e.Name())
		}
	}

	// Fill migration SQL
	_ = os.WriteFile(upFile, []byte("CREATE TABLE items (id INT PRIMARY KEY, title VARCHAR(50));\nINSERT INTO items VALUES (1, 'Book');"), 0644)
	_ = os.WriteFile(downFile, []byte("DROP TABLE items;"), 0644)

	// 2. Check Status (Pending)
	out, err := captureStdout(func() error {
		return runMigrateStatus([]string{
			"-dir=" + tempMigrations,
			"-data=" + tempData,
			"-engine=memory",
		})
	})
	if err != nil {
		t.Fatalf("runMigrateStatus failed: %v", err)
	}
	if !strings.Contains(out, "Pending") || !strings.Contains(out, "create_items") {
		t.Fatalf("Expected status output to show Pending create_items, got: %s", out)
	}

	// 3. Apply Up
	out, err = captureStdout(func() error {
		return runMigrateUp([]string{
			"-dir=" + tempMigrations,
			"-data=" + tempData,
			"-engine=memory",
		})
	})
	if err != nil {
		t.Fatalf("runMigrateUp failed: %v", err)
	}
	if !strings.Contains(out, "Successfully applied 1 migration") {
		t.Fatalf("Expected success output, got: %s", out)
	}

	// 4. Check Status (Applied)
	out, err = captureStdout(func() error {
		return runMigrateStatus([]string{
			"-dir=" + tempMigrations,
			"-data=" + tempData,
			"-engine=memory",
		})
	})
	if err != nil {
		t.Fatalf("runMigrateStatus failed: %v", err)
	}
	if !strings.Contains(out, "Applied") {
		t.Fatalf("Expected status output to show Applied, got: %s", out)
	}

	// 5. Test SQL query against created table
	out, err = captureStdout(func() error {
		return runSQL([]string{
			"-data=" + tempData,
			"-engine=memory",
			"SELECT id, title FROM items WHERE id = 1;",
		})
	})
	if err != nil {
		t.Fatalf("runSQL failed: %v", err)
	}
	if !strings.Contains(out, "Book") {
		t.Fatalf("Expected SQL output to contain Book, got: %s", out)
	}

	// 6. Rollback Down
	out, err = captureStdout(func() error {
		return runMigrateDown([]string{
			"-dir=" + tempMigrations,
			"-data=" + tempData,
			"-engine=memory",
			"-steps=1",
		})
	})
	if err != nil {
		t.Fatalf("runMigrateDown failed: %v", err)
	}
	if !strings.Contains(out, "Successfully rolled back 1 migration") {
		t.Fatalf("Expected rollback output, got: %s", out)
	}

	// 7. Check Status (Pending again)
	out, err = captureStdout(func() error {
		return runMigrateStatus([]string{
			"-dir=" + tempMigrations,
			"-data=" + tempData,
			"-engine=memory",
		})
	})
	if err != nil {
		t.Fatalf("runMigrateStatus after rollback failed: %v", err)
	}
	if !strings.Contains(out, "Pending") {
		t.Fatalf("Expected status Pending after rollback, got: %s", out)
	}
}

func TestLionsql_ErrorHandling(t *testing.T) {
	// Missing name on new
	err := runMigrateNew([]string{})
	if err == nil {
		t.Fatalf("Expected error when migration name missing")
	}

	// Unknown subcommand
	err = runMigrate([]string{"unknown_cmd"})
	if err == nil {
		t.Fatalf("Expected error for unknown subcommand")
	}

	// Empty query on sql
	err = runSQL([]string{})
	if err == nil {
		t.Fatalf("Expected error for empty SQL query")
	}
}

func TestLionsql_VersionOutput(t *testing.T) {
	out, err := captureStdout(func() error {
		printVersion()
		return nil
	})
	if err != nil {
		t.Fatalf("printVersion returned error: %v", err)
	}
	expectedStrings := []string{
		"LionSQL v0.90.0",
		"Build Time:",
		"Git Commit:",
		"Go Runtime:",
		"Storage Engine:",
		"Wire Protocols:",
	}
	for _, expected := range expectedStrings {
		if !strings.Contains(out, expected) {
			t.Errorf("expected version output to contain %q, got:\n%s", expected, out)
		}
	}

	// Test -v and -version in runServer
	for _, flagArg := range []string{"-v", "-version", "--version"} {
		out, err = captureStdout(func() error {
			return runServer([]string{flagArg})
		})
		if err != nil {
			t.Fatalf("runServer(%q) returned error: %v", flagArg, err)
		}
		if !strings.Contains(out, "LionSQL v0.90.0") {
			t.Errorf("expected runServer(%q) output to contain 'LionSQL v0.90.0', got:\n%s", flagArg, out)
		}
	}
}

func TestLionsql_UsageOutput(t *testing.T) {
	out, err := captureStdout(func() error {
		printUsage()
		return nil
	})
	if err != nil {
		t.Fatalf("printUsage returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines in usage header, got %d lines", len(lines))
	}
	if !strings.Contains(lines[0], "LionSQL - Single binary High-Concurrency OLTP Database Server with T-SQL & Stored Procedure (v0.90.0)") {
		t.Errorf("expected line 1 to contain title and version, got: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "(built: ") || !strings.Contains(lines[1], "commit: ") {
		t.Errorf("expected line 2 to contain compact build info, got: %q", lines[1])
	}
}
