package memengine

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"lionsql/pkg/storage"
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
	OpCreateTable   WALOpType = "CREATE_TABLE"
	OpDropTable     WALOpType = "DROP_TABLE"
	OpCreateIndex   WALOpType = "CREATE_INDEX"
	OpDropIndex     WALOpType = "DROP_INDEX"
	OpInsert        WALOpType = "INSERT"
	OpUpdate        WALOpType = "UPDATE"
	OpDelete        WALOpType = "DELETE"
	OpTruncateTable WALOpType = "TRUNCATE_TABLE"
	OpDropPartition WALOpType = "DROP_PARTITION"
	OpAlterTable    WALOpType = "ALTER_TABLE"
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
	Column      *storage.Column      `json:"column,omitempty" msgpack:"column,omitempty"`
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
	mu           sync.Mutex
	file         *os.File
	path         string
	policy       atomic.Int32
	periodicOnce sync.Once
	stopCh       chan struct{}
	closed       bool
	closeOnce    sync.Once
	bytesTotal   int64
	syncCount    atomic.Int64

	// Group commit state. Lock order: syncMu -> mu.
	// writeSeq is guarded by mu; syncedSeq is guarded by syncMu.
	syncMu    sync.Mutex
	writeSeq  int64
	syncedSeq int64
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
		stopCh:     make(chan struct{}),
		bytesTotal: stat.Size(),
	}
	w.SetPolicy(policy)

	return w, nil
}

// SetPolicy atomically updates the fsync policy.
func (w *WALWriter) SetPolicy(policy FsyncPolicy) {
	w.policy.Store(int32(policy))
	if policy == FsyncEverySec {
		w.periodicOnce.Do(func() {
			go w.startPeriodicSync()
		})
	}
}

// Policy returns the current fsync policy.
func (w *WALWriter) Policy() FsyncPolicy {
	return FsyncPolicy(w.policy.Load())
}

func (w *WALWriter) startPeriodicSync() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if w.Policy() == FsyncEverySec {
				if err := w.syncAll(); err != nil {
					log.Printf("[WAL] periodic sync failed: %v", err)
				}
			}
		case <-w.stopCh:
			return
		}
	}
}

// Append writes a binary MessagePack length-prefixed frame to the WAL file
// and synchronizes to disk if the policy is FsyncAlways.
func (w *WALWriter) Append(rec WALRecord) error {
	seq, err := w.writeFrame(rec)
	if err != nil {
		return err
	}
	return w.syncForPolicy(seq)
}

// writeFrame marshals rec and writes a single length-prefixed frame to the OS file buffer
// under w.mu, returning the assigned write sequence number without calling fsync.
func (w *WALWriter) writeFrame(rec WALRecord) (int64, error) {
	payload, err := MarshalMsgPack(rec)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal MessagePack WAL record: %w", err)
	}

	// Build single contiguous frame [4-byte big-endian length | payload] outside the lock
	// so we issue a single write(2) syscall under w.mu.
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)

	w.mu.Lock()
	if w.closed || w.file == nil {
		w.mu.Unlock()
		return 0, fmt.Errorf("WAL writer is closed")
	}

	n, err := w.file.Write(frame)
	if err != nil {
		w.mu.Unlock()
		return 0, fmt.Errorf("failed to write WAL frame: %w", err)
	}
	w.bytesTotal += int64(n)
	w.writeSeq++
	seq := w.writeSeq
	w.mu.Unlock()

	return seq, nil
}

// syncForPolicy performs leader/follower group commit fsync up to seq when policy is FsyncAlways.
func (w *WALWriter) syncForPolicy(seq int64) error {
	if seq > 0 && w.Policy() == FsyncAlways {
		if err := w.syncUpTo(seq); err != nil {
			return fmt.Errorf("failed to sync WAL: %w", err)
		}
	}
	return nil
}

