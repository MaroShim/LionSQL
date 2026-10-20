package execution_test

import (
	"context"
	"fmt"
	"io"
	"testing"

	"lionsql/pkg/execution"
	"lionsql/pkg/planner"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// mockIterator provides a synthetic stream of tuples for hermetic operator testing.
type mockIterator struct {
	tuples []execution.Tuple
	schema []string
	idx    int
	calls  int
}

func newMockIterator(schema []string, rows ...map[string]any) *mockIterator {
	tuples := make([]execution.Tuple, len(rows))
	for i, r := range rows {
		tuples[i] = execution.NewTupleFromMap(r, schema)
	}
	return &mockIterator{schema: schema, tuples: tuples}
}

func (m *mockIterator) Open(ctx context.Context) error {
	m.idx = 0
	return nil
}

func (m *mockIterator) Next(ctx context.Context) (execution.Tuple, error) {
	m.calls++
	if m.idx >= len(m.tuples) {
		return execution.Tuple{}, io.EOF
	}
	t := m.tuples[m.idx]
	m.idx++
	return t, nil
}

func (m *mockIterator) Close() error {
	return nil
}

func (m *mockIterator) Schema() []string {
	return m.schema
}

func (m *mockIterator) ColIndexMap() *execution.ColumnIndexMap {
	if len(m.tuples) > 0 && m.tuples[0].Index != nil {
		return m.tuples[0].Index
	}
	return execution.NewColumnIndexMapFromCols(m.schema)
}

func TestLimitIterator_ShortCircuit(t *testing.T) {
	ctx := context.Background()
	mock := newMockIterator([]string{"id", "val"},
		map[string]any{"id": int64(1), "val": "a"},
		map[string]any{"id": int64(2), "val": "b"},
		map[string]any{"id": int64(3), "val": "c"},
		map[string]any{"id": int64(4), "val": "d"},
		map[string]any{"id": int64(5), "val": "e"},
	)

	limitIter := execution.NewLimitIterator(mock, 2, 0)
	if err := limitIter.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer limitIter.Close()

	var rows []execution.Tuple
	for {
		tuple, err := limitIter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next error: %v", err)
		}
		rows = append(rows, tuple)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Get("id") != int64(1) || rows[1].Get("id") != int64(2) {
		t.Fatalf("unexpected rows: %v", rows)
	}

	// Short-circuit guarantee: mock should have been called at most 2 times
	if mock.calls > 2 {
		t.Fatalf("expected short-circuit <= 2 calls, got %d", mock.calls)
	}
}

func TestFilterIterator(t *testing.T) {
	ctx := context.Background()
	mock := newMockIterator([]string{"id", "age", "city"},
		map[string]any{"id": int64(1), "age": int64(25), "city": "Seoul"},
		map[string]any{"id": int64(2), "age": int64(17), "city": "Busan"},
		map[string]any{"id": int64(3), "age": int64(35), "city": "Seoul"},
		map[string]any{"id": int64(4), "age": int64(40), "city": "Incheon"},
	)

	filter := execution.NewFilterIterator(mock, "age >= 25 AND city = 'Seoul'")
	if err := filter.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer filter.Close()

	var rows []execution.Tuple
	for {
		tuple, err := filter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next error: %v", err)
		}
		rows = append(rows, tuple)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Get("id") != int64(1) || rows[1].Get("id") != int64(3) {
		t.Fatalf("unexpected filtered rows: %v", rows)
	}
}

func TestSortIterator(t *testing.T) {
	ctx := context.Background()
	mock := newMockIterator([]string{"name", "score"},
		map[string]any{"name": "Charlie", "score": int64(80)},
		map[string]any{"name": "Alice", "score": int64(95)},
		map[string]any{"name": "Bob", "score": int64(80)},
	)

	sortIter := execution.NewSortIterator(mock, "score DESC, name ASC")
	if err := sortIter.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sortIter.Close()

	var names []string
	for {
		tuple, err := sortIter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next error: %v", err)
		}
		names = append(names, fmt.Sprintf("%v", tuple.Get("name")))
	}

	expected := []string{"Alice", "Bob", "Charlie"}
	for i, exp := range expected {
		if names[i] != exp {
			t.Fatalf("expected at %d %s, got %s", i, exp, names[i])
		}
	}
}

