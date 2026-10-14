//go:build cgo && wiredtiger

package wiredtiger

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"sqlion/pkg/backup"
	"sqlion/pkg/catalog"
	"sqlion/pkg/engine"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
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