// syncUpTo makes every frame with sequence <= seq durable using leader/follower
// group commit: the first waiter to take syncMu becomes the leader and fsyncs
// all frames written so far; followers that queued behind it find their frame
// already covered and return without issuing a syscall. The fsync runs outside
// mu so other committers keep appending while the leader waits on the disk.
func (w *WALWriter) syncUpTo(seq int64) error {
	w.syncMu.Lock()
	defer w.syncMu.Unlock()

	if w.syncedSeq >= seq {
		return nil
	}
	return w.syncLeaderLocked()
}

// syncAll flushes everything written so far (no-op if already durable).
func (w *WALWriter) syncAll() error {
	w.syncMu.Lock()
	defer w.syncMu.Unlock()
	return w.syncLeaderLocked()
}

// syncLeaderLocked must be called with syncMu held.
func (w *WALWriter) syncLeaderLocked() error {
	w.mu.Lock()
	target := w.writeSeq
	file := w.file
	closed := w.closed
	w.mu.Unlock()

	if closed || file == nil || w.syncedSeq >= target {
		return nil
	}
	w.syncCount.Add(1)
	if err := file.Sync(); err != nil {
		return err
	}
	w.syncedSeq = target
	return nil
}

// SyncCount returns the number of fsync calls issued by WAL sync operations.
func (w *WALWriter) SyncCount() int64 { return w.syncCount.Load() }

// Sync flushes internal buffers and forces file synchronization to disk.
func (w *WALWriter) Sync() error {
	return w.syncAll()
}

// Size returns the total bytes written to the current WAL file.
func (w *WALWriter) Size() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bytesTotal
}

// Rotate atomically syncs and closes the active WAL file, renames it to rotatedPath,
// and immediately opens a fresh active WAL file at the original path.
// This guarantees zero transaction log truncation or data loss during archiving.
func (w *WALWriter) Rotate(rotatedPath string) error {
	w.syncMu.Lock()
	defer w.syncMu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed || w.file == nil {
		return fmt.Errorf("WAL writer is closed")
	}

	// 1. Flush any pending buffers to disk
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync WAL file before rotation: %w", err)
	}

	// 2. Close current file descriptor
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("failed to close WAL file for rotation: %w", err)
	}

	// 3. Atomically rename current file to rotatedPath
	if err := os.Rename(w.path, rotatedPath); err != nil {
		// Attempt to reopen original if rename failed
		reopenFile, _ := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		w.file = reopenFile
		return fmt.Errorf("failed to rename WAL file to %s: %w", rotatedPath, err)
	}

	// 4. Open fresh active WAL file at original path
	newFile, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to create fresh WAL file after rotation: %w", err)
	}

	w.file = newFile
	w.bytesTotal = 0
	w.syncedSeq = w.writeSeq
	return nil
}

// Truncate safely resets the WAL file to 0 bytes after an AOF rewrite.
func (w *WALWriter) Truncate() error {
	w.syncMu.Lock()
	defer w.syncMu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed || w.file == nil {
		return fmt.Errorf("WAL writer is closed")
	}

	if err := w.file.Close(); err != nil {
		return err
	}

	// Truncate file to empty while preserving O_APPEND semantics
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to truncate WAL file: %w", err)
	}
	w.file = file
	w.bytesTotal = 0
	// Everything previously written has been discarded; nothing is pending.
	w.syncedSeq = w.writeSeq

	return nil
}

// Close gracefully closes the WAL writer and terminates background sync routines.
func (w *WALWriter) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.stopCh)

		w.syncMu.Lock()
		defer w.syncMu.Unlock()
		w.mu.Lock()
		defer w.mu.Unlock()

		w.closed = true
		if w.file != nil {
			if syncErr := w.file.Sync(); syncErr != nil && err == nil {
				err = syncErr
			}
			if closeErr := w.file.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
			w.file = nil
		}
	})
	return err
}

