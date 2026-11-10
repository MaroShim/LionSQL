package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ValidateDir verifies that dir is a safe, valid directory and not a non-regular file
// (FIFO named pipe, socket, device node, regular file, or cyclic symlink) ([RULE:SymlinkSafety], [RULE:PathSecurity]).
// If autoCreate is true and the directory does not exist, it creates it with 0755 permissions.
func ValidateDir(dir string, autoCreate bool) error {
	clean := filepath.Clean(dir)
	fi, err := os.Lstat(clean)
	if err != nil {
		if os.IsNotExist(err) {
			if !autoCreate {
				return fmt.Errorf("directory does not exist: %s", clean)
			}
			if err := os.MkdirAll(clean, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", clean, err)
			}
			return nil
		}
		return fmt.Errorf("inaccessible directory path %s: %w", clean, err)
	}

	// Resolve symlinks defensively, detecting loops
	if fi.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(clean)
		if err != nil {
			return fmt.Errorf("symlink resolution failed for %s: %w", clean, err)
		}
		fi, err = os.Stat(resolved)
		if err != nil {
			return fmt.Errorf("symlink target %s is inaccessible: %w", resolved, err)
		}
		clean = resolved
	}

	mode := fi.Mode()
	if !fi.IsDir() {
		if mode&os.ModeNamedPipe != 0 {
			return fmt.Errorf("path %s is a named pipe (FIFO); a directory is required", clean)
		}
		if mode&os.ModeSocket != 0 {
			return fmt.Errorf("path %s is a socket; a directory is required", clean)
		}
		if mode&os.ModeDevice != 0 {
			return fmt.Errorf("path %s is a device node; a directory is required", clean)
		}
		return fmt.Errorf("path %s is a regular file, not a directory", clean)
	}

	// Verify directory writeability by probing with a temporary file
	probeFile := filepath.Join(clean, fmt.Sprintf(".lionsql_probe_%d", time.Now().UnixNano()))
	f, err := os.OpenFile(probeFile, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("directory %s is not writeable: %w", clean, err)
	}
	_ = f.Close()
	_ = os.Remove(probeFile)

	return nil
}

// ValidateRegularFile verifies that targetPath is a safe regular file (or can be written to)
// and rejects directories, FIFO named pipes, sockets, device nodes, or cyclic symlinks ([RULE:SymlinkSafety]).
// If mustExist is true, the file must physically exist and be a regular file.
// If mustExist is false, the file's parent directory must be a valid writeable directory, and if the file already
// exists, it must be a regular file.
func ValidateRegularFile(targetPath string, mustExist bool) error {
	clean := filepath.Clean(targetPath)
	fi, err := os.Lstat(clean)
	if err != nil {
		if os.IsNotExist(err) {
			if mustExist {
				return fmt.Errorf("file does not exist: %s", clean)
			}
			// Verify parent directory exists and is writeable
			parentDir := filepath.Dir(clean)
			return ValidateDir(parentDir, false)
		}
		return fmt.Errorf("inaccessible file path %s: %w", clean, err)
	}

	// Resolve symlinks defensively
	if fi.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(clean)
		if err != nil {
			return fmt.Errorf("symlink resolution failed for %s: %w", clean, err)
		}
		fi, err = os.Stat(resolved)
		if err != nil {
			return fmt.Errorf("symlink target %s is inaccessible: %w", resolved, err)
		}
		clean = resolved
	}

	mode := fi.Mode()
	if fi.IsDir() {
		return fmt.Errorf("path %s is a directory; a regular file is required", clean)
	}
	if mode&os.ModeNamedPipe != 0 {
		return fmt.Errorf("path %s is a named pipe (FIFO); a regular file is required", clean)
	}
	if mode&os.ModeSocket != 0 {
		return fmt.Errorf("path %s is a socket; a regular file is required", clean)
	}
	if mode&os.ModeDevice != 0 {
		return fmt.Errorf("path %s is a device node; a regular file is required", clean)
	}
	if !mode.IsRegular() {
		return fmt.Errorf("path %s is not a regular file (mode: %v)", clean, mode)
	}

	return nil
}
