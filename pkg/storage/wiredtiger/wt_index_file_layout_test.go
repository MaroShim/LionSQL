//go:build cgo && wiredtiger

package wiredtiger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lionsql/pkg/storage"
)

func listIndexLayoutFiles(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	files := make(map[string]bool, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".wt") {
			files[e.Name()] = true
		}
	}
	return files
}

func scanIndexPKs(t *testing.T, sess storage.Session, table, index string, desc bool) []int64 {
	t.Helper()
	tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()
	var cur storage.Cursor
	if desc {
		cur, err = tx.IndexScanDesc(table, index, nil, nil)
	} else {
		cur, err = tx.IndexScan(table, index, nil, nil)
	}
	if err != nil {
		t.Fatalf("IndexScan(%s) failed: %v", index, err)
	}
	defer cur.Close()
	var pks []int64
	for cur.Next() {
		row, err := cur.Row()
		if err != nil {
			t.Fatalf("Row failed: %v", err)
		}
		pks = append(pks, row.Values[0].IVal)
	}
	return pks
}

// TestIndexFileLayout_OneIndexFilePerTable verifies the ARCH-WT-FILE-LAYOUT-1 invariant: a table owns
// <table>.wt plus, once it has indexes, exactly one _idx_<table>.wt regardless of the index count;
// only the first CREATE INDEX creates a file and DROP INDEX never removes one (DROP TABLE does).
func TestIndexFileLayout_OneIndexFilePerTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db")
	eng := New()
	if err := eng.Open(dbPath); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "a", Type: storage.TypeInt},
			{Name: "b", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	// Lazy index file: an index-less table owns only its data file.
	before := listIndexLayoutFiles(t, dbPath)
	if !before["orders.wt"] || before["_idx_orders.wt"] {
		t.Fatalf("expected only orders.wt after CREATE TABLE without indexes, got %v", before)
	}

	names := []string{"ix_a", "ix_b", "ix_ab"}
	cols := map[string][]string{"ix_a": {"a"}, "ix_b": {"b"}, "ix_ab": {"a", "b"}}
	var afterFirst map[string]bool
	for i, name := range names {
		if err := eng.CreateIndex(&storage.IndexSchema{Name: name, TableName: "orders", Columns: cols[name]}); err != nil {
			t.Fatalf("CreateIndex %s failed: %v", name, err)
		}
		files := listIndexLayoutFiles(t, dbPath)
		if i == 0 {
			afterFirst = files
			if !files["_idx_orders.wt"] {
				t.Fatalf("first CREATE INDEX must create _idx_orders.wt, got %v", files)
			}
			continue
		}
		if len(files) != len(afterFirst) {
			t.Fatalf("CREATE INDEX #%d changed the file layout: first=%v now=%v", i+1, afterFirst, files)
		}
	}
	for name := range afterFirst {
		if strings.HasPrefix(name, "_idx_orders_") {
			t.Fatalf("unexpected per-index legacy file %s", name)
		}
	}

	// DROP INDEX never touches files, not even for the last index.
	for _, name := range names {
		if err := eng.DropIndex("orders", name); err != nil {
			t.Fatalf("DropIndex %s failed: %v", name, err)
		}
		if got := listIndexLayoutFiles(t, dbPath); len(got) != len(afterFirst) {
			t.Fatalf("DROP INDEX %s changed the file layout: %v", name, got)
		}
	}
	// ...and the retained file is reused by the next CREATE INDEX.
	if err := eng.CreateIndex(&storage.IndexSchema{Name: "ix_again", TableName: "orders", Columns: []string{"a"}}); err != nil {
		t.Fatalf("CreateIndex after dropping all indexes failed: %v", err)
	}

	if err := eng.DropTable("orders"); err != nil {
		t.Fatalf("DropTable failed: %v", err)
	}
	final := listIndexLayoutFiles(t, dbPath)
	if final["orders.wt"] || final["_idx_orders.wt"] {
		t.Fatalf("DROP TABLE must remove both files, got %v", final)
	}
}

