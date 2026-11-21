//go:build windows

package wiredtiger

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	procSetDllDirectory = kernel32.NewProc("SetDllDirectoryW")

	dllLoadOnce sync.Once
	dllLoadErr  error
)

// setDllDirectory wraps the Windows SetDllDirectoryW API to add a directory to the DLL search path.
func setDllDirectory(dir string) error {
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	r1, _, err := procSetDllDirectory.Call(uintptr(unsafe.Pointer(p)))
	if r1 == 0 {
		return err
	}
	return nil
}

// EnsureWiredTigerDLL verifies that wiredtiger.dll is accessible in the search path on Windows.
// If missing on disk but embedded in the binary, it atomically extracts the DLL to %LOCALAPPDATA%\LionSQL\bin\.
func EnsureWiredTigerDLL() error {
	dllLoadOnce.Do(func() {
		// 1. Check if wiredtiger.dll is placed alongside the running executable
		if exePath, err := os.Executable(); err == nil {
			exeDir := filepath.Dir(exePath)
			if _, err := os.Stat(filepath.Join(exeDir, "wiredtiger.dll")); err == nil {
				_ = setDllDirectory(exeDir)
				return
			}
		}

		// 2. Check current working directory
		if cwd, err := os.Getwd(); err == nil {
			if _, err := os.Stat(filepath.Join(cwd, "wiredtiger.dll")); err == nil {
				_ = setDllDirectory(cwd)
				return
			}
		}

		// 3. Check persistent location (%LOCALAPPDATA%\LionSQL\bin\wiredtiger.dll)
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = os.TempDir()
		}
		targetDir := filepath.Join(localAppData, "LionSQL", "bin")
		targetDLL := filepath.Join(targetDir, "wiredtiger.dll")

		if _, err := os.Stat(targetDLL); err == nil {
			_ = setDllDirectory(targetDir)
			return
		}

		// 4. Atomically extract embedded DLL if available ([RULE:AtomicSaves])
		if len(embeddedWiredTigerDLL) > 0 {
			if err := os.MkdirAll(targetDir, 0755); err != nil {
				dllLoadErr = err
				return
			}
			tmpFile := targetDLL + ".tmp"
			if err := os.WriteFile(tmpFile, embeddedWiredTigerDLL, 0644); err != nil {
				dllLoadErr = err
				return
			}
			if err := os.Rename(tmpFile, targetDLL); err != nil {
				dllLoadErr = err
				return
			}
			_ = setDllDirectory(targetDir)
		}
	})
	return dllLoadErr
}

func init() {
	_ = EnsureWiredTigerDLL()
}
