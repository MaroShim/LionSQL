package execution_test

import (
	"context"
	"fmt"
	"io"
	"testing"

	"lionsql/pkg/execution"
	"lionsql/pkg/storage"
)

// mockSortedIterator provides in-memory mock tuples with a schema and ColIndexMap.
type mockSortedIterator struct {
	tuples      []execution.Tuple
	idx         int
	schema      []string
	colIndexMap *execution.ColumnIndexMap
}

func newMockSortedIterator(tuples []execution.Tuple, tableName string, cols []string) *mockSortedIterator {
	var storageCols []storage.Column
	for _, c := range cols {
		storageCols = append(storageCols, storage.Column{Name: c, Type: storage.TypeVarChar})
	}
	ts := &storage.TableSchema{Name: tableName, Columns: storageCols}
	cmap := execution.NewColumnIndexMap(ts, tableName)

	// Set index on all tuples
	for i := range tuples {
		tuples[i].Index = cmap
	}

	return &mockSortedIterator{
		tuples:      tuples,
		schema:      cols,
		colIndexMap: cmap,
	}
}

func (m *mockSortedIterator) Open(ctx context.Context) error {
	m.idx = 0
	return nil
}

func (m *mockSortedIterator) Next(ctx context.Context) (execution.Tuple, error) {
	if m.idx >= len(m.tuples) {
		return execution.Tuple{}, io.EOF
	}
	t := m.tuples[m.idx]
	m.idx++
	return t, nil
}

func (m *mockSortedIterator) Close() error {
	return nil
}

func (m *mockSortedIterator) Schema() []string {
	return m.schema
}

func (m *mockSortedIterator) ColIndexMap() *execution.ColumnIndexMap {
	return m.colIndexMap
}

func TestMergeJoin_InnerJoin_Basic(t *testing.T) {
	ctx := context.Background()

	// Left: (1, 'Alice'), (2, 'Bob'), (4, 'Dave')
	leftTuples := []execution.Tuple{
		{Values: []any{int64(1), "Alice"}},
		{Values: []any{int64(2), "Bob"}},
		{Values: []any{int64(4), "Dave"}},
	}
	// Right: (2, 'Sales'), (3, 'HR'), (4, 'Engineering')
	rightTuples := []execution.Tuple{
		{Values: []any{int64(2), "Sales"}},
		{Values: []any{int64(3), "HR"}},
		{Values: []any{int64(4), "Engineering"}},
	}

	left := newMockSortedIterator(leftTuples, "users", []string{"id", "name"})
	right := newMockSortedIterator(rightTuples, "dept", []string{"dept_id", "dept_name"})

	mj := execution.NewMergeJoinIterator(left, right, "INNER", "id", "dept_id")
	if err := mj.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer mj.Close()

	var results []string
	for {
		tuple, err := mj.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		results = append(results, fmt.Sprintf("%v-%v", tuple.Values[0], tuple.Values[3]))
	}

	expected := []string{"2-Sales", "4-Engineering"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d results, got %d: %v", len(expected), len(results), results)
	}
	for i, exp := range expected {
		if results[i] != exp {
			t.Errorf("result[%d] = %s, expected %s", i, results[i], exp)
		}
	}
}

func TestMergeJoin_InnerJoin_Duplicates(t *testing.T) {
	ctx := context.Background()

	// Left has duplicate keys: (10, 'A1'), (10, 'A2'), (20, 'B')
	leftTuples := []execution.Tuple{
		{Values: []any{int64(10), "A1"}},
		{Values: []any{int64(10), "A2"}},
		{Values: []any{int64(20), "B"}},
	}
	// Right has duplicate keys: (10, 'X1'), (10, 'X2'), (10, 'X3'), (30, 'Z')
	rightTuples := []execution.Tuple{
		{Values: []any{int64(10), "X1"}},
		{Values: []any{int64(10), "X2"}},
		{Values: []any{int64(10), "X3"}},
		{Values: []any{int64(30), "Z"}},
	}

	left := newMockSortedIterator(leftTuples, "t1", []string{"k", "v1"})
	right := newMockSortedIterator(rightTuples, "t2", []string{"k", "v2"})

	mj := execution.NewMergeJoinIterator(left, right, "INNER", "k", "k")
	if err := mj.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer mj.Close()

	var matches []string
	for {
		tuple, err := mj.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		matches = append(matches, fmt.Sprintf("%v:%v", tuple.Values[1], tuple.Values[3]))
	}

	// 2 left rows * 3 right rows = 6 output rows for key 10
	expected := []string{
		"A1:X1", "A1:X2", "A1:X3",
		"A2:X1", "A2:X2", "A2:X3",
	}
	if len(matches) != len(expected) {
		t.Fatalf("expected %d matches, got %d: %v", len(expected), len(matches), matches)
	}
	for i, exp := range expected {
		if matches[i] != exp {
			t.Errorf("match[%d] = %s, expected %s", i, matches[i], exp)
		}
	}
}

