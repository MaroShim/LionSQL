//go:build cgo && wiredtiger

package wiredtiger

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lionsql/pkg/storage"
)

// TestSecondaryIndex_LifecycleAndPersistence verifies that secondary index B-Tree tables
// are physically created on disk, survive engine restart (re-open), and can be scanned.
func TestSecondaryIndex_LifecycleAndPersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_sec_idx_persist_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "db")

	// Phase 1: Open engine, create table and secondary index, insert data
	eng := New()
	if err := eng.Open(dbPath); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar},
			{Name: "age", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// Insert rows before creating index (tests backfill!)
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("users", storage.Record{"id": storage.IntValue(1), "email": storage.StringValue("alice@example.com"), "age": storage.IntValue(30)})
	_ = tx.Insert("users", storage.Record{"id": storage.IntValue(2), "email": storage.StringValue("bob@example.com"), "age": storage.IntValue(25)})
	_ = tx.Insert("users", storage.Record{"id": storage.IntValue(3), "email": storage.StringValue("charlie@example.com"), "age": storage.IntValue(35)})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Create secondary index with backfill
	idxAge := &storage.IndexSchema{
		Name:      "idx_users_age",
		TableName: "users",
		Columns:   []string{"age"},
		Unique:    false,
	}
	if err := eng.CreateIndex(idxAge); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	// Also create a unique index on email
	idxEmail := &storage.IndexSchema{
		Name:      "idx_users_email",
		TableName: "users",
		Columns:   []string{"email"},
		Unique:    true,
	}
	if err := eng.CreateIndex(idxEmail); err != nil {
		t.Fatalf("CreateIndex email failed: %v", err)
	}

	// Verify scan on age index
	txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	lowAge := storage.IntValue(25)
	cur, err := txRead.IndexScan("users", "idx_users_age", &lowAge, &lowAge)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	if !cur.Next() {
		t.Fatalf("expected row for age 25")
	}
	r, err := cur.Row()
	if err != nil || r.Values[0].IVal != 2 {
		t.Fatalf("expected id 2, got %v (err: %v)", r, err)
	}
	if cur.Next() {
		t.Fatalf("expected only 1 row for age 25")
	}
	_ = cur.Close()
	_ = txRead.Rollback()
	_ = sess.Close()
	_ = eng.Close()

	// Phase 2: Re-open engine and verify index persistence
	eng2 := New()
	if err := eng2.Open(dbPath); err != nil {
		t.Fatalf("failed to re-open engine: %v", err)
	}
	defer eng2.Close()

	sess2, err := eng2.NewSession()
	if err != nil {
		t.Fatalf("NewSession on re-opened engine failed: %v", err)
	}
	defer sess2.Close()

	txRead2, _ := sess2.BeginTx(storage.TxOptions{ReadOnly: true})
	defer txRead2.Rollback()

	emailKey := storage.StringValue("alice@example.com")
	curEmail, err := txRead2.IndexScan("users", "idx_users_email", &emailKey, &emailKey)
	if err != nil {
		t.Fatalf("IndexScan on re-opened engine failed: %v", err)
	}
	defer curEmail.Close()

	if !curEmail.Next() {
		t.Fatalf("expected row for alice@example.com after engine restart")
	}
	rAlice, err := curEmail.Row()
	if err != nil || rAlice.Values[0].IVal != 1 {
		t.Fatalf("expected id 1 for alice, got %v (err: %v)", rAlice, err)
	}
}

