package memengine

import (
	"errors"
	"testing"

	"lionsql/pkg/storage"
)

// TestRegression_Enterprise_CompositeUniqueIndex_AllowMultipleNulls verifies that
// composite unique indexes allow multiple rows containing NULL values without false-positive
// duplicate key constraint violations, matching SQL standard and Grafana migration requirements.
func TestRegression_Enterprise_CompositeUniqueIndex_AllowMultipleNulls(t *testing.T) {
	eng := New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "dashboard_acl",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "dashboard_id", Type: storage.TypeInt, Nullable: true},
			{Name: "user_id", Type: storage.TypeInt, Nullable: true},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:      "uqe_dashboard_acl_dashboard_id_user_id",
				TableName: "dashboard_acl",
				Columns:   []string{"dashboard_id", "user_id"},
				Unique:    true,
			},
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

	// Insert row 1 with user_id = NULL
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx tx1 failed: %v", err)
	}
	if err := tx1.Insert("dashboard_acl", storage.Record{
		"id":           storage.IntValue(1),
		"dashboard_id": storage.IntValue(-1),
		"user_id":      storage.NullValue(storage.TypeInt),
	}); err != nil {
		t.Fatalf("Insert row 1 failed: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit tx1 failed: %v", err)
	}

	// Insert row 2 with same dashboard_id = -1 and user_id = NULL
	// According to SQL standard and Grafana migrations, this MUST succeed.
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx tx2 failed: %v", err)
	}
	if err := tx2.Insert("dashboard_acl", storage.Record{
		"id":           storage.IntValue(2),
		"dashboard_id": storage.IntValue(-1),
		"user_id":      storage.NullValue(storage.TypeInt),
	}); err != nil {
		t.Fatalf("Insert row 2 with NULLs failed: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit tx2 failed (should allow duplicate NULLs): %v", err)
	}

	// Insert row 3 with non-null values
	tx3, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx tx3 failed: %v", err)
	}
	if err := tx3.Insert("dashboard_acl", storage.Record{
		"id":           storage.IntValue(3),
		"dashboard_id": storage.IntValue(100),
		"user_id":      storage.IntValue(200),
	}); err != nil {
		t.Fatalf("Insert row 3 failed: %v", err)
	}
	if err := tx3.Commit(); err != nil {
		t.Fatalf("Commit tx3 failed: %v", err)
	}

	// Insert row 4 with identical non-null values -> MUST fail with ErrUniqueViolation
	tx4, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx tx4 failed: %v", err)
	}
	err = tx4.Insert("dashboard_acl", storage.Record{
		"id":           storage.IntValue(4),
		"dashboard_id": storage.IntValue(100),
		"user_id":      storage.IntValue(200),
	})
	if err == nil {
		err = tx4.Commit()
	}
	if !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("Insert row 4 expected ErrUniqueViolation on identical non-null keys, got: %v", err)
	}
}

// TestRegression_Enterprise_TxDynamicDerivedTableVisibility verifies that temporary
// derived tables (#__dt_*) created dynamically during a transaction lifecycle are visible
// to the active transaction snapshot without throwing ErrTableNotFound.
func TestRegression_Enterprise_TxDynamicDerivedTableVisibility(t *testing.T) {
	eng := New()
	_ = eng.Open("")
	defer eng.Close()

	// Initial table
	baseSchema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(baseSchema); err != nil {
		t.Fatalf("CreateTable baseSchema failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// Start long-running transaction (takes snapTables)
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx tx failed: %v", err)
	}
	defer tx.Rollback()

	// Dynamically create temporary derived table during execution
	dtSchema := &storage.TableSchema{
		Name: "#__dt_6_max_versions",
		Columns: []storage.Column{
			{Name: "name", Type: storage.TypeVarChar, PrimaryKey: true},
			{Name: "max_version", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(dtSchema); err != nil {
		t.Fatalf("CreateTable dtSchema failed: %v", err)
	}

	// tx MUST be able to see the dynamically created derived table
	if err := tx.Insert("#__dt_6_max_versions", storage.Record{
		"name":        storage.StringValue("dashboard_v1"),
		"max_version": storage.IntValue(42),
	}); err != nil {
		t.Fatalf("tx.Insert into dynamic derived table failed: %v", err)
	}

	// Scan table via tx
	cursor, err := tx.Scan("#__dt_6_max_versions")
	if err != nil {
		t.Fatalf("tx.Scan on dynamic derived table failed: %v", err)
	}
	defer cursor.Close()

	if !cursor.Next() {
		t.Fatalf("cursor expected 1 row, got 0")
	}
	rec, err := cursor.Record()
	if err != nil {
		t.Fatalf("cursor.Record failed: %v", err)
	}
	if rec["name"].SVal != "dashboard_v1" || rec["max_version"].IVal != 42 {
		t.Fatalf("unexpected row data from derived table: %+v", rec)
	}
}
