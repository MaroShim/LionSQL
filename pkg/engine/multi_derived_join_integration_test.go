package engine_test

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestIntegration_ThreeTableMultiJoinDerivedTableExecution verifies that when 3+ tables
// (including multiple derived tables with different column counts) are joined,
// the actual execution engine generates and projects tuples with 100% correct data alignment.
func TestIntegration_ThreeTableMultiJoinDerivedTableExecution(t *testing.T) {
	ctx := context.Background()
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_derived_multijoin"
	coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	mustExec := func(sql string) *engine.QueryResult {
		t.Helper()
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, sql)
		if err != nil {
			t.Fatalf("failed to execute %q: %v", sql, err)
		}
		return res
	}

	mustExec("CREATE TABLE u_base (id INT PRIMARY KEY, name VARCHAR(50), age INT);")
	mustExec("CREATE TABLE o_orders (order_id INT PRIMARY KEY, u_id INT, amount INT);")
	mustExec("CREATE TABLE p_payments (pay_id INT PRIMARY KEY, o_id INT, status VARCHAR(20), fee INT);")

	mustExec("INSERT INTO u_base VALUES (1, 'Alice', 30), (2, 'Bob', 25);")
	mustExec("INSERT INTO o_orders VALUES (101, 1, 500), (102, 1, 300), (103, 2, 200);")
	mustExec("INSERT INTO p_payments VALUES (1001, 101, 'COMPLETED', 15), (1002, 102, 'COMPLETED', 10), (1003, 103, 'PENDING', 5);")

	// Join u_base (3 cols) with derived table d1 (2 cols) and derived table d2 (3 cols)
	query := `
SELECT 
    u.id, 
    u.name, 
    d1.total_spent, 
    d2.completed_fees
FROM u_base u
JOIN (
    SELECT u_id, SUM(amount) AS total_spent 
    FROM o_orders 
    GROUP BY u_id
) d1 ON u.id = d1.u_id
JOIN (
    SELECT o.u_id, p.status, SUM(p.fee) AS completed_fees
    FROM o_orders o
    JOIN p_payments p ON o.order_id = p.o_id
    WHERE p.status = 'COMPLETED'
    GROUP BY o.u_id, p.status
) d2 ON u.id = d2.u_id
ORDER BY u.id;
`
	res := mustExec(query)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row for completed fee joins, got %d rows: %+v", len(res.Rows), res.Rows)
	}

	row := res.Rows[0]
	// Check Alice: id=1, total_spent=800 (500+300), completed_fees=25 (15+10)
	if row["id"] != int64(1) {
		t.Errorf("expected u.id 1, got %v", row["id"])
	}
	if row["name"] != "Alice" {
		t.Errorf("expected u.name 'Alice', got %v", row["name"])
	}
	if row["total_spent"] != int64(800) {
		t.Errorf("expected d1.total_spent 800, got %v", row["total_spent"])
	}
	if row["completed_fees"] != int64(25) {
		t.Errorf("expected d2.completed_fees 25, got %v", row["completed_fees"])
	}
}