func TestProjectionIterator(t *testing.T) {
	ctx := context.Background()
	mock := newMockIterator([]string{"id", "first_name", "last_name"},
		map[string]any{"id": int64(1), "first_name": "John", "last_name": "Doe"},
	)

	proj := execution.NewProjectionIterator(mock, []string{"id", "first_name AS name"})
	if err := proj.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer proj.Close()

	tuple, err := proj.Next(ctx)
	if err != nil {
		t.Fatalf("Next error: %v", err)
	}

	if tuple.Get("id") != int64(1) || tuple.Get("name") != "John" {
		t.Fatalf("unexpected projection: %v", tuple)
	}
	if _, exists := tuple.GetOK("last_name"); exists {
		t.Fatalf("last_name should not be in projection")
	}
}

func TestHashJoinIterator(t *testing.T) {
	ctx := context.Background()
	users := newMockIterator([]string{"u.id", "u.name"},
		map[string]any{"u.id": int64(1), "u.name": "Alice"},
		map[string]any{"u.id": int64(2), "u.name": "Bob"},
		map[string]any{"u.id": int64(3), "u.name": "Carol"},
	)
	orders := newMockIterator([]string{"o.id", "o.user_id", "o.item"},
		map[string]any{"o.id": int64(101), "o.user_id": int64(1), "o.item": "Laptop"},
		map[string]any{"o.id": int64(102), "o.user_id": int64(1), "o.item": "Mouse"},
		map[string]any{"o.id": int64(103), "o.user_id": int64(2), "o.item": "Monitor"},
	)

	// INNER JOIN users ON u.id = o.user_id
	join := execution.NewHashJoinIterator(users, orders, "INNER", "u.id", "o.user_id")
	if err := join.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer join.Close()

	var rows []execution.Tuple
	for {
		tuple, err := join.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next error: %v", err)
		}
		rows = append(rows, tuple)
	}

	// 2 for Alice + 1 for Bob = 3 rows
	if len(rows) != 3 {
		t.Fatalf("expected 3 join rows, got %d", len(rows))
	}
}

func TestAggregateIterator(t *testing.T) {
	ctx := context.Background()
	mock := newMockIterator([]string{"dept", "salary"},
		map[string]any{"dept": "Engineering", "salary": int64(100)},
		map[string]any{"dept": "Engineering", "salary": int64(120)},
		map[string]any{"dept": "Sales", "salary": int64(80)},
		map[string]any{"dept": "Sales", "salary": int64(90)},
		map[string]any{"dept": "HR", "salary": int64(70)},
	)

	agg := execution.NewAggregateIterator(mock, []string{"dept"}, []string{"dept", "COUNT(*) AS cnt", "AVG(salary) AS avg_sal"}, "cnt >= 2")
	if err := agg.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer agg.Close()

	var rows []execution.Tuple
	for {
		tuple, err := agg.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next error: %v", err)
		}
		rows = append(rows, tuple)
	}

	// Engineering (2) and Sales (2) pass cnt >= 2; HR (1) is filtered out
	if len(rows) != 2 {
		t.Fatalf("expected 2 aggregated rows, got %d", len(rows))
	}
}

func TestEndToEnd_VolcanoWithMemEngine(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	defer eng.Close()

	// Create table
	schema := &storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "price", Type: storage.TypeFloat},
			{Name: "stock", Type: storage.TypeInt},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_stock", TableName: "products", Columns: []string{"stock"}},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable error: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()
	tx, _ := sess.BeginTx(storage.TxOptions{})

	// Insert test data
	for i := 1; i <= 20; i++ {
		_ = tx.Insert("products", storage.Record{
			"id":    storage.IntValue(int64(i)),
			"name":  storage.StringValue(fmt.Sprintf("Item-%02d", i)),
			"price": storage.FloatValue(float64(i * 10)),
			"stock": storage.IntValue(int64(i % 5)),
		})
	}
	_ = tx.Commit()

	// Plan query: SELECT name, price FROM products WHERE stock = 2 ORDER BY price DESC LIMIT 2
	p := planner.NewPlanner()
	qInfo, err := p.ParseSelect("SELECT name, price FROM products WHERE stock = 2 ORDER BY price DESC LIMIT 2")
	if err != nil {
		t.Fatalf("ParseSelect error: %v", err)
	}

	plan := p.BuildPlan(qInfo, schema, 20)
	if plan == nil {
		t.Fatalf("BuildPlan returned nil")
	}

	readTx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()

	exec, err := execution.BuildExecutor(plan, readTx, eng.GetTable)
	if err != nil {
		t.Fatalf("BuildExecutor error: %v", err)
	}

	ctx := context.Background()
	if err := exec.Open(ctx); err != nil {
		t.Fatalf("Executor Open failed: %v", err)
	}
	defer exec.Close()

	var result []execution.Tuple
	for {
		tuple, err := exec.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Executor Next error: %v", err)
		}
		result = append(result, tuple)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 rows from limit, got %d", len(result))
	}
	if result[0].Get("name") == nil || result[0].Get("price") == nil {
		t.Fatalf("expected projected name and price, got %v", result[0])
	}
}

