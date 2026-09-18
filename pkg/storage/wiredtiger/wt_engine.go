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

static int go_wt_session_begin(WT_SESSION *session, const char *config) {
    return session->begin_transaction(session, config);
}

static int go_wt_session_commit(WT_SESSION *session, const char *config) {
    return session->commit_transaction(session, config);
}

static int go_wt_session_rollback(WT_SESSION *session, const char *config) {
    return session->rollback_transaction(session, config);
}

static int go_wt_open_cursor(WT_SESSION *session, const char *uri, WT_CURSOR **cursorp) {
    return session->open_cursor(session, uri, NULL, NULL, cursorp);
}

static int go_wt_cursor_close(WT_CURSOR *cursor) {
    return cursor->close(cursor);
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

static int go_wt_cursor_get_key(WT_CURSOR *cursor, const char **keyp) {
    return cursor->get_key(cursor, keyp);
}

static int go_wt_cursor_get_val(WT_CURSOR *cursor, const char **valp) {
    return cursor->get_value(cursor, valp);
}
*/
import "C"
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"sqlion/pkg/storage"
)

// Engine implements storage.Engine via the WiredTiger C API.
type Engine struct {
	mu      sync.RWMutex
	conn    *C.WT_CONNECTION
	homeDir string
	tables  map[string]*storage.TableSchema
	closed  bool
}

// New creates a new WiredTiger storage engine instance.
func New() *Engine {
	return &Engine{
		tables: make(map[string]*storage.TableSchema),
	}
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

	cConfig := C.CString("create,cache_size=100M,log=(enabled=true)")
	defer C.free(unsafe.Pointer(cConfig))

	var conn *C.WT_CONNECTION
	ret := C.go_wt_open(cHome, cConfig, &conn)
	if ret != 0 {
		return fmt.Errorf("wiredtiger_open failed with code %d: %s", ret, C.GoString(C.wiredtiger_strerror(ret)))
	}

	e.conn = conn
	e.closed = false

	e.loadCatalog()
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
	defer e.mu.Unlock()

	if e.closed || e.conn == nil {
		return nil
	}
	e.closed = true
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

	config := C.CString("key_format=S,value_format=S")
	defer C.free(unsafe.Pointer(config))

	ret := C.go_wt_session_create(sess, uri, config)
	if ret != 0 {
		return fmt.Errorf("session->create failed with code %d", ret)
	}

	e.tables[schema.Name] = schema
	return e.saveCatalog()
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
	return e.saveCatalog()
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
	return res, nil
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
	engine *Engine
	sess   *C.WT_SESSION
	mu     sync.Mutex
	closed bool
}

func (s *WTSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.sess == nil {
		return nil
	}
	s.closed = true
	ret := C.go_wt_session_close(s.sess)
	s.sess = nil
	if ret != 0 {
		return fmt.Errorf("session_close failed: code %d", ret)
	}
	return nil
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

	return &WTTx{
		sess: s,
		opts: opts,
	}, nil
}

// WTTx manages ACID mutations in WiredTiger.
type WTTx struct {
	sess   *WTSession
	opts   storage.TxOptions
	mu     sync.Mutex
	closed bool
}

func (tx *WTTx) Insert(tableName string, record storage.Record) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}

	tbl, err := tx.sess.engine.GetTable(tableName)
	if err != nil {
		return err
	}

	pkCol, err := tbl.PrimaryKeyColumn()
	if err != nil {
		return err
	}

	pkVal, has := record[pkCol.Name]
	if !has || pkVal.IsNull {
		return fmt.Errorf("%w: missing primary key %s", storage.ErrNotNullViolation, pkCol.Name)
	}

	pkStr := pkVal.String()
	recJSON, err := json.Marshal(record)
	if err != nil {
		return err
	}

	uri := C.CString(fmt.Sprintf("table:%s", tableName))
	defer C.free(unsafe.Pointer(uri))

	var cur *C.WT_CURSOR
	if ret := C.go_wt_open_cursor(tx.sess.sess, uri, &cur); ret != 0 {
		return fmt.Errorf("open_cursor failed: %d", ret)
	}
	defer C.go_wt_cursor_close(cur)

	cKey := C.CString(pkStr)
	defer C.free(unsafe.Pointer(cKey))
	cVal := C.CString(string(recJSON))
	defer C.free(unsafe.Pointer(cVal))

	C.go_wt_cursor_set_key(cur, cKey)
	C.go_wt_cursor_set_val(cur, cVal)

	if ret := C.go_wt_cursor_insert(cur); ret != 0 {
		return fmt.Errorf("cursor_insert failed with code %d", ret)
	}

	return nil
}

