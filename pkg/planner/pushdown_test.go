package planner_test

import (
	"strings"
	"testing"

	"lionsql/pkg/planner"
	"lionsql/pkg/storage"
)

func TestPushdown_ExtractColumnRefs(t *testing.T) {
	tests := []struct {
		cond     string
		expected []planner.ColumnRef
	}{
		{
			cond:     "balance > 100",
			expected: []planner.ColumnRef{{TableOrAlias: "", Column: "balance"}},
		},
		{
			cond:     "u.name = 'Alice'",
			expected: []planner.ColumnRef{{TableOrAlias: "u", Column: "name"}},
		},
		{
			cond:     "UPPER(u.email) = 'ALICE@EXAMPLE.COM'",
			expected: []planner.ColumnRef{{TableOrAlias: "u", Column: "email"}},
		},
		{
			cond: "u.id = o.user_id",
			expected: []planner.ColumnRef{
				{TableOrAlias: "u", Column: "id"},
				{TableOrAlias: "o", Column: "user_id"},
			},
		},
		{
			cond:     "1 = 1",
			expected: nil,
		},
		{
			cond:     "CAST(id AS INT) > 10",
			expected: []planner.ColumnRef{{TableOrAlias: "", Column: "id"}},
		},
		{
			cond:     "TRY_CAST(u.age AS BIGINT) IS NOT NULL",
			expected: []planner.ColumnRef{{TableOrAlias: "u", Column: "age"}},
		},
		{
			cond:     "CONVERT(INT, id) = 5",
			expected: []planner.ColumnRef{{TableOrAlias: "", Column: "id"}},
		},
		{
			cond:     "TRY_CONVERT(VARCHAR(50), o.order_id) = '100'",
			expected: []planner.ColumnRef{{TableOrAlias: "o", Column: "order_id"}},
		},
		{
			cond:     "val::FLOAT > 3.14",
			expected: []planner.ColumnRef{{TableOrAlias: "", Column: "val"}},
		},
		{
			cond:     "CAST(amount AS DOUBLE PRECISION) < 1000.0",
			expected: []planner.ColumnRef{{TableOrAlias: "", Column: "amount"}},
		},
		{
			cond:     "name = 'Alice' COLLATE Latin1_General_BIN",
			expected: []planner.ColumnRef{{TableOrAlias: "", Column: "name"}},
		},
	}

	for _, tt := range tests {
		refs := planner.ExtractColumnRefs(tt.cond)
		if len(refs) != len(tt.expected) {
			t.Fatalf("ExtractColumnRefs(%q): expected %d refs, got %d: %+v", tt.cond, len(tt.expected), len(refs), refs)
		}
		for i, exp := range tt.expected {
			if !strings.EqualFold(refs[i].TableOrAlias, exp.TableOrAlias) || !strings.EqualFold(refs[i].Column, exp.Column) {
				t.Errorf("ExtractColumnRefs(%q) [%d]: expected %+v, got %+v", tt.cond, i, exp, refs[i])
			}
		}
	}
}

