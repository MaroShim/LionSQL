package memengine

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"lionsql/pkg/storage"
)

func TestCompactAudit_F1_ArenaPutDoesNotMutateDecodedStrings(t *testing.T) {
	opts := DefaultOptions("")
	opts.Mode = MemoryCompact
	eng := NewWithOptions(opts)
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "f1_arena_uaf",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "sso_txt", Type: storage.TypeVarChar},
			{Name: "heap_txt", Type: storage.TypeVarChar},
			{Name: "payload", Type: storage.TypeJSON},
			{Name: "uid", Type: storage.TypeUUID},
			{Name: "amt", Type: storage.TypeDecimal, Precision: 12, Scale: 2},
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

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	const (
		wantSSO  = "alice"
		wantHeap = "alice_original_secret_value_12345"
		wantJSON = `{"owner":"alice_original","tier":"gold"}`
		wantUUID = "550e8400-e29b-41d4-a716-446655440000"
		wantDec  = "12345.67"
	)
	if err := tx.Insert("f1_arena_uaf", storage.Record{
		"id":       storage.IntValue(1),
		"sso_txt":  storage.StringValue(wantSSO),
		"heap_txt": storage.StringValue(wantHeap),
		"payload":  storage.JSONValue(wantJSON),
		"uid":      storage.UUIDValue(wantUUID),
		"amt":      storage.DecimalValue(wantDec),
	}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	readTx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx read failed: %v", err)
	}
	defer readTx.Rollback()

	cur, err := readTx.Scan("f1_arena_uaf")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	arenaCur := cur.(storage.ArenaRowCursor)
	arena1 := storage.GetArena()
	if !cur.Next() {
		cur.Close()
		storage.PutArena(arena1)
		t.Fatalf("expected 1 row")
	}
	row, err := arenaCur.RowWithArena(arena1)
	if err != nil {
		cur.Close()
		storage.PutArena(arena1)
		t.Fatalf("RowWithArena failed: %v", err)
	}
	_ = cur.Close()

	gotSSO := row.Values[1].SVal
	gotHeap := row.Values[2].SVal
	gotJSON := row.Values[3].SVal
	gotUUID := row.Values[4].SVal
	gotDec := row.Values[5].String()

	// Return arena1 to pool, then acquire another arena and overwrite its pooled slab.
	storage.PutArena(arena1)

	arena2 := storage.GetArena()
	defer storage.PutArena(arena2)
	_ = arena2.AllocStringBytes([]byte("XXXXX_OVERWRITTEN_BY_SECOND_QUERY_XXXXX"))
	_ = arena2.AllocStringBytes([]byte("YYYYY_OVERWRITTEN_SECOND_SLICE_YYYYY_12345"))
	_ = arena2.AllocStringBytes([]byte(`{"corrupted":"overwrite_slab_contents_12345"}`))

	if gotSSO != wantSSO {
		t.Fatalf("SSO string corrupted after PutArena: got %q, want %q", gotSSO, wantSSO)
	}
	if gotHeap != wantHeap {
		t.Fatalf("VarChar string corrupted after PutArena: got %q, want %q", gotHeap, wantHeap)
	}
	if gotJSON != wantJSON {
		t.Fatalf("JSON string corrupted after PutArena: got %q, want %q", gotJSON, wantJSON)
	}
	if gotUUID != wantUUID {
		t.Fatalf("UUID string corrupted after PutArena: got %q, want %q", gotUUID, wantUUID)
	}
	if gotDec != wantDec {
		t.Fatalf("Decimal string corrupted after PutArena: got %q, want %q", gotDec, wantDec)
	}
}