func TestMergeJoin_LeftOuterJoin(t *testing.T) {
	ctx := context.Background()

	// Left: (1, 'Alice'), (2, 'Bob'), (3, 'Charlie')
	leftTuples := []execution.Tuple{
		{Values: []any{int64(1), "Alice"}},
		{Values: []any{int64(2), "Bob"}},
		{Values: []any{int64(3), "Charlie"}},
	}
	// Right: (2, 'Sales'), (4, 'Legal')
	rightTuples := []execution.Tuple{
		{Values: []any{int64(2), "Sales"}},
		{Values: []any{int64(4), "Legal"}},
	}

	left := newMockSortedIterator(leftTuples, "users", []string{"id", "name"})
	right := newMockSortedIterator(rightTuples, "dept", []string{"dept_id", "dept_name"})

	mj := execution.NewMergeJoinIterator(left, right, "LEFT", "id", "dept_id")
	if err := mj.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer mj.Close()

	var results []string
	for {
		tuple, err := mj.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		results = append(results, fmt.Sprintf("%v-%v", tuple.Values[0], tuple.Values[3]))
	}

	expected := []string{"1-<nil>", "2-Sales", "3-<nil>"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d results, got %d: %v", len(expected), len(results), results)
	}
	for i, exp := range expected {
		if results[i] != exp {
			t.Errorf("result[%d] = %s, expected %s", i, results[i], exp)
		}
	}
}

func TestMergeJoin_RightOuterJoin(t *testing.T) {
	ctx := context.Background()

	// Left: (2, 'Bob')
	leftTuples := []execution.Tuple{
		{Values: []any{int64(2), "Bob"}},
	}
	// Right: (1, 'Engineering'), (2, 'Sales'), (3, 'Marketing')
	rightTuples := []execution.Tuple{
		{Values: []any{int64(1), "Engineering"}},
		{Values: []any{int64(2), "Sales"}},
		{Values: []any{int64(3), "Marketing"}},
	}

	left := newMockSortedIterator(leftTuples, "users", []string{"id", "name"})
	right := newMockSortedIterator(rightTuples, "dept", []string{"dept_id", "dept_name"})

	mj := execution.NewMergeJoinIterator(left, right, "RIGHT", "id", "dept_id")
	if err := mj.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer mj.Close()

	var results []string
	for {
		tuple, err := mj.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		results = append(results, fmt.Sprintf("%v-%v", tuple.Values[1], tuple.Values[3]))
	}

	expected := []string{"<nil>-Engineering", "Bob-Sales", "<nil>-Marketing"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d results, got %d: %v", len(expected), len(results), results)
	}
	for i, exp := range expected {
		if results[i] != exp {
			t.Errorf("result[%d] = %s, expected %s", i, results[i], exp)
		}
	}
}

func TestMergeJoin_FullOuterJoin(t *testing.T) {
	ctx := context.Background()

	leftTuples := []execution.Tuple{
		{Values: []any{int64(1), "Alice"}},
		{Values: []any{int64(3), "Charlie"}},
	}
	rightTuples := []execution.Tuple{
		{Values: []any{int64(2), "Bob"}},
		{Values: []any{int64(3), "Charlie-Dept"}},
		{Values: []any{int64(4), "Dave"}},
	}

	left := newMockSortedIterator(leftTuples, "users", []string{"id", "name"})
	right := newMockSortedIterator(rightTuples, "dept", []string{"dept_id", "dept_name"})

	mj := execution.NewMergeJoinIterator(left, right, "FULL", "id", "dept_id")
	if err := mj.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer mj.Close()

	var results []string
	for {
		tuple, err := mj.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		results = append(results, fmt.Sprintf("%v-%v", tuple.Values[1], tuple.Values[3]))
	}

	expected := []string{"Alice-<nil>", "<nil>-Bob", "Charlie-Charlie-Dept", "<nil>-Dave"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d results, got %d: %v", len(expected), len(results), results)
	}
	for i, exp := range expected {
		if results[i] != exp {
			t.Errorf("result[%d] = %s, expected %s", i, results[i], exp)
		}
	}
}

