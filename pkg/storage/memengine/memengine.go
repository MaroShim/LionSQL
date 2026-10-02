package memengine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sqlion/pkg/storage"
)

type Options struct {
	DataDir       string
	EnableAOF     bool          // Whether to write append-only log (wal.aof)
	EnableRDB     bool          // Whether to periodically save and load RDB snapshots (tables.mpk)
	FsyncPolicy   FsyncPolicy   // FsyncAlways, FsyncEverySec, FsyncNo
	AOFThreshold  int64         // Size in bytes to trigger automatic AOF rewrite compaction (default: 64MB)
	RDBInterval   time.Duration // Periodic RDB snapshot / AOF rewrite interval (default: 60s)
	RDBMinChanges int64         // Minimum mutation count to trigger snapshot (default: 1000)
}

// DefaultOptions returns safe default persistence options based on directory.
func DefaultOptions(dir string) Options {
	return Options{
		DataDir:       dir,
		EnableAOF:     dir != "",
		EnableRDB:     dir != "",
		FsyncPolicy:   FsyncEverySec,
		AOFThreshold:  64 * 1024 * 1024,
		RDBInterval:   60 * time.Second,
		RDBMinChanges: 1000,
	}
}

// Engine implements an in-memory OCC storage engine with optional Redis-style AOF/RDB durability.
type Engine struct {
	mu               sync.RWMutex
	opts             Options
	dataDir          string
	tables           map[string]*TableData
	wal              *WALWriter
	fsyncPolicy      FsyncPolicy
	nextLSN          atomic.Int64
	aofThreshold     int64 // Size threshold in bytes to trigger AOF rewrite compaction
	changesSinceSave atomic.Int64
	walHook          func(rec WALRecord)
	stopCh           chan struct{}
	bgWg             sync.WaitGroup
	closed           bool
}

// CurrentLSN returns the latest assigned Log Sequence Number.
func (e *Engine) CurrentLSN() int64 {
	return e.nextLSN.Load()
}

// SetWALHook registers a callback that is synchronously invoked whenever a new WAL record is committed.
func (e *Engine) SetWALHook(fn func(rec WALRecord)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.walHook = fn
}

// recordWALLocked writes a WAL record, invokes the registered walHook, and returns the assigned WALRecord.
func (e *Engine) recordWALLocked(ops []WALOp) WALRecord {
	if len(ops) == 0 {
		return WALRecord{}
	}
	e.changesSinceSave.Add(int64(len(ops)))
	rec := WALRecord{
		LSN:       e.nextLSN.Add(1),
		Timestamp: time.Now().UnixNano(),
		Ops:       ops,
	}
	if e.wal != nil {
		_ = e.wal.Append(rec)
		if e.aofThreshold > 0 && e.wal.Size() > e.aofThreshold {
			_ = e.rewriteAOFLocked()
		}
	}
	if e.walHook != nil {
		e.walHook(rec)
	}
	return rec
}

// New creates a new in-memory storage engine.
func New() *Engine {
	return &Engine{
		tables:       make(map[string]*TableData),
		fsyncPolicy:  FsyncEverySec,
		aofThreshold: 64 * 1024 * 1024, // 64MB default compaction threshold
	}
}

// Type returns the human-readable storage engine backend name.
func (e *Engine) Type() string {
	return "In-Memory OCC"
}

// IsEphemeral returns true if persistence is completely disabled (pure in-memory mode).
func (e *Engine) IsEphemeral() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dataDir == "" || (!e.opts.EnableAOF && !e.opts.EnableRDB)
}

// PersistenceSummary returns a descriptive string of the persistence mode.
func (e *Engine) PersistenceSummary() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.dataDir == "" || (!e.opts.EnableAOF && !e.opts.EnableRDB) {
		return "ephemeral"
	}
	var parts []string
	if e.opts.EnableAOF {
		parts = append(parts, "AOF")
	}
	if e.opts.EnableRDB {
		parts = append(parts, "RDB")
	}
	return strings.Join(parts, "+")
}