func TestCompactAudit_F3_ConcurrentScanAndDDLRace(t *testing.T) {
	opts := DefaultOptions("")
	opts.Mode = MemoryCompact
	eng := NewWithOptions(opts)
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "f3_scan_ddl",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "a", Type: storage.TypeVarChar, Nullable: true},
			{Name: "b", Type: storage.TypeVarChar, Nullable: true},
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

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := 1; i <= 20; i++ {
		if err := tx.Insert("f3_scan_ddl", storage.Record{
			"id": storage.IntValue(int64(i)),
			"a":  storage.StringValue(fmt.Sprintf("alpha_%02d_long_string_value", i)),
			"b":  storage.StringValue(fmt.Sprintf("beta_%02d_long_string_value", i)),
		}); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	stopCh := make(chan struct{})
	errCh := make(chan error, 8)
	var wg sync.WaitGroup

	// Reader goroutine continuously scans and decodes with Arena & Row/Record
	wg.Add(1)
	go func() {
		defer wg.Done()
		rSess, err := eng.NewSession()
		if err != nil {
			errCh <- err
			return
		}
		defer rSess.Close()
		arena := storage.GetArena()
		defer storage.PutArena(arena)

		for {
			select {
			case <-stopCh:
				return
			default:
			}
			rtx, err := rSess.BeginTx(storage.TxOptions{ReadOnly: true})
			if err != nil {
				errCh <- err
				return
			}
			cur, err := rtx.Scan("f3_scan_ddl")
			if err != nil {
				_ = rtx.Rollback()
				errCh <- err
				return
			}
			ac := cur.(storage.ArenaRowCursor)
			arena.Reset()
			for cur.Next() {
				row, err := ac.RowWithArena(arena)
				if err != nil {
					_ = cur.Close()
					_ = rtx.Rollback()
					errCh <- fmt.Errorf("RowWithArena failed during concurrent DDL: %w", err)
					return
				}
				if len(row.Values) < 2 {
					_ = cur.Close()
					_ = rtx.Rollback()
					errCh <- fmt.Errorf("unexpected decoded column count %d", len(row.Values))
					return
				}
				if _, err := cur.Record(); err != nil {
					_ = cur.Close()
					_ = rtx.Rollback()
					errCh <- fmt.Errorf("Record failed during concurrent DDL: %w", err)
					return
				}
			}
			_ = cur.Close()
			_ = rtx.Rollback()
		}
	}()

	// Writer goroutine executes AddColumn & DROP COLUMN concurrently with active cursors
	for iter := 0; iter < 15; iter++ {
		colName := fmt.Sprintf("extra_%d", iter)
		if err := eng.AddColumn("f3_scan_ddl", &storage.Column{
			Name:     colName,
			Type:     storage.TypeVarChar,
			Nullable: true,
			Default:  "'def_val'",
		}); err != nil {
			close(stopCh)
			t.Fatalf("AddColumn(%s) failed: %v", colName, err)
		}
		if err := eng.AlterTable("f3_scan_ddl", func(sch *storage.TableSchema) error {
			for i, c := range sch.Columns {
				if c.Name == colName {
					sch.Columns = append(sch.Columns[:i], sch.Columns[i+1:]...)
					break
				}
			}
			return nil
		}); err != nil {
			close(stopCh)
			t.Fatalf("AlterTable DROP COLUMN(%s) failed: %v", colName, err)
		}
	}
	close(stopCh)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent reader error: %v", err)
	}
}

func TestCompactAudit_F3_ConcurrentIndexScanAndDDLRace(t *testing.T) {
	opts := DefaultOptions("")
	opts.Mode = MemoryCompact
	eng := NewWithOptions(opts)
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "f3_idx_ddl",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "k", Type: storage.TypeInt, Nullable: false},
			{Name: "payload", Type: storage.TypeVarChar, Nullable: true},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_f3_k", TableName: "f3_idx_ddl", Columns: []string{"k"}},
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

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := 1; i <= 15; i++ {
		if err := tx.Insert("f3_idx_ddl", storage.Record{
			"id":      storage.IntValue(int64(i)),
			"k":       storage.IntValue(int64(i)),
			"payload": storage.StringValue(fmt.Sprintf("item_%02d_payload_bytes", i)),
		}); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	stopCh := make(chan struct{})
	errCh := make(chan error, 8)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		rSess, err := eng.NewSession()
		if err != nil {
			errCh <- err
			return
		}
		defer rSess.Close()
		arena := storage.GetArena()
		defer storage.PutArena(arena)
		low := storage.IntValue(1)
		high := storage.IntValue(15)

		for {
			select {
			case <-stopCh:
				return
			default:
			}
			rtx, err := rSess.BeginTx(storage.TxOptions{ReadOnly: true})
			if err != nil {
				errCh <- err
				return
			}
			cur, err := rtx.IndexScan("f3_idx_ddl", "idx_f3_k", &low, &high)
			if err != nil {
				_ = rtx.Rollback()
				errCh <- err
				return
			}
			ac := cur.(storage.ArenaRowCursor)
			arena.Reset()
			for cur.Next() {
				if _, err := ac.RowWithArena(arena); err != nil {
					_ = cur.Close()
					_ = rtx.Rollback()
					errCh <- fmt.Errorf("IndexScan RowWithArena failed during concurrent DDL: %w", err)
					return
				}
				if _, err := cur.Record(); err != nil {
					_ = cur.Close()
					_ = rtx.Rollback()
					errCh <- fmt.Errorf("IndexScan Record failed during concurrent DDL: %w", err)
					return
				}
			}
			_ = cur.Close()
			_ = rtx.Rollback()
		}
	}()

	for iter := 0; iter < 15; iter++ {
		colName := fmt.Sprintf("idx_extra_%d", iter)
		if err := eng.AddColumn("f3_idx_ddl", &storage.Column{
			Name:     colName,
			Type:     storage.TypeVarChar,
			Nullable: true,
			Default:  "'extra_val'",
		}); err != nil {
			close(stopCh)
			t.Fatalf("AddColumn failed: %v", err)
		}
	}
	close(stopCh)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent index reader error: %v", err)
	}
}

