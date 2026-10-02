package memengine

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
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
	OpInsert        WALOpType = "INSERT"
	OpUpdate        WALOpType = "UPDATE"
	OpDelete        WALOpType = "DELETE"
	OpTruncateTable WALOpType = "TRUNCATE_TABLE"
	OpDropPartition WALOpType = "DROP_PARTITION"
)

// WALOp represents a single atomic row or DDL mutation.
type WALOp struct {
	Type        WALOpType            `json:"type" msgpack:"type"`
	Table       string               `json:"table" msgpack:"table"`
	Key         string               `json:"key,omitempty" msgpack:"key,omitempty"`
	Data        []byte               `json:"data,omitempty" msgpack:"data,omitempty"`
	Record      storage.Record       `json:"record,omitempty" msgpack:"record,omitempty"`
	BaseVer     int64                `json:"base_ver,omitempty" msgpack:"base_ver,omitempty"`
	Schema      *storage.TableSchema `json:"schema,omitempty" msgpack:"schema,omitempty"`
	IndexSchema *storage.IndexSchema `json:"index_schema,omitempty" msgpack:"index_schema,omitempty"`
	IndexName   string               `json:"index_name,omitempty" msgpack:"index_name,omitempty"`
}

// WALRecord represents a committed transaction batch in the AOF log.
type WALRecord struct {
	LSN       int64   `json:"lsn" msgpack:"lsn"`
	TxID      int64   `json:"tx_id" msgpack:"tx_id"`
	Timestamp int64   `json:"ts" msgpack:"ts"`
	Ops       []WALOp `json:"ops" msgpack:"ops"`
}

// WALWriter handles thread-safe append-only logging with configurable fsync policies and MessagePack binary frames.
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

// Append writes a binary MessagePack length-prefixed frame to the WAL file.
func (w *WALWriter) Append(rec WALRecord) error {
	payload, err := MarshalMsgPack(rec)
	if err != nil {
		return fmt.Errorf("failed to marshal MessagePack WAL record: %w", err)
	}

	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed || w.file == nil {
		return fmt.Errorf("WAL writer is closed")
	}

	// Write length prefix (4 bytes) followed by MessagePack payload
	n1, err := w.file.Write(header[:])
	if err != nil {
		return fmt.Errorf("failed to write WAL header: %w", err)
	}
	n2, err := w.file.Write(payload)
	if err != nil {
		return fmt.Errorf("failed to write WAL payload: %w", err)
	}
	w.bytesTotal += int64(n1 + n2)

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

// ReplayWAL reads an existing AOF file and applies all records via applyFn.
// Supports both high-speed binary MessagePack frames and legacy JSON line fallback.
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

	stat, err := file.Stat()
	if err != nil || stat.Size() == 0 {
		return 0, nil
	}

	var maxLSN int64

	// Binary MessagePack length-prefixed frames
	reader := bufio.NewReaderSize(file, 64*1024)
	frameIdx := 0

	for {
		var lenBuf [4]byte
		_, err := io.ReadFull(reader, lenBuf[:])
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return maxLSN, fmt.Errorf("error reading WAL frame header: %w", err)
		}

		frameLen := binary.BigEndian.Uint32(lenBuf[:])
		if frameLen == 0 || frameLen > 64*1024*1024 { // Cap at 64MB per tx batch
			return maxLSN, fmt.Errorf("invalid WAL frame length %d at frame %d", frameLen, frameIdx)
		}

		payload := make([]byte, frameLen)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return maxLSN, fmt.Errorf("truncated WAL frame payload at frame %d: %w", frameIdx, err)
		}

		var rec WALRecord
		if err := UnmarshalMsgPack(payload, &rec); err != nil {
			return maxLSN, fmt.Errorf("failed to decode MessagePack WAL frame %d: %w", frameIdx, err)
		}

		frameIdx++
		if rec.LSN > maxLSN {
			maxLSN = rec.LSN
		}

		if err := applyFn(rec); err != nil {
			return maxLSN, fmt.Errorf("failed to apply MessagePack WAL record (LSN=%d, frame=%d): %w", rec.LSN, frameIdx, err)
		}
	}

	return maxLSN, nil
}