func TestPushdown_PartitionPredicates(t *testing.T) {
	p := planner.NewPlanner()

	schemaUsers := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "status", Type: storage.TypeVarChar},
			{Name: "score", Type: storage.TypeInt},
		},
	}
	schemaOrders := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "order_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
			{Name: "amount", Type: storage.TypeFloat},
			{Name: "min_score", Type: storage.TypeInt},
		},
	}

	rightSchemas := map[string]*storage.TableSchema{
		"orders": schemaOrders,
		"o":      schemaOrders,
	}

	// 1. Inner Join: split into primary, join, and cross-table
	q1, err := p.ParseSelect("SELECT u.name, o.amount FROM users u JOIN orders o ON u.id = o.user_id WHERE u.status = 'ACTIVE' AND o.amount > 500 AND u.score > o.min_score")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	res1 := planner.PushDownPredicates(q1, schemaUsers, rightSchemas)
	if len(res1.PrimaryConds) != 1 || res1.PrimaryConds[0] != "u.status = 'ACTIVE'" {
		t.Errorf("expected PrimaryConds [u.status = 'ACTIVE'], got %v", res1.PrimaryConds)
	}
	if len(res1.JoinConds[0]) != 1 || res1.JoinConds[0][0] != "o.amount > 500" {
		t.Errorf("expected JoinConds[0] [o.amount > 500], got %v", res1.JoinConds[0])
	}
	if len(res1.RemainingConds) != 1 || res1.RemainingConds[0] != "u.score > o.min_score" {
		t.Errorf("expected RemainingConds [u.score > o.min_score], got %v", res1.RemainingConds)
	}

	// 2. Left Outer Join: right table condition must stay in remaining
	q2, err := p.ParseSelect("SELECT u.name, o.amount FROM users u LEFT JOIN orders o ON u.id = o.user_id WHERE u.status = 'ACTIVE' AND o.amount > 500")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	res2 := planner.PushDownPredicates(q2, schemaUsers, rightSchemas)
	if len(res2.PrimaryConds) != 1 || res2.PrimaryConds[0] != "u.status = 'ACTIVE'" {
		t.Errorf("expected PrimaryConds [u.status = 'ACTIVE'], got %v", res2.PrimaryConds)
	}
	if len(res2.JoinConds[0]) != 0 {
		t.Errorf("expected 0 JoinConds for LEFT JOIN right table condition, got %v", res2.JoinConds[0])
	}
	if len(res2.RemainingConds) != 1 || res2.RemainingConds[0] != "o.amount > 500" {
		t.Errorf("expected RemainingConds [o.amount > 500], got %v", res2.RemainingConds)
	}

	// 3. Unqualified column resolution
	q3, err := p.ParseSelect("SELECT name, amount FROM users JOIN orders ON users.id = orders.user_id WHERE name = 'Alice' AND amount > 100")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	res3 := planner.PushDownPredicates(q3, schemaUsers, rightSchemas)
	if len(res3.PrimaryConds) != 1 || res3.PrimaryConds[0] != "name = 'Alice'" {
		t.Errorf("expected unqualified name to resolve to PrimaryConds, got %v", res3.PrimaryConds)
	}
	if len(res3.JoinConds[0]) != 1 || res3.JoinConds[0][0] != "amount > 100" {
		t.Errorf("expected unqualified amount to resolve to JoinConds[0], got %v", res3.JoinConds[0])
	}
	if len(res3.RemainingConds) != 0 {
		t.Errorf("expected 0 RemainingConds, got %v", res3.RemainingConds)
	}
}

func TestPushdown_IndexScanSelectionInJoin(t *testing.T) {
	p := planner.NewPlanner()

	schemaUsers := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar},
			{Name: "status", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:    "idx_users_email",
				Columns: []string{"email"},
				Unique:  true,
			},
		},
	}
	schemaOrders := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
			{Name: "amount", Type: storage.TypeFloat},
		},
	}

	q, err := p.ParseSelect("SELECT u.id, o.amount FROM users u JOIN orders o ON u.id = o.user_id WHERE u.email = 'alice@example.com'")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	userStats := planner.NewTableStats("users", 1000)
	orderStats := planner.NewTableStats("orders", 5000)

	plan := p.BuildJoinPlanWithStats(q, schemaUsers, userStats, schemaOrders, orderStats)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	lines := planner.FormatPlanText(plan, false)
	fullText := strings.Join(lines, "\n")

	// Verify that users scan uses idx_users_email Index Scan inside the join!
	if !strings.Contains(fullText, "Index Scan using idx_users_email on users") {
		t.Errorf("expected Index Scan on users in join plan, got:\n%s", fullText)
	}
	// Verify that Hash Join, Nested Loop, or Merge Join was generated
	if !strings.Contains(fullText, "Hash Join") && !strings.Contains(fullText, "Nested Loop") && !strings.Contains(fullText, "Merge Join") {
		t.Errorf("expected join in plan, got:\n%s", fullText)
	}
	// Verify no redundant top-level filter
	if strings.HasPrefix(lines[0], "Filter") {
		t.Errorf("expected no root Filter node since condition was pushed down to index scan, got root:\n%s", lines[0])
	}
}

