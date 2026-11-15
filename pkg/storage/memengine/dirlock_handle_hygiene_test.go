package memengine_test

import (
	"os"
	"path/filepath"
	"testing"

	"lionsql/pkg/storage/memengine"
)

// TestMemEngine_InitializationFailureCleansUpWAL verifies that if OpenWithOptions encounters
// an error during startup/replay, all opened resources (WAL writer file handles, directory locks)
// are deterministically cleaned up and closed ([RULE:HandleHygiene], [RULE:DoubleCloseSafety]).
func TestMemEngine_InitializationFailureCleansUpWAL(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "memengine_wal_cleanup_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create an intentionally corrupt WAL file (unsupported magic/frame corruption)
	aofPath := filepath.Join(tempDir, "wal.aof")
	corruptData := []byte("INVALID_CORRUPTED_WAL_FRAME_DATA_THAT_FAILS_REPLAY")
	if err := os.WriteFile(aofPath, corruptData, 0644); err != nil {
		t.Fatalf("failed to write corrupt WAL: %v", err)
	}

	eng := memengine.New()
	opts := memengine.DefaultOptions(tempDir)

	// OpenWithOptions MUST fail due to WAL replay error
	openErr := eng.OpenWithOptions(opts)
	if openErr == nil {
		eng.Close()
		t.Fatalf("expected OpenWithOptions to fail on corrupt WAL, but got nil")
	}

	// Verify that the directory and files are not held open by any leaked descriptors:
	// We should be able to remove the directory without EBUSY / Sharing Violation
	if err := os.RemoveAll(tempDir); err != nil {
		t.Fatalf("failed to clean up tempDir after OpenWithOptions failure (possible leaked file handle): %v", err)
	}
}
