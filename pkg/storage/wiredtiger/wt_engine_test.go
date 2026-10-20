//go:build cgo && wiredtiger

package wiredtiger

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"lionsql/pkg/backup"
	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestWiredTigerWALHookAndReplay(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_wal_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(filepath.Join(tmpDir, "primary")); err != nil {
		t.Fatalf("failed to open primary WT engine: %v", err)
	}
	defer eng.Close()

	var (
		mu      sync.Mutex
		records []memengine.WALRecord
	)

	eng.SetWALHook(func(rec memengine.WALRecord) {
		mu.Lock()
		defer mu.Unlock()
		records = append(records, rec)
	})

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "val", Type: storage.TypeInt, Nullable: true},
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

	// 1. Transaction with Insert, Savepoint, and RollbackTo
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	if err := tx.Insert("users", storage.Record{
		"id":   storage.IntValue(1),
		"name": storage.StringValue("alice"),
		"val":  storage.IntValue(100),
	}); err != nil {
		t.Fatalf("Insert alice failed: %v", err)
	}

	if err := tx.Savepoint("sp1"); err != nil {
		t.Fatalf("Savepoint sp1 failed: %v", err)
	}

	if err := tx.Insert("users", storage.Record{
		"id":   storage.IntValue(2),
		"name": storage.StringValue("bob_temp"),
		"val":  storage.IntValue(200),
	}); err != nil {
		t.Fatalf("Insert bob_temp failed: %v", err)
	}

	if err := tx.RollbackTo("sp1"); err != nil {
		t.Fatalf("RollbackTo sp1 failed: %v", err)
	}

	if err := tx.Insert("users", storage.Record{
		"id":   storage.IntValue(2),
		"name": storage.StringValue("bob_final"),
		"val":  storage.IntValue(250),
	}); err != nil {
		t.Fatalf("Insert bob_final failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Transaction with Update and Delete
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	if err := tx2.Update("users", storage.IntValue(1), storage.Record{
		"val": storage.IntValue(150),
	}); err != nil {
		t.Fatalf("Update alice failed: %v", err)
	}

	if err := tx2.Delete("users", storage.IntValue(2)); err != nil {
		t.Fatalf("Delete bob failed: %v", err)
	}

	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit tx2 failed: %v", err)
	}

	// 3. Rolled back transaction should emit zero records
	tx3, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	_ = tx3.Insert("users", storage.Record{
		"id":   storage.IntValue(3),
		"name": storage.StringValue("charlie"),
		"val":  storage.IntValue(300),
	})
	if err := tx3.Rollback(); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	mu.Lock()
	captured := make([]memengine.WALRecord, len(records))
	copy(captured, records)
	mu.Unlock()

	// Should have:
	// 1: CreateTable
	// 2: Tx1 (alice, bob_final)
	// 3: Tx2 (update alice, delete bob)
	if len(captured) != 3 {
		t.Fatalf("expected 3 WAL records captured, got %d", len(captured))
	}

	if captured[0].Ops[0].Type != memengine.OpCreateTable {
		t.Errorf("expected record 0 to be OpCreateTable, got %v", captured[0].Ops[0].Type)
	}

	if len(captured[1].Ops) != 2 {
		t.Fatalf("expected record 1 to have 2 ops, got %d", len(captured[1].Ops))
	}
	if captured[1].Ops[0].Type != memengine.OpInsert || captured[1].Ops[0].Key != "1" {
		t.Errorf("expected op 0 to be insert id 1, got %+v", captured[1].Ops[0])
	}
	if captured[1].Ops[1].Type != memengine.OpInsert || captured[1].Ops[1].Key != "2" {
		t.Errorf("expected op 1 to be insert id 2, got %+v", captured[1].Ops[1])
	}

	if len(captured[2].Ops) != 2 {
		t.Fatalf("expected record 2 to have 2 ops, got %d", len(captured[2].Ops))
	}
	if captured[2].Ops[0].Type != memengine.OpUpdate || captured[2].Ops[0].Key != "1" {
		t.Errorf("expected op 0 to be update id 1, got %+v", captured[2].Ops[0])
	}
	if captured[2].Ops[1].Type != memengine.OpDelete || captured[2].Ops[1].Key != "2" {
		t.Errorf("expected op 1 to be delete id 2, got %+v", captured[2].Ops[1])
	}

	// 4. Verify replaying captured WAL records into an in-memory replica produces exact match
	replicaEng := memengine.New()
	for _, rec := range captured {
		if err := backup.ApplyWALRecordToEngine(replicaEng, rec); err != nil {
			t.Fatalf("failed to replay WAL record LSN %d: %v", rec.LSN, err)
		}
	}

	repSess, err := replicaEng.NewSession()
	if err != nil {
		t.Fatalf("failed to create replica session: %v", err)
	}
	defer repSess.Close()

	repTx, err := repSess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("failed to begin replica tx: %v", err)
	}
	defer repTx.Rollback()

	aliceRec, err := repTx.Get("users", storage.IntValue(1))
	if err != nil {
		t.Fatalf("failed to get alice from replica: %v", err)
	}
	if aliceRec["val"].IVal != 150 {
		t.Errorf("expected replica alice val=150, got %d", aliceRec["val"].IVal)
	}

	_, err = repTx.Get("users", storage.IntValue(2))
	if err == nil {
		t.Errorf("expected bob to be deleted on replica, but found")
	}
}

