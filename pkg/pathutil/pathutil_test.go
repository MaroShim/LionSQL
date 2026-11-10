package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestValidateDir_ValidAndAutoCreate(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_pathutil_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Existing directory should pass
	if err := ValidateDir(tmpDir, false); err != nil {
		t.Fatalf("expected existing dir to pass, got: %v", err)
	}

	// Non-existing directory with autoCreate=true should create and pass
	subDir := filepath.Join(tmpDir, "auto_sub", "nested")
	if err := ValidateDir(subDir, true); err != nil {
		t.Fatalf("expected autoCreate to succeed, got: %v", err)
	}
	if fi, err := os.Stat(subDir); err != nil || !fi.IsDir() {
		t.Fatalf("expected subDir to exist as directory")
	}

	// Non-existing directory with autoCreate=false should fail
	nonExistent := filepath.Join(tmpDir, "does_not_exist")
	if err := ValidateDir(nonExistent, false); err == nil {
		t.Fatalf("expected error for non-existent dir with autoCreate=false")
	}
}

func TestValidateDir_RejectsRegularFileAndDevice(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_pathutil_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Regular file passed as directory must be rejected
	regFile := filepath.Join(tmpDir, "regular.txt")
	if err := os.WriteFile(regFile, []byte("data"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	if err := ValidateDir(regFile, false); err == nil {
		t.Fatalf("expected regular file to be rejected as directory")
	}

	// Device node (/dev/null on Unix) must be rejected
	if runtime.GOOS != "windows" {
		if err := ValidateDir("/dev/null", false); err == nil {
			t.Fatalf("expected /dev/null to be rejected as directory")
		}
	}
}

func TestValidateDir_RejectsNamedPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping FIFO test on Windows")
	}
	tmpDir, err := os.MkdirTemp("", "lionsql_pathutil_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	fifoPath := filepath.Join(tmpDir, "test.fifo")
	if err := syscall.Mkfifo(fifoPath, 0666); err != nil {
		t.Skipf("cannot create FIFO: %v", err)
	}

	if err := ValidateDir(fifoPath, false); err == nil {
		t.Fatalf("expected named pipe (FIFO) to be rejected as directory")
	}
}

func TestValidateDir_RejectsSymlinkLoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping symlink loop test on Windows")
	}
	tmpDir, err := os.MkdirTemp("", "lionsql_pathutil_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	link1 := filepath.Join(tmpDir, "link1")
	link2 := filepath.Join(tmpDir, "link2")
	_ = os.Symlink(link1, link2)
	_ = os.Symlink(link2, link1)

	if err := ValidateDir(link1, false); err == nil {
		t.Fatalf("expected symlink loop to be rejected")
	}
}

func TestValidateRegularFile_ValidAndRejections(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_pathutil_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Directory passed where file is expected must be rejected
	if err := ValidateRegularFile(tmpDir, true); err == nil {
		t.Fatalf("expected directory to be rejected as regular file")
	}

	// Existing regular file must pass
	filePath := filepath.Join(tmpDir, "test.json")
	if err := os.WriteFile(filePath, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if err := ValidateRegularFile(filePath, true); err != nil {
		t.Fatalf("expected regular file to pass, got: %v", err)
	}

	// Future output file whose parent dir exists must pass with mustExist=false
	newOutput := filepath.Join(tmpDir, "output.log")
	if err := ValidateRegularFile(newOutput, false); err != nil {
		t.Fatalf("expected valid new output file to pass, got: %v", err)
	}

	// Device node (/dev/null) must be rejected
	if runtime.GOOS != "windows" {
		if err := ValidateRegularFile("/dev/null", true); err == nil {
			t.Fatalf("expected /dev/null device to be rejected as regular file")
		}
	}
}
