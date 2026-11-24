package memengine

import (
	"sync"
	"testing"
	"time"

	"lionsql/pkg/storage"
)

// TestMemEngine_ApplyWALRecord_FailureRollsBackPartialMutations tests that when
// applying a multi-op WALRecord fails midway, all prior mutations are rolled back cleanly
// and the engine LSN is not advanced (All-or-Nothing ACID atomicity).
func TestMemEngine_ApplyWALRecord_FailureRollsBackPartialMutations(t *testing.T) {
	eng := New()
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "balance", Type: storage.TypeInt},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_bal", TableName: "accounts", Columns: []string{"balance"}},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	initialLSN := eng.CurrentLSN()

	// Construct multi-op WALRecord:
	// Op 1: Insert account 1
	// Op 2: Insert account 2
	// Op 3: Fail on non-existent table "ghost_table"
	r1 := storage.Record{"id": storage.IntValue(1), "balance": storage.IntValue(100)}
	r2 := storage.Record{"id": storage.IntValue(2), "balance": storage.IntValue(200)}

	rec := WALRecord{
		LSN:       initialLSN + 5,
		Timestamp: time.Now().UnixNano(),
		Ops: []WALOp{
			{Type: OpInsert, Table: "accounts", Key: "1", Record: r1},
			{Type: OpInsert, Table: "accounts", Key: "2", Record: r2},
			{Type: OpInsert, Table: "ghost_table", Key: "3", Record: r1}, // Will fail!
		},
	}

	err := eng.ApplyWALRecord(rec)
	if err == nil {
		t.Fatal("expected ApplyWALRecord to fail on missing table, got nil")
	}

	// Verify LSN was NOT advanced on failure
	if currentLSN := eng.CurrentLSN(); currentLSN != initialLSN {
		t.Fatalf("expected engine LSN to remain %d, but was advanced to %d", initialLSN, currentLSN)
	}

	// Verify partial mutations (Op 1 and Op 2) were rolled back completely
	td := eng.tables["accounts"]
	if td.hasKey("1") || td.hasKey("2") {
		t.Fatalf("partial mutation leaked! Table has keys: %v", td.Keys)
	}
	if len(td.Keys) != 0 {
		t.Fatalf("expected 0 keys in table, got %d (%v)", len(td.Keys), td.Keys)
	}

	// Verify secondary index was also cleaned up
	idx := td.Indexes["idx_bal"]
	entries := idx.ScanRange(nil, nil)
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries in index idx_bal, got %d", len(entries))
	}
}

// TestMemEngine_CreateIndex_ConcurrentDML_NoMissingEntries tests that concurrent
// DML transactions committing while CreateIndex is executing never result in missing
// index entries.
func TestMemEngine_CreateIndex_ConcurrentDML_NoMissingEntries(t *testing.T) {
	eng := New()
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "score", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	// Pre-populate 100 rows
	for i := 1; i <= 100; i++ {
		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_ = tx.Insert("users", storage.Record{"id": storage.IntValue(int64(i)), "score": storage.IntValue(int64(i * 10))})
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	const insertCount = 200
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	// Goroutine 1: Rapid concurrent inserts committing rows 101..300
	var insertErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-startCh
		for i := 101; i <= 100+insertCount; i++ {
			tx, err := sess.BeginTx(storage.TxOptions{})
			if err != nil {
				insertErr = err
				return
			}
			_ = tx.Insert("users", storage.Record{"id": storage.IntValue(int64(i)), "score": storage.IntValue(int64(i * 10))})
			if err := tx.Commit(); err != nil {
				insertErr = err
				return
			}
		}
	}()

	// Goroutine 2: CreateIndex concurrently
	var createErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-startCh
		idxSchema := &storage.IndexSchema{
			Name:      "idx_users_score",
			TableName: "users",
			Columns:   []string{"score"},
		}
		createErr = eng.CreateIndex(idxSchema)
	}()

	close(startCh)
	wg.Wait()

	if insertErr != nil {
		t.Fatalf("concurrent insert failed: %v", insertErr)
	}
	if createErr != nil {
		t.Fatalf("CreateIndex failed: %v", createErr)
	}

	// Verify all 300 rows are present in the table
	td := eng.tables["users"]
	totalRows := td.rowCount()
	if totalRows != int64(100+insertCount) {
		t.Fatalf("expected %d total rows in table, got %d", 100+insertCount, totalRows)
	}

	// Crucial assertion: Every single row in the table MUST have an entry in idx_users_score
	idxData := td.Indexes["idx_users_score"]
	if idxData == nil {
		t.Fatal("idx_users_score index not found in table")
	}

	var missingKeys []string
	_ = td.forEachRow(func(pk string, row storage.Row) bool {
		scoreVal := row.Values[1]
		pks := idxData.ScanRange(&scoreVal, &scoreVal)
		found := false
		for _, foundPK := range pks {
			if foundPK == pk {
				found = true
				break
			}
		}
		if !found {
			missingKeys = append(missingKeys, pk)
		}
		return true
	})

	if len(missingKeys) > 0 {
		t.Fatalf("CRITICAL: %d index entries missing after concurrent CreateIndex! Missing PKs: %v", len(missingKeys), missingKeys)
	}
}