// TableRowCount returns the row count of a table in O(1) time across shards.
func (e *Engine) TableRowCount(tableName string) (int64, error) {
	e.mu.RLock()
	td, exists := e.tables[tableName]
	e.mu.RUnlock()
	if !exists {
		return 0, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	return td.rowCount(), nil
}

// SetFsyncPolicy sets the disk synchronization policy for WAL/AOF.
func (e *Engine) SetFsyncPolicy(policy FsyncPolicy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fsyncPolicy = policy
	if e.wal != nil {
		e.wal.policy = policy
	}
}

// SetAOFThreshold sets the byte threshold for automatic AOF rewrite compaction.
func (e *Engine) SetAOFThreshold(bytes int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.aofThreshold = bytes
}

// Open initializes the engine with default options based on directory.
func (e *Engine) Open(dir string) error {
	e.mu.RLock()
	thresh := e.aofThreshold
	policy := e.fsyncPolicy
	e.mu.RUnlock()
	opts := DefaultOptions(dir)
	if thresh > 0 {
		opts.AOFThreshold = thresh
	}
	if policy != 0 {
		opts.FsyncPolicy = policy
	}
	return e.OpenWithOptions(opts)
}

// OpenWithOptions initializes the engine with explicit Redis-style persistence options.
func (e *Engine) OpenWithOptions(opts Options) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.closed = false
	e.opts = opts
	e.fsyncPolicy = opts.FsyncPolicy
	if opts.AOFThreshold > 0 {
		e.aofThreshold = opts.AOFThreshold
	}

	if opts.DataDir != "" {
		e.dataDir = filepath.Clean(opts.DataDir)
		if err := os.MkdirAll(e.dataDir, 0755); err != nil {
			return fmt.Errorf("failed to create data directory: %w", err)
		}

		// 1. Load baseline snapshot (RDB) if enabled
		if opts.EnableRDB {
			e.loadDiskLocked()
		}

		// 2. Replay subsequent AOF log records if enabled
		if opts.EnableAOF {
			aofPath := filepath.Join(e.dataDir, "wal.aof")
			maxLSN, err := ReplayWAL(aofPath, func(rec WALRecord) error {
				return e.applyWALRecordLocked(rec)
			})
			if err != nil {
				return fmt.Errorf("WAL replay error: %w", err)
			}
			e.nextLSN.Store(maxLSN)

			// 3. Open WAL writer for future append-only logging
			walWriter, err := NewWALWriter(aofPath, e.fsyncPolicy)
			if err != nil {
				return fmt.Errorf("failed to initialize WAL writer: %w", err)
			}
			e.wal = walWriter

			// If AOF is already larger than threshold on startup, compact it
			if e.aofThreshold > 0 && e.wal.Size() > e.aofThreshold {
				_ = e.rewriteAOFLocked()
			}
		}
	}

	// 4. Start Redis-style background snapshot / compaction timer
	if (opts.EnableRDB || opts.EnableAOF) && opts.RDBInterval > 0 && e.dataDir != "" {
		stopCh := make(chan struct{})
		e.stopCh = stopCh
		e.bgWg.Add(1)
		go e.bgSaveLoop(stopCh, opts.RDBInterval, opts.RDBMinChanges)
	}

	return nil
}

func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true

	stopCh := e.stopCh
	e.stopCh = nil
	if stopCh != nil {
		close(stopCh)
	}
	e.mu.Unlock()

	e.bgWg.Wait()

	e.mu.Lock()
	defer e.mu.Unlock()

	var saveErr, walErr error
	if e.dataDir != "" && e.opts.EnableRDB {
		saveErr = e.saveDiskLocked()
	}
	if e.wal != nil {
		walErr = e.wal.Close()
		e.wal = nil
	}
	if saveErr != nil {
		return saveErr
	}
	return walErr
}