func TestPushdown_ChainedThreeTableJoinFilterPushdown(t *testing.T) {
	p := planner.NewPlanner()

	schemaUsers := &storage.TableSchema{
		Name:    "users",
		Columns: []storage.Column{{Name: "id", Type: storage.TypeInt}, {Name: "name", Type: storage.TypeVarChar}},
	}
	schemaOrders := &storage.TableSchema{
		Name:    "orders",
		Columns: []storage.Column{{Name: "id", Type: storage.TypeInt}, {Name: "user_id", Type: storage.TypeInt}, {Name: "total", Type: storage.TypeFloat}},
	}
	schemaPayments := &storage.TableSchema{
		Name:    "payments",
		Columns: []storage.Column{{Name: "id", Type: storage.TypeInt}, {Name: "order_id", Type: storage.TypeInt}, {Name: "status", Type: storage.TypeVarChar}},
	}

	rSchemas := map[string]*storage.TableSchema{
		"orders":   schemaOrders,
		"o":        schemaOrders,
		"payments": schemaPayments,
		"p":        schemaPayments,
	}

	sql := "SELECT u.name, o.total, p.status FROM users u JOIN orders o ON u.id = o.user_id JOIN payments p ON o.id = p.order_id WHERE u.name = 'Bob' AND o.total > 100 AND p.status = 'PAID'"
	q, err := p.ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	plan := p.BuildMultiJoinPlanWithStats(q, schemaUsers, nil, rSchemas, nil)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	lines := planner.FormatPlanText(plan, false)
	fullText := strings.Join(lines, "\n")

	// u.name = 'Bob' must be pushed down to users
	if !strings.Contains(fullText, "Filter (u.name = 'Bob')") {
		t.Errorf("expected pushed-down filter on users, got:\n%s", fullText)
	}
	// o.total > 100 must be pushed down to orders
	if !strings.Contains(fullText, "Filter (o.total > 100)") {
		t.Errorf("expected pushed-down filter on orders, got:\n%s", fullText)
	}
	// p.status = 'PAID' must be pushed down to payments
	if !strings.Contains(fullText, "Filter (p.status = 'PAID')") {
		t.Errorf("expected pushed-down filter on payments, got:\n%s", fullText)
	}
	// Root node should NOT be a Filter because all 3 predicates were pushed down!
	if strings.HasPrefix(lines[0], "Filter") {
		t.Errorf("expected root node to be Join rather than Filter, got:\n%s", lines[0])
	}
}

func TestPushdown_CompositeQualifiersAndImplicitAlias(t *testing.T) {
	p := planner.NewPlanner()
	schemaUsers := &storage.TableSchema{
		Name:    "users",
		Columns: []storage.Column{{Name: "id", Type: storage.TypeInt}, {Name: "name", Type: storage.TypeVarChar}},
	}

	sql := "SELECT a.id, a.name FROM [mydb].[dbo].[users] a WHERE a.name = 'Alice'"
	q, err := p.ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	if q.TableName != "users" || q.TableAlias != "a" {
		t.Fatalf("expected TableName: users, TableAlias: a, got TableName: %q, TableAlias: %q", q.TableName, q.TableAlias)
	}

	res := planner.PushDownPredicates(q, schemaUsers, nil)
	if len(res.PrimaryConds) != 1 || res.PrimaryConds[0] != "a.name = 'Alice'" {
		t.Errorf("expected pushed down primary cond 'a.name = 'Alice'', got %+v", res.PrimaryConds)
	}
}

