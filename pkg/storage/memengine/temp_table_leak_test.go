package memengine_test

import (
	"errors"
	"testing"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestBugTempTablePersistLeak_MemEngine(t *testing.T) {
	dir := t.TempDir()

	// 1. Open engine with RDB + AOF enabled
	eng1 := memengine.New()
	if err := eng1.Open(dir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Create regular table
	if err := eng1.CreateTable(&storage.TableSchema{
		Name: "perm_users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}); err != nil {
		t.Fatalf("CreateTable perm_users failed: %v", err)
	}

	// Create session temporary table and table variable
	if err := eng1.CreateTable(&storage.TableSchema{
		Name: "__temp_sess1_scratch",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}); err != nil {
		t.Fatalf("CreateTable __temp_sess1_scratch failed: %v", err)
	}

	if err := eng1.CreateTable(&storage.TableSchema{
		Name: "__tv_sess1_var",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}); err != nil {
		t.Fatalf("CreateTable __tv_sess1_var failed: %v", err)
	}

	// Insert into both permanent and temporary tables
	sess, err := eng1.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx.Insert("perm_users", storage.Record{"id": storage.IntValue(1), "name": storage.StringValue("Alice")}); err != nil {
		t.Fatalf("Insert perm_users failed: %v", err)
	}
	if err := tx.Insert("__temp_sess1_scratch", storage.Record{"id": storage.IntValue(99)}); err != nil {
		t.Fatalf("Insert temp table failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Trigger AOF rewrite / snapshot save and close
	if err := eng1.RewriteAOF(); err != nil {
		t.Fatalf("RewriteAOF failed: %v", err)
	}
	if err := eng1.Close(); err != nil {
		t.Fatalf("Close eng1 failed: %v", err)
	}

	// 2. Reopen engine from the same directory (simulating server restart)
	eng2 := memengine.New()
	if err := eng2.Open(dir); err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	defer eng2.Close()

	// Permanent table must exist
	if _, err := eng2.GetTable("perm_users"); err != nil {
		t.Fatalf("expected perm_users to survive restart, got err: %v", err)
	}

	// Temporary tables must NOT leak across restarts
	if _, err := eng2.GetTable("__temp_sess1_scratch"); !errors.Is(err, storage.ErrTableNotFound) {
		t.Fatalf("expected __temp_sess1_scratch to be absent after restart, got err: %v", err)
	}
	if _, err := eng2.GetTable("__tv_sess1_var"); !errors.Is(err, storage.ErrTableNotFound) {
		t.Fatalf("expected __tv_sess1_var to be absent after restart, got err: %v", err)
	}

	// 3. Verify ApplyWALRecord also filters out temporary tables
	err = eng2.ApplyWALRecord(memengine.WALRecord{
		LSN: 999,
		Ops: []memengine.WALOp{
			{
				Type:  memengine.OpCreateTable,
				Table: "__temp_replayed_leak",
				Schema: &storage.TableSchema{
					Name: "__temp_replayed_leak",
					Columns: []storage.Column{
						{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ApplyWALRecord failed: %v", err)
	}
	if _, err := eng2.GetTable("__temp_replayed_leak"); !errors.Is(err, storage.ErrTableNotFound) {
		t.Fatalf("expected __temp_replayed_leak to be filtered out during WAL replay, got err: %v", err)
	}
}
