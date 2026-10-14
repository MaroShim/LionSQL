package planner_test

import (
	"testing"

	"sqlion/pkg/planner"
	"sqlion/pkg/storage"
)

func TestTableStats_SelectivityEstimation(t *testing.T) {
	ts := planner.NewTableStats("users", 1000)

	// Create 1000 IDs (1 to 1000) -> NDV = 1000
	idVals := make([]storage.Value, 1000)
	for i := 0; i < 1000; i++ {
		idVals[i] = storage.IntValue(int64(i + 1))
	}
	ts.Columns["id"] = planner.BuildColumnStats("id", storage.TypeInt, idVals, 0, 1000)

	// Equality selectivity for unique id: 1 / 1000 = 0.001
	selEq := ts.EstimateSelectivity("id", "=", "500")
	if selEq < 0.0009 || selEq > 0.0011 {
		t.Fatalf("expected equality selectivity ~0.001, got %f", selEq)
	}

	// Inequality selectivity: 1 - 0.001 = 0.999
	selNeq := ts.EstimateSelectivity("id", "!=", "500")
	if selNeq < 0.998 || selNeq > 1.0 {
		t.Fatalf("expected inequality selectivity ~0.999, got %f", selNeq)
	}

	// Range selectivity: id <= 250 -> ~0.25 (25% quantile)
	selRange25 := ts.EstimateSelectivity("id", "<=", "250")
	if selRange25 < 0.20 || selRange25 > 0.30 {
		t.Fatalf("expected range selectivity ~0.25, got %f", selRange25)
	}

	// Range selectivity: id > 750 -> ~0.25
	selRange75 := ts.EstimateSelectivity("id", ">", "750")
	if selRange75 < 0.20 || selRange75 > 0.30 {
		t.Fatalf("expected range selectivity ~0.25, got %f", selRange75)
	}
}

func TestTableStats_CategoricalNDV(t *testing.T) {
	ts := planner.NewTableStats("orders", 1000)

	// 5 distinct status codes: 'NEW', 'PAID', 'SHIPPED', 'DELIVERED', 'CANCELLED'
	statuses := []string{"NEW", "PAID", "SHIPPED", "DELIVERED", "CANCELLED"}
	vals := make([]storage.Value, 1000)
	for i := 0; i < 1000; i++ {
		vals[i] = storage.StringValue(statuses[i%5])
	}
	ts.Columns["status"] = planner.BuildColumnStats("status", storage.TypeVarChar, vals, 0, 1000)

	if ts.Columns["status"].DistinctCount != 5 {
		t.Fatalf("expected NDV 5, got %d", ts.Columns["status"].DistinctCount)
	}

	// Equality selectivity: 1 / 5 = 0.20
	sel := ts.EstimateSelectivity("status", "=", "PAID")
	if sel < 0.19 || sel > 0.21 {
		t.Fatalf("expected status selectivity ~0.20, got %f", sel)
	}
}

func TestTableStats_EstimateCompoundSelectivity(t *testing.T) {
	ts := planner.NewTableStats("accounts", 1000)

	idVals := make([]storage.Value, 1000)
	for i := 0; i < 1000; i++ {
		idVals[i] = storage.IntValue(int64(i + 1))
	}
	ts.Columns["id"] = planner.BuildColumnStats("id", storage.TypeInt, idVals, 0, 1000)

	statuses := []string{"ACTIVE", "BLOCKED"}
	statVals := make([]storage.Value, 1000)
	for i := 0; i < 1000; i++ {
		statVals[i] = storage.StringValue(statuses[i%2])
	}
	ts.Columns["status"] = planner.BuildColumnStats("status", storage.TypeVarChar, statVals, 0, 1000)

	// id <= 500 (sel ~0.50) AND status = 'ACTIVE' (sel 0.50)
	// Compound selectivity = 0.50 * 0.50 = 0.25
	conds := []string{"id <= 500", "status = 'ACTIVE'"}
	compSel := ts.EstimateCompoundSelectivity(conds)
	if compSel < 0.20 || compSel > 0.30 {
		t.Fatalf("expected compound selectivity ~0.25, got %f", compSel)
	}
}

func TestTableStats_BooleanTreeAndORSelectivity(t *testing.T) {
	ts := planner.NewTableStats("orders", 1000)

	idVals := make([]storage.Value, 1000)
	for i := 0; i < 1000; i++ {
		idVals[i] = storage.IntValue(int64(i + 1))
	}
	ts.Columns["id"] = planner.BuildColumnStats("id", storage.TypeInt, idVals, 0, 1000)

	// 4 statuses with 25% each: 'NEW', 'PAID', 'SHIPPED', 'CANCELLED'
	statuses := []string{"NEW", "PAID", "SHIPPED", "CANCELLED"}
	statVals := make([]storage.Value, 1000)
	for i := 0; i < 1000; i++ {
		statVals[i] = storage.StringValue(statuses[i%4])
	}
	ts.Columns["status"] = planner.BuildColumnStats("status", storage.TypeVarChar, statVals, 0, 1000)

	// 1. Same-column OR equality (mutually exclusive disjoint events):
	// P(status = 'NEW' OR status = 'PAID') = 0.25 + 0.25 = 0.50
	selSameColOR := ts.EstimateCompoundSelectivity([]string{"status = 'NEW' OR status = 'PAID'"})
	if selSameColOR < 0.45 || selSameColOR > 0.55 {
		t.Errorf("expected same-column OR selectivity ~0.50, got %f", selSameColOR)
	}

	// 2. Multi-column independent OR union:
	// P(status = 'PAID' OR id <= 250) = 0.25 + 0.25 - (0.25 * 0.25) = 0.50 - 0.0625 = 0.4375
	selMultiColOR := ts.EstimateCompoundSelectivity([]string{"status = 'PAID' OR id <= 250"})
	if selMultiColOR < 0.40 || selMultiColOR > 0.48 {
		t.Errorf("expected independent OR selectivity ~0.4375, got %f", selMultiColOR)
	}

	// 3. NOT logic:
	// P(NOT (status = 'CANCELLED')) = 1.0 - 0.25 = 0.75
	selNot := ts.EstimateCompoundSelectivity([]string{"NOT (status = 'CANCELLED')"})
	if selNot < 0.70 || selNot > 0.80 {
		t.Errorf("expected NOT selectivity ~0.75, got %f", selNot)
	}

	// 4. IN predicate:
	// id IN (10, 20, 30, 40, 50) -> 5 / 1000 = 0.005
	selIn := ts.EstimateCompoundSelectivity([]string{"id IN (10, 20, 30, 40, 50)"})
	if selIn < 0.004 || selIn > 0.006 {
		t.Errorf("expected IN predicate selectivity ~0.005, got %f", selIn)
	}

	// 5. BETWEEN predicate:
	// id BETWEEN 200 AND 600 -> (600 - 200)/1000 = 0.40
	selBetween := ts.EstimateCompoundSelectivity([]string{"id BETWEEN 200 AND 600"})
	if selBetween < 0.35 || selBetween > 0.45 {
		t.Errorf("expected BETWEEN selectivity ~0.40, got %f", selBetween)
	}

	// 6. Complex nested boolean tree:
	// (status = 'NEW' OR status = 'PAID') AND id <= 500
	// = 0.50 * 0.50 = 0.25
	selComplex := ts.EstimateCompoundSelectivity([]string{"(status = 'NEW' OR status = 'PAID') AND id <= 500"})
	if selComplex < 0.20 || selComplex > 0.30 {
		t.Errorf("expected complex boolean tree selectivity ~0.25, got %f", selComplex)
	}
}