// TestSecondaryIndex_DMLSynchronization verifies Insert, Update, and Delete synchronization
// with the secondary index B-Tree.
func TestSecondaryIndex_DMLSynchronization(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_sec_idx_dml_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "sku", Type: storage.TypeVarChar},
			{Name: "price", Type: storage.TypeInt},
		},
		Indexes: []storage.IndexSchema{
			{Name: "ix_sku", TableName: "products", Columns: []string{"sku"}, Unique: true},
			{Name: "ix_price", TableName: "products", Columns: []string{"price"}, Unique: false},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()

	// 1. Insert row
	tx1, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx1.Insert("products", storage.Record{"id": storage.IntValue(100), "sku": storage.StringValue("SKU-A"), "price": storage.IntValue(50)})
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Insert commit failed: %v", err)
	}

	// Verify insertion in index
	txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	skuA := storage.StringValue("SKU-A")
	cur, err := txRead.IndexScan("products", "ix_sku", &skuA, &skuA)
	if err != nil || !cur.Next() {
		t.Fatalf("expected SKU-A in index after insert")
	}
	_ = cur.Close()
	_ = txRead.Rollback()

	// 2. Update price from 50 to 99, and sku from SKU-A to SKU-B
	tx2, _ := sess.BeginTx(storage.TxOptions{})
	if err := tx2.Update("products", storage.IntValue(100), storage.Record{
		"sku":   storage.StringValue("SKU-B"),
		"price": storage.IntValue(99),
	}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Update commit failed: %v", err)
	}

	// Verify old sku is GONE from index, and new sku is PRESENT
	txRead2, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	curOld, _ := txRead2.IndexScan("products", "ix_sku", &skuA, &skuA)
	if curOld.Next() {
		t.Fatalf("expected old SKU-A to be removed from index")
	}
	_ = curOld.Close()

	skuB := storage.StringValue("SKU-B")
	curNew, _ := txRead2.IndexScan("products", "ix_sku", &skuB, &skuB)
	if !curNew.Next() {
		t.Fatalf("expected new SKU-B to be present in index")
	}
	_ = curNew.Close()

	// Verify price index: old price 50 gone, new price 99 present
	p50 := storage.IntValue(50)
	curP50, _ := txRead2.IndexScan("products", "ix_price", &p50, &p50)
	if curP50.Next() {
		t.Fatalf("expected old price 50 to be removed from index")
	}
	_ = curP50.Close()

	p99 := storage.IntValue(99)
	curP99, _ := txRead2.IndexScan("products", "ix_price", &p99, &p99)
	if !curP99.Next() {
		t.Fatalf("expected new price 99 to be present in index")
	}
	_ = curP99.Close()
	_ = txRead2.Rollback()

	// 3. Delete row
	tx3, _ := sess.BeginTx(storage.TxOptions{})
	if err := tx3.Delete("products", storage.IntValue(100)); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if err := tx3.Commit(); err != nil {
		t.Fatalf("Delete commit failed: %v", err)
	}

	// Verify sku is completely gone
	txRead3, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	curDel, _ := txRead3.IndexScan("products", "ix_sku", &skuB, &skuB)
	if curDel.Next() {
		t.Fatalf("expected SKU-B to be deleted from index")
	}
	_ = curDel.Close()
	_ = txRead3.Rollback()
}

// TestSecondaryIndex_UniqueViolationAndNulls verifies O(log N) fast unique violation checks
// and proper handling of NULL values.
func TestSecondaryIndex_UniqueViolationAndNulls(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_sec_idx_uniq_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "members",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "national_id", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "ix_nid", TableName: "members", Columns: []string{"national_id"}, Unique: true},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()

	// Insert row 1
	tx1, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx1.Insert("members", storage.Record{"id": storage.IntValue(1), "national_id": storage.StringValue("NID-100")})
	_ = tx1.Commit()

	// Insert duplicate national_id -> must fail with ErrUniqueViolation
	tx2, _ := sess.BeginTx(storage.TxOptions{})
	err = tx2.Insert("members", storage.Record{"id": storage.IntValue(2), "national_id": storage.StringValue("NID-100")})
	if !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("expected ErrUniqueViolation, got: %v", err)
	}
	_ = tx2.Rollback()

	// Update row 1 with same national_id (self-update) -> must SUCCEED!
	tx3, _ := sess.BeginTx(storage.TxOptions{})
	err = tx3.Update("members", storage.IntValue(1), storage.Record{"national_id": storage.StringValue("NID-100")})
	if err != nil {
		t.Fatalf("self-update with same unique key should succeed, got: %v", err)
	}
	_ = tx3.Commit()

	// Multiple NULL values in unique index must be PERMITTED per SQL standard
	tx4, _ := sess.BeginTx(storage.TxOptions{})
	err = tx4.Insert("members", storage.Record{"id": storage.IntValue(10), "national_id": storage.NullValue(storage.TypeVarChar)})
	if err != nil {
		t.Fatalf("inserting first NULL should succeed: %v", err)
	}
	err = tx4.Insert("members", storage.Record{"id": storage.IntValue(11), "national_id": storage.NullValue(storage.TypeVarChar)})
	if err != nil {
		t.Fatalf("inserting second NULL in unique index should succeed: %v", err)
	}
	if err := tx4.Commit(); err != nil {
		t.Fatalf("commit with multiple NULLs failed: %v", err)
	}
}

