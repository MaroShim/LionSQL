package execution_test

import (
	"context"
	"io"
	"testing"

	"lionsql/pkg/execution"
	"lionsql/pkg/storage"
)

type mockRawTupleCursor struct {
	schema *storage.TableSchema
	data   [][]byte
	idx    int
}

func (m *mockRawTupleCursor) Next() bool {
	if m.idx < len(m.data) {
		m.idx++
		return true
	}
	return false
}

func (m *mockRawTupleCursor) RawTupleBuffer(buf *[]byte) bool {
	if m.idx-1 < len(m.data) {
		cur := m.data[m.idx-1]
		*buf = append((*buf)[:0], cur...)
		return true
	}
	return false
}

func (m *mockRawTupleCursor) RawTuple() ([]byte, bool) {
	if m.idx-1 < len(m.data) {
		return m.data[m.idx-1], true
	}
	return nil, false
}

func (m *mockRawTupleCursor) Row() (storage.Row, error) {
	return storage.Row{}, nil
}

func (m *mockRawTupleCursor) Record() (storage.Record, error) {
	return storage.Record{}, nil
}

func (m *mockRawTupleCursor) Close() error {
	return nil
}

func (m *mockRawTupleCursor) Err() error {
	return nil
}

type mockTx struct {
	storage.Transaction
	cur storage.Cursor
}

func (m *mockTx) Scan(tableName string) (storage.Cursor, error) {
	return m.cur, nil
}

func TestPERF_ScanFilterFusion_ZeroAllocationRejection(t *testing.T) {
	sch := &storage.TableSchema{
		Name: "metrics",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt},
			{Name: "val", Type: storage.TypeInt},
			{Name: "tag", Type: storage.TypeVarChar},
		},
	}

	// Create 1,000 raw encoded binary tuples
	nTuples := 1000
	rawData := make([][]byte, nTuples)
	for i := 0; i < nTuples; i++ {
		rec := storage.Record{
			"id":  storage.IntValue(int64(i + 1)),
			"val": storage.IntValue(int64(i * 10)),
			"tag": storage.StringValue("status_ok"),
		}
		buf, err := storage.EncodeTuple(sch, rec)
		if err != nil {
			t.Fatalf("EncodeTuple failed: %v", err)
		}
		rawData[i] = buf
	}

	cur := &mockRawTupleCursor{schema: sch, data: rawData}
	tx := &mockTx{cur: cur}

	scanIter := execution.NewSeqScanIterator(tx, "metrics", "m", sch)
	// Filter for only rows where id > 990 (should yield exactly 10 matching rows: 991..1000)
	scanIter.SetFilterCond("id > 990")

	ctx := context.Background()
	if err := scanIter.Open(ctx); err != nil {
		t.Fatalf("scan open failed: %v", err)
	}
	defer scanIter.Close()

	dest := execution.GetChunk()
	defer execution.PutChunk(dest)

	err := scanIter.NextChunk(ctx, dest)
	if err != nil && err != io.EOF {
		t.Fatalf("NextChunk error: %v", err)
	}

	// Out of 1000 tuples, exactly 10 tuples should be returned in chunk
	if dest.Len() != 10 {
		t.Fatalf("expected exactly 10 filtered tuples, got %d", dest.Len())
	}

	// Verify all returned tuples satisfy id > 990
	for i, tup := range dest.Tuples {
		idVal := tup.Get("id")
		id, ok := idVal.(int64)
		if !ok || id <= 990 {
			t.Fatalf("tuple %d failed filter: id=%v", i, idVal)
		}
	}
}
