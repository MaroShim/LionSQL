package memengine

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"lionsql/pkg/storage"
)

func TestFlatRecord_RoundTripAllTypes(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "short_name", Type: storage.TypeVarChar}, // SSO <= 4 bytes
			{Name: "long_name", Type: storage.TypeVarChar},  // Heap > 4 bytes
			{Name: "balance", Type: storage.TypeFloat},
			{Name: "is_active", Type: storage.TypeBool},
			{Name: "created_at", Type: storage.TypeDateTime},
			{Name: "birthday", Type: storage.TypeDate},
			{Name: "metadata", Type: storage.TypeJSON},
			{Name: "salary", Type: storage.TypeDecimal},
			{Name: "uuid", Type: storage.TypeUUID},
			{Name: "avatar", Type: storage.TypeBinary},
			{Name: "extra_null", Type: storage.TypeVarChar, Nullable: true},
		},
	}

	layout := BuildFlatRecordLayout(schema)
	if layout == nil {
		t.Fatalf("BuildFlatRecordLayout returned nil")
	}

	now := time.Now().Truncate(time.Millisecond).UTC()
	bday := time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)

	rec := storage.Record{
		"id":         storage.IntValue(1001),
		"short_name": storage.StringValue("Lion"),
		"long_name":  storage.StringValue("LionSQL High Concurrency Engine"),
		"balance":    storage.FloatValue(9876.54),
		"is_active":  storage.BoolValue(true),
		"created_at": storage.DateTimeValue(now),
		"birthday":   storage.DateValue(bday),
		"metadata":   storage.JSONValue(`{"role":"admin","level":9}`),
		"salary":     storage.DecimalValue("123456.78"),
		"uuid":       storage.UUIDValue("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"),
		"avatar":     storage.BinaryValue([]byte{0xDE, 0xAD, 0xBE, 0xEF}),
		"extra_null": storage.NullValue(storage.TypeVarChar),
	}

	originalRow := schema.RecordToRow(rec)
	flatBuf, err := layout.EncodeRowToFlat(schema, originalRow)
	if err != nil {
		t.Fatalf("EncodeRowToFlat failed: %v", err)
	}

	decodedRow, err := layout.DecodeFlatToRow(schema, flatBuf)
	if err != nil {
		t.Fatalf("DecodeFlatToRow failed: %v", err)
	}

	// Verify values
	if len(decodedRow.Values) != len(originalRow.Values) {
		t.Fatalf("column count mismatch: got %d, want %d", len(decodedRow.Values), len(originalRow.Values))
	}

	for i, col := range schema.Columns {
		orig := originalRow.Values[i]
		dec := decodedRow.Values[i]

		if orig.IsNull != dec.IsNull {
			t.Fatalf("col %d (%s) NULL mismatch: orig=%v, dec=%v", i, col.Name, orig.IsNull, dec.IsNull)
		}
		if orig.IsNull {
			continue
		}

		switch col.Type {
		case storage.TypeInt:
			if orig.IVal != dec.IVal {
				t.Errorf("col %s: got %d, want %d", col.Name, dec.IVal, orig.IVal)
			}
		case storage.TypeFloat:
			if orig.FVal != dec.FVal {
				t.Errorf("col %s: got %f, want %f", col.Name, dec.FVal, orig.FVal)
			}
		case storage.TypeBool:
			if orig.BVal != dec.BVal {
				t.Errorf("col %s: got %v, want %v", col.Name, dec.BVal, orig.BVal)
			}
		case storage.TypeVarChar, storage.TypeJSON, storage.TypeUUID:
			if orig.SVal != dec.SVal {
				t.Errorf("col %s: got %q, want %q", col.Name, dec.SVal, orig.SVal)
			}
		case storage.TypeDateTime:
			if !orig.TVal.Equal(dec.TVal) {
				t.Errorf("col %s: got %v, want %v", col.Name, dec.TVal, orig.TVal)
			}
		case storage.TypeDate:
			if orig.TVal.Unix() != dec.TVal.Unix() {
				t.Errorf("col %s: got %v, want %v", col.Name, dec.TVal, orig.TVal)
			}
		case storage.TypeBinary:
			if orig.BinVal != dec.BinVal {
				t.Errorf("col %s binary mismatch", col.Name)
			}
		case storage.TypeDecimal:
			if orig.Decimal().Cmp(dec.Decimal()) != 0 {
				t.Errorf("col %s decimal mismatch: got %s, want %s", col.Name, dec.Decimal(), orig.Decimal())
			}
		}
	}
}