func (tx *WTTx) Update(tableName string, pk storage.Value, record storage.Record) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}

	existing, err := tx.getLocked(tableName, pk)
	if err != nil {
		return err
	}

	for col, val := range record {
		existing[col] = val
	}

	recJSON, err := json.Marshal(existing)
	if err != nil {
		return err
	}

	uri := C.CString(fmt.Sprintf("table:%s", tableName))
	defer C.free(unsafe.Pointer(uri))

	var cur *C.WT_CURSOR
	if ret := C.go_wt_open_cursor(tx.sess.sess, uri, &cur); ret != 0 {
		return fmt.Errorf("open_cursor failed: %d", ret)
	}
	defer C.go_wt_cursor_close(cur)

	cKey := C.CString(pk.String())
	defer C.free(unsafe.Pointer(cKey))
	cVal := C.CString(string(recJSON))
	defer C.free(unsafe.Pointer(cVal))

	C.go_wt_cursor_set_key(cur, cKey)
	C.go_wt_cursor_set_val(cur, cVal)

	if ret := C.go_wt_cursor_update(cur); ret != 0 {
		return fmt.Errorf("cursor_update failed with code %d", ret)
	}

	return nil
}

func (tx *WTTx) Delete(tableName string, pk storage.Value) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}

	uri := C.CString(fmt.Sprintf("table:%s", tableName))
	defer C.free(unsafe.Pointer(uri))

	var cur *C.WT_CURSOR
	if ret := C.go_wt_open_cursor(tx.sess.sess, uri, &cur); ret != 0 {
		return fmt.Errorf("open_cursor failed: %d", ret)
	}
	defer C.go_wt_cursor_close(cur)

	cKey := C.CString(pk.String())
	defer C.free(unsafe.Pointer(cKey))

	C.go_wt_cursor_set_key(cur, cKey)
	if ret := C.go_wt_cursor_remove(cur); ret != 0 {
		return fmt.Errorf("cursor_remove failed with code %d", ret)
	}

	return nil
}

func (tx *WTTx) Get(tableName string, pk storage.Value) (storage.Record, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.getLocked(tableName, pk)
}

func (tx *WTTx) getLocked(tableName string, pk storage.Value) (storage.Record, error) {
	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	uri := C.CString(fmt.Sprintf("table:%s", tableName))
	defer C.free(unsafe.Pointer(uri))

	var cur *C.WT_CURSOR
	if ret := C.go_wt_open_cursor(tx.sess.sess, uri, &cur); ret != 0 {
		return nil, fmt.Errorf("open_cursor failed: %d", ret)
	}
	defer C.go_wt_cursor_close(cur)

	cKey := C.CString(pk.String())
	defer C.free(unsafe.Pointer(cKey))

	C.go_wt_cursor_set_key(cur, cKey)
	if ret := C.go_wt_cursor_search(cur); ret != 0 {
		return nil, storage.ErrKeyNotFound
	}

	var cVal *C.char
	if ret := C.go_wt_cursor_get_val(cur, &cVal); ret != 0 || cVal == nil {
		return nil, fmt.Errorf("cursor_get_value failed")
	}

	var rec storage.Record
	if err := json.Unmarshal([]byte(C.GoString(cVal)), &rec); err != nil {
		return nil, fmt.Errorf("record unmarshal error: %w", err)
	}

	return rec, nil
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

	return &WTCursor{
		cur: cur,
	}, nil
}

func (tx *WTTx) Commit() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	tx.closed = true

	ret := C.go_wt_session_commit(tx.sess.sess, nil)
	if ret != 0 {
		return fmt.Errorf("commit_transaction failed with code %d", ret)
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

	ret := C.go_wt_session_rollback(tx.sess.sess, nil)
	if ret != 0 {
		return fmt.Errorf("rollback_transaction failed with code %d", ret)
	}
	return nil
}

// WTCursor iterates over WiredTiger table records.
type WTCursor struct {
	cur    *C.WT_CURSOR
	closed bool
}

func (c *WTCursor) Next() bool {
	if c.closed || c.cur == nil {
		return false
	}
	return C.go_wt_cursor_next(c.cur) == 0
}

func (c *WTCursor) Record() (storage.Record, error) {
	if c.closed || c.cur == nil {
		return nil, fmt.Errorf("cursor closed")
	}

	var cVal *C.char
	if ret := C.go_wt_cursor_get_val(c.cur, &cVal); ret != 0 || cVal == nil {
		return nil, fmt.Errorf("cursor_get_val failed: %d", ret)
	}

	var rec storage.Record
	if err := json.Unmarshal([]byte(C.GoString(cVal)), &rec); err != nil {
		return nil, err
	}
	return rec, nil
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
