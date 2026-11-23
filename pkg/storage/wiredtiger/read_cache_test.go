//go:build cgo && wiredtiger

package wiredtiger

import (
	"path/filepath"
	"testing"

	"lionsql/pkg/storage"
)

func TestWiredTiger_ReadCache_RepeatedLookupsAndMutations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wt_read_cache_test")
	eng := New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "balance", Type: storage.TypeInt},
			{Name: "holder", Type: storage.TypeVarChar},
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

	// 1. Insert initial row
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	wtTx := tx.(*WTTx)

	rec1 := storage.Record{
		"id":      storage.IntValue(100),
		"balance": storage.IntValue(5000),
		"holder":  storage.StringValue("alice"),
	}
	if err := wtTx.Insert("accounts", rec1); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Verify insertion populated read cache
	if wtTx.readCache.Len() != 1 {
		t.Fatalf("expected readCache len 1 after insert, got %d", wtTx.readCache.Len())
	}

	// 2. Cache Hit on Get and GetRow
	recGot, err := wtTx.Get("accounts", storage.IntValue(100))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if recGot["holder"].String() != "alice" {
		t.Fatalf("expected holder=alice, got %v", recGot["holder"])
	}

	rowGot, err := wtTx.GetRow("accounts", storage.IntValue(100))
	if err != nil {
		t.Fatalf("GetRow failed: %v", err)
	}
	if len(rowGot.Values) != 3 {
		t.Fatalf("expected 3 row values, got %d", len(rowGot.Values))
	}

	// 3. Update mutates cache
	updRec := storage.Record{
		"balance": storage.IntValue(7500),
	}
	if err := wtTx.Update("accounts", storage.IntValue(100), updRec); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	recAfterUpd, err := wtTx.Get("accounts", storage.IntValue(100))
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if recAfterUpd["balance"].IVal != 7500 {
		t.Fatalf("expected updated balance 7500, got %d", recAfterUpd["balance"].IVal)
	}

	// 4. Savepoint rollback clears read cache
	if err := wtTx.Savepoint("sp1"); err != nil {
		t.Fatalf("Savepoint failed: %v", err)
	}
	rec2 := storage.Record{
		"id":      storage.IntValue(200),
		"balance": storage.IntValue(1000),
		"holder":  storage.StringValue("bob"),
	}
	if err := wtTx.Insert("accounts", rec2); err != nil {
		t.Fatalf("Insert rec2 failed: %v", err)
	}
	if wtTx.readCache.Len() < 2 {
		t.Fatalf("expected readCache to have at least 2 entries, got %d", wtTx.readCache.Len())
	}

	if err := wtTx.RollbackTo("sp1"); err != nil {
		t.Fatalf("RollbackTo sp1 failed: %v", err)
	}
	if wtTx.readCache.Len() != 0 {
		t.Fatalf("expected readCache to be cleared after savepoint rollback, got %d", wtTx.readCache.Len())
	}

	// 5. Delete invalidates cache
	rec1Again, err := wtTx.Get("accounts", storage.IntValue(100))
	if err != nil {
		t.Fatalf("Get rec1 again failed: %v", err)
	}
	if rec1Again["balance"].IVal != 7500 {
		t.Fatalf("expected balance 7500, got %d", rec1Again["balance"].IVal)
	}
	if wtTx.readCache.Len() != 1 {
		t.Fatalf("expected readCache len 1, got %d", wtTx.readCache.Len())
	}

	if err := wtTx.Delete("accounts", storage.IntValue(100)); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if wtTx.readCache.Len() != 0 {
		t.Fatalf("expected readCache len 0 after delete, got %d", wtTx.readCache.Len())
	}

	if err := wtTx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	if wtTx.readCache.Len() != 0 {
		t.Fatalf("expected readCache len 0 after commit, got %d", wtTx.readCache.Len())
	}
}