func BenchmarkFlatRecord_EncodeAndDecode(b *testing.B) {
	schema := &storage.TableSchema{
		Name: "bench_flat",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar}, // SSO <= 4 bytes
			{Name: "name", Type: storage.TypeVarChar}, // Heap string
			{Name: "balance", Type: storage.TypeFloat},
			{Name: "is_active", Type: storage.TypeBool},
			{Name: "created_at", Type: storage.TypeDateTime},
		},
	}
	layout := BuildFlatRecordLayout(schema)
	rec := storage.Record{
		"id":         storage.IntValue(1001),
		"code":       storage.StringValue("USD"),
		"name":       storage.StringValue("High Performance Enterprise Database"),
		"balance":    storage.FloatValue(12345.67),
		"is_active":  storage.BoolValue(true),
		"created_at": storage.DateTimeValue(time.Now().UTC()),
	}
	row := schema.RecordToRow(rec)

	b.Run("EncodeFlat", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = layout.EncodeRowToFlat(schema, row)
		}
	})

	flatBuf, _ := layout.EncodeRowToFlat(schema, row)
	b.Run("DecodeFlat", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = layout.DecodeFlatToRow(schema, flatBuf)
		}
	})

	b.Run("EncodeTuple_Compare", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = storage.EncodeRow(schema, row)
		}
	})

	tupleBuf, _ := storage.EncodeRow(schema, row)
	b.Run("DecodeTuple_Compare", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = storage.DecodeTupleToRow(schema, tupleBuf)
		}
	})
}

func TestMemEngine_ABModes_RawVsFlat(t *testing.T) {
	modes := []struct {
		name string
		mode OCCMode
	}{
		{"ModeRaw_Default", OCCModeRaw},
		{"ModeFlat_Compacted", OCCModeFlat},
	}

	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions("")
			opts.Mode = tc.mode
			eng := NewWithOptions(opts)
			_ = eng.Open("")
			defer eng.Close()

			schema := &storage.TableSchema{
				Name: "test_ab",
				Columns: []storage.Column{
					{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
					{Name: "name", Type: storage.TypeVarChar},
					{Name: "balance", Type: storage.TypeFloat},
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

			// 1. Insert 100 rows
			tx, _ := sess.BeginTx(storage.TxOptions{})
			for i := 1; i <= 100; i++ {
				_ = tx.Insert("test_ab", storage.Record{
					"id":      storage.IntValue(int64(i)),
					"name":    storage.StringValue(fmt.Sprintf("User_%d", i)),
					"balance": storage.FloatValue(float64(i) * 10.5),
				})
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit failed: %v", err)
			}

			// 2. Read back & verify
			txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
			defer txRead.Rollback()

			for i := 1; i <= 100; i++ {
				rec, err := txRead.Get("test_ab", storage.IntValue(int64(i)))
				if err != nil {
					t.Fatalf("Get id=%d failed: %v", i, err)
				}
				if rec["name"].SVal != fmt.Sprintf("User_%d", i) {
					t.Fatalf("unexpected name for id=%d: %s", i, rec["name"].SVal)
				}
			}

			// 3. Scan all rows
			cur, err := txRead.Scan("test_ab")
			if err != nil {
				t.Fatalf("Scan failed: %v", err)
			}
			count := 0
			for cur.Next() {
				count++
			}
			_ = cur.Close()
			if count != 100 {
				t.Fatalf("expected 100 rows, got %d", count)
			}
		})
	}
}