func TestExecutor_IndexOnlyScan(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar},
			{Name: "bio", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:    "idx_users_email",
				Columns: []string{"email"},
				Unique:  true,
			},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable error: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("users", storage.Record{
		"id":    storage.IntValue(1),
		"email": storage.StringValue("alice@example.com"),
		"bio":   storage.StringValue("Alice bio notes"),
	})
	_ = tx.Insert("users", storage.Record{
		"id":    storage.IntValue(2),
		"email": storage.StringValue("bob@example.com"),
		"bio":   storage.StringValue("Bob bio notes"),
	})
	_ = tx.Commit()

	// Build Index-Only Scan plan for "SELECT id, email FROM users WHERE email = 'bob@example.com'"
	p := planner.NewPlanner()
	q, err := p.ParseSelect("SELECT id, email FROM users WHERE email = 'bob@example.com'")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	plan := p.BuildPlan(q, schema, 2)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	readTx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()

	exec, err := execution.BuildExecutor(plan, readTx, eng.GetTable)
	if err != nil {
		t.Fatalf("BuildExecutor error: %v", err)
	}

	ctx := context.Background()
	if err := exec.Open(ctx); err != nil {
		t.Fatalf("Executor Open failed: %v", err)
	}
	defer exec.Close()

	var result []execution.Tuple
	for {
		tuple, err := exec.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Executor Next error: %v", err)
		}
		result = append(result, tuple)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 row from index-only scan, got %d", len(result))
	}
	if result[0].Get("email") != "bob@example.com" {
		t.Fatalf("expected email bob@example.com, got %v", result[0].Get("email"))
	}
	if result[0].Get("id") != int64(2) {
		t.Fatalf("expected id 2, got %v", result[0].Get("id"))
	}
}

func TestExecutor_CoveringIndexWithInclude(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "sku", Type: storage.TypeVarChar},
			{Name: "price", Type: storage.TypeFloat},
			{Name: "description", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:        "idx_products_sku_cov",
				TableName:   "products",
				Columns:     []string{"sku"},
				IncludeCols: []string{"price"},
				Unique:      true,
			},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable error: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("products", storage.Record{
		"id":          storage.IntValue(1),
		"sku":         storage.StringValue("PROD-001"),
		"price":       storage.FloatValue(9.99),
		"description": storage.StringValue("First product description"),
	})
	_ = tx.Insert("products", storage.Record{
		"id":          storage.IntValue(2),
		"sku":         storage.StringValue("PROD-002"),
		"price":       storage.FloatValue(19.99),
		"description": storage.StringValue("Second product description"),
	})
	_ = tx.Commit()

	// Build Index-Only Scan plan for "SELECT id, sku, price FROM products WHERE sku = 'PROD-002'"
	p := planner.NewPlanner()
	q, err := p.ParseSelect("SELECT id, sku, price FROM products WHERE sku = 'PROD-002'")
	if err != nil {
		t.Fatalf("ParseSelect failed: %v", err)
	}
	plan := p.BuildPlan(q, schema, 2)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	readTx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()

	exec, err := execution.BuildExecutor(plan, readTx, eng.GetTable)
	if err != nil {
		t.Fatalf("BuildExecutor error: %v", err)
	}

	ctx := context.Background()
	if err := exec.Open(ctx); err != nil {
		t.Fatalf("Executor Open failed: %v", err)
	}
	defer exec.Close()

	var result []execution.Tuple
	for {
		tuple, err := exec.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Executor Next error: %v", err)
		}
		result = append(result, tuple)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 row from covering index-only scan, got %d", len(result))
	}
	if result[0].Get("sku") != "PROD-002" {
		t.Fatalf("expected sku PROD-002, got %v", result[0].Get("sku"))
	}
	if result[0].Get("price") != float64(19.99) {
		t.Fatalf("expected price 19.99, got %v", result[0].Get("price"))
	}
	if result[0].Get("id") != int64(2) {
		t.Fatalf("expected id 2, got %v", result[0].Get("id"))
	}
}

