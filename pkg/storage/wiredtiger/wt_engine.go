//go:build cgo && wiredtiger

package wiredtiger

/*
#cgo CFLAGS: -I/opt/homebrew/include -I/usr/local/include
#cgo LDFLAGS: -L/opt/homebrew/lib -L/usr/local/lib -lwiredtiger

#include <stdlib.h>
#include <string.h>
#include <wiredtiger.h>

static int go_wt_open(const char *home, const char *config, WT_CONNECTION **connp) {
    return wiredtiger_open(home, NULL, config, connp);
}

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
    return session->drop(session, name, NULL);
}

static int go_wt_session_truncate(WT_SESSION *session, const char *name) {
    return session->truncate(session, name, NULL, NULL, NULL);
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

static int go_wt_session_log_flush_sync(WT_SESSION *session) {
    return session->log_flush(session, "sync=on");
}

static int go_wt_open_cursor(WT_SESSION *session, const char *uri, WT_CURSOR **cursorp) {
    return session->open_cursor(session, uri, NULL, NULL, cursorp);
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
	"fmt"
	"os"
	"path/filepath"
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

// Engine implements storage.Engine via the WiredTiger C API.
type Engine struct {
	mu          sync.RWMutex
	conn        *C.WT_CONNECTION
	homeDir     string
	tables      map[string]*storage.TableSchema
	nextLSN     atomic.Int64
	walHook     func(rec memengine.WALRecord)
	hasHook     atomic.Bool
	walOrderMu  sync.Mutex
	fsyncPolicy atomic.Int32
	syncMu      sync.Mutex
	writeSeq    atomic.Int64
	syncedSeq   atomic.Int64
	syncCount   atomic.Int64
	stopCh      chan struct{}
	bgWg        sync.WaitGroup
	closed      bool
	connClosed  atomic.Bool
	sessMu      sync.Mutex
	sessions    map[*WTSession]struct{}
}

// New creates a new WiredTiger storage engine instance.
func New() *Engine {
	e := &Engine{
		tables:   make(map[string]*storage.TableSchema),
		sessions: make(map[*WTSession]struct{}),
	}
	e.fsyncPolicy.Store(int32(memengine.FsyncEverySec))
	return e
}

// SetFsyncPolicy configures the journal synchronization policy (FsyncNone, FsyncEverySec, FsyncAlways).
func (e *Engine) SetFsyncPolicy(policy memengine.FsyncPolicy) {
	e.fsyncPolicy.Store(int32(policy))
}

// FsyncPolicy returns the current journal synchronization policy.
func (e *Engine) FsyncPolicy() memengine.FsyncPolicy {
	return memengine.FsyncPolicy(e.fsyncPolicy.Load())
}

// SyncCount returns the number of journal log_flush(sync=on) calls issued by group commit or periodic sync.
func (e *Engine) SyncCount() int64 {
	return e.syncCount.Load()
}

// syncUpTo performs leader/follower group commit via WT_SESSION::log_flush(sync=on).
// The first waiter on syncMu flushes all committed transactions up to writeSeq in a
// single disk sync; followers covered by that flush return immediately without a syscall.
func (e *Engine) syncUpTo(sess *C.WT_SESSION, seq int64) error {
	e.syncMu.Lock()
	defer e.syncMu.Unlock()

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

func (e *Engine) startPeriodicSync(stopCh <-chan struct{}) {
	defer e.bgWg.Done()

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return
	}
	defer C.go_wt_session_close(sess)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			if e.FsyncPolicy() == memengine.FsyncEverySec {
				seq := e.writeSeq.Load()
				if seq > e.syncedSeq.Load() {
					_ = e.syncUpTo(sess, seq)
				}
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

func (e *Engine) Open(dir string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if dir == "" {
		dir = "data/wt"
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create wt directory: %w", err)
	}
	e.homeDir = dir

	cHome := C.CString(dir)
	defer C.free(unsafe.Pointer(cHome))

	cConfig := C.CString("create,cache_size=500M,session_max=2000,eviction=(threads_min=4,threads_max=8),checkpoint=(wait=60),log=(enabled=true)")
	defer C.free(unsafe.Pointer(cConfig))

	var conn *C.WT_CONNECTION
	ret := C.go_wt_open(cHome, cConfig, &conn)
	if ret != 0 {
		return fmt.Errorf("wiredtiger_open failed with code %d: %s", ret, C.GoString(C.wiredtiger_strerror(ret)))
	}

	e.conn = conn
	e.closed = false
	e.connClosed.Store(false)

	e.loadCatalog()

	stopCh := make(chan struct{})
	e.stopCh = stopCh
	e.bgWg.Add(1)
	go e.startPeriodicSync(stopCh)

	return nil
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
		for _, t := range tables {
			e.tables[t.Name] = t
		}
	}
}

func (e *Engine) saveCatalog() error {
	list := make([]*storage.TableSchema, 0, len(e.tables))
	for _, t := range e.tables {
		list = append(list, t)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(e.catalogPath(), data, 0644)
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
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := sqlutil.MapGetFold(e.tables, schema.Name); exists {
		return fmt.Errorf("%w: %s", storage.ErrTableExists, schema.Name)
	}
	if _, err := schema.PrimaryKeyColumn(); err != nil {
		return err
	}

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return fmt.Errorf("open_session failed: code %d", ret)
	}
	defer C.go_wt_session_close(sess)

	uri := C.CString(fmt.Sprintf("table:%s", schema.Name))
	defer C.free(unsafe.Pointer(uri))

	config := C.CString("key_format=S,value_format=u")
	defer C.free(unsafe.Pointer(config))

	ret := C.go_wt_session_create(sess, uri, config)
	if ret != 0 {
		return fmt.Errorf("session->create failed with code %d", ret)
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
	e.mu.Lock()
	defer e.mu.Unlock()

	tbl, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return fmt.Errorf("open_session failed: code %d", ret)
	}
	defer C.go_wt_session_close(sess)

	uri := C.CString(fmt.Sprintf("table:%s", tbl.Name))
	defer C.free(unsafe.Pointer(uri))

	C.go_wt_session_drop(sess, uri)

	sqlutil.MapDeleteFold(e.tables, tableName)
	if err := e.saveCatalog(); err != nil {
		return err
	}
	e.recordWALLocked([]memengine.WALOp{{
		Type:  memengine.OpDropTable,
		Table: tbl.Name,
	}})
	return nil
}

func (e *Engine) TruncateTable(tableName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	tbl, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	tableName = tbl.Name

	// Verify no other table has a foreign key referencing this table
	for otherName, otherSchema := range e.tables {
		if strings.EqualFold(otherName, tableName) {
			continue
		}
		for _, fk := range otherSchema.ForeignKeys {
			if strings.EqualFold(fk.ParentTable, tableName) {
				return fmt.Errorf("%w: cannot truncate table %q because it is referenced by foreign key %q on table %q",
					storage.ErrDependentObjectsExist, tableName, fk.Name, otherName)
			}
		}
	}

	e.evictAllCursors(tableName)

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return fmt.Errorf("open_session failed: code %d", ret)
	}
	defer C.go_wt_session_close(sess)

	uri := C.CString(fmt.Sprintf("table:%s", tableName))
	defer C.free(unsafe.Pointer(uri))

	if ret := C.go_wt_session_truncate(sess, uri); ret != 0 {
		// Fallback: drop and recreate table
		C.go_wt_session_drop(sess, uri)
		config := C.CString("key_format=S,value_format=u")
		defer C.free(unsafe.Pointer(config))
		if ret := C.go_wt_session_create(sess, uri, config); ret != 0 {
			return fmt.Errorf("recreate table failed: code %d", ret)
		}
	}

	e.recordWALLocked([]memengine.WALOp{{
		Type:  memengine.OpTruncateTable,
		Table: tableName,
	}})
	return nil
}

func (e *Engine) DropPartition(tableName, partitionName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	tbl, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	if !tbl.IsPartitioned() {
		return fmt.Errorf("%w: %s", storage.ErrTableNotPartitioned, tableName)
	}

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return fmt.Errorf("open_session failed: %d", ret)
	}
	defer C.go_wt_session_close(sess)

	uri := C.CString(fmt.Sprintf("table:%s", tableName))
	defer C.free(unsafe.Pointer(uri))

	var cur *C.WT_CURSOR
	if ret := C.go_wt_open_cursor(sess, uri, &cur); ret != 0 {
		return fmt.Errorf("open_cursor failed: %d", ret)
	}
	defer C.go_wt_cursor_close(cur)

	matched := 0
	for C.go_wt_cursor_next(cur) == 0 {
		var pData unsafe.Pointer
		var sz C.size_t
		if ret := C.go_wt_cursor_get_val_raw(cur, &pData, &sz); ret == 0 && pData != nil {
			rec, err := decodeRecord(tbl, C.GoBytes(pData, C.int(sz)))
			if err == nil {
				pKey, err := tbl.ComputePartitionKey(rec)
				if err == nil && pKey == partitionName {
					C.go_wt_cursor_remove(cur)
					matched++
				}
			}
		}
	}
	if matched == 0 {
		return fmt.Errorf("%w: partition %q in table %q", storage.ErrPartitionNotFound, partitionName, tableName)
	}

	e.recordWALLocked([]memengine.WALOp{{
		Type:  memengine.OpDropPartition,
		Table: tableName,
		Key:   partitionName,
	}})
	return nil
}

func (e *Engine) GetPartitions(tableName string) ([]storage.PartitionInfo, error) {
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

	uri := C.CString(fmt.Sprintf("table:%s", tableName))
	defer C.free(unsafe.Pointer(uri))

	var cur *C.WT_CURSOR
	if ret := C.go_wt_open_cursor(sess, uri, &cur); ret != 0 {
		return nil, fmt.Errorf("open_cursor failed: %d", ret)
	}
	defer C.go_wt_cursor_close(cur)

	partCounts := make(map[string]int64)
	for C.go_wt_cursor_next(cur) == 0 {
		var pData unsafe.Pointer
		var sz C.size_t
		if ret := C.go_wt_cursor_get_val_raw(cur, &pData, &sz); ret == 0 && pData != nil {
			rec, err := decodeRecord(tbl, C.GoBytes(pData, C.int(sz)))
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

func (e *Engine) AddColumn(tableName string, col *storage.Column) error {
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

	oldSchema := *tbl
	tbl.Columns = append(tbl.Columns, *col)

	// Rewrite any existing records with the new column layout
	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret == 0 {
		defer C.go_wt_session_close(sess)
		uri := C.CString(fmt.Sprintf("table:%s", tableName))
		defer C.free(unsafe.Pointer(uri))

		var cur *C.WT_CURSOR
		if C.go_wt_open_cursor(sess, uri, &cur) == 0 {
			defer C.go_wt_cursor_close(cur)
			type rowUpdate struct {
				key  string
				data []byte
			}
			var updates []rowUpdate
			for C.go_wt_cursor_next(cur) == 0 {
				var pKey *C.char
				var pData unsafe.Pointer
				var sz C.size_t
				if C.go_wt_cursor_get_key(cur, &pKey) == 0 && C.go_wt_cursor_get_val_raw(cur, &pData, &sz) == 0 && pData != nil {
					rec, err := decodeRecord(&oldSchema, C.GoBytes(pData, C.int(sz)))
					if err == nil {
						rec[col.Name] = storage.NullValue(col.Type)
						newBytes, err := storage.EncodeTuple(tbl, rec)
						if err == nil {
							updates = append(updates, rowUpdate{key: C.GoString(pKey), data: newBytes})
						}
					}
				}
			}
			for _, u := range updates {
				cKey := C.CString(u.key)
				C.go_wt_cursor_update_raw(cur, cKey, unsafe.Pointer(&u.data[0]), C.size_t(len(u.data)))
				C.free(unsafe.Pointer(cKey))
			}
		}
	}

	if err := e.saveCatalog(); err != nil {
		return err
	}

	e.recordWALLocked([]memengine.WALOp{{
		Type:   memengine.OpAlterTable,
		Table:  tableName,
		Column: col,
	}})
	return nil
}

func (e *Engine) GetTable(tableName string) (*storage.TableSchema, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	t, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	return t, nil
}

func (e *Engine) ListTables() ([]*storage.TableSchema, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	res := make([]*storage.TableSchema, 0, len(e.tables))
	for _, t := range e.tables {
		res = append(res, t)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Name < res[j].Name
	})
	return res, nil
}

func (e *Engine) CreateIndex(index *storage.IndexSchema) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	t, exists := sqlutil.MapGetFold(e.tables, index.TableName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, index.TableName)
	}

	if _, _, exists := t.FindIndex(index.Name); exists {
		return fmt.Errorf("%w: %s", storage.ErrIndexExists, index.Name)
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
	e.mu.Lock()
	defer e.mu.Unlock()

	t, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	_, idxPos, exists := t.FindIndex(indexName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrIndexNotFound, indexName)
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
	mu         sync.Mutex
	closed     bool
	cursorPool map[string][]*C.WT_CURSOR
	allCursors map[*C.WT_CURSOR]struct{}
}

func (s *WTSession) evictCursors(tableName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.sess == nil {
		return
	}
	uri := "table:" + tableName
	if pool, ok := s.cursorPool[uri]; ok {
		for _, cur := range pool {
			delete(s.allCursors, cur)
			C.go_wt_cursor_close(cur)
		}
		delete(s.cursorPool, uri)
	}
}

func (s *WTSession) Close() error {
	s.engine.sessMu.Lock()
	delete(s.engine.sessions, s)
	s.engine.sessMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.sess == nil {
		return nil
	}
	s.closed = true

	for cur := range s.allCursors {
		C.go_wt_cursor_close(cur)
	}
	s.allCursors = nil
	s.cursorPool = nil

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
	if ret := C.go_wt_open_cursor(s.sess, cURI, &cur); ret != 0 {
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
	C.go_wt_cursor_reset(cur)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.sess == nil {
		delete(s.allCursors, cur)
		C.go_wt_cursor_close(cur)
		return
	}

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
	for cur := range s.allCursors {
		C.go_wt_cursor_reset(cur)
	}
}

func (s *WTSession) BeginTx(opts storage.TxOptions) (storage.Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, fmt.Errorf("session is closed")
	}

	ret := C.go_wt_session_begin(s.sess, nil)
	if ret != 0 {
		return nil, fmt.Errorf("begin_transaction failed with code %d", ret)
	}

	snapLSN := opts.SnapshotLSN
	if snapLSN == 0 {
		snapLSN = s.engine.CurrentLSN()
	}
	snapTS := opts.SnapshotTimestamp
	if snapTS == 0 {
		snapTS = time.Now().UnixNano()
	}

	return &WTTx{
		sess:              s,
		opts:              opts,
		snapshotLSN:       snapLSN,
		snapshotTimestamp: snapTS,
	}, nil
}

type wtUndoKind int

const (
	wtUndoDelete wtUndoKind = iota
	wtUndoPut
)

type wtUndoOp struct {
	table   string
	pk      storage.Value
	kind    wtUndoKind
	oldData []byte
}

// WTTx manages ACID mutations in WiredTiger.
type WTTx struct {
	sess              *WTSession
	opts              storage.TxOptions
	snapshotLSN       int64
	snapshotTimestamp int64
	commitLSN         int64
	commitTimestamp   int64
	mu                sync.Mutex
	closed            bool
	hasWrites         bool
	undoLog           []wtUndoOp
	savepoints        map[string]int
	spNames           []string
	walOps            []memengine.WALOp
	spWALIndices      map[string]int
	tableCache        map[string]*storage.TableSchema
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

// SnapshotLSN returns the snapshot LSN bound at transaction start.
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

	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	rec := tbl.RowToRecord(row)
	return tx.insertLocked(tableName, tbl, rec)
}

func (tx *WTTx) insertLocked(tableName string, tbl *storage.TableSchema, record storage.Record) error {
	normalized := make(storage.Record, len(record))
	for k, v := range record {
		if col, _, ok := tbl.FindColumn(k); ok {
			normalized.Set(col.Name, v)
		} else {
			normalized.Set(k, v)
		}
	}
	record = normalized

	pkCol, err := tbl.PrimaryKeyColumn()
	if err != nil {
		return err
	}

	pkVal, has := record.Get(pkCol.Name)
	if !has || pkVal.IsNull {
		return fmt.Errorf("%w: missing primary key %s", storage.ErrNotNullViolation, pkCol.Name)
	}

	if err := tx.checkForeignKeyLocked(tbl, record); err != nil {
		return err
	}
	if err := tx.checkUniqueIndexesLocked(tableName, tbl, pkVal, record); err != nil {
		return err
	}

	pkStr := pkVal.String()
	uri := "table:" + tableName
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
		return fmt.Errorf("cursor_insert failed with code %d", ret)
	}

	tx.hasWrites = true
	if len(tx.savepoints) > 0 {
		tx.undoLog = append(tx.undoLog, wtUndoOp{
			table: tableName,
			pk:    pkVal,
			kind:  wtUndoDelete,
		})
	}
	if tx.sess.engine.hasHook.Load() {
		recCopy := make([]byte, len(recBytes))
		copy(recCopy, recBytes)
		tx.walOps = append(tx.walOps, memengine.WALOp{
			Type:  memengine.OpInsert,
			Table: tableName,
			Key:   pkStr,
			Data:  recCopy,
		})
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

	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	rec := tbl.RowToRecord(row)
	return tx.updateLocked(tableName, tbl, pk, rec)
}

func (tx *WTTx) updateLocked(tableName string, tbl *storage.TableSchema, pk storage.Value, record storage.Record) error {
	oldData, err := tx.getRawBytesLocked(tableName, pk)
	if err != nil {
		return err
	}

	existing, err := decodeRecord(tbl, oldData)
	if err != nil {
		return err
	}

	for col, val := range record {
		if colSchema, _, ok := tbl.FindColumn(col); ok {
			existing.Set(colSchema.Name, val)
		} else {
			existing.Set(col, val)
		}
	}

	if err := tx.checkForeignKeyLocked(tbl, existing); err != nil {
		return err
	}
	if err := tx.checkUniqueIndexesLocked(tableName, tbl, pk, existing); err != nil {
		return err
	}

	uri := "table:" + tableName
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
		return fmt.Errorf("cursor_update failed with code %d", ret)
	}

	tx.hasWrites = true
	if len(tx.savepoints) > 0 {
		tx.undoLog = append(tx.undoLog, wtUndoOp{
			table:   tableName,
			pk:      pk,
			kind:    wtUndoPut,
			oldData: oldData,
		})
	}
	if tx.sess.engine.hasHook.Load() {
		recCopy := make([]byte, len(recBytes))
		copy(recCopy, recBytes)
		tx.walOps = append(tx.walOps, memengine.WALOp{
			Type:  memengine.OpUpdate,
			Table: tableName,
			Key:   pk.String(),
			Data:  recCopy,
		})
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

	return tx.deleteLocked(tableName, pk)
}

func (tx *WTTx) deleteLocked(tableName string, pk storage.Value) error {
	if err := tx.checkReferencingFKsOnDeleteLocked(tableName, pk); err != nil {
		return err
	}

	var oldData []byte
	if len(tx.savepoints) > 0 {
		oldData, _ = tx.getRawBytesLocked(tableName, pk)
	}

	uri := "table:" + tableName
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
		return fmt.Errorf("cursor_remove failed with code %d", ret)
	}

	tx.hasWrites = true
	if len(oldData) > 0 {
		tx.undoLog = append(tx.undoLog, wtUndoOp{
			table:   tableName,
			pk:      pk,
			kind:    wtUndoPut,
			oldData: oldData,
		})
	}
	if tx.sess.engine.hasHook.Load() {
		tx.walOps = append(tx.walOps, memengine.WALOp{
			Type:  memengine.OpDelete,
			Table: tableName,
			Key:   pk.String(),
		})
	}

	return nil
}

func (tx *WTTx) checkUniqueIndexesLocked(tableName string, tbl *storage.TableSchema, currentPK storage.Value, rec storage.Record) error {
	for _, idx := range tbl.Indexes {
		if !idx.Unique {
			continue
		}
		keyVal := storage.ExtractIndexKeyFromRecord(rec, &idx)
		if keyVal.IsNull {
			continue
		}
		cur, err := tx.scanLocked(tableName)
		if err != nil {
			return err
		}
		pkCol, _ := tbl.PrimaryKeyColumn()
		for cur.Next() {
			rowRec, rErr := cur.Record()
			if rErr != nil {
				continue
			}
			if pkCol != nil {
				if rPK, ok := rowRec.Get(pkCol.Name); ok && rPK.Equal(currentPK) {
					continue
				}
			}
			existingKey := storage.ExtractIndexKeyFromRecord(rowRec, &idx)
			if !existingKey.IsNull && existingKey.Equal(keyVal) {
				cur.Close()
				return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idx.Name)
			}
		}
		cur.Close()
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
		cPkCol, err := cTbl.PrimaryKeyColumn()
		if err != nil {
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
				_, pkIdx, _ := cTbl.FindColumn(cPkCol.Name)
				if pkIdx >= 0 && pkIdx < len(row.Values) {
					matchingPKs = append(matchingPKs, row.Values[pkIdx])
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
	return tx.getLocked(tableName, pk)
}

func (tx *WTTx) GetRow(tableName string, pk storage.Value) (storage.Row, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.getRowLocked(tableName, pk)
}

func (tx *WTTx) getRawBytesLocked(tableName string, pk storage.Value) ([]byte, error) {
	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	uri := "table:" + tableName
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

	var pData unsafe.Pointer
	var sz C.size_t
	if ret := C.go_wt_cursor_get(cur, cKey, &pData, &sz); ret != 0 || pData == nil {
		return nil, storage.ErrKeyNotFound
	}

	return C.GoBytes(pData, C.int(sz)), nil
}

func (tx *WTTx) getLocked(tableName string, pk storage.Value) (storage.Record, error) {
	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return nil, err
	}
	rawBytes, err := tx.getRawBytesLocked(tableName, pk)
	if err != nil {
		return nil, err
	}
	return decodeRecord(tbl, rawBytes)
}

func (tx *WTTx) getRowLocked(tableName string, pk storage.Value) (storage.Row, error) {
	if tx.closed {
		return storage.Row{}, storage.ErrTxClosed
	}
	tbl, err := tx.getTableLocked(tableName)
	if err != nil {
		return storage.Row{}, err
	}
	rawBytes, err := tx.getRawBytesLocked(tableName, pk)
	if err != nil {
		return storage.Row{}, err
	}
	return decodeRow(tbl, rawBytes)
}

func (tx *WTTx) Scan(tableName string) (storage.Cursor, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.scanLocked(tableName)
}

func (tx *WTTx) scanLocked(tableName string) (storage.Cursor, error) {
	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	uri := fmt.Sprintf("table:%s", tableName)
	cur, err := tx.sess.acquireCursor(uri)
	if err != nil {
		return nil, err
	}

	tbl, _ := tx.getTableLocked(tableName)
	return &WTCursor{
		sess:   tx.sess,
		uri:    uri,
		cur:    cur,
		schema: tbl,
	}, nil
}

func (tx *WTTx) ScanPartitions(tableName string, partitions []string) (storage.Cursor, error) {
	return tx.Scan(tableName)
}

func (tx *WTTx) IndexScan(tableName string, indexName string, low, high *storage.Value) (storage.Cursor, error) {
	cur, err := tx.Scan(tableName)
	if err != nil {
		return nil, err
	}
	if low == nil && high == nil {
		return cur, nil
	}
	tbl, _ := tx.getTableLocked(tableName)
	if tbl == nil {
		return cur, nil
	}
	idx, _, found := tbl.FindIndex(indexName)
	if !found || idx == nil {
		return cur, nil
	}
	return &wtFilteredIndexCursor{
		base:   cur,
		schema: tbl,
		idx:    idx,
		low:    low,
		high:   high,
	}, nil
}

func (tx *WTTx) IndexOnlyScan(tableName string, indexName string, low, high *storage.Value) (storage.Cursor, error) {
	return tx.IndexScan(tableName, indexName, low, high)
}

func (tx *WTTx) IndexScanDesc(tableName string, indexName string, low, high *storage.Value) (storage.Cursor, error) {
	return tx.IndexScan(tableName, indexName, low, high)
}

type wtFilteredIndexCursor struct {
	base    storage.Cursor
	schema  *storage.TableSchema
	idx     *storage.IndexSchema
	low     *storage.Value
	high    *storage.Value
	currRow storage.Row
	currRec storage.Record
	currRaw []byte
	hasRaw  bool
}

func (c *wtFilteredIndexCursor) Next() bool {
	for c.base.Next() {
		rawCur, ok := c.base.(storage.RawTupleCursor)
		if ok {
			c.currRaw, c.hasRaw = rawCur.RawTuple()
		}
		row, err := c.base.Row()
		if err != nil {
			rec, rerr := c.base.Record()
			if rerr != nil {
				continue
			}
			c.currRec = rec
			c.currRow = storage.Row{}
			if c.matchesRecord(rec) {
				return true
			}
			continue
		}
		c.currRow = row
		c.currRec = nil
		if c.matchesRow(row) {
			return true
		}
	}
	return false
}

func (c *wtFilteredIndexCursor) matchesRecord(rec storage.Record) bool {
	key := storage.ExtractIndexKeyFromRecord(rec, c.idx)
	if c.low != nil && key.Compare(*c.low) < 0 {
		return false
	}
	if c.high != nil && key.Compare(*c.high) > 0 {
		return false
	}
	return true
}

func (c *wtFilteredIndexCursor) matchesRow(row storage.Row) bool {
	var key storage.Value
	if c.idx.IsExpression() {
		colName := c.idx.ExprColumn
		if colName == "" && len(c.idx.Columns) > 0 {
			colName = c.idx.Columns[0]
		}
		if c.schema != nil {
			if _, colIdx, found := c.schema.FindColumn(colName); found && colIdx >= 0 && colIdx < len(row.Values) {
				docVal := row.Values[colIdx]
				if !docVal.IsNull {
					key = storage.ExtractJSONIndexKey(docVal.String(), c.idx.ExprPath)
				} else {
					key = storage.NullValue(storage.TypeVarChar)
				}
			}
		}
	} else if len(c.idx.Columns) > 0 && c.schema != nil {
		if _, colIdx, found := c.schema.FindColumn(c.idx.Columns[0]); found && colIdx >= 0 && colIdx < len(row.Values) {
			key = row.Values[colIdx]
		}
	}
	if c.low != nil && key.Compare(*c.low) < 0 {
		return false
	}
	if c.high != nil && key.Compare(*c.high) > 0 {
		return false
	}
	return true
}

func (c *wtFilteredIndexCursor) Record() (storage.Record, error) {
	if c.currRec != nil {
		return c.currRec, nil
	}
	if len(c.currRow.Values) > 0 && c.schema != nil {
		return c.schema.RowToRecord(c.currRow), nil
	}
	return c.base.Record()
}

func (c *wtFilteredIndexCursor) Row() (storage.Row, error) {
	if len(c.currRow.Values) > 0 {
		return c.currRow, nil
	}
	return c.base.Row()
}

func (c *wtFilteredIndexCursor) RawTuple() ([]byte, bool) {
	if c.hasRaw {
		return c.currRaw, true
	}
	return nil, false
}

func (c *wtFilteredIndexCursor) Close() error {
	return c.base.Close()
}

func (tx *WTTx) Commit() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	tx.closed = true
	hasWrites := tx.hasWrites
	tx.tableCache = nil
	walOps := tx.walOps
	tx.walOps = nil
	tx.spWALIndices = nil
	tx.undoLog = nil
	tx.savepoints = nil
	tx.spNames = nil

	tx.sess.resetCursors()

	ret := C.go_wt_session_commit(tx.sess.sess, nil)
	if ret != 0 {
		return fmt.Errorf("commit_transaction failed with code %d", ret)
	}

	if !hasWrites || tx.opts.ReadOnly {
		tx.commitLSN = tx.snapshotLSN
		tx.commitTimestamp = time.Now().UnixNano()
		return nil
	}

	eng := tx.sess.engine
	seq := eng.writeSeq.Add(1)
	if len(walOps) > 0 && eng.hasHook.Load() {
		rec := eng.recordWALLocked(walOps)
		tx.commitLSN = rec.LSN
		tx.commitTimestamp = rec.Timestamp
	} else {
		tx.commitLSN = eng.nextLSN.Add(1)
		tx.commitTimestamp = time.Now().UnixNano()
	}

	if eng.FsyncPolicy() == memengine.FsyncAlways {
		if err := eng.syncUpTo(tx.sess.sess, seq); err != nil {
			return err
		}
	}
	return nil
}

func (tx *WTTx) Rollback() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	tx.closed = true
	tx.tableCache = nil
	tx.walOps = nil
	tx.spWALIndices = nil
	tx.undoLog = nil
	tx.savepoints = nil
	tx.spNames = nil

	tx.sess.resetCursors()

	ret := C.go_wt_session_rollback(tx.sess.sess, nil)
	if ret != 0 {
		return fmt.Errorf("rollback_transaction failed with code %d", ret)
	}
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

	if tx.savepoints == nil {
		tx.savepoints = make(map[string]int)
	}
	if tx.spWALIndices == nil {
		tx.spWALIndices = make(map[string]int)
	}

	found := false
	for _, n := range tx.spNames {
		if n == normName {
			found = true
			break
		}
	}
	if !found {
		tx.spNames = append(tx.spNames, normName)
	}
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

	// Undo operations in reverse order down to targetLen
	for i := len(tx.undoLog) - 1; i >= targetLen; i-- {
		op := tx.undoLog[i]
		switch op.kind {
		case wtUndoDelete:
			_ = tx.removeRawLocked(op.table, op.pk)
		case wtUndoPut:
			if len(op.oldData) > 0 {
				_ = tx.putRawBytesLocked(op.table, op.pk, op.oldData)
			}
		}
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
		}
		tx.spNames = tx.spNames[:idx+1]
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
		}
		tx.spNames = tx.spNames[:idx]
	}

	return nil
}

func (tx *WTTx) putRawBytesLocked(tableName string, pk storage.Value, recBytes []byte) error {
	uri := "table:" + tableName
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
			return fmt.Errorf("cursor put failed with code %d", ret)
		}
	}
	return nil
}

func (tx *WTTx) putLocked(tableName string, pk storage.Value, record storage.Record) error {
	tbl, _ := tx.sess.engine.GetTable(tableName)
	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)

	recBytes, err := serializeRecordToBuffer(nil, tbl, record, *bufPtr)
	if err != nil {
		return err
	}

	return tx.putRawBytesLocked(tableName, pk, recBytes)
}

func (tx *WTTx) removeRawLocked(tableName string, pk storage.Value) error {
	uri := "table:" + tableName
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

	_ = C.go_wt_cursor_remove_key(cur, cKey)
	return nil
}

// WTCursor iterates over WiredTiger table records.
type WTCursor struct {
	sess   *WTSession
	uri    string
	cur    *C.WT_CURSOR
	schema *storage.TableSchema
	closed bool
	eof    bool
}

func (c *WTCursor) Next() bool {
	if c.closed || c.cur == nil || c.eof {
		return false
	}
	if C.go_wt_cursor_next(c.cur) != 0 {
		c.eof = true
		return false
	}
	return true
}

func (c *WTCursor) Record() (storage.Record, error) {
	if c.closed || c.cur == nil || c.eof {
		return nil, fmt.Errorf("cursor closed")
	}

	var pData unsafe.Pointer
	var sz C.size_t
	if ret := C.go_wt_cursor_get_val_raw(c.cur, &pData, &sz); ret != 0 || pData == nil {
		return nil, fmt.Errorf("cursor_get_val_raw failed: %d", ret)
	}

	return decodeRecord(c.schema, C.GoBytes(pData, C.int(sz)))
}

func (c *WTCursor) Row() (storage.Row, error) {
	if c.closed || c.cur == nil || c.eof {
		return storage.Row{}, fmt.Errorf("cursor closed")
	}

	var pData unsafe.Pointer
	var sz C.size_t
	if ret := C.go_wt_cursor_get_val_raw(c.cur, &pData, &sz); ret != 0 || pData == nil {
		return storage.Row{}, fmt.Errorf("cursor_get_val_raw failed: %d", ret)
	}

	return decodeRow(c.schema, C.GoBytes(pData, C.int(sz)))
}

func (c *WTCursor) RawTuple() ([]byte, bool) {
	if c.closed || c.cur == nil || c.eof {
		return nil, false
	}

	var pData unsafe.Pointer
	var sz C.size_t
	if ret := C.go_wt_cursor_get_val_raw(c.cur, &pData, &sz); ret != 0 || pData == nil {
		return nil, false
	}

	return C.GoBytes(pData, C.int(sz)), true
}

func decodeRecord(schema *storage.TableSchema, data []byte) (storage.Record, error) {
	if len(data) > 0 && data[0] == storage.TupleCodecVersion && schema != nil {
		return storage.DecodeTuple(schema, data)
	}
	return nil, fmt.Errorf("invalid binary tuple record bytes")
}

func decodeRow(schema *storage.TableSchema, data []byte) (storage.Row, error) {
	if len(data) > 0 && data[0] == storage.TupleCodecVersion && schema != nil {
		return storage.DecodeTupleToRow(schema, data)
	}
	return storage.Row{}, fmt.Errorf("invalid binary tuple record bytes")
}

func (c *WTCursor) Close() error {
	if c.closed || c.cur == nil {
		return nil
	}
	c.closed = true
	cur := c.cur
	c.cur = nil
	if c.sess != nil && c.uri != "" {
		c.sess.releaseCursor(c.uri, cur)
	} else {
		C.go_wt_cursor_close(cur)
	}
	return nil
}
