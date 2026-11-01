package planner_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/planner"
	"lionsql/pkg/storage"
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

func TestCBOJoinOrderOptimization_SelingerDP_LargeChain(t *testing.T) {
	p := planner.NewPlanner()

	// 6 tables in a chain:
	// users (1,000)
	// orders (50,000)
	// line_items (200,000)
	// products (5,000)
	// suppliers (200)
	// cities (50)
	sql := `SELECT u.name, o.id, l.item, p.price, s.name, c.name FROM users u JOIN orders o ON u.id = o.user_id JOIN line_items l ON o.id = l.order_id JOIN products p ON l.product_id = p.id JOIN suppliers s ON p.supplier_id = s.id JOIN cities c ON s.city_id = c.id`
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
	orderSchema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
		},
	}
	lineSchema := &storage.TableSchema{
		Name: "line_items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "order_id", Type: storage.TypeInt},
			{Name: "product_id", Type: storage.TypeInt},
			{Name: "item", Type: storage.TypeVarChar},
		},
	}
	productSchema := &storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "supplier_id", Type: storage.TypeInt},
			{Name: "price", Type: storage.TypeFloat},
		},
	}
	supplierSchema := &storage.TableSchema{
		Name: "suppliers",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "city_id", Type: storage.TypeInt},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	citySchema := &storage.TableSchema{
		Name: "cities",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}

	userStats := planner.NewTableStats("users", 1000)
	orderStats := planner.NewTableStats("orders", 50000)
	lineStats := planner.NewTableStats("line_items", 200000)
	productStats := planner.NewTableStats("products", 5000)
	supplierStats := planner.NewTableStats("suppliers", 200)
	cityStats := planner.NewTableStats("cities", 50)

	rSchemas := map[string]*storage.TableSchema{
		"orders":     orderSchema,
		"o":          orderSchema,
		"line_items": lineSchema,
		"l":          lineSchema,
		"products":   productSchema,
		"p":          productSchema,
		"suppliers":  supplierSchema,
		"s":          supplierSchema,
		"cities":     citySchema,
		"c":          citySchema,
	}
	rStats := map[string]*planner.TableStats{
		"orders":     orderStats,
		"o":          orderStats,
		"line_items": lineStats,
		"l":          lineStats,
		"products":   productStats,
		"p":          productStats,
		"suppliers":  supplierStats,
		"s":          supplierStats,
		"cities":     cityStats,
		"c":          cityStats,
	}

	start := time.Now()
	plan := p.BuildMultiJoinPlanWithStats(q, userSchema, userStats, rSchemas, rStats)
	dur := time.Since(start)

	if plan == nil {
		t.Fatal("expected non-nil plan for 6-table join")
	}

	// Performance verification: Selinger DP for 5 joins must complete well within 50ms (typically < 1ms)
	if dur > 50*time.Millisecond {
		t.Errorf("expected Selinger DP join optimization to finish under 50ms, took %v", dur)
	}

	lines := planner.FormatPlanText(plan, false)
	planText := strings.Join(lines, "\n")
	if !strings.Contains(planText, "Join") {
		t.Fatalf("expected Join nodes in plan, got:\n%s", planText)
	}
}

func TestCBOJoinOrderOptimization_SelingerDP_EightTables(t *testing.T) {
	p := planner.NewPlanner()

	// 8-table chain join (N=7 joins). Exceeds MaxJoinDPThreshold (6),
	// automatically falling back to Greedy Join Ordering to bound compilation time to < 1ms.
	sql := `SELECT t1.id, t8.val FROM t1
		JOIN t2 ON t1.id = t2.t1_id
		JOIN t3 ON t2.id = t3.t2_id
		JOIN t4 ON t3.id = t4.t3_id
		JOIN t5 ON t4.id = t5.t4_id
		JOIN t6 ON t5.id = t6.t5_id
		JOIN t7 ON t6.id = t7.t6_id
		JOIN t8 ON t7.id = t8.t7_id`

	q, err := p.ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	schemas := make(map[string]*storage.TableSchema)
	stats := make(map[string]*planner.TableStats)

	for i := 1; i <= 8; i++ {
		name := fmt.Sprintf("t%d", i)
		cols := []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		}
		if i > 1 {
			cols = append(cols, storage.Column{Name: fmt.Sprintf("t%d_id", i-1), Type: storage.TypeInt})
		}
		sch := &storage.TableSchema{Name: name, Columns: cols}
		st := planner.NewTableStats(name, int64(i*1000))
		schemas[name] = sch
		stats[name] = st
	}

	start := time.Now()
	plan := p.BuildMultiJoinPlanWithStats(q, schemas["t1"], stats["t1"], schemas, stats)
	dur := time.Since(start)

	if plan == nil {
		t.Fatal("expected non-nil plan for 8-table join")
	}

	if dur > 100*time.Millisecond {
		t.Errorf("expected 8-table Selinger DP to complete in < 100ms, took %v", dur)
	}

	lines := planner.FormatPlanText(plan, false)
	planText := strings.Join(lines, "\n")
	if !strings.Contains(planText, "Join") {
		t.Fatalf("expected Join nodes in plan, got:\n%s", planText)
	}
}