func BenchmarkMemEngine_AB_Insert(b *testing.B) {
	schema := &storage.TableSchema{
		Name: "bench_ab",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeFloat},
			{Name: "is_active", Type: storage.TypeBool},
			{Name: "created_at", Type: storage.TypeDateTime},
		},
	}

	b.Run("ModeRaw_Insert", func(b *testing.B) {
		opts := DefaultOptions("")
		opts.Mode = OCCModeRaw
		eng := NewWithOptions(opts)
		_ = eng.Open("")
		defer eng.Close()
		_ = eng.CreateTable(schema)
		sess, _ := eng.NewSession()
		defer sess.Close()

		now := time.Now().UTC()
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			tx, _ := sess.BeginTx(storage.TxOptions{})
			_ = tx.Insert("bench_ab", storage.Record{
				"id":         storage.IntValue(int64(i)),
				"code":       storage.StringValue("USD"),
				"name":       storage.StringValue("Enterprise Customer Account"),
				"balance":    storage.FloatValue(float64(i) * 1.5),
				"is_active":  storage.BoolValue(true),
				"created_at": storage.DateTimeValue(now),
			})
			_ = tx.Commit()
		}
	})

	b.Run("ModeFlat_Insert", func(b *testing.B) {
		opts := DefaultOptions("")
		opts.Mode = OCCModeFlat
		eng := NewWithOptions(opts)
		_ = eng.Open("")
		defer eng.Close()
		_ = eng.CreateTable(schema)
		sess, _ := eng.NewSession()
		defer sess.Close()

		now := time.Now().UTC()
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			tx, _ := sess.BeginTx(storage.TxOptions{})
			_ = tx.Insert("bench_ab", storage.Record{
				"id":         storage.IntValue(int64(i)),
				"code":       storage.StringValue("USD"),
				"name":       storage.StringValue("Enterprise Customer Account"),
				"balance":    storage.FloatValue(float64(i) * 1.5),
				"is_active":  storage.BoolValue(true),
				"created_at": storage.DateTimeValue(now),
			})
			_ = tx.Commit()
		}
	})
}

func BenchmarkMemEngine_AB_Scan(b *testing.B) {
	schema := &storage.TableSchema{
		Name: "bench_scan_ab",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeFloat},
		},
	}

	b.Run("ModeRaw_Scan1000", func(b *testing.B) {
		opts := DefaultOptions("")
		opts.Mode = OCCModeRaw
		eng := NewWithOptions(opts)
		_ = eng.Open("")
		defer eng.Close()
		_ = eng.CreateTable(schema)
		sess, _ := eng.NewSession()
		defer sess.Close()

		tx, _ := sess.BeginTx(storage.TxOptions{})
		for i := 1; i <= 1000; i++ {
			_ = tx.Insert("bench_scan_ab", storage.Record{
				"id":      storage.IntValue(int64(i)),
				"code":    storage.StringValue("KRW"),
				"name":    storage.StringValue("User Account Record"),
				"balance": storage.FloatValue(float64(i) * 100),
			})
		}
		_ = tx.Commit()

		txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
		defer txRead.Rollback()

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			cur, _ := txRead.Scan("bench_scan_ab")
			for cur.Next() {
				_, _ = cur.Row()
			}
			_ = cur.Close()
		}
	})

	b.Run("ModeFlat_Scan1000", func(b *testing.B) {
		opts := DefaultOptions("")
		opts.Mode = OCCModeFlat
		eng := NewWithOptions(opts)
		_ = eng.Open("")
		defer eng.Close()
		_ = eng.CreateTable(schema)
		sess, _ := eng.NewSession()
		defer sess.Close()

		tx, _ := sess.BeginTx(storage.TxOptions{})
		for i := 1; i <= 1000; i++ {
			_ = tx.Insert("bench_scan_ab", storage.Record{
				"id":      storage.IntValue(int64(i)),
				"code":    storage.StringValue("KRW"),
				"name":    storage.StringValue("User Account Record"),
				"balance": storage.FloatValue(float64(i) * 100),
			})
		}
		_ = tx.Commit()

		txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
		defer txRead.Rollback()

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			cur, _ := txRead.Scan("bench_scan_ab")
			for cur.Next() {
				_, _ = cur.Row()
			}
			_ = cur.Close()
		}
	})
}

