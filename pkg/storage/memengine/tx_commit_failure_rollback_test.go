package memengine

import (
	"strings"
	"testing"

	"lionsql/pkg/storage"
)

// TestMemEngine_CommitFailure_NoPartialMutationLeak tests that if an error occurs midway through
// applying changes during Commit (e.g. secondary index insert failure or WAL failure), all partially
// applied mutations (shard entries, partition keys, secondary indexes) are completely rolled back.
func TestMemEngine_CommitFailure_NoPartialMutationLeak(t *testing.T) {
	dataDir := t.TempDir()
	eng := New()
	opts := DefaultOptions(dataDir)
	if err := eng.OpenWithOptions(opts); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar},
			{Name: "sku", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	if err := eng.CreateIndex(&storage.IndexSchema{
		Name:      "idx_items_code",
		TableName: "items",
		Columns:   []string{"code"},
		Unique:    false,
	}); err != nil {
		t.Fatalf("CreateIndex 1 failed: %v", err)
	}
	if err := eng.CreateIndex(&storage.IndexSchema{
		Name:      "idx_items_sku",
		TableName: "items",
		Columns:   []string{"sku"},
		Unique:    false,
	}); err != nil {
		t.Fatalf("CreateIndex 2 failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// Initial seed
	txInit, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := txInit.Insert("items", storage.Record{"id": storage.IntValue(1), "code": storage.StringValue("C1"), "sku": storage.StringValue("S1")}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := txInit.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	t.Run("Midway failure in index insert must rollback shard entry and preceding indexes", func(t *testing.T) {
		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		defer tx.Rollback()

		// Update row 1 to code C2, sku S2
		if err := tx.Update("items", storage.IntValue(1), storage.Record{"id": storage.IntValue(1), "code": storage.StringValue("C2"), "sku": storage.StringValue("S2")}); err != nil {
			t.Fatalf("Update failed: %v", err)
		}

		// Inject failure on second index idx_items_sku by corrupting its skip list right before commit
		memTx := tx.(*Transaction)
		td, err := memTx.getTableLocked("items")
		if err != nil {
			t.Fatalf("getTableLocked failed: %v", err)
		}
		idxSku := td.Indexes["idx_items_sku"]
		// Set sl to nil so idxData.Insert fails with "secondary index uninitialized"
		realSL := idxSku.sl
		idxSku.sl = nil
		defer func() {
			idxSku.sl = realSL
		}()

		// Commit must fail
		err = tx.Commit()
		if err == nil {
			t.Fatalf("expected commit to fail on uninitialized index, got nil")
		}

		// Restore index skiplist
		idxSku.sl = realSL

		// Verify table state: row 1 must still have original values C1, S1
		txVerify, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatalf("BeginTx verify failed: %v", err)
		}
		defer txVerify.Rollback()

		rec, err := txVerify.Get("items", storage.IntValue(1))
		if err != nil {
			t.Fatalf("Get row 1 failed: %v", err)
		}
		if rec["code"].SVal != "C1" || rec["sku"].SVal != "S1" {
			t.Fatalf("expected row 1 to be rolled back to C1, S1; got code=%v, sku=%v", rec["code"].SVal, rec["sku"].SVal)
		}

		// Verify first index idx_items_code: must NOT have C2, and MUST have C1
		cur, err := txVerify.IndexScan("items", "idx_items_code", &storage.Value{Type: storage.TypeVarChar, SVal: "C2"}, &storage.Value{Type: storage.TypeVarChar, SVal: "C2"})
		if err != nil {
			t.Fatalf("IndexScan C2 failed: %v", err)
		}
		if cur.Next() {
			t.Fatalf("partial mutation leak: idx_items_code still contains uncommitted key C2!")
		}
		cur.Close()

		curOld, err := txVerify.IndexScan("items", "idx_items_code", &storage.Value{Type: storage.TypeVarChar, SVal: "C1"}, &storage.Value{Type: storage.TypeVarChar, SVal: "C1"})
		if err != nil {
			t.Fatalf("IndexScan C1 failed: %v", err)
		}
		if !curOld.Next() {
			t.Fatalf("original key C1 missing from idx_items_code after rollback!")
		}
		curOld.Close()
	})

	t.Run("Index restore failure during rollback must be explicitly propagated and not swallowed", func(t *testing.T) {
		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		defer tx.Rollback()

		if err := tx.Update("items", storage.IntValue(1), storage.Record{"id": storage.IntValue(1), "code": storage.StringValue("C3"), "sku": storage.StringValue("S3")}); err != nil {
			t.Fatalf("Update failed: %v", err)
		}

		memTx := tx.(*Transaction)
		td, err := memTx.getTableLocked("items")
		if err != nil {
			t.Fatalf("getTableLocked failed: %v", err)
		}
		idxSku := td.Indexes["idx_items_sku"]
		idxCode := td.Indexes["idx_items_code"]

		realSkuSL := idxSku.sl
		realCodeSL := idxCode.sl
		// Corrupt idxSku so commit fails in Phase B
		idxSku.sl = nil
		// Corrupt idxCode so rollback cannot restore old index entry
		idxCode.sl = nil
		defer func() {
			idxSku.sl = realSkuSL
			idxCode.sl = realCodeSL
		}()

		err = tx.Commit()
		if err == nil {
			t.Fatalf("expected commit to fail, got nil")
		}
		errMsg := err.Error()
		if !strings.Contains(errMsg, "critical rollback failure") || !strings.Contains(errMsg, "failed to restore index") {
			t.Fatalf("expected rollback failure to be explicitly propagated in error message, got: %s", errMsg)
		}
	})
}
