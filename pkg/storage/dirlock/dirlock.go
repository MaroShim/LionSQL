package dirlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ErrDirectoryLocked is returned when a data directory is already locked by another process.
var ErrDirectoryLocked = errors.New("directory is locked by another process")

// LockError provides detailed diagnostic information when a lock collision occurs.
type LockError struct {
	Dir      string
	Path     string
	OwnerPID int
}

func (e *LockError) Error() string {
	if e.OwnerPID > 0 {
		return fmt.Sprintf("data directory %q is locked by another active process (PID %d)", e.Dir, e.OwnerPID)
	}
	return fmt.Sprintf("data directory %q is locked by another active process", e.Dir)
}

func (e *LockError) Unwrap() error {
	return ErrDirectoryLocked
}

// LockFileName is the canonical lock file created inside the data directory.
const LockFileName = "lionsql.lock"

// DirLock maintains an exclusive OS-level advisory lock on a database data directory.
type DirLock struct {
	mu       sync.Mutex
	dir      string
	path     string
	file     *os.File
	refCount int
	closed   bool
}

var (
	registryMu sync.Mutex
	registry   = make(map[string]*DirLock) // canonical absolute dir -> *DirLock
)

// Acquire acquires a non-blocking exclusive OS-level lock on the specified data directory.
// If the directory is already locked by another OS process, it returns a *LockError wrapping ErrDirectoryLocked.
// Within the same process, repeated calls increment an internal reference counter, allowing cooperating modules
// (e.g. server coordinator and storage engine) within the same process to share the directory lock safely.
func Acquire(dataDir string) (*DirLock, error) {
	if dataDir == "" {
		return nil, nil // in-memory / ephemeral mode without physical data directory
	}
	cleanDir := filepath.Clean(dataDir)
	absDir, err := filepath.Abs(cleanDir)
	if err != nil {
		absDir = cleanDir
	}

	registryMu.Lock()
	defer registryMu.Unlock()

	// 1. In-process re-entrance check
	if existing, found := registry[absDir]; found && !existing.closed {
		existing.mu.Lock()
		existing.refCount++
		existing.mu.Unlock()
		return existing, nil
	}

	// 2. Ensure data directory exists
	if err := os.MkdirAll(absDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory for lock %q: %w", absDir, err)
	}

	lockPath := filepath.Join(absDir, LockFileName)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %q: %w", lockPath, err)
	}

	// 3. Acquire exclusive non-blocking OS lock
	if err := lockOSFile(file); err != nil {
		ownerPID := readPIDFromLockFile(file)
		_ = file.Close()
		if errors.Is(err, ErrDirectoryLocked) {
			return nil, &LockError{
				Dir:      cleanDir,
				Path:     lockPath,
				OwnerPID: ownerPID,
			}
		}
		return nil, fmt.Errorf("failed to acquire file lock on %q: %w", lockPath, err)
	}

	// 4. Record current process PID into lock file for diagnostics
	_ = file.Truncate(0)
	_, _ = file.Seek(0, 0)
	_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	_ = file.Sync()

	dl := &DirLock{
		dir:      absDir,
		path:     lockPath,
		file:     file,
		refCount: 1,
	}
	registry[absDir] = dl
	return dl, nil
}

// Release decrements the reference counter and, when it reaches zero, unlocks and closes the lock file.
func (l *DirLock) Release() error {
	if l == nil {
		return nil
	}
	registryMu.Lock()
	defer registryMu.Unlock()

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}
	l.refCount--
	if l.refCount > 0 {
		return nil
	}

	l.closed = true
	delete(registry, l.dir)

	var unlockErr, closeErr error
	if l.file != nil {
		unlockErr = unlockOSFile(l.file)
		closeErr = l.file.Close()
		l.file = nil
	}
	return errors.Join(unlockErr, closeErr)
}

// Dir returns the normalized directory path protected by this lock.
func (l *DirLock) Dir() string {
	if l == nil {
		return ""
	}
	return l.dir
}

// Path returns the lock file path.
func (l *DirLock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func readPIDFromLockFile(f *os.File) int {
	_, _ = f.Seek(0, 0)
	var buf [64]byte
	n, err := f.Read(buf[:])
	if err != nil || n == 0 {
		return 0
	}
	s := strings.TrimSpace(string(buf[:n]))
	pid, _ := strconv.Atoi(s)
	return pid
}
