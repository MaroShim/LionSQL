package memengine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lionsql/pkg/storage"
)

func createTestSchema() *storage.TableSchema {
	return &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeInt, Nullable: false},
		},
	}
}

func TestMemEngine_TableOperations(t *testing.T) {
	eng := New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := createTestSchema()
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// Duplicate table error
	if err := eng.CreateTable(schema); err == nil {
		t.Fatalf("Expected error creating duplicate table, got nil")
	}

	tbl, err := eng.GetTable("accounts")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	if tbl.Name != "accounts" {
		t.Errorf("Expected table accounts, got %s", tbl.Name)
	}

	tables, err := eng.ListTables()
	if err != nil || len(tables) != 1 {
		t.Fatalf("Expected 1 table in ListTables, got %d, err: %v", len(tables), err)
	}
}

func TestMemEngine_TransactionCommitAndRollback(t *testing.T) {
	eng := New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := createTestSchema()
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	// 1. Insert and Commit
	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	err = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"holder":  storage.StringValue("Alice"),
		"balance": storage.IntValue(1000),
	})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Verify committed row
	tx2, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	rec, err := tx2.Get("accounts", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if rec["holder"].SVal != "Alice" || rec["balance"].IVal != 1000 {
		t.Fatalf("Unexpected record data: %+v", rec)
	}
	_ = tx2.Commit()

	// 3. Test Rollback
	tx3, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	err = tx3.Insert("accounts", storage.Record{
		"id":      storage.IntValue(2),
		"holder":  storage.StringValue("Bob"),
		"balance": storage.IntValue(500),
	})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx3.Rollback(); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// 4. Verify rolled-back row does not exist
	tx4, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx4.Rollback()

	if _, err := tx4.Get("accounts", storage.IntValue(2)); err == nil {
		t.Fatalf("Expected key 2 to not exist after rollback, but was found")
	}
}

func TestMemEngine_DiskPersistenceAcrossRestarts(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Open engine, create table, insert record, close engine
	eng1 := New()
	if err := eng1.Open(tempDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	schema := createTestSchema()
	if err := eng1.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess1, err := eng1.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	tx1, err := sess1.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	err = tx1.Insert("accounts", storage.Record{
		"id":      storage.IntValue(100),
		"holder":  storage.StringValue("Persistent User"),
		"balance": storage.IntValue(2500),
	})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if err := eng1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 2. Open new engine instance on the same directory and verify data was recovered
	eng2 := New()
	if err := eng2.Open(tempDir); err != nil {
		t.Fatalf("Open 2 failed: %v", err)
	}
	defer eng2.Close()

	tbl, err := eng2.GetTable("accounts")
	if err != nil {
		t.Fatalf("GetTable failed after recovery: %v", err)
	}
	if tbl.Name != "accounts" {
		t.Errorf("expected table name accounts, got %s", tbl.Name)
	}

	sess2, err := eng2.NewSession()
	if err != nil {
		t.Fatalf("NewSession 2 failed: %v", err)
	}
	defer sess2.Close()

	tx2, err := sess2.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx 2 failed: %v", err)
	}
	defer tx2.Rollback()

	rec, err := tx2.Get("accounts", storage.IntValue(100))
	if err != nil {
		t.Fatalf("Get failed after recovery: %v", err)
	}

	if rec["holder"].SVal != "Persistent User" || rec["balance"].IVal != 2500 {
		t.Errorf("recovered unexpected record data: %+v", rec)
	}
}

