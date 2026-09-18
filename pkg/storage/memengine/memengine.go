package memengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"sqltiger/pkg/storage"
)

// TableData stores rows in memory for a single table.
type TableData struct {
	Schema storage.TableSchema        `json:"schema"`
	Rows   map[string]storage.Record `json:"rows"`
	Keys   []string                  `json:"keys"`
}

// Engine implements storage.Engine in pure Go with optional atomic disk snapshot persistence.
type Engine struct {
	mu      sync.RWMutex
	dataDir string
	tables  map[string]*TableData
	closed  bool
}

// New creates a new in-memory storage engine.
func New() *Engine {
	return &Engine{
		tables: make(map[string]*TableData),
	}
}

func (e *Engine) Open(dir string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.closed = false
	if dir != "" {
		e.dataDir = filepath.Clean(dir)
		if err := os.MkdirAll(e.dataDir, 0755); err != nil {
			return fmt.Errorf("failed to create data directory: %w", err)
		}
		e.loadDiskLocked()
	}
	return nil
}

func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}
	e.closed = true
	if e.dataDir != "" {
		_ = e.saveDiskLocked()
	}
	return nil
}

func (e *Engine) saveDiskLocked() error {
	if e.dataDir == "" {
		return nil
	}
	data, err := json.MarshalIndent(e.tables, "", "  ")
	if err != nil {
		return err
	}

	tmpFile := filepath.Join(e.dataDir, "tables.json.tmp")
	targetFile := filepath.Join(e.dataDir, "tables.json")

	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpFile, targetFile)
}

func (e *Engine) loadDiskLocked() {
	if e.dataDir == "" {
		return
	}
	targetFile := filepath.Join(e.dataDir, "tables.json")
	data, err := os.ReadFile(targetFile)
	if err != nil {
		return
	}

	var loaded map[string]*TableData
	if err := json.Unmarshal(data, &loaded); err == nil && loaded != nil {
		e.tables = loaded
	}
}

func (e *Engine) CreateTable(schema *storage.TableSchema) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.tables[schema.Name]; exists {
		return fmt.Errorf("%w: %s", storage.ErrTableExists, schema.Name)
	}

	// Validate that primary key exists
	if _, err := schema.PrimaryKeyColumn(); err != nil {
		return err
	}

	e.tables[schema.Name] = &TableData{
		Schema: *schema,
		Rows:   make(map[string]storage.Record),
		Keys:   make([]string, 0),
	}
	if e.dataDir != "" {
		_ = e.saveDiskLocked()
	}
	return nil
}

func (e *Engine) DropTable(tableName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.tables[tableName]; !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	delete(e.tables, tableName)
	if e.dataDir != "" {
		_ = e.saveDiskLocked()
	}
	return nil
}

func (e *Engine) GetTable(tableName string) (*storage.TableSchema, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	td, exists := e.tables[tableName]
	if !exists {
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	cp := td.Schema
	return &cp, nil
}

func (e *Engine) ListTables() ([]*storage.TableSchema, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*storage.TableSchema, 0, len(e.tables))
	for _, td := range e.tables {
		cp := td.Schema
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (e *Engine) NewSession() (storage.Session, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return nil, fmt.Errorf("engine is closed")
	}
	return &Session{engine: e}, nil
}

// Session represents a client session.
type Session struct {
	engine *Engine
}

func (s *Session) BeginTx(opts storage.TxOptions) (storage.Transaction, error) {
	s.engine.mu.RLock()
	defer s.engine.mu.RUnlock()

	if s.engine.closed {
		return nil, fmt.Errorf("engine is closed")
	}

	// Create snapshot of tables for transaction isolation
	snapshot := make(map[string]map[string]storage.Record)
	for tName, td := range s.engine.tables {
		tableCopy := make(map[string]storage.Record, len(td.Rows))
		for k, v := range td.Rows {
			recCopy := make(storage.Record, len(v))
			for col, val := range v {
				recCopy[col] = val
			}
			tableCopy[k] = recCopy
		}
		snapshot[tName] = tableCopy
	}

	return &Transaction{
		engine:   s.engine,
		opts:     opts,
		snapshot: snapshot,
	}, nil
}

func (s *Session) Close() error {
	return nil
}

type changeType int

const (
	changeInsert changeType = iota
	changeUpdate
	changeDelete
)

type rowChange struct {
	kind  changeType
	table string
	key   string
	rec   storage.Record
}

// Transaction handles ACID mutations with rollback/commit support.
type Transaction struct {
	mu       sync.Mutex
	engine   *Engine
	opts     storage.TxOptions
	snapshot map[string]map[string]storage.Record
	changes  []rowChange
	closed   bool
}

func (tx *Transaction) Insert(tableName string, record storage.Record) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return fmt.Errorf("cannot insert in read-only transaction")
	}

	tx.engine.mu.RLock()
	td, exists := tx.engine.tables[tableName]
	if !exists {
		tx.engine.mu.RUnlock()
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	schema := td.Schema
	tx.engine.mu.RUnlock()

	pkCol, err := schema.PrimaryKeyColumn()
	if err != nil {
		return err
	}

	pkVal, hasPk := record[pkCol.Name]
	if !hasPk || pkVal.IsNull {
		return fmt.Errorf("%w: missing primary key %q", storage.ErrNotNullViolation, pkCol.Name)
	}

	pkKey := pkVal.String()
	tableRows, ok := tx.snapshot[tableName]
	if !ok {
		tableRows = make(map[string]storage.Record)
		tx.snapshot[tableName] = tableRows
	}

	if _, exists := tableRows[pkKey]; exists {
		return fmt.Errorf("%w: key %q in table %q", storage.ErrKeyExists, pkKey, tableName)
	}

	// Validate columns and types
	for _, col := range schema.Columns {
		val, found := record[col.Name]
		if !found || val.IsNull {
			if !col.Nullable && !col.PrimaryKey {
				return fmt.Errorf("%w: column %q", storage.ErrNotNullViolation, col.Name)
			}
			continue
		}
		if val.Type != col.Type {
			return fmt.Errorf("%w: column %q expects %s, got %s",
				storage.ErrInvalidDataType, col.Name, col.Type, val.Type)
		}
	}

	recCopy := make(storage.Record, len(record))
	for k, v := range record {
		recCopy[k] = v
	}

	tableRows[pkKey] = recCopy
	tx.changes = append(tx.changes, rowChange{
		kind:  changeInsert,
		table: tableName,
		key:   pkKey,
		rec:   recCopy,
	})

	return nil
}

