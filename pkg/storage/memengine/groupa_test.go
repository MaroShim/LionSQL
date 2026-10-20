package memengine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"lionsql/pkg/storage"
)

// TestGroupA_BUG_MEM_SHARD_INIT_RACE_1 tests concurrent reads on an uninitialized TableData
// to ensure sync.Once prevents map concurrent read/write races.
func TestGroupA_BUG_MEM_SHARD_INIT_RACE_1(t *testing.T) {
	td := &TableData{
		Schema: storage.TableSchema{
			Name: "test_race",
			Columns: []storage.Column{
				{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			},
		},
		Rows:       make(map[string]storage.Row),
		Versions:   make(map[string]int64),
		Indexes:    make(map[string]*IndexData),
		Partitions: make(map[string]map[string]struct{}),
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, _, _ = td.getRowAndVer(fmt.Sprintf("%d", id))
			_ = td.hasKey(fmt.Sprintf("%d", id))
			_ = td.rowCount()
		}(i)
	}
	wg.Wait()
}

// TestGroupA_BUG_MEM_SKIPLIST_UNIQUE_INCLUDE_COPY_1 tests that updating an existing unique index entry
// properly updates IncludeValues directly in the slice without losing updates due to value-copying.
func TestGroupA_BUG_MEM_SKIPLIST_UNIQUE_INCLUDE_COPY_1(t *testing.T) {
	sl := NewSkipList(true) // unique index

	key := storage.StringValue("alice")
	inc1 := []storage.Value{storage.StringValue("v1")}
	inc2 := []storage.Value{storage.StringValue("v2")}

	// 1. Initial insert
	if err := sl.Insert(key, "pk1", inc1); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// 2. Re-index / update include values for the same key and PK
	if err := sl.Insert(key, "pk1", inc2); err != nil {
		t.Fatalf("re-index Insert failed: %v", err)
	}

	// 3. Scan and verify IncludeValues is inc2 ("v2")
	entries := sl.ScanRangeEntries(&key, &key)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if len(entries[0].IncludeValues) == 0 || entries[0].IncludeValues[0].SVal != "v2" {
		t.Fatalf("expected updated IncludeValues 'v2', got: %+v", entries[0].IncludeValues)
	}
}

// TestGroupA_BUG_OCC_COMMIT_INTRA_TX_UNIQUE_1 tests that committing multiple rows with conflicting
// unique index keys in the same transaction returns an ErrUniqueViolation rather than corrupting data.
func TestGroupA_BUG_OCC_COMMIT_INTRA_TX_UNIQUE_1(t *testing.T) {
	eng := New()
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "ix_email", TableName: "users", Columns: []string{"email"}, Unique: true},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	// Simulate two distinct inserts staged into tx.changes within the same transaction that have colliding unique keys
	row1 := schema.RecordToRow(storage.Record{"id": storage.IntValue(1), "email": storage.StringValue("same@domain.com")})
	row2 := schema.RecordToRow(storage.Record{"id": storage.IntValue(2), "email": storage.StringValue("same@domain.com")})

	memTx := tx.(*Transaction)
	memTx.changes["users:1"] = rowChange{
		kind:  changeInsert,
		table: "users",
		key:   "1",
		row:   row1,
	}
	memTx.changes["users:2"] = rowChange{
		kind:  changeInsert,
		table: "users",
		key:   "2",
		row:   row2,
	}

	// Commit must fail with ErrUniqueViolation
	err = tx.Commit()
	if err == nil {
		t.Fatalf("expected unique violation error on Commit(), got nil")
	}
	if !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("expected ErrUniqueViolation, got: %v", err)
	}
}

// TestGroupA_BUG_OCC_UPDATE_UNIQUE_CHECK_OMISSION_1 tests that updateLocked enforces unique constraints
// against both committed table rows and uncommitted transaction-local rows.
func TestGroupA_BUG_OCC_UPDATE_UNIQUE_CHECK_OMISSION_1(t *testing.T) {
	eng := New()
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "members",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "ix_code", TableName: "members", Columns: []string{"code"}, Unique: true},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// Commit two distinct rows
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 1 failed: %v", err)
	}
	_ = tx1.Insert("members", storage.Record{"id": storage.IntValue(1), "code": storage.StringValue("CODE_A")})
	_ = tx1.Insert("members", storage.Record{"id": storage.IntValue(2), "code": storage.StringValue("CODE_B")})
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit 1 failed: %v", err)
	}

	// 1. Update row 2 to collide with committed row 1
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 2 failed: %v", err)
	}
	defer tx2.Rollback()

	err = tx2.Update("members", storage.IntValue(2), storage.Record{"code": storage.StringValue("CODE_A")})
	if err == nil {
		t.Fatalf("expected error updating row to colliding unique code, got nil")
	}
	if !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("expected ErrUniqueViolation, got: %v", err)
	}

	// 2. Updating own row with same value should NOT fail
	err = tx2.Update("members", storage.IntValue(2), storage.Record{"code": storage.StringValue("CODE_B")})
	if err != nil {
		t.Fatalf("updating own row with same unique value should succeed, got: %v", err)
	}
}