func TestMemCursor_ZeroCopyScanCorrectness(t *testing.T) {
	eng := New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "score", Type: storage.TypeInt, Nullable: false},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_score", TableName: "users", Columns: []string{"score"}},
		},
	}
	_ = eng.CreateTable(schema)

	sess, _ := eng.NewSession()
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	for i := 1; i <= 100; i++ {
		_ = tx.Insert("users", storage.Record{
			"id":    storage.IntValue(int64(i)),
			"score": storage.IntValue(int64(i * 10)),
		})
	}
	_ = tx.Commit()

	// Sequential Scan
	txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer txRead.Rollback()

	cur, err := txRead.Scan("users")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	defer cur.Close()

	count := 0
	for cur.Next() {
		r, err := cur.Record()
		if err != nil {
			t.Fatalf("Record() error: %v", err)
		}
		if r == nil {
			t.Fatalf("Record() returned nil")
		}
		count++
	}
	if count != 100 {
		t.Fatalf("Expected 100 records from Scan, got %d", count)
	}

	// Index Scan
	low := storage.IntValue(200)
	high := storage.IntValue(400)
	idxCur, err := txRead.IndexScan("users", "idx_score", &low, &high)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	defer idxCur.Close()

	idxCount := 0
	for idxCur.Next() {
		r, _ := idxCur.Record()
		score := r["score"].IVal
		if score < 200 || score > 400 {
			t.Errorf("Score out of range: %d", score)
		}
		idxCount++
	}
	if idxCount != 21 {
		t.Fatalf("Expected 21 records in index range [200, 400], got %d", idxCount)
	}
}

func BenchmarkMemCursor_Scan(b *testing.B) {
	eng := New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "bench_scan",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt, Nullable: false},
		},
	}
	_ = eng.CreateTable(schema)

	sess, _ := eng.NewSession()
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	for i := 1; i <= 1000; i++ {
		_ = tx.Insert("bench_scan", storage.Record{
			"id":  storage.IntValue(int64(i)),
			"val": storage.IntValue(int64(i * 2)),
		})
	}
	_ = tx.Commit()

	txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer txRead.Rollback()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cur, _ := txRead.Scan("bench_scan")
		for cur.Next() {
			_, _ = cur.Record()
		}
		_ = cur.Close()
	}
}

