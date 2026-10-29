package planner_test

import (
	"strings"
	"testing"

	"lionsql/pkg/planner"
	"lionsql/pkg/storage"
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

func TestPlanner_IndexOnlyScan(t *testing.T) {
	p := planner.NewPlanner()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeFloat},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:    "idx_accounts_email",
				Columns: []string{"email"},
				Unique:  true,
			},
		},
	}

	// 1. Projection with only indexed column (email) -> Index Only Scan
	q1, err := p.ParseSelect("EXPLAIN SELECT email FROM accounts WHERE email = 'alice@example.com'")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	plan1 := p.BuildPlan(q1, schema, 1000)
	lines1 := planner.FormatPlanText(plan1, false)
	fullText1 := strings.Join(lines1, "\n")
	if !strings.Contains(fullText1, "Index Only Scan using idx_accounts_email on accounts") {
		t.Errorf("expected Index Only Scan in plan text, got:\n%s", fullText1)
	}

	// 2. Projection with primary key and indexed column (id, email) -> Index Only Scan
	q2, err := p.ParseSelect("EXPLAIN SELECT id, email FROM accounts WHERE email = 'alice@example.com'")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	plan2 := p.BuildPlan(q2, schema, 1000)
	lines2 := planner.FormatPlanText(plan2, false)
	fullText2 := strings.Join(lines2, "\n")
	if !strings.Contains(fullText2, "Index Only Scan using idx_accounts_email on accounts") {
		t.Errorf("expected Index Only Scan in plan text, got:\n%s", fullText2)
	}

	// 3. Projection with unindexed column (balance) -> Regular Index Scan
	q3, err := p.ParseSelect("EXPLAIN SELECT id, balance FROM accounts WHERE email = 'alice@example.com'")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	plan3 := p.BuildPlan(q3, schema, 1000)
	lines3 := planner.FormatPlanText(plan3, false)
	fullText3 := strings.Join(lines3, "\n")
	if !strings.Contains(fullText3, "Index Scan using idx_accounts_email on accounts") {
		t.Errorf("expected Index Scan in plan text, got:\n%s", fullText3)
	}
}

func TestPlanner_CoveringIndexWithInclude(t *testing.T) {
	p := planner.NewPlanner()

	schema := &storage.TableSchema{
		Name: "employees",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "dept", Type: storage.TypeVarChar},
			{Name: "salary", Type: storage.TypeFloat},
			{Name: "title", Type: storage.TypeVarChar},
			{Name: "bio", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:        "idx_emp_dept_covering",
				TableName:   "employees",
				Columns:     []string{"dept"},
				IncludeCols: []string{"salary", "title"},
				Unique:      true,
			},
		},
	}

	// 1. Projections covered by dept, salary, title, and id -> Index Only Scan
	q1, err := p.ParseSelect("EXPLAIN SELECT id, dept, salary, title FROM employees WHERE dept = 'Engineering'")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	plan1 := p.BuildPlan(q1, schema, 1000)
	lines1 := planner.FormatPlanText(plan1, false)
	fullText1 := strings.Join(lines1, "\n")
	if !strings.Contains(fullText1, "Index Only Scan using idx_emp_dept_covering on employees") {
		t.Errorf("expected Index Only Scan in plan text, got:\n%s", fullText1)
	}

	// 2. Projection includes unindexed/unincluded column (bio) -> Regular Index Scan
	q2, err := p.ParseSelect("EXPLAIN SELECT id, dept, bio FROM employees WHERE dept = 'Engineering'")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	plan2 := p.BuildPlan(q2, schema, 1000)
	lines2 := planner.FormatPlanText(plan2, false)
	fullText2 := strings.Join(lines2, "\n")
	if !strings.Contains(fullText2, "Index Scan using idx_emp_dept_covering on employees") {
		t.Errorf("expected Index Scan in plan text, got:\n%s", fullText2)
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

	if !strings.Contains(fullText, "actual time=0.000..0.250 rows=10 loops=1") {
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

func TestPlanner_MultiTableChainedJoins(t *testing.T) {
	p := planner.NewPlanner()

	// 1. Parsing a 3-table chained join query
	sql3 := `SELECT u.name, o.amount, p.status FROM users u JOIN orders o ON u.id = o.user_id JOIN payments p ON o.id = p.order_id WHERE u.name = 'Alice'`
	q3, err := p.ParseSelect(sql3)
	if err != nil {
		t.Fatalf("ParseSelect failed for 3-table join: %v", err)
	}

	if q3.TableName != "users" || q3.TableAlias != "u" {
		t.Errorf("expected primary table users u, got %s %s", q3.TableName, q3.TableAlias)
	}
	if len(q3.Joins) != 2 {
		t.Fatalf("expected 2 joins in Joins slice, got %d", len(q3.Joins))
	}
	if q3.Joins[0].Table != "orders" || q3.Joins[0].Alias != "o" || q3.Joins[0].LeftKey != "u.id" || q3.Joins[0].RightKey != "o.user_id" {
		t.Errorf("unexpected join 0: %+v", q3.Joins[0])
	}
	if q3.Joins[1].Table != "payments" || q3.Joins[1].Alias != "p" || q3.Joins[1].LeftKey != "o.id" || q3.Joins[1].RightKey != "p.order_id" {
		t.Errorf("unexpected join 1: %+v", q3.Joins[1])
	}

	// 2. Parsing a 4-table chained join with mixed INNER and LEFT joins
	sql4 := `SELECT * FROM t1 JOIN t2 ON t1.id = t2.t1_id LEFT JOIN t3 ON t2.id = t3.t2_id JOIN t4 ON t3.id = t4.t3_id`
	q4, err := p.ParseSelect(sql4)
	if err != nil {
		t.Fatalf("ParseSelect failed for 4-table join: %v", err)
	}
	if len(q4.Joins) != 3 {
		t.Fatalf("expected 3 joins in Joins slice, got %d", len(q4.Joins))
	}
	if q4.Joins[0].Type != "INNER" || q4.Joins[1].Type != "LEFT" || q4.Joins[2].Type != "INNER" {
		t.Errorf("unexpected join types: %s, %s, %s", q4.Joins[0].Type, q4.Joins[1].Type, q4.Joins[2].Type)
	}

	// 3. CBO Join Planning: verify left-deep tree with mixed Hash Join and Index Nested Loop Join
	userSchema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	orderSchema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
			{Name: "amount", Type: storage.TypeFloat},
		},
	}
	// Payments has an index on order_id -> should favor Index Nested Loop Join
	paymentSchema := &storage.TableSchema{
		Name: "payments",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "order_id", Type: storage.TypeInt},
			{Name: "status", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:    "idx_payments_order_id",
				Columns: []string{"order_id"},
				Unique:  false,
			},
		},
	}

	userStats := planner.NewTableStats("users", 100)
	orderStats := planner.NewTableStats("orders", 500)
	paymentStats := planner.NewTableStats("payments", 1000)

	rSchemas := map[string]*storage.TableSchema{
		"orders":   orderSchema,
		"o":        orderSchema,
		"payments": paymentSchema,
		"p":        paymentSchema,
	}
	rStats := map[string]*planner.TableStats{
		"orders":   orderStats,
		"o":        orderStats,
		"payments": paymentStats,
		"p":        paymentStats,
	}

	pl := planner.NewPlanner()
	qInfo, err := pl.ParseSelect("SELECT issue_id, count(*) as count FROM [comment] WHERE [issue_id] IN (49) AND (type = 22) GROUP BY issue_id")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	if qInfo.TableName != "comment" && qInfo.TableName != "[comment]" {
		t.Fatalf("unexpected TableName: %q", qInfo.TableName)
	}

	plan := p.BuildMultiJoinPlanWithStats(q3, userSchema, userStats, rSchemas, rStats)
	if plan == nil {
		t.Fatal("expected non-nil multi-join plan")
	}

	lines := planner.FormatPlanText(plan, false)
	fullPlanText := strings.Join(lines, "\n")

	// Plan should have Filter, then Nested Loop (payments), then Hash Join (orders)
	if !strings.Contains(fullPlanText, "Filter (u.name = 'Alice')") {
		t.Errorf("expected Filter in plan, got:\n%s", fullPlanText)
	}
	if !strings.Contains(fullPlanText, "Nested Loop") {
		t.Errorf("expected Nested Loop in plan for payments with index, got:\n%s", fullPlanText)
	}
	if !strings.Contains(fullPlanText, "Hash Join") {
		t.Errorf("expected Hash Join in plan for orders, got:\n%s", fullPlanText)
	}
	if !strings.Contains(fullPlanText, "Index Scan using idx_payments_order_id on payments") {
		t.Errorf("expected Index Scan on payments, got:\n%s", fullPlanText)
	}
}

