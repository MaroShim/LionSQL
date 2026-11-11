package memengine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"lionsql/pkg/storage"
)

func compositeKeyValue(parts ...storage.Value) storage.Value {
	encoded := ""
	for i, p := range parts {
		if i > 0 {
			encoded += "\x00"
		}
		encoded += storage.EncodeIndexKeyPart(p)
	}
	return storage.StringValue(encoded)
}

func assertAllEntriesFlat(t *testing.T, td *TableData) {
	t.Helper()
	if td.mode != MemoryCompact {
		t.Fatalf("table %q: expected MemoryCompact mode, got %v", td.Schema.Name, td.mode)
	}
	if td.layout == nil {
		t.Fatalf("table %q: expected non-nil FlatRecordLayout", td.Schema.Name)
	}
	if len(td.Rows) != 0 {
		t.Fatalf("table %q: expected empty td.Rows after recovery/sync, got len=%d", td.Schema.Name, len(td.Rows))
	}
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.RLock()
		for pk, entry := range sh.entries {
			if len(entry.flatBuf) == 0 || len(entry.row.Values) != 0 {
				sh.mu.RUnlock()
				t.Fatalf("table %q pk %q reverted to non-flat entry: flatLen=%d rowVals=%d",
					td.Schema.Name, pk, len(entry.flatBuf), len(entry.row.Values))
			}
		}
		sh.mu.RUnlock()
	}
}

