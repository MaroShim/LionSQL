package planner_test

import (
	"strings"
	"testing"

	"sqlion/pkg/planner"
	"sqlion/pkg/storage"
)

func TestCBOJoinOrderOptimization_SmallTableFirst(t *testing.T) {
	p := planner.NewPlanner()

	// Query: users u JOIN large_table l ON u.id = l.user_id JOIN tiny_table s ON u.id = s.user_id
	// In the SQL text, large_table comes first, then tiny_table.
	// CBO should recognize that tiny_table has far fewer rows (10 vs 100,000)
	// and reorder the join tree so tiny_table is joined first, minimizing intermediate cardinality.
	sql := `SELECT u.name, l.data, s.info FROM users u JOIN large_table l ON u.id = l.user_id JOIN tiny_table s ON u.id = s.user_id`
	q, err := p.ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	userSchema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	largeSchema := &storage.TableSchema{
		Name: "large_table",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
			{Name: "data", Type: storage.TypeVarChar},
		},
	}
	tinySchema := &storage.TableSchema{
		Name: "tiny_table",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
			{Name: "info", Type: storage.TypeVarChar},
		},
	}

	userStats := planner.NewTableStats("users", 1000)
	largeStats := planner.NewTableStats("large_table", 100000)
	tinyStats := planner.NewTableStats("tiny_table", 10)

	rSchemas := map[string]*storage.TableSchema{
		"large_table": largeSchema,
		"l":           largeSchema,
		"tiny_table":  tinySchema,
		"s":           tinySchema,
	}
	rStats := map[string]*planner.TableStats{
		"large_table": largeStats,
		"l":           largeStats,
		"tiny_table":  tinyStats,
		"s":           tinyStats,
	}

	plan := p.BuildMultiJoinPlanWithStats(q, userSchema, userStats, rSchemas, rStats)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	lines := planner.FormatPlanText(plan, false)
	fullPlanText := strings.Join(lines, "\n")

	rootJoin := plan
	if plan.Type == planner.NodeProjection && len(plan.Children) > 0 {
		rootJoin = plan.Children[0]
	}

	if rootJoin.Type != planner.NodeHashJoin {
		t.Fatalf("expected root to be HashJoin, got %s. Plan:\n%s", rootJoin.Type, fullPlanText)
	}

	// The top hash join should have large_table as its right child (or inner of top join)
	// and the lower hash join as its left child.
	leftChild := rootJoin.Children[0]
	rightChild := rootJoin.Children[1]

	// Verify top join is with large_table
	topRightRelation := ""
	if rightChild.Type == planner.NodeHash && len(rightChild.Children) > 0 {
		topRightRelation = rightChild.Children[0].Relation
	} else {
		topRightRelation = rightChild.Relation
	}
	if !strings.EqualFold(topRightRelation, "large_table") {
		t.Errorf("expected top join to build from large_table, got relation %q. Plan:\n%s", topRightRelation, fullPlanText)
	}

	// Verify bottom join is with tiny_table
	if leftChild.Type != planner.NodeHashJoin {
		t.Fatalf("expected left child to be HashJoin with tiny_table, got %s. Plan:\n%s", leftChild.Type, fullPlanText)
	}
	bottomRightRelation := ""
	if len(leftChild.Children) > 1 {
		bRight := leftChild.Children[1]
		if bRight.Type == planner.NodeHash && len(bRight.Children) > 0 {
			bottomRightRelation = bRight.Children[0].Relation
		} else {
			bottomRightRelation = bRight.Relation
		}
	}
	if !strings.EqualFold(bottomRightRelation, "tiny_table") {
		t.Errorf("expected inner/first join to be with tiny_table, got %q. Plan:\n%s", bottomRightRelation, fullPlanText)
	}
}

