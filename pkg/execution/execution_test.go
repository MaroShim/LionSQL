package execution_test

import (
	"context"
	"fmt"
	"io"
	"testing"

	"sqlion/pkg/execution"
	"sqlion/pkg/planner"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
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