func TestEngine_TruncateTable(t *testing.T) {
	tmpDir := t.TempDir()
	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Insert 5 rows
	tx, _ := sess.BeginTx(storage.TxOptions{})
	for i := 1; i <= 5; i++ {
		_ = tx.Insert("items", storage.Record{
			"id":   storage.IntValue(int64(i)),
			"name": storage.StringValue(fmt.Sprintf("Item_%d", i)),
		})
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	// Truncate table
	if err := eng.TruncateTable("items"); err != nil {
		t.Fatalf("TruncateTable failed: %v", err)
	}

	// Verify table is empty
	tx2, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	cur, _ := tx2.Scan("items")
	count := 0
	for cur.Next() {
		count++
	}
	cur.Close()
	tx2.Rollback()

	if count != 0 {
		t.Fatalf("expected 0 rows after truncate, got %d", count)
	}

	// Insert 1 row after truncate
	tx3, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx3.Insert("items", storage.Record{
		"id":   storage.IntValue(100),
		"name": storage.StringValue("Item_100"),
	})
	_ = tx3.Commit()

	sess.Close()
	eng.Close()

	// Reopen engine and verify WAL replay preserved truncate and subsequent insert
	eng2 := New()
	if err := eng2.Open(tmpDir); err != nil {
		t.Fatalf("failed to reopen engine: %v", err)
	}
	defer eng2.Close()

	sess2, _ := eng2.NewSession()
	defer sess2.Close()

	tx4, _ := sess2.BeginTx(storage.TxOptions{ReadOnly: true})
	defer tx4.Rollback()

	rec, err := tx4.Get("items", storage.IntValue(100))
	if err != nil || rec["name"].SVal != "Item_100" {
		t.Fatalf("failed retrieving record after WAL recovery: %v, rec: %+v", err, rec)
	}

	// Old records should not exist
	for i := 1; i <= 5; i++ {
		if _, err := tx4.Get("items", storage.IntValue(int64(i))); err == nil {
			t.Fatalf("old record id=%d still exists after truncate WAL replay", i)
		}
	}
}

func TestTransaction_LifecycleAndSnapshotCommitMetadata(t *testing.T) {
	eng := New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "balance", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	initialLSN := eng.CurrentLSN()
	if initialLSN <= 0 {
		t.Fatalf("expected initial LSN > 0 after CreateTable, got %d", initialLSN)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// 1. Begin write transaction
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	if tx1.SnapshotLSN() != initialLSN {
		t.Fatalf("expected snapshot LSN %d, got %d", initialLSN, tx1.SnapshotLSN())
	}
	if tx1.SnapshotTimestamp() <= 0 {
		t.Fatalf("expected positive snapshot timestamp, got %d", tx1.SnapshotTimestamp())
	}
	if tx1.CommitLSN() != 0 || tx1.CommitTimestamp() != 0 {
		t.Fatalf("uncommitted tx must have zero commit LSN/ts, got %d, %d", tx1.CommitLSN(), tx1.CommitTimestamp())
	}

	// Insert row and commit
	if err := tx1.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"balance": storage.IntValue(1000),
	}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	commitLSN1 := tx1.CommitLSN()
	if commitLSN1 <= initialLSN {
		t.Fatalf("expected commitLSN %d > initialLSN %d", commitLSN1, initialLSN)
	}
	if tx1.CommitTimestamp() <= 0 {
		t.Fatalf("expected positive commit timestamp, got %d", tx1.CommitTimestamp())
	}
	if eng.CurrentLSN() != commitLSN1 {
		t.Fatalf("expected engine CurrentLSN %d to match tx commitLSN %d", eng.CurrentLSN(), commitLSN1)
	}

	// 2. Rollback transaction should not assign commit LSN
	tx2, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx2.Insert("accounts", storage.Record{
		"id":      storage.IntValue(2),
		"balance": storage.IntValue(2000),
	})
	_ = tx2.Rollback()

	if tx2.CommitLSN() != 0 || tx2.CommitTimestamp() != 0 {
		t.Fatalf("rolled back tx must retain zero commit LSN/ts, got %d, %d", tx2.CommitLSN(), tx2.CommitTimestamp())
	}

	// 3. Explicit SnapshotLSN binding in options (Replica simulation)
	customSnapLSN := int64(9999)
	customSnapTS := int64(1234567890)
	tx3, _ := sess.BeginTx(storage.TxOptions{
		ReadOnly:          true,
		SnapshotLSN:       customSnapLSN,
		SnapshotTimestamp: customSnapTS,
	})
	defer tx3.Rollback()

	if tx3.SnapshotLSN() != customSnapLSN {
		t.Fatalf("expected custom snapshot LSN %d, got %d", customSnapLSN, tx3.SnapshotLSN())
	}
	if tx3.SnapshotTimestamp() != customSnapTS {
		t.Fatalf("expected custom snapshot ts %d, got %d", customSnapTS, tx3.SnapshotTimestamp())
	}
}

func TestMemEngine_RedisStyleOptionsAndCommitCycle(t *testing.T) {
	// 1. Test Pure In-Memory Mode (AOF=false, RDB=false)
	tmpDir := t.TempDir()
	engMem := New()
	err := engMem.OpenWithOptions(Options{
		DataDir:   tmpDir,
		EnableAOF: false,
		EnableRDB: false,
	})
	if err != nil {
		t.Fatalf("OpenWithOptions failed: %v", err)
	}
	defer engMem.Close()

	schema := &storage.TableSchema{
		Name: "cache",
		Columns: []storage.Column{
			{Name: "k", Type: storage.TypeVarChar, PrimaryKey: true},
			{Name: "v", Type: storage.TypeVarChar},
		},
	}
	if err := engMem.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := engMem.NewSession()
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("cache", storage.Record{"k": storage.StringValue("key1"), "v": storage.StringValue("val1")})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify no AOF or RDB file created in pure in-memory mode
	aofPath := filepath.Join(tmpDir, "wal.aof")
	rdbPath := filepath.Join(tmpDir, "tables.mpk")
	if _, err := os.Stat(aofPath); !os.IsNotExist(err) {
		t.Errorf("expected wal.aof to NOT exist in pure in-memory mode, got %v", err)
	}
	_ = engMem.Close()
	if _, err := os.Stat(rdbPath); !os.IsNotExist(err) {
		t.Errorf("expected tables.mpk to NOT exist in pure in-memory mode, got %v", err)
	}

	// 2. Test Redis AOF -> RDB commit cycle and background rewrite
	cycleDir := t.TempDir()
	engCycle := New()
	err = engCycle.OpenWithOptions(Options{
		DataDir:       cycleDir,
		EnableAOF:     true,
		EnableRDB:     true,
		FsyncPolicy:   FsyncEverySec,
		AOFThreshold:  1024 * 1024,
		RDBInterval:   30 * time.Millisecond,
		RDBMinChanges: 2,
	})
	if err != nil {
		t.Fatalf("OpenWithOptions cycle failed: %v", err)
	}

	if err := engCycle.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable cycle failed: %v", err)
	}

	sessCycle, _ := engCycle.NewSession()
	defer sessCycle.Close()

	// Mutate beyond RDBMinChanges (CreateTable is 1 op, Insert is 1 op -> 2 ops)
	txCycle, _ := sessCycle.BeginTx(storage.TxOptions{})
	_ = txCycle.Insert("cache", storage.Record{"k": storage.StringValue("user:1"), "v": storage.StringValue("alice")})
	_ = txCycle.Commit()

	// Wait for bgSaveLoop to trigger AOF -> RDB rewrite cycle
	cycleRDB := filepath.Join(cycleDir, "tables.mpk")
	deadline := time.Now().Add(2 * time.Second)
	saved := false
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(cycleRDB); err == nil && fi.Size() > 0 {
			saved = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !saved {
		t.Fatalf("timed out waiting for background AOF -> RDB compaction to create tables.mpk")
	}

	_ = engCycle.Close()

	// 3. Restart and verify data restored from RDB
	engRestored := New()
	err = engRestored.OpenWithOptions(Options{
		DataDir:   cycleDir,
		EnableRDB: true,
	})
	if err != nil {
		t.Fatalf("failed to open restored engine: %v", err)
	}
	defer engRestored.Close()

	restoredSess, _ := engRestored.NewSession()
	defer restoredSess.Close()

	readTx, _ := restoredSess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()

	rec, err := readTx.Get("cache", storage.StringValue("user:1"))
	if err != nil {
		t.Fatalf("failed to read restored key: %v", err)
	}
	if rec["v"].SVal != "alice" {
		t.Errorf("expected alice, got %+v", rec["v"])
	}
}

func TestTransaction_PoolingAndMapReuse(t *testing.T) {
	eng := New()
	defer eng.Close()
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	schema := createTestSchema()
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 1. Commit cycle reusing tx and maps
	for i := 1; i <= 10; i++ {
		tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: false})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}

		mTx := tx.(*Transaction)
		if len(mTx.changes) != 0 || len(mTx.local) != 0 {
			t.Fatalf("expected cleared changes and local maps on tx begin, got %d, %d", len(mTx.changes), len(mTx.local))
		}

		rec := storage.Record{
			"id":      storage.IntValue(int64(i)),
			"holder":  storage.StringValue(fmt.Sprintf("item-%d", i)),
			"balance": storage.IntValue(int64(i * 100)),
		}
		if err := tx.Insert("accounts", rec); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}

		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
	}

	// 2. Rollback cycle reusing tx and maps
	for i := 11; i <= 20; i++ {
		tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: false})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}

		mTx := tx.(*Transaction)
		if len(mTx.changes) != 0 || len(mTx.local) != 0 {
			t.Fatalf("expected cleared changes and local maps on tx begin, got %d, %d", len(mTx.changes), len(mTx.local))
		}

		rec := storage.Record{
			"id":      storage.IntValue(int64(i)),
			"holder":  storage.StringValue(fmt.Sprintf("item-%d", i)),
			"balance": storage.IntValue(int64(i * 100)),
		}
		if err := tx.Insert("accounts", rec); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}

		if err := tx.Rollback(); err != nil {
			t.Fatalf("Rollback failed: %v", err)
		}
	}

	// Verify only items 1..10 were committed
	readTx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx ReadOnly failed: %v", err)
	}
	defer readTx.Rollback()

	for i := 1; i <= 10; i++ {
		rec, err := readTx.Get("accounts", storage.IntValue(int64(i)))
		if err != nil || rec == nil {
			t.Fatalf("expected item %d committed, got err: %v", i, err)
		}
	}
	for i := 11; i <= 20; i++ {
		rec, err := readTx.Get("accounts", storage.IntValue(int64(i)))
		if err == nil && rec != nil {
			t.Fatalf("rolled back item %d must not exist", i)
		}
	}
}