func TestCBOJoinOrderOptimization_IndexedJoinPreferred(t *testing.T) {
	p := planner.NewPlanner()

	// Query: A JOIN B ON A.b_id = B.id JOIN C ON A.c_id = C.id
	// B has no index (requires Hash Join scan)
	// C has a unique primary index on id (allows fast Index Nested Loop Join)
	// Both B and C have 500 rows. CBO should prefer joining C first with INLJ to reduce intermediate rows.
	sql := `SELECT a.id, b.val, c.data FROM table_a a JOIN table_b b ON a.b_id = b.id JOIN table_c c ON a.c_id = c.id`
	q, err := p.ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	schemaA := &storage.TableSchema{
		Name: "table_a",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "b_id", Type: storage.TypeInt},
			{Name: "c_id", Type: storage.TypeInt},
		},
	}
	schemaB := &storage.TableSchema{
		Name: "table_b",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	schemaC := &storage.TableSchema{
		Name: "table_c",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "data", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:    "idx_c_id",
				Columns: []string{"id"},
				Unique:  true,
			},
		},
	}

	statsA := planner.NewTableStats("table_a", 100)
	statsB := planner.NewTableStats("table_b", 500)
	statsC := planner.NewTableStats("table_c", 500)

	rSchemas := map[string]*storage.TableSchema{
		"table_b": schemaB,
		"b":       schemaB,
		"table_c": schemaC,
		"c":       schemaC,
	}
	rStats := map[string]*planner.TableStats{
		"table_b": statsB,
		"b":       statsB,
		"table_c": statsC,
		"c":       statsC,
	}

	plan := p.BuildMultiJoinPlanWithStats(q, schemaA, statsA, rSchemas, rStats)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	lines := planner.FormatPlanText(plan, false)
	fullPlanText := strings.Join(lines, "\n")

	// Plan should have Nested Loop (with table_c) as lower join, and Hash Join (with table_b) on top
	if !strings.Contains(fullPlanText, "Nested Loop") {
		t.Errorf("expected Nested Loop Join in plan, got:\n%s", fullPlanText)
	}
	if !strings.Contains(fullPlanText, "Hash Join") {
		t.Errorf("expected Hash Join in plan, got:\n%s", fullPlanText)
	}
}

func TestCBOOptimizerHints_ForceOrder(t *testing.T) {
	p := planner.NewPlanner()

	// With OPTION (FORCE ORDER), the query order (large_table first, tiny_table second)
	// must be strictly preserved, overriding CBO's default behavior of joining tiny_table first.
	sql := `SELECT u.name, l.data, s.info FROM users u JOIN large_table l ON u.id = l.user_id JOIN tiny_table s ON u.id = s.user_id OPTION (FORCE ORDER)`
	q, err := p.ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	userSchema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	largeSchema := &storage.TableSchema{
		Name: "large_table",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
			{Name: "data", Type: storage.TypeVarChar},
		},
	}
	tinySchema := &storage.TableSchema{
		Name: "tiny_table",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
			{Name: "info", Type: storage.TypeVarChar},
		},
	}

	userStats := planner.NewTableStats("users", 1000)
	largeStats := planner.NewTableStats("large_table", 100000)
	tinyStats := planner.NewTableStats("tiny_table", 10)

	rSchemas := map[string]*storage.TableSchema{
		"large_table": largeSchema,
		"l":           largeSchema,
		"tiny_table":  tinySchema,
		"s":           tinySchema,
	}
	rStats := map[string]*planner.TableStats{
		"large_table": largeStats,
		"l":           largeStats,
		"tiny_table":  tinyStats,
		"s":           tinyStats,
	}

	plan := p.BuildMultiJoinPlanWithStats(q, userSchema, userStats, rSchemas, rStats)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	lines := planner.FormatPlanText(plan, false)
	fullPlanText := strings.Join(lines, "\n")

	rootJoin := plan
	if plan.Type == planner.NodeProjection && len(plan.Children) > 0 {
		rootJoin = plan.Children[0]
	}

	if rootJoin.Type != planner.NodeHashJoin {
		t.Fatalf("expected root to be HashJoin, got %s. Plan:\n%s", rootJoin.Type, fullPlanText)
	}

	// Under FORCE ORDER:
	// Top join must build from tiny_table (joined second in SQL text)
	// Lower join must build from large_table (joined first in SQL text)
	rightChild := rootJoin.Children[1]
	topRightRelation := ""
	if rightChild.Type == planner.NodeHash && len(rightChild.Children) > 0 {
		topRightRelation = rightChild.Children[0].Relation
	} else {
		topRightRelation = rightChild.Relation
	}
	if !strings.EqualFold(topRightRelation, "tiny_table") {
		t.Errorf("expected top join under FORCE ORDER to be tiny_table, got %q. Plan:\n%s", topRightRelation, fullPlanText)
	}

	leftChild := rootJoin.Children[0]
	bottomRightRelation := ""
	if len(leftChild.Children) > 1 {
		bRight := leftChild.Children[1]
		if bRight.Type == planner.NodeHash && len(bRight.Children) > 0 {
			bottomRightRelation = bRight.Children[0].Relation
		} else {
			bottomRightRelation = bRight.Relation
		}
	}
	if !strings.EqualFold(bottomRightRelation, "large_table") {
		t.Errorf("expected lower join under FORCE ORDER to be large_table, got %q. Plan:\n%s", bottomRightRelation, fullPlanText)
	}
}

