package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/config"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/storage/wiredtiger"
)

func TestDoctor_RunDiagnostics(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_doctor_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cfg := config.DefaultConfig()
	cfg.Server.DataDir = filepath.Join(tmpDir, "data")
	cfg.Network.HTTPAddr = "127.0.0.1:28090"
	cfg.Network.PGAddr = "127.0.0.1:25433"
	cfg.Network.TDSAddr = "127.0.0.1:21433"

	doc := NewDoctor(cfg)
	passed, warnings, failures := doc.RunDiagnostics()

	if failures > 0 {
		t.Errorf("Expected 0 failures in standard environment, got %d failures", failures)
	}
	if passed == 0 {
		t.Errorf("Expected positive passed checks, got 0")
	}

	t.Logf("Doctor results: %d passed, %d warnings, %d failures", passed, warnings, failures)
	doc.PrintReport()
}

func TestDoctor_RunDoctorFunc(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_doctor_func_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cfg := config.DefaultConfig()
	cfg.Server.DataDir = filepath.Join(tmpDir, "data")
	cfg.Network.HTTPAddr = "127.0.0.1:28091"
	cfg.Network.PGAddr = "127.0.0.1:25434"
	cfg.Network.TDSAddr = "127.0.0.1:21434"

	if err := RunDoctor(cfg); err != nil {
		t.Fatalf("RunDoctor failed: %v", err)
	}
}

type closeTrackingEngine struct {
	*memengine.Engine
	failCreate bool
	closeCount int
}

func (e *closeTrackingEngine) CreateTable(schema *storage.TableSchema) error {
	if e.failCreate {
		return fmt.Errorf("simulated CreateTable failure")
	}
	return e.Engine.CreateTable(schema)
}

func (e *closeTrackingEngine) Close() error {
	e.closeCount++
	return e.Engine.Close()
}

func TestP1_Batch6_DoctorCloseOnErrorTerminalRestoreAndTimerCleanup(t *testing.T) {
	t.Run("Scope_132_DoctorClosesEngineOnProbeError", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "lionsql_scope132_*")
		if err != nil {
			t.Fatalf("MkdirTemp failed: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		eng := &closeTrackingEngine{Engine: memengine.New(), failCreate: true}
		step, pErr := probeStorageEngine(eng, tmpDir, "__doctor_occ_probe__", "val", "probe", 1)
		if pErr == nil {
			t.Fatalf("expected probeStorageEngine to fail on simulated CreateTable error")
		}
		if step != "CreateTable" {
			t.Fatalf("expected failure at CreateTable, got %q (%v)", step, pErr)
		}
		if eng.closeCount == 0 {
			t.Fatalf("expected eng.Close() to be called even when probeStorageEngine fails after Open, got closeCount=%d", eng.closeCount)
		}
	})

	t.Run("Scope_133_PasswordInterruptRestoresTerminalAndDaemonLockHint", func(t *testing.T) {
		sigCh := make(chan os.Signal, 1)
		restored := false
		restoreFn := func() {
			restored = true
		}

		blockCh := make(chan struct{})
		readFn := func(fd int) ([]byte, error) {
			sigCh <- os.Interrupt
			<-blockCh
			return []byte("secret"), nil
		}

		doneCh := make(chan error, 1)
		go func() {
			_, err := readPasswordInteractive(0, readFn, restoreFn, sigCh)
			doneCh <- err
		}()

		select {
		case err := <-doneCh:
			close(blockCh)
			if err == nil {
				t.Fatalf("expected error on interrupt during password prompt, got nil")
			}
			if !restored {
				t.Fatalf("expected terminal state restoreFn to be invoked on interrupt")
			}
		case <-time.After(300 * time.Millisecond):
			close(blockCh)
			t.Fatalf("readPasswordInteractive did not abort and restore terminal on SIGINT")
		}

		// Also verify openCoordinator gives a clear hint when a daemon PID is active in dataDir
		tmpDir, err := os.MkdirTemp("", "lionsql_scope133_sql_*")
		if err != nil {
			t.Fatalf("MkdirTemp failed: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		// Write current process PID to lionsql.pid to simulate an active daemon, and lock WiredTiger
		pidPath := filepath.Join(tmpDir, "lionsql.pid")
		_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644)

		wtHold := wiredtiger.New()
		if err := wtHold.Open(tmpDir); err == nil {
			defer func() { _ = wtHold.Close() }()
			_, _, openErr := openCoordinator(tmpDir, "wiredtiger")
			if openErr == nil || !strings.Contains(openErr.Error(), "daemon is currently running") {
				t.Fatalf("expected openCoordinator error to mention active daemon hint, got: %v", openErr)
			}
		}
	})

	t.Run("Scope_131_PlatformTimerCleanupIdempotentOnce", func(t *testing.T) {
		callCount := 0
		platformTimerCleanupHook = func() {
			callCount++
		}
		defer func() { platformTimerCleanupHook = nil }()

		cleanupPlatformTimer()
		cleanupPlatformTimer()
		if callCount != 1 {
			t.Fatalf("expected cleanupPlatformTimer to invoke cleanup hook exactly once via sync.Once, got %d", callCount)
		}
	})
}