// TestGroupA_BUG_MEM_WAL_CREATE_TABLE_INDEX_OMISSION_1 tests that WAL recovery properly initializes
// inline secondary indexes from OpCreateTable.
func TestGroupA_BUG_MEM_WAL_CREATE_TABLE_INDEX_OMISSION_1(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wal_create_idx_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 1. Create engine with AOF and create table with inline index
	eng := New()
	opts := Options{
		EnableAOF:   true,
		DataDir:     tmpDir,
		FsyncPolicy: FsyncAlways,
	}
	if err := eng.OpenWithOptions(opts); err != nil {
		t.Fatalf("OpenWithOptions failed: %v", err)
	}

	schema := &storage.TableSchema{
		Name: "books",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "isbn", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "ix_isbn", TableName: "books", Columns: []string{"isbn"}, Unique: true},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("books", storage.Record{"id": storage.IntValue(1), "isbn": storage.StringValue("978-0134685991")})
	_ = tx.Commit()
	sess.Close()
	eng.Close()

	// 2. Re-open engine from WAL and verify ix_isbn index exists and is populated
	eng2 := New()
	if err := eng2.OpenWithOptions(opts); err != nil {
		t.Fatalf("re-opening eng2 failed: %v", err)
	}
	defer eng2.Close()

	td, err := eng2.GetTable("books")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	if len(td.Indexes) == 0 {
		t.Fatalf("expected secondary indexes in recovered table schema, got 0")
	}

	sess2, _ := eng2.NewSession()
	defer sess2.Close()
	tx2, _ := sess2.BeginTx(storage.TxOptions{ReadOnly: true})
	defer tx2.Rollback()

	cur, err := tx2.IndexScan("books", "ix_isbn", nil, nil)
	if err != nil {
		t.Fatalf("IndexScan on recovered index failed: %v", err)
	}
	defer cur.Close()

	count := 0
	for cur.Next() {
		count++
	}
	if count != 1 {
		t.Fatalf("expected 1 row from recovered index scan, got %d", count)
	}
}

// TestGroupA_BUG_WAL_TORN_BOOT_1 tests that an engine can cleanly boot and recover when the tail
// of the WAL file suffers a torn write from an ungraceful termination.
func TestGroupA_BUG_WAL_TORN_BOOT_1(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wal_torn_boot_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := Options{
		EnableAOF:   true,
		DataDir:     tmpDir,
		FsyncPolicy: FsyncAlways,
	}

	// 1. Write valid transactions
	eng := New()
	if err := eng.OpenWithOptions(opts); err != nil {
		t.Fatalf("OpenWithOptions failed: %v", err)
	}

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	_ = eng.CreateTable(schema)

	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(1), "val": storage.StringValue("v1")})
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(2), "val": storage.StringValue("v2")})
	_ = tx.Commit()
	sess.Close()
	eng.Close()

	// 2. Simulate a torn write by appending partial/truncated bytes at the tail of wal.aof
	aofPath := filepath.Join(tmpDir, "wal.aof")
	f, err := os.OpenFile(aofPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open wal.aof for appending torn write: %v", err)
	}
	// Append a 4-byte frame header indicating 100 bytes payload, but only write 12 bytes of payload
	var tornHeader [4]byte
	tornHeader[0] = 0
	tornHeader[1] = 0
	tornHeader[2] = 0
	tornHeader[3] = 100 // claims 100 bytes
	_, _ = f.Write(tornHeader[:])
	_, _ = f.Write([]byte("partial-torn"))
	f.Close()

	// 3. Re-open engine: it must successfully boot, recover valid records, and truncate the torn write
	engRecovered := New()
	if err := engRecovered.OpenWithOptions(opts); err != nil {
		t.Fatalf("engine failed to boot with torn WAL write: %v", err)
	}
	defer engRecovered.Close()

	// 4. Verify valid records (id=1, id=2) were fully recovered
	sessRec, _ := engRecovered.NewSession()
	defer sessRec.Close()
	txRec, _ := sessRec.BeginTx(storage.TxOptions{ReadOnly: true})
	defer txRec.Rollback()

	rec1, err := txRec.Get("items", storage.IntValue(1))
	if err != nil || rec1["val"].SVal != "v1" {
		t.Fatalf("expected recovered row 1, got error: %v, rec: %+v", err, rec1)
	}
	rec2, err := txRec.Get("items", storage.IntValue(2))
	if err != nil || rec2["val"].SVal != "v2" {
		t.Fatalf("expected recovered row 2, got error: %v, rec: %+v", err, rec2)
	}
}