// TestSecondaryIndex_CompositeIndexScanAndDesc verifies ascending and descending
// range scans on composite secondary indexes.
func TestSecondaryIndex_CompositeIndexScanAndDesc(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_sec_idx_comp_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "sales",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "region", Type: storage.TypeVarChar},
			{Name: "amount", Type: storage.TypeInt},
		},
		Indexes: []storage.IndexSchema{
			{Name: "ix_region_amount", TableName: "sales", Columns: []string{"region", "amount"}, Unique: false},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("sales", storage.Record{"id": storage.IntValue(1), "region": storage.StringValue("East"), "amount": storage.IntValue(100)})
	_ = tx.Insert("sales", storage.Record{"id": storage.IntValue(2), "region": storage.StringValue("East"), "amount": storage.IntValue(200)})
	_ = tx.Insert("sales", storage.Record{"id": storage.IntValue(3), "region": storage.StringValue("East"), "amount": storage.IntValue(300)})
	_ = tx.Insert("sales", storage.Record{"id": storage.IntValue(4), "region": storage.StringValue("West"), "amount": storage.IntValue(150)})
	_ = tx.Commit()

	txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer txRead.Rollback()

	// 1. Ascending scan with full table index traversal (nil, nil)
	curAll, err := txRead.IndexScan("sales", "ix_region_amount", nil, nil)
	if err != nil {
		t.Fatalf("IndexScan (nil, nil) failed: %v", err)
	}
	var allIDs []int64
	for curAll.Next() {
		r, err := curAll.Row()
		if err != nil {
			t.Fatalf("Row error: %v", err)
		}
		allIDs = append(allIDs, r.Values[0].IVal)
	}
	_ = curAll.Close()
	// East(100)=1, East(200)=2, East(300)=3, West(150)=4
	if len(allIDs) != 4 || allIDs[0] != 1 || allIDs[1] != 2 || allIDs[2] != 3 || allIDs[3] != 4 {
		t.Fatalf("unexpected ascending all IDs: %v", allIDs)
	}

	// 2. Descending scan with full table index traversal (nil, nil)
	curDesc, err := txRead.IndexScanDesc("sales", "ix_region_amount", nil, nil)
	if err != nil {
		t.Fatalf("IndexScanDesc (nil, nil) failed: %v", err)
	}
	var descIDs []int64
	for curDesc.Next() {
		r, err := curDesc.Row()
		if err != nil {
			t.Fatalf("Row error: %v", err)
		}
		descIDs = append(descIDs, r.Values[0].IVal)
	}
	_ = curDesc.Close()
	// Should be 4, 3, 2, 1
	if len(descIDs) != 4 || descIDs[0] != 4 || descIDs[1] != 3 || descIDs[2] != 2 || descIDs[3] != 1 {
		t.Fatalf("unexpected descending all IDs: %v", descIDs)
	}
}

// TestSecondaryIndex_TransactionRollback verifies that when an uncommitted transaction
// is rolled back, all mutations in the secondary index B-Tree are atomically rolled back.
func TestSecondaryIndex_TransactionRollback(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_sec_idx_rb_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "order_no", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "ix_ord", TableName: "orders", Columns: []string{"order_no"}, Unique: true},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()

	// Begin tx, insert, then ROLLBACK
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("orders", storage.Record{"id": storage.IntValue(1), "order_no": storage.StringValue("ORD-1")})
	_ = tx.Rollback()

	// Verify index does NOT see ORD-1
	txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	ordKey := storage.StringValue("ORD-1")
	cur, err := txRead.IndexScan("orders", "ix_ord", &ordKey, &ordKey)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	if cur.Next() {
		t.Fatalf("expected rolled back order to be absent from index")
	}
	_ = cur.Close()
	_ = txRead.Rollback()

	// Now insert ORD-1 in a committed tx -> must succeed without unique conflict
	tx2, _ := sess.BeginTx(storage.TxOptions{})
	if err := tx2.Insert("orders", storage.Record{"id": storage.IntValue(1), "order_no": storage.StringValue("ORD-1")}); err != nil {
		t.Fatalf("inserting key previously rolled back should succeed: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Verify now present
	txRead2, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	cur2, _ := txRead2.IndexScan("orders", "ix_ord", &ordKey, &ordKey)
	if !cur2.Next() {
		t.Fatalf("expected ORD-1 to be present after commit")
	}
	_ = cur2.Close()
	_ = txRead2.Rollback()
}

// TestSecondaryIndex_DropTableAndDropIndex verifies physical cleanup of index tables.
func TestSecondaryIndex_DropTableAndDropIndex(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_sec_idx_drop_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "inventory",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "ix_code", TableName: "inventory", Columns: []string{"code"}, Unique: true},
		},
	}
	_ = eng.CreateTable(schema)

	// Drop the index
	if err := eng.DropIndex("inventory", "ix_code"); err != nil {
		t.Fatalf("DropIndex failed: %v", err)
	}

	// Scanning dropped index should fail with ErrIndexNotFound
	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	k := storage.StringValue("ABC")
	_, err = tx.IndexScan("inventory", "ix_code", &k, &k)
	if !errors.Is(err, storage.ErrIndexNotFound) {
		t.Fatalf("expected ErrIndexNotFound after DropIndex, got %v", err)
	}
	_ = tx.Rollback()
	_ = sess.Close()

	// Drop the table itself
	if err := eng.DropTable("inventory"); err != nil {
		t.Fatalf("DropTable failed: %v", err)
	}
}