func TestWiredTiger_GroupCommit_FsyncAlways(t *testing.T) {
	dir := t.TempDir()
	eng := New()
	eng.SetFsyncPolicy(memengine.FsyncAlways)
	if err := eng.Open(dir); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	if err := eng.CreateTable(&storage.TableSchema{
		Name: "kv",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "v", Type: storage.TypeVarChar},
		},
	}); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	const workers = 16
	const perWorker = 15
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wID := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess, err := eng.NewSession()
			if err != nil {
				t.Error(err)
				return
			}
			defer sess.Close()
			for j := 0; j < perWorker; j++ {
				id := int64(wID*1000 + j)
				tx, err := sess.BeginTx(storage.TxOptions{})
				if err != nil {
					t.Error(err)
					return
				}
				if err := tx.Insert("kv", storage.Record{
					"id": storage.IntValue(id),
					"v":  storage.StringValue("val"),
				}); err != nil {
					_ = tx.Rollback()
					t.Error(err)
					return
				}
				if err := tx.Commit(); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()

	total := int64(workers * perWorker)
	syncs := eng.SyncCount()
	if syncs <= 0 || syncs > total {
		t.Fatalf("expected sync count in 1..%d, got %d", total, syncs)
	}
	t.Logf("WiredTiger group commit: %d commits flushed with %d log_flush(sync=on) calls (%.1f commits/sync)",
		total, syncs, float64(total)/float64(syncs))
}

func TestWiredTiger_CursorNextEOFNoWrapAround(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_cursor_eof_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("eng.Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
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
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(1), "name": storage.StringValue("Item1")})
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(2), "name": storage.StringValue("Item2")})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	readTx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("readTx failed: %v", err)
	}
	defer readTx.Rollback()

	cur, err := readTx.Scan("items")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	defer cur.Close()

	if !cur.Next() {
		t.Fatalf("expected item 1")
	}
	if !cur.Next() {
		t.Fatalf("expected item 2")
	}
	if cur.Next() {
		t.Fatalf("expected false on EOF")
	}

	// Repeated calls to Next() MUST continue to return false and NOT wrap around to item 1
	for i := 0; i < 5; i++ {
		if cur.Next() {
			t.Fatalf("call %d after EOF unexpectedly returned true (cursor wrapped around!)", i+1)
		}
	}
}

func TestWiredTiger_Coordinator_DbGateTableQuery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_coord_dbgate_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("eng.Open failed: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(100), balance FLOAT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO users VALUES (1, 'Alice', 100.5), (2, 'Bob', 200.75);"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	query := `SELECT
  [basetbl].[id] AS [id],
  [basetbl].[name] AS [name],
  [basetbl].[balance] AS [balance]
  FROM [dbo].[users] [basetbl]
ORDER BY [id] ASC
OFFSET 0 ROWS FETCH NEXT 100 ROWS ONLY`

	res, err := coord.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("ExecuteSQL failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	if res.Rows[0]["name"] != "Alice" {
		t.Fatalf("unexpected row 0: %v", res.Rows[0])
	}
	if res.Rows[1]["name"] != "Bob" {
		t.Fatalf("unexpected row 1: %v", res.Rows[1])
	}
}

