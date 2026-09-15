package memengine

import (
	"testing"

	"sqlserver/pkg/storage"
)

func createTestSchema() *storage.TableSchema {
	return &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeInt, Nullable: false},
		},
	}
}

func TestMemEngine_TableOperations(t *testing.T) {
	eng := New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := createTestSchema()
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// Duplicate table error
	if err := eng.CreateTable(schema); err == nil {
		t.Fatalf("Expected error creating duplicate table, got nil")
	}

	tbl, err := eng.GetTable("accounts")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	if tbl.Name != "accounts" {
		t.Errorf("Expected table accounts, got %s", tbl.Name)
	}

	tables, err := eng.ListTables()
	if err != nil || len(tables) != 1 {
		t.Fatalf("Expected 1 table in ListTables, got %d, err: %v", len(tables), err)
	}
}

func TestMemEngine_TransactionCommitAndRollback(t *testing.T) {
	eng := New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := createTestSchema()
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	// 1. Insert and Commit
	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	err = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"holder":  storage.StringValue("Alice"),
		"balance": storage.IntValue(1000),
	})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Verify committed row
	tx2, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	rec, err := tx2.Get("accounts", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if rec["holder"].SVal != "Alice" || rec["balance"].IVal != 1000 {
		t.Fatalf("Unexpected record data: %+v", rec)
	}
	_ = tx2.Commit()

	// 3. Test Rollback
	tx3, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	err = tx3.Insert("accounts", storage.Record{
		"id":      storage.IntValue(2),
		"holder":  storage.StringValue("Bob"),
		"balance": storage.IntValue(500),
	})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx3.Rollback(); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// 4. Verify rolled-back row does not exist
	tx4, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx4.Rollback()

	if _, err := tx4.Get("accounts", storage.IntValue(2)); err == nil {
		t.Fatalf("Expected key 2 to not exist after rollback, but was found")
	}
}
