package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/execution"
	"lionsql/pkg/storage/memengine"
)

type trackingIterator struct {
	openCalled  bool
	closeCalled bool
	failOpen    bool
	failNext    bool
}

func (m *trackingIterator) Open(ctx context.Context) error {
	m.openCalled = true
	if m.failOpen {
		return fmt.Errorf("mock open failed")
	}
	return nil
}

func (m *trackingIterator) Next(ctx context.Context) (execution.Tuple, error) {
	if m.failNext {
		return execution.Tuple{}, fmt.Errorf("mock next failed")
	}
	return execution.Tuple{}, nil
}

func (m *trackingIterator) Close() error {
	m.closeCalled = true
	return nil
}

func (m *trackingIterator) Schema() []string {
	return []string{"col"}
}

func (m *trackingIterator) ColIndexMap() *execution.ColumnIndexMap {
	return nil
}

func TestAggregate_OpenError_ClosesChildIterator(t *testing.T) {
	child := &trackingIterator{}
	// passing having condition with unaggregated column triggers validation error in Open()
	agg := execution.NewAggregateIterator(child, []string{"dept"}, []string{"dept", "COUNT(*)"}, "unaggregated_col > 10")

	err := agg.Open(context.Background())
	if err == nil {
		t.Fatalf("expected Open to fail on having validation, but succeeded")
	}

	if !child.openCalled {
		t.Fatalf("expected child.Open to be called")
	}
	if !child.closeCalled {
		t.Fatalf("[LEAK DEFECT] child.Close() was not called when aggregate.Open() failed!")
	}
}

func TestEngine_OpenError_VolcanoCleanTeardown(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE _t_cursor_leak (id INT PRIMARY KEY, val INT);"); err != nil {
		t.Fatalf("table setup failed: %v", err)
	}

	// Execute a query where Volcano Open() fails during execution (e.g. invalid having column)
	_, err := coord.ExecuteSQL("SELECT val, COUNT(*) FROM _t_cursor_leak GROUP BY val HAVING unaggregated_invalid_col > 10;")
	if err == nil {
		t.Fatalf("expected query to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "must appear in the GROUP BY clause") {
		t.Errorf("expected group by semantic error, got %v", err)
	}

	// Verify table can be dropped immediately without handle/cursor locks
	if _, err := coord.ExecuteSQL("DROP TABLE _t_cursor_leak;"); err != nil {
		t.Fatalf("failed to drop table after query error (cursor leak suspected): %v", err)
	}
}

func TestSetOpAndMergeJoin_OpenError_ClosesChildren(t *testing.T) {
	// Bug 34 (E-1): SetOpIterator.Open right child Next() failure closes both children
	left := &trackingIterator{}
	right := &trackingIterator{failNext: true}
	setOp := execution.NewSetOpIterator(left, right, execution.SetOpIntersect)
	if err := setOp.Open(context.Background()); err == nil {
		t.Fatalf("expected SetOpIterator.Open to fail on right child Next error")
	}
	if !left.closeCalled || !right.closeCalled {
		t.Fatalf("[LEAK DEFECT] SetOpIterator.Open failed to close children: left.closeCalled=%v, right.closeCalled=%v", left.closeCalled, right.closeCalled)
	}

	// Bug 35 (E-2): MergeJoinIterator.Open initial pull failure closes both children
	mjLeft := &trackingIterator{}
	mjRight := &trackingIterator{failNext: true}
	mj := execution.NewMergeJoinIterator(mjLeft, mjRight, "col", "col", "INNER")
	if err := mj.Open(context.Background()); err == nil {
		t.Fatalf("expected MergeJoinIterator.Open to fail on initial Next error")
	}
	if !mjLeft.closeCalled || !mjRight.closeCalled {
		t.Fatalf("[LEAK DEFECT] MergeJoinIterator.Open failed to close children: left.closeCalled=%v, right.closeCalled=%v", mjLeft.closeCalled, mjRight.closeCalled)
	}
}
