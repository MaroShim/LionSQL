package memengine_test

import (
	"os"
	"path/filepath"
	"strings"
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

// TestMemEngine_CorruptedRDBSnapshotFailsClosed verifies that if tables.mpk exists
// on disk but is corrupted or truncated, OpenWithOptions fails closed with an explicit error
// instead of silently booting with an empty database and risking overwriting user data ([RULE:ExplicitError], [RULE:AtomicSaves]).
func TestMemEngine_CorruptedRDBSnapshotFailsClosed(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "memengine_corrupt_rdb_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 1. Corrupted MsgPack payload in tables.mpk
	mpkPath := filepath.Join(tempDir, "tables.mpk")
	if err := os.WriteFile(mpkPath, []byte{0xff, 0x00, 0xde, 0xad, 0xbe, 0xef}, 0644); err != nil {
		t.Fatalf("failed to write corrupt tables.mpk: %v", err)
	}

	eng := memengine.New()
	opts := memengine.DefaultOptions(tempDir)

	openErr := eng.OpenWithOptions(opts)
	if openErr == nil {
		eng.Close()
		t.Fatalf("expected OpenWithOptions to fail on corrupted tables.mpk, but got nil")
	}
	if !strings.Contains(openErr.Error(), "RDB snapshot load error") {
		t.Errorf("expected error to mention 'RDB snapshot load error', got: %v", openErr)
	}

	// 2. Empty (0-byte truncated) tables.mpk
	if err := os.WriteFile(mpkPath, []byte{}, 0644); err != nil {
		t.Fatalf("failed to write empty tables.mpk: %v", err)
	}
	openErr = eng.OpenWithOptions(opts)
	if openErr == nil {
		eng.Close()
		t.Fatalf("expected OpenWithOptions to fail on 0-byte tables.mpk, but got nil")
	}
	if !strings.Contains(openErr.Error(), "empty or truncated") {
		t.Errorf("expected error to mention 'empty or truncated', got: %v", openErr)
	}
}
