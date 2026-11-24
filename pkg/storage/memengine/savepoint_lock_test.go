package memengine_test

import (
	"testing"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestSavepoint_RollbackReleasesLocks(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "balance", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess1, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession 1 failed: %v", err)
	}
	defer sess1.Close()

	sess2, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession 2 failed: %v", err)
	}
	defer sess2.Close()

	// 1. Initial seed: (100, 1000)
	txInit, _ := sess1.BeginTx(storage.TxOptions{})
	_ = txInit.Insert("accounts", storage.Record{"id": storage.IntValue(100), "balance": storage.IntValue(1000)})
	if err := txInit.Commit(); err != nil {
		t.Fatalf("Init commit failed: %v", err)
	}

	// 2. T1 creates a savepoint, then modifies row 100
	tx1, err := sess1.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx tx1 failed: %v", err)
	}
	defer tx1.Rollback()

	if err := tx1.Savepoint("sp1"); err != nil {
		t.Fatalf("Savepoint failed: %v", err)
	}

	if err := tx1.Update("accounts", storage.IntValue(100), storage.Record{"id": storage.IntValue(100), "balance": storage.IntValue(500)}); err != nil {
		t.Fatalf("Update in tx1 failed: %v", err)
	}

	// 3. Before rollback, T2 cannot modify row 100 (locked by T1)
	tx2Pre, err := sess2.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx tx2Pre failed: %v", err)
	}
	defer tx2Pre.Rollback()

	err = tx2Pre.Update("accounts", storage.IntValue(100), storage.Record{"id": storage.IntValue(100), "balance": storage.IntValue(999)})
	if err == nil {
		t.Fatalf("expected T2 to fail modifying row 100 while T1 holds write lock, but succeeded")
	}
	_ = tx2Pre.Rollback()

	// 4. T1 rolls back to sp1 (canceling the update on row 100)
	if err := tx1.RollbackTo("sp1"); err != nil {
		t.Fatalf("RollbackTo sp1 failed: %v", err)
	}

	// 5. After rollback, T1's lock on row 100 MUST be released!
	// Concurrent T2 should now be able to modify row 100 without ErrSerializationFailure
	tx2Post, err := sess2.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx tx2Post failed: %v", err)
	}
	defer tx2Post.Rollback()

	err = tx2Post.Update("accounts", storage.IntValue(100), storage.Record{"id": storage.IntValue(100), "balance": storage.IntValue(888)})
	if err != nil {
		t.Fatalf("FATAL: T2 failed to modify row 100 after T1 rolled back to savepoint! Lock leaked: %v", err)
	}
	if err := tx2Post.Commit(); err != nil {
		t.Fatalf("T2 commit failed: %v", err)
	}

	// Verify balance is 888
	txVerify, _ := sess1.BeginTx(storage.TxOptions{ReadOnly: true})
	defer txVerify.Rollback()
	rec, err := txVerify.Get("accounts", storage.IntValue(100))
	if err != nil || rec == nil || rec["balance"].IVal != 888 {
		t.Fatalf("expected balance=888, got rec=%v err=%v", rec, err)
	}
}

func TestSavepoint_RollbackReleasesRefLocks(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	parentSchema := &storage.TableSchema{
		Name: "parents",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}
	childSchema := &storage.TableSchema{
		Name: "children",
		Columns: []storage.Column{
			{Name: "cid", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "pid", Type: storage.TypeInt},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_child_parent",
				ChildColumns:  []string{"pid"},
				ParentTable:   "parents",
				ParentColumns: []string{"id"},
			},
		},
	}
	if err := eng.CreateTable(parentSchema); err != nil {
		t.Fatalf("CreateTable parent failed: %v", err)
	}
	if err := eng.CreateTable(childSchema); err != nil {
		t.Fatalf("CreateTable child failed: %v", err)
	}

	sess1, _ := eng.NewSession()
	defer sess1.Close()
	sess2, _ := eng.NewSession()
	defer sess2.Close()

	// Seed parent row (10)
	txInit, _ := sess1.BeginTx(storage.TxOptions{})
	_ = txInit.Insert("parents", storage.Record{"id": storage.IntValue(10)})
	_ = txInit.Commit()

	// 1. T1 creates savepoint, then inserts child referencing parent 10
	tx1, _ := sess1.BeginTx(storage.TxOptions{})
	defer tx1.Rollback()
	_ = tx1.Savepoint("sp_fk")
	if err := tx1.Insert("children", storage.Record{"cid": storage.IntValue(1), "pid": storage.IntValue(10)}); err != nil {
		t.Fatalf("Insert child failed: %v", err)
	}

	// 2. T2 cannot delete parent 10 while T1 holds ref lock
	tx2Pre, _ := sess2.BeginTx(storage.TxOptions{})
	err := tx2Pre.Delete("parents", storage.IntValue(10))
	if err == nil {
		t.Fatalf("expected T2 delete to fail while T1 references parent, but succeeded")
	}
	_ = tx2Pre.Rollback()

	// 3. T1 rolls back to sp_fk
	if err := tx1.RollbackTo("sp_fk"); err != nil {
		t.Fatalf("RollbackTo sp_fk failed: %v", err)
	}

	// 4. After rollback, T1's ref lock on parent 10 MUST be released!
	// T2 should now be able to delete parent 10
	tx2Post, _ := sess2.BeginTx(storage.TxOptions{})
	defer tx2Post.Rollback()
	if err := tx2Post.Delete("parents", storage.IntValue(10)); err != nil {
		t.Fatalf("FATAL: T2 failed to delete parent 10 after T1 rolled back child insert! RefLock leaked: %v", err)
	}
	if err := tx2Post.Commit(); err != nil {
		t.Fatalf("T2 delete commit failed: %v", err)
	}
}