// TestCompact_AOF_RDB_Recovery_And_TornWAL verifies TEST-OCC-FLAT-1:
// RDB snapshot compaction (RewriteAOF -> tables.mpk), subsequent WAL mutations
// including DDL (AddColumn), torn WAL tail truncation, and full recovery in MemoryCompact.
func TestCompact_AOF_RDB_Recovery_And_TornWAL(t *testing.T) {
	tempDir := t.TempDir()

	opts := DefaultOptions(tempDir)
	opts.Mode = MemoryCompact
	opts.EnableAOF = true
	opts.EnableRDB = true
	opts.FsyncPolicy = FsyncAlways
	opts.AOFThreshold = 64 * 1024 * 1024

	eng := NewWithOptions(opts)
	if err := eng.OpenWithOptions(opts); err != nil {
		t.Fatalf("OpenWithOptions failed: %v", err)
	}

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "region", Type: storage.TypeVarChar, Nullable: false},
			{Name: "owner", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeDecimal, Nullable: false},
			{Name: "active", Type: storage.TypeBool, Nullable: false},
			{Name: "note", Type: storage.TypeVarChar, Nullable: true},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	idx := &storage.IndexSchema{
		Name:      "idx_accounts_region_owner",
		TableName: "accounts",
		Columns:   []string{"region", "owner"},
		Unique:    true,
	}
	if err := eng.CreateIndex(idx); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	// Phase 1: Initial rows (mix of SSO <= 7B and heap-sized strings + NULL) compacted into RDB (tables.mpk)
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 1 failed: %v", err)
	}
	initial := []storage.Record{
		{"id": storage.IntValue(1), "region": storage.StringValue("US"), "owner": storage.StringValue("alice"), "balance": storage.DecimalValue("100.50"), "active": storage.BoolValue(true), "note": storage.NullValue(storage.TypeVarChar)},
		{"id": storage.IntValue(2), "region": storage.StringValue("KR"), "owner": storage.StringValue("bob-long-owner-name"), "balance": storage.DecimalValue("250.00"), "active": storage.BoolValue(true), "note": storage.StringValue("init")},
		{"id": storage.IntValue(3), "region": storage.StringValue("EU"), "owner": storage.StringValue("charlie-long-owner-name"), "balance": storage.DecimalValue("500.75"), "active": storage.BoolValue(false), "note": storage.StringValue("vip-account-note")},
	}
	for _, rec := range initial {
		if err := tx1.Insert("accounts", rec); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit 1 failed: %v", err)
	}

	if err := eng.RewriteAOF(); err != nil {
		t.Fatalf("RewriteAOF failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "tables.mpk")); err != nil {
		t.Fatalf("expected tables.mpk after RewriteAOF: %v", err)
	}

	// Phase 2: Post-snapshot WAL mutations (update SSO <-> long string, delete, insert, AddColumn DDL)
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 2 failed: %v", err)
	}
	if err := tx2.Update("accounts", storage.IntValue(1), storage.Record{
		"id":      storage.IntValue(1),
		"region":  storage.StringValue("US"),
		"owner":   storage.StringValue("alice-expanded-owner-name"),
		"balance": storage.DecimalValue("999.99"),
		"active":  storage.BoolValue(false),
		"note":    storage.StringValue("upd"),
	}); err != nil {
		t.Fatalf("Update row 1 failed: %v", err)
	}
	if err := tx2.Delete("accounts", storage.IntValue(2)); err != nil {
		t.Fatalf("Delete row 2 failed: %v", err)
	}
	if err := tx2.Insert("accounts", storage.Record{
		"id":      storage.IntValue(4),
		"region":  storage.StringValue("JP"),
		"owner":   storage.StringValue("ken"),
		"balance": storage.DecimalValue("42.00"),
		"active":  storage.BoolValue(true),
		"note":    storage.NullValue(storage.TypeVarChar),
	}); err != nil {
		t.Fatalf("Insert row 4 failed: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit 2 failed: %v", err)
	}

	if err := eng.AddColumn("accounts", &storage.Column{
		Name:    "tier",
		Type:    storage.TypeVarChar,
		Default: "'gold'",
	}); err != nil {
		t.Fatalf("AddColumn failed: %v", err)
	}

	// Simulate abrupt kill without calling eng.Close() (do not flush shutdown RDB)
	eng.mu.Lock()
	if eng.stopCh != nil {
		close(eng.stopCh)
		eng.stopCh = nil
	}
	wal := eng.wal
	eng.wal = nil
	eng.mu.Unlock()
	eng.bgWg.Wait()
	if wal != nil {
		_ = wal.Close()
	}
	_ = sess.Close()

	// Append torn partial bytes at tail of wal.aof to verify torn-write resilience in compact recovery
	aofPath := filepath.Join(tempDir, "wal.aof")
	f, err := os.OpenFile(aofPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("open wal.aof for torn write failed: %v", err)
	}
	if _, err := f.Write([]byte{0x04, 0x00, 0x00, 0x00, 0xde, 0xad}); err != nil {
		_ = f.Close()
		t.Fatalf("append torn bytes failed: %v", err)
	}
	_ = f.Close()

	// Phase 3: Re-open and validate full RDB + WAL + torn-tail recovery in MemoryCompact
	recEng := NewWithOptions(opts)
	if err := recEng.OpenWithOptions(opts); err != nil {
		t.Fatalf("Recovery OpenWithOptions failed: %v", err)
	}
	defer recEng.Close()

	recEng.mu.RLock()
	td := recEng.tables["accounts"]
	recEng.mu.RUnlock()
	if td == nil {
		t.Fatalf("recovered accounts table missing")
	}
	assertAllEntriesFlat(t, td)

	recSess, err := recEng.NewSession()
	if err != nil {
		t.Fatalf("recovered NewSession failed: %v", err)
	}
	defer recSess.Close()

	readTx, err := recSess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("recovered read BeginTx failed: %v", err)
	}
	defer readTx.Rollback()

	// Row 1 updated + tier='gold'
	r1, err := readTx.Get("accounts", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get row 1 failed: %v", err)
	}
	if r1["owner"].SVal != "alice-expanded-owner-name" || r1["balance"].String() != "999.99" || r1["active"].BVal || r1["note"].SVal != "upd" || r1["tier"].SVal != "gold" {
		t.Fatalf("row 1 recovered mismatch: %+v", r1)
	}

	// Row 2 deleted
	if _, err := readTx.Get("accounts", storage.IntValue(2)); !errors.Is(err, storage.ErrKeyNotFound) {
		t.Fatalf("expected row 2 deleted (ErrKeyNotFound), got err=%v", err)
	}

	// Row 3 from RDB snapshot + AddColumn replay
	r3, err := readTx.Get("accounts", storage.IntValue(3))
	if err != nil {
		t.Fatalf("Get row 3 failed: %v", err)
	}
	if r3["owner"].SVal != "charlie-long-owner-name" || r3["note"].SVal != "vip-account-note" || r3["tier"].SVal != "gold" {
		t.Fatalf("row 3 recovered mismatch: %+v", r3)
	}

	// Row 4 inserted post-snapshot + ArenaRowCursor verification
	arena := storage.GetArena()
	defer storage.PutArena(arena)
	cur, err := readTx.Scan("accounts")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	defer cur.Close()
	arenaCur := cur.(storage.ArenaRowCursor)
	seenIDs := make(map[int64]bool)
	for cur.Next() {
		row, err := arenaCur.RowWithArena(arena)
		if err != nil {
			t.Fatalf("RowWithArena failed: %v", err)
		}
		seenIDs[row.Values[0].IVal] = true
		if row.Values[6].SVal != "gold" {
			t.Fatalf("expected tier='gold' via ArenaRowCursor, got %+v", row.Values)
		}
	}
	if len(seenIDs) != 3 || !seenIDs[1] || !seenIDs[3] || !seenIDs[4] {
		t.Fatalf("unexpected recovered IDs: %+v", seenIDs)
	}

	// Recovered composite unique SkipList index still enforces uniqueness
	dupTx, err := recSess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx dup failed: %v", err)
	}
	err = dupTx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(99),
		"region":  storage.StringValue("US"),
		"owner":   storage.StringValue("alice-expanded-owner-name"),
		"balance": storage.DecimalValue("1.00"),
		"active":  storage.BoolValue(true),
		"note":    storage.NullValue(storage.TypeVarChar),
		"tier":    storage.StringValue("silver"),
	})
	if err == nil {
		err = dupTx.Commit()
	} else {
		_ = dupTx.Rollback()
	}
	if !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("expected ErrUniqueViolation after recovery, got %v", err)
	}
}