func TestMergeJoin_SemiAndAntiJoin(t *testing.T) {
	ctx := context.Background()

	leftTuples := []execution.Tuple{
		{Values: []any{int64(1), "Alice"}},
		{Values: []any{int64(2), "Bob"}},
		{Values: []any{int64(3), "Charlie"}},
	}
	rightTuples := []execution.Tuple{
		{Values: []any{int64(2), "DeptA"}},
		{Values: []any{int64(2), "DeptB"}},
		{Values: []any{int64(4), "DeptC"}},
	}

	// SEMI Join
	leftSemi := newMockSortedIterator(leftTuples, "users", []string{"id", "name"})
	rightSemi := newMockSortedIterator(rightTuples, "dept", []string{"dept_id", "dept_name"})
	mjSemi := execution.NewMergeJoinIterator(leftSemi, rightSemi, "SEMI", "id", "dept_id")
	if err := mjSemi.Open(ctx); err != nil {
		t.Fatalf("SEMI Open failed: %v", err)
	}
	defer mjSemi.Close()

	var semiResults []string
	for {
		tuple, err := mjSemi.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("SEMI Next failed: %v", err)
		}
		semiResults = append(semiResults, fmt.Sprintf("%v", tuple.Values[1]))
	}
	if len(semiResults) != 1 || semiResults[0] != "Bob" {
		t.Fatalf("expected [Bob] for SEMI join, got %v", semiResults)
	}

	// ANTI Join
	leftAnti := newMockSortedIterator(leftTuples, "users", []string{"id", "name"})
	rightAnti := newMockSortedIterator(rightTuples, "dept", []string{"dept_id", "dept_name"})
	mjAnti := execution.NewMergeJoinIterator(leftAnti, rightAnti, "ANTI", "id", "dept_id")
	if err := mjAnti.Open(ctx); err != nil {
		t.Fatalf("ANTI Open failed: %v", err)
	}
	defer mjAnti.Close()

	var antiResults []string
	for {
		tuple, err := mjAnti.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("ANTI Next failed: %v", err)
		}
		antiResults = append(antiResults, fmt.Sprintf("%v", tuple.Values[1]))
	}
	expectedAnti := []string{"Alice", "Charlie"}
	if len(antiResults) != len(expectedAnti) {
		t.Fatalf("expected %v for ANTI join, got %v", expectedAnti, antiResults)
	}
	for i, exp := range expectedAnti {
		if antiResults[i] != exp {
			t.Errorf("anti[%d] = %s, expected %s", i, antiResults[i], exp)
		}
	}
}

func TestMergeJoin_NullKeyHandling(t *testing.T) {
	ctx := context.Background()

	// NULL keys in left or right relation must not match in INNER join
	leftTuples := []execution.Tuple{
		{Values: []any{nil, "NullUser"}},
		{Values: []any{int64(10), "ValidUser"}},
	}
	rightTuples := []execution.Tuple{
		{Values: []any{nil, "NullDept"}},
		{Values: []any{int64(10), "ValidDept"}},
	}

	left := newMockSortedIterator(leftTuples, "users", []string{"id", "name"})
	right := newMockSortedIterator(rightTuples, "dept", []string{"dept_id", "dept_name"})

	mj := execution.NewMergeJoinIterator(left, right, "INNER", "id", "dept_id")
	if err := mj.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer mj.Close()

	var results []string
	for {
		tuple, err := mj.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		results = append(results, fmt.Sprintf("%v-%v", tuple.Values[1], tuple.Values[3]))
	}

	if len(results) != 1 || results[0] != "ValidUser-ValidDept" {
		t.Fatalf("expected only ValidUser-ValidDept, got %v", results)
	}
}

func TestMergeJoin_EmptyInputs(t *testing.T) {
	ctx := context.Background()

	left := newMockSortedIterator(nil, "users", []string{"id", "name"})
	right := newMockSortedIterator(nil, "dept", []string{"dept_id", "dept_name"})

	mj := execution.NewMergeJoinIterator(left, right, "INNER", "id", "dept_id")
	if err := mj.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer mj.Close()

	tuple, err := mj.Next(ctx)
	if err != io.EOF {
		t.Fatalf("expected io.EOF on empty inputs, got %v (tuple=%v)", err, tuple)
	}
}
