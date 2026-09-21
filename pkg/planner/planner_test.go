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
