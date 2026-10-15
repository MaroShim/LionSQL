package execution

import (
	"context"
	"io"
	"testing"

	"sqlion/pkg/planner"
	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

func TestArena_ExecutionIntegration(t *testing.T) {
	ctx := context.Background()
	arena := storage.GetArena()
	defer storage.PutArena(arena)

	schema := &storage.TableSchema{
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt},
			{Name: "dept", Type: storage.TypeVarChar},
			{Name: "salary", Type: storage.TypeInt},
		},
	}
	colMap := NewColumnIndexMap(schema, "emp")

	// 1. FormatRowWithArena
	row := schema.RecordToRowWithArena(storage.Record{
		"id":     storage.IntValue(1),
		"dept":   storage.StringValue("engineering"),
		"salary": storage.IntValue(100000),
	}, arena)

	tuple := FormatRowWithArena(row, schema, "emp", colMap, arena)
	if tuple.Get("emp.id") != int64(1) || tuple.Get("emp.dept") != "engineering" {
		t.Fatalf("unexpected tuple from FormatRowWithArena: %+v", tuple.Values)
	}

	// 2. HashJoinIterator with Arena
	leftTuples := []Tuple{
		FormatRowWithArena(schema.RecordToRowWithArena(storage.Record{
			"id":     storage.IntValue(1),
			"dept":   storage.StringValue("eng"),
			"salary": storage.IntValue(100),
		}, arena), schema, "emp", colMap, arena),
	}
	deptSchema := &storage.TableSchema{
		Columns: []storage.Column{
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "floor", Type: storage.TypeInt},
		},
	}
	deptColMap := NewColumnIndexMap(deptSchema, "dept")
	rightTuples := []Tuple{
		FormatRowWithArena(deptSchema.RecordToRowWithArena(storage.Record{
			"name":  storage.StringValue("eng"),
			"floor": storage.IntValue(5),
		}, arena), deptSchema, "dept", deptColMap, arena),
	}

	leftIter := &mockSliceIterator{tuples: leftTuples, schema: []string{"emp.id", "emp.dept", "emp.salary"}, colMap: colMap}
	rightIter := &mockSliceIterator{tuples: rightTuples, schema: []string{"dept.name", "dept.floor"}, colMap: deptColMap}

	hj := NewHashJoinIterator(leftIter, rightIter, "INNER", "emp.dept", "dept.name")
	hj.SetArena(arena)

	if err := hj.Open(ctx); err != nil {
		t.Fatalf("failed to open HashJoinIterator: %v", err)
	}
	defer hj.Close()

	joined, err := hj.Next(ctx)
	if err != nil {
		t.Fatalf("failed to pull next joined tuple: %v", err)
	}
	if joined.Get("emp.id") != int64(1) || joined.Get("dept.floor") != int64(5) {
		t.Fatalf("joined tuple mismatch: %+v", joined.Values)
	}

	// 3. AggregateIterator with Arena
	aggIter := NewAggregateIteratorWithModifier(
		&mockSliceIterator{tuples: leftTuples, schema: []string{"emp.id", "emp.dept", "emp.salary"}, colMap: colMap},
		[]string{"emp.dept"},
		proc.GroupByNormal,
		[]string{"emp.dept", "SUM(emp.salary) AS total"},
		"",
	)
	aggIter.SetArena(arena)

	if err := aggIter.Open(ctx); err != nil {
		t.Fatalf("failed to open AggregateIterator: %v", err)
	}
	defer aggIter.Close()

	aggTuple, err := aggIter.Next(ctx)
	if err != nil {
		t.Fatalf("failed to get aggregate tuple: %v", err)
	}
	if aggTuple.Get("emp.dept") != "eng" || (aggTuple.Get("total") != int64(100) && aggTuple.Get("total") != 100.0) {
		t.Fatalf("aggregate mismatch: %+v", aggTuple.Values)
	}

	if arena.TotalAllocated() == 0 {
		t.Fatalf("expected arena to have tracked allocations")
	}
}

type mockSliceIterator struct {
	tuples []Tuple
	schema []string
	colMap *ColumnIndexMap
	idx    int
}

func (m *mockSliceIterator) Open(ctx context.Context) error {
	m.idx = 0
	return nil
}

func (m *mockSliceIterator) Next(ctx context.Context) (Tuple, error) {
	if m.idx >= len(m.tuples) {
		return Tuple{}, io.EOF
	}
	t := m.tuples[m.idx]
	m.idx++
	return t, nil
}

func (m *mockSliceIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	dest.Reset()
	for dest.Len() < DefaultChunkSize && m.idx < len(m.tuples) {
		dest.Tuples = append(dest.Tuples, m.tuples[m.idx])
		m.idx++
	}
	if dest.Len() == 0 {
		return io.EOF
	}
	return nil
}

func (m *mockSliceIterator) Close() error {
	m.idx = 0
	return nil
}

func (m *mockSliceIterator) Schema() []string {
	return m.schema
}

func (m *mockSliceIterator) ColIndexMap() *ColumnIndexMap {
	return m.colMap
}

func TestArena_BuilderIntegration(t *testing.T) {
	opts := DefaultExecutorOptions()
	arena := storage.GetArena()
	defer storage.PutArena(arena)
	opts.Arena = arena

	// Verify options has Arena
	if opts.Arena == nil {
		t.Fatalf("expected opts.Arena to be non-nil")
	}

	schema := &storage.TableSchema{
		Name: "test_tbl",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt},
		},
	}
	plan := &planner.PlanNode{
		Type:     planner.NodeTableScan,
		Relation: "test_tbl",
	}

	iter, err := BuildExecutorWithOptions(plan, nil, func(name string) (*storage.TableSchema, error) {
		return schema, nil
	}, opts)
	if err != nil {
		t.Fatalf("failed to build executor: %v", err)
	}
	scan, ok := iter.(*SeqScanIterator)
	if !ok {
		t.Fatalf("expected *SeqScanIterator, got %T", iter)
	}
	if scan.arena != arena {
		t.Fatalf("expected scan.arena to match opts.Arena")
	}
}
