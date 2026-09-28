package memengine

import (
	"errors"
	"testing"

	"sqlion/pkg/storage"
)

func TestSecondaryIndex_LifecycleAndUniqueConstraint(t *testing.T) {
	eng := New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "customers",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar, Nullable: false},
			{Name: "age", Type: storage.TypeInt, Nullable: false},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 1. Create Unique Index on email
	idxEmail := &storage.IndexSchema{
		Name:      "idx_cust_email",
		TableName: "customers",
		Columns:   []string{"email"},
		Unique:    true,
	}
	if err := eng.CreateIndex(idxEmail); err != nil {
		t.Fatalf("CreateIndex idx_cust_email failed: %v", err)
	}

	// 2. Create Non-unique Index on age
	idxAge := &storage.IndexSchema{
		Name:      "idx_cust_age",
		TableName: "customers",
		Columns:   []string{"age"},
		Unique:    false,
	}
	if err := eng.CreateIndex(idxAge); err != nil {
		t.Fatalf("CreateIndex idx_cust_age failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// 3. Insert rows
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	_ = tx.Insert("customers", storage.Record{
		"id":    storage.IntValue(1),
		"email": storage.StringValue("alice@example.com"),
		"age":   storage.IntValue(25),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":    storage.IntValue(2),
		"email": storage.StringValue("bob@example.com"),
		"age":   storage.IntValue(30),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":    storage.IntValue(3),
		"email": storage.StringValue("charlie@example.com"),
		"age":   storage.IntValue(25),
	})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 4. Test Unique Constraint Violation
	tx2, _ := sess.BeginTx(storage.TxOptions{})
	err = tx2.Insert("customers", storage.Record{
		"id":    storage.IntValue(4),
		"email": storage.StringValue("alice@example.com"), // Duplicate!
		"age":   storage.IntValue(40),
	})
	if err == nil {
		err = tx2.Commit()
	}
	if !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("Expected ErrUniqueViolation on duplicate email, got: %v", err)
	}

	// 5. Test IndexScan Exact Match on Unique Index
	tx3, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer tx3.Rollback()

	keyVal := storage.StringValue("bob@example.com")
	cur, err := tx3.IndexScan("customers", "idx_cust_email", &keyVal, &keyVal)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	defer cur.Close()

	count := 0
	for cur.Next() {
		rec, _ := cur.Record()
		if rec["id"].IVal != 2 {
			t.Errorf("Expected id=2 for bob@example.com, got %v", rec["id"].IVal)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("Expected 1 row for exact match, got %d", count)
	}

	// 5.1. Test IndexOnlyScan: returns index keys and PK without accessing main table rows
	curOnly, err := tx3.IndexOnlyScan("customers", "idx_cust_email", &keyVal, &keyVal)
	if err != nil {
		t.Fatalf("IndexOnlyScan failed: %v", err)
	}
	defer curOnly.Close()

	onlyCount := 0
	for curOnly.Next() {
		rec, err := curOnly.Record()
		if err != nil {
			t.Fatalf("IndexOnlyScan Record error: %v", err)
		}
		if rec["id"].IVal != 2 {
			t.Errorf("Expected id=2 for bob@example.com, got %v", rec["id"].IVal)
		}
		if rec["email"].SVal != "bob@example.com" {
			t.Errorf("Expected email=bob@example.com, got %v", rec["email"].SVal)
		}
		if _, hasName := rec["name"]; hasName {
			t.Errorf("Expected name to NOT be present in index-only record, got %v", rec["name"])
		}
		onlyCount++
	}
	if onlyCount != 1 {
		t.Fatalf("Expected 1 row for index-only match, got %d", onlyCount)
	}

	// 6. Test IndexScan Range on Age Index [25, 30]
	lowAge := storage.IntValue(25)
	highAge := storage.IntValue(30)
	curAge, err := tx3.IndexScan("customers", "idx_cust_age", &lowAge, &highAge)
	if err != nil {
		t.Fatalf("IndexScan on age failed: %v", err)
	}
	defer curAge.Close()

	ageCount := 0
	for curAge.Next() {
		rec, _ := curAge.Record()
		age := rec["age"].IVal
		if age < 25 || age > 30 {
			t.Errorf("Record out of range: %v", age)
		}
		ageCount++
	}
	if ageCount != 3 {
		t.Fatalf("Expected 3 rows in age range [25, 30], got %d", ageCount)
	}
}

func TestSecondaryIndex_UpdateAndDeleteSync(t *testing.T) {
	eng := New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "category", Type: storage.TypeVarChar, Nullable: false},
			{Name: "price", Type: storage.TypeInt, Nullable: false},
		},
	}
	_ = eng.CreateTable(schema)
	_ = eng.CreateIndex(&storage.IndexSchema{
		Name:      "idx_prod_cat",
		TableName: "products",
		Columns:   []string{"category"},
	})

	sess, _ := eng.NewSession()
	defer sess.Close()

	// Insert 2 items
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("products", storage.Record{"id": storage.IntValue(1), "category": storage.StringValue("Electronics"), "price": storage.IntValue(100)})
	_ = tx.Insert("products", storage.Record{"id": storage.IntValue(2), "category": storage.StringValue("Books"), "price": storage.IntValue(20)})
	_ = tx.Commit()

	// Update item 1 from Electronics to Books
	tx2, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx2.Update("products", storage.IntValue(1), storage.Record{"category": storage.StringValue("Books")})
	_ = tx2.Commit()

	// Scan "Books"
	tx3, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	catBooks := storage.StringValue("Books")
	cur, _ := tx3.IndexScan("products", "idx_prod_cat", &catBooks, &catBooks)
	defer cur.Close()

	var bookIds []int64
	for cur.Next() {
		r, _ := cur.Record()
		bookIds = append(bookIds, r["id"].IVal)
	}
	if len(bookIds) != 2 {
		t.Fatalf("Expected 2 books after update, got %d", len(bookIds))
	}
	tx3.Rollback()

	// Delete item 2
	tx4, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx4.Delete("products", storage.IntValue(2))
	_ = tx4.Commit()

	// Scan "Books" again -> should only have item 1
	tx5, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	cur2, _ := tx5.IndexScan("products", "idx_prod_cat", &catBooks, &catBooks)
	defer cur2.Close()

	count := 0
	for cur2.Next() {
		r, _ := cur2.Record()
		if r["id"].IVal != 1 {
			t.Errorf("Expected only id=1, got %v", r["id"].IVal)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("Expected 1 book after delete, got %d", count)
	}
	tx5.Rollback()
}