func TestCompactAudit_F3_AddColumnFailClosedOnDecodeError(t *testing.T) {
	opts := DefaultOptions("")
	opts.Mode = MemoryCompact
	eng := NewWithOptions(opts)
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "f3_corrupt",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: true},
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
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx.Insert("f3_corrupt", storage.Record{
		"id":   storage.IntValue(1),
		"name": storage.StringValue("alice"),
	}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	td := eng.tables["f3_corrupt"]
	pk := "1"
	sh := &td.shards[hashShard(pk)]
	sh.mu.Lock()
	sh.entries[pk] = shardEntry{ver: 1, flatBuf: []byte{0xff, 0x01}}
	sh.mu.Unlock()

	err = eng.AddColumn("f3_corrupt", &storage.Column{
		Name:     "age",
		Type:     storage.TypeInt,
		Nullable: true,
		Default:  "42",
	})
	if err == nil {
		t.Fatalf("expected AddColumn to fail closed when existing flatBuf decode fails, got nil")
	}

	// Verify live Schema & existing entry were NOT overwritten
	if len(td.Schema.Columns) != 2 {
		t.Fatalf("expected Schema.Columns rollback to 2 columns, got %d", len(td.Schema.Columns))
	}
	sh.mu.RLock()
	gotEntry := sh.entries[pk]
	sh.mu.RUnlock()
	if len(gotEntry.row.Values) != 0 {
		t.Fatalf("expected fail-closed rollback without overwriting shard entry row, got %+v", gotEntry.row.Values)
	}
}

func TestCompactAudit_F3_AddColumnWALFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.Mode = MemoryCompact
	eng := NewWithOptions(opts)
	if err := eng.Open(dir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "f3_wal_rb",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: true},
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
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx.Insert("f3_wal_rb", storage.Record{
		"id":   storage.IntValue(1),
		"name": storage.StringValue("alice"),
	}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Close WAL to force deterministic WAL write error during AddColumn.
	if err := eng.wal.Close(); err != nil {
		t.Fatalf("wal.Close failed: %v", err)
	}
	err = eng.AddColumn("f3_wal_rb", &storage.Column{
		Name:     "extra",
		Type:     storage.TypeVarChar,
		Nullable: true,
		Default:  "'fallback'",
	})
	if err == nil {
		t.Fatalf("expected AddColumn to return WAL write error")
	}

	td := eng.tables["f3_wal_rb"]
	if len(td.Schema.Columns) != 2 {
		t.Fatalf("expected Schema.Columns rolled back to 2 on WAL failure, got %d", len(td.Schema.Columns))
	}
	if td.layout.NumColumns != 2 {
		t.Fatalf("expected FlatRecordLayout rolled back to 2 columns on WAL failure, got %d", td.layout.NumColumns)
	}
	row, _, ok := td.getRowAndVer("1")
	if !ok || len(row.Values) != 2 || row.Values[1].SVal != "alice" {
		t.Fatalf("expected row intact after WAL rollback, got ok=%v row=%+v", ok, row)
	}
}

func TestCompactAudit_F4_VarSlotRejectsOver16MiB(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "f4_16mib",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "payload", Type: storage.TypeVarChar, Nullable: true},
		},
	}
	layout := BuildFlatRecordLayout(schema)

	// 1. Exact boundary (16 MiB - 1 = 0xFFFFFF bytes) must succeed cleanly
	maxPayload := strings.Repeat("a", (1<<24)-1)
	okBuf, err := layout.EncodeRowToFlat(schema, storage.Row{
		Values: []storage.Value{storage.IntValue(1), storage.StringValue(maxPayload)},
	})
	if err != nil {
		t.Fatalf("expected max valid payload (16MiB-1) to encode without error, got: %v", err)
	}
	okRow, err := layout.DecodeFlatToRow(schema, okBuf)
	if err != nil {
		t.Fatalf("DecodeFlatToRow failed on 16MiB-1 payload: %v", err)
	}
	if len(okRow.Values[1].SVal) != len(maxPayload) {
		t.Fatalf("decoded length mismatch at boundary: got %d, want %d", len(okRow.Values[1].SVal), len(maxPayload))
	}

	// 2. 16 MiB (1 << 24 bytes) must fail closed instead of silently truncating uint24 vLen
	oversized := strings.Repeat("b", 1<<24)
	if _, err := layout.EncodeRowToFlat(schema, storage.Row{
		Values: []storage.Value{storage.IntValue(2), storage.StringValue(oversized)},
	}); !errors.Is(err, ErrFlatRecordTooLarge) {
		t.Fatalf("expected ErrFlatRecordTooLarge for >= 16 MiB variable value, got: %v", err)
	}

	// Verify 32-bit varHeap offset limit guard without allocating 4 GiB in test RAM
	if err := validateVarSlotBounds(math.MaxUint32-10, 10, math.MaxUint32); err != nil {
		t.Fatalf("expected exact 4 GiB boundary sum to succeed, got: %v", err)
	}
	if err := validateVarSlotBounds(math.MaxUint32-10, 11, math.MaxUint32); !errors.Is(err, ErrFlatRecordTooLarge) {
		t.Fatalf("expected ErrFlatRecordTooLarge on varHeap 32-bit offset overflow, got: %v", err)
	}

	// 3. Transaction Commit path must also reject >= 16 MiB and leave table empty
	opts := DefaultOptions("")
	opts.Mode = MemoryCompact
	eng := NewWithOptions(opts)
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx.Insert("f4_16mib", storage.Record{
		"id":      storage.IntValue(1),
		"payload": storage.StringValue(oversized),
	}); err != nil {
		t.Fatalf("Insert failed unexpectedly: %v", err)
	}
	if err := tx.Commit(); err == nil {
		t.Fatalf("expected Commit to reject >= 16 MiB variable payload")
	}
	if cnt := eng.tables["f4_16mib"].rowCount(); cnt != 0 {
		t.Fatalf("expected failed Commit to leave 0 rows, got %d", cnt)
	}
}

