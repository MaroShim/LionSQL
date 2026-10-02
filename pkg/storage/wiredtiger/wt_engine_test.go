//go:build cgo && wiredtiger

package wiredtiger

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"sqlion/pkg/backup"
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
