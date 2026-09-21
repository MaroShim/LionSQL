package planner_test

import (
	"strings"
	"testing"

	"sqlion/pkg/planner"
	"sqlion/pkg/storage"
)

func TestPlanner_SeqScanAndFilter(t *testing.T) {
	p := planner.NewPlanner()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeFloat},
		},
	}

	q, err := p.ParseSelect("SELECT id, name FROM accounts WHERE balance > 1000 ORDER BY balance DESC LIMIT 5")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	if q.TableName != "accounts" {
		t.Errorf("expected table accounts, got %s", q.TableName)
	}
	if len(q.Projections) != 2 || q.Projections[0] != "id" || q.Projections[1] != "name" {
		t.Errorf("unexpected projections: %v", q.Projections)
	}
	if q.LimitCount != 5 {
		t.Errorf("expected limit 5, got %d", q.LimitCount)
	}

	plan := p.BuildPlan(q, schema, 100)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	if plan.Type != planner.NodeLimit {
		t.Errorf("expected root Limit, got %v", plan.Type)
	}

	lines := planner.FormatPlanText(plan, false)
	if len(lines) == 0 {
		t.Fatal("expected non-empty plan lines")
	}

	fullText := strings.Join(lines, "\n")
	if !strings.Contains(fullText, "Limit") || !strings.Contains(fullText, "Seq Scan on accounts") {
		t.Errorf("expected Limit and Seq Scan in plan text, got:\n%s", fullText)
	}

	jsonStr, err := planner.FormatPlanJSON(plan)
	if err != nil || !strings.Contains(jsonStr, `"type": "Limit"`) {
		t.Errorf("unexpected json output: %v, err: %v", jsonStr, err)
	}
}

func TestPlanner_IndexScan(t *testing.T) {
	p := planner.NewPlanner()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:    "idx_accounts_email",
				Columns: []string{"email"},
				Unique:  true,
			},
		},
	}

	q, err := p.ParseSelect("EXPLAIN SELECT * FROM accounts WHERE email = 'alice@example.com'")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	plan := p.BuildPlan(q, schema, 1000)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	lines := planner.FormatPlanText(plan, false)
	fullText := strings.Join(lines, "\n")

	if !strings.Contains(fullText, "Index Scan using idx_accounts_email on accounts") {
		t.Errorf("expected Index Scan in plan text, got:\n%s", fullText)
	}
}

func TestPlanner_AnalyzeOutput(t *testing.T) {
	p := planner.NewPlanner()

	q, err := p.ParseSelect("EXPLAIN ANALYZE SELECT id FROM accounts")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	if !q.IsAnalyze {
		t.Errorf("expected IsAnalyze to be true")
	}

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}

	plan := p.BuildPlan(q, schema, 10)
	plan.ActualRows = 10
	plan.ActualDurationUS = 250

	lines := planner.FormatPlanText(plan, true)
	fullText := strings.Join(lines, "\n")

	if !strings.Contains(fullText, "actual time=0.250ms rows=10") {
		t.Errorf("expected actual time formatting in plan text, got:\n%s", fullText)
	}
}

func TestPlanner_HashJoin(t *testing.T) {
	p := planner.NewPlanner()

	q, err := p.ParseSelect("SELECT u.id, u.name, a.balance FROM users u INNER JOIN accounts a ON u.id = a.user_id WHERE a.balance > 500 ORDER BY a.balance DESC LIMIT 10")
	if err != nil {
		t.Fatalf("ParseSelect with JOIN failed: %v", err)
	}

	if q.Join == nil {
		t.Fatal("expected JoinInfo to be parsed")
	}
	if q.Join.Type != "INNER" || q.Join.Table != "accounts" || q.Join.Alias != "a" {
		t.Errorf("unexpected JoinInfo: %+v", q.Join)
	}

	schemaUsers := &storage.TableSchema{Name: "users", Columns: []storage.Column{{Name: "id", Type: storage.TypeInt}}}
	schemaAccounts := &storage.TableSchema{Name: "accounts", Columns: []storage.Column{{Name: "user_id", Type: storage.TypeInt}}}

	plan := p.BuildJoinPlan(q, schemaUsers, 100, schemaAccounts, 200)
	if plan == nil {
		t.Fatal("expected non-nil join plan")
	}

	lines := planner.FormatPlanText(plan, false)
	fullText := strings.Join(lines, "\n")

	if !strings.Contains(fullText, "Hash Join") || !strings.Contains(fullText, "Hash") || !strings.Contains(fullText, "on accounts a") {
		t.Errorf("expected Hash Join structure in plan text, got:\n%s", fullText)
	}
}