func TestExecutor_MultiTableChainedJoins(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	usersSchema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	ordersSchema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
			{Name: "amount", Type: storage.TypeFloat},
		},
	}
	itemsSchema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "order_id", Type: storage.TypeInt},
			{Name: "product", Type: storage.TypeVarChar},
		},
	}

	if err := eng.CreateTable(usersSchema); err != nil {
		t.Fatalf("CreateTable users failed: %v", err)
	}
	if err := eng.CreateTable(ordersSchema); err != nil {
		t.Fatalf("CreateTable orders failed: %v", err)
	}
	if err := eng.CreateTable(itemsSchema); err != nil {
		t.Fatalf("CreateTable items failed: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("users", storage.Record{"id": storage.IntValue(1), "name": storage.StringValue("Alice")})
	_ = tx.Insert("users", storage.Record{"id": storage.IntValue(2), "name": storage.StringValue("Bob")})

	_ = tx.Insert("orders", storage.Record{"id": storage.IntValue(101), "user_id": storage.IntValue(1), "amount": storage.FloatValue(50.0)})
	_ = tx.Insert("orders", storage.Record{"id": storage.IntValue(102), "user_id": storage.IntValue(1), "amount": storage.FloatValue(150.0)})
	_ = tx.Insert("orders", storage.Record{"id": storage.IntValue(103), "user_id": storage.IntValue(2), "amount": storage.FloatValue(20.0)})

	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(1001), "order_id": storage.IntValue(101), "product": storage.StringValue("Book")})
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(1002), "order_id": storage.IntValue(101), "product": storage.StringValue("Pen")})
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(1003), "order_id": storage.IntValue(102), "product": storage.StringValue("Laptop")})
	_ = tx.Commit()

	// 1. Test INNER JOIN chain
	p := planner.NewPlanner()
	qInner, err := p.ParseSelect("SELECT users.name, orders.amount, items.product FROM users JOIN orders ON users.id = orders.user_id JOIN items ON orders.id = items.order_id")
	if err != nil {
		t.Fatalf("ParseSelect INNER failed: %v", err)
	}

	rSchemas := map[string]*storage.TableSchema{
		"orders": ordersSchema,
		"items":  itemsSchema,
	}
	planInner := p.BuildMultiJoinPlanWithStats(qInner, usersSchema, nil, rSchemas, nil)
	if planInner == nil {
		t.Fatal("expected non-nil planInner")
	}

	readTx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()

	execInner, err := execution.BuildExecutor(planInner, readTx, eng.GetTable)
	if err != nil {
		t.Fatalf("BuildExecutor INNER error: %v", err)
	}

	ctx := context.Background()
	if err := execInner.Open(ctx); err != nil {
		t.Fatalf("execInner Open failed: %v", err)
	}
	defer execInner.Close()

	var innerRows []execution.Tuple
	for {
		tup, err := execInner.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("execInner Next error: %v", err)
		}
		innerRows = append(innerRows, tup)
	}

	// Should produce 3 rows (Book, Pen, Laptop)
	if len(innerRows) != 3 {
		t.Fatalf("expected 3 inner join rows, got %d", len(innerRows))
	}

	// 2. Test mixed INNER + LEFT JOIN chain: Bob's order 103 should appear with NULL items.product
	qLeft, err := p.ParseSelect("SELECT users.name, orders.amount, items.product FROM users JOIN orders ON users.id = orders.user_id LEFT JOIN items ON orders.id = items.order_id")
	if err != nil {
		t.Fatalf("ParseSelect LEFT failed: %v", err)
	}

	planLeft := p.BuildMultiJoinPlanWithStats(qLeft, usersSchema, nil, rSchemas, nil)
	if planLeft == nil {
		t.Fatal("expected non-nil planLeft")
	}

	readTx2, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx2.Rollback()

	execLeft, err := execution.BuildExecutor(planLeft, readTx2, eng.GetTable)
	if err != nil {
		t.Fatalf("BuildExecutor LEFT error: %v", err)
	}

	if err := execLeft.Open(ctx); err != nil {
		t.Fatalf("execLeft Open failed: %v", err)
	}
	defer execLeft.Close()

	var leftRows []execution.Tuple
	for {
		tup, err := execLeft.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("execLeft Next error: %v", err)
		}
		leftRows = append(leftRows, tup)
	}

	// Should produce 4 rows: 3 items for Alice, and 1 for Bob with NULL product
	if len(leftRows) != 4 {
		t.Fatalf("expected 4 left join rows, got %d", len(leftRows))
	}

	foundBobNull := false
	for _, row := range leftRows {
		name := row.Get("name")
		product := row.Get("product")
		if name == "Bob" && product == nil {
			foundBobNull = true
		}
	}
	if !foundBobNull {
		t.Errorf("expected to find Bob with NULL product in LEFT JOIN result")
	}
}