func BenchmarkMemEngine_MemoryFootprint_100k(b *testing.B) {
	schema := &storage.TableSchema{
		Name: "bench_mem",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeFloat},
			{Name: "is_active", Type: storage.TypeBool},
		},
	}

	const totalRows = 10000

	b.Run("Raw_10k_Rows_Heap", func(b *testing.B) {
		opts := DefaultOptions("")
		opts.Mode = OCCModeRaw
		eng := NewWithOptions(opts)
		_ = eng.Open("")
		defer eng.Close()
		_ = eng.CreateTable(schema)
		sess, _ := eng.NewSession()
		defer sess.Close()

		tx, _ := sess.BeginTx(storage.TxOptions{})
		for i := 1; i <= totalRows; i++ {
			_ = tx.Insert("bench_mem", storage.Record{
				"id":        storage.IntValue(int64(i)),
				"code":      storage.StringValue("USD"),
				"name":      storage.StringValue(fmt.Sprintf("Customer Account %d", i)),
				"balance":   storage.FloatValue(float64(i) * 10.5),
				"is_active": storage.BoolValue(true),
			})
		}
		_ = tx.Commit()

		var m runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m)
		b.ReportMetric(float64(m.HeapAlloc)/(1024*1024), "MB_HeapAlloc")
		b.ReportMetric(float64(m.HeapObjects), "HeapObjects")
	})

	b.Run("Flat_10k_Rows_Heap", func(b *testing.B) {
		opts := DefaultOptions("")
		opts.Mode = OCCModeFlat
		eng := NewWithOptions(opts)
		_ = eng.Open("")
		defer eng.Close()
		_ = eng.CreateTable(schema)
		sess, _ := eng.NewSession()
		defer sess.Close()

		tx, _ := sess.BeginTx(storage.TxOptions{})
		for i := 1; i <= totalRows; i++ {
			_ = tx.Insert("bench_mem", storage.Record{
				"id":        storage.IntValue(int64(i)),
				"code":      storage.StringValue("USD"),
				"name":      storage.StringValue(fmt.Sprintf("Customer Account %d", i)),
				"balance":   storage.FloatValue(float64(i) * 10.5),
				"is_active": storage.BoolValue(true),
			})
		}
		_ = tx.Commit()

		var m runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m)
		b.ReportMetric(float64(m.HeapAlloc)/(1024*1024), "MB_HeapAlloc")
		b.ReportMetric(float64(m.HeapObjects), "HeapObjects")
	})
}

func TestMemoryUsageCalculation_RawVsFlat(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "measure",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeFloat},
			{Name: "is_active", Type: storage.TypeBool},
			{Name: "created_at", Type: storage.TypeDateTime},
			{Name: "metadata", Type: storage.TypeJSON},
			{Name: "salary", Type: storage.TypeDecimal},
		},
	}

	layout := BuildFlatRecordLayout(schema)

	rec := storage.Record{
		"id":         storage.IntValue(1001),
		"code":       storage.StringValue("USD"),
		"name":       storage.StringValue("Acme Corporation Global Account"),
		"balance":    storage.FloatValue(99999.99),
		"is_active":  storage.BoolValue(true),
		"created_at": storage.DateTimeValue(time.Now().UTC()),
		"metadata":   storage.JSONValue(`{"plan":"enterprise","seats":500}`),
		"salary":     storage.DecimalValue("12500000.50"),
	}

	row := schema.RecordToRow(rec)

	// Measure Raw Row:
	// 8 * sizeof(Value) = 8 * 88 bytes = 704 bytes + slice header (24 bytes) = 728 bytes
	rawValSize := int(unsafe.Sizeof(storage.Value{}))
	rawTotal := len(row.Values)*rawValSize + int(unsafe.Sizeof(row.Values))
	for _, v := range row.Values {
		rawTotal += len(v.SVal)
	}

	// Measure Flat Record:
	flatBuf, err := layout.EncodeRowToFlat(schema, row)
	if err != nil {
		t.Fatalf("EncodeRowToFlat failed: %v", err)
	}
	flatTotal := len(flatBuf) + int(unsafe.Sizeof(flatBuf)) // bytes + slice header (24 bytes)

	t.Logf("=== 8-Column Row Memory Comparison ===")
	t.Logf("Raw Row Structure Memory : %d bytes", rawTotal)
	t.Logf("Flat Record Buffer Memory: %d bytes", flatTotal)
	t.Logf("Memory Savings Ratio     : %.2fx compression", float64(rawTotal)/float64(flatTotal))

	if flatTotal >= rawTotal {
		t.Errorf("expected flat record (%d bytes) to be smaller than raw row (%d bytes)", flatTotal, rawTotal)
	}
}

