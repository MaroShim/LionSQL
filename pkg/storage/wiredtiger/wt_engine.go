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

	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
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
}

// New creates a new WiredTiger storage engine instance.
func New() *Engine {
	e := &Engine{
		tables: make(map[string]*storage.TableSchema),
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

	e.syncMu.Lock()
	defer e.syncMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()

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

	if _, exists := e.tables[schema.Name]; exists {
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

	if _, exists := e.tables[tableName]; !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return fmt.Errorf("open_session failed: code %d", ret)
	}
	defer C.go_wt_session_close(sess)

	uri := C.CString(fmt.Sprintf("table:%s", tableName))
	defer C.free(unsafe.Pointer(uri))

	C.go_wt_session_drop(sess, uri)

	delete(e.tables, tableName)
	if err := e.saveCatalog(); err != nil {
		return err
	}
	e.recordWALLocked([]memengine.WALOp{{
		Type:  memengine.OpDropTable,
		Table: tableName,
	}})
	return nil
}

func (e *Engine) TruncateTable(tableName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	schema, exists := e.tables[tableName]
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

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

	for _, idx := range schema.Indexes {
		idxURI := C.CString(fmt.Sprintf("index:%s:%s", tableName, idx.Name))
		_ = C.go_wt_session_truncate(sess, idxURI)
		C.free(unsafe.Pointer(idxURI))
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

	tbl, exists := e.tables[tableName]
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

	tbl, exists := e.tables[tableName]
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

func (e *Engine) GetTable(tableName string) (*storage.TableSchema, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	t, exists := e.tables[tableName]
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

	t, exists := e.tables[index.TableName]
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
		Table:       index.TableName,
		IndexSchema: index,
	}})
	return nil
}

func (e *Engine) DropIndex(tableName, indexName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	t, exists := e.tables[tableName]
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

	if e.closed || e.conn == nil {
		return nil, fmt.Errorf("engine is closed")
	}

	var sess *C.WT_SESSION
	if ret := C.go_wt_open_session(e.conn, &sess); ret != 0 {
		return nil, fmt.Errorf("open_session failed with code %d", ret)
	}

	return &WTSession{
		engine: e,
		sess:   sess,
	}, nil
}

// WTSession represents an active client connection to WiredTiger.
type WTSession struct {
	engine  *Engine
	sess    *C.WT_SESSION
	mu      sync.Mutex
	closed  bool
	cursors map[string]*C.WT_CURSOR
}

func (s *WTSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.sess == nil {
		return nil
	}
	s.closed = true

	for _, cur := range s.cursors {
		C.go_wt_cursor_close(cur)
	}
	s.cursors = nil

	ret := C.go_wt_session_close(s.sess)
	s.sess = nil
	if ret != 0 {
		return fmt.Errorf("session_close failed: code %d", ret)
	}
	return nil
}

func (s *WTSession) getCursor(tableName string) (*C.WT_CURSOR, error) {
	if s.closed || s.sess == nil {
		return nil, fmt.Errorf("session is closed")
	}
	uri := "table:" + tableName
	if cur, ok := s.cursors[uri]; ok {
		C.go_wt_cursor_reset(cur)
		return cur, nil
	}

	cURI := C.CString(uri)
	defer C.free(unsafe.Pointer(cURI))

	var cur *C.WT_CURSOR
	if ret := C.go_wt_open_cursor(s.sess, cURI, &cur); ret != 0 {
		return nil, fmt.Errorf("open_cursor failed: %d", ret)
	}

	if s.cursors == nil {
		s.cursors = make(map[string]*C.WT_CURSOR)
	}
	s.cursors[uri] = cur
	return cur, nil
}

