//go:build cgo && wiredtiger

package wiredtiger

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage"
)

func TestWiredTiger_CompositePK_IndexScanAndFKCascade(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_comp_pk_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Bug 29 (A-1): Secondary Index Scan on Composite PK Table
	if _, err := coord.ExecuteSQL(`
		CREATE TABLE _t_idx_comp (k1 INT, k2 INT, val INT, PRIMARY KEY (k1, k2));
		CREATE INDEX idx_comp_val ON _t_idx_comp(val);
		INSERT INTO _t_idx_comp VALUES (1, 10, 500), (1, 20, 500), (2, 10, 999);
	`); err != nil {
		t.Fatalf("setup _t_idx_comp failed: %v", err)
	}

	resIdx, err := coord.ExecuteSQL("SELECT k1, k2, val FROM _t_idx_comp WHERE val = 500 ORDER BY k1, k2;")
	if err != nil {
		t.Fatalf("index scan query failed: %v", err)
	}
	if len(resIdx.Rows) != 2 {
		t.Fatalf("expected 2 rows from composite PK secondary index scan, got %d: %v", len(resIdx.Rows), resIdx.Rows)
	}
	if fmt.Sprintf("%v", resIdx.Rows[0]["k2"]) != "10" || fmt.Sprintf("%v", resIdx.Rows[1]["k2"]) != "20" {
		t.Errorf("unexpected rows: %v", resIdx.Rows)
	}

	// 2. Bug 30 (A-2): Foreign Key ON DELETE CASCADE on Composite PK Child Table
	parentSchema := storage.TableSchema{
		Name: "fk_parent",
		Columns: []storage.Column{
			{Name: "pid", Type: storage.TypeInt, PrimaryKey: true},
		},
	}
	childSchema := storage.TableSchema{
		Name: "fk_child_comp",
		Columns: []storage.Column{
			{Name: "pid", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "cid", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "note", Type: storage.TypeVarChar},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_p_c",
				ChildColumns:  []string{"pid"},
				ParentTable:   "fk_parent",
				ParentColumns: []string{"pid"},
				OnDelete:      storage.ActionCascade,
			},
		},
	}
	if err := eng.CreateTable(&parentSchema); err != nil {
		t.Fatalf("create parent failed: %v", err)
	}
	if err := eng.CreateTable(&childSchema); err != nil {
		t.Fatalf("create child failed: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("fk_parent", storage.Record{"pid": storage.IntValue(1)})
	_ = tx.Insert("fk_parent", storage.Record{"pid": storage.IntValue(2)})
	_ = tx.Insert("fk_child_comp", storage.Record{"pid": storage.IntValue(1), "cid": storage.IntValue(10), "note": storage.StringValue("a")})
	_ = tx.Insert("fk_child_comp", storage.Record{"pid": storage.IntValue(1), "cid": storage.IntValue(20), "note": storage.StringValue("b")})
	_ = tx.Insert("fk_child_comp", storage.Record{"pid": storage.IntValue(2), "cid": storage.IntValue(10), "note": storage.StringValue("c")})
	_ = tx.Commit()

	// Delete parent pid=1 -> both (1, 10) and (1, 20) in fk_child_comp must be cascade-deleted
	txDel, _ := sess.BeginTx(storage.TxOptions{})
	if err := txDel.Delete("fk_parent", storage.IntValue(1)); err != nil {
		t.Fatalf("delete parent failed: %v", err)
	}
	_ = txDel.Commit()

	txCheck, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer txCheck.Rollback()
	if _, err := txCheck.Get("fk_child_comp", storage.StringValue("1\x1f10")); err == nil {
		t.Errorf("expected child (1, 10) to be cascade-deleted, but still exists")
	}
	if _, err := txCheck.Get("fk_child_comp", storage.StringValue("1\x1f20")); err == nil {
		t.Errorf("expected child (1, 20) to be cascade-deleted, but still exists")
	}
	if _, err := txCheck.Get("fk_child_comp", storage.StringValue("2\x1f10")); err != nil {
		t.Errorf("expected child (2, 10) to remain intact, got err: %v", err)
	}
}
