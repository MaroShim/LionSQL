package memengine

import (
	"fmt"
	"testing"
	"time"

	"sqlion/pkg/storage"
)

func TestVacuum_TombstoneReclamation(t *testing.T) {
	eng := New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
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

	// 1. Insert 5 items
	tx, _ := sess.BeginTx(storage.TxOptions{})
	for i := 1; i <= 5; i++ {
		_ = tx.Insert("items", storage.Record{
			"id":   storage.IntValue(int64(i)),
			"name": storage.StringValue(fmt.Sprintf("item_%d", i)),
		})
	}
	_ = tx.Commit()

	// 2. Delete 2 items
	txDel, _ := sess.BeginTx(storage.TxOptions{})
	_ = txDel.Delete("items", storage.IntValue(2))
	_ = txDel.Delete("items", storage.IntValue(4))
	_ = txDel.Commit()

	// Check that tombstones were recorded
	eng.mu.RLock()
	td := eng.tables["items"]
	tombstoneCount := len(td.Tombstones)
	eng.mu.RUnlock()

	if tombstoneCount != 2 {
		t.Fatalf("Expected 2 tombstones recorded, got %d", tombstoneCount)
	}

	// 3. Trigger Vacuum sweep
	stats, err := eng.Vacuum()
	if err != nil {
		t.Fatalf("Vacuum failed: %v", err)
	}

	if stats.PurgedTombstones != 2 {
		t.Fatalf("Expected 2 purged tombstones in vacuum stats, got %d", stats.PurgedTombstones)
	}

	// Check table tombstones are empty
	eng.mu.RLock()
	remaining := len(eng.tables["items"].Tombstones)
	eng.mu.RUnlock()

	if remaining != 0 {
		t.Fatalf("Expected 0 remaining tombstones after vacuum, got %d", remaining)
	}
}

func TestVacuum_ActiveTxWatermark(t *testing.T) {
	eng := New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt, Nullable: false},
		},
	}
	_ = eng.CreateTable(schema)

	sess, _ := eng.NewSession()
	defer sess.Close()

	// Initial row
	txInit, _ := sess.BeginTx(storage.TxOptions{})
	_ = txInit.Insert("accounts", storage.Record{"id": storage.IntValue(1), "val": storage.IntValue(100)})
	_ = txInit.Commit()

	// Start long-running active read transaction TxLong
	txLong, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})

	// Wait slightly to ensure deletion timestamp is strictly after txLong start
	time.Sleep(5 * time.Millisecond)

	// Now delete row in another transaction
	txMut, _ := sess.BeginTx(storage.TxOptions{})
	_ = txMut.Delete("accounts", storage.IntValue(1))
	_ = txMut.Commit()

	// Run Vacuum while txLong is still active: tombstone must NOT be purged
	stats, _ := eng.Vacuum()
	if stats.PurgedTombstones != 0 {
		t.Fatalf("Tombstone should not be purged while older active transaction exists, got purged: %d", stats.PurgedTombstones)
	}

	eng.mu.RLock()
	tombstoneCount := len(eng.tables["accounts"].Tombstones)
	eng.mu.RUnlock()
	if tombstoneCount != 1 {
		t.Fatalf("Tombstone must still be preserved, got %d", tombstoneCount)
	}

	// Complete txLong
	_ = txLong.Rollback()

	// Run Vacuum again: now that txLong is closed, tombstone must be purged
	statsAfter, _ := eng.Vacuum()
	if statsAfter.PurgedTombstones != 1 {
		t.Fatalf("Expected 1 purged tombstone after active tx closed, got %d", statsAfter.PurgedTombstones)
	}

	eng.mu.RLock()
	finalCount := len(eng.tables["accounts"].Tombstones)
	eng.mu.RUnlock()
	if finalCount != 0 {
		t.Fatalf("Expected 0 tombstones after final vacuum, got %d", finalCount)
	}
}

func TestMemCursor_ZeroCopyScanCorrectness(t *testing.T) {
	eng := New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "score", Type: storage.TypeInt, Nullable: false},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_score", TableName: "users", Columns: []string{"score"}},
		},
	}
	_ = eng.CreateTable(schema)

	sess, _ := eng.NewSession()
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	for i := 1; i <= 100; i++ {
		_ = tx.Insert("users", storage.Record{
			"id":    storage.IntValue(int64(i)),
			"score": storage.IntValue(int64(i * 10)),
		})
	}
	_ = tx.Commit()

	// Sequential Scan
	txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer txRead.Rollback()

	cur, err := txRead.Scan("users")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	defer cur.Close()

	count := 0
	for cur.Next() {
		r, err := cur.Record()
		if err != nil {
			t.Fatalf("Record() error: %v", err)
		}
		if r == nil {
			t.Fatalf("Record() returned nil")
		}
		count++
	}
	if count != 100 {
		t.Fatalf("Expected 100 records from Scan, got %d", count)
	}

	// Index Scan
	low := storage.IntValue(200)
	high := storage.IntValue(400)
	idxCur, err := txRead.IndexScan("users", "idx_score", &low, &high)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	defer idxCur.Close()

	idxCount := 0
	for idxCur.Next() {
		r, _ := idxCur.Record()
		score := r["score"].IVal
		if score < 200 || score > 400 {
			t.Errorf("Score out of range: %d", score)
		}
		idxCount++
	}
	if idxCount != 21 {
		t.Fatalf("Expected 21 records in index range [200, 400], got %d", idxCount)
	}
}

func BenchmarkMemCursor_Scan(b *testing.B) {
	eng := New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "bench_scan",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt, Nullable: false},
		},
	}
	_ = eng.CreateTable(schema)

	sess, _ := eng.NewSession()
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	for i := 1; i <= 1000; i++ {
		_ = tx.Insert("bench_scan", storage.Record{
			"id":  storage.IntValue(int64(i)),
			"val": storage.IntValue(int64(i * 2)),
		})
	}
	_ = tx.Commit()

	txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer txRead.Rollback()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cur, _ := txRead.Scan("bench_scan")
		for cur.Next() {
			_, _ = cur.Record()
		}
		_ = cur.Close()
	}
}
