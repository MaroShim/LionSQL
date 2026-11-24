package storage_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/storage/wiredtiger"
)

func runBatchInsertTestsOnEngine(t *testing.T, eng storage.Engine) {
	schema := &storage.TableSchema{
		Name: "bulk_users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "email", Type: storage.TypeVarChar, Nullable: false},
			{Name: "age", Type: storage.TypeInt, Nullable: false},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_name", Columns: []string{"name"}},
			{Name: "idx_email", Columns: []string{"email"}, Unique: true},
			{Name: "idx_age", Columns: []string{"age"}},
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

	// 1. Batch insert 500 rows
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	count := 500
	records := make([]storage.Record, count)
	for i := 0; i < count; i++ {
		records[i] = storage.Record{
			"id":    storage.IntValue(int64(i + 1)),
			"name":  storage.StringValue(fmt.Sprintf("User_%04d", count-i)), // out of order to test index sorting
			"email": storage.StringValue(fmt.Sprintf("user_%04d@test.com", i+1)),
			"age":   storage.IntValue(int64(20 + (i % 50))),
		}
	}

	if err := tx.BatchInsert("bulk_users", records); err != nil {
		t.Fatalf("BatchInsert failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Verify row count via Scan
	tx2, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx 2 failed: %v", err)
	}

	cur, err := tx2.Scan("bulk_users")
	if err != nil {
		tx2.Rollback()
		t.Fatalf("Scan failed: %v", err)
	}
	scannedCount := 0
	for cur.Next() {
		scannedCount++
	}
	cur.Close()

	if scannedCount != count {
		tx2.Rollback()
		t.Fatalf("expected %d scanned rows, got %d", count, scannedCount)
	}

	// 3. Verify secondary index lookup
	emailVal := storage.StringValue("user_0250@test.com")
	idxCur, err := tx2.IndexScan("bulk_users", "idx_email", &emailVal, &emailVal)
	if err != nil {
		tx2.Rollback()
		t.Fatalf("IndexScan failed: %v", err)
	}
	idxCount := 0
	for idxCur.Next() {
		idxCount++
		r, err := idxCur.Record()
		if err != nil {
			idxCur.Close()
			tx2.Rollback()
			t.Fatalf("idxCur.Record failed: %v", err)
		}
		if r["id"].IVal != 250 {
			t.Errorf("expected id 250 from index scan, got %v", r["id"])
		}
	}
	idxCur.Close()

	if idxCount != 1 {
		t.Errorf("expected 1 row from unique index scan, got %d", idxCount)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatalf("tx2.Rollback failed: %v", err)
	}

	// 4. Verify duplicate PK in batch fails
	tx3, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 3 failed: %v", err)
	}
	dupRecords := []storage.Record{
		{"id": storage.IntValue(1001), "name": storage.StringValue("A"), "email": storage.StringValue("a@t.com"), "age": storage.IntValue(25)},
		{"id": storage.IntValue(1001), "name": storage.StringValue("B"), "email": storage.StringValue("b@t.com"), "age": storage.IntValue(26)},
	}
	if err := tx3.BatchInsert("bulk_users", dupRecords); err == nil {
		tx3.Rollback()
		t.Fatalf("expected duplicate key error in batch, got nil")
	} else {
		tx3.Rollback()
	}
}

func TestStorage_BatchInsert_MemEngine(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open memengine failed: %v", err)
	}
	defer eng.Close()

	runBatchInsertTestsOnEngine(t, eng)
}

func TestStorage_BatchInsert_WiredTiger(t *testing.T) {
	dir, err := os.MkdirTemp("", "wt_batch_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)

	eng := wiredtiger.New()
	if err := eng.Open(dir); err != nil {
		if strings.Contains(err.Error(), "not enabled in this build") {
			t.Skip("wiredtiger storage engine not enabled in this build")
		}
		t.Fatalf("Open wiredtiger failed: %v", err)
	}
	defer eng.Close()

	runBatchInsertTestsOnEngine(t, eng)
}

func BenchmarkStorage_BatchInsert_MemEngine(b *testing.B) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		b.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "bench_batch_users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "email", Type: storage.TypeVarChar, Nullable: false},
			{Name: "age", Type: storage.TypeInt, Nullable: false},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_batch_email", Columns: []string{"email"}, Unique: true},
			{Name: "idx_batch_age", Columns: []string{"age"}},
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

	batchSize := 1000
	records := make([]storage.Record, batchSize)
	for i := 0; i < batchSize; i++ {
		records[i] = storage.Record{
			"id":    storage.IntValue(int64(i + 1)),
			"name":  storage.StringValue(fmt.Sprintf("User_%05d", i+1)),
			"email": storage.StringValue(fmt.Sprintf("user_%05d@test.com", i+1)),
			"age":   storage.IntValue(int64(20 + (i % 50))),
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	totalRows := 0
	for i := 0; i < b.N; i++ {
		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			b.Fatalf("BeginTx failed: %v", err)
		}
		// Offset IDs to avoid collision across iterations
		for j := 0; j < batchSize; j++ {
			rowID := int64(totalRows + j + 1)
			records[j]["id"] = storage.IntValue(rowID)
			records[j]["email"] = storage.StringValue(fmt.Sprintf("user_%08d@test.com", rowID))
		}
		if err := tx.BatchInsert("bench_batch_users", records); err != nil {
			tx.Rollback()
			b.Fatalf("BatchInsert failed: %v", err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatalf("Commit failed: %v", err)
		}
		totalRows += batchSize
	}
	b.ReportMetric(float64(totalRows)/b.Elapsed().Seconds(), "rows/sec")
}

func BenchmarkStorage_BatchInsert_WiredTiger(b *testing.B) {
	dir, err := os.MkdirTemp("", "wt_batch_bench_*")
	if err != nil {
		b.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)

	eng := wiredtiger.New()
	if err := eng.Open(dir); err != nil {
		if strings.Contains(err.Error(), "not enabled in this build") {
			b.Skip("wiredtiger storage engine not enabled in this build")
		}
		b.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "bench_batch_users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "email", Type: storage.TypeVarChar, Nullable: false},
			{Name: "age", Type: storage.TypeInt, Nullable: false},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_batch_email", Columns: []string{"email"}, Unique: true},
			{Name: "idx_batch_age", Columns: []string{"age"}},
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

	batchSize := 1000
	records := make([]storage.Record, batchSize)
	for i := 0; i < batchSize; i++ {
		records[i] = storage.Record{
			"id":    storage.IntValue(int64(i + 1)),
			"name":  storage.StringValue(fmt.Sprintf("User_%05d", i+1)),
			"email": storage.StringValue(fmt.Sprintf("user_%05d@test.com", i+1)),
			"age":   storage.IntValue(int64(20 + (i % 50))),
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	totalRows := 0
	for i := 0; i < b.N; i++ {
		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			b.Fatalf("BeginTx failed: %v", err)
		}
		for j := 0; j < batchSize; j++ {
			rowID := int64(totalRows + j + 1)
			records[j]["id"] = storage.IntValue(rowID)
			records[j]["email"] = storage.StringValue(fmt.Sprintf("user_%08d@test.com", rowID))
		}
		if err := tx.BatchInsert("bench_batch_users", records); err != nil {
			tx.Rollback()
			b.Fatalf("BatchInsert failed: %v", err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatalf("Commit failed: %v", err)
		}
		totalRows += batchSize
	}
	b.ReportMetric(float64(totalRows)/b.Elapsed().Seconds(), "rows/sec")
}