// TestIndexFileLayout_NamespaceIsolation verifies that indexes multiplexed in one file never leak
// into each other (ascending and descending full scans), that DROP INDEX only removes its own
// key range and that a dropped index name can be recreated and re-backfilled.
func TestIndexFileLayout_NamespaceIsolation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db")
	eng := New()
	if err := eng.Open(dbPath); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "grp", Type: storage.TypeInt},
			{Name: "code", Type: storage.TypeInt},
		},
		Indexes: []storage.IndexSchema{
			{Name: "ix_grp", TableName: "items", Columns: []string{"grp"}},
			{Name: "ix_grp2", TableName: "items", Columns: []string{"grp"}}, // shares a name prefix with ix_grp
			{Name: "ux_code", TableName: "items", Columns: []string{"code"}, Unique: true},
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

	const n = 20
	tx, _ := sess.BeginTx(storage.TxOptions{})
	for i := int64(1); i <= n; i++ {
		if err := tx.Insert("items", storage.Record{
			"id":   storage.IntValue(i),
			"grp":  storage.IntValue(i % 3),
			"code": storage.IntValue(1000 + i),
		}); err != nil {
			t.Fatalf("Insert %d failed: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	for _, ix := range []string{"ix_grp", "ix_grp2", "ux_code"} {
		if got := scanIndexPKs(t, sess, "items", ix, false); len(got) != n {
			t.Fatalf("%s ascending scan returned %d rows, want %d (%v)", ix, len(got), n, got)
		}
		if got := scanIndexPKs(t, sess, "items", ix, true); len(got) != n {
			t.Fatalf("%s descending scan returned %d rows, want %d (%v)", ix, len(got), n, got)
		}
	}
	asc := scanIndexPKs(t, sess, "items", "ux_code", false)
	desc := scanIndexPKs(t, sess, "items", "ux_code", true)
	for i := range asc {
		if asc[i] != int64(i+1) || desc[i] != int64(n-i) {
			t.Fatalf("unique index order broken: asc=%v desc=%v", asc, desc)
		}
	}

	// Unique constraint is enforced inside the multiplexed file.
	tx2, _ := sess.BeginTx(storage.TxOptions{})
	err = tx2.Insert("items", storage.Record{"id": storage.IntValue(500), "grp": storage.IntValue(1), "code": storage.IntValue(1001)})
	_ = tx2.Rollback()
	if !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("expected ErrUniqueViolation, got %v", err)
	}

	// DROP INDEX removes only its own key range.
	if err := eng.DropIndex("items", "ix_grp"); err != nil {
		t.Fatalf("DropIndex failed: %v", err)
	}
	if got := scanIndexPKs(t, sess, "items", "ix_grp2", false); len(got) != n {
		t.Fatalf("ix_grp2 lost entries after dropping ix_grp: %d", len(got))
	}
	if got := scanIndexPKs(t, sess, "items", "ux_code", true); len(got) != n {
		t.Fatalf("ux_code lost entries after dropping ix_grp: %d", len(got))
	}
	txGone, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if _, err := txGone.IndexScan("items", "ix_grp", nil, nil); !errors.Is(err, storage.ErrIndexNotFound) {
		t.Fatalf("expected ErrIndexNotFound for dropped index, got %v", err)
	}
	_ = txGone.Rollback()

	// Re-creating the same name backfills from scratch without ghost entries.
	if err := eng.CreateIndex(&storage.IndexSchema{Name: "ix_grp", TableName: "items", Columns: []string{"grp"}}); err != nil {
		t.Fatalf("re-CreateIndex failed: %v", err)
	}
	if got := scanIndexPKs(t, sess, "items", "ix_grp", false); len(got) != n {
		t.Fatalf("re-created ix_grp has %d rows, want %d", len(got), n)
	}

	// TRUNCATE empties every index of the table.
	if err := eng.TruncateTable("items"); err != nil {
		t.Fatalf("TruncateTable failed: %v", err)
	}
	for _, ix := range []string{"ix_grp", "ix_grp2", "ux_code"} {
		if got := scanIndexPKs(t, sess, "items", ix, false); len(got) != 0 {
			t.Fatalf("%s not empty after TRUNCATE: %v", ix, got)
		}
	}
}
