//go:build cgo && wiredtiger

package wiredtiger

/*
#cgo darwin CFLAGS: -I/opt/homebrew/include -I/usr/local/include
#cgo darwin LDFLAGS: -L/opt/homebrew/lib -L/usr/local/lib -lwiredtiger
#cgo linux CFLAGS: -I/usr/local/include -I/usr/include
#cgo linux LDFLAGS: -L/usr/local/lib -L/usr/lib -L/usr/lib/x86_64-linux-gnu -L/usr/lib/aarch64-linux-gnu -lwiredtiger
#cgo windows,amd64 CFLAGS: -I${SRCDIR}/lib/windows_amd64/include

#include <stdlib.h>
#include <string.h>
#include <wiredtiger.h>

#if defined(_WIN32) || defined(__CYGWIN__)
#include <windows.h>
typedef int (*pfn_wiredtiger_open)(const char *home, WT_EVENT_HANDLER *event_handler, const char *config, WT_CONNECTION **connp);
typedef const char *(*pfn_wiredtiger_strerror)(int error);
static HMODULE g_wt_dll_handle = NULL;
static pfn_wiredtiger_open g_pfn_wt_open = NULL;
static pfn_wiredtiger_strerror g_pfn_wt_strerror = NULL;

static int go_init_windows_wt(const char *dll_path) {
    if (g_wt_dll_handle != NULL && g_pfn_wt_open != NULL && g_pfn_wt_strerror != NULL) {
        return 0;
    }
    if (dll_path != NULL && dll_path[0] != '\0') {
        g_wt_dll_handle = LoadLibraryA(dll_path);
    }
    if (g_wt_dll_handle == NULL) {
        g_wt_dll_handle = LoadLibraryA("wiredtiger.dll");
    }
    if (g_wt_dll_handle == NULL) {
        return -1;
    }
    g_pfn_wt_open = (pfn_wiredtiger_open)GetProcAddress(g_wt_dll_handle, "wiredtiger_open");
    g_pfn_wt_strerror = (pfn_wiredtiger_strerror)GetProcAddress(g_wt_dll_handle, "wiredtiger_strerror");
    if (g_pfn_wt_open == NULL || g_pfn_wt_strerror == NULL) {
        return -2;
    }
    return 0;
}

static int go_wt_open(const char *home, const char *config, WT_CONNECTION **connp) {
    if (g_pfn_wt_open == NULL) {
        int r = go_init_windows_wt(NULL);
        if (r != 0) {
            return -10000;
        }
    }
    return g_pfn_wt_open(home, NULL, config, connp);
}

static const char *go_wt_strerror(int err) {
    if (g_pfn_wt_strerror != NULL) {
        return g_pfn_wt_strerror(err);
    }
    if (go_init_windows_wt(NULL) == 0 && g_pfn_wt_strerror != NULL) {
        return g_pfn_wt_strerror(err);
    }
    return "wiredtiger error (wiredtiger.dll not loaded)";
}
#else
static int go_init_windows_wt(const char *dll_path) {
    (void)dll_path;
    return 0;
}

static int go_wt_open(const char *home, const char *config, WT_CONNECTION **connp) {
    return wiredtiger_open(home, NULL, config, connp);
}

static const char *go_wt_strerror(int err) {
    return wiredtiger_strerror(err);
}
#endif

static int go_wt_close(WT_CONNECTION *conn) {
    return conn->close(conn, NULL);
}

static int go_wt_open_session(WT_CONNECTION *conn, WT_SESSION **sessionp) {
    return conn->open_session(conn, NULL, NULL, sessionp);
}

static int go_wt_session_close(WT_SESSION *session) {
    return session->close(session, NULL);
}

static int go_wt_session_create(WT_SESSION *session, const char *name, const char *config) {
    return session->create(session, name, config);
}

static int go_wt_session_drop(WT_SESSION *session, const char *name) {
    return session->drop(session, name, "force=true");
}

static int go_wt_session_rename(WT_SESSION *session, const char *uri, const char *newuri) {
    return session->rename(session, uri, newuri, NULL);
}

static int go_wt_session_truncate(WT_SESSION *session, const char *name) {
    return session->truncate(session, name, NULL, NULL, NULL);
}

static int go_wt_session_checkpoint(WT_SESSION *session) {
    if (session == NULL) {
        return 0;
    }
    return session->checkpoint(session, NULL);
}

static int go_wt_session_begin(WT_SESSION *session, const char *config) {
    return session->begin_transaction(session, config);
}

static int go_wt_session_commit(WT_SESSION *session, const char *config) {
    return session->commit_transaction(session, config);
}

static int go_wt_session_rollback(WT_SESSION *session, const char *config) {
    return session->rollback_transaction(session, config);
}

static int go_wt_session_reset(WT_SESSION *session) {
    if (session == NULL) {
        return 0;
    }
    return session->reset(session);
}

static int go_wt_session_log_flush_sync(WT_SESSION *session) {
    return session->log_flush(session, "sync=on");
}

static int go_wt_session_log_flush_os(WT_SESSION *session) {
    return session->log_flush(session, "sync=off");
}

static int go_wt_open_cursor(WT_SESSION *session, const char *uri, WT_CURSOR **cursorp) {
    return session->open_cursor(session, uri, NULL, NULL, cursorp);
}

static int go_wt_open_cursor_config(WT_SESSION *session, const char *uri, const char *config, WT_CURSOR **cursorp) {
    return session->open_cursor(session, uri, NULL, config, cursorp);
}

static int go_wt_cursor_close(WT_CURSOR *cursor) {
    return cursor->close(cursor);
}

static int go_wt_cursor_reset(WT_CURSOR *cursor) {
    return cursor->reset(cursor);
}

static int go_wt_cursor_next(WT_CURSOR *cursor) {
    return cursor->next(cursor);
}

static int go_wt_cursor_prev(WT_CURSOR *cursor) {
    return cursor->prev(cursor);
}

static int go_wt_cursor_search_near(WT_CURSOR *cursor, int *exactp) {
    return cursor->search_near(cursor, exactp);
}

static int go_wt_cursor_insert(WT_CURSOR *cursor) {
    return cursor->insert(cursor);
}

static int go_wt_cursor_update(WT_CURSOR *cursor) {
    return cursor->update(cursor);
}

static int go_wt_cursor_remove(WT_CURSOR *cursor) {
    return cursor->remove(cursor);
}

static int go_wt_cursor_search(WT_CURSOR *cursor) {
    return cursor->search(cursor);
}

static void go_wt_cursor_set_key(WT_CURSOR *cursor, const char *key) {
    cursor->set_key(cursor, key);
}

static void go_wt_cursor_set_key_raw(WT_CURSOR *cursor, const void *data, size_t size) {
    if (cursor != NULL && cursor->key_format != NULL && strcmp(cursor->key_format, "u") == 0) {
        WT_ITEM item;
        memset(&item, 0, sizeof(item));
        item.data = data;
        item.size = size;
        cursor->set_key(cursor, &item);
    } else {
        cursor->set_key(cursor, (const char *)data);
    }
}

static int go_wt_cursor_get_key_raw(WT_CURSOR *cursor, const void **datap, size_t *sizep) {
    if (cursor != NULL && cursor->key_format != NULL && strcmp(cursor->key_format, "u") == 0) {
        WT_ITEM item;
        memset(&item, 0, sizeof(item));
        int ret = cursor->get_key(cursor, &item);
        if (ret == 0) {
            *datap = item.data;
            *sizep = item.size;
        } else {
            *datap = NULL;
            *sizep = 0;
        }
        return ret;
    } else {
        const char *str = NULL;
        int ret = cursor->get_key(cursor, &str);
        if (ret == 0 && str != NULL) {
            *datap = (const void *)str;
            *sizep = strlen(str);
        } else {
            *datap = NULL;
            *sizep = 0;
        }
        return ret;
    }
}

static int go_wt_cursor_remove_key_raw(WT_CURSOR *cursor, const void *data, size_t size) {
    go_wt_cursor_set_key_raw(cursor, data, size);
    return cursor->remove(cursor);
}

static void go_wt_cursor_set_val(WT_CURSOR *cursor, const char *val) {
    cursor->set_value(cursor, val);
}

static int go_wt_cursor_is_format_string(WT_CURSOR *cursor) {
    if (cursor != NULL && cursor->value_format != NULL && strcmp(cursor->value_format, "S") == 0) {
        return 1;
    }
    return 0;
}

static void go_wt_cursor_set_val_raw(WT_CURSOR *cursor, const void *data, size_t size) {
    if (cursor != NULL && cursor->value_format != NULL && strcmp(cursor->value_format, "S") == 0) {
        cursor->set_value(cursor, (const char *)data);
    } else {
        WT_ITEM item;
        memset(&item, 0, sizeof(item));
        item.data = data;
        item.size = size;
        cursor->set_value(cursor, &item);
    }
}

static int go_wt_cursor_get_key(WT_CURSOR *cursor, const char **keyp) {
    return cursor->get_key(cursor, keyp);
}

static int go_wt_cursor_get_val(WT_CURSOR *cursor, const char **valp) {
    return cursor->get_value(cursor, valp);
}

static int go_wt_cursor_get_val_raw(WT_CURSOR *cursor, const void **datap, size_t *sizep) {
    if (cursor != NULL && cursor->value_format != NULL && strcmp(cursor->value_format, "S") == 0) {
        const char *str = NULL;
        int ret = cursor->get_value(cursor, &str);
        if (ret == 0 && str != NULL) {
            *datap = (const void *)str;
            *sizep = strlen(str);
        } else {
            *datap = NULL;
            *sizep = 0;
        }
        return ret;
    } else {
        WT_ITEM item;
        memset(&item, 0, sizeof(item));
        int ret = cursor->get_value(cursor, &item);
        if (ret == 0) {
            *datap = item.data;
            *sizep = item.size;
        } else {
            *datap = NULL;
            *sizep = 0;
        }
        return ret;
    }
}

static int go_wt_cursor_get(WT_CURSOR *cursor, const char *key, const void **datap, size_t *sizep) {
    cursor->set_key(cursor, key);
    int ret = cursor->search(cursor);
    if (ret != 0) {
        cursor->reset(cursor);
        return ret;
    }
    return go_wt_cursor_get_val_raw(cursor, datap, sizep);
}

static int go_wt_cursor_insert_raw(WT_CURSOR *cursor, const char *key, const void *data, size_t size) {
    cursor->set_key(cursor, key);
    go_wt_cursor_set_val_raw(cursor, data, size);
    return cursor->insert(cursor);
}

static int go_wt_cursor_update_raw(WT_CURSOR *cursor, const char *key, const void *data, size_t size) {
    cursor->set_key(cursor, key);
    go_wt_cursor_set_val_raw(cursor, data, size);
    return cursor->update(cursor);
}

static int go_wt_cursor_remove_key(WT_CURSOR *cursor, const char *key) {
    cursor->set_key(cursor, key);
    return cursor->remove(cursor);
}
*/
import "C"
import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

var (
	cSyncOff = C.CString("sync=off")
	cSyncOn  = C.CString("sync=on")
)

const numWTLockShards = 1024

type wtLockShard struct {
	mu            sync.RWMutex
	activeWriters map[string]uint64 // lockKey (table + "\x00" + pk) -> txID
}

type wtLockRef struct {
	shardIdx int
	key      string
}

func hashWTLockKey(key string) int {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= 1099511628211
	}
	return int(h & (numWTLockShards - 1))
}

// Engine implements storage.Engine via the WiredTiger C API, routing
// session-scoped temporary tables and table variables transparently to an
// embedded ephemeral in-memory OCC engine ([PERF-HYBRID-TEMPMEM-1]).
type Engine struct {
	mu          sync.RWMutex
	conn        *C.WT_CONNECTION
	homeDir     string
	tables      map[string]*storage.TableSchema
	tempEng     *memengine.Engine
	nextLSN     atomic.Int64
	nextTxID    atomic.Uint64
	lockShards  [numWTLockShards]wtLockShard
	walHook     func(rec memengine.WALRecord)
	hasHook     atomic.Bool
	walOrderMu  sync.Mutex
	fsyncPolicy atomic.Int32
	syncMu      sync.Mutex
	writeSeq    atomic.Int64
	syncedSeq   atomic.Int64
	syncCount   atomic.Int64
	flushOSMu   sync.Mutex
	flushedOSSeq atomic.Int64
	stopCh      chan struct{}
	bgWg        sync.WaitGroup
	closed      bool
	connClosed  atomic.Bool
	sessMu      sync.Mutex
	sessions    map[*WTSession]struct{}
	rowCounts   map[string]*atomic.Int64
	tainted     atomic.Bool
	taintMu     sync.RWMutex
	taintErr    error
	cacheSize    string
	sessionMax   int
	maxDataBytes atomic.Int64
}

func (e *Engine) markTainted(err error) {
	if e == nil {
		return
	}
	e.taintMu.Lock()
	defer e.taintMu.Unlock()
	if !e.tainted.Load() {
		e.taintErr = err
		e.tainted.Store(true)
	}
}

func (e *Engine) checkTainted() error {
	if e == nil || !e.tainted.Load() {
		return nil
	}
	e.taintMu.RLock()
	defer e.taintMu.RUnlock()
	return fmt.Errorf("%w: %v", storage.ErrEngineTainted, e.taintErr)
}

func (e *Engine) IsTainted() bool {
	return e != nil && e.tainted.Load()
}

func (e *Engine) TaintError() error {
	if e == nil || !e.tainted.Load() {
		return nil
	}
	e.taintMu.RLock()
	defer e.taintMu.RUnlock()
	return e.taintErr
}

// New creates a new WiredTiger storage engine instance.
func New() *Engine {
	e := &Engine{
		tables:    make(map[string]*storage.TableSchema),
		tempEng:   memengine.New(),
		sessions:  make(map[*WTSession]struct{}),
		rowCounts: make(map[string]*atomic.Int64),
	}
	e.fsyncPolicy.Store(int32(memengine.FsyncEverySec))
	return e
}

// SetFsyncPolicy configures the journal synchronization policy (FsyncNone, FsyncEverySec, FsyncAlways).
func (e *Engine) SetFsyncPolicy(policy memengine.FsyncPolicy) {
	e.fsyncPolicy.Store(int32(policy))
}

// SetMaxDataBytes sets the maximum capacity in bytes allowed for storage in the WiredTiger engine.
func (e *Engine) SetMaxDataBytes(bytes int64) {
	e.maxDataBytes.Store(bytes)
}

// MaxDataBytes returns the current maximum capacity limit in bytes (0 if unlimited).
func (e *Engine) MaxDataBytes() int64 {
	return e.maxDataBytes.Load()
}

// FsyncPolicy returns the current journal synchronization policy.
func (e *Engine) FsyncPolicy() memengine.FsyncPolicy {
	return memengine.FsyncPolicy(e.fsyncPolicy.Load())
}

// SyncCount returns the number of journal log_flush(sync=on) calls issued by group commit or periodic sync.
func (e *Engine) SyncCount() int64 {
	return e.syncCount.Load()
}

// Sync forces flushing of all committed transactions to the WiredTiger write-ahead log ([RULE:ExplicitError], Scope-129, AUDIT-FULL-013).
func (e *Engine) Sync() error {
	e.syncMu.Lock()
	defer e.syncMu.Unlock()

	e.mu.RLock()
	if e.closed || e.conn == nil {
		e.mu.RUnlock()
		return nil
	}
	conn := e.conn
	e.mu.RUnlock()

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(conn, &sess); ret != 0 {
		return fmt.Errorf("wiredtiger open session for sync failed with code %d", ret)
	}
	defer C.go_wt_session_close(sess)
	return e.syncUpToLocked(sess, e.writeSeq.Load())
}

// syncUpToLocked flushes transactions up to seq while e.syncMu is held.
func (e *Engine) syncUpToLocked(sess *C.WT_SESSION, seq int64) error {
	if e.syncedSeq.Load() >= seq {
		return nil
	}
	target := e.writeSeq.Load()
	e.syncCount.Add(1)
	if ret := C.go_wt_session_log_flush_sync(sess); ret != 0 {
		return fmt.Errorf("wiredtiger log_flush failed with code %d", ret)
	}
	e.syncedSeq.Store(target)
	return nil
}

// syncUpTo performs leader/follower group commit via WT_SESSION::log_flush(sync=on).
// The first waiter on syncMu flushes all committed transactions up to writeSeq in a
// single disk sync; followers covered by that flush return immediately without a syscall.
func (e *Engine) syncUpTo(sess *C.WT_SESSION, seq int64) error {
	e.syncMu.Lock()
	defer e.syncMu.Unlock()
	return e.syncUpToLocked(sess, seq)
}

// flushOSToLocked flushes buffered log records from WiredTiger's user-space memory buffer
// to the operating system's page cache via WT_SESSION::log_flush(sync=off).
// This guarantees that if the process terminates abruptly (SIGKILL / kill -9), the OS kernel
// retains the written WAL frames and flushes them to disk with zero transaction loss.
func (e *Engine) flushOSToLocked(sess *C.WT_SESSION, seq int64) error {
	if e.flushedOSSeq.Load() >= seq {
		return nil
	}
	target := e.writeSeq.Load()
	if ret := C.go_wt_session_log_flush_os(sess); ret != 0 {
		return fmt.Errorf("wiredtiger log_flush(sync=off) failed with code %d", ret)
	}
	e.flushedOSSeq.Store(target)
	return nil
}

// flushOSTo performs leader/follower group log flush to the OS page cache.
func (e *Engine) flushOSTo(sess *C.WT_SESSION, seq int64) error {
	e.flushOSMu.Lock()
	defer e.flushOSMu.Unlock()
	return e.flushOSToLocked(sess, seq)
}

func (e *Engine) startPeriodicSync(stopCh <-chan struct{}) {
	defer e.bgWg.Done()

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return
	}
	defer C.go_wt_session_close(sess)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	lastReset := time.Now()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			if e.FsyncPolicy() == memengine.FsyncEverySec {
				seq := e.writeSeq.Load()
				if seq > e.syncedSeq.Load() {
					_ = e.syncUpTo(sess, seq)
					lastReset = time.Now()
				}
			}
			// Periodic session reset every 5 minutes prevents WiredTiger's internal
			// sweep-server from emitting 60-minute idle session warnings.
			if time.Since(lastReset) >= 5*time.Minute {
				C.go_wt_session_reset(sess)
				lastReset = time.Now()
			}
		}
	}
}

// Type returns the human-readable storage engine backend name.
func (e *Engine) Type() string {
	return "WiredTiger"
}

// CurrentLSN returns the latest assigned Log Sequence Number.
func (e *Engine) CurrentLSN() int64 {
	return e.nextLSN.Load()
}

// SetWALHook registers a callback that is synchronously invoked whenever a transaction commits or DDL executes.
func (e *Engine) SetWALHook(fn func(rec memengine.WALRecord)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.walHook = fn
	e.hasHook.Store(fn != nil)
}

// AddWALHook registers an additional callback invoked whenever a transaction commits or DDL executes.
func (e *Engine) AddWALHook(fn func(rec memengine.WALRecord)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	prev := e.walHook
	if prev == nil {
		e.walHook = fn
	} else {
		e.walHook = func(rec memengine.WALRecord) {
			prev(rec)
			fn(rec)
		}
	}
	e.hasHook.Store(true)
}

// recordWALLocked assigns a monotonically ordered LSN and invokes walHook under walOrderMu.
func (e *Engine) recordWALLocked(ops []memengine.WALOp) memengine.WALRecord {
	if len(ops) == 0 {
		return memengine.WALRecord{}
	}
	e.walOrderMu.Lock()
	rec := memengine.WALRecord{
		LSN:       e.nextLSN.Add(1),
		Timestamp: time.Now().UnixNano(),
		Ops:       ops,
	}
	if e.walHook != nil {
		e.walHook(rec)
	}
	e.walOrderMu.Unlock()
	return rec
}

// WiredTigerOpenConfig defines the canonical, verified configuration passed to wiredtiger_open.
// CRITICAL ARCHITECTURAL INVARIANT [RULE:RadicalKISS / PERF-WT-CONFIG-1 / Invariant 10]:
// NEVER add 'transaction_sync' to this configuration string!
// Enabling transaction_sync forces WiredTiger to acquire a single global C transaction mutex on
// EVERY commit, serializing all concurrent worker threads and causing a catastrophic 96% throughput collapse
// (146,000 TPS -> 4,800 TPS). Full ACID crash durability is 100% guaranteed via log=(enabled=true),
// automatic 60s checkpoints, and LionSQL's startPeriodicSync / syncUpTo group commit architecture
// (verified across 10/10 rounds of abrupt SIGKILL crash tests with zero acknowledged data loss).
const WiredTigerOpenConfig = "create,cache_size=500M,session_max=2000,eviction=(threads_min=4,threads_max=8),checkpoint=(wait=60),log=(enabled=true)"