func (s *WTSession) resetCursors() {
	for _, cur := range s.cursors {
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
	pkCol, err := tbl.PrimaryKeyColumn()
	if err != nil {
		return err
	}

	pkVal, has := record[pkCol.Name]
	if !has || pkVal.IsNull {
		return fmt.Errorf("%w: missing primary key %s", storage.ErrNotNullViolation, pkCol.Name)
	}

	pkStr := pkVal.String()
	cur, err := tx.sess.getCursor(tableName)
	if err != nil {
		return err
	}
	defer C.go_wt_cursor_reset(cur)

	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)

	recBytes, err := serializeRecordToBuffer(cur, tbl, record, *bufPtr)
	if err != nil {
		return err
	}

	cKey := C.CString(pkStr)
	defer C.free(unsafe.Pointer(cKey))

	C.go_wt_cursor_set_key(cur, cKey)
	var pData unsafe.Pointer
	if len(recBytes) > 0 {
		pData = unsafe.Pointer(&recBytes[0])
	}
	C.go_wt_cursor_set_val_raw(cur, pData, C.size_t(len(recBytes)))

	if ret := C.go_wt_cursor_insert(cur); ret != 0 {
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
		existing[col] = val
	}

	cur, err := tx.sess.getCursor(tableName)
	if err != nil {
		return err
	}
	defer C.go_wt_cursor_reset(cur)

	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)

	recBytes, err := serializeRecordToBuffer(cur, tbl, existing, *bufPtr)
	if err != nil {
		return err
	}

	cKey := C.CString(pk.String())
	defer C.free(unsafe.Pointer(cKey))

	C.go_wt_cursor_set_key(cur, cKey)
	var pData unsafe.Pointer
	if len(recBytes) > 0 {
		pData = unsafe.Pointer(&recBytes[0])
	}
	C.go_wt_cursor_set_val_raw(cur, pData, C.size_t(len(recBytes)))

	if ret := C.go_wt_cursor_update(cur); ret != 0 {
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

	var oldData []byte
	if len(tx.savepoints) > 0 {
		oldData, _ = tx.getRawBytesLocked(tableName, pk)
	}

	cur, err := tx.sess.getCursor(tableName)
	if err != nil {
		return err
	}
	defer C.go_wt_cursor_reset(cur)

	cKey := C.CString(pk.String())
	defer C.free(unsafe.Pointer(cKey))

	C.go_wt_cursor_set_key(cur, cKey)
	if ret := C.go_wt_cursor_remove(cur); ret != 0 {
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

	cur, err := tx.sess.getCursor(tableName)
	if err != nil {
		return nil, err
	}
	defer C.go_wt_cursor_reset(cur)

	cKey := C.CString(pk.String())
	defer C.free(unsafe.Pointer(cKey))

	C.go_wt_cursor_set_key(cur, cKey)
	if ret := C.go_wt_cursor_search(cur); ret != 0 {
		return nil, storage.ErrKeyNotFound
	}

	var pData unsafe.Pointer
	var sz C.size_t
	if ret := C.go_wt_cursor_get_val_raw(cur, &pData, &sz); ret != 0 || pData == nil {
		return nil, fmt.Errorf("cursor_get_value failed")
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

	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	uri := C.CString(fmt.Sprintf("table:%s", tableName))
	defer C.free(unsafe.Pointer(uri))

	var cur *C.WT_CURSOR
	if ret := C.go_wt_open_cursor(tx.sess.sess, uri, &cur); ret != 0 {
		return nil, fmt.Errorf("open_cursor failed: %d", ret)
	}

	tbl, _ := tx.getTableLocked(tableName)
	return &WTCursor{
		cur:    cur,
		schema: tbl,
	}, nil
}

func (tx *WTTx) ScanPartitions(tableName string, partitions []string) (storage.Cursor, error) {
	return tx.Scan(tableName)
}

func (tx *WTTx) IndexScan(tableName string, indexName string, low, high *storage.Value) (storage.Cursor, error) {
	return tx.Scan(tableName)
}

func (tx *WTTx) IndexOnlyScan(tableName string, indexName string, low, high *storage.Value) (storage.Cursor, error) {
	return tx.IndexScan(tableName, indexName, low, high)
}

func (tx *WTTx) IndexScanDesc(tableName string, indexName string, low, high *storage.Value) (storage.Cursor, error) {
	return tx.IndexScan(tableName, indexName, low, high)
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
			_ = tx.deleteLocked(op.table, op.pk)
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
	cur, err := tx.sess.getCursor(tableName)
	if err != nil {
		return err
	}
	defer C.go_wt_cursor_reset(cur)

	cKey := C.CString(pk.String())
	defer C.free(unsafe.Pointer(cKey))

	C.go_wt_cursor_set_key(cur, cKey)
	var pData unsafe.Pointer
	if len(recBytes) > 0 {
		pData = unsafe.Pointer(&recBytes[0])
	}
	C.go_wt_cursor_set_val_raw(cur, pData, C.size_t(len(recBytes)))

	if ret := C.go_wt_cursor_update(cur); ret != 0 {
		if ret := C.go_wt_cursor_insert(cur); ret != 0 {
			return fmt.Errorf("cursor put failed with code %d", ret)
		}
	}
	return nil
}

func (tx *WTTx) putLocked(tableName string, pk storage.Value, record storage.Record) error {
	cur, err := tx.sess.getCursor(tableName)
	if err != nil {
		return err
	}
	defer C.go_wt_cursor_reset(cur)

	tbl, _ := tx.sess.engine.GetTable(tableName)
	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)

	recBytes, err := serializeRecordToBuffer(cur, tbl, record, *bufPtr)
	if err != nil {
		return err
	}

	return tx.putRawBytesLocked(tableName, pk, recBytes)
}

func (tx *WTTx) deleteLocked(tableName string, pk storage.Value) error {
	cur, err := tx.sess.getCursor(tableName)
	if err != nil {
		return err
	}
	defer C.go_wt_cursor_reset(cur)

	cKey := C.CString(pk.String())
	defer C.free(unsafe.Pointer(cKey))

	C.go_wt_cursor_set_key(cur, cKey)
	_ = C.go_wt_cursor_remove(cur)
	return nil
}

// WTCursor iterates over WiredTiger table records.
type WTCursor struct {
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
	C.go_wt_cursor_close(c.cur)
	c.cur = nil
	return nil
}
