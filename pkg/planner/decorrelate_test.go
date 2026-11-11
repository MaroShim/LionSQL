package planner

import (
	"testing"
)

func TestDecorrelate_CorrelatedExists(t *testing.T) {
	p := NewPlanner()
	q, err := p.ParseSelect("SELECT u.id, u.name FROM users u WHERE EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id);")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	err = p.DecorrelateSubqueries(q, nil)
	if err != nil {
		t.Fatalf("DecorrelateSubqueries failed: %v", err)
	}

	if len(q.Joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(q.Joins))
	}
	j := q.Joins[0]
	if j.Type != "SEMI" {
		t.Errorf("expected SEMI join, got %s", j.Type)
	}
	if j.Table != "orders" || j.Alias != "o" {
		t.Errorf("expected table 'orders' alias 'o', got table %q alias %q", j.Table, j.Alias)
	}
	if j.LeftKey != "u.id" || j.RightKey != "o.user_id" {
		t.Errorf("expected keys u.id = o.user_id, got %s = %s", j.LeftKey, j.RightKey)
	}
	if q.WhereClause != "" {
		t.Errorf("expected empty where clause after decorrelation, got %q", q.WhereClause)
	}
}

func TestDecorrelate_CorrelatedNotExists(t *testing.T) {
	p := NewPlanner()
	q, err := p.ParseSelect("SELECT u.id, u.name FROM users u WHERE NOT EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id);")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	err = p.DecorrelateSubqueries(q, nil)
	if err != nil {
		t.Fatalf("DecorrelateSubqueries failed: %v", err)
	}

	if len(q.Joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(q.Joins))
	}
	j := q.Joins[0]
	if j.Type != "ANTI" {
		t.Errorf("expected ANTI join, got %s", j.Type)
	}
	if j.LeftKey != "u.id" || j.RightKey != "o.user_id" {
		t.Errorf("expected keys u.id = o.user_id, got %s = %s", j.LeftKey, j.RightKey)
	}
	if q.WhereClause != "" {
		t.Errorf("expected empty where clause, got %q", q.WhereClause)
	}
}

func TestDecorrelate_ExistsWithExtraFilter(t *testing.T) {
	p := NewPlanner()
	q, err := p.ParseSelect("SELECT u.id, u.name FROM users u WHERE u.status = 'active' AND EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id AND o.amount > 500);")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	err = p.DecorrelateSubqueries(q, nil)
	if err != nil {
		t.Fatalf("DecorrelateSubqueries failed: %v", err)
	}

	if len(q.Joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(q.Joins))
	}
	j := q.Joins[0]
	if j.Type != "SEMI" {
		t.Errorf("expected SEMI join, got %s", j.Type)
	}
	if j.Filter != "o.amount > 500" {
		t.Errorf("expected filter 'o.amount > 500', got %q", j.Filter)
	}
	if q.WhereClause != "u.status = 'active'" {
		t.Errorf("expected where clause \"u.status = 'active'\", got %q", q.WhereClause)
	}
}

func TestDecorrelate_InSubquery(t *testing.T) {
	p := NewPlanner()
	q, err := p.ParseSelect("SELECT e.id, e.name FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d WHERE d.budget > 1000);")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	err = p.DecorrelateSubqueries(q, nil)
	if err != nil {
		t.Fatalf("DecorrelateSubqueries failed: %v", err)
	}

	if len(q.Joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(q.Joins))
	}
	j := q.Joins[0]
	if j.Type != "SEMI" {
		t.Errorf("expected SEMI join, got %s", j.Type)
	}
	if j.Table != "depts" || j.Alias != "d" {
		t.Errorf("expected table 'depts' alias 'd', got table %q alias %q", j.Table, j.Alias)
	}
	if j.LeftKey != "e.dept_id" || j.RightKey != "d.id" {
		t.Errorf("expected keys e.dept_id = d.id, got %s = %s", j.LeftKey, j.RightKey)
	}
	if j.Filter != "d.budget > 1000" {
		t.Errorf("expected filter 'd.budget > 1000', got %q", j.Filter)
	}
	if q.WhereClause != "" {
		t.Errorf("expected empty where clause, got %q", q.WhereClause)
	}
}