func TestCompactAudit_F5_SnapshotFailClosedOnCorruptEntry(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.Mode = MemoryCompact
	eng := NewWithOptions(opts)
	if err := eng.Open(dir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "f5_snap_corrupt",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: true},
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
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx.Insert("f5_snap_corrupt", storage.Record{
		"id":   storage.IntValue(1),
		"name": storage.StringValue("alice"),
	}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	td := eng.tables["f5_snap_corrupt"]
	sh := &td.shards[hashShard("1")]
	sh.mu.Lock()
	sh.entries["1"] = shardEntry{ver: 1, flatBuf: []byte{0xff, 0x01}}
	sh.mu.Unlock()

	// Direct sync must fail closed rather than filling td.Rows with empty unmaterialized shell
	if err := td.syncShardsToMapsLocked(); err == nil {
		t.Fatalf("expected syncShardsToMapsLocked to fail closed on corrupt flatBuf, got nil")
	}

	// Full snapshot dump must also fail closed rather than committing corrupted tables.mpk
	if err := eng.SaveSnapshot(); err == nil {
		t.Fatalf("expected SaveSnapshot to fail when table has corrupt flatBuf")
	}
}

func TestCompactAudit_F6_EncodeRowToFlatDefensiveTypeCheck(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "f6_type_check",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "f", Type: storage.TypeFloat, Nullable: true},
			{Name: "d", Type: storage.TypeDate, Nullable: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: true},
		},
	}
	layout := BuildFlatRecordLayout(schema)

	// 1. Coercible mismatched types must be coerced properly:
	// id: "123" -> 123, f: 45 -> 45.0, d: "2026-11-11" -> Date(2026-11-11)
	coercibleRow := storage.Row{
		Values: []storage.Value{
			storage.StringValue("123"),
			storage.IntValue(45),
			storage.StringValue("2026-11-11"),
			storage.StringValue("alice"),
		},
	}
	buf, err := layout.EncodeRowToFlat(schema, coercibleRow)
	if err != nil {
		t.Fatalf("expected coercible values to encode successfully, got: %v", err)
	}
	decoded, err := layout.DecodeFlatToRow(schema, buf)
	if err != nil {
		t.Fatalf("DecodeFlatToRow failed: %v", err)
	}
	if decoded.Values[0].IVal != 123 || decoded.Values[1].FVal != 45.0 || decoded.Values[2].Type != storage.TypeDate {
		t.Fatalf("unexpected coerced decoded values: %+v", decoded.Values)
	}

	// 2. Uncoercible non-null values must fail closed rather than encoding zero-values silently
	badIntRow := storage.Row{
		Values: []storage.Value{
			storage.StringValue("not_a_number"),
			storage.FloatValue(1.23),
			storage.NullValue(storage.TypeDate),
			storage.StringValue("bob"),
		},
	}
	if _, err := layout.EncodeRowToFlat(schema, badIntRow); err == nil {
		t.Fatalf("expected EncodeRowToFlat to reject 'not_a_number' for TypeInt column, got nil")
	}

	badDateRow := storage.Row{
		Values: []storage.Value{
			storage.IntValue(2),
			storage.FloatValue(1.23),
			storage.StringValue("invalid_date_format"),
			storage.StringValue("carol"),
		},
	}
	if _, err := layout.EncodeRowToFlat(schema, badDateRow); err == nil {
		t.Fatalf("expected EncodeRowToFlat to reject 'invalid_date_format' for TypeDate column, got nil")
	}
}