func TestPlanner_IndexNestedLoopJoin(t *testing.T) {
	p := planner.NewPlanner()

	q, err := p.ParseSelect("SELECT u.id, a.amount FROM users u JOIN orders a ON u.id = a.user_id")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	schemaUsers := &storage.TableSchema{Name: "users", Columns: []storage.Column{{Name: "id", Type: storage.TypeInt}}}
	schemaOrders := &storage.TableSchema{
		Name:    "orders",
		Columns: []storage.Column{{Name: "id", Type: storage.TypeInt}, {Name: "user_id", Type: storage.TypeInt}},
		Indexes: []storage.IndexSchema{{Name: "idx_orders_user_id", Columns: []string{"user_id"}}},
	}

	plan := p.BuildJoinPlan(q, schemaUsers, 50, schemaOrders, 500)
	if plan == nil {
		t.Fatal("expected non-nil join plan")
	}

	lines := planner.FormatPlanText(plan, false)
	fullText := strings.Join(lines, "\n")

	if !strings.Contains(fullText, "Nested Loop") || !strings.Contains(fullText, "Index Scan using idx_orders_user_id on orders") {
		t.Errorf("expected Nested Loop Join with Index Scan in plan text, got:\n%s", fullText)
	}
}

func TestPlanner_AggregateAndGroupBy(t *testing.T) {
	p := planner.NewPlanner()

	q, err := p.ParseSelect("SELECT branch, COUNT(*), SUM(balance), AVG(balance) FROM accounts WHERE balance > 100 GROUP BY branch HAVING SUM(balance) >= 5000 ORDER BY SUM(balance) DESC")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	if !q.HasAggregates {
		t.Errorf("expected HasAggregates to be true")
	}
	if len(q.GroupByCols) != 1 || q.GroupByCols[0] != "branch" {
		t.Errorf("unexpected GroupByCols: %v", q.GroupByCols)
	}

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "branch", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeFloat},
		},
	}

	plan := p.BuildPlan(q, schema, 1000)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	lines := planner.FormatPlanText(plan, false)
	fullText := strings.Join(lines, "\n")

	if !strings.Contains(fullText, "Aggregate (Group By: branch)") || !strings.Contains(fullText, "Having: SUM(balance) >= 5000") {
		t.Errorf("expected Aggregate with Group By and Having in plan text, got:\n%s", fullText)
	}
}

func TestPlanner_CTE(t *testing.T) {
	p := planner.NewPlanner()

	cteSQL := `WITH HighEarners AS (
		SELECT id, name, salary FROM employees WHERE salary >= 100000
	)
	SELECT name, salary FROM HighEarners ORDER BY salary DESC;`

	q, err := p.ParseSelect(cteSQL)
	if err != nil {
		t.Fatalf("ParseSelect with CTE failed: %v", err)
	}
	if len(q.CTEs) != 1 || q.CTEs[0].Name != "HighEarners" {
		t.Fatalf("unexpected CTE parsed: %+v", q.CTEs)
	}

	schema := &storage.TableSchema{
		Name: "HighEarners",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "salary", Type: storage.TypeInt},
		},
	}

	plan := p.BuildPlan(q, schema, 20)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	lines := planner.FormatPlanText(plan, false)
	fullText := strings.Join(lines, "\n")

	if !strings.Contains(fullText, "CTE Scan") || !strings.Contains(fullText, "Materialize (CTE: HighEarners)") {
		t.Errorf("expected CTE Scan and Materialize in plan text, got:\n%s", fullText)
	}
}