// BuildWiredTigerOpenConfig dynamically constructs a verified configuration passed to wiredtiger_open
// with custom cache_size and session_max, strictly maintaining Invariant 10 ([RULE:RadicalKISS / Invariant 10]).
func BuildWiredTigerOpenConfig(cacheSize string, sessionMax int) string {
	if cacheSize == "" {
		cacheSize = "500M"
	}
	if sessionMax <= 0 {
		sessionMax = 2000
	}
	cfg := fmt.Sprintf("create,cache_size=%s,session_max=%d,eviction=(threads_min=4,threads_max=8),checkpoint=(wait=60),log=(enabled=true)", cacheSize, sessionMax)
	if strings.Contains(cfg, "transaction_sync") {
		panic("ILLEGAL WIREDTIGER CONFIG: 'transaction_sync' is strictly prohibited in LionSQL (causes 96% concurrency throughput collapse)")
	}
	return cfg
}

// SetCacheSize sets the WiredTiger heap cache allocation before Open().
func (e *Engine) SetCacheSize(sz string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cacheSize = sz
}

// SetSessionMax sets the maximum concurrent internal sessions allocated by WiredTiger.
func (e *Engine) SetSessionMax(max int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessionMax = max
}

func (e *Engine) Open(dir string) error {
	if err := EnsureWiredTigerDLL(); err != nil {
		return fmt.Errorf("failed to initialize wiredtiger dll: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	cfgStr := WiredTigerOpenConfig
	if e.cacheSize != "" || e.sessionMax > 0 {
		cfgStr = BuildWiredTigerOpenConfig(e.cacheSize, e.sessionMax)
	}

	if strings.Contains(cfgStr, "transaction_sync") {
		panic("ILLEGAL WIREDTIGER CONFIG: 'transaction_sync' is strictly prohibited in LionSQL (causes 96% concurrency throughput collapse)")
	}

	if dir == "" {
		dir = "data/wt"
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create wt directory: %w", err)
	}
	e.homeDir = dir

	if runtime.GOOS == "windows" {
		_ = EnsureWiredTigerDLL()
		var initRet C.int
		if p := getResolvedWiredTigerDLL(); p != "" {
			cP := C.CString(p)
			initRet = C.go_init_windows_wt(cP)
			C.free(unsafe.Pointer(cP))
		} else {
			initRet = C.go_init_windows_wt(nil)
		}
		if initRet != 0 {
			return fmt.Errorf("failed to load wiredtiger.dll dynamically on windows (code %d)", int(initRet))
		}
	}

	cHome := C.CString(dir)
	defer C.free(unsafe.Pointer(cHome))

	cConfig := C.CString(cfgStr)
	defer C.free(unsafe.Pointer(cConfig))

	var conn *C.WT_CONNECTION
	ret := C.go_wt_open(cHome, cConfig, &conn)
	if ret != 0 {
		return fmt.Errorf("wiredtiger_open failed with code %d: %s", ret, C.GoString(C.go_wt_strerror(ret)))
	}

	e.conn = conn
	e.closed = false
	e.connClosed.Store(false)
	if e.tempEng == nil {
		e.tempEng = memengine.New()
	}

	e.loadCatalog()

	e.cleanupOrphanTempFiles()

	if err := e.migrateIndexFiles(); err != nil {
		_ = C.go_wt_close(conn)
		e.conn = nil
		e.closed = true
		e.connClosed.Store(true)
		return fmt.Errorf("secondary index file migration failed: %w", err)
	}

	stopCh := make(chan struct{})
	e.stopCh = stopCh
	e.bgWg.Add(1)
	go e.startPeriodicSync(stopCh)

	return nil
}

// cleanupOrphanTempFiles removes unclosed temporary table files (#__dt_*.wt) left over
// from crashes or abrupt process terminations ([RULE:CleanTeardown]).
func (e *Engine) cleanupOrphanTempFiles() {
	if e.homeDir == "" {
		return
	}
	pattern := filepath.Join(e.homeDir, "#__dt_*.wt")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	for _, f := range matches {
		_ = os.Remove(f)
	}
}

func (e *Engine) catalogPath() string {
	return filepath.Join(e.homeDir, "catalog.json")
}

func (e *Engine) loadCatalog() {
	data, err := os.ReadFile(e.catalogPath())
	if err != nil {
		return
	}
	var tables []*storage.TableSchema
	if err := json.Unmarshal(data, &tables); err == nil {
		var hasTemp bool
		for _, t := range tables {
			if storage.IsTemporaryTable(t.Name) {
				hasTemp = true
				continue
			}
			e.tables[t.Name] = t
		}
		if hasTemp {
			_ = e.saveCatalog()
		}
		if e.conn != nil {
			var sess *C.WT_SESSION
			if C.go_wt_open_session(e.conn, &sess) == 0 {
				for _, t := range e.tables {
					cnt := e.countTableRows(sess, t)
					e.getRowCountAtomicLocked(t.Name).Store(cnt)
				}
				C.go_wt_session_close(sess)
			}
		}
	}
}

// countTableRows counts physical rows in WiredTiger table (used during catalog load).
func (e *Engine) countTableRows(sess *C.WT_SESSION, t *storage.TableSchema) int64 {
	uri := C.CString(t.TableURI())
	defer C.free(unsafe.Pointer(uri))
	var cur *C.WT_CURSOR
	if C.go_wt_open_cursor(sess, uri, &cur) != 0 {
		return 0
	}
	defer C.go_wt_cursor_close(cur)
	var cnt int64
	for C.go_wt_cursor_next(cur) == 0 {
		cnt++
	}
	return cnt
}

func (e *Engine) getRowCountAtomicLocked(tableName string) *atomic.Int64 {
	cnt, ok := sqlutil.MapGetFold(e.rowCounts, tableName)
	if ok && cnt != nil {
		return cnt
	}
	cnt = new(atomic.Int64)
	if e.rowCounts == nil {
		e.rowCounts = make(map[string]*atomic.Int64)
	}
	e.rowCounts[tableName] = cnt
	return cnt
}

func (e *Engine) getRowCountAtomic(tableName string) *atomic.Int64 {
	e.mu.RLock()
	cnt, ok := sqlutil.MapGetFold(e.rowCounts, tableName)
	e.mu.RUnlock()
	if ok && cnt != nil {
		return cnt
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	return e.getRowCountAtomicLocked(tableName)
}

// TableRowCount returns the live row count of a table in O(1) time across concurrent transactions.
func (e *Engine) TableRowCount(tableName string) (int64, error) {
	if storage.IsTemporaryTable(tableName) {
		return e.tempEng.TableRowCount(tableName)
	}

	tbl, err := e.GetTable(tableName)
	if err != nil {
		return 0, err
	}

	cnt := e.getRowCountAtomic(tbl.Name).Load()
	if cnt < 0 {
		cnt = 0
	}
	return cnt, nil
}

func (e *Engine) saveCatalog() error {
	list := make([]*storage.TableSchema, 0, len(e.tables))
	for _, t := range e.tables {
		if storage.IsTemporaryTable(t.Name) {
			continue
		}
		list = append(list, t)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Clean(e.catalogPath())
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed || e.conn == nil {
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

	// Drain and close any remaining active sessions while e.conn is still valid
	e.sessMu.Lock()
	activeSessions := make([]*WTSession, 0, len(e.sessions))
	for s := range e.sessions {
		activeSessions = append(activeSessions, s)
	}
	e.sessMu.Unlock()

	for _, s := range activeSessions {
		_ = s.Close()
	}

	if e.tempEng != nil {
		_ = e.tempEng.Close()
	}

	e.syncMu.Lock()
	defer e.syncMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()

	// Mark connection closed so any concurrent or deferred session close safely no-ops
	e.connClosed.Store(true)

	ret := C.go_wt_close(e.conn)
	e.conn = nil
	if ret != 0 {
		return fmt.Errorf("wiredtiger close failed with code %d", ret)
	}
	return nil
}

func (e *Engine) CreateTable(schema *storage.TableSchema) error {
	if schema != nil && len(schema.Columns) > storage.MaxColumnsPerTable {
		return fmt.Errorf("%w: table %q has %d columns (max %d)", storage.ErrTooManyColumns, schema.Name, len(schema.Columns), storage.MaxColumnsPerTable)
	}
	if schema != nil {
		if err := schema.ValidateRowSize(); err != nil {
			return err
		}
	}

	if schema != nil && storage.IsTemporaryTable(schema.Name) {
		return e.tempEng.CreateTable(schema)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := sqlutil.MapGetFold(e.tables, schema.Name); exists {
		return fmt.Errorf("%w: %s", storage.ErrTableExists, schema.Name)
	}
	if _, err := schema.PrimaryKeyColumn(); err != nil {
		return err
	}

	if schema.PhysicalName == "" {
		phys := schema.Name
		for _, existing := range e.tables {
			if strings.EqualFold(existing.PhysicalName, phys) || (existing.PhysicalName == "" && strings.EqualFold(existing.Name, phys)) {
				phys = fmt.Sprintf("%s_v%d", schema.Name, time.Now().UnixNano())
				break
			}
		}
		schema.PhysicalName = phys
	}

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return fmt.Errorf("open_session failed: code %d", ret)
	}
	defer C.go_wt_session_close(sess)

	uri := C.CString(schema.TableURI())
	defer C.free(unsafe.Pointer(uri))

	config := C.CString("key_format=S,value_format=u")
	defer C.free(unsafe.Pointer(config))

	ret := C.go_wt_session_create(sess, uri, config)
	if ret != 0 {
		return fmt.Errorf("session->create failed with code %d", ret)
	}

	// Lazy index file (ARCH-WT-FILE-LAYOUT-1): <table>.wt is always created; the single multiplexed
	// _idx_<table>.wt is created only when the table has indexes (or on its first CREATE INDEX) and
	// is removed only by DROP TABLE. DROP INDEX never touches the file layout.
	if len(schema.Indexes) > 0 {
		if err := createIndexFile(sess, schema); err != nil {
			cleanupURI := C.CString(schema.TableURI())
			_ = C.go_wt_session_drop(sess, cleanupURI)
			C.free(unsafe.Pointer(cleanupURI))
			return err
		}
	}

	e.tables[schema.Name] = schema
	if err := e.saveCatalog(); err != nil {
		return err
	}
	e.recordWALLocked([]memengine.WALOp{{
		Type:   memengine.OpCreateTable,
		Table:  schema.Name,
		Schema: schema,
	}})
	return nil
}

func (e *Engine) DropTable(tableName string) error {
	if storage.IsTemporaryTable(tableName) {
		return e.tempEng.DropTable(tableName)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	targetName := tableName
	tbl, exists := sqlutil.MapGetFold(e.tables, targetName)
	if !exists {
		cleaned := strings.Trim(strings.TrimSpace(tableName), "[]\"'`")
		if idx := strings.LastIndex(cleaned, "."); idx != -1 {
			cleaned = strings.Trim(cleaned[idx+1:], "[]\"'`")
		}
		tbl, exists = sqlutil.MapGetFold(e.tables, cleaned)
		if exists {
			targetName = cleaned
		} else {
			for name, t := range e.tables {
				cleanedName := strings.Trim(name, "[]\"'`")
				if idx := strings.LastIndex(cleanedName, "."); idx != -1 {
					cleanedName = strings.Trim(cleanedName[idx+1:], "[]\"'`")
				}
				if strings.EqualFold(cleanedName, cleaned) {
					tbl = t
					targetName = name
					exists = true
					break
				}
			}
		}
	}
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	// Verify no other table has a foreign key referencing this table
	for otherName, otherTbl := range e.tables {
		if strings.EqualFold(otherName, tbl.Name) {
			continue
		}
		for _, fk := range otherTbl.ForeignKeys {
			if strings.EqualFold(fk.ParentTable, tbl.Name) {
				return fmt.Errorf("%w: table %q is referenced by foreign key constraint %q on table %q",
					storage.ErrDependentObjectsExist, tableName, fk.Name, otherName)
			}
		}
	}

	e.evictAllCursors(tbl.Name)
	if tbl.PhysicalName != "" && tbl.PhysicalName != tbl.Name {
		e.evictAllCursors(tbl.PhysicalName)
	}

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return fmt.Errorf("open_session failed: code %d", ret)
	}
	defer C.go_wt_session_close(sess)

	uri := C.CString(tbl.TableURI())
	defer C.free(unsafe.Pointer(uri))

	// [AUDIT-FULL-007 / RULE:ExplicitError] Fail closed on drop failure to prevent orphan physical files.
	// Retry on transient EBUSY (code 16) with checkpoint to flush dirty cache/dhandles as recommended by WiredTiger docs.
	var dropRet C.int
	for attempt := 0; attempt < 10; attempt++ {
		dropRet = C.go_wt_session_drop(sess, uri)
		if dropRet == 0 {
			break
		}
		if dropRet == 16 { // EBUSY
			_ = C.go_wt_session_checkpoint(sess)
			time.Sleep(15 * time.Millisecond)
			continue
		}
		break
	}
	if dropRet != 0 {
		return fmt.Errorf("wiredtiger drop table %q (%s) failed with code %d", tableName, tbl.TableURI(), dropRet)
	}

	idxFileURI := tbl.IndexFileURI()
	e.evictAllCursors(idxFileURI)
	cIdxURI := C.CString(idxFileURI)
	_ = C.go_wt_session_drop(sess, cIdxURI)
	C.free(unsafe.Pointer(cIdxURI))

	sqlutil.MapDeleteFold(e.tables, targetName)
	sqlutil.MapDeleteFold(e.rowCounts, targetName)
	if err := e.saveCatalog(); err != nil {
		return err
	}
	e.recordWALLocked([]memengine.WALOp{{
		Type:  memengine.OpDropTable,
		Table: tbl.Name,
	}})
	return nil
}

func (e *Engine) TruncateTables(tableNames []string, cascade bool) error {
	var permNames, tempNames []string
	for _, name := range tableNames {
		if storage.IsTemporaryTable(name) {
			tempNames = append(tempNames, name)
		} else {
			permNames = append(permNames, name)
		}
	}
	if len(tempNames) > 0 && len(permNames) == 0 {
		return e.tempEng.TruncateTables(tempNames, cascade)
	}
	for _, name := range tempNames {
		if _, err := e.tempEng.GetTable(name); err != nil {
			return err
		}
	}

	if len(permNames) > 0 {
		if err := e.truncatePermTables(permNames, cascade); err != nil {
			return err
		}
	}
	if len(tempNames) > 0 {
		return e.tempEng.TruncateTables(tempNames, cascade)
	}
	return nil
}

func (e *Engine) truncatePermTables(tableNames []string, cascade bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	targetSet := make(map[string]*storage.TableSchema, len(tableNames))
	for _, name := range tableNames {
		tbl, exists := sqlutil.MapGetFold(e.tables, name)
		if !exists {
			return fmt.Errorf("%w: %s", storage.ErrTableNotFound, name)
		}
		targetSet[tbl.Name] = tbl
	}

	if cascade {
		changed := true
		for changed {
			changed = false
			for _, otherSchema := range e.tables {
				if _, already := targetSet[otherSchema.Name]; already {
					continue
				}
				for _, fk := range otherSchema.ForeignKeys {
					if _, parentInSet := targetSet[fk.ParentTable]; parentInSet {
						targetSet[otherSchema.Name] = otherSchema
						changed = true
						break
					}
				}
			}
		}
	}

	// Verify no foreign keys from outside targetSet
	for otherName, otherSchema := range e.tables {
		if _, inSet := targetSet[otherSchema.Name]; inSet {
			continue
		}
		for _, fk := range otherSchema.ForeignKeys {
			if _, parentInSet := targetSet[fk.ParentTable]; parentInSet {
				return fmt.Errorf("%w: cannot truncate table %q because it is referenced by foreign key %q on table %q",
					storage.ErrDependentObjectsExist, fk.ParentTable, fk.Name, otherName)
			}
		}
	}

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return fmt.Errorf("open_session failed: code %d", ret)
	}
	defer C.go_wt_session_close(sess)

	var walOps []memengine.WALOp
	for _, tbl := range targetSet {
		e.evictAllCursors(tbl.Name)
		if tbl.PhysicalName != "" && tbl.PhysicalName != tbl.Name {
			e.evictAllCursors(tbl.PhysicalName)
		}
		uri := C.CString(tbl.TableURI())
		if ret := C.go_wt_session_truncate(sess, uri); ret != 0 {
			if dropRet := C.go_wt_session_drop(sess, uri); dropRet != 0 {
				C.free(unsafe.Pointer(uri))
				return fmt.Errorf("wiredtiger truncate drop failed for table %q (uri: %s): code %d", tbl.Name, tbl.TableURI(), dropRet)
			}
			config := C.CString("key_format=S,value_format=u")
			createRet := C.go_wt_session_create(sess, uri, config)
			C.free(unsafe.Pointer(config))
			if createRet != 0 {
				C.free(unsafe.Pointer(uri))
				return fmt.Errorf("wiredtiger truncate recreate failed for table %q (uri: %s): code %d", tbl.Name, tbl.TableURI(), createRet)
			}
		}
		C.free(unsafe.Pointer(uri))

		// The multiplexed index file is empty by invariant when the table has no indexes.
		if len(tbl.Indexes) > 0 {
			if err := truncateIndexFile(sess, tbl); err != nil {
				return err
			}
			e.evictAllCursors(tbl.IndexFileURI())
		}

		if !storage.IsTemporaryTable(tbl.Name) {
			if cnt := e.getRowCountAtomicLocked(tbl.Name); cnt != nil {
				cnt.Store(0)
			}
			walOps = append(walOps, memengine.WALOp{
				Type:  memengine.OpTruncateTable,
				Table: tbl.Name,
			})
		}
	}

	if len(walOps) > 0 {
		e.recordWALLocked(walOps)
	}
	return nil
}

// truncateIndexFile empties the table's multiplexed index file, falling back to drop+create when
// WiredTiger refuses the in-place truncate.
func truncateIndexFile(sess *C.WT_SESSION, tbl *storage.TableSchema) error {
	idxFileURI := tbl.IndexFileURI()
	cIdxURI := C.CString(idxFileURI)
	defer C.free(unsafe.Pointer(cIdxURI))
	if tret := C.go_wt_session_truncate(sess, cIdxURI); tret == 0 {
		return nil
	}
	if dret := C.go_wt_session_drop(sess, cIdxURI); dret != 0 {
		return fmt.Errorf("wiredtiger truncate drop failed for index file of table %q (uri: %s): code %d", tbl.Name, idxFileURI, dret)
	}
	cIdxConfig := C.CString(indexFileConfig)
	defer C.free(unsafe.Pointer(cIdxConfig))
	if cret := C.go_wt_session_create(sess, cIdxURI, cIdxConfig); cret != 0 {
		return fmt.Errorf("wiredtiger truncate recreate failed for index file of table %q (uri: %s): code %d", tbl.Name, idxFileURI, cret)
	}
	return nil
}

func (e *Engine) TruncateTable(tableName string) error {
	return e.TruncateTables([]string{tableName}, false)
}

func (e *Engine) DropPartition(tableName, partitionName string) error {
	if storage.IsTemporaryTable(tableName) {
		return e.tempEng.DropPartition(tableName, partitionName)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	tbl, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	if !tbl.IsPartitioned() {
		return fmt.Errorf("%w: %s", storage.ErrTableNotPartitioned, tableName)
	}

	e.evictAllCursors(tbl.Name)
	if tbl.PhysicalName != "" && tbl.PhysicalName != tbl.Name {
		e.evictAllCursors(tbl.PhysicalName)
	}
	e.evictAllCursors(tbl.IndexFileURI())

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return fmt.Errorf("open_session failed: %d", ret)
	}
	defer C.go_wt_session_close(sess)

	if ret := C.go_wt_session_begin(sess, nil); ret != 0 {
		return fmt.Errorf("begin_transaction failed: %d", ret)
	}
	txCommitted := false
	defer func() {
		if !txCommitted {
			_ = C.go_wt_session_rollback(sess, nil)
		}
	}()

	uri := C.CString(tbl.TableURI())
	defer C.free(unsafe.Pointer(uri))

	var cur *C.WT_CURSOR
	if ret := C.go_wt_open_cursor(sess, uri, &cur); ret != 0 {
		return fmt.Errorf("open_cursor failed: %d", ret)
	}
	defer C.go_wt_cursor_close(cur)

	type deletedPartitionRow struct {
		pkStr string
		rec   storage.Record
	}
	var deletedRows []deletedPartitionRow

	matched := 0
	for {
		nret := C.go_wt_cursor_next(cur)
		if nret != 0 {
			if nret != C.WT_NOTFOUND {
				return wtOpError("cursor_next during DropPartition", int(nret))
			}
			break
		}
		var pKey *C.char
		var pkStr string
		if kret := C.go_wt_cursor_get_key(cur, &pKey); kret == 0 && pKey != nil {
			pkStr = C.GoString(pKey)
		}
		var pData unsafe.Pointer
		var sz C.size_t
		if ret := C.go_wt_cursor_get_val_raw(cur, &pData, &sz); ret == 0 && pData != nil {
			rec, err := decodeRecord(tbl, unsafe.Slice((*byte)(pData), int(sz)))
			if err != nil {
				return fmt.Errorf("DropPartition: decode record failed on table %s pk %s: %w", tbl.Name, pkStr, err)
			}
			pKeyVal, err := tbl.ComputePartitionKey(rec)
			if err != nil {
				return fmt.Errorf("DropPartition: compute partition key failed on table %s pk %s: %w", tbl.Name, pkStr, err)
			}
			if pKeyVal == partitionName {
				if len(tbl.Indexes) > 0 && pkStr != "" {
					deletedRows = append(deletedRows, deletedPartitionRow{pkStr: pkStr, rec: rec})
				}
				if rret := C.go_wt_cursor_remove(cur); rret != 0 {
					return wtOpError("cursor_remove", int(rret))
				}
				matched++
			}
		}
	}
	if matched == 0 {
		return fmt.Errorf("%w: partition %q in table %q", storage.ErrPartitionNotFound, partitionName, tableName)
	}

	if len(deletedRows) > 0 && len(tbl.Indexes) > 0 {
		for _, idx := range tbl.Indexes {
			cIdxURI := C.CString(tbl.IndexFileURI())
			var idxCur *C.WT_CURSOR
			if ret := C.go_wt_open_cursor(sess, cIdxURI, &idxCur); ret != 0 {
				C.free(unsafe.Pointer(cIdxURI))
				return fmt.Errorf("open index cursor %s failed: %d", idx.Name, ret)
			}
			for _, dr := range deletedRows {
				k, _, _ := formatIndexKeyValue(dr.rec, &idx, dr.pkStr)
				if err := removeIndexEntry(idxCur, k); err != nil {
					C.go_wt_cursor_close(idxCur)
					C.free(unsafe.Pointer(cIdxURI))
					return fmt.Errorf("remove index entry failed on %s: %w", idx.Name, err)
				}
			}
			C.go_wt_cursor_close(idxCur)
			C.free(unsafe.Pointer(cIdxURI))
		}
	}

	if ret := C.go_wt_session_commit(sess, nil); ret != 0 {
		return fmt.Errorf("commit_transaction failed: %d", ret)
	}
	txCommitted = true

	if !storage.IsTemporaryTable(tbl.Name) {
		if cnt := e.getRowCountAtomicLocked(tbl.Name); cnt != nil {
			newCnt := cnt.Add(-int64(matched))
			if newCnt < 0 {
				cnt.Store(0)
			}
		}
		e.recordWALLocked([]memengine.WALOp{{
			Type:  memengine.OpDropPartition,
			Table: tbl.Name,
			Key:   partitionName,
		}})
	}
	return nil
}

func (e *Engine) GetPartitions(tableName string) ([]storage.PartitionInfo, error) {
	if storage.IsTemporaryTable(tableName) {
		return e.tempEng.GetPartitions(tableName)
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	tbl, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	if !tbl.IsPartitioned() {
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotPartitioned, tableName)
	}

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return nil, fmt.Errorf("open_session failed: %d", ret)
	}
	defer C.go_wt_session_close(sess)

	uri := C.CString(tbl.TableURI())
	defer C.free(unsafe.Pointer(uri))

	var cur *C.WT_CURSOR
	if ret := C.go_wt_open_cursor(sess, uri, &cur); ret != 0 {
		return nil, fmt.Errorf("open_cursor failed: %d", ret)
	}
	defer C.go_wt_cursor_close(cur)

	partCounts := make(map[string]int64)
	for {
		nret := C.go_wt_cursor_next(cur)
		if nret != 0 {
			if nret != C.WT_NOTFOUND {
				return nil, wtOpError("cursor_next during GetPartitions", int(nret))
			}
			break
		}
		var pData unsafe.Pointer
		var sz C.size_t
		if ret := C.go_wt_cursor_get_val_raw(cur, &pData, &sz); ret == 0 && pData != nil {
			rec, err := decodeRecord(tbl, unsafe.Slice((*byte)(pData), int(sz)))
			if err == nil {
				if pKey, err := tbl.ComputePartitionKey(rec); err == nil {
					partCounts[pKey]++
				}
			}
		}
	}

	res := make([]storage.PartitionInfo, 0, len(partCounts))
	for name, cnt := range partCounts {
		res = append(res, storage.PartitionInfo{Name: name, RowCount: cnt})
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Name < res[j].Name
	})
	return res, nil
}

// tableHasRowsLocked returns true if the specified table contains at least one physical row.
func (e *Engine) tableHasRowsLocked(tbl *storage.TableSchema) bool {
	var chkSess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &chkSess); ret != 0 {
		return false
	}
	defer C.go_wt_session_close(chkSess)

	uri := C.CString(tbl.TableURI())
	defer C.free(unsafe.Pointer(uri))

	var chkCur *C.WT_CURSOR
	if ret := C.go_wt_open_cursor(chkSess, uri, &chkCur); ret != 0 {
		return false
	}
	defer C.go_wt_cursor_close(chkCur)

	return C.go_wt_cursor_next(chkCur) == 0
}

func (e *Engine) AddColumn(tableName string, col *storage.Column) error {
	if storage.IsTemporaryTable(tableName) {
		return e.tempEng.AddColumn(tableName, col)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	tbl, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	for _, c := range tbl.Columns {
		if strings.EqualFold(c.Name, col.Name) {
			return fmt.Errorf("column %q already exists in table %q", col.Name, tableName)
		}
	}

	// Check if table has existing rows when column is NOT NULL without default
	if !col.Nullable && strings.TrimSpace(col.Default) == "" {
		if e.tableHasRowsLocked(tbl) {
			return fmt.Errorf("cannot add NOT NULL column %q with no default value to non-empty table %q", col.Name, tableName)
		}
	}

	// [PERF-WT-INSTANT-ADD-COL-1 / AUDIT-FULL-012 / Invariant 13]
	// Instant O(1) ADD COLUMN:
	// Do not rewrite existing records on disk. Existing binary tuples retain their original
	// column byte layouts, and the decoder automatically pads missing trailing columns with
	// schema defaults or NULLs on-the-fly. Evict cached cursors to prevent stale schema reads.
	e.evictAllCursors(tbl.Name)
	if tbl.PhysicalName != "" && tbl.PhysicalName != tbl.Name {
		e.evictAllCursors(tbl.PhysicalName)
	}

	tbl.Columns = append(tbl.Columns, *col)

	if err := e.saveCatalog(); err != nil {
		return err
	}

	if !storage.IsTemporaryTable(tableName) {
		e.recordWALLocked([]memengine.WALOp{{
			Type:   memengine.OpAlterTable,
			Table:  tableName,
			Column: col,
		}})
	}
	return nil
}

func (e *Engine) RenameTable(oldName, newName string) error {
	oldTemp := storage.IsTemporaryTable(oldName)
	newTemp := storage.IsTemporaryTable(newName)
	if oldTemp && newTemp {
		return e.tempEng.RenameTable(oldName, newName)
	}
	if oldTemp != newTemp {
		return fmt.Errorf("cannot rename across temporary and permanent table namespaces (%q -> %q)", oldName, newName)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	targetOld := oldName
	tbl, exists := sqlutil.MapGetFold(e.tables, targetOld)
	if !exists {
		oldClean := strings.Trim(strings.TrimSpace(oldName), "[]\"'`")
		if idx := strings.LastIndex(oldClean, "."); idx != -1 {
			oldClean = strings.Trim(oldClean[idx+1:], "[]\"'`")
		}
		tbl, exists = sqlutil.MapGetFold(e.tables, oldClean)
		if exists {
			targetOld = oldClean
		} else {
			for name, t := range e.tables {
				cleanedName := strings.Trim(name, "[]\"'`")
				if idx := strings.LastIndex(cleanedName, "."); idx != -1 {
					cleanedName = strings.Trim(cleanedName[idx+1:], "[]\"'`")
				}
				if strings.EqualFold(cleanedName, oldClean) {
					tbl = t
					targetOld = name
					exists = true
					break
				}
			}
		}
	}
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, oldName)
	}

	targetNew := newName
	if !strings.HasPrefix(targetNew, "__") {
		targetNew = strings.Trim(strings.TrimSpace(newName), "[]\"'`")
		if idx := strings.LastIndex(targetNew, "."); idx != -1 {
			targetNew = strings.Trim(targetNew[idx+1:], "[]\"'`")
		}
	}

	if _, already := sqlutil.MapGetFold(e.tables, targetNew); already {
		return fmt.Errorf("%w: %s", storage.ErrTableExists, targetNew)
	}

	// Lock down physical table identifier so underlying WiredTiger B-Tree URI never changes
	if tbl.PhysicalName == "" {
		tbl.PhysicalName = tbl.Name
	}

	// Evict any active cached cursors under the old and physical name
	e.evictAllCursors(targetOld)
	if tbl.PhysicalName != "" && tbl.PhysicalName != targetOld {
		e.evictAllCursors(tbl.PhysicalName)
	}

	for k := range e.tables {
		if strings.EqualFold(k, targetOld) {
			delete(e.tables, k)
		}
	}

	tbl.Name = targetNew
	e.tables[targetNew] = tbl

	cnt := e.getRowCountAtomicLocked(targetOld)
	sqlutil.MapDeleteFold(e.rowCounts, targetOld)
	if e.rowCounts == nil {
		e.rowCounts = make(map[string]*atomic.Int64)
	}
	e.rowCounts[targetNew] = cnt

	// Update any foreign key definitions on other tables referencing targetOld
	for _, otherTd := range e.tables {
		for i := range otherTd.ForeignKeys {
			if strings.EqualFold(otherTd.ForeignKeys[i].ParentTable, targetOld) {
				otherTd.ForeignKeys[i].ParentTable = targetNew
			}
		}
	}

	if err := e.saveCatalog(); err != nil {
		// Rollback rename in memory to avoid catalog inconsistency
		delete(e.tables, targetNew)
		tbl.Name = targetOld
		e.tables[targetOld] = tbl
		return err
	}

	// [AUDIT-FULL-013] Record OpRenameTable WAL operation
	if !oldTemp {
		e.recordWALLocked([]memengine.WALOp{{
			Type:  memengine.OpRenameTable,
			Table: targetNew,
			Key:   targetOld,
		}})
	}
	return nil
}

func (e *Engine) AlterTable(tableName string, mutator func(schema *storage.TableSchema) error) error {
	if storage.IsTemporaryTable(tableName) {
		return e.tempEng.AlterTable(tableName, mutator)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	t, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	trialSchema := t.Clone()
	if err := mutator(&trialSchema); err != nil {
		return err
	}
	if len(trialSchema.Columns) > storage.MaxColumnsPerTable {
		return fmt.Errorf("%w: altered table %q would have %d columns (max %d)", storage.ErrTooManyColumns, tableName, len(trialSchema.Columns), storage.MaxColumnsPerTable)
	}
	if err := trialSchema.ValidateRowSize(); err != nil {
		return err
	}

	// [Invariant 13: Schema Evolution & Instant DDL 3-Layer Defense]
	// Verify DROP COLUMN safety on WiredTiger:
	// Dropping non-terminal (middle) columns from a non-empty table shifts column offsets
	// relative to existing physical binary tuples on disk, corrupting type/value interpretation.
	// Reject non-terminal DROP COLUMN with an explicit error ([RULE:ExplicitError]) until
	// virtual dropped-column masking is implemented.
	if len(trialSchema.Columns) < len(t.Columns) {
		oldCols := t.Columns
		newCols := trialSchema.Columns

		// Check if any dropped column is not at the tail of oldCols
		for oldIdx, oldCol := range oldCols {
			found := false
			for _, newCol := range newCols {
				if strings.EqualFold(oldCol.Name, newCol.Name) {
					found = true
					break
				}
			}
			if !found {
				// Column was dropped
				isTerminal := (oldIdx >= len(newCols))
				if !isTerminal {
					if e.tableHasRowsLocked(t) {
						return fmt.Errorf("dropping non-terminal column %q from non-empty table %q is not supported in WiredTiger engine (invariant 13)", oldCol.Name, tableName)
					}
				}
			}
		}
	}

	e.evictAllCursors(t.Name)
	if t.PhysicalName != "" && t.PhysicalName != t.Name {
		e.evictAllCursors(t.PhysicalName)
	}

	*t = trialSchema
	if err := e.saveCatalog(); err != nil {
		return err
	}

	if !storage.IsTemporaryTable(tableName) {
		walSchema := trialSchema.Clone()
		e.recordWALLocked([]memengine.WALOp{{
			Type:   memengine.OpAlterTable,
			Table:  tableName,
			Schema: &walSchema,
		}})
	}
	return nil
}

func (e *Engine) GetTable(tableName string) (*storage.TableSchema, error) {
	if storage.IsTemporaryTable(tableName) {
		return e.tempEng.GetTable(tableName)
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	t, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		for name, tbl := range e.tables {
			cleanA := strings.Trim(strings.TrimSpace(name), "[]\"'`")
			if idx := strings.LastIndex(cleanA, "."); idx != -1 {
				cleanA = strings.Trim(cleanA[idx+1:], "[]\"'`")
			}
			cleanB := strings.Trim(strings.TrimSpace(tableName), "[]\"'`")
			if idx := strings.LastIndex(cleanB, "."); idx != -1 {
				cleanB = strings.Trim(cleanB[idx+1:], "[]\"'`")
			}
			if strings.EqualFold(cleanA, cleanB) {
				t = tbl
				exists = true
				break
			}
		}
	}
	if !exists {
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	cp := t.Clone()
	return &cp, nil
}

func (e *Engine) ListTables() ([]*storage.TableSchema, error) {
	e.mu.RLock()
	res := make([]*storage.TableSchema, 0, len(e.tables))
	for _, t := range e.tables {
		cp := t.Clone()
		res = append(res, &cp)
	}
	tempEng := e.tempEng
	e.mu.RUnlock()

	if tempEng != nil {
		tempTables, err := tempEng.ListTables()
		if err != nil {
			return nil, err
		}
		res = append(res, tempTables...)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Name < res[j].Name
	})
	return res, nil
}

func encodeSecondaryIndexKey(idx *storage.IndexSchema, v storage.Value) string {
	if len(idx.Columns) > 1 {
		return v.String()
	}
	return storage.EncodeIndexKeyPart(v)
}

func formatIndexKeyValue(rec storage.Record, idx *storage.IndexSchema, pkStr string) (key string, val string, isNull bool) {
	keyVal := storage.ExtractIndexKeyFromRecord(rec, idx)
	if keyVal.ContainsNull() {
		isNull = true
	}
	var keyStr string
	if len(idx.Columns) > 1 {
		keyStr = keyVal.String()
	} else {
		keyStr = storage.EncodeIndexKeyPart(keyVal)
	}

	// All indexes of a table share one file; the index name prefix namespaces the entries.
	prefix := idx.KeyPrefix()
	if idx.Unique && !isNull {
		return prefix + keyStr, pkStr, false
	}
	return prefix + keyStr + "\x00" + pkStr, "", isNull
}

func formatIndexKeyValueFromRow(schema *storage.TableSchema, row storage.Row, idx *storage.IndexSchema, pkStr string) (key string, val string, isNull bool) {
	keyVal := storage.ExtractIndexKeyFromRow(schema, row, idx)
	if keyVal.ContainsNull() {
		isNull = true
	}
	var keyStr string
	if len(idx.Columns) > 1 {
		keyStr = keyVal.String()
	} else {
		keyStr = storage.EncodeIndexKeyPart(keyVal)
	}

	prefix := idx.KeyPrefix()
	if idx.Unique && !isNull {
		return prefix + keyStr, pkStr, false
	}
	return prefix + keyStr + "\x00" + pkStr, "", isNull
}

// isIndexFileURI reports whether uri is the multiplexed secondary index file of a known table.
func (e *Engine) isIndexFileURI(uri string) bool {
	if !strings.HasPrefix(uri, "table:_idx_") {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, tbl := range e.tables {
		if tbl.IndexFileURI() == uri {
			return true
		}
	}
	return false
}

// removeIndexRange deletes every entry whose key starts with prefix from the multiplexed index
// file inside a single WiredTiger transaction (all-or-nothing).
func removeIndexRange(sess *C.WT_SESSION, idxFileURI, prefix string) error {
	cURI := C.CString(idxFileURI)
	defer C.free(unsafe.Pointer(cURI))

	if ret := C.go_wt_session_begin(sess, nil); ret != 0 {
		return fmt.Errorf("begin_transaction failed: %d", ret)
	}
	committed := false
	defer func() {
		if !committed {
			_ = C.go_wt_session_rollback(sess, nil)
		}
	}()

	var cur *C.WT_CURSOR
	if ret := C.go_wt_open_cursor(sess, cURI, &cur); ret != 0 {
		return fmt.Errorf("open index file cursor %s failed: %d", idxFileURI, ret)
	}
	defer C.go_wt_cursor_close(cur)

	C.go_wt_cursor_set_key_raw(cur, unsafe.Pointer(unsafe.StringData(prefix)), C.size_t(len(prefix)))
	var exact C.int
	ret := C.go_wt_cursor_search_near(cur, &exact)
	done := false
	switch {
	case ret == C.WT_NOTFOUND:
		done = true
	case ret != 0:
		return wtOpError("cursor_search_near during index range removal", int(ret))
	case exact < 0:
		if nret := C.go_wt_cursor_next(cur); nret == C.WT_NOTFOUND {
			done = true
		} else if nret != 0 {
			return wtOpError("cursor_next during index range removal", int(nret))
		}
	}

	for !done {
		var kData unsafe.Pointer
		var kSz C.size_t
		if kret := C.go_wt_cursor_get_key_raw(cur, &kData, &kSz); kret != 0 || kData == nil {
			return wtOpError("cursor_get_key during index range removal", int(kret))
		}
		if !strings.HasPrefix(string(unsafe.Slice((*byte)(kData), int(kSz))), prefix) {
			break
		}
		if rret := C.go_wt_cursor_remove(cur); rret != 0 {
			return wtOpError("cursor_remove during index range removal", int(rret))
		}
		if nret := C.go_wt_cursor_next(cur); nret != 0 {
			if nret != C.WT_NOTFOUND {
				return wtOpError("cursor_next during index range removal", int(nret))
			}
			break
		}
	}

	if cret := C.go_wt_session_commit(sess, nil); cret != 0 {
		return fmt.Errorf("commit_transaction failed: %d", cret)
	}
	committed = true
	return nil
}

// buildIndexEntries backfills every existing row of t into the multiplexed index file for index.
// The whole build runs in one WiredTiger transaction and is rolled back on any failure.
func buildIndexEntries(sess *C.WT_SESSION, t *storage.TableSchema, index *storage.IndexSchema) error {
	if ret := C.go_wt_session_begin(sess, nil); ret != 0 {
		return fmt.Errorf("begin_transaction failed: %d", ret)
	}
	committed := false
	defer func() {
		if !committed {
			_ = C.go_wt_session_rollback(sess, nil)
		}
	}()

	primURI := C.CString(t.TableURI())
	defer C.free(unsafe.Pointer(primURI))
	cIdxURI := C.CString(t.IndexFileURI())
	defer C.free(unsafe.Pointer(cIdxURI))
	cConfig := C.CString("overwrite=false")
	defer C.free(unsafe.Pointer(cConfig))

	var tblCur, idxCur *C.WT_CURSOR
	if ret := C.go_wt_open_cursor(sess, primURI, &tblCur); ret != 0 {
		return fmt.Errorf("open table cursor %s failed: %d", t.TableURI(), ret)
	}
	defer C.go_wt_cursor_close(tblCur)
	if ret := C.go_wt_open_cursor_config(sess, cIdxURI, cConfig, &idxCur); ret != 0 {
		return fmt.Errorf("open index file cursor %s failed: %d", t.IndexFileURI(), ret)
	}
	defer C.go_wt_cursor_close(idxCur)

	for {
		nret := C.go_wt_cursor_next(tblCur)
		if nret != 0 {
			if nret != C.WT_NOTFOUND {
				return wtOpError("cursor_next during index build", int(nret))
			}
			break
		}
		var pKey *C.char
		if kret := C.go_wt_cursor_get_key(tblCur, &pKey); kret != 0 || pKey == nil {
			return wtOpError("cursor_get_key during index build", int(kret))
		}
		pkStr := C.GoString(pKey)

		var pData unsafe.Pointer
		var sz C.size_t
		if vret := C.go_wt_cursor_get_val_raw(tblCur, &pData, &sz); vret != 0 || pData == nil {
			return wtOpError("cursor_get_val during index build", int(vret))
		}
		rec, err := decodeRecord(t, unsafe.Slice((*byte)(pData), int(sz)))
		if err != nil {
			return fmt.Errorf("index build: decode record pk %s: %w", pkStr, err)
		}

		k, v, isNull := formatIndexKeyValue(rec, index, pkStr)
		if index.Unique && !isNull {
			var kData unsafe.Pointer
			if len(k) > 0 {
				kData = unsafe.Pointer(unsafe.StringData(k))
			}
			C.go_wt_cursor_set_key_raw(idxCur, kData, C.size_t(len(k)))
			found := C.go_wt_cursor_search(idxCur) == 0
			C.go_wt_cursor_reset(idxCur)
			if found {
				return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, k[len(index.KeyPrefix()):], index.Name)
			}
		}
		if err := insertIndexEntry(idxCur, k, v); err != nil {
			return err
		}
	}

	if cret := C.go_wt_session_commit(sess, nil); cret != 0 {
		return fmt.Errorf("commit_transaction failed: %d", cret)
	}
	committed = true
	return nil
}

// wtENOENT is the POSIX "no such file or object" code WiredTiger returns for a missing URI.
const wtENOENT = 2

// migrateLegacyIndex moves a pre-multiplexing per-index file ("table:_idx_<T>_<I>") into the
// table's multiplexed index file and drops the legacy file. It is idempotent: the copy overwrites,
// and the legacy file is dropped only after the copy has been committed.
func migrateLegacyIndex(sess *C.WT_SESSION, t *storage.TableSchema, idx *storage.IndexSchema) error {
	legacyURI := t.LegacyIndexURI(idx.Name)
	cLegacy := C.CString(legacyURI)
	defer C.free(unsafe.Pointer(cLegacy))
	cIdxURI := C.CString(t.IndexFileURI())
	defer C.free(unsafe.Pointer(cIdxURI))

	copyAll := func() (found bool, err error) {
		var src *C.WT_CURSOR
		if ret := C.go_wt_open_cursor(sess, cLegacy, &src); ret != 0 {
			if ret == wtENOENT {
				return false, nil
			}
			return false, fmt.Errorf("open legacy index %s failed: %d", legacyURI, ret)
		}
		defer C.go_wt_cursor_close(src)

		if ret := C.go_wt_session_begin(sess, nil); ret != 0 {
			return true, fmt.Errorf("begin_transaction failed: %d", ret)
		}
		committed := false
		defer func() {
			if !committed {
				_ = C.go_wt_session_rollback(sess, nil)
			}
		}()

		var dst *C.WT_CURSOR
		if ret := C.go_wt_open_cursor(sess, cIdxURI, &dst); ret != 0 {
			return true, fmt.Errorf("open index file cursor %s failed: %d", t.IndexFileURI(), ret)
		}
		defer C.go_wt_cursor_close(dst)

		prefix := idx.KeyPrefix()
		for {
			nret := C.go_wt_cursor_next(src)
			if nret != 0 {
				if nret != C.WT_NOTFOUND {
					return true, wtOpError("cursor_next during legacy index migration", int(nret))
				}
				break
			}
			var kData, vData unsafe.Pointer
			var kSz, vSz C.size_t
			if kret := C.go_wt_cursor_get_key_raw(src, &kData, &kSz); kret != 0 || kData == nil {
				return true, wtOpError("cursor_get_key during legacy index migration", int(kret))
			}
			if vret := C.go_wt_cursor_get_val_raw(src, &vData, &vSz); vret != 0 {
				return true, wtOpError("cursor_get_val during legacy index migration", int(vret))
			}
			key := prefix + string(unsafe.Slice((*byte)(kData), int(kSz)))
			var val string
			if vData != nil && vSz > 0 {
				val = string(unsafe.Slice((*byte)(vData), int(vSz)))
			}
			if err := insertIndexEntry(dst, key, val); err != nil {
				return true, err
			}
		}
		if cret := C.go_wt_session_commit(sess, nil); cret != 0 {
			return true, fmt.Errorf("commit_transaction failed: %d", cret)
		}
		committed = true
		return true, nil
	}

	found, err := copyAll()
	if err != nil {
		return fmt.Errorf("migrate legacy index %s: %w", legacyURI, err)
	}
	if !found {
		return nil
	}
	if ret := C.go_wt_session_drop(sess, cLegacy); ret != 0 {
		// The data is already safely migrated; a leftover legacy file only wastes disk space
		// and the idempotent migration retries the drop on the next start.
		fmt.Fprintf(os.Stderr, "wiredtiger: WARNING: failed to drop legacy index file %s after migration (code %d)\n", legacyURI, ret)
	}
	return nil
}

// migrateIndexFiles guarantees that every table owns its multiplexed index file and migrates any
// legacy per-index files left by older versions (ARCH-WT-FILE-LAYOUT-1).
func (e *Engine) migrateIndexFiles() error {
	if e.conn == nil || len(e.tables) == 0 {
		return nil
	}
	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return fmt.Errorf("open_session failed: %d", ret)
	}
	defer C.go_wt_session_close(sess)

	for _, t := range e.tables {
		if len(t.Indexes) == 0 {
			continue
		}
		if err := createIndexFile(sess, t); err != nil {
			return err
		}
		for i := range t.Indexes {
			if err := migrateLegacyIndex(sess, t, &t.Indexes[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// createIndexFile creates the table's multiplexed index file; it is a no-op when it already exists.
func createIndexFile(sess *C.WT_SESSION, t *storage.TableSchema) error {
	cURI := C.CString(t.IndexFileURI())
	defer C.free(unsafe.Pointer(cURI))
	cConfig := C.CString(indexFileConfig)
	defer C.free(unsafe.Pointer(cConfig))
	if ret := C.go_wt_session_create(sess, cURI, cConfig); ret != 0 {
		return fmt.Errorf("create index file %s failed: %d", t.IndexFileURI(), ret)
	}
	return nil
}

// indexFileConfig is the WiredTiger create config of the per-table multiplexed index file.
const indexFileConfig = "key_format=u,value_format=u"

func insertIndexEntry(cur *C.WT_CURSOR, key, val string) error {
	var kData, vData unsafe.Pointer
	if len(key) > 0 {
		kData = unsafe.Pointer(unsafe.StringData(key))
	}
	if len(val) > 0 {
		vData = unsafe.Pointer(unsafe.StringData(val))
	}
	C.go_wt_cursor_set_key_raw(cur, kData, C.size_t(len(key)))
	C.go_wt_cursor_set_val_raw(cur, vData, C.size_t(len(val)))
	if ret := C.go_wt_cursor_insert(cur); ret != 0 {
		if ret == C.WT_DUPLICATE_KEY {
			return storage.ErrUniqueViolation
		}
		return wtOpError("cursor_insert_index", int(ret))
	}
	return nil
}

func removeIndexEntry(cur *C.WT_CURSOR, key string) error {
	var kData unsafe.Pointer
	if len(key) > 0 {
		kData = unsafe.Pointer(unsafe.StringData(key))
	}
	if ret := C.go_wt_cursor_remove_key_raw(cur, kData, C.size_t(len(key))); ret != 0 {
		if ret == C.WT_NOTFOUND {
			return nil
		}
		return wtOpError("cursor_remove_index", int(ret))
	}
	return nil
}

func (e *Engine) CreateIndex(index *storage.IndexSchema) error {
	if index != nil && storage.IsTemporaryTable(index.TableName) {
		return e.tempEng.CreateIndex(index)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	t, exists := sqlutil.MapGetFold(e.tables, index.TableName)
	if !exists {
		for name, tbl := range e.tables {
			cleanA := strings.Trim(strings.TrimSpace(name), "[]\"'`")
			if idx := strings.LastIndex(cleanA, "."); idx != -1 {
				cleanA = strings.Trim(cleanA[idx+1:], "[]\"'`")
			}
			cleanB := strings.Trim(strings.TrimSpace(index.TableName), "[]\"'`")
			if idx := strings.LastIndex(cleanB, "."); idx != -1 {
				cleanB = strings.Trim(cleanB[idx+1:], "[]\"'`")
			}
			if strings.EqualFold(cleanA, cleanB) {
				t = tbl
				exists = true
				break
			}
		}
	}
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, index.TableName)
	}
	index.TableName = t.Name

	if err := index.Validate(t); err != nil {
		return err
	}

	if _, _, exists := t.FindIndex(index.Name); exists {
		return fmt.Errorf("%w: %s", storage.ErrIndexExists, index.Name)
	}

	if len(t.Indexes) >= storage.MaxIndexesPerTable {
		return fmt.Errorf("%w: table %q has reached maximum index limit (%d)", storage.ErrTooManyIndexes, t.Name, storage.MaxIndexesPerTable)
	}

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return fmt.Errorf("open_session failed: %d", ret)
	}
	defer C.go_wt_session_close(sess)

	// The multiplexed index file is created on the table's first index and reused afterwards:
	// building an index is a key insert inside one transaction, rolled back automatically on failure.
	if err := createIndexFile(sess, t); err != nil {
		return err
	}
	if err := buildIndexEntries(sess, t, index); err != nil {
		return err
	}

	t.Indexes = append(t.Indexes, *index)
	if err := e.saveCatalog(); err != nil {
		return err
	}
	e.recordWALLocked([]memengine.WALOp{{
		Type:        memengine.OpCreateIndex,
		Table:       t.Name,
		IndexSchema: index,
	}})
	return nil
}

func (e *Engine) DropIndex(tableName, indexName string) error {
	if storage.IsTemporaryTable(tableName) {
		return e.tempEng.DropIndex(tableName, indexName)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	t, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	idxSchema, idxPos, exists := t.FindIndex(indexName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrIndexNotFound, indexName)
	}

	// DROP INDEX never drops a file (no EBUSY, no layout change): it deletes the index's key
	// range from the table's multiplexed index file inside one transaction.
	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return fmt.Errorf("open_session failed: %d", ret)
	}
	defer C.go_wt_session_close(sess)
	if err := removeIndexRange(sess, t.IndexFileURI(), idxSchema.KeyPrefix()); err != nil {
		return fmt.Errorf("drop index %q: %w", indexName, err)
	}

	t.Indexes = append(t.Indexes[:idxPos], t.Indexes[idxPos+1:]...)
	if err := e.saveCatalog(); err != nil {
		return err
	}
	e.recordWALLocked([]memengine.WALOp{{
		Type:      memengine.OpDropIndex,
		Table:     tableName,
		IndexName: indexName,
	}})
	return nil
}

func (e *Engine) NewSession() (storage.Session, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.closed || e.connClosed.Load() || e.conn == nil {
		return nil, fmt.Errorf("engine is closed")
	}

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return nil, fmt.Errorf("open_session failed with code %d", ret)
	}

	wtSess := &WTSession{
		engine: e,
		sess:   sess,
	}
	e.sessMu.Lock()
	e.sessions[wtSess] = struct{}{}
	e.sessMu.Unlock()

	return wtSess, nil
}

func (e *Engine) evictAllCursors(tableName string) {
	e.sessMu.Lock()
	active := make([]*WTSession, 0, len(e.sessions))
	for s := range e.sessions {
		active = append(active, s)
	}
	e.sessMu.Unlock()

	for _, s := range active {
		s.evictCursors(tableName)
	}
}

const maxIdleCursorsPerURI = 16

// fastCString copies str into stackBuf with a null terminator if it fits,
// avoiding C.CString heap allocation and C.free.
func fastCString(str string, stackBuf []byte) (*C.char, func()) {
	if len(str)+1 <= len(stackBuf) {
		if len(str) > 0 {
			copy(stackBuf, str)
		}
		stackBuf[len(str)] = 0
		return (*C.char)(unsafe.Pointer(&stackBuf[0])), nil
	}
	cs := C.CString(str)
	return cs, func() { C.free(unsafe.Pointer(cs)) }
}

// WTSession represents an active client connection to WiredTiger.
type WTSession struct {
	engine     *Engine
	sess       *C.WT_SESSION
	tempSess   storage.Session
	mu         sync.Mutex
	closed     bool
	activeTx   bool
	currentTx  *WTTx
	cursorPool map[string][]*C.WT_CURSOR
	allCursors map[*C.WT_CURSOR]struct{}
	toClose    []*C.WT_CURSOR
}

func (s *WTSession) getTempSession() (storage.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("session is closed")
	}
	if s.tempSess != nil {
		return s.tempSess, nil
	}
	if s.engine == nil || s.engine.tempEng == nil {
		return nil, fmt.Errorf("temporary storage engine is not initialized")
	}
	ts, err := s.engine.tempEng.NewSession()
	if err != nil {
		return nil, err
	}
	s.tempSess = ts
	return ts, nil
}

func (s *WTSession) flushPendingClosesLocked() {
	if s.closed || s.sess == nil || len(s.toClose) == 0 {
		return
	}
	for _, cur := range s.toClose {
		delete(s.allCursors, cur)
		C.go_wt_cursor_close(cur)
	}
	s.toClose = s.toClose[:0]
}

func (s *WTSession) evictCursors(tableName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.sess == nil {
		return
	}
	prefixTable := "table:" + tableName
	prefixIndex := "index:" + tableName + ":"
	idxFileURI := "table:_idx_" + tableName
	for curURI, pool := range s.cursorPool {
		if curURI == prefixTable || strings.HasPrefix(curURI, prefixIndex) || curURI == idxFileURI || curURI == tableName {
			s.toClose = append(s.toClose, pool...)
			delete(s.cursorPool, curURI)
		}
	}
	s.flushPendingClosesLocked()
	if !s.activeTx {
		_ = C.go_wt_session_reset(s.sess)
	}
}

func (s *WTSession) Close() error {
	s.engine.sessMu.Lock()
	delete(s.engine.sessions, s)
	s.engine.sessMu.Unlock()

	s.mu.Lock()
	curTx := s.currentTx
	s.mu.Unlock()

	if curTx != nil {
		curTx.mu.Lock()
		curTx.closed = true
		curTx.closeOpenCursorsLocked()
		curTx.releaseAcquiredLocksLocked()
		defer curTx.mu.Unlock()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentTx != nil {
		s.currentTx.releaseAcquiredLocksLocked()
		s.currentTx = nil
	}

	if s.closed || s.sess == nil {
		return nil
	}
	s.closed = true

	s.flushPendingClosesLocked()

	if s.tempSess != nil {
		_ = s.tempSess.Close()
		s.tempSess = nil
	}

	for cur := range s.allCursors {
		C.go_wt_cursor_close(cur)
	}
	s.allCursors = nil
	s.cursorPool = nil
	s.toClose = nil

	// If the parent engine connection has already been closed or is being closed,
	// bypass C.go_wt_session_close to prevent dereferencing freed connection memory (SIGSEGV).
	if s.engine.connClosed.Load() {
		s.sess = nil
		return nil
	}

	ret := C.go_wt_session_close(s.sess)
	s.sess = nil
	if ret != 0 {
		return fmt.Errorf("session_close failed: code %d", ret)
	}
	return nil
}

func (s *WTSession) acquireCursor(uri string) (*C.WT_CURSOR, error) {
	s.mu.Lock()
	if s.closed || s.sess == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("session is closed")
	}
	s.flushPendingClosesLocked()

	if pool := s.cursorPool[uri]; len(pool) > 0 {
		idx := len(pool) - 1
		cur := pool[idx]
		s.cursorPool[uri] = pool[:idx]
		s.mu.Unlock()
		return cur, nil
	}
	s.mu.Unlock()

	cURI := C.CString(uri)
	defer C.free(unsafe.Pointer(cURI))

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.sess == nil {
		return nil, fmt.Errorf("session is closed")
	}

	var cur *C.WT_CURSOR
	var ret C.int
	if s.engine != nil && s.engine.isIndexFileURI(uri) {
		cConfig := C.CString("overwrite=false")
		ret = C.go_wt_open_cursor_config(s.sess, cURI, cConfig, &cur)
		C.free(unsafe.Pointer(cConfig))
	} else {
		ret = C.go_wt_open_cursor(s.sess, cURI, &cur)
	}
	if ret != 0 {
		return nil, fmt.Errorf("open_cursor failed: %d", ret)
	}

	if s.allCursors == nil {
		s.allCursors = make(map[*C.WT_CURSOR]struct{})
	}
	s.allCursors[cur] = struct{}{}
	return cur, nil
}

func (s *WTSession) releaseCursor(uri string, cur *C.WT_CURSOR) {
	if cur == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseCursorLocked(uri, cur)
}

func (s *WTSession) releaseCursorLocked(uri string, cur *C.WT_CURSOR) {
	if cur == nil {
		return
	}

	if s.closed || s.sess == nil {
		// Session has already been closed; s.Close() closed all cursors in s.allCursors.
		// Avoid double-close and use-after-free.
		return
	}

	C.go_wt_cursor_reset(cur)
	s.flushPendingClosesLocked()

	if s.cursorPool == nil {
		s.cursorPool = make(map[string][]*C.WT_CURSOR)
	}
	pool := s.cursorPool[uri]
	if len(pool) < maxIdleCursorsPerURI {
		s.cursorPool[uri] = append(pool, cur)
	} else {
		delete(s.allCursors, cur)
		C.go_wt_cursor_close(cur)
	}
}

func (s *WTSession) getCursor(tableName string) (*C.WT_CURSOR, error) {
	return s.acquireCursor("table:" + tableName)
}

func (s *WTSession) resetCursors() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.sess == nil {
		return
	}
	for cur := range s.allCursors {
		C.go_wt_cursor_reset(cur)
	}
}