func TestTuple_ToRow(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "test_tab",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeFloat},
		},
	}

	tuple := execution.NewTupleFromMap(map[string]any{
		"id":      int64(42),
		"name":    "Charlie",
		"balance": 123.45,
	}, []string{"id", "name", "balance"})

	row := tuple.ToRow(schema)
	if len(row.Values) != 3 {
		t.Fatalf("expected 3 row values, got %d", len(row.Values))
	}
	if row.Values[0].IVal != 42 || row.Values[0].Type != storage.TypeInt {
		t.Errorf("unexpected id value: %+v", row.Values[0])
	}
	if row.Values[1].SVal != "Charlie" || row.Values[1].Type != storage.TypeVarChar {
		t.Errorf("unexpected name value: %+v", row.Values[1])
	}
	if row.Values[2].FVal != 123.45 || row.Values[2].Type != storage.TypeFloat {
		t.Errorf("unexpected balance value: %+v", row.Values[2])
	}

	// Schema-less conversion
	rawRow := tuple.ToRow(nil)
	if len(rawRow.Values) != 3 {
		t.Fatalf("expected 3 raw row values, got %d", len(rawRow.Values))
	}
}

func TestVectorizedChunk_PipelineStreaming(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable error: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()
	tx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: false})

	const totalRows = 5000
	for i := 1; i <= totalRows; i++ {
		_ = tx.Insert("items", storage.Record{
			"id":  storage.IntValue(int64(i)),
			"val": storage.IntValue(int64(i * 2)),
		})
	}
	_ = tx.Commit()

	readTx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()

	// Pipeline: SeqScan -> Filter (val > 1000) -> Project (id, val) -> Limit 1500
	scan := execution.NewSeqScanIterator(readTx, "items", "items", schema)
	filter := execution.NewFilterIterator(scan, "val > 1000")
	proj := execution.NewProjectionIterator(filter, []string{"id", "val"})
	limit := execution.NewLimitIterator(proj, 1500, 0)

	ctx := context.Background()
	if err := limit.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer limit.Close()

	chunk := execution.NewChunk(execution.DefaultChunkSize)
	var fetchedCount int
	batchCount := 0

	for {
		err := execution.PullChunk(ctx, limit, chunk, execution.DefaultChunkSize)
		if err != nil && err != io.EOF {
			t.Fatalf("PullChunk failed: %v", err)
		}
		if chunk.Len() == 0 && err == io.EOF {
			break
		}
		fetchedCount += chunk.Len()
		batchCount++
		if err == io.EOF {
			break
		}
	}

	if fetchedCount != 1500 {
		t.Fatalf("expected 1500 rows, got %d", fetchedCount)
	}
	// With DefaultChunkSize = 1024, 1500 rows should arrive in 2 batches (1024 + 476)
	if batchCount != 2 {
		t.Fatalf("expected 2 chunk batches for 1500 rows, got %d", batchCount)
	}
}