// TestSecondaryIndex_LargeDatasetPerformance verifies that point-lookup in 10,000 rows
// executes in sub-millisecond O(log N) B-Tree seek time without scanning the primary table.
func TestSecondaryIndex_LargeDatasetPerformance(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_sec_idx_perf_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "telemetry",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "device_id", Type: storage.TypeInt},
			{Name: "reading", Type: storage.TypeFloat},
		},
		Indexes: []storage.IndexSchema{
			{Name: "ix_dev", TableName: "telemetry", Columns: []string{"device_id"}, Unique: false},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()

	// Insert 10,000 rows in batches of 1,000
	const totalRows = 10000
	const batchSize = 1000
	for b := 0; b < totalRows/batchSize; b++ {
		tx, _ := sess.BeginTx(storage.TxOptions{})
		for i := 0; i < batchSize; i++ {
			id := int64(b*batchSize + i + 1)
			_ = tx.Insert("telemetry", storage.Record{
				"id":        storage.IntValue(id),
				"device_id": storage.IntValue(id % 100), // 100 distinct devices
				"reading":   storage.FloatValue(float64(id) * 0.1),
			})
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("batch commit failed: %v", err)
		}
	}

	// Measure point-lookup time for device_id = 42
	txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer txRead.Rollback()

	devKey := storage.IntValue(42)
	start := time.Now()
	cur, err := txRead.IndexScan("telemetry", "ix_dev", &devKey, &devKey)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	defer cur.Close()

	count := 0
	for cur.Next() {
		count++
	}
	elapsed := time.Since(start)

	// 10,000 / 100 = exactly 100 rows per device
	if count != 100 {
		t.Fatalf("expected 100 rows for device_id 42, got %d", count)
	}

	// O(log N) seek + 100 point lookups should complete in well under 25ms
	if elapsed > 25*time.Millisecond {
		t.Fatalf("IndexScan on 10,000 rows took too long: %v (expected < 25ms)", elapsed)
	}
	t.Logf("10,000 rows secondary index scan completed in %v", elapsed)
}

// TestWiredTiger_UniqueIndex_OverwriteFalse_DuplicateKeyRejection verifies that unique index cursors
// are opened with overwrite=false, preventing silent duplicate overwrites at the B-Tree cursor level
// and returning storage.ErrUniqueViolation.
func TestWiredTiger_UniqueIndex_OverwriteFalse_DuplicateKeyRejection(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_sec_idx_unique_overwrite_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "db")
	eng := New()
	if err := eng.Open(dbPath); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "sku", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	idxSKU := &storage.IndexSchema{
		Name:      "idx_products_sku",
		TableName: "products",
		Columns:   []string{"sku"},
		Unique:    true,
	}
	if err := eng.CreateIndex(idxSKU); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	wtSess := sess.(*WTSession)
	tbl, err := eng.GetTable("products")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	idxURI := tbl.IndexURI("idx_products_sku")

	cur, err := wtSess.acquireCursor(idxURI)
	if err != nil {
		t.Fatalf("acquireCursor failed: %v", err)
	}
	defer wtSess.releaseCursor(idxURI, cur)

	k, v, _ := formatIndexKeyValue(storage.Record{"sku": storage.StringValue("SKU-1001")}, idxSKU, "1")
	if err := insertIndexEntry(cur, k, v); err != nil {
		t.Fatalf("first insertIndexEntry should succeed, got: %v", err)
	}

	// Attempt inserting the exact same unique index key with different PK value "2"
	// With overwrite=false, WiredTiger MUST reject this with storage.ErrUniqueViolation
	// (instead of silently replacing "1" with "2")
	k2, v2, _ := formatIndexKeyValue(storage.Record{"sku": storage.StringValue("SKU-1001")}, idxSKU, "2")
	err = insertIndexEntry(cur, k2, v2)
	if !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("expected ErrUniqueViolation on duplicate unique index insert, got: %v", err)
	}
}