func TestPushdown_TransitivePredicateInference(t *testing.T) {
	p := planner.NewPlanner()
	schemaOrders := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "order_id", Type: storage.TypeInt},
			{Name: "cust_id", Type: storage.TypeInt},
			{Name: "amount", Type: storage.TypeDecimal},
		},
	}
	schemaCustomers := &storage.TableSchema{
		Name: "customers",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	rightSchemas := map[string]*storage.TableSchema{
		"customers": schemaCustomers,
		"c":         schemaCustomers,
	}

	// Case 1: Equality predicate o.cust_id = 42 infers c.id = 42
	sql := "SELECT o.amount, c.name FROM orders o JOIN customers c ON o.cust_id = c.id WHERE o.cust_id = 42"
	q, err := p.ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	res := planner.PushDownPredicates(q, schemaOrders, rightSchemas)
	if len(res.PrimaryConds) == 0 || !strings.Contains(res.PrimaryConds[0], "42") {
		t.Errorf("expected PrimaryConds to contain cust_id = 42, got %+v", res.PrimaryConds)
	}

	// Verify transitive inference on customers:
	joinConds, ok := res.JoinConds[0]
	if !ok || len(joinConds) == 0 {
		t.Fatalf("expected transitive condition on customers, got none. JoinConds: %+v", res.JoinConds)
	}
	found := false
	for _, c := range joinConds {
		if strings.Contains(c, "id = 42") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected c.id = 42 inferred for customers, got %+v", joinConds)
	}

	// Case 2: Range predicate o.cust_id > 100 infers c.id > 100
	sqlRange := "SELECT o.amount, c.name FROM orders o JOIN customers c ON o.cust_id = c.id WHERE o.cust_id > 100"
	qRange, err := p.ParseSelect(sqlRange)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	resRange := planner.PushDownPredicates(qRange, schemaOrders, rightSchemas)
	joinCondsRange := resRange.JoinConds[0]
	foundRange := false
	for _, c := range joinCondsRange {
		if strings.Contains(c, "id > 100") {
			foundRange = true
			break
		}
	}
	if !foundRange {
		t.Errorf("expected c.id > 100 inferred for customers, got %+v", joinCondsRange)
	}
}

func TestPushdown_CollectReferencedColumns(t *testing.T) {
	p := planner.NewPlanner()
	schemaWide := &storage.TableSchema{
		Name: "wide_table",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "age", Type: storage.TypeInt},
			{Name: "email", Type: storage.TypeVarChar},
			{Name: "payload", Type: storage.TypeVarChar},
			{Name: "c5", Type: storage.TypeInt},
			{Name: "c6", Type: storage.TypeInt},
			{Name: "c7", Type: storage.TypeInt},
			{Name: "c8", Type: storage.TypeInt},
			{Name: "c9", Type: storage.TypeInt},
		},
	}

	// Case 1: SELECT id, name FROM wide_table WHERE age > 20
	q1, err := p.ParseSelect("SELECT id, name FROM wide_table WHERE age > 20")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	cols1 := planner.CollectReferencedColumns(schemaWide, q1.TableName, q1.TableAlias, q1)
	expected1 := []int{0, 1, 2} // id (0), name (1), age (2)
	if len(cols1) != len(expected1) {
		t.Fatalf("expected cols %v, got %v", expected1, cols1)
	}
	for i, v := range expected1 {
		if cols1[i] != v {
			t.Errorf("at index %d: expected %d, got %d", i, v, cols1[i])
		}
	}

	// Case 2: Wildcard SELECT * FROM wide_table -> returns nil (no pruning)
	q2, err := p.ParseSelect("SELECT * FROM wide_table")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	cols2 := planner.CollectReferencedColumns(schemaWide, q2.TableName, q2.TableAlias, q2)
	if cols2 != nil {
		t.Errorf("expected nil for SELECT *, got %v", cols2)
	}

	// Case 3: SELECT COUNT(*) FROM wide_table -> returns []int{0}
	q3, err := p.ParseSelect("SELECT COUNT(*) FROM wide_table")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	cols3 := planner.CollectReferencedColumns(schemaWide, q3.TableName, q3.TableAlias, q3)
	if len(cols3) != 1 || cols3[0] != 0 {
		t.Errorf("expected [0] for COUNT(*), got %v", cols3)
	}
}
