package execution_test

import (
	"context"
	"io"
	"testing"

	"lionsql/pkg/execution"
	"lionsql/pkg/planner"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// TestGroup2_AggregateIteratorDistinct tests BUG-EXEC-AGG-DISTINCT-IGNORED-1
func TestGroup2_AggregateIteratorDistinct(t *testing.T) {
	child := newMockIterator(
		[]string{"dept", "salary"},
		map[string]any{"dept": "Eng", "salary": int64(100)},
		map[string]any{"dept": "Eng", "salary": int64(100)}, // duplicate
		map[string]any{"dept": "Eng", "salary": int64(200)},
		map[string]any{"dept": "Sales", "salary": int64(50)},
	)

	agg := execution.NewAggregateIterator(
		child,
		[]string{"dept"},
		[]string{
			"dept",
			"COUNT(salary) AS total_count",
			"COUNT(DISTINCT salary) AS distinct_count",
			"SUM(DISTINCT salary) AS distinct_sum",
		},
		"",
	)

	ctx := context.Background()
	if err := agg.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer agg.Close()

	var engTuple execution.Tuple
	foundEng := false
	for {
		tup, err := agg.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		if tup.Values[0] == "Eng" {
			engTuple = tup
			foundEng = true
		}
	}

	if !foundEng {
		t.Fatalf("expected Eng group in aggregation")
	}

	// Schema: ["dept", "total_count", "distinct_count", "distinct_sum"]
	// Eng total_count should be 3
	if engTuple.Values[1] != int64(3) {
		t.Errorf("expected total_count=3, got %v", engTuple.Values[1])
	}
	// Eng distinct_count should be 2 (100, 200)
	if engTuple.Values[2] != int64(2) {
		t.Errorf("expected distinct_count=2, got %v", engTuple.Values[2])
	}
	// Eng distinct_sum should be 300 (100 + 200)
	if engTuple.Values[3] != int64(300) {
		t.Errorf("expected distinct_sum=300, got %v", engTuple.Values[3])
	}
}

// TestGroup2_JoinIndexMismatchFallback tests BUG-JOIN-IDX-1
func TestGroup2_JoinIndexMismatchFallback(t *testing.T) {
	e := memengine.New()
	defer e.Close()
	sess, err := e.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	// Outer table: Orders (order_id, cust_id)
	ordersSchema := &storage.TableSchema{
		Name: "Orders",
		Columns: []storage.Column{
			{Name: "order_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "cust_id", Type: storage.TypeInt},
		},
	}
	_ = e.CreateTable(ordersSchema)

	// Inner table: Customers (id, name, email) with an index ONLY on "email" (unrelated to cust_id)
	custSchema := &storage.TableSchema{
		Name: "Customers",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "email", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_cust_email", Columns: []string{"email"}},
		},
	}
	_ = e.CreateTable(custSchema)

	// Build a plan where JoinCond is "cust_id = id"
	// Because idx_cust_email is on "email", not "id", the builder must NOT blindly pick idx_cust_email,
	// but fall back safely to HashJoinIterator!
	plan := &planner.PlanNode{
		Type:     planner.NodeNestedLoop,
		JoinType: "INNER",
		JoinCond: "cust_id = id",
		Children: []*planner.PlanNode{
			{Type: planner.NodeTableScan, Relation: "Orders"},
			{Type: planner.NodeTableScan, Relation: "Customers"},
		},
	}

	op, err := execution.BuildExecutor(plan, tx, func(name string) (*storage.TableSchema, error) {
		if name == "Orders" {
			return ordersSchema, nil
		}
		return custSchema, nil
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Verify that operator is HashJoinIterator, not IndexNestedLoopJoinIterator
	if _, isHashJoin := op.(*execution.HashJoinIterator); !isHashJoin {
		t.Fatalf("expected HashJoinIterator fallback when no index matches join key, got %T", op)
	}
}