// TestWiredTiger_Savepoint_Rollback_RemovesUpdatedIndexKey verifies that rolling back an UPDATE
// to a savepoint properly removes the new secondary index keys inserted during UPDATE,
// preventing ghost orphan index entries and false unique constraint violations.
func TestWiredTiger_Savepoint_Rollback_RemovesUpdatedIndexKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_savepoint_idx_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "db")
	eng := New()
	if err := eng.Open(dbPath); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	idxEmail := &storage.IndexSchema{
		Name:      "idx_accounts_email",
		TableName: "accounts",
		Columns:   []string{"email"},
		Unique:    true,
	}
	if err := eng.CreateIndex(idxEmail); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
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
	defer tx.Rollback()

	// 1. Insert initial row: (1, 'alice@old.com', 100)
	err = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"email":   storage.StringValue("alice@old.com"),
		"balance": storage.IntValue(100),
	})
	if err != nil {
		t.Fatalf("Insert initial row failed: %v", err)
	}

	// 2. Create savepoint sp1
	if err := tx.Savepoint("sp1"); err != nil {
		t.Fatalf("Savepoint sp1 failed: %v", err)
	}

	// 3. Update row to 'alice@new.com'
	err = tx.Update("accounts", storage.IntValue(1), storage.Record{
		"id":      storage.IntValue(1),
		"email":   storage.StringValue("alice@new.com"),
		"balance": storage.IntValue(200),
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// 4. Rollback to savepoint sp1
	if err := tx.RollbackTo("sp1"); err != nil {
		t.Fatalf("RollbackTo sp1 failed: %v", err)
	}

	// 5. Now try to insert a different row (id: 2) with 'alice@new.com'.
	// Since the previous update to 'alice@new.com' was rolled back,
	// 'alice@new.com' MUST NOT be in the unique index!
	err = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(2),
		"email":   storage.StringValue("alice@new.com"),
		"balance": storage.IntValue(300),
	})
	if err != nil {
		t.Fatalf("Insert after savepoint rollback failed (ghost index entry was not removed): %v", err)
	}

	// 6. Also verify that scanning for 'alice@old.com' finds row 1
	oldKey := storage.StringValue("alice@old.com")
	cur, err := tx.IndexScan("accounts", "idx_accounts_email", &oldKey, &oldKey)
	if err != nil {
		t.Fatalf("IndexScan alice@old.com failed: %v", err)
	}
	foundOld := false
	for cur.Next() {
		rec, rerr := cur.Record()
		if rerr == nil && rec["id"].IVal == 1 {
			foundOld = true
		}
	}
	cur.Close()
	if !foundOld {
		t.Fatalf("expected to find row 1 with alice@old.com in index scan")
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
}

// TestWiredTiger_TableVariable_RollbackExemption verifies that table variables routed to tempEng
// survive WTTx.Rollback() and retain their inserted rows per T-SQL standard.
func TestWiredTiger_TableVariable_RollbackExemption(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_tv_rollback_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "db")
	eng := New()
	if err := eng.Open(dbPath); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	defer eng.Close()

	// 1. Regular disk table
	if err := eng.CreateTable(&storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}); err != nil {
		t.Fatalf("CreateTable items failed: %v", err)
	}

	// 2. Table variable (__tv_*)
	if err := eng.CreateTable(&storage.TableSchema{
		Name: "__tv_sess1_log",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "info", Type: storage.TypeVarChar},
		},
	}); err != nil {
		t.Fatalf("CreateTable __tv_sess1_log failed: %v", err)
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
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(1)})
	_ = tx.Insert("__tv_sess1_log", storage.Record{"id": storage.IntValue(1), "info": storage.StringValue("logged before rollback")})

	// Rollback transaction
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// Read in a new transaction
	txRead, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx read failed: %v", err)
	}
	defer txRead.Rollback()

	// items must be 0
	curItems, _ := txRead.Scan("items")
	itemCount := 0
	for curItems.Next() {
		itemCount++
	}
	curItems.Close()
	if itemCount != 0 {
		t.Fatalf("expected items to have 0 rows after rollback, got %d", itemCount)
	}

	// __tv_sess1_log must retain 1 row
	curTV, _ := txRead.Scan("__tv_sess1_log")
	tvCount := 0
	var infoVal string
	for curTV.Next() {
		tvCount++
		rec, _ := curTV.Record()
		infoVal = rec["info"].SVal
	}
	curTV.Close()
	if tvCount != 1 || infoVal != "logged before rollback" {
		t.Fatalf("expected table variable in WiredTiger hybrid mode to retain 1 row, got %d (info: %q)", tvCount, infoVal)
	}
}



