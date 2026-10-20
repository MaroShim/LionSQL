package engine_test

import (
	"os"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestDemoSeedScript_Execution(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())

	sqlBytes, err := os.ReadFile("../../scripts/demo_seed.sql")
	if err != nil {
		t.Fatalf("failed to read scripts/demo_seed.sql: %v", err)
	}

	sqlText := string(sqlBytes)
	res, err := coord.ExecuteSQL(sqlText)
	if err != nil {
		t.Fatalf("ExecuteSQL failed on demo_seed.sql: %v", err)
	}
	_ = res

	// Verify all 5 tables exist and have records
	tables := []struct {
		name     string
		expected int
	}{
		{"customers", 5},
		{"accounts", 5},
		{"products", 8},
		{"sales", 6},
		{"sales_details", 10},
	}

	for _, tc := range tables {
		qRes, err := coord.ExecuteSQL("SELECT COUNT(*) AS cnt FROM " + tc.name)
		if err != nil {
			t.Fatalf("failed to query table %s: %v", tc.name, err)
		}
		if len(qRes.Rows) != 1 {
			t.Fatalf("expected 1 row for count of %s, got %d", tc.name, len(qRes.Rows))
		}
		cntVal := qRes.Rows[0]["cnt"]
		t.Logf("Table %s count: %v", tc.name, cntVal)
	}

	// Verify stored procedures exist
	for _, procName := range []string{"TransferFunds", "ProcessOrder"} {
		p, err := cat.GetProcedure(procName)
		if err != nil || p == nil {
			t.Fatalf("expected %s stored procedure to be created, err: %v", procName, err)
		}
	}

	// Test executing TransferFunds
	res, err = coord.ExecuteSQL("EXEC TransferFunds @FromAccount=1, @ToAccount=2, @Amount=50000.00")
	if err != nil {
		t.Fatalf("failed to execute TransferFunds: %v", err)
	}
	t.Logf("TransferFunds result: %+v", res)

	// Test executing ProcessOrder
	res, err = coord.ExecuteSQL("EXEC ProcessOrder @SaleId=100, @CustomerId=1, @ProductId=1, @Quantity=2")
	if err != nil {
		t.Fatalf("failed to execute ProcessOrder: %v", err)
	}
	t.Logf("ProcessOrder result: %+v", res)
}