func TestPlanner_StandardPagingAndTop(t *testing.T) {
	p := planner.NewPlanner()

	// 1. OFFSET 10 ROWS FETCH NEXT 5 ROWS ONLY
	sql1 := "SELECT id, name FROM accounts WHERE balance > 1000 ORDER BY balance DESC OFFSET 10 ROWS FETCH NEXT 5 ROWS ONLY"
	q1, err := p.ParseSelect(sql1)
	if err != nil {
		t.Fatalf("ParseSelect failed for ANSI paging: %v", err)
	}
	if q1.OffsetCount != 10 || q1.LimitCount != 5 {
		t.Errorf("expected offset 10, limit 5, got offset %d, limit %d", q1.OffsetCount, q1.LimitCount)
	}

	// 2. FETCH FIRST 10 ROWS ONLY without OFFSET
	sql2 := "SELECT id, name FROM accounts ORDER BY id FETCH FIRST 10 ROWS ONLY"
	q2, err := p.ParseSelect(sql2)
	if err != nil {
		t.Fatalf("ParseSelect failed for FETCH only: %v", err)
	}
	if q2.OffsetCount != 0 || q2.LimitCount != 10 {
		t.Errorf("expected offset 0, limit 10, got offset %d, limit %d", q2.OffsetCount, q2.LimitCount)
	}

	// 3. TOP 5
	sql3 := "SELECT TOP 5 id, name FROM accounts WHERE balance > 500 ORDER BY id"
	q3, err := p.ParseSelect(sql3)
	if err != nil {
		t.Fatalf("ParseSelect failed for TOP: %v", err)
	}
	if q3.LimitCount != 5 {
		t.Errorf("expected limit 5, got %d", q3.LimitCount)
	}
	if len(q3.Projections) != 2 || q3.Projections[0] != "id" || q3.Projections[1] != "name" {
		t.Errorf("unexpected projections: %v", q3.Projections)
	}

	// 4. DISTINCT TOP (10)
	sql4 := "SELECT DISTINCT TOP (10) id FROM accounts"
	q4, err := p.ParseSelect(sql4)
	if err != nil {
		t.Fatalf("ParseSelect failed for DISTINCT TOP: %v", err)
	}
	if q4.LimitCount != 10 {
		t.Errorf("expected limit 10, got %d", q4.LimitCount)
	}
	if !strings.Contains(q4.Projections[0], "id") {
		t.Errorf("expected projection id, got %v", q4.Projections)
	}
}