// TestCompact_CompositeIndexes_SkipList_FK_And_ArenaScan verifies TEST-OCC-FLAT-2:
// Composite PK, composite unique & non-unique SkipList indexes, FKs (CASCADE/SET NULL),
// RenameTable FK cache updates, AddColumn flat layout rebuild, and TruncateTable under default MemoryCompact.
func TestCompact_CompositeIndexes_SkipList_FK_And_ArenaScan(t *testing.T) {
	eng := New()
	if eng.opts.Mode != MemoryCompact {
		t.Fatalf("expected memengine.New() default mode MemoryCompact, got %v", eng.opts.Mode)
	}
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	if err := eng.CreateTable(&storage.TableSchema{
		Name: "departments",
		Columns: []storage.Column{
			{Name: "dept_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar, Nullable: false},
		},
	}); err != nil {
		t.Fatalf("CreateTable departments failed: %v", err)
	}

	if err := eng.CreateTable(&storage.TableSchema{
		Name: "employees",
		Columns: []storage.Column{
			{Name: "tenant_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "emp_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "dept_id", Type: storage.TypeInt, Nullable: false},
			{Name: "role", Type: storage.TypeVarChar, Nullable: false},
			{Name: "salary", Type: storage.TypeInt, Nullable: false},
			{Name: "bio", Type: storage.TypeVarChar, Nullable: true},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_emp_dept",
				ChildColumns:  []string{"dept_id"},
				ParentTable:   "departments",
				ParentColumns: []string{"dept_id"},
				OnDelete:      storage.ActionCascade,
			},
		},
	}); err != nil {
		t.Fatalf("CreateTable employees failed: %v", err)
	}

	if err := eng.CreateTable(&storage.TableSchema{
		Name: "tasks",
		Columns: []storage.Column{
			{Name: "task_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "dept_id", Type: storage.TypeInt, Nullable: true},
			{Name: "title", Type: storage.TypeVarChar, Nullable: false},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_task_dept",
				ChildColumns:  []string{"dept_id"},
				ParentTable:   "departments",
				ParentColumns: []string{"dept_id"},
				OnDelete:      storage.ActionSetNull,
			},
		},
	}); err != nil {
		t.Fatalf("CreateTable tasks failed: %v", err)
	}

	if err := eng.CreateIndex(&storage.IndexSchema{
		Name:      "idx_emp_tenant_role",
		TableName: "employees",
		Columns:   []string{"tenant_id", "role"},
		Unique:    true,
	}); err != nil {
		t.Fatalf("CreateIndex unique composite failed: %v", err)
	}

	if err := eng.CreateIndex(&storage.IndexSchema{
		Name:      "idx_emp_dept_salary",
		TableName: "employees",
		Columns:   []string{"dept_id", "salary"},
		Unique:    false,
	}); err != nil {
		t.Fatalf("CreateIndex secondary composite failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx1, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx1.Insert("departments", storage.Record{"dept_id": storage.IntValue(10), "code": storage.StringValue("ENG")})
	_ = tx1.Insert("departments", storage.Record{"dept_id": storage.IntValue(20), "code": storage.StringValue("OPS")})

	_ = tx1.Insert("employees", storage.Record{"tenant_id": storage.IntValue(1), "emp_id": storage.IntValue(101), "dept_id": storage.IntValue(10), "role": storage.StringValue("lead"), "salary": storage.IntValue(150), "bio": storage.StringValue("kernel-architecture-engineer")})
	_ = tx1.Insert("employees", storage.Record{"tenant_id": storage.IntValue(1), "emp_id": storage.IntValue(102), "dept_id": storage.IntValue(10), "role": storage.StringValue("staff"), "salary": storage.IntValue(200), "bio": storage.NullValue(storage.TypeVarChar)})
	_ = tx1.Insert("employees", storage.Record{"tenant_id": storage.IntValue(2), "emp_id": storage.IntValue(101), "dept_id": storage.IntValue(20), "role": storage.StringValue("lead"), "salary": storage.IntValue(120), "bio": storage.StringValue("sre")})

	_ = tx1.Insert("tasks", storage.Record{"task_id": storage.IntValue(1), "dept_id": storage.IntValue(10), "title": storage.StringValue("compact-audit")})
	_ = tx1.Insert("tasks", storage.Record{"task_id": storage.IntValue(2), "dept_id": storage.IntValue(20), "title": storage.StringValue("keep-task")})
	if err := tx1.Commit(); err != nil {
		t.Fatalf("tx1 commit failed: %v", err)
	}

	// Duplicate composite unique key must reject cleanly
	dupTx, _ := sess.BeginTx(storage.TxOptions{})
	err = dupTx.Insert("employees", storage.Record{
		"tenant_id": storage.IntValue(1),
		"emp_id":    storage.IntValue(103),
		"dept_id":   storage.IntValue(10),
		"role":      storage.StringValue("lead"),
		"salary":    storage.IntValue(180),
		"bio":       storage.NullValue(storage.TypeVarChar),
	})
	if err == nil {
		err = dupTx.Commit()
	} else {
		_ = dupTx.Rollback()
	}
	if !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("expected ErrUniqueViolation on composite unique index, got %v", err)
	}

	// Composite SkipList range scan [dept=10, salary=100]..[dept=10, salary=250] + ArenaRowCursor
	readTx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	low := compositeKeyValue(storage.IntValue(10), storage.IntValue(100))
	high := compositeKeyValue(storage.IntValue(10), storage.IntValue(250))
	idxCur, err := readTx.IndexScan("employees", "idx_emp_dept_salary", &low, &high)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	arena := storage.GetArena()
	arenaCur := idxCur.(storage.ArenaRowCursor)
	var salaries []int64
	for idxCur.Next() {
		r, err := arenaCur.RowWithArena(arena)
		if err != nil {
			t.Fatalf("RowWithArena failed: %v", err)
		}
		salaries = append(salaries, r.Values[4].IVal)
	}
	_ = idxCur.Close()
	storage.PutArena(arena)
	_ = readTx.Rollback()
	if len(salaries) != 2 || salaries[0] != 150 || salaries[1] != 200 {
		t.Fatalf("unexpected composite SkipList range results: %v", salaries)
	}

	// RenameTable + cascade/set-null FK enforcement across FlatRecords
	if err := eng.RenameTable("departments", "org_units"); err != nil {
		t.Fatalf("RenameTable failed: %v", err)
	}
	if refs := eng.GetChildFKRefs("org_units"); len(refs) != 2 {
		t.Fatalf("expected 2 FK children on renamed org_units, got %d", len(refs))
	}

	delTx, _ := sess.BeginTx(storage.TxOptions{})
	if err := delTx.Delete("org_units", storage.IntValue(10)); err != nil {
		t.Fatalf("Delete parent dept 10 failed: %v", err)
	}
	if err := delTx.Commit(); err != nil {
		t.Fatalf("Delete commit failed: %v", err)
	}

	// Verify employees in dept 10 cascaded away while dept 20 stayed intact, and task 1 became NULL
	checkTx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	scanCur, _ := checkTx.Scan("employees")
	empCount := 0
	for scanCur.Next() {
		rec, err := scanCur.Record()
		if err != nil {
			t.Fatalf("Scan Record failed: %v", err)
		}
		if rec["dept_id"].IVal != 20 || rec["tenant_id"].IVal != 2 {
			t.Fatalf("expected only dept 20 survivor after CASCADE, got %+v", rec)
		}
		empCount++
	}
	_ = scanCur.Close()
	if empCount != 1 {
		t.Fatalf("expected 1 employee after CASCADE, got %d", empCount)
	}
	t1, err := checkTx.Get("tasks", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get task 1 failed: %v", err)
	}
	if !t1["dept_id"].IsNull {
		t.Fatalf("expected task 1 dept_id=NULL after ON DELETE SET NULL, got %+v", t1["dept_id"])
	}
	_ = checkTx.Rollback()

	// AddColumn must preserve MemoryCompact layout & convert rows in-place
	if err := eng.AddColumn("employees", &storage.Column{
		Name:    "badge",
		Type:    storage.TypeVarChar,
		Default: "'B-1'",
	}); err != nil {
		t.Fatalf("AddColumn employees failed: %v", err)
	}
	eng.mu.RLock()
	empTD := eng.tables["employees"]
	eng.mu.RUnlock()
	assertAllEntriesFlat(t, empTD)

	// TruncateTable must wipe shards and SkipList index entries cleanly so prior keys can be reused
	if err := eng.TruncateTable("employees"); err != nil {
		t.Fatalf("TruncateTable failed: %v", err)
	}
	reinsTx, _ := sess.BeginTx(storage.TxOptions{})
	if err := reinsTx.Insert("employees", storage.Record{
		"tenant_id": storage.IntValue(1),
		"emp_id":    storage.IntValue(101),
		"dept_id":   storage.IntValue(20),
		"role":      storage.StringValue("lead"),
		"salary":    storage.IntValue(300),
		"bio":       storage.StringValue(fmt.Sprintf("rebuilt-%d", 1)),
		"badge":     storage.StringValue("B-2"),
	}); err != nil {
		t.Fatalf("re-insert after TruncateTable failed: %v", err)
	}
	if err := reinsTx.Commit(); err != nil {
		t.Fatalf("reinsTx commit failed: %v", err)
	}
}

func TestCompact_ExtendedDataTypes_DateAndTimeRoundTrip(t *testing.T) {
	eng := New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "extended_events",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "ev_uuid", Type: storage.TypeUUID},
			{Name: "ev_date", Type: storage.TypeDate},
			{Name: "ev_time", Type: storage.TypeTime},
			{Name: "ev_bin", Type: storage.TypeBinary},
			{Name: "ev_json", Type: storage.TypeJSON},
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

	tx, _ := sess.BeginTx(storage.TxOptions{})
	if err := tx.Insert("extended_events", storage.Record{
		"id":      storage.IntValue(1),
		"ev_uuid": storage.UUIDValue("A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11"),
		"ev_date": storage.DateValueFromString("2026-10-01"),
		"ev_time": storage.TimeValueFromString("14:30:00"),
		"ev_bin":  storage.BinaryValue([]byte{0xde, 0xad, 0xbe, 0xef}),
		"ev_json": storage.JSONValue(`{"k":"v"}`),
	}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	readTx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()

	got, err := readTx.Get("extended_events", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got["ev_date"].String() != "2026-10-01" || got["ev_date"].SVal != "2026-10-01" {
		t.Fatalf("ev_date mismatch: %+v", got["ev_date"])
	}
	if got["ev_time"].String() != "14:30:00" || got["ev_time"].SVal != "14:30:00" {
		t.Fatalf("ev_time mismatch: %+v", got["ev_time"])
	}
	if got["ev_bin"].String() != "0xdeadbeef" {
		t.Fatalf("ev_bin mismatch: %+v", got["ev_bin"])
	}
	if got["ev_json"].SVal != `{"k":"v"}` {
		t.Fatalf("ev_json mismatch: %+v", got["ev_json"])
	}
}
