package memengine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"sqlion/pkg/storage"
)

// FsyncPolicy defines the disk synchronization frequency for AOF logs.
type FsyncPolicy int

const (
	// FsyncEverySec syncs the AOF file every second in the background (Redis default, recommended).
	FsyncEverySec FsyncPolicy = iota
	// FsyncAlways syncs the AOF file synchronously on every commit (Zero data loss).
	FsyncAlways
	// FsyncNone lets the operating system handle disk flushing (Maximum throughput).
	FsyncNone
)

// WALOpType represents the type of mutation stored in the WAL.
type WALOpType string

const (
	OpCreateTable WALOpType = "CREATE_TABLE"
	OpDropTable   WALOpType = "DROP_TABLE"
	OpCreateIndex WALOpType = "CREATE_INDEX"
	OpDropIndex   WALOpType = "DROP_INDEX"
	OpInsert      WALOpType = "INSERT"
	OpUpdate      WALOpType = "UPDATE"
	OpDelete      WALOpType = "DELETE"
)

// WALOp represents a single atomic row or DDL mutation.
type WALOp struct {
	Type        WALOpType            `json:"type"`
	Table       string               `json:"table"`
	Key         string               `json:"key,omitempty"`
	Record      storage.Record       `json:"record,omitempty"`
	BaseVer     int64                `json:"base_ver,omitempty"`
	Schema      *storage.TableSchema `json:"schema,omitempty"`
	IndexSchema *storage.IndexSchema `json:"index_schema,omitempty"`
	IndexName   string               `json:"index_name,omitempty"`
}

// WALRecord represents a committed transaction batch in the AOF log.
type WALRecord struct {
	LSN       int64   `json:"lsn"`
	TxID      int64   `json:"tx_id"`
	Timestamp int64   `json:"ts"`
	Ops       []WALOp `json:"ops"`
}

// WALWriter handles thread-safe append-only logging with configurable fsync policies.
type WALWriter struct {
	mu         sync.Mutex
	file       *os.File
	path       string
	policy     FsyncPolicy
	stopCh     chan struct{}
	closed     bool
	closeOnce  sync.Once
	bytesTotal int64
}

// NewWALWriter opens or creates a WAL file at the given path.
func NewWALWriter(path string, policy FsyncPolicy) (*WALWriter, error) {
	cleanPath := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	file, err := os.OpenFile(cleanPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open WAL file %s: %w", cleanPath, err)
	}

	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}

	w := &WALWriter{
		file:       file,
		path:       cleanPath,
		policy:     policy,
		stopCh:     make(chan struct{}),
		bytesTotal: stat.Size(),
	}

	if policy == FsyncEverySec {
		go w.startPeriodicSync()
	}

	return w, nil
}

func (w *WALWriter) startPeriodicSync() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.mu.Lock()
			if !w.closed && w.file != nil {
				_ = w.file.Sync()
			}
			w.mu.Unlock()
		case <-w.stopCh:
			return
		}
	}
}

// Append writes a single WAL record to the append-only log.
func (w *WALWriter) Append(rec WALRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("failed to marshal WAL record: %w", err)
	}
	data = append(data, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed || w.file == nil {
		return fmt.Errorf("WAL writer is closed")
	}

	n, err := w.file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write to WAL: %w", err)
	}
	w.bytesTotal += int64(n)

	if w.policy == FsyncAlways {
		if err := w.file.Sync(); err != nil {
			return fmt.Errorf("failed to sync WAL: %w", err)
		}
	}

	return nil
}

// Sync flushes internal buffers and forces file synchronization to disk.
func (w *WALWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed || w.file == nil {
		return nil
	}
	return w.file.Sync()
}

// Size returns the total bytes written to the current WAL file.
func (w *WALWriter) Size() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bytesTotal
}

// Truncate safely resets the WAL file to 0 bytes after an AOF rewrite.
func (w *WALWriter) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed || w.file == nil {
		return fmt.Errorf("WAL writer is closed")
	}

	if err := w.file.Close(); err != nil {
		return err
	}

	// Truncate file to empty
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to truncate WAL file: %w", err)
	}
	w.file = file
	w.bytesTotal = 0

	return nil
}

// Close gracefully closes the WAL writer and terminates background sync routines.
func (w *WALWriter) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.stopCh)

		w.mu.Lock()
		defer w.mu.Unlock()

		w.closed = true
		if w.file != nil {
			_ = w.file.Sync()
			err = w.file.Close()
			w.file = nil
		}
	})
	return err
}

// ReplayWAL reads an existing AOF file sequentially and applies all records via applyFn.
// Returns the maximum LSN encountered in the log.
func ReplayWAL(path string, applyFn func(rec WALRecord) error) (int64, error) {
	cleanPath := filepath.Clean(path)
	file, err := os.Open(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Allow up to 16MB per transaction batch
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 16*1024*1024)

	var maxLSN int64
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var rec WALRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return maxLSN, fmt.Errorf("corrupted WAL entry at line %d in %s: %w", lineNum, cleanPath, err)
		}

		if rec.LSN > maxLSN {
			maxLSN = rec.LSN
		}

		if err := applyFn(rec); err != nil {
			return maxLSN, fmt.Errorf("failed to apply WAL record (LSN=%d, line=%d): %w", rec.LSN, lineNum, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return maxLSN, fmt.Errorf("error reading WAL file: %w", err)
	}

	return maxLSN, nil
}
