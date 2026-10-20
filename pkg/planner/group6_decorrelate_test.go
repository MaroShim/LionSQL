package planner_test

import (
	"strings"
	"testing"

	"lionsql/pkg/planner"
)

// TestGroup6_Decorrelate_SuffixTrimSafe tests BUG-DECORRELATE-TRIM-1
func TestGroup6_Decorrelate_SuffixTrimSafe(t *testing.T) {
	p := planner.NewPlanner()

	// Query with literal 'Poland' ending in "and"
	sql := "SELECT u.id FROM users u WHERE country = 'Poland' AND EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id);"
	q, err := p.ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	err = p.DecorrelateSubqueries(q, nil)
	if err != nil {
		t.Fatalf("DecorrelateSubqueries failed: %v", err)
	}

	if len(q.Joins) != 1 {
		t.Fatalf("expected 1 decorrelated join, got %d", len(q.Joins))
	}

	// Must NOT trim 'Poland' to 'Pol'!
	expectedWhere := "country = 'Poland'"
	if strings.TrimSpace(q.WhereClause) != expectedWhere {
		t.Errorf("WhereClause corrupted: expected %q, got %q", expectedWhere, q.WhereClause)
	}

	// Another query with identifier ending in "and" e.g. brand = 'Nike'
	sql2 := "SELECT u.id FROM users u WHERE brand = 'Nike' AND id IN (SELECT user_id FROM orders);"
	q2, err2 := p.ParseSelect(sql2)
	if err2 != nil {
		t.Fatalf("ParseSelect 2 failed: %v", err2)
	}
	err2 = p.DecorrelateSubqueries(q2, nil)
	if err2 != nil {
		t.Fatalf("DecorrelateSubqueries 2 failed: %v", err2)
	}
	if len(q2.Joins) != 1 {
		t.Fatalf("expected 1 decorrelated join for query 2, got %d", len(q2.Joins))
	}
	expectedWhere2 := "brand = 'Nike'"
	if strings.TrimSpace(q2.WhereClause) != expectedWhere2 {
		t.Errorf("WhereClause 2 corrupted: expected %q, got %q", expectedWhere2, q2.WhereClause)
	}
}

// TestGroup6_Decorrelate_OrBranchSubqueryNotDecorrelated tests BUG-PLANNER-DECORRELATE-OR-SUBQUERY-CORRUPT-1
func TestGroup6_Decorrelate_OrBranchSubqueryNotDecorrelated(t *testing.T) {
	p := planner.NewPlanner()

	// 1. Direct OR branch: must NOT be converted to SEMI join!
	sql := "SELECT u.id FROM users u WHERE status = 'active' OR EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id);"
	q, err := p.ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	err = p.DecorrelateSubqueries(q, nil)
	if err != nil {
		t.Fatalf("DecorrelateSubqueries failed: %v", err)
	}

	if len(q.Joins) != 0 {
		t.Errorf("expected 0 joins (subquery under OR must not be decorrelated to SEMI join), got %d: %v", len(q.Joins), q.Joins)
	}
	if !strings.Contains(q.WhereClause, "EXISTS") {
		t.Errorf("expected EXISTS subquery to be preserved in WhereClause, got: %q", q.WhereClause)
	}

	// 2. Parenthesized OR branch: must NOT be converted to SEMI join!
	sql2 := "SELECT u.id FROM users u WHERE (status = 'active' OR id IN (SELECT user_id FROM orders));"
	q2, err2 := p.ParseSelect(sql2)
	if err2 != nil {
		t.Fatalf("ParseSelect 2 failed: %v", err2)
	}
	err2 = p.DecorrelateSubqueries(q2, nil)
	if err2 != nil {
		t.Fatalf("DecorrelateSubqueries 2 failed: %v", err2)
	}
	if len(q2.Joins) != 0 {
		t.Errorf("expected 0 joins for parenthesized OR, got %d", len(q2.Joins))
	}

	// 3. Top-level AND with independent subquery:
	// Here u.dept = 'Sales' AND EXISTS (...) has no OR, so it MUST be safely decorrelated!
	sql3 := "SELECT u.id FROM users u WHERE u.dept = 'Sales' AND EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id);"
	q3, err3 := p.ParseSelect(sql3)
	if err3 != nil {
		t.Fatalf("ParseSelect 3 failed: %v", err3)
	}
	err3 = p.DecorrelateSubqueries(q3, nil)
	if err3 != nil {
		t.Fatalf("DecorrelateSubqueries 3 failed: %v", err3)
	}
	if len(q3.Joins) != 1 {
		t.Fatalf("expected 1 decorrelated join for top-level AND conjunct, got %d", len(q3.Joins))
	}
	expectedWhere3 := "u.dept = 'Sales'"
	if strings.TrimSpace(q3.WhereClause) != expectedWhere3 {
		t.Errorf("WhereClause 3 corrupted: expected %q, got %q", expectedWhere3, q3.WhereClause)
	}
}