// ReplayWAL reads an existing AOF file and applies all records via applyFn.
// Supports high-speed binary MessagePack frames and automatically recovers from torn writes at the tail.
func ReplayWAL(path string, applyFn func(rec WALRecord) error) (int64, error) {
	cleanPath := filepath.Clean(path)
	file, err := os.OpenFile(cleanPath, os.O_RDWR, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		// Fallback to read-only open if write permissions are restricted
		file, err = os.Open(cleanPath)
		if err != nil {
			return 0, err
		}
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil || stat.Size() == 0 {
		return 0, nil
	}

	var maxLSN int64
	var validOffset int64
	fileSize := stat.Size()
	isTailTorn := false

	// Binary MessagePack length-prefixed frames
	reader := bufio.NewReaderSize(file, 64*1024)
	frameIdx := 0

	for {
		var lenBuf [4]byte
		n, err := io.ReadFull(reader, lenBuf[:])
		if err == io.EOF {
			// Case 1: Clean EOF exactly on record boundary.
			break
		}
		if err == io.ErrUnexpectedEOF {
			// Case 2-a: Header incomplete at tail.
			// Must verify that this is truly the physical end of file.
			if validOffset+int64(n) == fileSize {
				isTailTorn = true
				break
			}
			return maxLSN, fmt.Errorf("wal: premature unexpected EOF reading header at offset %d (file size %d)", validOffset, fileSize)
		}
		if err != nil {
			// Case 3: Fatal OS / hardware I/O read error. Zero truncation.
			return maxLSN, fmt.Errorf("wal: fatal I/O read error at offset %d: %w", validOffset, err)
		}

		frameLen := binary.BigEndian.Uint32(lenBuf[:])
		if frameLen == 0 || frameLen > 64*1024*1024 { // Cap at 64MB per tx batch
			// Check if this is an incomplete torn frame at the physical tail of the file.
			if validOffset+4 == fileSize {
				isTailTorn = true
				break
			}
			// Case 4: Mid-file corruption. Never truncate intact subsequent tail!
			return maxLSN, fmt.Errorf("wal: corrupted frame length %d at offset %d (file size %d)", frameLen, validOffset, fileSize)
		}

		payload := make([]byte, frameLen)
		pn, err := io.ReadFull(reader, payload)
		if err != nil {
			if (err == io.EOF || err == io.ErrUnexpectedEOF) && validOffset+4+int64(pn) == fileSize {
				// Case 2-b: Torn write in payload at exact physical tail.
				isTailTorn = true
				break
			}
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return maxLSN, fmt.Errorf("wal: premature EOF reading payload at offset %d (file size %d)", validOffset+4, fileSize)
			}
			return maxLSN, fmt.Errorf("wal: fatal I/O read error for payload at offset %d: %w", validOffset+4, err)
		}

		var rec WALRecord
		if err := UnmarshalMsgPack(payload, &rec); err != nil {
			// Corrupted payload. Check if this is the physical tail.
			if validOffset+4+int64(frameLen) == fileSize {
				isTailTorn = true
				break
			}
			return maxLSN, fmt.Errorf("wal: corrupted payload unmarshal at offset %d (file size %d): %w", validOffset+4, fileSize, err)
		}

		if err := applyFn(rec); err != nil {
			return maxLSN, fmt.Errorf("failed to apply MessagePack WAL record (LSN=%d, frame=%d): %w", rec.LSN, frameIdx, err)
		}

		frameIdx++
		if rec.LSN > maxLSN {
			maxLSN = rec.LSN
		}
		validOffset += int64(4 + frameLen)
	}

	// Strictly restrict truncation to verified tail torn writes.
	// Never truncate on clean EOF, I/O errors, or mid-file corruption ([RULE:ExplicitError], ADR-027).
	if isTailTorn && fileSize > validOffset {
		if err := file.Truncate(validOffset); err != nil {
			return maxLSN, fmt.Errorf("wal: failed to truncate torn tail at offset %d: %w", validOffset, err)
		}
	}

	return maxLSN, nil
}