func TestWiredTiger_CursorPoolingAndReset(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_pool_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("eng.Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "title", Type: storage.TypeVarChar, Nullable: false},
			{Name: "qty", Type: storage.TypeInt, Nullable: false},
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

	wtSess := sess.(*WTSession)

	// Insert 10 rows
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := 1; i <= 10; i++ {
		err := tx.Insert("items", storage.Record{
			"id":    storage.IntValue(int64(i)),
			"title": storage.StringValue(fmt.Sprintf("item_%d", i)),
			"qty":   storage.IntValue(int64(i * 10)),
		})
		if err != nil {
			t.Fatalf("Insert %d failed: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 1. Verify point lookups (tx.Get) and cursor reuse
	tx2, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx2.Rollback()

	for i := 1; i <= 10; i++ {
		rec, err := tx2.Get("items", storage.IntValue(int64(i)))
		if err != nil {
			t.Fatalf("Get %d failed: %v", i, err)
		}
		if rec["title"].String() != fmt.Sprintf("item_%d", i) {
			t.Fatalf("unexpected title for %d: %v", i, rec["title"])
		}
	}

	// Pool should have 1 idle cursor for table:items
	wtSess.mu.Lock()
	poolCount := len(wtSess.cursorPool["table:items"])
	allCount := len(wtSess.allCursors)
	wtSess.mu.Unlock()
	if poolCount != 1 || allCount != 1 {
		t.Fatalf("expected 1 pooled cursor and 1 total cursor, got pool=%d, all=%d", poolCount, allCount)
	}

	// 2. Verify Scan cursor pooling and release
	for loop := 0; loop < 5; loop++ {
		cur, err := tx2.Scan("items")
		if err != nil {
			t.Fatalf("Scan loop %d failed: %v", loop, err)
		}
		count := 0
		for cur.Next() {
			count++
		}
		if count != 10 {
			t.Fatalf("expected 10 records, got %d", count)
		}
		if err := cur.Close(); err != nil {
			t.Fatalf("cur.Close failed: %v", err)
		}
	}

	// Still only 1 cursor allocated in total across all 5 scans because it was reset and reused!
	wtSess.mu.Lock()
	poolCount = len(wtSess.cursorPool["table:items"])
	allCount = len(wtSess.allCursors)
	wtSess.mu.Unlock()
	if poolCount != 1 || allCount != 1 {
		t.Fatalf("expected 1 pooled and 1 total cursor after sequential scans, got pool=%d, all=%d", poolCount, allCount)
	}

	// 3. Verify nested scans on the same table
	curOuter, err := tx2.Scan("items")
	if err != nil {
		t.Fatalf("curOuter Scan failed: %v", err)
	}
	defer curOuter.Close()

	nestedCount := 0
	for curOuter.Next() {
		curInner, err := tx2.Scan("items")
		if err != nil {
			t.Fatalf("curInner Scan failed: %v", err)
		}
		for curInner.Next() {
			nestedCount++
		}
		if err := curInner.Close(); err != nil {
			t.Fatalf("curInner.Close failed: %v", err)
		}
	}
	if nestedCount != 100 {
		t.Fatalf("expected 100 nested iterations, got %d", nestedCount)
	}
	curOuter.Close()

	// Both outer and inner cursors should now be pooled
	wtSess.mu.Lock()
	poolCount = len(wtSess.cursorPool["table:items"])
	allCount = len(wtSess.allCursors)
	wtSess.mu.Unlock()
	if poolCount != 2 || allCount != 2 {
		t.Fatalf("expected 2 pooled cursors after nested scans, got pool=%d, all=%d", poolCount, allCount)
	}
}

func TestWiredTiger_PointLookupAndScanConcurrency(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_conc_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("eng.Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "bal", Type: storage.TypeInt, Nullable: false},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	initSess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	tx, err := initSess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := 1; i <= 50; i++ {
		_ = tx.Insert("accounts", storage.Record{
			"id":  storage.IntValue(int64(i)),
			"bal": storage.IntValue(int64(i * 100)),
		})
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	_ = initSess.Close()

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			s, err := eng.NewSession()
			if err != nil {
				t.Errorf("worker %d NewSession error: %v", workerID, err)
				return
			}
			defer s.Close()

			for iter := 0; iter < 20; iter++ {
				txi, err := s.BeginTx(storage.TxOptions{ReadOnly: true})
				if err != nil {
					t.Errorf("worker %d BeginTx error: %v", workerID, err)
					return
				}
				id := int64((iter % 50) + 1)
				rec, err := txi.Get("accounts", storage.IntValue(id))
				if err != nil || rec == nil {
					t.Errorf("worker %d Get error: %v", workerID, err)
					_ = txi.Rollback()
					return
				}
				cur, err := txi.Scan("accounts")
				if err != nil {
					t.Errorf("worker %d Scan error: %v", workerID, err)
					_ = txi.Rollback()
					return
				}
				cnt := 0
				for cur.Next() {
					cnt++
				}
				_ = cur.Close()
				if cnt != 50 {
					t.Errorf("worker %d expected 50 rows, got %d", workerID, cnt)
				}
				_ = txi.Rollback()
			}
		}(w)
	}
	wg.Wait()
}

func BenchmarkWiredTiger_PointGet(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "wt_bench_get_*")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		b.Fatalf("eng.Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "bench_tbl",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar, Nullable: false},
		},
	}
	_ = eng.CreateTable(schema)

	sess, _ := eng.NewSession()
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	for i := 1; i <= 100; i++ {
		_ = tx.Insert("bench_tbl", storage.Record{
			"id":  storage.IntValue(int64(i)),
			"val": storage.StringValue("benchmark_payload_value"),
		})
	}
	_ = tx.Commit()

	readTx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := storage.IntValue(int64((i % 100) + 1))
		_, _ = readTx.Get("bench_tbl", key)
	}
}