func TestDecorrelate_NotInSubquery(t *testing.T) {
	p := NewPlanner()
	q, err := p.ParseSelect("SELECT e.id, e.name FROM emps e WHERE e.dept_id NOT IN (SELECT d.id FROM depts d WHERE d.budget > 1000);")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	err = p.DecorrelateSubqueries(q, nil)
	if err != nil {
		t.Fatalf("DecorrelateSubqueries failed: %v", err)
	}

	if len(q.Joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(q.Joins))
	}
	j := q.Joins[0]
	if j.Type != "ANTI" {
		t.Errorf("expected ANTI join, got %s", j.Type)
	}
	if j.LeftKey != "e.dept_id" || j.RightKey != "d.id" {
		t.Errorf("expected keys e.dept_id = d.id, got %s = %s", j.LeftKey, j.RightKey)
	}
}

func TestDecorrelate_MultipleSubqueries(t *testing.T) {
	p := NewPlanner()
	q, err := p.ParseSelect("SELECT e.id FROM emps e WHERE EXISTS (SELECT 1 FROM depts d WHERE d.id = e.dept_id) AND NOT EXISTS (SELECT 1 FROM blacklist b WHERE b.emp_id = e.id);")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	err = p.DecorrelateSubqueries(q, nil)
	if err != nil {
		t.Fatalf("DecorrelateSubqueries failed: %v", err)
	}

	if len(q.Joins) != 2 {
		t.Fatalf("expected 2 joins, got %d", len(q.Joins))
	}
	if q.Joins[0].Type != "SEMI" || q.Joins[0].Table != "depts" {
		t.Errorf("expected 1st join SEMI depts, got %s %s", q.Joins[0].Type, q.Joins[0].Table)
	}
	if q.Joins[1].Type != "ANTI" || q.Joins[1].Table != "blacklist" {
		t.Errorf("expected 2nd join ANTI blacklist, got %s %s", q.Joins[1].Type, q.Joins[1].Table)
	}
	if q.WhereClause != "" {
		t.Errorf("expected empty where clause, got %q", q.WhereClause)
	}
}

func TestDecorrelate_UncorrelatedExistsPreserved(t *testing.T) {
	p := NewPlanner()
	q, err := p.ParseSelect("SELECT * FROM users WHERE EXISTS (SELECT 1 FROM config WHERE active = 1);")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	err = p.DecorrelateSubqueries(q, nil)
	if err != nil {
		t.Fatalf("DecorrelateSubqueries failed: %v", err)
	}

	if len(q.Joins) != 0 {
		t.Errorf("expected 0 joins for uncorrelated exists, got %d", len(q.Joins))
	}
	if q.WhereClause == "" {
		t.Errorf("expected where clause to be preserved for uncorrelated exists")
	}
}

// TestDecorrelate_NonEqualityCondition_NotDecorrelatedAsSemiJoin verifies that non-equality
// conditions (>=, <=, !=) are strictly rejected by isIdent and not corrupted into equality SEMI joins (AUDIT-FULL-025).
func TestDecorrelate_NonEqualityCondition_NotDecorrelatedAsSemiJoin(t *testing.T) {
	// 1. Direct unit verification of isIdent
	invalidIdents := []string{
		"o.amount >",
		"o.amount <",
		"o.status !",
		"o.val + 1",
		"o.val - 2",
		"o.val * 3",
		"o.val / 4",
		"o.val == 5",
	}
	for _, s := range invalidIdents {
		if isIdent(s) {
			t.Fatalf("CRITICAL BUG: isIdent(%q) returned true; non-identifier with operator must return false", s)
		}
	}

	validIdents := []string{
		"o.amount",
		"u.min_amount",
		"id",
		"_sys_col",
		"@param",
		"[orders].[user_id]",
	}
	for _, s := range validIdents {
		if !isIdent(s) {
			t.Errorf("expected isIdent(%q) = true, got false", s)
		}
	}

	// 2. Query level verification: WHERE EXISTS with >= condition must NOT become SEMI join
	p := NewPlanner()
	q, err := p.ParseSelect("SELECT u.id FROM users u WHERE EXISTS (SELECT 1 FROM orders o WHERE o.amount >= u.min_amount);")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	err = p.DecorrelateSubqueries(q, nil)
	if err != nil {
		t.Fatalf("DecorrelateSubqueries failed: %v", err)
	}

	if len(q.Joins) != 0 {
		t.Fatalf("CRITICAL SEMANTIC DISTORTION: non-equality >= condition was converted into %d joins (type: %s, keys: %s = %s)",
			len(q.Joins), q.Joins[0].Type, q.Joins[0].LeftKey, q.Joins[0].RightKey)
	}
}
