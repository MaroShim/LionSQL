package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareBenchmarkDir_PreservesParentAndExistingFiles(t *testing.T) {
	parentDir, err := os.MkdirTemp("", "lionsql_benchdir_parent_*")
	if err != nil {
		t.Fatalf("failed to create temp parent: %v", err)
	}
	defer os.RemoveAll(parentDir)

	// Create a canary file inside the parent directory (simulating existing user DB files)
	canaryPath := filepath.Join(parentDir, "important_database.db")
	if err := os.WriteFile(canaryPath, []byte("vital_user_data"), 0644); err != nil {
		t.Fatalf("failed to create canary file: %v", err)
	}

	// Prepare benchmark directory inside parentDir
	subDir, cleanup, err := PrepareBenchmarkDir(parentDir, "wt_bench", false)
	if err != nil {
		t.Fatalf("unexpected error preparing benchmark dir: %v", err)
	}

	// Verify subDir is a child of parentDir
	rel, err := filepath.Rel(parentDir, subDir)
	if err != nil || rel == "." || rel == ".." {
		t.Fatalf("subDir %q must be a child of parentDir %q", subDir, parentDir)
	}

	// Verify subDir was created
	info, statErr := os.Stat(subDir)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("subDir %q was not created properly", subDir)
	}

	// Write benchmark files inside subDir
	benchFile := filepath.Join(subDir, "bench_table.wt")
	if err := os.WriteFile(benchFile, []byte("temporary_table"), 0644); err != nil {
		t.Fatalf("failed to write bench file: %v", err)
	}

	// Execute cleanup
	cleanup()

	// Verify subDir was deleted
	if _, err := os.Stat(subDir); !os.IsNotExist(err) {
		t.Fatalf("subDir %q was not deleted by cleanup", subDir)
	}

	// Verify parentDir and canary file remain 100% intact!
	if _, err := os.Stat(parentDir); err != nil {
		t.Fatalf("parentDir %q was unexpectedly deleted!", parentDir)
	}
	canaryData, readErr := os.ReadFile(canaryPath)
	if readErr != nil || string(canaryData) != "vital_user_data" {
		t.Fatalf("canary file was corrupted or deleted! readErr=%v, data=%q", readErr, string(canaryData))
	}
}

func TestPrepareBenchmarkDir_IsolatedConcurrentRuns(t *testing.T) {
	parentDir, err := os.MkdirTemp("", "lionsql_benchdir_concurrent_*")
	if err != nil {
		t.Fatalf("failed to create temp parent: %v", err)
	}
	defer os.RemoveAll(parentDir)

	// Run two concurrent/sequential prepares with the same parent
	dir1, cleanup1, err1 := PrepareBenchmarkDir(parentDir, "bench_run", false)
	if err1 != nil {
		t.Fatalf("failed to prepare dir1: %v", err1)
	}
	dir2, cleanup2, err2 := PrepareBenchmarkDir(parentDir, "bench_run", false)
	if err2 != nil {
		t.Fatalf("failed to prepare dir2: %v", err2)
	}

	if dir1 == dir2 {
		t.Fatalf("expected distinct subdirectories, got identical: %q", dir1)
	}

	// Clean up dir1, dir2 should still exist
	cleanup1()
	if _, err := os.Stat(dir1); !os.IsNotExist(err) {
		t.Fatalf("dir1 was not deleted")
	}
	if _, err := os.Stat(dir2); err != nil {
		t.Fatalf("dir2 was unexpectedly deleted when dir1 was cleaned up")
	}

	// Clean up dir2
	cleanup2()
	if _, err := os.Stat(dir2); !os.IsNotExist(err) {
		t.Fatalf("dir2 was not deleted")
	}
}

func TestPrepareBenchmarkDir_KeepDir(t *testing.T) {
	parentDir, err := os.MkdirTemp("", "lionsql_benchdir_keep_*")
	if err != nil {
		t.Fatalf("failed to create temp parent: %v", err)
	}
	defer os.RemoveAll(parentDir)

	subDir, cleanup, err := PrepareBenchmarkDir(parentDir, "bench_preserve", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cleanup()

	// subDir must still exist
	if _, err := os.Stat(subDir); err != nil {
		t.Fatalf("subDir was deleted despite keepDir=true: %v", err)
	}
}

func TestPrepareBenchmarkDir_AutomaticParentCreation(t *testing.T) {
	baseDir, err := os.MkdirTemp("", "lionsql_benchdir_base_*")
	if err != nil {
		t.Fatalf("failed to create temp base: %v", err)
	}
	defer os.RemoveAll(baseDir)

	deepParent := filepath.Join(baseDir, "nested", "level2", "bench_parent")
	subDir, cleanup, err := PrepareBenchmarkDir(deepParent, "run", false)
	if err != nil {
		t.Fatalf("failed to prepare deep parent benchmark: %v", err)
	}

	if _, err := os.Stat(subDir); err != nil {
		t.Fatalf("subDir not found: %v", err)
	}

	cleanup()

	if _, err := os.Stat(subDir); !os.IsNotExist(err) {
		t.Fatalf("subDir not deleted")
	}
}