func BenchmarkWiredTiger_Scan(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "wt_bench_scan_*")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		b.Fatalf("eng.Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "bench_tbl",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar, Nullable: false},
		},
	}
	_ = eng.CreateTable(schema)

	sess, _ := eng.NewSession()
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	for i := 1; i <= 100; i++ {
		_ = tx.Insert("bench_tbl", storage.Record{
			"id":  storage.IntValue(int64(i)),
			"val": storage.StringValue("benchmark_payload_value"),
		})
	}
	_ = tx.Commit()

	readTx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cur, err := readTx.Scan("bench_tbl")
		if err != nil {
			b.Fatalf("Scan failed: %v", err)
		}
		for cur.Next() {
			_, _ = cur.Row()
		}
		_ = cur.Close()
	}
}

func TestWiredTiger_TruncateTable(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_truncate_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_users_name", TableName: "users", Columns: []string{"name"}},
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
	for i := 1; i <= 10; i++ {
		err = tx.Insert("users", storage.Record{
			"id":   storage.IntValue(int64(i)),
			"name": storage.StringValue(fmt.Sprintf("user_%d", i)),
		})
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify rows exist
	txRead, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx read failed: %v", err)
	}
	cur, err := txRead.Scan("users")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	count := 0
	for cur.Next() {
		count++
	}
	_ = cur.Close()
	_ = txRead.Rollback()

	if count != 10 {
		t.Fatalf("expected 10 rows, got %d", count)
	}

	// Now truncate table while sess is still alive (and holds cached cursors in its pool)
	if err := eng.TruncateTable("users"); err != nil {
		t.Fatalf("TruncateTable failed: %v", err)
	}

	// Verify table is empty
	txCheck, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx check failed: %v", err)
	}
	curCheck, err := txCheck.Scan("users")
	if err != nil {
		t.Fatalf("Scan after truncate failed: %v", err)
	}
	countAfter := 0
	for curCheck.Next() {
		countAfter++
	}
	_ = curCheck.Close()
	_ = txCheck.Rollback()

	if countAfter != 0 {
		t.Fatalf("expected 0 rows after truncate, got %d", countAfter)
	}

	// Now test TRUNCATE while an active transaction with uncommitted writes exists
	txOpen, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	err = txOpen.Insert("users", storage.Record{
		"id":   storage.IntValue(100),
		"name": storage.StringValue("uncommitted"),
	})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Attempt TruncateTable while txOpen is active
	truncErr := eng.TruncateTable("users")
	t.Logf("TruncateTable while uncommitted tx active returned: %v", truncErr)
	_ = txOpen.Rollback()
}

func TestWiredTiger_DuplicatePrimaryKey_ErrKeyExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_dup_pk_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	defer eng.Close()

	tbl := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(tbl); err != nil {
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

	err = tx.Insert("items", storage.Record{
		"id":  storage.IntValue(42),
		"val": storage.StringValue("initial"),
	})
	if err != nil {
		t.Fatalf("first Insert failed: %v", err)
	}

	// Attempt second insert with duplicate key 42
	dupErr := tx.Insert("items", storage.Record{
		"id":  storage.IntValue(42),
		"val": storage.StringValue("duplicate"),
	})
	if dupErr == nil {
		t.Fatalf("expected error on duplicate PK insert, got nil")
	}
	if !errors.Is(dupErr, storage.ErrKeyExists) {
		t.Fatalf("expected storage.ErrKeyExists, got: %v", dupErr)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify only the initial row is stored
	txRead, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx read failed: %v", err)
	}
	defer txRead.Rollback()

	rec, err := txRead.Get("items", storage.IntValue(42))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if rec["val"].SVal != "initial" {
		t.Fatalf("expected 'initial', got %q", rec["val"].SVal)
	}
}