func TestVectorizedChunk_Aggregation(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "sales",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "category", Type: storage.TypeVarChar},
			{Name: "amount", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable sales error: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()
	tx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: false})

	categories := []string{"Books", "Electronics", "Clothing"}
	for i := 1; i <= 3000; i++ {
		cat := categories[i%len(categories)]
		_ = tx.Insert("sales", storage.Record{
			"id":       storage.IntValue(int64(i)),
			"category": storage.StringValue(cat),
			"amount":   storage.IntValue(int64(i * 10)),
		})
	}
	_ = tx.Commit()

	readTx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()

	scan := execution.NewSeqScanIterator(readTx, "sales", "sales", schema)
	agg := execution.NewAggregateIterator(scan, []string{"category"}, []string{"category", "COUNT(*) AS total_count", "SUM(amount) AS total_amount"}, "")

	ctx := context.Background()
	if err := agg.Open(ctx); err != nil {
		t.Fatalf("Agg Open failed: %v", err)
	}
	defer agg.Close()

	chunk := execution.NewChunk(execution.DefaultChunkSize)
	err := execution.PullChunk(ctx, agg, chunk, execution.DefaultChunkSize)
	if err != nil && err != io.EOF {
		t.Fatalf("Agg PullChunk failed: %v", err)
	}

	if chunk.Len() != 3 {
		t.Fatalf("expected 3 aggregate groups, got %d", chunk.Len())
	}
}

func BenchmarkVectorizedChunkVsRowByRow(b *testing.B) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "bench_items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		b.Fatalf("CreateTable bench error: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()
	tx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: false})

	const N = 10000
	for i := 1; i <= N; i++ {
		_ = tx.Insert("bench_items", storage.Record{
			"id":  storage.IntValue(int64(i)),
			"val": storage.IntValue(int64(i)),
		})
	}
	_ = tx.Commit()

	readTx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()
	ctx := context.Background()

	b.Run("RowByRow_Next", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			scan := execution.NewSeqScanIterator(readTx, "bench_items", "bench_items", schema)
			filter := execution.NewFilterIterator(scan, "val > 100")
			proj := execution.NewProjectionIterator(filter, []string{"id", "val"})
			_ = proj.Open(ctx)
			count := 0
			for {
				_, err := proj.Next(ctx)
				if err == io.EOF {
					break
				}
				count++
			}
			_ = proj.Close()
		}
	})

	b.Run("VectorizedChunk_PullChunk", func(b *testing.B) {
		chunk := execution.NewChunk(execution.DefaultChunkSize)
		for i := 0; i < b.N; i++ {
			scan := execution.NewSeqScanIterator(readTx, "bench_items", "bench_items", schema)
			filter := execution.NewFilterIterator(scan, "val > 100")
			proj := execution.NewProjectionIterator(filter, []string{"id", "val"})
			_ = proj.Open(ctx)
			count := 0
			for {
				err := execution.PullChunk(ctx, proj, chunk, execution.DefaultChunkSize)
				if err != nil && err != io.EOF {
					b.Fatalf("PullChunk error: %v", err)
				}
				if chunk.Len() == 0 && err == io.EOF {
					break
				}
				count += chunk.Len()
				if err == io.EOF {
					break
				}
			}
			_ = proj.Close()
		}
	})
}

func TestVectorizedChunk_Pool(t *testing.T) {
	c1 := execution.GetChunk()
	if c1 == nil {
		t.Fatalf("GetChunk returned nil")
	}
	if c1.Len() != 0 {
		t.Fatalf("expected empty chunk, got len %d", c1.Len())
	}
	if cap(c1.Tuples) < execution.DefaultChunkSize {
		t.Fatalf("expected capacity >= %d, got %d", execution.DefaultChunkSize, cap(c1.Tuples))
	}

	// Append mock tuples
	c1.Tuples = append(c1.Tuples, execution.Tuple{
		Values: []any{"val1", 123},
	})
	if c1.Len() != 1 {
		t.Fatalf("expected len 1, got %d", c1.Len())
	}

	// Release back to pool
	execution.PutChunk(c1)

	// Acquire again
	c2 := execution.AcquireChunk()
	if c2 == nil {
		t.Fatalf("AcquireChunk returned nil")
	}
	if c2.Len() != 0 {
		t.Fatalf("expected cleared chunk from pool, got len %d", c2.Len())
	}
	execution.ReleaseChunk(c2)

	// Safe handling of nil
	execution.PutChunk(nil)
	execution.ReleaseChunk(nil)
}