func TestShardEntrySizeInspection(t *testing.T) {
	t.Logf("sizeof(shardEntry) = %d bytes", unsafe.Sizeof(shardEntry{}))
	t.Logf("sizeof(storage.Row) = %d bytes", unsafe.Sizeof(storage.Row{}))
	t.Logf("sizeof([]byte)      = %d bytes", unsafe.Sizeof([]byte{}))
}

func TestVerifyTableMode_FlatBufferStored(t *testing.T) {
	opts := DefaultOptions("")
	opts.Mode = OCCModeFlat
	eng := NewWithOptions(opts)
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "test_mode",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	_ = eng.CreateTable(schema)

	sess, _ := eng.NewSession()
	defer sess.Close()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("test_mode", storage.Record{
		"id":   storage.IntValue(1),
		"name": storage.StringValue("hello"),
	})
	_ = tx.Commit()

	td := eng.tables["test_mode"]
	sIdx := hashShard("1")
	sh := &td.shards[sIdx]
	sh.mu.RLock()
	entry := sh.entries["1"]
	sh.mu.RUnlock()

	t.Logf("Mode = %v", td.mode)
	t.Logf("Entry row.Values len = %d", len(entry.row.Values))
	t.Logf("Entry flatBuf len    = %d", len(entry.flatBuf))

	if td.mode != OCCModeFlat {
		t.Fatalf("expected td.mode == OCCModeFlat, got %v", td.mode)
	}
	if len(entry.flatBuf) == 0 {
		t.Fatalf("expected entry.flatBuf to be non-empty, but was 0")
	}
	if len(entry.row.Values) != 0 {
		t.Fatalf("expected entry.row.Values to be 0 in flat mode, but got %d", len(entry.row.Values))
	}
}

