package memengine

import (
	"errors"
	"os"
	"testing"

	"lionsql/pkg/storage"
)

// TestMemEngine_DDL_WALFailure_RevertsInMemoryState verifies that if WAL write fails during DDL,
// all in-memory catalog and shard state modifications are completely rolled back (fail-closed).
func TestMemEngine_DDL_WALFailure_RevertsInMemoryState(t *testing.T) {
	dataDir := t.TempDir()
	eng := New()
	opts := DefaultOptions(dataDir)
	if err := eng.OpenWithOptions(opts); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "users",
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
	_ = tx.Insert("users", storage.Record{"id": storage.IntValue(1), "name": storage.StringValue("Alice")})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Helper to make WAL append fail by closing the file
	makeWALFail := func() {
		eng.walOrderMu.Lock()
		defer eng.walOrderMu.Unlock()
		if eng.wal != nil && eng.wal.file != nil {
			_ = eng.wal.file.Close()
		}
	}

	t.Run("CreateTable WAL failure must not leave table in catalog", func(t *testing.T) {
		makeWALFail()

		newSch := &storage.TableSchema{
			Name: "orders",
			Columns: []storage.Column{
				{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			},
		}
		err := eng.CreateTable(newSch)
		if err == nil {
			t.Fatalf("expected CreateTable to fail when WAL is disabled, got nil")
		}

		eng.mu.RLock()
		_, exists := eng.tables["orders"]
		eng.mu.RUnlock()
		if exists {
			t.Fatalf("CreateTable WAL failure leaked table 'orders' into in-memory catalog!")
		}
	})

	t.Run("DropTable WAL failure must keep table in catalog", func(t *testing.T) {
		makeWALFail()

		err := eng.DropTable("users")
		if err == nil {
			t.Fatalf("expected DropTable to fail when WAL is disabled, got nil")
		}

		eng.mu.RLock()
		td, exists := eng.tables["users"]
		eng.mu.RUnlock()
		if !exists || td == nil {
			t.Fatalf("DropTable WAL failure dropped table 'users' from in-memory catalog without rollback!")
		}
	})

	t.Run("CreateIndex WAL failure must not leave index in table", func(t *testing.T) {
		makeWALFail()

		idxSch := &storage.IndexSchema{
			Name:      "idx_users_name",
			TableName: "users",
			Columns:   []string{"name"},
		}
		err := eng.CreateIndex(idxSch)
		if err == nil {
			t.Fatalf("expected CreateIndex to fail when WAL is disabled, got nil")
		}

		eng.mu.RLock()
		td := eng.tables["users"]
		_, hasIdx := td.Indexes["idx_users_name"]
		_, _, hasSchIdx := td.Schema.FindIndex("idx_users_name")
		eng.mu.RUnlock()
		if hasIdx || hasSchIdx {
			t.Fatalf("CreateIndex WAL failure leaked index into table data/schema!")
		}
	})

	t.Run("DropIndex WAL failure must keep index in table", func(t *testing.T) {
		// First restore WAL to create index successfully
		eng.walOrderMu.Lock()
		w, _ := NewWALWriter(eng.wal.path, eng.fsyncPolicy)
		eng.wal = w
		eng.walOrderMu.Unlock()

		idxSch := &storage.IndexSchema{
			Name:      "idx_users_name_ok",
			TableName: "users",
			Columns:   []string{"name"},
		}
		if err := eng.CreateIndex(idxSch); err != nil {
			t.Fatalf("CreateIndex failed: %v", err)
		}

		makeWALFail()

		err := eng.DropIndex("users", "idx_users_name_ok")
		if err == nil {
			t.Fatalf("expected DropIndex to fail when WAL is disabled, got nil")
		}

		eng.mu.RLock()
		td := eng.tables["users"]
		_, hasIdx := td.Indexes["idx_users_name_ok"]
		_, _, hasSchIdx := td.Schema.FindIndex("idx_users_name_ok")
		eng.mu.RUnlock()
		if !hasIdx || !hasSchIdx {
			t.Fatalf("DropIndex WAL failure removed index from table without rollback!")
		}
	})

	t.Run("TruncateTable WAL failure must keep table rows intact", func(t *testing.T) {
		makeWALFail()

		err := eng.TruncateTable("users")
		if err == nil {
			t.Fatalf("expected TruncateTable to fail when WAL is disabled, got nil")
		}

		// Verify row 1 still exists
		readTx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		defer readTx.Rollback()
		rec, err := readTx.Get("users", storage.IntValue(1))
		if err != nil {
			t.Fatalf("Get row 1 failed after failed truncate: %v", err)
		}
		if rec["name"].SVal != "Alice" {
			t.Fatalf("expected row 1 name=Alice, got: %v", rec["name"].SVal)
		}
	})

	t.Run("RenameTable WAL failure must keep old table name", func(t *testing.T) {
		makeWALFail()

		err := eng.RenameTable("users", "users_new")
		if err == nil {
			t.Fatalf("expected RenameTable to fail when WAL is disabled, got nil")
		}

		eng.mu.RLock()
		_, hasOld := eng.tables["users"]
		_, hasNew := eng.tables["users_new"]
		eng.mu.RUnlock()
		if !hasOld || hasNew {
			t.Fatalf("RenameTable WAL failure corrupted table names! hasOld=%v, hasNew=%v", hasOld, hasNew)
		}
	})
}

// Ensure unused import errors are prevented
var _ = errors.New
var _ = os.ErrNotExist
