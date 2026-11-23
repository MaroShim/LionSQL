//go:build cgo && wiredtiger

package wiredtiger

import (
	"os"
	"testing"

	"lionsql/pkg/storage"
)

func TestWiredTiger_InsertRow_Direct_ParityAndConstraints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_insert_row_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("eng.Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar, Nullable: false},
			{Name: "score", Type: storage.TypeInt, Nullable: true},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_users_email", Columns: []string{"email"}, Unique: true},
			{Name: "idx_users_score", Columns: []string{"score"}, Unique: false},
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
		t.Fatalf("Begin failed: %v", err)
	}

	// 1. InsertRow success
	row1 := storage.Row{
		Values: []storage.Value{
			storage.IntValue(1),
			storage.StringValue("alice@example.com"),
			storage.IntValue(100),
		},
	}
	if err := tx.InsertRow("users", row1); err != nil {
		t.Fatalf("InsertRow row1 failed: %v", err)
	}

	// Verify GetRow returns identical data
	gotRow1, err := tx.GetRow("users", storage.IntValue(1))
	if err != nil {
		t.Fatalf("GetRow failed: %v", err)
	}
	if len(gotRow1.Values) != 3 || gotRow1.Values[0].IVal != 1 || gotRow1.Values[1].String() != "alice@example.com" {
		t.Fatalf("unexpected gotRow1: %v", gotRow1)
	}

	// 2. Duplicate Primary Key via InsertRow -> ErrKeyExists
	rowDupPK := storage.Row{
		Values: []storage.Value{
			storage.IntValue(1),
			storage.StringValue("other@example.com"),
			storage.IntValue(200),
		},
	}
	if err := tx.InsertRow("users", rowDupPK); err == nil {
		t.Fatalf("expected ErrKeyExists on duplicate PK, got nil")
	}

	// 3. Duplicate Unique Index via InsertRow -> ErrUniqueViolation
	rowDupEmail := storage.Row{
		Values: []storage.Value{
			storage.IntValue(2),
			storage.StringValue("alice@example.com"),
			storage.IntValue(300),
		},
	}
	if err := tx.InsertRow("users", rowDupEmail); err == nil {
		t.Fatalf("expected ErrUniqueViolation on duplicate email, got nil")
	}

	// 4. Valid row 2
	row2 := storage.Row{
		Values: []storage.Value{
			storage.IntValue(2),
			storage.StringValue("bob@example.com"),
			storage.IntValue(100), // non-unique index allows duplicate scores
		},
	}
	if err := tx.InsertRow("users", row2); err != nil {
		t.Fatalf("InsertRow row2 failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
}

func BenchmarkWiredTiger_InsertRecord(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "wt_bench_insert_rec_*")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		b.Fatalf("eng.Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "bench_insert_rec",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "val", Type: storage.TypeInt, Nullable: false},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		b.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		b.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		b.Fatalf("Begin failed: %v", err)
	}
	defer tx.Rollback()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		rec := storage.Record{
			"id":   storage.IntValue(int64(i + 1)),
			"name": storage.StringValue("benchmark_user"),
			"val":  storage.IntValue(int64(i * 10)),
		}
		if err := tx.Insert("bench_insert_rec", rec); err != nil {
			b.Fatalf("Insert failed: %v", err)
		}
	}
}

func BenchmarkWiredTiger_InsertRow(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "wt_bench_insert_row_*")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(tmpDir); err != nil {
		b.Fatalf("eng.Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "bench_insert",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "val", Type: storage.TypeInt, Nullable: false},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		b.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		b.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		b.Fatalf("Begin failed: %v", err)
	}
	defer tx.Rollback()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		row := storage.Row{
			Values: []storage.Value{
				storage.IntValue(int64(i + 1)),
				storage.StringValue("benchmark_user"),
				storage.IntValue(int64(i * 10)),
			},
		}
		if err := tx.InsertRow("bench_insert", row); err != nil {
			b.Fatalf("InsertRow failed: %v", err)
		}
	}
}