func TestMemEngine_CapacityGuard(t *testing.T) {
	eng := New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := createTestSchema()
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// 1. Unlimited capacity (default) - commit succeeds
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	err = tx1.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"holder":  storage.StringValue("Alice"),
		"balance": storage.IntValue(100),
	})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("expected commit to succeed with default limit, got: %v", err)
	}

	// 2. Set capacity limit below current heap - commit must return ErrTableFull
	currHeap := CurrentHeapBytes()
	if currHeap <= 0 {
		t.Fatalf("expected positive heap bytes, got %d", currHeap)
	}
	eng.SetMaxDataBytes(1) // 1 byte limit is strictly below current heap

	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	err = tx2.Insert("accounts", storage.Record{
		"id":      storage.IntValue(2),
		"holder":  storage.StringValue("Bob"),
		"balance": storage.IntValue(200),
	})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	err = tx2.Commit()
	if err == nil {
		t.Fatalf("expected ErrTableFull when heap exceeds MaxDataBytes, but commit succeeded")
	}
	if !errors.Is(err, storage.ErrTableFull) {
		t.Fatalf("expected errors.Is(err, storage.ErrTableFull), got: %v", err)
	}

	// 3. Relax limit to generous budget - commit succeeds
	eng.SetMaxDataBytes(currHeap + 100*1024*1024)

	tx3, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	err = tx3.Insert("accounts", storage.Record{
		"id":      storage.IntValue(2),
		"holder":  storage.StringValue("Bob"),
		"balance": storage.IntValue(200),
	})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx3.Commit(); err != nil {
		t.Fatalf("expected commit to succeed with relaxed limit, got: %v", err)
	}

	// 4. Verify ReadTx can read both accounts
	readTx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx ReadOnly failed: %v", err)
	}
	defer readTx.Rollback()

	rec1, err := readTx.Get("accounts", storage.IntValue(1))
	if err != nil || rec1 == nil {
		t.Fatalf("account 1 not found: %v", err)
	}
	rec2, err := readTx.Get("accounts", storage.IntValue(2))
	if err != nil || rec2 == nil {
		t.Fatalf("account 2 not found: %v", err)
	}
}

