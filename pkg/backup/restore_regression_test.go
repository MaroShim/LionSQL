package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// createBackupArchive creates an in-memory tar.gz backup archive from provided schemas and record maps.
func createBackupArchive(t *testing.T, tables []*storage.TableSchema, records map[string][]storage.Record, corruptTable string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// 1. Write schemas.json
	schemaBytes, err := json.Marshal(tables)
	if err != nil {
		t.Fatalf("marshal schemas error: %v", err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:    "schemas.json",
		Mode:    0600,
		Size:    int64(len(schemaBytes)),
		ModTime: time.Now(),
	}); err != nil {
		t.Fatalf("write header schemas.json: %v", err)
	}
	if _, err := tw.Write(schemaBytes); err != nil {
		t.Fatalf("write schemas.json: %v", err)
	}

	// 2. Write data/<tableName>.jsonl
	for _, tbl := range tables {
		var dataBuf bytes.Buffer
		recs := records[tbl.Name]
		for i, rec := range recs {
			if tbl.Name == corruptTable && i == len(recs)-1 {
				// Inject corrupted line
				dataBuf.WriteString("{invalid json syntax !!!\n")
				break
			}
			b, err := json.Marshal(rec)
			if err != nil {
				t.Fatalf("marshal rec error: %v", err)
			}
			dataBuf.Write(b)
			dataBuf.WriteByte('\n')
		}

		dataBytes := dataBuf.Bytes()
		entryName := fmt.Sprintf("data/%s.jsonl", tbl.Name)
		if err := tw.WriteHeader(&tar.Header{
			Name:    entryName,
			Mode:    0600,
			Size:    int64(len(dataBytes)),
			ModTime: time.Now(),
		}); err != nil {
			t.Fatalf("write header %s: %v", entryName, err)
		}
		if _, err := tw.Write(dataBytes); err != nil {
			t.Fatalf("write %s: %v", entryName, err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}

	return &buf
}

// TestRestore_Regression_P0_16_RollbackOnFailure verifies that if RestoreBackup fails
// midway (e.g. corrupt record in table B), any pre-existing database tables and records
// are completely restored to their original state and not left in a half-restored broken state.
func TestRestore_Regression_P0_16_RollbackOnFailure(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("open memengine error: %v", err)
	}
	cat := catalog.New(eng)

	// Step 1: Create initial existing database with table "orig_users" (20 rows)
	origSchema := &storage.TableSchema{
		Name: "orig_users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
		},
	}
	if err := eng.CreateTable(origSchema); err != nil {
		t.Fatalf("create orig_users error: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("session error: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx error: %v", err)
	}
	for i := 1; i <= 20; i++ {
		if err := tx.Insert("orig_users", storage.Record{
			"id":   storage.IntValue(int64(i)),
			"name": storage.StringValue(fmt.Sprintf("orig_user_%d", i)),
		}); err != nil {
			t.Fatalf("insert error: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit error: %v", err)
	}

	// Step 2: Prepare a restore archive containing new_table1 (valid) and new_table2 (corrupt)
	t1Schema := &storage.TableSchema{
		Name: "new_table1",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar, Nullable: false},
		},
	}
	t2Schema := &storage.TableSchema{
		Name: "new_table2",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar, Nullable: false},
		},
	}

	records := map[string][]storage.Record{
		"new_table1": {
			{"id": storage.IntValue(1), "val": storage.StringValue("row1")},
			{"id": storage.IntValue(2), "val": storage.StringValue("row2")},
		},
		"new_table2": {
			{"id": storage.IntValue(10), "val": storage.StringValue("corrupt_test")},
		},
	}

	// Archive with corruption in new_table2
	corruptArchive := createBackupArchive(t, []*storage.TableSchema{t1Schema, t2Schema}, records, "new_table2")

	// Step 3: Attempt restore with DropExisting = true
	opts := DefaultRestoreOptions()
	opts.DropExisting = true

	_, restoreErr := RestoreBackup(corruptArchive, eng, cat, opts)
	if restoreErr == nil {
		t.Fatalf("[P0-16] Expected restore error on corrupt archive, got nil")
	}
	t.Logf("[P0-16] Expected restore error observed: %v", restoreErr)

	// Step 4: Verify rollback integrity!
	// 4a. The original table "orig_users" MUST exist and have all 20 rows!
	origTable, err := eng.GetTable("orig_users")
	if err != nil {
		t.Fatalf("[P0-16 DEFECT CONFIRMED] Original table 'orig_users' was permanently lost/dropped during failed restore: %v", err)
	}
	if origTable == nil {
		t.Fatalf("[P0-16 DEFECT CONFIRMED] Original table 'orig_users' is nil after failed restore!")
	}

	rTx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read tx error: %v", err)
	}
	defer rTx.Rollback()

	cur, err := rTx.Scan("orig_users")
	if err != nil {
		t.Fatalf("scan orig_users error: %v", err)
	}
	defer cur.Close()

	var rowCount int
	for cur.Next() {
		rowCount++
	}
	if rowCount != 20 {
		t.Fatalf("[P0-16 DEFECT CONFIRMED] Expected 20 original rows in 'orig_users', but found %d!", rowCount)
	}

	// 4b. Partially restored tables ("new_table1", "new_table2") MUST NOT exist!
	if _, err := eng.GetTable("new_table1"); err == nil {
		t.Fatalf("[P0-16 DEFECT CONFIRMED] Partially restored table 'new_table1' was left behind in the database!")
	}
	if _, err := eng.GetTable("new_table2"); err == nil {
		t.Fatalf("[P0-16 DEFECT CONFIRMED] Partially restored table 'new_table2' was left behind in the database!")
	}

	t.Logf("[P0-16 PASS] Database state was atomically rolled back to pre-restore snapshot upon failure.")
}