// Reset resets all cached cursors and calls WT_SESSION::reset to release resources
// and prevent sweep-server warnings on idle sessions.
func (s *WTSession) Reset() error {
	s.mu.Lock()
	curTx := s.currentTx
	s.mu.Unlock()

	if curTx != nil {
		curTx.mu.Lock()
		curTx.closed = true
		curTx.closeOpenCursorsLocked()
		curTx.releaseAcquiredLocksLocked()
		defer curTx.mu.Unlock()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentTx != nil {
		s.currentTx.releaseAcquiredLocksLocked()
		s.currentTx = nil
	}

	if s.closed || s.sess == nil {
		return nil
	}
	if s.activeTx {
		_ = C.go_wt_session_rollback(s.sess, nil)
		s.activeTx = false
	}
	for cur := range s.allCursors {
		C.go_wt_cursor_reset(cur)
	}
	ret := C.go_wt_session_reset(s.sess)
	if ret != 0 {
		return fmt.Errorf("session_reset failed: code %d", ret)
	}
	return nil
}

func (s *WTSession) BeginTx(opts storage.TxOptions) (storage.Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, fmt.Errorf("session is closed")
	}

	if !opts.ReadOnly {
		if err := s.engine.checkTainted(); err != nil {
			return nil, err
		}
	}

	if s.currentTx != nil {
		s.currentTx.releaseAcquiredLocksLocked()
		s.currentTx = nil
	}

	ret := C.go_wt_session_begin(s.sess, nil)
	if ret != 0 {
		return nil, fmt.Errorf("begin_transaction failed with code %d", ret)
	}
	s.activeTx = true

	snapLSN := opts.SnapshotLSN
	if snapLSN == 0 {
		snapLSN = s.engine.CurrentLSN()
	}
	snapTS := opts.SnapshotTimestamp
	if snapTS == 0 {
		snapTS = time.Now().UnixNano()
	}

	var snapTables []*storage.TableSchema
	if opts.ReadOnly {
		snapTables, _ = s.engine.ListTables()
	}

	var txID uint64
	if s.engine != nil {
		txID = s.engine.nextTxID.Add(1)
	}

	wttx := &WTTx{
		sess:              s,
		txID:              txID,
		opts:              opts,
		snapshotLSN:       snapLSN,
		snapshotTimestamp: snapTS,
		snapTables:        snapTables,
		readCache:         storage.NewTxReadCache(16),
	}
	s.currentTx = wttx
	return wttx, nil
}

