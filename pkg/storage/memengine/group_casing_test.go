package memengine

import (
	"testing"

	"lionsql/pkg/storage"
)

func TestTableCasingAndZombieIndex(t *testing.T) {
	eng := New()
	defer eng.Close()

	// 1. Create table with casing "Users"
	schema := &storage.TableSchema{
		Name: "Users",
		Columns: []storage.Column{
			{Name: "Id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "UserName", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "Idx_UserName", TableName: "Users", Columns: []string{"UserName"}},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable Users failed: %v", err)
	}

	// 2. Duplicate create with different casing should fail (BUG-STORAGE-TABLE-CASE-SENSITIVITY-1)
	schemaDup := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}
	if err := eng.CreateTable(schemaDup); err == nil {
		t.Fatalf("expected ErrTableExists when creating 'users' after 'Users'")
	}

	// 3. GetTable with different casing and brackets (BUG-TABLE-CASE-1)
	tbl1, err := eng.GetTable("users")
	if err != nil || tbl1.Name != "Users" {
		t.Fatalf("GetTable('users') failed: %v", err)
	}
	tbl2, err := eng.GetTable("[USERS]")
	if err != nil || tbl2.Name != "Users" {
		t.Fatalf("GetTable('[USERS]') failed: %v", err)
	}

	// 4. Transaction insert with lower-case "users" (BUG-MEM-TX-TABLE-CASE-1)
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	rec := storage.Record{
		"Id":       storage.IntValue(1),
		"UserName": storage.StringValue("alice"),
	}
	if err := tx.Insert("users", rec); err != nil {
		t.Fatalf("tx.Insert('users') failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("tx.Commit failed: %v", err)
	}

	// 5. Verify row was not silently dropped upon commit
	tx2, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx 2 failed: %v", err)
	}
	gotRec, err := tx2.Get("USERS", storage.IntValue(1))
	if err != nil {
		t.Fatalf("tx2.Get('USERS', 1) failed: %v", err)
	}
	if uVal, ok := gotRec.Get("username"); !ok || uVal.SVal != "alice" {
		t.Fatalf("expected UserName alice, got %v", uVal)
	}

	// 6. IndexScan with case-insensitive index name (BUG-MEM-TX-INDEX-CASE-1)
	low := storage.StringValue("a")
	high := storage.StringValue("z")
	cur, err := tx2.IndexScan("users", "idx_username", &low, &high)
	if err != nil {
		t.Fatalf("IndexScan with 'idx_username' failed: %v", err)
	}
	if !cur.Next() {
		t.Fatalf("expected cursor item in IndexScan")
	}
	cur.Close()
	tx2.Rollback()

	// 7. DropIndex with different casing (BUG-DROP-INDEX-CASE-1)
	if err := eng.DropIndex("users", "IDX_USERNAME"); err != nil {
		t.Fatalf("DropIndex with 'IDX_USERNAME' failed: %v", err)
	}
	// Verify index is completely removed from td.Indexes (no zombie index)
	eng.mu.RLock()
	td := eng.tables["Users"]
	if len(td.Indexes) != 0 {
		t.Fatalf("expected td.Indexes to be empty, got %d indexes", len(td.Indexes))
	}
	eng.mu.RUnlock()

	// 8. DropTable with different casing (BUG-TABLE-CASE-1)
	if err := eng.DropTable("users"); err != nil {
		t.Fatalf("DropTable('users') failed: %v", err)
	}
	if _, err := eng.GetTable("users"); err == nil {
		t.Fatalf("table 'users' should not exist after DropTable")
	}
}