// TestRestore_Regression_P0_17_ForeignKeyTopologicalSort verifies that tables with
// foreign key dependencies are restored in topological order (parents before children),
// even if the archive lists child tables before parent tables.
func TestRestore_Regression_P0_17_ForeignKeyTopologicalSort(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("open memengine error: %v", err)
	}
	cat := catalog.New(eng)

	// Define Parent: "customers", Child: "orders", Grandchild: "order_items"
	customersSchema := &storage.TableSchema{
		Name: "customers",
		Columns: []storage.Column{
			{Name: "customer_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
		},
	}
	ordersSchema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "order_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "customer_id", Type: storage.TypeInt, Nullable: false},
			{Name: "total", Type: storage.TypeFloat, Nullable: false},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_orders_customer",
				ChildTable:    "orders",
				ChildColumns:  []string{"customer_id"},
				ParentTable:   "customers",
				ParentColumns: []string{"customer_id"},
			},
		},
	}
	orderItemsSchema := &storage.TableSchema{
		Name: "order_items",
		Columns: []storage.Column{
			{Name: "item_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "order_id", Type: storage.TypeInt, Nullable: false},
			{Name: "product", Type: storage.TypeVarChar, Nullable: false},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_items_order",
				ChildTable:    "order_items",
				ChildColumns:  []string{"order_id"},
				ParentTable:   "orders",
				ParentColumns: []string{"order_id"},
			},
		},
	}

	records := map[string][]storage.Record{
		"customers": {
			{"customer_id": storage.IntValue(1), "name": storage.StringValue("Alice")},
			{"customer_id": storage.IntValue(2), "name": storage.StringValue("Bob")},
		},
		"orders": {
			{"order_id": storage.IntValue(100), "customer_id": storage.IntValue(1), "total": storage.FloatValue(50.0)},
			{"order_id": storage.IntValue(101), "customer_id": storage.IntValue(2), "total": storage.FloatValue(75.5)},
		},
		"order_items": {
			{"item_id": storage.IntValue(1001), "order_id": storage.IntValue(100), "product": storage.StringValue("Book")},
			{"item_id": storage.IntValue(1002), "order_id": storage.IntValue(101), "product": storage.StringValue("Pen")},
		},
	}

	// Deliberately REVERSE order in archive: child first, parent last!
	// order_items -> orders -> customers
	reversedTables := []*storage.TableSchema{orderItemsSchema, ordersSchema, customersSchema}
	archive := createBackupArchive(t, reversedTables, records, "")

	// Attempt restore
	res, err := RestoreBackup(archive, eng, cat, DefaultRestoreOptions())
	if err != nil {
		t.Fatalf("[P0-17 DEFECT CONFIRMED] Restore failed due to lack of FK topological sorting: %v", err)
	}

	if res.TablesRestored != 3 {
		t.Fatalf("expected 3 tables restored, got %d", res.TablesRestored)
	}
	if res.RecordsRestored != 6 {
		t.Fatalf("expected 6 records restored, got %d", res.RecordsRestored)
	}
	t.Logf("[P0-17 PASS] Successfully restored 3 FK-dependent tables in topological order!")
}
