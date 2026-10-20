package execution_test

import (
	"context"
	"io"
	"testing"

	"lionsql/pkg/execution"
	"lionsql/pkg/planner"
)

// TestGroup6_HashJoinStringCaseSensitivity tests BUG-EXEC-JOIN-STR-CASE-1 for HashJoin
func TestGroup6_HashJoinStringCaseSensitivity(t *testing.T) {
	left := newMockIterator(
		[]string{"username", "score"},
		map[string]any{"username": "ADMIN", "score": int64(99)},
		map[string]any{"username": "Guest", "score": int64(10)},
	)
	right := newMockIterator(
		[]string{"user_code", "role"},
		map[string]any{"user_code": "admin", "role": "superuser"},
		map[string]any{"user_code": "guest", "role": "anonymous"},
	)

	join := execution.NewHashJoinIterator(left, right, "INNER", "username", "user_code")
	ctx := context.Background()
	if err := join.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer join.Close()

	var matches []map[string]any
	for {
		tuple, err := join.Next(ctx)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("Next failed: %v", err)
		}
		matches = append(matches, tuple.ToMap())
	}

	if len(matches) != 2 {
		t.Fatalf("expected 2 case-insensitive join matches, got %d: %v", len(matches), matches)
	}
}

// TestGroup6_MergeJoinStringCaseSensitivity tests BUG-EXEC-JOIN-STR-CASE-1 for MergeJoin
func TestGroup6_MergeJoinStringCaseSensitivity(t *testing.T) {
	left := newMockIterator(
		[]string{"username", "score"},
		map[string]any{"username": "ADMIN", "score": int64(99)},
		map[string]any{"username": "GUEST", "score": int64(10)},
	)
	right := newMockIterator(
		[]string{"user_code", "role"},
		map[string]any{"user_code": "admin", "role": "superuser"},
		map[string]any{"user_code": "guest", "role": "anonymous"},
	)

	join := execution.NewMergeJoinIterator(left, right, "INNER", "username", "user_code")
	ctx := context.Background()
	if err := join.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer join.Close()

	var matches []map[string]any
	for {
		tuple, err := join.Next(ctx)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("Next failed: %v", err)
		}
		matches = append(matches, tuple.ToMap())
	}

	if len(matches) != 2 {
		t.Fatalf("expected 2 case-insensitive merge join matches, got %d: %v", len(matches), matches)
	}
}

// TestGroup6_ParseJoinCond_CompositeAndLiteralFilter tests BUG-EXEC-BUILDER-JOIN-COND-SPLIT-1
func TestGroup6_ParseJoinCond_CompositeAndLiteralFilter(t *testing.T) {
	cond := "a.id = b.id AND a.dept = b.dept AND b.status = 1 AND a.type = 'active'"
	leftKey, rightKey := execution.ParseJoinCond(cond)

	expectedLeft := "a.id, a.dept"
	expectedRight := "b.id, b.dept"

	if leftKey != expectedLeft {
		t.Errorf("expected leftKey %q, got %q", expectedLeft, leftKey)
	}
	if rightKey != expectedRight {
		t.Errorf("expected rightKey %q, got %q", expectedRight, rightKey)
	}
}

// TestGroup6_HashJoin_CompositeKeys tests HashJoin with composite keys
func TestGroup6_HashJoin_CompositeKeys(t *testing.T) {
	left := newMockIterator(
		[]string{"id", "dept", "val"},
		map[string]any{"id": int64(1), "dept": "HR", "val": "l1"},
		map[string]any{"id": int64(1), "dept": "IT", "val": "l2"},
	)
	right := newMockIterator(
		[]string{"e_id", "e_dept", "e_val"},
		map[string]any{"e_id": int64(1), "e_dept": "hr", "e_val": "r1"}, // matches HR (case-insensitive)
		map[string]any{"e_id": int64(2), "e_dept": "it", "e_val": "r2"}, // id doesn't match
	)

	join := execution.NewHashJoinIterator(left, right, "INNER", "id, dept", "e_id, e_dept")
	ctx := context.Background()
	if err := join.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer join.Close()

	var matches []map[string]any
	for {
		tuple, err := join.Next(ctx)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("Next failed: %v", err)
		}
		matches = append(matches, tuple.ToMap())
	}

	if len(matches) != 1 {
		t.Fatalf("expected 1 composite key match, got %d: %v", len(matches), matches)
	}
	if matches[0]["val"] != "l1" || matches[0]["e_val"] != "r1" {
		t.Errorf("unexpected match: %v", matches[0])
	}
}

