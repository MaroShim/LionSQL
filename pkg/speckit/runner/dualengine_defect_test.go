package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lionsql/pkg/speckit/oracle"
)

func TestDEFECT_Runner_DualEngineOracleOption(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "spec_runner_dual_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := Config{
		Duration:   400 * time.Millisecond,
		Workers:    1,
		MaxDepth:   2,
		OracleType: "dualengine",
		OutputDir:  filepath.Join(tempDir, "defects"),
		Verbose:    false,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run with OracleType dualengine failed: %v", err)
	}

	if res.OracleExecuted != oracle.DiscrepancyDualEngine {
		t.Fatalf("[DEFECT-SPEC-FUZZ-ORACLE-FLAG-IGNORE-1] expected OracleExecuted to be %q, got %q", oracle.DiscrepancyDualEngine, res.OracleExecuted)
	}
}
