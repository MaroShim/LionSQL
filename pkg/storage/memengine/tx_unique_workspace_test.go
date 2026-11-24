package memengine

import (
	"errors"
	"testing"

	"lionsql/pkg/storage"
)

func TestMemEngine_UniqueKeyReleaseInSameTx(t *testing.T) {
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
			{Name: "email", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	idxSchema := &storage.IndexSchema{
		Name:      "idx_users_email",
		TableName: "users",
		Columns:   []string{"email"},
		Unique:    true,
	}
	if err := eng.CreateIndex(idxSchema); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// Initial seed: (1, "alice@example.com") and (2, "bob@example.com")
	txInit, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	_ = txInit.Insert("users", storage.Record{"id": storage.IntValue(1), "email": storage.StringValue("alice@example.com")})
	_ = txInit.Insert("users", storage.Record{"id": storage.IntValue(2), "email": storage.StringValue("bob@example.com")})
	if err := txInit.Commit(); err != nil {
		t.Fatalf("Init commit failed: %v", err)
	}

	t.Run("Delete then Insert same unique key in same tx", func(t *testing.T) {
		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		defer tx.Rollback()

		// Delete row 1 (alice@example.com)
		if err := tx.Delete("users", storage.IntValue(1)); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		// Insert row 3 with the released unique key alice@example.com
		if err := tx.Insert("users", storage.Record{"id": storage.IntValue(3), "email": storage.StringValue("alice@example.com")}); err != nil {
			t.Fatalf("Insert with released unique key failed: %v", err)
		}

		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit failed for delete-then-insert unique key: %v", err)
		}
	})

	t.Run("Update away then Insert same unique key in same tx", func(t *testing.T) {
		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		defer tx.Rollback()

		// Row 3 currently has alice@example.com. Update row 3 to alice_new@example.com, releasing alice@example.com
		if err := tx.Update("users", storage.IntValue(3), storage.Record{"id": storage.IntValue(3), "email": storage.StringValue("alice_new@example.com")}); err != nil {
			t.Fatalf("Update failed: %v", err)
		}

		// Insert row 4 with the released unique key alice@example.com
		if err := tx.Insert("users", storage.Record{"id": storage.IntValue(4), "email": storage.StringValue("alice@example.com")}); err != nil {
			t.Fatalf("Insert with released unique key failed: %v", err)
		}

		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit failed for update-away-then-insert unique key: %v", err)
		}
	})

	t.Run("Update away then Update another row to original unique key in same tx", func(t *testing.T) {
		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		defer tx.Rollback()

		// Row 2 has bob@example.com. Update row 2 to bob_temp@example.com, releasing bob@example.com
		if err := tx.Update("users", storage.IntValue(2), storage.Record{"id": storage.IntValue(2), "email": storage.StringValue("bob_temp@example.com")}); err != nil {
			t.Fatalf("Update away failed: %v", err)
		}

		// Update row 4 (currently alice@example.com) to bob@example.com
		if err := tx.Update("users", storage.IntValue(4), storage.Record{"id": storage.IntValue(4), "email": storage.StringValue("bob@example.com")}); err != nil {
			t.Fatalf("Update another row to released unique key failed: %v", err)
		}

		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit failed for update-away-then-update-another unique key: %v", err)
		}
	})

	t.Run("Swap two rows unique keys via temp in same tx", func(t *testing.T) {
		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		defer tx.Rollback()

		// Row 3 currently has alice_new@example.com, Row 4 has bob@example.com
		// Swap them via temp value in the same transaction
		if err := tx.Update("users", storage.IntValue(3), storage.Record{"id": storage.IntValue(3), "email": storage.StringValue("temp@example.com")}); err != nil {
			t.Fatalf("Update row 3 to temp failed: %v", err)
		}
		if err := tx.Update("users", storage.IntValue(4), storage.Record{"id": storage.IntValue(4), "email": storage.StringValue("alice_new@example.com")}); err != nil {
			t.Fatalf("Update row 4 to alice_new failed: %v", err)
		}
		if err := tx.Update("users", storage.IntValue(3), storage.Record{"id": storage.IntValue(3), "email": storage.StringValue("bob@example.com")}); err != nil {
			t.Fatalf("Update row 3 to bob failed: %v", err)
		}

		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit failed for unique key swap: %v", err)
		}

		// Verify index lookups after swap commit
		txVerify, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		defer txVerify.Rollback()

		bobVal := storage.StringValue("bob@example.com")
		cur, err := txVerify.IndexScan("users", "idx_users_email", &bobVal, &bobVal)
		if err != nil {
			t.Fatalf("IndexScan failed: %v", err)
		}
		defer cur.Close()
		if !cur.Next() {
			t.Fatalf("expected cursor next for bob, got false")
		}
		rec, err := cur.Record()
		if err != nil {
			t.Fatalf("Record failed: %v", err)
		}
		if rec["id"].IVal != 3 {
			t.Fatalf("expected bob to belong to row 3, got: %v", rec["id"])
		}
		if cur.Next() {
			t.Fatalf("expected only 1 row for bob")
		}

		aliceVal := storage.StringValue("alice_new@example.com")
		cur2, err := txVerify.IndexScan("users", "idx_users_email", &aliceVal, &aliceVal)
		if err != nil {
			t.Fatalf("IndexScan failed: %v", err)
		}
		defer cur2.Close()
		if !cur2.Next() {
			t.Fatalf("expected cursor next for alice_new, got false")
		}
		rec2, err := cur2.Record()
		if err != nil {
			t.Fatalf("Row failed: %v", err)
		}
		if rec2["id"].IVal != 4 {
			t.Fatalf("expected alice_new to belong to row 4, got: %v", rec2["id"])
		}
		if cur2.Next() {
			t.Fatalf("expected only 1 row for alice_new")
		}
	})

	t.Run("Real collision with unreleased unique key must still fail", func(t *testing.T) {
		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		defer tx.Rollback()

		// Row 4 currently has bob@example.com and was NOT modified/released.
		// Attempting to insert row 10 with bob@example.com MUST fail
		err = tx.Insert("users", storage.Record{"id": storage.IntValue(10), "email": storage.StringValue("bob@example.com")})
		if err == nil {
			t.Fatalf("expected ErrUniqueViolation on real collision, got nil")
		}
		if !errors.Is(err, storage.ErrUniqueViolation) {
			t.Fatalf("expected ErrUniqueViolation, got: %v", err)
		}
	})
}