// TestGroup6_WindowRangePeerEvaluation tests BUG-WINDOW-RANGE-PEER-EVAL-1
func TestGroup6_WindowRangePeerEvaluation(t *testing.T) {
	// 4 rows: score 10, 20, 20 (peer), 30
	child := newMockIterator(
		[]string{"name", "score", "amount"},
		map[string]any{"name": "A", "score": int64(10), "amount": float64(100)},
		map[string]any{"name": "B", "score": int64(20), "amount": float64(200)},
		map[string]any{"name": "C", "score": int64(20), "amount": float64(300)},
		map[string]any{"name": "D", "score": int64(30), "amount": float64(400)},
	)

	// Default frame: RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
	winRange := execution.NewWindowIterator(
		child,
		[]planner.WindowDef{
			{
				Function: "SUM",
				Args:     []string{"amount"},
				OrderByKeys: []planner.SortKeyDef{
					{Column: "score", Direction: "ASC"},
				},
				Alias: "running_sum",
			},
		},
	)

	ctx := context.Background()
	if err := winRange.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer winRange.Close()

	var results []float64
	for {
		tuple, err := winRange.Next(ctx)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("Next failed: %v", err)
		}
		v := tuple.Get("running_sum")
		f, ok := v.(float64)
		if !ok {
			t.Fatalf("expected float64 for running_sum, got %T: %v", v, v)
		}
		results = append(results, f)
	}

	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}

	// In RANGE mode:
	// Row 0 (score 10): sum = 100
	// Row 1 (score 20): sum = 100 + 200 + 300 = 600 (peers B and C get the same sum!)
	// Row 2 (score 20): sum = 100 + 200 + 300 = 600
	// Row 3 (score 30): sum = 100 + 200 + 300 + 400 = 1000
	expectedRange := []float64{100, 600, 600, 1000}
	for i, exp := range expectedRange {
		if results[i] != exp {
			t.Errorf("row %d: expected RANGE sum %v, got %v", i, exp, results[i])
		}
	}

	// Now verify ROWS mode:
	childRows := newMockIterator(
		[]string{"name", "score", "amount"},
		map[string]any{"name": "A", "score": int64(10), "amount": float64(100)},
		map[string]any{"name": "B", "score": int64(20), "amount": float64(200)},
		map[string]any{"name": "C", "score": int64(20), "amount": float64(300)},
		map[string]any{"name": "D", "score": int64(30), "amount": float64(400)},
	)

	winRows := execution.NewWindowIterator(
		childRows,
		[]planner.WindowDef{
			{
				Function: "SUM",
				Args:     []string{"amount"},
				OrderByKeys: []planner.SortKeyDef{
					{Column: "score", Direction: "ASC"},
				},
				Frame: &planner.WindowFrame{
					Unit:  planner.FrameRows,
					Start: planner.FrameBound{Type: planner.FrameUnboundedPreceding},
					End:   planner.FrameBound{Type: planner.FrameCurrentRow},
				},
				Alias: "running_sum",
			},
		},
	)

	if err := winRows.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer winRows.Close()

	var rowResults []float64
	for {
		tuple, err := winRows.Next(ctx)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("Next failed: %v", err)
		}
		v := tuple.Get("running_sum")
		f, ok := v.(float64)
		if !ok {
			t.Fatalf("expected float64 for running_sum, got %T: %v", v, v)
		}
		rowResults = append(rowResults, f)
	}

	// In ROWS mode:
	// Row 1 sum is 100 + 200 = 300, Row 2 sum is 100 + 200 + 300 = 600
	expectedRows := []float64{100, 300, 600, 1000}
	for i, exp := range expectedRows {
		if rowResults[i] != exp {
			t.Errorf("row %d: expected ROWS sum %v, got %v", i, exp, rowResults[i])
		}
	}
}
