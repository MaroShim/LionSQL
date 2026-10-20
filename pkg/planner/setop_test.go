package planner_test

import (
	"testing"

	"lionsql/pkg/planner"
)

func TestFindTopLevelSetOps_Basic(t *testing.T) {
	sql := "SELECT id, name FROM users_2025 UNION ALL SELECT id, name FROM users_2026;"
	q, ok := planner.FindTopLevelSetOps(sql)
	if !ok || q == nil {
		t.Fatalf("expected top-level set op, got ok=false")
	}

	if len(q.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(q.Segments))
	}
	if q.Segments[0].Query != "SELECT id, name FROM users_2025" {
		t.Errorf("seg 0 query mismatch: %q", q.Segments[0].Query)
	}
	if q.Segments[1].Op != "UNION ALL" {
		t.Errorf("seg 1 op mismatch: %q", q.Segments[1].Op)
	}
	if q.Segments[1].Query != "SELECT id, name FROM users_2026" {
		t.Errorf("seg 1 query mismatch: %q", q.Segments[1].Query)
	}
	if q.OrderBy != "" || q.Limit != -1 {
		t.Errorf("unexpected orderBy/limit: %v, %v", q.OrderBy, q.Limit)
	}
}

func TestFindTopLevelSetOps_ChainedWithTrailingOrderByAndLimit(t *testing.T) {
	sql := `SELECT id, amount FROM ledger_q1
            UNION
            SELECT id, amount FROM ledger_q2
            EXCEPT
            SELECT id, amount FROM ledger_audit
            ORDER BY amount DESC LIMIT 10;`

	q, ok := planner.FindTopLevelSetOps(sql)
	if !ok || q == nil {
		t.Fatalf("expected top-level set op, got ok=false")
	}

	if len(q.Segments) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(q.Segments))
	}
	if q.Segments[1].Op != "UNION" {
		t.Errorf("seg 1 op mismatch: got %q", q.Segments[1].Op)
	}
	if q.Segments[2].Op != "EXCEPT" {
		t.Errorf("seg 2 op mismatch: got %q", q.Segments[2].Op)
	}
	if q.Segments[2].Query != "SELECT id, amount FROM ledger_audit" {
		t.Errorf("seg 2 query mismatch: got %q", q.Segments[2].Query)
	}
	if q.OrderBy != "amount DESC" {
		t.Errorf("expected OrderBy 'amount DESC', got %q", q.OrderBy)
	}
	if q.Limit != 10 {
		t.Errorf("expected Limit 10, got %d", q.Limit)
	}
}

func TestFindTopLevelSetOps_SubqueryInWhereIgnored(t *testing.T) {
	// UNION is inside WHERE (...), not top-level
	sql := "SELECT * FROM orders WHERE user_id IN (SELECT id FROM active_users UNION SELECT id FROM trial_users);"
	q, ok := planner.FindTopLevelSetOps(sql)
	if ok {
		t.Fatalf("expected no top-level set op, got %v", q)
	}
}

func TestFindTopLevelSetOps_ParenthesizedQueries(t *testing.T) {
	sql := "(SELECT 1 AS val) INTERSECT (SELECT 2 AS val)"
	q, ok := planner.FindTopLevelSetOps(sql)
	if !ok || q == nil {
		t.Fatalf("expected top-level set op, got ok=false")
	}
	if len(q.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(q.Segments))
	}
	if q.Segments[0].Query != "SELECT 1 AS val" {
		t.Errorf("seg 0 query mismatch: %q", q.Segments[0].Query)
	}
	if q.Segments[1].Op != "INTERSECT" {
		t.Errorf("seg 1 op mismatch: %q", q.Segments[1].Op)
	}
	if q.Segments[1].Query != "SELECT 2 AS val" {
		t.Errorf("seg 1 query mismatch: %q", q.Segments[1].Query)
	}
}

func TestPlanner_BuildSetOpPlan(t *testing.T) {
	p := planner.NewPlanner()
	setQuery := &planner.SetOpQuery{
		Segments: []planner.SetOpSegment{
			{Op: "", Query: "SELECT id FROM a"},
			{Op: "UNION", Query: "SELECT id FROM b"},
		},
		OrderBy: "id DESC",
		Limit:   5,
	}

	plan, err := p.BuildSetOpPlan(setQuery, func(subSQL string) (*planner.PlanNode, error) {
		return &planner.PlanNode{
			Type:          planner.NodeTableScan,
			Relation:      subSQL,
			TotalCost:     10.0,
			EstimatedRows: 100,
		}, nil
	})
	if err != nil {
		t.Fatalf("BuildSetOpPlan failed: %v", err)
	}

	// Root should be NodeLimit
	if plan.Type != planner.NodeLimit || plan.LimitCount != 5 {
		t.Fatalf("expected root NodeLimit with count 5, got %v (%d)", plan.Type, plan.LimitCount)
	}

	// Child should be NodeSort
	if len(plan.Children) != 1 || plan.Children[0].Type != planner.NodeSort {
		t.Fatalf("expected child NodeSort, got %v", plan.Children)
	}
	sortNode := plan.Children[0]
	if sortNode.SortKey != "id" || sortNode.SortOrder != "DESC" {
		t.Errorf("sort node mismatch: %s %s", sortNode.SortKey, sortNode.SortOrder)
	}

	// Child should be NodeSetOp
	if len(sortNode.Children) != 1 || sortNode.Children[0].Type != planner.NodeSetOp {
		t.Fatalf("expected child NodeSetOp, got %v", sortNode.Children)
	}
	setNode := sortNode.Children[0]
	if setNode.SetOp != "UNION" || len(setNode.Children) != 2 {
		t.Errorf("set node mismatch: op=%s, children=%d", setNode.SetOp, len(setNode.Children))
	}
}