func TestDynamicRootSelection_FilteredTablePromotedToScanNode(t *testing.T) {
	p := planner.NewPlanner()

	// Query: large_table l, users u WHERE l.user_id = u.id AND u.id = 42
	// In the SQL text, large_table (100,000 rows) is FROM table 0.
	// But users u has a single-table equality filter u.id = 42 (1 row).
	// Dynamic Root Selection should promote users u to base scan root.
	sql := `SELECT l.data, u.name FROM large_table l, users u WHERE l.user_id = u.id AND u.id = 42`
	q, err := p.ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	largeSchema := &storage.TableSchema{
		Name: "large_table",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
			{Name: "data", Type: storage.TypeVarChar},
		},
	}
	userSchema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}

	largeStats := planner.NewTableStats("large_table", 100000)
	userStats := planner.NewTableStats("users", 1000)

	rSchemas := map[string]*storage.TableSchema{
		"users": userSchema,
		"u":     userSchema,
	}
	rStats := map[string]*planner.TableStats{
		"users": userStats,
		"u":     userStats,
	}

	plan := p.BuildMultiJoinPlanWithStats(q, largeSchema, largeStats, rSchemas, rStats)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	planText := strings.Join(planner.FormatPlanText(plan, false), "\n")
	// The innermost relation (root scan) should be users or u, not large_table
	if !strings.Contains(planText, "users") {
		t.Fatalf("expected users in plan, got:\n%s", planText)
	}
	if q.TableName != "users" {
		t.Errorf("expected q.TableName to be promoted to users, got %q", q.TableName)
	}
}

func TestContradictionFastFail_ZeroCardinalityPruning(t *testing.T) {
	p := planner.NewPlanner()

	// Query: large_table l, users u WHERE l.user_id = u.id AND 1 = 0
	sql := `SELECT l.data, u.name FROM large_table l, users u WHERE l.user_id = u.id AND 1 = 0`
	q, err := p.ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}

	largeSchema := &storage.TableSchema{
		Name: "large_table",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
		},
	}
	userSchema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}

	rSchemas := map[string]*storage.TableSchema{"users": userSchema}
	plan := p.BuildMultiJoinPlanWithStats(q, largeSchema, nil, rSchemas, nil)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	planText := strings.Join(planner.FormatPlanText(plan, false), "\n")
	if !strings.Contains(planText, "Empty Result") {
		t.Fatalf("expected Empty Result in plan for contradiction 1=0, got:\n%s", planText)
	}
}

func TestCBOJoinOrderOptimization_MaxJoinDPThreshold_Boundary(t *testing.T) {
	p := planner.NewPlanner()

	// 1. Exactly 6 joins (7 tables total: base t0 + t1..t6): Fits within MaxJoinDPThreshold (6)
	sql6 := `SELECT t0.id, t6.val FROM t0
		JOIN t1 ON t0.id = t1.t0_id
		JOIN t2 ON t1.id = t2.t1_id
		JOIN t3 ON t2.id = t3.t2_id
		JOIN t4 ON t3.id = t4.t3_id
		JOIN t5 ON t4.id = t5.t4_id
		JOIN t6 ON t5.id = t6.t5_id`

	q6, err := p.ParseSelect(sql6)
	if err != nil {
		t.Fatalf("ParseSelect failed for 7-table query: %v", err)
	}

	schemas := make(map[string]*storage.TableSchema)
	stats := make(map[string]*planner.TableStats)
	for i := 0; i <= 7; i++ {
		name := fmt.Sprintf("t%d", i)
		cols := []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		}
		if i > 0 {
			cols = append(cols, storage.Column{Name: fmt.Sprintf("t%d_id", i-1), Type: storage.TypeInt})
		}
		schemas[name] = &storage.TableSchema{Name: name, Columns: cols}
		stats[name] = planner.NewTableStats(name, int64((i+1)*1000))
	}

	start6 := time.Now()
	plan6 := p.BuildMultiJoinPlanWithStats(q6, schemas["t0"], stats["t0"], schemas, stats)
	dur6 := time.Since(start6)
	if plan6 == nil {
		t.Fatal("expected non-nil plan for 7-table join (6 joins, Selinger DP)")
	}
	if dur6 > 50*time.Millisecond {
		t.Errorf("expected 6-join DP to finish in < 50ms, took %v", dur6)
	}

	// 2. Exactly 7 joins (8 tables total: base t0 + t1..t7): Exceeds MaxJoinDPThreshold (6) -> Greedy Fallback
	sql7 := `SELECT t0.id, t7.val FROM t0
		JOIN t1 ON t0.id = t1.t0_id
		JOIN t2 ON t1.id = t2.t1_id
		JOIN t3 ON t2.id = t3.t2_id
		JOIN t4 ON t3.id = t4.t3_id
		JOIN t5 ON t4.id = t5.t4_id
		JOIN t6 ON t5.id = t6.t5_id
		JOIN t7 ON t6.id = t7.t6_id`

	q7, err := p.ParseSelect(sql7)
	if err != nil {
		t.Fatalf("ParseSelect failed for 8-table query: %v", err)
	}

	start7 := time.Now()
	plan7 := p.BuildMultiJoinPlanWithStats(q7, schemas["t0"], stats["t0"], schemas, stats)
	dur7 := time.Since(start7)
	if plan7 == nil {
		t.Fatal("expected non-nil plan for 8-table join (7 joins, Greedy fallback)")
	}
	if dur7 > 50*time.Millisecond {
		t.Errorf("expected 7-join Greedy fallback to finish in < 50ms, took %v", dur7)
	}
}
