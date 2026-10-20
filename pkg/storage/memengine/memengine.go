package memengine

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/metrics"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sqlion/pkg/storage"
)

// CurrentHeapBytes returns the current live heap memory allocated in bytes via runtime/metrics.
func CurrentHeapBytes() int64 {
	sample := [1]metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample[:])
	if sample[0].Value.Kind() == metrics.KindUint64 {
		return int64(sample[0].Value.Uint64())
	}
	return 0
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

type Options struct {
	DataDir       string
	EnableAOF     bool          // Whether to write append-only log (wal.aof)
	EnableRDB     bool          // Whether to periodically save and load RDB snapshots (tables.mpk)
	FsyncPolicy   FsyncPolicy   // FsyncAlways, FsyncEverySec, FsyncNo
	AOFThreshold  int64         // Size in bytes to trigger automatic AOF rewrite compaction (default: 64MB)
	RDBInterval   time.Duration // Periodic RDB snapshot / AOF rewrite interval (default: 60s)
	RDBMinChanges int64         // Minimum mutation count to trigger snapshot (default: 1000)
	MaxDataBytes  int64         // Maximum memory budget for in-memory storage in bytes (0 for unlimited)
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
	maxDataBytes     atomic.Int64
	cachedHeapBytes  atomic.Int64
	commitCounter    atomic.Uint64
	walHook          func(rec WALRecord)
	stopCh           chan struct{}
	bgWg             sync.WaitGroup
	closed           bool
	compacting       atomic.Bool
	diskMu           sync.Mutex
	walOrderMu       sync.Mutex
}

// SetMaxDataBytes sets the maximum capacity in bytes allowed for storage in the in-memory engine.
// A value of 0 or negative disables the limit.
func (e *Engine) SetMaxDataBytes(bytes int64) {
	if bytes < 0 {
		bytes = 0
	}
	e.maxDataBytes.Store(bytes)
	if bytes > 0 {
		e.cachedHeapBytes.Store(CurrentHeapBytes())
	}
}

// MaxDataBytes returns the current maximum capacity limit in bytes (0 if unlimited).
func (e *Engine) MaxDataBytes() int64 {
	return e.maxDataBytes.Load()
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

// AddWALHook registers an additional callback invoked whenever a new WAL record is committed.
func (e *Engine) AddWALHook(fn func(rec WALRecord)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	prev := e.walHook
	if prev == nil {
		e.walHook = fn
	} else {
		e.walHook = func(rec WALRecord) {
			prev(rec)
			fn(rec)
		}
	}
}

// appendWALFrameLocked atomically assigns the next monotonic LSN, writes the WAL frame
// to the OS file buffer, and notifies walHook in strict LSN order. Group commit fsync and
// AOF compaction are deferred to finishWALCommitLocked so shard locks are not held during disk sync.
func (e *Engine) appendWALFrameLocked(ops []WALOp) (WALRecord, int64, error) {
	if len(ops) == 0 {
		return WALRecord{}, 0, nil
	}
	e.changesSinceSave.Add(int64(len(ops)))

	e.walOrderMu.Lock()
	rec := WALRecord{
		LSN:       e.nextLSN.Add(1),
		Timestamp: time.Now().UnixNano(),
		Ops:       ops,
	}
	var seq int64
	if e.wal != nil {
		var err error
		seq, err = e.wal.writeFrame(rec)
		if err != nil {
			e.walOrderMu.Unlock()
			return rec, 0, err
		}
	}
	if e.walHook != nil {
		e.walHook(rec)
	}
	e.walOrderMu.Unlock()

	return rec, seq, nil
}

// finishWALCommitLocked performs group commit fsync (when FsyncAlways) and triggers
// background-safe AOF compaction if the WAL file exceeds aofThreshold.
func (e *Engine) finishWALCommitLocked(seq int64) error {
	if e.wal == nil {
		return nil
	}
	if err := e.wal.syncForPolicy(seq); err != nil {
		return err
	}
	if e.aofThreshold > 0 && e.wal.Size() > e.aofThreshold {
		// Guard compaction with atomic CAS so concurrent committers under e.mu.RLock()
		// do not race in saveDiskLocked() / rewriteAOFLocked().
		if e.compacting.CompareAndSwap(false, true) {
			if err := e.rewriteAOFLocked(); err != nil {
				e.compacting.Store(false)
				return err
			}
			e.compacting.Store(false)
		}
	}
	return nil
}

// recordWALLocked writes a WAL record, invokes the registered walHook, and returns the assigned WALRecord.
func (e *Engine) recordWALLocked(ops []WALOp) (WALRecord, error) {
	rec, seq, err := e.appendWALFrameLocked(ops)
	if err != nil {
		return rec, err
	}
	if err := e.finishWALCommitLocked(seq); err != nil {
		return rec, err
	}
	return rec, nil
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
		e.wal.SetPolicy(policy)
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
	if opts.MaxDataBytes > 0 {
		e.maxDataBytes.Store(opts.MaxDataBytes)
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