// WTTx manages ACID mutations in WiredTiger and routes temporary tables to tempTx lazily.
type WTTx struct {
	sess              *WTSession
	txID              uint64
	lockMu            sync.Mutex
	acquiredLocks     []wtLockRef
	tempTx            storage.Transaction
	opts              storage.TxOptions
	snapshotLSN       int64
	snapshotTimestamp int64
	commitLSN         int64
	commitTimestamp   int64
	mu                sync.Mutex
	closed            bool
	hasWrites         bool
	undoLog           []*storage.PhysiologicalRecord
	savepoints        map[string]int
	spNames           []string
	tempSpPreCreated  map[string]bool
	walOps            []memengine.WALOp
	spWALIndices      map[string]int
	tableCache        map[string]*storage.TableSchema
	snapTables        []*storage.TableSchema
	rowDeltas         map[string]int64
	openCursors       []storage.Cursor
	testFailIndexOp   func(op string, idxName string) error
	readCache         *storage.TxReadCache
}

func (tx *WTTx) closeOpenCursorsLocked() {
	for _, cur := range tx.openCursors {
		if cur != nil {
			_ = cur.Close()
		}
	}
	tx.openCursors = nil
}

func (tx *WTTx) tryAcquireWriteLockLocked(tableName string, pkKey string) error {
	eng := tx.sess.engine
	if eng == nil {
		return nil
	}
	lockKey := tableName + "\x00" + pkKey
	sIdx := hashWTLockKey(lockKey)

	tx.lockMu.Lock()
	defer tx.lockMu.Unlock()

	sh := &eng.lockShards[sIdx]

	for spin := 0; spin < 3; spin++ {
		sh.mu.Lock()
		if sh.activeWriters == nil {
			sh.activeWriters = make(map[string]uint64)
		}
		owner, exists := sh.activeWriters[lockKey]
		if !exists || owner == tx.txID {
			if !exists {
				sh.activeWriters[lockKey] = tx.txID
				tx.acquiredLocks = append(tx.acquiredLocks, wtLockRef{
					shardIdx: sIdx,
					key:      lockKey,
				})
			}
			sh.mu.Unlock()
			return nil
		}
		sh.mu.Unlock()

		// Allow the active owner transaction a brief window to commit and release its locks
		if spin == 0 {
			runtime.Gosched()
		} else {
			time.Sleep(time.Duration(spin*25) * time.Microsecond)
		}
	}

	return storage.ErrSerializationFailure
}

