//go:build cgo && wiredtiger

package wiredtiger

import (
	"testing"

	"lionsql/pkg/storage"
)

// TestWTTx_IndexErrorRollsBackPrimaryRowAndPartialIndexes verifies that when an index
// mutation fails during Insert, Update, or Delete (e.g. due to a unique key collision
// or write conflict discovered at the B-Tree cursor level after primary row mutation),
// the operation does not swallow the error and leaves zero partial mutations in the
// primary table or earlier secondary indexes.
func TestWTTx_IndexErrorRollsBackPrimaryRowAndPartialIndexes(t *testing.T) {
	dir := t.TempDir()
	eng := New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true, Nullable: false},
			{Name: "tag", Type: storage.TypeVarChar, Nullable: false},
			{Name: "email", Type: storage.TypeVarChar, Nullable: false},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_tag", TableName: "accounts", Columns: []string{"tag"}, Unique: false},
			{Name: "idx_email", TableName: "accounts", Columns: []string{"email"}, Unique: true},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// Seed row 1: (1, 'tag1', 'alice@example.com') and row 2: (2, 'tag2', 'bob@example.com')
	sessSeed, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sessSeed.Close()

	txSeed, err := sessSeed.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := txSeed.Insert("accounts", storage.Record{
		"id":    storage.IntValue(1),
		"tag":   storage.StringValue("tag1"),
		"email": storage.StringValue("alice@example.com"),
	}); err != nil {
		t.Fatalf("seed insert 1 failed: %v", err)
	}
	if err := txSeed.Insert("accounts", storage.Record{
		"id":    storage.IntValue(2),
		"tag":   storage.StringValue("tag2"),
		"email": storage.StringValue("bob@example.com"),
	}); err != nil {
		t.Fatalf("seed insert 2 failed: %v", err)
	}
	if err := txSeed.Commit(); err != nil {
		t.Fatalf("seed commit failed: %v", err)
	}

	// Inject a raw index entry on the 2nd index (idx_email) for "collision@example.com"
	// pointing to PK "1" without changing row 1, or simulate snapshot race where
	// checkUniqueIndexesLocked sees existingPK == pkStr for row 2 on update, etc.
	// Wait: if we insert a raw entry key = "idx_email\x00collision@example.com", val = "" (empty value!)
	// into the index B-Tree:
	// Look at checkUniqueIndexesLocked (line 3370):
	// `if vret := C.go_wt_cursor_get_val_raw(cur, &pValData, &valSz); vret == 0 && pValData != nil && valSz > 0`
	// If valSz == 0, checkUniqueIndexesLocked passes, and then insertIndexEntry with overwrite=false
	// (or duplicate key) or cursor error is triggered!
	// Even better: let's test BOTH a mid-loop index error on the 2nd index AND concurrent WT_ROLLBACK!
	t.Run("Insert_SecondIndexFailure_RollsBackPrimaryRowAndFirstIndex", func(t *testing.T) {
		sess, err := eng.NewSession()
		if err != nil {
			t.Fatalf("NewSession failed: %v", err)
		}
		defer sess.Close()

		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		wttx := tx.(*WTTx)

		// First valid insert in tx: row 10
		if err := tx.Insert("accounts", storage.Record{
			"id":    storage.IntValue(10),
			"tag":   storage.StringValue("tag10"),
			"email": storage.StringValue("ten@example.com"),
		}); err != nil {
			t.Fatalf("Insert row 10 failed: %v", err)
		}

		// Simulate index failure on the 2nd index ("idx_email") during Insert of row 20
		wttx.testFailIndexOp = func(op string, idxName string) error {
			if op == "insert" && idxName == "idx_email" {
				return storage.ErrUniqueViolation
			}
			return nil
		}
		err = tx.Insert("accounts", storage.Record{
			"id":    storage.IntValue(20),
			"tag":   storage.StringValue("tag20"),
			"email": storage.StringValue("twenty@example.com"),
		})
		wttx.testFailIndexOp = nil
		if err == nil {
			t.Fatalf("expected Insert of row 20 to fail on 2nd index, but it succeeded")
		}

		// Verify row 20 was rolled back from primary table inside the active transaction
		if rec, gerr := tx.Get("accounts", storage.IntValue(20)); gerr == nil && rec != nil {
			t.Fatalf("CRITICAL: primary row 20 leaked into transaction after 2nd index failure: %+v", rec)
		}

		// Verify 1st index ("idx_tag" for "tag20") was also rolled back
		tag20 := storage.StringValue("tag20")
		cur, ierr := tx.IndexScan("accounts", "idx_tag", &tag20, &tag20)
		if ierr != nil {
			t.Fatalf("IndexScan failed: %v", ierr)
		}
		if cur.Next() {
			cur.Close()
			t.Fatalf("CRITICAL: 1st index entry ('tag20') leaked into transaction after 2nd index failure")
		}
		cur.Close()

		// Row 10 must still be intact and committable
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
	})

	t.Run("Update_SecondIndexFailure_RestoresPrimaryRowAndFirstIndex", func(t *testing.T) {
		sess, err := eng.NewSession()
		if err != nil {
			t.Fatalf("NewSession failed: %v", err)
		}
		defer sess.Close()

		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		wttx := tx.(*WTTx)

		// Fail on the 2nd index ("idx_email") during Update of row 1
		wttx.testFailIndexOp = func(op string, idxName string) error {
			if op == "insert" && idxName == "idx_email" {
				return storage.ErrUniqueViolation
			}
			return nil
		}
		err = tx.Update("accounts", storage.IntValue(1), storage.Record{
			"tag":   storage.StringValue("tag1_updated"),
			"email": storage.StringValue("alice_updated@example.com"),
		})
		wttx.testFailIndexOp = nil
		if err == nil {
			t.Fatalf("expected Update of row 1 to fail on 2nd index, but it succeeded")
		}

		// Verify primary row 1 was restored to ('tag1', 'alice@example.com')
		rec, gerr := tx.Get("accounts", storage.IntValue(1))
		if gerr != nil {
			t.Fatalf("Get row 1 failed: %v", gerr)
		}
		if rec["tag"].SVal != "tag1" || rec["email"].SVal != "alice@example.com" {
			t.Fatalf("CRITICAL: primary row 1 was not restored after index failure: got tag=%q email=%q", rec["tag"].SVal, rec["email"].SVal)
		}

		// Verify 1st index ("idx_tag") does NOT contain 'tag1_updated' and DOES contain 'tag1'
		tagUpd := storage.StringValue("tag1_updated")
		curUpd, _ := tx.IndexScan("accounts", "idx_tag", &tagUpd, &tagUpd)
		if curUpd.Next() {
			curUpd.Close()
			t.Fatalf("CRITICAL: updated 1st index entry ('tag1_updated') was not rolled back")
		}
		curUpd.Close()

		tagOrig := storage.StringValue("tag1")
		curOrig, _ := tx.IndexScan("accounts", "idx_tag", &tagOrig, &tagOrig)
		if !curOrig.Next() {
			curOrig.Close()
			t.Fatalf("CRITICAL: original 1st index entry ('tag1') was lost after failed update")
		}
		curOrig.Close()

		// Verify 2nd index ("idx_email") still contains 'alice@example.com'
		emailOrig := storage.StringValue("alice@example.com")
		curEmail, _ := tx.IndexScan("accounts", "idx_email", &emailOrig, &emailOrig)
		if !curEmail.Next() {
			curEmail.Close()
			t.Fatalf("CRITICAL: original 2nd index entry ('alice@example.com') was deleted and not restored after failed update")
		}
		curEmail.Close()

		_ = tx.Rollback()
	})

	t.Run("Delete_SecondIndexFailure_PropagatesErrorAndRestoresPrimaryRowAndFirstIndex", func(t *testing.T) {
		sess, err := eng.NewSession()
		if err != nil {
			t.Fatalf("NewSession failed: %v", err)
		}
		defer sess.Close()

		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		wttx := tx.(*WTTx)

		// Fail on the 2nd index ("idx_email") during Delete of row 2
		wttx.testFailIndexOp = func(op string, idxName string) error {
			if op == "remove" && idxName == "idx_email" {
				return storage.ErrSerializationFailure
			}
			return nil
		}
		err = tx.Delete("accounts", storage.IntValue(2))
		wttx.testFailIndexOp = nil
		if err == nil {
			t.Fatalf("CRITICAL: Delete swallowed index removal error and returned nil!")
		}

		// Verify primary row 2 was restored
		rec, gerr := tx.Get("accounts", storage.IntValue(2))
		if gerr != nil || rec == nil {
			t.Fatalf("CRITICAL: primary row 2 was not restored after Delete index failure: %v", gerr)
		}
		if rec["tag"].SVal != "tag2" || rec["email"].SVal != "bob@example.com" {
			t.Fatalf("unexpected row 2 after rollback: %+v", rec)
		}

		// Verify 1st index ("idx_tag" for "tag2") was restored
		tag2 := storage.StringValue("tag2")
		curTag, _ := tx.IndexScan("accounts", "idx_tag", &tag2, &tag2)
		if !curTag.Next() {
			curTag.Close()
			t.Fatalf("CRITICAL: 1st index entry ('tag2') was not restored after Delete index failure")
		}
		curTag.Close()

		_ = tx.Rollback()
	})
}