func TestWiredTiger_SessionCloseAfterEngineClose_NoPanic(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_teardown_race_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Open multiple sessions concurrently
	sessions := make([]storage.Session, 10)
	for i := 0; i < len(sessions); i++ {
		sess, err := eng.NewSession()
		if err != nil {
			t.Fatalf("NewSession failed: %v", err)
		}
		sessions[i] = sess
	}

	// Close engine first while sessions are still open
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Ensure closing sessions AFTER eng.Close() is completely safe (no SIGSEGV / panic)
	for i, sess := range sessions {
		if err := sess.Close(); err != nil {
			t.Errorf("sess[%d].Close failed unexpectedly: %v", i, err)
		}
	}
}

func TestWiredTiger_AddColumn(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_add_col_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	tbl := &storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "title", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(tbl); err != nil {
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
	if err := tx.Insert("products", storage.Record{
		"id":    storage.IntValue(100),
		"title": storage.StringValue("Laptop"),
	}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Add new column 'price' FLOAT
	newCol := &storage.Column{
		Name:     "price",
		Type:     storage.TypeFloat,
		Nullable: true,
	}
	if err := eng.AddColumn("products", newCol); err != nil {
		t.Fatalf("AddColumn failed: %v", err)
	}

	// Verify schema in engine
	tSchema, err := eng.GetTable("products")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	if len(tSchema.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(tSchema.Columns))
	}

	// Read existing row
	tx2, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx 2 failed: %v", err)
	}
	defer tx2.Rollback()

	rec, err := tx2.Get("products", storage.IntValue(100))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if rec["title"].String() != "Laptop" {
		t.Fatalf("expected title 'Laptop', got %v", rec["title"])
	}
	if !rec["price"].IsNull {
		t.Fatalf("expected NULL price for existing row, got %v", rec["price"])
	}
}

func TestWiredTiger_SecondaryUniqueIndexEnforcement(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_unique_idx_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	tbl := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:    "idx_users_email",
				Columns: []string{"email"},
				Unique:  true,
			},
		},
	}
	if err := eng.CreateTable(tbl); err != nil {
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
	if err := tx.Insert("users", storage.Record{
		"id":    storage.IntValue(1),
		"email": storage.StringValue("alice@example.com"),
	}); err != nil {
		t.Fatalf("Insert 1 failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit 1 failed: %v", err)
	}

	// Insert duplicate email with different id -> must fail with ErrUniqueViolation
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 2 failed: %v", err)
	}
	defer tx2.Rollback()
	err = tx2.Insert("users", storage.Record{
		"id":    storage.IntValue(2),
		"email": storage.StringValue("alice@example.com"),
	})
	if err == nil {
		t.Fatalf("expected unique constraint violation on secondary index, got nil")
	}
}

func TestWiredTiger_ForeignKeyIntegrityAndCascade(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_fk_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	authorTbl := &storage.TableSchema{
		Name: "authors",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(authorTbl); err != nil {
		t.Fatalf("CreateTable authors failed: %v", err)
	}

	bookTbl := &storage.TableSchema{
		Name: "books",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "author_id", Type: storage.TypeInt},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_book_author",
				ParentTable:   "authors",
				ChildColumns:  []string{"author_id"},
				ParentColumns: []string{"id"},
				OnDelete:      storage.ActionCascade,
			},
		},
	}
	if err := eng.CreateTable(bookTbl); err != nil {
		t.Fatalf("CreateTable books failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// 1. Invalid foreign key insertion
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx1.Insert("books", storage.Record{"id": storage.IntValue(1), "author_id": storage.IntValue(999)}); err == nil {
		t.Fatalf("expected FK violation for author_id=999, got nil")
	}
	tx1.Rollback()

	// 2. Insert valid author and valid book
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx2.Insert("authors", storage.Record{"id": storage.IntValue(10), "name": storage.StringValue("Tolkien")}); err != nil {
		t.Fatalf("Insert author failed: %v", err)
	}
	if err := tx2.Insert("books", storage.Record{"id": storage.IntValue(100), "author_id": storage.IntValue(10)}); err != nil {
		t.Fatalf("Insert book failed: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 3. Delete author -> cascades delete to book
	tx3, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx3.Delete("authors", storage.IntValue(10)); err != nil {
		t.Fatalf("Delete author failed: %v", err)
	}
	if err := tx3.Commit(); err != nil {
		t.Fatalf("Commit delete author failed: %v", err)
	}

	// 4. Verify book is also deleted
	tx4, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx4.Rollback()
	if _, err := tx4.Get("books", storage.IntValue(100)); err == nil {
		t.Fatalf("expected book 100 to be cascaded deleted, but it still exists")
	}
}