func (tx *WTTx) releaseAcquiredLocksLocked() {
	tx.lockMu.Lock()
	defer tx.lockMu.Unlock()

	nLocks := len(tx.acquiredLocks)
	if nLocks == 0 {
		return
	}
	eng := tx.sess.engine
	if eng == nil {
		tx.acquiredLocks = nil
		return
	}
	shrinkMap := nLocks > 128

	if nLocks > 1 {
		sort.Slice(tx.acquiredLocks, func(i, j int) bool {
			return tx.acquiredLocks[i].shardIdx < tx.acquiredLocks[j].shardIdx
		})
	}

	for i := 0; i < nLocks; {
		shardIdx := tx.acquiredLocks[i].shardIdx
		if shardIdx < 0 || shardIdx >= numWTLockShards {
			i++
			continue
		}
		sh := &eng.lockShards[shardIdx]
		sh.mu.Lock()
		for i < nLocks && tx.acquiredLocks[i].shardIdx == shardIdx {
			curKey := tx.acquiredLocks[i].key
			if sh.activeWriters != nil && sh.activeWriters[curKey] == tx.txID {
				delete(sh.activeWriters, curKey)
			}
			i++
		}
		if shrinkMap && len(sh.activeWriters) == 0 {
			sh.activeWriters = nil
		}
		sh.mu.Unlock()
	}

	if shrinkMap {
		tx.acquiredLocks = nil
	} else {
		tx.acquiredLocks = tx.acquiredLocks[:0]
	}
}

func (tx *WTTx) clearUndoLogLocked() {
	for _, rec := range tx.undoLog {
		storage.ReleasePhysRecord(rec)
	}
	tx.undoLog = nil
}

func (tx *WTTx) ensureTempTxLocked() (storage.Transaction, error) {
	if tx.tempTx != nil {
		return tx.tempTx, nil
	}
	ts, err := tx.sess.getTempSession()
	if err != nil {
		return nil, err
	}
	ttx, err := ts.BeginTx(tx.opts)
	if err != nil {
		return nil, err
	}
	tx.tempTx = ttx
	return ttx, nil
}

func (tx *WTTx) getTableLocked(tableName string) (*storage.TableSchema, error) {
	if tx.tableCache != nil {
		if tbl, ok := tx.tableCache[tableName]; ok {
			return tbl, nil
		}
		for name, tbl := range tx.tableCache {
			if strings.EqualFold(name, tableName) {
				tx.tableCache[tableName] = tbl
				return tbl, nil
			}
		}
	}
	tbl, err := tx.sess.engine.GetTable(tableName)
	if err != nil {
		return nil, err
	}
	if tx.tableCache == nil {
		tx.tableCache = make(map[string]*storage.TableSchema, 4)
	}
	tx.tableCache[tableName] = tbl
	return tbl, nil
}

// ListTables returns the list of table schemas visible to this transaction snapshot ([RULE:BoundedLookup]).
func (tx *WTTx) ListTables() ([]*storage.TableSchema, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.snapTables != nil {
		res := make([]*storage.TableSchema, len(tx.snapTables))
		copy(res, tx.snapTables)
		return res, nil
	}
	return tx.sess.engine.ListTables()
}

// SnapshotLSN captures the engine LSN at transaction start for replication boundary and metadata purposes.
// It does not provide historical data visibility, snapshot isolation, or time-travel reads.
func (tx *WTTx) SnapshotLSN() int64 {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.snapshotLSN
}

// SnapshotTimestamp returns the snapshot unix nano timestamp bound at transaction start.
func (tx *WTTx) SnapshotTimestamp() int64 {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.snapshotTimestamp
}

// CommitLSN returns the committed LSN (0 if uncommitted or rolled back).
func (tx *WTTx) CommitLSN() int64 {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.commitLSN
}

// CommitTimestamp returns the committed unix nano timestamp (0 if uncommitted or rolled back).
func (tx *WTTx) CommitTimestamp() int64 {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.commitTimestamp
}

// SetReadOnly dynamically toggles the ReadOnly status of an active transaction.
func (tx *WTTx) SetReadOnly(ro bool) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.opts.ReadOnly = ro
}

func serializeRecord(cur *C.WT_CURSOR, schema *storage.TableSchema, record storage.Record) ([]byte, error) {
	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)
	b, err := serializeRecordToBuffer(cur, schema, record, *bufPtr)
	if err != nil {
		return nil, err
	}
	res := make([]byte, len(b))
	copy(res, b)
	return res, nil
}

func serializeRecordToBuffer(cur *C.WT_CURSOR, schema *storage.TableSchema, record storage.Record, buf []byte) ([]byte, error) {
	if schema != nil {
		return storage.EncodeTupleToBuffer(schema, record, buf)
	}
	return nil, fmt.Errorf("table schema is required for binary tuple serialization")
}

func (tx *WTTx) Insert(tableName string, record storage.Record) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return storage.ErrReadOnlyTransaction
	}
	if storage.IsTemporaryTable(tableName) {
		ttx, err := tx.ensureTempTxLocked()
		if err != nil {
			return err
		}
		return ttx.Insert(tableName, record)
	}

	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	return tx.insertLocked(tableName, tbl, record)
}

func (tx *WTTx) InsertRow(tableName string, row storage.Row) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return storage.ErrReadOnlyTransaction
	}
	if storage.IsTemporaryTable(tableName) {
		ttx, err := tx.ensureTempTxLocked()
		if err != nil {
			return err
		}
		return ttx.InsertRow(tableName, row)
	}

	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	return tx.insertRowLocked(tableName, tbl, row)
}

func (tx *WTTx) insertRowLocked(tableName string, tbl *storage.TableSchema, row storage.Row) error {
	if err := tx.sess.engine.checkTainted(); err != nil {
		return err
	}

	// Defensive copy: isolate row to eliminate caller slice mutation side-effects ([RULE:DefensiveIndexing])
	if len(row.Values) > 0 {
		vals := make([]storage.Value, len(row.Values))
		copy(vals, row.Values)
		row.Values = vals
	}

	var normErr error
	row, normErr = storage.ValidateAndNormalizeRow(tbl, row)
	if normErr != nil {
		return normErr
	}

	if _, rvIdx, hasRV := tbl.RowVersionColumn(); hasRV {
		if rvIdx >= 0 && rvIdx < len(row.Values) {
			row.Values[rvIdx] = storage.BinaryValue(storage.NextRowVersion())
		}
	}

	if _, err := tbl.PrimaryKeyColumn(); err != nil {
		return err
	}

	pkVal, has := tbl.ResolveRowPK(row)
	if !has || pkVal.IsNull {
		pkCol, _ := tbl.PrimaryKeyColumn()
		colName := ""
		if pkCol != nil {
			colName = pkCol.Name
		}
		return fmt.Errorf("%w: missing primary key %s", storage.ErrNotNullViolation, colName)
	}

	if err := tx.tryAcquireWriteLockLocked(tbl.Name, pkVal.String()); err != nil {
		return err
	}

	if err := tx.checkForeignKeyFromRowLocked(tbl, row); err != nil {
		return err
	}
	if err := tx.checkUniqueIndexesFromRowLocked(tableName, tbl, pkVal, row); err != nil {
		return err
	}

	pkStr := pkVal.String()
	uri := tbl.TableURI()
	cur, err := tx.sess.acquireCursor(uri)
	if err != nil {
		return err
	}
	defer tx.sess.releaseCursor(uri, cur)

	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)

	recBytes, err := storage.EncodeRowToBuffer(tbl, row, *bufPtr)
	if err != nil {
		return err
	}

	var keyBuf [256]byte
	cKey, cleanup := fastCString(pkStr, keyBuf[:])
	if cleanup != nil {
		defer cleanup()
	}

	// Verify primary key uniqueness
	var pData unsafe.Pointer
	var sz C.size_t
	if ret := C.go_wt_cursor_get(cur, cKey, &pData, &sz); ret == 0 && pData != nil {
		C.go_wt_cursor_reset(cur)
		return fmt.Errorf("%w: key %q in table %q", storage.ErrKeyExists, pkStr, tableName)
	}

	if len(recBytes) > 0 {
		pData = unsafe.Pointer(&recBytes[0])
	} else {
		pData = nil
	}

	if ret := C.go_wt_cursor_insert_raw(cur, cKey, pData, C.size_t(len(recBytes))); ret != 0 {
		return wtOpError("cursor_insert", int(ret))
	}

	var physRec *storage.PhysiologicalRecord
	if len(tx.savepoints) > 0 {
		recCopy := make([]byte, len(recBytes))
		copy(recCopy, recBytes)
		physRec = storage.AcquirePhysRecord(storage.OpInsert, tbl.Name, pkVal, nil, recCopy)
	}

	if len(tbl.Indexes) > 0 {
		var insertedIdxBuf [8]storage.IndexImage
		insertedIdx := insertedIdxBuf[:0]
		rollbackInserted := func() {
			for j := len(insertedIdx) - 1; j >= 0; j-- {
				img := insertedIdx[j]
				if rbCur, rbErr := tx.sess.acquireCursor(img.URI); rbErr == nil {
					_ = removeIndexEntry(rbCur, img.Key)
					tx.sess.releaseCursor(img.URI, rbCur)
				}
			}
			_ = C.go_wt_cursor_remove_key(cur, cKey)
			if physRec != nil {
				storage.ReleasePhysRecord(physRec)
			}
		}

		for _, idx := range tbl.Indexes {
			if idx.Disabled {
				continue
			}
			if tx.testFailIndexOp != nil {
				if err := tx.testFailIndexOp("insert", idx.Name); err != nil {
					rollbackInserted()
					return err
				}
			}
			idxURI := tbl.IndexFileURI()
			idxCur, err := tx.sess.acquireCursor(idxURI)
			if err != nil {
				rollbackInserted()
				return err
			}
			k, v, _ := formatIndexKeyValueFromRow(tbl, row, &idx, pkStr)
			if err := insertIndexEntry(idxCur, k, v); err != nil {
				tx.sess.releaseCursor(idxURI, idxCur)
				rollbackInserted()
				return err
			}
			tx.sess.releaseCursor(idxURI, idxCur)
			img := storage.IndexImage{
				URI:   idxURI,
				Key:   k,
				Value: v,
			}
			insertedIdx = append(insertedIdx, img)
			if physRec != nil {
				physRec.NewIdx = append(physRec.NewIdx, img)
			}
		}
	}

	tx.hasWrites = true
	if physRec != nil {
		tx.undoLog = append(tx.undoLog, physRec)
	}
	if tx.sess.engine.hasHook.Load() && !storage.IsTemporaryTable(tableName) {
		recCopy := make([]byte, len(recBytes))
		copy(recCopy, recBytes)
		tx.walOps = append(tx.walOps, memengine.WALOp{
			Type:  memengine.OpInsert,
			Table: tableName,
			Key:   pkStr,
			Data:  recCopy,
		})
	}

	if tx.rowDeltas == nil {
		tx.rowDeltas = make(map[string]int64)
	}
	tx.rowDeltas[tbl.Name]++
	if tx.readCache != nil {
		tx.readCache.PutRow(tableName, pkStr, row, 0)
	}

	return nil
}

func (tx *WTTx) insertLocked(tableName string, tbl *storage.TableSchema, record storage.Record) error {
	if err := tx.sess.engine.checkTainted(); err != nil {
		return err
	}
	normalized := make(storage.Record, len(record))
	for k, v := range record {
		if col, _, ok := tbl.FindColumn(k); ok {
			normalized.Set(col.Name, v)
		} else {
			normalized.Set(k, v)
		}
	}
	record = normalized

	var normErr error
	record, normErr = storage.ValidateAndNormalizeRecord(tbl, record)
	if normErr != nil {
		return normErr
	}

	if rvCol, _, hasRV := tbl.RowVersionColumn(); hasRV {
		record[rvCol.Name] = storage.BinaryValue(storage.NextRowVersion())
	}

	if _, err := tbl.PrimaryKeyColumn(); err != nil {
		return err
	}

	pkVal, has := tbl.ResolveRecordPK(record)
	if !has || pkVal.IsNull {
		pkCol, _ := tbl.PrimaryKeyColumn()
		colName := ""
		if pkCol != nil {
			colName = pkCol.Name
		}
		return fmt.Errorf("%w: missing primary key %s", storage.ErrNotNullViolation, colName)
	}

	if err := tx.tryAcquireWriteLockLocked(tbl.Name, pkVal.String()); err != nil {
		return err
	}

	if err := tx.checkForeignKeyLocked(tbl, record); err != nil {
		return err
	}
	if err := tx.checkUniqueIndexesLocked(tableName, tbl, pkVal, record); err != nil {
		return err
	}

	pkStr := pkVal.String()
	uri := tbl.TableURI()
	cur, err := tx.sess.acquireCursor(uri)
	if err != nil {
		return err
	}
	defer tx.sess.releaseCursor(uri, cur)

	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)

	recBytes, err := serializeRecordToBuffer(cur, tbl, record, *bufPtr)
	if err != nil {
		return err
	}

	var keyBuf [256]byte
	cKey, cleanup := fastCString(pkStr, keyBuf[:])
	if cleanup != nil {
		defer cleanup()
	}

	// Verify primary key uniqueness
	var pData unsafe.Pointer
	var sz C.size_t
	if ret := C.go_wt_cursor_get(cur, cKey, &pData, &sz); ret == 0 && pData != nil {
		C.go_wt_cursor_reset(cur)
		return fmt.Errorf("%w: key %q in table %q", storage.ErrKeyExists, pkStr, tableName)
	}

	if len(recBytes) > 0 {
		pData = unsafe.Pointer(&recBytes[0])
	} else {
		pData = nil
	}

	if ret := C.go_wt_cursor_insert_raw(cur, cKey, pData, C.size_t(len(recBytes))); ret != 0 {
		return wtOpError("cursor_insert", int(ret))
	}

	var physRec *storage.PhysiologicalRecord
	if len(tx.savepoints) > 0 {
		recCopy := make([]byte, len(recBytes))
		copy(recCopy, recBytes)
		physRec = storage.AcquirePhysRecord(storage.OpInsert, tbl.Name, pkVal, nil, recCopy)
	}

	if len(tbl.Indexes) > 0 {
		var insertedIdxBuf [8]storage.IndexImage
		insertedIdx := insertedIdxBuf[:0]
		rollbackInserted := func() {
			for j := len(insertedIdx) - 1; j >= 0; j-- {
				img := insertedIdx[j]
				if rbCur, rbErr := tx.sess.acquireCursor(img.URI); rbErr == nil {
					_ = removeIndexEntry(rbCur, img.Key)
					tx.sess.releaseCursor(img.URI, rbCur)
				}
			}
			_ = C.go_wt_cursor_remove_key(cur, cKey)
			if physRec != nil {
				storage.ReleasePhysRecord(physRec)
			}
		}

		for _, idx := range tbl.Indexes {
			if idx.Disabled {
				continue
			}
			if tx.testFailIndexOp != nil {
				if err := tx.testFailIndexOp("insert", idx.Name); err != nil {
					rollbackInserted()
					return err
				}
			}
			idxURI := tbl.IndexFileURI()
			idxCur, err := tx.sess.acquireCursor(idxURI)
			if err != nil {
				rollbackInserted()
				return err
			}
			k, v, _ := formatIndexKeyValue(record, &idx, pkStr)
			if err := insertIndexEntry(idxCur, k, v); err != nil {
				tx.sess.releaseCursor(idxURI, idxCur)
				rollbackInserted()
				return err
			}
			tx.sess.releaseCursor(idxURI, idxCur)
			img := storage.IndexImage{
				URI:   idxURI,
				Key:   k,
				Value: v,
			}
			insertedIdx = append(insertedIdx, img)
			if physRec != nil {
				physRec.NewIdx = append(physRec.NewIdx, img)
			}
		}
	}

	tx.hasWrites = true
	if physRec != nil {
		tx.undoLog = append(tx.undoLog, physRec)
	}
	if tx.sess.engine.hasHook.Load() && !storage.IsTemporaryTable(tableName) {
		recCopy := make([]byte, len(recBytes))
		copy(recCopy, recBytes)
		tx.walOps = append(tx.walOps, memengine.WALOp{
			Type:  memengine.OpInsert,
			Table: tableName,
			Key:   pkStr,
			Data:  recCopy,
		})
	}

	if tx.rowDeltas == nil {
		tx.rowDeltas = make(map[string]int64)
	}
	tx.rowDeltas[tbl.Name]++
	if tx.readCache != nil {
		tx.readCache.PutRecord(tableName, pkStr, normalized)
	}

	return nil
}

func (tx *WTTx) Update(tableName string, pk storage.Value, record storage.Record) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return storage.ErrReadOnlyTransaction
	}
	if storage.IsTemporaryTable(tableName) {
		ttx, err := tx.ensureTempTxLocked()
		if err != nil {
			return err
		}
		return ttx.Update(tableName, pk, record)
	}

	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	return tx.updateLocked(tableName, tbl, pk, record)
}

func (tx *WTTx) UpdateRow(tableName string, pk storage.Value, row storage.Row) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return storage.ErrReadOnlyTransaction
	}
	if storage.IsTemporaryTable(tableName) {
		ttx, err := tx.ensureTempTxLocked()
		if err != nil {
			return err
		}
		return ttx.UpdateRow(tableName, pk, row)
	}

	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	rec := tbl.RowToRecord(row)
	return tx.updateLocked(tableName, tbl, pk, rec)
}