func TestMemengine_RenameTable_FKCacheAndWAL(t *testing.T) {
	dir := t.TempDir()
	eng := New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	// 1. Create parent table
	parentSchema := &storage.TableSchema{
		Name: "parent_tbl",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}
	if err := eng.CreateTable(parentSchema); err != nil {
		t.Fatalf("CreateTable parent failed: %v", err)
	}

	// 2. Create child table referencing parent
	childSchema := &storage.TableSchema{
		Name: "child_tbl",
		Columns: []storage.Column{
			{Name: "cid", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "pid", Type: storage.TypeInt},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_child_parent",
				ChildColumns:  []string{"pid"},
				ParentTable:   "parent_tbl",
				ParentColumns: []string{"id"},
			},
		},
	}
	if err := eng.CreateTable(childSchema); err != nil {
		t.Fatalf("CreateTable child failed: %v", err)
	}

	// 3. Rename table
	if err := eng.RenameTable("parent_tbl", "parent_renamed"); err != nil {
		t.Fatalf("RenameTable failed: %v", err)
	}

	// Verify old table doesn't exist, new table exists
	if _, err := eng.GetTable("parent_tbl"); err == nil {
		t.Errorf("expected parent_tbl to not exist")
	}
	newTbl, err := eng.GetTable("parent_renamed")
	if err != nil || newTbl == nil {
		t.Fatalf("expected parent_renamed to exist: %v", err)
	}

	// Verify FK cache was rebuilt
	eng.mu.RLock()
	fkRefOld := eng.fkCache["parent_tbl"]
	fkRefNew := eng.fkCache["parent_renamed"]
	eng.mu.RUnlock()
	if len(fkRefOld) != 0 {
		t.Errorf("expected old table to have 0 FK references, got %d", len(fkRefOld))
	}
	if len(fkRefNew) != 1 {
		t.Errorf("expected renamed table to have 1 FK reference, got %d", len(fkRefNew))
	}

	// 4. Reopen from disk and verify renamed table is preserved across AOF replay (Scope-012)
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	eng2 := New()
	if err := eng2.Open(dir); err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	defer eng2.Close()

	if _, err := eng2.GetTable("parent_tbl"); err == nil {
		t.Errorf("after reopen: expected parent_tbl to not exist")
	}
	if _, err := eng2.GetTable("parent_renamed"); err != nil {
		t.Errorf("after reopen: expected parent_renamed to exist: %v", err)
	}

	// 5. Test mixed-case rename across AOF replay
	if err := eng2.RenameTable("PARENT_RENAMED", "Parent_Final"); err != nil {
		t.Fatalf("RenameTable with mixed case failed: %v", err)
	}
	if err := eng2.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	eng3 := New()
	if err := eng3.Open(dir); err != nil {
		t.Fatalf("Reopen 3 failed: %v", err)
	}
	defer eng3.Close()

	if _, err := eng3.GetTable("parent_final"); err != nil {
		t.Errorf("expected parent_final to exist case-insensitively after reopen: %v", err)
	}
}