func TestFlatRecord_ConcurrentReadWriteRace(t *testing.T) {
	opts := DefaultOptions("")
	opts.Mode = OCCModeFlat
	eng := NewWithOptions(opts)
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "test_concurrent_race",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	const numGoroutines = 8
	const opsPerWorker = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(workerID int) {
			defer wg.Done()
			sess, err := eng.NewSession()
			if err != nil {
				return
			}
			defer sess.Close()

			for i := 0; i < opsPerWorker; i++ {
				rowID := int64(workerID*1000 + i)
				tx, err := sess.BeginTx(storage.TxOptions{})
				if err != nil {
					continue
				}
				_ = tx.Insert("test_concurrent_race", storage.Record{
					"id":  storage.IntValue(rowID),
					"val": storage.StringValue(fmt.Sprintf("worker_%d_payload_%d", workerID, i)),
				})
				_ = tx.Commit()

				// Concurrent read
				rtx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
				if err == nil {
					_, _ = rtx.Get("test_concurrent_race", storage.IntValue(rowID))
					_ = rtx.Rollback()
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestFlatRecord_CorruptedBufferExplicitError(t *testing.T) {
	opts := DefaultOptions("")
	opts.Mode = OCCModeFlat
	eng := NewWithOptions(opts)
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "test_corrupt",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	_ = eng.CreateTable(schema)

	td := eng.tables["test_corrupt"]
	// Manually inject corrupted flatBuf with impossible short length
	sIdx := hashShard("bad_key")
	sh := &td.shards[sIdx]
	sh.mu.Lock()
	sh.entries["bad_key"] = shardEntry{
		ver:     1,
		flatBuf: []byte{0xFF, 0x01}, // truncated buffer, shorter than layout min size
	}
	sh.mu.Unlock()

	// getRowAndVer must return false without panic or returning empty corrupted row
	_, _, ok := td.getRowAndVer("bad_key")
	if ok {
		t.Fatalf("expected getRowAndVer to fail on corrupted flatBuf, but got ok=true")
	}
}

// TestFlatRecord_Regression_P0_01_HeapOffsetTagCollisionNoCorruption verifies that when
// heap offset vOff has byte 1 set to 0xFF (e.g. vOff = 65280, 0x0000FF00), the decoder does NOT
// mistake the heap pointer for an inlined small string and corrupt data (P0-01).
func TestFlatRecord_Regression_P0_01_HeapOffsetTagCollisionNoCorruption(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "collision_table",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "col_padding", Type: storage.TypeVarChar},
			{Name: "target_col", Type: storage.TypeVarChar},
		},
	}

	layout := BuildFlatRecordLayout(schema)
	if layout == nil {
		t.Fatalf("failed to build flat record layout")
	}

	// Create padding of exactly 65280 bytes (0x0000FF00) so that the subsequent column's
	// heap offset vOff will have byte 1 equal to 0xFF.
	paddingBytes := make([]byte, 65280)
	for i := range paddingBytes {
		paddingBytes[i] = 'A'
	}
	paddingStr := string(paddingBytes)
	expectedTarget := "ExactTargetStringValueThatMustNotBeCorrupted"

	row := storage.Row{
		Values: []storage.Value{
			storage.IntValue(1),
			storage.StringValue(paddingStr),
			storage.StringValue(expectedTarget),
		},
	}

	flatBuf, err := layout.EncodeRowToFlat(schema, row)
	if err != nil {
		t.Fatalf("EncodeRowToFlat failed: %v", err)
	}

	decodedRow, err := layout.DecodeFlatToRow(schema, flatBuf)
	if err != nil {
		t.Fatalf("[CRITICAL P0-01 REGRESSION] DecodeFlatToRow failed due to heap offset collision: %v", err)
	}

	if decodedRow.Values[2].SVal != expectedTarget {
		t.Fatalf("[CRITICAL P0-01 REGRESSION] target_col corrupted! Got %q, want %q",
			decodedRow.Values[2].SVal, expectedTarget)
	}
}

// TestFlatRecord_Regression_P0_02_SnapshotSyncMaterialization verifies that
// in OCCModeFlat, syncShardsToMapsLocked materializes rows from flatBuf rather than
// populating td.Rows with empty unmaterialized row shells (P0-02).
func TestFlatRecord_Regression_P0_02_SnapshotSyncMaterialization(t *testing.T) {
	opts := DefaultOptions("")
	opts.Mode = OCCModeFlat
	eng := NewWithOptions(opts)
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "accounts_flat",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "owner", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeFloat},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	err := tx.Insert("accounts_flat", storage.Record{
		"id":      storage.IntValue(101),
		"owner":   storage.StringValue("AliceCorp"),
		"balance": storage.FloatValue(54321.0),
	})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	td := eng.tables["accounts_flat"]
	if td.mode != OCCModeFlat {
		t.Fatalf("expected table mode OCCModeFlat, got %v", td.mode)
	}

	// Trigger snapshot synchronization
	td.syncShardsToMapsLocked()

	// Verify that td.Rows contains materialized data rather than an empty row shell
	row, exists := td.Rows["101"]
	if !exists {
		t.Fatalf("expected row '101' in td.Rows after snapshot sync")
	}

	if len(row.Values) == 0 {
		t.Fatalf("[CRITICAL P0-02 REGRESSION] td.Rows['101'] contains empty unmaterialized shell! Data evaporated on snapshot sync")
	}

	if row.Values[0].IVal != 101 || row.Values[1].SVal != "AliceCorp" || row.Values[2].FVal != 54321.0 {
		t.Fatalf("row values corrupted or empty: %+v", row.Values)
	}
}


