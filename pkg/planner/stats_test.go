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