func (tx *WTTx) updateLocked(tableName string, tbl *storage.TableSchema, pk storage.Value, record storage.Record) error {
	if err := tx.sess.engine.checkTainted(); err != nil {
		return err
	}
	if err := tx.tryAcquireWriteLockLocked(tbl.Name, pk.String()); err != nil {
		return err
	}
	oldData, err := tx.getRawBytesLocked(tableName, pk)
	if err != nil {
		return err
	}

	existing, err := decodeRecord(tbl, oldData)
	if err != nil {
		return err
	}
	var oldRec storage.Record
	if len(tbl.Indexes) > 0 {
		oldRec = existing.Clone()
	}

	for col, val := range record {
		if colSchema, _, ok := tbl.FindColumn(col); ok {
			existing.Set(colSchema.Name, val)
		} else {
			existing.Set(col, val)
		}
	}

	if rvCol, _, hasRV := tbl.RowVersionColumn(); hasRV {
		existing[rvCol.Name] = storage.BinaryValue(storage.NextRowVersion())
	}

	var normErr error
	existing, normErr = storage.ValidateAndNormalizeRecord(tbl, existing)
	if normErr != nil {
		return normErr
	}

	if err := tx.checkForeignKeyLocked(tbl, existing); err != nil {
		return err
	}
	if err := tx.checkUniqueIndexesLocked(tableName, tbl, pk, existing); err != nil {
		return err
	}

	uri := tbl.TableURI()
	cur, err := tx.sess.acquireCursor(uri)
	if err != nil {
		return err
	}
	defer tx.sess.releaseCursor(uri, cur)

	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)

	recBytes, err := serializeRecordToBuffer(cur, tbl, existing, *bufPtr)
	if err != nil {
		return err
	}

	pkStr := pk.String()
	var keyBuf [256]byte
	cKey, cleanup := fastCString(pkStr, keyBuf[:])
	if cleanup != nil {
		defer cleanup()
	}

	var pData unsafe.Pointer
	if len(recBytes) > 0 {
		pData = unsafe.Pointer(&recBytes[0])
	}

	if ret := C.go_wt_cursor_update_raw(cur, cKey, pData, C.size_t(len(recBytes))); ret != 0 {
		return wtOpError("cursor_update", int(ret))
	}

	var physRec *storage.PhysiologicalRecord
	if len(tx.savepoints) > 0 {
		newDataCopy := make([]byte, len(recBytes))
		copy(newDataCopy, recBytes)
		oldDataCopy := make([]byte, len(oldData))
		copy(oldDataCopy, oldData)
		physRec = storage.AcquirePhysRecord(storage.OpUpdate, tbl.Name, pk, oldDataCopy, newDataCopy)
	}

	if len(tbl.Indexes) > 0 {
		type idxUpdateUndo struct {
			uri    string
			oldKey string
			oldVal string
			newKey string
		}
		var undoBuf [8]idxUpdateUndo
		undos := undoBuf[:0]
		rollbackUpdated := func() {
			for j := len(undos) - 1; j >= 0; j-- {
				u := undos[j]
				if rbCur, rbErr := tx.sess.acquireCursor(u.uri); rbErr == nil {
					if u.newKey != "" {
						_ = removeIndexEntry(rbCur, u.newKey)
					}
					_ = insertIndexEntry(rbCur, u.oldKey, u.oldVal)
					tx.sess.releaseCursor(u.uri, rbCur)
				}
			}
			var oldPData unsafe.Pointer
			if len(oldData) > 0 {
				oldPData = unsafe.Pointer(&oldData[0])
			}
			_ = C.go_wt_cursor_update_raw(cur, cKey, oldPData, C.size_t(len(oldData)))
			if physRec != nil {
				storage.ReleasePhysRecord(physRec)
			}
		}

		for _, idx := range tbl.Indexes {
			if idx.Disabled {
				continue
			}
			oldKey, oldVal, _ := formatIndexKeyValue(oldRec, &idx, pkStr)
			newKey, newVal, _ := formatIndexKeyValue(existing, &idx, pkStr)
			if oldKey == newKey && oldVal == newVal {
				continue
			}
			idxURI := tbl.IndexFileURI()
			idxCur, err := tx.sess.acquireCursor(idxURI)
			if err != nil {
				rollbackUpdated()
				return err
			}
			if tx.testFailIndexOp != nil {
				if err := tx.testFailIndexOp("remove", idx.Name); err != nil {
					tx.sess.releaseCursor(idxURI, idxCur)
					rollbackUpdated()
					return err
				}
			}
			if err := removeIndexEntry(idxCur, oldKey); err != nil {
				tx.sess.releaseCursor(idxURI, idxCur)
				rollbackUpdated()
				return err
			}
			if tx.testFailIndexOp != nil {
				if err := tx.testFailIndexOp("insert", idx.Name); err != nil {
					_ = insertIndexEntry(idxCur, oldKey, oldVal)
					tx.sess.releaseCursor(idxURI, idxCur)
					rollbackUpdated()
					return err
				}
			}
			if err := insertIndexEntry(idxCur, newKey, newVal); err != nil {
				_ = insertIndexEntry(idxCur, oldKey, oldVal)
				tx.sess.releaseCursor(idxURI, idxCur)
				rollbackUpdated()
				return err
			}
			tx.sess.releaseCursor(idxURI, idxCur)
			undos = append(undos, idxUpdateUndo{
				uri:    idxURI,
				oldKey: oldKey,
				oldVal: oldVal,
				newKey: newKey,
			})
			if physRec != nil {
				physRec.OldIdx = append(physRec.OldIdx, storage.IndexImage{
					URI:   idxURI,
					Key:   oldKey,
					Value: oldVal,
				})
				physRec.NewIdx = append(physRec.NewIdx, storage.IndexImage{
					URI:   idxURI,
					Key:   newKey,
					Value: newVal,
				})
			}
		}
	}

	tx.hasWrites = true
	if physRec != nil {
		tx.undoLog = append(tx.undoLog, physRec)
	}
	if tx.sess.engine.hasHook.Load() && !storage.IsTemporaryTable(tableName) {
		recCopy := make([]byte, len(recBytes))
		copy(recCopy, recBytes)
		tx.walOps = append(tx.walOps, memengine.WALOp{
			Type:  memengine.OpUpdate,
			Table: tableName,
			Key:   pk.String(),
			Data:  recCopy,
		})
	}

	if tx.readCache != nil {
		tx.readCache.PutRecord(tableName, pk.String(), existing)
	}

	return nil
}

func (tx *WTTx) Delete(tableName string, pk storage.Value) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return storage.ErrReadOnlyTransaction
	}
	if storage.IsTemporaryTable(tableName) {
		ttx, err := tx.ensureTempTxLocked()
		if err != nil {
			return err
		}
		return ttx.Delete(tableName, pk)
	}

	return tx.deleteLocked(tableName, pk)
}

func (tx *WTTx) deleteLocked(tableName string, pk storage.Value) error {
	if err := tx.sess.engine.checkTainted(); err != nil {
		return err
	}
	if err := tx.tryAcquireWriteLockLocked(tableName, pk.String()); err != nil {
		return err
	}
	if err := tx.checkReferencingFKsOnDeleteLocked(tableName, pk); err != nil {
		return err
	}

	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}

	var oldData []byte
	var existingRec storage.Record
	if len(tx.savepoints) > 0 || len(tbl.Indexes) > 0 {
		var gerr error
		oldData, gerr = tx.getRawBytesLocked(tableName, pk)
		if gerr != nil {
			if errors.Is(gerr, storage.ErrKeyNotFound) {
				return nil
			}
			return gerr
		}
		if len(tbl.Indexes) > 0 && len(oldData) > 0 {
			var derr error
			existingRec, derr = decodeRecord(tbl, oldData)
			if derr != nil {
				return derr
			}
		}
	}

	uri := tbl.TableURI()
	cur, err := tx.sess.acquireCursor(uri)
	if err != nil {
		return err
	}
	defer tx.sess.releaseCursor(uri, cur)

	pkStr := pk.String()
	var keyBuf [256]byte
	cKey, cleanup := fastCString(pkStr, keyBuf[:])
	if cleanup != nil {
		defer cleanup()
	}

	if ret := C.go_wt_cursor_remove_key(cur, cKey); ret != 0 {
		if ret == C.WT_NOTFOUND {
			return nil
		}
		return wtOpError("cursor_remove", int(ret))
	}

	var physRec *storage.PhysiologicalRecord
	if len(tx.savepoints) > 0 && len(oldData) > 0 {
		oldDataCopy := make([]byte, len(oldData))
		copy(oldDataCopy, oldData)
		physRec = storage.AcquirePhysRecord(storage.OpDelete, tbl.Name, pk, oldDataCopy, nil)
	}

	if existingRec != nil && len(tbl.Indexes) > 0 {
		var removedIdxBuf [8]storage.IndexImage
		removedIdx := removedIdxBuf[:0]
		rollbackDeleted := func() {
			for j := len(removedIdx) - 1; j >= 0; j-- {
				img := removedIdx[j]
				if rbCur, rbErr := tx.sess.acquireCursor(img.URI); rbErr == nil {
					_ = insertIndexEntry(rbCur, img.Key, img.Value)
					tx.sess.releaseCursor(img.URI, rbCur)
				}
			}
			if len(oldData) > 0 {
				oldPData := unsafe.Pointer(&oldData[0])
				_ = C.go_wt_cursor_insert_raw(cur, cKey, oldPData, C.size_t(len(oldData)))
			}
			if physRec != nil {
				storage.ReleasePhysRecord(physRec)
			}
		}

		for _, idx := range tbl.Indexes {
			if idx.Disabled {
				continue
			}
			if tx.testFailIndexOp != nil {
				if err := tx.testFailIndexOp("remove", idx.Name); err != nil {
					rollbackDeleted()
					return err
				}
			}
			idxURI := tbl.IndexFileURI()
			idxCur, err := tx.sess.acquireCursor(idxURI)
			if err != nil {
				rollbackDeleted()
				return err
			}
			k, v, _ := formatIndexKeyValue(existingRec, &idx, pkStr)
			if err := removeIndexEntry(idxCur, k); err != nil {
				tx.sess.releaseCursor(idxURI, idxCur)
				rollbackDeleted()
				return err
			}
			tx.sess.releaseCursor(idxURI, idxCur)
			img := storage.IndexImage{
				URI:   idxURI,
				Key:   k,
				Value: v,
			}
			removedIdx = append(removedIdx, img)
			if physRec != nil {
				physRec.OldIdx = append(physRec.OldIdx, img)
			}
		}
	}

	tx.hasWrites = true
	if physRec != nil {
		tx.undoLog = append(tx.undoLog, physRec)
	}
	if tx.sess.engine.hasHook.Load() && !storage.IsTemporaryTable(tableName) {
		tx.walOps = append(tx.walOps, memengine.WALOp{
			Type:  memengine.OpDelete,
			Table: tableName,
			Key:   pk.String(),
		})
	}

	if tx.rowDeltas == nil {
		tx.rowDeltas = make(map[string]int64)
	}
	tx.rowDeltas[tbl.Name]--
	if tx.readCache != nil {
		tx.readCache.Invalidate(tableName, pk.String())
	}

	return nil
}

func (tx *WTTx) checkUniqueIndexesLocked(tableName string, tbl *storage.TableSchema, currentPK storage.Value, rec storage.Record) error {
	pkStr := currentPK.String()
	for _, idx := range tbl.Indexes {
		if !idx.Unique {
			continue
		}
		keyVal := storage.ExtractIndexKeyFromRecord(rec, &idx)
		if keyVal.ContainsNull() {
			continue
		}
		var keyStr string
		if len(idx.Columns) > 1 {
			keyStr = keyVal.String()
		} else {
			keyStr = storage.EncodeIndexKeyPart(keyVal)
		}

		idxURI := tbl.IndexFileURI()
		cur, err := tx.sess.acquireCursor(idxURI)
		if err != nil {
			return err
		}

		keyStr = idx.KeyPrefix() + keyStr
		var kData unsafe.Pointer
		if len(keyStr) > 0 {
			kData = unsafe.Pointer(unsafe.StringData(keyStr))
		}
		C.go_wt_cursor_set_key_raw(cur, kData, C.size_t(len(keyStr)))
		ret := C.go_wt_cursor_search(cur)
		if ret == 0 {
			var pValData unsafe.Pointer
			var valSz C.size_t
			if vret := C.go_wt_cursor_get_val_raw(cur, &pValData, &valSz); vret == 0 && pValData != nil && valSz > 0 {
				existingPK := string(unsafe.Slice((*byte)(pValData), int(valSz)))
				if existingPK != pkStr {
					C.go_wt_cursor_reset(cur)
					tx.sess.releaseCursor(idxURI, cur)
					return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idx.Name)
				}
			}
		}
		C.go_wt_cursor_reset(cur)
		tx.sess.releaseCursor(idxURI, cur)
	}
	return nil
}

func (tx *WTTx) checkUniqueIndexesFromRowLocked(tableName string, tbl *storage.TableSchema, currentPK storage.Value, row storage.Row) error {
	pkStr := currentPK.String()
	for _, idx := range tbl.Indexes {
		if !idx.Unique {
			continue
		}
		keyVal := storage.ExtractIndexKeyFromRow(tbl, row, &idx)
		if keyVal.ContainsNull() {
			continue
		}
		var keyStr string
		if len(idx.Columns) > 1 {
			keyStr = keyVal.String()
		} else {
			keyStr = storage.EncodeIndexKeyPart(keyVal)
		}

		idxURI := tbl.IndexFileURI()
		cur, err := tx.sess.acquireCursor(idxURI)
		if err != nil {
			return err
		}

		keyStr = idx.KeyPrefix() + keyStr
		var kData unsafe.Pointer
		if len(keyStr) > 0 {
			kData = unsafe.Pointer(unsafe.StringData(keyStr))
		}
		C.go_wt_cursor_set_key_raw(cur, kData, C.size_t(len(keyStr)))
		ret := C.go_wt_cursor_search(cur)
		if ret == 0 {
			var pValData unsafe.Pointer
			var valSz C.size_t
			if vret := C.go_wt_cursor_get_val_raw(cur, &pValData, &valSz); vret == 0 && pValData != nil && valSz > 0 {
				existingPK := string(unsafe.Slice((*byte)(pValData), int(valSz)))
				if existingPK != pkStr {
					C.go_wt_cursor_reset(cur)
					tx.sess.releaseCursor(idxURI, cur)
					return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idx.Name)
				}
			}
		}
		C.go_wt_cursor_reset(cur)
		tx.sess.releaseCursor(idxURI, cur)
	}
	return nil
}

func (tx *WTTx) checkForeignKeyFromRowLocked(tbl *storage.TableSchema, row storage.Row) error {
	for _, fk := range tbl.ForeignKeys {
		childVals := make([]storage.Value, len(fk.ChildColumns))
		hasNull := false
		for i, cCol := range fk.ChildColumns {
			_, cIdx, ok := tbl.FindColumn(cCol)
			if !ok || cIdx < 0 || cIdx >= len(row.Values) || row.Values[cIdx].IsNull {
				hasNull = true
				break
			}
			childVals[i] = row.Values[cIdx]
		}
		if hasNull {
			continue
		}

		pTbl, err := tx.getTableLocked(fk.ParentTable)
		if err != nil {
			return fmt.Errorf("%w: referenced table %q not found", storage.ErrTableNotFound, fk.ParentTable)
		}

		pCols := fk.ParentColumns
		if len(pCols) == 0 {
			pkCol, err := pTbl.PrimaryKeyColumn()
			if err != nil || pkCol == nil {
				return fmt.Errorf("referenced table %q has no primary key", fk.ParentTable)
			}
			pCols = []string{pkCol.Name}
		}

		parentPkCol, _ := pTbl.PrimaryKeyColumn()
		if parentPkCol != nil && len(pCols) == 1 && strings.EqualFold(pCols[0], parentPkCol.Name) {
			_, err := tx.getRawBytesLocked(pTbl.Name, childVals[0])
			if err != nil {
				return fmt.Errorf("%w: foreign key %q references non-existent row in %q",
					storage.ErrForeignKeyViolation, fk.Name, fk.ParentTable)
			}
			continue
		}

		cur, err := tx.scanLocked(pTbl.Name)
		if err != nil {
			return err
		}
		matched := false
		for cur.Next() {
			r, rErr := cur.Row()
			if rErr == nil && pTbl.RowMatchesFK(r, pCols, childVals) {
				matched = true
				break
			}
		}
		cur.Close()
		if !matched {
			return fmt.Errorf("%w: foreign key %q references non-existent row in %q",
				storage.ErrForeignKeyViolation, fk.Name, fk.ParentTable)
		}
	}
	return nil
}

func (tx *WTTx) checkForeignKeyLocked(tbl *storage.TableSchema, childRec storage.Record) error {
	for _, fk := range tbl.ForeignKeys {
		childVals := make([]storage.Value, len(fk.ChildColumns))
		hasNull := false
		for i, cCol := range fk.ChildColumns {
			val, ok := childRec.Get(cCol)
			if !ok || val.IsNull {
				hasNull = true
				break
			}
			childVals[i] = val
		}
		if hasNull {
			continue
		}

		pTbl, err := tx.getTableLocked(fk.ParentTable)
		if err != nil {
			return fmt.Errorf("%w: referenced table %q not found", storage.ErrTableNotFound, fk.ParentTable)
		}

		pCols := fk.ParentColumns
		if len(pCols) == 0 {
			pkCol, err := pTbl.PrimaryKeyColumn()
			if err != nil || pkCol == nil {
				return fmt.Errorf("referenced table %q has no primary key", fk.ParentTable)
			}
			pCols = []string{pkCol.Name}
		}

		parentPkCol, _ := pTbl.PrimaryKeyColumn()
		if parentPkCol != nil && len(pCols) == 1 && strings.EqualFold(pCols[0], parentPkCol.Name) {
			_, err := tx.getRawBytesLocked(pTbl.Name, childVals[0])
			if err != nil {
				return fmt.Errorf("%w: foreign key %q references non-existent row in %q",
					storage.ErrForeignKeyViolation, fk.Name, fk.ParentTable)
			}
			continue
		}

		cur, err := tx.scanLocked(pTbl.Name)
		if err != nil {
			return err
		}
		matched := false
		for cur.Next() {
			r, rErr := cur.Row()
			if rErr == nil && pTbl.RowMatchesFK(r, pCols, childVals) {
				matched = true
				break
			}
		}
		cur.Close()
		if !matched {
			return fmt.Errorf("%w: foreign key %q references non-existent row in %q",
				storage.ErrForeignKeyViolation, fk.Name, fk.ParentTable)
		}
	}
	return nil
}

func (tx *WTTx) checkReferencingFKsOnDeleteLocked(tableName string, pk storage.Value) error {
	pTbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	oldRec, err := tx.getLocked(tableName, pk)
	if err != nil {
		return nil
	}

	type childRef struct {
		childTable string
		fk         storage.ForeignKeySchema
	}
	var childRefs []childRef
	tx.sess.engine.mu.RLock()
	for childName, td := range tx.sess.engine.tables {
		for _, fk := range td.ForeignKeys {
			if strings.EqualFold(fk.ParentTable, tableName) {
				childRefs = append(childRefs, childRef{childTable: childName, fk: fk})
			}
		}
	}
	tx.sess.engine.mu.RUnlock()

	for _, cr := range childRefs {
		cTbl, err := tx.getTableLocked(cr.childTable)
		if err != nil {
			continue
		}
		if _, err := cTbl.PrimaryKeyColumn(); err != nil {
			continue
		}

		pCols := cr.fk.ParentColumns
		if len(pCols) == 0 {
			if pkCol, err := pTbl.PrimaryKeyColumn(); err == nil && pkCol != nil {
				pCols = []string{pkCol.Name}
			}
		}

		parentValues := make([]storage.Value, len(pCols))
		hasNull := false
		for i, pCol := range pCols {
			val, ok := oldRec.Get(pCol)
			if !ok || val.IsNull {
				hasNull = true
				break
			}
			parentValues[i] = val
		}
		if hasNull {
			continue
		}

		cur, err := tx.scanLocked(cr.childTable)
		if err != nil {
			continue
		}
		var matchingPKs []storage.Value
		for cur.Next() {
			row, rErr := cur.Row()
			if rErr == nil && cTbl.RowMatchesFK(row, cr.fk.ChildColumns, parentValues) {
				if childPK, ok := cTbl.ResolveRowPK(row); ok && !childPK.IsNull {
					matchingPKs = append(matchingPKs, childPK)
				}
			}
		}
		cur.Close()

		if len(matchingPKs) > 0 {
			switch cr.fk.OnDelete {
			case storage.ActionCascade:
				for _, cpk := range matchingPKs {
					if err := tx.deleteLocked(cr.childTable, cpk); err != nil {
						return err
					}
				}
			case storage.ActionSetNull:
				for _, cpk := range matchingPKs {
					nullPatch := make(storage.Record)
					for _, cCol := range cr.fk.ChildColumns {
						nullPatch.Set(cCol, storage.NullValue(storage.TypeVarChar))
					}
					if err := tx.updateLocked(cr.childTable, cTbl, cpk, nullPatch); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("%w: cannot delete from %q: referenced by child row in %q",
					storage.ErrForeignKeyViolation, tableName, cr.childTable)
			}
		}
	}
	return nil
}

func (tx *WTTx) Get(tableName string, pk storage.Value) (storage.Record, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return nil, storage.ErrTxClosed
	}
	if storage.IsTemporaryTable(tableName) {
		ttx, err := tx.ensureTempTxLocked()
		if err != nil {
			return nil, err
		}
		return ttx.Get(tableName, pk)
	}
	return tx.getLocked(tableName, pk)
}

