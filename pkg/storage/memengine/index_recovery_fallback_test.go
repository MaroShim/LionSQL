package memengine

import (
	"errors"
	"fmt"
	"testing"

	"lionsql/pkg/storage"
)

func TestSecondaryIndex_AutoRebuildOnCorruptedSnapshotEntries(t *testing.T) {
	dataDir := t.TempDir()
	eng := New()
	opts := DefaultOptions(dataDir)
	if err := eng.OpenWithOptions(opts); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	// 1. Create table and secondary index
	schema := &storage.TableSchema{
		Name: "employees",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "dept", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	idxSchema := &storage.IndexSchema{
		Name:      "idx_emp_dept",
		TableName: "employees",
		Columns:   []string{"dept"},
	}
	if err := eng.CreateIndex(idxSchema); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	// 2. Insert records
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	_ = tx.Insert("employees", storage.Record{"id": storage.IntValue(1), "name": storage.StringValue("Alice"), "dept": storage.StringValue("Engineering")})
	_ = tx.Insert("employees", storage.Record{"id": storage.IntValue(2), "name": storage.StringValue("Bob"), "dept": storage.StringValue("Engineering")})
	_ = tx.Insert("employees", storage.Record{"id": storage.IntValue(3), "name": storage.StringValue("Charlie"), "dept": storage.StringValue("Sales")})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 3. Corrupt index entries in memory to simulate corrupted snapshot entries
	td := eng.tables["employees"]
	idxData := td.Indexes["idx_emp_dept"]
	idxData.sl = nil // wipe skiplist
	idxData.initErr = errors.New("corrupted snapshot entries")

	// 4. Invoke restore logic on td
	err = td.restoreIndexesLocked()
	if err != nil {
		t.Fatalf("restoreIndexesLocked returned unexpected error: %v", err)
	}

	// 5. Verify index was auto-rebuilt from table rows and is NOT disabled
	if idxData.Schema.Disabled {
		t.Errorf("expected idxData.Schema.Disabled to be false after auto-rebuild")
	}
	foundIdx, _, ok := td.Schema.FindIndex("idx_emp_dept")
	if !ok || foundIdx.Disabled {
		t.Errorf("expected Schema.FindIndex to find non-disabled index")
	}

	// 6. Verify index scan works and returns Engineering employees
	rtx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx ReadOnly failed: %v", err)
	}
	defer rtx.Rollback()

	deptKey := storage.StringValue("Engineering")
	cur, err := rtx.IndexScan("employees", "idx_emp_dept", &deptKey, &deptKey)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	defer cur.Close()

	var count int
	for cur.Next() {
		count++
	}
	if cur.Err() != nil {
		t.Fatalf("cursor error: %v", cur.Err())
	}
	if count != 2 {
		t.Fatalf("expected 2 Engineering employees from index scan, got %d", count)
	}
}

func TestSecondaryIndex_DisableAndFallbackToTableScanOnRebuildFailure(t *testing.T) {
	dataDir := t.TempDir()
	eng := New()
	opts := DefaultOptions(dataDir)
	if err := eng.OpenWithOptions(opts); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	// 1. Create table with unique secondary index
	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	idxSchema := &storage.IndexSchema{
		Name:      "idx_users_email",
		TableName: "users",
		Columns:   []string{"email"},
		Unique:    true,
	}
	if err := eng.CreateIndex(idxSchema); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	// 2. Insert rows
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	_ = tx.Insert("users", storage.Record{"id": storage.IntValue(1), "email": storage.StringValue("test@example.com")})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 3. Inject a duplicate row directly into table data bypassing unique check to simulate table-level anomaly
	td := eng.tables["users"]
	row2 := td.Schema.RecordToRow(storage.Record{"id": storage.IntValue(2), "email": storage.StringValue("test@example.com")})
	td.putRowAndVer("2", row2, 1)

	// Corrupt index entries
	idxData := td.Indexes["idx_users_email"]
	idxData.sl = nil
	idxData.initErr = errors.New("corrupted snapshot entries")

	// 4. Invoke restore logic on td.
	// Auto-rebuild will encounter duplicate key "test@example.com" on row 2, failing unique insertion!
	_ = td.restoreIndexesLocked()

	// 5. Verify index is marked Disabled
	if !idxData.Schema.Disabled {
		t.Errorf("expected idxData.Schema.Disabled to be true when rebuild fails")
	}
	foundIdx, _, ok := td.Schema.FindIndex("idx_users_email")
	if !ok {
		t.Fatalf("expected Schema.FindIndex to locate index metadata")
	}
	if !foundIdx.Disabled {
		t.Errorf("expected Schema.Indexes disabled flag to be true")
	}
	snap := td.SnapshotSchema()
	foundSnapIdx, _, snapOk := snap.FindIndex("idx_users_email")
	if !snapOk || !foundSnapIdx.Disabled {
		t.Errorf("expected td.SnapshotSchema() to also reflect Disabled: true after auto-rebuild failure")
	}

	// 6. Verify FindIndexByColumn skips the disabled index
	if _, found := td.Schema.FindIndexByColumn("email"); found {
		t.Errorf("expected FindIndexByColumn to skip disabled index")
	}

	// 7. Verify tx.IndexScan on disabled index returns ErrIndexDisabled
	rtx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer rtx.Rollback()

	emailKey := storage.StringValue("test@example.com")
	_, err = rtx.IndexScan("users", "idx_users_email", &emailKey, &emailKey)
	if err == nil {
		t.Fatalf("expected IndexScan to fail on disabled index, got nil error")
	}
	if !errors.Is(err, storage.ErrIndexDisabled) && err.Error() != fmt.Sprintf("index %q on table users is disabled", "idx_users_email") {
		t.Fatalf("expected ErrIndexDisabled or disabled message, got: %v", err)
	}

	// 8. Verify TableScan works and finds BOTH matching rows
	cur, err := rtx.Scan("users")
	if err != nil {
		t.Fatalf("TableScan failed: %v", err)
	}
	defer cur.Close()

	var matchingCount int
	for cur.Next() {
		rec, _ := cur.Record()
		if rec["email"].String() == "test@example.com" {
			matchingCount++
		}
	}
	if matchingCount != 2 {
		t.Fatalf("expected table scan to return 2 matching rows, got %d", matchingCount)
	}
}