// TestMemEngine_CommitFailure_NoPrematureUniqueIndexDeletion verifies that if a transaction fails during
// WAL commit, its existing unique index keys were NEVER prematurely deleted from the SkipList,
// preventing concurrent transactions from stealing the uncommitted key or corrupting the index.
func TestMemEngine_CommitFailure_NoPrematureUniqueIndexDeletion(t *testing.T) {
	dataDir := t.TempDir()
	eng := New()
	opts := DefaultOptions(dataDir)
	if err := eng.OpenWithOptions(opts); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	if err := eng.CreateIndex(&storage.IndexSchema{
		Name:      "idx_accounts_email",
		TableName: "accounts",
		Columns:   []string{"email"},
		Unique:    true,
	}); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// 1. Initial insert: (1, "target@example.com")
	txInit, _ := sess.BeginTx(storage.TxOptions{})
	_ = txInit.Insert("accounts", storage.Record{"id": storage.IntValue(1), "email": storage.StringValue("target@example.com")})
	if err := txInit.Commit(); err != nil {
		t.Fatalf("Init commit failed: %v", err)
	}

	// 2. T1 updates row 1 to "target_updated@example.com"
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx1.Rollback()

	if err := tx1.Update("accounts", storage.IntValue(1), storage.Record{
		"id":    storage.IntValue(1),
		"email": storage.StringValue("target_updated@example.com"),
	}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Simulate WAL corruption/failure so T1 fails during commit
	if eng.wal != nil && eng.wal.file != nil {
		_ = eng.wal.file.Close()
	}

	// T1 commit fails
	if err := tx1.Commit(); err == nil {
		t.Fatalf("expected commit to fail on closed WAL, but got nil")
	}

	// 3. Verify that "target@example.com" was NEVER prematurely deleted:
	// A new transaction T2 attempting to insert "target@example.com" MUST fail with ErrUniqueViolation
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx T2 failed: %v", err)
	}
	defer tx2.Rollback()

	err = tx2.Insert("accounts", storage.Record{
		"id":    storage.IntValue(2),
		"email": storage.StringValue("target@example.com"),
	})
	if err == nil {
		t.Fatalf("FATAL: unique index key was prematurely deleted during failed commit! T2 was able to insert duplicate key")
	}
	if !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("expected ErrUniqueViolation on duplicate key, got: %v", err)
	}

	// Verify original row 1 still cleanly owns "target@example.com" via IndexScan
	targetVal := storage.StringValue("target@example.com")
	cur, err := tx2.IndexScan("accounts", "idx_accounts_email", &targetVal, &targetVal)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	defer cur.Close()
	if !cur.Next() {
		t.Fatalf("expected original unique key target@example.com to still exist in index")
	}
	rec, _ := cur.Record()
	if rec["id"].IVal != 1 {
		t.Fatalf("expected row 1, got %v", rec["id"])
	}
}
