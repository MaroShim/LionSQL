package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	// DefaultMaxLogFileSize caps log files at 5MB per engineering guidelines (Rule 40).
	DefaultMaxLogFileSize int64 = 5 * 1024 * 1024
	// MaxRotatedBackups specifies maximum rotated historical log files to retain.
	MaxRotatedBackups = 3
)

// RotatingLogWriter manages thread-safe, bounded append-only log files with automatic rolling.
type RotatingLogWriter struct {
	mu          sync.Mutex
	filePath    string
	maxBytes    int64
	currentSize int64
	file        *os.File
	closed      bool
	closeOnce   sync.Once
}

// NewRotatingLogWriter creates a new RotatingLogWriter for target file.
func NewRotatingLogWriter(filePath string, maxBytes int64) (*RotatingLogWriter, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxLogFileSize
	}
	cleanPath := filepath.Clean(filePath)
	dir := filepath.Dir(cleanPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}
	}

	w := &RotatingLogWriter{
		filePath: cleanPath,
		maxBytes: maxBytes,
	}

	if err := w.openFile(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingLogWriter) openFile() error {
	f, err := os.OpenFile(w.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file %q: %w", w.filePath, err)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to stat log file %q: %w", w.filePath, err)
	}
	w.file = f
	w.currentSize = fi.Size()
	return nil
}

// FilePath returns the clean path of the managed log file.
func (w *RotatingLogWriter) FilePath() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.filePath
}

// WriteLine serializes any payload to JSON and appends a single newline-delimited record.
func (w *RotatingLogWriter) WriteLine(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}
	data = append(data, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return os.ErrClosed
	}

	if w.file == nil {
		if err := w.openFile(); err != nil {
			return err
		}
	}

	entryLen := int64(len(data))
	if w.currentSize+entryLen > w.maxBytes {
		if err := w.rotate(); err != nil {
			return err
		}
	}

	n, writeErr := w.file.Write(data)
	w.currentSize += int64(n)
	return writeErr
}

// rotate rolls log files up to MaxRotatedBackups (.1, .2, .3).
func (w *RotatingLogWriter) rotate() error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}

	for i := MaxRotatedBackups - 1; i >= 1; i-- {
		oldPath := fmt.Sprintf("%s.%d", w.filePath, i)
		newPath := fmt.Sprintf("%s.%d", w.filePath, i+1)
		if _, err := os.Stat(oldPath); err == nil {
			_ = os.Rename(oldPath, newPath)
		}
	}

	backupPath := fmt.Sprintf("%s.1", w.filePath)
	_ = os.Rename(w.filePath, backupPath)

	f, err := os.OpenFile(w.filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create rotated log file %q: %w", w.filePath, err)
	}
	w.file = f
	w.currentSize = 0
	return nil
}

// Close gracefully closes the underlying file handle.
func (w *RotatingLogWriter) Close() error {
	var err error
	w.closeOnce.Do(func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.closed = true
		if w.file != nil {
			err = w.file.Close()
			w.file = nil
		}
	})
	return err
}
