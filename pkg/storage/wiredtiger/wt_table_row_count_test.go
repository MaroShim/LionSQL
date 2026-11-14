//go:build cgo && wiredtiger

package wiredtiger_test

import (
	"errors"
	"path/filepath"
	"testing"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/wiredtiger"
)

func TestWiredTiger_TableRowCount(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wt_row_count")
	eng := wiredtiger.New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	// Non-existent table should return ErrTableNotFound
	_, err := eng.TableRowCount("unknown_tbl")
	if !errors.Is(err, storage.ErrTableNotFound) {
		t.Fatalf("expected ErrTableNotFound, got %v", err)
	}

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	cnt, err := eng.TableRowCount("items")
	if err != nil {
		t.Fatalf("TableRowCount failed: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("expected 0 rows initially, got %d", cnt)
	}

	// Insert 10 rows in a committed transaction
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := int64(1); i <= 10; i++ {
		if err := tx1.Insert("items", storage.Record{
			"id":  storage.IntValue(i),
			"val": storage.StringValue("test"),
		}); err != nil {
			t.Fatalf("Insert %d failed: %v", i, err)
		}
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	cnt, err = eng.TableRowCount("items")
	if err != nil {
		t.Fatalf("TableRowCount failed: %v", err)
	}
	if cnt != 10 {
		t.Fatalf("expected 10 rows after commit, got %d", cnt)
	}

	// Insert 5 rows in an uncommitted / rolled back transaction
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := int64(11); i <= 15; i++ {
		_ = tx2.Insert("items", storage.Record{
			"id":  storage.IntValue(i),
			"val": storage.StringValue("rolled_back"),
		})
	}
	_ = tx2.Rollback()

	cnt, err = eng.TableRowCount("items")
	if err != nil {
		t.Fatalf("TableRowCount failed: %v", err)
	}
	if cnt != 10 {
		t.Fatalf("expected still 10 rows after rollback, got %d", cnt)
	}

	// Delete 3 rows
	tx3, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := int64(1); i <= 3; i++ {
		if err := tx3.Delete("items", storage.IntValue(i)); err != nil {
			t.Fatalf("Delete %d failed: %v", i, err)
		}
	}
	if err := tx3.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	cnt, err = eng.TableRowCount("items")
	if err != nil {
		t.Fatalf("TableRowCount failed: %v", err)
	}
	if cnt != 7 {
		t.Fatalf("expected 7 rows after delete, got %d", cnt)
	}

	// Truncate table
	if err := eng.TruncateTable("items"); err != nil {
		t.Fatalf("TruncateTable failed: %v", err)
	}

	cnt, err = eng.TableRowCount("items")
	if err != nil {
		t.Fatalf("TableRowCount failed: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("expected 0 rows after truncate, got %d", cnt)
	}
}