func TestCBOOptimizerHints_JoinAlgorithms(t *testing.T) {
	p := planner.NewPlanner()

	// Base query where table_c has an index, so CBO normally chooses Nested Loop Join for table_c
	sql := `SELECT a.id, b.val, c.data FROM table_a a JOIN table_b b ON a.b_id = b.id JOIN table_c c ON a.c_id = c.id OPTION (HASH JOIN)`
	q, err := p.ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	schemaA := &storage.TableSchema{
		Name: "table_a",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "b_id", Type: storage.TypeInt},
			{Name: "c_id", Type: storage.TypeInt},
		},
	}
	schemaB := &storage.TableSchema{
		Name: "table_b",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	schemaC := &storage.TableSchema{
		Name: "table_c",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "data", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:    "idx_c_id",
				Columns: []string{"id"},
				Unique:  true,
			},
		},
	}

	statsA := planner.NewTableStats("table_a", 100)
	statsB := planner.NewTableStats("table_b", 500)
	statsC := planner.NewTableStats("table_c", 500)

	rSchemas := map[string]*storage.TableSchema{
		"table_b": schemaB,
		"b":       schemaB,
		"table_c": schemaC,
		"c":       schemaC,
	}
	rStats := map[string]*planner.TableStats{
		"table_b": statsB,
		"b":       statsB,
		"table_c": statsC,
		"c":       statsC,
	}

	plan := p.BuildMultiJoinPlanWithStats(q, schemaA, statsA, rSchemas, rStats)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	lines := planner.FormatPlanText(plan, false)
	fullPlanText := strings.Join(lines, "\n")

	// Because of OPTION (HASH JOIN), no Nested Loop join should appear in the plan
	if strings.Contains(fullPlanText, "Nested Loop") {
		t.Errorf("expected no Nested Loop Join when OPTION (HASH JOIN) is supplied, got:\n%s", fullPlanText)
	}
}

func TestCBOOptimizerHints_TableHints(t *testing.T) {
	p := planner.NewPlanner()

	schemaC := &storage.TableSchema{
		Name: "table_c",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "data", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:    "idx_c_id",
				Columns: []string{"id"},
				Unique:  true,
			},
		},
	}
	statsC := planner.NewTableStats("table_c", 10000)

	// 1. Force Table Scan via WITH (INDEX(0))
	sqlSeq := `SELECT * FROM table_c WITH (INDEX(0)) WHERE id = 10`
	qSeq, err := p.ParseSelect(sqlSeq)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	planSeq := p.BuildMultiJoinPlanWithStats(qSeq, schemaC, statsC, nil, nil)
	linesSeq := planner.FormatPlanText(planSeq, false)
	textSeq := strings.Join(linesSeq, "\n")
	if !strings.Contains(textSeq, "Seq Scan") {
		t.Errorf("expected Seq Scan with WITH (INDEX(0)), got:\n%s", textSeq)
	}
	if strings.Contains(textSeq, "Index Scan") {
		t.Errorf("did not expect Index Scan with WITH (INDEX(0)), got:\n%s", textSeq)
	}

	// 2. Force Index Scan via WITH (INDEX(idx_c_id))
	sqlIdx := `SELECT * FROM table_c WITH (INDEX(idx_c_id)) WHERE id = 10`
	qIdx, err := p.ParseSelect(sqlIdx)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	planIdx := p.BuildMultiJoinPlanWithStats(qIdx, schemaC, statsC, nil, nil)
	linesIdx := planner.FormatPlanText(planIdx, false)
	textIdx := strings.Join(linesIdx, "\n")
	if !strings.Contains(textIdx, "Index Scan") {
		t.Errorf("expected Index Scan with WITH (INDEX(idx_c_id)), got:\n%s", textIdx)
	}
}

