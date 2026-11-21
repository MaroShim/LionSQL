package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunner_ShortExecution(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "spec_runner_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := Config{
		Duration:   300 * time.Millisecond,
		Workers:    1,
		MaxDepth:   2,
		OracleType: "norec",
		OutputDir:  filepath.Join(tempDir, "defects"),
		Verbose:    false,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if res.TotalQueries == 0 {
		t.Errorf("expected queries executed, got 0")
	}
	if res.TotalTables == 0 {
		t.Errorf("expected tables generated, got 0")
	}
}