func (tx *Transaction) Update(tableName string, pk storage.Value, record storage.Record) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return fmt.Errorf("cannot update in read-only transaction")
	}

	tableRows, ok := tx.snapshot[tableName]
	if !ok {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	pkKey := pk.String()
	existing, exists := tableRows[pkKey]
	if !exists {
		return fmt.Errorf("%w: key %q in table %q", storage.ErrKeyNotFound, pkKey, tableName)
	}

	// Merge updates into existing record
	for col, val := range record {
		existing[col] = val
	}

	tx.changes = append(tx.changes, rowChange{
		kind:  changeUpdate,
		table: tableName,
		key:   pkKey,
		rec:   existing,
	})

	return nil
}

func (tx *Transaction) Delete(tableName string, pk storage.Value) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return fmt.Errorf("cannot delete in read-only transaction")
	}

	tableRows, ok := tx.snapshot[tableName]
	if !ok {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	pkKey := pk.String()
	if _, exists := tableRows[pkKey]; !exists {
		return fmt.Errorf("%w: key %q in table %q", storage.ErrKeyNotFound, pkKey, tableName)
	}

	delete(tableRows, pkKey)
	tx.changes = append(tx.changes, rowChange{
		kind:  changeDelete,
		table: tableName,
		key:   pkKey,
	})

	return nil
}

func (tx *Transaction) Get(tableName string, pk storage.Value) (storage.Record, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	tableRows, ok := tx.snapshot[tableName]
	if !ok {
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	pkKey := pk.String()
	rec, exists := tableRows[pkKey]
	if !exists {
		return nil, fmt.Errorf("%w: key %q in table %q", storage.ErrKeyNotFound, pkKey, tableName)
	}

	res := make(storage.Record, len(rec))
	for k, v := range rec {
		res[k] = v
	}
	return res, nil
}

func (tx *Transaction) Scan(tableName string) (storage.Cursor, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	tableRows, ok := tx.snapshot[tableName]
	if !ok {
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	keys := make([]string, 0, len(tableRows))
	for k := range tableRows {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	records := make([]storage.Record, len(keys))
	for i, k := range keys {
		records[i] = tableRows[k]
	}

	return &memCursor{
		records: records,
		idx:     -1,
	}, nil
}

func (tx *Transaction) Commit() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	tx.closed = true

	if tx.opts.ReadOnly || len(tx.changes) == 0 {
		return nil
	}

	// Apply changes to master tables under engine lock
	tx.engine.mu.Lock()
	defer tx.engine.mu.Unlock()

	for _, ch := range tx.changes {
		td, exists := tx.engine.tables[ch.table]
		if !exists {
			continue
		}
		switch ch.kind {
		case changeInsert, changeUpdate:
			td.Rows[ch.key] = ch.rec
		case changeDelete:
			delete(td.Rows, ch.key)
		}
	}

	if tx.engine.dataDir != "" {
		_ = tx.engine.saveDiskLocked()
	}

	return nil
}

func (tx *Transaction) Rollback() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	tx.closed = true
	tx.changes = nil
	tx.snapshot = nil
	return nil
}

type memCursor struct {
	records []storage.Record
	idx     int
}

func (c *memCursor) Next() bool {
	c.idx++
	return c.idx < len(c.records)
}

func (c *memCursor) Record() (storage.Record, error) {
	if c.idx < 0 || c.idx >= len(c.records) {
		return nil, fmt.Errorf("cursor out of range")
	}
	return c.records[c.idx], nil
}

func (c *memCursor) Close() error {
	c.records = nil
	return nil
}
