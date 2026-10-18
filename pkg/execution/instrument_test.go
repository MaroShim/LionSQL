package execution

import (
	"context"
	"io"
	"testing"
	"time"

	"sqlion/pkg/planner"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

type mockIterator struct {
	tuples []Tuple
	idx    int
	schema []string
	colMap *ColumnIndexMap
}

func newMockIterator(cols []string, rows [][]any) *mockIterator {
	colMap := NewColumnIndexMapFromCols(cols)
	tuples := make([]Tuple, len(rows))
	for i, r := range rows {
		tuples[i] = Tuple{
			Values: r,
			Index:  colMap,
		}
	}
	return &mockIterator{
		tuples: tuples,
		schema: cols,
		colMap: colMap,
	}
}

func (m *mockIterator) Open(ctx context.Context) error {
	m.idx = 0
	return nil
}

func (m *mockIterator) Next(ctx context.Context) (Tuple, error) {
	if m.idx >= len(m.tuples) {
		return Tuple{}, io.EOF
	}
	// Simulate minor processing delay
	time.Sleep(100 * time.Microsecond)
	t := m.tuples[m.idx]
	m.idx++
	return t, nil
}

func (m *mockIterator) Close() error {
	return nil
}

func (m *mockIterator) Schema() []string {
	return m.schema
}

func (m *mockIterator) ColIndexMap() *ColumnIndexMap {
	return m.colMap
}

func TestInstrumentedIterator_Direct(t *testing.T) {
	node := &planner.PlanNode{
		Type: planner.NodeTableScan,
	}

	cols := []string{"id", "name"}
	rows := [][]any{
		{1, "Alice"},
		{2, "Bob"},
		{3, "Charlie"},
	}

	mock := newMockIterator(cols, rows)
	instr := NewInstrumentedIterator(mock, node)

	ctx := context.Background()
	if err := instr.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if node.ActualLoops != 1 {
		t.Errorf("expected ActualLoops 1, got %d", node.ActualLoops)
	}

	count := 0
	for {
		tuple, err := instr.Next(ctx)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("unexpected Next error: %v", err)
		}
		if tuple.IsNil() {
			break
		}
		count++
	}

	if count != 3 {
		t.Fatalf("expected 3 tuples, got %d", count)
	}
	if node.ActualRows != 3 {
		t.Errorf("expected ActualRows 3, got %d", node.ActualRows)
	}
	if node.ActualStartupUS <= 0 {
		t.Errorf("expected ActualStartupUS > 0, got %d", node.ActualStartupUS)
	}
	if node.ActualDurationUS <= 0 {
		t.Errorf("expected ActualDurationUS > 0, got %d", node.ActualDurationUS)
	}

	if err := instr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestInstrumentedIterator_Chunked(t *testing.T) {
	node := &planner.PlanNode{
		Type: planner.NodeTableScan,
	}

	cols := []string{"id"}
	rows := make([][]any, 10)
	for i := 0; i < 10; i++ {
		rows[i] = []any{i + 1}
	}

	mock := newMockIterator(cols, rows)
	instr := NewInstrumentedIterator(mock, node)

	ctx := context.Background()
	if err := instr.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	chunk := GetChunk()
	defer PutChunk(chunk)

	err := instr.NextChunk(ctx, chunk)
	if err != nil {
		t.Fatalf("NextChunk failed: %v", err)
	}
	if chunk.Len() != 10 {
		t.Fatalf("expected chunk len 10, got %d", chunk.Len())
	}
	if node.ActualRows != 10 {
		t.Errorf("expected ActualRows 10, got %d", node.ActualRows)
	}
	if node.ActualStartupUS <= 0 {
		t.Errorf("expected ActualStartupUS > 0, got %d", node.ActualStartupUS)
	}
}

func TestBuildInstrumentedExecutor_Pipeline(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "score", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: false})
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}

	// Insert 10 rows: scores 10, 20, ..., 100
	for i := 1; i <= 10; i++ {
		rec := storage.Record{
			"id":    storage.IntValue(int64(i)),
			"score": storage.IntValue(int64(i * 10)),
		}
		if err := tx.Insert("users", rec); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	readTx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read tx failed: %v", err)
	}
	defer readTx.Rollback()

	// Build plan tree: Limit(2) -> Sort(score DESC) -> Filter(score > 30) -> TableScan(users)
	scanNode := &planner.PlanNode{
		Type:     planner.NodeTableScan,
		Relation: "users",
	}
	filterNode := &planner.PlanNode{
		Type:       planner.NodeFilter,
		FilterCond: "score > 30",
		Children:   []*planner.PlanNode{scanNode},
	}
	sortNode := &planner.PlanNode{
		Type:      planner.NodeSort,
		SortKey:   "score",
		SortOrder: "DESC",
		Children:  []*planner.PlanNode{filterNode},
	}
	limitNode := &planner.PlanNode{
		Type:       planner.NodeLimit,
		LimitCount: 2,
		Children:   []*planner.PlanNode{sortNode},
	}

	getTable := eng.GetTable
	opts := DefaultExecutorOptions()

	iter, err := BuildInstrumentedExecutor(limitNode, readTx, getTable, opts)
	if err != nil {
		t.Fatalf("BuildInstrumentedExecutor failed: %v", err)
	}

	ctx := context.Background()
	if err := iter.Open(ctx); err != nil {
		t.Fatalf("iter.Open failed: %v", err)
	}
	defer iter.Close()

	var pulled []Tuple
	for {
		tup, err := iter.Next(ctx)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("Next failed: %v", err)
		}
		pulled = append(pulled, tup)
	}

	if len(pulled) != 2 {
		t.Fatalf("expected 2 tuples from limit, got %d", len(pulled))
	}

	// Verify each node in the plan was individually measured
	if limitNode.ActualRows != 2 {
		t.Errorf("expected limitNode.ActualRows 2, got %d", limitNode.ActualRows)
	}
	if limitNode.ActualLoops != 1 {
		t.Errorf("expected limitNode.ActualLoops 1, got %d", limitNode.ActualLoops)
	}

	// Filter condition score > 30 should yield 7 rows (40, 50, ..., 100)
	if filterNode.ActualRows != 7 {
		t.Errorf("expected filterNode.ActualRows 7, got %d", filterNode.ActualRows)
	}

	// Scan node should scan all 10 rows
	if scanNode.ActualRows != 10 {
		t.Errorf("expected scanNode.ActualRows 10, got %d", scanNode.ActualRows)
	}

	// Check durations
	if limitNode.ActualDurationUS <= 0 || scanNode.ActualDurationUS <= 0 {
		t.Errorf("expected positive actual durations: limit=%d, scan=%d", limitNode.ActualDurationUS, scanNode.ActualDurationUS)
	}
}