func TestCBOOptimizerHints_MergeJoin(t *testing.T) {
	p := planner.NewPlanner()

	schemaA := &storage.TableSchema{
		Name: "table_a",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "b_id", Type: storage.TypeInt},
		},
	}
	schemaB := &storage.TableSchema{
		Name: "table_b",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}

	statsA := planner.NewTableStats("table_a", 100)
	statsB := planner.NewTableStats("table_b", 100)

	rSchemas := map[string]*storage.TableSchema{
		"table_b": schemaB,
		"b":       schemaB,
	}
	rStats := map[string]*planner.TableStats{
		"table_b": statsB,
		"b":       statsB,
	}

	// 1. Query Hint: OPTION (MERGE JOIN)
	sqlOpt := `SELECT a.id, b.val FROM table_a a JOIN table_b b ON a.b_id = b.id OPTION (MERGE JOIN)`
	qOpt, err := p.ParseSelect(sqlOpt)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	planOpt := p.BuildMultiJoinPlanWithStats(qOpt, schemaA, statsA, rSchemas, rStats)
	linesOpt := planner.FormatPlanText(planOpt, false)
	textOpt := strings.Join(linesOpt, "\n")
	if !strings.Contains(textOpt, "Merge Join") {
		t.Errorf("expected Merge Join with OPTION (MERGE JOIN), got:\n%s", textOpt)
	}

	// 2. Inline Join Hint: INNER MERGE JOIN
	sqlInline := `SELECT a.id, b.val FROM table_a a INNER MERGE JOIN table_b b ON a.b_id = b.id`
	qInline, err := p.ParseSelect(sqlInline)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	planInline := p.BuildMultiJoinPlanWithStats(qInline, schemaA, statsA, rSchemas, rStats)
	linesInline := planner.FormatPlanText(planInline, false)
	textInline := strings.Join(linesInline, "\n")
	if !strings.Contains(textInline, "Merge Join") {
		t.Errorf("expected Merge Join with INNER MERGE JOIN, got:\n%s", textInline)
	}
}

func TestCBOMergeJoin_CostSelection(t *testing.T) {
	p := planner.NewPlanner()

	// When outer relation and inner relation are both pre-sorted on their join keys,
	// Sort-Merge Join eliminates hash table allocation and sort overhead, making it competitive.
	schemaA := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_users_id", Columns: []string{"id"}, Unique: true},
		},
	}
	schemaB := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
			{Name: "amount", Type: storage.TypeFloat},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_orders_user_id", Columns: []string{"user_id"}, Unique: false},
		},
	}

	statsA := planner.NewTableStats("users", 1000)
	statsB := planner.NewTableStats("orders", 1000)

	rSchemas := map[string]*storage.TableSchema{
		"orders": schemaB,
		"o":      schemaB,
	}
	rStats := map[string]*planner.TableStats{
		"orders": statsB,
		"o":      statsB,
	}

	// Forcing MERGE JOIN or observing competitive plan
	sql := `SELECT u.name, o.amount FROM users u JOIN orders o ON u.id = o.user_id OPTION (MERGE JOIN)`
	q, err := p.ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	plan := p.BuildMultiJoinPlanWithStats(q, schemaA, statsA, rSchemas, rStats)
	lines := planner.FormatPlanText(plan, false)
	planText := strings.Join(lines, "\n")
	if !strings.Contains(planText, "Merge Join") {
		t.Errorf("expected Merge Join in plan text, got:\n%s", planText)
	}
}
