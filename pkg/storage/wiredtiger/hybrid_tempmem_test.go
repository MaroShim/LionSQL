//go:build cgo && wiredtiger

package wiredtiger

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"lionsql/pkg/storage"
)

func listWTFiles(t *testing.T, dir string) map[string]struct{} {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q) failed: %v", dir, err)
	}
	res := make(map[string]struct{})
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".wt") {
			res[e.Name()] = struct{}{}
		}
	}
	return res
}

func TestHybridTempMem_ZeroWTFilesCreated(t *testing.T) {
	homeDir := t.TempDir()
	eng := New()
	if err := eng.Open(homeDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	permSchema := &storage.TableSchema{
		Name: "perm_orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "amount", Type: storage.TypeInt, Nullable: false},
		},
	}
	if err := eng.CreateTable(permSchema); err != nil {
		t.Fatalf("CreateTable perm_orders failed: %v", err)
	}

	baselineWT := listWTFiles(t, homeDir)
	if _, ok := baselineWT["perm_orders.wt"]; !ok {
		t.Fatalf("expected permanent table perm_orders.wt to exist in %v", baselineWT)
	}

	tempNames := []string{
		"#local_scratch",
		"__temp_sess1_buf",
		"__tv_sess1_items",
		"__var_sess1_rows",
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	for i, name := range tempNames {
		schema := &storage.TableSchema{
			Name: name,
			Columns: []storage.Column{
				{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
				{Name: "score", Type: storage.TypeInt, Nullable: false},
				{Name: "label", Type: storage.TypeVarChar, Nullable: true},
			},
		}
		if err := eng.CreateTable(schema); err != nil {
			t.Fatalf("CreateTable(%s) failed: %v", name, err)
		}
		if err := eng.CreateIndex(&storage.IndexSchema{
			Name:      "idx_score",
			TableName: name,
			Columns:   []string{"score"},
		}); err != nil {
			t.Fatalf("CreateIndex(%s) failed: %v", name, err)
		}

		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		for k := 1; k <= 5; k++ {
			rec := storage.Record{
				"id":    storage.IntValue(int64(k)),
				"score": storage.IntValue(int64(k * 10)),
				"label": storage.StringValue(fmt.Sprintf("%s-%d-%d", name, i, k)),
			}
			if err := tx.Insert(name, rec); err != nil {
				t.Fatalf("Insert(%s) failed: %v", name, err)
			}
		}
		if err := tx.Update(name, storage.IntValue(2), storage.Record{
			"id":    storage.IntValue(2),
			"score": storage.IntValue(99),
			"label": storage.StringValue("updated"),
		}); err != nil {
			t.Fatalf("Update(%s) failed: %v", name, err)
		}
		if err := tx.Delete(name, storage.IntValue(5)); err != nil {
			t.Fatalf("Delete(%s) failed: %v", name, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit(%s) failed: %v", name, err)
		}

		// Verify index scans (forward & desc), point get, add column, alter, truncate
		rtx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatalf("ReadOnly BeginTx failed: %v", err)
		}
		gotRec, err := rtx.Get(name, storage.IntValue(2))
		if err != nil {
			t.Fatalf("Get(%s, 2) failed: %v", name, err)
		}
		if v, ok := gotRec["score"]; !ok || v.IVal != 99 {
			t.Fatalf("Get(%s, 2) unexpected score: %+v", name, gotRec)
		}

		low := storage.IntValue(30)
		high := storage.IntValue(100)
		cur, err := rtx.IndexScanDesc(name, "idx_score", &low, &high)
		if err != nil {
			t.Fatalf("IndexScanDesc(%s) failed: %v", name, err)
		}
		var scores []int64
		for cur.Next() {
			r, err := cur.Record()
			if err != nil {
				t.Fatalf("Record() failed: %v", err)
			}
			scores = append(scores, r["score"].IVal)
		}
		cur.Close()
		if len(scores) != 3 || scores[0] != 99 || scores[1] != 40 || scores[2] != 30 {
			t.Fatalf("unexpected IndexScanDesc scores for %s: %v", name, scores)
		}
		_ = rtx.Commit()

		if err := eng.AddColumn(name, &storage.Column{Name: "extra", Type: storage.TypeInt, Nullable: true}); err != nil {
			t.Fatalf("AddColumn(%s) failed: %v", name, err)
		}
		if err := eng.TruncateTable(name); err != nil {
			t.Fatalf("TruncateTable(%s) failed: %v", name, err)
		}
	}

	// Verify RenameTable & DropTable on temp tables
	renamed := "__temp_sess1_renamed"
	if err := eng.RenameTable("__temp_sess1_buf", renamed); err != nil {
		t.Fatalf("RenameTable failed: %v", err)
	}
	if _, err := eng.GetTable(renamed); err != nil {
		t.Fatalf("GetTable(%s) failed: %v", renamed, err)
	}
	if err := eng.DropIndex("#local_scratch", "idx_score"); err != nil {
		t.Fatalf("DropIndex failed: %v", err)
	}
	if err := eng.DropTable(renamed); err != nil {
		t.Fatalf("DropTable failed: %v", err)
	}

	// Assert ZERO additional .wt files were created anywhere in homeDir
	afterWT := listWTFiles(t, homeDir)
	for fname := range afterWT {
		if _, existed := baselineWT[fname]; !existed {
			t.Fatalf("unexpected .wt disk file created for temporary tables: %s", fname)
		}
	}
	entries, err := os.ReadDir(homeDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, e := range entries {
		lower := strings.ToLower(e.Name())
		if strings.Contains(lower, "temp") || strings.Contains(lower, "__tv_") || strings.Contains(lower, "__var_") || strings.Contains(lower, "#") {
			t.Fatalf("unexpected filesystem artifact for temp table: %s", filepath.Join(homeDir, e.Name()))
		}
	}

	_ = sess.Close()
	if err := eng.Close(); err != nil {
		t.Fatalf("Close eng failed: %v", err)
	}

	// Reopen and verify permanent table persisted while temp tables did not
	eng2 := New()
	if err := eng2.Open(homeDir); err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	defer eng2.Close()

	if _, err := eng2.GetTable("perm_orders"); err != nil {
		t.Fatalf("expected perm_orders after reopen, got err: %v", err)
	}
	for _, name := range append(tempNames, renamed) {
		if _, err := eng2.GetTable(name); !errors.Is(err, storage.ErrTableNotFound) {
			t.Fatalf("expected temp table %q not persisted across reopen, got err: %v", name, err)
		}
	}
}

func TestHybridTempMem_MixedTransactionAndSavepoints(t *testing.T) {
	homeDir := t.TempDir()
	eng := New()
	if err := eng.Open(homeDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	if err := eng.CreateTable(&storage.TableSchema{
		Name: "perm_accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "bal", Type: storage.TypeInt, Nullable: false},
		},
	}); err != nil {
		t.Fatalf("CreateTable perm_accounts failed: %v", err)
	}
	if err := eng.CreateTable(&storage.TableSchema{
		Name: "__temp_batch",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "delta", Type: storage.TypeInt, Nullable: false},
		},
	}); err != nil {
		t.Fatalf("CreateTable __temp_batch failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// 1. Mixed TX: Savepoint BEFORE lazy tempTx init, then touch temp, then RollbackTo(sp_pre)
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx1.Insert("perm_accounts", storage.Record{
		"id":  storage.IntValue(1),
		"bal": storage.IntValue(100),
	}); err != nil {
		t.Fatalf("Insert perm_accounts failed: %v", err)
	}
	if err := tx1.Savepoint("sp_pre"); err != nil {
		t.Fatalf("Savepoint sp_pre failed: %v", err)
	}
	if err := tx1.Insert("__temp_batch", storage.Record{
		"id":    storage.IntValue(10),
		"delta": storage.IntValue(50),
	}); err != nil {
		t.Fatalf("Insert __temp_batch failed: %v", err)
	}
	if err := tx1.RollbackTo("sp_pre"); err != nil {
		t.Fatalf("RollbackTo sp_pre failed: %v", err)
	}
	if _, err := tx1.Get("__temp_batch", storage.IntValue(10)); !errors.Is(err, storage.ErrKeyNotFound) {
		t.Fatalf("expected __temp_batch key 10 rolled back at sp_pre, got err: %v", err)
	}

	// Now insert after sp_pre, take sp_post, mutate both, and rollback sp_post
	if err := tx1.Insert("__temp_batch", storage.Record{
		"id":    storage.IntValue(11),
		"delta": storage.IntValue(25),
	}); err != nil {
		t.Fatalf("Insert __temp_batch 11 failed: %v", err)
	}
	if err := tx1.Savepoint("sp_post"); err != nil {
		t.Fatalf("Savepoint sp_post failed: %v", err)
	}
	if err := tx1.Insert("perm_accounts", storage.Record{
		"id":  storage.IntValue(2),
		"bal": storage.IntValue(999),
	}); err != nil {
		t.Fatalf("Insert perm_accounts 2 failed: %v", err)
	}
	if err := tx1.Insert("__temp_batch", storage.Record{
		"id":    storage.IntValue(12),
		"delta": storage.IntValue(777),
	}); err != nil {
		t.Fatalf("Insert __temp_batch 12 failed: %v", err)
	}
	if err := tx1.RollbackTo("sp_post"); err != nil {
		t.Fatalf("RollbackTo sp_post failed: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit tx1 failed: %v", err)
	}

	// Verify committed state across both engines & ReadOnly checks
	rtx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("ReadOnly BeginTx failed: %v", err)
	}
	defer rtx.Rollback()

	if err := rtx.Insert("__temp_batch", storage.Record{
		"id":    storage.IntValue(99),
		"delta": storage.IntValue(1),
	}); !errors.Is(err, storage.ErrReadOnlyTransaction) {
		t.Fatalf("expected ErrReadOnlyTransaction on temp write in ReadOnly tx, got: %v", err)
	}

	tbls, err := rtx.ListTables()
	if err != nil || len(tbls) != 2 {
		t.Fatalf("expected 2 tables from ReadOnly ListTables, got len=%d err=%v", len(tbls), err)
	}
	if rec, err := rtx.Get("perm_accounts", storage.IntValue(1)); err != nil || rec["bal"].IVal != 100 {
		t.Fatalf("unexpected perm_accounts(1): %v, err=%v", rec, err)
	}
	if _, err := rtx.Get("perm_accounts", storage.IntValue(2)); !errors.Is(err, storage.ErrKeyNotFound) {
		t.Fatalf("expected perm_accounts(2) rolled back, err=%v", err)
	}
	if rec, err := rtx.Get("__temp_batch", storage.IntValue(11)); err != nil || rec["delta"].IVal != 25 {
		t.Fatalf("unexpected __temp_batch(11): %v, err=%v", rec, err)
	}
	if _, err := rtx.Get("__temp_batch", storage.IntValue(12)); !errors.Is(err, storage.ErrKeyNotFound) {
		t.Fatalf("expected __temp_batch(12) rolled back, err=%v", err)
	}
}

func TestHybridTempMem_ConcurrentWorkers_RaceFree(t *testing.T) {
	homeDir := t.TempDir()
	eng := New()
	if err := eng.Open(homeDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	if err := eng.CreateTable(&storage.TableSchema{
		Name: "perm_shared",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt, Nullable: false},
		},
	}); err != nil {
		t.Fatalf("CreateTable perm_shared failed: %v", err)
	}

	const workers = 16
	const iters = 20
	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sess, err := eng.NewSession()
			if err != nil {
				errCh <- err
				return
			}
			defer sess.Close()

			tempName := fmt.Sprintf("__var_w%d_scratch", workerID)
			if err := eng.CreateTable(&storage.TableSchema{
				Name: tempName,
				Columns: []storage.Column{
					{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
					{Name: "val", Type: storage.TypeInt, Nullable: false},
				},
			}); err != nil {
				errCh <- err
				return
			}
			defer eng.DropTable(tempName)

			for i := 0; i < iters; i++ {
				tx, err := sess.BeginTx(storage.TxOptions{})
				if err != nil {
					errCh <- err
					return
				}
				pk := int64(workerID*1000 + i)
				if err := tx.Insert("perm_shared", storage.Record{
					"id":  storage.IntValue(pk),
					"val": storage.IntValue(int64(i)),
				}); err != nil {
					_ = tx.Rollback()
					errCh <- err
					return
				}
				if err := tx.Insert(tempName, storage.Record{
					"id":  storage.IntValue(int64(i)),
					"val": storage.IntValue(pk),
				}); err != nil {
					_ = tx.Rollback()
					errCh <- err
					return
				}
				if err := tx.Commit(); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent worker error: %v", err)
		}
	}
}