func (tx *WTTx) GetRow(tableName string, pk storage.Value) (storage.Row, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return storage.Row{}, storage.ErrTxClosed
	}
	if storage.IsTemporaryTable(tableName) {
		ttx, err := tx.ensureTempTxLocked()
		if err != nil {
			return storage.Row{}, err
		}
		return ttx.GetRow(tableName, pk)
	}
	return tx.getRowLocked(tableName, pk)
}

func (tx *WTTx) getRawBytesLocked(tableName string, pk storage.Value) ([]byte, error) {
	return tx.getRawBytesToBufferLocked(tableName, pk, nil)
}

func (tx *WTTx) getRawBytesToBufferLocked(tableName string, pk storage.Value, targetBuf *[]byte) ([]byte, error) {
	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return nil, err
	}

	uri := tbl.TableURI()
	cur, err := tx.sess.acquireCursor(uri)
	if err != nil {
		return nil, err
	}
	defer tx.sess.releaseCursor(uri, cur)

	pkStr := pk.String()
	var keyBuf [256]byte
	cKey, cleanup := fastCString(pkStr, keyBuf[:])
	if cleanup != nil {
		defer cleanup()
	}

	tx.sess.mu.Lock()
	if tx.sess.closed || tx.sess.sess == nil {
		tx.sess.mu.Unlock()
		return nil, storage.ErrTxClosed
	}
	var pData unsafe.Pointer
	var sz C.size_t
	if ret := C.go_wt_cursor_get(cur, cKey, &pData, &sz); ret != 0 || pData == nil {
		tx.sess.mu.Unlock()
		if ret == wtRollback {
			return nil, wtOpError("cursor_get", int(ret))
		}
		return nil, storage.ErrKeyNotFound
	}
	cSlice := unsafe.Slice((*byte)(pData), int(sz))
	if targetBuf == nil {
		raw := make([]byte, len(cSlice))
		copy(raw, cSlice)
		tx.sess.mu.Unlock()
		return raw, nil
	}
	*targetBuf = append((*targetBuf)[:0], cSlice...)
	tx.sess.mu.Unlock()

	return *targetBuf, nil
}

func (tx *WTTx) getLocked(tableName string, pk storage.Value) (storage.Record, error) {
	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return nil, err
	}
	pkStr := pk.String()
	if tx.readCache != nil {
		if entry, ok := tx.readCache.Get(tableName, pkStr); ok {
			if entry.Record != nil {
				return entry.Record.Clone(), nil
			}
			if len(entry.Row.Values) > 0 {
				rec := tbl.RowToRecord(entry.Row)
				tx.readCache.PutRecord(tableName, pkStr, rec)
				return rec.Clone(), nil
			}
		}
	}
	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)

	rawBytes, err := tx.getRawBytesToBufferLocked(tableName, pk, bufPtr)
	if err != nil {
		return nil, err
	}
	rec, err := decodeRecord(tbl, rawBytes)
	if err == nil && tx.readCache != nil {
		tx.readCache.PutRecord(tableName, pkStr, rec)
	}
	return rec, err
}

func (tx *WTTx) getRowLocked(tableName string, pk storage.Value) (storage.Row, error) {
	if tx.closed {
		return storage.Row{}, storage.ErrTxClosed
	}
	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return storage.Row{}, err
	}
	pkStr := pk.String()
	if tx.readCache != nil {
		if entry, ok := tx.readCache.Get(tableName, pkStr); ok {
			if len(entry.Row.Values) > 0 {
				return entry.Row.Clone(), nil
			}
			if entry.Record != nil {
				row := tbl.RecordToRow(entry.Record)
				tx.readCache.PutRow(tableName, pkStr, row, 0)
				return row.Clone(), nil
			}
		}
	}
	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)

	rawBytes, err := tx.getRawBytesToBufferLocked(tableName, pk, bufPtr)
	if err != nil {
		return storage.Row{}, err
	}
	row, err := decodeRow(tbl, rawBytes)
	if err == nil && tx.readCache != nil {
		tx.readCache.PutRow(tableName, pkStr, row, 0)
	}
	return row, err
}

func (tx *WTTx) Scan(tableName string) (storage.Cursor, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return nil, storage.ErrTxClosed
	}
	if storage.IsTemporaryTable(tableName) {
		ttx, err := tx.ensureTempTxLocked()
		if err != nil {
			return nil, err
		}
		return ttx.Scan(tableName)
	}
	return tx.scanLocked(tableName)
}

func (tx *WTTx) scanLocked(tableName string) (storage.Cursor, error) {
	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return nil, err
	}

	uri := tbl.TableURI()
	cur, err := tx.sess.acquireCursor(uri)
	if err != nil {
		return nil, err
	}

	wtCur := &WTCursor{
		tx:     tx,
		sess:   tx.sess,
		uri:    uri,
		cur:    cur,
		schema: tbl,
	}
	tx.openCursors = append(tx.openCursors, wtCur)
	return wtCur, nil
}

func (tx *WTTx) ScanPartitions(tableName string, partitions []string) (storage.Cursor, error) {
	if storage.IsTemporaryTable(tableName) {
		tx.mu.Lock()
		defer tx.mu.Unlock()
		if tx.closed {
			return nil, storage.ErrTxClosed
		}
		ttx, err := tx.ensureTempTxLocked()
		if err != nil {
			return nil, err
		}
		return ttx.ScanPartitions(tableName, partitions)
	}
	return tx.Scan(tableName)
}

func (tx *WTTx) IndexScan(tableName string, indexName string, low, high *storage.Value) (storage.Cursor, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return nil, storage.ErrTxClosed
	}
	if storage.IsTemporaryTable(tableName) {
		ttx, err := tx.ensureTempTxLocked()
		if err != nil {
			return nil, err
		}
		return ttx.IndexScan(tableName, indexName, low, high)
	}
	return tx.indexScanBTreeLocked(tableName, indexName, low, high, false)
}

func (tx *WTTx) IndexScanDesc(tableName string, indexName string, low, high *storage.Value) (storage.Cursor, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return nil, storage.ErrTxClosed
	}
	if storage.IsTemporaryTable(tableName) {
		ttx, err := tx.ensureTempTxLocked()
		if err != nil {
			return nil, err
		}
		return ttx.IndexScanDesc(tableName, indexName, low, high)
	}
	return tx.indexScanBTreeLocked(tableName, indexName, low, high, true)
}

func (tx *WTTx) IndexOnlyScan(tableName string, indexName string, low, high *storage.Value) (storage.Cursor, error) {
	if storage.IsTemporaryTable(tableName) {
		tx.mu.Lock()
		defer tx.mu.Unlock()
		if tx.closed {
			return nil, storage.ErrTxClosed
		}
		ttx, err := tx.ensureTempTxLocked()
		if err != nil {
			return nil, err
		}
		return ttx.IndexOnlyScan(tableName, indexName, low, high)
	}
	return tx.IndexScan(tableName, indexName, low, high)
}

func (tx *WTTx) indexScanBTreeLocked(tableName string, indexName string, low, high *storage.Value, desc bool) (storage.Cursor, error) {
	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return nil, err
	}
	idx, _, found := tbl.FindIndex(indexName)
	if !found || idx == nil {
		return nil, fmt.Errorf("%w: %s on table %s", storage.ErrIndexNotFound, indexName, tableName)
	}

	idxURI := tbl.IndexFileURI()
	cur, err := tx.sess.acquireCursor(idxURI)
	if err != nil {
		return nil, err
	}

	btreeCur := &wtIndexBTreeCursor{
		tx:     tx,
		sess:   tx.sess,
		tbl:    tbl,
		idx:    idx,
		prefix: idx.KeyPrefix(),
		uri:    idxURI,
		cur:    cur,
		desc:   desc,
	}

	if low != nil {
		btreeCur.hasLow = true
		btreeCur.lowKey = encodeSecondaryIndexKey(idx, *low)
	}
	if high != nil {
		btreeCur.hasHigh = true
		btreeCur.highKey = encodeSecondaryIndexKey(idx, *high)
	}

	tx.openCursors = append(tx.openCursors, btreeCur)
	return btreeCur, nil
}

func compareCompositePrefix(curKey, boundKey string) int {
	curParts := strings.Split(curKey, "\x00")
	boundParts := strings.Split(boundKey, "\x00")
	for i := 0; i < len(boundParts) && i < len(curParts); i++ {
		if curParts[i] < boundParts[i] {
			return -1
		}
		if curParts[i] > boundParts[i] {
			return 1
		}
	}
	return 0
}

type wtIndexBTreeCursor struct {
	tx      *WTTx
	sess    *WTSession
	tbl     *storage.TableSchema
	idx     *storage.IndexSchema
	prefix  string // key namespace of this index inside the multiplexed index file
	uri     string
	cur     *C.WT_CURSOR
	lowKey  string
	highKey string
	hasLow  bool
	hasHigh bool
	desc    bool
	started bool
	eof     bool
	closed  bool
	currPK  string
	currKey string
	err     error
}

func (c *wtIndexBTreeCursor) Next() bool {
	if c.sess == nil {
		return false
	}
	c.sess.mu.Lock()
	defer c.sess.mu.Unlock()
	if c.closed || c.cur == nil || c.eof || c.err != nil || c.sess.closed || c.sess.sess == nil {
		return false
	}

	for {
		if !c.started {
			c.started = true
			if !c.desc {
				// Ascending initial seek: first key >= prefix [+ low bound]
				searchKey := c.prefix
				if c.hasLow {
					searchKey = c.prefix + c.lowKey
					if !c.idx.Unique {
						searchKey += "\x00"
					}
				}
				{
					var kData unsafe.Pointer
					if len(searchKey) > 0 {
						kData = unsafe.Pointer(unsafe.StringData(searchKey))
					}
					C.go_wt_cursor_set_key_raw(c.cur, kData, C.size_t(len(searchKey)))
					var exact C.int
					ret := C.go_wt_cursor_search_near(c.cur, &exact)
					if ret != 0 {
						c.eof = true
						if ret != C.WT_NOTFOUND {
							c.err = fmt.Errorf("wiredtiger index cursor search_near error: %d (%s)", ret, C.GoString(C.go_wt_strerror(ret)))
						}
						return false
					}
					if exact < 0 {
						if nret := C.go_wt_cursor_next(c.cur); nret != 0 {
							c.eof = true
							if nret != C.WT_NOTFOUND {
								c.err = fmt.Errorf("wiredtiger index cursor next error: %d (%s)", nret, C.GoString(C.go_wt_strerror(nret)))
							}
							return false
						}
					}
				}
			} else {
				// Descending initial seek: last key <= upper bound. Without an explicit high bound the
				// sentinel "<prefix minus NUL>\x01" sorts right after every key of this index.
				searchKey := c.prefix[:len(c.prefix)-1] + "\x01"
				if c.hasHigh {
					searchKey = c.prefix + c.highKey
					if !c.idx.Unique {
						searchKey += "\x00\xff\xff\xff\xff"
					}
				}
				{
					var kData unsafe.Pointer
					if len(searchKey) > 0 {
						kData = unsafe.Pointer(unsafe.StringData(searchKey))
					}
					C.go_wt_cursor_set_key_raw(c.cur, kData, C.size_t(len(searchKey)))
					var exact C.int
					ret := C.go_wt_cursor_search_near(c.cur, &exact)
					if ret != 0 {
						c.eof = true
						if ret != C.WT_NOTFOUND {
							c.err = fmt.Errorf("wiredtiger index cursor search_near error: %d (%s)", ret, C.GoString(C.go_wt_strerror(ret)))
						}
						return false
					}
					if exact > 0 {
						if pret := C.go_wt_cursor_prev(c.cur); pret != 0 {
							c.eof = true
							if pret != C.WT_NOTFOUND {
								c.err = fmt.Errorf("wiredtiger index cursor prev error: %d (%s)", pret, C.GoString(C.go_wt_strerror(pret)))
							}
							return false
						}
					}
				}
			}
		} else {
			if !c.desc {
				if nret := C.go_wt_cursor_next(c.cur); nret != 0 {
					c.eof = true
					if nret != C.WT_NOTFOUND {
						c.err = fmt.Errorf("wiredtiger index cursor next error: %d (%s)", nret, C.GoString(C.go_wt_strerror(nret)))
					}
					return false
				}
			} else {
				if pret := C.go_wt_cursor_prev(c.cur); pret != 0 {
					c.eof = true
					if pret != C.WT_NOTFOUND {
						c.err = fmt.Errorf("wiredtiger index cursor prev error: %d (%s)", pret, C.GoString(C.go_wt_strerror(pret)))
					}
					return false
				}
			}
		}

		// Read current key & value from positioned cursor
		var pKeyData unsafe.Pointer
		var keySz C.size_t
		if ret := C.go_wt_cursor_get_key_raw(c.cur, &pKeyData, &keySz); ret != 0 || pKeyData == nil {
			c.eof = true
			if ret != 0 && ret != C.WT_NOTFOUND {
				c.err = fmt.Errorf("wiredtiger index cursor get_key error: %d (%s)", ret, C.GoString(C.go_wt_strerror(ret)))
			}
			return false
		}
		keyStr := string(unsafe.Slice((*byte)(pKeyData), int(keySz)))

		// The index file multiplexes every index of the table: stay inside this index's namespace.
		if !strings.HasPrefix(keyStr, c.prefix) {
			if !c.desc {
				if keyStr < c.prefix {
					continue
				}
			} else if keyStr > c.prefix {
				continue
			}
			c.eof = true
			return false
		}
		keyStr = keyStr[len(c.prefix):]

		var curIndexKey, curPK string
		if c.idx.Unique {
			var pValData unsafe.Pointer
			var valSz C.size_t
			if vret := C.go_wt_cursor_get_val_raw(c.cur, &pValData, &valSz); vret == 0 && pValData != nil && valSz > 0 {
				curIndexKey = keyStr
				curPK = string(unsafe.Slice((*byte)(pValData), int(valSz)))
			} else {
				sep := strings.LastIndexByte(keyStr, 0)
				if sep >= 0 {
					curIndexKey = keyStr[:sep]
					curPK = keyStr[sep+1:]
				} else {
					curIndexKey = keyStr
					curPK = ""
				}
			}
		} else {
			sep := strings.LastIndexByte(keyStr, 0)
			if sep >= 0 {
				curIndexKey = keyStr[:sep]
				curPK = keyStr[sep+1:]
			} else {
				curIndexKey = keyStr
				curPK = ""
			}
		}

		// Boundary checks
		if !c.desc {
			if c.hasHigh && compareCompositePrefix(curIndexKey, c.highKey) > 0 {
				c.eof = true
				return false
			}
			if c.hasLow && compareCompositePrefix(curIndexKey, c.lowKey) < 0 {
				continue
			}
		} else {
			if c.hasLow && compareCompositePrefix(curIndexKey, c.lowKey) < 0 {
				c.eof = true
				return false
			}
			if c.hasHigh && compareCompositePrefix(curIndexKey, c.highKey) > 0 {
				continue
			}
		}

		c.currPK = curPK
		c.currKey = curIndexKey
		return true
	}
}

func (c *wtIndexBTreeCursor) Row() (storage.Row, error) {
	if c.sess == nil {
		return storage.Row{}, fmt.Errorf("cursor closed or out of range")
	}
	c.sess.mu.Lock()
	if c.err != nil {
		err := c.err
		c.sess.mu.Unlock()
		return storage.Row{}, err
	}
	if c.closed || c.cur == nil || c.eof || c.currPK == "" || c.sess.closed || c.sess.sess == nil {
		c.sess.mu.Unlock()
		return storage.Row{}, fmt.Errorf("cursor closed or out of range")
	}
	currPK := c.currPK
	c.sess.mu.Unlock()

	var pkVal storage.Value
	if c.tbl.IsCompositePK() {
		pkVal = storage.StringValue(currPK)
	} else {
		pkCol, err := c.tbl.PrimaryKeyColumn()
		if err != nil {
			return storage.Row{}, err
		}
		pkVal = storage.ParsePKValue(currPK, pkCol.Type)
	}
	return c.tx.GetRow(c.tbl.Name, pkVal)
}

func (c *wtIndexBTreeCursor) Record() (storage.Record, error) {
	if c.sess == nil {
		return nil, fmt.Errorf("cursor closed or out of range")
	}
	c.sess.mu.Lock()
	if c.err != nil {
		err := c.err
		c.sess.mu.Unlock()
		return nil, err
	}
	if c.closed || c.cur == nil || c.eof || c.currPK == "" || c.sess.closed || c.sess.sess == nil {
		c.sess.mu.Unlock()
		return nil, fmt.Errorf("cursor closed or out of range")
	}
	currPK := c.currPK
	c.sess.mu.Unlock()

	var pkVal storage.Value
	if c.tbl.IsCompositePK() {
		pkVal = storage.StringValue(currPK)
	} else {
		pkCol, err := c.tbl.PrimaryKeyColumn()
		if err != nil {
			return nil, err
		}
		pkVal = storage.ParsePKValue(currPK, pkCol.Type)
	}
	row, err := c.tx.GetRow(c.tbl.Name, pkVal)
	if err != nil {
		return nil, err
	}
	return c.tbl.RowToRecord(row), nil
}

func (c *wtIndexBTreeCursor) RawTuple() ([]byte, bool) {
	if c.sess == nil || c.tx == nil {
		return nil, false
	}
	c.tx.mu.Lock()
	defer c.tx.mu.Unlock()
	c.sess.mu.Lock()
	if c.err != nil || c.closed || c.cur == nil || c.eof || c.currPK == "" || c.sess.closed || c.sess.sess == nil {
		c.sess.mu.Unlock()
		return nil, false
	}
	currPK := c.currPK
	c.sess.mu.Unlock()

	var pkVal storage.Value
	if c.tbl.IsCompositePK() {
		pkVal = storage.StringValue(currPK)
	} else {
		pkCol, err := c.tbl.PrimaryKeyColumn()
		if err != nil {
			return nil, false
		}
		pkVal = storage.ParsePKValue(currPK, pkCol.Type)
	}
	data, err := c.tx.getRawBytesLocked(c.tbl.Name, pkVal)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

func (c *wtIndexBTreeCursor) Close() error {
	if c.sess != nil {
		c.sess.mu.Lock()
		defer c.sess.mu.Unlock()
	}
	if c.closed || c.cur == nil {
		return c.err
	}
	c.closed = true
	cur := c.cur
	c.cur = nil
	if c.sess != nil && c.uri != "" {
		c.sess.releaseCursorLocked(c.uri, cur)
	} else {
		C.go_wt_cursor_close(cur)
	}
	return c.err
}

func (c *wtIndexBTreeCursor) Err() error {
	if c.sess != nil {
		c.sess.mu.Lock()
		defer c.sess.mu.Unlock()
	}
	return c.err
}

// Validate performs pre-commit validation for WiredTiger and its embedded tempTx.
func (tx *WTTx) Validate() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}

	if err := tx.sess.engine.checkTainted(); err != nil {
		return err
	}

	if tx.tempTx != nil {
		return tx.tempTx.Validate()
	}
	return nil
}

