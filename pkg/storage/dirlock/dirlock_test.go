package dirlock_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"lionsql/pkg/storage/dirlock"
)

func TestDirLock_BasicAcquireAndRelease(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dirlock_basic_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 1. First acquire should succeed
	lock, err := dirlock.Acquire(tempDir)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	if lock == nil {
		t.Fatalf("expected non-nil lock")
	}

	// Lock file should exist
	lockPath := filepath.Join(tempDir, dirlock.LockFileName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected lock file to exist at %s: %v", lockPath, err)
	}

	// 2. In-process re-entrance should succeed with reference count increment
	lock2, err := dirlock.Acquire(tempDir)
	if err != nil {
		t.Fatalf("in-process re-entrant Acquire failed: %v", err)
	}
	if lock2 != lock {
		t.Errorf("expected re-entrant acquire to return same instance, got %p vs %p", lock2, lock)
	}

	// 3. First release decrements refCount
	if err := lock2.Release(); err != nil {
		t.Fatalf("first Release failed: %v", err)
	}

	// 4. Second release frees OS lock completely
	if err := lock.Release(); err != nil {
		t.Fatalf("second Release failed: %v", err)
	}

	// 5. Repeated release is a safe no-op ([RULE:DoubleCloseSafety])
	if err := lock.Release(); err != nil {
		t.Fatalf("double release failed: %v", err)
	}

	// 6. Should be re-acquirable after complete release
	lock3, err := dirlock.Acquire(tempDir)
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	_ = lock3.Release()
}

func TestDirLock_EmptyDirIsNoop(t *testing.T) {
	lock, err := dirlock.Acquire("")
	if err != nil {
		t.Fatalf("Acquire(\"\") should not return error: %v", err)
	}
	if lock != nil {
		t.Fatalf("Acquire(\"\") should return nil lock")
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release on nil lock should be no-op: %v", err)
	}
}

// TestDirLock_HelperProcess is invoked as a subprocess to hold a directory lock.
func TestDirLock_HelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DIRLOCK_HELPER") != "1" {
		return
	}
	targetDir := os.Getenv("DIRLOCK_TARGET_DIR")
	if targetDir == "" {
		os.Exit(2)
	}

	lock, err := dirlock.Acquire(targetDir)
	if err != nil {
		os.Exit(1)
	}
	defer lock.Release()

	// Signal to parent that lock is acquired by writing to stdout
	os.Stdout.WriteString("LOCKED\n")

	// Sleep until parent terminates this helper
	time.Sleep(10 * time.Second)
}

// TestDirLock_ConcurrentProcessMutualExclusion verifies that two OS processes
// running almost simultaneously on the same data directory cannot both succeed:
// exactly one process holds the lock, and the other is immediately rejected with ErrDirectoryLocked.
func TestDirLock_ConcurrentProcessMutualExclusion(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dirlock_process_mutex_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 1. Launch subprocess 1 holding the lock
	cmd := exec.Command(os.Args[0], "-test.run=TestDirLock_HelperProcess")
	cmd.Env = append(os.Environ(), "GO_WANT_DIRLOCK_HELPER=1", "DIRLOCK_TARGET_DIR="+tempDir)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe failed: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start helper process: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	// Wait until subprocess signals it holds the lock
	buf := make([]byte, 7)
	_, _ = stdout.Read(buf)

	// 2. Parent process now attempts to acquire the lock on the same directory
	// It MUST be rejected with ErrDirectoryLocked!
	_, err = dirlock.Acquire(tempDir)
	if err == nil {
		t.Fatalf("expected Acquire to fail when directory is locked by subprocess, but it succeeded!")
	}
	if !errors.Is(err, dirlock.ErrDirectoryLocked) {
		t.Fatalf("expected ErrDirectoryLocked, got: %v", err)
	}

	// 3. Terminate subprocess
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	// Small settle time for kernel to release file lock upon process exit
	time.Sleep(50 * time.Millisecond)

	// 4. Now parent process acquire MUST succeed!
	lock, err := dirlock.Acquire(tempDir)
	if err != nil {
		t.Fatalf("Acquire should succeed after subprocess exit: %v", err)
	}
	_ = lock.Release()
}