func TestP1_High_Scope005_DeleteThenInsertSamePKInSameTx(t *testing.T) {
	eng := New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "test_reinsert",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 1. Initial insert of row id=1
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx1.Insert("test_reinsert", storage.Record{
		"id":   storage.IntValue(1),
		"name": storage.StringValue("initial"),
	}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. In a second transaction, delete row id=1, then insert row id=1 with new name, then commit
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx2.Delete("test_reinsert", storage.IntValue(1)); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if err := tx2.Insert("test_reinsert", storage.Record{
		"id":   storage.IntValue(1),
		"name": storage.StringValue("reinserted"),
	}); err != nil {
		t.Fatalf("Insert after delete failed: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit after delete+insert failed: %v", err)
	}

	// 3. Verify row has updated value
	tx3, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx3.Rollback()

	rec, err := tx3.Get("test_reinsert", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if rec["name"].String() != "reinserted" {
		t.Fatalf("expected 'reinserted', got %v", rec["name"])
	}
}

func TestRegression_AUDIT_093_001_TableVarNoDoubleApplyOnDeferRollback(t *testing.T) {
	eng := New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	tvSchema := &storage.TableSchema{
		Name: "__tv_audit001",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(tvSchema); err != nil {
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
	if err := tx.Insert("__tv_audit001", storage.Record{
		"id":  storage.IntValue(1),
		"val": storage.StringValue("once"),
	}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	// Simulate idiomatic defer tx.Rollback() after tx.Commit()
	_ = tx.Rollback()

	cnt, err := eng.TableRowCount("__tv_audit001")
	if err != nil {
		t.Fatalf("TableRowCount failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected TableRowCount=1 (no double apply), got %d", cnt)
	}
}

func TestRegression_AUDIT_093_005_SnapSchemaRefreshedOnIndexDDL(t *testing.T) {
	eng := New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	if err := eng.CreateTable(&storage.TableSchema{
		Name: "audit005_tbl",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar},
		},
	}); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	if err := eng.CreateIndex(&storage.IndexSchema{
		Name:      "idx_audit005_code",
		TableName: "audit005_tbl",
		Columns:   []string{"code"},
		Unique:    true,
	}); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	td := eng.tables["audit005_tbl"]
	snap := td.snapSchema
	if snap == nil || len(snap.Indexes) != 1 || snap.Indexes[0].Name != "idx_audit005_code" {
		t.Fatalf("expected snapSchema to reflect Created index, got %+v", snap)
	}

	if err := eng.DropIndex("audit005_tbl", "idx_audit005_code"); err != nil {
		t.Fatalf("DropIndex failed: %v", err)
	}
	snapAfter := td.snapSchema
	if snapAfter == nil || len(snapAfter.Indexes) != 0 {
		t.Fatalf("expected snapSchema to have 0 indexes after DropIndex, got %+v", snapAfter)
	}
}

func TestRegression_AUDIT_093_003_WALReplayCorruptTupleFailsClosed(t *testing.T) {
	eng := New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	if err := eng.CreateTable(&storage.TableSchema{
		Name: "audit003_tbl",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 1. OpInsert with corrupt Data (version 0x01 instead of 0x02) must fail closed
	corruptInsert := WALRecord{
		LSN: 10,
		Ops: []WALOp{
			{
				Type:    OpInsert,
				Table:   "audit003_tbl",
				Key:     "1",
				Data:    []byte{0x01, 0x00, 0x02, 0x00},
				BaseVer: 1,
			},
		},
	}
	if err := eng.applyWALRecordLocked(corruptInsert); err == nil {
		t.Fatalf("expected applyWALRecordLocked OpInsert with corrupt tuple to fail closed, got nil")
	}

	// 2. OpUpdate with corrupt Data must fail closed and NOT append orphan key to sh.keys
	corruptUpdate := WALRecord{
		LSN: 11,
		Ops: []WALOp{
			{
				Type:    OpUpdate,
				Table:   "audit003_tbl",
				Key:     "999",
				Data:    []byte{0x01, 0x00, 0x02, 0x00},
				BaseVer: 2,
			},
		},
	}
	if err := eng.applyWALRecordLocked(corruptUpdate); err == nil {
		t.Fatalf("expected applyWALRecordLocked OpUpdate with corrupt tuple to fail closed, got nil")
	}
	td := eng.tables["audit003_tbl"]
	sh := &td.shards[hashShard("999")]
	if _, exists := sh.entries["999"]; exists {
		t.Fatalf("expected key 999 to not exist in shard entries after failed OpUpdate decode")
	}
	if td.hasKey("999") {
		t.Fatalf("expected key 999 to not exist in td.Keys after failed OpUpdate decode")
	}

	// 3. OpInsert targeting non-existent table must fail closed
	noTableInsert := WALRecord{
		LSN: 12,
		Ops: []WALOp{
			{
				Type:  OpInsert,
				Table: "no_such_table",
				Key:   "1",
				Data:  []byte{0x02, 0x00, 0x01, 0x00, 0x00},
			},
		},
	}
	if err := eng.applyWALRecordLocked(noTableInsert); err == nil {
		t.Fatalf("expected applyWALRecordLocked OpInsert for missing table to fail closed, got nil")
	}

	// 4. OpInsert with empty data and nil Record must fail closed
	emptyInsert := WALRecord{
		LSN: 13,
		Ops: []WALOp{
			{
				Type:  OpInsert,
				Table: "audit003_tbl",
				Key:   "2",
			},
		},
	}
	if err := eng.applyWALRecordLocked(emptyInsert); err == nil {
		t.Fatalf("expected applyWALRecordLocked OpInsert with missing data and record to fail closed, got nil")
	}
}

func TestRegression_AUDIT_093_004_ReplayWALClosedFileStatErrorFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "wal.aof")
	// Write a mid-file corrupt header (8 bytes with invalid frameLen=0) so ReplayWAL fails closed
	if err := os.WriteFile(walPath, []byte{0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04}, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	_, err := ReplayWAL(walPath, func(rec WALRecord) error {
		return nil
	})
	if err == nil {
		t.Fatalf("expected ReplayWAL to return error on corrupted frame header, got nil")
	}

	// 2. Non-existent WAL returns (0, nil) cleanly
	cleanLSN, cleanErr := ReplayWAL(filepath.Join(tmpDir, "missing.aof"), func(rec WALRecord) error {
		return nil
	})
	if cleanErr != nil || cleanLSN != 0 {
		t.Fatalf("expected non-existent WAL to return (0, nil), got (%d, %v)", cleanLSN, cleanErr)
	}
}

func TestRegression_AUDIT_093_006_GetTableAndListTablesReturnDeepClone(t *testing.T) {
	eng := New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	if err := eng.CreateTable(&storage.TableSchema{
		Name: "audit006_tbl",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_audit006_email", Columns: []string{"email"}},
		},
	}); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sch, err := eng.GetTable("audit006_tbl")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	sch.Columns[0].Name = "MUTATED_COL"
	sch.Indexes[0].Disabled = true

	sch2, err := eng.GetTable("audit006_tbl")
	if err != nil {
		t.Fatalf("GetTable 2 failed: %v", err)
	}
	if sch2.Columns[0].Name != "id" || sch2.Indexes[0].Disabled {
		t.Fatalf("GetTable did not return a deep clone; engine schema was mutated: %+v", sch2)
	}

	list, err := eng.ListTables()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListTables failed: %v", err)
	}
	list[0].Columns[0].Name = "MUTATED_FROM_LIST"
	sch3, _ := eng.GetTable("audit006_tbl")
	if sch3.Columns[0].Name != "id" {
		t.Fatalf("ListTables did not return a deep clone; engine schema was mutated: %+v", sch3)
	}

	// Verify Transaction.ListTables returns an independent deep clone
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()
	tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	txList, err := tx.ListTables()
	if err != nil || len(txList) != 1 {
		t.Fatalf("tx.ListTables failed: %v", err)
	}
	txList[0].Columns[0].Name = "MUTATED_FROM_TX_LIST"
	txList[0].Indexes[0].Disabled = true

	sch4, _ := eng.GetTable("audit006_tbl")
	if sch4.Columns[0].Name != "id" || sch4.Indexes[0].Disabled {
		t.Fatalf("tx.ListTables did not return a deep clone; engine schema was mutated: %+v", sch4)
	}
}

func TestRegression_AUDIT_093_010_LoadDiskLockedNilTableGuard(t *testing.T) {
	tmpDir := t.TempDir()
	container := SnapshotContainer{
		SnapshotLSN: 5,
		Tables: map[string]*TableData{
			"nil_table": nil,
		},
	}
	raw, err := MarshalMsgPack(container)
	if err != nil {
		t.Fatalf("MarshalMsgPack failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "tables.mpk"), raw, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	eng := New()
	err = eng.Open(tmpDir)
	if err == nil {
		_ = eng.Close()
		t.Fatalf("expected Open to return fail-closed error on nil table entry in RDB snapshot, got nil")
	}
}

func TestRegression_AUDIT_093_010_LoadDiskLockedCaseCollisionAndSchemaIntegrity(t *testing.T) {
	t.Run("CaseCollisionRejection", func(t *testing.T) {
		tmpDir := t.TempDir()
		container := SnapshotContainer{
			SnapshotLSN: 5,
			Tables: map[string]*TableData{
				"users": {
					Schema: storage.TableSchema{
						Name: "users",
						Columns: []storage.Column{
							{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
						},
					},
				},
				"USERS": {
					Schema: storage.TableSchema{
						Name: "USERS",
						Columns: []storage.Column{
							{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
						},
					},
				},
			},
		}
		raw, err := MarshalMsgPack(container)
		if err != nil {
			t.Fatalf("MarshalMsgPack failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "tables.mpk"), raw, 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		eng := New()
		err = eng.Open(tmpDir)
		if err == nil {
			_ = eng.Close()
			t.Fatalf("expected Open to return fail-closed error on case-colliding table entries in RDB snapshot, got nil")
		}
	})

	t.Run("EmptySchemaNameRejection", func(t *testing.T) {
		tmpDir := t.TempDir()
		container := SnapshotContainer{
			SnapshotLSN: 5,
			Tables: map[string]*TableData{
				"bad_table": {
					Schema: storage.TableSchema{
						Name: "",
						Columns: []storage.Column{
							{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
						},
					},
				},
			},
		}
		raw, err := MarshalMsgPack(container)
		if err != nil {
			t.Fatalf("MarshalMsgPack failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "tables.mpk"), raw, 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		eng := New()
		err = eng.Open(tmpDir)
		if err == nil {
			_ = eng.Close()
			t.Fatalf("expected Open to return fail-closed error on empty schema name in RDB snapshot, got nil")
		}
	})
}



