package execution_test

import (
	"context"
	"fmt"
	"io"
	"testing"

	"lionsql/pkg/execution"
	"lionsql/pkg/storage"
)

type mockRawCursor struct {
	records [][]byte
	idx     int
}

func (c *mockRawCursor) Next() bool {
	c.idx++
	return c.idx <= len(c.records)
}

func (c *mockRawCursor) RawTuple() ([]byte, bool) {
	if c.idx > 0 && c.idx <= len(c.records) {
		return c.records[c.idx-1], true
	}
	return nil, false
}

func (c *mockRawCursor) Record() (storage.Record, error) {
	return storage.Record{}, nil
}

func (c *mockRawCursor) Row() (storage.Row, error) {
	return storage.Row{}, nil
}

func (c *mockRawCursor) Err() error {
	return nil
}

func (c *mockRawCursor) Close() error {
	return nil
}

type mockScanTx struct {
	storage.Transaction
	cur storage.Cursor
}

func (m *mockScanTx) Scan(tableName string) (storage.Cursor, error) {
	return m.cur, nil
}

func createWideSchemaAndData(nCols, nRows int) (*storage.TableSchema, [][]byte) {
	cols := make([]storage.Column, nCols)
	cols[0] = storage.Column{Name: "id", Type: storage.TypeInt}
	cols[1] = storage.Column{Name: "name", Type: storage.TypeVarChar}
	for i := 2; i < nCols; i++ {
		cols[i] = storage.Column{Name: fmt.Sprintf("payload_%d", i), Type: storage.TypeVarChar}
	}
	schema := &storage.TableSchema{
		Name:    "wide_bench",
		Columns: cols,
	}

	records := make([][]byte, nRows)
	for r := 0; r < nRows; r++ {
		rec := make(storage.Record, nCols)
		rec["id"] = storage.IntValue(int64(r))
		rec["name"] = storage.StringValue(fmt.Sprintf("user_%d", r))
		for c := 2; c < nCols; c++ {
			rec[fmt.Sprintf("payload_%d", c)] = storage.StringValue(fmt.Sprintf("heavy_payload_data_string_for_column_%d_row_%d_padding_bytes_abcdef", c, r))
		}
		raw, err := storage.EncodeTuple(schema, rec)
		if err != nil {
			panic(err)
		}
		records[r] = raw
	}
	return schema, records
}

func TestProjectionPushdown_Correctness(t *testing.T) {
	schema, rawData := createWideSchemaAndData(10, 100)
	cur := &mockRawCursor{records: rawData}
	tx := &mockScanTx{cur: cur}

	iter := execution.NewSeqScanIterator(tx, "wide_bench", "w", schema)
	// Push down only col 0 (id) and col 1 (name)
	iter.SetRequiredCols([]int{0, 1})

	if err := iter.Open(context.Background()); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer iter.Close()

	count := 0
	for {
		tuple, err := iter.Next(context.Background())
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if tuple.Get("id") != int64(count) {
			t.Errorf("row %d: expected id %d, got %v", count, count, tuple.Get("id"))
		}
		expectedName := fmt.Sprintf("user_%d", count)
		if tuple.Get("name") != expectedName {
			t.Errorf("row %d: expected name %q, got %v", count, expectedName, tuple.Get("name"))
		}

		// Unselected columns should be nil
		if tuple.Get("payload_2") != nil {
			t.Errorf("row %d: expected unselected payload_2 to be nil, got %v", count, tuple.Get("payload_2"))
		}
		count++
	}

	if count != 100 {
		t.Fatalf("expected 100 rows, got %d", count)
	}
}

func BenchmarkScan_FullDecode_20Cols(b *testing.B) {
	schema, rawData := createWideSchemaAndData(20, 1000)
	arena := storage.GetArena()
	defer storage.PutArena(arena)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cur := &mockRawCursor{records: rawData}
		tx := &mockScanTx{cur: cur}
		iter := execution.NewSeqScanIterator(tx, "wide_bench", "w", schema)
		iter.SetArena(arena)
		_ = iter.Open(context.Background())

		var chunk execution.Chunk
		for {
			err := iter.NextChunk(context.Background(), &chunk)
			if err == io.EOF || chunk.Len() == 0 {
				break
			}
			// Simulate consuming all columns
			for _, t := range chunk.Tuples {
				_ = t.Get("id")
				_ = t.Get("name")
				_ = t.Get("payload_2")
				_ = t.Get("payload_19")
			}
		}
		_ = iter.Close()
	}
}

func BenchmarkScan_ProjectionPushdown_2of20Cols(b *testing.B) {
	schema, rawData := createWideSchemaAndData(20, 1000)
	arena := storage.GetArena()
	defer storage.PutArena(arena)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cur := &mockRawCursor{records: rawData}
		tx := &mockScanTx{cur: cur}
		iter := execution.NewSeqScanIterator(tx, "wide_bench", "w", schema)
		iter.SetRequiredCols([]int{0, 1}) // Only id and name
		iter.SetArena(arena)
		_ = iter.Open(context.Background())

		var chunk execution.Chunk
		for {
			err := iter.NextChunk(context.Background(), &chunk)
			if err == io.EOF || chunk.Len() == 0 {
				break
			}
			for _, t := range chunk.Tuples {
				_ = t.Get("id")
				_ = t.Get("name")
			}
		}
		_ = iter.Close()
	}
}
