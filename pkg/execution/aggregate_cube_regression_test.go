package execution

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"lionsql/pkg/proc"
)

// TestAggregate_Regression_P0_11_CubeMaxColumnsGuard verifies that specifying
// more than MaxCubeColumns (12) columns in GROUP BY CUBE returns ErrTooManyCubeColumns
// instead of attempting to allocate 2^N elements and crashing with OOM.
func TestAggregate_Regression_P0_11_CubeMaxColumnsGuard(t *testing.T) {
	ctx := context.Background()

	// Case 1: 13 columns (exceeds 12-column limit)
	cols13 := make([]string, 13)
	for i := 0; i < 13; i++ {
		cols13[i] = fmt.Sprintf("c%d", i)
	}
	emptyChild13 := NewEmptyIterator(cols13, nil)
	agg13 := NewAggregateIteratorWithModifier(
		emptyChild13,
		cols13,
		proc.GroupByCube,
		[]string{"COUNT(*) AS cnt"},
		"",
	)

	err := agg13.Open(ctx)
	if err == nil {
		t.Fatalf("[P0-11 DEFECT] Expected ErrTooManyCubeColumns for 13 columns, but Open() succeeded!")
	}
	if !errors.Is(err, ErrTooManyCubeColumns) {
		t.Fatalf("[P0-11 DEFECT] Expected error wrapping ErrTooManyCubeColumns, got: %v", err)
	}
	t.Logf("[P0-11 PASS] 13 columns rejected safely: %v", err)

	// Case 2: 30 columns (would cause 1GB+ allocation and OOM DoS crash)
	cols30 := make([]string, 30)
	for i := 0; i < 30; i++ {
		cols30[i] = fmt.Sprintf("c%d", i)
	}
	emptyChild30 := NewEmptyIterator(cols30, nil)
	agg30 := NewAggregateIteratorWithModifier(
		emptyChild30,
		cols30,
		proc.GroupByCube,
		[]string{"COUNT(*) AS cnt"},
		"",
	)

	err30 := agg30.Open(ctx)
	if err30 == nil {
		t.Fatalf("[P0-11 DEFECT] Expected ErrTooManyCubeColumns for 30 columns, but Open() succeeded!")
	}
	if !errors.Is(err30, ErrTooManyCubeColumns) {
		t.Fatalf("[P0-11 DEFECT] Expected ErrTooManyCubeColumns for 30 columns, got: %v", err30)
	}
	t.Logf("[P0-11 PASS] 30 columns rejected safely without OOM crash: %v", err30)

	// Case 3: 12 columns (exactly at maximum allowed limit)
	cols12 := make([]string, 12)
	for i := 0; i < 12; i++ {
		cols12[i] = fmt.Sprintf("c%d", i)
	}
	emptyChild12 := NewEmptyIterator(cols12, nil)
	agg12 := NewAggregateIteratorWithModifier(
		emptyChild12,
		cols12,
		proc.GroupByCube,
		[]string{"COUNT(*) AS cnt"},
		"",
	)
	if err := agg12.Open(ctx); err != nil {
		t.Fatalf("unexpected error for 12 columns: %v", err)
	}
	defer agg12.Close()
	t.Logf("[P0-11 PASS] 12 columns (4,096 groups) succeeded within budget.")
}