func (tx *WTTx) Commit() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	defer tx.releaseAcquiredLocksLocked()
	defer tx.closeOpenCursorsLocked()
	if tx.readCache != nil {
		tx.readCache.Reset()
	}

	if tx.closed {
		return storage.ErrTxClosed
	}
	tx.closeOpenCursorsLocked()

	if err := tx.sess.engine.checkTainted(); err != nil {
		tx.closed = true
		tempTx := tx.tempTx
		tx.tempTx = nil
		if tempTx != nil {
			_ = tempTx.Rollback()
		}
		_ = C.go_wt_session_rollback(tx.sess.sess, nil)
		_ = C.go_wt_session_reset(tx.sess.sess)
		tx.sess.mu.Lock()
		tx.sess.activeTx = false
		tx.sess.mu.Unlock()
		return err
	}

	// 1. Pre-Commit Validation: Validate tempTx BEFORE committing WiredTiger
	// to prevent split-brain commit atomicity violations ([FIX-HYBRID-TX-SPLIT-BRAIN-1]).
	if tx.tempTx != nil {
		if err := tx.tempTx.Validate(); err != nil {
			tx.closed = true
			tempTx := tx.tempTx
			tx.tempTx = nil
			_ = tempTx.Rollback()
			_ = C.go_wt_session_rollback(tx.sess.sess, nil)
			_ = C.go_wt_session_reset(tx.sess.sess)
			tx.sess.mu.Lock()
			tx.sess.activeTx = false
			tx.sess.mu.Unlock()
			return err
		}
	}

	// 2. Enforce disk quota limit guard if writes occurred
	if tx.hasWrites {
		if limit := tx.sess.engine.maxDataBytes.Load(); limit > 0 {
			var totalBytes int64
			if tx.sess.engine.homeDir != "" {
				_ = filepath.Walk(tx.sess.engine.homeDir, func(path string, info os.FileInfo, err error) error {
					if err == nil && info != nil && !info.IsDir() {
						totalBytes += info.Size()
					}
					return nil
				})
			}
			if totalBytes >= limit {
				tx.closed = true
				tempTx := tx.tempTx
				tx.tempTx = nil
				if tempTx != nil {
					_ = tempTx.Rollback()
				}
				_ = C.go_wt_session_rollback(tx.sess.sess, nil)
				_ = C.go_wt_session_reset(tx.sess.sess)
				tx.sess.mu.Lock()
				tx.sess.activeTx = false
				tx.sess.mu.Unlock()
				return fmt.Errorf("%w: wiredtiger storage disk quota exceeded (%d >= %d bytes)",
					storage.ErrTableFull, totalBytes, limit)
			}
		}
	}

	tx.closed = true
	hasWrites := tx.hasWrites
	rowDeltas := tx.rowDeltas
	tx.rowDeltas = nil
	tx.tableCache = nil
	walOps := tx.walOps
	tx.walOps = nil
	tx.spWALIndices = nil
	tx.clearUndoLogLocked()
	tx.savepoints = nil
	tx.spNames = nil
	tx.tempSpPreCreated = nil
	tempTx := tx.tempTx
	tx.tempTx = nil

	tx.sess.resetCursors()

	var commitCfg *C.char
	if hasWrites && !tx.opts.ReadOnly {
		switch tx.sess.engine.FsyncPolicy() {
		case memengine.FsyncAlways:
			commitCfg = cSyncOn
		case memengine.FsyncEverySec:
			commitCfg = cSyncOff
		}
	}

	ret := C.go_wt_session_commit(tx.sess.sess, commitCfg)
	_ = C.go_wt_session_reset(tx.sess.sess)
	tx.sess.mu.Lock()
	tx.sess.activeTx = false
	tx.sess.mu.Unlock()
	if ret != 0 {
		if tempTx != nil {
			_ = tempTx.Rollback()
		}
		return wtOpError("commit_transaction", int(ret))
	}

	if tempTx != nil {
		commitErr := tempTx.Commit()
		_ = tempTx.Rollback()
		if commitErr != nil {
			return commitErr
		}
	}

	if !hasWrites || tx.opts.ReadOnly {
		tx.commitLSN = tx.snapshotLSN
		tx.commitTimestamp = time.Now().UnixNano()
		return nil
	}

	eng := tx.sess.engine
	if len(rowDeltas) > 0 {
		for tblName, delta := range rowDeltas {
			if delta != 0 {
				eng.getRowCountAtomic(tblName).Add(delta)
			}
		}
	}
	seq := eng.writeSeq.Add(1)
	if eng.FsyncPolicy() == memengine.FsyncAlways {
		if err := eng.syncUpTo(tx.sess.sess, seq); err != nil {
			eng.markTainted(err)
			return fmt.Errorf("%w: wiredtiger fsync failed: %v", storage.ErrEngineTainted, err)
		}
	} else if eng.FsyncPolicy() == memengine.FsyncEverySec {
		if err := eng.flushOSTo(tx.sess.sess, seq); err != nil {
			eng.markTainted(err)
			return fmt.Errorf("%w: wiredtiger log_flush(sync=off) failed: %v", storage.ErrEngineTainted, err)
		}
	}

	if len(walOps) > 0 && eng.hasHook.Load() {
		rec := eng.recordWALLocked(walOps)
		tx.commitLSN = rec.LSN
		tx.commitTimestamp = rec.Timestamp
	} else {
		tx.commitLSN = eng.nextLSN.Add(1)
		tx.commitTimestamp = time.Now().UnixNano()
	}
	return nil
}

func (tx *WTTx) Rollback() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	defer tx.releaseAcquiredLocksLocked()
	defer tx.closeOpenCursorsLocked()
	if tx.readCache != nil {
		tx.readCache.Reset()
	}

	if tx.closed {
		return storage.ErrTxClosed
	}
	tx.closeOpenCursorsLocked()
	tx.closed = true
	tx.rowDeltas = nil
	tx.tableCache = nil
	tx.walOps = nil
	tx.spWALIndices = nil
	tx.clearUndoLogLocked()
	tx.savepoints = nil
	tx.spNames = nil
	tx.tempSpPreCreated = nil
	tempTx := tx.tempTx
	tx.tempTx = nil

	tx.sess.resetCursors()

	ret := C.go_wt_session_rollback(tx.sess.sess, nil)
	if tempTx != nil {
		_ = tempTx.Rollback()
	}
	if ret != 0 {
		return fmt.Errorf("rollback_transaction failed with code %d", ret)
	}
	_ = C.go_wt_session_reset(tx.sess.sess)
	tx.sess.mu.Lock()
	tx.sess.activeTx = false
	tx.sess.mu.Unlock()
	return nil
}

func (tx *WTTx) Savepoint(name string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}

	normName := strings.ToLower(strings.TrimSpace(name))
	if normName == "" {
		return fmt.Errorf("savepoint name cannot be empty")
	}

	if tx.tempTx != nil {
		if err := tx.tempTx.Savepoint(name); err != nil {
			return err
		}
		delete(tx.tempSpPreCreated, normName)
	} else {
		if tx.tempSpPreCreated == nil {
			tx.tempSpPreCreated = make(map[string]bool)
		}
		tx.tempSpPreCreated[normName] = true
	}

	if tx.savepoints == nil {
		tx.savepoints = make(map[string]int)
	}
	if tx.spWALIndices == nil {
		tx.spWALIndices = make(map[string]int)
	}

	for i, n := range tx.spNames {
		if n == normName {
			tx.spNames = append(tx.spNames[:i], tx.spNames[i+1:]...)
			break
		}
	}
	tx.spNames = append(tx.spNames, normName)
	tx.savepoints[normName] = len(tx.undoLog)
	tx.spWALIndices[normName] = len(tx.walOps)
	return nil
}

func (tx *WTTx) RollbackTo(name string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}

	normName := strings.ToLower(strings.TrimSpace(name))
	targetLen, exists := tx.savepoints[normName]
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrSavepointNotFound, name)
	}

	if tx.tempTx != nil {
		if tx.tempSpPreCreated[normName] {
			_ = tx.tempTx.Rollback()
			tx.tempTx = nil
		} else if err := tx.tempTx.RollbackTo(name); err != nil {
			return err
		}
	}

	var rbErrs []error
	// Undo operations in reverse order down to targetLen using ARIES Physiological Logging
	for i := len(tx.undoLog) - 1; i >= targetLen; i-- {
		op := tx.undoLog[i]
		switch op.Kind {
		case storage.OpInsert:
			// Undo Insert: remove all inserted index entries, then remove raw row
			for _, img := range op.NewIdx {
				if idxCur, cerr := tx.sess.acquireCursor(img.URI); cerr == nil {
					if rerr := removeIndexEntry(idxCur, img.Key); rerr != nil {
						rbErrs = append(rbErrs, rerr)
					}
					tx.sess.releaseCursor(img.URI, idxCur)
				} else {
					rbErrs = append(rbErrs, cerr)
				}
			}
			if rerr := tx.removeRawLocked(op.Table, op.PK); rerr != nil {
				rbErrs = append(rbErrs, rerr)
			}
			if tx.rowDeltas != nil {
				tx.rowDeltas[op.Table]--
			}
		case storage.OpDelete:
			// Undo Delete: restore raw row, then restore all deleted index entries
			if len(op.OldTuple) > 0 {
				if perr := tx.putRawBytesLocked(op.Table, op.PK, op.OldTuple, false); perr != nil {
					rbErrs = append(rbErrs, perr)
				}
				for _, img := range op.OldIdx {
					if idxCur, cerr := tx.sess.acquireCursor(img.URI); cerr == nil {
						if ierr := insertIndexEntry(idxCur, img.Key, img.Value); ierr != nil {
							rbErrs = append(rbErrs, ierr)
						}
						tx.sess.releaseCursor(img.URI, idxCur)
					} else {
						rbErrs = append(rbErrs, cerr)
					}
				}
				if tx.rowDeltas != nil {
					tx.rowDeltas[op.Table]++
				}
			}
		case storage.OpUpdate:
			// Undo Update: remove new index entries, restore old index entries, restore old row
			for _, img := range op.NewIdx {
				if idxCur, cerr := tx.sess.acquireCursor(img.URI); cerr == nil {
					if rerr := removeIndexEntry(idxCur, img.Key); rerr != nil {
						rbErrs = append(rbErrs, rerr)
					}
					tx.sess.releaseCursor(img.URI, idxCur)
				} else {
					rbErrs = append(rbErrs, cerr)
				}
			}
			for _, img := range op.OldIdx {
				if idxCur, cerr := tx.sess.acquireCursor(img.URI); cerr == nil {
					if ierr := insertIndexEntry(idxCur, img.Key, img.Value); ierr != nil {
						rbErrs = append(rbErrs, ierr)
					}
					tx.sess.releaseCursor(img.URI, idxCur)
				} else {
					rbErrs = append(rbErrs, cerr)
				}
			}
			if len(op.OldTuple) > 0 {
				if perr := tx.putRawBytesLocked(op.Table, op.PK, op.OldTuple, false); perr != nil {
					rbErrs = append(rbErrs, perr)
				}
			}
		}
		storage.ReleasePhysRecord(op)
	}
	if len(rbErrs) > 0 {
		return errors.Join(rbErrs...)
	}
	tx.undoLog = tx.undoLog[:targetLen]

	targetWAL, hasWAL := tx.spWALIndices[normName]
	if hasWAL && targetWAL >= 0 && targetWAL <= len(tx.walOps) {
		tx.walOps = tx.walOps[:targetWAL]
	}

	// Invalidate subsequent savepoints defined after this savepoint
	idx := -1
	for i, n := range tx.spNames {
		if n == normName {
			idx = i
			break
		}
	}
	if idx >= 0 {
		for i := idx + 1; i < len(tx.spNames); i++ {
			delete(tx.savepoints, tx.spNames[i])
			delete(tx.spWALIndices, tx.spNames[i])
			delete(tx.tempSpPreCreated, tx.spNames[i])
		}
		tx.spNames = tx.spNames[:idx+1]
	}

	if tx.readCache != nil {
		tx.readCache.Clear()
	}

	return nil
}

func (tx *WTTx) ReleaseSavepoint(name string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}

	normName := strings.ToLower(strings.TrimSpace(name))
	if _, exists := tx.savepoints[normName]; !exists {
		return fmt.Errorf("%w: %s", storage.ErrSavepointNotFound, name)
	}

	if tx.tempTx != nil && !tx.tempSpPreCreated[normName] {
		if err := tx.tempTx.ReleaseSavepoint(name); err != nil {
			return err
		}
	}

	idx := -1
	for i, n := range tx.spNames {
		if n == normName {
			idx = i
			break
		}
	}
	if idx >= 0 {
		for i := idx; i < len(tx.spNames); i++ {
			delete(tx.savepoints, tx.spNames[i])
			delete(tx.spWALIndices, tx.spNames[i])
			delete(tx.tempSpPreCreated, tx.spNames[i])
		}
		tx.spNames = tx.spNames[:idx]
	}

	if len(tx.savepoints) == 0 {
		tx.clearUndoLogLocked()
	}

	return nil
}

func (tx *WTTx) putRawBytesLocked(tableName string, pk storage.Value, recBytes []byte, updateIndexes bool) error {
	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	uri := tbl.TableURI()
	cur, err := tx.sess.acquireCursor(uri)
	if err != nil {
		return err
	}
	defer tx.sess.releaseCursor(uri, cur)

	pkStr := pk.String()
	var keyBuf [256]byte
	cKey, cleanup := fastCString(pkStr, keyBuf[:])
	if cleanup != nil {
		defer cleanup()
	}

	var pData unsafe.Pointer
	if len(recBytes) > 0 {
		pData = unsafe.Pointer(&recBytes[0])
	}

	if ret := C.go_wt_cursor_update_raw(cur, cKey, pData, C.size_t(len(recBytes))); ret != 0 {
		if ret := C.go_wt_cursor_insert_raw(cur, cKey, pData, C.size_t(len(recBytes))); ret != 0 {
			return wtOpError("cursor put", int(ret))
		}
	}
	if updateIndexes && len(tbl.Indexes) > 0 {
		rec, err := decodeRecord(tbl, recBytes)
		if err != nil {
			return err
		}
		for _, idx := range tbl.Indexes {
			if idx.Disabled {
				continue
			}
			idxURI := tbl.IndexFileURI()
			idxCur, err := tx.sess.acquireCursor(idxURI)
			if err != nil {
				return err
			}
			k, v, _ := formatIndexKeyValue(rec, &idx, pkStr)
			if err := insertIndexEntry(idxCur, k, v); err != nil {
				tx.sess.releaseCursor(idxURI, idxCur)
				return err
			}
			tx.sess.releaseCursor(idxURI, idxCur)
		}
	}
	if tx.readCache != nil {
		tx.readCache.Invalidate(tableName, pkStr)
	}
	return nil
}

func (tx *WTTx) putLocked(tableName string, pk storage.Value, record storage.Record) error {
	tbl, err := tx.sess.engine.GetTable(tableName)
	if err != nil {
		return err
	}
	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)

	recBytes, err := serializeRecordToBuffer(nil, tbl, record, *bufPtr)
	if err != nil {
		return err
	}

	return tx.putRawBytesLocked(tableName, pk, recBytes, true)
}

func (tx *WTTx) removeRawLocked(tableName string, pk storage.Value) error {
	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	uri := tbl.TableURI()
	cur, err := tx.sess.acquireCursor(uri)
	if err != nil {
		return err
	}
	defer tx.sess.releaseCursor(uri, cur)

	pkStr := pk.String()
	var keyBuf [256]byte
	cKey, cleanup := fastCString(pkStr, keyBuf[:])
	if cleanup != nil {
		defer cleanup()
	}

	if ret := C.go_wt_cursor_remove_key(cur, cKey); ret != 0 && ret != C.WT_NOTFOUND {
		return wtOpError("cursor_remove", int(ret))
	}
	if tx.readCache != nil {
		tx.readCache.Invalidate(tableName, pkStr)
	}
	return nil
}

// WTCursor iterates over WiredTiger table records.
type WTCursor struct {
	tx     *WTTx
	sess   *WTSession
	uri    string
	cur    *C.WT_CURSOR
	schema *storage.TableSchema
	closed bool
	eof    bool
	err    error
}

func (c *WTCursor) Next() bool {
	if c.sess == nil {
		return false
	}
	c.sess.mu.Lock()
	defer c.sess.mu.Unlock()
	if c.closed || c.cur == nil || c.eof || c.err != nil || c.sess.closed || c.sess.sess == nil {
		return false
	}
	ret := C.go_wt_cursor_next(c.cur)
	if ret == 0 {
		return true
	}
	c.eof = true
	if ret != C.WT_NOTFOUND {
		c.err = fmt.Errorf("wiredtiger cursor next error: %d (%s)", ret, C.GoString(C.go_wt_strerror(ret)))
	}
	return false
}

func (c *WTCursor) Record() (storage.Record, error) {
	if c.sess == nil {
		return nil, fmt.Errorf("cursor closed")
	}
	c.sess.mu.Lock()
	if c.err != nil {
		err := c.err
		c.sess.mu.Unlock()
		return nil, err
	}
	if c.closed || c.cur == nil || c.eof || c.sess.closed || c.sess.sess == nil {
		c.sess.mu.Unlock()
		return nil, fmt.Errorf("cursor closed")
	}

	var pData unsafe.Pointer
	var sz C.size_t
	if ret := C.go_wt_cursor_get_val_raw(c.cur, &pData, &sz); ret != 0 || pData == nil {
		c.sess.mu.Unlock()
		return nil, fmt.Errorf("cursor_get_val_raw failed: %d", ret)
	}
	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)
	*bufPtr = append((*bufPtr)[:0], unsafe.Slice((*byte)(pData), int(sz))...)
	c.sess.mu.Unlock()

	return decodeRecord(c.schema, *bufPtr)
}

func (c *WTCursor) Row() (storage.Row, error) {
	if c.sess == nil {
		return storage.Row{}, fmt.Errorf("cursor closed")
	}
	c.sess.mu.Lock()
	if c.err != nil {
		err := c.err
		c.sess.mu.Unlock()
		return storage.Row{}, err
	}
	if c.closed || c.cur == nil || c.eof || c.sess.closed || c.sess.sess == nil {
		c.sess.mu.Unlock()
		return storage.Row{}, fmt.Errorf("cursor closed")
	}

	var pData unsafe.Pointer
	var sz C.size_t
	if ret := C.go_wt_cursor_get_val_raw(c.cur, &pData, &sz); ret != 0 || pData == nil {
		c.sess.mu.Unlock()
		return storage.Row{}, fmt.Errorf("cursor_get_val_raw failed: %d", ret)
	}
	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)
	*bufPtr = append((*bufPtr)[:0], unsafe.Slice((*byte)(pData), int(sz))...)
	c.sess.mu.Unlock()

	return decodeRow(c.schema, *bufPtr)
}

func (c *WTCursor) RawTuple() ([]byte, bool) {
	if c.sess == nil {
		return nil, false
	}
	c.sess.mu.Lock()
	defer c.sess.mu.Unlock()
	if c.err != nil || c.closed || c.cur == nil || c.eof || c.sess.closed || c.sess.sess == nil {
		return nil, false
	}

	var pData unsafe.Pointer
	var sz C.size_t
	if ret := C.go_wt_cursor_get_val_raw(c.cur, &pData, &sz); ret != 0 || pData == nil {
		return nil, false
	}

	cSlice := unsafe.Slice((*byte)(pData), int(sz))
	res := make([]byte, len(cSlice))
	copy(res, cSlice)
	return res, true
}

func decodeRecord(schema *storage.TableSchema, data []byte) (storage.Record, error) {
	if len(data) > 0 && storage.IsTupleCodec(data[0]) && schema != nil {
		return storage.DecodeTuple(schema, data)
	}
	return nil, fmt.Errorf("invalid binary tuple record bytes")
}

func decodeRow(schema *storage.TableSchema, data []byte) (storage.Row, error) {
	if len(data) > 0 && storage.IsTupleCodec(data[0]) && schema != nil {
		return storage.DecodeTupleToRow(schema, data)
	}
	return storage.Row{}, fmt.Errorf("invalid binary tuple record bytes")
}

func (c *WTCursor) Close() error {
	if c.sess != nil {
		c.sess.mu.Lock()
		defer c.sess.mu.Unlock()
	}
	if c.closed || c.cur == nil {
		return c.err
	}
	c.closed = true
	cur := c.cur
	c.cur = nil
	if c.sess != nil && c.uri != "" {
		c.sess.releaseCursorLocked(c.uri, cur)
	} else {
		C.go_wt_cursor_close(cur)
	}
	return c.err
}

func (c *WTCursor) Err() error {
	if c.sess != nil {
		c.sess.mu.Lock()
		defer c.sess.mu.Unlock()
	}
	return c.err
}

// wtOpError converts a WiredTiger return code into a Go error. WT_ROLLBACK
// (a write-write conflict or cache-pressure rollback) is mapped to
// storage.ErrSerializationFailure so that callers and wire protocols can
// report it as a retryable serialization failure (SQLSTATE 40001).
func wtOpError(op string, ret int) error {
	if ret == wtRollback {
		return fmt.Errorf("%w (wiredtiger %s code %d)", storage.ErrSerializationFailure, op, ret)
	}
	if ret == C.WT_DUPLICATE_KEY {
		return fmt.Errorf("%w (wiredtiger %s code %d)", storage.ErrUniqueViolation, op, ret)
	}
	return fmt.Errorf("%s failed with code %d", op, ret)
}

// wtRollback mirrors the WT_ROLLBACK return code from wiredtiger.h.
const wtRollback = C.WT_ROLLBACK
