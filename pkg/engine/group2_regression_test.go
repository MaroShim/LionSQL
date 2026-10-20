package engine

import (
	"testing"
	"time"

	"sqlion/pkg/planner"
	"sqlion/pkg/storage"
)

// TestGroup2_AggCaseAndMinMaxNonNumeric tests BUG-SELECT-AGG-CASE-1 and BUG-COORD-AGG-MIN-MAX-NON-NUMERIC-NIL-1
func TestGroup2_AggCaseAndMinMaxNonNumeric(t *testing.T) {
	coord := &Coordinator{}
	rows := []map[string]any{
		{"category": "Fruit", "name": "Cherry", "price": 10},
		{"Category": "Fruit", "name": "Apple", "price": 20},
		{"category": "Fruit", "name": "Banana", "price": 10},
		{"CATEGORY": "Grain", "name": "Wheat", "price": 5},
	}

	qInfo := &planner.QueryInfo{
		LimitCount:  -1,
		GroupByCols: []string{"Category"},
		Projections: []string{
			"Category",
			"COUNT(name) AS total_names",
			"COUNT(DISTINCT price) AS unique_prices",
			"MIN(name) AS min_name",
			"MAX(name) AS max_name",
			"SUM(price) AS sum_price",
		},
	}

	res, err := coord.execAggregate(rows, qInfo, time.Now())
	if err != nil {
		t.Fatalf("execAggregate failed: %v", err)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 groups (Fruit, Grain), got %d", len(res.Rows))
	}

	// Verify Fruit group
	var fruitRow map[string]any
	for _, r := range res.Rows {
		sRow := NewSQLRow(r)
		if cat, _ := sRow.GetString("Category"); cat == "Fruit" {
			fruitRow = r
			break
		}
	}
	if fruitRow == nil {
		t.Fatalf("Fruit group missing in aggregation")
	}

	sFruit := NewSQLRow(fruitRow)
	// total_names should be 3
	if cnt, _ := sFruit.GetInt("total_names"); cnt != 3 {
		t.Errorf("expected total_names=3, got %d", cnt)
	}
	// unique_prices (prices are 10, 20, 10 -> distinct prices are 10, 20 -> count=2)
	if uCnt, _ := sFruit.GetInt("unique_prices"); uCnt != 2 {
		t.Errorf("expected unique_prices=2, got %d", uCnt)
	}
	// min_name (Apple, Banana, Cherry -> Apple) - must not be nil!
	if minName, _ := sFruit.GetString("min_name"); minName != "Apple" {
		t.Errorf("expected min_name='Apple', got '%s'", minName)
	}
	// max_name (Apple, Banana, Cherry -> Cherry) - must not be nil!
	if maxName, _ := sFruit.GetString("max_name"); maxName != "Cherry" {
		t.Errorf("expected max_name='Cherry', got '%s'", maxName)
	}
	// sum_price (10 + 20 + 10 = 40)
	if sumPrice, _ := sFruit.GetInt("sum_price"); sumPrice != 40 {
		t.Errorf("expected sum_price=40, got %d", sumPrice)
	}
}

// TestGroup2_CBOSpotCaseSensitivity tests BUG-PLANNER-CBO-SELECTIVITY-CASE-SENSITIVE-1
func TestGroup2_CBOSpotCaseSensitivity(t *testing.T) {
	ts := planner.NewTableStats("orders", 1000)
	vals := make([]storage.Value, 1000)
	for i := 0; i < 1000; i++ {
		vals[i] = storage.IntValue(int64(i + 1))
	}
	cs := planner.BuildColumnStats("Amount", storage.TypeInt, vals, 0, 1000)
	ts.Columns["Amount"] = cs

	// Exact match
	selExact := ts.EstimateSelectivity("Amount", "=", "50")
	if selExact >= 0.30 {
		t.Fatalf("expected specific selectivity for Amount, got fallback %f", selExact)
	}

	// Lowercase match
	selLower := ts.EstimateSelectivity("amount", "=", "50")
	if selLower >= 0.30 {
		t.Fatalf("expected case-insensitive selectivity for amount, got fallback %f", selLower)
	}

	if selExact != selLower {
		t.Fatalf("expected identical selectivity for Amount and amount, got %f vs %f", selExact, selLower)
	}
}
